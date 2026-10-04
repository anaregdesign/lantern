package provider

import (
	"github.com/prometheus/client_golang/prometheus"
	"io"
	"log/slog"
	"testing"
)

func TestProtectedMetricsRequiresLocalOperatorBoundary(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	runtime := &SecurityRuntime{mode: "oidc", data: data}
	cert := runtimeCertified{valid: true, runtime: data}
	public := publicSecurityCertified{runtime: runtime}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, config := range []ObservabilityConfig{
		{MetricsAddr: ":9090"}, {MetricsAddr: "0.0.0.0:9090"}, {MetricsAddr: "localhost:9090"}, {EnablePprof: true},
	} {
		if _, err := NewServingMetricsServer(config, prometheus.NewRegistry(), nil, logger, cert, public); err == nil {
			t.Fatal("unprotected diagnostic listener accepted", config)
		}
	}
	if _, err := NewServingMetricsServer(ObservabilityConfig{MetricsAddr: "127.0.0.1:9090"}, prometheus.NewRegistry(), nil, logger, cert, public); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServingMetricsServer(ObservabilityConfig{}, prometheus.NewRegistry(), nil, logger, runtimeCertified{}, public); err == nil {
		t.Fatal("uncertified metrics accepted")
	}
}
