package peerauth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"reflect"
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

func TestAdmissionSamePeerBindsStoreAndCompleteMember(t *testing.T) {
	store := &Store{}
	member := Member{ID: [16]byte{1}, Identity: "spiffe://lantern.test/node-a", SPKI: [32]byte{2}, Origin: "https://localhost:6381"}
	original := &Admission{store: store, member: member, expiresAt: time.Unix(100, 0)}
	fresh := &Admission{store: store, member: member, expiresAt: time.Unix(200, 0)}
	if !original.SamePeer(fresh) || !fresh.SamePeer(original) {
		t.Fatal("independent admissions for the exact store/workload did not match")
	}
	for _, tc := range []struct {
		name  string
		other *Admission
	}{
		{"nil admission", nil},
		{"zero admission", &Admission{}},
		{"same member in different store", &Admission{store: &Store{}, member: member}},
		{"different ID", &Admission{store: store, member: Member{ID: [16]byte{3}, Identity: member.Identity, SPKI: member.SPKI, Origin: member.Origin}}},
		{"different identity", &Admission{store: store, member: Member{ID: member.ID, Identity: "spiffe://lantern.test/node-b", SPKI: member.SPKI, Origin: member.Origin}}},
		{"different SPKI", &Admission{store: store, member: Member{ID: member.ID, Identity: member.Identity, SPKI: [32]byte{3}, Origin: member.Origin}}},
		{"different origin", &Admission{store: store, member: Member{ID: member.ID, Identity: member.Identity, SPKI: member.SPKI, Origin: "https://localhost:6382"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if original.SamePeer(tc.other) || tc.other.SamePeer(original) {
				t.Fatal("different or absent peer matched original admission")
			}
		})
	}
	if (*Admission)(nil).SamePeer(nil) || (&Admission{}).SamePeer(&Admission{}) {
		t.Fatal("absent stores matched")
	}
}

func TestAdmissionDiagnosticsPreserveErrorsAndOriginalBudget(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		known        bool
	}{
		{"missing admission", "admission_missing", false},
		{"missing store", "admission_missing", false},
		{"canceled", "context_canceled", false},
		{"deadline", "context_deadline", false},
		{"time unavailable", "time_unavailable", false},
		{"store faulted", "store_faulted", false},
		{"certificate not started", "certificate_not_started", false},
		{"admission expired", "admission_expired", false},
		{"membership not live", "membership_not_live", false},
		{"member changed", "member_changed", false},
		{"native lifetime expired", "native_lifetime_expired", true},
		{"accepted", "none", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, key, options, now := membershipFixture(t)
			uri, _ := url.Parse(m.Members[0].Identity)
			leaf := &x509.Certificate{URIs: []*url.URL{uri}, RawSubjectPublicKeyInfo: []byte("diagnostic peer"), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
			m.Members[0].SPKI = sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
			sample := peerTestObservation{new(byte), *now, *now, 0}
			var sourceErr error
			samples := 0
			options.Time = func() (TimeObservation, error) {
				samples++
				if sourceErr != nil {
					return nil, sourceErr
				}
				return sample, nil
			}
			store, err := CreateStore(options, signFixture(t, m, key))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			a, err := store.Admit(&tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}, "")
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			wantErr := ErrMembership
			wantSamples := 1
			switch tc.name {
			case "missing admission":
				a, wantSamples = nil, 0
			case "missing store":
				a, wantSamples = &Admission{}, 0
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				wantErr, wantSamples = context.Canceled, 0
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
				wantErr, wantSamples = context.DeadlineExceeded, 0
			case "time unavailable":
				sourceErr = errors.New("private source failure must not escape")
			case "store faulted":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			case "certificate not started":
				sample.low = a.startsAt.Add(-time.Nanosecond)
			case "admission expired":
				sample.high = a.expiresAt
			case "membership not live":
				m.Version++
				m.ExpiresAt = now.Add(5 * time.Second)
				if err := store.Apply(signFixture(t, m, key)); err != nil {
					t.Fatal(err)
				}
				sample.low, sample.high = now.Add(6*time.Second), now.Add(6*time.Second)
			case "member changed":
				m.Version++
				m.Members[0] = Member{ID: [16]byte{5}, Identity: "spiffe://lantern.test/replacement", SPKI: [32]byte{6}, Origin: "https://localhost:6382"}
				if err := store.Apply(signFixture(t, m, key)); err != nil {
					t.Fatal(err)
				}
			case "native lifetime expired":
				sample.elapsed = MaxRPCLifetime
			case "accepted":
				wantErr = nil
			}
			var before Admission
			if a != nil {
				before = *a
			}
			samples = 0
			diagnostics, err := a.CheckWithDiagnostics(ctx)
			if err != wantErr || diagnostics.Reason != tc.reason || diagnostics.RemainingKnown != tc.known || samples != wantSamples {
				t.Fatalf("diagnostic check = (%+v, %v), samples=%d; want reason=%s known=%t error=%v samples=%d", diagnostics, err, samples, tc.reason, tc.known, wantErr, wantSamples)
			}
			if wantErr != nil && diagnostics.OriginalBudgetRemaining != 0 {
				t.Fatal("rejected check reported usable budget", diagnostics)
			}
			if wantErr == nil && (diagnostics.OriginalBudgetRemaining <= 0 || diagnostics.OriginalBudgetRemaining > MaxRPCLifetime) {
				t.Fatal("accepted check lost its bounded original budget", diagnostics)
			}
			samples = 0
			if err := a.Check(ctx); err != wantErr || samples != wantSamples {
				t.Fatalf("Check error/samples changed: %v %d", err, samples)
			}
			samples = 0
			remaining, err := a.remaining(ctx)
			if err != wantErr || samples != wantSamples || wantErr != nil && remaining != 0 {
				t.Fatalf("remaining error/samples changed: %v %d remaining=%v", err, samples, remaining)
			}
			if a != nil && !reflect.DeepEqual(*a, before) {
				t.Fatal("diagnostic check changed the original admission")
			}
		})
	}
}
