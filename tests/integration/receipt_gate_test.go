package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func authedReceiptRequest[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+testToken)
	return req
}

// TestReceiptReadSurface_RealConnectWire keeps the dormant capability/status
// surface honest: authentication precedes receipt inspection, and a server
// without an atomic receipt engine cannot fabricate an absent result.
func TestReceiptReadSurface_RealConnectWire(t *testing.T) {
	srv, _, _ := newAuthedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	if _, err := raw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless capability = %v, want Unauthenticated", err)
	}
	if _, err := raw.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless status = %v, want Unauthenticated", err)
	}

	capability, err := raw.GetReceiptCapability(ctx, authedReceiptRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || capability.Msg.GetEnabled() || capability.Msg.GetPolicy() != nil || capability.Msg.GetEndpoint() != nil || capability.Msg.GetServerNowUnixMs() != 0 {
		t.Fatalf("authenticated disabled capability = (%v, %v)", capability, err)
	}
	operationID := make([]byte, 49)
	for i := range operationID {
		operationID[i] = byte(i + 1)
	}
	if _, err := raw.GetReceiptStatus(ctx, authedReceiptRequest(&pb.GetReceiptStatusRequest{OperationId: operationID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled singular status = %v, want FailedPrecondition", err)
	}
	if _, err := raw.GetReceiptStatuses(ctx, authedReceiptRequest(&pb.GetReceiptStatusesRequest{OperationIds: [][]byte{operationID}})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled plural status = %v, want FailedPrecondition", err)
	}

	// Auth can be disabled for ordinary Lantern deployments. The dormant
	// preflight still cannot expose a continuity marker in that configuration.
	open := newConnectTestServer(t, service.NewLanternService(nil), nil)
	openRaw := graphv1connect.NewLanternServiceClient(h2cClient(), open.url)
	openCapability, err := openRaw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || openCapability.Msg.GetEnabled() || openCapability.Msg.GetPolicy() != nil || openCapability.Msg.GetEndpoint() != nil || openCapability.Msg.GetServerNowUnixMs() != 0 {
		t.Fatalf("unauthenticated deployment capability = (%v, %v)", openCapability, err)
	}
	if _, err := openRaw.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{OperationId: operationID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unauthenticated deployment status = %v, want FailedPrecondition", err)
	}
}

func durableReceiptWireConfig(path string, nodeID hlc.NodeID) service.DurableReceiptWALRuntimeConfig {
	now := time.Now()
	return service.DurableReceiptWALRuntimeConfig{
		Path: path,
		Receipt: mutationreceipt.Config{
			Epoch:          mutationreceipt.Epoch{0x42},
			Retention:      time.Hour,
			MaxEntries:     32,
			MaxBytes:       1 << 20,
			ClockHighWater: now,
		},
		Log:        mutationlog.Options{Capacity: 16, SubscriberBuffer: 4},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			provider.ConfigureGraphCache(
				graph,
				provider.CacheConfig{TTL: time.Hour},
				provider.SearchConfig{},
			)
			return nil
		},
		NodeID:        nodeID,
		Now:           now,
		BaselineCodec: backup.ReceiptBaselineCodec{},
	}
}

func durableFollowerReceiptMutation(
	t *testing.T,
	config mutationreceipt.Config,
	origin hlc.NodeID,
	seq uint64,
	tail, head string,
) (*pb.Mutation, []byte) {
	t.Helper()
	return durableFollowerReceiptMutationAt(t, config, origin, seq, tail, head, time.Now())
}

func durableFollowerReceiptMutationAt(
	t *testing.T,
	config mutationreceipt.Config,
	origin hlc.NodeID,
	seq uint64,
	tail, head string,
	acceptedAt time.Time,
) (*pb.Mutation, []byte) {
	t.Helper()
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	issued := acceptedAt.Add(-time.Second)
	id, err := mutationreceipt.NewID(config.Epoch, issued, [24]byte{0x39, byte(seq)})
	if err != nil {
		t.Fatal(err)
	}
	group := mutationreceipt.GroupID{0x4a, byte(seq)}
	canonical := []byte{byte(mutationreceipt.DeleteEdge)}
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(tail)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, tail...)
	binary.BigEndian.PutUint64(length[:], uint64(len(head)))
	canonical = append(canonical, length[:]...)
	canonical = append(canonical, head...)
	digest := mutationreceipt.IntentDigest(canonical)
	policy := store.PolicyFingerprint()
	stamp := hlc.Timestamp{
		WallNs: acceptedAt.UnixNano(),
		NodeID: origin,
	}
	return &pb.Mutation{
		Seq: seq, Origin: origin[:],
		Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, NodeId: origin[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeDelete{
			ReplicatedReceiptEdgeDelete: &pb.ReplicatedReceiptEdgeDelete{
				DeploymentEpoch:     config.Epoch[:],
				PolicyFingerprint:   policy[:],
				TombstoneExpiration: timestamppb.New(acceptedAt.Add(time.Hour)),
				Items: []*pb.ReplicatedReceiptEdgeDeleteItem{{
					Key: &pb.EdgeKey{Tail: tail, Head: head},
					Receipt: &pb.MutationReceipt{
						OperationId: id.Bytes(), LogicalCallId: group[:],
						ItemIndex: 0, ItemCount: 1, IntentSha256: digest[:],
						DeadlineUnixMs: uint64(issued.Add(config.Retention).UnixMilli()),
						OriginalResult: &pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{
							DeleteEdgeExisted: false,
						}},
					},
					CausallyAccepted: false,
				}},
			},
		}},
	}, id.Bytes()
}

func mountDurableReceiptWireRuntime(
	t *testing.T,
	runtime *service.ServingRuntime,
) (*connectTestServer, *client.Lantern) {
	t.Helper()
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(2 * time.Hour)
	replication, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	server := newConnectTestServer(t, primary, replication)
	sdk, err := client.NewLantern(server.url, client.WithHTTPClient(h2cClient()))
	if err != nil {
		t.Fatal(err)
	}
	return server, sdk
}

