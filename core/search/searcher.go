package search

// Result is a single ranked search hit: the ID of a matching document and the
// relevance Score the Searcher's Scorer assigned it. Higher scores are more
// relevant.
type Result[S comparable] struct {
	ID    S
	Score float64
}

// Searcher is the read side of a keyword-search engine. Search turns a raw
// query string into the matching documents, each paired with a relevance
// score, ordered from most to least relevant — the ranked seed candidates a
// caller picks before a wider graph traversal, where the score can double as a
// seed's initial weight. Documents that share no analyzed term with the query
// are omitted, and a query with no analyzable terms yields no results. Ties in
// score have an unspecified relative order.
type Searcher[S comparable] interface {
	Search(query string) []Result[S]
}
