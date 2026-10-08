package security

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

var (
	errS2LocalClosed         = errors.New("S2 local materialization owner is closed")
	errS2LocalUnknown        = errors.New("S2 local materialization outcome is unknown; recovery required")
	errS2LocalPlan           = errors.New("S2 local materialization plan is inactive")
	errS2BlockedSameDecision = errors.New("S2 local materialization blocked for the same certified decision")
)

// This refusal neither changes the certified value nor manufactures a terminal
// business outcome. The caller still owns its independent proof obligation.
type s2LocalBlockedError struct {
	Commit CommitRef
	Cause  error
}

func (e *s2LocalBlockedError) Error() string {
	return fmt.Sprintf("%v: %v", errS2BlockedSameDecision, e.Cause)
}
func (e *s2LocalBlockedError) Unwrap() []error { return []error{errS2BlockedSameDecision, e.Cause} }

// A local receipt is not an Accepted vote, choice certificate, earliest commit
// time or current enforcement/freshness assertion. It also serves as an exact
// independently retained minimum cut for a later explicit Resume.
type s2LocalReceipt struct {
	ScopeDigest   [32]byte
	LocalIndex    uint64
	Chain         [32]byte
	CapsuleDigest [32]byte
	ControlSlot   uint64
	ControlPrefix [32]byte
}

// Only the active handle is accepted, including after release and reprepare.
// Payload, state, budget and predecessor are never supplied through the token.
type s2LocalPlan struct {
	owner  *s2LocalOwner
	serial uint64
}
type s2LocalPrepared struct {
	handle        *s2LocalPlan
	serial        uint64
	before        s2LocalReceipt
	beforeOffset  int64
	decision      CommitRef
	state         *S1ApplyState
	capsule       string
	capsuleDigest [32]byte
	payload       s2LocalPayload
	charge        uint64
}

// Narrow private test checkpoints, never a caller WAL or authentication port.
// Hooks are installed only on an otherwise real private owner in native tests.
type s2LocalHooks struct {
	beforeAppend, beforePublish, afterMetadata, afterState, afterLog func()
	replay                                                           func()
}

// Every metadata/state/Log observation is behind gate. No cache, metadata,
// Log, tip, provenance or lease handle is returned by this inactive component.
type s2LocalOwner struct {
	gate            sync.Mutex
	admissionClosed atomic.Bool
	closed          bool
	unknown         error
	closeErr        error
	scope           s2LocalScope
	roots           s2CapsuleRoots
	state           *S1ApplyState
	capsule         string
	receipt         s2LocalReceipt
	used            uint64
	offset          int64
	serial          uint64
	pending         *s2LocalPrepared
	metadata        *graphcache.SystemMetadata
	log             *mutationlog.Log
	walOwner        io.Closer
	tip             *mutationlog.FileWALTipJournal
	lease           *mutationlog.FileWALLease
	provenance      *mutationlog.FileWALTipProvenance
	hooks           *s2LocalHooks
}

type s2LocalPayload []byte

func s2EncodeLocalPayload(op mutationlog.MutationOp) ([]byte, error) {
	p, ok := op.(s2LocalPayload)
	if !ok || len(p) == 0 || uint64(len(p)) > s2LocalMaxPayloadBytes {
		return nil, errS2LocalRecord
	}
	return p, nil
}

func s2NewLocalCandidate(path string, scope s2LocalScope, genesis *s2TrustedGenesis) (*s2LocalOwner, error) {
	if !filepath.IsAbs(path) {
		return nil, errS2LocalScope
	}
	if err := scope.validateGenesis(genesis); err != nil {
		return nil, err
	}
	state, err := s2DetachState(genesis.state)
	if err != nil {
		return nil, err
	}
	// The graph exists solely to own this one reserved system image. Its raw
	// pointer never escapes construction and has no external graph users.
	cache := graphcache.NewGraphCache[string, struct{}](0)
	metadata, err := cache.EnableSystemMetadata("sys:security:s2:materialized", int(scope.Storage.Capsule.Bytes))
	if err != nil {
		return nil, err
	}
	return &s2LocalOwner{scope: scope, roots: genesis.roots, state: state, metadata: metadata}, nil
}

