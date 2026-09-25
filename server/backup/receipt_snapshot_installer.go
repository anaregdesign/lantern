package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

// ReceiptSnapshotInstallTarget is the narrow service seam that atomically
// publishes one fully staged RECEIPT candidate.
type ReceiptSnapshotInstallTarget interface {
	InstallReceiptBaseline(context.Context, service.ReceiptWholeStateCapture) error
}

// ReceiptSnapshotInstaller collects and validates a complete RECEIPT cut
// before publishing it through the certified durable service primitive.
type ReceiptSnapshotInstaller struct {
	collector *ReceiptSnapshotCollector
	target    ReceiptSnapshotInstallTarget
	logger    *slog.Logger
	gate      chan struct{}
}

func NewReceiptSnapshotInstaller(
	collector *ReceiptSnapshotCollector,
	target ReceiptSnapshotInstallTarget,
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
	return pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT
}

func (*ReceiptSnapshotInstaller) CompatibleFormat(format pb.SnapshotFormat) bool {
	return format == pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT
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
		return result, fmt.Errorf("backup: collect RECEIPT Snapshot: %w", err)
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
		return result, fmt.Errorf("backup: decode validated RECEIPT candidate: %w", err)
	}
	if err := i.target.InstallReceiptBaseline(ctx, capture); err != nil {
		return result, fmt.Errorf("backup: install RECEIPT baseline: %w", err)
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
	if c.closed || c.spool == nil {
		return service.ReceiptWholeStateCapture{}, errors.New("backup: receipt Snapshot candidate is closed")
	}
	if err := ctx.Err(); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	digest, size, err := digestReceiptSnapshotSpool(c.spool)
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	if size != c.metadata.SpoolBytes || digest != c.metadata.SpoolSHA256 {
		return service.ReceiptWholeStateCapture{}, errors.New("backup: receipt Snapshot candidate spool changed after validation")
	}
	if _, err := c.spool.Seek(0, 0); err != nil {
		return service.ReceiptWholeStateCapture{}, fmt.Errorf("backup: rewind receipt Snapshot candidate: %w", err)
	}
	frames, err := readReceiptSnapshotSpool(ctx, c.spool, c.frameCount, c.limits)
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	capture, err := service.DecodeReceiptSnapshotFrames(
		frames,
		c.expectedPolicy,
		c.retiredConfig,
	)
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	if c.stage == nil || c.stage.receiptWholeStateStage == nil ||
		c.stage.graph == nil || c.stage.receipts == nil || c.stage.retired == nil {
		return service.ReceiptWholeStateCapture{}, errors.New("backup: receipt Snapshot candidate stage changed after validation")
	}
	active, err := c.stage.receipts.Snapshot()
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	retired, err := c.stage.retired.Snapshot(capture.Receipts.ClockHighWater())
	if err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	if !reflect.DeepEqual(active, capture.Receipts) ||
		!reflect.DeepEqual(retired, capture.Retired) ||
		!reflect.DeepEqual(c.stage.origins, capture.Origins) ||
		c.stage.policy != capture.Policy ||
		c.stage.cutoffLocalSeq != capture.Graph[0].GetHeader().GetCutoffLocalSeq() {
		return service.ReceiptWholeStateCapture{}, errors.New("backup: receipt Snapshot candidate stage differs from validated stream")
	}
	if err := ctx.Err(); err != nil {
		return service.ReceiptWholeStateCapture{}, err
	}
	return capture, nil
}
