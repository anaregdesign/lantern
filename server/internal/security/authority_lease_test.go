package security

import (
	"crypto/ed25519"
	"crypto/sha256"
	"testing"
	"time"
)

func TestAuthorityLeaseSignatureAndBinding(t *testing.T) {
	store, clock := newAuthorityTestStore(t)
	revision, _ := store.Current()
	lease := authorityLease{generation: store.generation, writer: sha256.Sum256(store.publicKey), incarnation: [16]byte{3}, request: LeaseRequest{Receiver: [16]byte{4}, BootNonce: [16]byte{5}, Challenge: [32]byte{6}}, revision: revision.sequence, digest: revision.digest, issuedAt: clock.Now(), lifetime: 30 * time.Second}
	restored, err := decodeAuthorityLease(lease.encode(store.privateKey), store.publicKey)
	if err != nil || restored.request != lease.request || restored.digest != lease.digest {
		t.Fatal(restored, err)
	}
	encoded := lease.encode(store.privateKey)
	encoded[len(encoded)-1] ^= 1
	if _, err := decodeAuthorityLease(encoded, store.publicKey); err == nil {
		t.Fatal("forged lease accepted")
	}
	wrongKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	if _, err := decodeAuthorityLease(lease.encode(store.privateKey), wrongKey); err == nil {
		t.Fatal("wrong writer accepted")
	}
}
