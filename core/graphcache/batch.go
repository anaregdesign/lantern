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
	// CausalBarrier marks an authoritative accepted-expired replication
	// entry. Only HLC batch paths interpret it. It prevents a receiver whose
	// wall clock is behind the origin from reclassifying the entry as live,
	// while preserving duplicate-key request order inside one atomic batch.
	CausalBarrier bool
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
	// CausalBarrier is the edge sibling of VertexItem.CausalBarrier. It is
	// consumed only by the HLC Put batch path and never creates a bucket or
	// endpoint vertices.
	CausalBarrier bool
}

func vertexItemLiveAt[S comparable, T any](item VertexItem[S, T], now time.Time) bool {
	return !item.CausalBarrier && cache.IsLiveAt(item.Expiration, now)
}

func edgeItemLiveAt[S comparable](item EdgeItem[S], now time.Time) bool {
	return !item.CausalBarrier && cache.IsLiveAt(item.Expiration, now)
}

// EdgeKey identifies a directed edge for batch deletion via DeleteEdges.
type EdgeKey[S comparable] struct {
	Tail S
	Head S
}

// PutOutcome is the storage-authoritative result for one Put item. It is
// deliberately independent of protobuf so core remains a leaf module.
type PutOutcome uint8

const (
	// PutOutcomeAppliedAndLive means the item was accepted and live at the cache's
	// sampled application time.
	PutOutcomeAppliedAndLive PutOutcome = iota + 1
	// PutOutcomeExpired means the item was accepted as a delete-like overwrite
	// but its absolute expiration was not live at application time.
	PutOutcomeExpired
	// PutOutcomeConditionNotMet means if-absent observed an existing live item.
	PutOutcomeConditionNotMet
	// PutOutcomeSuperseded means a newer causal write or tombstone rejected the
	// item and the previous state was preserved.
	PutOutcomeSuperseded
)

// PutVerticesWithExpiration writes every supplied vertex under a single
// write lock. Concurrent readers observe either the pre-batch or the
// post-batch state — never an intermediate snapshot where some keys are
// present and others are not.
//
// Search-document projection, tokenization, sorting, and term aggregation run
// BEFORE the lock is taken (see prepareSearchDocsBounded), so expensive
// per-vertex work never serializes other writers behind the aggregate graph
// lock. The accepted compact mutations are applied through one index write-lock
// acquisition. SearchVertices holds the shared side of searchCommitMu, so it
// observes the complete pre-batch or post-batch state, never a midpoint. A
// search-analysis limit error rejects the complete batch before either
// structure changes (#739, #1051).
func (c *GraphCache[S, T]) PutVerticesWithExpiration(items []VertexItem[S, T]) error {
	return c.PutVerticesWithExpirationChecked(items)
}

// PutVerticesWithExpirationIfAbsent writes each vertex only when no live vertex
// exists at its key (SET NX semantics, #896). It returns the number of vertices
// actually stored live and, in request order, the keys skipped because a live
// vertex already existed. A vertex expired at application time is accepted as
// a delete-like outcome but counted as neither stored nor skipped. Liveness
// follows the live-visibility rule (#750): an expired-but-uncollected vertex
// does not block its write. The whole batch commits under a single write lock,
// so the existence check and store are atomic per key; within one batch a key's
// first live accepted write makes it live, so a later duplicate is skipped.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsent(items []VertexItem[S, T]) (written int, skipped []S) {
	written, skipped, _ = c.PutVerticesWithExpirationIfAbsentChecked(items)
	return written, skipped
}

// searchPreparation holds index-aligned compact documents across optimistic
// planning retries. A ready entry has actually been analyzed (successfully or
// with an error). An item observed expired remains not-ready: a final
// application clock may move backwards and make it live, in which case the
// retry must prepare its real document before storage can commit.
type searchPreparation struct {
	documents []search.PreparedDocument
	errs      []error
	ready     []bool
}

