# 0004: Make search expiration independent of background GC

Status: Accepted

## Context

Lantern vertices are logically dead at their absolute expiration even when the
periodic cache GC has not reclaimed their physical entries. Returning only
live keys is insufficient for full-text search: an expired document left in
the inverted index also changes BM25 corpus size, document frequency, average
length, proximity evidence, and fuzzy/prefix vocabulary. Nodes with the same
live graph could therefore rank it differently solely because their GC ticks
ran at different times.

## Decision

Search is a live read. Every query samples the index clock once and removes all
documents due at that instant before query analysis, expansion, scoring, or
proximity evaluation. A document expiring concurrently after that sample
belongs to the query snapshot; a write that is already expired at its own
sample is treated as a delete.

Each expiring index entry owns one node in an expiration min-heap. Overwrite
and delete remove that node eagerly, so the heap is bounded by the number of
physical indexed documents and does not retain stale generations. Posting
replacement and expiration bookkeeping occur under the same index write lock.
Replication and HLC apply carry the accepted vertex expiration through the
same prepared-batch path.

Query-time purge charges one `expiration_visits` unit per due document and the
normal `posting_visits` work needed to remove its distinct postings. Charges
and context cancellation are checked before each mutation. Completed removals
are safe incremental progress, but an exhausted or cancelled attempt returns
no partial search results. Operators bound the new work with
`LANTERN_SEARCH_MAX_EXPIRATION_VISITS`.

The cache's later background eviction remains authoritative for reclaiming the
vertex itself. Its search-index delete is idempotent when query-time purge ran
first, so GC timing cannot change ranking for an unchanged live set.

## Consequences

- BM25 statistics, result membership, vocabulary expansion, and proximity use
  one live corpus snapshot.
- Replicas with the same live data and search configuration produce the same
  ranking independently of their cache GC schedules.
- Status and Prometheus expose logical documents separately from physical and
  expired documents, expiration-heap size, cumulative query purges, and the
  latest purge duration.
- A large expiration backlog can require retryable bounded queries before it
  is drained; it cannot turn into an unbounded latency spike.
