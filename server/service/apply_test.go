package service

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestApplyMutation_BasicOps walks each MutationOp variant once and
// asserts that ApplyMutation hits the matching cache method. The fake
// backend's accumulators are sufficient because the test only verifies
// "we dispatched at least once" — semantic correctness of dedup/LWW is
// covered by TestApplyMutation_Convergence below using the real cache.
func TestApplyMutation_BasicOps(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	ctx := context.Background()
	origin := bytes16("origin-A")
	exp := timestamppb.New(time.Now().Add(time.Hour))
	ts := newHLC(1, origin)

	mustApply := func(t *testing.T, name string, op *pb.MutationOp) {
		t.Helper()
		m := &pb.Mutation{Seq: 1, Hlc: ts, Origin: origin[:], Op: op}
		if err := svc.ApplyMutation(ctx, m); err != nil {
			t.Fatalf("ApplyMutation %s: %v", name, err)
		}
	}

	mustApply(t, "PutVertex", &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
		PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v1", Expiration: exp}},
	}})
	mustApply(t, "PutVertices", &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
		PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "v2", Expiration: exp}}},
	}})
	mustApply(t, "AddEdge", &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
		AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "v1", Head: "v2", Weight: 1, Expiration: exp}},
	}})
	mustApply(t, "AddEdges", &pb.MutationOp{Op: &pb.MutationOp_AddEdges{
		AddEdges: &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "v1", Head: "v2", Weight: 1, Expiration: exp}}},
	}})
	mustApply(t, "PutEdge", &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
		PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "v1", Head: "v2", Weight: 2, Expiration: exp}},
	}})
	mustApply(t, "PutEdges", &pb.MutationOp{Op: &pb.MutationOp_PutEdges{
		PutEdges: &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "v1", Head: "v2", Weight: 3, Expiration: exp}}},
	}})
	mustApply(t, "DeleteEdge", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdge{
		DeleteEdge: &pb.DeleteEdgeRequest{Tail: "v1", Head: "v2"},
	}})
	mustApply(t, "DeleteEdges", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
		DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "v1", Head: "v2"}}},
	}})
	mustApply(t, "DeleteVertex", &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{
		DeleteVertex: &pb.DeleteVertexRequest{Key: "v1"},
	}})
	mustApply(t, "DeleteVertices", &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
		DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"v2"}},
	}})
	mustApply(t, "DeleteVerticesByPrefix", &pb.MutationOp{Op: &pb.MutationOp_DeleteVerticesByPrefix{
		DeleteVerticesByPrefix: &pb.DeleteVerticesByPrefixRequest{Prefix: "x"},
	}})

	// Nil-safety: empty mutation, nil op, nil request all return nil err.
	if err := svc.ApplyMutation(ctx, nil); err != nil {
		t.Errorf("ApplyMutation(nil) returned %v, want nil", err)
	}
	if err := svc.ApplyMutation(ctx, &pb.Mutation{}); err != nil {
		t.Errorf("ApplyMutation(empty) returned %v, want nil", err)
	}
}

