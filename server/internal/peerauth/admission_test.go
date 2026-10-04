package peerauth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"net/url"
	"testing"
	"time"
)

func TestAdmissionRequiresVerifiedExactWorkloadAndBounds(t *testing.T) {
	m, key, options, now := membershipFixture(t)
	uri, _ := url.Parse(m.Members[0].Identity)
	leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("peer key"), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	state := &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	a, err := s.Admit(state, m.Members[0].Origin)
	if err != nil || a.Member() != m.Members[0] || a.Check(t.Context()) != nil || !a.ExpiresAt().Equal(now.Add(MaxRPCLifetime)) {
		t.Fatal("valid admission rejected", err)
	}
	if _, err := s.Admit(state, "https://localhost:6382"); err == nil {
		t.Fatal("workload admitted at another approved origin")
	}
	for _, mutate := range []func(*tls.ConnectionState){
		func(s *tls.ConnectionState) { s.Version = tls.VersionTLS12 },
		func(s *tls.ConnectionState) { s.VerifiedChains = nil },
		func(s *tls.ConnectionState) { s.PeerCertificates = nil },
		func(s *tls.ConnectionState) {
			clone := *leaf
			clone.URIs = append(clone.URIs, uri)
			s.PeerCertificates = []*x509.Certificate{&clone}
		},
		func(s *tls.ConnectionState) {
			clone := *leaf
			clone.RawSubjectPublicKeyInfo = []byte("wrong key")
			s.PeerCertificates = []*x509.Certificate{&clone}
		},
	} {
		candidate := *state
		mutate(&candidate)
		if _, err := s.Admit(&candidate, ""); err == nil {
			t.Fatal("unverified/ambiguous certificate admitted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if a.Check(ctx) == nil {
		t.Fatal("cancelled request retained authority")
	}
	// A signed renewal cannot extend an already admitted request.
	m.Version++
	m.ExpiresAt = now.Add(5 * time.Minute)
	if s.Apply(signFixture(t, m, key)) != nil {
		t.Fatal("renewal rejected")
	}
	*now = now.Add(MaxRPCLifetime)
	if a.Check(t.Context()) == nil {
		t.Fatal("old admission extended by renewal")
	}
}

func TestAdmissionRemovalInvalidatesCapturedMember(t *testing.T) {
	m, key, options, now := membershipFixture(t)
	uri, _ := url.Parse(m.Members[0].Identity)
	leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("peer key"), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a, err := s.Admit(&tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	m.Version++
	m.Members[0] = Member{ID: [16]byte{5}, Identity: "spiffe://lantern.test/node-b", SPKI: [32]byte{6}, Origin: "https://localhost:6382"}
	if s.Apply(signFixture(t, m, key)) != nil || a.Check(t.Context()) == nil {
		t.Fatal("removed workload retained admission")
	}
}
