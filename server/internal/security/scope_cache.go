package security

import (
	"container/list"
	"sync"
)

const scopeCacheBytes = 8 << 20
const scopeCacheEntries = 256

type scopeCacheEntry struct {
	key   string
	scope *Scope
	bytes int
}

// scopeCache belongs to one immutable compiled Role policy, not to identities
// or tokens. Both retained bytes and entries are bounded under membership
// churn. Active queries may safely retain an evicted immutable scope.
type scopeCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	order      list.List
	bytes      int
	maxBytes   int
	maxEntries int
}

func newScopeCache() *scopeCache {
	return &scopeCache{entries: make(map[string]*list.Element), maxBytes: scopeCacheBytes, maxEntries: scopeCacheEntries}
}

func (c *scopeCache) get(key string, build func() *Scope) *Scope {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		c.order.MoveToFront(element)
		return element.Value.(scopeCacheEntry).scope
	}
	// Coalesce bounded compilation while holding only this derived-cache lock.
	// No Store/graph lock, network call, or I/O occurs inside build.
	scope := build()
	bytes := 128 + len(key) + 32*len(scope.ranges)
	for _, r := range scope.ranges {
		bytes += len(r.Lower) + len(r.Upper)
	}
	if bytes > c.maxBytes {
		return scope
	}
	for len(c.entries) >= c.maxEntries || c.bytes+bytes > c.maxBytes {
		element := c.order.Back()
		if element == nil {
			return scope
		}
		entry := element.Value.(scopeCacheEntry)
		delete(c.entries, entry.key)
		c.bytes -= entry.bytes
		c.order.Remove(element)
	}
	element := c.order.PushFront(scopeCacheEntry{key, scope, bytes})
	c.entries[key] = element
	c.bytes += bytes
	return scope
}
