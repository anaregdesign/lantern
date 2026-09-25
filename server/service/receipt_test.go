package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func newActivatedReceiptService(
	t *testing.T,
	maxEntries int,
) (*ServingRuntime, *LanternService, *LanternReplicationService) {
	t.Helper()
	config := durableRuntimeTestConfig(t.TempDir() + "/receipts.wal")
	config.Receipt.MaxEntries = maxEntries
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyReceiptBackup(primary, replication); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(primary, replication); err != nil {
		t.Fatal(err)
	}
	return runtime, primary, replication
}

func receiptOperationID(
	t *testing.T,
	epoch mutationreceipt.Epoch,
	issued time.Time,
	seed byte,
) mutationreceipt.ID {
	t.Helper()
	id, err := mutationreceipt.NewID(epoch, issued, [24]byte{seed})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func commitReceiptForStatus(
	t *testing.T,
	store *mutationreceipt.Store,
	now time.Time,
	intent mutationreceipt.Intent,
	result byte,
) {
	t.Helper()
	tx, err := store.Begin(now)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	class, _, err := tx.Classify([]mutationreceipt.Intent{intent})
	if err != nil || class != mutationreceipt.Fresh {
		t.Fatalf("Classify = %v, %v", class, err)
	}
	if err := tx.Reserve([][]byte{{result}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
}

func TestReceiptReadSurfaceDisabled(t *testing.T) {
	svc := NewLanternService(nil)
	id := validReceiptOperationIDForTest(t, 0x01)
	capability, err := svc.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || capability.GetEnabled() || capability.GetPolicy() != nil || capability.GetEndpoint() != nil {
		t.Fatalf("disabled capability = (%v, %v)", capability, err)
	}
	if _, err := svc.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: id,
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("singular disabled status = %v, want FailedPrecondition", err)
	}
	if _, err := svc.GetReceiptStatuses(context.Background(), &pb.GetReceiptStatusesRequest{
		OperationIds: [][]byte{id},
	}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("plural disabled status = %v, want FailedPrecondition", err)
	}
}

func TestReceiptReadSurfaceTriStateAndAlignment(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	now := time.Now().Add(-time.Second)
	epoch := runtime.receipt.epoch
	group := mutationreceipt.GroupID{0x31}
	confirmedID := receiptOperationID(t, epoch, now, 0x41)
	unobservedID := receiptOperationID(t, epoch, now, 0x42)
	expiredID := receiptOperationID(t, epoch, now.Add(-2*time.Hour), 0x43)
	confirmedIntent := mutationreceipt.Intent{
		ID: confirmedID, Group: group, Count: 1, Kind: mutationreceipt.DeleteEdge,
		Digest: mutationreceipt.IntentDigest([]byte("confirmed")),
	}
	commitReceiptForStatus(t, runtime.receipt.store, now, confirmedIntent, 0)

	oldEpoch := mutationreceipt.Epoch{0x55}
	oldConfig := mutationreceipt.Config{
		Epoch: oldEpoch, Retention: time.Hour, MaxEntries: 8, MaxBytes: 1 << 20,
		ClockHighWater: now,
	}
	oldStore, err := mutationreceipt.New(oldConfig)
	if err != nil {
		t.Fatal(err)
	}
	retiredID := receiptOperationID(t, oldEpoch, now, 0x44)
	retiredIntent := mutationreceipt.Intent{
		ID: retiredID, Group: mutationreceipt.GroupID{0x32}, Count: 1,
		Kind: mutationreceipt.DeleteEdge, Digest: mutationreceipt.IntentDigest([]byte("retired")),
	}
	commitReceiptForStatus(t, oldStore, now, retiredIntent, 1)
	oldState, err := oldStore.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	emptyCatalog, err := mutationreceipt.NewRetiredCatalog(mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch: epoch, MaxEntries: 8, MaxBytes: 1 << 20, ClockHighWater: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	retiredState, err := emptyCatalog.Snapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	retiredState.Epochs = []mutationreceipt.RetiredEpochSnapshot{{
		Policy: mutationreceipt.RetiredEpochPolicy{
			Epoch: oldEpoch, Retention: oldConfig.Retention,
			MaxEntries: oldConfig.MaxEntries, MaxBytes: oldConfig.MaxBytes,
		},
		State: oldState,
	}}
	runtime.receipt.retired, err = mutationreceipt.NewRetiredCatalogFromSnapshot(
		mutationreceipt.RetiredCatalogConfig{
			ActiveEpoch: epoch, MaxEntries: 8, MaxBytes: 1 << 20, ClockHighWater: now,
		},
		retiredState,
	)
	if err != nil {
		t.Fatal(err)
	}

	capability, err := svc.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	nodeID := runtime.clock.NodeID()
	if err != nil || !capability.GetEnabled() ||
		!bytes.Equal(capability.GetPolicy().GetDeploymentEpoch(), epoch[:]) ||
		!bytes.Equal(capability.GetEndpoint().GetNodeId(), nodeID[:]) ||
		len(capability.GetEndpoint().GetGeneration()) != 16 ||
		capability.GetServerNowUnixMs() == 0 {
		t.Fatalf("capability = %+v, %v", capability, err)
	}

	response, err := svc.GetReceiptStatuses(context.Background(), &pb.GetReceiptStatusesRequest{
		OperationIds: [][]byte{
			unobservedID.Bytes(),
			confirmedID.Bytes(),
			expiredID.Bytes(),
			retiredID.Bytes(),
			confirmedID.Bytes(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	statuses := response.GetStatuses()
	if len(statuses) != 5 ||
		statuses[0].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED ||
		statuses[0].GetReceipt() != nil ||
		statuses[1].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		statuses[1].GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() ||
		statuses[2].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE ||
		statuses[2].GetReceipt() != nil ||
		statuses[3].GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
		!statuses[3].GetReceipt().GetOriginalResult().GetDeleteEdgeExisted() ||
		!bytes.Equal(statuses[4].GetOperationId(), confirmedID.Bytes()) {
		t.Fatalf("aligned tri-state response = %+v", statuses)
	}
	if got := runtime.ReceiptStats().NoLongerProvableLookups; got != 1 {
		t.Fatalf("no-longer-provable status count = %d, want 1", got)
	}
}

func TestReceiptReadSurfaceMalformedAndFaulted(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 8)
	if _, err := svc.GetReceiptStatuses(context.Background(), &pb.GetReceiptStatusesRequest{
		OperationIds: [][]byte{validReceiptOperationIDForTest(t, 1), {1}},
	}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("malformed plural status = %v, want InvalidArgument", err)
	}

	svc.replicationCutMu.Lock()
	svc.markReceiptCommitFaultLocked()
	svc.replicationCutMu.Unlock()
	capability, err := svc.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || capability.GetEnabled() || capability.GetPolicy() != nil ||
		capability.GetEndpoint() != nil {
		t.Fatalf("faulted capability = %+v, %v", capability, err)
	}
	id := receiptOperationID(t, runtime.receipt.epoch, time.Now(), 0x51)
	if _, err := svc.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{
		OperationId: id.Bytes(),
	}); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("faulted status = %v, want Internal", err)
	}
}

func TestReceiptReadSurfaceCancellation(t *testing.T) {
	svc := NewLanternService(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.GetReceiptCapability(ctx, &pb.GetReceiptCapabilityRequest{}); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled capability = %v, want Canceled", err)
	}
	if _, err := svc.GetReceiptStatuses(ctx, &pb.GetReceiptStatusesRequest{}); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled status = %v, want Canceled", err)
	}
}
