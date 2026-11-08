package dataruntime

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Disclosure depends only on the admitted policy and public request, never on
// whether a Vertex or Edge exists or a conditional mutation would actually succeed.
func blindMutationRequired(access *security.Access, request proto.Message) bool {
	if access == nil {
		return false
	}
	blind := func(tail, head string) bool { return !access.AllowsEdge(security.EdgeRead, tail, head) }
	list := func(edges []*pb.Edge) bool {
		for _, edge := range edges {
			if blind(edge.GetTail(), edge.GetHead()) {
				return true
			}
		}
		return false
	}
	switch req := request.(type) {
	case *pb.PutVertexRequest:
		return !access.Allows(security.VertexRead, req.GetVertex().GetKey())
	case *pb.PutVerticesRequest:
		for _, vertex := range req.GetVertices() {
			if !access.Allows(security.VertexRead, vertex.GetKey()) {
				return true
			}
		}
	case *pb.DeleteVertexRequest:
		return !access.Allows(security.VertexRead, req.GetKey())
	case *pb.DeleteVerticesRequest:
		for _, key := range req.GetKeys() {
			if !access.Allows(security.VertexRead, key) {
				return true
			}
		}
	case *pb.DeleteVerticesByPrefixRequest:
		return !access.Scope(security.VertexDelete).Within(req.GetPrefix()).IsSubsetOf(access.Scope(security.VertexRead))
	case *pb.MutateGraphRequest:
		for _, item := range req.GetItems() {
			if vertex := item.GetPutVertex(); vertex != nil && !access.Allows(security.VertexRead, vertex.GetKey()) {
				return true
			}
			if edge := item.GetAddEdge().GetEdge(); edge != nil && blind(edge.GetTail(), edge.GetHead()) {
				return true
			}
		}
	case *pb.AddEdgeRequest:
		return blind(req.GetEdge().GetTail(), req.GetEdge().GetHead())
	case *pb.AddEdgesRequest:
		return list(req.GetEdges())
	case *pb.PutEdgeRequest:
		return blind(req.GetEdge().GetTail(), req.GetEdge().GetHead())
	case *pb.PutEdgesRequest:
		return list(req.GetEdges())
	case *pb.CreateEdgeRequest:
		return blind(req.GetEdge().GetTail(), req.GetEdge().GetHead())
	case *pb.CreateEdgesRequest:
		return list(req.GetEdges())
	case *pb.DeleteEdgeRequest:
		return blind(req.GetTail(), req.GetHead())
	case *pb.DeleteEdgesRequest:
		for _, edge := range req.GetEdges() {
			if blind(edge.GetTail(), edge.GetHead()) {
				return true
			}
		}
	case *pb.DeleteEdgeContributionRequest:
		return blind(req.GetTail(), req.GetHead())
	case *pb.DeleteEdgeContributionsRequest:
		for _, edge := range req.GetContributions() {
			if blind(edge.GetTail(), edge.GetHead()) {
				return true
			}
		}
	case *pb.DeleteEdgesByPrefixRequest:
		tailAction, headAction := access.EdgeEndpointActions()
		read := access.Scope(security.VertexRead)
		return !access.Scope(tailAction).Within(req.GetTailPrefix()).IsSubsetOf(read) ||
			!access.Scope(headAction).Within(req.GetHeadPrefix()).IsSubsetOf(read)
	}
	return false
}

// These errors describe private stored receipt/idempotency state. A blind
// request is handled without reexecution or disclosure. Input/authority/WAL
// failures are deliberately not included in this list.
func blindReceiptDisposition(err error) bool {
	return errors.Is(err, graphcache.ErrEdgeEndpointNotLive) ||
		errors.Is(err, mutationreceipt.ErrIntentConflict) ||
		errors.Is(err, mutationreceipt.ErrContributionConflict) ||
		errors.Is(err, mutationreceipt.ErrPartialEnvelope) ||
		errors.Is(err, mutationreceipt.ErrNoLongerProvable) ||
		errors.Is(err, mutationreceipt.ErrNotFresh)
}

// Reset a detached public response instead of touching the authoritative
// original receipt result. No existence/weight/count/unknown field survives.
func mutationAcceptanceResponse(response proto.Message) (proto.Message, error) {
	if response == nil || !response.ProtoReflect().IsValid() {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid mutation acceptance response"))
	}
	field := response.ProtoReflect().Descriptor().Fields().ByName("acceptance")
	if field == nil || field.Kind() != protoreflect.MessageKind || field.Message().FullName() != "graph.v1.MutationAcceptance" {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unsupported mutation acceptance response"))
	}
	result := proto.Clone(response)
	proto.Reset(result)
	acceptance := &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}
	result.ProtoReflect().Set(field, protoreflect.ValueOfMessage(acceptance.ProtoReflect()))
	return result, nil
}
