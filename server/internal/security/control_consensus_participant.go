package security

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	errS2CClosed   = errors.New("S2-C participant is closed")
	errS2CUnknown  = errors.New("S2-C participant outcome unknown; recovery required")
	errS2CProtocol = errors.New("invalid S2-C protocol transition")
	errS2CCapacity = errors.New("S2-C participant capacity unavailable for same value")
	errS2CPending  = errors.New("S2-C chosen decision pending materialization")
)

// Every local identity, path and budget is supplied independently and bound by
// GENESIS. Neither a recovered record nor a peer message supplies bootstrap.
type s2cParticipantConfig struct {
	Trust                                   *s2cTrust
	Member                                  uint32
	Incarnation                             [16]byte
	Key                                     ed25519.PrivateKey
	PPath, BPath                            string
	PIdentity                               [32]byte
	PEpoch                                  [16]byte
	PPolicy                                 s2cJournalPolicy
	BScope                                  s2LocalScope
	OwnedOrigin                             uint32
	PendingBytes, PendingCount, OutboxBytes uint64
}

type s2cManifest struct {
	Common                                  [32]byte
	Member                                  uint32
	Incarnation                             [16]byte
	PPath, BPath                            string
	PIdentity                               [32]byte
	PEpoch                                  [16]byte
	PPolicy                                 s2cJournalPolicy
	BScope                                  s2LocalScope
	OwnedOrigin                             uint32
	PendingBytes, PendingCount, OutboxBytes uint64
}

type s2cOutbox struct {
	To    uint32
	Bytes []byte
}
type s2cCredit struct{ PBytes, PRecords, BBytes, BRecords uint64 }
type s2cParticipantHooks struct {
	beforePAppend, afterPAppend                               func(byte)
	afterChosen, beforeB, afterB, beforeDrained, afterDrained func()
}

// One mutex owns the entire two-journal lifecycle. No B owner, signing key,
// journal handle, or mutable trusted state is returned by this private kernel.
type s2cParticipant struct {
	gate                                 sync.Mutex
	admissionClosed                      atomic.Bool
	closed                               bool
	unknown, closeErr                    error
	config                               s2cParticipantConfig
	manifest                             s2cManifest
	binding                              [32]byte
	p                                    *s2cJournal
	b                                    *s2LocalOwner
	trust                                *s2cTrust
	key                                  ed25519.PrivateKey
	origins                              map[[32]byte]*s2cHistoricalH
	serials                              map[uint64][32]byte
	originSerial                         uint64
	originReservations                   map[uint64]authorityOriginReservation
	originIDs                            map[FullChangeID]uint64
	pending                              map[[32]byte]*s2cHistoricalH
	pendingBytes                         uint64
	counter                              uint64
	promise, accepted, prepare, selected *s2cMessage
	candidate                            [32]byte
	promises, votes                      map[uint32]*s2cMessage
	credit                               s2cCredit
	history                              []*s2cMessage
	proofs                               []S1CertifiedNext
	receipts                             []s2LocalReceipt
	// replayState and replayReceipt are detached authenticated P reconstruction,
	// also used to validate the one possible CHOSEN tail before opening B.
	replayState   *S1ApplyState
	replayReceipt s2LocalReceipt
	chosen        *s2cMessage
	preview       *s2cPreview
	bootstrapped  bool
	hooks         *s2cParticipantHooks
}

func s2cCanonicalFamilies(p, b string) (string, string, error) {
	canonical := func(path string) (string, error) {
		if !filepath.IsAbs(path) {
			return "", errS2CProtocol
		}
		dir, e := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(path)))
		if e != nil {
			return "", e
		}
		return filepath.Join(dir, filepath.Base(filepath.Clean(path))), nil
	}
	p, e := canonical(p)
	if e != nil {
		return "", "", e
	}
	b, e = canonical(b)
	if e != nil {
		return "", "", e
	}
	paths := []string{p, p + ".tip", p + ".lease", b, b + ".tip", b + ".lease"}
	seen := map[string]bool{}
	infos := []os.FileInfo{}
	for _, path := range paths {
		if seen[path] {
			return "", "", errS2CProtocol
		}
		seen[path] = true
		info, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return "", "", e
		}
		if !info.Mode().IsRegular() {
			return "", "", errS2CProtocol
		}
		for _, old := range infos {
			if os.SameFile(info, old) {
				return "", "", errS2CProtocol
			}
		}
		infos = append(infos, info)
	}
	return p, b, nil
}

