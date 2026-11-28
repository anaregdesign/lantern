package dataruntime

import (
	"context"
	"encoding/hex"
	"errors"
	"iter"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
	"github.com/anaregdesign/tarp"
)

// tarpDiskSearchInput is tied to a pinned graph read. Building or opening a
// derived view cannot resolve any Vertex from a newer graph revision.
type tarpDiskSearchInput struct {
	snapshot   *tarp.Snapshot
	generation uint64
	rows       *graphstore.Rows[string, *pb.Vertex]
	source     search.DiskSource
	changeCut  tarpSearchCut
	projection func(string, *pb.Vertex) search.Document
}

func captureTarpDiskSearchInput(ctx context.Context, view *graphstore.ReadView, corpus, fingerprint string, watermark time.Time, projection func(string, *pb.Vertex) search.Document) (tarpDiskSearchInput, error) {
	if view == nil || view.Database() == nil || projection == nil || watermark.IsZero() {
		return tarpDiskSearchInput{}, errTarpSearchCut
	}
	if err := checkTarpSearchTime(ctx, view.Snapshot, view.Generation, watermark); err != nil {
		return tarpDiskSearchInput{}, err
	}
	cut, err := readTarpSearchCut(ctx, view.Snapshot, view.Generation)
	if err != nil {
		if ctx.Err() != nil {
			return tarpDiskSearchInput{}, ctx.Err()
		}
		return tarpDiskSearchInput{}, errors.Join(search.ErrIndexIncomplete, err)
	}
	rows, err := diskRows()
	if err != nil {
		return tarpDiskSearchInput{}, err
	}
	node := view.Database().NodeID()
	source := search.DiskSource{
		Store: hex.EncodeToString(node[:]), Corpus: corpus, Fingerprint: fingerprint,
		Epoch: cut.epoch, Sequence: cut.last, Watermark: watermark,
		// The graph commit chain binds the source after restoring an old
		// backup and applying a different suffix at the same sequence.
		Digest: cut.digest,
	}
	return tarpDiskSearchInput{snapshot: view.Snapshot, generation: view.Generation, rows: rows, source: source, changeCut: cut, projection: projection}, nil
}

func (in tarpDiskSearchInput) documents(ctx context.Context) iter.Seq2[search.DiskDocument, error] {
	return func(yield func(search.DiskDocument, error) bool) {
		for row, err := range in.rows.VerifiedVertices(ctx, in.snapshot, in.generation) {
			if err != nil {
				yield(search.DiskDocument{}, err)
				return
			}
			if !yield(in.document(row.Key, row.Vertex), nil) {
				return
			}
		}
	}
}

func (in tarpDiskSearchInput) build(ctx context.Context, db *tarp.DB, base uint64, analyzer search.Analyzer, opts ...search.IndexOption) error {
	return search.BuildDiskBase(ctx, db, base, in.source, in.documents(ctx), analyzer, opts...)
}

func (in tarpDiskSearchInput) document(key string, vertex graphstore.Vertex[*pb.Vertex]) search.DiskDocument {
	version := encodeTarpClock(nil, vertex.HLC)
	version = append(version, vertex.HLC.NodeID[:]...)
	return search.DiskDocument{Key: key, Version: version, Expiration: vertex.Expiration, Document: in.projection(key, vertex.Value)}
}

