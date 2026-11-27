package dataruntime

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// The caller owns the serving publication gate. A failed append can leave
// a graph-first effect applied in RAM while its Tarp postimage is absent.
// Keep those pending effects authoritative until the exact relay repair.
func (s *Executor) cleanStoredVertexGraphLocked() *graphcache.GraphCache[string, *pb.Vertex] {
	if s.publicationFaultCount != 0 || s.runtime == nil {
		return nil
	}
	graph, _ := s.cache.(*graphcache.GraphCache[string, *pb.Vertex])
	if graph == nil || graph != s.runtime.graph || !graph.StorageBoundTo(s.runtime.GraphStore()) {
		return nil
	}
	return graph
}

func (s *Executor) putVertexBatchAtCut(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp, ifAbsent bool) ([]graphcache.PutOutcome, error) {
	if graph := s.cleanStoredVertexGraphLocked(); graph != nil {
		// Graph-first application and exact publication already own this
		// admitted request. Caller cancellation cannot abandon their work.
		return graph.PutVerticesFromCommittedCut(context.Background(), items, ts, ifAbsent)
	}
	if ifAbsent {
		_, outcomes, err := s.cache.PutVerticesWithExpirationIfAbsentHLCOutcomesChecked(items, ts)
		return outcomes, err
	}
	return s.cache.PutVerticesWithExpirationHLCOutcomesChecked(items, ts)
}

func (s *Executor) applyVertexPutsAtCut(items []graphcache.VertexItem[string, *pb.Vertex], ts hlc.Timestamp) ([]graphcache.PutOutcome, error) {
	if graph := s.cleanStoredVertexGraphLocked(); graph != nil {
		return graph.ApplyVertexPutsFromCommittedCut(context.Background(), items, ts)
	}
	return s.cache.PutVerticesWithExpirationHLCOutcomes(items, ts), nil
}

func (s *Executor) deleteVertexBatchAtCut(keys []string, ts hlc.Timestamp, expiration time.Time, local bool) ([]bool, []int, error) {
	if graph := s.cleanStoredVertexGraphLocked(); graph != nil {
		return graph.DeleteVerticesFromCommittedCut(context.Background(), keys, ts, expiration, local)
	}
	if local {
		return s.cache.DeleteVerticesHLCDecisionsChecked(keys, ts, expiration)
	}
	existed, accepted := s.cache.DeleteVerticesHLCDecisions(keys, ts, expiration)
	return existed, accepted, nil
}
