package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func currentControlError(err error) error { return connect.NewError(securityErrorCode(err), err) }

func (h *SecurityConnectHandler) currentManagement(ctx context.Context) (*security.Admission, security.CurrentCredentialProducer, error) {
	a, err := h.admission(ctx, true)
	if err != nil {
		return nil, nil, err
	}
	if err = a.CheckManagement(ctx, nil, h.now()); err != nil {
		return nil, nil, currentControlError(err)
	}
	p, err := h.current.RequestCredential(ctx)
	if err != nil {
		return nil, nil, currentControlError(err)
	}
	return a, p, nil
}

// A control Apply/catch-up may advance the cut. Re-verify the original trusted
// request producer explicitly; never relabel an already computed query result.
func (h *SecurityConnectHandler) currentDisclosure(ctx context.Context) (context.Context, *security.Admission, error) {
	ctx, a, err := h.current.RefreshRequest(ctx)
	if err != nil {
		return ctx, nil, currentControlError(err)
	}
	if !a.Access().AllowsGlobal(security.SecurityManage) {
		return ctx, nil, currentControlError(security.ErrPermissionDenied)
	}
	if err := a.BindCurrentGlobalOutput(ctx, security.SecurityManage); err != nil {
		return ctx, nil, currentControlError(err)
	}
	return ctx, a, nil
}

func (h *SecurityConnectHandler) validateCurrentIssuers(ctx context.Context, a *security.Admission, command security.S1Command) error {
	for _, c := range command.Changes {
		if c.Kind != security.PutIssuer || !c.Issuer.Enabled {
			continue
		}
		i := *c.Issuer
		if c.PreserveSecret {
			if prior, known := a.Snapshot().Issuer(i.URL); known {
				i.SecretRef = prior.SecretRef
			}
		}
		if h.validateIssuer(ctx, i) != nil {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("issuer validation failed"))
		}
	}
	return nil
}

func (h *SecurityConnectHandler) prepareCurrentChanges(ctx context.Context, req *connect.Request[pb.PrepareSecurityChangesRequest]) (*connect.Response[pb.PrepareSecurityChangesResponse], error) {
	if securityRequestError(req.Msg) != nil || req.Msg.Review != nil {
		return nil, currentControlError(security.ErrS1Contract)
	}
	a, producer, err := h.currentManagement(ctx)
	if err != nil {
		return nil, err
	}
	wire := req.Msg.CurrentReview
	p, c, command, err := decodeCurrentCommand(wire)
	if err != nil || p != h.current.Profile() {
		return nil, currentControlError(security.ErrS1Contract)
	}
	response := &pb.PrepareSecurityChangesResponse{}
	var review security.CurrentReview
	var purpose bool
	if wire.ChangeId != nil {
		review, _, err = h.decodeCurrentReview(wire)
		if err != nil {
			return nil, currentControlError(err)
		}
		retained, lookupErr := h.current.Lookup(ctx, p, review.ID, review.Operation.Digest())
		if errors.Is(lookupErr, security.ErrChangeConflict) {
			return nil, currentControlError(lookupErr)
		}
		// Unknown is deliberately still unresolved, never safe nonexecution.
		if retained.Profile != p {
			return nil, currentControlError(security.ErrAuthorityUnavailable)
		}
		response.CurrentResult = EncodeCurrentResult(h.current.ObserveResult(ctx, retained))
		if retained.Progress == security.CurrentUnresolved {
			purpose, err = h.current.ReviewRequirement(ctx, a, review)
			if err == nil {
				response.Requirement = pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY
			}
		} else {
			ctx, a, err = h.currentDisclosure(ctx)
			if err != nil {
				return nil, err
			}
		}
	} else {
		if wire.Actor != nil || len(wire.IntentDigest) != 0 {
			return nil, currentControlError(security.ErrS1Contract)
		}
		if err = h.validateCurrentIssuers(ctx, a, command); err != nil {
			return nil, err
		}
		review, purpose, err = h.current.Prepare(ctx, a, producer, p, c, command)
		if err != nil {
			return nil, currentControlError(err)
		}
		response.Requirement = pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY
	}
	response.CurrentReview = proto.Clone(wire).(*pb.CurrentSecurityReview)
	response.CurrentReview.ChangeId = encodeCurrentID(review.ID)
	response.CurrentReview.Actor = encodeSecurityIdentity(review.Operation.Actor())
	intent := review.Operation.Digest()
	response.CurrentReview.IntentDigest = intent[:]
	if purpose {
		response.Requirement = pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION
	}
	return securityReadResponse(ctx, a, h.now(), response)
}