func TestDurableReceiptWALRuntime_RealConnectWireSnapshotActivation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := durableReceiptWireConfig(filepath.Join(t.TempDir(), "receipts.wal"), hlc.NodeID{0x31})
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	server, sdk := mountDurableReceiptWireRuntime(t, runtime)
	defer closeDurableReceiptWireRuntime(t, server, sdk, runtime)

	const tail, head = "receipt-snapshot-tail", "receipt-snapshot-head"
	if outcome, err := sdk.PutEdge(ctx, tail, head, 1, time.Hour); err != nil ||
		outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("seed PutEdge = (%v, %v)", outcome, err)
	}
	origin := hlc.NodeID{0x72}
	mutation, operationID := durableFollowerReceiptMutation(
		t, config.Receipt, origin, 1, tail, head,
	)
	if err := server.svc.ApplyMutation(ctx, mutation); err != nil {
		t.Fatalf("follower receipt apply: %v", err)
	}

	repl := newReplicationRawClient(t, server.url)
	status, err := repl.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
	if err != nil ||
		status.Msg.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 {
		t.Fatalf("production receipt PeerStatus = (%v, %v)", status, err)
	}

	legacy, err := repl.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{}))
	if err == nil {
		defer func() { _ = legacy.Close() }()
		if legacy.Receive() {
			t.Fatal("legacy full Subscribe emitted an entry in durable receipt mode")
		}
		err = legacy.Err()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("legacy full Subscribe = %v, want InvalidArgument", err)
	}

	downgrade, err := repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
	}))
	if err == nil {
		defer func() { _ = downgrade.Close() }()
		if downgrade.Receive() {
			t.Fatal("graph-only downgrade emitted a frame in durable receipt mode")
		}
		err = downgrade.Err()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("graph-only Snapshot downgrade = %v, want FailedPrecondition", err)
	}

	stream, err := repl.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var (
		header         *pb.SnapshotHeader
		receipt        *pb.SnapshotReceipt
		footer         *pb.SnapshotFooter
		edgeTombstones int
	)
	for stream.Receive() {
		frame := stream.Msg()
		switch entry := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			header = entry.Header
		case *pb.SnapshotResponse_Receipt:
			receipt = entry.Receipt
		case *pb.SnapshotResponse_EdgeTombstone:
			if entry.EdgeTombstone.GetTail() == tail && entry.EdgeTombstone.GetHead() == head {
				edgeTombstones++
			}
		case *pb.SnapshotResponse_Footer:
			footer = entry.Footer
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	policyStore, err := mutationreceipt.New(config.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := policyStore.PolicyFingerprint()
	if header == nil ||
		header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
		!bytes.Equal(header.GetReceiptMetadata().GetPolicy().GetDeploymentEpoch(), config.Receipt.Epoch[:]) ||
		!bytes.Equal(header.GetReceiptMetadata().GetPolicy().GetFingerprint(), fingerprint[:]) {
		t.Fatalf("production receipt Snapshot header = %+v", header)
	}
	if receipt == nil ||
		receipt.GetKind() != pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_DELETE_EDGE ||
		!bytes.Equal(receipt.GetOperationId(), operationID) ||
		receipt.GetItemIndex() != 0 || receipt.GetItemCount() != 1 {
		t.Fatalf("production receipt Snapshot row = %+v", receipt)
	}
	if footer == nil || footer.GetReceiptCount() != 1 || footer.GetEdgeTombstoneCount() != 1 ||
		edgeTombstones != 1 {
		t.Fatalf("production receipt Snapshot footer/tombstones = %+v / %d", footer, edgeTombstones)
	}

	raw := graphv1connect.NewLanternServiceClient(h2cClient(), server.url)
	capability, err := raw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || capability.Msg.GetEnabled() {
		t.Fatalf("production public receipt capability = (%v, %v)", capability, err)
	}
	if _, err := raw.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{
		OperationId: operationID,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("production public receipt status = %v, want FailedPrecondition", err)
	}
}

func closeDurableReceiptWireRuntime(
	t *testing.T,
	server *connectTestServer,
	sdk *client.Lantern,
	runtime *service.ServingRuntime,
) {
	t.Helper()
	if err := sdk.Close(); err != nil {
		t.Fatal(err)
	}
	server.srv.Close()
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDurableReceiptBackupSchedule_RealConnectWire(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "receipts.wal")
	backupDir := t.TempDir()
	t.Setenv("LANTERN_STRICT_CONFIG", "false")
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "fresh")
	t.Setenv("LANTERN_RECEIPT_WAL_PATH", walPath)
	t.Setenv("LANTERN_RECEIPT_EPOCH", strings.Repeat("42", 16))
	t.Setenv("LANTERN_RECEIPT_RETENTION", "1h")
	t.Setenv("LANTERN_RECEIPT_MAX_ENTRIES", "32")
	t.Setenv("LANTERN_RECEIPT_MAX_BYTES", "1048576")
	t.Setenv("LANTERN_NODE_ID", strings.Repeat("31", 16))
	t.Setenv("LANTERN_BACKUP_ENABLED", "true")
	t.Setenv("LANTERN_BACKUP_DIR", backupDir)
	t.Setenv("LANTERN_BACKUP_INTERVAL", "20ms")
	t.Setenv("LANTERN_BACKUP_RETAIN", "1")
	t.Setenv("LANTERN_BACKUP_INSTANCE_ID", "receipt-wire-owner")
	t.Setenv("LANTERN_BACKUP_RESTORE_ON_START", "false")

	cfg, err := provider.NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Backup.Enabled || cfg.Backup.RestoreOnStart {
		t.Fatalf("durable backup config = %+v", cfg.Backup)
	}

	t.Run("restore on start remains rejected", func(t *testing.T) {
		invalid := cfg.Backup
		invalid.RestoreOnStart = true
		runtime, cleanup, err := provider.NewServingRuntime(
			cfg.ReceiptWAL,
			cfg.Cache,
			cfg.Search,
			cfg.MutationLog,
			cfg.Replication,
			invalid,
			nil,
		)
		if runtime != nil || cleanup != nil || err == nil ||
			!strings.Contains(err.Error(), "LANTERN_BACKUP_RESTORE_ON_START must be disabled") {
			t.Fatalf("durable restore-on-start runtime = (%v, cleanup nil=%t, %v)", runtime, cleanup == nil, err)
		}
	})

	runtime, cleanup, err := provider.NewServingRuntime(
		cfg.ReceiptWAL,
		cfg.Cache,
		cfg.Search,
		cfg.MutationLog,
		cfg.Replication,
		cfg.Backup,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup()
	})
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(2 * time.Hour)
	replicationService, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := provider.NewRuntimeCertified(runtime, primary, replicationService)
	if err != nil {
		t.Fatal(err)
	}
	backupper, err := provider.NewBackupper(
		cfg.Backup,
		cfg.ReceiptWAL,
		runtime,
		primary,
		certified,
		prometheus.NewRegistry(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	server := newConnectTestServer(t, primary, replicationService)
	sdk := newConnectClientFor(t, server.url)

	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- backupper.Run(runCtx)
	}()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("stop durable backup scheduler: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("durable backup scheduler did not stop")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if outcome, err := sdk.PutVertex(ctx, "durable/backup-wire", "persisted", time.Hour); err != nil ||
		outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("wire PutVertex = (%v, %v)", outcome, err)
	}
	source, policy, err := runtime.ReceiptWholeStateBackupSource(primary, replicationService)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := source.CaptureForBackup(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	if expected.WALTip.Seq == 0 {
		t.Fatal("wire mutation did not advance the durable WAL")
	}

	evidence, manifestPath := waitForDurableReceiptBackupSet(
		t,
		ctx,
		backupDir,
		cfg.Backup.InstanceID,
	)
	if evidence.NodeID != expected.NodeID ||
		evidence.Generation != expected.Generation ||
		evidence.WALCut != expected.WALTip {
		t.Fatalf(
			"loaded durable evidence identity/cut = %x/%x/%+v, want %x/%x/%+v",
			evidence.NodeID,
			evidence.Generation,
			evidence.WALCut,
			expected.NodeID,
			expected.Generation,
			expected.WALTip,
		)
	}
	if evidence.SetID == 0 || evidence.BackupTimestamp.IsZero() ||
		evidence.Stats.Vertices != 1 || evidence.Stats.Members != 3 ||
		evidence.Stats.Bytes <= 0 || len(evidence.Archive) == 0 ||
		len(evidence.RetiredCatalog) == 0 {
		t.Fatalf("loaded durable backup evidence = %+v", evidence)
	}
	for _, entry := range mustReadDir(t, backupDir) {
		if strings.HasSuffix(entry.Name(), ".lbk") || strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("durable scheduler emitted legacy/temporary path %q", entry.Name())
		}
	}
	if filepath.Ext(manifestPath) != ".json" {
		t.Fatalf("loaded manifest path = %q", manifestPath)
	}
}

