package graphcache

import (
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

type stagedVertexMutation[S comparable, T any] struct {
	cache           *GraphCache[S, T]
	slots           map[S]stagedVertexSlot[T]
	keys            []S
	projections     map[S]string
	prefixBefore    map[string]bool
	dict            stagedVertexDictionaryUndo[S]
	causal          stagedVertexCausalUndo[S]
	searchIndex     *search.InvertedIndex[S, search.Document]
	searchBefore    []search.PreparedItem[S]
	searchAfter     []search.PreparedItem[S]
	applicationTime time.Time
	applied         bool
}

type stagedVertexDictionaryUndo[S comparable] struct {
	free      []vertexID
	freeCells map[int]vertexID
	reverse   []S
	refcount  []uint32
	keys      map[S]stagedValue[vertexID]
	cells     map[vertexID]stagedDictRef[S]
}

type stagedVertexCausalUndo[S comparable] struct {
	hlcMap       map[S]hlc.Timestamp
	hlcs         map[S]stagedValue[hlc.Timestamp]
	tombstoneMap map[S]tombstoneEntry
	tombstones   map[S]stagedValue[tombstoneEntry]
	barrierMap   map[S]hlc.Timestamp
	barriers     map[S]stagedValue[hlc.Timestamp]
	usageMap     map[S]uint64
	usage        map[S]stagedValue[uint64]

	hlcHighWater   int
	usageBytes     uint64
	usagePeak      int
	highWater      int
	bytesHighWater uint64
	rejected       uint64

	deadlines      stagedDeadlineHeapUndo[S]
	deadlineBytes  uint64
	oldestDeadline time.Time
}

type stagedDeadlineHeapUndo[K comparable] struct {
	entries causalDeadlineHeap[K]
	cells   map[int]causalDeadlineEntry[K]
}

func (u *stagedDeadlineHeapUndo[K]) capture(h *causalDeadlineHeap[K]) {
	u.entries = *h
	u.cells = make(map[int]causalDeadlineEntry[K])
}

func (u *stagedDeadlineHeapUndo[K]) touch(index int) {
	if index >= len(u.entries) {
		return
	}
	if _, ok := u.cells[index]; !ok {
		u.cells[index] = u.entries[index]
	}
}

func (u *stagedDeadlineHeapUndo[K]) swap(h *causalDeadlineHeap[K], i, j int) {
	u.touch(i)
	u.touch(j)
	(*h)[i], (*h)[j] = (*h)[j], (*h)[i]
}

func (u *stagedDeadlineHeapUndo[K]) push(h *causalDeadlineHeap[K], entry causalDeadlineEntry[K]) {
	*h = append(*h, entry)
	index := len(*h) - 1
	for index > 0 {
		parent := (index - 1) / 2
		if !(*h)[index].deadline.Before((*h)[parent].deadline) {
			break
		}
		u.swap(h, index, parent)
		index = parent
	}
}

func (u *stagedDeadlineHeapUndo[K]) pop(h *causalDeadlineHeap[K]) causalDeadlineEntry[K] {
	last := len(*h) - 1
	u.swap(h, 0, last)
	u.touch(last)
	entry := (*h)[last]
	var zero causalDeadlineEntry[K]
	(*h)[last] = zero
	*h = (*h)[:last]
	index := 0
	for {
		left := 2*index + 1
		if left >= len(*h) {
			break
		}
		child := left
		if right := left + 1; right < len(*h) &&
			(*h)[right].deadline.Before((*h)[left].deadline) {
			child = right
		}
		if !(*h)[child].deadline.Before((*h)[index].deadline) {
			break
		}
		u.swap(h, index, child)
		index = child
	}
	return entry
}

func (u *stagedDeadlineHeapUndo[K]) restore(h *causalDeadlineHeap[K]) {
	for index, entry := range u.cells {
		u.entries[index] = entry
	}
	*h = u.entries
}

func (u *stagedVertexDictionaryUndo[S]) capture(
	d *dictionary[S], keys []S, maximumAllocations int,
) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	u.free = d.free
	u.freeCells = make(map[int]vertexID, min(maximumAllocations, len(d.free)))
	u.reverse = d.reverse
	u.refcount = d.refcount
	u.keys = make(map[S]stagedValue[vertexID], len(keys))
	u.cells = make(map[vertexID]stagedDictRef[S], len(keys)+maximumAllocations)
	touchID := func(id vertexID) {
		if _, ok := u.cells[id]; ok {
			return
		}
		u.cells[id] = stagedDictRef[S]{
			key: d.reverse[id], count: atomic.LoadUint32(&d.refcount[id]),
		}
	}
	for _, key := range keys {
		id, ok := d.forward[key]
		u.keys[key] = stagedValue[vertexID]{value: id, set: ok}
		if ok {
			touchID(id)
		}
	}
	for i := 0; i < maximumAllocations && i < len(d.free); i++ {
		index := len(d.free) - 1 - i
		u.freeCells[index] = d.free[index]
		touchID(d.free[index])
	}
}

