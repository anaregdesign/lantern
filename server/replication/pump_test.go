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
	putEdges []recordedEdge
	addEdges []recordedEdge
}

type recordedEdge struct {
	tail, head string
	cid        graphcache.ContribID
}

func (r *recordingApplier) PutVertexWithExpirationHLC(string, *pb.Vertex, time.Time, hlc.Timestamp) bool {
	return true
}

func (r *recordingApplier) AddEdgeWithExpirationContribHLC(tail, head string, _ float32, _ time.Time, cid graphcache.ContribID, _ hlc.Timestamp) bool {
	r.addEdges = append(r.addEdges, recordedEdge{tail: tail, head: head, cid: cid})
	return true
}

func (r *recordingApplier) PutEdgeWithExpirationHLC(tail, head string, _ float32, _ time.Time, _ hlc.Timestamp) bool {
	r.putEdges = append(r.putEdges, recordedEdge{tail: tail, head: head})
	return true
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
