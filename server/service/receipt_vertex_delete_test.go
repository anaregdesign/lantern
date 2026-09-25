package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type receiptVertexDeleteWALFunc func(mutationlog.Entry) error

func (f receiptVertexDeleteWALFunc) Write(entry mutationlog.Entry) error { return f(entry) }

type receiptVertexDeleteFixture struct {
	coordinator *vertexDeleteReceiptCoordinator
	service     *LanternService
	cache       *graphcache.GraphCache[string, *pb.Vertex]
	log         *mutationlog.Log
	store       *mutationreceipt.Store
	replication *LanternReplicationService
	epoch       mutationreceipt.Epoch
}

func newReceiptVertexDeleteFixture(
	t *testing.T,
	wal mutationlog.WAL,
	node hlc.NodeID,
	maxEntries int,
	configure func(*graphcache.GraphCache[string, *pb.Vertex]),
) receiptVertexDeleteFixture {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x72}
	store, err := mutationreceipt.New(mutationreceipt.Config{
		Epoch: epoch, Retention: time.Hour, MaxEntries: maxEntries, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	if configure != nil {
		configure(cache)
	}
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	service := NewLanternService(cache).
		WithReplication(log, hlc.New(node, hlc.Options{}), nil).
		WithTombstoneTTL(time.Hour)
	coordinator, err := newVertexDeleteReceiptCoordinator(service, store)
	if err != nil {
		t.Fatal(err)
	}
	replication := NewLanternReplicationService(log, cache, service.clock).WithOriginStates(service)
	return receiptVertexDeleteFixture{coordinator, service, cache, log, store, replication, epoch}
}

func receiptVertexDeleteTestCall(
	t *testing.T,
	epoch mutationreceipt.Epoch,
	groupSeed byte,
	keys ...string,
) receiptVertexDeleteCall {
	t.Helper()
	call := receiptVertexDeleteCall{
		Group: mutationreceipt.GroupID{groupSeed},
		Items: make([]receiptVertexDeleteItem, len(keys)),
	}
	issued := time.Now().Add(-time.Second)
	for i, key := range keys {
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{groupSeed, byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		call.Items[i] = receiptVertexDeleteItem{ID: id, Key: key}
	}
	return call
}

func TestPublicVertexDeleteReceiptsPreserveExactAbsentResult(t *testing.T) {
	runtime, service, _ := newActivatedReceiptService(t, 8)
	receiptContext := publicReceiptContext(
		t,
		runtime,
		0x41,
		1,
	)
	beforeLog := runtime.log.Len()
	beforeSeq := service.LocalSeq(service.clock.NodeID())
	response, err := service.DeleteVertex(context.Background(), &pb.DeleteVertexRequest{
		Key: "absent", ReceiptContext: receiptContext,
	})
	if err != nil || response.GetExisted() ||
		runtime.log.Len() != beforeLog+1 ||
		service.LocalSeq(service.clock.NodeID()) != beforeSeq+1 {
		t.Fatalf("public absent Delete = (%+v, %v), log=%d seq=%d",
			response, err, runtime.log.Len(), service.LocalSeq(service.clock.NodeID()))
	}
	replay, err := service.DeleteVertex(context.Background(), &pb.DeleteVertexRequest{
		Key: "absent", ReceiptContext: receiptContext,
	})
	if err != nil || replay.GetExisted() || runtime.log.Len() != beforeLog+1 {
		t.Fatalf("public absent Delete replay = (%+v, %v), log=%d", replay, err, runtime.log.Len())
	}
	status, err := service.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: receiptContext.GetOperationIds()[0],
	})
	result := status.GetStatus().GetReceipt().GetOriginalResult()
	_, hasDeleteResult := result.GetResult().(*pb.ReceiptResult_DeleteVertexExisted)
	if err != nil ||
		status.GetStatus().GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		!hasDeleteResult || result.GetDeleteVertexExisted() {
		t.Fatalf("public absent Delete status = (%+v, %v)", status, err)
	}
}

