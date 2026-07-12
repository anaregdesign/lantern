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
// lock is taken (see prepareSearchDocsBounded) so the expensive per-vertex work never
// serializes other writers behind the aggregate graph lock; only the cheap
// store + postings mutation happens under c.mu (#739). A search-analysis limit
// error rejects the complete batch before either structure changes.
func (c *GraphCache[S, T]) PutVerticesWithExpiration(items []VertexItem[S, T]) error {
	return c.PutVerticesWithExpirationChecked(items)
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
	written, skipped, _ = c.PutVerticesWithExpirationIfAbsentChecked(items)
	return written, skipped
}

// prepareSearchDocsBounded analyzes every live item outside c.mu, returning a
// 1:1 prepared/error pair. Born-expired items are left empty and skipped.
func (c *GraphCache[S, T]) prepareSearchDocsBounded(items []VertexItem[S, T], now time.Time) ([]search.PreparedDocument, []error) {
	if c.searchIndex == nil {
		return nil, nil
	}
	prepared := make([]search.PreparedDocument, len(items))
	errs := make([]error, len(items))
	for i := range items {
		if cache.IsLiveAt(items[i].Expiration, now) {
			prepared[i], _, errs[i] = c.searchIndex.Prepare(c.searchExtract(items[i].Value))
		}
	}
	return prepared, errs
}

// preparedAt returns a pointer to the i-th prepared document, or nil when no
// search index produced a batch (prepared == nil) so the callee falls back to
// inline analysis. The pointer is safe to take because prepared is a
// fixed-size slice that is never appended to after preparation returns.
func preparedAt(prepared []search.PreparedDocument, i int) *search.PreparedDocument {
	if prepared == nil {
		return nil
	}
	return &prepared[i]
}

// PutVerticesWithExpirationChecked is the bounded, all-or-nothing local write
// path. Analysis and aggregate-limit validation complete before either the
// vertex cache or secondary index is mutated.
func (c *GraphCache[S, T]) PutVerticesWithExpirationChecked(items []VertexItem[S, T]) error {
	if len(items) == 0 {
		return nil
	}
	now := time.Now()
	prepared, errs := c.prepareSearchDocsBounded(items, now)
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		return search.ErrIndexIncomplete
	}
	indexItems := preparedItems(items, prepared)
	if c.searchIndex != nil {
		if err := c.searchIndex.ValidateManyPrepared(indexItems); err != nil {
			return err
		}
		c.searchIndex.IndexManyPreparedValidated(indexItems)
	}
	for i := range items {
		c.putLocalVertexLockedAtMode(items[i].Key, items[i].Value, items[i].Expiration, now, preparedAt(prepared, i), false)
	}
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		return c.rebuildSearchIndexLocked()
	}
	return nil
}

func preparedItems[S comparable, T any](items []VertexItem[S, T], prepared []search.PreparedDocument) []search.PreparedItem[S] {
	if prepared == nil {
		return nil
	}
	out := make([]search.PreparedItem[S], len(items))
	for i := range items {
		out[i] = search.PreparedItem[S]{ID: items[i].Key, Prepared: prepared[i]}
	}
	return out
}

// PutVerticesWithExpirationIfAbsentChecked is the conditional sibling of the
// bounded local path. Limits are evaluated only for items that would actually
// be accepted; an oversized skipped item cannot poison an otherwise valid
// batch.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsentChecked(items []VertexItem[S, T]) (written int, skipped []S, err error) {
	accepted, skipped, err := c.putVerticesIfAbsentChecked(items, hlc.Timestamp{}, false)
	return len(accepted), skipped, err
}

// PutVerticesWithExpirationIfAbsentHLCChecked is the local replicated-origin
// path: accepted writes are bounded before graph or HLC mutation.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsentHLCChecked(items []VertexItem[S, T], ts hlc.Timestamp) (writtenIdx []int, skipped []S, err error) {
	return c.putVerticesIfAbsentChecked(items, ts, true)
}

