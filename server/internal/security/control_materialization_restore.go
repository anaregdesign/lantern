package security

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

// Resume cannot infer either root or lower bound from the capsule it is asked
// to recover. The minimum genesis receipt proves continuity only; an external
// post-genesis receipt is needed to detect rollback of an entire valid family.
func resumeS2Local(path string, scope s2LocalScope, genesis *s2TrustedGenesis, replay []S1CertifiedNext, required s2LocalReceipt) (*s2LocalOwner, error) {
	return resumeS2LocalWithHooks(path, scope, genesis, replay, required, nil)
}

// The extra argument is a package-private native test checkpoint. It cannot
// replace the WAL, supply authentication or change the verified replay rules.
func resumeS2LocalWithHooks(path string, scope s2LocalScope, genesis *s2TrustedGenesis, replay []S1CertifiedNext, required s2LocalReceipt, hooks *s2LocalHooks) (_ *s2LocalOwner, err error) {
	o, err := s2NewLocalCandidate(path, scope, genesis)
	if err != nil {
		return nil, err
	}
	index, e := s2LocalIndex(required.ControlSlot)
	if e != nil || required.ScopeDigest != scope.digest() || required.LocalIndex != index || required.LocalIndex > scope.Storage.ReplayRecords || required.Chain == [32]byte{} || required.CapsuleDigest == [32]byte{} || required.ControlPrefix == [32]byte{} || uint64(len(replay)) >= scope.Storage.ReplayRecords {
		return nil, errS2Restore
	}
	// Own the opaque historical inputs for every validation pass. No retained
	// capsule data is converted into a genesis or certification capability.
	proof := make([]S1CertifiedNext, len(replay))
	for i, next := range replay {
		if next.certificate == nil {
			return nil, errS2Restore
		}
		c := *next.certificate
		proof[i].certificate = &c
		if next.handoff != nil {
			h := *next.handoff
			if h.authorization == nil {
				return nil, errS2Restore
			}
			a := *h.authorization
			h.authorization = &a
			proof[i].handoff = &h
		}
	}
	o.hooks = hooks
	o.lease, err = mutationlog.AcquireFileWALLease(path)
	if err != nil {
		return nil, err
	}
	transferred := false
	// Covers errors and panics, including the first callback during replay.
	// Each next resource is assigned directly to this cleanup-owned candidate.
	defer func() {
		if !transferred {
			err = errors.Join(err, o.closeResources())
		}
	}()
	err = o.lease.WithPath(func(canonical string) error {
		if e := s2LocalLeaseEmpty(canonical); e != nil {
			return e
		}
		used, count, e := s2LocalFiles(canonical, scope.Storage)
		if e != nil {
			return e
		}
		if count == 0 || count != uint64(len(proof))+1 || required.LocalIndex > count {
			return errS2Restore
		}
		o.tip, e = mutationlog.ResumeFileWALTipJournal(canonical, scope.tipBinding())
		if e != nil {
			return e
		}
		tipSeq, _, _ := o.tip.Frontier()
		if tipSeq > count || count-tipSeq > 1 {
			return errS2Restore
		}
		missing := uint64(0)
		if tipSeq < count {
			missing = 44
		}
		if used > scope.Storage.JournalBytes || missing > scope.Storage.JournalBytes-used {
			return errS2Unpersistable
		}
		decode := func(raw []byte) (mutationlog.MutationOp, error) { return s2DecodeLocalRecord(raw, scope) }
		validate := func(entry mutationlog.Entry) error {
			r, ok := entry.Op.(*s2LocalRecord)
			if !ok || r == nil || entry.HLC != (hlc.Timestamp{}) || entry.Seq != r.localIndex || entry.Seq > count {
				return errS2LocalRecord
			}
			return nil
		}
		// Integrity inspection proves the required physical commitment occurs in
		// the complete selected file; later semantic replay must also match its
		// whole capsule and control prefix. Never trim an unexplained suffix.
		cut, e := mutationlog.InspectFileWALCut(canonical, required.LocalIndex, decode, validate)
		if e != nil {
			return e
		}
		if cut.ObservedLast != count || cut.ChainSHA256 != required.Chain {
			return errS2Restore
		}
		capsule, _, e := s2EncodeCapsule(o.state, o.roots, scope.Storage.Capsule)
		if e != nil {
			return e
		}
		current := o.state
		previous := s2LocalCapsuleDigest(capsule)
		var restored uint64
		restore := func(entry mutationlog.Entry) error {
			if o.hooks != nil && o.hooks.replay != nil {
				o.hooks.replay()
			}
			if e := validate(entry); e != nil {
				return e
			}
			r := entry.Op.(*s2LocalRecord)
			if entry.Seq != restored+1 {
				return errS2Restore
			}
			if restored == 0 {
				if r.kind != s2LocalGenesis {
					return errS2Restore
				}
			} else {
				if r.kind != s2LocalApply || r.previousCapsuleDigest != previous || r.commit != proof[restored-1].certificate.commit {
					return errS2Restore
				}
				result, e := ApplyS1(current, proof[restored-1])
				if e != nil {
					return errors.Join(errS2Restore, e)
				}
				current = result.State
				capsule, _, e = s2EncodeCapsule(current, o.roots, scope.Storage.Capsule)
				if e != nil {
					return e
				}
			}
			if !bytes.Equal(capsule, []byte(r.capsule)) || r.capsuleDigest != s2LocalCapsuleDigest(capsule) {
				return errS2Restore
			}
			if entry.Seq == required.LocalIndex && (r.capsuleDigest != required.CapsuleDigest || current.slot != required.ControlSlot || current.prefix != required.ControlPrefix) {
				return errS2Restore
			}
			previous = r.capsuleDigest
			restored++
			return nil
		}
		// The bridge completes detached reconstruction before synchronizing its
		// exact resumed WAL FD, then catches up and unconditionally synchronizes
		// its exact tip FD and canonical directory before binding the live Log.
		o.log, o.walOwner, e = mutationlog.ResumeLogFromFileWALWithDurableTip(canonical, mutationlog.Options{Capacity: 1}, s2EncodeLocalPayload, decode, validate, restore, o.tip)
		if e != nil {
			return e
		}
		if restored != count {
			return errS2Restore
		}
		o.provenance, e = o.log.FileWALTipProvenance(canonical)
		if e != nil {
			return e
		}
		w, e := o.provenance.TipWitness(canonical)
		if e != nil {
			return e
		}
		if w.Seq != count || w.ChainSHA256 != cut.ObservedChainSHA256 {
			return errS2Restore
		}
		actual, actualCount, e := s2LocalFiles(canonical, scope.Storage)
		if e != nil {
			return e
		}
		if actual != used+missing || actualCount != count {
			return errS2Restore
		}
		// All proof, allocation, accounting and provenance checks precede this
		// one virgin installation. No fallible initialization follows it.
		receipt := s2Receipt(required.ScopeDigest, current, previous, w)
		frozenCapsule := string(capsule)
		if e = o.metadata.InstallRecovered(count, capsule); e != nil {
			return e
		}
		o.state, o.capsule, o.receipt, o.used = current, frozenCapsule, receipt, actual
		o.offset = w.Offset
		return nil
	})
	if err != nil {
		return nil, err
	}
	transferred = true
	return o, nil
}

