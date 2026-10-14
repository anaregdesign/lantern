package security

// Explicit factual unit fixtures. Signed provider verification and real public
// wire composition are separate gates; these constructors certify neither.
import (
	"context"
	"testing"
	"time"
)

func registrationTestEvent(view CurrentCredentialView, actor Identity, purpose NativeRegistrationPurpose) (TokenAuthenticationEvidence, error) {
	low, _, err := view.TimeBounds()
	if err != nil {
		return TokenAuthenticationEvidence{}, err
	}
	issuer, _ := view.Snapshot().Issuer(actor.Issuer)
	client, _ := issuer.NativeClient("android")
	return TokenAuthenticationEvidence{Version: 3, Policy: "lantern-native-registration-v1", Mode: "native-registration", Profile: NativeEndUserProfile, Identity: actor,
		Algorithm: "RS256", KeyID: "explicit-unit-facts", Credential: [32]byte{1}, Key: [32]byte{2}, Configuration: authorityIssuerCommitment(issuer, view.Cut().Generation), AudienceClient: [32]byte{3}, Generation: view.Cut().Generation, ConfigRevision: issuer.ConfigRevision,
		IssuedAt: authorityNumericTime(low.Add(-time.Minute)), ExpiresAt: authorityNumericTime(low.Add(time.Hour)), AuthTime: authorityNumericTime(low.Add(-48 * time.Hour)),
		Native: NativeAuthenticationEvidence{Kind: GoogleIDToken, ClientProfileID: client.ID, ServiceOrigin: client.ServiceOrigin, NoncePolicy: "google-initialize-unbound", Client: NativeClientCommitment(client), Transaction: [32]byte{4}, CreatedAt: low.Add(-time.Minute), ConsumedAt: low.Add(-time.Second), ExpiresAt: low.Add(9 * time.Minute), Registration: purpose}}, nil
}

func registrationTestProducer(actor Identity, purpose NativeRegistrationPurpose) CurrentCredentialProducer {
	var event *TokenAuthenticationEvidence
	return authorityPublicProducerFunc(func(_ context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
		if event == nil {
			e, err := registrationTestEvent(view, actor, purpose)
			if err != nil {
				return CurrentCredentialFacts{}, err
			}
			event = &e
		}
		return CurrentCredentialFacts{Token: event}, nil
	})
}

func registrationTestView(t *testing.T, image Image) (*S1ApplyState, CurrentCredentialView, TokenAuthenticationEvidence) {
	t.Helper()
	s := s1Fixture(t, image)
	clock, _ := fakeAuthorityTimeOwnerAt(t, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	v := CurrentCredentialView{projection: s.projection, clock: clock}
	e, err := registrationTestEvent(v, Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "new-user"}, NativeRegistrationPurpose{Kind: "registration"})
	if err != nil {
		t.Fatal(err)
	}
	return s, v, e
}

func registrationTestRenew(t *testing.T, owners map[uint32]*authorityOriginOwner) {
	t.Helper()
	authorityTestConverge(t, owners)
	for _, owner := range owners {
		if err := owner.network.renewAuthority(s3aTestContext(t)); err != nil {
			t.Fatal(err)
		}
	}
}
