package graphcache

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/collection/pq"
	"github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphstore"
)

var ErrStoredTraversalBudgetExceeded = errors.New("stored graph read budget exhausted")

const (
	// Includes graph headers, source payloads and returned Vertex payloads.
	maxStoredTraversalReadBytes = 64 << 20
	// Folding groups one Edge at a time. Bound source scratch independently
	// of the total request read budget so a hub cannot occupy the whole budget.
	maxStoredTraversalEdgeBytes = 8 << 20
)

// StoredTraversal reads one admitted immutable cut. Its caller owns the view
// and uses one reader per attempt. It does not retain loaded graph rows.
type StoredTraversal[V any] struct {
	rows       *graphstore.Rows[string, V]
	cut        *graphstore.ReadView
	now        time.Time
	scope      *QueryView
	readLeft   int
	byteLeft   int
	budget     PPRWorkBudget
	weight     EdgeWeighting
	topology   graphstore.TopologyStats
	avgLen     float64
	statsReady bool
}

func NewStoredTraversal[V any](rows *graphstore.Rows[string, V], cut *graphstore.ReadView, now time.Time, scope *QueryView, budget PPRWorkBudget) *StoredTraversal[V] {
	budget = budget.normalized()
	return &StoredTraversal[V]{
		rows: rows, cut: cut, now: now, scope: scope,
		readLeft: budget.MaxTouchedEdges, byteLeft: maxStoredTraversalReadBytes, budget: budget,
	}
}

func (r *StoredTraversal[V]) charge(bytes int) error {
	if r.readLeft == 0 || bytes > r.byteLeft {
		return ErrStoredTraversalBudgetExceeded
	}
	r.readLeft--
	r.byteLeft -= bytes
	return nil
}

// PrepareWeighting selects the transform for Neighbor, PageRank and
// Community. Derived counts must be certified on this reader's cut.
func (r *StoredTraversal[V]) PrepareWeighting(weighting EdgeWeighting) error {
	if weighting != WeightingRaw {
		if err := r.loadTopology(); err != nil {
			return err
		}
	}
	r.weight = weighting
	return nil
}

func (r *StoredTraversal[V]) loadTopology() error {
	if r.statsReady {
		return nil
	}
	// The corpus record is a family header and two 64-bit counters.
	if err := r.charge(26); err != nil {
		return err
	}
	stats, ready, err := r.rows.GetTopologyStats(r.cut.Snapshot, r.cut.Generation)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("%w: missing topology statistics", graphstore.ErrInvalidRow)
	}
	if stats.NonemptyTails > math.MaxInt {
		return fmt.Errorf("%w: topology corpus size exceeds the scoring range", graphstore.ErrInvalidRow)
	}
	r.topology = stats
	if stats.NonemptyTails > 0 {
		r.avgLen = float64(stats.Edges) / float64(stats.NonemptyTails)
	}
	r.statsReady = true
	return nil
}

func (r *StoredTraversal[V]) vertex(key string) (V, bool, error) {
	return r.rows.VertexAtWithReadCharge(r.cut.Snapshot, r.cut.Generation, key, r.now, r.charge)
}

func (r *StoredTraversal[V]) edge(ctx context.Context, key graphstore.EdgeKey[string]) (graphstore.FoldedEdge[string], error) {
	left := maxStoredTraversalEdgeBytes
	detail, err := r.rows.EdgeAtWithReadCharge(ctx, r.cut.Snapshot, r.cut.Generation, key, r.now, func(bytes int) error {
		if bytes > left {
			return ErrStoredTraversalBudgetExceeded
		}
		left -= bytes
		return r.charge(bytes)
	})
	return graphstore.FoldedEdge[string]{Key: key, Detail: detail}, err
}

func (r *StoredTraversal[V]) outgoing(ctx context.Context, tail, headPrefix string) iter.Seq2[graphstore.FoldedEdge[string], error] {
	return r.outgoingRanges(ctx, tail, r.scope.Ranges(headPrefix, true, false), nil)
}

