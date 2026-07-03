package search

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// TestWithinEdits covers the bounded Levenshtein check at distances 0/1/2,
// including insert/delete/substitute, a transposition (which costs two edits),
// and multibyte (accented and CJK) runes so distance is counted per rune.
func TestWithinEdits(t *testing.T) {
	r := func(s string) []rune { return []rune(s) }
	cases := []struct {
		a, b string
		max  int
		want bool
	}{
		{"search", "search", 0, true},
		{"search", "serch", 1, true},   // delete 'a'
		{"search", "searchh", 1, true}, // insert 'h'
		{"search", "xearch", 1, true},  // substitute s->x
		{"search", "serach", 1, false}, // transposition = 2 edits
		{"search", "serach", 2, true},
		{"search", "beta", 2, false},
		{"café", "cafe", 1, true}, // accent substitution (multibyte)
		{"ねこ", "いこ", 1, true},     // CJK single-rune substitution
		{"ねこ", "いぬ", 1, false},    // two CJK substitutions
		{"ねこ", "いぬ", 2, true},
		{"", "", 0, true},
		{"a", "", 1, true},
		{"a", "", 0, false},
	}
	for _, c := range cases {
		if got := withinEdits(r(c.a), r(c.b), c.max); got != c.want {
			t.Errorf("withinEdits(%q, %q, %d) = %v, want %v", c.a, c.b, c.max, got, c.want)
		}
	}
}

// TestIsExpandable verifies word terms expand while CJK grams (all-unbounded
// runs) and the empty term are exempt.
func TestIsExpandable(t *testing.T) {
	cases := []struct {
		term string
		want bool
	}{
		{"search", true},
		{"café", true}, // accented letters are word runes
		{"a1", true},   // digits are word runes
		{"デー", false},  // katakana bigram — exempt
		{"人々", false},  // kanji + iteration mark — exempt
		{"aあ", true},   // one word rune is enough
		{"", false},
	}
	for _, c := range cases {
		if got := isExpandable(c.term); got != c.want {
			t.Errorf("isExpandable(%q) = %v, want %v", c.term, got, c.want)
		}
	}
}

// TestTermDictExpand covers exact/prefix/fuzzy expansion, the exact term coming
// first, and the cap bounding the result deterministically.
func TestTermDictExpand(t *testing.T) {
	d := newTermDict()
	// Interned in this order, so ids are search=0 serch=1 searchh=2 xearch=3
	// sea=4 searching=5 beta=6 (id order drives the deterministic scan).
	for _, w := range []string{"search", "serch", "searchh", "xearch", "sea", "searching", "beta"} {
		d.intern(w)
	}
	expandTerms := func(term string, prefix bool, edits, limit int) []string {
		ids := d.expand(term, prefix, edits, limit)
		out := make([]string, len(ids))
		for i, id := range ids {
			out[i] = d.terms[id]
		}
		return out
	}
	sorted := func(term string, prefix bool, edits, limit int) []string {
		out := expandTerms(term, prefix, edits, limit)
		sort.Strings(out)
		return out
	}

	t.Run("exact only when no expansion", func(t *testing.T) {
		if got := sorted("search", false, 0, 50); !equalStrings(got, []string{"search"}) {
			t.Fatalf("exact = %v, want [search]", got)
		}
		if got := d.expand("absent", true, 2, 50); len(got) != 0 {
			t.Fatalf("absent term = %v, want no matches", got)
		}
	})
	t.Run("prefix reaches extending terms", func(t *testing.T) {
		got := sorted("sea", true, 0, 50)
		if !equalStrings(got, []string{"sea", "search", "searchh", "searching"}) {
			t.Fatalf("prefix 'sea' = %v, want [sea search searchh searching]", got)
		}
	})
	t.Run("fuzzy reaches edit-distance-1 terms", func(t *testing.T) {
		got := sorted("search", false, 1, 50)
		if !equalStrings(got, []string{"search", "searchh", "serch", "xearch"}) {
			t.Fatalf("fuzzy 'search' = %v, want [search searchh serch xearch]", got)
		}
	})
	t.Run("exact term is always first", func(t *testing.T) {
		got := expandTerms("search", true, 1, 50)
		if len(got) == 0 || got[0] != "search" {
			t.Fatalf("expansion = %v, want 'search' first", got)
		}
	})
	t.Run("cap bounds the result", func(t *testing.T) {
		got := expandTerms("sea", true, 0, 2)
		if len(got) != 2 || got[0] != "sea" {
			t.Fatalf("capped expansion = %v, want 2 entries starting with sea", got)
		}
	})
}

