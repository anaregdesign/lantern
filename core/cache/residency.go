package cache

import "time"

// SetValueLoader installs the store owner for nonresident payloads. Before
// returning an error the loader must fence its owning runtime. Use
// PeekWithExpirationChecked where the caller can propagate an I/O error.
func (c *Cache[S, T]) SetValueLoader(load func(S, time.Time) (T, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load = load
}

func (c *Cache[S, T]) valueLocked(key S, entry volatile[T]) (T, error) {
	if !entry.cold {
		return entry.value, nil
	}
	return c.load(key, entry.expiration)
}

// ReleaseValue releases only the payload. Membership, expiration and all
// eviction hooks remain unchanged. The store owner calls this after commit.
func (c *Cache[S, T]) ReleaseValue(key S) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || e.cold || c.load == nil {
		return
	}
	var zero T
	e.value, e.cold = zero, true
	c.cache[key] = e
}

// ReleaseValues is the whole-generation counterpart used after an atomic
// switch. It never consults disk or invokes callbacks.
func (c *Cache[S, T]) ReleaseValues() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.load == nil {
		return
	}
	var zero T
	for key, e := range c.cache {
		e.value, e.cold = zero, true
		c.cache[key] = e
	}
}

// PeekResident reports physical presence and payload residency separately.
// A present cold row can be omitted from a batch that didn't change it.
func (c *Cache[S, T]) PeekResident(key S) (value T, expiry time.Time, present, cold bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.cache[key]
	return e.value, e.expiration, ok, e.cold
}

// PeekWithExpirationChecked returns storage errors independently of absence.
func (c *Cache[S, T]) PeekWithExpirationChecked(key S) (value T, expiry time.Time, present bool, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.cache[key]
	if !ok {
		return value, time.Time{}, false, nil
	}
	value, err = c.valueLocked(key, e)
	return value, e.expiration, true, err
}
