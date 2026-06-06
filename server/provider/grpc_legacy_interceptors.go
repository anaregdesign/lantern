// Package provider: grpc_legacy_interceptors.go retains the
// gRPC-flavoured UnaryServerInterceptor / StreamServerInterceptor
// methods on ValidationInterceptor, RateLimitInterceptor, and
// SlowRPCInterceptor purely so the legacy integration tests in
// tests/integration/ that build their own grpc.NewServer harnesses to
// exercise *service.LanternService types directly keep compiling.
//
// The primary :6380 listener (lantern_listener.go) does NOT call any
// of these — it uses the ConnectInterceptor() shape declared in
// connect_interceptors.go + connect_middleware.go. Full deletion of
// these methods (and the google.golang.org/grpc dependency they pull
// in) is deferred to #342 as part of the v1.0 dep audit.
package provider

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor returns the gRPC variant of the validation
// interceptor. Production wiring uses ConnectInterceptor() instead;
// this method exists for legacy gRPC-server test harnesses only.
func (v *ValidationInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := v.validate(req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor is a pass-through. Lantern has no streaming
// RPCs validated at the interceptor layer; the wrapper exists for
// future-proofing the gRPC test harness only.
func (v *ValidationInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, ss)
	}
}

// UnaryServerInterceptor returns the gRPC variant of the rate-limit
// interceptor. Production wiring uses ConnectInterceptor() instead.
func (r *RateLimitInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !r.lim.Allow() {
			if r.rejectHook != nil {
				r.rejectHook()
			}
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor is the gRPC stream variant of the rate-limit
// interceptor.
func (r *RateLimitInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !r.lim.Allow() {
			if r.rejectHook != nil {
				r.rejectHook()
			}
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, ss)
	}
}

// UnaryServerInterceptor returns the gRPC variant of the slow-RPC
// interceptor. Production wiring uses ConnectInterceptor() instead.
func (s *SlowRPCInterceptor) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		d := time.Since(start)
		if s.Enabled() && d > s.threshold {
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

// StreamServerInterceptor is the gRPC stream variant of the slow-RPC
// interceptor.
func (s *SlowRPCInterceptor) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		d := time.Since(start)
		if s.Enabled() && d > s.threshold {
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
