package integration_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// snapshotPeer stands up LanternService + LanternReplicationService
// on an h2c httptest server and exposes both an SDK client (for
// writes) and a raw Connect-Go replication client (for
// Snapshot/Subscribe).
type snapshotPeer struct {
	cache *graphcache.GraphCache[string, *pb.Vertex]
	clock *hlc.Clock
	log   *mutationlog.Log
	sdk   *client.Lantern
	raw   graphv1connect.LanternServiceClient
	repl  graphv1connect.LanternReplicationServiceClient
}

func newSnapshotPeer(t *testing.T, nodeID hlc.NodeID) *snapshotPeer {
	t.Helper()
	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})

	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(nodeID, hlc.Options{})
	limits := productionSearchLimits(true, true)
	cache := newProductionSearchCache(time.Minute, true, true, limits.AnalysisLimits)
	svc := service.NewLanternService(cache).
		WithSearchLimits(limits).
		WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock).
		WithOriginStates(svc).
		WithSearchConfig(svc)
	srv := newConnectTestServer(t, svc, rep, vi.ConnectInterceptor())

	return &snapshotPeer{
		cache: cache,
		clock: clock,
		log:   log,
		sdk:   newConnectClientFor(t, srv.url),
		raw:   graphv1connect.NewLanternServiceClient(h2cClient(), srv.url),
		repl:  newReplicationRawClient(t, srv.url),
	}
}

