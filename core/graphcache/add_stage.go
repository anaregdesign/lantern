package graphcache

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
)

type stagedEdgeAdd[S comparable, T any] struct {
	cache     *GraphCache[S, T]
	vertex    *stagedVertexMutation[S, T]
	plans     []*stagedEdgeAddPlan[S]
	effective []float32
	accepted  []bool
	edgeCount int
	dfBefore  map[vertexID]stagedValue[int]
	applied   bool
}

type stagedEdgeAddPlan[S comparable] struct {
	key           EdgeKey[S]
	before        *weight
	after         *weight
	tailID        vertexID
	headID        vertexID
	headProjected string
	created       bool
}

func (c *GraphCache[S, T]) planStagedEdgeAddLocked(
	items []EdgeItem[S],
	ts hlc.Timestamp,
	now time.Time,
) ([]*stagedEdgeAddPlan[S], []float32, []bool, error) {
	plans := make([]*stagedEdgeAddPlan[S], 0, len(items))
	byKey := make(map[EdgeKey[S]]*stagedEdgeAddPlan[S], len(items))
	effective := make([]float32, len(items))
	accepted := make([]bool, len(items))

	for i, item := range items {
		key := EdgeKey[S]{Tail: item.Tail, Head: item.Head}
		if !c.edgeAddWriteAllowedLocked(item.Tail, item.Head, ts) {
			if before := c.edges.bucket(item.Tail, item.Head); before != nil {
				after := cloneWeightForStaging(before)
				effective[i], _, _ = after.snapshotAt(now)
			}
			continue
		}
		plan := byKey[key]
		if plan == nil {
			before := c.edges.bucket(item.Tail, item.Head)
			after := cloneWeightForStaging(before)
			if after == nil {
				after = newWeight()
			}
			plan = &stagedEdgeAddPlan[S]{
				key: key, before: before, after: after,
			}
			if before != nil {
				var ok bool
				plan.tailID, plan.headID, ok = c.edges.lookupIDs(item.Tail, item.Head)
				if !ok {
					return nil, nil, nil, errors.New("graphcache: staged Add dictionary drift")
				}
			}
			byKey[key] = plan
			plans = append(plans, plan)
		}
		accepted[i], effective[i] = plan.after.addWithExpirationContribHLCAt(
			item.Weight,
			item.Expiration,
			item.ContribID,
			ts,
			now,
		)
	}
	return plans, effective, accepted, nil
}

func (c *GraphCache[S, T]) stagedEdgeAddEndpointItemsLocked(
	items []EdgeItem[S],
	accepted []bool,
	endpoints []VertexItem[S, T],
	indexByKey map[S]int,
	now time.Time,
) []int {
	live := make(map[S]bool, len(endpoints))
	initialized := make(map[S]bool, len(endpoints))
	touched := make(map[int]struct{}, len(endpoints))
	for i, item := range items {
		if !accepted[i] {
			continue
		}
		for _, endpoint := range [...]S{item.Tail, item.Head} {
			if !initialized[endpoint] {
				live[endpoint] = c.vertices.HasAt(endpoint, now)
				initialized[endpoint] = true
			}
			if live[endpoint] {
				continue
			}
			index := indexByKey[endpoint]
			endpoints[index].Expiration = item.Expiration
			touched[index] = struct{}{}
			live[endpoint] = cache.IsLiveAt(item.Expiration, now)
		}
	}
	indexes := make([]int, 0, len(touched))
	for i := range endpoints {
		if _, ok := touched[i]; ok {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func (s *stagedEdgeAdd[S, T]) applyLocked(
	endpoints []VertexItem[S, T],
	endpointIndexes []int,
) {
	if s.applied {
		panic("graphcache: staged Add applied twice")
	}
	s.applied = true
	s.vertex.applyLocked(func() {
		for _, index := range endpointIndexes {
			item := endpoints[index]
			s.vertex.upsertVertexLocked(item.Key, item.Value, item.Expiration)
		}
		s.applyEdgesLocked()
	})
}

func (s *stagedEdgeAdd[S, T]) applyEdgesLocked() {
	complete := false
	defer func() {
		if !complete {
			s.rollbackEdgesLocked()
		}
	}()
	c := s.cache
	s.edgeCount = c.edges.edgeCount
	s.dfBefore = make(map[vertexID]stagedValue[int])
	for _, plan := range s.plans {
		if plan.before != nil {
			c.edges.mu.Lock()
			c.edges.tf[plan.tailID][plan.headID] = plan.after
			c.edges.mu.Unlock()
			continue
		}

		plan.tailID = c.dict.intern(plan.key.Tail)
		plan.headID = c.dict.intern(plan.key.Head)
		if _, ok := s.dfBefore[plan.headID]; !ok {
			count, present := c.edges.df[plan.headID]
			s.dfBefore[plan.headID] = stagedValue[int]{value: count, set: present}
		}
		c.edges.mu.Lock()
		heads := c.edges.tf[plan.tailID]
		if heads == nil {
			heads = make(map[vertexID]*weight)
			c.edges.tf[plan.tailID] = heads
		}
		if heads[plan.headID] != nil {
			c.edges.mu.Unlock()
			panic("graphcache: staged Add edge bucket drift")
		}
		heads[plan.headID] = plan.after
		c.edges.df[plan.headID]++
		c.edges.edgeCount++
		c.edges.mu.Unlock()
		plan.created = true
		c.onEdgeAddedLocked(true, plan.tailID, plan.headID, plan.key.Head)
	}
	complete = true
}

func (s *stagedEdgeAdd[S, T]) rollbackEdgesLocked() {
	if !s.applied {
		return
	}
	c := s.cache
	c.edges.mu.Lock()
	for _, plan := range s.plans {
		if plan.before != nil {
			if heads := c.edges.tf[plan.tailID]; heads != nil {
				heads[plan.headID] = plan.before
			}
			continue
		}
		if !plan.created {
			continue
		}
		if heads := c.edges.tf[plan.tailID]; heads != nil {
			delete(heads, plan.headID)
			if len(heads) == 0 {
				delete(c.edges.tf, plan.tailID)
			}
		}
	}
	for headID, before := range s.dfBefore {
		if before.set {
			c.edges.df[headID] = before.value
		} else {
			delete(c.edges.df, headID)
		}
	}
	c.edges.edgeCount = s.edgeCount
	c.edges.mu.Unlock()

	if c.headByTail != nil {
		for _, plan := range s.plans {
			if !plan.created {
				continue
			}
			if index := c.headByTail[plan.tailID]; index != nil &&
				index.delete(plan.headID, plan.headProjected) {
				delete(c.headByTail, plan.tailID)
			}
			plan.created = false
		}
	}
	s.applied = false
}
