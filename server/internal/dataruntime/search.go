package dataruntime

import (
	"context"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/query"
)

// SearchObservation keeps the executor's public metrics contract available to
// cross-module callers; query owns the terminal observation definition.
type SearchObservation = query.Observation

// SearchVertices delegates bounded execution and paging to query. The service
// retains graph publication cuts, namespace handling and authorization scopes.
func (s *Executor) SearchVertices(ctx context.Context, in *pb.SearchVerticesRequest) (*pb.SearchVerticesResponse, error) {
	return s.search.SearchVertices(ctx, in, searchReader{service: s}, s.metrics.OnSearchExecution)
}

// searchReader is the only query adapter allowed to read this executor's graph.
// Resolve the current backend on each capture so a runtime installed after
// construction retains the same publication and Snapshot ownership.
type searchReader struct{ service *Executor }

func (r searchReader) Capture(ctx context.Context, spec query.Spec) (query.CaptureResult, error) {
	var captured query.CaptureResult
	err := r.service.withPublicGraphReadRetry(ctx, func() error {
		// Optimistic captures replace the entire previous attempt, including
		// its backend failure and work counters.
		captured = query.CaptureResult{}
		if spec.Projection == pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX {
			snapshot, stats, err := r.service.cache.SearchVerticesSnapshotContext(ctx, spec.Query, spec.Limit, spec.Prefix, spec.Options, spec.Phrase, spec.Budget)
			captured.Stats, captured.BackendError = stats, err
			if err == nil {
				captured.Hits = make([]*pb.SearchHit, 0, len(snapshot))
				for _, ranked := range snapshot {
					hit := &pb.SearchHit{Key: ranked.Result.ID, Score: ranked.Result.Score}
					if ranked.Found && ranked.Value != nil {
						// The Snapshot-admission latch is still held. Cursor
						// sessions clone their retained copy independently.
						hit.Vertex = detachedGraphVertex(ranked.Value)
						hit.ProjectionStatus = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT
					} else {
						hit.ProjectionStatus = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_MISSING
					}
					captured.Hits = append(captured.Hits, hit)
				}
			}
		} else {
			ranked, stats, err := r.service.cache.SearchVerticesMatchContext(ctx, spec.Query, spec.Limit, spec.Prefix, spec.Options, spec.Phrase, spec.Budget)
			captured.Stats, captured.BackendError = stats, err
			if err == nil {
				captured.Hits = make([]*pb.SearchHit, 0, len(ranked))
				for _, result := range ranked {
					captured.Hits = append(captured.Hits, &pb.SearchHit{
						Key: result.ID, Score: result.Score,
						ProjectionStatus: pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_KEY_SCORE,
					})
				}
			}
		}
		// Keep backend errors separate: a publication fault or conflict must
		// still be checked before that error is exposed to the caller.
		return nil
	})
	if err != nil {
		return query.CaptureResult{}, err
	}
	return captured, nil
}

func (r searchReader) ReadPage(capture func() error) error {
	return r.service.withPublicGraphRead(capture)
}
