package securitytransport

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityConnectOffCapabilitiesAndManagement(t *testing.T) {
	for _, ready := range []bool{false, true} {
		h, err := NewSecurityConnectHandler(SecurityServiceOptions{Ready: func(context.Context) bool { return ready }})
		if err != nil {
			t.Fatal(err)
		}
		caps, err := h.GetAuthCapabilities(t.Context(), connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
		if err != nil || caps.Msg.Mode != pb.AuthMode_AUTH_MODE_OFF || caps.Msg.Ready != ready || caps.Msg.ProtocolVersion != 1 {
			t.Fatal(caps, err)
		}
		if _, err := h.ListUsers(t.Context(), connect.NewRequest(&pb.ListUsersRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal("OFF invented management authority", err)
		}
	}
	if _, err := NewSecurityConnectHandler(SecurityServiceOptions{Current: &security.CurrentAuthority{}}); err == nil {
		t.Fatal("uninstalled OIDC became configured authority")
	}
}
