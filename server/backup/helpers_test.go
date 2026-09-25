package backup

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

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
	return service.ReceiptWholeStateCapture{
		Graph: a.Graph, Receipts: a.Receipts, Policy: a.Policy, Origins: a.Origins,
	}
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
