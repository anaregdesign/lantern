package provider

import (
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/service"
)

func (r *SecurityRuntime) protectDataService(svc *service.LanternService) error {
	if r == nil || svc == nil {
		return errors.New("missing data/security runtime")
	}
	if r.mode == "oidc" {
		if !svc.SharesServingRuntime(r.data) || svc.DataNamespaceFormat() != keyspace.Version {
			return errors.New("data service differs from security-owned namespace runtime")
		}
		svc.WithDataAuthorization(r.now)
	}
	return nil
}

// These constructors retain caller-owned limits/observability while making
// admission and the logical-key Role boundary mandatory on protected data.
func (r *SecurityRuntime) PublicDataHTTPHandler(svc *service.LanternService, options ...connect.HandlerOption) (string, http.Handler, error) {
	if err := r.protectDataService(svc); err != nil {
		return "", nil, err
	}
	options = append(options, service.StrictJSONHandlerOption(), connect.WithInterceptors(r.PublicAuthenticationInterceptor()))
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternServiceName+"/", func(options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternServiceHandler(service.NewLanternServiceConnectHandler(svc), options...)
	}, options...)
	return path, r.boundSnapshotHTTPHandler(handler), nil
}
func (r *SecurityRuntime) BrowserDataHTTPHandler(svc *service.LanternService, options ...connect.HandlerOption) (string, http.Handler, error) {
	if err := r.protectDataService(svc); err != nil {
		return "", nil, err
	}
	options = append(options, service.StrictJSONHandlerOption())
	path, handler := r.publicRPCHandler("/"+graphv1connect.LanternServiceName+"/", func(options ...connect.HandlerOption) (string, http.Handler) {
		return graphv1connect.NewLanternServiceHandler(service.NewLanternServiceConnectHandler(svc), options...)
	}, options...)
	return "/browser" + path, r.boundSnapshotHTTPHandler(r.BrowserRPCHandler(path, handler)), nil
}
