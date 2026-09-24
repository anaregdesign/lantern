package integration_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	"google.golang.org/protobuf/types/known/timestamppb"
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
	return newPumpNodeWithWAL(t, nodeID, logCapacity, positions, nil)
}

func newPumpNodeWithWAL(t *testing.T, nodeID hlc.NodeID, logCapacity int, positions bool, wal mutationlog.WAL) *pumpNode {
	return newPumpNodeWithWALAndMetrics(t, nodeID, logCapacity, positions, wal, nil)
}

func newPumpNodeWithWALAndMetrics(t *testing.T, nodeID hlc.NodeID, logCapacity int, positions bool, wal mutationlog.WAL, metrics service.SubscribeMetrics) *pumpNode {
	t.Helper()
	log := mutationlog.New(mutationlog.Options{Capacity: logCapacity, SubscriberBuffer: 1024, WAL: wal})
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
		WithSearchConfig(svc).
		WithMetrics(metrics)

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

type retryRelayWAL struct {
	writes       atomic.Int32
	failed       chan struct{}
	retryStarted chan struct{}
	allowRetry   chan struct{}
}

type stickyRelayWAL struct {
	armed    atomic.Bool
	healed   atomic.Bool
	failures atomic.Int32
	faulted  chan struct{}
}

type oneShotLocalWAL struct {
	armed    atomic.Bool
	failures atomic.Int32
}

type blockedFailureWAL struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (w *blockedFailureWAL) Write(mutationlog.Entry) error {
	if w.armed.CompareAndSwap(true, false) {
		close(w.entered)
		<-w.release
		return errors.New("injected blocked WAL failure")
	}
	return nil
}

func (w *oneShotLocalWAL) Write(mutationlog.Entry) error {
	if w.armed.CompareAndSwap(true, false) {
		w.failures.Add(1)
		return errors.New("injected local WAL failure")
	}
	return nil
}

func (w *stickyRelayWAL) Write(mutationlog.Entry) error {
	if w.armed.Load() && !w.healed.Load() {
		if w.failures.Add(1) == 1 {
			close(w.faulted)
		}
		return errors.New("injected persistent relay WAL failure")
	}
	return nil
}

type signalSubscribeMetrics struct{ started chan struct{} }

func (m *signalSubscribeMetrics) OnSubscribeStarted() {
	select {
	case m.started <- struct{}{}:
	default:
	}
}
func (*signalSubscribeMetrics) OnSubscribeEnded()         {}
func (*signalSubscribeMetrics) OnSubscribeDropped(string) {}

func (w *retryRelayWAL) Write(mutationlog.Entry) error {
	switch w.writes.Add(1) {
	case 1:
		close(w.failed)
		return errors.New("injected relay WAL failure")
	case 2:
		close(w.retryStarted)
		<-w.allowRetry
	}
	return nil
}

// assertGraphReadsGapped exercises every ordinary graph-data read RPC over
// Connect/h2c. GetReplicationStatus deliberately remains available for
// diagnosis while the graph publication cut is faulted.
func assertGraphReadsGapped(t *testing.T, ctx context.Context, n *pumpNode) {
	t.Helper()
	checks := []struct {
		name string
		read func() error
	}{
		{"Illuminate", func() error {
			_, err := n.raw.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{Seed: "relay-warmup", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}}))
			return err
		}},
		{"GetVertex", func() error {
			_, err := n.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "relay-warmup"}))
			return err
		}},
		{"GetVertices", func() error {
			_, err := n.raw.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{Keys: []string{"relay-warmup"}}))
			return err
		}},
		{"ScanVertices", func() error {
			_, err := n.raw.ScanVertices(ctx, connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "relay"}))
			return err
		}},
		{"ScanVertexKeys", func() error {
			_, err := n.raw.ScanVertexKeys(ctx, connect.NewRequest(&pb.ScanVertexKeysRequest{Prefix: "relay"}))
			return err
		}},
		{"SearchVertices", func() error {
			_, err := n.raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "ready"}))
			return err
		}},
		{"CountVerticesByPrefix", func() error {
			_, err := n.raw.CountVerticesByPrefix(ctx, connect.NewRequest(&pb.CountVerticesByPrefixRequest{Prefix: "relay"}))
			return err
		}},
		{"TopVerticesByDegree", func() error {
			_, err := n.raw.TopVerticesByDegree(ctx, connect.NewRequest(&pb.TopVerticesByDegreeRequest{Prefix: "relay"}))
			return err
		}},
		{"GetEdge", func() error {
			_, err := n.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "fault-tail", Head: "fault-head"}))
			return err
		}},
		{"GetEdges", func() error {
			_, err := n.raw.GetEdges(ctx, connect.NewRequest(&pb.GetEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "fault-tail", Head: "fault-head"}}}))
			return err
		}},
		{"ScanEdges", func() error {
			_, err := n.raw.ScanEdges(ctx, connect.NewRequest(&pb.ScanEdgesRequest{TailPrefix: "fault"}))
			return err
		}},
		{"GetServerStatus", func() error {
			_, err := n.raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
			return err
		}},
		{"DeleteVerticesByPrefix dry run", func() error {
			_, err := n.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "relay", DryRun: true}))
			return err
		}},
		{"DeleteEdgesByPrefix dry run", func() error {
			_, err := n.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "fault", DryRun: true}))
			return err
		}},
	}
	for _, check := range checks {
		if err := check.read(); err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
			t.Errorf("%s during publication fault = %v, want gapped FailedPrecondition", check.name, err)
		}
	}
	stream, err := n.raw.BackupSnapshot(ctx, connect.NewRequest(&pb.BackupSnapshotRequest{}))
	if err == nil {
		if stream.Receive() {
			t.Error("BackupSnapshot exposed a graph record during publication fault")
		}
		err = stream.Err()
		_ = stream.Close()
	}
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
		t.Errorf("BackupSnapshot during publication fault = %v, want gapped FailedPrecondition", err)
	}
	if _, err := n.raw.GetReplicationStatus(ctx, connect.NewRequest(&pb.GetReplicationStatusRequest{})); err != nil {
		t.Errorf("GetReplicationStatus must remain available during publication fault: %v", err)
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

// waitForWireEdge is the real Connect/h2c sibling used by gap-recovery tests
// whose contract is externally observable through GetEdge. NotFound and a
// publication gap during an in-flight Snapshot install are transient; a gap
// still present at the deadline is terminal.
func waitForWireEdge(t *testing.T, ctx context.Context, raw graphv1connect.LanternServiceClient, tail, head string, want float32, timeout time.Duration) (float32, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: tail, Head: head}))
		if err == nil {
			weight := response.Msg.GetEdge().GetWeight()
			if weight == want {
				return weight, true
			}
		} else if code := connect.CodeOf(err); code != connect.CodeNotFound &&
			(code != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped:")) {
			t.Fatalf("GetEdge %s->%s: %v", tail, head, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: tail, Head: head}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("GetEdge %s->%s: %v", tail, head, err)
		}
		return 0, false
	}
	return response.Msg.GetEdge().GetWeight(), true
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
	// C's PutVertex). Per-origin contiguous commit (ApplyMutation) +
	// origin-anchored mu.Seq (logMutation allocates it; Subscribe relay
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

