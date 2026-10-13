//go:build lantern_test_time && (darwin || linux)

package security

import (
	"net"
	"os"
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

// A finite real-native integration: unchanged exchange/backoff/holdover bounds,
// actual UDP loss, expired authority, then a fresh same-owner anchor. This is
// synthetic source evidence, never an assertion about the configured source.
func TestAuthorityTimeFixtureLossRecovery(t *testing.T) {
	if os.Getenv("LANTERN_CURRENT_PUBLIC_GATE") != "1" {
		t.Skip("explicit finite mechanism gate")
	}
	o, fixture, err := newFixtureAuthorityTimeOwner()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.close)
	waitCurrent := func(limit time.Duration) authorityCurrentTime {
		t.Helper()
		deadline := time.Now().Add(limit)
		for {
			current, err := o.current()
			if err == nil {
				return current
			}
			if time.Now().After(deadline) {
				t.Fatal("native fixture failed to acquire", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	initial := waitCurrent(45 * time.Second)
	fixture.drop.Store(true)
	deadline := time.Now().Add(65 * time.Second)
	for {
		stamp, err := o.producer.sample()
		if err != nil {
			t.Fatal(err)
		}
		age, err := o.premises.elapsed(initial.stamp, stamp)
		if err != nil {
			t.Fatal(err)
		}
		if age.low >= o.premises.maxAge {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native age did not advance")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := o.current(); err == nil {
		t.Fatal("lost source extended holdover")
	}
	o.mu.Lock()
	sequence, lastError := o.sequence, o.lastError
	o.mu.Unlock()
	if sequence != initial.sequence || lastError == nil {
		t.Fatal("loss replaced anchor or no UDP failure observed")
	}
	fixture.drop.Store(false)
	recovered := waitCurrent(75 * time.Second)
	if recovered.sequence <= initial.sequence || recovered.stamp.boot != initial.stamp.boot || recovered.stamp.process != initial.stamp.process {
		t.Fatal("recovery did not use fresh same-epoch evidence")
	}
	o.close()
	select {
	case <-fixture.done:
	default:
		t.Fatal("responder not joined after recovery")
	}
	newOwner, _, err := newFixtureAuthorityTimeOwner()
	if err != nil {
		t.Fatal(err)
	}
	defer newOwner.close()
	stamp, err := newOwner.producer.sample()
	if err != nil || stamp.process == recovered.stamp.process {
		t.Fatal("new owner reused native process identity", err)
	}
	t.Log("controlled source loss: expired authority refused; fresh same-owner recovery; responder joined; restart has fresh native identity")
}
