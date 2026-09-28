package graphcache

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// IndexedEdgeContributionDelete identifies every causally admitted request
// position, including an absent or duplicated contribution.
type IndexedEdgeContributionDelete[S comparable] struct {
	Index int
	Key   EdgeContributionKey[S]
}

type EdgeContributionDeleteStageResult[S comparable] struct {
	Existed  []bool
	Accepted []IndexedEdgeContributionDelete[S]
}

func validateEdgeContributionKeys[S comparable](keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time) error {
	for _, key := range keys {
		if key.ContribID.IsZero() {
			return errors.New("graphcache: contribution deletion requires a nonzero ContribID")
		}
	}
	if len(keys) > 0 && ts != (hlc.Timestamp{}) && expiration.IsZero() {
		return errors.New("graphcache: causal contribution deletion requires a D4 deadline")
	}
	return nil
}

func (c *GraphCache[S, T]) edgeContributionTombstoneLockedAt(key EdgeContributionKey[S], now time.Time) bool {
	tombstone, ok := c.edgeContributionTombstones[key]
	return ok && !tombstone.expiration.IsZero() && now.Before(tombstone.expiration)
}

func (c *GraphCache[S, T]) edgeContributionDeleteAllowedLockedAt(key EdgeContributionKey[S], ts hlc.Timestamp, now time.Time) bool {
	if c.edgeContributionTombstoneLockedAt(key, now) {
		return !ts.Less(c.edgeContributionTombstones[key].ts)
	}
	return true
}

func edgeContributionEstimatedBytes[S comparable](key EdgeContributionKey[S]) uint64 {
	return causalEdgeContributionBaseBytes + causalKeyPayloadBytes(key.Tail) + causalKeyPayloadBytes(key.Head)
}

func edgeContributionDeadlineEstimatedBytes[S comparable](key EdgeContributionKey[S]) uint64 {
	return causalEdgeContributionDeadlineBaseBytes + causalKeyPayloadBytes(key.Tail) + causalKeyPayloadBytes(key.Head)
}

func (c *GraphCache[S, T]) refreshOldestEdgeContributionDeadlineLocked() {
	c.oldestEdgeContributionDeadline = time.Time{}
	if c.edgeContributionDeadlines.Len() > 0 {
		c.oldestEdgeContributionDeadline = c.edgeContributionDeadlines.entries[0].deadline
	}
}

func (c *GraphCache[S, T]) setEdgeContributionTombstoneLocked(key EdgeContributionKey[S], ts hlc.Timestamp, expiration, now time.Time) {
	if ts == (hlc.Timestamp{}) || !now.Before(expiration) {
		return
	}
	if c.edgeContributionTombstones == nil {
		c.edgeContributionTombstones = make(map[EdgeContributionKey[S]]tombstoneEntry)
	}
	if existing, ok := c.edgeContributionTombstones[key]; ok {
		if ts.Less(existing.ts) {
			return
		}
		expiration = clampTombstoneExpirationOnReplay(existing, ts, expiration)
	} else {
		c.edgeContributionTombstoneBytes += edgeContributionEstimatedBytes(key)
	}
	c.edgeContributionTombstones[key] = tombstoneEntry{ts: ts, expiration: expiration}
	c.contributionTombstonesPresent.Store(true)
	if c.edgeContributionDeadlines.upsert(key, expiration) {
		c.edgeContributionDeadlineBytes += edgeContributionDeadlineEstimatedBytes(key)
	}
	c.refreshOldestEdgeContributionDeadlineLocked()
	c.updateEdgeCausalHighWaterLocked()
	c.updateEdgeCausalBytesHighWaterLocked()
}

