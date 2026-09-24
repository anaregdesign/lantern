package mutationreceipt

import (
	"crypto/sha256"
	"testing"
)

func TestIntentDigestDomainAndCanonicalBytes(t *testing.T) {
	canonical := []byte{0, 0, 0, 3, 'P', 'u', 't', 0, 0, 0, 1, 'x'}
	h := sha256.Sum256(append([]byte("lantern-receipt-intent-v1\x00"), canonical...))
	if got := IntentDigest(canonical); got != h {
		t.Fatalf("digest = %x, want %x", got, h)
	}
	if IntentDigest(canonical) == sha256.Sum256(canonical) {
		t.Fatal("domain separation missing")
	}
	changed := append([]byte(nil), canonical...)
	changed[len(changed)-1] = 'y'
	if IntentDigest(changed) == h {
		t.Fatal("distinct canonical semantic fields had the same digest")
	}
}