// TestPeerPump_ReverseOriginSeqRelay exercises the Connect/h2c Subscribe path
// across A -> B -> C. A's log deliberately delivers one origin in reverse
// seq order. B must keep the future entries out of its graph, Snapshot, and
// relay log until seq 1 arrives; C must then observe the contiguous tail.
func TestPeerPump_ReverseOriginSeqRelay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-peer pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := newPumpNode(t, hlc.NodeID{0xA1})
	b := newPumpNode(t, hlc.NodeID{0xB1})
	c := newPumpNode(t, hlc.NodeID{0xC1})
	appendSource := func(seq uint64) {
		t.Helper()
		stamp := hlc.Timestamp{WallNs: int64(seq), NodeID: a.nodeID}
		mutation := &pb.Mutation{
			Origin: a.nodeID[:], Seq: seq,
			Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, NodeId: a.nodeID[:]},
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
				Vertex: &pb.Vertex{Key: fmt.Sprintf("reverse-wire-%d", seq)},
			}}},
		}
		if _, err := a.log.Append(mutation, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, seq := range []uint64{4, 3, 2} {
		appendSource(seq)
	}
	b.startPump(ctx, t, []string{a.url})
	c.startPump(ctx, t, []string{b.url})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows := b.pump.Snapshot()
		if len(rows) == 1 && rows[0].AppliedSeq == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows := b.pump.Snapshot()
	if len(rows) != 1 || rows[0].AppliedSeq != 2 {
		t.Fatalf("B did not receive reverse source prefix over h2c: %+v", rows)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 0 {
		t.Fatalf("B advertised missing origin prefix as seq %d", got)
	}
	if got := b.log.Len(); got != 0 {
		t.Fatalf("B relayed %d future entries before seq 1", got)
	}
	for _, node := range []*pumpNode{b, c} {
		if _, ok := node.cache.GetVertex("reverse-wire-4"); ok {
			t.Fatalf("node %x exposed future mutation before gap closed", node.nodeID)
		}
	}
	stream, err := newReplicationRawClient(t, b.url).Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var header *pb.SnapshotHeader
	for stream.Receive() {
		switch entry := stream.Msg().GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			header = entry.Header
		case *pb.SnapshotResponse_Vertex:
			if entry.Vertex.GetVertex().GetKey() == "reverse-wire-4" {
				t.Fatal("Snapshot contained an unpublished future mutation")
			}
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if header == nil || header.GetCutoffSeqPerOrigin()[hex.EncodeToString(a.nodeID[:])] != 0 {
		t.Fatalf("Snapshot crossed pending origin gap: %+v", header)
	}

	appendSource(1)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (b.svc.LocalSeq(a.nodeID) < 4 || c.svc.LocalSeq(a.nodeID) < 4) {
		time.Sleep(10 * time.Millisecond)
	}
	for _, node := range []*pumpNode{b, c} {
		if got := node.svc.LocalSeq(a.nodeID); got != 4 {
			t.Fatalf("node %x origin cursor = %d, want 4", node.nodeID, got)
		}
		if got := node.log.Len(); got != 4 {
			t.Fatalf("node %x relay log length = %d, want 4", node.nodeID, got)
		}
		for seq := uint64(1); seq <= 4; seq++ {
			if _, ok := node.cache.GetVertex(fmt.Sprintf("reverse-wire-%d", seq)); !ok {
				t.Fatalf("node %x missing graph seq %d", node.nodeID, seq)
			}
		}
	}
	for _, node := range []*pumpNode{b, c} {
		feed, err := newReplicationRawClient(t, node.url).Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
		if err != nil {
			t.Fatal(err)
		}
		for seq := uint64(1); seq <= 4; seq++ {
			if !feed.Receive() || feed.Msg().GetMutation().GetSeq() != seq {
				t.Fatalf("node %x Subscribe origin seq %d: msg=%+v err=%v", node.nodeID, seq, feed.Msg(), feed.Err())
			}
		}
		_ = feed.Close()
	}
}

// TestPeerPump_RelayAppendFailureReconnects confirms a failed relay append
// closes the h2c Subscribe session and the pump's next session re-delivers
// the same origin seq. The graph-applied frontier is not applied twice.
func TestPeerPump_RelayAppendFailureReconnects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-peer pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := newPumpNode(t, hlc.NodeID{0xA2})
	wal := &retryRelayWAL{
		failed: make(chan struct{}), retryStarted: make(chan struct{}), allowRetry: make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-wal.allowRetry:
		default:
			close(wal.allowRetry)
		}
	})
	b := newPumpNodeWithWAL(t, hlc.NodeID{0xB2}, 1024, true, wal)
	c := newPumpNode(t, hlc.NodeID{0xC2})
	b.startPump(ctx, t, []string{a.url})
	c.startPump(ctx, t, []string{b.url})
	if _, err := a.sdk.AddEdge(ctx, "retry-a", "retry-b", 2, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []<-chan struct{}{wal.failed, wal.retryStarted} {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatalf("pump did not reconnect after failed relay append: %v", ctx.Err())
		}
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 0 {
		t.Fatalf("B advanced origin cutoff across failed append: %d", got)
	}
	if weight, ok := b.cache.GetWeight("retry-a", "retry-b"); !ok || weight != 2 {
		t.Fatalf("B graph after first apply = (%v,%v), want (2,true)", weight, ok)
	}
	if _, ok := c.cache.GetWeight("retry-a", "retry-b"); ok {
		t.Fatal("C observed mutation before B published relay log")
	}
	close(wal.allowRetry)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (b.svc.LocalSeq(a.nodeID) < 1 || c.svc.LocalSeq(a.nodeID) < 1) {
		time.Sleep(10 * time.Millisecond)
	}
	for _, node := range []*pumpNode{b, c} {
		if got := node.svc.LocalSeq(a.nodeID); got != 1 {
			t.Fatalf("node %x origin cursor = %d, want 1", node.nodeID, got)
		}
		// C may receive the mutation through Snapshot after B gaps its
		// existing Subscribe stream. Bootstrap state has a cutoff but does
		// not synthesize historical local log entries.
		if got := node.log.Len(); node == b && got != 1 {
			t.Fatalf("B relay log length = %d, want 1", got)
		}
		if weight, ok := node.cache.GetWeight("retry-a", "retry-b"); !ok || weight != 2 {
			t.Fatalf("node %x AddEdge replayed twice: (%v,%v)", node.nodeID, weight, ok)
		}
	}
	if got := wal.writes.Load(); got != 2 {
		t.Fatalf("B WAL writes = %d, want initial failure and one reconnect retry", got)
	}
}

