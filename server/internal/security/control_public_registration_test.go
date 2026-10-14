package security

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentRegistrationDurableTransitionAndRecovery(t *testing.T) {
	image := registrationImage()
	n, owners, _ := authorityTestCompositeImage(t, 3, &image)
	registrationTestRenew(t, owners)
	o := &CurrentAuthority{origin: owners[1]}
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "new-user"}
	producer := registrationTestProducer(actor, NativeRegistrationPurpose{Kind: "registration"})
	if _, err := o.Admit(t.Context(), producer); err == nil {
		t.Fatal("unknown registration admitted normally")
	}
	proof, err := o.PrepareRegistration(t.Context(), producer)
	if err != nil {
		t.Fatal("prepare", err)
	}
	command, err := proof.Review().Command()
	if err != nil || !command.Registration.valid() || proof.RecoveryOnly() || proof.ExpiresAt().IsZero() {
		t.Fatal("prepared review", err)
	}
	uid := command.Registration.UserUID
	if _, known := o.origin.network.kernel.replayState.projection.snapshot.principals[actor]; known {
		t.Fatal("Prepare committed Principal")
	}
	if _, _, err := o.PrepareRegistrationSession(t.Context(), proof, producer, strings.Repeat("a", 64)); err == nil {
		t.Fatal("session issued before registration Applied")
	}
	result, err := o.ApplyRegistration(s3aTestContext(t), proof, producer)
	if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
		t.Fatal("Apply registration", err)
	}
	registrationTestRenew(t, owners)
	installedUID, _, registered := o.origin.network.kernel.replayState.projection.snapshot.PersonalArea(actor)
	if !registered || installedUID != uid || len(o.origin.network.kernel.replayState.projection.Image().Sessions) != 0 {
		t.Fatal("registration/session atomicity boundary")
	}
	serial := o.origin.network.kernel.originSerial
	again, err := o.ApplyRegistration(s3aTestContext(t), proof, producer)
	if err != nil || again.Original != result.Original || o.origin.network.kernel.originSerial != serial {
		t.Fatal("duplicate registration did new work", err)
	}
	// A fresh recovery verification can only see the original, even when the
	// original volatile proof or the caller's process has been lost.
	recoveryProducer := registrationTestProducer(actor, NativeRegistrationPurpose{Kind: "registration-recovery", Original: RegistrationReviewBinding(proof.Review())})
	recovered, err := o.RecoverRegistration(t.Context(), recoveryProducer, proof.Review())
	if err != nil || !recovered.RecoveryOnly() {
		t.Fatal("same identity recovery", err)
	}
	status, err := o.RegistrationStatus(t.Context(), recovered, recoveryProducer)
	if err != nil || status.Original != result.Original {
		t.Fatal("recovered original", err)
	}
	if _, err := o.ApplyRegistration(t.Context(), recovered, recoveryProducer); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("recovery acquired Apply", err)
	}
	if _, err := o.PrepareRegistration(t.Context(), recoveryProducer); err == nil {
		t.Fatal("recovery acquired new registration")
	}
	if _, _, err := o.PrepareRegistrationSession(t.Context(), recovered, recoveryProducer, strings.Repeat("b", 64)); err == nil {
		t.Fatal("status recovered raw session capability")
	}
	other := actor
	other.Subject = "other-user"
	otherProducer := registrationTestProducer(other, NativeRegistrationPurpose{Kind: "registration-recovery", Original: RegistrationReviewBinding(proof.Review())})
	if _, err := o.RecoverRegistration(t.Context(), otherProducer, proof.Review()); err == nil {
		t.Fatal("other account recovered original")
	}
	if _, err := o.RegistrationStatus(t.Context(), proof, otherProducer); err == nil {
		t.Fatal("proof reused under another identity")
	}
	sessionReview, session, err := o.PrepareRegistrationSession(t.Context(), proof, producer, strings.Repeat("c", 64))
	if err != nil {
		t.Fatal("prepare separate session", err)
	}
	sessionResult, err := o.Apply(s3aTestContext(t), sessionReview, producer, [32]byte{})
	if err != nil || sessionResult.Original == nil || sessionResult.Original.Disposition() != S1Applied {
		t.Fatal("Apply separate session", err)
	}
	registrationTestRenew(t, owners)
	if session.AuthTime.IsZero() || !session.AuthTime.Before(session.CreatedAt) || session.ExpiresAt.Sub(session.CreatedAt) != MaxSessionLifetime {
		t.Fatal("registration fabricated freshness or lifetime")
	}
	if got, known := o.origin.network.kernel.replayState.projection.snapshot.Session(session.Digest); !known || got != session {
		t.Fatal("session was not installed")
	}
	if _, _, err := o.PrepareRegistrationSession(t.Context(), proof, producer, strings.Repeat("d", 64)); err == nil {
		t.Fatal("registration crossed the subsequent session cut")
	}
	status, err = o.RegistrationStatus(t.Context(), recovered, recoveryProducer)
	if err != nil || status.Original == nil || status.Original.Commit() != result.Original.Commit() || status.Original.HandoffDigest() != result.Original.HandoffDigest() {
		t.Fatal("status changed after session issuance", err)
	}
	// Reopen the actual native journal family with its independent floors.
	// The UID, typed Role, registration policy and original outcome must survive;
	// volatile registration/serving authority must not survive a process restart.
	floors, err := o.origin.network.Floors()
	if err != nil {
		t.Fatal(err)
	}
	if err := o.origin.network.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := resumeS3AOwner(n.configs[1], floors)
	if err != nil {
		t.Fatal("registration journal replay", err)
	}
	n.nodes[1] = restarted
	key, err := loadAuthorityOriginKey(n.configs[1], filepath.Join(filepath.Dir(n.configs[1].Identity.VotingKey), "origin.key"))
	if err != nil {
		t.Fatal(err)
	}
	origin, err := attachAuthorityOrigin(restarted, key, "https://admin.example")
	if err != nil {
		t.Fatal(err)
	}
	state := restarted.kernel.replayState
	gotUID, prefix, ok := state.projection.snapshot.PersonalArea(actor)
	access, active := state.projection.snapshot.AccessFor(actor)
	if !ok || !active || gotUID != uid || !access.Allows(VertexWrite, prefix+"settings:locale") || !state.projection.Image().RegistrationPolicy.permits(actor.Issuer, "android") {
		t.Fatal("replay lost UID/policy/typed personal authority")
	}
	if original := state.ledger[proof.Review().ID]; original == nil || original.Commit() != result.Original.Commit() || original.HandoffDigest() != result.Original.HandoffDigest() {
		t.Fatal("replay changed original registration outcome")
	}
	if restarted.receiver.active != nil {
		t.Fatal("restart reused old serving authority")
	}
	if _, err := (&CurrentAuthority{origin: origin}).RegistrationStatus(t.Context(), proof, producer); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("old process proof survived restart", err)
	}
}

