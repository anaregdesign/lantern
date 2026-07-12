package search

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// TestSearchMatchModes covers the three modes on a word-only analyzer, where
// every match is a word match so the modes reduce to textbook boolean logic.
func TestSearchMatchModes(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
	idx.Index("all", Text("alpha beta gamma")) // alpha + beta
	idx.Index("one", Text("alpha delta"))      // alpha only
	idx.Index("other", Text("beta epsilon"))   // beta only
	idx.Index("none", Text("zeta eta"))        // neither

	const q = "alpha beta"

	t.Run("MatchAny unions", func(t *testing.T) {
		got := idsOf(idx.SearchMatch(q, MatchOptions{Mode: MatchAny}))
		sort.Strings(got)
		if !equalStrings(got, []string{"all", "one", "other"}) {
			t.Fatalf("MatchAny = %v, want [all one other]", got)
		}
	})
	t.Run("MatchAll intersects", func(t *testing.T) {
		got := idsOf(idx.SearchMatch(q, MatchOptions{Mode: MatchAll}))
		if !equalStrings(got, []string{"all"}) {
			t.Fatalf("MatchAll = %v, want [all]", got)
		}
	})
	t.Run("MatchMinShould 2 == MatchAll here", func(t *testing.T) {
		got := idsOf(idx.SearchMatch(q, MatchOptions{Mode: MatchMinShould, MinShouldMatch: 2}))
		if !equalStrings(got, []string{"all"}) {
			t.Fatalf("msm(2) = %v, want [all]", got)
		}
	})
	t.Run("MatchMinShould 1 == MatchAny", func(t *testing.T) {
		got := idsOf(idx.SearchMatch(q, MatchOptions{Mode: MatchMinShould, MinShouldMatch: 1}))
		sort.Strings(got)
		if !equalStrings(got, []string{"all", "one", "other"}) {
			t.Fatalf("msm(1) = %v, want [all one other]", got)
		}
	})
	t.Run("Search equals SearchMatch MatchAny", func(t *testing.T) {
		a := idsOf(idx.Search(q))
		b := idsOf(idx.SearchMatch(q, MatchOptions{}))
		sort.Strings(a)
		sort.Strings(b)
		if !equalStrings(a, b) {
			t.Fatalf("Search=%v SearchMatch(zero)=%v", a, b)
		}
	})
}

// TestMatchModeEquivalences property-checks the boolean identities on random
// word-only corpora: msm(1) ≡ MatchAny, msm(#terms) ≡ MatchAll, and MatchAll ≡
// MatchAny filtered to documents that hold every distinct query term.
func TestMatchModeEquivalences(t *testing.T) {
	rng := rand.New(rand.NewSource(890))
	vocab := []string{"a", "b", "c", "d", "e"}
	for trial := 0; trial < 60; trial++ {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		docWords := map[string]map[string]struct{}{}
		nDocs := 5 + rng.Intn(40)
		for i := 0; i < nDocs; i++ {
			nw := 1 + rng.Intn(4)
			words := make([]string, nw)
			ws := make(map[string]struct{}, nw)
			for w := range words {
				word := vocab[rng.Intn(len(vocab))]
				words[w] = word
				ws[word] = struct{}{}
			}
			id := fmt.Sprintf("d%02d", i)
			docWords[id] = ws
			idx.Index(id, Text(strings.Join(words, " ")))
		}
		nq := 1 + rng.Intn(3)
		qwords := make([]string, nq)
		distinct := map[string]struct{}{}
		for w := range qwords {
			word := vocab[rng.Intn(len(vocab))]
			qwords[w] = word
			distinct[word] = struct{}{}
		}
		query := strings.Join(qwords, " ")
		numTerms := len(distinct)

		anySet := idSet(idx.SearchMatch(query, MatchOptions{Mode: MatchAny}))
		allSet := idSet(idx.SearchMatch(query, MatchOptions{Mode: MatchAll}))
		msm1 := idSet(idx.SearchMatch(query, MatchOptions{Mode: MatchMinShould, MinShouldMatch: 1}))
		msmN := idSet(idx.SearchMatch(query, MatchOptions{Mode: MatchMinShould, MinShouldMatch: numTerms}))

		if !sameSet(msm1, anySet) {
			t.Fatalf("trial %d q=%q: msm(1) %v != MatchAny %v", trial, query, msm1, anySet)
		}
		if !sameSet(msmN, allSet) {
			t.Fatalf("trial %d q=%q: msm(%d) %v != MatchAll %v", trial, query, numTerms, msmN, allSet)
		}
		wantAll := map[string]struct{}{}
		for id := range anySet {
			covers := true
			for term := range distinct {
				if _, ok := docWords[id][term]; !ok {
					covers = false
					break
				}
			}
			if covers {
				wantAll[id] = struct{}{}
			}
		}
		if !sameSet(allSet, wantAll) {
			t.Fatalf("trial %d q=%q: MatchAll %v != any-filtered %v", trial, query, allSet, wantAll)
		}
	}
}

