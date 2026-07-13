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
			{Term: "café", Class: ClassWord},
			{Term: "ca", Class: ClassGram},
			{Term: "af", Class: ClassGram},
			{Term: "fé", Class: ClassGram},
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

	t.Run("FullCaseFoldAndCanonicalEquivalence", func(t *testing.T) {
		if got, want := a.Analyze("Straße ΟΣ cafe\u0301"), a.Analyze("STRASSE ος café"); !tokensEqual(got, want) {
			t.Fatalf("case/canonical equivalents differ:\n got %v\nwant %v", got, want)
		}
	})

	t.Run("MeaningfulMarksRemainInPrimaryTerms", func(t *testing.T) {
		for _, pair := range [][2]string{{"กา", "ก่า"}, {"कल", "काल"}, {"cafe", "café"}} {
			left := primaryTerms(a.Analyze(pair[0]))
			right := primaryTerms(a.Analyze(pair[1]))
			if tokensEqual(left, right) {
				t.Fatalf("Analyze(%q) and Analyze(%q) collapsed to %v", pair[0], pair[1], left)
			}
		}
	})

	t.Run("EmojiPresentationEquivalentButZWJIntentDistinct", func(t *testing.T) {
		if got, want := a.Analyze("❤"), a.Analyze("❤️"); !tokensEqual(got, want) {
			t.Fatalf("emoji presentation differs: %v vs %v", got, want)
		}
		if tokensEqual(a.Analyze("👩‍💻"), a.Analyze("👩‍🔬")) {
			t.Fatal("distinct ZWJ emoji sequences collapsed")
		}
	})

	t.Run("TwoRuneQueryAddsAuxiliaryChannel", func(t *testing.T) {
		qa, ok := a.(QueryAnalyzer)
		if !ok {
			t.Fatal("production analyzer does not implement QueryAnalyzer")
		}
		want := []Token{{Term: "ar", Class: ClassWord}, {Term: "ar", Class: ClassGram}}
		if got := qa.AnalyzeQuery("ar"); !tokensEqual(got, want) {
			t.Fatalf("AnalyzeQuery(ar) = %v, want %v", got, want)
		}
		if got := a.Analyze("ar"); !tokensEqual(got, want[:1]) {
			t.Fatalf("document Analyze(ar) = %v, want word channel only", got)
		}
		cjk := []Token{{Term: "東京", Class: ClassWord}}
		if got := qa.AnalyzeQuery("東京"); !tokensEqual(got, cjk) {
			t.Fatalf("AnalyzeQuery(東京) = %v, want unchanged primary CJK bigram", got)
		}
	})
}

func primaryTerms(tokens []Token) []Token {
	out := make([]Token, 0, len(tokens))
	for _, token := range tokens {
		if token.Class == ClassWord {
			out = append(out, token)
		}
	}
	return out
}

func FuzzScriptAwareAnalyzer(f *testing.F) {
	for _, seed := range []string{"Straße", "cafe\u0301", "สวัสดี", "हिन्दी", "👩‍💻", string([]byte{0xff, 'a', 0xfe})} {
		f.Add(seed, uint8(16))
	}
	a := NewScriptAwareAnalyzer()
	f.Fuzz(func(t *testing.T, text string, rawLimit uint8) {
		limit := int(rawLimit) + 1
		tokens := a.Analyze(text)
		// Each input rune can produce at most one primary token plus one
		// auxiliary bigram, with a small constant allowance for case-fold
		// expansion and symbol clusters. This catches accidental unbounded
		// expansion while accepting malformed UTF-8's replacement runes.
		if len(tokens) > 2*len([]rune(text))+4 {
			t.Fatalf("Analyze expanded %d runes to %d tokens", len([]rune(text)), len(tokens))
		}
		bounded, ok := a.(boundedAnalyzer)
		if !ok {
			t.Fatal("production analyzer is not bounded")
		}
		got, exceeded := bounded.AnalyzeBounded(text, limit)
		if exceeded && got != nil {
			t.Fatalf("bounded overflow retained %d tokens", len(got))
		}
		if !exceeded && len(got) > limit {
			t.Fatalf("bounded analysis returned %d tokens over limit %d", len(got), limit)
		}
	})
}

// BenchmarkScriptAwareAnalyzerVersions measures the CPU/allocation cost of the
// #1067 Unicode policy against the previously shipped v1 pipeline.
func BenchmarkScriptAwareAnalyzerVersions(b *testing.B) {
	v1 := NewAnalyzer(
		[]Normalizer{WidthNormalizer{}, DiacriticNormalizer{}, LowercaseNormalizer{}, PunctuationNormalizer{}, SpaceNormalizer{}},
		ScriptAwareTokenizer{N: 2}, nil,
	)
	v2 := NewScriptAwareAnalyzer()
	// Keep the comparison corpus on scripts whose tokenizer policy did not
	// change, isolating the analyzer v2 transforms from the separately tested
	// Thai/Indic/emoji tokenization behavior.
	text := "Straße café ΟΣ 東京 2026 full-text search"
	for _, tc := range []struct {
		name     string
		analyzer Analyzer
	}{
		{"V1", v1},
		{"V2", v2},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if got := tc.analyzer.Analyze(text); len(got) == 0 {
					b.Fatal("no tokens")
				}
			}
		})
	}
}
