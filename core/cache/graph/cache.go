package graph

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/collection/pq"
	"github.com/anaregdesign/lantern/core/graph"
)

type GraphCache[S comparable, T any] struct {
	mu         sync.RWMutex
	defaultTTL time.Duration
	vertices   *cache.Cache[S, T]
	edges      *edgeCache[S]
	// dict is the shared vertex-id allocator. Both the vertex cache and the
	// edge cache reference each key through this dictionary so the heavy
	// edge maps can be keyed by uint32 instead of S. The vertex cache holds
	// one reference per live entry (released via SetOnEvict); the edge cache
	// holds one reference per endpoint per edge.
	dict *dictionary[S]

	// GC observability hooks. Both are optional; the cache stays metrics-free
	// by default. The server wires Prometheus collectors via SetGCHooks so
	// reporting lives outside core.
	//
	// onExpire is invoked once per Watch tick per kind ("vertex" | "edge" |
	// "dangling_edge") with the number of entries the tick removed.
	// onGCDuration is invoked once per Watch tick with the wall-clock time
	// spent inside the tick.
	hookMu       sync.RWMutex
	onExpire     func(kind string, n int)
	onGCDuration func(d time.Duration)
}

func NewGraphCache[S comparable, T any](defaultTTL time.Duration) *GraphCache[S, T] {
	dict := newDictionary[S]()
	vertices := cache.NewCache[S, T](defaultTTL)
	// Vertex eviction (Delete, Clear, or Flush) must release the vertex
	// cache's one dictionary reference per live key. The callback fires
	// AFTER the inner cache lock is released (see cache.Cache.SetOnEvict),
	// so taking dict.mu here cannot deadlock. Edges that still reference
	// the evicted key keep refcount > 0 until the dangling-edge sweep
	// removes them.
	vertices.SetOnEvict(func(key S) {
		if id, ok := dict.lookup(key); ok {
			dict.release(id)
		}
	})
	return &GraphCache[S, T]{
		defaultTTL: defaultTTL,
		vertices:   vertices,
		edges:      newEdgeCache[S](defaultTTL, dict),
		dict:       dict,
	}
}

// putVertexLocked inserts (or refreshes) a vertex entry, interning the key
// in the dictionary exactly once per net live entry. Caller must hold c.mu.
func (c *GraphCache[S, T]) putVertexLocked(key S, value T, expiration time.Time) {
	if c.dict != nil && !c.vertices.Has(key) {
		c.dict.intern(key)
	}
	c.vertices.PutWithExpiration(key, value, expiration)
}

// ensureVertexLocked auto-creates an endpoint vertex (used by edge writes)
// without overwriting an existing value. The dict reference is taken only
// on the first insertion so refcount tracks the cache contents 1:1.
func (c *GraphCache[S, T]) ensureVertexLocked(key S, expiration time.Time) {
	if c.vertices.Has(key) {
		return
	}
	if c.dict != nil {
		c.dict.intern(key)
	}
	var noop T
	c.vertices.PutWithExpiration(key, noop, expiration)
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

// GetEdgeDetail returns the current edge weight together with the latest
// contribution expiration. The expiration is the moment after which the edge
// is guaranteed to have decayed to zero. When no edge exists, ok is false.
func (c *GraphCache[S, T]) GetEdgeDetail(tail, head S) (float32, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.edges.getDetail(tail, head)
}

func (c *GraphCache[S, T]) PutVertexWithExpiration(key S, value T, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.putVertexLocked(key, value, expiration)
}

func (c *GraphCache[S, T]) PutVertexWithTTL(key S, value T, ttl time.Duration) {
	c.PutVertexWithExpiration(key, value, time.Now().Add(ttl))
}

func (c *GraphCache[S, T]) PutVertex(key S, value T) {
	c.PutVertexWithTTL(key, value, c.defaultTTL)
}

func (c *GraphCache[S, T]) AddEdgeWithExpiration(tail, head S, w float32, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Auto-create endpoint vertices synchronously so that a subsequent flush
	// cannot race ahead and drop the edge we are about to add. The inner
	// vertex cache has its own mutex, so calling it while holding c.mu is safe.
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	c.edges.addWithExpiration(tail, head, w, expiration)
}

func (c *GraphCache[S, T]) AddEdgeWithTTL(tail, head S, w float32, ttl time.Duration) {
	c.AddEdgeWithExpiration(tail, head, w, time.Now().Add(ttl))
}

func (c *GraphCache[S, T]) AddEdge(tail, head S, w float32) {
	c.AddEdgeWithTTL(tail, head, w, c.defaultTTL)
}

// PutEdgeWithExpiration atomically replaces the (tail, head) edge weight.
// AddEdgeWithExpiration is additive (Add semantics) so a naive
// "DeleteEdge + AddEdgeWithExpiration" sequence performed by callers
// exposes a window in which concurrent GetEdge readers observe a spurious
// NotFound. PutEdgeWithExpiration takes the write lock once and performs
// the delete + add under the same lock, restoring atomicity for the
// idempotent Put semantics.
func (c *GraphCache[S, T]) PutEdgeWithExpiration(tail, head S, w float32, expiration time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Same endpoint auto-creation invariant as AddEdgeWithExpiration.
	c.ensureVertexLocked(tail, expiration)
	c.ensureVertexLocked(head, expiration)
	c.edges.delete(tail, head)
	c.edges.addWithExpiration(tail, head, w, expiration)
}

// DeleteVertex removes the vertex (by key) and returns whether it was present.
func (c *GraphCache[S, T]) DeleteVertex(key S) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.vertices.Delete(key)
}