// Explicit Create refuses every existing/partial family. Failed construction
// retains all files (including the lease inode); only explicit Resume can
// qualify a complete uncertain GENESIS using independent trust.
func createS2Local(path string, scope s2LocalScope, genesis *s2TrustedGenesis) (_ *s2LocalOwner, err error) {
	o, err := s2NewLocalCandidate(path, scope, genesis)
	if err != nil {
		return nil, err
	}
	capsule, _, err := s2EncodeCapsule(o.state, o.roots, scope.Storage.Capsule)
	if err != nil {
		return nil, err
	}
	record := &s2LocalRecord{kind: s2LocalGenesis, scope: scope, scopeDigest: scope.digest(), localIndex: 1, capsuleDigest: s2LocalCapsuleDigest(capsule), capsule: string(capsule)}
	payload, err := s2EncodeLocalRecord(record, scope)
	if err != nil {
		return nil, err
	}
	charge, err := s2CheckedAdd(uint64(len(payload)), 88)
	if err != nil || scope.Storage.JournalBytes < 85 || charge > scope.Storage.JournalBytes-85 {
		return nil, errS2Unpersistable
	}
	stage, err := o.metadata.Prepare([32]byte{}, 1, capsule)
	if err != nil {
		return nil, err
	}
	defer stage.Abort()
	o.lease, err = mutationlog.AcquireFileWALLease(path)
	if err != nil {
		return nil, err
	}
	// This cleanup is registered immediately after acquisition, before any
	// filesystem callback, replay or other fallible step can panic.
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, o.closeResources())
		}
	}()
	err = o.lease.WithPath(func(canonical string) error {
		if e := s2LocalLeaseEmpty(canonical); e != nil {
			return e
		}
		for _, p := range []string{canonical, canonical + ".tip"} {
			if _, e := os.Lstat(p); e == nil {
				return os.ErrExist
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
		wal, e := mutationlog.CreateFileWAL(canonical, s2EncodeLocalPayload)
		if e != nil {
			return e
		}
		o.walOwner = wal // register before acquiring the next resource
		o.tip, e = mutationlog.CreateFileWALTipJournal(canonical, scope.tipBinding())
		if e != nil {
			return e
		}
		if e = o.tip.VerifyAndCatchUp(canonical, func([]byte) (mutationlog.MutationOp, error) { return nil, errS2LocalRecord }, func(mutationlog.Entry) error { return errS2LocalRecord }); e != nil {
			return e
		}
		if e = wal.BindTipJournal(o.tip); e != nil {
			return e
		}
		o.log = mutationlog.New(mutationlog.Options{Capacity: 1, WAL: wal})
		o.provenance, e = o.log.FileWALTipProvenance(canonical)
		if e != nil {
			return e
		}
		_, e = o.log.CommitWithPublication(s2LocalPayload(payload), hlc.Timestamp{}, func(mutationlog.Entry) { stage.Commit(); o.capsule = record.capsule })
		if e != nil {
			return errors.Join(errS2LocalUnknown, e)
		}
		w, e := o.provenance.TipWitness(canonical)
		if e != nil {
			return errors.Join(errS2LocalUnknown, e)
		}
		o.receipt = s2Receipt(record.scopeDigest, o.state, record.capsuleDigest, w)
		o.used = 85 + charge
		o.offset = w.Offset
		return nil
	})
	if err != nil {
		return nil, err
	}
	transferred = true
	return o, nil
}

func s2Receipt(scopeDigest [32]byte, state *S1ApplyState, capsule [32]byte, w mutationlog.FileWALTipWitness) s2LocalReceipt {
	return s2LocalReceipt{scopeDigest, w.Seq, w.ChainSHA256, capsule, state.slot, state.prefix}
}

func (o *s2LocalOwner) readyLocked() error {
	if o.unknown != nil {
		return errors.Join(errS2LocalUnknown, o.unknown)
	}
	if o.closed || o.admissionClosed.Load() {
		return errS2LocalClosed
	}
	return nil
}

func (o *s2LocalOwner) PrepareNext(next S1CertifiedNext) (*s2LocalPlan, error) {
	if o == nil || o.admissionClosed.Load() {
		return nil, errS2LocalClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	if err := o.readyLocked(); err != nil {
		return nil, err
	}
	if o.pending != nil {
		return nil, errS2LocalPlan
	}
	result, err := ApplyS1(o.state, next)
	if err != nil {
		if errors.Is(err, ErrControlReserve) && next.certificate != nil {
			return nil, &s2LocalBlockedError{next.certificate.commit, err}
		}
		return nil, err
	}
	decision := next.certificate.commit
	blocked := func(e error) (*s2LocalPlan, error) { return nil, &s2LocalBlockedError{decision, e} }
	index, err := s2LocalIndex(result.State.slot)
	if err != nil || index > o.scope.Storage.ReplayRecords || o.serial == math.MaxUint64 {
		return blocked(errS2Unpersistable)
	}
	state, err := s2DetachState(result.State)
	if err != nil {
		return blocked(err)
	}
	capsule, _, err := s2EncodeCapsule(state, o.roots, o.scope.Storage.Capsule)
	if err != nil {
		return blocked(err)
	}
	record := &s2LocalRecord{kind: s2LocalApply, scopeDigest: o.receipt.ScopeDigest, localIndex: index, previousCapsuleDigest: o.receipt.CapsuleDigest, capsuleDigest: s2LocalCapsuleDigest(capsule), commit: decision, capsule: string(capsule)}
	payload, err := s2EncodeLocalRecord(record, o.scope)
	if err != nil {
		return blocked(err)
	}
	charge, err := s2CheckedAdd(uint64(len(payload)), 88)
	if err != nil || o.used > o.scope.Storage.JournalBytes || charge > o.scope.Storage.JournalBytes-o.used {
		return blocked(errS2Unpersistable)
	}
	o.serial++
	handle := &s2LocalPlan{o, o.serial}
	o.pending = &s2LocalPrepared{handle, o.serial, o.receipt, o.offset, decision, state, record.capsule, record.capsuleDigest, s2LocalPayload(payload), charge}
	return handle, nil
}

func (o *s2LocalOwner) activeLocked(p *s2LocalPlan) bool {
	a := o.pending
	return p != nil && a != nil && a.handle == p && p.owner == o && p.serial == a.serial && a.serial == o.serial && a.before == o.receipt && a.beforeOffset == o.offset && a.before.ScopeDigest == o.scope.digest() && a.state.slot == o.state.slot+1 && a.decision.Slot == a.state.slot
}

func (o *s2LocalOwner) DiscardLocalPlan(p *s2LocalPlan) error {
	if o == nil || o.admissionClosed.Load() {
		return errS2LocalClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	if err := o.readyLocked(); err != nil {
		return err
	}
	if !o.activeLocked(p) {
		return errS2LocalPlan
	}
	o.pending = nil
	return nil
}

func (o *s2LocalOwner) CommitPrepared(p *s2LocalPlan) (_ s2LocalReceipt, err error) {
	if o == nil || o.admissionClosed.Load() {
		return s2LocalReceipt{}, errS2LocalClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	if err = o.readyLocked(); err != nil {
		return s2LocalReceipt{}, err
	}
	if !o.activeLocked(p) {
		return s2LocalReceipt{}, errS2LocalPlan
	}
	a := o.pending
	w, err := o.provenance.TipWitness(o.lease.Path())
	if err != nil || w.Seq != o.receipt.LocalIndex || w.ChainSHA256 != o.receipt.Chain || w.Offset != a.beforeOffset {
		o.unknown = errors.Join(errS2LocalUnknown, err)
		return s2LocalReceipt{}, o.unknown
	}
	stage, err := o.metadata.Prepare(sha256.Sum256([]byte(o.capsule)), a.state.slot+1, []byte(a.capsule))
	if err != nil {
		return s2LocalReceipt{}, err
	}
	defer stage.Abort()
	if o.hooks != nil && o.hooks.beforeAppend != nil {
		o.hooks.beforeAppend()
	}
	// Install uncertainty handling before entering the real Log/WAL. Unknown
	// errors and panics dominate every convenience error class. A native
	// preflight refusal above remains reusable; once entered we fail closed.
	resolved := false
	defer func() {
		if !resolved {
			o.unknown = errors.Join(errS2LocalUnknown, err)
		}
	}()
	_, err = o.log.CommitWithPublication(a.payload, hlc.Timestamp{}, func(mutationlog.Entry) {
		if o.hooks != nil && o.hooks.beforePublish != nil {
			o.hooks.beforePublish()
		}
		stage.Commit()
		if o.hooks != nil && o.hooks.afterMetadata != nil {
			o.hooks.afterMetadata()
		}
		o.state, o.capsule = a.state, a.capsule
		if o.hooks != nil && o.hooks.afterState != nil {
			o.hooks.afterState()
		}
	})
	if err != nil {
		return s2LocalReceipt{}, errors.Join(errS2LocalUnknown, err)
	}
	if o.hooks != nil && o.hooks.afterLog != nil {
		o.hooks.afterLog()
	}
	w, err = o.provenance.TipWitness(o.lease.Path())
	if err != nil {
		return s2LocalReceipt{}, errors.Join(errS2LocalUnknown, err)
	}
	o.receipt = s2Receipt(a.before.ScopeDigest, o.state, a.capsuleDigest, w)
	o.offset = w.Offset
	o.used += a.charge // checked while preparing, and only this plan can append
	o.pending = nil
	resolved = true
	return o.receipt, nil
}

func (o *s2LocalOwner) ReadLocalCut() (*S1ApplyState, s2LocalReceipt, error) {
	if o == nil || o.admissionClosed.Load() {
		return nil, s2LocalReceipt{}, errS2LocalClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	if err := o.readyLocked(); err != nil {
		return nil, s2LocalReceipt{}, err
	}
	state, err := s2DetachState(o.state)
	if err != nil {
		return nil, s2LocalReceipt{}, err
	}
	return state, o.receipt, nil
}

func (o *s2LocalOwner) LookupOriginal(id FullChangeID) (S1LookupState, *OriginalOutcome, s2LocalReceipt, error) {
	if o == nil || o.admissionClosed.Load() {
		return S1Unresolved, nil, s2LocalReceipt{}, errS2LocalClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	if err := o.readyLocked(); err != nil {
		return S1Unresolved, nil, s2LocalReceipt{}, err
	}
	status, original := o.state.Lookup(id)
	if original == nil {
		return status, nil, o.receipt, nil
	}
	copy := *original
	copy.items = append([]S1ItemOutcome(nil), original.items...)
	return status, &copy, o.receipt, nil
}

// Admission closes before waiting for gate. An append already holding gate
// may finish, but neither queued readers nor plans may enter behind Close.
func (o *s2LocalOwner) Close() error {
	if o == nil {
		return nil
	}
	o.admissionClosed.Store(true)
	o.gate.Lock()
	defer o.gate.Unlock()
	if o.closed {
		return o.closeErr
	}
	o.closed = true
	o.closeErr = o.closeResources()
	return o.closeErr
}

func (o *s2LocalOwner) closeResources() error {
	var err error
	if o.log != nil {
		err = errors.Join(err, o.log.Close())
	}
	if o.walOwner != nil {
		err = errors.Join(err, o.walOwner.Close())
	}
	if o.tip != nil {
		err = errors.Join(err, o.tip.Close())
	}
	if o.lease != nil {
		err = errors.Join(err, o.lease.Close())
	}
	return err
}

func s2LocalLeaseEmpty(path string) error {
	info, err := os.Lstat(path + ".lease")
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return errS2LocalRecord
	}
	return nil
}
