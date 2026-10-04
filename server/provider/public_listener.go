package provider

import (
	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/grpcreflect"
	"errors"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"log/slog"
	"net"
	"net/http"
)

// NewPublicLanternListener is the production public composition. Complete peer
// replication and private policy renewal are never registered on this mux.
func NewPublicLanternListener(lis net.Listener, netCfg NetConfig, tlsCfg TLSConfig, obs ObservabilityConfig, cors CORSConfig, svc *service.LanternService, runtime *SecurityRuntime, changes ChangeConfig, val *ValidationInterceptor, rl *RateLimitInterceptor, logInt *LoggingInterceptor, met *PrometheusInterceptor, slow *SlowRPCInterceptor, hc *HealthChecker, logger *slog.Logger, certified publicSecurityCertified) (*LanternListener, error) {
	if runtime == nil || certified.runtime != runtime || certified.primary != svc {
		return nil, errors.New("public listener requires security certification")
	}
	// Reuse message/TLS/protocol configuration before installing the certified
	// mux. The listener is not serving during construction.
	withoutReflection := obs
	withoutReflection.EnableReflection = false
	listener, err := NewLanternListener(lis, netCfg, tlsCfg, withoutReflection, CORSConfig{}, svc, nil, val, rl, nil, logInt, met, slow, hc, logger)
	if err != nil {
		return nil, err
	}
	var authentication connect.Interceptor
	if runtime.mode == "oidc" {
		authentication = runtime.PublicAuthenticationInterceptor()
	}
	publicOptions := authenticatedHandlerOptions(netCfg, val, nil, authentication, logInt, met, slow, logger)
	browserOptions := authenticatedHandlerOptions(netCfg, val, nil, nil, logInt, met, slow, logger)
	mux := http.NewServeMux()
	path, handler, err := runtime.PublicDataHTTPHandler(svc, publicOptions...)
	if err != nil {
		return nil, err
	}
	mux.Handle(path, handler)
	path, handler, err = runtime.BrowserDataHTTPHandler(svc, browserOptions...)
	if err != nil {
		return nil, err
	}
	mux.Handle(path, handler)
	mux.Handle(runtime.PublicControlHTTPHandler(publicOptions...))
	mux.Handle(runtime.BrowserControlHTTPHandler(browserOptions...))
	mux.Handle("/auth/", runtime.AuthHTTPHandler())
	if changes.Enabled {
		path, handler, err = runtime.PublicChangeHTTPHandler(svc, changes.Options, browserOptions...)
		if err != nil {
			return nil, err
		}
		mux.Handle(path, handler)
		path, handler, err = runtime.BrowserChangeHTTPHandler(svc, changes.Options, browserOptions...)
		if err != nil {
			return nil, err
		}
		mux.Handle(path, handler)
	}
	mux.Handle(grpchealth.NewHandler(publicHealthChecker{hc.Inner(), runtime}))
	if obs.EnableReflection {
		names := []string{graphv1connect.LanternServiceName, graphv1connect.LanternSecurityServiceName, grpchealth.HealthV1ServiceName}
		if changes.Enabled {
			names = append(names, graphv1connect.LanternChangeServiceName)
		}
		reflector := grpcreflect.NewStaticReflector(names...)
		path, handler := grpcreflect.NewHandlerV1(reflector)
		mux.Handle(path, runtime.requireGlobalHTTP(security.SchemaRead, handler))
		path, handler = grpcreflect.NewHandlerV1Alpha(reflector)
		mux.Handle(path, runtime.requireGlobalHTTP(security.SchemaRead, handler))
	}
	listener.server.Handler = otelhttp.NewHandler(runtime.publicIngressHandler(CORSMiddleware(cors)(publicRateLimit(mux, rl))), "lantern", otelhttp.WithSpanNameFormatter(func(_ string, req *http.Request) string { return req.URL.Path }))
	return listener, nil
}
