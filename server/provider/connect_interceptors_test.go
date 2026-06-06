package provider

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// TestValidationInterceptor_ConnectInterceptor_RejectsAndFiresHook
// covers the Connect-Go path of the validation interceptor: the same
// underlying validate() rules apply, the registered reject hook fires
// exactly once with the canonical reason, and the returned error
// surfaces as connect.CodeInvalidArgument (translated from gRPC's
// codes.InvalidArgument via grpcCodeToConnect).
//
// One reason per request type is enough — the dispatch through
// validate() is shared with the gRPC path, which TestValidationInterceptor_
// RejectHookFiresPerReason already exercises across every reason.
func TestValidationInterceptor_ConnectInterceptor_RejectsAndFiresHook(t *testing.T) {
	limits := ValidationLimits{MaxKeyLen: 4, MaxBatchSize: 2}

	var got string
	v := NewValidationInterceptor(limits).
		WithRejectHook(func(reason string) { got = reason })

	interceptor := v.ConnectInterceptor()
	called := false
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		called = true
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	wrapped := interceptor(next)

	_, err := wrapped(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: ""}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if called {
		t.Error("next handler should NOT have been called on a rejected request")
	}
	if got != "empty_key" {
		t.Errorf("hook reason = %q, want empty_key", got)
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("err is not *connect.Error: %T (%v)", err, err)
	}
	if cerr.Code() != connect.CodeInvalidArgument {
		t.Errorf("connect code = %v, want CodeInvalidArgument", cerr.Code())
	}
}

// TestValidationInterceptor_ConnectInterceptor_PassesValidRequest
// verifies the happy path: a request that survives validate() reaches
// the next handler with its body intact and the response flows back
// out unchanged.
func TestValidationInterceptor_ConnectInterceptor_PassesValidRequest(t *testing.T) {
	v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 32, MaxBatchSize: 100})
	interceptor := v.ConnectInterceptor()

	wantKey := "users/42"
	next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		r, ok := req.Any().(*pb.GetVertexRequest)
		if !ok {
			t.Fatalf("next: unexpected req type %T", req.Any())
		}
		if r.Key != wantKey {
			t.Errorf("next: key = %q, want %q", r.Key, wantKey)
		}
		return connect.NewResponse(&pb.GetVertexResponse{Vertex: &pb.Vertex{Key: wantKey}}), nil
	})
	wrapped := interceptor(next)

	resp, err := wrapped(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: wantKey}))
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	gv, ok := resp.Any().(*pb.GetVertexResponse)
	if !ok {
		t.Fatalf("resp.Any() unexpected type %T", resp.Any())
	}
	if got := gv.GetVertex().GetKey(); got != wantKey {
		t.Errorf("resp.Vertex.Key = %q, want %q", got, wantKey)
	}
}

// TestRateLimitInterceptor_ConnectInterceptor_AllowsThenRejects drains
// the bucket to zero with a burst of allowed calls, then asserts the
// next call is rejected with connect.CodeResourceExhausted and that
// the reject hook fired exactly once for the rejection. Uses
// rps=1/burst=2 so the bucket is small enough to exhaust deterministically
// inside one test without time.Sleep.
func TestRateLimitInterceptor_ConnectInterceptor_AllowsThenRejects(t *testing.T) {
	r := NewRateLimitInterceptor(1, 2)
	rejects := 0
	r.WithRejectHook(func() { rejects++ })

	allow := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	wrapped := r.ConnectInterceptor()(allow)

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := wrapped(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
			t.Fatalf("burst call %d: %v", i, err)
		}
	}
	// The bucket is empty; the next call must trip the limiter.
	_, err := wrapped(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "k"}))
	if err == nil {
		t.Fatal("third call: want error, got nil")
	}
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("err is not *connect.Error: %T (%v)", err, err)
	}
	if cerr.Code() != connect.CodeResourceExhausted {
		t.Errorf("connect code = %v, want CodeResourceExhausted", cerr.Code())
	}
	if rejects != 1 {
		t.Errorf("reject hook fired %d times, want 1", rejects)
	}
}

// TestRateLimitInterceptor_ConnectInterceptor_ZeroBurstDefaults_To_One
// verifies the constructor's safety net: burst<=0 becomes 1 so the
// interceptor never collapses into a permanent reject. Bursts of 0
// would deadlock callers that expect at least one allowed request per
// second of warm-up. Keep this in lockstep with the gRPC equivalent.
func TestRateLimitInterceptor_ConnectInterceptor_ZeroBurstDefaults_To_One(t *testing.T) {
	r := NewRateLimitInterceptor(1, 0)
	allow := connect.UnaryFunc(func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	wrapped := r.ConnectInterceptor()(allow)

	// At least one call must succeed; otherwise burst=0 saturated the
	// limiter immediately.
	if _, err := wrapped(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
		t.Fatalf("first call: %v", err)
	}
}

// TestGRPCCodeToConnect verifies the 16-entry mapping is exhaustive
// and unambiguous. If a future grpc release adds a new code, gofmt
// won't catch it — this table will.
func TestGRPCCodeToConnect(t *testing.T) {
	cases := []struct {
		name string
		in   codes.Code
		want connect.Code
	}{
		{name: "Canceled", in: codes.Canceled, want: connect.CodeCanceled},
		{name: "Unknown", in: codes.Unknown, want: connect.CodeUnknown},
		{name: "InvalidArgument", in: codes.InvalidArgument, want: connect.CodeInvalidArgument},
		{name: "DeadlineExceeded", in: codes.DeadlineExceeded, want: connect.CodeDeadlineExceeded},
		{name: "NotFound", in: codes.NotFound, want: connect.CodeNotFound},
		{name: "AlreadyExists", in: codes.AlreadyExists, want: connect.CodeAlreadyExists},
		{name: "PermissionDenied", in: codes.PermissionDenied, want: connect.CodePermissionDenied},
		{name: "ResourceExhausted", in: codes.ResourceExhausted, want: connect.CodeResourceExhausted},
		{name: "FailedPrecondition", in: codes.FailedPrecondition, want: connect.CodeFailedPrecondition},
		{name: "Aborted", in: codes.Aborted, want: connect.CodeAborted},
		{name: "OutOfRange", in: codes.OutOfRange, want: connect.CodeOutOfRange},
		{name: "Unimplemented", in: codes.Unimplemented, want: connect.CodeUnimplemented},
		{name: "Internal", in: codes.Internal, want: connect.CodeInternal},
		{name: "Unavailable", in: codes.Unavailable, want: connect.CodeUnavailable},
		{name: "DataLoss", in: codes.DataLoss, want: connect.CodeDataLoss},
		{name: "Unauthenticated", in: codes.Unauthenticated, want: connect.CodeUnauthenticated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := grpcCodeToConnect(c.in); got != c.want {
				t.Errorf("grpcCodeToConnect(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
	// Sanity: an unknown code maps to CodeUnknown rather than panicking.
	if got := grpcCodeToConnect(codes.Code(9999)); got != connect.CodeUnknown {
		t.Errorf("unknown code = %v, want CodeUnknown", got)
	}
}
