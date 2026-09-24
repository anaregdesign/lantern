package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

func authedReceiptRequest[T any](msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+testToken)
	return req
}

// TestReceiptReadSurface_RealConnectWire keeps the dormant capability/status
// surface honest: authentication precedes receipt inspection, and a server
// without an atomic receipt engine cannot fabricate an absent result.
func TestReceiptReadSurface_RealConnectWire(t *testing.T) {
	srv, _, _ := newAuthedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	if _, err := raw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless capability = %v, want Unauthenticated", err)
	}
	if _, err := raw.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless status = %v, want Unauthenticated", err)
	}

	capability, err := raw.GetReceiptCapability(ctx, authedReceiptRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || capability.Msg.GetEnabled() || capability.Msg.GetPolicy() != nil || capability.Msg.GetEndpoint() != nil || capability.Msg.GetServerNowUnixMs() != 0 {
		t.Fatalf("authenticated disabled capability = (%v, %v)", capability, err)
	}
	operationID := make([]byte, 49)
	for i := range operationID {
		operationID[i] = byte(i + 1)
	}
	if _, err := raw.GetReceiptStatus(ctx, authedReceiptRequest(&pb.GetReceiptStatusRequest{OperationId: operationID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled singular status = %v, want FailedPrecondition", err)
	}
	if _, err := raw.GetReceiptStatuses(ctx, authedReceiptRequest(&pb.GetReceiptStatusesRequest{OperationIds: [][]byte{operationID}})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled plural status = %v, want FailedPrecondition", err)
	}

	// Auth can be disabled for ordinary Lantern deployments. The dormant
	// preflight still cannot expose a continuity marker in that configuration.
	open := newConnectTestServer(t, service.NewLanternService(nil), nil)
	openRaw := graphv1connect.NewLanternServiceClient(h2cClient(), open.url)
	openCapability, err := openRaw.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || openCapability.Msg.GetEnabled() || openCapability.Msg.GetPolicy() != nil || openCapability.Msg.GetEndpoint() != nil || openCapability.Msg.GetServerNowUnixMs() != 0 {
		t.Fatalf("unauthenticated deployment capability = (%v, %v)", openCapability, err)
	}
	if _, err := openRaw.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{OperationId: operationID})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unauthenticated deployment status = %v, want FailedPrecondition", err)
	}
}
