package dataruntime

import (
	"errors"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

type dataPermissionPlan struct {
	resources []security.PublicOutputResource
	global    security.Action
}

func planDataPermissions(access *security.Access, message proto.Message) (dataPermissionPlan, error) {
	if access == nil {
		return dataPermissionPlan{}, dataPermissionError()
	}
	if message == nil || rejectProtoUnknownFields(message.ProtoReflect()) != nil {
		return dataPermissionPlan{}, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid data request"))
	}
	var err error
	var resources []security.PublicOutputResource
	var globalOutput security.Action
	vertices := func(keys []string, actions ...security.Action) error {
		for _, key := range keys {
			for _, action := range actions {
				if !access.Allows(action, key) {
					return dataPermissionError()
				}
			}
			{
				resources = append(resources, security.PublicOutputResource{Kind: security.OutputKey, Key: key, Actions: actions})
			}
		}
		return nil
	}
	edge := func(tail, head string, action security.Action) error {
		if !access.AllowsEdge(action, tail, head) {
			return dataPermissionError()
		}
		{
			resources = append(resources, security.PublicOutputResource{Kind: security.OutputEdge, Tail: tail, Head: head, Actions: []security.Action{action}})
		}
		return nil
	}
	global := func(action security.Action) error {
		if !access.AllowsGlobal(action) {
			return dataPermissionError()
		}
		globalOutput = action
		return nil
	}
	switch request := message.(type) {
	case *pb.MutateGraphRequest:
		for _, item := range request.GetItems() {
			if vertex := item.GetPutVertex(); vertex != nil {
				err = vertices([]string{vertex.GetKey()}, security.VertexRead, security.VertexWrite)
			} else if add := item.GetAddEdge(); add != nil {
				err = edge(add.GetEdge().GetTail(), add.GetEdge().GetHead(), security.EdgeAdd)
			} else {
				err = invalidReceiptRequest(errGraphMutation)
			}
			if err != nil {
				break
			}
		}
	case *pb.GetVertexRequest:
		err = vertices([]string{request.GetKey()}, security.VertexRead)
	case *pb.GetVerticesRequest:
		err = vertices(request.GetKeys(), security.VertexRead)
	case *pb.PutVertexRequest:
		err = vertices([]string{request.GetVertex().GetKey()}, security.VertexRead, security.VertexWrite)
	case *pb.PutVerticesRequest:
		for _, vertex := range request.GetVertices() {
			if err = vertices([]string{vertex.GetKey()}, security.VertexRead, security.VertexWrite); err != nil {
				break
			}
		}
	case *pb.DeleteVertexRequest:
		err = vertices([]string{request.GetKey()}, security.VertexRead, security.VertexDelete)
	case *pb.DeleteVerticesRequest:
		err = vertices(request.GetKeys(), security.VertexRead, security.VertexDelete)
	case *pb.GetEdgeRequest:
		err = edge(request.GetTail(), request.GetHead(), security.EdgeRead)
	case *pb.GetEdgesRequest:
		for _, item := range request.GetEdges() {
			if err = edge(item.GetTail(), item.GetHead(), security.EdgeRead); err != nil {
				break
			}
		}
	case *pb.CreateEdgeRequest:
		err = edge(request.GetEdge().GetTail(), request.GetEdge().GetHead(), security.EdgeCreate)
	case *pb.CreateEdgesRequest:
		for _, item := range request.GetEdges() {
			if err = edge(item.GetTail(), item.GetHead(), security.EdgeCreate); err != nil {
				break
			}
		}
	case *pb.AddEdgeRequest:
		err = edge(request.GetEdge().GetTail(), request.GetEdge().GetHead(), security.EdgeAdd)
	case *pb.AddEdgesRequest:
		for _, item := range request.GetEdges() {
			if err = edge(item.GetTail(), item.GetHead(), security.EdgeAdd); err != nil {
				break
			}
		}
	case *pb.PutEdgeRequest:
		err = edge(request.GetEdge().GetTail(), request.GetEdge().GetHead(), security.EdgeWrite)
	case *pb.PutEdgesRequest:
		for _, item := range request.GetEdges() {
			if err = edge(item.GetTail(), item.GetHead(), security.EdgeWrite); err != nil {
				break
			}
		}
	case *pb.DeleteEdgeRequest:
		err = edge(request.GetTail(), request.GetHead(), security.EdgeDelete)
	case *pb.DeleteEdgesRequest:
		for _, item := range request.GetEdges() {
			if err = edge(item.GetTail(), item.GetHead(), security.EdgeDelete); err != nil {
				break
			}
		}
	case *pb.DeleteEdgeContributionRequest:
		err = edge(request.GetTail(), request.GetHead(), security.EdgeDelete)
	case *pb.DeleteEdgeContributionsRequest:
		for _, item := range request.GetContributions() {
			if err = edge(item.GetTail(), item.GetHead(), security.EdgeDelete); err != nil {
				break
			}
		}
	case *pb.GetServerStatusRequest, *pb.GetReplicationStatusRequest:
		err = global(security.OperationsRead)
	case *pb.GetReceiptCapabilityRequest, *pb.GetReceiptStatusRequest, *pb.GetReceiptStatusesRequest:
		if access.Scope(security.ReceiptRead, security.VertexRead).Empty() && access.EdgeCandidateScope(security.ReceiptRead, security.VertexRead).Empty() {
			err = dataPermissionError()
		}
		{
			if !access.Scope(security.ReceiptRead, security.VertexRead).Empty() {
				resources = append(resources, security.PublicOutputResource{Kind: security.OutputVertexCollection, Actions: []security.Action{security.ReceiptRead, security.VertexRead}})
			}
			if !access.EdgeCandidateScope(security.ReceiptRead, security.VertexRead).Empty() {
				resources = append(resources, security.PublicOutputResource{Kind: security.OutputEdgeCollection, Actions: []security.Action{security.ReceiptRead, security.VertexRead}})
			}
		}
	case *pb.ScanVerticesRequest, *pb.ScanVertexKeysRequest, *pb.CountVerticesByPrefixRequest:
		// The immutable range view rejects empty scopes before index lookup.
	case *pb.SearchVerticesRequest:
		// Matching candidates are constrained before top-k; shared ranking
		// statistics do not require a whole-domain data grant.
	case *pb.ScanEdgesRequest:
		// Both endpoint ranges are checked before collecting a page.
	case *pb.DeleteVerticesByPrefixRequest:
		// Exact authorized victims are selected atomically inside the view.
	case *pb.DeleteEdgesByPrefixRequest:
		// Dry-run and commit share the same authorized edge selector.
	case *pb.IlluminateRequest:
		err = vertices([]string{request.GetSeed()}, security.VertexRead, security.Query)
	case *pb.TopVerticesByDegreeRequest:
		// Degree is computed inside the authorized induced graph.
	case *pb.BackupSnapshotRequest:
		// Export/read grants are compiled by dataQueryContext. Interactive
		// recent auth belongs to security changes, not Role-scoped automation.

	default:
		err = connect.NewError(connect.CodeUnimplemented, errors.New("data authorization boundary missing"))
	}
	if receiptRequest, known := message.(interface {
		GetReceiptContext() *pb.MutationReceiptContext
	}); err == nil && known && receiptRequest.GetReceiptContext() != nil {
		err = authorizeReceiptRequest(access, message)
		for i := range resources {
			resources[i].Actions = append(append([]security.Action(nil), resources[i].Actions...), security.ReceiptRead)
		}
	}
	return dataPermissionPlan{resources, globalOutput}, err
}
