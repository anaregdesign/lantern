package security

import (
	"crypto/ecdh"
	"math/big"
)

// Bootstrap key validation only; all signatures use crypto/ed25519. Require a
// canonical point on Edwards25519 and exclude small-order points. This handles
// public data only. The birational y->u conversion lets the standard library's
// X25519 low-order rejection perform the small-order check without a second
// implementation of signature verification or Edwards group arithmetic.
// Decoding: RFC 8032 section 5.1.3; conversion: RFC 7748 section 4.1.
func s2cValidPublicKey(key [32]byte) bool {
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	encoded := key
	sign := encoded[31] >> 7
	encoded[31] &= 127
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	y := new(big.Int).SetBytes(encoded[:])
	if y.Cmp(p) >= 0 {
		return false
	}
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, p)
	d := new(big.Int).ModInverse(big.NewInt(121666), p)
	d.Mul(d, big.NewInt(-121665)).Mod(d, p)
	denominator := new(big.Int).Mul(d, y2)
	denominator.Add(denominator, big.NewInt(1)).Mod(denominator, p)
	inverse := new(big.Int).ModInverse(denominator, p)
	if inverse == nil {
		return false
	}
	x2 := new(big.Int).Sub(y2, big.NewInt(1))
	x2.Mul(x2, inverse).Mod(x2, p)
	x := new(big.Int).ModSqrt(x2, p)
	if x == nil || x.Sign() == 0 && sign != 0 {
		return false
	}
	// u=(1+y)/(1-y); the identity has no finite Montgomery image.
	inverse = new(big.Int).ModInverse(new(big.Int).Sub(big.NewInt(1), y), p)
	if inverse == nil {
		return false
	}
	u := new(big.Int).Add(big.NewInt(1), y)
	u.Mul(u, inverse).Mod(u, p)
	u.FillBytes(encoded[:])
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	public, err := ecdh.X25519().NewPublicKey(encoded[:])
	if err != nil {
		return false
	}
	// A fixed nonsecret scalar is sufficient: X25519 clamps it to a multiple
	// of eight and rejects the all-zero shared secret for every small-order u.
	probe := make([]byte, 32)
	probe[0] = 1
	private, err := ecdh.X25519().NewPrivateKey(probe)
	if err != nil {
		return false
	}
	_, err = private.ECDH(public)
	return err == nil
}
