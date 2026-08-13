package service

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestApplyMutation_CausalMetadataCapacityConvergesAcrossReplicas pins the
// #1204 admission split: local-origin writes are bounded, but replication
// apply must never reject state another replica already committed. Two peers
// independently fill the same per-kind limit, exchange those commits, and
// therefore each retain more identities than its local limit. Once over the
// limit a genuinely new local identity fails atomically, while replacing an
// identity already represented in the causal ledger remains admissible and
// continues to converge.
func TestApplyMutation_CausalMetadataCapacityConvergesAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	latestMutation := func(t *testing.T, log *mutationlog.Log) *pb.Mutation {
		t.Helper()
		seq, ok := log.LastSeq()
		if !ok {
			t.Fatal("mutation log is empty")
		}
		ch, cancel, err := log.Subscribe(seq)
		if err != nil {
			t.Fatalf("Subscribe(%d): %v", seq, err)
		}
		defer cancel()
		select {
		case entry := <-ch:
			if entry.Seq != seq {
				t.Fatalf("latest log entry seq = %d, want %d", entry.Seq, seq)
			}
			mutation, ok := entry.Op.(*pb.Mutation)
			if !ok {
				t.Fatalf("latest log entry type = %T, want *pb.Mutation", entry.Op)
			}
			return proto.Clone(mutation).(*pb.Mutation)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out reading mutation %d", seq)
			return nil
		}
	}
	newPeer := func(t *testing.T, node byte, limits graphcache.CausalMetadataLimits) (*graphcache.GraphCache[string, *pb.Vertex], *mutationlog.Log, *LanternService) {
		t.Helper()
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		cache.SetCausalMetadataLimits(limits)
		log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32})
		t.Cleanup(func() { _ = log.Close() })
		clock := hlc.New(hlc.NodeID{node}, hlc.Options{})
		return cache, log, NewLanternService(cache).WithReplication(log, clock, nil)
	}

	t.Run("vertices", func(t *testing.T) {
		cacheA, logA, svcA := newPeer(t, 0xA1, graphcache.CausalMetadataLimits{MaxVertexEntries: 1})
		cacheB, logB, svcB := newPeer(t, 0xB1, graphcache.CausalMetadataLimits{MaxVertexEntries: 1})

		put := func(t *testing.T, svc *LanternService, key, value string) error {
			t.Helper()
			_, err := svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
				Key: key, Value: &pb.Vertex_String_{String_: value},
			}})
			return err
		}
		if err := put(t, svcA, "vertex-a", "a1"); err != nil {
			t.Fatalf("peer A local Put: %v", err)
		}
		mutationA := latestMutation(t, logA)
		if err := put(t, svcB, "vertex-b", "b1"); err != nil {
			t.Fatalf("peer B local Put: %v", err)
		}
		mutationB := latestMutation(t, logB)

		if err := svcA.ApplyMutation(ctx, mutationB); err != nil {
			t.Fatalf("peer A apply B: %v", err)
		}
		if err := svcB.ApplyMutation(ctx, mutationA); err != nil {
			t.Fatalf("peer B apply A: %v", err)
		}
		for name, cache := range map[string]*graphcache.GraphCache[string, *pb.Vertex]{"A": cacheA, "B": cacheB} {
			stats := cache.CausalMetadataStats()
			if stats.VertexEntries != 2 || !stats.VertexOverLimit {
				t.Fatalf("peer %s causal stats after convergence = %+v, want 2 entries over limit 1", name, stats)
			}
			for _, key := range []string{"vertex-a", "vertex-b"} {
				if _, ok := cache.GetVertex(key); !ok {
					t.Fatalf("peer %s missing remotely converged %q", name, key)
				}
			}
		}

		beforeA, _ := logA.LastSeq()
		beforeB, _ := logB.LastSeq()
		if err := put(t, svcA, "vertex-c", "c"); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("peer A new identity error = %v (%v), want ResourceExhausted", err, connect.CodeOf(err))
		}
		if err := put(t, svcB, "vertex-c", "c"); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("peer B new identity error = %v (%v), want ResourceExhausted", err, connect.CodeOf(err))
		}
		if after, _ := logA.LastSeq(); after != beforeA {
			t.Fatalf("peer A rejected write appended log seq %d -> %d", beforeA, after)
		}
		if after, _ := logB.LastSeq(); after != beforeB {
			t.Fatalf("peer B rejected write appended log seq %d -> %d", beforeB, after)
		}
		if _, ok := cacheA.GetVertex("vertex-c"); ok {
			t.Fatal("peer A retained rejected new identity")
		}
		if _, ok := cacheB.GetVertex("vertex-c"); ok {
			t.Fatal("peer B retained rejected new identity")
		}

		if err := put(t, svcA, "vertex-a", "a2"); err != nil {
			t.Fatalf("peer A same-identity replacement while over limit: %v", err)
		}
		replacementA := latestMutation(t, logA)
		if err := put(t, svcB, "vertex-b", "b2"); err != nil {
			t.Fatalf("peer B same-identity replacement while over limit: %v", err)
		}
		replacementB := latestMutation(t, logB)
		if err := svcA.ApplyMutation(ctx, replacementB); err != nil {
			t.Fatalf("peer A apply B replacement: %v", err)
		}
		if err := svcB.ApplyMutation(ctx, replacementA); err != nil {
			t.Fatalf("peer B apply A replacement: %v", err)
		}
		for _, key := range []string{"vertex-a", "vertex-b"} {
			gotA, okA := cacheA.GetVertex(key)
			gotB, okB := cacheB.GetVertex(key)
			if !okA || !okB || !proto.Equal(gotA, gotB) {
				t.Fatalf("replacement for %q did not converge: A=(%v,%v) B=(%v,%v)", key, gotA, okA, gotB, okB)
			}
		}
	})

	t.Run("edges", func(t *testing.T) {
		cacheA, logA, svcA := newPeer(t, 0xA2, graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
		cacheB, logB, svcB := newPeer(t, 0xB2, graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})

		put := func(t *testing.T, svc *LanternService, tail, head string, weight float32) error {
			t.Helper()
			_, err := svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: tail, Head: head, Weight: weight}})
			return err
		}
		if err := put(t, svcA, "tail-a", "head-a", 1); err != nil {
			t.Fatalf("peer A local Put: %v", err)
		}
		mutationA := latestMutation(t, logA)
		if err := put(t, svcB, "tail-b", "head-b", 1); err != nil {
			t.Fatalf("peer B local Put: %v", err)
		}
		mutationB := latestMutation(t, logB)

		if err := svcA.ApplyMutation(ctx, mutationB); err != nil {
			t.Fatalf("peer A apply B: %v", err)
		}
		if err := svcB.ApplyMutation(ctx, mutationA); err != nil {
			t.Fatalf("peer B apply A: %v", err)
		}
		for name, cache := range map[string]*graphcache.GraphCache[string, *pb.Vertex]{"A": cacheA, "B": cacheB} {
			stats := cache.CausalMetadataStats()
			if stats.EdgeEntries != 2 || !stats.EdgeOverLimit {
				t.Fatalf("peer %s causal stats after convergence = %+v, want 2 entries over limit 1", name, stats)
			}
			for _, edge := range [][2]string{{"tail-a", "head-a"}, {"tail-b", "head-b"}} {
				if _, ok := cache.GetWeight(edge[0], edge[1]); !ok {
					t.Fatalf("peer %s missing remotely converged edge %q -> %q", name, edge[0], edge[1])
				}
			}
		}

		beforeA, _ := logA.LastSeq()
		beforeB, _ := logB.LastSeq()
		if err := put(t, svcA, "tail-c", "head-c", 3); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("peer A new edge error = %v (%v), want ResourceExhausted", err, connect.CodeOf(err))
		}
		if err := put(t, svcB, "tail-c", "head-c", 3); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("peer B new edge error = %v (%v), want ResourceExhausted", err, connect.CodeOf(err))
		}
		if after, _ := logA.LastSeq(); after != beforeA {
			t.Fatalf("peer A rejected edge appended log seq %d -> %d", beforeA, after)
		}
		if after, _ := logB.LastSeq(); after != beforeB {
			t.Fatalf("peer B rejected edge appended log seq %d -> %d", beforeB, after)
		}
		if _, ok := cacheA.GetWeight("tail-c", "head-c"); ok {
			t.Fatal("peer A retained rejected new edge identity")
		}
		if _, ok := cacheB.GetWeight("tail-c", "head-c"); ok {
			t.Fatal("peer B retained rejected new edge identity")
		}

		if err := put(t, svcA, "tail-a", "head-a", 2); err != nil {
			t.Fatalf("peer A same-edge replacement while over limit: %v", err)
		}
		replacementA := latestMutation(t, logA)
		if err := put(t, svcB, "tail-b", "head-b", 2); err != nil {
			t.Fatalf("peer B same-edge replacement while over limit: %v", err)
		}
		replacementB := latestMutation(t, logB)
		if err := svcA.ApplyMutation(ctx, replacementB); err != nil {
			t.Fatalf("peer A apply B replacement: %v", err)
		}
		if err := svcB.ApplyMutation(ctx, replacementA); err != nil {
			t.Fatalf("peer B apply A replacement: %v", err)
		}
		for _, edge := range [][2]string{{"tail-a", "head-a"}, {"tail-b", "head-b"}} {
			weightA, okA := cacheA.GetWeight(edge[0], edge[1])
			weightB, okB := cacheB.GetWeight(edge[0], edge[1])
			if !okA || !okB || weightA != weightB {
				t.Fatalf("replacement for %q -> %q did not converge: A=(%v,%v) B=(%v,%v)", edge[0], edge[1], weightA, okA, weightB, okB)
			}
		}
	})
}

