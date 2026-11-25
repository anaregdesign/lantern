package dataruntime

import (
	"context"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// BackupSnapshot streams the live subgraph induced by the requested prefix.
// The database generation and expiration time are pinned before sending.
func (s *Executor) BackupSnapshot(ctx context.Context, request *pb.BackupSnapshotRequest, stream Sender[pb.BackupSnapshotResponse]) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	if db := s.runtime.GraphStore(); db != nil {
		return s.backupStoredGraph(ctx, db, request.GetVertexPrefix(), stream)
	}
	var snap graphcache.GraphSnapshot[string, *pb.Vertex]
	if err := s.withPublicGraphReadRetry(ctx, func() error {
		var err error
		snap, err = s.cache.SnapshotGraphContext(ctx)
		if err != nil {
			return ctxToConnect(err)
		}
		detachSnapshotVertices(snap.Vertices)
		return nil
	}); err != nil {
		return err
	}

	var keep map[string]bool
	if prefix := request.GetVertexPrefix(); prefix != "" {
		keep = make(map[string]bool, len(snap.Vertices))
		for _, v := range snap.Vertices {
			if strings.HasPrefix(v.Key, prefix) {
				keep[v.Key] = true
			}
		}
	}

	for _, v := range snap.Vertices {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		if keep != nil && !keep[v.Key] {
			continue
		}
		vertex := v.Value
		if vertex == nil {
			// Edge-created endpoints have no stored protobuf payload. Their
			// identity and lifetime still belong to this captured graph cut.
			vertex = &pb.Vertex{Key: v.Key, Value: &pb.Vertex_Nil{Nil: true}}
			if !v.Expiration.IsZero() {
				vertex.Expiration = timestamppb.New(v.Expiration)
			}
		}
		if err := stream.Send(&pb.BackupSnapshotResponse{
			Record: &pb.BackupSnapshotResponse_Vertex{Vertex: vertex},
		}); err != nil {
			return err
		}
	}

	for _, e := range snap.Edges {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		if keep != nil && (!keep[e.Tail] || !keep[e.Head]) {
			continue
		}
		if err := stream.Send(&pb.BackupSnapshotResponse{
			Record: &pb.BackupSnapshotResponse_Edge{Edge: foldBackupEdge(e)},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Executor) backupStoredGraph(ctx context.Context, db *graphstore.DB, prefix string, stream Sender[pb.BackupSnapshotResponse]) error {
	rows, err := diskRows()
	if err != nil {
		return graphReadError(err)
	}
	var cut *graphstore.ReadView
	var now time.Time
	defer func() {
		if cut != nil {
			_ = cut.Close()
		}
	}()
	if err := s.withCommittedViewNamed("backup", func() error {
		var err error
		cut, err = db.Read()
		now = time.Now()
		return graphReadError(err)
	}); err != nil {
		return err
	}

	// Retain only a bounded set of live endpoint identities. Nodes outside
	// the set are checked against the same pinned cut when the set fills.
	const endpointBudget = 4 << 20
	remaining := endpointBudget
	live := make(map[string]struct{})
	scope := graphcache.AdmittedQueryView(ctx)
	for interval := range scope.Ranges(prefix, false, false) {
		for row, err := range rows.LiveVertexRange(ctx, cut.Snapshot, cut.Generation, interval.Lower, interval.Upper, nil, false, false, now) {
			if err != nil {
				return graphReadError(err)
			}

			if remaining > 0 {
				remaining -= len(row.Key) + 48
				if remaining > 0 {
					live[row.Key] = struct{}{}
				}
			}
			v := row.Vertex.Value
			if v == nil {
				v = &pb.Vertex{Key: row.Key, Value: &pb.Vertex_Nil{Nil: true}}
				if !row.Vertex.Expiration.IsZero() {
					v.Expiration = timestamppb.New(row.Vertex.Expiration)
				}
			} else if v.GetKey() != row.Key {
				return graphReadError(graphstore.ErrInvalidRow)
			}
			if err := stream.Send(&pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Vertex{Vertex: v}}); err != nil {
				return err
			}
		}
	}
	hasEndpoint := func(key string) (bool, error) {
		if _, ok := live[key]; ok {
			return true, nil
		}
		if remaining > 0 {
			return false, nil
		}
		return rows.HasVertexAt(cut.Snapshot, cut.Generation, key, now)
	}
	for e, err := range rows.FoldedEdges(ctx, cut.Snapshot, cut.Generation, prefix, now) {
		if err != nil {
			return graphReadError(err)
		}
		if !strings.HasPrefix(e.Key.Head, prefix) || !scope.Edge(e.Key.Tail, e.Key.Head) {
			continue
		}
		tail, err := hasEndpoint(e.Key.Tail)
		if err != nil {
			return graphReadError(err)
		}
		if !tail {
			continue
		}
		head, err := hasEndpoint(e.Key.Head)
		if err != nil {
			return graphReadError(err)
		}
		if !head {
			continue
		}
		edge := &pb.Edge{Tail: e.Key.Tail, Head: e.Key.Head, Weight: e.Detail.Weight}
		if !e.Detail.Expiration.IsZero() {
			edge.Expiration = timestamppb.New(e.Detail.Expiration)
		}
		if err := stream.Send(&pb.BackupSnapshotResponse{Record: &pb.BackupSnapshotResponse_Edge{Edge: edge}}); err != nil {
			return err
		}
	}
	return ctxToConnect(ctx.Err())
}

// foldBackupEdge collapses a snapshot edge's per-contribution
// decomposition into the single wire Edge a backup carries: weight summed
// across live contributions, expiration = the furthest-future contribution
// (zero ⇒ permanent). This mirrors the fold in GraphCache.GetEdgeDetail
// (core/graphcache weight.snapshot), so a dump→restore round-trip
// reproduces the same GetEdge result.
func foldBackupEdge(e graphcache.SnapshotEdge[string]) *pb.Edge {
	var weight float32
	var latest time.Time
	for _, c := range e.Contributions {
		weight += c.Weight
		if c.Expiration.After(latest) {
			latest = c.Expiration
		}
	}
	edge := &pb.Edge{Tail: e.Tail, Head: e.Head, Weight: weight}
	if !latest.IsZero() {
		edge.Expiration = timestamppb.New(latest)
	}
	return edge
}
