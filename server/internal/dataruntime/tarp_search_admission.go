package dataruntime

import (
	"context"
	"errors"
	"iter"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

var errUnstagedDiskSearchWrite = errors.New("disk Search requires a reversible local Vertex stage")

// diskSearchAdmissionRejection identifies a validation failure before the
// Graph commit attempt. Do not infer definite rejection from arbitrary WAL
// causes which happen to contain a Search error.
type diskSearchAdmissionRejection struct{ cause error }

func (e *diskSearchAdmissionRejection) Error() string { return e.cause.Error() }
func (e *diskSearchAdmissionRejection) Unwrap() error { return e.cause }

func (e *diskSearchAdmissionRejection) graphWriteRejection() error {
	return diskSearchWriteRejection(e)
}

func diskSearchWriteRejection(err error) error {
	var rejection *diskSearchAdmissionRejection
	if !errors.As(err, &rejection) {
		return nil
	}
	var limit *search.AnalysisLimitError
	if errors.As(rejection.cause, &limit) || errors.Is(rejection.cause, search.ErrIndexIncomplete) {
		return searchIndexWriteError(rejection.cause)
	}
	var work *search.BudgetExceededError
	if errors.As(rejection.cause, &work) {
		return query.ResourceError(pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED, string(work.Kind), rejection.cause)
	}
	return connect.NewError(connect.CodeUnavailable, rejection.cause)
}

type searchPostimages struct {
	rows    *graphstore.Rows[string, *pb.Vertex]
	keys    []string
	changed map[string][]byte
}

func (p searchPostimages) documents(project func(string, *pb.Vertex) search.Document) iter.Seq2[search.DiskDocument, error] {
	return func(yield func(search.DiskDocument, error) bool) {
		input := tarpDiskSearchInput{projection: project}
		for _, key := range p.keys {
			doc := search.DiskDocument{Key: key}
			if value := p.changed[key]; value != nil {
				vertex, err := p.rows.DecodeVertexPostimage(value)
				if err != nil {
					yield(search.DiskDocument{}, err)
					return
				}
				doc = input.document(key, vertex)
			}
			if !yield(doc, nil) {
				return
			}
		}
	}
}

// validateAdmission runs under Graph's writer lock using the very preimage
// that will be committed. The active Search handle is atomic: taking the
// Search reader gate here would invert advance's reader-gate -> Graph order.
func (reader *tarpDiskSearchReader) validateAdmission(ctx context.Context, index *search.DiskIndex, admission *search.DiskAdmission, snapshot *tarp.Snapshot, generation uint64, cut tarpSearchCut, at time.Time, proposal searchPostimages) error {
	if at.IsZero() || reader.badBase.Load() == index {
		return search.ErrIndexIncomplete
	}
	if err := checkTarpSearchTime(ctx, snapshot, generation, at); err != nil {
		return err
	}
	base := index.Source()
	ref, err := readTarpSearchBaseRefAt(ctx, snapshot, generation)
	if err != nil {
		return errors.Join(search.ErrIndexIncomplete, err)
	}
	if index.BaseID() != ref.ID || !sameTarpDiskSource(base, ref.Source) {
		return search.ErrIndexIncomplete
	}
	current := base
	current.Epoch, current.Sequence, current.Digest, current.Watermark = cut.epoch, cut.last, cut.digest, at
	input := tarpDiskSearchInput{
		snapshot: snapshot, generation: generation, rows: proposal.rows,
		source: current, changeCut: cut, projection: reader.project,
	}
	err = admission.Validate(ctx, at, input.changes(ctx, base, reader.maxChangeBytes, reader.maxChangeKeys),
		proposal.documents(reader.project), search.Budget{})
	if errors.Is(err, search.ErrDiskIndex) {
		return errors.Join(search.ErrIndexIncomplete, err)
	}
	return err
}

func sameTarpDiskSource(a, b search.DiskSource) bool {
	return a.Store == b.Store && a.Corpus == b.Corpus && a.Fingerprint == b.Fingerprint &&
		a.Epoch == b.Epoch && a.Sequence == b.Sequence && a.Digest == b.Digest && a.Watermark.Equal(b.Watermark)
}
