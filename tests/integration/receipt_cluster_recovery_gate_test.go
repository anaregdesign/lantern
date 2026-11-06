package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/backup"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPublicReceiptClusterLossBackupRestore_RealConnectWire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-node receipt backup recovery")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	origin := newClusterRecoveryReceiptProtocolServer(t, clusterRecoveryReceiptConfig(filepath.Join(t.TempDir(), "origin.wal"), hlc.NodeID{0xa1}))
	follower := newClusterRecoveryReceiptProtocolServer(t, clusterRecoveryReceiptConfig(filepath.Join(t.TempDir(), "follower.wal"), hlc.NodeID{0xb2}))
	partitioned := newClusterRecoveryReceiptProtocolServer(t, clusterRecoveryReceiptConfig(filepath.Join(t.TempDir(), "partitioned.wal"), hlc.NodeID{0xe5}))
	stopFollower := startClusterRecoveryReceiptPump(t, ctx, "receipt backup follower", follower, origin, nil)
	defer stopFollower()

	oldCapability := receiptProtocolCapability(t, origin)
	partitionedCapability := receiptProtocolCapability(t, partitioned)
	if !proto.Equal(partitionedCapability.GetPolicy(), oldCapability.GetPolicy()) ||
		proto.Equal(partitionedCapability.GetEndpoint(), oldCapability.GetEndpoint()) {
		t.Fatalf("partitioned replica policy/endpoint = %+v, want A policy and independent endpoint",
			partitionedCapability)
	}
	issued := time.UnixMilli(int64(oldCapability.GetServerNowUnixMs())).Add(-time.Second)
	putContext := receiptProtocolContext(t, oldCapability, 0xa1, 4, issued)
	put, err := origin.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{
			{Key: "cluster/live", Value: &pb.Vertex_String_{String_: "saved"}},
			{Key: "cluster/deleted", Value: &pb.Vertex_String_{String_: "removed"}},
			{Key: "cluster/add-deleted", Value: &pb.Vertex_String_{String_: "add then delete"}},
			{Key: "cluster/add-live", Value: &pb.Vertex_String_{String_: "add survives"}},
		},
		ReceiptContext: putContext,
	}))
	if err != nil || !reflect.DeepEqual(put.Msg.GetOutcomes(), []pb.PutOutcome{
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
	}) {
		t.Fatalf("receipt PutVertices = (%+v, %v)", put, err)
	}
	stopPartitioned := startClusterRecoveryReceiptPump(t, ctx, "receipt backup partitioned replica", partitioned, origin, nil)
	defer stopPartitioned()
	aOrigin := hex.EncodeToString(origin.config.NodeID[:])
	seedCut := map[string]uint64{aOrigin: 1}
	requireExactReceiptCut(t, "C before partition", waitForClusterRecoveryReceiptProtocolCut(t, ctx, "C before partition", partitioned, seedCut, 5*time.Second), seedCut)
	seed, err := partitioned.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/live"}))
	if err != nil || seed.Msg.GetVertex().GetString_() != "saved" {
		t.Fatalf("C seed vertex before partition = (%+v, %v)", seed, err)
	}
	stopPartitioned()

	edge := &pb.EdgeKey{Tail: "cluster/live", Head: "cluster/deleted"}
	putReceiptProtocolEdges(t, origin.raw, edge)
	edgeContext := receiptProtocolContext(t, oldCapability, 0xa2, 1, issued)
	deletedEdge, err := origin.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{
		Tail: edge.GetTail(), Head: edge.GetHead(), ReceiptContext: edgeContext,
	}))
	if err != nil || !deletedEdge.Msg.GetExisted() {
		t.Fatalf("receipt DeleteEdge = (%+v, %v)", deletedEdge, err)
	}
	vertexContext := receiptProtocolContext(t, oldCapability, 0xa3, 2, issued)
	deletedVertices, err := origin.raw.DeleteVertices(ctx, connect.NewRequest(&pb.DeleteVerticesRequest{
		Keys: []string{"cluster/deleted", "cluster/absent"}, ReceiptContext: vertexContext,
	}))
	if err != nil || deletedVertices.Msg.GetDeleted() != 1 ||
		!reflect.DeepEqual(deletedVertices.Msg.GetExisted(), []bool{true, false}) {
		t.Fatalf("receipt DeleteVertices = (%+v, %v)", deletedVertices, err)
	}

	addContext := receiptProtocolContext(t, oldCapability, 0xa4, 2, issued)
	addEdges := []*pb.Edge{
		{Tail: "cluster/live", Head: "cluster/add-deleted", Weight: 2,
			Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		{Tail: "cluster/live", Head: "cluster/add-live", Weight: 3,
			Expiration: timestamppb.New(time.Now().Add(time.Hour))},
	}
	add, err := origin.raw.AddEdges(ctx, connect.NewRequest(&pb.AddEdgesRequest{
		Edges: addEdges,
		ContribIds: [][]byte{
			bytes.Repeat([]byte{0xa4}, len(graphcache.ContribID{})),
			bytes.Repeat([]byte{0xa5}, len(graphcache.ContribID{})),
		},
		ReceiptContext: addContext,
	}))
	addWeights := []float32{2, 3}
	if err != nil || add.Msg.GetWritten() != 2 ||
		!reflect.DeepEqual(add.Msg.GetEffectiveWeights(), addWeights) {
		t.Fatalf("receipt AddEdges = (%+v, %v), want %v", add, err, addWeights)
	}
	addCut := map[string]uint64{aOrigin: 5}
	requireExactReceiptCut(t, "B confirmed Add", waitForClusterRecoveryReceiptProtocolCut(t, ctx, "B confirmed Add", follower, addCut, 5*time.Second), addCut)
	requireReceiptAddResults(t, "B confirmed Add", receiptProtocolStatuses(t, ctx, follower, addContext.GetOperationIds()), addContext.GetOperationIds(), addWeights)
	requireExactReceiptCut(t, "C partitioned from Add", waitForClusterRecoveryReceiptProtocolCut(t, ctx, "C partitioned from Add", partitioned, seedCut, 5*time.Second), seedCut)
	for i, status := range receiptProtocolStatuses(t, ctx, partitioned, addContext.GetOperationIds()) {
		if status.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED ||
			!bytes.Equal(status.GetOperationId(), addContext.GetOperationIds()[i]) ||
			status.GetReceipt() != nil {
			t.Fatalf("partitioned C Add status[%d] = %+v, want NOT_YET_OBSERVED", i, status)
		}
	}
	if _, err := partitioned.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
		Tail: addEdges[1].GetTail(), Head: addEdges[1].GetHead(),
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("partitioned C saw Add graph: %v", err)
	}

	deletedAdd, err := origin.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{
		Tail: addEdges[0].GetTail(), Head: addEdges[0].GetHead(),
	}))
	if err != nil || !deletedAdd.Msg.GetExisted() {
		t.Fatalf("DeleteEdge after confirmed Add = (%+v, %v), want existed", deletedAdd, err)
	}

	originCut := map[string]uint64{aOrigin: 6}
	requireExactReceiptCut(t, "converged backup follower", waitForClusterRecoveryReceiptProtocolCut(t, ctx, "converged backup follower", follower, originCut, 5*time.Second), originCut)
	operationIDs := append(append([][]byte{}, putContext.GetOperationIds()...), edgeContext.GetOperationIds()...)
	operationIDs = append(operationIDs, vertexContext.GetOperationIds()...)
	operationIDs = append(operationIDs, addContext.GetOperationIds()...)
	wantResults := []*pb.ReceiptResult{
		{Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}},
		{Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}},
		{Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}},
		{Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}},
		{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: true}},
		{Result: &pb.ReceiptResult_DeleteVertexExisted{DeleteVertexExisted: true}},
		{Result: &pb.ReceiptResult_DeleteVertexExisted{DeleteVertexExisted: false}},
		{Result: &pb.ReceiptResult_AddEdgeEffectiveWeight{AddEdgeEffectiveWeight: 2}},
		{Result: &pb.ReceiptResult_AddEdgeEffectiveWeight{AddEdgeEffectiveWeight: 3}},
	}
	original := receiptProtocolStatuses(t, ctx, follower, operationIDs)
	requireStatuses := func(name string, node receiptProtocolWireServer) {
		t.Helper()
		statuses := receiptProtocolStatuses(t, ctx, node, operationIDs)
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
	requireGraph := func(name string, node receiptProtocolWireServer) {
		t.Helper()
		live, err := node.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/live"}))
		if err != nil || live.Msg.GetVertex().GetString_() != "saved" {
			t.Fatalf("%s live vertex = (%+v, %v)", name, live, err)
		}
		if _, err := node.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/deleted"})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("%s deleted vertex = %v, want NotFound", name, err)
		}
		if _, err := node.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: edge.GetTail(), Head: edge.GetHead()})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("%s deleted edge = %v, want NotFound", name, err)
		}
		if _, err := node.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: addEdges[0].GetTail(), Head: addEdges[0].GetHead()})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("%s deleted Add edge = %v, want NotFound despite original result 2", name, err)
		}
		liveAdd, err := node.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: addEdges[1].GetTail(), Head: addEdges[1].GetHead()}))
		if err != nil || liveAdd.Msg.GetEdge().GetWeight() != 3 {
			t.Fatalf("%s surviving Add edge = (%+v, %v), want weight 3", name, liveAdd, err)
		}
	}
	requireStatuses("origin", origin)
	requireStatuses("backed-up follower", follower)
	requireGraph("origin", origin)
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
	if err != nil || stats.Vertices != 3 || stats.Edges != 1 ||
		stats.Receipts != len(operationIDs) || stats.Members != 3 {
		t.Fatalf("converged follower backup = (%+v, %v)", stats, err)
	}

	stopFollower()
	for _, node := range []receiptProtocolWireServer{origin, follower, partitioned} {
		node.server.srv.Close()
		if err := node.runtime.Close(); err != nil {
			t.Fatalf("close original cluster node: %v", err)
		}
	}
	evidence, err := backup.LoadLatestReceiptBackupSet(backupDir, instance)
	if err != nil || evidence.WALCut.Seq == 0 || evidence.Stats.Receipts != len(operationIDs) {
		t.Fatalf("loaded receipt backup after total-cluster loss = (%+v, %v)", evidence, err)
	}

	restoredConfig := clusterRecoveryReceiptConfig(filepath.Join(t.TempDir(), "restored.wal"), hlc.NodeID{0xc3})
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
	restored := newClusterRecoveryReceiptProtocolServer(t, restoredConfig)
	restoredCapability := receiptProtocolCapability(t, restored)
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
	beforeCut := receiptOriginCut(waitForClusterRecoveryReceiptProtocolCut(t, ctx, "fresh restored origin cut", restored, originCut, 5*time.Second))
	staleContext := receiptProtocolContext(t, oldCapability, 0xaf, 1, issued)
	staleContext.Endpoint = proto.Clone(restoredCapability.GetEndpoint()).(*pb.ReceiptEndpoint)
	if _, err := restored.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{
		Vertex: &pb.Vertex{
			Key: "cluster/stale", Value: &pb.Vertex_String_{String_: "must not land"},
		},
		ReceiptContext: staleContext,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition ||
		!strings.Contains(err.Error(), "outside the active receipt epoch") {
		t.Fatalf("old-epoch PutVertex with current endpoint = %v, want epoch FailedPrecondition", err)
	}
	afterWAL, err := os.Stat(restoredConfig.Path)
	if err != nil {
		t.Fatal(err)
	}
	afterStatus, err := newReplicationRawClient(t, restored.server.url).PeerStatus(
		ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"}),
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
	if _, err := restored.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/stale"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("rejected old-epoch write changed graph: GetVertex = %v", err)
	}
	staleStatus := receiptProtocolStatuses(t, ctx, restored, staleContext.GetOperationIds())[0]
	if staleStatus.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE ||
		staleStatus.GetReceipt() != nil {
		t.Fatalf("unknown retired-epoch ID = %+v, want NO_LONGER_PROVABLE without receipt", staleStatus)
	}
	requireStatuses("restored node after rejection", restored)
	if length, _, evicted := restored.runtime.MutationLogStats(); length != 0 || evicted == 0 {
		t.Fatalf("fresh restore replay log = len %d evicted %d, want a committed boundary with no retained tail",
			length, evicted)
	}

	joinConfig := clusterRecoveryReceiptConfig(filepath.Join(t.TempDir(), "joiner.wal"), hlc.NodeID{0xd4})
	joinConfig.Receipt.Epoch = restoredConfig.Receipt.Epoch
	joiner := newClusterRecoveryReceiptProtocolServer(t, joinConfig)
	snapshotMetrics := &clusterReceiptSnapshotMetrics{installed: make(chan struct{}, 1)}
	stopJoiner := startClusterRecoveryReceiptPump(t, ctx, "recovered receipt snapshot", joiner, restored, snapshotMetrics)
	defer stopJoiner()
	select {
	case <-snapshotMetrics.installed:
	case <-ctx.Done():
		t.Fatalf("joiner did not install a RECEIPT Snapshot: %v", ctx.Err())
	case <-time.After(5 * time.Second):
		t.Fatal("joiner did not install a RECEIPT Snapshot; WAL replay cannot prove recovery")
	}
	waitForClusterRecoveryReceiptProtocolCut(t, ctx, "new replica recovered", joiner, originCut, 5*time.Second)
	requireStatuses("rejoined node (retired epoch)", joiner)
	requireGraph("rejoined node", joiner)

	currentContext := receiptProtocolContext(t, restoredCapability, 0xc4, 1,
		time.UnixMilli(int64(restoredCapability.GetServerNowUnixMs())).Add(-time.Second))
	current, err := restored.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{
		Vertex:         &pb.Vertex{Key: "cluster/new", Value: &pb.Vertex_String_{String_: "current"}},
		ReceiptContext: currentContext,
	}))
	if err != nil || current.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("new-epoch PutVertex = (%+v, %v)", current, err)
	}
	joinedCut := map[string]uint64{
		hex.EncodeToString(origin.config.NodeID[:]):   6,
		hex.EncodeToString(restored.config.NodeID[:]): 1,
	}
	joinedStatus := waitForClusterRecoveryReceiptProtocolCut(t, ctx, "new-epoch tail on joiner", joiner, joinedCut, 5*time.Second)
	requireExactReceiptCut(t, "new-epoch tail on joiner", joinedStatus, joinedCut)
	for _, node := range []struct {
		name string
		wire receiptProtocolWireServer
	}{
		{"restored node", restored},
		{"rejoined node", joiner},
	} {
		currentStatus := receiptProtocolStatuses(t, ctx, node.wire, currentContext.GetOperationIds())[0]
		if currentStatus.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
			currentStatus.GetReceipt().GetOriginalResult().GetPutVertexOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
			t.Fatalf("%s current-epoch receipt = %+v", node.name, currentStatus)
		}
		live, err := node.wire.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/new"}))
		if err != nil || live.Msg.GetVertex().GetString_() != "current" {
			t.Fatalf("%s new-epoch graph = (%+v, %v)", node.name, live, err)
		}
		requireStatuses(node.name+" after new epoch write", node.wire)
	}

	beforeCurrent := receiptProtocolStatuses(t, ctx, joiner, currentContext.GetOperationIds())[0]
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
	restarted := openClusterRecoveryReceiptProtocolServer(t, joinConfig)
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
	restartedStatus := waitForClusterRecoveryReceiptProtocolCut(t, ctx, "restarted snapshot joiner", restarted, joinedCut, 5*time.Second)
	requireExactReceiptCut(t, "restarted snapshot joiner", restartedStatus, joinedCut)
	for _, row := range restartedStatus.GetOrigins() {
		key := hex.EncodeToString(row.GetOrigin())
		if !proto.Equal(row, beforeOrigins[key]) {
			t.Fatalf("restarted origin %s = %+v, want %+v", key, row, beforeOrigins[key])
		}
	}
	requireStatuses("restarted snapshot joiner (retired epoch)", restarted)
	requireGraph("restarted snapshot joiner", restarted)
	afterCurrent := receiptProtocolStatuses(t, ctx, restarted, currentContext.GetOperationIds())[0]
	if afterCurrent.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		!proto.Equal(afterCurrent.GetReceipt(), beforeCurrent.GetReceipt()) {
		t.Fatalf("restarted new-epoch receipt = %+v, want %+v", afterCurrent, beforeCurrent)
	}
	newVertex, err := restarted.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/new"}))
	if err != nil || newVertex.Msg.GetVertex().GetString_() != "current" {
		t.Fatalf("restarted new-epoch graph = (%+v, %v)", newVertex, err)
	}
	stale := receiptProtocolStatuses(t, ctx, restarted, staleContext.GetOperationIds())[0]
	if stale.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE ||
		stale.GetReceipt() != nil {
		t.Fatalf("restarted unknown retired receipt = %+v", stale)
	}
	if _, err := restarted.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/stale"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("restarted joiner admitted rejected old-epoch write: %v", err)
	}

	restartCapability := receiptProtocolCapability(t, restarted)
	restartContext := receiptProtocolContext(t, restartCapability, 0xd5, 1,
		time.UnixMilli(int64(restartCapability.GetServerNowUnixMs())).Add(-time.Second))
	restartPut, err := restarted.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{
		Vertex:         &pb.Vertex{Key: "cluster/restarted", Value: &pb.Vertex_String_{String_: "live"}},
		ReceiptContext: restartContext,
	}))
	if err != nil || restartPut.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("post-restart new-epoch PutVertex = (%+v, %v)", restartPut, err)
	}
	restartStatus := receiptProtocolStatuses(t, ctx, restarted, restartContext.GetOperationIds())[0]
	if restartStatus.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		restartStatus.GetReceipt().GetOriginalResult().GetPutVertexOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("post-restart new-epoch receipt = %+v", restartStatus)
	}
	live, err := restarted.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "cluster/restarted"}))
	if err != nil || live.Msg.GetVertex().GetString_() != "live" {
		t.Fatalf("post-restart new-epoch graph = (%+v, %v)", live, err)
	}
	afterWrite, err := newReplicationRawClient(t, restarted.server.url).PeerStatus(
		ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"}),
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

func newClusterRecoveryReceiptProtocolServer(
	t *testing.T,
	config service.DurableReceiptWALRuntimeConfig,
) receiptProtocolWireServer {
	t.Helper()
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	return mountClusterRecoveryReceiptProtocolServer(t, config, runtime, provider.ReceiptWALModeFresh)
}

func openClusterRecoveryReceiptProtocolServer(
	t *testing.T,
	config service.DurableReceiptWALRuntimeConfig,
) receiptProtocolWireServer {
	t.Helper()
	runtime, err := service.OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	return mountClusterRecoveryReceiptProtocolServer(t, config, runtime, provider.ReceiptWALModeRestart)
}

func mountClusterRecoveryReceiptProtocolServer(
	t *testing.T,
	config service.DurableReceiptWALRuntimeConfig,
	runtime *service.ServingRuntime,
	mode provider.ReceiptWALMode,
) receiptProtocolWireServer {
	t.Helper()
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close recovered receipt runtime: %v", err)
		}
	})
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(2 * time.Hour)
	replicationService, err := runtime.NewReplication(primary)
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
	_, err = provider.NewBackupper(
		backup.Config{}, receiptConfig, runtime, primary, certified, prometheus.NewRegistry(), nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	server := newConnectTestServer(t, primary, replicationService)
	return receiptProtocolWireServer{
		runtime: runtime,
		server:  server,
		raw:     graphv1connect.NewLanternServiceClient(h2cClient(), server.url),
		config:  config,
	}
}

