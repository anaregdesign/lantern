package search

import (
	"container/heap"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
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
	limits          SearchAnalysisLimits

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
	docs                  map[uint32]docEntry[S]
	livePostings          int64
	livePositions         int64
	health                IndexHealth
	rebuildCount          uint64
	lastRebuildDuration   time.Duration
	writeLockAcquisitions uint64
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
	terms           [numTokenClasses][]uint32
	positionEntries int
}

// IndexOption configures an InvertedIndex at construction time.
type IndexOption func(*indexConfig)

// indexConfig holds the resolved construction options.
type indexConfig struct {
	positions       bool
	proximityWeight float64
	limits          SearchAnalysisLimits
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

// WithAnalysisLimits installs hard per-document and aggregate index budgets.
func WithAnalysisLimits(limits SearchAnalysisLimits) IndexOption {
	return func(c *indexConfig) { c.limits = limits }
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
		limits:          cfg.limits,
		ords:            newOrdinals[S](),
		docs:            make(map[uint32]docEntry[S]),
		health:          IndexHealthy,
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
// removes id. Configured analysis/index limits are checked before mutation.
//
// Index is the analyze-then-write convenience wrapper over Prepare +
// IndexPrepared: analysis runs outside idx.mu and only the cheap postings
// mutation happens under it. Callers that want to keep analysis off a hotter
// lock of their own (e.g. a layered cache writing under its aggregate lock)
// should call Prepare before taking that lock and IndexPrepared after.
func (idx *InvertedIndex[S, D]) Index(id S, doc D) error {
	prepared, _, err := idx.Prepare(doc)
	if err != nil {
		return err
	}
	return idx.IndexPrepared(id, prepared)
}

// PreparedDocument carries an already grouped document representation so all
// analysis, partitioning, sorting, frequency aggregation, and position
// collection complete before the index write lock is acquired. Produce one
// with Prepare and apply it with IndexPrepared or IndexManyPrepared. The zero
// value is a valid empty document: applying it removes the id, matching Index
// of a document with no analyzable terms.
type PreparedDocument struct {
	classes         [numTokenClasses]preparedClass
	postings        int
	positionEntries int
}

type preparedClass struct {
	length int
	terms  []preparedTerm
}

type preparedTerm struct {
	term      string
	frequency int
	positions []uint32
}

// PreparedItem pairs an id with its PreparedDocument for IndexManyPrepared.
type PreparedItem[S comparable] struct {
	ID       S
	Prepared PreparedDocument
}

// Prepare analyzes doc.String() WITHOUT taking the index lock and returns the
// bounded tokens IndexPrepared needs, their AnalysisStats, or a typed
// AnalysisLimitError. The analyzer is immutable and stateless, so Prepare is
// safe to call concurrently and from outside any of the caller's locks.
func (idx *InvertedIndex[S, D]) Prepare(doc D) (PreparedDocument, AnalysisStats, error) {
	if sized, ok := any(doc).(SizedDocument); ok {
		hint := sized.SizeHint()
		if err := limitError(LimitDocumentBytes, int64(hint), int64(idx.limits.MaxDocumentBytes)); err != nil {
			return PreparedDocument{}, AnalysisStats{ProjectedBytes: hint}, err
		}
	}
	text := doc.String()
	stats := AnalysisStats{ProjectedBytes: len(text)}
	if err := limitError(LimitDocumentBytes, int64(stats.ProjectedBytes), int64(idx.limits.MaxDocumentBytes)); err != nil {
		return PreparedDocument{}, stats, err
	}
	var tokens []Token
	if analyzer, ok := idx.analyzer.(boundedAnalyzer); ok {
		var exceeded bool
		tokens, exceeded = analyzer.AnalyzeBounded(text, idx.limits.MaxDocumentTokens)
		if exceeded {
			return PreparedDocument{}, stats, &AnalysisLimitError{Kind: LimitDocumentTokens, Got: int64(idx.limits.MaxDocumentTokens + 1), Limit: int64(idx.limits.MaxDocumentTokens)}
		}
	} else {
		tokens = idx.analyzer.Analyze(text)
	}
	stats.Tokens = len(tokens)
	if err := limitError(LimitDocumentTokens, int64(stats.Tokens), int64(idx.limits.MaxDocumentTokens)); err != nil {
		return PreparedDocument{}, stats, err
	}
	prepared := PreparedDocument{}
	var wordPositions map[string][]uint32
	if idx.positions {
		wordPositions = make(map[string][]uint32)
	}
	var wordPosition uint32
	for _, token := range tokens {
		if int(token.Class) >= numTokenClasses {
			panic("search: token with undefined TokenClass")
		}
		if idx.positions && token.Class == ClassWord {
			wordPositions[token.Term] = append(wordPositions[token.Term], wordPosition)
			wordPosition++
		}
	}
	// The analyzer owns this token slice for the call, so sort it in place
	// instead of allocating a second occurrence-sized scratch representation.
	// Positions were captured above while tokens were still in source order.
	slices.SortFunc(tokens, func(a, b Token) int {
		if a.Class < b.Class {
			return -1
		}
		if a.Class > b.Class {
			return 1
		}
		return strings.Compare(a.Term, b.Term)
	})
	var distinct [numTokenClasses]int
	for i := 0; i < len(tokens); {
		j := i + 1
		for j < len(tokens) && tokens[j] == tokens[i] {
			j++
		}
		distinct[tokens[i].Class]++
		i = j
	}
	for class, count := range distinct {
		if count > 0 {
			prepared.classes[class].terms = make([]preparedTerm, 0, count)
		}
	}
	for i := 0; i < len(tokens); {
		j := i + 1
		for j < len(tokens) && tokens[j] == tokens[i] {
			j++
		}
		class := tokens[i].Class
		term := preparedTerm{term: tokens[i].Term, frequency: j - i}
		if idx.positions && class == ClassWord {
			term.positions = wordPositions[term.term]
		}
		prepared.classes[class].length += j - i
		prepared.classes[class].terms = append(prepared.classes[class].terms, term)
		prepared.postings++
		i = j
	}
	if idx.positions {
		prepared.positionEntries = int(wordPosition)
	}
	stats.UniqueTerms = prepared.postings
	stats.Postings = prepared.postings
	stats.PositionEntries = prepared.positionEntries
	if err := limitError(LimitDocumentTerms, int64(stats.UniqueTerms), int64(idx.limits.MaxDocumentTerms)); err != nil {
		return PreparedDocument{}, stats, err
	}
	return prepared, stats, nil
}

// IndexPrepared records id's postings from a PreparedDocument produced by
// Prepare, taking idx.mu only for the postings mutation. Replace semantics match
// Index: any previous postings for id are dropped first, and an empty prepared
// document simply removes id.
func (idx *InvertedIndex[S, D]) IndexPrepared(id S, prepared PreparedDocument) error {
	idx.lockWrite()
	defer idx.mu.Unlock()
	if err := idx.validatePreparedLocked([]PreparedItem[S]{{ID: id, Prepared: prepared}}); err != nil {
		return err
	}
	idx.indexPreparedLocked(id, prepared)
	idx.compactIfNeededLocked()
	return nil
}

// IndexManyPrepared applies a batch of prepared documents under a SINGLE index
// write lock. Items are applied in slice order, so a repeated id keeps
// last-write semantics. It is the batch sibling of IndexPrepared for callers
// (e.g. batch vertex writes) that have no other per-id work to interleave
// between the postings updates (#739).
func (idx *InvertedIndex[S, D]) IndexManyPrepared(items []PreparedItem[S]) error {
	if len(items) == 0 {
		return nil
	}
	idx.lockWrite()
	defer idx.mu.Unlock()
	if err := idx.validatePreparedLocked(items); err != nil {
		return err
	}
	for _, it := range finalPreparedItems(items) {
		idx.indexPreparedLocked(it.ID, it.Prepared)
	}
	idx.compactIfNeededLocked()
	return nil
}

// ValidateManyPrepared checks aggregate limits against the batch's final
// replace state without mutating the index. A caller that serializes all index
// writers with an outer lock may use this to preserve a larger transaction's
// atomicity before applying the same batch.
func (idx *InvertedIndex[S, D]) ValidateManyPrepared(items []PreparedItem[S]) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.validatePreparedLocked(items)
}

// IndexManyPreparedValidated applies a batch previously accepted by
// ValidateManyPrepared. The caller must serialize intervening writers.
func (idx *InvertedIndex[S, D]) IndexManyPreparedValidated(items []PreparedItem[S]) {
	if len(items) == 0 {
		return
	}
	idx.lockWrite()
	defer idx.mu.Unlock()
	for _, item := range finalPreparedItems(items) {
		idx.indexPreparedLocked(item.ID, item.Prepared)
	}
	idx.compactIfNeededLocked()
}

func finalPreparedItems[S comparable](items []PreparedItem[S]) []PreparedItem[S] {
	last := make(map[S]int, len(items))
	for i, item := range items {
		last[item.ID] = i
	}
	out := make([]PreparedItem[S], 0, len(last))
	for i, item := range items {
		if last[item.ID] == i {
			out = append(out, item)
		}
	}
	return out
}

// indexPreparedLocked is the shared postings-mutation core of IndexPrepared and
// IndexManyPrepared; callers must hold idx.mu for writing. Prepare has already
// validated classes and reduced every class to sorted distinct terms.
func (idx *InvertedIndex[S, D]) indexPreparedLocked(id S, prepared PreparedDocument) {
	// Replace semantics: drop any postings from a previous Index(id, ...)
	// before recording the new ones.
	idx.deleteLocked(id)
	if prepared.postings == 0 {
		return
	}
	ord := idx.ords.assign(id)
	entry := docEntry[S]{id: id}
	for class, preparedClass := range prepared.classes {
		if len(preparedClass.terms) == 0 {
			continue
		}
		cp := &idx.classes[class]
		terms := make([]uint32, 0, len(preparedClass.terms))
		for _, preparedTerm := range preparedClass.terms {
			tid := cp.dict.intern(preparedTerm.term)
			pl, ok := cp.postings[tid]
			if !ok {
				pl = newPostingList()
				cp.postings[tid] = pl
			}
			pl.set(ord, preparedTerm.frequency, preparedTerm.positions)
			terms = append(terms, tid)
		}
		entry.lengths[class] = preparedClass.length
		entry.terms[class] = terms
		cp.totalLen += preparedClass.length
		cp.docCount++
	}
	entry.positionEntries = prepared.positionEntries
	idx.docs[ord] = entry
	for class := range entry.terms {
		idx.livePostings += int64(len(entry.terms[class]))
	}
	idx.livePositions += int64(entry.positionEntries)
}

type termKey struct {
	class TokenClass
	term  string
}

// validatePreparedLocked evaluates the final state of a batch before any
// posting mutation. Repeated IDs use last-write semantics, and replacements
// subtract their old contribution, so a near-cap in-place update is accepted
// without weakening the hard aggregate bounds.
func (idx *InvertedIndex[S, D]) validatePreparedLocked(items []PreparedItem[S]) error {
	if idx.limits.MaxLiveTerms <= 0 && idx.limits.MaxLivePostings <= 0 && idx.limits.MaxPositionEntries <= 0 {
		return nil
	}
	last := make(map[S]PreparedDocument, len(items))
	for _, item := range items {
		last[item.ID] = item.Prepared
	}
	postings := idx.livePostings
	positions := idx.livePositions
	deltaDF := make(map[termKey]int)
	baseDF := make(map[termKey]int)
	loadDF := func(key termKey) int {
		if n, ok := baseDF[key]; ok {
			return n
		}
		cp := &idx.classes[key.class]
		tid, ok := cp.dict.lookup(key.term)
		if !ok {
			baseDF[key] = 0
			return 0
		}
		n := cp.postings[tid].cardinality()
		baseDF[key] = n
		return n
	}
	for id := range last {
		if ord, ok := idx.ords.lookup(id); ok {
			entry := idx.docs[ord]
			for class := range entry.terms {
				for _, tid := range entry.terms[class] {
					key := termKey{class: TokenClass(class), term: idx.classes[class].dict.terms[tid]}
					loadDF(key)
					deltaDF[key]--
					postings--
				}
			}
			positions -= int64(entry.positionEntries)
		}
	}
	for _, prepared := range last {
		for class, preparedClass := range prepared.classes {
			for _, term := range preparedClass.terms {
				key := termKey{class: TokenClass(class), term: term.term}
				loadDF(key)
				deltaDF[key]++
				postings++
			}
		}
		positions += int64(prepared.positionEntries)
	}
	liveTerms := 0
	for class := range idx.classes {
		liveTerms += len(idx.classes[class].postings)
	}
	for key, delta := range deltaDF {
		before := baseDF[key]
		after := before + delta
		if before == 0 && after > 0 {
			liveTerms++
		}
		if before > 0 && after == 0 {
			liveTerms--
		}
	}
	if err := limitError(LimitLiveTerms, int64(liveTerms), int64(idx.limits.MaxLiveTerms)); err != nil {
		return err
	}
	if err := limitError(LimitLivePostings, postings, idx.limits.MaxLivePostings); err != nil {
		return err
	}
	return limitError(LimitPositionEntries, positions, idx.limits.MaxPositionEntries)
}

// Delete removes id and all of its postings from the index. It is a no-op when
// id was never indexed. A term whose last document is removed is dropped
// entirely, so a delete-heavy workload never leaks empty posting sets.
func (idx *InvertedIndex[S, D]) Delete(id S) {
	idx.lockWrite()
	defer idx.mu.Unlock()
	idx.deleteLocked(id)
	idx.compactIfNeededLocked()
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
	idx.lockWrite()
	defer idx.mu.Unlock()
	for _, id := range ids {
		idx.deleteLocked(id)
	}
	idx.compactIfNeededLocked()
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
		idx.livePostings -= int64(len(entry.terms[class]))
	}
	idx.livePositions -= int64(entry.positionEntries)
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

// MarkIncomplete makes all subsequent context-aware searches fail closed.
func (idx *InvertedIndex[S, D]) MarkIncomplete() {
	idx.lockWrite()
	idx.health = IndexIncomplete
	idx.mu.Unlock()
}

// Health returns the current consistency state.
func (idx *InvertedIndex[S, D]) Health() IndexHealth {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.health
}

// Compact atomically rebuilds dictionaries, ordinals, postings, and document
// maps from the live logical corpus. Searches observe either complete state.
func (idx *InvertedIndex[S, D]) Compact() {
	idx.lockWrite()
	defer idx.mu.Unlock()
	idx.compactLocked()
}

// RebuildPrepared constructs a complete bounded replacement off to the side
// and swaps it in only after every aggregate limit succeeds. A failed rebuild
// leaves the prior (possibly incomplete) index untouched and unhealthy.
func (idx *InvertedIndex[S, D]) RebuildPrepared(items []PreparedItem[S]) error {
	var opts []IndexOption
	if idx.positions {
		opts = append(opts, WithPositions())
	}
	opts = append(opts, WithProximityWeight(idx.proximityWeight), WithAnalysisLimits(idx.limits))
	tmp := NewInvertedIndex[S, D](idx.analyzer, idx.scorer, idx.compareID, opts...)
	start := time.Now()
	if err := tmp.IndexManyPrepared(items); err != nil {
		return err
	}
	idx.lockWrite()
	defer idx.mu.Unlock()
	idx.classes, idx.ords, idx.docs = tmp.classes, tmp.ords, tmp.docs
	idx.livePostings, idx.livePositions = tmp.livePostings, tmp.livePositions
	idx.health = IndexHealthy
	idx.rebuildCount++
	idx.lastRebuildDuration = time.Since(start)
	return nil
}

func (idx *InvertedIndex[S, D]) compactIfNeededLocked() {
	liveTerms, retainedTerms := 0, 0
	for class := range idx.classes {
		liveTerms += len(idx.classes[class].postings)
		retainedTerms += len(idx.classes[class].dict.terms)
	}
	if len(idx.docs) == 0 {
		if retainedTerms > 0 || idx.ords.next > 0 {
			idx.compactLocked()
		}
		return
	}
	ratio := idx.limits.CompactionRatio
	if ratio <= 1 {
		return
	}
	retired := retainedTerms - liveTerms + int(idx.ords.next) - len(idx.docs)
	if retired < idx.limits.CompactionMinRetired {
		return
	}
	termHigh := liveTerms == 0 || float64(retainedTerms) > float64(liveTerms)*ratio
	ordHigh := float64(idx.ords.next) > float64(len(idx.docs))*ratio
	if termHigh || ordHigh {
		idx.compactLocked()
	}
}

func (idx *InvertedIndex[S, D]) compactLocked() {
	start := time.Now()
	items := make([]PreparedItem[S], 0, len(idx.docs))
	for ord, entry := range idx.docs {
		prepared := PreparedDocument{positionEntries: entry.positionEntries}
		for class := range entry.terms {
			preparedClass := preparedClass{
				length: entry.lengths[class],
				terms:  make([]preparedTerm, 0, len(entry.terms[class])),
			}
			frequencySum := 0
			for _, tid := range entry.terms[class] {
				pl := idx.classes[class].postings[tid]
				positions := []uint32(nil)
				frequency := pl.tf(ord)
				if idx.positions && class == int(ClassWord) {
					positions = pl.positionsOf(ord)
					frequency = len(positions)
				}
				preparedClass.terms = append(preparedClass.terms, preparedTerm{
					term:      idx.classes[class].dict.terms[tid],
					frequency: frequency,
					positions: positions,
				})
				frequencySum += frequency
			}
			// Frequencies above uint16 are intentionally clamped in postingList.
			// Preserve the exact document length across compaction by assigning
			// the unobservable excess to one already-saturated term.
			if missing := preparedClass.length - frequencySum; missing > 0 && len(preparedClass.terms) > 0 {
				preparedClass.terms[0].frequency += missing
			}
			sort.Slice(preparedClass.terms, func(i, j int) bool {
				return preparedClass.terms[i].term < preparedClass.terms[j].term
			})
			prepared.classes[class] = preparedClass
			prepared.postings += len(preparedClass.terms)
		}
		items = append(items, PreparedItem[S]{ID: entry.id, Prepared: prepared})
	}
	var opts []IndexOption
	if idx.positions {
		opts = append(opts, WithPositions())
	}
	opts = append(opts, WithProximityWeight(idx.proximityWeight), WithAnalysisLimits(idx.limits))
	tmp := NewInvertedIndex[S, D](idx.analyzer, idx.scorer, idx.compareID, opts...)
	// The logical state already passed these limits; rebuilding cannot fail.
	_ = tmp.IndexManyPrepared(items)
	idx.classes, idx.ords, idx.docs = tmp.classes, tmp.ords, tmp.docs
	idx.livePostings, idx.livePositions = tmp.livePostings, tmp.livePositions
	idx.rebuildCount++
	idx.lastRebuildDuration = time.Since(start)
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
	stats := idx.MemoryStats()
	return stats.LiveTerms, stats.Documents
}

// MemoryStats returns live/retained cardinalities, a conservative byte
// estimate, compaction history, and consistency health.
func (idx *InvertedIndex[S, D]) MemoryStats() IndexMemoryStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	stats := IndexMemoryStats{
		Documents:             len(idx.docs),
		Postings:              idx.livePostings,
		PositionEntries:       idx.livePositions,
		RetainedOrdinals:      int(idx.ords.next),
		RebuildCount:          idx.rebuildCount,
		LastRebuildDuration:   idx.lastRebuildDuration,
		WriteLockAcquisitions: idx.writeLockAcquisitions,
		Health:                idx.health,
	}
	var termBytes int64
	for class := range idx.classes {
		cp := &idx.classes[class]
		stats.LiveTerms += len(cp.postings)
		stats.RetainedTermSlots += len(cp.dict.terms)
		for term := range cp.dict.ids {
			termBytes += int64(len(term))
		}
	}
	// This estimate intentionally uses stable logical units rather than Go map
	// implementation details, making it comparable across builds and nodes.
	stats.EstimatedLiveBytes = termBytes + int64(stats.Documents)*32 + stats.Postings*12 + stats.PositionEntries
	stats.EstimatedRetainedBytes = stats.EstimatedLiveBytes + int64(stats.RetainedTermSlots-stats.LiveTerms)*16 + int64(stats.RetainedOrdinals-stats.Documents)*8
	return stats
}

func (idx *InvertedIndex[S, D]) lockWrite() {
	idx.mu.Lock()
	idx.writeLockAcquisitions++
}

// Interface assertions: InvertedIndex is both an Indexer and a Searcher.
var (
	_ Indexer[string, Text] = (*InvertedIndex[string, Text])(nil)
	_ Searcher[string]      = (*InvertedIndex[string, Text])(nil)
)
