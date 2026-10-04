package graphcache

import (
	"errors"
	"math"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
)

// CreateOutcome describes an existing-endpoints-only conditional Edge create.
// It never contains an existing Edge's value, expiration or contribution IDs.
type CreateOutcome uint8

const (
	CreateOutcomeCreatedAndLive CreateOutcome = iota + 1
	CreateOutcomeEdgeExists
	CreateOutcomeEndpointNotLive
	CreateOutcomeExpired
)

// IndexedEdgeCreate records only the request positions that created an Edge.
type IndexedEdgeCreate[S comparable] struct {
	Index int
	Item  EdgeItem[S]
}

type EdgeCreateStageResult[S comparable] struct {
	Outcomes []CreateOutcome
	Accepted []IndexedEdgeCreate[S]
}

// EdgeCreateTransaction owns aggregate and point-read visibility until the
// enclosing publication commits or definitely aborts. This local primitive
// provides no distributed absence proof. While open, its owner must not call
// any other GraphCache method on this cache.
type EdgeCreateTransaction[S comparable, T any] struct {
	cache  *GraphCache[S, T]
	stage  *stagedEdgeAdd[S, T]
	dict   stagedVertexDictionaryUndo[S]
	result EdgeCreateStageResult[S]

	usageMap   map[EdgeKey[S]]uint64
	usage      map[EdgeKey[S]]stagedValue[uint64]
	usageBytes uint64
	usagePeak  int
	highWater  int
	bytesHigh  uint64
	closed     bool
}

// BeginEdgeCreate checks input TTL, both endpoint liveness and absence at one
// application-time sample under the final graph lock. Any unexpired existing
// contribution collides, including a zero aggregate. Only Edge buckets and
// their indexes change; endpoint values, lifetimes and search docs are untouched.
func (c *GraphCache[S, T]) BeginEdgeCreate(items []EdgeItem[S], ts hlc.Timestamp) (*EdgeCreateTransaction[S, T], error) {
	return c.beginEdgeCreate(items, ts, false)
}

// BeginEdgeCreateReplay restores only previously accepted local effects. TTL
// expiration, missing endpoints or newer causal floors may suppress visibility;
// no endpoint is fabricated and a live collision still fails closed.
func (c *GraphCache[S, T]) BeginEdgeCreateReplay(items []EdgeItem[S], ts hlc.Timestamp) (*EdgeCreateTransaction[S, T], error) {
	return c.beginEdgeCreate(items, ts, true)
}

