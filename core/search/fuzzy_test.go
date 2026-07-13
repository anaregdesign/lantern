package search

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
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

// TestASCIILevenshteinDistance compares the Myers fast path to the independent
// full-matrix oracle across the complete supported pattern-length range.
func TestASCIILevenshteinDistance(t *testing.T) {
	rng := rand.New(rand.NewSource(1056))
	randomASCII := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.Intn(8))
		}
		return string(b)
	}
	for patternLen := 1; patternLen <= 64; patternLen++ {
		for trial := 0; trial < 40; trial++ {
			pattern := randomASCII(patternLen)
			candidate := randomASCII(rng.Intn(71))
			var masks [utf8.RuneSelf]uint64
			for i := range pattern {
				masks[pattern[i]] |= uint64(1) << i
			}
			want := naiveLevenshtein([]rune(pattern), []rune(candidate))
			for _, maxEdits := range []int{0, 1, 2, want} {
				got, ok := asciiLevenshteinWithin(len(pattern), candidate, maxEdits, &masks)
				if wantOK := want <= maxEdits; ok != wantOK || ok && got != want {
					t.Fatalf("patternLen=%d trial=%d distance(%q, %q, max=%d) = (%d,%v), want (%d,%v)", patternLen, trial, pattern, candidate, maxEdits, got, ok, want, wantOK)
				}
			}
		}
	}

	// Exhaust every short binary string pair, including an empty candidate.
	binaryStrings := []string{""}
	for length := 1; length <= 5; length++ {
		for bits := 0; bits < 1<<length; bits++ {
			word := make([]byte, length)
			for i := range word {
				word[i] = "ab"[(bits>>i)&1]
			}
			binaryStrings = append(binaryStrings, string(word))
		}
	}
	for _, pattern := range binaryStrings {
		if pattern == "" {
			continue
		}
		var masks [utf8.RuneSelf]uint64
		for i := range pattern {
			masks[pattern[i]] |= uint64(1) << i
		}
		for _, candidate := range binaryStrings {
			want := naiveLevenshtein([]rune(pattern), []rune(candidate))
			for maxEdits := 0; maxEdits <= 2; maxEdits++ {
				got, ok := asciiLevenshteinWithin(len(pattern), candidate, maxEdits, &masks)
				if wantOK := want <= maxEdits; ok != wantOK || ok && got != want {
					t.Fatalf("binary distance(%q, %q, max=%d) = (%d,%v), want (%d,%v)", pattern, candidate, maxEdits, got, ok, want, wantOK)
				}
			}
		}
	}

	// Pin the 64th-bit boundary plus valid ASCII NUL/DEL bytes.
	boundary := "\x00" + strings.Repeat("a", 62) + "\x7f"
	mutations := []string{
		boundary,
		"b" + boundary[1:],
		boundary[:31] + "b" + boundary[32:],
		boundary[:63] + "b",
		boundary[:62],
		boundary[:63],
		boundary + "bb",
	}
	var masks [utf8.RuneSelf]uint64
	for i := range boundary {
		masks[boundary[i]] |= uint64(1) << i
	}
	for _, candidate := range mutations {
		want := naiveLevenshtein([]rune(boundary), []rune(candidate))
		got, ok := asciiLevenshteinWithin(len(boundary), candidate, want, &masks)
		if !ok || got != want {
			t.Fatalf("64-bit boundary distance(%q) = (%d,%v), want %d", candidate, got, ok, want)
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

func expandedTerms(d *termDict, term string, prefix bool, edits, limit int) []string {
	ids := d.expand(term, prefix, edits, limit)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = d.terms[id]
	}
	return out
}

// TestTermDictExpand covers the semantic exact/prefix/fuzzy order, including
// the combined-mode precedence and rune-counted prefix quality.
func TestTermDictExpand(t *testing.T) {
	d := newTermDict()
	for _, w := range []string{"search", "serch", "searchh", "xearch", "sea", "searching", "beta"} {
		d.intern(w)
	}

	t.Run("exact only when no expansion", func(t *testing.T) {
		if got := expandedTerms(d, "search", false, 0, 50); !equalStrings(got, []string{"search"}) {
			t.Fatalf("exact = %v, want [search]", got)
		}
		if got := d.expand("absent", true, 2, 50); len(got) != 0 {
			t.Fatalf("absent term = %v, want no matches", got)
		}
	})
	t.Run("prefix orders by extension then term", func(t *testing.T) {
		got := expandedTerms(d, "sea", true, 0, 50)
		if !equalStrings(got, []string{"sea", "search", "searchh", "searching"}) {
			t.Fatalf("prefix 'sea' = %v, want [sea search searchh searching]", got)
		}
	})
	t.Run("tracked expansion reports only non-exact survivors", func(t *testing.T) {
		work := newWorkTracker(nil, Budget{})
		ids, err := d.expandTracked("sea", true, 0, 50, work)
		if err != nil || len(ids) != 4 {
			t.Fatalf("expandTracked ids=%v err=%v", ids, err)
		}
		if work.stats.ExpansionRetained != 3 {
			t.Fatalf("expansions retained = %d, want 3 non-exact survivors", work.stats.ExpansionRetained)
		}
	})
	t.Run("fuzzy orders by edit distance then term", func(t *testing.T) {
		got := expandedTerms(d, "search", false, 1, 50)
		if !equalStrings(got, []string{"search", "searchh", "serch", "xearch"}) {
			t.Fatalf("fuzzy 'search' = %v, want [search searchh serch xearch]", got)
		}
	})
	t.Run("combined mode prefers prefix and deduplicates overlap", func(t *testing.T) {
		both := newTermDict()
		for _, term := range []string{"aaaa", "aaaab", "aaaaaa", "aaab", "baaa"} {
			both.intern(term)
		}
		got := expandedTerms(both, "aaaa", true, 1, 50)
		want := []string{"aaaa", "aaaab", "aaaaaa", "aaab", "baaa"}
		if !equalStrings(got, want) {
			t.Fatalf("combined expansion = %v, want %v", got, want)
		}
	})
	t.Run("prefix extension is counted in runes", func(t *testing.T) {
		unicode := newTermDict()
		for _, term := range []string{"é", "éaa", "é東", "éa"} {
			unicode.intern(term)
		}
		got := expandedTerms(unicode, "é", true, 0, 50)
		want := []string{"é", "éa", "é東", "éaa"}
		if !equalStrings(got, want) {
			t.Fatalf("unicode prefix expansion = %v, want %v", got, want)
		}
	})
	t.Run("unicode fuzzy candidates use rune distance", func(t *testing.T) {
		unicode := newTermDict()
		for _, term := range []string{"cafe", "café", "cafés", "coffee"} {
			unicode.intern(term)
		}
		if got, want := expandedTerms(unicode, "cafe", false, 1, 50), []string{"cafe", "café"}; !equalStrings(got, want) {
			t.Fatalf("ASCII query Unicode candidate expansion = %v, want %v", got, want)
		}
		if got, want := expandedTerms(unicode, "café", false, 1, 50), []string{"café", "cafe", "cafés"}; !equalStrings(got, want) {
			t.Fatalf("Unicode query expansion = %v, want %v", got, want)
		}
	})
	t.Run("invalid UTF-8 stays on rune fallback", func(t *testing.T) {
		invalid := newTermDict()
		invalid.intern("\xfe")
		// Go decodes each invalid byte as RuneError, so these terms have rune
		// distance zero. Treating them as ASCII bytes would incorrectly return 1.
		if got, want := expandedTerms(invalid, "\xff", false, 1, 50), []string{"\xfe"}; !equalStrings(got, want) {
			t.Fatalf("invalid UTF-8 expansion = %q, want %q", got, want)
		}
	})
	t.Run("65-byte query uses DP fallback", func(t *testing.T) {
		query := strings.Repeat("a", 65)
		candidate := query[:64] + "b"
		long := newTermDict()
		long.intern(candidate)
		if got, want := expandedTerms(long, query, false, 1, 50), []string{candidate}; !equalStrings(got, want) {
			t.Fatalf("65-byte expansion = %v, want one DP match", got)
		}
	})
}

// TestTermDictExpandHistoryIndependent builds the same >50-candidate vocabulary
// in 100 insertion/release/reuse histories and compares the exact cap survivors
// to an independent semantic oracle.
func TestTermDictExpandHistoryIndependent(t *testing.T) {
	vocabulary := []string{"aaaa"}
	for i := 0; i < 80; i++ {
		vocabulary = append(vocabulary, fmt.Sprintf("aaaa%03d", i))
	}
	for pos := 0; pos < 4; pos++ {
		for ch := byte('b'); ch <= 'z'; ch++ {
			word := []byte("aaaa")
			word[pos] = ch
			vocabulary = append(vocabulary, string(word))
		}
	}
	for i := 0; i < 40; i++ {
		vocabulary = append(vocabulary, fmt.Sprintf("noise%03d", i))
	}

	cases := []struct {
		name   string
		prefix bool
		edits  int
	}{
		{"prefix", true, 0},
		{"fuzzy", false, 2},
		{"combined", true, 2},
	}
	rng := rand.New(rand.NewSource(1056))
	var baseline map[string][]string
	for trial := 0; trial < 100; trial++ {
		order := append([]string(nil), vocabulary...)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		d := newTermDict()
		for _, term := range order {
			d.intern(term)
		}
		// Force released-id reuse while restoring the identical live vocabulary.
		for i := trial % 7; i < len(vocabulary); i += 7 {
			d.release(d.ids[vocabulary[i]])
		}
		for i := len(vocabulary) - 1; i >= 0; i-- {
			if _, live := d.ids[vocabulary[i]]; !live {
				d.intern(vocabulary[i])
			}
		}

		if baseline == nil {
			baseline = make(map[string][]string, len(cases))
		}
		for _, tc := range cases {
			gotIDs := d.expand("aaaa", tc.prefix, tc.edits, MaxTermExpansions)
			wantIDs := referenceExpand(d, "aaaa", tc.prefix, tc.edits, MaxTermExpansions)
			got := make([]string, len(gotIDs))
			want := make([]string, len(wantIDs))
			for i := range gotIDs {
				got[i] = d.terms[gotIDs[i]]
			}
			for i := range wantIDs {
				want[i] = d.terms[wantIDs[i]]
			}
			if !slices.Equal(got, want) {
				t.Fatalf("trial=%d %s expansion = %v, want %v", trial, tc.name, got, want)
			}
			if trial == 0 {
				baseline[tc.name] = append([]string(nil), got...)
			} else if !slices.Equal(got, baseline[tc.name]) {
				t.Fatalf("trial=%d %s expansion changed with history: got %v baseline %v", trial, tc.name, got, baseline[tc.name])
			}
		}
	}
}

// referenceExpand is a deliberately naive semantic oracle. It computes full
// Levenshtein matrices and sorts every match, sharing neither the optimized DP
// rows nor the bounded heap with termDict.expand.
func referenceExpand(d *termDict, term string, prefix bool, maxEdits, limit int) []uint32 {
	if limit <= 0 {
		return nil
	}
	out := make([]uint32, 0, limit)
	if id, ok := d.ids[term]; ok {
		out = append(out, id)
		if len(out) == limit {
			return out
		}
	}
	type candidate struct {
		id      uint32
		term    string
		kind    int
		quality int
	}
	var candidates []candidate
	for id, cand := range d.terms {
		if cand == "" || cand == term {
			continue
		}
		if prefix && strings.HasPrefix(cand, term) {
			candidates = append(candidates, candidate{uint32(id), cand, 0, len([]rune(cand)) - len([]rune(term))})
			continue
		}
		if maxEdits > 0 {
			distance := naiveLevenshtein([]rune(cand), []rune(term))
			if distance <= maxEdits {
				candidates = append(candidates, candidate{uint32(id), cand, 1, distance})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].kind != candidates[j].kind {
			return candidates[i].kind < candidates[j].kind
		}
		if candidates[i].quality != candidates[j].quality {
			return candidates[i].quality < candidates[j].quality
		}
		return candidates[i].term < candidates[j].term
	})
	for _, candidate := range candidates {
		if len(out) == limit {
			break
		}
		out = append(out, candidate.id)
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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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
	idx := NewInvertedIndex[string, Document](NewScriptAwareAnalyzer(), nil, compareStringID)
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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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

// seedCapOverflowExpandDict replaces a bounded number of the dictionary's
// random terms with one exact query term, more than MaxTermExpansions literal
// continuations, and more than MaxTermExpansions edit-distance-1 neighbours.
// The live dictionary size stays unchanged, while release/reuse churn makes
// the benchmark independent of a favourable fresh-id layout.
func seedCapOverflowExpandDict(d *termDict) string {
	const query = "aaaaaaaa"
	hot := []string{query}
	for i := 0; i < 2*MaxTermExpansions; i++ {
		hot = append(hot, fmt.Sprintf("%s%03d", query, i))
	}
	for pos := 0; pos < len(query); pos++ {
		for replacement := byte('b'); replacement <= 'z'; replacement++ {
			candidate := []byte(query)
			candidate[pos] = replacement
			hot = append(hot, string(candidate))
		}
	}

	// A generated random term could theoretically equal a hot term. Only make
	// room for genuinely new terms so the number of live dictionary entries is
	// exactly preserved.
	before := d.len()
	hotSet := make(map[string]struct{}, len(hot))
	for _, term := range hot {
		hotSet[term] = struct{}{}
	}
	newTerms := hot[:0]
	for _, term := range hot {
		if _, exists := d.ids[term]; !exists {
			newTerms = append(newTerms, term)
		}
	}
	removed := 0
	for id, term := range d.terms {
		if term == "" {
			continue
		}
		if _, keep := hotSet[term]; keep {
			continue
		}
		d.release(uint32(id))
		removed++
		if removed == len(newTerms) {
			break
		}
	}
	for _, term := range newTerms {
		d.intern(term)
	}
	if removed != len(newTerms) || d.len() != before {
		panic("cap-overflow benchmark failed to preserve live dictionary size")
	}
	return query
}

// BenchmarkTermDictExpand isolates the dictionary-scan cost of termDict.expand
// (#909) across dictionary sizes and expansion kinds. expand is O(dictionary):
// prefix does a byte-prefix test per term, while fuzzy converts every candidate
// to runes and runs bounded Levenshtein. The CapOverflow arms force more than
// 50 semantic candidates after release/reuse churn, exposing the bounded heap's
// selection cost instead of measuring only sparse matches. Together the arms
// show whether a sorted/radix structure is worth adding at each scale. Each
// sparse operation runs all eight fixed queries so benchmark calibration cannot
// skew the query-length mix. Run under -bench only; plain `go test` never pays
// for the 1M dictionary build.
func BenchmarkTermDictExpand(b *testing.B) {
	for _, size := range []int{10_000, 100_000, 1_000_000} {
		d, queries := buildExpandDict(size)
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			bench := func(b *testing.B, query string, prefix bool, maxEdits int) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					expandSink = d.expand(query, prefix, maxEdits, MaxTermExpansions)
				}
			}
			benchSparse := func(b *testing.B, prefix bool, maxEdits int) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// One operation always executes the complete fixed query set.
					// This prevents b.N calibration differences from changing the
					// query-length mix between implementations.
					for _, query := range queries {
						expandSink = d.expand(query, prefix, maxEdits, MaxTermExpansions)
					}
				}
			}
			b.Run("Prefix", func(b *testing.B) { benchSparse(b, true, 0) })
			b.Run("Fuzzy1", func(b *testing.B) { benchSparse(b, false, 1) })
			b.Run("Fuzzy2", func(b *testing.B) { benchSparse(b, false, 2) })

			hotQuery := seedCapOverflowExpandDict(d)
			b.Run("CapOverflowPrefix", func(b *testing.B) { bench(b, hotQuery, true, 0) })
			b.Run("CapOverflowFuzzy1", func(b *testing.B) { bench(b, hotQuery, false, 1) })
			b.Run("CapOverflowFuzzy2", func(b *testing.B) { bench(b, hotQuery, false, 2) })
			b.Run("CapOverflowCombined", func(b *testing.B) { bench(b, hotQuery, true, 2) })
		})
	}
}
