package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
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
	cache       *graphcache.GraphCache[string, *pb.Vertex]
	clock       *hlc.Clock
	log         *mutationlog.Log
	service     *service.LanternService
	replication *service.LanternReplicationService
	sdk         *client.Lantern
	raw         graphv1connect.LanternServiceClient
	repl        graphv1connect.LanternReplicationServiceClient
}

func newSnapshotPeer(t *testing.T, nodeID hlc.NodeID) *snapshotPeer {
	return newSnapshotPeerWithMode(t, nodeID, 1024, false)
}

func newSnapshotPeerWithMode(t *testing.T, nodeID hlc.NodeID, logCapacity int, receiptSnapshotRequired bool) *snapshotPeer {
	t.Helper()
	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})

	log := mutationlog.New(mutationlog.Options{Capacity: logCapacity, SubscriberBuffer: 1024})
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
	if receiptSnapshotRequired {
		svc.WithTombstoneTTL(time.Hour)
		rep.WithReceiptSnapshotRequired()
	}
	srv := newConnectTestServer(t, svc, rep, vi.ConnectInterceptor())

	return &snapshotPeer{
		cache:       cache,
		clock:       clock,
		log:         log,
		service:     svc,
		replication: rep,
		sdk:         newConnectClientFor(t, srv.url),
		raw:         graphv1connect.NewLanternServiceClient(h2cClient(), srv.url),
		repl:        newReplicationRawClient(t, srv.url),
	}
}

// A Snapshot install can fault a standalone service that has no mutation log.
// The Connect graph-read adapter must honor that fault for both point reads
// and the optimistic paths that build a larger response.
func TestSnapshotInstallFaultGatesLoglessGraphReadsOnRealWire(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	if err := cache.PutVertex("cut", &pb.Vertex{Key: "cut"}); err != nil {
		t.Fatal(err)
	}
	svc := service.NewLanternService(cache)
	srv := newConnectTestServer(t, svc, nil)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reads := []struct {
		name string
		call func() (bool, error)
	}{
		{"GetVertex", func() (bool, error) {
			resp, err := raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cut"}))
			return err == nil && resp.Msg.GetVertex().GetKey() == "cut", err
		}},
		{"GetVertices", func() (bool, error) {
			resp, err := raw.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{Keys: []string{"cut", "missing"}}))
			return err == nil && len(resp.Msg.GetVertices()) == 1 && resp.Msg.GetVertices()[0].GetKey() == "cut", err
		}},
	}
	checkReads := func(phase string, wantFault bool) {
		t.Helper()
		for _, read := range reads {
			t.Run(phase+"/"+read.name, func(t *testing.T) {
				found, err := read.call()
				if wantFault {
					if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
						t.Fatalf("read during Snapshot fault = %v, want gapped FailedPrecondition", err)
					}
				} else if err != nil || !found {
					t.Fatalf("healthy read = (found=%v, err=%v), want seeded vertex", found, err)
				}
			})
		}
	}
	checkReads("before", false)
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	checkReads("installing", true)
	finish(false)
	checkReads("incomplete", true)
	finish, err = svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	finish(true)
	checkReads("recovered", false)
}

// TestSnapshotFormatNegotiation_RealConnectWire pins both sides of the
// receipt-continuity boundary. A graph-only responder advertises and serves
// only its graph format; a future receipt-enabled responder cannot let an old
// peer recover an evicted receipt through a graph-only Snapshot.
func TestSnapshotFormatNegotiation_RealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	graphPeer := newSnapshotPeer(t, hlc.NodeID{0x31})
	status, err := graphPeer.repl.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
	if err != nil || status.Msg.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 {
		t.Fatalf("graph-only PeerStatus = (%v, %v)", status, err)
	}
	graphStream, err := graphPeer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = graphStream.Close() }()
	if !graphStream.Receive() || graphStream.Msg().GetHeader().GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 {
		t.Fatalf("graph Snapshot header = (%v, %v)", graphStream.Msg(), graphStream.Err())
	}
	for _, required := range []pb.SnapshotFormat{pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1, pb.SnapshotFormat(99)} {
		stream, err := graphPeer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{RequiredFormat: required}))
		if err == nil {
			defer func() { _ = stream.Close() }()
			if stream.Receive() {
				t.Fatalf("unsupported format %v sent a frame", required)
			}
			err = stream.Err()
		}
		want := connect.CodeFailedPrecondition
		if required == pb.SnapshotFormat(99) {
			want = connect.CodeInvalidArgument
		}
		if connect.CodeOf(err) != want {
			t.Fatalf("unsupported format %v = %v, want %v", required, err, want)
		}
	}

	// Production receipt writes are still disabled. Inject one receipt-bearing
	// wire mutation into the test log, then evict it with two ordinary writes.
	// The static mode gate must reject legacy clients before inspecting the
	// now graph-only retained ring.
	receiptPeer := newSnapshotPeerWithMode(t, hlc.NodeID{0x32}, 2, true)
	receiptWire, stamp := receiptEdgeDeleteTailFixture(t, hlc.NodeID{0x33}, 1, []bool{false})
	if _, err := receiptPeer.log.Append(receiptWire, stamp); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := receiptPeer.sdk.PutVertex(ctx, "receipt-mode-"+itoa(i), i, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	retained := receiptPeer.log.RetainedEntries()
	if len(retained) != 2 || retained[0].Seq != 2 || retained[1].Seq != 3 {
		t.Fatalf("receipt fixture was not evicted: %+v", retained)
	}
	status, err = receiptPeer.repl.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
	if err != nil || status.Msg.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 {
		t.Fatalf("receipt-mode PeerStatus = (%v, %v)", status, err)
	}
	legacyTail, err := receiptPeer.repl.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{}))
	if err == nil {
		defer func() { _ = legacyTail.Close() }()
		if legacyTail.Receive() {
			t.Fatal("legacy full Subscribe emitted an entry in receipt mode")
		}
		err = legacyTail.Err()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("legacy full Subscribe = %v, want InvalidArgument", err)
	}
	legacySnapshot, err := receiptPeer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err == nil {
		defer func() { _ = legacySnapshot.Close() }()
		if legacySnapshot.Receive() {
			t.Fatal("legacy Snapshot emitted a graph-only frame in receipt mode")
		}
		err = legacySnapshot.Err()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("legacy Snapshot = %v, want FailedPrecondition", err)
	}
}

