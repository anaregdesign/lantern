package mutationlog

import (
	"errors"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/hlc"
)

// ErrWALIndeterminate means a WAL write may have committed even though it
// returned an error. The log rejects subsequent writes rather than reusing
// the possibly committed sequence. Recovery must inspect the WAL and build a
// fresh Log before serving writes again.
var ErrWALIndeterminate = errors.New("mutationlog: WAL commit is indeterminate")

// ErrPublicationInterrupted means a WAL write succeeded but its in-memory
// publication did not complete. A caller may have installed only part of its
// external state, so the process must fail closed and recover before serving.
var ErrPublicationInterrupted = errors.New("mutationlog: publication interrupted after WAL commit")

// ErrLegacyWALUncertain means a preceding legacy Append returned an
// unclassified WAL error. Append remains retryable for compatibility, but
// that history cannot prove the unique durable sequence required by a
// receipt-capable commit on the same Log.
var ErrLegacyWALUncertain = errors.New("mutationlog: legacy WAL history is uncertain")

// ErrSeqExhausted means the log has used every uint64 sequence number. It is
// returned before WAL.Write, so it never leaves a partially committed entry.
var ErrSeqExhausted = errors.New("mutationlog: sequence exhausted")

// DefiniteWALAbort certifies that a failed WAL.Write committed no record for
// the supplied entry. A WAL implementation may return this wrapper only when
// it can prove that no part of the entry became durable or recoverable. A
// generic I/O error, timeout, or lost acknowledgement is indeterminate and
// must not be wrapped this way.
type DefiniteWALAbort struct{ Cause error }

func (e *DefiniteWALAbort) Error() string {
	if e == nil || e.Cause == nil {
		return "mutationlog: WAL write definitely aborted"
	}
	return fmt.Sprintf("mutationlog: WAL write definitely aborted: %v", e.Cause)
}

func (e *DefiniteWALAbort) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// writeReadyLocked checks all conditions that prevent assignment of another
// sequence. The caller holds l.mu.
func (l *Log) writeReadyLocked() error {
	if l.closed {
		return ErrClosed
	}
	if l.unusableErr != nil {
		return l.unusableErr
	}
	if l.hasEntries && l.lastSeq == math.MaxUint64 {
		return ErrSeqExhausted
	}
	return nil
}

func (l *Log) commitReadyLocked() error {
	if err := l.writeReadyLocked(); err != nil {
		return err
	}
	if l.legacyWALUncertain {
		return ErrLegacyWALUncertain
	}
	return nil
}

// classifyWALFailureLocked accepts seq reuse only when the WAL proves that
// its failed write could not reappear during recovery. The caller holds l.mu.
func (l *Log) classifyWALFailureLocked(err error) error {
	var aborted *DefiniteWALAbort
	if errors.As(err, &aborted) {
		return err
	}
	l.markUnusableLocked(ErrWALIndeterminate)
	return errors.Join(ErrWALIndeterminate, err)
}

// markUnusableLocked rejects writes and new subscriptions, and closes the
// existing subscriptions at their last proven prefix. The caller holds l.mu.
func (l *Log) markUnusableLocked(cause error) {
	l.unusableErr = cause
	// Existing subscribers must stop at the last proven prefix. Their
	// forwarders may drain already committed entries, then close. Future
	// Subscribe calls fail with the same cause.
	l.subsMu.Lock()
	for sub := range l.subscribers {
		close(sub.ch)
		delete(l.subscribers, sub)
	}
	l.subsMu.Unlock()
}

// CommitWithPublication writes entry to the WAL, calls publish, then makes
// entry visible in the ring, sequence status, and Subscribe stream. All three
// steps run while holding the log mutex, so another writer cannot overtake
// the entry and no log reader can observe it before publish has returned.
// This is an internal prerequisite for a larger graph/receipt commit gate;
// by itself it does not make graph, receipt, Snapshot, or RPC reads atomic.
// The caller must hold its external publication gate against all those reads
// until this call returns and must prepare an infallible publish callback.
// publish must not call back into this Log or block on a reader that needs it.
// A nil callback is allowed when no external state needs installation.
//
// A WAL error wrapped in [DefiniteWALAbort] certifies that the entry did not
// commit: publish is skipped, status remains unchanged, and the seq may be
// safely reused. Every other WAL error is indeterminate. In that case this
// Log rejects future writes with [ErrWALIndeterminate], because reusing the
// seq could conflict with a record recovered from durable storage. A panic
// after WAL.Write succeeds also poisons the Log with
// [ErrPublicationInterrupted] before propagating; callers must treat recovery
// as incomplete rather than resume serving from it.
//
// The optional SeqStamper runs before WAL.Write, as in [Log.Append]. If a
// write fails, the caller must discard the stamped payload and never expose
// it through another path. The WAL implementation must not retain mutable
// references to the entry payload after Write returns. A non-durable WAL
// such as [NopWAL] only provides an in-process ordering boundary. A durable
// WAL must make a nil Write result recoverable and replay that record before
// any restored process serves reads or writes. If a preceding legacy Append
// had an unclassified WAL error, this method rejects with
// [ErrLegacyWALUncertain]; mixing it into that history would not restore the
// unique durable sequence that Append's retry semantics cannot prove.
func (l *Log) CommitWithPublication(op MutationOp, ts hlc.Timestamp, publish func(Entry), stampers ...SeqStamper) (Entry, error) {
	return l.commitWithPublication(op, ts, publish, nil, false, stampers...)
}

