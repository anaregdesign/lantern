package search

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeAnalyzer is a whitespace-splitting, case-folding analyzer used to
// exercise the generic index without depending on a production
// normalizer/tokenizer/filter pipeline.
type fakeAnalyzer struct{}

func (fakeAnalyzer) Analyze(text string) []Token {
	fields := strings.Fields(strings.ToLower(text))
	tokens := make([]Token, len(fields))
	for i, f := range fields {
		tokens[i] = Token{Term: f}
	}
	return tokens
}

func TestInvertedIndex(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
	idx.Index("doc1", Text("Alpha Beta"))
	idx.Index("doc2", Text("beta gamma"))

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"single term, one hit", "alpha", []string{"doc1"}},
		{"shared term unions docs", "beta", []string{"doc1", "doc2"}},
		{"multi term query unions", "alpha gamma", []string{"doc1", "doc2"}},
		{"analyzer folds query case", "BETA", []string{"doc1", "doc2"}},
		{"no analyzable terms", "   ", nil},
		{"miss", "delta", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idsOf(idx.Search(tt.query))
			sort.Strings(got) // Search ranks by score; sort to compare the match set.
			if !equalStrings(got, tt.want) {
				t.Fatalf("Search(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestInvertedIndexDelete(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
	idx.Index("doc1", Text("alpha beta"))
	idx.Index("doc2", Text("beta gamma"))

	idx.Delete("doc1")

	if got := idsOf(idx.Search("alpha")); len(got) != 0 {
		t.Fatalf(`Search("alpha") after delete = %v, want none`, got)
	}
	// doc2 still owns "beta"; only doc1's posting is gone.
	got := idsOf(idx.Search("beta"))
	if !equalStrings(got, []string{"doc2"}) {
		t.Fatalf(`Search("beta") after delete = %v, want [doc2]`, got)
	}

	// Deleting an unknown id is a no-op.
	idx.Delete("missing")

	// Deleting the last document for the remaining terms empties the index.
	idx.Delete("doc2")
	if got := idx.Search("beta gamma"); got != nil {
		t.Fatalf("Search after deleting all docs = %v, want nil", got)
	}
	// Empty posting sets, forward entries, and the length total must be
	// reclaimed, not leaked.
	if n := postingsCount(idx); n != 0 {
		t.Fatalf("postings not fully reclaimed: %d live terms", n)
	}
	if len(idx.docs) != 0 {
		t.Fatalf("forward docs not fully reclaimed: %v", idx.docs)
	}
	if n := totalLenSum(idx); n != 0 {
		t.Fatalf("totalLen not reset: %d", n)
	}
}

// TestInvertedIndexDeleteMany verifies the batch delete removes every supplied
// id under one lock, skips unknown ids, fully reclaims postings/forward
// entries/length, and is a no-op for an empty input. It is the batch sibling
// of Delete (#738).
func TestInvertedIndexDeleteMany(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
	idx.Index("doc1", Text("alpha beta"))
	idx.Index("doc2", Text("beta gamma"))
	idx.Index("doc3", Text("gamma delta"))

	idx.DeleteMany(nil) // no-op

	idx.DeleteMany([]string{"doc1", "missing", "doc3"})

	if got := idsOf(idx.Search("alpha")); len(got) != 0 {
		t.Fatalf(`Search("alpha") after DeleteMany = %v, want none`, got)
	}
	if got := idsOf(idx.Search("delta")); len(got) != 0 {
		t.Fatalf(`Search("delta") after DeleteMany = %v, want none`, got)
	}
	// doc2 still owns "beta" and "gamma".
	if got := idsOf(idx.Search("beta")); !equalStrings(got, []string{"doc2"}) {
		t.Fatalf(`Search("beta") after DeleteMany = %v, want [doc2]`, got)
	}
	if got := idsOf(idx.Search("gamma")); !equalStrings(got, []string{"doc2"}) {
		t.Fatalf(`Search("gamma") after DeleteMany = %v, want [doc2]`, got)
	}

	// Removing the last remaining doc empties every internal map.
	idx.DeleteMany([]string{"doc2"})
	if n := postingsCount(idx); n != 0 {
		t.Fatalf("postings not fully reclaimed: %d live terms", n)
	}
	if len(idx.docs) != 0 {
		t.Fatalf("forward docs not fully reclaimed: %v", idx.docs)
	}
	if n := totalLenSum(idx); n != 0 {
		t.Fatalf("totalLen not reset: %d", n)
	}
}

func TestInvertedIndexReindexReplaces(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
	idx.Index("doc1", Text("alpha beta"))
	// Re-index the same id with new text: the old terms must not linger.
	idx.Index("doc1", Text("gamma"))

	if got := idx.Search("alpha"); got != nil {
		t.Fatalf(`Search("alpha") after re-index = %v, want nil`, got)
	}
	if got := idx.Search("beta"); got != nil {
		t.Fatalf(`Search("beta") after re-index = %v, want nil`, got)
	}
	if got := idsOf(idx.Search("gamma")); !equalStrings(got, []string{"doc1"}) {
		t.Fatalf(`Search("gamma") after re-index = %v, want [doc1]`, got)
	}

	// Re-indexing with empty text removes the document entirely.
	idx.Index("doc1", Text("   "))
	if got := idx.Search("gamma"); got != nil {
		t.Fatalf(`Search("gamma") after empty re-index = %v, want nil`, got)
	}
	if n := totalLenSum(idx); n != 0 {
		t.Fatalf("totalLen not reset after removing last doc: %d", n)
	}
}

// TestInvertedIndexRanking covers the BM25 ranking the index applies on top of
// boolean matching: more occurrences, shorter documents, and rarer query terms
// all rank a document higher, and a query term repeated does not double-count.
func TestInvertedIndexRanking(t *testing.T) {
	t.Run("higher term frequency ranks first", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		idx.Index("more", Text("go go go rust"))
		idx.Index("less", Text("go rust python"))
		res := idx.Search("go")
		if len(res) != 2 {
			t.Fatalf("Search(go) returned %d results, want 2", len(res))
		}
		if res[0].ID != "more" {
			t.Fatalf("ranked %q first, want \"more\" (higher term frequency)", res[0].ID)
		}
		if !(res[0].Score > res[1].Score && res[1].Score > 0) {
			t.Fatalf("scores not positive and descending: %+v", res)
		}
	})

	t.Run("shorter document ranks first at equal term frequency", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		idx.Index("short", Text("go rust"))
		idx.Index("long", Text("go rust python java perl"))
		res := idx.Search("go")
		if len(res) != 2 || res[0].ID != "short" {
			t.Fatalf("ranked %+v, want \"short\" first (length normalization)", res)
		}
	})

	t.Run("rarer query term lifts its document", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		idx.Index("a", Text("common rare"))
		idx.Index("b", Text("common x"))
		idx.Index("c", Text("common y"))
		res := idx.Search("common rare")
		if len(res) != 3 || res[0].ID != "a" {
			t.Fatalf("ranked %+v, want \"a\" first (it owns the rare term)", res)
		}
	})

	t.Run("repeated query term scored once", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		idx.Index("d", Text("go rust"))
		once := idx.Search("go")
		twice := idx.Search("go go")
		if len(once) != 1 || len(twice) != 1 || once[0].Score != twice[0].Score {
			t.Fatalf("repeated query term changed the score: once=%+v twice=%+v", once, twice)
		}
	})
}

