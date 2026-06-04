package provider

import (
	"bytes"
	"context"
	"log/slog"
	"math"
	"strings"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestValidationInterceptor_RejectHookFiresPerReason walks every
// canonical reason exposed by lantern_validation_rejected_total{reason}
// (excluding bad_ttl and bad_cursor which the service layer owns) and
// asserts that (a) the interceptor returns InvalidArgument and (b) the
// registered hook is invoked with the matching reason exactly once.
func TestValidationInterceptor_RejectHookFiresPerReason(t *testing.T) {
	limits := ValidationLimits{
		MaxKeyLen:         4,
		MaxBatchSize:      2,
		IlluminateMaxStep: 3,
		IlluminateMaxK:    5,
	}
	cases := []struct {
		name   string
		want   string
		req    any
		anyMsg string // optional substring assertion on error message
	}{
		{
			name: "empty_key",
			want: "empty_key",
			req:  &pb.GetVertexRequest{Key: ""},
		},
		{
			name: "key_too_long",
			want: "key_too_long",
			req:  &pb.GetVertexRequest{Key: "abcdef"}, // 6 > MaxKeyLen=4
		},
		{
			name: "empty_batch",
			want: "empty_batch",
			req:  &pb.PutVerticesRequest{Vertices: nil},
		},
		{
			name: "batch_too_large",
			want: "batch_too_large",
			req: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "a"}, {Key: "b"}, {Key: "c"}, // 3 > MaxBatchSize=2
			}},
		},
		{
			name: "nil_item",
			want: "nil_item",
			req: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "a"}, nil,
			}},
		},
		{
			name: "bad_weight",
			want: "bad_weight",
			req: &pb.AddEdgesRequest{Edges: []*pb.Edge{
				{Tail: "a", Head: "b", Weight: float32(math.NaN())},
			}},
		},
		{
			name: "step_too_large",
			want: "step_too_large",
			req:  &pb.IlluminateRequest{Seed: "s", Step: 99}, // > IlluminateMaxStep=3
		},
		{
			name: "k_too_large",
			want: "k_too_large",
			req:  &pb.IlluminateRequest{Seed: "s", Step: 1, K: 99}, // > IlluminateMaxK=5
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			v := NewValidationInterceptor(limits).
				WithRejectHook(func(reason string) { got = reason })
			interceptor := v.UnaryServerInterceptor()
			handler := func(ctx context.Context, req any) (any, error) {
				t.Fatal("handler called on rejected request")
				return nil, nil
			}
			_, err := interceptor(context.Background(), c.req, &grpc.UnaryServerInfo{}, handler)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if code := status.Code(err); code != codes.InvalidArgument {
				t.Errorf("status code = %s, want InvalidArgument", code)
			}
			if got != c.want {
				t.Errorf("reject hook reason = %q, want %q", got, c.want)
			}
			if c.anyMsg != "" && !strings.Contains(err.Error(), c.anyMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), c.anyMsg)
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
	interceptor := v.UnaryServerInterceptor()
	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	}
	if _, err := interceptor(context.Background(), &pb.GetVertexRequest{Key: "k"}, &grpc.UnaryServerInfo{}, handler); err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if !called {
		t.Errorf("handler not invoked on valid request")
	}
	if got != "" {
		t.Errorf("reject hook fired on success path: reason=%q", got)
	}
}

// TestValidationInterceptor_NilHookSafe asserts the reject path does
// not panic when no hook was registered.
func TestValidationInterceptor_NilHookSafe(t *testing.T) {
	v := NewValidationInterceptor(ValidationLimits{})
	interceptor := v.UnaryServerInterceptor()
	handler := func(ctx context.Context, req any) (any, error) { return nil, nil }
	_, err := interceptor(context.Background(), &pb.GetVertexRequest{Key: ""}, &grpc.UnaryServerInfo{}, handler)
	if err == nil || status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

// TestRateLimitInterceptor_RejectHookFires asserts the limiter fires
// the registered hook once per ResourceExhausted return on both the
// unary and the stream interceptor.
func TestRateLimitInterceptor_RejectHookFires(t *testing.T) {
	// rps = small, burst = 1: first call passes, second call blocked.
	r := NewRateLimitInterceptor(0.0001, 1)
	var n int
	r.WithRejectHook(func() { n++ })

	unary := r.UnaryServerInterceptor()
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	if _, err := unary(context.Background(), nil, &grpc.UnaryServerInfo{}, handler); err != nil {
		t.Fatalf("first unary call: %v", err)
	}
	if _, err := unary(context.Background(), nil, &grpc.UnaryServerInfo{}, handler); err == nil {
		t.Fatal("second unary call: expected ResourceExhausted")
	} else if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("second unary call: code = %s, want ResourceExhausted", status.Code(err))
	}
	if n != 1 {
		t.Errorf("hook calls after unary = %d, want 1", n)
	}

	stream := r.StreamServerInterceptor()
	streamHandler := func(srv any, ss grpc.ServerStream) error { return nil }
	if err := stream(nil, nil, &grpc.StreamServerInfo{}, streamHandler); err == nil {
		t.Fatal("stream call: expected ResourceExhausted")
	} else if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("stream call: code = %s, want ResourceExhausted", status.Code(err))
	}
	if n != 2 {
		t.Errorf("hook calls after stream = %d, want 2", n)
	}
}

// TestRateLimitInterceptor_NilHookSafe asserts the reject path does
// not panic when no hook was registered.
func TestRateLimitInterceptor_NilHookSafe(t *testing.T) {
	r := NewRateLimitInterceptor(0.0001, 1)
	unary := r.UnaryServerInterceptor()
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	if _, err := unary(context.Background(), nil, &grpc.UnaryServerInfo{}, handler); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := unary(context.Background(), nil, &grpc.UnaryServerInfo{}, handler); err == nil || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second call: expected ResourceExhausted, got %v", err)
	}
}

// TestValidationInterceptor_LoggerEmitsDebugOnReject asserts that
// WithLogger() causes reject() to emit one debug-level "validation
// rejected" record with the documented reason/msg fields, and that
// successful paths stay silent.
func TestValidationInterceptor_LoggerEmitsDebugOnReject(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 4}).WithLogger(logger)

	// success path: no log records.
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	if _, err := v.UnaryServerInterceptor()(context.Background(), &pb.GetVertexRequest{Key: "k"}, &grpc.UnaryServerInfo{}, handler); err != nil {
		t.Fatalf("success path: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("success path emitted logs: %s", buf.String())
	}

	// reject path: exactly one debug record with reason + msg.
	_, err := v.UnaryServerInterceptor()(context.Background(), &pb.GetVertexRequest{Key: ""}, &grpc.UnaryServerInfo{}, handler)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("reject path code = %s, want InvalidArgument", status.Code(err))
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

	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	_, err := v.UnaryServerInterceptor()(context.Background(), &pb.GetVertexRequest{Key: ""}, &grpc.UnaryServerInfo{}, handler)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("reject path code = %s, want InvalidArgument", status.Code(err))
	}
	if buf.Len() != 0 {
		t.Errorf("info-level logger emitted records: %s", buf.String())
	}
}

// TestValidationInterceptor_NilLoggerSafe asserts that the reject path
// stays safe when WithLogger was never invoked (default install).
func TestValidationInterceptor_NilLoggerSafe(t *testing.T) {
	v := NewValidationInterceptor(ValidationLimits{})
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	_, err := v.UnaryServerInterceptor()(context.Background(), &pb.GetVertexRequest{Key: ""}, &grpc.UnaryServerInfo{}, handler)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("reject path: %v", err)
	}
}
