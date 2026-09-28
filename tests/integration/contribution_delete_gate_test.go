package integration_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/proto"
)

func wireContributionKey(tail, head string, id []byte) *pb.EdgeContributionKey {
	return &pb.EdgeContributionKey{Tail: tail, Head: head, ContribId: id}
}

func TestContributionDelete_ExactOutcomesAndAdmissionRealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	node := newAuthedPumpNode(t, hlc.NodeID{0xe1}, 32)
	first := bytes.Repeat([]byte{0x11}, 24)
	second := bytes.Repeat([]byte{0x22}, 24)
	absent := bytes.Repeat([]byte{0x33}, 24)
	if _, err := node.raw.AddEdges(ctx, authedReceiptRequest(&pb.AddEdgesRequest{
		Edges: []*pb.Edge{
			{Tail: "tail", Head: "head", Weight: 3},
			{Tail: "tail", Head: "head", Weight: 5},
		},
		ContribIds: [][]byte{first, second},
	})); err != nil {
		t.Fatal(err)
	}
	keys := []*pb.EdgeContributionKey{
		wireContributionKey("tail", "head", first),
		wireContributionKey("tail", "head", absent),
		wireContributionKey("tail", "head", first),
	}
	resp, err := node.raw.DeleteEdgeContributions(ctx, authedReceiptRequest(
		&pb.DeleteEdgeContributionsRequest{Contributions: keys},
	))
	if err != nil || resp.Msg.GetDeleted() != 1 ||
		!slices.Equal(resp.Msg.GetExisted(), []bool{true, false, false}) {
		t.Fatalf("plural contribution Delete = (%+v, %v)", resp, err)
	}
	if edge, err := node.raw.GetEdge(ctx, authedReceiptRequest(
		&pb.GetEdgeRequest{Tail: "tail", Head: "head"},
	)); err != nil || edge.Msg.GetEdge().GetWeight() != 5 {
		t.Fatalf("surviving contribution = (%+v, %v), want weight 5", edge, err)
	}
	if _, err := node.raw.AddEdge(ctx, authedReceiptRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "tail", Head: "head", Weight: 7}, ContribId: absent,
	})); err != nil {
		t.Fatal(err)
	}
	if weight, ok := node.cache.GetWeight("tail", "head"); !ok || weight != 5 {
		t.Fatalf("absent ID resurrected after targeted Delete: weight=%g live=%t", weight, ok)
	}
	one, err := node.raw.DeleteEdgeContribution(ctx, authedReceiptRequest(
		&pb.DeleteEdgeContributionRequest{Tail: "tail", Head: "head", ContribId: second},
	))
	if err != nil || !one.Msg.GetExisted() {
		t.Fatalf("singular contribution Delete = (%+v, %v)", one, err)
	}
	if _, err := node.raw.GetEdge(ctx, authedReceiptRequest(
		&pb.GetEdgeRequest{Tail: "tail", Head: "head"},
	)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("last contribution still visible: %v", err)
	}

	before := node.svc.LocalSeq(node.nodeID)
	for _, invalid := range []byte{0, 1} {
		var id []byte
		if invalid == 0 {
			id = first[:23]
		} else {
			id = make([]byte, 24)
		}
		_, err := node.raw.DeleteEdgeContribution(ctx, authedReceiptRequest(
			&pb.DeleteEdgeContributionRequest{Tail: "tail", Head: "head", ContribId: id},
		))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("invalid contribution ID %x accepted: %v", id, err)
		}
	}
	if _, err := node.raw.DeleteEdgeContributions(ctx, authedReceiptRequest(
		&pb.DeleteEdgeContributionsRequest{Contributions: keys[:1]},
	)); err != nil {
		t.Fatalf("valid repeated Delete: %v", err)
	}
	if got := node.svc.LocalSeq(node.nodeID); got != before+1 {
		t.Fatalf("invalid calls changed local seq: before=%d after=%d", before, got)
	}
}