// Cluster recovery exercises explicitly activated receipt protocol services.
// Private tail admission remains separate: the senders below use the actual
// signed-membership and mTLS fixture, never public credentials.
func clusterRecoveryReceiptConfig(path string, nodeID hlc.NodeID) service.DurableReceiptWALRuntimeConfig {
	config := durableReceiptWireConfig(path, nodeID)
	config.NamespaceFormat = "namespaced-v1"
	return config
}

func startClusterRecoveryReceiptPump(
	t *testing.T,
	parent context.Context,
	name string,
	target, source receiptProtocolWireServer,
	metrics replication.Metrics,
) func() {
	t.Helper()
	if source.config.NamespaceFormat != "namespaced-v1" || target.config.NamespaceFormat != source.config.NamespaceFormat {
		t.Fatal("native recovery requires matching namespaced receipt runtimes")
	}
	qualifiedUTC, clockOwner, started := time.Now().UTC(), new(byte), time.Now()
	native := newPeerSecurityFixture(t, peerSecurityOptions{
		Now:        func() time.Time { return qualifiedUTC },
		TimeBounds: func() (time.Time, time.Time, error) { return qualifiedUTC, qualifiedUTC, nil },
		Time: func() (provider.PeerTimeObservation, error) {
			return peerWireObservation{clockOwner, qualifiedUTC, qualifiedUTC, time.Since(started)}, nil
		},
		IssuedBefore: time.Minute,
		Mount: func(_ *peerSecurityFixture, index int, _ *service.ServingRuntime, mux *http.ServeMux) {
			if index != 0 {
				return
			}
			_, handler := graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(source.server.rep))
			for _, method := range []string{"PeerStatus", "Subscribe", "Snapshot"} {
				mux.Handle("/graph.v1.LanternReplicationService/"+method, handler)
			}
		},
	})
	t.Cleanup(source.server.rep.CloseSnapshotTails)
	transport, err := provider.NewWorkloadPeerTransport(native.runtimes[1])
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	pump := replication.NewPump(replication.Config{
		NodeID: target.config.NodeID, NamespaceFormat: target.config.NamespaceFormat,
		Peers: []string{native.servers[0].URL}, PeerTransport: transport,
		BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond,
		SnapshotInstaller:       newDurableReceiptSnapshotInstaller(t, target.config, target.server),
		SearchConfigFingerprint: target.server.svc.SearchConfigFingerprint(), Metrics: metrics,
	}, target.server.svc, target.runtime.GraphCache())
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
				t.Errorf("%s native recovery owner did not join", name)
			}
		})
	}
}

func waitForClusterRecoveryReceiptProtocolCut(
	t *testing.T,
	ctx context.Context,
	name string,
	node receiptProtocolWireServer,
	want map[string]uint64,
	timeout time.Duration,
) *pb.PeerStatusResponse {
	t.Helper()
	client := newReplicationRawClient(t, node.server.url)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var last *pb.PeerStatusResponse
	var lastErr error
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		response, err := client.PeerStatus(probeCtx, connect.NewRequest(&pb.PeerStatusRequest{
			NamespaceFormat: node.config.NamespaceFormat,
		}))
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
			t.Fatalf("%s namespaced receipt cut canceled: want=%v got=%v rpc=%v: %v", name, want, receiptOriginCut(last), lastErr, ctx.Err())
		case <-deadline.C:
			t.Fatalf("%s namespaced receipt cut timeout: want=%v got=%v rpc=%v", name, want, receiptOriginCut(last), lastErr)
		case <-ticker.C:
		}
	}
}
