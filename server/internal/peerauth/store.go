package peerauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

type StoreOptions struct {
	Path   string
	Key    ed25519.PublicKey
	Domain Domain
	Self   Member
	Now    func() time.Time
}

// Store owns one complete signed checkpoint under the existing native path
// lease. No request appends history or renews expiry from a local file.
type Store struct {
	mu          sync.Mutex
	options     StoreOptions
	lease       *mutationlog.FileWALLease
	current     *membershipSnapshot
	lastWall    time.Time
	faulted     bool
	faultReason string
	// Only the typed control constructor sets this. Legacy Domain remains
	// unchanged; no synthetic OFF/OIDC domain represents control membership.
	controlBinding *[32]byte
	timeBounds     func() (low, high time.Time, err error)
	sampleLow      time.Time // Same sample as nowLocked's returned high; owned by mu.
}

type membershipSnapshot struct {
	manifest Manifest
	raw      []byte
	digest   [32]byte
	members  map[string]Member
}

func prepareStore(options StoreOptions) (*Store, error) {
	if !filepath.IsAbs(options.Path) || len(options.Key) != ed25519.PublicKeySize || !options.Domain.valid() {
		return nil, ErrMembership
	}
	options.Key = bytes.Clone(options.Key)
	if options.Now == nil {
		options.Now = time.Now
	}
	lease, err := mutationlog.AcquireFileWALLease(options.Path)
	if err != nil {
		return nil, err
	}
	return &Store{options: options, lease: lease, lastWall: options.Now()}, nil
}

// CreateStore is explicit genesis and never overwrites a partial/existing cut.
// ResumeStore is mandatory for a previously initialized deployment path.
func CreateStore(options StoreOptions, raw []byte) (*Store, error) {
	m, err := decodeManifest(raw, options.Key, options.Domain)
	if err != nil {
		return nil, err
	}
	s, err := prepareStore(options)
	if err != nil {
		return nil, err
	}
	if !manifestLive(m, s.options.Now()) {
		return nil, errors.Join(ErrMembership, s.Close())
	}
	if err := writeCheckpoint(options.Path, raw, true); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	s.current = snapshot(m, raw)
	return s, nil
}

func ResumeStore(options StoreOptions) (*Store, error) {
	s, err := prepareStore(options)
	if err != nil {
		return nil, err
	}
	raw, err := readCheckpoint(options.Path)
	if err != nil {
		return nil, errors.Join(err, s.Close())
	}
	m, err := decodeManifest(raw, options.Key, options.Domain)
	if err != nil {
		return nil, errors.Join(err, s.Close())
	}
	// Expired signed state retains its version floor while accepting no peers.
	// A fresh operator-signed higher revision may restore membership serving.
	s.current = snapshot(m, raw)
	return s, nil
}

func snapshot(m Manifest, raw []byte) *membershipSnapshot {
	result := &membershipSnapshot{manifest: m, raw: bytes.Clone(raw), digest: sha256.Sum256(raw), members: make(map[string]Member, len(m.Members))}
	for _, member := range m.Members {
		result.members[member.Identity] = member
	}
	return result
}

func (s *Store) nowLocked() (time.Time, bool) {
	if s.controlBinding != nil && s.lease.WithPath(func(string) error { return nil }) != nil {
		s.faultLocked("checkpoint_ownership")
	}
	if s.timeBounds != nil {
		low, high, err := s.timeBounds()
		if err != nil || low.IsZero() || high.IsZero() || low.After(high) {
			return time.Time{}, false
		}
		// A tighter honest interval may lower its high endpoint. The native
		// producer, rather than that endpoint, detects counter/epoch rollback.
		s.sampleLow, s.lastWall = low, high
		return high, !s.faulted
	}
	now := s.options.Now()
	s.sampleLow = now
	if now.UnixNano() < s.lastWall.UnixNano() {
		s.faultLocked("clock_rollback")
	}
	s.lastWall = now
	return now, !s.faulted
}

func (s *Store) faultLocked(reason string) {
	if !s.faulted {
		s.faulted, s.faultReason = true, reason
	}
}

// FaultReason returns a fixed private diagnostic category for an already
// latched fault. Inspection neither samples time nor changes serving authority.
func (s *Store) FaultReason() string {
	if s == nil {
		return "unavailable"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.faulted {
		return "none"
	}
	return s.faultReason
}

func (s *Store) liveLocked(now time.Time) bool {
	return s.current != nil && s.manifestLiveLocked(s.current.manifest, now) &&
		(s.options.Self == (Member{}) || s.current.members[s.options.Self.Identity] == s.options.Self)
}

func (s *Store) manifestLiveLocked(m Manifest, high time.Time) bool {
	return manifestLive(m, high) && (s.timeBounds == nil || !s.sampleLow.Before(m.IssuedAt))
}

func (s *Store) Apply(raw []byte) error {
	if s == nil {
		return ErrMembership
	}
	m, err := decodeManifest(raw, s.options.Key, s.options.Domain)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || !manifestLive(m, now) {
		return ErrMembership
	}
	if m.Version == s.current.manifest.Version && bytes.Equal(raw, s.current.raw) {
		return nil
	}
	if m.Version <= s.current.manifest.Version {
		return ErrMembership
	}
	if err := writeCheckpoint(s.options.Path, raw, false); err != nil {
		s.faultLocked("checkpoint_failure")
		return errors.Join(ErrMembership, err)
	}
	s.current = snapshot(m, raw)
	return nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faultLocked("closed")
	return s.lease.Close()
}

func (s *Store) Member(identity string) (Member, time.Time, error) {
	if s == nil {
		return Member{}, time.Time{}, ErrMembership
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || !s.liveLocked(now) {
		return Member{}, time.Time{}, ErrMembership
	}
	member, known := s.current.members[identity]
	if !known {
		return Member{}, time.Time{}, ErrMembership
	}
	return member, s.current.manifest.ExpiresAt.Add(-ClockMargin), nil
}

func (s *Store) Domain() Domain { return s.options.Domain }

func (s *Store) wireProfile() (string, [32]byte) {
	if s.controlBinding != nil {
		return ControlHeader, *s.controlBinding
	}
	return DomainHeader, s.Domain().Digest()
}

func (s *Store) Origins() ([]string, error) {
	if s == nil {
		return nil, ErrMembership
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || !s.liveLocked(now) {
		return nil, ErrMembership
	}
	result := make([]string, 0, len(s.current.manifest.Members))
	for _, member := range s.current.manifest.Members {
		if member.Identity != s.options.Self.Identity {
			result = append(result, member.Origin)
		}
	}
	return result, nil
}

// MemberAtOrigin is a local, current operator membership lookup. No network
// discovery or caller-owned identity can expand this set.
func (s *Store) MemberAtOrigin(origin string) (Member, time.Time, error) {
	if s == nil {
		return Member{}, time.Time{}, ErrMembership
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || !s.liveLocked(now) {
		return Member{}, time.Time{}, ErrMembership
	}
	for _, member := range s.current.manifest.Members {
		if member.Origin == origin {
			return member, s.current.manifest.ExpiresAt.Add(-ClockMargin), nil
		}
	}
	return Member{}, time.Time{}, ErrMembership
}
