package security

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

const (
	s2cJournalHeaderBytes = 85
	s2cJournalFrameBytes  = 88
	s2cJournalMaxPayload  = (32 << 20) - 36
)

var (
	errS2CJournalClosed   = errors.New("S2-C journal is closed")
	errS2CJournalUnknown  = errors.New("S2-C journal outcome is unknown; recovery required")
	errS2CJournalRecord   = errors.New("invalid S2-C journal record or policy")
	errS2CJournalCapacity = errors.New("S2-C journal capacity exhausted")
	errS2CJournalRestore  = errors.New("S2-C journal recovery differs from required history")
)

// These are explicit, immutable file-content and replay bounds. Completion
// credits are supplied by the protocol owner on every ordinary append; this
// physical journal does not infer which protocol obligations may be released.
type s2cJournalPolicy struct{ Bytes, Records uint64 }

func (p s2cJournalPolicy) valid() bool {
	return p.Bytes >= s2cJournalHeaderBytes && p.Bytes <= uint64(MaxSystemJournalBytes) &&
		p.Records > 0 && p.Records <= p.Bytes/s2cJournalFrameBytes
}

type s2cJournalFloor struct {
	Scope [32]byte
	Index uint64
	Chain [32]byte
}

// Native test checkpoints run around the real owned Log. They neither replace
// a WAL nor supply a durability assertion or a protocol verifier.
type s2cJournalHooks struct{ beforeAppend, afterLog func() }

// The composite participant owns this private journal and its B owner. It
// serializes all access to retained records with its own admission gate. The
// journal gate additionally drains append before releasing the P lease.
type s2cJournal struct {
	gate            sync.Mutex
	admissionClosed atomic.Bool
	closed          bool
	unknown         error
	closeErr        error
	policy          s2cJournalPolicy
	floor           s2cJournalFloor
	used, count     uint64
	records         []string
	log             *mutationlog.Log
	walOwner        io.Closer
	tip             *mutationlog.FileWALTipJournal
	lease           *mutationlog.FileWALLease
	provenance      *mutationlog.FileWALTipProvenance
	hooks           *s2cJournalHooks
}

type s2cJournalPayload string

func s2cEncodeJournalPayload(op mutationlog.MutationOp) ([]byte, error) {
	p, ok := op.(s2cJournalPayload)
	if !ok || len(p) == 0 || len(p) > s2cJournalMaxPayload {
		return nil, errS2CJournalRecord
	}
	return []byte(p), nil
}

func s2cDecodeJournalPayload(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) == 0 || len(raw) > s2cJournalMaxPayload {
		return nil, errS2CJournalRecord
	}
	return s2cJournalPayload(raw), nil
}

func s2cJournalCharge(raw []byte) (uint64, error) {
	if len(raw) == 0 || len(raw) > s2cJournalMaxPayload {
		return 0, errS2CJournalRecord
	}
	return uint64(len(raw)) + s2cJournalFrameBytes, nil
}

func s2cNewJournal(path string, binding [32]byte, policy s2cJournalPolicy) (*s2cJournal, error) {
	if !filepath.IsAbs(path) || binding == ([32]byte{}) || !policy.valid() {
		return nil, errS2CJournalRecord
	}
	return &s2cJournal{policy: policy, floor: s2cJournalFloor{Scope: binding}, used: s2cJournalHeaderBytes}, nil
}

