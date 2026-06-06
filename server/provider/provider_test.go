package provider

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/health"
)

// TestWireCacheGCHooks_EmitsTickSummary asserts that a single
// "graph cache: gc tick" info log record is emitted per cache tick
// after WireCacheGCHooks installs the multiplexed hooks (#223).
func TestWireCacheGCHooks_EmitsTickSummary(t *testing.T) {
	cache := graph.NewGraphCache[string, *v1.Vertex](time.Second)
	reg := prometheus.NewRegistry()
	dm := domainmetrics.New(reg, domainmetrics.Options{})

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if got := WireCacheGCHooks(cache, dm, logger); got != (CacheGCHooksWired{}) {
		t.Fatalf("WireCacheGCHooks return = %+v, want empty marker", got)
	}

	// Drive the Watch loop on a short interval; expect at least one tick
	// to fire while we hold the goroutine open.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cache.Watch(ctx, 10*time.Millisecond)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	recs := decodeRecords(t, &buf)
	if len(recs) == 0 {
		t.Fatalf("no log records emitted; want >=1 'graph cache: gc tick'")
	}
	rec := recs[0]
	if rec["msg"] != "graph cache: gc tick" {
		t.Errorf("msg = %v, want 'graph cache: gc tick'", rec["msg"])
	}
	for _, k := range []string{
		"vertices_expired", "edges_expired", "dangling_edges_removed",
		"vertices_remaining", "edges_remaining", "duration_ms",
	} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing field %q in record: %v", k, rec)
		}
	}
}

// TestMetricsMux groups the HTTP-shim tests for newMetricsMux. The
// function under test lives in provider.go alongside the wire-time
// metrics + readiness assembly, so the tests live next to the
// WireCacheGCHooks coverage in this same file rather than in an
// orphan metrics_server_test.go that has no matching source.
func TestMetricsMux(t *testing.T) {
	t.Run("ReadinessSingleInstance", func(t *testing.T) {
		// Single-instance mode (peerMode=false) returns 200 on
		// /readyz + /healthz/ready immediately at startup.
		gate := readiness.NewGate(10000, false, health.NewServer())
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
		gate := readiness.NewGate(100, true, health.NewServer())
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
