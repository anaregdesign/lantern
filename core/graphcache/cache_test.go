package graphcache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

func TestGraph_AddEdge(t *testing.T) {
	v := cache.NewCache[string, string](time.Minute)
	e := newEdgeCache[string](time.Minute, newDictionary[string]())
	type args[S comparable] struct {
		tail S
		head S
		w    float32
	}
	type testCase[S comparable, T any] struct {
		name string
		g    GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraph_AddEdge",
			g: GraphCache[string, string]{
				vertices: v,
				edges:    e,
			},
			args: args[string]{
				tail: "tail",
				head: "head",
				w:    1,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.g.AddEdge(tt.args.tail, tt.args.head, tt.args.w)
		})
	}
}

func TestGraph_AddEdgeWithTTL(t *testing.T) {
	v := cache.NewCache[string, string](time.Minute)
	e := newEdgeCache[string](time.Minute, newDictionary[string]())
	type args[S comparable] struct {
		tail S
		head S
		w    float32
		ttl  time.Duration
	}
	type testCase[S comparable, T any] struct {
		name string
		g    GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraph_AddEdgeWithTTL",
			g: GraphCache[string, string]{
				vertices: v,
				edges:    e,
			},
			args: args[string]{
				tail: "tail",
				head: "head",
				w:    1,
			},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.g.AddEdgeWithTTL(tt.args.tail, tt.args.head, tt.args.w, tt.args.ttl)
		})
	}
}

func TestGraph_PutVertex(t *testing.T) {
	type args[S comparable, T any] struct {
		key   S
		value T
	}
	type testCase[S comparable, T any] struct {
		name string
		g    GraphCache[S, T]
		args args[S, T]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraph_PutVertex",
			g: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.g.PutVertex(tt.args.key, tt.args.value)
		})
	}
}

func TestGraph_PutVertexWithTTL(t *testing.T) {
	type args[S comparable, T any] struct {
		key   S
		value T
		ttl   time.Duration
	}
	type testCase[S comparable, T any] struct {
		name string
		g    GraphCache[S, T]
		args args[S, T]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraph_PutVertexWithTTL",
			g: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value", ttl: time.Minute},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.g.PutVertexWithTTL(tt.args.key, tt.args.value, tt.args.ttl)
		})
	}
}

func TestGraph_GetVertex(t *testing.T) {
	v := cache.NewCache[string, string](time.Minute)
	v.Put("key", "value")

	type args[S comparable] struct {
		key S
	}
	type testCase[S comparable, T any] struct {
		name  string
		g     GraphCache[S, T]
		args  args[S]
		want  T
		want1 bool
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraph_GetVertex",
			g: GraphCache[string, string]{
				vertices: v,
			},
			args:  args[string]{key: "key"},
			want:  "value",
			want1: true,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.g.GetVertex(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetVertex() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetVertex() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestGraph_getWeight(t *testing.T) {
	e := newEdgeCache[string](time.Minute, newDictionary[string]())

	type args[S comparable] struct {
		tail S
		head S
	}
	type testCase[S comparable, T any] struct {
		name  string
		g     GraphCache[S, T]
		args  args[S]
		want  float32
		want1 bool
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraph_getWeight",
			g: GraphCache[string, string]{
				edges: e,
			},
			args:  args[string]{tail: "tail", head: "head"},
			want:  0,
			want1: false,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.g.GetWeight(tt.args.tail, tt.args.head)
			if got != tt.want {
				t.Errorf("GetWeight() = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetWeight() = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestGraphCache_AddEdge(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
		w    float32
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_AddEdge",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
				edges:    newEdgeCache[string](time.Minute, newDictionary[string]()),
			},
			args: args[string]{tail: "tail", head: "head", w: 0},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.AddEdge(tt.args.tail, tt.args.head, tt.args.w)
		})
	}
}

func TestGraphCache_AddEdgeWithExpiration(t *testing.T) {
	type args[S comparable] struct {
		tail       S
		head       S
		w          float32
		expiration time.Time
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_AddEdgeWithExpiration",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
				edges:    newEdgeCache[string](time.Minute, newDictionary[string]()),
			},
			args: args[string]{tail: "tail", head: "head", w: 0, expiration: time.Now()},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.AddEdgeWithExpiration(tt.args.tail, tt.args.head, tt.args.w, tt.args.expiration)
		})
	}
}

func TestGraphCache_AddEdgeWithTTL(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
		w    float32
		ttl  time.Duration
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_AddEdgeWithTTL",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
				edges:    newEdgeCache[string](time.Minute, newDictionary[string]()),
			},
			args: args[string]{tail: "tail", head: "head", w: 0, ttl: time.Minute},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.AddEdgeWithTTL(tt.args.tail, tt.args.head, tt.args.w, tt.args.ttl)
		})
	}
}

func TestGraphCache_PutVertex(t *testing.T) {
	type args[S comparable, T any] struct {
		key   S
		value T
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S, T]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_PutVertex",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.PutVertex(tt.args.key, tt.args.value)
		})
	}
}

func TestGraphCache_PutVertexWithExpiration(t *testing.T) {
	type args[S comparable, T any] struct {
		key        S
		value      T
		expiration time.Time
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S, T]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_PutVertexWithExpiration",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value", expiration: time.Now()},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.PutVertexWithExpiration(tt.args.key, tt.args.value, tt.args.expiration)
		})
	}
}

