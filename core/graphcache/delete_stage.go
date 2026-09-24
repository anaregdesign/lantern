package graphcache

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// stagedEdgeDelete is an internal, deliberately incomplete part of a future
// WAL-first Delete envelope. Its caller must hold c.mu and publicationGate for
// the entire prepare/apply/rollback interval. The deadline index is handled
// by a separate change: applyLocked does not update it, and must never be
// published until that index is staged under the same gate.
type stagedEdgeDelete[S comparable, T any] struct {
	cache    *GraphCache[S, T]
	plans    []*stagedEdgeDeletePlan[S]
	outcomes []bool
	undo     stagedEdgeDeleteUndo[S]
	ts       hlc.Timestamp
	expireAt time.Time
	applied  bool
}

type stagedEdgeDeletePlan[S comparable] struct {
	key           EdgeKey[S]
	before        *weight
	after         *weight
	tailID        vertexID
	headID        vertexID
	headProjected string
	removedIndex  bool
}

type stagedValue[V any] struct {
	value V
	set   bool
}

type stagedDictRef[S comparable] struct {
	key   S
	count uint32
}

type stagedEdgeDeleteUndo[S comparable] struct {
	oldEdgeCount int
	tailMaps     map[vertexID]map[vertexID]*weight
	df           map[vertexID]stagedValue[int]
	headIndexes  map[vertexID]*headIndex
	dictRefs     map[vertexID]stagedDictRef[S]
	dictFree     []vertexID

	tombstoneMap map[EdgeKey[S]]tombstoneEntry
	tombstones   map[EdgeKey[S]]stagedValue[tombstoneEntry]
	barrierMap   map[EdgeKey[S]]hlc.Timestamp
	barriers     map[EdgeKey[S]]stagedValue[hlc.Timestamp]
	usageMap     map[EdgeKey[S]]uint64
	usage        map[EdgeKey[S]]stagedValue[uint64]
	usageBytes   uint64
	usagePeak    int
	highWater    int
	bytesHigh    uint64
}

// prepareStagedEdgeDeleteLocked computes exact original outcomes and captures
// only touched entries. The caller must calculate head projections before
// taking publicationGate: a user extractor may call a point reader. Both
// locks must be held here so an Add fast path cannot alter a weight or a
// dictionary refcount between preparation and application.
func (c *GraphCache[S, T]) prepareStagedEdgeDeleteLocked(
	keys []EdgeKey[S], ts hlc.Timestamp, expiration, now time.Time, projected map[EdgeKey[S]]string,
) (*stagedEdgeDelete[S, T], error) {
	if c.publicationGate == nil {
		return nil, errors.New("graphcache: staged Delete requires a publication gate")
	}
	if c.dict == nil {
		return nil, errors.New("graphcache: staged Delete requires a dictionary")
	}
	stage := &stagedEdgeDelete[S, T]{cache: c, outcomes: make([]bool, len(keys)), ts: ts, expireAt: expiration}
	byKey := make(map[EdgeKey[S]]*stagedEdgeDeletePlan[S], len(keys))
	for i, key := range keys {
		if !c.edgeDeleteWriteAllowedLocked(key.Tail, key.Head, ts) {
			continue
		}
		plan := byKey[key]
		if plan == nil {
			before := c.edges.bucket(key.Tail, key.Head)
			projection, hasProjection := projected[key]
			plan = &stagedEdgeDeletePlan[S]{
				key: key, before: before, after: cloneWeightForStagedDelete(before),
				headProjected: projection,
			}
			if before != nil {
				var ok bool
				plan.tailID, plan.headID, ok = c.edges.lookupIDs(key.Tail, key.Head)
				if !ok {
					return nil, errors.New("graphcache: staged edge dictionary drift")
				}
				if c.headByTail != nil {
					if !hasProjection {
						return nil, errors.New("graphcache: staged edge projection missing")
					}
					index := c.headByTail[plan.tailID]
					if index == nil {
						return nil, errors.New("graphcache: staged edge head index missing")
					}
					if _, ok := index.byProj[plan.headProjected][plan.headID]; !ok {
						return nil, errors.New("graphcache: staged edge head projection drift")
					}
				}
			}
			byKey[key] = plan
			stage.plans = append(stage.plans, plan)
		}
		if plan.after == nil {
			continue
		}
		stage.outcomes[i] = true
		plan.after.retainAddsNewerThanLocked(ts)
		plan.after.lastHLC = hlc.Timestamp{}
		plan.after.flushLockedAt(now)
		if len(plan.after.values) == 0 {
			plan.after = nil
		}
	}
	stage.captureUndoLocked()
	needed := make(map[vertexID]uint32)
	for _, plan := range stage.plans {
		if plan.before != nil && plan.after == nil {
			needed[plan.tailID]++
			needed[plan.headID]++
		}
	}
	for id, count := range needed {
		current := stage.undo.dictRefs[id].count
		if current == freedRefcount || current < count {
			return nil, errors.New("graphcache: staged edge dictionary refcount drift")
		}
	}
	return stage, nil
}