func waitForDurableReceiptBackupSet(
	t *testing.T,
	ctx context.Context,
	dir, instance string,
) (backup.ReceiptBackupSetEvidence, string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			lastErr = err
		} else {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".set.json") {
					continue
				}
				manifestPath := filepath.Join(dir, entry.Name())
				evidence, loadErr := backup.LoadReceiptBackupSet(dir, instance, manifestPath)
				if loadErr == nil && evidence.Stats.Vertices == 1 {
					return evidence, manifestPath
				}
				if loadErr != nil {
					lastErr = loadErr
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for complete durable receipt backup set: %v (last validation: %v)", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func mustReadDir(t *testing.T, path string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func newDurableReceiptSnapshotInstaller(
	t *testing.T,
	config service.DurableReceiptWALRuntimeConfig,
	target *connectTestServer,
) *backup.ReceiptSnapshotInstaller {
	t.Helper()
	expectedPolicy := config.Receipt
	expectedPolicy.ClockHighWater = time.Time{}
	collector, err := backup.NewReceiptSnapshotCollector(backup.ReceiptSnapshotCollectorConfig{
		TempDir: filepath.Dir(config.Path),
		Limits: backup.ReceiptSnapshotCollectorLimits{
			MaxFrameBytes: 8 << 20, MaxFrames: 128, MaxTotalBytes: 16 << 20,
			MaxReceipts: 32, MaxOrigins: 16, MaxGraphFrames: 96,
		},
		ExpectedPolicy: expectedPolicy,
		DefaultTTL:     config.DefaultTTL,
		ConfigureGraph: config.ConfigureGraph,
	})
	if err != nil {
		t.Fatal(err)
	}
	installer, err := backup.NewReceiptSnapshotInstaller(collector, target.svc, nil)
	if err != nil {
		t.Fatal(err)
	}
	return installer
}

func startDurableReceiptPump(
	t *testing.T,
	parent context.Context,
	name string,
	config service.DurableReceiptWALRuntimeConfig,
	targetRuntime *service.ServingRuntime,
	target *connectTestServer,
	sourceURL string,
) func() {
	t.Helper()
	return startDurableReceiptPumpWithApplier(
		t, parent, name, config, targetRuntime, target, sourceURL, target.svc, nil,
	)
}

func startDurableReceiptPumpWithApplier(
	t *testing.T,
	parent context.Context,
	name string,
	config service.DurableReceiptWALRuntimeConfig,
	targetRuntime *service.ServingRuntime,
	target *connectTestServer,
	sourceURL string,
	applier replication.MutationApplier,
	metrics replication.Metrics,
) func() {
	t.Helper()
	runCtx, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	pump := replication.NewPump(replication.Config{
		NodeID: config.NodeID, Peers: []string{sourceURL},
		BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
		HTTPClient: h2cClient(), SnapshotInstaller: newDurableReceiptSnapshotInstaller(t, config, target),
		SearchConfigFingerprint: target.svc.SearchConfigFingerprint(), Metrics: metrics,
	}, applier, targetRuntime.GraphCache())
	go func() { done <- pump.Run(runCtx) }()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("%s stop: %v", name, err)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("%s did not stop", name)
			}
		})
	}
}

type scriptedReceiptPumpPeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	subscribe               func(context.Context, int32, *pb.SubscribeRequest, *connect.ServerStream[pb.SubscribeResponse]) error
	searchConfigFingerprint string
	subscribeCalls          atomic.Int32
}

func (p *scriptedReceiptPumpPeer) PeerStatus(
	context.Context,
	*connect.Request[pb.PeerStatusRequest],
) (*connect.Response[pb.PeerStatusResponse], error) {
	return connect.NewResponse(&pb.PeerStatusResponse{
		RequiredSnapshotFormat:  pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1,
		SearchConfigFingerprint: p.searchConfigFingerprint,
	}), nil
}

func (p *scriptedReceiptPumpPeer) Subscribe(
	ctx context.Context,
	request *connect.Request[pb.SubscribeRequest],
	stream *connect.ServerStream[pb.SubscribeResponse],
) error {
	if !request.Msg.GetAcceptReceiptEnvelopes() {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("receipt envelopes not accepted"))
	}
	call := p.subscribeCalls.Add(1)
	if p.subscribe == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return p.subscribe(ctx, call, request.Msg, stream)
}

func newScriptedReceiptPumpServer(t *testing.T, peer *scriptedReceiptPumpPeer) string {
	t.Helper()
	path, handler := graphv1connect.NewLanternReplicationServiceHandler(peer)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	return server.URL
}

func sendScriptedReceiptMutation(
	request *pb.SubscribeRequest,
	stream *connect.ServerStream[pb.SubscribeResponse],
	mutation *pb.Mutation,
) error {
	origin := hex.EncodeToString(mutation.GetOrigin())
	if next, ok := request.GetFromSeqPerOrigin()[origin]; ok && mutation.GetSeq() < next {
		return nil
	}
	return stream.Send(&pb.SubscribeResponse{
		Event: &pb.SubscribeResponse_Mutation{
			Mutation: proto.Clone(mutation).(*pb.Mutation),
		},
	})
}

type receiptPumpMetrics struct {
	applied chan struct{}
}

func (*receiptPumpMetrics) OnPumpConnect(string)                                         {}
func (*receiptPumpMetrics) OnPumpDisconnect(string, string)                              {}
func (*receiptPumpMetrics) OnPumpDropSelfEcho(string)                                    {}
func (*receiptPumpMetrics) OnPumpSnapshotReplayed(string, uint64, uint64, time.Duration) {}
func (*receiptPumpMetrics) OnSearchConfig(string, bool)                                  {}
func (m *receiptPumpMetrics) OnPumpApply(string) {
	select {
	case m.applied <- struct{}{}:
	default:
	}
}

func waitForReceiptPumpApplies(
	t *testing.T,
	ctx context.Context,
	name string,
	applied <-chan struct{},
	want int,
	timeout time.Duration,
) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for got := 0; got < want; got++ {
		select {
		case <-applied:
		case <-ctx.Done():
			t.Fatalf("%s canceled after %d/%d applied frames: %v", name, got, want, ctx.Err())
		case <-timer.C:
			t.Fatalf("%s timed out after %d/%d applied frames", name, got, want)
		}
	}
}

type receiptMutationErrorObserver struct {
	target *service.LanternService
	errors chan error
}

func (o *receiptMutationErrorObserver) ApplyMutation(ctx context.Context, mutation *pb.Mutation) error {
	err := o.target.ApplyMutation(ctx, mutation)
	if err != nil {
		select {
		case o.errors <- err:
		default:
		}
	}
	return err
}

func receiptOriginCut(status *pb.PeerStatusResponse) map[string]uint64 {
	cut := make(map[string]uint64, len(status.GetOrigins()))
	for _, origin := range status.GetOrigins() {
		cut[hex.EncodeToString(origin.GetOrigin())] = origin.GetLastSeq()
	}
	return cut
}

func receiptCutReached(got, want map[string]uint64) bool {
	for origin, seq := range want {
		if got[origin] < seq {
			return false
		}
	}
	return true
}

func waitForDurableReceiptCut(
	t *testing.T,
	ctx context.Context,
	name string,
	server *connectTestServer,
	runtime *service.ServingRuntime,
	want map[string]uint64,
	timeout time.Duration,
) *pb.PeerStatusResponse {
	t.Helper()
	raw := newReplicationRawClient(t, server.url)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var (
		last    *pb.PeerStatusResponse
		lastErr error
	)
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		response, err := raw.PeerStatus(probeCtx, connect.NewRequest(&pb.PeerStatusRequest{}))
		cancel()
		lastErr = err
		if err == nil {
			last = response.Msg
			if receiptCutReached(receiptOriginCut(last), want) {
				return last
			}
		}
		select {
		case <-ctx.Done():
			length, capacity, evicted := runtime.MutationLogStats()
			t.Fatalf("%s origin cut canceled: want=%v got=%v rpc=%v log=%d/%d evicted=%d: %v",
				name, want, receiptOriginCut(last), lastErr, length, capacity, evicted, ctx.Err())
		case <-deadline.C:
			length, capacity, evicted := runtime.MutationLogStats()
			t.Fatalf("%s origin cut timeout: want=%v got=%v rpc=%v log=%d/%d evicted=%d",
				name, want, receiptOriginCut(last), lastErr, length, capacity, evicted)
		case <-ticker.C:
		}
	}
}

