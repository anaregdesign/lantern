package security

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

type authorityPublicProducerFunc func(context.Context, CurrentCredentialView) (CurrentCredentialFacts, error)

func (f authorityPublicProducerFunc) VerifyCurrentCredential(ctx context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
	return f(ctx, view)
}

func TestCurrentPublicSessionRequiresAppliedThenFreshAdmission(t *testing.T) {
	_, origins, ticks := authorityTestCompositeOrigins(t, 2)
	o := &CurrentAuthority{origin: origins[1]}
	ctx := s3aTestContext(t)
	if err := o.origin.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	var original *TokenAuthenticationEvidence
	code := authorityPublicProducerFunc(func(ctx context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
		if original == nil {
			facts, err := (authorityFakeCredentialProducer{}).VerifyCurrentCredential(ctx, view)
			if err != nil {
				return facts, err
			}
			e := *facts.Token
			low, _, _ := view.TimeBounds()
			e.Mode, e.Profile, e.Nonce = "code", "oidc-id", [32]byte{7}
			e.Code = CodeAuthenticationEvidence{Flow: "login", Transaction: [32]byte{8}, Exchange: [32]byte{9}, Nonce: e.Nonce, PKCE: [32]byte{10}, CreatedAt: low.Add(-20 * time.Second), ConsumedAt: low.Add(-10 * time.Second), ExpiresAt: low.Add(time.Minute)}
			original = &e
		}
		return CurrentCredentialFacts{Token: original}, nil
	})
	ctx, before, err := o.WithRequestCredential(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	review, session, err := o.PrepareSession(ctx, before, code, strings.Repeat("a", 64), strings.Repeat("b", 64), "")
	if err != nil || session.ExpiresAt.Sub(session.CreatedAt) != MaxSessionLifetime || !session.AuthTime.IsZero() {
		t.Fatal("session changed original Code facts/lifetime", err)
	}
	if o.ConfirmIssuedSession(ctx, before, session) == nil {
		t.Fatal("unapplied session issued cookie permission")
	}
	result, err := o.Apply(ctx, review, code, [32]byte{})
	if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
		t.Fatal(err)
	}
	if o.ConfirmIssuedSession(ctx, before, session) == nil {
		t.Fatal("pre-Apply admission authorized installed cookie")
	}
	authorityTestConverge(t, origins)
	for _, origin := range origins {
		if err := origin.network.renewAuthority(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ctx, after, err := o.RefreshRequest(ctx)
	if err != nil || o.ConfirmIssuedSession(ctx, after, session) != nil {
		t.Fatal("same Code facts could not obtain fresh installed view", err)
	}
	// Original expiry is retained on status-first retry, not minted anew.
	ticks[1].Add(uint64(time.Second))
	replayed, err := o.Apply(ctx, review, nil, [32]byte{})
	if err != nil || replayed.Original != result.Original {
		t.Fatal("original session retry changed", err)
	}
	readOnly := true
	cookie := authorityPublicProducerFunc(func(_ context.Context, view CurrentCredentialView) (CurrentCredentialFacts, error) {
		s, known := view.Snapshot().Session(session.Digest)
		if !known {
			return CurrentCredentialFacts{}, ErrPermissionDenied
		}
		return CurrentCredentialFacts{Session: &s, Origin: sha256.Sum256([]byte("lantern/authentication/browser-origin/v1\x00https://admin.example")), CSRF: [32]byte{4}, BrowserReadOnly: readOnly}, nil
	})
	observer := &CurrentAuthority{origin: origins[3]}
	read, err := observer.Admit(ctx, cookie)
	if err != nil || !read.BrowserReadOnly() {
		t.Fatal("established session failed at enrolled origin-free member", err)
	}
	if _, err = observer.PrepareSessionRevocation(ctx, read, cookie); err == nil {
		t.Fatal("read-only CSRF bootstrap promoted to mutation")
	}
	readOnly = false
	a, err := o.Admit(ctx, cookie)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := o.PrepareSessionRevocation(ctx, a, cookie)
	if err != nil {
		t.Fatal(err)
	}
	result, err = o.Apply(ctx, revoke, cookie, [32]byte{})
	if err != nil || result.Original == nil || result.Original.Disposition() != S1Applied {
		t.Fatal("session revoke", err)
	}
	if o.CheckOriginalDisclosure(ctx, a, revoke.ID, revoke.Operation.Digest()) == nil {
		t.Fatal("self-revoked cookie disclosed original result under old view")
	}
}