func (c *GraphCache[S, T]) newSearchPreparation(n int) *searchPreparation {
	if c.searchIndex == nil {
		return nil
	}
	return &searchPreparation{
		documents: make([]search.PreparedDocument, n),
		errs:      make([]error, n),
		ready:     make([]bool, n),
	}
}

// prepareSearchDocsBounded performs the expensive projection, tokenization,
// sorting, and aggregation for selected live items outside c.mu. An item that
// is expired at this optimistic sample is deliberately left not-ready. The
// final-lock revalidation can then detect a clock rollback and analyze it
// before allowing a live storage mutation (#1178).
func (c *GraphCache[S, T]) prepareSearchDocsBounded(items []VertexItem[S, T], indexes []int, now time.Time, preparation *searchPreparation) {
	if preparation == nil {
		return
	}
	for _, i := range indexes {
		if preparation.ready[i] {
			continue
		}
		if !vertexItemLiveAt(items[i], now) {
			continue
		}
		preparation.ready[i] = true
		preparation.documents[i], _, preparation.errs[i] = c.searchIndex.Prepare(c.searchExtract(items[i].Key, items[i].Value))
	}
}

// finalVertexIndexes returns the last accepted item for each key, in request
// order of those final occurrences. Only these values can affect the final
// secondary-index state; preparing earlier duplicates is wasted work.
func finalVertexIndexes[S comparable, T any](items []VertexItem[S, T], accepted []int) []int {
	last := make(map[S]int, len(accepted))
	for _, i := range accepted {
		last[items[i].Key] = i
	}
	final := make([]int, 0, len(last))
	for _, i := range accepted {
		if last[items[i].Key] == i {
			final = append(final, i)
		}
	}
	return final
}

func allVertexIndexes[S comparable, T any](items []VertexItem[S, T]) []int {
	indexes := make([]int, len(items))
	for i := range items {
		indexes[i] = i
	}
	return indexes
}

func missingPreparedIndexesAt[S comparable, T any](preparation *searchPreparation, items []VertexItem[S, T], indexes []int, now time.Time) []int {
	if preparation == nil {
		return nil
	}
	var missing []int
	for _, i := range indexes {
		if vertexItemLiveAt(items[i], now) && !preparation.ready[i] {
			missing = append(missing, i)
		}
	}
	return missing
}

// preparedItemsForIndexesAt converts any document that crossed its expiration
// while analysis or lock acquisition was in flight into the index's zero-value
// deletion marker. The caller supplies the same final application sample used
// for storage and outcomes, so postings cannot outlive a result of Expired. A
// preparation error is relevant only while its item is live at that sample: a
// crossed-expiry item is a valid delete-like overwrite and must not poison the
// batch with an analysis error for content that will never be indexed.
func preparedItemsForIndexesAt[S comparable, T any](items []VertexItem[S, T], indexes []int, preparation *searchPreparation, now time.Time) ([]search.PreparedItem[S], error) {
	if preparation == nil {
		return nil, nil
	}
	out := make([]search.PreparedItem[S], 0, len(indexes))
	for _, i := range indexes {
		prepared := search.PreparedDocument{}
		if vertexItemLiveAt(items[i], now) {
			if preparation.errs[i] != nil {
				return nil, preparation.errs[i]
			}
			prepared = preparation.documents[i]
		}
		out = append(out, search.PreparedItem[S]{
			ID:         items[i].Key,
			Prepared:   prepared,
			Expiration: items[i].Expiration,
		})
	}
	return out, nil
}

// PutVerticesWithExpirationChecked is the bounded, all-or-nothing local write
// path. Analysis and aggregate-limit validation complete before either the
// vertex cache or secondary index is mutated.
func (c *GraphCache[S, T]) PutVerticesWithExpirationChecked(items []VertexItem[S, T]) error {
	return c.putVerticesWithExpirationChecked(items, nil)
}

// PutVerticesWithExpirationOutcomesChecked is PutVerticesWithExpirationChecked
// plus an index-aligned result for every input item.
func (c *GraphCache[S, T]) PutVerticesWithExpirationOutcomesChecked(items []VertexItem[S, T]) ([]PutOutcome, error) {
	return c.putVerticesWithExpirationOutcomesChecked(items, nil)
}