func (c *GraphCache[S, T]) clearEdgeContributionTombstoneLocked(key EdgeContributionKey[S]) {
	if _, ok := c.edgeContributionTombstones[key]; !ok {
		return
	}
	delete(c.edgeContributionTombstones, key)
	c.edgeContributionTombstoneBytes -= edgeContributionEstimatedBytes(key)
	if c.edgeContributionDeadlines.remove(key) {
		c.edgeContributionDeadlineBytes -= edgeContributionDeadlineEstimatedBytes(key)
	}
	if len(c.edgeContributionTombstones) == 0 {
		c.edgeContributionTombstones = nil
		c.contributionTombstonesPresent.Store(false)
	}
	c.refreshOldestEdgeContributionDeadlineLocked()
}

// removeEdgeContributionLocked never subtracts from the aggregate. It
// recalculates source rows and retains an LWW Put floor if the last Add row
// disappeared with its bucket.
func (c *GraphCache[S, T]) removeEdgeContributionLocked(key EdgeContributionKey[S], now time.Time) bool {
	tailID, headID, found := c.edges.lookupIDs(key.Tail, key.Head)
	if !found {
		return false
	}
	c.edges.mu.Lock()
	bucket := c.edges.tf[tailID][headID]
	if bucket == nil {
		c.edges.mu.Unlock()
		return false
	}
	existed, empty := bucket.removeContributionAt(key.ContribID, now)
	floor := hlc.Timestamp{}
	if empty {
		floor = bucket.lastPutTimestamp()
		c.edges.deleteLocked(tailID, headID)
	}
	c.edges.mu.Unlock()
	if empty {
		if floor != (hlc.Timestamp{}) {
			c.recordEdgeCausalBarrierLocked(key.Tail, key.Head, floor)
		}
		c.onEdgeDeletedLocked(tailID, headID, key.Head)
		c.reconcileEdgeCausalUsageLocked(EdgeKey[S]{Tail: key.Tail, Head: key.Head})
	}
	return existed
}

// DeleteEdgeContributionsHLCDecisionsChecked atomically deletes individual Add
// identities with index-aligned observations. Even a missing accepted identity
// acquires a D4 tombstone to fence a delayed Add. Duplicate items observe
// their own turn in the batch. Capacity errors leave all state unchanged.
func (c *GraphCache[S, T]) DeleteEdgeContributionsHLCDecisionsChecked(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time,
) (existed []bool, acceptedIndexes []int, err error) {
	return c.deleteEdgeContributionsHLC(keys, ts, expiration, true)
}

// DeleteEdgeContributionsHLCDecisions is the replication-safe counterpart:
// already-committed effects bypass this receiver's local admission budget.
func (c *GraphCache[S, T]) DeleteEdgeContributionsHLCDecisions(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time,
) (existed []bool, acceptedIndexes []int, err error) {
	return c.deleteEdgeContributionsHLC(keys, ts, expiration, false)
}

func (c *GraphCache[S, T]) deleteEdgeContributionsHLC(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time, strict bool,
) (existed []bool, acceptedIndexes []int, err error) {
	if err := validateEdgeContributionKeys(keys, ts, expiration); err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.applicationTime()
	accepted := make([]EdgeContributionKey[S], 0, len(keys))
	acceptedIndexes = make([]int, 0, len(keys))
	for i, key := range keys {
		if c.edgeContributionDeleteAllowedLockedAt(key, ts, now) {
			accepted = append(accepted, key)
			acceptedIndexes = append(acceptedIndexes, i)
		}
	}
	if strict && ts != (hlc.Timestamp{}) && now.Before(expiration) {
		if err := c.checkEdgeContributionCausalCapacityLocked(accepted); err != nil {
			return nil, nil, err
		}
	}
	existed = make([]bool, len(keys))
	for i, key := range accepted {
		index := acceptedIndexes[i]
		existed[index] = c.removeEdgeContributionLocked(key, now)
		c.setEdgeContributionTombstoneLocked(key, ts, expiration, now)
	}
	return existed, acceptedIndexes, nil
}

