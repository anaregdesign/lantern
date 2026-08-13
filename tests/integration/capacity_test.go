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

// newCapacityClient stands up an in-process server with the #848 aggregate
// soft caps configured and (optionally) the GC loop running on a fast tick,
// so the test can observe TTL decay freeing capacity end-to-end.
func newCapacityClient(t *testing.T, limits service.CapacityLimits, gcTick time.Duration) *client.Lantern {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	if gcTick > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go cache.Watch(ctx, gcTick)
	}
	svc := service.NewLanternService(cache).WithCapacityLimits(limits)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	return newConnectClientFor(t, srv.url)
}

// TestCapacityCap_SDKSurface fills the vertex cap through the SDK and pins
// the client-visible contract: the rejection surfaces as a typed
// ErrResourceExhausted (retryable-after-decay), deletes free capacity
// immediately, and TTL decay + GC frees capacity without any client action.
func TestCapacityCap_SDKSurface(t *testing.T) {
	l := newCapacityClient(t, service.CapacityLimits{MaxVertices: 3}, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	longExp := time.Now().Add(time.Minute)
	if _, err := l.PutVertices(ctx, []client.VertexInput{
		{Key: "keep-1", Value: "v", Expiration: longExp},
		{Key: "keep-2", Value: "v", Expiration: longExp},
		{Key: "short", Value: "v", Expiration: time.Now().Add(300 * time.Millisecond)},
	}); err != nil {
		t.Fatalf("fill to cap: %v", err)
	}
	_, // At capacity: the typed sentinel must surface through the SDK.
		err := l.PutVertex(ctx, "overflow", "v", time.Minute)
	if !errors.Is(err, client.ErrResourceExhausted) {
		t.Fatalf("at-cap put: got %v, want errors.Is ErrResourceExhausted", err)
	}

	// TTL decay frees capacity: once "short" expires and a GC tick flushes
	// it, the same write goes through with no deletes issued.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err = l.PutVertex(ctx, "overflow", "v", time.Minute)
		if err == nil {
			break
		}
		if !errors.Is(err, client.ErrResourceExhausted) {
			t.Fatalf("unexpected error while waiting for decay: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("capacity never freed by TTL decay: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, // Back at capacity; a delete frees a slot immediately.
		err = l.PutVertex(ctx, "one-more", "v", time.Minute)
	if !errors.Is(err, client.ErrResourceExhausted) {
		t.Fatalf("re-filled cap: got %v, want ErrResourceExhausted", err)
	}
	if _, err := l.DeleteVertex(ctx, "keep-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := l.PutVertex(ctx, "one-more", "v", time.Minute); err != nil {
		t.Fatalf("put after delete: %v", err)
	}
}
