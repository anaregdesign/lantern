package security

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWorkloadTimeNativeObservationAndBorrowLifetime(t *testing.T) {
	clock, ticks := fakeAuthorityTimeOwner(t)
	view := &WorkloadTime{clock: clock}
	first, err := view.Observe()
	if err != nil {
		t.Fatal(err)
	}
	counts := view.TimeAcquisitionCounts()
	for range 5 {
		if _, err = view.Observe(); err != nil {
			t.Fatal(err)
		}
	}
	if view.TimeAcquisitionCounts() != counts {
		t.Fatal("local observations acquired source time")
	}
	ticks.Add(uint64(time.Second))
	second, err := view.Observe()
	if err != nil {
		t.Fatal(err)
	}
	elapsed, err := second.ElapsedSince(first)
	if err != nil || elapsed < time.Second {
		t.Fatal("native elapsed was not conservative", elapsed, err)
	}
	_, oldHigh := second.Bounds()
	clock.mu.Lock()
	clock.anchor.utc.high -= uint64(5 * time.Millisecond)
	clock.sequence++
	clock.mu.Unlock()
	narrow, err := view.Observe()
	if err != nil {
		t.Fatal(err)
	}
	_, newHigh := narrow.Bounds()
	if !newHigh.Before(oldHigh) {
		t.Fatal("fixture did not tighten high")
	}
	if _, err = narrow.ElapsedSince(first); err != nil {
		t.Fatal("anchor sequence change broke native elapsed", err)
	}
	foreign, err := (&WorkloadTime{clock: clock}).Observe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = foreign.ElapsedSince(first); err == nil {
		t.Fatal("different view reused original capability")
	}
	owner := &s3aOwner{timeOwner: clock}
	authority := &CurrentAuthority{origin: &authorityOriginOwner{network: owner}}
	borrowed := authority.WorkloadTime()
	if _, err = borrowed.Observe(); err != nil {
		t.Fatal(err)
	}
	if err = borrowed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = borrowed.Observe(); err == nil {
		t.Fatal("retired view usable")
	}
	if _, err = view.Observe(); err != nil || clock.closed {
		t.Fatal("borrower closed authority time", err)
	}
	live := authority.WorkloadTime()
	owner.closed.Store(true)
	if _, err = live.Observe(); err == nil {
		t.Fatal("view outlived authority")
	}
	clock.mu.Lock()
	clock.failed = true
	clock.anchor = nil
	clock.mu.Unlock()
	if _, err = view.Observe(); err == nil {
		t.Fatal("native terminal fault ignored")
	}
}

func TestWorkloadTimeOwnedCloseAndCanceledConstruction(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if owner, err := OpenWorkloadTime(ctx); owner != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled construction acquired owner", err)
	}
	clock, _ := fakeAuthorityTimeOwner(t)
	clock.cancel = func() {}
	clock.done = make(chan struct{})
	close(clock.done)
	view := &WorkloadTime{clock: clock}
	for range 2 {
		if err := view.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if !clock.closed {
		t.Fatal("OFF did not close owned native time")
	}
	if _, err := view.Observe(); err == nil {
		t.Fatal("closed owner observed time")
	}
}

func TestNativeWorkloadTimeTargetPreservesOtherOFFProfiles(t *testing.T) {
	for _, row := range []struct {
		os, arch  string
		qualified bool
	}{
		{"linux", "amd64", true}, {"linux", "arm64", true}, {"darwin", "arm64", true},
		{"darwin", "amd64", false}, {"windows", "amd64", false}, {"windows", "arm64", false}, {"linux", "386", false},
	} {
		if got := nativeWorkloadTimeTarget(row.os, row.arch); got != row.qualified {
			t.Fatal(row, got)
		}
	}
}
