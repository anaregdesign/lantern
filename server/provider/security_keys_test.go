package provider

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityKeysOperatorFileContract(t *testing.T) {
	privatePath, publicPath := securityKeyFixture(t, t.TempDir())
	raw, err := readSecurityOperatorFile(privatePath, true, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	private := parsed.(ed25519.PrivateKey)
	public, err := loadOperatorPublicKey(publicPath)
	if err != nil || !bytes.Equal(private.Public().(ed25519.PublicKey), public) {
		t.Fatal("key mismatch", err)
	}
	if err = os.Chmod(privatePath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = readSecurityOperatorFile(privatePath, true, 16<<10); err == nil {
		t.Fatal("world-readable signing key accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(publicPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err = loadOperatorPublicKey(link); err == nil {
		t.Fatal("symlink signing trust accepted")
	}
	if _, err = loadOperatorPublicKey(privatePath); err == nil {
		t.Fatal("private key accepted as public PEM")
	}
	if _, err = readSecurityOperatorFile("relative", false, 16<<10); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestSecurityRootsInlinePEM(t *testing.T) {
	server := httptest.NewTLSServer(nil)
	t.Cleanup(server.Close)
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	roots, err := loadSecurityRootsPEM(string(certificate))
	if err != nil || roots == nil {
		t.Fatal("inline trusted roots rejected", err)
	}
	if roots, err := loadSecurityRootsPEM(""); err != nil || roots != nil {
		t.Fatal("absent private CA must retain system defaults", err)
	}
	if _, err := loadSecurityRootsPEM("not-a-certificate"); err == nil {
		t.Fatal("invalid root PEM accepted")
	}
}
