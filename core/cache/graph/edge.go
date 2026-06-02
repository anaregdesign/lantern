package graph

import (
	"maps"
	"sync"
	"time"
)

type weightValue struct {
	value      float32
	expiration time.Time
}

func (w weightValue) expired() bool {
	return time.Now().After(w.expiration)
}

// weight aggregates additive contributions to an edge, each with its own
// expiration. It is safe for concurrent use; the cached sum lets readers avoid
// re-scanning the slice on every call.
type weight struct {
	mu     sync.Mutex
	values []weightValue
	sum    float32
}

func newWeight() *weight {
	return &weight{}
}

func (w *weight) value() float32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return w.sum
}

func (w *weight) addWithExpiration(value float32, expiration time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.values = append(w.values, weightValue{
		value:      value,
		expiration: expiration,
	})
	w.sum += value
}

func (w *weight) addWithTTL(value float32, ttl time.Duration) {
	w.addWithExpiration(value, time.Now().Add(ttl))
}

func (w *weight) isZero() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
	return w.sum == 0
}

// flushLocked compacts expired entries in place and recomputes the cached sum.
// Caller must hold w.mu.
func (w *weight) flushLocked() {
	now := time.Now()
	write := 0
	var sum float32
	for _, v := range w.values {
		if v.expiration.After(now) {
			w.values[write] = v
			write++
			sum += v.value
		}
	}
	w.values = w.values[:write]
	w.sum = sum
}

type edgeCache[S comparable] struct {
	mu         sync.RWMutex
	defaultTTL time.Duration
	tf         map[S]map[S]*weight
	df         map[S]int
}

func newEdgeCache[S comparable](defaultTTL time.Duration) *edgeCache[S] {
	return &edgeCache[S]{
		defaultTTL: defaultTTL,
		tf:         make(map[S]map[S]*weight),
		df:         make(map[S]int),
	}
}

func (c *edgeCache[S]) get(tail, head S) (float32, bool) {
	c.mu.RLock()
	heads, ok := c.tf[tail]
	if !ok {
		c.mu.RUnlock()
		return 0, false
	}
	w, ok := heads[head]
	c.mu.RUnlock()
	if !ok {
		return 0, false
	}

	if w.isZero() {
		go c.delete(tail, head)
		return 0, false
	}
	return w.value(), true
}

// snapshotTF returns a shallow copy of the tail->heads map so callers can
// iterate without holding the edgeCache lock. The inner *weight values are
// shared and remain individually thread-safe.
func (c *edgeCache[S]) snapshotTF() map[S]map[S]*weight {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[S]map[S]*weight, len(c.tf))
	for tail, heads := range c.tf {
		out[tail] = maps.Clone(heads)
	}
	return out
}

func (c *edgeCache[S]) snapshotDF() map[S]int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return maps.Clone(c.df)
}

func (c *edgeCache[S]) addWithExpiration(tail, head S, w float32, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	heads, ok := c.tf[tail]
	if !ok {
		heads = make(map[S]*weight)
		c.tf[tail] = heads
	}

	edge, ok := heads[head]
	if !ok {
		edge = newWeight()
		heads[head] = edge
		c.df[head]++
	}

	edge.addWithExpiration(w, expiration)
}

func (c *edgeCache[S]) addWithTTL(tail, head S, w float32, ttl time.Duration) {
	c.addWithExpiration(tail, head, w, time.Now().Add(ttl))
}

func (c *edgeCache[S]) add(tail, head S, w float32) {
	c.addWithTTL(tail, head, w, c.defaultTTL)
}

func (c *edgeCache[S]) delete(tail, head S) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteLocked(tail, head)
}

// deleteLocked performs the same work as delete but assumes the caller already
// holds c.mu in write mode.
func (c *edgeCache[S]) deleteLocked(tail, head S) {
	heads, ok := c.tf[tail]
	if !ok {
		return
	}
	if _, ok := heads[head]; !ok {
		return
	}

	delete(heads, head)
	c.df[head]--
	if c.df[head] <= 0 {
		delete(c.df, head)
	}
	if len(heads) == 0 {
		delete(c.tf, tail)
	}
}

func (c *edgeCache[S]) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for tail, heads := range c.tf {
		for head, w := range heads {
			if w.isZero() {
				c.deleteLocked(tail, head)
			}
		}
	}
}
