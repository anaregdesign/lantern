package graphcache

import (
	"context"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/search"
)

// newSearchIndex builds the inverted index used by the optional content-search
// feature. Since #888 it installs the script-aware dual-channel pipeline
// (search.NewScriptAwareAnalyzer): width / NFC / full-case / emoji-presentation
// / punctuation / space normalizers feed a ScriptAwareTokenizer whose word runs index whole
// words (primary) plus intra-word bigrams (auxiliary, for infix and typo
// recall) and whose unbounded-script (CJK-like) runs index bigrams as the
// word-level unit. Matches are ranked by Okapi BM25 with the standard
// parameters, wrapped in ClassWeighted so auxiliary gram evidence counts at
// DefaultGramWeight and a whole-word match dominates fragment matches. The
// Query analysis adds only the two-rune auxiliary term needed to make short
// infix recall continuous. Match modes count ClassWord terms only; document
// postings and corpus statistics remain unchanged.
//
// Since #889 the index also records positional postings (WithPositions): the
// token positions of each primary-channel term, so the search layer can tell
// an exact phrase or a near match from scattered term hits. The cost is one
// position store per (word term, vertex). positions is opt-out
// (LANTERN_SEARCH_POSITIONS, #908): when false the index skips the position
// store entirely — SearchPhrase degrades to the AND-intersection and the
// proximity boost is inert, so an operator with a large corpus can trade
// phrase/proximity support for the memory.
//
// The relevance gate (core/search/relevance, parity_gate_test.go) replicates
// exactly this pipeline (with positions on) and ratchets its measured metrics
// against the pinned Lucene baseline — change the two in lockstep.
func newSearchIndex[S comparable](positions bool, limits search.SearchAnalysisLimits, compareID func(S, S) int) *search.InvertedIndex[S, search.Document] {
	analyzer := search.NewScriptAwareAnalyzer()
	scorer := search.ClassWeighted{
		Base:       search.BM25{K1: search.DefaultBM25K1, B: search.DefaultBM25B},
		GramWeight: search.DefaultGramWeight,
	}
	fieldScorer := search.FieldWeighted{Base: scorer, KeyWeight: search.DefaultKeyFieldWeight, ValueWeight: 1}
	var opts []search.IndexOption
	if positions {
		opts = append(opts, search.WithPositions())
	}
	opts = append(opts, search.WithAnalysisLimits(limits))
	return search.NewInvertedIndex[S, search.Document](analyzer, fieldScorer, compareID, opts...)
}

// SearchIndexOption configures the optional content-search index at
// EnableSearchIndex time. The zero configuration records positional postings;
// options only pare it back.
type SearchIndexOption func(*searchIndexConfig)

// searchIndexConfig is the resolved EnableSearchIndex configuration. positions
// defaults to true so the common call — EnableSearchIndex(extract, compareID)
// with no options — keeps recording positions exactly as before #908.
type searchIndexConfig struct {
	positions bool
	limits    search.SearchAnalysisLimits
}

// WithoutSearchPositions builds the search index without positional postings
// (the LANTERN_SEARCH_POSITIONS=false path, #908). The index then pays nothing
// for the per-(word term, vertex) position store; in exchange SearchPhrase
// degrades to the AND-intersection (word terms all present, order/adjacency
// unverified) and the proximity boost is inert. The SearchVertices RPC keeps
// working — a phrase query simply widens to the AND-intersection rather than
// failing.
func WithoutSearchPositions() SearchIndexOption {
	return func(c *searchIndexConfig) { c.positions = false }
}

// WithSearchAnalysisLimits installs hard document/aggregate budgets and
// retained-capacity compaction thresholds on the secondary index.
func WithSearchAnalysisLimits(limits search.SearchAnalysisLimits) SearchIndexOption {
	return func(c *searchIndexConfig) { c.limits = limits }
}

