package cache

import (
	"context"
	"sync"
	"time"
)

type volatile[T any] struct {
	value      T
	expiration time.Time
}

func (v *volatile[T]) IsExpired() bool {
	return v.expiration.Before(time.Now())
}

type Cache[S comparable, T any] struct {
	defaultTTL time.Duration
	cache      map[S]volatile[T]
	mu         sync.RWMutex
}

func NewCache[S comparable, T any](defaultTTL time.Duration) *Cache[S, T] {
	return &Cache[S, T]{
		defaultTTL: defaultTTL,
		cache:      make(map[S]volatile[T]),
	}
}

func (c *Cache[S, T]) Get(key S) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if v, ok := c.cache[key]; ok {
		if v.IsExpired() {
			go c.Delete(key)
			var noop T
			return noop, false
		}
		return v.value, true
	}
	var noop T
	return noop, false
}

func (c *Cache[S, T]) PutWithExpiration(key S, value T, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[key] = volatile[T]{
		value:      value,
		expiration: expiration,
	}
}

func (c *Cache[S, T]) PutWithTTL(key S, value T, ttl time.Duration) {
	c.PutWithExpiration(key, value, time.Now().Add(ttl))
}

func (c *Cache[S, T]) Put(key S, value T) {
	c.PutWithTTL(key, value, c.defaultTTL)
}

func (c *Cache[S, T]) Delete(key S) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.cache[key]; ok {
		delete(c.cache, key)
	}
}

func (c *Cache[S, T]) Has(key S) bool {

	_, ok := c.Get(key)
	return ok
}
func (c *Cache[S, T]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[S]volatile[T])
}

func (c *Cache[S, T]) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cache)
}

func (c *Cache[S, T]) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range c.cache {
		if v.IsExpired() {
			delete(c.cache, k)
		}
	}
}

func (c *Cache[S, T]) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.Flush()
		case <-ctx.Done():
			return
		}
	}
}
