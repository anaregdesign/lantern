package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/prometheus/client_golang/prometheus"
)

// TestMetricsMux exercises metrics and readiness through the HTTP mux.
func TestMetricsMux(t *testing.T) {
	t.Run("ReadinessSingleInstance", func(t *testing.T) {
		// Single-instance mode (peerMode=false) returns 200 on
		// /readyz + /healthz/ready immediately at startup.
		gate := readiness.NewGate(10000, false, NewHealthChecker())
		ts := httptest.NewServer(newMetricsMux(prometheus.NewRegistry(), gate, false))
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
	})

	t.Run("ReadinessMultiPeerFlips", func(t *testing.T) {
		// Multi-peer mode drives the gate through bootstrap and
		// per-peer lag transitions; both probe endpoints must
		// reflect each transition.
		gate := readiness.NewGate(100, true, NewHealthChecker())
		ts := httptest.NewServer(newMetricsMux(prometheus.NewRegistry(), gate, false))
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
	})
}
