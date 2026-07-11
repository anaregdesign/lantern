package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func newTestService(t *testing.T) *LanternService {
	t.Helper()
	return NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute))
}

func futureTs(d time.Duration) *timestamppb.Timestamp {
	return timestamppb.New(time.Now().Add(d))
}

func TestLanternService_PutAndGetVertex(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	v := &pb.Vertex{
		Key:        "k1",
		Value:      &pb.Vertex_String_{String_: "hello"},
		Expiration: futureTs(time.Minute),
	}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	resp, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "k1"})
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got := resp.Vertex.GetString_(); got != "hello" {
		t.Errorf("GetVertex value = %q, want \"hello\"", got)
	}
}

// TestLanternService_PutVertexIfAbsent pins the SET NX surface (#896): the
// singular PutVertex reports written via a bool, the plural PutVertices reports
// the written count plus the skipped keys, and an unconditional put still
// overwrites. Uses the default (non-replicated) service so the clock==nil
// branch is exercised.
func TestLanternService_PutVertexIfAbsent(t *testing.T) {
	ctx := context.Background()

	mkVertex := func(key, val string) *pb.Vertex {
		return &pb.Vertex{
			Key:        key,
			Value:      &pb.Vertex_String_{String_: val},
			Expiration: futureTs(time.Minute),
		}
	}
	valueOf := func(t *testing.T, s *LanternService, key string) string {
		t.Helper()
		r, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: key})
		if err != nil {
			t.Fatalf("GetVertex(%q): %v", key, err)
		}
		return r.Vertex.GetString_()
	}

	t.Run("SingularWritesThenSkips", func(t *testing.T) {
		s := newTestService(t)
		first, err := s.PutVertex(ctx, &pb.PutVertexRequest{Vertex: mkVertex("k", "one"), IfAbsent: true})
		if err != nil {
			t.Fatalf("PutVertex: %v", err)
		}
		if !first.GetWritten() {
			t.Fatal("first if_absent PutVertex Written = false, want true")
		}
		second, err := s.PutVertex(ctx, &pb.PutVertexRequest{Vertex: mkVertex("k", "two"), IfAbsent: true})
		if err != nil {
			t.Fatalf("PutVertex(repeat): %v", err)
		}
		if second.GetWritten() {
			t.Fatal("second if_absent PutVertex Written = true, want false (key already live)")
		}
		if got := valueOf(t, s, "k"); got != "one" {
			t.Errorf("value = %q, want \"one\" (skipped write must not overwrite)", got)
		}
	})

	t.Run("PluralReportsWrittenAndSkipped", func(t *testing.T) {
		s := newTestService(t)
		if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{mkVertex("live", "old")}}); err != nil {
			t.Fatalf("seed PutVertices: %v", err)
		}
		resp, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{mkVertex("fresh", "a"), mkVertex("live", "b")},
			IfAbsent: true,
		})
		if err != nil {
			t.Fatalf("PutVertices(if_absent): %v", err)
		}
		if resp.GetWritten() != 1 {
			t.Errorf("Written = %d, want 1", resp.GetWritten())
		}
		if got := resp.GetSkippedKeys(); len(got) != 1 || got[0] != "live" {
			t.Errorf("SkippedKeys = %v, want [live]", got)
		}
		if got := valueOf(t, s, "live"); got != "old" {
			t.Errorf("live value = %q, want \"old\"", got)
		}
	})

	t.Run("UnconditionalStillOverwrites", func(t *testing.T) {
		s := newTestService(t)
		if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{mkVertex("k", "one")}}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		// if_absent defaults false → ordinary LWW upsert.
		if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{mkVertex("k", "two")}}); err != nil {
			t.Fatalf("overwrite: %v", err)
		}
		if got := valueOf(t, s, "k"); got != "two" {
			t.Errorf("value = %q, want \"two\" (unconditional put must overwrite)", got)
		}
	})

	t.Run("BornExpiredReportsNotWritten", func(t *testing.T) {
		s := newTestService(t)
		dead := &pb.Vertex{
			Key:        "k",
			Value:      &pb.Vertex_String_{String_: "dead"},
			Expiration: timestamppb.New(time.Now().Add(-time.Hour)),
		}
		resp, err := s.PutVertex(ctx, &pb.PutVertexRequest{Vertex: dead, IfAbsent: true})
		if err != nil {
			t.Fatalf("PutVertex: %v", err)
		}
		if resp.GetWritten() {
			t.Fatal("born-expired if_absent PutVertex Written = true, want false")
		}
		if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "k"}); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("GetVertex after born-expired put: code = %v, want NotFound", connect.CodeOf(err))
		}
	})
}

func TestLanternService_GetVertex_NotFound(t *testing.T) {
	s := newTestService(t)
	_, err := s.GetVertex(context.Background(), &pb.GetVertexRequest{Key: "missing"})
	if err == nil {
		t.Fatal("expected error for missing vertex, got nil")
	}
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Errorf("connect code = %v, want CodeNotFound", code)
	}
}

func TestLanternService_DeleteVertex(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	v := &pb.Vertex{Key: "x", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: futureTs(time.Minute)}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	dResp, err := s.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "x"})
	if err != nil {
		t.Fatalf("DeleteVertex: %v", err)
	}
	if !dResp.GetExisted() {
		t.Errorf("DeleteVertex.Existed = false, want true (vertex was present)")
	}
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "x"}); err == nil {
		t.Error("expected NotFound after delete, got nil error")
	}
	// Second delete of the same key must report existed=false.
	dResp2, err := s.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "x"})
	if err != nil {
		t.Fatalf("DeleteVertex(repeat): %v", err)
	}
	if dResp2.GetExisted() {
		t.Errorf("DeleteVertex.Existed = true on second call, want false")
	}
}

func TestLanternService_AddAndGetEdge(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	edge := &pb.Edge{Tail: "a", Head: "b", Weight: 1.25, Expiration: futureTs(time.Minute)}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{edge}}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 1.25 {
		t.Errorf("Weight = %v, want 1.25", resp.Edge.Weight)
	}
}

func TestLanternService_GetEdge_NotFound(t *testing.T) {
	s := newTestService(t)
	_, err := s.GetEdge(context.Background(), &pb.GetEdgeRequest{Tail: "no", Head: "such"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Errorf("code = %v, want CodeNotFound", code)
	}
}

func TestLanternService_AddEdge_Additive(t *testing.T) {
	// AddEdge is additive: two adds of weight 2 each should accumulate to 4.
	// Each AddEdges response also reports the post-accumulation effective
	// weight (#897), index-aligned with the request edges.
	s := newTestService(t)
	ctx := context.Background()
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 2, Expiration: futureTs(time.Minute)}
	resp1, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if got := resp1.GetEffectiveWeights(); len(got) != 1 || got[0] != 2 {
		t.Errorf("effective_weights after first add = %v, want [2]", got)
	}
	resp2, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}})
	if err != nil {
		t.Fatalf("AddEdge2: %v", err)
	}
	if got := resp2.GetEffectiveWeights(); len(got) != 1 || got[0] != 4 {
		t.Errorf("effective_weights after second add = %v, want [4]", got)
	}
	// Singular AddEdge surfaces the same running total from plural[0] (#897).
	single, err := s.AddEdge(ctx, &pb.AddEdgeRequest{Edge: e})
	if err != nil {
		t.Fatalf("AddEdge single: %v", err)
	}
	if single.GetEffectiveWeight() != 6 {
		t.Errorf("singular effective_weight = %v, want 6", single.GetEffectiveWeight())
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 6 {
		t.Errorf("Weight = %v, want 6 (additive)", resp.Edge.Weight)
	}
}

