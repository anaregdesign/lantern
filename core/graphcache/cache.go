package graphcache

import (
	"context"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/collection/pq"
	"github.com/anaregdesign/lantern/core/graph"
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
	// Vertex eviction (Delete, Clear, or Flush) must release the vertex
	// cache's one dictionary reference per live key AND drop the key from
	// the optional prefix index. The callback fires AFTER the inner cache
	// lock is released (see cache.Cache.SetOnEvict). When invoked via the
	// outer GraphCache write paths (DeleteVertex, etc.) we are still
	// holding c.mu.Lock, which is exactly the ordering radix.mu and
	// dict.mu expect — neither lock calls back into GraphCache.
	vertices.SetOnEvict(func(key S) {
		if id, ok := dict.lookup(key); ok {
			dict.release(id)
		}
		if idx := c.prefixIndex; idx != nil {
			idx.delete(c.prefixExtract(key))
		}
		// One hook covers Delete, Clear, and the TTL Flush GC tick, so the
		// search index decays in lockstep with the vertices it describes.
		if idx := c.searchIndex; idx != nil {
			idx.Delete(key)
		}
	})
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
	if firstInsert && c.prefixIndex != nil {
		c.prefixIndex.insert(c.prefixExtract(key))
	}
	// Unlike the prefix index, the search index is refreshed on EVERY put:
	// an overwrite changes the value and therefore the postings, so the
	// index must re-extract even when the key already existed. Index doubles
	// as update (it drops stale postings first), and projecting a valueless
	// entry to an empty Document is a no-op, so this is safe for every value.
	if c.searchIndex != nil {
		c.searchIndex.Index(key, c.searchExtract(value))
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
	if c.prefixIndex != nil {
		c.prefixIndex.insert(c.prefixExtract(key))
	}
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
	// Auto-create endpoint vertices synchronously so that a subsequent flush
	// cannot race ahead and drop the edge we are about to add. The inner
	// vertex cache has its own mutex, so calling it while holding c.mu is safe.
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID := c.edges.addWithExpiration(tail, head, w, expiration)
	c.onEdgeAddedLocked(created, tailID, headID, head)
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
	// Same endpoint auto-creation invariant as AddEdgeWithExpiration.
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	// PutEdge replaces the edge atomically. The head projected string is
	// unchanged across delete+add (same head key), so the head index needs
	// no maintenance when the edge already existed — it only changes when
	// add creates a fresh bucket.
	c.edges.delete(tail, head)
	created, tailID, headID := c.edges.addWithExpiration(tail, head, w, expiration)
	c.onEdgeAddedLocked(created, tailID, headID, head)
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
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID, applied := c.edges.addWithExpirationContrib(tail, head, w, expiration, contribID)
	if applied {
		c.onEdgeAddedLocked(created, tailID, headID, head)
	} else if created {
		// Defensive: addWithExpirationContrib cannot create a fresh
		// bucket and dedup-skip in the same call (the new bucket has
		// no prior contributions), but if a future refactor breaks
		// that invariant we still keep side indexes consistent.
		c.onEdgeAddedLocked(created, tailID, headID, head)
	}
	return applied
}

// AddEdgeWithExpirationContribHLC is the tombstone-aware sibling of
// AddEdgeWithExpirationContrib used by the replication apply path (#183).
// When a live edge tombstone exists for (tail, head) whose HLC is strictly
// newer than ts the contribution is dropped and false is returned —
// preventing a late Add* from resurrecting a freshly-deleted edge.
// Otherwise behaviour matches AddEdgeWithExpirationContrib, including
// ContribID-based dedup. A successful apply clears any existing
// tombstone for the edge (the new write supersedes the deletion).
func (c *GraphCache[S, T]) AddEdgeWithExpirationContribHLC(tail, head S, w float32, expiration time.Time, contribID ContribID, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tombTs, ok := c.edgeTombstoneLocked(tail, head); ok && ts.Less(tombTs) {
		return false
	}
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID, applied := c.edges.addWithExpirationContrib(tail, head, w, expiration, contribID)
	if applied {
		c.onEdgeAddedLocked(created, tailID, headID, head)
		if c.edgeTombstones != nil {
			delete(c.edgeTombstones, EdgeKey[S]{Tail: tail, Head: head})
		}
	} else if created {
		c.onEdgeAddedLocked(created, tailID, headID, head)
	}
	return applied
}

// PutVertexWithExpirationHLC is the LWW-aware sibling of PutVertexWithExpiration
// used by the replication apply path. When the stored HLC for key is strictly
// newer than ts the call is a no-op and returns applied=false. A zero ts
// always applies (no causality recorded). Local writers continue to use
// PutVertexWithExpiration; this helper is intentionally narrow to the
// replicated path so non-replicated workloads pay nothing.
func (c *GraphCache[S, T]) PutVertexWithExpirationHLC(key S, value T, expiration time.Time, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tombTs, ok := c.vertexTombstoneLocked(key); ok && ts.Less(tombTs) {
		return false
	}
	if existing, ok := c.vertexHLC[key]; ok && ts.Less(existing) {
		return false
	}
	c.putVertexLocked(key, value, expiration)
	if c.vertexHLC == nil {
		c.vertexHLC = make(map[S]hlc.Timestamp)
	}
	c.vertexHLC[key] = ts
	if c.vertexTombstones != nil {
		delete(c.vertexTombstones, key)
	}
	return true
}

// PutEdgeWithExpirationHLC is the LWW-aware sibling of PutEdgeWithExpiration
// used by the replication apply path. Returns applied=false when the stored
// edge's last accepted HLC is strictly newer than ts. Endpoint vertices are
// auto-created (matching PutEdgeWithExpiration) regardless of the LWW
// outcome — the endpoints exist independently of any individual edge
// write's causality and must be present for downstream traversal.
func (c *GraphCache[S, T]) PutEdgeWithExpirationHLC(tail, head S, w float32, expiration time.Time, ts hlc.Timestamp) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tombTs, ok := c.edgeTombstoneLocked(tail, head); ok && ts.Less(tombTs) {
		return false
	}
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	created, tailID, headID, applied := c.edges.putWithExpirationHLC(tail, head, w, expiration, ts)
	if created {
		c.onEdgeAddedLocked(created, tailID, headID, head)
	}
	if applied && c.edgeTombstones != nil {
		delete(c.edgeTombstones, EdgeKey[S]{Tail: tail, Head: head})
	}
	return applied
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

	deleted, tailID, headID := c.edges.delete(tail, head)
	if deleted {
		c.onEdgeDeletedLocked(tailID, headID, head)
	}
	return deleted
}

