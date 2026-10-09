package service

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// WithDataAuthorization is trusted startup composition. It requires the
// logical/physical mapper and private verified admission on every public RPC.
func (s *LanternService) WithDataAuthorization(now func() time.Time) *LanternService {
	if s.namespaceFormat != keyspace.Version {
		panic("data authorization requires the namespace boundary")
	}
	s.dataAuthorization = true
	s.securityNow = now
	if s.securityNow == nil {
		s.securityNow = time.Now
	}
	return s
}
func (s *LanternService) dataAdmission(ctx context.Context) (*security.Admission, error) {
	if !s.dataAuthorization {
		return nil, nil
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if err := admission.Check(ctx, s.securityNow()); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	return admission, nil
}
func dataPermissionError() error {
	return connect.NewError(connect.CodePermissionDenied, security.ErrPermissionDenied)
}

// authorizeData checks every exact member before graph/receipt lookup or
// mutation. Collection execution receives the admitted range view separately;
// ranking statistics are shared within its existing application-data corpus.
func (s *LanternService) authorizeData(ctx context.Context, message proto.Message) (*security.Admission, error) {
	admission, err := s.dataAdmission(ctx)
	if err != nil || admission == nil {
		return admission, err
	}
	if message == nil || rejectProtoUnknownFields(message.ProtoReflect()) != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid data request"))
	}
	access := admission.Access()
	_, current := admission.CurrentProfile()
	var resources []security.PublicOutputResource
	globalOutput := false
	vertices := func(keys []string, actions ...security.Action) error {
		for _, key := range keys {
			for _, action := range actions {
				if !access.Allows(action, key) {
					return dataPermissionError()
				}
			}
			if current {
				resources = append(resources, security.PublicOutputResource{Kind: security.OutputKey, Key: key, Actions: actions})
			}
		}
		return nil
	}
	edge := func(tail, head string, action security.Action) error {
		if !access.AllowsEdge(action, tail, head) {
			return dataPermissionError()
		}
		if current {
			resources = append(resources, security.PublicOutputResource{Kind: security.OutputEdge, Tail: tail, Head: head, Actions: []security.Action{action}})
		}
		return nil
	}
	global := func(action security.Action) error {
		if !access.AllowsGlobal(action) {
			return dataPermissionError()
		}
		globalOutput = true
		return admission.BindCurrentGlobalOutput(ctx, action)
	}
	switch request := message.(type) {
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
		if current {
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
	if err == nil && current && !globalOutput {
		err = bindCurrentDataOutput(ctx, admission, message, resources)
	}
	return admission, err
}

// Only the precomputed local permission is carried into the storage effect
// planner. No policy compilation, control Store lock, lease check or I/O runs
// under the aggregate graph lock.
func (s *LanternService) denyVertexLifecycle(ctx context.Context, physical string) bool {
	if !s.dataAuthorization {
		return false
	}
	admission, known := security.AdmissionFromContext(ctx)
	logical, err := keyspace.LogicalKey(physical)
	return !known || err != nil || !admission.Access().Allows(security.VertexDelete, logical) || !admission.Access().Allows(security.VertexRead, logical)
}
func (s *LanternService) denyEdgeLifecycle(ctx context.Context, tail, head string) bool {
	if !s.dataAuthorization {
		return false
	}
	admission, known := security.AdmissionFromContext(ctx)
	logicalTail, tailError := keyspace.LogicalKey(tail)
	logicalHead, headError := keyspace.LogicalKey(head)
	return !known || tailError != nil || headError != nil || !admission.Access().AllowsEdge(security.EdgeDelete, logicalTail, logicalHead)
}
