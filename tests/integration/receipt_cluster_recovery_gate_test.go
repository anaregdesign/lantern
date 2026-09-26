package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
)

func TestPublicReceiptClusterLossBackupRestore_RealConnectWire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-node receipt backup recovery")
	}
	const token = "receipt-cluster-restore-token"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	origin := newPublicReceiptWireServer(t, hlc.NodeID{0xa1}, 32, token)
	follower := newPublicReceiptWireServer(t, hlc.NodeID{0xb2}, 32, token)
	stopFollower := startPublicReceiptPump(t, ctx, "receipt backup follower", follower, origin, token)
	defer stopFollower()

	oldCapability := publicReceiptCapability(t, origin, token)
	issued := time.UnixMilli(int64(oldCapability.GetServerNowUnixMs())).Add(-time.Second)
	putContext := publicReceiptWireContext(t, oldCapability, 0xa1, 2, issued)
	put, err := origin.raw.PutVertices(ctx, receiptRequestWithToken(&pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{
			{Key: "cluster/live", Value: &pb.Vertex_String_{String_: "saved"}},
			{Key: "cluster/deleted", Value: &pb.Vertex_String_{String_: "removed"}},
		},
		ReceiptContext: putContext,
	}, token))
	if err != nil || !reflect.DeepEqual(put.Msg.GetOutcomes(), []pb.PutOutcome{
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
	}) {
		t.Fatalf("receipt PutVertices = (%+v, %v)", put, err)
	}

	edge := &pb.EdgeKey{Tail: "cluster/live", Head: "cluster/deleted"}
	putReceiptWireEdges(t, origin.raw, token, edge)
	edgeContext := publicReceiptWireContext(t, oldCapability, 0xa2, 1, issued)
	deletedEdge, err := origin.raw.DeleteEdge(ctx, receiptRequestWithToken(&pb.DeleteEdgeRequest{
		Tail: edge.GetTail(), Head: edge.GetHead(), ReceiptContext: edgeContext,
	}, token))
	if err != nil || !deletedEdge.Msg.GetExisted() {
		t.Fatalf("receipt DeleteEdge = (%+v, %v)", deletedEdge, err)
	}
	vertexContext := publicReceiptWireContext(t, oldCapability, 0xa3, 2, issued)
	deletedVertices, err := origin.raw.DeleteVertices(ctx, receiptRequestWithToken(&pb.DeleteVerticesRequest{
		Keys: []string{"cluster/deleted", "cluster/absent"}, ReceiptContext: vertexContext,
	}, token))
	if err != nil || deletedVertices.Msg.GetDeleted() != 1 ||
		!reflect.DeepEqual(deletedVertices.Msg.GetExisted(), []bool{true, false}) {
		t.Fatalf("receipt DeleteVertices = (%+v, %v)", deletedVertices, err)
	}

	originCut := map[string]uint64{hex.EncodeToString(origin.config.NodeID[:]): 4}
	requireExactReceiptCut(t, "converged backup follower", waitForPublicReceiptCut(
		t, ctx, "converged backup follower", follower, token, originCut, 5*time.Second,
	), originCut)
	operationIDs := append(append([][]byte{}, putContext.GetOperationIds()...), edgeContext.GetOperationIds()...)
	operationIDs = append(operationIDs, vertexContext.GetOperationIds()...)
	wantResults := []*pb.ReceiptResult{
		{Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}},
		{Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}},
		{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: true}},
		{Result: &pb.ReceiptResult_DeleteVertexExisted{DeleteVertexExisted: true}},
		{Result: &pb.ReceiptResult_DeleteVertexExisted{DeleteVertexExisted: false}},
	}
	original := receiptWireStatuses(t, ctx, origin, token, operationIDs)
	requireStatuses := func(name string, node publicReceiptWireServer) {
		t.Helper()
		statuses := receiptWireStatuses(t, ctx, node, token, operationIDs)
		for i, status := range statuses {
			if status.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
				!bytes.Equal(status.GetOperationId(), operationIDs[i]) ||
				!bytes.Equal(status.GetReceipt().GetOperationId(), operationIDs[i]) ||
				!proto.Equal(status.GetReceipt().GetOriginalResult(), wantResults[i]) ||
				!proto.Equal(status.GetReceipt(), original[i].GetReceipt()) {
				t.Fatalf("%s receipt[%d] = %+v, want confirmed %v", name, i, status, wantResults[i])
			}
		}
	}
	requireGraph := func(name string, node publicReceiptWireServer) {
		t.Helper()
		live, err := node.raw.GetVertex(ctx, receiptRequestWithToken(
			&pb.GetVertexRequest{Key: "cluster/live"}, token,
		))
		if err != nil || live.Msg.GetVertex().GetString_() != "saved" {
			t.Fatalf("%s live vertex = (%+v, %v)", name, live, err)
		}
		if _, err := node.raw.GetVertex(ctx, receiptRequestWithToken(
			&pb.GetVertexRequest{Key: "cluster/deleted"}, token,
		)); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("%s deleted vertex = %v, want NotFound", name, err)
		}
		if _, err := node.raw.GetEdge(ctx, receiptRequestWithToken(
			&pb.GetEdgeRequest{Tail: edge.GetTail(), Head: edge.GetHead()}, token,
		)); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("%s deleted edge = %v, want NotFound", name, err)
		}
	}
	requireStatuses("origin", origin)
	requireStatuses("backed-up follower", follower)
	requireGraph("backed-up follower", follower)

	source, policy, err := follower.runtime.ReceiptWholeStateBackupSource(follower.server.svc, follower.server.rep)
	if err != nil {
		t.Fatal(err)
	}
	backupDir := t.TempDir()
	const instance = "cluster-restore"
	producer, err := backup.NewReceipt(follower.server.svc, source, policy, backup.Config{
		Enabled: true, Dir: backupDir, Interval: time.Hour, Retain: 1, InstanceID: instance,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := producer.BackupNow(ctx)
	if err != nil || stats.Vertices != 1 || stats.Edges != 0 ||
		stats.Receipts != len(operationIDs) || stats.Members != 3 {
		t.Fatalf("converged follower backup = (%+v, %v)", stats, err)
	}

	stopFollower()
	for _, node := range []publicReceiptWireServer{origin, follower} {
		node.server.srv.Close()
		if err := node.runtime.Close(); err != nil {
			t.Fatalf("close original cluster node: %v", err)
		}
	}
	evidence, err := backup.LoadLatestReceiptBackupSet(backupDir, instance)
	if err != nil || evidence.WALCut.Seq == 0 || evidence.Stats.Receipts != len(operationIDs) {
		t.Fatalf("loaded receipt backup after total-cluster loss = (%+v, %v)", evidence, err)
	}

	restoredConfig := durableReceiptWireConfig(filepath.Join(t.TempDir(), "restored.wal"), hlc.NodeID{0xc3})
	restoredConfig.Receipt.Epoch = mutationreceipt.Epoch{0x73}
	restore, err := backup.PrepareFreshReceiptStartupRestore(evidence, restoredConfig.Receipt, restoredConfig.Now)
	if err != nil {
		t.Fatal(err)
	}
	if len(restore.Capture.Receipts.Receipts) != 0 ||
		len(restore.Capture.Retired.Epochs) != 1 ||
		len(restore.Capture.Retired.Epochs[0].State.Receipts) != len(operationIDs) {
		t.Fatalf("fresh restore did not rotate %d active receipts into retired evidence", len(operationIDs))
	}
	restoredConfig.StartupRestore = &restore
	restored := newClusterRecoveryPublicReceiptServer(t, restoredConfig, token)
	restoredCapability := publicReceiptCapability(t, restored, token)
	if !bytes.Equal(restoredCapability.GetPolicy().GetDeploymentEpoch(), restoredConfig.Receipt.Epoch[:]) ||
		bytes.Equal(restoredCapability.GetPolicy().GetDeploymentEpoch(), oldCapability.GetPolicy().GetDeploymentEpoch()) ||
		restored.runtime.ReceiptStats().Entries != 0 {
		t.Fatalf("fresh restore active receipt policy = %+v, Store = %+v", restoredCapability.GetPolicy(), restored.runtime.ReceiptStats())
	}
	requireStatuses("fresh restored node (retired epoch)", restored)
	requireGraph("fresh restored node", restored)

	beforeStore := restored.runtime.ReceiptStats()
	beforeWAL, err := os.Stat(restoredConfig.Path)
	if err != nil {
		t.Fatal(err)
	}
	beforeCut := receiptOriginCut(waitForPublicReceiptCut(
		t, ctx, "fresh restored origin cut", restored, token, originCut, 5*time.Second,
	))
	staleContext := publicReceiptWireContext(t, oldCapability, 0xaf, 1, issued)
	staleContext.Endpoint = proto.Clone(restoredCapability.GetEndpoint()).(*pb.ReceiptEndpoint)
	if _, err := restored.raw.PutVertex(ctx, receiptRequestWithToken(&pb.PutVertexRequest{
		Vertex: &pb.Vertex{
			Key: "cluster/stale", Value: &pb.Vertex_String_{String_: "must not land"},
		},
		ReceiptContext: staleContext,
	}, token)); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "outside the active receipt epoch") {
		t.Fatalf("old-epoch PutVertex with current endpoint = %v, want epoch FailedPrecondition", err)
	}
	afterWAL, err := os.Stat(restoredConfig.Path)
	if err != nil {
		t.Fatal(err)
	}
	afterStatus, err := newReplicationRawClient(t, restored.server.url).PeerStatus(
		ctx, receiptRequestWithToken(&pb.PeerStatusRequest{}, token),
	)
	if err != nil {
		t.Fatal(err)
	}
	afterStore := restored.runtime.ReceiptStats()
	if afterStore.Entries != beforeStore.Entries || afterStore.Bytes != beforeStore.Bytes ||
		afterWAL.Size() != beforeWAL.Size() ||
		!reflect.DeepEqual(receiptOriginCut(afterStatus.Msg), beforeCut) {
		t.Fatalf("rejected old-epoch write changed Store/WAL/origin cut: before %+v/%d/%v, after %+v/%d/%v",
			beforeStore, beforeWAL.Size(), beforeCut,
			afterStore, afterWAL.Size(), receiptOriginCut(afterStatus.Msg))
	}
	if _, err := restored.raw.GetVertex(ctx, receiptRequestWithToken(
		&pb.GetVertexRequest{Key: "cluster/stale"}, token,
	)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("rejected old-epoch write changed graph: GetVertex = %v", err)
	}
	staleStatus := receiptWireStatuses(t, ctx, restored, token, staleContext.GetOperationIds())[0]
	if staleStatus.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE ||
		staleStatus.GetReceipt() != nil {
		t.Fatalf("unknown retired-epoch ID = %+v, want NO_LONGER_PROVABLE without receipt", staleStatus)
	}
	requireStatuses("restored node after rejection", restored)
	if length, _, evicted := restored.runtime.MutationLogStats(); length != 0 || evicted == 0 {
		t.Fatalf("fresh restore replay log = len %d evicted %d, want a committed boundary with no retained tail",
			length, evicted)
	}

	joinConfig := durableReceiptWireConfig(filepath.Join(t.TempDir(), "joiner.wal"), hlc.NodeID{0xd4})
	joinConfig.Receipt.Epoch = restoredConfig.Receipt.Epoch
	joiner := newClusterRecoveryPublicReceiptServer(t, joinConfig, token)
	snapshotMetrics := &clusterReceiptSnapshotMetrics{installed: make(chan struct{}, 1)}
	stopJoiner := startDurableReceiptPumpWithApplier(
		t, ctx, "recovered receipt snapshot",
		joinConfig, joiner.runtime, joiner.server, restored.server.url,
		joiner.server.svc, snapshotMetrics, token,
	)
	defer stopJoiner()
	select {
	case <-snapshotMetrics.installed:
	case <-ctx.Done():
		t.Fatalf("joiner did not install a RECEIPT Snapshot: %v", ctx.Err())
	case <-time.After(5 * time.Second):
		t.Fatal("joiner did not install a RECEIPT Snapshot; WAL replay cannot prove recovery")
	}
	waitForPublicReceiptCut(t, ctx, "new replica recovered", joiner, token, originCut, 5*time.Second)
	requireStatuses("rejoined node (retired epoch)", joiner)
	requireGraph("rejoined node", joiner)

	currentContext := publicReceiptWireContext(t, restoredCapability, 0xc4, 1,
		time.UnixMilli(int64(restoredCapability.GetServerNowUnixMs())).Add(-time.Second))
	current, err := restored.raw.PutVertex(ctx, receiptRequestWithToken(&pb.PutVertexRequest{
		Vertex:         &pb.Vertex{Key: "cluster/new", Value: &pb.Vertex_String_{String_: "current"}},
		ReceiptContext: currentContext,
	}, token))
	if err != nil || current.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("new-epoch PutVertex = (%+v, %v)", current, err)
	}
	joinedCut := map[string]uint64{
		hex.EncodeToString(origin.config.NodeID[:]):   4,
		hex.EncodeToString(restored.config.NodeID[:]): 1,
	}
	joinedStatus := waitForPublicReceiptCut(t, ctx, "new-epoch tail on joiner", joiner, token, joinedCut, 5*time.Second)
	requireExactReceiptCut(t, "new-epoch tail on joiner", joinedStatus, joinedCut)
	for _, node := range []struct {
		name string
		wire publicReceiptWireServer
	}{
		{"restored node", restored},
		{"rejoined node", joiner},
	} {
		currentStatus := receiptWireStatuses(t, ctx, node.wire, token, currentContext.GetOperationIds())[0]
		if currentStatus.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
			currentStatus.GetReceipt().GetOriginalResult().GetPutVertexOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
			t.Fatalf("%s current-epoch receipt = %+v", node.name, currentStatus)
		}
		live, err := node.wire.raw.GetVertex(ctx, receiptRequestWithToken(
			&pb.GetVertexRequest{Key: "cluster/new"}, token,
		))
		if err != nil || live.Msg.GetVertex().GetString_() != "current" {
			t.Fatalf("%s new-epoch graph = (%+v, %v)", node.name, live, err)
		}
		requireStatuses(node.name+" after new epoch write", node.wire)
	}

	beforeCurrent := receiptWireStatuses(t, ctx, joiner, token, currentContext.GetOperationIds())[0]
	beforeClock := joiner.runtime.ReceiptStats().HighWaterMillis
	beforeLength, beforeCapacity, beforeEvicted := joiner.runtime.MutationLogStats()
	if beforeEvicted == 0 {
		t.Fatal("joining node did not retain a committed Snapshot boundary")
	}
	beforeOrigins := make(map[string]*pb.OriginState, len(joinedStatus.GetOrigins()))
	for _, row := range joinedStatus.GetOrigins() {
		beforeOrigins[hex.EncodeToString(row.GetOrigin())] = proto.Clone(row).(*pb.OriginState)
	}
	beforeGeneration, err := os.ReadFile(joinConfig.Path + ".generation")
	if err != nil {
		t.Fatal(err)
	}

	stopJoiner()
	joiner.server.srv.Close()
	if err := joiner.runtime.Close(); err != nil {
		t.Fatalf("close snapshot joiner: %v", err)
	}
	joinConfig.Now = time.Now()
	joinConfig.Receipt.ClockHighWater = joinConfig.Now
	restarted := openClusterRecoveryPublicReceiptServer(t, joinConfig, token)
	if afterClock := restarted.runtime.ReceiptStats().HighWaterMillis; afterClock < beforeClock {
		t.Fatalf("receipt clock regressed across Snapshot restart: before %d after %d", beforeClock, afterClock)
	}
	if length, capacity, evicted := restarted.runtime.MutationLogStats(); length != beforeLength ||
		capacity != beforeCapacity || evicted != beforeEvicted {
		t.Fatalf("restarted Snapshot log = %d/%d evicted %d, want %d/%d evicted %d",
			length, capacity, evicted, beforeLength, beforeCapacity, beforeEvicted)
	}
	afterGeneration, err := os.ReadFile(joinConfig.Path + ".generation")
	if err != nil || !bytes.Equal(afterGeneration, beforeGeneration) {
		t.Fatalf("restarted receipt generation = %x, want %x: %v", afterGeneration, beforeGeneration, err)
	}
	restartedStatus := waitForPublicReceiptCut(t, ctx, "restarted snapshot joiner", restarted, token, joinedCut, 5*time.Second)
	requireExactReceiptCut(t, "restarted snapshot joiner", restartedStatus, joinedCut)
	for _, row := range restartedStatus.GetOrigins() {
		key := hex.EncodeToString(row.GetOrigin())
		if !proto.Equal(row, beforeOrigins[key]) {
			t.Fatalf("restarted origin %s = %+v, want %+v", key, row, beforeOrigins[key])
		}
	}
	requireStatuses("restarted snapshot joiner (retired epoch)", restarted)
	requireGraph("restarted snapshot joiner", restarted)
	afterCurrent := receiptWireStatuses(t, ctx, restarted, token, currentContext.GetOperationIds())[0]
	if afterCurrent.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		!proto.Equal(afterCurrent.GetReceipt(), beforeCurrent.GetReceipt()) {
		t.Fatalf("restarted new-epoch receipt = %+v, want %+v", afterCurrent, beforeCurrent)
	}
	newVertex, err := restarted.raw.GetVertex(ctx, receiptRequestWithToken(
		&pb.GetVertexRequest{Key: "cluster/new"}, token,
	))
	if err != nil || newVertex.Msg.GetVertex().GetString_() != "current" {
		t.Fatalf("restarted new-epoch graph = (%+v, %v)", newVertex, err)
	}
	stale := receiptWireStatuses(t, ctx, restarted, token, staleContext.GetOperationIds())[0]
	if stale.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE ||
		stale.GetReceipt() != nil {
		t.Fatalf("restarted unknown retired receipt = %+v", stale)
	}
	if _, err := restarted.raw.GetVertex(ctx, receiptRequestWithToken(
		&pb.GetVertexRequest{Key: "cluster/stale"}, token,
	)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("restarted joiner admitted rejected old-epoch write: %v", err)
	}

	restartCapability := publicReceiptCapability(t, restarted, token)
	restartContext := publicReceiptWireContext(t, restartCapability, 0xd5, 1,
		time.UnixMilli(int64(restartCapability.GetServerNowUnixMs())).Add(-time.Second))
	restartPut, err := restarted.raw.PutVertex(ctx, receiptRequestWithToken(&pb.PutVertexRequest{
		Vertex:         &pb.Vertex{Key: "cluster/restarted", Value: &pb.Vertex_String_{String_: "live"}},
		ReceiptContext: restartContext,
	}, token))
	if err != nil || restartPut.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("post-restart new-epoch PutVertex = (%+v, %v)", restartPut, err)
	}
	restartStatus := receiptWireStatuses(t, ctx, restarted, token, restartContext.GetOperationIds())[0]
	if restartStatus.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		restartStatus.GetReceipt().GetOriginalResult().GetPutVertexOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("post-restart new-epoch receipt = %+v", restartStatus)
	}
	live, err := restarted.raw.GetVertex(ctx, receiptRequestWithToken(
		&pb.GetVertexRequest{Key: "cluster/restarted"}, token,
	))
	if err != nil || live.Msg.GetVertex().GetString_() != "live" {
		t.Fatalf("post-restart new-epoch graph = (%+v, %v)", live, err)
	}
	afterWrite, err := newReplicationRawClient(t, restarted.server.url).PeerStatus(
		ctx, receiptRequestWithToken(&pb.PeerStatusRequest{}, token),
	)
	if err != nil {
		t.Fatal(err)
	}
	var local *pb.OriginState
	for _, row := range afterWrite.Msg.GetOrigins() {
		if bytes.Equal(row.GetOrigin(), joinConfig.NodeID[:]) {
			local = row
		}
	}
	if local == nil || local.GetLastSeq() != 1 ||
		local.GetLastHlc().GetWallNs() <= beforeClock*int64(time.Millisecond) ||
		!bytes.Equal(local.GetLastHlc().GetNodeId(), joinConfig.NodeID[:]) {
		t.Fatalf("post-restart local HLC did not advance past receipt clock %d: %+v", beforeClock, local)
	}
}

