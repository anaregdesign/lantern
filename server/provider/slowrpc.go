package provider

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// SlowRPCInterceptor measures each RPC's wall-clock duration and emits a
// warn-level slog line ("slow rpc") whenever the duration exceeds the
// configured threshold (#223). Threshold == 0 disables emission entirely.
//
// Fields:
//   - method        the gRPC method name (info.FullMethod)
//   - code          the resulting gRPC status code (string form)
//   - duration_ms   handler wall-clock in milliseconds (int64)
//   - threshold_ms  the configured threshold in milliseconds (int64)
//
// Slow-RPC accounting is purely a logging signal; it does not affect the
// response or any Prometheus counter (the existing grpc-middleware
// histogram already covers latency distributions). The dedicated log
// makes single-request outliers grep-able in JSON log streams.
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

// Enabled reports whether the interceptor will measure / emit logs. Used by
// the wiring layer to decide whether to install the interceptor at all,
// keeping the hot path identical to pre-#223 when the threshold is 0.
func (s *SlowRPCInterceptor) Enabled() bool { return s != nil && s.threshold > 0 && s.logger != nil }

func (s *SlowRPCInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		d := time.Since(start)
		if d > s.threshold {
			s.logger.LogAttrs(ctx, slog.LevelWarn, "slow rpc",
				slog.String("method", info.FullMethod),
				slog.String("code", status.Code(err).String()),
				slog.Int64("duration_ms", d.Milliseconds()),
				slog.Int64("threshold_ms", s.threshold.Milliseconds()),
			)
		}
		return resp, err
	}
}

func (s *SlowRPCInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		d := time.Since(start)
		if d > s.threshold {
			s.logger.LogAttrs(ss.Context(), slog.LevelWarn, "slow rpc",
				slog.String("method", info.FullMethod),
				slog.String("code", status.Code(err).String()),
				slog.Int64("duration_ms", d.Milliseconds()),
				slog.Int64("threshold_ms", s.threshold.Milliseconds()),
			)
		}
		return err
	}
}
