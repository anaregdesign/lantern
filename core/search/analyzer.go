package search

import "unicode/utf8"

// Analyzer converts raw text into index terms. Queries use the same Analyze
// method unless the dynamic implementation also provides QueryAnalyzer for a
// documented query-only recall channel. Implementations in this package are
// stateless and safe for concurrent use by multiple goroutines.
type Analyzer interface {
	Analyze(text string) []Token
}

// QueryAnalyzer is an optional asymmetric analysis extension. An index always
// calls Analyze for documents; when the dynamic Analyzer also implements this
// interface it calls AnalyzeQuery for search input. Match-mode coverage remains
// defined solely by ClassWord; extra ClassGram terms are ranking evidence.
type QueryAnalyzer interface {
	Analyzer
	AnalyzeQuery(text string) []Token
}

// boundedAnalyzer lets the index stop a production analysis pipeline as soon
// as its token budget is crossed, rather than materializing the remainder.
type boundedAnalyzer interface {
	AnalyzeBounded(text string, maxTokens int) ([]Token, bool)
}

// AnalyzerFunc adapts an ordinary function to the Analyzer interface.
type AnalyzerFunc func(text string) []Token

// Analyze calls f(text).
func (f AnalyzerFunc) Analyze(text string) []Token { return f(text) }

// pipelineAnalyzer is the standard analysis chain: zero or more Normalizers
// rewrite the raw text in order, a Tokenizer splits it into terms, and zero or
// more TokenFilters post-process the term stream in order. It holds no mutable
// state, so one instance may be shared across goroutines.
type pipelineAnalyzer struct {
	normalizers []Normalizer
	tokenizer   Tokenizer
	filters     []TokenFilter
}

// NewAnalyzer builds an Analyzer from an ordered list of normalizers (each
// applied in turn; pass nil or an empty slice to skip normalization), a
// tokenizer, and an ordered list of token filters (each applied in turn; pass
// nil or an empty slice to skip filtering). The tokenizer is required:
// NewAnalyzer panics on a nil tokenizer rather than substituting a default, so
// the analysis pipeline is always exactly what the caller spelled out. Pass
// UnicodeTokenizer{} explicitly for the language-independent default.
func NewAnalyzer(normalizers []Normalizer, tokenizer Tokenizer, filters []TokenFilter) Analyzer {
	if tokenizer == nil {
		panic("search: NewAnalyzer requires a non-nil tokenizer")
	}
	return &pipelineAnalyzer{
		normalizers: normalizers,
		tokenizer:   tokenizer,
		filters:     filters,
	}
}

// NewNGramAnalyzer returns the batteries-included analyzer for substring and
// no-whitespace-script search: it lowercases the text and then emits every
// n-rune window with an NGramTokenizer, so a query matches a document whenever
// they share an n-gram. Prefer it over a word-splitting analyzer (NewAnalyzer
// with a Unicode or Whitespace tokenizer) when words are not whitespace
// delimited (e.g. CJK, where the word-splitting tokenizers emit one
// giant token) or when infix matches matter ("arch" finding "search"). Because
// the same analyzer runs on both sides, queries must use the same n: a query
// shorter than n runes shares no n-gram with anything and therefore matches
// nothing. Like NGramTokenizer, the window slides over whitespace and
// punctuation too, so grams may straddle word boundaries; normalize the text
// first if that matters. n < 1 is treated as 1 (unigrams).
func NewNGramAnalyzer(n int) Analyzer {
	return NewAnalyzer([]Normalizer{LowercaseNormalizer{}}, NGramTokenizer{N: n}, nil)
}

// NewScriptAwareAnalyzer returns the production analyzer for mixed-script
// content search (#888, #1067): width folding, canonical normalization, full
// Unicode case folding, emoji-presentation folding, punctuation boundaries,
// and space normalization feeding a
// ScriptAwareTokenizer at N = 2. Space-delimited scripts index whole words as
// primary tokens plus intra-word bigrams as auxiliary tokens, and unbounded
// (CJK-like) scripts index bigrams as primary tokens, so one analyzer serves
// word-precise ranking for languages with word boundaries and Lucene
// CJKAnalyzer-style recall for those without. Pair the index with a
// ClassWeighted scorer so the auxiliary grams keep infix and typo recall
// without outranking whole-word matches. No token filter is needed: the
// tokenizer already drops delimiters and never emits a gram across a word
// boundary. Query analysis adds a ClassGram copy of a two-rune primary term,
// making "ar" recall "search" while the exact whole-word document still wins
// through the higher-weight ClassWord channel. Document postings stay unchanged.
func NewScriptAwareAnalyzer() Analyzer {
	pipeline := NewAnalyzer(
		[]Normalizer{
			WidthNormalizer{},
			CanonicalNormalizer{},
			CaseFoldNormalizer{},
			EmojiPresentationNormalizer{},
			SymbolPreservingPunctuationNormalizer{},
			SpaceNormalizer{},
		},
		ScriptAwareTokenizer{N: 2},
		nil,
	)
	return &scriptAwareAnalyzer{pipeline: pipeline.(*pipelineAnalyzer)}
}

type scriptAwareAnalyzer struct {
	pipeline *pipelineAnalyzer
}

func (a *scriptAwareAnalyzer) Analyze(text string) []Token {
	return a.pipeline.Analyze(text)
}

func (a *scriptAwareAnalyzer) AnalyzeBounded(text string, maxTokens int) ([]Token, bool) {
	return a.pipeline.AnalyzeBounded(text, maxTokens)
}

func (a *scriptAwareAnalyzer) AnalyzeQuery(text string) []Token {
	tokens := a.Analyze(text)
	baseLen := len(tokens)
	for i := 0; i < baseLen; i++ {
		if tokens[i].Class == ClassWord && utf8.RuneCountInString(tokens[i].Term) == 2 && !isCJKPrimaryTerm(tokens[i].Term) {
			tokens = append(tokens, Token{Term: tokens[i].Term, Class: ClassGram})
		}
	}
	return tokens
}

var _ QueryAnalyzer = (*scriptAwareAnalyzer)(nil)
var _ boundedAnalyzer = (*scriptAwareAnalyzer)(nil)

// Analyze runs text through the normalizers, tokenizer, and filter chain.
func (a *pipelineAnalyzer) Analyze(text string) []Token {
	for _, n := range a.normalizers {
		text = n.Normalize(text)
	}
	tokens := a.tokenizer.Tokenize(text)
	for _, filter := range a.filters {
		tokens = filter.Filter(tokens)
	}
	return tokens
}

func (a *pipelineAnalyzer) AnalyzeBounded(text string, maxTokens int) ([]Token, bool) {
	for _, n := range a.normalizers {
		text = n.Normalize(text)
	}
	var tokens []Token
	var exceeded bool
	if t, ok := a.tokenizer.(boundedTokenizer); ok {
		tokens, exceeded = t.TokenizeBounded(text, maxTokens)
	} else {
		tokens = a.tokenizer.Tokenize(text)
		exceeded = maxTokens > 0 && len(tokens) > maxTokens
	}
	if exceeded {
		return nil, true
	}
	for _, filter := range a.filters {
		tokens = filter.Filter(tokens)
		if maxTokens > 0 && len(tokens) > maxTokens {
			return nil, true
		}
	}
	return tokens, false
}
