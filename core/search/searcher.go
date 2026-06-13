package search

// Searcher is the read side of a keyword-search engine. Search turns a raw
// query string into the IDs of matching documents — the minimal surface
// needed to pick seed candidates before a wider graph traversal. Result
// ordering is implementation-defined.
type Searcher[S comparable] interface {
	Search(query string) []S
}
