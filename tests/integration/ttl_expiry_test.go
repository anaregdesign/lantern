package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
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

		if err := l.PutVertex(ctx, "mortal", "v", expiryTTL); err != nil {
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

		if err := l.PutVertex(ctx, "tail", 1, time.Hour); err != nil {
			t.Fatalf("PutVertex tail: %v", err)
		}
		if err := l.PutVertex(ctx, "head", 2, time.Hour); err != nil {
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

		if err := l.PutVertex(ctx, "ephemeral", 1, expiryTTL); err != nil {
			t.Fatalf("PutVertex ephemeral: %v", err)
		}
		if err := l.PutVertex(ctx, "durable", 2, time.Hour); err != nil {
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
