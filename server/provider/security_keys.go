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
func loadSecurityWriterPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := readSecurityOperatorFile(path, false, 16<<10)
	if err != nil {
		return nil, err
	}
	block, trailing := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, errors.New("security writer public key requires one Ed25519 SPKI PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid security writer public key")
	}
	typed, ok := key.(ed25519.PublicKey)
	if !ok || len(typed) != ed25519.PublicKeySize {
		return nil, errors.New("security writer key must be Ed25519")
	}
	return typed, nil
}
func loadSecurityWriterPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := readSecurityOperatorFile(path, true, 16<<10)
	if err != nil {
		return nil, err
	}
	block, trailing := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(trailing)) != 0 {
		return nil, errors.New("security writer private key requires one Ed25519 PKCS8 PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid security writer private key")
	}
	typed, ok := key.(ed25519.PrivateKey)
	if !ok || len(typed) != ed25519.PrivateKeySize {
		return nil, errors.New("security writer key must be Ed25519")
	}
	return typed, nil
}
func loadSecurityRoots(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := readSecurityOperatorFile(path, false, 1<<20)
	if err != nil {
		return nil, err
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid OIDC operator CA bundle")
	}
	return roots, nil
}
