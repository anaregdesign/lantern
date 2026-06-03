package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/health"
)

// TestMetricsMux_ReadinessSingleInstance verifies that single-instance mode
// returns 200 on both /readyz and /healthz/ready immediately at startup.
func TestMetricsMux_ReadinessSingleInstance(t *testing.T) {
	gate := readiness.NewGate(10000, false, health.NewServer())
	ts := httptest.NewServer(newMetricsMux(prometheus.NewRegistry(), gate))
	defer ts.Close()

	for _, path := range []string{"/readyz", "/healthz/ready"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: want 200, got %d body=%q", path, resp.StatusCode, body)
		}
	}
}

// TestMetricsMux_ReadinessMultiPeerFlips drives the gate through the
// expected bootstrap and lag lifecycle and verifies the HTTP shim reflects
// each transition on both probe endpoints.
func TestMetricsMux_ReadinessMultiPeerFlips(t *testing.T) {
	gate := readiness.NewGate(100, true, health.NewServer())
	ts := httptest.NewServer(newMetricsMux(prometheus.NewRegistry(), gate))
	defer ts.Close()

	probe := func(path string) int {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	for _, path := range []string{"/readyz", "/healthz/ready"} {
		if got := probe(path); got != http.StatusServiceUnavailable {
			t.Fatalf("multi-peer pre-bootstrap GET %s: want 503, got %d", path, got)
		}
	}

	gate.MarkBootstrapped()
	for _, path := range []string{"/readyz", "/healthz/ready"} {
		if got := probe(path); got != http.StatusOK {
			t.Fatalf("post-bootstrap GET %s: want 200, got %d", path, got)
		}
	}

	gate.SetLag("peer-a", "origin-a", 250)
	for _, path := range []string{"/readyz", "/healthz/ready"} {
		if got := probe(path); got != http.StatusServiceUnavailable {
			t.Fatalf("lag-exceeded GET %s: want 503, got %d", path, got)
		}
	}

	gate.SetLag("peer-a", "origin-a", 0)
	for _, path := range []string{"/readyz", "/healthz/ready"} {
		if got := probe(path); got != http.StatusOK {
			t.Fatalf("caught-up GET %s: want 200, got %d", path, got)
		}
	}
}
