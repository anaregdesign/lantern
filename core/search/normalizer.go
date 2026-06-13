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

// Normalizers chains normalizers so the output of each feeds the next, in
// order. Passing none returns an identity normalizer.
func Normalizers(normalizers ...Normalizer) Normalizer {
	chain := make([]Normalizer, len(normalizers))
	copy(chain, normalizers)
	return NormalizerFunc(func(text string) string {
		for _, n := range chain {
			text = n.Normalize(text)
		}
		return text
	})
}

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