func (u *stagedVertexDictionaryUndo[S]) restore(d *dictionary[S]) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, old := range u.keys {
		if old.set {
			d.forward[key] = old.value
		} else {
			delete(d.forward, key)
		}
	}
	for id, old := range u.cells {
		u.reverse[id] = old.key
		atomic.StoreUint32(&u.refcount[id], old.count)
	}
	for index, id := range u.freeCells {
		u.free[index] = id
	}
	d.reverse = u.reverse
	d.refcount = u.refcount
	d.free = u.free
}

func captureStagedVertexCausalUndo[S comparable, T any](
	u *stagedVertexCausalUndo[S], c *GraphCache[S, T], keys []S,
) {
	u.hlcMap = c.vertexHLC
	u.hlcs = make(map[S]stagedValue[hlc.Timestamp], len(keys))
	u.tombstoneMap = c.vertexTombstones
	u.tombstones = make(map[S]stagedValue[tombstoneEntry], len(keys))
	u.barrierMap = c.vertexCausalBarriers
	u.barriers = make(map[S]stagedValue[hlc.Timestamp], len(keys))
	u.usageMap = c.vertexCausalUsage
	u.usage = make(map[S]stagedValue[uint64], len(keys))
	for _, key := range keys {
		value, ok := c.vertexHLC[key]
		u.hlcs[key] = stagedValue[hlc.Timestamp]{value: value, set: ok}
		tombstone, ok := c.vertexTombstones[key]
		u.tombstones[key] = stagedValue[tombstoneEntry]{value: tombstone, set: ok}
		barrier, ok := c.vertexCausalBarriers[key]
		u.barriers[key] = stagedValue[hlc.Timestamp]{value: barrier, set: ok}
		usage, ok := c.vertexCausalUsage[key]
		u.usage[key] = stagedValue[uint64]{value: usage, set: ok}
	}
	u.hlcHighWater = c.vertexHLCHighWater
	u.usageBytes = c.vertexCausalUsageBytes
	u.usagePeak = c.vertexCausalUsagePeak
	u.highWater = c.vertexCausalHighWater
	u.bytesHighWater = c.vertexCausalBytesHighWater
	u.rejected = c.vertexCausalRejected
	u.deadlines.capture(&c.vertexTombstoneDeadlines)
	u.deadlineBytes = c.vertexTombstoneDeadlineBytes
	u.oldestDeadline = c.oldestVertexTombstoneDeadline
}

func restoreStagedValues[K comparable, V any](
	target *map[K]V, original map[K]V, values map[K]stagedValue[V],
) {
	for key, old := range values {
		if old.set {
			if *target == nil {
				*target = original
			}
			(*target)[key] = old.value
		} else {
			delete(*target, key)
		}
	}
	if original == nil {
		*target = nil
	}
}

func restoreStagedVertexCausalUndo[S comparable, T any](
	u *stagedVertexCausalUndo[S], c *GraphCache[S, T],
) {
	restoreStagedValues(&c.vertexHLC, u.hlcMap, u.hlcs)
	restoreStagedValues(&c.vertexTombstones, u.tombstoneMap, u.tombstones)
	restoreStagedValues(&c.vertexCausalBarriers, u.barrierMap, u.barriers)
	restoreStagedValues(&c.vertexCausalUsage, u.usageMap, u.usage)
	c.vertexHLCHighWater = u.hlcHighWater
	c.vertexCausalUsageBytes = u.usageBytes
	c.vertexCausalUsagePeak = u.usagePeak
	c.vertexCausalHighWater = u.highWater
	c.vertexCausalBytesHighWater = u.bytesHighWater
	c.vertexCausalRejected = u.rejected
	u.deadlines.restore(&c.vertexTombstoneDeadlines)
	c.vertexTombstoneDeadlineBytes = u.deadlineBytes
	c.oldestVertexTombstoneDeadline = u.oldestDeadline
}

