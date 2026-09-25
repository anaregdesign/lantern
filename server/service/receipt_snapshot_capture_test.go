package service

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func receiptCapturePolicy(epoch mutationreceipt.Epoch) mutationreceipt.Config {
	return mutationreceipt.Config{Epoch: epoch, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20}
}

func receiptCaptureGraphEdge(capture ReceiptWholeStateCapture, tail, head string) bool {
	for _, frame := range capture.Graph {
		if edge := frame.GetEdge(); edge != nil && edge.GetTail() == tail && edge.GetHead() == head {
			return true
		}
	}
	return false
}

func receiptCaptureEdgeTombstone(capture ReceiptWholeStateCapture, tail, head string) bool {
	for _, frame := range capture.Graph {
		if edge := frame.GetEdgeTombstone(); edge != nil && edge.GetTail() == tail && edge.GetHead() == head {
			return true
		}
	}
	return false
}

func assertReceiptCaptureCut(t *testing.T, capture ReceiptWholeStateCapture, receiptCount int, localSeq uint64, edgeLive, tombstone bool) {
	t.Helper()
	if len(capture.Graph) < 2 || capture.Graph[0].GetHeader() == nil || capture.Graph[len(capture.Graph)-1].GetFooter() == nil {
		t.Fatalf("capture has no complete graph frame stream: %+v", capture.Graph)
	}
	header := capture.Graph[0].GetHeader()
	footer := capture.Graph[len(capture.Graph)-1].GetFooter()
	var counts [6]uint64
	for _, frame := range capture.Graph[1 : len(capture.Graph)-1] {
		switch frame.GetEntry().(type) {
		case *pb.SnapshotResponse_VertexCausalBarrier:
			counts[0]++
		case *pb.SnapshotResponse_EdgeCausalBarrier:
			counts[1]++
		case *pb.SnapshotResponse_VertexTombstone:
			counts[2]++
		case *pb.SnapshotResponse_EdgeTombstone:
			counts[3]++
		case *pb.SnapshotResponse_Vertex:
			counts[4]++
		case *pb.SnapshotResponse_Edge:
			counts[5]++
		default:
			t.Fatalf("unexpected capture frame: %+v", frame)
		}
	}
	wantCounts := [6]uint64{footer.GetVertexCausalBarrierCount(), footer.GetEdgeCausalBarrierCount(),
		footer.GetVertexTombstoneCount(), footer.GetEdgeTombstoneCount(), footer.GetVertexCount(), footer.GetEdgeCount()}
	if counts != wantCounts {
		t.Fatalf("capture footer counts = %v, body = %v", wantCounts, counts)
	}
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 || header.GetCutoffLocalSeq() != localSeq ||
		header.GetCutoffHlc() == nil || len(capture.Receipts.Receipts) != receiptCount ||
		receiptCaptureGraphEdge(capture, "tail", "head") != edgeLive ||
		receiptCaptureEdgeTombstone(capture, "tail", "head") != tombstone ||
		capture.Policy.ClockHighWater.UnixMilli() != capture.Receipts.ClockHighWaterMillis {
		t.Fatalf("inconsistent graph/receipt/local/high-water cut: header=%+v receipts=%+v policy=%+v", header, capture.Receipts, capture.Policy)
	}
	if localSeq == 0 {
		if len(capture.Origins) != 0 || len(header.GetCutoffSeqPerOrigin()) != 0 {
			t.Fatalf("old cut has origin frontier: %+v, %+v", capture.Origins, header)
		}
	} else if len(capture.Origins) != 1 || capture.Origins[0].LastSeq != 1 || len(header.GetCutoffSeqPerOrigin()) != 1 ||
		header.GetCutoffSeqPerOrigin()[hex.EncodeToString(capture.Origins[0].Origin[:])] != 1 {
		t.Fatalf("new cut lost origin frontier: %+v, %+v", capture.Origins, header)
	}
}

