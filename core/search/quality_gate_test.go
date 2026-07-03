package search

import (
	"sort"
	"testing"
)

// This is a cross-cutting quality gate: it drives the exact pipeline the
// example installs — LowercaseNormalizer + PunctuationNormalizer +
// SpaceNormalizer, an NGramTokenizer at N=2, the WhitespaceFilter quality
// filter, and BM25 — end to end through Index and Search, so it exercises the
// analyzer, tokenizer, filter, inverted index, and scorer together. It proves
// the one combination actually registers and finds documents across many
// writing systems and survives the corner cases that bite n-gram search.

// gateAnalyzer is the example's analyzer: bigrams kept inside word boundaries.
func gateAnalyzer() Analyzer {
	return NewAnalyzer(
		[]Normalizer{LowercaseNormalizer{}, PunctuationNormalizer{}, SpaceNormalizer{}},
		NGramTokenizer{N: 2},
		[]TokenFilter{WhitespaceFilter{}},
	)
}

// gateIndex builds a fresh index wired with the example's analyzer and scorer.
func gateIndex() *InvertedIndex[string, Text] {
	return NewInvertedIndex[string, Text](gateAnalyzer(), BM25{K1: 1.2, B: 0.75})
}

// foldingAnalyzer extends the gate analyzer with the width and diacritic
// folding normalizers, so a query typed at a different character width or
// without accents still resolves to the same grams as the document.
func foldingAnalyzer() Analyzer {
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

// foldingIndex builds a fresh index wired with the folding analyzer and the
// example's scorer.
func foldingIndex() *InvertedIndex[string, Text] {
	return NewInvertedIndex[string, Text](foldingAnalyzer(), BM25{K1: 1.2, B: 0.75})
}

// TestSearchQualityGateMultilingual indexes a small corpus per case and asserts
// both recall (the right documents match) and ranking (the best match is on
// top) across Latin, Cyrillic, Greek, Arabic, Hebrew, Devanagari, Han, Kana,
// Hangul, Thai, emoji, and digits — plus infix and compound-word behavior.
func TestSearchQualityGateMultilingual(t *testing.T) {
	cases := []struct {
		name     string
		docs     map[string]string
		query    string
		wantTop  string
		wantHits []string
	}{
		{
			name:     "EnglishInfix",
			docs:     map[string]string{"search": "Full-text search.", "research": "Academic  research", "arch": "A stone arch", "panda": "A giant panda"},
			query:    "arch",
			wantTop:  "arch",
			wantHits: []string{"arch", "research", "search"},
		},
		{
			name:     "EnglishCaseInsensitive",
			docs:     map[string]string{"apple": "Apple", "melon": "Melon"},
			query:    "APPLE",
			wantTop:  "apple",
			wantHits: []string{"apple"},
		},
		{
			name:     "GermanUmlautSameCase",
			docs:     map[string]string{"street": "Straße", "house": "Haus"},
			query:    "straße",
			wantTop:  "street",
			wantHits: []string{"street"},
		},
		{
			name:     "FrenchAccent",
			docs:     map[string]string{"cafe": "Café", "tea": "Thé"},
			query:    "CAFÉ",
			wantTop:  "cafe",
			wantHits: []string{"cafe"},
		},
		{
			name:     "RussianCyrillic",
			docs:     map[string]string{"book": "Книга", "house": "Дом"},
			query:    "книга",
			wantTop:  "book",
			wantHits: []string{"book"},
		},
		{
			name:     "GreekFolding",
			docs:     map[string]string{"hellas": "Ελλάδα", "athens": "Αθήνα"},
			query:    "Ελλάδα",
			wantTop:  "hellas",
			wantHits: []string{"hellas"},
		},
		{
			name:     "ArabicRTL",
			docs:     map[string]string{"book": "كتاب", "pen": "قلم"},
			query:    "كتاب",
			wantTop:  "book",
			wantHits: []string{"book"},
		},
		{
			name:     "HebrewRTL",
			docs:     map[string]string{"book": "ספר", "pen": "עט"},
			query:    "ספר",
			wantTop:  "book",
			wantHits: []string{"book"},
		},
		{
			name:     "HindiDevanagari",
			docs:     map[string]string{"india": "भारत", "delhi": "दिल्ली"},
			query:    "भारत",
			wantTop:  "india",
			wantHits: []string{"india"},
		},
		{
			name:     "JapaneseKanjiCompound",
			docs:     map[string]string{"tokyo": "東京", "tokyoto": "東京都"},
			query:    "東京",
			wantTop:  "tokyo",
			wantHits: []string{"tokyo", "tokyoto"},
		},
		{
			name:     "JapaneseKyotoInfixGuard",
			docs:     map[string]string{"kyoto": "京都", "tokyoto": "東京都"},
			query:    "京都",
			wantTop:  "kyoto",
			wantHits: []string{"kyoto", "tokyoto"},
		},
		{
			name:     "JapaneseKatakanaInfix",
			docs:     map[string]string{"tower": "東京タワー", "sky": "スカイツリー"},
			query:    "タワー",
			wantTop:  "tower",
			wantHits: []string{"tower"},
		},
		{
			name:     "ChineseCompound",
			docs:     map[string]string{"cn": "中国", "cnp": "中国人"},
			query:    "中国",
			wantTop:  "cn",
			wantHits: []string{"cn", "cnp"},
		},
		{
			name:     "KoreanHangul",
			docs:     map[string]string{"kr": "한국", "seoul": "서울"},
			query:    "한국",
			wantTop:  "kr",
			wantHits: []string{"kr"},
		},
		{
			name:     "ThaiNoSpacesInfix",
			docs:     map[string]string{"hello": "สวัสดี", "thanks": "ขอบคุณ"},
			query:    "สวัส",
			wantTop:  "hello",
			wantHits: []string{"hello"},
		},
		{
			name:     "EmojiStrippedToBoundary",
			docs:     map[string]string{"ny": "I ❤ NY", "la": "LA sunshine"},
			query:    "ny",
			wantTop:  "ny",
			wantHits: []string{"ny"},
		},
		{
			name:     "DigitsExact",
			docs:     map[string]string{"iphone": "iPhone 15", "pixel": "Pixel 9"},
			query:    "15",
			wantTop:  "iphone",
			wantHits: []string{"iphone"},
		},
		{
			name:     "LatinInfixWord",
			docs:     map[string]string{"iphone": "iPhone 15", "pixel": "Pixel 9"},
			query:    "phone",
			wantTop:  "iphone",
			wantHits: []string{"iphone"},
		},
		{
			name:     "MixedScriptDocument",
			docs:     map[string]string{"mix": "Café 日本", "other": "Tea 中国"},
			query:    "日本",
			wantTop:  "mix",
			wantHits: []string{"mix"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := gateIndex()
			for id, text := range tc.docs {
				idx.Index(id, Text(text))
			}
			results := idx.Search(tc.query)
			if got := idsOf(results); !sortedEqual(got, tc.wantHits) {
				t.Fatalf("Search(%q) hits = %v, want %v", tc.query, got, tc.wantHits)
			}
			if len(results) == 0 || results[0].ID != tc.wantTop {
				t.Fatalf("Search(%q) top = %v, want %q", tc.query, idsOf(results), tc.wantTop)
			}
		})
	}
}

// TestSearchQualityGateCornerCases pins down the behaviors that quietly break
// n-gram search: queries that analyze to nothing, queries shorter than the
// gram width, repeated query terms, deletion, and re-indexing.
func TestSearchQualityGateCornerCases(t *testing.T) {
	t.Run("UnanalyzableQueriesReturnNil", func(t *testing.T) {
		idx := gateIndex()
		idx.Index("en", Text("hello world"))
		idx.Index("jp", Text("東京タワー"))
		// Empty, whitespace-only, punctuation-only, and any query shorter than
		// the 2-rune gram (Latin or CJK) share no gram and must match nothing.
		for _, q := range []string{"", "   \t\n ", "!!! --- ???", "h", "x", "東"} {
			if got := idx.Search(q); got != nil {
				t.Fatalf("Search(%q) = %v, want nil", q, got)
			}
		}
	})
	t.Run("RepeatedQueryTermWeightedOnce", func(t *testing.T) {
		idx := gateIndex()
		idx.Index("jp", Text("東京"))
		one := idx.Search("東京")
		two := idx.Search("東京 東京")
		if len(one) != 1 || len(two) != 1 {
			t.Fatalf("len(one)=%d len(two)=%d, want 1 and 1", len(one), len(two))
		}
		if one[0].ID != two[0].ID || !approxEqual(one[0].Score, two[0].Score) {
			t.Fatalf("repeated-term query changed result: %+v vs %+v", one[0], two[0])
		}
	})
	t.Run("DeleteRemovesDocument", func(t *testing.T) {
		idx := gateIndex()
		idx.Index("a", Text("hello"))
		if got := idsOf(idx.Search("hello")); !sortedEqual(got, []string{"a"}) {
			t.Fatalf("before delete: %v", got)
		}
		idx.Delete("a")
		if got := idx.Search("hello"); got != nil {
			t.Fatalf("after delete: %v, want nil", got)
		}
	})
	t.Run("ReindexReplacesPostings", func(t *testing.T) {
		idx := gateIndex()
		idx.Index("a", Text("東京"))
		if got := idsOf(idx.Search("東京")); !sortedEqual(got, []string{"a"}) {
			t.Fatalf("before reindex: %v", got)
		}
		idx.Index("a", Text("京都"))
		if got := idx.Search("東京"); got != nil {
			t.Fatalf("stale posting after reindex: %v", got)
		}
		if got := idsOf(idx.Search("京都")); !sortedEqual(got, []string{"a"}) {
			t.Fatalf("after reindex: %v", got)
		}
	})
}

// TestSearchQualityGateIntraWordGrams shows the WhitespaceFilter's contribution
// directly: every emitted gram stays inside one word, so the cross-boundary
// grams ("b ", " c", "京 ", " タ") never reach the index.
func TestSearchQualityGateIntraWordGrams(t *testing.T) {
	a := gateAnalyzer()
	if got := termsOf(a.Analyze("ab cd")); !equalStrings(got, []string{"ab", "cd"}) {
		t.Fatalf("Analyze(%q) = %v, want [ab cd]", "ab cd", got)
	}
	if got := termsOf(a.Analyze("東京 タワー")); !equalStrings(got, []string{"東京", "タワ", "ワー"}) {
		t.Fatalf("Analyze(%q) = %v, want [東京 タワ ワー]", "東京 タワー", got)
	}
}

// TestSearchQualityGateFolding proves the width and diacritic folding
// normalizers raise recall: a query typed without accents, with a different
// case, or in a different character width still finds its document across
// Latin, Greek, Cyrillic, and CJK width variants. Each case pairs the target
// with a decoy whose grams are disjoint, so a hit is real recall, not a
// collision.
func TestSearchQualityGateFolding(t *testing.T) {
	cases := []struct {
		name     string
		docs     map[string]string
		query    string
		wantTop  string
		wantHits []string
	}{
		{
			name:     "AccentInsensitiveLatin",
			docs:     map[string]string{"cafe": "Café", "melon": "Melon"},
			query:    "cafe",
			wantTop:  "cafe",
			wantHits: []string{"cafe"},
		},
		{
			name:     "DiaeresisFolded",
			docs:     map[string]string{"naive": "naïve", "story": "story"},
			query:    "naive",
			wantTop:  "naive",
			wantHits: []string{"naive"},
		},
		{
			name:     "GreekAccentFolded",
			docs:     map[string]string{"hellas": "Ελλάδα", "athens": "Αθήνα"},
			query:    "ελλαδα",
			wantTop:  "hellas",
			wantHits: []string{"hellas"},
		},
		{
			name:     "CyrillicYoFolded",
			docs:     map[string]string{"hedgehog": "ёж", "house": "дом"},
			query:    "еж",
			wantTop:  "hedgehog",
			wantHits: []string{"hedgehog"},
		},
		{
			name:     "FullWidthLatin",
			docs:     map[string]string{"tokyo": "ＴＯＫＹＯ", "osaka": "Osaka"},
			query:    "tokyo",
			wantTop:  "tokyo",
			wantHits: []string{"tokyo"},
		},
		{
			name:     "FullWidthDigits",
			docs:     map[string]string{"iphone": "iPhone １５", "pixel": "Pixel ９"},
			query:    "15",
			wantTop:  "iphone",
			wantHits: []string{"iphone"},
		},
		{
			name:     "HalfWidthKatakana",
			docs:     map[string]string{"tower": "ﾀﾜｰ", "sky": "スカイ"},
			query:    "タワー",
			wantTop:  "tower",
			wantHits: []string{"tower"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := foldingIndex()
			for id, text := range tc.docs {
				idx.Index(id, Text(text))
			}
			results := idx.Search(tc.query)
			if got := idsOf(results); !sortedEqual(got, tc.wantHits) {
				t.Fatalf("Search(%q) hits = %v, want %v", tc.query, got, tc.wantHits)
			}
			if len(results) == 0 || results[0].ID != tc.wantTop {
				t.Fatalf("Search(%q) top = %v, want %q", tc.query, idsOf(results), tc.wantTop)
			}
		})
	}
}

// sortedEqual reports whether got and want hold the same IDs regardless of
// order, so set-membership assertions do not depend on score ties.
func sortedEqual(got, want []string) bool {
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	return equalStrings(g, w)
}

// approxEqual reports whether a and b are within a small epsilon, so score
// comparisons tolerate floating-point noise.
func approxEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

// scriptAwareIndex builds a fresh index wired exactly like the production
// content-search index since #888 (graphcache.newSearchIndex): the
// script-aware dual-channel analyzer with class-weighted BM25.
func scriptAwareIndex() *InvertedIndex[string, Text] {
	return NewInvertedIndex[string, Text](
		NewScriptAwareAnalyzer(),
		ClassWeighted{Base: BM25{K1: DefaultBM25K1, B: DefaultBM25B}, GramWeight: DefaultGramWeight},
	)
}

// TestSearchQualityGateScriptAware gates the #888 pipeline end to end on the
// behaviors the dual-channel design promises beyond the bigram pipeline:
// whole-word matches dominate infix fragments while infix recall survives,
// single-character words become searchable, folding still applies, and CJK
// behavior matches the bigram strategy it kept.
func TestSearchQualityGateScriptAware(t *testing.T) {
	cases := []struct {
		name     string
		docs     map[string]string
		query    string
		wantTop  string
		wantHits []string
	}{
		{
			// The bigram gate above pins "arch" ranking by fragment strength;
			// here the whole word must win while fragments still surface.
			name:     "WholeWordBeatsInfix",
			docs:     map[string]string{"search": "Full-text search.", "research": "Academic  research", "arch": "A stone arch", "panda": "A giant panda"},
			query:    "arch",
			wantTop:  "arch",
			wantHits: []string{"arch", "research", "search"},
		},
		{
			name:     "SingleLetterWordSearchable",
			docs:     map[string]string{"unit": "vitamin b complex", "other": "vitamin c serum"},
			query:    "b",
			wantTop:  "unit",
			wantHits: []string{"unit"},
		},
		{
			name:     "TypoStillRecalls",
			docs:     map[string]string{"k8s": "kubernetes deployment guide", "cook": "carbonara recipe"},
			query:    "kubernets",
			wantTop:  "k8s",
			wantHits: []string{"k8s"},
		},
		{
			// kyoto also surfaces — "tokyo" and "kyoto" share the grams
			// to/ky/yo on the auxiliary channel — but the whole-word match
			// must stay on top.
			name:     "WidthAndCaseFoldAcrossChannels",
			docs:     map[string]string{"tokyo": "ＴＯＫＹＯ tower", "kyoto": "Kyoto station"},
			query:    "tokyo",
			wantTop:  "tokyo",
			wantHits: []string{"kyoto", "tokyo"},
		},
		{
			name:     "CJKBigramMatching",
			docs:     map[string]string{"ramen": "東京の醤油ラーメン", "sushi": "銀座の寿司職人"},
			query:    "ラーメン",
			wantTop:  "ramen",
			wantHits: []string{"ramen"},
		},
		{
			name:     "LoneIdeographSearchable",
			docs:     map[string]string{"noodle": "麺", "rice": "米"},
			query:    "麺",
			wantTop:  "noodle",
			wantHits: []string{"noodle"},
		},
		{
			// backup also surfaces — アップデート and バックアップ share the
			// CJK grams アッ/ップ under the OR union — but the document
			// matching both query halves must stay on top.
			name:     "MixedScriptValue",
			docs:     map[string]string{"deploy": "Kubernetesのローリングアップデート", "backup": "PostgreSQLのバックアップ"},
			query:    "kubernetes アップデート",
			wantTop:  "deploy",
			wantHits: []string{"backup", "deploy"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := scriptAwareIndex()
			for id, text := range tc.docs {
				idx.Index(id, Text(text))
			}
			results := idx.Search(tc.query)
			if len(results) == 0 {
				t.Fatalf("Search(%q) returned nothing", tc.query)
			}
			if results[0].ID != tc.wantTop {
				t.Fatalf("Search(%q) top = %q, want %q (results %v)", tc.query, results[0].ID, tc.wantTop, results)
			}
			got := idsOf(results)
			sort.Strings(got)
			want := append([]string(nil), tc.wantHits...)
			sort.Strings(want)
			if !equalStrings(got, want) {
				t.Fatalf("Search(%q) hits = %v, want %v", tc.query, got, want)
			}
		})
	}
}
