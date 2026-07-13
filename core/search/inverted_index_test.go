package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeAnalyzer is a whitespace-splitting, case-folding analyzer used to
// exercise the generic index without depending on a production
// normalizer/tokenizer/filter pipeline.
type fakeAnalyzer struct{}

type hintedDocument struct {
	hint   int
	called *bool
}

func (d hintedDocument) String() string { *d.called = true; return strings.Repeat("x", d.hint) }
func (d hintedDocument) SizeHint() int  { return d.hint }

func (fakeAnalyzer) Analyze(text string) []Token {
	fields := strings.Fields(strings.ToLower(text))
	tokens := make([]Token, len(fields))
	for i, f := range fields {
		tokens[i] = Token{Term: f}
	}
	return tokens
}

func TestInvertedIndexStructuredFields(t *testing.T) {
	newIndex := func(weight float64, opts ...IndexOption) *InvertedIndex[string, Document] {
		scorer := FieldWeighted{Base: BM25{K1: DefaultBM25K1, B: DefaultBM25B}, KeyWeight: weight, ValueWeight: 1}
		return NewInvertedIndex[string, Document](fakeAnalyzer{}, scorer, compareStringID, opts...)
	}

	t.Run("phrase cannot cross semantic field or value instance", func(t *testing.T) {
		idx := newIndex(1, WithPositions())
		docs := map[string]Fields{
			"key-value-split": {{ID: FieldKey, Text: "alpha"}, {ID: FieldValue, Text: "beta"}},
			"value-split":     {{ID: FieldValue, Text: "alpha"}, {ID: FieldValue, Text: "beta"}},
			"key-phrase":      {{ID: FieldKey, Text: "alpha beta"}},
			"value-phrase":    {{ID: FieldValue, Text: "alpha beta"}},
		}
		for id, doc := range docs {
			if err := idx.Index(id, doc); err != nil {
				t.Fatal(err)
			}
		}
		if got, want := idsOf(idx.SearchPhrase("alpha beta")), []string{"key-phrase", "value-phrase"}; !slices.Equal(got, want) {
			t.Fatalf("phrase hits = %v, want %v", got, want)
		}
	})

	t.Run("proximity is local to one value instance", func(t *testing.T) {
		idx := newIndex(1, WithPositions())
		idx.Index("together", Fields{{ID: FieldValue, Text: "alpha beta"}})
		idx.Index("split", Fields{{ID: FieldValue, Text: "alpha"}, {ID: FieldValue, Text: "beta"}})
		results := idx.Search("alpha beta")
		if len(results) != 2 || results[0].ID != "together" || results[0].Score <= results[1].Score {
			t.Fatalf("proximity results = %+v, want together strictly first", results)
		}
	})

	t.Run("key weight uses field-local corpus statistics", func(t *testing.T) {
		idx := newIndex(DefaultKeyFieldWeight)
		idx.Index("key", Fields{{ID: FieldKey, Text: "alpha"}})
		idx.Index("value", Fields{{ID: FieldValue, Text: "alpha"}})
		results := idx.Search("alpha")
		if len(results) != 2 || results[0].ID != "key" || results[0].Score <= results[1].Score {
			t.Fatalf("field-weighted results = %+v, want key strictly first", results)
		}
	})

	t.Run("compaction preserves fields and boundaries", func(t *testing.T) {
		idx := newIndex(1, WithPositions())
		idx.Index("cross", Fields{{ID: FieldKey, Text: "alpha"}, {ID: FieldValue, Text: "beta"}})
		idx.Index("within", Fields{{ID: FieldValue, Text: "alpha beta"}})
		idx.Compact()
		if got := idsOf(idx.SearchPhrase("alpha beta")); !slices.Equal(got, []string{"within"}) {
			t.Fatalf("phrase after compaction = %v", got)
		}
	})
}