// CommitWithPostRingPublication keeps external staged state hidden until the
// matching entry has been installed in the log ring and its sequence advanced.
// The callback runs while l.mu still excludes log readers, but before the
// potentially blocking dispatcher handoff. It may only release already-staged
// visibility locks: it must be infallible and must not call this Log, wait for
// a log reader, or perform another mutation. Readers of those released stores
// may then observe the committed state while log readers wait for this method
// to return; once the latter resume, the matching entry is already present.
//
// This is for a caller that staged its external state under locks before the
// WAL write. It does not by itself supply recovery or a fail-closed read API
// for a WAL error whose commit outcome is indeterminate.
func (l *Log) CommitWithPostRingPublication(op MutationOp, ts hlc.Timestamp, release func(Entry), stampers ...SeqStamper) (Entry, error) {
	return l.commitWithPublication(op, ts, nil, release, false, stampers...)
}

// CommitBoundaryWithPostRingPublication commits a private durable boundary
// without retaining or dispatching its payload. After WAL success it gaps all
// existing subscribers, removes every pre-boundary ring row, advances the
// responder-local sequence, and only then invokes release while log readers
// remain excluded. The next ordinary commit retains sequence boundary+1.
//
// This is the compaction seam used by a separately validated whole-state
// baseline. It does not validate that baseline or interpret op. A caller must
// keep its application publication cut held, prepare an infallible release,
// and recover the committed marker before serving after an interrupted
// publication.
func (l *Log) CommitBoundaryWithPostRingPublication(op MutationOp, ts hlc.Timestamp, release func(Entry)) (Entry, error) {
	return l.commitWithPublication(op, ts, nil, release, true)
}

func (l *Log) commitWithPublication(op MutationOp, ts hlc.Timestamp, publishBeforeRing, releaseAfterRing func(Entry), boundary bool, stampers ...SeqStamper) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.commitReadyLocked(); err != nil {
		return Entry{}, err
	}
	seq := l.lastSeq + 1
	if boundary && seq == math.MaxUint64 {
		return Entry{}, ErrSeqExhausted
	}
	for _, s := range stampers {
		if s != nil {
			s(seq)
		}
	}
	entry := Entry{Seq: seq, HLC: ts, Op: op}
	// A WAL implementation can panic after persisting a record. Install the
	// fail-closed guard before entering Write, not just before publish.
	walReturned := false
	resolved := false
	defer func() {
		if !resolved {
			if walReturned {
				l.markUnusableLocked(ErrPublicationInterrupted)
			} else {
				l.markUnusableLocked(ErrWALIndeterminate)
			}
		}
	}()
	if err := l.wal.Write(entry); err != nil {
		walReturned = true
		classified := l.classifyWALFailureLocked(err)
		resolved = true
		return Entry{}, classified
	}
	walReturned = true
	// Any panic after the WAL succeeds may leave a durable entry without a
	// complete in-memory publication. Fail closed even if an upper layer
	// recovers the panic, rather than allowing the next writer to reuse seq.
	if publishBeforeRing != nil {
		publishBeforeRing(entry)
	}
	if boundary {
		l.installBoundaryLocked(seq)
	} else {
		l.storeLocked(entry)
		l.lastSeq = seq
		l.hasEntries = true
	}
	if releaseAfterRing != nil {
		releaseAfterRing(entry)
	}
	if !boundary {
		l.dispatch <- entry
	}
	resolved = true
	return entry, nil
}

// installBoundaryLocked invalidates all retained replay state without
// resetting the WAL-local sequence. Queued pre-boundary dispatcher entries
// cannot reach a later subscriber because Subscribe captures startSeq from
// the advanced frontier. The caller holds l.mu.
func (l *Log) installBoundaryLocked(seq uint64) {
	clear(l.ring)
	l.head = 0
	l.size = 0
	l.firstSeq = seq + 1
	l.lastSeq = seq
	l.hasEntries = true
	if l.evicted < seq {
		l.evicted = seq
	}
	l.subsMu.Lock()
	for sub := range l.subscribers {
		sub.gapped = true
		close(sub.ch)
		delete(l.subscribers, sub)
	}
	l.subsMu.Unlock()
}
