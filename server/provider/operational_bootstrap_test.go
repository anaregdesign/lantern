package provider

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestOperationalBootstrapClosedDetachedMetadata(t *testing.T) {
	cfg, err := LoadConfig(envconfig.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	metadata := NewOperationalBootstrapConfig(cfg)
	counts := []int{1, 2, 12, 3, 6, 3, 11, 5}
	seen := map[string]bool{}
	for index, group := range operationalpolicy.Groups() {
		fields, known := metadata.Fields(group)
		if !known || len(fields) != counts[index] {
			t.Fatal("closed group projection changed", group, len(fields))
		}
		for _, field := range fields {
			if seen[field.Key] || field.Source != OperationalBootstrapDefault {
				t.Fatal("duplicate or invented bootstrap source", field)
			}
			seen[field.Key] = true
		}
		fields[0] = OperationalBootstrapField{Key: "changed", Source: OperationalBootstrapEnvironment}
		again, _ := metadata.Fields(group)
		if again[0].Key == "changed" || again[0].Source != OperationalBootstrapDefault {
			t.Fatal("provenance projection aliases load metadata")
		}
	}
	if len(seen) != 43 {
		t.Fatal("provenance escaped selected43 scope", len(seen))
	}
	for _, key := range []string{"LANTERN_LOG_LEVEL", "LANTERN_BACKUP_DIR", "LANTERN_SECURITY_CONFIG_JSON", "unknown"} {
		if _, known := metadata.Source(key); known {
			t.Fatal("unselected setting retained", key)
		}
	}
	for _, metadata := range []OperationalBootstrapConfig{{}, NewOperationalBootstrapConfig(nil), NewOperationalBootstrapConfig(&Config{})} {
		if source, known := metadata.Source("LANTERN_RATE_LIMIT_RPS"); known || source != "" {
			t.Fatal("hand-built Config invented a default")
		}
		if fields, known := metadata.Fields(operationalpolicy.GroupSlowRPC); known || fields != nil {
			t.Fatal("uncaptured projection fabricated fields")
		}
	}
}

func TestOperationalBootstrapParserAndDerivedSources(t *testing.T) {
	cases := []struct {
		name  string
		input []string
		key   string
		want  OperationalBootstrapSource
	}{
		{"unset", nil, "LANTERN_SLOW_RPC_THRESHOLD_MS", OperationalBootstrapDefault},
		{"empty", []string{"LANTERN_SLOW_RPC_THRESHOLD_MS= \t "}, "LANTERN_SLOW_RPC_THRESHOLD_MS", OperationalBootstrapDefault},
		{"malformed", []string{"LANTERN_SLOW_RPC_THRESHOLD_MS=private-malformed-input"}, "LANTERN_SLOW_RPC_THRESHOLD_MS", OperationalBootstrapDefault},
		{"explicit-zero", []string{"LANTERN_SLOW_RPC_THRESHOLD_MS=0"}, "LANTERN_SLOW_RPC_THRESHOLD_MS", OperationalBootstrapEnvironment},
		{"custom-fallback", []string{"LANTERN_TRAVERSAL_TIMEOUT_MS=-1"}, "LANTERN_TRAVERSAL_TIMEOUT_MS", OperationalBootstrapDefault},
		{"custom-positive-fallback", []string{"LANTERN_TRAVERSAL_MAX_PUSHES=0"}, "LANTERN_TRAVERSAL_MAX_PUSHES", OperationalBootstrapDefault},
		{"valid-false", []string{"LANTERN_BACKUP_ENABLED=false"}, "LANTERN_BACKUP_ENABLED", OperationalBootstrapEnvironment},
		{"backup-path-disable", []string{"LANTERN_BACKUP_ENABLED=true"}, "LANTERN_BACKUP_ENABLED", OperationalBootstrapDerived},
		{"backup-valid-true", []string{"LANTERN_BACKUP_ENABLED=true", "LANTERN_BACKUP_DIR=/fixture/backups"}, "LANTERN_BACKUP_ENABLED", OperationalBootstrapEnvironment},
		{"derived-burst", []string{"LANTERN_RATE_LIMIT_RPS=2.5"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDerived},
		{"malformed-derived-burst", []string{"LANTERN_RATE_LIMIT_RPS=2.5", "LANTERN_RATE_LIMIT_BURST=bad"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDerived},
		{"empty-derived-burst", []string{"LANTERN_RATE_LIMIT_RPS=2.5", "LANTERN_RATE_LIMIT_BURST="}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDerived},
		{"overridden-burst", []string{"LANTERN_RATE_LIMIT_RPS=2.5", "LANTERN_RATE_LIMIT_BURST=0"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDerived},
		{"explicit-burst", []string{"LANTERN_RATE_LIMIT_RPS=2.5", "LANTERN_RATE_LIMIT_BURST=7"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapEnvironment},
		{"explicit-zero-rps", []string{"LANTERN_RATE_LIMIT_RPS=0"}, "LANTERN_RATE_LIMIT_RPS", OperationalBootstrapEnvironment},
		{"default-zero-burst", []string{"LANTERN_RATE_LIMIT_RPS=0"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDefault},
		{"fractional-normalization", []string{"LANTERN_RATE_LIMIT_RPS=0.25"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDerived},
		{"huge-normalization", []string{"LANTERN_RATE_LIMIT_RPS=1.7976931348623157e+308"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDerived},
		{"nan-rps", []string{"LANTERN_RATE_LIMIT_RPS=NaN"}, "LANTERN_RATE_LIMIT_RPS", OperationalBootstrapEnvironment},
		{"nan-derived-burst", []string{"LANTERN_RATE_LIMIT_RPS=NaN"}, "LANTERN_RATE_LIMIT_BURST", OperationalBootstrapDerived},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reader := envconfig.NewReader(test.input)
			cfg, err := LoadConfig(reader)
			if err != nil {
				t.Fatal(err)
			}
			metadata := NewOperationalBootstrapConfig(cfg)
			if got, known := metadata.Source(test.key); !known || got != test.want {
				t.Fatal("wrong effective input source", got, known, test.want)
			}
			if strings.Contains(fmt.Sprintf("%#v", metadata), "private-malformed-input") {
				t.Fatal("raw finding retained in provenance")
			}
			if test.name == "nan-rps" {
				values := operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: cfg.RateLimit.RPS, Burst: int64(cfg.RateLimit.Burst)}}
				if !math.IsNaN(cfg.RateLimit.RPS) || values.Validate() == nil {
					t.Fatal("invalid environment RPS was fabricated as a valid default")
				}
			}
			if test.name == "fractional-normalization" && (cfg.RateLimit.Burst != 0 || NewRateLimitInterceptor(cfg.RateLimit.RPS, cfg.RateLimit.Burst).lim.Burst() != 1) {
				t.Fatal("provenance capture changed legacy constructor normalization")
			}
		})
	}
	if cfg, err := LoadConfig(envconfig.NewReader([]string{"LANTERN_STRICT_CONFIG=true", "LANTERN_SLOW_RPC_THRESHOLD_MS=bad"})); cfg != nil || err == nil {
		t.Fatal("source capture bypassed strict parser refusal")
	}
}

func TestOperationalBootstrapCapturedInputAndConcurrentLoads(t *testing.T) {
	input := []string{"LANTERN_SLOW_RPC_THRESHOLD_MS=0", "FOREIGN_SECRET=private-sentinel"}
	reader := envconfig.NewReader(input)
	input[0] = "LANTERN_SLOW_RPC_THRESHOLD_MS=999"
	t.Setenv("LANTERN_SLOW_RPC_THRESHOLD_MS", "123")
	cfg, err := LoadConfig(reader)
	if err != nil || cfg.Observability.SlowRPCThreshold != 0 {
		t.Fatal("load reread process/mutable input", err)
	}
	metadata := NewOperationalBootstrapConfig(cfg)
	t.Setenv("LANTERN_SLOW_RPC_THRESHOLD_MS", "bad")
	if got, known := metadata.Source("LANTERN_SLOW_RPC_THRESHOLD_MS"); !known || got != OperationalBootstrapEnvironment {
		t.Fatal("observation reread process environment")
	}
	if strings.Contains(fmt.Sprintf("%#v", metadata), "private-sentinel") {
		t.Fatal("unselected secret retained")
	}
	var wg sync.WaitGroup
	for index := range 20 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			var input []string
			want := OperationalBootstrapDefault
			if index%2 == 0 {
				input = []string{"LANTERN_SLOW_RPC_THRESHOLD_MS=0"}
				want = OperationalBootstrapEnvironment
			}
			cfg, err := LoadConfig(envconfig.NewReader(input))
			if err != nil {
				t.Error(err)
				return
			}
			if got, known := NewOperationalBootstrapConfig(cfg).Source("LANTERN_SLOW_RPC_THRESHOLD_MS"); !known || got != want {
				t.Error("concurrent load sources leaked", index, got, want)
			}
		}(index)
	}
	wg.Wait()
}
