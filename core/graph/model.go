package graph

import (
	"container/heap"
	"context"

	"github.com/anaregdesign/lantern/core/collection/pq"
	"github.com/anaregdesign/lantern/core/collection/set"
)

type Graph[S comparable, T any] struct {
	Vertices map[S]T             `json:"vertices,omitempty"`
	Edges    map[S]map[S]float32 `json:"edges,omitempty"`
}

func NewGraph[S comparable, T any]() *Graph[S, T] {
	return &Graph[S, T]{
		Vertices: make(map[S]T),
		Edges:    make(map[S]map[S]float32),
	}
}

func (g *Graph[S, T]) PutVertex(key S, value T) {
	g.Vertices[key] = value
}

func (g *Graph[S, T]) PutEdge(tail, head S, weight float32) {
	if _, ok := g.Vertices[tail]; !ok {
		var noop T
		g.Vertices[tail] = noop
	}

	if _, ok := g.Vertices[head]; !ok {
		var noop T
		g.Vertices[head] = noop
	}

	if _, ok := g.Edges[tail]; !ok {
		g.Edges[tail] = make(map[S]float32)
	}
	g.Edges[tail][head] = weight
}

func (g *Graph[S, T]) ConnectedGraph(seed S) *Graph[S, T] {
	connected, _ := g.ConnectedGraphContext(context.Background(), seed)
	return connected
}

// ConnectedGraphContext is the context-aware variant of ConnectedGraph.
// It returns ctx.Err() as soon as the context is cancelled or its deadline
// has expired, so callers can short-circuit large traversals.
//
// Implementation: classic FIFO-queue BFS. Each reachable vertex is enqueued
// exactly once, each outgoing edge is visited exactly once, giving O(V+E)
// time and O(V) auxiliary memory. The prior round-based loop re-scanned
// connected.Edges every iteration for O(V·(V+E)).
func (g *Graph[S, T]) ConnectedGraphContext(ctx context.Context, seed S) (*Graph[S, T], error) {
	connected := NewGraph[S, T]()
	connected.PutVertex(seed, g.Vertices[seed])

	seen := set.NewSet[S]()
	seen.Add(seed)

	queue := make([]S, 0, 16)
	queue = append(queue, seed)

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tail := queue[0]
		queue = queue[1:]

		for head, weight := range g.Edges[tail] {
			if !seen.Has(head) {
				connected.PutVertex(head, g.Vertices[head])
				seen.Add(head)
				queue = append(queue, head)
			}
			connected.PutEdge(tail, head, weight)
		}
	}

	return connected, nil
}

// MinimumSpanningTree returns a minimum spanning tree of the graph rooted at seed.
func (g *Graph[S, T]) MinimumSpanningTree(seed S) *Graph[S, T] {
	mst, _ := g.spanningTreeContext(context.Background(), seed, false)
	return mst
}

// MaximumSpanningTree returns a maximum spanning tree of the graph rooted at seed.
func (g *Graph[S, T]) MaximumSpanningTree(seed S) *Graph[S, T] {
	mst, _ := g.spanningTreeContext(context.Background(), seed, true)
	return mst
}

// MinimumSpanningTreeContext is the context-aware variant of [Graph.MinimumSpanningTree].
func (g *Graph[S, T]) MinimumSpanningTreeContext(ctx context.Context, seed S) (*Graph[S, T], error) {
	return g.spanningTreeContext(ctx, seed, false)
}

// MaximumSpanningTreeContext is the context-aware variant of [Graph.MaximumSpanningTree].
func (g *Graph[S, T]) MaximumSpanningTreeContext(ctx context.Context, seed S) (*Graph[S, T], error) {
	return g.spanningTreeContext(ctx, seed, true)
}

