package securitytransport

import (
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestSecurityAuthorizationRequiresInstalledReviewOwner(t *testing.T) {
	h, _ := NewSecurityConnectHandler(SecurityServiceOptions{})
	_, err := h.PrepareSecurityChanges(t.Context(), connect.NewRequest(&pb.PrepareSecurityChangesRequest{}))
	if err == nil {
		t.Fatal("absent review reached Prepare")
	}
	_, err = h.BeginSecurityChangeAuthorization(t.Context(), connect.NewRequest(&pb.BeginSecurityChangeAuthorizationRequest{}))
	if err == nil {
		t.Fatal("absent review reached authorization")
	}
	_, err = h.GetSecurityChangeAuthorization(t.Context(), connect.NewRequest(&pb.GetSecurityChangeAuthorizationRequest{}))
	if err == nil {
		t.Fatal("absent original reached status")
	}
}