func requireExactReceiptCut(
	t *testing.T,
	name string,
	status *pb.PeerStatusResponse,
	want map[string]uint64,
) {
	t.Helper()
	got := receiptOriginCut(status)
	if status == nil || len(status.GetOrigins()) != len(want) || len(got) != len(want) {
		t.Fatalf("%s origin cut = %v, want exactly %v", name, got, want)
	}
	for origin, seq := range want {
		if got[origin] != seq {
			t.Fatalf("%s origin cut = %v, want exactly %v", name, got, want)
		}
	}
}

func requireDurableReceiptSnapshot(
	t *testing.T,
	ctx context.Context,
	name string,
	server *connectTestServer,
	wantCut map[string]uint64,
	operationID []byte,
) {
	t.Helper()
	stream, err := newReplicationRawClient(t, server.url).Snapshot(
		ctx,
		connect.NewRequest(&pb.SnapshotRequest{
			RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1,
		}),
	)
	if err != nil {
		t.Fatalf("%s Snapshot: %v", name, err)
	}
	defer func() { _ = stream.Close() }()
	var (
		header       *pb.SnapshotHeader
		footer       *pb.SnapshotFooter
		headers      int
		footers      int
		receipts     int
		matchingRows int
	)
	for stream.Receive() {
		frame := stream.Msg()
		if candidate := frame.GetHeader(); candidate != nil {
			headers++
			header = candidate
		}
		if receipt := frame.GetReceipt(); receipt != nil {
			receipts++
			if bytes.Equal(receipt.GetOperationId(), operationID) &&
				receipt.GetKind() == pb.SnapshotReceiptKind_SNAPSHOT_RECEIPT_KIND_DELETE_EDGE &&
				receipt.GetItemIndex() == 0 && receipt.GetItemCount() == 1 &&
				len(receipt.GetOriginalResult()) == 1 && receipt.GetOriginalResult()[0] == 0 {
				matchingRows++
			}
		}
		if candidate := frame.GetFooter(); candidate != nil {
			footers++
			footer = candidate
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("%s Snapshot stream: %v", name, err)
	}
	if headers != 1 || footers != 1 || header == nil || footer == nil ||
		header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 {
		t.Fatalf("%s Snapshot framing = headers %d footers %d header %+v footer %+v",
			name, headers, footers, header, footer)
	}
	requireExactReceiptCut(t, name+" Snapshot", &pb.PeerStatusResponse{
		Origins: header.GetReceiptMetadata().GetOriginCutoffs(),
	}, wantCut)
	if got := header.GetCutoffSeqPerOrigin(); len(got) != len(wantCut) {
		t.Fatalf("%s Snapshot cutoff = %v, want exactly %v", name, got, wantCut)
	} else {
		for origin, seq := range wantCut {
			if got[origin] != seq {
				t.Fatalf("%s Snapshot cutoff = %v, want exactly %v", name, got, wantCut)
			}
		}
	}
	if receipts != 1 || matchingRows != 1 || footer.GetReceiptCount() != 1 ||
		footer.GetReceiptOriginCount() != uint64(len(wantCut)) {
		t.Fatalf("%s Snapshot receipt rows = total %d matching %d footer %+v",
			name, receipts, matchingRows, footer)
	}
}

func requireReceiptAcceptanceGraph(
	t *testing.T,
	ctx context.Context,
	name string,
	sdk *client.Lantern,
) {
	t.Helper()
	edge, err := sdk.GetEdge(ctx, "receipt-acceptance/tail", "receipt-acceptance/head")
	if err != nil || edge.GetWeight() != 7 {
		t.Fatalf("%s mixed edge = (%+v, %v), want weight 7", name, edge, err)
	}
	vertex, err := sdk.GetVertex(ctx, "receipt-acceptance/after-partition")
	if err != nil {
		t.Fatalf("%s partition tail vertex: %v", name, err)
	}
	value, err := client.StringValue(vertex)
	if err != nil || value != "rejoined" {
		t.Fatalf("%s partition tail value = %q, %v", name, value, err)
	}
}

func TestDurableReceiptWALRuntime_RealConnectWireOutOfOrderDuplicatePump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config := durableReceiptWireConfig(filepath.Join(t.TempDir(), "target.wal"), hlc.NodeID{0x64})
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	target, sdk := mountDurableReceiptWireRuntime(t, runtime)
	defer closeDurableReceiptWireRuntime(t, target, sdk, runtime)

	origin := hlc.NodeID{0x74}
	const tail = "receipt-order/tail"
	for _, head := range []string{"first", "second"} {
		runtime.GraphCache().AddEdgeWithExpiration(tail, head, 1, time.Now().Add(time.Hour))
	}
	acceptedAt := time.Now().Add(-time.Second)
	first, _ := durableFollowerReceiptMutationAt(
		t, config.Receipt, origin, 1, tail, "first", acceptedAt,
	)
	second, _ := durableFollowerReceiptMutationAt(
		t, config.Receipt, origin, 2, tail, "second", acceptedAt.Add(time.Millisecond),
	)

	futureSent := make(chan struct{})
	releaseContiguous := make(chan struct{})
	peer := &scriptedReceiptPumpPeer{
		searchConfigFingerprint: target.svc.SearchConfigFingerprint(),
	}
	peer.subscribe = func(
		streamCtx context.Context,
		call int32,
		request *pb.SubscribeRequest,
		stream *connect.ServerStream[pb.SubscribeResponse],
	) error {
		if call != 1 {
			<-streamCtx.Done()
			return streamCtx.Err()
		}
		if err := sendScriptedReceiptMutation(request, stream, second); err != nil {
			return err
		}
		close(futureSent)
		select {
		case <-releaseContiguous:
		case <-streamCtx.Done():
			return streamCtx.Err()
		}
		for _, mutation := range []*pb.Mutation{first, second, first} {
			if err := sendScriptedReceiptMutation(request, stream, mutation); err != nil {
				return err
			}
		}
		<-streamCtx.Done()
		return streamCtx.Err()
	}

	metrics := &receiptPumpMetrics{applied: make(chan struct{}, 8)}
	stop := startDurableReceiptPumpWithApplier(
		t,
		ctx,
		"scripted out-of-order receipt Pump",
		config,
		runtime,
		target,
		newScriptedReceiptPumpServer(t, peer),
		target.svc,
		metrics,
	)
	defer stop()

	select {
	case <-futureSent:
	case <-ctx.Done():
		t.Fatalf("scripted seq2 was not sent: %v", ctx.Err())
	}
	waitForReceiptPumpApplies(t, ctx, "future receipt", metrics.applied, 1, 2*time.Second)
	pendingStatus, err := newReplicationRawClient(t, target.url).PeerStatus(
		ctx, connect.NewRequest(&pb.PeerStatusRequest{}),
	)
	if err != nil {
		t.Fatalf("out-of-order PeerStatus: %v", err)
	}
	requireExactReceiptCut(t, "out-of-order seq2", pendingStatus.Msg, map[string]uint64{})
	if length, capacity, evicted := runtime.MutationLogStats(); length != 0 || capacity != 16 || evicted != 0 {
		t.Fatalf("out-of-order seq2 relay log = %d/%d/%d, want 0/16/0", length, capacity, evicted)
	}
	for _, head := range []string{"first", "second"} {
		if _, _, live := runtime.GraphCache().GetEdgeDetail(tail, head); !live {
			t.Fatalf("out-of-order seq2 changed %s→%s before seq1", tail, head)
		}
	}

	close(releaseContiguous)
	waitForReceiptPumpApplies(t, ctx, "contiguous receipts and duplicates", metrics.applied, 3, 2*time.Second)
	originKey := hex.EncodeToString(origin[:])
	status := waitForDurableReceiptCut(
		t, ctx, "out-of-order receipt drain", target, runtime,
		map[string]uint64{originKey: 2}, 2*time.Second,
	)
	requireExactReceiptCut(t, "out-of-order receipt drain", status, map[string]uint64{originKey: 2})
	if length, capacity, evicted := runtime.MutationLogStats(); length != 2 || capacity != 16 || evicted != 0 {
		t.Fatalf("contiguous receipt relay log = %d/%d/%d, want exactly 2/16/0", length, capacity, evicted)
	}
	for _, head := range []string{"first", "second"} {
		if _, _, live := runtime.GraphCache().GetEdgeDetail(tail, head); live {
			t.Fatalf("contiguous receipt drain left %s→%s live", tail, head)
		}
	}
	if calls := peer.subscribeCalls.Load(); calls != 1 {
		t.Fatalf("out-of-order receipt Subscribe calls = %d, want 1", calls)
	}
}

