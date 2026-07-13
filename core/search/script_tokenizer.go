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
//   - A CJKBigram run — Unicode Ideographic characters, Hiragana, Katakana,
//     and Hangul, following Lucene's documented CJKBigramFilter script set —
//     emits its N-rune windows as primary tokens, exactly the strategy of
//     Lucene's CJKAnalyzer, because there the gram is the word-level unit. A
//     run shorter than N (a lone ideograph) is emitted whole, so single-
//     character words stay searchable.
//   - Combining marks continue the preceding lexical run, preserving Thai and
//     Indic identity. Each Unicode symbol is searchable as one primary token;
//     ZWJ-linked symbols form one token and adjacent unjoined symbols do not.
//   - Every other rune (whitespace and punctuation) delimits runs and is
//     dropped, so no gram ever straddles a word boundary and the
//     WhitespaceFilter step of the plain n-gram pipeline is unnecessary.
//
// Run it after the production normalizers (width, canonical composition, full
// case fold, emoji presentation, punctuation, space) — NewScriptAwareAnalyzer
// wires that pipeline. In
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
	tokens, _ := t.TokenizeBounded(text, 0)
	return tokens
}

// TokenizeBounded stops before appending token maxTokens+1. The boolean is
// true when more output exists, allowing callers to reject a large document
// without retaining its complete token stream.
func (t ScriptAwareTokenizer) TokenizeBounded(text string, maxTokens int) ([]Token, bool) {
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
			for j < len(runes) && (isUnboundedScript(runes[j]) || unicode.IsMark(runes[j])) {
				j++
			}
			var exceeded bool
			tokens, exceeded = appendRunGramsBounded(tokens, runes[i:j], n, ClassWord, maxTokens)
			if exceeded {
				return nil, true
			}
			i = j
		case isWordRune(r):
			j := i + 1
			for j < len(runes) && (isWordRune(runes[j]) || unicode.IsMark(runes[j]) || unicode.Is(unicode.Join_Control, runes[j])) {
				j++
			}
			word := runes[i:j]
			tokens = append(tokens, Token{Term: string(word), Class: ClassWord})
			if maxTokens > 0 && len(tokens) > maxTokens {
				return nil, true
			}
			if len(word) > n {
				// Document analysis omits a redundant exact-width gram. The
				// production QueryAnalyzer adds it only to queries, where it
				// closes the short-infix discontinuity without growing postings.
				var exceeded bool
				tokens, exceeded = appendRunGramsBounded(tokens, word, n, ClassGram, maxTokens)
				if exceeded {
					return nil, true
				}
			}
			i = j
		case unicode.IsSymbol(r):
			j := symbolClusterEnd(runes, i)
			tokens = append(tokens, Token{Term: string(runes[i:j]), Class: ClassWord})
			if maxTokens > 0 && len(tokens) > maxTokens {
				return nil, true
			}
			i = j
		default:
			i++ // delimiter: whitespace, punctuation, orphaned mark/control
		}
	}
	return tokens, false
}

func symbolClusterEnd(runes []rune, start int) int {
	j := start + 1
	for j < len(runes) && (unicode.IsMark(runes[j]) || isEmojiModifier(runes[j])) {
		j++
	}
	for j+1 < len(runes) && runes[j] == '\u200d' && unicode.IsSymbol(runes[j+1]) {
		j += 2
		for j < len(runes) && (unicode.IsMark(runes[j]) || isEmojiModifier(runes[j])) {
			j++
		}
	}
	return j
}

func isEmojiModifier(r rune) bool { return r >= 0x1f3fb && r <= 0x1f3ff }

// appendRunGramsBounded emits every n-rune window, stopping at the cap. A
// short run is emitted whole (the CJKBigramFilter unigram fallback).
func appendRunGramsBounded(tokens []Token, run []rune, n int, class TokenClass, maxTokens int) ([]Token, bool) {
	if len(run) < n {
		tokens = append(tokens, Token{Term: string(run), Class: class})
		return tokens, maxTokens > 0 && len(tokens) > maxTokens
	}
	for i := 0; i+n <= len(run); i++ {
		tokens = append(tokens, Token{Term: string(run[i : i+n]), Class: class})
		if maxTokens > 0 && len(tokens) > maxTokens {
			return nil, true
		}
	}
	return tokens, false
}

// unboundedScripts mirrors Lucene CJKBigramFilter's documented script set.
// Han membership is derived from Unicode's Ideographic property below rather
// than a hand-maintained block list; the remaining scripts have standard Go
// Unicode tables. Southeast Asian scripts follow UAX #29's default lexical
// runs and retain auxiliary grams, avoiding an invented per-script list.
var unboundedScripts = []*unicode.RangeTable{
	unicode.Hiragana,
	unicode.Katakana,
	unicode.Hangul,
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
	if unicode.Is(unicode.Ideographic, r) {
		return true
	}
	for _, table := range unboundedScripts {
		if unicode.Is(table, r) {
			return true
		}
	}
	return false
}

func isCJKPrimaryTerm(term string) bool {
	if term == "" {
		return false
	}
	for _, r := range term {
		if !isUnboundedScript(r) && !unicode.IsMark(r) {
			return false
		}
	}
	return true
}

// isWordRune reports whether r continues a word run of a space-delimited
// script: letters and digits, minus the runes the unbounded scripts claim.
func isWordRune(r rune) bool {
	return (unicode.IsLetter(r) || unicode.IsDigit(r)) && !isUnboundedScript(r)
}

// Interface assertion: ScriptAwareTokenizer is a Tokenizer.
var _ Tokenizer = ScriptAwareTokenizer{}
var _ boundedTokenizer = ScriptAwareTokenizer{}