func (c *GraphCache[S, T]) putVerticesWithExpirationChecked(items []VertexItem[S, T], observeLock func(time.Duration)) error {
	return c.putVerticesWithExpiration(items, observeLock, nil)
}

func (c *GraphCache[S, T]) putVerticesWithExpirationOutcomesChecked(items []VertexItem[S, T], observeLock func(time.Duration)) ([]PutOutcome, error) {
	outcomes := make([]PutOutcome, len(items))
	err := c.putVerticesWithExpiration(items, observeLock, outcomes)
	return outcomes, err
}

func (c *GraphCache[S, T]) putVerticesWithExpiration(items []VertexItem[S, T], observeLock func(time.Duration), outcomes []PutOutcome) error {
	if len(items) == 0 {
		return nil
	}
	preparationTime := time.Now()
	preparation := c.newSearchPreparation(len(items))
	var final []int
	if preparation != nil {
		final = finalVertexIndexes(items, allVertexIndexes(items))
		c.prepareSearchDocsBounded(items, final, preparationTime, preparation)
	}
	for {
		c.mu.Lock()
		lockedAt := time.Time{}
		if observeLock != nil {
			lockedAt = time.Now()
		}
		unlock := func() {
			if observeLock != nil {
				observeLock(time.Since(lockedAt))
			}
			c.mu.Unlock()
		}
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			unlock()
			return search.ErrIndexIncomplete
		}
		if c.searchIndex != nil {
			c.searchCommitMu.Lock()
		}
		// This is the authoritative application instant. It is sampled only
		// after every final commit lock is held, then shared by validation,
		// index/storage mutation, and returned outcomes.
		applicationTime := c.applicationTime()
		missing := missingPreparedIndexesAt(preparation, items, final, applicationTime)
		if len(missing) > 0 {
			if c.searchIndex != nil {
				c.searchCommitMu.Unlock()
			}
			unlock()
			c.prepareSearchDocsBounded(items, missing, applicationTime, preparation)
			continue
		}
		indexItems, err := preparedItemsForIndexesAt(items, final, preparation, applicationTime)
		if err != nil {
			if c.searchIndex != nil {
				c.searchCommitMu.Unlock()
			}
			unlock()
			return err
		}
		if c.searchIndex != nil {
			if err := c.searchIndex.ValidateManyPreparedAt(indexItems, applicationTime); err != nil {
				c.searchCommitMu.Unlock()
				unlock()
				return err
			}
			c.searchIndex.IndexManyPreparedValidatedAt(indexItems, applicationTime)
		}
		for i := range items {
			stored := c.putLocalVertexLockedAtMode(items[i].Key, items[i].Value, items[i].Expiration, applicationTime, false)
			if outcomes != nil {
				if stored {
					outcomes[i] = PutOutcomeAppliedAndLive
				} else {
					outcomes[i] = PutOutcomeExpired
				}
			}
		}
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			if err := c.rebuildSearchIndexLocked(); err != nil {
				c.searchCommitMu.Unlock()
				unlock()
				return err
			}
		}
		if c.searchIndex != nil {
			c.searchCommitMu.Unlock()
		}
		unlock()
		return nil
	}
}

// PutVerticesWithExpirationIfAbsentChecked is the conditional sibling of the
// bounded local path. Limits are evaluated only for items that would actually
// be accepted; an oversized skipped item cannot poison an otherwise valid
// batch.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsentChecked(items []VertexItem[S, T]) (written int, skipped []S, err error) {
	accepted, skipped, err := c.putVerticesIfAbsentChecked(items, hlc.Timestamp{}, false, nil, nil)
	return len(accepted), skipped, err
}

// PutVerticesWithExpirationIfAbsentOutcomesChecked is the conditional Put
// path with one exact, index-aligned outcome per input.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsentOutcomesChecked(items []VertexItem[S, T]) ([]PutOutcome, error) {
	outcomes := make([]PutOutcome, len(items))
	_, _, err := c.putVerticesIfAbsentChecked(items, hlc.Timestamp{}, false, nil, outcomes)
	return outcomes, err
}