func TestInvertedIndexAnalysisLimits(t *testing.T) {
	t.Run("document dimensions", func(t *testing.T) {
		unicodeIndex := NewInvertedIndex[string, Text](NewScriptAwareAnalyzer(), nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxDocumentBytes: 1}))
		if _, stats, err := unicodeIndex.Prepare(Text("é")); err == nil || stats.ProjectedBytes != 2 {
			t.Fatalf("Prepare unicode = stats=%+v err=%v, want 2-byte rejection", stats, err)
		}

		tokenIndex := NewInvertedIndex[string, Text](NewScriptAwareAnalyzer(), nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxDocumentTokens: 3}))
		if _, _, err := tokenIndex.Prepare(Text("abcdef")); !isAnalysisLimit(err, LimitDocumentTokens) {
			t.Fatalf("Prepare token cap err = %v", err)
		}

		termIndex := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxDocumentTerms: 2}))
		if _, _, err := termIndex.Prepare(Text("a b c a")); !isAnalysisLimit(err, LimitDocumentTerms) {
			t.Fatalf("Prepare term cap err = %v", err)
		}
	})

	t.Run("structured fields share aggregate limits and retain field postings", func(t *testing.T) {
		idx := NewInvertedIndex[string, Document](fakeAnalyzer{}, nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxDocumentTokens: 3}))
		_, stats, err := idx.Prepare(Fields{{ID: FieldKey, Text: "alpha beta"}, {ID: FieldValue, Text: "alpha gamma"}})
		if !isAnalysisLimit(err, LimitDocumentTokens) || stats.Tokens != 4 {
			t.Fatalf("structured token limit stats=%+v err=%v", stats, err)
		}
		unbounded := NewInvertedIndex[string, Document](fakeAnalyzer{}, nil, compareStringID)
		_, stats, err = unbounded.Prepare(Fields{{ID: FieldKey, Text: "alpha"}, {ID: FieldValue, Text: "alpha"}})
		if err != nil || stats.UniqueTerms != 1 || stats.Postings != 2 {
			t.Fatalf("structured stats=%+v err=%v, want terms=1 postings=2", stats, err)
		}
	})

	t.Run("size hint rejects before projection", func(t *testing.T) {
		called := false
		idx := NewInvertedIndex[string, hintedDocument](fakeAnalyzer{}, nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxDocumentBytes: 4}))
		if _, _, err := idx.Prepare(hintedDocument{hint: 5, called: &called}); !isAnalysisLimit(err, LimitDocumentBytes) {
			t.Fatalf("Prepare err = %v", err)
		}
		if called {
			t.Fatal("String was called after the size hint already exceeded the limit")
		}
	})

	t.Run("aggregate batch is atomic", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxLiveTerms: 2, MaxLivePostings: 2}))
		if err := idx.Index("existing", Text("alpha")); err != nil {
			t.Fatal(err)
		}
		p1, _, _ := idx.Prepare(Text("beta"))
		p2, _, _ := idx.Prepare(Text("gamma"))
		err := idx.IndexManyPrepared([]PreparedItem[string]{{ID: "one", Prepared: p1}, {ID: "two", Prepared: p2}})
		if !isAnalysisLimit(err, LimitLiveTerms) {
			t.Fatalf("IndexManyPrepared err = %v", err)
		}
		if got := idsOf(idx.Search("alpha")); !equalStrings(got, []string{"existing"}) {
			t.Fatalf("existing after rejection = %v", got)
		}
		if got := idx.MemoryStats(); got.Documents != 1 || got.LiveTerms != 1 || got.Postings != 1 {
			t.Fatalf("stats after rejection = %+v", got)
		}
	})

	t.Run("duplicate IDs are budgeted and applied by final state", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxLiveTerms: 2, MaxLivePostings: 2}))
		if err := idx.Index("existing", Text("alpha")); err != nil {
			t.Fatal(err)
		}
		transient, _, _ := idx.Prepare(Text("beta gamma"))
		final, _, _ := idx.Prepare(Text("beta"))
		if err := idx.IndexManyPrepared([]PreparedItem[string]{{ID: "same", Prepared: transient}, {ID: "same", Prepared: final}}); err != nil {
			t.Fatalf("final-state batch: %v", err)
		}
		if got := idx.MemoryStats(); got.LiveTerms != 2 || got.Postings != 2 {
			t.Fatalf("stats = %+v", got)
		}
	})

	t.Run("positions", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions(),
			WithAnalysisLimits(SearchAnalysisLimits{MaxPositionEntries: 2}))
		if err := idx.Index("x", Text("a b c")); !isAnalysisLimit(err, LimitPositionEntries) {
			t.Fatalf("Index err = %v", err)
		}
		if got := idx.MemoryStats().Documents; got != 0 {
			t.Fatalf("documents = %d after rejection", got)
		}
	})

	t.Run("aggregate postings", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID,
			WithAnalysisLimits(SearchAnalysisLimits{MaxLiveTerms: 10, MaxLivePostings: 1}))
		if err := idx.Index("x", Text("a b")); !isAnalysisLimit(err, LimitLivePostings) {
			t.Fatalf("Index err = %v", err)
		}
	})
}

