package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Second)
	id, err := mutationreceipt.NewID(config.Epoch, issued, [24]byte{0x39, byte(seq)})
	if err != nil {
		t.Fatal(err)
	}
	group := mutationreceipt.GroupID{0x4a}
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
		WallNs: time.Now().UnixNano(),
		NodeID: origin,
	}
	return &pb.Mutation{
		Seq: seq, Origin: origin[:],
		Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, NodeId: origin[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeDelete{
			ReplicatedReceiptEdgeDelete: &pb.ReplicatedReceiptEdgeDelete{
				DeploymentEpoch:     config.Epoch[:],
				PolicyFingerprint:   policy[:],
				TombstoneExpiration: timestamppb.New(time.Unix(0, stamp.WallNs).Add(time.Hour)),
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

			expectedPolicy := targetConfig.Receipt
			expectedPolicy.ClockHighWater = time.Time{}
			collector, err := backup.NewReceiptSnapshotCollector(backup.ReceiptSnapshotCollectorConfig{
				TempDir: filepath.Dir(targetConfig.Path),
				Limits: backup.ReceiptSnapshotCollectorLimits{
					MaxFrameBytes: 8 << 20, MaxFrames: 128, MaxTotalBytes: 16 << 20,
					MaxReceipts: 32, MaxOrigins: 16, MaxGraphFrames: 96,
				},
				ExpectedPolicy: expectedPolicy,
				DefaultTTL:     targetConfig.DefaultTTL,
				ConfigureGraph: targetConfig.ConfigureGraph,
			})
			if err != nil {
				t.Fatal(err)
			}
			installer, err := backup.NewReceiptSnapshotInstaller(collector, target.svc, nil)
			if err != nil {
				t.Fatal(err)
			}

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
