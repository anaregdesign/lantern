package graph

import (
	"context"
	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/collection/pq"
	"github.com/anaregdesign/lantern/core/collection/set"
	"github.com/anaregdesign/lantern/core/graph"
	"math"
	"sync"
	"time"
)

type GraphCache[S comparable, T any] struct {
	mu         sync.RWMutex
	defaultTTL time.Duration
	vertices   *cache.Cache[S, T]
	edges      *edgeCache[S]
}

func NewGraphCache[S comparable, T any](defaultTTL time.Duration) *GraphCache[S, T] {
	return &GraphCache[S, T]{
		defaultTTL: defaultTTL,
		vertices:   cache.NewCache[S, T](defaultTTL),
		edges:      newEdgeCache[S](defaultTTL),
	}
}

func (c *GraphCache[S, T]) GetVertex(key S) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.vertices.Get(key)
}

func (c *GraphCache[S, T]) GetWeight(tail, head S) (float32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.edges.get(tail, head)
}

func (c *GraphCache[S, T]) AddVertexWithExpiration(key S, value T, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.vertices.PutWithExpiration(key, value, expiration)
}

func (c *GraphCache[S, T]) AddVertexWithTTL(key S, value T, ttl time.Duration) {
	c.AddVertexWithExpiration(key, value, time.Now().Add(ttl))
}

func (c *GraphCache[S, T]) PutVertex(key S, value T) {
	c.AddVertexWithTTL(key, value, c.defaultTTL)
}

func (c *GraphCache[S, T]) AddEdgeWithExpiration(tail, head S, w float32, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Auto-create endpoint vertices synchronously so that a subsequent flush
	// cannot race ahead and drop the edge we are about to add. The inner
	// vertex cache has its own mutex, so calling it while holding c.mu is safe.
	if !c.vertices.Has(tail) {
		var noop T
		c.vertices.PutWithExpiration(tail, noop, expiration)
	}
	if !c.vertices.Has(head) {
		var noop T
		c.vertices.PutWithExpiration(head, noop, expiration)
	}
	c.edges.addWithExpiration(tail, head, w, expiration)
}

func (c *GraphCache[S, T]) AddEdgeWithTTL(tail, head S, w float32, ttl time.Duration) {
	c.AddEdgeWithExpiration(tail, head, w, time.Now().Add(ttl))
}

func (c *GraphCache[S, T]) AddEdge(tail, head S, w float32) {
	c.AddEdgeWithTTL(tail, head, w, c.defaultTTL)
}

func (c *GraphCache[S, T]) DeleteVertex(key S) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.vertices.Delete(key)
}

func (c *GraphCache[S, T]) DeleteEdge(tail, head S) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.edges.delete(tail, head)
}
func (c *GraphCache[S, T]) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for tail, heads := range c.edges.snapshotTF() {
		for head := range heads {
			if !c.vertices.Has(tail) || !c.vertices.Has(head) {
				c.edges.delete(tail, head)
			}
		}
	}
}

func (c *GraphCache[S, T]) Neighbor(seed S, step int, k int, tfidf bool) *graph.Graph[S, T] {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g := graph.NewGraph[S, T]()

	if v, ok := c.vertices.Get(seed); !ok {
		return g
	} else {
		g.Vertices[seed] = v
	}

	var wg sync.WaitGroup
	var mu sync.RWMutex
	targets := set.NewSet[S]()
	targets.Add(seed)
	seen := set.NewSet[S]()
	// Snapshot edge maps once per Neighbor call. The TF clone is shallow so the
	// per-edge *weight values remain shared and internally thread-safe.
	tf := c.edges.snapshotTF()
	df := c.edges.snapshotDF()
	for range step {

		for _, tail := range targets.Values() {
			// Skip if already seen
			if seen.Has(tail) {
				continue
			}

			// Add to wait group
			wg.Add(1)
			go func(t S) {
				defer wg.Done()
				// Add edges to the graph
				heads := tf[t]
				if len(heads) == 0 {
					seen.Add(t)
					return
				}
				edges := make(pq.SortableMap[S, float32], len(heads))
				for head, w := range heads {
					if tfidf {
						edges[head] = w.value() / float32(math.Log2(float64(1+df[head])))
					} else {
						edges[head] = w.value()
					}
				}

				// Filter light edges
				edges = edges.Top(k)
				mu.Lock()
				g.Edges[t] = edges
				mu.Unlock()
				// Mark as seen
				seen.Add(t)
			}(tail)
		}

		// Wait for all goroutines to finish
		wg.Wait()

		// Find all next targets
		for _, heads := range g.Edges {
			for head := range heads {
				if !seen.Has(head) {
					targets.Add(head)
				}
			}
		}
	}

	// Add vertices to the graph
	for tail, heads := range g.Edges {
		g.Vertices[tail], _ = c.vertices.Get(tail)
		for head := range heads {
			g.Vertices[head], _ = c.vertices.Get(head)
		}
	}

	return g
}

func (c *GraphCache[S, T]) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.vertices.Flush()
			c.edges.flush()
			c.flush()

		case <-ctx.Done():
			return
		}
	}
}
