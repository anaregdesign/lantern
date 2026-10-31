package securitytransport

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (r *AuthenticationAdapter) registrationRecovery(review *pb.NativeRegistrationReview) (*security.CurrentReview, error) {
	if review == nil {
		return nil, nil
	}
	decoded, err := decodeRegistrationReview(r.current, review)
	if err != nil {
		return nil, nativeError(authn.ErrInvalidInput)
	}
	return &decoded, nil
}

func (r *AuthenticationAdapter) BeginNativeRegistration(ctx context.Context, req *connect.Request[pb.BeginNativeRegistrationRequest]) (*connect.Response[pb.BeginNativeRegistrationResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), true); err != nil {
		return nil, err
	}
	recovery, err := r.registrationRecovery(req.Msg.RecoveryReview)
	if err != nil {
		return nil, err
	}
	start, err := r.workflow.BeginNativeRegistration(ctx, req.Msg.Issuer, req.Msg.ClientProfileId, recovery)
	if err != nil {
		return nil, nativeError(err)
	}
	return connect.NewResponse(&pb.BeginNativeRegistrationResponse{Attempt: encodeNativeStart(start)}), nil
}

func (r *AuthenticationAdapter) PrepareNativeRegistration(ctx context.Context, req *connect.Request[pb.PrepareNativeRegistrationRequest]) (*connect.Response[pb.PrepareNativeRegistrationResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), true); err != nil {
		return nil, err
	}
	affinity, err := nativeAffinity(req.Header())
	if err != nil {
		return nil, nativeError(err)
	}
	recovery, err := r.registrationRecovery(req.Msg.RecoveryReview)
	if err != nil {
		return nil, err
	}
	input := authn.NativeCompletionInput{AttemptID: req.Msg.AttemptId, CompletionSecret: req.Msg.CompletionSecret, Affinity: affinity}
	switch provider := req.Msg.Provider.(type) {
	case *pb.PrepareNativeRegistrationRequest_Google:
		if provider.Google == nil {
			return nil, nativeError(authn.ErrInvalidInput)
		}
		input.Provider = oidc.NativeInput{Provider: "google", IDToken: provider.Google.IdToken}
	case *pb.PrepareNativeRegistrationRequest_Apple:
		if provider.Apple == nil || provider.Apple.AuthorizationCode == "" || provider.Apple.State == "" {
			return nil, nativeError(authn.ErrInvalidInput)
		}
		input.Provider = oidc.NativeInput{Provider: "apple", IDToken: provider.Apple.IdentityToken, AuthorizationCode: provider.Apple.AuthorizationCode, State: provider.Apple.State}
	default:
		return nil, nativeError(authn.ErrInvalidInput)
	}
	if !validNativeCompletionInput(input) {
		return nil, nativeError(authn.ErrInvalidInput)
	}
	prepared, err := r.workflow.PrepareNativeRegistration(ctx, input, recovery)
	if err != nil {
		return nil, nativeError(err)
	}
	review, err := encodeRegistrationReview(prepared.Review)
	if err != nil {
		return nil, nativeError(authn.ErrUnavailable)
	}
	return connect.NewResponse(&pb.PrepareNativeRegistrationResponse{Review: review, Proof: prepared.Proof, ExpiresAt: timestamppb.New(prepared.ExpiresAt), Affinity: prepared.Affinity}), nil
}

func (r *AuthenticationAdapter) registrationRequest(review *pb.NativeRegistrationReview, proof string, headers http.Header) (authn.NativeRegistrationRequest, error) {
	affinity, err := nativeAffinity(headers)
	if err != nil || len(proof) != 43 {
		return authn.NativeRegistrationRequest{}, nativeError(authn.ErrInvalidInput)
	}
	decoded, err := decodeRegistrationReview(r.current, review)
	if err != nil {
		return authn.NativeRegistrationRequest{}, nativeError(authn.ErrInvalidInput)
	}
	return authn.NativeRegistrationRequest{Review: decoded, Proof: proof, Affinity: affinity}, nil
}

func (r *AuthenticationAdapter) ApplyNativeRegistration(ctx context.Context, req *connect.Request[pb.ApplyNativeRegistrationRequest]) (*connect.Response[pb.ApplyNativeRegistrationResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), true); err != nil {
		return nil, err
	}
	input, err := r.registrationRequest(req.Msg.Review, req.Msg.Proof, req.Header())
	if err != nil {
		return nil, err
	}
	result, err := r.workflow.ApplyNativeRegistration(ctx, input)
	if err != nil {
		return nil, nativeError(err)
	}
	response := &pb.ApplyNativeRegistrationResponse{CurrentResult: EncodeCurrentResult(result.Status)}
	if result.Session != nil {
		a, known := security.AdmissionFromContext(result.Session.Context)
		if !known || result.Session.Context.Err() != nil {
			return nil, nativeError(authn.ErrUnavailable)
		}
		response.Credential, response.Session = result.Session.Credential, encodeNativeSession(result.Session.Session, a)
	} else {
		response.SignInRequired = result.Status.Original != nil && result.Status.Original.Disposition() == security.S1Applied
	}
	return connect.NewResponse(response), nil
}

func (r *AuthenticationAdapter) GetNativeRegistrationStatus(ctx context.Context, req *connect.Request[pb.GetNativeRegistrationStatusRequest]) (*connect.Response[pb.GetNativeRegistrationStatusResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), true); err != nil {
		return nil, err
	}
	input, err := r.registrationRequest(req.Msg.Review, req.Msg.Proof, req.Header())
	if err != nil {
		return nil, err
	}
	result, err := r.workflow.GetNativeRegistrationStatus(ctx, input)
	if err != nil {
		return nil, nativeError(err)
	}
	return connect.NewResponse(&pb.GetNativeRegistrationStatusResponse{CurrentResult: EncodeCurrentResult(result.Status)}), nil
}

func (h *SecurityConnectHandler) GetRegistrationPolicy(ctx context.Context, req *connect.Request[pb.GetRegistrationPolicyRequest]) (*connect.Response[pb.GetRegistrationPolicyResponse], error) {
	a, err := h.admission(ctx, true)
	if err != nil {
		return nil, err
	}
	if err := securityRequestError(req.Msg); err != nil {
		return nil, err
	}
	return securityReadResponse(ctx, a, h.now(), &pb.GetRegistrationPolicyResponse{Policy: encodeRegistrationPolicy(a.Snapshot().Image().RegistrationPolicy), Version: SecurityVersionForAdmission(a)})
}
