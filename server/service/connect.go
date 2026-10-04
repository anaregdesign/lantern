// Package service: connect.go adapts *LanternService and
// *LanternReplicationService to the Connect-Go handler interfaces
// generated under pb/graph/v1/graphv1connect. The adapters forward writes
// directly; graph reads use the service's Snapshot latch and publication
// generation checks.
//
// Why an adapter (rather than implementing the Connect interfaces
// directly on the service structs):
//   - The service structs predate Connect codegen and have value-typed
//     request/response signatures (no connect.Request[T] wrapper).
//     Adapting them here keeps each side idiomatic for its consumers.
//   - The Connect server-stream method signature takes ctx + req +
//     *connect.ServerStream[T]. *connect.ServerStream[T] satisfies the
//     local service.Sender[T] interface directly (both expose
//     Send(*T) error), so streaming methods forward without a bridge.
package service

import (
	"context"
	"errors"
	"mime"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// NewLanternServiceConnectHandler wraps *LanternService so it satisfies
// graphv1connect.LanternServiceHandler. The returned value carries no
// extra state; it is safe to construct on demand at wire time.
func NewLanternServiceConnectHandler(svc *LanternService) graphv1connect.LanternServiceHandler {
	return &lanternServiceConnect{svc: svc}
}

// NewLanternReplicationServiceConnectHandler wraps
// *LanternReplicationService so it satisfies
// graphv1connect.LanternReplicationServiceHandler. Nil is permitted (so
// replication can be disabled on single-node deployments); in that case
// all three RPCs return Unavailable.
func NewLanternReplicationServiceConnectHandler(svc *LanternReplicationService) graphv1connect.LanternReplicationServiceHandler {
	return &lanternReplicationServiceConnect{svc: svc}
}

type lanternServiceConnect struct {
	graphv1connect.UnimplementedLanternServiceHandler
	svc *LanternService
}

// unary is the one-line forwarding helper every adapter method uses.
// Generics keep the per-method code to a single line while preserving
// type safety: the call site supplies the typed underlying method.
//
// Service-layer errors are already native *connect.Error values (see
// server/service/errors.go and the per-method bodies), so this helper
// is a pure boxing shim — no error translation required.
func unary[Req, Resp any](ctx context.Context, req *connect.Request[Req], fn func(context.Context, *Req) (*Resp, error)) (*connect.Response[Resp], error) {
	out, err := fn(ctx, req.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (h *lanternServiceConnect) Illuminate(ctx context.Context, req *connect.Request[pb.IlluminateRequest]) (*connect.Response[pb.IlluminateResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.Illuminate)
}
func (h *lanternServiceConnect) GetVertex(ctx context.Context, req *connect.Request[pb.GetVertexRequest]) (*connect.Response[pb.GetVertexResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetVertex)
}
func (h *lanternServiceConnect) GetVertices(ctx context.Context, req *connect.Request[pb.GetVerticesRequest]) (*connect.Response[pb.GetVerticesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetVertices)
}
func (h *lanternServiceConnect) PutVertex(ctx context.Context, req *connect.Request[pb.PutVertexRequest]) (*connect.Response[pb.PutVertexResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.PutVertex)
}
func (h *lanternServiceConnect) PutVertices(ctx context.Context, req *connect.Request[pb.PutVerticesRequest]) (*connect.Response[pb.PutVerticesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.PutVertices)
}
func (h *lanternServiceConnect) DeleteVertex(ctx context.Context, req *connect.Request[pb.DeleteVertexRequest]) (*connect.Response[pb.DeleteVertexResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteVertex)
}
func (h *lanternServiceConnect) DeleteVertices(ctx context.Context, req *connect.Request[pb.DeleteVerticesRequest]) (*connect.Response[pb.DeleteVerticesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteVertices)
}
func (h *lanternServiceConnect) ScanVertices(ctx context.Context, req *connect.Request[pb.ScanVerticesRequest]) (*connect.Response[pb.ScanVerticesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.ScanVertices)
}
func (h *lanternServiceConnect) ScanVertexKeys(ctx context.Context, req *connect.Request[pb.ScanVertexKeysRequest]) (*connect.Response[pb.ScanVertexKeysResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.ScanVertexKeys)
}
func (h *lanternServiceConnect) SearchVertices(ctx context.Context, req *connect.Request[pb.SearchVerticesRequest]) (*connect.Response[pb.SearchVerticesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.SearchVertices)
}
func (h *lanternServiceConnect) CountVerticesByPrefix(ctx context.Context, req *connect.Request[pb.CountVerticesByPrefixRequest]) (*connect.Response[pb.CountVerticesByPrefixResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.CountVerticesByPrefix)
}
func (h *lanternServiceConnect) DeleteVerticesByPrefix(ctx context.Context, req *connect.Request[pb.DeleteVerticesByPrefixRequest]) (*connect.Response[pb.DeleteVerticesByPrefixResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteVerticesByPrefix)
}
func (h *lanternServiceConnect) TopVerticesByDegree(ctx context.Context, req *connect.Request[pb.TopVerticesByDegreeRequest]) (*connect.Response[pb.TopVerticesByDegreeResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.TopVerticesByDegree)
}
func (h *lanternServiceConnect) GetEdge(ctx context.Context, req *connect.Request[pb.GetEdgeRequest]) (*connect.Response[pb.GetEdgeResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetEdge)
}
func (h *lanternServiceConnect) GetEdges(ctx context.Context, req *connect.Request[pb.GetEdgesRequest]) (*connect.Response[pb.GetEdgesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetEdges)
}
func (h *lanternServiceConnect) AddEdge(ctx context.Context, req *connect.Request[pb.AddEdgeRequest]) (*connect.Response[pb.AddEdgeResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.AddEdge)
}
func (h *lanternServiceConnect) AddEdges(ctx context.Context, req *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.AddEdges)
}
func (h *lanternServiceConnect) PutEdge(ctx context.Context, req *connect.Request[pb.PutEdgeRequest]) (*connect.Response[pb.PutEdgeResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.PutEdge)
}
func (h *lanternServiceConnect) PutEdges(ctx context.Context, req *connect.Request[pb.PutEdgesRequest]) (*connect.Response[pb.PutEdgesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.PutEdges)
}
func (h *lanternServiceConnect) DeleteEdge(ctx context.Context, req *connect.Request[pb.DeleteEdgeRequest]) (*connect.Response[pb.DeleteEdgeResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteEdge)
}
func (h *lanternServiceConnect) DeleteEdges(ctx context.Context, req *connect.Request[pb.DeleteEdgesRequest]) (*connect.Response[pb.DeleteEdgesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteEdges)
}
func (h *lanternServiceConnect) DeleteEdgeContribution(ctx context.Context, req *connect.Request[pb.DeleteEdgeContributionRequest]) (*connect.Response[pb.DeleteEdgeContributionResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteEdgeContribution)
}
func (h *lanternServiceConnect) DeleteEdgeContributions(ctx context.Context, req *connect.Request[pb.DeleteEdgeContributionsRequest]) (*connect.Response[pb.DeleteEdgeContributionsResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteEdgeContributions)
}
func (h *lanternServiceConnect) DeleteEdgesByPrefix(ctx context.Context, req *connect.Request[pb.DeleteEdgesByPrefixRequest]) (*connect.Response[pb.DeleteEdgesByPrefixResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.DeleteEdgesByPrefix)
}
func (h *lanternServiceConnect) ScanEdges(ctx context.Context, req *connect.Request[pb.ScanEdgesRequest]) (*connect.Response[pb.ScanEdgesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.ScanEdges)
}
func (h *lanternServiceConnect) GetServerStatus(ctx context.Context, req *connect.Request[pb.GetServerStatusRequest]) (*connect.Response[pb.GetServerStatusResponse], error) {
	// GetServerStatus owns its committed view so direct service callers and
	// this adapter observe the same fail-closed cut without nested RLocks.
	return dataUnary(ctx, req, h.svc, h.svc.GetServerStatus)
}
func (h *lanternServiceConnect) GetReplicationStatus(ctx context.Context, req *connect.Request[pb.GetReplicationStatusRequest]) (*connect.Response[pb.GetReplicationStatusResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetReplicationStatus)
}
func (h *lanternServiceConnect) GetReceiptCapability(ctx context.Context, req *connect.Request[pb.GetReceiptCapabilityRequest]) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetReceiptCapability)
}
func (h *lanternServiceConnect) GetReceiptStatus(ctx context.Context, req *connect.Request[pb.GetReceiptStatusRequest]) (*connect.Response[pb.GetReceiptStatusResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetReceiptStatus)
}
func (h *lanternServiceConnect) GetReceiptStatuses(ctx context.Context, req *connect.Request[pb.GetReceiptStatusesRequest]) (*connect.Response[pb.GetReceiptStatusesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.GetReceiptStatuses)
}
func (h *lanternServiceConnect) BackupSnapshot(ctx context.Context, req *connect.Request[pb.BackupSnapshotRequest], stream *connect.ServerStream[pb.BackupSnapshotResponse]) error {
	admission, err := h.svc.authorizeData(ctx, req.Msg)
	if err != nil {
		return err
	}
	ctx, err = h.svc.dataQueryContext(ctx, req.Msg, admission)
	if err != nil {
		return err
	}
	// *connect.ServerStream[T] satisfies service.Sender[T] directly.
	if h.svc.namespaceFormat == "" {
		return h.svc.BackupSnapshot(ctx, req.Msg, stream)
	}
	mapped, _, err := h.svc.mapDataRequest(req.Msg)
	if err != nil {
		return err
	}
	return h.svc.BackupSnapshot(ctx, mapped.(*pb.BackupSnapshotRequest), dataBackupSender{ctx: ctx, service: h.svc, next: stream})
}

