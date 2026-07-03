package search

import (
	"sort"
	"testing"
)

// TestSearchPhrase covers phrase matching over the primary word channel: the
// query's terms must occur at consecutive positions, so an adjacent phrase
// matches while the same terms scattered apart do not.
func TestSearchPhrase(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, WithPositions())
	idx.Index("adjacent", Text("the data set is clean"))   // data@1 set@2 — adjacent
	idx.Index("scattered", Text("the set holds the data")) // set@1 data@4 — apart
	idx.Index("partial", Text("only data here"))           // data only

	t.Run("matches the consecutive phrase, not the scattered terms", func(t *testing.T) {
		got := idsOf(idx.SearchPhrase("data set"))
		if !equalStrings(got, []string{"adjacent"}) {
			t.Fatalf(`SearchPhrase("data set") = %v, want [adjacent]`, got)
		}
	})

	t.Run("order matters: the reversed phrase matches neither document", func(t *testing.T) {
		if got := idx.SearchPhrase("set data"); got != nil {
			t.Fatalf(`SearchPhrase("set data") = %v, want nil`, idsOf(got))
		}
	})

	t.Run("single-term phrase behaves like a term search", func(t *testing.T) {
		got := idsOf(idx.SearchPhrase("data"))
		sort.Strings(got)
		if !equalStrings(got, []string{"adjacent", "partial", "scattered"}) {
			t.Fatalf(`SearchPhrase("data") = %v, want all three`, got)
		}
	})

	t.Run("a missing term matches nothing", func(t *testing.T) {
		if got := idx.SearchPhrase("data absent"); got != nil {
			t.Fatalf("SearchPhrase with absent term = %v, want nil", idsOf(got))
		}
	})

	t.Run("a query with no word terms returns nil", func(t *testing.T) {
		if got := idx.SearchPhrase("   "); got != nil {
			t.Fatalf("SearchPhrase(blank) = %v, want nil", idsOf(got))
		}
	})
}

// TestSearchPhraseRepeatedWord verifies a repeated query word must appear
// repeated and adjacent in the document, exercising a term whose posting
// carries more than one position.
func TestSearchPhraseRepeatedWord(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, WithPositions())
	idx.Index("double", Text("go go rust")) // go@0 go@1
	idx.Index("single", Text("go rust go")) // go@0 go@2 — not adjacent

	got := idsOf(idx.SearchPhrase("go go"))
	if !equalStrings(got, []string{"double"}) {
		t.Fatalf(`SearchPhrase("go go") = %v, want [double]`, got)
	}
}

// TestSearchPhraseDegradesWithoutPositions verifies that an index built
// without WithPositions cannot verify adjacency, so SearchPhrase falls back to
// the AND-intersection: every term present, order unverified.
func TestSearchPhraseDegradesWithoutPositions(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil) // no WithPositions
	idx.Index("adjacent", Text("the data set is clean"))
	idx.Index("scattered", Text("the set holds the data"))
	idx.Index("partial", Text("only data here")) // only one of the two terms

	got := idsOf(idx.SearchPhrase("data set"))
	sort.Strings(got)
	if !equalStrings(got, []string{"adjacent", "scattered"}) {
		t.Fatalf(`SearchPhrase("data set") without positions = %v, want [adjacent scattered] (AND)`, got)
	}
}

// TestSearchPhrasePositionsLifecycle proves positions survive delete and
// re-index with replace semantics: a deleted document's positions leave the
// survivor intact, and re-indexing with a different order updates them so a
// stale phrase no longer matches.
func TestSearchPhrasePositionsLifecycle(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, WithPositions())
	idx.Index("doc1", Text("data set one"))
	idx.Index("doc2", Text("data set two"))

	idx.Delete("doc1")
	if got := idsOf(idx.SearchPhrase("data set")); !equalStrings(got, []string{"doc2"}) {
		t.Fatalf(`SearchPhrase after delete = %v, want [doc2]`, got)
	}

	// Re-index doc2 reversed: the old "data set" adjacency must not linger.
	idx.Index("doc2", Text("set data now"))
	if got := idx.SearchPhrase("data set"); got != nil {
		t.Fatalf(`SearchPhrase("data set") after reversed re-index = %v, want nil`, idsOf(got))
	}
	if got := idsOf(idx.SearchPhrase("set data")); !equalStrings(got, []string{"doc2"}) {
		t.Fatalf(`SearchPhrase("set data") after re-index = %v, want [doc2]`, got)
	}
}

// TestSearchPhraseCJK verifies phrase adjacency over the script-aware analyzer,
// where a CJK run indexes overlapping bigrams as the primary (word) channel: a
// query phrase's bigrams must occur at consecutive positions, exactly as they
// do inside the matching run.
func TestSearchPhraseCJK(t *testing.T) {
	idx := NewInvertedIndex[string, Document](NewScriptAwareAnalyzer(), nil, WithPositions())
	// "データセット" bigrams デー ータ タセ セッ ット appear consecutively here.
	idx.Index("match", Text("データセットを解析する"))
	// The same characters, but セット precedes データ — the phrase's bigrams
	// are not consecutive.
	idx.Index("split", Text("セットのデータを見る"))

	got := idsOf(idx.SearchPhrase("データセット"))
	if !equalStrings(got, []string{"match"}) {
		t.Fatalf(`SearchPhrase("データセット") = %v, want [match]`, got)
	}
}

// TestSearchPhraseTopK covers the bounded-selection sibling: k caps the page,
// accept filters candidates, and k <= 0 or no word terms return nil.
func TestSearchPhraseTopK(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, WithPositions())
	idx.Index("a", Text("data set alpha"))
	idx.Index("b", Text("data set beta data set"))
	idx.Index("c", Text("no phrase here"))

	t.Run("k bounds the page to the top hit", func(t *testing.T) {
		got := idx.SearchPhraseTopK("data set", 1, nil)
		if len(got) != 1 {
			t.Fatalf("SearchPhraseTopK k=1 returned %d results, want 1", len(got))
		}
	})

	t.Run("accept filters a candidate out", func(t *testing.T) {
		got := idsOf(idx.SearchPhraseTopK("data set", 10, func(id string) bool { return id != "b" }))
		sort.Strings(got)
		if !equalStrings(got, []string{"a"}) {
			t.Fatalf("SearchPhraseTopK with accept = %v, want [a]", got)
		}
	})

	t.Run("k <= 0 returns nil", func(t *testing.T) {
		if got := idx.SearchPhraseTopK("data set", 0, nil); got != nil {
			t.Fatalf("SearchPhraseTopK k=0 = %v, want nil", idsOf(got))
		}
	})

	t.Run("no word terms returns nil", func(t *testing.T) {
		if got := idx.SearchPhraseTopK("   ", 10, nil); got != nil {
			t.Fatalf("SearchPhraseTopK blank = %v, want nil", idsOf(got))
		}
	})
}

// TestContainsSortedU32 covers the binary-search helper phrase adjacency relies
// on, including the empty slice and both boundary elements.
func TestContainsSortedU32(t *testing.T) {
	s := []uint32{2, 5, 5, 9}
	cases := []struct {
		v    uint32
		want bool
	}{
		{2, true}, {9, true}, {5, true}, {0, false}, {3, false}, {10, false},
	}
	for _, c := range cases {
		if got := containsSortedU32(s, c.v); got != c.want {
			t.Fatalf("containsSortedU32(%v, %d) = %v, want %v", s, c.v, got, c.want)
		}
	}
	if containsSortedU32(nil, 1) {
		t.Fatal("containsSortedU32(nil, 1) = true, want false")
	}
}
