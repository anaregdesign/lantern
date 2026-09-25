package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type receiptEdgeDeleteWALFunc func(mutationlog.Entry) error

func (f receiptEdgeDeleteWALFunc) Write(entry mutationlog.Entry) error { return f(entry) }

type receiptEdgeDeleteFixture struct {
	coordinator *edgeDeleteReceiptCoordinator
	service     *LanternService
	cache       *graphcache.GraphCache[string, *pb.Vertex]
	log         *mutationlog.Log
	replication *LanternReplicationService
	epoch       mutationreceipt.Epoch
}

func canonicalizeReceiptReplicationSnapshot(snapshot *graphcache.ReplicationSnapshot[string, *pb.Vertex]) {
	sort.Slice(snapshot.Graph.Vertices, func(i, j int) bool {
		return snapshot.Graph.Vertices[i].Key < snapshot.Graph.Vertices[j].Key
	})
	sort.Slice(snapshot.Graph.Edges, func(i, j int) bool {
		left, right := snapshot.Graph.Edges[i], snapshot.Graph.Edges[j]
		if left.Tail != right.Tail {
			return left.Tail < right.Tail
		}
		return left.Head < right.Head
	})
	for i := range snapshot.Graph.Edges {
		sort.Slice(snapshot.Graph.Edges[i].Contributions, func(left, right int) bool {
			return bytes.Compare(
				snapshot.Graph.Edges[i].Contributions[left].ContribID[:],
				snapshot.Graph.Edges[i].Contributions[right].ContribID[:],
			) < 0
		})
	}
	sort.Slice(snapshot.Barriers.Vertices, func(i, j int) bool {
		return snapshot.Barriers.Vertices[i].Key < snapshot.Barriers.Vertices[j].Key
	})
	sort.Slice(snapshot.Barriers.Edges, func(i, j int) bool {
		left, right := snapshot.Barriers.Edges[i], snapshot.Barriers.Edges[j]
		if left.Tail != right.Tail {
			return left.Tail < right.Tail
		}
		return left.Head < right.Head
	})
	sort.Slice(snapshot.Tombstones.Vertices, func(i, j int) bool {
		return snapshot.Tombstones.Vertices[i].Key < snapshot.Tombstones.Vertices[j].Key
	})
	sort.Slice(snapshot.Tombstones.Edges, func(i, j int) bool {
		left, right := snapshot.Tombstones.Edges[i], snapshot.Tombstones.Edges[j]
		if left.Tail != right.Tail {
			return left.Tail < right.Tail
		}
		return left.Head < right.Head
	})
}

func newReceiptEdgeDeleteFixture(t *testing.T, wal mutationlog.WAL) receiptEdgeDeleteFixture {
	t.Helper()
	return newReceiptEdgeDeleteFixtureWithLimits(t, wal, hlc.NodeID{0x41}, 32, 1<<20)
}

func newReceiptEdgeDeleteFixtureWithLimits(
	t *testing.T,
	wal mutationlog.WAL,
	localNode hlc.NodeID,
	maxEntries int,
	maxBytes uint64,
) receiptEdgeDeleteFixture {
	t.Helper()
	return newReceiptEdgeDeleteFixtureWithStoreConfig(t, wal, localNode, mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour, MaxEntries: maxEntries, MaxBytes: maxBytes,
	})
}

func newReceiptEdgeDeleteFixtureWithStoreConfig(
	t *testing.T,
	wal mutationlog.WAL,
	localNode hlc.NodeID,
	config mutationreceipt.Config,
) receiptEdgeDeleteFixture {
	t.Helper()
	return newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(t, wal, localNode, config, hlc.Options{})
}

func newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
	t *testing.T,
	wal mutationlog.WAL,
	localNode hlc.NodeID,
	config mutationreceipt.Config,
	clockOptions hlc.Options,
) receiptEdgeDeleteFixture {
	t.Helper()
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(localNode, clockOptions)
	svc := NewLanternService(cache).WithReplication(log, clock, nil).WithTombstoneTTL(time.Hour)
	coordinator, err := newEdgeDeleteReceiptCoordinator(svc, store)
	if err != nil {
		t.Fatal(err)
	}
	rep := NewLanternReplicationService(log, cache, clock).WithOriginStates(svc)
	return receiptEdgeDeleteFixture{coordinator, svc, cache, log, rep, config.Epoch}
}

