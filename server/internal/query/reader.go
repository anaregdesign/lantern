package query

import (
	"context"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Reader is the publication-aware read boundary consumed by Engine. It never
// exposes a backend or a graph value borrowed from storage.
type Reader interface {
	// Capture applies the caller's read consistency checks and may repeat the
	// search after a publication conflict. It must preserve ctx's authorization
	// view and detach all hits before releasing its read cut. A cut failure is
	// returned as error; a completed backend failure belongs in CaptureResult so
	// publication faults retain priority over backend errors.
	Capture(ctx context.Context, spec Spec) (CaptureResult, error)
	// ReadPage checks the same public-read availability around a retained page.
	// It must call capture at most once: session paging also updates its LRU.
	ReadPage(capture func() error) error
}

// Spec is one bounded, already validated search over the authorized context.
type Spec struct {
	Query      string
	Limit      int
	Prefix     string
	Options    search.MatchOptions
	Phrase     bool
	Budget     search.Budget
	Projection pb.SearchProjection
}

// CaptureResult owns the final attempt's hits, work record and backend error.
// It never combines partial results from optimistic attempts.
type CaptureResult struct {
	Hits         []*pb.SearchHit
	Stats        search.Stats
	BackendError error
}