func TestDurableReceiptWALRuntime_RealConnectWireCapacityStallRecoveryPump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	config := durableReceiptWireConfig(filepath.Join(t.TempDir(), "target.wal"), hlc.NodeID{0x65})
	config.Receipt.MaxEntries = 1
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	target, sdk := mountDurableReceiptWireRuntime(t, runtime)
	defer closeDurableReceiptWireRuntime(t, target, sdk, runtime)

	origin := hlc.NodeID{0x75}
	const tail = "receipt-capacity/tail"
	for _, head := range []string{"seed", "pending"} {
		runtime.GraphCache().AddEdgeWithExpiration(tail, head, 1, time.Now().Add(time.Hour))
	}
	firstDeadline := time.Now().Add(2 * time.Second)
	firstAcceptedAt := firstDeadline.Add(-config.Receipt.Retention).Add(time.Second)
	first, _ := durableFollowerReceiptMutationAt(
		t, config.Receipt, origin, 1, tail, "seed", firstAcceptedAt,
	)
	second, secondOperationID := durableFollowerReceiptMutationAt(
		t, config.Receipt, origin, 2, tail, "pending", time.Now(),
	)

	firstSent := make(chan struct{})
	releaseSecond := make(chan struct{})
	peer := &scriptedReceiptPumpPeer{
		searchConfigFingerprint: target.svc.SearchConfigFingerprint(),
	}
	peer.subscribe = func(
		streamCtx context.Context,
		call int32,
		request *pb.SubscribeRequest,
		stream *connect.ServerStream[pb.SubscribeResponse],
	) error {
		if err := sendScriptedReceiptMutation(request, stream, first); err != nil {
			return err
		}
		if call == 1 {
			close(firstSent)
			select {
			case <-releaseSecond:
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
		}
		if err := sendScriptedReceiptMutation(request, stream, second); err != nil {
			return err
		}
		<-streamCtx.Done()
		return streamCtx.Err()
	}

	observer := &receiptMutationErrorObserver{
		target: target.svc,
		errors: make(chan error, 32),
	}
	stop := startDurableReceiptPumpWithApplier(
		t,
		ctx,
		"scripted receipt-capacity Pump",
		config,
		runtime,
		target,
		newScriptedReceiptPumpServer(t, peer),
		observer,
		nil,
	)
	defer stop()

	select {
	case <-firstSent:
	case <-ctx.Done():
		t.Fatalf("capacity seed receipt was not sent: %v", ctx.Err())
	}
	originKey := hex.EncodeToString(origin[:])
	status := waitForDurableReceiptCut(
		t, ctx, "capacity seed receipt", target, runtime,
		map[string]uint64{originKey: 1}, 2*time.Second,
	)
	requireExactReceiptCut(t, "capacity seed receipt", status, map[string]uint64{originKey: 1})
	if length, capacity, evicted := runtime.MutationLogStats(); length != 1 || capacity != 16 || evicted != 0 {
		t.Fatalf("capacity seed relay log = %d/%d/%d, want 1/16/0", length, capacity, evicted)
	}
	if _, _, live := runtime.GraphCache().GetEdgeDetail(tail, "seed"); live {
		t.Fatal("capacity seed receipt did not delete its edge")
	}
	if _, _, live := runtime.GraphCache().GetEdgeDetail(tail, "pending"); !live {
		t.Fatal("capacity pending edge changed before seq2 delivery")
	}

	close(releaseSecond)
	stallTimer := time.NewTimer(time.Second)
	defer stallTimer.Stop()
	var stallErr error
	select {
	case stallErr = <-observer.errors:
	case <-ctx.Done():
		t.Fatalf("capacity stall observation canceled: %v", ctx.Err())
	case <-stallTimer.C:
		t.Fatal("capacity seq2 did not report ResourceExhausted within 1s")
	}
	if connect.CodeOf(stallErr) != connect.CodeResourceExhausted {
		t.Fatalf("capacity seq2 error = %v, want ResourceExhausted", stallErr)
	}
	if !time.Now().Before(firstDeadline) {
		t.Fatalf("capacity stall arrived after seed deadline %s", firstDeadline.Format(time.RFC3339Nano))
	}
	stalledStatus, err := newReplicationRawClient(t, target.url).PeerStatus(
		ctx, connect.NewRequest(&pb.PeerStatusRequest{}),
	)
	if err != nil {
		t.Fatalf("capacity-stalled PeerStatus: %v", err)
	}
	requireExactReceiptCut(t, "capacity-stalled receipt", stalledStatus.Msg, map[string]uint64{originKey: 1})
	if length, capacity, evicted := runtime.MutationLogStats(); length != 1 || capacity != 16 || evicted != 0 {
		t.Fatalf("capacity stall relay log = %d/%d/%d, want unchanged 1/16/0", length, capacity, evicted)
	}
	if _, _, live := runtime.GraphCache().GetEdgeDetail(tail, "pending"); !live {
		t.Fatal("capacity stall published pending seq2 graph effect")
	}

	status = waitForDurableReceiptCut(
		t, ctx, "capacity recovery", target, runtime,
		map[string]uint64{originKey: 2}, 5*time.Second,
	)
	if time.Now().Before(firstDeadline) {
		t.Fatalf("capacity seq2 committed before seed receipt deadline %s", firstDeadline.Format(time.RFC3339Nano))
	}
	requireExactReceiptCut(t, "capacity recovery", status, map[string]uint64{originKey: 2})
	if length, capacity, evicted := runtime.MutationLogStats(); length != 2 || capacity != 16 || evicted != 0 {
		t.Fatalf("capacity recovery relay log = %d/%d/%d, want exactly 2/16/0", length, capacity, evicted)
	}
	if _, _, live := runtime.GraphCache().GetEdgeDetail(tail, "pending"); live {
		t.Fatal("capacity recovery did not publish pending seq2 graph effect")
	}
	if calls := peer.subscribeCalls.Load(); calls < 2 {
		t.Fatalf("capacity recovery Subscribe calls = %d, want reconnect retry", calls)
	}
	requireDurableReceiptSnapshot(
		t, ctx, "capacity recovery", target,
		map[string]uint64{originKey: 2}, secondOperationID,
	)
}

