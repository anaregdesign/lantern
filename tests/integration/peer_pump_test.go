package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

// pumpNode is a tiny harness that stands up LanternService +
// LanternReplicationService on an h2c httptest.Server reachable by
// host:port (so other nodes' Pump instances can dial it via
// graphv1connect's HTTP client). Mirrors the legacy bufconn-flavoured
// helper the pre-#363 suite used; the migration to Connect is
// observationally equivalent at the wire level.
type pumpNode struct {
	url    string // full http://host:port URL (used by pump dialer + replication client)
	cache  *graphcache.GraphCache[string, *pb.Vertex]
	clock  *hlc.Clock
	log    *mutationlog.Log
	svc    *service.LanternService
	sdk    *client.Lantern
	pump   *replication.Pump
	nodeID hlc.NodeID
}

func newPumpNode(t *testing.T, nodeID hlc.NodeID) *pumpNode {
	t.Helper()
	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(nodeID, hlc.Options{})
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock)

	// Connect-on-h2c httptest.Server — same pattern as
	// newConnectTestServer but the URL form is what the pump
	// consumes directly (replication.peerBaseURL accepts both
	// "host:port" and "http://host:port" forms).
	srv := newConnectTestServer(t, svc, rep)

	return &pumpNode{
		url:    srv.url,
		cache:  cache,
		clock:  clock,
		log:    log,
		svc:    svc,
		sdk:    newConnectClientFor(t, srv.url),
		nodeID: nodeID,
	}
}

// startPump attaches a Pump to the node aimed at the supplied peer
// URLs (or "host:port" — replication.peerBaseURL coerces both).
func (n *pumpNode) startPump(ctx context.Context, t *testing.T, peers []string) {
	t.Helper()
	p := replication.NewPump(replication.Config{
		NodeID:     n.nodeID,
		Peers:      peers,
		BackoffMin: 20 * time.Millisecond,
		BackoffMax: 200 * time.Millisecond,
		HTTPClient: h2cClient(),
	}, n.svc, n.cache)
	n.pump = p
	pumpCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		_ = p.Run(pumpCtx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Logf("pump %x did not stop within 2s", n.nodeID[:4])
		}
	})
}

