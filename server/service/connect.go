// Package service: connect.go adapts *LanternService and
// *LanternReplicationService to the Connect-Go handler interfaces
// generated under pb/graph/v1/graphv1connect. The adapters forward
// every Connect call to the unchanged underlying method.
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
	return unary(ctx, req, h.svc.Illuminate)
}
func (h *lanternServiceConnect) GetVertex(ctx context.Context, req *connect.Request[pb.GetVertexRequest]) (*connect.Response[pb.GetVertexResponse], error) {
	return unary(ctx, req, h.svc.GetVertex)
}
func (h *lanternServiceConnect) GetVertices(ctx context.Context, req *connect.Request[pb.GetVerticesRequest]) (*connect.Response[pb.GetVerticesResponse], error) {
	return unary(ctx, req, h.svc.GetVertices)
}
func (h *lanternServiceConnect) PutVertex(ctx context.Context, req *connect.Request[pb.PutVertexRequest]) (*connect.Response[pb.PutVertexResponse], error) {
	return unary(ctx, req, h.svc.PutVertex)
}
func (h *lanternServiceConnect) PutVertices(ctx context.Context, req *connect.Request[pb.PutVerticesRequest]) (*connect.Response[pb.PutVerticesResponse], error) {
	return unary(ctx, req, h.svc.PutVertices)
}
func (h *lanternServiceConnect) DeleteVertex(ctx context.Context, req *connect.Request[pb.DeleteVertexRequest]) (*connect.Response[pb.DeleteVertexResponse], error) {
	return unary(ctx, req, h.svc.DeleteVertex)
}
func (h *lanternServiceConnect) DeleteVertices(ctx context.Context, req *connect.Request[pb.DeleteVerticesRequest]) (*connect.Response[pb.DeleteVerticesResponse], error) {
	return unary(ctx, req, h.svc.DeleteVertices)
}
func (h *lanternServiceConnect) ScanVertices(ctx context.Context, req *connect.Request[pb.ScanVerticesRequest]) (*connect.Response[pb.ScanVerticesResponse], error) {
	return unary(ctx, req, h.svc.ScanVertices)
}
func (h *lanternServiceConnect) ScanVertexKeys(ctx context.Context, req *connect.Request[pb.ScanVertexKeysRequest]) (*connect.Response[pb.ScanVertexKeysResponse], error) {
	return unary(ctx, req, h.svc.ScanVertexKeys)
}
func (h *lanternServiceConnect) SearchVertices(ctx context.Context, req *connect.Request[pb.SearchVerticesRequest]) (*connect.Response[pb.SearchVerticesResponse], error) {
	return unary(ctx, req, h.svc.SearchVertices)
}
func (h *lanternServiceConnect) CountVerticesByPrefix(ctx context.Context, req *connect.Request[pb.CountVerticesByPrefixRequest]) (*connect.Response[pb.CountVerticesByPrefixResponse], error) {
	return unary(ctx, req, h.svc.CountVerticesByPrefix)
}
func (h *lanternServiceConnect) DeleteVerticesByPrefix(ctx context.Context, req *connect.Request[pb.DeleteVerticesByPrefixRequest]) (*connect.Response[pb.DeleteVerticesByPrefixResponse], error) {
	return unary(ctx, req, h.svc.DeleteVerticesByPrefix)
}
func (h *lanternServiceConnect) TopVerticesByDegree(ctx context.Context, req *connect.Request[pb.TopVerticesByDegreeRequest]) (*connect.Response[pb.TopVerticesByDegreeResponse], error) {
	return unary(ctx, req, h.svc.TopVerticesByDegree)
}
func (h *lanternServiceConnect) GetEdge(ctx context.Context, req *connect.Request[pb.GetEdgeRequest]) (*connect.Response[pb.GetEdgeResponse], error) {
	return unary(ctx, req, h.svc.GetEdge)
}
func (h *lanternServiceConnect) GetEdges(ctx context.Context, req *connect.Request[pb.GetEdgesRequest]) (*connect.Response[pb.GetEdgesResponse], error) {
	return unary(ctx, req, h.svc.GetEdges)
}
func (h *lanternServiceConnect) AddEdge(ctx context.Context, req *connect.Request[pb.AddEdgeRequest]) (*connect.Response[pb.AddEdgeResponse], error) {
	return unary(ctx, req, h.svc.AddEdge)
}
func (h *lanternServiceConnect) AddEdges(ctx context.Context, req *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	return unary(ctx, req, h.svc.AddEdges)
}
func (h *lanternServiceConnect) PutEdge(ctx context.Context, req *connect.Request[pb.PutEdgeRequest]) (*connect.Response[pb.PutEdgeResponse], error) {
	return unary(ctx, req, h.svc.PutEdge)
}
func (h *lanternServiceConnect) PutEdges(ctx context.Context, req *connect.Request[pb.PutEdgesRequest]) (*connect.Response[pb.PutEdgesResponse], error) {
	return unary(ctx, req, h.svc.PutEdges)
}
func (h *lanternServiceConnect) DeleteEdge(ctx context.Context, req *connect.Request[pb.DeleteEdgeRequest]) (*connect.Response[pb.DeleteEdgeResponse], error) {
	return unary(ctx, req, h.svc.DeleteEdge)
}
func (h *lanternServiceConnect) DeleteEdges(ctx context.Context, req *connect.Request[pb.DeleteEdgesRequest]) (*connect.Response[pb.DeleteEdgesResponse], error) {
	return unary(ctx, req, h.svc.DeleteEdges)
}
func (h *lanternServiceConnect) DeleteEdgesByPrefix(ctx context.Context, req *connect.Request[pb.DeleteEdgesByPrefixRequest]) (*connect.Response[pb.DeleteEdgesByPrefixResponse], error) {
	return unary(ctx, req, h.svc.DeleteEdgesByPrefix)
}
func (h *lanternServiceConnect) ScanEdges(ctx context.Context, req *connect.Request[pb.ScanEdgesRequest]) (*connect.Response[pb.ScanEdgesResponse], error) {
	return unary(ctx, req, h.svc.ScanEdges)
}
func (h *lanternServiceConnect) GetServerStatus(ctx context.Context, req *connect.Request[pb.GetServerStatusRequest]) (*connect.Response[pb.GetServerStatusResponse], error) {
	return unary(ctx, req, h.svc.GetServerStatus)
}
func (h *lanternServiceConnect) GetReplicationStatus(ctx context.Context, req *connect.Request[pb.GetReplicationStatusRequest]) (*connect.Response[pb.GetReplicationStatusResponse], error) {
	return unary(ctx, req, h.svc.GetReplicationStatus)
}
func (h *lanternServiceConnect) BackupSnapshot(ctx context.Context, req *connect.Request[pb.BackupSnapshotRequest], stream *connect.ServerStream[pb.BackupSnapshotResponse]) error {
	// *connect.ServerStream[T] satisfies service.Sender[T] directly.
	return h.svc.BackupSnapshot(ctx, req.Msg, stream)
}

type lanternReplicationServiceConnect struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	svc *LanternReplicationService
}

func (h *lanternReplicationServiceConnect) Subscribe(ctx context.Context, req *connect.Request[pb.SubscribeRequest], stream *connect.ServerStream[pb.SubscribeResponse]) error {
	if h.svc == nil {
		return connect.NewError(connect.CodeUnavailable, errReplicationDisabled)
	}
	// *connect.ServerStream[T] satisfies service.Sender[T] directly
	// (both expose Send(*T) error). The service method returns a
	// *connect.Error already, so no translation layer is needed.
	return h.svc.Subscribe(ctx, req.Msg, stream)
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
