package cache

import (
	"context"
	"maps"
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

	var noop T
	v, ok := c.cache[key]
	if !ok || v.IsExpired() {
		// Expired entries are cleaned up by the periodic Flush in Watch;
		// avoid spawning a goroutine per lookup.
		return noop, false
	}
	return v.value, true
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

// Delete removes the entry for key. It returns true if the key was
// present (and therefore removed by this call), false otherwise.
func (c *Cache[S, T]) Delete(key S) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.cache[key]; !ok {
		return false
	}
	delete(c.cache, key)
	return true
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

// Flush evicts every entry whose TTL has passed. It returns the number of
// entries removed so callers (e.g. server-side TTL metrics) can record
// expiration counts without scanning the cache again.
func (c *Cache[S, T]) Flush() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	before := len(c.cache)
	maps.DeleteFunc(c.cache, func(_ S, v volatile[T]) bool {
		return v.IsExpired()
	})
	return before - len(c.cache)
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