func (c *GraphCache[S, T]) beginEdgeCreate(items []EdgeItem[S], ts hlc.Timestamp, replay bool) (*EdgeCreateTransaction[S, T], error) {
	if c.publicationGate == nil {
		return nil, errors.New("graphcache: staged Create requires a publication gate")
	}
	for _, item := range items {
		if item.Weight == 0 || math.IsNaN(float64(item.Weight)) || math.IsInf(float64(item.Weight), 0) || !item.ContribID.IsZero() {
			return nil, errors.New("graphcache: Create requires finite nonzero weights and no contribution identity")
		}
	}
	// Projection callbacks run outside exclusive locks and never during Apply.
	c.mu.RLock()
	extract, indexed := c.prefixExtract, c.headByTail != nil
	c.mu.RUnlock()
	projected := make(map[S]string)
	if indexed {
		for _, item := range items {
			if _, known := projected[item.Head]; !known {
				projected[item.Head] = extract(item.Head)
			}
		}
	}
	c.mu.Lock()
	c.publicationGate.Lock()
	owned := true
	var tx *EdgeCreateTransaction[S, T]
	defer func() {
		if owned {
			if tx != nil {
				tx.rollbackLocked()
			}
			c.publicationGate.Unlock()
			c.mu.Unlock()
		}
	}()
	if indexed != (c.headByTail != nil) {
		return nil, errors.New("graphcache: head projection changed during staged Create")
	}
	now := c.applicationTime()
	result := EdgeCreateStageResult[S]{Outcomes: make([]CreateOutcome, len(items)), Accepted: make([]IndexedEdgeCreate[S], 0, len(items))}
	plans := make([]*stagedEdgeAddPlan[S], 0, len(items))
	created := make(map[EdgeKey[S]]bool, len(items))
	var keys []S
	for i, item := range items {
		if !cache.IsLiveAt(item.Expiration, now) {
			result.Outcomes[i] = CreateOutcomeExpired
			continue
		}
		if !c.vertices.HasAt(item.Tail, now) || !c.vertices.HasAt(item.Head, now) {
			result.Outcomes[i] = CreateOutcomeEndpointNotLive
			continue
		}
		key := EdgeKey[S]{Tail: item.Tail, Head: item.Head}
		before := c.edges.bucket(item.Tail, item.Head)
		if created[key] || before != nil && before.hasLiveContributionAt(now) {
			if replay {
				return nil, errors.New("graphcache: accepted Create replay collided with a live Edge")
			}
			result.Outcomes[i] = CreateOutcomeEdgeExists
			continue
		}
		// Never reinterpret a stale local creation as a new causal write.
		// Durable replay uses the stored accepted effect, not this condition.
		if ts != (hlc.Timestamp{}) && !c.edgeAddWriteAllowedLocked(item.Tail, item.Head, ts) {
			if replay {
				result.Outcomes[i] = CreateOutcomeEdgeExists
				continue
			}
			return nil, errors.New("graphcache: Create timestamp does not advance the causal floor")
		}
		after := newWeight()
		after.values = []weightValue{{value: item.Weight, expiration: item.Expiration, hlc: ts}}
		after.sum, after.lastFlushLen, after.lastHLC = item.Weight, 1, ts
		after.noteExpirationLocked(item.Expiration)
		plan := &stagedEdgeAddPlan[S]{key: key, before: before, after: after, headProjected: projected[item.Head]}
		if before != nil {
			var ok bool
			plan.tailID, plan.headID, ok = c.edges.lookupIDs(item.Tail, item.Head)
			if !ok {
				return nil, errors.New("graphcache: staged Create dictionary drift")
			}
		}
		plans = append(plans, plan)
		keys = append(keys, item.Tail, item.Head)
		created[key] = true
		result.Outcomes[i] = CreateOutcomeCreatedAndLive
		result.Accepted = append(result.Accepted, IndexedEdgeCreate[S]{Index: i, Item: item})
	}
	if ts != (hlc.Timestamp{}) {
		causalKeys := make([]EdgeKey[S], len(plans))
		for i, plan := range plans {
			causalKeys[i] = plan.key
		}
		if err := c.checkEdgeCausalCapacityLocked(causalKeys); err != nil {
			return nil, err
		}
	}
	candidate := &EdgeCreateTransaction[S, T]{cache: c, stage: &stagedEdgeAdd[S, T]{cache: c, plans: plans, edgeCount: c.edges.edgeCount, dfBefore: make(map[vertexID]stagedValue[int]), applied: true}, result: result,
		usageMap: c.edgeCausalUsage, usage: make(map[EdgeKey[S]]stagedValue[uint64], len(plans)), usageBytes: c.edgeCausalUsageBytes, usagePeak: c.edgeCausalUsagePeak, highWater: c.edgeCausalHighWater, bytesHigh: c.edgeCausalBytesHighWater}
	candidate.dict.capture(c.dict, uniqueVertexKeys(keys), len(keys))
	for _, plan := range plans {
		value, present := c.edgeCausalUsage[plan.key]
		candidate.usage[plan.key] = stagedValue[uint64]{value, present}
	}
	tx = candidate
	for _, plan := range plans {
		if plan.before != nil {
			c.edges.mu.Lock()
			c.edges.tf[plan.tailID][plan.headID] = plan.after
			c.edges.mu.Unlock()
		} else {
			plan.tailID, plan.headID = c.dict.intern(plan.key.Tail), c.dict.intern(plan.key.Head)
			if _, seen := tx.stage.dfBefore[plan.headID]; !seen {
				count, present := c.edges.df[plan.headID]
				tx.stage.dfBefore[plan.headID] = stagedValue[int]{count, present}
			}
			c.edges.mu.Lock()
			heads := c.edges.tf[plan.tailID]
			if heads == nil {
				heads = make(map[vertexID]*weight)
				c.edges.tf[plan.tailID] = heads
			}
			heads[plan.headID] = plan.after
			c.edges.df[plan.headID]++
			c.edges.edgeCount++
			c.edges.mu.Unlock()
			plan.created = true
			if indexed {
				index := c.headByTail[plan.tailID]
				if index == nil {
					index = newHeadIndex()
					c.headByTail[plan.tailID] = index
				}
				index.insert(plan.headID, plan.headProjected)
			}
		}
		c.reconcileEdgeCausalUsageLocked(plan.key)
	}
	owned = false
	return tx, nil
}

func (w *weight) hasLiveContributionAt(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, value := range w.values {
		if cache.IsLiveAt(value.expiration, now) {
			return true
		}
	}
	return false
}

func (tx *EdgeCreateTransaction[S, T]) Result() EdgeCreateStageResult[S] {
	return EdgeCreateStageResult[S]{Outcomes: append([]CreateOutcome(nil), tx.result.Outcomes...), Accepted: append([]IndexedEdgeCreate[S](nil), tx.result.Accepted...)}
}
func (tx *EdgeCreateTransaction[S, T]) Commit() {
	if tx == nil || tx.closed {
		panic("graphcache: staged Create committed in invalid state")
	}
	tx.closed = true
	tx.cache.publicationGate.Unlock()
	tx.cache.mu.Unlock()
}
func (tx *EdgeCreateTransaction[S, T]) rollbackLocked() {
	tx.stage.rollbackEdgesLocked()
	tx.dict.restore(tx.cache.dict)
	for key, before := range tx.usage {
		if before.set {
			tx.usageMap[key] = before.value
		} else {
			delete(tx.usageMap, key)
		}
	}
	c := tx.cache
	c.edgeCausalUsage, c.edgeCausalUsageBytes, c.edgeCausalUsagePeak = tx.usageMap, tx.usageBytes, tx.usagePeak
	c.edgeCausalHighWater, c.edgeCausalBytesHighWater = tx.highWater, tx.bytesHigh
}
func (tx *EdgeCreateTransaction[S, T]) Abort() {
	if tx == nil || tx.closed {
		return
	}
	tx.closed = true
	defer tx.cache.mu.Unlock()
	defer tx.cache.publicationGate.Unlock()
	tx.rollbackLocked()
}