func TestInvertedIndexCompactionAndHealth(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID,
		WithAnalysisLimits(SearchAnalysisLimits{CompactionRatio: 1.1, CompactionMinRetired: 1}))
	for i := range 100 {
		_ = idx.Index(fmt.Sprintf("doc-%d", i), Text(fmt.Sprintf("term-%d", i)))
	}
	for i := range 99 {
		idx.Delete(fmt.Sprintf("doc-%d", i))
	}
	stats := idx.MemoryStats()
	if stats.RetainedTermSlots > 2 || stats.RetainedOrdinals > 2 || stats.RebuildCount == 0 {
		t.Fatalf("compacted stats = %+v", stats)
	}
	_, work, err := idx.SearchMatchTopKContext(context.Background(), "term-99", 10, nil, MatchOptions{Fuzziness: 1}, Budget{MaxDictionaryVisits: 10})
	if err != nil || work.DictionaryVisits > 2 {
		t.Fatalf("post-compaction fuzzy work = %+v err=%v", work, err)
	}
	idx.MarkIncomplete()
	if _, _, err := idx.SearchMatchTopKContext(context.Background(), "term", 10, nil, MatchOptions{}, Budget{}); !errors.Is(err, ErrIndexIncomplete) {
		t.Fatalf("incomplete search err = %v", err)
	}
	p, _, err := idx.Prepare(Text("healthy"))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.RebuildPrepared([]PreparedItem[string]{{ID: "fresh", Prepared: p}}); err != nil {
		t.Fatal(err)
	}
	if got := idx.MemoryStats().Health; got != IndexHealthy {
		t.Fatalf("health = %q", got)
	}
}

func TestInvertedIndexConcurrentSearchWriteCompaction(t *testing.T) {
	idx := NewInvertedIndex[string, Text](NewScriptAwareAnalyzer(), nil, compareStringID, WithPositions(),
		WithAnalysisLimits(SearchAnalysisLimits{MaxDocumentBytes: 1 << 10, MaxDocumentTokens: 1_000, MaxDocumentTerms: 1_000, MaxLiveTerms: 10_000, MaxLivePostings: 100_000, MaxPositionEntries: 100_000, CompactionRatio: 2, CompactionMinRetired: 10}))
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 200 {
				key := fmt.Sprintf("%d/%d", worker, i%50)
				_ = idx.Index(key, Text(fmt.Sprintf("shared term-%d", i)))
				if i%3 == 0 {
					idx.Delete(key)
				}
				if i%7 == 0 {
					idx.Compact()
				}
				_, _, _ = idx.SearchMatchTopKContext(context.Background(), "shared", 10, nil, MatchOptions{}, Budget{})
			}
		}(worker)
	}
	wg.Wait()
	if got := idx.MemoryStats().Health; got != IndexHealthy {
		t.Fatalf("health = %q", got)
	}
}

