package search

// Token is the unit of analyzed text that flows through the package's analysis
// pipeline: a Tokenizer emits Tokens, TokenFilters rewrite or drop them, and an
// Analyzer returns the final slice that the index records and queries against.
type Token struct {
	// Term is the analyzed text of this token.
	Term string
}
