package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestObservabilityLoading(t *testing.T) {
	cleanEnvironment(t)
	got := Load("127.0.0.1:9090").Observability
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
	got = Load(":9090").Observability
	want = ObservabilityConfig{
		LogLevel: slog.LevelDebug, LogFormat: "text", MetricsAddr: "", EnableReflection: false,
		Version: "test", Commit: "commit", EnablePprof: true, MutexProfileFraction: 7, BlockProfileRate: 123,
	}
	if got != want {
		t.Fatalf("observability overrides = %+v, want %+v", got, want)
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
			if got := Load(":9090").Tracing; got != tc.want {
				t.Fatalf("tracing values = %+v, want %+v", got, tc.want)
			}
		})
	}
}
