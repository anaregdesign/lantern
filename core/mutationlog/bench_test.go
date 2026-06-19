package mutationlog

import (
	"sync"
	"testing"
	"time"
)

// BenchmarkAppendAtCapacity exercises the eviction path that issue #252
// turned from O(N) (shift-copy of the entire ring) into O(1) head bumping.
// The pre-fix implementation degrades sharply as Capacity grows; the
// post-fix implementation is flat.
func BenchmarkAppendAtCapacity(b *testing.B) {
	const capacity = 100_000
	l := New(Options{Capacity: capacity, SubscriberBuffer: 1})
	// Pre-fill so every iteration below hits the eviction branch.
	for i := 0; i < capacity; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			b.Fatal(err)
		}
	}
}

// sleepWAL models a durability layer with a fixed per-write latency. Append
// holds the log write lock across WAL.Write, so this is the knob the #745
// benchmarks use to show how WAL latency couples to append throughput.
type sleepWAL struct{ delay time.Duration }

func (w sleepWAL) Write(Entry) error {
	if w.delay > 0 {
		time.Sleep(w.delay)
	}
	return nil
}

// BenchmarkAppend measures the steady-state append hot path under the #745
// locking model across three WAL/fan-out shapes: a no-op WAL, a slow WAL (the
// latency that serializes under the write lock), and many live subscribers
// (which the #260 dispatcher decouples from append latency).
func BenchmarkAppend(b *testing.B) {
	b.Run("NopWAL", func(b *testing.B) {
		l := New(Options{Capacity: 4096, SubscriberBuffer: 4096})
		defer l.Close()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := l.Append(i, ts(int64(i))); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("SlowWAL", func(b *testing.B) {
		l := New(Options{Capacity: 4096, SubscriberBuffer: 4096, WAL: sleepWAL{delay: 5 * time.Microsecond}})
		defer l.Close()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := l.Append(i, ts(int64(i))); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("ManySubscribers", func(b *testing.B) {
		const subs = 64
		l := New(Options{Capacity: 4096, SubscriberBuffer: 1 << 20})
		defer l.Close()
		for s := 0; s < subs; s++ {
			ch, cancel, err := l.Subscribe(1)
			if err != nil {
				b.Fatal(err)
			}
			defer cancel()
			go func() {
				for range ch {
				}
			}()
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := l.Append(i, ts(int64(i))); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkStatusReadsDuringAppend measures the read-only status path
// (LastSeq) under concurrent append pressure. After #745 these reads take the
// RWMutex read lock, so this captures the contended status-poll cost a
// replication monitor pays while writes stream in.
func BenchmarkStatusReadsDuringAppend(b *testing.B) {
	l := New(Options{Capacity: 4096, SubscriberBuffer: 4096})
	defer l.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = l.Append(i, ts(int64(i)))
			i++
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = l.LastSeq()
	}
	b.StopTimer()
	close(stop)
	wg.Wait()
}

// BenchmarkSubscribeReplay measures the cost of registering a subscriber that
// replays a full ring-buffer window, the Subscribe critical section #745
// item 5 targets. The replay window is sized to the whole capacity so every
// resident entry is copied into the new subscriber channel under the lock.
func BenchmarkSubscribeReplay(b *testing.B) {
	const capacity = 4096
	l := New(Options{Capacity: capacity, SubscriberBuffer: capacity})
	defer l.Close()
	for i := 0; i < capacity; i++ {
		if _, err := l.Append(i, ts(int64(i))); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, cancel, err := l.Subscribe(1)
		if err != nil {
			b.Fatal(err)
		}
		cancel()
	}
}