// PutVerticesWithExpirationIfAbsentHLCChecked is the local replicated-origin
// path: accepted writes are bounded before graph or HLC mutation.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsentHLCChecked(items []VertexItem[S, T], ts hlc.Timestamp) (writtenIdx []int, skipped []S, err error) {
	return c.putVerticesIfAbsentChecked(items, ts, true, nil, nil)
}

// PutVerticesWithExpirationIfAbsentHLCOutcomesChecked is the replicated local
// conditional Put path with one exact outcome per input.
func (c *GraphCache[S, T]) PutVerticesWithExpirationIfAbsentHLCOutcomesChecked(items []VertexItem[S, T], ts hlc.Timestamp) (writtenIdx []int, outcomes []PutOutcome, err error) {
	outcomes = make([]PutOutcome, len(items))
	writtenIdx, _, err = c.putVerticesIfAbsentChecked(items, ts, true, nil, outcomes)
	return writtenIdx, outcomes, err
}

func (c *GraphCache[S, T]) putVerticesIfAbsentChecked(items []VertexItem[S, T], ts hlc.Timestamp, useHLC bool, observeLock func(time.Duration), outcomes []PutOutcome) ([]int, []S, error) {
	if len(items) == 0 {
		return nil, nil, nil
	}
	preparation := c.newSearchPreparation(len(items))
	for {
		c.mu.Lock()
		lockedAt := time.Time{}
		if observeLock != nil {
			lockedAt = time.Now()
		}
		unlock := func() {
			if observeLock != nil {
				observeLock(time.Since(lockedAt))
			}
			c.mu.Unlock()
		}
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			unlock()
			return nil, nil, search.ErrIndexIncomplete
		}
		// This preliminary sample is only a cheap plan used to avoid analyzing
		// stable skips. It is deliberately not the injectable application clock:
		// the single authoritative sample for a successful commit is taken below
		// after every final lock is held.
		planningTime := time.Now()
		accepted, _, skipped := c.planVerticesIfAbsentLocked(items, ts, useHLC, planningTime, nil)
		missing := missingPreparedIndexesAt(preparation, items, accepted, planningTime)
		if len(missing) > 0 {
			unlock()
			c.prepareSearchDocsBounded(items, missing, planningTime, preparation)
			continue
		}
		if c.searchIndex != nil {
			c.searchCommitMu.Lock()
		}
		// Re-plan against the final application sample after waiting for every
		// commit lock. A live existing value may have expired while preparation
		// ran, and an accepted input may itself have crossed its expiration.
		applicationTime := time.Now()
		if outcomes != nil {
			applicationTime = c.applicationTime()
		}
		finalAccepted, finalApplied, finalSkipped := c.planVerticesIfAbsentLocked(items, ts, useHLC, applicationTime, outcomes)
		missing = missingPreparedIndexesAt(preparation, items, finalAccepted, applicationTime)
		if len(missing) > 0 {
			if c.searchIndex != nil {
				c.searchCommitMu.Unlock()
			}
			unlock()
			c.prepareSearchDocsBounded(items, missing, applicationTime, preparation)
			continue
		}
		accepted, skipped = finalAccepted, finalSkipped
		indexItems, err := preparedItemsForIndexesAt(items, finalApplied, preparation, applicationTime)
		if err != nil {
			if c.searchIndex != nil {
				c.searchCommitMu.Unlock()
			}
			unlock()
			return nil, nil, err
		}
		if c.searchIndex != nil {
			if err := c.searchIndex.ValidateManyPreparedAt(indexItems, applicationTime); err != nil {
				c.searchCommitMu.Unlock()
				unlock()
				return nil, nil, err
			}
			c.searchIndex.IndexManyPreparedValidatedAt(indexItems, applicationTime)
		}
		for _, i := range finalApplied {
			item := items[i]
			stored := c.putLocalVertexLockedAtMode(item.Key, item.Value, item.Expiration, applicationTime, false)
			if useHLC {
				if stored {
					c.recordVertexHLCLocked(item.Key, ts)
					c.clearVertexCausalBarrierLocked(item.Key)
				} else {
					c.applyVertexCausalBarrierLocked(item.Key, ts)
				}
				c.clearVertexTombstoneLocked(item.Key)
			}
		}
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			if err := c.rebuildSearchIndexLocked(); err != nil {
				c.searchCommitMu.Unlock()
				unlock()
				return nil, nil, err
			}
		}
		if c.searchIndex != nil {
			c.searchCommitMu.Unlock()
		}
		unlock()
		return accepted, skipped, nil
	}
}

