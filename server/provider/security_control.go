package provider

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

// PublicControlHTTPHandler always installs strict JSON decoding and verified
// bearer admission. Browser session routes use a distinct authenticated mount.
func (r *SecurityRuntime) PublicControlHTTPHandler(extra ...connect.HandlerOption) (string, http.Handler) {
	options := securitytransport.SecurityHandlerOptions()
	options = append(options, extra...)
	options = append(options, connect.WithInterceptors(r.PublicAuthenticationInterceptor()))
	return r.publicRPCHandler("/"+graphv1connect.LanternSecurityServiceName+"/", func(_ string, options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternSecurityServiceHandler(r.control, options...)
	}, options...)
}

func (r *SecurityRuntime) NativeAuthHTTPHandler(extra ...connect.HandlerOption) (string, http.Handler) {
	options := append([]connect.HandlerOption{}, extra...)
	options = append(options, securitytransport.NativeHandlerOptions()...)
	options = append(options, connect.WithInterceptors(r.PublicAuthenticationInterceptor()))
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternNativeAuthServiceName+"/", func(_ string, options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternNativeAuthServiceHandler(r.authentication, options...)
	}, options...)
	return path, r.authentication.NativeRPCHandler(handler)
}

func (r *SecurityRuntime) BrowserControlHTTPHandler(extra ...connect.HandlerOption) (string, http.Handler) {
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternSecurityServiceName+"/", func(_ string, options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternSecurityServiceHandler(r.control, options...)
	}, append(securitytransport.SecurityHandlerOptions(), extra...)...)
	return "/browser" + path, r.BrowserRPCHandler(path, handler)
}
