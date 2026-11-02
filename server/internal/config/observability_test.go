package config

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestObservabilityLoading(t *testing.T) {
	cleanEnvironment(t)
	reader := envconfig.NewReader(os.Environ())
	got := Load(reader, "127.0.0.1:9090").Observability
	want := ObservabilityConfig{
		LogLevel: slog.LevelInfo, LogFormat: "json", MetricsAddr: "127.0.0.1:9090",
		EnableReflection: true, SlowRPCThreshold: 500 * time.Millisecond,
	}
	if got != want {
		t.Fatalf("observability defaults = %+v, want %+v", got, want)
	}
	for key, value := range map[string]string{
		"LANTERN_LOG_LEVEL": "debug", "LANTERN_LOG_FORMAT": "text", "LANTERN_METRICS_ADDR": "",
		"LANTERN_REFLECTION": "false", "LANTERN_VERSION": "test", "LANTERN_COMMIT": "commit",
		"LANTERN_SLOW_RPC_THRESHOLD_MS": "0", "LANTERN_PPROF_ENABLED": "true",
		"LANTERN_MUTEX_PROFILE_FRACTION": "7", "LANTERN_BLOCK_PROFILE_RATE": "123",
	} {
		t.Setenv(key, value)
	}
	reader = envconfig.NewReader(os.Environ())
	got = Load(reader, ":9090").Observability
	want = ObservabilityConfig{
		LogLevel: slog.LevelDebug, LogLevelFromEnvironment: true, LogFormat: "text", MetricsAddr: "", EnableReflection: false,
		Version: "test", Commit: "commit", EnablePprof: true, MutexProfileFraction: 7, BlockProfileRate: 123,
	}
	if got != want {
		t.Fatalf("observability overrides = %+v, want %+v", got, want)
	}
}

func TestLogLevelSourceReflectsParsedInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     []string
		level     slog.Level
		explicit  bool
		malformed bool
	}{
		{"unset", nil, slog.LevelInfo, false, false},
		{"empty", []string{"LANTERN_LOG_LEVEL="}, slog.LevelInfo, false, false},
		{"whitespace", []string{"LANTERN_LOG_LEVEL=  "}, slog.LevelInfo, false, false},
		{"malformed", []string{"LANTERN_LOG_LEVEL=verbose"}, slog.LevelInfo, false, true},
		{"explicit default value", []string{"LANTERN_LOG_LEVEL=info"}, slog.LevelInfo, true, false},
		{"alias", []string{"LANTERN_LOG_LEVEL= WARNING "}, slog.LevelWarn, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := envconfig.NewReader(tc.input)
			got := loadObservability(reader, "")
			if got.LogLevel != tc.level || got.LogLevelFromEnvironment != tc.explicit || (len(reader.Findings()) != 0) != tc.malformed {
				t.Fatalf("level/source/findings = %v/%v/%v", got.LogLevel, got.LogLevelFromEnvironment, reader.Findings())
			}
		})
	}
}

func TestTracingEnvironmentPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name          string
		endpoint      string
		traceEndpoint string
		protocol      string
		traceProtocol string
		want          TracingConfig
	}{
		{name: "disabled", want: TracingConfig{Protocol: "grpc"}},
		{name: "general", endpoint: "https://collector.invalid", protocol: "http/protobuf", want: TracingConfig{Enabled: true, Protocol: "http/protobuf"}},
		{name: "trace overrides", endpoint: "general", traceEndpoint: "trace", protocol: "http", traceProtocol: "grpc", want: TracingConfig{Enabled: true, Protocol: "grpc"}},
		{name: "empty trace falls back", endpoint: "general", protocol: "http", want: TracingConfig{Enabled: true, Protocol: "http"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnvironment(t)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", tc.traceEndpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", tc.protocol)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", tc.traceProtocol)
			reader := envconfig.NewReader(os.Environ())
			if got := Load(reader, ":9090").Tracing; got != tc.want {
				t.Fatalf("tracing values = %+v, want %+v", got, tc.want)
			}
		})
	}
}
