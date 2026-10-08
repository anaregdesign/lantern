package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func TestSecurityAuthorizationTypedRefusalPluralSingularAndIndeterminateCommit(t *testing.T) {
	for _, singular := range []bool{false, true} {
		t.Run(map[bool]string{false: "plural", true: "singular"}[singular], func(t *testing.T) {
			handler, sink, contexts := securityAPIFixture(t)
			now := handler.now()
			ctx := contexts("admin", time.Time{})
			manager := security.NewManagementAuthorizations(time.Minute, 8)
			handler.verifyAuthorization = manager.Verify
			current, _ := handler.store.Current()
			review := &pb.SecurityChangeReview{ExpectedVersion: securityVersion(current), ChangeId: bytes.Repeat([]byte{8}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: "reader"}, RoleId: "security_admin"}}}}}
			prepared, err := handler.PrepareSecurityChanges(ctx, connect.NewRequest(&pb.PrepareSecurityChangesRequest{Review: review}))
			if err != nil {
				t.Fatal(err)
			}
			admission, _ := security.AdmissionFromContext(ctx)
			_, binding, err := handler.prepareReviewed(ctx, review)
			if err != nil {
				t.Fatal(err)
			}
			mint := func() []byte {
				start, err := manager.Begin(binding.Binding, now)
				if err != nil {
					t.Fatal(err)
				}
				now = now.Add(2 * time.Second)
				handler.now = func() time.Time { return now }
				if err := manager.Complete(start.ID, authorizationEventFixture(start.ID, admission.Identity(), now.Truncate(time.Second), now.Add(time.Hour), 1, current, now), current, now); err != nil {
					t.Fatal(err)
				}
				status, err := manager.Status(start.ID, admission.Identity(), current, now)
				if err != nil {
					t.Fatal(err)
				}
				return status.Proof[:]
			}
			proof := mint()
			now = now.Add(time.Minute)
			apply := func(proof []byte) error {
				if singular {
					_, err := handler.ApplySecurityChange(ctx, connect.NewRequest(&pb.ApplySecurityChangeRequest{ExpectedRevision: 1, ChangeId: review.ChangeId, Change: review.Changes[0], AuthorizationProof: proof}))
					return err
				}
				_, err := handler.ApplySecurityChanges(ctx, connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: review.ChangeId, Changes: review.Changes, AuthorizationProof: proof}))
				return err
			}
			err = apply(proof)
			var ce *connect.Error
			if !errors.As(err, &ce) || ce.Code() != connect.CodeFailedPrecondition || len(ce.Details()) != 1 || sink.calls != 1 {
				t.Fatal("expired proof was not a definite refusal", err)
			}
			value, err := ce.Details()[0].Value()
			if err != nil {
				t.Fatal(err)
			}
			detail, ok := value.(*pb.SecurityOperationAuthorizationRequired)
			if !ok || !bytes.Equal(detail.ChangeId, review.ChangeId) || !proto.Equal(detail.ExpectedVersion, review.ExpectedVersion) || !bytes.Equal(detail.IntentDigest, prepared.Msg.IntentDigest) {
				t.Fatal("unbound refusal", value)
			}
			if err := apply(mint()); err != nil || sink.calls != 2 {
				t.Fatal("same ID fresh approval refused", err)
			}
			ctx = contexts("admin", time.Time{})
			if err := apply(nil); err != nil || sink.calls != 2 {
				t.Fatal("original commit demanded new approval", err)
			}
		})
	}
	handler, sink, contexts := securityAPIFixture(t)
	sink.err = security.ErrOperationAuthorization // persistence has already begun
	_, err := handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(&pb.ApplySecurityChangesRequest{ExpectedRevision: 1, ChangeId: bytes.Repeat([]byte{9}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "ordinary"}}}}}))
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeUnavailable || len(ce.Details()) != 0 || sink.calls != 2 {
		t.Fatal("indeterminate persistence produced definitive detail", err)
	}
	if _, healthy := handler.store.Current(); healthy {
		t.Fatal("indeterminate commit left serving healthy")
	}
}

