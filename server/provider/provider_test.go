package provider

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/config"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestNewConfigOperationalComposition(t *testing.T) {
	t.Run("strict sweep waits for both operational and provider loaders", func(t *testing.T) {
		clearSecurityEnvironment(t)
		envconfig.ResetForTesting()
		for key, value := range map[string]string{
			"LANTERN_STRICT_CONFIG":                  "true",
			"LANTERN_PORT":                           "6399",
			"LANTERN_RATE_LIMIT_RPS":                 "7.5",
			"LANTERN_RATE_LIMIT_BURST":               "19",
			"LANTERN_METRICS_ADDR":                   "",
			"LANTERN_SHUTDOWN_TIMEOUT_SECONDS":       "9",
			"LANTERN_DRAIN_DELAY_SECONDS":            "2",
			"LANTERN_SCAN_DEFAULT_LIMIT":             "21",
			"LANTERN_SEARCH_MAX_IN_FLIGHT":           "3",
			"LANTERN_MAX_BATCH_SIZE":                 "17",
			"LANTERN_BACKUP_RESTORE_ON_START":        "false",
			"LANTERN_BACKUP_INTERVAL":                "7m",
			"LANTERN_MUTATION_LOG_CAPACITY":          "73",
			"LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER": "11",
			"LANTERN_NODE_ID":                        "0102030405060708090a0b0c0d0e0f10",
			"LANTERN_MAX_REPLICATION_LAG":            "33",
			"LANTERN_ANTI_ENTROPY_INTERVAL_MS":       "1234",
			"LANTERN_CORS_ALLOWED_ORIGINS":           "https://admin.example",
			"LANTERN_LLM_PROVIDER":                   "disabled",
			"LANTERN_CDC_ENABLED":                    "false",
		} {
			t.Setenv(key, value)
		}
		cfg, err := NewConfig()
		if err != nil {
			t.Fatalf("valid settings from both layers were rejected: %v", err)
		}
		if NewNetConfig(cfg).Port != 6399 || NewRateLimitConfig(cfg).RPS != 7.5 || NewRateLimitConfig(cfg).Burst != 19 || NewObservabilityConfig(cfg).MetricsAddr != "" {
			t.Fatal("network and observability selectors lost operational settings")
		}
		if NewShutdownConfig(cfg).Timeout != 9*time.Second || NewShutdownConfig(cfg).DrainDelay != 2*time.Second || NewScanConfig(cfg).ScanDefaultLimit != 21 || NewSearchConfig(cfg).MaxInFlight != 3 || NewValidationLimits(cfg).MaxBatchSize != 17 {
			t.Fatal("focused selectors lost lifecycle or request settings")
		}
		if cfg.Backup.Interval != 7*time.Minute || cfg.Backup.RestoreOnStart || cfg.MutationLog.Capacity != 73 || cfg.MutationLog.SubscriberBuffer != 11 || !cfg.Replication.nodeIDExplicit || cfg.Replication.NodeID[0] != 1 || cfg.Readiness.MaxLag != 33 || cfg.AntiEntropy.Interval != 1234*time.Millisecond || len(cfg.CORS.AllowedOrigins) != 1 || cfg.Changes.Enabled {
			t.Fatal("provider-owned settings were lost during operational composition")
		}
	})

	t.Run("metrics keeps the OFF default and explicit overrides", func(t *testing.T) {
		clearSecurityEnvironment(t)
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_METRICS_ADDR", "")
		if err := os.Unsetenv("LANTERN_METRICS_ADDR"); err != nil {
			t.Fatal(err)
		}
		off, err := NewConfig()
		if err != nil {
			t.Fatalf("OFF config: %v", err)
		}
		if off.Observability.MetricsAddr != ":9090" {
			t.Fatalf("OFF metrics default = %q", off.Observability.MetricsAddr)
		}
		for _, address := range []string{"", "127.0.0.1:9393"} {
			t.Setenv("LANTERN_METRICS_ADDR", address)
			envconfig.ResetForTesting()
			cfg, err := NewConfig()
			if err != nil {
				t.Fatalf("explicit metrics setting: %v", err)
			}
			if cfg.Observability.MetricsAddr != address {
				t.Fatalf("metrics setting = %q, want %q", cfg.Observability.MetricsAddr, address)
			}
		}
	})
}

// TestNewConfigValidation covers the #847 boot-time validation pass: a
// malformed value or an unknown LANTERN_* variable is tolerated (warn +
// default) by default and fatal under LANTERN_STRICT_CONFIG. The strict
// failure happens inside NewConfig — the first provider wire constructs — so
// a refused boot never reaches listener construction.
func TestNewConfigValidation(t *testing.T) {
	t.Run("GC edge budget loads and rejects a negative value", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_GC_EDGE_BUDGET", "5000")
		cfg, err := NewConfig()
		if err != nil {
			t.Fatalf("NewConfig: %v", err)
		}
		if cfg.Cache.GCEdgeBudget != 5000 {
			t.Fatalf("GCEdgeBudget = %d, want 5000", cfg.Cache.GCEdgeBudget)
		}
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_GC_EDGE_BUDGET", "-1")
		if _, err := NewConfig(); err == nil || !strings.Contains(err.Error(), "LANTERN_GC_EDGE_BUDGET") {
			t.Fatalf("NewConfig error = %v, want negative-budget rejection", err)
		}
	})

	t.Run("bootstrap validation uses configured JSON logger", func(t *testing.T) {
		envconfig.ResetForTesting()
		t.Setenv("LANTERN_STRICT_CONFIG", "false")
		var buf bytes.Buffer
		logger := newLogger(ObservabilityConfig{
			LogLevel:  slog.LevelInfo,
			LogFormat: "json",
		}, &buf)

		if err := config.CheckEnvironment([]string{"LANTERN_NOT_REAL=1"}, logger); err != nil {
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
