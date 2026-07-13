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

// IsLiveAt reports whether an expiration deadline is still in the future
// relative to now. It centralises the "no expiration" sentinel handling
// shared across the vertex cache, the edge cache, and any future expiring
// store. Two values are treated as "never expires":
//
//   - Go zero time (`time.Time{}`, year 1) — the documented sentinel for
//     "no expiration" used by service.validateExpiration and the proto
//     contract (`Vertex.expiration` / `Edge.expiration` are optional).
//   - Unix epoch or earlier (`expiration.Unix() <= 0`) — `(*timestamppb.
//     Timestamp)(nil).AsTime()` returns `time.Unix(0,0).UTC()`, NOT Go zero,
//     so the cache used to silently treat every PutVertex without an
//     expiration as already-expired (issue #250).
//
// Any positive future deadline behaves as before: live until now passes it.
func IsLiveAt(expiration, now time.Time) bool {
	if expiration.IsZero() || expiration.Unix() <= 0 {
		return true
	}
	return expiration.After(now)
}

// IsLive is the IsLiveAt shorthand that samples time.Now() once per call.
func IsLive(expiration time.Time) bool {
	return IsLiveAt(expiration, time.Now())
}

// IsExpired reports whether this entry has passed its expiration deadline.
// See IsLiveAt for the "no expiration" sentinel semantics.
func (v *volatile[T]) IsExpired() bool {
	return !IsLive(v.expiration)
}

type Cache[S comparable, T any] struct {
	defaultTTL time.Duration
	cache      map[S]volatile[T]
	mu         sync.RWMutex

	// onEvict, if set, is invoked once per key removed by Delete, Clear, or
	// Flush (TTL expiry). Set via SetOnEvict; protected by hookMu so it can
	// be installed concurrently with cache operations. Callbacks fire AFTER
	// the lock on c.mu has been released, so the callback may safely call
	// back into the same Cache without deadlocking — but it must not block
	// indefinitely (it runs inline on the caller's goroutine).
	//
	// onEvictMany is the batch counterpart installed via SetOnEvictMany. When
	// non-nil it TAKES PRECEDENCE over onEvict: a single removal fires it with
	// a one-element slice and a batch removal (DeleteMany, Clear, Flush) fires
	// it once with the whole evicted set, so a layered cache can amortize its
	// per-key index cleanup over one pass and one set of inner-lock
	// acquisitions (#738). Install exactly one of the two hooks.
	hookMu      sync.RWMutex
	onEvict     func(S)
	onEvictMany func([]S)
}

func NewCache[S comparable, T any](defaultTTL time.Duration) *Cache[S, T] {
	return &Cache[S, T]{
		defaultTTL: defaultTTL,
		cache:      make(map[S]volatile[T]),
	}
}

// SetOnEvict installs (or clears, when nil) a callback invoked once per key
// removed by Delete, Clear, or Flush. The callback fires after c.mu has been
// released, so it may re-enter the same Cache without deadlocking. It is
// invoked inline on the caller's goroutine, so it must not block.
//
// Eviction notifications enable layered caches (e.g. GraphCache wiring a
// shared vertex-id dictionary) to release per-entry resources without
// scanning the cache each tick.
func (c *Cache[S, T]) SetOnEvict(fn func(S)) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	c.onEvict = fn
}

// SetOnEvictMany installs (or clears, when nil) a batch eviction callback
// invoked once with the full slice of keys removed by a Delete, DeleteMany,
// Clear, or Flush call. When installed it takes precedence over the per-key
// SetOnEvict hook (the per-key hook is not also called), so install exactly
// one of the two. Like the per-key hook it fires after c.mu has been released,
// so it may re-enter the same Cache without deadlocking, and it must not
// block. The slice is owned by the callback for the duration of the call only.
func (c *Cache[S, T]) SetOnEvictMany(fn func([]S)) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	c.onEvictMany = fn
}

// snapshotHooks returns both eviction hooks under a single hookMu read so a
// caller observes a consistent (one, many) pair.
func (c *Cache[S, T]) snapshotHooks() (one func(S), many func([]S)) {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()
	return c.onEvict, c.onEvictMany
}

