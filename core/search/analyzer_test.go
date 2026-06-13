package search

import "testing"

func TestNewAnalyzerStopWordFilter(t *testing.T) {
	// Lowercase normalization + letter/digit tokenization strips punctuation and
	// case; the stop-word filter then drops "the" and "a".
	a := NewAnalyzer(
		[]Normalizer{LowercaseNormalizer{}},
		UnicodeTokenizer{},
		NewStopWordFilter("the", "a"),
	)
	got := termsOf(a.Analyze("The quick, brown FOX! a fox?"))
	want := []string{"quick", "brown", "fox", "fox"}
	if !equalStrings(got, want) {
		t.Fatalf("Analyze = %v, want %v", got, want)
	}
}

func TestNewAnalyzerWhitespacePunctuation(t *testing.T) {
	// WhitespaceTokenizer keeps punctuation attached; PunctuationFilter trims
	// the edges while preserving the inner hyphen of "node-1".
	a := NewAnalyzer([]Normalizer{LowercaseNormalizer{}}, WhitespaceTokenizer{}, PunctuationFilter{})
	got := termsOf(a.Analyze("Hello, world! node-1."))
	want := []string{"hello", "world", "node-1"}
	if !equalStrings(got, want) {
		t.Fatalf("Analyze = %v, want %v", got, want)
	}
}

func TestNewAnalyzerMultipleNormalizers(t *testing.T) {
	// Normalizers apply in order: PunctuationNormalizer turns every mark into a
	// space, then SpaceNormalizer collapses the runs and trims, so the
	// WhitespaceTokenizer sees clean, single-space-delimited text.
	a := NewAnalyzer([]Normalizer{PunctuationNormalizer{}, SpaceNormalizer{}}, WhitespaceTokenizer{})
	got := termsOf(a.Analyze("Hello,  world... テスト！"))
	want := []string{"Hello", "world", "テスト"}
	if !equalStrings(got, want) {
		t.Fatalf("Analyze = %v, want %v", got, want)
	}
}

func TestNewAnalyzerNilTokenizerPanics(t *testing.T) {
	// The tokenizer is required: NewAnalyzer forbids a nil default and panics
	// instead of silently substituting one.
	defer func() {
		if recover() == nil {
			t.Fatal("NewAnalyzer(nil, nil) did not panic")
		}
	}()
	NewAnalyzer(nil, nil)
}

func TestNGramAnalyzer(t *testing.T) {
	a := NewNGramAnalyzer(2)
	// Lowercased bigrams over the whole input, CJK and Latin alike.
	if got := termsOf(a.Analyze("東京都")); !equalStrings(got, []string{"東京", "京都"}) {
		t.Fatalf("Analyze(東京都) = %v, want [東京 京都]", got)
	}
	if got := termsOf(a.Analyze("Go")); !equalStrings(got, []string{"go"}) {
		t.Fatalf("Analyze(Go) = %v, want [go]", got)
	}
	// A single rune is shorter than n, so it shares no bigram with anything.
	if got := termsOf(a.Analyze("都")); len(got) != 0 {
		t.Fatalf("Analyze(都) = %v, want []", got)
	}
}

func TestAnalyzerFunc(t *testing.T) {
	var a Analyzer = AnalyzerFunc(func(s string) []Token { return []Token{{Term: s}} })
	if got := termsOf(a.Analyze("x")); !equalStrings(got, []string{"x"}) {
		t.Fatalf("Analyze = %v, want [x]", got)
	}
}
