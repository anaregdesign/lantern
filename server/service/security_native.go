package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ graphv1connect.LanternNativeAuthServiceHandler = (*AuthenticationAdapter)(nil)

const NativeAuthMaxBytes = 64 << 10

// NativeAffinityHeader selects a process behind the configured endpoint. It is
// never an authentication credential or a destination URL.
const NativeAffinityHeader = "Lantern-Native-Affinity"

func nativeAuthProcedure(path string) bool {
	return strings.HasPrefix(path, "/"+graphv1connect.LanternNativeAuthServiceName+"/")
}
func nativeBootstrapProcedure(path string) bool {
	switch path {
	case graphv1connect.LanternNativeAuthServiceBeginNativeLoginProcedure, graphv1connect.LanternNativeAuthServiceCompleteNativeLoginProcedure,
		graphv1connect.LanternNativeAuthServiceBeginNativeRegistrationProcedure, graphv1connect.LanternNativeAuthServicePrepareNativeRegistrationProcedure,
		graphv1connect.LanternNativeAuthServiceApplyNativeRegistrationProcedure, graphv1connect.LanternNativeAuthServiceGetNativeRegistrationStatusProcedure:
		return true
	default:
		return false
	}
}

func (r *AuthenticationAdapter) nativeRequest(message proto.Message, headers http.Header, bootstrap bool) error {
	if r == nil || r.workflow == nil || !r.workflow.NativeEnabled() {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("native login requires current-v2"))
	}
	if securityRequestError(message) != nil || proto.Size(message) > NativeAuthMaxBytes {
		return nativeError(authn.ErrInvalidInput)
	}
	if len(headers.Values("Origin")) != 0 || len(headers.Values("Cookie")) != 0 || bootstrap && len(headers.Values("Authorization")) != 0 {
		return nativeError(authn.ErrUnauthenticated)
	}
	return nil
}

// NativeRPCHandler owns the complete HTTP call's deadline, including response
// output. A workflow cannot cancel its returned admission context prematurely.
func (r *AuthenticationAdapter) NativeRPCHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if req.Method != http.MethodPost || req.URL.RawQuery != "" || req.URL.ForceQuery || req.URL.RawPath != "" || req.ContentLength > NativeAuthMaxBytes ||
			req.Header.Get("Content-Encoding") != "" || req.Header.Get("Connect-Content-Encoding") != "" || req.Header.Get("Grpc-Encoding") != "" && req.Header.Get("Grpc-Encoding") != "identity" {
			WritePublicAdmissionError(w, req, connect.CodeInvalidArgument)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func (r *AuthenticationAdapter) BeginNativeLogin(ctx context.Context, req *connect.Request[pb.BeginNativeLoginRequest]) (*connect.Response[pb.BeginNativeLoginResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), true); err != nil {
		return nil, err
	}
	start, err := r.workflow.BeginNativeLogin(ctx, req.Msg.Issuer, req.Msg.ClientProfileId)
	if err != nil {
		return nil, nativeError(err)
	}
	return connect.NewResponse(encodeNativeStart(start)), nil
}

func (r *AuthenticationAdapter) CompleteNativeLogin(ctx context.Context, req *connect.Request[pb.CompleteNativeLoginRequest]) (*connect.Response[pb.CompleteNativeLoginResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), true); err != nil {
		return nil, err
	}
	affinity, err := nativeAffinity(req.Header())
	if err != nil {
		return nil, nativeError(authn.ErrInvalidInput)
	}
	input := authn.NativeCompletionInput{AttemptID: req.Msg.AttemptId, CompletionSecret: req.Msg.CompletionSecret, Affinity: affinity}
	switch provider := req.Msg.Provider.(type) {
	case *pb.CompleteNativeLoginRequest_Google:
		if provider.Google == nil {
			return nil, nativeError(authn.ErrInvalidInput)
		}
		input.Provider = oidc.NativeInput{Provider: "google", IDToken: provider.Google.IdToken}
	case *pb.CompleteNativeLoginRequest_Apple:
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
	result, err := r.workflow.CompleteNativeLogin(ctx, input)
	if err != nil {
		return nil, nativeError(err)
	}
	a, known := security.AdmissionFromContext(result.Context)
	if !known {
		return nil, nativeError(authn.ErrUnavailable)
	}
	return connect.NewResponse(&pb.CompleteNativeLoginResponse{Credential: result.Credential, Session: encodeNativeSession(result.Session, a)}), nil
}

func validNativeCompletionInput(input authn.NativeCompletionInput) bool {
	return len(input.AttemptID) == 43 && len(input.CompletionSecret) == 43 && input.Provider.IDToken != "" && len(input.Provider.IDToken) <= 16<<10 && len(input.Provider.AuthorizationCode) <= 4096 && len(input.Provider.State) <= oidc.MaxNativeStateBytes
}

func (r *AuthenticationAdapter) nativeAdmission(ctx context.Context) (*security.Admission, security.Session, error) {
	a, known := security.AdmissionFromContext(ctx)
	if !known || a.Authentication().Provenance != security.NativeEndUserSession || a.Browser() {
		return nil, security.Session{}, nativeError(authn.ErrUnauthenticated)
	}
	if err := a.Check(ctx, r.now()); err != nil {
		return nil, security.Session{}, nativeError(err)
	}
	s, known := a.Snapshot().Session(a.Authentication().SessionDigest)
	if !known || s.CredentialProfile() != security.NativeEndUserProfile {
		return nil, security.Session{}, nativeError(authn.ErrUnauthenticated)
	}
	return a, s, nil
}

