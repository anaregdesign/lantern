package provider

import (
	"context"
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
