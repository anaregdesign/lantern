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
// doc.String()) and raw queries pass through the configured Analyzer. An
// optional QueryAnalyzer may add query-only evidence; otherwise index-time and
// query-time terms stay symmetric.
//
// It splits the two responsibilities of relevance search. The index owns
// matching and the statistics behind it — per-document term frequencies and
// document lengths — while a pluggable Scorer (BM25 by default) turns those
// statistics into the ranking. Search returns hits in the stable total order
// (score descending, caller-provided typed ID comparator ascending).
//
// Terms live in per-class tables (#888): each TokenClass has its own term
// dictionary and postings. Corpus statistics and posting payloads are further
// scoped by semantic Document field (#1061), so a dual-channel analyzer
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
	clock           func() time.Time

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
	expirations           expiryHeap
	livePostings          int64
	livePositions         int64
	health                IndexHealth
	rebuildCount          uint64
	lastRebuildDuration   time.Duration
	writeLockAcquisitions uint64
	expirationPurged      uint64
	lastExpirationPurge   time.Duration
	generation            uint64
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
	// Field-local corpus statistics back BM25F-style scoring while the shared
	// dictionary and docs bitmap preserve key-or-value recall and expansion.
	fieldTotalLen [numDocumentFields]int
	fieldDocCount [numDocumentFields]int
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
	// fieldLengths keeps length normalization inside a semantic field.
	fieldLengths [numTokenClasses][numDocumentFields]int
	// terms is the de-duplicated list of distinct term ids the document was
	// indexed under, per class (see termDict), used to drop its postings on
	// delete or re-index. A []uint32 keeps the per-document forward index
	// compact.
	terms           [numTokenClasses][]uint32
	positionEntries int
	postingEntries  int
	expiry          *expiryRecord
}

// IndexOption configures an InvertedIndex at construction time.
type IndexOption func(*indexConfig)

