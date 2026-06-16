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
		if err := l.AddEdge(ctx, "a", "b", 2, time.Minute); err != nil {
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
		if err := l.AddEdge(ctx, "a", "b", 2, time.Minute); err != nil {
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
		for i := 0; i < 2; i++ {
			if err := l.AddEdge(ctx, "a", "b", 2, time.Minute); err != nil {
				t.Fatalf("AddEdge #%d: %v", i, err)
			}
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
