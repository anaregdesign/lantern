package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// MaxReceiptStatusBatchSize is the handler-private ceiling for status
// amplification. Production validation may impose a lower configured batch
// limit, but no interceptor configuration can raise this bound.
const MaxReceiptStatusBatchSize = 10_000

var (
	errReceiptsDisabled   = errors.New("mutation receipts are not enabled on this server")
	errReceiptsRecovering = errors.New("mutation receipt recovery or Snapshot installation is in progress")
)

func invalidReceiptRequest(err error) error {
	return connect.NewError(connect.CodeInvalidArgument, err)
}

func (s *LanternService) publicReceiptRuntime() *receiptServingRuntime {
	if s == nil || s.runtime == nil || s.runtime.closed.Load() ||
		s.runtime.receipt == nil || !s.runtime.receipt.publicEnabled.Load() {
		return nil
	}
	runtime := s.runtime.receipt
	edgeAdd := s.receiptEdgeAddCoordinator
	edgeDelete := s.receiptEdgeDeleteCoordinator
	vertexPut := s.receiptVertexPutCoordinator
	vertexDelete := s.receiptVertexDeleteCoordinator
	if runtime.store == nil || runtime.retired == nil ||
		s.receiptStore != runtime.store ||
		edgeAdd == nil || edgeAdd.service != s ||
		edgeAdd.cache != s.runtime.graph || edgeAdd.store != runtime.store ||
		edgeDelete == nil || edgeDelete.service != s ||
		edgeDelete.cache != s.runtime.graph || edgeDelete.store != runtime.store ||
		vertexPut == nil || vertexPut.service != s ||
		vertexPut.cache != s.runtime.graph || vertexPut.store != runtime.store ||
		vertexDelete == nil || vertexDelete.service != s ||
		vertexDelete.cache != s.runtime.graph || vertexDelete.store != runtime.store {
		return nil
	}
	return runtime
}

func (s *LanternService) acquirePublicReceiptRuntime() (*receiptServingRuntime, func(), error) {
	runtime := s.publicReceiptRuntime()
	if runtime == nil || runtime.operationAdmission == nil {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition, errReceiptsDisabled)
	}
	release, ok := runtime.operationAdmission.tryAcquireShared()
	if !ok {
		return nil, nil, connect.NewError(connect.CodeFailedPrecondition, errReceiptsRecovering)
	}
	return runtime, release, nil
}

func (s *LanternService) validatePublicReceiptContext(
	runtime *receiptServingRuntime,
	receiptContext *pb.MutationReceiptContext,
	itemCount int,
	itemName string,
) (mutationreceipt.GroupID, []mutationreceipt.ID, error) {
	if receiptContext == nil || receiptContext.GetEndpoint() == nil {
		return mutationreceipt.GroupID{}, nil,
			invalidReceiptRequest(errors.New("receipt context and endpoint are required"))
	}
	rawIDs := receiptContext.GetOperationIds()
	if len(rawIDs) != itemCount || len(rawIDs) == 0 {
		return mutationreceipt.GroupID{}, nil, invalidReceiptRequest(fmt.Errorf(
			"receipt operation IDs must be nonempty and index-aligned with %s", itemName,
		))
	}
	group, err := mutationreceipt.DecodeGroupID(receiptContext.GetLogicalCallId())
	if err != nil {
		return mutationreceipt.GroupID{}, nil, invalidReceiptRequest(err)
	}
	endpoint := receiptContext.GetEndpoint()
	nodeID := s.clock.NodeID()
	if len(endpoint.GetNodeId()) != len(nodeID) ||
		len(endpoint.GetGeneration()) != len(runtime.generation) ||
		!bytes.Equal(endpoint.GetNodeId(), nodeID[:]) ||
		!bytes.Equal(endpoint.GetGeneration(), runtime.generation[:]) {
		return mutationreceipt.GroupID{}, nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("receipt endpoint does not match the active certified generation"))
	}
	ids := make([]mutationreceipt.ID, len(rawIDs))
	seen := make(map[mutationreceipt.ID]struct{}, len(rawIDs))
	for i, rawID := range rawIDs {
		id, err := mutationreceipt.DecodeID(rawID)
		if err != nil {
			return mutationreceipt.GroupID{}, nil,
				invalidReceiptRequest(fmt.Errorf("operation_ids[%d]: %w", i, err))
		}
		epoch, err := id.Epoch()
		if err != nil {
			return mutationreceipt.GroupID{}, nil,
				invalidReceiptRequest(fmt.Errorf("operation_ids[%d]: %w", i, err))
		}
		if epoch != runtime.epoch {
			return mutationreceipt.GroupID{}, nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("operation_ids[%d] is outside the active receipt epoch", i))
		}
		if _, duplicate := seen[id]; duplicate {
			return mutationreceipt.GroupID{}, nil,
				invalidReceiptRequest(fmt.Errorf("operation_ids[%d] duplicates an earlier item", i))
		}
		seen[id] = struct{}{}
		ids[i] = id
	}
	return group, ids, nil
}

