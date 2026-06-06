package provider

import (
	"log/slog"
	"time"
)

// SlowRPCInterceptor measures each RPC's wall-clock duration and emits a
// warn-level slog line ("slow rpc") whenever the duration exceeds the
// configured threshold (#223). Threshold == 0 disables emission entirely.
//
// Fields:
//   - method        the RPC procedure path (Connect Spec.Procedure;
//     shape "/<service>/<method>")
//   - code          the resulting Connect status code (string form;
//     "ok" / "not_found" / …)
//   - duration_ms   handler wall-clock in milliseconds (int64)
//   - threshold_ms  the configured threshold in milliseconds (int64)
//
// Slow-RPC accounting is purely a logging signal; it does not affect the
// response or any Prometheus counter (PrometheusInterceptor already
// covers latency distributions). The dedicated log makes single-request
// outliers grep-able in JSON log streams.
//
// The Connect interceptor implementation lives in connect_middleware.go
// (SlowRPCInterceptor.ConnectInterceptor).
type SlowRPCInterceptor struct {
	threshold time.Duration
	logger    *slog.Logger
}

// NewSlowRPCInterceptor builds an interceptor that emits a warn log per RPC
// exceeding threshold. A zero/negative threshold disables emission (the
// interceptor returns the handler directly without measuring time).
// logger MUST be non-nil; pass slog.Default() if no dedicated logger is
// available.
func NewSlowRPCInterceptor(threshold time.Duration, logger *slog.Logger) *SlowRPCInterceptor {
	return &SlowRPCInterceptor{threshold: threshold, logger: logger}
}

// NewSlowRPCInterceptorProvider is the wire seam that pulls the
// configured threshold out of ObservabilityConfig. Splitting this from
// NewSlowRPCInterceptor lets tests construct the interceptor with an
// explicit duration without needing a full Config.
func NewSlowRPCInterceptorProvider(obs ObservabilityConfig, logger *slog.Logger) *SlowRPCInterceptor {
	return NewSlowRPCInterceptor(obs.SlowRPCThreshold, logger)
}

// Enabled reports whether the interceptor will measure / emit logs. Used by
// the wiring layer to decide whether to install the interceptor at all,
// keeping the hot path identical to pre-#223 when the threshold is 0.
func (s *SlowRPCInterceptor) Enabled() bool {
	return s != nil && s.threshold > 0 && s.logger != nil
}
