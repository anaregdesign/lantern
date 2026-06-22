package search

import (
	"sort"
	"sync"
)

// InvertedIndex is a generic, in-memory inverted index that maps each
// analyzed term to the documents whose text contains it. It is the default
// Indexer + Searcher implementation: indexed documents (any Document, via
// doc.String()) and raw queries pass through the same Analyzer, so index-time
// and query-time terms stay symmetric.
//
// It splits the two responsibilities of relevance search. The index owns
// matching and the statistics behind it — per-document term frequencies and
// document lengths — while a pluggable Scorer (BM25 by default) turns those
// statistics into the ranking. Search returns hits ordered by descending
// score.
//
// InvertedIndex is safe for concurrent use by multiple goroutines: reads
// (Search) take a shared lock and writes (Index, Delete) take an exclusive
// one, and text analysis runs outside the lock to keep the critical section
// short.
type InvertedIndex[S comparable, D Document] struct {
	analyzer Analyzer
	scorer   Scorer

	mu sync.RWMutex
	// dict assigns each distinct term a compact uint32 id so postings and the
	// per-document forward lists key on the id instead of repeating the term
	// string; the term itself is stored exactly once, in the dictionary.
	dict *termDict
	// ords assigns each distinct document a compact uint32 ordinal so postings
	// address documents by integer (a Roaring bitmap member) instead of
	// repeating the caller's id (typically a string vertex key) in every posting
	// the document appears in.
	ords *ordinals[S]
	// postings is the inverted index proper: term id -> the document ordinals
	// carrying the term and their frequencies. Search reads it; the per-document
	// frequency is what the Scorer needs to weight a match.
	postings map[uint32]*postingList
	// docs is the forward index: document ordinal -> the id, length, and term ids
	// needed to score, return, and drop the document in O(terms-in-doc).
	docs map[uint32]docEntry[S]
	// totalLen is the sum of every document's length, so the mean document
	// length (for length normalization) is totalLen/len(docs) without a scan.
	totalLen int
}

// docEntry is the forward-index entry recorded for each indexed document, keyed
// by the document's ordinal.
type docEntry[S comparable] struct {
	// id is the caller's document identifier, returned in search results and used
	// to resolve a delete (id -> ordinal -> entry).
	id S
	// length is the document's size in tokens, repeats included — the |d| that
	// length normalization compares against the corpus average.
	length int
	// terms is the de-duplicated list of distinct term ids the document was
	// indexed under (see termDict), used to drop its postings on delete or
	// re-index. A []uint32 keeps the per-document forward index compact.
	terms []uint32
}

// NewInvertedIndex returns an empty index that analyzes both documents and
// queries with analyzer and ranks matches with scorer. Passing a nil scorer
// installs BM25 with the standard parameters (K1 = 1.2, B = 0.75). D is the
// document type it accepts; use Text to index plain strings.
func NewInvertedIndex[S comparable, D Document](analyzer Analyzer, scorer Scorer) *InvertedIndex[S, D] {
	if scorer == nil {
		scorer = BM25{K1: defaultBM25K1, B: defaultBM25B}
	}
	return &InvertedIndex[S, D]{
		analyzer: analyzer,
		scorer:   scorer,
		dict:     newTermDict(),
		ords:     newOrdinals[S](),
		postings: make(map[uint32]*postingList),
		docs:     make(map[uint32]docEntry[S]),
	}
}

// Index makes id discoverable under every term the Analyzer extracts from
// doc.String(). Indexing an id that is already present replaces its previous
// postings, so re-indexing a mutated document leaves no stale terms behind;
// Index therefore doubles as update. A document with no analyzable terms simply
// removes id.
//
// Index is the analyze-then-write convenience wrapper over Prepare +
// IndexPrepared: analysis runs outside idx.mu and only the cheap postings
// mutation happens under it. Callers that want to keep analysis off a hotter
// lock of their own (e.g. a layered cache writing under its aggregate lock)
// should call Prepare before taking that lock and IndexPrepared after.
func (idx *InvertedIndex[S, D]) Index(id S, doc D) {
	idx.IndexPrepared(id, idx.Prepare(doc))
}

// PreparedDocument carries the analyzed tokens of a document so the expensive
// analysis (doc.String() + tokenization) can run outside the index write lock.
// Produce one with Prepare and apply it with IndexPrepared or IndexManyPrepared.
// The zero value is a valid empty document: applying it removes the id, matching
// Index of a document with no analyzable terms.
type PreparedDocument struct {
	tokens []Token
}

// PreparedItem pairs an id with its PreparedDocument for IndexManyPrepared.
type PreparedItem[S comparable] struct {
	ID       S
	Prepared PreparedDocument
}

// Prepare analyzes doc.String() WITHOUT taking the index lock and returns the
// tokens IndexPrepared needs. The analyzer is immutable and stateless, so
// Prepare is safe to call concurrently and from outside any of the caller's
// locks.
func (idx *InvertedIndex[S, D]) Prepare(doc D) PreparedDocument {
	return PreparedDocument{tokens: idx.analyzer.Analyze(doc.String())}
}

// IndexPrepared records id's postings from a PreparedDocument produced by
// Prepare, taking idx.mu only for the postings mutation. Replace semantics match
// Index: any previous postings for id are dropped first, and an empty prepared
// document simply removes id.
func (idx *InvertedIndex[S, D]) IndexPrepared(id S, prepared PreparedDocument) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.indexPreparedLocked(id, prepared.tokens)
}

// IndexManyPrepared applies a batch of prepared documents under a SINGLE index
// write lock. Items are applied in slice order, so a repeated id keeps
// last-write semantics. It is the batch sibling of IndexPrepared for callers
// (e.g. batch vertex writes) that have no other per-id work to interleave
// between the postings updates (#739).
func (idx *InvertedIndex[S, D]) IndexManyPrepared(items []PreparedItem[S]) {
	if len(items) == 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, it := range items {
		idx.indexPreparedLocked(it.ID, it.Prepared.tokens)
	}
}

