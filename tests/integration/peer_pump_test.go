package integration_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/readiness"
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
	raw    graphv1connect.LanternServiceClient
	pump   *replication.Pump
	nodeID hlc.NodeID
}

func newPumpNode(t *testing.T, nodeID hlc.NodeID) *pumpNode {
	return newPumpNodeWithSearch(t, nodeID, 1024, true)
}

func newPumpNodeWithSearch(t *testing.T, nodeID hlc.NodeID, logCapacity int, positions bool) *pumpNode {
	t.Helper()
	log := mutationlog.New(mutationlog.Options{Capacity: logCapacity, SubscriberBuffer: 1024})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(nodeID, hlc.Options{})
	limits := productionSearchLimits(true, positions)
	cache := newProductionSearchCache(time.Minute, true, positions, limits.AnalysisLimits)
	svc := service.NewLanternService(cache).
		WithSearchLimits(limits).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock).
		WithOriginStates(svc).
		WithSearchConfig(svc)

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
		raw:    graphv1connect.NewLanternServiceClient(h2cClient(), srv.url),
		nodeID: nodeID,
	}
}

// startPump attaches a Pump to the node aimed at the supplied peer
// URLs (or "host:port" — replication.peerBaseURL coerces both).
func (n *pumpNode) startPump(ctx context.Context, t *testing.T, peers []string) {
	n.startPumpWithMetrics(ctx, t, peers, nil)
}

