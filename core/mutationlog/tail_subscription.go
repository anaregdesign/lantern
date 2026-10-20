package mutationlog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
)

// SubscribeTail registers an owned live tail exactly at the next local
// sequence. It never replays the ring. Callers must capture their state and
// this registration under their own publication cut so writes cannot enter
// between the two. A changed log position fails closed with ErrGapped.
//
// project must return an independently owned payload; retaining entry.Op
// defeats that ownership. It runs synchronously in the dispatcher, must not
// block or call this Log, and may reject an entry to terminate the stream.
// Callers own any external byte/count accounting, including reclamation on
// projection failure or queue overflow. The queue has the smaller of
// SubscriberBuffer and maxEntries slots; maxEntries must be positive.
// It never retains an additional original Entry in a forwarder.
//
// Done closes on overflow, projection failure, log close, invalidation, or
// explicit Close, independently of queued delivery. Close unregisters and
// discards unread projected payloads. Consumers must check Err before use.
func (l *Log) SubscribeTail(fromSeq uint64, maxEntries int, project func(Entry) (Entry, error)) (*Subscription, error) {
	if project == nil {
		return nil, errors.New("mutationlog: owned tail requires a projector")
	}
	if maxEntries <= 0 {
		return nil, errors.New("mutationlog: owned tail requires a positive entry limit")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	if l.unusableErr != nil {
		return nil, l.unusableErr
	}
	if l.lastSeq == math.MaxUint64 || fromSeq != l.lastSeq+1 {
		return nil, fmt.Errorf("%w: tail must start immediately after local seq %d", ErrGapped, l.lastSeq)
	}
	state := newSubscription()
	state.entries = make(chan Entry, min(l.subBuf, maxEntries))
	// This subscription delivers directly, without a goroutine to join.
	close(state.stopped)
	live := &subscription{
		ch: state.entries, startSeq: fromSeq, project: project,
		directTail: true, stream: state,
	}
	var cancelOnce sync.Once
	state.cancel = func() error {
		cancelOnce.Do(func() {
			l.subsMu.Lock()
			defer l.subsMu.Unlock()
			state.finish(context.Canceled)
			if _, registered := l.subscribers[live]; registered {
				delete(l.subscribers, live)
				close(live.ch)
			}
			// Projection and cancellation share subsMu, so no dispatcher
			// can enqueue after this drain or retain the projector afterward.
			for range state.entries {
			}
			live.project = nil
		})
		return nil
	}
	// mu -> subsMu is the same lock order as ordinary subscriptions.
	l.subsMu.Lock()
	l.subscribers[live] = struct{}{}
	l.subsMu.Unlock()
	return state, nil
}
