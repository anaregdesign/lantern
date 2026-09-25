package service

import (
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

func assertReceiptBackupWitnessMatchesCut(
	t *testing.T,
	witness mutationlog.FileWALTipWitness,
	cut mutationlog.FileWALCut,
) {
	t.Helper()
	if witness.Seq != cut.Seq ||
		witness.Offset != cut.Offset ||
		witness.SHA256 != cut.SHA256 ||
		witness.ChainSHA256 != cut.ChainSHA256 {
		t.Fatalf("WAL witness = %#v, want inspected cut %#v", witness, cut)
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

func TestReceiptWholeStateBackupCaptureTracksFreshAndResumedWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.log")
	config := durableRuntimeTestConfig(path)
	policy := config.Receipt

	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
	if err != nil {
		t.Fatal(err)
	}

	zero, err := source.CaptureForBackup(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := zero.WholeState.Graph[0].GetHeader().GetCutoffLocalSeq(); got != 0 {
		t.Fatalf("fresh graph cutoff = %d, want 0", got)
	}
	if zero.WALTip.Seq != 0 {
		t.Fatalf("fresh WAL witness seq = %d, want 0", zero.WALTip.Seq)
	}

	call := receiptDeleteCall(t, config.Receipt.Epoch, graphcache.EdgeKey[string]{Tail: "fresh-tail", Head: "fresh-head"})
	if _, err := primary.receiptEdgeDeleteCoordinator.Commit(t.Context(), call); err != nil {
		t.Fatal(err)
	}
	fresh, err := source.CaptureForBackup(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := fresh.WholeState.Graph[0].GetHeader().GetCutoffLocalSeq(); got != 1 {
		t.Fatalf("fresh appended graph cutoff = %d, want 1", got)
	}
	if fresh.WALTip.Seq != 1 {
		t.Fatalf("fresh appended WAL witness seq = %d, want 1", fresh.WALTip.Seq)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	freshCuts, err := mutationlog.InspectFileWALCuts(
		path,
		[]uint64{0, 1},
		decodeReceiptWALUnion,
		validateReceiptWALUnionEntry,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptBackupWitnessMatchesCut(t, zero.WALTip, freshCuts[0])
	assertReceiptBackupWitnessMatchesCut(t, fresh.WALTip, freshCuts[1])

	config.Now = time.Now().Add(time.Second)
	config.Receipt.ClockHighWater = config.Now
	policy = config.Receipt
	resumedRuntime, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumedRuntime.Close() })
	resumedPrimary := resumedRuntime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	resumedSource, err := NewReceiptWholeStateSource(resumedPrimary, resumedRuntime.receipt.store)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := resumedSource.CaptureForBackup(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := resumed.WholeState.Graph[0].GetHeader().GetCutoffLocalSeq(); got != 1 {
		t.Fatalf("resumed graph cutoff = %d, want 1", got)
	}
	assertReceiptBackupWitnessMatchesCut(t, resumed.WALTip, freshCuts[1])
}

func TestReceiptWholeStateBackupCaptureFailsClosed(t *testing.T) {
	t.Run("graph-only runtime", func(t *testing.T) {
		f := newReceiptEdgeDeleteFixture(t, nil)
		source, err := NewReceiptWholeStateSource(f.service, f.coordinator.store)
		if err != nil {
			t.Fatal(err)
		}
		got, err := source.CaptureForBackup(t.Context(), receiptCapturePolicy(f.epoch))
		if err == nil {
			t.Fatal("CaptureForBackup succeeded for graph-only runtime")
		}
		if !reflect.DeepEqual(got, ReceiptWholeStateBackupCapture{}) {
			t.Fatalf("CaptureForBackup returned partial graph-only result: %#v", got)
		}
	})

	t.Run("closed durable runtime", func(t *testing.T) {
		config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipt.log"))
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if err != nil {
			t.Fatal(err)
		}
		primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
		source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		got, err := source.CaptureForBackup(t.Context(), config.Receipt)
		if err == nil {
			t.Fatal("CaptureForBackup succeeded for closed runtime")
		}
		if !reflect.DeepEqual(got, ReceiptWholeStateBackupCapture{}) {
			t.Fatalf("CaptureForBackup returned partial closed-runtime result: %#v", got)
		}
	})

	t.Run("foreign runtime owner", func(t *testing.T) {
		config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipt.log"))
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
		source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
		if err != nil {
			t.Fatal(err)
		}
		originalOwner := runtime.receipt.owner
		runtime.receipt.owner = &receiptWALOwnedCandidate{}
		t.Cleanup(func() { runtime.receipt.owner = originalOwner })

		got, err := source.CaptureForBackup(t.Context(), config.Receipt)
		if err == nil {
			t.Fatal("CaptureForBackup succeeded for foreign runtime owner")
		}
		if !reflect.DeepEqual(got, ReceiptWholeStateBackupCapture{}) {
			t.Fatalf("CaptureForBackup returned partial foreign-owner result: %#v", got)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipt.log"))
		runtime, err := CreateDurableReceiptWALServingRuntime(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
		source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got, err := source.CaptureForBackup(ctx, config.Receipt)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CaptureForBackup error = %v, want context.Canceled", err)
		}
		if !reflect.DeepEqual(got, ReceiptWholeStateBackupCapture{}) {
			t.Fatalf("CaptureForBackup returned partial canceled result: %#v", got)
		}
	})
}

func TestReceiptWholeStateBackupCaptureRejectsServicePublicationFaults(t *testing.T) {
	tests := []struct {
		name  string
		fault func(*LanternService)
	}{
		{
			name: "publication fault",
			fault: func(primary *LanternService) {
				primary.publicationFaultCount++
			},
		},
		{
			name: "receipt commit fault",
			fault: func(primary *LanternService) {
				primary.receiptCommitFaulted = true
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipt.log"))
			runtime, err := CreateDurableReceiptWALServingRuntime(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
			source, err := NewReceiptWholeStateSource(primary, runtime.receipt.store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.CaptureForBackup(t.Context(), config.Receipt); err != nil {
				t.Fatalf("healthy CaptureForBackup failed: %v", err)
			}

			tt.fault(primary)
			got, err := source.CaptureForBackup(t.Context(), config.Receipt)
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("faulted CaptureForBackup error = %v, want FailedPrecondition", err)
			}
			if !reflect.DeepEqual(got, ReceiptWholeStateBackupCapture{}) {
				t.Fatalf("faulted CaptureForBackup returned partial result: %#v", got)
			}
		})
	}
}

func TestReceiptWholeStateBackupCaptureCannotSplitCommittedView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.log")
	policy := receiptCapturePolicy(mutationreceipt.Epoch{0x74})
	localOrigin := hlc.NodeID{0x76}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := mutationlog.AcquireFileWALLease(path)
	if err != nil {
		t.Fatal(err)
	}
	blockNext := make(chan struct{}, 1)
	encoderEntered := make(chan struct{})
	releaseEncoder := make(chan struct{})
	encoder := func(op mutationlog.MutationOp) ([]byte, error) {
		select {
		case <-blockNext:
			close(encoderEntered)
			<-releaseEncoder
		default:
		}
		return encodeReceiptWALUnion(op)
	}
	wal, err := mutationlog.CreateFileWAL(lease.Path(), encoder)
	if err != nil {
		_ = lease.Close()
		t.Fatal(err)
	}
	tip, err := mutationlog.CreateFileWALTipJournal(
		lease.Path(),
		receiptWALTipBinding(policy.Epoch, store.PolicyFingerprint()),
	)
	if err != nil {
		_ = wal.Close()
		_ = lease.Close()
		t.Fatal(err)
	}
	if err := tip.VerifyAndCatchUp(lease.Path(), decodeReceiptWALUnion, validateReceiptWALUnionEntry); err != nil {
		_ = tip.Close()
		_ = wal.Close()
		_ = lease.Close()
		t.Fatal(err)
	}
	if err := wal.BindTipJournal(tip); err != nil {
		_ = tip.Close()
		_ = wal.Close()
		_ = lease.Close()
		t.Fatal(err)
	}

	log := mutationlog.New(mutationlog.Options{
		Capacity:         32,
		SubscriberBuffer: 32,
		WAL:              wal,
	})
	t.Cleanup(func() {
		select {
		case <-releaseEncoder:
		default:
			close(releaseEncoder)
		}
		_ = log.Close()
		_ = tip.Close()
		_ = wal.Close()
		_ = lease.Close()
	})
	provenance, err := log.FileWALTipProvenance(lease.Path())
	if err != nil {
		t.Fatal(err)
	}

	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	clock := hlc.New(localOrigin, hlc.Options{})
	primary := NewLanternService(cache).
		WithReplication(log, clock, nil).
		WithTombstoneTTL(time.Hour)
	owner := &receiptWALOwnedCandidate{
		state: &receiptWALRecoveryCandidate{
			graph:    cache,
			receipts: store,
			origins:  primary.origins,
			log:      log,
		},
		tip:           tip,
		lease:         lease,
		walProvenance: provenance,
	}
	runtime := &ServingRuntime{
		graph:   cache,
		log:     log,
		clock:   clock,
		origins: primary.origins,
		receipt: &receiptServingRuntime{
			store: store,
			owner: owner,
		},
		owner: owner,
	}
	primary.runtime = runtime
	source, err := NewReceiptWholeStateSource(primary, store)
	if err != nil {
		t.Fatal(err)
	}

	tail, head := "atomic-tail", "atomic-head"
	if _, err := primary.PutEdge(t.Context(), &pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: tail, Head: head, Weight: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := log.LastSeq(); !ok || got != 1 {
		t.Fatalf("precondition log seq = %d, present = %t, want 1, true", got, ok)
	}

	deleteCall := receiptDeleteCall(t, policy.Epoch, graphcache.EdgeKey[string]{Tail: tail, Head: head})
	blockNext <- struct{}{}
	type commitResult struct {
		response *pb.DeleteEdgesResponse
		err      error
	}
	commitDone := make(chan commitResult, 1)
	go func() {
		response, err := primary.receiptEdgeDeleteCoordinator.Commit(t.Context(), deleteCall)
		commitDone <- commitResult{response: response, err: err}
	}()
	waitReceiptTest(t, "blocked WAL encoder", encoderEntered)

	captureStarted := make(chan struct{})
	type captureResult struct {
		capture ReceiptWholeStateBackupCapture
		err     error
	}
	captureDone := make(chan captureResult, 1)
	go func() {
		close(captureStarted)
		capture, err := source.CaptureForBackup(t.Context(), policy)
		captureDone <- captureResult{capture: capture, err: err}
	}()
	waitReceiptTest(t, "backup capture start", captureStarted)
	select {
	case early := <-captureDone:
		t.Fatalf("backup capture escaped staged commit: %#v, err=%v", early.capture, early.err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseEncoder)
	committed := waitReceiptTest(t, "receipt commit", commitDone)
	if committed.err != nil {
		t.Fatal(committed.err)
	}
	if committed.response == nil ||
		len(committed.response.GetExisted()) != 1 ||
		!committed.response.GetExisted()[0] {
		t.Fatalf("receipt commit response = %#v, want one existing edge", committed.response)
	}
	captured := waitReceiptTest(t, "backup capture", captureDone)
	if captured.err != nil {
		t.Fatal(captured.err)
	}

	header := captured.capture.WholeState.Graph[0].GetHeader()
	if header == nil {
		t.Fatal("backup capture is missing graph header")
	}
	if got := header.GetCutoffLocalSeq(); got != 2 {
		t.Fatalf("graph cutoff = %d, want 2", got)
	}
	if captured.capture.WALTip.Seq != 2 {
		t.Fatalf("WAL witness seq = %d, want 2", captured.capture.WALTip.Seq)
	}
	if len(captured.capture.WholeState.Receipts.Receipts) != 1 {
		t.Fatalf("receipt count = %d, want 1", len(captured.capture.WholeState.Receipts.Receipts))
	}
	if header.GetCutoffHlc() == nil ||
		header.GetCutoffHlc().GetWallNs()/int64(time.Millisecond) <
			captured.capture.WholeState.Receipts.ClockHighWaterMillis ||
		captured.capture.WholeState.Policy.ClockHighWater.UnixMilli() !=
			captured.capture.WholeState.Receipts.ClockHighWaterMillis {
		t.Fatalf("captured HLC/receipt high-water mismatch: header=%+v capture=%+v", header, captured.capture.WholeState)
	}
	if receiptCaptureGraphEdge(captured.capture.WholeState, tail, head) {
		t.Fatal("captured graph still contains the deleted edge")
	}
	if !receiptCaptureEdgeTombstone(captured.capture.WholeState, tail, head) {
		t.Fatal("captured graph is missing the deleted edge tombstone")
	}
	if len(captured.capture.WholeState.Origins) != 1 ||
		captured.capture.WholeState.Origins[0].Origin != localOrigin ||
		captured.capture.WholeState.Origins[0].LastSeq != 2 {
		t.Fatalf("captured origins = %#v, want local origin at seq 2", captured.capture.WholeState.Origins)
	}

	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tip.Close(); err != nil {
		t.Fatal(err)
	}
	cut, err := mutationlog.InspectFileWALCut(path, 2, decodeReceiptWALUnion, validateReceiptWALUnionEntry)
	if err != nil {
		t.Fatal(err)
	}
	assertReceiptBackupWitnessMatchesCut(t, captured.capture.WALTip, cut)
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