func (n *pumpNode) startPumpWithMetrics(ctx context.Context, t *testing.T, peers []string, metrics replication.Metrics) {
	t.Helper()
	p := replication.NewPump(replication.Config{
		NodeID:                  n.nodeID,
		Peers:                   peers,
		BackoffMin:              20 * time.Millisecond,
		BackoffMax:              200 * time.Millisecond,
		HTTPClient:              h2cClient(),
		SearchConfigFingerprint: n.svc.SearchConfigFingerprint(),
		Metrics:                 metrics,
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

type observedSearchConfigMetrics struct {
	gate         *readiness.Gate
	observations chan bool
	snapshots    atomic.Int32
}

func (m *observedSearchConfigMetrics) OnPumpConnect(peer string) {
	m.gate.OnPumpConnect(peer)
}
func (m *observedSearchConfigMetrics) OnPumpDisconnect(peer, reason string) {
	m.gate.OnPumpDisconnect(peer, reason)
}
func (m *observedSearchConfigMetrics) OnPumpApply(peer string) { m.gate.OnPumpApply(peer) }
func (m *observedSearchConfigMetrics) OnPumpDropSelfEcho(peer string) {
	m.gate.OnPumpDropSelfEcho(peer)
}
func (m *observedSearchConfigMetrics) OnPumpSnapshotReplayed(peer string, vertices, edges uint64, duration time.Duration) {
	m.snapshots.Add(1)
	m.gate.OnPumpSnapshotReplayed(peer, vertices, edges, duration)
}
func (m *observedSearchConfigMetrics) OnSearchConfig(peer string, matched bool) {
	m.gate.OnSearchConfig(peer, matched)
	select {
	case m.observations <- matched:
	default:
	}
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
	if _, err := a.sdk.PutVertex(ctx, "from-a", "va", time.Minute); err != nil {
		t.Fatalf("a.PutVertex: %v", err)
	}
	if _, err := a.sdk.AddEdge(ctx, "from-a", "target", 1.5, time.Minute); err != nil {
		t.Fatalf("a.AddEdge: %v", err)
	}

	// Write to B; expect convergence on A and C.
	if _, err := b.sdk.PutVertex(ctx, "from-b", "vb", time.Minute); err != nil {
		t.Fatalf("b.PutVertex: %v", err)
	}

	// Write to C; expect convergence on A and B.
	if _, err := c.sdk.PutVertex(ctx, "from-c", "vc", time.Minute); err != nil {
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
	waitForSearchConvergence(t, ctx, "from", nil, a.raw, b.raw, c.raw)
}

// TestPeerPump_SearchPartitionHealConvergence starts a follower only after a
// production-search corpus has changed on the primary, then exercises the live
// Subscribe tail across overwrite, singular/batch/prefix delete, tombstones,
// TTL, structured/typed values, and implicit edge endpoints. Homogeneous
// replicas must converge to identical key order and score bits.
func TestPeerPump_SearchPartitionHealConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping search partition-heal test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	primary := newPumpNode(t, hlc.NodeID{0xD1})
	follower := newPumpNode(t, hlc.NodeID{0xD2})
	if _, err := primary.sdk.PutVertices(ctx, []client.VertexInput{
		{Key: "search/keep", Value: "common lantern alpha", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/overwrite", Value: "common retiredterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-one", Value: "common deletedterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-batch-a", Value: "common batchterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-batch-b", Value: "common batchterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-prefix/a", Value: "common prefixterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-prefix/b", Value: "common prefixterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/json", Value: `{"title":"common structuredvalue","ignored":42}`, Expiration: time.Now().Add(time.Hour)},
		{Key: "search/int", Value: int64(4242), Expiration: time.Now().Add(time.Hour)},
		{Key: "search/ttl", Value: "common expiredterm", Expiration: time.Now().Add(250 * time.Millisecond)},
	}); err != nil {
		t.Fatalf("seed PutVertices: %v", err)
	}
	if _, err := primary.sdk.AddEdge(ctx, "search/implicit-tail", "search/implicit-head", 1, time.Hour); err != nil {
		t.Fatalf("seed implicit endpoints: %v", err)
	}
	// A born-expired input is an accepted delete-like overwrite. This absent
	// key remains absent on both replicas and must never enter the derived
	// index, while the SDK still exposes the authoritative EXPIRED outcome.
	outcome, err := primary.sdk.PutVertexAt(ctx, "search/born-expired", "bornexpiredterm", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("born-expired PutVertexAt: %v", err)
	}
	if outcome != client.PutOutcomeExpired {
		t.Fatalf("born-expired PutVertexAt outcome = %s, want EXPIRED", outcome)
	}

	// The follower was partitioned for every seed mutation. Starting its pump
	// now heals through the retained Subscribe tail rather than shared memory.
	follower.startPump(ctx, t, []string{primary.url})
	waitForSearchConvergence(t, ctx, "common", nil, primary.raw, follower.raw)

	if _, err := primary.sdk.PutVertex(ctx, "search/overwrite", "common currentterm", time.Hour); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := primary.sdk.DeleteVertex(ctx, "search/delete-one"); err != nil {
		t.Fatalf("DeleteVertex: %v", err)
	}
	if _, err := primary.sdk.DeleteVertices(ctx, []string{"search/delete-batch-a", "search/delete-batch-b"}); err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if _, err := primary.sdk.DeleteVerticesByPrefix(ctx, "search/delete-prefix/"); err != nil {
		t.Fatalf("DeleteVerticesByPrefix: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	exactAll := &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}
	for _, retired := range []string{"retiredterm", "deletedterm", "batchterm", "prefixterm", "expiredterm", "bornexpiredterm"} {
		if got := waitForSearchConvergence(t, ctx, retired, exactAll, primary.raw, follower.raw); len(got) != 0 {
			t.Errorf("retired query %q still matched: %+v", retired, got)
		}
	}
	for _, live := range []string{"currentterm", "structuredvalue", "4242", "implicit"} {
		if got := waitForSearchConvergence(t, ctx, live, exactAll, primary.raw, follower.raw); len(got) == 0 {
			t.Errorf("live query %q returned no hits", live)
		}
	}
	if got := waitForSearchConvergence(t, ctx, "common", exactAll, primary.raw, follower.raw); len(got) != 3 {
		t.Fatalf("final common corpus = %+v, want three live documents", got)
	}
}

func TestPeerPump_SearchConfigMismatchBlocksReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	primary := newPumpNodeWithSearch(t, hlc.NodeID{0xE1}, 1024, true)
	follower := newPumpNodeWithSearch(t, hlc.NodeID{0xE2}, 1024, false)
	if _, err := primary.sdk.PutVertex(ctx, "mismatch-proof", "graph still converges", time.Hour); err != nil {
		t.Fatalf("primary PutVertex: %v", err)
	}

	peerStatus, err := newReplicationRawClient(t, primary.url).PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
	if err != nil {
		t.Fatalf("PeerStatus: %v", err)
	}
	remoteFingerprint := peerStatus.Msg.GetSearchConfigFingerprint()
	if remoteFingerprint == "" || remoteFingerprint != primary.svc.SearchConfigFingerprint() {
		t.Fatalf("peer search fingerprint = %q, primary = %q", remoteFingerprint, primary.svc.SearchConfigFingerprint())
	}
	if remoteFingerprint == follower.svc.SearchConfigFingerprint() {
		t.Fatal("positions mismatch produced identical search fingerprints")
	}

	gate := readiness.NewGate(100, true, nil)
	metrics := &observedSearchConfigMetrics{gate: gate, observations: make(chan bool, 1)}
	follower.startPumpWithMetrics(ctx, t, []string{primary.url}, metrics)
	select {
	case matched := <-metrics.observations:
		if matched {
			t.Fatal("pump reported mismatched search configs as compatible")
		}
	case <-ctx.Done():
		t.Fatal("pump did not publish search config comparison")
	}
	if !waitForVertex(t, follower.cache, "mismatch-proof", 3*time.Second) {
		t.Fatal("graph replication stopped on a search config mismatch")
	}
	if gate.Ready() {
		t.Fatal("heterogeneous search config masqueraded as ready")
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

	primary := newPumpNodeWithSearch(t, hlc.NodeID{0xA1}, 4, true)
	// Pre-populate the primary BEFORE the follower attaches. With a
	// small ring buffer the follower's effective request (empty
	// cursor → server starts at oldest = 1) will be below firstSeq
	// → service returns FailedPrecondition reason=gapped → Pump runs
	// a snapshot to catch up, then resubscribes.
	for i := 0; i < 16; i++ {
		if _, err := primary.sdk.PutVertex(ctx, fmt.Sprintf("pre-%d", i), "v", time.Minute); err != nil {
			t.Fatalf("primary.PutVertex[%d]: %v", i, err)
		}
	}
	// These accepted-expired HLC20 overwrites have no live payload and no
	// retained log record in this storage-level fixture. Gap recovery therefore
	// has to bootstrap their explicit causal-barrier frames from Snapshot.
	barrierTS := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0xB0}}
	if !primary.cache.PutVertexWithExpirationHLC(
		"snapshot-barrier-vertex", &pb.Vertex{Key: "snapshot-barrier-vertex"},
		time.Now().Add(-time.Hour), barrierTS,
	) {
		t.Fatal("primary accepted-expired vertex barrier was rejected")
	}
	if !primary.cache.PutEdgeWithExpirationHLC(
		"snapshot-barrier-tail", "snapshot-barrier-head", 1,
		time.Now().Add(-time.Hour), barrierTS,
	) {
		t.Fatal("primary accepted-expired edge barrier was rejected")
	}

	follower := newPumpNode(t, hlc.NodeID{0xA2})
	metrics := &observedSearchConfigMetrics{
		gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
	}
	follower.startPumpWithMetrics(ctx, t, []string{primary.url}, metrics)

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
	if _, err := primary.sdk.PutVertex(ctx, "post-snapshot", "v", time.Minute); err != nil {
		t.Fatalf("primary.PutVertex post: %v", err)
	}
	if !waitForVertex(t, follower.cache, "post-snapshot", 3*time.Second) {
		t.Errorf("follower did not receive post-snapshot vertex via resubscribe")
	}
	time.Sleep(100 * time.Millisecond)
	if got := metrics.snapshots.Load(); got != 1 {
		t.Fatalf("snapshot replay count = %d, want exactly 1 before live tail", got)
	}
	olderTS := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0xA0}}
	if follower.cache.PutVertexWithExpirationHLC(
		"snapshot-barrier-vertex", &pb.Vertex{Key: "snapshot-barrier-vertex"},
		time.Now().Add(time.Hour), olderTS,
	) {
		t.Fatal("pump bootstrap lost vertex barrier: HLC10 live Put was accepted")
	}
	if follower.cache.PutEdgeWithExpirationHLC(
		"snapshot-barrier-tail", "snapshot-barrier-head", 9,
		time.Now().Add(time.Hour), olderTS,
	) {
		t.Fatal("pump bootstrap lost edge barrier: HLC10 live Put was accepted")
	}
	if _, ok := follower.cache.GetVertex("snapshot-barrier-vertex"); ok {
		t.Fatal("vertex barrier became visible after pump bootstrap")
	}
	if _, _, ok := follower.cache.GetEdgeDetail("snapshot-barrier-tail", "snapshot-barrier-head"); ok {
		t.Fatal("edge barrier became visible after pump bootstrap")
	}
	if _, ok := follower.cache.GetVertex("snapshot-barrier-tail"); ok {
		t.Fatal("edge barrier materialized its tail endpoint during pump bootstrap")
	}
	if _, ok := follower.cache.GetVertex("snapshot-barrier-head"); ok {
		t.Fatal("edge barrier materialized its head endpoint during pump bootstrap")
	}
	waitForSearchConvergence(t, ctx, "pre", nil, primary.raw, follower.raw)
	waitForSearchConvergence(t, ctx, "post snapshot", nil, primary.raw, follower.raw)
	exactAll := &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}
	if got := waitForSearchConvergence(t, ctx, "snapshot barrier", exactAll, primary.raw, follower.raw); len(got) != 0 {
		t.Fatalf("causal barrier identities became searchable: %+v", got)
	}
}
