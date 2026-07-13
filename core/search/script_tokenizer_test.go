package search

import (
	"testing"
)

// tokensEqual compares full tokens (term + class), the property this
// tokenizer exists to get right.
func tokensEqual(a, b []Token) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScriptAwareTokenizer(t *testing.T) {
	tok := ScriptAwareTokenizer{} // zero value: N = 2, the production shape

	cases := []struct {
		name string
		text string
		want []Token
	}{
		{
			name: "LatinWordEmitsWordPlusAuxGrams",
			text: "search",
			want: []Token{
				{Term: "search", Class: ClassWord},
				{Term: "se", Class: ClassGram},
				{Term: "ea", Class: ClassGram},
				{Term: "ar", Class: ClassGram},
				{Term: "rc", Class: ClassGram},
				{Term: "ch", Class: ClassGram},
			},
		},
		{
			name: "TwoRuneDocumentSkipsRedundantGram",
			text: "go",
			want: []Token{{Term: "go", Class: ClassWord}},
		},
		{
			name: "SingleRuneWordStaysSearchable",
			// The pure bigram pipeline dropped one-letter words entirely.
			text: "a",
			want: []Token{{Term: "a", Class: ClassWord}},
		},
		{
			name: "DigitsAreWords",
			text: "2024 v2",
			want: []Token{
				{Term: "2024", Class: ClassWord},
				{Term: "20", Class: ClassGram},
				{Term: "02", Class: ClassGram},
				{Term: "24", Class: ClassGram},
				{Term: "v2", Class: ClassWord},
			},
		},
		{
			name: "CJKRunEmitsPrimaryBigrams",
			text: "東京駅",
			want: []Token{
				{Term: "東京", Class: ClassWord},
				{Term: "京駅", Class: ClassWord},
			},
		},
		{
			name: "LoneIdeographEmittedWhole",
			// CJKBigramFilter unigram fallback: a single character must not
			// vanish below the window.
			text: "麺",
			want: []Token{{Term: "麺", Class: ClassWord}},
		},
		{
			name: "ProlongedSoundMarkStaysInKatakanaRun",
			// ー is script Common but must not split ラーメン.
			text: "ラーメン",
			want: []Token{
				{Term: "ラー", Class: ClassWord},
				{Term: "ーメ", Class: ClassWord},
				{Term: "メン", Class: ClassWord},
			},
		},
		{
			name: "IterationMarkStaysInHanRun",
			text: "人々",
			want: []Token{{Term: "人々", Class: ClassWord}},
		},
		{
			name: "MixedScriptSplitsAtScriptBoundary",
			// "4k" is a word run; "モニター" is a katakana run — no token
			// bridges the boundary.
			text: "4kモニター",
			want: []Token{
				{Term: "4k", Class: ClassWord},
				{Term: "モニ", Class: ClassWord},
				{Term: "ニタ", Class: ClassWord},
				{Term: "ター", Class: ClassWord},
			},
		},
		{
			name: "DelimitersDropAndNeverBridge",
			// Space and punctuation split runs; no gram straddles them, so
			// the n-gram pipeline's WhitespaceFilter step is unnecessary.
			text: "go, rust",
			want: []Token{
				{Term: "go", Class: ClassWord},
				{Term: "rust", Class: ClassWord},
				{Term: "ru", Class: ClassGram},
				{Term: "us", Class: ClassGram},
				{Term: "st", Class: ClassGram},
			},
		},
		{
			name: "HangulIsUnbounded",
			text: "한국어",
			want: []Token{
				{Term: "한국", Class: ClassWord},
				{Term: "국어", Class: ClassWord},
			},
		},
		{
			name: "ThaiUsesWordAndAuxiliaryGrams",
			text: "ไทย",
			want: []Token{
				{Term: "ไทย", Class: ClassWord},
				{Term: "ไท", Class: ClassGram},
				{Term: "ทย", Class: ClassGram},
			},
		},
		{
			name: "ThaiMarksStayAttached",
			text: "ก่า",
			want: []Token{
				{Term: "ก่า", Class: ClassWord},
				{Term: "ก่", Class: ClassGram},
				{Term: "่า", Class: ClassGram},
			},
		},
		{
			name: "IndicMarksStayInsideWord",
			text: "हिन्दी",
			want: []Token{
				{Term: "हिन्दी", Class: ClassWord},
				{Term: "हि", Class: ClassGram},
				{Term: "िन", Class: ClassGram},
				{Term: "न्", Class: ClassGram},
				{Term: "्द", Class: ClassGram},
				{Term: "दी", Class: ClassGram},
			},
		},
		{
			name: "EmojiClustersAreSearchable",
			text: "❤ 👩‍💻 😀👍",
			want: []Token{
				{Term: "❤", Class: ClassWord},
				{Term: "👩‍💻", Class: ClassWord},
				{Term: "😀", Class: ClassWord},
				{Term: "👍", Class: ClassWord},
			},
		},
		{
			name: "Empty",
			text: "",
			want: nil,
		},
		{
			name: "OnlyDelimiters",
			text: " ,. !",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tok.Tokenize(tc.text)
			if !tokensEqual(got, tc.want) {
				t.Fatalf("Tokenize(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}

	t.Run("WiderGramWidth", func(t *testing.T) {
		got := ScriptAwareTokenizer{N: 3}.Tokenize("search 東京都庁")
		want := []Token{
			{Term: "search", Class: ClassWord},
			{Term: "sea", Class: ClassGram},
			{Term: "ear", Class: ClassGram},
			{Term: "arc", Class: ClassGram},
			{Term: "rch", Class: ClassGram},
			{Term: "東京都", Class: ClassWord},
			{Term: "京都庁", Class: ClassWord},
		}
		if !tokensEqual(got, want) {
			t.Fatalf("N=3 Tokenize = %v, want %v", got, want)
		}
	})

	t.Run("ShortCJKRunUnderWiderWidthEmittedWhole", func(t *testing.T) {
		got := ScriptAwareTokenizer{N: 3}.Tokenize("東京")
		want := []Token{{Term: "東京", Class: ClassWord}}
		if !tokensEqual(got, want) {
			t.Fatalf("N=3 Tokenize(東京) = %v, want %v", got, want)
		}
	})
}
