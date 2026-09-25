package service

import (
	"context"
	"errors"
	"reflect"
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

func newReceiptEdgeDeleteFixture(t *testing.T, wal mutationlog.WAL) receiptEdgeDeleteFixture {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x42}
	store, err := mutationreceipt.New(mutationreceipt.Config{
		Epoch: epoch, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x41}, hlc.Options{})
	svc := NewLanternService(cache).WithReplication(log, clock, nil).WithTombstoneTTL(time.Hour)
	coordinator, err := newEdgeDeleteReceiptCoordinator(svc, store)
	if err != nil {
		t.Fatal(err)
	}
	rep := NewLanternReplicationService(log, cache, clock).WithOriginStates(svc)
	return receiptEdgeDeleteFixture{coordinator, svc, cache, log, rep, epoch}
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

func waitReceiptTest[T any](t *testing.T, label string, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		var zero T
		return zero
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
