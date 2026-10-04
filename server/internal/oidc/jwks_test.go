package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWKSAlgorithms(t *testing.T) {
	p := newTestProvider(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	for _, tc := range []struct {
		method jwt.SigningMethod
		key    any
		jwk    jsonKey
	}{
		{jwt.SigningMethodRS256, rsaKey, jsonKey{Kind: "RSA", N: enc(rsaKey.N.Bytes()), E: enc(big.NewInt(int64(rsaKey.E)).Bytes())}},
		{jwt.SigningMethodPS256, rsaKey, jsonKey{Kind: "RSA", N: enc(rsaKey.N.Bytes()), E: enc(big.NewInt(int64(rsaKey.E)).Bytes())}},
		{jwt.SigningMethodES256, ecKey, jsonKey{Kind: "EC", Curve: "P-256", X: enc(ecKey.X.FillBytes(make([]byte, 32))), Y: enc(ecKey.Y.FillBytes(make([]byte, 32)))}},
	} {
		t.Run(tc.method.Alg(), func(t *testing.T) {
			tc.jwk.ID, tc.jwk.Algorithm = "key", tc.method.Alg()
			trust := p.trust
			trust.Issuer.Algorithms = []string{tc.method.Alg()}
			p.mu.Lock()
			p.document = map[string]any{"keys": []jsonKey{tc.jwk}}
			p.mu.Unlock()
			v := NewVerifier(NewKeyCache(p.fetcher))
			v.now = p.now
			token := jwt.NewWithClaims(tc.method, p.accessClaims())
			token.Header["kid"], token.Header["typ"] = "key", "at+jwt"
			raw, err := token.SignedString(tc.key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := v.VerifyAccess(context.Background(), raw, trust); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJWKSRejectsAmbiguousAndWeakKeys(t *testing.T) {
	p := newTestProvider(t)
	valid := jsonKey{Kind: "OKP", ID: "key", Algorithm: "EdDSA", Curve: "Ed25519", X: base64.RawURLEncoding.EncodeToString(p.private[32:])}
	for _, keys := range [][]jsonKey{nil, {valid, valid}, {{Kind: "oct", ID: "key", Symmetric: "secret"}}, {{Kind: "RSA", ID: "key", N: "AQ", E: "AQAB"}}, {{Kind: "EC", ID: "key", Curve: "P-256", X: "AA", Y: "AA"}}, {{Kind: "OKP", ID: "key", Curve: "Ed25519", X: "AA"}}} {
		if _, err := decodeKeys(keys, []string{"RS256", "ES256", "EdDSA"}); err == nil {
			t.Fatal("ambiguous or weak JWKS accepted")
		}
	}
}
