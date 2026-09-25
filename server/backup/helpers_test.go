package backup

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

type receiptBackupStaticReadDir struct {
	entries  []os.DirEntry
	index    int
	readErr  error
	closeErr error
}

func (d *receiptBackupStaticReadDir) ReadDir(n int) ([]os.DirEntry, error) {
	if d.index == len(d.entries) {
		if d.readErr != nil {
			err := d.readErr
			d.readErr = nil
			return nil, err
		}
		return nil, io.EOF
	}
	end := min(d.index+n, len(d.entries))
	entries := d.entries[d.index:end]
	d.index = end
	return entries, nil
}

func (d *receiptBackupStaticReadDir) Close() error {
	return d.closeErr
}

func producerWALTipWitness(seq uint64) mutationlog.FileWALTipWitness {
	offset := receiptArchiveWALZeroOffset
	if seq != 0 {
		offset += int64(seq)
	}
	return mutationlog.FileWALTipWitness{
		Seq:         seq,
		Offset:      offset,
		SHA256:      sha256.Sum256([]byte("producer WAL prefix")),
		ChainSHA256: sha256.Sum256([]byte("producer WAL chain")),
	}
}

func producerCapture(a wholeStateArchive) service.ReceiptWholeStateCapture {
	retired, err := mutationreceipt.NewRetiredCatalog(mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch:    a.Policy.Epoch,
		MaxEntries:     a.Policy.MaxEntries,
		MaxBytes:       a.Policy.MaxBytes,
		ClockHighWater: a.Receipts.ClockHighWater(),
	})
	if err != nil {
		panic(err)
	}
	retiredSnapshot, err := retired.Snapshot(a.Receipts.ClockHighWater())
	if err != nil {
		panic(err)
	}
	return service.ReceiptWholeStateCapture{
		Graph: a.Graph, Receipts: a.Receipts, Retired: retiredSnapshot,
		Policy: a.Policy, Origins: a.Origins,
	}
}

func producerRetiredSnapshot(
	t testing.TB,
	active mutationreceipt.Config,
	highWaterMillis int64,
	seed byte,
) mutationreceipt.RetiredCatalogSnapshot {
	t.Helper()
	retiredEpoch := mutationreceipt.Epoch{seed}
	if retiredEpoch == active.Epoch {
		retiredEpoch[1] = 1
	}
	policy := mutationreceipt.Config{
		Epoch: retiredEpoch, Retention: 7 * 24 * time.Hour,
		MaxEntries: active.MaxEntries, MaxBytes: active.MaxBytes,
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
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	intent := mutationreceipt.Intent{
		ID: id, Group: mutationreceipt.GroupID{seed, 2}, Count: 1,
		Kind: mutationreceipt.PutVertex, Digest: mutationreceipt.IntentDigest([]byte{seed, 3}),
	}
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
				Epoch: retiredEpoch, Retention: policy.Retention,
				MaxEntries: policy.MaxEntries, MaxBytes: policy.MaxBytes,
			},
			State: state,
		}},
	}
}

func producerEmptyRetiredSnapshot(
	t testing.TB,
	active mutationreceipt.Config,
	highWaterMillis int64,
) mutationreceipt.RetiredCatalogSnapshot {
	t.Helper()
	catalog, err := mutationreceipt.NewRetiredCatalog(mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch:    active.Epoch,
		MaxEntries:     active.MaxEntries,
		MaxBytes:       active.MaxBytes,
		ClockHighWater: time.UnixMilli(highWaterMillis),
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := catalog.Snapshot(time.UnixMilli(highWaterMillis))
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func producerBackupCapture(a wholeStateArchive) service.ReceiptWholeStateBackupCapture {
	var seq uint64
	var nodeID hlc.NodeID
	if len(a.Graph) != 0 && a.Graph[0].GetHeader() != nil {
		seq = a.Graph[0].GetHeader().GetCutoffLocalSeq()
		copy(nodeID[:], a.Graph[0].GetHeader().GetCutoffHlc().GetNodeId())
	}
	return service.ReceiptWholeStateBackupCapture{
		WholeState: producerCapture(a),
		WALTip:     producerWALTipWitness(seq),
		NodeID:     nodeID,
		Generation: [16]byte{0x7f},
	}
}

type receiptBackupSetSource struct {
	capture service.ReceiptWholeStateBackupCapture
	hook    func(context.Context) error
	calls   atomic.Int64
	active  atomic.Int64
	max     atomic.Int64
}

func (s *receiptBackupSetSource) CaptureForBackup(
	ctx context.Context,
	policy mutationreceipt.Config,
) (service.ReceiptWholeStateBackupCapture, error) {
	s.calls.Add(1)
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.max.Load()
		if active <= maximum || s.max.CompareAndSwap(maximum, active) {
			break
		}
	}
	if policy != s.capture.WholeState.Policy {
		return service.ReceiptWholeStateBackupCapture{}, errors.New("test source policy mismatch")
	}
	if s.hook != nil {
		if err := s.hook(ctx); err != nil {
			return service.ReceiptWholeStateBackupCapture{}, err
		}
	}
	return s.capture, nil
}

func newReceiptBackupSetTestBackupper(
	t *testing.T,
	dir, instance string,
	retain int,
	source ReceiptSource,
	policy mutationreceipt.Config,
) *Backupper {
	t.Helper()
	b, err := NewReceipt(
		&fakeService{},
		source,
		policy,
		Config{
			Enabled: true, Dir: dir, Interval: time.Hour,
			Retain: retain, InstanceID: instance,
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
