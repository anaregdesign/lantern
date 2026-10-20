package peerauth

import (
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