// TestPeerPump_RelayAppendFaultGapsCDC verifies that a graph-visible remote
// mutation with a failed relay WAL append never looks like an uninterrupted
// Subscribe/Snapshot history. The old stream stays gapped after repair.
func TestPeerPump_RelayAppendFaultGapsCDC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping h2c peer-pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := newPumpNode(t, hlc.NodeID{0xA3})
	wal := &stickyRelayWAL{faulted: make(chan struct{})}
	metrics := &signalSubscribeMetrics{started: make(chan struct{}, 1)}
	b := newPumpNodeWithWALAndMetrics(t, hlc.NodeID{0xB3}, 1024, true, wal, metrics)
	b.startPump(ctx, t, []string{a.url})
	if _, err := a.sdk.PutVertex(ctx, "relay-warmup", "ready", time.Hour); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && b.svc.LocalSeq(a.nodeID) < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 1 {
		t.Fatalf("warmup origin seq = %d, want 1", got)
	}

	rep := newReplicationRawClient(t, b.url)
	oldFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = oldFeed.Close() }()
	if !oldFeed.Receive() || oldFeed.Msg().GetMutation().GetSeq() != 1 {
		t.Fatalf("old Subscribe warmup = (%+v,%v), want origin seq 1", oldFeed.Msg(), oldFeed.Err())
	}
	oldStreamEnded := make(chan error, 1)
	go func() {
		if oldFeed.Receive() {
			oldStreamEnded <- fmt.Errorf("old Subscribe unexpectedly received %+v", oldFeed.Msg())
			return
		}
		oldStreamEnded <- oldFeed.Err()
	}()
	select {
	case <-metrics.started:
	case <-ctx.Done():
		t.Fatal("old Subscribe did not start")
	}
	wal.armed.Store(true)
	if _, err := a.sdk.AddEdge(ctx, "fault-tail", "fault-head", 2, time.Hour); err != nil {
		t.Fatal(err)
	}
	select {
	case streamErr := <-oldStreamEnded:
		if connect.CodeOf(streamErr) != connect.CodeFailedPrecondition || !strings.Contains(streamErr.Error(), "gapped") {
			t.Fatalf("old Subscribe error = %v, want gapped FailedPrecondition", streamErr)
		}
	case <-ctx.Done():
		t.Fatal("old Subscribe did not close on relay WAL failure")
	}
	if weight, ok := b.cache.GetWeight("fault-tail", "fault-head"); !ok || weight != 2 {
		t.Fatalf("graph-visible failed publication = (%v,%v), want (2,true)", weight, ok)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 1 {
		t.Fatalf("failed publication advanced origin cursor to %d", got)
	}
	if got := b.log.Len(); got != 1 {
		t.Fatalf("failed publication added %d relay log entries, want only warmup", got)
	}
	assertGraphReadsGapped(t, ctx, b)

	faultCtx, faultCancel := context.WithTimeout(ctx, 2*time.Second)
	defer faultCancel()
	newFeed, err := rep.Subscribe(faultCtx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 2}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("new Subscribe error = %v, want FailedPrecondition", err)
		}
	} else {
		if newFeed.Receive() || connect.CodeOf(newFeed.Err()) != connect.CodeFailedPrecondition {
			t.Fatalf("new Subscribe during fault = (%+v,%v), want gapped", newFeed.Msg(), newFeed.Err())
		}
		_ = newFeed.Close()
	}
	snapshot, err := rep.Snapshot(faultCtx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("Snapshot during fault error = %v, want FailedPrecondition", err)
		}
	} else {
		if snapshot.Receive() || connect.CodeOf(snapshot.Err()) != connect.CodeFailedPrecondition {
			t.Fatalf("Snapshot during fault = (%+v,%v), want gapped", snapshot.Msg(), snapshot.Err())
		}
		_ = snapshot.Close()
	}

	wal.healed.Store(true)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && b.svc.LocalSeq(a.nodeID) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 2 {
		t.Fatalf("repaired origin cursor = %d, want 2", got)
	}
	if got := b.log.Len(); got != 2 {
		t.Fatalf("repaired relay log length = %d, want 2", got)
	}
	if wal.failures.Load() == 0 {
		t.Fatal("WAL failure was not exercised")
	}
	healthyFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if !healthyFeed.Receive() || healthyFeed.Msg().GetMutation().GetSeq() != 2 {
		t.Fatalf("repaired Subscribe = (%+v,%v), want origin seq 2", healthyFeed.Msg(), healthyFeed.Err())
	}
	_ = healthyFeed.Close()
	healthySnapshot, err := rep.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !healthySnapshot.Receive() || healthySnapshot.Msg().GetHeader().GetCutoffSeqPerOrigin()[hex.EncodeToString(a.nodeID[:])] != 2 {
		t.Fatalf("repaired Snapshot header = (%+v,%v), want origin cutoff 2", healthySnapshot.Msg(), healthySnapshot.Err())
	}
	_ = healthySnapshot.Close()
	if _, err := b.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "fault-tail", Head: "fault-head"})); err != nil {
		t.Fatalf("graph read after relay repair: %v", err)
	}
}

