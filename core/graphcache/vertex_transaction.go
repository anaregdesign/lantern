package graphcache

import (
	"context"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// IndexedVertexPut identifies one causally admitted Put request item. Outcome
// is always PutOutcomeAppliedAndLive or PutOutcomeExpired. Items rejected by
// if-absent or a newer causal floor remain in Outcomes but are omitted here.
type IndexedVertexPut[S comparable, T any] struct {
	Index   int
	Item    VertexItem[S, T]
	Outcome PutOutcome
}

// VertexPutStageResult is a detached result of a staged Put. Outcomes has one
// entry per input item. Accepted retains every admitted request position in
// request order, including duplicate keys and accepted-expired effects.
type VertexPutStageResult[S comparable, T any] struct {
	Outcomes            []PutOutcome
	Accepted            []IndexedVertexPut[S, T]
	LifecycleReductions []bool
}

// VertexPutTransaction owns GraphCache visibility locks until Commit or Abort.
// It is single-owner and must not be used concurrently. While open, the caller
// must not invoke another GraphCache method on the same cache. Callers should
// defer Abort immediately after Begin succeeds; Abort is safe after Commit.
type VertexPutTransaction[S comparable, T any] struct {
	stage  *stagedVertexMutation[S, T]
	result VertexPutStageResult[S, T]
	stagedMutationLifecycle
}

// BeginVertexPut validates and stages a locally-originated Vertex Put batch.
// When ifAbsent is true, request-order SET NX semantics are evaluated at one
// authoritative application-time sample. Local causal-capacity limits are
// enforced before the staged state is applied.
func (c *GraphCache[S, T]) BeginVertexPut(
	items []VertexItem[S, T], ts hlc.Timestamp, ifAbsent bool,
) (*VertexPutTransaction[S, T], error) {
	return c.BeginVertexPutContext(context.Background(), items, ts, ifAbsent)
}

// BeginVertexPutContext is BeginVertexPut with cancellation during planning.
func (c *GraphCache[S, T]) BeginVertexPutContext(
	ctx context.Context, items []VertexItem[S, T], ts hlc.Timestamp, ifAbsent bool,
) (*VertexPutTransaction[S, T], error) {
	return c.beginVertexPut(ctx, items, ts, ifAbsent, true, false, nil)
}

// BeginVertexPutInAppliedViewContext stages a local Put against the applied
// graph. The serving publisher uses this view while already-applied peer
// effects wait for their original WAL publication.
func (c *GraphCache[S, T]) BeginVertexPutInAppliedViewContext(ctx context.Context, items []VertexItem[S, T], ts hlc.Timestamp, ifAbsent bool) (*VertexPutTransaction[S, T], error) {
	return c.beginVertexPut(ctx, items, ts, ifAbsent, true, true, nil)
}

// BeginReplicatedVertexPut stages an already-committed receiver projection.
// It never re-evaluates the origin's if-absent condition and does not apply
// local causal-capacity admission limits. Search health, document analysis,
// and aggregate index limits remain fail-closed: receipt staging never returns
// a transaction whose accepted graph effect cannot be represented locally.
// Pass only the origin's accepted live/barrier effects, preserving their order.
func (c *GraphCache[S, T]) BeginReplicatedVertexPut(
	items []VertexItem[S, T], ts hlc.Timestamp,
) (*VertexPutTransaction[S, T], error) {
	return c.BeginReplicatedVertexPutContext(context.Background(), items, ts)
}

// BeginReplicatedVertexPutContext is BeginReplicatedVertexPut with cancellation during planning.
func (c *GraphCache[S, T]) BeginReplicatedVertexPutContext(
	ctx context.Context, items []VertexItem[S, T], ts hlc.Timestamp,
) (*VertexPutTransaction[S, T], error) {
	return c.beginVertexPut(ctx, items, ts, false, false, false, nil)
}

func (c *GraphCache[S, T]) beginVertexPut(
	ctx context.Context, items []VertexItem[S, T], ts hlc.Timestamp, ifAbsent, strict, appliedView bool,
	mixed *stagedGraphMutation[S, T],
) (*VertexPutTransaction[S, T], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.publicationGate == nil {
		return nil, errors.New("graphcache: staged Vertex Put requires a publication gate")
	}
	if strict {
		for _, item := range items {
			if item.CausalBarrier {
				return nil, errors.New("graphcache: local Vertex Put cannot contain a causal barrier")
			}
		}
	}
	if len(items) == 0 && mixed == nil {
		c.mu.Lock()
		c.publicationGate.Lock()
		c.searchCommitMu.Lock()
		return &VertexPutTransaction[S, T]{
			stage: &stagedVertexMutation[S, T]{cache: c},
			result: VertexPutStageResult[S, T]{
				Outcomes: []PutOutcome{},
				Accepted: []IndexedVertexPut[S, T]{},
			},
		}, nil
	}

	vertexKeys := vertexItemKeys(items)
	keys := vertexKeys
	if mixed != nil {
		keys = mixed.unionKeys(items)
	}
	projections, prefixEnabled := c.prepareStagedVertexProjections(keys)
	preparation := c.newSearchPreparation(len(items))
	var oldSearch map[S]stagedVertexSearchBefore[T]
	if preparation != nil {
		oldSearch = make(map[S]stagedVertexSearchBefore[T])
	}
	lockState := 0
	var pendingStage *stagedVertexMutation[S, T]
	defer func() {
		if lockState == 0 {
			return
		}
		defer c.mu.Unlock()
		if lockState >= 2 {
			defer c.publicationGate.Unlock()
		}
		if lockState >= 3 {
			defer c.searchCommitMu.Unlock()
		}
		if pendingStage != nil {
			if mixed != nil && mixed.edgeStage != nil {
				mixed.edgeStage.rollbackEdgesLocked()
			}
			pendingStage.rollbackLocked()
		}
	}()
	unlockMu := func() {
		lockState = 0
		c.mu.Unlock()
	}
	unlockAll := func() {
		lockState = 0
		c.unlockStagedVertexBegin()
	}

	for {
		if mixed != nil {
			if err := mixed.checkPreparation(); err != nil {
				return nil, err
			}
		}
		c.mu.Lock()
		lockState = 1
		if mixed != nil {
			mixed.lockedAt = time.Now()
			if err := mixed.checkBudget(); err != nil {
				unlockMu()
				return nil, err
			}
		}
		if (c.prefixIndex != nil) != prefixEnabled {
			unlockMu()
			return nil, errors.New("graphcache: vertex prefix policy changed during staging")
		}
		if !c.searchPreparationCurrentLocked(preparation) {
			unlockMu()
			preparation = c.newSearchPreparation(len(items))
			if preparation == nil {
				oldSearch = nil
			} else {
				oldSearch = make(map[S]stagedVertexSearchBefore[T])
			}
			continue
		}
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			unlockMu()
			return nil, search.ErrIndexIncomplete
		}

		var before map[S]storedVertexBefore
		if !appliedView {
			var err error
			before, err = c.storedVertexPreimagesLocked(ctx, vertexKeys)
			if err != nil {
				unlockMu()
				return nil, err
			}
		}
		applied := c.planStagedVertexPutLocked(items, ts, ifAbsent, time.Now(), nil, before)
		final := finalVertexIndexes(items, applied)
		missing := missingPreparedIndexesAt(preparation, items, final, time.Now())
		oldMissing := c.missingStagedVertexSearchBeforeLocked(vertexKeysAt(items, final), oldSearch)
		if len(missing) > 0 || len(oldMissing) > 0 {
			unlockMu()
			if mixed != nil {
				if err := mixed.prepareSearch(items, missing, time.Now(), preparation, oldMissing, oldSearch); err != nil {
					return nil, err
				}
			} else {
				c.prepareSearchDocsBounded(items, missing, time.Now(), preparation)
				prepareStagedVertexSearchBefore(preparation, oldMissing, oldSearch)
			}
			continue
		}

		c.publicationGate.Lock()
		lockState = 2
		c.searchCommitMu.Lock()
		lockState = 3
		if err := ctx.Err(); err != nil {
			unlockAll()
			return nil, err
		}
		applicationTime := c.applicationTime()
		outcomes := make([]PutOutcome, len(items))
		applied = c.planStagedVertexPutLocked(items, ts, ifAbsent, applicationTime, outcomes, before)
		readLifetime := c.vertexLifetimeReaderLocked(before, applicationTime)
		if err := validateVertexLifecycle(items, applied, applicationTime, false, readLifetime); err != nil {
			unlockAll()
			return nil, err
		}
		if strict && ts != (hlc.Timestamp{}) {
			keys := vertexKeysAt(items, applied)
			if err := c.checkVertexCausalCapacityLocked(keys); err != nil {
				unlockAll()
				return nil, err
			}
		}
		if len(applied) == 0 && mixed == nil {
			stage := &stagedVertexMutation[S, T]{cache: c}
			lockState = 0
			return &VertexPutTransaction[S, T]{
				stage: stage,
				result: VertexPutStageResult[S, T]{
					Outcomes: outcomes,
					Accepted: []IndexedVertexPut[S, T]{},
				},
			}, nil
		}

		final = finalVertexIndexes(items, applied)
		missing = missingPreparedIndexesAt(preparation, items, final, applicationTime)
		oldMissing = c.missingStagedVertexSearchBeforeLocked(vertexKeysAt(items, final), oldSearch)
		if len(missing) > 0 || len(oldMissing) > 0 {
			unlockAll()
			if mixed != nil {
				if err := mixed.prepareSearch(items, missing, applicationTime, preparation, oldMissing, oldSearch); err != nil {
					return nil, err
				}
			} else {
				c.prepareSearchDocsBounded(items, missing, applicationTime, preparation)
				prepareStagedVertexSearchBefore(preparation, oldMissing, oldSearch)
			}
			continue
		}

		searchAfter, err := preparedItemsForIndexesAt(items, final, preparation, applicationTime)
		if err != nil {
			unlockAll()
			return nil, err
		}
		searchBefore, err := stagedVertexSearchBeforeItems(
			vertexKeysAt(items, final), oldSearch, applicationTime,
		)
		if err != nil {
			unlockAll()
			return nil, err
		}
		if c.searchIndex != nil {
			if err := c.searchIndex.ValidateManyPreparedAt(searchAfter, applicationTime); err != nil {
				unlockAll()
				return nil, err
			}
		}

		accepted := make([]IndexedVertexPut[S, T], len(applied))
		reductions := vertexLifecycleReductions(items, applied, applicationTime, readLifetime)
		for i, index := range applied {
			accepted[i] = IndexedVertexPut[S, T]{
				Index: index, Item: items[index], Outcome: outcomes[index],
			}
		}
		stage, err := c.prepareStagedVertexMutationLocked(
			vertexKeysAt(items, applied), projections, searchBefore, searchAfter, applicationTime,
		)
		if err != nil {
			unlockAll()
			return nil, err
		}
		if mixed != nil {
			if err := mixed.prepareLocked(c, items, applied, outcomes, stage, ts, strict); err != nil {
				unlockAll()
				return nil, err
			}
		}
		pendingStage = stage
		stage.applyPutLocked(items, applied, outcomes, ts)
		if mixed != nil {
			mixed.edgeStage.applyOnlyEdgesLocked()
			if err := mixed.checkBudget(); err != nil {
				return nil, err
			}
		}
		lockState = 0
		return &VertexPutTransaction[S, T]{
			stage: stage,
			result: VertexPutStageResult[S, T]{
				Outcomes:            outcomes,
				Accepted:            accepted,
				LifecycleReductions: reductions,
			},
		}, nil
	}
}