// TestLanternService_AddEdges_ContribIDWiring asserts the #588 plumbing:
// AddEdges maps the optional, index-aligned contrib_ids onto
// EdgeItem.ContribID (short or empty slots stay zero), AddEdge forwards its
// singular contrib_id into contrib_ids[0], and the backend's reported dedup
// count is surfaced via HotPathMetrics.OnEdgeContribDeduped. Dedup
// convergence itself is covered by the core tests and tests/integration.
func TestLanternService_AddEdges_ContribIDWiring(t *testing.T) {
	var id0, id1 graphcache.ContribID
	id0[0], id0[23] = 0x11, 0x22
	id1[0], id1[23] = 0x33, 0x44

	t.Run("AddEdges maps index-aligned contrib_ids", func(t *testing.T) {
		fb := newFakeBackend()
		fm := &fakeHotPathMetrics{}
		fb.dedupReturn = 2
		s := NewLanternService(fb).WithHotPathMetrics(fm)

		_, err := s.AddEdges(context.Background(), &pb.AddEdgesRequest{
			Edges: []*pb.Edge{
				{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)},
				{Tail: "a", Head: "c", Weight: 1, Expiration: futureTs(time.Minute)},
				{Tail: "a", Head: "d", Weight: 1, Expiration: futureTs(time.Minute)},
			},
			// id0 for edge 0; empty slot for edge 1; edge 2 has no slot.
			ContribIds: [][]byte{id0[:], {}},
		})
		if err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		if fb.addEdgesContribCalls != 1 {
			t.Fatalf("AddEdgesWithExpirationContrib calls = %d, want 1", fb.addEdgesContribCalls)
		}
		if len(fb.lastAddEdgesItems) != 3 {
			t.Fatalf("captured items = %d, want 3", len(fb.lastAddEdgesItems))
		}
		if got := fb.lastAddEdgesItems[0].ContribID; got != id0 {
			t.Errorf("item[0].ContribID = %x, want %x", got, id0)
		}
		if got := fb.lastAddEdgesItems[1].ContribID; !got.IsZero() {
			t.Errorf("item[1].ContribID = %x, want zero (empty slot)", got)
		}
		if got := fb.lastAddEdgesItems[2].ContribID; !got.IsZero() {
			t.Errorf("item[2].ContribID = %x, want zero (missing slot)", got)
		}
		if len(fm.contribDed) != 1 || fm.contribDed[0] != 2 {
			t.Errorf("OnEdgeContribDeduped = %v, want [2]", fm.contribDed)
		}
	})

	t.Run("legacy AddEdges without contrib_ids stays zero", func(t *testing.T) {
		fb := newFakeBackend()
		fm := &fakeHotPathMetrics{}
		s := NewLanternService(fb).WithHotPathMetrics(fm)
		if _, err := s.AddEdges(context.Background(), &pb.AddEdgesRequest{
			Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)}},
		}); err != nil {
			t.Fatalf("AddEdges: %v", err)
		}
		if got := fb.lastAddEdgesItems[0].ContribID; !got.IsZero() {
			t.Errorf("item[0].ContribID = %x, want zero (no contrib_ids on wire)", got)
		}
		// The service calls OnEdgeContribDeduped unconditionally; with the
		// fake's default dedupReturn of 0 it fires once with 0.
		if len(fm.contribDed) != 1 || fm.contribDed[0] != 0 {
			t.Errorf("OnEdgeContribDeduped = %v, want [0]", fm.contribDed)
		}
	})

	t.Run("AddEdge forwards contrib_id into contrib_ids[0]", func(t *testing.T) {
		fb := newFakeBackend()
		s := NewLanternService(fb)
		if _, err := s.AddEdge(context.Background(), &pb.AddEdgeRequest{
			Edge:      &pb.Edge{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)},
			ContribId: id1[:],
		}); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if len(fb.lastAddEdgesItems) != 1 {
			t.Fatalf("captured items = %d, want 1", len(fb.lastAddEdgesItems))
		}
		if got := fb.lastAddEdgesItems[0].ContribID; got != id1 {
			t.Errorf("forwarded ContribID = %x, want %x", got, id1)
		}
	})

	t.Run("AddEdge without contrib_id stays zero", func(t *testing.T) {
		fb := newFakeBackend()
		s := NewLanternService(fb)
		if _, err := s.AddEdge(context.Background(), &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)},
		}); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if got := fb.lastAddEdgesItems[0].ContribID; !got.IsZero() {
			t.Errorf("ContribID = %x, want zero (no contrib_id on wire)", got)
		}
	})
}

// TestLanternService_LocalAddEdges_StampsSnapshotContribID is the #733
// regression. A locally-originated AddEdges that carries no wire
// contrib_ids (the ghz bench, and any client predating the optional #588
// plumbing) must stamp the SAME synthesized (origin, seq, idx) ContribID
// into the backend that a peer derives in ApplyMutation, so a re-pulled
// snapshot frame is a G-Set no-op (docs/replication.md §4/§6) instead of
// doubling edge weight without bound. Before the fix the origin stored a
// zero ContribID, Snapshot emitted it verbatim, and a gapped follower
// re-applying the snapshot double-counted every edge on each reconnect.
func TestLanternService_LocalAddEdges_StampsSnapshotContribID(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 128, SubscriberBuffer: 128})
	t.Cleanup(func() { _ = log.Close() })
	origin := bytes16("origin-A")
	clock := hlc.New(origin, hlc.Options{})
	s := NewLanternService(cache).WithReplication(log, clock, nil)
	ctx := context.Background()

	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{
		Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 2, Expiration: futureTs(time.Minute)}},
		// no contrib_ids on the wire (ghz bench / optional #588 path)
	}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	// The single logged mutation commits under seq 1 on a fresh log, so the
	// origin's own graphcache must carry contribIDFor(origin, 1, 0): the
	// exact id ApplyMutation synthesizes for the same frame on a peer.
	want := contribIDFor(origin[:], 1, 0)
	edges := cache.SnapshotEdges()
	if len(edges) != 1 {
		t.Fatalf("snapshot edges = %d, want 1", len(edges))
	}
	contribs := edges[0].Contributions
	if len(contribs) != 1 {
		t.Fatalf("snapshot contributions = %d, want 1", len(contribs))
	}
	if got := contribs[0].ContribID; got.IsZero() || got != want {
		t.Fatalf("snapshot contribID = %x, want %x (synthesized, non-zero)", got, want)
	}

	// Re-applying that snapshot contribution — the pump / anti-entropy
	// bootstrap path — must dedup on the matching non-zero ContribID, so
	// AddEdgeWithExpirationContribHLC reports applied=false and the stored
	// weight stays at 2 instead of doubling (the exact #733 leak).
	re := edges[0]
	c := contribs[0]
	if cache.AddEdgeWithExpirationContribHLC(re.Tail, re.Head, c.Weight, c.Expiration, c.ContribID, re.HLC) {
		t.Errorf("re-apply of snapshot contribution was additive; want dedup no-op (#733)")
	}
	if w, ok := cache.GetWeight("a", "b"); !ok || w != 2 {
		t.Fatalf("weight after snapshot re-apply = %v ok=%v, want 2 true (dedup, not double)", w, ok)
	}
}

func TestLanternService_PutEdge_Replaces(t *testing.T) {
	// PutEdge deletes-then-adds: repeated calls with the same weight stay at that weight.
	s := newTestService(t)
	ctx := context.Background()
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 7, Expiration: futureTs(time.Minute)}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("PutEdge2: %v", err)
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 7 {
		t.Errorf("Weight = %v, want 7 (replaced, not accumulated)", resp.Edge.Weight)
	}
}

func TestLanternService_DeleteEdge(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	dResp, err := s.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if !dResp.GetExisted() {
		t.Errorf("DeleteEdge.Existed = false, want true (edge was present)")
	}
	if _, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err == nil {
		t.Error("expected NotFound after delete")
	}
	// Second delete reports existed=false.
	dResp2, err := s.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("DeleteEdge(repeat): %v", err)
	}
	if dResp2.GetExisted() {
		t.Errorf("DeleteEdge.Existed = true on second call, want false")
	}
}

func seedTriangle(t *testing.T, s *LanternService) {
	t.Helper()
	ctx := context.Background()
	exp := futureTs(time.Minute)
	verts := []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_String_{String_: "A"}, Expiration: exp},
		{Key: "b", Value: &pb.Vertex_String_{String_: "B"}, Expiration: exp},
		{Key: "c", Value: &pb.Vertex_String_{String_: "C"}, Expiration: exp},
	}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: verts}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	edges := []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: exp},
		{Tail: "b", Head: "a", Weight: 1, Expiration: exp},
		{Tail: "b", Head: "c", Weight: 2, Expiration: exp},
		{Tail: "c", Head: "b", Weight: 2, Expiration: exp},
		{Tail: "a", Head: "c", Weight: 10, Expiration: exp},
		{Tail: "c", Head: "a", Weight: 10, Expiration: exp},
	}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: edges}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
}

func TestLanternService_Illuminate_NoAlgorithm(t *testing.T) {
	s := newTestService(t)
	seedTriangle(t, s)

	resp, err := s.Illuminate(context.Background(), &pb.IlluminateRequest{
		Seed:   "a",
		Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 3, FanOut: 10}},
	})
	if err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	if len(resp.Graph.Vertices) != 3 {
		t.Errorf("vertices = %d, want 3", len(resp.Graph.Vertices))
	}
}

// TestLanternService_Illuminate_AllAxisCombos exercises every params-oneof
// arm (#846) against every shared axis: the BFS family across reduction ×
// objective × weighting, and the PPR family across weighting. Every tuple
// must run to completion and return at least one vertex against the
// triangle seed. The explicit raw-BFS baseline is covered separately by the
// _NoAlgorithm test above.
func TestLanternService_Illuminate_AllAxisCombos(t *testing.T) {
	reductions := []pb.Reduction{
		pb.Reduction_REDUCTION_MINIMUM_SPANNING_TREE,
		pb.Reduction_REDUCTION_SHORTEST_PATH_TREE,
	}
	objectives := []pb.Objective{
		pb.Objective_OBJECTIVE_MINIMIZE,
		pb.Objective_OBJECTIVE_MAXIMIZE,
	}
	weightings := []pb.Weighting{
		pb.Weighting_WEIGHTING_RAW,
		pb.Weighting_WEIGHTING_TFIDF,
		pb.Weighting_WEIGHTING_BM25,
	}
	// The oneof wrapper interface is unexported in pb, so each arm carries a
	// request factory instead of the params value itself.
	type arm struct {
		name string
		req  func(w pb.Weighting) *pb.IlluminateRequest
	}
	var arms []arm
	for _, red := range reductions {
		for _, obj := range objectives {
			red, obj := red, obj
			arms = append(arms, arm{
				name: red.String() + "/" + obj.String(),
				req: func(w pb.Weighting) *pb.IlluminateRequest {
					return &pb.IlluminateRequest{
						Seed:      "a",
						Weighting: w,
						Params:    &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 3, FanOut: 10, Objective: obj, Reduction: red}},
					}
				},
			})
		}
	}
	arms = append(arms, arm{
		name: "PPR",
		req: func(w pb.Weighting) *pb.IlluminateRequest {
			return &pb.IlluminateRequest{
				Seed:      "a",
				Weighting: w,
				Params:    &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 10}},
			}
		},
	})
	for _, a := range arms {
		for _, w := range weightings {
			name := a.name + "/" + w.String()
			t.Run(name, func(t *testing.T) {
				s := newTestService(t)
				seedTriangle(t, s)
				resp, err := s.Illuminate(context.Background(), a.req(w))
				if err != nil {
					t.Fatalf("Illuminate(%s): %v", name, err)
				}
				if len(resp.Graph.Vertices) == 0 {
					t.Errorf("Illuminate(%s) returned empty vertices", name)
				}
			})
		}
	}
}