func (r *StoredTraversal[V]) topologyDegree(key string) (graphstore.TopologyDegree, error) {
	// The ordered string field can take up to two bytes per input byte
	// plus its terminator. Include the family header and both counters.
	if err := r.charge(28 + 2*len(key)); err != nil {
		return graphstore.TopologyDegree{}, err
	}
	return r.rows.GetTopologyDegree(r.cut.Snapshot, r.cut.Generation, key)
}

func (r *StoredTraversal[V]) scoredOutgoing(tail string, retainedOut int, edges iter.Seq2[graphstore.FoldedEdge[string], error]) iter.Seq2[graphstore.FoldedEdge[string], error] {
	if r.weight == WeightingRaw {
		return edges
	}
	return func(yield func(graphstore.FoldedEdge[string], error) bool) {
		var zero graphstore.FoldedEdge[string]
		docLen := retainedOut
		for edge, err := range edges {
			if err != nil {
				yield(zero, err)
				return
			}
			if r.weight == WeightingBM25 && docLen == 0 {
				degree, err := r.topologyDegree(tail)
				if err == nil && (degree.Out == 0 || degree.Out > r.topology.Edges || degree.Out > math.MaxInt) {
					err = fmt.Errorf("%w: invalid topology out-degree", graphstore.ErrInvalidRow)
				}
				if err != nil {
					yield(zero, err)
					return
				}
				docLen = int(degree.Out)
			}
			degree, err := r.topologyDegree(edge.Key.Head)
			if err == nil && (degree.In == 0 || degree.In > r.topology.NonemptyTails) {
				err = fmt.Errorf("%w: invalid topology in-degree", graphstore.ErrInvalidRow)
			}
			if err != nil {
				yield(zero, err)
				return
			}
			edge.Detail.Weight = scoreGraphEdge(r.weight, edge.Detail.Weight, int(degree.In), docLen, int(r.topology.NonemptyTails), r.avgLen)
			if !yield(edge, nil) {
				return
			}
		}
	}
}

func (r *StoredTraversal[V]) outgoingRanges(ctx context.Context, tail string, ranges iter.Seq[KeyRange], keep func(string) bool) iter.Seq2[graphstore.FoldedEdge[string], error] {
	return func(yield func(graphstore.FoldedEdge[string], error) bool) {
		var zero graphstore.FoldedEdge[string]
		for interval := range ranges {
			for head, err := range r.rows.EdgeHeadsRange(ctx, r.cut.Snapshot, r.cut.Generation, tail, interval.Lower, interval.Upper, nil) {
				if err == nil {
					err = r.charge(len(tail) + len(head))
				}
				if err != nil {
					yield(zero, err)
					return
				}
				if !r.scope.Edge(tail, head) || keep != nil && !keep(head) {
					continue
				}
				edge, err := r.edge(ctx, graphstore.EdgeKey[string]{Tail: tail, Head: head})
				if err != nil {
					yield(zero, err)
					return
				}
				if edge.Detail.Found && !yield(edge, nil) {
					return
				}
			}
		}
	}
}

func (r *StoredTraversal[V]) incoming(ctx context.Context, head string) iter.Seq2[graphstore.FoldedEdge[string], error] {
	return func(yield func(graphstore.FoldedEdge[string], error) bool) {
		for key, err := range r.rows.IncomingEdges(ctx, r.cut.Snapshot, r.cut.Generation, head) {
			if err == nil {
				err = r.charge(len(key.Tail) + len(key.Head))
			}
			if err != nil {
				yield(graphstore.FoldedEdge[string]{}, err)
				return
			}
			if !r.scope.Edge(key.Tail, key.Head) {
				continue
			}
			edge, err := r.edge(ctx, key)
			if err != nil {
				yield(graphstore.FoldedEdge[string]{}, err)
				return
			}
			if edge.Detail.Found && !yield(edge, nil) {
				return
			}
		}
	}
}