func replicatedReceiptDelete(
	t *testing.T,
	f receiptEdgeDeleteFixture,
	origin hlc.NodeID,
	seq uint64,
	groupSeed byte,
	keys []graphcache.EdgeKey[string],
	issued []time.Time,
	original, senderAccepted []bool,
	stamp, tombstoneExpiration time.Time,
) *pb.Mutation {
	t.Helper()
	if len(keys) == 0 || len(issued) != len(keys) || len(original) != len(keys) || len(senderAccepted) != len(keys) {
		t.Fatal("invalid replicated receipt test fixture alignment")
	}
	group := mutationreceipt.GroupID{groupSeed}
	receipts := make([]mutationreceipt.Receipt, len(keys))
	accepted := make([]graphcache.IndexedEdgeDelete[string], 0, len(keys))
	for i, key := range keys {
		id, err := mutationreceipt.NewID(f.epoch, issued[i], [24]byte{groupSeed, byte(seq), byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: group, Index: uint32(i), Count: uint32(len(keys)),
				Kind: mutationreceipt.DeleteEdge, Digest: edgeDeleteDigest(key.Tail, key.Head),
			},
			Result:         []byte{boolByte(original[i])},
			DeadlineMillis: issued[i].Add(time.Hour).UnixMilli(),
		}
		if senderAccepted[i] {
			accepted = append(accepted, graphcache.IndexedEdgeDelete[string]{Index: i, Key: key})
		}
	}
	envelope := &edgeDeleteReceiptEnvelope{
		Origin: origin, OriginSeq: seq,
		HLC:                 hlc.Timestamp{WallNs: stamp.UnixNano(), Logical: uint32(seq), NodeID: origin},
		Epoch:               f.epoch,
		PolicyFingerprint:   f.coordinator.store.PolicyFingerprint(),
		TombstoneExpiration: tombstoneExpiration,
		OriginalKeys:        append([]graphcache.EdgeKey[string](nil), keys...),
		Accepted:            accepted,
		Receipts:            receipts,
	}
	envelope.Mutation = receiptEdgeDeleteWALMutation(envelope)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func receiptIDFromWire(t *testing.T, m *pb.Mutation, index int) mutationreceipt.ID {
	t.Helper()
	id, err := mutationreceipt.DecodeID(
		m.GetOp().GetReplicatedReceiptEdgeDelete().GetItems()[index].GetReceipt().GetOperationId(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func receiptDeleteCall(t *testing.T, epoch mutationreceipt.Epoch, keys ...graphcache.EdgeKey[string]) receiptEdgeDeleteCall {
	t.Helper()
	call := receiptEdgeDeleteCall{Group: mutationreceipt.GroupID{0x7f}, Items: make([]receiptEdgeDeleteItem, len(keys))}
	issued := time.Now().Add(-time.Second)
	for i, key := range keys {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		call.Items[i] = receiptEdgeDeleteItem{ID: id, Tail: key.Tail, Head: key.Head}
	}
	return call
}

func bindPublicReceiptFixtureForConcurrencyTest(
	t *testing.T,
	f receiptEdgeDeleteFixture,
) *ServingRuntime {
	t.Helper()
	policy := mutationreceipt.Config{
		Epoch: f.epoch, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
		ClockHighWater: time.UnixMilli(f.coordinator.store.Stats().HighWaterMillis),
	}
	retired, err := newEmptyRetiredReceiptCatalogSlot(
		policy,
		policy.ClockHighWater.UnixMilli(),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.coordinator.retired = retired
	if _, err := newVertexPutReceiptCoordinator(f.service, f.coordinator.store); err != nil {
		t.Fatal(err)
	}
	if _, err := newVertexDeleteReceiptCoordinator(f.service, f.coordinator.store); err != nil {
		t.Fatal(err)
	}
	receiptRuntime := &receiptServingRuntime{
		store: f.coordinator.store, retired: retired, policy: policy,
		epoch: f.epoch, generation: [16]byte{0x7e},
		operationAdmission: newReceiptOperationAdmission(),
	}
	receiptRuntime.publicEnabled.Store(true)
	runtime := &ServingRuntime{
		graph: f.cache, log: f.log, clock: f.service.clock,
		origins: f.service.origins, receipt: receiptRuntime,
	}
	f.service.runtime = runtime
	f.service.receiptRetiredCatalog = retired
	return runtime
}

func publicReceiptContext(
	t *testing.T,
	runtime *ServingRuntime,
	seed byte,
	count int,
) *pb.MutationReceiptContext {
	t.Helper()
	ids := make([][]byte, count)
	issued := time.Now().Add(-time.Second)
	for i := range ids {
		id := receiptOperationID(t, runtime.receipt.epoch, issued, seed+byte(i))
		ids[i] = id.Bytes()
	}
	nodeID := runtime.clock.NodeID()
	return &pb.MutationReceiptContext{
		OperationIds:  ids,
		LogicalCallId: append([]byte(nil), seed, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15),
		Endpoint: &pb.ReceiptEndpoint{
			NodeId:     append([]byte(nil), nodeID[:]...),
			Generation: append([]byte(nil), runtime.receipt.generation[:]...),
		},
	}
}

func TestPublicReceiptDeleteEdgesReplaysAlignedOriginalResults(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	ctx := context.Background()
	if _, err := svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "present", Head: "edge", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		{Tail: "protected", Head: "edge", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
	}}); err != nil {
		t.Fatal(err)
	}
	request := &pb.DeleteEdgesRequest{
		Edges: []*pb.EdgeKey{
			{Tail: "present", Head: "edge"},
			{Tail: "absent", Head: "edge"},
		},
		ReceiptContext: publicReceiptContext(t, runtime, 0x61, 2),
	}
	first, err := svc.DeleteEdges(ctx, proto.Clone(request).(*pb.DeleteEdgesRequest))
	if err != nil || first.GetDeleted() != 1 ||
		!reflect.DeepEqual(first.GetExisted(), []bool{true, false}) {
		t.Fatalf("first public receipt delete = %+v, %v", first, err)
	}
	replay, err := svc.DeleteEdges(ctx, proto.Clone(request).(*pb.DeleteEdgesRequest))
	if err != nil || !proto.Equal(first, replay) {
		t.Fatalf("duplicate public receipt delete = %+v, %v, want %+v", replay, err, first)
	}

	statuses, err := svc.GetReceiptStatuses(ctx, &pb.GetReceiptStatusesRequest{
		OperationIds: request.GetReceiptContext().GetOperationIds(),
	})
	if err != nil || len(statuses.GetStatuses()) != 2 ||
		!statuses.GetStatuses()[0].GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() ||
		statuses.GetStatuses()[1].GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() {
		t.Fatalf("committed receipt statuses = %+v, %v", statuses, err)
	}

	mismatch := proto.Clone(request).(*pb.DeleteEdgesRequest)
	mismatch.Edges[1] = &pb.EdgeKey{Tail: "protected", Head: "edge"}
	if _, err := svc.DeleteEdges(ctx, mismatch); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("semantic mismatch = %v, want InvalidArgument", err)
	}
	if _, _, ok := runtime.graph.GetEdgeDetail("protected", "edge"); !ok {
		t.Fatal("semantic mismatch mutated a protected edge")
	}
}

func TestPublicReceiptDeleteEdgesValidatesBeforeMutationAndCapacity(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 1)
	ctx := context.Background()
	if _, err := svc.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{
		{Tail: "first", Head: "edge", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		{Tail: "second", Head: "edge", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
	}}); err != nil {
		t.Fatal(err)
	}

	duplicate := publicReceiptContext(t, runtime, 0x71, 2)
	duplicate.OperationIds[1] = append([]byte(nil), duplicate.OperationIds[0]...)
	if _, err := svc.DeleteEdges(ctx, &pb.DeleteEdgesRequest{
		Edges: []*pb.EdgeKey{
			{Tail: "first", Head: "edge"},
			{Tail: "second", Head: "edge"},
		},
		ReceiptContext: duplicate,
	}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("duplicate operation IDs = %v, want InvalidArgument", err)
	}
	if _, _, ok := runtime.graph.GetEdgeDetail("first", "edge"); !ok {
		t.Fatal("invalid receipt context mutated first edge")
	}

	firstContext := publicReceiptContext(t, runtime, 0x72, 1)
	if _, err := svc.DeleteEdge(ctx, &pb.DeleteEdgeRequest{
		Tail: "first", Head: "edge", ReceiptContext: firstContext,
	}); err != nil {
		t.Fatal(err)
	}
	secondContext := publicReceiptContext(t, runtime, 0x73, 1)
	if _, err := svc.DeleteEdge(ctx, &pb.DeleteEdgeRequest{
		Tail: "second", Head: "edge", ReceiptContext: secondContext,
	}); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("capacity rejection = %v, want ResourceExhausted", err)
	}
	if _, _, ok := runtime.graph.GetEdgeDetail("second", "edge"); !ok {
		t.Fatal("capacity rejection happened after graph mutation")
	}

	stale := proto.Clone(secondContext).(*pb.MutationReceiptContext)
	stale.Endpoint.Generation[0] ^= 0xff
	if _, err := svc.DeleteEdge(ctx, &pb.DeleteEdgeRequest{
		Tail: "second", Head: "edge", ReceiptContext: stale,
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("stale endpoint = %v, want FailedPrecondition", err)
	}
	if _, _, ok := runtime.graph.GetEdgeDetail("second", "edge"); !ok {
		t.Fatal("stale endpoint mutated the edge")
	}
}

func TestPublicReceiptDeleteEdgesRejectsUnrepresentableWALBeforeState(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	ctx := context.Background()
	tail := strings.Repeat("t", 500)
	const head = "edge"
	if _, err := svc.PutEdge(ctx, &pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: tail, Head: head, Weight: 1,
		Expiration: timestamppb.New(time.Now().Add(time.Hour)),
	}}); err != nil {
		t.Fatal(err)
	}

	const count = 10_000
	edges := make([]*pb.EdgeKey, count)
	nodeID := runtime.clock.NodeID()
	receiptContext := &pb.MutationReceiptContext{
		OperationIds:  make([][]byte, count),
		LogicalCallId: []byte{0x79, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		Endpoint: &pb.ReceiptEndpoint{
			NodeId:     append([]byte(nil), nodeID[:]...),
			Generation: append([]byte(nil), runtime.receipt.generation[:]...),
		},
	}
	issued := time.Now().Add(-time.Second)
	for i := range edges {
		edges[i] = &pb.EdgeKey{Tail: tail, Head: head}
		var random [24]byte
		binary.BigEndian.PutUint64(random[:8], uint64(i+1))
		id, err := mutationreceipt.NewID(runtime.receipt.epoch, issued, random)
		if err != nil {
			t.Fatal(err)
		}
		receiptContext.OperationIds[i] = id.Bytes()
	}
	request := &pb.DeleteEdgesRequest{
		Edges: edges, ReceiptContext: receiptContext,
	}
	beforeGraph := runtime.graph.SnapshotReplication()
	canonicalizeReceiptReplicationSnapshot(&beforeGraph)
	beforeReceipts := runtime.receipt.store.Stats()
	beforeSeq := svc.LocalSeq(runtime.clock.NodeID())
	beforeLog := runtime.log.Len()

	var firstError string
	for attempt := 0; attempt < 2; attempt++ {
		_, err := svc.DeleteEdges(ctx, request)
		if connect.CodeOf(err) != connect.CodeResourceExhausted ||
			!errors.Is(err, errReceiptEdgeDeleteWALCapacity) {
			t.Fatalf("oversize attempt %d = %v, want stable ResourceExhausted", attempt+1, err)
		}
		if attempt == 0 {
			firstError = err.Error()
		} else if err.Error() != firstError {
			t.Fatalf("oversize retry error changed: %q -> %q", firstError, err)
		}
	}

	afterGraph := runtime.graph.SnapshotReplication()
	canonicalizeReceiptReplicationSnapshot(&afterGraph)
	if !reflect.DeepEqual(afterGraph, beforeGraph) {
		t.Fatal("oversize receipt request changed graph, index, or causal state")
	}
	if got := runtime.receipt.store.Stats(); got != beforeReceipts {
		t.Fatalf("oversize receipt request changed Store state: before=%+v after=%+v", beforeReceipts, got)
	}
	if got := svc.LocalSeq(runtime.clock.NodeID()); got != beforeSeq {
		t.Fatalf("oversize receipt request changed origin sequence: %d -> %d", beforeSeq, got)
	}
	if got := runtime.log.Len(); got != beforeLog {
		t.Fatalf("oversize receipt request changed WAL/log state: %d -> %d", beforeLog, got)
	}
	if _, _, ok := runtime.graph.GetEdgeDetail(tail, head); !ok {
		t.Fatal("oversize receipt request deleted the seed edge")
	}
}

func TestPublicReceiptOperationsShareAdmissionDuringCommit(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var writes atomic.Int32
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		if writes.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	}))
	runtime := bindPublicReceiptFixtureForConcurrencyTest(t, f)
	f.cache.AddEdgeWithExpiration("tail", "head", 1, time.Now().Add(time.Hour))
	request := &pb.DeleteEdgesRequest{
		Edges:          []*pb.EdgeKey{{Tail: "tail", Head: "head"}},
		ReceiptContext: publicReceiptContext(t, runtime, 0x7a, 1),
	}
	type deleteResult struct {
		response *pb.DeleteEdgesResponse
		err      error
	}
	firstDone := make(chan deleteResult, 1)
	go func() {
		response, err := f.service.DeleteEdges(
			context.Background(),
			proto.Clone(request).(*pb.DeleteEdgesRequest),
		)
		firstDone <- deleteResult{response: response, err: err}
	}()
	waitReceiptTest(t, "public receipt WAL write", entered)

	capabilityDone := make(chan struct {
		response *pb.GetReceiptCapabilityResponse
		err      error
	}, 1)
	go func() {
		response, err := f.service.GetReceiptCapability(
			context.Background(),
			&pb.GetReceiptCapabilityRequest{},
		)
		capabilityDone <- struct {
			response *pb.GetReceiptCapabilityResponse
			err      error
		}{response: response, err: err}
	}()
	statusDone := make(chan struct {
		response *pb.GetReceiptStatusesResponse
		err      error
	}, 1)
	go func() {
		response, err := f.service.GetReceiptStatuses(
			context.Background(),
			&pb.GetReceiptStatusesRequest{
				OperationIds: request.GetReceiptContext().GetOperationIds(),
			},
		)
		statusDone <- struct {
			response *pb.GetReceiptStatusesResponse
			err      error
		}{response: response, err: err}
	}()
	retryDone := make(chan deleteResult, 1)
	go func() {
		response, err := f.service.DeleteEdges(
			context.Background(),
			proto.Clone(request).(*pb.DeleteEdgesRequest),
		)
		retryDone <- deleteResult{response: response, err: err}
	}()

	select {
	case result := <-capabilityDone:
		t.Fatalf("capability misclassified healthy mutation overlap: %+v", result)
	case result := <-statusDone:
		t.Fatalf("status misclassified healthy mutation overlap: %+v", result)
	case result := <-retryDone:
		t.Fatalf("duplicate retry misclassified healthy mutation overlap: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	first := waitReceiptTest(t, "first public receipt response", firstDone)
	retry := waitReceiptTest(t, "duplicate public receipt response", retryDone)
	capability := waitReceiptTest(t, "overlapping capability", capabilityDone)
	status := waitReceiptTest(t, "overlapping status", statusDone)
	if first.err != nil || retry.err != nil || !proto.Equal(first.response, retry.response) ||
		first.response.GetDeleted() != 1 || writes.Load() != 1 {
		t.Fatalf("response-loss retry = first(%+v, %v) retry(%+v, %v) writes=%d",
			first.response, first.err, retry.response, retry.err, writes.Load())
	}
	if capability.err != nil || !capability.response.GetEnabled() {
		t.Fatalf("capability during healthy overlap = %+v, %v", capability.response, capability.err)
	}
	if status.err != nil || len(status.response.GetStatuses()) != 1 ||
		status.response.GetStatuses()[0].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED {
		t.Fatalf("status during healthy overlap = %+v, %v", status.response, status.err)
	}
}

func TestEdgeDeleteReceiptCoordinatorBindsOneStore(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	if _, err := newEdgeDeleteReceiptCoordinator(f.service, f.coordinator.store); err != nil {
		t.Fatalf("same Store could not rebuild coordinator: %v", err)
	}
	other, err := mutationreceipt.New(mutationreceipt.Config{
		Epoch: f.epoch, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newEdgeDeleteReceiptCoordinator(f.service, other); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("same-policy different Store = %v, want FailedPrecondition", err)
	}
	if f.service.receiptStore != f.coordinator.store {
		t.Fatal("rejected Store replaced the active coordinator Store")
	}
}

func TestEdgeDeleteReceiptCoordinatorConcurrentFirstStoreBind(t *testing.T) {
	policy := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x47}, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
	}
	stores := [2]*mutationreceipt.Store{}
	for i := range stores {
		var err error
		stores[i], err = mutationreceipt.New(policy)
		if err != nil {
			t.Fatal(err)
		}
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32})
	t.Cleanup(func() { _ = log.Close() })
	svc := NewLanternService(cache).WithReplication(log, hlc.New(hlc.NodeID{0x48}, hlc.Options{}), nil).WithTombstoneTTL(time.Hour)
	start := make(chan struct{})
	type result struct {
		index int
		err   error
	}
	results := make(chan result, len(stores))
	for i, store := range stores {
		go func() {
			<-start
			_, err := newEdgeDeleteReceiptCoordinator(svc, store)
			results <- result{index: i, err: err}
		}()
	}
	close(start)
	first, second := waitReceiptTest(t, "first Store bind", results), waitReceiptTest(t, "second Store bind", results)
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || connect.CodeOf(second.err) != connect.CodeFailedPrecondition ||
		svc.receiptStore != stores[first.index] {
		t.Fatalf("concurrent Store bind = %+v, %+v, bound=%p", first, second, svc.receiptStore)
	}
	// A wrong first Store blocks later construction with the intended Store;
	// the service cannot silently switch to an incomplete receipt image.
	if _, err := newEdgeDeleteReceiptCoordinator(svc, stores[second.index]); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("losing Store was accepted after first bind: %v", err)
	}
	source, err := NewReceiptWholeStateSource(svc, stores[first.index])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Capture(context.Background(), policy); err != nil {
		t.Fatalf("bound Store could no longer capture: %v", err)
	}
}

