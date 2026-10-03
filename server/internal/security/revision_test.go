package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestRevisionAuthenticity(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := CompileImage(testImage(), DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	generation, change := [16]byte{1}, [16]byte{2}
	revision, err := SignRevision(generation, 1, [32]byte{}, change, snapshot, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	encoded := revision.Encode()
	decoded, err := DecodeRevision(encoded, publicKey, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Generation() != generation || decoded.Sequence() != 1 || decoded.Digest() != revision.Digest() ||
		!bytes.Equal(decoded.Encode(), encoded) {
		t.Fatal("revision changed on decode")
	}
	// Neither encoder result nor decoder input can mutate an installed image.
	encoded[len(encoded)-1] ^= 1
	if !bytes.Equal(decoded.Encode(), revision.Encode()) {
		t.Fatal("encoding alias escaped")
	}
	otherKey, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := DecodeRevision(revision.Encode(), otherKey, DefaultPolicyLimits()); !errors.Is(err, ErrInvalidRevision) {
		t.Fatal("relay key accepted as writer")
	}
	for _, offset := range []int{0, len(revisionPrefix), len(revisionPrefix) + 16, revisionHeaderBytes - 1, revisionHeaderBytes + 1, len(revision.Encode()) - 1} {
		corrupt := revision.Encode()
		corrupt[offset] ^= 1
		if _, err := DecodeRevision(corrupt, publicKey, DefaultPolicyLimits()); !errors.Is(err, ErrInvalidRevision) {
			t.Fatalf("tampered byte %d admitted: %v", offset, err)
		}
	}
	for _, invalid := range [][]byte{nil, []byte("unknown"), revision.Encode()[:revisionHeaderBytes], bytes.Repeat([]byte{0}, revisionHeaderBytes+MaxImageBytes+ed25519.SignatureSize+1)} {
		if _, err := DecodeRevision(invalid, publicKey, DefaultPolicyLimits()); !errors.Is(err, ErrInvalidRevision) {
			t.Fatal("invalid length admitted")
		}
	}
	if _, err := DecodeRevision(revision.Encode(), nil, DefaultPolicyLimits()); !errors.Is(err, ErrInvalidRevision) {
		t.Fatal("nil writer key admitted")
	}
	for _, test := range []struct {
		gen  [16]byte
		seq  uint64
		prev [32]byte
		id   [16]byte
	}{
		{gen: generation, seq: 0, id: change}, {seq: 1, id: change}, {gen: generation, seq: 1},
		{gen: generation, seq: 1, prev: [32]byte{1}, id: change}, {gen: generation, seq: 2, id: change},
	} {
		if _, err := SignRevision(test.gen, test.seq, test.prev, test.id, snapshot, privateKey); !errors.Is(err, ErrInvalidRevision) {
			t.Fatal("invalid revision header admitted")
		}
	}
}
