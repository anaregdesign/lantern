package cache

import (
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCache_OnEvict_Delete verifies the callback fires exactly once when an
// existing key is deleted, and not at all when an absent key is "deleted".
func TestCache_OnEvict_Delete(t *testing.T) {
	c := NewCache[string, int](time.Minute)
	c.Put("a", 1)

	var got []string
	var mu sync.Mutex
	c.SetOnEvict(func(k string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, k)
	})

	if !c.Delete("a") {
		t.Fatalf("Delete(a) = false, want true")
	}
	if c.Delete("missing") {
		t.Fatalf("Delete(missing) = true, want false")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("evicted = %v, want [a]", got)
	}
}

// TestCache_OnEvict_Clear verifies the callback fires once per cleared key.
func TestCache_OnEvict_Clear(t *testing.T) {
	c := NewCache[string, int](time.Minute)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)

	var got []string
	var mu sync.Mutex
	c.SetOnEvict(func(k string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, k)
	})

	c.Clear()

	mu.Lock()
	defer mu.Unlock()
	sort.Strings(got)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("evicted = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("evicted = %v, want %v", got, want)
		}
	}
}

// TestCache_OnEvict_Flush verifies the callback fires once per key whose TTL
// has expired, and not for keys still alive.
func TestCache_OnEvict_Flush(t *testing.T) {
	c := NewCache[string, int](time.Minute)
	c.PutWithTTL("alive", 1, time.Hour)
	c.PutWithExpiration("dead-1", 2, time.Now().Add(-time.Second))
	c.PutWithExpiration("dead-2", 3, time.Now().Add(-time.Second))

	var got []string
	var mu sync.Mutex
	c.SetOnEvict(func(k string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, k)
	})

	removed := c.Flush()
	if removed != 2 {
		t.Fatalf("Flush() = %d, want 2", removed)
	}

	mu.Lock()
	defer mu.Unlock()
	sort.Strings(got)
	want := []string{"dead-1", "dead-2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("evicted = %v, want %v", got, want)
	}
}

// TestCache_OnEvict_NotInstalled verifies Delete / Clear / Flush behave
// identically to before this commit when no callback is installed: no
// panic, return values unchanged.
func TestCache_OnEvict_NotInstalled(t *testing.T) {
	c := NewCache[string, int](time.Minute)
	c.Put("a", 1)
	c.PutWithExpiration("dead", 2, time.Now().Add(-time.Second))

	if !c.Delete("a") {
		t.Fatalf("Delete(a) = false, want true")
	}
	if c.Flush() != 1 {
		t.Fatalf("Flush() = unexpected, want 1")
	}
	c.Put("x", 9)
	c.Clear()
	if c.Count() != 0 {
		t.Fatalf("Count after Clear = %d, want 0", c.Count())
	}
}

// TestCache_OnEvict_Reentrant verifies the callback may safely call back into
// the same Cache without deadlocking (must fire after c.mu is released).
func TestCache_OnEvict_Reentrant(t *testing.T) {
	c := NewCache[string, int](time.Minute)
	c.Put("a", 1)
	c.Put("b", 2)

	var reentrantCalls atomic.Int32
	done := make(chan struct{})
	c.SetOnEvict(func(k string) {
		// Re-enter: every read API must succeed without deadlock.
		_, _ = c.Get(k)
		_ = c.Has(k)
		_ = c.Count()
		reentrantCalls.Add(1)
	})

	go func() {
		c.Delete("a")
		c.Clear()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("callback re-entry deadlocked")
	}
	// 1 Delete + 1 Clear (which evicts "b") = 2 callbacks.
	if got := reentrantCalls.Load(); got != 2 {
		t.Fatalf("reentrantCalls = %d, want 2", got)
	}
}

// TestCache_OnEvict_Clearing verifies SetOnEvict(nil) un-installs a previously
// installed callback.
func TestCache_OnEvict_Clearing(t *testing.T) {
	c := NewCache[string, int](time.Minute)
	c.Put("a", 1)

	var calls atomic.Int32
	c.SetOnEvict(func(string) { calls.Add(1) })
	c.SetOnEvict(nil)

	c.Delete("a")
	if got := calls.Load(); got != 0 {
		t.Fatalf("calls = %d, want 0", got)
	}
}
