package search

import "sync"

// InvertedIndex is a generic, in-memory inverted index that maps each
// analyzed term to the set of document IDs whose text contains it. It is the
// default Indexer + Searcher implementation: indexed documents (any Document,
// via doc.String()) and raw queries pass through the same Analyzer, so
// index-time and query-time terms stay symmetric.
//
// InvertedIndex is safe for concurrent use by multiple goroutines: reads
// (Search) take a shared lock and writes (Index, Delete) take an exclusive
// one, and text analysis runs outside the lock to keep the critical section
// short.
type InvertedIndex[S comparable, D Document] struct {
	analyzer Analyzer

	mu sync.RWMutex
	// postings is the inverted index proper: term -> set of documents that
	// contain it. Search reads it.
	postings map[string]map[S]struct{}
	// terms is a forward index: document -> set of terms it was indexed
	// under. It lets Delete (and re-indexing) drop a document's old postings
	// in O(terms-in-doc) without scanning the whole vocabulary.
	terms map[S]map[string]struct{}
}

// NewInvertedIndex returns an empty index that analyzes both documents and
// queries with analyzer. D is the document type it accepts; use Text to index
// plain strings.
func NewInvertedIndex[S comparable, D Document](analyzer Analyzer) *InvertedIndex[S, D] {
	return &InvertedIndex[S, D]{
		analyzer: analyzer,
		postings: make(map[string]map[S]struct{}),
		terms:    make(map[S]map[string]struct{}),
	}
}

// Index makes id discoverable under every term the Analyzer extracts from
// doc.String(). Indexing an id that is already present replaces its previous
// postings, so re-indexing a mutated document leaves no stale terms behind;
// Index therefore doubles as update. A document with no analyzable terms simply
// removes id.
func (idx *InvertedIndex[S, D]) Index(id S, doc D) {
	// Analyze outside the lock: the analyzer is immutable and stateless, and
	// tokenization is the expensive part we do not want under the write lock.
	tokens := idx.analyzer.Analyze(doc.String())

	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Replace semantics: drop any postings from a previous Index(id, ...)
	// before recording the new ones.
	idx.deleteLocked(id)
	if len(tokens) == 0 {
		return
	}
	terms := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		posting, ok := idx.postings[token.Term]
		if !ok {
			posting = make(map[S]struct{})
			idx.postings[token.Term] = posting
		}
		posting[id] = struct{}{}
		terms[token.Term] = struct{}{}
	}
	idx.terms[id] = terms
}

// Delete removes id and all of its postings from the index. It is a no-op when
// id was never indexed. A term whose last document is removed is dropped
// entirely, so a delete-heavy workload never leaks empty posting sets.
func (idx *InvertedIndex[S, D]) Delete(id S) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.deleteLocked(id)
}

// deleteLocked removes id and its postings; callers must hold idx.mu for
// writing. It is shared by Delete and the replace step of Index, which already
// holds the lock—sync.Mutex is not reentrant, so Index must not call Delete.
func (idx *InvertedIndex[S, D]) deleteLocked(id S) {
	terms, ok := idx.terms[id]
	if !ok {
		return
	}
	for term := range terms {
		posting := idx.postings[term]
		delete(posting, id)
		if len(posting) == 0 {
			delete(idx.postings, term)
		}
	}
	delete(idx.terms, id)
}

// Search returns every document ID that shares at least one analyzed term with
// query (union / OR semantics). OR maximizes recall, which is what a caller
// wants when the result only has to seed a wider graph traversal. Each ID
// appears once and the order is unspecified; a query with no analyzable terms
// returns nil.
func (idx *InvertedIndex[S, D]) Search(query string) []S {
	tokens := idx.analyzer.Analyze(query)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	matched := make(map[S]struct{})
	for _, token := range tokens {
		for id := range idx.postings[token.Term] {
			matched[id] = struct{}{}
		}
	}
	if len(matched) == 0 {
		return nil
	}
	ids := make([]S, 0, len(matched))
	for id := range matched {
		ids = append(ids, id)
	}
	return ids
}

// Interface assertions: InvertedIndex is both an Indexer and a Searcher.
var (
	_ Indexer[string, Text] = (*InvertedIndex[string, Text])(nil)
	_ Searcher[string]      = (*InvertedIndex[string, Text])(nil)
)
