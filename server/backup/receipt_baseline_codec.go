package backup

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

// ReceiptBaselineCodec is the canonical RECEIPT_V1 archive adapter used by
// the durable runtime. It has no network surface and does not enable receipt
// mutation or Snapshot exchange.
type ReceiptBaselineCodec struct{}

func (ReceiptBaselineCodec) EncodeReceiptBaseline(ctx context.Context, capture service.ReceiptWholeStateCapture) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	archive := wholeStateArchive{
		Graph:    capture.Graph,
		Receipts: capture.Receipts,
		Policy:   capture.Policy,
		Origins:  capture.Origins,
	}
	var encoded bytes.Buffer
	if err := encodeWholeStateArchive(&encoded, archive); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func (ReceiptBaselineCodec) StageReceiptBaseline(
	ctx context.Context,
	raw []byte,
	expected mutationreceipt.Config,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
) (*service.ReceiptBaselineCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	archive, err := decodeWholeStateArchive(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	var canonical bytes.Buffer
	if err := encodeWholeStateArchive(&canonical, archive); err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical.Bytes(), raw) {
		return nil, wholeStateArchiveError("archive is not canonical")
	}
	stage, err := stageReceiptWholeStateArchive(ctx, bytes.NewReader(raw), expected, defaultTTL, configureGraph)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stage == nil || stage.graph == nil || stage.receipts == nil {
		return nil, fmt.Errorf("%w: staged baseline is incomplete", errWholeStateArchive)
	}
	return &service.ReceiptBaselineCandidate{
		Graph:          stage.graph,
		Receipts:       stage.receipts,
		Policy:         stage.policy,
		Origins:        append([]service.OriginState(nil), stage.origins...),
		CutoffLocalSeq: stage.cutoffLocalSeq,
		CutoffHLC:      stage.cutoffHLC,
	}, nil
}