func cloneWeightForStagedDelete(before *weight) *weight {
	if before == nil {
		return nil
	}
	before.mu.Lock()
	defer before.mu.Unlock()
	return &weight{
		values:       append([]weightValue(nil), before.values...),
		sum:          before.sum,
		lastFlushLen: before.lastFlushLen,
		minExp:       before.minExp,
		needsSort:    before.needsSort,
		lastHLC:      before.lastHLC,
	}
}

func (s *stagedEdgeDelete[S, T]) captureUndoLocked() {
	c := s.cache
	u := &s.undo
	u.oldEdgeCount = c.edges.edgeCount
	u.tailMaps = make(map[vertexID]map[vertexID]*weight)
	u.df = make(map[vertexID]stagedValue[int])
	u.headIndexes = make(map[vertexID]*headIndex)
	u.dictRefs = make(map[vertexID]stagedDictRef[S])
	u.tombstoneMap = c.edgeTombstones
	u.tombstones = make(map[EdgeKey[S]]stagedValue[tombstoneEntry], len(s.plans))
	u.barrierMap = c.edgeCausalBarriers
	u.barriers = make(map[EdgeKey[S]]stagedValue[hlc.Timestamp], len(s.plans))
	u.usageMap = c.edgeCausalUsage
	u.usage = make(map[EdgeKey[S]]stagedValue[uint64], len(s.plans))
	u.usageBytes = c.edgeCausalUsageBytes
	u.usagePeak = c.edgeCausalUsagePeak
	u.highWater = c.edgeCausalHighWater
	u.bytesHigh = c.edgeCausalBytesHighWater

	d := c.dict
	if d != nil {
		d.mu.RLock()
		u.dictFree = d.free
	}
	for _, plan := range s.plans {
		key := plan.key
		v, ok := c.edgeTombstones[key]
		u.tombstones[key] = stagedValue[tombstoneEntry]{v, ok}
		b, ok := c.edgeCausalBarriers[key]
		u.barriers[key] = stagedValue[hlc.Timestamp]{b, ok}
		usage, ok := c.edgeCausalUsage[key]
		u.usage[key] = stagedValue[uint64]{usage, ok}
		if plan.before == nil {
			continue
		}
		u.tailMaps[plan.tailID] = c.edges.tf[plan.tailID]
		if _, seen := u.df[plan.headID]; !seen {
			count, present := c.edges.df[plan.headID]
			u.df[plan.headID] = stagedValue[int]{count, present}
		}
		if c.headByTail != nil {
			u.headIndexes[plan.tailID] = c.headByTail[plan.tailID]
		}
		if d != nil {
			for _, id := range [...]vertexID{plan.tailID, plan.headID} {
				if _, seen := u.dictRefs[id]; !seen {
					u.dictRefs[id] = stagedDictRef[S]{
						key: d.reverse[id], count: atomic.LoadUint32(&d.refcount[id]),
					}
				}
			}
		}
	}
	if d != nil {
		d.mu.RUnlock()
	}
}

