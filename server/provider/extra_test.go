package provider

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
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
		{"bad_contrib_id", "bad_contrib_id", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.DeleteEdgeContributionRequest{
				Tail: "a", Head: "b", ContribId: make([]byte, 24),
			})
		}},
		{"step_too_large", "step_too_large", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.IlluminateRequest{Seed: "s",
				Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 99}}}) // > IlluminateMaxStep=3
		}},
		{"k_too_large", "k_too_large", func(t *testing.T, v *ValidationInterceptor) error {
			return connectCallValidator(t, v, &pb.IlluminateRequest{Seed: "s",
				Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 99}}}) // > IlluminateMaxK=5
		}},
		{"ppr_top_n_too_large", "k_too_large", func(t *testing.T, v *ValidationInterceptor) error {
			// IlluminateMaxK caps ppr.top_n on the PPR arm too (#846).
			return connectCallValidator(t, v, &pb.IlluminateRequest{Seed: "s",
				Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 99}}}) // > IlluminateMaxK=5
		}},
		{"community_max_size_too_large", "k_too_large", func(t *testing.T, v *ValidationInterceptor) error {
			// IlluminateMaxK caps community.max_size on the community arm (#845).
			return connectCallValidator(t, v, &pb.IlluminateRequest{Seed: "s",
				Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{MaxSize: 99}}}) // > IlluminateMaxK=5
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

func TestValidationInterceptorValidatesSingularAddEdge(t *testing.T) {
	v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 4, MaxBatchSize: 2})
	for _, tc := range []struct {
		name string
		edge *pb.Edge
	}{
		{name: "nil"},
		{name: "empty tail", edge: &pb.Edge{Head: "b", Weight: 1}},
		{name: "empty head", edge: &pb.Edge{Tail: "a", Weight: 1}},
		{name: "long key", edge: &pb.Edge{Tail: "abcde", Head: "b", Weight: 1}},
		{name: "NaN", edge: &pb.Edge{Tail: "a", Head: "b", Weight: float32(math.NaN())}},
		{name: "infinity", edge: &pb.Edge{Tail: "a", Head: "b", Weight: float32(math.Inf(1))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := connectCallValidator(t, v, &pb.AddEdgeRequest{Edge: tc.edge}); err == nil {
				t.Fatal("invalid singular AddEdge passed validation")
			}
		})
	}
	connectCallValidatorOK(t, v, &pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 1},
	})
}

func TestValidationInterceptorValidatesSingularAndPluralPutEdge(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edge   *pb.Edge
		reason string
	}{
		{"nil", nil, "nil_item"},
		{"empty tail", &pb.Edge{Head: "b", Weight: 1}, "empty_key"},
		{"empty head", &pb.Edge{Tail: "a", Weight: 1}, "empty_key"},
		{"long key", &pb.Edge{Tail: "abcde", Head: "b", Weight: 1}, "key_too_long"},
		{"NaN", &pb.Edge{Tail: "a", Head: "b", Weight: float32(math.NaN())}, "bad_weight"},
		{"positive infinity", &pb.Edge{Tail: "a", Head: "b", Weight: float32(math.Inf(1))}, "bad_weight"},
		{"negative infinity", &pb.Edge{Tail: "a", Head: "b", Weight: float32(math.Inf(-1))}, "bad_weight"},
	} {
		for _, call := range []struct {
			name string
			run  func(*testing.T, *ValidationInterceptor, *pb.Edge) error
		}{
			{"singular", func(t *testing.T, v *ValidationInterceptor, edge *pb.Edge) error {
				return connectCallValidator(t, v, &pb.PutEdgeRequest{Edge: edge})
			}},
			{"plural", func(t *testing.T, v *ValidationInterceptor, edge *pb.Edge) error {
				return connectCallValidator(t, v, &pb.PutEdgesRequest{Edges: []*pb.Edge{edge}})
			}},
		} {
			t.Run(call.name+"/"+tc.name, func(t *testing.T) {
				var reason string
				v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 4, MaxBatchSize: 2}).
					WithRejectHook(func(got string) { reason = got })
				err := call.run(t, v, tc.edge)
				if connect.CodeOf(err) != connect.CodeInvalidArgument || reason != tc.reason {
					t.Fatalf("validation = (%v, %q), want InvalidArgument / %q", err, reason, tc.reason)
				}
			})
		}
	}
	v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 4, MaxBatchSize: 2})
	connectCallValidatorOK(t, v, &pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "a", Head: "b", Weight: math.MaxFloat32},
	})
	connectCallValidatorOK(t, v, &pb.PutEdgesRequest{
		Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: -math.MaxFloat32}},
	})
}

