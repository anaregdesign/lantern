// Package provider: connect_interceptors.go wires the existing
// ValidationInterceptor and RateLimitInterceptor into Connect-Go's
// connect.UnaryInterceptorFunc shape so the listener picks up the
// shared validation rules, token-bucket policy, reject hooks, and
// slog channels.
package provider

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"

	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
)

// NewValidationInterceptorProvider wires the shared
// *ValidationInterceptor instance the Connect listener mounts as its
// per-request validator. It also publishes the reject hook so
// lantern_validation_rejected_total{reason} keeps incrementing on
// rejection.
func NewValidationInterceptorProvider(
	limits ValidationLimits,
	dm *domainmetrics.DomainMetrics,
	logger *slog.Logger,
) *ValidationInterceptor {
	return NewValidationInterceptor(limits).
		WithRejectHook(dm.OnValidationRejected).
		WithLogger(logger)
}

// NewRateLimitInterceptorProvider wires the shared
// *RateLimitInterceptor instance the Connect listener consults before
// forwarding a call.
//
// When LANTERN_RATE_LIMIT_RPS<=0 the limiter is disabled: a
// *RateLimitInterceptor with lim=nil is returned so the listener
// wiring inside connectHandlerOptions can detect the disabled state
// (rli.lim == nil) and skip the interceptor.
func NewRateLimitInterceptorProvider(
	rl RateLimitConfig,
	dm *domainmetrics.DomainMetrics,
) *RateLimitInterceptor {
	if rl.RPS <= 0 {
		return &RateLimitInterceptor{}
	}
	return NewRateLimitInterceptor(rl.RPS, rl.Burst).
		WithRejectHook(dm.OnRateLimitRejected)
}

// ConnectInterceptor returns a connect.UnaryInterceptorFunc that
// validates every unary Connect call against the same ValidationLimits
// configured on the interceptor.
//
// Streaming RPCs (Subscribe / Snapshot) skip validation because
// validate() only has cases for unary request types. If validation is
// ever extended to streams, mirror the change here.
func (v *ValidationInterceptor) ConnectInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if err := v.validate(req.Any()); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

// ConnectInterceptor returns a connect.UnaryInterceptorFunc that
// drains one token from the shared *rate.Limiter per unary Connect call
// and returns connect.CodeResourceExhausted when the bucket is empty.
//
// The reject hook fires on the rejection path so
// lantern_rate_limit_rejected_total keeps incrementing.
func (r *RateLimitInterceptor) ConnectInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if !r.lim.Allow() {
				if r.rejectHook != nil {
					r.rejectHook()
				}
				return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limit exceeded"))
			}
			return next(ctx, req)
		}
	}
}
