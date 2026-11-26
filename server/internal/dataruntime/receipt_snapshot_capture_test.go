package dataruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math"
	"path/filepath"
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
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT || header.GetCutoffLocalSeq() != localSeq ||
		header.GetCutoffHlc() == nil || len(capture.Receipts.Receipts) != receiptCount ||
		receiptCaptureGraphEdge(capture, "tail", "head") != edgeLive ||
		receiptCaptureEdgeTombstone(capture, "tail", "head") != tombstone ||
		capture.Policy.ClockHighWater.UnixMilli() != capture.Receipts.ClockHighWaterMillis ||
		capture.Retired.ClockHighWaterMillis != capture.Receipts.ClockHighWaterMillis {
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

func TestReceiptWholeStateCapturePreservesVertexReceiptKindsAndOpaqueResults(t *testing.T) {
	policy := receiptCapturePolicy(mutationreceipt.Epoch{0x61})
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	type receiptCase struct {
		kind   mutationreceipt.Kind
		result []byte
		seed   byte
	}
	cases := []receiptCase{
		{kind: mutationreceipt.PutVertex, result: []byte{1, 0, 0xff, 7}, seed: 0x11},
		{kind: mutationreceipt.DeleteVertex, result: []byte{0, 0xfe, 9}, seed: 0x12},
	}
	for _, tc := range cases {
		id, err := mutationreceipt.NewID(policy.Epoch, issued, [24]byte{tc.seed})
		if err != nil {
			t.Fatal(err)
		}
		intent := mutationreceipt.Intent{
			ID: id, Group: mutationreceipt.GroupID{tc.seed}, Count: 1,
			Kind: tc.kind, Digest: mutationreceipt.IntentDigest([]byte{tc.seed}),
		}
		tx, err := store.Begin(issued)
		if err != nil {
			t.Fatal(err)
		}
		classification, _, err := tx.Classify([]mutationreceipt.Intent{intent})
		if err != nil || classification != mutationreceipt.Fresh {
			tx.Abort()
			t.Fatalf("classify %v = %v, %v", tc.kind, classification, err)
		}
		if err := tx.Reserve([][]byte{tc.result}); err != nil {
			tx.Abort()
			t.Fatal(err)
		}
		if err := tx.Stage(); err != nil {
			tx.Abort()
			t.Fatal(err)
		}
		tx.Commit()
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	log := mutationlog.New(mutationlog.Options{Capacity: 4})
	t.Cleanup(func() { _ = log.Close() })
	service := NewExecutor(cache).
		WithReplication(log, hlc.New(hlc.NodeID{0x62}, hlc.Options{}), nil).
		WithTombstoneTTL(time.Hour)
	source, err := NewReceiptWholeStateSource(service, store)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := source.Capture(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Receipts.Receipts) != len(cases) {
		t.Fatalf("captured receipt rows = %d, want %d", len(capture.Receipts.Receipts), len(cases))
	}
	byKind := make(map[mutationreceipt.Kind][]byte, len(cases))
	for _, receipt := range capture.Receipts.Receipts {
		byKind[receipt.Kind] = receipt.Result
	}
	for _, tc := range cases {
		if got := byKind[tc.kind]; !bytes.Equal(got, tc.result) {
			t.Fatalf("captured %v result = %x, want %x", tc.kind, got, tc.result)
		}
	}
	restored, err := mutationreceipt.NewFromSnapshot(capture.Policy, capture.Receipts)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := restored.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range roundTrip.Receipts {
		if want := byKind[receipt.Kind]; !bytes.Equal(receipt.Result, want) {
			t.Fatalf("restored %v result = %x, want %x", receipt.Kind, receipt.Result, want)
		}
	}
	capture.Receipts.Receipts[0].Result[0] ^= 0xff
	again, err := source.Capture(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range again.Receipts.Receipts {
		for _, tc := range cases {
			if receipt.Kind == tc.kind && !bytes.Equal(receipt.Result, tc.result) {
				t.Fatalf("captured %v result aliases previous Snapshot: %x", tc.kind, receipt.Result)
			}
		}
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
	svc := NewExecutor(cache).
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

func TestReceiptWholeStateCaptureIncludesRetiredCatalogAtActiveHighWater(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	policy := receiptCapturePolicy(f.epoch)
	source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	highWater := time.Now().UTC()
	tx, err := f.coordinator.store.Begin(highWater)
	if err != nil {
		t.Fatal(err)
	}
	tx.Abort()
	active, err := f.coordinator.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	state := mutationreceipt.RetiredCatalogSnapshot{
		Version:              1,
		ClockHighWaterMillis: active.ClockHighWaterMillis,
		Epochs: []mutationreceipt.RetiredEpochSnapshot{
			receiptSnapshotTestRetiredMember(
				t,
				mutationreceipt.Epoch{0x21},
				active.ClockHighWater(),
				0x22,
			),
		},
	}
	if _, err := source.Capture(t.Context(), policy); err != nil {
		t.Fatal(err)
	}
	mustReplaceRetiredCatalog(
		t,
		f.service.receiptRetiredCatalog,
		policy,
		active.ClockHighWaterMillis,
		state,
	)
	capture, err := source.Capture(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if capture.Receipts.ClockHighWaterMillis != capture.Retired.ClockHighWaterMillis ||
		!reflect.DeepEqual(capture.Retired, state) {
		t.Fatalf("active/retired capture = %+v / %+v", capture.Receipts, capture.Retired)
	}
	frames, err := PrepareReceiptSnapshotFrames(capture, policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := frames[len(frames)-1].GetFooter(); got.GetRetiredEpochCount() != 1 ||
		got.GetRetiredReceiptCount() != 1 {
		t.Fatalf("retired capture footer = %+v", got)
	}
	capture.Retired.Epochs[0].State.Receipts[0].Result[0] ^= 1
	again, err := source.Capture(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(again.Retired, capture.Retired) {
		t.Fatal("retired capture aliases the runtime catalog")
	}
}

func TestReceiptWholeStateCaptureRejectsRetiredClockAheadOfActiveStore(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	policy := receiptCapturePolicy(f.epoch)
	active, err := f.coordinator.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	retired, err := newEmptyRetiredReceiptCatalogSlot(
		policy,
		active.ClockHighWaterMillis+1,
	)
	if err != nil {
		t.Fatal(err)
	}
	f.service.receiptRetiredCatalog = retired
	f.coordinator.retired = retired
	source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	if capture, err := source.Capture(t.Context(), policy); err == nil ||
		!reflect.DeepEqual(capture, ReceiptWholeStateCapture{}) {
		t.Fatalf("retired clock rollback returned (%+v, %v)", capture, err)
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
	svc := NewExecutor(cache).
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
	before, err := receiptCaptureSource(t, f).Capture(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	if _, err := f.coordinator.Commit(context.Background(), call); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("definite WAL abort = %v", err)
	}
	after, err := receiptCaptureSource(t, f).Capture(context.Background(), policy)
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
	capture, err := receiptCaptureSource(t, f).Capture(context.Background(), receiptCapturePolicy(f.epoch))
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

func TestReceiptWholeStateSourceRejectsUnknownFormatBeforeCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.db")
	config := baselineRuntimeTestConfig(path)
	runtime, err := OpenReceiptDatabase(t.Context(), config, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	highWater := runtime.receipt.store.Stats().HighWaterMillis
	retired, _ := mustRetiredCatalogSnapshot(t, config.Receipt, highWater, 0x6a)
	mustReplaceRetiredCatalog(
		t,
		runtime.receipt.retired,
		runtime.receipt.policy,
		highWater,
		retired,
	)

	recorder := &replicationSnapshotRecorder{}
	err = replication.Snapshot(t.Context(), &pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat(99),
	}, recorder)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unknown durable receipt format = %v, want InvalidArgument", err)
	}
	if len(recorder.frames) != 0 {
		t.Fatalf("unknown durable receipt format sent %d partial frames", len(recorder.frames))
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
			capture, err := receiptCaptureSource(t, f).Capture(context.Background(), policy)
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
	capture, err := receiptCaptureSource(t, f).Capture(context.Background(), receiptCapturePolicy(f.epoch))
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
	capture, err := receiptCaptureSource(t, f).Capture(context.Background(), receiptCapturePolicy(f.epoch))
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

// receiptCaptureSource binds the same service-owned graph/Store/catalog cut as
// production Snapshot and backup, independently of an Edge Delete adapter.
func receiptCaptureSource(t *testing.T, f receiptEdgeDeleteFixture) *ReceiptWholeStateSource {
	t.Helper()
	source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestReceiptWholeStateCaptureWithTailReservesInsideExactCut(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	source := receiptCaptureSource(t, f)
	call := receiptDeleteCall(t, f.epoch, graphcache.EdgeKey[string]{Tail: "tail", Head: "head"})
	if _, err := f.coordinator.Commit(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	nodeID := f.service.clock.NodeID()
	origin := hex.EncodeToString(nodeID[:])
	var subscription *mutationlog.Subscription
	defer func() {
		if subscription != nil {
			_ = subscription.Close()
		}
	}()
	called := false
	capture, err := source.captureWithTail(t.Context(), receiptCapturePolicy(f.epoch), func(local uint64, origins map[string]uint64, fault <-chan struct{}) error {
		called = true
		if local != 1 || origins[origin] != 1 || len(origins) != 1 || fault != f.service.publicationFaultCh {
			t.Fatalf("tail cut = %d, %v, %v", local, origins, fault)
		}
		if f.service.replicationCutMu.TryRLock() {
			f.service.replicationCutMu.RUnlock()
			t.Fatal("tail opened outside exclusive publication cut")
		}
		select {
		case <-fault:
			t.Fatal("tail reserved a faulted generation")
		default:
		}
		var err error
		subscription, err = f.log.SubscribeStream(local+1, nil)
		// Callback ownership must not let a reservation mutate Snapshot metadata.
		origins[origin] = 99
		return err
	})
	if err != nil || !called || subscription == nil {
		t.Fatalf("capture with tail = %+v, %v; callback=%v", capture, err, called)
	}
	if header := capture.Graph[0].GetHeader(); header.GetCutoffLocalSeq() != 1 || header.GetCutoffSeqPerOrigin()[origin] != 1 {
		t.Fatalf("callback changed captured metadata: %+v", header)
	}
	call.Group = mutationreceipt.GroupID{0x5a}
	call.Items[0].ID, err = mutationreceipt.NewID(f.epoch, time.Now().Add(-time.Second), [24]byte{0xfa})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.coordinator.Commit(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	select {
	case entry := <-subscription.Entries():
		if entry.Seq != 2 {
			t.Fatalf("reserved tail started at local seq %d, want 2", entry.Seq)
		}
		frame, err := subscribeMutationFrame(entry.Op)
		if err != nil || frame.GetMutation().GetSeq() != 2 || frame.GetMutation().GetOp().GetReplicatedReceiptEdgeDelete() == nil {
			t.Fatalf("tail lost exact receipt envelope: %+v, %v", frame, err)
		}
	case <-time.After(time.Second):
		t.Fatal("capture tail missed the next receipt publication")
	}
}

func TestReceiptWholeStateCaptureWithTailFailsClosed(t *testing.T) {
	f := newReceiptEdgeDeleteFixture(t, nil)
	source := receiptCaptureSource(t, f)
	policy := receiptCapturePolicy(f.epoch)
	want := errors.New("tail reservation unavailable")
	called := false
	capture, err := source.captureWithTail(t.Context(), policy, func(uint64, map[string]uint64, <-chan struct{}) error {
		called = true
		return want
	})
	if !called || !errors.Is(err, want) || !reflect.DeepEqual(capture, ReceiptWholeStateCapture{}) {
		t.Fatalf("failed tail returned image: %+v, %v; callback=%v", capture, err, called)
	}
	policy.Epoch = mutationreceipt.Epoch{0x43}
	called = false
	capture, err = source.captureWithTail(t.Context(), policy, func(uint64, map[string]uint64, <-chan struct{}) error {
		called = true
		return nil
	})
	if called || !errors.Is(err, mutationreceipt.ErrInvalidSnapshot) || !reflect.DeepEqual(capture, ReceiptWholeStateCapture{}) {
		t.Fatalf("invalid epoch reserved tail: %+v, %v; callback=%v", capture, err, called)
	}
	finish, err := f.service.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	defer finish(false)
	called = false
	capture, err = source.captureWithTail(t.Context(), receiptCapturePolicy(f.epoch), func(uint64, map[string]uint64, <-chan struct{}) error {
		called = true
		return nil
	})
	if called || connect.CodeOf(err) != connect.CodeFailedPrecondition || !reflect.DeepEqual(capture, ReceiptWholeStateCapture{}) {
		t.Fatalf("faulted generation reserved tail: %+v, %v; callback=%v", capture, err, called)
	}
}