func s2cNewParticipant(c s2cParticipantConfig) (*s2cParticipant, error) {
	if c.Trust == nil || len(c.Key) != ed25519.PrivateKeySize || c.Incarnation == [16]byte{} || c.PIdentity == [32]byte{} || c.PEpoch == [16]byte{} || !c.PPolicy.valid() || c.PendingBytes == 0 || c.PendingBytes > c.PPolicy.Bytes || c.PendingCount == 0 || c.PendingCount > c.PPolicy.Records || c.OutboxBytes == 0 || c.OutboxBytes > uint64(MaxSystemJournalBytes) {
		return nil, errS2CProtocol
	}
	// Reconstruct a detached immutable trust copy rather than retaining a caller's
	// package-private pointer. There is no mutable runtime trust setter.
	t, e := newS2CTrust(s2cBootstrap{c.Trust.genesis, c.Trust.members, c.Trust.origins, c.Trust.bounds})
	if e != nil {
		return nil, e
	}
	m, ok := t.member(c.Member)
	if !ok || !bytes.Equal(c.Key.Public().(ed25519.PublicKey), m.PublicKey[:]) {
		return nil, errS2CProtocol
	}
	if c.OwnedOrigin != 0 {
		d, ok := t.origin(c.OwnedOrigin)
		if !ok || d.Member != c.Member {
			return nil, errS2CProtocol
		}
	}
	if e = c.BScope.validateGenesis(t.genesis); e != nil {
		return nil, e
	}
	if _, e = s2CheckedAdd(c.PPolicy.Bytes, c.BScope.Storage.JournalBytes); e != nil {
		return nil, e
	}
	c.PPath, c.BPath, e = s2cCanonicalFamilies(c.PPath, c.BPath)
	if e != nil {
		return nil, e
	}
	manifest := s2cManifest{t.scope, c.Member, c.Incarnation, c.PPath, c.BPath, c.PIdentity, c.PEpoch, c.PPolicy, c.BScope, c.OwnedOrigin, c.PendingBytes, c.PendingCount, c.OutboxBytes}
	binding := s2cHash("local-manifest", manifest)
	state, e := s2DetachState(t.genesis.state)
	if e != nil {
		return nil, e
	}
	capsule, _, e := s2EncodeCapsule(state, t.genesis.roots, c.BScope.Storage.Capsule)
	if e != nil {
		return nil, e
	}
	c.Key = nil
	c.Trust = nil
	o := &s2cParticipant{config: c, manifest: manifest, binding: binding, trust: t, origins: map[[32]byte]*s2cHistoricalH{}, serials: map[uint64][32]byte{}, pending: map[[32]byte]*s2cHistoricalH{}, promises: map[uint32]*s2cMessage{}, votes: map[uint32]*s2cMessage{}, replayState: state}
	o.originReservations = make(map[uint64]authorityOriginReservation)
	o.originIDs = make(map[FullChangeID]uint64)
	o.replayReceipt = s2LocalReceipt{ScopeDigest: c.BScope.digest(), LocalIndex: 1, CapsuleDigest: s2LocalCapsuleDigest(capsule), ControlPrefix: state.prefix}
	return o, nil
}

func createS2CParticipant(c s2cParticipantConfig) (_ *s2cParticipant, err error) {
	o, err := s2cNewParticipant(c)
	if err != nil {
		return nil, err
	}
	o.key = append(ed25519.PrivateKey(nil), c.Key...)
	for _, path := range []string{o.config.PPath, o.config.PPath + ".tip", o.config.BPath, o.config.BPath + ".tip"} {
		if _, e := os.Lstat(path); e == nil {
			return nil, os.ErrExist
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, o.closeResources())
		}
	}()
	raw, _ := json.Marshal(o.manifest)
	genesis := s2cPRecord{kind: s2cPGenesis, index: 1, raw: string(raw)}
	payload, err := o.encodeRecord(genesis)
	if err != nil {
		return nil, err
	}
	// A partial Create remains closed. Resume requires both real families and a
	// durable bootstrap receipt; it never manufactures a missing GENESIS.
	o.p, err = createS2CJournal(o.config.PPath, o.binding, o.config.PPolicy, payload)
	if err != nil {
		return nil, err
	}
	o.b, err = createS2Local(o.config.BPath, o.config.BScope, o.trust.genesis)
	if err != nil {
		return nil, err
	}
	receipt := o.b.receipt
	if err = o.appendRecord(s2cPRecord{kind: s2cPDrained, receipt: receipt}, s2cCredit{}); err != nil {
		return nil, err
	}
	o.bootstrapped = true
	o.receipts = append(o.receipts, receipt)
	o.replayReceipt = receipt
	transferred = true
	return o, nil
}

func (o *s2cParticipant) readyLocked() error {
	if o.unknown != nil {
		return errors.Join(errS2CUnknown, o.unknown)
	}
	if o.closed || o.admissionClosed.Load() {
		return errS2CClosed
	}
	if o.p != nil {
		if e := o.p.guard(); e != nil {
			o.unknown = e
			return errors.Join(errS2CUnknown, e)
		}
	}
	if !o.bootstrapped {
		return errS2CProtocol
	}
	return nil
}

func (o *s2cParticipant) poisonPanic() {
	if v := recover(); v != nil {
		o.unknown = errS2CUnknown
		panic(v)
	}
}

