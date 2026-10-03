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
		actions = []security.Action{security.VertexRead, security.EdgeRead, security.EdgeDelete}
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
	if vertices.Within(vertexPrefix).Empty() {
		return nil, dataPermissionError()
	}
	edgeActions := append([]security.Action(nil), actions...)
	var edges *security.Scope
	if needsEdges {
		edgeActions = append(edgeActions, security.EdgeRead)
		edges = access.Scope(edgeActions...)
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
	physical := func(scope *security.Scope) []graphcache.KeyRange {
		ranges := scope.Ranges()
		result := make([]graphcache.KeyRange, len(ranges))
		for i, r := range ranges {
			upper := "data;"
			if r.Upper != "" {
				upper = keyspace.DataPrefix + r.Upper
			}
			result[i] = graphcache.KeyRange{Lower: keyspace.DataPrefix + r.Lower, Upper: upper}
		}
		return result
	}
	view, err := graphcache.NewQueryView(physical(vertices), physical(edges))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid authorized view"))
	}
	return graphcache.WithQueryView(ctx, view), nil
}