// TestSearchExpansion covers prefix and fuzzy term matching end to end: a query
// that matches nothing exactly still finds the document once expansion is on.
func TestSearchExpansion(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
	idx.Index("d1", Text("lantern search index"))
	idx.Index("d2", Text("beta gamma"))

	t.Run("prefix finds the extending term", func(t *testing.T) {
		if exact := idx.SearchMatch("lan", MatchOptions{}); len(exact) != 0 {
			t.Fatalf("exact 'lan' = %v, want none", idsOf(exact))
		}
		got := idsOf(idx.SearchMatch("lan", MatchOptions{PrefixTerms: true}))
		if !equalStrings(got, []string{"d1"}) {
			t.Fatalf("prefix 'lan' = %v, want [d1]", got)
		}
	})
	t.Run("fuzzy finds the mistyped term", func(t *testing.T) {
		if exact := idx.SearchMatch("serch", MatchOptions{}); len(exact) != 0 {
			t.Fatalf("exact 'serch' = %v, want none", idsOf(exact))
		}
		got := idsOf(idx.SearchMatch("serch", MatchOptions{Fuzziness: 1}))
		if !equalStrings(got, []string{"d1"}) {
			t.Fatalf("fuzzy 'serch' = %v, want [d1]", got)
		}
	})
	t.Run("fuzzy keeps precision on unrelated terms", func(t *testing.T) {
		if got := idx.SearchMatch("beto", MatchOptions{Fuzziness: 1}); !equalStrings(idsOf(got), []string{"d2"}) {
			t.Fatalf("fuzzy 'beto' = %v, want [d2] (beta only)", idsOf(got))
		}
	})
}

// TestSearchExpansionCoverage verifies that expansion composes correctly with
// MatchAll: each original query word is covered by ANY of its expansions, so a
// two-word expanded query still requires both words, not both expansions of one.
func TestSearchExpansionCoverage(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
	idx.Index("both", Text("lantern search")) // lantern + search
	idx.Index("oneL", Text("lantern here"))   // lantern only
	idx.Index("oneS", Text("search here"))    // search only

	// "lan" -> lantern (prefix), "serch" -> search (fuzzy). MatchAll needs both.
	got := idsOf(idx.SearchMatch("lan serch", MatchOptions{Mode: MatchAll, PrefixTerms: true, Fuzziness: 1}))
	if !equalStrings(got, []string{"both"}) {
		t.Fatalf("MatchAll expanded = %v, want [both]", got)
	}
	// MatchAny surfaces all three (each has one of the two words).
	any := idsOf(idx.SearchMatch("lan serch", MatchOptions{Mode: MatchAny, PrefixTerms: true, Fuzziness: 1}))
	sort.Strings(any)
	if !equalStrings(any, []string{"both", "oneL", "oneS"}) {
		t.Fatalf("MatchAny expanded = %v, want [both oneL oneS]", any)
	}
}

// TestSearchExpansionCJKExempt verifies a CJK query is not widened by fuzzy or
// prefix expansion: its bigrams match exactly (Lucene CJKAnalyzer behavior).
func TestSearchExpansionCJKExempt(t *testing.T) {
	idx := NewInvertedIndex[string, Document](NewScriptAwareAnalyzer(), nil)
	idx.Index("neko", Text("ねこがすき"))
	idx.Index("inu", Text("いぬがすき"))

	// Exact and fuzzy give the same result — the CJK bigrams are exempt, so
	// "ねこ" never fuzzy-expands into "いぬ".
	exact := idsOf(idx.SearchMatch("ねこ", MatchOptions{}))
	fuzzy := idsOf(idx.SearchMatch("ねこ", MatchOptions{Fuzziness: 2, PrefixTerms: true}))
	sort.Strings(exact)
	sort.Strings(fuzzy)
	if !equalStrings(exact, fuzzy) {
		t.Fatalf("CJK exact %v != fuzzy %v (bigrams must be exempt)", exact, fuzzy)
	}
	if !equalStrings(exact, []string{"neko"}) {
		t.Fatalf("CJK 'ねこ' = %v, want [neko]", exact)
	}
}

// randWord builds a deterministic pseudo-word for the expansion benchmarks.
func randWord(rng *rand.Rand) string {
	n := 3 + rng.Intn(6)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.Intn(26))
	}
	return string(b)
}

// BenchmarkSearchExpansion measures query latency over a large word dictionary
// with expansion off, prefix on, and fuzzy on, so the cost of the dictionary
// scan is visible (#891). The brute-force scan is O(dictionary); compare the
// sub-benchmarks to decide whether a radix/automaton is worth adding.
func BenchmarkSearchExpansion(b *testing.B) {
	rng := rand.New(rand.NewSource(891))
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
	for i := 0; i < 20000; i++ {
		idx.Index(fmt.Sprintf("d%05d", i), Text(randWord(rng)+" "+randWord(rng)+" "+randWord(rng)))
	}
	queries := []string{"abc", "lan", "sear"}

	run := func(b *testing.B, opts MatchOptions) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			idx.SearchMatch(queries[i%len(queries)], opts)
		}
	}
	b.Run("Exact", func(b *testing.B) { run(b, MatchOptions{}) })
	b.Run("Prefix", func(b *testing.B) { run(b, MatchOptions{PrefixTerms: true}) })
	b.Run("Fuzzy1", func(b *testing.B) { run(b, MatchOptions{Fuzziness: 1}) })
}
