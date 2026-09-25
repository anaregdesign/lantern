package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

type receiptWholeStateBackupCapturer interface {
	CaptureForBackup(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error)
}

// receiptActiveEpochArchiveProduct keeps one future backup-set member's paired
// binary artifacts immutable after production. It covers only the captured
// active epoch; a later versioned backup-set layer may add a retired-epoch
// catalog alongside it without resampling the live WAL. bytes returns fresh
// all-or-nothing copies for that later persistence boundary.
type receiptActiveEpochArchiveProduct struct {
	archive  string
	manifest string
}

func (p receiptActiveEpochArchiveProduct) bytes() (archive, manifest []byte) {
	if p.archive == "" || p.manifest == "" {
		return nil, nil
	}
	return []byte(p.archive), []byte(p.manifest)
}

// produceReceiptWholeStateArchive is an unwired in-process prerequisite for one
// active-epoch member of a receipt-aware backup set. Its sole source call
// returns a detached publication cut and the exact live FileWAL tip captured
// with it. The producer encodes only that detached state and builds the
// manifest directly from that witness; it never reopens the appendable WAL
// path. The complete canonical archive and manifest are decoded before an
// immutable pair is returned.
func produceReceiptWholeStateArchive(
	ctx context.Context,
	source receiptWholeStateBackupCapturer,
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
	if err := validateReceiptArchiveCapturedWitness(decoded, capture.WALTip); err != nil {
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
		archive:  string(archiveRaw),
		manifest: string(manifestRaw),
	}, nil
}

func validateReceiptArchiveCapturedWitness(
	archive wholeStateArchive,
	witness mutationlog.FileWALTipWitness,
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
	if len(archive.Graph) == 0 || archive.Graph[0].GetHeader() == nil {
		return wholeStateArchiveError("archive graph header is missing")
	}
	if archive.Graph[0].GetHeader().GetCutoffLocalSeq() != witness.Seq {
		return fmt.Errorf("%w: archive local sequence differs from captured FileWAL witness", errReceiptArchiveWALCut)
	}
	return nil
}
