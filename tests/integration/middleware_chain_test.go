package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// TestIntegration_FullMiddlewareChain spins up an h2c httptest server
// mounting the full Connect interceptor chain (validation + rate-limit)
// and drives it through the public SDK Connect client. The closest
// thing to an end-to-end smoke test without hitting the wire.
func TestIntegration_FullMiddlewareChain(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         32,
		MaxBatchSize:      5,
		IlluminateMaxStep: 4,
		IlluminateMaxK:    8,
	})
	// 2 rps / burst 2 — generous enough for the happy path, tight enough
	// that a burst of 10 quickly exhausts the bucket.
	rl := provider.NewRateLimitInterceptor(2, 2)

	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor(), rl.ConnectInterceptor())
	c := newConnectClientFor(t, srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("happy path round-trip", func(t *testing.T) {
		if _, err := c.PutVertex(ctx, "k", "hello", time.Minute); err != nil {
			t.Fatalf("PutVertex: %v", err)
		}
		v, err := c.GetVertex(ctx, "k")
		if err != nil {
			t.Fatalf("GetVertex: %v", err)
		}
		if got, _ := client.StringValue(v); got != "hello" {
			t.Errorf("StringValue = %q, want %q", got, "hello")
		}
	})

	t.Run("validation rejects oversize batch", func(t *testing.T) {
		inputs := make([]client.VertexInput, 0, 6)
		for i := 0; i < 6; i++ {
			inputs = append(inputs, client.VertexInput{
				Key:        string(rune('a' + i)),
				Value:      int64(i),
				Expiration: time.Now().Add(time.Minute),
			})
		}
		// Force a single oversized request by raising the SDK chunk
		// size; the default would split into 5+1 and pass.
		bigClient := newConnectClientFor(t, srv.url, client.WithBatchChunkSize(100))
		_, err := bigClient.PutVertices(ctx, inputs)
		if !errors.Is(err, client.ErrInvalidArgument) {
			t.Fatalf("PutVertices(6) err = %v, want errors.Is(err, ErrInvalidArgument)", err)
		}
	})

	t.Run("illuminate caps enforced", func(t *testing.T) {
		_, err := c.Illuminate(ctx, "k", client.WithBFS(client.BFSOpts{Step: 99, FanOut: 1}))
		if !errors.Is(err, client.ErrInvalidArgument) {
			t.Errorf("Illuminate step=99 err = %v, want errors.Is(err, ErrInvalidArgument)", err)
		}
	})

	t.Run("rate limiter trips under burst", func(t *testing.T) {
		var hit bool
		for i := 0; i < 20; i++ {
			_, err := c.PutVertex(ctx, "rl", int64(i), time.Minute)
			if errors.Is(err, client.ErrResourceExhausted) {
				hit = true
				break
			}
		}
		if !hit {
			t.Error("expected at least one ErrResourceExhausted within 20 rapid calls")
		}
	})
}

// TestIntegration_BatchDeletes exercises the DeleteVertices and
// DeleteEdges RPCs end-to-end through the Connect transport. Uses its
// own server with no rate limiter so the assertions aren't sensitive
// to token bucket state from other tests in this file.
func TestIntegration_BatchDeletes(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache)
	val := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         32,
		MaxBatchSize:      5,
		IlluminateMaxStep: 4,
		IlluminateMaxK:    8,
	})

	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	c := newConnectClientFor(t, srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("delete vertices round-trip", func(t *testing.T) {
		keys := []string{"d1", "d2", "d3"}
		for _, k := range keys {
			if _, err := c.PutVertex(ctx, k, int64(1), time.Minute); err != nil {
				t.Fatalf("PutVertex(%s): %v", k, err)
			}
		}
		if n, err := c.DeleteVertices(ctx, keys); err != nil {
			t.Fatalf("DeleteVertices: %v", err)
		} else if n != len(keys) {
			t.Errorf("DeleteVertices n = %d, want %d", n, len(keys))
		}
		for _, k := range keys {
			if _, err := c.GetVertex(ctx, k); !errors.Is(err, client.ErrNotFound) {
				t.Errorf("GetVertex(%s) after delete: err = %v, want errors.Is(err, ErrNotFound)", k, err)
			}
		}
	})

	t.Run("delete edges round-trip", func(t *testing.T) {
		for _, k := range []string{"ea", "eb", "ex"} {
			if _, err := c.PutVertex(ctx, k, int64(0), time.Minute); err != nil {
				t.Fatalf("PutVertex(%s): %v", k, err)
			}
		}
		if _, err := c.PutEdge(ctx, "ea", "eb", 1.0, time.Minute); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
		if _, err := c.PutEdge(ctx, "ea", "ex", 1.0, time.Minute); err != nil {
			t.Fatalf("PutEdge: %v", err)
		}
		refs := []client.EdgeRef{{Tail: "ea", Head: "eb"}, {Tail: "ea", Head: "ex"}}
		if n, err := c.DeleteEdges(ctx, refs); err != nil {
			t.Fatalf("DeleteEdges: %v", err)
		} else if n != len(refs) {
			t.Errorf("DeleteEdges n = %d, want %d", n, len(refs))
		}
		for _, r := range refs {
			if _, err := c.GetEdge(ctx, r.Tail, r.Head); !errors.Is(err, client.ErrNotFound) {
				t.Errorf("GetEdge(%s,%s) after delete: err = %v, want errors.Is(err, ErrNotFound)", r.Tail, r.Head, err)
			}
		}
	})

	t.Run("delete vertices empty is no-op", func(t *testing.T) {
		if n, err := c.DeleteVertices(ctx, nil); err != nil {
			t.Errorf("DeleteVertices(nil): %v", err)
		} else if n != 0 {
			t.Errorf("DeleteVertices(nil) n = %d, want 0", n)
		}
	})

	t.Run("delete edges empty is no-op", func(t *testing.T) {
		if n, err := c.DeleteEdges(ctx, nil); err != nil {
			t.Errorf("DeleteEdges(nil): %v", err)
		} else if n != 0 {
			t.Errorf("DeleteEdges(nil) n = %d, want 0", n)
		}
	})

	t.Run("validation rejects oversize delete vertices", func(t *testing.T) {
		bigClient := newConnectClientFor(t, srv.url, client.WithBatchChunkSize(100))
		keys := make([]string, 6)
		for i := range keys {
			keys[i] = string(rune('a' + i))
		}
		if _, err := bigClient.DeleteVertices(ctx, keys); !errors.Is(err, client.ErrInvalidArgument) {
			t.Errorf("DeleteVertices(6) err = %v, want errors.Is(err, ErrInvalidArgument)", err)
		}
	})

	t.Run("validation rejects oversize delete edges", func(t *testing.T) {
		bigClient := newConnectClientFor(t, srv.url, client.WithBatchChunkSize(100))
		refs := make([]client.EdgeRef, 6)
		for i := range refs {
			refs[i] = client.EdgeRef{Tail: string(rune('a' + i)), Head: "z"}
		}
		if _, err := bigClient.DeleteEdges(ctx, refs); !errors.Is(err, client.ErrInvalidArgument) {
			t.Errorf("DeleteEdges(6) err = %v, want errors.Is(err, ErrInvalidArgument)", err)
		}
	})
}