// changes resolves final postimages from the pinned Graph state only after
// the retained accepted suffix is verified. Repeated changes use one key.
func (in tarpDiskSearchInput) changes(ctx context.Context, base search.DiskSource, maxBytes, maxKeys int) iter.Seq2[search.DiskDocument, error] {
	return func(yield func(search.DiskDocument, error) bool) {
		if base.Store != in.source.Store || base.Corpus != in.source.Corpus || base.Fingerprint != in.source.Fingerprint || base.Epoch != in.source.Epoch {
			yield(search.DiskDocument{}, search.ErrIndexIncomplete)
			return
		}
		seen := make(map[string]struct{})
		err := visitTarpSearchChanges(ctx, in.snapshot, in.generation, in.changeCut, base.Sequence, base.Digest, maxBytes, maxKeys, func(_ uint64, key string) error {
			seen[key] = struct{}{}
			return nil
		})
		if err != nil {
			if ctx.Err() != nil {
				yield(search.DiskDocument{}, ctx.Err())
			} else {
				yield(search.DiskDocument{}, errors.Join(search.ErrIndexIncomplete, err))
			}
			return
		}
		keys := make([]string, 0, len(seen))
		for key := range seen {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				yield(search.DiskDocument{}, err)
				return
			}
			vertex, ok, err := in.rows.GetVertex(in.snapshot, in.generation, key)
			if err != nil {
				yield(search.DiskDocument{}, errors.Join(search.ErrIndexIncomplete, err))
				return
			}
			doc := search.DiskDocument{Key: key}
			if ok {
				doc = in.document(key, vertex)
			}
			if !yield(doc, nil) {
				return
			}
		}
	}
}

type tarpDiskSearchReader struct {
	mu             sync.RWMutex
	base           atomic.Pointer[search.DiskIndex]
	badBase        atomic.Pointer[search.DiskIndex]
	project        func(string, *pb.Vertex) search.Document
	maxChangeBytes int
	maxChangeKeys  int
}

func (reader *tarpDiskSearchReader) capture(ctx context.Context, db *graphstore.DB, spec query.Spec) query.CaptureResult {
	reader.mu.RLock()
	defer reader.mu.RUnlock()
	var result query.CaptureResult
	view, err := db.Read()
	if err != nil {
		result.BackendError = err
		return result
	}
	defer func() { _ = view.Close() }()
	now := time.Now()
	index, input, err := reader.readInput(ctx, view, now)
	if err != nil {
		result.BackendError = err
		return result
	}
	scope := graphcache.AdmittedQueryView(ctx)
	accept := func(key string) bool {
		return scope.Vertex(key) && strings.HasPrefix(key, spec.Prefix)
	}
	budget := spec.Budget
	budget.MaxQueryBytes = int64(spec.MaxQueryBytes)
	ranked, stats, err := index.SearchAtWithChanges(ctx, spec.Query, spec.Limit, accept,
		spec.Options, spec.Phrase, budget, now, input.changes(ctx, index.Source(), reader.maxChangeBytes, reader.maxChangeKeys))
	var analysisLimit *search.AnalysisLimitError
	if reader.noteDerivedFault(index, err) || errors.As(err, &analysisLimit) {
		err = errors.Join(search.ErrIndexIncomplete, err)
	}
	result.Stats, result.BackendError = stats, err
	if err != nil {
		return result
	}
	hits := make([]*pb.SearchHit, 0, len(ranked))
	for _, rank := range ranked {
		hit := &pb.SearchHit{Key: rank.ID, Score: rank.Score, ProjectionStatus: pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_KEY_SCORE}
		if spec.Projection == pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX {
			vertex, found, err := input.rows.VertexAt(view.Snapshot, view.Generation, rank.ID, now)
			if err != nil {
				result.BackendError = err
				return result
			}
			if found && vertex != nil {
				hit.Vertex = detachedGraphVertex(vertex)
				hit.ProjectionStatus = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT
			} else {
				hit.ProjectionStatus = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_MISSING
			}
		}
		hits = append(hits, hit)
	}
	result.Hits = hits
	return result
}

func isDerivedSearchFault(err error) bool {
	return !errors.Is(err, search.ErrIndexIncomplete) &&
		(errors.Is(err, search.ErrDiskIndex) || errors.Is(err, tarp.ErrDataUnavailable) || errors.Is(err, tarp.ErrCorrupt))
}

// Graph-stream read failures carry ErrIndexIncomplete. Only a fault from the
// selected derived base should schedule replacement of that exact handle.
func (reader *tarpDiskSearchReader) noteDerivedFault(index *search.DiskIndex, err error) bool {
	if !isDerivedSearchFault(err) {
		return false
	}
	if reader.base.Load() == index {
		reader.badBase.Store(index)
	}
	return true
}

