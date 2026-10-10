package provider

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

// PublicControlHTTPHandler always installs strict JSON decoding and verified
// bearer admission. Browser session routes use a distinct authenticated mount.
func (r *SecurityRuntime) PublicControlHTTPHandler(extra ...connect.HandlerOption) (string, http.Handler) {
	options := service.SecurityHandlerOptions()
	options = append(options, extra...)
	options = append(options, connect.WithInterceptors(r.PublicAuthenticationInterceptor()))
	return r.publicRPCHandler("/"+graphv1connect.LanternSecurityServiceName+"/", func(options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternSecurityServiceHandler(r.control, options...)
	}, options...)
}

func (r *SecurityRuntime) BrowserControlHTTPHandler(extra ...connect.HandlerOption) (string, http.Handler) {
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternSecurityServiceName+"/", func(options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternSecurityServiceHandler(r.control, options...)
	}, append(service.SecurityHandlerOptions(), extra...)...)
	return "/browser" + path, r.BrowserRPCHandler(path, handler)
}
