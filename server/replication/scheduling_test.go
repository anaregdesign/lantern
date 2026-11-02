package replication

import (
	"testing"
	"time"
)

func TestSchedulingObservesActualConstructorValues(t *testing.T) {
	var missingPump *Pump
	if _, ok := missingPump.Scheduling(); ok {
		t.Fatal("nil pump reported an installed schedule")
	}
	var missingAnti *AntiEntropy
	if _, ok := missingAnti.Scheduling(); ok {
		t.Fatal("nil anti-entropy reported an installed schedule")
	}
	pump := NewPump(Config{SearchConfigFingerprint: "engine"}, nil, nil)
	anti := NewAntiEntropy(AntiEntropyConfig{Pump: pump, Interval: -time.Second, SearchConfigFingerprint: "engine"}, nil, nil, nil)
	p, _ := pump.Scheduling()
	a, _ := anti.Scheduling()
	if p.BackoffMin != 250*time.Millisecond || p.BackoffMax != 30*time.Second || p.SearchFingerprint != "engine" ||
		a.Interval != 30*time.Second || a.SubscribeTimeout != 30*time.Second || a.Pump != pump || a.SearchFingerprint != "engine" {
		t.Fatalf("constructor-resolved schedules = %+v / %+v", p, a)
	}
	p.BackoffMin = time.Hour
	a.SubscribeTimeout = time.Hour
	actualPump, _ := pump.Scheduling()
	actualAnti, _ := anti.Scheduling()
	if actualPump.BackoffMin != 250*time.Millisecond || actualAnti.SubscribeTimeout != 30*time.Second {
		t.Fatal("observation mutated installed config")
	}

	disabled := NewAntiEntropy(AntiEntropyConfig{Interval: 0}, nil, nil, nil)
	d, _ := disabled.Scheduling()
	if d.Interval != 0 || d.GapWarnThreshold != 0 {
		t.Fatal("disabled interval or warning threshold was normalized away")
	}
}
