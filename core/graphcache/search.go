package graphcache

import (
	"strings"

	"github.com/anaregdesign/lantern/core/search"
)

// newSearchIndex builds the inverted index used by the optional content-search
// feature. Since #888 it installs the script-aware dual-channel pipeline
// (search.NewScriptAwareAnalyzer): width / diacritic / lowercase / punctuation
// / space normalizers feed a ScriptAwareTokenizer whose word runs index whole
// words (primary) plus intra-word bigrams (auxiliary, for infix and typo
// recall) and whose unbounded-script (CJK-like) runs index bigrams as the
// word-level unit. Matches are ranked by Okapi BM25 with the standard
// parameters, wrapped in ClassWeighted so auxiliary gram evidence counts at
// DefaultGramWeight and a whole-word match dominates fragment matches. The
// same analyzer runs over both stored values and queries, so index-time and
// query-time terms stay symmetric.
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
func newSearchIndex[S comparable](positions bool, compareID func(S, S) int) *search.InvertedIndex[S, search.Document] {
	analyzer := search.NewScriptAwareAnalyzer()
	scorer := search.ClassWeighted{
		Base:       search.BM25{K1: search.DefaultBM25K1, B: search.DefaultBM25B},
		GramWeight: search.DefaultGramWeight,
	}
	var opts []search.IndexOption
	if positions {
		opts = append(opts, search.WithPositions())
	}
	return search.NewInvertedIndex[S, search.Document](analyzer, scorer, compareID, opts...)
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

// EnableSearchIndex turns on the optional content-search index, projecting each
// stored value T into the search.Document that gets indexed (for example the
// vertex's string value). Like EnablePrefixIndex it must be called before any
// vertex is stored, is idempotent, and panics on a non-empty cache so the
// caller cannot silently observe an index that disagrees with point reads.
// extract and compareID must not be nil. compareID is the stable ascending
// typed-key order used to break equal-score ties; production passes raw lexical
// string-key comparison. By default the index records positional postings for
// phrase and proximity queries; pass WithoutSearchPositions to build the leaner
// position-free index (#908).
//
// Once enabled, the index is kept in perfect lockstep with the vertex
// lifecycle: every put (including overwrites) re-indexes the value, and
// eviction — Delete, DeleteMany, Clear, and TTL Flush — drops the key through
// the shared SetOnEvictMany hook, so entries decay together with the vertices
// they describe.
// The index is a third opt-in secondary structure alongside the prefix index;
// when it is left disabled the put / evict hot paths pay only a nil check.
func (c *GraphCache[S, T]) EnableSearchIndex(extract func(T) search.Document, compareID func(S, S) int, opts ...SearchIndexOption) {
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
	c.searchIndex = newSearchIndex[S](cfg.positions, compareID)
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
func (c *GraphCache[S, T]) SearchVertices(query string, limit int, keyPrefix string) []search.Result[S] {
	return c.SearchVerticesMatch(query, limit, keyPrefix, search.MatchOptions{}, false)
}

// SearchVerticesMatch is SearchVertices with explicit relevance options (#892):
// opts selects the match mode and prefix/fuzzy term expansion, and phrase
// requires the query's word terms to occur adjacently, in order. phrase takes
// precedence over opts.Mode — a phrase query is served by the index's phrase
// search. SearchVertices is exactly SearchVerticesMatch with the zero
// MatchOptions and phrase == false (the default OR-union). Liveness and prefix
// scoping are identical, and the bounded top-k selection still pushes them into
// the accept callback so a broad query never materialises its whole match set.
func (c *GraphCache[S, T]) SearchVerticesMatch(query string, limit int, keyPrefix string, opts search.MatchOptions, phrase bool) []search.Result[S] {
	if limit <= 0 {
		return nil
	}
	// Phase 1 — capture the immutable references the search path needs under a
	// short RLock, then release c.mu before the expensive work. searchIndex and
	// prefixExtract are both set once at Enable*Index time (before any vertex is
	// stored) and never reassigned, so copying the pointers under the lock is
	// sufficient; we do not need to hold c.mu while they are used.
	c.mu.RLock()
	index := c.searchIndex
	prefixExtract := c.prefixExtract
	c.mu.RUnlock()

	if index == nil {
		return nil
	}
	// Phase 2 — query analysis, BM25 ranking, and liveness/prefix filtering all
	// run WITHOUT GraphCache.mu. The liveness and prefix filters are pushed INTO
	// the index's bounded top-k selection as the accept callback (#841), so a
	// broad query (the bigram analyzer makes a two-character query match most of
	// the corpus) never materialises and fully sorts its whole match set.
	//
	// Lock order: accept runs under the index's RLock and probes only the vertex
	// cache's inner mutex (which never acquires the index lock), an order already
	// established by index writes running under GraphCache.mu. Liveness is
	// checked via vertices.Has, so an expired-but-not-yet-flushed vertex is
	// skipped; the read may race a concurrent Put/Delete and observe either side
	// of it, the same racy point-read contract GetVertex provides (#740).
	accept := func(id S) bool {
		if !c.vertices.Has(id) {
			// Expired between the index write and this read but the async Flush
			// has not run yet; consistent with point-read semantics, absent.
			return false
		}
		return keyPrefix == "" || keyHasPrefix(prefixExtract, id, keyPrefix)
	}
	var out []search.Result[S]
	if phrase {
		out = index.SearchPhraseTopK(query, limit, accept)
	} else {
		out = index.SearchMatchTopK(query, limit, accept, opts)
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