// TestGraphCache_PutVertexWithExpiration_BornExpired pins #698: a client write
// whose expiration is already in the past is dead on arrival and must not be
// stored. Storing it would only inflate the vertex / dictionary / prefix /
// search high-water until the next GC sweep, for zero observable benefit
// (GetVertex already hides expired entries). An overwrite-to-expired must still
// drop the existing live entry. The full secondary-index wiring is enabled so
// the test catches a dead key leaking into the prefix or search index too.
func TestGraphCache_PutVertexWithExpiration_BornExpired(t *testing.T) {
	newCache := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Minute)
		c.EnablePrefixIndex(func(s string) string { return s })
		c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) })
		return c
	}
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	// assertNoTrace fails when any of the four structures still references key.
	assertNoTrace := func(t *testing.T, c *GraphCache[string, string], key, term string) {
		t.Helper()
		if _, ok := c.GetVertex(key); ok {
			t.Fatalf("GetVertex(%q) is visible, want absent", key)
		}
		if got := c.VertexCount(); got != 0 {
			t.Fatalf("VertexCount() = %d, want 0", got)
		}
		if n := c.CountByPrefix(key); n != 0 {
			t.Fatalf("CountByPrefix(%q) = %d, want 0 (prefix index retained dead key)", key, n)
		}
		if hits := c.SearchVertices(term, 10, ""); len(hits) != 0 {
			t.Fatalf("SearchVertices(%q) = %d hits, want 0 (search index retained dead key)", term, len(hits))
		}
	}

	t.Run("NewKeyNotStored", func(t *testing.T) {
		c := newCache()
		c.PutVertexWithExpiration("k", "alpha", past)
		assertNoTrace(t, c, "k", "alpha")
	})

	t.Run("OverwriteExpiresExisting", func(t *testing.T) {
		c := newCache()
		c.PutVertexWithExpiration("k", "alpha", future)
		if _, ok := c.GetVertex("k"); !ok {
			t.Fatal("setup: live vertex was not stored")
		}
		c.PutVertexWithExpiration("k", "beta", past)
		assertNoTrace(t, c, "k", "alpha")
	})

	t.Run("BatchSkipsBornExpiredOnly", func(t *testing.T) {
		c := newCache()
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "live", Value: "v", Expiration: future},
			{Key: "dead", Value: "v", Expiration: past},
		})
		if got := c.VertexCount(); got != 1 {
			t.Fatalf("VertexCount() = %d, want 1 (only the live item)", got)
		}
		if _, ok := c.GetVertex("dead"); ok {
			t.Fatal("batch stored the born-expired item 'dead'")
		}
		if _, ok := c.GetVertex("live"); !ok {
			t.Fatal("batch dropped the live item 'live'")
		}
	})
}

func TestGraphCache_PutVertexWithTTL(t *testing.T) {
	type args[S comparable, T any] struct {
		key   S
		value T
		ttl   time.Duration
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S, T]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_PutVertexWithTTL",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value", ttl: time.Minute},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.PutVertexWithTTL(tt.args.key, tt.args.value, tt.args.ttl)
		})
	}
}

func TestGraphCache_GetVertex(t *testing.T) {
	type args[S comparable] struct {
		key S
	}
	type testCase[S comparable, T any] struct {
		name  string
		c     GraphCache[S, T]
		args  args[S]
		want  T
		want1 bool
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_GetVertex",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string]{key: "key"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.c.GetVertex(tt.args.key)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetVertex() got = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetVertex() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestGraphCache_flush(t *testing.T) {
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
	}
	tests := []testCase[string, string]{
		// TODO: Add test cases.
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.flush()
		})
	}
}

func TestGraphCache_getWeight(t *testing.T) {
	type args[S comparable] struct {
		tail S
		head S
	}
	type testCase[S comparable, T any] struct {
		name  string
		c     *GraphCache[S, T]
		args  args[S]
		want  float32
		want1 bool
	}
	mk := func() *GraphCache[string, string] {
		c := NewGraphCache[string, string](time.Minute)
		c.AddEdgeWithExpiration("tail", "head", 1, time.Now().Add(time.Minute))
		return c
	}
	tests := []testCase[string, string]{
		{
			name:  "TestGraphCache_getWeight",
			c:     mk(),
			args:  args[string]{tail: "tail", head: "head"},
			want:  1,
			want1: true,
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			got, got1 := tt.c.GetWeight(tt.args.tail, tt.args.head)
			if got != tt.want {
				t.Errorf("GetWeight() = %v, want %v", got, tt.want)
			}
			if got1 != tt.want1 {
				t.Errorf("GetWeight() = %v, want %v", got1, tt.want1)
			}
		})
	}
}

func TestGraphCache_watch(t *testing.T) {
	type args struct {
		ctx      context.Context
		interval time.Duration
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args
	}
	tests := []testCase[string, string]{
		// TODO: Add test cases.
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.Watch(tt.args.ctx, tt.args.interval)
		})
	}
}

