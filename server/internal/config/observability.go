package config

import (
	"log/slog"
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
	LogLevel slog.Level
	// LogLevelFromEnvironment is true only for a valid explicit input. Empty
	// or malformed environment input falls back to the default instead.
	LogLevelFromEnvironment bool
	LogFormat               string
	MetricsAddr             string
	EnableReflection        bool
	Version                 string
	Commit                  string
	SlowRPCThreshold        time.Duration
	EnablePprof             bool
	MutexProfileFraction    int
	BlockProfileRate        int
}

func loadObservability(reader *envconfig.Reader, metricsDefault string) ObservabilityConfig {
	level, explicit := reader.LevelWithSource("LANTERN_LOG_LEVEL", slog.LevelInfo)
	return ObservabilityConfig{
		LogLevel:                level,
		LogLevelFromEnvironment: explicit,
		LogFormat:               reader.String("LANTERN_LOG_FORMAT", "json"),
		MetricsAddr:             reader.String("LANTERN_METRICS_ADDR", metricsDefault),
		EnableReflection:        reader.Bool("LANTERN_REFLECTION", true),
		Version:                 reader.String("LANTERN_VERSION", ""),
		Commit:                  reader.String("LANTERN_COMMIT", ""),
		SlowRPCThreshold:        time.Duration(reader.Int("LANTERN_SLOW_RPC_THRESHOLD_MS", 500)) * time.Millisecond,
		EnablePprof:             reader.Bool("LANTERN_PPROF_ENABLED", false),
		MutexProfileFraction:    reader.Int("LANTERN_MUTEX_PROFILE_FRACTION", 0),
		BlockProfileRate:        reader.Int("LANTERN_BLOCK_PROFILE_RATE", 0),
	}
}

// TracingConfig selects optional tracing activation and wire protocol. Other
// standard OTEL exporter options, including the destination URL and TLS, remain
// owned by the OpenTelemetry SDK to preserve its endpoint/path precedence.
type TracingConfig struct {
	Enabled  bool
	Protocol string
}

func loadTracing(reader *envconfig.Reader) TracingConfig {
	return TracingConfig{
		Enabled:  firstNonEmpty(rawValue(reader, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"), rawValue(reader, "OTEL_EXPORTER_OTLP_ENDPOINT")) != "",
		Protocol: firstNonEmpty(rawValue(reader, "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"), rawValue(reader, "OTEL_EXPORTER_OTLP_PROTOCOL"), "grpc"),
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

// rawValue preserves the OpenTelemetry SDK's empty-versus-nonempty precedence
// while reading the same captured input as the ordinary Server settings.
func rawValue(reader *envconfig.Reader, key string) string {
	value, _ := reader.LookupEnv(key)
	return value
}