func (c *GraphCache[S, T]) planStagedVertexPutLocked(
	items []VertexItem[S, T], ts hlc.Timestamp, ifAbsent bool, now time.Time, outcomes []PutOutcome, before map[S]storedVertexBefore,
) []int {
	if ifAbsent {
		_, applied, _ := c.planVerticesIfAbsentLocked(items, ts, true, now, outcomes, before)
		return applied
	}
	applied, _ := planVerticesHLC(items, c.vertexWriteAdmissionLocked(ts, before))
	if outcomes == nil {
		return applied
	}
	for i := range outcomes {
		outcomes[i] = PutOutcomeSuperseded
	}
	for _, index := range applied {
		if vertexItemLiveAt(items[index], now) {
			outcomes[index] = PutOutcomeAppliedAndLive
		} else {
			outcomes[index] = PutOutcomeExpired
		}
	}
	return applied
}

// Result returns defensive copies of the request-aligned outcomes and indexed
// accepted effects.
func (tx *VertexPutTransaction[S, T]) Result() VertexPutStageResult[S, T] {
	return VertexPutStageResult[S, T]{
		Outcomes:            append([]PutOutcome{}, tx.result.Outcomes...),
		Accepted:            append([]IndexedVertexPut[S, T]{}, tx.result.Accepted...),
		LifecycleReductions: append([]bool(nil), tx.result.LifecycleReductions...),
	}
}