func TestNewGraphCache(t *testing.T) {
	type args struct {
		defaultTTL time.Duration
	}
	type testCase[S comparable, T any] struct {
		name string
		args args
		want *GraphCache[S, T]
	}
	tests := []testCase[string, string]{
		// TODO: Add test cases.
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			if got := NewGraphCache[string, string](tt.args.defaultTTL); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewGraphCache() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGraphCache_DeleteVertex(t *testing.T) {
	g := NewGraphCache[string, string](time.Minute)
	g.PutVertexWithTTL("a", "A", 60*time.Second)

	type args[S comparable] struct {
		key S
	}
	type testCase[S comparable, T any] struct {
		name string
		c    *GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_DeleteVertex",
			c:    g,
			args: args[string]{key: "a"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.DeleteVertex(tt.args.key)
		})
	}
}

func TestGraphCache_PointReadsLockFree(t *testing.T) {
	// GetVertex / GetWeight / GetEdgeDetail are lock-free point reads as of
	// #740: they no longer take GraphCache.mu. These stress tests must be run
	// under -race (the package suite is) to prove the lock removal is safe.

	t.Run("GetVertex under concurrent Put/Delete/Flush returns only live values", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		const keys = 32
		key := func(i int) string { return fmt.Sprintf("k-%d", i) }
		// Invariant under test: a vertex's value, when present, always equals
		// its key. A reader that observes any other (e.g. torn) value would
		// indicate a data race the race detector might miss.
		for i := 0; i < keys; i++ {
			c.PutVertex(key(i), key(i))
		}

		var stop atomic.Bool
		var wg sync.WaitGroup

		writer := func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed)))
			for !stop.Load() {
				i := rng.Intn(keys)
				switch rng.Intn(3) {
				case 0:
					c.PutVertex(key(i), key(i))
				case 1:
					c.DeleteVertex(key(i))
				default:
					c.PutVertexWithTTL(key(i), key(i), time.Nanosecond) // born-expired churn
				}
			}
		}
		flusher := func() {
			defer wg.Done()
			for !stop.Load() {
				c.vertices.Flush()
				c.flush()
			}
		}
		reader := func() {
			defer wg.Done()
			for !stop.Load() {
				for i := 0; i < keys; i++ {
					if v, ok := c.GetVertex(key(i)); ok && v != key(i) {
						t.Errorf("GetVertex(%s) returned stale/torn value %q", key(i), v)
						return
					}
				}
			}
		}

		for w := 0; w < 4; w++ {
			wg.Add(1)
			go writer(w + 1)
		}
		wg.Add(1)
		go flusher()
		for r := 0; r < 4; r++ {
			wg.Add(1)
			go reader()
		}
		time.Sleep(150 * time.Millisecond)
		stop.Store(true)
		wg.Wait()
	})

	t.Run("edge point read never resolves a recycled endpoint id to a foreign edge", func(t *testing.T) {
		// ABA hazard (#740): GetEdgeDetail resolves (tail,head) to dict ids,
		// then reads the edge map. If a concurrent delete frees an endpoint id
		// and a decoy edge recycles it, an unpinned read could surface the
		// decoy's weight under the real edge's key. pinBoth must prevent that.
		c := NewGraphCache[string, int](time.Minute)
		const realWeight = 1
		const decoyWeight = 99
		exp := time.Now().Add(time.Hour)
		c.AddEdgeWithExpiration("T", "H", realWeight, exp)

		var stop atomic.Bool
		var wg sync.WaitGroup

		// Churner: repeatedly delete T/H and their edge (freeing their dict
		// ids), create decoy vertices+edges (weight 99) to recycle the freed
		// ids, then restore the real T->H edge.
		wg.Add(1)
		go func() {
			defer wg.Done()
			i := 0
			for !stop.Load() {
				c.DeleteEdge("T", "H")
				c.DeleteVertex("T")
				c.DeleteVertex("H")
				d1 := fmt.Sprintf("decoy-a-%d", i)
				d2 := fmt.Sprintf("decoy-b-%d", i)
				c.AddEdgeWithExpiration(d1, d2, decoyWeight, exp)
				c.DeleteEdge(d1, d2)
				c.DeleteVertex(d1)
				c.DeleteVertex(d2)
				c.AddEdgeWithExpiration("T", "H", realWeight, exp)
				i++
			}
		}()

		// Readers: a successful read of (T,H) must never report the decoy
		// weight. Either the real weight or not-found is acceptable.
		for r := 0; r < 4; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for !stop.Load() {
					if w, _, ok := c.GetEdgeDetail("T", "H"); ok && w == decoyWeight {
						t.Errorf("GetEdgeDetail(T,H) returned decoy weight %v from a recycled id", w)
						return
					}
				}
			}()
		}

		time.Sleep(200 * time.Millisecond)
		stop.Store(true)
		wg.Wait()
	})
}

func TestGraphCache_DeleteEdge(t *testing.T) {
	g := NewGraphCache[string, string](time.Minute)
	g.AddEdgeWithTTL("a", "b", 3.14, 60*time.Second)
	type args[S comparable] struct {
		tail S
		head S
	}
	type testCase[S comparable, T any] struct {
		name string
		c    *GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_DeleteEdge",
			c:    g,
			args: args[string]{tail: "a", head: "b"},
		},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			tt.c.DeleteEdge(tt.args.tail, tt.args.head)
		})
	}
}