func TestVertexDeleteReceiptCoordinatorPreservesExactResultsAndRetry(t *testing.T) {
	f := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x81}, 32, nil)
	expiration := time.Now().Add(time.Hour)
	if err := f.cache.PutVertexWithExpiration(
		"present",
		&pb.Vertex{Key: "present", Value: &pb.Vertex_String_{String_: "value"}},
		expiration,
	); err != nil {
		t.Fatal(err)
	}
	f.cache.ApplyVertexCausalBarrierHLC("rejected", hlc.Timestamp{
		WallNs: time.Now().Add(time.Minute).UnixNano(), NodeID: hlc.NodeID{0x7f},
	})
	call := receiptVertexDeleteTestCall(t, f.epoch, 0x51,
		"present", "absent", "present", "rejected")
	response, err := f.coordinator.Commit(context.Background(), call)
	want := []bool{true, false, false, false}
	if err != nil || response.GetDeleted() != 1 || !reflect.DeepEqual(response.GetExisted(), want) {
		t.Fatalf("Commit = (%v, %v), want %v", response, err, want)
	}
	if _, live := f.cache.GetVertex("present"); live {
		t.Fatal("committed exact Delete left vertex live")
	}
	if f.log.Len() != 1 || f.service.LocalSeq(f.service.clock.NodeID()) != 1 {
		t.Fatalf("publication = log %d seq %d, want 1/1",
			f.log.Len(), f.service.LocalSeq(f.service.clock.NodeID()))
	}
	envelope, ok := f.log.RetainedEntries()[0].Op.(*vertexDeleteReceiptEnvelope)
	if !ok || len(envelope.Receipts) != len(want) ||
		len(envelope.Accepted) != 3 ||
		envelope.Accepted[0].Index != 0 ||
		envelope.Accepted[1].Index != 1 ||
		envelope.Accepted[2].Index != 2 {
		t.Fatalf("WAL envelope = %#v", envelope)
	}
	for i, item := range call.Items {
		status, receipt, err := f.coordinator.Lookup(item.ID, time.Now())
		if err != nil || status != mutationreceipt.Confirmed ||
			receipt.Index != uint32(i) || !reflect.DeepEqual(receipt.Result, []byte{boolByte(want[i])}) {
			t.Fatalf("receipt[%d] = %v, %+v, %v", i, status, receipt, err)
		}
	}
	retry, err := f.coordinator.Commit(context.Background(), call)
	if err != nil || !reflect.DeepEqual(retry.GetExisted(), want) || f.log.Len() != 1 {
		t.Fatalf("duplicate retry = (%v, %v), log=%d", retry, err, f.log.Len())
	}
	conflict := call
	conflict.Items = append([]receiptVertexDeleteItem(nil), call.Items...)
	conflict.Items[0].Key = "changed"
	if _, err := f.coordinator.Commit(context.Background(), conflict); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("intent conflict = %v, want InvalidArgument", err)
	}
}

func TestVertexDeleteReceiptCoordinatorReceiptOnlyPublication(t *testing.T) {
	f := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x82}, 8, nil)
	f.cache.ApplyVertexCausalBarrierHLC("already-fenced", hlc.Timestamp{
		WallNs: time.Now().Add(time.Hour).UnixNano(), NodeID: hlc.NodeID{0x7f},
	})
	call := receiptVertexDeleteTestCall(t, f.epoch, 0x52, "already-fenced")
	response, err := f.coordinator.Commit(context.Background(), call)
	if err != nil || response.GetDeleted() != 0 ||
		!reflect.DeepEqual(response.GetExisted(), []bool{false}) {
		t.Fatalf("receipt-only response = (%v, %v)", response, err)
	}
	envelope := f.log.RetainedEntries()[0].Op.(*vertexDeleteReceiptEnvelope)
	if len(envelope.Accepted) != 0 || len(envelope.Receipts) != 1 ||
		f.log.Len() != 1 || f.service.LocalSeq(f.service.clock.NodeID()) != 1 {
		t.Fatalf("receipt-only envelope = %#v log=%d seq=%d",
			envelope, f.log.Len(), f.service.LocalSeq(f.service.clock.NodeID()))
	}
	for _, projection := range []pb.SubscribeProjection{
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_FULL_MUTATION,
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
	} {
		ctx, cancel := context.WithCancel(context.Background())
		sender := &receiptDeleteSubscribeSender{frames: make(chan *pb.SubscribeResponse, 1)}
		done := make(chan error, 1)
		go func() {
			done <- f.replication.Subscribe(ctx, &pb.SubscribeRequest{
				Projection: projection, FromLocalSeq: 0, AcceptReceiptEnvelopes: true,
			}, sender)
		}()
		frame := waitReceiptTest(t, "Vertex Delete receipt-only Subscribe", sender.frames)
		if projection == pb.SubscribeProjection_SUBSCRIBE_PROJECTION_FULL_MUTATION {
			if frame.GetMutation().GetSeq() != 1 ||
				len(frame.GetMutation().GetOp().GetReplicatedReceiptVertexDelete().GetItems()) != 1 {
				t.Fatalf("full projection = %+v", frame)
			}
		} else if chunk := frame.GetIdentityChunk(); chunk.GetSeq() != 1 ||
			!chunk.GetIsLast() || len(chunk.GetVertexKeys()) != 0 ||
			chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY {
			t.Fatalf("identity projection = %+v", frame)
		}
		cancel()
		waitReceiptTest(t, "Subscribe cancellation", done)
	}
}