func encodeNativeSession(s security.Session, a *security.Admission) *pb.NativeSession {
	result := &pb.NativeSession{CredentialProfile: s.CredentialProfile(), ClientProfileId: s.ClientProfileID, ServiceOrigin: s.ServiceOrigin, CreatedAt: timestamppb.New(s.CreatedAt), ExpiresAt: timestamppb.New(s.ExpiresAt), Identity: encodeSecurityIdentity(s.Identity), Version: SecurityVersionForAdmission(a)}
	if a != nil && a.Identity() == s.Identity {
		if id, prefix, present := a.Snapshot().PersonalArea(s.Identity); present {
			result.UserId, result.PersonalPrefix = &id, &prefix
		}
	}
	if !s.AuthTime.IsZero() {
		result.AuthTime = timestamppb.New(s.AuthTime)
	}
	return result
}

func (r *AuthenticationAdapter) GetNativeSession(ctx context.Context, req *connect.Request[pb.GetNativeSessionRequest]) (*connect.Response[pb.GetNativeSessionResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), false); err != nil {
		return nil, err
	}
	a, s, err := r.nativeAdmission(ctx)
	if err != nil {
		return nil, err
	}
	view, err := authn.InspectSession(ctx, a, nil, r.current, r.now)
	if err != nil {
		return nil, nativeError(err)
	}
	return connect.NewResponse(&pb.GetNativeSessionResponse{Session: encodeNativeSession(s, a), Roles: encodeSessionView(view).Roles}), nil
}

func (r *AuthenticationAdapter) PrepareNativeLogout(ctx context.Context, req *connect.Request[pb.PrepareNativeLogoutRequest]) (*connect.Response[pb.PrepareNativeLogoutResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), false); err != nil {
		return nil, err
	}
	a, _, err := r.nativeAdmission(ctx)
	if err != nil {
		return nil, err
	}
	review, err := r.workflow.PrepareNativeLogout(ctx, a)
	if err != nil {
		return nil, nativeError(err)
	}
	wire, err := EncodeCurrentSessionReview(review)
	if err != nil || a.BindCurrentSelfOutput(ctx) != nil {
		return nil, nativeError(authn.ErrUnavailable)
	}
	return connect.NewResponse(&pb.PrepareNativeLogoutResponse{Review: wire}), nil
}

func (r *AuthenticationAdapter) ApplyNativeLogout(ctx context.Context, req *connect.Request[pb.ApplyNativeLogoutRequest]) (*connect.Response[pb.ApplyNativeLogoutResponse], error) {
	if err := r.nativeRequest(req.Msg, req.Header(), false); err != nil {
		return nil, err
	}
	a, s, err := r.nativeAdmission(ctx)
	if err != nil {
		return nil, err
	}
	review, err := DecodeCurrentSessionReview(r.current, req.Msg.Review)
	if err != nil || req.Msg.Review.SessionDigest != s.Digest {
		return nil, nativeError(authn.ErrInvalidInput)
	}
	result, err := r.workflow.ApplyNativeLogout(ctx, a, review)
	if err != nil {
		return nil, nativeError(err)
	}
	response := &pb.ApplyNativeLogoutResponse{ClearLocalSession: true}
	if result != nil {
		response.CurrentResult = EncodeCurrentResult(*result)
	}
	return connect.NewResponse(response), nil
}

func nativeError(err error) error {
	code := connect.CodeUnauthenticated
	switch {
	case errors.Is(err, authn.ErrInvalidInput):
		code = connect.CodeInvalidArgument
	case errors.Is(err, oidc.ErrNativeCapacity), errors.Is(err, security.ErrControlReserve):
		code = connect.CodeResourceExhausted
	case errors.Is(err, authn.ErrUnavailable), errors.Is(err, security.ErrAuthorityUnavailable), errors.Is(err, oidc.ErrFetch), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code = connect.CodeUnavailable
	}
	return connect.NewError(code, errors.New("native authentication request rejected"))
}

func nativeAffinity(headers http.Header) (string, error) {
	hints := headers.Values(NativeAffinityHeader)
	if len(hints) > 1 || len(hints) == 1 && (hints[0] == "" || len(hints[0]) > 57) {
		return "", authn.ErrInvalidInput
	}
	return headers.Get(NativeAffinityHeader), nil
}

func encodeNativeStart(start oidc.NativeStart) *pb.BeginNativeLoginResponse {
	return &pb.BeginNativeLoginResponse{AttemptId: start.AttemptID, CompletionSecret: start.CompletionSecret, Affinity: start.Affinity, ExpiresAt: timestamppb.New(start.ExpiresAt),
		Configuration: &pb.NativePluginConfiguration{Kind: encodeNativeKind(start.Kind), ClientProfileId: start.ClientProfileID, ClientId: start.ClientID, ServiceOrigin: start.ServiceOrigin, RedirectUri: start.RedirectURI, State: start.State, Nonce: start.Nonce}}
}