// Creation never adopts an existing or partial WAL/tip family. Failed Create
// retains its files and releases only ownership; it cannot reset an identity.
func createS2CJournal(path string, binding [32]byte, policy s2cJournalPolicy, genesis []byte) (_ *s2cJournal, err error) {
	j, err := s2cNewJournal(path, binding, policy)
	if err != nil {
		return nil, err
	}
	if err = j.checkAppend(genesis, 0, 0); err != nil {
		return nil, err
	}
	j.lease, err = mutationlog.AcquireFileWALLease(path)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, j.closeResources())
		}
	}()
	err = j.lease.WithPath(func(canonical string) error {
		if e := s2LocalLeaseEmpty(canonical); e != nil {
			return e
		}
		for _, name := range []string{canonical, canonical + ".tip"} {
			if _, e := os.Lstat(name); e == nil {
				return os.ErrExist
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
		wal, e := mutationlog.CreateFileWAL(canonical, s2cEncodeJournalPayload)
		if e != nil {
			return e
		}
		j.walOwner = wal
		j.tip, e = mutationlog.CreateFileWALTipJournal(canonical, binding)
		if e != nil {
			return e
		}
		if e = j.tip.VerifyAndCatchUp(canonical, func([]byte) (mutationlog.MutationOp, error) {
			return nil, errS2CJournalRecord
		}, func(mutationlog.Entry) error { return errS2CJournalRecord }); e != nil {
			return e
		}
		if e = wal.BindTipJournal(j.tip); e != nil {
			return e
		}
		j.log = mutationlog.New(mutationlog.Options{Capacity: 1, SubscriberBuffer: 1, WAL: wal})
		j.provenance, e = j.log.FileWALTipProvenance(canonical)
		if e != nil {
			return e
		}
		w, e := j.provenance.TipWitness(canonical)
		if e != nil {
			return e
		}
		j.floor.Chain = w.ChainSHA256
		_, e = j.append(genesis, 0, 0)
		return e
	})
	if err != nil {
		return nil, err
	}
	transferred = true
	return j, nil
}

// validate runs once per record, in sequence, on detached bytes, before any
// recovery barrier. It may reconstruct private protocol state. A zero required
// floor supplies no rollback witness; independent bootstrap and validate still
// govern the complete history. Every nonzero floor must be contained exactly.
func resumeS2CJournal(path string, binding [32]byte, policy s2cJournalPolicy, required s2cJournalFloor, validate func(uint64, []byte) error) (_ *s2cJournal, err error) {
	j, err := s2cNewJournal(path, binding, policy)
	if err != nil {
		return nil, err
	}
	if validate == nil || required != (s2cJournalFloor{}) &&
		(required.Scope != binding || required.Index == 0 || required.Index > policy.Records || required.Chain == ([32]byte{})) {
		return nil, errS2CJournalRestore
	}
	j.lease, err = mutationlog.AcquireFileWALLease(path)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, j.closeResources())
		}
	}()
	err = j.lease.WithPath(func(canonical string) error {
		if e := s2LocalLeaseEmpty(canonical); e != nil {
			return e
		}
		used, count, tips, e := s2cJournalFiles(canonical, policy)
		if e != nil {
			return e
		}
		if count == 0 || required.Index > count {
			return errS2CJournalRestore
		}
		j.tip, e = mutationlog.ResumeFileWALTipJournal(canonical, binding)
		if e != nil {
			return e
		}
		tipSeq, _, _ := j.tip.Frontier()
		if tipSeq != tips || tipSeq > count || count-tipSeq > 1 {
			return errS2CJournalRestore
		}
		missing := (count - tipSeq) * 44
		if missing > policy.Bytes-used {
			return errS2CJournalCapacity
		}
		physical := func(entry mutationlog.Entry) error {
			if _, ok := entry.Op.(s2cJournalPayload); !ok || entry.HLC != (hlc.Timestamp{}) || entry.Seq == 0 || entry.Seq > count {
				return errS2CJournalRecord
			}
			return nil
		}
		cut, e := mutationlog.InspectFileWALCut(canonical, required.Index, s2cDecodeJournalPayload, physical)
		if e != nil {
			return e
		}
		if cut.ObservedLast != count || required.Index != 0 && cut.ChainSHA256 != required.Chain {
			return errS2CJournalRestore
		}
		j.records = make([]string, 0, int(count))
		restore := func(entry mutationlog.Entry) error {
			if e := physical(entry); e != nil {
				return e
			}
			if entry.Seq != uint64(len(j.records))+1 {
				return errS2CJournalRestore
			}
			raw := string(entry.Op.(s2cJournalPayload))
			if e := validate(entry.Seq, []byte(raw)); e != nil {
				return e
			}
			j.records = append(j.records, raw)
			return nil
		}
		j.log, j.walOwner, e = mutationlog.ResumeLogFromFileWALWithDurableTip(canonical,
			mutationlog.Options{Capacity: 1, SubscriberBuffer: 1}, s2cEncodeJournalPayload,
			s2cDecodeJournalPayload, physical, restore, j.tip)
		if e != nil {
			return e
		}
		j.provenance, e = j.log.FileWALTipProvenance(canonical)
		if e != nil {
			return e
		}
		w, e := j.provenance.TipWitness(canonical)
		if e != nil {
			return e
		}
		actual, actualCount, actualTips, e := s2cJournalFiles(canonical, policy)
		if e != nil {
			return e
		}
		if w.Seq != count || uint64(len(j.records)) != count || w.ChainSHA256 != cut.ObservedChainSHA256 ||
			actual != used+missing || actualCount != count || actualTips != count {
			return errS2CJournalRestore
		}
		j.floor = s2cJournalFloor{binding, count, w.ChainSHA256}
		j.used, j.count = actual, count
		return nil
	})
	if err != nil {
		return nil, err
	}
	transferred = true
	return j, nil
}