// indexConfig holds the resolved construction options.
type indexConfig struct {
	positions       bool
	proximityWeight float64
	limits          SearchAnalysisLimits
	clock           func() time.Time
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

// WithIndexClock overrides the wall clock used for expiration decisions. It is
// primarily useful for deterministic tests; production callers normally use
// the default time.Now clock.
func WithIndexClock(clock func() time.Time) IndexOption {
	if clock == nil {
		panic("search: WithIndexClock clock must not be nil")
	}
	return func(c *indexConfig) { c.clock = clock }
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
	cfg.clock = time.Now
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
		clock:           cfg.clock,
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
	return idx.IndexWithExpiration(id, doc, time.Time{})
}

// IndexWithExpiration is Index with an absolute TTL deadline. Zero time and
// Unix epoch-or-earlier mean no expiration. A born-expired document behaves as
// a delete and never enters postings or the expiration heap.
func (idx *InvertedIndex[S, D]) IndexWithExpiration(id S, doc D, expiration time.Time) error {
	prepared, _, err := idx.Prepare(doc)
	if err != nil {
		return err
	}
	return idx.IndexPreparedWithExpiration(id, prepared, expiration)
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
	length       int
	fieldLengths [numDocumentFields]int
	terms        []preparedTerm
}

type preparedTerm struct {
	term      string
	frequency int
	fields    [numDocumentFields]preparedFieldTerm
}

type preparedFieldTerm struct {
	frequency int
	positions []uint64
}

type preparedOccurrence struct {
	token    Token
	field    FieldID
	position uint64
}

// PreparedItem pairs an id with its PreparedDocument for IndexManyPrepared.
type PreparedItem[S comparable] struct {
	ID         S
	Prepared   PreparedDocument
	Expiration time.Time
}

// Prepare analyzes a FieldedDocument's field instances (or doc.String() as one
// default field) WITHOUT taking the index lock and returns the
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
	fields := []DocumentField{{ID: FieldDefault, Text: doc.String()}}
	if fielded, ok := any(doc).(FieldedDocument); ok {
		fields = fielded.SearchFields()
	}
	var stats AnalysisStats
	for _, field := range fields {
		if field.ID >= numDocumentFields {
			panic("search: document with undefined FieldID")
		}
		stats.ProjectedBytes += len(field.Text)
	}
	if err := limitError(LimitDocumentBytes, int64(stats.ProjectedBytes), int64(idx.limits.MaxDocumentBytes)); err != nil {
		return PreparedDocument{}, stats, err
	}
	prepared := PreparedDocument{}
	var occurrences []preparedOccurrence
	var fieldInstances [numDocumentFields]uint32
	for _, field := range fields {
		instance := fieldInstances[field.ID]
		fieldInstances[field.ID]++
		remaining := 0
		if idx.limits.MaxDocumentTokens > 0 {
			remaining = idx.limits.MaxDocumentTokens - stats.Tokens
			if remaining < 1 {
				remaining = 1
			}
		}
		var tokens []Token
		if analyzer, ok := idx.analyzer.(boundedAnalyzer); ok {
			var exceeded bool
			tokens, exceeded = analyzer.AnalyzeBounded(field.Text, remaining)
			if exceeded {
				return PreparedDocument{}, stats, &AnalysisLimitError{Kind: LimitDocumentTokens, Got: int64(idx.limits.MaxDocumentTokens + 1), Limit: int64(idx.limits.MaxDocumentTokens)}
			}
		} else {
			tokens = idx.analyzer.Analyze(field.Text)
		}
		stats.Tokens += len(tokens)
		if err := limitError(LimitDocumentTokens, int64(stats.Tokens), int64(idx.limits.MaxDocumentTokens)); err != nil {
			return PreparedDocument{}, stats, err
		}
		var wordPosition uint32
		for _, token := range tokens {
			if int(token.Class) >= numTokenClasses {
				panic("search: token with undefined TokenClass")
			}
			occurrence := preparedOccurrence{token: token, field: field.ID}
			if idx.positions && token.Class == ClassWord {
				occurrence.position = uint64(instance)<<32 | uint64(wordPosition)
				wordPosition++
				prepared.positionEntries++
			}
			occurrences = append(occurrences, occurrence)
			prepared.classes[token.Class].length++
			prepared.classes[token.Class].fieldLengths[field.ID]++
		}
	}
	slices.SortFunc(occurrences, func(a, b preparedOccurrence) int {
		if a.token.Class < b.token.Class {
			return -1
		}
		if a.token.Class > b.token.Class {
			return 1
		}
		if cmp := strings.Compare(a.token.Term, b.token.Term); cmp != 0 {
			return cmp
		}
		if a.field < b.field {
			return -1
		}
		if a.field > b.field {
			return 1
		}
		if a.position < b.position {
			return -1
		}
		if a.position > b.position {
			return 1
		}
		return 0
	})
	for i := 0; i < len(occurrences); {
		j := i + 1
		for j < len(occurrences) && occurrences[j].token == occurrences[i].token {
			j++
		}
		class := occurrences[i].token.Class
		term := preparedTerm{term: occurrences[i].token.Term, frequency: j - i}
		for k := i; k < j; {
			end := k + 1
			for end < j && occurrences[end].field == occurrences[k].field {
				end++
			}
			fieldTerm := &term.fields[occurrences[k].field]
			fieldTerm.frequency = end - k
			if idx.positions && class == ClassWord {
				fieldTerm.positions = make([]uint64, 0, end-k)
				for _, occurrence := range occurrences[k:end] {
					fieldTerm.positions = append(fieldTerm.positions, occurrence.position)
				}
			}
			prepared.postings++
			k = end
		}
		prepared.classes[class].terms = append(prepared.classes[class].terms, term)
		i = j
	}
	for class := range prepared.classes {
		stats.UniqueTerms += len(prepared.classes[class].terms)
	}
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
	return idx.IndexPreparedWithExpiration(id, prepared, time.Time{})
}

// IndexPreparedWithExpiration applies a prepared document with an absolute
// expiration deadline.
func (idx *InvertedIndex[S, D]) IndexPreparedWithExpiration(id S, prepared PreparedDocument, expiration time.Time) error {
	idx.lockWrite()
	defer idx.mu.Unlock()
	now := idx.clock()
	item := PreparedItem[S]{ID: id, Prepared: prepared, Expiration: expiration}
	if err := idx.validatePreparedLocked([]PreparedItem[S]{item}, now); err != nil {
		return err
	}
	idx.indexPreparedLocked(item, now)
	idx.generation++
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
	now := idx.clock()
	if err := idx.validatePreparedLocked(items, now); err != nil {
		return err
	}
	for _, it := range finalPreparedItems(items) {
		idx.indexPreparedLocked(it, now)
	}
	idx.generation++
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
	return idx.validatePreparedLocked(items, idx.clock())
}