// TestDurableReceiptWALRuntime_ThreeReplicaAcceptance supplies the remaining
// cross-layer evidence for #1393. Public receipt writes stay disabled, so one
// private follower envelope is injected at A; A→B tailing, A→C RECEIPT_V1
// handoff, and B→C partition recovery all traverse real Connect/h2c.
func TestDurableReceiptWALRuntime_ThreeReplicaAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-replica durable receipt acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	aConfig := durableReceiptWireConfig(filepath.Join(t.TempDir(), "a.wal"), hlc.NodeID{0x61})
	aConfig.Log.Capacity = 3
	aRuntime, err := service.CreateDurableReceiptWALServingRuntime(aConfig)
	if err != nil {
		t.Fatal(err)
	}
	a, aSDK := mountDurableReceiptWireRuntime(t, aRuntime)
	defer closeDurableReceiptWireRuntime(t, a, aSDK, aRuntime)

	bConfig := durableReceiptWireConfig(filepath.Join(t.TempDir(), "b.wal"), hlc.NodeID{0x62})
	bRuntime, err := service.CreateDurableReceiptWALServingRuntime(bConfig)
	if err != nil {
		t.Fatal(err)
	}
	b, bSDK := mountDurableReceiptWireRuntime(t, bRuntime)
	defer closeDurableReceiptWireRuntime(t, b, bSDK, bRuntime)
	stopAB := startDurableReceiptPump(t, ctx, "A→B receipt tail", bConfig, bRuntime, b, a.url)
	defer stopAB()

	aOrigin := hex.EncodeToString(aConfig.NodeID[:])
	receiptOriginID := hlc.NodeID{0x71}
	receiptOrigin := hex.EncodeToString(receiptOriginID[:])
	if weight, err := aSDK.AddEdge(
		ctx, "receipt-acceptance/tail", "receipt-acceptance/head", 2, time.Hour,
	); err != nil || weight != 2 {
		t.Fatalf("A AddEdge = (%v, %v)", weight, err)
	}
	waitForDurableReceiptCut(t, ctx, "B after Add", b, bRuntime, map[string]uint64{aOrigin: 1}, 5*time.Second)

	if existed, err := aSDK.DeleteEdge(ctx, "receipt-acceptance/tail", "receipt-acceptance/head"); err != nil || !existed {
		t.Fatalf("A DeleteEdge = (%v, %v), want true, nil", existed, err)
	}
	waitForDurableReceiptCut(t, ctx, "B after Delete", b, bRuntime, map[string]uint64{aOrigin: 2}, 5*time.Second)
	if _, err := bSDK.GetEdge(ctx, "receipt-acceptance/tail", "receipt-acceptance/head"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("B edge after ordinary Delete = %v, want ErrNotFound", err)
	}

	receiptMutation, operationID := durableFollowerReceiptMutationAt(
		t,
		aConfig.Receipt,
		receiptOriginID,
		1,
		"receipt-acceptance/tail",
		"receipt-acceptance/head",
		time.Now().Add(-5*time.Minute),
	)
	if err := a.svc.ApplyMutation(ctx, receiptMutation); err != nil {
		t.Fatalf("inject private receipt mutation at A: %v", err)
	}
	waitForDurableReceiptCut(t, ctx, "B after receipt tail", b, bRuntime, map[string]uint64{
		aOrigin: 2, receiptOrigin: 1,
	}, 5*time.Second)
	if _, err := bSDK.GetEdge(ctx, "receipt-acceptance/tail", "receipt-acceptance/head"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("B edge after causally older receipt Delete = %v, want ErrNotFound", err)
	}
	if length, capacity, evicted := bRuntime.MutationLogStats(); length != 3 || capacity != 16 || evicted != 0 {
		t.Fatalf("B initial receipt tail log = len %d cap %d evicted %d, want 3/16/0",
			length, capacity, evicted)
	}

	identityCtx, identityCancel := context.WithCancel(ctx)
	identity, err := newReplicationRawClient(t, b.url).Subscribe(
		identityCtx,
		connect.NewRequest(&pb.SubscribeRequest{
			FromSeqPerOrigin: map[string]uint64{
				aOrigin:       3,
				receiptOrigin: 1,
			},
			Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		}),
	)
	if err != nil {
		identityCancel()
		t.Fatal(err)
	}
	if !identity.Receive() {
		identityCancel()
		_ = identity.Close()
		t.Fatalf("B receipt-only identity frame: %v", identity.Err())
	}
	marker := identity.Msg().GetIdentityChunk()
	if marker == nil || marker.GetSeq() != 1 ||
		!bytes.Equal(marker.GetOrigin(), receiptOriginID[:]) ||
		marker.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY ||
		!marker.GetIsLast() || len(marker.GetVertexKeys())+len(marker.GetEdgeKeys()) != 0 {
		identityCancel()
		_ = identity.Close()
		t.Fatalf("B receipt-only relay marker = %+v", identity.Msg())
	}
	identityCancel()
	if err := identity.Close(); err != nil {
		t.Fatalf("close B identity stream: %v", err)
	}

	if outcome, err := aSDK.PutEdge(
		ctx, "receipt-acceptance/tail", "receipt-acceptance/head", 7, time.Hour,
	); err != nil || outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("A PutEdge = (%v, %v)", outcome, err)
	}
	waitForDurableReceiptCut(t, ctx, "B after Put", b, bRuntime, map[string]uint64{
		aOrigin: 3, receiptOrigin: 1,
	}, 5*time.Second)
	edge, err := bSDK.GetEdge(ctx, "receipt-acceptance/tail", "receipt-acceptance/head")
	if err != nil || edge.GetWeight() != 7 {
		t.Fatalf("B mixed edge after Put = (%+v, %v), want weight 7", edge, err)
	}
	requireDurableReceiptSnapshot(t, ctx, "B tail replica", b, map[string]uint64{
		aOrigin: 3, receiptOrigin: 1,
	}, operationID)
	if length, capacity, evicted := aRuntime.MutationLogStats(); length != 3 || capacity != 3 || evicted != 1 {
		t.Fatalf("A pre-handoff log = len %d cap %d evicted %d, want 3/3/1",
			length, capacity, evicted)
	}

	cConfig := durableReceiptWireConfig(filepath.Join(t.TempDir(), "c.wal"), hlc.NodeID{0x63})
	cRuntime, err := service.CreateDurableReceiptWALServingRuntime(cConfig)
	if err != nil {
		t.Fatal(err)
	}
	c, cSDK := mountDurableReceiptWireRuntime(t, cRuntime)
	cClosed := false
	defer func() {
		if !cClosed {
			closeDurableReceiptWireRuntime(t, c, cSDK, cRuntime)
		}
	}()
	stopCA := startDurableReceiptPump(t, ctx, "A→C receipt Snapshot", cConfig, cRuntime, c, a.url)
	defer stopCA()
	cStatus := waitForDurableReceiptCut(t, ctx, "C after Snapshot", c, cRuntime, map[string]uint64{
		aOrigin: 3, receiptOrigin: 1,
	}, 5*time.Second)
	requireExactReceiptCut(t, "C after Snapshot", cStatus, map[string]uint64{
		aOrigin: 3, receiptOrigin: 1,
	})
	snapshotLength, snapshotCapacity, snapshotEvicted := cRuntime.MutationLogStats()
	if snapshotLength != 0 || snapshotCapacity != 16 || snapshotEvicted != 1 {
		t.Fatalf("C installed Snapshot log = len %d cap %d evicted %d, want 0/16/1",
			snapshotLength, snapshotCapacity, snapshotEvicted)
	}
	if sourceLength, sourceCapacity, sourceEvicted := aRuntime.MutationLogStats(); sourceLength != 3 ||
		sourceCapacity != 3 || sourceEvicted != snapshotEvicted {
		t.Fatalf("A/C Snapshot boundary = source %d/%d/%d target %d/%d/%d",
			sourceLength, sourceCapacity, sourceEvicted,
			snapshotLength, snapshotCapacity, snapshotEvicted,
		)
	}
	requireDurableReceiptSnapshot(t, ctx, "C installed replica", c, map[string]uint64{
		aOrigin: 3, receiptOrigin: 1,
	}, operationID)
	edge, err = cSDK.GetEdge(ctx, "receipt-acceptance/tail", "receipt-acceptance/head")
	if err != nil || edge.GetWeight() != 7 {
		t.Fatalf("C mixed edge after Snapshot = (%+v, %v), want weight 7", edge, err)
	}
	stopCA()

	if outcome, err := aSDK.PutVertex(
		ctx, "receipt-acceptance/after-partition", "rejoined", time.Hour,
	); err != nil || outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("A partition-tail PutVertex = (%v, %v)", outcome, err)
	}
	waitForDurableReceiptCut(t, ctx, "B while C partitioned", b, bRuntime, map[string]uint64{
		aOrigin: 4, receiptOrigin: 1,
	}, 5*time.Second)
	if _, err := cSDK.GetVertex(ctx, "receipt-acceptance/after-partition"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("C observed partitioned write before rejoin: %v", err)
	}

	stopCB := startDurableReceiptPump(t, ctx, "B→C receipt rejoin", cConfig, cRuntime, c, b.url)
	defer stopCB()
	cStatus = waitForDurableReceiptCut(t, ctx, "C after rejoin", c, cRuntime, map[string]uint64{
		aOrigin: 4, receiptOrigin: 1,
	}, 5*time.Second)
	requireExactReceiptCut(t, "C after rejoin", cStatus, map[string]uint64{
		aOrigin: 4, receiptOrigin: 1,
	})
	requireReceiptAcceptanceGraph(t, ctx, "C after rejoin", cSDK)
	if length, capacity, evicted := cRuntime.MutationLogStats(); length != snapshotLength+1 ||
		capacity != snapshotCapacity || evicted != snapshotEvicted {
		t.Fatalf("C duplicate replay log = len %d cap %d evicted %d, want %d/%d/%d",
			length, capacity, evicted, snapshotLength+1, snapshotCapacity, snapshotEvicted)
	}
	stopCB()

	generation, err := os.ReadFile(cConfig.Path + ".generation")
	if err != nil {
		t.Fatal(err)
	}
	beforeRestartLength, beforeRestartCapacity, beforeRestartEvicted := cRuntime.MutationLogStats()
	closeDurableReceiptWireRuntime(t, c, cSDK, cRuntime)
	cClosed = true

	cConfig.Now = time.Now()
	cConfig.Receipt.ClockHighWater = cConfig.Now
	restartedRuntime, err := service.OpenDurableReceiptWALServingRuntime(cConfig)
	if err != nil {
		t.Fatal(err)
	}
	restarted, restartedSDK := mountDurableReceiptWireRuntime(t, restartedRuntime)
	defer closeDurableReceiptWireRuntime(t, restarted, restartedSDK, restartedRuntime)
	restartedGeneration, err := os.ReadFile(cConfig.Path + ".generation")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restartedGeneration, generation) {
		t.Fatal("C durable receipt restart changed generation bytes")
	}
	if length, capacity, evicted := restartedRuntime.MutationLogStats(); length != beforeRestartLength ||
		capacity != beforeRestartCapacity || evicted != beforeRestartEvicted {
		t.Fatalf("C restarted log = len %d cap %d evicted %d, want %d/%d/%d",
			length, capacity, evicted, beforeRestartLength, beforeRestartCapacity, beforeRestartEvicted)
	}

	wantFinalCut := map[string]uint64{aOrigin: 4, receiptOrigin: 1}
	replicas := []struct {
		name    string
		server  *connectTestServer
		sdk     *client.Lantern
		runtime *service.ServingRuntime
	}{
		{name: "A", server: a, sdk: aSDK, runtime: aRuntime},
		{name: "B", server: b, sdk: bSDK, runtime: bRuntime},
		{name: "C restarted", server: restarted, sdk: restartedSDK, runtime: restartedRuntime},
	}
	for _, replica := range replicas {
		status := waitForDurableReceiptCut(
			t, ctx, replica.name+" final", replica.server, replica.runtime, wantFinalCut, 5*time.Second,
		)
		requireExactReceiptCut(t, replica.name+" final", status, wantFinalCut)
		requireReceiptAcceptanceGraph(t, ctx, replica.name, replica.sdk)
		requireDurableReceiptSnapshot(t, ctx, replica.name, replica.server, wantFinalCut, operationID)

		raw := graphv1connect.NewLanternServiceClient(h2cClient(), replica.server.url)
		capability, err := raw.GetReceiptCapability(
			ctx,
			connect.NewRequest(&pb.GetReceiptCapabilityRequest{}),
		)
		if err != nil || capability.Msg.GetEnabled() {
			t.Fatalf("%s public receipt capability = (%v, %v)", replica.name, capability, err)
		}
		if _, err := raw.GetReceiptStatus(
			ctx,
			connect.NewRequest(&pb.GetReceiptStatusRequest{OperationId: operationID}),
		); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("%s public receipt status = %v, want FailedPrecondition", replica.name, err)
		}
	}
	if length, capacity, evicted := aRuntime.MutationLogStats(); length != 3 || capacity != 3 || evicted != 2 {
		t.Fatalf("A final log = len %d cap %d evicted %d, want 3/3/2", length, capacity, evicted)
	}
	if length, capacity, evicted := bRuntime.MutationLogStats(); length != 5 || capacity != 16 || evicted != 0 {
		t.Fatalf("B final relay log = len %d cap %d evicted %d, want 5/16/0", length, capacity, evicted)
	}
}