func (j *s2cJournal) readyLocked() error {
	if j.unknown != nil {
		return errors.Join(errS2CJournalUnknown, j.unknown)
	}
	if j.closed || j.admissionClosed.Load() {
		return errS2CJournalClosed
	}
	return nil
}

func (j *s2cJournal) guard() error {
	if j == nil {
		return errS2CJournalClosed
	}
	j.gate.Lock()
	defer j.gate.Unlock()
	return j.readyLocked()
}

func (j *s2cJournal) checkAppend(raw []byte, protectedBytes, protectedRecords uint64) error {
	charge, err := s2cJournalCharge(raw)
	if err != nil {
		return err
	}
	if j.count == math.MaxUint64 || j.count >= j.policy.Records ||
		protectedRecords > j.policy.Records-j.count-1 || j.used > j.policy.Bytes ||
		charge > j.policy.Bytes-j.used || protectedBytes > j.policy.Bytes-j.used-charge {
		return errS2CJournalCapacity
	}
	return nil
}

func (j *s2cJournal) append(raw []byte, protectedBytes, protectedRecords uint64) (_ s2cJournalFloor, err error) {
	if j == nil {
		return s2cJournalFloor{}, errS2CJournalClosed
	}
	j.gate.Lock()
	defer j.gate.Unlock()
	if err = j.readyLocked(); err != nil {
		return s2cJournalFloor{}, err
	}
	if err = j.checkAppend(raw, protectedBytes, protectedRecords); err != nil {
		return s2cJournalFloor{}, err
	}
	// Freeze caller bytes before crossing the uncertain I/O boundary. No caller
	// buffer can subsequently rewrite retained evidence or future replay data.
	payload := s2cJournalPayload(raw)
	resolved := false
	defer func() {
		if !resolved {
			if p := recover(); p != nil {
				if cause, ok := p.(error); ok {
					err = errors.Join(err, cause)
				}
				j.unknown = errors.Join(errS2CJournalUnknown, err)
				panic(p)
			}
			j.unknown = errors.Join(errS2CJournalUnknown, err)
		}
	}()
	w, err := j.provenance.TipWitness(j.lease.Path())
	if err != nil || w.Seq != j.count || w.ChainSHA256 != j.floor.Chain {
		return s2cJournalFloor{}, errors.Join(errS2CJournalUnknown, errS2CJournalRestore, err)
	}
	if j.hooks != nil && j.hooks.beforeAppend != nil {
		j.hooks.beforeAppend()
	}
	if _, err = j.log.CommitWithPublication(payload, hlc.Timestamp{}, nil); err != nil {
		// Even an error also carrying DefiniteWALAbort cannot overrule this
		// owner's uncertainty once the real append boundary was entered.
		return s2cJournalFloor{}, errors.Join(errS2CJournalUnknown, err)
	}
	if j.hooks != nil && j.hooks.afterLog != nil {
		j.hooks.afterLog()
	}
	w, err = j.provenance.TipWitness(j.lease.Path())
	if err != nil || w.Seq != j.count+1 {
		return s2cJournalFloor{}, errors.Join(errS2CJournalUnknown, errS2CJournalRestore, err)
	}
	j.records = append(j.records, string(payload))
	j.used += uint64(len(payload)) + s2cJournalFrameBytes
	j.count++
	j.floor.Index, j.floor.Chain = j.count, w.ChainSHA256
	resolved = true
	return j.floor, nil
}

