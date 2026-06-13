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
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{})
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
			got := idx.Search(tt.query)
			sort.Strings(got) // Search order is unspecified.
			if !equalStrings(got, tt.want) {
				t.Fatalf("Search(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestInvertedIndexDelete(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{})
	idx.Index("doc1", Text("alpha beta"))
	idx.Index("doc2", Text("beta gamma"))

	idx.Delete("doc1")

	if got := idx.Search("alpha"); got != nil {
		t.Fatalf(`Search("alpha") after delete = %v, want nil`, got)
	}
	// doc2 still owns "beta"; only doc1's posting is gone.
	got := idx.Search("beta")
	sort.Strings(got)
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
	// Empty posting sets and forward entries must be reclaimed, not leaked.
	if len(idx.postings) != 0 {
		t.Fatalf("postings not fully reclaimed: %v", idx.postings)
	}
	if len(idx.terms) != 0 {
		t.Fatalf("forward terms not fully reclaimed: %v", idx.terms)
	}
}

func TestInvertedIndexReindexReplaces(t *testing.T) {
	idx := NewInvertedIndex[string, Text](fakeAnalyzer{})
	idx.Index("doc1", Text("alpha beta"))
	// Re-index the same id with new text: the old terms must not linger.
	idx.Index("doc1", Text("gamma"))

	if got := idx.Search("alpha"); got != nil {
		t.Fatalf(`Search("alpha") after re-index = %v, want nil`, got)
	}
	if got := idx.Search("beta"); got != nil {
		t.Fatalf(`Search("beta") after re-index = %v, want nil`, got)
	}
	if got := idx.Search("gamma"); !equalStrings(got, []string{"doc1"}) {
		t.Fatalf(`Search("gamma") after re-index = %v, want [doc1]`, got)
	}

	// Re-indexing with empty text removes the document entirely.
	idx.Index("doc1", Text("   "))
	if got := idx.Search("gamma"); got != nil {
		t.Fatalf(`Search("gamma") after empty re-index = %v, want nil`, got)
	}
}

// TestInvertedIndexConcurrent exercises concurrent Index/Delete/Search so the
// race detector (go test -race) can prove the RWMutex discipline holds. The
// final document count is deterministic: each writer keeps its odd-indexed
// documents and deletes the even-indexed ones.
func TestInvertedIndexConcurrent(t *testing.T) {
	idx := NewInvertedIndex[int, Text](NewStandardAnalyzer())

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
