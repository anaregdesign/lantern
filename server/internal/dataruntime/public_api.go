package dataruntime

import (
	"context"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

// PublicAPI admits logical data operations against one execution owner. It
// exposes typed operations rather than mutable state or publication locks.
// Transport adapters only wrap the resulting messages and errors.
type PublicAPI struct {
	executor *Executor
}

// Public returns a public-operation facade bound to this exact executor.
func (s *Executor) Public() *PublicAPI { return &PublicAPI{executor: s} }

func (p *PublicAPI) Illuminate(ctx context.Context, request *pb.IlluminateRequest) (*pb.IlluminateResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.Illuminate)
}

func (p *PublicAPI) GetVertex(ctx context.Context, request *pb.GetVertexRequest) (*pb.GetVertexResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetVertex)
}

func (p *PublicAPI) GetVertices(ctx context.Context, request *pb.GetVerticesRequest) (*pb.GetVerticesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetVertices)
}

func (p *PublicAPI) PutVertex(ctx context.Context, request *pb.PutVertexRequest) (*pb.PutVertexResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.PutVertex)
}

func (p *PublicAPI) PutVertices(ctx context.Context, request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.PutVertices)
}

func (p *PublicAPI) MutateGraph(ctx context.Context, request *pb.MutateGraphRequest) (*pb.MutateGraphResponse, error) {
	if request == nil || len(request.GetItems()) > 64 || proto.Size(request) > graphMutationMaxBytes {
		return nil, invalidReceiptRequest(errGraphMutation)
	}
	return invokePublic(ctx, request, p.executor, p.executor.MutateGraph)
}

func (p *PublicAPI) DeleteVertex(ctx context.Context, request *pb.DeleteVertexRequest) (*pb.DeleteVertexResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteVertex)
}

func (p *PublicAPI) DeleteVertices(ctx context.Context, request *pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteVertices)
}

func (p *PublicAPI) ScanVertices(ctx context.Context, request *pb.ScanVerticesRequest) (*pb.ScanVerticesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.ScanVertices)
}

func (p *PublicAPI) ScanVertexKeys(ctx context.Context, request *pb.ScanVertexKeysRequest) (*pb.ScanVertexKeysResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.ScanVertexKeys)
}

func (p *PublicAPI) SearchVertices(ctx context.Context, request *pb.SearchVerticesRequest) (*pb.SearchVerticesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.SearchVertices)
}

func (p *PublicAPI) CountVerticesByPrefix(ctx context.Context, request *pb.CountVerticesByPrefixRequest) (*pb.CountVerticesByPrefixResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.CountVerticesByPrefix)
}

func (p *PublicAPI) DeleteVerticesByPrefix(ctx context.Context, request *pb.DeleteVerticesByPrefixRequest) (*pb.DeleteVerticesByPrefixResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteVerticesByPrefix)
}

func (p *PublicAPI) TopVerticesByDegree(ctx context.Context, request *pb.TopVerticesByDegreeRequest) (*pb.TopVerticesByDegreeResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.TopVerticesByDegree)
}

func (p *PublicAPI) GetEdge(ctx context.Context, request *pb.GetEdgeRequest) (*pb.GetEdgeResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetEdge)
}

func (p *PublicAPI) GetEdges(ctx context.Context, request *pb.GetEdgesRequest) (*pb.GetEdgesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetEdges)
}

func (p *PublicAPI) AddEdge(ctx context.Context, request *pb.AddEdgeRequest) (*pb.AddEdgeResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.AddEdge)
}

func (p *PublicAPI) AddEdges(ctx context.Context, request *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.AddEdges)
}

func (p *PublicAPI) PutEdge(ctx context.Context, request *pb.PutEdgeRequest) (*pb.PutEdgeResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.PutEdge)
}

func (p *PublicAPI) PutEdges(ctx context.Context, request *pb.PutEdgesRequest) (*pb.PutEdgesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.PutEdges)
}

func (p *PublicAPI) DeleteEdge(ctx context.Context, request *pb.DeleteEdgeRequest) (*pb.DeleteEdgeResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteEdge)
}

func (p *PublicAPI) DeleteEdges(ctx context.Context, request *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteEdges)
}

func (p *PublicAPI) DeleteEdgeContribution(ctx context.Context, request *pb.DeleteEdgeContributionRequest) (*pb.DeleteEdgeContributionResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteEdgeContribution)
}

func (p *PublicAPI) DeleteEdgeContributions(ctx context.Context, request *pb.DeleteEdgeContributionsRequest) (*pb.DeleteEdgeContributionsResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteEdgeContributions)
}

func (p *PublicAPI) DeleteEdgesByPrefix(ctx context.Context, request *pb.DeleteEdgesByPrefixRequest) (*pb.DeleteEdgesByPrefixResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.DeleteEdgesByPrefix)
}

func (p *PublicAPI) ScanEdges(ctx context.Context, request *pb.ScanEdgesRequest) (*pb.ScanEdgesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.ScanEdges)
}

func (p *PublicAPI) GetServerStatus(ctx context.Context, request *pb.GetServerStatusRequest) (*pb.GetServerStatusResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetServerStatus)
}

func (p *PublicAPI) GetReplicationStatus(ctx context.Context, request *pb.GetReplicationStatusRequest) (*pb.GetReplicationStatusResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetReplicationStatus)
}

func (p *PublicAPI) GetReceiptCapability(ctx context.Context, request *pb.GetReceiptCapabilityRequest) (*pb.GetReceiptCapabilityResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetReceiptCapability)
}

func (p *PublicAPI) GetReceiptStatus(ctx context.Context, request *pb.GetReceiptStatusRequest) (*pb.GetReceiptStatusResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetReceiptStatus)
}

func (p *PublicAPI) GetReceiptStatuses(ctx context.Context, request *pb.GetReceiptStatusesRequest) (*pb.GetReceiptStatusesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.GetReceiptStatuses)
}

func (p *PublicAPI) CreateEdge(ctx context.Context, request *pb.CreateEdgeRequest) (*pb.CreateEdgeResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.CreateEdge)
}

func (p *PublicAPI) CreateEdges(ctx context.Context, request *pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
	return invokePublic(ctx, request, p.executor, p.executor.CreateEdges)
}

// BackupSnapshot streams admitted logical data frames. The executor owns
// namespace conversion and each frame's current output admission check.
func (p *PublicAPI) BackupSnapshot(ctx context.Context, request *pb.BackupSnapshotRequest, stream Sender[pb.BackupSnapshotResponse]) error {
	s := p.executor
	admission, err := s.authorizeData(ctx, request)
	if err != nil {
		return err
	}
	ctx, err = s.dataQueryContext(ctx, request, admission)
	if err != nil {
		return err
	}
	if s.namespaceFormat == "" {
		return s.BackupSnapshot(ctx, request, stream)
	}
	mapped, _, err := s.mapDataRequest(request)
	if err != nil {
		return err
	}
	return s.BackupSnapshot(ctx, mapped.(*pb.BackupSnapshotRequest), dataBackupSender{ctx: ctx, service: s, next: stream})
}