func TestApplyMutation_ReplicatedPutEntriesPreserveOrderAndAuthoritativeOutcome(t *testing.T) {
	origin := bytes16("ordered-origin")
	ts := newHLC(20, origin)
	liveExpiration := timestamppb.New(time.Now().Add(time.Hour))
	liveVertex := func(value string) *pb.Vertex {
		return &pb.Vertex{Key: "k", Value: &pb.Vertex_String_{String_: value}, Expiration: liveExpiration}
	}
	vertexBarrier := func() *pb.ReplicatedPutVertex {
		return &pb.ReplicatedPutVertex{Outcome: &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: &pb.VertexCausalBarrier{Key: "k"}}}
	}
	vertexLive := func(value string) *pb.ReplicatedPutVertex {
		return &pb.ReplicatedPutVertex{Outcome: &pb.ReplicatedPutVertex_Live{Live: liveVertex(value)}}
	}

	for _, tc := range []struct {
		name       string
		entries    []*pb.ReplicatedPutVertex
		wantLive   bool
		searchTerm string
	}{
		{name: "barrier then live", entries: []*pb.ReplicatedPutVertex{vertexBarrier(), vertexLive("final searchable")}, wantLive: true, searchTerm: "searchable"},
		{name: "live then barrier", entries: []*pb.ReplicatedPutVertex{vertexLive("must disappear"), vertexBarrier()}, searchTerm: "disappear"},
	} {
		t.Run("vertex/"+tc.name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			cache.EnableSearchIndex(func(_ string, v *pb.Vertex) search.Document { return search.Text(v.GetString_()) }, strings.Compare)
			svc := NewLanternService(cache)
			m := &pb.Mutation{Seq: 1, Hlc: ts, Origin: origin[:], Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{
				ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: tc.entries},
			}}}
			if err := svc.ApplyMutation(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			_, live := cache.GetVertex("k")
			if live != tc.wantLive {
				t.Fatalf("vertex live = %v, want %v", live, tc.wantLive)
			}
			hits := cache.SearchVertices(tc.searchTerm, 10, "")
			if tc.wantLive && (len(hits) != 1 || hits[0].ID != "k") {
				t.Fatalf("SearchVertices = %v, want k", hits)
			}
			if !tc.wantLive && len(hits) != 0 {
				t.Fatalf("SearchVertices = %v, want empty", hits)
			}
		})
	}

	edgeBarrier := func() *pb.ReplicatedPutEdge {
		return &pb.ReplicatedPutEdge{Outcome: &pb.ReplicatedPutEdge_CausalBarrier{CausalBarrier: &pb.EdgeCausalBarrier{Tail: "tail", Head: "head"}}}
	}
	edgeLive := func(weight float32) *pb.ReplicatedPutEdge {
		return &pb.ReplicatedPutEdge{Outcome: &pb.ReplicatedPutEdge_Live{Live: &pb.Edge{Tail: "tail", Head: "head", Weight: weight, Expiration: liveExpiration}}}
	}
	for _, tc := range []struct {
		name     string
		entries  []*pb.ReplicatedPutEdge
		wantLive bool
	}{
		{name: "barrier then live", entries: []*pb.ReplicatedPutEdge{edgeBarrier(), edgeLive(4)}, wantLive: true},
		{name: "live then barrier", entries: []*pb.ReplicatedPutEdge{edgeLive(4), edgeBarrier()}},
	} {
		t.Run("edge/"+tc.name, func(t *testing.T) {
			cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			svc := NewLanternService(cache)
			m := &pb.Mutation{Seq: 1, Hlc: ts, Origin: origin[:], Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutEdges{
				ReplicatedPutEdges: &pb.ReplicatedPutEdges{Entries: tc.entries},
			}}}
			if err := svc.ApplyMutation(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			_, live := cache.GetWeight("tail", "head")
			if live != tc.wantLive {
				t.Fatalf("edge live = %v, want %v", live, tc.wantLive)
			}
		})
	}

	t.Run("malformed entry rejects whole batch", func(t *testing.T) {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
		svc := NewLanternService(cache)
		m := &pb.Mutation{Seq: 1, Hlc: ts, Origin: origin[:], Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{
			ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: []*pb.ReplicatedPutVertex{vertexLive("must not apply"), {}}},
		}}}
		if err := svc.ApplyMutation(context.Background(), m); err == nil {
			t.Fatal("malformed ordered entry was accepted")
		}
		if _, ok := cache.GetVertex("k"); ok {
			t.Fatal("valid prefix of malformed batch was partially applied")
		}
	})
}

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
	mustApply(t, "DeleteEdgesByPrefix", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdgesByPrefix{
		DeleteEdgesByPrefix: &pb.DeleteEdgesByPrefixRequest{TailPrefix: "v1"},
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
		{"DeleteEdgesByPrefix", &pb.MutationOp{Op: &pb.MutationOp_DeleteEdgesByPrefix{DeleteEdgesByPrefix: &pb.DeleteEdgesByPrefixRequest{TailPrefix: "v1"}}}},
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
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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
				cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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

// TestApplyMutation_ConvergenceWithTombstones is the delete-inclusive
// sibling of TestApplyMutation_Convergence (#718). It stresses the
// steady-state guarantee that reordered + duplicated delivery of a
// workload that ALSO contains Delete* mutations still converges to an
// identical state on every replica.
//
// TestApplyMutation_Convergence deliberately excludes Delete* because,
// without a tombstone TTL, a late Put re-inserts a deleted key on some
// nodes but not others. Here every replica is built with
// WithTombstoneTTL, so a Delete* installs an HLC-fenced tombstone and a
// strictly-older Put/Add is clamped (LWW) instead of resurrecting the key.
//
// Order-independence is kept inside the documented convergence subset
// (README "Conflict resolution"; docs/ha-runbook.md) by construction:
//
//   - Every mutation carries an EXPLICIT, globally-monotonic wall_ns, so
//     the HLC total order is fixed regardless of delivery order (no ties,
//     so no NodeID tiebreak is needed).
//   - Every terminal Delete is emitted LAST, at a wall_ns strictly greater
//     than every write to the same key, so the tombstone wins under every
//     shuffle and a doomed key is absent on all replicas.
//   - No Put is ever emitted NEWER than a key's Delete, so the test stays
//     out of the resurrection case (a Put strictly newer than a tombstone),
//     which is order-sensitive and a documented hazard, not a convergence
//     guarantee.
//
// Each doomed key is force-written before its Delete so the tombstone
// fences a real value rather than tombstoning an absent key. The trace
// index seeds the PCG stream, so a failing case is reproducible from the
// (trace, seedHi) pair printed on failure.
//
// Standalone vertices (v*) and edge endpoints (p*) live in DISJOINT key
// namespaces on purpose: DeleteVertexHLC tombstones only the vertex, not
// the edges that name it, and PutEdge/AddEdge auto-create their endpoints
// via ensureVertexLocked. A doomed vertex that doubled as a live edge
// endpoint would be resurrected order-dependently (the documented hazard
// in core/graphcache/tombstone.go), which is outside the convergence
// guarantee. Keeping the namespaces disjoint means nothing re-creates a
// doomed vertex, so its tombstone wins under every delivery order.
func TestApplyMutation_ConvergenceWithTombstones(t *testing.T) {
	const (
		traces       = 400
		randWrites   = 14
		numOrigins   = 3
		numNodes     = 3
		vertexPool   = 6
		endpointPool = 5
		edgePool     = 8
		seedHi       = 0x70B5701E
	)

	for trace := 0; trace < traces; trace++ {
		t.Run(fmt.Sprintf("trace=%04d", trace), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(trace), seedHi))

			origins := make([][16]byte, numOrigins)
			for i := range origins {
				origins[i] = bytes16(fmt.Sprintf("origin-%d", i))
			}

			// Edge partition: each (tail, head) pair is Add-only or
			// Put-only across the whole trace (mixing Add/Put on one edge is
			// order-sensitive — same rule as TestApplyMutation_Convergence).
			// Endpoints are drawn from the p* namespace, disjoint from the
			// v* standalone vertices that get deleted (see test header).
			edgeSet := map[[2]string]bool{}
			for len(edgeSet) < edgePool {
				key := [2]string{
					fmt.Sprintf("p%d", rng.IntN(endpointPool)),
					fmt.Sprintf("p%d", rng.IntN(endpointPool)),
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

			// Doom ~40% of vertices and edges. A doomed key receives a
			// terminal Delete (phase 3) at the highest wall_ns in the trace.
			doomedVertex := map[string]bool{}
			for vi := 0; vi < vertexPool; vi++ {
				if rng.IntN(10) < 4 {
					doomedVertex[fmt.Sprintf("v%d", vi)] = true
				}
			}
			doomedEdge := map[[2]string]bool{}
			for _, e := range edges {
				if rng.IntN(10) < 4 {
					doomedEdge[e] = true
				}
			}

			exp := timestamppb.New(time.Now().Add(time.Hour))

			// A single global wall_ns counter gives every mutation a unique
			// HLC, so the total order is fixed by wall_ns alone and is
			// independent of delivery order. seqPerOrigin feeds the per-origin
			// watermark and the synthesized AddEdge ContribID.
			var wall int64
			seqPerOrigin := make([]uint64, numOrigins)
			var valSeed int64
			tape := make([]*pb.Mutation, 0, randWrites+2*vertexPool+2*edgePool)
			push := func(oi int, op *pb.MutationOp) {
				wall++
				seqPerOrigin[oi]++
				tape = append(tape, &pb.Mutation{
					Seq:    seqPerOrigin[oi],
					Hlc:    newHLC(wall, origins[oi]),
					Origin: origins[oi][:],
					Op:     op,
				})
			}
			putEdgeOp := func(e [2]string, w float32) *pb.MutationOp {
				return &pb.MutationOp{Op: &pb.MutationOp_PutEdge{
					PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: e[0], Head: e[1], Weight: w, Expiration: exp}},
				}}
			}
			addEdgeOp := func(e [2]string) *pb.MutationOp {
				return &pb.MutationOp{Op: &pb.MutationOp_AddEdge{
					AddEdge: &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: e[0], Head: e[1], Weight: 1, Expiration: exp}},
				}}
			}

			// Phase 1 — force one write to every doomed key so its terminal
			// tombstone fences a real value (a non-vacuous fence).
			for vi := 0; vi < vertexPool; vi++ {
				k := fmt.Sprintf("v%d", vi)
				if !doomedVertex[k] {
					continue
				}
				oi := rng.IntN(numOrigins)
				valSeed++
				push(oi, &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
					PutVertex: &pb.PutVertexRequest{Vertex: vertexFor(k, oi, valSeed, exp)},
				}})
			}
			for i, e := range edges {
				if !doomedEdge[e] {
					continue
				}
				oi := rng.IntN(numOrigins)
				if edgeIsPut[i] {
					valSeed++
					push(oi, putEdgeOp(e, float32(2+(valSeed%7))))
				} else {
					push(oi, addEdgeOp(e))
				}
			}

			// Phase 2 — a random Put/Add workload over the whole key space
			// (survivors and doomed alike). randomOp never emits Delete*.
			for i := 0; i < randWrites; i++ {
				oi := rng.IntN(numOrigins)
				valSeed++
				push(oi, randomOp(rng, vertexPool, edges, edgeIsPut, oi, valSeed, exp))
			}

			// Phase 3 — terminal Delete* for every doomed key, emitted LAST
			// so each carries a wall_ns greater than every write to that key.
			// Singular and plural variants are chosen at random.
			for vi := 0; vi < vertexPool; vi++ {
				k := fmt.Sprintf("v%d", vi)
				if !doomedVertex[k] {
					continue
				}
				oi := rng.IntN(numOrigins)
				if rng.IntN(2) == 0 {
					push(oi, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{
						DeleteVertex: &pb.DeleteVertexRequest{Key: k},
					}})
				} else {
					push(oi, &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{
						DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{k}},
					}})
				}
			}
			for _, e := range edges {
				if !doomedEdge[e] {
					continue
				}
				oi := rng.IntN(numOrigins)
				if rng.IntN(2) == 0 {
					push(oi, &pb.MutationOp{Op: &pb.MutationOp_DeleteEdge{
						DeleteEdge: &pb.DeleteEdgeRequest{Tail: e[0], Head: e[1]},
					}})
				} else {
					push(oi, &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{
						DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: e[0], Head: e[1]}}},
					}})
				}
			}

			// Deliver the tape to each node in a distinct shuffled order with
			// random duplicates (at-least-once). Every replica enables the
			// HLC-fenced tombstone path via WithTombstoneTTL.
			states := make([]nodeSnapshot, numNodes)
			for n := 0; n < numNodes; n++ {
				cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
				svc := NewLanternService(cache).WithTombstoneTTL(time.Hour)

				delivery := make([]*pb.Mutation, 0, len(tape)*2)
				delivery = append(delivery, tape...)
				for i := 0; i < len(tape)/3; i++ {
					delivery = append(delivery, tape[rng.IntN(len(tape))])
				}
				rng.Shuffle(len(delivery), func(i, j int) {
					delivery[i], delivery[j] = delivery[j], delivery[i]
				})

				for _, m := range delivery {
					if err := svc.ApplyMutation(context.Background(), m); err != nil {
						t.Fatalf("trace=%d seedHi=%#x: node %d ApplyMutation: %v", trace, seedHi, n, err)
					}
				}
				states[n] = snapshotCache(cache, vertexPool, edges)
			}

			// (a) Order-independence: every node converges to node 0's state.
			for n := 1; n < numNodes; n++ {
				if diff := states[0].diff(states[n]); diff != "" {
					t.Fatalf("trace=%d seedHi=%#x: node 0 vs node %d divergence:\n%s", trace, seedHi, n, diff)
				}
			}

			// (b) Tombstone-wins: every doomed key is absent on the converged
			// replica (node 0 stands in for all by the agreement just proven).
			for vi := 0; vi < vertexPool; vi++ {
				k := fmt.Sprintf("v%d", vi)
				if doomedVertex[k] {
					if _, ok := states[0].vertices[k]; ok {
						t.Fatalf("trace=%d seedHi=%#x: doomed vertex %q survived its terminal delete (tombstone lost)", trace, seedHi, k)
					}
				}
			}
			for _, e := range edges {
				if doomedEdge[e] {
					if _, ok := states[0].weights[e]; ok {
						t.Fatalf("trace=%d seedHi=%#x: doomed edge %v survived its terminal delete (tombstone lost)", trace, seedHi, e)
					}
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
			cacheA := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
			cacheB := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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

	var cid graphcache.ContribID
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
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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

func snapshotCache(c *graphcache.GraphCache[string, *pb.Vertex], vp int, edges [][2]string) nodeSnapshot {
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
func (s *nodeSnapshot) includeKeys(c *graphcache.GraphCache[string, *pb.Vertex], vs []string, es [][2]string) {
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
// TestApplyMutation_BatchBornExpiredAlignment pins the deliberate behavior
// change of routing MutationOp_PutVertices through the batch HLC method
// (#840): a born-expired item is now dead on arrival — NOTHING is stored
// physically (the singular path stored it until GC, inflating replica
// high-water) — but its HLC watermark IS recorded, so a later strictly-older
// write for the same key still loses LWW and cannot resurrect it. Live items
// in the same mixed batch apply normally, and nil entries are skipped.
func TestApplyMutation_BatchBornExpiredAlignment(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := NewLanternService(cache)
	ctx := context.Background()
	origin := bytes16("origin-A")

	apply := func(t *testing.T, seq uint64, op *pb.MutationOp) {
		t.Helper()
		m := &pb.Mutation{Seq: seq, Hlc: newHLC(int64(seq), origin), Origin: origin[:], Op: op}
		if err := svc.ApplyMutation(ctx, m); err != nil {
			t.Fatalf("ApplyMutation seq=%d: %v", seq, err)
		}
	}

	liveExp := timestamppb.New(time.Now().Add(time.Hour))
	deadExp := timestamppb.New(time.Now().Add(-time.Hour))
	apply(t, 5, &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
		PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
			{Key: "dead", Value: &pb.Vertex_Nil{Nil: true}, Expiration: deadExp},
			nil, // wire nils are skipped, not applied
			{Key: "live", Value: &pb.Vertex_Nil{Nil: true}, Expiration: liveExp},
		}},
	}})

	// The born-expired key is not stored: invisible to reads AND absent from
	// the physical count (no GC tick has run — the singular path would still
	// hold it here).
	if _, ok := cache.GetVertex("dead"); ok {
		t.Fatalf("born-expired vertex is readable")
	}
	if got := cache.VertexCount(); got != 1 {
		t.Fatalf("VertexCount = %d, want 1 (live only; born-expired must not be stored)", got)
	}

	// The watermark WAS recorded: a strictly-older replay for the same key is
	// rejected, so it cannot resurrect the dead key with stale data.
	apply(t, 1, &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
		PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
			{Key: "dead", Value: &pb.Vertex_Nil{Nil: true}, Expiration: liveExp},
		}},
	}})
	if _, ok := cache.GetVertex("dead"); ok {
		t.Fatalf("strictly-older put resurrected the born-expired key — watermark was not recorded")
	}

	// A strictly-newer put for the same key applies normally.
	apply(t, 9, &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
		PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
			{Key: "dead", Value: &pb.Vertex_Nil{Nil: true}, Expiration: liveExp},
		}},
	}})
	if _, ok := cache.GetVertex("dead"); !ok {
		t.Fatalf("strictly-newer put after the born-expired watermark did not apply")
	}
}

