package mutationlog

import (
	"errors"
	"fmt"
	"io"
	"math"
)

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
	return log, &leasedResumedLogCloser{owner: owner, lease: lease}, nil
}

// Close releases the WAL writer before the path lease, so another process
// cannot acquire the path while this Log may still append.
type leasedResumedLogCloser struct {
	owner io.Closer
	lease *FileWALLease
}

func (c *leasedResumedLogCloser) Close() error {
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