func (j *s2cJournal) close() error {
	if j == nil {
		return nil
	}
	j.admissionClosed.Store(true)
	j.gate.Lock()
	defer j.gate.Unlock()
	if j.closed {
		return j.closeErr
	}
	j.closed = true
	j.closeErr = j.closeResources()
	if j.closeErr != nil {
		j.unknown = errors.Join(errS2CJournalUnknown, j.closeErr)
	}
	return j.closeErr
}

func (j *s2cJournal) closeResources() error {
	var err error
	var panicValue any
	// A failure in an earlier closer cannot strand the remaining descriptors
	// or release the lease ahead of attempted Log/WAL/tip shutdown.
	closeOne := func(closer io.Closer) {
		defer func() {
			if p := recover(); p != nil {
				if panicValue == nil {
					panicValue = p
				}
				if cause, ok := p.(error); ok {
					err = errors.Join(err, cause)
				}
				j.unknown = errors.Join(errS2CJournalUnknown, err)
			}
		}()
		err = errors.Join(err, closer.Close())
	}
	if j.log != nil {
		closeOne(j.log)
	}
	if j.walOwner != nil {
		closeOne(j.walOwner)
	}
	if j.tip != nil {
		closeOne(j.tip)
	}
	if j.lease != nil {
		closeOne(j.lease)
	}
	if panicValue != nil {
		panic(panicValue)
	}
	return err
}

// Scan lengths without allocating application payloads. Core subsequently
// verifies every CRC, sequence, canonical frame and exact prefix commitment.
func s2cJournalFiles(path string, policy s2cJournalPolicy) (used, count, tips uint64, err error) {
	var walBytes uint64
	for _, name := range []string{path, path + ".tip"} {
		info, e := os.Lstat(name)
		if e != nil {
			return 0, 0, 0, e
		}
		if !info.Mode().IsRegular() || info.Size() < 0 {
			return 0, 0, 0, errS2CJournalRecord
		}
		n := uint64(info.Size())
		if used > policy.Bytes || n > policy.Bytes-used {
			return 0, 0, 0, errS2CJournalCapacity
		}
		used += n
		if name == path {
			walBytes = n
		} else {
			if n < 77 || (n-77)%44 != 0 || (n-77)/44 > policy.Records {
				return 0, 0, 0, errS2CJournalRecord
			}
			tips = (n - 77) / 44
		}
	}
	if walBytes < 8 {
		return 0, 0, 0, errS2CJournalRecord
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	var header [8]byte
	if _, err = io.ReadFull(f, header[:]); err != nil || string(header[:]) != "LNWAL01\n" {
		return 0, 0, 0, errS2CJournalRecord
	}
	for remaining := walBytes - 8; remaining != 0; count++ {
		if remaining < 8 || count >= policy.Records {
			return 0, 0, 0, errS2CJournalRecord
		}
		if _, err = io.ReadFull(f, header[:]); err != nil {
			return 0, 0, 0, err
		}
		n := uint64(binary.BigEndian.Uint32(header[:4]))
		if n <= 36 || n > 32<<20 || n > remaining-8 {
			return 0, 0, 0, errS2CJournalRecord
		}
		if _, err = f.Seek(int64(n), io.SeekCurrent); err != nil {
			return 0, 0, 0, err
		}
		remaining -= n + 8
	}
	return used, count, tips, nil
}