// EnableSearchIndex turns on the optional content-search index, projecting each
// stored (key, value) pair into the search.Document that gets indexed. Receiving
// the key lets a structured projection index implicit endpoint vertices as
// key-only documents. Like EnablePrefixIndex it must be called before any
// vertex is stored, is idempotent, and panics on a non-empty cache so the
// caller cannot silently observe an index that disagrees with point reads.
// extract and compareID must not be nil. compareID is the stable ascending
// typed-key order used to break equal-score ties; production passes raw lexical
// string-key comparison. By default the index records positional postings for
// phrase and proximity queries; pass WithoutSearchPositions to build the leaner
// position-free index (#908).
//
// Once enabled, every put (including overwrites) atomically replaces the value,
// postings, and expiration record. Explicit eviction drops the key through the
// shared SetOnEvictMany hook. A query purges due index entries before scoring,
// while the later TTL Flush hook performs an idempotent delete, so cache GC
// timing cannot affect live-corpus ranking.
// The index is a third opt-in secondary structure alongside the prefix index;
// when it is left disabled the put / evict hot paths pay only a nil check.
func (c *GraphCache[S, T]) EnableSearchIndex(extract func(S, T) search.Document, compareID func(S, S) int, opts ...SearchIndexOption) {
	if extract == nil {
		panic("graphcache: EnableSearchIndex extract must not be nil")
	}
	if compareID == nil {
		panic("graphcache: EnableSearchIndex compareID must not be nil")
	}
	cfg := searchIndexConfig{positions: true}
	for _, opt := range opts {
		opt(&cfg)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.searchIndex != nil {
		return // idempotent: re-enabling with the same intent is fine
	}
	if c.vertices.Count() != 0 {
		panic("graphcache: EnableSearchIndex must be called before any vertex is stored")
	}
	c.searchExtract = extract
	c.searchIndex = newSearchIndex[S](cfg.positions, cfg.limits, compareID)
}

// RebuildSearchIndex re-analyzes the complete live graph under the configured
// limits and atomically replaces an incomplete index. The replacement is built
// through bounded batches so recovery memory does not scale with a second
// corpus-wide PreparedItem slice. It fails without changing health when any
// current vertex still exceeds a bound.
func (c *GraphCache[S, T]) RebuildSearchIndex() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rebuildSearchIndexLocked()
}

// BeginSearchIndexRecovery makes search fail closed before a bulk recovery
// path starts mutating the graph. Replication snapshot and backup restore use
// this boundary so a partially replayed derived index is never advertised as
// healthy or queried for partial results.
func (c *GraphCache[S, T]) BeginSearchIndexRecovery() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.searchIndex == nil {
		return
	}
	// Preserve the graph -> search lock order used by every vertex writer.
	// Snapshot recovery runs concurrently with local writes, so taking these
	// locks in the opposite order can deadlock a writer that already owns mu.
	c.searchCommitMu.Lock()
	defer c.searchCommitMu.Unlock()
	c.searchIndex.MarkIncomplete()
}

// CompleteSearchIndexRecovery rebuilds the complete derived index from the
// live graph and marks it healthy only after the atomic replacement succeeds.
// A limit error leaves the graph intact and the index incomplete.
func (c *GraphCache[S, T]) CompleteSearchIndexRecovery() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Match the graph -> search lock order used by vertex writes. Recovery is
	// reachable from replication while those writes remain active.
	c.searchCommitMu.Lock()
	defer c.searchCommitMu.Unlock()
	return c.rebuildSearchIndexLocked()
}

func (c *GraphCache[S, T]) rebuildSearchIndexLocked() error {
	if c.searchIndex == nil {
		return nil
	}
	return c.searchIndex.RebuildDocuments(func(yield func(S, search.Document, time.Time) error) error {
		var firstErr error
		c.vertices.Range(func(key S, value T, expiration time.Time) bool {
			firstErr = yield(key, c.searchExtract(key, value), expiration)
			return firstErr == nil
		})
		return firstErr
	})
}

func (c *GraphCache[S, T]) rebuildIncompleteSearchLocked() {
	if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
		_ = c.rebuildSearchIndexLocked()
	}
}