func TestGraphCache_NeighborContextCancelled(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.PutVertex("a", 1)
	c.PutVertex("b", 2)
	c.AddEdge("a", "b", 1.0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.NeighborContext(ctx, "a", 5, 10, false, false, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// Asserts that NeighborWithExpirationsContext returns expiration data
// aligned to the same (tail, head) pairs present in the returned graph,
// so handlers do not need a second per-edge cache lookup.
func TestGraphCache_NeighborWithExpirationsContext_ReturnsAlignedMap(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.PutVertex("a", 1)
	c.PutVertex("b", 2)
	c.PutVertex("c", 3)

	expAB := time.Now().Add(30 * time.Second).Truncate(time.Microsecond)
	expAC := time.Now().Add(45 * time.Second).Truncate(time.Microsecond)
	c.AddEdgeWithExpiration("a", "b", 1.0, expAB)
	c.AddEdgeWithExpiration("a", "c", 2.0, expAC)

	g, exps, err := c.NeighborWithExpirationsContext(context.Background(), "a", 2, 10, false, false, nil)
	if err != nil {
		t.Fatalf("NeighborWithExpirationsContext: %v", err)
	}
	if exps == nil {
		t.Fatal("expirations map is nil")
	}

	for tail, heads := range g.Edges {
		row, ok := exps[tail]
		if !ok {
			t.Errorf("missing expirations row for %q", tail)
			continue
		}
		for head := range heads {
			got, ok := row[head]
			if !ok {
				t.Errorf("missing expiration for edge %q->%q", tail, head)
				continue
			}
			if got.IsZero() {
				t.Errorf("expiration for edge %q->%q is zero", tail, head)
			}
		}
	}

	if got, want := exps["a"]["b"].Unix(), expAB.Unix(); got != want {
		t.Errorf("exp a->b unix = %d, want %d", got, want)
	}
	if got, want := exps["a"]["c"].Unix(), expAC.Unix(); got != want {
		t.Errorf("exp a->c unix = %d, want %d", got, want)
	}
}

// Smoke test: the plain Neighbor / NeighborContext paths still work.
func TestGraphCache_NeighborContext_StillWorksAfterRefactor(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.PutVertex("a", 1)
	c.PutVertex("b", 2)
	c.AddEdge("a", "b", 1.0)

	g, err := c.NeighborContext(context.Background(), "a", 2, 10, false, false, nil)
	if err != nil {
		t.Fatalf("NeighborContext: %v", err)
	}
	if len(g.Edges["a"]) != 1 {
		t.Errorf("len(g.Edges[a]) = %d, want 1", len(g.Edges["a"]))
	}
}

// TestDictRefcount_VertexPutIsIdempotent verifies that re-inserting the same
// vertex key does not inflate the dictionary refcount. After N PutWithExpiration
// calls for the same key the dict must hold exactly one reference.
func TestDictRefcount_VertexPutIsIdempotent(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	for i := 0; i < 50; i++ {
		c.PutVertexWithExpiration("k", i, time.Now().Add(time.Minute))
	}
	if got := c.dict.len(); got != 1 {
		t.Fatalf("dict.len after 50 puts of same key = %d, want 1", got)
	}
	if got := c.vertices.Count(); got != 1 {
		t.Fatalf("vertices.Count = %d, want 1", got)
	}
}

// TestDictRefcount_PutEdgeIdempotent verifies that PutEdge on the same
// (tail, head) does not inflate dict refcounts. After N PutEdge calls the
// dict still holds exactly the references owned by the vertex slots (1 per
// endpoint) plus the single edge (1 per endpoint) = 2 net refs per endpoint.
func TestDictRefcount_PutEdgeIdempotent(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	for i := 0; i < 25; i++ {
		c.PutEdgeWithExpiration("a", "b", float32(i+1), time.Now().Add(time.Minute))
	}
	// Two distinct vertex slots ("a", "b"); each interned once for the vertex
	// cache entry and once for the single edge endpoint = 2 ids in the dict.
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after 25 PutEdge same (a,b) = %d, want 2", got)
	}
	// Each vertex id holds refcount 2 (vertex slot + edge endpoint).
	id, ok := c.dict.lookup("a")
	if !ok {
		t.Fatalf("dict.lookup(a) missing")
	}
	if got := c.dict.refcount[id]; got != 2 {
		t.Fatalf("refcount(a) = %d, want 2", got)
	}
}

// TestDictRefcount_AddEdgeAdditiveBumps verifies that AddEdge on the same
// (tail, head) keeps the edge-slot refcount at exactly 1 per endpoint
// (additive contributions on the same edge are not new edges).
func TestDictRefcount_AddEdgeAdditiveBumps(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	for i := 0; i < 10; i++ {
		c.AddEdgeWithExpiration("x", "y", 1, time.Now().Add(time.Minute))
	}
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after 10 AddEdge same (x,y) = %d, want 2", got)
	}
	id, _ := c.dict.lookup("x")
	if got := c.dict.refcount[id]; got != 2 {
		t.Fatalf("refcount(x) = %d, want 2 (vertex slot + 1 edge endpoint)", got)
	}
}

// TestDictRefcount_DeleteReleases verifies vertex Delete drops the vertex
// slot's reference (through SetOnEvict) and edge Delete drops both endpoint
// references.
func TestDictRefcount_DeleteReleases(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	c.PutVertexWithExpiration("v", 1, time.Now().Add(time.Minute))
	if got := c.dict.len(); got != 1 {
		t.Fatalf("dict.len after add = %d, want 1", got)
	}
	c.DeleteVertex("v")
	if got := c.dict.len(); got != 0 {
		t.Fatalf("dict.len after DeleteVertex = %d, want 0", got)
	}

	c.AddEdgeWithExpiration("a", "b", 1, time.Now().Add(time.Minute))
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after AddEdge = %d, want 2", got)
	}
	c.DeleteEdge("a", "b")
	// Endpoints still live as vertex slots (refcount 1 each).
	if got := c.dict.len(); got != 2 {
		t.Fatalf("dict.len after DeleteEdge = %d, want 2", got)
	}
	c.DeleteVertex("a")
	c.DeleteVertex("b")
	if got := c.dict.len(); got != 0 {
		t.Fatalf("dict.len after deleting both vertices = %d, want 0", got)
	}
}