// flush performs the fused GC sweep over the edge map in a single walk.
// It removes zero-weight edges (returned as `zero`) and edges whose endpoints
// reference vertices that are no longer live (returned as `dangling`).
//
// Previously the GC tick walked the edge map twice and additionally cloned
// the whole tf map via snapshotTF before the dangling sweep; folding the two
// checks into one walk eliminates that O(E) snapshot allocation and roughly
// halves the c.mu.Lock hold time on large graphs.
func (c *GraphCache[S, T]) flush() (zero, dangling int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweepExpiredTombstonesLocked(time.Now())
	c.sweepStaleVertexHLCLocked()

	return c.edges.flushFunc(func(tailID, headID vertexID) bool {
		tail, ok := c.edges.resolveID(tailID)
		if !ok {
			return false
		}
		head, ok := c.edges.resolveID(headID)
		if !ok {
			return false
		}
		return c.vertices.Has(tail) && c.vertices.Has(head)
	}, c.headIndexOnFlushDeleteLocked())
}

// VertexCount returns the live vertex count under an RLock. Intended for
// Prometheus gauges that sample the cache periodically.
func (c *GraphCache[S, T]) VertexCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vertices.Count()
}

// EdgeCount returns the live (tail, head) edge count under an RLock. Like
// VertexCount it is intended for periodic gauge sampling, not hot paths.
func (c *GraphCache[S, T]) EdgeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.edges.count()
}

// VertexHLCCount returns the number of entries in the per-key LWW watermark
// map (vertexHLC). Under a healthy workload this equals the number of distinct
// vertex keys ever touched by the replication apply path and should track the
// live vertex count after a full GC sweep. A value that grows without bound
// across GC ticks is a leak signal (see issue #700). Returns 0 when the map
// has never been initialised (no replicated writes have arrived yet).
func (c *GraphCache[S, T]) VertexHLCCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.vertexHLC)
}

