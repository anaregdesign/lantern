package service

import (
	"fmt"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// No endpoint capacity is consumed by an immutable Edge-only effect.
func (s *LanternService) checkEdgeEndpointCapacity(count int, noEndpointCreation bool) error {
	if noEndpointCreation {
		return nil
	}
	return s.checkVertexCapacity(2 * count)
}

// Reject a misplaced effect flag before graph, WAL, or origin progress changes.
func validateEdgeEndpointEffect(op *pb.MutationOp) error {
	if !op.GetNoEndpointCreation() {
		return nil
	}
	switch op.GetOp().(type) {
	case *pb.MutationOp_AddEdge, *pb.MutationOp_AddEdges,
		*pb.MutationOp_PutEdge, *pb.MutationOp_PutEdges,
		*pb.MutationOp_ReplicatedPutEdges, *pb.MutationOp_ReplicatedReceiptEdgeAdd:
		return nil
	default:
		return fmt.Errorf("no-endpoint-creation effect requires Edge Add/Put")
	}
}

func (s *LanternService) applyLocalEdgeAdd(items []graphcache.EdgeItem[string]) ([]float32, int, error) {
	if s.dataAuthorization {
		return s.cache.AddEdgesWithExpirationContribChecked(items)
	}
	effective, deduped := s.cache.AddEdgesWithExpirationContrib(items)
	return effective, deduped, nil
}

func (s *LanternService) applyLocalEdgeAddHLC(items []graphcache.EdgeItem[string], ts hlc.Timestamp) ([]float32, []bool, int, error) {
	if s.dataAuthorization {
		return s.cache.AddEdgesWithExpirationContribHLCResultsChecked(items, ts)
	}
	effective, accepted, deduped := s.cache.AddEdgesWithExpirationContribHLCResults(items, ts)
	return effective, accepted, deduped, nil
}

func edgeEffectMayCreateEndpoints(op *pb.MutationOp) bool {
	switch op.GetOp().(type) {
	case *pb.MutationOp_AddEdge, *pb.MutationOp_AddEdges, *pb.MutationOp_PutEdge,
		*pb.MutationOp_PutEdges, *pb.MutationOp_ReplicatedPutEdges, *pb.MutationOp_ReplicatedReceiptEdgeAdd:
		return true
	}
	return false
}
