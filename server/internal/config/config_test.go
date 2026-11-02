package config

import (
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestLoadLeavesEnvironmentFinalizationToComposition(t *testing.T) {
	cleanEnvironment(t)
	t.Setenv("LANTERN_STRICT_CONFIG", "true")
	t.Setenv("LANTERN_LATER_COMPONENT", "configured")
	reader := envconfig.NewReader(os.Environ())
	cfg := Load(reader, ":9090")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ordinary value validation: %v", err)
	}
	// Loading another component must complete before the load-local typo sweep.
	if got := reader.String("LANTERN_LATER_COMPONENT", ""); got != "configured" {
		t.Fatalf("later component value = %q", got)
	}
	if err := CheckEnvironment(reader, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("completed environment registry: %v", err)
	}
}

func TestConcurrentLoadsAreIndependent(t *testing.T) {
	const loads = 32
	for i := range loads {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			port := strconv.Itoa(7000 + i)
			laterKey := "LANTERN_COMPONENT_" + strconv.Itoa(i)
			environ := []string{
				"LANTERN_STRICT_CONFIG=true",
				"LANTERN_PORT=" + port,
				"LANTERN_LOG_LEVEL=debug",
				"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
				"OTEL_EXPORTER_OTLP_ENDPOINT=https://collector.invalid/" + port,
				laterKey + "=present",
			}
			wantPort := 7000 + i
			malformed := i%2 == 0
			if malformed {
				environ[0] = "LANTERN_STRICT_CONFIG=false"
				environ[1] = "LANTERN_PORT=bad"
				wantPort = 6380
			}
			reader := envconfig.NewReader(environ)
			environ[1] = "LANTERN_PORT=changed-after-capture"
			cfg := Load(reader, "127.0.0.1:9090")
			if cfg.Net.Port != wantPort || cfg.Observability.LogLevel != slog.LevelDebug || cfg.Tracing != (TracingConfig{Enabled: true, Protocol: "http/protobuf"}) {
				t.Fatalf("independent settings = %+v, %+v, %+v", cfg.Net, cfg.Observability, cfg.Tracing)
			}
			if unknown := reader.UnknownLanternVars(); len(unknown) != 2 || unknown[0].Key != laterKey || unknown[1].Key != "LANTERN_STRICT_CONFIG" {
				t.Fatalf("later registration unexpectedly shared: %+v", unknown)
			}
			reader.String(laterKey, "")
			if err := CheckEnvironment(reader, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
				t.Fatalf("completed load: %v", err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("ordinary validation: %v", err)
			}
			wantFindings := 0
			if malformed {
				wantFindings = 1
			}
			if findings := reader.Findings(); len(findings) != wantFindings || (malformed && findings[0].Key != "LANTERN_PORT") {
				t.Fatalf("independent parser findings = %+v", findings)
			}
		})
	}
}
