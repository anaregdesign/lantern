package pubsub

import (
	"testing"
	"time"
)

func TestNoopObserver_AllMethodsAreSafe(t *testing.T) {
	// The default noop observer is used on every subscription that does
	// not call WithObserver. It must accept any input without side
	// effects so the hot path stays branch-free.
	var o Observer = noopObserver{}
	o.RecordEnqueueDepth(0)
	o.RecordEnqueueDepth(1 << 30)
	o.RecordDrop(DropPolicyNewest)
	o.RecordDrop(DropPolicyOldest)
	o.RecordDrop(DropPolicyNewestAfterOldest)
	o.RecordDrop("unknown-policy")
	o.ObserveDispatch(0)
	o.ObserveDispatch(time.Hour)
}

func TestDropPolicies_CoversInterfaceContract(t *testing.T) {
	// DropPolicies is consumed by server-side collectors for label
	// pre-warming; it must list every policy string the pubsub package
	// can pass to Observer.RecordDrop. Guard against silent additions
	// to the policy set by checking length + membership here.
	want := map[string]struct{}{
		DropPolicyNewest:            {},
		DropPolicyOldest:            {},
		DropPolicyNewestAfterOldest: {},
	}
	if len(DropPolicies) != len(want) {
		t.Fatalf("DropPolicies = %v (len %d), want %d entries", DropPolicies, len(DropPolicies), len(want))
	}
	for _, p := range DropPolicies {
		if _, ok := want[p]; !ok {
			t.Errorf("DropPolicies contains unexpected entry %q", p)
		}
	}
}
