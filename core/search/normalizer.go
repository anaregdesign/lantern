package search

import "strings"

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