// TestInvertedIndexConcurrent exercises concurrent Index/Delete/Search so the
// race detector (go test -race) can prove the RWMutex discipline holds. The
// final document count is deterministic: each writer keeps its odd-indexed
// documents and deletes the even-indexed ones.
func TestInvertedIndexConcurrent(t *testing.T) {
	idx := NewInvertedIndex[int, Text](NewAnalyzer([]Normalizer{LowercaseNormalizer{}}, UnicodeTokenizer{}, nil), nil)

	const writers = 8
	const docsPerWriter = 200

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < docsPerWriter; i++ {
				id := base*docsPerWriter + i
				idx.Index(id, Text("alpha beta gamma"))
				_ = idx.Search("beta") // read concurrently with other writers
				if i%2 == 0 {
					idx.Delete(id)
				}
			}
		}(w)
	}
	wg.Wait()

	want := writers * docsPerWriter / 2
	if got := len(idx.Search("beta")); got != want {
		t.Fatalf(`Search("beta") returned %d docs, want %d`, got, want)
	}
}

// TestInvertedIndexPrepared covers the Prepare / IndexPrepared split that lets
// callers analyze a document outside the index write lock (#739). The
// analyze-then-index result must be byte-for-byte equivalent to Index, an empty
// prepared document must remove the id like Index of an unanalyzable document,
// and IndexManyPrepared must apply a batch under one lock with last-write
// semantics.
func TestInvertedIndexPrepared(t *testing.T) {
	t.Run("IndexPrepared matches Index", func(t *testing.T) {
		viaIndex := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		viaIndex.Index("doc1", Text("Alpha Beta"))
		viaIndex.Index("doc2", Text("beta gamma"))

		viaPrepared := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		viaPrepared.IndexPrepared("doc1", viaPrepared.Prepare(Text("Alpha Beta")))
		viaPrepared.IndexPrepared("doc2", viaPrepared.Prepare(Text("beta gamma")))

		for _, q := range []string{"alpha", "beta", "gamma", "alpha gamma"} {
			a := idsOf(viaIndex.Search(q))
			b := idsOf(viaPrepared.Search(q))
			sort.Strings(a)
			sort.Strings(b)
			if !equalStrings(a, b) {
				t.Fatalf("Search(%q): Index=%v IndexPrepared=%v", q, a, b)
			}
		}
	})

	t.Run("empty prepared removes id", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		idx.IndexPrepared("doc1", idx.Prepare(Text("alpha beta")))
		// Re-index doc1 with a document that analyzes to no terms: its postings
		// must be dropped, matching Index's replace-with-empty behavior.
		idx.IndexPrepared("doc1", idx.Prepare(Text("   ")))
		if got := idsOf(idx.Search("alpha")); len(got) != 0 {
			t.Fatalf(`Search("alpha") after empty re-index = %v, want none`, got)
		}
	})

	t.Run("IndexManyPrepared applies batch last-write", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil)
		// doc1 appears twice; the later item must win (replace semantics in
		// slice order). doc2's first document is later replaced by an empty one,
		// so it must be absent at the end.
		items := []PreparedItem[string]{
			{ID: "doc1", Prepared: idx.Prepare(Text("alpha"))},
			{ID: "doc2", Prepared: idx.Prepare(Text("beta"))},
			{ID: "doc1", Prepared: idx.Prepare(Text("gamma"))},
			{ID: "doc2", Prepared: idx.Prepare(Text("   "))},
		}
		idx.IndexManyPrepared(items)

		if got := idsOf(idx.Search("alpha")); len(got) != 0 {
			t.Fatalf(`Search("alpha") = %v, want none (replaced by gamma)`, got)
		}
		if got := idsOf(idx.Search("gamma")); !equalStrings(got, []string{"doc1"}) {
			t.Fatalf(`Search("gamma") = %v, want [doc1]`, got)
		}
		if got := idsOf(idx.Search("beta")); len(got) != 0 {
			t.Fatalf(`Search("beta") = %v, want none (doc2 emptied)`, got)
		}

		// Empty batch is a no-op and must not panic.
		idx.IndexManyPrepared(nil)
	})
}

