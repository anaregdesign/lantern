package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
)

type jsonKey struct {
	Kind       string   `json:"kty"`
	ID         string   `json:"kid"`
	Algorithm  string   `json:"alg"`
	Use        string   `json:"use"`
	Operations []string `json:"key_ops"`
	Curve      string   `json:"crv"`
	N          string   `json:"n"`
	E          string   `json:"e"`
	X          string   `json:"x"`
	Y          string   `json:"y"`
	Private    string   `json:"d"`
	Symmetric  string   `json:"k"`
}

type keyID struct{ id, algorithm string }

func decodeKeys(keys []jsonKey, algorithms []string) (map[keyID]crypto.PublicKey, error) {
	if len(keys) == 0 || len(keys) > 64 {
		return nil, ErrInvalidDocument
	}
	result := make(map[keyID]crypto.PublicKey)
	for _, key := range keys {
		if len(key.ID) == 0 || len(key.ID) > 256 || key.Private != "" || key.Symmetric != "" {
			return nil, ErrInvalidDocument
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if len(key.Operations) != 0 && (len(key.Operations) != 1 || key.Operations[0] != "verify") {
			continue
		}
		for _, algorithm := range algorithms {
			if key.Algorithm != "" && key.Algorithm != algorithm {
				continue
			}
			public, err := decodeKey(key, algorithm)
			if err != nil {
				return nil, err
			}
			if public == nil {
				continue
			}
			id := keyID{key.ID, algorithm}
			if _, duplicate := result[id]; duplicate {
				return nil, ErrInvalidDocument
			}
			result[id] = public
		}
	}
	if len(result) == 0 {
		return nil, ErrInvalidDocument
	}
	return result, nil
}

func decodeKey(key jsonKey, algorithm string) (crypto.PublicKey, error) {
	decode := base64.RawURLEncoding.Strict().DecodeString
	switch {
	case key.Kind == "RSA" && (algorithm == "RS256" || algorithm == "PS256"):
		n, err := decode(key.N)
		if err != nil || len(n) < 256 || len(n) > 1024 || n[0] == 0 {
			return nil, ErrInvalidDocument
		}
		e, err := decode(key.E)
		if err != nil || len(e) == 0 || len(e) > 4 || e[0] == 0 {
			return nil, ErrInvalidDocument
		}
		modulus, exponent := new(big.Int).SetBytes(n), new(big.Int).SetBytes(e).Uint64()
		if modulus.BitLen() < 2048 || modulus.Bit(0) == 0 || exponent < 3 || exponent > 0x7fffffff || exponent%2 == 0 {
			return nil, ErrInvalidDocument
		}
		return &rsa.PublicKey{N: modulus, E: int(exponent)}, nil
	case key.Kind == "EC" && algorithm == "ES256" && key.Curve == "P-256":
		x, errX := decode(key.X)
		y, errY := decode(key.Y)
		if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
			return nil, ErrInvalidDocument
		}
		public := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !public.Curve.IsOnCurve(public.X, public.Y) {
			return nil, ErrInvalidDocument
		}
		return public, nil
	case key.Kind == "OKP" && algorithm == "EdDSA" && key.Curve == "Ed25519":
		x, err := decode(key.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, ErrInvalidDocument
		}
		return ed25519.PublicKey(x), nil
	default:
		return nil, nil
	}
}
