package client

import (
	"context"
	"errors"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

// GraphMutationCapability describes the receipt-required Put-then-Add API.
// Availability is endpoint-local, with effect-wise HA conflict resolution.
type GraphMutationCapability struct{ MaxItems, MaxRequestBytes uint32 }

// GraphMutationReceiptContext is the caller-persisted identity of one complete
// mixed call. IDs are ordered as all Vertex Puts followed by all Edge Adds.
// Preserve this, the explicit contribution IDs and exact inputs after any
// ambiguous result; never mint a replacement identity or split the retry.
type GraphMutationReceiptContext struct {
	Continuity   ReceiptContinuity
	GroupID      ReceiptGroupID
	OperationIDs []ReceiptOperationID
}

func (c GraphMutationReceiptContext) common() ReceiptContext {
	return ReceiptContext{Mutation: ReceiptMutationPutVertex, Continuity: c.Continuity, GroupID: c.GroupID, OperationIDs: c.OperationIDs}
}

// NewGraphMutationReceiptContext mints identities without sending a mutation.
// Persist the returned context before calling MutateGraph.
func (l *Lantern) NewGraphMutationReceiptContext(capability ReceiptCapability, count int) (GraphMutationReceiptContext, error) {
	if capability.GraphMutation == nil || count < 2 || count > int(capability.GraphMutation.MaxItems) || count > 64 || !capability.Supports(ReceiptMutationAddEdge) {
		return GraphMutationReceiptContext{}, errors.Join(ErrFailedPrecondition, ErrReceiptMutationUnsupported)
	}
	common, err := l.NewReceiptContext(capability, ReceiptMutationPutVertex, count)
	if err != nil {
		return GraphMutationReceiptContext{}, err
	}
	return GraphMutationReceiptContext{Continuity: common.Continuity, GroupID: common.GroupID, OperationIDs: common.OperationIDs}, nil
}

// GraphMutationResult contains the original receipts' typed results, not a
// promise that all effects remain current after replication or later writes.
type GraphMutationResult struct {
	Vertices []VertexPutReceiptResult
	Edges    []EdgeAddReceiptResult
}

// MutateGraph sends exactly one mixed request after one endpoint-continuity
// check. It never chunks, fails over, refreshes IDs or automatically retries.
// Both slices must be nonempty, Vertex keys unique, and contribution IDs explicit.
// Adds require live endpoints after the planned Puts and never create endpoints.
func (l *Lantern) MutateGraph(ctx context.Context, vertices []VertexInput, edges []EdgeAddReceiptInput, receiptContext GraphMutationReceiptContext) (GraphMutationResult, error) {
	empty := GraphMutationResult{}
	count := len(vertices) + len(edges)
	if len(vertices) == 0 || len(edges) == 0 || count > 64 {
		return empty, invalidReceiptError("mixed call requires Put then Add, at most 64 items")
	}
	stable := receiptContext.common().Clone()
	if err := stable.Validate(count); err != nil {
		return empty, err
	}
	seen := make(map[string]bool, len(vertices))
	for _, v := range vertices {
		if seen[v.Key] {
			return empty, invalidReceiptError("duplicate mixed Vertex key")
		}
		seen[v.Key] = true
	}
	vc := stable.Clone()
	vc.OperationIDs = vc.OperationIDs[:len(vertices)]
	ec := stable.Clone()
	ec.Mutation = ReceiptMutationAddEdge
	ec.OperationIDs = ec.OperationIDs[len(vertices):]
	puts, stableVertices, _, err := receiptPutVerticesRequest(vertices, vc, false)
	if err != nil {
		return empty, err
	}
	adds, stableEdges, _, err := receiptAddEdgesRequest(edges, ec)
	if err != nil {
		return empty, err
	}
	request := &pb.MutateGraphRequest{ReceiptContext: receiptContextToProto(stable), Items: make([]*pb.GraphMutationItem, 0, count)}
	for _, v := range puts.Vertices {
		request.Items = append(request.Items, &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_PutVertex{PutVertex: v}})
	}
	for i, e := range adds.Edges {
		request.Items = append(request.Items, &pb.GraphMutationItem{Operation: &pb.GraphMutationItem_AddEdge{AddEdge: &pb.GraphMutationEdgeAdd{Edge: e, ContribId: adds.ContribIds[i]}}})
	}
	if proto.Size(request) > 1<<20 {
		return empty, invalidReceiptError("mixed request exceeds 1 MiB")
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	capability, err := l.getReceiptCapabilityOnce(ctx)
	if err != nil {
		return empty, err
	}
	if !capability.Enabled || capability.GraphMutation == nil || capability.Continuity != stable.Continuity || count > int(capability.GraphMutation.MaxItems) || proto.Size(request) > int(capability.GraphMutation.MaxRequestBytes) {
		return empty, &ReceiptReconciliationError{Expected: stable.Continuity, Observed: &capability.Continuity, Cause: ErrReceiptMutationUnsupported}
	}
	response, err := unaryOnce(ctx, request, l.client.MutateGraph)
	if err != nil {
		return empty, err
	}
	if err := mutationAcceptanceFromProto(response); err != nil {
		return empty, err
	}
	if len(response.GetResults()) != count {
		return empty, receiptProtocolError("mixed response count differs")
	}
	vertexResponse := &pb.PutVerticesResponse{Outcomes: make([]pb.PutOutcome, len(vertices))}
	edgeResponse := &pb.AddEdgesResponse{Written: int32(len(edges)), EffectiveWeights: make([]float32, len(edges))}
	for i, r := range response.GetResults() {
		if i < len(vertices) {
			v, ok := r.GetResult().(*pb.GraphMutationResult_PutVertex)
			if !ok || v == nil || v.PutVertex == pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET {
				return empty, receiptProtocolError("mixed Put result differs")
			}
			vertexResponse.Outcomes[i] = v.PutVertex
		} else {
			e, ok := r.GetResult().(*pb.GraphMutationResult_AddEdgeEffectiveWeight)
			if !ok || e == nil {
				return empty, receiptProtocolError("mixed Add result differs")
			}
			edgeResponse.EffectiveWeights[i-len(vertices)] = e.AddEdgeEffectiveWeight
		}
	}
	result := GraphMutationResult{}
	result.Vertices, err = vertexPutReceiptResults(stableVertices, vc.OperationIDs, vertexResponse)
	if err != nil {
		return empty, err
	}
	result.Edges, err = edgeAddReceiptResults(stableEdges, ec.OperationIDs, edgeResponse)
	return result, err
}
