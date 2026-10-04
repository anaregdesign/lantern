package replication

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func trustedTestPeer(t *testing.T, handler http.Handler) (*httptest.Server, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
		DNSNames:    []string{"example.com"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return srv, ca
}

func TestAuthenticatedPeerTransportStatic(t *testing.T) {
	const token = "not-logged-peer-token"
	var accepted atomic.Int32
	peer, ca := trustedTestPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("peer did not receive the expected bearer")
		}
		accepted.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	transport, err := NewAuthenticatedPeerTransport(ca, nil, token, []string{peer.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	url, err := transport.BaseURL(peer.URL)
	if err != nil || url != peer.URL {
		t.Fatalf("static peer URL = %q, %v", url, err)
	}
	resp, err := transport.client.Get(url)
	if err != nil {
		t.Fatalf("trusted peer: %v", err)
	}
	resp.Body.Close()
	if resp.ProtoMajor != 2 || accepted.Load() != 1 {
		t.Fatalf("trusted peer: protocol=%s accepted=%d", resp.Proto, accepted.Load())
	}
	if _, err := transport.BaseURL("http://" + strings.TrimPrefix(url, "https://")); err == nil {
		t.Fatal("accepted a plaintext static peer")
	}
	if _, err := transport.BaseURL("https://unlisted.example:6380"); err == nil {
		t.Fatal("accepted an unapproved peer")
	}
	if _, err := transport.client.Get("http://" + strings.TrimPrefix(url, "https://")); err == nil ||
		!strings.Contains(err.Error(), "unapproved HTTPS origin") {
		t.Fatalf("plaintext request = %v, want rejection before sending bearer", err)
	}
	if accepted.Load() != 1 {
		t.Fatal("rejected request reached the peer")
	}
}

func TestAuthenticatedPeerTransportRejectsRedirectsAndBadCertificates(t *testing.T) {
	var redirected atomic.Int32
	var sourceRequests atomic.Int32
	destination, _ := trustedTestPeer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	source, ca := trustedTestPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceRequests.Add(1)
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	token := "redirection-is-not-an-authorization"
	transport, err := NewAuthenticatedPeerTransport(ca, nil, token, []string{source.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.client.Get(source.URL); err == nil || !strings.Contains(err.Error(), "redirect rejected") {
		t.Fatalf("redirect = %v, want an explicit refusal", err)
	}
	if redirected.Load() != 0 {
		t.Fatal("followed a peer redirect")
	}
	_, otherCA := trustedTestPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("sent bearer to untrusted peer")
	}))
	other, err := NewAuthenticatedPeerTransport(otherCA, nil, token, []string{source.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.client.Get(source.URL); err == nil {
		t.Fatal("accepted peer certificate outside pinned CA")
	}
	_, port, err := net.SplitHostPort(strings.TrimPrefix(source.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	mismatchedName := "https://localhost:" + port
	wrongName, err := NewAuthenticatedPeerTransport(ca, nil, token, []string{mismatchedName}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, nameErr := wrongName.client.Get(mismatchedName)
	if nameErr == nil || !strings.Contains(nameErr.Error(), "certificate") {
		t.Fatalf("static peer hostname mismatch = %v, want TLS verification failure", nameErr)
	}
	if sourceRequests.Load() != 1 {
		t.Fatalf("mismatched static peer sent a request before certificate verification: %d requests, error=%v", sourceRequests.Load(), nameErr)
	}
}

func TestAuthenticatedPeerTransportDNSIdentity(t *testing.T) {
	const token = "dns-identity-token"
	var received atomic.Int32
	peer, ca := trustedTestPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("DNS peer did not receive the expected bearer")
		}
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	host, port, err := net.SplitHostPort(strings.TrimPrefix(peer.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	transport, err := NewAuthenticatedPeerTransport(ca, nil, token, nil, "example.com", port)
	if err != nil {
		t.Fatal(err)
	}
	url, err := transport.BaseURL(net.JoinHostPort(host, port))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.client.Get(url)
	if err != nil {
		t.Fatalf("DNS IP dial against certificate name: %v", err)
	}
	resp.Body.Close()
	if received.Load() != 1 {
		t.Fatal("verified DNS peer received no authenticated request")
	}
	for _, addr := range []string{
		"http://" + net.JoinHostPort(host, port),
		net.JoinHostPort(host, "1"),
		net.JoinHostPort("untrusted.example.com", port),
		"127.0.0.1:443/path",
	} {
		if _, err := transport.BaseURL(addr); err == nil {
			t.Errorf("accepted invalid DNS address %q", addr)
		}
	}
	mismatch, err := NewAuthenticatedPeerTransport(ca, nil, token, nil, "unrelated.invalid", port)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mismatch.client.Get(url); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("DNS identity mismatch = %v, want TLS verification failure", err)
	}
	if received.Load() != 1 {
		t.Fatal("mismatched DNS identity sent a bearer")
	}
}

func TestAuthenticatedPeerTransportRejectsConfiguration(t *testing.T) {
	_, ca := trustedTestPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, tc := range []struct {
		name, peer, dnsName, port string
	}{
		{"bare static address", "peer:6380", "", ""},
		{"plaintext static URL", "http://peer:6380", "", ""},
		{"credential in URL", "https://user:pass@peer:6380", "", ""},
		{"path in URL", "https://peer:6380/snapshot", "", ""},
		{"fragment in URL", "https://peer:6380#fragment", "", ""},
		{"invalid static port", "https://peer:0", "", ""},
		{"invalid DNS identity", "", "https://peer", "6380"},
		{"invalid DNS port", "", "peer.example", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var peers []string
			if tc.peer != "" {
				peers = []string{tc.peer}
			}
			if _, err := NewAuthenticatedPeerTransport(ca, nil, "token", peers, tc.dnsName, tc.port); err == nil {
				t.Fatalf("accepted invalid peer config: %s", tc.name)
			}
		})
	}
	if _, err := NewAuthenticatedPeerTransport([]byte("not a CA"), nil, "token",
		[]string{"https://peer:6380"}, "", ""); err == nil {
		t.Fatal("accepted malformed CA")
	}
	if _, err := NewAuthenticatedPeerTransport(ca, nil, "", []string{"https://peer:6380"}, "", ""); err == nil {
		t.Fatal("accepted empty bearer")
	}
	if _, err := NewAuthenticatedPeerTransport(ca, nil, "token",
		[]string{"https://peer:6380"}, "peer.example", "6380"); err == nil {
		t.Fatal("accepted ambiguous static and DNS peers")
	}
	if _, err := NewAuthenticatedPeerTransport(ca, &tls.Certificate{}, "token",
		[]string{"https://peer:6380"}, "", ""); err != nil {
		t.Fatalf("outbound mTLS certificate option: %v", err)
	}
}

func TestVerifiedPeerTransportUsesCurrentMembershipWithoutPublicCredentials(t *testing.T) {
	eligible := true
	client := &http.Client{Transport: http.DefaultTransport}
	transport, err := NewVerifiedPeerTransport(client, func(origin string) bool { return eligible && origin == "https://localhost:6391" })
	if err != nil {
		t.Fatal(err)
	}
	if url, err := transport.BaseURL("https://localhost:6391"); err != nil || url != "https://localhost:6391" {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://localhost:6391", "https://localhost:6380", "https://localhost:6391/path"} {
		if _, err := transport.BaseURL(origin); err == nil {
			t.Fatal("unapproved origin eligible", origin)
		}
	}
	eligible = false
	if _, err := transport.BaseURL("https://localhost:6391"); err == nil {
		t.Fatal("removed member still eligible")
	}
	if transport.client != client {
		t.Fatal("certified client replaced")
	}
	if _, err := NewVerifiedPeerTransport(&http.Client{}, func(string) bool { return true }); err == nil {
		t.Fatal("default ambient client accepted")
	}
}
