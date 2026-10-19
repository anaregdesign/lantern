package security

import (
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
