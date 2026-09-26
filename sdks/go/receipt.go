package client

import (
	"context"
	"fmt"
	"math"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ReceiptState is one exact read-only receipt observation.
type ReceiptState uint8

const (
	ReceiptConfirmed ReceiptState = iota + 1
	ReceiptNotYetObserved
	ReceiptNoLongerProvable
)

// ReceiptOriginalResult is the typed original result stored in a confirmed
// receipt. Its concrete type identifies the mutation family without relying
// on a zero-valued bool or enum.
type ReceiptOriginalResult interface {
	MutationKind() ReceiptMutationKind
	isReceiptOriginalResult()
}

// ReceiptPutVertexResult preserves the original Vertex Put outcome.
type ReceiptPutVertexResult struct {
	Outcome PutOutcome
}

func (ReceiptPutVertexResult) MutationKind() ReceiptMutationKind {
	return ReceiptMutationPutVertex
}
func (ReceiptPutVertexResult) isReceiptOriginalResult() {}

// ReceiptDeleteVertexResult preserves the original Vertex Delete existence
// result.
type ReceiptDeleteVertexResult struct {
	Existed bool
}

func (ReceiptDeleteVertexResult) MutationKind() ReceiptMutationKind {
	return ReceiptMutationDeleteVertex
}
func (ReceiptDeleteVertexResult) isReceiptOriginalResult() {}

// ReceiptDeleteEdgeResult preserves the original Edge Delete existence result.
type ReceiptDeleteEdgeResult struct {
	Existed bool
}

func (ReceiptDeleteEdgeResult) MutationKind() ReceiptMutationKind {
	return ReceiptMutationDeleteEdge
}
func (ReceiptDeleteEdgeResult) isReceiptOriginalResult() {}

// MutationReceipt is one confirmed request-index-aligned mutation result.
type MutationReceipt struct {
	OperationID    ReceiptOperationID
	GroupID        ReceiptGroupID
	ItemIndex      uint32
	ItemCount      uint32
	IntentSHA256   ReceiptIntentSHA256
	Deadline       time.Time
	OriginalResult ReceiptOriginalResult
}

// ReceiptStatus preserves the three-state receipt contract. Receipt is set
// exactly for ReceiptConfirmed.
type ReceiptStatus struct {
	OperationID ReceiptOperationID
	State       ReceiptState
	Receipt     *MutationReceipt
}

// String returns the stable wire-contract name of the state.
func (s ReceiptState) String() string {
	switch s {
	case ReceiptConfirmed:
		return "CONFIRMED"
	case ReceiptNotYetObserved:
		return "NOT_YET_OBSERVED"
	case ReceiptNoLongerProvable:
		return "NO_LONGER_PROVABLE"
	default:
		return fmt.Sprintf("ReceiptState(%d)", s)
	}
}

// GetReceiptCapability returns the endpoint's authenticated receipt
// capability. Disabled is a successful, identity-free result.
func (l *Lantern) GetReceiptCapability(ctx context.Context) (ReceiptCapability, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.GetReceiptCapabilityRequest{}, l.client.GetReceiptCapability)
	if err != nil {
		return ReceiptCapability{}, err
	}
	return receiptCapabilityFromProto(resp)
}

func (l *Lantern) getReceiptCapabilityOnce(ctx context.Context) (ReceiptCapability, error) {
	resp, err := unaryOnce(ctx, &pb.GetReceiptCapabilityRequest{}, l.client.GetReceiptCapability)
	if err != nil {
		return ReceiptCapability{}, err
	}
	return receiptCapabilityFromProto(resp)
}