// A local graph-first write may have changed read-visible state when the WAL
// rejects its append. Across the real Connect/h2c service and replication
// handlers, every such path must gap the old feed, hold its origin seq, and
// repair the exact original mutation before a later local write proceeds.
func TestLocalWritePublication_WALFaultGapsCDCAndRepairs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping h2c publication fault test in short mode")
	}
	type scenario struct {
		name   string
		seed   func(context.Context, *pumpNode) error
		write  func(context.Context, *pumpNode) error
		verify func(*testing.T, *pumpNode, *pb.Mutation)
	}
	putVertex := func(ctx context.Context, n *pumpNode, key string) error {
		_, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: key, Value: &pb.Vertex_String_{String_: key}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
		}}))
		return err
	}
	putEdge := func(ctx context.Context, n *pumpNode, tail, head string) error {
		_, err := n.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
			Tail: tail, Head: head, Weight: 3, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
		}}))
		return err
	}
	cases := []scenario{
		{
			name: "conditional PutVertex",
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{IfAbsent: true, Vertex: &pb.Vertex{
					Key: "conditional", Value: &pb.Vertex_String_{String_: "original"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
				}}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				live := m.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetLive()
				if live.GetKey() != "conditional" || live.GetString_() != "original" {
					t.Fatalf("conditional Put payload = %v", m.GetOp())
				}
				if v, ok := n.cache.GetVertex("conditional"); !ok || v.GetString_() != "original" {
					t.Fatalf("conditional graph effect = (%v,%v)", v, ok)
				}
			},
		},
		{
			name: "born-expired PutVertex",
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
					Key: "born-expired", Expiration: timestamppb.New(time.Now().Add(-time.Second)),
				}}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				if got := m.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetCausalBarrier().GetKey(); got != "born-expired" {
					t.Fatalf("born-expired barrier key = %q", got)
				}
				if _, ok := n.cache.GetVertex("born-expired"); ok {
					t.Fatal("born-expired vertex is live")
				}
			},
		},
		{
			name:  "PutEdge",
			write: func(ctx context.Context, n *pumpNode) error { return putEdge(ctx, n, "put/a", "put/b") },
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				live := m.GetOp().GetReplicatedPutEdges().GetEntries()[0].GetLive()
				if live.GetTail() != "put/a" || live.GetHead() != "put/b" || live.GetWeight() != 3 {
					t.Fatalf("PutEdge payload = %v", m.GetOp())
				}
				if weight, ok := n.cache.GetWeight("put/a", "put/b"); !ok || weight != 3 {
					t.Fatalf("PutEdge graph effect = (%v,%v)", weight, ok)
				}
			},
		},
		{
			name: "born-expired PutEdge",
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
					Tail: "expired/a", Head: "expired/b", Weight: 3, Expiration: timestamppb.New(time.Now().Add(-time.Second)),
				}}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				barrier := m.GetOp().GetReplicatedPutEdges().GetEntries()[0].GetCausalBarrier()
				if barrier.GetTail() != "expired/a" || barrier.GetHead() != "expired/b" {
					t.Fatalf("born-expired edge barrier = %v", m.GetOp())
				}
				if _, ok := n.cache.GetWeight("expired/a", "expired/b"); ok {
					t.Fatal("born-expired edge is live")
				}
			},
		},
		{
			name: "DeleteVertex",
			seed: func(ctx context.Context, n *pumpNode) error { return putVertex(ctx, n, "delete-vertex") },
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "delete-vertex"}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				if keys := m.GetOp().GetDeleteVertices().GetKeys(); len(keys) != 1 || keys[0] != "delete-vertex" {
					t.Fatalf("DeleteVertex exact keys = %v", keys)
				}
				if _, ok := n.cache.GetVertex("delete-vertex"); ok {
					t.Fatal("DeleteVertex graph effect missing")
				}
			},
		},
		{
			name: "DeleteEdge",
			seed: func(ctx context.Context, n *pumpNode) error { return putEdge(ctx, n, "delete/a", "delete/b") },
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "delete/a", Head: "delete/b"}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				edges := m.GetOp().GetDeleteEdges().GetEdges()
				if len(edges) != 1 || edges[0].GetTail() != "delete/a" || edges[0].GetHead() != "delete/b" {
					t.Fatalf("DeleteEdge exact edges = %v", edges)
				}
				if _, ok := n.cache.GetWeight("delete/a", "delete/b"); ok {
					t.Fatal("DeleteEdge graph effect missing")
				}
			},
		},
		{
			name: "capped DeleteVerticesByPrefix",
			seed: func(ctx context.Context, n *pumpNode) error {
				if err := putVertex(ctx, n, "cap/one"); err != nil {
					return err
				}
				return putVertex(ctx, n, "cap/two")
			},
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "cap/", Limit: 1}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				keys := m.GetOp().GetDeleteVertices().GetKeys()
				if len(keys) != 1 || (keys[0] != "cap/one" && keys[0] != "cap/two") {
					t.Fatalf("capped vertex victims = %v", keys)
				}
				if _, ok := n.cache.GetVertex(keys[0]); ok {
					t.Fatal("exact vertex victim is still live")
				}
				other := "cap/one"
				if keys[0] == other {
					other = "cap/two"
				}
				if _, ok := n.cache.GetVertex(other); !ok {
					t.Fatal("capped vertex survivor was deleted")
				}
			},
		},
		{
			name: "capped DeleteEdgesByPrefix",
			seed: func(ctx context.Context, n *pumpNode) error {
				if err := putEdge(ctx, n, "edge/one", "target"); err != nil {
					return err
				}
				return putEdge(ctx, n, "edge/two", "target")
			},
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "edge/", Limit: 1}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				edges := m.GetOp().GetDeleteEdges().GetEdges()
				if len(edges) != 1 || (edges[0].GetTail() != "edge/one" && edges[0].GetTail() != "edge/two") || edges[0].GetHead() != "target" {
					t.Fatalf("capped edge victims = %v", edges)
				}
				if _, ok := n.cache.GetWeight(edges[0].GetTail(), "target"); ok {
					t.Fatal("exact edge victim is still live")
				}
				other := "edge/one"
				if edges[0].GetTail() == other {
					other = "edge/two"
				}
				if _, ok := n.cache.GetWeight(other, "target"); !ok {
					t.Fatal("capped edge survivor was deleted")
				}
			},
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			wal := &oneShotLocalWAL{}
			nodeID := hlc.NodeID{0xC3, byte(i + 1)}
			node := newPumpNodeWithWAL(t, nodeID, 32, true, wal)
			if err := putVertex(ctx, node, "stream/warmup"); err != nil {
				t.Fatal(err)
			}
			if tc.seed != nil {
				if err := tc.seed(ctx, node); err != nil {
					t.Fatal(err)
				}
			}
			beforeOrigin := node.svc.LocalSeq(nodeID)
			beforeLocal, _ := node.log.LastSeq()
			rep := newReplicationRawClient(t, node.url)
			oldFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: beforeLocal}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = oldFeed.Close() }()
			if !oldFeed.Receive() || oldFeed.Msg().GetMutation().GetSeq() != beforeOrigin {
				t.Fatalf("old Subscribe warmup = (%v,%v)", oldFeed.Msg(), oldFeed.Err())
			}
			wal.armed.Store(true)
			if code := connect.CodeOf(tc.write(ctx, node)); code != connect.CodeUnavailable {
				t.Fatalf("failed local write code = %v, want Unavailable", code)
			}
			if wal.failures.Load() != 1 || node.svc.LocalSeq(nodeID) != beforeOrigin || node.log.Len() != int(beforeLocal) {
				t.Fatalf("failed publication: WAL failures=%d origin seq=%d log len=%d", wal.failures.Load(), node.svc.LocalSeq(nodeID), node.log.Len())
			}
			if tc.name == "DeleteEdge" {
				assertGraphReadsGapped(t, ctx, node)
			}
			if oldFeed.Receive() || connect.CodeOf(oldFeed.Err()) != connect.CodeFailedPrecondition || !strings.Contains(oldFeed.Err().Error(), "gapped") {
				t.Fatalf("old Subscribe after fault = (%v,%v), want gapped", oldFeed.Msg(), oldFeed.Err())
			}
			newFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: beforeLocal + 1}))
			if err == nil {
				if newFeed.Receive() || connect.CodeOf(newFeed.Err()) != connect.CodeFailedPrecondition {
					t.Fatalf("new Subscribe during fault = (%v,%v)", newFeed.Msg(), newFeed.Err())
				}
				_ = newFeed.Close()
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("new Subscribe error = %v", err)
			}
			snapshot, err := rep.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
			if err == nil {
				if snapshot.Receive() || connect.CodeOf(snapshot.Err()) != connect.CodeFailedPrecondition {
					t.Fatalf("Snapshot during fault = (%v,%v)", snapshot.Msg(), snapshot.Err())
				}
				_ = snapshot.Close()
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("Snapshot during fault error = %v", err)
			}
			if err := putVertex(ctx, node, "repair/marker"); err != nil {
				t.Fatalf("repairing next PutVertex: %v", err)
			}
			if got := node.svc.LocalSeq(nodeID); got != beforeOrigin+2 {
				t.Fatalf("repaired origin seq = %d, want %d", got, beforeOrigin+2)
			}
			if got := node.log.Len(); got != int(beforeLocal)+2 {
				t.Fatalf("repaired local log len = %d, want %d", got, beforeLocal+2)
			}
			feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: beforeLocal + 1}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = feed.Close() }()
			if !feed.Receive() {
				t.Fatalf("repaired pending mutation missing: %v", feed.Err())
			}
			pending := feed.Msg().GetMutation()
			if pending.GetSeq() != beforeOrigin+1 {
				t.Fatalf("pending seq = %d, want %d", pending.GetSeq(), beforeOrigin+1)
			}
			tc.verify(t, node, pending)
			if tc.name == "DeleteEdge" {
				if _, err := node.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "delete/a", Head: "delete/b"})); connect.CodeOf(err) != connect.CodeNotFound {
					t.Fatalf("graph read after local repair = %v, want NotFound", err)
				}
			}
			if !feed.Receive() || feed.Msg().GetMutation().GetSeq() != beforeOrigin+2 {
				t.Fatalf("next mutation = (%v,%v), want seq %d", feed.Msg(), feed.Err(), beforeOrigin+2)
			}
			healthySnapshot, err := rep.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = healthySnapshot.Close() }()
			if !healthySnapshot.Receive() || healthySnapshot.Msg().GetHeader().GetCutoffSeqPerOrigin()[hex.EncodeToString(nodeID[:])] != beforeOrigin+2 {
				t.Fatalf("repaired Snapshot header = (%v,%v)", healthySnapshot.Msg(), healthySnapshot.Err())
			}
		})
	}
}