// fireEvicted invokes the eviction hooks for evicted after c.mu has been
// released. The batch hook takes precedence over the per-key hook; when only
// the per-key hook is installed it is called once per key. A nil/empty evicted
// slice is a no-op.
func (c *Cache[S, T]) fireEvicted(one func(S), many func([]S), evicted []S) {
	if len(evicted) == 0 {
		return
	}
	if many != nil {
		many(evicted)
		return
	}
	if one != nil {
		for _, k := range evicted {
			one(k)
		}
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

// UpsertWithExpiration stores value for key like PutWithExpiration and reports
// whether an entry for key was PHYSICALLY present beforehand, regardless of
// whether that entry had already expired. It exists so layered callers can
// detect a true first insert in a single lock cycle (vs. a Has()+Put pair, two
// cycles) AND so an expired-but-not-yet-flushed slot is correctly treated as
// "already present" — Has() reports such a slot as absent, which would make a
// caller re-run first-insert side effects (e.g. interning the key again) on a
// slot that was never released, inflating bookkeeping (#739).
func (c *Cache[S, T]) UpsertWithExpiration(key S, value T, expiration time.Time) (physicallyExisted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, physicallyExisted = c.cache[key]
	c.cache[key] = volatile[T]{
		value:      value,
		expiration: expiration,
	}
	return physicallyExisted
}

func (c *Cache[S, T]) PutWithTTL(key S, value T, ttl time.Duration) {
	c.PutWithExpiration(key, value, time.Now().Add(ttl))
}

func (c *Cache[S, T]) Put(key S, value T) {
	c.PutWithTTL(key, value, c.defaultTTL)
}

// Delete removes the entry for key. It returns true if the key was
// present (and therefore removed by this call), false otherwise. When the
// key was present and an eviction callback is installed, it fires once
// after c.mu is released (the batch hook with a one-element slice, else the
// per-key hook).
func (c *Cache[S, T]) Delete(key S) bool {
	c.mu.Lock()
	if _, ok := c.cache[key]; !ok {
		c.mu.Unlock()
		return false
	}
	delete(c.cache, key)
	one, many := c.snapshotHooks()
	c.mu.Unlock()

	switch {
	case many != nil:
		many([]S{key})
	case one != nil:
		one(key)
	}
	return true
}

// DeleteMany removes every supplied key that is present, under a single c.mu
// critical section, and returns the keys actually removed (those present at
// call time) in unspecified order. Absent keys are skipped and not reported;
// duplicate keys are reported at most once. The eviction callback fires once
// after c.mu is released — the batch hook with the whole evicted slice when
// installed, else the per-key hook once per removed key — so a layered cache
// amortizes its index cleanup over one pass (#738).
func (c *Cache[S, T]) DeleteMany(keys []S) []S {
	if len(keys) == 0 {
		return nil
	}
	c.mu.Lock()
	evicted := make([]S, 0, len(keys))
	for _, k := range keys {
		if _, ok := c.cache[k]; ok {
			delete(c.cache, k)
			evicted = append(evicted, k)
		}
	}
	one, many := c.snapshotHooks()
	c.mu.Unlock()

	c.fireEvicted(one, many, evicted)
	return evicted
}

func (c *Cache[S, T]) Has(key S) bool {
	return c.HasAt(key, time.Now())
}

// HasAt reports logical membership at the caller's already-sampled time. It
// lets compound reads apply one liveness instant across several entries
// instead of sampling the wall clock once per probe.
func (c *Cache[S, T]) HasAt(key S, now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.cache[key]
	return ok && IsLiveAt(v.expiration, now)
}

// Clear removes every entry. When an eviction callback is installed it is
// invoked once after c.mu has been released — the batch hook with all removed
// keys, else the per-key hook once per key. Keys are reported in unspecified
// order.
func (c *Cache[S, T]) Clear() {
	c.mu.Lock()
	one, many := c.snapshotHooks()
	hook := one != nil || many != nil
	var evicted []S
	if hook {
		evicted = make([]S, 0, len(c.cache))
		for k := range c.cache {
			evicted = append(evicted, k)
		}
	}
	c.cache = make(map[S]volatile[T])
	c.mu.Unlock()

	c.fireEvicted(one, many, evicted)
}

func (c *Cache[S, T]) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cache)
}

// Range invokes fn for every live (non-expired) entry. fn returns false to
// stop iteration early; the boolean return mirrors the iter.Seq2 contract
// callers may already be wired against. Range takes c.mu.RLock for the
// duration of the walk, so fn MUST NOT call back into the same Cache
// (which would acquire c.mu.Lock and deadlock with the reader-priority
// blockage described in #202). The intended consumer is the replication
// snapshot path (#184), which iterates under the GraphCache write lock
// where this constraint is trivially satisfied.
func (c *Cache[S, T]) Range(fn func(key S, value T, expiration time.Time) bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, v := range c.cache {
		if v.IsExpired() {
			continue
		}
		if !fn(k, v.value, v.expiration) {
			return
		}
	}
}

// Flush evicts every entry whose TTL has passed. It returns the number of
// entries removed so callers (e.g. server-side TTL metrics) can record
// expiration counts without scanning the cache again. When an eviction
// callback is installed it is invoked once after c.mu has been released —
// the batch hook with the whole expired set, else the per-key hook once per
// key — so a layered cache cleans its side indexes in one pass (#738).
func (c *Cache[S, T]) Flush() int {
	c.mu.Lock()
	one, many := c.snapshotHooks()
	if one != nil || many != nil {
		// Two-phase: collect keys under the lock, fire callbacks after release.
		// maps.DeleteFunc cannot collect because the predicate's S is the live
		// key but we need to defer the callback past Unlock.
		var evicted []S
		for k, v := range c.cache {
			if v.IsExpired() {
				evicted = append(evicted, k)
			}
		}
		for _, k := range evicted {
			delete(c.cache, k)
		}
		c.mu.Unlock()
		c.fireEvicted(one, many, evicted)
		return len(evicted)
	}
	before := len(c.cache)
	maps.DeleteFunc(c.cache, func(_ S, v volatile[T]) bool {
		return v.IsExpired()
	})
	removed := before - len(c.cache)
	c.mu.Unlock()
	return removed
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
