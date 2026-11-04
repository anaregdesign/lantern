package provider

import (
	"testing"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/dataruntime"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

// searchPeerTestFixture is shared by the paired Pump and anti-entropy provider
// tests. It constructs real data resources without starting peer workers.
type searchPeerTestFixture struct {
	manager     *ManagedOperationalRuntime
	primary     *dataruntime.Executor
	cache       *graphcache.GraphCache[string, *pb.Vertex]
	certified   runtimeCertified
	search      managedSearchCertified
	bootstrapFP string
	recoveredFP string
}

func newSearchPeerTestFixture(t *testing.T, recovered bool) searchPeerTestFixture {
	t.Helper()
	data, primary := managedSearchTestPrimary(t)
	peer, err := data.NewReplication(primary)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRuntimeRestored(data, primary)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := NewRuntimeCertified(data, primary, peer, restored, NetConfig{})
	if err != nil {
		t.Fatal(err)
	}
	f := searchPeerTestFixture{primary: primary, cache: data.GraphCache(), certified: certified, bootstrapFP: primary.SearchConfigFingerprint()}
	if recovered {
		policy := managedSearchTestPolicy(t, primary, 7)
		projection := operationalRuntimeTestPrepare(operationalRuntimeTestProjection(3), operationalpolicy.GroupSearch)
		if err := projection.Policies.Set(operationalpolicy.GroupSearch, policy); err != nil {
			t.Fatal(err)
		}
		f.manager, _ = operationalRuntimeTestManager(t, projection)
		f.manager.bootstrap = managedSearchBootstrap()
		f.manager.runtime.data = data
		f.manager.runtime.managedOperational = f.manager
		f.recoveredFP = policy.CompatibilityFingerprint
	} else {
		f.manager, err = NewManagedOperationalRuntime(t.Context(), &SecurityRuntime{mode: "off", data: data}, managedSearchBootstrap())
		if err != nil {
			t.Fatal(err)
		}
	}
	f.search, err = NewManagedSearchRuntime(t.Context(), f.manager, primary)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f searchPeerTestFixture) pump(t *testing.T) searchPumpCertified {
	t.Helper()
	result, err := NewSearchCertifiedReplicationPump(PeerConfig{}, nil, ReplicationConfig{}, nil, f.primary, f.cache, nil, nil, nil, f.certified, f.manager, f.search)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSearchCertifiedPumpUsesRecoveredFingerprintAndKeepsPendingInstallation(t *testing.T) {
	f := newSearchPeerTestFixture(t, true)
	if f.recoveredFP == "" || f.recoveredFP == f.bootstrapFP || f.primary.SearchConfigFingerprint() != f.recoveredFP {
		t.Fatal("fixture did not restore a distinct Search fingerprint")
	}
	witness := f.pump(t)
	pump, err := NewSearchReplicationPump(witness)
	if err != nil || pump != witness.pump || pump.SearchConfigFingerprint() != f.recoveredFP {
		t.Fatal("Pump captured pre-restoration Search policy", err)
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
	selected, err := NewSearchReplicationPump(witness)
	if err != nil || selected != pump || !f.search.runtime.owner.Status().PendingRestart ||
		pump.SearchConfigFingerprint() != f.recoveredFP || f.primary.SearchConfigFingerprint() != f.recoveredFP {
		t.Fatal("desired Search change replaced an installed worker", err)
	}
	if err := f.search.runtime.owner.Restart(f.search.runtime.owner.Status().Desired); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSearchReplicationPump(witness); err == nil {
		t.Fatal("stale Search installation retained its worker selector")
	}
	if _, err := NewSearchCertifiedReplicationPump(PeerConfig{}, nil, ReplicationConfig{}, nil, f.primary, f.cache, nil, nil, nil, f.certified, f.manager, f.search); err == nil {
		t.Fatal("stale Search installation constructed another worker")
	}
}

func TestSearchCertifiedPumpRefusesForeignComposition(t *testing.T) {
	f := newSearchPeerTestFixture(t, false)
	foreign := newSearchPeerTestFixture(t, false)
	if foreign.primary.SearchConfigFingerprint() != f.primary.SearchConfigFingerprint() {
		t.Fatal("fixture must exercise equal-valued foreign resources")
	}
	for name, mutate := range map[string]func(*searchPeerTestFixture){
		"missing manager": func(f *searchPeerTestFixture) { f.manager = nil },
		"foreign manager": func(f *searchPeerTestFixture) { f.manager = foreign.manager },
		"missing primary": func(f *searchPeerTestFixture) { f.primary = nil },
		"foreign primary": func(f *searchPeerTestFixture) { f.primary = foreign.primary },
		"missing graph": func(f *searchPeerTestFixture) { f.cache = nil },
		"foreign graph": func(f *searchPeerTestFixture) { f.cache = foreign.cache },
		"invalid runtime certificate": func(f *searchPeerTestFixture) { f.certified.valid = false },
		"foreign runtime certificate": func(f *searchPeerTestFixture) { f.certified = foreign.certified },
		"missing Search": func(f *searchPeerTestFixture) { f.search = managedSearchCertified{} },
		"foreign Search": func(f *searchPeerTestFixture) { f.search = foreign.search },
	} {
		t.Run(name, func(t *testing.T) {
			wrong := f
			mutate(&wrong)
			if _, err := NewSearchCertifiedReplicationPump(PeerConfig{}, nil, ReplicationConfig{}, nil, wrong.primary, wrong.cache, nil, nil, nil, wrong.certified, wrong.manager, wrong.search); err == nil {
				t.Fatal("foreign Search worker composition accepted")
			}
		})
	}
	if _, err := NewSearchReplicationPump(searchPumpCertified{}); err == nil {
		t.Fatal("empty Pump witness accepted")
	}
	witness := f.pump(t)
	witness.fingerprint = foreign.primary.SearchConfigFingerprint() + "changed"
	if _, err := NewSearchReplicationPump(witness); err == nil {
		t.Fatal("fingerprint contradiction accepted")
	}
}
