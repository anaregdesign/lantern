package graphcache

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// stagedEdgeDelete is an internal part of a future WAL-first Delete envelope.
// Its caller must hold c.mu and publicationGate for the entire
// prepare/apply/rollback interval. No publication or WAL callback is exposed
// until the enclosing receipt/log cut is implemented.
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

	tombstoneMap   map[EdgeKey[S]]tombstoneEntry
	tombstones     map[EdgeKey[S]]stagedValue[tombstoneEntry]
	barrierMap     map[EdgeKey[S]]hlc.Timestamp
	barriers       map[EdgeKey[S]]stagedValue[hlc.Timestamp]
	usageMap       map[EdgeKey[S]]uint64
	usage          map[EdgeKey[S]]stagedValue[uint64]
	usageBytes     uint64
	usagePeak      int
	highWater      int
	bytesHigh      uint64
	deadlines      stagedIndexedDeadlineUndo[EdgeKey[S]]
	deadlineBytes  uint64
	oldestDeadline time.Time
}

// stagedIndexedDeadlineUndo records only heap paths touched by this batch.
// The indexed heap has one entry per key, so an upsert/remove performs at
// most O(log retained tombstones) swaps without rebuilding unrelated entries.
type stagedIndexedDeadlineUndo[K comparable] struct {
	entries   []causalDeadlineEntry[K]
	positions map[K]int
	peak      int
	cells     map[int]causalDeadlineEntry[K]
	keys      map[K]stagedValue[int]
}

func (u *stagedIndexedDeadlineUndo[K]) capture(h *indexedCausalDeadlineHeap[K]) {
	u.entries = h.entries
	u.positions = h.positions
	u.peak = h.peak
	u.cells = make(map[int]causalDeadlineEntry[K])
	u.keys = make(map[K]stagedValue[int])
}

func (u *stagedIndexedDeadlineUndo[K]) touchCell(index int) {
	if index >= len(u.entries) {
		return
	}
	if _, seen := u.cells[index]; !seen {
		u.cells[index] = u.entries[index]
	}
}

func (u *stagedIndexedDeadlineUndo[K]) touchKey(key K) {
	if _, seen := u.keys[key]; !seen {
		pos, ok := u.positions[key]
		u.keys[key] = stagedValue[int]{pos, ok}
	}
}

func (u *stagedIndexedDeadlineUndo[K]) swap(h *indexedCausalDeadlineHeap[K], i, j int) {
	u.touchCell(i)
	u.touchCell(j)
	u.touchKey(h.entries[i].key)
	u.touchKey(h.entries[j].key)
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.positions[h.entries[i].key] = i
	h.positions[h.entries[j].key] = j
}

func (u *stagedIndexedDeadlineUndo[K]) up(h *indexedCausalDeadlineHeap[K], index int) {
	for index > 0 {
		parent := (index - 1) / 2
		if !h.Less(index, parent) {
			return
		}
		u.swap(h, parent, index)
		index = parent
	}
}

func (u *stagedIndexedDeadlineUndo[K]) down(h *indexedCausalDeadlineHeap[K], index int) bool {
	start := index
	for {
		left := 2*index + 1
		if left >= h.Len() {
			break
		}
		child := left
		if right := left + 1; right < h.Len() && h.Less(right, left) {
			child = right
		}
		if !h.Less(child, index) {
			break
		}
		u.swap(h, index, child)
		index = child
	}
	return index != start
}

func (u *stagedIndexedDeadlineUndo[K]) fix(h *indexedCausalDeadlineHeap[K], index int) {
	if !u.down(h, index) {
		u.up(h, index)
	}
}