// TestApplyMutation_ReplicationApplyHook asserts the per-MutationOp
// observability hook (#221) fires exactly once per applied case and
// records the canonical op name used by the
// lantern_replication_apply_total counter. Nil-payload mutations and
// empty mutations must NOT fire the hook (they short-circuit before
// the backend call).
func TestApplyMutation_ReplicationApplyHook(t *testing.T) {
	fb := newFakeBackend()
	var recorded []string
	svc := NewLanternService(fb).
		WithReplicationApplyHook(func(op string) { recorded = append(recorded, op) })
	ctx := context.Background()
	origin := bytes16("origin-A")
	exp := timestamppb.New(time.Now().Add(time.Hour))
	ts := newHLC(1, origin)

	mustApply := func(t *testing.T, op *pb.MutationOp) {
		t.Helper()
		m := &pb.Mutation{Seq: 1, Hlc: ts, Origin: origin[:], Op: op}
		if err := svc.ApplyMutation(ctx, m); err != nil {
			t.Fatalf("ApplyMutation: %v", err)
		}
	}

	cases := []struct {
		want string
		op   *pb.MutationOp
	}{
		{"PutVertex", &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v1", Expiration: exp}}}}},
		{"PutVertices", &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "v2", Expiration: exp}}}}}},
		{"AddEdge", &pb.MutationOp{Op: &pb.MutationOp_AddEdge{AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "v1", Head: "v2", Weight: 1, Expiration: exp}}}}},
		{"AddEdges", &pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "v1", Head: "v2", Weight: 1, Expiration: exp}}}}}},
		{"PutEdge", &pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "v1", Head: "v2", Weight: 2, Expiration: exp}}}}},
		{"PutEdges", &pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "v1", Head: "v2", Weight: 3, Expiration: exp}}}}}},
		{"DeleteEdge", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdge{DeleteEdge: &pb.DeleteEdgeRequest{Tail: "v1", Head: "v2"}}}},
		{"DeleteEdges", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "v1", Head: "v2"}}}}}},
		{"DeleteVertex", &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{DeleteVertex: &pb.DeleteVertexRequest{Key: "v1"}}}},
		{"DeleteVertices", &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"v2"}}}}},
		{"DeleteVerticesByPrefix", &pb.MutationOp{Op: &pb.MutationOp_DeleteVerticesByPrefix{DeleteVerticesByPrefix: &pb.DeleteVerticesByPrefixRequest{Prefix: "x"}}}},
	}
	for _, c := range cases {
		mustApply(t, c.op)
	}

	if len(recorded) != len(cases) {
		t.Fatalf("recorded %d hook calls, want %d (recorded=%v)", len(recorded), len(cases), recorded)
	}
	for i, c := range cases {
		if recorded[i] != c.want {
			t.Errorf("recorded[%d] = %q, want %q", i, recorded[i], c.want)
		}
	}

	// Nil-payload short-circuits and empty mutations must NOT bump the
	// counter — they don't reach the backend.
	before := len(recorded)
	if err := svc.ApplyMutation(ctx, nil); err != nil {
		t.Errorf("ApplyMutation(nil) returned %v, want nil", err)
	}
	if err := svc.ApplyMutation(ctx, &pb.Mutation{}); err != nil {
		t.Errorf("ApplyMutation(empty) returned %v, want nil", err)
	}
	if err := svc.ApplyMutation(ctx, &pb.Mutation{Seq: 2, Hlc: ts, Origin: origin[:], Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: nil}}}); err != nil {
		t.Errorf("ApplyMutation(nil PutVertex) returned %v, want nil", err)
	}
	if len(recorded) != before {
		t.Errorf("hook fired on no-op path: recorded grew from %d to %d", before, len(recorded))
	}
}

// TestApplyMutation_AppendsToLocalLog is the Reading B contract test
// (#415, B-3): a remote-applied mutation MUST land in the local
// mutation log so external Subscribe consumers on this replica observe
// every cluster-wide mutation, not just writes that this replica
// directly received. Replication loops are prevented by the per-origin
// watermark dedup in ApplyMutation itself (covered separately by
// TestApplyMutation_DoesNotReAppendDuplicate), not by bypassing the
// log on apply.
func TestApplyMutation_AppendsToLocalLog(t *testing.T) {
	cache := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 128, SubscriberBuffer: 128})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("origin-A")
	clock := hlc.New(origin, hlc.Options{})
	svc := NewLanternService(cache).
		WithReplication(log, clock, nil)

	ts := newHLC(1, origin)
	exp := timestamppb.New(time.Now().Add(time.Hour))
	m := &pb.Mutation{
		Seq: 1, Hlc: ts, Origin: origin[:],
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v1", Expiration: exp}},
		}},
	}
	if err := svc.ApplyMutation(context.Background(), m); err != nil {
		t.Fatalf("ApplyMutation: %v", err)
	}
	gotSeq, ok := log.LastSeq()
	if !ok {
		t.Fatalf("log empty after ApplyMutation; want one entry under Reading B")
	}
	if gotSeq != 1 {
		t.Errorf("log.LastSeq = %d, want 1 (first entry on a fresh log)", gotSeq)
	}
}

