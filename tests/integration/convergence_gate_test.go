package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/proto"
)

// convergenceNode is a tombstone-enabled sibling of pumpNode
// (peer_pump_test.go). The only difference from newPumpNode is the
// extra WithTombstoneTTL on the service: the partition→heal convergence
// gate below exercises Delete vs Put races, which only converge when
// the replication apply path stamps HLC tombstones (a delete handled
// via the non-HLC backend leaves no fence, so a re-delivered older Put
// can resurrect the key on some replicas but not others). Everything
// else — the Connect/h2c httptest server, the SDK client, the
// startPump / waitForVertex / waitForEdge helpers — is reused verbatim
// from the pump harness, so a convergenceNode IS a *pumpNode.
func newConvergenceNode(t *testing.T, nodeID hlc.NodeID, tombstoneTTL time.Duration) *pumpNode {
	t.Helper()
	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(nodeID, hlc.Options{})
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).
		WithReplication(log, clock, nil).
		WithTombstoneTTL(tombstoneTTL)
	rep := service.NewLanternReplicationService(log, cache, clock).
		WithOriginStates(svc).
		WithSearchConfig(svc)
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

// waitForVertexAbsent polls cache.GetVertex until the key is gone or the
// deadline elapses. It is the tombstone-aware counterpart of
// waitForVertex: a deleted key must converge to *absent* on every
// replica, so the assertion waits for the terminal "not present" state
// rather than a value.
func waitForVertexAbsent(t *testing.T, cache *graphcache.GraphCache[string, *pb.Vertex], key string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok := cache.GetVertex(key); !ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, ok := cache.GetVertex(key)
	return !ok
}