// SearchVertices returns up to limit keys whose indexed content matches query,
// ranked most-relevant first by BM25. An empty or unanalyzable query, a
// disabled index, or limit <= 0 returns nil. A non-empty keyPrefix scopes the
// results to a namespace, keeping only keys whose projection (the same one the
// prefix index uses) starts with keyPrefix.
//
// Consistency mirrors ScanByPrefix: a hit is returned only when the vertex is
// still live, so a vertex whose TTL has expired but which has not yet been
// flushed is skipped — the returned set matches what a matching sequence of
// GetVertex calls under the snapshot lock would surface. Because matching is
// the index's boolean OR over the query terms, limit is applied after the
// liveness and prefix filters so a full page of live, in-scope hits is
// returned whenever one exists. Equal scores are ordered ascending by the
// typed compareID function supplied to EnableSearchIndex.
//
// Plural vertex writes/deletes publish their vertex and index mutations through
// one commit barrier, so a concurrent search observes the complete pre-batch or
// post-batch result set. Singular writes retain GetVertex's point-read contract
// and may race a search on that one key.
func (c *GraphCache[S, T]) SearchVertices(query string, limit int, keyPrefix string) []search.Result[S] {
	return c.SearchVerticesMatch(query, limit, keyPrefix, search.MatchOptions{}, false)
}

// SearchVerticesMatch is SearchVertices with explicit relevance options (#892):
// opts selects the match mode and prefix/fuzzy term expansion, and phrase
// requires the query's word terms to occur adjacently, in order. phrase takes
// precedence over opts.Mode — a phrase query is served by the index's phrase
// search. SearchVertices is exactly SearchVerticesMatch with the zero
// MatchOptions and phrase == false (the default OR-union). Liveness is pushed
// into the accept callback; when the prefix radix is enabled, prefix membership
// is streamed into core as a CandidateSource before scoring.
func (c *GraphCache[S, T]) SearchVerticesMatch(query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool) []search.Result[S] {
	results, _, _ := c.SearchVerticesMatchContext(context.Background(), query, limit, keyPrefix, opts, phrase, search.Budget{})
	return results
}

// SearchSnapshotHit is one relevance result plus the exact live value selected
// under the same search commit barrier. Found is false only when an unexpected
// backend inconsistency prevented hydration; callers must never pair the score
// with a later unrelated point read.
type SearchSnapshotHit[S comparable, T any] struct {
	Result search.Result[S]
	Value  T
	Found  bool
}

// SearchVerticesMatchContext is SearchVerticesMatch with cancellation and
// deterministic search-work accounting. An error returns no partial results.
func (c *GraphCache[S, T]) SearchVerticesMatchContext(ctx context.Context, query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool, budget search.Budget) ([]search.Result[S], search.Stats, error) {
	return c.searchVerticesContext(ctx, query, limit, keyPrefix, opts, phrase, budget, nil)
}

// SearchVerticesSnapshotContext ranks and hydrates one bounded result snapshot
// under the same commit barrier. It is used by cursor sessions and FULL_VERTEX
// projection so TTL/overwrite races cannot attach new content to an old score.
func (c *GraphCache[S, T]) SearchVerticesSnapshotContext(ctx context.Context, query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool, budget search.Budget) ([]SearchSnapshotHit[S, T], search.Stats, error) {
	var snapshot []SearchSnapshotHit[S, T]
	_, stats, err := c.searchVerticesContext(ctx, query, limit, keyPrefix, opts, phrase, budget, func(results []search.Result[S], queryNow time.Time) {
		snapshot = make([]SearchSnapshotHit[S, T], 0, len(results))
		for _, result := range results {
			value, found := c.vertices.GetAt(result.ID, queryNow)
			snapshot = append(snapshot, SearchSnapshotHit[S, T]{Result: result, Value: value, Found: found})
		}
	})
	return snapshot, stats, err
}

