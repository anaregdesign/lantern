package provider

import (
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"github.com/anaregdesign/lantern/server/replication"
)

func TestManagedReplicationRuntime(t *testing.T) {
	values := operationalpolicy.Replication{AntiEntropyGapWarnThreshold: 1024, AntiEntropyIntervalMS: 30_000, AntiEntropySubscribeTimeoutMS: 30_000, PumpBackoffMinMS: 250, PumpBackoffMaxMS: 30_000}
	policy := func(gen uint64, v operationalpolicy.Replication) *operationalpolicy.Policy {
		return &operationalpolicy.Policy{Version: operationalpolicy.Version, Generation: gen, Values: operationalpolicy.Values{Replication: &v}}
	}
	workers := func(v operationalpolicy.Replication) (*replication.Pump, *replication.AntiEntropy) {
		t.Helper()
		pc, ac, err := resolveReplicationPolicy(v, PeerConfig{}, AntiEntropyConfig{})
		if err != nil {
			t.Fatal(err)
		}
		fp := strings.Repeat("a", 64)
		pump := replication.NewPump(replication.Config{BackoffMin: pc.BackoffMin, BackoffMax: pc.BackoffMax, SearchConfigFingerprint: fp}, nil, nil)
		anti := replication.NewAntiEntropy(replication.AntiEntropyConfig{Pump: pump, Interval: ac.Interval, SubscribeTimeout: ac.SubscribeTimeout, GapWarnThreshold: ac.GapWarnThreshold, SearchConfigFingerprint: fp}, nil, nil, nil)
		return pump, anti
	}
	t.Run("stage is not application", func(t *testing.T) {
		pump, anti := workers(values)
		r, err := newReplicationPolicyRuntime(pump, anti, policy(2, values))
		if err != nil {
			t.Fatal(err)
		}
		next := values
		next.PumpBackoffMaxMS = 45_000
		if err := r.ApplyOperationalPolicy(*policy(3, next)); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := r.ApplyOperationalPolicy(*policy(3, next)); err != nil {
				t.Fatal(err)
			}
		}
		observation, err := r.observation()
		if err != nil || observation.AppliedGeneration != 2 || observation.DesiredGeneration != 3 || !observation.PendingRestart || *observation.Effective.Replication != values {
			t.Fatalf("observation = %+v, %v", observation, err)
		}
		// Returning to the boot values is a new desired generation, not a
		// claim that existing workers were constructed for that generation.
		if err := r.ApplyOperationalPolicy(*policy(4, values)); err != nil {
			t.Fatal(err)
		}
		observation, _ = r.observation()
		if observation.AppliedGeneration != 2 || observation.DesiredGeneration != 4 || !observation.PendingRestart {
			t.Fatal("rollback generation was falsely reported applied")
		}
		if err := r.ApplyOperationalPolicy(*policy(3, next)); err == nil {
			t.Fatal("stale callback accepted")
		}
		if err := r.ApplyOperationalPolicy(*policy(4, next)); err == nil {
			t.Fatal("conflicting same-generation callback accepted")
		}
		observation.Effective.Replication.PumpBackoffMaxMS = 1
		fresh, _ := r.observation()
		if *fresh.Effective.Replication != values {
			t.Fatal("observation aliases owner state")
		}
		// A new process's actual constructors, not the callback, establish
		// the successor's effective generation.
		newPump, newAnti := workers(next)
		restarted, err := newReplicationPolicyRuntime(newPump, newAnti, policy(5, next))
		if err != nil {
			t.Fatal(err)
		}
		after, err := restarted.observation()
		if err != nil || after.AppliedGeneration != 5 || after.DesiredGeneration != 5 || after.PendingRestart || *after.Effective.Replication != next {
			t.Fatalf("restarted = %+v, %v", after, err)
		}
		stillRunning, _ := pump.Scheduling()
		if stillRunning.BackoffMax != 30*time.Second {
			t.Fatal("successor mutated old process worker")
		}
	})
	t.Run("actual pair and restored values required", func(t *testing.T) {
		pump, anti := workers(values)
		otherPump, _ := workers(values)
		if _, err := newReplicationPolicyRuntime(otherPump, anti, nil); err == nil {
			t.Fatal("unrelated worker pair accepted")
		}
		wrong := values
		wrong.AntiEntropyIntervalMS = 0
		if _, err := newReplicationPolicyRuntime(pump, anti, policy(1, wrong)); err == nil {
			t.Fatal("boot generation differed from actual worker config")
		}
		if _, err := newReplicationPolicyRuntime(nil, anti, nil); err == nil {
			t.Fatal("missing pump accepted")
		}
		mismatch := replication.NewAntiEntropy(replication.AntiEntropyConfig{Pump: pump, SearchConfigFingerprint: "different"}, nil, nil, nil)
		if _, err := newReplicationPolicyRuntime(pump, mismatch, nil); err == nil {
			t.Fatal("mixed Search fingerprints accepted")
		}
	})
	t.Run("invalid bootstrap remains observable and repair stages", func(t *testing.T) {
		pump := replication.NewPump(replication.Config{BackoffMin: time.Minute}, nil, nil)
		anti := replication.NewAntiEntropy(replication.AntiEntropyConfig{Pump: pump, GapWarnThreshold: math.MaxUint64}, nil, nil, nil)
		r, err := newReplicationPolicyRuntime(pump, anti, nil)
		if err != nil {
			t.Fatal(err)
		}
		if observed, err := r.observation(); err == nil || observed.Effective != nil || observed.AppliedGeneration != 0 {
			t.Fatal("invalid bootstrap became an invented default")
		}
		if err := r.ApplyOperationalPolicy(*policy(1, values)); err != nil {
			t.Fatal(err)
		}
		observed, err := r.observation()
		if err == nil || observed.Effective != nil || !observed.PendingRestart || observed.DesiredGeneration != 1 || observed.AppliedGeneration != 0 {
			t.Fatal("staging repaired policy falsely installed live worker values")
		}
	})
	t.Run("concurrent callbacks never advance applied", func(t *testing.T) {
		pump, anti := workers(values)
		r, err := newReplicationPolicyRuntime(pump, anti, policy(1, values))
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for n := uint64(2); n <= 32; n++ {
			wg.Go(func() {
				_ = r.ApplyOperationalPolicy(*policy(n, values))
				_, _ = r.observation()
			})
		}
		wg.Wait()
		observed, err := r.observation()
		if err != nil || observed.AppliedGeneration != 1 || observed.DesiredGeneration != 32 || !observed.PendingRestart {
			t.Fatalf("concurrent snapshot = %+v, %v", observed, err)
		}
	})
}

