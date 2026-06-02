package graph

import (
	"context"
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

type weight struct {
	values []weightValue
}

func newWeight() *weight {
	return &weight{
		values: make([]weightValue, 0),
	}
}

func (w *weight) value() float32 {
	w.flush()
	var sum float32
	for _, v := range w.values {
		sum += v.value
	}
	return sum
}

func (w *weight) addWithExpiration(value float32, expiration time.Time) {
	w.values = append(w.values, weightValue{
		value:      value,
		expiration: expiration,
	})
}

func (w *weight) addWithTTL(value float32, ttl time.Duration) {
	w.addWithExpiration(value, time.Now().Add(ttl))
}

func (w *weight) isZero() bool {
	return w.value() == 0
}

func (w *weight) flush() {
	v := make([]weightValue, 0)
	for _, value := range w.values {
		if !value.expired() {
			v = append(v, value)
		}
	}
	w.values = v
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
	defer c.mu.RUnlock()

	if _, ok := c.tf[tail]; !ok {
		return 0, false
	}

	if _, ok := c.tf[tail][head]; !ok {
		return 0, false
	}

	if w := c.tf[tail][head]; w.isZero() {
		go c.delete(tail, head)
		return 0, false
	} else {
		return w.value(), true
	}
}

func (c *edgeCache[S]) getTF() map[S]map[S]*weight {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.tf
}

func (c *edgeCache[S]) getDF() map[S]int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.df
}

func (c *edgeCache[S]) addWithExpiration(tail, head S, w float32, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.tf[tail]; !ok {
		c.tf[tail] = make(map[S]*weight)
	}

	if _, ok := c.tf[tail][head]; !ok {
		c.tf[tail][head] = newWeight()
		c.df[head]++
	}

	c.tf[tail][head].addWithExpiration(w, expiration)
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

	if _, ok := c.tf[tail]; !ok {
		return
	}

	if _, ok := c.tf[tail][head]; !ok {
		return
	}

	delete(c.tf[tail], head)
	c.df[head]--
	if c.df[head] <= 0 {
		delete(c.df, head)
	}
	if len(c.tf[tail]) == 0 {
		delete(c.tf, tail)
	}
}

func (c *edgeCache[S]) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for tail, heads := range c.tf {
		for head, w := range heads {
			if w.isZero() {
				c.df[head]--
				if c.df[head] <= 0 {
					delete(c.df, head)
				}
				delete(c.tf[tail], head)
				if len(c.tf[tail]) == 0 {
					delete(c.tf, tail)
				}
			}
		}
	}
}

func (c *edgeCache[S]) watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ticker.C:
			c.flush()

		case <-ctx.Done():
			return

		}
	}
}
