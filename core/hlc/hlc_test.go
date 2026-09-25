package hlc

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func nodeID(b byte) NodeID {
	var n NodeID
	n[0] = b
	return n
}

// fakeNow is an atomic int64 wall-time source for deterministic tests.
type fakeNow struct{ ns atomic.Int64 }

func (f *fakeNow) set(t time.Time) { f.ns.Store(t.UnixNano()) }
func (f *fakeNow) add(d time.Duration) int64 {
	return f.ns.Add(int64(d))
}
func (f *fakeNow) get() int64 { return f.ns.Load() }

func TestTimestampLessOrdering(t *testing.T) {
	a := Timestamp{WallNs: 10, Logical: 0, NodeID: nodeID(1)}
	b := Timestamp{WallNs: 10, Logical: 0, NodeID: nodeID(2)}
	c := Timestamp{WallNs: 10, Logical: 1, NodeID: nodeID(1)}
	d := Timestamp{WallNs: 11, Logical: 0, NodeID: nodeID(1)}

	cases := []struct {
		name     string
		x, y     Timestamp
		wantLess bool
	}{
		{"node tiebreak", a, b, true},
		{"logical wins over node", a, c, true},
		{"wall wins over logical", c, d, true},
		{"equal not less", a, a, false},
		{"reverse not less", b, a, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.x.Less(tc.y); got != tc.wantLess {
				t.Fatalf("Less = %v, want %v", got, tc.wantLess)
			}
		})
	}

	if !a.Equal(a) {
		t.Fatal("Equal must hold for identical timestamps")
	}
	if a.Equal(b) {
		t.Fatal("Equal must distinguish NodeID")
	}
}

func TestNowIsStrictlyMonotonic(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(1_700_000_000, 0))
	c := New(nodeID(7), Options{Now: f.get})

	var prev Timestamp
	for i := 0; i < 1000; i++ {
		// Hold wall time constant for the first half, then advance.
		if i == 500 {
			f.add(time.Millisecond)
		}
		got := c.Now()
		if i > 0 && !prev.Less(got) {
			t.Fatalf("iteration %d: got %+v, prev %+v, want strictly greater", i, got, prev)
		}
		prev = got
	}
}

func TestNowResetsLogicalWhenWallAdvances(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 1000))
	c := New(nodeID(1), Options{Now: f.get})

	first := c.Now()
	second := c.Now()
	if second.Logical != first.Logical+1 {
		t.Fatalf("logical should bump when wall stays put: %d → %d", first.Logical, second.Logical)
	}

	f.add(time.Microsecond)
	third := c.Now()
	if third.Logical != 0 {
		t.Fatalf("logical should reset when wall advances, got %d", third.Logical)
	}
	if !second.Less(third) {
		t.Fatal("third must order after second")
	}
}

func TestRestoreFloorKeepsCommittedOrderAfterWallClockRollback(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(1, 0))
	c := New(nodeID(1), Options{Now: f.get, MaxSkew: time.Millisecond})
	floor := Timestamp{WallNs: time.Unix(10, 0).UnixNano(), Logical: 7, NodeID: nodeID(9)}
	if err := c.RestoreFloor(floor); err != nil {
		t.Fatal(err)
	}

	first := c.Now()
	if !floor.Less(first) || first.WallNs != floor.WallNs || first.Logical != floor.Logical+1 {
		t.Fatalf("restored Now = %+v, want strictly after %+v", first, floor)
	}
	remote := Timestamp{WallNs: floor.WallNs - 1, Logical: 100, NodeID: nodeID(2)}
	second := c.Update(remote)
	if !first.Less(second) || !remote.Less(second) {
		t.Fatalf("restored Update = %+v, want after local %+v and remote %+v", second, first, remote)
	}
	// A later, lower persisted floor cannot undo a stamp already emitted.
	if err := c.RestoreFloor(floor); err != nil {
		t.Fatal(err)
	}
	if third := c.Now(); !second.Less(third) {
		t.Fatalf("re-restored Now = %+v, want after %+v", third, second)
	}
}

func TestRestoreFloorStageCommitAndAbort(t *testing.T) {
	c := New(nodeID(1), Options{Now: func() int64 { return 10 }})
	first := c.Now()
	stage, err := c.BeginRestoreFloor(Timestamp{WallNs: 100, Logical: 7, NodeID: nodeID(9)})
	if err != nil {
		t.Fatal(err)
	}
	if got := stage.Floor(); got.WallNs != 100 || got.Logical != 7 || got.NodeID != nodeID(1) {
		t.Fatalf("staged floor = %+v", got)
	}
	stage.Abort()
	afterAbort := c.Now()
	if afterAbort.WallNs != first.WallNs || afterAbort.Logical != first.Logical+1 {
		t.Fatalf("abort did not restore prior clock: first=%+v after=%+v", first, afterAbort)
	}

	stage, err = c.BeginRestoreFloor(Timestamp{WallNs: 200, Logical: 3, NodeID: nodeID(8)})
	if err != nil {
		t.Fatal(err)
	}
	stage.Commit()
	if got := c.Now(); got.WallNs != 200 || got.Logical != 4 {
		t.Fatalf("committed floor produced %+v", got)
	}
	stage.Abort()
}

