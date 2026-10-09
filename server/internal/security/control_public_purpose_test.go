package security

import (
	"context"
	"testing"
	"time"
)

func TestCurrentPublicPurposeExactAttemptDoesNotRefreshSessionOrCut(t *testing.T) {
	_, origins, ticks := authorityTestComposite(t)
	o := &CurrentAuthority{origin: origins[1]}
	ctx := s3aTestContext(t)
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	producer := authorityFakeCredentialProducer{}
	a, err := o.Admit(ctx, producer)
	if err != nil {
		t.Fatal(err)
	}
	cut, _ := a.CurrentCut()
	bob := s1Bob()
	review, purpose, err := o.Prepare(ctx, a, producer, o.Profile(), cut, s1Changes(Change{Kind: PutAssignment, Identity: &bob, RoleID: "security_admin"}))
	if err != nil || !purpose {
		t.Fatal("effective manage expansion did not require purpose", err)
	}
	start, err := o.BeginPurpose(ctx, review, producer)
	if err != nil {
		t.Fatal(err)
	}
	if notBefore, err := o.PurposeNotBefore(ctx, start.Ticket); err != nil || notBefore != start.NotBefore {
		t.Fatal(err)
	}
	if _, err := o.StartPurpose(ctx, start.Ticket); err == nil {
		t.Fatal("navigation ignored qualified lower start")
	}
	ticks[1].Add(uint64(3 * time.Second))
	navigation, err := o.StartPurpose(ctx, start.Ticket)
	if err != nil || navigation.ID != start.ID || navigation.Cut != cut {
		t.Fatal("navigation changed exact reviewed binding", err)
	}
	if _, err := o.StartPurpose(ctx, start.Ticket); err == nil {
		t.Fatal("navigation ticket replay")
	}
	code := authorityPublicProducerFunc(func(ctx context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
		facts, err := producer.VerifyCurrentCredential(ctx, view)
		if err != nil {
			return facts, err
		}
		e := facts.Token
		e.Mode, e.Profile, e.Nonce = "code", "oidc-id", [32]byte{10}
		e.IssuedAt, e.AuthTime = authorityNumericTime(start.NotBefore), authorityNumericTime(start.NotBefore)
		e.Code = CodeAuthenticationEvidence{Flow: "operation", AuthorizationID: start.ID, Transaction: [32]byte{4}, Exchange: [32]byte{5}, Nonce: e.Nonce, PKCE: [32]byte{6}, CreatedAt: start.NotBefore, ConsumedAt: start.NotBefore.Add(time.Millisecond), ExpiresAt: start.ExpiresAt}
		return facts, nil
	})
	approved, err := o.CompletePurpose(ctx, start.ID, code)
	if err != nil || approved.State != AuthorizationApproved {
		t.Fatal(err)
	}
	if _, err := o.CompletePurpose(ctx, start.ID, code); err == nil {
		t.Fatal("Code event consumed twice")
	}
	if status, err := o.ReadPurpose(ctx, a, start.ID); err != nil || status.State != AuthorizationPending || status.Proof != [32]byte{} {
		t.Fatal("proof exposed before native lower endpoint reached approval", err)
	}
	if now := o.origin.network.kernel.replayState.projection.cut; now != cut || len(a.Snapshot().Image().Sessions) != 0 || !a.AuthTime().IsZero() {
		t.Fatal("purpose callback refreshed normal session or reviewed cut")
	}
	// The consume's lower endpoint must be past the approval's upper endpoint.
	ticks[1].Add(uint64(100 * time.Millisecond))
	if status, err := o.ReadPurpose(ctx, a, start.ID); err != nil || status.Proof != approved.Proof {
		t.Fatal("qualified usable approval unavailable", err)
	}
	result, err := o.Apply(ctx, review, producer, approved.Proof)
	if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
		t.Fatal("exact one-use purpose Apply", err)
	}
	if _, err := o.ReadPurpose(ctx, a, start.ID); err == nil {
		t.Fatal("pre-Apply read reused approval after cut advanced")
	}
	if replay, err := o.Apply(ctx, review, nil, [32]byte{}); err != nil || replay.Original != result.Original {
		t.Fatal("retained result demanded new reauthentication", err)
	}
}
