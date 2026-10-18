package security

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
	diagnostic := struct {
		Version int    `json:"version"`
		Factory string `json:"factory"`
		Source  string `json:"source"`
		Stage   string `json:"stage"`
		Reason  string `json:"reason"`
		Attempt uint64 `json:"attempt"`
		Install string `json:"install"`
		Current string `json:"current"`
		Verdict string `json:"verdict"`
	}{Version: 1, Factory: "failed", Source: "not_checked", Stage: "not_run", Reason: "not_run", Install: "not_run", Current: "not_run", Verdict: "fail"}
	defer func() {
		raw, err := json.Marshal(diagnostic)
		if err != nil {
			t.Fatal("diagnostic encoding failed")
		}
		t.Logf("configured_time_diagnostic:%s", raw)
	}()
	p, err := newNativeAuthorityTimeProducer()
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	diagnostic.Factory = "ready"
	if p.source.host != "time.asia.apple.com" {
		diagnostic.Source = "mismatch"
		t.Fatal("unexpected configured source")
	}
	diagnostic.Source = "expected"
	m, err := p.measure(context.Background())
	probe := p.probeObservation()
	diagnostic.Stage, diagnostic.Reason, diagnostic.Attempt = probe.Stage, probe.Reason, probe.Attempt
	if err != nil {
		t.Fatal(err)
	}
	o := &authorityTimeOwner{producer: p, premises: authorityOperationalTimePremises(), profile: sha256.Sum256([]byte(authorityTimeProfileDescription))}
	err = o.installLocked(m)
	diagnostic.Install = o.installReason
	if err != nil {
		t.Fatal(err)
	}
	_, observation, err := o.currentObserved()
	diagnostic.Current = observation.Reason
	if err != nil {
		t.Fatal(err)
	}
	diagnostic.Verdict = "pass"
}
