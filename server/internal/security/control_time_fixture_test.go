//go:build lantern_test_time && (darwin || linux)

package security

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthorityTimeFixtureLifecycle(t *testing.T) {
	o, fixture, err := newFixtureAuthorityTimeOwner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.close)
	deadline := time.Now().Add(45 * time.Second)
	var initial authorityCurrentTime
	for {
		initial, err = o.current()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture initial anchor", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if o.producer.source.host != "fixture.invalid" || initial.sequence == 0 {
		t.Fatal("fixture source/anchor provenance")
	}
	o.close()
	select {
	case <-fixture.done:
	default:
		t.Fatal("responder not joined")
	}
	if _, err = o.current(); err == nil {
		t.Fatal("closed owner remained current")
	}
	if _, err = fixture.socket.WriteToUDP([]byte{1}, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}); err == nil {
		t.Fatal("responder socket remained open")
	}
}

// Controlled source integration: real UDP loss/fresh recovery with injected
// native advancement. No external source and no minute-long expiry sleep.
func TestAuthorityTimeFixtureLossRecovery(t *testing.T) {
	if os.Getenv("LANTERN_CURRENT_PUBLIC_GATE") != "1" {
		t.Skip("explicit controlled mechanism gate")
	}
	var ticks atomic.Uint64
	ticks.Store(100_000_000)
	sample := func() (authorityTimeStamp, error) { return fakeAuthorityTimeStamp(ticks.Load()), nil }
	o, fixture, err := newFixtureAuthorityTimeOwnerFrom(sample, time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	defer o.close()
	deadline := time.Now().Add(time.Second)
	var initial authorityCurrentTime
	for {
		initial, err = o.current()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial fixture", err)
		}
		time.Sleep(time.Millisecond)
	}
	fixture.drop.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	_, err = o.producer.exchange(ctx, fixture.socket.LocalAddr().String())
	cancel()
	if err == nil {
		t.Fatal("dropped UDP accepted")
	}
	ticks.Add(uint64(time.Hour))
	current, err := o.current()
	if err != nil || current.sequence != initial.sequence || current.utc.high-current.utc.low < uint64(7*time.Second) {
		t.Fatal("sound holdover lost", err)
	}
	fixture.drop.Store(false)
	m, err := o.producer.exchange(t.Context(), fixture.socket.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	o.mu.Lock()
	err = o.installLocked(m)
	o.mu.Unlock()
	if err != nil {
		t.Fatal("fresh recovery", err)
	}
	recovered, err := o.current()
	if err != nil || recovered.sequence <= initial.sequence || recovered.stamp.process != initial.stamp.process {
		t.Fatal("fresh same-owner recovery", err)
	}
	o.close()
	select {
	case <-fixture.done:
	default:
		t.Fatal("responder not joined")
	}
	t.Log("controlled UDP loss retained sound one-hour holdover; fresh evidence tightened bounds; responder joined")
}
