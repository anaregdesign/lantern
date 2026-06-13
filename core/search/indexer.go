package search

// Indexer is the write side of a keyword-search engine. Index associates the
// document identified by id with the searchable terms extracted from doc, and
// Delete removes a previously indexed document. Indexing the same id again
// replaces its postings, so Index doubles as update. How text is analyzed and
// how postings are stored is left to the implementation. S is the caller's
// document-identifier type and D is the indexable document type.
type Indexer[S comparable, D Document] interface {
	Index(id S, doc D)
	Delete(id S)
}