func (o *s2cParticipant) Close() error {
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
func (o *s2cParticipant) closeResources() error {
	var e error
	var failure any
	closeOne := func(close func() error) (panicked bool) {
		defer func() {
			if v := recover(); v != nil {
				panicked = true
				if failure == nil {
					failure = v
				}
			}
		}()
		e = errors.Join(e, close())
		return false
	}
	if o.b != nil && closeOne(o.b.Close) {
		// B is private to this composite. Its legacy closer may stop at a panic;
		// finish attempting each remaining descriptor before releasing P ownership.
		if o.b.log != nil {
			closeOne(o.b.log.Close)
		}
		if o.b.walOwner != nil {
			closeOne(o.b.walOwner.Close)
		}
		if o.b.tip != nil {
			closeOne(o.b.tip.Close)
		}
		if o.b.lease != nil {
			closeOne(o.b.lease.Close)
		}
	}
	if o.p != nil {
		closeOne(o.p.close)
	}
	clear(o.key)
	o.key = nil
	if failure != nil {
		o.unknown = errS2CUnknown
		panic(failure)
	}
	return e
}

func (o *s2cParticipant) Floors() (s2cJournalFloor, s2LocalReceipt, error) {
	if o == nil {
		return s2cJournalFloor{}, s2LocalReceipt{}, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if e := o.readyLocked(); e != nil {
		return s2cJournalFloor{}, s2LocalReceipt{}, e
	}
	return o.p.floor, o.b.receipt, nil
}
func (o *s2cParticipant) ReadLocalCut() (*S1ApplyState, s2LocalReceipt, error) {
	if o == nil {
		return nil, s2LocalReceipt{}, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if e := o.readyLocked(); e != nil {
		return nil, s2LocalReceipt{}, e
	}
	if o.chosen != nil {
		return nil, s2LocalReceipt{}, errS2CPending
	}
	s, r, e := o.b.ReadLocalCut()
	if e != nil {
		o.unknown = e
	}
	return s, r, e
}
func (o *s2cParticipant) LookupOriginal(id FullChangeID) (S1LookupState, *OriginalOutcome, s2LocalReceipt, error) {
	if o == nil {
		return S1Unresolved, nil, s2LocalReceipt{}, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	if e := o.readyLocked(); e != nil {
		return S1Unresolved, nil, s2LocalReceipt{}, e
	}
	if o.chosen != nil {
		return S1Unresolved, nil, s2LocalReceipt{}, errS2CPending
	}
	s, v, r, e := o.b.LookupOriginal(id)
	if e != nil {
		o.unknown = e
	}
	return s, v, r, e
}

// Only exact already-signed historical input is accepted. This method has no
// authentication/timestamp/operation arguments with which to mint another H.
func (o *s2cParticipant) persistSealedOriginH(raw []byte) ([32]byte, error) {
	if o == nil {
		return [32]byte{}, errS2CClosed
	}
	o.gate.Lock()
	defer o.gate.Unlock()
	defer o.poisonPanic()
	return o.persistSealedOriginLocked(raw)
}

// Caller holds the composite gate through reserve, consume, seal and append.
func (o *s2cParticipant) persistSealedOriginLocked(raw []byte) ([32]byte, error) {
	if e := o.readyLocked(); e != nil {
		return [32]byte{}, e
	}
	if o.chosen != nil {
		return [32]byte{}, errS2CPending
	}
	h, e := verifyHistoricalH(o.trust, raw)
	if e != nil {
		return [32]byte{}, e
	}
	if o.config.OwnedOrigin == 0 || h.originID != o.config.OwnedOrigin {
		return [32]byte{}, errS2CProtocol
	}
	if !o.reservationMatches(h) {
		return [32]byte{}, errS2CProtocol
	}
	d := h.digest()
	if old, ok := o.serials[h.handoff.serial]; ok {
		if old != d || o.origins[d].raw != h.raw {
			return [32]byte{}, errS2CProtocol
		}
		return d, nil
	}
	if uint64(len(o.pending)) >= o.config.PendingCount || uint64(len(h.raw)) > o.config.PendingBytes-o.pendingBytes {
		return [32]byte{}, errS2CCapacity
	}
	if e = o.appendRecord(s2cPRecord{kind: s2cPOrigin, raw: h.raw}, o.credit); e != nil {
		return [32]byte{}, e
	}
	o.origins[d] = h
	o.serials[h.handoff.serial] = d
	o.pending[d] = h
	o.pendingBytes += uint64(len(h.raw))
	return d, nil
}

func (o *s2cParticipant) nextSlot() (uint64, [32]byte, error) {
	if o.chosen != nil {
		return 0, [32]byte{}, errS2CPending
	}
	if o.replayState.slot > math.MaxUint64-3 {
		return 0, [32]byte{}, errS2CCapacity
	}
	return o.replayState.slot + 1, o.replayState.prefix, nil
}
