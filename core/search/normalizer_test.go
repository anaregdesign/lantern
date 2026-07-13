package search

import (
	"slices"
	"testing"
)

func TestLowercaseNormalizer(t *testing.T) {
	if got := (LowercaseNormalizer{}).Normalize("HeLLo Wörld"); got != "hello wörld" {
		t.Fatalf("Normalize = %q, want %q", got, "hello wörld")
	}
}

func TestCaseFoldNormalizer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"GermanSharpS", "Straße", "strasse"},
		{"GreekFinalSigma", "ΟΣ ος οσ", "οσ οσ οσ"},
		{"TurkishIsLanguageNeutral", "İ I ı i", "i̇ i ı i"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (CaseFoldNormalizer{}).Normalize(tc.in); got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalNormalizer(t *testing.T) {
	composed := "café"
	decomposed := "cafe\u0301"
	if got := (CanonicalNormalizer{}).Normalize(decomposed); got != composed {
		t.Fatalf("Normalize(%q) = %q, want NFC %q", decomposed, got, composed)
	}
	for _, text := range []string{"สวัสดี", "हिन्दी", "Ελλάδα", "ёж"} {
		if got := (CanonicalNormalizer{}).Normalize(text); got != text {
			t.Fatalf("Normalize(%q) = %q, want meaningful marks preserved", text, got)
		}
	}
}

func TestSpaceNormalizer(t *testing.T) {
	if got := (SpaceNormalizer{}).Normalize("  a\t\nb   c  "); got != "a b c" {
		t.Fatalf("Normalize = %q, want %q", got, "a b c")
	}
}

func TestNormalizerFunc(t *testing.T) {
	var n Normalizer = NormalizerFunc(func(s string) string { return s + "!" })
	if got := n.Normalize("x"); got != "x!" {
		t.Fatalf("Normalize = %q, want %q", got, "x!")
	}
}

func TestPunctuationNormalizer(t *testing.T) {
	// Every punctuation or symbol rune becomes a space, regardless of language.
	if got := (PunctuationNormalizer{}).Normalize("日本語。テスト、です"); got != "日本語 テスト です" {
		t.Fatalf("Normalize = %q, want %q", got, "日本語 テスト です")
	}
	// Symbols count too ('=' and '+'), and "node-1" splits on the hyphen rather
	// than being preserved as PunctuationFilter would.
	if got := (PunctuationNormalizer{}).Normalize("a=b+node-1"); got != "a b node 1" {
		t.Fatalf("Normalize = %q, want %q", got, "a b node 1")
	}
}

func TestSymbolPreservingPunctuationNormalizer(t *testing.T) {
	got := (SymbolPreservingPunctuationNormalizer{}).Normalize("go,node+❤ 👩‍💻")
	if got != "go node+❤ 👩‍💻" {
		t.Fatalf("Normalize = %q, want punctuation boundaries and symbols preserved", got)
	}
}

func TestEmojiPresentationNormalizer(t *testing.T) {
	if got := (EmojiPresentationNormalizer{}).Normalize("❤︎ ❤️"); got != "❤ ❤" {
		t.Fatalf("Normalize = %q, want presentation selectors removed", got)
	}
}

func TestPunctuationNormalizerChain(t *testing.T) {
	// Recommended pipeline: turn marks into spaces, then collapse the runs and
	// trim, so consecutive punctuation does not leave behind multiple spaces.
	got := (SpaceNormalizer{}).Normalize((PunctuationNormalizer{}).Normalize("Hello, world... 「テスト」"))
	if got != "Hello world テスト" {
		t.Fatalf("Normalize = %q, want %q", got, "Hello world テスト")
	}
}

func TestDiacriticNormalizer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"LatinAcute", "Café", "Cafe"},
		{"LatinDiaeresis", "naïve", "naive"},
		{"GreekTonos", "Ελλάδα", "Ελλαδα"},
		{"CyrillicYo", "ёж", "еж"},
		{"AtomicLetterKept", "Straße", "Straße"}, // ß is not base+mark
		{"DevanagariMatraKept", "भारत", "भारत"},  // spacing mark (Mc) preserved
		{"HangulStable", "한국", "한국"},             // NFC recomposition keeps syllables
		{"CJKNoOp", "東京", "東京"},                  // no combining marks
		{"CaseIndependent", "CAFÉ café", "CAFE cafe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (DiacriticNormalizer{}).Normalize(tc.in); got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWidthNormalizer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"FullWidthLatin", "ＴＯＫＹＯ", "TOKYO"},
		{"FullWidthDigits", "２０２４", "2024"},
		{"FullWidthPunct", "ｉＰｈｏｎｅ！", "iPhone!"},
		{"HalfWidthKatakana", "ｶﾀｶﾅ", "カタカナ"},
		{"NormalWidthNoOp", "Tokyo 2024", "Tokyo 2024"},
		{"CJKIdeographNoOp", "東京", "東京"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (WidthNormalizer{}).Normalize(tc.in); got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestJSONStringValueNormalizer(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Non-JSON text passes through untouched.
		{"PlainProse", "calm and concise", "calm and concise"},
		{"BareScalarWord", "true", "true"},
		{"BareNumberWord", "42", "42"},
		{"QuotedScalarLeftAsLiteral", `"hello"`, `"hello"`},
		{"Empty", "", ""},
		{"NotJSONButBrace", "{not valid json", "{not valid json"},
		// Objects drop field names, keep string values in sorted-key order.
		{"ObjectDropsKeys", `{"role":"admin","name":"Alice"}`, "Alice admin"},
		// Non-string scalars are filtered out.
		{"FiltersNonStrings", `{"role":"admin","score":9,"active":true,"note":null}`, "admin"},
		{"OnlyNonStrings", `{"a":1,"b":false,"c":null}`, ""},
		{"EmptyObject", "{}", ""},
		{"EmptyStringValuesSkipped", `{"a":"","b":"x"}`, "x"},
		// Arrays keep order; nested structures recurse.
		{"ArrayOfStringsFiltersScalars", `["red","green",2,true]`, "red green"},
		{"NestedObjectAndArray", `{"user":{"name":"Alice"},"tags":["go","db"],"n":5}`, "go db Alice"},
		// Surrounding whitespace is tolerated before parsing.
		{"LeadingWhitespace", `  {"x":"y"}  `, "y"},
		// A top-level array of objects.
		{"ArrayOfObjects", `[{"t":"hi"},{"t":"bye","skip":1}]`, "hi bye"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := (JSONStringValueNormalizer{}).Normalize(tc.in); got != tc.want {
				t.Fatalf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestJSONStringValueNormalizerValuesPreserveBoundaries(t *testing.T) {
	values, structured := (JSONStringValueNormalizer{}).Values(`{"b":["beta","gamma"],"a":"alpha","n":4}`)
	if !structured || !slices.Equal(values, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("Values = %v, %t", values, structured)
	}
	if values, structured := (JSONStringValueNormalizer{}).Values("plain text"); structured || values != nil {
		t.Fatalf("plain Values = %v, %t", values, structured)
	}
}
