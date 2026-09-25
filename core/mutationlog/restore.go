package mutationlog

import (
	"errors"
	"fmt"
	"io"
	"math"
)

// CreateLeasedLogWithFileWAL creates a new durable Log while holding one
// exclusive path lease from file creation through shutdown. An existing WAL is
// never overwritten or silently resumed. The returned owner closes the Log
// and writer before releasing the lease. This only establishes WAL/Log
// ownership; callers still own application-level publication and recovery.
func CreateLeasedLogWithFileWAL(path string, opts Options, encode func(MutationOp) ([]byte, error)) (*Log, io.Closer, error) {
	if opts.WAL != nil {
		return nil, nil, errors.New("mutationlog: leased Log cannot replace a configured WAL")
	}
	if encode == nil {
		return nil, nil, errors.New("mutationlog: FileWAL encoder is nil")
	}
	lease, err := AcquireFileWALLease(path)
	if err != nil {
		return nil, nil, err
	}
	transferred := false
	var wal *FileWAL
	defer func() {
		if !transferred {
			if wal != nil {
				_ = wal.Close()
			}
			_ = lease.Close()
		}
	}()
	err = lease.WithPath(func(canonicalPath string) error {
		var createErr error
		wal, createErr = CreateFileWAL(canonicalPath, encode)
		return createErr
	})
	if err != nil {
		return nil, nil, errors.Join(err, lease.Close())
	}
	opts.WAL = wal
	log := New(opts)
	transferred = true
	return log, &leasedFileWALLogCloser{
		owner: &resumedLogCloser{log: log, wal: wal},
		lease: lease,
	}, nil
}

// CreateLeasedLogWithFileWALTip creates a fresh WAL and tip sidecar under one
// lease, binds their empty genesis frontier, and retains all three owners
// through Log shutdown. Existing or partially created files fail closed;
// callers must not remove them automatically after an uncertain creation
// failure. binding must identify the active application epoch and policy.
func CreateLeasedLogWithFileWALTip(
	path string,
	opts Options,
	encode func(MutationOp) ([]byte, error),
	binding [32]byte,
) (_ *Log, _ io.Closer, err error) {
	if opts.WAL != nil || encode == nil || binding == ([32]byte{}) {
		return nil, nil, errors.New("mutationlog: fresh tipped Log requires a FileWAL encoder, binding, and no configured WAL")
	}
	lease, err := AcquireFileWALLease(path)
	if err != nil {
		return nil, nil, err
	}
	var wal *FileWAL
	var tip *FileWALTipJournal
	transferred := false
	defer func() {
		if !transferred {
			if tip != nil {
				err = errors.Join(err, tip.Close())
			}
			if wal != nil {
				err = errors.Join(err, wal.Close())
			}
			err = errors.Join(err, lease.Close())
		}
	}()
	err = lease.WithPath(func(canonicalPath string) error {
		var createErr error
		wal, createErr = CreateFileWAL(canonicalPath, encode)
		if createErr != nil {
			return createErr
		}
		tip, createErr = CreateFileWALTipJournal(canonicalPath, binding)
		if createErr != nil {
			return createErr
		}
		// A new O_EXCL FileWAL must contain only its version header. Reject
		// any unexpected frame rather than attesting unknown application data.
		unexpectedFrame := func([]byte) (MutationOp, error) {
			return nil, ErrFileWALTipMismatch
		}
		if createErr = tip.VerifyAndCatchUp(canonicalPath, unexpectedFrame, func(Entry) error {
			return ErrFileWALTipMismatch
		}); createErr != nil {
			return createErr
		}
		return wal.BindTipJournal(tip)
	})
	if err != nil {
		return nil, nil, err
	}
	opts.WAL = wal
	log := New(opts)
	transferred = true
	return log, &leasedFileWALTipLogCloser{
		owner: &resumedLogCloser{log: log, wal: wal},
		tip:   tip,
		lease: lease,
	}, nil
}

type leasedFileWALTipLogCloser struct {
	owner io.Closer
	tip   *FileWALTipJournal
	lease *FileWALLease
}