// TestApplyMutation_DoesNotReAppendDuplicate asserts the per-origin
// watermark dedup gate in ApplyMutation: the same (origin, seq) tuple
// arriving twice (e.g. through two different peer hops in a fan-out
// triangle) appends to the local log exactly once. Without this, the
// peer-pump in #184 would amplify every mutation by the fan-out
// factor on external Subscribe streams.
func TestApplyMutation_DoesNotReAppendDuplicate(t *testing.T) {
	cache := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 128, SubscriberBuffer: 128})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("origin-A")
	clock := hlc.New(origin, hlc.Options{})
	svc := NewLanternService(cache).
		WithReplication(log, clock, nil)

	ts := newHLC(1, origin)
	exp := timestamppb.New(time.Now().Add(time.Hour))
	m := &pb.Mutation{
		Seq: 1, Hlc: ts, Origin: origin[:],
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v1", Expiration: exp}},
		}},
	}
	for i := 0; i < 3; i++ {
		if err := svc.ApplyMutation(context.Background(), m); err != nil {
			t.Fatalf("ApplyMutation iter=%d: %v", i, err)
		}
	}
	gotSeq, ok := log.LastSeq()
	if !ok {
		t.Fatalf("log empty after three ApplyMutation calls; want one entry")
	}
	if gotSeq != 1 {
		t.Errorf("log.LastSeq = %d after dedup; want 1 (single append)", gotSeq)
	}
}

// TestApplyMutation_Convergence is the cluster-wide property test. It
// builds three independent caches, generates a random batch of
// mutations from a fixed pool of synthetic origins, then delivers the
// same batch to every node in a SHUFFLED order (with occasional
// duplicates). The post-condition: all three nodes hold identical
// vertex and edge state.
//
// Restrictions for the #182 universe:
//
//   - Per-edge ops are partitioned: each (tail, head) pair receives ONLY
//     Add operations OR ONLY Put operations across the whole trace.
//     Mixing on the same edge composes additive contributions with an
//     LWW reset and is order-sensitive (and is #185/#186 territory).
//
//   - No Delete* ops. Without tombstones a late Put may re-insert a
//     deleted vertex on some nodes but not others; that's exactly what
//     #183 will harden. Delete idempotence on a single node is tested
//     separately in TestApplyMutation_Idempotence below.
//
//   - Add weights are always 1.0 so the float32 running sum is the
//     integer count of distinct contributions (exact in float32 up to
//     2^24). Put weights are integer-valued for the same reason.
func TestApplyMutation_Convergence(t *testing.T) {
	const (
		traces     = 1000
		mutsPerTrc = 12
		numOrigins = 3
		numNodes   = 3
		vertexPool = 6
		edgePool   = 8
	)

	for trace := 0; trace < traces; trace++ {
		t.Run(fmt.Sprintf("trace=%04d", trace), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(trace), 0xC0FFEE))

			origins := make([][16]byte, numOrigins)
			clocks := make([]*hlc.Clock, numOrigins)
			for i := range origins {
				origins[i] = bytes16(fmt.Sprintf("origin-%d", i))
				clocks[i] = hlc.New(origins[i], hlc.Options{})
			}

			// Pre-partition edges into "Add-only" vs "Put-only" so the
			// universe is order-insensitive (see test header). Edges are
			// keyed by (tail, head) and deduped so the same pair never
			// straddles both buckets.
			edgeSet := map[[2]string]bool{}
			for len(edgeSet) < edgePool {
				key := [2]string{
					fmt.Sprintf("v%d", rng.IntN(vertexPool)),
					fmt.Sprintf("v%d", rng.IntN(vertexPool)),
				}
				if _, dup := edgeSet[key]; dup {
					continue
				}
				edgeSet[key] = rng.IntN(2) == 0
			}
			edges := make([][2]string, 0, len(edgeSet))
			for k := range edgeSet {
				edges = append(edges, k)
			}
			sort.Slice(edges, func(i, j int) bool {
				if edges[i][0] != edges[j][0] {
					return edges[i][0] < edges[j][0]
				}
				return edges[i][1] < edges[j][1]
			})
			edgeIsPut := make([]bool, len(edges))
			for i, k := range edges {
				edgeIsPut[i] = edgeSet[k]
			}

			// Build the mutation tape.
			tape := make([]*pb.Mutation, 0, mutsPerTrc)
			seqPerOrigin := make([]uint64, numOrigins)
			for i := 0; i < mutsPerTrc; i++ {
				oi := rng.IntN(numOrigins)
				seqPerOrigin[oi]++
				seq := seqPerOrigin[oi]
				ts := clocks[oi].Now()
				hlcTS := &pb.HLCTimestamp{
					WallNs: ts.WallNs, Logical: ts.Logical,
					NodeId: append([]byte(nil), ts.NodeID[:]...),
				}
				exp := timestamppb.New(time.Now().Add(time.Hour))
				op := randomOp(rng, vertexPool, edges, edgeIsPut, oi, int64(i), exp)
				tape = append(tape, &pb.Mutation{
					Seq: seq, Hlc: hlcTS, Origin: origins[oi][:], Op: op,
				})
			}

			// Three independent nodes, each receives the tape in a
			// distinct shuffled order with random duplicates.
			states := make([]nodeSnapshot, numNodes)
			for n := 0; n < numNodes; n++ {
				cache := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
				svc := NewLanternService(cache)

				delivery := make([]*pb.Mutation, 0, len(tape)*2)
				delivery = append(delivery, tape...)
				// Random duplicates (idempotence stress).
				for i := 0; i < len(tape)/3; i++ {
					delivery = append(delivery, tape[rng.IntN(len(tape))])
				}
				rng.Shuffle(len(delivery), func(i, j int) {
					delivery[i], delivery[j] = delivery[j], delivery[i]
				})

				for _, m := range delivery {
					if err := svc.ApplyMutation(context.Background(), m); err != nil {
						t.Fatalf("node %d ApplyMutation: %v", n, err)
					}
				}
				states[n] = snapshotCache(cache, vertexPool, edges)
			}

			for n := 1; n < numNodes; n++ {
				if diff := states[0].diff(states[n]); diff != "" {
					t.Fatalf("node 0 vs node %d divergence:\n%s", n, diff)
				}
			}
		})
	}
}

