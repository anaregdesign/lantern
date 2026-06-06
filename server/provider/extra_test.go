package provider

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// connectCallValidator drives the ValidationInterceptor's Connect
// surface end-to-end: builds a connect.Request from the proto payload,
// invokes the interceptor, and reports back (response-or-nil, error).
// The hop through connect.NewRequest exercises the same code path
// production calls take, so the rejection codes and metadata observed
// here mirror what wire clients see.
func connectCallValidator[T any](t *testing.T, v *ValidationInterceptor, req *T) error {
	t.Helper()
	interceptor := v.ConnectInterceptor()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		t.Fatal("handler called on rejected request")
		return nil, nil
	})
	_, err := interceptor(next)(context.Background(), connect.NewRequest(req))
	return err
}

// connectCallValidatorOK invokes the interceptor on a known-valid
// payload and asserts the handler runs.
func connectCallValidatorOK[T any](t *testing.T, v *ValidationInterceptor, req *T) {
	t.Helper()
	interceptor := v.ConnectInterceptor()
	called := false
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return connect.NewResponse[T](new(T)), nil
	})
	if _, err := interceptor(next)(context.Background(), connect.NewRequest(req)); err != nil {
		t.Fatalf("interceptor returned error on accepted request: %v", err)
	}
	if !called {
		t.Fatal("handler not invoked on valid request")
	}
}

// TestValidationInterceptor_RejectHookFiresPerReason walks every
// canonical reason exposed by lantern_validation_rejected_total{reason}
// (excluding bad_ttl and bad_cursor which the service layer owns) and
// asserts that (a) the Connect interceptor returns CodeInvalidArgument
// and (b) the registered hook is invoked with the matching reason
// exactly once.
func TestValidationInterceptor_RejectHookFiresPerReason(t *testing.T) {
	limits := ValidationLimits{
		MaxKeyLen:         4,
		MaxBatchSize:      2,
		IlluminateMaxStep: 3,
		IlluminateMaxK:    5,
	}
	type c struct {
		name string
		want string
		fn   func(t *testing.T, v *ValidationInterceptor) error
	}
	cases := []c{
		{"empty_key", "empty_key", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.GetVertexRequest{Key: ""})
		}},
		{"key_too_long", "key_too_long", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.GetVertexRequest{Key: "abcdef"}) // 6 > MaxKeyLen=4
		}},
		{"empty_batch", "empty_batch", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.PutVerticesRequest{Vertices: nil})
		}},
		{"batch_too_large", "batch_too_large", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "a"}, {Key: "b"}, {Key: "c"}, // 3 > MaxBatchSize=2
			}})
		}},
		{"nil_item", "nil_item", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "a"}, nil,
			}})
		}},
		{"bad_weight", "bad_weight", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.AddEdgesRequest{Edges: []*pb.Edge{
				{Tail: "a", Head: "b", Weight: float32(math.NaN())},
			}})
		}},
		{"step_too_large", "step_too_large", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.IlluminateRequest{Seed: "s", Step: 99}) // > IlluminateMaxStep=3
		}},
		{"k_too_large", "k_too_large", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.IlluminateRequest{Seed: "s", Step: 1, K: 99}) // > IlluminateMaxK=5
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			v := NewValidationInterceptor(limits).
				WithRejectHook(func(reason string) { got = reason })
			err := tc.fn(t, v)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			var connErr *connect.Error
			if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
				t.Errorf("error = %v, want connect.CodeInvalidArgument", err)
			}
			if got != tc.want {
				t.Errorf("reject hook reason = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestValidationInterceptor_AcceptedRequestDoesNotFireHook asserts the
// hook stays silent when validation passes (handler runs, no reason
// recorded).
func TestValidationInterceptor_AcceptedRequestDoesNotFireHook(t *testing.T) {
	var got string
	v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 16}).
		WithRejectHook(func(reason string) { got = reason })
	connectCallValidatorOK(t, v, &pb.GetVertexRequest{Key: "k"})
	if got != "" {
		t.Errorf("reject hook fired on success path: reason=%q", got)
	}
}