func TestValidationInterceptorValidatesContributionDeletes(t *testing.T) {
	v := NewValidationInterceptor(ValidationLimits{MaxKeyLen: 4, MaxBatchSize: 2})
	id := bytes.Repeat([]byte{1}, 24)
	key := &pb.EdgeContributionKey{Tail: "tail", Head: "head", ContribId: id}
	connectCallValidatorOK(t, v, &pb.DeleteEdgeContributionRequest{
		Tail: key.GetTail(), Head: key.GetHead(), ContribId: key.GetContribId(),
	})
	connectCallValidatorOK(t, v, &pb.DeleteEdgeContributionsRequest{
		Contributions: []*pb.EdgeContributionKey{key, key},
	})
	for _, req := range []*pb.DeleteEdgeContributionsRequest{
		{},
		{Contributions: []*pb.EdgeContributionKey{nil}},
		{Contributions: []*pb.EdgeContributionKey{key, key, key}},
		{Contributions: []*pb.EdgeContributionKey{{Tail: "toolong", Head: "b", ContribId: id}}},
		{Contributions: []*pb.EdgeContributionKey{{Tail: "a", Head: "b", ContribId: id[:23]}}},
	} {
		if err := connectCallValidator(t, v, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid contribution Delete %v returned %v", req, err)
		}
	}
}

