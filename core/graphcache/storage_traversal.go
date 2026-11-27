package graphcache

import (
	"context"
	"errors"
	"fmt"
	"iter"
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
	rows      *graphstore.Rows[string, V]
	cut       *graphstore.ReadView
	now       time.Time
	scope     *QueryView
	readLeft  int
	byteLeft  int
	vertexMax int
}

func NewStoredTraversal[V any](rows *graphstore.Rows[string, V], cut *graphstore.ReadView, now time.Time, scope *QueryView, budget PPRWorkBudget) *StoredTraversal[V] {
	budget = budget.normalized()
	return &StoredTraversal[V]{
		rows: rows, cut: cut, now: now, scope: scope,
		readLeft: budget.MaxTouchedEdges, byteLeft: maxStoredTraversalReadBytes, vertexMax: budget.MaxPushes,
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

func (r *StoredTraversal[V]) vertex(key string) (V, bool, error) {
	return r.rows.VertexAtWithReadCharge(r.cut.Snapshot, r.cut.Generation, key, r.now, r.charge)
}

func (r *StoredTraversal[V]) outgoing(ctx context.Context, tail, headPrefix string) iter.Seq2[graphstore.FoldedEdge[string], error] {
	return func(yield func(graphstore.FoldedEdge[string], error) bool) {
		var zero graphstore.FoldedEdge[string]
		for interval := range r.scope.Ranges(headPrefix, true, false) {
			for head, err := range r.rows.EdgeHeadsRange(ctx, r.cut.Snapshot, r.cut.Generation, tail, interval.Lower, interval.Upper, nil) {
				if err == nil {
					err = r.charge(len(tail) + len(head))
				}
				if err != nil {
					yield(zero, err)
					return
				}
				if !r.scope.Edge(tail, head) {
					continue
				}
				key := graphstore.EdgeKey[string]{Tail: tail, Head: head}
				left := maxStoredTraversalEdgeBytes
				detail, err := r.rows.EdgeAtWithReadCharge(ctx, r.cut.Snapshot, r.cut.Generation, key, r.now, func(bytes int) error {
					if bytes > left {
						return ErrStoredTraversalBudgetExceeded
					}
					left -= bytes
					return r.charge(bytes)
				})
				if err != nil {
					yield(zero, err)
					return
				}
				if detail.Found && !yield(graphstore.FoldedEdge[string]{Key: key, Detail: detail}, nil) {
					return
				}
			}
		}
	}
}

// Neighbor performs RAW breadth-first traversal. Each adjacency scan retains
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
			for edge, err := range r.outgoing(ctx, tail, headPrefix) {
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
				if len(g.Vertices) >= r.vertexMax {
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

// OutDegree selects the highest live outgoing degrees in the admitted
// prefix. It only keeps k ranking entries, including zero-degree candidates.
func (r *StoredTraversal[V]) OutDegree(ctx context.Context, prefix string, k int, weighted bool) ([]DegreeEntry[string], error) {
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
			for edge, err := range r.outgoing(ctx, tail, "") {
				if err != nil {
					return nil, err
				}
				candidate.Degree++
				candidate.WeightedDegree += float64(edge.Detail.Weight)
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