type receiptDeleteSubscribeSender struct{ frames chan *pb.SubscribeResponse }

func (s *receiptDeleteSubscribeSender) Send(frame *pb.SubscribeResponse) error {
	s.frames <- frame
	return nil
}

func TestEdgeDeleteReceiptCoordinatorAlignedResultsAndDuplicate(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	expiration := time.Now().Add(time.Hour)
	f.cache.AddEdgeWithExpiration("tail", "present", 1, expiration)
	rejected := graphcache.EdgeKey[string]{Tail: "tail", Head: "rejected"}
	if _, err := f.cache.DeleteEdgesHLCChecked([]graphcache.EdgeKey[string]{rejected}, hlc.Timestamp{WallNs: time.Now().Add(time.Hour).UnixNano()}, expiration); err != nil {
		t.Fatal(err)
	}
	present := graphcache.EdgeKey[string]{Tail: "tail", Head: "present"}
	absent := graphcache.EdgeKey[string]{Tail: "tail", Head: "absent"}
	call := receiptDeleteCall(t, f.epoch, present, absent, present, rejected)
	response, err := f.coordinator.Commit(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, false, false, false}
	if response.GetDeleted() != 1 || !reflect.DeepEqual(response.GetExisted(), want) {
		t.Fatalf("aligned response = %+v, want %v", response, want)
	}
	if _, live := f.cache.GetWeight("tail", "present"); live {
		t.Fatal("committed Delete left present edge live")
	}
	if got := f.service.LocalSeq(f.service.clock.NodeID()); got != 1 || f.log.Len() != 1 {
		t.Fatalf("published origin/log = %d/%d, want 1/1", got, f.log.Len())
	}
	entry := f.log.RetainedEntries()[0]
	envelope, ok := entry.Op.(*edgeDeleteReceiptEnvelope)
	if !ok || len(envelope.Receipts) != len(want) || len(envelope.OriginalKeys) != len(want) {
		t.Fatalf("WAL envelope = %T, %+v", entry.Op, envelope)
	}
	if envelope.OriginSeq != 1 || envelope.Epoch != f.epoch || envelope.PolicyFingerprint != f.coordinator.store.PolicyFingerprint() || envelope.HLC != entry.HLC {
		t.Fatalf("WAL envelope metadata = %+v", envelope)
	}
	accepted := envelope.Accepted
	if len(accepted) != 3 || accepted[0].Index != 0 || accepted[1].Index != 1 || accepted[2].Index != 2 {
		t.Fatalf("accepted indexed transitions = %+v", accepted)
	}
	projected := envelope.Mutation.GetOp().GetDeleteEdges().GetEdges()
	if len(projected) != 3 || projected[0].GetHead() != "present" || projected[1].GetHead() != "absent" || projected[2].GetHead() != "present" {
		t.Fatalf("graph-only private projection = %+v", projected)
	}
	for i, item := range call.Items {
		status, receipt, err := f.coordinator.Lookup(item.ID, time.Now())
		if err != nil || status != mutationreceipt.Confirmed || receipt.Index != uint32(i) || receipt.Result[0] != boolByte(want[i]) {
			t.Fatalf("receipt[%d] = %v, %+v, %v", i, status, receipt, err)
		}
	}
	duplicate, err := f.coordinator.Commit(context.Background(), call)
	if err != nil || !reflect.DeepEqual(duplicate.GetExisted(), want) || f.log.Len() != 1 {
		t.Fatalf("duplicate = %+v, %v; log=%d", duplicate, err, f.log.Len())
	}
	changed := call
	changed.Items = append([]receiptEdgeDeleteItem(nil), call.Items...)
	changed.Items[0].Head = "different"
	if _, err := f.coordinator.Commit(context.Background(), changed); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("changed intent = %v, want InvalidArgument", err)
	}
}

func TestEdgeDeleteReceiptCoordinatorAlignsStoreClockBeforeReplication(t *testing.T) {
	base := time.Now()
	highWater := base.Add(hlc.DefaultMaxSkew).Truncate(time.Millisecond)
	config := mutationreceipt.Config{
		Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
		MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: highWater,
	}
	local := newReceiptEdgeDeleteFixtureWithStoreConfig(t, nil, hlc.NodeID{0x49}, config)
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "clock-floor"}
	local.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, base.Add(time.Hour))
	id, err := mutationreceipt.NewID(local.epoch, highWater.Add(5*time.Minute), [24]byte{0x49})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.coordinator.Commit(context.Background(), receiptEdgeDeleteCall{
		Group: mutationreceipt.GroupID{0x49},
		Items: []receiptEdgeDeleteItem{{ID: id, Tail: key.Tail, Head: key.Head}},
	}); err != nil {
		t.Fatalf("local commit after wall rollback = %v", err)
	}
	envelope, ok := local.log.RetainedEntries()[0].Op.(*edgeDeleteReceiptEnvelope)
	if !ok || envelope.HLC.WallNs < highWater.UnixNano() {
		t.Fatalf("local envelope did not honor Store clock floor: %T %+v", local.log.RetainedEntries()[0].Op, envelope)
	}
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}

	remoteConfig := config
	remoteConfig.ClockHighWater = time.Time{}
	remote := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
		t, nil, hlc.NodeID{0x4a}, remoteConfig,
		hlc.Options{Now: func() int64 { return base.UnixNano() }},
	)
	remote.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, base.Add(time.Hour))
	if err := remote.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatalf("follower rejected rollback-safe local envelope = %v", err)
	}
	if status, _, err := remote.coordinator.Lookup(id, base); err != nil || status != mutationreceipt.Confirmed {
		t.Fatalf("follower receipt after rollback-safe publication = %v, %v", status, err)
	}

	source, err := NewReceiptWholeStateSource(remote.service, remote.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.Capture(context.Background(), remoteConfig)
	if err != nil {
		t.Fatalf("receipt snapshot after remote publication = %v", err)
	}
	if len(capture.Graph) == 0 || capture.Graph[0].GetHeader() == nil {
		t.Fatalf("receipt snapshot has no header: %+v", capture.Graph)
	}
	cutoff := hlcFromProto(capture.Graph[0].GetHeader().GetCutoffHlc())
	if !envelope.HLC.Less(cutoff) {
		t.Fatalf("snapshot cutoff %v did not advance beyond remote origin %v", cutoff, envelope.HLC)
	}
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}

