# 0005: Stream exact top-k retrieval and preserve global BM25 statistics

Status: Accepted

## Context

The first bounded `SearchVertices(limit=k)` implementation replaced a complete
result sort with a size-`k` heap, but it still built an
`O(total matches)` ordinal-to-score map before selection. A selective key
prefix was also evaluated after global scoring, so one tenant's query work and
temporary heap grew with every out-of-scope tenant.

Prefix scoping raises a separate relevance choice: statistics could describe
either the complete live corpus or only the prefix subtree. Changing `N`,
`DF`, or average document length per prefix would change score bits and ranking
relative to the existing API.

## Decision

Top-k search uses an exact document-at-a-time executor. For an unscoped query,
it multiway-merges the sorted posting iterators and scores each distinct
candidate once. Match-mode coverage, fuzzy/prefix-expanded clauses, class
weights, and proximity are evaluated for that candidate before a size-`k`
heap retains it. Phrase search streams the rarest posting and verifies the
remaining postings and positions one candidate at a time. The exhaustive score
map remains the test and benchmark oracle.

A layered store may pass a streaming `CandidateSource` into core search.
GraphCache uses the key-prefix radix as that source, establishing radix then
inverted-index lock order and never materialising the prefix subtree as a
bitmap or slice. Core search remains independent of GraphCache.

Prefix defines membership only. BM25 `N`, `DF`, and average document length
remain statistics of the complete live corpus after query-time expiration
purge. This preserves the established score and ordering contract; a result
does not acquire a different relevance score merely because the caller adds a
prefix that still contains it.

The executor records `candidate_visits` and `candidate_skips` alongside the
existing posting, dictionary, position, and expiration counters. Existing
posting/position budgets and context checkpoints bound the dominant work; an
error returns no partial results.

## Consequences

- Unscoped top-k working memory is `O(k + query clauses/expansions)` and does
  not grow with the number of matching documents.
- Prefix-scoped work is proportional to prefix population times query
  complexity, independent of the out-of-scope corpus.
- Exact scores and the stable `(score DESC, key ASC)` boundary agree with the
  exhaustive implementation, including match modes, expansions, phrase, and
  proximity.
- Prefix traversal holds a read lock for the synchronous query. Very large
  prefixes can therefore delay prefix-index writers even though memory stays
  bounded; work budgets and cancellation remain the operational escape hatch.
