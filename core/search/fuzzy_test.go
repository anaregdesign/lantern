package search

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
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

// TestTermDictExpandMatchesReference proves the rune-length gate and reusable
// DP buffers (#909) leave expand's result byte-identical to a naive
// allocation-per-candidate oracle — same match set, same order, same cap
// survivors — even after intern/release churn seeds tombstones and reused
// (out-of-order) ids. referenceExpand shares no code with the optimized scan.
func TestTermDictExpandMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(9091))
	d := newTermDict()
	live := make(map[string]bool)
	for i := 0; i < 4000; i++ {
		w := randWord(rng)
		d.intern(w)
		live[w] = true
		if rng.Intn(3) == 0 && len(live) > 0 { // churn: release a live term
			for k := range live {
				d.release(d.ids[k])
				delete(live, k)
				break
			}
		}
	}
	queries := make([]string, 0, 40)
	for k := range live {
		queries = append(queries, k)
		if len(queries) >= 20 {
			break
		}
	}
	for i := 0; i < 20; i++ {
		queries = append(queries, randWord(rng)) // include absent terms
	}
	kinds := []struct {
		prefix bool
		edits  int
	}{{true, 0}, {false, 1}, {false, 2}, {true, 1}, {true, 2}}
	for _, limit := range []int{3, 50, MaxTermExpansions} {
		for _, q := range queries {
			for _, k := range kinds {
				got := d.expand(q, k.prefix, k.edits, limit)
				want := referenceExpand(d, q, k.prefix, k.edits, limit)
				if !slices.Equal(got, want) {
					t.Fatalf("expand(%q, prefix=%v, edits=%d, limit=%d) = %v, want %v",
						q, k.prefix, k.edits, limit, got, want)
				}
			}
		}
	}
}

// referenceExpand is a deliberately naive mirror of termDict.expand used only as
// an equivalence oracle: the exact id first, then a full id-order scan with
// strings.HasPrefix and an un-gated full-matrix Levenshtein. It shares none of
// expand's rune-length gate or reusable DP buffers, so a match confirms the
// optimized scan is behaviour-preserving.
func referenceExpand(d *termDict, term string, prefix bool, maxEdits, limit int) []uint32 {
	if limit <= 0 {
		return nil
	}
	out := make([]uint32, 0)
	seen := make(map[uint32]bool)
	add := func(id uint32) bool {
		if seen[id] {
			return len(out) < limit
		}
		seen[id] = true
		out = append(out, id)
		return len(out) < limit
	}
	if id, ok := d.ids[term]; ok {
		if !add(id) {
			return out
		}
	}
	if !prefix && maxEdits <= 0 {
		return out
	}
	qr := []rune(term)
	for id, cand := range d.terms {
		if cand == "" || cand == term {
			continue
		}
		matched := prefix && strings.HasPrefix(cand, term)
		if !matched && maxEdits > 0 {
			matched = naiveLevenshtein([]rune(cand), qr) <= maxEdits
		}
		if matched {
			if !add(uint32(id)) {
				return out
			}
		}
	}
	return out
}

// naiveLevenshtein is the textbook full-matrix edit distance, independent of
// withinEdits, so the oracle inherits no bug from the bounded two-row DP.
func naiveLevenshtein(a, b []rune) int {
	m := make([][]int, len(a)+1)
	for i := range m {
		m[i] = make([]int, len(b)+1)
		m[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		m[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m[i][j] = min(m[i-1][j]+1, m[i][j-1]+1, m[i-1][j-1]+cost)
		}
	}
	return m[len(a)][len(b)]
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

// expandSink keeps BenchmarkTermDictExpand's result live so the compiler cannot
// elide the expansion scan.
var expandSink []uint32

// buildExpandDict interns size distinct random words into a fresh term
// dictionary and returns it with a handful of query terms drawn from the
// interned set (so the exact-term hit always lands and prefix/fuzzy have real
// neighbours to find). Deterministic for a stable sweep.
func buildExpandDict(size int) (*termDict, []string) {
	rng := rand.New(rand.NewSource(909))
	d := newTermDict()
	for d.len() < size {
		d.intern(randWord(rng))
	}
	queries := make([]string, 0, 8)
	for i := 0; i < len(d.terms) && len(queries) < 8; i += max(1, len(d.terms)/8) {
		if d.terms[i] != "" {
			queries = append(queries, d.terms[i])
		}
	}
	return d, queries
}

// BenchmarkTermDictExpand isolates the dictionary-scan cost of termDict.expand
// (#909) across dictionary sizes and expansion kinds. expand is O(dictionary):
// prefix does a byte-prefix test per term, while fuzzy converts every candidate
// to runes and runs bounded Levenshtein, so the sub-benchmarks expose whether a
// sorted/radix structure is worth adding at each scale. Run under -bench only;
// plain `go test` never pays for the 1M dictionary build.
func BenchmarkTermDictExpand(b *testing.B) {
	for _, size := range []int{10_000, 100_000, 1_000_000} {
		d, queries := buildExpandDict(size)
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			bench := func(b *testing.B, prefix bool, maxEdits int) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					expandSink = d.expand(queries[i%len(queries)], prefix, maxEdits, MaxTermExpansions)
				}
			}
			b.Run("Prefix", func(b *testing.B) { bench(b, true, 0) })
			b.Run("Fuzzy1", func(b *testing.B) { bench(b, false, 1) })
			b.Run("Fuzzy2", func(b *testing.B) { bench(b, false, 2) })
		})
	}
}
