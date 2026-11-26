package dataruntime

import (
	"context"
	"errors"
	"iter"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/graphstorage"
	"github.com/anaregdesign/tarp"
)

var diskRows = sync.OnceValues(graphstorage.Rows)

type graphRead struct {
	backend Backend
	cut     *graphstore.ReadView
	rows    *graphstore.Rows[string, *pb.Vertex]
	now     time.Time
}

func graphReadError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ctxToConnect(err)
	case errors.Is(err, graphstore.ErrInvalidRow), errors.Is(err, graphstore.ErrInvalidManifest), errors.Is(err, tarp.ErrCorrupt):
		return connect.NewError(connect.CodeDataLoss, errors.New("invalid stored graph"))
	default:
		return connect.NewError(connect.CodeUnavailable, errors.New("graph storage unavailable"))
	}
}

// Each attempt pins one generation behind the existing publication and
// Snapshot-install checks. All liveness decisions share its operation time.
func (s *Executor) withGraphRows(ctx context.Context, capture func(graphRead) error) error {
	return s.withPublicGraphReadRetry(ctx, func() error {
		db := s.runtime.GraphStore()
		if db == nil {
			return capture(graphRead{backend: s.cache})
		}
		rows, err := diskRows()
		if err != nil {
			return graphReadError(err)
		}
		cut, err := db.Read()
		if err != nil {
			return graphReadError(err)
		}
		defer cut.Close()
		return capture(graphRead{cut: cut, rows: rows, now: time.Now()})
	})
}

func (r graphRead) vertex(key string) (*pb.Vertex, bool, error) {
	if r.cut == nil {
		v, ok := r.backend.GetVertex(key)
		return v, ok, nil
	}
	v, ok, err := r.rows.VertexAt(r.cut.Snapshot, r.cut.Generation, key, r.now)
	return v, ok, graphReadError(err)
}

func (r graphRead) edges(ctx context.Context, keys []graphcache.EdgeKey[string]) ([]graphcache.EdgeDetail, error) {
	if r.cut == nil {
		return r.backend.GetEdgeDetails(keys), nil
	}
	result := make([]graphcache.EdgeDetail, len(keys))
	seen := make(map[graphcache.EdgeKey[string]]graphcache.EdgeDetail, len(keys))
	for i, k := range keys {
		if err := ctx.Err(); err != nil {
			return nil, ctxToConnect(err)
		}
		d, exists := seen[k]
		if !exists {
			v, err := r.rows.EdgeAt(ctx, r.cut.Snapshot, r.cut.Generation, graphstore.EdgeKey[string]{Tail: k.Tail, Head: k.Head}, r.now)
			if err != nil {
				return nil, graphReadError(err)
			}
			d = graphcache.EdgeDetail{Weight: v.Weight, Expiration: v.Expiration, Found: v.Found}
			seen[k] = d
		}
		result[i] = d
	}
	return result, nil
}

func (r graphRead) vertexPage(ctx context.Context, prefix, after string, limit int, reverse, keysOnly bool, emit func(string, *pb.Vertex)) (bool, error) {
	if r.cut == nil {
		more, _ := r.backend.ScanByPrefixPage(ctx, prefix, after, limit, reverse, func(_ string, key string, v *pb.Vertex) bool { emit(key, v); return true })
		return more, ctxToConnect(ctx.Err())
	}
	scope := graphcache.AdmittedQueryView(ctx)
	var cursor *string
	if after != "" {
		cursor = &after
	}
	n := 0
	for interval := range scope.Ranges(prefix, false, reverse) {
		for row, err := range r.rows.LiveVertexRange(ctx, r.cut.Snapshot, r.cut.Generation, interval.Lower, interval.Upper, cursor, reverse, keysOnly, r.now) {
			if err != nil {
				return false, graphReadError(err)
			}
			if n == limit {
				return true, nil
			}
			emit(row.Key, row.Vertex.Value)
			n++
		}
	}
	return false, ctxToConnect(ctx.Err())
}

func (r graphRead) edgePage(ctx context.Context, tailPrefix, headPrefix, afterTail, afterHead string, limit int, emit func(string, string, float32, time.Time)) (bool, error) {
	if r.cut == nil {
		more, _ := r.backend.ScanEdgesByPrefixPage(ctx, tailPrefix, headPrefix, afterTail, afterHead, limit, func(_ string, tail string, _ string, head string, w float32, exp time.Time) bool {
			emit(tail, head, w, exp)
			return true
		})
		return more, ctxToConnect(ctx.Err())
	}
	n := 0
	for e, err := range r.scopedEdges(ctx, tailPrefix, headPrefix, afterTail, afterHead) {
		if err != nil {
			return false, graphReadError(err)
		}
		if n == limit {
			return true, nil
		}
		emit(e.Key.Tail, e.Key.Head, e.Detail.Weight, e.Detail.Expiration)
		n++
	}
	return false, ctxToConnect(ctx.Err())
}

func (r graphRead) scopedEdges(ctx context.Context, tailPrefix, headPrefix, afterTail, afterHead string) iter.Seq2[graphstore.FoldedEdge[string], error] {
	return func(yield func(graphstore.FoldedEdge[string], error) bool) {
		scope := graphcache.AdmittedQueryView(ctx)
		for tails := range scope.Ranges(tailPrefix, true, false) {
			for tail, err := range r.rows.EdgeTailsRange(ctx, r.cut.Snapshot, r.cut.Generation, max(afterTail, tails.Lower), tails.Upper) {
				if err != nil {
					yield(graphstore.FoldedEdge[string]{}, err)
					return
				}
				if !scope.Vertex(tail) {
					continue
				}
				var cursor *string
				if tail == afterTail && afterHead != "" {
					cursor = &afterHead
				}
				for heads := range scope.Ranges(headPrefix, true, false) {
					for head, err := range r.rows.EdgeHeadsRange(ctx, r.cut.Snapshot, r.cut.Generation, tail, heads.Lower, heads.Upper, cursor) {
						if err != nil {
							yield(graphstore.FoldedEdge[string]{}, err)
							return
						}
						if !scope.Edge(tail, head) {
							continue
						}
						d, err := r.rows.EdgeAt(ctx, r.cut.Snapshot, r.cut.Generation, graphstore.EdgeKey[string]{Tail: tail, Head: head}, r.now)
						if err != nil {
							yield(graphstore.FoldedEdge[string]{}, err)
							return
						}
						if !d.Found {
							continue
						}
						if !yield(graphstore.FoldedEdge[string]{Key: graphstore.EdgeKey[string]{Tail: tail, Head: head}, Detail: d}, nil) {
							return
						}
					}
				}
			}
		}

	}
}

func (s *Executor) storageFault() error {
	if s.runtime != nil && s.runtime.tarp != nil {
		if err := s.runtime.tarp.replayReadFault.Load(); err != nil {
			return *err
		}
	}
	if s.runtime != nil && s.runtime.graph != nil {
		return s.runtime.graph.StorageError()
	}
	return nil
}

func (s *Executor) captureGraphStorage(capture func() error) error {
	if err := s.storageFault(); err != nil {
		return graphReadError(err)
	}
	err := capture()
	if fault := s.storageFault(); fault != nil {
		return graphReadError(fault)
	}
	return err
}