func TestLanternService_GetEdge_ReturnsExpiration(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	exp := futureTs(2 * time.Minute)
	e := &pb.Edge{Tail: "a", Head: "b", Weight: 1, Expiration: exp}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{e}}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	resp, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Expiration == nil {
		t.Fatal("Expiration is nil; expected propagated TTL")
	}
	got := resp.Edge.Expiration.AsTime()
	want := exp.AsTime()
	if delta := got.Sub(want); delta > time.Second || delta < -time.Second {
		t.Errorf("Expiration = %v, want ~%v (delta=%v)", got, want, delta)
	}
}

func TestLanternService_WriteResponses_ReportCounts(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	exp := futureTs(time.Minute)

	vresp, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: exp},
		{Key: "b", Value: &pb.Vertex_Int64{Int64: 2}, Expiration: exp},
	}})
	if err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	if vresp.Written != 2 {
		t.Errorf("PutVertex Written = %d, want 2", vresp.Written)
	}

	aresp, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: exp},
	}})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if aresp.Written != 1 {
		t.Errorf("AddEdge Written = %d, want 1", aresp.Written)
	}

	presp, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 3, Expiration: exp},
	}})
	if err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if presp.Written != 1 {
		t.Errorf("PutEdge Written = %d, want 1", presp.Written)
	}
}

func TestLanternService_RespectsContextCancel(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "x"}); err == nil {
		t.Fatal("expected ctx cancel error, got nil")
	} else if code := connect.CodeOf(err); code != connect.CodeCanceled {
		t.Errorf("code = %v, want CodeCanceled", code)
	}
}

// TestLanternService_SingularWriteFacades verifies that PutVertex / AddEdge /
// PutEdge behave like single-element calls to their plural counterparts.
func TestLanternService_SingularWriteFacades(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	exp := futureTs(time.Minute)

	// PutVertex writes a single vertex visible via GetVertex.
	if _, err := s.PutVertex(ctx, &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: "k", Value: &pb.Vertex_String_{String_: "v"}, Expiration: exp},
	}); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	if gv, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "k"}); err != nil {
		t.Fatalf("GetVertex: %v", err)
	} else if got := gv.GetVertex().GetString_(); got != "v" {
		t.Errorf("vertex value = %q, want %q", got, "v")
	}

	// AddEdge accumulates weight just like AddEdges.
	for range 2 {
		if _, err := s.AddEdge(ctx, &pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 1.5, Expiration: exp},
		}); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if ge, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatalf("GetEdge: %v", err)
	} else if ge.GetEdge().GetWeight() != 3 {
		t.Errorf("weight = %v, want 3 (additive)", ge.GetEdge().GetWeight())
	}

	// PutEdge replaces weight (idempotent) instead of accumulating.
	if _, err := s.PutEdge(ctx, &pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 9, Expiration: exp},
	}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if _, err := s.PutEdge(ctx, &pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "a", Head: "b", Weight: 9, Expiration: exp},
	}); err != nil {
		t.Fatalf("PutEdge(repeat): %v", err)
	}
	if ge, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatalf("GetEdge: %v", err)
	} else if ge.GetEdge().GetWeight() != 9 {
		t.Errorf("weight = %v, want 9 (replaced)", ge.GetEdge().GetWeight())
	}
}

// When WithTombstoneTTL is set, RPCs that accept a per-entry Expiration
// must reject values that exceed the clamp with codes.InvalidArgument and
// an error message that names LANTERN_TOMBSTONE_TTL (so operators can
// trace it back to the env knob).
func TestExpirationClamp_RejectsBeyondTTL(t *testing.T) {
	const ttl = time.Hour
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl)
	ctx := context.Background()

	tooFar := timestamppb.New(time.Now().Add(2 * ttl))

	cases := []struct {
		name string
		call func() error
	}{
		{"PutVertices", func() error {
			_, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
				Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: tooFar}},
			})
			return err
		}},
		{"AddEdges", func() error {
			_, err := s.AddEdges(ctx, &pb.AddEdgesRequest{
				Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: tooFar}},
			})
			return err
		}},
		{"PutEdges", func() error {
			_, err := s.PutEdges(ctx, &pb.PutEdgesRequest{
				Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: tooFar}},
			})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
				t.Fatalf("code: got %v, want CodeInvalidArgument", code)
			}
			if !strings.Contains(err.Error(), "LANTERN_TOMBSTONE_TTL") {
				t.Errorf("message should reference LANTERN_TOMBSTONE_TTL; got %q", err.Error())
			}
		})
	}
}

// Within the clamp, the same RPCs succeed.
func TestExpirationClamp_AcceptsWithinTTL(t *testing.T) {
	const ttl = time.Hour
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl)
	ctx := context.Background()

	ok := timestamppb.New(time.Now().Add(ttl / 2))

	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: ok}},
	}); err != nil {
		t.Fatalf("PutVertices within TTL: %v", err)
	}
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{
		Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 1, Expiration: ok}},
	}); err != nil {
		t.Fatalf("AddEdges within TTL: %v", err)
	}
}

// Zero expiration (= no expiration) is always accepted regardless of TTL.
func TestExpirationClamp_ZeroAlwaysAllowed(t *testing.T) {
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(time.Minute)
	ctx := context.Background()
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}}},
	}); err != nil {
		t.Fatalf("PutVertices with zero expiration: %v", err)
	}
}

func TestLanternService_FakeBackend_PutGetDelete(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	ctx := context.Background()

	v := &pb.Vertex{Key: "a", Value: &pb.Vertex_String_{String_: "alpha"}}
	if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{v}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	if fb.putVerticesCalls != 1 {
		t.Errorf("putVerticesCalls = %d, want 1", fb.putVerticesCalls)
	}

	resp, err := svc.GetVertex(ctx, &pb.GetVertexRequest{Key: "a"})
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got := resp.Vertex.GetString_(); got != "alpha" {
		t.Errorf("value = %q, want \"alpha\"", got)
	}

	if _, err := svc.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{"a"}}); err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if fb.deleteVertices != 1 {
		t.Errorf("deleteVertices = %d, want 1", fb.deleteVertices)
	}
}

func TestLanternService_FakeBackend_Illuminate_PropagatesError(t *testing.T) {
	fb := newFakeBackend()
	fb.neighborErr = errors.New("simulated cache failure")
	svc := NewLanternService(fb)

	_, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{Seed: "a", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// A bare non-context error from the backend passes through to the
	// service handler unwrapped; connect.CodeOf reports CodeUnknown.
	if code := connect.CodeOf(err); code != connect.CodeUnknown {
		t.Errorf("connect code = %v, want CodeUnknown", code)
	}
}

// TestLanternService_FakeBackend_Illuminate_ObjectiveSteersPruning pins the
// #560 contract at the service boundary: the Illuminate handler must translate
// the request Objective into the per-hop pruning direction it hands the
// backend. MINIMIZE asks for the cheapest edges (selectSmallest=true); MAXIMIZE
// and the UNSPECIFIED default both ask for the strongest (selectSmallest=false),
// preserving the historical strongest-neighbour behaviour.
func TestLanternService_FakeBackend_Illuminate_ObjectiveSteersPruning(t *testing.T) {
	tests := []struct {
		name            string
		objective       pb.Objective
		wantSelectSmall bool
	}{
		{
			name:            "MINIMIZE prunes to smallest",
			objective:       pb.Objective_OBJECTIVE_MINIMIZE,
			wantSelectSmall: true,
		},
		{
			name:            "MAXIMIZE prunes to largest",
			objective:       pb.Objective_OBJECTIVE_MAXIMIZE,
			wantSelectSmall: false,
		},
		{
			name:            "UNSPECIFIED defaults to largest (#560)",
			objective:       pb.Objective_OBJECTIVE_UNSPECIFIED,
			wantSelectSmall: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fb := newFakeBackend()
			svc := NewLanternService(fb)
			if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
				Seed:   "a",
				Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 3, Objective: tt.objective}},
			}); err != nil {
				t.Fatalf("Illuminate: %v", err)
			}
			if fb.neighborCalls != 1 {
				t.Fatalf("neighborCalls = %d, want 1", fb.neighborCalls)
			}
			if fb.lastNeighborSelectSmall != tt.wantSelectSmall {
				t.Errorf("lastNeighborSelectSmall = %v, want %v",
					fb.lastNeighborSelectSmall, tt.wantSelectSmall)
			}
		})
	}
}

// TestLanternService_FakeBackend_Illuminate_VertexPrefixBuildsKeep pins the
// #602 contract at the service boundary: the Illuminate handler must translate
// a non-empty vertex_prefix into a HasPrefix keep predicate it hands the
// backend, and leave keep nil when the prefix is empty (an unfiltered walk).
// The concrete string predicate lives in the server (core stays generic), so
// this is the only seam that can prove the closure shape.
func TestLanternService_FakeBackend_Illuminate_VertexPrefixBuildsKeep(t *testing.T) {
	t.Run("empty prefix leaves keep nil", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed: "a", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 3}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.neighborCalls != 1 {
			t.Fatalf("neighborCalls = %d, want 1", fb.neighborCalls)
		}
		if fb.lastNeighborKeep != nil {
			t.Error("lastNeighborKeep = non-nil, want nil for empty vertex_prefix")
		}
	})

	t.Run("non-empty prefix builds HasPrefix keep", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed: "users/1", VertexPrefix: "users/",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 3}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.neighborCalls != 1 {
			t.Fatalf("neighborCalls = %d, want 1", fb.neighborCalls)
		}
		keep := fb.lastNeighborKeep
		if keep == nil {
			t.Fatal("lastNeighborKeep = nil, want non-nil for vertex_prefix=\"users/\"")
		}
		if !keep("users/42") {
			t.Error(`keep("users/42") = false, want true`)
		}
		if keep("orgs/42") {
			t.Error(`keep("orgs/42") = true, want false`)
		}
	})
}

