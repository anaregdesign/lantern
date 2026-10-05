package service

import (
	"bytes"
	"context"
	"errors"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func operationAuthorizationError(err error, binding security.ManagementBinding) error {
	if !errors.Is(err, security.ErrOperationAuthorization) || errors.Is(err, security.ErrStoreUnavailable) {
		return connect.NewError(securityErrorCode(err), err)
	}
	result := connect.NewError(connect.CodeFailedPrecondition, err)
	detail, detailErr := connect.NewErrorDetail(&pb.SecurityOperationAuthorizationRequired{
		ChangeId:        append([]byte(nil), binding.ChangeID[:]...),
		ExpectedVersion: &pb.SecurityVersion{Revision: binding.ExpectedRevision, Digest: append([]byte(nil), binding.ExpectedDigest[:]...), Generation: append([]byte(nil), binding.Generation[:]...)},
		IntentDigest:    append([]byte(nil), binding.IntentDigest[:]...),
	})
	if detailErr == nil {
		result.AddDetail(detail)
	}
	return result
}

func (h *SecurityConnectHandler) managementRequest(admission *security.Admission, expected uint64, id [16]byte, changes []security.Change) security.ManagementRequest {
	return security.ManagementRequest{ExpectedRevision: expected, ChangeID: id, Actor: admission.Identity(), AuthTime: admission.AuthTime(), Now: h.now(), Clock: h.now, Changes: changes, Admission: admission}
}
func (h *SecurityConnectHandler) prepareReviewed(ctx context.Context, review *pb.SecurityChangeReview) (*security.Admission, security.PreparedManagement, error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, security.PreparedManagement{}, err
	}
	if review == nil || review.ExpectedVersion == nil || review.ExpectedVersion.Revision == 0 || len(review.ExpectedVersion.Digest) != 32 || len(review.ExpectedVersion.Generation) != 16 || len(review.ChangeId) != 16 || len(review.Changes) == 0 || len(review.Changes) > security.MaxTransactionChanges {
		return nil, security.PreparedManagement{}, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	current := securityVersion(admission.Revision())
	if !bytes.Equal(current.Generation, review.ExpectedVersion.Generation) {
		return nil, security.PreparedManagement{}, connect.NewError(connect.CodeAborted, security.ErrRevisionConflict)
	}
	var id [16]byte
	copy(id[:], review.ChangeId)
	changes := make([]security.Change, len(review.Changes))
	for i, change := range review.Changes {
		changes[i], err = decodeSecurityChange(change)
		if err != nil {
			return nil, security.PreparedManagement{}, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	prepared, err := h.store.PrepareManagement(ctx, h.managementRequest(admission, review.ExpectedVersion.Revision, id, changes))
	if err != nil {
		return nil, security.PreparedManagement{}, connect.NewError(securityErrorCode(err), err)
	}
	if !prepared.Retained && (current.Revision != review.ExpectedVersion.Revision || !bytes.Equal(current.Digest, review.ExpectedVersion.Digest)) {
		return nil, security.PreparedManagement{}, connect.NewError(connect.CodeAborted, security.ErrRevisionConflict)
	}
	return admission, prepared, nil
}
func (h *SecurityConnectHandler) PrepareSecurityChanges(ctx context.Context, req *connect.Request[pb.PrepareSecurityChangesRequest]) (*connect.Response[pb.PrepareSecurityChangesResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	admission, prepared, err := h.prepareReviewed(ctx, req.Msg.Review)
	if err != nil {
		return nil, err
	}
	requirement := pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY
	if prepared.AuthorizationRequired {
		requirement = pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION
	}
	response := &pb.PrepareSecurityChangesResponse{ExpectedVersion: req.Msg.Review.ExpectedVersion, ChangeId: append([]byte(nil), prepared.Binding.ChangeID[:]...), IntentDigest: append([]byte(nil), prepared.Binding.IntentDigest[:]...), Requirement: requirement}
	if prepared.Retained {
		generation := admission.Revision().Generation()
		proof := &pb.GetSecurityChangeStatusResponse{ChangeId: append([]byte(nil), prepared.Binding.ChangeID[:]...), Version: &pb.SecurityVersion{Revision: prepared.Result.Revision, Digest: append([]byte(nil), prepared.Result.Digest[:]...), Generation: append([]byte(nil), generation[:]...)}, Enforcement: pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING}
		if h.enforced != nil && h.enforced(prepared.Result) {
			proof.Enforcement = pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED
		}
		response.RetainedCommit = proof
	}
	return securityReadResponse(ctx, admission, h.now(), response)
}
func (h *SecurityConnectHandler) BeginSecurityChangeAuthorization(ctx context.Context, req *connect.Request[pb.BeginSecurityChangeAuthorizationRequest]) (*connect.Response[pb.BeginSecurityChangeAuthorizationResponse], error) {
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	admission, prepared, err := h.prepareReviewed(ctx, req.Msg.Review)
	if err != nil {
		return nil, err
	}
	if prepared.Retained || !prepared.AuthorizationRequired {
		return nil, connect.NewError(connect.CodeFailedPrecondition, security.ErrOperationAuthorization)
	}
	if h.beginAuthorization == nil {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	start, startURL, err := h.beginAuthorization(ctx, admission, prepared.Binding)
	if err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	return securityReadResponse(ctx, admission, h.now(), &pb.BeginSecurityChangeAuthorizationResponse{AuthorizationId: append([]byte(nil), start.ID[:]...), StartUrl: startURL, ExpiresAt: timestamppb.New(start.ExpiresAt)})
}
func (h *SecurityConnectHandler) GetSecurityChangeAuthorization(ctx context.Context, req *connect.Request[pb.GetSecurityChangeAuthorizationRequest]) (*connect.Response[pb.GetSecurityChangeAuthorizationResponse], error) {
	admission, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	if len(req.Msg.AuthorizationId) != 32 {
		return nil, connect.NewError(connect.CodeInvalidArgument, security.ErrInvalidImage)
	}
	if err := admission.CheckManagement(ctx, admission.Revision(), h.now()); err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	if h.readAuthorization == nil {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	var id [32]byte
	copy(id[:], req.Msg.AuthorizationId)
	status, err := h.readAuthorization(ctx, admission, id)
	if err != nil {
		return nil, connect.NewError(securityErrorCode(err), err)
	}
	state := pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_PENDING
	if status.State == security.AuthorizationApproved {
		state = pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_APPROVED
	}
	if status.State == security.AuthorizationDenied {
		state = pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_DENIED
	}
	response := &pb.GetSecurityChangeAuthorizationResponse{AuthorizationId: append([]byte(nil), id[:]...), State: state, ExpiresAt: timestamppb.New(status.ExpiresAt)}
	if status.State == security.AuthorizationApproved {
		response.AuthorizationProof = append([]byte(nil), status.Proof[:]...)
	}
	return securityReadResponse(ctx, admission, h.now(), response)
}