// DeleteEdge removes the (tail, head) edge and returns whether it was present.
func (c *GraphCache[S, T]) DeleteEdge(tail, head S) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.edges.delete(tail, head)
}
func (c *GraphCache[S, T]) flush() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	for tail, heads := range c.edges.snapshotTF() {
		for head := range heads {
			if !c.vertices.Has(tail) || !c.vertices.Has(head) {
				if c.edges.delete(tail, head) {
					removed++
				}
			}
		}
	}
	return removed
}

// VertexCount returns the live vertex count under an RLock. Intended for
// Prometheus gauges that sample the cache periodically.
func (c *GraphCache[S, T]) VertexCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.vertices.Count()
}

// EdgeCount returns the live (tail, head) edge count under an RLock. Like
// VertexCount it is intended for periodic gauge sampling, not hot paths.
func (c *GraphCache[S, T]) EdgeCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.edges.count()
}

// SetGCHooks installs optional observability callbacks invoked from Watch
// after every GC tick. Either argument may be nil. Hooks must not call back
// into the cache (re-entrant locking would deadlock). Safe for concurrent use.
func (c *GraphCache[S, T]) SetGCHooks(onExpire func(kind string, n int), onGCDuration func(d time.Duration)) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	c.onExpire = onExpire
	c.onGCDuration = onGCDuration
}

func (c *GraphCache[S, T]) snapshotHooks() (func(string, int), func(time.Duration)) {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()
	return c.onExpire, c.onGCDuration
}

func (c *GraphCache[S, T]) Neighbor(seed S, step int, k int, tfidf bool) *graph.Graph[S, T] {
	g, _ := c.NeighborContext(context.Background(), seed, step, k, tfidf)
	return g
}

// NeighborContext is the context-aware variant of Neighbor. It returns
// ctx.Err() as soon as the context is cancelled or its deadline has expired
// — checked between BFS expansion steps — so handlers can short-circuit
// large traversals when the caller has given up.
func (c *GraphCache[S, T]) NeighborContext(ctx context.Context, seed S, step int, k int, tfidf bool) (*graph.Graph[S, T], error) {
	g, _, err := c.neighborContext(ctx, seed, step, k, tfidf, false)
	return g, err
}

// NeighborWithExpirationsContext returns the same subgraph as
// NeighborContext together with a parallel expirations map keyed by
// (tail, head). Both are computed under a single RLock so handlers can
// compose responses without re-acquiring the cache lock per edge.
//
// The expirations map only contains entries for edges that ended up in
// the returned graph; a missing or zero value means the edge has no
// known expiration.
func (c *GraphCache[S, T]) NeighborWithExpirationsContext(ctx context.Context, seed S, step int, k int, tfidf bool) (*graph.Graph[S, T], map[S]map[S]time.Time, error) {
	return c.neighborContext(ctx, seed, step, k, tfidf, true)
}

func (c *GraphCache[S, T]) neighborContext(ctx context.Context, seed S, step int, k int, tfidf bool, collectExpirations bool) (*graph.Graph[S, T], map[S]map[S]time.Time, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g := graph.NewGraph[S, T]()

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	if v, ok := c.vertices.Get(seed); !ok {
		return g, nil, nil
	} else {
		g.Vertices[seed] = v
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	// targets / seen are accessed only from the main goroutine (between
	// wg.Wait barriers), so plain maps suffice — no need for the locked
	// set.Set wrapper. mu still protects concurrent writes to g.Edges.
	targets := map[S]struct{}{seed: {}}
	seen := make(map[S]struct{})
	// Snapshot edge maps once per Neighbor call. The TF clone is shallow so the
	// per-edge *weight values remain shared and internally thread-safe.
	tf := c.edges.snapshotTF()
	df := c.edges.snapshotDF()
	for range step {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		// Collect this step's frontier (targets not yet processed). Marking
		// happens after wg.Wait so the goroutines need not touch `seen`.
		frontier := make([]S, 0, len(targets))
		for t := range targets {
			if _, ok := seen[t]; ok {
				continue
			}
			frontier = append(frontier, t)
		}

		for _, tail := range frontier {
			wg.Add(1)
			go func(t S) {
				defer wg.Done()
				heads := tf[t]
				if len(heads) == 0 {
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
			}(tail)
		}

		// Wait for all goroutines to finish
		wg.Wait()

		// Mark this step's frontier as processed.
		for _, t := range frontier {
			seen[t] = struct{}{}
		}

		// Find all next targets
		for _, heads := range g.Edges {
			for head := range heads {
				if _, ok := seen[head]; !ok {
					targets[head] = struct{}{}
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

	// Collect expirations under the same RLock so the handler can compose
	// edges without re-locking the cache O(E) times. Only edges that
	// survived the Top-k filter are queried.
	var expirations map[S]map[S]time.Time
	if collectExpirations {
		expirations = make(map[S]map[S]time.Time, len(g.Edges))
		for tail, heads := range g.Edges {
			if len(heads) == 0 {
				continue
			}
			row := make(map[S]time.Time, len(heads))
			tfRow := tf[tail]
			for head := range heads {
				if w, ok := tfRow[head]; ok {
					row[head] = w.latestExpiration()
				}
			}
			expirations[tail] = row
		}
	}

	return g, expirations, nil
}

func (c *GraphCache[S, T]) Watch(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			start := time.Now()
			vExpired := c.vertices.Flush()
			eExpired := c.edges.flush()
			dRemoved := c.flush()
			d := time.Since(start)
			onExpire, onGC := c.snapshotHooks()
			if onExpire != nil {
				if vExpired > 0 {
					onExpire("vertex", vExpired)
				}
				if eExpired > 0 {
					onExpire("edge", eExpired)
				}
				if dRemoved > 0 {
					onExpire("dangling_edge", dRemoved)
				}
			}
			if onGC != nil {
				onGC(d)
			}

		case <-ctx.Done():
			return
		}
	}
}