// Neighbor performs breadth-first traversal. Each adjacency scan retains
// only the selected edges. Prefix and authorization filters apply before
// weight selection; the seed is exempt only from the frontier prefix.
func (r *StoredTraversal[V]) Neighbor(ctx context.Context, seed, headPrefix string, steps, fanOut int, smallest bool) (*graph.Graph[string, V], map[string]map[string]time.Time, error) {
	g := graph.NewGraph[string, V]()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !r.scope.Vertex(seed) {
		return g, nil, nil
	}
	value, present, err := r.vertex(seed)
	if err != nil {
		return nil, nil, err
	}
	if !present {
		return g, nil, nil
	}
	g.Vertices[seed] = value
	expirations := make(map[string]map[string]time.Time)
	frontier := []string{seed}
	for range steps {
		if len(frontier) == 0 {
			break
		}
		var next []string
		for _, tail := range frontier {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			selected := pq.NewSelection[graphstore.FoldedEdge[string], float32](fanOut, smallest, func(a, b graphstore.FoldedEdge[string]) bool {
				return a.Key.Head < b.Key.Head
			})
			for edge, err := range r.scoredOutgoing(tail, 0, r.outgoing(ctx, tail, headPrefix)) {
				if err != nil {
					return nil, nil, err
				}
				selected.Add(edge, edge.Detail.Weight)
			}
			edges := make(map[string]float32)
			deadlines := make(map[string]time.Time)
			selected.Visit(func(edge graphstore.FoldedEdge[string], weight float32) {
				edges[edge.Key.Head] = weight
				deadlines[edge.Key.Head] = edge.Detail.Expiration
			})
			if len(edges) == 0 {
				continue
			}
			g.Edges[tail], expirations[tail] = edges, deadlines
			for head := range edges {
				if _, seen := g.Vertices[head]; seen {
					continue
				}
				if len(g.Vertices) >= r.budget.MaxPushes {
					return nil, nil, ErrStoredTraversalBudgetExceeded
				}
				value, present, err := r.vertex(head)
				if err != nil {
					return nil, nil, err
				}
				if !present {
					return nil, nil, fmt.Errorf("%w: missing Vertex payload after an admitted Edge", graphstore.ErrInvalidRow)
				}
				g.Vertices[head] = value
				next = append(next, head)
			}
		}
		frontier = next
	}
	return g, expirations, ctx.Err()
}

// PageRank runs forward-push on the pinned graph. Small rows are reused
// for normalization and propagation. Large rows are scanned again as a
// stream so fan-out cannot grow the normalizer's workspace.
func (r *StoredTraversal[V]) PageRank(ctx context.Context, seed, headPrefix string, topN int, alpha, epsilon float64) (*graph.Graph[string, V], error) {
	g := graph.NewGraph[string, V]()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !r.scope.Vertex(seed) {
		return g, nil
	}
	value, present, err := r.vertex(seed)
	if err != nil || !present {
		return g, err
	}
	g.Vertices[seed] = value
	mass, err := r.pageRankMass(ctx, seed, headPrefix, alpha, epsilon)
	if err != nil {
		return nil, err
	}
	selected := pq.NewSelection[string, float32](topN, false, stableKeyLess[string])
	for key, score := range mass {
		if key != seed && score > 0 {
			selected.Add(key, float32(score))
		}
	}
	edges := make(map[string]float32)
	selected.Visit(func(key string, weight float32) { edges[key] = weight })
	for key := range edges {
		value, found, err := r.vertex(key)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("%w: missing Vertex payload for a PageRank result", graphstore.ErrInvalidRow)
		}
		g.Vertices[key] = value
	}
	if len(edges) != 0 {
		g.Edges[seed] = edges
	}
	return g, ctx.Err()
}

func positiveEdges(edges iter.Seq2[graphstore.FoldedEdge[string], error]) iter.Seq2[pprEdge[string], error] {
	return func(yield func(pprEdge[string], error) bool) {
		for edge, err := range edges {
			if err != nil {
				yield(pprEdge[string]{}, err)
				return
			}
			if edge.Detail.Weight <= 0 {
				continue
			}
			if !yield(pprEdge[string]{head: edge.Key.Head, weight: float64(edge.Detail.Weight)}, nil) {
				return
			}
		}
	}
}