// --- Memory benchmarks (epic #782) -----------------------------------------
//
// These measure the inverted index's footprint and allocation behaviour for a
// production-shaped bigram corpus, so each step of the index-memory work
// (#783 -> #784 -> #785) can report its compression. bigramAnalyzer mirrors
// the graphcache content-search pipeline (multilingual normalizers -> NGram
// N=2 -> whitespace filter); makeBigramCorpus builds deterministic documents
// whose ids mimic hierarchical vertex keys.

func bigramAnalyzer() Analyzer {
	return NewAnalyzer(
		[]Normalizer{
			WidthNormalizer{},
			DiacriticNormalizer{},
			LowercaseNormalizer{},
			PunctuationNormalizer{},
			SpaceNormalizer{},
		},
		NGramTokenizer{N: 2},
		[]TokenFilter{WhitespaceFilter{}},
	)
}

// benchVocab is a fixed word list; documents draw from it so the bigram
// vocabulary and posting-list lengths stay stable across runs and steps.
var benchVocab = []string{
	"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta",
	"lantern", "vertex", "edge", "decay", "search", "index", "memory", "graph",
	"replica", "cluster", "metric", "bigram", "posting", "token", "roaring",
	"prometheus", "snapshot", "mutation", "namespace", "preference", "tone",
	"summary", "context", "session", "agent", "fact", "relation", "recall",
}