func TestCurrentRegistrationReservationLossRemainsUnresolved(t *testing.T) {
	image := registrationImage()
	_, owners, _ := authorityTestCompositeImage(t, 3, &image)
	registrationTestRenew(t, owners)
	o := &CurrentAuthority{origin: owners[1]}
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "new-user"}
	producer := registrationTestProducer(actor, NativeRegistrationPurpose{Kind: "registration"})
	proof, err := o.PrepareRegistration(t.Context(), producer)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	o.origin.hooks = &authorityOriginHooks{beforeSample: cancel}
	_, err = o.ApplyRegistration(ctx, proof, producer)
	if err == nil {
		t.Fatal("cancelled consume succeeded")
	}
	o.origin.hooks = nil
	if o.origin.network.kernel.originSerial != 1 || len(o.origin.network.kernel.origins) != 0 {
		t.Fatal("did not stop at durable bare reservation")
	}
	recoveryProducer := registrationTestProducer(actor, NativeRegistrationPurpose{Kind: "registration-recovery", Original: RegistrationReviewBinding(proof.Review())})
	recovered, err := o.RecoverRegistration(t.Context(), recoveryProducer, proof.Review())
	if err != nil {
		t.Fatal(err)
	}
	status, err := o.RegistrationStatus(t.Context(), recovered, recoveryProducer)
	if err != nil || status.Progress != CurrentUnresolved || status.Original != nil {
		t.Fatal("absence reported as safe rollback", err)
	}
	if _, err := o.ApplyRegistration(t.Context(), proof, producer); err == nil {
		t.Fatal("uncertain reserved original resealed")
	}
	if o.origin.network.kernel.originSerial != 1 || len(o.origin.network.kernel.origins) != 0 {
		t.Fatal("status/retry minted replacement intent")
	}
}

func TestCurrentRegistrationConcurrentSameIdentity(t *testing.T) {
	image := registrationImage()
	_, owners, _ := authorityTestCompositeImage(t, 3, &image)
	registrationTestRenew(t, owners)
	actor := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "concurrent-user"}
	var authorities [2]*CurrentAuthority
	var proofs [2]*CurrentRegistration
	var producers [2]CurrentCredentialProducer
	for i := range authorities {
		authorities[i] = &CurrentAuthority{origin: owners[uint32(i+1)]}
		producers[i] = registrationTestProducer(actor, NativeRegistrationPurpose{Kind: "registration"})
		var err error
		proofs[i], err = authorities[i].PrepareRegistration(t.Context(), producers[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	type completed struct {
		result CurrentChangeResult
		err    error
	}
	done := make(chan completed, 2)
	ctx := s3aTestContext(t)
	for i := range authorities {
		go func() {
			r, err := authorities[i].ApplyRegistration(ctx, proofs[i], producers[i])
			done <- completed{r, err}
		}()
	}
	applied := 0
	for range authorities {
		got := <-done
		if got.result.Original != nil && got.result.Original.Disposition() == S1Applied {
			applied++
		} else if got.err == nil && got.result.Original == nil {
			t.Fatal("concurrent registration reported an empty success")
		}
	}
	registrationTestRenew(t, owners)
	if applied != 1 {
		t.Fatal("same reviewed cut must install exactly one registration", applied)
	}
	for _, owner := range owners {
		image := owner.network.kernel.replayState.projection.Image()
		count := 0
		for _, principal := range image.Principals {
			if principal.Identity == actor {
				count++
				if principal.UserUID == "" || len(principal.Assignments) != 1 || principal.Assignments[0].RoleID != PersonalUserRoleID {
					t.Fatal("concurrent registration partially allocated personal area")
				}
			}
		}
		if count != 1 {
			t.Fatal("concurrent registration duplicated Principal", count)
		}
	}
}