func (c *leasedFileWALTipLogCloser) Close() error {
	return errors.Join(c.owner.Close(), c.tip.Close(), c.lease.Close())
}

// ResumeLogFromFileWAL validates and replays a complete FileWAL, then returns
// an in-memory Log at exactly the same local sequence frontier. restore must
// install every decoded entry into application state before returning nil.
// File framing, checksums, contiguous sequences, and payload decoding are
// checked in full before restore is called for the first entry. Replay only
// fills the bounded Log ring: it never rewrites the WAL or fans out entries.
// The first later Log commit uses the sequence after the recovered frontier.
//
// opts.WAL must be nil because the returned Log is bound to the resumed
// FileWAL. The returned io.Closer stops the Log and then closes its WAL; callers
// must use it when finished rather than closing the WAL independently. The
// caller must own the WAL path exclusively throughout replay and operation.
// decode must produce the same result across the validation and restore passes,
// and restored Entry.Op payloads must remain immutable while retained.
// On any error, neither a Log nor a closer is returned, and restore's partial
// application state must be discarded by the caller.
//
// This helper certifies only the Log/WAL sequence and replay window. The
// caller owns graph, receipt, origin epoch, policy, and highwater recovery and
// must not serve application traffic until those states match the WAL. The
// production server does not wire FileWAL recovery yet.
func ResumeLogFromFileWAL(
	path string,
	opts Options,
	encode func(MutationOp) ([]byte, error),
	decode func([]byte) (MutationOp, error),
	restore func(Entry) error,
) (*Log, io.Closer, error) {
	return resumeLogFromFileWAL(path, opts, encode, decode, nil, restore, nil)
}

// ResumeLogFromFileWALWithTip requires an existing tip journal from the same
// exclusive path lease. It replays the complete WAL into the caller's
// detached application state, checks its published prefix against the tip,
// durably attests any valid extra suffix, and binds that journal to every
// future FileWAL.Write before constructing the live Log. The caller owns
// closing the journal after the returned Log owner and before the lease.
// A failed restore may have changed application state; discard it.
func ResumeLogFromFileWALWithTip(
	path string,
	opts Options,
	encode func(MutationOp) ([]byte, error),
	decode func([]byte) (MutationOp, error),
	validate func(Entry) error,
	restore func(Entry) error,
	tip *FileWALTipJournal,
) (*Log, io.Closer, error) {
	if validate == nil || tip == nil {
		return nil, nil, errors.New("mutationlog: FileWAL tip validator and journal are required")
	}
	return resumeLogFromFileWAL(path, opts, encode, decode, validate, restore, tip)
}

