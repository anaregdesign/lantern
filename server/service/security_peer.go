package service

import (
	"context"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// SecurityPeerConnectHandler belongs exclusively to the private workload mux.
// Neither public Roles nor an IdP token can issue a replica policy lease.
type SecurityPeerConnectHandler struct{ authority *security.LeaseAuthority }

func NewSecurityPeerConnectHandler(authority *security.LeaseAuthority) *SecurityPeerConnectHandler {
	return &SecurityPeerConnectHandler{authority: authority}
}

func (h *SecurityPeerConnectHandler) RenewPolicyLease(ctx context.Context, request *connect.Request[pb.RenewPolicyLeaseRequest]) (*connect.Response[pb.RenewPolicyLeaseResponse], error) {
	refuse := func() (*connect.Response[pb.RenewPolicyLeaseResponse], error) {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	admission, ok := peerauth.AdmissionFromContext(ctx)
	if !ok || admission.Check(ctx) != nil || h == nil || h.authority == nil {
		return refuse()
	}
	if request == nil || request.Msg == nil {
		return refuse()
	}
	msg := request.Msg
	if len(msg.GetReceiver()) != 16 || len(msg.GetBootNonce()) != 16 || len(msg.GetChallenge()) != 32 ||
		len(msg.GetKnownDigest()) != 0 && len(msg.GetKnownDigest()) != 32 {
		return refuse()
	}
	var challenge security.LeaseRequest
	copy(challenge.Receiver[:], msg.GetReceiver())
	copy(challenge.BootNonce[:], msg.GetBootNonce())
	copy(challenge.Challenge[:], msg.GetChallenge())
	if challenge.Receiver != admission.Member().ID {
		return refuse()
	}
	var known [32]byte
	copy(known[:], msg.GetKnownDigest())
	lease, checkpoint, err := h.authority.CheckpointSince(ctx, challenge, known)
	if err != nil || admission.Check(ctx) != nil {
		return refuse()
	}
	return connect.NewResponse(&pb.RenewPolicyLeaseResponse{SignedLease: lease, SignedCheckpoint: checkpoint}), nil
}