// TestValidationInterceptor_NilHookSafe asserts the reject path does
// not panic when no hook was registered.
func TestValidationInterceptor_NilHookSafe(t *testing.T) {
	v := NewValidationInterceptor(ValidationLimits{})
	err := connectCallValidator(t, v, &pb.GetVertexRequest{Key: ""})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument, got %v", err)
	}
}

// TestRateLimitInterceptor_RejectHookFires asserts the limiter fires
// the registered hook once per CodeResourceExhausted return.
func TestRateLimitInterceptor_RejectHookFires(t *testing.T) {
	// rps = small, burst = 1: first call passes, second call blocked.
	r := NewRateLimitInterceptor(0.0001, 1)
	var n int
	r.WithRejectHook(func() { n++ })

	interceptor := r.ConnectInterceptor()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	if _, err := interceptor(next)(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := interceptor(next)(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"}))
	if err == nil {
		t.Fatal("second call: expected ResourceExhausted")
	}
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeResourceExhausted {
		t.Errorf("second call: code = %v, want ResourceExhausted", err)
	}
	if n != 1 {
		t.Errorf("hook calls after second call = %d, want 1", n)
	}
}

// TestRateLimitInterceptor_NilHookSafe asserts the reject path does
// not panic when no hook was registered.
func TestRateLimitInterceptor_NilHookSafe(t *testing.T) {
	r := NewRateLimitInterceptor(0.0001, 1)
	interceptor := r.ConnectInterceptor()
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	if _, err := interceptor(next)(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := interceptor(next)(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"}))
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeResourceExhausted {
		t.Fatalf("second call: expected CodeResourceExhausted, got %v", err)
	}
}

// TestValidationInterceptor_LoggerEmitsDebugOnReject asserts that
// WithLogger() causes reject() to emit one debug-level "validation
// rejected" record with the documented reason/error fields, and that
// successful paths stay silent.
func TestValidationInterceptor_LoggerEmitsDebugOnReject(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 4}).WithLogger(logger)

	// success path: no log records.
	connectCallValidatorOK(t, v, &pb.GetVertexRequest{Key: "k"})
	if buf.Len() != 0 {
		t.Fatalf("success path emitted logs: %s", buf.String())
	}

	// reject path: exactly one debug record with reason + error.
	err := connectCallValidator(t, v, &pb.GetVertexRequest{Key: ""})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("reject path code = %v, want CodeInvalidArgument", err)
	}
	recs := decodeRecords(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	rec := recs[0]
	if rec["msg"] != "validation rejected" {
		t.Errorf("msg = %v, want %q", rec["msg"], "validation rejected")
	}
	if rec["reason"] != "empty_key" {
		t.Errorf("reason = %v, want %q", rec["reason"], "empty_key")
	}
	if got, _ := rec["error"].(string); !strings.Contains(got, "key") {
		t.Errorf("error field missing or unexpected: %v", rec)
	}
}

// TestValidationInterceptor_LoggerSuppressedBelowDebug asserts the gate
// short-circuits when the logger handler is set above debug, keeping the
// hot path quiet by default.
func TestValidationInterceptor_LoggerSuppressedBelowDebug(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	v := NewValidationInterceptor(ValidationLimits{}).WithLogger(logger)

	err := connectCallValidator(t, v, &pb.GetVertexRequest{Key: ""})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("reject path code = %v, want CodeInvalidArgument", err)
	}
	if buf.Len() != 0 {
		t.Errorf("info-level logger emitted records: %s", buf.String())
	}
}

// TestValidationInterceptor_NilLoggerSafe asserts that the reject path
// stays safe when WithLogger was never invoked (default install).
func TestValidationInterceptor_NilLoggerSafe(t *testing.T) {
	v := NewValidationInterceptor(ValidationLimits{})
	err := connectCallValidator(t, v, &pb.GetVertexRequest{Key: ""})
	var connErr *connect.Error
	if !errors.As(err, &connErr) || connErr.Code() != connect.CodeInvalidArgument {
		t.Fatalf("reject path: %v", err)
	}
}