func TestInvertedIndexCompactionPreservesClampedFrequencyLength(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
	if err := idx.Index("long", Text(strings.Repeat("repeat ", 70_000))); err != nil {
		t.Fatal(err)
	}
	want := totalLenSum(idx)
	idx.Compact()
	if got := totalLenSum(idx); got != want {
		t.Fatalf("total length after compaction = %d, want %d", got, want)
	}
}

func isAnalysisLimit(err error, kind AnalysisLimitKind) bool {
	var limit *AnalysisLimitError
	return errors.As(err, &limit) && limit.Kind == kind
}

func TestInvertedIndex(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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

func TestInvertedIndexGeneration(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, strings.Compare)
	if got := idx.MemoryStats().Generation; got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}
	idx.Index("a", Text("alpha"))
	first := idx.MemoryStats().Generation
	if first == 0 {
		t.Fatal("generation did not advance after index")
	}
	idx.DeleteMany([]string{"a", "missing"})
	second := idx.MemoryStats().Generation
	if second != first+1 {
		t.Fatalf("generation after delete batch = %d, want %d", second, first+1)
	}
	idx.MarkIncomplete()
	if got := idx.MemoryStats().Generation; got != second+1 {
		t.Fatalf("generation after incomplete = %d, want %d", got, second+1)
	}
}

