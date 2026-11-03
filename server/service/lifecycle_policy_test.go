package service

import (
	"testing"
	"time"
)

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
	actual := LifecyclePolicy{DrainDelay: 1500 * time.Millisecond}
	bootstrap := NewBootstrapLifecyclePolicyOwner(actual)
	if bootstrap.LifecycleSnapshot().Generation != 0 || bootstrap.LifecycleSnapshot().Policy != actual {
		t.Fatal("bootstrap facade lost actual legacy duration")
	}
}
