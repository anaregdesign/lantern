package search

import (
	"container/heap"
	"math"
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
// statistics into the ranking. Search returns hits in the stable total order
// (score descending, caller-provided typed ID comparator ascending).
//
// Terms live in per-class tables (#888): each TokenClass has its own term
// dictionary, postings, and corpus statistics, so a dual-channel analyzer
// (ScriptAwareTokenizer's whole words + auxiliary intra-word grams) never
// mixes the two channels' document frequencies or lengths, and a class-aware
// Scorer (ClassWeighted) can weight them apart. A single-channel pipeline
// simply leaves the other class empty at zero cost.
//
// InvertedIndex is safe for concurrent use by multiple goroutines: reads
// (Search) take a shared lock and writes (Index, Delete) take an exclusive
// one, and text analysis runs outside the lock to keep the critical section
// short.
type InvertedIndex[S comparable, D Document] struct {
	analyzer Analyzer
	scorer   Scorer
	// compareID defines the stable ascending typed-ID order used to break
	// equal-score ties.
	// It is required because comparable alone does not imply an order, and an
	// internal document ordinal would make externally visible ranking depend on
	// insertion/delete history.
	compareID func(S, S) int
	// positions is fixed at construction (WithPositions): when set, Index
	// records each primary-channel (ClassWord) term's token positions so
	// SearchPhrase and the proximity boost can tell an exact phrase from
	// scattered matches. Read without the lock — it never changes after
	// construction.
	positions bool
	// proximityWeight scales the proximity boost (see proximity.go); it
	// defaults to proximityBoostWeight and is overridable with
	// WithProximityWeight so the relevance harness can sweep it. 0 disables the
	// boost. Read without the lock — it never changes after construction.
	proximityWeight float64

	mu sync.RWMutex
	// classes holds one posting table per token class; a term's class is part
	// of its identity, so the same spelling on two channels never collides.
	classes [numTokenClasses]classPostings
	// ords assigns each distinct document a compact uint32 ordinal so postings
	// address documents by integer (a Roaring bitmap member) instead of
	// repeating the caller's id (typically a string vertex key) in every posting
	// the document appears in.
	ords *ordinals[S]
	// docs is the forward index: document ordinal -> the id, per-class lengths,
	// and term ids needed to score, return, and drop the document in
	// O(terms-in-doc).
	docs map[uint32]docEntry[S]
}

// classPostings is one token class's slice of the index: its term dictionary,
// its postings, and the class-scoped corpus statistics BM25 length
// normalization and IDF need. Keeping them per class is what stops auxiliary
// gram evidence from inflating the word channel's document lengths (and vice
// versa) when one document feeds both channels.
type classPostings struct {
	// dict assigns each distinct term of this class a compact uint32 id so
	// postings and the per-document forward lists key on the id instead of
	// repeating the term string; the term itself is stored exactly once.
	dict *termDict
	// postings is the inverted index proper: term id -> the document ordinals
	// carrying the term and their frequencies.
	postings map[uint32]*postingList
	// totalLen is the sum of this class's per-document token counts, so the
	// class's mean document length is totalLen/docCount without a scan.
	totalLen int
	// docCount is how many documents carry at least one token of this class —
	// the class's corpus size, the N of its TermStats.
	docCount int
}

// docEntry is the forward-index entry recorded for each indexed document, keyed
// by the document's ordinal.
type docEntry[S comparable] struct {
	// id is the caller's document identifier, returned in search results and used
	// to resolve a delete (id -> ordinal -> entry).
	id S
	// lengths is the document's size in tokens per class, repeats included —
	// the |d| that each class's length normalization compares against the
	// class average.
	lengths [numTokenClasses]int
	// terms is the de-duplicated list of distinct term ids the document was
	// indexed under, per class (see termDict), used to drop its postings on
	// delete or re-index. A []uint32 keeps the per-document forward index
	// compact.
	terms [numTokenClasses][]uint32
}

// IndexOption configures an InvertedIndex at construction time.
type IndexOption func(*indexConfig)

// indexConfig holds the resolved construction options.
type indexConfig struct {
	positions       bool
	proximityWeight float64
}

// WithPositions makes the index record each term's token positions on the
// primary word channel (ClassWord), which SearchPhrase and the proximity boost
// need to tell an exact phrase from scattered term matches. It costs one
// ascending []uint32 per (word term, document); an index left without it ranks
// by the OR-union BM25 alone and stores no positions. The auxiliary gram
// channel never carries positions: phrase and proximity are word-level, and a
// CJK run's adjacency is already encoded by its overlapping bigrams on the
// word channel, so pos+1 verification works there too (#889).
func WithPositions() IndexOption {
	return func(c *indexConfig) { c.positions = true }
}

// WithProximityWeight overrides the multiplier the proximity boost applies to a
// multi-term query's OR-union scores (default proximityBoostWeight). It is the
// injection point the relevance harness sweeps to justify the shipped value:
// with it the boost's contribution is a measured number, not an unguarded
// constant (#910). A weight of 0 disables the boost, making ranking pure
// OR-union BM25 even under WithPositions; it is only meaningful together with
// WithPositions, since the boost needs the positional postings.
func WithProximityWeight(w float64) IndexOption {
	return func(c *indexConfig) { c.proximityWeight = w }
}

// NewInvertedIndex returns an empty index that analyzes both documents and
// queries with analyzer and ranks matches with scorer. Passing a nil scorer
// installs BM25 with the standard parameters (K1 = 1.2, B = 0.75). compareID
// is the mandatory ascending total order for equal-score document IDs; it must
// be stable across processes/replicas. D is the document type it accepts; use
// Text to index plain strings. Pass WithPositions to enable phrase and
// proximity queries (SearchPhrase).
func NewInvertedIndex[S comparable, D Document](analyzer Analyzer, scorer Scorer, compareID func(S, S) int, opts ...IndexOption) *InvertedIndex[S, D] {
	if compareID == nil {
		panic("search: NewInvertedIndex compareID must not be nil")
	}
	if scorer == nil {
		scorer = BM25{K1: DefaultBM25K1, B: DefaultBM25B}
	}
	var cfg indexConfig
	cfg.proximityWeight = proximityBoostWeight
	for _, opt := range opts {
		opt(&cfg)
	}
	idx := &InvertedIndex[S, D]{
		analyzer:        analyzer,
		scorer:          scorer,
		compareID:       compareID,
		positions:       cfg.positions,
		proximityWeight: cfg.proximityWeight,
		ords:            newOrdinals[S](),
		docs:            make(map[uint32]docEntry[S]),
	}
	for class := range idx.classes {
		idx.classes[class] = classPostings{
			dict:     newTermDict(),
			postings: make(map[uint32]*postingList),
		}
	}
	return idx
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
// IndexManyPrepared; callers must hold idx.mu for writing. A token with an
// undefined class panics: that is a broken analyzer, not a malformed document.
func (idx *InvertedIndex[S, D]) indexPreparedLocked(id S, tokens []Token) {
	// Replace semantics: drop any postings from a previous Index(id, ...)
	// before recording the new ones.
	idx.deleteLocked(id)
	if len(tokens) == 0 {
		return
	}
	// Partition the document's terms by class; each class's postings and
	// statistics are recorded independently. perClass slices are transient
	// scratch; only the compact distinct-term slices are retained.
	var perClass [numTokenClasses][]string
	// wordPositions maps each primary (ClassWord) term to its ascending token
	// positions in this document, recorded only when the index tracks positions.
	// Position counts word tokens only (auxiliary grams are skipped), so
	// "data set" yields data@0 set@1 and a CJK run's consecutive bigrams get
	// consecutive positions — both let SearchPhrase verify adjacency with pos+1.
	var wordPositions map[string][]uint32
	if idx.positions {
		wordPositions = make(map[string][]uint32)
	}
	var wordPos uint32
	for _, token := range tokens {
		if int(token.Class) >= numTokenClasses {
			panic("search: token with undefined TokenClass")
		}
		perClass[token.Class] = append(perClass[token.Class], token.Term)
		if wordPositions != nil && token.Class == ClassWord {
			wordPositions[token.Term] = append(wordPositions[token.Term], wordPos)
			wordPos++
		}
	}
	ord := idx.ords.assign(id)
	entry := docEntry[S]{id: id}
	for class, sorted := range perClass {
		if len(sorted) == 0 {
			continue
		}
		// Sort this class's terms so the distinct terms and their frequencies
		// (the run length of each equal stretch) can be read in one linear pass.
		sort.Strings(sorted)

		// Count distinct terms up front so the retained forward-index slice is
		// allocated at its exact length (no spare capacity held per document).
		distinct := 1
		for i := 1; i < len(sorted); i++ {
			if sorted[i] != sorted[i-1] {
				distinct++
			}
		}
		cp := &idx.classes[class]
		terms := make([]uint32, 0, distinct)
		for i := 0; i < len(sorted); {
			j := i + 1
			for j < len(sorted) && sorted[j] == sorted[i] {
				j++
			}
			tid := cp.dict.intern(sorted[i])
			pl, ok := cp.postings[tid]
			if !ok {
				pl = newPostingList()
				cp.postings[tid] = pl
			}
			var pos []uint32
			if wordPositions != nil && class == int(ClassWord) {
				pos = wordPositions[sorted[i]]
			}
			pl.set(ord, j-i, pos) // term frequency = run length of this term
			terms = append(terms, tid)
			i = j
		}
		entry.lengths[class] = len(sorted)
		entry.terms[class] = terms
		cp.totalLen += len(sorted)
		cp.docCount++
	}
	idx.docs[ord] = entry
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
	for class := range entry.terms {
		cp := &idx.classes[class]
		for _, tid := range entry.terms[class] {
			if cp.postings[tid].remove(ord) {
				delete(cp.postings, tid)
				cp.dict.release(tid)
			}
		}
		if entry.lengths[class] > 0 {
			cp.totalLen -= entry.lengths[class]
			cp.docCount--
		}
	}
	delete(idx.docs, ord)
	idx.ords.release(id, ord)
}

// Search returns the documents that share at least one analyzed term with
// query, each paired with its relevance score, ordered from most to least
// relevant. The set of matches is the union (OR) of the query's terms, which
// maximizes recall for seeding a wider traversal, while the Scorer (BM25 by
// default) ranks that set so the strongest seeds come first. A term repeated
// in the query weights a document only once; the same spelling on two token
// classes counts once per class, since the channels carry distinct evidence.
// A query with no analyzable terms, or one that matches nothing, returns nil;
// equal scores use the required typed document-ID comparator ascending.
//
// Search is the MatchAny case of SearchMatch; pass a MatchOptions to require
// every query term (MatchAll) or a minimum number of them (MatchMinShould).
func (idx *InvertedIndex[S, D]) Search(query string) []Result[S] {
	return idx.SearchMatch(query, MatchOptions{})
}

// queryTerms analyzes query and collapses the tokens to a stable, distinct
// (class, term) slice, so a term repeated in the query contributes a document's
// weight once per channel and floating-point contributions are accumulated in
// the same order on every call and replica.
func (idx *InvertedIndex[S, D]) queryTerms(query string) []Token {
	tokens := idx.analyzer.Analyze(query)
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[Token]struct{}, len(tokens))
	queryTerms := make([]Token, 0, len(tokens))
	for _, token := range tokens {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		queryTerms = append(queryTerms, token)
	}
	sort.Slice(queryTerms, func(i, j int) bool {
		if queryTerms[i].Class != queryTerms[j].Class {
			return queryTerms[i].Class < queryTerms[j].Class
		}
		return queryTerms[i].Term < queryTerms[j].Term
	})
	return queryTerms
}

// scoreLocked computes the OR-union score map for a distinct query-term set;
// callers must hold idx.mu (read or write). A query token with an undefined
// class matches nothing — the write side panics on such tokens, so no posting
// can exist for one.
func (idx *InvertedIndex[S, D]) scoreLocked(queryTerms []Token) map[uint32]float64 {
	scores, _ := idx.scoreTrackedLocked(queryTerms, newWorkTracker(nil, Budget{}))
	return scores
}

func (idx *InvertedIndex[S, D]) scoreTrackedLocked(queryTerms []Token, work *workTracker) (map[uint32]float64, error) {
	scores := make(map[uint32]float64)
	for _, token := range queryTerms {
		if err := work.check(); err != nil {
			return nil, err
		}
		if int(token.Class) >= numTokenClasses {
			continue
		}
		cp := &idx.classes[token.Class]
		if cp.docCount == 0 {
			continue
		}
		tid, ok := cp.dict.lookup(token.Term)
		if !ok {
			continue
		}
		pl, ok := cp.postings[tid]
		if !ok {
			continue
		}
		df := pl.cardinality()
		avgLen := float64(cp.totalLen) / float64(cp.docCount)
		for it := pl.docs.Iterator(); it.HasNext(); {
			if err := work.visit(WorkPostingVisits, 1); err != nil {
				return nil, err
			}
			ord := it.Next()
			addScore(scores, ord, idx.scorer.Score(TermStats{
				TF:     pl.tf(ord),
				DF:     df,
				N:      cp.docCount,
				DocLen: idx.docs[ord].lengths[token.Class],
				AvgLen: avgLen,
				Class:  token.Class,
			}))
		}
	}
	dropNonFiniteScores(scores)
	return scores, nil
}

// addScore accumulates one contribution while poisoning a document whose
// scorer emits NaN/Inf (or whose finite contributions overflow). The poison is
// removed by dropNonFiniteScores, so non-finite custom scorer output can never
// enter sorting or heap selection.
func addScore(scores map[uint32]float64, ord uint32, contribution float64) {
	current, exists := scores[ord]
	if exists && math.IsNaN(current) {
		return
	}
	if math.IsNaN(contribution) || math.IsInf(contribution, 0) {
		scores[ord] = math.NaN()
		return
	}
	total := current + contribution
	if math.IsNaN(total) || math.IsInf(total, 0) {
		scores[ord] = math.NaN()
		return
	}
	scores[ord] = total
}

func dropNonFiniteScores(scores map[uint32]float64) {
	for ord, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			delete(scores, ord)
		}
	}
}

