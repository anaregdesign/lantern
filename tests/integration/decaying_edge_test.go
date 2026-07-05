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

// This file pins the server-free exponential edge-weight decay helper (#952)
// as an externally observable contract over the real Connect/h2c wire path.
// AddDecayingEdge expands a geometric target curve into staggered-TTL additive
// contributions client-side; the server sees only an ordinary AddEdges batch,
// so the guarantees under test are: (1) the live weight right after the add is
// the target InitialWeight — S(0), NOT the sum of the raw per-step schedule —
// (2) the weight decays monotonically and the edge eventually vanishes, and
// (3) a curve whose horizon overruns the server's tombstone-TTL is rejected
// whole (all-or-nothing), never written as a truncated staircase.

// newTombstoneClient stands up an in-process server whose delete/expiration
// retention window (LANTERN_TOMBSTONE_TTL) is clamped to ttl, so a write whose
// expiration exceeds now+ttl is rejected with InvalidArgument — the guard the
// decay-horizon rejection test exercises.
func newTombstoneClient(t *testing.T, ttl time.Duration) *client.Lantern {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithTombstoneTTL(ttl)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	return newConnectClientFor(t, srv.url)
}

func TestAddDecayingEdge_ExternallyObservableDecay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// seedEndpoints writes both endpoints with a long TTL so referential
	// closure (#750) never hides the edge before its own contributions decay —
	// isolating edge-weight decay as the only variable.
	seedEndpoints := func(t *testing.T, l *client.Lantern, tail, head string) {
		t.Helper()
		if err := l.PutVertex(ctx, tail, 1, time.Hour); err != nil {
			t.Fatalf("PutVertex %s: %v", tail, err)
		}
		if err := l.PutVertex(ctx, head, 2, time.Hour); err != nil {
			t.Fatalf("PutVertex %s: %v", head, err)
		}
	}

	t.Run("immediate live weight is InitialWeight, not the raw-schedule sum", func(t *testing.T) {
		l, cleanup := newInProcessClient(t)
		defer cleanup()
		seedEndpoints(t, l, "a", "b")

		// 16 → 8 → 4 → 2 → 1 → 0. The contributions are 8,4,2,1,1 (sum 16); a
		// naive 16,8,4,2,1 schedule would read 31 right after the add. The
		// returned effective weight and an immediate GetEdge must both be ~16.
		opts := client.DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: time.Second}
		effective, err := l.AddDecayingEdge(ctx, "a", "b", opts)
		if err != nil {
			t.Fatalf("AddDecayingEdge: %v", err)
		}
		if effective <= 14 || effective >= 18 {
			t.Fatalf("returned effective weight = %v, want ~16 (NOT 31 — the raw-schedule sum)", effective)
		}
		edge, err := l.GetEdge(ctx, "a", "b")
		if err != nil {
			t.Fatalf("GetEdge right after add: %v", err)
		}
		if edge.Weight <= 14 || edge.Weight >= 18 {
			t.Fatalf("live edge weight = %v, want ~16 (NOT 31)", edge.Weight)
		}
	})

	t.Run("weight decays after the first step and the edge eventually vanishes", func(t *testing.T) {
		l, cleanup := newInProcessClient(t)
		defer cleanup()
		seedEndpoints(t, l, "c", "d")

		opts := client.DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: time.Second}
		if _, err := l.AddDecayingEdge(ctx, "c", "d", opts); err != nil {
			t.Fatalf("AddDecayingEdge: %v", err)
		}

		// After more than one interval the first contribution (8, expiring at
		// +1s) has dropped from the live sum, so the weight is strictly below
		// InitialWeight yet still positive. The read excludes expired
		// contributions deterministically by absolute expiration, so this does
		// not depend on GC timing — only on the wall clock passing +1s.
		time.Sleep(1500 * time.Millisecond)
		edge, err := l.GetEdge(ctx, "c", "d")
		if err != nil {
			t.Fatalf("GetEdge mid-decay: %v", err)
		}
		if edge.Weight <= 0 || edge.Weight >= 16 {
			t.Fatalf("mid-decay weight = %v, want 0 < w < 16 (first step decayed)", edge.Weight)
		}

		// Past the full horizon (5×1s) every contribution has expired, so the
		// edge is gone from the point read even though both endpoints live on.
		eventuallyNotFound(t, "decaying edge", func() error {
			_, err := l.GetEdge(ctx, "c", "d")
			return err
		})
		if _, err := l.GetVertex(ctx, "c"); err != nil {
			t.Fatalf("tail vertex must survive the decayed edge: %v", err)
		}
	})
}

// TestAddDecayingEdge_TombstoneTTLRejection pins the horizon guard: when
// Steps×Interval overruns the server's tombstone-TTL, the later contributions'
// expirations exceed now+tombstoneTTL and the whole AddEdges batch is rejected
// with a typed ErrInvalidArgument — the client never sees a partially written
// staircase (single-chunk, validate-before-apply atomicity).
func TestAddDecayingEdge_TombstoneTTLRejection(t *testing.T) {
	l := newTombstoneClient(t, 3*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Endpoints are seeded within the 3s retention window (the tombstone TTL
	// clamps every expiration, endpoints included) and read back immediately,
	// so they are live when we assert the edge was not written.
	if err := l.PutVertex(ctx, "x", 1, 2*time.Second); err != nil {
		t.Fatalf("PutVertex x: %v", err)
	}
	if err := l.PutVertex(ctx, "y", 2, 2*time.Second); err != nil {
		t.Fatalf("PutVertex y: %v", err)
	}

	// Horizon 5s ≫ tombstone 3s: contributions at +4s/+5s exceed the retention
	// window, so the server rejects the whole batch.
	opts := client.DecayOpts{InitialWeight: 16, Ratio: 0.5, Steps: 5, Interval: time.Second}
	_, err := l.AddDecayingEdge(ctx, "x", "y", opts)
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Fatalf("over-horizon decay: got %v, want errors.Is ErrInvalidArgument", err)
	}

	// All-or-nothing: no contribution was written, so the edge does not exist
	// even though both endpoints are still live.
	if _, err := l.GetEdge(ctx, "x", "y"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("rejected decay must write nothing; GetEdge = %v, want ErrNotFound", err)
	}
}
