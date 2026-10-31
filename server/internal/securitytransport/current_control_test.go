package securitytransport

import (
	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"testing"
)

func TestCurrentControlRequiresAuthorityForManagement(t *testing.T) {
	h, err := NewSecurityConnectHandler(SecurityServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_, err = h.ApplySecurityChanges(ctx, connect.NewRequest(&pb.ApplySecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("Apply reached disabled management", err)
	}
	_, err = h.PrepareSecurityChanges(ctx, connect.NewRequest(&pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("prepare reached disabled management", err)
	}
	_, err = h.GetSecurityChangeStatus(ctx, connect.NewRequest(&pb.GetSecurityChangeStatusRequest{CurrentProfile: &pb.CurrentAuthorityProfile{Version: 2}, CurrentIntentDigest: make([]byte, 32)}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("status reached disabled management", err)
	}
	if _, err := NewSecurityConnectHandler(SecurityServiceOptions{Current: &security.CurrentAuthority{}}); err == nil {
		t.Fatal("zero current facade became enabled service")
	}
}
