package config

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestGraphLoading(t *testing.T) {
	cleanEnvironment(t)
	cfg := Load(":9090")
	if cfg.Cache != (CacheConfig{TTL: time.Minute, GCInterval: time.Minute}) {
		t.Fatalf("cache defaults = %+v", cfg.Cache)
	}
	if cfg.Traversal != (TraversalConfig{Timeout: 5 * time.Second, MaxPushes: 1_000_000, MaxTouchedEdges: 10_000_000, MaxResults: 1024}) {
		t.Fatalf("traversal defaults = %+v", cfg.Traversal)
	}
	if cfg.Scan != (ScanConfig{ScanDefaultLimit: 1000, ScanMaxLimit: 10000, DeleteByPrefixDefaultLimit: 10000, DeleteByPrefixMaxLimit: 100000}) {
		t.Fatalf("scan defaults = %+v", cfg.Scan)
	}
	if cfg.Validation != (ValidationLimits{MaxKeyLen: 1024, MaxBatchSize: 10000, IlluminateMaxStep: 16, IlluminateMaxK: 1024}) {
		t.Fatalf("validation defaults = %+v", cfg.Validation)
	}
	for key, value := range map[string]string{
		"LANTERN_DEFAULT_TTL_SECONDS": "4", "LANTERN_GC_INTERVAL_SECONDS": "5", "LANTERN_GC_EDGE_BUDGET": "6",
		"LANTERN_MAX_VERTICES": "7", "LANTERN_MAX_EDGES": "8", "LANTERN_MAX_VERTEX_CAUSAL_ENTRIES": "9", "LANTERN_MAX_EDGE_CAUSAL_ENTRIES": "10",
		"LANTERN_TRAVERSAL_TIMEOUT_MS": "0", "LANTERN_TRAVERSAL_MAX_PUSHES": "11", "LANTERN_TRAVERSAL_MAX_TOUCHED_EDGES": "12", "LANTERN_TRAVERSAL_MAX_RESULTS": "13",
		"LANTERN_SCAN_DEFAULT_LIMIT": "14", "LANTERN_SCAN_MAX_LIMIT": "15", "LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT": "16", "LANTERN_DELETE_BY_PREFIX_MAX_LIMIT": "17",
		"LANTERN_MAX_KEY_LEN": "18", "LANTERN_MAX_BATCH_SIZE": "19", "LANTERN_ILLUMINATE_MAX_STEP": "20", "LANTERN_ILLUMINATE_MAX_K": "21",
	} {
		t.Setenv(key, value)
	}
	cfg = Load(":9090")
	if cfg.Cache != (CacheConfig{TTL: 4 * time.Second, GCInterval: 5 * time.Second, GCEdgeBudget: 6, MaxVertices: 7, MaxEdges: 8, MaxVertexCausalEntries: 9, MaxEdgeCausalEntries: 10}) {
		t.Fatalf("cache overrides = %+v", cfg.Cache)
	}
	if cfg.Traversal != (TraversalConfig{MaxPushes: 11, MaxTouchedEdges: 12, MaxResults: 13}) ||
		cfg.Scan != (ScanConfig{ScanDefaultLimit: 14, ScanMaxLimit: 15, DeleteByPrefixDefaultLimit: 16, DeleteByPrefixMaxLimit: 17}) ||
		cfg.Validation != (ValidationLimits{MaxKeyLen: 18, MaxBatchSize: 19, IlluminateMaxStep: 20, IlluminateMaxK: 21}) {
		t.Fatalf("graph overrides = traversal %+v, scan %+v, validation %+v", cfg.Traversal, cfg.Scan, cfg.Validation)
	}
}

func TestTraversalInvalidValuesKeepExistingFallback(t *testing.T) {
	cleanEnvironment(t)
	for key, value := range map[string]string{
		"LANTERN_TRAVERSAL_TIMEOUT_MS": "-1", "LANTERN_TRAVERSAL_MAX_PUSHES": "0",
		"LANTERN_TRAVERSAL_MAX_TOUCHED_EDGES": "-2", "LANTERN_TRAVERSAL_MAX_RESULTS": "0",
	} {
		t.Setenv(key, value)
	}
	cfg := Load(":9090")
	if cfg.Traversal.Timeout != 5*time.Second || cfg.Traversal.MaxPushes != 1_000_000 || cfg.Traversal.MaxTouchedEdges != 10_000_000 || cfg.Traversal.MaxResults != 1024 {
		t.Fatalf("invalid traversal fallback = %+v", cfg.Traversal)
	}
	if len(envconfig.Findings()) != 4 {
		t.Fatalf("traversal findings = %+v, want four", envconfig.Findings())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("fallback value validation: %v", err)
	}
}