func TestGraphReadPublicationCut_BlockedWALThenFault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping h2c publication cut test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	wal := &blockedFailureWAL{entered: make(chan struct{}), release: make(chan struct{})}
	released := false
	defer func() {
		if !released {
			close(wal.release)
		}
	}()
	n := newPumpNodeWithWAL(t, hlc.NodeID{0xD1}, 32, true, wal)
	if _, err := n.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: "cut/a", Head: "cut/b", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
	}})); err != nil {
		t.Fatal(err)
	}
	beforeSeq := n.svc.LocalSeq(n.nodeID)
	beforeLen := n.log.Len()
	wal.armed.Store(true)
	writeDone := make(chan error, 1)
	go func() {
		_, err := n.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "cut/a", Head: "cut/b"}))
		writeDone <- err
	}()
	select {
	case <-wal.entered:
	case <-ctx.Done():
		t.Fatal("DeleteEdge never reached the blocked WAL")
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := n.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "cut/a", Head: "cut/b"}))
		readDone <- err
	}()
	select {
	case err := <-readDone:
		t.Fatalf("read crossed an in-flight publication cut: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(wal.release)
	released = true
	select {
	case err := <-writeDone:
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("failed DeleteEdge = %v, want Unavailable", err)
		}
	case <-ctx.Done():
		t.Fatal("failed DeleteEdge did not return")
	}
	select {
	case err := <-readDone:
		if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
			t.Fatalf("read after failed publication = %v, want gapped FailedPrecondition", err)
		}
	case <-ctx.Done():
		t.Fatal("blocked GetEdge did not return after WAL failure")
	}
	if got := n.svc.LocalSeq(n.nodeID); got != beforeSeq || n.log.Len() != beforeLen {
		t.Fatalf("failed publication changed origin/log cut: seq=%d log=%d, want %d/%d", got, n.log.Len(), beforeSeq, beforeLen)
	}
	if _, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "repair", Value: &pb.Vertex_String_{String_: "done"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
	}})); err != nil {
		t.Fatalf("repairing write: %v", err)
	}
	if _, err := n.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "cut/a", Head: "cut/b"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("read after repair = %v, want NotFound", err)
	}
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