func TestEdgeDeleteReceiptCoordinatorReceiptOnlyPositionProjectsZeroKeys(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "already-fenced"}
	if _, err := f.cache.DeleteEdgesHLCChecked([]graphcache.EdgeKey[string]{key}, hlc.Timestamp{WallNs: time.Now().Add(time.Hour).UnixNano()}, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	call := receiptDeleteCall(t, f.epoch, key)
	response, err := f.coordinator.Commit(context.Background(), call)
	if err != nil || response.GetDeleted() != 0 || len(response.GetExisted()) != 1 || response.GetExisted()[0] {
		t.Fatalf("receipt-only response = %+v, %v", response, err)
	}
	envelope := f.log.RetainedEntries()[0].Op.(*edgeDeleteReceiptEnvelope)
	if len(envelope.Accepted) != 0 || len(envelope.Mutation.GetOp().GetDeleteEdges().GetEdges()) != 0 || len(envelope.Receipts) != 1 {
		t.Fatalf("receipt-only WAL envelope = %+v", envelope)
	}
	oldSender := &receiptDeleteSubscribeSender{frames: make(chan *pb.SubscribeResponse, 1)}
	if err := f.replication.Subscribe(context.Background(), &pb.SubscribeRequest{FromLocalSeq: 1}, oldSender); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("old full Subscribe consumer = %v, want InvalidArgument", err)
	}
	select {
	case frame := <-oldSender.frames:
		t.Fatalf("old full Subscribe received receipt frame: %+v", frame)
	default:
	}
	for _, projection := range []pb.SubscribeProjection{
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_FULL_MUTATION,
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
	} {
		ctx, cancel := context.WithCancel(context.Background())
		sender := &receiptDeleteSubscribeSender{frames: make(chan *pb.SubscribeResponse, 1)}
		done := make(chan error, 1)
		go func() {
			done <- f.replication.Subscribe(ctx, &pb.SubscribeRequest{Projection: projection, FromLocalSeq: 0, AcceptReceiptEnvelopes: true}, sender)
		}()
		frame := waitReceiptTest(t, "receipt-only Subscribe", sender.frames)
		if projection == pb.SubscribeProjection_SUBSCRIBE_PROJECTION_FULL_MUTATION {
			if frame.GetMutation().GetSeq() != 1 || len(frame.GetMutation().GetOp().GetReplicatedReceiptEdgeDelete().GetItems()) != 1 {
				t.Fatalf("full projection = %+v", frame)
			}
		} else {
			chunk := frame.GetIdentityChunk()
			if chunk.GetSeq() != 1 || !chunk.GetIsLast() || len(chunk.GetEdgeKeys()) != 0 || chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY {
				t.Fatalf("identity projection = %+v", frame)
			}
		}
		cancel()
		waitReceiptTest(t, "Subscribe cancellation", done)
	}
	peerLog := mutationlog.New(mutationlog.Options{Capacity: 8})
	t.Cleanup(func() { _ = peerLog.Close() })
	peerClock := hlc.New(hlc.NodeID{0x55}, hlc.Options{})
	peer := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithReplication(peerLog, peerClock, nil).WithTombstoneTTL(time.Hour)
	wire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeUnimplemented || peer.LocalSeq(f.service.clock.NodeID()) != 0 {
		t.Fatalf("unwired downstream receipt replay = %v, seq=%d", err, peer.LocalSeq(f.service.clock.NodeID()))
	}
	malformed := proto.Clone(wire).(*pb.Mutation)
	malformed.Seq = 2
	malformed.Op.Op = &pb.MutationOp_ReplicatedReceiptEdgeDelete{}
	if _, err := f.log.Append(malformed, hlcFromProto(malformed.Hlc)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		optIn bool
		want  connect.Code
	}{
		{name: "legacy", want: connect.CodeInvalidArgument},
		{name: "opt-in", optIn: true, want: connect.CodeInternal},
	} {
		t.Run(tc.name+" rejects typed nil arm", func(t *testing.T) {
			sender := &receiptDeleteSubscribeSender{frames: make(chan *pb.SubscribeResponse, 1)}
			err := f.replication.Subscribe(context.Background(), &pb.SubscribeRequest{
				FromLocalSeq: 2, AcceptReceiptEnvelopes: tc.optIn,
			}, sender)
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("typed nil receipt arm = %v, want %v", err, tc.want)
			}
			select {
			case frame := <-sender.frames:
				t.Fatalf("typed nil receipt arm produced frame: %+v", frame)
			default:
			}
		})
	}
}

func TestEdgeDeleteReceiptCoordinatorDefiniteWALAbortRollsBack(t *testing.T) {
	var writes atomic.Int32
	wal := receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		if writes.Add(1) == 1 {
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected definite abort")}
		}
		return nil
	})
	f := newReceiptEdgeDeleteFixture(t, wal)
	f.cache.AddEdgeWithExpiration("tail", "head", 1, time.Now().Add(time.Hour))
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("definite WAL abort = %v, want Unavailable", err)
	}
	if _, live := f.cache.GetWeight("tail", "head"); !live || f.service.LocalSeq(f.service.clock.NodeID()) != 0 || f.log.Len() != 0 {
		t.Fatalf("definite rollback graph/origin/log = live %v, seq %d, log %d", live, f.service.LocalSeq(f.service.clock.NodeID()), f.log.Len())
	}
	status, _, err := f.coordinator.Lookup(call.Items[0].ID, time.Now())
	if err != nil || status != mutationreceipt.NotYetObserved {
		t.Fatalf("receipt after definite abort = %v, %v", status, err)
	}
	snapshot := &replicationSnapshotRecorder{}
	if err := f.replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, snapshot); err != nil {
		t.Fatalf("Snapshot after definite abort: %v", err)
	}
	response, err := f.coordinator.Commit(context.Background(), call)
	if err != nil || response.GetDeleted() != 1 || f.service.LocalSeq(f.service.clock.NodeID()) != 1 {
		t.Fatalf("retry after definite abort = %+v, %v", response, err)
	}
}

func TestEdgeDeleteReceiptCoordinatorIndeterminateWALFailStops(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error { return errors.New("lost WAL acknowledgement") }))
	f.cache.AddEdgeWithExpiration("tail", "head", 1, time.Now().Add(time.Hour))
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable || !errors.Is(err, mutationlog.ErrWALIndeterminate) {
		t.Fatalf("indeterminate WAL = %v", err)
	}
	if _, live := f.cache.GetWeight("tail", "head"); !live || f.service.LocalSeq(f.service.clock.NodeID()) != 0 || f.log.Len() != 0 {
		t.Fatalf("local cleanup graph/origin/log = live %v, seq %d, log %d", live, f.service.LocalSeq(f.service.clock.NodeID()), f.log.Len())
	}
	if _, _, err := f.coordinator.Lookup(call.Items[0].ID, time.Now()); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("receipt status after indeterminate WAL = %v", err)
	}
	if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("new receipt commit after indeterminate WAL = %v", err)
	}
	if _, err := f.service.DeleteEdges(context.Background(), &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "tail", Head: "head"}}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("legacy write after indeterminate WAL = %v", err)
	}
	remote := hlc.NodeID{0x55}
	deadline := time.Now().Add(time.Hour)
	if err := f.service.ApplyMutation(context.Background(), &pb.Mutation{
		Origin: remote[:], Seq: 1,
		Hlc:                 hlcToProto(hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: remote}),
		Op:                  &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "remote", Head: "blocked"}}}}},
		TombstoneExpiration: timestamppb.New(deadline),
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("remote write after indeterminate WAL = %v", err)
	}
	if _, err := (&lanternServiceConnect{svc: f.service}).GetEdge(context.Background(), connect.NewRequest(&pb.GetEdgeRequest{Tail: "tail", Head: "head"})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("graph read after indeterminate WAL = %v", err)
	}
	if _, err := (&lanternServiceConnect{svc: f.service}).GetServerStatus(context.Background(), connect.NewRequest(&pb.GetServerStatusRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("GetServerStatus after indeterminate WAL = %v", err)
	}
	if _, err := (&lanternServiceConnect{svc: f.service}).GetReplicationStatus(context.Background(), connect.NewRequest(&pb.GetReplicationStatusRequest{})); err != nil {
		t.Fatalf("diagnostic GetReplicationStatus after indeterminate WAL = %v", err)
	}
	if _, err := f.replication.PeerStatus(context.Background(), &pb.PeerStatusRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("PeerStatus after indeterminate WAL = %v", err)
	}
	if err := f.replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Snapshot after indeterminate WAL = %v", err)
	}
	if err := f.replication.Subscribe(context.Background(), &pb.SubscribeRequest{}, &receiptDeleteSubscribeSender{frames: make(chan *pb.SubscribeResponse, 1)}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Subscribe after indeterminate WAL = %v", err)
	}
}

func TestEdgeDeleteReceiptCoordinatorHeldWALBlocksStagedReaders(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		close(entered)
		<-release
		return nil
	}))
	f.cache.AddEdgeWithExpiration("tail", "head", 1, time.Now().Add(time.Hour))
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	commitDone := make(chan error, 1)
	go func() {
		_, err := f.coordinator.Commit(context.Background(), call)
		commitDone <- err
	}()
	waitReceiptTest(t, "WAL stage", entered)
	observerStarted := make(chan struct{}, 8)
	statusDone := make(chan error, 1)
	go func() {
		observerStarted <- struct{}{}
		status, _, err := f.coordinator.Lookup(call.Items[0].ID, time.Now())
		if err == nil && status != mutationreceipt.Confirmed {
			err = errors.New("staged receipt was not confirmed after commit")
		}
		statusDone <- err
	}()
	storeDone := make(chan mutationreceipt.Status, 1)
	go func() {
		observerStarted <- struct{}{}
		status, _, _ := f.coordinator.store.Lookup(call.Items[0].ID, time.Now())
		storeDone <- status
	}()
	graphDone := make(chan bool, 1)
	go func() {
		observerStarted <- struct{}{}
		_, live := f.cache.GetWeight("tail", "head")
		graphDone <- live
	}()
	originDone := make(chan uint64, 1)
	go func() {
		observerStarted <- struct{}{}
		originDone <- f.service.LocalSeq(f.service.clock.NodeID())
	}()
	peerDone := make(chan error, 1)
	go func() {
		observerStarted <- struct{}{}
		_, err := f.replication.PeerStatus(context.Background(), &pb.PeerStatusRequest{})
		peerDone <- err
	}()
	serverStatusDone := make(chan error, 1)
	go func() {
		observerStarted <- struct{}{}
		_, err := (&lanternServiceConnect{svc: f.service}).GetServerStatus(context.Background(), connect.NewRequest(&pb.GetServerStatusRequest{}))
		serverStatusDone <- err
	}()
	snapshotDone := make(chan error, 1)
	go func() {
		observerStarted <- struct{}{}
		snapshotDone <- f.replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{})
	}()
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()
	frames := &receiptDeleteSubscribeSender{frames: make(chan *pb.SubscribeResponse, 1)}
	streamDone := make(chan error, 1)
	go func() {
		observerStarted <- struct{}{}
		streamDone <- f.replication.Subscribe(streamCtx, &pb.SubscribeRequest{FromLocalSeq: 1, AcceptReceiptEnvelopes: true}, frames)
	}()
	for range 8 {
		waitReceiptTest(t, "observer start", observerStarted)
	}
	select {
	case err := <-statusDone:
		t.Fatalf("receipt status crossed staged WAL: %v", err)
	case status := <-storeDone:
		t.Fatalf("Store lookup crossed staged WAL: %v", status)
	case live := <-graphDone:
		t.Fatalf("graph read crossed staged WAL: live=%v", live)
	case seq := <-originDone:
		t.Fatalf("origin frontier crossed staged WAL: seq=%d", seq)
	case err := <-peerDone:
		t.Fatalf("PeerStatus crossed staged WAL: %v", err)
	case err := <-serverStatusDone:
		t.Fatalf("GetServerStatus crossed staged WAL: %v", err)
	case err := <-snapshotDone:
		t.Fatalf("Snapshot crossed staged WAL: %v", err)
	case frame := <-frames.frames:
		t.Fatalf("Subscribe crossed staged WAL: %+v", frame)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := waitReceiptTest(t, "commit", commitDone); err != nil {
		t.Fatal(err)
	}
	if err := waitReceiptTest(t, "receipt status", statusDone); err != nil {
		t.Fatal(err)
	}
	if status := waitReceiptTest(t, "Store lookup", storeDone); status != mutationreceipt.Confirmed {
		t.Fatalf("Store lookup after commit = %v", status)
	}
	if live := waitReceiptTest(t, "graph observer", graphDone); live {
		t.Fatal("graph observer saw edge after committed Delete")
	}
	if seq := waitReceiptTest(t, "origin frontier", originDone); seq != 1 {
		t.Fatalf("origin frontier after commit = %d", seq)
	}
	if err := waitReceiptTest(t, "PeerStatus", peerDone); err != nil {
		t.Fatal(err)
	}
	if err := waitReceiptTest(t, "GetServerStatus", serverStatusDone); err != nil {
		t.Fatal(err)
	}
	if err := waitReceiptTest(t, "Snapshot", snapshotDone); err != nil {
		t.Fatal(err)
	}
	frame := waitReceiptTest(t, "Subscribe", frames.frames)
	if frame.GetMutation().GetSeq() != 1 {
		t.Fatalf("Subscribe after commit = %+v", frame)
	}
	streamCancel()
	waitReceiptTest(t, "Subscribe cancel", streamDone)
}

