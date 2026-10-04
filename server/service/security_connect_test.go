package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityConnectAtomicRoleMembershipAndRetry(t *testing.T) {
	handler, sink, contexts := securityAPIFixture(t)
	now := handler.now()
	ctx := contexts("admin", now)
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: "alice"}
	request := &pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{2}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "orders_reader", Name: "Orders reader", Rules: []*pb.SecurityRule{
			{Id: "read", Effect: pb.SecurityEffect_SECURITY_EFFECT_ALLOW, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:"}},
			{Id: "deny", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:private:"}},
		}}}},
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "orders_reader"}}},
	}}
	response, err := handler.ApplySecurityChanges(ctx, connect.NewRequest(request))
	if err != nil || response.Msg.Version.Revision != 2 || len(response.Msg.Applied) != 3 || sink.calls != 2 || response.Msg.Enforcement != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING {
		t.Fatal(response, err, sink.calls)
	}
	current := contexts("admin", now)
	replay, err := handler.ApplySecurityChanges(current, connect.NewRequest(request))
	if err != nil || !replay.Msg.Replayed || sink.calls != 2 {
		t.Fatal("retry recommitted or rejected", replay, err)
	}
	key := "orders:private:1"
	explanation, err := handler.ExplainAccess(current, connect.NewRequest(&pb.ExplainAccessRequest{Identity: identity, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, LogicalKey: &key}))
	if err != nil || explanation.Msg.Allowed || len(explanation.Msg.Matches) != 2 {
		t.Fatal(explanation, err)
	}
	key = "orders:public:1"
	explanation, err = handler.ExplainAccess(current, connect.NewRequest(&pb.ExplainAccessRequest{Identity: identity, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, LogicalKey: &key}))
	if err != nil || !explanation.Msg.Allowed {
		t.Fatal(explanation, err)
	}
	// Role deletion cannot silently remove memberships.
	_, err = handler.ApplySecurityChange(current, connect.NewRequest(&pb.ApplySecurityChangeRequest{ExpectedRevision: 2, ChangeId: bytes.Repeat([]byte{3}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteRole{DeleteRole: "orders_reader"}}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || sink.calls != 2 {
		t.Fatal("in-use role removed", err)
	}
	_, err = handler.ApplySecurityChanges(current, connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: 2, ChangeId: bytes.Repeat([]byte{4}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "orders_reader"}}}, {Operation: &pb.SecurityChange_DeleteRole{DeleteRole: "orders_reader"}}}}))
	if err != nil || sink.calls != 3 {
		t.Fatal("atomic removal failed", err)
	}
}
func TestSecurityConnectVisibilityCursorAndRecentAuth(t *testing.T) {
	handler, sink, contexts := securityAPIFixture(t)
	now := handler.now()
	admin := contexts("admin", now)
	page, err := handler.ListUsers(admin, connect.NewRequest(&pb.ListUsersRequest{Limit: 1}))
	if err != nil || len(page.Msg.Users) != 1 || page.Msg.NextCursor == "" {
		t.Fatal(page, err)
	}
	_, err = handler.ListUsers(contexts("other_admin", now), connect.NewRequest(&pb.ListUsersRequest{Limit: 1, Cursor: page.Msg.NextCursor}))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("cursor stolen", err)
	}
	issuers, err := handler.ListIssuers(admin, connect.NewRequest(&pb.ListIssuersRequest{}))
	if err != nil || issuers.Msg.Issuers[0].SecretRef != nil {
		t.Fatal("secret exposed", err)
	}
	reader := contexts("reader", now)
	if _, err = handler.ListUsers(reader, connect.NewRequest(&pb.ListUsersRequest{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("reader saw users", err)
	}
	current, err := handler.GetCurrentPrincipal(reader, connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
	if err != nil || len(current.Msg.Roles) != 0 || current.Msg.CsrfToken != "" {
		t.Fatal(current, err)
	}
	if _, err = handler.ListUsers(t.Context(), connect.NewRequest(&pb.ListUsersRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("anonymous management", err)
	}
	calls := 0
	handler.validateIssuer = func(context.Context, security.Issuer) error { calls++; return nil }
	_, err = handler.ValidateIssuer(contexts("admin", now.Add(-6*time.Minute)), connect.NewRequest(&pb.ValidateIssuerRequest{Issuer: &pb.SecurityIssuer{Issuer: "https://another.example"}}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || calls != 0 || sink.calls != 1 {
		t.Fatal("stale auth triggered network", err, calls, sink.calls)
	}
	off, err := NewSecurityConnectHandler(SecurityServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	caps, err := off.GetAuthCapabilities(t.Context(), connect.NewRequest(&pb.GetAuthCapabilitiesRequest{}))
	if err != nil || caps.Msg.Mode != pb.AuthMode_AUTH_MODE_OFF || !caps.Msg.Ready || caps.Msg.ProtocolVersion != 1 {
		t.Fatal(caps, err)
	}
	if _, err = off.ListUsers(t.Context(), connect.NewRequest(&pb.ListUsersRequest{})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("OFF granted admin", err)
	}
}

func TestSecurityCapabilitiesNeverConvertUnavailableOIDCToOff(t *testing.T) {
	handler, _, _ := securityAPIFixture(t)
	request := connect.NewRequest(&pb.GetAuthCapabilitiesRequest{})
	handler.ready = nil
	response, err := handler.GetAuthCapabilities(t.Context(), request)
	if err != nil || response.Msg.Mode != pb.AuthMode_AUTH_MODE_OIDC || response.Msg.Ready || response.Msg.ProtocolVersion != 1 {
		t.Fatal("unavailable OIDC became OFF/ready", err)
	}
	handler.ready = func(context.Context, *security.Revision) bool { return true }
	response, err = handler.GetAuthCapabilities(t.Context(), request)
	if err != nil || !response.Msg.Ready || response.Msg.Mode != pb.AuthMode_AUTH_MODE_OIDC {
		t.Fatal("current authority not advertised", err)
	}
}

func TestSecurityConnectGetResourcesAndEnforcementStatus(t *testing.T) {
	handler, _, contexts := securityAPIFixture(t)
	ctx := contexts("admin", handler.now())
	issuer, err := handler.GetIssuer(ctx, connect.NewRequest(&pb.GetIssuerRequest{Issuer: "https://idp.example"}))
	if err != nil || !issuer.Msg.Issuer.HasSecretBinding || issuer.Msg.Issuer.SecretRef != nil {
		t.Fatal(issuer, err)
	}
	role, err := handler.GetRole(ctx, connect.NewRequest(&pb.GetRoleRequest{Id: "security_admin"}))
	if err != nil || role.Msg.Role.Id != "security_admin" {
		t.Fatal(role, err)
	}
	user, err := handler.GetUser(ctx, connect.NewRequest(&pb.GetUserRequest{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: "admin"}}))
	if err != nil || len(user.Msg.User.Assignments) != 1 {
		t.Fatal(user, err)
	}
	id := make([]byte, 16)
	id[0] = 1
	handler.enforced = func(security.ChangeResult) bool { return true }
	status, err := handler.GetSecurityChangeStatus(ctx, connect.NewRequest(&pb.GetSecurityChangeStatusRequest{ChangeId: id}))
	if err != nil || status.Msg.Enforcement != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED || status.Msg.Version.Revision != 1 {
		t.Fatal(status, err)
	}
	id[0] = 2
	_, err = handler.GetSecurityChangeStatus(ctx, connect.NewRequest(&pb.GetSecurityChangeStatusRequest{ChangeId: id}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("unknown commit returned rollback proof", err)
	}
}
