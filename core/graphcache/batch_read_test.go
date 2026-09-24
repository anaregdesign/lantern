package graphcache

import (
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetEdgeDetailsRequestAlignmentAndVisibility(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	deadline := time.Now().Add(time.Hour)
	c.AddEdgeWithExpiration("live-tail", "live-head", 2.5, deadline)
	c.AddEdgeWithExpiration("dangling-tail", "dangling-head", 3, deadline)
	c.DeleteVertex("dangling-head")
	c.AddEdgeWithExpiration("expired-tail", "expired-head", 4, time.Now().Add(-time.Second))

	keys := []EdgeKey[string]{
		{Tail: "live-tail", Head: "live-head"},
		{Tail: "missing-tail", Head: "missing-head"},
		{Tail: "live-tail", Head: "live-head"},
		{Tail: "dangling-tail", Head: "dangling-head"},
		{Tail: "expired-tail", Head: "expired-head"},
		{Tail: "missing-tail", Head: "missing-head"},
	}
	got := c.GetEdgeDetails(keys)
	if len(got) != len(keys) {
		t.Fatalf("results = %d, want %d", len(got), len(keys))
	}
	if !got[0].Found || got[0].Weight != 2.5 || !got[0].Expiration.Equal(deadline) || got[2] != got[0] {
		t.Fatalf("duplicate live results = %+v / %+v", got[0], got[2])
	}
	for _, i := range []int{1, 3, 4, 5} {
		if got[i].Found {
			t.Fatalf("results[%d] = %+v, want missing", i, got[i])
		}
	}
	if got[1] != got[5] {
		t.Fatalf("duplicate missing results differ: %+v / %+v", got[1], got[5])
	}
	if empty := c.GetEdgeDetails(nil); len(empty) != 0 {
		t.Fatalf("empty batch = %+v", empty)
	}
}

func TestGetEdgeDetailsConcurrentBatchAndFastAdd(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	c.AddEdgeWithExpiration("tail", "a", 1, time.Time{})
	c.AddEdgeWithExpiration("tail", "b", 1, time.Time{})
	keys := []EdgeKey[string]{
		{Tail: "tail", Head: "a"},
		{Tail: "tail", Head: "b"},
		{Tail: "tail", Head: "a"},
		{Tail: "tail", Head: "b"},
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var writes atomic.Int64
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			c.PutEdgesWithExpiration([]EdgeItem[string]{
				{Tail: "tail", Head: "a", Weight: 2, Expiration: time.Time{}},
				{Tail: "tail", Head: "b", Weight: 2, Expiration: time.Time{}},
			})
			c.PutEdgesWithExpiration([]EdgeItem[string]{
				{Tail: "tail", Head: "a", Weight: 1, Expiration: time.Time{}},
				{Tail: "tail", Head: "b", Weight: 1, Expiration: time.Time{}},
			})
			writes.Add(1)
			runtime.Gosched()
		}
	}()
	go func() {
		defer wg.Done()
		// The fast path has no aggregate graph lock. Its one-edge Add can
		// legitimately change a relative to b, but the duplicate a entries
		// within a single read still must agree.
		for {
			select {
			case <-stop:
				return
			default:
			}
			c.AddEdgeWithExpiration("tail", "a", 1, time.Time{})
			writes.Add(1)
			runtime.Gosched()
		}
	}()
	for i := 0; i < 200; i++ {
		got := c.GetEdgeDetails(keys)
		if !got[0].Found || !got[1].Found || got[0] != got[2] || got[1] != got[3] {
			close(stop)
			wg.Wait()
			t.Fatalf("mixed read at iteration %d: %+v", i, got)
		}
	}
	close(stop)
	wg.Wait()
	if writes.Load() == 0 {
		t.Fatal("concurrent writers made no progress")
	}
}

func TestGetEdgeDetailsLockOrderWithMaintenance(t *testing.T) {
	c := NewGraphCache[string, string](time.Hour)
	keys := []EdgeKey[string]{
		{Tail: "tail", Head: "a"},
		{Tail: "tail", Head: "b"},
		{Tail: "tail", Head: "c"},
	}
	for _, key := range keys {
		c.AddEdgeWithExpiration(key.Tail, key.Head, 1, time.Time{})
	}
	reverse := []EdgeKey[string]{keys[2], keys[1], keys[0]}
	var wg sync.WaitGroup
	for _, readKeys := range [][]EdgeKey[string]{keys, reverse} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = c.GetEdgeDetails(readKeys)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.AddEdgeWithExpiration("tail", "a", 1, time.Time{})
			c.PutEdgesWithExpiration([]EdgeItem[string]{
				{Tail: "tail", Head: "b", Weight: 1},
				{Tail: "tail", Head: "c", Weight: 1},
			})
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			_ = c.SnapshotGraph()
			c.flushVertices()
			c.flush()
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("batch reads, Add, Snapshot, or GC deadlocked")
	}
}

func BenchmarkGetEdgeDetails(b *testing.B) {
	for _, size := range []int{1, 16, 128} {
		b.Run("batch-"+strconv.Itoa(size), func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			keys := make([]EdgeKey[string], size)
			for i := range keys {
				keys[i] = EdgeKey[string]{Tail: "tail", Head: strconv.Itoa(i)}
				c.AddEdgeWithExpiration(keys[i].Tail, keys[i].Head, 1, time.Time{})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = c.GetEdgeDetails(keys)
			}
		})
	}
}
