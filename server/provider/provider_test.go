package provider

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/prometheus/client_golang/prometheus"
)

// TestWireCacheGCHooks_EmitsTickSummary asserts that a single
// "graph cache: gc tick" info log record is emitted per cache tick
// after WireCacheGCHooks installs the multiplexed hooks (#223).
func TestWireCacheGCHooks_EmitsTickSummary(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *v1.Vertex](time.Second)
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

// TestNewConfigValidation covers the #847 boot-time validation pass: a
// malformed value or an unknown LANTERN_* variable is tolerated (warn +
// default) by default and fatal under LANTERN_STRICT_CONFIG. The strict
// failure happens inside NewConfig — the first provider wire constructs — so
// a refused boot never reaches listener construction.
func TestNewConfigValidation(t *testing.T) {
	t.Run("defaults survive a malformed value without strict", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "false")
		t.Setenv("LANTERN_PORT", "not-a-number")
		cfg, err := NewConfig()
		if err != nil {
			t.Fatalf("NewConfig: %v", err)
		}
		if cfg.Net.Port != 6380 {
			t.Fatalf("Port = %d, want default 6380", cfg.Net.Port)
		}
	})

	t.Run("strict mode rejects a malformed value", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "true")
		t.Setenv("LANTERN_PORT", "not-a-number")
		_, err := NewConfig()
		if err == nil {
			t.Fatal("NewConfig accepted a malformed value under strict mode")
		}
		if !strings.Contains(err.Error(), "LANTERN_PORT") || !strings.Contains(err.Error(), "not-a-number") {
			t.Fatalf("error does not name the offending variable: %v", err)
		}
	})

	t.Run("strict mode rejects an unknown variable with a suggestion", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "true")
		t.Setenv("LANTERN_PROT", "6381") // typo of LANTERN_PORT
		_, err := NewConfig()
		if err == nil {
			t.Fatal("NewConfig accepted an unknown LANTERN_* variable under strict mode")
		}
		if !strings.Contains(err.Error(), "LANTERN_PROT") {
			t.Fatalf("error does not name the unknown variable: %v", err)
		}
		if !strings.Contains(err.Error(), "LANTERN_PORT") {
			t.Fatalf("error does not carry the did-you-mean suggestion: %v", err)
		}
	})

	t.Run("foreign namespaces are exempt from the sweep", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "true")
		t.Setenv("LANTERN_MCP_AGENT_ID", "agent-a")
		if _, err := NewConfig(); err != nil {
			t.Fatalf("NewConfig rejected a foreign-namespace variable: %v", err)
		}
	})

	t.Run("malformed NodeID reaches the strict gate via Malformed", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "true")
		t.Setenv("LANTERN_NODE_ID", "zz-not-hex")
		_, err := NewConfig()
		if err == nil {
			t.Fatal("NewConfig accepted a malformed LANTERN_NODE_ID under strict mode")
		}
		if !strings.Contains(err.Error(), "LANTERN_NODE_ID") {
			t.Fatalf("error does not name LANTERN_NODE_ID: %v", err)
		}
	})

	// #911: an unrecognised LANTERN_SEARCH_DEFAULT_MODE always fails boot,
	// independent of LANTERN_STRICT_CONFIG — the value parses as a string but a
	// typo would silently rewrite server-wide default ranking semantics.
	t.Run("invalid DEFAULT_MODE fails boot even without strict", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "false")
		t.Setenv("LANTERN_SEARCH_DEFAULT_MODE", "min_shold") // typo of min-should
		_, err := NewConfig()
		if err == nil {
			t.Fatal("NewConfig accepted an unrecognised LANTERN_SEARCH_DEFAULT_MODE")
		}
		if !strings.Contains(err.Error(), "LANTERN_SEARCH_DEFAULT_MODE") || !strings.Contains(err.Error(), "min_shold") {
			t.Fatalf("error does not name the offending value: %v", err)
		}
		if !strings.Contains(err.Error(), "any|all|min-should") {
			t.Fatalf("error does not list the allowed values: %v", err)
		}
	})

	t.Run("canonical and alias DEFAULT_MODE spellings boot", func(t *testing.T) {
		for _, mode := range []string{"any", "all", "min-should", "minshould", "min_should", "ALL", ""} {
			envconfig.ResetForTesting()
			t.Setenv("LANTERN_SEARCH_DEFAULT_MODE", mode)
			if _, err := NewConfig(); err != nil {
				t.Fatalf("NewConfig rejected DEFAULT_MODE=%q: %v", mode, err)
			}
		}
	})
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
