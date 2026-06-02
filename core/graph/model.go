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
func (g *Graph[S, T]) ConnectedGraphContext(ctx context.Context, seed S) (*Graph[S, T], error) {
	targets := set.NewSet[S]()
	seen := set.NewSet[S]()
	connected := NewGraph[S, T]()
	connected.PutVertex(seed, g.Vertices[seed])

	targets.Add(seed)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, tail := range targets.Values() {
			if seen.Has(tail) {
				continue
			}

			for head, weight := range g.Edges[tail] {
				connected.PutVertex(head, g.Vertices[head])
				connected.PutEdge(tail, head, weight)
			}
			seen.Add(tail)
		}
		for _, heads := range connected.Edges {
			for head := range heads {
				targets.Add(head)
			}
		}

		if targets.Size() == seen.Size() {
			break
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
	q := make(pq.PriorityQueue[*edge, float32], 0)
	heap.Init(&q)
	seen := set.NewSet[S]()

	mst.PutVertex(seed, connected.Vertices[seed])
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(mst.Vertices) == len(connected.Vertices) {
			break
		}

		for tail := range mst.Vertices {
			if seen.Has(tail) {
				continue
			}

			for head, weight := range connected.Edges[tail] {
				var w float32
				if negate {
					w = weight
				} else {
					w = -weight
				}

				item := &pq.Item[*edge, float32]{
					Value: &edge{
						tail:   tail,
						head:   head,
						weight: weight,
					},
					Priority: w,
				}
				heap.Push(&q, item)
			}
			seen.Add(tail)
		}

		var pickedUp *edge
		for {
			if q.Len() == 0 {
				break
			}
			pickedUp = heap.Pop(&q).(*pq.Item[*edge, float32]).Value
			if _, ok := mst.Vertices[pickedUp.head]; !ok {
				break
			}
		}
		if pickedUp == nil {
			break
		}
		mst.PutVertex(pickedUp.head, connected.Vertices[pickedUp.head])
		mst.PutEdge(pickedUp.tail, pickedUp.head, pickedUp.weight)

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
func (g *Graph[S, T]) ShortestPathTreeContext(ctx context.Context, seed S, costFunc func(x float32) float32) (*Graph[S, T], error) {
	connected, err := g.ConnectedGraphContext(ctx, seed)
	if err != nil {
		return nil, err
	}
	spt := NewGraph[S, T]()
	spt.PutVertex(seed, connected.Vertices[seed])

	type edge struct {
		tail   S
		head   S
		weight float32
	}

	seen := set.NewSet[S]()
	q := make(pq.PriorityQueue[*edge, float32], 0)
	heap.Init(&q)
	pivot := seed
	position := float32(0.0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(spt.Vertices) == len(connected.Vertices) {
			break
		}

		for head, weight := range connected.Edges[pivot] {
			if seen.Has(head) {
				continue
			}
			cost := costFunc(weight)

			heap.Push(&q, &pq.Item[*edge, float32]{
				Value: &edge{
					tail:   pivot,
					head:   head,
					weight: weight,
				},
				Priority: position - cost,
			})
		}

		var pickedUp *pq.Item[*edge, float32]
		for {
			if q.Len() == 0 {
				break
			}
			pickedUp = heap.Pop(&q).(*pq.Item[*edge, float32])
			if _, ok := spt.Vertices[pickedUp.Value.head]; !ok {
				break
			}
		}
		if pickedUp == nil {
			break
		}
		spt.PutVertex(pickedUp.Value.head, connected.Vertices[pickedUp.Value.head])
		spt.PutEdge(pickedUp.Value.tail, pickedUp.Value.head, pickedUp.Value.weight)

		seen.Add(pivot)
		pivot = pickedUp.Value.head
		position = pickedUp.Priority
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