// TestLanternService_FakeBackend_Illuminate_PPRRouting pins the #801 contract
// at the service boundary: algorithm=ppr must route to the forward-push path
// (PersonalizedPageRankContext), NOT the BFS+reduction path; the handler
// resolves restart_prob/epsilon to the core defaults when unset or out of
// range, passes explicit in-range values through, forwards k as the top-N cap,
// threads weighting + the vertex_prefix keep predicate, and maps the returned
// relevance star onto response edges.
func TestLanternService_FakeBackend_Illuminate_PPRRouting(t *testing.T) {
	newSeeded := func() *fakeBackend {
		fb := newFakeBackend()
		fb.vertices["s"] = &pb.Vertex{Key: "s", Value: &pb.Vertex_String_{String_: "S"}}
		fb.vertices["users/1"] = &pb.Vertex{Key: "users/1", Value: &pb.Vertex_String_{String_: "U1"}}
		fb.vertices["orgs/9"] = &pb.Vertex{Key: "orgs/9", Value: &pb.Vertex_String_{String_: "O9"}}
		fb.edges["s"] = map[string]float32{"users/1": 0.7, "orgs/9": 0.3}
		return fb
	}

	t.Run("routes to forward-push, not BFS", func(t *testing.T) {
		fb := newSeeded()
		svc := NewLanternService(fb)
		resp, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed:   "s",
			Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 5}},
		})
		if err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.pprCalls != 1 {
			t.Errorf("pprCalls = %d, want 1", fb.pprCalls)
		}
		if fb.neighborCalls != 0 {
			t.Errorf("neighborCalls = %d, want 0 (ppr must not take the BFS path)", fb.neighborCalls)
		}
		if fb.lastPPRTopN != 5 {
			t.Errorf("lastPPRTopN = %d, want 5 (k forwarded as top-N)", fb.lastPPRTopN)
		}
		// The synthesised relevance star must surface as response edges.
		var got float32
		var found bool
		for _, e := range resp.Graph.Edges {
			if e.Tail == "s" && e.Head == "users/1" {
				got, found = e.Weight, true
			}
		}
		if !found {
			t.Fatalf("response missing star edge s->users/1: %+v", resp.Graph.Edges)
		}
		if got != 0.7 {
			t.Errorf("star edge s->users/1 weight = %v, want 0.7", got)
		}
	})

	t.Run("unset restart_prob/epsilon resolve to core defaults", func(t *testing.T) {
		fb := newSeeded()
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed:   "s",
			Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.lastPPRAlpha != graphcache.DefaultPPRAlpha {
			t.Errorf("lastPPRAlpha = %v, want default %v", fb.lastPPRAlpha, graphcache.DefaultPPRAlpha)
		}
		if fb.lastPPREpsilon != graphcache.DefaultPPREpsilon {
			t.Errorf("lastPPREpsilon = %v, want default %v", fb.lastPPREpsilon, graphcache.DefaultPPREpsilon)
		}
	})

	t.Run("in-range restart_prob/epsilon pass through", func(t *testing.T) {
		fb := newSeeded()
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed:   "s",
			Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{RestartProb: 0.3, Epsilon: 1e-3}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if math.Abs(fb.lastPPRAlpha-0.3) > 1e-6 {
			t.Errorf("lastPPRAlpha = %v, want 0.3", fb.lastPPRAlpha)
		}
		if math.Abs(fb.lastPPREpsilon-1e-3) > 1e-9 {
			t.Errorf("lastPPREpsilon = %v, want 1e-3", fb.lastPPREpsilon)
		}
	})

	t.Run("out-of-range restart_prob falls back to default", func(t *testing.T) {
		for _, rp := range []float32{0, 1, 1.5, -0.2, float32(math.NaN())} {
			fb := newSeeded()
			svc := NewLanternService(fb)
			if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
				Seed:   "s",
				Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{RestartProb: rp}},
			}); err != nil {
				t.Fatalf("Illuminate(restart_prob=%v): %v", rp, err)
			}
			if fb.lastPPRAlpha != graphcache.DefaultPPRAlpha {
				t.Errorf("restart_prob=%v: lastPPRAlpha = %v, want default %v", rp, fb.lastPPRAlpha, graphcache.DefaultPPRAlpha)
			}
		}
	})

	t.Run("threads weighting and vertex_prefix keep", func(t *testing.T) {
		fb := newSeeded()
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed:         "s",
			Weighting:    pb.Weighting_WEIGHTING_BM25,
			VertexPrefix: "users/",
			Params:       &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.lastPPRWeighting != graphcache.WeightingBM25 {
			t.Errorf("lastPPRWeighting = %v, want WeightingBM25", fb.lastPPRWeighting)
		}
		keep := fb.lastPPRKeep
		if keep == nil {
			t.Fatal("lastPPRKeep = nil, want non-nil for vertex_prefix=\"users/\"")
		}
		if !keep("users/42") || keep("orgs/9") {
			t.Errorf(`keep mis-scoped: keep("users/42")=%v keep("orgs/9")=%v`, keep("users/42"), keep("orgs/9"))
		}
	})

	t.Run("zero top_n resolves to the operator cap and forwards the work budget", func(t *testing.T) {
		fb := newSeeded()
		budget := graphcache.PPRWorkBudget{MaxPushes: 7, MaxTouchedEdges: 11}
		svc := NewLanternService(fb).WithTraversalLimits(TraversalLimits{WorkBudget: budget, MaxResults: 1})
		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed: "s", Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.lastPPRTopN != 1 {
			t.Errorf("top_n=0 forwarded as %d, want configured cap 1", fb.lastPPRTopN)
		}
		if fb.lastPPRBudget != budget {
			t.Errorf("PPR budget = %+v, want %+v", fb.lastPPRBudget, budget)
		}
	})

	t.Run("explicit top_n above the operator cap is rejected", func(t *testing.T) {
		fb := newSeeded()
		svc := NewLanternService(fb).WithTraversalLimits(TraversalLimits{MaxResults: 1})
		_, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed: "s", Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 2}},
		})
		if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
			t.Fatalf("Illuminate code = %v, want InvalidArgument (err=%v)", got, err)
		}
	})

	t.Run("propagates backend error", func(t *testing.T) {
		fb := newSeeded()
		fb.pprErr = errors.New("simulated ppr failure")
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed:   "s",
			Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}},
		}); err == nil {
			t.Fatal("expected error from PPR backend, got nil")
		}
	})
}

// seedStar builds a directed star: seed "s" fans out to h1..h5 with distinct
// weights 1..5 (and nothing else), so a per-hop prune at k < 5 must choose
// which heads survive. Used by the #560 min-vs-max-differ end-to-end test.
func seedStar(t *testing.T, s *LanternService) {
	t.Helper()
	ctx := context.Background()
	exp := futureTs(time.Minute)
	verts := []*pb.Vertex{
		{Key: "s", Value: &pb.Vertex_String_{String_: "S"}, Expiration: exp},
		{Key: "h1", Value: &pb.Vertex_String_{String_: "H1"}, Expiration: exp},
		{Key: "h2", Value: &pb.Vertex_String_{String_: "H2"}, Expiration: exp},
		{Key: "h3", Value: &pb.Vertex_String_{String_: "H3"}, Expiration: exp},
		{Key: "h4", Value: &pb.Vertex_String_{String_: "H4"}, Expiration: exp},
		{Key: "h5", Value: &pb.Vertex_String_{String_: "H5"}, Expiration: exp},
	}
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: verts}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	edges := []*pb.Edge{
		{Tail: "s", Head: "h1", Weight: 1, Expiration: exp},
		{Tail: "s", Head: "h2", Weight: 2, Expiration: exp},
		{Tail: "s", Head: "h3", Weight: 3, Expiration: exp},
		{Tail: "s", Head: "h4", Weight: 4, Expiration: exp},
		{Tail: "s", Head: "h5", Weight: 5, Expiration: exp},
	}
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: edges}); err != nil {
		t.Fatalf("PutEdges: %v", err)
	}
}

// TestLanternService_Illuminate_ObjectiveSelectsPrunedNeighbors is the #560
// end-to-end regression through the real GraphCache: against a star where k
// binds (out-degree 5, k=2), the surviving per-hop neighbours must differ by
// Objective. MAXIMIZE (and the UNSPECIFIED default) keep the two STRONGEST
// heads; MINIMIZE keeps the two weakest. Before #560 the prune always kept the
// strongest two regardless of Objective, so the MINIMIZE row would have
// (wrongly) returned {h4, h5}.
func TestLanternService_Illuminate_ObjectiveSelectsPrunedNeighbors(t *testing.T) {
	tests := []struct {
		name      string
		objective pb.Objective
		wantHeads []string
	}{
		{
			name:      "MAXIMIZE keeps the strongest k",
			objective: pb.Objective_OBJECTIVE_MAXIMIZE,
			wantHeads: []string{"h4", "h5"},
		},
		{
			name:      "UNSPECIFIED defaults to strongest k (#560)",
			objective: pb.Objective_OBJECTIVE_UNSPECIFIED,
			wantHeads: []string{"h4", "h5"},
		},
		{
			name:      "MINIMIZE keeps the weakest k",
			objective: pb.Objective_OBJECTIVE_MINIMIZE,
			wantHeads: []string{"h1", "h2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(t)
			seedStar(t, s)
			resp, err := s.Illuminate(context.Background(), &pb.IlluminateRequest{
				Seed:   "s",
				Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 2, Objective: tt.objective}},
			})
			if err != nil {
				t.Fatalf("Illuminate: %v", err)
			}
			got := map[string]struct{}{}
			for _, v := range resp.Graph.Vertices {
				got[v.GetKey()] = struct{}{}
			}
			want := append([]string{"s"}, tt.wantHeads...)
			if len(got) != len(want) {
				t.Fatalf("vertex count = %d (%v), want %d (%v)", len(got), got, len(want), want)
			}
			for _, k := range want {
				if _, ok := got[k]; !ok {
					t.Errorf("missing vertex %q; got %v, want %v", k, got, want)
				}
			}
		})
	}
}

