package graphcache

import (
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// neighborParallelThreshold is the minimum frontier size that triggers
// goroutine fan-out in neighborContext. Smaller frontiers are processed
// sequentially since goroutine startup + mu.Lock round-trips dominate
// the actual sort work for typical degrees.
const neighborParallelThreshold = 8

type GraphCache[S comparable, T any] struct {
	mu         sync.RWMutex
	defaultTTL time.Duration
	vertices   *cache.Cache[S, T]
	edges      *edgeCache[S]
	// applicationClock is sampled only while the final aggregate write lock is
	// held by outcome-bearing Put batches. Nil selects time.Now. Tests replace
	// it to pin expiry-boundary behavior without sleeping.
	applicationClock func() time.Time
	// dict is the shared vertex-id allocator. Both the vertex cache and the
	// edge cache reference each key through this dictionary so the heavy
	// edge maps can be keyed by uint32 instead of S. The vertex cache holds
	// one reference per live entry (released via the SetOnEvictMany batch
	// eviction hook); the edge cache holds one reference per endpoint per edge.
	dict *dictionary[S]

	// GC observability hooks. Both are optional; the cache stays metrics-free
	// by default. The server wires Prometheus collectors via SetGCHooks so
	// reporting lives outside core.
	//
	// onExpire is invoked once per Watch tick per kind ("vertex" | "edge" |
	// "dangling_edge") with the number of entries the tick removed.
	// onGCDuration is invoked once per Watch tick with the wall-clock time
	// spent inside the tick.
	hookMu       sync.RWMutex
	onExpire     func(kind string, n int)
	onGCDuration func(d time.Duration)

	// prefixIndex is an optional secondary index keyed by the string
	// projection of S (see EnablePrefixIndex). When non-nil it is kept in
	// sync with the vertex cache under c.mu so prefix scans return a
	// view consistent with point reads. When nil the put / evict paths
	// pay no extra cost beyond a single nil check.
	prefixIndex   *radix
	prefixExtract func(S) string

	// headByTail is the per-tail head-side prefix index that backs the
	// head dimension of ScanEdgesByPrefix (Issue #167). It is allocated
	// alongside prefixIndex by EnablePrefixIndex and maintained in
	// lockstep with edge mutations under c.mu.Lock — every AddEdge /
	// PutEdge / DeleteEdge code path (single and batch) that touches the
	// edge map also touches the matching headIndex entry, so the two
	// structures never disagree from a reader's perspective.
	//
	// Set to nil by disableHeadIndexForTesting to force ScanEdgesByPrefix
	// onto the v1 materialise-and-sort fallback for regression tests and
	// before/after benchmarks. Production code never disables it.
	headByTail map[vertexID]*headIndex

	// searchIndex is an optional secondary inverted index (see
	// EnableSearchIndex) that ranks live vertices by how well their projected
	// key and value fields match a query (BM25). Like prefixIndex it is opt-in
	// and maintained under c.mu, but it differs in one way: it is refreshed
	// on EVERY put, not just the first insert, because a vertex's value —
	// and therefore its postings — changes on overwrite. Entries are dropped
	// from the index by the shared vertices.SetOnEvict hook, so they decay
	// for non-TTL eviction by the shared vertices.SetOnEvict hook. TTL entries
	// are removed from search at query time using the index's expiration heap;
	// the later cache Flush hook is an idempotent delete. When nil the put /
	// evict paths pay only a single nil check.
	searchIndex   *search.InvertedIndex[S, search.Document]
	searchExtract func(S, T) search.Document
	// searchCommitMu makes a prepared vertex batch visible to Search as one
	// transition across both the vertex store and inverted index. Searches hold
	// RLock across the same bounded execution interval that takes the index read
	// lock; batch analysis is complete before the writer takes Lock.
	searchCommitMu sync.RWMutex
	// searchPreappliedEvictions suppresses the search-index portion of the
	// synchronous vertex eviction hook when a prepared batch has already
	// applied the same deletion through IndexManyPrepared. Dictionary and
	// prefix cleanup still run normally. The separate mutex is required because
	// eviction hooks may fire without GraphCache.mu.
	searchEvictionMu          sync.Mutex
	searchPreappliedEvictions map[S]int

	// vertexHLC tracks the last HLC accepted by PutVertexWithExpirationHLC
	// (the LWW replication apply path; #182). It is only populated when
	// a replicated write touches a vertex; the local non-replicated path
	// never reads or writes this map, so the steady-state cost is one
	// nil check per Put. The map is bounded by the set of currently live
	// replicated vertices — stale entries are swept inside flush() under
	// c.mu via sweepStaleVertexHLCLocked, which reconciles the map
	// against the vertex cache after every GC tick (issue #700).
	vertexHLC map[S]hlc.Timestamp

	// vertexHLCHighWater records the largest len(vertexHLC) ever observed at
	// the start of a sweep (sweepStaleVertexHLCLocked) — i.e. the per-GC-cycle
	// peak before stale watermarks are drained. It is monotonic non-decreasing
	// for the life of the cache and is the authoritative churn-peak signal for
	// the born-expired LWW load behind the ttl_churn release-bench leak gate
	// (#700/#719/#727). It used to also size the map's retained heap (Go never
	// shrinks a map after delete), but the sweep now reallocates vertexHLC after
	// a large drain (see vertexHLCShrinkFloor), so this is a historical peak for
	// observability, not a live heap-size proxy. Guarded by c.mu like vertexHLC.
	vertexHLCHighWater int

	// vertexTombstones / edgeTombstones are the per-key deletion records
	// produced by Delete*HLC (#183). They live outside the live cache so
	// reads never accidentally surface a deleted key, and they fence
	// late replication Add*/Put* with strictly-older HLC from
	// resurrecting freshly-deleted data. Tombstones are reaped on the
	// regular GC tick (sweepExpiredTombstonesLocked); steady-state cost
	// for non-replicated workloads is one nil check per write.
	vertexTombstones map[S]tombstoneEntry
	edgeTombstones   map[EdgeKey[S]]tombstoneEntry

	// vertexCausalBarriers / edgeCausalBarriers retain the HLC of an
	// accepted Put whose absolute expiration was already dead at the final
	// application sample. Such a Put is a delete-like LWW overwrite: it must
	// remove the previous value and continue to fence strictly-older replicas
	// even though there is deliberately no live cache entry to carry the HLC.
	//
	// Unlike delete tombstones these barriers have no wall-clock retention
	// deadline. Reaping one merely because GC removed an expired bucket would
	// let a delayed older Put resurrect data after clock rollback or snapshot
	// bootstrap. They are allocated only on the replicated/HLC path, so the
	// non-replicated hot path still pays a single nil check in its write helper.
	// Replication snapshots export them separately from the live graph.
	vertexCausalBarriers map[S]hlc.Timestamp
	edgeCausalBarriers   map[EdgeKey[S]]hlc.Timestamp

	// gcEdgeBudget bounds the incremental GC edge sweep (#744). When it is
	// <= 0 (the default) flush() performs a full O(E) sweep every tick — the
	// historical behavior and the safety net. When set to a positive N via
	// SetGCEdgeBudget, flush() walks at most N tail buckets per tick, carrying
	// gcSweepPlan / gcSweepPos across ticks so every tail present when a cycle
	// began is visited exactly once before the plan is rebuilt. All three are
	// guarded by c.mu: flush mutates them under Lock; GCSweepBacklog reads them
	// under RLock.
	//
	// Deferring physical edge reclamation never surfaces dead data through
	// point reads: fully-decayed (zero-weight) edges are already hidden by the
	// edge read path regardless of sweep timing, and dangling-edge logical
	// visibility is tracked separately (see #744). The plan holds vertexIDs,
	// not edges, so a delayed sweep re-resolves liveness at sweep time — an ID
	// reused between plan build and visit is evaluated against current state.
	gcEdgeBudget int
	gcSweepPlan  []vertexID
	gcSweepPos   int
}

func (c *GraphCache[S, T]) applicationTime() time.Time {
	if c.applicationClock != nil {
		return c.applicationClock()
	}
	return time.Now()
}

func NewGraphCache[S comparable, T any](defaultTTL time.Duration) *GraphCache[S, T] {
	dict := newDictionary[S]()
	vertices := cache.NewCache[S, T](defaultTTL)
	c := &GraphCache[S, T]{
		defaultTTL: defaultTTL,
		vertices:   vertices,
		edges:      newEdgeCache[S](defaultTTL, dict),
		dict:       dict,
	}
	vertices.SetOnEvictMany(c.onVerticesEvicted)
	return c
}

// EnablePrefixIndex turns on the optional prefix index, projecting each
// key S through extract to obtain the string used by ScanByPrefix /
// CountByPrefix / DeleteByPrefix. It must be called before any vertex is
// stored; calling it on a non-empty cache panics so the caller cannot
// silently observe an index that disagrees with point reads. extract
// must not be nil.
//
// EnablePrefixIndex is intentionally separate from the constructor:
//   - GraphCache is generic over S comparable, but prefix semantics are
//     well-defined only for string-like keys. Lifting the constraint
//     into the signature would force every caller (including those that
//     never want prefix scans) to thread a projection through;
//   - opt-in keeps the put / evict hot paths free of any radix work for
//     the existing string-keyed deployments that have not migrated.
func (c *GraphCache[S, T]) EnablePrefixIndex(extract func(S) string) {
	if extract == nil {
		panic("graph: EnablePrefixIndex extract must not be nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prefixIndex != nil {
		return // idempotent: re-enabling with the same intent is fine
	}
	if c.vertices.Count() != 0 {
		panic("graph: EnablePrefixIndex must be called before any vertex is stored")
	}
	c.prefixExtract = extract
	c.prefixIndex = newRadix()
	c.headByTail = make(map[vertexID]*headIndex)
}

// putVertexLocked inserts (or refreshes) a vertex entry with its search
// document analyzed inline (under c.mu). It backs the replicated apply path
// (PutVertexWithExpirationHLC) and the inline-analysis fallback for single
// local writes. Prepared batch writers bypass it after applying their compact
// mutations once through IndexManyPrepared. Caller must hold c.mu.
func (c *GraphCache[S, T]) putVertexLocked(key S, value T, expiration time.Time) {
	c.upsertVertexLocked(key, value, expiration)
}

// upsertVertexLocked stores/refreshes the vertex and updates its secondary
// indexes in a single inner-cache lock cycle. physicallyExisted (true when a
// slot for key was present even if already expired) drives the once-per-slot
// side effects — dictionary interning and prefix insertion — so an
// expired-but-not-yet-flushed overwrite neither re-interns the key (which would
// inflate its dictionary refcount past the single live slot) nor re-inserts it
// into the prefix index. The singular path analyzes the document inline;
// prepared batch paths call upsertVertexStorageLocked directly after their one
// IndexManyPrepared mutation. Caller must hold c.mu (#739, #1051).
func (c *GraphCache[S, T]) upsertVertexLocked(key S, value T, expiration time.Time) {
	c.upsertVertexStorageLocked(key, value, expiration)
	if c.searchIndex != nil {
		if err := c.searchIndex.IndexWithExpiration(key, c.searchExtract(key, value), expiration); err != nil {
			c.searchIndex.MarkIncomplete()
		}
	}
}

func (c *GraphCache[S, T]) upsertVertexStorageLocked(key S, value T, expiration time.Time) {
	physicallyExisted := c.vertices.UpsertWithExpiration(key, value, expiration)
	if !physicallyExisted {
		if c.dict != nil {
			c.dict.intern(key)
		}
		c.insertVertexPrefixLocked(key)
	}
}

// putLocalVertexLocked is the client-write entry that backs
// PutVertexWithExpiration and the PutVerticesWithExpiration batch. It treats a
// dead-on-arrival write — one whose expiration is already in the past — as an
// expiry of the key rather than an insert. Storing an already-expired vertex
// has no observable effect (GetVertex hides expired entries via
// cache.IsLiveAt) and would only occupy a cache slot plus prefix- and
// search-index postings until the next GC sweep, inflating the map high-water
// under a churn of short-lived writes (#698). A born-expired write therefore
// deletes any existing entry for the key and stores nothing.
//
// The replicated singular path (PutVertexWithExpirationHLC) has its own
// causality wrapper. The plural HLC outcome path uses
// putLocalVertexLockedAtMode with its final application sample, then records
// the accepted HLC watermark even for a delete-like expired overwrite. Caller
// must hold c.mu.
func (c *GraphCache[S, T]) putLocalVertexLocked(key S, value T, expiration time.Time) bool {
	return c.putLocalVertexLockedAt(key, value, expiration, time.Now())
}

// putLocalVertexLockedAt is putLocalVertexLocked with the liveness clock
// supplied by the caller. Caller must hold c.mu.
//
// It returns stored=false when the write was discarded because its expiration
// was already past (dead on arrival), and true when the value was upserted.
// The if-absent write paths use this to count only writes that actually landed
// so a born-expired item is reported as neither written nor skipped (#918);
// unconditional callers may ignore the return.
func (c *GraphCache[S, T]) putLocalVertexLockedAt(key S, value T, expiration, now time.Time) (stored bool) {
	return c.putLocalVertexLockedAtMode(key, value, expiration, now, true)
}

func (c *GraphCache[S, T]) putLocalVertexLockedAtMode(key S, value T, expiration, now time.Time, updateSearch bool) (stored bool) {
	if !cache.IsLiveAt(expiration, now) {
		// Dead on arrival. Delete is a cheap map-miss when the key is absent
		// (the common churn case) and fires the eviction hook — releasing the
		// dictionary reference and dropping prefix/search postings — when it is
		// present, so an overwrite-to-expired still takes effect.
		if updateSearch {
			c.vertices.Delete(key)
		} else {
			c.deleteVertexStoragePreindexedLocked(key)
		}
		return false
	}
	if updateSearch {
		c.upsertVertexLocked(key, value, expiration)
	} else {
		c.upsertVertexStorageLocked(key, value, expiration)
	}
	return true
}

// ensureVertexLocked auto-creates an endpoint vertex (used by edge writes)
// without overwriting an existing value. The dict reference is taken only
// on the first insertion so refcount tracks the cache contents 1:1.
func (c *GraphCache[S, T]) ensureVertexLocked(key S, expiration time.Time) {
	if c.vertices.Has(key) {
		return
	}
	var value T
	physicallyExisted := c.vertices.UpsertWithExpiration(key, value, expiration)
	if !physicallyExisted && c.dict != nil {
		c.dict.intern(key)
	}
	c.onEndpointVertexCreatedLocked(key, expiration, !physicallyExisted)
}

// GetVertex returns the live value stored for key. It is a point read that
// does not take GraphCache.mu: the inner vertex cache has its own RWMutex and
// hides expired entries, so a point lookup needs no aggregate-lock
// consistency with the prefix/search/edge indexes. Dropping c.mu here keeps
// hot reads from serializing behind unrelated writes, long scans, and GC
// (#740). A read may race a concurrent Put/Delete on the same key and observe
// either the pre- or post-write state, which is the existing point-read
// contract.
func (c *GraphCache[S, T]) GetVertex(key S) (T, bool) {
	return c.vertices.Get(key)
}

// edgeEndpointsLive reports whether BOTH endpoint vertices of an edge are
// currently live — present in the vertex cache and not past their TTL. It is
// the single enforcement point for the read/snapshot referential-closure
// contract (#750): an edge may physically outlive a deleted or expired endpoint
// vertex until the dangling-edge GC sweep reclaims it, but no public read,
// scan, traversal, or snapshot surface may expose such an edge. vertices.Has
// hides expired-but-not-yet-flushed entries (it routes through Get), so the
// check is correct even before GC runs.
//
// The vertex cache carries its own mutex, independent of GraphCache.mu, so this
// is safe to call both from the lock-free point reads (GetWeight/GetEdgeDetail,
// which hold no GraphCache.mu) and from the scan/traversal/snapshot paths that
// hold GraphCache.mu (R or W). It is the same liveness primitive the additive
// write fast path already uses to gate an in-place contribution (see
// tryAddExistingEdgeContrib).
func (c *GraphCache[S, T]) edgeEndpointsLive(tail, head S) bool {
	return c.vertices.Has(tail) && c.vertices.Has(head)
}

// GetWeight returns the additive weight of the (tail, head) edge. Like
// GetVertex it is a lock-free point read: edges.get pins both endpoint ids
// (closing the dictionary ABA hazard) and reads the edge map under the
// edgeCache's own lock, so GraphCache.mu is not required (#740).
//
// The edge is hidden (ok=false) unless both endpoint vertices are still live,
// so a dangling edge whose tail or head was deleted or expired but not yet
// swept is reported as absent (#750). The endpoint check reads the vertex
// cache under its own lock, after the edge read, so GetWeight stays free of
// GraphCache.mu.
func (c *GraphCache[S, T]) GetWeight(tail, head S) (float32, bool) {
	w, ok := c.edges.get(tail, head)
	if !ok || !c.edgeEndpointsLive(tail, head) {
		return 0, false
	}
	return w, true
}

// GetEdgeDetail returns the current edge weight together with the latest
// contribution expiration. The expiration is the moment after which the edge
// is guaranteed to have decayed to zero. When no edge exists, ok is false.
//
// Like GetWeight it is a lock-free point read (see edges.getDetail): it does
// not take GraphCache.mu and may observe either the pre- or post-write state
// under a concurrent edge mutation, but it never resolves a recycled endpoint
// id to an unrelated edge. The edge is hidden unless both endpoint vertices are
// still live, so a dangling edge to a deleted or expired-but-not-flushed vertex
// is reported as absent (#750).
func (c *GraphCache[S, T]) GetEdgeDetail(tail, head S) (float32, time.Time, bool) {
	w, exp, ok := c.edges.getDetail(tail, head)
	if !ok || !c.edgeEndpointsLive(tail, head) {
		return 0, time.Time{}, false
	}
	return w, exp, true
}

func (c *GraphCache[S, T]) PutVertexWithExpiration(key S, value T, expiration time.Time) error {
	return c.PutVerticesWithExpirationChecked([]VertexItem[S, T]{{Key: key, Value: value, Expiration: expiration}})
}

// PutVertexWithExpirationIfAbsent writes the vertex only when no live vertex
// exists at key (SET NX, #896). It returns true when the write was applied and
// false when it was skipped — either because an existing live vertex blocked it
// (the stored value and expiration are left untouched) or because the vertex was
// born expired and discarded (#918). Liveness follows the live-visibility rule
// (#750): an expired-but-uncollected vertex does not block the write. The
// existence check and the store happen under the same write lock, so two racing
// if-absent writers cannot both observe "absent" and both write.
func (c *GraphCache[S, T]) PutVertexWithExpirationIfAbsent(key S, value T, expiration time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}

	if c.vertices.Has(key) {
		return false
	}
	return c.putLocalVertexLocked(key, value, expiration)
}

func (c *GraphCache[S, T]) PutVertexWithTTL(key S, value T, ttl time.Duration) error {
	return c.PutVertexWithExpiration(key, value, time.Now().Add(ttl))
}

func (c *GraphCache[S, T]) PutVertex(key S, value T) error {
	return c.PutVertexWithTTL(key, value, c.defaultTTL)
}

func (c *GraphCache[S, T]) AddEdgeWithExpiration(tail, head S, w float32, expiration time.Time) {
	now := time.Now()
	if _, _, ok := c.tryAddExistingEdgeContrib(tail, head, w, expiration, ContribID{}, now); ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addEdgeLocked(tail, head, w, expiration)
}

func (c *GraphCache[S, T]) AddEdgeWithTTL(tail, head S, w float32, ttl time.Duration) {
	c.AddEdgeWithExpiration(tail, head, w, time.Now().Add(ttl))
}

func (c *GraphCache[S, T]) AddEdge(tail, head S, w float32) {
	c.AddEdgeWithTTL(tail, head, w, c.defaultTTL)
}

// PutEdgeWithExpiration atomically replaces the (tail, head) edge weight.
// AddEdgeWithExpiration is additive (Add semantics) so a naive
// "DeleteEdge + AddEdgeWithExpiration" sequence performed by callers
// exposes a window in which concurrent GetEdge readers observe a spurious
// NotFound. PutEdgeWithExpiration takes the write lock once and performs
// the delete + add under the same lock, restoring atomicity for the
// idempotent Put semantics.
func (c *GraphCache[S, T]) PutEdgeWithExpiration(tail, head S, w float32, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putEdgeLockedAt(tail, head, w, expiration, c.applicationTime())
}

// AddEdgeWithExpirationContrib is the dedup-aware additive write used by
// the replication apply path (#182). A non-zero contribID makes the call
// idempotent: re-applying the same mutation (e.g. on peer reconnect or
// duplicate stream delivery) leaves the stored weight unchanged. A zero
// contribID falls through to ordinary additive semantics, which is the
// local non-replicated path. Returns applied=true when the contribution
// was recorded; false means dedup suppressed an already-stored
// contribution with the same ID.
func (c *GraphCache[S, T]) AddEdgeWithExpirationContrib(tail, head S, w float32, expiration time.Time, contribID ContribID) bool {
	now := time.Now()
	if applied, _, ok := c.tryAddExistingEdgeContrib(tail, head, w, expiration, contribID, now); ok {
		return applied
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	applied, _ := c.addEdgeContribLocked(tail, head, w, expiration, contribID, now)
	return applied
}

// DeleteVertex removes the vertex (by key) and returns whether it was present.
func (c *GraphCache[S, T]) DeleteVertex(key S) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}

	deleted := c.vertices.Delete(key)
	c.clearVertexCausalBarrierLocked(key)
	if c.vertexHLC != nil {
		delete(c.vertexHLC, key)
	}
	c.rebuildIncompleteSearchLocked()
	return deleted
}

// DeleteEdge removes the (tail, head) edge and returns whether it was present.
func (c *GraphCache[S, T]) DeleteEdge(tail, head S) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	deleted := c.deleteEdgeLocked(tail, head)
	c.clearEdgeCausalBarrierLocked(tail, head)
	return deleted
}
