package replication

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// recordingApplier records which SnapshotApplier seam each snapshot edge
// re-apply routed to, so applySnapshotEdge's LWW-vs-G-Set routing can be
// asserted in isolation.
type recordingApplier struct {
	putEdges      []recordedEdge
	addEdges      []recordedEdge
	vertexBarrier []recordedVertexBarrier
	edgeBarrier   []recordedEdge
}

type recordedVertexBarrier struct {
	key string
	ts  hlc.Timestamp
}

type recordedEdge struct {
	tail, head string
	cid        graphcache.ContribID
	expiration time.Time
	ts         hlc.Timestamp
}

func (r *recordingApplier) PutVertexWithExpirationHLC(string, *pb.Vertex, time.Time, hlc.Timestamp) bool {
	return true
}

func (r *recordingApplier) AddEdgeWithExpirationContribHLC(tail, head string, _ float32, _ time.Time, cid graphcache.ContribID, _ hlc.Timestamp) bool {
	r.addEdges = append(r.addEdges, recordedEdge{tail: tail, head: head, cid: cid})
	return true
}

func (r *recordingApplier) PutEdgeWithExpirationHLC(tail, head string, _ float32, expiration time.Time, ts hlc.Timestamp) bool {
	r.putEdges = append(r.putEdges, recordedEdge{tail: tail, head: head, expiration: expiration, ts: ts})
	return true
}

func (r *recordingApplier) ApplyVertexCausalBarrierHLC(key string, ts hlc.Timestamp) bool {
	r.vertexBarrier = append(r.vertexBarrier, recordedVertexBarrier{key: key, ts: ts})
	return true
}

func (r *recordingApplier) ApplyEdgeCausalBarrierHLC(tail, head string, ts hlc.Timestamp) bool {
	r.edgeBarrier = append(r.edgeBarrier, recordedEdge{tail: tail, head: head, ts: ts})
	return true
}

func TestApplySnapshotCausalBarriers(t *testing.T) {
	r := &recordingApplier{}
	ts := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0x20}}
	r.ApplyVertexCausalBarrierHLC("barrier-vertex", ts)
	r.ApplyEdgeCausalBarrierHLC("barrier-tail", "barrier-head", ts)
	if len(r.addEdges) != 0 || len(r.putEdges) != 0 {
		t.Fatalf("barrier leaked into live apply seams: add=%d put=%d", len(r.addEdges), len(r.putEdges))
	}
	if len(r.vertexBarrier) != 1 || r.vertexBarrier[0].key != "barrier-vertex" || r.vertexBarrier[0].ts != ts {
		t.Fatalf("vertex barrier = %+v", r.vertexBarrier)
	}
	if len(r.edgeBarrier) != 1 {
		t.Fatalf("edge barrier count = %d, want 1", len(r.edgeBarrier))
	}
	got := r.edgeBarrier[0]
	if got.tail != "barrier-tail" || got.head != "barrier-head" || got.ts != ts {
		t.Fatalf("edge barrier = %+v", got)
	}
}

// TestApplySnapshotEdge pins the #735 fix: a Put-origin (LWW, zero ContribID)
// snapshot edge must be re-applied via PutEdgeWithExpirationHLC (idempotent
// overwrite), NOT AddEdgeWithExpirationContribHLC (which G-Set-appends and, for
// a zero cid, skips dedup — accumulating a fresh contribution on every repeated
// snapshot / anti-entropy re-sync and leaking heap). AddEdge-origin edges
// (non-zero ContribID) must keep the dedup-aware AddEdge path.
func TestApplySnapshotEdge(t *testing.T) {
	now := time.Now().Add(time.Hour)
	var ts hlc.Timestamp

	t.Run("zero contribID routes to Put across repeated re-applies", func(t *testing.T) {
		r := &recordingApplier{}
		var zero graphcache.ContribID
		// Re-apply the SAME zero-cid edge three times, simulating repeated
		// full snapshots (the anti-entropy storm in #735). Every pass must
		// route to PutEdge so the edge is overwritten, never appended.
		for i := 0; i < 3; i++ {
			applySnapshotEdge(r, "a", "b", 1.5, now, zero, ts)
		}
		if len(r.addEdges) != 0 {
			t.Fatalf("zero-cid edge routed to AddEdge %d time(s); want 0 (must use Put to stay idempotent)", len(r.addEdges))
		}
		if len(r.putEdges) != 3 {
			t.Fatalf("zero-cid edge routed to Put %d time(s); want 3", len(r.putEdges))
		}
	})

	t.Run("non-zero contribID routes to AddEdge", func(t *testing.T) {
		r := &recordingApplier{}
		var cid graphcache.ContribID
		cid[0] = 0x01 // non-zero origin byte => G-Set (AddEdge) contribution
		applySnapshotEdge(r, "a", "b", 1.5, now, cid, ts)
		if len(r.putEdges) != 0 {
			t.Fatalf("non-zero-cid edge routed to Put %d time(s); want 0", len(r.putEdges))
		}
		if len(r.addEdges) != 1 {
			t.Fatalf("non-zero-cid edge routed to AddEdge %d time(s); want 1", len(r.addEdges))
		}
		if r.addEdges[0].cid != cid {
			t.Fatalf("AddEdge got cid %v; want %v", r.addEdges[0].cid, cid)
		}
	})
}