func TestLanternService_FakeBackend_PutAndDeleteEdge(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	ctx := context.Background()

	if _, err := svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: "h", Weight: 2.5}}); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	if fb.putEdgesCalls != 1 {
		t.Errorf("putEdgesCalls = %d, want 1", fb.putEdgesCalls)
	}

	resp, err := svc.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "t", Head: "h"})
	if err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if resp.Edge.Weight != 2.5 {
		t.Errorf("weight = %v, want 2.5", resp.Edge.Weight)
	}

	del, err := svc.DeleteEdge(ctx, &pb.DeleteEdgeRequest{Tail: "t", Head: "h"})
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if !del.Existed {
		t.Error("Existed = false, want true")
	}
}

// TestLanternService_MutationLog_BurstAppendsMonotone exercises the wire-in
// from issue #179: a burst of N plural-write RPCs must produce N entries on
// the in-memory mutation log with strictly monotonic Seq starting at 1, and
// the onAppend metric callback must fire exactly once per append.
func TestLanternService_MutationLog_BurstAppendsMonotone(t *testing.T) {
	const N = 100

	log := mutationlog.New(mutationlog.Options{Capacity: 2 * N, SubscriberBuffer: 2 * N})
	t.Cleanup(func() { _ = log.Close() })

	clock := hlc.New(hlc.NodeID{0x11, 0x22, 0x33, 0x44}, hlc.Options{})

	var appendCount int
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithReplication(log, clock, func() { appendCount++ })

	ch, cancel, err := log.Subscribe(0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = cancel() })

	for i := 0; i < N; i++ {
		_, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{{Key: keyFor(i)}},
		})
		if err != nil {
			t.Fatalf("PutVertices[%d] error: %v", i, err)
		}
	}

	if appendCount != N {
		t.Fatalf("onAppend count = %d, want %d", appendCount, N)
	}
	first, ok1 := log.FirstSeq()
	last, ok2 := log.LastSeq()
	if !ok1 || !ok2 {
		t.Fatalf("log empty after %d appends (first ok=%v, last ok=%v)", N, ok1, ok2)
	}
	if got := last - first + 1; int(got) != N {
		t.Fatalf("log length = %d, want %d (first=%d last=%d)", got, N, first, last)
	}

	prev := uint64(0)
	for i := 0; i < N; i++ {
		select {
		case e := <-ch:
			if e.Seq != prev+1 {
				t.Fatalf("entry[%d] seq = %d, want %d", i, e.Seq, prev+1)
			}
			prev = e.Seq
			mu, ok := e.Op.(*pb.Mutation)
			if !ok {
				t.Fatalf("entry[%d] op type = %T, want *pb.Mutation", i, e.Op)
			}
			if mu.GetOp().GetPutVertices() == nil {
				t.Fatalf("entry[%d] missing PutVertices oneof", i)
			}
			if len(mu.GetOrigin()) != len(hlc.NodeID{}) {
				t.Fatalf("entry[%d] origin len = %d, want %d",
					i, len(mu.GetOrigin()), len(hlc.NodeID{}))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for entry %d", i)
		}
	}
}

// TestLanternService_MutationLog_NotWired_NoOp guards the test path where
// the service is built without WithReplication: write RPCs must still
// succeed and not panic on the nil log/clock.
func TestLanternService_MutationLog_NotWired_NoOp(t *testing.T) {
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute))
	if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "k"}},
	}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
}

// TestLanternService_PutVertices_BornExpiredNotReplicated pins #698: a
// born-expired vertex (expiration already in the past) is dead on arrival —
// the cache does not store it, and it must not be appended to the mutation
// log either, so peers never apply (store + index + watermark) data that is
// already gone. A mixed batch logs only its live subset; an all-live batch is
// forwarded unchanged.
func TestLanternService_PutVertices_BornExpiredNotReplicated(t *testing.T) {
	newSvc := func(t *testing.T) (*LanternService, *mutationlog.Log, *int) {
		t.Helper()
		log := mutationlog.New(mutationlog.Options{Capacity: 64, SubscriberBuffer: 64})
		t.Cleanup(func() { _ = log.Close() })
		clock := hlc.New(hlc.NodeID{0x01}, hlc.Options{})
		appendCount := 0
		s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
			WithReplication(log, clock, func() { appendCount++ })
		return s, log, &appendCount
	}
	past := timestamppb.New(time.Now().Add(-time.Hour))
	future := timestamppb.New(time.Now().Add(time.Hour))

	t.Run("AllBornExpired_NotLogged", func(t *testing.T) {
		s, log, appendCount := newSvc(t)
		if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{{Key: "a", Expiration: past}, {Key: "b", Expiration: past}},
		}); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}
		if *appendCount != 0 {
			t.Fatalf("appendCount = %d, want 0 (born-expired must not replicate)", *appendCount)
		}
		if _, ok := log.LastSeq(); ok {
			t.Fatal("mutation log has entries, want empty")
		}
	})

	t.Run("Mixed_LogsOnlyLive", func(t *testing.T) {
		s, log, appendCount := newSvc(t)
		ch, cancel, err := log.Subscribe(0)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		t.Cleanup(func() { _ = cancel() })
		if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{
				{Key: "dead1", Expiration: past},
				{Key: "live", Expiration: future},
				{Key: "dead2", Expiration: past},
			},
		}); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}
		if *appendCount != 1 {
			t.Fatalf("appendCount = %d, want 1 (one mutation for the live subset)", *appendCount)
		}
		select {
		case e := <-ch:
			mu := e.Op.(*pb.Mutation)
			got := mu.GetOp().GetPutVertices().GetVertices()
			if len(got) != 1 || got[0].GetKey() != "live" {
				t.Fatalf("logged vertices = %v, want exactly [live]", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the live mutation")
		}
	})

	t.Run("AllLive_ForwardsRequestUnchanged", func(t *testing.T) {
		s, log, appendCount := newSvc(t)
		ch, cancel, err := log.Subscribe(0)
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		t.Cleanup(func() { _ = cancel() })
		// "y" has no expiration (zero = never expires = live).
		if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{{Key: "x", Expiration: future}, {Key: "y"}},
		}); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}
		if *appendCount != 1 {
			t.Fatalf("appendCount = %d, want 1", *appendCount)
		}
		select {
		case e := <-ch:
			mu := e.Op.(*pb.Mutation)
			if got := mu.GetOp().GetPutVertices().GetVertices(); len(got) != 2 {
				t.Fatalf("logged %d vertices, want 2 (request forwarded unchanged)", len(got))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out")
		}
	})

	t.Run("IfAbsent_WrittenExcludesBornExpired", func(t *testing.T) {
		s, _, appendCount := newSvc(t)
		resp, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
			Vertices: []*pb.Vertex{
				{Key: "dead", Expiration: past},    // discarded: born expired
				{Key: "fresh", Expiration: future}, // written
			},
			IfAbsent: true,
		})
		if err != nil {
			t.Fatalf("PutVertices(if_absent): %v", err)
		}
		if resp.GetWritten() != 1 {
			t.Fatalf("Written = %d, want 1 (born-expired must not count)", resp.GetWritten())
		}
		if got := resp.GetSkippedKeys(); len(got) != 0 {
			t.Fatalf("SkippedKeys = %v, want [] (born-expired is discarded, not skipped)", got)
		}
		if *appendCount != 1 {
			t.Fatalf("appendCount = %d, want 1 (only the live if-absent write replicates)", *appendCount)
		}
	})
}

