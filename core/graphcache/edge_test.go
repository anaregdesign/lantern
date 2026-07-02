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

// Test_edgeCache_addExistingContribByID covers the id-keyed existing-edge
// append used by the GraphCache hot write path: it appends additively to a
// present bucket, reports ok=false when the bucket is absent (so the caller
// falls back to the locked slow path), and honors ContribID dedup.
func Test_edgeCache_addExistingContribByID(t *testing.T) {
	d := newDictionary[string]()
	c := newEdgeCache[string](time.Minute, d)
	exp := time.Now().Add(time.Minute)

	created, tailID, headID, applied := c.addWithExpirationContrib("a", "b", 2, exp, ContribID{})
	if !created || !applied {
		t.Fatalf("setup add: created=%v applied=%v, want true true", created, applied)
	}

	t.Run("existing bucket appends additively", func(t *testing.T) {
		applied, ok := c.addExistingContribByID(tailID, headID, 3, exp, ContribID{})
		if !ok || !applied {
			t.Fatalf("addExistingContribByID = (applied=%v ok=%v), want (true true)", applied, ok)
		}
		if w, present := c.get("a", "b"); !present || w != 5 {
			t.Fatalf("weight after append = %v present=%v, want 5 true", w, present)
		}
	})

	t.Run("missing bucket returns ok=false", func(t *testing.T) {
		missing := d.intern("z")
		applied, ok := c.addExistingContribByID(tailID, missing, 1, exp, ContribID{})
		if ok || applied {
			t.Fatalf("addExistingContribByID on missing bucket = (applied=%v ok=%v), want (false false)", applied, ok)
		}
	})

	t.Run("contribID dedup at edge layer", func(t *testing.T) {
		var id ContribID
		id[0] = 0x42
		if applied, ok := c.addExistingContribByID(tailID, headID, 1, exp, id); !ok || !applied {
			t.Fatalf("first contrib = (applied=%v ok=%v), want (true true)", applied, ok)
		}
		if applied, ok := c.addExistingContribByID(tailID, headID, 1, exp, id); !ok || applied {
			t.Fatalf("replay contrib = (applied=%v ok=%v), want (false true)", applied, ok)
		}
	})
}

// Test_weight_snapshotAt pins the caller-supplied-clock contract (#838): the
// SAME bucket answers differently depending only on the instant passed in,
// so a traversal that samples time.Now() once observes consistent liveness
// across every edge it visits.
func Test_weight_snapshotAt(t *testing.T) {
	base := time.Now()
	w := newWeight()
	w.addWithExpiration(2, base.Add(50*time.Millisecond))
	w.addWithExpiration(3, base.Add(200*time.Millisecond))

	if sum, latest, nonZero := w.snapshotAt(base); !nonZero || sum != 5 || !latest.Equal(base.Add(200*time.Millisecond)) {
		t.Fatalf("snapshotAt(base) = (%v, %v, %v), want (5, +200ms, true)", sum, latest, nonZero)
	}
	// Between the two expirations only the later contribution survives.
	if sum, _, nonZero := w.snapshotAt(base.Add(100 * time.Millisecond)); !nonZero || sum != 3 {
		t.Fatalf("snapshotAt(+100ms) sum = %v, want 3", sum)
	}
	// Past both expirations the bucket is fully decayed.
	if _, _, nonZero := w.snapshotAt(base.Add(300 * time.Millisecond)); nonZero {
		t.Fatal("snapshotAt(+300ms) still nonZero, want decayed")
	}
}

// Test_edgeCache_edgeCount is the parity property for the O(1) bucket
// counter (#838): after every mutation mix — additive creates (with and
// without dict, with and without ContribID), LWW puts, in-place put
// replaces, deletes, zero-weight flushes, and keep-predicate sweeps — the
// counter must equal a fresh O(E) walk of tf.
func Test_edgeCache_edgeCount(t *testing.T) {
	walk := func(c *edgeCache[string]) int {
		c.mu.RLock()
		defer c.mu.RUnlock()
		n := 0
		for _, heads := range c.tf {
			n += len(heads)
		}
		return n
	}
	check := func(t *testing.T, c *edgeCache[string], want int) {
		t.Helper()
		if got := c.count(); got != want {
			t.Fatalf("count() = %d, want %d", got, want)
		}
		if got := walk(c); got != c.edgeCount {
			t.Fatalf("edgeCount = %d, walk = %d", c.edgeCount, got)
		}
		tails, edges := c.corpusStats()
		if edges != want || tails != len(c.tf) {
			t.Fatalf("corpusStats = (%d, %d), want (%d, %d)", tails, edges, len(c.tf), want)
		}
	}

	t.Run("create paths and deletes agree with the walk", func(t *testing.T) {
		c := newEdgeCache[string](time.Minute, newDictionary[string]())
		exp := time.Now().Add(time.Minute)

		c.addWithExpiration("a", "b", 1, exp) // additive create
		c.addWithExpiration("a", "b", 1, exp) // existing bucket: no change
		c.putWithExpiration("a", "c", 1, exp) // put create
		c.putWithExpiration("a", "c", 2, exp) // in-place replace: no change
		c.addWithExpirationContrib("b", "c", 1, exp, ContribID{1})
		c.addWithExpirationContrib("b", "c", 1, exp, ContribID{1}) // deduped: no change
		c.putWithExpirationHLC("c", "a", 1, exp, hlc.Timestamp{WallNs: 1})
		check(t, c, 4)

		if deleted, _, _ := c.delete("a", "b"); !deleted {
			t.Fatal("delete(a,b) = false")
		}
		if deleted, _, _ := c.delete("a", "b"); deleted {
			t.Fatal("double delete(a,b) = true")
		}
		check(t, c, 3)
	})

	t.Run("dict-nil test caches keep parity", func(t *testing.T) {
		// Without a dict every endpoint resolves to vertexID 0, so all edges
		// collapse into the single (0, 0) bucket — the pre-existing dict-nil
		// degenerate shape. The counter must track that reality (one bucket),
		// not the logical key pairs.
		c := newEdgeCache[string](time.Minute, nil)
		exp := time.Now().Add(time.Minute)
		c.addWithExpiration("a", "b", 1, exp)
		c.addWithExpiration("a", "c", 1, exp)
		check(t, c, 1)
	})

	t.Run("zero-weight flush and keep-predicate sweep decrement", func(t *testing.T) {
		c := newEdgeCache[string](time.Minute, newDictionary[string]())
		live := time.Now().Add(time.Minute)
		dead := time.Now().Add(-time.Minute)

		c.addWithExpiration("a", "b", 1, dead) // fully decayed
		c.addWithExpiration("a", "c", 1, live)
		c.addWithExpiration("d", "e", 1, live)
		check(t, c, 3)

		if removed := c.flush(); removed != 1 {
			t.Fatalf("flush removed %d, want 1", removed)
		}
		check(t, c, 2)

		// Sweep away everything whose tail is "d" via the keep predicate.
		dID, ok := c.dict.lookup("d")
		if !ok {
			t.Fatal("dict.lookup(d) miss")
		}
		zero, dangling := c.flushFunc(func(tail, _ vertexID) bool { return tail != dID }, nil)
		if zero != 0 || dangling != 1 {
			t.Fatalf("flushFunc = (%d, %d), want (0, 1)", zero, dangling)
		}
		check(t, c, 1)
	})
}