// Commit publishes the staged Put by releasing its visibility locks.
func (tx *VertexPutTransaction[S, T]) Commit() {
	if tx.closed {
		panic("graphcache: staged Vertex Put committed after close")
	}
	tx.stagedMutationLifecycle.commit(tx.stage.release)
}

// Abort restores the exact pre-Begin logical state and releases the visibility
// locks. It is idempotent so it can be deferred by the caller.
func (tx *VertexPutTransaction[S, T]) Abort() {
	if tx == nil || tx.closed {
		return
	}
	tx.stagedMutationLifecycle.abort(tx.stage.rollbackWithEvictionsLocked, tx.stage.release)
}

// IndexedVertexDelete identifies one causally admitted Delete request item.
// Accepted absent keys and duplicate positions remain distinct and ordered.
type IndexedVertexDelete[S comparable] struct {
	Index int
	Key   S
}

// VertexDeleteStageResult is a detached result of a staged exact Delete.
type VertexDeleteStageResult[S comparable] struct {
	Existed  []bool
	Accepted []IndexedVertexDelete[S]
}

// VertexDeleteTransaction owns GraphCache visibility locks until Commit or
// Abort. Its ownership and close behavior match EdgeDeleteTransaction.
type VertexDeleteTransaction[S comparable, T any] struct {
	stage  *stagedVertexMutation[S, T]
	result VertexDeleteStageResult[S]
	stagedMutationLifecycle
}

