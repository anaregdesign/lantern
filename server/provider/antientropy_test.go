package provider

import (
	"testing"

	"github.com/anaregdesign/lantern/server/replication"
)

func TestSearchCertifiedAntiEntropyUsesExactPumpAndEffectiveFingerprint(t *testing.T) {
	f := newSearchPeerTestFixture(t, true)
	witness := f.pump(t)
	pump, err := NewSearchReplicationPump(witness)
	if err != nil {
		t.Fatal(err)
	}
	next := managedSearchTestPolicy(t, f.primary, 8)
	next.Values.Search.MaxQueryBytes--
	next.CompatibilityFingerprint, err = f.search.runtime.PreviewOperationalPolicy(next.Values)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.search.runtime.ApplyOperationalPolicy(*next); err != nil {
		t.Fatal(err)
	}
	driver, err := NewSearchCertifiedAntiEntropyDriver(PeerConfig{}, nil, ReplicationConfig{}, AntiEntropyConfig{}, nil, f.primary, f.cache, pump, nil, nil, nil, f.certified, f.manager, f.search, witness)
	if err != nil || !driver.UsesPump(pump) || driver.SearchConfigFingerprint() != f.recoveredFP ||
		pump.SearchConfigFingerprint() != f.recoveredFP || !f.search.runtime.owner.Status().PendingRestart {
		t.Fatal("anti-entropy did not retain the actual effective Search/Pump pair", err)
	}
	if err := f.search.runtime.owner.Restart(f.search.runtime.owner.Status().Desired); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSearchCertifiedAntiEntropyDriver(PeerConfig{}, nil, ReplicationConfig{}, AntiEntropyConfig{}, nil, f.primary, f.cache, pump, nil, nil, nil, f.certified, f.manager, f.search, witness); err == nil {
		t.Fatal("stale Search installation constructed anti-entropy")
	}
}

func TestSearchCertifiedAntiEntropyRefusesEqualFingerprintForeignPump(t *testing.T) {
	f := newSearchPeerTestFixture(t, false)
	foreign := newSearchPeerTestFixture(t, false)
	witness, foreignWitness := f.pump(t), foreign.pump(t)
	pump, err := NewSearchReplicationPump(witness)
	if err != nil {
		t.Fatal(err)
	}
	foreignPump, err := NewSearchReplicationPump(foreignWitness)
	if err != nil {
		t.Fatal(err)
	}
	if pump.SearchConfigFingerprint() != foreignPump.SearchConfigFingerprint() {
		t.Fatal("fixture must exercise equal-fingerprint foreign Pump")
	}
	for name, tc := range map[string]struct {
		fixture searchPeerTestFixture
		pump *replication.Pump
		witness searchPumpCertified
	}{
		"missing Pump": {f, nil, witness},
		"foreign Pump": {f, foreignPump, witness},
		"missing witness": {f, pump, searchPumpCertified{}},
		"foreign witness": {f, pump, foreignWitness},
		"foreign graph": {searchPeerTestFixture{manager: f.manager, primary: f.primary, cache: foreign.cache, certified: f.certified, search: f.search}, pump, witness},
		"foreign Search": {searchPeerTestFixture{manager: f.manager, primary: f.primary, cache: f.cache, certified: f.certified, search: foreign.search}, pump, witness},
		"foreign certificate": {searchPeerTestFixture{manager: f.manager, primary: f.primary, cache: f.cache, certified: foreign.certified, search: f.search}, pump, witness},
	} {
		t.Run(name, func(t *testing.T) {
			v := tc.fixture
			if _, err := NewSearchCertifiedAntiEntropyDriver(PeerConfig{}, nil, ReplicationConfig{}, AntiEntropyConfig{}, nil, v.primary, v.cache, tc.pump, nil, nil, nil, v.certified, v.manager, v.search, tc.witness); err == nil {
				t.Fatal("foreign anti-entropy composition accepted")
			}
		})
	}
}