func TestVertexDeleteReceiptCoordinatorRejectsOversizedReceiverLocalRelay(t *testing.T) {
	node := hlc.NodeID{0x83}
	const itemCount = 8
	keys := make([]string, itemCount)
	for i := range keys {
		keys[i] = fmt.Sprintf("relay-vertex-%02d", i)
	}
	prepare := func(t *testing.T) receiptVertexDeleteFixture {
		t.Helper()
		f := newReceiptVertexDeleteFixture(t, nil, node, 32, nil)
		for i, key := range keys {
			if i%2 == 0 {
				if !f.cache.ApplyVertexCausalBarrierHLC(key, hlc.Timestamp{
					WallNs: time.Now().Add(time.Minute).UnixNano(), NodeID: node,
				}) {
					t.Fatalf("cannot install causal barrier for %q", key)
				}
			} else if err := f.cache.PutVertexWithExpiration(
				key, &pb.Vertex{Key: key}, time.Now().Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}
		}
		return f
	}
	reference := prepare(t)
	call := receiptVertexDeleteTestCall(t, reference.epoch, 0x66, keys...)
	if _, err := reference.coordinator.Commit(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	envelope := reference.log.RetainedEntries()[0].Op.(*vertexDeleteReceiptEnvelope)
	sparseSize, err := validateReplicationFrameSize(envelope, 0)
	if err != nil {
		t.Fatal(err)
	}
	maximalSize, err := validateReplicationFrameSize(maximalReceiptVertexDeleteEnvelope(envelope), 0)
	if err != nil || maximalSize <= sparseSize+1 {
		t.Fatalf("sparse/maximal Vertex Delete sizes = %d/%d, %v", sparseSize, maximalSize, err)
	}

	rejected := prepare(t)
	rejected.service.replicationFrameCertified = true
	rejected.service.replicationSendMaxBytes = maximalSize - 1
	rejections := 0
	rejected.service.onValidationReject = func(reason string) {
		if reason != "replication_frame" {
			t.Errorf("validation rejection reason = %q", reason)
		}
		rejections++
	}
	_, err = rejected.coordinator.Commit(
		context.Background(),
		receiptVertexDeleteTestCall(t, rejected.epoch, 0x67, keys...),
	)
	if connect.CodeOf(err) != connect.CodeResourceExhausted ||
		!strings.Contains(err.Error(), fmt.Sprintf("LANTERN_MAX_SEND_MSG_BYTES=%d", maximalSize-1)) ||
		rejections != 1 {
		t.Fatalf("one-byte-under receiver-local relay = %v, rejections=%d", err, rejections)
	}
	for i, key := range keys {
		if i%2 == 1 {
			if _, live := rejected.cache.GetVertex(key); !live {
				t.Fatalf("rejected Vertex Delete changed %q", key)
			}
		}
	}
	if rejected.store.Stats().Entries != 0 || rejected.log.Len() != 0 ||
		rejected.service.LocalSeq(node) != 0 {
		t.Fatal("rejected Vertex Delete changed Store, log, or origin")
	}

	sparseWire, err := envelope.ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	rejectedRelay := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x84}, 32, nil)
	rejectedRelay.service.replicationFrameCertified = true
	rejectedRelay.service.replicationSendMaxBytes = maximalSize - 1
	if err := rejectedRelay.service.ApplyMutation(context.Background(), sparseWire); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("one-byte-under sparse Vertex Delete relay = %v, want ResourceExhausted", err)
	}
	rejectedGraph := rejectedRelay.cache.SnapshotReplication()
	if len(rejectedGraph.Tombstones.Vertices) != 0 ||
		len(rejectedGraph.Barriers.Vertices) != 0 ||
		len(rejectedGraph.Graph.Vertices) != 0 ||
		rejectedRelay.store.Stats().Entries != 0 || rejectedRelay.log.Len() != 0 ||
		rejectedRelay.service.LocalSeq(node) != 0 {
		t.Fatal("rejected sparse Vertex Delete relay changed graph, Store, log, or origin")
	}

	exactRelay := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x85}, 32, nil)
	exactRelay.service.replicationFrameCertified = true
	exactRelay.service.replicationSendMaxBytes = maximalSize
	if err := exactRelay.service.ApplyMutation(context.Background(), sparseWire); err != nil {
		t.Fatalf("exact-fit sparse Vertex Delete relay: %v", err)
	}
	if tombstones := exactRelay.cache.SnapshotReplication().Tombstones.Vertices; len(tombstones) != itemCount ||
		exactRelay.store.Stats().Entries != itemCount || exactRelay.log.Len() != 1 ||
		exactRelay.service.LocalSeq(node) != 1 {
		t.Fatal("exact-fit Vertex Delete relay lost causal effects, receipts, log, or origin")
	}
	if size, err := validateReplicationFrameSize(exactRelay.log.RetainedEntries()[0].Op, 0); err != nil || size != maximalSize {
		t.Fatalf("exact-fit Vertex Delete relay frame = %d, %v, want %d", size, err, maximalSize)
	}
}