// spanningTreeContext computes either the minimum (negate=false) or maximum
// (negate=true) spanning tree rooted at seed. It is the shared implementation
// behind the exported MinimumSpanningTree / MaximumSpanningTree variants.
//
// Implementation: classic Prim. When a vertex enters the MST, only its
// outgoing edges are pushed onto the priority queue, and stale entries
// pointing at already-included vertices are skipped at pop time. Each edge
// is therefore pushed and popped at most once, giving O((V+E) log V).
// The previous version re-scanned mst.Vertices on every iteration of the
// outer loop, paying an extra O(V) per added vertex.
func (g *Graph[S, T]) spanningTreeContext(ctx context.Context, seed S, negate bool) (*Graph[S, T], error) {
	connected, err := g.ConnectedGraphContext(ctx, seed)
	if err != nil {
		return nil, err
	}

	type edge struct {
		tail   S
		head   S
		weight float32
	}

	mst := NewGraph[S, T]()
	mst.PutVertex(seed, connected.Vertices[seed])

	q := make(pq.PriorityQueue[*edge, float32], 0)
	heap.Init(&q)

	// pq is a max-heap; flip sign for the minimum-tree case so that the
	// smallest original weight surfaces at the top.
	pushOutgoing := func(tail S) {
		for head, weight := range connected.Edges[tail] {
			if _, ok := mst.Vertices[head]; ok {
				continue
			}
			w := weight
			if !negate {
				w = -weight
			}
			heap.Push(&q, &pq.Item[*edge, float32]{
				Value:    &edge{tail: tail, head: head, weight: weight},
				Priority: w,
			})
		}
	}
	pushOutgoing(seed)

	for q.Len() > 0 && len(mst.Vertices) < len(connected.Vertices) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		picked := heap.Pop(&q).(*pq.Item[*edge, float32]).Value
		if _, ok := mst.Vertices[picked.head]; ok {
			// stale: head already absorbed via a better edge
			continue
		}
		mst.PutVertex(picked.head, connected.Vertices[picked.head])
		mst.PutEdge(picked.tail, picked.head, picked.weight)
		pushOutgoing(picked.head)
	}
	return mst, nil
}

// ShortestPathTree
/*
 * ShortestPathTree returns a shortest path tree of the graph from the seed.
 * The costFunc is a function that returns the cost of the edge.
 * Typically, if the weight means like `importance`, the costFunc is a function that returns the 1 / weight.
 * It is calculated by Dijkstra's algorithm, so the costFunc must return a positive value.
 */
func (g *Graph[S, T]) ShortestPathTree(seed S, costFunc func(x float32) float32) *Graph[S, T] {
	spt, _ := g.ShortestPathTreeContext(context.Background(), seed, costFunc)
	return spt
}

// ShortestPathTreeContext is the context-aware variant of ShortestPathTree.
//
// Implementation: classic Dijkstra with relaxation against the original
// graph. A dist[v] table tracks the best known cost from seed; whenever a
// shorter path is discovered, dist/prev are updated and a new PQ entry is
// pushed. Stale entries (top.Priority worse than the current dist[u]) are
// skipped on pop. Each vertex is therefore settled at most once and each
// edge is relaxed at most once, giving O((V+E) log V) time and O(V) active
// PQ entries.
//
// The previous implementation called ConnectedGraphContext first (an extra
// O(V+E) walk plus a full subgraph copy) and used a pivot/position trick
// without a distance table, which allowed the PQ to grow to O(E). Both are
// avoided here.
//
// PriorityQueue is a max-heap; priorities are negated so the smallest dist
// surfaces first. costFunc must return non-negative values (Dijkstra
// precondition).
func (g *Graph[S, T]) ShortestPathTreeContext(ctx context.Context, seed S, costFunc func(x float32) float32) (*Graph[S, T], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, ok := g.Vertices[seed]; !ok {
		spt := NewGraph[S, T]()
		spt.PutVertex(seed, g.Vertices[seed])
		return spt, nil
	}

	dist := map[S]float32{seed: 0}
	prev := make(map[S]S)

	q := make(pq.PriorityQueue[S, float32], 0)
	heap.Init(&q)
	heap.Push(&q, &pq.Item[S, float32]{Value: seed, Priority: 0})

	for q.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		top := heap.Pop(&q).(*pq.Item[S, float32])
		u := top.Value
		// max-heap: stored priority is -dist[u]; un-negate to compare.
		if -top.Priority > dist[u] {
			continue // stale
		}
		for v, w := range g.Edges[u] {
			alt := dist[u] + costFunc(w)
			if d, ok := dist[v]; !ok || alt < d {
				dist[v] = alt
				prev[v] = u
				heap.Push(&q, &pq.Item[S, float32]{Value: v, Priority: -alt})
			}
		}
	}

	spt := NewGraph[S, T]()
	spt.PutVertex(seed, g.Vertices[seed])
	for v, u := range prev {
		spt.PutVertex(v, g.Vertices[v])
		spt.PutEdge(u, v, g.Edges[u][v])
	}
	return spt, nil
}

func (g *Graph[S, T]) Render(key2int func(k S) int, value2string func(v T) string) GraphView {
	var vertices []VertexView
	var edges []EdgeView

	for i, v := range g.Vertices {
		vertices = append(vertices, VertexView{
			ID:    key2int(i),
			Label: value2string(v),
		})
	}

	for from, tos := range g.Edges {
		for to, value := range tos {
			edges = append(edges, EdgeView{
				From:  key2int(from),
				To:    key2int(to),
				Value: value,
			})
		}
	}

	return GraphView{
		Vertices: vertices,
		Edges:    edges,
	}
}
