package cache

import (
	"context"
	"github.com/anaregdesign/lantern/pkg/core/model/function"
	"time"
)

type LoadingCache[S comparable, T any] struct {
	cache  *Cache[S, T]
	loader function.Loader[S, T]
}

func NewLoadingCache[S comparable, T any](ctx context.Context, loader function.Loader[S, T], defaultTTL time.Duration) *LoadingCache[S, T] {
	return &LoadingCache[S, T]{
		cache:  NewCache[S, T](defaultTTL),
		loader: loader,
	}
}

func (c *LoadingCache[S, T]) Get(key S) (T, bool) {
	if value, ok := c.cache.Get(key); ok {
		return value, true
	}
	if value, ok := c.loader(key); ok {
		c.cache.Put(key, value)
		return value, true
	}
	var noop T
	return noop, false
}

func (c *LoadingCache[S, T]) Set(key S, value T) {
	c.cache.Put(key, value)
}

func (c *LoadingCache[S, T]) SetWithTTL(key S, value T, ttl time.Duration) {
	c.cache.PutWithTTL(key, value, ttl)
}
