package peerauth

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"time"
)

const MaxRPCLifetime = 30 * time.Second

// Admission is private workload evidence, never a security Principal/Role.
// Its original expiry cannot be extended by membership reload or local state.
type Admission struct {
	store     *Store
	member    Member
	expiresAt time.Time
	startsAt  time.Time
}

func (a *Admission) Member() Member       { return a.member }
func (a *Admission) ExpiresAt() time.Time { return a.expiresAt }

func (s *Store) Admit(state *tls.ConnectionState, expectedOrigin string) (*Admission, error) {
	if s == nil || state == nil || state.Version < tls.VersionTLS13 || len(state.VerifiedChains) == 0 ||
		len(state.PeerCertificates) == 0 || len(state.PeerCertificates[0].URIs) != 1 {
		return nil, ErrMembership
	}
	leaf := state.PeerCertificates[0]
	identity := leaf.URIs[0].String()
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || !s.liveLocked(now) {
		return nil, ErrMembership
	}
	member, known := s.current.members[identity]
	if !known || member.SPKI != sha256.Sum256(leaf.RawSubjectPublicKeyInfo) ||
		expectedOrigin != "" && expectedOrigin != member.Origin {
		return nil, ErrMembership
	}
	expiry := s.current.manifest.ExpiresAt.Add(-ClockMargin)
	// Select a verified chain for this exact leaf and bound the admission by
	// every certificate in that chain. A chain cached by TLS is not immortal.
	chainExpiry := time.Time{}
	chainStart := time.Time{}
	for _, chain := range state.VerifiedChains {
		if len(chain) == 0 || !chain[0].Equal(leaf) {
			continue
		}
		candidate := chain[0].NotAfter
		candidateStart := chain[0].NotBefore
		usable := true
		for _, cert := range chain {
			usable = usable && !s.sampleLow.Before(cert.NotBefore) && now.Add(ClockMargin).Before(cert.NotAfter)
			if cert.NotBefore.After(candidateStart) {
				candidateStart = cert.NotBefore
			}
			if cert.NotAfter.Before(candidate) {
				candidate = cert.NotAfter
			}
		}
		if usable && candidate.After(chainExpiry) {
			chainExpiry = candidate
			chainStart = candidateStart
		}
	}
	if chainExpiry.IsZero() {
		return nil, ErrMembership
	}
	for _, bound := range []time.Time{chainExpiry.Add(-ClockMargin), now.Add(MaxRPCLifetime)} {
		if bound.Before(expiry) {
			expiry = bound
		}
	}
	return &Admission{store: s, member: member, expiresAt: expiry, startsAt: chainStart}, nil
}

func (a *Admission) Check(ctx context.Context) error {
	if a == nil || a.store == nil {
		return ErrMembership
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s := a.store
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || s.sampleLow.Before(a.startsAt) || !now.Before(a.expiresAt) || !s.liveLocked(now) ||
		s.current.members[a.member.Identity] != a.member {
		return ErrMembership
	}
	return nil
}

type admissionContextKey struct{}

func WithAdmission(ctx context.Context, admission *Admission) context.Context {
	return context.WithValue(ctx, admissionContextKey{}, admission)
}

func AdmissionFromContext(ctx context.Context) (*Admission, bool) {
	a, ok := ctx.Value(admissionContextKey{}).(*Admission)
	return a, ok && a != nil
}
