package integration_test

import (
	"context"
	"net"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// antiEntropyNode is a deliberately stripped-down variant of pumpNode
// that wires WithOriginStates on the replication service so PeerStatus
// returns real data (instead of Unavailable). The pump is NOT started
// — the whole point of this test is to prove the anti-entropy driver
// alone can heal a missed write.
type antiEntropyNode struct {
	addr   string
	cache  *cachegraph.GraphCache[string, *pb.Vertex]
	clock  *hlc.Clock
	log    *mutationlog.Log
	svc    *service.LanternService
	sdk    *client.Lantern
	nodeID hlc.NodeID
}

func newAntiEntropyNode(t *testing.T, nodeID hlc.NodeID) *antiEntropyNode {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	clock := hlc.New(nodeID, hlc.Options{})
	cache := cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil)
	pb.RegisterLanternServiceServer(srv, svc)
	pb.RegisterLanternReplicationServiceServer(srv,
		service.NewLanternReplicationService(log, cache, clock).WithOriginStates(svc),
	)
	go func() { _ = srv.Serve(lis) }()

	sdk, err := client.NewLantern(
		lis.Addr().String(),
		client.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	t.Cleanup(func() {
		_ = sdk.Close()
		srv.Stop()
		_ = log.Close()
	})
	return &antiEntropyNode{
		addr: lis.Addr().String(), cache: cache, clock: clock,
		log: log, svc: svc, sdk: sdk, nodeID: nodeID,
	}
}

// TestAntiEntropy_DriverConvergesWithoutPump asserts that the
// anti-entropy driver (#186) alone — without the pump — is enough to
// converge a follower node after a write to the leader. This is the
// convergence safety-net case: the pump is assumed broken / not yet
// connected, and anti-entropy fills the gap on its tick cadence.
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
	// 50ms cadence keeps the test fast without thrashing the loopback.
	// HTTPClient is left zero so the driver constructs its default
	// h2c client (plaintext HTTP/2). The Connect-Go client is
	// configured with connect.WithGRPC() internally so it talks to
	// b's grpc.NewServer peer.
	ae := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		NodeID:           c.nodeID,
		Peers:            []string{b.addr},
		Interval:         50 * time.Millisecond,
		SubscribeTimeout: 2 * time.Second,
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
	if err := b.sdk.PutVertex(ctx, "ae-key", "ae-val", time.Minute); err != nil {
		t.Fatalf("b.PutVertex: %v", err)
	}
	if err := b.sdk.AddEdge(ctx, "ae-key", "ae-head", 2.5, time.Minute); err != nil {
		t.Fatalf("b.AddEdge: %v", err)
	}

	if !waitForVertex(t, c.cache, "ae-key", 3*time.Second) {
		t.Fatalf("c never observed ae-key via anti-entropy")
	}
	if w, ok := waitForEdge(t, c.cache, "ae-key", "ae-head", 2.5, 3*time.Second); !ok || w != 2.5 {
		t.Errorf("c edge ae-key->ae-head: got w=%v ok=%v want 2.5/true", w, ok)
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
