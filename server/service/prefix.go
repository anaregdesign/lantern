package service

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
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
		return nil, ctxToConnect(err)
	}
	start := time.Now()
	limit := clampLimit(in.GetLimit(), s.scan.ScanDefaultLimit, s.scan.ScanMaxLimit)

	cursor, err := decodeCursor(in.GetCursor())
	if err != nil {
		if s.onValidationReject != nil {
			s.onValidationReject("bad_cursor")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// The cursor seek and page limit are pushed into the index walk (#836):
	// the backend resumes strictly after cursor.LastKey and buffers at most
	// one page, so a resumed page costs O(page), not O(matching set).
	vertices := make([]*pb.Vertex, 0, limit)
	var lastKey string
	more, _ := s.cache.ScanByPrefixPage(ctx, in.GetPrefix(), cursor.LastKey, int(limit), func(_ string, key string, v *pb.Vertex) bool {
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
	if more && lastKey != "" {
		resp.NextCursor = encodeCursor(scanCursor{LastKey: lastKey})
	}
	s.metrics.OnScan("ScanVertices", len(vertices), time.Since(start))
	return resp, nil
}

// ScanVertexKeys streams just the KEYS of vertices whose key starts with the
// request prefix, in lexicographic order — the wire-efficient backing for the
// `keys` CLI verb (#674). It differs from ScanVertices in two ways:
//
//   - a non-empty prefix is REQUIRED (empty prefix → InvalidArgument), so
//     there is no whole-keyspace dump;
//   - it carries its OWN opaque cursor kind (decodeKeysCursor rejects cursors
//     minted by ScanVertices / ScanEdges with InvalidArgument).
//
// Limit clamping reuses the ScanVertices knobs (ScanDefaultLimit /
// ScanMaxLimit). Only the key string is collected, so vertex values are never
// materialised onto the wire.
func (s *LanternService) ScanVertexKeys(ctx context.Context, in *pb.ScanVertexKeysRequest) (*pb.ScanVertexKeysResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if in.GetPrefix() == "" {
		if s.onValidationReject != nil {
			s.onValidationReject("empty_prefix")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("prefix is required"))
	}
	start := time.Now()
	limit := clampLimit(in.GetLimit(), s.scan.ScanDefaultLimit, s.scan.ScanMaxLimit)

	cursor, err := decodeKeysCursor(in.GetCursor())
	if err != nil {
		if s.onValidationReject != nil {
			s.onValidationReject("bad_cursor")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	keys := make([]string, 0, limit)
	var lastKey string
	more, _ := s.cache.ScanByPrefixPage(ctx, in.GetPrefix(), cursor.LastKey, int(limit), func(_ string, key string, _ *pb.Vertex) bool {
		keys = append(keys, key)
		lastKey = key
		return true
	})

	resp := &pb.ScanVertexKeysResponse{Keys: keys}
	if more && lastKey != "" {
		resp.NextCursor = encodeKeysCursor(scanKeysCursor{LastKey: lastKey})
	}
	s.metrics.OnScan("ScanVertexKeys", len(keys), time.Since(start))
	return resp, nil
}

// CountVerticesByPrefix returns the indexed count of vertex keys starting
// with the request prefix. The count is best-effort: it reflects entries in
// the prefix index, which may include vertices whose TTL has expired but
// have not yet been flushed by the GC tick. For most workloads the skew is
// bounded by LANTERN_GC_INTERVAL_SECONDS.
func (s *LanternService) CountVerticesByPrefix(ctx context.Context, in *pb.CountVerticesByPrefixRequest) (*pb.CountVerticesByPrefixResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	start := time.Now()
	n := s.cache.CountByPrefix(in.GetPrefix())
	s.metrics.OnScan("CountVerticesByPrefix", n, time.Since(start))
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
		return nil, ctxToConnect(err)
	}
	start := time.Now()
	limit := clampLimit(in.GetLimit(), s.scan.DeleteByPrefixDefaultLimit, s.scan.DeleteByPrefixMaxLimit)

	if in.GetDryRun() {
		n := uint64(s.cache.CountByPrefix(in.GetPrefix()))
		if n > uint64(limit) {
			n = uint64(limit)
		}
		s.metrics.OnScan("DeleteVerticesByPrefix", int(n), time.Since(start))
		return &pb.DeleteVerticesByPrefixResponse{Deleted: n}, nil
	}
	deleted := 0
	if s.clock != nil && s.tombstoneTTL > 0 {
		var err error
		deleted, err = s.cache.DeleteByPrefixHLC(ctx, in.GetPrefix(), limit, s.clock.Now(), s.tombstoneExpiration())
		if err != nil {
			return nil, ctxToConnect(err)
		}
	} else {
		deleted = s.cache.DeleteByPrefix(ctx, in.GetPrefix(), int(limit))
	}
	s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_DeleteVerticesByPrefix{DeleteVerticesByPrefix: in}})
	s.metrics.OnScan("DeleteVerticesByPrefix", deleted, time.Since(start))
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

// ScanEdges walks the edge keyspace in ascending (tail, head) order,
// returning a single page of edges whose tail key starts with TailPrefix
// AND whose head key starts with HeadPrefix. Either prefix may be empty.
//
// Pagination shares the ScanVertices limits (clamped to (0,
// ScanMaxLimit]; zero falls back to ScanDefaultLimit). The opaque cursor
// encodes (LastTail, LastHead) — wire-incompatible with the vertex
// cursor on purpose so the two pagination streams cannot be swapped.
//
// Implementation note: v1 walks the vertex-side prefix index for the
// tail dimension and applies HeadPrefix as a post-filter. AddEdge /
// PutEdge auto-create both endpoints as vertices, so the radix already
// covers every live tail; no parallel edge index exists today.
func (s *LanternService) ScanEdges(ctx context.Context, in *pb.ScanEdgesRequest) (*pb.ScanEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	start := time.Now()
	limit := clampLimit(in.GetLimit(), s.scan.ScanDefaultLimit, s.scan.ScanMaxLimit)

	cursor, err := decodeEdgesCursor(in.GetCursor())
	if err != nil {
		if s.onValidationReject != nil {
			s.onValidationReject("bad_cursor")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Cursor resume and page limit are pushed into the index walk (#836):
	// the backend seeks the tail radix to the cursor tail and, within it,
	// past the cursor head, buffering at most one page.
	edges := make([]*pb.Edge, 0, limit)
	var lastTail, lastHead string
	more, _ := s.cache.ScanEdgesByPrefixPage(ctx, in.GetTailPrefix(), in.GetHeadPrefix(), cursor.LastTail, cursor.LastHead, int(limit),
		func(_ string, tail string, _ string, head string, weight float32, exp time.Time) bool {
			edge := &pb.Edge{Tail: tail, Head: head, Weight: weight}
			if !exp.IsZero() {
				edge.Expiration = timestamppb.New(exp)
			}
			edges = append(edges, edge)
			lastTail, lastHead = tail, head
			return true
		})

	resp := &pb.ScanEdgesResponse{Edges: edges}
	if more && (lastTail != "" || lastHead != "") {
		resp.NextCursor = encodeEdgesCursor(scanEdgesCursor{LastTail: lastTail, LastHead: lastHead})
	}
	s.metrics.OnScan("ScanEdges", len(edges), time.Since(start))
	return resp, nil
}