// TestApplyMutation_Idempotence applies the same mutation twice against
// a single node and asserts the cache state matches a single application.
// Covers every oneof variant including Delete (where idempotence is
// "second delete is a no-op").
func TestApplyMutation_Idempotence(t *testing.T) {
	origin := bytes16("origin-A")
	exp := timestamppb.New(time.Now().Add(time.Hour))
	ts := newHLC(42, origin)
	mkMutation := func(op *pb.MutationOp) *pb.Mutation {
		return &pb.Mutation{Seq: 7, Hlc: ts, Origin: origin[:], Op: op}
	}

	cases := []struct {
		name string
		op   *pb.MutationOp
	}{
		{"PutVertex", &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "v1", Expiration: exp}},
		}}},
		{"AddEdge", &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
			AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 1.0, Expiration: exp}},
		}}},
		{"PutEdge", &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
			PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 5.0, Expiration: exp}},
		}}},
		{"DeleteVertex", &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{
			DeleteVertex: &pb.DeleteVertexRequest{Key: "v1"},
		}}},
		{"DeleteEdge", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdge{
			DeleteEdge: &pb.DeleteEdgeRequest{Tail: "a", Head: "b"},
		}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cacheA := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
			cacheB := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
			svcA := NewLanternService(cacheA)
			svcB := NewLanternService(cacheB)

			ctx := context.Background()
			if err := svcA.ApplyMutation(ctx, mkMutation(tc.op)); err != nil {
				t.Fatalf("A first apply: %v", err)
			}
			if err := svcB.ApplyMutation(ctx, mkMutation(tc.op)); err != nil {
				t.Fatalf("B first apply: %v", err)
			}
			if err := svcB.ApplyMutation(ctx, mkMutation(tc.op)); err != nil {
				t.Fatalf("B second apply: %v", err)
			}

			vs := []string{"v1", "a", "b"}
			es := [][2]string{{"a", "b"}}
			sa := snapshotCache(cacheA, 0, nil)
			sa.includeKeys(cacheA, vs, es)
			sb := snapshotCache(cacheB, 0, nil)
			sb.includeKeys(cacheB, vs, es)
			if diff := sa.diff(sb); diff != "" {
				t.Errorf("second apply diverged from first apply:\n%s", diff)
			}
		})
	}
}