func TestSecurityAuthorizationPreflightApprovalAndOriginalRetry(t *testing.T) {
	handler, sink, contexts := securityAPIFixture(t)
	now := handler.now()
	manager := security.NewManagementAuthorizations(time.Minute, 8)
	handler.beginAuthorization = func(_ context.Context, _ *security.Admission, binding security.ManagementBinding) (security.AuthorizationStart, string, error) {
		start, err := manager.Begin(binding, now)
		return start, "https://admin.example/auth/management-authorization/start", err
	}
	handler.readAuthorization = func(_ context.Context, admission *security.Admission, id [32]byte) (security.AuthorizationStatus, error) {
		current, _ := handler.store.Current()
		return manager.Status(id, admission.Identity(), current, now)
	}
	handler.verifyAuthorization = manager.Verify
	ctx := contexts("admin", time.Time{})
	current, _ := handler.store.Current()
	identity := &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC, Issuer: "https://idp.example", Subject: "target"}
	review := &pb.SecurityChangeReview{ExpectedVersion: securityVersion(current), ChangeId: bytes.Repeat([]byte{2}, 16), Changes: []*pb.SecurityChange{
		{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}},
		{Operation: &pb.SecurityChange_PutAssignment{PutAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "security_admin"}}},
	}}
	prepared, err := handler.PrepareSecurityChanges(ctx, connect.NewRequest(&pb.PrepareSecurityChangesRequest{Review: review}))
	if err != nil || prepared.Msg.Requirement != pb.SecurityAuthorizationRequirement_SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION || !bytes.Equal(prepared.Msg.ChangeId, review.ChangeId) || len(prepared.Msg.IntentDigest) != 32 || sink.calls != 1 {
		t.Fatal("preflight mutated or misclassified", prepared, err, sink.calls)
	}
	begin, err := handler.BeginSecurityChangeAuthorization(ctx, connect.NewRequest(&pb.BeginSecurityChangeAuthorizationRequest{Review: review}))
	if err != nil || len(begin.Msg.AuthorizationId) != 32 || sink.calls != 1 {
		t.Fatal(begin, err)
	}
	var id [32]byte
	copy(id[:], begin.Msg.AuthorizationId)
	status, err := handler.GetSecurityChangeAuthorization(ctx, connect.NewRequest(&pb.GetSecurityChangeAuthorizationRequest{AuthorizationId: id[:]}))
	if err != nil || status.Msg.State != pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_PENDING || len(status.Msg.AuthorizationProof) != 0 {
		t.Fatal("pending minted proof", status, err)
	}
	now = now.Add(2 * time.Second)
	handler.now = func() time.Time { return now }
	admission, _ := security.AdmissionFromContext(ctx)
	if err := manager.Complete(id, authorizationEventFixture(id, admission.Identity(), now.Add(-time.Second), now.Add(time.Hour), 1, current, now), current, now); err != nil {
		t.Fatal(err)
	}
	status, err = handler.GetSecurityChangeAuthorization(ctx, connect.NewRequest(&pb.GetSecurityChangeAuthorizationRequest{AuthorizationId: id[:]}))
	if err != nil || status.Msg.State != pb.SecurityAuthorizationState_SECURITY_AUTHORIZATION_STATE_APPROVED || len(status.Msg.AuthorizationProof) != 32 {
		t.Fatal(status, err)
	}
	request := &pb.ApplySecurityChangesRequest{ExpectedRevision: review.ExpectedVersion.Revision, ChangeId: review.ChangeId, Changes: review.Changes, AuthorizationProof: status.Msg.AuthorizationProof}
	altered := proto.Clone(request).(*pb.ApplySecurityChangesRequest)
	altered.ChangeId[0]++
	if _, err := handler.ApplySecurityChanges(ctx, connect.NewRequest(altered)); connect.CodeOf(err) != connect.CodeFailedPrecondition || sink.calls != 1 {
		t.Fatal("proof accepted another ID", err)
	}
	original, err := handler.ApplySecurityChanges(ctx, connect.NewRequest(request))
	if err != nil || sink.calls != 2 {
		t.Fatal(original, err)
	}
	request.AuthorizationProof = nil
	replay, err := handler.ApplySecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(request))
	if err != nil || !replay.Msg.Replayed || !bytes.Equal(replay.Msg.Version.Digest, original.Msg.Version.Digest) || sink.calls != 2 {
		t.Fatal("known retry required approval or changed original", replay, err)
	}
	retained, err := handler.PrepareSecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(&pb.PrepareSecurityChangesRequest{Review: review}))
	if err != nil || retained.Msg.RetainedCommit == nil || retained.Msg.RetainedCommit.Version.Revision != original.Msg.Version.Revision {
		t.Fatal("preflight lost retained original proof", retained, err)
	}
}

