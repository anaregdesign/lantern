package config

import (
	"log/slog"
	"os"
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// ObservabilityConfig groups logging, metrics endpoint, gRPC reflection
// (the on-the-wire surface served by connectrpc.com/grpcreflect), and
// build-info knobs.
//
//   - LANTERN_LOG_LEVEL                  debug|info|warn|error (default info)
//   - LANTERN_LOG_FORMAT                 json|text             (default json)
//   - LANTERN_METRICS_ADDR               host:port for /metrics + /healthz
//     (default :9090; set empty to disable)
//   - LANTERN_REFLECTION                 true|false            (default true)
//   - LANTERN_VERSION                    overrides lantern_build_info{version}
//   - LANTERN_COMMIT                     overrides lantern_build_info{commit}
//   - LANTERN_SLOW_RPC_THRESHOLD_MS      milliseconds; RPCs that take longer
//     emit a warn-level "slow rpc" log. Default 500. Set to 0 to disable.
//   - LANTERN_PPROF_ENABLED              true|false            (default false)
//     when true, mounts /debug/pprof/* on the metrics listener for heap,
//     goroutine, mutex, block, allocs, threadcreate, profile (CPU), trace,
//     cmdline, and symbol. The metrics listener is intended for internal
//     scrape traffic only — leave PPROF disabled unless that boundary holds.
//   - LANTERN_MUTEX_PROFILE_FRACTION     int   (default 0 = disabled)
//     passed to runtime.SetMutexProfileFraction. Sampling rate for mutex
//     contention events; 1-in-N stacks are recorded. Has runtime cost on
//     contended workloads — leave 0 in production unless actively profiling.
//   - LANTERN_BLOCK_PROFILE_RATE         int   (default 0 = disabled)
//     passed to runtime.SetBlockProfileRate. Nanoseconds between block-event
//     samples; lower = more samples. Same runtime-cost caveat as above.
type ObservabilityConfig struct {
	LogLevel             slog.Level
	LogFormat            string
	MetricsAddr          string
	EnableReflection     bool
	Version              string
	Commit               string
	SlowRPCThreshold     time.Duration
	EnablePprof          bool
	MutexProfileFraction int
	BlockProfileRate     int
}

func loadObservability(metricsDefault string) ObservabilityConfig {
	return ObservabilityConfig{
		LogLevel:             envconfig.Level("LANTERN_LOG_LEVEL", slog.LevelInfo),
		LogFormat:            envconfig.String("LANTERN_LOG_FORMAT", "json"),
		MetricsAddr:          envconfig.String("LANTERN_METRICS_ADDR", metricsDefault),
		EnableReflection:     envconfig.Bool("LANTERN_REFLECTION", true),
		Version:              envconfig.String("LANTERN_VERSION", ""),
		Commit:               envconfig.String("LANTERN_COMMIT", ""),
		SlowRPCThreshold:     time.Duration(envconfig.Int("LANTERN_SLOW_RPC_THRESHOLD_MS", 500)) * time.Millisecond,
		EnablePprof:          envconfig.Bool("LANTERN_PPROF_ENABLED", false),
		MutexProfileFraction: envconfig.Int("LANTERN_MUTEX_PROFILE_FRACTION", 0),
		BlockProfileRate:     envconfig.Int("LANTERN_BLOCK_PROFILE_RATE", 0),
	}
}

// TracingConfig selects optional tracing activation and wire protocol. Other
// standard OTEL exporter options, including the destination URL and TLS, remain
// owned by the OpenTelemetry SDK to preserve its endpoint/path precedence.
type TracingConfig struct {
	Enabled  bool
	Protocol string
}

func loadTracing() TracingConfig {
	return TracingConfig{
		Enabled:  firstNonEmpty(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"), os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "",
		Protocol: firstNonEmpty(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"), os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"), "grpc"),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
