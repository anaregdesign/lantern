package replication

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type receiptIncompatiblePeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	subscribes atomic.Int32
	snapshots  atomic.Int32
}

func (p *receiptIncompatiblePeer) Subscribe(_ context.Context, req *connect.Request[pb.SubscribeRequest], _ *connect.ServerStream[pb.SubscribeResponse]) error {
	p.subscribes.Add(1)
	if req.Msg.GetAcceptReceiptEnvelopes() {
		return connect.NewError(connect.CodeInternal, errors.New("legacy Pump unexpectedly opted into receipts"))
	}
	return connect.NewError(connect.CodeInvalidArgument, errors.New("receipt envelope requires opt-in"))
}

func (p *receiptIncompatiblePeer) Snapshot(context.Context, *connect.Request[pb.SnapshotRequest], *connect.ServerStream[pb.SnapshotResponse]) error {
	p.snapshots.Add(1)
	return connect.NewError(connect.CodeInternal, errors.New("receipt mismatch must not trigger graph-only Snapshot"))
}

func TestPumpReceiptIncompatibilityDoesNotSnapshot(t *testing.T) {
	peer := &receiptIncompatiblePeer{}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
	srv := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = protocols
	srv.Start()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pump := NewPump(Config{HTTPClient: defaultH2CClient()}, nil, nil)
	if err := pump.session(ctx, srv.URL); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("incompatible receipt stream = %v, want InvalidArgument", err)
	}
	if got := peer.subscribes.Load(); got != 1 {
		t.Fatalf("Subscribe calls = %d, want 1", got)
	}
	if got := peer.snapshots.Load(); got != 0 {
		t.Fatalf("graph-only Snapshot calls = %d, want 0", got)
	}
}

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

func (r *recordingApplier) ApplySnapshotVertexTombstoneHLC(string, hlc.Timestamp, time.Time)       {}
func (r *recordingApplier) ApplySnapshotEdgeTombstoneHLC(string, string, hlc.Timestamp, time.Time) {}

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

func TestSnapshotEdgeRows(t *testing.T) {
	stamp := func(wall int64) *pb.HLCTimestamp {
		return &pb.HLCTimestamp{WallNs: wall, NodeId: []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}
	}
	id := make([]byte, len(graphcache.ContribID{}))
	id[0] = 1
	frame := func(addStamp *pb.HLCTimestamp) *pb.SnapshotEdge {
		return &pb.SnapshotEdge{
			Tail: "tail", Head: "head", Hlc: stamp(20),
			Contributions: []*pb.SnapshotEdgeContribution{
				{Weight: 5, Hlc: stamp(20)},
				{Weight: 3, ContribId: id, Hlc: addStamp},
			},
		}
	}
	rows, err := snapshotEdgeRows(frame(stamp(30)))
	if err != nil || len(rows) != 2 || rows[0].hlc.WallNs != 20 || rows[1].hlc.WallNs != 30 {
		t.Fatalf("valid mixed rows=%+v err=%v", rows, err)
	}
	for _, tc := range []struct {
		name  string
		frame *pb.SnapshotEdge
	}{
		{"missing Add HLC", frame(nil)},
		{"Add below reset", frame(stamp(10))},
		{"Add equal to reset", frame(stamp(20))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := snapshotEdgeRows(tc.frame); err == nil {
				t.Fatal("invalid mixed Snapshot frame accepted")
			}
		})
	}
	duplicate := frame(stamp(30))
	duplicate.Contributions = append(duplicate.Contributions, duplicate.Contributions[1])
	if _, err := snapshotEdgeRows(duplicate); err == nil {
		t.Fatal("duplicate Add identity accepted")
	}
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
	footer := func(v, e, vb, eb uint64, tombstones ...uint64) *pb.SnapshotFooter {
		f := &pb.SnapshotFooter{
			VertexCount: v, EdgeCount: e,
			VertexCausalBarrierCount: vb, EdgeCausalBarrierCount: eb,
		}
		if len(tombstones) == 2 {
			f.VertexTombstoneCount, f.EdgeTombstoneCount = tombstones[0], tombstones[1]
		}
		return f
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
			{"vertex tombstone", snapshotPhaseVertexTombstone},
			{"edge tombstone", snapshotPhaseEdgeTombstone},
			{"vertex", snapshotPhaseVertex},
			{"edge", snapshotPhaseEdge},
		} {
			if err := state.acceptBody(step.kind, step.phase); err != nil {
				t.Fatal(err)
			}
		}
		state.counts = snapshotReplayCounts{vertices: 1, edges: 2, vertexBarrier: 3, edgeBarrier: 4, vertexTombstone: 5, edgeTombstone: 6}
		if err := state.acceptFooter(footer(1, 2, 3, 4, 5, 6)); err != nil {
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
		{"tombstone after live vertex", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("vertex", snapshotPhaseVertex)
			return s.acceptBody("vertex tombstone", snapshotPhaseVertexTombstone)
		}},
		{"edge tombstone before vertex tombstone", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("edge tombstone", snapshotPhaseEdgeTombstone)
			return s.acceptBody("vertex tombstone", snapshotPhaseVertexTombstone)
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
		{"truncated tombstone count", func(s *snapshotReplayState) error {
			_ = s.acceptHeader(header)
			_ = s.acceptBody("vertex tombstone", snapshotPhaseVertexTombstone)
			s.counts.vertexTombstone = 1
			_ = s.acceptFooter(footer(0, 0, 0, 0, 2, 0))
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

func TestSnapshotTombstoneFields(t *testing.T) {
	stamp := &pb.HLCTimestamp{WallNs: 10, NodeId: append([]byte{1}, make([]byte, 15)...)}
	deadline := time.Now().Add(time.Minute).UTC().Round(0)
	gotTS, gotDeadline, err := snapshotTombstoneFields(stamp, timestamppb.New(deadline))
	if err != nil || gotTS.WallNs != 10 || !gotDeadline.Equal(deadline) {
		t.Fatalf("valid tombstone = (%+v,%v,%v)", gotTS, gotDeadline, err)
	}
	for _, tc := range []struct {
		name       string
		stamp      *pb.HLCTimestamp
		expiration *timestamppb.Timestamp
	}{
		{"missing HLC", nil, timestamppb.New(deadline)},
		{"zero HLC", &pb.HLCTimestamp{NodeId: make([]byte, 16)}, timestamppb.New(deadline)},
		{"short NodeID", &pb.HLCTimestamp{WallNs: 10, NodeId: []byte{1}}, timestamppb.New(deadline)},
		{"zero NodeID", &pb.HLCTimestamp{WallNs: 10, NodeId: make([]byte, 16)}, timestamppb.New(deadline)},
		{"missing expiration", stamp, nil},
		{"invalid expiration", stamp, &timestamppb.Timestamp{Seconds: 253402300800}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := snapshotTombstoneFields(tc.stamp, tc.expiration); err == nil {
				t.Fatal("malformed tombstone frame accepted")
			}
		})
	}
}