// TestDictRefcount_InsertDeleteInvariant fuzzes random Add/Delete sequences
// and asserts dict.len() always tracks the distinct live keys.
func TestDictRefcount_InsertDeleteInvariant(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	r := rand.New(rand.NewSource(42))
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}

	for step := 0; step < 500; step++ {
		k1 := keys[r.Intn(len(keys))]
		k2 := keys[r.Intn(len(keys))]
		switch r.Intn(5) {
		case 0:
			c.PutVertexWithExpiration(k1, step, time.Now().Add(time.Minute))
		case 1:
			c.AddEdgeWithExpiration(k1, k2, 1, time.Now().Add(time.Minute))
		case 2:
			c.PutEdgeWithExpiration(k1, k2, 1, time.Now().Add(time.Minute))
		case 3:
			c.DeleteEdge(k1, k2)
		case 4:
			c.DeleteVertex(k1)
		}

		// Invariant: every key with refcount > 0 must be one of:
		//   - present in the vertex cache (vertex-slot ref), or
		//   - referenced as an endpoint of a live edge.
		// And every distinct live key (vertex OR endpoint) must own an id.
		live := make(map[string]struct{})
		for _, k := range keys {
			if c.vertices.Has(k) {
				live[k] = struct{}{}
			}
		}
		for tail, heads := range c.edges.snapshotTF() {
			live[tail] = struct{}{}
			for head := range heads {
				live[head] = struct{}{}
			}
		}
		if got := c.dict.len(); got != len(live) {
			t.Fatalf("step %d: dict.len=%d, distinct live keys=%d (keys=%v)",
				step, got, len(live), live)
		}
	}
}

// TestDictRefcount_FullExpiryFreesAll verifies that after every entry expires
// and the GC sweep runs, the dict is empty and every id ever allocated has
// been returned to the freelist.
func TestDictRefcount_FullExpiryFreesAll(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	const N = 20
	short := 30 * time.Millisecond
	deadline := time.Now().Add(short)

	for i := 0; i < N; i++ {
		k := fmt.Sprintf("v%d", i)
		c.PutVertexWithExpiration(k, i, deadline)
	}
	for i := 0; i < N; i++ {
		for j := 0; j < 3; j++ {
			tail := fmt.Sprintf("v%d", i)
			head := fmt.Sprintf("v%d", (i+j+1)%N)
			c.AddEdgeWithExpiration(tail, head, 1, deadline)
		}
	}

	maxID := c.dict.len()
	if maxID == 0 {
		t.Fatalf("setup error: dict empty before expiry")
	}

	time.Sleep(short + 80*time.Millisecond)

	// Reproduce the Watch tick: expire vertex entries, sweep dangling edges,
	// flush expired edges.
	c.vertices.Flush()
	c.flush()
	c.edges.flush()

	if got := c.dict.len(); got != 0 {
		t.Fatalf("dict.len after full expiry = %d, want 0", got)
	}
	if got := len(c.dict.free); got != maxID {
		t.Fatalf("dict.free count after full expiry = %d, want %d (every id should be reusable)",
			got, maxID)
	}
}

// TestDictRefcount_HotEdgeFastPathStability verifies that the existing-edge
// fast path in addWithExpiration does not touch dict refcounts. After 100k
// writes to the same edge, both endpoints must still have refcount = 2
// (vertex slot + 1 edge endpoint), the dict size must still be 2, and the
// edge's accumulated sum must be exactly 100k.
func TestDictRefcount_HotEdgeFastPathStability(t *testing.T) {
	c := NewGraphCache[string, int](time.Minute)
	// First write goes through the slow path and interns both endpoints.
	exp := time.Now().Add(time.Minute)
	c.AddEdgeWithExpiration("hot-tail", "hot-head", 1, exp)
	tailID, _ := c.dict.lookup("hot-tail")
	headID, _ := c.dict.lookup("hot-head")
	wantTail := c.dict.refcount[tailID]
	wantHead := c.dict.refcount[headID]
	wantLen := c.dict.len()

	const n = 100_000
	for i := 0; i < n; i++ {
		c.AddEdgeWithExpiration("hot-tail", "hot-head", 1, exp)
	}

	if got := c.dict.refcount[tailID]; got != wantTail {
		t.Fatalf("tail refcount drifted: got %d, want %d (fast path must not touch dict)", got, wantTail)
	}
	if got := c.dict.refcount[headID]; got != wantHead {
		t.Fatalf("head refcount drifted: got %d, want %d (fast path must not touch dict)", got, wantHead)
	}
	if got := c.dict.len(); got != wantLen {
		t.Fatalf("dict.len drifted: got %d, want %d", got, wantLen)
	}
	if got, ok := c.GetWeight("hot-tail", "hot-head"); !ok || got != float32(n+1) {
		t.Fatalf("edge sum after %d adds: got %v ok=%v, want %d", n+1, got, ok, n+1)
	}
}

