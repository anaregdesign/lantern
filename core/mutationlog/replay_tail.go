package mutationlog

import (
	"context"
	"errors"
	"math"
	"sync"
)

// tailReplayBinding is immutable and survives queue cleanup only as part of
// the original Subscription owner. It neither pins the ring nor retains Op.
type tailReplayBinding struct {
	log        *Log
	generation *byte
	fromSeq    uint64
}

// ValidateReplay checks whether an owned tail lost specifically to queue
// overflow can still replay its complete original cut from the same Log
// generation. It does not reserve history: ReplayTail revalidates atomically.
// No other terminal cause, later starting position, or boundary is eligible.
func (s *Subscription) ValidateReplay(fromSeq uint64) error {
	binding, err := s.ownedReplayBinding(fromSeq)
	if err != nil {
		return err
	}
	l := binding.log
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.validateOwnedReplayLocked(binding, fromSeq)
}

func (s *Subscription) ownedReplayBinding(fromSeq uint64) (*tailReplayBinding, error) {
	if s == nil || s.replay == nil || s.replay.log == nil || !errors.Is(s.Err(), ErrTailQueueFull) {
		return nil, errors.New("mutationlog: owned replay requires queue-overflowed tail")
	}
	if fromSeq != s.replay.fromSeq {
		return nil, ErrGapped
	}
	return s.replay, nil
}

// Caller holds mu. Every replay projection repeats this check with its next
// position, so rollover during a slow consumer cannot silently skip history.
func (l *Log) validateOwnedReplayLocked(binding *tailReplayBinding, next uint64) error {
	if l.closed {
		return ErrClosed
	}
	if l.unusableErr != nil {
		return l.unusableErr
	}
	if l.tailGeneration != binding.generation || next == 0 {
		return ErrGapped
	}
	if l.lastSeq == math.MaxUint64 {
		return ErrSeqExhausted
	}
	if next > l.lastSeq+1 || l.hasEntries && next < l.firstSeq {
		return ErrGapped
	}
	return nil
}

// ReplayTail prepares bounded replay of this queue-overflowed owned tail's
// exact original cut. The original owner's Close/drain and external credit
// refund must complete first. The returned subscription remains paused until
// start closes; this lets its caller publish ownership before any projection.
// Cancellation, log closure, or invalidation also terminates a paused replay.
//
// project follows SubscribeTail's ownership rules and must not block or call
// this Log. During replay it runs under mu on one ring entry at a time. Only
// its independently owned result crosses the unbuffered output; no original
// Op or copied replay window survives the lock. Once caught up, registration
// switches atomically to ordinary bounded owned live fan-out under mu→subsMu.
// The live queue has min(SubscriberBuffer,maxEntries) slots plus at most one
// worker-held owned entry, all of which the caller's credits must cover.
// Projector failure, later eviction, or live overflow is terminal.
func (s *Subscription) ReplayTail(fromSeq uint64, maxEntries int, project func(Entry) (Entry, error), start <-chan struct{}) (*Subscription, error) {
	if project == nil || maxEntries <= 0 || start == nil {
		return nil, errors.New("mutationlog: owned replay requires projector, positive entry limit, and start gate")
	}
	binding, err := s.ownedReplayBinding(fromSeq)
	if err != nil {
		return nil, err
	}
	l := binding.log
	l.mu.Lock()
	if err := l.validateOwnedReplayLocked(binding, fromSeq); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	state := newSubscription()
	state.replay = binding
	live := &subscription{stream: state, ch: make(chan Entry, min(l.subBuf, maxEntries)),
		project: project, directTail: true, replaying: true}
	// Register the lifecycle observer before releasing mu. It cannot receive
	// queued live traffic until replay catches up under this same lock order.
	l.subsMu.Lock()
	l.subscribers[live] = struct{}{}
	l.subsMu.Unlock()
	l.mu.Unlock()

	var cancelOnce sync.Once
	state.cancel = func() error {
		cancelOnce.Do(func() {
			state.finish(context.Canceled)
			l.subsMu.Lock()
			if _, registered := l.subscribers[live]; registered {
				delete(l.subscribers, live)
				close(live.ch)
			}
			l.subsMu.Unlock()
			<-state.stopped
			for range live.ch {
			}
		})
		return nil
	}
	go l.runOwnedReplay(state, live, binding, fromSeq, project, start)
	return state, nil
}

func (l *Log) runOwnedReplay(state *Subscription, live *subscription, binding *tailReplayBinding, next uint64, project func(Entry) (Entry, error), start <-chan struct{}) {
	defer func() {
		l.subsMu.Lock()
		if _, registered := l.subscribers[live]; registered {
			delete(l.subscribers, live)
			close(live.ch)
		}
		live.project = nil
		l.subsMu.Unlock()
		state.finish(nil)
		close(state.entries)
		close(state.stopped)
	}()
	select {
	case <-state.done:
		return
	case <-start:
	}
	for {
		owned, caughtUp, err := l.projectOwnedReplayEntry(state, live, binding, next, project)
		if err != nil {
			state.finish(err)
			return
		}
		if caughtUp {
			break
		}
		select {
		case <-state.done:
			return
		case state.entries <- owned:
		}
		next++ // MaxUint64 is rejected under mu before a replay projection.
	}
	for {
		select {
		case <-state.done:
			return
		case owned, open := <-live.ch:
			if !open {
				return
			}
			select {
			case <-state.done:
				return
			case state.entries <- owned:
			}
		}
	}
}

func (l *Log) projectOwnedReplayEntry(state *Subscription, live *subscription, binding *tailReplayBinding, next uint64, project func(Entry) (Entry, error)) (Entry, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-state.done:
		return Entry{}, false, state.Err()
	default:
	}
	if err := l.validateOwnedReplayLocked(binding, next); err != nil {
		return Entry{}, false, err
	}
	if next == l.lastSeq+1 {
		l.subsMu.Lock()
		defer l.subsMu.Unlock()
		select {
		case <-state.done:
			return Entry{}, false, state.Err()
		default:
		}
		live.startSeq = next
		live.replaying = false
		return Entry{}, true, nil
	}
	index := (l.head + int(next-l.firstSeq)) % l.capacity
	owned, err := project(l.ring[index])
	return owned, false, err
}