// ApplySnapshotEdgeContributionTombstoneHLC installs an unexpired D4 floor
// before replaying live edge contributions. Remote snapshot apply must not be
// refused solely because this replica's local causal budget is exhausted.
func (c *GraphCache[S, T]) ApplySnapshotEdgeContributionTombstoneHLC(
	key EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time,
) error {
	if !time.Now().Before(expiration) {
		return nil
	}
	_, _, err := c.DeleteEdgeContributionsHLCDecisions([]EdgeContributionKey[S]{key}, ts, expiration)
	return err
}

type stagedEdgeContributionDelete[S comparable, T any] struct {
	base           *stagedEdgeDelete[S, T]
	accepted       []IndexedEdgeContributionDelete[S]
	now            time.Time
	original       map[EdgeContributionKey[S]]stagedValue[tombstoneEntry]
	originalMap    map[EdgeContributionKey[S]]tombstoneEntry
	deadlines      stagedIndexedDeadlineUndo[EdgeContributionKey[S]]
	tombstoneBytes uint64
	deadlineBytes  uint64
	oldest         time.Time
}

// EdgeContributionDeleteTransaction holds both visibility gates until its
// enclosing durable WAL publication commits or aborts.
type EdgeContributionDeleteTransaction[S comparable, T any] struct {
	stage  *stagedEdgeContributionDelete[S, T]
	closed bool
}

func (c *GraphCache[S, T]) PrepareEdgeContributionDelete(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time,
) (*EdgeContributionDeleteTransaction[S, T], error) {
	return c.prepareEdgeContributionDelete(keys, ts, expiration, true)
}

func (c *GraphCache[S, T]) PrepareReplicatedEdgeContributionDelete(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time,
) (*EdgeContributionDeleteTransaction[S, T], error) {
	return c.prepareEdgeContributionDelete(keys, ts, expiration, false)
}

func (c *GraphCache[S, T]) BeginEdgeContributionDelete(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time,
) (*EdgeContributionDeleteTransaction[S, T], error) {
	tx, err := c.PrepareEdgeContributionDelete(keys, ts, expiration)
	if err != nil {
		return nil, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			tx.Abort()
			panic(recovered)
		}
	}()
	tx.Apply()
	return tx, nil
}

func (c *GraphCache[S, T]) BeginReplicatedEdgeContributionDelete(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time,
) (*EdgeContributionDeleteTransaction[S, T], error) {
	tx, err := c.PrepareReplicatedEdgeContributionDelete(keys, ts, expiration)
	if err != nil {
		return nil, err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			tx.Abort()
			panic(recovered)
		}
	}()
	tx.Apply()
	return tx, nil
}

