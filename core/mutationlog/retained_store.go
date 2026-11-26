package mutationlog

// RetainedStore backs new Logs whose replay payloads already reside in the
// application's atomic commit. Reference returns a compact immutable identity
// and must also accept its own references when resuming a verified Log.
// Pin samples the active durable cut while the Log mutex excludes commits.
type RetainedStore interface {
	Reference(Entry) MutationOp
	Pin() (RetainedReader, error)
}

// RetainedReader resolves immutable references from a single durable cut.
// Closing the reader releases its storage pin, including on cancellation.
type RetainedReader interface {
	Read(Entry) (Entry, error)
	Close() error
}

type replayWindow struct {
	entries []Entry
	reader  RetainedReader
	unpin   func()
}

func (l *Log) replayWindowLocked(from uint64) (replayWindow, error) {
	w := replayWindow{entries: l.retainedFromLocked(from)}
	if len(w.entries) == 0 || l.retained == nil {
		return w, nil
	}
	if l.closed {
		return replayWindow{}, ErrClosed
	}
	reader, err := l.retained.Pin()
	if err != nil {
		return replayWindow{}, err
	}
	l.replayPins.Add(1)
	w.reader = reader
	w.unpin = l.replayPins.Done
	return w, nil
}

func (w *replayWindow) close() {
	if w.reader != nil {
		_ = w.reader.Close()
		w.reader = nil
		w.unpin()
	}
	w.entries = nil
}

func (w *replayWindow) read(ref Entry) (Entry, error) {
	if w.reader == nil {
		return ref, nil
	}
	return w.reader.Read(ref)
}

// VisitRetained visits full entries one at a time from one exact Log cut.
// The visitor runs outside the Log mutex. Its error terminates the scan.
// Close waits for the scan to release its storage pin, so it must run after
// the visitor returns.
func (l *Log) VisitRetained(visit func(Entry) error) error {
	l.mu.RLock()
	w, err := l.replayWindowLocked(l.firstSeq)
	l.mu.RUnlock()
	if err != nil {
		return err
	}
	defer w.close()
	for _, ref := range w.entries {
		e, err := w.read(ref)
		if err != nil {
			return err
		}
		if err = visit(e); err != nil {
			return err
		}
	}
	return nil
}