// GetReceiptCapability samples the same persisted monotonic clock used for
// receipt admission. A disabled, recovering, closed, faulted, or otherwise
// uncertified runtime returns enabled=false without identity-bearing fields.
func (s *LanternService) GetReceiptCapability(ctx context.Context, req *pb.GetReceiptCapabilityRequest) (*pb.GetReceiptCapabilityResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if req == nil {
		req = &pb.GetReceiptCapabilityRequest{}
	}
	if err := rejectProtoUnknownFields(req.ProtoReflect()); err != nil {
		return nil, invalidReceiptRequest(err)
	}
	runtime, release, err := s.acquirePublicReceiptRuntime()
	if err != nil {
		return &pb.GetReceiptCapabilityResponse{}, nil
	}
	defer release()

	var effective time.Time
	err = s.withCommittedView(func() error {
		var observeErr error
		effective, _, observeErr = runtime.store.ObserveMany(nil, time.Now())
		return observeErr
	})
	if err != nil || effective.UnixMilli() < 0 {
		return &pb.GetReceiptCapabilityResponse{}, nil
	}
	nodeID := s.clock.NodeID()
	if nodeID == ([16]byte{}) || runtime.generation == ([16]byte{}) {
		return &pb.GetReceiptCapabilityResponse{}, nil
	}
	fingerprint := runtime.store.PolicyFingerprint()
	return &pb.GetReceiptCapabilityResponse{
		Enabled: true,
		Policy: &pb.ReceiptPolicy{
			DeploymentEpoch: append([]byte(nil), runtime.epoch[:]...),
			Fingerprint:     append([]byte(nil), fingerprint[:]...),
			RetentionMs:     uint64(runtime.policy.Retention / time.Millisecond),
			MaxEntries:      uint64(runtime.policy.MaxEntries),
			MaxBytes:        runtime.policy.MaxBytes,
		},
		Endpoint: &pb.ReceiptEndpoint{
			NodeId:     append([]byte(nil), nodeID[:]...),
			Generation: append([]byte(nil), runtime.generation[:]...),
		},
		ServerNowUnixMs: uint64(effective.UnixMilli()),
		SupportedMutations: []pb.ReceiptMutationKind{
			pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_PUT_VERTEX,
			pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_VERTEX,
			pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_EDGE,
			pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_ADD_EDGE,
		},
	}, nil
}

// GetReceiptStatuses is the plural-canonical read-only status path. All IDs
// are decoded before the committed view is sampled; status never executes a
// graph mutation.
func (s *LanternService) GetReceiptStatuses(ctx context.Context, req *pb.GetReceiptStatusesRequest) (*pb.GetReceiptStatusesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	if req == nil {
		req = &pb.GetReceiptStatusesRequest{}
	}
	if err := rejectProtoUnknownFields(req.ProtoReflect()); err != nil {
		return nil, invalidReceiptRequest(err)
	}
	rawIDs := req.GetOperationIds()
	if len(rawIDs) == 0 || len(rawIDs) > MaxReceiptStatusBatchSize {
		return nil, invalidReceiptRequest(mutationreceipt.ErrInvalidBatch)
	}
	ids := make([]mutationreceipt.ID, len(rawIDs))
	for i, raw := range rawIDs {
		id, err := mutationreceipt.DecodeID(raw)
		if err != nil {
			return nil, invalidReceiptRequest(fmt.Errorf("operation_ids[%d]: %w", i, err))
		}
		ids[i] = id
	}

	runtime, release, err := s.acquirePublicReceiptRuntime()
	if err != nil {
		return nil, err
	}
	defer release()

	var observations []mutationreceipt.Observation
	err = s.withCommittedView(func() error {
		effective, storeObservations, err := runtime.store.ObserveMany(ids, time.Now())
		if err != nil {
			return err
		}
		observations = storeObservations
		retiredIDs := make([]mutationreceipt.ID, 0, len(ids))
		retiredIndexes := make([]int, 0, len(ids))
		for i, id := range ids {
			epoch, err := id.Epoch()
			if err != nil {
				return err
			}
			if epoch != runtime.epoch {
				retiredIDs = append(retiredIDs, id)
				retiredIndexes = append(retiredIndexes, i)
			}
		}
		if len(retiredIDs) == 0 {
			return nil
		}
		retired, err := runtime.retired.lookupMany(runtime.policy, retiredIDs, effective)
		if err != nil {
			return err
		}
		for i, observation := range retired {
			observations[retiredIndexes[i]] = observation
		}
		return nil
	})
	if err != nil {
		return nil, receiptLookupError(err)
	}

	statuses := make([]*pb.ReceiptStatus, len(ids))
	var noLongerProvable uint64
	for i, observation := range observations {
		status, err := receiptStatusProto(ids[i], observation)
		if err != nil {
			return nil, err
		}
		if observation.Status == mutationreceipt.NoLongerProvable {
			noLongerProvable++
		}
		statuses[i] = status
	}
	runtime.noLongerProvableLookups.Add(noLongerProvable)
	return &pb.GetReceiptStatusesResponse{Statuses: statuses}, nil
}