// IndexManyPreparedValidated applies a batch previously accepted by
// ValidateManyPrepared. The caller must serialize intervening writers.
func (idx *InvertedIndex[S, D]) IndexManyPreparedValidated(items []PreparedItem[S]) {
	if len(items) == 0 {
		return
	}
	idx.lockWrite()
	defer idx.mu.Unlock()
	now := idx.clock()
	for _, item := range finalPreparedItems(items) {
		idx.indexPreparedLocked(item, now)
	}
	idx.generation++
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
func (idx *InvertedIndex[S, D]) indexPreparedLocked(item PreparedItem[S], now time.Time) {
	// Replace semantics: drop any postings from a previous Index(id, ...)
	// before recording the new ones.
	idx.deleteLocked(item.ID)
	if item.Prepared.postings == 0 || !expirationLiveAt(item.Expiration, now) {
		return
	}
	ord := idx.ords.assign(item.ID)
	entry := docEntry[S]{id: item.ID}
	for class, preparedClass := range item.Prepared.classes {
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
			pl.setFields(ord, preparedTerm.fields)
			terms = append(terms, tid)
		}
		entry.lengths[class] = preparedClass.length
		entry.fieldLengths[class] = preparedClass.fieldLengths
		entry.terms[class] = terms
		cp.totalLen += preparedClass.length
		cp.docCount++
		for field, length := range preparedClass.fieldLengths {
			if length == 0 {
				continue
			}
			cp.fieldTotalLen[field] += length
			cp.fieldDocCount[field]++
		}
	}
	entry.positionEntries = item.Prepared.positionEntries
	entry.postingEntries = item.Prepared.postings
	if expirationFinite(item.Expiration) {
		record := &expiryRecord{at: item.Expiration, ord: ord, index: -1}
		heap.Push(&idx.expirations, record)
		entry.expiry = record
	}
	idx.docs[ord] = entry
	idx.livePostings += int64(entry.postingEntries)
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
func (idx *InvertedIndex[S, D]) validatePreparedLocked(items []PreparedItem[S], now time.Time) error {
	if idx.limits.MaxLiveTerms <= 0 && idx.limits.MaxLivePostings <= 0 && idx.limits.MaxPositionEntries <= 0 {
		return nil
	}
	last := make(map[S]PreparedItem[S], len(items))
	for _, item := range items {
		last[item.ID] = item
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
			postings -= int64(entry.postingEntries)
			for class := range entry.terms {
				for _, tid := range entry.terms[class] {
					key := termKey{class: TokenClass(class), term: idx.classes[class].dict.terms[tid]}
					loadDF(key)
					deltaDF[key]--
				}
			}
			positions -= int64(entry.positionEntries)
		}
	}
	for _, item := range last {
		if !expirationLiveAt(item.Expiration, now) {
			continue
		}
		prepared := item.Prepared
		for class, preparedClass := range prepared.classes {
			for _, term := range preparedClass.terms {
				key := termKey{class: TokenClass(class), term: term.term}
				loadDF(key)
				deltaDF[key]++
				for _, fieldTerm := range term.fields {
					if fieldTerm.frequency > 0 {
						postings++
					}
				}
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
	idx.generation++
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
	idx.generation++
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
	if entry.expiry != nil && entry.expiry.index >= 0 {
		heap.Remove(&idx.expirations, entry.expiry.index)
		entry.expiry = nil
	}
	idx.livePostings -= int64(entry.postingEntries)
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
		for field, length := range entry.fieldLengths[class] {
			if length == 0 {
				continue
			}
			cp.fieldTotalLen[field] -= length
			cp.fieldDocCount[field]--
		}
	}
	delete(idx.docs, ord)
	idx.ords.release(id, ord)
}

// MarkIncomplete makes all subsequent context-aware searches fail closed.
func (idx *InvertedIndex[S, D]) MarkIncomplete() {
	idx.lockWrite()
	idx.health = IndexIncomplete
	idx.generation++
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
	opts = append(opts, WithIndexClock(idx.clock))
	tmp := NewInvertedIndex[S, D](idx.analyzer, idx.scorer, idx.compareID, opts...)
	start := time.Now()
	if err := tmp.IndexManyPrepared(items); err != nil {
		return err
	}
	idx.lockWrite()
	defer idx.mu.Unlock()
	idx.classes, idx.ords, idx.docs, idx.expirations = tmp.classes, tmp.ords, tmp.docs, tmp.expirations
	idx.livePostings, idx.livePositions = tmp.livePostings, tmp.livePositions
	idx.health = IndexHealthy
	idx.rebuildCount++
	idx.generation++
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
				length:       entry.lengths[class],
				fieldLengths: entry.fieldLengths[class],
				terms:        make([]preparedTerm, 0, len(entry.terms[class])),
			}
			var frequencySum [numDocumentFields]int
			for _, tid := range entry.terms[class] {
				pl := idx.classes[class].postings[tid]
				term := preparedTerm{term: idx.classes[class].dict.terms[tid]}
				for field := FieldID(0); field < numDocumentFields; field++ {
					frequency := pl.tfInField(ord, field)
					if frequency == 0 {
						continue
					}
					positions := []uint64(nil)
					if idx.positions && class == int(ClassWord) {
						positions = pl.positionsInField(ord, field)
						frequency = len(positions)
					}
					term.fields[field] = preparedFieldTerm{frequency: frequency, positions: positions}
					term.frequency += frequency
					frequencySum[field] += frequency
					prepared.postings++
				}
				preparedClass.terms = append(preparedClass.terms, term)
			}
			// Frequencies above uint16 are intentionally clamped in postingList.
			// Preserve the exact document length across compaction by assigning
			// the unobservable excess to one already-saturated term.
			for field, length := range preparedClass.fieldLengths {
				missing := length - frequencySum[field]
				if missing <= 0 {
					continue
				}
				for i := range preparedClass.terms {
					if preparedClass.terms[i].fields[field].frequency > 0 {
						preparedClass.terms[i].fields[field].frequency += missing
						preparedClass.terms[i].frequency += missing
						break
					}
				}
			}
			sort.Slice(preparedClass.terms, func(i, j int) bool {
				return preparedClass.terms[i].term < preparedClass.terms[j].term
			})
			prepared.classes[class] = preparedClass
		}
		var expiration time.Time
		if entry.expiry != nil {
			expiration = entry.expiry.at
		}
		items = append(items, PreparedItem[S]{ID: entry.id, Prepared: prepared, Expiration: expiration})
	}
	var opts []IndexOption
	if idx.positions {
		opts = append(opts, WithPositions())
	}
	opts = append(opts, WithProximityWeight(idx.proximityWeight), WithAnalysisLimits(idx.limits))
	opts = append(opts, WithIndexClock(idx.clock))
	tmp := NewInvertedIndex[S, D](idx.analyzer, idx.scorer, idx.compareID, opts...)
	// The logical state already passed these limits; rebuilding cannot fail.
	_ = tmp.IndexManyPrepared(items)
	idx.classes, idx.ords, idx.docs, idx.expirations = tmp.classes, tmp.ords, tmp.docs, tmp.expirations
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
	return distinctQueryTerms(idx.analyzeQuery(query))
}