func (r *StoredTraversal[V]) pageRankMass(ctx context.Context, seed, headPrefix string, alpha, epsilon float64) (map[string]float64, error) {
	if err := r.loadTopology(); err != nil {
		return nil, err
	}
	return pushPageRank(ctx, seed, alpha, epsilon, r.budget, r.budget.MaxPushes, func(tail string, visit func(uint64) error) (pprRow[string], error) {
		const retainedNeighbors = 32
		var buffer []pprEdge[string]
		var row pprRow[string]
		degree, err := r.topologyDegree(tail)
		if err != nil {
			return row, err
		}
		if degree.Out > r.topology.Edges {
			return row, fmt.Errorf("%w: invalid topology out-degree", graphstore.ErrInvalidRow)
		}
		// RAM charges each retained bucket before liveness, ACL and prefix
		// filters. Use the same cut's retained count so range seeks can skip
		// denied rows without skipping their logical work charge.
		if err := visit(degree.Out); err != nil {
			return row, err
		}
		edges := positiveEdges(r.scoredOutgoing(tail, int(degree.Out), r.outgoing(ctx, tail, headPrefix)))
		for edge, err := range edges {
			if err != nil {
				return pprRow[string]{}, err
			}
			row.weight += edge.weight
			row.degree++
			if len(buffer) < retainedNeighbors {
				buffer = append(buffer, edge)
			}
		}
		if uint64(row.degree) > degree.Out {
			return pprRow[string]{}, fmt.Errorf("%w: invalid topology out-degree", graphstore.ErrInvalidRow)
		}
		if row.degree <= retainedNeighbors {
			row.edges = pprEdgesFromSlice(buffer)
		} else {
			row.edges = edges
		}
		return row, nil
	})
}

// Community extracts a weighted community from the pinned graph.
// The sweep streams adjacency and keeps only weights for possible members.
func (r *StoredTraversal[V]) Community(ctx context.Context, seed, headPrefix string, maxSize int, alpha, epsilon float64) (*graph.Graph[string, V], map[string]map[string]time.Time, error) {
	g := graph.NewGraph[string, V]()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !r.scope.Vertex(seed) {
		return g, nil, nil
	}
	value, present, err := r.vertex(seed)
	if err != nil || !present {
		return g, nil, err
	}
	mass, err := r.pageRankMass(ctx, seed, headPrefix, alpha, epsilon)
	if err != nil {
		return nil, nil, err
	}
	touched := make([]string, 0, len(mass))
	for key, score := range mass {
		if score <= 0 {
			continue
		}
		touched = append(touched, key)
	}
	slices.Sort(touched)
	outgoing := func(tail string) iter.Seq2[pprEdge[string], error] {
		return positiveEdges(r.scoredOutgoing(tail, 0, r.outgoingRanges(ctx, tail, r.communityRanges(tail, headPrefix, seed), nil)))
	}
	degrees := make(map[string]float64, len(touched))
	var volume float64
	for _, tail := range touched {
		var degree float64
		for edge, err := range outgoing(tail) {
			if err != nil {
				return nil, nil, err
			}
			degree += edge.weight
		}
		degrees[tail] = degree
		volume += degree
	}
	selected, err := sweepCommunity(seed, maxSize, touched, mass, degrees, volume, func(tail string) iter.Seq2[pprEdge[string], error] {
		if _, touched := degrees[tail]; !touched {
			return pprEdgesFromSlice[string](nil)
		}
		return outgoing(tail)
	})
	if err != nil {
		return nil, nil, err
	}
	g.Vertices[seed] = value
	for _, member := range selected {
		if member == seed {
			continue
		}
		value, found, err := r.vertex(member)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, fmt.Errorf("%w: missing Vertex payload for a community member", graphstore.ErrInvalidRow)
		}
		g.Vertices[member] = value
	}
	keep := func(head string) bool {
		_, member := g.Vertices[head]
		return member
	}
	expirations := make(map[string]map[string]time.Time)
	for _, tail := range selected {
		var weights map[string]float32
		var deadlines map[string]time.Time
		for edge, err := range r.scoredOutgoing(tail, 0, r.outgoingRanges(ctx, tail, r.communityRanges(tail, headPrefix, seed), keep)) {
			if err != nil {
				return nil, nil, err
			}
			if edge.Detail.Weight <= 0 {
				continue
			}
			if weights == nil {
				weights = make(map[string]float32)
				deadlines = make(map[string]time.Time)
			}
			weights[edge.Key.Head] = edge.Detail.Weight
			deadlines[edge.Key.Head] = edge.Detail.Expiration
		}
		if weights != nil {
			g.Edges[tail], expirations[tail] = weights, deadlines
		}
	}
	return g, expirations, ctx.Err()
}

