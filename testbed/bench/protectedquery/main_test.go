package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifiedQueryTransportRequiresCAHTTP2AndHostname(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := verifiedClient(path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatal("HTTP/2 not negotiated")
	}
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.RootCAs = x509.NewCertPool()
	wrongCA := &http.Client{Transport: transport}
	defer wrongCA.CloseIdleConnections()
	if response, err := wrongCA.Get(server.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
	http1 := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer http1.Close()
	if response, err := client.Get(http1.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("HTTP/1 accepted")
	} else if !strings.Contains(err.Error(), "requires verified TLS HTTP/2") {
		t.Fatal("HTTP/1 test did not reach protocol check", err)
	}
	transport = client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig.ServerName = "wrong.invalid"
	wrongName := &http.Client{Transport: transport}
	defer wrongName.CloseIdleConnections()
	if response, err := wrongName.Get(server.URL); err == nil {
		_ = response.Body.Close()
		t.Fatal("wrong hostname accepted")
	}
}

func TestQueryFixtureRejectsPlaintextOtherProfilesAndExpiry(t *testing.T) {
	const raw = `{"nodes":[{"public_origin":"https://localhost:6380"}],"ca_file":"local.pem","protected_query":{"mode":"oidc","reader_role":"fixture_query_reader","reader_subject":"fixture-query-reader"}}`
	var f fixtureInput
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	f.Query.ExpiresAt = time.Now().Add(5 * time.Minute)
	if err := validateFixture(f); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*fixtureInput){
		func(f *fixtureInput) { f.Nodes[0].PublicOrigin = "http://localhost:6380" },
		func(f *fixtureInput) { f.Nodes[0].PeerOrigin = "https://peer:6380" },
		func(f *fixtureInput) { f.Query.Mode = "machine" },
		func(f *fixtureInput) { f.Query.ExpiresAt = time.Now() },
	} {
		var invalid fixtureInput
		_ = json.Unmarshal([]byte(raw), &invalid)
		invalid.Query.ExpiresAt = f.Query.ExpiresAt
		mutate(&invalid)
		if validateFixture(invalid) == nil {
			t.Fatal("invalid preparation input accepted")
		}
	}
}
