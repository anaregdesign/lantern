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
	t.Run("GetAtUsesCallerInstant", func(t *testing.T) {
		expiration := time.Unix(200, 0)
		c := Cache[string, int]{cache: map[string]volatile[int]{
			"k": {value: 7, expiration: expiration},
		}}
		if got, ok := c.GetAt("k", expiration.Add(-time.Nanosecond)); !ok || got != 7 {
			t.Fatalf("GetAt before expiration = (%d, %t), want (7, true)", got, ok)
		}
		if got, ok := c.GetAt("k", expiration); ok || got != 0 {
			t.Fatalf("GetAt at expiration = (%d, %t), want (0, false)", got, ok)
		}
	})
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

func TestCache_HasAt(t *testing.T) {
	base := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	c := NewCache[string, int](time.Minute)
	c.PutWithExpiration("k", 1, base.Add(time.Minute))
	if !c.HasAt("k", base) {
		t.Fatal("HasAt before expiration = false, want true")
	}
	if c.HasAt("k", base.Add(time.Minute)) {
		t.Fatal("HasAt at expiration = true, want false")
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

// TestCache_DeleteMany verifies the batch delete removes every present key,
// skips absent keys, reports the removed set, and is a no-op for an empty
// input.
func TestCache_DeleteMany(t *testing.T) {
	c := NewCache[string, int](time.Minute)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Put("c", 3)

	if got := c.DeleteMany(nil); got != nil {
		t.Fatalf("DeleteMany(nil) = %v, want nil", got)
	}

	evicted := c.DeleteMany([]string{"a", "missing", "c", "a"})
	sort.Strings(evicted)
	want := []string{"a", "c"}
	if !reflect.DeepEqual(evicted, want) {
		t.Fatalf("DeleteMany evicted = %v, want %v", evicted, want)
	}
	if c.Has("a") || c.Has("c") {
		t.Fatalf("deleted keys still present")
	}
	if !c.Has("b") {
		t.Fatalf("untouched key b was removed")
	}
}

// TestCache_OnEvictMany_Batch verifies the batch hook fires exactly once with
// the full set of removed keys for Delete (one-element slice), DeleteMany,
// Clear, and Flush, and that it takes precedence over a per-key hook.
func TestCache_OnEvictMany_Batch(t *testing.T) {
	type batch []string
	newWatched := func() (*[]batch, *sync.Mutex, *atomic.Int32) {
		var batches []batch
		var mu sync.Mutex
		var perKey atomic.Int32
		return &batches, &mu, &perKey
	}

	t.Run("DeleteSingleKeyArrivesAsBatch", func(t *testing.T) {
		c := NewCache[string, int](time.Minute)
		c.Put("a", 1)
		batches, mu, perKey := newWatched()
		c.SetOnEvict(func(string) { perKey.Add(1) })
		c.SetOnEvictMany(func(keys []string) {
			mu.Lock()
			defer mu.Unlock()
			*batches = append(*batches, append(batch(nil), keys...))
		})
		c.Delete("a")
		mu.Lock()
		defer mu.Unlock()
		if len(*batches) != 1 || len((*batches)[0]) != 1 || (*batches)[0][0] != "a" {
			t.Fatalf("batches = %v, want [[a]]", *batches)
		}
		if perKey.Load() != 0 {
			t.Fatalf("per-key hook fired %d times; batch hook must take precedence", perKey.Load())
		}
	})

	t.Run("DeleteMany", func(t *testing.T) {
		c := NewCache[string, int](time.Minute)
		c.Put("a", 1)
		c.Put("b", 2)
		c.Put("c", 3)
		batches, mu, _ := newWatched()
		c.SetOnEvictMany(func(keys []string) {
			mu.Lock()
			defer mu.Unlock()
			*batches = append(*batches, append(batch(nil), keys...))
		})
		c.DeleteMany([]string{"a", "b", "missing"})
		mu.Lock()
		defer mu.Unlock()
		if len(*batches) != 1 {
			t.Fatalf("want exactly one batch, got %v", *batches)
		}
		got := append(batch(nil), (*batches)[0]...)
		sort.Strings(got)
		if !reflect.DeepEqual([]string(got), []string{"a", "b"}) {
			t.Fatalf("batch = %v, want [a b]", got)
		}
	})

	t.Run("Clear", func(t *testing.T) {
		c := NewCache[string, int](time.Minute)
		c.Put("a", 1)
		c.Put("b", 2)
		batches, mu, _ := newWatched()
		c.SetOnEvictMany(func(keys []string) {
			mu.Lock()
			defer mu.Unlock()
			*batches = append(*batches, append(batch(nil), keys...))
		})
		c.Clear()
		mu.Lock()
		defer mu.Unlock()
		if len(*batches) != 1 || len((*batches)[0]) != 2 {
			t.Fatalf("Clear batch = %v, want one batch of 2", *batches)
		}
	})

	t.Run("Flush", func(t *testing.T) {
		c := NewCache[string, int](time.Minute)
		c.PutWithTTL("alive", 1, time.Hour)
		c.PutWithExpiration("dead-1", 2, time.Now().Add(-time.Second))
		c.PutWithExpiration("dead-2", 3, time.Now().Add(-time.Second))
		batches, mu, _ := newWatched()
		c.SetOnEvictMany(func(keys []string) {
			mu.Lock()
			defer mu.Unlock()
			*batches = append(*batches, append(batch(nil), keys...))
		})
		if removed := c.Flush(); removed != 2 {
			t.Fatalf("Flush() = %d, want 2", removed)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(*batches) != 1 {
			t.Fatalf("want exactly one batch, got %v", *batches)
		}
		got := append(batch(nil), (*batches)[0]...)
		sort.Strings(got)
		if !reflect.DeepEqual([]string(got), []string{"dead-1", "dead-2"}) {
			t.Fatalf("Flush batch = %v, want [dead-1 dead-2]", got)
		}
	})

	t.Run("Clearing", func(t *testing.T) {
		c := NewCache[string, int](time.Minute)
		c.Put("a", 1)
		var calls atomic.Int32
		c.SetOnEvictMany(func([]string) { calls.Add(1) })
		c.SetOnEvictMany(nil)
		c.Delete("a")
		if calls.Load() != 0 {
			t.Fatalf("calls = %d, want 0 after SetOnEvictMany(nil)", calls.Load())
		}
	})
}

// TestCache_UpsertWithExpiration covers the single-lock physical-presence probe
// added for #739: it reports whether a slot for the key existed beforehand even
// when that slot had already expired, which Has/Get (live checks) hide.
func TestCache_UpsertWithExpiration(t *testing.T) {
	t.Run("reports physical presence and overwrites", func(t *testing.T) {
		c := NewCache[string, int](time.Minute)
		if existed := c.UpsertWithExpiration("k", 1, time.Now().Add(time.Minute)); existed {
			t.Fatal("first upsert: physicallyExisted=true, want false")
		}
		if existed := c.UpsertWithExpiration("k", 2, time.Now().Add(time.Minute)); !existed {
			t.Fatal("second upsert: physicallyExisted=false, want true")
		}
		if got, ok := c.Get("k"); !ok || got != 2 {
			t.Fatalf("Get after overwrite: got=%v ok=%v want=2 true", got, ok)
		}
	})

	t.Run("expired-but-not-flushed slot counts as present", func(t *testing.T) {
		c := NewCache[string, int](time.Minute)
		// Store an already-expired entry and never flush it, so the physical
		// slot lingers while the live checks report it absent.
		c.UpsertWithExpiration("k", 1, time.Now().Add(-time.Minute))
		if _, ok := c.Get("k"); ok {
			t.Fatal("Get on expired slot: ok=true, want false")
		}
		if c.Has("k") {
			t.Fatal("Has on expired slot: true, want false")
		}
		// Upsert, by contrast, must see the lingering physical slot.
		if existed := c.UpsertWithExpiration("k", 2, time.Now().Add(time.Minute)); !existed {
			t.Fatal("upsert over expired-not-flushed slot: physicallyExisted=false, want true")
		}
		if got, ok := c.Get("k"); !ok || got != 2 {
			t.Fatalf("Get after reviving overwrite: got=%v ok=%v want=2 true", got, ok)
		}
	})
}