// Bound actual bytes and physical record count before decoding any payload.
// This framing-only preflight does not replace the core CRC, path, complete
// frame, sequence or canonical application validation passes.
func s2LocalFiles(path string, policy s2LocalStoragePolicy) (uint64, uint64, error) {
	var used uint64
	var walBytes int64
	for _, p := range []string{path, path + ".tip"} {
		info, err := os.Lstat(p)
		if err != nil {
			return 0, 0, err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 {
			return 0, 0, errS2LocalRecord
		}
		n := uint64(info.Size())
		if used > policy.JournalBytes || n > policy.JournalBytes-used {
			return 0, 0, errS2Unpersistable
		}
		used += n
		if p == path {
			walBytes = info.Size()
		} else if n < 77 || (n-77)%44 != 0 || (n-77)/44 > policy.ReplayRecords {
			return 0, 0, errS2LocalRecord
		}
	}
	if walBytes < 8 {
		return 0, 0, errS2LocalRecord
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var header [8]byte
	if _, err = io.ReadFull(f, header[:]); err != nil || string(header[:]) != "LNWAL01\n" {
		return 0, 0, errS2LocalRecord
	}
	remaining := uint64(walBytes - 8)
	var count uint64
	for remaining != 0 {
		if remaining < 8 || count >= policy.ReplayRecords {
			return 0, 0, errS2LocalRecord
		}
		if _, err = io.ReadFull(f, header[:]); err != nil {
			return 0, 0, err
		}
		n := uint64(binary.BigEndian.Uint32(header[:4]))
		if n < 36 || n > 32<<20 || n > remaining-8 {
			return 0, 0, errS2LocalRecord
		}
		if _, err = f.Seek(int64(n), io.SeekCurrent); err != nil {
			return 0, 0, err
		}
		remaining -= n + 8
		count++
	}
	return used, count, nil
}
