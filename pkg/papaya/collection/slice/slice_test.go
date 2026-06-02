package slice

import (
	"context"
	"github.com/anaregdesign/lantern/pkg/papaya/model/function"
	"reflect"
	"testing"
)

var ctx = context.Background()

func TestForEach(t *testing.T) {
	var a [32]int
	for i := 0; i < len(a); i++ {
		a[i] = i
	}

	t.Run("valid case", func(t *testing.T) {
		ForEach(ctx, a[:], func(i int) {
			if a[i] != i {
				t.Errorf("ForEach() = %v, want %v", a[i], i)
			}
		})
	})
}

func TestMap(t *testing.T) {
	var a [32]int
	for i := 0; i < len(a); i++ {
		a[i] = i
	}
	t.Run("valid case", func(t *testing.T) {
		b := Map(ctx, a[:], func(i int) int {
			return i * 2
		})

		for i := 0; i < len(b); i++ {
			if b[i] != a[i]*2 {
				t.Errorf("Map() = %v, want %v", b[i], a[i]*2)
			}
		}
	})
}

func TestReduce(t *testing.T) {
	add := func(a, b int) int { return a + b }
	type args[T any] struct {
		ctx      context.Context
		slice    []T
		operator function.Operator[T]
	}
	type testCase[T any] struct {
		name string
		args args[T]
		want T
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			args: args[int]{
				ctx:      ctx,
				slice:    []int{1, 2, 3, 4, 5},
				operator: add,
			},
			want: 15,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Reduce(tt.args.ctx, tt.args.slice, tt.args.operator); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reduce() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilter(t *testing.T) {
	type args[T any] struct {
		ctx       context.Context
		slice     []T
		predicate function.Predicate[T]
	}
	type testCase[T any] struct {
		name string
		args args[T]
		want []T
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			args: args[int]{
				ctx:   ctx,
				slice: []int{1, 2, 3, 4, 5},
				predicate: func(i int) bool {
					return i%2 == 0
				},
			},
			want: []int{2, 4},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Filter(tt.args.ctx, tt.args.slice, tt.args.predicate); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContains(t *testing.T) {
	type args[T comparable] struct {
		slice   []T
		element T
	}
	type testCase[T comparable] struct {
		name string
		args args[T]
		want bool
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			args: args[int]{
				slice:   []int{1, 2, 3, 4, 5},
				element: 3,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Contains(tt.args.slice, tt.args.element); got != tt.want {
				t.Errorf("Has() = %v, want %v", got, tt.want)
			}
		})
	}
}
