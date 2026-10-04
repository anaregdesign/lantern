package security

import (
	"testing"
	"time"
)

func TestCheckpointProofRequiresOutstandingCurrentProcessChallenge(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	authority, _ := NewLeaseAuthority(store, LeaseAuthorityOptions{Clock: clock})
	receiver, _ := NewLeaseReceiver(store, [16]byte{1}, store.publicKey, clock, 2*time.Second)
	clock.advance(35 * time.Second)
	request, _ := receiver.Challenge()
	lease, _ := authority.Issue(t.Context(), request)
	proof, err := receiver.ProveCheckpoint(lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = receiver.ProveCheckpoint(lease); err == nil {
		t.Fatal("proof replay accepted")
	}
	current, _ := store.Current()
	if err = proof.check(t.Context(), store, current); err != nil {
		t.Fatal(err)
	}
	other, _ := newAuthorityTestStore(t)
	if err = proof.check(t.Context(), other, current); err == nil {
		t.Fatal("proof crossed store boundary")
	}
	wrong := append([]byte(nil), lease...)
	wrong[len(wrong)-1] ^= 1
	if _, err = receiver.ProveCheckpoint(wrong); err == nil {
		t.Fatal("forged proof accepted")
	}
}
