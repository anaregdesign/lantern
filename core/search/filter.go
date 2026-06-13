package search

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenFilter post-processes a token stream produced by a Tokenizer—dropping,
// rewriting, or compacting tokens. An Analyzer applies filters in order.
// Implementations in this package are stateless and safe for concurrent use,
// but they may reuse the input slice's backing array, so a caller must not keep
// using the slice it passes in after the call returns.
type TokenFilter interface {
	Filter(tokens []Token) []Token
}

// TokenFilterFunc adapts an ordinary function to the TokenFilter interface.
type TokenFilterFunc func(tokens []Token) []Token

// Filter calls f(tokens).
func (f TokenFilterFunc) Filter(tokens []Token) []Token { return f(tokens) }

// LowercaseFilter folds each token to lower case. Use it when tokenization runs
// before case folding; if you already lowercase with a LowercaseNormalizer it
// is redundant.
type LowercaseFilter struct{}

// Filter lower-cases every term in place.
func (LowercaseFilter) Filter(tokens []Token) []Token {
	for i := range tokens {
		tokens[i].Term = strings.ToLower(tokens[i].Term)
	}
	return tokens
}

// PunctuationFilter strips leading and trailing punctuation and symbol runes
// from each token and drops tokens left empty, while preserving inner marks (so
// "hello," becomes "hello" but "node-1" and "u.s.a" are untouched). It is the
// companion to WhitespaceTokenizer; UnicodeTokenizer already drops punctuation.
type PunctuationFilter struct{}

// Filter trims edge punctuation/symbols and removes now-empty tokens.
func (PunctuationFilter) Filter(tokens []Token) []Token {
	out := tokens[:0]
	for _, t := range tokens {
		trimmed := strings.TrimFunc(t.Term, isPunctOrSymbol)
		if trimmed == "" {
			continue
		}
		out = append(out, Token{Term: trimmed})
	}
	return out
}

// EmptyTokenFilter drops tokens whose term is empty or all whitespace—cheap
// insurance after custom normalizers or filters.
type EmptyTokenFilter struct{}

// Filter removes blank tokens.
func (EmptyTokenFilter) Filter(tokens []Token) []Token {
	out := tokens[:0]
	for _, t := range tokens {
		if strings.TrimSpace(t.Term) == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// LengthFilter drops tokens whose length in runes is below Min or above Max. A
// Max of 0 means no upper bound. It removes noise such as stray single letters.
type LengthFilter struct {
	Min int
	Max int
}

// Filter keeps only tokens whose rune count is within [Min, Max].
func (f LengthFilter) Filter(tokens []Token) []Token {
	out := tokens[:0]
	for _, t := range tokens {
		n := utf8.RuneCountInString(t.Term)
		if n < f.Min || (f.Max > 0 && n > f.Max) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// StopWordFilter removes tokens that appear in a stop-word set. Matching is
// case-insensitive (terms and stop words are compared lower-cased), so the
// filter works whether or not case folding ran earlier.
type StopWordFilter struct {
	stop map[string]struct{}
}

// NewStopWordFilter builds a StopWordFilter from words. Duplicate or mixed-case
// entries are fine; each is folded to lower case.
func NewStopWordFilter(words ...string) *StopWordFilter {
	stop := make(map[string]struct{}, len(words))
	for _, w := range words {
		stop[strings.ToLower(w)] = struct{}{}
	}
	return &StopWordFilter{stop: stop}
}

// Filter removes stop-word tokens.
func (f *StopWordFilter) Filter(tokens []Token) []Token {
	out := tokens[:0]
	for _, t := range tokens {
		if _, ok := f.stop[strings.ToLower(t.Term)]; ok {
			continue
		}
		out = append(out, t)
	}
	return out
}

// isPunctOrSymbol reports whether r is a Unicode punctuation or symbol rune.
func isPunctOrSymbol(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}
