package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

var errReceiptsDisabled = errors.New("mutation receipts are not enabled on this server")

// GetReceiptCapability is the preflight for future receipt writes. It follows
// the configured LanternService auth policy. Until graph, receipt, WAL,
// recovery, and Snapshot state share one certified publication boundary, it
// never advertises an epoch or endpoint marker. In particular, a process-local
// receipt map is not a capability.
func (s *LanternService) GetReceiptCapability(ctx context.Context, _ *pb.GetReceiptCapabilityRequest) (*pb.GetReceiptCapabilityResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	return &pb.GetReceiptCapabilityResponse{}, nil
}

// GetReceiptStatuses is the canonical read-only status path. No receipt
// engine is wired yet, so returning NOT_YET_OBSERVED or NO_LONGER_PROVABLE
// would falsely describe an operation this server cannot account for.
func (s *LanternService) GetReceiptStatuses(ctx context.Context, _ *pb.GetReceiptStatusesRequest) (*pb.GetReceiptStatusesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	return nil, connect.NewError(connect.CodeFailedPrecondition, errReceiptsDisabled)
}

// GetReceiptStatus forwards one item to the plural implementation, preserving
// its fail-closed behavior until the receipt engine is enabled.
func (s *LanternService) GetReceiptStatus(ctx context.Context, req *pb.GetReceiptStatusRequest) (*pb.GetReceiptStatusResponse, error) {
	if req == nil {
		req = &pb.GetReceiptStatusRequest{}
	}
	resp, err := s.GetReceiptStatuses(ctx, &pb.GetReceiptStatusesRequest{OperationIds: [][]byte{req.GetOperationId()}})
	if err != nil {
		return nil, err
	}
	if len(resp.GetStatuses()) != 1 || resp.GetStatuses()[0] == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("receipt status result length mismatch"))
	}
	return &pb.GetReceiptStatusResponse{Status: resp.GetStatuses()[0]}, nil
}