func TestEdgeDeleteReceiptFollowerAppliesAndDeduplicates(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x31}, 32, 1<<20)
	now := time.Now()
	keys := []graphcache.EdgeKey[string]{
		{Tail: "tail", Head: "present"},
		{Tail: "tail", Head: "absent"},
	}
	f.cache.AddEdgeWithExpiration("tail", "present", 1, now.Add(time.Hour))
	origin := hlc.NodeID{0x51}
	wire := replicatedReceiptDelete(t, f, origin, 1, 0x61, keys,
		[]time.Time{now.Add(-time.Minute), now.Add(-time.Minute)},
		[]bool{true, false}, []bool{true, false}, now, now.Add(30*time.Minute))
	var applies, origins atomic.Int32
	var appliedOp string
	f.service.WithReplicationApplyHook(func(op string) {
		appliedOp = op
		applies.Add(1)
	}).WithAppliedHook(func(string) { origins.Add(1) })

	if err := f.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatal(err)
	}
	if _, live := f.cache.GetWeight("tail", "present"); live {
		t.Fatal("follower left the accepted edge live")
	}
	if got := f.service.LocalSeq(origin); got != 1 || f.log.Len() != 1 {
		t.Fatalf("follower origin/log = %d/%d, want 1/1", got, f.log.Len())
	}
	for i, want := range []bool{true, false} {
		status, receipt, err := f.coordinator.Lookup(receiptIDFromWire(t, wire, i), time.Now())
		if err != nil || status != mutationreceipt.Confirmed || receipt.Result[0] != boolByte(want) {
			t.Fatalf("follower receipt[%d] = %v, %+v, %v", i, status, receipt, err)
		}
	}
	relay, ok := f.log.RetainedEntries()[0].Op.(*edgeDeleteReceiptEnvelope)
	if !ok || len(relay.Accepted) != 2 || relay.Accepted[0].Index != 0 || relay.Accepted[1].Index != 1 ||
		relay.Receipts[0].Result[0] != 1 || relay.Receipts[1].Result[0] != 0 {
		t.Fatalf("receiver-local relay envelope = %T %+v", f.log.RetainedEntries()[0].Op, relay)
	}
	if applies.Load() != 1 || origins.Load() != 1 || appliedOp != "replicated_receipt_edge_delete" {
		t.Fatalf("success metrics = applies %d origins %d op %q", applies.Load(), origins.Load(), appliedOp)
	}

	duplicate := proto.Clone(wire).(*pb.Mutation)
	for _, item := range duplicate.GetOp().GetReplicatedReceiptEdgeDelete().GetItems() {
		item.CausallyAccepted = !item.GetCausallyAccepted()
	}
	if err := f.service.ApplyMutation(context.Background(), duplicate); err != nil {
		t.Fatalf("committed duplicate with relay-local accepted-bit drift = %v", err)
	}
	if f.log.Len() != 1 || applies.Load() != 1 || origins.Load() != 1 {
		t.Fatalf("committed duplicate republished: log=%d metrics=%d/%d", f.log.Len(), applies.Load(), origins.Load())
	}
}

func TestEdgeDeleteReceiptFollowerUsesReceiverLocalAcceptedDecision(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x32}, 32, 1<<20)
	now := time.Now()
	origin := hlc.NodeID{0x52}
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "newer"}
	newer := hlc.Timestamp{WallNs: now.Add(time.Minute).UnixNano(), NodeID: hlc.NodeID{0x7f}}
	if !f.cache.PutEdgeWithExpirationHLC(key.Tail, key.Head, 3, now.Add(time.Hour), newer) {
		t.Fatal("failed to seed receiver-newer edge")
	}
	wire := replicatedReceiptDelete(t, f, origin, 1, 0x62, []graphcache.EdgeKey[string]{key},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now, now.Add(30*time.Minute))
	if err := f.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatal(err)
	}
	if weight, live := f.cache.GetWeight(key.Tail, key.Head); !live || weight != 3 {
		t.Fatalf("older receipt Delete changed receiver-newer edge: %v, %v", weight, live)
	}
	relay := f.log.RetainedEntries()[0].Op.(*edgeDeleteReceiptEnvelope)
	if len(relay.Accepted) != 0 || len(relay.Mutation.GetOp().GetDeleteEdges().GetEdges()) != 0 ||
		len(relay.Receipts) != 1 || relay.Receipts[0].Result[0] != 1 {
		t.Fatalf("relay used origin accepted bits instead of receiver decision: %+v", relay)
	}
	replicated, err := relay.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	item := replicated.GetOp().GetReplicatedReceiptEdgeDelete().GetItems()[0]
	if item.GetCausallyAccepted() || !item.GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() {
		t.Fatalf("relay lost local rejection or original result: %+v", item)
	}
}

func TestEdgeDeleteReceiptFollowerRejectsIntentConflictBeforeGraph(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x33}, 32, 1<<20)
	now := time.Now()
	origin := hlc.NodeID{0x53}
	firstKey := graphcache.EdgeKey[string]{Tail: "tail", Head: "first"}
	first := replicatedReceiptDelete(t, f, origin, 1, 0x63, []graphcache.EdgeKey[string]{firstKey},
		[]time.Time{now.Add(-time.Minute)}, []bool{false}, []bool{true}, now, now.Add(30*time.Minute))
	if err := f.service.ApplyMutation(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	conflictKey := graphcache.EdgeKey[string]{Tail: "tail", Head: "must-stay-live"}
	f.cache.AddEdgeWithExpiration(conflictKey.Tail, conflictKey.Head, 1, now.Add(time.Hour))
	conflict := replicatedReceiptDelete(t, f, origin, 2, 0x64, []graphcache.EdgeKey[string]{conflictKey},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now.Add(time.Millisecond), now.Add(30*time.Minute))
	firstReceipt := first.GetOp().GetReplicatedReceiptEdgeDelete().GetItems()[0].GetReceipt()
	conflictReceipt := conflict.GetOp().GetReplicatedReceiptEdgeDelete().GetItems()[0].GetReceipt()
	conflictReceipt.OperationId = append([]byte(nil), firstReceipt.GetOperationId()...)
	conflictReceipt.DeadlineUnixMs = firstReceipt.GetDeadlineUnixMs()
	if err := f.service.ApplyMutation(context.Background(), conflict); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("receipt intent conflict = %v, want InvalidArgument", err)
	}
	if _, live := f.cache.GetWeight(conflictKey.Tail, conflictKey.Head); !live {
		t.Fatal("receipt intent conflict changed graph")
	}
	if got := f.service.LocalSeq(origin); got != 1 || f.log.Len() != 1 {
		t.Fatalf("receipt intent conflict advanced origin/log = %d/%d", got, f.log.Len())
	}
}