// TestPutEdgeWithExpiration_Atomic asserts that a reader observing an edge
// that is being continually replaced via PutEdgeWithExpiration NEVER sees a
// transient "missing" state. The previous service-level implementation
// performed DeleteEdge + AddEdgeWithExpiration as two separate cache calls,
// which exposed a window where concurrent GetEdge readers observed a
// spurious NotFound. Run with -race to also catch any introduced races.
func TestPutEdgeWithExpiration_Atomic(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	exp := time.Now().Add(time.Minute)
	// Seed an edge so the first reader iterations are guaranteed to see it.
	c.AddEdgeWithExpiration("T", "H", 1, exp)

	const writers = 4
	const readers = 16
	const writesPerWriter = 2000

	var wg sync.WaitGroup
	var missing int64
	var reads int64
	stop := make(chan struct{})

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _, ok := c.GetEdgeDetail("T", "H")
				if !ok {
					atomic.AddInt64(&missing, 1)
				}
				atomic.AddInt64(&reads, 1)
			}
		}()
	}

	var writerWG sync.WaitGroup
	for w := 0; w < writers; w++ {
		writerWG.Add(1)
		go func(w int) {
			defer writerWG.Done()
			for i := 0; i < writesPerWriter; i++ {
				c.PutEdgeWithExpiration("T", "H", float32(w*1000+i+1), exp)
			}
		}(w)
	}
	writerWG.Wait()
	close(stop)
	wg.Wait()

	if missing != 0 {
		t.Fatalf("PutEdgeWithExpiration leaked NotFound to readers: %d of %d reads observed a missing edge",
			missing, reads)
	}
}
func TestAddEdgeWithExpirationContrib_Dedup(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	id := ContribID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 0, 0, 0, 0, 0, 0, 0, 42}

	expiration := time.Now().Add(time.Minute)
	if !c.AddEdgeWithExpirationContrib("a", "b", 1.5, expiration, id) {
		t.Fatalf("first add: want applied=true")
	}
	if c.AddEdgeWithExpirationContrib("a", "b", 1.5, expiration, id) {
		t.Fatalf("second add: want applied=false (dedup)")
	}
	if c.AddEdgeWithExpirationContrib("a", "b", 1.5, expiration, id) {
		t.Fatalf("third add: want applied=false (dedup)")
	}

	got, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("edge a→b missing")
	}
	if got != 1.5 {
		t.Errorf("weight: got %v, want 1.5 (one contribution despite three Add calls)", got)
	}
}

// Zero ContribID disables dedup — local (non-replicated) Add semantics
// must remain additive when callers do not opt in.
func TestAddEdgeWithExpirationContrib_ZeroIDStillAdditive(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	for i := 0; i < 3; i++ {
		if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, ContribID{}) {
			t.Fatalf("call %d: want applied=true (zero contrib never dedups)", i)
		}
	}
	got, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("edge missing")
	}
	if got != 3.0 {
		t.Errorf("weight: got %v, want 3.0 (three contributions, no dedup)", got)
	}
}

// Distinct ContribIDs must accumulate — only the *same* identity dedups.
func TestAddEdgeWithExpirationContrib_DistinctIDsAccumulate(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	id1 := ContribID{0: 1}
	id2 := ContribID{0: 2}
	id3 := ContribID{0: 3}

	if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, id1) {
		t.Fatalf("id1: want applied=true")
	}
	if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, id2) {
		t.Fatalf("id2: want applied=true")
	}
	if !c.AddEdgeWithExpirationContrib("a", "b", 1.0, expiration, id3) {
		t.Fatalf("id3: want applied=true")
	}

	got, _ := c.GetWeight("a", "b")
	if got != 3.0 {
		t.Errorf("weight: got %v, want 3.0", got)
	}
}

// PutVertexWithExpirationHLC enforces LWW: a strictly-older HLC must not
// overwrite a newer one. Equal HLCs apply (idempotent re-apply remains a
// no-op semantically because the value is the same).
func TestPutVertexWithExpirationHLC_LWW(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	if !c.PutVertexWithExpirationHLC("v", "first", expiration, newer) {
		t.Fatalf("first put: want applied=true")
	}
	if c.PutVertexWithExpirationHLC("v", "stale", expiration, older) {
		t.Fatalf("older put: want applied=false (LWW)")
	}
	got, ok := c.GetVertex("v")
	if !ok {
		t.Fatalf("vertex missing")
	}
	if got != "first" {
		t.Errorf("value: got %q, want %q (newer HLC must win)", got, "first")
	}
}

// PutEdgeWithExpirationHLC enforces LWW on the edge weight.
func TestPutEdgeWithExpirationHLC_LWW(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	expiration := time.Now().Add(time.Minute)

	older := hlc.Timestamp{WallNs: 1000}
	newer := hlc.Timestamp{WallNs: 2000}

	if !c.PutEdgeWithExpirationHLC("a", "b", 2.5, expiration, newer) {
		t.Fatalf("first put: want applied=true")
	}
	if c.PutEdgeWithExpirationHLC("a", "b", 9.9, expiration, older) {
		t.Fatalf("older put: want applied=false (LWW)")
	}
	got, ok := c.GetWeight("a", "b")
	if !ok {
		t.Fatalf("edge missing")
	}
	if got != 2.5 {
		t.Errorf("weight: got %v, want 2.5 (newer HLC must win)", got)
	}
}