func keyFor(i int) string {
	return "k-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// fakeHotPathMetrics captures one observation per RPC so tests can assert
// the service-layer instrumentation hooks fire on the right callbacks.
type fakeHotPathMetrics struct {
	illuminate []illuminateObs
	results    []illuminateResultObs
	scan       []scanObs
	search     []searchObs
	batch      []batchObs
	getVertex  []hitMissObs
	getEdge    []hitMissObs
	contribDed []int
}

type illuminateObs struct {
	algorithm, reduction, objective, weighting string
	visitedVertices, visitedEdges              int
	traversal, optimize                        time.Duration
}

type illuminateResultObs struct {
	algorithm, reduction, objective, weighting, phase, code string
}

type scanObs struct {
	op       string
	results  int
	duration time.Duration
}

type searchObs struct {
	results  int
	duration time.Duration
}

type batchObs struct {
	op   string
	size int
}

type hitMissObs struct {
	hits   int
	misses int
}

func (f *fakeHotPathMetrics) OnIlluminate(algorithm, reduction, objective, weighting string, vV, vE int, traversal, optimize time.Duration) {
	f.illuminate = append(f.illuminate, illuminateObs{algorithm, reduction, objective, weighting, vV, vE, traversal, optimize})
}
func (f *fakeHotPathMetrics) OnIlluminateResult(algorithm, reduction, objective, weighting, phase, code string) {
	f.results = append(f.results, illuminateResultObs{algorithm, reduction, objective, weighting, phase, code})
}
func (f *fakeHotPathMetrics) OnScan(op string, results int, d time.Duration) {
	f.scan = append(f.scan, scanObs{op, results, d})
}
func (f *fakeHotPathMetrics) OnSearch(results int, d time.Duration) {
	f.search = append(f.search, searchObs{results, d})
}
func (f *fakeHotPathMetrics) OnBatch(op string, size int) {
	f.batch = append(f.batch, batchObs{op, size})
}
func (f *fakeHotPathMetrics) OnGetVertices(hits, misses int) {
	f.getVertex = append(f.getVertex, hitMissObs{hits, misses})
}
func (f *fakeHotPathMetrics) OnGetEdges(hits, misses int) {
	f.getEdge = append(f.getEdge, hitMissObs{hits, misses})
}
func (f *fakeHotPathMetrics) OnEdgeContribDeduped(n int) {
	f.contribDed = append(f.contribDed, n)
}

func TestLanternService_HotPathMetrics_EmitsOnceForBatchAndIlluminate(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithHotPathMetrics(fm)

	ctx := context.Background()

	// PutVertices → one batch observation.
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: futureTs(time.Minute)},
		{Key: "b", Value: &pb.Vertex_Int64{Int64: 2}, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	// AddEdges → one batch observation.
	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	if len(fm.batch) != 2 {
		t.Fatalf("batch observations = %d, want 2 (PutVertices, AddEdges)", len(fm.batch))
	}
	if fm.batch[0].op != "PutVertices" || fm.batch[0].size != 2 {
		t.Errorf("batch[0] = %+v, want {PutVertices, 2}", fm.batch[0])
	}
	if fm.batch[1].op != "AddEdges" || fm.batch[1].size != 1 {
		t.Errorf("batch[1] = %+v, want {AddEdges, 1}", fm.batch[1])
	}

	// Illuminate → exactly one illuminate observation with the right labels.
	// Objective is pinned to MINIMIZE so the asserted "minimize" label is
	// explicit and independent of the UNSPECIFIED default (which is MAXIMIZE
	// since #560).
	if _, err := s.Illuminate(ctx, &pb.IlluminateRequest{
		Seed: "a",
		Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{
			Step:      2,
			FanOut:    10,
			Reduction: pb.Reduction_REDUCTION_MINIMUM_SPANNING_TREE,
			Objective: pb.Objective_OBJECTIVE_MINIMIZE,
		}},
	}); err != nil {
		t.Fatalf("Illuminate: %v", err)
	}
	if len(fm.illuminate) != 1 {
		t.Fatalf("illuminate observations = %d, want 1", len(fm.illuminate))
	}
	if got := fm.illuminate[0]; got.algorithm != "bfs" || got.reduction != "mst" || got.objective != "minimize" || got.weighting != "raw" || got.visitedVertices < 1 {
		t.Errorf("illuminate[0] = %+v, want algorithm=bfs reduction=mst objective=minimize weighting=raw visitedVertices≥1", got)
	}
	if len(fm.results) != 1 || fm.results[0].phase != "complete" || fm.results[0].code != "ok" {
		t.Fatalf("illuminate result observations = %+v, want one complete/ok", fm.results)
	}

	// Singular forwarders must NOT double-instrument: GetVertex forwards
	// to GetVertices; one batch observation is expected, not two.
	prev := len(fm.batch)
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "a"}); err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if added := len(fm.batch) - prev; added != 1 {
		t.Errorf("singular GetVertex emitted %d batch observations, want 1 (must not double-count via plural)", added)
	}
}

func TestLanternService_HotPathMetrics_EmitsOnScan(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	fb := newFakeBackend()
	fb.vertices["p:1"] = &pb.Vertex{Key: "p:1", Value: &pb.Vertex_Int64{Int64: 1}}
	fb.vertices["p:2"] = &pb.Vertex{Key: "p:2", Value: &pb.Vertex_Int64{Int64: 2}}
	s := NewLanternService(fb).WithHotPathMetrics(fm)
	ctx := context.Background()
	if _, err := s.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "p:", Limit: 100}); err != nil {
		t.Fatalf("ScanVertices: %v", err)
	}
	if _, err := s.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "p:", Limit: 100}); err != nil {
		t.Fatalf("ScanVertexKeys: %v", err)
	}
	if _, err := s.ScanEdges(ctx, &pb.ScanEdgesRequest{Limit: 100}); err != nil {
		t.Fatalf("ScanEdges: %v", err)
	}
	if _, err := s.CountVerticesByPrefix(ctx, &pb.CountVerticesByPrefixRequest{Prefix: "p:"}); err != nil {
		t.Fatalf("CountVerticesByPrefix: %v", err)
	}
	if _, err := s.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "p:", Limit: 100}); err != nil {
		t.Fatalf("DeleteVerticesByPrefix: %v", err)
	}
	if len(fm.scan) != 5 {
		t.Fatalf("scan observations = %d, want 5", len(fm.scan))
	}
	wantOps := []string{"ScanVertices", "ScanVertexKeys", "ScanEdges", "CountVerticesByPrefix", "DeleteVerticesByPrefix"}
	for i, want := range wantOps {
		if fm.scan[i].op != want {
			t.Errorf("scan[%d].op = %q, want %q", i, fm.scan[i].op, want)
		}
	}
	if fm.scan[0].results != 2 {
		t.Errorf("ScanVertices results = %d, want 2", fm.scan[0].results)
	}
	if fm.scan[4].results != 2 {
		t.Errorf("DeleteVerticesByPrefix results = %d, want 2", fm.scan[4].results)
	}
}

// TestLanternService_HotPathMetrics_EmitsOnSearch asserts that SearchVertices
// fires OnSearch (not OnScan) with the result count and a positive duration.
func TestLanternService_HotPathMetrics_EmitsOnSearch(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	fb := newFakeBackend()
	fb.searchResults = []search.Result[string]{{ID: "k:1", Score: 0.9}, {ID: "k:2", Score: 0.5}}
	s := NewLanternService(fb).
		WithSearchLimits(SearchLimits{Enabled: true, DefaultLimit: 10, MaxLimit: 100}).
		WithHotPathMetrics(fm)
	ctx := context.Background()

	if _, err := s.SearchVertices(ctx, &pb.SearchVerticesRequest{Query: "hello"}); err != nil {
		t.Fatalf("SearchVertices: %v", err)
	}
	if len(fm.scan) != 0 {
		t.Errorf("OnScan called %d times during SearchVertices, want 0 (must use OnSearch instead)", len(fm.scan))
	}
	if len(fm.search) != 1 {
		t.Fatalf("OnSearch observations = %d, want 1", len(fm.search))
	}
	if fm.search[0].results != 2 {
		t.Errorf("OnSearch results = %d, want 2", fm.search[0].results)
	}
	if fm.search[0].duration <= 0 {
		t.Errorf("OnSearch duration = %v, want > 0", fm.search[0].duration)
	}
}

// TestLanternService_HotPathMetrics_EmitsGetVertexHitMiss asserts the #539
// hit/miss split fires once per GetVertices with the right counts, that the
// singular GetVertex forwards through the plural (so it counts exactly
// once), and that a present-but-nil vertex value still scores as a hit.
func TestLanternService_HotPathMetrics_EmitsGetVertexHitMiss(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithHotPathMetrics(fm)
	ctx := context.Background()

	// Two live vertices: one with a concrete value, one explicitly nil.
	if _, err := s.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_Int64{Int64: 1}, Expiration: futureTs(time.Minute)},
		{Key: "n", Value: &pb.Vertex_Nil{Nil: true}, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	// Mixed batch: 2 hits (a, n incl. present-but-nil) + 1 miss (gone).
	if _, err := s.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{"a", "n", "gone"}}); err != nil {
		t.Fatalf("GetVertices: %v", err)
	}
	if len(fm.getVertex) != 1 {
		t.Fatalf("getVertex observations = %d, want 1", len(fm.getVertex))
	}
	if got := fm.getVertex[0]; got.hits != 2 || got.misses != 1 {
		t.Errorf("getVertex[0] = %+v, want {hits:2 misses:1}", got)
	}

	// Singular GetVertex must forward through the plural: exactly one more
	// observation, counted as a single hit.
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "a"}); err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if len(fm.getVertex) != 2 {
		t.Fatalf("getVertex observations after singular = %d, want 2", len(fm.getVertex))
	}
	if got := fm.getVertex[1]; got.hits != 1 || got.misses != 0 {
		t.Errorf("getVertex[1] = %+v, want {hits:1 misses:0}", got)
	}

	// A pure miss scores only on the miss side.
	if _, err := s.GetVertex(ctx, &pb.GetVertexRequest{Key: "absent"}); err == nil {
		t.Fatal("GetVertex(absent): expected NotFound error, got nil")
	}
	if got := fm.getVertex[2]; got.hits != 0 || got.misses != 1 {
		t.Errorf("getVertex[2] = %+v, want {hits:0 misses:1}", got)
	}
}

// TestLanternService_HotPathMetrics_EmitsGetEdgeHitMiss asserts the #539
// edge-side hit/miss split fires once per GetEdges and that the singular
// GetEdge forwards through the plural.
func TestLanternService_HotPathMetrics_EmitsGetEdgeHitMiss(t *testing.T) {
	fm := &fakeHotPathMetrics{}
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithHotPathMetrics(fm)
	ctx := context.Background()

	if _, err := s.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
		{Tail: "a", Head: "b", Weight: 1, Expiration: futureTs(time.Minute)},
	}}); err != nil {
		t.Fatalf("AddEdges: %v", err)
	}

	// 1 hit (a->b) + 1 miss (a->z).
	if _, err := s.GetEdges(ctx, &pb.GetEdgesRequest{Edges: []*pb.EdgeKey{
		{Tail: "a", Head: "b"},
		{Tail: "a", Head: "z"},
	}}); err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(fm.getEdge) != 1 {
		t.Fatalf("getEdge observations = %d, want 1", len(fm.getEdge))
	}
	if got := fm.getEdge[0]; got.hits != 1 || got.misses != 1 {
		t.Errorf("getEdge[0] = %+v, want {hits:1 misses:1}", got)
	}

	// Singular GetEdge forwards through the plural: one more observation.
	if _, err := s.GetEdge(ctx, &pb.GetEdgeRequest{Tail: "a", Head: "b"}); err != nil {
		t.Fatalf("GetEdge: %v", err)
	}
	if len(fm.getEdge) != 2 {
		t.Fatalf("getEdge observations after singular = %d, want 2", len(fm.getEdge))
	}
	if got := fm.getEdge[1]; got.hits != 1 || got.misses != 0 {
		t.Errorf("getEdge[1] = %+v, want {hits:1 misses:0}", got)
	}
}