// betterResult is the single external ranking contract: score descending,
// then the caller-supplied document ID order ascending.
func (idx *InvertedIndex[S, D]) betterResult(a, b Result[S]) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return idx.compareID(a.ID, b.ID) < 0
}

func (idx *InvertedIndex[S, D]) rankedResultsLocked(scores map[uint32]float64) []Result[S] {
	results := make([]Result[S], 0, len(scores))
	for ord, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			continue
		}
		results = append(results, Result[S]{ID: idx.docs[ord].id, Score: score})
	}
	sort.Slice(results, func(i, j int) bool { return idx.betterResult(results[i], results[j]) })
	return results
}

// SearchTopK is the bounded-selection sibling of Search (#841): it computes
// the same OR-union BM25 scores but returns only the k highest-scoring
// documents that satisfy accept, without materialising or fully sorting the
// complete match set. With a bigram analyzer a short query can match most
// of the corpus, so Search's O(M log M) sort over all M matches dominates
// broad queries; SearchTopK selects with a size-k bounded heap in
// O(M log k) time and O(k) result memory instead.
//
// accept gates a document BEFORE it can occupy one of the k slots, so
// filtered-out documents (dead vertices, out-of-scope keys) never consume
// the page budget — a full page of accepted hits is returned whenever one
// exists. accept may be nil (accept everything). To keep accept calls off
// the O(M) score loop it is consulted lazily, only when a candidate's score
// qualifies it for the heap.
//
// LOCK ORDER: accept runs while idx.mu is held for reading. The established
// order is GraphCache.mu → idx.mu → (vertex-cache inner mutex): index writes
// already run under GraphCache.mu, and accept's typical body (a vertex-cache
// liveness probe) takes only the inner cache mutex, which never acquires
// idx.mu — so no cycle is possible. accept must not call back into this
// index's write methods.
//
// Results use the index's total order (score DESC, typed ID comparator ASC),
// including at the k-th boundary. k <= 0, an unanalyzable query, or zero
// accepted matches return nil.
//
// SearchTopK is the MatchAny case of SearchMatchTopK.
func (idx *InvertedIndex[S, D]) SearchTopK(query string, k int, accept func(id S) bool) []Result[S] {
	return idx.SearchMatchTopK(query, k, accept, MatchOptions{})
}

