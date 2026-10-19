//go:build lantern_source_qualification && linux

package security

import (
	"os"
	"testing"
	"time"
)

// Explicit source integration, never part of ordinary unit or functional loops.
func TestAuthorityTimeLinuxConfiguredUpstream(t *testing.T) {
	if os.Getenv("LANTERN_TEST_CONFIGURED_TIME_UPSTREAM") != "1" {
		t.Skip("explicit bounded native campaign only")
	}
	o, err := newNativeAuthorityTimeOwner()
	if err != nil {
		t.Fatal(err)
	}
	defer o.close()
	deadline := time.Now().Add(45 * time.Second)
	for {
		if now, err := o.current(); err == nil {
			o.mu.Lock()
			m := *o.lastObservation
			o.mu.Unlock()
			t.Logf("Linux configured profile=%x host=%s configuration=%x endpoint=%s sequence=%d boot=%x process=%x sent=%d received=%d current=%d UTC=[%d,%d] request=%x response=%x; conditional source/path/rate premises", now.profile, m.source.host, m.source.configuration, m.endpoint, m.sequence, now.stamp.boot, now.stamp.process, m.sent.nanos, m.received.nanos, now.stamp.nanos, now.utc.low, now.utc.high, m.request, m.response)
			return
		}
		if time.Now().After(deadline) {
			o.mu.Lock()
			cause := o.lastError
			o.mu.Unlock()
			t.Fatal("bounded Linux source startup unavailable", cause)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