// TestExpirationClamp_FiresValidationRejectHook covers the #222
// bad_ttl hook fire from validateExpiration. The hook MUST run before
// the InvalidArgument status is returned and MUST NOT fire when the
// expiration is within the clamp.
func TestExpirationClamp_FiresValidationRejectHook(t *testing.T) {
	const ttl = time.Hour
	var got []string
	s := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)).
		WithTombstoneTTL(ttl).
		WithValidationRejectHook(func(reason string) { got = append(got, reason) })

	bad := timestamppb.New(time.Now().Add(2 * ttl))
	if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: bad}},
	}); err == nil {
		t.Fatal("PutVertices over TTL: expected error")
	}
	if len(got) != 1 || got[0] != "bad_ttl" {
		t.Fatalf("reject hook calls = %v, want [bad_ttl]", got)
	}

	got = nil
	ok := timestamppb.New(time.Now().Add(ttl / 2))
	if _, err := s.PutVertices(context.Background(), &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "v", Value: &pb.Vertex_Nil{Nil: true}, Expiration: ok}},
	}); err != nil {
		t.Fatalf("PutVertices within TTL: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("reject hook fired on success path: %v", got)
	}
}

// TestLanternService_Illuminate_TraversalBudget pins the #842 server-side
// wall-clock budget: a traversal that outlives LANTERN_TRAVERSAL_TIMEOUT_MS
// is cancelled and surfaces as CodeDeadlineExceeded, while the disabled
// default injects no deadline at all (client-owned behaviour unchanged), and
// an already-shorter client deadline still wins.
func TestLanternService_Illuminate_TraversalBudget(t *testing.T) {
	t.Run("default budget reaches the backend as a deadline", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb)

		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{
			Seed: "a", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if !fb.lastNeighborHadDeadline {
			t.Fatal("default traversal budget carried no deadline")
		}
	})

	t.Run("budget cancels an over-long traversal as DeadlineExceeded", func(t *testing.T) {
		fb := newFakeBackend()
		fb.neighborBlockUntilCtxDone = true
		svc := NewLanternService(fb).WithTraversalTimeout(20 * time.Millisecond)

		_, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{Seed: "a", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}})
		if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			t.Fatalf("err = %v, want CodeDeadlineExceeded", err)
		}
	})

	t.Run("explicitly disabled budget injects no deadline", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb).WithTraversalTimeout(0)

		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{Seed: "a", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.lastNeighborHadDeadline {
			t.Fatal("backend ctx carried a deadline with the budget disabled")
		}
	})

	t.Run("enabled budget reaches the backend as a deadline", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb).WithTraversalTimeout(time.Hour)

		if _, err := svc.Illuminate(context.Background(), &pb.IlluminateRequest{Seed: "a", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if !fb.lastNeighborHadDeadline {
			t.Fatal("backend ctx carried no deadline with the budget enabled")
		}
	})

	t.Run("shorter client deadline still wins over a long budget", func(t *testing.T) {
		fb := newFakeBackend()
		fb.neighborBlockUntilCtxDone = true
		svc := NewLanternService(fb).WithTraversalTimeout(time.Hour)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := svc.Illuminate(ctx, &pb.IlluminateRequest{Seed: "a", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}})
		if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
			t.Fatalf("err = %v, want CodeDeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("client deadline did not win: took %v", elapsed)
		}
	})
}

// TestLanternService_CapacityCaps pins the #848 soft-cap contract at the
// write-RPC boundary: caps unset leave behavior unchanged; a batch that
// exactly reaches the cap succeeds while the next single-item write fails
// with RESOURCE_EXHAUSTED naming the env knob; deletes free capacity; edge
// writes consult BOTH caps with the conservative 2-endpoints-per-item vertex
// delta (over-rejection near the cap for existing keys is the documented
// soft-cap slack, not a bug); ApplyMutation bypasses the caps entirely; and
// each rejection fires the validation-reject hook with reason "capacity".
func TestLanternService_CapacityCaps(t *testing.T) {
	ctx := context.Background()
	exp := timestamppb.New(time.Now().Add(time.Hour))
	vertex := func(key string) *pb.Vertex {
		return &pb.Vertex{Key: key, Value: &pb.Vertex_Nil{Nil: true}, Expiration: exp}
	}
	newSvc := func(l CapacityLimits, rejects *[]string) *LanternService {
		cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
		svc := NewLanternService(cache).WithCapacityLimits(l)
		if rejects != nil {
			svc = svc.WithValidationRejectHook(func(reason string) { *rejects = append(*rejects, reason) })
		}
		return svc
	}
	isExhausted := func(t *testing.T, err error, knob string) {
		t.Helper()
		if connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("code = %v (err=%v), want ResourceExhausted", connect.CodeOf(err), err)
		}
		if !strings.Contains(err.Error(), knob) {
			t.Fatalf("error %q does not name the knob %s", err.Error(), knob)
		}
	}

	t.Run("UnsetCapsUnchanged", func(t *testing.T) {
		svc := newSvc(CapacityLimits{}, nil)
		for i := 0; i < 50; i++ {
			if _, err := svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: vertex(fmt.Sprintf("k%02d", i))}); err != nil {
				t.Fatalf("put %d with caps unset: %v", i, err)
			}
		}
	})

	t.Run("VertexBoundary", func(t *testing.T) {
		var rejects []string
		svc := newSvc(CapacityLimits{MaxVertices: 3}, &rejects)
		// A batch that exactly reaches the cap succeeds.
		if _, err := svc.PutVertices(ctx, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
			vertex("a"), vertex("b"), vertex("c"),
		}}); err != nil {
			t.Fatalf("exact-fit batch: %v", err)
		}
		// The next single-item write fails, naming the knob.
		_, err := svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: vertex("d")})
		isExhausted(t, err, "LANTERN_MAX_VERTICES")
		// Over-rejection slack: re-putting an EXISTING key at the cap is
		// rejected too — the pre-check cannot know the key already exists.
		// This is the documented conservative behavior, not a bug.
		_, err = svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: vertex("a")})
		isExhausted(t, err, "LANTERN_MAX_VERTICES")
		// Deletes free capacity.
		if _, err := svc.DeleteVertex(ctx, &pb.DeleteVertexRequest{Key: "a"}); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: vertex("d")}); err != nil {
			t.Fatalf("put after delete freed capacity: %v", err)
		}
		for i, r := range rejects {
			if r != "capacity" {
				t.Fatalf("reject %d reason = %q, want capacity", i, r)
			}
		}
		if len(rejects) != 2 {
			t.Fatalf("hook fired %d times, want 2", len(rejects))
		}
	})

	t.Run("EdgeCapsBothSides", func(t *testing.T) {
		var rejects []string
		svc := newSvc(CapacityLimits{MaxVertices: 4, MaxEdges: 1}, &rejects)
		// One edge: 2 potential endpoints <= 4, 1 edge <= 1 — fits.
		if _, err := svc.AddEdge(ctx, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: "h", Weight: 1, Expiration: exp}}); err != nil {
			t.Fatalf("first edge: %v", err)
		}
		// Second edge trips the EDGE cap.
		_, err := svc.AddEdge(ctx, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: "h2", Weight: 1, Expiration: exp}})
		isExhausted(t, err, "LANTERN_MAX_EDGES")
		// Vertex cap via the conservative 2-per-item delta: 2 live + 2*2 > 4.
		svc2 := newSvc(CapacityLimits{MaxVertices: 4}, nil)
		if _, err := svc2.AddEdge(ctx, &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "t", Head: "h", Weight: 1, Expiration: exp}}); err != nil {
			t.Fatalf("edge within vertex cap: %v", err)
		}
		_, err = svc2.AddEdges(ctx, &pb.AddEdgesRequest{Edges: []*pb.Edge{
			{Tail: "x1", Head: "y1", Weight: 1, Expiration: exp},
			{Tail: "x2", Head: "y2", Weight: 1, Expiration: exp},
		}})
		isExhausted(t, err, "LANTERN_MAX_VERTICES")
		// PutEdges shares the same guards.
		_, err = svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "p", Head: "q", Weight: 1, Expiration: exp}}})
		isExhausted(t, err, "LANTERN_MAX_EDGES")
	})

	t.Run("ApplyMutationBypasses", func(t *testing.T) {
		svc := newSvc(CapacityLimits{MaxVertices: 1, MaxEdges: 1}, nil)
		origin := bytes16("origin-A")
		m := &pb.Mutation{Seq: 1, Hlc: newHLC(1, origin), Origin: origin[:], Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertices{
			PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{vertex("r1"), vertex("r2"), vertex("r3")}},
		}}}
		if err := svc.ApplyMutation(ctx, m); err != nil {
			t.Fatalf("replicated apply must bypass the cap: %v", err)
		}
		if got := svc.cache.VertexCount(); got != 3 {
			t.Fatalf("VertexCount = %d, want 3 (apply ignores the cap)", got)
		}
		m2 := &pb.Mutation{Seq: 2, Hlc: newHLC(2, origin), Origin: origin[:], Op: &pb.MutationOp{Op: &pb.MutationOp_AddEdges{
			AddEdges: &pb.AddEdgesRequest{Edges: []*pb.Edge{
				{Tail: "r1", Head: "r2", Weight: 1, Expiration: exp},
				{Tail: "r2", Head: "r3", Weight: 1, Expiration: exp},
			}},
		}}}
		if err := svc.ApplyMutation(ctx, m2); err != nil {
			t.Fatalf("replicated edge apply must bypass the cap: %v", err)
		}
	})
}

