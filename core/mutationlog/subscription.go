package mutationlog

import (
	"context"
	"math"
	"sync"
)

// Subscription separates entry delivery from terminal state. Done closes as
// soon as delivery is lost, even when a consumer has not read queued entries.
// Callers that deliver over a blocking transport must watch Done independently
// and interrupt that transport when Err reports a loss.
// The zero value is not ready for use; obtain a Subscription from SubscribeStream.
type Subscription struct {
	entries    chan Entry
	done       chan struct{}
	stopped    chan struct{}
	cancel     func() error
	mu         sync.Mutex
	err        error
	finishOnce sync.Once
}

func newSubscription() *Subscription {
	return &Subscription{entries: make(chan Entry), done: make(chan struct{}), stopped: make(chan struct{})}
}

// Entries delivers the ordered retained replay and then the live tail.
// After terminal loss, an entry already in flight may still be received.
// Always inspect Err; closure alone does not prove complete delivery.
func (s *Subscription) Entries() <-chan Entry { return s.entries }

// Done closes independently of entry delivery when this subscription ends.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Err reports terminal loss, cancellation, or nil for a clean end.
// It returns nil while the subscription is still active.
func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close unregisters the subscription and joins its delivery goroutine.
// It is safe to call repeatedly, including after terminal loss.
func (s *Subscription) Close() error { return s.cancel() }

func (s *Subscription) finish(err error) {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.err = err
		close(s.done)
		s.mu.Unlock()
	})
}

// SubscribeStream opens the same ordered subscription engine used by Subscribe
// and SubscribeFiltered, with an independent terminal signal for network users.
// Filtering runs before replay/live delivery. Filters must be safe for the
// dispatcher, non-blocking, and must not close their subscription or call back
// into this Log. Overflow reports ErrSubscriptionLost; the
// consumer must revalidate its committed cursor to decide if Snapshot is needed.
func (l *Log) SubscribeStream(fromSeq uint64, filter func(Entry) bool) (*Subscription, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, ErrClosed
	}
	if l.unusableErr != nil {
		l.mu.Unlock()
		return nil, l.unusableErr
	}
	if l.hasEntries && fromSeq < l.firstSeq {
		l.mu.Unlock()
		return nil, ErrGapped
	}
	// Each replay window is copied under mu. A subsequent window may have
	// been evicted while the previous one was delivered; detect that gap
	// before sending any later entries.
	replay := l.retainedFromLocked(fromSeq)
	next := fromSeq
	exhausted := false
	if len(replay) != 0 {
		last := replay[len(replay)-1].Seq
		exhausted = last == math.MaxUint64
		if !exhausted {
			next = last + 1
		}
	}
	state := newSubscription()
	live := &subscription{
		stream:   state,
		ch:       make(chan Entry, l.subBuf),
		startSeq: l.lastSeq + 1,
		filter:   filter,
	}
	out := state.entries
	done := state.done
	registered := len(replay) == 0
	if registered {
		// Lock order is mu → subsMu. Register before releasing mu so
		// no new entry can slip between an empty replay and the live tail.
		l.subsMu.Lock()
		l.subscribers[live] = struct{}{}
		l.subsMu.Unlock()
	}
	l.mu.Unlock()

	go func() {
		defer close(state.stopped)
		defer close(out)
		defer state.finish(nil)
		for {
			for _, e := range replay {
				if filter != nil && !filter(e) {
					continue
				}
				select {
				case out <- e:
				case <-done:
					return
				}
			}
			if registered {
				for {
					select {
					case e, ok := <-live.ch:
						if !ok {
							return
						}
						select {
						case out <- e:
						case <-done:
							return
						}
					case <-done:
						return
					}
				}
			}

			l.mu.Lock()
			if l.closed || l.unusableErr != nil || (!exhausted && l.hasEntries && next < l.firstSeq) {
				cause := l.unusableErr
				if cause == nil {
					cause = ErrGapped
					if l.closed {
						cause = ErrClosed
					}
				}
				l.mu.Unlock()
				state.finish(cause)
				return
			}
			if exhausted {
				l.mu.Unlock()
				// No subsequent sequence is representable. Do not register
				// a live subscriber that could redeliver the queued max entry.
				select {
				case <-done:
				case <-l.dispatcherDone:
				}
				return
			}
			replay = l.retainedFromLocked(next)
			if len(replay) != 0 {
				last := replay[len(replay)-1].Seq
				exhausted = last == math.MaxUint64
				if !exhausted {
					next = last + 1
				}
			} else {
				l.subsMu.Lock()
				select {
				case <-done:
					l.subsMu.Unlock()
					l.mu.Unlock()
					return
				default:
					live.startSeq = l.lastSeq + 1
					l.subscribers[live] = struct{}{}
					registered = true
				}
				l.subsMu.Unlock()
			}
			l.mu.Unlock()
		}
	}()

	var cancelOnce sync.Once
	cancel := func() error {
		cancelOnce.Do(func() {
			// Stop the forwarder even if it is blocked on out <- e, then
			// unregister + close the live channel (guarded by membership so
			// we never double-close against a concurrent dispatcher gap).
			state.finish(context.Canceled)
			l.subsMu.Lock()
			defer l.subsMu.Unlock()
			if _, ok := l.subscribers[live]; ok {
				delete(l.subscribers, live)
				close(live.ch)
			}
		})
		<-state.stopped
		return nil
	}
	state.cancel = cancel
	return state, nil
}
