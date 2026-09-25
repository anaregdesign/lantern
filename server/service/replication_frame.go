package service

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type replicationFrameSizeError struct {
	size  int
	limit int
}

func (e *replicationFrameSizeError) Error() string {
	return fmt.Sprintf(
		"replication SubscribeResponse size %d exceeds LANTERN_MAX_SEND_MSG_BYTES=%d",
		e.size,
		e.limit,
	)
}

// subscribeMutationFrame is the sole full-mutation projection used by both
// admission and Subscribe. Receipt envelopes must take their richer wire path
// before the graph-only fallback so a peer cannot advance without receipts.
func subscribeMutationFrame(op mutationlog.MutationOp) (*pb.SubscribeResponse, error) {
	var mutation *pb.Mutation
	switch value := op.(type) {
	case interface{ ReplicationMutation() (*pb.Mutation, error) }:
		var err error
		mutation, err = value.ReplicationMutation()
		if err != nil {
			return nil, err
		}
	case *pb.Mutation:
		mutation = value
	case interface{ GraphMutation() *pb.Mutation }:
		mutation = value.GraphMutation()
	default:
		return nil, fmt.Errorf("unsupported mutation log payload %T", op)
	}
	if mutation == nil {
		return nil, fmt.Errorf("mutation log payload %T has no Subscribe projection", op)
	}
	return &pb.SubscribeResponse{
		Event: &pb.SubscribeResponse_Mutation{Mutation: mutation},
	}, nil
}

func graphMutationFromLog(op mutationlog.MutationOp) (*pb.Mutation, bool) {
	frame, err := subscribeMutationFrame(op)
	if err != nil {
		return nil, false
	}
	return frame.GetMutation(), true
}

func validateReplicationFrameSize(op mutationlog.MutationOp, limit int) (int, error) {
	frame, err := subscribeMutationFrame(op)
	if err != nil {
		return 0, err
	}
	size := proto.Size(frame)
	if limit > 0 && size > limit {
		return size, &replicationFrameSizeError{size: size, limit: limit}
	}
	return size, nil
}

// Receiver-local causal admission can add receipt effects to the frame a
// follower retains. Size the largest valid relay of the same receipt evidence,
// not just the current node's accepted subset.
func maximalReplicationRelayEnvelope(op mutationlog.MutationOp) (mutationlog.MutationOp, error) {
	switch envelope := op.(type) {
	case *edgeDeleteReceiptEnvelope:
		if envelope == nil {
			return nil, errors.New("nil receipt Edge Delete envelope")
		}
		return maximalReceiptEdgeDeleteEnvelope(envelope), nil
	case *vertexDeleteReceiptEnvelope:
		if envelope == nil {
			return nil, errors.New("nil receipt Vertex Delete envelope")
		}
		return maximalReceiptVertexDeleteEnvelope(envelope), nil
	case *vertexPutReceiptEnvelope:
		if envelope == nil {
			return nil, errors.New("nil receipt Vertex Put envelope")
		}
		return maximalReceiptVertexPutEnvelope(envelope)
	default:
		return op, nil
	}
}

func validateReplicationRelayFrameSize(op mutationlog.MutationOp, limit int) (int, error) {
	maximal, err := maximalReplicationRelayEnvelope(op)
	if err != nil {
		return 0, fmt.Errorf("maximal receipt relay projection: %w", err)
	}
	return validateReplicationFrameSize(maximal, limit)
}

func (s *LanternService) replicationFrameCapacityError(err error) error {
	if s.onValidationReject != nil {
		s.onValidationReject("replication_frame")
	}
	return connect.NewError(connect.CodeResourceExhausted, err)
}

func (s *LanternService) validateReplicationFrame(op mutationlog.MutationOp) error {
	limit := 0
	if s.replicationFrameCertified {
		limit = s.replicationSendMaxBytes
	}
	if _, err := validateReplicationFrameSize(op, limit); err != nil {
		var sizeErr *replicationFrameSizeError
		if errors.As(err, &sizeErr) ||
			errors.Is(err, errReceiptEdgeDeleteWireCapacity) ||
			errors.Is(err, errReceiptVertexDeleteWireCapacity) ||
			errors.Is(err, errReceiptVertexPutWireCapacity) {
			return s.replicationFrameCapacityError(err)
		}
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("replication Subscribe projection: %w", err))
	}
	return nil
}

func (s *LanternService) validateReplicationRelayFrame(op mutationlog.MutationOp) error {
	maximal, err := maximalReplicationRelayEnvelope(op)
	if err != nil {
		return connect.NewError(connect.CodeInternal,
			fmt.Errorf("maximal receipt relay projection: %w", err))
	}
	return s.validateReplicationFrame(maximal)
}

func publicationShapeError(scope string, err error) error {
	code := connect.CodeOf(err)
	if code == connect.CodeUnknown {
		code = connect.CodeInvalidArgument
	}
	return connect.NewError(code, fmt.Errorf("%s publication shape: %w", scope, err))
}