// waitForVertexValue polls cache.GetVertex until the key holds the
// expected string value or the deadline elapses. Returns the last value
// observed (for diagnostics) and whether it matched. Used to assert that
// an LWW conflict converges to the higher-HLC writer's payload on every
// replica.
func waitForVertexValue(t *testing.T, cache *graphcache.GraphCache[string, *pb.Vertex], key, want string, timeout time.Duration) (string, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		if v, ok := cache.GetVertex(key); ok {
			if s, err := client.StringValue(v); err == nil {
				last = s
				if s == want {
					return s, true
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return last, false
}

// TestConvergence_PartitionHealReconverges is the HA correctness gate
// for issue #715. It stands up a three-replica leaderless cluster, holds
// the peer pumps DOWN to simulate a network partition, applies divergent
// writes to individual replicas, then HEALS the partition by attaching a
// full-mesh of pumps and asserts that every replica reconverges to one
// byte-identical state.
//
// The gate covers exactly the conflict classes the replication RFC
// (docs/replication.md §"Conflict resolution") guarantees converge under
// arbitrary delivery order:
//
//   - LWW-Register (PutVertex / PutEdge): the strictly-higher HLC wins.
//   - Additive merge (AddEdge): contributions from distinct origins carry
//     distinct ContribIDs and SUM; re-delivery dedups.
//   - Tombstone (Delete with a HIGHER HLC than the racing Put): the
//     delete wins and the key converges to absent. Any Put whose HLC is
//     strictly older than the delete is dropped on every replica
//     (README "LANTERN_TOMBSTONE_TTL"), so the outcome is delivery-order
//     independent.
//
// The reverse race — a Put whose HLC is *newer* than a delete
// (resurrection) — is intentionally NOT asserted here: the project only
// guarantees that an older Put is fenced, and ha-runbook.md documents
// resurrection as a known hazard rather than a convergence guarantee. A
// gate that asserted it would encode a property the system does not
// claim to provide.
//
// The winner of each LWW race is made deterministic by sequencing: all
// "loser" writes happen first, then a real-time gap, then the "winner"
// writes. Because every replica reads the same wall clock, the later
// write carries the strictly-higher HLC.
func TestConvergence_PartitionHealReconverges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping partition→heal convergence gate in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Tombstone retention comfortably exceeds every write TTL below so the
	// D4 clamp never rejects a write and tombstones never expire mid-test;
	// convergence is decided on the stored HLC, not on local expiration.
	const (
		tombstoneTTL = 24 * time.Hour
		writeTTL     = time.Hour
		converge     = 8 * time.Second
	)

	a := newConvergenceNode(t, hlc.NodeID{0xA1}, tombstoneTTL)
	b := newConvergenceNode(t, hlc.NodeID{0xB2}, tombstoneTTL)
	c := newConvergenceNode(t, hlc.NodeID{0xC3}, tombstoneTTL)
	nodes := []*pumpNode{a, b, c}

	// ----------------------------------------------------------------
	// Phase 1 — PARTITION. No pumps are running, so writes stay local to
	// the replica that received them and the cluster diverges.
	// ----------------------------------------------------------------

	// Round 1: the "loser" / uncontested writes (lower HLC).
	if err := a.sdk.PutVertex(ctx, "ow", "ow-from-A", writeTTL); err != nil {
		t.Fatalf("a.PutVertex(ow): %v", err)
	}
	if err := a.sdk.PutVertex(ctx, "del", "doomed", writeTTL); err != nil {
		t.Fatalf("a.PutVertex(del): %v", err)
	}
	if err := a.sdk.PutVertex(ctx, "only-a", "only-on-A", writeTTL); err != nil {
		t.Fatalf("a.PutVertex(only-a): %v", err)
	}
	if err := a.sdk.PutEdge(ctx, "pe-t", "pe-h", 5.0, writeTTL); err != nil {
		t.Fatalf("a.PutEdge: %v", err)
	}
	// Additive merge: A and B each contribute 1.0 to the same edge from
	// distinct origins, so the contributions sum to 2.0 cluster-wide.
	if _, err := a.sdk.AddEdge(ctx, "m-t", "m-h", 1.0, writeTTL); err != nil {
		t.Fatalf("a.AddEdge: %v", err)
	}
	if _, err := b.sdk.AddEdge(ctx, "m-t", "m-h", 1.0, writeTTL); err != nil {
		t.Fatalf("b.AddEdge: %v", err)
	}
	if err := c.sdk.PutVertex(ctx, "only-c", "only-on-C", writeTTL); err != nil {
		t.Fatalf("c.PutVertex(only-c): %v", err)
	}

	// Real-time gap so the Round 2 writes carry a strictly-higher HLC
	// (every replica shares the same wall clock).
	time.Sleep(25 * time.Millisecond)

	// Round 2: the "winner" writes on B (higher HLC).
	if err := b.sdk.PutVertex(ctx, "ow", "ow-from-B", writeTTL); err != nil {
		t.Fatalf("b.PutVertex(ow): %v", err)
	}
	if _, err := b.sdk.DeleteVertex(ctx, "del"); err != nil {
		t.Fatalf("b.DeleteVertex(del): %v", err)
	}
	if err := b.sdk.PutEdge(ctx, "pe-t", "pe-h", 9.0, writeTTL); err != nil {
		t.Fatalf("b.PutEdge: %v", err)
	}

	// ----------------------------------------------------------------
	// Phase 2 — HEAL. Attach a full mesh of pumps. Each pump subscribes
	// to its peers from an empty cursor and replays their entire logs, so
	// every divergent mutation reaches every replica.
	// ----------------------------------------------------------------
	a.startPump(ctx, t, []string{b.url, c.url})
	b.startPump(ctx, t, []string{a.url, c.url})
	c.startPump(ctx, t, []string{a.url, b.url})

	// ----------------------------------------------------------------
	// Phase 3 — CONVERGENCE. Every replica must reach the same terminal
	// state for the full controlled keyspace.
	// ----------------------------------------------------------------
	nodeName := map[*pumpNode]string{a: "A", b: "B", c: "C"}

	for _, n := range nodes {
		name := nodeName[n]

		// LWW vertex: B's higher-HLC write wins on every replica.
		if got, ok := waitForVertexValue(t, n.cache, "ow", "ow-from-B", converge); !ok {
			t.Errorf("node %s: vertex %q did not converge to %q (last=%q)", name, "ow", "ow-from-B", got)
		}

		// Uncontested writes from A and C propagate everywhere.
		if got, ok := waitForVertexValue(t, n.cache, "only-a", "only-on-A", converge); !ok {
			t.Errorf("node %s: vertex %q did not converge to %q (last=%q)", name, "only-a", "only-on-A", got)
		}
		if got, ok := waitForVertexValue(t, n.cache, "only-c", "only-on-C", converge); !ok {
			t.Errorf("node %s: vertex %q did not converge to %q (last=%q)", name, "only-c", "only-on-C", got)
		}

		// Tombstone: B's delete has the higher HLC, so the key converges
		// to absent on every replica (A's older Put is fenced).
		if !waitForVertexAbsent(t, n.cache, "del", converge) {
			t.Errorf("node %s: vertex %q did not converge to absent (tombstone-wins)", name, "del")
		}

		// Additive merge: two distinct-origin contributions sum to 2.0.
		if w, ok := waitForEdge(t, n.cache, "m-t", "m-h", 2.0, converge); !ok || w != 2.0 {
			t.Errorf("node %s: edge m-t->m-h converged to w=%v ok=%v, want 2.0/true", name, w, ok)
		}

		// LWW edge: B's higher-HLC PutEdge (9.0) replaces A's 5.0.
		if w, ok := waitForEdge(t, n.cache, "pe-t", "pe-h", 9.0, converge); !ok || w != 9.0 {
			t.Errorf("node %s: edge pe-t->pe-h converged to w=%v ok=%v, want 9.0/true", name, w, ok)
		}
	}

	// Belt-and-suspenders: assert byte-identical vertex payloads and
	// exact edge weights across all three replicas, not merely that each
	// reached its own expected value. This is the literal "byte-for-byte
	// identical state" the gate promises.
	assertReplicasIdentical(t, nodes,
		[]string{"ow", "del", "only-a", "only-c", "pe-t", "pe-h", "m-t", "m-h"},
		[][2]string{{"m-t", "m-h"}, {"pe-t", "pe-h"}})
}

// assertReplicasIdentical fails the test if any vertex key or edge pair
// differs across the supplied replicas. Vertex presence+payload is
// compared by deterministic proto marshaling; edge presence+weight by
// exact float32 equality. An absent key must be absent on every replica.
func assertReplicasIdentical(t *testing.T, nodes []*pumpNode, vertexKeys []string, edges [][2]string) {
	t.Helper()
	if len(nodes) < 2 {
		return
	}
	ref := nodes[0]
	for _, key := range vertexKeys {
		refV, refOK := ref.cache.GetVertex(key)
		for _, n := range nodes[1:] {
			gotV, gotOK := n.cache.GetVertex(key)
			if refOK != gotOK {
				t.Errorf("vertex %q presence diverged: node0=%v nodeN=%v", key, refOK, gotOK)
				continue
			}
			if refOK && string(mustMarshalVertex(t, refV)) != string(mustMarshalVertex(t, gotV)) {
				t.Errorf("vertex %q payload diverged across replicas", key)
			}
		}
	}
	for _, e := range edges {
		refW, refOK := ref.cache.GetWeight(e[0], e[1])
		for _, n := range nodes[1:] {
			gotW, gotOK := n.cache.GetWeight(e[0], e[1])
			if refOK != gotOK {
				t.Errorf("edge %v presence diverged: node0=%v nodeN=%v", e, refOK, gotOK)
				continue
			}
			if refOK && refW != gotW {
				t.Errorf("edge %v weight diverged: node0=%g nodeN=%g", e, refW, gotW)
			}
		}
	}
}

// mustMarshalVertex deterministically marshals a vertex for byte-equality
// comparison across replicas. Kept local to the integration suite so it
// does not depend on the service package's test-only helper of the same
// shape.
func mustMarshalVertex(t *testing.T, v *pb.Vertex) []byte {
	t.Helper()
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(v)
	if err != nil {
		t.Fatalf("marshal vertex: %v", err)
	}
	return b
}