// TestSnapshot_E2E_PrimaryToFollower verifies the snapshot bootstrap
// surface (#184): a follower opens Snapshot on a primary populated
// with a mix of vertices and additive edges, replays every frame
// into its own cache via the HLC + ContribID seams, and observes the
// same Illuminate answers as the primary. The header's per-origin
// origin and responder-local cutoffs are asserted at snapshot-open time so a
// downstream Subscribe carrying both +1 cursors is guaranteed to stitch
// cleanly (#415, B-4).
func TestSnapshot_E2E_PrimaryToFollower(t *testing.T) {
	primary := newSnapshotPeer(t, hlc.NodeID{0x01})
	follower := newSnapshotPeer(t, hlc.NodeID{0x02})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Populate primary: 5 vertices, 10 additive-edge calls across 6
	// distinct (tail, head) pairs (some pairs receive 2 contributions).
	const Nv = 5
	for i := 0; i < Nv; i++ {
		if _, err := primary.sdk.PutVertex(ctx, "v-"+itoa(i), "val", time.Minute); err != nil {
			t.Fatalf("primary PutVertex[%d]: %v", i, err)
		}
	}
	edgeWrites := []struct {
		tail, head string
		w          float32
	}{
		{"v-0", "v-1", 0.5},
		{"v-0", "v-2", 0.25},
		{"v-1", "v-2", 1.0},
		{"v-1", "v-2", 0.5}, // second contribution to (v-1,v-2)
		{"v-2", "v-3", 0.75},
		{"v-3", "v-4", 1.0},
		{"v-3", "v-4", 1.0}, // second contribution to (v-3,v-4)
	}
	for i, e := range edgeWrites {
		if _, err := primary.sdk.AddEdge(ctx, e.tail, e.head, e.w, time.Minute); err != nil {
			t.Fatalf("primary AddEdge[%d]: %v", i, err)
		}
	}

	wantSeq, _ := primary.log.LastSeq()

	stream, err := primary.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// First frame MUST be the header.
	if !stream.Receive() {
		t.Fatalf("recv header: %v", stream.Err())
	}
	first := stream.Msg()
	hdr := first.GetHeader()
	if hdr == nil {
		t.Fatalf("first frame is not a header: %T", first.GetEntry())
	}
	// Per-origin cutoff (#415, B-4): primary only saw its own writes,
	// so the map has exactly one entry keyed by primary's NodeID with
	// the latest applied seq for that origin (== primary.log.LastSeq
	// when the log only ever held local writes).
	cutoff := hdr.GetCutoffSeqPerOrigin()
	if len(cutoff) != 1 {
		t.Fatalf("cutoff_seq_per_origin has %d entries, want 1", len(cutoff))
	}
	primaryOriginHex := "01" + strings.Repeat("00", 15)
	if got := cutoff[primaryOriginHex]; got != wantSeq {
		t.Errorf("cutoff_seq_per_origin[%s]=%d want %d", primaryOriginHex, got, wantSeq)
	}
	if hdr.GetCutoffHlc() == nil {
		t.Errorf("cutoff_hlc is nil; expected the primary clock's current Now()")
	}
	if got := hdr.GetCutoffLocalSeq(); got != wantSeq {
		t.Errorf("cutoff_local_seq=%d want %d", got, wantSeq)
	}

	// Stream body: apply each frame to the follower using the HLC +
	// ContribID seams. Footer MUST be the very last frame.
	var (
		gotVertexCount uint64
		gotEdgeCount   uint64
		footer         *pb.SnapshotFooter
	)
	follower.cache.BeginSearchIndexRecovery()
	recovering, err := follower.raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil {
		t.Fatalf("follower GetServerStatus during snapshot: %v", err)
	}
	if got := recovering.Msg.GetSearch().GetIndexStats().GetHealth(); got != pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_INCOMPLETE {
		t.Fatalf("follower search health during snapshot = %v", got)
	}
	for stream.Receive() {
		entry := stream.Msg()
		switch e := entry.GetEntry().(type) {
		case *pb.SnapshotResponse_Vertex:
			if footer != nil {
				t.Fatalf("vertex frame after footer")
			}
			sv := e.Vertex
			v := sv.GetVertex()
			follower.cache.PutVertexWithExpirationHLC(
				v.GetKey(), v, v.GetExpiration().AsTime(),
				snapshotHLC(sv.GetHlc()),
			)
			gotVertexCount++
		case *pb.SnapshotResponse_Edge:
			if footer != nil {
				t.Fatalf("edge frame after footer")
			}
			se := e.Edge
			edgeHLC := snapshotHLC(se.GetHlc())
			for _, c := range se.GetContributions() {
				var cid graphcache.ContribID
				copy(cid[:], c.GetContribId())
				follower.cache.AddEdgeWithExpirationContribHLC(
					se.GetTail(), se.GetHead(), c.GetWeight(),
					c.GetExpiration().AsTime(), cid, edgeHLC,
				)
			}
			gotEdgeCount++
		case *pb.SnapshotResponse_Footer:
			footer = e.Footer
		case *pb.SnapshotResponse_Header:
			t.Fatalf("second header frame mid-stream")
		default:
			t.Fatalf("unknown entry type: %T", e)
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("stream err: %v", err)
	}
	if footer == nil {
		t.Fatalf("stream ended without a footer")
	}
	if footer.GetVertexCount() != gotVertexCount {
		t.Errorf("footer vertex_count=%d, streamed=%d", footer.GetVertexCount(), gotVertexCount)
	}
	if footer.GetEdgeCount() != gotEdgeCount {
		t.Errorf("footer edge_count=%d, streamed=%d", footer.GetEdgeCount(), gotEdgeCount)
	}
	if got := uint64(Nv); footer.GetVertexCount() != got {
		t.Errorf("vertex_count=%d want %d", footer.GetVertexCount(), got)
	}
	// 5 distinct (tail, head) pairs in edgeWrites.
	if got := uint64(5); footer.GetEdgeCount() != got {
		t.Errorf("edge_count=%d want %d", footer.GetEdgeCount(), got)
	}
	if err := follower.cache.CompleteSearchIndexRecovery(); err != nil {
		t.Fatalf("follower CompleteSearchIndexRecovery: %v", err)
	}

	// Verify convergence: every (tail, head) pair has the same weight
	// on both peers and every vertex round-trips.
	for _, e := range []struct {
		tail, head string
	}{
		{"v-0", "v-1"}, {"v-0", "v-2"}, {"v-1", "v-2"},
		{"v-2", "v-3"}, {"v-3", "v-4"},
	} {
		pw, pok := primary.cache.GetWeight(e.tail, e.head)
		fw, fok := follower.cache.GetWeight(e.tail, e.head)
		if pok != fok {
			t.Errorf("edge (%s,%s) presence mismatch: primary=%v follower=%v", e.tail, e.head, pok, fok)
		}
		if pw != fw {
			t.Errorf("edge (%s,%s) weight mismatch: primary=%v follower=%v", e.tail, e.head, pw, fw)
		}
	}
	for i := 0; i < Nv; i++ {
		key := "v-" + itoa(i)
		_, pok := primary.cache.GetVertex(key)
		_, fok := follower.cache.GetVertex(key)
		if !pok || !fok {
			t.Errorf("vertex %s presence mismatch: primary=%v follower=%v", key, pok, fok)
		}
	}
	waitForSearchConvergence(t, ctx, "val", nil, primary.raw, follower.raw)
	healthy, err := follower.raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil {
		t.Fatalf("follower GetServerStatus after snapshot: %v", err)
	}
	if got := healthy.Msg.GetSearch().GetIndexStats().GetHealth(); got != pb.SearchIndexHealth_SEARCH_INDEX_HEALTH_HEALTHY {
		t.Fatalf("follower search health after snapshot = %v", got)
	}
}

// TestSnapshot_E2E_AcceptedExpiredCausalBarriers proves that replication
// bootstrap carries delete-like HLC Put outcomes even though they have no live
// Vertex/Edge payload. The source emits explicit causal-barrier frames and the
// follower replays them through the dedicated no-materialization seams; a
// delayed cross-origin HLC10 live Put remains absent behind the retained HLC20
// floor.
func TestSnapshot_E2E_AcceptedExpiredCausalBarriers(t *testing.T) {
	primary := newSnapshotPeer(t, hlc.NodeID{0x31})
	follower := newSnapshotPeer(t, hlc.NodeID{0x32})
	newer := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0x20}}
	older := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0x10}}
	expired := time.Now().Add(-time.Hour)
	live := time.Now().Add(time.Hour)

	if !primary.cache.PutVertexWithExpirationHLC(
		"barrier-vertex", &pb.Vertex{Key: "barrier-vertex"}, expired, newer,
	) {
		t.Fatal("primary accepted-expired vertex Put was rejected")
	}
	if !primary.cache.PutEdgeWithExpirationHLC("barrier-tail", "barrier-head", 2, expired, newer) {
		t.Fatal("primary accepted-expired edge Put was rejected")
	}
	if primary.cache.VertexCount() != 0 || primary.cache.EdgeCount() != 0 {
		t.Fatalf("primary materialized barrier state: vertices=%d edges=%d", primary.cache.VertexCount(), primary.cache.EdgeCount())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := primary.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var vertexMarkers, edgeMarkers uint64
	var footer *pb.SnapshotFooter
	for stream.Receive() {
		entry := stream.Msg()
		switch e := entry.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			// No mutation-log writes were needed to seed this storage-level
			// bootstrap fixture; the header is still mandatory framing.
		case *pb.SnapshotResponse_VertexCausalBarrier:
			barrier := e.VertexCausalBarrier
			if barrier == nil || barrier.GetKey() != "barrier-vertex" {
				t.Fatalf("vertex barrier frame = %+v", barrier)
			}
			follower.cache.ApplyVertexCausalBarrierHLC(barrier.GetKey(), snapshotHLC(barrier.GetHlc()))
			vertexMarkers++
		case *pb.SnapshotResponse_EdgeCausalBarrier:
			barrier := e.EdgeCausalBarrier
			if barrier == nil || barrier.GetTail() != "barrier-tail" || barrier.GetHead() != "barrier-head" {
				t.Fatalf("edge barrier frame = %+v", barrier)
			}
			follower.cache.ApplyEdgeCausalBarrierHLC(
				barrier.GetTail(), barrier.GetHead(), snapshotHLC(barrier.GetHlc()),
			)
			edgeMarkers++
		case *pb.SnapshotResponse_Vertex, *pb.SnapshotResponse_Edge:
			t.Fatalf("accepted-expired-only snapshot emitted live payload: %T", e)
		case *pb.SnapshotResponse_Footer:
			footer = e.Footer
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("snapshot stream: %v", err)
	}
	if vertexMarkers != 1 || edgeMarkers != 1 {
		t.Fatalf("barrier markers vertex=%d edge=%d, want 1/1", vertexMarkers, edgeMarkers)
	}
	if footer == nil || footer.GetVertexCount() != 0 || footer.GetEdgeCount() != 0 ||
		footer.GetVertexCausalBarrierCount() != 1 || footer.GetEdgeCausalBarrierCount() != 1 {
		t.Fatalf("footer = %+v, want live 0/0 and barrier 1/1", footer)
	}

	if follower.cache.PutVertexWithExpirationHLC(
		"barrier-vertex", &pb.Vertex{Key: "barrier-vertex"}, live, older,
	) {
		t.Fatal("follower accepted cross-origin HLC10 vertex after HLC20 barrier bootstrap")
	}
	if follower.cache.PutEdgeWithExpirationHLC("barrier-tail", "barrier-head", 9, live, older) {
		t.Fatal("follower accepted cross-origin HLC10 edge after HLC20 barrier bootstrap")
	}
	if _, ok := follower.cache.GetVertex("barrier-vertex"); ok {
		t.Fatal("barrier vertex resurrected after bootstrap")
	}
	if _, _, ok := follower.cache.GetEdgeDetail("barrier-tail", "barrier-head"); ok {
		t.Fatal("barrier edge resurrected after bootstrap")
	}
	if _, ok := follower.cache.GetVertex("barrier-tail"); ok {
		t.Fatal("barrier bootstrap materialized edge endpoints")
	}
	searchResp, err := follower.raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "barrier"}))
	if err != nil {
		t.Fatalf("SearchVertices after barrier bootstrap: %v", err)
	}
	if len(searchResp.Msg.GetHits()) != 0 {
		t.Fatalf("barrier marker became searchable: %+v", searchResp.Msg.GetHits())
	}
}

// snapshotHLC converts a wire HLCTimestamp into the in-process
// hlc.Timestamp value. Mirrors server/service.hlcFromProto, duplicated
// here because that helper is package-internal to server/service.
func snapshotHLC(p *pb.HLCTimestamp) hlc.Timestamp {
	if p == nil {
		return hlc.Timestamp{}
	}
	var nid hlc.NodeID
	copy(nid[:], p.GetNodeId())
	return hlc.Timestamp{
		WallNs:  p.GetWallNs(),
		Logical: p.GetLogical(),
		NodeID:  nid,
	}
}