func TestDurableReceiptWALRuntime_RealConnectWireGapRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping durable receipt gap recovery in short mode")
	}
	for _, recovery := range []string{"pump", "anti-entropy"} {
		t.Run(recovery, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			sourceConfig := durableReceiptWireConfig(
				filepath.Join(t.TempDir(), "source.wal"),
				hlc.NodeID{0x51},
			)
			sourceConfig.Log.Capacity = 2
			sourceRuntime, err := service.CreateDurableReceiptWALServingRuntime(sourceConfig)
			if err != nil {
				t.Fatal(err)
			}
			source, sourceSDK := mountDurableReceiptWireRuntime(t, sourceRuntime)
			defer closeDurableReceiptWireRuntime(t, source, sourceSDK, sourceRuntime)

			const tail, head = "durable-gap-tail", "durable-gap-head"
			if outcome, err := sourceSDK.PutEdge(ctx, tail, head, 1, time.Hour); err != nil ||
				outcome != client.PutOutcomeAppliedAndLive {
				t.Fatalf("seed source edge = (%v, %v)", outcome, err)
			}
			receiptOrigin := hlc.NodeID{0x73}
			mutation, operationID := durableFollowerReceiptMutation(
				t, sourceConfig.Receipt, receiptOrigin, 1, tail, head,
			)
			if err := source.svc.ApplyMutation(ctx, mutation); err != nil {
				t.Fatalf("seed source receipt mutation: %v", err)
			}
			for i := range 8 {
				key := fmt.Sprintf("durable-gap-%02d", i)
				if _, err := sourceSDK.PutVertex(ctx, key, key, time.Hour); err != nil {
					t.Fatalf("seed source vertex %q: %v", key, err)
				}
			}
			if _, _, evicted := sourceRuntime.MutationLogStats(); evicted == 0 {
				t.Fatal("source mutation log did not create a recovery gap")
			}

			targetConfig := durableReceiptWireConfig(
				filepath.Join(t.TempDir(), "target.wal"),
				hlc.NodeID{0x52},
			)
			targetRuntime, err := service.CreateDurableReceiptWALServingRuntime(targetConfig)
			if err != nil {
				t.Fatal(err)
			}
			target, targetSDK := mountDurableReceiptWireRuntime(t, targetRuntime)
			defer closeDurableReceiptWireRuntime(t, target, targetSDK, targetRuntime)

			installer := newDurableReceiptSnapshotInstaller(t, targetConfig, target)

			runCtx, stop := context.WithCancel(ctx)
			done := make(chan error, 1)
			switch recovery {
			case "pump":
				pump := replication.NewPump(replication.Config{
					NodeID: targetConfig.NodeID, Peers: []string{source.url},
					BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
					HTTPClient: h2cClient(), SnapshotInstaller: installer,
					SearchConfigFingerprint: target.svc.SearchConfigFingerprint(),
				}, target.svc, targetRuntime.GraphCache())
				go func() { done <- pump.Run(runCtx) }()
			case "anti-entropy":
				antiEntropy := replication.NewAntiEntropy(replication.AntiEntropyConfig{
					NodeID: targetConfig.NodeID, Peers: []string{source.url},
					Interval: 20 * time.Millisecond, SubscribeTimeout: 2 * time.Second,
					HTTPClient: h2cClient(), SnapshotInstaller: installer,
					SearchConfigFingerprint: target.svc.SearchConfigFingerprint(),
				}, target.svc, target.svc, targetRuntime.GraphCache())
				go func() { done <- antiEntropy.Run(runCtx) }()
			default:
				t.Fatalf("unknown recovery path %q", recovery)
			}
			defer func() {
				stop()
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Errorf("%s stop: %v", recovery, err)
					}
				case <-time.After(2 * time.Second):
					t.Errorf("%s did not stop", recovery)
				}
			}()

			if !waitForVertex(t, targetRuntime.GraphCache(), "durable-gap-07", 5*time.Second) {
				t.Fatal("durable receipt Snapshot did not publish graph state")
			}
			if _, _, ok := targetRuntime.GraphCache().GetEdgeDetail(tail, head); ok {
				t.Fatal("durable receipt Snapshot did not publish the receipt tombstone")
			}
			if _, err := sourceSDK.PutVertex(ctx, "durable-gap-tail-resume", "tail", time.Hour); err != nil {
				t.Fatalf("source tail write: %v", err)
			}
			if !waitForVertex(t, targetRuntime.GraphCache(), "durable-gap-tail-resume", 5*time.Second) {
				t.Fatal("durable recovery did not resume the same responder tail")
			}

			stream, err := newReplicationRawClient(t, target.url).Snapshot(
				ctx,
				connect.NewRequest(&pb.SnapshotRequest{
					RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1,
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			var header *pb.SnapshotHeader
			var foundReceipt bool
			for stream.Receive() {
				frame := stream.Msg()
				if candidate := frame.GetHeader(); candidate != nil {
					header = candidate
				}
				if receipt := frame.GetReceipt(); receipt != nil &&
					bytes.Equal(receipt.GetOperationId(), operationID) {
					foundReceipt = true
				}
			}
			if err := stream.Err(); err != nil {
				t.Fatal(err)
			}
			if err := stream.Close(); err != nil {
				t.Fatal(err)
			}
			if header == nil || header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
				header.GetCutoffSeqPerOrigin()[hex.EncodeToString(receiptOrigin[:])] != 1 ||
				header.GetCutoffSeqPerOrigin()[hex.EncodeToString(sourceConfig.NodeID[:])] < 9 ||
				!foundReceipt {
				t.Fatalf(
					"installed whole-state cut = header %+v, receipt %t",
					header,
					foundReceipt,
				)
			}
			capability, err := graphv1connect.NewLanternServiceClient(
				h2cClient(),
				target.url,
			).GetReceiptCapability(
				ctx,
				connect.NewRequest(&pb.GetReceiptCapabilityRequest{}),
			)
			if err != nil || capability.Msg.GetEnabled() {
				t.Fatalf("public receipt capability after recovery = (%v, %v)", capability, err)
			}
		})
	}
}