func TestRestoreFloorAvoidsLogicalWraparound(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 1))
	c := New(nodeID(1), Options{Now: f.get})
	floor := Timestamp{WallNs: 100, Logical: math.MaxUint32, NodeID: nodeID(9)}
	if err := c.RestoreFloor(floor); err != nil {
		t.Fatal(err)
	}
	first := c.Now()
	if first.WallNs != 101 || first.Logical != 0 || !floor.Less(first) {
		t.Fatalf("logical overflow produced %+v from %+v", first, floor)
	}
	// The same overflow rule applies when a live remote stamp leads.
	remote := Timestamp{WallNs: 200, Logical: math.MaxUint32, NodeID: nodeID(2)}
	out := c.Update(remote)
	if out.WallNs != 201 || out.Logical != 0 || !remote.Less(out) {
		t.Fatalf("Update logical overflow produced %+v from %+v", out, remote)
	}
}

func TestRestoreFloorRejectsUnrepresentableWithoutChangingClock(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 100))
	c := New(nodeID(1), Options{Now: f.get})
	first := c.Now()
	for _, floor := range []Timestamp{
		{WallNs: -1},
		{WallNs: math.MaxInt64, Logical: math.MaxUint32},
	} {
		if err := c.RestoreFloor(floor); !errors.Is(err, ErrInvalidRestoreFloor) {
			t.Fatalf("RestoreFloor(%+v) = %v, want ErrInvalidRestoreFloor", floor, err)
		}
	}
	second := c.Now()
	if second.WallNs != first.WallNs || second.Logical != first.Logical+1 {
		t.Fatalf("rejected floor changed clock: first=%+v second=%+v", first, second)
	}
}

func TestUpdateProducesGreaterThanBoth(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 10_000))
	c := New(nodeID(1), Options{Now: f.get})

	local := c.Now()
	remote := Timestamp{WallNs: f.get() + 50, Logical: 7, NodeID: nodeID(9)}

	out := c.Update(remote)
	if !local.Less(out) {
		t.Fatalf("Update output %+v must exceed prior local %+v", out, local)
	}
	if !remote.Less(out) {
		t.Fatalf("Update output %+v must exceed remote %+v", out, remote)
	}
	if out.NodeID != c.NodeID() {
		t.Fatalf("Update output should carry local nodeID, got %v", out.NodeID)
	}
}

func TestUpdateWhenLocalLeads(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 100_000))
	c := New(nodeID(1), Options{Now: f.get})

	local := c.Now()
	// remote is strictly behind on every axis.
	remote := Timestamp{WallNs: f.get() - 1000, Logical: 0, NodeID: nodeID(2)}

	out := c.Update(remote)
	if !local.Less(out) {
		t.Fatalf("Update output %+v must exceed prior local %+v", out, local)
	}
	if out.WallNs != local.WallNs {
		t.Fatalf("Update should not move wall backwards: got %d, prior %d", out.WallNs, local.WallNs)
	}
}

func TestUpdateClampsExcessiveSkew(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 1_000_000_000))
	var calls atomic.Int32
	var captured Timestamp
	var captureMu sync.Mutex
	c := New(nodeID(1), Options{
		Now:     f.get,
		MaxSkew: 100 * time.Millisecond,
		OnSkewExceeded: func(remote Timestamp, _ int64, err error) {
			if err != ErrSkewExceeded {
				t.Errorf("callback err = %v, want ErrSkewExceeded", err)
			}
			captureMu.Lock()
			captured = remote
			captureMu.Unlock()
			calls.Add(1)
		},
	})

	// Remote is 10s ahead — far beyond the 100ms tolerance.
	farFuture := Timestamp{WallNs: f.get() + int64(10*time.Second), Logical: 3, NodeID: nodeID(2)}
	out := c.Update(farFuture)

	if calls.Load() != 1 {
		t.Fatalf("callback should fire exactly once, got %d", calls.Load())
	}
	captureMu.Lock()
	if !captured.Equal(farFuture) {
		t.Fatalf("callback saw %+v, want %+v", captured, farFuture)
	}
	captureMu.Unlock()

	maxAllowed := f.get() + int64(100*time.Millisecond)
	if out.WallNs > maxAllowed {
		t.Fatalf("clock advanced past clamp: out=%d, max=%d", out.WallNs, maxAllowed)
	}
	if out.WallNs < f.get() {
		t.Fatalf("clock fell behind local wall: out=%d, wall=%d", out.WallNs, f.get())
	}
}

