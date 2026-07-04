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

// PutVerticesWithExpirationIfAbsent writes each vertex only when no live vertex
// exists at its key (SET NX semantics, #896). It returns the number of vertices
// actually written and, in request order, the keys skipped because a live
// vertex already existed. A vertex whose expiration is already past is discarded
// and counted as neither written nor skipped (#918). Liveness follows the
// live-visibility rule (#750): an expired-but-uncollected vertex does not block
// its write. The whole batch commits under a single write lock, so the existence
// check and store are atomic per key; within one batch a key's first accepted
// write makes it live, so a later duplicate of that key is reported as skipped.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsent(items []VertexItem[S, T]) (written int, skipped []S) {
	if len(items) == 0 {
		return 0, nil
	}
	now := time.Now()
	prepared := c.prepareSearchDocs(items, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range items {
		if c.vertices.Has(items[i].Key) {
			skipped = append(skipped, items[i].Key)
			continue
		}
		if c.putLocalVertexLockedAt(items[i].Key, items[i].Value, items[i].Expiration, now, preparedAt(prepared, i)) {
			written++
		}
	}
	return written, skipped
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
// lock and returns, index-aligned with items, the post-apply LIVE weight
// sum for each edge (#897), plus the number of items that were
// deduplicated — i.e. whose non-zero ContribID matched a live contribution
// already stored on the (tail, head) edge, so no weight was added. Items
// with a zero ContribID always apply (legacy additive semantics) and never
// count as deduped.
//
// effective[i] is the edge's live weight sum after this batch's contribution
// was folded in, compacted against a single `now` sampled once for the whole
// batch (matching #838's single-clock-read pattern). On a dedup no-op it is
// the current live sum, so a client can read back the running total of a
// windowed counter without a second round-trip. It is a serving-node local
// view: under active replication a peer may hold contributions not yet
// streamed in, so treat it as a fast local estimate, not a cluster-wide total.
//
// The dedup guarantee mirrors the per-edge AddEdgeWithExpirationContrib used
// by the replication apply path (#182): replaying a batch with the same
// ContribIDs leaves the stored weights unchanged. This is what lets a
// client-supplied idempotency key make AddEdge(s) safe to retry (#588).
func (c *GraphCache[S, T]) AddEdgesWithExpirationContrib(items []EdgeItem[S]) (effective []float32, deduped int) {
	if len(items) == 0 {
		return nil, 0
	}
	// now is sampled once for the whole batch so every item's live-sum
	// compaction shares one clock read (issue #838 single-clock pattern).
	now := time.Now()
	effective = make([]float32, len(items))
	// Fast path: additive appends to edges that already exist between live
	// endpoints take only the per-edge weight lock (see
	// tryAddExistingEdgeContrib), so a batch of hot existing edges never
	// serializes on c.mu. Items that miss the fast path — new edges, or
	// endpoints needing revival — are collected by index and applied together
	// under a single c.mu.Lock, which preserves bucket-creation atomicity for
	// the edge SET a concurrent reader observes (the fast path only mutates
	// already-present edge weights, never the bucket structure). Tracking miss
	// INDICES (not a copy of the items) keeps effective[] index-aligned across
	// the fast/slow split. Additive writes are commutative and ContribID dedup
	// is per-edge, so applying the fast-path items before the collected misses
	// changes no per-edge result (issue #743 item 5).
	var missIdx []int
	for i, it := range items {
		applied, eff, ok := c.tryAddExistingEdgeContrib(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID, now)
		if !ok {
			missIdx = append(missIdx, i)
			continue
		}
		effective[i] = eff
		if !applied {
			deduped++
		}
	}
	if len(missIdx) == 0 {
		return effective, deduped
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, i := range missIdx {
		it := items[i]
		applied, eff := c.addEdgeContribLocked(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID, now)
		effective[i] = eff
		if !applied {
			deduped++
		}
	}
	return effective, deduped
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
//
// Returns the number of items REJECTED by those guards — tombstone fence or
// LWW watermark — exactly the set the singular PutVertexWithExpirationHLC
// reports as applied=false. Born-expired items are applied (dead on arrival,
// watermark recorded) and are NOT counted, and nothing else contributes, so
// the replication apply path (#840) can fire its clamp-reject metric once per
// rejected item with the same meaning the per-item loop had.
func (c *GraphCache[S, T]) PutVerticesWithExpirationHLC(items []VertexItem[S, T], ts hlc.Timestamp) (rejected int) {
	if len(items) == 0 {
		return 0
	}
	now := time.Now()
	prepared := c.prepareSearchDocs(items, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range items {
		it := items[i]
		if !c.vertexWriteAllowedLocked(it.Key, ts) {
			rejected++
			continue
		}
		c.putLocalVertexLockedAt(it.Key, it.Value, it.Expiration, now, preparedAt(prepared, i))
		c.recordVertexHLCLocked(it.Key, ts)
		c.clearVertexTombstoneLocked(it.Key)
	}
	return rejected
}

// PutVerticesWithExpirationIfAbsentHLC is the replication-aware sibling of
// PutVerticesWithExpirationIfAbsent used by the LOCAL write path when
// replication is enabled (#896). Like the non-HLC variant it writes each key
// only when no live vertex exists there, but it additionally stamps every
// accepted write with the originating mutation's ts (and clears any tombstone)
// so the origin participates in last-writer-wins on equal footing with peers —
// the same discipline PutVerticesWithExpirationHLC uses. A key whose write is
// forbidden by the causal fence (a strictly newer delete/put watermark) is
// reported as skipped rather than resurrected.
//
// It returns the indices of items that passed the absence check AND were
// actually stored (in request order) so the caller can both count them and
// replicate only that live subset as an unconditional LWW put, and the keys
// skipped because a live vertex already existed. A born-expired item is
// discarded and appears in neither slice (#918). Two concurrent if-absent
// writers on different nodes can both report their write as accepted locally;
// the replicated unconditional puts then converge via higher-HLC-wins
// (documented best-effort, like Redis SETNX with async replicas).
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsentHLC(items []VertexItem[S, T], ts hlc.Timestamp) (writtenIdx []int, skipped []S) {
	if len(items) == 0 {
		return nil, nil
	}
	now := time.Now()
	prepared := c.prepareSearchDocs(items, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range items {
		it := items[i]
		if c.vertices.Has(it.Key) {
			skipped = append(skipped, it.Key)
			continue
		}
		if !c.vertexWriteAllowedLocked(it.Key, ts) {
			skipped = append(skipped, it.Key)
			continue
		}
		if !c.putLocalVertexLockedAt(it.Key, it.Value, it.Expiration, now, preparedAt(prepared, i)) {
			// Born expired: nothing stored, so it is neither written nor
			// skipped. Deliberately do NOT stamp an HLC watermark or clear a
			// tombstone for a non-write — that would fence later valid writes
			// to this key behind a watermark for a value that never existed.
			continue
		}
		c.recordVertexHLCLocked(it.Key, ts)
		c.clearVertexTombstoneLocked(it.Key)
		writtenIdx = append(writtenIdx, i)
	}
	return writtenIdx, skipped
}

// PutEdgesWithExpiration used by the LOCAL write path when replication is
// enabled. Like PutVerticesWithExpirationHLC it stamps every edge with the
// originating mutation's ts so PutEdge resolves as an LWW-Register on
// (tail, head) across replicas rather than diverging when two origins write
// the same edge concurrently. Endpoint vertices are auto-created regardless of
// the per-edge LWW outcome (matching PutEdgeWithExpirationHLC) so traversal
// always sees the endpoints.
//
// Returns the number of items REJECTED by the tombstone fence or by the
// per-edge LWW watermark inside the storage layer — exactly the set the
// singular PutEdgeWithExpirationHLC reports as applied=false — so the
// replication apply path (#840) can fire its clamp-reject metric once per
// rejected item with unchanged meaning. ContribID dedup never contributes
// here (that is AddEdgesWithExpirationContribHLC's separate count).
func (c *GraphCache[S, T]) PutEdgesWithExpirationHLC(items []EdgeItem[S], ts hlc.Timestamp) (rejected int) {
	if len(items) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range items {
		if !c.edgeWriteAllowedLocked(it.Tail, it.Head, ts) {
			rejected++
			continue
		}
		if c.putEdgeHLCLocked(it.Tail, it.Head, it.Weight, it.Expiration, ts) {
			c.clearEdgeTombstoneLocked(it.Tail, it.Head)
		} else {
			rejected++
		}
	}
	return rejected
}

// AddEdgesWithExpirationContribHLC is the tombstone-aware, single-lock batch
// sibling of AddEdgesWithExpirationContrib used by the LOCAL write path when
// replication is enabled. Additive merge already converges via ContribID set
// semantics regardless of delivery order, so the ts is NOT compared against a
// per-edge write watermark; it is consulted ONLY against the edge tombstone so
// a contribution whose ts is strictly older than a delete is dropped on the
// origin exactly as it is on every peer. Items whose ts loses to the tombstone
// are skipped and counted as deduped=false (they applied nothing); items with
// a matching live ContribID are deduped as in the non-HLC variant. Returns,
// index-aligned with items, the post-apply LIVE weight sum for each edge
// (#897) plus the number of items that added no weight (tombstone-dropped or
// ContribID-deduped). A tombstone-dropped item applied nothing, but its
// effective entry still reports the edge's current live sum — matching the
// ContribID-deduped path — so a genuinely nonzero live weight (e.g. from a
// newer contribution that re-created the edge) is never misreported as 0 (#918).
func (c *GraphCache[S, T]) AddEdgesWithExpirationContribHLC(items []EdgeItem[S], ts hlc.Timestamp) (effective []float32, deduped int) {
	if len(items) == 0 {
		return nil, 0
	}
	now := time.Now()
	effective = make([]float32, len(items))
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, it := range items {
		if !c.edgeWriteAllowedLocked(it.Tail, it.Head, ts) {
			// Fenced by a newer tombstone: this item applies nothing, but the
			// edge may still hold live weight from another contribution. Report
			// that real live sum, the same value the dedup no-op path returns.
			effective[i] = c.edges.liveSumAt(it.Tail, it.Head, now)
			deduped++
			continue
		}
		applied, eff := c.addEdgeContribLocked(it.Tail, it.Head, it.Weight, it.Expiration, it.ContribID, now)
		effective[i] = eff
		if applied {
			c.clearEdgeTombstoneLocked(it.Tail, it.Head)
			continue
		}
		deduped++
	}
	return effective, deduped
}