// indexPreparedLocked is the shared postings-mutation core of IndexPrepared and
// IndexManyPrepared; callers must hold idx.mu for writing.
func (idx *InvertedIndex[S, D]) indexPreparedLocked(id S, tokens []Token) {
	// Replace semantics: drop any postings from a previous Index(id, ...)
	// before recording the new ones.
	idx.deleteLocked(id)
	if len(tokens) == 0 {
		return
	}
	// Sort this document's terms so the distinct terms and their frequencies
	// (the run length of each equal stretch) can be read in one linear pass.
	// sorted is transient scratch; only the compact distinct-term slice is
	// retained in docStats.
	sorted := make([]string, len(tokens))
	for i, token := range tokens {
		sorted[i] = token.Term
	}
	sort.Strings(sorted)

	// Count distinct terms up front so the retained forward-index slice is
	// allocated at its exact length (no spare capacity held per document).
	distinct := 1
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1] {
			distinct++
		}
	}
	ord := idx.ords.assign(id)
	terms := make([]uint32, 0, distinct)
	for i := 0; i < len(sorted); {
		j := i + 1
		for j < len(sorted) && sorted[j] == sorted[i] {
			j++
		}
		tid := idx.dict.intern(sorted[i])
		pl, ok := idx.postings[tid]
		if !ok {
			pl = newPostingList()
			idx.postings[tid] = pl
		}
		pl.set(ord, j-i) // term frequency = run length of this term
		terms = append(terms, tid)
		i = j
	}
	idx.docs[ord] = docEntry[S]{id: id, length: len(tokens), terms: terms}
	idx.totalLen += len(tokens)
}

// Delete removes id and all of its postings from the index. It is a no-op when
// id was never indexed. A term whose last document is removed is dropped
// entirely, so a delete-heavy workload never leaks empty posting sets.
func (idx *InvertedIndex[S, D]) Delete(id S) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.deleteLocked(id)
}

// DeleteMany removes every id in ids and its postings under a single write
// lock. Ids that were never indexed are skipped. It is the batch sibling of
// Delete used by GraphCache's batch eviction path so a namespace-wide or
// TTL-flush delete pays one idx.mu acquisition instead of one per document
// (#738).
func (idx *InvertedIndex[S, D]) DeleteMany(ids []S) {
	if len(ids) == 0 {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, id := range ids {
		idx.deleteLocked(id)
	}
}

// deleteLocked removes id and its postings; callers must hold idx.mu for
// writing. It is shared by Delete and the replace step of Index, which already
// holds the lock—sync.Mutex is not reentrant, so Index must not call Delete.
func (idx *InvertedIndex[S, D]) deleteLocked(id S) {
	ord, ok := idx.ords.lookup(id)
	if !ok {
		return
	}
	entry := idx.docs[ord]
	for _, tid := range entry.terms {
		if idx.postings[tid].remove(ord) {
			delete(idx.postings, tid)
			idx.dict.release(tid)
		}
	}
	idx.totalLen -= entry.length
	delete(idx.docs, ord)
	idx.ords.release(id, ord)
}

// Search returns the documents that share at least one analyzed term with
// query, each paired with its relevance score, ordered from most to least
// relevant. The set of matches is the union (OR) of the query's terms, which
// maximizes recall for seeding a wider traversal, while the Scorer (BM25 by
// default) ranks that set so the strongest seeds come first. A term repeated
// in the query weights a document only once. A query with no analyzable terms,
// or one that matches nothing, returns nil; ties in score have an unspecified
// order.
func (idx *InvertedIndex[S, D]) Search(query string) []Result[S] {
	tokens := idx.analyzer.Analyze(query)
	if len(tokens) == 0 {
		return nil
	}
	// Collapse the query to its distinct terms so a term repeated in the query
	// contributes a document's weight once, matching the boolean union.
	queryTerms := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		queryTerms[token.Term] = struct{}{}
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	n := len(idx.docs)
	if n == 0 {
		return nil
	}
	avgLen := float64(idx.totalLen) / float64(n)

	scores := make(map[uint32]float64)
	for term := range queryTerms {
		tid, ok := idx.dict.lookup(term)
		if !ok {
			continue
		}
		pl, ok := idx.postings[tid]
		if !ok {
			continue
		}
		df := pl.cardinality()
		for it := pl.docs.Iterator(); it.HasNext(); {
			ord := it.Next()
			scores[ord] += idx.scorer.Score(TermStats{
				TF:     pl.tf(ord),
				DF:     df,
				N:      n,
				DocLen: idx.docs[ord].length,
				AvgLen: avgLen,
			})
		}
	}
	if len(scores) == 0 {
		return nil
	}
	results := make([]Result[S], 0, len(scores))
	for ord, score := range scores {
		results = append(results, Result[S]{ID: idx.docs[ord].id, Score: score})
	}
	// Rank by descending score; ties keep an unspecified relative order.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// Stats returns the current number of distinct terms in the posting list and
// the number of indexed documents. The counts are approximate under concurrent
// load (a concurrent Index or Delete may be in progress), but are accurate
// enough for a Prometheus gauge sampled on a regular cadence. The call does
// not block writers.
func (idx *InvertedIndex[S, D]) Stats() (terms, docs int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.postings), len(idx.docs)
}

// Interface assertions: InvertedIndex is both an Indexer and a Searcher.
var (
	_ Indexer[string, Text] = (*InvertedIndex[string, Text])(nil)
	_ Searcher[string]      = (*InvertedIndex[string, Text])(nil)
)