func (s *stagedVertexMutation[S, T]) applyPutLocked(
	items []VertexItem[S, T], applied []int, outcomes []PutOutcome, ts hlc.Timestamp,
) {
	s.applyLocked(func() {
		for _, index := range applied {
			item := items[index]
			if outcomes[index] == PutOutcomeAppliedAndLive {
				s.upsertVertexLocked(item.Key, item.Value, item.Expiration)
				s.recordVertexHLCLocked(item.Key, ts)
				s.clearVertexCausalBarrierLocked(item.Key)
			} else {
				s.deleteVertexLocked(item.Key)
				s.recordVertexCausalBarrierLocked(item.Key, ts)
				s.clearVertexHLCLocked(item.Key)
			}
			s.clearVertexTombstoneLocked(item.Key)
		}
	})
}

func (s *stagedVertexMutation[S, T]) applyDeleteLocked(
	keys []S, accepted []int, ts hlc.Timestamp, expiration time.Time,
) {
	s.applyLocked(func() {
		for _, index := range accepted {
			key := keys[index]
			s.deleteVertexLocked(key)
			s.setVertexTombstoneLocked(key, ts, expiration)
			if barrier, ok := s.cache.vertexCausalBarriers[key]; ok && !ts.Less(barrier) {
				s.clearVertexCausalBarrierLocked(key)
			}
			if live, ok := s.cache.vertexHLC[key]; ok && !ts.Less(live) {
				s.clearVertexHLCLocked(key)
			}
		}
	})
}

func (s *stagedVertexMutation[S, T]) applyLocked(apply func()) {
	if s.applied {
		panic("graphcache: staged vertex mutation applied twice")
	}
	s.applied = true
	complete := false
	s.cache.vertices.SetOnEvictMany(nil)
	defer s.cache.vertices.SetOnEvictMany(s.cache.onVerticesEvicted)
	defer func() {
		if !complete {
			s.rollbackLocked()
		}
	}()
	if s.searchIndex != nil {
		s.searchIndex.IndexManyPreparedValidatedAt(s.searchAfter, s.applicationTime)
	}
	apply()
	complete = true
}

func (s *stagedVertexMutation[S, T]) upsertVertexLocked(
	key S, value T, expiration time.Time,
) {
	if s.cache.vertices.UpsertWithExpiration(key, value, expiration) {
		return
	}
	s.cache.dict.intern(key)
	if s.cache.prefixIndex != nil {
		s.cache.prefixIndex.insert(s.projections[key])
	}
}

func (s *stagedVertexMutation[S, T]) deleteVertexLocked(key S) bool {
	if !s.cache.vertices.Delete(key) {
		return false
	}
	id, ok := s.cache.dict.lookup(key)
	if !ok {
		panic("graphcache: staged vertex dictionary drift")
	}
	s.cache.dict.release(id)
	if s.cache.prefixIndex != nil {
		s.cache.prefixIndex.delete(s.projections[key])
	}
	return true
}

func (s *stagedVertexMutation[S, T]) recordVertexHLCLocked(key S, ts hlc.Timestamp) {
	if ts == (hlc.Timestamp{}) {
		s.clearVertexHLCLocked(key)
		return
	}
	c := s.cache
	if c.vertexHLC == nil {
		c.vertexHLC = make(map[S]hlc.Timestamp)
	}
	c.vertexHLC[key] = ts
	s.reconcileVertexCausalUsageLocked(key)
}

func (s *stagedVertexMutation[S, T]) clearVertexHLCLocked(key S) {
	if s.cache.vertexHLC != nil {
		delete(s.cache.vertexHLC, key)
	}
	s.reconcileVertexCausalUsageLocked(key)
}

func (s *stagedVertexMutation[S, T]) recordVertexCausalBarrierLocked(key S, ts hlc.Timestamp) {
	if ts == (hlc.Timestamp{}) {
		return
	}
	c := s.cache
	if c.vertexCausalBarriers == nil {
		c.vertexCausalBarriers = make(map[S]hlc.Timestamp)
	}
	if existing, ok := c.vertexCausalBarriers[key]; ok && ts.Less(existing) {
		return
	}
	c.vertexCausalBarriers[key] = ts
	s.reconcileVertexCausalUsageLocked(key)
}

func (s *stagedVertexMutation[S, T]) clearVertexCausalBarrierLocked(key S) {
	c := s.cache
	if c.vertexCausalBarriers != nil {
		delete(c.vertexCausalBarriers, key)
		if len(c.vertexCausalBarriers) == 0 {
			c.vertexCausalBarriers = nil
		}
	}
	s.reconcileVertexCausalUsageLocked(key)
}

