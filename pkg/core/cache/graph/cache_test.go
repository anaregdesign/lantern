package graph

import (
	"context"
	"github.com/anaregdesign/lantern/pkg/core/cache"
	"github.com/anaregdesign/lantern/pkg/core/graph"
	"reflect"
	"testing"
	"time"
)

func TestGraph_AddEdge(t *testing.T) {
	v := cache.NewCache[string, string](time.Minute)
	e := newEdgeCache[string](time.Minute)
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.g.AddEdge(tt.args.tail, tt.args.head, tt.args.w)
		})
	}
}

func TestGraph_AddEdgeWithTTL(t *testing.T) {
	v := cache.NewCache[string, string](time.Minute)
	e := newEdgeCache[string](time.Minute)
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.g.AddEdgeWithTTL(tt.args.tail, tt.args.head, tt.args.w, tt.args.ttl)
		})
	}
}

func TestGraph_AddVertex(t *testing.T) {
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
			name: "TestGraph_AddVertex",
			g: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.g.PutVertex(tt.args.key, tt.args.value)
		})
	}
}

func TestGraph_AddVertexWithTTL(t *testing.T) {
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
			name: "TestGraph_AddVertexWithTTL",
			g: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value", ttl: time.Minute},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.g.AddVertexWithTTL(tt.args.key, tt.args.value, tt.args.ttl)
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
	for _, tt := range tests {
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
	e := newEdgeCache[string](time.Minute)

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
	for _, tt := range tests {
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
				edges:    newEdgeCache[string](time.Minute),
			},
			args: args[string]{tail: "tail", head: "head", w: 0},
		},
	}
	for _, tt := range tests {
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
				edges:    newEdgeCache[string](time.Minute),
			},
			args: args[string]{tail: "tail", head: "head", w: 0, expiration: time.Now()},
		},
	}
	for _, tt := range tests {
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
				edges:    newEdgeCache[string](time.Minute),
			},
			args: args[string]{tail: "tail", head: "head", w: 0, ttl: time.Minute},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.AddEdgeWithTTL(tt.args.tail, tt.args.head, tt.args.w, tt.args.ttl)
		})
	}
}

func TestGraphCache_AddVertex(t *testing.T) {
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
			name: "TestGraphCache_AddVertex",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.PutVertex(tt.args.key, tt.args.value)
		})
	}
}

func TestGraphCache_AddVertexWithExpiration(t *testing.T) {
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
			name: "TestGraphCache_AddVertexWithExpiration",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value", expiration: time.Now()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.AddVertexWithExpiration(tt.args.key, tt.args.value, tt.args.expiration)
		})
	}
}

func TestGraphCache_AddVertexWithTTL(t *testing.T) {
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
			name: "TestGraphCache_AddVertexWithTTL",
			c: GraphCache[string, string]{
				vertices: cache.NewCache[string, string](time.Minute),
			},
			args: args[string, string]{key: "key", value: "value", ttl: time.Minute},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.AddVertexWithTTL(tt.args.key, tt.args.value, tt.args.ttl)
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
	for _, tt := range tests {
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

func TestGraphCache_Neighbor(t *testing.T) {
	type args[S comparable] struct {
		seed  S
		step  int
		k     int
		tfidf bool
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S]
		want *graph.Graph[S, T]
	}
	tests := []testCase[string, string]{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Neighbor(tt.args.seed, tt.args.step, tt.args.k, tt.args.tfidf); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Neighbor() = %v, want %v", got, tt.want)
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
	for _, tt := range tests {
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
		c     GraphCache[S, T]
		args  args[S]
		want  float32
		want1 bool
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_getWeight",
			c: GraphCache[string, string]{
				edges: &edgeCache[string]{
					tf: map[string]map[string]*weight{
						"tail": {
							"head": &weight{
								values: []weightValue{
									{
										value:      1,
										expiration: time.Now().Add(time.Minute),
									},
								},
							},
						},
					},
				},
			},
			args:  args[string]{tail: "tail", head: "head"},
			want:  1,
			want1: true,
		},
	}
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewGraphCache[string, string](tt.args.defaultTTL); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewGraphCache() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGraphCache_DeleteVertex(t *testing.T) {
	g := NewGraphCache[string, string](time.Minute)
	g.AddVertexWithTTL("a", "A", 60*time.Second)

	type args[S comparable] struct {
		key S
	}
	type testCase[S comparable, T any] struct {
		name string
		c    GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_DeleteVertex",
			c:    *g,
			args: args[string]{key: "a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.DeleteVertex(tt.args.key)
		})
	}
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
		c    GraphCache[S, T]
		args args[S]
	}
	tests := []testCase[string, string]{
		{
			name: "TestGraphCache_DeleteEdge",
			c:    *g,
			args: args[string]{tail: "a", head: "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.c.DeleteEdge(tt.args.tail, tt.args.head)
		})
	}
}
