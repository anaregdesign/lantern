package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	"github.com/anaregdesign/lantern/server/backup"
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
// only its graph format; a receipt-enabled responder cannot let a peer recover
// an evicted receipt through a graph-only Snapshot.
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
	for _, required := range []pb.SnapshotFormat{pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT, pb.SnapshotFormat(99)} {
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
	// The static mode gate must reject non-opted-in clients before inspecting the
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
	if err != nil || status.Msg.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT {
		t.Fatalf("receipt-mode PeerStatus = (%v, %v)", status, err)
	}
	unoptedTail, err := receiptPeer.repl.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{}))
	if err == nil {
		defer func() { _ = unoptedTail.Close() }()
		if unoptedTail.Receive() {
			t.Fatal("receipt-less full Subscribe emitted an entry in receipt mode")
		}
		err = unoptedTail.Err()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("receipt-less full Subscribe = %v, want InvalidArgument", err)
	}
	unspecifiedSnapshot, err := receiptPeer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err == nil {
		defer func() { _ = unspecifiedSnapshot.Close() }()
		if unspecifiedSnapshot.Receive() {
			t.Fatal("unspecified Snapshot emitted a graph-only frame in receipt mode")
		}
		err = unspecifiedSnapshot.Err()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unspecified Snapshot = %v, want FailedPrecondition", err)
	}
}

func TestReceiptSnapshotProducer_RealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config := durableReceiptWireConfig(
		filepath.Join(t.TempDir(), "receipts.wal"),
		hlc.NodeID{0x41},
	)
	epoch := mutationreceipt.Epoch{0x51}
	config.Receipt.Epoch = epoch
	config.Receipt.MaxEntries = 16
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	server, sdk := mountDurableReceiptWireRuntime(t, runtime)
	defer closeDurableReceiptWireRuntime(t, server, sdk, runtime)
	repl := newReplicationRawClient(t, server.url)

	if _, err := sdk.PutVertex(ctx, "receipt-wire", "value", time.Minute); err != nil {
		t.Fatal(err)
	}
	source, policy, err := runtime.ReceiptWholeStateBackupSource(server.svc, server.rep)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.Capture(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.ClockHighWater = capture.Receipts.ClockHighWater()
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	issued := capture.Receipts.ClockHighWater()
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
	activeState, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	retiredEpoch := mutationreceipt.Epoch{0x41}
	retiredPolicy := mutationreceipt.Config{
		Epoch: retiredEpoch, Retention: 2 * time.Hour,
		MaxEntries: 16, MaxBytes: 1 << 20,
		ClockHighWater: activeState.ClockHighWater(),
	}
	retiredStore, err := mutationreceipt.New(retiredPolicy)
	if err != nil {
		t.Fatal(err)
	}
	retiredIssued := issued.Add(-time.Second)
	retiredID, err := mutationreceipt.NewID(retiredEpoch, retiredIssued, [24]byte{0x42})
	if err != nil {
		t.Fatal(err)
	}
	retiredIntent := mutationreceipt.Intent{
		ID: retiredID, Group: mutationreceipt.GroupID{0x43}, Count: 1,
		Kind:   mutationreceipt.PutVertex,
		Digest: mutationreceipt.IntentDigest([]byte("wire-retired-receipt")),
	}
	retiredTx, err := retiredStore.Begin(retiredIssued)
	if err != nil {
		t.Fatal(err)
	}
	defer retiredTx.Abort()
	if class, _, err := retiredTx.Classify([]mutationreceipt.Intent{retiredIntent}); err != nil ||
		class != mutationreceipt.Fresh {
		t.Fatalf("retired Classify = (%v, %v), want fresh", class, err)
	}
	if err := retiredTx.Reserve([][]byte{{0x41}}); err != nil {
		t.Fatal(err)
	}
	if err := retiredTx.Stage(); err != nil {
		t.Fatal(err)
	}
	retiredTx.Commit()
	retiredState, err := retiredStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	retiredConfig := mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch: epoch, MaxEntries: 16, MaxBytes: 1 << 20,
		ClockHighWater: activeState.ClockHighWater(),
	}
	retiredSnapshot := mutationreceipt.RetiredCatalogSnapshot{
		Version:              1,
		ClockHighWaterMillis: activeState.ClockHighWaterMillis,
		Epochs: []mutationreceipt.RetiredEpochSnapshot{{
			Policy: mutationreceipt.RetiredEpochPolicy{
				Epoch: retiredEpoch, Retention: retiredPolicy.Retention,
				MaxEntries: retiredPolicy.MaxEntries, MaxBytes: retiredPolicy.MaxBytes,
			},
			State: retiredState,
		}},
	}
	if _, err := mutationreceipt.NewRetiredCatalogFromSnapshot(
		retiredConfig,
		retiredSnapshot,
	); err != nil {
		t.Fatal(err)
	}
	capture.Policy = policy
	capture.Receipts = activeState
	capture.Retired = retiredSnapshot
	if err := server.svc.InstallReceiptBaseline(ctx, capture); err != nil {
		t.Fatal(err)
	}

	stream, err := repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
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
	if len(frames) != 5 {
		t.Fatalf("receipt Snapshot frames = %d, want header/two receipts/vertex/footer", len(frames))
	}
	header := frames[0].GetHeader()
	retiredReceipt := frames[1].GetReceipt()
	receipt := frames[2].GetReceipt()
	vertex := frames[3].GetVertex()
	footer := frames[4].GetFooter()
	fingerprint := store.PolicyFingerprint()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT ||
		header.GetCutoffLocalSeq() != 2 ||
		header.GetReceiptMetadata().GetActivePolicy().GetRetentionMs() != uint64(time.Hour/time.Millisecond) ||
		header.GetReceiptMetadata().GetActivePolicy().GetMaxEntries() != 16 ||
		header.GetReceiptMetadata().GetActivePolicy().GetMaxBytes() != 1<<20 ||
		!bytes.Equal(header.GetReceiptMetadata().GetActivePolicy().GetDeploymentEpoch(), epoch[:]) ||
		!bytes.Equal(header.GetReceiptMetadata().GetActivePolicy().GetFingerprint(), fingerprint[:]) ||
		len(header.GetReceiptMetadata().GetRetiredPolicies()) != 1 ||
		!bytes.Equal(
			header.GetReceiptMetadata().GetRetiredPolicies()[0].GetDeploymentEpoch(),
			retiredEpoch[:],
		) ||
		len(header.GetReceiptMetadata().GetOriginCutoffs()) != 1 {
		t.Fatalf("receipt Snapshot header = %+v", header)
	}
	if !bytes.Equal(retiredReceipt.GetOperationId(), retiredID.Bytes()) ||
		retiredReceipt.GetKind() != pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_PUT_VERTEX ||
		!bytes.Equal(retiredReceipt.GetOriginalResult(), []byte{0x41}) {
		t.Fatalf("retired receipt Snapshot row = %+v", retiredReceipt)
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
	if footer.GetActiveReceiptCount() != 1 || footer.GetOriginCount() != 1 ||
		footer.GetRetiredEpochCount() != 1 || footer.GetRetiredReceiptCount() != 1 ||
		footer.GetVertexCount() != 1 || footer.GetEdgeCount() != 0 {
		t.Fatalf("receipt Snapshot footer = %+v", footer)
	}

	downgrade, err := repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
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

type scriptedReceiptSnapshotService struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	frames []*pb.SnapshotResponse
	err    error
}

