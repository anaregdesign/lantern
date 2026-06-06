package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/prometheus/client_golang/prometheus"
)

// TestPprof_DisabledByDefault confirms /debug/pprof/* is not reachable when
// EnablePprof is false. This is the on-by-default-on-prod safety guarantee.
func TestPprof_DisabledByDefault(t *testing.T) {
	gate := readiness.NewGate(10000, false, NewHealthChecker())
	ts := httptest.NewServer(newMetricsMux(prometheus.NewRegistry(), gate, false))
	defer ts.Close()

	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s (disabled): want 404, got %d", path, resp.StatusCode)
		}
	}
}

// TestPprof_EnabledServesIndexAndProfiles confirms that the index page and
// each registered runtime profile respond with 2xx when EnablePprof is true.
// We deliberately skip /debug/pprof/profile and /debug/pprof/trace because
// those block for a sampling window.
func TestPprof_EnabledServesIndexAndProfiles(t *testing.T) {
	gate := readiness.NewGate(10000, false, NewHealthChecker())
	ts := httptest.NewServer(newMetricsMux(prometheus.NewRegistry(), gate, true))
	defer ts.Close()

	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/heap",
		"/debug/pprof/goroutine",
		"/debug/pprof/allocs",
		"/debug/pprof/threadcreate",
		"/debug/pprof/block",
		"/debug/pprof/mutex",
		"/debug/pprof/cmdline",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			t.Fatalf("GET %s (enabled): want 2xx, got %d", path, resp.StatusCode)
		}
	}
}

// TestPprof_EnabledKeepsMetricsAndReadyz makes sure mounting the pprof
// handlers does not collide with the pre-existing routes on the same mux.
func TestPprof_EnabledKeepsMetricsAndReadyz(t *testing.T) {
	gate := readiness.NewGate(10000, false, NewHealthChecker())
	ts := httptest.NewServer(newMetricsMux(prometheus.NewRegistry(), gate, true))
	defer ts.Close()

	for _, path := range []string{"/metrics", "/healthz", "/readyz", "/healthz/ready"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s with pprof enabled: want 200, got %d", path, resp.StatusCode)
		}
	}
}