// distinctQueryTerms collapses an already analyzed token stream to the
// stable clause set used for scoring. Keeping analysis separate lets the
// bounded executor report both analyzer output (tokens) and scoring input
// (clauses) without running the analyzer twice.
func distinctQueryTerms(tokens []Token) []Token {
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

func (idx *InvertedIndex[S, D]) analyzeQuery(query string) []Token {
	if analyzer, ok := idx.analyzer.(QueryAnalyzer); ok {
		return analyzer.AnalyzeQuery(query)
	}
	return idx.analyzer.Analyze(query)
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
		for it := pl.docs.Iterator(); it.HasNext(); {
			if err := work.visit(WorkPostingVisits, 1); err != nil {
				return nil, err
			}
			ord := it.Next()
			addScore(scores, ord, idx.termScoreLocked(ord, token.Class, pl))
		}
	}
	dropNonFiniteScores(scores)
	return scores, nil
}

// termScoreLocked sums one term's field-local contributions for ord. Corpus
// statistics and length normalization never cross fields; the shared posting
// bitmap still makes result membership key-or-value.
func (idx *InvertedIndex[S, D]) termScoreLocked(ord uint32, class TokenClass, pl *postingList) float64 {
	cp := &idx.classes[class]
	entry := idx.docs[ord]
	var score float64
	for field := FieldID(0); field < numDocumentFields; field++ {
		tf := pl.tfInField(ord, field)
		if tf == 0 || cp.fieldDocCount[field] == 0 {
			continue
		}
		contribution := idx.scorer.Score(TermStats{
			TF:     tf,
			DF:     pl.fieldCardinality(field),
			N:      cp.fieldDocCount[field],
			DocLen: entry.fieldLengths[class][field],
			AvgLen: float64(cp.fieldTotalLen[field]) / float64(cp.fieldDocCount[field]),
			Class:  class,
			Field:  field,
		})
		if math.IsNaN(contribution) || math.IsInf(contribution, 0) {
			return math.NaN()
		}
		score += contribution
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return math.NaN()
		}
	}
	return score
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

