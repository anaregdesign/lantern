package query

import "testing"

func TestEngineMetadataKeepsZeroServiceConfiguration(t *testing.T) {
	// The public service fingerprint previously supported a zero-value service.
	// An unconfigured executor must keep that exact zero-settings contract.
	var engine *Engine
	if got := engine.Limits(); got != (Limits{}) {
		t.Fatalf("unconfigured limits = %+v, want zero settings", got)
	}
	if got := engine.ConfigFingerprint(); got != ConfigFingerprint(Limits{}) {
		t.Fatalf("unconfigured fingerprint = %q", got)
	}
	configured := New(Limits{Enabled: true, MaxInFlight: 1})
	if configured.ConfigFingerprint() != ConfigFingerprint(configured.Limits()) {
		t.Fatal("executor metadata disagrees with its normalized configuration")
	}
}