// TestApplySnapshotEdgeIdempotent is the end-to-end #735 regression against a
// real GraphCache: replaying the SAME zero-cid (LWW/Put-origin) snapshot edge
// many times must NOT accumulate weight. Before the fix, applySnapshotEdge
// routed through AddEdgeWithExpirationContribHLC, whose zero-cid path skips
// dedup and G-Set-appends a fresh contribution on every pass, so the edge
// weight grew without bound on each repeated snapshot / anti-entropy re-sync.
func TestApplySnapshotEdgeIdempotent(t *testing.T) {
	c := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	exp := time.Now().Add(time.Minute)

	t.Run("zero-cid edge does not accumulate across repeated snapshots", func(t *testing.T) {
		var zero graphcache.ContribID
		for i := 0; i < 5; i++ {
			applySnapshotEdge(c, "a", "b", 2.5, exp, zero, hlc.Timestamp{})
		}
		got, ok := c.GetWeight("a", "b")
		if !ok {
			t.Fatalf("edge a->b missing after re-applies")
		}
		if got != 2.5 {
			t.Fatalf("edge weight = %v after 5 zero-cid re-applies; want 2.5 (LWW overwrite, no accumulation)", got)
		}
	})

	t.Run("non-zero-cid edge dedups on ContribID", func(t *testing.T) {
		var cid graphcache.ContribID
		cid[0] = 0x07
		for i := 0; i < 5; i++ {
			applySnapshotEdge(c, "x", "y", 3.0, exp, cid, hlc.Timestamp{})
		}
		got, ok := c.GetWeight("x", "y")
		if !ok {
			t.Fatalf("edge x->y missing after re-applies")
		}
		if got != 3.0 {
			t.Fatalf("edge weight = %v after 5 same-cid re-applies; want 3.0 (G-Set dedup)", got)
		}
	})
}

func TestResumeAfterSnapshot(t *testing.T) {
	t.Run("empty header has no cursor", func(t *testing.T) {
		if got := resumeAfterSnapshot(nil); got.origins != nil || got.local != 0 {
			t.Fatalf("resumeAfterSnapshot(nil) = %+v, want zero cursor", got)
		}
	})

	t.Run("cutoffs advance to the next origin sequence", func(t *testing.T) {
		header := &pb.SnapshotHeader{
			CutoffSeqPerOrigin: map[string]uint64{"origin-a": 7, "origin-b": 12},
			CutoffLocalSeq:     20,
		}
		got := resumeAfterSnapshot(header)
		if got.origins["origin-a"] != 8 || got.origins["origin-b"] != 13 || got.local != 21 {
			t.Fatalf("resume cursor = %+v, want origin-a=8 origin-b=13 local=21", got)
		}
	})

	t.Run("maximum cutoff does not wrap", func(t *testing.T) {
		header := &pb.SnapshotHeader{
			CutoffSeqPerOrigin: map[string]uint64{"origin-max": ^uint64(0)},
			CutoffLocalSeq:     ^uint64(0),
		}
		got := resumeAfterSnapshot(header)
		if got.origins["origin-max"] != ^uint64(0) || got.local != ^uint64(0) {
			t.Fatalf("maximum cutoffs resumed at %+v, want no wrap", got)
		}
	})
}

func TestSnapshotReplayStateFailClosed(t *testing.T) {
	header := &pb.SnapshotHeader{}
	footer := func(v, e, vb, eb uint64) *pb.SnapshotFooter {
		return &pb.SnapshotFooter{
			VertexCount: v, EdgeCount: e,
			VertexCausalBarrierCount: vb, EdgeCausalBarrierCount: eb,
		}
	}

	t.Run("complete ordered stream", func(t *testing.T) {
		var state snapshotReplayState
		if err := state.acceptHeader(header); err != nil {
			t.Fatal(err)
		}
		for _, step := range []struct {
			kind  string
			phase snapshotFramePhase
		}{
			{"vertex causal barrier", snapshotPhaseVertexBarrier},
			{"edge causal barrier", snapshotPhaseEdgeBarrier},
			{"vertex", snapshotPhaseVertex},
			{"edge", snapshotPhaseEdge},
		} {
			if err := state.acceptBody(step.kind, step.phase); err != nil {
				t.Fatal(err)
			}
		}
		state.counts = snapshotReplayCounts{vertices: 1, edges: 2, vertexBarrier: 3, edgeBarrier: 4}
		if err := state.acceptFooter(footer(1, 2, 3, 4)); err != nil {
			t.Fatal(err)
		}
		if err := state.validateComplete(); err != nil {
			t.Fatal(err)
		}
	})

	for _, tc := range []struct {
		name string
		run  func(*snapshotReplayState) error
	}{
		{"body before header", func(s *snapshotReplayState) error { return s.acceptBody("vertex", snapshotPhaseVertex) }},
		{"missing header", func(s *snapshotReplayState) error { return s.validateComplete() }},
		{"missing footer", func(s *snapshotReplayState) error { _ = s.acceptHeader(header); return s.validateComplete() }},
		{"duplicate header", func(s *snapshotReplayState) error { _ = s.acceptHeader(header); return s.acceptHeader(header) }},
		{"duplicate footer", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptFooter(footer(0, 0, 0, 0))
			return s.acceptFooter(footer(0, 0, 0, 0))
		}},
		{"body after footer", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptFooter(footer(0, 0, 0, 0))
			return s.acceptBody("edge", snapshotPhaseEdge)
		}},
		{"barrier after live vertex", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("vertex", snapshotPhaseVertex)
			return s.acceptBody("edge causal barrier", snapshotPhaseEdgeBarrier)
		}},
		{"vertex after live edge", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("edge", snapshotPhaseEdge)
			return s.acceptBody("vertex", snapshotPhaseVertex)
		}},
		{"footer count mismatch", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			s.counts.vertices = 1
			_ = s.acceptFooter(footer(0, 0, 0, 0))
			return s.validateComplete()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(&snapshotReplayState{}); err == nil {
				t.Fatal("protocol violation was accepted")
			}
		})
	}
}