func TestContributionDelete_AtomicCausalCapacityRealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	node := newAuthedPumpNode(t, hlc.NodeID{0xe2}, 32)
	id := bytes.Repeat([]byte{0x11}, 24)
	absent := bytes.Repeat([]byte{0x33}, 24)
	if _, err := node.raw.AddEdge(ctx, authedReceiptRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "tail", Head: "head", Weight: 3}, ContribId: id,
	})); err != nil {
		t.Fatal(err)
	}
	node.cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
	before := node.svc.LocalSeq(node.nodeID)
	resp, err := node.raw.DeleteEdgeContributions(ctx, authedReceiptRequest(
		&pb.DeleteEdgeContributionsRequest{Contributions: []*pb.EdgeContributionKey{
			wireContributionKey("tail", "head", id),
			wireContributionKey("tail", "head", absent),
		}},
	))
	if resp != nil || connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("over-capacity Delete = (%+v, %v), want nil/ResourceExhausted", resp, err)
	}
	if got := node.svc.LocalSeq(node.nodeID); got != before {
		t.Fatalf("rejected Delete advanced origin %d -> %d", before, got)
	}
	if weight, live := node.cache.GetWeight("tail", "head"); !live || weight != 3 {
		t.Fatalf("rejected Delete changed graph: weight=%g live=%t", weight, live)
	}
}

func TestContributionDelete_PartitionAndIdentityInvalidationRealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	a := newConvergenceNode(t, hlc.NodeID{0xe3}, time.Hour)
	b := newConvergenceNode(t, hlc.NodeID{0xe4}, time.Hour)
	aRaw := graphv1connect.NewLanternServiceClient(h2cClient(), a.url)
	bRaw := graphv1connect.NewLanternServiceClient(h2cClient(), b.url)
	first := bytes.Repeat([]byte{0x44}, 24)
	second := bytes.Repeat([]byte{0x55}, 24)
	third := bytes.Repeat([]byte{0x66}, 24)
	if _, err := aRaw.AddEdges(ctx, connect.NewRequest(&pb.AddEdgesRequest{
		Edges: []*pb.Edge{
			{Tail: "partition", Head: "head", Weight: 3},
			{Tail: "partition", Head: "head", Weight: 5},
		}, ContribIds: [][]byte{first, second},
	})); err != nil {
		t.Fatal(err)
	}
	// B has not observed A's Add: this absent-target Delete must nevertheless
	// fence A's delayed contribution while leaving the other ID untouched.
	deleted, err := bRaw.DeleteEdgeContribution(ctx, connect.NewRequest(
		&pb.DeleteEdgeContributionRequest{Tail: "partition", Head: "head", ContribId: first},
	))
	if err != nil || deleted.Msg.GetExisted() {
		t.Fatalf("partitioned Delete = (%+v, %v), want existed=false", deleted, err)
	}
	cursor := map[string]uint64{hex.EncodeToString(b.nodeID[:]): 1}
	stream, err := newReplicationRawClient(t, b.url).Subscribe(ctx, connect.NewRequest(
		&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
			FromSeqPerOrigin: cursor},
	))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() {
		t.Fatalf("identity Subscribe: %v", stream.Err())
	}
	chunk := stream.Msg().GetIdentityChunk()
	frame, err := proto.Marshal(stream.Msg())
	if err != nil {
		t.Fatal(err)
	}
	if chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE_CONTRIBUTION ||
		len(chunk.GetEdgeKeys()) != 1 ||
		chunk.GetEdgeKeys()[0].GetTail() != "partition" ||
		chunk.GetEdgeKeys()[0].GetHead() != "head" ||
		bytes.Contains(frame, first) {
		t.Fatalf("wrong identity-only invalidation or leaked ContribID: %+v", chunk)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := bRaw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "partition", Head: "head", Weight: 7}, ContribId: third,
	})); err != nil {
		t.Fatal(err)
	}
	b.startPump(ctx, t, []string{a.url})
	deadline := time.Now().Add(5 * time.Second)
	for b.svc.LocalSeq(a.nodeID) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 1 {
		t.Fatalf("B did not receive delayed Add: frontier=%d", got)
	}
	if weight, ok := b.cache.GetWeight("partition", "head"); !ok || weight != 12 {
		t.Fatalf("B failed remove-wins convergence: weight=%g live=%t", weight, ok)
	}
	a.startPump(ctx, t, []string{b.url})
	deadline = time.Now().Add(5 * time.Second)
	for a.svc.LocalSeq(b.nodeID) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := a.svc.LocalSeq(b.nodeID); got != 2 {
		t.Fatalf("A did not receive Delete and independent Add: frontier=%d", got)
	}
	if weight, ok := a.cache.GetWeight("partition", "head"); !ok || weight != 12 {
		t.Fatalf("A failed remove-wins convergence: weight=%g live=%t", weight, ok)
	}
}

