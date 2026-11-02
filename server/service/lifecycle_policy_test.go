package service

import "testing"

func TestLifecyclePolicyAliases(t *testing.T) {
	initial := LifecyclePolicySnapshot{Generation: 1}
	owner, err := NewLifecyclePolicyOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewShutdownPlan(owner)
	if err != nil || plan.Capture() != initial {
		t.Fatal("shared lifecycle aliases changed snapshot", err)
	}
}
