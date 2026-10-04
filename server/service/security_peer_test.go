package service

import (
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestSecurityPeerRefusesRequestsWithoutWorkloadAdmission(t *testing.T) {
	handler := NewSecurityPeerConnectHandler(nil)
	request := connect.NewRequest(&pb.RenewPolicyLeaseRequest{Receiver: make([]byte, 16), BootNonce: make([]byte, 16), Challenge: make([]byte, 32)})
	request.Header().Set("Authorization", "Bearer human-or-machine")
	if _, err := handler.RenewPolicyLease(t.Context(), request); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("public credential admitted to lease authority", err)
	}
}
