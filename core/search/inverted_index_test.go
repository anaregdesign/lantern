package search

import (
	"fmt"
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
	if len(idx.postings) != 0 {
		t.Fatalf("postings not fully reclaimed: %v", idx.postings)
	}
	if len(idx.docs) != 0 {
		t.Fatalf("forward docs not fully reclaimed: %v", idx.docs)
	}
	if idx.totalLen != 0 {
		t.Fatalf("totalLen not reset: %d", idx.totalLen)
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
	if len(idx.postings) != 0 {
		t.Fatalf("postings not fully reclaimed: %v", idx.postings)
	}
	if len(idx.docs) != 0 {
		t.Fatalf("forward docs not fully reclaimed: %v", idx.docs)
	}
	if idx.totalLen != 0 {
		t.Fatalf("totalLen not reset: %d", idx.totalLen)
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
	if idx.totalLen != 0 {
		t.Fatalf("totalLen not reset after removing last doc: %d", idx.totalLen)
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
