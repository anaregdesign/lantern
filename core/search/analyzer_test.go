package search

import "testing"

func TestStandardAnalyzer(t *testing.T) {
	a := NewStandardAnalyzer("the", "a")
	// Lowercasing + letter/digit tokenization strips punctuation and case;
	// the stop-word filter drops "the" and "a".
	got := termsOf(a.Analyze("The quick, brown FOX! a fox?"))
	want := []string{"quick", "brown", "fox", "fox"}
	if !equalStrings(got, want) {
		t.Fatalf("Analyze = %v, want %v", got, want)
	}
}

func TestStandardAnalyzerNoStopWords(t *testing.T) {
	got := termsOf(NewStandardAnalyzer().Analyze("Hello, WORLD"))
	if !equalStrings(got, []string{"hello", "world"}) {
		t.Fatalf("Analyze = %v, want [hello world]", got)
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

func TestNewAnalyzerDefaultsTokenizer(t *testing.T) {
	// An empty normalizer list is skipped; a nil tokenizer falls back to UnicodeTokenizer.
	a := NewAnalyzer(nil, nil)
	got := termsOf(a.Analyze("a-b c"))
	if !equalStrings(got, []string{"a", "b", "c"}) {
		t.Fatalf("Analyze = %v, want [a b c]", got)
	}
}

func TestAnalyzerFunc(t *testing.T) {
	var a Analyzer = AnalyzerFunc(func(s string) []Token { return []Token{{Term: s}} })
	if got := termsOf(a.Analyze("x")); !equalStrings(got, []string{"x"}) {
		t.Fatalf("Analyze = %v, want [x]", got)
	}
}