func TestContributionDelete_GapSnapshotFencesDelayedAddRealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	primary := newPumpNodeWithSearch(t, hlc.NodeID{0xe6}, 4, true)
	stale := newPumpNode(t, hlc.NodeID{0xe7})
	first := bytes.Repeat([]byte{0x77}, 24)
	second := bytes.Repeat([]byte{0x88}, 24)
	if _, err := stale.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
		Edge:      &pb.Edge{Tail: "snapshot", Head: "head", Weight: 3},
		ContribId: first,
	})); err != nil {
		t.Fatal(err)
	}
	primary.clock.Update(stale.clock.Now())
	deleted, err := primary.raw.DeleteEdgeContribution(ctx, connect.NewRequest(
		&pb.DeleteEdgeContributionRequest{Tail: "snapshot", Head: "head", ContribId: first},
	))
	if err != nil || deleted.Msg.GetExisted() {
		t.Fatalf("primary absent-target Delete = (%+v, %v)", deleted, err)
	}
	if _, err := primary.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
		Edge:      &pb.Edge{Tail: "snapshot", Head: "head", Weight: 5},
		ContribId: second,
	})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := primary.sdk.PutVertex(ctx, "evict/"+itoa(i), "live", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// The original Delete fell out of the log, so a new replica can only
	// recover its per-ID fence through Snapshot, not Subscribe.
	stream, err := newReplicationRawClient(t, primary.url).Snapshot(ctx, connect.NewRequest(
		&pb.SnapshotRequest{RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1},
	))
	if err != nil {
		t.Fatal(err)
	}
	var count uint64
	var footer *pb.SnapshotFooter
	for stream.Receive() {
		switch frame := stream.Msg().GetEntry().(type) {
		case *pb.SnapshotResponse_EdgeContributionTombstone:
			count++
			tombstone := frame.EdgeContributionTombstone
			if tombstone.GetTail() != "snapshot" || tombstone.GetHead() != "head" ||
				!bytes.Equal(tombstone.GetContribId(), first) ||
				!tombstone.GetExpiration().AsTime().After(time.Now()) {
				t.Fatalf("Snapshot lost absolute per-ID fence: %+v", tombstone)
			}
		case *pb.SnapshotResponse_Footer:
			footer = frame.Footer
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 1 || footer == nil || footer.GetEdgeContributionTombstoneCount() != count {
		t.Fatalf("per-ID Snapshot frames=%d footer=%+v", count, footer)
	}
	boot := newPumpNode(t, hlc.NodeID{0xe8})
	metrics := &tombstoneSnapshotMetrics{
		failed:    make(chan struct{}, 1),
		snapshots: make(chan struct{}, 1),
	}
	boot.startPumpWithMetrics(ctx, t, []string{primary.url}, metrics)
	select {
	case <-metrics.snapshots:
	case <-ctx.Done():
		t.Fatalf("bootstrap failed to recover gapped contribution Delete: %v", ctx.Err())
	}
	if weight, ok := boot.cache.GetWeight("snapshot", "head"); !ok || weight != 5 {
		t.Fatalf("bootstrapped independent Add = %g/%t, want 5/true", weight, ok)
	}
	boot.startPump(ctx, t, []string{stale.url})
	deadline := time.Now().Add(5 * time.Second)
	for boot.svc.LocalSeq(stale.nodeID) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := boot.svc.LocalSeq(stale.nodeID); got != 1 {
		t.Fatalf("boot node did not receive delayed Add: frontier=%d", got)
	}
	if weight, ok := boot.cache.GetWeight("snapshot", "head"); !ok || weight != 5 {
		t.Fatalf("delayed Add resurrected after Snapshot: weight=%g live=%t", weight, ok)
	}
}

func TestContributionDelete_ReceiptAuthDisabledRealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	wire := newPublicReceiptWireServer(t, hlc.NodeID{0xef}, 8)
	id := bytes.Repeat([]byte{0x71}, 24)
	if _, err := wire.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "auth-disabled", Head: "head", Weight: 3}, ContribId: id,
	})); err != nil {
		t.Fatal(err)
	}
	beforeStore := wire.runtime.ReceiptStats()
	beforeSeq := wire.server.svc.LocalSeq(hlc.NodeID{0xef})
	_, err := wire.raw.DeleteEdgeContribution(ctx, connect.NewRequest(
		&pb.DeleteEdgeContributionRequest{
			Tail: "auth-disabled", Head: "head", ContribId: id,
			ReceiptContext: &pb.MutationReceiptContext{},
		},
	))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("auth-disabled receipt Delete = %v, want FailedPrecondition", err)
	}
	if afterStore := wire.runtime.ReceiptStats(); afterStore != beforeStore ||
		wire.server.svc.LocalSeq(hlc.NodeID{0xef}) != beforeSeq {
		t.Fatalf("auth-disabled receipt Delete changed Store/origin: before=%+v/%d", beforeStore, beforeSeq)
	}
	if edge, err := wire.raw.GetEdge(ctx, connect.NewRequest(
		&pb.GetEdgeRequest{Tail: "auth-disabled", Head: "head"},
	)); err != nil || edge.Msg.GetEdge().GetWeight() != 3 {
		t.Fatalf("auth-disabled receipt Delete changed graph: (%+v, %v)", edge, err)
	}
}

