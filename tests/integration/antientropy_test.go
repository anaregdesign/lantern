package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

type mutableAntiEntropyPeerSource struct {
	mu    sync.RWMutex
	peers []string
	err   error
}

func (s *mutableAntiEntropyPeerSource) Resolve(context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.err != nil {
		return nil, s.err
	}
	return append([]string(nil), s.peers...), nil
}

func (s *mutableAntiEntropyPeerSource) set(peers []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers = append([]string(nil), peers...)
	s.err = err
}

type antiEntropyMetricRecorder struct {
	mu              sync.Mutex
	cycles          int
	ticks           map[string]int
	discoveryErrors int
}

func newAntiEntropyMetricRecorder() *antiEntropyMetricRecorder {
	return &antiEntropyMetricRecorder{ticks: make(map[string]int)}
}

func (m *antiEntropyMetricRecorder) OnAntiEntropyCycle() {
	m.mu.Lock()
	m.cycles++
	m.mu.Unlock()
}

func (m *antiEntropyMetricRecorder) OnAntiEntropyTick(peer string) {
	m.mu.Lock()
	m.ticks[peer]++
	m.mu.Unlock()
}

func (*antiEntropyMetricRecorder) OnAntiEntropyBehind(string, string, uint64)   {}
func (*antiEntropyMetricRecorder) OnAntiEntropyCaughtUp(string, string, uint64) {}
func (m *antiEntropyMetricRecorder) OnAntiEntropyError(peer, reason string) {
	if peer != "discovery" || reason != "discovery_failed" {
		return
	}
	m.mu.Lock()
	m.discoveryErrors++
	m.mu.Unlock()
}
func (*antiEntropyMetricRecorder) OnSearchConfig(string, bool) {}

func (m *antiEntropyMetricRecorder) snapshot(peer string) (cycles, ticks, discoveryErrors int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cycles, m.ticks[peer], m.discoveryErrors
}

// antiEntropyNode is a deliberately stripped-down variant of pumpNode
// that wires WithOriginStates on the replication service so PeerStatus
// returns real data (instead of Unavailable). The pump is NOT started
// — the whole point of this test is to prove the anti-entropy driver
// alone can heal a missed write.
type antiEntropyNode struct {
	addr   string // host:port form, for replication.AntiEntropy peer config
	url    string // full http:// URL, for SDK + raw Connect clients
	cache  *graphcache.GraphCache[string, *pb.Vertex]
	clock  *hlc.Clock
	log    *mutationlog.Log
	svc    *service.LanternService
	sdk    *client.Lantern
	raw    graphv1connect.LanternServiceClient
	nodeID hlc.NodeID
}

func newAntiEntropyNode(t *testing.T, nodeID hlc.NodeID) *antiEntropyNode {
	return newAntiEntropyNodeWithCapacity(t, nodeID, 1024)
}

func newAntiEntropyNodeWithCapacity(t *testing.T, nodeID hlc.NodeID, logCapacity int) *antiEntropyNode {
	t.Helper()
	mlog := mutationlog.New(mutationlog.Options{Capacity: logCapacity, SubscriberBuffer: 1024})
	t.Cleanup(func() { _ = mlog.Close() })
	clock := hlc.New(nodeID, hlc.Options{})
	limits := productionSearchLimits(true, true)
	cache := newProductionSearchCache(time.Minute, true, true, limits.AnalysisLimits)
	svc := service.NewLanternService(cache).
		WithSearchLimits(limits).
		WithTombstoneTTL(time.Hour).
		WithReplication(mlog, clock, nil)
	rep := service.NewLanternReplicationService(mlog, cache, clock).
		WithOriginStates(svc).
		WithSearchConfig(svc)
	srv := newConnectTestServer(t, svc, rep)

	sdk := newConnectClientFor(t, srv.url)
	u, err := url.Parse(srv.url)
	if err != nil {
		t.Fatalf("parse %q: %v", srv.url, err)
	}
	return &antiEntropyNode{
		addr: u.Host, url: srv.url, cache: cache, clock: clock,
		log: mlog, svc: svc, sdk: sdk,
		raw: graphv1connect.NewLanternServiceClient(h2cClient(), srv.url), nodeID: nodeID,
	}
}