func buildBigramIndex(corpus map[string]Text) *InvertedIndex[string, Text] {
	idx := NewInvertedIndex[string, Text](bigramAnalyzer(), nil)
	for id, doc := range corpus {
		idx.Index(id, doc)
	}
	return idx
}

// makeBigramCorpus builds n deterministic documents whose ids mimic
// hierarchical vertex keys and whose text draws from benchVocab, so the
// resulting index resembles a production content index.
func makeBigramCorpus(n int) map[string]Text {
	rng := rand.New(rand.NewSource(1)) // fixed seed: deterministic corpus
	corpus := make(map[string]Text, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("user.notes.%06d", i) // hierarchical, vertex-key-shaped
		words := 8 + rng.Intn(9)                // 8..16 words per document
		terms := make([]string, words)
		for w := range terms {
			terms[w] = benchVocab[rng.Intn(len(benchVocab))]
		}
		corpus[id] = Text(strings.Join(terms, " "))
	}
	return corpus
}

// BenchmarkInvertedIndexBuild reports B/op and allocs/op for building the whole
// index, so a step that cuts allocation churn or object count is visible.
func BenchmarkInvertedIndexBuild(b *testing.B) {
	corpus := makeBigramCorpus(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime.KeepAlive(buildBigramIndex(corpus))
	}
}

// BenchmarkInvertedIndexFootprint reports the retained HeapInuse attributable
// to the index (measured against a corpus-only baseline) plus live object
// count, so each step's compression of the steady-state footprint is visible.
// It is a benchmark (run under -bench) so plain `go test ./...` pays nothing.
func BenchmarkInvertedIndexFootprint(b *testing.B) {
	const docs = 4000
	corpus := makeBigramCorpus(docs)

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var idx *InvertedIndex[string, Text]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx = buildBigramIndex(corpus)
	}
	b.StopTimer()

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(idx)
	runtime.KeepAlive(corpus)

	delta := float64(after.HeapInuse) - float64(base.HeapInuse)
	b.ReportMetric(delta/(1024*1024), "MiB/index")
	b.ReportMetric(delta/float64(docs), "B/doc")
	b.ReportMetric(float64(after.HeapObjects-base.HeapObjects), "objects")
}