func TestEdgeDeleteReceiptFollowerDrainsContiguousOutOfOrder(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x34}, 32, 1<<20)
	now := time.Now()
	origin := hlc.NodeID{0x54}
	firstKey := graphcache.EdgeKey[string]{Tail: "tail", Head: "first"}
	secondKey := graphcache.EdgeKey[string]{Tail: "tail", Head: "second"}
	f.cache.AddEdgeWithExpiration(firstKey.Tail, firstKey.Head, 1, now.Add(time.Hour))
	f.cache.AddEdgeWithExpiration(secondKey.Tail, secondKey.Head, 1, now.Add(time.Hour))
	first := replicatedReceiptDelete(t, f, origin, 1, 0x65, []graphcache.EdgeKey[string]{firstKey},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now, now.Add(30*time.Minute))
	second := replicatedReceiptDelete(t, f, origin, 2, 0x66, []graphcache.EdgeKey[string]{secondKey},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now.Add(time.Millisecond), now.Add(30*time.Minute))
	var applies atomic.Int32
	f.service.WithReplicationApplyHook(func(string) { applies.Add(1) })

	if err := f.service.ApplyMutation(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	secondViaDifferentRelay := proto.Clone(second).(*pb.Mutation)
	secondViaDifferentRelay.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].CausallyAccepted = false
	if err := f.service.ApplyMutation(context.Background(), secondViaDifferentRelay); err != nil {
		t.Fatalf("pending duplicate with sender-local accepted-bit drift = %v", err)
	}
	if _, live := f.cache.GetWeight(secondKey.Tail, secondKey.Head); !live ||
		f.service.LocalSeq(origin) != 0 || f.log.Len() != 0 || applies.Load() != 0 {
		t.Fatalf("future sequence published early: live=%v seq=%d log=%d metrics=%d",
			live, f.service.LocalSeq(origin), f.log.Len(), applies.Load())
	}
	status, _, err := f.coordinator.Lookup(receiptIDFromWire(t, second, 0), time.Now())
	if err != nil || status != mutationreceipt.NotYetObserved {
		t.Fatalf("future receipt became visible: %v, %v", status, err)
	}

	if err := f.service.ApplyMutation(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, live := f.cache.GetWeight(firstKey.Tail, firstKey.Head); live {
		t.Fatal("first contiguous receipt did not apply")
	}
	if _, live := f.cache.GetWeight(secondKey.Tail, secondKey.Head); live {
		t.Fatal("queued second receipt did not drain")
	}
	if f.service.LocalSeq(origin) != 2 || f.log.Len() != 2 || applies.Load() != 2 ||
		f.service.pendingCount != 0 || f.service.pendingBytes != 0 {
		t.Fatalf("contiguous drain = seq %d log %d metrics %d pending %d/%d",
			f.service.LocalSeq(origin), f.log.Len(), applies.Load(), f.service.pendingCount, f.service.pendingBytes)
	}
}

func TestEdgeDeleteReceiptFollowerCapacityStallsAndRecovers(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x35}, 1, 1<<20)
	now := time.Now()
	seedDeadline := now.Add(2 * time.Minute)
	seedIssued := seedDeadline.Add(-time.Hour)
	seedWire := replicatedReceiptDelete(t, f, hlc.NodeID{0x75}, 1, 0x67,
		[]graphcache.EdgeKey[string]{{Tail: "seed", Head: "receipt"}},
		[]time.Time{seedIssued}, []bool{false}, []bool{false},
		seedIssued, seedIssued.Add(30*time.Minute))
	seedEnvelope, err := decodeReceiptEdgeDeleteMutation(seedWire)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.coordinator.store.Begin(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.PrepareCommitted(seedEnvelope.Receipts, seedEnvelope.HLC.WallNs/int64(time.Millisecond)); err != nil {
		tx.Abort()
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		tx.Abort()
		t.Fatal(err)
	}
	tx.Commit()

	origin := hlc.NodeID{0x55}
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "capacity"}
	f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
	incomingIssued := now.Add(-10 * time.Minute)
	wire := replicatedReceiptDelete(t, f, origin, 1, 0x68, []graphcache.EdgeKey[string]{key},
		[]time.Time{incomingIssued}, []bool{true}, []bool{true},
		incomingIssued, incomingIssued.Add(30*time.Minute))
	var applies atomic.Int32
	f.service.WithReplicationApplyHook(func(string) { applies.Add(1) })
	if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("live receipt capacity stall = %v, want ResourceExhausted", err)
	}
	if _, live := f.cache.GetWeight(key.Tail, key.Head); !live || f.service.LocalSeq(origin) != 0 ||
		f.log.Len() != 0 || applies.Load() != 0 || f.service.pendingMutations[origin][1] == nil {
		t.Fatalf("capacity stall published state: live=%v seq=%d log=%d metrics=%d pending=%v",
			live, f.service.LocalSeq(origin), f.log.Len(), applies.Load(), f.service.pendingMutations[origin][1])
	}
	if status, _, err := f.coordinator.Lookup(receiptIDFromWire(t, wire, 0), now); err != nil || status != mutationreceipt.NotYetObserved {
		t.Fatalf("stalled receipt status = %v, %v", status, err)
	}
	if got := f.coordinator.store.Stats(); got.Entries != 1 || got.ReplicationCapacityStalls != 1 {
		t.Fatalf("capacity stall Store stats = %+v", got)
	}

	if status, _, err := f.coordinator.store.Lookup(receiptIDFromWire(t, seedWire, 0), seedDeadline); err != nil ||
		status != mutationreceipt.NoLongerProvable {
		t.Fatalf("seed capacity expiry = %v, %v", status, err)
	}
	if err := f.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatalf("capacity recovery retry = %v", err)
	}
	if _, live := f.cache.GetWeight(key.Tail, key.Head); live || f.service.LocalSeq(origin) != 1 ||
		f.log.Len() != 1 || applies.Load() != 1 {
		t.Fatalf("capacity recovery did not publish once: live=%v seq=%d log=%d metrics=%d",
			live, f.service.LocalSeq(origin), f.log.Len(), applies.Load())
	}
	if status, _, err := f.coordinator.Lookup(receiptIDFromWire(t, wire, 0), seedDeadline); err != nil ||
		status != mutationreceipt.Confirmed {
		t.Fatalf("recovered receipt status = %v, %v", status, err)
	}
}

func TestEdgeDeleteReceiptFollowerMixedAndFullyExpiredEnvelopes(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x36}, 32, 1<<20)
	now := time.Now()
	origin := hlc.NodeID{0x56}
	keys := []graphcache.EdgeKey[string]{
		{Tail: "tail", Head: "expired-receipt"},
		{Tail: "tail", Head: "live-receipt"},
	}
	for _, key := range keys {
		f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
	}
	originStamp := now.Add(-56 * time.Minute)
	mixed := replicatedReceiptDelete(t, f, origin, 1, 0x69, keys,
		[]time.Time{now.Add(-61 * time.Minute), now.Add(-51 * time.Minute)},
		[]bool{true, true}, []bool{true, true}, originStamp, originStamp.Add(30*time.Minute))
	if err := f.service.ApplyMutation(context.Background(), mixed); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, live := f.cache.GetWeight(key.Tail, key.Head); live {
			t.Fatalf("mixed-expiry envelope left edge live: %+v", key)
		}
	}
	if status, _, err := f.coordinator.Lookup(receiptIDFromWire(t, mixed, 0), now); err != nil ||
		status != mutationreceipt.NoLongerProvable {
		t.Fatalf("expired sibling status = %v, %v", status, err)
	}
	if status, receipt, err := f.coordinator.Lookup(receiptIDFromWire(t, mixed, 1), now); err != nil ||
		status != mutationreceipt.Confirmed || receipt.Index != 1 || receipt.Count != 2 {
		t.Fatalf("live sibling status = %v, %+v, %v", status, receipt, err)
	}
	if got := f.coordinator.store.Stats(); got.Entries != 1 {
		t.Fatalf("mixed-expiry Store entries = %+v", got)
	}

	allExpiredKey := graphcache.EdgeKey[string]{Tail: "tail", Head: "all-expired"}
	f.cache.AddEdgeWithExpiration(allExpiredKey.Tail, allExpiredKey.Head, 1, now.Add(time.Hour))
	allExpiredStamp := now.Add(-55 * time.Minute)
	allExpired := replicatedReceiptDelete(t, f, origin, 2, 0x6a,
		[]graphcache.EdgeKey[string]{allExpiredKey}, []time.Time{now.Add(-time.Hour)},
		[]bool{true}, []bool{true}, allExpiredStamp, allExpiredStamp.Add(30*time.Minute))
	if err := f.service.ApplyMutation(context.Background(), allExpired); err != nil {
		t.Fatal(err)
	}
	if _, live := f.cache.GetWeight(allExpiredKey.Tail, allExpiredKey.Head); live {
		t.Fatal("fully expired receipt envelope skipped its graph effect")
	}
	if status, _, err := f.coordinator.Lookup(receiptIDFromWire(t, allExpired, 0), now); err != nil ||
		status != mutationreceipt.NoLongerProvable {
		t.Fatalf("fully expired receipt status = %v, %v", status, err)
	}
	if f.coordinator.store.Stats().Entries != 1 || f.service.LocalSeq(origin) != 2 || f.log.Len() != 2 {
		t.Fatalf("fully expired publication = store %d seq %d log %d",
			f.coordinator.store.Stats().Entries, f.service.LocalSeq(origin), f.log.Len())
	}
	relay := f.log.RetainedEntries()[1].Op.(*edgeDeleteReceiptEnvelope)
	if !relay.TombstoneExpiration.Equal(allExpiredStamp.Add(30*time.Minute)) || len(relay.Receipts) != 1 {
		t.Fatalf("expired relay renewed or dropped metadata: %+v", relay)
	}
}

func TestEdgeDeleteReceiptFollowerBindsPolicyAndDeadlineBeforeGraph(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*pb.Mutation)
		code   connect.Code
	}{
		{"policy", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeDelete().PolicyFingerprint[0] ^= 1
		}, connect.CodeFailedPrecondition},
		{"deadline", func(m *pb.Mutation) {
			m.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Receipt.DeadlineUnixMs++
		}, connect.CodeInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x37}, 32, 1<<20)
			now := time.Now()
			origin := hlc.NodeID{0x57}
			key := graphcache.EdgeKey[string]{Tail: "tail", Head: tc.name}
			f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
			wire := replicatedReceiptDelete(t, f, origin, 1, 0x6b, []graphcache.EdgeKey[string]{key},
				[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now, now.Add(30*time.Minute))
			tc.mutate(wire)
			if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != tc.code {
				t.Fatalf("%s mismatch = %v, want %v", tc.name, err, tc.code)
			}
			if _, live := f.cache.GetWeight(key.Tail, key.Head); !live ||
				f.service.LocalSeq(origin) != 0 || f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 {
				t.Fatalf("%s mismatch published state", tc.name)
			}
		})
	}
}

