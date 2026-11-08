package securitytransport

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authz"
	"github.com/anaregdesign/lantern/server/internal/security"
)

var edgeModes = map[pb.EdgeEndpointMode]authz.EdgeEndpointMode{
	pb.EdgeEndpointMode_EDGE_ENDPOINT_MODE_READ_READ:   authz.EdgeReadRead,
	pb.EdgeEndpointMode_EDGE_ENDPOINT_MODE_WRITE_WRITE: authz.EdgeWriteWrite,
	pb.EdgeEndpointMode_EDGE_ENDPOINT_MODE_READ_WRITE:  authz.EdgeReadWrite,
	pb.EdgeEndpointMode_EDGE_ENDPOINT_MODE_WRITE_READ:  authz.EdgeWriteRead,
}

func decodeEdgePolicy(p *pb.SecurityEdgePolicy) (*security.EdgePolicy, error) {
	if p == nil || strictSecurityMessage(p) != nil || p.Version != security.EdgePolicyVersion {
		return nil, security.ErrInvalidImage
	}
	mode, ok := edgeModes[p.Mode]
	if !ok {
		return nil, security.ErrInvalidImage
	}
	return &security.EdgePolicy{Version: security.EdgePolicyVersion, Mode: mode}, nil
}
func encodeEdgePolicy(p security.EdgePolicy) *pb.SecurityEdgePolicy {
	for wire, mode := range edgeModes {
		if p.Mode == mode {
			return &pb.SecurityEdgePolicy{Version: uint32(p.Version), Mode: wire}
		}
	}
	return nil
}
func (h *SecurityConnectHandler) GetEdgePolicy(ctx context.Context, req *connect.Request[pb.GetEdgePolicyRequest]) (*connect.Response[pb.GetEdgePolicyResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	a, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	return securityReadResponse(ctx, a, h.now(), &pb.GetEdgePolicyResponse{Policy: encodeEdgePolicy(a.Snapshot().EdgePolicy()), Version: SecurityVersionForAdmission(a)})
}