// The forward-push prefix excludes nonmatching heads. The subsequent sweep
// and induced graph also admit a permitted edge back to the seed.
func (r *StoredTraversal[V]) communityRanges(tail, prefix, seed string) iter.Seq[KeyRange] {
	ranges := r.scope.Ranges(prefix, true, false)
	if strings.HasPrefix(seed, prefix) || !r.scope.Edge(tail, seed) {
		return ranges
	}
	anchor := KeyRange{Lower: seed, Upper: seed + "\x00"}
	return func(yield func(KeyRange) bool) {
		if seed < prefix && !yield(anchor) {
			return
		}
		for interval := range ranges {
			if !yield(interval) {
				return
			}
		}
		if seed >= prefix {
			yield(anchor)
		}
	}
}

// Degree selects the highest live degrees in the admitted prefix. It only
// keeps k ranking entries, including zero-degree candidates. IN and BOTH
// require a complete incoming projection on this cut.
func (r *StoredTraversal[V]) Degree(ctx context.Context, prefix string, k int, direction DegreeDirection, weighted bool) ([]DegreeEntry[string], error) {
	if direction != DegreeOut {
		if err := r.charge(10); err != nil {
			return nil, err
		}
		ready, err := r.rows.HasIncomingEdgeIndex(r.cut.Snapshot, r.cut.Generation)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, fmt.Errorf("%w: missing incoming Edge projection", graphstore.ErrInvalidRow)
		}
	}
	selected := pq.NewSelection[DegreeEntry[string], float64](k, false, func(a, b DegreeEntry[string]) bool { return a.Key < b.Key })
	for interval := range r.scope.Ranges(prefix, false, false) {
		for tail, err := range r.rows.VertexKeysRange(ctx, r.cut.Snapshot, r.cut.Generation, interval.Lower, interval.Upper, nil, false) {
			if err == nil {
				err = r.charge(len(tail))
			}
			if err != nil {
				return nil, err
			}
			present, err := r.rows.HasVertexAtWithReadCharge(r.cut.Snapshot, r.cut.Generation, tail, r.now, r.charge)
			if err != nil {
				return nil, err
			}
			if !present {
				continue
			}
			candidate := DegreeEntry[string]{Key: tail}
			count := func(edges iter.Seq2[graphstore.FoldedEdge[string], error]) error {
				for edge, err := range edges {
					if err != nil {
						return err
					}
					candidate.Degree++
					candidate.WeightedDegree += float64(edge.Detail.Weight)
				}
				return nil
			}
			if direction != DegreeIn {
				if err := count(r.outgoing(ctx, tail, "")); err != nil {
					return nil, err
				}
			}
			if direction != DegreeOut {
				if err := count(r.incoming(ctx, tail)); err != nil {
					return nil, err
				}
			}
			priority := float64(candidate.Degree)
			if weighted {
				priority = candidate.WeightedDegree
			}
			selected.Add(candidate, priority)
		}
	}
	var result []DegreeEntry[string]
	selected.Visit(func(entry DegreeEntry[string], _ float64) { result = append(result, entry) })
	return result, ctx.Err()
}
