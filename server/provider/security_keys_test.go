package provider

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityKeysOperatorFileContract(t *testing.T) {
	privatePath, publicPath := securityKeyFixture(t, t.TempDir())
	private, err := loadSecurityWriterPrivateKey(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	public, err := loadSecurityWriterPublicKey(publicPath)
	if err != nil || !bytes.Equal(private.Public().(ed25519.PublicKey), public) {
		t.Fatal("key mismatch", err)
	}
	if err = os.Chmod(privatePath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = loadSecurityWriterPrivateKey(privatePath); err == nil {
		t.Fatal("world-readable signing key accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(publicPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err = loadSecurityWriterPublicKey(link); err == nil {
		t.Fatal("symlink signing trust accepted")
	}
	if _, err = loadSecurityWriterPublicKey(privatePath); err == nil {
		t.Fatal("private key accepted as public PEM")
	}
	if _, err = readSecurityOperatorFile("relative", false, 16<<10); err == nil {
		t.Fatal("relative path accepted")
	}
}
