package provider

import (
	"runtime"
	"testing"
)

// resetProfilingRates returns the runtime to "off" so tests don't leak
// global state into siblings. SetMutexProfileFraction(0) returns the
// previous value, which is how we also assert what ApplyRuntimeProfiling
// installed.
func resetProfilingRates(t *testing.T) {
	t.Helper()
	runtime.SetMutexProfileFraction(0)
	runtime.SetBlockProfileRate(0)
}

func TestApplyRuntimeProfiling_DefaultsAreNoOp(t *testing.T) {
	resetProfilingRates(t)
	t.Cleanup(func() { resetProfilingRates(t) })

	// Seed a non-zero value so we can prove Apply with zeros does NOT
	// clobber it. (Realistic foot-gun: a process that already set the
	// rates via runtime/debug should not be silently overwritten.)
	runtime.SetMutexProfileFraction(7)

	ApplyRuntimeProfiling(ObservabilityConfig{}, nil)

	got := runtime.SetMutexProfileFraction(0)
	if got != 7 {
		t.Fatalf("Apply with zero fraction overwrote pre-set value: got %d, want 7", got)
	}
}

func TestApplyRuntimeProfiling_AppliesMutexFraction(t *testing.T) {
	resetProfilingRates(t)
	t.Cleanup(func() { resetProfilingRates(t) })

	ApplyRuntimeProfiling(ObservabilityConfig{MutexProfileFraction: 23}, nil)

	prev := runtime.SetMutexProfileFraction(0)
	if prev != 23 {
		t.Fatalf("mutex fraction not installed: got %d, want 23", prev)
	}
}

func TestApplyRuntimeProfiling_AppliesBlockRate(t *testing.T) {
	resetProfilingRates(t)
	t.Cleanup(func() { resetProfilingRates(t) })

	// runtime exposes no getter for the block profile rate, so we can
	// only assert the call path does not panic and that pairing with a
	// mutex fraction still works end-to-end.
	ApplyRuntimeProfiling(ObservabilityConfig{
		MutexProfileFraction: 11,
		BlockProfileRate:     1000,
	}, nil)

	prev := runtime.SetMutexProfileFraction(0)
	if prev != 11 {
		t.Fatalf("mutex fraction not installed alongside block rate: got %d, want 11", prev)
	}
}
