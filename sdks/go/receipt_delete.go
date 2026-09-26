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
	stableRefs := append([]EdgeRef(nil), refs...)
	request, stableContext, err := receiptDeleteRequest(stableRefs, receiptContext)
	if err != nil {
		return nil, err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()

	var response *pb.DeleteEdgesResponse
	err = l.executeReceiptMutation(ctx, ReceiptMutationDeleteEdge, stableContext.Continuity, func(ctx context.Context) error {
		resp, err := unaryOnce(ctx, request, l.client.DeleteEdges)
		if err == nil {
			response = resp
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return edgeDeleteReceiptResults(stableRefs, stableContext.OperationIDs, response)
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
	if receiptContext.Mutation != ReceiptMutationDeleteEdge {
		return nil, ReceiptContext{}, invalidReceiptError(
			"receipt context mutation is %s, want %s",
			receiptContext.Mutation,
			ReceiptMutationDeleteEdge,
		)
	}
	stableContext := receiptContext.Clone()
	keys := make([]*pb.EdgeKey, len(refs))
	for i, ref := range refs {
		if ref.Tail == "" || ref.Head == "" || !utf8.ValidString(ref.Tail) || !utf8.ValidString(ref.Head) {
			return nil, ReceiptContext{}, invalidReceiptError("edges[%d] must have nonempty UTF-8 tail and head", i)
		}
		keys[i] = &pb.EdgeKey{Tail: ref.Tail, Head: ref.Head}
	}
	return &pb.DeleteEdgesRequest{
		Edges:          keys,
		ReceiptContext: receiptContextToProto(stableContext),
	}, stableContext, nil
}

func (l *Lantern) executeReceiptMutation(
	ctx context.Context,
	mutation ReceiptMutationKind,
	expected ReceiptContinuity,
	send func(context.Context) error,
) error {
	attempt := func() error {
		if err := l.requireReceiptCapabilityOnce(ctx, expected, mutation); err != nil {
			return err
		}
		err := send(ctx)
		if err != nil && errors.Is(err, ErrFailedPrecondition) {
			if capabilityErr := l.recheckReceiptCapability(ctx, expected, mutation); capabilityErr != nil {
				return capabilityErr
			}
		}
		return err
	}
	if l != nil && l.opts.retry != nil {
		return l.opts.retry.run(ctx, attempt)
	}
	return attempt()
}

func (l *Lantern) requireReceiptCapabilityOnce(
	ctx context.Context,
	expected ReceiptContinuity,
	mutation ReceiptMutationKind,
) error {
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
	if !capability.Supports(mutation) {
		observed := capability.Continuity
		return &ReceiptReconciliationError{
			Expected: expected,
			Observed: &observed,
			Cause: errors.Join(
				ErrFailedPrecondition,
				ErrReceiptMutationUnsupported,
			),
		}
	}
	return nil
}

func (l *Lantern) recheckReceiptCapability(
	ctx context.Context,
	expected ReceiptContinuity,
	mutation ReceiptMutationKind,
) error {
	err := l.requireReceiptCapabilityOnce(ctx, expected, mutation)
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
