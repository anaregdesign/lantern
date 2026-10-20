package security

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

func TestWorkloadTimeFinalCountersSurviveCloseWithoutSampling(t *testing.T) {
	clock, _ := fakeAuthorityTimeOwner(t)
	clock.producer.attempts, clock.producer.writes = 3, 2
	var samples atomic.Uint64
	clock.producer.sample = func() (authorityTimeStamp, error) {
		samples.Add(1)
		return authorityTimeStamp{}, errAuthorityTimeNative
	}
	clock.cancel = func() {}
	clock.done = make(chan struct{})
	close(clock.done)
	view := &WorkloadTime{clock: clock}
	before := view.TimeAcquisitionCounts()
	if before.Attempts != 3 || before.Writes != 2 || before.Installations == 0 {
		t.Fatal("missing nonzero producer evidence", before)
	}
	if err := view.Close(); err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 16 {
				if got := view.TimeAcquisitionCounts(); got != before {
					t.Error("closed counter snapshot changed", got, before)
				}
			}
		})
	}
	readers.Wait()
	if samples.Load() != 0 {
		t.Fatal("diagnostics sampled native time")
	}
	if _, err := view.Observe(); err == nil || samples.Load() != 0 {
		t.Fatal("closed diagnostic access revived serving capability", err)
	}
	borrower := (&CurrentAuthority{origin: &authorityOriginOwner{network: &s3aOwner{timeOwner: clock}}}).WorkloadTime()
	if err := borrower.Close(); err != nil || borrower.TimeAcquisitionCounts() != before {
		t.Fatal("closed borrower lost passive counters", err)
	}
	if _, err := borrower.Observe(); err == nil {
		t.Fatal("closed borrower retained capability")
	}
	for _, empty := range []*WorkloadTime{nil, {}, {authority: &CurrentAuthority{}},
		{authority: &CurrentAuthority{origin: &authorityOriginOwner{}}},
		{authority: &CurrentAuthority{origin: &authorityOriginOwner{network: &s3aOwner{}}}}} {
		if got := empty.TimeAcquisitionCounts(); got.Attempts != 0 || got.Writes != 0 || got.Installations != 0 {
			t.Fatal("unconstructed view invented evidence", got)
		}
	}
}

func TestWorkloadTimeCountersCannotHideAnUnjoinedExchange(t *testing.T) {
	clock, _ := fakeAuthorityTimeOwner(t)
	clock.producer.attempts, clock.producer.writes, clock.producer.busy = 2, 1, true
	clock.producer.workers.Add(1)
	closing := make(chan struct{})
	clock.cancel = func() { close(closing) }
	clock.done = make(chan struct{})
	view := &WorkloadTime{clock: clock}
	returned := make(chan struct{})
	go func() { _ = view.Close(); close(returned) }()
	<-closing
	if counts := view.TimeAcquisitionCounts(); !counts.InFlight || counts.Attempts != 2 {
		t.Error("retiring view hid an active acquisition", counts)
	}
	if _, err := view.Observe(); err == nil {
		t.Error("retiring view still granted time")
	}
	select {
	case <-returned:
		t.Error("Close returned before the exchange and owner joined")
	default:
	}
	clock.producer.mu.Lock()
	clock.producer.busy = false
	clock.producer.mu.Unlock()
	clock.producer.workers.Done()
	close(clock.done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("owned close did not join")
	}
	counts := view.TimeAcquisitionCounts()
	if counts.InFlight || counts != view.TimeAcquisitionCounts() {
		t.Fatal("joined diagnostic counters unstable", counts)
	}
}