// TestSearchTopK property-checks the bounded selection (#841) against the
// reference pipeline "full Search → filter by accept → truncate to k":
// result length, membership, per-id scores, and the descending score
// multiset must all agree (tie order at the k-th boundary is free).
func TestSearchTopK(t *testing.T) {
	idx := NewInvertedIndex[string, Text](NewAnalyzer(
		[]Normalizer{LowercaseNormalizer{}},
		NGramTokenizer{N: 2},
		nil,
	), nil)
	rng := rand.New(rand.NewSource(841))
	// Search and SearchTopK both accumulate BM25 contributions while iterating
	// the distinct-term set (a Go map), so the floating-point addition order —
	// and therefore the last ULP of a multi-term score — can differ between
	// any two calls. Compare scores with a relative tolerance, not bitwise.
	closeEnough := func(a, b float64) bool {
		diff := math.Abs(a - b)
		return diff <= 1e-9*math.Max(math.Abs(a), math.Abs(b))
	}
	words := []string{"alpha", "alps", "altitude", "beta", "bethel", "gamma", "gambit", "delta"}
	docs := make(map[string]string, 120)
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("doc-%03d", i)
		body := words[rng.Intn(len(words))] + " " + words[rng.Intn(len(words))] + " " + words[rng.Intn(len(words))]
		docs[id] = body
		idx.Index(id, Text(body))
	}

	accepts := map[string]func(string) bool{
		"all":        nil,
		"evens only": func(id string) bool { return (id[len(id)-1]-'0')%2 == 0 },
		"none":       func(string) bool { return false },
	}
	for _, query := range []string{"al", "alpha", "ga", "zz"} {
		full := idx.Search(query)
		for name, accept := range accepts {
			for _, k := range []int{1, 3, 10, 500} {
				var want []Result[string]
				for _, r := range full {
					if accept == nil || accept(r.ID) {
						want = append(want, r)
					}
				}
				sort.SliceStable(want, func(i, j int) bool { return want[i].Score > want[j].Score })
				if len(want) > k {
					want = want[:k]
				}

				got := idx.SearchTopK(query, k, accept)
				if len(got) != len(want) {
					t.Fatalf("q=%q accept=%s k=%d: len=%d, want %d", query, name, k, len(got), len(want))
				}
				fullScore := make(map[string]float64, len(full))
				for _, r := range full {
					fullScore[r.ID] = r.Score
				}
				for i, r := range got {
					if i > 0 && got[i-1].Score < r.Score {
						t.Fatalf("q=%q accept=%s k=%d: not descending at %d: %v", query, name, k, i, got)
					}
					if s, ok := fullScore[r.ID]; !ok || !closeEnough(s, r.Score) {
						t.Fatalf("q=%q accept=%s k=%d: id %s score %v disagrees with full search (%v, %v)", query, name, k, r.ID, r.Score, s, ok)
					}
					if accept != nil && !accept(r.ID) {
						t.Fatalf("q=%q accept=%s k=%d: rejected id %s returned", query, name, k, r.ID)
					}
				}
				for i := range got {
					if !closeEnough(got[i].Score, want[i].Score) {
						t.Fatalf("q=%q accept=%s k=%d: score multiset diverges at %d: got %v want %v", query, name, k, i, got, want)
					}
				}
			}
		}
	}

	t.Run("k<=0 and empty query return nil", func(t *testing.T) {
		if got := idx.SearchTopK("al", 0, nil); got != nil {
			t.Fatalf("k=0 got %v", got)
		}
		if got := idx.SearchTopK("", 5, nil); got != nil {
			t.Fatalf("empty query got %v", got)
		}
	})
}

// BenchmarkSearchTopKBroadQuery pits the bounded selection against the full
// sort on the workload #841 targets: a two-character query over a large
// corpus (the bigram analyzer makes it match nearly everything), keeping
// only a small page.
func BenchmarkSearchTopKBroadQuery(b *testing.B) {
	idx := NewInvertedIndex[string, Text](NewAnalyzer(
		[]Normalizer{LowercaseNormalizer{}},
		NGramTokenizer{N: 2},
		nil,
	), nil)
	for i := 0; i < 20000; i++ {
		idx.Index(fmt.Sprintf("doc-%05d", i), Text(fmt.Sprintf("alpha beta gamma delta %05d", i)))
	}
	b.Run("SearchTopK-10", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if got := idx.SearchTopK("alpha", 10, nil); len(got) != 10 {
				b.Fatalf("len=%d", len(got))
			}
		}
	})
	b.Run("FullSearch", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if got := idx.Search("alpha"); len(got) < 10 {
				b.Fatalf("len=%d", len(got))
			}
		}
	})
}