// TestVertexHLCCount_BoundedByLiveSet pins issue #700: the vertexHLC map must
// be swept back to (approximately) zero after replicated vertices expire and
// the GC flush runs. Before the fix, the map grew monotonically because
// sweepStaleVertexHLCLocked was never called.
func TestVertexHLCCount_BoundedByLiveSet(t *testing.T) {
	const n = 50
	c := NewGraphCache[string, string](time.Minute)
	ts := hlc.Timestamp{WallNs: 1}

	// Apply n distinct replicated vertices with short TTL so they expire
	// immediately after the test moves on.
	exp := time.Now().Add(50 * time.Millisecond)
	for i := range n {
		key := fmt.Sprintf("hlc-%d", i)
		if !c.PutVertexWithExpirationHLC(key, "v", exp, ts) {
			t.Fatalf("put %s: want applied=true", key)
		}
	}
	if got := c.VertexHLCCount(); got != n {
		t.Errorf("VertexHLCCount after puts = %d, want %d", got, n)
	}

	// Let the TTLs expire, then run a flush tick to sweep stale entries.
	time.Sleep(100 * time.Millisecond)
	c.vertices.Flush() // evict expired vertices from the inner cache
	c.flush()          // sweepStaleVertexHLCLocked runs here under c.mu

	if got := c.VertexHLCCount(); got != 0 {
		t.Errorf("VertexHLCCount after flush = %d, want 0 (map must track live set, not all-time set)", got)
	}
}

// TestVertexHLCHighWater_SurvivesDrain pins the confirm-the-phenomenon metric
// for #727: VertexHLCHighWater must retain the per-cycle peak len(vertexHLC)
// even after sweepStaleVertexHLCLocked drains the map back to zero. The
// instantaneous VertexHLCCount reads low right after a sweep (Go drains the
// entries), but the bucket array is sized by the high-water and never shrinks
// — so a low count paired with a large high-water is the fingerprint that
// explains the retained heap in the born-expired ttl_churn firehose.
func TestVertexHLCHighWater_SurvivesDrain(t *testing.T) {
	const n = 50
	c := NewGraphCache[string, string](time.Minute)
	ts := hlc.Timestamp{WallNs: 1}

	// No sweep has run yet: the high-water starts at zero.
	if got := c.VertexHLCHighWater(); got != 0 {
		t.Errorf("VertexHLCHighWater before any sweep = %d, want 0", got)
	}

	// Apply n distinct replicated vertices with a short TTL so they all expire
	// before the sweep, mirroring the born-expired ttl_churn firehose.
	exp := time.Now().Add(50 * time.Millisecond)
	for i := range n {
		key := fmt.Sprintf("hlc-%d", i)
		if !c.PutVertexWithExpirationHLC(key, "v", exp, ts) {
			t.Fatalf("put %s: want applied=true", key)
		}
	}
	if got := c.VertexHLCCount(); got != n {
		t.Errorf("VertexHLCCount after puts = %d, want %d", got, n)
	}

	// Let the TTLs expire, then flush so the sweep records the peak and drains.
	time.Sleep(100 * time.Millisecond)
	c.vertices.Flush() // evict expired vertices from the inner cache
	c.flush()          // sweepStaleVertexHLCLocked: records high-water, then drains

	// The instantaneous count is back to zero, but the high-water retains the
	// peak — this is exactly the pairing the bench snapshot must surface.
	if got := c.VertexHLCCount(); got != 0 {
		t.Errorf("VertexHLCCount after flush = %d, want 0", got)
	}
	if got := c.VertexHLCHighWater(); got != n {
		t.Errorf("VertexHLCHighWater after drain = %d, want %d (peak must survive the sweep)", got, n)
	}

	// A later, smaller cycle must not lower the high-water (monotonic).
	if !c.PutVertexWithExpirationHLC("hlc-late", "v", time.Now().Add(50*time.Millisecond), hlc.Timestamp{WallNs: 2}) {
		t.Fatalf("late put: want applied=true")
	}
	time.Sleep(100 * time.Millisecond)
	c.vertices.Flush()
	c.flush()
	if got := c.VertexHLCHighWater(); got != n {
		t.Errorf("VertexHLCHighWater after smaller cycle = %d, want %d (must be monotonic non-decreasing)", got, n)
	}
}

