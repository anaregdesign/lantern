package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueryJWTSignatureAndShortLifetime(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	parts := strings.Split(queryJWT(private, "https://local.invalid", "reader", now), ".")
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), signature) {
		t.Fatal("signature", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil || claims["exp"].(float64)-claims["iat"].(float64) != 900 || claims["aud"] != "lantern-fixture" || claims["client_id"] != "fixture-query-client" || claims["jti"] != "fixture-query-reader" {
		t.Fatal("bounded JWT contract", claims)
	}
}

func TestQueryProfileRetainsTLSAndIsolatesAdmission(t *testing.T) {
	for _, mode := range []string{"off", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "query")
			f, err := generate(dir, []int{16380}, nil, mode, "")
			if err != nil {
				t.Fatal(err)
			}
			stop, err := addFixtureQuery(&f, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			env := f.Nodes[0].Environment
			if f.Query.Mode != mode || env["LANTERN_TLS_CERT_FILE"] == "" || env["LANTERN_SEARCH_ENABLED"] != "true" || f.Nodes[0].PeerOrigin != "" {
				t.Fatal("standalone matched transport")
			}
			if mode == "oidc" && (env["LANTERN_OIDC_ADMIN_ISSUER"] != f.Query.Issuer || env["LANTERN_OIDC_ROOT_CA_FILE"] != f.CAFile || !strings.Contains(env["LANTERN_SECURITY_BOOTSTRAP_ROLES"], "bench:private:")) {
				t.Fatal("local production admission inputs")
			}
			pem, err := os.ReadFile(f.CAFile)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM(pem)
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: time.Second}
			response, err := client.Get(f.Query.Issuer + "/jwks")
			if err != nil {
				t.Fatal("process-local trust", err)
			}
			var jwks struct{ Keys []map[string]string }
			decodeErr := json.NewDecoder(response.Body).Decode(&jwks)
			_ = response.Body.Close()
			if decodeErr != nil || response.StatusCode != 200 || len(jwks.Keys) != 1 {
				t.Fatal("JWKS", decodeErr)
			}
			public, _ := base64.RawURLEncoding.DecodeString(jwks.Keys[0]["x"])
			output, _ := json.Marshal(f)
			for _, path := range []string{f.Query.AdminTokenFile, f.Query.ReaderTokenFile, f.Query.InvalidTokenFile} {
				raw, err := os.ReadFile(path)
				info, statErr := os.Stat(path)
				if err != nil || statErr != nil || info.Mode().Perm() != 0600 || strings.Contains(string(output), string(raw)) {
					t.Fatal("private token paths only", err, statErr)
				}
				parts := strings.Split(string(raw), ".")
				signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
				valid := ed25519.Verify(public, []byte(parts[0]+"."+parts[1]), signature)
				if valid != (path != f.Query.InvalidTokenFile) {
					t.Fatal("valid/invalid signatures conflated")
				}
			}
			response, err = client.Get(f.Query.Issuer + "/token")
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != 404 {
				t.Fatal("fixture unexpectedly issues provider tokens")
			}
		})
	}
}

func TestQueryProfileRejectsOtherTopologies(t *testing.T) {
	for _, f := range []fixture{{}, {Nodes: []fixtureNode{{PeerOrigin: "https://peer.invalid"}}}, {Nodes: []fixtureNode{{}, {}}}} {
		if _, err := addFixtureQuery(&f, t.TempDir()); err == nil {
			t.Fatal("other topology accepted")
		}
	}
}