type clusterReceiptSnapshotMetrics struct{ installed chan struct{} }

func (*clusterReceiptSnapshotMetrics) OnPumpConnect(string)            {}
func (*clusterReceiptSnapshotMetrics) OnPumpDisconnect(string, string) {}
func (*clusterReceiptSnapshotMetrics) OnPumpApply(string)              {}
func (*clusterReceiptSnapshotMetrics) OnPumpDropSelfEcho(string)       {}
func (*clusterReceiptSnapshotMetrics) OnSearchConfig(string, bool)     {}
func (m *clusterReceiptSnapshotMetrics) OnPumpSnapshotReplayed(string, uint64, uint64, time.Duration) {
	select {
	case m.installed <- struct{}{}:
	default:
	}
}

func newClusterRecoveryPublicReceiptServer(
	t *testing.T,
	config service.DurableReceiptWALRuntimeConfig,
	token string,
) publicReceiptWireServer {
	t.Helper()
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	return mountClusterRecoveryPublicReceiptServer(t, config, token, runtime, provider.ReceiptWALModeFresh)
}

func openClusterRecoveryPublicReceiptServer(
	t *testing.T,
	config service.DurableReceiptWALRuntimeConfig,
	token string,
) publicReceiptWireServer {
	t.Helper()
	runtime, err := service.OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	return mountClusterRecoveryPublicReceiptServer(t, config, token, runtime, provider.ReceiptWALModeRestart)
}

