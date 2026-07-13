# 0006: Preserve key and value field boundaries in full-text search

Status: Accepted

## Context

The original production projection concatenated a vertex key, one space, and
its rendered value. JSON string leaves were also space-joined. That made a
synthetic `key=alpha, value=beta` document satisfy phrase `alpha beta`, allowed
proximity across unrelated JSON leaves, and mixed key/value length and document
frequency statistics. Edge-created endpoint vertices were visible to point and
prefix reads but absent from full-text search until an explicit vertex put.

## Decision

`core/search.Document` keeps its single-text contract and gains the optional
`FieldedDocument` interface. Structured documents expose stable `FieldKey` and
`FieldValue` instances; unstructured callers continue to use `FieldDefault`
with byte-for-byte compatible behavior.

The term dictionary and global candidate bitmap remain shared so exact,
fuzzy, prefix-term, and match-mode membership retain key-or-value recall. Each
posting additionally records field-local membership, term frequency, document
length, corpus size, document frequency, and positions. BM25 therefore runs
inside one field. Positions encode a field-instance number plus token offset;
phrase and proximity require one common field instance and can never cross a
key/value or JSON-leaf boundary.

Key evidence uses a `1.75` multiplier; value/default evidence uses `1.0`. A
pinned three-query qrel sweep records `1/3` top-1 accuracy without weighting
and `3/3` across the measured `[1.75, 2.25]` plateau. The lowest plateau value
is selected to lift exact namespace evidence conservatively.

GraphCache's extractor receives `(key, value)`. Every endpoint auto-creation
indexes a key-only structured document with the endpoint TTL; a later explicit
put replaces it atomically with key plus value fields. JSON object traversal
remains key-sorted, field names and non-string scalars remain excluded, and
each string leaf becomes a separate value instance.

The observable projection version changes from `vertex-key-value-v1` to
`vertex-fields-v2` and remains part of the search config fingerprint.

On the 20,000-document structured benchmark (Apple M3 Max, positions and
two-term proximity enabled), the flattened and structured paths measured:

| Projection | query latency | query alloc | retained heap |
|---|---:|---:|---:|
| flattened | 6.53 ms/op | 2,080 B/op, 54 allocs | 1,356 B/document |
| structured | 6.67 ms/op | 2,058 B/op, 52 allocs | 1,478 B/document |

The enabled default therefore paid about 2% query latency and 9% retained heap
in this fixture; query allocations did not regress.

## Consequences

- Synthetic boundaries no longer create phrase or proximity evidence.
- Key and value relevance can evolve through measured field weights without
  changing the analyzer or duplicating dictionaries.
- Field-local bitmaps and corpus counters add retained memory. The structured
  benchmark records this cost next to query allocations and latency.
- `MaxLivePostings` now counts `(document, term, field)` memberships, matching
  the retained posting structures rather than only the shared term union.
- Nodes with different projection versions are observably incompatible for
  deterministic search convergence and future cursor portability.