// TestPeerPump_SnapshotCarriesDeleteTombstones covers a three-peer partition:
// A deletes identities while B holds older writes, then C bootstraps from A
// after A's log has gapped. The pre-cutoff Deletes cannot arrive in C's tail;
// when B reconnects later its older Put/Add must still be fenced for D4.
func TestPeerPump_SnapshotCarriesDeleteTombstones(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-peer snapshot recovery test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	primary := newPumpNodeWithSearch(t, hlc.NodeID{0xD1}, 4, true)
	stale := newPumpNode(t, hlc.NodeID{0xD2})
	const victim, tail, head = "deleted-v", "deleted-tail", "deleted-head"
	if _, err := stale.sdk.PutVertex(ctx, victim, "old", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.sdk.PutEdge(ctx, tail, head, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.sdk.AddEdge(ctx, tail, head, 2, time.Hour); err != nil {
		t.Fatal(err)
	}
	staleSeq, _ := stale.log.LastSeq()
	primary.clock.Update(stale.clock.Now())
	if _, err := primary.sdk.DeleteVertex(ctx, victim); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.sdk.DeleteEdge(ctx, tail, head); err != nil {
		t.Fatal(err)
	}
	// Evict both Deletes so C can only learn their floors from Snapshot.
	for i := 0; i < 16; i++ {
		if _, err := primary.sdk.PutVertex(ctx, fmt.Sprintf("delete-gap-%d", i), "live", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// Verify the actual Connect Snapshot advertises both exact source
	// deadlines and counts. These Deletes precede its cutoff, so the tail
	// cannot repair an omitted frame.
	deadlines := primary.cache.CausalMetadataStats()
	stream, err := newReplicationRawClient(t, primary.url).Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var vertexFrames, edgeFrames uint64
	var footer *pb.SnapshotFooter
	for stream.Receive() {
		switch e := stream.Msg().GetEntry().(type) {
		case *pb.SnapshotResponse_VertexTombstone:
			vertexFrames++
			if e.VertexTombstone.GetKey() != victim ||
				!e.VertexTombstone.GetExpiration().AsTime().Equal(deadlines.OldestVertexRetentionDeadline) {
				t.Fatalf("vertex tombstone wire frame = %+v, deadline=%v", e.VertexTombstone, deadlines.OldestVertexRetentionDeadline)
			}
		case *pb.SnapshotResponse_EdgeTombstone:
			edgeFrames++
			if e.EdgeTombstone.GetTail() != tail || e.EdgeTombstone.GetHead() != head ||
				!e.EdgeTombstone.GetExpiration().AsTime().Equal(deadlines.OldestEdgeRetentionDeadline) {
				t.Fatalf("edge tombstone wire frame = %+v, deadline=%v", e.EdgeTombstone, deadlines.OldestEdgeRetentionDeadline)
			}
		case *pb.SnapshotResponse_Footer:
			footer = e.Footer
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if vertexFrames != 1 || edgeFrames != 1 || footer == nil ||
		footer.GetVertexTombstoneCount() != vertexFrames || footer.GetEdgeTombstoneCount() != edgeFrames {
		t.Fatalf("tombstone frames=%d/%d footer=%+v", vertexFrames, edgeFrames, footer)
	}
	boot := newPumpNode(t, hlc.NodeID{0xD3})
	metrics := &observedSearchConfigMetrics{
		gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
	}
	boot.startPumpWithMetrics(ctx, t, []string{primary.url}, metrics)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && metrics.snapshots.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := metrics.snapshots.Load(); got != 1 {
		t.Fatalf("gap repair snapshots=%d, want 1", got)
	}
	// A second real Connect pump now delivers B's stale origin stream.
	boot.startPump(ctx, t, []string{stale.url})
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && boot.svc.LocalSeq(stale.nodeID) < staleSeq {
		time.Sleep(10 * time.Millisecond)
	}
	if got := boot.svc.LocalSeq(stale.nodeID); got != staleSeq {
		t.Fatalf("stale peer applied seq=%d, want %d", got, staleSeq)
	}
	if _, ok := boot.cache.GetVertex(victim); ok {
		t.Fatal("pre-cutoff DeleteVertex lost: stale PutVertex resurrected on bootstrap peer")
	}
	if _, _, ok := boot.cache.GetEdgeDetail(tail, head); ok {
		t.Fatal("pre-cutoff DeleteEdge lost: stale PutEdge/AddEdge resurrected on bootstrap peer")
	}
}

type scriptedTombstoneSnapshotPeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	frames         []*pb.SnapshotResponse
	subscribeCalls atomic.Int32
}

func (*scriptedTombstoneSnapshotPeer) PeerStatus(context.Context, *connect.Request[pb.PeerStatusRequest]) (*connect.Response[pb.PeerStatusResponse], error) {
	return connect.NewResponse(&pb.PeerStatusResponse{
		RequiredSnapshotFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
	}), nil
}

func (p *scriptedTombstoneSnapshotPeer) Subscribe(ctx context.Context, _ *connect.Request[pb.SubscribeRequest], _ *connect.ServerStream[pb.SubscribeResponse]) error {
	if p.subscribeCalls.Add(1) == 1 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped"))
	}
	<-ctx.Done()
	return ctx.Err()
}

func (p *scriptedTombstoneSnapshotPeer) Snapshot(_ context.Context, _ *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	for _, frame := range p.frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

type tombstoneSnapshotMetrics struct {
	failed    chan struct{}
	snapshots chan struct{}
}

func (*tombstoneSnapshotMetrics) OnPumpConnect(string)        {}
func (*tombstoneSnapshotMetrics) OnPumpApply(string)          {}
func (*tombstoneSnapshotMetrics) OnPumpDropSelfEcho(string)   {}
func (*tombstoneSnapshotMetrics) OnSearchConfig(string, bool) {}
func (m *tombstoneSnapshotMetrics) OnPumpDisconnect(_ string, reason string) {
	if reason == "snapshot_failed" {
		select {
		case m.failed <- struct{}{}:
		default:
		}
	}
}
func (m *tombstoneSnapshotMetrics) OnPumpSnapshotReplayed(string, uint64, uint64, time.Duration) {
	select {
	case m.snapshots <- struct{}{}:
	default:
	}
}

// TestPeerPump_SnapshotTombstoneFraming_E2E sends malformed and expired
// Snapshot frames through the actual Connect/h2c handler. Invalid framing
// fails before resume; an expired but otherwise valid marker is counted and
// cannot create a fresh D4 window on the receiver.
func TestPeerPump_SnapshotTombstoneFraming_E2E(t *testing.T) {
	stamp := &pb.HLCTimestamp{WallNs: 20, NodeId: append([]byte{0xF1}, make([]byte, 15)...)}
	cutoffOrigin := hlc.NodeID{0xF1}
	for _, tc := range []struct {
		name       string
		marker     *pb.SnapshotVertexTombstone
		footer     *pb.SnapshotFooter
		wantFailed bool
	}{
		{"valid expired", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp, Expiration: timestamppb.New(time.Now().Add(-time.Minute))}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, false},
		{"missing HLC", &pb.SnapshotVertexTombstone{Key: "expired-v", Expiration: timestamppb.New(time.Now().Add(time.Hour))}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, true},
		{"zero NodeID", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: &pb.HLCTimestamp{WallNs: 20, NodeId: make([]byte, 16)}, Expiration: timestamppb.New(time.Now().Add(time.Hour))}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, true},
		{"missing expiration", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, true},
		{"truncated marker count", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp, Expiration: timestamppb.New(time.Now().Add(time.Hour))}, &pb.SnapshotFooter{VertexTombstoneCount: 2}, true},
		{"missing footer", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp, Expiration: timestamppb.New(time.Now().Add(time.Hour))}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := []*pb.SnapshotResponse{
				{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
					CutoffSeqPerOrigin: map[string]uint64{hex.EncodeToString(cutoffOrigin[:]): 7},
					CutoffHlc:          stamp,
				}}},
				{Entry: &pb.SnapshotResponse_VertexTombstone{VertexTombstone: tc.marker}},
			}
			if tc.footer != nil {
				frames = append(frames, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Footer{Footer: tc.footer}})
			}
			peer := &scriptedTombstoneSnapshotPeer{frames: frames}
			mux := http.NewServeMux()
			mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
			srv := httptest.NewUnstartedServer(mux)
			protocols := new(http.Protocols)
			protocols.SetHTTP1(true)
			protocols.SetUnencryptedHTTP2(true)
			srv.Config.Protocols = protocols
			srv.Start()
			t.Cleanup(srv.Close)
			boot := newPumpNode(t, hlc.NodeID{0xF2})
			metrics := &tombstoneSnapshotMetrics{failed: make(chan struct{}, 1), snapshots: make(chan struct{}, 1)}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			t.Cleanup(cancel)
			pump := replication.NewPump(replication.Config{NodeID: boot.nodeID, Peers: []string{srv.URL}, HTTPClient: h2cClient(), BackoffMin: time.Second, BackoffMax: time.Second, Metrics: metrics}, boot.svc, boot.cache)
			go func() { _ = pump.Run(ctx) }()
			var event <-chan struct{} = metrics.snapshots
			if tc.wantFailed {
				event = metrics.failed
			}
			select {
			case <-event:
			case <-ctx.Done():
				t.Fatalf("Snapshot result did not arrive: %v", ctx.Err())
			}
			wantCutoff := uint64(7)
			if tc.wantFailed {
				wantCutoff = 0
			}
			if got := boot.svc.LocalSeq(cutoffOrigin); got != wantCutoff {
				t.Fatalf("Snapshot watermark after replay = %d, want %d", got, wantCutoff)
			}
			if !tc.wantFailed {
				older := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0xF0}}
				if !boot.cache.PutVertexWithExpirationHLC("expired-v", &pb.Vertex{Key: "expired-v"}, time.Now().Add(time.Hour), older) {
					t.Fatal("expired tombstone frame extended the Delete floor")
				}
			}
		})
	}
}