// BeginVertexDelete validates and stages a locally-originated exact Vertex
// Delete batch, enforcing local causal-capacity limits before publication.
func (c *GraphCache[S, T]) BeginVertexDelete(
	keys []S, ts hlc.Timestamp, expiration time.Time,
) (*VertexDeleteTransaction[S, T], error) {
	return c.BeginVertexDeleteContext(context.Background(), keys, ts, expiration)
}

// BeginVertexDeleteContext is BeginVertexDelete with cancellation during planning.
func (c *GraphCache[S, T]) BeginVertexDeleteContext(
	ctx context.Context, keys []S, ts hlc.Timestamp, expiration time.Time,
) (*VertexDeleteTransaction[S, T], error) {
	return c.beginVertexDelete(ctx, keys, ts, expiration, true, false)
}

// BeginVertexDeleteInAppliedViewContext stages an exact local Delete against
// the graph-applied state while the publication owner has pending WAL work.
// The owner must serialize the stage and WAL attempt with that pending work.
func (c *GraphCache[S, T]) BeginVertexDeleteInAppliedViewContext(
	ctx context.Context, keys []S, ts hlc.Timestamp, expiration time.Time,
) (*VertexDeleteTransaction[S, T], error) {
	return c.beginVertexDelete(ctx, keys, ts, expiration, true, true)
}