func (s scriptedReceiptSnapshotService) Snapshot(
	ctx context.Context,
	_ *connect.Request[pb.SnapshotRequest],
	stream *connect.ServerStream[pb.SnapshotResponse],
) error {
	for _, frame := range s.frames {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := stream.Send(proto.Clone(frame).(*pb.SnapshotResponse)); err != nil {
			return err
		}
	}
	return s.err
}

func newScriptedReceiptSnapshotClient(
	t *testing.T,
	frames []*pb.SnapshotResponse,
	err error,
) graphv1connect.LanternReplicationServiceClient {
	t.Helper()
	path, handler := graphv1connect.NewLanternReplicationServiceHandler(
		scriptedReceiptSnapshotService{frames: frames, err: err},
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	return graphv1connect.NewLanternReplicationServiceClient(h2cClient(), server.URL)
}

func receiptSnapshotIntegrationCollector(
	t *testing.T,
	dir string,
	policy mutationreceipt.Config,
) *backup.ReceiptSnapshotCollector {
	t.Helper()
	collector, err := backup.NewReceiptSnapshotCollector(backup.ReceiptSnapshotCollectorConfig{
		TempDir: dir,
		Limits: backup.ReceiptSnapshotCollectorLimits{
			MaxFrameBytes:      1 << 20,
			MaxFrames:          64,
			MaxTotalBytes:      4 << 20,
			MaxActiveReceipts:  16,
			MaxRetiredEpochs:   16,
			MaxRetiredReceipts: 16,
			MaxOrigins:         16,
			MaxGraphFrames:     32,
		},
		ExpectedPolicy: policy,
		ExpectedRetiredConfig: mutationreceipt.RetiredCatalogConfig{
			ActiveEpoch: policy.Epoch,
			MaxEntries:  policy.MaxEntries,
			MaxBytes:    policy.MaxBytes,
		},
		DefaultTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return collector
}

func assertReceiptSnapshotIntegrationTempDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("receipt Snapshot collector left temporary artifacts: %+v", entries)
	}
}

func TestReceiptSnapshotCollector_RealConnectWireDetachedAndFailClosed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	peer := newSnapshotPeerWithMode(t, hlc.NodeID{0x61}, 1024, true)
	policy := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x62}, Retention: time.Hour,
		MaxEntries: 16, MaxBytes: 1 << 20,
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Second)
	id, err := mutationreceipt.NewID(policy.Epoch, issued, [24]byte{0x63})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID: id, Group: mutationreceipt.GroupID{0x64}, Count: 1,
		Kind: mutationreceipt.PutVertex, Digest: mutationreceipt.IntentDigest([]byte("collector-wire")),
	}
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if class, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil ||
		class != mutationreceipt.Fresh {
		t.Fatalf("Classify = (%v, %v)", class, err)
	}
	if err := tx.Reserve([][]byte{{0xca, 0xfe}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if _, err := peer.sdk.PutVertex(ctx, "collector-live", "value", time.Minute); err != nil {
		t.Fatal(err)
	}
	source, err := service.NewReceiptWholeStateSource(peer.service, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.replication.ConfigureReceiptSnapshot(source, policy); err != nil {
		t.Fatal(err)
	}
	beforeStore, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	beforeGraph := peer.cache.SnapshotReplication()

	stream, err := peer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	dir := t.TempDir()
	candidate, err := receiptSnapshotIntegrationCollector(t, dir, policy).Collect(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	metadata := candidate.Metadata()
	var canonical bytes.Buffer
	if err := candidate.WriteSpool(&canonical); err != nil {
		t.Fatal(err)
	}
	if metadata.Header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT ||
		metadata.Header.GetCutoffLocalSeq() != 1 ||
		metadata.SpoolBytes != uint64(canonical.Len()) ||
		metadata.SpoolSHA256 != sha256.Sum256(canonical.Bytes()) {
		t.Fatalf("collected candidate metadata = %+v", metadata)
	}
	if err := candidate.Close(); err != nil {
		t.Fatal(err)
	}
	assertReceiptSnapshotIntegrationTempDirEmpty(t, dir)

	validStream, err := peer.repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var validFrames []*pb.SnapshotResponse
	for validStream.Receive() {
		validFrames = append(validFrames, proto.Clone(validStream.Msg()).(*pb.SnapshotResponse))
	}
	if err := validStream.Err(); err != nil {
		t.Fatal(err)
	}
	_ = validStream.Close()

	tests := []struct {
		name   string
		frames func() []*pb.SnapshotResponse
	}{
		{"truncated", func() []*pb.SnapshotResponse {
			return validFrames[:len(validFrames)-1]
		}},
		{"duplicate header", func() []*pb.SnapshotResponse {
			return append([]*pb.SnapshotResponse{
				validFrames[0], proto.Clone(validFrames[0]).(*pb.SnapshotResponse),
			}, validFrames[1:]...)
		}},
		{"format downgrade", func() []*pb.SnapshotResponse {
			frames := make([]*pb.SnapshotResponse, len(validFrames))
			for i, frame := range validFrames {
				frames[i] = proto.Clone(frame).(*pb.SnapshotResponse)
			}
			frames[0].GetHeader().Format = pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
			return frames
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newScriptedReceiptSnapshotClient(t, test.frames(), nil)
			stream, err := client.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
				RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT,
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			dir := t.TempDir()
			candidate, err := receiptSnapshotIntegrationCollector(t, dir, policy).Collect(ctx, stream)
			if err == nil || candidate != nil {
				if candidate != nil {
					_ = candidate.Close()
				}
				t.Fatalf("invalid real-wire stream returned candidate=%p, err=%v", candidate, err)
			}
			assertReceiptSnapshotIntegrationTempDirEmpty(t, dir)
		})
	}

	afterStore, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	afterGraph := peer.cache.SnapshotReplication()
	if !reflect.DeepEqual(beforeStore, afterStore) ||
		!reflect.DeepEqual(beforeGraph, afterGraph) {
		t.Fatal("collector changed the producer's live graph or receipt Store")
	}
	if vertex, ok := peer.cache.GetVertex("collector-live"); !ok || vertex.GetString_() != "value" {
		t.Fatalf("producer live vertex changed: %+v, %t", vertex, ok)
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