func TestInvertedIndexReindexReplaces(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		idx.Index("short", Text("go rust"))
		idx.Index("long", Text("go rust python java perl"))
		res := idx.Search("go")
		if len(res) != 2 || res[0].ID != "short" {
			t.Fatalf("ranked %+v, want \"short\" first (length normalization)", res)
		}
	})

	t.Run("rarer query term lifts its document", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		idx.Index("a", Text("common rare"))
		idx.Index("b", Text("common x"))
		idx.Index("c", Text("common y"))
		res := idx.Search("common rare")
		if len(res) != 3 || res[0].ID != "a" {
			t.Fatalf("ranked %+v, want \"a\" first (it owns the rare term)", res)
		}
	})

	t.Run("repeated query term scored once", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
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
	idx := NewInvertedIndex[int, Text](NewAnalyzer([]Normalizer{LowercaseNormalizer{}}, UnicodeTokenizer{}, nil), nil, compareIntID)

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
	mustPrepare := func(t *testing.T, idx *InvertedIndex[string, Text], text Text) PreparedDocument {
		t.Helper()
		prepared, _, err := idx.Prepare(text)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		return prepared
	}
	t.Run("Prepare groups sorted frequencies and positions", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID, WithPositions())
		prepared := mustPrepare(t, idx, Text("Beta alpha beta"))
		words := prepared.classes[ClassWord]
		if words.length != 3 || len(words.terms) != 2 || prepared.postings != 2 || prepared.positionEntries != 3 {
			t.Fatalf("prepared = %+v", prepared)
		}
		if got := words.terms[0]; got.term != "alpha" || got.frequency != 1 || !slices.Equal(got.fields[FieldDefault].positions, []uint64{1}) {
			t.Fatalf("first grouped term = %+v", got)
		}
		if got := words.terms[1]; got.term != "beta" || got.frequency != 2 || !slices.Equal(got.fields[FieldDefault].positions, []uint64{0, 2}) {
			t.Fatalf("second grouped term = %+v", got)
		}
	})
	t.Run("IndexPrepared matches Index", func(t *testing.T) {
		viaIndex := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		viaIndex.Index("doc1", Text("Alpha Beta"))
		viaIndex.Index("doc2", Text("beta gamma"))

		viaPrepared := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		viaPrepared.IndexPrepared("doc1", mustPrepare(t, viaPrepared, Text("Alpha Beta")))
		viaPrepared.IndexPrepared("doc2", mustPrepare(t, viaPrepared, Text("beta gamma")))

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
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		idx.IndexPrepared("doc1", mustPrepare(t, idx, Text("alpha beta")))
		// Re-index doc1 with a document that analyzes to no terms: its postings
		// must be dropped, matching Index's replace-with-empty behavior.
		idx.IndexPrepared("doc1", mustPrepare(t, idx, Text("   ")))
		if got := idsOf(idx.Search("alpha")); len(got) != 0 {
			t.Fatalf(`Search("alpha") after empty re-index = %v, want none`, got)
		}
	})

	t.Run("IndexManyPrepared applies batch last-write", func(t *testing.T) {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		// doc1 appears twice; the later item must win (replace semantics in
		// slice order). doc2's first document is later replaced by an empty one,
		// so it must be absent at the end.
		items := []PreparedItem[string]{
			{ID: "doc1", Prepared: mustPrepare(t, idx, Text("alpha"))},
			{ID: "doc2", Prepared: mustPrepare(t, idx, Text("beta"))},
			{ID: "doc1", Prepared: mustPrepare(t, idx, Text("gamma"))},
			{ID: "doc2", Prepared: mustPrepare(t, idx, Text("   "))},
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
	idx := NewInvertedIndex[string, Text](bigramAnalyzer(), nil, compareStringID)
	for id, doc := range corpus {
		idx.Index(id, doc)
	}
	return idx
}

// buildBigramIndexWithPositions is buildBigramIndex built WithPositions, for
// the #889 benchmarks that measure the positional postings' build and steady-
// state overhead. The bigram analyzer emits every term on the primary channel,
// so every term carries positions — the worst case for the position store.
func buildBigramIndexWithPositions(corpus map[string]Text) *InvertedIndex[string, Text] {
	idx := NewInvertedIndex[string, Text](bigramAnalyzer(), nil, compareStringID, WithPositions())
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

// BenchmarkInvertedIndexBuildWithPositions is the WithPositions counterpart of
// BenchmarkInvertedIndexBuild: -benchmem against it reports the build-time
// allocation cost of the positional postings (#889).
func BenchmarkInvertedIndexBuildWithPositions(b *testing.B) {
	corpus := makeBigramCorpus(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime.KeepAlive(buildBigramIndexWithPositions(corpus))
	}
}

// BenchmarkInvertedIndexBoundedBatch covers the #1050 capacity path at the
// two canonical plural sizes and a long document, with positional storage on
// and off. Each iteration includes analysis, final-state preflight, and build.
func BenchmarkInvertedIndexBoundedBatch(b *testing.B) {
	for _, positions := range []bool{false, true} {
		positionName := map[bool]string{false: "PositionsOff", true: "PositionsOn"}[positions]
		for _, size := range []int{1_000, 10_000} {
			b.Run(fmt.Sprintf("%s/Batch%d", positionName, size), func(b *testing.B) {
				corpus := makeBigramCorpus(size)
				b.ReportAllocs()
				for b.Loop() {
					var opts []IndexOption
					if positions {
						opts = append(opts, WithPositions())
					}
					idx := NewInvertedIndex[string, Text](NewScriptAwareAnalyzer(), nil, compareStringID, opts...)
					items := make([]PreparedItem[string], 0, size)
					for id, doc := range corpus {
						prepared, _, err := idx.Prepare(doc)
						if err != nil {
							b.Fatal(err)
						}
						items = append(items, PreparedItem[string]{ID: id, Prepared: prepared})
					}
					if err := idx.IndexManyPrepared(items); err != nil {
						b.Fatal(err)
					}
					runtime.KeepAlive(idx)
				}
			})
		}
		b.Run(positionName+"/LongDocument", func(b *testing.B) {
			doc := Text(strings.Repeat("bounded-search-document ", 10_000))
			b.ReportAllocs()
			for b.Loop() {
				var opts []IndexOption
				if positions {
					opts = append(opts, WithPositions())
				}
				idx := NewInvertedIndex[string, Text](NewScriptAwareAnalyzer(), nil, compareStringID, opts...)
				if err := idx.Index("long", doc); err != nil {
					b.Fatal(err)
				}
				runtime.KeepAlive(idx)
			}
		})
	}
}

// BenchmarkInvertedIndexFootprintWithPositions is the WithPositions counterpart
// of BenchmarkInvertedIndexFootprint: the retained-heap delta against the
// positions-off footprint is the steady-state cost of the position slices.
func BenchmarkInvertedIndexFootprintWithPositions(b *testing.B) {
	const docs = 4000
	corpus := makeBigramCorpus(docs)

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var idx *InvertedIndex[string, Text]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx = buildBigramIndexWithPositions(corpus)
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

// BenchmarkSearchProximity measures multi-term query latency with and without
// the proximity boost, which runs only when the index tracks positions. Both
// sub-benchmarks share the corpus and query set so -benchmem/ns compares the
// boost's per-query cost directly (#889).
func BenchmarkSearchProximity(b *testing.B) {
	const docs = 4000
	corpus := makeBigramCorpus(docs)
	queries := []string{"al", "search index", "alpha beta gamma"}

	b.Run("WithoutPositions", func(b *testing.B) {
		idx := buildBigramIndex(corpus)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			idx.Search(queries[i%len(queries)])
		}
	})
	b.Run("WithPositions", func(b *testing.B) {
		idx := buildBigramIndexWithPositions(corpus)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			idx.Search(queries[i%len(queries)])
		}
	})
}

// BenchmarkStructuredFieldSearch records the query-time cost and retained
// logical bytes of the v2 field model against the historical flattened text.
func BenchmarkStructuredFieldSearch(b *testing.B) {
	const documents = 20_000
	for _, structured := range []bool{false, true} {
		name := map[bool]string{false: "Flat", true: "Structured"}[structured]
		b.Run(name, func(b *testing.B) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			scorer := FieldWeighted{Base: BM25{K1: DefaultBM25K1, B: DefaultBM25B}, KeyWeight: DefaultKeyFieldWeight, ValueWeight: 1}
			idx := NewInvertedIndex[string, Document](fakeAnalyzer{}, scorer, compareStringID, WithPositions())
			for i := 0; i < documents; i++ {
				key := fmt.Sprintf("tenant.%03d.note.%06d", i%100, i)
				valueA := fmt.Sprintf("alpha topic%d", i%100)
				valueB := "beta searchable content"
				var doc Document = Text(key + " " + valueA + " " + valueB)
				if structured {
					doc = Fields{{ID: FieldKey, Text: key}, {ID: FieldValue, Text: valueA}, {ID: FieldValue, Text: valueB}}
				}
				if err := idx.Index(key, doc); err != nil {
					b.Fatal(err)
				}
			}
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			heapBytesPerDoc := float64(after.HeapAlloc-before.HeapAlloc) / documents
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if results := idx.SearchTopK("alpha beta", 10, nil); len(results) != 10 {
					b.Fatalf("results=%d", len(results))
				}
			}
			runtime.KeepAlive(idx)
			b.ReportMetric(heapBytesPerDoc, "heap-B/doc")
		})
	}
}