// TestVertexHLCShrink_ReleasesBucketArray pins the #727 remediation: after a
// large born-expired churn drains vertexHLC back to near-zero, the sweep must
// reallocate the map so Go can reclaim the oversized bucket array — a plain
// delete loop leaves it pinned at the high-water size. A map's capacity is not
// observable, so we assert the map header pointer changes across the draining
// sweep (proof the backing store was rebuilt) for a large drain, and does NOT
// change for a small one (the shrink floor must keep tiny maps off the copy
// path). The high-water peak is preserved either way for observability.
func TestVertexHLCShrink_ReleasesBucketArray(t *testing.T) {
	t.Run("LargeDrainReallocates", func(t *testing.T) {
		const n = 2 * vertexHLCShrinkFloor // comfortably above the shrink floor
		c := NewGraphCache[string, string](time.Minute)
		ts := hlc.Timestamp{WallNs: 1}

		exp := time.Now().Add(50 * time.Millisecond)
		for i := range n {
			if !c.PutVertexWithExpirationHLC(fmt.Sprintf("hlc-%d", i), "v", exp, ts) {
				t.Fatalf("put %d: want applied=true", i)
			}
		}
		if got := c.VertexHLCCount(); got != n {
			t.Fatalf("VertexHLCCount after puts = %d, want %d", got, n)
		}
		before := reflect.ValueOf(c.vertexHLC).Pointer()

		// Expire every vertex, then sweep: the live set collapses to zero, which
		// must trigger the reallocation that releases the oversized bucket array.
		time.Sleep(100 * time.Millisecond)
		c.vertices.Flush()
		c.flush()

		if got := c.VertexHLCCount(); got != 0 {
			t.Errorf("VertexHLCCount after drain = %d, want 0", got)
		}
		if got := c.VertexHLCHighWater(); got != n {
			t.Errorf("VertexHLCHighWater after drain = %d, want %d (peak must survive)", got, n)
		}
		if after := reflect.ValueOf(c.vertexHLC).Pointer(); after == before {
			t.Errorf("vertexHLC backing store was not reallocated after a %d→0 drain; the oversized bucket array is still pinned (#727)", n)
		}
	})

	t.Run("SmallDrainKeepsBackingStore", func(t *testing.T) {
		const n = 8 // far below vertexHLCShrinkFloor
		c := NewGraphCache[string, string](time.Minute)
		ts := hlc.Timestamp{WallNs: 1}

		exp := time.Now().Add(50 * time.Millisecond)
		for i := range n {
			if !c.PutVertexWithExpirationHLC(fmt.Sprintf("hlc-%d", i), "v", exp, ts) {
				t.Fatalf("put %d: want applied=true", i)
			}
		}
		before := reflect.ValueOf(c.vertexHLC).Pointer()

		time.Sleep(100 * time.Millisecond)
		c.vertices.Flush()
		c.flush()

		if got := c.VertexHLCCount(); got != 0 {
			t.Errorf("VertexHLCCount after drain = %d, want 0", got)
		}
		if after := reflect.ValueOf(c.vertexHLC).Pointer(); after != before {
			t.Errorf("small map (n=%d, below the shrink floor) was reallocated; the floor must keep tiny maps off the copy path", n)
		}
	})
}

// TestSearchIndexStats returns (0, 0) when the search index is disabled and
// the actual term/doc counts when enabled.
func TestSearchIndexStats(t *testing.T) {
	c := NewGraphCache[string, string](time.Minute)
	terms, docs := c.SearchIndexStats()
	if terms != 0 || docs != 0 {
		t.Errorf("disabled: got (%d, %d), want (0, 0)", terms, docs)
	}

	c.EnableSearchIndex(func(v string) search.Document { return search.Text(v) })
	exp := time.Now().Add(time.Hour)
	c.PutVertexWithExpiration("key:1", "hello world", exp)
	c.PutVertexWithExpiration("key:2", "foo bar", exp)

	terms, docs = c.SearchIndexStats()
	if docs != 2 {
		t.Errorf("enabled: docs = %d, want 2", docs)
	}
	if terms == 0 {
		t.Errorf("enabled: terms = 0, want > 0")
	}
}

// ContribID.IsZero distinguishes the zero value (no identity) from a
// populated one with a low byte — guards against accidental "all bytes
// must be set" implementations.

// TestGraphCache_DictRefcountStableAcrossExpiredOverwrite guards the #739 fix:
// overwriting a vertex whose slot has expired but not yet been flushed must NOT
// re-intern the key. The old !Has() probe reported the lingering expired slot as
// absent, so the overwrite re-interned the key and inflated its dictionary
// refcount past the single live slot it represents — a slow leak under a churn
// of short-TTL keys rewritten before the GC sweep runs. The physical-presence
// upsert keeps refcount pinned at one for both the single and batch put paths.
func TestGraphCache_DictRefcountStableAcrossExpiredOverwrite(t *testing.T) {
	setupExpiredSlot := func(t *testing.T, c *GraphCache[string, string]) vertexID {
		// putVertexLocked stores unconditionally (no born-expired gate), so a
		// past expiration yields exactly the lingering "expired, interned once,
		// not yet flushed" state a live put reaches once its TTL elapses.
		c.mu.Lock()
		c.putVertexLocked("k", "v1", time.Now().Add(-time.Minute))
		c.mu.Unlock()
		id, ok := c.dict.lookup("k")
		if !ok {
			t.Fatal("setup: key not interned")
		}
		if got := c.dict.refcount[id]; got != 1 {
			t.Fatalf("setup: refcount=%d, want 1", got)
		}
		return id
	}
	assertStable := func(t *testing.T, c *GraphCache[string, string], id vertexID) {
		if got := c.dict.refcount[id]; got != 1 {
			t.Fatalf("refcount after expired overwrite = %d, want 1", got)
		}
		if got := c.dict.len(); got != 1 {
			t.Fatalf("dict.len after expired overwrite = %d, want 1", got)
		}
		if got, ok := c.GetVertex("k"); !ok || got != "v2" {
			t.Fatalf("GetVertex after overwrite: got=%q ok=%v want v2 true", got, ok)
		}
	}

	t.Run("single put", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		id := setupExpiredSlot(t, c)
		c.PutVertexWithExpiration("k", "v2", time.Now().Add(time.Minute))
		assertStable(t, c, id)
	})

	t.Run("batch put", func(t *testing.T) {
		c := NewGraphCache[string, string](time.Minute)
		id := setupExpiredSlot(t, c)
		c.PutVerticesWithExpiration([]VertexItem[string, string]{
			{Key: "k", Value: "v2", Expiration: time.Now().Add(time.Minute)},
		})
		assertStable(t, c, id)
	})
}
