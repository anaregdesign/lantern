package service

import (
	"context"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ScanVertices walks the vertex keyspace in lexicographic order, returning a
// single page of vertices whose keys start with the request prefix. Empty
// prefix scans the whole keyspace.
//
// Pagination: the request limit is clamped to (0, ScanMaxLimit]; a zero or
// negative value falls back to ScanDefaultLimit. The opaque cursor is
// encoded once at the boundary so clients only ever round-trip bytes; see
// cursor.go. NextCursor is empty on the final page.
//
// The vertices slice preserves the cache's lexicographic order — clients
// that need a different order must sort downstream.
func (s *LanternService) ScanVertices(ctx context.Context, in *pb.ScanVerticesRequest) (*pb.ScanVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	limit := clampLimit(in.GetLimit(), s.scan.ScanDefaultLimit, s.scan.ScanMaxLimit)

	cursor, err := decodeCursor(in.GetCursor())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%s", err.Error())
	}

	// Pre-allocate one extra slot so the callback can land the (limit+1)-th
	// hit, see it overshoot, and stop without a reallocation. We trim back
	// to limit before returning.
	vertices := make([]*pb.Vertex, 0, limit)
	var lastKey string
	hitLimit := false
	s.cache.ScanByPrefix(ctx, in.GetPrefix(), func(_ string, key string, v *pb.Vertex) bool {
		// Cursor skip: drop everything <= cursor.LastKey. ScanByPrefix
		// walks in lexicographic order, so once we cross the cursor we
		// will never see a <= key again — no need for a sorted-set
		// state machine. This is O(cursor-page-size) overhead per
		// resumed scan; if it ever shows up in profiles, push the seek
		// into the radix walk.
		if cursor.LastKey != "" && key <= cursor.LastKey {
			return true
		}
		if uint32(len(vertices)) >= limit {
			hitLimit = true
			return false
		}
		// Normalise nil-valued vertices the same way GetVertex does so
		// callers see a uniform shape.
		if v == nil {
			vertices = append(vertices, &pb.Vertex{Key: key, Value: &pb.Vertex_Nil{Nil: true}})
		} else {
			vertices = append(vertices, v)
		}
		lastKey = key
		return true
	})

	resp := &pb.ScanVerticesResponse{Vertices: vertices}
	if hitLimit && lastKey != "" {
		resp.NextCursor = encodeCursor(scanCursor{LastKey: lastKey})
	}
	return resp, nil
}

// CountVerticesByPrefix returns the indexed count of vertex keys starting
// with the request prefix. The count is best-effort: it reflects entries in
// the prefix index, which may include vertices whose TTL has expired but
// have not yet been flushed by the GC tick. For most workloads the skew is
// bounded by LANTERN_GC_INTERVAL_SECONDS.
func (s *LanternService) CountVerticesByPrefix(ctx context.Context, in *pb.CountVerticesByPrefixRequest) (*pb.CountVerticesByPrefixResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	n := s.cache.CountByPrefix(in.GetPrefix())
	return &pb.CountVerticesByPrefixResponse{Count: uint64(n)}, nil
}

// DeleteVerticesByPrefix removes vertices matching the request prefix, up
// to limit (clamped to DeleteByPrefixMaxLimit; zero falls back to
// DeleteByPrefixDefaultLimit).
//
// When DryRun is set, no deletion happens — the response reports how many
// vertices *would* be deleted, capped at the same effective limit. The dry
// run uses CountByPrefix and so inherits its bounded-skew semantics.
func (s *LanternService) DeleteVerticesByPrefix(ctx context.Context, in *pb.DeleteVerticesByPrefixRequest) (*pb.DeleteVerticesByPrefixResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	limit := clampLimit(in.GetLimit(), s.scan.DeleteByPrefixDefaultLimit, s.scan.DeleteByPrefixMaxLimit)

	if in.GetDryRun() {
		n := uint64(s.cache.CountByPrefix(in.GetPrefix()))
		if n > uint64(limit) {
			n = uint64(limit)
		}
		return &pb.DeleteVerticesByPrefixResponse{Deleted: n}, nil
	}
	deleted := s.cache.DeleteByPrefix(ctx, in.GetPrefix(), int(limit))
	return &pb.DeleteVerticesByPrefixResponse{Deleted: uint64(deleted)}, nil
}

// clampLimit applies the standard "0 means default, otherwise cap at max"
// rule used by the prefix RPCs. Centralising it keeps the three handlers
// readable and the rule trivially unit-testable.
func clampLimit(requested, def, max uint32) uint32 {
	if requested == 0 {
		requested = def
	}
	if requested > max {
		requested = max
	}
	return requested
}