// SearchTopK is the bounded-execution sibling of Search (#841/#1060): it
// multiway-merges sorted postings, scores one document at a time, and retains
// only the k highest-scoring accepted documents. It therefore avoids both the
// exhaustive ordinal-to-score map and the complete result sort. Working memory
// is O(k + query clauses), independent of the total match count M.
//
// accept gates a document before scoring and before it can occupy one of the k
// slots, so filtered-out documents (for example dead vertices) consume neither
// scoring work nor page budget. accept may be nil (accept everything). A
// layered store with a selective secondary index should use CandidateSource
// through SearchMatchTopKCandidatesContextAt instead of expressing that scope
// as an O(M) accept predicate.
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
	now := idx.clock()
	stats := IndexMemoryStats{
		Documents:              len(idx.docs),
		PhysicalDocuments:      len(idx.docs),
		ExpirationQueueEntries: len(idx.expirations),
		Postings:               idx.livePostings,
		PositionEntries:        idx.livePositions,
		RetainedOrdinals:       int(idx.ords.next),
		RebuildCount:           idx.rebuildCount,
		LastRebuildDuration:    idx.lastRebuildDuration,
		WriteLockAcquisitions:  idx.writeLockAcquisitions,
		ExpirationPurged:       idx.expirationPurged,
		LastExpirationPurge:    idx.lastExpirationPurge,
		Generation:             idx.generation,
		Health:                 idx.health,
	}
	var liveTermBytes, physicalTermBytes int64
	activeTerms := 0
	for class := range idx.classes {
		cp := &idx.classes[class]
		activeTerms += len(cp.postings)
		stats.RetainedTermSlots += len(cp.dict.terms)
		for term := range cp.dict.ids {
			physicalTermBytes += int64(len(term))
		}
	}
	stats.LiveTerms = activeTerms
	liveTermBytes = physicalTermBytes

	// The heap already contains exactly one node per physically retained
	// expiring document, so deriving the logical snapshot from due nodes avoids
	// scanning permanent/future documents. The temporary DF maps scale with the
	// cleanup lag, not the whole corpus.
	var expiredDF [numTokenClasses]map[uint32]int
	for _, record := range idx.expirations {
		if record.at.After(now) {
			continue
		}
		entry, ok := idx.docs[record.ord]
		if !ok || entry.expiry != record {
			continue
		}
		stats.Documents--
		stats.ExpiredDocuments++
		stats.PositionEntries -= int64(entry.positionEntries)
		stats.Postings -= int64(entry.postingEntries)
		for class := range entry.terms {
			if len(entry.terms[class]) > 0 && expiredDF[class] == nil {
				expiredDF[class] = make(map[uint32]int, len(entry.terms[class]))
			}
			for _, tid := range entry.terms[class] {
				expiredDF[class][tid]++
			}
		}
	}
	for class := range expiredDF {
		cp := &idx.classes[class]
		for tid, expiredDocuments := range expiredDF[class] {
			if expiredDocuments == cp.postings[tid].cardinality() {
				stats.LiveTerms--
				liveTermBytes -= int64(len(cp.dict.terms[tid]))
			}
		}
	}
	// This estimate intentionally uses stable logical units rather than Go map
	// implementation details, making it comparable across builds and nodes.
	stats.EstimatedLiveBytes = liveTermBytes + int64(stats.Documents)*32 + stats.Postings*12 + stats.PositionEntries
	stats.EstimatedRetainedBytes = physicalTermBytes + int64(stats.PhysicalDocuments)*32 + idx.livePostings*12 + idx.livePositions + int64(stats.RetainedTermSlots-activeTerms)*16 + int64(stats.RetainedOrdinals-stats.PhysicalDocuments)*8
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