// VertexHLCHighWater returns the largest number of vertexHLC entries ever
// observed at the start of a GC sweep — the per-cycle peak before stale
// watermarks are drained. Unlike VertexHLCCount (which reports the current,
// post-sweep len and therefore reads low right after a drain), this value is
// monotonic non-decreasing and records the all-time churn peak. It used to also
// proxy the map's retained bucket-array heap (Go never shrinks a map after
// delete), but sweepStaleVertexHLCLocked now reallocates the map after a large
// drain, so a high VertexHLCHighWater no longer implies pinned heap — it is a
// historical confirm-the-phenomenon signal for the ttl_churn leak gate
// (#700/#719/#727). Returns 0 when no sweep has run yet. Safe for concurrent
// use; intended for Prometheus gauge sampling.
func (c *GraphCache[S, T]) VertexHLCHighWater() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vertexHLCHighWater
}

// SearchIndexStats returns the current number of distinct terms and indexed
// documents in the optional search index. Both values are 0 when the search
// index is disabled. Safe for concurrent use; intended for Prometheus gauge
// sampling.
func (c *GraphCache[S, T]) SearchIndexStats() (terms, docs int) {
	c.mu.RLock()
	idx := c.searchIndex
	c.mu.RUnlock()
	if idx == nil {
		return 0, 0
	}
	return idx.Stats()
}

// vertexHLCShrinkFloor and vertexHLCShrinkDivisor govern when
// sweepStaleVertexHLCLocked reallocates vertexHLC to release its bucket array.
// Go never shrinks a map after delete, so a born-expired churn that peaks at
// hundreds of thousands of entries (#719) and is then drained back to the live
// set (#700) would otherwise leave the full-size backing store pinned on the
// heap — the retention behind the ttl_churn release-bench leak gate (#727).
// The sweep rebuilds the map only when the pre-sweep size cleared the floor and
// the survivors are at most 1/divisor of it: the floor keeps negligible maps
// off the copy path, and the ratio keeps a healthy steady-state workload —
// whose live set tracks the vertex cache, so survivors ≈ pre-sweep size — from
// ever rebuilding.
const (
	vertexHLCShrinkFloor   = 1024
	vertexHLCShrinkDivisor = 4
)

// sweepStaleVertexHLCLocked removes vertexHLC entries whose corresponding
// vertex is no longer live. It is called from flush() which already holds
// c.mu.Lock(), so it is safe to mutate c.vertexHLC without an additional lock.
// This bounds the map to the live replicated-key set rather than the all-time
// set, fixing the leak described in issue #700. After a large drain it also
// reallocates the map so Go can reclaim the oversized bucket array (#727).
func (c *GraphCache[S, T]) sweepStaleVertexHLCLocked() {
	if c.vertexHLC == nil {
		return
	}
	// Record the pre-drain peak first: len(vertexHLC) here is the per-cycle
	// high-water (writes only grow the map between GC ticks; this sweep is the
	// only drain). This is the value that sizes the retained bucket array, so
	// it must be captured before the delete loop below empties the map.
	before := len(c.vertexHLC)
	if before > c.vertexHLCHighWater {
		c.vertexHLCHighWater = before
	}
	for key := range c.vertexHLC {
		if !c.vertices.Has(key) {
			delete(c.vertexHLC, key)
		}
	}
	// Reclaim the oversized bucket array after a large drain. Go never shrinks a
	// map after delete, so without this the map keeps the heap it sized for its
	// high-water entry count even though len() has collapsed (#727). When the
	// pre-sweep size cleared the floor and the survivors are at most 1/divisor of
	// it, copy them into a right-sized map and drop the old one for the GC.
	if after := len(c.vertexHLC); before >= vertexHLCShrinkFloor && after <= before/vertexHLCShrinkDivisor {
		rebuilt := make(map[S]hlc.Timestamp, after)
		for key, ts := range c.vertexHLC {
			rebuilt[key] = ts
		}
		c.vertexHLC = rebuilt
	}
}

// SetGCHooks installs optional observability callbacks invoked from Watch
// after every GC tick. Either argument may be nil. Hooks must not call back
// into the cache (re-entrant locking would deadlock). Safe for concurrent use.
func (c *GraphCache[S, T]) SetGCHooks(onExpire func(kind string, n int), onGCDuration func(d time.Duration)) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	c.onExpire = onExpire
	c.onGCDuration = onGCDuration
}