// TestInvertedIndexDualClass covers the per-class posting tables (#888): the
// two channels keep separate statistics and identities, a class-aware scorer
// ranks whole-word evidence above fragment evidence, and deletes reclaim both
// channels.
func TestInvertedIndexDualClass(t *testing.T) {
	newIdx := func() *InvertedIndex[string, Text] {
		return NewInvertedIndex[string, Text](
			NewScriptAwareAnalyzer(),
			ClassWeighted{Base: BM25{K1: DefaultBM25K1, B: DefaultBM25B}, GramWeight: DefaultGramWeight},
		)
	}

	t.Run("whole word outranks infix fragment", func(t *testing.T) {
		idx := newIdx()
		idx.Index("exact", Text("a stone arch"))
		idx.Index("infix", Text("full text search"))
		idx.Index("infix2", Text("academic research"))
		res := idx.Search("arch")
		if len(res) != 3 {
			t.Fatalf("Search(arch) returned %d results, want 3 (infix recall preserved)", len(res))
		}
		if res[0].ID != "exact" {
			t.Fatalf("ranked %q first, want \"exact\" (word evidence dominates)", res[0].ID)
		}
	})

	t.Run("same spelling on both channels does not collide", func(t *testing.T) {
		// "se" exists as a whole word in doc1 and as an intra-word gram of
		// "sea" in doc2. A word query for "se" must rank the word carrier
		// first even though the gram channel also knows the spelling.
		idx := newIdx()
		idx.Index("word", Text("se"))
		idx.Index("gram", Text("sea"))
		res := idx.Search("se")
		if len(res) == 0 || res[0].ID != "word" {
			t.Fatalf("Search(se) = %+v, want \"word\" first", res)
		}
	})

	t.Run("delete reclaims both channels", func(t *testing.T) {
		idx := newIdx()
		idx.Index("doc", Text("search 東京"))
		idx.Delete("doc")
		if n := postingsCount(idx); n != 0 {
			t.Fatalf("postings not fully reclaimed: %d live terms", n)
		}
		if n := totalLenSum(idx); n != 0 {
			t.Fatalf("totalLen not reset: %d", n)
		}
		for class := range idx.classes {
			if idx.classes[class].docCount != 0 {
				t.Fatalf("class %d docCount not reset: %d", class, idx.classes[class].docCount)
			}
		}
	})

	t.Run("undefined class panics on index", func(t *testing.T) {
		bad := AnalyzerFunc(func(text string) []Token {
			return []Token{{Term: text, Class: TokenClass(9)}}
		})
		idx := NewInvertedIndex[string, Text](bad, nil)
		defer func() {
			if recover() == nil {
				t.Fatal("indexing an undefined TokenClass did not panic")
			}
		}()
		idx.Index("doc", Text("boom"))
	})

	t.Run("cjk stays searchable alongside words", func(t *testing.T) {
		idx := newIdx()
		idx.Index("ja", Text("東京の醤油ラーメン"))
		idx.Index("en", Text("tokyo ramen guide"))
		if res := idx.Search("ラーメン"); len(res) != 1 || res[0].ID != "ja" {
			t.Fatalf("Search(ラーメン) = %+v, want just ja", res)
		}
		if res := idx.Search("ramen"); len(res) != 1 || res[0].ID != "en" {
			t.Fatalf("Search(ramen) = %+v, want just en", res)
		}
	})
}
