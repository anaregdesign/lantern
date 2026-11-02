package lifecyclepolicy

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockedLifecycleSource struct {
	owner            *Owner
	first            atomic.Bool
	entered, release chan struct{}
}

func (s *blockedLifecycleSource) LifecycleSnapshot() Snapshot {
	if s.first.CompareAndSwap(false, true) {
		close(s.entered)
		<-s.release
	}
	return s.owner.LifecycleSnapshot()
}

func TestShutdownPlanCurrentRechecksConcurrentCapture(t *testing.T) {
	initial := Snapshot{Generation: 1}
	owner, err := NewOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	source := &blockedLifecycleSource{owner: owner, entered: make(chan struct{}), release: make(chan struct{})}
	plan, err := NewShutdownPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	current := make(chan Snapshot, 1)
	go func() { current <- plan.Current() }()
	select {
	case <-source.entered:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	if plan.Capture() != initial {
		t.Fatal("concurrent capture did not freeze initial generation")
	}
	if err := owner.Apply(Snapshot{Generation: 2}); err != nil {
		t.Fatal(err)
	}
	close(source.release)
	if got := <-current; got != initial {
		t.Fatalf("Current reported post-capture source generation: %+v", got)
	}
}

func TestShutdownPlanCapturesAtFirstActionAndKeepsGeneration(t *testing.T) {
	if _, err := NewShutdownPlan(nil); err == nil {
		t.Fatal("missing source accepted")
	}
	first := Snapshot{Generation: 1, Policy: Policy{DrainDelay: time.Second, ShutdownTimeout: 30 * time.Second, MaxReplicationLag: 100}}
	owner, err := NewOwner(first)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewShutdownPlan(owner)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Current() != first {
		t.Fatal("initial observation differs")
	}
	second := Snapshot{Generation: 2, Policy: Policy{ShutdownTimeout: 10 * time.Second}}
	if err := owner.Apply(second); err != nil {
		t.Fatal(err)
	}
	if plan.Current() != second || plan.Capture() != second {
		t.Fatal("construction/status read captured shutdown early")
	}
	third := Snapshot{Generation: 3, Policy: Policy{DrainDelay: 5 * time.Second}}
	if err := owner.Apply(third); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				if plan.Capture() != second || plan.Current() != second {
					t.Error("in-progress shutdown changed generation")
				}
			}
		})
	}
	workers.Wait()
	if owner.LifecycleSnapshot() != third {
		t.Fatal("frozen shutdown blocked future effective publication")
	}
}
