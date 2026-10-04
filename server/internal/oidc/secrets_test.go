package oidc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecretRegistryDestinationBinding(t *testing.T) {
	p := newTestProvider(t)
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("confidential"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := NewSecretRegistry(map[string]SecretBinding{"admin": {Issuer: p.trust.Issuer.URL, ClientID: "admin", TokenOrigin: p.server.URL, Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	issuer := p.trust.Issuer
	issuer.SecretRef = "admin"
	value, err := r.load(issuer, p.server.URL+"/token")
	if err != nil || value != "confidential" {
		t.Fatal("valid secret binding failed")
	}
	for _, mutation := range []string{"Issuer", "ClientID", "SecretRef", "endpoint"} {
		other, endpoint := issuer, p.server.URL+"/token"
		switch mutation {
		case "Issuer":
			other.URL += "/other"
		case "ClientID":
			other.ClientID = "different"
		case "SecretRef":
			other.SecretRef = path
		case "endpoint":
			endpoint = "https://attacker.example/token"
		}
		if _, err := r.load(other, endpoint); err == nil {
			t.Fatal("cross-destination secret reuse accepted")
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.load(issuer, p.server.URL+"/token"); err == nil {
		t.Fatal("world-readable secret accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := path + ".link"
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	r.bindings["admin"] = SecretBinding{Issuer: issuer.URL, ClientID: issuer.ClientID, TokenOrigin: p.server.URL, Path: link}
	if _, err := r.load(issuer, p.server.URL+"/token"); err == nil {
		t.Fatal("symbolic secret path accepted")
	}
}