func TestVertexDeleteReceiptCoordinatorRejectsBeforeGraphOrLog(t *testing.T) {
	t.Run("Hard batch limit", func(t *testing.T) {
		f := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x80}, 1, nil)
		call := receiptVertexDeleteCall{
			Group: mutationreceipt.GroupID{0x50},
			Items: make([]receiptVertexDeleteItem, receiptVertexWALMaxItems+1),
		}
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("batch-limit rejection = %v, want InvalidArgument", err)
		}
		if f.log.Len() != 0 || f.store.Stats().Entries != 0 {
			t.Fatal("batch-limit rejection changed log or Store")
		}
	})

	t.Run("Store capacity", func(t *testing.T) {
		f := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x83}, 1, nil)
		if err := f.cache.PutVertexWithExpiration("one", &pb.Vertex{Key: "one"}, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		call := receiptVertexDeleteTestCall(t, f.epoch, 0x53, "one", "two")
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("capacity rejection = %v, want ResourceExhausted", err)
		}
		if _, live := f.cache.GetVertex("one"); !live || f.log.Len() != 0 ||
			f.service.LocalSeq(f.service.clock.NodeID()) != 0 {
			t.Fatal("capacity rejection changed graph, log, or origin")
		}
	})

	t.Run("WAL capacity", func(t *testing.T) {
		f := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x85}, 8, nil)
		beforeStore := f.store.Stats()
		call := receiptVertexDeleteTestCall(
			t,
			f.epoch,
			0x54,
			strings.Repeat("k", receiptVertexWALMaxBytes),
		)
		_, err := f.coordinator.Commit(context.Background(), call)
		if connect.CodeOf(err) != connect.CodeResourceExhausted ||
			!errors.Is(err, errReceiptVertexWALCapacity) {
			t.Fatalf("WAL capacity rejection = %v, want ResourceExhausted capacity error", err)
		}
		causal := f.cache.CausalMetadataStats()
		if f.store.Stats() != beforeStore || f.log.Len() != 0 ||
			f.service.LocalSeq(f.service.clock.NodeID()) != 0 ||
			f.cache.VertexHLCCount() != 0 || causal.VertexEntries != 0 {
			t.Fatal("WAL capacity rejection changed graph, Store, log, or origin")
		}
	})

	t.Run("Search preparation", func(t *testing.T) {
		var reject atomic.Bool
		f := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x84}, 8,
			func(cache *graphcache.GraphCache[string, *pb.Vertex]) {
				cache.EnableSearchIndex(
					func(_ string, value *pb.Vertex) search.Document {
						if reject.Load() {
							return search.Text("oversized")
						}
						return search.Text(value.GetString_())
					},
					strings.Compare,
					graphcache.WithSearchAnalysisLimits(search.SearchAnalysisLimits{MaxDocumentBytes: 4}),
				)
			})
		if err := f.cache.PutVertexWithExpiration(
			"search",
			&pb.Vertex{Key: "search", Value: &pb.Vertex_String_{String_: "ok"}},
			time.Now().Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		reject.Store(true)
		call := receiptVertexDeleteTestCall(t, f.epoch, 0x54, "search")
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("search rejection = %v, want ResourceExhausted", err)
		}
		if _, live := f.cache.GetVertex("search"); !live || f.log.Len() != 0 ||
			f.store.Stats().Entries != 0 {
			t.Fatal("search rejection changed graph, receipt Store, or log")
		}
	})
}