// planVerticesIfAbsentLocked performs only cheap liveness/causality checks.
// Callers release c.mu before preparing the returned documents, then retry the
// plan before commit; a stable skipped item is therefore never analyzed.
func (c *GraphCache[S, T]) planVerticesIfAbsentLocked(items []VertexItem[S, T], ts hlc.Timestamp, useHLC bool, now time.Time, outcomes []PutOutcome) (accepted, applied []int, skipped []S) {
	reserved := make(map[S]struct{})
	for i, item := range items {
		_, duplicate := reserved[item.Key]
		if c.vertices.HasAt(item.Key, now) || duplicate {
			skipped = append(skipped, item.Key)
			if outcomes != nil {
				outcomes[i] = PutOutcomeConditionNotMet
			}
			continue
		}
		if useHLC && !c.vertexWriteAllowedLocked(item.Key, ts) {
			skipped = append(skipped, item.Key)
			if outcomes != nil {
				outcomes[i] = PutOutcomeSuperseded
			}
			continue
		}
		applied = append(applied, i)
		if !vertexItemLiveAt(item, now) {
			if outcomes != nil {
				outcomes[i] = PutOutcomeExpired
			}
			continue
		}
		accepted = append(accepted, i)
		if outcomes != nil {
			outcomes[i] = PutOutcomeAppliedAndLive
		}
		reserved[item.Key] = struct{}{}
	}
	return accepted, applied, skipped
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
	c.putEdgesWithExpiration(items, nil)
}

// PutEdgesWithExpirationOutcomes is PutEdgesWithExpiration plus one result per
// input. Liveness is sampled once for the batch under the final write lock.
func (c *GraphCache[S, T]) PutEdgesWithExpirationOutcomes(items []EdgeItem[S]) []PutOutcome {
	outcomes := make([]PutOutcome, len(items))
	c.putEdgesWithExpiration(items, outcomes)
	return outcomes
}

func (c *GraphCache[S, T]) putEdgesWithExpiration(items []EdgeItem[S], outcomes []PutOutcome) {
	if len(items) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.applicationTime()
	for i, it := range items {
		stored := c.putEdgeLockedAt(it.Tail, it.Head, it.Weight, it.Expiration, now)
		if outcomes != nil {
			if stored {
				outcomes[i] = PutOutcomeAppliedAndLive
			} else {
				outcomes[i] = PutOutcomeExpired
			}
		}
	}
}

// DeleteVertices removes every supplied vertex under a single write lock
// and returns the count of keys that were actually present (and therefore
// deleted). Concurrent SearchVertices readers observe either the pre-batch or
// the post-batch state through searchCommitMu. Vertex-owned side indexes (dict refs, prefix radix,
// search postings) are cleaned in one pass via the batch eviction hook so a
// large delete pays one acquisition per index instead of one per key (#738).
func (c *GraphCache[S, T]) DeleteVertices(keys []S) int {
	if len(keys) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		c.searchCommitMu.Lock()
		defer c.searchCommitMu.Unlock()
	}
	n := len(c.vertices.DeleteMany(keys))
	for _, key := range keys {
		c.clearVertexCausalBarrierLocked(key)
		if c.vertexHLC != nil {
			delete(c.vertexHLC, key)
		}
	}
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
		c.clearEdgeCausalBarrierLocked(k.Tail, k.Head)
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
	rejected, _ = c.putVerticesWithExpirationHLC(items, ts, false, nil)
	return rejected
}

