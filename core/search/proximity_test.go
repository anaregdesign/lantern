package search

import "testing"

// TestProximityBoostRanksTightMatchFirst isolates the proximity boost: two
// documents with identical length and term frequencies, differing only in how
// far apart the query terms sit, so the boost is the sole tie-breaker and the
// adjacent document must rank first.
func TestProximityBoostRanksTightMatchFirst(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
	idx.Index("adjacent", Text("alpha quick fox beta gamma delta"))  // quick@1 fox@2
	idx.Index("scattered", Text("quick alpha beta gamma delta fox")) // quick@0 fox@5

	res := idx.Search("quick fox")
	if len(res) != 2 {
		t.Fatalf(`Search("quick fox") returned %d results, want 2`, len(res))
	}
	if res[0].ID != "adjacent" {
		t.Fatalf("proximity boost did not rank the adjacent doc first: %+v", res)
	}
	if res[0].Score <= res[1].Score {
		t.Fatalf("adjacent score %.4f not above scattered %.4f", res[0].Score, res[1].Score)
	}
}

// TestProximityBoostInertWithoutPositions verifies the boost needs positions:
// the same two documents score identically when the index tracks none, so the
// OR-union ranking is unchanged.
func TestProximityBoostInertWithoutPositions(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID) // no WithPositions
	idx.Index("adjacent", Text("alpha quick fox beta gamma delta"))
	idx.Index("scattered", Text("quick alpha beta gamma delta fox"))

	res := idx.Search("quick fox")
	if len(res) != 2 {
		t.Fatalf(`Search("quick fox") returned %d results, want 2`, len(res))
	}
	// Identical BM25 statistics and no proximity signal: the scores tie exactly.
	if res[0].Score != res[1].Score {
		t.Fatalf("scores differ without positions: %+v", res)
	}
}

// TestProximityWeightScalesBoost pins WithProximityWeight to the boost formula:
// weight 0 makes the boost inert even under WithPositions (the two docs tie on
// pure BM25), and the bonus the adjacent document earns is exactly linear in the
// weight, so the harness can read the ranking off a swept weight. The adjacent
// doc's window is 1 (span 0) and the scattered doc's is 5 (span 4), so at weight
// w the bonuses are w/(0+1) and w/(4+1) and the tie-break gap is w - w/5 =
// 0.8*w — measured here at two weights to fix the slope.
func TestProximityWeightScalesBoost(t *testing.T) {
	build := func(w float64) []Result[string] {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions(), WithProximityWeight(w))
		idx.Index("adjacent", Text("alpha quick fox beta gamma delta"))  // quick@1 fox@2
		idx.Index("scattered", Text("quick alpha beta gamma delta fox")) // quick@0 fox@5
		return idx.Search("quick fox")
	}

	t.Run("ZeroDisables", func(t *testing.T) {
		res := build(0)
		if len(res) != 2 {
			t.Fatalf("want 2 results, got %d", len(res))
		}
		if res[0].Score != res[1].Score {
			t.Fatalf("weight 0 must tie like no boost, got %+v", res)
		}
	})

	t.Run("LinearInWeight", func(t *testing.T) {
		gap := func(res []Result[string]) float64 {
			byID := map[string]float64{res[0].ID: res[0].Score, res[1].ID: res[1].Score}
			return byID["adjacent"] - byID["scattered"]
		}
		g1 := gap(build(0.3))
		g2 := gap(build(0.6))
		if g1 <= 0 {
			t.Fatalf("adjacent not boosted above scattered at weight 0.3: gap %.4f", g1)
		}
		// The bonus is linear in the weight, so doubling it doubles the gap.
		if diff := g2 - 2*g1; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("gap not linear in weight: g(0.3)=%.6f g(0.6)=%.6f", g1, g2)
		}
		// Slope check: the gap is 0.8*w by construction (windows 1 vs 5).
		if diff := g1 - 0.8*0.3; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("gap %.6f at weight 0.3 not 0.8*w = %.6f", g1, 0.8*0.3)
		}
	})
}

// TestProximityBoostSingleTermNoOp verifies a single-term query has no pair to
// measure, so the boost is a no-op and ranking stays pure BM25 (the shorter,
// denser document still wins on its own merits).
func TestProximityBoostSingleTermNoOp(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
	idx.Index("dense", Text("quick quick fox"))
	idx.Index("sparse", Text("quick alpha beta gamma"))

	res := idx.Search("quick")
	if len(res) != 2 || res[0].ID != "dense" {
		t.Fatalf(`Search("quick") = %+v, want "dense" first on term frequency`, res)
	}
}

// TestProximityBoostTopK verifies the boost also reorders SearchTopK, which
// applies it before bounded selection so a tight match can claim a scarce slot.
func TestProximityBoostTopK(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
	idx.Index("adjacent", Text("alpha quick fox beta gamma delta"))
	idx.Index("scattered", Text("quick alpha beta gamma delta fox"))

	res := idx.SearchTopK("quick fox", 1, nil)
	if len(res) != 1 || res[0].ID != "adjacent" {
		t.Fatalf(`SearchTopK("quick fox", 1) = %+v, want [adjacent]`, res)
	}
}

// TestSmallestWindow covers the smallest-range sweep: adjacency, spread, three
// lists, choosing the closest occurrences, a single list, and an empty list.
func TestSmallestWindow(t *testing.T) {
	tests := []struct {
		name  string
		lists [][]uint64
		want  int
	}{
		{"two adjacent", [][]uint64{{1}, {2}}, 1},
		{"two apart", [][]uint64{{0}, {5}}, 5},
		{"three consecutive", [][]uint64{{0}, {1}, {2}}, 2},
		{"picks the closest occurrences", [][]uint64{{0, 10}, {9}}, 1},
		{"single list has zero width", [][]uint64{{3, 7}}, 0},
		{"an empty list has no window", [][]uint64{{1}, {}}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := smallestWindow(tt.lists); got != tt.want {
				t.Fatalf("smallestWindow(%v) = %d, want %d", tt.lists, got, tt.want)
			}
		})
	}
}