// searchVerticesContext owns the shared ranking path. When capture is non-nil,
// it runs before the search commit barrier is released so projection hydration
// observes the same liveness boundary without imposing wrapper allocations on
// the lightweight key+score path.
func (c *GraphCache[S, T]) searchVerticesContext(ctx context.Context, query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool, budget search.Budget, capture func([]search.Result[S], time.Time)) ([]search.Result[S], search.Stats, error) {
	if limit <= 0 {
		return nil, search.Stats{}, nil
	}
	// Phase 1 — capture the immutable references the search path needs under a
	// short RLock, then release c.mu before the expensive work. searchIndex and
	// prefixExtract are both set once at Enable*Index time (before any vertex is
	// stored) and never reassigned, so copying the pointers under the lock is
	// sufficient; we do not need to hold c.mu while they are used.
	c.mu.RLock()
	index := c.searchIndex
	prefixExtract := c.prefixExtract
	prefixIndex := c.prefixIndex
	c.mu.RUnlock()

	if index == nil {
		return nil, search.Stats{}, nil
	}
	// A prepared vertex batch updates the index and vertex store under the
	// exclusive side of this barrier. Holding the shared side for ranking plus
	// liveness filtering guarantees a search observes the complete pre-batch or
	// post-batch view, never an index/store midpoint. The barrier does not cover
	// analysis or any unrelated graph operation.
	c.searchCommitMu.RLock()
	defer c.searchCommitMu.RUnlock()
	queryNow := time.Now()
	// Phase 2 — query analysis, BM25 ranking, and liveness/prefix filtering all
	// run WITHOUT GraphCache.mu. Liveness is pushed into bounded execution as an
	// accept callback (#841), so a
	// broad query (the bigram analyzer makes a two-character query match most of
	// the corpus) never materialises and fully sorts its whole match set. A
	// non-empty prefix is pushed down separately through the radix candidate
	// source below, avoiding global scoring.
	//
	// Lock order: accept runs under searchCommitMu.RLock and the index's RLock,
	// then probes only the vertex cache's inner mutex (which never acquires the
	// index lock). This preserves the order already established by index writes
	// running under GraphCache.mu. Liveness is
	// checked via vertices.HasAt using the same queryNow passed to the index, so
	// candidate probes never resample the wall clock per posting. An
	// expired-but-not-yet-flushed vertex is
	// skipped; the read may race a concurrent Put/Delete and observe either side
	// of it, the same racy point-read contract GetVertex provides (#740).
	accept := func(id S) bool {
		if !c.vertices.HasAt(id, queryNow) {
			// Dead at the sampled instant or physically removed by a concurrent
			// Delete/Flush; consistent with the point-read race boundary.
			return false
		}
		return keyPrefix == "" || keyHasPrefix(prefixExtract, id, keyPrefix)
	}
	var out []search.Result[S]
	var stats search.Stats
	var err error
	execute := func(candidates search.CandidateSource[S]) {
		if phrase {
			out, stats, err = index.SearchPhraseTopKCandidatesContextAt(ctx, query, limit, accept, budget, queryNow, candidates)
		} else {
			out, stats, err = index.SearchMatchTopKCandidatesContextAt(ctx, query, limit, accept, opts, budget, queryNow, candidates)
		}
	}
	if keyPrefix != "" && prefixIndex != nil {
		// Establish radix -> inverted-index lock order and retain the radix read
		// lock only for the synchronous query. Writers already mutate the prefix
		// radix before search postings under GraphCache.mu, so this cannot cycle.
		prefixIndex.withPrefixWalk(keyPrefix, func(walk func(func(string) bool)) {
			execute(func(yield func(S) bool) {
				walk(func(projected string) bool {
					id, ok := c.keyForProjection(projected)
					return !ok || yield(id)
				})
			})
		})
	} else {
		execute(nil)
	}
	if err != nil {
		return nil, stats, err
	}
	if len(out) == 0 {
		return nil, stats, nil
	}
	if capture != nil {
		capture(out, queryNow)
	}
	return out, stats, nil
}

// keyHasPrefix reports whether key's string projection starts with prefix,
// using the same projection the prefix index uses when it is enabled
// (prefixExtract, which the caller captures under c.mu before releasing it).
// When the prefix index is disabled (prefixExtract == nil) it falls back to the
// identity projection for the string-keyed instantiation (Lantern's only
// production case); a non-string S without a prefix projection cannot be
// scoped, so it never matches a non-empty prefix. prefixExtract is set once at
// EnablePrefixIndex time and never reassigned, so it is safe to read without
// holding c.mu.
func keyHasPrefix[S comparable](prefixExtract func(S) string, key S, prefix string) bool {
	if prefixExtract != nil {
		return strings.HasPrefix(prefixExtract(key), prefix)
	}
	if s, ok := any(key).(string); ok {
		return strings.HasPrefix(s, prefix)
	}
	return false
}

// keyForProjection reverses the radix projection without applying liveness.
// Search evaluates liveness once at queryNow in accept; keeping projection and
// liveness separate avoids resampling the clock while candidates stream.
func (c *GraphCache[S, T]) keyForProjection(projected string) (S, bool) {
	var zero S
	if _, isString := any(zero).(string); isString {
		key, ok := any(projected).(S)
		return key, ok
	}
	return c.dict.findByProjection(c.prefixExtract, projected)
}