func TestApplyMutation_TombstoneClampRejectHook(t *testing.T) {
	exp := timestamppb.New(time.Now().Add(time.Hour))
	origin := bytes16("origin-A")

	newSvc := func(tsCount *int) *LanternService {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
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

// FuzzContribIDFromBytes fuzzes the wire→ContribID decode. Only the canonical
// 24-byte length may yield a non-zero id; every other length must collapse to
// the zero sentinel (the "no identity" marker that makes receivers skip
// dedup). A non-zero decode must round-trip back through contribIDBytes
// unchanged, and the decode must never panic. Guards the dedup-id boundary the
// broad_rw bench comment flags as a silent zero-decode hazard.
func FuzzContribIDFromBytes(f *testing.F) {
	var full graphcache.ContribID
	for i := range full {
		full[i] = byte(i + 1)
	}
	f.Add([]byte(nil))
	f.Add(make([]byte, 24)) // canonical length, all-zero -> zero sentinel
	f.Add(full[:])          // canonical length, non-zero
	f.Add([]byte{1, 2, 3})  // too short
	f.Add(make([]byte, 25)) // too long
	f.Fuzz(func(t *testing.T, b []byte) {
		var zero graphcache.ContribID
		got := contribIDFromBytes(b)
		if len(b) != len(zero) {
			if got != zero {
				t.Fatalf("non-canonical %d-byte input decoded to a non-zero ContribID", len(b))
			}
			return
		}
		// Canonical length: the bytes must be copied verbatim.
		if got != graphcache.ContribID(b) {
			t.Fatalf("24-byte decode altered bytes: got %x, want %x", got, b)
		}
		// Non-zero ids must survive the encode→decode round-trip.
		if got != zero {
			if rt := contribIDFromBytes(contribIDBytes(got)); rt != got {
				t.Fatalf("round-trip mismatch: %x -> %x", got, rt)
			}
		}
	})
}

// TestContribIDForGoldenVectors pins the 24-byte wire layout byte-for-byte:
// [0:16] origin nonce ‖ [16:24] big-endian uint64 (seq<<16)|idx. The SAME
// literals live in sdks/go/client_test.go TestContribIDGoldenVectors and
// sdks/node/test/contrib.test.ts (#922) — a unilateral change to any one
// implementation's shift width, endianness, or nonce length turns exactly this
// suite red while the dedup-behavior tests above stay green, catching the
// coordinated-drift refactor that would silently break cross-SDK idempotency.
func TestContribIDForGoldenVectors(t *testing.T) {
	origin := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	cases := []struct {
		seq  uint64
		idx  uint16
		low8 [8]byte
	}{
		{1, 0, [8]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00}},
		{1, 1, [8]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01}},
		{0xABCD, 0xFFFF, [8]byte{0x00, 0x00, 0x00, 0x00, 0xab, 0xcd, 0xff, 0xff}},
	}
	for _, tc := range cases {
		got := contribIDFor(origin, tc.seq, tc.idx)
		var wantID graphcache.ContribID
		copy(wantID[:16], origin)
		copy(wantID[16:], tc.low8[:])
		if got != wantID {
			t.Fatalf("contribIDFor(origin, %#x, %#x):\n  got  %x\n  want %x", tc.seq, tc.idx, got[:], wantID[:])
		}
	}
}
