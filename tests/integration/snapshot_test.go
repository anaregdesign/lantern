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
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock).
		WithOriginStates(svc)
	srv := newConnectTestServer(t, svc, rep, vi.ConnectInterceptor())

	return &snapshotPeer{
		cache: cache,
		clock: clock,
		log:   log,
		sdk:   newConnectClientFor(t, srv.url),
		repl:  newReplicationRawClient(t, srv.url),
	}
}

// TestSnapshot_E2E_PrimaryToFollower verifies the snapshot bootstrap
// surface (#184): a follower opens Snapshot on a primary populated
// with a mix of vertices and additive edges, replays every frame
// into its own cache via the HLC + ContribID seams, and observes the
// same Illuminate answers as the primary. The header's per-origin
// cutoff is also asserted to equal the primary's local writes
// watermark at snapshot-open time so that downstream
// `Subscribe(from_seq_per_origin = {origin: seq+1})` is guaranteed
// to stitch cleanly (#415, B-4).
func TestSnapshot_E2E_PrimaryToFollower(t *testing.T) {
	primary := newSnapshotPeer(t, hlc.NodeID{0x01})
	follower := newSnapshotPeer(t, hlc.NodeID{0x02})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Populate primary: 5 vertices, 10 additive-edge calls across 6
	// distinct (tail, head) pairs (some pairs receive 2 contributions).
	const Nv = 5
	for i := 0; i < Nv; i++ {
		if err := primary.sdk.PutVertex(ctx, "v-"+itoa(i), "val", time.Minute); err != nil {
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

	// Stream body: apply each frame to the follower using the HLC +
	// ContribID seams. Footer MUST be the very last frame.
	var (
		gotVertexCount uint64
		gotEdgeCount   uint64
		footer         *pb.SnapshotFooter
	)
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
