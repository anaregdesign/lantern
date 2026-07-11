package pq

import (
	"math/rand"
	"reflect"
	"strconv"
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
			name: "k_greater_than_len_returns_all",
			m: SortableMap[string, float64]{
				"one":   1,
				"two":   2,
				"three": 3,
				"four":  4,
				"five":  5,
			},
			args: args{k: 10},
			want: SortableMap[string, float64]{
				"five": 5, "four": 4, "three": 3, "two": 2, "one": 1,
			},
		},
		{
			name: "k_equal_to_len_returns_all",
			m: SortableMap[string, float64]{
				"a": 1, "b": 2, "c": 3,
			},
			args: args{k: 3},
			want: SortableMap[string, float64]{
				"a": 1, "b": 2, "c": 3,
			},
		},
		{
			name: "k_smaller_picks_largest",
			m: SortableMap[string, float64]{
				"a": 1, "b": 5, "c": 3, "d": 4, "e": 2,
			},
			args: args{k: 2},
			want: SortableMap[string, float64]{"b": 5, "d": 4},
		},
		{
			name: "k_one_picks_max",
			m:    SortableMap[string, float64]{"a": 1, "b": 9, "c": 3},
			args: args{k: 1},
			want: SortableMap[string, float64]{"b": 9},
		},
		{
			name: "k_zero_returns_empty",
			m:    SortableMap[string, float64]{"a": 1, "b": 2},
			args: args{k: 0},
			want: SortableMap[string, float64]{},
		},
		{
			name: "k_negative_returns_empty",
			m:    SortableMap[string, float64]{"a": 1, "b": 2},
			args: args{k: -1},
			want: SortableMap[string, float64]{},
		},
		{
			name: "empty_map",
			m:    SortableMap[string, float64]{},
			args: args{k: 3},
			want: SortableMap[string, float64]{},
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

// TestSortableMap_Top_TieBoundary makes sure that when many entries share the
// boundary priority value, the top-k still contains exactly k entries and the
// kept entries are all >= the discarded ones.
func TestSortableMap_Top_TieBoundary(t *testing.T) {
	m := SortableMap[int, int]{
		1: 5, 2: 5, 3: 5, 4: 5, 5: 5,
		6: 1, 7: 1, 8: 1,
	}
	got := m.Top(3)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	for k, v := range got {
		if v != 5 {
			t.Errorf("entry %d has priority %d, want 5", k, v)
		}
	}
}

func TestSortableMap_StableTieBreak(t *testing.T) {
	const runs = 100
	keys := []string{"delta", "bravo", "alpha", "charlie"}
	want := SortableMap[string, int]{"alpha": 5, "bravo": 5}

	for run := 0; run < runs; run++ {
		order := append([]string(nil), keys...)
		rand.New(rand.NewSource(int64(run))).Shuffle(len(order), func(i, j int) {
			order[i], order[j] = order[j], order[i]
		})
		m := NewSortableMap[string, int]()
		for _, key := range order {
			m[key] = 5
		}

		for _, got := range []SortableMap[string, int]{
			m.TopStable(2, func(a, b string) bool { return a < b }),
			m.BottomStable(2, func(a, b string) bool { return a < b }),
		} {
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("run %d stable tie selection = %v, want %v", run, got, want)
			}
		}
	}
}

// TestSortableMap_Top_LargeRandom cross-checks the new O(N log k) algorithm
// against a straightforward sort-based reference on a randomized input. Only
// the priority *values* of the top-k matter (ties may pick different keys).
func TestSortableMap_Top_LargeRandom(t *testing.T) {
	const (
		n = 2000
		k = 17
	)
	r := rand.New(rand.NewSource(42))
	m := make(SortableMap[int, float64], n)
	values := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		v := r.Float64()
		m[i] = v
		values = append(values, v)
	}

	got := m.Top(k)
	if len(got) != k {
		t.Fatalf("len(got) = %d, want %d", len(got), k)
	}

	// Sort values descending and take the k-th largest as the threshold.
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] > values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
	threshold := values[k-1]
	for key, v := range got {
		if v < threshold {
			t.Errorf("entry %d has priority %v, below threshold %v", key, v, threshold)
		}
	}
}

