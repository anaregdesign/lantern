// Package provider: connect_interceptors.go wires the existing
// ValidationInterceptor and RateLimitInterceptor into Connect-Go's
// connect.UnaryInterceptorFunc shape so the additive Connect listener
// from #337 — and later the cutover in #347 — pick up the same
// validation rules, token-bucket policy, reject hooks, and slog
// channels the gRPC path uses.
//
// Design constraints (per #349):
//   - Reuse the existing private validate(req any) and r.lim.Allow()
//     logic verbatim. Behaviour MUST be observable as identical from
//     reject-hook callbacks and slog output regardless of transport.
//   - Translate the gRPC status errors the underlying logic returns
//     into the matching connect.Error so wire clients see
//     CodeInvalidArgument / CodeResourceExhausted, not a leaked
//     gRPC-flavoured payload.
//   - The existing UnaryServerInterceptor / StreamServerInterceptor
//     gRPC methods are untouched (additive only — production deletion
//     lives in #347).
package provider

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	domainmetrics "github.com/anaregdesign/lantern/server/metrics"
)

// NewValidationInterceptorProvider wires the shared
// *ValidationInterceptor instance both the gRPC server and the
// additive Connect listener mount as their per-request validator.
// Hoisting construction out of NewGrpcServerOptions (where it used to
// live inline) lets both transports observe the same reject hook and
// slog channel for free — the underlying validate() rules + reason set
// stay in lockstep without duplication.
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
// *RateLimitInterceptor instance both the gRPC server and the
// additive Connect listener consult before forwarding a call.
//
// When LANTERN_RATE_LIMIT_RPS<=0 the limiter is disabled: a
// *RateLimitInterceptor with lim=nil is returned so the gRPC chain
// builder can detect the disabled state (rli.lim == nil) and skip the
// interceptor, mirroring the original RPS-gate behaviour. The Connect
// side does the same check inside connectHandlerOptions.
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
// the gRPC path uses.
//
// The interceptor calls the private validate(req any) shared with the
// gRPC path so the canonical reason set, reject hook, and debug-level
// slog channel stay identical across transports.
//
// Streaming RPCs (Subscribe / Snapshot) skip validation because
// validate() only has cases for unary request types; the gRPC stream
// interceptor (StreamServerInterceptor) is similarly a pass-through.
// If validation is ever extended to streams, mirror the change here.
func (v *ValidationInterceptor) ConnectInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if err := v.validate(req.Any()); err != nil {
				return nil, grpcStatusToConnect(err)
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
// lantern_rate_limit_rejected_total keeps incrementing whether the
// caller hit the gRPC or the Connect surface.
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

// grpcStatusToConnect translates the gRPC status errors the existing
// validate() path returns into matching connect.Errors so wire clients
// receive Connect-flavoured codes and messages. Non-status errors fall
// through unchanged.
//
// The 16-entry table mirrors connect-go's own internal table verbatim
// (the names match by design — Connect's codes are wire-compatible with
// gRPC's). The unknown fallback preserves CodeUnknown semantics for any
// future gRPC code the bridge has not yet learned.
func grpcStatusToConnect(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	return connect.NewError(grpcCodeToConnect(st.Code()), errors.New(st.Message()))
}

func grpcCodeToConnect(c codes.Code) connect.Code {
	switch c {
	case codes.Canceled:
		return connect.CodeCanceled
	case codes.Unknown:
		return connect.CodeUnknown
	case codes.InvalidArgument:
		return connect.CodeInvalidArgument
	case codes.DeadlineExceeded:
		return connect.CodeDeadlineExceeded
	case codes.NotFound:
		return connect.CodeNotFound
	case codes.AlreadyExists:
		return connect.CodeAlreadyExists
	case codes.PermissionDenied:
		return connect.CodePermissionDenied
	case codes.ResourceExhausted:
		return connect.CodeResourceExhausted
	case codes.FailedPrecondition:
		return connect.CodeFailedPrecondition
	case codes.Aborted:
		return connect.CodeAborted
	case codes.OutOfRange:
		return connect.CodeOutOfRange
	case codes.Unimplemented:
		return connect.CodeUnimplemented
	case codes.Internal:
		return connect.CodeInternal
	case codes.Unavailable:
		return connect.CodeUnavailable
	case codes.DataLoss:
		return connect.CodeDataLoss
	case codes.Unauthenticated:
		return connect.CodeUnauthenticated
	default:
		return connect.CodeUnknown
	}
}
