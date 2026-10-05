package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// AuthenticateBearer ignores browser cookies and identity/Role headers. A
// bounded unverified Issuer is only a selector into current registered state.
type verifiedRuntimeKey struct{}

func (r *SecurityRuntime) AuthenticateBearer(ctx context.Context, headers http.Header) (context.Context, error) {
	if r == nil {
		return ctx, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	if r.mode == "off" {
		return ctx, nil
	}
	if ctx.Value(verifiedRuntimeKey{}) == r {
		admission, known := security.AdmissionFromContext(ctx)
		if !known || admission.Check(ctx, r.now()) != nil {
			return ctx, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
		}
		return ctx, nil
	}
	values := headers.Values("Authorization")
	if len(values) != 1 || len(values[0]) > 16<<10+7 {
		return ctx, connect.NewError(connect.CodeUnauthenticated, oidc.ErrInvalidToken)
	}
	scheme, raw, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return ctx, connect.NewError(connect.CodeUnauthenticated, oidc.ErrInvalidToken)
	}
	revision, known := r.native.Store().Current()
	if !known {
		return ctx, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	if err := r.authorityCheck(ctx, revision); err != nil {
		return ctx, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	var identity security.Identity
	var authTime, credentialExpiry time.Time
	authentication := security.Authentication{Provenance: security.NativeMachine, Class: security.MachineActor}
	if strings.HasPrefix(raw, security.MachineTokenPrefix) {
		var valid bool
		identity, credentialExpiry, valid = revision.Snapshot().MachineAccess(raw, r.now())
		if !valid {
			return ctx, connect.NewError(connect.CodeUnauthenticated, oidc.ErrInvalidToken)
		}
		// Machines cannot claim an interactive recent-authentication event.
	} else {
		issuerURL, err := oidc.TokenIssuer(raw)
		if err != nil {
			return ctx, connect.NewError(connect.CodeUnauthenticated, oidc.ErrInvalidToken)
		}
		issuer, registered := revision.Snapshot().Issuer(issuerURL)
		if !registered || !issuer.Enabled || issuer.ConfigRevision == 0 {
			return ctx, connect.NewError(connect.CodeUnauthenticated, oidc.ErrInvalidToken)
		}
		verified, err := r.verifier.VerifyAccess(ctx, raw, oidc.Trust{Issuer: issuer, Generation: revision.Generation(), ConfigRevision: issuer.ConfigRevision})
		if err != nil {
			return ctx, connect.NewError(connect.CodeUnauthenticated, oidc.ErrInvalidToken)
		}
		identity, authTime, credentialExpiry = verified.Identity, verified.AuthTime, verified.ExpiresAt
		authentication = security.Authentication{Provenance: security.RFC9068Bearer, Class: revision.Snapshot().BearerActor(identity), IssuerConfigRevision: issuer.ConfigRevision}
	}
	expiry := minSecurityTime(credentialExpiry, r.now().Add(28*time.Second))
	if r.receiver != nil {
		expiry = minSecurityTime(expiry, r.receiver.Expiry())
	}
	admission, err := security.NewAdmission(identity, authTime, expiry, revision, r.authorityCheck)
	if err != nil {
		return ctx, connect.NewError(connect.CodePermissionDenied, security.ErrPermissionDenied)
	}
	if err := admission.Check(ctx, r.now()); err != nil {
		return ctx, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	admission = admission.WithAuthentication(authentication)
	return context.WithValue(security.WithAdmission(ctx, admission), verifiedRuntimeKey{}, r), nil
}
func minSecurityTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// PublicAuthenticationInterceptor is solely for the public bearer surface.
// Private peers use separate workload admission; browser mounts use the opaque
// session boundary. Only the declared capability method is anonymous here.
func (r *SecurityRuntime) PublicAuthenticationInterceptor() connect.Interceptor {
	return securityAuthenticationInterceptor{runtime: r}
}

type securityAuthenticationInterceptor struct{ runtime *SecurityRuntime }

func (i securityAuthenticationInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().Procedure == graphv1connect.LanternSecurityServiceGetAuthCapabilitiesProcedure {
			return next(ctx, req)
		}
		if !strings.HasPrefix(req.Spec().Procedure, "/graph.v1.LanternService/") && !strings.HasPrefix(req.Spec().Procedure, "/graph.v1.LanternSecurityService/") {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("private service requires workload admission"))
		}
		verified, err := i.runtime.AuthenticateBearer(ctx, req.Header())
		if err != nil {
			return nil, err
		}
		return next(verified, req)
	}
}
func (i securityAuthenticationInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}
func (i securityAuthenticationInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		verified, err := i.runtime.AuthenticateBearer(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}
		if !strings.HasPrefix(conn.Spec().Procedure, "/graph.v1.LanternService/") {
			return connect.NewError(connect.CodePermissionDenied, errors.New("private service requires workload admission"))
		}
		verified, err = i.runtime.boundSnapshotAdmission(verified)
		if err != nil {
			return connect.NewError(connect.CodeUnavailable, err)
		}
		return next(verified, conn)
	}
}
