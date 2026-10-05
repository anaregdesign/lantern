package mutationlog

import (
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
)

// ResumeLogFromFileWALWithDurableTip reconstructs a detached application and
// resynchronizes its complete recovered WAL and tip before constructing a Log.
// Unlike ResumeLogFromFileWALWithTip, it syncs the exact resumed WAL writer,
// unconditionally syncs the borrowed tip writer even at an equal frontier, and
// syncs the canonical parent directory before binding either writer to the Log.
// All decoding, validation, and restoration finish before these barriers.
//
// The caller must continuously hold the same exclusive path lease, including
// throughout this call (for example inside FileWALLease.WithPath), and must keep
// decode/validate deterministic and restored payloads immutable. The callbacks
// reconstruct application state; this helper does not supply authentication,
// application size/count limits, or independent rollback protection.
//
// tip must already be open with the expected binding. The returned owner closes
// the Log and WAL only; the caller must then close tip before releasing its
// lease. On error or panic no Log is returned, the acquired WAL is closed, and
// the caller must discard detached state and close the borrowed tip. Files are
// never removed, truncated, or repaired.
func ResumeLogFromFileWALWithDurableTip(
	path string,
	opts Options,
	encode func(MutationOp) ([]byte, error),
	decode func([]byte) (MutationOp, error),
	validate func(Entry) error,
	restore func(Entry) error,
	tip *FileWALTipJournal,
) (*Log, io.Closer, error) {
	return resumeLogFromFileWALWithDurableTip(path, opts, encode, decode, validate, restore, tip,
		ResumeFileWAL, syncFileWALDirectory)
}

type resumeFileWALFunc func(string, func(MutationOp) ([]byte, error), func([]byte) (MutationOp, error), func(Entry) error) (*FileWAL, error)

// The private I/O parameters let tests wrap the actual reopened descriptor,
// without exposing a caller-controlled durability assertion in the public API.
func resumeLogFromFileWALWithDurableTip(
	path string,
	opts Options,
	encode func(MutationOp) ([]byte, error),
	decode func([]byte) (MutationOp, error),
	validate func(Entry) error,
	restore func(Entry) error,
	tip *FileWALTipJournal,
	resume resumeFileWALFunc,
	syncDirectory func(string) error,
) (_ *Log, _ io.Closer, err error) {
	if opts.WAL != nil || encode == nil || decode == nil || validate == nil || restore == nil || tip == nil {
		return nil, nil, errors.New("mutationlog: durable tip resume requires encoder, decoder, validator, restore, tip, and no configured WAL")
	}
	// Inspect the tip prefix and the entire WAL without advancing either file.
	// Resume below independently reconstructs the exact same complete bytes on
	// the descriptor which will later sync and append, before any barrier.
	cut, err := tip.inspectRecoveredPrefix(path, decode, validate)
	if err != nil {
		return nil, nil, err
	}
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	tail := &Log{capacity: capacity, ring: make([]Entry, capacity)}
	wal, err := resume(path, encode, decode, func(entry Entry) error {
		if tail.lastSeq == math.MaxUint64 || entry.Seq != tail.lastSeq+1 {
			return fmt.Errorf("%w: restored seq %d after %d", ErrFileWALSequence, entry.Seq, tail.lastSeq)
		}
		if err := restore(entry); err != nil {
			return err
		}
		tail.storeLocked(entry)
		tail.lastSeq = entry.Seq
		tail.hasEntries = true
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			err = errors.Join(err, wal.Close())
		}
	}()
	if err := wal.syncRecoveredPrefix(cut); err != nil {
		return nil, nil, err
	}
	if err := tip.catchUpRecoveredPrefix(wal.path, cut); err != nil {
		return nil, nil, err
	}
	if err := tip.syncVerifiedPrefix(); err != nil {
		return nil, nil, err
	}
	if err := syncDirectory(filepath.Dir(wal.path)); err != nil {
		return nil, nil, fmt.Errorf("mutationlog: sync recovered FileWAL directory: %w", err)
	}
	if err := wal.BindTipJournal(tip); err != nil {
		return nil, nil, err
	}
	log, owner, err := attachResumedFileWAL(opts, wal, tail)
	if err != nil {
		return nil, nil, err
	}
	transferred = true
	return log, owner, nil
}

// syncRecoveredPrefix verifies that the same descriptor replayed the complete
// inspected bytes, then guards and syncs that descriptor. A failed or panicking
// Sync leaves the writer unusable even when all bytes remain readable.
func (w *FileWAL) syncRecoveredPrefix(cut FileWALCut) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrFileWALClosed
	}
	if w.unusable || w.tip != nil || w.rawPrefix == nil || w.path == "" {
		return ErrFileWALUnusable
	}
	if w.lastSeq != cut.ObservedLast || w.offset != cut.ObservedOffset ||
		w.chain != cut.ObservedChainSHA256 || digestFileWALPrefix(w.rawPrefix) != cut.ObservedSHA256 {
		return ErrFileWALTipMismatch
	}
	w.unusable = true
	if err := w.file.Sync(); err != nil {
		return errors.Join(ErrFileWALUnusable, fmt.Errorf("mutationlog: sync recovered FileWAL prefix: %w", err))
	}
	w.unusable = false
	return nil
}

func (j *FileWALTipJournal) inspectRecoveredPrefix(path string, decode func([]byte) (MutationOp, error), validate func(Entry) error) (FileWALCut, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkRecoveredPrefix(path); err != nil {
		return FileWALCut{}, err
	}
	cut, err := InspectFileWALCut(j.walPath, j.seq, decode, validate)
	if err != nil {
		return FileWALCut{}, err
	}
	if cut.ChainSHA256 != j.chain {
		return FileWALCut{}, ErrFileWALTipMismatch
	}
	return cut, nil
}

// catchUpRecoveredPrefix consumes only the already validated immutable cut;
// there are no decoder or application callbacks after WAL recovery Sync.
func (j *FileWALTipJournal) catchUpRecoveredPrefix(path string, cut FileWALCut) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkRecoveredPrefix(path); err != nil {
		return err
	}
	if j.seq != cut.Seq || j.chain != cut.ChainSHA256 {
		return ErrFileWALTipMismatch
	}
	if err := j.advanceLocked(cut.ObservedLast, cut.ObservedChainSHA256); err != nil {
		return err
	}
	j.verified = true
	return nil
}

func (j *FileWALTipJournal) checkRecoveredPrefix(path string) error {
	if j.closed {
		return ErrFileWALTipClosed
	}
	if j.unusable || j.bound {
		return ErrFileWALTipUnusable
	}
	canonical, err := canonicalFileWALPath(path)
	if err != nil {
		return err
	}
	if canonical != j.walPath {
		return ErrFileWALTipBinding
	}
	return nil
}

func (j *FileWALTipJournal) syncVerifiedPrefix() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkRecoveredPrefix(j.walPath); err != nil {
		return err
	}
	if !j.verified {
		return ErrFileWALTipUnverified
	}
	j.unusable = true
	if err := j.file.Sync(); err != nil {
		return errors.Join(ErrFileWALTipUnusable, fmt.Errorf("mutationlog: sync recovered FileWAL tip: %w", err))
	}
	j.unusable = false
	return nil
}
