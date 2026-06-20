package graphcache

import (
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// VertexItem is a single (key, value, expiration) tuple supplied to
// PutVerticesWithExpiration.
type VertexItem[S comparable, T any] struct {
	Key        S
	Value      T
	Expiration time.Time
}

// EdgeItem is a single (tail, head, weight, expiration) tuple supplied to
// AddEdgesWithExpiration / PutEdgesWithExpiration.
type EdgeItem[S comparable] struct {
	Tail       S
	Head       S
	Weight     float32
	Expiration time.Time
	// ContribID is an optional dedup key for additive AddEdges* writes.
	// A non-zero id makes the contribution idempotent: re-applying an item
	// with the same id leaves the stored weight unchanged (see
	// AddEdgesWithExpirationContrib). The zero value disables dedup and keeps
	// the legacy additive semantics. PutEdgesWithExpiration ignores this
	// field — Put is already idempotent.
	ContribID ContribID
}

// EdgeKey identifies a directed edge for batch deletion via DeleteEdges.
type EdgeKey[S comparable] struct {
	Tail S
	Head S
}

// PutVerticesWithExpiration writes every supplied vertex under a single
// write lock. Concurrent readers observe either the pre-batch or the
// post-batch state — never an intermediate snapshot where some keys are
// present and others are not.
//
// Search-document analysis (tokenization) for the whole batch runs BEFORE the
// lock is taken (see prepareSearchDocs) so the expensive per-vertex work never
// serializes other writers behind the aggregate graph lock; only the cheap
// store + postings mutation happens under c.mu (#739).
func (c *GraphCache[S, T]) PutVerticesWithExpiration(items []VertexItem[S, T]) {
	if len(items) == 0 {
		return
	}
	now := time.Now()
	prepared := c.prepareSearchDocs(items, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range items {
		c.putLocalVertexLockedAt(items[i].Key, items[i].Value, items[i].Expiration, now, preparedAt(prepared, i))
	}
}

// prepareSearchDocs analyzes the search document of every live item OUTSIDE
// c.mu so the per-vertex tokenization never runs under the aggregate graph lock
// (#739). It returns nil when no search index is installed — the overwhelmingly
// common case pays a single nil check and no allocation — and otherwise a slice
// aligned 1:1 with items. Born-expired items (which putLocalVertexLockedAt
// deletes rather than stores) are left as the zero PreparedDocument and never
// indexed, so their analysis is skipped. Safe to call without c.mu held:
// c.searchIndex and c.searchExtract are installed once by EnableSearchIndex
// before any vertex is stored and never mutated afterward.
func (c *GraphCache[S, T]) prepareSearchDocs(items []VertexItem[S, T], now time.Time) []search.PreparedDocument {
	if c.searchIndex == nil {
		return nil
	}
	prepared := make([]search.PreparedDocument, len(items))
	for i := range items {
		if cache.IsLiveAt(items[i].Expiration, now) {
			prepared[i] = c.searchIndex.Prepare(c.searchExtract(items[i].Value))
		}
	}
	return prepared
}

// preparedAt returns a pointer to the i-th prepared document, or nil when no
// search index produced a batch (prepared == nil) so the callee falls back to
// inline analysis. The pointer is safe to take because prepared is a
// fixed-size slice that is never appended to after prepareSearchDocs returns.
func preparedAt(prepared []search.PreparedDocument, i int) *search.PreparedDocument {
	if prepared == nil {
		return nil
	}
	return &prepared[i]
}

// AddEdgesWithExpiration additively writes every supplied edge under a
// single write lock, auto-creating endpoint vertices on demand (matching
// the per-edge AddEdgeWithExpiration invariant). Concurrent readers see
// either the pre-batch or the post-batch state.
//
// Each item's ContribID is honored: a non-zero id deduplicates the
// contribution (see AddEdgesWithExpirationContrib). Leaving ContribID at its
// zero value — the default — keeps the legacy non-idempotent additive path.
func (c *GraphCache[S, T]) AddEdgesWithExpiration(items []EdgeItem[S]) {
	c.AddEdgesWithExpirationContrib(items)
}

// AddEdgesWithExpirationContrib is the dedup-aware batch sibling of
// AddEdgesWithExpiration. It applies the whole batch under a single write
// lock and returns the number of items that were deduplicated — i.e. whose
// non-zero ContribID matched a live contribution already stored on the
// (tail, head) edge, so no weight was added. Items with a zero ContribID
// always apply (legacy additive semantics) and never count as deduped.
//
// The dedup guarantee mirrors the per-edge AddEdgeWithExpirationContrib used
// by the replication apply path (#182): replaying a batch with the same
// ContribIDs leaves the stored weights unchanged. This is what lets a
// client-supplied idempotency key make AddEdge(s) safe to retry (#588).
func (c *GraphCache[S, T]) AddEdgesWithExpirationContrib(items []EdgeItem[S]) (deduped int) {
	if len(items) == 0 {
		return 0
	}
	// Fast path: additive appends to edges that already exist between live
	// endpoints take only the per-edge weight lock (see
	// tryAddExistingEdgeContrib), so a batch of hot existing edges never
	// serializes on c.mu. Items that miss the fast path — new edges, or
	// endpoints needing revival — are collected and applied together under a
	// single c.mu.Lock, which preserves bucket-creation atomicity for the
	// edge SET a concurrent reader observes (the fast path only mutates
	// already-present edge weights, never the bucket structure). Additive
	// writes are commutative and ContribID dedup is per-edge, so applying the
	// fast-path items before the collected misses changes no per-edge result
	// (issue #743 item 5).
	var misses []EdgeItem[S]
	for _, it := range items {
		applied, ok := c.tryAddExistingEdgeContrib(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID)
		if !ok {
			misses = append(misses, it)
			continue
		}
		if !applied {
			deduped++
		}
	}
	if len(misses) == 0 {
		return deduped
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range misses {
		if !c.addEdgeContribLocked(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID) {
			deduped++
		}
	}
	return deduped
}

// PutEdgesWithExpiration replaces every supplied edge atomically under a
// single write lock. Each (tail, head) pair is deleted and re-added so the
// resulting weight is exactly the supplied weight — matching
// PutEdgeWithExpiration semantics for each item in the batch.
func (c *GraphCache[S, T]) PutEdgesWithExpiration(items []EdgeItem[S]) {
	if len(items) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		c.putEdgeLocked(it.Tail, it.Head, it.Weight, it.Expiration)
	}
}

// DeleteVertices removes every supplied vertex under a single write lock
// and returns the count of keys that were actually present (and therefore
// deleted). Concurrent readers observe either the pre-batch or the
// post-batch state. Vertex-owned side indexes (dict refs, prefix radix,
// search postings) are cleaned in one pass via the batch eviction hook so a
// large delete pays one acquisition per index instead of one per key (#738).
func (c *GraphCache[S, T]) DeleteVertices(keys []S) int {
	if len(keys) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.vertices.DeleteMany(keys))
}

// DeleteEdges removes every supplied edge under a single write lock and
// returns the count of edges that were actually present. Concurrent
// readers observe either the pre-batch or the post-batch state.
func (c *GraphCache[S, T]) DeleteEdges(keys []EdgeKey[S]) int {
	if len(keys) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int
	for _, k := range keys {
		if c.deleteEdgeLocked(k.Tail, k.Head) {
			n++
		}
	}
	return n
}

// PutVerticesWithExpirationHLC is the LWW-aware, single-lock batch sibling of
// PutVerticesWithExpiration used by the LOCAL write path when replication is
// enabled. Every item is stamped with the SAME ts — the HLC the originating
// mutation is logged under — so the origin's own writes participate in
// last-writer-wins on equal footing with the values its peers apply from its
// mutation log.
//
// This closes a convergence hole: when the local path used the non-HLC
// PutVerticesWithExpiration it stored a value WITHOUT recording a vertexHLC
// watermark, so a concurrently-written OLDER value replayed from a peer would
// clobber the origin's newer value on the origin (the incoming write saw no
// watermark to lose to) while every other replica kept the newer value —
// permanent divergence for the same key. Stamping the watermark here makes
// PutVertex an LWW-Register on every replica, exactly as docs/replication.md
// specifies ("Higher HLC wins; same HLC ⇒ higher origin ID wins").
//
// Per item the usual guards apply: a write whose ts is strictly older than the
// key's tombstone, or strictly older than the key's stored vertexHLC, is
// skipped. Born-expired items follow the same dead-on-arrival handling as
// PutVerticesWithExpiration (the entry is removed, not stored) so the #698
// high-water optimisation is preserved; the watermark is still recorded so a
// later strictly-older write cannot resurrect the key.
func (c *GraphCache[S, T]) PutVerticesWithExpirationHLC(items []VertexItem[S, T], ts hlc.Timestamp) {
	if len(items) == 0 {
		return
	}
	now := time.Now()
	prepared := c.prepareSearchDocs(items, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range items {
		it := items[i]
		if !c.vertexWriteAllowedLocked(it.Key, ts) {
			continue
		}
		c.putLocalVertexLockedAt(it.Key, it.Value, it.Expiration, now, preparedAt(prepared, i))
		c.recordVertexHLCLocked(it.Key, ts)
		c.clearVertexTombstoneLocked(it.Key)
	}
}

// PutEdgesWithExpirationHLC is the LWW-aware, single-lock batch sibling of
// PutEdgesWithExpiration used by the LOCAL write path when replication is
// enabled. Like PutVerticesWithExpirationHLC it stamps every edge with the
// originating mutation's ts so PutEdge resolves as an LWW-Register on
// (tail, head) across replicas rather than diverging when two origins write
// the same edge concurrently. Endpoint vertices are auto-created regardless of
// the per-edge LWW outcome (matching PutEdgeWithExpirationHLC) so traversal
// always sees the endpoints.
func (c *GraphCache[S, T]) PutEdgesWithExpirationHLC(items []EdgeItem[S], ts hlc.Timestamp) {
	if len(items) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		if !c.edgeWriteAllowedLocked(it.Tail, it.Head, ts) {
			continue
		}
		if c.putEdgeHLCLocked(it.Tail, it.Head, it.Weight, it.Expiration, ts) {
			c.clearEdgeTombstoneLocked(it.Tail, it.Head)
		}
	}
}

// AddEdgesWithExpirationContribHLC is the tombstone-aware, single-lock batch
// sibling of AddEdgesWithExpirationContrib used by the LOCAL write path when
// replication is enabled. Additive merge already converges via ContribID set
// semantics regardless of delivery order, so the ts is NOT compared against a
// per-edge write watermark; it is consulted ONLY against the edge tombstone so
// a contribution whose ts is strictly older than a delete is dropped on the
// origin exactly as it is on every peer. Items whose ts loses to the tombstone
// are skipped and counted as deduped=false (they applied nothing); items with
// a matching live ContribID are deduped as in the non-HLC variant. Returns the
// number of items that added no weight (tombstone-dropped or ContribID-deduped).
func (c *GraphCache[S, T]) AddEdgesWithExpirationContribHLC(items []EdgeItem[S], ts hlc.Timestamp) (deduped int) {
	if len(items) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		if !c.edgeWriteAllowedLocked(it.Tail, it.Head, ts) {
			deduped++
			continue
		}
		if c.addEdgeContribLocked(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID) {
			c.clearEdgeTombstoneLocked(it.Tail, it.Head)
			continue
		}
		deduped++
	}
	return deduped
}