// TestApplyMutation_WireContribIDDedupsAcrossSeq covers the #588 cross-replica
// path: a client-supplied ContribID carried on the wire dedups a retried
// additive contribution even when it arrives as two distinct mutation-log
// entries (different Seq). Without the wire id the synthesized
// (origin, seq, idx) id differs per entry, so legacy additive contributions
// correctly sum.
func TestApplyMutation_WireContribIDDedupsAcrossSeq(t *testing.T) {
	origin := bytes16("origin-A")
	exp := timestamppb.New(time.Now().Add(time.Hour))

	var cid graph.ContribID
	cid[0], cid[23] = 0xAB, 0x07

	mkAddEdges := func(seq uint64, contribIDs [][]byte) *pb.Mutation {
		return &pb.Mutation{
			Seq:    seq,
			Hlc:    newHLC(int64(seq), origin),
			Origin: origin[:],
			Op: &pb.MutationOp{Op: &pb.MutationOp_AddEdges{
				AddEdges: &pb.AddEdgesRequest{
					Edges:      []*pb.Edge{{Tail: "a", Head: "b", Weight: 2, Expiration: exp}},
					ContribIds: contribIDs,
				},
			}},
		}
	}

	t.Run("same wire ContribID dedups across distinct Seq", func(t *testing.T) {
		cache := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
		svc := NewLanternService(cache)
		ctx := context.Background()
		if err := svc.ApplyMutation(ctx, mkAddEdges(1, [][]byte{cid[:]})); err != nil {
			t.Fatalf("first apply: %v", err)
		}
		if err := svc.ApplyMutation(ctx, mkAddEdges(2, [][]byte{cid[:]})); err != nil {
			t.Fatalf("second apply: %v", err)
		}
		if w, ok := cache.GetWeight("a", "b"); !ok || w != 2 {
			t.Fatalf("weight = %v ok=%v, want 2 true (wire ContribID must dedup the retry)", w, ok)
		}
	})

	t.Run("legacy AddEdges without wire ContribID sums across Seq", func(t *testing.T) {
		cache := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
		svc := NewLanternService(cache)
		ctx := context.Background()
		if err := svc.ApplyMutation(ctx, mkAddEdges(1, nil)); err != nil {
			t.Fatalf("first apply: %v", err)
		}
		if err := svc.ApplyMutation(ctx, mkAddEdges(2, nil)); err != nil {
			t.Fatalf("second apply: %v", err)
		}
		if w, ok := cache.GetWeight("a", "b"); !ok || w != 4 {
			t.Fatalf("weight = %v ok=%v, want 4 true (distinct synthesized ids must sum)", w, ok)
		}
	})
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

func bytes16(s string) [16]byte {
	var b [16]byte
	copy(b[:], s)
	return b
}

func newHLC(wallNs int64, origin [16]byte) *pb.HLCTimestamp {
	return &pb.HLCTimestamp{WallNs: wallNs, Logical: 0, NodeId: origin[:]}
}

// randomOp produces a random per-vertex/per-edge mutation drawn from
// {PutVertex, PutVertices, AddEdge, AddEdges, PutEdge, PutEdges} subject
// to the per-edge partition. Index i provides a deterministic value seed
// so vertex payloads on different nodes converge to the same proto under
// LWW.
func randomOp(rng *rand.Rand, vp int, edges [][2]string, edgeIsPut []bool, oi int, i int64, exp *timestamppb.Timestamp) *pb.MutationOp {
	pickEdge := func(want bool) (int, bool) {
		// scan for an edge matching the desired Add/Put bucket
		offset := rng.IntN(len(edges))
		for s := 0; s < len(edges); s++ {
			idx := (offset + s) % len(edges)
			if edgeIsPut[idx] == want {
				return idx, true
			}
		}
		return 0, false
	}
	switch rng.IntN(6) {
	case 0:
		k := fmt.Sprintf("v%d", rng.IntN(vp))
		return &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: vertexFor(k, oi, i, exp)},
		}}
	case 1:
		vs := []*pb.Vertex{
			vertexFor(fmt.Sprintf("v%d", rng.IntN(vp)), oi, i, exp),
			vertexFor(fmt.Sprintf("v%d", rng.IntN(vp)), oi, i+1, exp),
		}
		return &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
			PutVertices: &pb.PutVerticesRequest{Vertices: vs},
		}}
	case 2:
		if idx, ok := pickEdge(false); ok {
			e := edges[idx]
			return &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
				AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: e[0], Head: e[1], Weight: 1, Expiration: exp}},
			}}
		}
	case 3:
		if idx, ok := pickEdge(false); ok {
			e := edges[idx]
			return &pb.MutationOp{Op: &pb.MutationOp_AddEdges{
				AddEdges: &pb.AddEdgesRequest{Edges: []*pb.Edge{
					{Tail: e[0], Head: e[1], Weight: 1, Expiration: exp},
				}},
			}}
		}
	case 4:
		if idx, ok := pickEdge(true); ok {
			e := edges[idx]
			return &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
				PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: e[0], Head: e[1], Weight: float32(2 + (i % 7)), Expiration: exp}},
			}}
		}
	case 5:
		if idx, ok := pickEdge(true); ok {
			e := edges[idx]
			return &pb.MutationOp{Op: &pb.MutationOp_PutEdges{
				PutEdges: &pb.PutEdgesRequest{Edges: []*pb.Edge{
					{Tail: e[0], Head: e[1], Weight: float32(2 + (i % 7)), Expiration: exp},
				}},
			}}
		}
	}
	// Fallback: a trivial PutVertex so we never return nil.
	k := fmt.Sprintf("v%d", rng.IntN(vp))
	return &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
		PutVertex: &pb.PutVertexRequest{Vertex: vertexFor(k, oi, i, exp)},
	}}
}