func (c *GraphCache[S, T]) snapshotHooks() (func(string, int), func(time.Duration)) {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()
	return c.onExpire, c.onGCDuration
}

// Neighbor walks the graph from seed and returns the visited subgraph. The
// per-hop top-k pruning keeps the k largest-weight edges when selectSmallest
// is false and the k smallest-weight edges when it is true (#560) — the
// caller picks the direction that matches its Objective so a cost-minimiser
// is not handed the costliest edges. tfidf re-scores edge weights BEFORE the
// directional top/bottom-k selection.
//
// keep is an optional frontier predicate (nil = accept all): a candidate head
// is expanded into the result only when keep(head) is true. It is applied at
// frontier materialisation, BEFORE scoring and the directional top/bottom-k
// prune, so top-k selects the k best *accepted* neighbours per hop. The seed
// is the anchor exemption — it is always retained and never passed through
// keep. Because the next-hop frontier is derived from the surviving edges, a
// matching vertex reachable only through a rejected "bridge" is not reached
// (induced-subgraph semantics). core stays generic: keep is just a predicate
// over S; the concrete prefix/string logic lives in the caller.
func (c *GraphCache[S, T]) Neighbor(seed S, step int, k int, tfidf bool, selectSmallest bool, keep func(S) bool) *graph.Graph[S, T] {
	g, _ := c.NeighborContext(context.Background(), seed, step, k, tfidf, selectSmallest, keep)
	return g
}

// NeighborContext is the context-aware variant of Neighbor. It returns
// ctx.Err() as soon as the context is cancelled or its deadline has expired
// — checked between BFS expansion steps — so handlers can short-circuit
// large traversals when the caller has given up. keep is the optional frontier
// predicate documented on Neighbor (nil = accept all).
func (c *GraphCache[S, T]) NeighborContext(ctx context.Context, seed S, step int, k int, tfidf bool, selectSmallest bool, keep func(S) bool) (*graph.Graph[S, T], error) {
	g, _, err := c.neighborContext(ctx, seed, step, k, tfidf, selectSmallest, keep, false)
	return g, err
}

// NeighborWithExpirationsContext returns the same subgraph as
// NeighborContext together with a parallel expirations map keyed by
// (tail, head). Both are computed under a single RLock so handlers can
// compose responses without re-acquiring the cache lock per edge.
//
// The expirations map only contains entries for edges that ended up in
// the returned graph; a missing or zero value means the edge has no
// known expiration. keep is the optional frontier predicate documented on
// Neighbor (nil = accept all).
func (c *GraphCache[S, T]) NeighborWithExpirationsContext(ctx context.Context, seed S, step int, k int, tfidf bool, selectSmallest bool, keep func(S) bool) (*graph.Graph[S, T], map[S]map[S]time.Time, error) {
	return c.neighborContext(ctx, seed, step, k, tfidf, selectSmallest, keep, true)
}

