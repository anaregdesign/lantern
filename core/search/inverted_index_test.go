package search

import (
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