// vertexFor returns a proto.Vertex whose payload is a deterministic
// function of (key, origin, mutation index). Two nodes processing the
// same mutation produce byte-equal vertices under proto.Equal.
func vertexFor(key string, origin int, idx int64, exp *timestamppb.Timestamp) *pb.Vertex {
	return &pb.Vertex{
		Key:        key,
		Value:      &pb.Vertex_Int64{Int64: int64(origin)*1000 + idx},
		Expiration: exp,
	}
}

// nodeSnapshot is the canonicalized post-trace state used to compare
// nodes. Vertex values are serialized via proto.Marshal for byte
// equality; edge weights are compared by exact float32 equality
// (Add weight=1 sums and Put integer weights are exact in float32).
type nodeSnapshot struct {
	vertices map[string][]byte
	weights  map[[2]string]float32
}

func snapshotCache(c *graph.GraphCache[string, *pb.Vertex], vp int, edges [][2]string) nodeSnapshot {
	s := nodeSnapshot{
		vertices: map[string][]byte{},
		weights:  map[[2]string]float32{},
	}
	for i := 0; i < vp; i++ {
		k := fmt.Sprintf("v%d", i)
		if v, ok := c.GetVertex(k); ok {
			s.vertices[k] = mustMarshal(v)
		}
	}
	for _, e := range edges {
		if w, ok := c.GetWeight(e[0], e[1]); ok {
			s.weights[[2]string{e[0], e[1]}] = w
		}
	}
	return s
}

// includeKeys extends a snapshot with explicit (vertex, edge) keys.
// Used by the idempotence test where the universe is hand-picked.
func (s *nodeSnapshot) includeKeys(c *graph.GraphCache[string, *pb.Vertex], vs []string, es [][2]string) {
	for _, k := range vs {
		if v, ok := c.GetVertex(k); ok {
			s.vertices[k] = mustMarshal(v)
		}
	}
	for _, e := range es {
		if w, ok := c.GetWeight(e[0], e[1]); ok {
			s.weights[[2]string{e[0], e[1]}] = w
		}
	}
}

func (s nodeSnapshot) diff(other nodeSnapshot) string {
	out := ""
	keys := unionKeys(s.vertices, other.vertices)
	for _, k := range keys {
		a, ok1 := s.vertices[k]
		b, ok2 := other.vertices[k]
		switch {
		case ok1 && !ok2:
			out += fmt.Sprintf("  vertex %q present in A, missing in B\n", k)
		case !ok1 && ok2:
			out += fmt.Sprintf("  vertex %q missing in A, present in B\n", k)
		case string(a) != string(b):
			out += fmt.Sprintf("  vertex %q diverged: A=%x B=%x\n", k, a, b)
		}
	}
	ekeys := unionEdgeKeys(s.weights, other.weights)
	for _, e := range ekeys {
		a, ok1 := s.weights[e]
		b, ok2 := other.weights[e]
		switch {
		case ok1 && !ok2:
			out += fmt.Sprintf("  edge %v present in A (w=%g), missing in B\n", e, a)
		case !ok1 && ok2:
			out += fmt.Sprintf("  edge %v missing in A, present in B (w=%g)\n", e, b)
		case a != b:
			out += fmt.Sprintf("  edge %v weight diverged: A=%g B=%g\n", e, a, b)
		}
	}
	return out
}

