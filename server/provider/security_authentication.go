package provider

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
)

func (r *SecurityRuntime) loginResources() authn.OIDCResources {
	return authn.OIDCResources{Fetcher: r.fetcher, Secrets: r.secrets, Verifier: r.verifier, Credentials: r.credentials, Logins: r.logins, NativeLogins: r.nativeLogins, Now: r.now}
}
func (r *SecurityRuntime) composeAuthentication() {
	r.authentication = service.NewAuthenticationAdapter(service.AuthenticationOptions{
		Credentials: service.NewCredentialAdapter(r.mode, service.BrowserConfig{BrowserOrigin: r.config.BrowserOrigin, TrustedProxyIPs: r.config.TrustedProxyIPs}, r.credentials),
		Workflow:    r.workflow, Current: r.current, Now: r.now,
		Ready: r.Ready, Output: func() *security.CurrentOutput { return r.output }, AttemptPrefix: r.currentAttemptPrefix(),
	})
}
func (r *SecurityRuntime) AuthenticateBearer(ctx context.Context, headers http.Header) (context.Context, error) {
	if r == nil {
		return ctx, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	return r.authentication.AuthenticateBearer(ctx, headers)
}
func (r *SecurityRuntime) PublicAuthenticationInterceptor() connect.Interceptor {
	return r.authentication.PublicAuthenticationInterceptor()
}
func (r *SecurityRuntime) AuthHTTPHandler() http.Handler { return r.authentication.AuthHTTPHandler() }
func (r *SecurityRuntime) BrowserRPCHandler(path string, handler http.Handler) http.Handler {
	return r.authentication.BrowserRPCHandler(path, handler)
}