// waitForVertex polls cache.GetVertex until the key appears or the
// deadline elapses.
func waitForVertex(t *testing.T, cache *graphcache.GraphCache[string, *pb.Vertex], key string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok := cache.GetVertex(key); ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// waitForEdge polls cache.GetWeight until the (tail,head) appears with
// at least the expected weight, or the deadline elapses.
func waitForEdge(t *testing.T, cache *graphcache.GraphCache[string, *pb.Vertex], tail, head string, want float32, timeout time.Duration) (float32, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if w, ok := cache.GetWeight(tail, head); ok && w >= want-1e-6 {
			return w, true
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, ok := cache.GetWeight(tail, head)
	return w, ok
}

// TestPeerPump_E2E_ThreeNodeConvergence wires three peers in a full
// mesh (A↔B↔C) and asserts that a write to any one node is observed
// on every other node within a short polling window. This exercises
// the Subscribe path of the Pump (#185) plus the self-echo filter
// (no mutation should bounce back to its origin).
func TestPeerPump_E2E_ThreeNodeConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-peer pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	a := newPumpNode(t, hlc.NodeID{0x0A})
	b := newPumpNode(t, hlc.NodeID{0x0B})
	c := newPumpNode(t, hlc.NodeID{0x0C})

	a.startPump(ctx, t, []string{b.url, c.url})
	b.startPump(ctx, t, []string{a.url, c.url})
	c.startPump(ctx, t, []string{a.url, b.url})

	// Give the pumps a moment to attach Subscribe streams before we
	// start writing — without this small head-start a fast write
	// could race the very first Subscribe handshake and the receiver
	// would have to fall through the gap → snapshot recovery path,
	// which is also tested below but adds latency to this assertion.
	time.Sleep(150 * time.Millisecond)

	// Write to A; expect convergence on B and C.
	if err := a.sdk.PutVertex(ctx, "from-a", "va", time.Minute); err != nil {
		t.Fatalf("a.PutVertex: %v", err)
	}
	if err := a.sdk.AddEdge(ctx, "from-a", "target", 1.5, time.Minute); err != nil {
		t.Fatalf("a.AddEdge: %v", err)
	}

	// Write to B; expect convergence on A and C.
	if err := b.sdk.PutVertex(ctx, "from-b", "vb", time.Minute); err != nil {
		t.Fatalf("b.PutVertex: %v", err)
	}

	// Write to C; expect convergence on A and B.
	if err := c.sdk.PutVertex(ctx, "from-c", "vc", time.Minute); err != nil {
		t.Fatalf("c.PutVertex: %v", err)
	}

	for _, tc := range []struct {
		name, key string
		cache     *graphcache.GraphCache[string, *pb.Vertex]
	}{
		{"b sees from-a", "from-a", b.cache},
		{"c sees from-a", "from-a", c.cache},
		{"a sees from-b", "from-b", a.cache},
		{"c sees from-b", "from-b", c.cache},
		{"a sees from-c", "from-c", a.cache},
		{"b sees from-c", "from-c", b.cache},
	} {
		if !waitForVertex(t, tc.cache, tc.key, 3*time.Second) {
			t.Errorf("%s: vertex %q not visible within 3s", tc.name, tc.key)
		}
	}

	// Edge convergence.
	if w, ok := waitForEdge(t, b.cache, "from-a", "target", 1.5, 3*time.Second); !ok || w != 1.5 {
		t.Errorf("b edge from-a->target: got w=%v ok=%v want 1.5/true", w, ok)
	}
	if w, ok := waitForEdge(t, c.cache, "from-a", "target", 1.5, 3*time.Second); !ok || w != 1.5 {
		t.Errorf("c edge from-a->target: got w=%v ok=%v want 1.5/true", w, ok)
	}

	// Reading B (#415): every node's mutation log retains all four
	// cluster-wide writes (A's PutVertex + AddEdge, B's PutVertex,
	// C's PutVertex). Per-origin watermark CAS (ApplyMutation) +
	// origin-anchored mu.Seq (logMutation stamps it; Subscribe relay
	// preserves it across hops) guarantee each (origin, seq) lands
	// at most once on every replica, so the monotonic LastSeq is
	// exactly the count of distinct cluster mutations.
	const wantClusterWrites = 4
	if last, ok := a.log.LastSeq(); !ok || last != wantClusterWrites {
		t.Errorf("a.log.LastSeq=%d ok=%v want %d (leaderless Subscribe contract)", last, ok, wantClusterWrites)
	}
	if last, ok := b.log.LastSeq(); !ok || last != wantClusterWrites {
		t.Errorf("b.log.LastSeq=%d ok=%v want %d", last, ok, wantClusterWrites)
	}
	if last, ok := c.log.LastSeq(); !ok || last != wantClusterWrites {
		t.Errorf("c.log.LastSeq=%d ok=%v want %d", last, ok, wantClusterWrites)
	}
}

// TestPeerPump_EmptyPeers_NoOp asserts that an empty peer list yields
// a Pump.Run that returns immediately, so the single-instance
// deployment path remains a no-op (no goroutine leak, no dial
// attempts, no error).
func TestPeerPump_EmptyPeers_NoOp(t *testing.T) {
	n := newPumpNode(t, hlc.NodeID{0x01})
	p := replication.NewPump(replication.Config{NodeID: n.nodeID}, n.svc, n.cache)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Pump.Run returned err for empty peers: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Pump.Run did not return promptly for empty peers")
	}
}

// TestPeerPump_GapRecoverySnapshot verifies that a follower attaching
// to a primary that has already advanced past the follower's known
// next-seq triggers the snapshot-then-resubscribe fallback path. This
// indirectly exercises Pump.session's FailedPrecondition handler
// (#184/#185 stitching).
func TestPeerPump_GapRecoverySnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping snapshot-recovery test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	primary := newPumpNode(t, hlc.NodeID{0xA1})
	// Pre-populate the primary BEFORE the follower attaches. With a
	// small ring buffer the follower's effective request (empty
	// cursor → server starts at oldest = 1) will be below firstSeq
	// → service returns FailedPrecondition reason=gapped → Pump runs
	// a snapshot to catch up, then resubscribes.
	for i := 0; i < 16; i++ {
		if err := primary.sdk.PutVertex(ctx, fmt.Sprintf("pre-%d", i), "v", time.Minute); err != nil {
			t.Fatalf("primary.PutVertex[%d]: %v", i, err)
		}
	}

	follower := newPumpNode(t, hlc.NodeID{0xA2})
	follower.startPump(ctx, t, []string{primary.url})

	// Snapshot should replay all 16 pre-existing vertices.
	for i := 0; i < 16; i++ {
		key := fmt.Sprintf("pre-%d", i)
		if !waitForVertex(t, follower.cache, key, 5*time.Second) {
			t.Errorf("follower did not receive %q via snapshot fallback", key)
		}
	}

	// After the snapshot, a fresh write on the primary must arrive
	// via the resubscribed stream — proves that Pump correctly
	// resubscribed after the snapshot bootstrap (#415, B-4: cutoff is
	// per-origin and lives in the server's watermark tracker; the
	// pump just sends an empty cursor again and the local
	// ApplyMutation CAS dedups whatever the snapshot already covered).
	if err := primary.sdk.PutVertex(ctx, "post-snapshot", "v", time.Minute); err != nil {
		t.Fatalf("primary.PutVertex post: %v", err)
	}
	if !waitForVertex(t, follower.cache, "post-snapshot", 3*time.Second) {
		t.Errorf("follower did not receive post-snapshot vertex via resubscribe")
	}
}
