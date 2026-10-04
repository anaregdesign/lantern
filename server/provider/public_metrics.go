package provider

import (
	"errors"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
)

// Protected diagnostics bind only an operator-local socket. The same-origin
// gateway must authorize /metrics with /auth/operations before proxying it.
// Runtime profiling is disabled in OIDC mode because it can expose secrets.
func NewServingMetricsServer(config ObservabilityConfig, registry *prometheus.Registry, gate *readiness.Gate, logger *slog.Logger, certified runtimeCertified, public publicSecurityCertified) (MetricsServer, error) {
	runtime := public.runtime
	if !certified.valid || runtime == nil || runtime.data != certified.runtime {
		return nil, errors.New("metrics requires the exact certified security runtime")
	}
	if runtime.mode == "oidc" {
		if config.EnablePprof {
			return nil, errors.New("runtime profiling is disabled in protected mode")
		}
		if config.MetricsAddr != "" {
			host, _, err := net.SplitHostPort(config.MetricsAddr)
			addr, parseErr := netip.ParseAddr(host)
			if err != nil || parseErr != nil || !addr.IsLoopback() || addr.Zone() != "" {
				return nil, errors.New("protected metrics requires an exact loopback address")
			}
		}
	}
	server := NewMetricsServer(config, registry, gate, logger, certified)
	if bound, ok := server.(*httpMetricsServer); ok {
		handler := bound.srv.Handler
		bound.srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if (req.URL.Path == "/readyz" || req.URL.Path == "/healthz/ready") && !runtime.Ready(req.Context()) {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
			handler.ServeHTTP(w, req)
		})
	}
	return server, nil
}
