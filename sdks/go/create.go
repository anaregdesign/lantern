package client

import (
	"context"
	"fmt"
	"math"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// CreateEdgeOutcome is an original conditional result, never an existing value.
type CreateEdgeOutcome uint8

const (
	CreateEdgeCreatedAndLive CreateEdgeOutcome = iota + 1
	CreateEdgeExists
	CreateEdgeEndpointNotLive
	CreateEdgeExpired
)

func createEdgeOutcomeFromProto(raw pb.CreateEdgeOutcome) (CreateEdgeOutcome, error) {
	if raw < pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE || raw > pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EXPIRED {
		return 0, fmt.Errorf("client: invalid Create outcome %d", raw)
	}
	return CreateEdgeOutcome(raw), nil
}
func createEdgeOutcomes(raw []pb.CreateEdgeOutcome, count int) ([]CreateEdgeOutcome, error) {
	if len(raw) != count {
		return nil, fmt.Errorf("client: misaligned Create outcomes %d != %d", len(raw), count)
	}
	results := make([]CreateEdgeOutcome, count)
	for i, value := range raw {
		var err error
		results[i], err = createEdgeOutcomeFromProto(value)
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}
func validateCreateInputs(inputs []EdgeInput) error {
	for i, item := range inputs {
		if item.Weight == 0 || math.IsNaN(float64(item.Weight)) || math.IsInf(float64(item.Weight), 0) {
			return fmt.Errorf("client: Create input %d requires a finite nonzero weight", i)
		}
	}
	return nil
}

// CreateEdges creates only between existing endpoints. Chunks are ordered;
// uncertain responses are never automatically retried or sent to another node.
func (l *Lantern) CreateEdges(ctx context.Context, inputs []EdgeInput) ([]CreateEdgeOutcome, error) {
	if err := validateCreateInputs(inputs); err != nil {
		return nil, err
	}
	results := []CreateEdgeOutcome{}
	undisclosed := false
	_, err := runBatchWrite(ctx, l, edgesFrom(inputs), func(ctx context.Context, chunk []*pb.Edge) (int32, error) {
		resp, err := unaryOnce(ctx, &pb.CreateEdgesRequest{Edges: chunk}, l.client.CreateEdges)
		if err != nil {
			return 0, err
		}
		if err := mutationAcceptanceFromProto(resp); err != nil {
			if _, accepted := err.(*MutationAcceptance); accepted {
				undisclosed = true
			}
			return 0, err
		}
		decoded, err := createEdgeOutcomes(resp.GetOutcomes(), len(chunk))
		if err != nil {
			return 0, err
		}
		results = append(results, decoded...)
		return int32(len(chunk)), nil
	})
	if undisclosed {
		return nil, err
	}
	return results, err
}

// CreateEdge forwards one input to CreateEdges.
func (l *Lantern) CreateEdge(ctx context.Context, input EdgeInput) (CreateEdgeOutcome, error) {
	outcomes, err := l.CreateEdges(ctx, []EdgeInput{input})
	if err != nil {
		return 0, err
	}
	return outcomes[0], nil
}

// CreateEdgesWithReceipt preserves one atomic logical call. Context and exact
// absolute expirations must be persisted before sending; ambiguous results
// require status-first reconciliation using the original IDs.
func (l *Lantern) CreateEdgesWithReceipt(ctx context.Context, inputs []EdgeInput, receiptContext ReceiptContext) ([]CreateEdgeOutcome, error) {
	if err := validateCreateInputs(inputs); err != nil {
		return nil, err
	}
	if len(inputs) > 10000 {
		return nil, invalidReceiptError("receipt Create supports at most 10000 items")
	}
	if err := receiptContext.Validate(len(inputs)); err != nil {
		return nil, err
	}
	if receiptContext.Mutation != ReceiptMutationCreateEdge {
		return nil, invalidReceiptError("Create requires a Create receipt context")
	}
	stable := receiptContext.Clone()
	request := &pb.CreateEdgesRequest{Edges: edgesFrom(inputs), ReceiptContext: receiptContextToProto(stable)}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	var response *pb.CreateEdgesResponse
	err := l.executeReceiptMutation(ctx, ReceiptMutationCreateEdge, stable.Continuity, func(callCtx context.Context) error {
		resp, err := unaryOnce(callCtx, request, l.client.CreateEdges)
		if err == nil {
			response = resp
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := mutationAcceptanceFromProto(response); err != nil {
		return nil, err
	}
	return createEdgeOutcomes(response.GetOutcomes(), len(inputs))
}

// CreateEdgeWithReceipt forwards one input to the atomic plural operation.
func (l *Lantern) CreateEdgeWithReceipt(ctx context.Context, input EdgeInput, receiptContext ReceiptContext) (CreateEdgeOutcome, error) {
	outcomes, err := l.CreateEdgesWithReceipt(ctx, []EdgeInput{input}, receiptContext)
	if err != nil {
		return 0, err
	}
	return outcomes[0], nil
}