func (s *stagedVertexMutation[S, T]) setVertexTombstoneLocked(
	key S, ts hlc.Timestamp, expiration time.Time,
) {
	if ts == (hlc.Timestamp{}) {
		return
	}
	c := s.cache
	if c.vertexTombstones == nil {
		c.vertexTombstones = make(map[S]tombstoneEntry)
	}
	if existing, ok := c.vertexTombstones[key]; ok {
		if ts.Less(existing.ts) {
			return
		}
		expiration = clampTombstoneExpirationOnReplay(existing, ts, expiration)
	}
	c.vertexTombstones[key] = tombstoneEntry{ts: ts, expiration: expiration}
	if !expiration.IsZero() {
		s.causal.deadlines.push(
			&c.vertexTombstoneDeadlines,
			causalDeadlineEntry[S]{key: key, deadline: expiration},
		)
		c.vertexTombstoneDeadlineBytes += causalVertexDeadlineEntryBaseBytes + causalKeyPayloadBytes(key)
		c.updateVertexCausalBytesHighWaterLocked()
	}
	s.refreshOldestVertexTombstoneDeadlineLocked()
	s.reconcileVertexCausalUsageLocked(key)
}

func (s *stagedVertexMutation[S, T]) clearVertexTombstoneLocked(key S) {
	if s.cache.vertexTombstones != nil {
		delete(s.cache.vertexTombstones, key)
	}
	s.refreshOldestVertexTombstoneDeadlineLocked()
	s.reconcileVertexCausalUsageLocked(key)
}

func (s *stagedVertexMutation[S, T]) refreshOldestVertexTombstoneDeadlineLocked() {
	c := s.cache
	for len(c.vertexTombstoneDeadlines) > 0 {
		entry := c.vertexTombstoneDeadlines[0]
		current, ok := c.vertexTombstones[entry.key]
		if ok && !current.expiration.IsZero() && current.expiration.Equal(entry.deadline) {
			c.oldestVertexTombstoneDeadline = entry.deadline
			return
		}
		removed := s.causal.deadlines.pop(&c.vertexTombstoneDeadlines)
		c.vertexTombstoneDeadlineBytes -= causalVertexDeadlineEntryBaseBytes + causalKeyPayloadBytes(removed.key)
	}
	c.oldestVertexTombstoneDeadline = time.Time{}
}

func (s *stagedVertexMutation[S, T]) reconcileVertexCausalUsageLocked(key S) {
	c := s.cache
	if c.vertexHasCausalStateLocked(key) {
		c.ensureVertexCausalUsageLocked(key)
		return
	}
	if bytes, ok := c.vertexCausalUsage[key]; ok {
		delete(c.vertexCausalUsage, key)
		c.vertexCausalUsageBytes -= bytes
	}
}

func (s *stagedVertexMutation[S, T]) rollbackLocked() {
	if !s.applied {
		return
	}
	c := s.cache
	for _, key := range s.keys {
		before := s.slots[key]
		if before.present {
			c.vertices.UpsertWithExpiration(key, before.value, before.expiration)
		} else {
			c.vertices.Delete(key)
		}
	}
	s.dict.restore(c.dict)
	if c.prefixIndex != nil {
		for projected, present := range s.prefixBefore {
			if present {
				c.prefixIndex.insert(projected)
			} else {
				c.prefixIndex.delete(projected)
			}
		}
	}
	restoreStagedVertexCausalUndo(&s.causal, c)
	if s.searchIndex != nil {
		s.searchIndex.IndexManyPreparedValidatedAt(s.searchBefore, s.applicationTime)
	}
	s.applied = false
}

func (s *stagedVertexMutation[S, T]) release() {
	s.cache.searchCommitMu.Unlock()
	s.cache.publicationGate.Unlock()
	s.cache.mu.Unlock()
}

func (s *stagedVertexMutation[S, T]) abort() {
	defer s.release()
	if s.applied {
		s.cache.vertices.SetOnEvictMany(nil)
		defer s.cache.vertices.SetOnEvictMany(s.cache.onVerticesEvicted)
		s.rollbackLocked()
	}
}

func (c *GraphCache[S, T]) unlockStagedVertexBegin() {
	c.searchCommitMu.Unlock()
	c.publicationGate.Unlock()
	c.mu.Unlock()
}