func (h *SecurityConnectHandler) applyCurrentChanges(ctx context.Context, req *connect.Request[pb.ApplySecurityChangesRequest]) (*connect.Response[pb.ApplySecurityChangesResponse], error) {
	if securityRequestError(req.Msg) != nil || req.Msg.ExpectedRevision != 0 || len(req.Msg.ChangeId) != 0 || len(req.Msg.Changes) != 0 || len(req.Msg.AuthorizationProof) != 0 && len(req.Msg.AuthorizationProof) != 32 {
		return nil, currentControlError(security.ErrS1Contract)
	}
	a, producer, err := h.currentManagement(ctx)
	if err != nil {
		return nil, err
	}
	review, command, err := h.decodeCurrentReview(req.Msg.CurrentReview)
	if err != nil {
		return nil, currentControlError(err)
	}
	// Resolve an original before IdP/purpose work. New consume still checks the
	// exact prepared operation and current complete cut inside the origin owner.
	retained, _ := h.current.Inspect(ctx, review.Profile, review.ID, review.Operation.Digest())
	if retained.Progress == security.CurrentUnresolved {
		// Only a locally prepared review can be new work. A remote retry is
		// resolved by exact historical H inside Apply, without new IdP probes.
		if _, e := h.current.ReviewRequirement(ctx, a, review); e == nil {
			if err = h.validateCurrentIssuers(ctx, a, command); err != nil {
				return nil, err
			}
		}
	}
	var proof [32]byte
	copy(proof[:], req.Msg.AuthorizationProof)
	result, applyErr := h.current.Apply(ctx, review, producer, proof)
	ctx, a, err = h.currentDisclosure(ctx)
	if err != nil {
		return nil, err
	}
	if errors.Is(applyErr, security.ErrOperationAuthorization) {
		refusal := connect.NewError(connect.CodeFailedPrecondition, security.ErrOperationAuthorization)
		intent := review.Operation.Digest()
		if detail, e := boundedErrorDetail(&pb.CurrentSecurityInvocationRejected{Profile: EncodeCurrentProfile(review.Profile), ChangeId: encodeCurrentID(review.ID), IntentDigest: intent[:], PurposeRequired: true}); e == nil {
			refusal.AddDetail(detail)
		}
		return nil, refusal
	}
	if result.Profile != review.Profile {
		return nil, currentControlError(security.ErrAuthorityUnavailable)
	}
	// The versioned stage preserves ambiguity on timeout/transport/cancellation.
	// An RPC success with unresolved/durable/chosen is explicitly not Apply.
	return securityReadResponse(ctx, a, h.now(), &pb.ApplySecurityChangesResponse{CurrentResult: EncodeCurrentResult(h.current.ObserveResult(ctx, result))})
}

