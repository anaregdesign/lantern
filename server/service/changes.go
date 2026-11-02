package service

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
)

// ChangeConnectHandler adapts the runtime's authorized change stream to Connect.
type ChangeConnectHandler struct {
	changes *dataruntime.ChangeService
}

var _ graphv1connect.LanternChangeServiceHandler = (*ChangeConnectHandler)(nil)

func NewChangeConnectHandler(svc *dataruntime.Executor, options dataruntime.ChangeServiceOptions) (*ChangeConnectHandler, error) {
	changes, err := dataruntime.NewChangeService(svc, options)
	if err != nil {
		return nil, err
	}
	return &ChangeConnectHandler{changes: changes}, nil
}

func (h *ChangeConnectHandler) WatchChanges(ctx context.Context, req *connect.Request[pb.WatchChangesRequest], stream *connect.ServerStream[pb.WatchChangesResponse]) error {
	return h.changes.Watch(ctx, req.Msg, stream)
}
