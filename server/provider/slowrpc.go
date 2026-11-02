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
//   - policy_generation the captured managed generation; zero is bootstrap
//
// Slow-RPC accounting is purely a logging signal; it does not affect the
// response or any Prometheus counter (PrometheusInterceptor already
// covers latency distributions). The dedicated log makes single-request
// outliers grep-able in JSON log streams.
//
// The Connect interceptor implementation lives in connect_middleware.go
// (SlowRPCInterceptor.ConnectInterceptor).
type SlowRPCInterceptor struct {
	policy atomic.Pointer[SlowRPCPolicySnapshot]
	logger *slog.Logger
	now    func() time.Time
}

// SlowRPCPolicySnapshot is one actual request-entry threshold and generation.
// It contains no desired state or authorization evidence.
type SlowRPCPolicySnapshot struct {
	Threshold  time.Duration
	Generation uint64
}

// NewSlowRPCInterceptor builds an interceptor that emits a warn log per RPC
// exceeding threshold. A zero/negative threshold disables emission (the
// interceptor returns the handler directly without measuring time).
// logger MUST be non-nil; pass slog.Default() if no dedicated logger is
// available.
func NewSlowRPCInterceptor(threshold time.Duration, logger *slog.Logger) *SlowRPCInterceptor {
	s := &SlowRPCInterceptor{logger: logger, now: time.Now}
	initial := SlowRPCPolicySnapshot{}
	// Preserve the startup constructor's historical nonpositive disable mode.
	if threshold > 0 {
		initial.Threshold = threshold
	}
	s.policy.Store(&initial)
	return s
}

var errSlowRPCThreshold = errors.New("slow RPC threshold requires a logging owner and nonnegative representable milliseconds")
var errSlowRPCGeneration = errors.New("slow RPC managed generation conflicts with installed policy")

// SetThresholdMilliseconds publishes one validated policy for future requests.
// Requests already running retain the threshold captured at their entry.
// Once managed ownership is adopted, this bootstrap setter refuses updates.
func (s *SlowRPCInterceptor) SetThresholdMilliseconds(milliseconds int64) error {
	const maxMilliseconds = int64(1<<63-1) / int64(time.Millisecond)
	if s == nil || s.logger == nil || milliseconds < 0 || milliseconds > maxMilliseconds {
		return errSlowRPCThreshold
	}
	next := SlowRPCPolicySnapshot{Threshold: time.Duration(milliseconds) * time.Millisecond}
	for {
		previous := s.policy.Load()
		if previous != nil && previous.Generation != 0 {
			return errSlowRPCGeneration
		}
		if s.policy.CompareAndSwap(previous, &next) {
			return nil
		}
	}
}

// ApplyManagedThresholdMilliseconds installs only committed nonzero
// generations. Exact retries are harmless; stale/conflicting callbacks and
// subsequent bootstrap setters cannot overwrite managed ownership.
func (s *SlowRPCInterceptor) ApplyManagedThresholdMilliseconds(milliseconds int64, generation uint64) error {
	const maxMilliseconds = int64(1<<63-1) / int64(time.Millisecond)
	if s == nil || s.logger == nil || milliseconds < 0 || milliseconds > maxMilliseconds {
		return errSlowRPCThreshold
	}
	if generation == 0 {
		return errSlowRPCGeneration
	}
	next := SlowRPCPolicySnapshot{Threshold: time.Duration(milliseconds) * time.Millisecond, Generation: generation}
	for {
		previous := s.policy.Load()
		if previous != nil {
			if generation < previous.Generation || generation == previous.Generation && next.Threshold != previous.Threshold {
				return errSlowRPCGeneration
			}
			if next == *previous {
				return nil
			}
		}
		if s.policy.CompareAndSwap(previous, &next) {
			return nil
		}
	}
}

// Snapshot returns a detached effective tuple. A missing owner is disabled at
// bootstrap generation zero; it cannot accept a managed application.
func (s *SlowRPCInterceptor) Snapshot() SlowRPCPolicySnapshot {
	if s != nil {
		if current := s.policy.Load(); current != nil {
			return *current
		}
	}
	return SlowRPCPolicySnapshot{}
}

// Threshold returns the configured value, including zero for disabled owners.
func (s *SlowRPCInterceptor) Threshold() time.Duration {
	return s.Snapshot().Threshold
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
