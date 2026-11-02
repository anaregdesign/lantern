package provider

import (
	"errors"
	"log/slog"
	"sync/atomic"
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
	threshold atomic.Int64
	logger    *slog.Logger
	now       func() time.Time
}

// NewSlowRPCInterceptor builds an interceptor that emits a warn log per RPC
// exceeding threshold. A zero/negative threshold disables emission (the
// interceptor returns the handler directly without measuring time).
// logger MUST be non-nil; pass slog.Default() if no dedicated logger is
// available.
func NewSlowRPCInterceptor(threshold time.Duration, logger *slog.Logger) *SlowRPCInterceptor {
	s := &SlowRPCInterceptor{logger: logger, now: time.Now}
	// Preserve the startup constructor's historical nonpositive disable mode.
	if threshold > 0 {
		s.threshold.Store(int64(threshold))
	}
	return s
}

var errSlowRPCThreshold = errors.New("slow RPC threshold requires a logging owner and nonnegative representable milliseconds")

// SetThresholdMilliseconds publishes one validated policy for future requests.
// Requests already running retain the threshold captured at their entry.
func (s *SlowRPCInterceptor) SetThresholdMilliseconds(milliseconds int64) error {
	const maxMilliseconds = int64(1<<63-1) / int64(time.Millisecond)
	if s == nil || s.logger == nil || milliseconds < 0 || milliseconds > maxMilliseconds {
		return errSlowRPCThreshold
	}
	s.threshold.Store(milliseconds * int64(time.Millisecond))
	return nil
}

// Threshold returns the configured value, including zero for disabled owners.
func (s *SlowRPCInterceptor) Threshold() time.Duration {
	if s == nil {
		return 0
	}
	return time.Duration(s.threshold.Load())
}

// NewSlowRPCInterceptorProvider is the wire seam that pulls the
// configured threshold out of ObservabilityConfig. Splitting this from
// NewSlowRPCInterceptor lets tests construct the interceptor with an
// explicit duration without needing a full Config.
func NewSlowRPCInterceptorProvider(obs ObservabilityConfig, logger *slog.Logger) *SlowRPCInterceptor {
	return NewSlowRPCInterceptor(obs.SlowRPCThreshold, logger)
}

// Enabled reports current emission capability, not whether to mount the owner.
// The owner stays installed while disabled so later settings can enable it.
func (s *SlowRPCInterceptor) Enabled() bool {
	return s != nil && s.Threshold() > 0 && s.logger != nil
}
