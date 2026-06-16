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
// eviction — Delete, Clear, and TTL Flush — drops the key through the shared
// SetOnEvict hook, so entries decay together with the vertices they describe.
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.searchIndex == nil {
		return nil
	}
	ranked := c.searchIndex.Search(query)
	if len(ranked) == 0 {
		return nil
	}
	out := make([]search.Result[S], 0, min(limit, len(ranked)))
	for _, r := range ranked {
		if !c.vertices.Has(r.ID) {
			// Expired between the index write and this snapshot but the
			// async Flush has not run yet; consistent with point-read
			// semantics, treat as absent.
			continue
		}
		if keyPrefix != "" && !c.keyHasPrefix(r.ID, keyPrefix) {
			continue
		}
		out = append(out, r)
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// keyHasPrefix reports whether key's string projection starts with prefix,
// using the same projection the prefix index uses when it is enabled. When the
// prefix index is disabled it falls back to the identity projection for the
// string-keyed instantiation (Lantern's only production case); a non-string S
// without a prefix projection cannot be scoped, so it never matches a
// non-empty prefix. Callers must hold c.mu.
func (c *GraphCache[S, T]) keyHasPrefix(key S, prefix string) bool {
	if c.prefixExtract != nil {
		return strings.HasPrefix(c.prefixExtract(key), prefix)
	}
	if s, ok := any(key).(string); ok {
		return strings.HasPrefix(s, prefix)
	}
	return false
}
