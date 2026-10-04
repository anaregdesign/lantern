package security

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAuthorityReceiverLatencyReplayAndCurrentCut(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	authority, err := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := NewLeaseReceiver(store, [16]byte{1}, store.publicKey, clock, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(35 * time.Second)
	request, err := receiver.Challenge()
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(5 * time.Second)
	encoded, err := authority.Issue(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(5 * time.Second)
	if err = receiver.Accept(encoded); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current()
	if err = receiver.Check(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	if err = receiver.Accept(encoded); !errors.Is(err, ErrInvalidLease) {
		t.Fatal("replay extended lease", err)
	}
	clock.advance(18 * time.Second)
	if err = receiver.Check(t.Context(), current); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("expiry started at arrival", err)
	}
	request, _ = receiver.Challenge()
	delayed, _ := authority.Issue(t.Context(), request)
	newer, _ := receiver.Challenge()
	fresh, _ := authority.Issue(t.Context(), newer)
	if err = receiver.Accept(delayed); !errors.Is(err, ErrInvalidLease) {
		t.Fatal("out-of-order accepted", err)
	}
	if err = receiver.Accept(fresh); err != nil {
		t.Fatal(err)
	}
	restarted, _ := NewLeaseReceiver(store, [16]byte{1}, store.publicKey, clock, 2*time.Second)
	_, _ = restarted.Challenge()
	if err = restarted.Accept(fresh); !errors.Is(err, ErrInvalidLease) {
		t.Fatal("restart recovered lease", err)
	}
	image := current.Snapshot().Image()
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "new"}, State: Active})
	if _, err = store.Commit(t.Context(), current.Sequence(), [16]byte{2}, image); err != nil {
		t.Fatal(err)
	}
	next, _ := store.Current()
	if err = receiver.Check(context.Background(), next); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("unleased cut active", err)
	}
	request, _ = receiver.Challenge()
	newCut, _ := authority.Issue(t.Context(), request)
	if err = receiver.Accept(newCut); err != nil {
		t.Fatal(err)
	}
	if err = receiver.Check(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	clock.advance(-time.Nanosecond)
	if err = receiver.Check(t.Context(), next); err == nil {
		t.Fatal("clock rollback active")
	}
}
func TestAuthorityReceiverExpiredResponseCannotActivate(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	authority, _ := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock})
	receiver, _ := NewLeaseReceiver(store, [16]byte{1}, store.publicKey, clock, 2*time.Second)
	clock.advance(35 * time.Second)
	request, _ := receiver.Challenge()
	encoded, _ := authority.Issue(t.Context(), request)
	clock.advance(29 * time.Second)
	if err := receiver.Accept(encoded); err == nil {
		t.Fatal("delayed response granted lifetime")
	}
}
