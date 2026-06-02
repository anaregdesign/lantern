package set

import (
	"fmt"
	"github.com/anaregdesign/lantern/pkg/core/collection/slice"
	"github.com/anaregdesign/lantern/pkg/core/model/function"
	"reflect"
	"testing"
)

func TestNewSet(t *testing.T) {
	type testCase[T comparable] struct {
		name string
		want *Set[T]
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			want: &Set[int]{
				set: make(map[int]struct{}),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewSet[int](); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Add(t *testing.T) {
	type args[T comparable] struct {
		value T
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: make(map[int]struct{}),
			},
			args: args[int]{value: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.Add(tt.args.value)
		})
	}
}

func TestSet_AllMatch(t *testing.T) {
	type args[T comparable] struct {
		predicate function.Predicate[T]
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
		want bool
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{predicate: func(i int) bool { return i%2 == 0 }},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.AllMatch(tt.args.predicate); got != tt.want {
				t.Errorf("AllMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_AnyMatch(t *testing.T) {
	type args[T comparable] struct {
		predicate function.Predicate[T]
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
		want bool
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{predicate: func(i int) bool { return i%2 == 0 }},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.AnyMatch(tt.args.predicate); got != tt.want {
				t.Errorf("AnyMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Clear(t *testing.T) {
	type testCase[T comparable] struct {
		name string
		s    Set[T]
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.Clear()
		})
	}
}

func TestSet_Contains(t *testing.T) {
	type args[T comparable] struct {
		value T
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
		want bool
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{value: 1},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Has(tt.args.value); got != tt.want {
				t.Errorf("Has() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Filter(t *testing.T) {
	type args[T comparable] struct {
		predicate function.Predicate[T]
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{predicate: func(i int) bool { return i%2 == 0 }},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.Filter(tt.args.predicate)
		})
	}
}

func TestSet_ForEach(t *testing.T) {
	type args[T comparable] struct {
		consumer function.Consumer[T]
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{consumer: func(i int) { fmt.Println(i) }},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.ForEach(tt.args.consumer)
		})
	}
}

func TestSet_NoneMatch(t *testing.T) {
	type args[T comparable] struct {
		predicate function.Predicate[T]
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
		want bool
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{predicate: func(i int) bool { return i%2 == 0 }},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.NoneMatch(tt.args.predicate); got != tt.want {
				t.Errorf("NoneMatch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Reduce(t *testing.T) {
	type args[T comparable] struct {
		operator function.Operator[T]
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
		want T
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{operator: func(i, j int) int { return i + j }},
			want: 15,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Reduce(tt.args.operator); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reduce() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Remove(t *testing.T) {
	type args[T comparable] struct {
		value T
	}
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		args args[T]
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			args: args[int]{value: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.s.Remove(tt.args.value)
		})
	}
}

func TestSet_Size(t *testing.T) {
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		want int
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			want: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Size(); got != tt.want {
				t.Errorf("Size() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Values(t *testing.T) {
	type testCase[T comparable] struct {
		name string
		s    Set[T]
		want []T
	}
	tests := []testCase[int]{
		{
			name: "valid case",
			s: Set[int]{
				set: map[int]struct{}{
					1: {},
					2: {},
					3: {},
					4: {},
					5: {},
				},
			},
			want: []int{1, 2, 3, 4, 5},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.s.Values()
			for _, v := range tt.want {
				if !slice.Contains(got, v) {
					t.Errorf("Values() = %v, don't contains %v", got, v)
				}
			}
		})
	}
}