func TestUpdateSkewCeilingSaturatesWithoutOverflow(t *testing.T) {
	f := &fakeNow{}
	f.ns.Store(math.MaxInt64 - 10)
	var calls atomic.Int32
	c := New(nodeID(1), Options{
		Now: f.get, MaxSkew: 100 * time.Nanosecond,
		OnSkewExceeded: func(Timestamp, int64, error) { calls.Add(1) },
	})
	remote := Timestamp{WallNs: math.MaxInt64 - 9, Logical: 3, NodeID: nodeID(2)}
	out := c.Update(remote)
	if calls.Load() != 0 || !remote.Less(out) || out.WallNs != remote.WallNs || out.Logical != 4 {
		t.Fatalf("near-max skew Update = %+v, callback count = %d; want above in-window remote %+v", out, calls.Load(), remote)
	}
}

func TestUpdateClampedLogicalOverflowStaysWithinSkewCeiling(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 1_000_000_000))
	const skew = 100 * time.Nanosecond
	c := New(nodeID(1), Options{Now: f.get, MaxSkew: skew})
	remote := Timestamp{WallNs: f.get() + int64(time.Second), Logical: math.MaxUint32, NodeID: nodeID(2)}
	out := c.Update(remote)
	if out.WallNs != f.get()+int64(skew) || out.Logical != 1 {
		t.Fatalf("clamped Update = %+v, want wall at skew ceiling with fresh logical counter", out)
	}
}

func TestUpdateInsideSkewWindowAcceptsRemote(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 1_000_000_000))
	var calls atomic.Int32
	c := New(nodeID(1), Options{
		Now:            f.get,
		MaxSkew:        500 * time.Millisecond,
		OnSkewExceeded: func(Timestamp, int64, error) { calls.Add(1) },
	})

	near := Timestamp{WallNs: f.get() + int64(50*time.Millisecond), Logical: 2, NodeID: nodeID(2)}
	out := c.Update(near)

	if calls.Load() != 0 {
		t.Fatalf("callback must not fire within tolerance, got %d", calls.Load())
	}
	if out.WallNs != near.WallNs {
		t.Fatalf("expected remote wall accepted as-is: out=%d, remote=%d", out.WallNs, near.WallNs)
	}
	if out.Logical != near.Logical+1 {
		t.Fatalf("logical must exceed remote: out=%d, remote=%d", out.Logical, near.Logical)
	}
}

func TestUpdateConcurrentSafety(t *testing.T) {
	f := &fakeNow{}
	f.set(time.Unix(0, 1_000_000))
	c := New(nodeID(1), Options{Now: f.get})

	const goroutines = 32
	const perG = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < perG; i++ {
				if r.Intn(2) == 0 {
					_ = c.Now()
				} else {
					remote := Timestamp{
						WallNs:  f.get() + int64(r.Intn(int(time.Millisecond))),
						Logical: uint32(r.Intn(8)),
						NodeID:  nodeID(byte(2 + g%4)),
					}
					_ = c.Update(remote)
				}
				if i%50 == 0 {
					f.add(time.Microsecond)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()

	// After the storm, two fresh Nows must still be strictly ordered.
	a := c.Now()
	b := c.Now()
	if !a.Less(b) {
		t.Fatalf("post-race ordering broken: a=%+v, b=%+v", a, b)
	}
}

// TestThreeClockInterleaving exercises the HLC invariant that any chain of
// (Now, Update) operations across multiple clocks yields a strictly
// increasing sequence at each receiver, matching the paper's claim that HLC
// preserves the happens-before relation.
func TestThreeClockInterleaving(t *testing.T) {
	const iterations = 2000
	r := rand.New(rand.NewSource(0xC0FFEE))

	wall := &fakeNow{}
	wall.set(time.Unix(0, 1))

	clocks := []*Clock{
		New(nodeID(1), Options{Now: wall.get}),
		New(nodeID(2), Options{Now: wall.get}),
		New(nodeID(3), Options{Now: wall.get}),
	}
	last := make([]Timestamp, len(clocks))

	for i := 0; i < iterations; i++ {
		src := r.Intn(len(clocks))
		if r.Intn(3) == 0 {
			wall.add(time.Duration(1+r.Intn(100)) * time.Nanosecond)
		}

		if r.Intn(2) == 0 {
			ts := clocks[src].Now()
			if !last[src].Less(ts) && (last[src] != Timestamp{}) {
				t.Fatalf("clock %d: Now broke monotonicity: prev=%+v cur=%+v", src, last[src], ts)
			}
			last[src] = ts
			continue
		}

		dst := r.Intn(len(clocks))
		for dst == src {
			dst = r.Intn(len(clocks))
		}
		// Send last[src] (or a fresh Now if nothing yet) to dst.
		msg := last[src]
		if (msg == Timestamp{}) {
			msg = clocks[src].Now()
			last[src] = msg
		}
		got := clocks[dst].Update(msg)
		if !last[dst].Less(got) && (last[dst] != Timestamp{}) {
			t.Fatalf("clock %d: Update broke monotonicity: prev=%+v cur=%+v", dst, last[dst], got)
		}
		if !msg.Less(got) {
			t.Fatalf("clock %d: Update did not exceed remote: remote=%+v cur=%+v", dst, msg, got)
		}
		last[dst] = got
	}
}
