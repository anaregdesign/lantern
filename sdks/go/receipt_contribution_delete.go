package client

import (
	"context"
	"math"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// EdgeContributionDeleteReceiptResult preserves the original existence
// observation for one selectively deleted contribution.
type EdgeContributionDeleteReceiptResult struct {
	Contribution EdgeContributionRef
	OperationID  ReceiptOperationID
	Existed      bool
}

// DeleteEdgeContributionsWithReceipt sends one atomic, unchunked logical
// Delete. Persist the exact refs and ReceiptContext before the first send:
// only an authenticated matching receipt can recover the original true
// result after a response is lost. Retries stay pinned to the same endpoint
// and recheck its epoch, node, generation, and mutation capability.
func (l *Lantern) DeleteEdgeContributionsWithReceipt(
	ctx context.Context,
	refs []EdgeContributionRef,
	receiptContext ReceiptContext,
) ([]EdgeContributionDeleteReceiptResult, error) {
	request, stableRefs, stableContext, err := receiptContributionDeleteRequest(refs, receiptContext)
	if err != nil {
		return nil, err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()

	var response *pb.DeleteEdgeContributionsResponse
	err = l.executeReceiptMutation(
		ctx,
		ReceiptMutationDeleteEdgeContribution,
		stableContext.Continuity,
		func(callCtx context.Context) error {
			resp, callErr := unaryOnce(callCtx, request, l.client.DeleteEdgeContributions)
			if callErr == nil {
				response = resp
			}
			return callErr
		},
	)
	if err != nil {
		return nil, err
	}
	if err := mutationAcceptanceFromProto(response); err != nil {
		return nil, err
	}
	if err := validateContributionDeleteResponse(len(stableRefs), response); err != nil {
		return nil, receiptProtocolError("%v", err)
	}
	results := make([]EdgeContributionDeleteReceiptResult, len(stableRefs))
	for i, ref := range stableRefs {
		results[i] = EdgeContributionDeleteReceiptResult{
			Contribution: ref,
			OperationID:  stableContext.OperationIDs[i],
			Existed:      response.GetExisted()[i],
		}
	}
	return results, nil
}

// DeleteEdgeContributionWithReceipt is the one-item plural RPC facade.
func (l *Lantern) DeleteEdgeContributionWithReceipt(
	ctx context.Context,
	tail, head string,
	id ContribID,
	receiptContext ReceiptContext,
) (EdgeContributionDeleteReceiptResult, error) {
	results, err := l.DeleteEdgeContributionsWithReceipt(
		ctx, []EdgeContributionRef{{Tail: tail, Head: head, ContribID: id}}, receiptContext,
	)
	if err != nil {
		return EdgeContributionDeleteReceiptResult{}, err
	}
	if len(results) != 1 {
		return EdgeContributionDeleteReceiptResult{}, receiptProtocolError("singular contribution Delete returned %d items", len(results))
	}
	return results[0], nil
}

func receiptContributionDeleteRequest(
	refs []EdgeContributionRef,
	receiptContext ReceiptContext,
) (*pb.DeleteEdgeContributionsRequest, []EdgeContributionRef, ReceiptContext, error) {
	if len(refs) == 0 || uint64(len(refs)) > math.MaxInt32 {
		return nil, nil, ReceiptContext{}, invalidReceiptError("receipt contribution Delete item count must be between 1 and %d", int64(math.MaxInt32))
	}
	if err := receiptContext.Validate(len(refs)); err != nil {
		return nil, nil, ReceiptContext{}, err
	}
	if receiptContext.Mutation != ReceiptMutationDeleteEdgeContribution {
		return nil, nil, ReceiptContext{}, invalidReceiptError(
			"receipt context mutation is %s, want %s",
			receiptContext.Mutation, ReceiptMutationDeleteEdgeContribution,
		)
	}
	stableRefs := append([]EdgeContributionRef(nil), refs...)
	keys, err := edgeContributionKeys(stableRefs)
	if err != nil {
		return nil, nil, ReceiptContext{}, invalidReceiptError("%v", err)
	}
	stableContext := receiptContext.Clone()
	return &pb.DeleteEdgeContributionsRequest{
		Contributions: keys, ReceiptContext: receiptContextToProto(stableContext),
	}, stableRefs, stableContext, nil
}
