package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

type ReceiptSource interface {
	CaptureForBackup(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error)
}

// receiptActiveEpochArchiveProduct keeps one backup set's paired binary
// artifacts immutable after production. It covers only the captured active
// epoch; a later set version may add a retired-epoch catalog without
// resampling the live WAL. bytes returns fresh all-or-nothing copies for the
// persistence boundary.
type receiptActiveEpochArchiveProduct struct {
	archive    string
	manifest   string
	nodeID     hlc.NodeID
	generation [16]byte
	stats      Stats
}

func (p receiptActiveEpochArchiveProduct) bytes() (archive, manifest []byte) {
	if p.archive == "" || p.manifest == "" {
		return nil, nil
	}
	return []byte(p.archive), []byte(p.manifest)
}

// produceReceiptWholeStateArchive creates the active-epoch members of one
// receipt backup set. Its sole source call returns a detached publication cut
// and the exact live FileWAL tip captured with it. The producer encodes only
// that detached state and builds the WAL-cut member directly from that
// witness; it never reopens the appendable WAL path. The complete canonical
// archive and WAL-cut member are decoded before an immutable pair is returned.
func produceReceiptWholeStateArchive(
	ctx context.Context,
	source ReceiptSource,
	policy mutationreceipt.Config,
) (receiptActiveEpochArchiveProduct, error) {
	var zero receiptActiveEpochArchiveProduct
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if source == nil {
		return zero, errors.New("backup: receipt whole-state source is nil")
	}
	capture, err := source.CaptureForBackup(ctx, policy)
	if err != nil {
		return zero, fmt.Errorf("backup: capture receipt whole-state with WAL witness: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	wholeState := capture.WholeState
	if len(wholeState.Retired.Epochs) != 0 {
		return zero, errors.New("backup: active-only receipt archive cannot represent retired receipt evidence")
	}
	if wholeState.Policy.Epoch != policy.Epoch || wholeState.Policy.Retention != policy.Retention ||
		wholeState.Policy.MaxEntries != policy.MaxEntries || wholeState.Policy.MaxBytes != policy.MaxBytes ||
		(!policy.ClockHighWater.IsZero() && policy.ClockHighWater.UnixMilli() > wholeState.Policy.ClockHighWater.UnixMilli()) {
		return zero, wholeStateArchiveError("capture policy differs from requested policy")
	}
	archive := wholeStateArchive{
		Graph: wholeState.Graph, Receipts: wholeState.Receipts,
		Policy: wholeState.Policy, Origins: wholeState.Origins,
	}
	var out bytes.Buffer
	if err := encodeWholeStateArchive(&out, archive); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	archiveRaw := out.Bytes()
	if len(archiveRaw) == 0 || len(archiveRaw) > wholeStateArchiveMaxBytes {
		return zero, wholeStateArchiveError("invalid archive size")
	}
	decoded, err := decodeWholeStateArchive(bytes.NewReader(archiveRaw))
	if err != nil {
		return zero, fmt.Errorf("backup: verify receipt whole-state archive: %w", err)
	}
	if err := validateReceiptArchiveCapturedWitness(
		decoded,
		capture.WALTip,
		capture.NodeID,
		capture.Generation,
	); err != nil {
		return zero, err
	}
	manifestRaw, err := encodeReceiptArchiveWALCutWitnesses(archiveRaw, capture.WALTip, capture.WALTip)
	if err != nil {
		return zero, fmt.Errorf("backup: encode captured receipt archive/FileWAL cut: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return receiptActiveEpochArchiveProduct{
		archive:    string(archiveRaw),
		manifest:   string(manifestRaw),
		nodeID:     capture.NodeID,
		generation: capture.Generation,
		stats:      receiptArchiveStats(decoded),
	}, nil
}

func receiptArchiveStats(archive wholeStateArchive) Stats {
	stats := Stats{
		Receipts: len(archive.Receipts.Receipts),
		Origins:  len(archive.Origins),
	}
	for _, frame := range archive.Graph {
		switch {
		case frame.GetVertex() != nil:
			stats.Vertices++
		case frame.GetEdge() != nil:
			stats.Edges++
		}
	}
	return stats
}

func validateReceiptArchiveCapturedWitness(
	archive wholeStateArchive,
	witness mutationlog.FileWALTipWitness,
	nodeID hlc.NodeID,
	generation [16]byte,
) error {
	if err := validateReceiptArchiveWALWitness(
		"captured tip",
		witness.Seq,
		witness.Offset,
		witness.SHA256,
		witness.ChainSHA256,
	); err != nil {
		return err
	}
	if nodeID == (hlc.NodeID{}) || generation == ([16]byte{}) {
		return errors.New("backup: receipt archive runtime identity is zero")
	}
	if len(archive.Graph) == 0 || archive.Graph[0].GetHeader() == nil {
		return wholeStateArchiveError("archive graph header is missing")
	}
	header := archive.Graph[0].GetHeader()
	if header.GetCutoffLocalSeq() != witness.Seq {
		return fmt.Errorf("%w: archive local sequence differs from captured FileWAL witness", errReceiptArchiveWALCut)
	}
	cutoffHLC := header.GetCutoffHlc()
	if cutoffHLC == nil || len(cutoffHLC.GetNodeId()) != len(nodeID) ||
		!bytes.Equal(cutoffHLC.GetNodeId(), nodeID[:]) {
		return errors.New("backup: receipt archive cutoff HLC differs from runtime NodeID")
	}
	return nil
}
