package search

import "testing"

// TestProximityBoostRanksTightMatchFirst isolates the proximity boost: two
// documents with identical length and term frequencies, differing only in how
// far apart the query terms sit, so the boost is the sole tie-breaker and the
// adjacent document must rank first.
func TestProximityBoostRanksTightMatchFirst(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, WithPositions())
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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil) // no WithPositions
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

// TestProximityBoostSingleTermNoOp verifies a single-term query has no pair to
// measure, so the boost is a no-op and ranking stays pure BM25 (the shorter,
// denser document still wins on its own merits).
func TestProximityBoostSingleTermNoOp(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, WithPositions())
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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, WithPositions())
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
		lists [][]uint32
		want  int
	}{
		{"two adjacent", [][]uint32{{1}, {2}}, 1},
		{"two apart", [][]uint32{{0}, {5}}, 5},
		{"three consecutive", [][]uint32{{0}, {1}, {2}}, 2},
		{"picks the closest occurrences", [][]uint32{{0, 10}, {9}}, 1},
		{"single list has zero width", [][]uint32{{3, 7}}, 0},
		{"an empty list has no window", [][]uint32{{1}, {}}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := smallestWindow(tt.lists); got != tt.want {
				t.Fatalf("smallestWindow(%v) = %d, want %d", tt.lists, got, tt.want)
			}
		})
	}
}
