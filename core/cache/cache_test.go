package cache

import (
	"reflect"
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.Has(tt.args.key); got != tt.want {
				t.Errorf("Has() = %v, want %v", got, tt.want)
			}
		})
	}
}
