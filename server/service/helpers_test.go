package service

import (
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func validReceiptOperationIDForTest(t *testing.T, seed byte) []byte {
	t.Helper()
	id, err := mutationreceipt.NewID(
		mutationreceipt.Epoch{0x71},
		time.Unix(1_700_000_000, 0),
		[24]byte{seed},
	)
	if err != nil {
		t.Fatal(err)
	}
	return id.Bytes()
}

func mustMutationLogEntry(t *testing.T, log *mutationlog.Log, seq uint64) mutationlog.Entry {
	t.Helper()
	entries, cancel, err := log.Subscribe(seq)
	if err != nil {
		t.Fatalf("Subscribe(%d): %v", seq, err)
	}
	defer func() { _ = cancel() }()
	select {
	case entry := <-entries:
		if entry.Seq != seq {
			t.Fatalf("log entry seq = %d, want %d", entry.Seq, seq)
		}
		return entry
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out reading log entry %d", seq)
		return mutationlog.Entry{}
	}
}

func mustGraphMutation(t *testing.T, op mutationlog.MutationOp) *pb.Mutation {
	t.Helper()
	mutation, ok := graphMutationFromLog(op)
	if !ok {
		t.Fatalf("graph mutation unavailable from %T", op)
	}
	return mutation
}

func receiptVertexWALItemsWire(arm protowire.Number, count int, item []byte) []byte {
	call := make([]byte, 0, count*(len(item)+2))
	for range count {
		call = protowire.AppendTag(call, protowire.Number(receiptVertexItemsField), protowire.BytesType)
		call = protowire.AppendBytes(call, item)
	}
	op := protowire.AppendTag(nil, arm, protowire.BytesType)
	op = protowire.AppendBytes(op, call)
	mutation := protowire.AppendTag(nil, 4, protowire.BytesType)
	return protowire.AppendBytes(mutation, op)
}

func receiptVertexWALMalformedItemWire(arm protowire.Number) []byte {
	call := protowire.AppendTag(nil, protowire.Number(receiptVertexItemsField), protowire.BytesType)
	call = append(call, 0x80)
	op := protowire.AppendTag(nil, arm, protowire.BytesType)
	op = protowire.AppendBytes(op, call)
	mutation := protowire.AppendTag(nil, 4, protowire.BytesType)
	return protowire.AppendBytes(mutation, op)
}

func receiptVertexDeleteRecoveryEnvelope(
	t *testing.T,
	config mutationreceipt.Config,
	originByte byte,
	originSequence uint64,
	wallNS int64,
	expiration time.Time,
	keys []string,
	acceptedIndexes ...int,
) *vertexDeleteReceiptEnvelope {
	t.Helper()
	issued := time.Unix(0, wallNS).UTC().Truncate(time.Millisecond)
	var origin hlc.NodeID
	for i := range origin {
		origin[i] = originByte
	}
	stamp := hlc.Timestamp{WallNs: issued.UnixNano(), NodeID: origin}
	store, err := mutationreceipt.New(config)
	if err != nil {
		t.Fatal(err)
	}
	group := mutationreceipt.GroupID{originByte}
	receipts := make([]mutationreceipt.Receipt, len(keys))
	for i, key := range keys {
		id, err := mutationreceipt.NewID(
			config.Epoch,
			issued,
			[24]byte{originByte, byte(i + 1)},
		)
		if err != nil {
			t.Fatal(err)
		}
		receipts[i] = mutationreceipt.Receipt{
			Intent: mutationreceipt.Intent{
				ID: id, Group: group, Index: uint32(i), Count: uint32(len(keys)),
				Kind: mutationreceipt.DeleteVertex, Digest: vertexDeleteDigest(key),
			},
			Result: []byte{0}, DeadlineMillis: issued.Add(config.Retention).UnixMilli(),
		}
	}
	accepted := make([]graphcache.IndexedVertexDelete[string], len(acceptedIndexes))
	for i, index := range acceptedIndexes {
		if index < 0 || index >= len(keys) {
			t.Fatalf("accepted index %d is outside %d recovery keys", index, len(keys))
		}
		accepted[i] = graphcache.IndexedVertexDelete[string]{
			Index: index,
			Key:   keys[index],
		}
	}
	envelope := &vertexDeleteReceiptEnvelope{
		Origin: origin, OriginSeq: originSequence, HLC: stamp,
		Epoch: config.Epoch, PolicyFingerprint: store.PolicyFingerprint(),
		TombstoneExpiration: expiration,
		OriginalKeys:        append([]string(nil), keys...),
		Accepted:            accepted,
		Receipts:            receipts,
	}
	envelope.Mutation = receiptVertexDeleteGraphMutation(envelope)
	if err := validateReceiptVertexDeleteWALEntry(mutationlog.Entry{
		Seq: 1, HLC: stamp, Op: envelope,
	}); err != nil {
		t.Fatalf("invalid Vertex Delete recovery fixture: %v", err)
	}
	return envelope
}

func insertReceiptSnapshotFrames(
	frames []*pb.SnapshotResponse,
	index int,
	additions ...*pb.SnapshotResponse,
) []*pb.SnapshotResponse {
	tail := append([]*pb.SnapshotResponse(nil), frames[index:]...)
	frames = append(frames[:index], additions...)
	return append(frames, tail...)
}

func mustRetiredCatalogSnapshot(
	t testing.TB,
	active mutationreceipt.Config,
	highWaterMillis int64,
	seed byte,
) (mutationreceipt.RetiredCatalogSnapshot, mutationreceipt.ID) {
	t.Helper()
	retiredEpoch := mutationreceipt.Epoch{seed}
	if retiredEpoch == active.Epoch {
		retiredEpoch[1] = 1
	}
	policy := mutationreceipt.Config{
		Epoch:          retiredEpoch,
		Retention:      2 * time.Hour,
		MaxEntries:     active.MaxEntries,
		MaxBytes:       active.MaxBytes,
		ClockHighWater: time.UnixMilli(highWaterMillis),
	}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	issued := time.UnixMilli(highWaterMillis)
	id, err := mutationreceipt.NewID(retiredEpoch, issued, [24]byte{seed, 1})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{
		ID:     id,
		Group:  mutationreceipt.GroupID{seed, 2},
		Count:  1,
		Kind:   mutationreceipt.PutVertex,
		Digest: mutationreceipt.IntentDigest([]byte{seed, 3}),
	}
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	classification, _, err := tx.Classify([]mutationreceipt.Intent{intent})
	if err != nil || classification != mutationreceipt.Fresh {
		t.Fatalf("classify retired receipt = %v, %v", classification, err)
	}
	if err := tx.Reserve([][]byte{{seed, 4}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return mutationreceipt.RetiredCatalogSnapshot{
		Version:              1,
		ClockHighWaterMillis: highWaterMillis,
		Epochs: []mutationreceipt.RetiredEpochSnapshot{{
			Policy: mutationreceipt.RetiredEpochPolicy{
				Epoch:      retiredEpoch,
				Retention:  policy.Retention,
				MaxEntries: policy.MaxEntries,
				MaxBytes:   policy.MaxBytes,
			},
			State: state,
		}},
	}, id
}

func mustReplaceRetiredCatalog(
	t testing.TB,
	slot *retiredReceiptCatalogSlot,
	policy mutationreceipt.Config,
	highWaterMillis int64,
	state mutationreceipt.RetiredCatalogSnapshot,
) {
	t.Helper()
	_, revision, err := slot.snapshot(policy, highWaterMillis)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := slot.beginReplace(policy, revision, highWaterMillis, state)
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
}
