package security

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestS2CKeyValidation(t *testing.T) {
	// RFC 8032 section 7.1 test vectors 1 and 2, independently fixed keys.
	for _, vector := range []string{"d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a", "3d4017c3e843895a92b70aa74d1b7ebc9c982ccf2ec4968cc0cd55f12af4660c"} {
		decoded, err := hex.DecodeString(vector)
		if err != nil {
			t.Fatal(err)
		}
		var key [32]byte
		copy(key[:], decoded)
		if !s2cValidPublicKey(key) {
			t.Fatal("RFC 8032 public key rejected")
		}
	}
	for i := byte(0); i < 32; i++ {
		seed := make([]byte, ed25519.SeedSize)
		seed[0] = i
		key := ed25519.NewKeyFromSeed(seed)
		var public [32]byte
		copy(public[:], key[32:])
		if !s2cValidPublicKey(public) {
			t.Fatalf("generated key %d", i)
		}
	}
	minusOne := [32]byte{}
	for i := range minusOne {
		minusOne[i] = 255
	}
	minusOne[0] = 236
	minusOne[31] = 127
	noncanonical := minusOne
	noncanonical[0] = 237
	negativeIdentity := [32]byte{1}
	negativeIdentity[31] = 128
	// The two order-eight y coordinates satisfy d*y^4+2*y^2-1=0;
	// both x signs must be excluded, along with the order-four sign variant.
	for _, encoded := range []string{"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05", "c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a", "0000000000000000000000000000000000000000000000000000000000000000"} {
		b, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var key [32]byte
		copy(key[:], b)
		for _, sign := range []byte{0, 128} {
			key[31] = (key[31] & 127) | sign
			if s2cValidPublicKey(key) {
				t.Fatal("small-order sign variant")
			}
		}
	}
	for name, key := range map[string][32]byte{"zero order4": {}, "identity": {1}, "order2": minusOne, "noncanonical y": noncanonical, "negative identity": negativeIdentity, "off curve": {2}} {
		t.Run(name, func(t *testing.T) {
			if s2cValidPublicKey(key) {
				t.Fatal("weak/malformed key")
			}
		})
	}
	// The standard verifier's equation alone is insufficient for bootstrap:
	// A=identity,R=identity,S=0 verifies arbitrary signed content without a key.
	identity := [32]byte{1}
	signature := make([]byte, ed25519.SignatureSize)
	signature[0] = 1
	if !ed25519.Verify(identity[:], []byte("bootstrap weak-key negative control"), signature) {
		t.Fatal("stdlib control changed; reevaluate bootstrap check")
	}
	f := s2cTestCluster(t, 3)
	f.members[0].PublicKey = identity
	if _, err := s2cMemberSetDigest(f.members); err == nil {
		t.Fatal("weak voter bootstrap")
	}
	f = s2cTestCluster(t, 3)
	f.origins[0].PublicKey = identity
	if _, err := newS2CTrust(s2cBootstrap{f.genesis, f.members, f.origins, s2cTestBounds()}); err == nil {
		t.Fatal("weak origin bootstrap")
	}
}