// applyLocked changes graph, head index, dictionary, tombstone record, and
// causal usage under the two exclusive gates. It intentionally leaves the
// deadline index untouched; only rollbackLocked is currently supported.
func (s *stagedEdgeDelete[S, T]) applyLocked() {
	if s.applied {
		panic("graphcache: staged Delete applied twice")
	}
	s.applied = true
	complete := false
	defer func() {
		if !complete {
			s.rollbackLocked()
		}
	}()
	c := s.cache
	ts, expiration := s.ts, s.expireAt
	for _, plan := range s.plans {
		key := plan.key
		if ts != (hlc.Timestamp{}) {
			if c.edgeTombstones == nil {
				c.edgeTombstones = make(map[EdgeKey[S]]tombstoneEntry)
			}
			if old, ok := c.edgeTombstones[key]; ok {
				if ts.Less(old.ts) {
					// edgeDeleteWriteAllowedLocked ignores an expired floor,
					// while setEdgeTombstoneLocked preserves its larger HLC.
					c.edgeTombstones[key] = old
				} else if ts == old.ts && !old.expiration.IsZero() && old.expiration.Before(expiration) {
					c.edgeTombstones[key] = old
				} else {
					c.edgeTombstones[key] = tombstoneEntry{ts: ts, expiration: expiration}
				}
			} else {
				c.edgeTombstones[key] = tombstoneEntry{ts: ts, expiration: expiration}
			}
		}
		if plan.before != nil {
			if plan.after == nil {
				deleted := func() bool {
					c.edges.mu.Lock()
					defer c.edges.mu.Unlock()
					return c.edges.deleteLocked(plan.tailID, plan.headID)
				}()
				if !deleted {
					panic("graphcache: staged edge bucket drift")
				}
				if c.headByTail != nil {
					index := c.headByTail[plan.tailID]
					plan.removedIndex = true
					if index.delete(plan.headID, plan.headProjected) {
						delete(c.headByTail, plan.tailID)
					}
				}
			} else {
				func() {
					c.edges.mu.Lock()
					defer c.edges.mu.Unlock()
					c.edges.tf[plan.tailID][plan.headID] = plan.after
				}()
			}
		}
		if barrier, ok := c.edgeCausalBarriers[key]; ok && !ts.Less(barrier) {
			delete(c.edgeCausalBarriers, key)
			if len(c.edgeCausalBarriers) == 0 {
				c.edgeCausalBarriers = nil
			}
		}
		if c.edgeHasCausalStateLocked(key) {
			c.ensureEdgeCausalUsageLocked(key)
		} else if bytes, ok := c.edgeCausalUsage[key]; ok {
			delete(c.edgeCausalUsage, key)
			c.edgeCausalUsageBytes -= bytes
			// Shrinking would rebuild the whole ledger and defeat sparse undo.
		}
	}
	complete = true
}

// rollbackLocked restores the exact logical state captured before applyLocked.
// Only touched graph/index/dictionary/causal entries are visited. It is safe
// to call after a partially completed apply (including a panic) while both
// exclusive gates are still held.
func (s *stagedEdgeDelete[S, T]) rollbackLocked() {
	if !s.applied {
		return
	}
	c := s.cache
	u := &s.undo
	for key, old := range u.tombstones {
		if old.set {
			if c.edgeTombstones == nil {
				c.edgeTombstones = u.tombstoneMap
			}
			c.edgeTombstones[key] = old.value
		} else {
			delete(c.edgeTombstones, key)
		}
	}
	if u.tombstoneMap == nil {
		c.edgeTombstones = nil
	}
	for key, old := range u.barriers {
		if old.set {
			if c.edgeCausalBarriers == nil {
				c.edgeCausalBarriers = u.barrierMap
			}
			c.edgeCausalBarriers[key] = old.value
		} else {
			delete(c.edgeCausalBarriers, key)
		}
	}
	if u.barrierMap == nil {
		c.edgeCausalBarriers = nil
	}
	for key, old := range u.usage {
		if old.set {
			if c.edgeCausalUsage == nil {
				c.edgeCausalUsage = u.usageMap
			}
			c.edgeCausalUsage[key] = old.value
		} else {
			delete(c.edgeCausalUsage, key)
		}
	}
	if u.usageMap == nil {
		c.edgeCausalUsage = nil
	}
	c.edgeCausalUsageBytes = u.usageBytes
	c.edgeCausalUsagePeak = u.usagePeak
	c.edgeCausalHighWater = u.highWater
	c.edgeCausalBytesHighWater = u.bytesHigh

	if c.dict != nil {
		d := c.dict
		d.mu.Lock()
		d.free = u.dictFree
		for id, old := range u.dictRefs {
			d.forward[old.key] = id
			d.reverse[id] = old.key
			atomic.StoreUint32(&d.refcount[id], old.count)
		}
		d.mu.Unlock()
	}
	c.edges.mu.Lock()
	for tailID, heads := range u.tailMaps {
		c.edges.tf[tailID] = heads
	}
	for _, plan := range s.plans {
		if plan.before != nil {
			c.edges.tf[plan.tailID][plan.headID] = plan.before
		}
	}
	for headID, old := range u.df {
		if old.set {
			c.edges.df[headID] = old.value
		} else {
			delete(c.edges.df, headID)
		}
	}
	c.edges.edgeCount = u.oldEdgeCount
	c.edges.mu.Unlock()
	if c.headByTail != nil {
		for tailID, index := range u.headIndexes {
			c.headByTail[tailID] = index
		}
		for _, plan := range s.plans {
			if plan.removedIndex {
				c.headByTail[plan.tailID].insert(plan.headID, plan.headProjected)
				plan.removedIndex = false
			}
		}
	}
	s.applied = false
}
