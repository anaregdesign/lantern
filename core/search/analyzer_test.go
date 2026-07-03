package search

import "testing"

func TestNewAnalyzerStopWordFilter(t *testing.T) {
	// Lowercase normalization + letter/digit tokenization strips punctuation and
	// case; the stop-word filter then drops "the" and "a".
	a := NewAnalyzer(
		[]Normalizer{LowercaseNormalizer{}},
		UnicodeTokenizer{},
		[]TokenFilter{NewStopWordFilter("the", "a")},
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
	a := NewAnalyzer([]Normalizer{LowercaseNormalizer{}}, WhitespaceTokenizer{}, []TokenFilter{PunctuationFilter{}})
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
	a := NewAnalyzer([]Normalizer{PunctuationNormalizer{}, SpaceNormalizer{}}, WhitespaceTokenizer{}, nil)
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
			t.Fatal("NewAnalyzer(nil, nil, nil) did not panic")
		}
	}()
	NewAnalyzer(nil, nil, nil)
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

func TestNewScriptAwareAnalyzer(t *testing.T) {
	a := NewScriptAwareAnalyzer()

	t.Run("FoldsWidthDiacriticsCaseAndPunctuation", func(t *testing.T) {
		// Full-width "ＴＯＫＹＯ!" folds to "tokyo" with the "!" becoming a
		// boundary, then tokenizes as one word plus its auxiliary bigrams.
		got := a.Analyze("ＴＯＫＹＯ! Café")
		want := []Token{
			{Term: "tokyo", Class: ClassWord},
			{Term: "to", Class: ClassGram},
			{Term: "ok", Class: ClassGram},
			{Term: "ky", Class: ClassGram},
			{Term: "yo", Class: ClassGram},
			{Term: "cafe", Class: ClassWord},
			{Term: "ca", Class: ClassGram},
			{Term: "af", Class: ClassGram},
			{Term: "fe", Class: ClassGram},
		}
		if !tokensEqual(got, want) {
			t.Fatalf("Analyze = %v, want %v", got, want)
		}
	})

	t.Run("HalfWidthKatakanaJoinsKatakanaRun", func(t *testing.T) {
		// WidthNormalizer runs before tokenization, so ﾗｰﾒﾝ folds to ラーメン
		// and bigrams as one run.
		got := a.Analyze("ﾗｰﾒﾝ")
		want := []Token{
			{Term: "ラー", Class: ClassWord},
			{Term: "ーメ", Class: ClassWord},
			{Term: "メン", Class: ClassWord},
		}
		if !tokensEqual(got, want) {
			t.Fatalf("Analyze = %v, want %v", got, want)
		}
	})

	t.Run("NoTokenBridgesAWordBoundary", func(t *testing.T) {
		for _, tok := range a.Analyze("data set 東京 タワー") {
			if len(tok.Term) == 0 {
				t.Fatal("empty term emitted")
			}
			for _, r := range tok.Term {
				if r == ' ' {
					t.Fatalf("token %q bridges a word boundary", tok.Term)
				}
			}
		}
	})
}
