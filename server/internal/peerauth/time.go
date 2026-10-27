package peerauth

import "time"

// TimeObservation is a process-local observation supplied by trusted
// composition. Its implementation owns the native stamp and elapsed proof;
// peerauth never derives qualified UTC from its scheduling clock.
type TimeObservation interface {
	Bounds() (low, high time.Time)
	ElapsedSince(TimeObservation) (time.Duration, error)
}

// At most entry, TLS handshake and response admission contribute limits.
// Fixed storage bounds the intersection without per-request proof allocations.
type rpcLimit struct {
	original TimeObservation
	lifetime time.Duration
}
type rpcBudget struct {
	limits   [3]rpcLimit
	count    int
	deadline time.Time // interruption only; fixed before setup can pause
}

func newRPCBudget(start time.Time, original TimeObservation, high, expiry time.Time) (rpcBudget, error) {
	lifetime := min(MaxRPCLifetime, expiry.Sub(high))
	if lifetime <= 0 {
		return rpcBudget{}, ErrMembership
	}
	return rpcBudget{limits: [3]rpcLimit{{original, lifetime}}, count: 1, deadline: start.Add(lifetime)}, nil
}

func (b rpcBudget) remaining(current TimeObservation, high, expiry time.Time) (time.Duration, error) {
	diagnostics, err := b.remainingWithDiagnostics(current, high, expiry)
	return diagnostics.OriginalBudgetRemaining, err
}

// Keep diagnostics in the authoritative pass: another elapsed-proof query or
// scheduling sample could describe a different boundary than the returned error.
func (b rpcBudget) remainingWithDiagnostics(current TimeObservation, high, expiry time.Time) (AdmissionDiagnostics, error) {
	deadlineRemaining, expiryRemaining := time.Until(b.deadline), expiry.Sub(high)
	remaining := min(deadlineRemaining, expiryRemaining)
	if b.count < 1 || b.count > len(b.limits) {
		return AdmissionDiagnostics{Reason: "budget_invalid"}, ErrMembership
	}
	for _, limit := range b.limits[:b.count] {
		if limit.original == nil {
			continue
		}
		if current == nil {
			return AdmissionDiagnostics{Reason: "native_sample_missing"}, ErrMembership
		}
		elapsed, err := current.ElapsedSince(limit.original)
		if err != nil || elapsed < 0 {
			return AdmissionDiagnostics{Reason: "native_elapsed_invalid"}, ErrMembership
		}
		if elapsed >= limit.lifetime {
			return AdmissionDiagnostics{Reason: "native_lifetime_expired", RemainingKnown: true}, ErrMembership
		}
		remaining = min(remaining, limit.lifetime-elapsed)
	}

	if remaining <= 0 {
		reason := "absolute_expiry"
		if deadlineRemaining <= 0 {
			reason = "local_deadline_expired"
		}
		return AdmissionDiagnostics{Reason: reason, RemainingKnown: true}, ErrMembership
	}
	return AdmissionDiagnostics{Reason: "none", OriginalBudgetRemaining: remaining, RemainingKnown: true}, nil
}

func (b rpcBudget) intersect(prior rpcBudget) (rpcBudget, error) {
	if b.count < 1 || prior.count < 1 || b.count > len(b.limits) || prior.count > len(b.limits) || b.count+prior.count > len(b.limits) {
		return rpcBudget{}, ErrMembership
	}
	copy(b.limits[b.count:], prior.limits[:prior.count])
	b.count += prior.count
	if prior.deadline.Before(b.deadline) {
		b.deadline = prior.deadline
	}
	return b, nil
}