func (reader *tarpDiskSearchReader) readInput(ctx context.Context, view *graphstore.ReadView, now time.Time) (*search.DiskIndex, tarpDiskSearchInput, error) {
	index := reader.base.Load()
	if index == nil {
		return nil, tarpDiskSearchInput{}, search.ErrIndexIncomplete
	}
	base := index.Source()
	ref, err := readTarpSearchBaseRef(ctx, view)
	if err != nil || index.BaseID() != ref.ID || !sameTarpDiskSource(base, ref.Source) {
		if ctx.Err() != nil {
			return nil, tarpDiskSearchInput{}, ctx.Err()
		}
		return nil, tarpDiskSearchInput{}, errors.Join(search.ErrIndexIncomplete, err)
	}
	input, err := captureTarpDiskSearchInput(ctx, view, base.Corpus, base.Fingerprint, now, reader.project)
	return index, input, err
}

// SearchIndexStats samples the selected index and its committed Graph overlay.
func (r *ServingRuntime) SearchIndexStats(ctx context.Context) search.IndexMemoryStats {
	if r.tarp == nil || r.tarp.diskSearch == nil {
		return r.graph.SearchIndexMemoryStats()
	}
	reader := r.tarp.diskSearch
	reader.mu.RLock()
	defer reader.mu.RUnlock()
	incomplete := search.IndexMemoryStats{Health: search.IndexIncomplete}
	view, err := r.tarp.db.Read()
	if err != nil {
		return incomplete
	}
	defer func() { _ = view.Close() }()
	now := time.Now()
	index, input, err := reader.readInput(ctx, view, now)
	if err != nil {
		return incomplete
	}
	stats, err := index.InspectAtWithChanges(ctx, now, input.changes(ctx, index.Source(), reader.maxChangeBytes, reader.maxChangeKeys))
	if err != nil {
		reader.noteDerivedFault(index, err)
		return incomplete
	}
	return stats
}

// advance publishes an incremental derived cut before selecting it in
// Graph. Holding the reader gate keeps new captures from opening the old
// handle after the derived footer has changed.
func (reader *tarpDiskSearchReader) advance(ctx context.Context, graph *graphstore.DB, derived *tarp.DB, id uint64, analyzer search.Analyzer, scorer search.Scorer, budget search.Budget, opts ...search.IndexOption) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	view, err := graph.Read()
	if err != nil {
		return err
	}
	defer func() { _ = view.Close() }()
	index := reader.base.Load()
	if index == nil {
		return search.ErrIndexIncomplete
	}
	previous := index.Source()
	now := time.Now()
	input, err := captureTarpDiskSearchInput(ctx, view, previous.Corpus, previous.Fingerprint, now, reader.project)
	if err != nil {
		return err
	}
	deadline := index.NextDeadline()
	if input.source.Sequence == previous.Sequence && input.source.Digest == previous.Digest && (deadline.IsZero() || now.Before(deadline)) {
		return nil
	}
	ref := tarpSearchBaseRef{ID: id, Source: input.source}
	current, err := index.AdvanceDiskBase(ctx, input.source, input.changes(ctx, previous, reader.maxChangeBytes, reader.maxChangeKeys), budget)
	if err != nil {
		return err
	}
	if err := selectTarpSearchBase(ctx, graph, derived, ref, analyzer, scorer, reader.maxChangeBytes, reader.maxChangeKeys, opts...); err != nil {
		return err
	}
	reader.base.Store(current)
	return reclaimTarpSearchChanges(ctx, graph, ref)
}

func checkTarpSearchTime(ctx context.Context, snapshot *tarp.Snapshot, generation uint64, at time.Time) error {
	reclaimed, err := graphstore.VertexExpiryWatermark(ctx, snapshot, generation)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.Join(search.ErrIndexIncomplete, err)
	}
	if at.Before(reclaimed) {
		return search.ErrIndexIncomplete
	}
	return nil
}
