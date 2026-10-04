package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func TestAdmissionBindsCutIdentityAndExpiry(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	authority, _ := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock})
	clock.advance(35 * time.Second)
	cut, _ := store.Current()
	admission, err := NewAdmission(testIdentity(), clock.Now(), clock.Now().Add(time.Hour), cut, authority.Check)
	if err != nil {
		t.Fatal(err)
	}
	if err = admission.Check(t.Context(), clock.Now()); err != nil {
		t.Fatal(err)
	}
	proof := admission.WithBrowserProof("opaque")
	if proof.Identity() != admission.Identity() || proof.ScopeBinding() != admission.ScopeBinding() || admission.Browser() || !proof.Browser() {
		t.Fatal("browser proof changed authority")
	}
	ctx := WithAdmission(context.Background(), proof)
	got, ok := AdmissionFromContext(ctx)
	if !ok || got != proof {
		t.Fatal("missing verified context")
	}
	image := cut.Snapshot().Image()
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "extra"}, State: Active})
	if _, err = store.Commit(t.Context(), cut.Sequence(), [16]byte{3}, image); err != nil {
		t.Fatal(err)
	}
	if err = admission.Check(t.Context(), clock.Now()); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("changed policy reused", err)
	}
	if _, err = NewAdmission(testIdentity(), clock.Now(), clock.Now().Add(time.Hour), cut, nil); err == nil {
		t.Fatal("snapshot alone authorized")
	}
}
func BenchmarkAdmissionLocalCheck(b *testing.B) {
	// Measure only the local hot path, without network, persistence or policy
	// compilation. Full protocol/performance qualification remains separate.
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	store, err := NewStore(StoreOptions{Generation: [16]byte{1}, PublicKey: key.Public().(ed25519.PublicKey), PrivateKey: key, Committer: &fakeCommitter{}, Limits: DefaultPolicyLimits()})
	if err != nil {
		b.Fatal(err)
	}
	if _, err = store.ReconcileBootstrap(b.Context(), 0, [16]byte{1}, testImage()); err != nil {
		b.Fatal(err)
	}
	clock := &fakeAuthorityClock{now: time.Now()}
	authority, _ := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock})
	clock.advance(35 * time.Second)
	cut, _ := store.Current()
	admission, _ := NewAdmission(testIdentity(), clock.Now(), clock.Now().Add(time.Hour), cut, authority.Check)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := admission.Check(b.Context(), clock.Now()); err != nil {
			b.Fatal(err)
		}
	}
}
