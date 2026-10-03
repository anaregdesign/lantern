package graphcache

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
)

var ErrLifecycleEffectDenied = errors.New("graphcache: lifecycle reduction denied")

type lifetimeBefore struct {
	expiration time.Time
	live       bool
}

func lifecycleReduction(old lifetimeBefore, next time.Time, now time.Time) bool {
	if !cache.IsLiveAt(next, now) {
		return true
	}
	return old.live && !next.IsZero() && (old.expiration.IsZero() || next.Before(old.expiration))
}

// Optional receipt effect capture reads touched resources only and simulates
// all admitted duplicate positions before any staged write becomes visible.
func (c *GraphCache[S, T]) vertexLifecycleReductionsLocked(items []VertexItem[S, T], accepted []int, now time.Time) []bool {
	capture := false
	for _, item := range items {
		capture = capture || item.CaptureLifecycleReduction
	}
	if !capture {
		return nil
	}
	result := make([]bool, len(items))
	previous := make(map[S]lifetimeBefore, len(accepted))
	for _, index := range accepted {
		item := items[index]
		old, known := previous[item.Key]
		if !known {
			_, expiration, present := c.vertices.PeekWithExpiration(item.Key)
			old = lifetimeBefore{expiration: expiration, live: present && cache.IsLiveAt(expiration, now)}
		}
		result[index] = item.CaptureLifecycleReduction && lifecycleReduction(old, item.Expiration, now)
		previous[item.Key] = lifetimeBefore{expiration: item.Expiration, live: vertexItemLiveAt(item, now)}
	}
	return result
}

// These planners read only touched resources and simulate request-order
// duplicates. They mutate neither graph nor index, and allocate nothing when
// ingress supplied no effect restrictions (including trusted peer apply).
func (c *GraphCache[S, T]) validateVertexLifecycleLocked(items []VertexItem[S, T], accepted []int, now time.Time, all bool) error {
	guarded := false
	for _, item := range items {
		guarded = guarded || item.DenyLifecycleReduction
	}
	if !guarded {
		return nil
	}
	previous := make(map[S]lifetimeBefore, len(items))
	check := func(i int) error {
		item := items[i]
		old, known := previous[item.Key]
		if !known {
			_, expiration, present := c.vertices.PeekWithExpiration(item.Key)
			old = lifetimeBefore{expiration: expiration, live: present && cache.IsLiveAt(expiration, now)}
		}
		if item.DenyLifecycleReduction && lifecycleReduction(old, item.Expiration, now) {
			return ErrLifecycleEffectDenied
		}
		previous[item.Key] = lifetimeBefore{expiration: item.Expiration, live: vertexItemLiveAt(item, now)}
		return nil
	}
	if all {
		for i := range items {
			if err := check(i); err != nil {
				return err
			}
		}
	} else {
		for _, i := range accepted {
			if err := check(i); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *GraphCache[S, T]) validateEdgeLifecycleLocked(items []EdgeItem[S], accepted []int, now time.Time, all bool) error {
	guarded := false
	for _, item := range items {
		guarded = guarded || item.DenyLifecycleReduction
	}
	if !guarded {
		return nil
	}
	previous := make(map[EdgeKey[S]]lifetimeBefore, len(items))
	check := func(i int) error {
		item := items[i]
		key := EdgeKey[S]{Tail: item.Tail, Head: item.Head}
		old, known := previous[key]
		if !known {
			old = c.edgeLifetimeLocked(key, now)
		}
		if item.DenyLifecycleReduction && lifecycleReduction(old, item.Expiration, now) {
			return ErrLifecycleEffectDenied
		}
		previous[key] = lifetimeBefore{expiration: item.Expiration, live: edgeItemLiveAt(item, now)}
		return nil
	}
	if all {
		for i := range items {
			if err := check(i); err != nil {
				return err
			}
		}
	} else {
		for _, i := range accepted {
			if err := check(i); err != nil {
				return err
			}
		}
	}
	return nil
}
func (c *GraphCache[S, T]) validateEdgeHLCLifecycleLocked(items []EdgeItem[S], ts hlc.Timestamp, now time.Time) error {
	guarded := false
	for _, item := range items {
		guarded = guarded || item.DenyLifecycleReduction
	}
	if !guarded {
		return nil
	}
	accepted := make([]int, 0, len(items))
	for i, item := range items {
		if c.edgePutWriteAllowedLocked(item.Tail, item.Head, ts) {
			accepted = append(accepted, i)
		}
	}
	return c.validateEdgeLifecycleLocked(items, accepted, now, false)
}

// Read retained live contributions without flushing the bucket. A permanent
// contribution means an unbounded lifetime, including a zero aggregate weight.
func (c *GraphCache[S, T]) edgeLifetimeLocked(key EdgeKey[S], now time.Time) lifetimeBefore {
	tail, head, known := c.edges.lookupIDs(key.Tail, key.Head)
	if !known {
		return lifetimeBefore{}
	}
	c.edges.mu.RLock()
	weight := c.edges.tf[tail][head]
	c.edges.mu.RUnlock()
	if weight == nil {
		return lifetimeBefore{}
	}
	weight.mu.Lock()
	defer weight.mu.Unlock()
	var old lifetimeBefore
	for _, value := range weight.values {
		if !cache.IsLiveAt(value.expiration, now) {
			continue
		}
		old.live = true
		if value.expiration.IsZero() {
			old.expiration = time.Time{}
			return old
		}
		if value.expiration.After(old.expiration) {
			old.expiration = value.expiration
		}
	}
	return old
}