// TestDurableReceiptWALRuntime_RealConnectWireRestart is the external-surface
// gate for the private production runtime. Public writes traverse real h2c,
// shutdown releases every serving consumer before the runtime owner, and a
// same-generation restart reconstructs live, expired, and deleted state.
func TestDurableReceiptWALRuntime_RealConnectWireRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "receipts.wal")
	nodeID := hlc.NodeID{0x31}
	config := durableReceiptWireConfig(path, nodeID)

	fresh, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	server, sdk := mountDurableReceiptWireRuntime(t, fresh)
	for _, key := range []string{"durable/live", "durable/deleted"} {
		if outcome, err := sdk.PutVertex(ctx, key, key, time.Hour); err != nil ||
			outcome != client.PutOutcomeAppliedAndLive {
			closeDurableReceiptWireRuntime(t, server, sdk, fresh)
			t.Fatalf("PutVertex(%q) = (%v, %v)", key, outcome, err)
		}
	}
	expiresAt := time.Now().Add(500 * time.Millisecond)
	if outcome, err := sdk.PutVertexAt(ctx, "durable/expired", "expired", expiresAt); err != nil ||
		outcome != client.PutOutcomeAppliedAndLive {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatalf("PutVertexAt(expired) = (%v, %v)", outcome, err)
	}
	if existed, err := sdk.DeleteVertex(ctx, "durable/deleted"); err != nil || !existed {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatalf("DeleteVertex = (%v, %v), want true, nil", existed, err)
	}
	if length, _, evicted := fresh.MutationLogStats(); length != 4 || evicted != 0 {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatalf("fresh mutation Log = len %d evicted %d, want 4, 0", length, evicted)
	}
	generation, err := os.ReadFile(path + ".generation")
	if err != nil {
		closeDurableReceiptWireRuntime(t, server, sdk, fresh)
		t.Fatal(err)
	}
	closeDurableReceiptWireRuntime(t, server, sdk, fresh)
	if wait := time.Until(expiresAt) + 10*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}

	clockBeforeRejectedRestart, err := os.ReadFile(path + ".clock")
	if err != nil {
		t.Fatal(err)
	}
	wrongNode := config
	wrongNode.NodeID[0] ^= 0xff
	wrongNode.Now = time.Now()
	wrongNode.Receipt.ClockHighWater = wrongNode.Now
	if runtime, err := service.OpenDurableReceiptWALServingRuntime(wrongNode); runtime != nil ||
		err == nil || !strings.Contains(err.Error(), "generation binding mismatch") {
		if runtime != nil {
			_ = runtime.Close()
		}
		t.Fatalf("changed-NodeID restart = %p, %v", runtime, err)
	}
	clockAfterRejectedRestart, err := os.ReadFile(path + ".clock")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(clockAfterRejectedRestart, clockBeforeRejectedRestart) {
		t.Fatal("changed-NodeID restart modified the clock journal before rejection")
	}

	config.Now = time.Now()
	config.Receipt.ClockHighWater = config.Now
	restarted, err := service.OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	restartedGeneration, err := os.ReadFile(path + ".generation")
	if err != nil {
		_ = restarted.Close()
		t.Fatal(err)
	}
	if !bytes.Equal(restartedGeneration, generation) {
		_ = restarted.Close()
		t.Fatal("same-epoch restart changed durable generation bytes")
	}
	restartedServer, restartedSDK := mountDurableReceiptWireRuntime(t, restarted)
	defer closeDurableReceiptWireRuntime(t, restartedServer, restartedSDK, restarted)

	vertex, err := restartedSDK.GetVertex(ctx, "durable/live")
	if err != nil {
		t.Fatalf("GetVertex(live): %v", err)
	}
	if value, err := client.StringValue(vertex); err != nil || value != "durable/live" {
		t.Fatalf("recovered live value = %q, %v", value, err)
	}
	for _, key := range []string{"durable/deleted", "durable/expired"} {
		if _, err := restartedSDK.GetVertex(ctx, key); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("GetVertex(%q) after restart = %v, want ErrNotFound", key, err)
		}
	}
	if length, _, evicted := restarted.MutationLogStats(); length != 4 || evicted != 0 {
		t.Fatalf("restarted mutation Log = len %d evicted %d, want 4, 0", length, evicted)
	}
	status, err := newReplicationRawClient(t, restartedServer.url).PeerStatus(
		ctx,
		connect.NewRequest(&pb.PeerStatusRequest{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(status.Msg.GetSelfOrigin(), nodeID[:]) ||
		len(status.Msg.GetOrigins()) != 1 ||
		status.Msg.GetOrigins()[0].GetLastSeq() != 4 {
		t.Fatalf("restarted PeerStatus = %+v", status.Msg)
	}
}
