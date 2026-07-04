package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// doubleSendInterceptor invokes the next unary func twice with the same
// request and returns the second result. It models an at-least-once transport
// retry that re-delivers identical request bytes after the server has already
// applied the first attempt — the exact hazard WithIdempotentAdds guards
// against (#588). Streaming RPCs pass through untouched.
type doubleSendInterceptor struct{}

func (doubleSendInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if _, err := next(ctx, req); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (doubleSendInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (doubleSendInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// newIdempotencyHarness stands up a fresh GraphCache-backed service over the
// real Connect/h2c transport and returns an SDK client built with the
// supplied options. Each call owns its own cache so the three subtests below
// stay isolated.
func newIdempotencyHarness(t *testing.T, opts ...client.Option) *client.Lantern {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	return newConnectClientFor(t, srv.url, opts...)
}

// TestAddEdge_IdempotentRetry_SingleContribution is the end-to-end #588
// acceptance check: with WithIdempotentAdds, a duplicate delivery of one
// AddEdge call contributes its weight exactly once, while the legacy additive
// path double-counts it, and distinct user-level calls still sum.
func TestAddEdge_IdempotentRetry_SingleContribution(t *testing.T) {
	ctx := context.Background()

	t.Run("WithIdempotentAdds dedups a re-delivered request", func(t *testing.T) {
		l := newIdempotencyHarness(t,
			client.WithIdempotentAdds(),
			client.WithConnectClientOption(connect.WithInterceptors(doubleSendInterceptor{})),
		)
		if _, err := l.AddEdge(ctx, "a", "b", 2, time.Minute); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		e, err := l.GetEdge(ctx, "a", "b")
		if err != nil {
			t.Fatalf("GetEdge: %v", err)
		}
		if e.Weight != 2 {
			t.Fatalf("idempotent retry must contribute once: weight = %v, want 2", e.Weight)
		}
	})

	t.Run("legacy additive path double-counts a re-delivered request", func(t *testing.T) {
		l := newIdempotencyHarness(t,
			client.WithConnectClientOption(connect.WithInterceptors(doubleSendInterceptor{})),
		)
		if _, err := l.AddEdge(ctx, "a", "b", 2, time.Minute); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		e, err := l.GetEdge(ctx, "a", "b")
		if err != nil {
			t.Fatalf("GetEdge: %v", err)
		}
		if e.Weight != 4 {
			t.Fatalf("default path is additive on re-delivery: weight = %v, want 4", e.Weight)
		}
	})

	t.Run("distinct calls still sum under WithIdempotentAdds", func(t *testing.T) {
		l := newIdempotencyHarness(t, client.WithIdempotentAdds())
		var lastEffective float32
		for i := 0; i < 2; i++ {
			eff, err := l.AddEdge(ctx, "a", "b", 2, time.Minute)
			if err != nil {
				t.Fatalf("AddEdge #%d: %v", i, err)
			}
			lastEffective = eff
		}
		if lastEffective != 4 {
			t.Fatalf("second distinct AddEdge must report accumulated effective weight = 4, got %v", lastEffective)
		}
		e, err := l.GetEdge(ctx, "a", "b")
		if err != nil {
			t.Fatalf("GetEdge: %v", err)
		}
		if e.Weight != 4 {
			t.Fatalf("distinct AddEdge calls get distinct keys and must sum: weight = %v, want 4", e.Weight)
		}
	})
}

// TestAddEdges_BatchSemantics covers the canonical plural additive write over
// the real Connect/h2c wire — the AddEdges surface previously had no
// integration coverage at all (#935): accumulation across calls, the
// index-aligned effective-weights contract (#897), chunked delivery
// (WithBatchChunkSize), and at-least-once re-delivery dedup for the batch
// path under WithIdempotentAdds.
func TestAddEdges_BatchSemantics(t *testing.T) {
	ctx := context.Background()
	exp := time.Now().Add(time.Hour) // EdgeInput carries an absolute expiration; zero would be born-expired

	inputs := func(w0, w1, w2 float32) []client.EdgeInput {
		return []client.EdgeInput{
			{Tail: "a", Head: "b", Weight: w0, Expiration: exp},
			{Tail: "a", Head: "c", Weight: w1, Expiration: exp},
			{Tail: "b", Head: "c", Weight: w2, Expiration: exp},
		}
	}

	t.Run("accumulates and returns index-aligned effective weights", func(t *testing.T) {
		l := newIdempotencyHarness(t)
		eff, err := l.AddEdges(ctx, inputs(1, 2, 3))
		if err != nil {
			t.Fatalf("AddEdges #1: %v", err)
		}
		if len(eff) != 3 || eff[0] != 1 || eff[1] != 2 || eff[2] != 3 {
			t.Fatalf("first AddEdges effective weights = %v, want [1 2 3]", eff)
		}
		eff, err = l.AddEdges(ctx, inputs(1, 2, 3))
		if err != nil {
			t.Fatalf("AddEdges #2: %v", err)
		}
		if len(eff) != 3 || eff[0] != 2 || eff[1] != 4 || eff[2] != 6 {
			t.Fatalf("second AddEdges must report accumulated weights [2 4 6], got %v", eff)
		}
		e, err := l.GetEdge(ctx, "a", "c")
		if err != nil {
			t.Fatalf("GetEdge: %v", err)
		}
		if e.Weight != 4 {
			t.Fatalf("stored weight after two additive batches = %v, want 4", e.Weight)
		}
	})

	t.Run("chunked batch applies every input and stitches effective weights", func(t *testing.T) {
		l := newIdempotencyHarness(t, client.WithBatchChunkSize(2))
		batch := make([]client.EdgeInput, 5)
		for i := range batch {
			batch[i] = client.EdgeInput{
				Tail:       "hub",
				Head:       string(rune('p' + i)),
				Weight:     float32(i + 1),
				Expiration: exp,
			}
		}
		eff, err := l.AddEdges(ctx, batch) // 5 inputs / chunk size 2 → 3 RPCs
		if err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		if len(eff) != len(batch) {
			t.Fatalf("effective weights len = %d, want %d (must span chunks)", len(eff), len(batch))
		}
		for i, in := range batch {
			if eff[i] != in.Weight {
				t.Errorf("eff[%d] = %v, want %v", i, eff[i], in.Weight)
			}
			e, err := l.GetEdge(ctx, in.Tail, in.Head)
			if err != nil {
				t.Fatalf("GetEdge(%s→%s): %v", in.Tail, in.Head, err)
			}
			if e.Weight != in.Weight {
				t.Errorf("stored weight %s→%s = %v, want %v", in.Tail, in.Head, e.Weight, in.Weight)
			}
		}
	})

	t.Run("WithIdempotentAdds dedups a re-delivered batch", func(t *testing.T) {
		l := newIdempotencyHarness(t,
			client.WithIdempotentAdds(),
			client.WithConnectClientOption(connect.WithInterceptors(doubleSendInterceptor{})),
		)
		if _, err := l.AddEdges(ctx, inputs(1, 2, 3)); err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		for _, tc := range inputs(1, 2, 3) {
			e, err := l.GetEdge(ctx, tc.Tail, tc.Head)
			if err != nil {
				t.Fatalf("GetEdge(%s→%s): %v", tc.Tail, tc.Head, err)
			}
			if e.Weight != tc.Weight {
				t.Errorf("re-delivered batch must contribute once: %s→%s weight = %v, want %v",
					tc.Tail, tc.Head, e.Weight, tc.Weight)
			}
		}
	})

	t.Run("legacy batch path double-counts a re-delivered batch", func(t *testing.T) {
		l := newIdempotencyHarness(t,
			client.WithConnectClientOption(connect.WithInterceptors(doubleSendInterceptor{})),
		)
		if _, err := l.AddEdges(ctx, inputs(1, 2, 3)); err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		for _, tc := range inputs(1, 2, 3) {
			e, err := l.GetEdge(ctx, tc.Tail, tc.Head)
			if err != nil {
				t.Fatalf("GetEdge(%s→%s): %v", tc.Tail, tc.Head, err)
			}
			if e.Weight != 2*tc.Weight {
				t.Errorf("default path is additive on re-delivery: %s→%s weight = %v, want %v",
					tc.Tail, tc.Head, e.Weight, 2*tc.Weight)
			}
		}
	})
}
