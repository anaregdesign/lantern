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

func TestNewGraphCache_CausalMetadataLimits(t *testing.T) {
	cache := NewGraphCache(CacheConfig{
		TTL: time.Minute, MaxVertexCausalEntries: 7, MaxEdgeCausalEntries: 9,
	}, SearchConfig{})
	stats := cache.CausalMetadataStats()
	if stats.MaxVertexEntries != 7 || stats.MaxEdgeEntries != 9 {
		t.Fatalf("NewGraphCache causal limits = (%d, %d), want (7, 9)", stats.MaxVertexEntries, stats.MaxEdgeEntries)
	}
}

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
	t.Run("bootstrap validation uses configured JSON logger", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "false")
		var buf bytes.Buffer
		logger := newLogger(ObservabilityConfig{
			LogLevel:  slog.LevelInfo,
			LogFormat: "json",
		}, &buf)

		if err := validateEnv([]string{"LANTERN_NOT_REAL=1"}, logger); err != nil {
			t.Fatalf("validateEnv: %v", err)
		}
		recs := decodeRecords(t, &buf)
		if len(recs) != 2 {
			t.Fatalf("bootstrap records = %d, want 2: %v", len(recs), recs)
		}
		warn := recs[0]
		if warn["level"] != "WARN" || warn["msg"] != "config: unknown LANTERN_* variable" {
			t.Fatalf("bootstrap warning severity/message = %v/%v, want WARN/unknown-variable", warn["level"], warn["msg"])
		}
		if warn["service"] != "lantern" || warn["key"] != "LANTERN_NOT_REAL" {
			t.Fatalf("bootstrap warning fields = %v", warn)
		}
		info := recs[1]
		if info["level"] != "INFO" || info["msg"] != "config: environment overrides active" {
			t.Fatalf("bootstrap summary severity/message = %v/%v, want INFO/environment-summary", info["level"], info["msg"])
		}
	})

	t.Run("traversal safety defaults are finite", func(t *testing.T) {
		envconfig.ResetForTesting()
		for _, key := range []string{
			"LANTERN_TRAVERSAL_TIMEOUT_MS",
			"LANTERN_TRAVERSAL_MAX_PUSHES",
			"LANTERN_TRAVERSAL_MAX_TOUCHED_EDGES",
			"LANTERN_TRAVERSAL_MAX_RESULTS",
		} {
			t.Setenv(key, "")
		}
		cfg, err := NewConfig()
		if err != nil {
			t.Fatalf("NewConfig: %v", err)
		}
		if got, want := cfg.Traversal.Timeout, 5*time.Second; got != want {
			t.Errorf("Traversal.Timeout = %v, want %v", got, want)
		}
		if got, want := cfg.Traversal.MaxPushes, 1_000_000; got != want {
			t.Errorf("Traversal.MaxPushes = %d, want %d", got, want)
		}
		if got, want := cfg.Traversal.MaxTouchedEdges, 10_000_000; got != want {
			t.Errorf("Traversal.MaxTouchedEdges = %d, want %d", got, want)
		}
		if got, want := cfg.Traversal.MaxResults, 1024; got != want {
			t.Errorf("Traversal.MaxResults = %d, want %d", got, want)
		}
	})

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

	t.Run("causal metadata budgets default unlimited and accept explicit limits", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_MAX_VERTEX_CAUSAL_ENTRIES", "123")
		t.Setenv("LANTERN_MAX_EDGE_CAUSAL_ENTRIES", "456")
		cfg, err := NewConfig()
		if err != nil {
			t.Fatalf("NewConfig: %v", err)
		}
		if cfg.Cache.MaxVertexCausalEntries != 123 || cfg.Cache.MaxEdgeCausalEntries != 456 {
			t.Fatalf("causal budgets = %d/%d, want 123/456", cfg.Cache.MaxVertexCausalEntries, cfg.Cache.MaxEdgeCausalEntries)
		}

		envconfig.ResetForTesting()
		t.Setenv("LANTERN_MAX_VERTEX_CAUSAL_ENTRIES", "")
		t.Setenv("LANTERN_MAX_EDGE_CAUSAL_ENTRIES", "")
		defaults, err := NewConfig()
		if err != nil {
			t.Fatalf("NewConfig defaults: %v", err)
		}
		if defaults.Cache.MaxVertexCausalEntries != 0 || defaults.Cache.MaxEdgeCausalEntries != 0 {
			t.Fatalf("default causal budgets = %d/%d, want unlimited 0/0", defaults.Cache.MaxVertexCausalEntries, defaults.Cache.MaxEdgeCausalEntries)
		}
	})

	for _, key := range []string{
		"LANTERN_MAX_VERTEX_CAUSAL_ENTRIES",
		"LANTERN_MAX_EDGE_CAUSAL_ENTRIES",
	} {
		t.Run("causal metadata budget rejects negative "+key, func(t *testing.T) {
			envconfig.ResetForTesting()
			t.Setenv(key, "-1")
			_, err := NewConfig()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("NewConfig error = %v, want unconditional rejection naming %s", err, key)
			}
		})
	}

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

	t.Run("search work limits default to finite positive values", func(t *testing.T) {
		envconfig.ResetForTesting()
		cfg, err := NewConfig()
		if err != nil {
			t.Fatalf("NewConfig: %v", err)
		}
		if cfg.Search.Timeout <= 0 || cfg.Search.MaxQueryBytes <= 0 || cfg.Search.MaxQueryTerms <= 0 ||
			cfg.Search.MaxDictionaryVisits <= 0 || cfg.Search.MaxPostingVisits <= 0 ||
			cfg.Search.MaxPositionVisits <= 0 || cfg.Search.MaxExpirationVisits <= 0 || cfg.Search.MaxInFlight <= 0 ||
			cfg.Search.CursorTTL <= 0 || cfg.Search.MaxSessions <= 0 || cfg.Search.MaxSessionHits <= int(cfg.Search.MaxLimit) || cfg.Search.MaxSessionBytes <= 0 ||
			cfg.Search.AnalysisLimits.MaxDocumentBytes <= 0 || cfg.Search.AnalysisLimits.MaxDocumentTokens <= 0 ||
			cfg.Search.AnalysisLimits.MaxDocumentTerms <= 0 || cfg.Search.AnalysisLimits.MaxLiveTerms <= 0 ||
			cfg.Search.AnalysisLimits.MaxLivePostings <= 0 || cfg.Search.AnalysisLimits.MaxPositionEntries <= 0 ||
			cfg.Search.AnalysisLimits.CompactionRatio <= 1 || cfg.Search.AnalysisLimits.CompactionMinRetired <= 0 {
			t.Fatalf("search safety defaults are not all positive: %+v", cfg.Search)
		}
	})

	for _, key := range []string{
		"LANTERN_SEARCH_TIMEOUT_MS",
		"LANTERN_SEARCH_MAX_QUERY_BYTES",
		"LANTERN_SEARCH_MAX_QUERY_TERMS",
		"LANTERN_SEARCH_MAX_DICTIONARY_VISITS",
		"LANTERN_SEARCH_MAX_POSTING_VISITS",
		"LANTERN_SEARCH_MAX_POSITION_VISITS",
		"LANTERN_SEARCH_MAX_EXPIRATION_VISITS",
		"LANTERN_SEARCH_MAX_IN_FLIGHT",
		"LANTERN_SEARCH_CURSOR_TTL_SECONDS",
		"LANTERN_SEARCH_MAX_SESSIONS",
		"LANTERN_SEARCH_MAX_SESSION_HITS",
		"LANTERN_SEARCH_MAX_SESSION_BYTES",
		"LANTERN_SEARCH_MAX_DOCUMENT_BYTES",
		"LANTERN_SEARCH_MAX_DOCUMENT_TOKENS",
		"LANTERN_SEARCH_MAX_DOCUMENT_TERMS",
		"LANTERN_SEARCH_MAX_LIVE_TERMS",
		"LANTERN_SEARCH_MAX_LIVE_POSTINGS",
		"LANTERN_SEARCH_MAX_POSITION_ENTRIES",
		"LANTERN_SEARCH_COMPACTION_MIN_RETIRED",
	} {
		t.Run("search safety rejects non-positive "+key, func(t *testing.T) {
			envconfig.ResetForTesting()
			t.Setenv("LANTERN_STRICT_CONFIG", "false")
			t.Setenv(key, "0")
			_, err := NewConfig()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("NewConfig error = %v, want unconditional rejection naming %s", err, key)
			}
		})
	}

	t.Run("search compaction ratio must exceed one", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_SEARCH_COMPACTION_RATIO", "1")
		_, err := NewConfig()
		if err == nil || !strings.Contains(err.Error(), "LANTERN_SEARCH_COMPACTION_RATIO") {
			t.Fatalf("NewConfig error = %v", err)
		}
	})

	t.Run("query term cap cannot exceed byte cap", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_SEARCH_MAX_QUERY_BYTES", "4")
		t.Setenv("LANTERN_SEARCH_MAX_QUERY_TERMS", "5")
		_, err := NewConfig()
		if err == nil || !strings.Contains(err.Error(), "MAX_QUERY_TERMS") || !strings.Contains(err.Error(), "MAX_QUERY_BYTES") {
			t.Fatalf("NewConfig error = %v, want cap-relationship rejection", err)
		}
	})

	t.Run("search session hit cap must exceed a page", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_SEARCH_MAX_LIMIT", "100")
		t.Setenv("LANTERN_SEARCH_MAX_SESSION_HITS", "100")
		_, err := NewConfig()
		if err == nil || !strings.Contains(err.Error(), "MAX_SESSION_HITS") || !strings.Contains(err.Error(), "MAX_LIMIT") {
			t.Fatalf("NewConfig error = %v, want session/page relationship rejection", err)
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