func receiptLookupError(err error) error {
	switch {
	case errors.Is(err, mutationreceipt.ErrInvalidID),
		errors.Is(err, mutationreceipt.ErrActiveEpochReceipt):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, mutationreceipt.ErrInvalidClock),
		errors.Is(err, mutationreceipt.ErrRetiredCatalogClockRollback):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func receiptStatusProto(id mutationreceipt.ID, observation mutationreceipt.Observation) (*pb.ReceiptStatus, error) {
	status := &pb.ReceiptStatus{OperationId: id.Bytes()}
	switch observation.Status {
	case mutationreceipt.Confirmed:
		receipt := observation.Receipt
		if receipt.ID != id || receipt.Group == (mutationreceipt.GroupID{}) ||
			receipt.Count == 0 || receipt.Index >= receipt.Count ||
			receipt.DeadlineMillis < 0 {
			return nil, connect.NewError(connect.CodeInternal, errors.New("confirmed mutation receipt is invalid"))
		}
		var result *pb.ReceiptResult
		switch receipt.Kind {
		case mutationreceipt.PutVertex:
			if len(receipt.Result) != 1 {
				return nil, connect.NewError(connect.CodeInternal, errors.New("confirmed Vertex Put receipt result is invalid"))
			}
			outcome := pb.PutOutcome(receipt.Result[0])
			if outcome < pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE ||
				outcome > pb.PutOutcome_PUT_OUTCOME_SUPERSEDED {
				return nil, connect.NewError(connect.CodeInternal, errors.New("confirmed Vertex Put receipt result is invalid"))
			}
			result = &pb.ReceiptResult{
				Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: outcome},
			}
		case mutationreceipt.DeleteVertex:
			if len(receipt.Result) != 1 || receipt.Result[0] > 1 {
				return nil, connect.NewError(connect.CodeInternal, errors.New("confirmed Vertex Delete receipt result is invalid"))
			}
			result = &pb.ReceiptResult{
				Result: &pb.ReceiptResult_DeleteVertexExisted{DeleteVertexExisted: receipt.Result[0] == 1},
			}
		case mutationreceipt.DeleteEdge:
			if len(receipt.Result) != 1 || receipt.Result[0] > 1 {
				return nil, connect.NewError(connect.CodeInternal, errors.New("confirmed Edge Delete receipt result is invalid"))
			}
			result = &pb.ReceiptResult{
				Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: receipt.Result[0] == 1},
			}
		case mutationreceipt.AddEdge:
			if len(receipt.Result) != 4 {
				return nil, connect.NewError(connect.CodeInternal, errors.New("confirmed Edge Add receipt result is invalid"))
			}
			result = &pb.ReceiptResult{
				Result: &pb.ReceiptResult_AddEdgeEffectiveWeight{
					AddEdgeEffectiveWeight: math.Float32frombits(binary.BigEndian.Uint32(receipt.Result)),
				},
			}
		default:
			return nil, connect.NewError(connect.CodeInternal, errors.New("confirmed mutation receipt kind is not public"))
		}
		status.State = pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED
		status.Receipt = &pb.MutationReceipt{
			OperationId:    receipt.ID.Bytes(),
			LogicalCallId:  append([]byte(nil), receipt.Group[:]...),
			ItemIndex:      receipt.Index,
			ItemCount:      receipt.Count,
			IntentSha256:   append([]byte(nil), receipt.Digest[:]...),
			DeadlineUnixMs: uint64(receipt.DeadlineMillis),
			OriginalResult: result,
		}
	case mutationreceipt.NotYetObserved:
		status.State = pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED
	case mutationreceipt.NoLongerProvable:
		status.State = pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE
	default:
		return nil, connect.NewError(connect.CodeInternal, errors.New("receipt Store returned an unknown status"))
	}
	return status, nil
}

// GetReceiptStatus forwards exactly one item to the plural implementation.
func (s *LanternService) GetReceiptStatus(ctx context.Context, req *pb.GetReceiptStatusRequest) (*pb.GetReceiptStatusResponse, error) {
	if req == nil {
		req = &pb.GetReceiptStatusRequest{}
	}
	if err := rejectProtoUnknownFields(req.ProtoReflect()); err != nil {
		return nil, invalidReceiptRequest(err)
	}
	resp, err := s.GetReceiptStatuses(ctx, &pb.GetReceiptStatusesRequest{
		OperationIds: [][]byte{req.GetOperationId()},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.GetStatuses()) != 1 || resp.GetStatuses()[0] == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("receipt status result length mismatch"))
	}
	return &pb.GetReceiptStatusResponse{Status: resp.GetStatuses()[0]}, nil
}