func TestAntiEntropy_GappedLogRebuildsExactSearchIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping anti-entropy snapshot search test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	source := newAntiEntropyNodeWithCapacity(t, hlc.NodeID{0xB1}, 4)
	follower := newAntiEntropyNode(t, hlc.NodeID{0xC1})
	inputs := make([]client.VertexInput, 12)
	for i := range inputs {
		inputs[i] = client.VertexInput{
			Key:        fmt.Sprintf("ae-gap-%02d", i),
			Value:      fmt.Sprintf("anti snapshot common-%02d", i),
			Expiration: time.Now().Add(time.Hour),
		}
	}
	for _, input := range inputs {
		if _, err := source.sdk.PutVertexAt(ctx, input.Key, input.Value, input.Expiration); err != nil {
			t.Fatalf("source PutVertexAt(%q): %v", input.Key, err)
		}
	}
	if first, ok := source.log.FirstSeq(); !ok || first <= 1 {
		t.Fatalf("source log did not evict: first=%d ok=%v", first, ok)
	}

	ae := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		NodeID:                  follower.nodeID,
		Peers:                   []string{source.url},
		Interval:                25 * time.Millisecond,
		SubscribeTimeout:        2 * time.Second,
		HTTPClient:              h2cClient(),
		SearchConfigFingerprint: follower.svc.SearchConfigFingerprint(),
	}, follower.svc, follower.svc, follower.cache)
	aeCtx, cancelAE := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ae.Run(aeCtx)
	}()
	t.Cleanup(func() {
		cancelAE()
		<-done
	})

	got := waitForSearchConvergence(t, ctx, "anti snapshot", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}, source.raw, follower.raw)
	if len(got) != len(inputs) {
		t.Fatalf("anti-entropy snapshot search hits = %d, want %d", len(got), len(inputs))
	}
	status, err := follower.raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil {
		t.Fatalf("follower GetServerStatus: %v", err)
	}
	stats := status.Msg.GetSearch().GetIndexStats()
	if stats.GetHealth() != pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY || stats.GetDocuments() != uint64(len(inputs)) {
		t.Fatalf("anti-entropy snapshot search stats = %+v", stats)
	}

	// The remembered same-responder local cutoff lets the next repair use the
	// retained tail instead of falling back to another snapshot immediately.
	if _, err := source.sdk.PutVertex(ctx, "ae-gap-tail", "anti snapshot tail", time.Hour); err != nil {
		t.Fatalf("source tail PutVertex: %v", err)
	}
	if got := waitForSearchConvergence(t, ctx, "anti snapshot tail", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}, source.raw, follower.raw); len(got) != 1 {
		t.Fatalf("anti-entropy post-snapshot tail = %+v", got)
	}
}

