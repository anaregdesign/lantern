# 0008: Retain bounded endpoint-sticky search snapshots for pagination

Status: Accepted

## Context

`SearchVertices` originally returned one bounded top-k list. Raising the page
limit could not reach matches beyond `LANTERN_SEARCH_MAX_LIMIT`, and the
response did not distinguish an exhaustive result from a truncated one.
Hydrating ranked keys in a second RPC also allowed TTL expiry or replacement to
pair an old score with missing or unrelated new content.

The preferred cursor was initially stateless: bind the request and search
configuration to the last `(score, key)` boundary and reject continuation when
the index generation changes. That is unusable under Lantern's normal churn.
The production `search_churn` scenario sustains 500 indexed vertex mutations/s
and the blocking qualification scenario uses 100/s. With a 20 ms interval
between pages, a strict global generation cursor becomes stale with probability
`1 - exp(-rate * interval)`: approximately 99.995% and 86.5%, respectively,
before TTL purges or replication are considered.

Weakening that cursor into best-effort search-after would keep it available but
could silently duplicate or skip results. Copying a cursor between replicas
would have the same problem because search membership, statistics, and mutation
visibility are local and eventual.

## Decision

The first page selects one immutable, bounded ranked snapshot in stable
`(score DESC, raw key ASC)` order. The endpoint retains at most
`LANTERN_SEARCH_MAX_SESSION_HITS` hits for an absolute
`LANTERN_SEARCH_CURSOR_TTL_SECONDS`. Retained sessions share count and aggregate
byte caps; least-recently-used sessions are evicted to admit new work. A result
larger than the retained hit cap, or a snapshot that cannot be admitted under
the byte cap, remains a successful first page but reports
`continuation_limited=true` rather than implying exhaustive continuation.

The opaque cursor is URL-safe, versioned, and HMAC-signed with an
endpoint-local CSPRNG key. It binds:

- a deterministic hash of query, prefix, normalized page limit, relevance
  options, and projection;
- the search configuration fingerprint, including analyzer/projection versions
  and session limits;
- an endpoint fingerprint, session identifier, page offset, and nanosecond
  expiry.

Malformed, tampered, cross-request, cross-configuration, and cross-endpoint
cursors return `INVALID_ARGUMENT` with `SEARCH_CURSOR_INVALID`. An expired or
evicted session returns `ABORTED` with `SEARCH_CURSOR_STALE`. Validation happens
before search execution. Clients never silently restart from page one.

The cursor is endpoint-sticky. Static SDK failover may rotate endpoints for the
first page, but a continuation calls only the endpoint that issued it. A lost
endpoint therefore makes the chain fail explicitly. Completed final pages stay
replayable until the absolute session expiry, so retrying an idempotent unary
page after a lost response returns the same data.

`truncated` is true whenever the current page is not the complete ranked
snapshot, including a bounded tail. `next_cursor` is non-empty only when the
endpoint retained another page. `effective_limit` reports the server-clamped
page size. No exact total count is computed.

The default `KEY_SCORE` projection preserves the lightweight response.
`FULL_VERTEX` selects the vertex value and TTL under the same GraphCache search
commit barrier and liveness instant as ranking. Its per-hit status is
`SNAPSHOT`; backends that cannot prove the selection return fail-closed
`MISSING` or `REPLACED` without an unrelated vertex. The retained session owns
clones of these values, so later writes, deletes, and expiry do not change an
existing page chain.

The index still publishes a monotonic generation in `GetServerStatus` for
diagnostics and convergence observation, but generation does not invalidate an
already retained session. The same status surface publishes cursor TTL and all
session caps so clients and operators can discover the contract.

## Measurement

The committed real Connect/h2c benchmark compares the two projections over the
same 50-hit response. A 20-iteration Apple M3 Max smoke run measured 2,402
wire bytes for `KEY_SCORE` and 21,702 for `FULL_VERTEX`; the short-run latency
was 0.88 ms and 0.81 ms, respectively. The wire-size delta is the durable
tradeoff; use longer repeated runs before treating the latency ordering as
significant.

Reproduce with:

```shell
go test ./tests/integration -run '^$' \
  -bench BenchmarkSearchVerticesProjectionOverRealH2C -benchmem
```

## Consequences

- Stable sessions enumerate retained hits without duplicates, gaps, or ranking
  drift while concurrent mutation and TTL expiry continue.
- The first request computes and retains a bounded prefix of the ranking; later
  pages avoid re-running the query and copy only their page.
- Memory is bounded by session count, retained-hit, and aggregate-byte limits.
  High churn can evict an idle cursor early, which is an explicit typed stale
  result.
- `FULL_VERTEX` increases first-page work, retained memory, and wire bytes in
  exchange for removing the hydration race and second RPC. `KEY_SCORE` remains
  the default for callers that do not need values.
- Load balancers must preserve endpoint affinity for a page chain. Replicas do
  not share sessions or signing keys, and failover does not claim cursor
  portability.
- Iterators stream pages and surface a bounded-tail error after the final
  retained hit; they never build an implicit unbounded result collection.

## Rejected alternatives

- **Strict index-generation cursor:** correct but almost always stale under the
  measured mutation rates.
- **Best-effort stateless search-after:** available under churn but permits
  silent duplicate/skip behavior.
- **Replica-portable cursor:** would require a shared snapshot/session service
  and search-state convergence that Lantern's AP local indexes do not provide.
- **Exact total hit count:** adds work unrelated to safe continuation and was
  not requested.
