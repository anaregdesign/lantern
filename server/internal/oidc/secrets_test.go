package oidc

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSecretRegistryInlineValue(t *testing.T) {
	bindings := map[string]SecretBinding{"browser": {
		Issuer: "https://idp.example", ClientID: "browser-client", TokenOrigin: "https://tokens.example", Value: "in-memory-secret",
	}}
	r, err := NewSecretRegistry(bindings)
	if err != nil {
		t.Fatal(err)
	}
	// The registry owns its startup snapshot rather than an application map.
	bindings["browser"] = SecretBinding{}
	value, err := r.loadClient("https://idp.example", "browser-client", "browser", "https://tokens.example/oauth/token")
	if err != nil || value != "in-memory-secret" {
		t.Fatal("inline value was not available at its exact destination")
	}
	for name, args := range map[string][4]string{
		"Issuer":    {"https://other.example", "browser-client", "browser", "https://tokens.example/oauth/token"},
		"client":    {"https://idp.example", "other-client", "browser", "https://tokens.example/oauth/token"},
		"handle":    {"https://idp.example", "browser-client", "unknown", "https://tokens.example/oauth/token"},
		"origin":    {"https://idp.example", "browser-client", "browser", "https://attacker.example/oauth/token"},
		"plaintext": {"https://idp.example", "browser-client", "browser", "http://tokens.example/oauth/token"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.loadClient(args[0], args[1], args[2], args[3]); err == nil {
				t.Fatal("inline secret crossed its destination binding")
			}
		})
	}
	for name, value := range map[string]string{
		"empty": "", "oversized": strings.Repeat("x", 4097), "NUL": "private\x00value", "newline": "private\nvalue", "carriage return": "private\rvalue", "UTF8": string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSecretRegistry(map[string]SecretBinding{"browser": {Issuer: "https://idp.example", ClientID: "browser-client", TokenOrigin: "https://tokens.example", Value: value}}); err == nil {
				t.Fatal("invalid inline secret accepted")
			}
		})
	}
	if _, err := NewSecretRegistry(map[string]SecretBinding{"browser": {Issuer: "https://idp.example", ClientID: "browser-client", TokenOrigin: "https://tokens.example", Path: filepath.Join(t.TempDir(), "secret"), Value: "ambiguous"}}); err == nil {
		t.Fatal("inline and file sources were both accepted")
	}
}