// TestPeerPump_OutcomeAwarePutEdgesGapRecovery is the #1206 regression for
// the real broad_mutate failure: outcome-aware edge Puts filled a tiny log,
// a third node attached through a relay after the retained window had moved,
// and Snapshot had to restore endpoint vertices that edge writes had created
// implicitly. Those endpoints are represented internally by nil *pb.Vertex
// values, but the replication wire must carry canonical nil-valued Vertex
// messages rather than a nil SnapshotVertex payload.
func TestPeerPump_OutcomeAwarePutEdgesGapRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-node gap-recovery test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const logCapacity = 8
	a := newPumpNodeWithSearch(t, hlc.NodeID{0xC1}, logCapacity, true)
	b := newPumpNodeWithSearch(t, hlc.NodeID{0xC2}, logCapacity, true)
	c := newPumpNodeWithSearch(t, hlc.NodeID{0xC3}, logCapacity, true)

	// A and B replicate directly while C is partitioned. B therefore becomes
	// a relay whose tiny local log contains mutations from both origins.
	a.startPump(ctx, t, []string{b.url})
	b.startPump(ctx, t, []string{a.url})
	time.Sleep(150 * time.Millisecond)

	type expectedEdge struct {
		tail, head string
		weight     float32
	}
	var expected []expectedEdge
	writeSet := func(name string, node *pumpNode, baseWeight float32) {
		t.Helper()
		for i := 0; i < 4; i++ {
			tail := fmt.Sprintf("gap/%s/singular/%d", name, i)
			weight := baseWeight + float32(i)
			outcome, err := node.sdk.PutEdge(ctx, tail, "gap/head", weight, time.Hour)
			if err != nil {
				t.Fatalf("%s PutEdge[%d]: %v", name, i, err)
			}
			if outcome != client.PutOutcomeAppliedAndLive {
				t.Fatalf("%s PutEdge[%d] outcome = %s, want APPLIED_AND_LIVE", name, i, outcome)
			}
			expected = append(expected, expectedEdge{tail: tail, head: "gap/head", weight: weight})
		}
		for batch := 0; batch < 4; batch++ {
			inputs := make([]client.EdgeInput, 0, 2)
			for item := 0; item < 2; item++ {
				tail := fmt.Sprintf("gap/%s/plural/%d/%d", name, batch, item)
				weight := baseWeight + 10 + float32(2*batch+item)
				inputs = append(inputs, client.EdgeInput{
					Tail: tail, Head: "gap/head", Weight: weight,
					Expiration: time.Now().Add(time.Hour),
				})
				expected = append(expected, expectedEdge{tail: tail, head: "gap/head", weight: weight})
			}
			results, err := node.sdk.PutEdges(ctx, inputs)
			if err != nil {
				t.Fatalf("%s PutEdges[%d]: %v", name, batch, err)
			}
			for i, result := range results {
				if result.Outcome != client.PutOutcomeAppliedAndLive {
					t.Fatalf("%s PutEdges[%d][%d] outcome = %s, want APPLIED_AND_LIVE", name, batch, i, result.Outcome)
				}
			}
		}
	}
	writeSet("a", a, 100)
	writeSet("b", b, 200)

	// An accepted-expired Put is absent from the live graph but must survive
	// gap recovery as a causal floor. This stays Put-only: mixed Put/Add on one
	// identity belongs to the reset-aware contribution contract in #1203.
	expiredOutcome, err := a.sdk.PutEdgeAt(
		ctx, "gap/expired", "gap/head", 999, time.Now().Add(-time.Second),
	)
	if err != nil {
		t.Fatalf("a born-expired PutEdgeAt: %v", err)
	}
	if expiredOutcome != client.PutOutcomeExpired {
		t.Fatalf("a born-expired PutEdgeAt outcome = %s, want EXPIRED", expiredOutcome)
	}

	for _, edge := range expected {
		for _, node := range []*pumpNode{a, b} {
			if got, ok := waitForEdge(t, node.cache, edge.tail, edge.head, edge.weight, 5*time.Second); !ok || got != edge.weight {
				t.Fatalf("pre-gap %x edge %s->%s = (%v,%v), want (%v,true)", node.nodeID[0], edge.tail, edge.head, got, ok, edge.weight)
			}
		}
	}
	barrierDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(barrierDeadline) {
		_, edgeBarriers := b.cache.CausalBarrierCounts()
		if edgeBarriers == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, edgeBarriers := b.cache.CausalBarrierCounts(); edgeBarriers != 1 {
		t.Fatalf("relay edge causal barriers = %d, want 1 before gap recovery", edgeBarriers)
	}

	// More cluster mutations than B's ring capacity make a local-seq=1
	// Subscribe deterministically gapped. This is the same real Connect/h2c
	// surface the pump uses, asserted explicitly before C starts recovery.
	relayReplication := newReplicationRawClient(t, b.url)
	gapStream, err := relayReplication.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err == nil {
		if gapStream.Receive() {
			t.Fatal("gapped direct Subscribe unexpectedly delivered a mutation")
		}
		err = gapStream.Err()
		_ = gapStream.Close()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("direct Subscribe gap error = %v (code %v), want FailedPrecondition", err, connect.CodeOf(err))
	}

	metrics := &observedSearchConfigMetrics{
		gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
	}
	c.startPumpWithMetrics(ctx, t, []string{b.url}, metrics)
	for _, edge := range expected {
		if got, ok := waitForWireEdge(t, ctx, c.raw, edge.tail, edge.head, edge.weight, 8*time.Second); !ok || got != edge.weight {
			t.Fatalf("snapshot relay edge %s->%s = (%v,%v), want (%v,true)", edge.tail, edge.head, got, ok, edge.weight)
		}
	}
	snapshotDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(snapshotDeadline) && metrics.snapshots.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := metrics.snapshots.Load(); got != 1 {
		t.Fatalf("completed snapshot count = %d, want exactly 1", got)
	}
	if _, edgeBarriers := c.cache.CausalBarrierCounts(); edgeBarriers != 1 {
		t.Fatalf("snapshot edge causal barriers = %d, want 1", edgeBarriers)
	}
	if c.cache.PutEdgeWithExpirationHLC(
		"gap/expired", "gap/head", 1, time.Now().Add(time.Hour),
		hlc.Timestamp{WallNs: 1, NodeID: hlc.NodeID{0x01}},
	) {
		t.Fatal("snapshot lost born-expired edge causal floor")
	}

	// Prove the Snapshot cutoff stitched to the live Subscribe tail on the
	// same relay, for both singular and plural outcome-aware Put forms.
	postSingular := expectedEdge{tail: "gap/post/singular", head: "gap/head", weight: 301}
	if outcome, err := a.sdk.PutEdge(ctx, postSingular.tail, postSingular.head, postSingular.weight, time.Hour); err != nil || outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("post-snapshot PutEdge = (%s,%v), want (APPLIED_AND_LIVE,nil)", outcome, err)
	}
	postPlural := []expectedEdge{
		{tail: "gap/post/plural/0", head: "gap/head", weight: 302},
		{tail: "gap/post/plural/1", head: "gap/head", weight: 303},
	}
	results, err := b.sdk.PutEdges(ctx, []client.EdgeInput{
		{Tail: postPlural[0].tail, Head: postPlural[0].head, Weight: postPlural[0].weight, Expiration: time.Now().Add(time.Hour)},
		{Tail: postPlural[1].tail, Head: postPlural[1].head, Weight: postPlural[1].weight, Expiration: time.Now().Add(time.Hour)},
	})
	if err != nil || len(results) != 2 || results[0].Outcome != client.PutOutcomeAppliedAndLive || results[1].Outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("post-snapshot PutEdges = (%+v,%v), want two APPLIED_AND_LIVE outcomes", results, err)
	}
	expected = append(expected, postSingular)
	expected = append(expected, postPlural...)

	for _, edge := range expected {
		for _, node := range []*pumpNode{a, b, c} {
			if got, ok := waitForWireEdge(t, ctx, node.raw, edge.tail, edge.head, edge.weight, 5*time.Second); !ok || got != edge.weight {
				t.Errorf("final %x edge %s->%s = (%v,%v), want (%v,true)", node.nodeID[0], edge.tail, edge.head, got, ok, edge.weight)
			}
		}
	}
	waitForSearchConvergence(t, ctx, "gap", nil, a.raw, b.raw, c.raw)
}