func (h *SecurityConnectHandler) currentChangeStatus(ctx context.Context, req *connect.Request[pb.GetSecurityChangeStatusRequest]) (*connect.Response[pb.GetSecurityChangeStatusResponse], error) {
	if securityRequestError(req.Msg) != nil || len(req.Msg.ChangeId) != 0 || len(req.Msg.CurrentIntentDigest) != 32 {
		return nil, currentControlError(security.ErrS1Contract)
	}
	if _, err := h.admission(ctx, false); err != nil {
		return nil, err
	}
	p, err := decodeCurrentProfile(req.Msg.CurrentProfile)
	if err != nil || p != h.current.Profile() {
		return nil, currentControlError(security.ErrS1Contract)
	}
	id, err := decodeCurrentID(req.Msg.CurrentChangeId)
	if err != nil {
		return nil, currentControlError(err)
	}
	result, lookupErr := h.current.Lookup(ctx, p, id, [32]byte(req.Msg.CurrentIntentDigest))
	if errors.Is(lookupErr, security.ErrChangeConflict) || result.Profile != p {
		return nil, currentControlError(security.ErrChangeConflict)
	}
	ctx, a, err := h.current.RefreshRequest(ctx)
	if err != nil {
		return nil, currentControlError(err)
	}
	if err := h.current.BindOriginalOutput(ctx, a, id, [32]byte(req.Msg.CurrentIntentDigest)); err != nil {
		return nil, currentControlError(err)
	}
	return securityReadResponse(ctx, a, h.now(), &pb.GetSecurityChangeStatusResponse{CurrentResult: EncodeCurrentResult(h.current.ObserveResult(ctx, result))})
}

func (h *SecurityConnectHandler) beginCurrentAuthorization(ctx context.Context, req *connect.Request[pb.BeginSecurityChangeAuthorizationRequest]) (*connect.Response[pb.BeginSecurityChangeAuthorizationResponse], error) {
	if securityRequestError(req.Msg) != nil || req.Msg.Review != nil {
		return nil, currentControlError(security.ErrS1Contract)
	}
	a, _, err := h.currentManagement(ctx)
	if err != nil {
		return nil, err
	}
	review, _, err := h.decodeCurrentReview(req.Msg.CurrentReview)
	if err != nil {
		return nil, currentControlError(err)
	}
	if h.currentBeginAuthorization == nil {
		return nil, currentControlError(security.ErrAuthorityUnavailable)
	}
	start, url, affinity, err := h.currentBeginAuthorization(ctx, a, review)
	if err != nil {
		return nil, currentControlError(err)
	}
	return securityReadResponse(ctx, a, h.now(), &pb.BeginSecurityChangeAuthorizationResponse{AuthorizationId: start.ID[:], StartUrl: url, ExpiresAt: timestamppb.New(start.ExpiresAt), CurrentProfile: EncodeCurrentProfile(review.Profile), AttemptAffinity: affinity})
}

func (h *SecurityConnectHandler) readCurrentAuthorization(ctx context.Context, req *connect.Request[pb.GetSecurityChangeAuthorizationRequest]) (*connect.Response[pb.GetSecurityChangeAuthorizationResponse], error) {
	if securityRequestError(req.Msg) != nil || len(req.Msg.AuthorizationId) != 32 || len(req.Msg.AttemptAffinity) > 512 {
		return nil, currentControlError(security.ErrS1Contract)
	}
	p, err := decodeCurrentProfile(req.Msg.CurrentProfile)
	if err != nil || p != h.current.Profile() {
		return nil, currentControlError(security.ErrS1Contract)
	}
	a, _, err := h.currentManagement(ctx)
	if err != nil {
		return nil, err
	}
	if h.currentReadAuthorization == nil {
		return nil, currentControlError(security.ErrAuthorityUnavailable)
	}
	status, err := h.currentReadAuthorization(ctx, a, [32]byte(req.Msg.AuthorizationId), req.Msg.AttemptAffinity)
	if err != nil {
		return nil, currentControlError(err)
	}
	state := pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_PENDING
	if status.State == security.AuthorizationApproved {
		state = pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_APPROVED
	}
	if status.State == security.AuthorizationDenied {
		state = pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_DENIED
	}
	response := &pb.GetSecurityChangeAuthorizationResponse{AuthorizationId: status.ID[:], State: state, ExpiresAt: timestamppb.New(status.ExpiresAt), CurrentProfile: EncodeCurrentProfile(p)}
	if status.State == security.AuthorizationApproved {
		response.AuthorizationProof = status.Proof[:]
	}
	return securityReadResponse(ctx, a, h.now(), response)
}