func (u *stagedIndexedDeadlineUndo[K]) upsert(h *indexedCausalDeadlineHeap[K], key K, deadline time.Time) bool {
	if index, ok := h.positions[key]; ok {
		if !h.entries[index].deadline.Equal(deadline) {
			u.touchCell(index)
			h.entries[index].deadline = deadline
			u.fix(h, index)
		}
		return false
	}
	if h.positions == nil {
		h.positions = make(map[K]int)
	}
	u.touchKey(key)
	h.positions[key] = len(h.entries)
	h.entries = append(h.entries, causalDeadlineEntry[K]{key: key, deadline: deadline})
	if len(h.entries) > h.peak {
		h.peak = len(h.entries)
	}
	u.up(h, len(h.entries)-1)
	return true
}

func (u *stagedIndexedDeadlineUndo[K]) remove(h *indexedCausalDeadlineHeap[K], key K) bool {
	index, ok := h.positions[key]
	if !ok {
		return false
	}
	last := len(h.entries) - 1
	if index != last {
		u.swap(h, index, last)
	}
	u.touchCell(last)
	u.touchKey(key)
	delete(h.positions, key)
	var zero causalDeadlineEntry[K]
	h.entries[last] = zero
	if last == 0 {
		h.entries = nil
		h.positions = nil
		h.peak = 0
	} else {
		h.entries = h.entries[:last]
		if index < last {
			u.fix(h, index)
		}
	}
	return true
}

func (u *stagedIndexedDeadlineUndo[K]) restore(h *indexedCausalDeadlineHeap[K]) {
	for index, old := range u.cells {
		u.entries[index] = old
	}
	h.entries = u.entries
	if u.positions != nil {
		for key, old := range u.keys {
			if old.set {
				u.positions[key] = old.value
			} else {
				delete(u.positions, key)
			}
		}
	}
	h.positions = u.positions
	h.peak = u.peak
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
		if !c.edgeDeleteWriteAllowedLockedAt(key.Tail, key.Head, ts, now) {
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
	u.deadlines.capture(&c.edgeTombstoneDeadlines)
	u.deadlineBytes = c.edgeTombstoneDeadlineBytes
	u.oldestDeadline = c.oldestEdgeTombstoneDeadline

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

// applyLocked changes graph, head index, dictionary, tombstone, deadline
// index, and causal usage under the two exclusive gates. Only rollbackLocked
// is currently supported; a future envelope will own the publication step.
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
			stamped := true
			deadline := expiration
			if old, ok := c.edgeTombstones[key]; ok {
				if ts.Less(old.ts) {
					// edgeDeleteWriteAllowedLocked ignores an expired floor,
					// while setEdgeTombstoneLocked preserves its larger HLC.
					stamped = false
				} else if ts == old.ts && !old.expiration.IsZero() && old.expiration.Before(expiration) {
					deadline = old.expiration
				}
			}
			if stamped {
				c.edgeTombstones[key] = tombstoneEntry{ts: ts, expiration: deadline}
				s.stageDeadlineLocked(key, deadline)
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

func (s *stagedEdgeDelete[S, T]) stageDeadlineLocked(key EdgeKey[S], deadline time.Time) {
	c := s.cache
	if deadline.IsZero() {
		if s.undo.deadlines.remove(&c.edgeTombstoneDeadlines, key) {
			c.edgeTombstoneDeadlineBytes -= causalEdgeDeadlineEntryBaseBytes + causalKeyPayloadBytes(key.Tail) + causalKeyPayloadBytes(key.Head)
		}
	} else if s.undo.deadlines.upsert(&c.edgeTombstoneDeadlines, key, deadline) {
		c.edgeTombstoneDeadlineBytes += causalEdgeDeadlineEntryBaseBytes + causalKeyPayloadBytes(key.Tail) + causalKeyPayloadBytes(key.Head)
		c.updateEdgeCausalBytesHighWaterLocked()
	}
	c.refreshOldestEdgeTombstoneDeadlineLocked()
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
	u.deadlines.restore(&c.edgeTombstoneDeadlines)
	c.edgeTombstoneDeadlineBytes = u.deadlineBytes
	c.oldestEdgeTombstoneDeadline = u.oldestDeadline

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
