package security

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestAuthorityTimeStartupErrorSnapshot(t *testing.T) {
	running, stop := context.WithCancel(context.Background())
	defer stop()
	anchoring, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	cause := &authorityTimeTransportError{cause: net.ErrClosed}
	p := &authorityTimeProducer{attempts: 4, busy: true, lastAttempt: decisiontrace.StartupAttempt{Attempt: 3, Stage: "packet", Reason: "source_fault"}}
	o := &authorityTimeOwner{producer: p, runContext: running, lastError: cause}
	o.mu.Lock()
	o.recordStartupLocked(decisiontrace.StartupAttempt{Attempt: 1, Stage: "packet", Reason: "source_fault"})
	o.recordStartupLocked(decisiontrace.StartupAttempt{Attempt: 2, Stage: "read", Reason: "read_timeout", HasReadBudget: true, ReadRemainingNS: int64(3 * time.Second),
		ExchangeContext: decisiontrace.ContextDeadlineExceeded, IncomingContext: decisiontrace.ContextLive})
	o.mu.Unlock()
	err := o.startupFailure(anchoring, context.Background())
	want := errors.Join(anchoring.Err(), cause)
	if err.Error() != want.Error() || !errors.Is(err, cause) || !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, net.ErrClosed) {
		t.Fatal("startup wrapper changed the original error text or chain")
	}
	var transport *authorityTimeTransportError
	if !errors.As(err, &transport) || transport != cause {
		t.Fatal("transport boundary no longer unwraps")
	}
	var diagnostic interface {
		AuthorityTimeStartupSnapshot() decisiontrace.StartupSnapshot
	}
	if !errors.As(errors.Join(errors.New("outer"), err), &diagnostic) {
		t.Fatal("startup diagnostic cannot cross provider error wrapping")
	}
	snapshot := diagnostic.AuthorityTimeStartupSnapshot()
	if snapshot.Count != 2 || snapshot.PublishedAttempt != 2 || snapshot.Attempts[1].Stage != "read" || snapshot.Attempts[1].Reason != "read_timeout" ||
		snapshot.CompletedAttempt != 3 || snapshot.StartedAttempt != 4 || !snapshot.InFlight {
		t.Fatal("accepted cause was replaced by a completed/unpublished or in-flight attempt")
	}
	if snapshot.AnchoringContext != decisiontrace.ContextDeadlineExceeded || snapshot.IncomingContext != decisiontrace.ContextLive || snapshot.OwnerContext != decisiontrace.ContextLive || snapshot.OwnerClosed || snapshot.OwnerFailed {
		t.Fatal("snapshot did not retain the pre-cleanup context state")
	}
	// Neither caller mutation nor subsequent cleanup/owner work can alter the
	// already returned failure or the value returned by its structural accessor.
	snapshot.Attempts[1].Reason = "mutated"
	snapshot.Count = 0
	stop()
	o.closed, o.failed, o.lastError = true, true, errors.New("later")
	o.startupHistory.Attempts[1].Reason = "later"
	p.lastAttempt.Attempt = 99
	again := diagnostic.AuthorityTimeStartupSnapshot()
	if again.Count != 2 || again.Attempts[1].Reason != "read_timeout" || again.OwnerContext != decisiontrace.ContextLive || again.OwnerClosed || again.OwnerFailed || err.Error() != want.Error() {
		t.Fatal("returned startup diagnostic aliases mutable owner or caller state")
	}
}

func TestAuthorityTimeStartupHistoryBound(t *testing.T) {
	anchoring, cancel := context.WithCancel(context.Background())
	cancel()
	p := &authorityTimeProducer{attempts: 6, lastAttempt: decisiontrace.StartupAttempt{Attempt: 6}}
	o := &authorityTimeOwner{producer: p, runContext: context.Background()}
	var latest error
	for attempt := uint64(1); attempt <= 6; attempt++ {
		latest = &authorityTimeTransportError{cause: net.ErrClosed}
		o.mu.Lock()
		o.lastError = latest
		o.recordStartupLocked(decisiontrace.StartupAttempt{Attempt: attempt, Stage: "read", Reason: "read_timeout"})
		o.mu.Unlock()
	}
	err := o.startupFailure(anchoring, context.Background())
	var diagnostic interface {
		AuthorityTimeStartupSnapshot() decisiontrace.StartupSnapshot
	}
	if !errors.As(err, &diagnostic) || !errors.Is(err, latest) {
		t.Fatal("bounded history lost the actual returned cause")
	}
	snapshot := diagnostic.AuthorityTimeStartupSnapshot()
	if snapshot.Count != 4 || !snapshot.Overflow || snapshot.PublishedAttempt != 6 {
		t.Fatal("history bound/truncation is not explicit")
	}
	for i, attempt := range snapshot.Attempts {
		if attempt.Attempt != uint64(i+3) {
			t.Fatal("bounded history lost chronological latest-attempt order")
		}
	}
}

func TestAuthorityTimeStartupHistoryFollowsOwnerPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		p := &authorityTimeProducer{}
		o := &authorityTimeOwner{producer: p, runContext: ctx, cancel: cancel, done: make(chan struct{})}
		first := &authorityTimeTransportError{cause: net.ErrClosed}
		second := errAuthorityTimeSourceFault
		var attempts uint64
		go o.run(ctx, func(context.Context) (authorityTimeMeasurement, error) {
			attempts++
			p.mu.Lock()
			p.attempts = attempts
			p.lastProbe = decisiontrace.Event{Attempt: attempts, Stage: "read", Reason: "read_timeout"}
			p.lastAttempt = decisiontrace.StartupAttempt{Attempt: attempts, ExchangeContext: decisiontrace.ContextLive, IncomingContext: decisiontrace.ContextLive}
			p.mu.Unlock()
			if attempts == 1 {
				return authorityTimeMeasurement{}, first
			}
			return authorityTimeMeasurement{}, second
		})
		synctest.Wait()
		anchoring, stop := context.WithCancel(context.Background())
		stop()
		err := o.startupFailure(anchoring, context.Background())
		if !errors.Is(err, first) {
			t.Fatal("first owner publication was not returned")
		}
		time.Sleep(32 * time.Second)
		synctest.Wait()
		err = o.startupFailure(anchoring, context.Background())
		var diagnostic interface {
			AuthorityTimeStartupSnapshot() decisiontrace.StartupSnapshot
		}
		if !errors.Is(err, second) || errors.Is(err, first) || !errors.As(err, &diagnostic) {
			t.Fatal("latest owner error and history publication diverged")
		}
		snapshot := diagnostic.AuthorityTimeStartupSnapshot()
		if snapshot.Count != 2 || snapshot.PublishedAttempt != 2 || snapshot.Attempts[0].Attempt != 1 || snapshot.Attempts[1].Attempt != 2 {
			t.Fatal("owner did not preserve both accepted startup attempts")
		}
		o.close()
		if attempts != 2 {
			t.Fatal("observation changed the existing retry schedule")
		}
	})
}

func TestWaitAuthorityTimeAnchorUsesIncomingCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &authorityTimeOwner{producer: &authorityTimeProducer{}}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- waitAuthorityTimeAnchor(ctx, clock) }()
		synctest.Wait()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		clock := &authorityTimeOwner{producer: &authorityTimeProducer{}}
		started := time.Now()
		err := waitAuthorityTimeAnchor(t.Context(), clock)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != 45*time.Second {
			t.Fatal("anchor startup bound changed", err, time.Since(started))
		}
	})
}
