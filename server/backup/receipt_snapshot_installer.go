package backup

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

// ReceiptSnapshotInstaller collects and validates a complete RECEIPT_V1 cut
// before publishing it through the certified durable service primitive.
type ReceiptSnapshotInstaller struct {
	collector *ReceiptSnapshotCollector
	target    *service.LanternService
	logger    *slog.Logger
	gate      chan struct{}
}

func NewReceiptSnapshotInstaller(
	collector *ReceiptSnapshotCollector,
	target *service.LanternService,
	logger *slog.Logger,
) (*ReceiptSnapshotInstaller, error) {
	if collector == nil || target == nil {
		return nil, errors.New("backup: receipt Snapshot installer requires a collector and target")
	}
	if logger == nil {
		logger = slog.Default()
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &ReceiptSnapshotInstaller{
		collector: collector,
		target:    target,
		logger:    logger,
		gate:      gate,
	}, nil
}

func (*ReceiptSnapshotInstaller) RequiredFormat() pb.SnapshotFormat {
	return pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1
}

func (*ReceiptSnapshotInstaller) CompatibleFormat(format pb.SnapshotFormat) bool {
	return format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1
}

func (i *ReceiptSnapshotInstaller) Install(
	ctx context.Context,
	stream replication.SnapshotStream,
) (result replication.SnapshotInstallResult, installErr error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if i == nil || i.collector == nil || i.target == nil || stream == nil {
		return result, errors.New("backup: receipt Snapshot installer is incomplete")
	}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-i.gate:
	}
	defer func() {
		i.gate <- struct{}{}
	}()

	candidate, err := i.collector.Collect(ctx, stream)
	if err != nil {
		return result, fmt.Errorf("backup: collect RECEIPT_V1 Snapshot: %w", err)
	}
	committed := false
	defer func() {
		closeErr := candidate.Close()
		if closeErr == nil {
			return
		}
		if committed {
			i.logger.Error(
				"receipt Snapshot committed but detached candidate cleanup failed",
				"error", closeErr,
			)
			return
		}
		installErr = errors.Join(installErr, fmt.Errorf("backup: discard receipt Snapshot candidate: %w", closeErr))
	}()

	capture, err := candidate.installCapture(ctx)
	if err != nil {
		return result, fmt.Errorf("backup: decode validated RECEIPT_V1 candidate: %w", err)
	}
	if err := i.target.InstallActiveReceiptBaselineV1(ctx, capture); err != nil {
		return result, fmt.Errorf("backup: install RECEIPT_V1 baseline: %w", err)
	}
	committed = true

	metadata := candidate.Metadata()
	footer := capture.Graph[len(capture.Graph)-1].GetFooter()
	result.Header = metadata.Header
	result.Graph = replication.SnapshotGraphCounts{
		Vertices:             footer.GetVertexCount(),
		Edges:                footer.GetEdgeCount(),
		VertexCausalBarriers: footer.GetVertexCausalBarrierCount(),
		EdgeCausalBarriers:   footer.GetEdgeCausalBarrierCount(),
		VertexTombstones:     footer.GetVertexTombstoneCount(),
		EdgeTombstones:       footer.GetEdgeTombstoneCount(),
	}
	return result, nil
}

func (c *ReceiptSnapshotCandidate) installCapture(ctx context.Context) (service.ReceiptWholeStateCapture, error) {
	if c == nil {
		return service.ReceiptWholeStateCapture{}, errors.New("backup: receipt Snapshot candidate is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.archive == nil {
		return service.ReceiptWholeStateCapture{}, errors.New("backup: receipt Snapshot candidate is closed")
	}
	if err := ctx.Err(); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	if _, err := c.archive.Seek(0, io.SeekStart); err != nil {
		return service.ReceiptWholeStateCapture{}, fmt.Errorf("backup: rewind receipt Snapshot candidate: %w", err)
	}
	hash := sha256.New()
	reader := &receiptSnapshotContextReader{ctx: ctx, reader: c.archive}
	archive, err := decodeWholeStateArchive(io.TeeReader(reader, hash))
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	offset, err := c.archive.Seek(0, io.SeekCurrent)
	if err != nil {
		return service.ReceiptWholeStateCapture{}, fmt.Errorf("backup: inspect receipt Snapshot candidate offset: %w", err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	if offset < 0 || uint64(offset) != c.metadata.ArchiveBytes || digest != c.metadata.ArchiveSHA256 {
		return service.ReceiptWholeStateCapture{}, errors.New("backup: receipt Snapshot candidate archive changed after validation")
	}
	if err := ctx.Err(); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	return service.ReceiptWholeStateCapture{
		Graph:    archive.Graph,
		Receipts: archive.Receipts,
		Policy:   archive.Policy,
		Origins:  archive.Origins,
	}, nil
}

type receiptSnapshotContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *receiptSnapshotContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