func (c *GraphCache[S, T]) prepareEdgeContributionDelete(
	keys []EdgeContributionKey[S], ts hlc.Timestamp, expiration time.Time, strict bool,
) (*EdgeContributionDeleteTransaction[S, T], error) {
	if c.publicationGate == nil || c.dict == nil {
		return nil, errors.New("graphcache: staged contribution Delete requires a publication gate and dictionary")
	}
	if err := validateEdgeContributionKeys(keys, ts, expiration); err != nil {
		return nil, err
	}
	// Extractors can call a point reader, so never run them under either
	// visibility gate.
	c.mu.RLock()
	extract := c.prefixExtract
	c.mu.RUnlock()
	projected := make(map[EdgeKey[S]]string)
	if extract != nil {
		for _, key := range keys {
			projected[EdgeKey[S]{Tail: key.Tail, Head: key.Head}] = extract(key.Head)
		}
	}

	c.mu.Lock()
	c.publicationGate.Lock()
	owned := true
	defer func() {
		if owned {
			c.publicationGate.Unlock()
			c.mu.Unlock()
		}
	}()
	now := c.applicationTime()
	base := &stagedEdgeDelete[S, T]{
		cache: c, outcomes: make([]bool, len(keys)), ts: ts, expireAt: expiration,
	}
	stage := &stagedEdgeContributionDelete[S, T]{
		base: base, now: now,
		accepted: make([]IndexedEdgeContributionDelete[S], 0, len(keys)),
	}
	plans := make(map[EdgeKey[S]]*stagedEdgeDeletePlan[S])
	accepted := make([]EdgeContributionKey[S], 0, len(keys))
	for index, key := range keys {
		if !c.edgeContributionDeleteAllowedLockedAt(key, ts, now) {
			continue
		}
		accepted = append(accepted, key)
		stage.accepted = append(stage.accepted, IndexedEdgeContributionDelete[S]{Index: index, Key: key})
		pair := EdgeKey[S]{Tail: key.Tail, Head: key.Head}
		plan := plans[pair]
		if plan == nil {
			before := c.edges.bucket(key.Tail, key.Head)
			plan = &stagedEdgeDeletePlan[S]{
				key: pair, before: before, after: cloneWeightForStaging(before),
				headProjected: projected[pair],
			}
			if before != nil {
				var found bool
				plan.tailID, plan.headID, found = c.edges.lookupIDs(key.Tail, key.Head)
				if !found {
					return nil, errors.New("graphcache: staged contribution Delete dictionary drift")
				}
				if c.headByTail != nil {
					if extract == nil || c.headByTail[plan.tailID] == nil {
						return nil, errors.New("graphcache: staged contribution Delete head index missing")
					}
					if _, ok := c.headByTail[plan.tailID].byProj[plan.headProjected][plan.headID]; !ok {
						return nil, errors.New("graphcache: staged contribution Delete head projection drift")
					}
				}
			}
			plans[pair] = plan
			base.plans = append(base.plans, plan)
		}
		if plan.after != nil {
			removed, empty := plan.after.removeContributionAt(key.ContribID, now)
			base.outcomes[index] = removed
			if empty {
				plan.after = nil
			}
		}
	}
	if strict && ts != (hlc.Timestamp{}) && now.Before(expiration) {
		if err := c.checkEdgeContributionCausalCapacityLocked(accepted); err != nil {
			return nil, err
		}
	}
	base.captureUndoLocked()
	stage.originalMap = c.edgeContributionTombstones
	stage.original = make(map[EdgeContributionKey[S]]stagedValue[tombstoneEntry], len(accepted))
	for _, key := range accepted {
		value, set := c.edgeContributionTombstones[key]
		stage.original[key] = stagedValue[tombstoneEntry]{value: value, set: set}
	}
	stage.deadlines.capture(&c.edgeContributionDeadlines)
	stage.tombstoneBytes = c.edgeContributionTombstoneBytes
	stage.deadlineBytes = c.edgeContributionDeadlineBytes
	stage.oldest = c.oldestEdgeContributionDeadline
	needed := make(map[vertexID]uint32)
	for _, plan := range base.plans {
		if plan.before != nil && plan.after == nil {
			needed[plan.tailID]++
			needed[plan.headID]++
		}
	}
	for id, count := range needed {
		current := base.undo.dictRefs[id].count
		if current == freedRefcount || current < count {
			return nil, errors.New("graphcache: staged contribution Delete dictionary refcount drift")
		}
	}
	owned = false
	return &EdgeContributionDeleteTransaction[S, T]{stage: stage}, nil
}

func (tx *EdgeContributionDeleteTransaction[S, T]) Result() EdgeContributionDeleteStageResult[S] {
	return EdgeContributionDeleteStageResult[S]{
		Existed:  append([]bool{}, tx.stage.base.outcomes...),
		Accepted: append([]IndexedEdgeContributionDelete[S]{}, tx.stage.accepted...),
	}
}

