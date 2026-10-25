package provider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/anaregdesign/lantern/core/privatefile"
)

func readSecurityOperatorFile(path string, private bool, maxBytes int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("security operator file must have an absolute path")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > maxBytes {
		return nil, errors.New("security operator file unavailable or unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("security operator file unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || private && privatefile.Check(file) != nil {
		return nil, errors.New("security operator file changed or unsafe")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		return nil, errors.New("security operator file unavailable or oversized")
	}
	return raw, nil
}
func loadOperatorPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := readSecurityOperatorFile(path, false, 16<<10)
	if err != nil {
		return nil, err
	}
	return parseOperatorPublicKeyPEM(raw)
}

func parseOperatorPublicKeyPEM(raw []byte) (ed25519.PublicKey, error) {
	block, trailing := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, errors.New("operator public key requires one Ed25519 SPKI PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid operator public key")
	}
	typed, ok := key.(ed25519.PublicKey)
	if !ok || len(typed) != ed25519.PublicKeySize {
		return nil, errors.New("operator key must be Ed25519")
	}
	return typed, nil
}

func loadSecurityRootsPEM(value string) (*x509.CertPool, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 1<<20 {
		return nil, errors.New("oversized OIDC operator CA bundle")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM([]byte(value)) {
		return nil, errors.New("invalid OIDC operator CA bundle")
	}
	return roots, nil
}
