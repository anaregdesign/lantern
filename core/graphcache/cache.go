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
	// dict is the shared vertex-id allocator. Both the vertex cache and the
	// edge cache reference each key through this dictionary so the heavy
	// edge maps can be keyed by uint32 instead of S. The vertex cache holds
	// one reference per live entry (released via SetOnEvict); the edge cache
	// holds one reference per endpoint per edge.
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
	// EnableSearchIndex) that ranks live vertices by how well their
	// projected value matches a query (BM25). Like prefixIndex it is opt-in
	// and maintained under c.mu, but it differs in one way: it is refreshed
	// on EVERY put, not just the first insert, because a vertex's value —
	// and therefore its postings — changes on overwrite. Entries are dropped
	// from the index by the shared vertices.SetOnEvict hook, so they decay
	// in lockstep with the vertices they describe (Delete, Clear, and TTL
	// Flush all route through that hook). When nil the put / evict paths pay
	// only a single nil check.
	searchIndex   *search.InvertedIndex[S, search.Document]
	searchExtract func(T) search.Document

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

func NewGraphCache[S comparable, T any](defaultTTL time.Duration) *GraphCache[S, T] {
	dict := newDictionary[S]()
	vertices := cache.NewCache[S, T](defaultTTL)
	c := &GraphCache[S, T]{
		defaultTTL: defaultTTL,
		vertices:   vertices,
		edges:      newEdgeCache[S](defaultTTL, dict),
		dict:       dict,
	}
	vertices.SetOnEvict(c.onVertexEvicted)
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

// putVertexLocked inserts (or refreshes) a vertex entry, interning the key
// in the dictionary exactly once per net live entry. Caller must hold c.mu.
func (c *GraphCache[S, T]) putVertexLocked(key S, value T, expiration time.Time) {
	firstInsert := !c.vertices.Has(key)
	if c.dict != nil && firstInsert {
		c.dict.intern(key)
	}
	c.vertices.PutWithExpiration(key, value, expiration)
	c.onExplicitVertexStoredLocked(key, value, firstInsert)
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
// The replicated apply path (PutVertexWithExpirationHLC) deliberately does NOT
// route through here: it must preserve LWW/HLC/tombstone causality and records
// a per-key watermark after the store, so it keeps calling putVertexLocked
// directly. Caller must hold c.mu.
func (c *GraphCache[S, T]) putLocalVertexLocked(key S, value T, expiration time.Time) {
	if !cache.IsLiveAt(expiration, time.Now()) {
		// Dead on arrival. Delete is a cheap map-miss when the key is absent
		// (the common churn case) and fires the eviction hook — releasing the
		// dictionary reference and dropping prefix/search postings — when it is
		// present, so an overwrite-to-expired still takes effect.
		c.vertices.Delete(key)
		return
	}
	c.putVertexLocked(key, value, expiration)
}

// ensureVertexLocked auto-creates an endpoint vertex (used by edge writes)
// without overwriting an existing value. The dict reference is taken only
// on the first insertion so refcount tracks the cache contents 1:1.
func (c *GraphCache[S, T]) ensureVertexLocked(key S, expiration time.Time) {
	if c.vertices.Has(key) {
		return
	}
	if c.dict != nil {
		c.dict.intern(key)
	}
	var noop T
	c.vertices.PutWithExpiration(key, noop, expiration)
	c.onEndpointVertexCreatedLocked(key)
}

func (c *GraphCache[S, T]) GetVertex(key S) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.vertices.Get(key)
}

func (c *GraphCache[S, T]) GetWeight(tail, head S) (float32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.edges.get(tail, head)
}

// GetEdgeDetail returns the current edge weight together with the latest
// contribution expiration. The expiration is the moment after which the edge
// is guaranteed to have decayed to zero. When no edge exists, ok is false.
func (c *GraphCache[S, T]) GetEdgeDetail(tail, head S) (float32, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.edges.getDetail(tail, head)
}

func (c *GraphCache[S, T]) PutVertexWithExpiration(key S, value T, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.putLocalVertexLocked(key, value, expiration)
}

func (c *GraphCache[S, T]) PutVertexWithTTL(key S, value T, ttl time.Duration) {
	c.PutVertexWithExpiration(key, value, time.Now().Add(ttl))
}

func (c *GraphCache[S, T]) PutVertex(key S, value T) {
	c.PutVertexWithTTL(key, value, c.defaultTTL)
}

func (c *GraphCache[S, T]) AddEdgeWithExpiration(tail, head S, w float32, expiration time.Time) {
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
	c.putEdgeLocked(tail, head, w, expiration)
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
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addEdgeContribLocked(tail, head, w, expiration, contribID)
}

// DeleteVertex removes the vertex (by key) and returns whether it was present.
func (c *GraphCache[S, T]) DeleteVertex(key S) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.vertices.Delete(key)
}

// DeleteEdge removes the (tail, head) edge and returns whether it was present.
func (c *GraphCache[S, T]) DeleteEdge(tail, head S) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.deleteEdgeLocked(tail, head)
}