func mountClusterRecoveryPublicReceiptServer(
	t *testing.T,
	config service.DurableReceiptWALRuntimeConfig,
	token string,
	runtime *service.ServingRuntime,
	mode provider.ReceiptWALMode,
) publicReceiptWireServer {
	t.Helper()
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close recovered receipt runtime: %v", err)
		}
	})
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(2 * time.Hour)
	replicationService, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	replicationService.WithSearchConfig(primary)
	restored, err := provider.NewRuntimeRestored(runtime, primary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := provider.NewRuntimeCertified(
		runtime, primary, replicationService, restored, provider.NetConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptConfig := provider.ReceiptWALConfig{
		Mode: mode, Path: config.Path,
		Epoch: config.Receipt.Epoch, Retention: config.Receipt.Retention,
		MaxEntries: config.Receipt.MaxEntries, MaxBytes: int(config.Receipt.MaxBytes),
	}
	backupper, err := provider.NewBackupper(
		backup.Config{}, receiptConfig, runtime, primary, certified, prometheus.NewRegistry(), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	auth := provider.AuthConfig{Tokens: []string{token}}
	if _, err := provider.NewPublicReceiptsCertified(
		receiptConfig, auth, runtime, primary, backupper, certified,
	); err != nil {
		t.Fatal(err)
	}
	server := newConnectTestServer(t, primary, replicationService, provider.NewAuthInterceptor(auth))
	return publicReceiptWireServer{
		runtime: runtime,
		server:  server,
		raw:     graphv1connect.NewLanternServiceClient(h2cClient(), server.url),
		config:  config,
	}
}
