package mutationreceipt

import "crypto/sha256"

const intentDomain = "lantern-receipt-intent-v1\x00"

// IntentDigest hashes an already validated, length-delimited canonical
// semantic intent. Protobuf serialization is not canonical intent: callers
// must encode operation kind, exact identity, values/number bits, expiration,
// condition, contribution ID, and other semantic fields themselves.
func IntentDigest(canonical []byte) [sha256.Size]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(intentDomain))
	_, _ = h.Write(canonical)
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}
