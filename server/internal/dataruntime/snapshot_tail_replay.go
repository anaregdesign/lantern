package dataruntime

import (
	"context"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

// prepareRingReplay retires only an unclaimed queue that overflowed. The same
// owner, cut, native admission and deadline survive; no payload or new queue is
// retained until claim. Actual ring continuity is checked at finish, claim and
// by the bounded core replay before each projected entry.
func (t *snapshotTail) prepareRingReplay() error {
	t.transitionMu.Lock()
	defer t.transitionMu.Unlock()
	return t.prepareRingReplayLocked()
}

func (t *snapshotTail) prepareRingReplayLocked() error {
	m := t.manager
	m.mu.Lock()
	if t.closed || t.claimed {
		m.mu.Unlock()
		return mutationlog.ErrSubscriptionLost
	}
	if t.ringReplay {
		m.mu.Unlock()
		return nil
	}
	err := t.subscription.Err()
	if err == nil {
		m.mu.Unlock()
		return nil
	}
	// A projector budget failure is also ErrSubscriptionLost, but must never
	// acquire replay eligibility. Only the core queue's authoritative sentinel
	// permits this transition, and never after an HTTP consumer has claimed it.
	if !errors.Is(err, mutationlog.ErrTailQueueFull) {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	_ = t.subscription.Close() // unregister, join projection and discard owned bytes
	m.mu.Lock()
	m.totalBytes -= t.bytes
	m.totalEntries -= t.entries
	t.bytes, t.entries = 0, 0
	t.ringReplay = true
	m.mu.Unlock()
	return nil
}

func (t *snapshotTail) ringReplayRequired() bool {
	t.manager.mu.Lock()
	defer t.manager.mu.Unlock()
	return t.ringReplay
}

func (t *snapshotTail) currentSubscription() *mutationlog.Subscription {
	t.manager.mu.Lock()
	defer t.manager.mu.Unlock()
	if t.deliverySubscription != nil {
		return t.deliverySubscription
	}
	return t.subscription
}

// A select may already have chosen the old queue's Done while claim publishes
// the replacement. Compare again after serializing with claim so that stale
// notification cannot tear down a newly claimed, healthy replay.
func (t *snapshotTail) observeSubscriptionEnd(observed *mutationlog.Subscription) bool {
	t.transitionMu.Lock()
	defer t.transitionMu.Unlock()
	t.manager.mu.Lock()
	current := t.subscription
	if t.ringReplay {
		current = t.deliverySubscription
	}
	stale := observed != current
	t.manager.mu.Unlock()
	if stale {
		return true
	}
	return t.prepareRingReplayLocked() == nil
}

func (t *snapshotTail) unclaimedReplayLost() bool {
	t.transitionMu.Lock()
	defer t.transitionMu.Unlock()
	t.manager.mu.Lock()
	check := t.ringReplay && !t.claimed && !t.closed
	t.manager.mu.Unlock()
	if !check {
		return false
	}
	if err := t.subscription.ValidateReplay(t.local + 1); err != nil {
		t.rememberTerminal(snapshotTailCoreReason(err))
		return true
	}
	return false
}

// claim runs inside the current publication cut, and bypasses only ring replay
// admission. It never bypasses per-frame validation or receiver ApplyMutation.
func (s *Replication) claimSnapshotTail(ctx context.Context, token string, req *pb.SubscribeRequest, cursor map[string]uint64, fault <-chan struct{}) (*snapshotTail, error) {
	current, ok := peerauth.AdmissionFromContext(ctx)
	if !ok || current.Check(ctx) != nil {
		return nil, connect.NewError(connect.CodeUnavailable, peerauth.ErrMembership)
	}
	m := s.snapshotTails
	m.mu.Lock()
	tail := m.active[token]
	m.mu.Unlock()
	if tail == nil {
		// No tombstone is retained after cleanup. Do not invent its old cause.
		return nil, snapshotTailError("claim", "reservation_missing", "unknown")
	}
	if !tail.admission.SamePeer(current) {
		return nil, snapshotTailError("claim", "peer_mismatch", "")
	}
	if tail.admission.Check(ctx) != nil {
		return nil, tail.diagnostic("claim", "original_admission_rejected", "")
	}
	tail.transitionMu.Lock()
	defer tail.transitionMu.Unlock()
	if err := tail.prepareRingReplayLocked(); err != nil {
		return nil, tail.diagnostic("claim", "subscription_terminal", "")
	}
	m.mu.Lock()
	guard := ""
	switch {
	case tail.closed:
		guard = "owner_closed"
	case !tail.ready:
		guard = "not_ready"
	case tail.claimed:
		guard = "already_claimed"
	case !tail.ringReplay && tail.subscription.Err() != nil:
		guard = "subscription_terminal"
	case tail.fault != fault:
		guard = "generation_mismatch"
	case !time.Now().Before(tail.deadline):
		guard = "deadline_expired"
	case req.GetFromLocalSeq() != tail.local+1:
		guard = "local_cursor_mismatch"
	}
	if guard != "" {
		err := tail.diagnosticLocked("claim", guard, "")
		m.mu.Unlock()
		return nil, err
	}
	select {
	case <-tail.fault:
		err := tail.diagnosticLocked("claim", "generation_closed", "")
		m.mu.Unlock()
		return nil, err
	default:
	}
	for origin, cutoff := range tail.origins {
		if cutoff == math.MaxUint64 {
			err := tail.diagnosticLocked("claim", "origin_exhausted", "")
			m.mu.Unlock()
			return nil, err
		}
		if cursor[origin] < cutoff+1 {
			err := tail.diagnosticLocked("claim", "origin_cursor_mismatch", "")
			m.mu.Unlock()
			return nil, err
		}
	}
	replay := tail.ringReplay
	m.mu.Unlock()
	var candidate *mutationlog.Subscription
	var start chan struct{}
	if replay {
		start = make(chan struct{})
		var err error
		candidate, err = tail.subscription.ReplayTail(tail.local+1, maxSnapshotTailEntries, tail.project, start)
		if err != nil {
			return nil, tail.diagnostic("claim", "replay_unavailable", snapshotTailCoreReason(err))
		}
	}
	// Core setup happens outside manager.mu. Teardown cannot pass transitionMu,
	// and the paused candidate cannot project before its owner is published.
	if ctx.Err() != nil || current.Check(ctx) != nil || tail.admission.Check(ctx) != nil {
		if candidate != nil {
			_ = candidate.Close()
		}
		return nil, tail.diagnostic("claim", "admission_rejected", "admission_rejected")
	}
	m.mu.Lock()
	select {
	case <-tail.fault:
		guard = "generation_closed"
	default:
	}
	if !time.Now().Before(tail.deadline) {
		guard = "deadline_expired"
	}
	if tail.closed || m.closed {
		guard = "owner_closed"
	}
	if guard != "" {
		err := tail.diagnosticLocked("claim", guard, "")
		m.mu.Unlock()
		if candidate != nil {
			_ = candidate.Close()
		}
		return nil, err
	}
	tail.deliverySubscription = candidate
	tail.claimed = true
	m.mu.Unlock()
	if start != nil {
		close(start)
	}
	select {
	case tail.updated <- struct{}{}:
	default:
	}
	return tail, nil
}
