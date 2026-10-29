package benchheap

import (
	"context"
	"testing"
	"time"
)

func TestBenchHeapSymmetricCompletedGC(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		drift, badForced, late bool
		want                   string
	}{
		{"complete", false, false, false, StatusComplete},
		{"native drift", true, false, false, StatusIncomplete},
		{"forced count", false, true, false, StatusIncomplete},
		{"deadline", false, false, true, StatusIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1700000000, 0)
			memory := Memory{NumGC: 3, NumForcedGC: 1, CycleAfter: 3, MetricCycles: 3, MetricForced: 1}
			calls := 0
			driver := Driver{Now: func() time.Time { return now }, Wait: func(context.Context, time.Duration) error { now = now.Add(PollInterval); return nil },
				Memory: func() Memory { return memory }, Collect: func() {
					calls++
					memory.NumGC++
					memory.CycleAfter++
					memory.MetricCycles++
					if !tc.badForced {
						memory.NumForcedGC++
						memory.MetricForced++
					}
					if tc.late {
						now = now.Add(TargetBudget)
					}
				}}
			out := Observe(context.Background(), Request{ExpectedReceipts: 1}, func(context.Context) (Native, error) {
				n := Native{ReceiptEntries: 1}
				if tc.drift && calls > 0 {
					n.Generation = 1
				}
				return n, nil
			}, driver)
			if out.Status != tc.want {
				t.Fatalf("status=%s reason=%s", out.Status, out.Reason)
			}
			if out.Status == StatusComplete && (calls != 2 || len(out.Collections) != 2 || out.Memory.NumForcedGC != 3) {
				t.Fatal("symmetric GC count changed")
			}
			if tc.late && calls != 1 {
				t.Fatal("deadline triggered another collection")
			}
		})
	}
}

func TestBenchHeapDrainRequiresWholeWorkAndAllowsMaintainedSubscribe(t *testing.T) {
	if !NativeIdle(Native{PeerSubscribe: Counter{Begun: 2, Active: 2}}) {
		t.Fatal("maintained streams were forced closed")
	}
	for _, n := range []Native{{Public: Counter{Begun: 1, Active: 1}}, {PeerUnary: Counter{Begun: 1, Active: 1}}, {Apply: Counter{Begun: 1}}, {PendingCount: 1}, {PendingBytes: 1}, {PendingLocal: true}, {SnapshotActive: true}, {Faulted: true}, {PeerSubscribe: Counter{Begun: 1, Completed: 2}}} {
		if NativeIdle(n) {
			t.Fatalf("unfinished native work admitted: %+v", n)
		}
	}
	if MemoryCoherent(Memory{NumGC: 1, CycleAfter: 2, MetricCycles: 1}) {
		t.Fatal("mixed GC cycle accepted")
	}
}

func TestBenchHeapMissingSubmittedWorkNeverCollects(t *testing.T) {
	now := time.Unix(1700000000, 0)
	collections := 0
	driver := Driver{Now: func() time.Time { return now }, Wait: func(context.Context, time.Duration) error { now = now.Add(PollInterval); return nil }, Collect: func() { collections++ }, Memory: func() Memory { return Memory{} }}
	out := Observe(context.Background(), Request{ExpectedReceipts: 2}, func(context.Context) (Native, error) { return Native{ReceiptEntries: 1}, nil }, driver)
	if out.Status != StatusIncomplete || collections != 0 || len(out.Collections) != 0 {
		t.Fatal("collected before submitted work arrived")
	}
}