// BenchmarkSearchPhrase measures phrase-query latency (AND-intersection plus
// positional adjacency verification) on the positions-enabled bigram index.
func BenchmarkSearchPhrase(b *testing.B) {
	const docs = 4000
	corpus := makeBigramCorpus(docs)
	idx := buildBigramIndexWithPositions(corpus)
	queries := []string{"search index", "alpha beta", "vertex edge decay"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx.SearchPhrase(queries[i%len(queries)])
	}
}

// TestSearchTopK property-checks the bounded selection (#841) against the
// reference pipeline "full Search → filter by accept → truncate to k". The
// complete Result slice must agree exactly, including equal-score boundary
// membership and score bits.
func TestSearchTopK(t *testing.T) {
	idx := NewInvertedIndex[string, Text](NewAnalyzer(
		[]Normalizer{LowercaseNormalizer{}},
		NGramTokenizer{N: 2},
		nil,
	), nil, compareStringID)
	rng := rand.New(rand.NewSource(841))
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
				if len(want) > k {
					want = want[:k]
				}

				got := idx.SearchTopK(query, k, accept)
				if !slices.Equal(got, want) {
					t.Fatalf("q=%q accept=%s k=%d: got %v, want %v", query, name, k, got, want)
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

// TestSearchOrderingIndependentOfHistory proves the externally visible total
// order is independent of document ordinals, insertion order, and delete /
// reinsert churn. k deliberately cuts through a 64-way score tie.
func TestSearchOrderingIndependentOfHistory(t *testing.T) {
	ids := make([]string, 64)
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%03d", i)
	}
	build := func(order []string, churn bool) *InvertedIndex[string, Text] {
		idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, compareStringID)
		for _, id := range order {
			idx.Index(id, Text("common stable"))
		}
		if churn {
			for i := 0; i < len(ids); i += 3 {
				idx.Index(ids[i], Text("transient history term"))
			}
			for i := len(ids) - 1; i >= 0; i -= 3 {
				idx.Index(ids[i], Text("common stable"))
			}
		}
		return idx
	}

	reverse := append([]string(nil), ids...)
	slices.Reverse(reverse)
	random := append([]string(nil), ids...)
	rand.New(rand.NewSource(1056)).Shuffle(len(random), func(i, j int) { random[i], random[j] = random[j], random[i] })
	histories := []struct {
		name  string
		order []string
		churn bool
	}{
		{"forward", ids, false},
		{"reverse", reverse, false},
		{"random", random, false},
		{"release-reuse", random, true},
	}

	var baseline []Result[string]
	for _, history := range histories {
		t.Run(history.name, func(t *testing.T) {
			idx := build(history.order, history.churn)
			full := idx.Search("common stable")
			if got := idsOf(full); !slices.Equal(got, ids) {
				t.Fatalf("full order = %v, want lexical IDs", got)
			}
			if baseline == nil {
				baseline = append([]Result[string](nil), full...)
			} else if !slices.Equal(full, baseline) {
				t.Fatalf("full results differ from baseline:\n got  %v\n want %v", full, baseline)
			}
			wantTop := baseline[:10]
			for repeat := 0; repeat < 100; repeat++ {
				if got := idx.SearchTopK("common stable", 10, nil); !slices.Equal(got, wantTop) {
					t.Fatalf("repeat %d top-k = %v, want %v", repeat, got, wantTop)
				}
			}
		})
	}
}

