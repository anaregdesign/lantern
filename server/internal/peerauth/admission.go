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
	budget    rpcBudget
}

func (a *Admission) Member() Member       { return a.member }
func (a *Admission) ExpiresAt() time.Time { return a.expiresAt }

// SamePeer binds two admissions to the exact workload and membership store.
// It grants no authority: callers must still Check both original lifetimes.
func (a *Admission) SamePeer(other *Admission) bool {
	return a != nil && other != nil && a.store != nil &&
		a.store == other.store && a.member == other.member
}

func (s *Store) Admit(state *tls.ConnectionState, expectedOrigin string) (*Admission, error) {
	started := time.Now()
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
	window, err := SelectCertificateWindow(state.VerifiedChains, leaf, s.sampleLow, now)
	if err != nil {
		return nil, err
	}
	chainExpiry, chainStart := window.End, window.Start
	for _, self := range s.options.SelfCertificates {
		if self.End.Before(chainExpiry) {
			chainExpiry = self.End
		}
		if self.Start.After(chainStart) {
			chainStart = self.Start
		}
	}

	for _, bound := range []time.Time{chainExpiry.Add(-ClockMargin), now.Add(MaxRPCLifetime)} {
		if bound.Before(expiry) {
			expiry = bound
		}
	}
	budget, err := newRPCBudget(started, s.sample, now, expiry)
	if err != nil {
		return nil, err
	}
	return &Admission{store: s, member: member, expiresAt: expiry, startsAt: chainStart, budget: budget}, nil
}

func (a *Admission) Check(ctx context.Context) error {
	_, err := a.remaining(ctx)
	return err
}

func (a *Admission) remaining(ctx context.Context) (time.Duration, error) {
	if a == nil || a.store == nil {
		return 0, ErrMembership
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s := a.store
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || s.sampleLow.Before(a.startsAt) || !now.Before(a.expiresAt) || !s.liveLocked(now) ||
		s.current.members[a.member.Identity] != a.member {
		return 0, ErrMembership
	}
	return a.budget.remaining(s.sample, now, a.expiresAt)
}

func (a *Admission) deadline(ctx context.Context) (time.Time, error) {
	scheduled := time.Now()
	remaining, err := a.remaining(ctx)
	if err != nil {
		return time.Time{}, err
	}
	deadline := scheduled.Add(remaining)
	if a.budget.deadline.Before(deadline) {
		deadline = a.budget.deadline
	}
	return deadline, nil
}

// Restrict to the earliest request capability, never mint a new response budget.
func (a *Admission) restrict(prior *Admission) error {
	if a == nil || prior == nil || a.store != prior.store || a.member != prior.member {
		return ErrMembership
	}
	budget, err := a.budget.intersect(prior.budget)
	if err != nil {
		return err
	}
	if prior.expiresAt.Before(a.expiresAt) {
		a.expiresAt = prior.expiresAt
	}
	if prior.startsAt.After(a.startsAt) {
		a.startsAt = prior.startsAt
	}
	a.budget = budget
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

// Capture the outbound lifetime before membership/TLS/response setup. This is
// restricted by the verified peer chain as soon as the handshake completes.
func (s *Store) admitOrigin(origin string, started time.Time) (*Admission, error) {
	if s == nil {
		return nil, ErrMembership
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	high, valid := s.nowLocked()
	if !valid || !s.liveLocked(high) {
		return nil, ErrMembership
	}
	var member Member
	for _, candidate := range s.current.manifest.Members {
		if candidate.Origin == origin {
			member = candidate
			break
		}
	}
	if member == (Member{}) || member.Identity == s.options.Self.Identity {
		return nil, ErrMembership
	}
	expiry := s.current.manifest.ExpiresAt.Add(-ClockMargin)
	var start time.Time
	for _, window := range s.options.SelfCertificates {
		if window.Start.After(start) {
			start = window.Start
		}
		if window.End.Add(-ClockMargin).Before(expiry) {
			expiry = window.End.Add(-ClockMargin)
		}
	}
	if high.Add(MaxRPCLifetime).Before(expiry) {
		expiry = high.Add(MaxRPCLifetime)
	}
	budget, err := newRPCBudget(started, s.sample, high, expiry)
	if err != nil {
		return nil, err
	}
	return &Admission{store: s, member: member, expiresAt: expiry, startsAt: start, budget: budget}, nil
}
