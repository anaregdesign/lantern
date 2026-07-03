package service

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ScanVertices walks the vertex keyspace in the requested key order (ascending
// by default, descending when Order is SCAN_ORDER_DESC — #898), returning a
// single page of vertices whose keys start with the request prefix. Empty
// prefix scans the whole keyspace.
//
// Pagination: the request limit is clamped to (0, ScanMaxLimit]; a zero or
// negative value falls back to ScanDefaultLimit. The opaque cursor is
// encoded once at the boundary so clients only ever round-trip bytes; see
// cursor.go. NextCursor is empty on the final page. The cursor is order-bound:
// replaying an ascending cursor under a descending scan (or vice versa) is
// rejected with InvalidArgument.
//
// The vertices slice preserves the requested order — clients that need a
// different order must sort downstream.
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
	// The cursor is order-bound (#898): its LastKey means "resume above" for
	// an ascending scan and "resume below" for a descending one, so replaying
	// it under the other order would silently skip or repeat rows. Reject the
	// mismatch instead.
	order := normalizeScanOrder(in.GetOrder())
	if len(in.GetCursor()) > 0 && cursorOrder(cursor.Order) != order {
		if s.onValidationReject != nil {
			s.onValidationReject("order_mismatch")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("scan vertices: cursor order does not match request order"))
	}
	desc := order == scanOrderDesc

	// The cursor seek and page limit are pushed into the index walk (#836):
	// the backend resumes strictly after cursor.LastKey and buffers at most
	// one page, so a resumed page costs O(page), not O(matching set).
	vertices := make([]*pb.Vertex, 0, limit)
	var lastKey string
	more, _ := s.cache.ScanByPrefixPage(ctx, in.GetPrefix(), cursor.LastKey, int(limit), desc, func(_ string, key string, v *pb.Vertex) bool {
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
		resp.NextCursor = encodeCursor(scanCursor{LastKey: lastKey, Order: order})
	}
	s.metrics.OnScan("ScanVertices", len(vertices), time.Since(start))
	return resp, nil
}

// ScanVertexKeys streams just the KEYS of vertices whose key starts with the
// request prefix, in the requested key order (ascending by default, descending
// when Order is SCAN_ORDER_DESC — #898) — the wire-efficient backing for the
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
	order := normalizeScanOrder(in.GetOrder())
	if len(in.GetCursor()) > 0 && cursorOrder(cursor.Order) != order {
		if s.onValidationReject != nil {
			s.onValidationReject("order_mismatch")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("scan vertex keys: cursor order does not match request order"))
	}
	desc := order == scanOrderDesc

	keys := make([]string, 0, limit)
	var lastKey string
	more, _ := s.cache.ScanByPrefixPage(ctx, in.GetPrefix(), cursor.LastKey, int(limit), desc, func(_ string, key string, _ *pb.Vertex) bool {
		keys = append(keys, key)
		lastKey = key
		return true
	})

	resp := &pb.ScanVertexKeysResponse{Keys: keys}
	if more && lastKey != "" {
		resp.NextCursor = encodeKeysCursor(scanKeysCursor{LastKey: lastKey, Order: order})
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

// DeleteEdgesByPrefix removes live edges whose tail key starts with
// TailPrefix AND whose head key starts with HeadPrefix, up to limit
// (clamped to DeleteByPrefixMaxLimit; zero falls back to
// DeleteByPrefixDefaultLimit). At least one prefix must be non-empty — an
// all-empty request is rejected with InvalidArgument so a bulk edge wipe is
// always explicitly scoped. Callers loop on the returned count until it
// reaches zero to drain a large matching set.
//
// When DryRun is set, no deletion happens — the response reports how many
// edges *would* be deleted, bounded by the same effective limit and the same
// live-visibility rules as the real delete.
func (s *LanternService) DeleteEdgesByPrefix(ctx context.Context, in *pb.DeleteEdgesByPrefixRequest) (*pb.DeleteEdgesByPrefixResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	start := time.Now()
	if in.GetTailPrefix() == "" && in.GetHeadPrefix() == "" {
		if s.onValidationReject != nil {
			s.onValidationReject("empty_edge_prefix")
		}
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("delete edges by prefix: at least one of tail_prefix / head_prefix must be non-empty"))
	}
	limit := clampLimit(in.GetLimit(), s.scan.DeleteByPrefixDefaultLimit, s.scan.DeleteByPrefixMaxLimit)

	if in.GetDryRun() {
		// Count would-be deletions by walking the same bounded edge scan
		// without mutating state; the page collector caps the count at the
		// effective limit and applies the identical live-visibility filter.
		n := 0
		s.cache.ScanEdgesByPrefixPage(ctx, in.GetTailPrefix(), in.GetHeadPrefix(), "", "", int(limit),
			func(string, string, string, string, float32, time.Time) bool {
				n++
				return true
			})
		s.metrics.OnScan("DeleteEdgesByPrefix", n, time.Since(start))
		return &pb.DeleteEdgesByPrefixResponse{Deleted: uint64(n)}, nil
	}

	var deleted int
	if s.clock != nil && s.tombstoneTTL > 0 {
		// Share one commit HLC between the edge tombstones and the logged
		// mutation so a peer replaying this delete stamps the same watermark
		// the origin did (see DeleteEdges for the divergence this closes).
		ts := s.clock.Now()
		var err error
		deleted, err = s.cache.DeleteEdgesByPrefixHLC(ctx, in.GetTailPrefix(), in.GetHeadPrefix(), int(limit), ts, s.tombstoneExpiration())
		if err != nil {
			return nil, ctxToConnect(err)
		}
		s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdgesByPrefix{DeleteEdgesByPrefix: in}}, ts)
	} else {
		deleted = s.cache.DeleteEdgesByPrefix(ctx, in.GetTailPrefix(), in.GetHeadPrefix(), int(limit))
		s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdgesByPrefix{DeleteEdgesByPrefix: in}})
	}
	s.metrics.OnScan("DeleteEdgesByPrefix", deleted, time.Since(start))
	return &pb.DeleteEdgesByPrefixResponse{Deleted: uint64(deleted)}, nil
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

// scanOrder* are the compact order codes stored in a scan cursor (#898).
// They are deliberately non-zero so an omitted cursor field (zero) is
// distinguishable and can be normalised to ascending for legacy cursors.
const (
	scanOrderAsc  uint8 = 1
	scanOrderDesc uint8 = 2
)

// normalizeScanOrder maps the request's ScanOrder enum to a compact order
// code, treating SCAN_ORDER_UNSPECIFIED as ascending — the historical
// default — so a client that never sets the field keeps ascending scans.
func normalizeScanOrder(o pb.ScanOrder) uint8 {
	if o == pb.ScanOrder_SCAN_ORDER_DESC {
		return scanOrderDesc
	}
	return scanOrderAsc
}

// cursorOrder normalises a cursor's stored order code, mapping the zero value
// (a pre-#898 cursor that predates the field) to ascending so old cursors are
// treated as the ascending scans that minted them.
func cursorOrder(o uint8) uint8 {
	if o == scanOrderDesc {
		return scanOrderDesc
	}
	return scanOrderAsc
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
