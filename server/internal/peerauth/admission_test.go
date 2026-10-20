package peerauth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAdmissionChecksIntervalNotBeforeAndExpiry(t *testing.T) {
	for _, profile := range []string{"control", "current_data"} {
		t.Run(profile, func(t *testing.T) {
			m, key, opts, now := controlFixture(t)
			uri, _ := url.Parse(m.Profile.Voters[0].Workload.Identity)
			leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("interval workload"), NotBefore: *now, NotAfter: now.Add(20 * time.Second)}
			m.Profile.Voters[0].Workload.SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
			opts.Profile, opts.Self = m.Profile, m.Profile.Voters[0].Workload
			low, high := *now, now.Add(time.Second)
			var sourceErr error
			opts.TimeBounds = func() (time.Time, time.Time, error) { return low, high, sourceErr }
			var s *Store
			var err error
			if profile == "control" {
				control, e := CreateControlStore(opts, signControlFixture(t, m, key))
				err = e
				if control != nil {
					s = control.store
				}
			} else {
				domain := Domain{Deployment: [16]byte{1}, NamespaceFormat: "namespaced-v1", AuthMode: "oidc", TrustDigest: [32]byte{2}, SecurityGeneration: [16]byte{1}, CurrentProfile: "current-v2:" + strings.Repeat("ab", 32)}
				data := Manifest{Version: 1, Domain: domain, IssuedAt: m.IssuedAt, ExpiresAt: m.ExpiresAt, Members: []Member{opts.Self}}
				s, err = CreateStore(StoreOptions{Path: opts.Path, Key: opts.Key, Domain: domain, Self: opts.Self, Now: opts.Now, TimeBounds: opts.TimeBounds}, signFixture(t, data, key))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			}()
			state := &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
			a, err := s.Admit(state, opts.Self.Origin)
			if err != nil {
				t.Fatal(err)
			}
			sourceErr = errors.New("no qualified interval")
			if a.Check(t.Context()) == nil {
				t.Fatal("admission retained authority without a current interval")
			}
			if _, err := s.Admit(state, opts.Self.Origin); err == nil {
				t.Fatal("new admission ignored source error")
			}
			sourceErr = nil
			if a.Check(t.Context()) != nil {
				t.Fatal("unexpired admission did not recover before request cancellation")
			}
			leaf.NotBefore = now.Add(500 * time.Millisecond)
			if _, err := s.Admit(state, opts.Self.Origin); err == nil {
				t.Fatal("not-before checked against high only")
			}
			leaf.NotBefore = *now
			low = now.Add(-time.Nanosecond)
			if a.Check(t.Context()) == nil {
				t.Fatal("captured admission forgot interval lower endpoint")
			}
			low = *now
			high = leaf.NotAfter.Add(-ClockMargin)
			if a.Check(t.Context()) == nil {
				t.Fatal("certificate expiry equality accepted")
			}
		})
	}
}

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

func TestAdmissionOriginalBudgetSurvivesNarrowingAndResponseRevalidation(t *testing.T) {
	m, key, options, now := membershipFixture(t)
	uri, _ := url.Parse(m.Members[0].Identity)
	leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("budget peer"), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	owner := new(byte)
	sample := peerTestObservation{owner, *now, now.Add(2 * time.Second), 0}
	options.Time = func() (TimeObservation, error) { return sample, nil }
	s, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}()
	state := &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	a, err := s.Admit(state, "")
	if err != nil {
		t.Fatal(err)
	}
	originalDeadline := a.budget.deadline
	sample.high = *now
	sample.elapsed = 29 * time.Second
	b, err := s.Admit(state, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.restrict(a); err != nil {
		t.Fatal(err)
	}
	remaining, err := b.remaining(t.Context())
	if err != nil || remaining > time.Second || b.budget.deadline != originalDeadline {
		t.Fatal("response minted later budget", remaining, err)
	}
	sample.elapsed = 30 * time.Second
	if a.Check(t.Context()) == nil || b.Check(t.Context()) == nil {
		t.Fatal("native expiry equality admitted after narrowing")
	}
	if s.FaultReason() != "none" {
		t.Fatal("honest narrowing latched scalar rollback")
	}
}

func TestAdmissionKeepsShortHandshakeBudgetWhenUTCNarrows(t *testing.T) {
	m, key, options, now := membershipFixture(t)
	uri, _ := url.Parse(m.Members[0].Identity)
	leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("short handshake peer"), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(12*time.Second + ClockMargin)}
	m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	sample := peerTestObservation{new(byte), *now, now.Add(2 * time.Second), 0}
	options.Time = func() (TimeObservation, error) { return sample, nil }
	store, err := CreateStore(options, signFixture(t, m, key))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()
	original, err := store.admitOrigin(m.Members[0].Origin, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sample.elapsed = 5 * time.Second
	sample.high = now.Add(10 * time.Second)
	state := &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	handshake, err := store.Admit(state, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := handshake.restrict(original); err != nil {
		t.Fatal(err)
	}
	sample.elapsed = 8 * time.Second
	sample.high = now.Add(11 * time.Second)
	if handshake.Check(t.Context()) == nil {
		t.Fatal("original 30s budget erased the shorter frozen handshake budget")
	}
}
