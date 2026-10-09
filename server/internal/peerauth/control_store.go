package peerauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/anaregdesign/lantern/core/mutationlog"
)

// ControlFloor is an explicitly supplied membership minimum, not a promise,
// materialization receipt, policy freshness certificate or independent witness.
// Zero means the caller supplied no known-rollback witness.
type ControlFloor struct {
	Version uint64
	Digest  [32]byte
	Binding [32]byte
}

type ControlStoreOptions struct {
	Path    string
	Key     ed25519.PublicKey
	Profile ControlProfile
	Self    Member
	Now     func() time.Time
	// TimeBounds is an optional trusted composition input for the current
	// authority profile. The producer owns UTC uncertainty and source health;
	// this store checks both endpoints and never manufactures qualification.
	TimeBounds func() (low, high time.Time, err error)
}

// ControlStore shares the existing certificate/HTTP/checkpoint mechanisms but
// has a separate typed constructor and signed wire profile. No caller can mint
// verified workload evidence, reach the legacy Store, or replace this binding.
type ControlStore struct {
	store   *Store
	profile ControlProfile
	// Native persistence faults are injected only by this package's tests.
	checkpointHook func(string) error
}

func prepareControlStore(o ControlStoreOptions) (_ *ControlStore, err error) {
	if !filepath.IsAbs(o.Path) || len(o.Key) != ed25519.PublicKeySize || !o.Profile.Valid() || o.Self == (Member{}) {
		return nil, ErrMembership
	}
	found := false
	for _, v := range o.Profile.Voters {
		found = found || v.Workload == o.Self
	}
	if !found {
		return nil, ErrMembership
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.Profile.Voters = append([]ControlVoter(nil), o.Profile.Voters...)
	lease, err := mutationlog.AcquireFileWALLease(o.Path)
	if err != nil {
		return nil, err
	}
	// Own the lease before the first configured callback can panic. No caller
	// can release it until this constructor has returned a complete store.
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, lease.Close())
		}
	}()
	binding := o.Profile.Digest()
	s := &Store{options: StoreOptions{Path: o.Path, Key: bytes.Clone(o.Key), Self: o.Self, Now: o.Now}, lease: lease, lastWall: o.Now(), controlBinding: &binding}
	s.timeBounds = o.TimeBounds
	c := &ControlStore{store: s, profile: o.Profile}
	transferred = true
	return c, nil
}

func CreateControlStore(o ControlStoreOptions, raw []byte) (_ *ControlStore, err error) {
	m, err := VerifyControlManifest(raw, o.Key, o.Profile.Lineage)
	if err != nil || m.Profile.Digest() != o.Profile.Digest() {
		return nil, ErrMembership
	}
	c, err := prepareControlStore(o)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, c.Close())
		}
	}()
	s := c.store
	now, valid := s.nowLocked()
	if !valid || !s.manifestLiveLocked(controlSnapshot(m, raw).manifest, now) {
		return nil, ErrMembership
	}
	if err := writeCheckpoint(o.Path, raw, true); err != nil {
		return nil, err
	}
	s.current = controlSnapshot(m, raw)
	transferred = true
	return c, nil
}

func ResumeControlStore(o ControlStoreOptions, floor ControlFloor) (_ *ControlStore, err error) {
	if floor.Version == 0 && floor != (ControlFloor{}) || floor.Version != 0 &&
		(floor.Digest == [32]byte{} || floor.Binding != o.Profile.Digest()) {
		return nil, ErrMembership
	}
	c, err := prepareControlStore(o)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, c.Close())
		}
	}()
	raw, err := readCheckpoint(o.Path)
	if err != nil {
		return nil, err
	}
	m, err := VerifyControlManifest(raw, o.Key, o.Profile.Lineage)
	if err != nil || m.Profile.Digest() != c.profile.Digest() || m.Version < floor.Version ||
		m.Version == floor.Version && sha256.Sum256(raw) != floor.Digest {
		// A persisted same-lineage changed binding is a permanent fence even
		// when the caller presents the original manifest and a zero floor.
		return nil, ErrMembership
	}
	// Re-establish a durable checkpoint barrier before publishing recovered M.
	// This does not extend its signed lifetime or create a missing checkpoint.
	if err := writeCheckpoint(o.Path, raw, false); err != nil {
		return nil, err
	}
	c.store.current = controlSnapshot(m, raw)
	transferred = true
	return c, nil
}

func (c *ControlStore) Apply(raw []byte) error {
	if c == nil || c.store == nil {
		return ErrMembership
	}
	s := c.store
	m, err := VerifyControlManifest(raw, s.options.Key, c.profile.Lineage)
	if err != nil {
		return err // Foreign lineage cannot become a shutdown command.
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now, valid := s.nowLocked()
	if !valid || s.current == nil {
		return ErrMembership
	}
	if m.Version == s.current.manifest.Version && bytes.Equal(raw, s.current.raw) {
		return nil
	}
	if m.Version <= s.current.manifest.Version {
		return ErrMembership
	}
	changed := m.Profile.Digest() != c.profile.Digest()
	if !changed && !s.manifestLiveLocked(controlSnapshot(m, raw).manifest, now) {
		return ErrMembership
	}
	// A failed or panicking write may already have retained the new checkpoint.
	// Keep the guard under the store mutex until both memory and any terminal
	// binding fence reflect the completed checkpoint. Never retry over uncertainty.
	completed := false
	defer func() {
		if !completed {
			s.faultLocked("checkpoint_failure")
		}
	}()
	if err := writeCheckpointWithHook(s.options.Path, raw, false, c.checkpointHook); err != nil {
		return errors.Join(ErrMembership, err)
	}
	s.current = controlSnapshot(m, raw)
	if changed {
		s.faultLocked("control_binding_changed")
	}
	completed = true
	if changed {
		return ErrMembership
	}
	return nil
}

func (c *ControlStore) Floor() (ControlFloor, error) {
	if c == nil || c.store == nil {
		return ControlFloor{}, ErrMembership
	}
	s := c.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == nil || s.faulted && s.faultReason != "control_binding_changed" {
		return ControlFloor{}, ErrMembership
	}
	return ControlFloor{s.current.manifest.Version, s.current.digest, c.profile.Digest()}, nil
}

func (c *ControlStore) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.store == nil {
		return ErrMembership
	}
	_, _, err := c.store.Member(c.store.options.Self.Identity)
	return err
}

func (c *ControlStore) Admit(state *tls.ConnectionState, origin string) (*Admission, error) {
	if c == nil {
		return nil, ErrMembership
	}
	return c.store.Admit(state, origin)
}

func (c *ControlStore) ProtectHandler(next http.Handler) http.Handler {
	return c.store.ProtectHandler(next)
}

func (c *ControlStore) NewHTTPClient(cert tls.Certificate, roots *x509.CertPool) *http.Client {
	return NewHTTPClient(c.store, cert, roots)
}

func (c *ControlStore) FaultReason() string {
	if c == nil {
		return "unavailable"
	}
	return c.store.FaultReason()
}

func (c *ControlStore) Close() error {
	if c == nil {
		return nil
	}
	return c.store.Close()
}
