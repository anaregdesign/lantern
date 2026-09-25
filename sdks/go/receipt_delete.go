package client

import (
	"context"
	"errors"
	"math"
	"unicode/utf8"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// EdgeDeleteReceiptResult is one request-index-aligned exact Delete result.
type EdgeDeleteReceiptResult struct {
	Edge        EdgeRef
	OperationID ReceiptOperationID
	Existed     bool
}

// DeleteEdgesWithReceipt removes one atomic logical batch using a
// caller-owned ReceiptContext. The method never chunks the batch because the
// group and its index/count contract must remain one server commit envelope.
//
// Persist receiptContext before the first call. WithRetry may resend the
// byte-identical request only after the same endpoint reports the same epoch,
// node ID, and generation. A continuity change returns
// *ReceiptReconciliationError without sending the mutation.
func (l *Lantern) DeleteEdgesWithReceipt(
	ctx context.Context,
	refs []EdgeRef,
	receiptContext ReceiptContext,
) ([]EdgeDeleteReceiptResult, error) {
	request, stableContext, err := receiptDeleteRequest(refs, receiptContext)
	if err != nil {
		return nil, err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()

	var response *pb.DeleteEdgesResponse
	attempt := func() error {
		if err := l.requireReceiptContinuityOnce(ctx, stableContext.Continuity); err != nil {
			return err
		}
		resp, err := unaryOnce(ctx, request, l.client.DeleteEdges)
		if err != nil {
			if errors.Is(err, ErrFailedPrecondition) {
				if continuityErr := l.recheckReceiptContinuity(ctx, stableContext.Continuity); continuityErr != nil {
					return continuityErr
				}
			}
			return err
		}
		response = resp
		return nil
	}
	if l != nil && l.opts.retry != nil {
		err = l.opts.retry.run(ctx, attempt)
	} else {
		err = attempt()
	}
	if err != nil {
		return nil, err
	}
	return edgeDeleteReceiptResults(refs, stableContext.OperationIDs, response)
}

// DeleteEdgeWithReceipt is the one-item facade over DeleteEdgesWithReceipt.
func (l *Lantern) DeleteEdgeWithReceipt(
	ctx context.Context,
	tail, head string,
	receiptContext ReceiptContext,
) (EdgeDeleteReceiptResult, error) {
	results, err := l.DeleteEdgesWithReceipt(ctx, []EdgeRef{{Tail: tail, Head: head}}, receiptContext)
	if err != nil {
		return EdgeDeleteReceiptResult{}, err
	}
	if len(results) != 1 {
		return EdgeDeleteReceiptResult{}, receiptProtocolError("singular Edge Delete returned %d items", len(results))
	}
	return results[0], nil
}

func receiptDeleteRequest(refs []EdgeRef, receiptContext ReceiptContext) (*pb.DeleteEdgesRequest, ReceiptContext, error) {
	if len(refs) == 0 || uint64(len(refs)) > math.MaxUint32 {
		return nil, ReceiptContext{}, invalidReceiptError("receipt Edge Delete item count must be between 1 and %d", uint64(math.MaxUint32))
	}
	if err := receiptContext.Validate(len(refs)); err != nil {
		return nil, ReceiptContext{}, err
	}
	stableContext := receiptContext.Clone()
	keys := make([]*pb.EdgeKey, len(refs))
	for i, ref := range refs {
		if ref.Tail == "" || ref.Head == "" || !utf8.ValidString(ref.Tail) || !utf8.ValidString(ref.Head) {
			return nil, ReceiptContext{}, invalidReceiptError("edges[%d] must have nonempty UTF-8 tail and head", i)
		}
		keys[i] = &pb.EdgeKey{Tail: ref.Tail, Head: ref.Head}
	}
	rawIDs := make([][]byte, len(stableContext.OperationIDs))
	for i, id := range stableContext.OperationIDs {
		rawIDs[i] = id.Bytes()
	}
	return &pb.DeleteEdgesRequest{
		Edges: keys,
		ReceiptContext: &pb.MutationReceiptContext{
			OperationIds:  rawIDs,
			LogicalCallId: stableContext.GroupID.Bytes(),
			Endpoint: &pb.ReceiptEndpoint{
				NodeId:     stableContext.Continuity.NodeID.Bytes(),
				Generation: stableContext.Continuity.Generation.Bytes(),
			},
		},
	}, stableContext, nil
}

func (l *Lantern) requireReceiptContinuityOnce(ctx context.Context, expected ReceiptContinuity) error {
	capability, err := l.getReceiptCapabilityOnce(ctx)
	if err != nil {
		return err
	}
	if !capability.Enabled {
		return &ReceiptReconciliationError{
			Expected: expected,
			Cause:    errors.Join(ErrFailedPrecondition, ErrReceiptsDisabled),
		}
	}
	if capability.Continuity != expected {
		observed := capability.Continuity
		return &ReceiptReconciliationError{
			Expected: expected,
			Observed: &observed,
			Cause:    ErrFailedPrecondition,
		}
	}
	return nil
}

func (l *Lantern) recheckReceiptContinuity(ctx context.Context, expected ReceiptContinuity) error {
	err := l.requireReceiptContinuityOnce(ctx, expected)
	var reconciliation *ReceiptReconciliationError
	if errors.As(err, &reconciliation) {
		return reconciliation
	}
	return nil
}

func edgeDeleteReceiptResults(
	refs []EdgeRef,
	operationIDs []ReceiptOperationID,
	response *pb.DeleteEdgesResponse,
) ([]EdgeDeleteReceiptResult, error) {
	if response == nil {
		return nil, receiptProtocolError("Edge Delete response is nil")
	}
	outcomes := response.GetExisted()
	if len(outcomes) != len(refs) {
		return nil, receiptProtocolError("Edge Delete outcome count %d does not match request count %d", len(outcomes), len(refs))
	}
	var deleted int32
	results := make([]EdgeDeleteReceiptResult, len(refs))
	for i, existed := range outcomes {
		if existed {
			deleted++
		}
		results[i] = EdgeDeleteReceiptResult{
			Edge:        refs[i],
			OperationID: operationIDs[i],
			Existed:     existed,
		}
	}
	if response.GetDeleted() != deleted {
		return nil, receiptProtocolError("Edge Delete deleted=%d does not match %d true outcomes", response.GetDeleted(), deleted)
	}
	return results, nil
}