func (c *GraphCache[S, T]) neighborContext(ctx context.Context, seed S, step int, k int, tfidf bool, selectSmallest bool, keep func(S) bool, collectExpirations bool) (*graph.Graph[S, T], map[S]map[S]time.Time, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g := graph.NewGraph[S, T]()

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	if v, ok := c.vertices.Get(seed); !ok {
		return g, nil, nil
	} else {
		g.Vertices[seed] = v
	}

	var mu sync.Mutex
	// targets / seen are accessed only from the main goroutine (between
	// wg.Wait barriers), so plain maps suffice — no need for the locked
	// set.Set wrapper. mu protects concurrent writes to g.Edges and
	// (when requested) expirations.
	targets := map[S]struct{}{seed: {}}
	seen := make(map[S]struct{})
	// expirations is allocated up front so per-tail goroutines can publish
	// their surviving heads without an extra coordination pass.
	var expirations map[S]map[S]time.Time
	if collectExpirations {
		expirations = make(map[S]map[S]time.Time)
	}
	// We deliberately do NOT clone the entire edge table here: with c.mu
	// held read-locked, no writer can mutate c.edges.tf, so the per-tail
	// edgeCache.headsOf / docFreq accessors are safe to call directly.
	// This turns the per-call cost from O(V+E) (snapshotTF + snapshotDF)
	// into O(sum of degrees of visited tails).

	// processTail computes one tail's top-k neighbor edges and publishes
	// them to g.Edges (and expirations, if requested) under mu. It is the
	// shared body used by both the sequential and worker-pool paths.
	processTail := func(t S) {
		heads, ok := c.edges.headsOf(t)
		if !ok || len(heads) == 0 {
			return
		}
		edges := make(pq.SortableMap[S, float32], len(heads))
		var expRow map[S]time.Time
		if collectExpirations {
			expRow = make(map[S]time.Time, len(heads))
		}
		for headID, w := range heads {
			head, ok := c.edges.resolveID(headID)
			if !ok {
				continue
			}
			// Frontier predicate: reject non-matching heads here, BEFORE
			// scoring and the Top(k)/Bottom(k) prune below, so top-k selects
			// the k best *accepted* neighbours. The seed is set on g.Vertices
			// before the walk and never reaches this loop, so it is exempt.
			if keep != nil && !keep(head) {
				continue
			}
			sum, latest, nonZero := w.snapshot()
			if !nonZero {
				continue
			}
			if tfidf {
				edges[head] = sum / float32(math.Log2(float64(1+c.edges.docFreq(headID))))
			} else {
				edges[head] = sum
			}
			if expRow != nil {
				expRow[head] = latest
			}
		}

		// Prune to the k edges at the Objective-selected extreme — the k
		// smallest weights when selectSmallest (MINIMIZE), the k largest
		// otherwise (#560) — then trim expirations to the survivors.
		if selectSmallest {
			edges = edges.Bottom(k)
		} else {
			edges = edges.Top(k)
		}
		if expRow != nil {
			filtered := make(map[S]time.Time, len(edges))
			for head := range edges {
				if exp, has := expRow[head]; has {
					filtered[head] = exp
				}
			}
			expRow = filtered
		}

		mu.Lock()
		g.Edges[t] = edges
		if expRow != nil {
			expirations[t] = expRow
		}
		mu.Unlock()
	}

	for range step {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		// Collect this step's frontier (targets not yet processed). Marking
		// happens after wg.Wait so the goroutines need not touch `seen`.
		frontier := make([]S, 0, len(targets))
		for t := range targets {
			if _, ok := seen[t]; ok {
				continue
			}
			frontier = append(frontier, t)
		}

		// Small frontiers run sequentially: goroutine startup and the
		// shared mu.Lock round-trip dominate per-tail sort work below the
		// threshold. Larger frontiers fan out across a bounded worker pool
		// (capped at GOMAXPROCS) so we keep parallelism without unbounded
		// goroutine spawning.
		if len(frontier) < neighborParallelThreshold {
			for _, tail := range frontier {
				processTail(tail)
			}
		} else {
			workers := runtime.GOMAXPROCS(0)
			if workers > len(frontier) {
				workers = len(frontier)
			}
			tailCh := make(chan S, len(frontier))
			for _, tail := range frontier {
				tailCh <- tail
			}
			close(tailCh)
			var wg sync.WaitGroup
			wg.Add(workers)
			for i := 0; i < workers; i++ {
				go func() {
					defer wg.Done()
					for t := range tailCh {
						processTail(t)
					}
				}()
			}
			wg.Wait()
		}

		// Mark this step's frontier as processed.
		for _, t := range frontier {
			seen[t] = struct{}{}
		}

		// Find all next targets
		for _, heads := range g.Edges {
			for head := range heads {
				if _, ok := seen[head]; !ok {
					targets[head] = struct{}{}
				}
			}
		}
	}

	// Add vertices to the graph
	for tail, heads := range g.Edges {
		g.Vertices[tail], _ = c.vertices.Get(tail)
		for head := range heads {
			g.Vertices[head], _ = c.vertices.Get(head)
		}
	}

	return g, expirations, nil
}

func (c *GraphCache[S, T]) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			start := time.Now()
			vExpired := c.vertices.Flush()
			eExpired, dRemoved := c.flush()
			d := time.Since(start)
			onExpire, onGC := c.snapshotHooks()
			if onExpire != nil {
				if vExpired > 0 {
					onExpire("vertex", vExpired)
				}
				if eExpired > 0 {
					onExpire("edge", eExpired)
				}
				if dRemoved > 0 {
					onExpire("dangling_edge", dRemoved)
				}
			}
			if onGC != nil {
				onGC(d)
			}

		case <-ctx.Done():
			return
		}
	}
}
