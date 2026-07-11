package graph

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/collection/pq"
	"github.com/anaregdesign/lantern/core/collection/set"
)

// ErrInvalidShortestPathCost reports a cost that violates Dijkstra's
// finite, non-negative cost precondition. Callers can use errors.Is to
// distinguish invalid graph data or a cost transform from cancellation and
// other traversal failures.
var ErrInvalidShortestPathCost = errors.New("shortest-path tree requires finite, non-negative costs")

// InvalidShortestPathCostError identifies the edge cost or candidate distance
// that made a shortest-path-tree traversal unsafe. It unwraps to
// ErrInvalidShortestPathCost so callers need not parse its diagnostic text.
type InvalidShortestPathCostError struct {
	Weight   float32
	Cost     float32
	Distance float32
}

func (e *InvalidShortestPathCostError) Error() string {
	return fmt.Sprintf("%v: weight=%g cost=%g candidate_distance=%g", ErrInvalidShortestPathCost, e.Weight, e.Cost, e.Distance)
}

func (e *InvalidShortestPathCostError) Unwrap() error { return ErrInvalidShortestPathCost }

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

// MinimumSpanningTree returns the minimum-cost rooted directed arborescence
// over every vertex reachable from seed. Lantern edges are directed, so this
// is not an undirected Prim MST: every non-seed vertex has exactly one
// selected incoming edge and remains reachable from seed in the result.
func (g *Graph[S, T]) MinimumSpanningTree(seed S) *Graph[S, T] {
	mst, _ := g.spanningTreeContext(context.Background(), seed, false)
	return mst
}

// MaximumSpanningTree returns the maximum-weight rooted directed arborescence
// over every vertex reachable from seed. See [Graph.MinimumSpanningTree] for
// the directed semantics.
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

// spanningTreeContext computes either the minimum (maximize=false) or
// maximum (maximize=true) rooted directed arborescence. It is the shared
// implementation behind the exported MinimumSpanningTree /
// MaximumSpanningTree variants.
//
// Implementation: Chu–Liu/Edmonds. It selects the best incoming edge for
// every non-root vertex, contracts any selected-edge cycles, recursively
// solves the contracted graph, then expands the cycles by replacing precisely
// the incoming edge where the chosen external edge enters. This gives the
// optimal directed tree over the root-reachable subgraph, unlike Prim (whose
// guarantee applies only to undirected graphs). The worst-case running time
// is O(VE); traversal work limits bound this post-processing at the service
// boundary.
func (g *Graph[S, T]) spanningTreeContext(ctx context.Context, seed S, maximize bool) (*Graph[S, T], error) {
	connected, err := g.ConnectedGraphContext(ctx, seed)
	if err != nil {
		return nil, err
	}

	mst := NewGraph[S, T]()
	if len(connected.Vertices) == 0 {
		return mst, nil
	}

	vertices := make([]S, 0, len(connected.Vertices))
	index := make(map[S]int, len(connected.Vertices))
	for vertex := range connected.Vertices {
		index[vertex] = len(vertices)
		vertices = append(vertices, vertex)
	}
	root := index[seed]
	edges := make([]*directedArborescenceEdge, 0)
	for tail, heads := range connected.Edges {
		from := index[tail]
		for head, weight := range heads {
			to := index[head]
			if from == to {
				// A self-loop cannot belong to a rooted arborescence.
				continue
			}
			cost := weight
			if maximize {
				// The solver minimizes. Negating retains the original edge
				// weight for the returned graph while selecting the maximum
				// total-weight arborescence.
				cost = -cost
			}
			edges = append(edges, &directedArborescenceEdge{
				from:           from,
				to:             to,
				cost:           cost,
				originalWeight: weight,
			})
		}
	}

	selected, err := minimumDirectedArborescence(ctx, len(vertices), root, edges)
	if err != nil {
		return nil, err
	}
	for _, vertex := range vertices {
		mst.PutVertex(vertex, connected.Vertices[vertex])
	}
	for _, edge := range selected {
		mst.PutEdge(vertices[edge.from], vertices[edge.to], edge.originalWeight)
	}
	return mst, nil
}

// directedArborescenceEdge is an edge in one Chu–Liu/Edmonds contraction
// level. original points to the edge in the preceding level; top-level edges
// have nil original and retain the source graph's endpoints and weight.
type directedArborescenceEdge struct {
	from, to       int
	cost           float32
	originalWeight float32
	original       *directedArborescenceEdge
}

