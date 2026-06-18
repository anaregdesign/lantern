package service

import (
	"context"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// BackupSnapshot streams a whole-graph, point-in-time backup: every live
// vertex and a folded live edge as a BackupSnapshotResponse. It is the dump half of
// the backup/restore pair (#685); the restore half replays the records
// through PutVertices / PutEdges, so there is no dedicated restore RPC.
//
// Unlike the replication Snapshot RPC, BackupSnapshot has NO replication
// gate — it works on a single node. The whole graph is materialised once
// under the GraphCache lock via SnapshotGraph (#689), so vertices and
// edges share one instant; records are then sent off-lock.
//
// vertex_prefix, when non-empty, scopes the backup to the induced subgraph
// over vertices whose key has the prefix: a vertex is emitted only if it
// matches, and an edge only if BOTH endpoints match, so a restore of the
// partial dump stays referentially closed.
func (s *LanternService) BackupSnapshot(ctx context.Context, request *pb.BackupSnapshotRequest, stream Sender[pb.BackupSnapshotResponse]) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	snap := s.cache.SnapshotGraph()

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
		if err := stream.Send(&pb.BackupSnapshotResponse{
			Record: &pb.BackupSnapshotResponse_Vertex{Vertex: v.Value},
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