func TestReceiptWholeStateCaptureBlocksHeldWALAndCopiesReceipts(t *testing.T) {
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
	policy := receiptCapturePolicy(f.epoch)
	source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	before, err := source.Capture(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptCaptureCut(t, before, 0, 0, true, false)
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	commitDone := make(chan error, 1)
	go func() {
		_, err := f.coordinator.Commit(context.Background(), call)
		commitDone <- err
	}()
	waitReceiptTest(t, "WAL stage", entered)
	type result struct {
		capture ReceiptWholeStateCapture
		err     error
	}
	captureStarted := make(chan struct{})
	captureDone := make(chan result, 1)
	go func() {
		close(captureStarted)
		got, err := source.Capture(context.Background(), policy)
		captureDone <- result{got, err}
	}()
	<-captureStarted
	select {
	case early := <-captureDone:
		t.Fatalf("capture crossed staged WAL: %+v", early)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := waitReceiptTest(t, "commit", commitDone); err != nil {
		t.Fatal(err)
	}
	after := waitReceiptTest(t, "post-publication capture", captureDone)
	if after.err != nil {
		t.Fatal(after.err)
	}
	assertReceiptCaptureCut(t, after.capture, 1, 1, false, true)
	if after.capture.Receipts.Receipts[0].Result[0] != 1 {
		t.Fatalf("original Delete result lost: %+v", after.capture.Receipts.Receipts[0])
	}
	// The returned receipt result is owned by the capture, not the Store.
	after.capture.Receipts.Receipts[0].Result[0] = 0
	again, err := source.Capture(context.Background(), policy)
	if err != nil || again.Receipts.Receipts[0].Result[0] != 1 {
		t.Fatalf("capture result aliased Store: %+v, %v", again.Receipts, err)
	}
}

func TestReceiptWholeStateCaptureReconcilesStoreClockIntoGraphCutoff(t *testing.T) {
	const cutoffMillis int64 = 1_000
	policy := mutationreceipt.Config{
		Epoch:          mutationreceipt.Epoch{0x51},
		Retention:      time.Hour,
		MaxEntries:     32,
		MaxBytes:       1 << 20,
		ClockHighWater: time.UnixMilli(cutoffMillis + 1),
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x52}, hlc.Options{
		Now: func() int64 { return cutoffMillis * int64(time.Millisecond) },
	})
	svc := NewLanternService(cache).
		WithReplication(log, clock, nil).
		WithTombstoneTTL(time.Hour)
	source, err := NewReceiptWholeStateSource(svc, store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.Capture(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := capture.Graph[0].GetHeader().GetCutoffHlc()
	if cutoff.GetWallNs()/int64(time.Millisecond) < capture.Receipts.ClockHighWaterMillis ||
		cutoff.GetLogical() == 0 {
		t.Fatalf("reconciled cutoff = %+v, high-water=%d", cutoff, capture.Receipts.ClockHighWaterMillis)
	}
}

func TestReceiptWholeStateCaptureRejectsUnrepresentableClockHighWater(t *testing.T) {
	policy := mutationreceipt.Config{
		Epoch:          mutationreceipt.Epoch{0x53},
		Retention:      time.Hour,
		MaxEntries:     32,
		MaxBytes:       1 << 20,
		ClockHighWater: time.UnixMilli(math.MaxInt64),
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x54}, hlc.Options{})
	svc := NewLanternService(cache).
		WithReplication(log, clock, nil).
		WithTombstoneTTL(time.Hour)
	source, err := NewReceiptWholeStateSource(svc, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Capture(context.Background(), policy); err == nil {
		t.Fatal("unrepresentable Store high-water produced a Snapshot cut")
	}
}

func TestReceiptWholeStateCaptureDefiniteAbortKeepsGraphAndCutoff(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error {
		return &mutationlog.DefiniteWALAbort{Cause: errors.New("injected abort")}
	}))
	f.cache.AddEdgeWithExpiration("tail", "head", 1, time.Now().Add(time.Hour))
	policy := receiptCapturePolicy(f.epoch)
	before, err := f.coordinator.captureReceiptWholeState(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("definite WAL abort = %v", err)
	}
	after, err := f.coordinator.captureReceiptWholeState(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptCaptureCut(t, before, 0, 0, true, false)
	assertReceiptCaptureCut(t, after, 0, 0, true, false)
	if f.log.Len() != 0 {
		t.Fatalf("definite abort appended log entry: %d", f.log.Len())
	}
	if after.Receipts.ClockHighWaterMillis < before.Receipts.ClockHighWaterMillis {
		t.Fatalf("aborted attempt rolled back monotonic high-water: %d < %d", after.Receipts.ClockHighWaterMillis, before.Receipts.ClockHighWaterMillis)
	}
}

func TestReceiptWholeStateCaptureCopiesUncommittedClockAdvance(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	now := time.Now().Add(10 * time.Minute)
	tx, err := f.coordinator.store.Begin(now)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	lookupAt := now.Add(10 * time.Minute)
	if status, _, err := f.coordinator.Lookup(call.Items[0].ID, lookupAt); err != nil || status != mutationreceipt.NotYetObserved {
		t.Fatalf("uncommitted receipt Lookup = %v, %v", status, err)
	}
	capture, err := f.coordinator.captureReceiptWholeState(context.Background(), receiptCapturePolicy(f.epoch))
	if err != nil {
		t.Fatal(err)
	}
	if capture.Receipts.ClockHighWaterMillis < lookupAt.UnixMilli() ||
		capture.Policy.ClockHighWater.UnixMilli() != capture.Receipts.ClockHighWaterMillis ||
		capture.Graph[0].GetHeader().GetCutoffHlc().GetWallNs()/int64(time.Millisecond) <
			capture.Receipts.ClockHighWaterMillis {
		t.Fatalf("uncommitted high-water missing from coherent capture: %+v", capture)
	}
}

func TestReceiptWholeStateCaptureFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wal    mutationlog.WAL
		mutate func(*receiptEdgeDeleteFixture, *mutationreceipt.Config)
		want   error
	}{
		{"policy epoch", nil, func(_ *receiptEdgeDeleteFixture, policy *mutationreceipt.Config) {
			policy.Epoch = mutationreceipt.Epoch{0x43}
		}, mutationreceipt.ErrInvalidSnapshot},
		{"policy retention", nil, func(_ *receiptEdgeDeleteFixture, policy *mutationreceipt.Config) { policy.Retention = 2 * time.Hour }, mutationreceipt.ErrInvalidSnapshot},
		{"policy high-water", nil, func(_ *receiptEdgeDeleteFixture, policy *mutationreceipt.Config) {
			policy.ClockHighWater = time.Now().Add(time.Hour)
		}, mutationreceipt.ErrInvalidSnapshot},
		{"indeterminate WAL", receiptEdgeDeleteWALFunc(func(mutationlog.Entry) error { return errors.New("uncertain write") }), func(f *receiptEdgeDeleteFixture, _ *mutationreceipt.Config) {
			call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
			_, _ = f.coordinator.Commit(context.Background(), call)
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := receiptCapturePolicy(mutationreceipt.Epoch{0x42})
			f := newReceiptEdgeDeleteFixture(t, tc.wal)
			if tc.mutate != nil {
				tc.mutate(&f, &policy)
			}
			capture, err := f.coordinator.captureReceiptWholeState(context.Background(), policy)
			if err == nil || !reflect.DeepEqual(capture, ReceiptWholeStateCapture{}) {
				t.Fatalf("invalid capture returned partial state: %+v, %v", capture, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("invalid capture error = %v, want %v", err, tc.want)
			}
			if tc.want == nil && connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("poisoned capture = %v, want FailedPrecondition", err)
			}
		})
	}
}

