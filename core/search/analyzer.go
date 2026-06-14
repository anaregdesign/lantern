package search

// Analyzer converts raw text into the sequence of index terms used by both
// indexing and querying. Running the same Analyzer on documents and on queries
// is what keeps the two sides symmetric. Implementations in this package are
// stateless and safe for concurrent use by multiple goroutines.
type Analyzer interface {
	Analyze(text string) []Token
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