func (s *stagedEdgeContributionDelete[S, T]) applyLocked() {
	base := s.base
	if base.applied {
		panic("graphcache: staged contribution Delete applied twice")
	}
	base.applied = true
	complete := false
	defer func() {
		if !complete {
			s.rollbackLocked()
		}
	}()
	c := base.cache
	for _, entry := range s.accepted {
		key := entry.Key
		if base.ts == (hlc.Timestamp{}) || !s.now.Before(base.expireAt) {
			continue
		}
		if c.edgeContributionTombstones == nil {
			c.edgeContributionTombstones = make(map[EdgeContributionKey[S]]tombstoneEntry)
		}
		deadline := base.expireAt
		if old, exists := c.edgeContributionTombstones[key]; exists {
			if base.ts.Less(old.ts) {
				continue
			}
			deadline = clampTombstoneExpirationOnReplay(old, base.ts, deadline)
		} else {
			c.edgeContributionTombstoneBytes += edgeContributionEstimatedBytes(key)
		}
		c.edgeContributionTombstones[key] = tombstoneEntry{ts: base.ts, expiration: deadline}
		c.contributionTombstonesPresent.Store(true)
		if s.deadlines.upsert(&c.edgeContributionDeadlines, key, deadline) {
			c.edgeContributionDeadlineBytes += edgeContributionDeadlineEstimatedBytes(key)
		}
		c.refreshOldestEdgeContributionDeadlineLocked()
		c.updateEdgeCausalHighWaterLocked()
		c.updateEdgeCausalBytesHighWaterLocked()
	}
	for _, plan := range base.plans {
		if plan.before == nil {
			continue
		}
		if plan.after == nil {
			if floor := plan.before.lastPutTimestamp(); floor != (hlc.Timestamp{}) {
				c.recordEdgeCausalBarrierLocked(plan.key.Tail, plan.key.Head, floor)
			}
			c.edges.mu.Lock()
			deleted := c.edges.deleteLocked(plan.tailID, plan.headID)
			c.edges.mu.Unlock()
			if !deleted {
				panic("graphcache: staged contribution Delete bucket drift")
			}
			if c.headByTail != nil {
				index := c.headByTail[plan.tailID]
				plan.removedIndex = true
				if index.delete(plan.headID, plan.headProjected) {
					delete(c.headByTail, plan.tailID)
				}
			}
		} else {
			c.edges.mu.Lock()
			c.edges.tf[plan.tailID][plan.headID] = plan.after
			c.edges.mu.Unlock()
		}
		c.reconcileEdgeCausalUsageLocked(plan.key)
	}
	complete = true
}

func (s *stagedEdgeContributionDelete[S, T]) rollbackLocked() {
	if !s.base.applied {
		return
	}
	c := s.base.cache
	for key, old := range s.original {
		if old.set {
			if c.edgeContributionTombstones == nil {
				c.edgeContributionTombstones = s.originalMap
			}
			c.edgeContributionTombstones[key] = old.value
		} else {
			delete(c.edgeContributionTombstones, key)
		}
	}
	if s.originalMap == nil {
		c.edgeContributionTombstones = nil
	}
	s.deadlines.restore(&c.edgeContributionDeadlines)
	c.edgeContributionTombstoneBytes = s.tombstoneBytes
	c.edgeContributionDeadlineBytes = s.deadlineBytes
	c.oldestEdgeContributionDeadline = s.oldest
	c.contributionTombstonesPresent.Store(len(c.edgeContributionTombstones) > 0)
	s.base.rollbackLocked()
}

func (tx *EdgeContributionDeleteTransaction[S, T]) Apply() {
	if tx == nil || tx.closed || tx.stage.base.applied {
		panic("graphcache: staged contribution Delete applied in invalid state")
	}
	tx.stage.applyLocked()
}

func (tx *EdgeContributionDeleteTransaction[S, T]) Commit() {
	if tx == nil || tx.closed || !tx.stage.base.applied {
		panic("graphcache: staged contribution Delete committed after close")
	}
	tx.closed = true
	tx.stage.base.cache.publicationGate.Unlock()
	tx.stage.base.cache.mu.Unlock()
}

func (tx *EdgeContributionDeleteTransaction[S, T]) Abort() {
	if tx == nil || tx.closed {
		return
	}
	tx.closed = true
	c := tx.stage.base.cache
	defer c.mu.Unlock()
	defer c.publicationGate.Unlock()
	tx.stage.rollbackLocked()
}
