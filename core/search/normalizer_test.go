package search

import "testing"

func TestLowercaseNormalizer(t *testing.T) {
	if got := (LowercaseNormalizer{}).Normalize("HeLLo Wörld"); got != "hello wörld" {
		t.Fatalf("Normalize = %q, want %q", got, "hello wörld")
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
