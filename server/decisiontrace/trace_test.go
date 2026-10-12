package decisiontrace

import "testing"

func TestBoundedNonblockingCapture(t *testing.T) {
	c := Start(2)
	if c == nil {
		t.Fatal("capture")
	}
	defer c.Stop()
	if Start(1) != nil {
		t.Fatal("replaced live capture")
	}
	c.mu.Lock()
	Emit(Event{Kind: "locked"})
	c.mu.Unlock()
	Emit(Event{Kind: "one"})
	Emit(Event{Kind: "two"})
	Emit(Event{Kind: "full"})
	events, dropped := c.Drain()
	if len(events) != 2 || dropped != 2 || events[0].At.IsZero() {
		t.Fatalf("events=%d drops=%d", len(events), dropped)
	}
	c.Stop()
	Emit(Event{Kind: "disabled"})
	events, _ = c.Drain()
	if len(events) != 0 {
		t.Fatal("disabled capture")
	}
}
func TestTransitionCounts(t *testing.T) {
	c := Start(8)
	defer c.Stop()
	var s Transition
	s.Emit("ok", Event{Reason: "ok"})
	s.Emit("ok", Event{Reason: "ok"})
	s.Emit("age", Event{Reason: "age", Check: 9})
	events, drops := c.Drain()
	if drops != 0 || len(events) != 2 || events[1].Repeats != 1 || events[1].Check != 9 {
		t.Fatal("transition folding")
	}
}
