package security

import (
	"context"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

// This wrapper adds copied diagnostic facts without changing the startup error
// text, chain or the authority constructor's refusal and cleanup behavior.
type authorityTimeStartupError struct {
	cause    error
	snapshot decisiontrace.StartupSnapshot
}

func (e *authorityTimeStartupError) Error() string { return e.cause.Error() }
func (e *authorityTimeStartupError) Unwrap() error { return e.cause }
func (e *authorityTimeStartupError) AuthorityTimeStartupSnapshot() decisiontrace.StartupSnapshot {
	return e.snapshot
}

func authorityTimeContextState(ctx context.Context) decisiontrace.ContextState {
	if ctx == nil {
		return decisiontrace.ContextUnobserved
	}
	switch ctx.Err() {
	case nil:
		return decisiontrace.ContextLive
	case context.Canceled:
		return decisiontrace.ContextCanceled
	case context.DeadlineExceeded:
		return decisiontrace.ContextDeadlineExceeded
	default:
		return decisiontrace.ContextOtherError
	}
}

// Called in the same owner critical section that publishes lastError. This
// bounded journal is observational and is never consulted for admission.
func (o *authorityTimeOwner) recordStartupLocked(attempt decisiontrace.StartupAttempt) {
	if attempt.Attempt == 0 {
		return
	}
	h := &o.startupHistory
	if int(h.Count) < len(h.Attempts) {
		h.Attempts[h.Count] = attempt
		h.Count++
	} else {
		copy(h.Attempts[:], h.Attempts[1:])
		h.Attempts[len(h.Attempts)-1] = attempt
		h.Overflow = true
	}
	h.PublishedAttempt = attempt.Attempt
}

// Copy the cause and its accepted history together, before the caller's
// deferred cancel/close. Read producer state separately, without nesting locks;
// it may describe a newer exchange and is explicitly not the returned cause.
func (o *authorityTimeOwner) startupFailure(anchoring, incoming context.Context) error {
	o.mu.Lock()
	cause, snapshot := o.lastError, o.startupHistory
	snapshot.OwnerFailed, snapshot.OwnerClosed = o.failed, o.closed
	snapshot.OwnerContext = authorityTimeContextState(o.runContext)
	o.mu.Unlock()
	o.producer.mu.Lock()
	snapshot.StartedAttempt = o.producer.attempts
	snapshot.CompletedAttempt = o.producer.lastAttempt.Attempt
	snapshot.InFlight = o.producer.busy
	o.producer.mu.Unlock()
	snapshot.AnchoringContext = authorityTimeContextState(anchoring)
	snapshot.IncomingContext = authorityTimeContextState(incoming)
	if deadline, ok := anchoring.Deadline(); ok {
		snapshot.RemainingNS = int64(max(time.Until(deadline), 0))
	}
	return &authorityTimeStartupError{cause: errors.Join(anchoring.Err(), cause), snapshot: snapshot}
}

// Shared by authority and OFF workload composition. Construction cancellation
// does not own a successfully transferred clock's later drain lifetime.
func waitAuthorityTimeAnchor(ctx context.Context, clock *authorityTimeOwner) error {
	anchoring, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := clock.current(); err == nil {
			return ctx.Err()
		}
		select {
		case <-anchoring.Done():
			return clock.startupFailure(anchoring, ctx)
		case <-ticker.C:
		}
	}
}
