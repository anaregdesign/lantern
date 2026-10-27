package peerauth

import (
	"errors"
	"testing"
	"time"
)

func TestRPCBudgetCannotBeRenewedByUTCNarrowingOrSetup(t *testing.T) {
	base := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	owner := new(byte)
	first := peerTestObservation{owner, base, base.Add(2 * time.Second), 0}
	budget, err := newRPCBudget(time.Now().Add(-7*time.Second), first, first.high, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	current := peerTestObservation{owner, base, base, 5 * time.Second}
	remaining, err := budget.remaining(current, current.high, base.Add(time.Hour))
	if err != nil || remaining > 23*time.Second || remaining <= 22*time.Second {
		t.Fatal("setup pause renewed the entry deadline", remaining, err)
	}
	current.elapsed = 29 * time.Second
	remaining, err = budget.remaining(current, current.high, base.Add(time.Hour))
	if err != nil || remaining > time.Second {
		t.Fatal("UTC narrowing renewed native budget", remaining, err)
	}
	current.elapsed = 30 * time.Second
	if _, err = budget.remaining(current, current.high, base.Add(time.Hour)); err == nil {
		t.Fatal("exact native expiry admitted")
	}
	current.elapsed = time.Second
	current.owner = new(byte)
	if _, err = budget.remaining(current, current.high, base.Add(time.Hour)); err == nil {
		t.Fatal("cross-owner elapsed admitted")
	}
	current.owner = owner
	if _, err = budget.remaining(current, base.Add(time.Hour), base.Add(time.Hour)); err == nil {
		t.Fatal("absolute expiry equality admitted")
	}
	budget.deadline = time.Now().Add(-time.Nanosecond)
	if _, err = budget.remaining(first, first.high, base.Add(time.Hour)); err == nil {
		t.Fatal("interruption deadline extended")
	}
}

func TestRPCBudgetIntersectionIsBoundedAndNeverDropsAnEarlierLimit(t *testing.T) {
	base := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	owner := new(byte)
	start := peerTestObservation{owner, base, base.Add(2 * time.Second), 0}
	entry, err := newRPCBudget(time.Now(), start, start.high, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	handshakeSample := peerTestObservation{owner, base, base.Add(10 * time.Second), 5 * time.Second}
	handshake, err := newRPCBudget(time.Now(), handshakeSample, handshakeSample.high, base.Add(12*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	combined, err := handshake.intersect(entry)
	if err != nil {
		t.Fatal(err)
	}
	current := peerTestObservation{owner, base, base.Add(11 * time.Second), 7 * time.Second}
	if _, err = combined.remaining(current, current.high, base.Add(12*time.Second)); err == nil {
		t.Fatal("shorter handshake budget lost at equality")
	}
	response, err := entry.intersect(combined)
	if err != nil || response.count != 3 {
		t.Fatal("bounded response intersection", err)
	}
	if _, err = response.intersect(entry); err == nil {
		t.Fatal("unbounded proof chain admitted")
	}
}

type rpcBudgetDiagnosticObservation struct {
	peerTestObservation
	calls   *int
	elapsed time.Duration
	err     error
}

func (o rpcBudgetDiagnosticObservation) ElapsedSince(TimeObservation) (time.Duration, error) {
	*o.calls++
	return o.elapsed, o.err
}

func TestRPCBudgetDiagnosticsUseEachOriginalProofOnce(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		known        bool
		calls        int
	}{
		{"empty budget", "budget_invalid", false, 0},
		{"oversize budget", "budget_invalid", false, 0},
		{"missing sample", "native_sample_missing", false, 0},
		{"elapsed failure", "native_elapsed_invalid", false, 1},
		{"negative elapsed", "native_elapsed_invalid", false, 1},
		{"native expiry equality", "native_lifetime_expired", true, 1},
		{"interruption deadline", "local_deadline_expired", true, 1},
		{"absolute expiry equality", "absolute_expiry", true, 1},
		{"accepted", "none", true, 1},
		{"intersected earlier limit", "none", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
			original := peerTestObservation{new(byte), base, base, 0}
			budget, err := newRPCBudget(time.Now(), original, base, base.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			observation := rpcBudgetDiagnosticObservation{peerTestObservation: original, calls: &calls, elapsed: 7 * time.Second}
			var current TimeObservation = observation
			expiry := base.Add(time.Hour)
			maximumRemaining := 23 * time.Second
			wantErr := ErrMembership
			switch tc.name {
			case "empty budget":
				budget.count = 0
			case "oversize budget":
				budget.count = len(budget.limits) + 1
			case "missing sample":
				current = nil
			case "elapsed failure":
				observation.err = errors.New("private native proof error")
				current = observation
			case "negative elapsed":
				observation.elapsed = -time.Nanosecond
				current = observation
			case "native expiry equality":
				observation.elapsed = MaxRPCLifetime
				current = observation
			case "interruption deadline":
				budget.deadline = time.Now().Add(-time.Second)
			case "absolute expiry equality":
				expiry = base
			case "accepted":
				wantErr = nil
			case "intersected earlier limit":
				earlier := budget
				earlier.limits[0].lifetime = 12 * time.Second
				budget, err = budget.intersect(earlier)
				if err != nil {
					t.Fatal(err)
				}
				maximumRemaining, wantErr = 5*time.Second, nil
			}
			before := budget
			diagnostics, err := budget.remainingWithDiagnostics(current, base, expiry)
			if err != wantErr || diagnostics.Reason != tc.reason || diagnostics.RemainingKnown != tc.known || calls != tc.calls {
				t.Fatalf("diagnostics = (%+v, %v), proof calls=%d; want reason=%s known=%t error=%v calls=%d", diagnostics, err, calls, tc.reason, tc.known, wantErr, tc.calls)
			}
			if wantErr != nil && diagnostics.OriginalBudgetRemaining != 0 {
				t.Fatal("rejected budget reported usable time", diagnostics)
			}
			if wantErr == nil && (diagnostics.OriginalBudgetRemaining <= 0 || diagnostics.OriginalBudgetRemaining > maximumRemaining) {
				t.Fatal("diagnostics renewed an original limit", diagnostics)
			}
			calls = 0
			remaining, err := budget.remaining(current, base, expiry)
			if err != wantErr || calls != tc.calls || wantErr != nil && remaining != 0 {
				t.Fatalf("remaining error/proof calls changed: %v %d remaining=%v", err, calls, remaining)
			}
			if budget != before {
				t.Fatal("diagnostics changed the original budget")
			}
		})
	}
}
