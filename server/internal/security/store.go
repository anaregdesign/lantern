package security

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
)

var (
	ErrRevisionConflict = errors.New("security revision conflict")
	ErrChangeConflict   = errors.New("security change ID reused with different intent")
	ErrBootstrapLocked  = errors.New("env-owned security assignment is locked")
	ErrStoreUnavailable = errors.New("security store unavailable")
	ErrReadOnlyWriter   = errors.New("security changes require the pinned writer")
)

// RevisionCommitter must durably persist the entire revision and materialize
// it in the same sys GraphCache before returning success. Any error is treated
// as an indeterminate commit and poisons serving until certified recovery.
type RevisionCommitter interface {
	CommitRevision(context.Context, *Revision) error
}

type StoreOptions struct {
	Generation [16]byte
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey // omitted on a replica
	Committer  RevisionCommitter
	Limits     PolicyLimits
}

type ChangeResult struct {
	Revision uint64
	Digest   [32]byte
	Replayed bool
}

type changeRecord struct {
	expected     uint64
	intentDigest [32]byte
	result       ChangeResult
}

const retainedChanges = 256

// Store publishes one complete immutable revision after persistence. Its
// serialized writer and contiguous signed apply path are not graph HLC/LWW.
// It does not itself establish a serving lease or listener certification.
type Store struct {
	mu         sync.Mutex
	current    atomic.Pointer[Revision]
	faulted    atomic.Bool
	generation [16]byte
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	committer  RevisionCommitter
	limits     PolicyLimits
	changes    map[[16]byte]changeRecord
	order      [][16]byte
}

func NewStore(options StoreOptions) (*Store, error) {
	if options.Generation == [16]byte{} || len(options.PublicKey) != ed25519.PublicKeySize || options.Committer == nil {
		return nil, ErrStoreUnavailable
	}
	if len(options.PrivateKey) != 0 && (len(options.PrivateKey) != ed25519.PrivateKeySize ||
		!bytes.Equal(options.PrivateKey, ed25519.NewKeyFromSeed(options.PrivateKey[:ed25519.SeedSize])) ||
		!bytes.Equal(options.PrivateKey.Public().(ed25519.PublicKey), options.PublicKey)) {
		return nil, ErrStoreUnavailable
	}
	if _, err := CompileRoles(nil, options.Limits); err != nil {
		return nil, err
	}
	return &Store{generation: options.Generation, publicKey: bytes.Clone(options.PublicKey),
		privateKey: bytes.Clone(options.PrivateKey), committer: options.Committer,
		limits: options.Limits, changes: make(map[[16]byte]changeRecord)}, nil
}

// Current is an authority-state snapshot, not permission to serve: callers
// must independently validate a serving lease and response-publication fence.
func (s *Store) Current() (*Revision, bool) {
	if s == nil || s.faulted.Load() {
		return nil, false
	}
	revision := s.current.Load()
	return revision, revision != nil
}

func (s *Store) Commit(ctx context.Context, expected uint64, changeID [16]byte, image Image) (ChangeResult, error) {
	return s.commit(ctx, expected, changeID, image, false)
}

// ReconcileBootstrap is an operator-only path. Management APIs must not expose
// it. A new env revision is monotonic; old pods cannot recreate assignments.
func (s *Store) ReconcileBootstrap(ctx context.Context, expected uint64, changeID [16]byte, image Image) (ChangeResult, error) {
	return s.commit(ctx, expected, changeID, image, true)
}

func (s *Store) commit(ctx context.Context, expected uint64, changeID [16]byte, image Image, bootstrap bool) (ChangeResult, error) {
	if s == nil || s.faulted.Load() {
		return ChangeResult{}, ErrStoreUnavailable
	}
	if len(s.privateKey) == 0 {
		return ChangeResult{}, ErrReadOnlyWriter
	}
	var previousSnapshot *Snapshot
	if current := s.current.Load(); current != nil {
		previousSnapshot = current.snapshot
	}
	snapshot, err := compileImage(image, s.limits, previousSnapshot)
	if err != nil {
		return ChangeResult{}, err
	}
	intentDigest := sha256.Sum256(snapshot.image)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faulted.Load() {
		return ChangeResult{}, ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return ChangeResult{}, err
	}
	if record, known := s.changes[changeID]; known {
		if record.expected != expected || record.intentDigest != intentDigest {
			return ChangeResult{}, ErrChangeConflict
		}
		result := record.result
		result.Replayed = true
		return result, nil
	}
	current := s.current.Load()
	previous := [32]byte{}
	if current == nil {
		if expected != 0 {
			return ChangeResult{}, ErrRevisionConflict
		}
		if !bootstrap || image.BootstrapRevision == 0 {
			return ChangeResult{}, ErrBootstrapLocked
		}
	} else {
		if expected != current.sequence {
			return ChangeResult{}, ErrRevisionConflict
		}
		old := current.snapshot.Image()
		if bootstrap {
			if image.BootstrapRevision <= old.BootstrapRevision {
				return ChangeResult{}, ErrBootstrapLocked
			}
		} else if image.BootstrapRevision != old.BootstrapRevision || image.BootstrapDigest != old.BootstrapDigest || !sameEnvOwned(old, image) {
			return ChangeResult{}, ErrBootstrapLocked
		}
		previous = current.digest
	}
	revision, err := signRevisionWithHistory(s.generation, expected+1, previous, changeID, snapshot, s.privateKey, s.checkpointHistory(), [32]byte{})
	if err != nil {
		return ChangeResult{}, err
	}
	if err := s.persistAndPublish(ctx, revision); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{Revision: revision.sequence, Digest: revision.digest}, nil
}