func TestValidationInterceptorReceiptStatusBatchUsesLowerEffectiveLimit(t *testing.T) {
	id := make([]byte, 49)
	tests := []struct {
		name       string
		configured int
		count      int
	}{
		{name: "configured limit", configured: 2, count: 3},
		{
			name:       "handler hard limit",
			configured: dataruntime.MaxReceiptStatusBatchSize + 1,
			count:      dataruntime.MaxReceiptStatusBatchSize + 1,
		},
		{
			name:  "unlimited config still uses handler hard limit",
			count: dataruntime.MaxReceiptStatusBatchSize + 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ids := make([][]byte, tc.count)
			for i := range ids {
				ids[i] = id
			}
			v := NewValidationInterceptor(ValidationLimits{MaxBatchSize: tc.configured})
			err := connectCallValidator(t, v, &pb.GetReceiptStatusesRequest{OperationIds: ids})
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("status batch validation = %v, want InvalidArgument", err)
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

func TestRateLimitInterceptor_ConfigurationValidation(t *testing.T) {
	r := NewRateLimitInterceptor(2, 3)
	want := RateLimitConfig{RPS: 2, Burst: 3}
	for _, policy := range []RateLimitConfig{
		{RPS: math.NaN(), Burst: 1}, {RPS: math.Inf(1), Burst: 1},
		{RPS: math.Inf(-1)}, {RPS: -1}, {Burst: 1}, {RPS: 1}, {RPS: 1, Burst: -1},
		{RPS: math.Copysign(0, -1)},
	} {
		if err := r.Configure(policy.RPS, policy.Burst); err == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
		if actual, err := r.Configuration(); err != nil || actual != want {
			t.Fatalf("invalid update changed configuration: %+v, %v", actual, err)
		}
	}
	for _, rps := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		invalid := NewRateLimitInterceptor(rps, 1)
		if _, err := invalid.Configuration(); err == nil || invalid.Allow() {
			t.Fatalf("nonfinite startup rate must be visibly invalid and closed: %v", rps)
		}
		if err := invalid.Configure(1, 1); err != nil || !invalid.Allow() {
			t.Fatalf("valid update did not recover invalid owner: %v", err)
		}
	}
	for _, tc := range []struct {
		rps   float64
		burst int
		want  RateLimitConfig
	}{
		{-1, 10, RateLimitConfig{}}, {0, 10, RateLimitConfig{}},
		{2.5, 0, RateLimitConfig{RPS: 2.5, Burst: 2}},
		{0.5, -1, RateLimitConfig{RPS: 0.5, Burst: 1}},
	} {
		actual, err := NewRateLimitInterceptor(tc.rps, tc.burst).Configuration()
		if err != nil || actual != tc.want {
			t.Fatalf("startup policy (%v, %d) = %+v, %v; want %+v", tc.rps, tc.burst, actual, err, tc.want)
		}
	}
}

func TestRateLimitInterceptor_RetainsCreditOnUpdates(t *testing.T) {
	now := time.Now()
	r := NewRateLimitInterceptor(0.0001, 2)
	r.now = func() time.Time { return now }
	for index, want := range []bool{true, true, false} {
		if r.Allow() != want {
			t.Fatalf("initial burst request %d: want admitted=%v", index, want)
		}
	}
	for _, policy := range []RateLimitConfig{{RPS: 0.0001, Burst: 2}, {RPS: 0.0002, Burst: 10}, {RPS: 0.0002, Burst: 1}} {
		if err := r.Configure(policy.RPS, policy.Burst); err != nil {
			t.Fatal(err)
		}
		if r.Allow() {
			t.Fatalf("enabled update minted fresh credit: %+v", policy)
		}
	}
	if err := r.Configure(0, 0); err != nil || !r.Allow() {
		t.Fatalf("disable failed: %v", err)
	}
	if err := r.Configure(0.0001, 2); err != nil {
		t.Fatal(err)
	}
	for index, want := range []bool{true, true, false} {
		if r.Allow() != want {
			t.Fatalf("explicit re-enable request %d: want admitted=%v", index, want)
		}
	}
	full := NewRateLimitInterceptor(0.0001, 10)
	full.now = func() time.Time { return now }
	if err := full.Configure(0.0001, 1); err != nil || !full.Allow() || full.Allow() {
		t.Fatalf("shrink must cap previously unspent credit: %v", err)
	}
}

func TestRateLimitInterceptor_ConcurrentAtomicPair(t *testing.T) {
	r := NewRateLimitInterceptor(1, 2)
	var group sync.WaitGroup
	for worker := range 8 {
		group.Go(func() {
			for range 100 {
				if worker%2 == 0 {
					policy := RateLimitConfig{RPS: float64(worker + 1), Burst: worker + 2}
					if err := r.Configure(policy.RPS, policy.Burst); err != nil {
						t.Error(err)
					}
				} else {
					r.Allow()
				}
				// Observe the complete pair at the same boundary used by Allow.
				r.mu.Lock()
				if r.lim.Limit() != rate.Limit(r.policy.RPS) || r.lim.Burst() != r.policy.Burst || r.policy.Burst != int(r.policy.RPS)+1 {
					t.Error("admission observed a partially installed pair")
				}
				r.mu.Unlock()
			}
		})
	}
	group.Wait()
}

func TestRateLimitInterceptor_ManagedGenerationAndCredit(t *testing.T) {
	owner := NewRateLimitInterceptor(0.0001, 2)
	owner.now = func() time.Time { return time.Unix(100, 0) }
	for index, want := range []bool{true, true, false} {
		if got := owner.Allow(); got != want {
			t.Fatalf("bootstrap admission %d=%v; want %v", index, got, want)
		}
	}
	policy := RateLimitConfig{RPS: 0.0002, Burst: 3}
	if err := owner.applyManagedRate(policy, 3); err != nil {
		t.Fatal(err)
	}
	for _, generation := range []uint64{3, 4} {
		if err := owner.applyManagedRate(policy, generation); err != nil || owner.Allow() {
			t.Fatal("managed replay or successor replenished enabled credit", err)
		}
		actual := owner.Snapshot()
		if actual.Config != policy || actual.Generation != generation || actual.Error != nil {
			t.Fatalf("atomic policy/generation observation = %+v", actual)
		}
	}
	for _, candidate := range []struct {
		policy     RateLimitConfig
		generation uint64
	}{
		{policy, 0}, {policy, 2}, {RateLimitConfig{RPS: 1, Burst: 1}, 4},
		{RateLimitConfig{RPS: math.NaN(), Burst: 1}, 5}, {RateLimitConfig{RPS: math.Copysign(0, -1)}, 5},
	} {
		if err := owner.applyManagedRate(candidate.policy, candidate.generation); err == nil {
			t.Fatal("invalid or conflicting managed generation accepted", candidate.generation)
		}
	}
	if err := owner.Configure(0, 0); err == nil || owner.Snapshot().Generation != 4 || owner.Allow() {
		t.Fatal("ordinary configuration overrode adopted authority", err)
	}
	if err := owner.applyManagedRate(RateLimitConfig{}, 5); err != nil || !owner.Allow() {
		t.Fatal("managed disable failed", err)
	}
	if err := owner.applyManagedRate(RateLimitConfig{RPS: 0.0001, Burst: 1}, 6); err != nil {
		t.Fatal(err)
	}
	first, second := owner.Allow(), owner.Allow()
	if !first || second {
		t.Fatal("explicit disabled-to-enabled generation has incorrect initial burst")
	}
}

func TestRateLimitInterceptor_ManagedConcurrentSnapshot(t *testing.T) {
	owner := NewRateLimitInterceptor(0, 0)
	var workers sync.WaitGroup
	workers.Go(func() {
		for generation := uint64(1); generation <= 100; generation++ {
			policy := RateLimitConfig{RPS: float64(generation), Burst: int(generation) + 1}
			if err := owner.applyManagedRate(policy, generation); err != nil {
				t.Error(err)
			}
		}
	})
	for range 4 {
		workers.Go(func() {
			for range 100 {
				actual := owner.Snapshot()
				if actual.Error != nil || actual.Generation != 0 && (actual.Config.RPS != float64(actual.Generation) || actual.Config.Burst != int(actual.Generation)+1) {
					t.Errorf("partially installed policy/generation: %+v", actual)
				}
				owner.Allow()
			}
		})
	}
	workers.Wait()
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
