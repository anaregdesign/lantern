package provider

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestProtectedMetricsExposition(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	runtime := &SecurityRuntime{mode: "oidc", data: data}
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "lantern_test_calls_total", Help: "test requests"}, []string{"result"})
	registry.MustRegister(counter)
	const label = "成功\"\\\n"
	counter.WithLabelValues(label).Add(2)
	server, err := NewServingMetricsServer(ObservabilityConfig{MetricsAddr: "127.0.0.1:0"}, registry, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), runtimeCertified{valid: true, runtime: data}, publicSecurityCertified{runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewServer(server.(*httpMetricsServer).srv.Handler)
	defer endpoint.Close()
	resp, err := endpoint.Client().Get(endpoint.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %s", resp.Status)
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	family := families["lantern_test_calls_total"]
	if family == nil || len(family.Metric) != 1 || family.Metric[0].GetCounter().GetValue() != 2 || len(family.Metric[0].Label) != 1 || family.Metric[0].Label[0].GetValue() != label {
		t.Fatalf("metrics exposition did not round-trip: %v", family)
	}
}