func TestSecurityAuthorizationInvalidReviewAndActor(t *testing.T) {
	handler, sink, contexts := securityAPIFixture(t)
	current, _ := handler.store.Current()
	review := &pb.SecurityChangeReview{ExpectedVersion: securityVersion(current), ChangeId: bytes.Repeat([]byte{2}, 16), Changes: []*pb.SecurityChange{{Operation: &pb.SecurityChange_PutRole{PutRole: &pb.SecurityRole{Id: "data"}}}}}
	bad := proto.Clone(review).(*pb.SecurityChangeReview)
	bad.ExpectedVersion.Digest[0]++
	if _, err := handler.PrepareSecurityChanges(contexts("admin", time.Time{}), connect.NewRequest(&pb.PrepareSecurityChangesRequest{Review: bad})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("wrong cut accepted", err)
	}
	ctx := contexts("admin", handler.now())
	admission, _ := security.AdmissionFromContext(ctx)
	ctx = security.WithAdmission(ctx, admission.WithAuthentication(security.Authentication{Provenance: security.RFC9068Bearer, Class: security.UnresolvedActor, IssuerConfigRevision: 1}))
	if _, err := handler.PrepareSecurityChanges(ctx, connect.NewRequest(&pb.PrepareSecurityChangesRequest{Review: review})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("unresolved OIDC actor reviewed a mutation", err)
	}
	if sink.calls != 1 {
		t.Fatal("rejected preflight mutated")
	}
}

// This fixture models the trusted adapter only for owner/service unit tests.
// Actual cryptographic producer and callback coverage lives with oidc/provider.
func authorizationEventFixture(id [32]byte, actor security.Identity, authTime, expiry time.Time, revision uint64, current *security.Revision, now time.Time) security.TokenAuthenticationEvidence {
	date := func(t time.Time) security.AuthenticationTime {
		if t.IsZero() {
			return security.AuthenticationTime{}
		}
		return security.AuthenticationTime{Present: !t.IsZero(), Numeric: !t.IsZero(), Seconds: t.Unix(), Nanoseconds: int32(t.Nanosecond())}
	}
	return security.TokenAuthenticationEvidence{Version: 1, Policy: "lantern-oidc-v1", Mode: "code", Profile: "oidc-id", Identity: actor, Algorithm: "EdDSA", KeyID: "fixture", Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: [32]byte{3}, AudienceClient: [32]byte{4}, Generation: current.Generation(), ConfigRevision: revision, IssuedAt: date(now), ExpiresAt: date(expiry), AuthTime: date(authTime), Nonce: [32]byte{5}, Code: security.CodeAuthenticationEvidence{Flow: "operation", AuthorizationID: id, Transaction: [32]byte{6}, Exchange: [32]byte{7}, Nonce: [32]byte{5}, PKCE: [32]byte{8}, CreatedAt: now, ConsumedAt: now, ExpiresAt: now.Add(time.Minute)}}
}
