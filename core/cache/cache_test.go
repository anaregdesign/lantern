package cache

import (
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var example = map[string]volatile[int]{"a": {value: 1, expiration: time.Now().Add(time.Minute)}}

func TestCache_Clear(t *testing.T) {
	type testCase[S comparable, T any] struct {
		name string
		c    Cache[S, T]
	}
	tests := []testCase[string, int]{
		{
			name: "valid case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      example,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.Clear()
			if len(tt.c.cache) != 0 {
				t.Errorf("Clear() = %v, want %v", len(tt.c.cache), 0)
			}
		})
	}
}

func TestCache_Count(t *testing.T) {
	type testCase[S comparable, T any] struct {
		name string
		c    Cache[S, T]
		want int
	}
	tests := []testCase[string, int]{
		{
			name: "valid case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      example,
			},
			want: 1,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Count(); got != tt.want {
				t.Errorf("Count() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_Delete(t *testing.T) {
	type args[S comparable] struct {
		key S
	}
	type testCase[S comparable, T any] struct {
		name string
		c    Cache[S, T]
		args args[S]
	}
	tests := []testCase[string, int]{
		{
			name: "valid case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      map[string]volatile[int]{},
			},
			args: args[string]{key: "a"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.Delete(tt.args.key)
		})
	}
}

func TestCache_Flush(t *testing.T) {
	type testCase[S comparable, T any] struct {
		name string
		c    Cache[S, T]
	}
	tests := []testCase[string, int]{
		{
			name: "valid case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      map[string]volatile[int]{"a": {value: 1, expiration: time.Now().Add(time.Second)}},
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.Flush()
		})
	}
}

func TestCache_Get(t *testing.T) {
	type args[S comparable] struct {
		key S
	}
	type testCase[S comparable, T any] struct {
		name  string
		c     Cache[S, T]
		args  args[S]
		want  T
		want1 bool
	}
	tests := []testCase[string, int]{
		{
			name: "hit case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      example,
			},
			args:  args[string]{key: "a"},
			want:  1,
			want1: true,
		},
		{
			name: "miss case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      example,
			},
			args:  args[string]{key: "b"},
			want:  0,
			want1: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.c.Get(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Get() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("Get() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestCache_Set(t *testing.T) {
	type args[S comparable, T any] struct {
		key   S
		value T
	}
	type testCase[S comparable, T any] struct {
		name string
		c    Cache[S, T]
		args args[S, T]
	}
	tests := []testCase[string, int]{
		{
			name: "valid case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      example,
			},
			args: args[string, int]{key: "a", value: 1},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.Put(tt.args.key, tt.args.value)
		})
	}
}

func TestCache_SetWithTTL(t *testing.T) {
	type args[S comparable, T any] struct {
		key   S
		value T
		ttl   time.Duration
	}
	type testCase[S comparable, T any] struct {
		name string
		c    Cache[S, T]
		args args[S, T]
	}
	tests := []testCase[string, int]{
		{
			name: "valid case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      example,
			},
			args: args[string, int]{key: "a", value: 1, ttl: time.Second},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.PutWithTTL(tt.args.key, tt.args.value, tt.args.ttl)
		})
	}
}

func Test_volatile_IsExpired(t *testing.T) {
	type testCase[T any] struct {
		name string
		v    volatile[T]
		want bool
	}
	tests := []testCase[int]{
		{
			name: "expired case",
			v: volatile[int]{
				value:      1,
				expiration: time.Now().Add(-time.Second),
			},
			want: true,
		},
		{
			name: "not expired case",
			v: volatile[int]{
				value:      1,
				expiration: time.Now().Add(time.Second),
			},
			want: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCache_Has(t *testing.T) {
	type args[S comparable] struct {
		key S
	}
	type testCase[S comparable, T any] struct {
		name string
		c    Cache[S, T]
		args args[S]
		want bool
	}
	tests := []testCase[string, int]{
		{
			name: "hit case",
			c: Cache[string, int]{
				defaultTTL: time.Second,
				cache:      example,
			},
			args: args[string]{key: "a"},
			want: true,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Has(tt.args.key); got != tt.want {
				t.Errorf("Has() = %v, want %v", got, tt.want)
			}
		})
	}
}

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

// TestIsLiveAt covers the "never expires" sentinels documented in the
// IsLiveAt godoc. Regression for issue #250: PutVertex without an
// Expiration was silently stored as already-expired because the volatile
// entry was constructed from `(*timestamppb.Timestamp)(nil).AsTime()`,
// which yields Unix(0,0) — not Go zero — so the original Before(now)
// check evicted it on the next read.
func TestIsLiveAt(t *testing.T) {
	now := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		exp  time.Time
		want bool
	}{
		{"go zero is never-expires", time.Time{}, true},
		{"unix epoch UTC is never-expires", time.Unix(0, 0).UTC(), true},
		{"unix epoch local is never-expires", time.Unix(0, 0), true},
		{"negative unix is never-expires", time.Unix(-1, 0), true},
		{"future is live", now.Add(time.Hour), true},
		{"past is expired", now.Add(-time.Hour), false},
	}
	for _, tt := range tests {
		if got := IsLiveAt(tt.exp, now); got != tt.want {
			t.Errorf("%s: IsLiveAt(%v) = %v, want %v", tt.name, tt.exp, got, tt.want)
		}
	}
}

// TestCache_PutWithExpiration_ZeroNeverExpires is the end-to-end regression
// for #250: a value stored with a zero or unix-epoch expiration must remain
// retrievable indefinitely, and must survive a Flush pass.
func TestCache_PutWithExpiration_ZeroNeverExpires(t *testing.T) {
	cases := map[string]time.Time{
		"go-zero":    {},
		"unix-epoch": time.Unix(0, 0).UTC(),
	}
	for name, exp := range cases {
		t.Run(name, func(t *testing.T) {
			c := NewCache[string, int](time.Minute)
			c.PutWithExpiration("k", 42, exp)
			if got, ok := c.Get("k"); !ok || got != 42 {
				t.Fatalf("Get after put: got=%v ok=%v want=42 true", got, ok)
			}
			c.Flush()
			if got, ok := c.Get("k"); !ok || got != 42 {
				t.Fatalf("Get after flush: got=%v ok=%v want=42 true", got, ok)
			}
		})
	}
}