func receiptCapabilityFromProto(resp *pb.GetReceiptCapabilityResponse) (ReceiptCapability, error) {
	if resp == nil {
		return ReceiptCapability{}, receiptProtocolError("capability response is nil")
	}
	if !resp.GetEnabled() {
		if resp.GetPolicy() != nil || resp.GetEndpoint() != nil ||
			resp.GetServerNowUnixMs() != 0 || len(resp.GetSupportedMutations()) != 0 {
			return ReceiptCapability{}, receiptProtocolError("disabled capability carried receipt identity or policy")
		}
		return ReceiptCapability{}, nil
	}
	policy := resp.GetPolicy()
	endpoint := resp.GetEndpoint()
	if policy == nil || endpoint == nil {
		return ReceiptCapability{}, receiptProtocolError("enabled capability omitted policy or endpoint")
	}
	epoch, err := ReceiptEpochFromBytes(policy.GetDeploymentEpoch())
	if err != nil {
		return ReceiptCapability{}, receiptProtocolError("deployment epoch: %v", err)
	}
	nodeID, err := ReceiptNodeIDFromBytes(endpoint.GetNodeId())
	if err != nil {
		return ReceiptCapability{}, receiptProtocolError("node ID: %v", err)
	}
	generation, err := ReceiptGenerationFromBytes(endpoint.GetGeneration())
	if err != nil {
		return ReceiptCapability{}, receiptProtocolError("generation: %v", err)
	}
	fingerprint, err := ReceiptPolicyFingerprintFromBytes(policy.GetFingerprint())
	if err != nil {
		return ReceiptCapability{}, receiptProtocolError("policy fingerprint: %v", err)
	}
	if policy.GetRetentionMs() == 0 ||
		policy.GetRetentionMs() > uint64(math.MaxInt64/int64(time.Millisecond)) ||
		policy.GetMaxEntries() == 0 || policy.GetMaxBytes() == 0 ||
		resp.GetServerNowUnixMs() == 0 || resp.GetServerNowUnixMs() > math.MaxInt64 {
		return ReceiptCapability{}, receiptProtocolError("enabled capability has invalid policy limits or server time")
	}
	supported := make([]ReceiptMutationKind, len(resp.GetSupportedMutations()))
	for i, raw := range resp.GetSupportedMutations() {
		mutation, err := receiptMutationKindFromProto(raw)
		if err != nil {
			return ReceiptCapability{}, receiptProtocolError("supported_mutations[%d]: %v", i, err)
		}
		supported[i] = mutation
	}
	capability := ReceiptCapability{
		Enabled: true,
		Continuity: ReceiptContinuity{
			Epoch: epoch, NodeID: nodeID, Generation: generation,
		},
		PolicyFingerprint:  fingerprint,
		Retention:          time.Duration(policy.GetRetentionMs()) * time.Millisecond,
		MaxEntries:         policy.GetMaxEntries(),
		MaxBytes:           policy.GetMaxBytes(),
		ServerTime:         time.UnixMilli(int64(resp.GetServerNowUnixMs())).UTC(),
		SupportedMutations: supported,
	}
	if err := capability.Validate(); err != nil {
		return ReceiptCapability{}, receiptProtocolError("%v", err)
	}
	return capability, nil
}

func receiptMutationKindFromProto(raw pb.ReceiptMutationKind) (ReceiptMutationKind, error) {
	switch raw {
	case pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_PUT_VERTEX:
		return ReceiptMutationPutVertex, nil
	case pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_VERTEX:
		return ReceiptMutationDeleteVertex, nil
	case pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_EDGE:
		return ReceiptMutationDeleteEdge, nil
	default:
		return ReceiptMutationUnspecified, fmt.Errorf("unknown receipt mutation kind %d", raw)
	}
}