func TestEdgeDeleteReceiptFollowerUsesBoundedOriginFreshness(t *testing.T) {
	t.Run("origin boundary survives follower clock lag", func(t *testing.T) {
		f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x3b}, 32, 1<<20)
		now := time.Now()
		stamp := now.Add(100 * time.Millisecond)
		origin := hlc.NodeID{0x5b}
		key := graphcache.EdgeKey[string]{Tail: "tail", Head: "origin-boundary"}
		f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
		wire := replicatedReceiptDelete(t, f, origin, 1, 0x6f, []graphcache.EdgeKey[string]{key},
			[]time.Time{stamp.Add(5 * time.Minute)}, []bool{true}, []bool{true},
			stamp, stamp.Add(30*time.Minute))
		if err := f.service.ApplyMutation(context.Background(), wire); err != nil {
			t.Fatalf("origin-accepted +5m ID rejected against follower clock: %v", err)
		}
		if status, _, err := f.coordinator.Lookup(receiptIDFromWire(t, wire, 0), time.Now()); err != nil ||
			status != mutationreceipt.Confirmed {
			t.Fatalf("origin-boundary receipt = %v, %v", status, err)
		}
	})

	t.Run("unbounded future origin rejected", func(t *testing.T) {
		f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x3c}, 32, 1<<20)
		now := time.Now()
		stamp := now.Add(hlc.DefaultMaxSkew + time.Second)
		origin := hlc.NodeID{0x5c}
		key := graphcache.EdgeKey[string]{Tail: "tail", Head: "future"}
		f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
		wire := replicatedReceiptDelete(t, f, origin, 1, 0x70, []graphcache.EdgeKey[string]{key},
			[]time.Time{stamp.Add(5 * time.Minute)}, []bool{true}, []bool{true},
			stamp, now.Add(30*time.Minute))
		if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("future origin HLC = %v, want InvalidArgument", err)
		}
		if _, live := f.cache.GetWeight(key.Tail, key.Head); !live ||
			f.service.LocalSeq(origin) != 0 || f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 {
			t.Fatal("future origin HLC published follower state")
		}
	})

	t.Run("old boundary includes origin HLC skew", func(t *testing.T) {
		f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x3d}, 32, 1<<20)
		receiverWall := time.Now()
		stamp := receiverWall.Add(hlc.DefaultMaxSkew)
		origin := hlc.NodeID{0x5d}
		key := graphcache.EdgeKey[string]{Tail: "tail", Head: "old-boundary"}
		f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, receiverWall.Add(time.Hour))
		wire := replicatedReceiptDelete(t, f, origin, 1, 0x71, []graphcache.EdgeKey[string]{key},
			[]time.Time{receiverWall.Add(-5 * time.Minute)}, []bool{true}, []bool{true},
			stamp, stamp.Add(30*time.Minute))
		if err := f.service.ApplyMutation(context.Background(), wire); err != nil {
			t.Fatalf("origin-skew old-side boundary = %v", err)
		}
	})

	t.Run("old boundary plus one millisecond rejected", func(t *testing.T) {
		f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x3e}, 32, 1<<20)
		receiverWall := time.Now()
		stamp := receiverWall.Add(hlc.DefaultMaxSkew)
		origin := hlc.NodeID{0x5e}
		key := graphcache.EdgeKey[string]{Tail: "tail", Head: "too-old"}
		f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, receiverWall.Add(time.Hour))
		wire := replicatedReceiptDelete(t, f, origin, 1, 0x72, []graphcache.EdgeKey[string]{key},
			[]time.Time{receiverWall.Add(-5*time.Minute - time.Millisecond)}, []bool{true}, []bool{true},
			stamp, stamp.Add(30*time.Minute))
		if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("origin-skew old-side boundary +1ms = %v, want InvalidArgument", err)
		}
		if _, live := f.cache.GetWeight(key.Tail, key.Head); !live ||
			f.service.LocalSeq(origin) != 0 || f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 {
			t.Fatal("too-old committed ID published follower state")
		}
	})

	t.Run("persisted Store high-water survives wall rollback", func(t *testing.T) {
		rawWall := time.Now().Truncate(time.Millisecond)
		highWater := rawWall.Add(2 * time.Minute)
		config := mutationreceipt.Config{
			Epoch: mutationreceipt.Epoch{0x42}, Retention: time.Hour,
			MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: highWater,
		}
		f := newReceiptEdgeDeleteFixtureWithStoreConfigAndClock(
			t, nil, hlc.NodeID{0x3f}, config,
			hlc.Options{Now: func() int64 { return rawWall.UnixNano() }},
		)
		stamp := highWater.Add(hlc.DefaultMaxSkew)
		origin := hlc.NodeID{0x5f}
		key := graphcache.EdgeKey[string]{Tail: "tail", Head: "rollback"}
		f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, highWater.Add(time.Hour))
		wire := replicatedReceiptDelete(t, f, origin, 1, 0x73, []graphcache.EdgeKey[string]{key},
			[]time.Time{highWater.Add(-5 * time.Minute)}, []bool{true}, []bool{true},
			stamp, highWater.Add(time.Hour))
		if err := f.service.ApplyMutation(context.Background(), wire); err != nil {
			t.Fatalf("receipt at persisted high-water skew boundary = %v", err)
		}
		if _, live := f.cache.GetWeight(key.Tail, key.Head); live ||
			f.service.LocalSeq(origin) != 1 || f.log.Len() != 1 || f.coordinator.store.Stats().Entries != 1 {
			t.Fatal("rollback-high-water receipt did not commit atomically")
		}
		remoteStamp := hlc.Timestamp{WallNs: stamp.UnixNano(), Logical: 1, NodeID: origin}
		if next := f.service.clock.Now(); !remoteStamp.Less(next) {
			t.Fatalf("rollback clock %v did not advance beyond committed origin %v", next, remoteStamp)
		}
		source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
		if err != nil {
			t.Fatal(err)
		}
		capture, err := source.Capture(context.Background(), config)
		if err != nil {
			t.Fatalf("rollback receipt snapshot = %v", err)
		}
		if len(capture.Graph) == 0 || capture.Graph[0].GetHeader() == nil ||
			!remoteStamp.Less(hlcFromProto(capture.Graph[0].GetHeader().GetCutoffHlc())) {
			t.Fatalf("rollback snapshot cutoff did not exceed remote origin: %+v", capture.Graph)
		}
	})
}

func TestEdgeDeleteReceiptFollowerDefiniteWALRetryReusesEvidence(t *testing.T) {
	var attempts []mutationlog.MutationOp
	wal := receiptEdgeDeleteWALFunc(func(entry mutationlog.Entry) error {
		attempts = append(attempts, entry.Op)
		if len(attempts) == 1 {
			return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected definite abort")}
		}
		return nil
	})
	f := newReceiptEdgeDeleteFixtureWithLimits(t, wal, hlc.NodeID{0x38}, 32, 1<<20)
	now := time.Now()
	origin := hlc.NodeID{0x58}
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "retry"}
	f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
	wire := replicatedReceiptDelete(t, f, origin, 1, 0x6c, []graphcache.EdgeKey[string]{key},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now, now.Add(30*time.Minute))

	if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("definite follower WAL abort = %v, want Unavailable", err)
	}
	pending := f.service.pendingMutations[origin][1]
	if pending == nil || pending.receiptWAL == nil || len(attempts) != 1 || attempts[0] != pending.receiptWAL {
		t.Fatalf("definite abort did not retain exact evidence: pending=%+v attempts=%d", pending, len(attempts))
	}
	retained := pending.receiptWAL
	if _, live := f.cache.GetWeight(key.Tail, key.Head); !live ||
		f.service.LocalSeq(origin) != 0 || f.log.Len() != 0 {
		t.Fatalf("definite abort leaked graph/origin/log state")
	}
	if status, _, err := f.coordinator.Lookup(receiptIDFromWire(t, wire, 0), now); err != nil ||
		status != mutationreceipt.NotYetObserved {
		t.Fatalf("definite abort exposed receipt = %v, %v", status, err)
	}

	if err := f.service.ApplyMutation(context.Background(), wire); err != nil {
		t.Fatalf("definite follower WAL retry = %v", err)
	}
	if len(attempts) != 2 || attempts[1] != retained ||
		f.log.RetainedEntries()[0].Op != retained {
		t.Fatal("definite retry replaced the receiver-local WAL evidence")
	}
	if _, live := f.cache.GetWeight(key.Tail, key.Head); live ||
		f.service.LocalSeq(origin) != 1 || f.log.Len() != 1 {
		t.Fatalf("definite retry did not commit exactly once")
	}
}

func TestEdgeDeleteReceiptFollowerIndeterminateWALRetainsEvidenceAndFailStops(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t,
		receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error { return errors.New("lost follower WAL acknowledgement") }),
		hlc.NodeID{0x39}, 32, 1<<20)
	now := time.Now()
	origin := hlc.NodeID{0x59}
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "ambiguous"}
	f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
	wire := replicatedReceiptDelete(t, f, origin, 1, 0x6d, []graphcache.EdgeKey[string]{key},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now, now.Add(30*time.Minute))
	if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeUnavailable ||
		!errors.Is(err, mutationlog.ErrWALIndeterminate) {
		t.Fatalf("indeterminate follower WAL = %v", err)
	}

	pending := f.service.pendingMutations[origin][1]
	var retained *edgeDeleteReceiptEnvelope
	if pending != nil {
		retained, _ = pending.receiptWAL.(*edgeDeleteReceiptEnvelope)
	}
	if pending == nil || retained == nil ||
		retained.Receipts[0].ID != receiptIDFromWire(t, wire, 0) ||
		retained.Origin != origin || retained.OriginSeq != 1 {
		t.Fatalf("indeterminate follower WAL lost retry evidence: %+v", pending)
	}
	if _, live := f.cache.GetWeight(key.Tail, key.Head); !live ||
		f.service.LocalSeq(origin) != 0 || f.log.Len() != 0 {
		t.Fatalf("indeterminate cleanup leaked graph/origin/log state")
	}
	if _, _, err := f.coordinator.Lookup(receiptIDFromWire(t, wire, 0), now); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("receipt lookup after follower ambiguity = %v", err)
	}
	if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("write after follower ambiguity = %v", err)
	}
	if _, err := (&lanternServiceConnect{svc: f.service}).GetServerStatus(
		context.Background(), connect.NewRequest(&pb.GetServerStatusRequest{}),
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("status after follower ambiguity = %v", err)
	}
	if err := f.replication.Snapshot(
		context.Background(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{},
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Snapshot after follower ambiguity = %v", err)
	}
}