// BeginReplicatedVertexDelete stages a certified remote exact Delete without
// applying local causal-capacity limits. Unlike legacy graph-first convergence,
// receipt staging remains fail-closed when the search index is incomplete or
// the prior document cannot be analyzed for exact rollback.
func (c *GraphCache[S, T]) BeginReplicatedVertexDelete(
	keys []S, ts hlc.Timestamp, expiration time.Time,
) (*VertexDeleteTransaction[S, T], error) {
	return c.BeginReplicatedVertexDeleteContext(context.Background(), keys, ts, expiration)
}

// BeginReplicatedVertexDeleteContext is BeginReplicatedVertexDelete with cancellation during planning.
func (c *GraphCache[S, T]) BeginReplicatedVertexDeleteContext(
	ctx context.Context, keys []S, ts hlc.Timestamp, expiration time.Time,
) (*VertexDeleteTransaction[S, T], error) {
	return c.beginVertexDelete(ctx, keys, ts, expiration, false, false)
}

func (c *GraphCache[S, T]) beginVertexDelete(
	ctx context.Context, keys []S, ts hlc.Timestamp, expiration time.Time, strict, appliedView bool,
) (*VertexDeleteTransaction[S, T], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.publicationGate == nil {
		return nil, errors.New("graphcache: staged Vertex Delete requires a publication gate")
	}
	if len(keys) == 0 {
		c.mu.Lock()
		c.publicationGate.Lock()
		c.searchCommitMu.Lock()
		return &VertexDeleteTransaction[S, T]{
			stage: &stagedVertexMutation[S, T]{cache: c},
			result: VertexDeleteStageResult[S]{
				Existed:  []bool{},
				Accepted: []IndexedVertexDelete[S]{},
			},
		}, nil
	}

	projections, prefixEnabled := c.prepareStagedVertexProjections(keys)
	preparation := c.newSearchPreparation(0)
	var oldSearch map[S]stagedVertexSearchBefore[T]
	if preparation != nil {
		oldSearch = make(map[S]stagedVertexSearchBefore[T])
	}
	lockState := 0
	var pendingStage *stagedVertexMutation[S, T]
	defer func() {
		if lockState == 0 {
			return
		}
		defer c.mu.Unlock()
		if lockState >= 2 {
			defer c.publicationGate.Unlock()
		}
		if lockState >= 3 {
			defer c.searchCommitMu.Unlock()
		}
		if pendingStage != nil {
			pendingStage.rollbackLocked()
		}
	}()
	unlockMu := func() {
		lockState = 0
		c.mu.Unlock()
	}
	unlockAll := func() {
		lockState = 0
		c.unlockStagedVertexBegin()
	}
	for {
		c.mu.Lock()
		lockState = 1
		if (c.prefixIndex != nil) != prefixEnabled {
			unlockMu()
			return nil, errors.New("graphcache: vertex prefix policy changed during staging")
		}
		if !c.searchPreparationCurrentLocked(preparation) {
			unlockMu()
			preparation = c.newSearchPreparation(0)
			if preparation == nil {
				oldSearch = nil
			} else {
				oldSearch = make(map[S]stagedVertexSearchBefore[T])
			}
			continue
		}
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			unlockMu()
			return nil, search.ErrIndexIncomplete
		}

		var before map[S]storedVertexBefore
		if !appliedView {
			var err error
			before, err = c.storedVertexPreimagesLocked(ctx, keys)
			if err != nil {
				unlockMu()
				return nil, err
			}
		}
		_, acceptedIndexes := c.planExactVertexDeleteLocked(keys, ts, before)
		acceptedKeys := vertexKeysAtIndexes(keys, acceptedIndexes)
		oldMissing := c.missingStagedVertexSearchBeforeLocked(acceptedKeys, oldSearch)
		if len(oldMissing) > 0 {
			unlockMu()
			prepareStagedVertexSearchBefore(preparation, oldMissing, oldSearch)
			continue
		}

		c.publicationGate.Lock()
		lockState = 2
		c.searchCommitMu.Lock()
		lockState = 3
		if err := ctx.Err(); err != nil {
			unlockAll()
			return nil, err
		}
		applicationTime := c.applicationTime()
		existed, acceptedIndexes := c.planExactVertexDeleteLocked(keys, ts, before)
		acceptedKeys = vertexKeysAtIndexes(keys, acceptedIndexes)
		if strict && ts != (hlc.Timestamp{}) {
			if err := c.checkVertexCausalCapacityLocked(acceptedKeys); err != nil {
				unlockAll()
				return nil, err
			}
		}
		if len(acceptedIndexes) == 0 {
			lockState = 0
			return &VertexDeleteTransaction[S, T]{
				stage: &stagedVertexMutation[S, T]{cache: c},
				result: VertexDeleteStageResult[S]{
					Existed:  existed,
					Accepted: []IndexedVertexDelete[S]{},
				},
			}, nil
		}

		oldMissing = c.missingStagedVertexSearchBeforeLocked(acceptedKeys, oldSearch)
		if len(oldMissing) > 0 {
			unlockAll()
			prepareStagedVertexSearchBefore(preparation, oldMissing, oldSearch)
			continue
		}
		searchBefore, err := stagedVertexSearchBeforeItems(
			acceptedKeys, oldSearch, applicationTime,
		)
		if err != nil {
			unlockAll()
			return nil, err
		}
		searchAfter := stagedVertexSearchDeletes(acceptedKeys, c.searchIndex != nil)
		if c.searchIndex != nil {
			if err := c.searchIndex.ValidateManyPreparedAt(searchAfter, applicationTime); err != nil {
				unlockAll()
				return nil, err
			}
		}

		accepted := make([]IndexedVertexDelete[S], len(acceptedIndexes))
		for i, index := range acceptedIndexes {
			accepted[i] = IndexedVertexDelete[S]{Index: index, Key: keys[index]}
		}
		stage, err := c.prepareStagedVertexMutationLocked(
			acceptedKeys, projections, searchBefore, searchAfter, applicationTime,
		)
		if err != nil {
			unlockAll()
			return nil, err
		}
		pendingStage = stage
		stage.applyDeleteLocked(keys, acceptedIndexes, ts, expiration)
		lockState = 0
		return &VertexDeleteTransaction[S, T]{
			stage: stage,
			result: VertexDeleteStageResult[S]{
				Existed:  existed,
				Accepted: accepted,
			},
		}, nil
	}
}