type lanternReplicationServiceConnect struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	svc *LanternReplicationService
}

func (h *lanternReplicationServiceConnect) Subscribe(ctx context.Context, req *connect.Request[pb.SubscribeRequest], stream *connect.ServerStream[pb.SubscribeResponse]) error {
	if h.svc == nil {
		return connect.NewError(connect.CodeUnavailable, errReplicationDisabled)
	}
	if req.Msg.GetProjection() != pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY &&
		!binaryProtobufContentType(req.Header().Get("Content-Type")) {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("full-mutation Subscribe requires binary protobuf encoding; JSON and unknown codecs are unsupported"))
	}
	return h.svc.Subscribe(ctx, req.Msg, replicationHeaderSender{stream})
}

// A nil transport send flushes headers without serializing a protobuf frame.
// The service invokes it only after accepting and registering a subscription.
type replicationHeaderSender struct {
	*connect.ServerStream[pb.SubscribeResponse]
}

func (s replicationHeaderSender) FlushHeaders() error { return s.Send(nil) }

func binaryProtobufContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch mediaType {
	case "application/connect+proto",
		"application/grpc", "application/grpc+proto",
		"application/grpc-web", "application/grpc-web+proto":
		return true
	default:
		return false
	}
}

func (h *lanternReplicationServiceConnect) Snapshot(ctx context.Context, req *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	if h.svc == nil {
		return connect.NewError(connect.CodeUnavailable, errReplicationDisabled)
	}
	return h.svc.Snapshot(ctx, req.Msg, stream)
}

func (h *lanternReplicationServiceConnect) PeerStatus(ctx context.Context, req *connect.Request[pb.PeerStatusRequest]) (*connect.Response[pb.PeerStatusResponse], error) {
	if h.svc == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errReplicationDisabled)
	}
	return unary(ctx, req, h.svc.PeerStatus)
}

// errReplicationDisabled is the sentinel surfaced when the Connect adapter
// is wired but the underlying *LanternReplicationService is nil.
var errReplicationDisabled = &replicationDisabledError{}

type replicationDisabledError struct{}

func (*replicationDisabledError) Error() string {
	return "replication is not enabled on this server"
}

func (h *lanternServiceConnect) CreateEdge(ctx context.Context, req *connect.Request[pb.CreateEdgeRequest]) (*connect.Response[pb.CreateEdgeResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.CreateEdge)
}
func (h *lanternServiceConnect) CreateEdges(ctx context.Context, req *connect.Request[pb.CreateEdgesRequest]) (*connect.Response[pb.CreateEdgesResponse], error) {
	return dataUnary(ctx, req, h.svc, h.svc.CreateEdges)
}