func unionKeys(a, b map[string][]byte) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unionEdgeKeys(a, b map[[2]string]float32) [][2]string {
	seen := map[[2]string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([][2]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func mustMarshal(v *pb.Vertex) []byte {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestApplyMutation_TombstoneClampRejectHook covers the #222
// tombstone-clamp counter wiring: any HLC Put/Add path that loses LWW
// inside the s.tombstoneTTL>0 branch must fire the registered hook
// exactly once per rejected case (per-entry in the plural variants).
// Each scenario applies a "winner" mutation at a high HLC first, then
// a strictly-older "loser" mutation for the same key/edge and asserts
// the hook count.
func TestApplyMutation_TombstoneClampRejectHook(t *testing.T) {
	exp := timestamppb.New(time.Now().Add(time.Hour))
	origin := bytes16("origin-A")

	newSvc := func(tsCount *int) *LanternService {
		cache := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
		return NewLanternService(cache).
			WithTombstoneTTL(time.Hour).
			WithTombstoneClampRejectHook(func() { *tsCount++ })
	}

	apply := func(t *testing.T, svc *LanternService, seq uint64, op *pb.MutationOp) {
		t.Helper()
		m := &pb.Mutation{Seq: seq, Hlc: newHLC(int64(seq), origin), Origin: origin[:], Op: op}
		if err := svc.ApplyMutation(context.Background(), m); err != nil {
			t.Fatalf("ApplyMutation seq=%d: %v", seq, err)
		}
	}

	t.Run("PutVertex", func(t *testing.T) {
		var n int
		svc := newSvc(&n)
		put := func(seq uint64) *pb.MutationOp {
			return &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "k", Value: &pb.Vertex_Nil{Nil: true}, Expiration: exp}},
			}}
		}
		apply(t, svc, 5, put(5)) // winner
		apply(t, svc, 1, put(1)) // loser
		if n != 1 {
			t.Fatalf("hook fired %d times, want 1", n)
		}
	})

	t.Run("PutVertices", func(t *testing.T) {
		var n int
		svc := newSvc(&n)
		put := func() *pb.MutationOp {
			return &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
				PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
					{Key: "k1", Value: &pb.Vertex_Nil{Nil: true}, Expiration: exp},
					{Key: "k2", Value: &pb.Vertex_Nil{Nil: true}, Expiration: exp},
				}},
			}}
		}
		apply(t, svc, 5, put()) // both win
		apply(t, svc, 1, put()) // both lose
		if n != 2 {
			t.Fatalf("hook fired %d times, want 2 (one per entry)", n)
		}
	})

	t.Run("PutEdge", func(t *testing.T) {
		var n int
		svc := newSvc(&n)
		put := func() *pb.MutationOp {
			return &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
				PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 1, Expiration: exp}},
			}}
		}
		apply(t, svc, 5, put())
		apply(t, svc, 1, put())
		if n != 1 {
			t.Fatalf("hook fired %d times, want 1", n)
		}
	})

	t.Run("WinnerDoesNotFire", func(t *testing.T) {
		var n int
		svc := newSvc(&n)
		apply(t, svc, 1, &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "k", Value: &pb.Vertex_Nil{Nil: true}, Expiration: exp}},
		}})
		if n != 0 {
			t.Fatalf("hook fired %d times on first apply, want 0", n)
		}
	})

	t.Run("ClampDisabledNeverFires", func(t *testing.T) {
		var n int
		cache := graph.NewGraphCache[string, *pb.Vertex](time.Minute)
		svc := NewLanternService(cache).
			WithTombstoneClampRejectHook(func() { n++ })
		// useTomb=false: the non-HLC backend path is taken, hook stays at 0.
		put := func(seq uint64) *pb.MutationOp {
			return &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "k", Value: &pb.Vertex_Nil{Nil: true}, Expiration: exp}},
			}}
		}
		apply(t, svc, 5, put(5))
		apply(t, svc, 1, put(1))
		if n != 0 {
			t.Fatalf("hook fired %d times with clamp disabled, want 0", n)
		}
	})
}
