package security

import "context"

// Private observation only. It says when old-cut NEW authorizations have
// ceased under this fixed profile. It never means that all physical bytes have
// arrived, and is not wired into the old public globally_enforced status.
type authorityAppliedWait struct {
	owner   *authorityOriginOwner
	started authorityCurrentTime
	outcome *OriginalOutcome
}

func (o *authorityOriginOwner) observeApplied(ctx context.Context, id FullChangeID, operation OperationIdentity) (*authorityAppliedWait, error) {
	if o == nil || !o.network.enterCall() {
		return nil, errS3AClosed
	}
	defer o.network.calls.Done()
	k := o.network.kernel
	k.gate.Lock()
	defer k.gate.Unlock()
	defer k.poisonPanic()
	if ctx.Err() != nil || k.readyLocked() != nil || o.closed {
		return nil, ErrAuthorityUnavailable
	}
	outcome := k.replayState.ledger[id]
	if outcome == nil || outcome.operation != operation || outcome.disposition != S1Applied {
		return nil, ErrPermissionDenied
	}
	// The installed certified contiguous Apply already happened. Starting
	// later only makes this observer's wait more conservative.
	now, err := o.network.timeOwner.current()
	if err != nil {
		return nil, err
	}
	return &authorityAppliedWait{o, now, outcome}, nil
}
func (w *authorityAppliedWait) complete(ctx context.Context) (bool, error) {
	if w == nil || w.owner == nil || w.outcome == nil || !w.owner.network.enterCall() {
		return false, errS3AClosed
	}
	defer w.owner.network.calls.Done()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	now, err := w.owner.network.timeOwner.current()
	if err != nil {
		return false, err
	}
	elapsed, err := w.owner.network.timeOwner.elapsed(w.started, now)
	return err == nil && elapsed.low >= authorityRenewalLifetime, err
}