// TestSortableMap_Bottom is the directional counterpart of
// TestSortableMap_Top: it pins that Bottom keeps the SMALLEST k while sharing
// Top's passthrough (len <= k returns the receiver) and k <= 0 empty
// semantics. Illuminate relies on this lower-tail selection for per-hop
// pruning when the Objective is MINIMIZE (#560).
func TestSortableMap_Bottom(t *testing.T) {
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
			name: "k_greater_than_len_returns_all",
			m: SortableMap[string, float64]{
				"one":   1,
				"two":   2,
				"three": 3,
				"four":  4,
				"five":  5,
			},
			args: args{k: 10},
			want: SortableMap[string, float64]{
				"five": 5, "four": 4, "three": 3, "two": 2, "one": 1,
			},
		},
		{
			name: "k_equal_to_len_returns_all",
			m: SortableMap[string, float64]{
				"a": 1, "b": 2, "c": 3,
			},
			args: args{k: 3},
			want: SortableMap[string, float64]{
				"a": 1, "b": 2, "c": 3,
			},
		},
		{
			name: "k_smaller_picks_smallest",
			m: SortableMap[string, float64]{
				"a": 1, "b": 5, "c": 3, "d": 4, "e": 2,
			},
			args: args{k: 2},
			want: SortableMap[string, float64]{"a": 1, "e": 2},
		},
		{
			name: "k_one_picks_min",
			m:    SortableMap[string, float64]{"a": 7, "b": 9, "c": 3},
			args: args{k: 1},
			want: SortableMap[string, float64]{"c": 3},
		},
		{
			name: "k_zero_returns_empty",
			m:    SortableMap[string, float64]{"a": 1, "b": 2},
			args: args{k: 0},
			want: SortableMap[string, float64]{},
		},
		{
			name: "k_negative_returns_empty",
			m:    SortableMap[string, float64]{"a": 1, "b": 2},
			args: args{k: -1},
			want: SortableMap[string, float64]{},
		},
		{
			name: "empty_map",
			m:    SortableMap[string, float64]{},
			args: args{k: 3},
			want: SortableMap[string, float64]{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Bottom(tt.args.k); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Bottom() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSortableMap_Bottom_TieBoundary mirrors the Top tie test: when many
// entries share the boundary priority the bottom-k still contains exactly k
// entries and every kept entry sits at the low tail.
func TestSortableMap_Bottom_TieBoundary(t *testing.T) {
	m := SortableMap[int, int]{
		1: 1, 2: 1, 3: 1, 4: 1, 5: 1,
		6: 5, 7: 5, 8: 5,
	}
	got := m.Bottom(3)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	for k, v := range got {
		if v != 1 {
			t.Errorf("entry %d has priority %d, want 1", k, v)
		}
	}
}

// TestSortableMap_Bottom_LargeRandom cross-checks Bottom against a sort-based
// reference on randomized input: every kept value must be <= the k-th smallest.
func TestSortableMap_Bottom_LargeRandom(t *testing.T) {
	const (
		n = 2000
		k = 17
	)
	r := rand.New(rand.NewSource(42))
	m := make(SortableMap[int, float64], n)
	values := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		v := r.Float64()
		m[i] = v
		values = append(values, v)
	}

	got := m.Bottom(k)
	if len(got) != k {
		t.Fatalf("len(got) = %d, want %d", len(got), k)
	}

	// Sort values ascending and take the k-th smallest as the threshold.
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
	threshold := values[k-1]
	for key, v := range got {
		if v > threshold {
			t.Errorf("entry %d has priority %v, above threshold %v", key, v, threshold)
		}
	}
}

func BenchmarkSortableMap_Top(b *testing.B) {
	const n = 10_000
	r := rand.New(rand.NewSource(1))
	m := make(SortableMap[int, float64], n)
	for i := 0; i < n; i++ {
		m[i] = r.Float64()
	}
	for _, k := range []int{1, 10, 100, 1000} {
		b.Run("k="+strconv.Itoa(k), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = m.Top(k)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
