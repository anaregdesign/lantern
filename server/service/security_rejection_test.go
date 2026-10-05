package service

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"errors"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"testing"
	"time"
)

func TestSecurityPrecommitRejectionPluralSingular(t *testing.T) {
	for _, singular := range []bool{false, true} {
		t.Run(map[bool]string{false: "plural", true: "singular"}[singular], func(t *testing.T) {
			for _, test := range []struct {
				name     string
				change   *pb.SecurityChange
				expected uint64
				reason   pb.SecurityChangeRejectionReason
			}{
				{"malformed", &pb.SecurityChange{}, 1, pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_INVALID_CHANGES},
				{"unknown role", &pb.SecurityChange{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: "reader"}, RoleId: "unknown"}}}, 1, pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_UNKNOWN_ROLE},
				{"environment owned", &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: "admin"}, RoleId: "security_admin"}}}, 1, pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_ENVIRONMENT_OWNED},
				{"revision", &pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "ordinary"}}}, 2, pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_REVISION_CONFLICT},
			} {
				t.Run(test.name, func(t *testing.T) {
					handler, sink, contexts := securityAPIFixture(t)
					id := bytes.Repeat([]byte{10}, 16)
					var err error
					if singular {
						_, err = handler.ApplySecurityChange(contexts("admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangeRequest{ExpectedRevision: test.expected, ChangeId: id, Change: test.change}))
					} else {
						_, err = handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: test.expected, ChangeId: id, Changes: []*pb.SecurityChange{test.change}}))
					}
					var ce *connect.Error
					if !errors.As(err, &ce) || len(ce.Details()) != 1 || sink.calls != 1 {
						t.Fatal("not a definite local refusal", err, sink.calls)
					}
					value, err := ce.Details()[0].Value()
					if err != nil {
						t.Fatal(err)
					}
					detail, ok := value.(*pb.SecurityChangePrecommitRejected)
					if !ok || !bytes.Equal(detail.ChangeId, id) || detail.ExpectedRevision != test.expected || detail.Reason != test.reason {
						t.Fatal("unbound refusal", value)
					}
				})
			}
		})
	}
}
func TestSecurityPrecommitRejectionCannotSettlePersistenceOrRetainedIntent(t *testing.T) {
	for _, poison := range []error{security.ErrUnknownRole, &security.ManagementRejection{Reason: security.RejectInvalidChanges, Cause: security.ErrInvalidImage}} {
		t.Run(poison.Error(), func(t *testing.T) {
			handler, sink, contexts := securityAPIFixture(t)
			sink.err = poison
			_, err := handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{12}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "ordinary"}}}}}))
			var ce *connect.Error
			if !errors.As(err, &ce) || ce.Code() != connect.CodeUnavailable || len(ce.Details()) != 0 || sink.calls != 2 {
				t.Fatal("post-persistence cause claimed noncommit", err)
			}
		})
	}
	handler, sink, contexts := securityAPIFixture(t)
	request := &pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{13}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "ordinary"}}}}}
	if _, err := handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(request)); err != nil {
		t.Fatal(err)
	}
	for _, change := range []*pb.SecurityChange{{}, {Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "changed"}}}} {
		request.Changes = []*pb.SecurityChange{change}
		_, err := handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(request))
		var ce *connect.Error
		if !errors.As(err, &ce) || len(ce.Details()) != 0 || sink.calls != 2 {
			t.Fatal("retained ID was unlocked", err)
		}
	}
}
func TestSecurityPrecommitIssuerValidation(t *testing.T) {
	handler, sink, contexts := securityAPIFixture(t)
	handler.verifyAuthorization = func([]byte, security.ManagementBinding, time.Time) error { return nil }
	handler.validateIssuer = func(context.Context, security.Issuer) error { return errors.New("synthetic discovery unavailable") }
	_, err := handler.ApplySecurityChange(contexts("admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangeRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{14}, 16), Change: &pb.SecurityChange{Operation: &pb.SecurityChange_PutIssuer{PutIssuer: &pb.SecurityIssuer{Issuer: "https://other.example", Enabled: true, ClientId: "admin", ApiAudience: "api", RedirectUri: "https://admin.example/auth/callback", Algorithms: []string{"EdDSA"}}}}}))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeFailedPrecondition || len(ce.Details()) != 1 || sink.calls != 1 {
		t.Fatal("issuer refusal", err)
	}
	value, err := ce.Details()[0].Value()
	if err != nil {
		t.Fatal(err)
	}
	if detail, ok := value.(*pb.SecurityChangePrecommitRejected); !ok || detail.Reason != pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_ISSUER_VALIDATION {
		t.Fatal(value)
	}
}

func TestSecurityPrecommitLastHumanAndInvalidInvocationBinding(t *testing.T) {
	for _, singular := range []bool{false, true} {
		t.Run(map[bool]string{false: "plural", true: "singular"}[singular], func(t *testing.T) {
			handler, sink, contexts := securityAPIFixture(t)
			identity := func(subject string) *pb.SecurityIdentity {
				return &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: subject}
			}
			suspend := func(subject string) *pb.SecurityChange {
				return &pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity(subject), State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_SUSPENDED}}}
			}
			if _, err := handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{15}, 16), Changes: []*pb.SecurityChange{suspend("admin")}})); err != nil {
				t.Fatal(err)
			}
			var err error
			id := bytes.Repeat([]byte{16}, 16)
			if singular {
				_, err = handler.ApplySecurityChange(contexts("other_admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangeRequest{ExpectedRevision: 2, ChangeId: id, Change: suspend("other_admin")}))
			} else {
				_, err = handler.ApplySecurityChanges(contexts("other_admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: 2, ChangeId: id, Changes: []*pb.SecurityChange{suspend("other_admin")}}))
			}
			var ce *connect.Error
			if !errors.As(err, &ce) || len(ce.Details()) != 1 || sink.calls != 2 {
				t.Fatal("last human refusal", err)
			}
			value, err := ce.Details()[0].Value()
			if err != nil {
				t.Fatal(err)
			}
			if detail, ok := value.(*pb.SecurityChangePrecommitRejected); !ok || detail.Reason != pb.SecurityChangeRejectionReason_SECURITY_CHANGE_REJECTION_REASON_LAST_ADMINISTRATOR {
				t.Fatal(value)
			}
		})
	}
	for _, request := range []*pb.ApplySecurityChangesRequest{
		{ExpectedRevision: 1, ChangeId: make([]byte, 15)}, {ExpectedRevision: 1, ChangeId: make([]byte, 16)}, {ChangeId: bytes.Repeat([]byte{17}, 16)},
	} {
		handler, _, contexts := securityAPIFixture(t)
		_, err := handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(request))
		var ce *connect.Error
		if !errors.As(err, &ce) || len(ce.Details()) != 0 {
			t.Fatal("malformed invocation claimed correlation", err)
		}
	}
}
