package lifecyclepolicy

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestOwnerValidationAndAtomicGenerations(t *testing.T) {
	for _, bad := range []Policy{
		{DrainDelay: -time.Second}, {ShutdownTimeout: -time.Second},
		{DrainDelay: time.Nanosecond}, {ShutdownTimeout: time.Millisecond},
	} {
		if bad.Validate() == nil {
			t.Fatal("invalid duration accepted")
		}
		if _, err := NewOwner(Snapshot{Policy: bad}); err == nil {
			t.Fatal("invalid initial owner accepted")
		}
	}
	initial := Snapshot{Generation: 1, Policy: Policy{DrainDelay: time.Second, ShutdownTimeout: 30 * time.Second, MaxReplicationLag: 10}}
	owner, err := NewOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	next := Snapshot{Generation: 2, Policy: Policy{MaxReplicationLag: math.MaxUint64}}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 1000 {
				if got := owner.LifecycleSnapshot(); got != initial && got != next {
					t.Errorf("torn lifecycle snapshot: %+v", got)
				}
			}
		})
	}
	if err := owner.Apply(next); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	if err := owner.Apply(next); err != nil {
		t.Fatal("exact generation replay rejected", err)
	}
	conflict := next
	conflict.Policy.MaxReplicationLag = 0
	invalid := next
	invalid.Generation++
	invalid.Policy.DrainDelay = -time.Second
	for _, rejected := range []Snapshot{initial, conflict, invalid} {
		if owner.Apply(rejected) == nil || owner.LifecycleSnapshot() != next {
			t.Fatal("failed callback changed lifecycle snapshot")
		}
	}
}

func TestBootstrapOwnerPreservesUnrepresentableActualUntilManagedRepair(t *testing.T) {
	actual := Policy{DrainDelay: 1500 * time.Millisecond, ShutdownTimeout: 2500 * time.Millisecond, MaxReplicationLag: 17}
	owner := NewBootstrapOwner(actual)
	if owner.LifecycleSnapshot() != (Snapshot{Policy: actual}) {
		t.Fatal("bootstrap replaced actual policy with defaults")
	}
	if owner.Apply(Snapshot{Generation: 1, Policy: actual}) == nil {
		t.Fatal("unrepresentable actual bootstrap became managed")
	}
	next := Snapshot{Generation: 1, Policy: Policy{ShutdownTimeout: 3 * time.Second}}
	if err := owner.Apply(next); err != nil || owner.LifecycleSnapshot() != next {
		t.Fatal("valid managed generation could not repair bootstrap", err)
	}
}