func TestReceiptWholeStateCaptureRejectsSnapshotInstall(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	finish, err := f.service.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	defer finish(false)
	capture, err := f.coordinator.captureReceiptWholeState(context.Background(), receiptCapturePolicy(f.epoch))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !reflect.DeepEqual(capture, ReceiptWholeStateCapture{}) {
		t.Fatalf("incomplete install capture = %+v, %v", capture, err)
	}
}

func TestReceiptWholeStateCaptureFrameValuesAreDetached(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	value := &pb.Vertex{Key: "value", Value: &pb.Vertex_String_{String_: "original"}}
	if err := f.cache.PutVertex("value", value); err != nil {
		t.Fatal(err)
	}
	capture, err := f.coordinator.captureReceiptWholeState(context.Background(), receiptCapturePolicy(f.epoch))
	if err != nil {
		t.Fatal(err)
	}
	var copied *pb.Vertex
	for _, frame := range capture.Graph {
		if vertex := frame.GetVertex().GetVertex(); vertex != nil && vertex.GetKey() == "value" {
			copied = vertex
		}
	}
	if copied == nil || !proto.Equal(copied, value) {
		t.Fatalf("missing captured Vertex: %+v", copied)
	}
	value.Value = &pb.Vertex_String_{String_: "changed"}
	if copied.GetString_() != "original" {
		t.Fatalf("captured Vertex changed with cache alias: %+v", copied)
	}
}