// TestSearchScoreBitsRepeat verifies sorted query clauses fix floating-point
// addition order and the resulting near-tie membership. The two small
// contributions disappear when added after 1e16, but survive when accumulated
// first; a map-order implementation can therefore flip both score bits and k=1.
func TestSearchScoreBitsRepeat(t *testing.T) {
	scorer := ScorerFunc(func(stats TermStats) float64 {
		if stats.TF == 1 {
			return 1e16
		}
		return 1
	})
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, scorer, compareStringID)
	idx.Index("huge-first", Text("a b b c c c")) // lexical contribution order: 1e16, 1, 1
	idx.Index("huge-last", Text("a a b b c"))    // lexical contribution order: 1, 1, 1e16
	want := []Result[string]{
		{ID: "huge-last", Score: 1e16 + 2},
		{ID: "huge-first", Score: 1e16},
	}
	for _, query := range []string{"a b c", "c a b", "b c a"} {
		for repeat := 0; repeat < 100; repeat++ {
			got := idx.Search(query)
			if !slices.Equal(got, want) {
				t.Fatalf("query %q repeat %d results = %v, want %v", query, repeat, got, want)
			}
			for i := range got {
				if bits, wantBits := math.Float64bits(got[i].Score), math.Float64bits(want[i].Score); bits != wantBits {
					t.Fatalf("query %q repeat %d result %d score bits = %x, want %x", query, repeat, i, bits, wantBits)
				}
			}
			if top := idx.SearchTopK(query, 1, nil); !slices.Equal(top, want[:1]) {
				t.Fatalf("query %q repeat %d top-k = %v, want %v", query, repeat, top, want[:1])
			}
		}
	}
}

