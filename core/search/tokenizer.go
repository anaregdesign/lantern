package search

import (
	"strings"
	"unicode"
)

// Token is the unit of analyzed text that flows through the package's analysis
// pipeline: a Tokenizer emits Tokens, TokenFilters rewrite or drop them, and an
// Analyzer returns the final slice that the index records and queries against.
type Token struct {
	// Term is the analyzed text of this token.
	Term string
}

// Tokenizer splits text into tokens. Implementations in this package are
// stateless and therefore safe for concurrent use by multiple goroutines.
type Tokenizer interface {
	Tokenize(text string) []Token
}

// TokenizerFunc adapts an ordinary function to the Tokenizer interface.
type TokenizerFunc func(text string) []Token

// Tokenize calls f(text).
func (f TokenizerFunc) Tokenize(text string) []Token { return f(text) }

// WhitespaceTokenizer splits text on runs of Unicode whitespace, leaving every
// other rune—including punctuation—attached to its token (so "word." stays
// "word."). Pair it with a PunctuationFilter when that is not what you want.
type WhitespaceTokenizer struct{}

// Tokenize splits text on Unicode whitespace.
func (WhitespaceTokenizer) Tokenize(text string) []Token {
	return tokensFromFields(strings.FieldsFunc(text, unicode.IsSpace))
}

// UnicodeTokenizer splits text into maximal runs of letters and digits; every
// other rune (whitespace, punctuation, symbols) is treated as a delimiter and
// dropped. Tokenizing and stripping punctuation in a single pass makes it the
// simplest language-independent choice, so it is the default tokenizer.
type UnicodeTokenizer struct{}

// Tokenize splits text on any rune that is neither a letter nor a digit.
func (UnicodeTokenizer) Tokenize(text string) []Token {
	return tokensFromFields(strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}))
}

// NGramTokenizer slides a fixed-width window of N runes across the whole input
// and emits one token per position, so "search" with N=3 yields "sea", "ear",
// "arc", "rch". Because it works on runes (not bytes) and never relies on word
// boundaries, it is the language-independent way to make substring queries and
// scripts with no whitespace between words (e.g. CJK) matchable, where the
// word-splitting tokenizers would otherwise emit one giant token.
//
// The window moves over every rune—whitespace and punctuation included—so a
// caller who wants grams that do not straddle word boundaries should strip or
// collapse those runes with a Normalizer first. Every emitted term is exactly
// N runes long, and an input shorter than N yields no tokens. N < 1 is treated
// as 1 (unigrams), so the zero value tokenizes into single characters rather
// than misbehaving.
type NGramTokenizer struct {
	N int
}

// Tokenize emits every N-rune window of text in left-to-right order.
func (t NGramTokenizer) Tokenize(text string) []Token {
	n := t.N
	if n < 1 {
		n = 1
	}
	runes := []rune(text)
	if len(runes) < n {
		return nil
	}
	tokens := make([]Token, 0, len(runes)-n+1)
	for i := 0; i+n <= len(runes); i++ {
		tokens = append(tokens, Token{Term: string(runes[i : i+n])})
	}
	return tokens
}

// tokensFromFields wraps each field string in a Token. strings.Fields and
// FieldsFunc never yield empty fields, so the result carries no empty terms.
func tokensFromFields(fields []string) []Token {
	tokens := make([]Token, len(fields))
	for i, f := range fields {
		tokens[i] = Token{Term: f}
	}
	return tokens
}