// PutVerticesWithExpirationHLCOutcomes is the non-strict replicated apply
// path with one exact outcome per input.
func (c *GraphCache[S, T]) PutVerticesWithExpirationHLCOutcomes(items []VertexItem[S, T], ts hlc.Timestamp) []PutOutcome {
	outcomes := make([]PutOutcome, len(items))
	_, _ = c.putVerticesWithExpirationHLC(items, ts, false, outcomes)
	return outcomes
}

// PutVerticesWithExpirationHLCChecked is used for locally-originated writes:
// unlike replication apply it rejects a budget overflow atomically.
func (c *GraphCache[S, T]) PutVerticesWithExpirationHLCChecked(items []VertexItem[S, T], ts hlc.Timestamp) (rejected int, err error) {
	return c.putVerticesWithExpirationHLC(items, ts, true, nil)
}

// PutVerticesWithExpirationHLCOutcomesChecked is the strict local HLC path
// with an exact, index-aligned result for every item.
func (c *GraphCache[S, T]) PutVerticesWithExpirationHLCOutcomesChecked(items []VertexItem[S, T], ts hlc.Timestamp) ([]PutOutcome, error) {
	outcomes := make([]PutOutcome, len(items))
	_, err := c.putVerticesWithExpirationHLC(items, ts, true, outcomes)
	return outcomes, err
}

func (c *GraphCache[S, T]) putVerticesWithExpirationHLC(items []VertexItem[S, T], ts hlc.Timestamp, strict bool, outcomes []PutOutcome) (rejected int, err error) {
	if len(items) == 0 {
		return 0, nil
	}
	preparation := c.newSearchPreparation(len(items))
	for {
		c.mu.Lock()
		accepted, rejected := c.planVerticesHLCLocked(items, ts)
		planningTime := time.Now()
		if outcomes != nil {
			for i := range outcomes {
				outcomes[i] = PutOutcomeSuperseded
			}
		}
		var final []int
		var indexErr error
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			indexErr = search.ErrIndexIncomplete
		} else if preparation != nil {
			final = finalVertexIndexes(items, accepted)
			missing := missingPreparedIndexesAt(preparation, items, final, planningTime)
			if len(missing) > 0 {
				c.mu.Unlock()
				c.prepareSearchDocsBounded(items, missing, planningTime, preparation)
				continue
			}
		}
		if c.searchIndex != nil {
			c.searchCommitMu.Lock()
		}
		applicationTime := c.applicationTime()
		if indexErr == nil {
			missing := missingPreparedIndexesAt(preparation, items, final, applicationTime)
			if len(missing) > 0 {
				if c.searchIndex != nil {
					c.searchCommitMu.Unlock()
				}
				c.mu.Unlock()
				c.prepareSearchDocsBounded(items, missing, applicationTime, preparation)
				continue
			}
		}
		indexItems, preparationErr := preparedItemsForIndexesAt(items, final, preparation, applicationTime)
		if indexErr == nil {
			indexErr = preparationErr
		}
		if indexErr == nil && c.searchIndex != nil {
			indexErr = c.searchIndex.ValidateManyPreparedAt(indexItems, applicationTime)
		}
		if strict && indexErr != nil {
			if c.searchIndex != nil {
				c.searchCommitMu.Unlock()
			}
			c.mu.Unlock()
			return rejected, indexErr
		}
		if c.searchIndex != nil && indexErr == nil {
			c.searchIndex.IndexManyPreparedValidatedAt(indexItems, applicationTime)
		}
		if indexErr != nil && c.searchIndex != nil {
			c.searchIndex.MarkIncomplete()
		}
		for _, i := range accepted {
			item := items[i]
			stored := false
			if !item.CausalBarrier {
				stored = c.putLocalVertexLockedAtMode(item.Key, item.Value, item.Expiration, applicationTime, false)
			} else {
				// Search has already committed the final per-key document for
				// this ordered batch. Delete storage through the preindexed seam
				// so an earlier barrier for a duplicate key cannot fire the
				// eviction hook and erase a later live document.
				c.deleteVertexStoragePreindexedLocked(item.Key)
				c.recordVertexCausalBarrierLocked(item.Key, ts)
				if c.vertexHLC != nil {
					delete(c.vertexHLC, item.Key)
				}
			}
			if outcomes != nil {
				if stored {
					outcomes[i] = PutOutcomeAppliedAndLive
				} else {
					outcomes[i] = PutOutcomeExpired
				}
			}
			if stored {
				c.recordVertexHLCLocked(item.Key, ts)
				c.clearVertexCausalBarrierLocked(item.Key)
			} else if !item.CausalBarrier {
				c.applyVertexCausalBarrierLocked(item.Key, ts)
			}
			c.clearVertexTombstoneLocked(item.Key)
		}
		if c.searchIndex != nil {
			c.searchCommitMu.Unlock()
		}
		c.mu.Unlock()
		return rejected, nil
	}
}

