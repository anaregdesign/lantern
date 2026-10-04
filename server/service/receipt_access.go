package service

import (
	"context"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// Receipt handling uses original provenance and today's captured Role cut.
// This check performs no control Store access or I/O while a receipt/graph
// transaction is open; protected publication separately fences the admission.
func allowsReceiptMutationResource(access *security.Access, receipt mutationreceipt.Receipt) bool {
	resource := receipt.Resource
	allow := func(action security.Action) bool {
		if resource == (mutationreceipt.ResourceIdentity{}) {
			return access.AllowsAll(action)
		}
		if resource.Head != "" && action != security.VertexRead && action != security.VertexWrite && action != security.VertexDelete {
			return access.AllowsEdgeAction(action, resource.Key, resource.Head)
		}
		return access.Allows(action, resource.Key) && (resource.Head == "" || access.Allows(action, resource.Head))
	}
	if !allow(security.ReceiptRead) {
		return false
	}
	switch receipt.Kind {
	case mutationreceipt.PutVertex:
		return resource.Head == "" && allow(security.VertexRead) && allow(security.VertexWrite) && ((!receipt.LifecycleReduction && resource != (mutationreceipt.ResourceIdentity{})) || allow(security.VertexDelete))
	case mutationreceipt.DeleteVertex:
		return resource.Head == "" && allow(security.VertexRead) && allow(security.VertexDelete)
	case mutationreceipt.CreateEdge:
		return resource.Head != "" && allow(security.EdgeCreate)
	case mutationreceipt.AddEdge, mutationreceipt.PutEdge:
		mutation := security.EdgeAdd
		if receipt.Kind == mutationreceipt.PutEdge {
			mutation = security.EdgeWrite
		}
		return (resource.Head != "" || resource == (mutationreceipt.ResourceIdentity{})) && allow(mutation)
	case mutationreceipt.DeleteEdge, mutationreceipt.DeleteEdgeContribution:
		return (resource.Head != "" || resource == (mutationreceipt.ResourceIdentity{})) && allow(security.EdgeDelete)
	default:
		return false
	}
}

// Detailed original effects additionally need ordinary data disclosure. A
// handling acknowledgement must not expose the original receipt metadata.
func allowsReceiptResource(access *security.Access, receipt mutationreceipt.Receipt) bool {
	if !allowsReceiptMutationResource(access, receipt) {
		return false
	}
	resource := receipt.Resource
	if resource == (mutationreceipt.ResourceIdentity{}) {
		return access.AllowsAll(security.VertexRead)
	}
	return access.Allows(security.VertexRead, resource.Key) && (resource.Head == "" || access.Allows(security.VertexRead, resource.Head))
}

func wholeReceiptAbsence(access *security.Access) bool {
	return access.AllowsAll(security.ReceiptRead) && access.AllowsAll(security.VertexRead) && access.AllowsAll(security.EdgeRead)
}

// Public mutation ingress is checked before resource lookup. Original replay
// rows subsequently add the stored lifecycle-effect requirement.
func authorizeReceiptRequest(access *security.Access, message proto.Message) error {
	check := func(key, head string) error {
		if head != "" {
			if !access.AllowsEdgeAction(security.ReceiptRead, key, head) {
				return dataPermissionError()
			}
			return nil
		}
		if !access.Allows(security.ReceiptRead, key) {
			return dataPermissionError()
		}
		return nil
	}
	switch req := message.(type) {
	case *pb.PutVertexRequest:
		return check(req.GetVertex().GetKey(), "")
	case *pb.PutVerticesRequest:
		for _, v := range req.GetVertices() {
			if err := check(v.GetKey(), ""); err != nil {
				return err
			}
		}
	case *pb.DeleteVertexRequest:
		return check(req.GetKey(), "")
	case *pb.DeleteVerticesRequest:
		for _, key := range req.GetKeys() {
			if err := check(key, ""); err != nil {
				return err
			}
		}
	case *pb.CreateEdgeRequest:
		return check(req.GetEdge().GetTail(), req.GetEdge().GetHead())
	case *pb.CreateEdgesRequest:
		for _, edge := range req.GetEdges() {
			if err := check(edge.GetTail(), edge.GetHead()); err != nil {
				return err
			}
		}
	case *pb.AddEdgeRequest:
		return check(req.GetEdge().GetTail(), req.GetEdge().GetHead())
	case *pb.AddEdgesRequest:
		for _, edge := range req.GetEdges() {
			if err := check(edge.GetTail(), edge.GetHead()); err != nil {
				return err
			}
		}
	case *pb.DeleteEdgeRequest:
		return check(req.GetTail(), req.GetHead())
	case *pb.DeleteEdgesRequest:
		for _, edge := range req.GetEdges() {
			if err := check(edge.GetTail(), edge.GetHead()); err != nil {
				return err
			}
		}
	case *pb.DeleteEdgeContributionRequest:
		return check(req.GetTail(), req.GetHead())
	case *pb.DeleteEdgeContributionsRequest:
		for _, edge := range req.GetContributions() {
			if err := check(edge.GetTail(), edge.GetHead()); err != nil {
				return err
			}
		}
	default:
		return dataPermissionError()
	}
	return nil
}

func (s *LanternService) authorizeReceiptRows(ctx context.Context, receipts []mutationreceipt.Receipt) error {
	if !s.dataAuthorization {
		return nil
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known {
		return dataPermissionError()
	}
	for _, receipt := range receipts {
		if !allowsReceiptMutationResource(admission.Access(), receipt) {
			return dataPermissionError()
		}
	}
	return nil
}

func (s *LanternService) authorizeReceiptObservations(ctx context.Context, observations []mutationreceipt.Observation) error {
	if !s.dataAuthorization {
		return nil
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known {
		return dataPermissionError()
	}
	for _, observation := range observations {
		if observation.Status == mutationreceipt.Confirmed {
			if !allowsReceiptMutationResource(admission.Access(), observation.Receipt) {
				return dataPermissionError()
			}
		} else if !wholeReceiptAbsence(admission.Access()) {
			return dataPermissionError()
		}
	}
	return nil
}