func TestContributionDelete_ReceiptRetryAndStatusRealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	wire := newPublicReceiptWireServer(t, hlc.NodeID{0xe5}, 32, testToken)
	capability := publicReceiptCapability(t, wire, testToken)
	if !slices.Contains(capability.GetSupportedMutations(),
		pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_EDGE_CONTRIBUTION) {
		t.Fatalf("contribution Delete was not advertised by certified runtime: %+v", capability)
	}
	first := bytes.Repeat([]byte{0x11}, 24)
	absent := bytes.Repeat([]byte{0x33}, 24)
	if _, err := wire.raw.AddEdge(ctx, authedReceiptRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "receipt", Head: "head", Weight: 3}, ContribId: first,
	})); err != nil {
		t.Fatal(err)
	}
	receipt := publicReceiptWireContext(t, capability, 0x62, 2, time.Now())
	request := &pb.DeleteEdgeContributionsRequest{
		Contributions: []*pb.EdgeContributionKey{
			wireContributionKey("receipt", "head", first),
			wireContributionKey("receipt", "head", absent),
		},
		ReceiptContext: receipt,
	}
	got, err := wire.raw.DeleteEdgeContributions(ctx, authedReceiptRequest(request))
	if err != nil || got.Msg.GetDeleted() != 1 ||
		!slices.Equal(got.Msg.GetExisted(), []bool{true, false}) {
		t.Fatalf("initial receipt Delete = (%+v, %v)", got, err)
	}
	// Simulate a response lost at the caller, then mutate the edge. The exact
	// same logical call must report the original result, not re-run the Delete.
	if _, err := wire.raw.PutEdge(ctx, authedReceiptRequest(&pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "receipt", Head: "head", Weight: 9},
	})); err != nil {
		t.Fatal(err)
	}
	retry, err := wire.raw.DeleteEdgeContributions(ctx, authedReceiptRequest(request))
	if err != nil || retry.Msg.GetDeleted() != 1 ||
		!slices.Equal(retry.Msg.GetExisted(), []bool{true, false}) {
		t.Fatalf("same-operation receipt retry = (%+v, %v)", retry, err)
	}
	if edge, err := wire.raw.GetEdge(ctx, authedReceiptRequest(
		&pb.GetEdgeRequest{Tail: "receipt", Head: "head"},
	)); err != nil || edge.Msg.GetEdge().GetWeight() != 9 {
		t.Fatalf("receipt retry changed later Put base: (%+v, %v)", edge, err)
	}
	statuses := receiptWireStatuses(t, ctx, wire, testToken, receipt.GetOperationIds())
	for i, status := range statuses {
		result, ok := status.GetReceipt().GetOriginalResult().GetResult().(*pb.ReceiptResult_DeleteEdgeContributionExisted)
		if status.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
			!ok || result.DeleteEdgeContributionExisted != (i == 0) ||
			!bytes.Equal(status.GetOperationId(), receipt.GetOperationIds()[i]) {
			t.Fatalf("receipt status[%d] lost typed original result: %+v", i, status)
		}
	}

	endpoint := proto.Clone(capability.GetEndpoint()).(*pb.ReceiptEndpoint)
	wire.server.srv.Close()
	if err := wire.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	restartConfig := wire.config
	restartConfig.Now = time.Now()
	restartConfig.Receipt.ClockHighWater = restartConfig.Now
	restartedRuntime, err := service.OpenDurableReceiptWALServingRuntime(restartConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restartedRuntime.Close() })
	restarted := mountPublicReceiptWireRuntime(
		t, restartedRuntime, restartConfig, provider.NetConfig{}, 2*time.Hour,
		provider.ReceiptWALModeRestart, testToken,
	)
	if current := publicReceiptCapability(t, restarted, testToken); !proto.Equal(current.GetEndpoint(), endpoint) {
		t.Fatalf("receipt endpoint changed across clean WAL restart: before=%+v after=%+v", endpoint, current.GetEndpoint())
	}
	restoredStatuses := receiptWireStatuses(t, ctx, restarted, testToken, receipt.GetOperationIds())
	if !proto.Equal(&pb.GetReceiptStatusesResponse{Statuses: statuses},
		&pb.GetReceiptStatusesResponse{Statuses: restoredStatuses}) {
		t.Fatalf("original per-ID results changed after WAL restart: before=%+v after=%+v", statuses, restoredStatuses)
	}
	again, err := restarted.raw.DeleteEdgeContributions(ctx, authedReceiptRequest(request))
	if err != nil || again.Msg.GetDeleted() != 1 ||
		!slices.Equal(again.Msg.GetExisted(), []bool{true, false}) {
		t.Fatalf("same-operation retry after WAL restart = (%+v, %v)", again, err)
	}
	for _, id := range [][]byte{first, absent} {
		if _, err := restarted.raw.AddEdge(ctx, authedReceiptRequest(&pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "receipt", Head: "head", Weight: 4}, ContribId: id,
		})); err != nil {
			t.Fatal(err)
		}
	}
	if edge, err := restarted.raw.GetEdge(ctx, authedReceiptRequest(
		&pb.GetEdgeRequest{Tail: "receipt", Head: "head"},
	)); err != nil || edge.Msg.GetEdge().GetWeight() != 9 {
		t.Fatalf("WAL restart lost contribution tombstones: (%+v, %v)", edge, err)
	}

	source, policy, err := restarted.runtime.ReceiptWholeStateBackupSource(
		restarted.server.svc, restarted.server.rep,
	)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	const instance = "contribution-delete"
	producer, err := backup.NewReceipt(restarted.server.svc, source, policy, backup.Config{
		Enabled: true, Dir: dir, Interval: time.Hour, Retain: 1, InstanceID: instance,
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := producer.BackupNow(ctx); err != nil || stats.Receipts != 2 {
		t.Fatalf("certified backup after per-ID delete = (%+v, %v)", stats, err)
	}
	evidence, err := backup.LoadLatestReceiptBackupSet(dir, instance)
	if err != nil {
		t.Fatal(err)
	}
	freshConfig := durableReceiptWireConfig(filepath.Join(t.TempDir(), "restored.wal"), hlc.NodeID{0xd2})
	freshConfig.Receipt.Epoch = mutationreceipt.Epoch{0xd2}
	restore, err := backup.PrepareFreshReceiptStartupRestore(evidence, freshConfig.Receipt, freshConfig.Now)
	if err != nil {
		t.Fatal(err)
	}
	freshConfig.StartupRestore = &restore
	fresh := newClusterRecoveryPublicReceiptServer(t, freshConfig, testToken)
	freshStatuses := receiptWireStatuses(t, ctx, fresh, testToken, receipt.GetOperationIds())
	if !proto.Equal(&pb.GetReceiptStatusesResponse{Statuses: statuses},
		&pb.GetReceiptStatusesResponse{Statuses: freshStatuses}) {
		t.Fatalf("certified backup lost original results: before=%+v after=%+v", statuses, freshStatuses)
	}
	for _, id := range [][]byte{first, absent} {
		if _, err := fresh.raw.AddEdge(ctx, authedReceiptRequest(&pb.AddEdgeRequest{
			Edge: &pb.Edge{Tail: "receipt", Head: "head", Weight: 4}, ContribId: id,
		})); err != nil {
			t.Fatal(err)
		}
	}
	if edge, err := fresh.raw.GetEdge(ctx, authedReceiptRequest(
		&pb.GetEdgeRequest{Tail: "receipt", Head: "head"},
	)); err != nil || edge.Msg.GetEdge().GetWeight() != 9 {
		t.Fatalf("certified backup lost contribution tombstones: (%+v, %v)", edge, err)
	}
}

func TestContributionDelete_ReceiptCapacityFailureAtomicRealConnectWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	node := hlc.NodeID{0xe8}
	config := durableReceiptWireConfig(filepath.Join(t.TempDir(), "receipt.wal"), node)
	config.ConfigureGraph = func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
		provider.ConfigureGraphCache(graph, provider.CacheConfig{
			TTL: time.Hour, MaxEdgeCausalEntries: 1,
		}, provider.SearchConfig{})
		return nil
	}
	wire := newClusterRecoveryPublicReceiptServer(t, config, testToken)
	first, absent := bytes.Repeat([]byte{0x39}, 24), bytes.Repeat([]byte{0x3a}, 24)
	if _, err := wire.raw.AddEdge(ctx, authedReceiptRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "bounded", Head: "head", Weight: 5}, ContribId: first,
	})); err != nil {
		t.Fatal(err)
	}
	capability := publicReceiptCapability(t, wire, testToken)
	receipt := publicReceiptWireContext(
		t, capability, 0xe9, 2,
		time.UnixMilli(int64(capability.GetServerNowUnixMs())).Add(-time.Second),
	)
	beforeStore := wire.runtime.ReceiptStats()
	beforeLog, _, _ := wire.runtime.MutationLogStats()
	beforeSeq := wire.server.svc.LocalSeq(node)
	beforeWAL, err := os.Stat(config.Path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wire.raw.DeleteEdgeContributions(ctx, authedReceiptRequest(
		&pb.DeleteEdgeContributionsRequest{
			Contributions: []*pb.EdgeContributionKey{
				{Tail: "bounded", Head: "head", ContribId: first},
				{Tail: "bounded", Head: "head", ContribId: absent},
			},
			ReceiptContext: receipt,
		},
	))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("batch exceeding edge/tombstone capacity = %v, want ResourceExhausted", err)
	}
	afterWAL, err := os.Stat(config.Path)
	if err != nil {
		t.Fatal(err)
	}
	afterLog, _, _ := wire.runtime.MutationLogStats()
	if afterStore := wire.runtime.ReceiptStats(); afterStore.Entries != beforeStore.Entries ||
		afterStore.Bytes != beforeStore.Bytes || afterLog != beforeLog ||
		wire.server.svc.LocalSeq(node) != beforeSeq || afterWAL.Size() != beforeWAL.Size() {
		t.Fatalf("rejected batch changed Store/log/origin/WAL: before=%+v/%d/%d/%d after=%+v/%d/%d/%d",
			beforeStore, beforeLog, beforeSeq, beforeWAL.Size(),
			afterStore, afterLog, wire.server.svc.LocalSeq(node), afterWAL.Size())
	}
	if edge, err := wire.raw.GetEdge(ctx, authedReceiptRequest(
		&pb.GetEdgeRequest{Tail: "bounded", Head: "head"},
	)); err != nil || edge.Msg.GetEdge().GetWeight() != 5 {
		t.Fatalf("rejected batch changed graph: (%+v, %v)", edge, err)
	}
	for i, status := range receiptWireStatuses(t, ctx, wire, testToken, receipt.GetOperationIds()) {
		if status.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED ||
			status.GetReceipt() != nil {
			t.Fatalf("rejected batch receipt status[%d] = %+v", i, status)
		}
	}
}