func resumeLogFromFileWAL(
	path string,
	opts Options,
	encode func(MutationOp) ([]byte, error),
	decode func([]byte) (MutationOp, error),
	validate func(Entry) error,
	restore func(Entry) error,
	tip *FileWALTipJournal,
) (*Log, io.Closer, error) {
	if opts.WAL != nil {
		return nil, nil, errors.New("mutationlog: resumed Log cannot replace a configured WAL")
	}
	if encode == nil || decode == nil || restore == nil {
		return nil, nil, errors.New("mutationlog: FileWAL encoder, decoder, and restore callback are required")
	}
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	// A temporary Log uses only its ring fields; it has no dispatcher, WAL,
	// subscribers, or external references during replay.
	tail := &Log{capacity: capacity, ring: make([]Entry, capacity)}
	wal, err := ResumeFileWAL(path, encode, decode, func(entry Entry) error {
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
	if tip != nil {
		if err := tip.VerifyAndCatchUp(path, decode, validate); err != nil {
			return nil, nil, errors.Join(err, wal.Close())
		}
		if err := wal.BindTipJournal(tip); err != nil {
			return nil, nil, errors.Join(err, wal.Close())
		}
	}
	return attachResumedFileWAL(opts, wal, tail)
}

// ResumeLeasedLogFromFileWAL acquires the path lease before the first WAL
// validation pass and keeps it until the returned owner closes the Log and
// writer. The caller must discard any application state partly changed by a
// failing restore callback. A successful return proves only Log/WAL sequence
// continuity; receipt-capable serving also needs a certified graph, Store,
// clock, epoch, and origin cut.
func ResumeLeasedLogFromFileWAL(
	path string,
	opts Options,
	encode func(MutationOp) ([]byte, error),
	decode func([]byte) (MutationOp, error),
	restore func(Entry) error,
) (*Log, io.Closer, error) {
	lease, err := AcquireFileWALLease(path)
	if err != nil {
		return nil, nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = lease.Close()
		}
	}()
	var log *Log
	var owner io.Closer
	err = lease.WithPath(func(canonicalPath string) error {
		var restoreErr error
		log, owner, restoreErr = ResumeLogFromFileWAL(canonicalPath, opts, encode, decode, restore)
		return restoreErr
	})
	if err != nil {
		if owner != nil {
			err = errors.Join(err, owner.Close())
		}
		return nil, nil, errors.Join(err, lease.Close())
	}
	if log == nil || owner == nil {
		missing := errors.New("mutationlog: resumed Log owner is missing")
		if owner != nil {
			missing = errors.Join(missing, owner.Close())
		}
		return nil, nil, errors.Join(missing, lease.Close())
	}
	transferred = true
	return log, &leasedFileWALLogCloser{owner: owner, lease: lease}, nil
}

// Close releases the WAL writer before the path lease, so another process
// cannot acquire the path while this Log may still append.
type leasedFileWALLogCloser struct {
	owner io.Closer
	lease *FileWALLease
}

func (c *leasedFileWALLogCloser) Close() error {
	return errors.Join(c.owner.Close(), c.lease.Close())
}

// attachResumedFileWAL is the final ownership transfer. Keep every frontier
// check before New starts the dispatcher; an invalid restore closes the writer.
func attachResumedFileWAL(opts Options, wal *FileWAL, tail *Log) (*Log, io.Closer, error) {
	if wal == nil || tail == nil {
		if wal != nil {
			_ = wal.Close()
		}
		return nil, nil, errors.New("mutationlog: resumed WAL and Log tail are required")
	}
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	valid := wal.lastSeq == tail.lastSeq &&
		(wal.lastSeq != 0) == tail.hasEntries &&
		tail.capacity == capacity &&
		tail.size >= 0 && tail.size <= tail.capacity &&
		tail.capacity > 0 && len(tail.ring) == tail.capacity &&
		tail.head >= 0 && tail.head < tail.capacity &&
		tail.lastSeq >= uint64(tail.size) &&
		tail.evicted == tail.lastSeq-uint64(tail.size)
	if valid && tail.hasEntries {
		valid = tail.size > 0 && tail.firstSeq == tail.lastSeq-uint64(tail.size)+1
	} else if valid {
		valid = tail.size == 0 && tail.evicted == 0
	}
	if !valid {
		_ = wal.Close()
		return nil, nil, fmt.Errorf("%w: FileWAL and Log restore frontiers differ", ErrFileWALSequence)
	}
	opts.WAL = wal
	log := New(opts)
	log.mu.Lock()
	log.ring = tail.ring
	log.head = tail.head
	log.size = tail.size
	log.firstSeq = tail.firstSeq
	log.lastSeq = tail.lastSeq
	log.evicted = tail.evicted
	log.hasEntries = tail.hasEntries
	log.mu.Unlock()
	lastSeq, hasEntries := log.LastSeq()
	if lastSeq != wal.lastSeq || hasEntries != tail.hasEntries {
		_ = log.Close()
		_ = wal.Close()
		return nil, nil, fmt.Errorf("%w: resumed Log frontier differs from FileWAL", ErrFileWALSequence)
	}
	return log, &resumedLogCloser{log: log, wal: wal}, nil
}

// resumedLogCloser prevents callers from writing directly to the FileWAL and
// getting it ahead of the Log. Closing the Log first also waits for any active
// commit to leave the WAL before its file descriptor is released.
type resumedLogCloser struct {
	log *Log
	wal *FileWAL
}

func (c *resumedLogCloser) Close() error {
	return errors.Join(c.log.Close(), c.wal.Close())
}
