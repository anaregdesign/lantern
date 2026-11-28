package backup

import (
	"context"
	"io"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"google.golang.org/protobuf/proto"
)

// StageReceiptBaselineCapture verifies the same canonical archive boundaries
// without assembling or decoding a combined archive. Graph frames are decoded
// individually so neither the staged graph nor the publication digest borrows
// mutable frame contents from the caller.
func (ReceiptBaselineCodec) StageReceiptBaselineCapture(
	ctx context.Context,
	capture dataruntime.ReceiptWholeStateCapture,
	expected mutationreceipt.Config,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (*dataruntime.ReceiptBaselineCandidate, []*pb.SnapshotResponse, error) {
	archive, retired, err := validateReceiptBaselineCapture(ctx, capture)
	if err != nil {
		return nil, nil, err
	}
	if _, err := writeRetiredCatalogArchive(ctx, io.Discard, capture.Policy, retired); err != nil {
		return nil, nil, err
	}
	frames, err := detachReceiptBaselineGraph(ctx, archive)
	if err != nil {
		return nil, nil, err
	}
	archive.Graph = frames
	archive.Policy.ClockHighWater = archive.Receipts.ClockHighWater()
	stage, err := stageDecodedReceiptWholeStateArchive(ctx, archive, expected, defaultTTL, configureGraph)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return receiptBaselineCandidate(archive, stage, retired), frames, nil
}

func detachReceiptBaselineGraph(ctx context.Context, archive wholeStateArchive) ([]*pb.SnapshotResponse, error) {
	frames := make([]*pb.SnapshotResponse, 0, len(archive.Graph))
	size := wholeStateArchiveHeaderSize
	if err := visitWholeStateArchiveRecords(archive, func(kind byte, payload []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		size, err = checkedArchiveRecordSize(size, len(payload))
		if err != nil {
			return err
		}
		if kind == wholeStateGraphRecord {
			frame := new(pb.SnapshotResponse)
			if err := proto.Unmarshal(payload, frame); err != nil || frame.GetEntry() == nil {
				return wholeStateArchiveError("invalid graph frame")
			}
			frames = append(frames, frame)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if _, err := checkedArchiveRecordSize(size, wholeStateArchiveFooterSize); err != nil {
		return nil, err
	}
	return frames, nil
}