func TestResolveReplicationPolicy(t *testing.T) {
	values := operationalpolicy.Replication{AntiEntropyGapWarnThreshold: 0, AntiEntropyIntervalMS: 0, AntiEntropySubscribeTimeoutMS: 15, PumpBackoffMinMS: 2, PumpBackoffMaxMS: 3}
	peer := PeerConfig{Peers: []string{"external-peer"}, CAFile: "external-ca", Discovery: "dns", DNSName: "external-name", DefaultPort: "6400", DiscoveryInterval: time.Minute}
	anti := AntiEntropyConfig{Interval: time.Hour, SubscribeTimeout: time.Hour, GapWarnThreshold: 9}
	p, a, err := resolveReplicationPolicy(values, peer, anti)
	if err != nil || p.BackoffMin != 2*time.Millisecond || p.BackoffMax != 3*time.Millisecond || a.Interval != 0 || a.SubscribeTimeout != 15*time.Millisecond || a.GapWarnThreshold != 0 {
		t.Fatalf("resolved = %+v / %+v, %v", p, a, err)
	}
	p.BackoffMin, p.BackoffMax = peer.BackoffMin, peer.BackoffMax
	if !reflect.DeepEqual(p, peer) || anti.Interval != time.Hour {
		t.Fatal("resolution changed external bindings or caller input")
	}
	for _, bad := range []operationalpolicy.Replication{
		{AntiEntropySubscribeTimeoutMS: 1, PumpBackoffMinMS: 2, PumpBackoffMaxMS: 1},
		{AntiEntropySubscribeTimeoutMS: 0, PumpBackoffMinMS: 1, PumpBackoffMaxMS: 1},
		{AntiEntropyIntervalMS: -1, AntiEntropySubscribeTimeoutMS: 1, PumpBackoffMinMS: 1, PumpBackoffMaxMS: 1},
		{AntiEntropySubscribeTimeoutMS: math.MaxInt64, PumpBackoffMinMS: 1, PumpBackoffMaxMS: 1},
	} {
		if _, _, err := resolveReplicationPolicy(bad, peer, anti); err == nil {
			t.Fatalf("invalid policy accepted: %+v", bad)
		}
	}
}
