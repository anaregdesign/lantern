package service

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ReceiptBaselineArchiveCodec bridges the runtime to the canonical
// RECEIPT_V1 archive implementation without making service depend on backup.
// It is an internal composition seam, not a transport or user-facing API.
type ReceiptBaselineArchiveCodec interface {
	EncodeReceiptBaseline(context.Context, ReceiptWholeStateCapture) ([]byte, error)
	StageReceiptBaseline(
		context.Context,
		[]byte,
		mutationreceipt.Config,
		time.Duration,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error,
	) (*ReceiptBaselineCandidate, error)
}

// ReceiptBaselineCandidate is a fully validated detached archive image.
// Only ServingRuntime may install it into its identity-stable live objects.
type ReceiptBaselineCandidate struct {
	Graph          *graphcache.GraphCache[string, *pb.Vertex]
	Receipts       *mutationreceipt.Store
	Policy         mutationreceipt.Config
	Origins        []OriginState
	CutoffLocalSeq uint64
	CutoffHLC      hlc.Timestamp
}
