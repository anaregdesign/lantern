package graphcache

import (
	"strings"

	"github.com/anaregdesign/lantern/core/search"
)

// newSearchIndex builds the inverted index used by the optional content-search
// feature. It installs the multilingual bigram pipeline documented in
// core/search/example as the best general-purpose setup: width / diacritic /
// lowercase / punctuation / space normalizers feed an NGramTokenizer{N: 2}
// whose grams keep two-character (e.g. CJK) words indexable while still
// matching infixes, and a single WhitespaceFilter drops the grams that straddle
// a word boundary. Matches are ranked by Okapi BM25 with the standard
// parameters. The same analyzer runs over both stored values and queries, so
// index-time and query-time terms stay symmetric.
func newSearchIndex[S comparable]() *search.InvertedIndex[S, search.Document] {
	normalizers := []search.Normalizer{
		search.WidthNormalizer{},
		search.DiacriticNormalizer{},
		search.LowercaseNormalizer{},
		search.PunctuationNormalizer{},
		search.SpaceNormalizer{},
	}
	analyzer := search.NewAnalyzer(
		normalizers,
		search.NGramTokenizer{N: 2},
		[]search.TokenFilter{search.WhitespaceFilter{}},
	)
	scorer := search.BM25{K1: 1.2, B: 0.75}
	return search.NewInvertedIndex[S, search.Document](analyzer, scorer)
}

// EnableSearchIndex turns on the optional content-search index, projecting each
// stored value T into the search.Document that gets indexed (for example the
// vertex's string value). Like EnablePrefixIndex it must be called before any
// vertex is stored, is idempotent, and panics on a non-empty cache so the
// caller cannot silently observe an index that disagrees with point reads.
// extract must not be nil.
//
// Once enabled, the index is kept in perfect lockstep with the vertex
// lifecycle: every put (including overwrites) re-indexes the value, and
// eviction — Delete, DeleteMany, Clear, and TTL Flush — drops the key through
// the shared SetOnEvictMany hook, so entries decay together with the vertices
// they describe.
// The index is a third opt-in secondary structure alongside the prefix index;
// when it is left disabled the put / evict hot paths pay only a nil check.
func (c *GraphCache[S, T]) EnableSearchIndex(extract func(T) search.Document) {
	if extract == nil {
		panic("graphcache: EnableSearchIndex extract must not be nil")
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
	c.searchIndex = newSearchIndex[S]()
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
// returned whenever one exists.
func (c *GraphCache[S, T]) SearchVertices(query string, limit int, keyPrefix string) []search.Result[S] {
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
	// run WITHOUT GraphCache.mu. Since #841 the liveness and prefix filters are
	// pushed INTO the index's bounded top-k selection as the accept callback,
	// so a broad query (the bigram analyzer makes a two-character query match
	// most of the corpus) no longer materialises and fully sorts every match
	// just to keep `limit` of them: SearchTopK selects in O(M log k) with O(k)
	// result memory, and rejected documents never consume the page budget.
	//
	// Lock order: accept runs under the index's RLock and probes only the
	// vertex cache's inner mutex (which never acquires the index lock), an
	// order already established by index writes running under GraphCache.mu —
	// documented on SearchTopK. Liveness is checked via vertices.Has exactly as
	// before, so an expired-but-not-yet-flushed vertex is skipped; the read may
	// race a concurrent Put/Delete on a given key and observe either side of
	// it, which is the same racy point-read contract GetVertex provides (#740).
	accept := func(id S) bool {
		if !c.vertices.Has(id) {
			// Expired between the index write and this read but the async
			// Flush has not run yet; consistent with point-read semantics,
			// treat as absent.
			return false
		}
		return keyPrefix == "" || keyHasPrefix(prefixExtract, id, keyPrefix)
	}
	out := index.SearchTopK(query, limit, accept)
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