// TestAntiEntropy_DriverConvergesWithoutPump asserts that the
// anti-entropy driver (#186) alone — without the pump — is enough to
// converge a follower node after a write to the leader. Convergence
// safety-net case: the pump is assumed broken / not yet connected,
// and anti-entropy fills the gap on its tick cadence.
func TestAntiEntropy_DriverConvergesWithoutPump(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping anti-entropy convergence test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// B is the leader/writer. C is the follower. B has no pump
	// pointing at C and no anti-entropy of its own — it just serves
	// reads of its mutation log.
	b := newAntiEntropyNode(t, hlc.NodeID{0x0B})
	c := newAntiEntropyNode(t, hlc.NodeID{0x0C})

	// Wire C's anti-entropy driver at B with a short tick interval.
	// 50ms cadence keeps the test fast without thrashing loopback.
	// HTTPClient is supplied so the driver speaks h2c against the
	// httptest server (replication.defaultH2CClient is unexported).
	ae := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		NodeID:                  c.nodeID,
		Peers:                   []string{b.url},
		Interval:                50 * time.Millisecond,
		SubscribeTimeout:        2 * time.Second,
		HTTPClient:              h2cClient(),
		SearchConfigFingerprint: c.svc.SearchConfigFingerprint(),
	}, c.svc, c.svc, c.cache)
	aeCtx, cancelAE := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = ae.Run(aeCtx); close(done) }()
	t.Cleanup(func() {
		cancelAE()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Log("anti-entropy did not stop within 2s")
		}
	})

	// Write to B; C has no pump, so until the first anti-entropy tick
	// fires, C MUST NOT see it. After the first tick (and at most one
	// catch-up Subscribe round-trip), C MUST see it.
	if _, err := b.sdk.PutVertex(ctx, "ae-key", "common ae-old", time.Minute); err != nil {
		t.Fatalf("b.PutVertex: %v", err)
	}
	if _, err := b.sdk.AddEdge(ctx, "ae-key", "ae-head", 2.5, time.Minute); err != nil {
		t.Fatalf("b.AddEdge: %v", err)
	}

	if !waitForVertex(t, c.cache, "ae-key", 3*time.Second) {
		t.Fatalf("c never observed ae-key via anti-entropy")
	}
	if w, ok := waitForEdge(t, c.cache, "ae-key", "ae-head", 2.5, 3*time.Second); !ok || w != 2.5 {
		t.Errorf("c edge ae-key->ae-head: got w=%v ok=%v want 2.5/true", w, ok)
	}
	waitForSearchConvergence(t, ctx, "common", nil, b.raw, c.raw)

	if _, err := b.sdk.PutVertices(ctx, []client.VertexInput{
		{Key: "ae-key", Value: "common ae-current", Expiration: time.Now().Add(time.Hour)},
		{Key: "ae-delete", Value: "ae-deleted", Expiration: time.Now().Add(time.Hour)},
		{Key: "ae-expire", Value: "ae-expired", Expiration: time.Now().Add(150 * time.Millisecond)},
	}); err != nil {
		t.Fatalf("anti-entropy lifecycle PutVertices: %v", err)
	}
	if _, err := b.sdk.DeleteVertex(ctx, "ae-delete"); err != nil {
		t.Fatalf("anti-entropy DeleteVertex: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	exactAll := &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}
	for _, query := range []string{"ae-old", "ae-deleted", "ae-expired"} {
		if got := waitForSearchConvergence(t, ctx, query, exactAll, b.raw, c.raw); len(got) != 0 {
			t.Errorf("anti-entropy retired query %q = %+v", query, got)
		}
	}
	if got := waitForSearchConvergence(t, ctx, "ae-current", exactAll, b.raw, c.raw); len(got) != 1 {
		t.Fatalf("anti-entropy current query = %+v", got)
	}
}

// TestAntiEntropy_DynamicPeerSource follows the production DNS-discovery
// contract over real h2c: membership changes are picked up on later cycles, a
// transient resolver failure does not stop the driver, and a removed peer is
// no longer polled.
func TestAntiEntropy_DynamicPeerSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dynamic anti-entropy convergence test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	b := newAntiEntropyNode(t, hlc.NodeID{0xDB})
	c := newAntiEntropyNode(t, hlc.NodeID{0xDC})
	follower := newAntiEntropyNode(t, hlc.NodeID{0xDD})
	source := &mutableAntiEntropyPeerSource{}
	source.set([]string{b.url}, nil)
	metrics := newAntiEntropyMetricRecorder()
	ae := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		NodeID:                  follower.nodeID,
		Source:                  source,
		Interval:                25 * time.Millisecond,
		SubscribeTimeout:        2 * time.Second,
		HTTPClient:              h2cClient(),
		Metrics:                 metrics,
		SearchConfigFingerprint: follower.svc.SearchConfigFingerprint(),
	}, follower.svc, follower.svc, follower.cache)
	aeCtx, cancelAE := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = ae.Run(aeCtx); close(done) }()
	t.Cleanup(func() {
		cancelAE()
		<-done
	})

	if _, err := b.sdk.PutVertex(ctx, "dynamic-b", "from-b", time.Minute); err != nil {
		t.Fatalf("b.PutVertex: %v", err)
	}
	if !waitForVertex(t, follower.cache, "dynamic-b", 3*time.Second) {
		t.Fatal("dynamic source never converged the initial peer")
	}

	cyclesBeforeError, _, _ := metrics.snapshot(b.url)
	source.set(nil, errors.New("transient DNS failure"))
	time.Sleep(100 * time.Millisecond)
	cyclesAfterError, _, discoveryErrors := metrics.snapshot(b.url)
	if cyclesAfterError <= cyclesBeforeError {
		t.Fatalf("cycles stalled across discovery error: before=%d after=%d", cyclesBeforeError, cyclesAfterError)
	}
	if discoveryErrors == 0 {
		t.Fatal("discovery failure was not exposed through anti-entropy metrics")
	}

	source.set([]string{c.url}, nil)
	if _, err := c.sdk.PutVertex(ctx, "dynamic-c", "from-c", time.Minute); err != nil {
		t.Fatalf("c.PutVertex: %v", err)
	}
	if !waitForVertex(t, follower.cache, "dynamic-c", 3*time.Second) {
		t.Fatal("dynamic source never converged the added peer")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, cTicks, _ := metrics.snapshot(c.url)
		if cTicks >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dynamic source did not poll the replacement peer twice")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, bTicks, _ := metrics.snapshot(b.url)
	time.Sleep(100 * time.Millisecond)
	_, bTicksLater, _ := metrics.snapshot(b.url)
	if bTicksLater != bTicks {
		t.Fatalf("removed peer kept receiving ticks: before=%d after=%d", bTicks, bTicksLater)
	}
}

// TestAntiEntropy_NoopWithoutPeers asserts that the driver returns
// immediately when configured with zero peers, so a single-instance
// deployment incurs no cost. Mirrors the pump's no-op contract.
func TestAntiEntropy_NoopWithoutPeers(t *testing.T) {
	ae := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		Interval: 10 * time.Millisecond,
	}, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := ae.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestAntiEntropy_NoopWithZeroInterval asserts that an explicit
// Interval=0 disables the driver even when peers are configured.
func TestAntiEntropy_NoopWithZeroInterval(t *testing.T) {
	ae := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		Peers:    []string{"unused:0"},
		Interval: 0,
	}, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := ae.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