// selectTopKLocked returns the k highest-scoring documents in scores that pass
// accept, as a descending-ranked slice. It is the bounded-selection phase
// shared by SearchTopK and SearchPhraseTopK: a size-k min-heap holds the
// current best, and accept is consulted lazily only after a candidate clears
// the same total-order boundary used by exhaustive ranking. Callers must hold
// idx.mu; k > 0 is the caller's precondition.
func (idx *InvertedIndex[S, D]) selectTopKLocked(scores map[uint32]float64, k int, accept func(id S) bool) []Result[S] {
	out, _ := idx.selectTopKTrackedLocked(scores, k, accept, newWorkTracker(nil, Budget{}))
	return out
}

func (idx *InvertedIndex[S, D]) selectTopKTrackedLocked(scores map[uint32]float64, k int, accept func(id S) bool, work *workTracker) ([]Result[S], error) {
	h := topKHeap[S]{entries: make([]Result[S], 0, k), better: idx.betterResult}
	for ord, score := range scores {
		if err := work.check(); err != nil {
			return nil, err
		}
		if math.IsNaN(score) || math.IsInf(score, 0) {
			continue
		}
		id := idx.docs[ord].id
		candidate := Result[S]{ID: id, Score: score}
		if len(h.entries) == k && !idx.betterResult(candidate, h.entries[0]) {
			continue
		}
		if accept != nil && !accept(id) {
			continue
		}
		if len(h.entries) < k {
			heap.Push(&h, candidate)
			continue
		}
		h.entries[0] = candidate
		heap.Fix(&h, 0)
	}
	if len(h.entries) == 0 {
		return nil, nil
	}
	out := h.entries
	sort.Slice(out, func(i, j int) bool { return idx.betterResult(out[i], out[j]) })
	return out, nil
}

