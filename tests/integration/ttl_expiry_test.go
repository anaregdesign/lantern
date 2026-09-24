package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// This file pins Lantern's defining product semantic — data decays — as an
// externally observable contract (#935): a client that writes with a TTL
// must see the entry vanish from every point read once the TTL elapses.
// Reads hide expired-but-not-yet-swept entries lazily (vertices.Has routes
// through Get; edges are hidden unless both endpoints are live, #750), so
// none of these assertions depend on GC timing. Expiry itself does depend on
// the wall clock, hence the poll helper: assert liveness strictly before the
// TTL, then poll with a generous deadline for the flip to ErrNotFound.

const (
	expiryTTL          = 500 * time.Millisecond
	expiryPollDeadline = 10 * time.Second
	expiryPollTick     = 20 * time.Millisecond
)

// eventuallyNotFound polls read until it reports client.ErrNotFound, failing
// the test if the entry is still readable after expiryPollDeadline.
func eventuallyNotFound(t *testing.T, what string, read func() error) {
	t.Helper()
	deadline := time.Now().Add(expiryPollDeadline)
	for {
		err := read()
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still readable %v after its TTL (last err: %v)", what, expiryPollDeadline, err)
		}
		if err != nil {
			t.Fatalf("%s: unexpected error while waiting for expiry: %v", what, err)
		}
		time.Sleep(expiryPollTick)
	}
}

func TestTTL_ExternallyObservableDecay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("vertex expires after its TTL", func(t *testing.T) {
		l, cleanup := newInProcessClient(t)
		defer cleanup()

		if _, err := l.PutVertex(ctx, "mortal", "v", expiryTTL); err != nil {
			t.Fatalf("PutVertex: %v", err)
		}
		if _, err := l.GetVertex(ctx, "mortal"); err != nil {
			t.Fatalf("vertex must be readable before its TTL: %v", err)
		}
		eventuallyNotFound(t, "vertex", func() error {
			_, err := l.GetVertex(ctx, "mortal")
			return err
		})
	})

	t.Run("edge expires after its TTL while endpoints stay live", func(t *testing.T) {
		l, cleanup := newInProcessClient(t)
		defer cleanup()

		if _, err := l.PutVertex(ctx, "tail", 1, time.Hour); err != nil {
			t.Fatalf("PutVertex tail: %v", err)
		}
		if _, err := l.PutVertex(ctx, "head", 2, time.Hour); err != nil {
			t.Fatalf("PutVertex head: %v", err)
		}
		if _, err := l.AddEdge(ctx, "tail", "head", 1, expiryTTL); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if _, err := l.GetEdge(ctx, "tail", "head"); err != nil {
			t.Fatalf("edge must be readable before its TTL: %v", err)
		}
		eventuallyNotFound(t, "edge", func() error {
			_, err := l.GetEdge(ctx, "tail", "head")
			return err
		})
		// The endpoints outlive their edge.
		if _, err := l.GetVertex(ctx, "tail"); err != nil {
			t.Fatalf("tail vertex must survive its edge: %v", err)
		}
	})

	t.Run("expired endpoint hides a still-live edge", func(t *testing.T) {
		// Referential closure (#750): an edge may physically outlive an
		// expired endpoint until GC, but no read may expose it.
		l, cleanup := newInProcessClient(t)
		defer cleanup()

		if _, err := l.PutVertex(ctx, "ephemeral", 1, expiryTTL); err != nil {
			t.Fatalf("PutVertex ephemeral: %v", err)
		}
		if _, err := l.PutVertex(ctx, "durable", 2, time.Hour); err != nil {
			t.Fatalf("PutVertex durable: %v", err)
		}
		if _, err := l.AddEdge(ctx, "ephemeral", "durable", 1, time.Hour); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if _, err := l.GetEdge(ctx, "ephemeral", "durable"); err != nil {
			t.Fatalf("edge must be readable while both endpoints live: %v", err)
		}
		eventuallyNotFound(t, "edge with expired tail", func() error {
			_, err := l.GetEdge(ctx, "ephemeral", "durable")
			return err
		})
	})
}

func TestTTL_BudgetedGCDoesNotDelayWireHidingAndEventuallyReclaimsDangling(t *testing.T) {
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour, GCEdgeBudget: 1}, provider.SearchConfig{})
	svc := service.NewLanternService(cache)
	validation := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, validation.ConnectInterceptor())
	l := newConnectClientFor(t, srv.url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, key := range []string{"a", "b", "c"} {
		if _, err := l.PutVertex(ctx, key, key, time.Hour); err != nil {
			t.Fatalf("PutVertex(%s): %v", key, err)
		}
	}
	if _, err := l.PutVertex(ctx, "short_head", "head", expiryTTL); err != nil {
		t.Fatalf("PutVertex(short_head): %v", err)
	}
	for _, tail := range []string{"a", "b", "c"} {
		if _, err := l.AddEdge(ctx, tail, "short_head", 1, time.Hour); err != nil {
			t.Fatalf("AddEdge(%s): %v", tail, err)
		}
		if _, err := l.GetEdge(ctx, tail, "short_head"); err != nil {
			t.Fatalf("live GetEdge(%s): %v", tail, err)
		}
	}
	eventuallyNotFound(t, "short_head", func() error {
		_, err := l.GetVertex(ctx, "short_head")
		return err
	})
	// No GC has run yet, so all three buckets remain physically present, but
	// the wire contract already hides every edge with the expired endpoint.
	if got := cache.EdgeCount(); got != 3 {
		t.Fatalf("physical edges before GC = %d, want 3", got)
	}
	for _, tail := range []string{"a", "b", "c"} {
		if _, err := l.GetEdge(ctx, tail, "short_head"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("GetEdge(%s) after endpoint expiry = %v, want NotFound", tail, err)
		}
	}

	statsCh := make(chan graphcache.GCSweepStats, 8)
	cache.SetGCHooks(nil, func(time.Duration) {
		select {
		case statsCh <- cache.LastGCSweepStats():
		default:
		}
	})
	watchCtx, stop := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() { cache.Watch(watchCtx, 10*time.Millisecond); close(watchDone) }()
	defer func() { stop(); <-watchDone }()
	var removed int
	for removed < 3 {
		select {
		case stats := <-statsCh:
			if stats.ScannedTails > 1 {
				t.Fatalf("one tick scanned %d tails under budget 1", stats.ScannedTails)
			}
			removed += stats.DanglingRemoved
		case <-ctx.Done():
			t.Fatal("budgeted GC did not reclaim dangling edges before deadline")
		}
	}
	if got := cache.EdgeCount(); got != 0 {
		t.Fatalf("physical edges after bounded GC = %d, want 0", got)
	}
}
