package peerauth

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type deadlineResponse struct {
	*httptest.ResponseRecorder
	mu       sync.Mutex
	deadline time.Time
}

func (w *deadlineResponse) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deadline = deadline
	return nil
}

func TestHandlerRejectsPublicCredentialsDomainAndUnsupportedDeadline(t *testing.T) {
	m, key, options, _ := membershipFixture(t)
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	called := false
	handler := s.ProtectHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	for _, domain := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodPost, "/peer", nil)
		req.Header.Set("Authorization", "Bearer security-admin")
		req.Header.Set(DomainHeader, domain)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if called {
			t.Fatal("public credential bypassed peer membership")
		}
	}
	// Verified TLS evidence alone does not prove a usable bounded transport.
	uri, _ := url.Parse(m.Members[0].Identity)
	leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("peer"), NotBefore: options.Now().Add(-time.Hour), NotAfter: options.Now().Add(time.Hour)}
	m.Version++
	m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	if err := s.Apply(signFixture(t, m, key)); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/peer", nil)
	digest := m.Domain.Digest()
	req.Header.Set(DomainHeader, hex.EncodeToString(digest[:]))
	req.TLS = &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	for _, header := range []string{"Authorization", "Cookie"} {
		attempted := req.Clone(req.Context())
		attempted.Header.Set(header, "public-credential")
		bounded := &deadlineResponse{ResponseRecorder: httptest.NewRecorder()}
		handler.ServeHTTP(bounded, attempted)
		if called || bounded.Code != http.StatusServiceUnavailable {
			t.Fatal("public credential accepted with verified private TLS", header, bounded.Code)
		}
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if called {
		t.Fatal("unbounded transport admitted")
	}
}

func TestHandlerRemovalCancelsEstablishedRequest(t *testing.T) {
	m, key, options, _ := membershipFixture(t)
	options.Now = time.Now
	m.IssuedAt, m.ExpiresAt = time.Now().UTC(), time.Now().UTC().Add(time.Minute)
	uri, _ := url.Parse(m.Members[0].Identity)
	leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("peer"), NotBefore: m.IssuedAt.Add(-time.Hour), NotAfter: m.IssuedAt.Add(time.Hour)}
	m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	entered, cancelled, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handler := s.ProtectHandler(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		if a, ok := AdmissionFromContext(req.Context()); !ok || a.Member().ID != m.Members[0].ID {
			t.Error("missing private workload evidence")
		}
		close(entered)
		<-req.Context().Done()
		close(cancelled)
	}))
	req := httptest.NewRequest(http.MethodPost, "/peer", nil)
	digest := m.Domain.Digest()
	req.Header.Set(DomainHeader, hex.EncodeToString(digest[:]))
	req.TLS = &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	w := &deadlineResponse{ResponseRecorder: httptest.NewRecorder()}
	go func() { defer close(done); handler.ServeHTTP(w, req) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not admitted")
	}
	updated := m
	updated.Version++
	updated.Members = []Member{{ID: [16]byte{8}, Identity: "spiffe://lantern.test/node-b", SPKI: [32]byte{9}, Origin: "https://localhost:6382"}}
	if err := s.Apply(signFixture(t, updated, key)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("removed peer retained established request")
	}
	<-done
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.deadline.IsZero() {
		t.Fatal("transport deadline left on reused response")
	}
}
