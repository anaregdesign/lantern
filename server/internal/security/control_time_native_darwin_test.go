package security

import (
	"context"
	"crypto/sha256"
	"os"
	"testing"
)

func TestAuthorityTimeDarwinNativeSampler(t *testing.T) {
	sample, err := newDarwinAuthorityTimeSampler()
	if err != nil {
		t.Fatal(err)
	}
	a, err := sample()
	if err != nil {
		t.Fatal(err)
	}
	b, err := sample()
	if err != nil {
		t.Fatal(err)
	}
	if a.boot == [32]byte{} || a.process == [16]byte{} || a.boot != b.boot || a.process != b.process || a.nanos == 0 || b.nanos < a.nanos {
		t.Fatalf("native stamps: %+v %+v", a, b)
	}
	another, err := newDarwinAuthorityTimeSampler()
	if err != nil {
		t.Fatal(err)
	}
	c, err := another()
	if err != nil {
		t.Fatal(err)
	}
	if c.boot != a.boot || c.process == a.process {
		t.Fatal("new owner did not bind fresh process epoch")
	}
}

// Explicit opt-in: one read-only exchange with the already configured source.
// Normal unit tests never contact a public NTP service or modify OS time.
func TestAuthorityTimeDarwinConfiguredUpstream(t *testing.T) {
	if os.Getenv("LANTERN_TEST_CONFIGURED_TIME_UPSTREAM") != "1" {
		t.Skip("explicit native compatibility campaign only")
	}
	p, err := newNativeAuthorityTimeProducer()
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	m, err := p.measure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	o := &authorityTimeOwner{producer: p, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription))}
	if err = o.installLocked(m); err != nil {
		t.Fatal(err)
	}
	now, err := o.current()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("conditional profile=%x configured=%s config=%x endpoint=%s sequence=%d boot=%x process=%x send=%d receive=%d current=%d UTC=[%d,%d] request=%x response=%x; premises=honest-source-error,intact-DNS-UDP-path,1000ppm-counter; authenticated-NTP=false", o.profile, m.source.host, m.source.configuration, m.endpoint, m.sequence, m.received.boot, m.received.process, m.sent.nanos, m.received.nanos, now.stamp.nanos, now.utc.low, now.utc.high, m.request, m.response)
}
