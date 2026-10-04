package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// dataQueryContext translates a verified logical permission scope once, before
// execution. Core only receives immutable physical index ranges, never Roles,
// credentials, Store pointers, or callbacks into the control plane.
func (s *LanternService) dataQueryContext(ctx context.Context, message proto.Message, admission *security.Admission) (context.Context, error) {
	if admission == nil {
		return ctx, nil
	}
	actions := []security.Action{security.VertexRead, security.Query}
	var vertexPrefix, tailPrefix, headPrefix string
	edgeCollection, needsEdges := false, false
	headModification := false
	switch request := message.(type) {
	case *pb.ScanVerticesRequest:
		actions = []security.Action{security.VertexRead}
		vertexPrefix = request.GetPrefix()
	case *pb.ScanVertexKeysRequest:
		actions = []security.Action{security.VertexRead}
		vertexPrefix = request.GetPrefix()
	case *pb.CountVerticesByPrefixRequest:
		actions = []security.Action{security.VertexRead}
		vertexPrefix = request.GetPrefix()
	case *pb.SearchVerticesRequest:
		vertexPrefix = request.GetPrefix()
	case *pb.ScanEdgesRequest:
		actions = []security.Action{security.VertexRead}
		tailPrefix, headPrefix, edgeCollection, needsEdges = request.GetTailPrefix(), request.GetHeadPrefix(), true, true
	case *pb.DeleteVerticesByPrefixRequest:
		actions, vertexPrefix = []security.Action{security.VertexRead, security.VertexDelete}, request.GetPrefix()
	case *pb.DeleteEdgesByPrefixRequest:
		actions, headModification = nil, true
		tailPrefix, headPrefix, edgeCollection, needsEdges = request.GetTailPrefix(), request.GetHeadPrefix(), true, true
	case *pb.IlluminateRequest:
		needsEdges = true
		// The request's frontier prefix does not exempt the seed from ACLs.
		// Keep the complete traversable view; ranking statistics separately
		// use the existing application corpus.
	case *pb.TopVerticesByDegreeRequest:
		needsEdges = true
		vertexPrefix = request.GetPrefix()
	case *pb.BackupSnapshotRequest:
		needsEdges = true
		actions, vertexPrefix = []security.Action{security.VertexRead, security.Export}, request.GetVertexPrefix()
	default:
		return ctx, nil
	}
	access := admission.Access()
	vertices := access.Scope(actions...)
	if headModification {
		vertices = access.EdgeCandidateScope(security.EdgeDelete)
		// Collection authority is proved from policy alone, before scanning
		// hidden state. Each final Edge keeps its tail/read, head/write cut.
		if access.Scope(security.VertexRead).Within(tailPrefix).Empty() || access.Scope(security.VertexWrite).Within(headPrefix).Empty() {
			return nil, dataPermissionError()
		}
	}
	if vertices.Within(vertexPrefix).Empty() {
		return nil, dataPermissionError()
	}
	edgeActions := append([]security.Action(nil), actions...)
	var edges *security.Scope
	if needsEdges {
		if headModification {
			edgeActions = append(edgeActions, security.EdgeDelete)
		} else {
			edgeActions = append(edgeActions, security.EdgeRead)
		}
		edges = access.EdgeCandidateScope(edgeActions...)
	}
	if edgeCollection && (edges.Within(tailPrefix).Empty() || edges.Within(headPrefix).Empty()) {
		return nil, dataPermissionError()
	}
	all := true
	for _, action := range edgeActions {
		all = all && access.AllowsAll(action)
	}
	if all {
		return ctx, nil
	}
	physicalRange := func(r security.Range) graphcache.KeyRange {
		upper := "data;"
		if r.Upper != "" {
			upper = keyspace.DataPrefix + r.Upper
		}
		return graphcache.KeyRange{Lower: keyspace.DataPrefix + r.Lower, Upper: upper}
	}
	physicalRanges := func(ranges []security.Range) []graphcache.KeyRange {
		result := make([]graphcache.KeyRange, len(ranges))
		for i, r := range ranges {
			result[i] = physicalRange(r)
		}
		return result
	}
	var filters []graphcache.EdgeRangeFilter
	if headModification {
		// Two independent generic filters avoid a read-ranges × write-ranges
		// product. Core has no knowledge of which endpoint owns the Edge.
		whole := physicalRange(security.Range{})
		tails, heads := graphcache.EdgeRangeFilter{}, graphcache.EdgeRangeFilter{}
		for _, r := range access.Scope(security.VertexRead).Ranges() {
			tails.Included = append(tails.Included, graphcache.KeyPairRange{Tail: physicalRange(r), Head: whole})
		}
		for _, r := range access.Scope(security.VertexWrite).Ranges() {
			heads.Included = append(heads.Included, graphcache.KeyPairRange{Tail: whole, Head: physicalRange(r)})
		}
		filters = []graphcache.EdgeRangeFilter{tails, heads}
	}
	view, err := graphcache.NewQueryViewWithEdgeFilters(physicalRanges(vertices.Ranges()), physicalRanges(edges.Ranges()), filters)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid authorized view"))
	}
	return graphcache.WithQueryView(ctx, view), nil
}
