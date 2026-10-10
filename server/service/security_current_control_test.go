package service

import (
	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"testing"
)

func TestCurrentControlRefusesVersionFieldsOnLegacySurface(t *testing.T) {
	h, err := NewSecurityConnectHandler(SecurityServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_, err = h.ApplySecurityChanges(ctx, connect.NewRequest(&pb.ApplySecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("current Apply reached legacy/OFF", err)
	}
	_, err = h.PrepareSecurityChanges(ctx, connect.NewRequest(&pb.PrepareSecurityChangesRequest{CurrentReview: &pb.CurrentSecurityReview{}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("current prepare reached legacy/OFF", err)
	}
	_, err = h.GetSecurityChangeStatus(ctx, connect.NewRequest(&pb.GetSecurityChangeStatusRequest{CurrentProfile: &pb.CurrentAuthorityProfile{Version: 2}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("current status reached legacy/OFF", err)
	}
	if _, err := NewSecurityConnectHandler(SecurityServiceOptions{Current: &security.CurrentAuthority{}}); err == nil {
		t.Fatal("zero current facade became enabled service")
	}
}