// Apply accepts only the next complete revision signed by the pinned writer.
// A duplicate is a no-op; stale state, gaps, forks and generation changes are
// rejected before persistence. Certified checkpoint recovery is a separate path.
func (s *Store) Apply(ctx context.Context, encoded []byte) error {
	if s == nil || s.faulted.Load() {
		return ErrStoreUnavailable
	}
	revision, err := DecodeRevision(encoded, s.publicKey, s.limits)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faulted.Load() {
		return ErrStoreUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if revision.generation != s.generation {
		return ErrRevisionConflict
	}
	current := s.current.Load()
	if current != nil && current.sequence == revision.sequence && current.digest == revision.digest {
		return nil
	}
	if current == nil {
		if revision.sequence != 1 || revision.previous != [32]byte{} || revision.snapshot.Image().BootstrapRevision == 0 {
			return ErrRevisionConflict
		}
	} else if revision.sequence != current.sequence+1 || revision.previous != current.digest ||
		revision.snapshot.Image().BootstrapRevision < current.snapshot.Image().BootstrapRevision {
		return ErrRevisionConflict
	}
	if current != nil && revision.snapshot.Image().BootstrapRevision == current.snapshot.Image().BootstrapRevision &&
		(current.snapshot.Image().BootstrapDigest != revision.snapshot.Image().BootstrapDigest || !sameEnvOwned(current.snapshot.Image(), revision.snapshot.Image())) {
		return ErrBootstrapLocked
	}
	if _, known := s.changes[revision.changeID]; known {
		return ErrChangeConflict
	}
	return s.persistAndPublish(ctx, revision)
}

func (s *Store) persistAndPublish(ctx context.Context, revision *Revision) error {
	complete := false
	defer func() {
		// HTTP recovery must not leave old authority healthy if a committer
		// panics after a partial durable write or GraphCache installation.
		if !complete {
			s.faulted.Store(true)
		}
	}()
	if err := s.committer.CommitRevision(ctx, revision); err != nil {
		return errors.Join(ErrStoreUnavailable, err)
	}
	s.publish(revision)
	complete = true
	return nil
}

func (s *Store) publish(revision *Revision) {
	s.changes[revision.changeID] = changeRecord{expected: revision.sequence - 1,
		intentDigest: revision.intentDigest,
		result:       ChangeResult{Revision: revision.sequence, Digest: revision.digest}}
	s.order = append(s.order, revision.changeID)
	if len(s.order) > retainedChanges {
		delete(s.changes, s.order[0])
		s.order = s.order[1:]
	}
	s.current.Store(revision)
}

func sameEnvOwned(old, next Image) bool {
	if !reflect.DeepEqual(old.MachineCredentials, next.MachineCredentials) {
		return false
	}
	locked := func(image Image) (map[Identity]map[string]bool, map[string][]PermissionRule) {
		assignments := make(map[Identity]map[string]bool)
		roles := make(map[string][]PermissionRule)
		for _, principal := range image.Principals {
			for _, assignment := range principal.Assignments {
				if !assignment.EnvOwned {
					continue
				}
				if assignments[principal.Identity] == nil {
					assignments[principal.Identity] = make(map[string]bool)
				}
				assignments[principal.Identity][assignment.RoleID] = true
			}
		}
		for _, role := range image.Roles {
			for _, ids := range assignments {
				if ids[role.ID] {
					roles[role.ID] = role.Rules
				}
			}
		}
		return assignments, roles
	}
	envIssuers := func(image Image) map[string]Issuer {
		result := make(map[string]Issuer)
		for _, issuer := range image.Issuers {
			if issuer.EnvOwned {
				result[issuer.URL] = issuer
			}
		}
		return result
	}
	if !reflect.DeepEqual(envIssuers(old), envIssuers(next)) {
		return false
	}
	oldAssignments, oldRoles := locked(old)
	nextAssignments, nextRoles := locked(next)
	return reflect.DeepEqual(oldAssignments, nextAssignments) && reflect.DeepEqual(oldRoles, nextRoles)
}
