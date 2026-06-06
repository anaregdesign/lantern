// Package service: connect.go adapts the existing *LanternService and
// *LanternReplicationService structs to the Connect-Go handler interfaces
// generated under pb/graph/v1/graphv1connect (#336). The adapters forward
// every Connect call to the unchanged underlying method, so all existing
// gRPC tests keep covering the business logic by reference.
//
// Why an adapter (rather than rewriting the service struct's method set):
//   - Until #347 cuts the primary :6380 listener over to h2c+Connect, both
//     the gRPC and Connect surfaces are live concurrently. Keeping the two
//     handler shapes in separate structs lets each surface evolve without
//     either becoming an "almost-Connect" or "almost-gRPC" Frankenstein.
//   - The grpc.ServerStreamingServer[T] interface that the existing
//     Subscribe/Snapshot methods take has seven methods; only Send and
//     Context are actually called inside replication.go. We satisfy the
//     full interface here with a tiny shim (connectServerStream) so the
//     unchanged Subscribe/Snapshot implementations work over Connect with
//     zero modification.
package service

import (
	"context"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"

	"google.golang.org/grpc/metadata"
)

// NewLanternServiceConnectHandler wraps *LanternService so it satisfies
// graphv1connect.LanternServiceHandler. The returned value carries no
// extra state; it is safe to construct on demand at wire time.
func NewLanternServiceConnectHandler(svc *LanternService) graphv1connect.LanternServiceHandler {
	return &lanternServiceConnect{svc: svc}
}

// NewLanternReplicationServiceConnectHandler wraps
// *LanternReplicationService so it satisfies
// graphv1connect.LanternReplicationServiceHandler. Nil is permitted (mirrors
// the gRPC path where replication can be disabled); in that case all three
// RPCs return Unavailable.
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
func (h *lanternServiceConnect) CountVerticesByPrefix(ctx context.Context, req *connect.Request[pb.CountVerticesByPrefixRequest]) (*connect.Response[pb.CountVerticesByPrefixResponse], error) {
	return unary(ctx, req, h.svc.CountVerticesByPrefix)
}
func (h *lanternServiceConnect) DeleteVerticesByPrefix(ctx context.Context, req *connect.Request[pb.DeleteVerticesByPrefixRequest]) (*connect.Response[pb.DeleteVerticesByPrefixResponse], error) {
	return unary(ctx, req, h.svc.DeleteVerticesByPrefix)
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
func (h *lanternServiceConnect) ScanEdges(ctx context.Context, req *connect.Request[pb.ScanEdgesRequest]) (*connect.Response[pb.ScanEdgesResponse], error) {
	return unary(ctx, req, h.svc.ScanEdges)
}
func (h *lanternServiceConnect) GetServerStatus(ctx context.Context, req *connect.Request[pb.GetServerStatusRequest]) (*connect.Response[pb.GetServerStatusResponse], error) {
	return unary(ctx, req, h.svc.GetServerStatus)
}
func (h *lanternServiceConnect) GetReplicationStatus(ctx context.Context, req *connect.Request[pb.GetReplicationStatusRequest]) (*connect.Response[pb.GetReplicationStatusResponse], error) {
	return unary(ctx, req, h.svc.GetReplicationStatus)
}

type lanternReplicationServiceConnect struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	svc *LanternReplicationService
}

func (h *lanternReplicationServiceConnect) Subscribe(ctx context.Context, req *connect.Request[pb.SubscribeRequest], stream *connect.ServerStream[pb.SubscribeResponse]) error {
	if h.svc == nil {
		return connect.NewError(connect.CodeUnavailable, errReplicationDisabled)
	}
	return h.svc.Subscribe(req.Msg, newConnectServerStream[pb.SubscribeResponse](ctx, stream))
}

func (h *lanternReplicationServiceConnect) Snapshot(ctx context.Context, req *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	if h.svc == nil {
		return connect.NewError(connect.CodeUnavailable, errReplicationDisabled)
	}
	return h.svc.Snapshot(req.Msg, newConnectServerStream[pb.SnapshotResponse](ctx, stream))
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

// connectServerStream adapts *connect.ServerStream[T] to the
// grpc.ServerStreamingServer[T] interface that the existing
// Subscribe/Snapshot methods take. The Subscribe/Snapshot implementations
// in replication.go only call Send and Context — the remaining
// grpc.ServerStream methods (SetHeader/SendHeader/SetTrailer/SendMsg/
// RecvMsg) are no-ops or panic, which is safe because the production
// callers never invoke them.
type connectServerStream[T any] struct {
	ctx    context.Context
	stream *connect.ServerStream[T]
}

func newConnectServerStream[T any](ctx context.Context, stream *connect.ServerStream[T]) *connectServerStream[T] {
	return &connectServerStream[T]{ctx: ctx, stream: stream}
}

func (s *connectServerStream[T]) Send(m *T) error          { return s.stream.Send(m) }
func (s *connectServerStream[T]) Context() context.Context { return s.ctx }

// SetHeader / SendHeader / SetTrailer / SendMsg / RecvMsg satisfy the
// remaining grpc.ServerStream surface. They are unused by the underlying
// Subscribe/Snapshot implementations.
func (s *connectServerStream[T]) SetHeader(md metadata.MD) error  { return nil }
func (s *connectServerStream[T]) SendHeader(md metadata.MD) error { return nil }
func (s *connectServerStream[T]) SetTrailer(md metadata.MD)       {}

func (s *connectServerStream[T]) SendMsg(any) error {
	panic("connectServerStream.SendMsg: unsupported on the Connect adapter; production callers use Send")
}

func (s *connectServerStream[T]) RecvMsg(any) error {
	panic("connectServerStream.RecvMsg: unsupported on the Connect adapter; server-streaming has no client→server frames")
}
