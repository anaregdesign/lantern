package security

import (
	"context"
	"errors"
	"testing"
	"time"
)

type countedAuthorityClock struct {
	clock *fakeAuthorityClock
	calls int
}

func (c *countedAuthorityClock) Now() time.Time { c.calls++; return c.clock.Now() }

func TestAuthorityCheckObservationUsesExactlyItsExistingClockSample(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	counted := &countedAuthorityClock{clock: clock}
	a, err := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: counted})
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := store.Current()
	check := func(ctx context.Context, rev *Revision, reason string, samples int) {
		t.Helper()
		before := counted.calls
		got, err := a.CheckObserved(ctx, rev)
		if got != reason || (err == nil) != (reason == "ready") || counted.calls-before != samples {
			t.Fatalf("reason=%s error=%v samples=%d", got, err, counted.calls-before)
		}
	}
	check(t.Context(), revision, "restart_wait", 1)
	clock.advance(35*time.Second - time.Nanosecond)
	check(t.Context(), revision, "restart_wait", 1)
	clock.advance(time.Nanosecond)
	check(t.Context(), revision, "ready", 1)
	before := counted.calls
	if a.Check(t.Context(), revision) != nil || counted.calls-before != 1 {
		t.Fatal("ordinary Check changed samples")
	}
	foreign := *revision
	foreign.digest[0] ^= 1
	check(t.Context(), &foreign, "store_revision_refusal", 1)
	store.faulted.Store(true)
	check(t.Context(), revision, "store_revision_refusal", 1)
	store.faulted.Store(false)
	clock.advance(-time.Nanosecond)
	check(t.Context(), revision, "clock_fault", 1)
	clock.advance(time.Hour)
	check(t.Context(), revision, "clock_fault", 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	check(ctx, revision, "context", 0)
	check(t.Context(), nil, "unavailable", 0)
}

func TestAuthorityWriterRestartAndUnseenHolders(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	authority, err := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := store.Current()
	request := LeaseRequest{Receiver: [16]byte{1}, BootNonce: [16]byte{2}, Challenge: [32]byte{3}}
	if _, err = authority.Issue(t.Context(), request); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("restart outstanding leases forgotten", err)
	}
	clock.advance(35 * time.Second)
	if _, err = authority.Issue(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	image := old.Snapshot().Image()
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "reader"}, State: Active})
	result, err := store.Commit(t.Context(), old.Sequence(), [16]byte{2}, image)
	if err != nil {
		t.Fatal(err)
	}
	if authority.Enforced(result) {
		t.Fatal("disconnected old holder lost")
	}
	// New-cut renewal cannot erase authority granted by an older response.
	request.Challenge = [32]byte{4}
	if _, err = authority.Issue(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if authority.Enforced(result) {
		t.Fatal("new-cut renewal erased old authority")
	}
	if err = authority.Check(t.Context(), old); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("old admission retained", err)
	}
	clock.advance(32 * time.Second)
	if !authority.Enforced(result) {
		t.Fatal("expired unseen holder never enforced")
	}
	current, _ := store.Current()
	if err = authority.Check(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	clock.advance(-time.Nanosecond)
	if err = authority.Check(context.Background(), current); err == nil {
		t.Fatal("clock rollback admitted")
	}
	clock.advance(time.Minute)
	if err = authority.Check(context.Background(), current); err == nil {
		t.Fatal("poisoned clock recovered")
	}
}
func TestAuthorityWriterReceiverCapFailsClosed(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	authority, err := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock, MaxReceivers: 1})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(35 * time.Second)
	request := LeaseRequest{Receiver: [16]byte{1}, BootNonce: [16]byte{2}, Challenge: [32]byte{3}}
	if _, err = authority.Issue(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	request.BootNonce = [16]byte{4}
	if _, err = authority.Issue(t.Context(), request); !errors.Is(err, ErrControlReserve) {
		t.Fatal("active holder evicted", err)
	}
	clock.advance(32 * time.Second)
	if _, err = authority.Issue(t.Context(), request); err != nil {
		t.Fatal("expired holder retained", err)
	}
}

func TestAuthorityCheckpointSinceKeepsExactSignedCut(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	authority, err := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(35 * time.Second)
	request := LeaseRequest{Receiver: [16]byte{1}, BootNonce: [16]byte{2}, Challenge: [32]byte{3}}
	lease, checkpoint, err := authority.CheckpointSince(t.Context(), request, [32]byte{})
	if err != nil || len(lease) == 0 || len(checkpoint) == 0 {
		t.Fatal("initial cut omitted", err)
	}
	cut, err := DecodeRevision(checkpoint, store.publicKey, store.limits)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeAuthorityLease(lease, store.publicKey)
	if err != nil || decoded.digest != cut.Digest() || decoded.revision != cut.Sequence() {
		t.Fatal("lease/image cut differs", err)
	}
	lease, checkpoint, err = authority.CheckpointSince(t.Context(), request, cut.Digest())
	if err != nil || len(lease) == 0 || len(checkpoint) != 0 {
		t.Fatal("unchanged cut retransferred", err)
	}
	image := cut.Snapshot().Image()
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "later"}, State: Suspended})
	if _, err := store.Commit(t.Context(), cut.Sequence(), [16]byte{5}, image); err != nil {
		t.Fatal(err)
	}
	lease, checkpoint, err = authority.CheckpointSince(t.Context(), request, cut.Digest())
	if err != nil || len(checkpoint) == 0 {
		t.Fatal("changed cut omitted", err)
	}
	cut, err = DecodeRevision(checkpoint, store.publicKey, store.limits)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = decodeAuthorityLease(lease, store.publicKey)
	if err != nil || decoded.digest != cut.Digest() || decoded.revision != cut.Sequence() {
		t.Fatal("new lease/image cut differs", err)
	}
}