// TestSearchMatchTopK covers the bounded-selection sibling under a match mode:
// a full page fills when one exists, k bounds it, accept filters, and k <= 0
// returns nil.
func TestSearchMatchTopK(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
	idx.Index("all1", Text("alpha beta x"))
	idx.Index("all2", Text("alpha beta y z"))
	idx.Index("one", Text("alpha only"))

	t.Run("MatchAll fills the page", func(t *testing.T) {
		got := idsOf(idx.SearchMatchTopK("alpha beta", 10, nil, MatchOptions{Mode: MatchAll}))
		sort.Strings(got)
		if !equalStrings(got, []string{"all1", "all2"}) {
			t.Fatalf("MatchAll TopK = %v, want [all1 all2]", got)
		}
	})
	t.Run("k bounds the page", func(t *testing.T) {
		if got := idx.SearchMatchTopK("alpha beta", 1, nil, MatchOptions{Mode: MatchAll}); len(got) != 1 {
			t.Fatalf("k=1 returned %d results, want 1", len(got))
		}
	})
	t.Run("accept filters within the mode", func(t *testing.T) {
		got := idsOf(idx.SearchMatchTopK("alpha beta", 10, func(id string) bool { return id != "all1" }, MatchOptions{Mode: MatchAll}))
		if !equalStrings(got, []string{"all2"}) {
			t.Fatalf("accept-filtered MatchAll = %v, want [all2]", got)
		}
	})
	t.Run("k<=0 returns nil", func(t *testing.T) {
		if got := idx.SearchMatchTopK("alpha", 0, nil, MatchOptions{Mode: MatchAll}); got != nil {
			t.Fatalf("k=0 = %v, want nil", idsOf(got))
		}
	})
}

// TestSearchMatchModesCJK verifies coverage counts the CJK bigrams (which live
// on the word channel), so MatchAll over a CJK query requires all of its
// bigrams — the Lucene CJKAnalyzer + AND behavior — while MatchAny still
// surfaces a document sharing only some.
func TestSearchMatchModesCJK(t *testing.T) {
	idx := NewInvertedIndex[string, Document](NewScriptAwareAnalyzer(), nil, compareStringID)
	idx.Index("both", Text("データセット"))    // デー ータ タセ セッ ット
	idx.Index("partial", Text("データベース")) // shares デー ータ only

	if got := idsOf(idx.SearchMatch("データセット", MatchOptions{Mode: MatchAll})); !equalStrings(got, []string{"both"}) {
		t.Fatalf(`MatchAll("データセット") = %v, want [both]`, got)
	}
	if got := idsOf(idx.SearchMatch("データセット", MatchOptions{Mode: MatchAny})); len(got) != 2 {
		t.Fatalf(`MatchAny("データセット") = %v, want both documents`, got)
	}
}

// TestCoverageThreshold covers the threshold resolution: MatchAny requires
// nothing, MatchAll requires every word term, and MatchMinShould clamps to
// [1, numWords]; a query with no word terms requires nothing.
func TestCoverageThreshold(t *testing.T) {
	cases := []struct {
		name     string
		mode     MatchMode
		msm      int
		numWords int
		want     int
	}{
		{"any requires nothing", MatchAny, 0, 3, 0},
		{"all requires every term", MatchAll, 0, 3, 3},
		{"all with no words", MatchAll, 0, 0, 0},
		{"msm exact", MatchMinShould, 2, 3, 2},
		{"msm clamps up to 1", MatchMinShould, 0, 3, 1},
		{"msm clamps down to numWords", MatchMinShould, 5, 3, 3},
		{"msm with no words", MatchMinShould, 2, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := coverageThreshold(MatchOptions{Mode: c.mode, MinShouldMatch: c.msm}, c.numWords)
			if got != c.want {
				t.Fatalf("coverageThreshold = %d, want %d", got, c.want)
			}
		})
	}
}

// idSet collects result IDs into a set for order-independent comparison.
func idSet(results []Result[string]) map[string]struct{} {
	s := make(map[string]struct{}, len(results))
	for _, r := range results {
		s[r.ID] = struct{}{}
	}
	return s
}

// sameSet reports whether two ID sets are equal.
func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