func TestEdgeDeleteReceiptFollowerLegacyWALUncertaintyFailStops(t *testing.T) {
	writes := 0
	f := newReceiptEdgeDeleteFixtureWithLimits(t,
		receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
			writes++
			if writes == 1 {
				return errors.New("legacy WAL acknowledgement lost")
			}
			return nil
		}),
		hlc.NodeID{0x40}, 32, 1<<20)
	if _, err := f.log.Append("legacy", hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: hlc.NodeID{0x7a}}); err == nil {
		t.Fatal("legacy append unexpectedly succeeded")
	}

	now := time.Now()
	origin := hlc.NodeID{0x60}
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "legacy-uncertain"}
	f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
	wire := replicatedReceiptDelete(t, f, origin, 1, 0x74, []graphcache.EdgeKey[string]{key},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now, now.Add(30*time.Minute))
	err := f.service.ApplyMutation(context.Background(), wire)
	if connect.CodeOf(err) != connect.CodeUnavailable || !errors.Is(err, mutationlog.ErrLegacyWALUncertain) {
		t.Fatalf("legacy-uncertain follower commit = %v", err)
	}
	pending := f.service.pendingMutations[origin][1]
	if pending == nil || pending.receiptWAL == nil || !f.service.receiptCommitFaulted {
		t.Fatalf("legacy uncertainty lost evidence or fail-stop: %+v", pending)
	}
	if _, live := f.cache.GetWeight(key.Tail, key.Head); !live ||
		f.service.LocalSeq(origin) != 0 || f.log.Len() != 0 || f.coordinator.store.Stats().Entries != 0 {
		t.Fatal("legacy uncertainty leaked graph/Store/origin/log state")
	}
	if _, _, err := f.coordinator.Lookup(receiptIDFromWire(t, wire, 0), now); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("receipt lookup after legacy uncertainty = %v", err)
	}
	if _, err := (&lanternServiceConnect{svc: f.service}).GetEdge(
		context.Background(), connect.NewRequest(&pb.GetEdgeRequest{Tail: key.Tail, Head: key.Head}),
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("graph read after legacy uncertainty = %v", err)
	}
	if _, err := (&lanternServiceConnect{svc: f.service}).GetServerStatus(
		context.Background(), connect.NewRequest(&pb.GetServerStatusRequest{}),
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("status after legacy uncertainty = %v", err)
	}
	if err := f.replication.Snapshot(
		context.Background(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{},
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Snapshot after legacy uncertainty = %v", err)
	}
	if err := f.replication.Subscribe(
		context.Background(), &pb.SubscribeRequest{},
		&receiptDeleteSubscribeSender{frames: make(chan *pb.SubscribeResponse, 1)},
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("Subscribe after legacy uncertainty = %v", err)
	}
	if err := f.service.ApplyMutation(context.Background(), wire); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("legacy uncertainty fail-stop self-cleared before restart: %v", err)
	}
}

func TestEdgeDeleteReceiptFollowerHeldWALHidesWholeCut(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f := newReceiptEdgeDeleteFixtureWithLimits(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		close(entered)
		<-release
		return nil
	}), hlc.NodeID{0x3a}, 32, 1<<20)
	now := time.Now()
	origin := hlc.NodeID{0x5a}
	key := graphcache.EdgeKey[string]{Tail: "tail", Head: "held"}
	f.cache.AddEdgeWithExpiration(key.Tail, key.Head, 1, now.Add(time.Hour))
	wire := replicatedReceiptDelete(t, f, origin, 1, 0x6e, []graphcache.EdgeKey[string]{key},
		[]time.Time{now.Add(-time.Minute)}, []bool{true}, []bool{true}, now, now.Add(30*time.Minute))
	var applies atomic.Int32
	f.service.WithReplicationApplyHook(func(string) { applies.Add(1) })
	applyDone := make(chan error, 1)
	go func() { applyDone <- f.service.ApplyMutation(context.Background(), wire) }()
	waitReceiptTest(t, "follower WAL stage", entered)

	started := make(chan struct{}, 5)
	receiptDone := make(chan mutationreceipt.Status, 1)
	go func() {
		started <- struct{}{}
		status, _, _ := f.coordinator.Lookup(receiptIDFromWire(t, wire, 0), time.Now())
		receiptDone <- status
	}()
	storeDone := make(chan mutationreceipt.Status, 1)
	go func() {
		started <- struct{}{}
		status, _, _ := f.coordinator.store.Lookup(receiptIDFromWire(t, wire, 0), time.Now())
		storeDone <- status
	}()
	graphDone := make(chan bool, 1)
	go func() {
		started <- struct{}{}
		_, live := f.cache.GetWeight(key.Tail, key.Head)
		graphDone <- live
	}()
	originDone := make(chan uint64, 1)
	go func() {
		started <- struct{}{}
		originDone <- f.service.LocalSeq(origin)
	}()
	snapshotDone := make(chan error, 1)
	go func() {
		started <- struct{}{}
		snapshotDone <- f.replication.Snapshot(context.Background(), &pb.SnapshotRequest{}, &replicationSnapshotRecorder{})
	}()
	for range 5 {
		waitReceiptTest(t, "follower observer start", started)
	}
	select {
	case status := <-receiptDone:
		t.Fatalf("receipt status crossed follower WAL: %v", status)
	case status := <-storeDone:
		t.Fatalf("raw Store status crossed follower WAL: %v", status)
	case live := <-graphDone:
		t.Fatalf("graph read crossed follower WAL: %v", live)
	case seq := <-originDone:
		t.Fatalf("origin crossed follower WAL: %d", seq)
	case err := <-snapshotDone:
		t.Fatalf("Snapshot crossed follower WAL: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if applies.Load() != 0 {
		t.Fatal("follower apply metric fired before the whole cut committed")
	}

	close(release)
	if err := waitReceiptTest(t, "follower apply", applyDone); err != nil {
		t.Fatal(err)
	}
	if status := waitReceiptTest(t, "follower receipt", receiptDone); status != mutationreceipt.Confirmed {
		t.Fatalf("receipt after follower commit = %v", status)
	}
	if status := waitReceiptTest(t, "follower Store", storeDone); status != mutationreceipt.Confirmed {
		t.Fatalf("Store after follower commit = %v", status)
	}
	if live := waitReceiptTest(t, "follower graph", graphDone); live {
		t.Fatal("graph after follower commit still contains edge")
	}
	if seq := waitReceiptTest(t, "follower origin", originDone); seq != 1 {
		t.Fatalf("origin after follower commit = %d", seq)
	}
	if err := waitReceiptTest(t, "follower Snapshot", snapshotDone); err != nil {
		t.Fatal(err)
	}
	if applies.Load() != 1 {
		t.Fatalf("follower apply metrics after commit = %d", applies.Load())
	}
}

func TestEdgeDeleteReceiptFollowerConvergesAndRecoversAboveCausalLimit(t *testing.T) {
	f := newReceiptEdgeDeleteFixtureWithLimits(t, nil, hlc.NodeID{0x3d}, 32, 1<<20)
	f.cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
	now := time.Now()
	origin := hlc.NodeID{0x5d}
	firstStamp := now.Add(-time.Millisecond)
	first := &pb.Mutation{
		Origin: origin[:],
		Seq:    1,
		Hlc:    hlcToProto(hlc.Timestamp{WallNs: firstStamp.UnixNano(), Logical: 1, NodeID: origin}),
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{
			Edges: []*pb.EdgeKey{{Tail: "first", Head: "floor"}},
		}}},
		TombstoneExpiration: timestamppb.New(firstStamp.Add(30 * time.Minute)),
	}
	if err := f.service.ApplyMutation(context.Background(), first); err != nil {
		t.Fatalf("seed graph-only receipt follower capacity: %v", err)
	}
	if stats := f.cache.CausalMetadataStats(); stats.EdgeEntries != 1 || stats.EdgeOverLimit {
		t.Fatalf("seed causal metadata = %+v", stats)
	}

	secondKey := graphcache.EdgeKey[string]{Tail: "second", Head: "floor"}
	receipt := replicatedReceiptDelete(t, f, origin, 2, 0x71, []graphcache.EdgeKey[string]{secondKey},
		[]time.Time{now.Add(-time.Minute)}, []bool{false}, []bool{true},
		now, now.Add(30*time.Minute))
	if err := f.service.ApplyMutation(context.Background(), receipt); err != nil {
		t.Fatalf("receipt follower applied local causal admission limit: %v", err)
	}
	if stats := f.cache.CausalMetadataStats(); stats.EdgeEntries != 2 || !stats.EdgeOverLimit {
		t.Fatalf("receipt follower did not converge above local causal limit: %+v", stats)
	}
	if _, ok := f.log.RetainedEntries()[1].Op.(*edgeDeleteReceiptEnvelope); !ok {
		t.Fatalf("over-limit receipt relay = %T", f.log.RetainedEntries()[1].Op)
	}

	entries := f.log.RetainedEntries()
	path := writeReceiptWALAuditEntries(t, entries...)
	config := mutationreceipt.Config{
		Epoch: f.epoch, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
	}
	candidate, err := resumeReceiptWALCandidateWithEffectPolicy(
		path,
		config,
		time.Now(),
		mutationlog.Options{Capacity: 32, SubscriberBuffer: 32},
		time.Hour,
		true,
		func(cache *graphcache.GraphCache[string, *pb.Vertex]) error {
			cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
			return nil
		},
	)
	if err != nil {
		t.Fatalf("restart above causal limit: %v", err)
	}
	t.Cleanup(func() { _ = candidate.log.Close() })
	if stats := candidate.graph.CausalMetadataStats(); stats.EdgeEntries != 2 || !stats.EdgeOverLimit {
		t.Fatalf("restarted causal metadata = %+v", stats)
	}
	if got := candidate.origins.LocalSeq(origin); got != 2 {
		t.Fatalf("restarted origin seq = %d, want 2", got)
	}
	requireReceiptWALEvidence(t, candidate, f.log.RetainedEntries()[1].Op.(*edgeDeleteReceiptEnvelope).Receipts)
}