// minimumDirectedArborescence returns a minimum-cost arborescence rooted at
// root. Its callers only pass a root-reachable graph, so every non-root node
// has an incoming edge at every contraction level.
func minimumDirectedArborescence(ctx context.Context, nodeCount, root int, edges []*directedArborescenceEdge) ([]*directedArborescenceEdge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	incoming := make([]*directedArborescenceEdge, nodeCount)
	for _, edge := range edges {
		if edge.to == root {
			continue
		}
		if current := incoming[edge.to]; current == nil || edge.cost < current.cost {
			incoming[edge.to] = edge
		}
	}

	// Find all cycles formed by the selected incoming edges. component is the
	// contracted node ID; -1 marks a node not yet assigned to a component.
	component := make([]int, nodeCount)
	seen := make([]int, nodeCount)
	for i := range component {
		component[i] = -1
		seen[i] = -1
	}
	componentCount := 0
	for start := 0; start < nodeCount; start++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		vertex := start
		for vertex != root && component[vertex] == -1 && seen[vertex] != start {
			seen[vertex] = start
			vertex = incoming[vertex].from
		}
		if vertex == root || component[vertex] != -1 {
			continue
		}
		for cycleVertex := incoming[vertex].from; cycleVertex != vertex; cycleVertex = incoming[cycleVertex].from {
			component[cycleVertex] = componentCount
		}
		component[vertex] = componentCount
		componentCount++
	}

	if componentCount == 0 {
		selected := make([]*directedArborescenceEdge, 0, nodeCount-1)
		for vertex, edge := range incoming {
			if vertex != root {
				selected = append(selected, edge)
			}
		}
		return selected, nil
	}

	// Give each non-cycle vertex a distinct component after the contracted
	// cycles. The root cannot be in a selected-incoming-edge cycle.
	for vertex := range component {
		if component[vertex] == -1 {
			component[vertex] = componentCount
			componentCount++
		}
	}

	contracted := make([]*directedArborescenceEdge, 0, len(edges))
	for _, edge := range edges {
		if edge.to == root {
			// Root has no selected incoming edge, and no arborescence edge
			// may enter it.
			continue
		}
		from, to := component[edge.from], component[edge.to]
		if from == to {
			continue
		}
		contracted = append(contracted, &directedArborescenceEdge{
			from:     from,
			to:       to,
			cost:     edge.cost - incoming[edge.to].cost,
			original: edge,
		})
	}

	selectedContracted, err := minimumDirectedArborescence(ctx, componentCount, component[root], contracted)
	if err != nil {
		return nil, err
	}
	// Begin with every locally best incoming edge. Each recursively selected
	// contracted edge replaces the incoming edge for its original destination;
	// when it enters a cycle that replacement is exactly the edge that breaks
	// the cycle on expansion.
	for _, edge := range selectedContracted {
		incoming[edge.original.to] = edge.original
	}

	selected := make([]*directedArborescenceEdge, 0, nodeCount-1)
	for vertex, edge := range incoming {
		if vertex != root {
			selected = append(selected, edge)
		}
	}
	return selected, nil
}

// ShortestPathTree
/*
 * ShortestPathTree returns a shortest path tree of the graph from the seed.
 * The costFunc is a function that returns the cost of the edge.
 * Typically, if the weight means like `importance`, the costFunc is a function that returns the 1 / weight.
 * It is calculated by Dijkstra's algorithm, so the costFunc must return a
 * finite, non-negative value. This convenience variant returns nil when that
 * precondition is violated; callers that need the typed error should use
 * ShortestPathTreeContext.
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
// surfaces first. costFunc must return finite, non-negative values (Dijkstra
// precondition). Costs and candidate distances are validated before they enter
// the queue: negative costs can otherwise make a reachable cycle relax
// forever, while NaN/Inf makes priority ordering undefined.
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
			cost := costFunc(w)
			if cost < 0 || math.IsNaN(float64(cost)) || math.IsInf(float64(cost), 0) {
				return nil, &InvalidShortestPathCostError{Weight: w, Cost: cost}
			}
			alt := dist[u] + cost
			if math.IsNaN(float64(alt)) || math.IsInf(float64(alt), 0) {
				return nil, &InvalidShortestPathCostError{Weight: w, Cost: cost, Distance: alt}
			}
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
