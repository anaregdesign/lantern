package main

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
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
	for _, mutate := range []func(*fixtureInput){
		func(f *fixtureInput) { f.Nodes[0].PublicOrigin = "http://localhost:6380" },
		func(f *fixtureInput) { f.Nodes[0].PeerOrigin = "https://peer:6380" },
		func(f *fixtureInput) { f.Query.Mode = "machine" },
		func(f *fixtureInput) { f.Query.ExpiresAt = time.Now() },
		func(f *fixtureInput) { f.Nodes = f.Nodes[:1] },
	} {
		invalid := queryFixtureInput(t)
		mutate(&invalid)
		if validateFixture(invalid) == nil {
			t.Fatal("invalid preparation input accepted")
		}
	}
}

func TestCurrentQueryCapabilitiesRejectFallbackAndMissingFullProfile(t *testing.T) {
	f := queryFixtureInput(t)
	profile := f.Query.CurrentProfile
	if err := validateFixture(f); err != nil {
		t.Fatal(err)
	}
	valid := &pb.GetAuthCapabilitiesResponse{Mode: pb.AuthMode_AUTH_MODE_OIDC, Ready: true, ProtocolVersion: 2, CurrentMember: 1, CurrentProfile: profile}
	if err := validateQueryCapabilities(f, 0, valid); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*pb.GetAuthCapabilitiesResponse){
		func(c *pb.GetAuthCapabilitiesResponse) {
			c.Mode = pb.AuthMode_AUTH_MODE_OFF
			c.ProtocolVersion = 1
			c.CurrentProfile = nil
		},
		func(c *pb.GetAuthCapabilitiesResponse) { c.ProtocolVersion = 1 },
		func(c *pb.GetAuthCapabilitiesResponse) { c.Ready = false },
		func(c *pb.GetAuthCapabilitiesResponse) { c.CurrentProfile = nil },
		func(c *pb.GetAuthCapabilitiesResponse) { c.CurrentProfile.TimeProfile = nil },
		func(c *pb.GetAuthCapabilitiesResponse) { c.CurrentProfile.Cohort[0] ^= 1 },
		func(c *pb.GetAuthCapabilitiesResponse) { c.CurrentMember = 2 },
	} {
		invalid := proto.Clone(valid).(*pb.GetAuthCapabilitiesResponse)
		edit(invalid)
		if validateQueryCapabilities(f, 0, invalid) == nil {
			t.Fatal("current profile fallback accepted")
		}
	}
	for _, profileName := range []string{"", "legacy-v1", "unknown"} {
		old := f.Query.SecurityProfile
		f.Query.SecurityProfile = profileName
		if validateFixture(f) == nil {
			t.Fatal("current fixture silently downgraded", profileName)
		}
		f.Query.SecurityProfile = old
	}
	f.Query.CurrentProfile = nil
	if validateFixture(f) == nil {
		t.Fatal("missing expected current profile accepted")
	}
}