// topKHeap is the size-k min-heap behind SearchTopK: the root is the weakest
// retained hit, evicted whenever a stronger accepted candidate arrives.
type topKHeap[S comparable] struct {
	entries []Result[S]
	better  func(Result[S], Result[S]) bool
}

func (h topKHeap[S]) Len() int { return len(h.entries) }
func (h topKHeap[S]) Less(i, j int) bool {
	return h.better(h.entries[j], h.entries[i]) // weakest result at the root
}
func (h topKHeap[S]) Swap(i, j int) { h.entries[i], h.entries[j] = h.entries[j], h.entries[i] }
func (h *topKHeap[S]) Push(x any)   { h.entries = append(h.entries, x.(Result[S])) }
func (h *topKHeap[S]) Pop() any {
	old := h.entries
	n := len(old)
	x := old[n-1]
	h.entries = old[:n-1]
	return x
}

// Stats returns the current number of distinct terms in the posting lists
// (summed across token classes) and the number of indexed documents. The
// counts are approximate under concurrent load (a concurrent Index or Delete
// may be in progress), but are accurate enough for a Prometheus gauge sampled
// on a regular cadence. The call does not block writers.
func (idx *InvertedIndex[S, D]) Stats() (terms, docs int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for class := range idx.classes {
		terms += len(idx.classes[class].postings)
	}
	return terms, len(idx.docs)
}

// Interface assertions: InvertedIndex is both an Indexer and a Searcher.
var (
	_ Indexer[string, Text] = (*InvertedIndex[string, Text])(nil)
	_ Searcher[string]      = (*InvertedIndex[string, Text])(nil)
)