// TestIlluminate_FlatFieldGhost is the #846 reserved-range regression: a
// stale pre-oneof client whose binary still emits the retired FLAT field
// numbers (2 step, 3 k, 6 algorithm, 7 objective, 10 restart_prob,
// 11 epsilon) must decode as params-unset and be rejected as
// InvalidArgument — never alias onto the new oneof arms (12/13) or steer a
// dispatch. Proves the `reserved` ranges actually shield against stale
// clients rather than just documenting intent.
func TestIlluminate_FlatFieldGhost(t *testing.T) {
	var raw []byte
	raw = protowire.AppendTag(raw, 1, protowire.BytesType) // seed = "a"
	raw = protowire.AppendString(raw, "a")
	raw = protowire.AppendTag(raw, 2, protowire.VarintType) // retired step = 9
	raw = protowire.AppendVarint(raw, 9)
	raw = protowire.AppendTag(raw, 3, protowire.VarintType) // retired k = 7
	raw = protowire.AppendVarint(raw, 7)
	raw = protowire.AppendTag(raw, 6, protowire.VarintType) // retired algorithm = PPR(3)
	raw = protowire.AppendVarint(raw, 3)
	raw = protowire.AppendTag(raw, 7, protowire.VarintType) // retired objective = MINIMIZE(1)
	raw = protowire.AppendVarint(raw, 1)
	raw = protowire.AppendTag(raw, 10, protowire.Fixed32Type) // retired restart_prob = 0.5
	raw = protowire.AppendFixed32(raw, math.Float32bits(0.5))
	raw = protowire.AppendTag(raw, 11, protowire.Fixed32Type) // retired epsilon = 0.001
	raw = protowire.AppendFixed32(raw, math.Float32bits(0.001))

	var req pb.IlluminateRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		t.Fatalf("Unmarshal ghost request: %v", err)
	}
	if req.GetSeed() != "a" {
		t.Fatalf("seed = %q, want a", req.GetSeed())
	}
	if req.GetParams() != nil {
		t.Fatalf("ghost flat fields decoded into params oneof: %T", req.GetParams())
	}

	// End-to-end through the dispatcher: the stale request must fail before it
	// reaches either traversal family. A retired algorithm=3 byte cannot turn
	// an invalid request into PPR (or a silent zero-hop BFS).
	fb := newFakeBackend()
	svc := NewLanternService(fb)
	if _, err := svc.Illuminate(context.Background(), &req); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("Illuminate(ghost) code = %v, want InvalidArgument (err = %v)", connect.CodeOf(err), err)
	}
	if fb.pprCalls != 0 {
		t.Fatalf("pprCalls = %d, want 0 — stale request reached PPR", fb.pprCalls)
	}
	if fb.neighborCalls != 0 {
		t.Fatalf("neighborCalls = %d, want 0 — stale request reached BFS", fb.neighborCalls)
	}
}

// TestLanternService_Illuminate_LocalCommunity pins the #845 wire arm:
// dispatch to LocalCommunityContext with max_size/α/ε plumbing, the
// "community" metric label, a response carrying real induced edges (not a
// star), the optional tree reduction with the isolated-vertex membership
// contract, and reduction-unset output identical to the raw arm.
func TestLanternService_Illuminate_LocalCommunity(t *testing.T) {
	ctx := context.Background()

	t.Run("dispatch and knob plumbing", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(ctx, &pb.IlluminateRequest{
			Seed: "s",
			Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{
				MaxSize: 7, RestartProb: 0.3, Epsilon: 1e-3,
			}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.communityCalls != 1 || fb.pprCalls != 0 || fb.neighborCalls != 0 {
			t.Fatalf("dispatch: community=%d ppr=%d neighbor=%d, want 1/0/0",
				fb.communityCalls, fb.pprCalls, fb.neighborCalls)
		}
		if fb.lastCommunityMaxSize != 7 {
			t.Errorf("maxSize = %d, want 7", fb.lastCommunityMaxSize)
		}
		if math.Abs(fb.lastCommunityAlpha-0.3) > 1e-6 || math.Abs(fb.lastCommunityEpsilon-1e-3) > 1e-9 {
			t.Errorf("alpha/epsilon = %v/%v, want 0.3/1e-3", fb.lastCommunityAlpha, fb.lastCommunityEpsilon)
		}
	})

	t.Run("unset knobs resolve to PPR defaults", func(t *testing.T) {
		fb := newFakeBackend()
		svc := NewLanternService(fb)
		if _, err := svc.Illuminate(ctx, &pb.IlluminateRequest{
			Seed:   "s",
			Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.lastCommunityAlpha != graphcache.DefaultPPRAlpha || fb.lastCommunityEpsilon != graphcache.DefaultPPREpsilon {
			t.Errorf("defaults = %v/%v, want %v/%v", fb.lastCommunityAlpha, fb.lastCommunityEpsilon,
				graphcache.DefaultPPRAlpha, graphcache.DefaultPPREpsilon)
		}
	})

	t.Run("zero max_size resolves to the operator cap and forwards the work budget", func(t *testing.T) {
		fb := newFakeBackend()
		budget := graphcache.PPRWorkBudget{MaxPushes: 7, MaxTouchedEdges: 11}
		svc := NewLanternService(fb).WithTraversalLimits(TraversalLimits{WorkBudget: budget, MaxResults: 3})
		if _, err := svc.Illuminate(ctx, &pb.IlluminateRequest{
			Seed: "s", Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{}},
		}); err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		if fb.lastCommunityMaxSize != 3 {
			t.Errorf("max_size=0 forwarded as %d, want configured cap 3", fb.lastCommunityMaxSize)
		}
		if fb.lastCommunityBudget != budget {
			t.Errorf("community budget = %+v, want %+v", fb.lastCommunityBudget, budget)
		}
	})

	t.Run("real induced edges end to end, not a star", func(t *testing.T) {
		s := newTestService(t)
		seedTriangle(t, s)
		resp, err := s.Illuminate(ctx, &pb.IlluminateRequest{
			Seed:   "a",
			Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{}},
		})
		if err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		// The triangle's non-seed-tail edges must survive — a star would
		// only ever emit edges with tail == seed.
		nonSeedTail := false
		for _, e := range resp.Graph.Edges {
			if e.Tail != "a" {
				nonSeedTail = true
			}
			if e.Expiration == nil {
				t.Errorf("induced edge %s->%s missing expiration", e.Tail, e.Head)
			}
		}
		if !nonSeedTail {
			t.Fatalf("no non-seed-tail edge in response — looks like a star: %+v", resp.Graph.Edges)
		}
	})

	t.Run("reduction keeps unreachable members as isolated vertices", func(t *testing.T) {
		// Fake community {s, m, island}: island is a member but has no edge
		// from s's reachable component. The SPT view must keep island as an
		// isolated vertex, and tree edges must not exceed |reachable|-1.
		fb := newFakeBackend()
		g := coregraph.NewGraph[string, *pb.Vertex]()
		for _, k := range []string{"s", "m", "island"} {
			g.Vertices[k] = &pb.Vertex{Key: k, Value: &pb.Vertex_Nil{Nil: true}}
		}
		g.Edges["s"] = map[string]float32{"m": 1}
		fb.communityGraph = g
		fb.communityExpirations = map[string]map[string]time.Time{
			"s": {"m": time.Now().Add(time.Hour)},
		}
		svc := NewLanternService(fb)
		resp, err := svc.Illuminate(ctx, &pb.IlluminateRequest{
			Seed: "s",
			Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{
				Reduction: pb.Reduction_REDUCTION_SHORTEST_PATH_TREE,
			}},
		})
		if err != nil {
			t.Fatalf("Illuminate: %v", err)
		}
		keys := map[string]bool{}
		for _, v := range resp.Graph.Vertices {
			keys[v.GetKey()] = true
		}
		if !keys["island"] {
			t.Fatalf("unreachable member dropped by the reduction: %v", keys)
		}
		if len(resp.Graph.Edges) > 1 {
			t.Fatalf("tree has %d edges, want <= |reachable|-1 = 1", len(resp.Graph.Edges))
		}
	})

	t.Run("reduction unset equals raw induced output", func(t *testing.T) {
		build := func(red pb.Reduction) *pb.IlluminateResponse {
			s := newTestService(t)
			seedTriangle(t, s)
			resp, err := s.Illuminate(ctx, &pb.IlluminateRequest{
				Seed:   "a",
				Params: &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{Reduction: red}},
			})
			if err != nil {
				t.Fatalf("Illuminate: %v", err)
			}
			return resp
		}
		raw := build(pb.Reduction_REDUCTION_UNSPECIFIED)
		again := build(pb.Reduction_REDUCTION_UNSPECIFIED)
		if len(raw.Graph.Vertices) != len(again.Graph.Vertices) || len(raw.Graph.Edges) != len(again.Graph.Edges) {
			t.Fatalf("reduction-unset output not stable: %d/%d vs %d/%d vertices/edges",
				len(raw.Graph.Vertices), len(raw.Graph.Edges), len(again.Graph.Vertices), len(again.Graph.Edges))
		}
	})
}
