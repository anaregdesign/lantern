package pq

import (
	"reflect"
	"testing"
)

func TestSortableMap_Top(t *testing.T) {
	type args struct {
		k int
	}
	type testCase[S comparable, T Number] struct {
		name string
		m    SortableMap[S, T]
		args args
		want SortableMap[S, T]
	}
	tests := []testCase[string, float64]{
		{
			name: "TestSortableMap_Top",
			m: SortableMap[string, float64]{
				"one":   1,
				"two":   2,
				"three": 3,
				"four":  4,
				"five":  5,
			},
			args: args{
				k: 10,
			},
			want: SortableMap[string, float64]{
				"five":  5,
				"four":  4,
				"three": 3,
				"two":   2,
				"one":   1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Top(tt.args.k); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Top() = %v, want %v", got, tt.want)
			}
		})
	}
}
