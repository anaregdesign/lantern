package search

import "unicode"

// ScriptAwareTokenizer is the production tokenizer for mixed-script content
// search (#888). It splits text into script runs and picks the right unit per
// run, instead of forcing one strategy onto every language the way a pure
// word or pure n-gram tokenizer does:
//
//   - A run of a space-delimited script (Latin, Cyrillic, Greek, digits, …)
//     emits the whole word as a primary token (ClassWord) — the unit Lucene's
//     StandardAnalyzer indexes, and what lets a whole-word match outrank an
//     infix fragment. Words of at least N runes additionally emit every
//     N-rune window as auxiliary tokens (ClassGram), which is what keeps the
//     bigram pipeline's infix recall ("arch" finding "search") and typo
//     tolerance; pair the index with ClassWeighted so this redundant channel
//     stays evidence, not the ranking.
//   - A run of an unbounded script — one written without spaces between
//     words: Han, Hiragana, Katakana, Hangul, Thai, Lao, Khmer, Myanmar —
//     emits its N-rune windows as primary tokens, exactly the strategy of
//     Lucene's CJKAnalyzer, because there the gram is the word-level unit. A
//     run shorter than N (a lone ideograph) is emitted whole, so single-
//     character words stay searchable.
//   - Every other rune (whitespace, punctuation, symbols) delimits runs and
//     is dropped, so no gram ever straddles a word boundary and the
//     WhitespaceFilter step of the plain n-gram pipeline is unnecessary.
//
// Run it after the folding normalizers (width, diacritic, lowercase,
// punctuation, space) — NewScriptAwareAnalyzer wires that pipeline. In
// particular WidthNormalizer must run first so half-width katakana joins the
// katakana run it belongs to. N < 2 is treated as 2 (bigrams), so the zero
// value is the production configuration. It holds no state and is safe for
// concurrent use.
type ScriptAwareTokenizer struct {
	// N is the gram width, for unbounded-script runs and the auxiliary
	// intra-word grams alike. N < 2 is treated as 2.
	N int
}

// Tokenize splits text into script runs and emits per-run tokens as described
// on the type.
func (t ScriptAwareTokenizer) Tokenize(text string) []Token {
	n := t.N
	if n < 2 {
		n = 2
	}
	runes := []rune(text)
	var tokens []Token
	for i := 0; i < len(runes); {
		switch r := runes[i]; {
		case isUnboundedScript(r):
			j := i + 1
			for j < len(runes) && isUnboundedScript(runes[j]) {
				j++
			}
			tokens = appendRunGrams(tokens, runes[i:j], n, ClassWord)
			i = j
		case isWordRune(r):
			j := i + 1
			for j < len(runes) && isWordRune(runes[j]) {
				j++
			}
			word := runes[i:j]
			tokens = append(tokens, Token{Term: string(word), Class: ClassWord})
			if len(word) > n {
				// Strictly longer than one window: a word of exactly N runes
				// would emit itself again, and the word token already carries
				// that evidence on the primary channel.
				tokens = appendRunGrams(tokens, word, n, ClassGram)
			}
			i = j
		default:
			i++ // delimiter: whitespace, punctuation, symbols
		}
	}
	return tokens
}

// appendRunGrams appends every n-rune window of run as a token of the given
// class; a run shorter than n is emitted whole, so it stays matchable rather
// than vanishing (the CJKBigramFilter unigram fallback).
func appendRunGrams(tokens []Token, run []rune, n int, class TokenClass) []Token {
	if len(run) < n {
		return append(tokens, Token{Term: string(run), Class: class})
	}
	for i := 0; i+n <= len(run); i++ {
		tokens = append(tokens, Token{Term: string(run[i : i+n]), Class: class})
	}
	return tokens
}

// unboundedScripts are the scripts written without spaces between words, for
// which n-grams are the word-level indexing unit: the CJKBigramFilter set
// (Han, Hiragana, Katakana, Hangul) plus the space-free Southeast Asian
// scripts.
var unboundedScripts = []*unicode.RangeTable{
	unicode.Han,
	unicode.Hiragana,
	unicode.Katakana,
	unicode.Hangul,
	unicode.Thai,
	unicode.Lao,
	unicode.Khmer,
	unicode.Myanmar,
}

// isUnboundedScript reports whether r belongs to a script indexed by n-grams.
// The prolonged-sound and iteration marks (ー, 々, 〆) carry script Common but
// occur inside Japanese words ("ラーメン", "人々"), so they are kept in the run
// rather than treated as delimiters.
func isUnboundedScript(r rune) bool {
	switch r {
	case 'ー', '々', '〆':
		return true
	}
	for _, table := range unboundedScripts {
		if unicode.Is(table, r) {
			return true
		}
	}
	return false
}

// isWordRune reports whether r continues a word run of a space-delimited
// script: letters and digits, minus the runes the unbounded scripts claim.
func isWordRune(r rune) bool {
	return (unicode.IsLetter(r) || unicode.IsDigit(r)) && !isUnboundedScript(r)
}

// Interface assertion: ScriptAwareTokenizer is a Tokenizer.
var _ Tokenizer = ScriptAwareTokenizer{}