func (c *GraphCache[S, T]) planVerticesHLCLocked(items []VertexItem[S, T], ts hlc.Timestamp) (accepted []int, rejected int) {
	accepted = make([]int, 0, len(items))
	for i := range items {
		if !c.vertexWriteAllowedLocked(items[i].Key, ts) {
			rejected++
			continue
		}
		accepted = append(accepted, i)
	}
	return accepted, rejected
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
// actually stored live (in request order) so legacy callers can count them,
// and the keys skipped because a live vertex already existed. Exact-outcome
// callers additionally receive EXPIRED for accepted delete-like overwrites;
// those must be replicated even though they are absent from writtenIdx. Two
// concurrent if-absent writers on different nodes can both report their write as accepted locally;
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
	return c.putEdgesWithExpirationHLC(items, ts, nil)
}

// PutEdgesWithExpirationHLCOutcomes is the local/replicated LWW path with one
// exact result per item. All accepted items share one final-lock liveness sample.
func (c *GraphCache[S, T]) PutEdgesWithExpirationHLCOutcomes(items []EdgeItem[S], ts hlc.Timestamp) []PutOutcome {
	outcomes := make([]PutOutcome, len(items))
	c.putEdgesWithExpirationHLC(items, ts, outcomes)
	return outcomes
}

func (c *GraphCache[S, T]) putEdgesWithExpirationHLC(items []EdgeItem[S], ts hlc.Timestamp, outcomes []PutOutcome) (rejected int) {
	if len(items) == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.applicationTime()
	for i, it := range items {
		live := edgeItemLiveAt(it, now)
		allowed := false
		if live {
			allowed = c.edgeWriteAllowedLocked(it.Tail, it.Head, ts)
		} else {
			allowed = c.edgePutWriteAllowedLocked(it.Tail, it.Head, ts)
		}
		if !allowed {
			rejected++
			if outcomes != nil {
				outcomes[i] = PutOutcomeSuperseded
			}
			continue
		}
		if !live {
			c.applyEdgeCausalBarrierLocked(it.Tail, it.Head, ts)
			c.clearEdgeTombstoneLocked(it.Tail, it.Head)
			if outcomes != nil {
				outcomes[i] = PutOutcomeExpired
			}
			continue
		}
		if c.putEdgeHLCLocked(it.Tail, it.Head, it.Weight, it.Expiration, ts) {
			c.clearEdgeCausalBarrierLocked(it.Tail, it.Head)
			c.clearEdgeTombstoneLocked(it.Tail, it.Head)
			if outcomes != nil {
				outcomes[i] = PutOutcomeAppliedAndLive
			}
		} else {
			rejected++
			if outcomes != nil {
				outcomes[i] = PutOutcomeSuperseded
			}
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
			// The additive bucket has no replacement Put watermark. Retain an
			// accepted-expired Put barrier so a delayed older Put remains fenced.
			c.clearEdgeTombstoneLocked(it.Tail, it.Head)
			continue
		}
		deduped++
	}
	return effective, deduped
}