func TestReceiptSnapshotProducer_RealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	peer := newSnapshotPeerWithMode(t, hlc.NodeID{0x41}, 1024, true)
	epoch := mutationreceipt.Epoch{0x51}
	policy := mutationreceipt.Config{
		Epoch:      epoch,
		Retention:  time.Hour,
		MaxEntries: 16,
		MaxBytes:   1 << 20,
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Second)
	id, err := mutationreceipt.NewID(epoch, issued, [24]byte{0x52})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID:     id,
		Group:  mutationreceipt.GroupID{0x53},
		Count:  1,
		Kind:   mutationreceipt.PutVertex,
		Digest: mutationreceipt.IntentDigest([]byte("wire-receipt")),
	}
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if class, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil || class != mutationreceipt.Fresh {
		t.Fatalf("Classify = (%v, %v), want fresh", class, err)
	}
	originalResult := []byte{0x7a, 0x01}
	if err := tx.Reserve([][]byte{originalResult}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()

	if _, err := peer.sdk.PutVertex(ctx, "receipt-wire", "value", time.Minute); err != nil {
		t.Fatal(err)
	}
	source, err := service.NewReceiptWholeStateSource(peer.service, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.replication.ConfigureReceiptSnapshot(source, policy); err != nil {
		t.Fatal(err)
	}

	stream, err := peer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var frames []*pb.SnapshotResponse
	for stream.Receive() {
		frames = append(frames, proto.Clone(stream.Msg()).(*pb.SnapshotResponse))
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 4 {
		t.Fatalf("receipt Snapshot frames = %d, want header/receipt/vertex/footer", len(frames))
	}
	header := frames[0].GetHeader()
	receipt := frames[1].GetReceipt()
	vertex := frames[2].GetVertex()
	footer := frames[3].GetFooter()
	fingerprint := store.PolicyFingerprint()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		header.GetCutoffLocalSeq() != 1 ||
		header.GetReceiptMetadata().GetPolicy().GetRetentionMs() != uint64(time.Hour/time.Millisecond) ||
		header.GetReceiptMetadata().GetPolicy().GetMaxEntries() != 16 ||
		header.GetReceiptMetadata().GetPolicy().GetMaxBytes() != 1<<20 ||
		!bytes.Equal(header.GetReceiptMetadata().GetPolicy().GetDeploymentEpoch(), epoch[:]) ||
		!bytes.Equal(header.GetReceiptMetadata().GetPolicy().GetFingerprint(), fingerprint[:]) ||
		len(header.GetReceiptMetadata().GetOriginCutoffs()) != 1 {
		t.Fatalf("receipt Snapshot header = %+v", header)
	}
	if receipt.GetKind() != pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_PUT_VERTEX ||
		!bytes.Equal(receipt.GetOperationId(), id.Bytes()) ||
		!bytes.Equal(receipt.GetOriginalResult(), originalResult) ||
		receipt.GetContribution() != nil {
		t.Fatalf("receipt Snapshot row = %+v", receipt)
	}
	if vertex.GetVertex().GetKey() != "receipt-wire" {
		t.Fatalf("receipt Snapshot graph row = %+v", vertex)
	}
	if footer.GetReceiptCount() != 1 || footer.GetReceiptOriginCount() != 1 ||
		footer.GetVertexCount() != 1 || footer.GetEdgeCount() != 0 {
		t.Fatalf("receipt Snapshot footer = %+v", footer)
	}

	downgrade, err := peer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
	}))
	if err == nil {
		defer func() { _ = downgrade.Close() }()
		if downgrade.Receive() {
			t.Fatalf("graph-only downgrade emitted frame: %+v", downgrade.Msg())
		}
		err = downgrade.Err()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("graph-only downgrade = %v, want FailedPrecondition", err)
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
				if cid.IsZero() {
					follower.cache.PutEdgeWithExpirationHLC(
						se.GetTail(), se.GetHead(), c.GetWeight(), c.GetExpiration().AsTime(), edgeHLC,
					)
					continue
				}
				if c.GetHlc() == nil {
					t.Fatal("snapshot Add contribution omitted its HLC")
				}
				follower.cache.AddEdgeWithExpirationContribHLC(
					se.GetTail(), se.GetHead(), c.GetWeight(),
					c.GetExpiration().AsTime(), cid, snapshotHLC(c.GetHlc()),
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

// TestSnapshot_E2E_MixedEdgeResetAndAdd keeps the original Add HLC across
// the real Snapshot wire, then replays the same snapshot twice. A Put base
// and a Delete floor both coexist with later Add rows; older Add rows remain
// rejected after bootstrap.
func TestSnapshot_E2E_MixedEdgeResetAndAdd(t *testing.T) {
	primary := newSnapshotPeer(t, hlc.NodeID{0x41})
	follower := newSnapshotPeer(t, hlc.NodeID{0x42})
	node := hlc.NodeID{0x43}
	stamp := func(wall int64) hlc.Timestamp { return hlc.Timestamp{WallNs: wall, NodeID: node} }
	exp := time.Now().Add(time.Hour)
	id := func(b byte) graphcache.ContribID { var out graphcache.ContribID; out[0] = b; return out }

	if !primary.cache.PutEdgeWithExpirationHLC("put-tail", "put-head", 5, exp, stamp(20)) ||
		!primary.cache.AddEdgeWithExpirationContribHLC("put-tail", "put-head", 3, exp, id(1), stamp(30)) {
		t.Fatal("could not seed Put followed by Add")
	}
	if !primary.cache.AddEdgeWithExpirationContribHLC("delete-tail", "delete-head", 7, exp, id(2), stamp(10)) {
		t.Fatal("could not seed old Add")
	}
	primary.cache.DeleteEdgeHLC("delete-tail", "delete-head", stamp(20), exp)
	if !primary.cache.AddEdgeWithExpirationContribHLC("delete-tail", "delete-head", 3, exp, id(3), stamp(30)) {
		t.Fatal("could not seed Add after Delete")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for pass := 0; pass < 2; pass++ {
		stream, err := primary.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
		if err != nil {
			t.Fatalf("Snapshot pass %d: %v", pass, err)
		}
		var addRows, tombstones int
		for stream.Receive() {
			switch entry := stream.Msg().GetEntry().(type) {
			case *pb.SnapshotResponse_Vertex:
				v := entry.Vertex.GetVertex()
				follower.cache.PutVertexWithExpirationHLC(v.GetKey(), v, v.GetExpiration().AsTime(), snapshotHLC(entry.Vertex.GetHlc()))
			case *pb.SnapshotResponse_EdgeTombstone:
				m := entry.EdgeTombstone
				follower.cache.ApplySnapshotEdgeTombstoneHLC(m.GetTail(), m.GetHead(), snapshotHLC(m.GetHlc()), m.GetExpiration().AsTime())
				tombstones++
			case *pb.SnapshotResponse_Edge:
				e := entry.Edge
				for _, contribution := range e.GetContributions() {
					var cid graphcache.ContribID
					copy(cid[:], contribution.GetContribId())
					if cid.IsZero() {
						follower.cache.PutEdgeWithExpirationHLC(e.GetTail(), e.GetHead(), contribution.GetWeight(), contribution.GetExpiration().AsTime(), snapshotHLC(e.GetHlc()))
						continue
					}
					if contribution.GetHlc() == nil {
						t.Fatal("Snapshot Add row lost its HLC")
					}
					follower.cache.AddEdgeWithExpirationContribHLC(e.GetTail(), e.GetHead(), contribution.GetWeight(), contribution.GetExpiration().AsTime(), cid, snapshotHLC(contribution.GetHlc()))
					addRows++
				}
			}
		}
		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("Snapshot pass %d stream: %v", pass, err)
		}
		_ = stream.Close()
		if addRows != 2 || tombstones != 1 {
			t.Fatalf("Snapshot pass %d Add rows=%d tombstones=%d, want 2/1", pass, addRows, tombstones)
		}
		for _, edge := range []struct {
			tail, head string
			weight     float32
		}{
			{"put-tail", "put-head", 8}, {"delete-tail", "delete-head", 3},
		} {
			if got, ok := follower.cache.GetWeight(edge.tail, edge.head); !ok || got != edge.weight {
				t.Errorf("Snapshot pass %d %s->%s weight=%g/%v, want %g", pass, edge.tail, edge.head, got, ok, edge.weight)
			}
		}
	}
	if follower.cache.AddEdgeWithExpirationContribHLC("delete-tail", "delete-head", 7, exp, id(4), stamp(10)) {
		t.Fatal("older Add crossed the replayed Delete floor")
	}
	if follower.cache.AddEdgeWithExpirationContribHLC("put-tail", "put-head", 7, exp, id(5), stamp(10)) {
		t.Fatal("older Add crossed the replayed Put floor")
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
