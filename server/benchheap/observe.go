package benchheap

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"runtime/metrics"
	"time"
)

var ErrBusy = errors.New("measurement snapshot busy")

// Driver permits deterministic unit tests; the tagged App uses RuntimeDriver.
// No observer changes GC scheduling during normal service work.
type Driver struct {
	Now     func() time.Time
	Wait    func(context.Context, time.Duration) error
	Collect func()
	Memory  func() Memory
}

func RuntimeDriver() Driver {
	return Driver{Now: time.Now, Collect: runtime.GC, Memory: ReadMemory, Wait: func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}
}

// ReadMemory captures allocator counters immediately; callers must invoke it
// before native bracket B or output encoding. The final GC counter detects a
// different collection during the runtime/metrics read. No profiling is used.
func ReadMemory() Memory {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	samples := [3]metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}, {Name: "/gc/cycles/forced:gc-cycles"}}
	metrics.Read(samples[:])
	runtime.ReadMemStats(&after)
	return Memory{HeapAlloc: before.HeapAlloc, HeapObjects: before.HeapObjects, HeapInuse: before.HeapInuse,
		NumGC: before.NumGC, NumForcedGC: before.NumForcedGC, LastGC: before.LastGC, NextGC: before.NextGC,
		MarkedLive: samples[0].Value.Uint64(), MetricCycles: samples[1].Value.Uint64(), MetricForced: samples[2].Value.Uint64(), CycleAfter: after.NumGC}
}

func CounterIdle(c Counter) bool { return c.Active == 0 && c.Begun == c.Completed }

func NativeIdle(n Native) bool {
	return !n.Faulted && n.PendingCount == 0 && n.PendingBytes == 0 && !n.PendingLocal && !n.SnapshotActive && len(n.Origins) <= MaxOrigins &&
		CounterIdle(n.Public) && CounterIdle(n.PeerUnary) && CounterIdle(n.Apply) &&
		n.PeerSubscribe.Active >= 0 && n.PeerSubscribe.Completed <= n.PeerSubscribe.Begun &&
		n.PeerSubscribe.Begun-n.PeerSubscribe.Completed == uint64(n.PeerSubscribe.Active)
}

func NativeEqual(a, b Native) bool { return reflect.DeepEqual(a, b) }
func MemoryCoherent(m Memory) bool {
	return m.CycleAfter == m.NumGC && m.MetricCycles == uint64(m.NumGC) && m.MetricForced == uint64(m.NumForcedGC)
}

// Observe performs one admitted cut, without retries or alternate samples. Its
// caller writes Started first and owns admission, files, signals and shutdown.
// Snapshot takes only a brief existing native read cut, never across Collect.
func Observe(ctx context.Context, request Request, snapshot func(context.Context) (Native, error), driver Driver) Completion {
	start := driver.Now()
	ctx, cancel := context.WithTimeout(ctx, TargetBudget)
	defer cancel()
	out := Completion{Schema: Schema, Protocol: Protocol, Request: request, Status: StatusIncomplete, Reason: "drain", Collections: make([]GC, 0, 2)}
	deadline := start.Add(TargetBudget)
	finish := func(reason string) Completion {
		out.Reason = reason
		out.ElapsedNS = driver.Now().Sub(start).Nanoseconds()
		return out
	}
	var prior Native
	var stableSince time.Time
	for {
		if ctx.Err() != nil || !driver.Now().Before(start.Add(DrainBudget)) {
			return finish("drain_deadline")
		}
		native, err := snapshot(ctx)
		if err != nil && !errors.Is(err, ErrBusy) {
			return finish("native_snapshot")
		}
		if err == nil && NativeIdle(native) && request.ExpectedReceipts > 0 && request.ExpectedReceipts <= 8000 && int64(native.ReceiptEntries) >= request.ExpectedReceipts {
			if stableSince.IsZero() || !NativeEqual(native, prior) {
				stableSince = driver.Now()
			}
			prior = native
			if driver.Now().Sub(stableSince) >= StableWindow {
				out.Before = native
				break
			}
		} else {
			stableSince = time.Time{}
		}
		if driver.Wait(ctx, PollInterval) != nil {
			return finish("cancelled")
		}
	}
	for i := 0; i < 2; i++ {
		if ctx.Err() != nil || !driver.Now().Before(deadline) {
			return finish("collection_deadline")
		}
		before := driver.Memory()
		started := driver.Now()
		driver.Collect()
		returned := driver.Now()
		// This read precedes bracket B, slice growth and completion encoding.
		after := driver.Memory()
		out.Collections = append(out.Collections, GC{StartedNS: started.Sub(start).Nanoseconds(), ReturnedNS: returned.Sub(start).Nanoseconds(), Before: before, After: after})
		if !MemoryCoherent(before) || !MemoryCoherent(after) || after.NumGC <= before.NumGC || after.NumForcedGC <= before.NumForcedGC || after.NumForcedGC-before.NumForcedGC != 1 {
			return finish("collection_counters")
		}
		if i == 1 {
			out.Memory = after
		}
	}
	if ctx.Err() != nil || !driver.Now().Before(deadline) {
		return finish("collection_deadline")
	}
	var err error
	out.After, err = snapshot(ctx)
	if err != nil || !NativeIdle(out.After) || !NativeEqual(out.Before, out.After) {
		return finish("native_drift")
	}
	if out.Collections[1].After.NumForcedGC-out.Collections[0].Before.NumForcedGC != 2 {
		return finish("forced_counter_drift")
	}
	if ctx.Err() != nil || !driver.Now().Before(deadline) {
		return finish("capture_deadline")
	}
	out.Status = StatusComplete
	return finish("")
}