// TestSearchDropsNonFiniteScores keeps malformed custom scorer output out of
// exhaustive and bounded ranking.
func TestSearchDropsNonFiniteScores(t *testing.T) {
	scorer := ScorerFunc(func(stats TermStats) float64 {
		switch stats.TF {
		case 1:
			return math.NaN()
		case 2:
			return math.Inf(1)
		case 3:
			return math.Inf(-1)
		default:
			return 1
		}
	})
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{}, scorer, compareStringID)
	idx.Index("nan", Text("x"))
	idx.Index("positive-infinity", Text("x x"))
	idx.Index("negative-infinity", Text("x x x"))
	idx.Index("finite", Text("x x x x"))
	want := []Result[string]{{ID: "finite", Score: 1}}
	if got := idx.Search("x"); !slices.Equal(got, want) {
		t.Fatalf("Search non-finite scores = %v, want %v", got, want)
	}
	if got := idx.SearchTopK("x", 10, nil); !slices.Equal(got, want) {
		t.Fatalf("SearchTopK non-finite scores = %v, want %v", got, want)
	}
}

func TestNewInvertedIndexRequiresComparator(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil comparator did not panic")
		}
	}()
	NewInvertedIndex[string, Text](fakeAnalyzer{}, nil, nil)
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
	), nil, compareStringID)
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

// BenchmarkSearchTwoRuneInfix measures the production #1067 path on a broad
// two-rune query and records retained heap next to the pre-v2 comparable
// four-rune query. The corpus is identical across cases.
func BenchmarkSearchTwoRuneInfix(b *testing.B) {
	const documents = 20_000
	legacy := func() Analyzer {
		return NewAnalyzer(
			[]Normalizer{WidthNormalizer{}, DiacriticNormalizer{}, LowercaseNormalizer{}, PunctuationNormalizer{}, SpaceNormalizer{}},
			ScriptAwareTokenizer{N: 2}, nil,
		)
	}
	for _, tc := range []struct {
		name     string
		analyzer func() Analyzer
		query    string
	}{
		{"V1-arch", legacy, "arch"},
		{"V2-arch", NewScriptAwareAnalyzer, "arch"},
		{"V2-ar", NewScriptAwareAnalyzer, "ar"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			idx := NewInvertedIndex[string, Text](tc.analyzer(), ClassWeighted{
				Base: BM25{K1: DefaultBM25K1, B: DefaultBM25B}, GramWeight: DefaultGramWeight,
			}, compareStringID)
			for i := 0; i < documents; i++ {
				idx.Index(fmt.Sprintf("doc-%05d", i), Text(fmt.Sprintf("full text search archive shard %05d", i)))
			}
			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)
			heapBytesPerDoc := float64(after.HeapAlloc-before.HeapAlloc) / documents
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if got := idx.SearchTopK(tc.query, 10, nil); len(got) != 10 {
					b.Fatalf("results=%d", len(got))
				}
			}
			runtime.KeepAlive(idx)
			b.ReportMetric(heapBytesPerDoc, "heap-B/doc")
		})
	}
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
			compareStringID,
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
		idx := NewInvertedIndex[string, Text](bad, nil, compareStringID)
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
