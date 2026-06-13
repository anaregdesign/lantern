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
// tokenizer (defaults to UnicodeTokenizer when nil), and an ordered list of
// token filters.
func NewAnalyzer(normalizers []Normalizer, tokenizer Tokenizer, filters ...TokenFilter) Analyzer {
	if tokenizer == nil {
		tokenizer = UnicodeTokenizer{}
	}
	return &pipelineAnalyzer{
		normalizers: normalizers,
		tokenizer:   tokenizer,
		filters:     filters,
	}
}

// NewStandardAnalyzer returns the batteries-included, language-independent
// analyzer: it lowercases the text, splits it into letter/digit runs (so
// whitespace and punctuation act as delimiters), and—if any stop words are
// supplied—drops them. It is the analyzer most callers want for simple keyword
// search.
func NewStandardAnalyzer(stopWords ...string) Analyzer {
	var filters []TokenFilter
	if len(stopWords) > 0 {
		filters = append(filters, NewStopWordFilter(stopWords...))
	}
	return NewAnalyzer([]Normalizer{LowercaseNormalizer{}}, UnicodeTokenizer{}, filters...)
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