func TestVertexDeleteReceiptCoordinatorWALFailureSemantics(t *testing.T) {
	t.Run("Definite rollback and retry", func(t *testing.T) {
		var writes atomic.Int32
		wal := receiptVertexDeleteWALFunc(func(mutationlog.Entry) error {
			if writes.Add(1) == 1 {
				return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected definite abort")}
			}
			return nil
		})
		f := newReceiptVertexDeleteFixture(t, wal, hlc.NodeID{0x85}, 8, nil)
		if err := f.cache.PutVertexWithExpiration(
			"definite", &pb.Vertex{Key: "definite"}, time.Now().Add(time.Hour),
		); err != nil {
			t.Fatal(err)
		}
		call := receiptVertexDeleteTestCall(t, f.epoch, 0x55, "definite")
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("definite WAL failure = %v, want Unavailable", err)
		}
		if _, live := f.cache.GetVertex("definite"); !live || f.log.Len() != 0 ||
			f.service.LocalSeq(f.service.clock.NodeID()) != 0 || f.store.Stats().Entries != 0 ||
			f.service.receiptCommitFaulted {
			t.Fatal("definite WAL abort did not roll back exactly")
		}
		if _, err := f.coordinator.Commit(context.Background(), call); err != nil {
			t.Fatalf("retry after definite abort: %v", err)
		}
	})

	t.Run("Ambiguous failure fail-stops", func(t *testing.T) {
		f := newReceiptVertexDeleteFixture(t,
			receiptVertexDeleteWALFunc(func(mutationlog.Entry) error {
				return errors.New("lost WAL acknowledgement")
			}),
			hlc.NodeID{0x86}, 8, nil,
		)
		call := receiptVertexDeleteTestCall(t, f.epoch, 0x56, "ambiguous")
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("ambiguous WAL failure = %v, want Unavailable", err)
		}
		if !f.service.receiptCommitFaulted || f.store.Stats().Entries != 0 || f.log.Len() != 0 {
			t.Fatal("ambiguous WAL failure leaked state or did not fail-stop")
		}
		if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("post-ambiguity write = %v, want FailedPrecondition", err)
		}
	})
}

func TestVertexDeleteReceiptFollowerOutOfOrderAndDuplicate(t *testing.T) {
	origin := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x91}, 16, nil)
	remote := newReceiptVertexDeleteFixture(t, nil, hlc.NodeID{0x92}, 16, nil)
	for _, key := range []string{"first", "second"} {
		for _, fixture := range []*receiptVertexDeleteFixture{&origin, &remote} {
			if err := fixture.cache.PutVertexWithExpiration(
				key, &pb.Vertex{Key: key}, time.Now().Add(time.Hour),
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := receiptVertexDeleteTestCall(t, origin.epoch, 0x61, "first")
	second := receiptVertexDeleteTestCall(t, origin.epoch, 0x62, "second")
	if _, err := origin.coordinator.Commit(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.coordinator.Commit(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	firstWire, err := origin.log.RetainedEntries()[0].Op.(*vertexDeleteReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	secondWire, err := origin.log.RetainedEntries()[1].Op.(*vertexDeleteReceiptEnvelope).ReplicationMutation()
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.service.ApplyMutation(context.Background(), secondWire); err != nil {
		t.Fatalf("out-of-order seq 2: %v", err)
	}
	if remote.log.Len() != 0 || remote.service.LocalSeq(origin.service.clock.NodeID()) != 0 {
		t.Fatal("out-of-order envelope published before its predecessor")
	}
	if err := remote.service.ApplyMutation(context.Background(), firstWire); err != nil {
		t.Fatalf("seq 1 drain: %v", err)
	}
	if remote.log.Len() != 2 || remote.service.LocalSeq(origin.service.clock.NodeID()) != 2 {
		t.Fatalf("drained publication = log %d seq %d, want 2/2",
			remote.log.Len(), remote.service.LocalSeq(origin.service.clock.NodeID()))
	}
	if err := remote.service.ApplyMutation(context.Background(), firstWire); err != nil || remote.log.Len() != 2 {
		t.Fatalf("duplicate replay = %v, log=%d", err, remote.log.Len())
	}
	for _, call := range []receiptVertexDeleteCall{first, second} {
		if status, _, err := remote.coordinator.Lookup(call.Items[0].ID, time.Now()); err != nil ||
			status != mutationreceipt.Confirmed {
			t.Fatalf("remote receipt = %v, %v", status, err)
		}
	}
}
