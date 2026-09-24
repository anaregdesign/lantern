package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestReceiptReadSurfaceDisabled(t *testing.T) {
	svc := NewLanternService(nil)
	capability, err := svc.GetReceiptCapability(context.Background(), &pb.GetReceiptCapabilityRequest{})
	if err != nil || capability.GetEnabled() || capability.GetPolicy() != nil || capability.GetEndpoint() != nil {
		t.Fatalf("disabled capability = (%v, %v)", capability, err)
	}
	if _, err := svc.GetReceiptStatus(context.Background(), &pb.GetReceiptStatusRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("singular disabled status = %v, want FailedPrecondition", err)
	}
	if _, err := svc.GetReceiptStatuses(context.Background(), &pb.GetReceiptStatusesRequest{}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("plural disabled status = %v, want FailedPrecondition", err)
	}
}

func TestReceiptReadSurfaceCancellation(t *testing.T) {
	svc := NewLanternService(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.GetReceiptCapability(ctx, &pb.GetReceiptCapabilityRequest{}); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled capability = %v, want Canceled", err)
	}
	if _, err := svc.GetReceiptStatuses(ctx, &pb.GetReceiptStatusesRequest{}); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled status = %v, want Canceled", err)
	}
}
