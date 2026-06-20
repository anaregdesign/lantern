package graphcache

import (
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

func Test_newWeight(t *testing.T) {
	tests := []struct {
		name string
		want *weight
	}{
		{
			name: "newWeight",
			want: &weight{},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := newWeight(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("newWeight() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_weight_add(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	type args struct {
		value float32
		ttl   time.Duration
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "weight_add",
			fields: fields{
				values: make([]weightValue, 0),
			},
			args: args{
				value: 1,
				ttl:   time.Minute,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			w.addWithTTL(tt.args.value, tt.args.ttl)
		})
	}
}

func Test_weight_value(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	tests := []struct {
		name   string
		fields fields
		want   float32
	}{
		{
			name: "weight_Value",
			fields: fields{
				values: []weightValue{
					{
						value:      1,
						expiration: time.Now().Add(time.Minute),
					},
					{
						value:      1,
						expiration: time.Now().Add(-time.Minute),
					},
				},
			},
			want: 1,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			if got := w.value(); got != tt.want {
				t.Errorf("value() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_weight_isZero(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	tests := []struct {
		name   string
		fields fields
		want   bool
	}{
		{
			name: "weight_isZero",
			fields: fields{
				values: []weightValue{
					{
						value:      1,
						expiration: time.Now().Add(time.Minute),
					},
				},
			},
			want: false,
		},
		{
			name: "weight_isZero",
			fields: fields{
				values: []weightValue{
					{
						value:      1,
						expiration: time.Now().Add(-time.Minute),
					},
				},
			},
			want: true,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			if got := w.isZero(); got != tt.want {
				t.Errorf("isZero() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_edgeCache_delete(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_delete",
			c: edgeCache[string]{
				tf: make(map[vertexID]map[vertexID]*weight),
			},
			args: args[string]{
				tail: "a",
				head: "b",
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.delete(tt.args.tail, tt.args.head)
		})
	}
}

func Test_edgeCache_get(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
	}
	type testCase[S comparable] struct {
		name  string
		c     edgeCache[S]
		args  args[S]
		want  float32
		want1 bool
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_get",
			c: edgeCache[string]{
				tf: make(map[vertexID]map[vertexID]*weight),
			},
			args: args[string]{
				tail: "a",
				head: "b",
			},
			want: 0,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.c.get(tt.args.tail, tt.args.head)
			if got != tt.want {
				t.Errorf("get() = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("get() = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func Test_edgeCache_set(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
		w    float32
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_set",
			c: edgeCache[string]{
				tf: make(map[vertexID]map[vertexID]*weight),
				df: make(map[vertexID]int),
			},
			args: args[string]{
				tail: "a",
				head: "b",
				w:    1,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.add(tt.args.tail, tt.args.head, tt.args.w)
		})
	}
}

func Test_edgeCache_setWithExpiration(t *testing.T) {
	type args[S comparable] struct {
		tail       S
		head       S
		w          float32
		expiration time.Time
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_setWithExpiration",
			c: edgeCache[string]{
				tf: make(map[vertexID]map[vertexID]*weight),
				df: make(map[vertexID]int),
			},
			args: args[string]{
				tail: "a",
				head: "b",
				w:    1,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.addWithExpiration(tt.args.tail, tt.args.head, tt.args.w, tt.args.expiration)
		})
	}
}

func Test_edgeCache_setWithTTL(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
		w    float32
		ttl  time.Duration
	}
	type testCase[S comparable] struct {
		name string
		c    edgeCache[S]
		args args[S]
	}
	tests := []testCase[string]{
		{
			name: "edgeCache_setWithTTL",
			c: edgeCache[string]{
				tf: make(map[vertexID]map[vertexID]*weight),
				df: make(map[vertexID]int),
			},
			args: args[string]{
				tail: "a",
				head: "b",
				w:    1,
				ttl:  time.Second,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.addWithTTL(tt.args.tail, tt.args.head, tt.args.w, tt.args.ttl)
		})
	}
}

func Test_weight_addWithExpiration(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	type args struct {
		value      float32
		expiration time.Time
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "weight_addWithExpiration",
			fields: fields{
				values: []weightValue{},
			},
			args: args{
				value:      1,
				expiration: time.Now().Add(time.Second),
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			w.addWithExpiration(tt.args.value, tt.args.expiration)
		})
	}
}

func Test_weight_addWithTTL(t *testing.T) {
	type fields struct {
		values []weightValue
	}
	type args struct {
		value float32
		ttl   time.Duration
	}
	tests := []struct {
		name   string
		fields fields
		args   args
	}{
		{
			name: "weight_addWithTTL",
			fields: fields{
				values: []weightValue{},
			},
			args: args{
				value: 1,
				ttl:   time.Second,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			w := &weight{
				values: tt.fields.values,
			}
			w.addWithTTL(tt.args.value, tt.args.ttl)
		})
	}
}

func Test_weight_snapshot(t *testing.T) {
	now := time.Now()
	t.Run("empty", func(t *testing.T) {
		w := newWeight()
		sum, latest, nonZero := w.snapshot()
		if sum != 0 || !latest.IsZero() || nonZero {
			t.Fatalf("snapshot empty = (%v, %v, %v), want (0, zero, false)", sum, latest, nonZero)
		}
	})
	t.Run("agrees with value/latestExpiration/isZero on live contributions", func(t *testing.T) {
		w := newWeight()
		w.addWithExpiration(1.5, now.Add(time.Minute))
		w.addWithExpiration(2.5, now.Add(2*time.Minute))
		w.addWithExpiration(7, now.Add(-time.Minute)) // already expired
		sum, latest, nonZero := w.snapshot()
		if got, want := sum, w.value(); got != want {
			t.Errorf("snapshot.sum = %v, want %v", got, want)
		}
		if !latest.Equal(w.latestExpiration()) {
			t.Errorf("snapshot.latest = %v, want %v", latest, w.latestExpiration())
		}
		if nonZero == w.isZero() {
			t.Errorf("snapshot.nonZero = %v, isZero = %v (should be opposite)", nonZero, w.isZero())
		}
	})
	t.Run("all expired collapses to zero", func(t *testing.T) {
		w := newWeight()
		w.addWithExpiration(3, now.Add(-time.Minute))
		sum, latest, nonZero := w.snapshot()
		if sum != 0 || !latest.IsZero() || nonZero {
			t.Fatalf("snapshot all expired = (%v, %v, %v), want (0, zero, false)", sum, latest, nonZero)
		}
	})
}

func Test_edgeCache_getDetail_noGoroutineForExpired(t *testing.T) {
	c := newEdgeCache[string](time.Minute, newDictionary[string]())
	c.addWithExpiration("a", "b", 1, time.Now().Add(-time.Second))

	before := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		if _, _, ok := c.getDetail("a", "b"); ok {
			t.Fatalf("expected expired edge to report ok=false on iteration %d", i)
		}
	}
	// Goroutine count must not drift upward: previously each call spawned
	// `go c.delete(...)`. Allow a small slack for runtime housekeeping.
	after := runtime.NumGoroutine()
	if after > before+2 {
		t.Fatalf("goroutine count grew: before=%d after=%d", before, after)
	}

	// The edge bucket may still be present until the next flush — that is the
	// intended trade.
	c.flush()
	if got := c.count(); got != 0 {
		t.Fatalf("flush should reclaim expired edge, count=%d", got)
	}
}

// Test_edgeCache_putWithExpiration_inPlaceReplace verifies the local PutEdge
// path replaces an existing edge in place (weight.replace) instead of
// delete+add, so the dict refcounts and df stay stable across a replace and a
// concurrent lock-free reader never sees the bucket vanish (#740).
func Test_edgeCache_putWithExpiration_inPlaceReplace(t *testing.T) {
	d := newDictionary[string]()
	c := newEdgeCache[string](time.Minute, d)
	exp := time.Now().Add(time.Hour)

	created, tailID, headID := c.putWithExpiration("a", "b", 1, exp)
	if !created {
		t.Fatalf("first putWithExpiration created=false, want true")
	}
	// Replace the same edge: must reuse the bucket (created=false), the same
	// ids, leave a single contribution, and not churn dict refcounts.
	refTail := d.refcount[tailID]
	refHead := d.refcount[headID]
	created2, tailID2, headID2 := c.putWithExpiration("a", "b", 5, exp)
	if created2 {
		t.Fatalf("replace reported created=true, want false")
	}
	if tailID2 != tailID || headID2 != headID {
		t.Fatalf("replace returned different ids: (%d,%d) vs (%d,%d)", tailID2, headID2, tailID, headID)
	}
	if d.refcount[tailID] != refTail || d.refcount[headID] != refHead {
		t.Fatalf("replace churned dict refcounts: tail %d->%d head %d->%d",
			refTail, d.refcount[tailID], refHead, d.refcount[headID])
	}
	if got, _, ok := c.getDetail("a", "b"); !ok || got != 5 {
		t.Fatalf("after replace getDetail = (%v,%v), want (5,true)", got, ok)
	}
}

// Test_weight_replace verifies replace swaps all contributions for a single
// one, recomputes the cached sum, and resets lastHLC (matching fresh-weight
// semantics).
func Test_weight_replace(t *testing.T) {
	w := newWeight()
	w.addWithExpiration(1, time.Now().Add(time.Hour))
	w.addWithExpiration(2, time.Now().Add(time.Hour))
	w.replace(7, time.Now().Add(time.Hour))
	if got := w.value(); got != 7 {
		t.Fatalf("value after replace = %v, want 7", got)
	}
	if len(w.values) != 1 {
		t.Fatalf("replace left %d contributions, want 1", len(w.values))
	}
	if !w.lastHLC.Equal(hlc.Timestamp{}) {
		t.Fatalf("replace did not reset lastHLC")
	}
}

// Test_weight_amortizedCompaction verifies that addWithExpiration triggers
// flushLocked() in-band when the slice grows past 2*lastFlushLen (with a floor
// of weightCompactMin), so a write-only edge cannot grow without bound.
func Test_weight_amortizedCompaction(t *testing.T) {
	w := newWeight()
	// Add many already-expired entries. Without amortized compaction these
	// would all accumulate; with it the slice must collapse to 0 once the
	// trigger fires.
	const n = 4096
	past := time.Now().Add(-time.Hour)
	for i := 0; i < n; i++ {
		w.addWithExpiration(1, past)
	}
	w.mu.Lock()
	live := len(w.values)
	w.mu.Unlock()
	// After the last in-band trigger, lastFlushLen drops to 0 (all expired),
	// so subsequent adds re-grow up to the floor before the next trigger.
	// The bound must therefore be the floor, not n.
	if live > weightCompactMin {
		t.Fatalf("expected slice bounded by floor, live=%d > %d", live, weightCompactMin)
	}
	// Sanity: peak slice growth between flushes is bounded by ~2x floor +
	// last flush size, never the full n.
	// (We can't observe the peak after the fact, but the post-state above
	// already guarantees the trigger fired at least once.)

	// Now mix live entries; trigger should still fire as the slice grows,
	// but live entries must survive.
	future := time.Now().Add(time.Hour)
	for i := 0; i < n; i++ {
		w.addWithExpiration(1, future)
	}
	w.mu.Lock()
	live = len(w.values)
	w.mu.Unlock()
	// Live entries must survive every compaction; expired ones from the
	// first loop get reclaimed, so the final live count is at least n.
	if live < n {
		t.Fatalf("live entries should survive compaction: got %d, want >= %d", live, n)
	}
	if got := w.value(); got != float32(n) {
		t.Fatalf("sum mismatch after compaction: got %v, want %v", got, n)
	}
}