// GetReceiptStatuses performs a read-only, plural-canonical lookup and
// preserves request-index alignment, including duplicate operation IDs.
func (l *Lantern) GetReceiptStatuses(ctx context.Context, ids []ReceiptOperationID) ([]ReceiptStatus, error) {
	if len(ids) == 0 {
		return nil, invalidReceiptError("at least one operation ID is required")
	}
	rawIDs := make([][]byte, len(ids))
	for i, id := range ids {
		if err := validateReceiptOperationID(id); err != nil {
			return nil, invalidReceiptError("operation_ids[%d]: %v", i, err)
		}
		rawIDs[i] = id.Bytes()
	}
	statuses := make([]ReceiptStatus, 0, len(ids))
	offset := 0
	err := runBatchRead(ctx, l, rawIDs, func(ctx context.Context, chunk [][]byte) error {
		resp, err := unary(ctx, l, &pb.GetReceiptStatusesRequest{OperationIds: chunk}, l.client.GetReceiptStatuses)
		if err != nil {
			return err
		}
		if len(resp.GetStatuses()) != len(chunk) {
			return receiptProtocolError("status count %d does not match request count %d", len(resp.GetStatuses()), len(chunk))
		}
		for i, raw := range resp.GetStatuses() {
			status, err := receiptStatusFromProto(ids[offset+i], raw)
			if err != nil {
				return fmt.Errorf("client: receipt status %d: %w", offset+i, err)
			}
			statuses = append(statuses, status)
		}
		offset += len(chunk)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return statuses, nil
}

// GetReceiptStatus is the one-item facade over GetReceiptStatuses.
func (l *Lantern) GetReceiptStatus(ctx context.Context, id ReceiptOperationID) (ReceiptStatus, error) {
	statuses, err := l.GetReceiptStatuses(ctx, []ReceiptOperationID{id})
	if err != nil {
		return ReceiptStatus{}, err
	}
	if len(statuses) != 1 {
		return ReceiptStatus{}, receiptProtocolError("singular status returned %d items", len(statuses))
	}
	return statuses[0], nil
}

func receiptStatusFromProto(expected ReceiptOperationID, status *pb.ReceiptStatus) (ReceiptStatus, error) {
	if status == nil {
		return ReceiptStatus{}, receiptProtocolError("status is nil")
	}
	operationID, err := ReceiptOperationIDFromBytes(status.GetOperationId())
	if err != nil {
		return ReceiptStatus{}, receiptProtocolError("operation ID: %v", err)
	}
	if operationID != expected {
		return ReceiptStatus{}, receiptProtocolError("status operation ID is not request-index aligned")
	}
	out := ReceiptStatus{OperationID: operationID}
	switch status.GetState() {
	case pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED:
		receipt, err := mutationReceiptFromProto(operationID, status.GetReceipt())
		if err != nil {
			return ReceiptStatus{}, err
		}
		out.State = ReceiptConfirmed
		out.Receipt = receipt
	case pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED:
		if status.GetReceipt() != nil {
			return ReceiptStatus{}, receiptProtocolError("NOT_YET_OBSERVED status carried a receipt")
		}
		out.State = ReceiptNotYetObserved
	case pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE:
		if status.GetReceipt() != nil {
			return ReceiptStatus{}, receiptProtocolError("NO_LONGER_PROVABLE status carried a receipt")
		}
		out.State = ReceiptNoLongerProvable
	default:
		return ReceiptStatus{}, receiptProtocolError("unknown receipt state %d", status.GetState())
	}
	return out, nil
}

func mutationReceiptFromProto(expected ReceiptOperationID, receipt *pb.MutationReceipt) (*MutationReceipt, error) {
	if receipt == nil {
		return nil, receiptProtocolError("CONFIRMED status omitted its receipt")
	}
	operationID, err := ReceiptOperationIDFromBytes(receipt.GetOperationId())
	if err != nil {
		return nil, receiptProtocolError("confirmed operation ID: %v", err)
	}
	if operationID != expected {
		return nil, receiptProtocolError("confirmed receipt operation ID does not match status")
	}
	groupID, err := ReceiptGroupIDFromBytes(receipt.GetLogicalCallId())
	if err != nil {
		return nil, receiptProtocolError("logical-call ID: %v", err)
	}
	if receipt.GetItemCount() == 0 || receipt.GetItemIndex() >= receipt.GetItemCount() {
		return nil, receiptProtocolError("invalid receipt item index/count %d/%d", receipt.GetItemIndex(), receipt.GetItemCount())
	}
	var intent ReceiptIntentSHA256
	if err := copyNonzeroReceiptBytes("intent SHA-256", intent[:], receipt.GetIntentSha256()); err != nil {
		return nil, receiptProtocolError("%v", err)
	}
	if receipt.GetDeadlineUnixMs() == 0 || receipt.GetDeadlineUnixMs() > math.MaxInt64 {
		return nil, receiptProtocolError("invalid receipt deadline %d", receipt.GetDeadlineUnixMs())
	}
	originalResult := receipt.GetOriginalResult()
	if originalResult == nil {
		return nil, receiptProtocolError("confirmed receipt omitted its original result")
	}
	var result ReceiptOriginalResult
	switch typed := originalResult.GetResult().(type) {
	case *pb.ReceiptResult_PutVertexOutcome:
		outcome, err := putOutcomeFromProto(typed.PutVertexOutcome)
		if err != nil {
			return nil, receiptProtocolError("Vertex Put result: %v", err)
		}
		result = ReceiptPutVertexResult{Outcome: outcome}
	case *pb.ReceiptResult_DeleteVertexExisted:
		result = ReceiptDeleteVertexResult{Existed: typed.DeleteVertexExisted}
	case *pb.ReceiptResult_DeleteEdgeExisted:
		result = ReceiptDeleteEdgeResult{Existed: typed.DeleteEdgeExisted}
	default:
		return nil, receiptProtocolError("confirmed receipt has unknown original result")
	}
	return &MutationReceipt{
		OperationID:    operationID,
		GroupID:        groupID,
		ItemIndex:      receipt.GetItemIndex(),
		ItemCount:      receipt.GetItemCount(),
		IntentSHA256:   intent,
		Deadline:       time.UnixMilli(int64(receipt.GetDeadlineUnixMs())).UTC(),
		OriginalResult: result,
	}, nil
}

func receiptContextToProto(context ReceiptContext) *pb.MutationReceiptContext {
	operationIDs := make([][]byte, len(context.OperationIDs))
	for i, id := range context.OperationIDs {
		operationIDs[i] = id.Bytes()
	}
	return &pb.MutationReceiptContext{
		OperationIds:  operationIDs,
		LogicalCallId: context.GroupID.Bytes(),
		Endpoint: &pb.ReceiptEndpoint{
			NodeId:     context.Continuity.NodeID.Bytes(),
			Generation: context.Continuity.Generation.Bytes(),
		},
	}
}
