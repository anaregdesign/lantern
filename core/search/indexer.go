package search

// Document is the indexable payload handed to an Indexer: anything that can
// serialize itself to the text to be analyzed. It is structurally identical to
// fmt.Stringer, so any existing Stringer already satisfies it. Implementing
// String lets a richer record decide which of its fields become searchable
// (for example concatenating a title and a body) instead of forcing callers to
// pre-flatten every document into a bare string.
type Document interface {
	String() string
}

// Text adapts a plain string to Document so raw text can be indexed without a
// bespoke wrapper type: idx.Index(id, Text("some words")).
type Text string

// String returns the underlying text, making Text a Document.
func (t Text) String() string { return string(t) }

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