func (c *GraphCache[S, T]) putVerticesIfAbsentChecked(items []VertexItem[S, T], ts hlc.Timestamp, useHLC bool) ([]int, []S, error) {
	if len(items) == 0 {
		return nil, nil, nil
	}
	now := time.Now()
	prepared, errs := c.prepareSearchDocsBounded(items, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		return nil, nil, search.ErrIndexIncomplete
	}
	reserved := make(map[S]struct{})
	var accepted []int
	var skipped []S
	for i, item := range items {
		_, duplicate := reserved[item.Key]
		if c.vertices.Has(item.Key) || duplicate || (useHLC && !c.vertexWriteAllowedLocked(item.Key, ts)) {
			skipped = append(skipped, item.Key)
			continue
		}
		if !cache.IsLiveAt(item.Expiration, now) {
			continue
		}
		if errs != nil && errs[i] != nil {
			return nil, nil, errs[i]
		}
		accepted = append(accepted, i)
		reserved[item.Key] = struct{}{}
	}
	indexItems := make([]search.PreparedItem[S], 0, len(accepted))
	if c.searchIndex != nil {
		for _, i := range accepted {
			indexItems = append(indexItems, search.PreparedItem[S]{ID: items[i].Key, Prepared: prepared[i]})
		}
	}
	if c.searchIndex != nil {
		if err := c.searchIndex.ValidateManyPrepared(indexItems); err != nil {
			return nil, nil, err
		}
		c.searchIndex.IndexManyPreparedValidated(indexItems)
	}
	for _, i := range accepted {
		item := items[i]
		c.putLocalVertexLockedAtMode(item.Key, item.Value, item.Expiration, now, preparedAt(prepared, i), false)
		if useHLC {
			c.recordVertexHLCLocked(item.Key, ts)
			c.clearVertexTombstoneLocked(item.Key)
		}
	}
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		if err := c.rebuildSearchIndexLocked(); err != nil {
			return nil, nil, err
		}
	}
	return accepted, skipped, nil
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
	n := len(c.vertices.DeleteMany(keys))
	c.rebuildIncompleteSearchLocked()
	return n
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
	rejected, _ = c.putVerticesWithExpirationHLC(items, ts, false)
	return rejected
}

// PutVerticesWithExpirationHLCChecked is used for locally-originated writes:
// unlike replication apply it rejects a budget overflow atomically.
func (c *GraphCache[S, T]) PutVerticesWithExpirationHLCChecked(items []VertexItem[S, T], ts hlc.Timestamp) (rejected int, err error) {
	return c.putVerticesWithExpirationHLC(items, ts, true)
}

func (c *GraphCache[S, T]) putVerticesWithExpirationHLC(items []VertexItem[S, T], ts hlc.Timestamp, strict bool) (rejected int, err error) {
	if len(items) == 0 {
		return 0, nil
	}
	now := time.Now()
	prepared, prepErrs := c.prepareSearchDocsBounded(items, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	if strict && c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		return 0, search.ErrIndexIncomplete
	}
	accepted := make([]int, 0, len(items))
	for i := range items {
		it := items[i]
		if !c.vertexWriteAllowedLocked(it.Key, ts) {
			rejected++
			continue
		}
		accepted = append(accepted, i)
	}
	indexItems := make([]search.PreparedItem[S], 0, len(accepted))
	var indexErr error
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		indexErr = search.ErrIndexIncomplete
	} else if c.searchIndex != nil {
		for _, i := range accepted {
			if prepErrs != nil && prepErrs[i] != nil {
				indexErr = prepErrs[i]
				break
			}
			indexItems = append(indexItems, search.PreparedItem[S]{ID: items[i].Key, Prepared: prepared[i]})
		}
	}
	if indexErr == nil && c.searchIndex != nil {
		indexErr = c.searchIndex.ValidateManyPrepared(indexItems)
	}
	if strict && indexErr != nil {
		return rejected, indexErr
	}
	updateSearch := c.searchIndex != nil && indexErr == nil
	if updateSearch {
		c.searchIndex.IndexManyPreparedValidated(indexItems)
	}
	if indexErr != nil && c.searchIndex != nil {
		c.searchIndex.MarkIncomplete()
	}
	for _, i := range accepted {
		it := items[i]
		c.putLocalVertexLockedAtMode(it.Key, it.Value, it.Expiration, now, preparedAt(prepared, i), false)
		c.recordVertexHLCLocked(it.Key, ts)
		c.clearVertexTombstoneLocked(it.Key)
	}
	return rejected, nil
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
	writtenIdx, skipped, _ = c.PutVerticesWithExpirationIfAbsentHLCChecked(items, ts)
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
