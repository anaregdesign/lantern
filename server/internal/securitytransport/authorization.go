package securitytransport

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func (h *SecurityConnectHandler) PrepareSecurityChanges(ctx context.Context, req *connect.Request[pb.PrepareSecurityChangesRequest]) (*connect.Response[pb.PrepareSecurityChangesResponse], error) {
	return h.prepareCurrentChanges(ctx, req)
}
func (h *SecurityConnectHandler) BeginSecurityChangeAuthorization(ctx context.Context, req *connect.Request[pb.BeginSecurityChangeAuthorizationRequest]) (*connect.Response[pb.BeginSecurityChangeAuthorizationResponse], error) {
	return h.beginCurrentAuthorization(ctx, req)
}
func (h *SecurityConnectHandler) GetSecurityChangeAuthorization(ctx context.Context, req *connect.Request[pb.GetSecurityChangeAuthorizationRequest]) (*connect.Response[pb.GetSecurityChangeAuthorizationResponse], error) {
	return h.readCurrentAuthorization(ctx, req)
}
