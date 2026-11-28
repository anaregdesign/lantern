package dataruntime

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ReceiptBaselineFormat identifies the canonical whole-state representation
// whose digest is bound to the published receipt generation.
type ReceiptBaselineFormat uint32

const (
	ReceiptBaselineFormatCombined ReceiptBaselineFormat = 1
)

// ReceiptBaselineArchiveCodec bridges the runtime to the canonical LANTARCH
// active-section and combined-baseline implementations without making dataruntime
// depend on backup.
// It is an internal composition seam, not a transport or user-facing API.
type ReceiptBaselineArchiveCodec interface {
	DigestCombinedReceiptBaseline(context.Context, ReceiptWholeStateCapture) ([32]byte, uint64, error)
	// StageReceiptBaselineCapture returns a detached candidate and canonical
	// graph frames for publication digest calculation and retry.
	StageReceiptBaselineCapture(
		context.Context,
		ReceiptWholeStateCapture,
		mutationreceipt.Config,
		time.Duration,
		func(*graphcache.GraphCache[string, *pb.Vertex]) error,
	) (*ReceiptBaselineCandidate, []*pb.SnapshotResponse, error)
}

// ReceiptBaselineCandidate is a fully validated detached archive image.
// Only ServingRuntime may install it into its identity-stable live objects.
type ReceiptBaselineCandidate struct {
	NamespaceFormat string
	Graph           *graphcache.GraphCache[string, *pb.Vertex]
	Receipts        *mutationreceipt.Store
	Retired         mutationreceipt.RetiredCatalogSnapshot
	Policy          mutationreceipt.Config
	Origins         []OriginState
	CutoffLocalSeq  uint64
	CutoffHLC       hlc.Timestamp
}