func (c *GraphCache[S, T]) planExactVertexDeleteLocked(
	keys []S, ts hlc.Timestamp, before map[S]storedVertexBefore,
) (existed []bool, accepted []int) {
	existed = make([]bool, len(keys))
	accepted = make([]int, 0, len(keys))
	present := make(map[S]bool, len(keys))
	for _, key := range keys {
		if _, seen := present[key]; seen {
			continue
		}
		if before != nil {
			present[key] = before[key].present
		} else {
			_, _, present[key], _ = c.vertices.PeekResident(key)
		}
	}
	allowed := c.vertexWriteAdmissionLocked(ts, before)
	for i, key := range keys {
		if !allowed(key) {
			continue
		}
		accepted = append(accepted, i)
		existed[i] = present[key]
		present[key] = false
	}
	return existed, accepted
}

// Result returns defensive copies of the request-aligned observations and
// accepted indexed transitions.
func (tx *VertexDeleteTransaction[S, T]) Result() VertexDeleteStageResult[S] {
	return VertexDeleteStageResult[S]{
		Existed:  append([]bool{}, tx.result.Existed...),
		Accepted: append([]IndexedVertexDelete[S]{}, tx.result.Accepted...),
	}
}

// Commit publishes the staged Delete by releasing its visibility locks.
func (tx *VertexDeleteTransaction[S, T]) Commit() {
	if tx.closed {
		panic("graphcache: staged Vertex Delete committed after close")
	}
	tx.stagedMutationLifecycle.commit(tx.stage.release)
}

// Abort restores the exact pre-Begin logical state and releases the visibility
// locks. It is idempotent so it can be deferred by the caller.
func (tx *VertexDeleteTransaction[S, T]) Abort() {
	if tx == nil || tx.closed {
		return
	}
	tx.stagedMutationLifecycle.abort(tx.stage.rollbackWithEvictionsLocked, tx.stage.release)
}
