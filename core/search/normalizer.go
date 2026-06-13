package search

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// Normalizer rewrites raw text before tokenization—for example folding case or
// collapsing whitespace. Implementations in this package are stateless and
// therefore safe for concurrent use by multiple goroutines.
type Normalizer interface {
	Normalize(text string) string
}

// NormalizerFunc adapts an ordinary function to the Normalizer interface.
type NormalizerFunc func(text string) string

// Normalize calls f(text).
func (f NormalizerFunc) Normalize(text string) string { return f(text) }

// LowercaseNormalizer folds text to lower case with Unicode case mapping, so
// matching is case-insensitive independent of language.
type LowercaseNormalizer struct{}

// Normalize returns the lower-cased text.
func (LowercaseNormalizer) Normalize(text string) string { return strings.ToLower(text) }

// SpaceNormalizer collapses every run of Unicode whitespace to a single ASCII
// space and trims the ends. It is useful in front of a WhitespaceTokenizer when
// the input may contain tabs, newlines, or repeated spaces.
type SpaceNormalizer struct{}

// Normalize collapses internal whitespace runs to single spaces.
func (SpaceNormalizer) Normalize(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// PunctuationNormalizer replaces every Unicode punctuation or symbol rune with
// an ASCII space, turning marks such as '.', ',', '。', and '、' into word
// boundaries independent of language. It does not merge the spaces it
// introduces; chain SpaceNormalizer after it to collapse the resulting runs and
// trim the ends. Unlike PunctuationFilter, which only trims marks from the
// edges of an already-formed token (so "node-1" stays intact), this splits on
// every mark, so "node-1" becomes "node 1".
type PunctuationNormalizer struct{}

// Normalize replaces each punctuation or symbol rune with a space.
func (PunctuationNormalizer) Normalize(text string) string {
	return strings.Map(func(r rune) rune {
		if isPunctOrSymbol(r) {
			return ' '
		}
		return r
	}, text)
}

// DiacriticNormalizer folds accents and other nonspacing combining marks so
// matching is diacritic-insensitive: it decomposes text to Unicode NFD, drops
// every rune in category Mn (nonspacing mark), then recomposes to NFC. This
// makes "café" match "cafe", "naïve" match "naive", Greek "Ελλάδα" match
// "Ελλαδα", and Russian "ёж" match "еж" — across any script that encodes its
// accents as combining marks (Latin, Greek, Cyrillic, Vietnamese, …). It does
// not transliterate between scripts and never alters a base letter, so letters
// that are atomic rather than base-plus-mark — "ß", "ø", "ł" — are left intact.
//
// Because it removes every nonspacing mark, it also strips the optional
// harakat of Arabic, the niqqud of Hebrew, and the (non-optional) vowel marks
// of Thai, trading some precision for recall in those scripts. It preserves
// spacing combining marks (category Mc), so Brahmic base+matra clusters such as
// Devanagari "भारत" keep their vowels, and the NFC recomposition keeps Hangul
// syllables and other composed forms stable. It folds independently of case,
// so its order relative to LowercaseNormalizer does not matter, and it is a
// no-op for scripts written without combining marks (CJK, Hangul).
type DiacriticNormalizer struct{}

// Normalize strips nonspacing combining marks via NFD → drop Mn → NFC.
func (DiacriticNormalizer) Normalize(text string) string {
	decomposed := norm.NFD.String(text)
	stripped := strings.Map(dropNonspacingMark, decomposed)
	return norm.NFC.String(stripped)
}

// WidthNormalizer folds full-width and half-width characters to their normal
// width with Unicode width folding, so the same word written at either width
// indexes and queries identically — a frequent need for CJK input. Full-width
// Latin letters, digits, and punctuation collapse to ASCII ("ＴＯＫＹＯ" → "TOKYO",
// "２０２４" → "2024", "！" → "!"), and half-width katakana expands to full-width
// ("ｶﾀｶﾅ" → "カタカナ"). It does not fold case, strip accents, or expand
// compatibility forms such as ligatures; chain LowercaseNormalizer and
// DiacriticNormalizer for those. Run it early — before PunctuationNormalizer —
// so width-folded punctuation is then treated as a word boundary. It is a no-op
// for text that already uses only normal-width characters, ordinary CJK
// ideographs ("東京") included.
type WidthNormalizer struct{}

// Normalize folds full-width and half-width variants to their normal width.
func (WidthNormalizer) Normalize(text string) string {
	return width.Fold.String(text)
}

// dropNonspacingMark returns r unless r is a Unicode nonspacing combining mark
// (category Mn), in which case it returns -1 so strings.Map removes it.
func dropNonspacingMark(r rune) rune {
	if unicode.Is(unicode.Mn, r) {
		return -1
	}
	return r
}
