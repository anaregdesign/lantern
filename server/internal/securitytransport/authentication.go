package securitytransport

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// AuthenticateBearer ignores browser cookies and identity/Role headers. A
// bounded unverified Issuer is only a selector into current registered state.
type verifiedRuntimeKey struct{}

func (r *AuthenticationAdapter) AuthenticateBearer(ctx context.Context, headers http.Header) (context.Context, error) {
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
	if r.current != nil {
		p, err := r.CurrentBearerProducer(headers)
		if err != nil {
			return ctx, connect.NewError(connect.CodeUnauthenticated, oidc.ErrInvalidToken)
		}
		verified, _, err := r.current.WithRequestCredential(ctx, p)
		if err != nil {
			code := connect.CodeUnauthenticated
			if errors.Is(err, security.ErrAuthorityUnavailable) {
				code = connect.CodeUnavailable
			}
			return ctx, connect.NewError(code, errors.New("current authentication unavailable"))
		}
		return context.WithValue(verified, verifiedRuntimeKey{}, r), nil
	}
	return ctx, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
}

// PublicAuthenticationInterceptor is solely for the public bearer surface.
// Private peers use separate workload admission; browser mounts use the opaque
// session boundary. Only the declared capability method is anonymous here.
func (r *AuthenticationAdapter) PublicAuthenticationInterceptor() connect.Interceptor {
	return securityAuthenticationInterceptor{runtime: r}
}

type securityAuthenticationInterceptor struct{ runtime *AuthenticationAdapter }

func (i securityAuthenticationInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().Procedure == graphv1connect.LanternSecurityServiceGetAuthCapabilitiesProcedure {
			return next(ctx, req)
		}
		if nativeBootstrapProcedure(req.Spec().Procedure) {
			return next(ctx, req)
		}
		if nativeAuthProcedure(req.Spec().Procedure) && (len(req.Header().Values("Origin")) != 0 || len(req.Header().Values("Cookie")) != 0) {
			return nil, nativeError(authn.ErrUnauthenticated)
		}
		if !strings.HasPrefix(req.Spec().Procedure, "/graph.v1.LanternService/") && !strings.HasPrefix(req.Spec().Procedure, "/graph.v1.LanternSecurityService/") && !nativeAuthProcedure(req.Spec().Procedure) {
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
		return next(verified, conn)
	}
}
