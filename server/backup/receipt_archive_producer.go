package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

type ReceiptSource interface {
	CaptureForBackup(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error)
}

// receiptBackupSetProduct keeps one backup set's three binary artifacts
// immutable after production. bytes returns fresh all-or-nothing copies for
// the persistence boundary.
type receiptBackupSetProduct struct {
	activeArchive  string
	walCut         string
	retiredCatalog string
	nodeID         hlc.NodeID
	generation     [16]byte
	stats          Stats
}

func (p receiptBackupSetProduct) bytes() (activeArchive, walCut, retiredCatalog []byte) {
	if p.activeArchive == "" || p.walCut == "" || p.retiredCatalog == "" {
		return nil, nil, nil
	}
	return []byte(p.activeArchive), []byte(p.walCut), []byte(p.retiredCatalog)
}

// produceReceiptWholeStateArchive creates all three members of one receipt
// backup set from a single detached publication cut. The WAL-cut member is
// built directly from the captured witness; the producer never reopens the
// appendable WAL path.
func produceReceiptWholeStateArchive(
	ctx context.Context,
	source ReceiptSource,
	policy mutationreceipt.Config,
) (receiptBackupSetProduct, error) {
	var zero receiptBackupSetProduct
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
	if err := validateBackupRetiredSnapshot(
		wholeState.Policy,
		wholeState.Receipts,
		wholeState.Retired,
	); err != nil {
		return zero, fmt.Errorf("backup: validate retired receipt state: %w", err)
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
	walCutRaw, err := encodeReceiptArchiveWALCutWitnesses(archiveRaw, capture.WALTip, capture.WALTip)
	if err != nil {
		return zero, fmt.Errorf("backup: encode captured receipt archive/FileWAL cut: %w", err)
	}
	retiredCatalogRaw, err := encodeRetiredCatalogArchive(wholeState.Policy, wholeState.Retired)
	if err != nil {
		return zero, fmt.Errorf("backup: encode retired receipt catalog: %w", err)
	}
	retired, retiredMetadata, err := decodeRetiredCatalogArchive(retiredCatalogRaw)
	if err != nil {
		return zero, fmt.Errorf("backup: verify retired receipt catalog: %w", err)
	}
	if retiredMetadata.ClockHighWaterMillis != decoded.Receipts.ClockHighWaterMillis {
		return zero, errors.New("backup: active and retired receipt clock high-water differ")
	}
	canonicalRetired, err := encodeRetiredCatalogArchiveWithoutPolicy(retiredMetadata, retired)
	if err != nil {
		return zero, fmt.Errorf("backup: canonicalize retired receipt catalog: %w", err)
	}
	if !bytes.Equal(canonicalRetired, retiredCatalogRaw) {
		return zero, errors.New("backup: retired receipt catalog is not canonical")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	stats := receiptArchiveStats(decoded)
	for _, epoch := range retired.Epochs {
		if len(epoch.State.Receipts) > int(^uint(0)>>1)-stats.Receipts {
			return zero, errors.New("backup: retired receipt count overflows int")
		}
		stats.Receipts += len(epoch.State.Receipts)
	}
	return receiptBackupSetProduct{
		activeArchive:  string(archiveRaw),
		walCut:         string(walCutRaw),
		retiredCatalog: string(retiredCatalogRaw),
		nodeID:         capture.NodeID,
		generation:     capture.Generation,
		stats:          stats,
	}, nil
}

func validateBackupRetiredSnapshot(
	policy mutationreceipt.Config,
	active mutationreceipt.Snapshot,
	retired mutationreceipt.RetiredCatalogSnapshot,
) error {
	if retired.ClockHighWaterMillis != active.ClockHighWaterMillis {
		return errors.New("backup: active and retired receipt clock high-water differ")
	}
	config := mutationreceipt.RetiredCatalogConfig{
		ActiveEpoch:    policy.Epoch,
		MaxEntries:     policy.MaxEntries,
		MaxBytes:       policy.MaxBytes,
		ClockHighWater: time.UnixMilli(active.ClockHighWaterMillis),
	}
	if _, err := mutationreceipt.NewRetiredCatalogFromSnapshot(config, retired); err != nil {
		return fmt.Errorf("backup: invalid retired receipt catalog: %w", err)
	}
	return nil
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
