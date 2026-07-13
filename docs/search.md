# SearchVertices contract

This is the canonical contract for Lantern content search. It defines what is
indexed, how membership and ranking work, which failures are machine-readable,
and what changes in a replicated deployment. The RPC schema lives in
[`graph.proto`](../proto/graph/v1/graph.proto); this guide is the normative
developer and operator interpretation of that schema.

Search is a derived, in-memory index over the same live vertices served by the
KVS. It is not a second source of truth. Prefix scans answer “which keys begin
with this prefix?”; `SearchVertices` answers “which live vertices are most
relevant to this analyzed query?”

## Discover the serving endpoint first

Read `GetServerStatus.search` instead of hard-coding deployment assumptions.
The response reports whether search and positional postings are enabled, the
effective defaults and limits, implementation versions, current index health,
and a `config_fingerprint`. The fingerprint covers search-affecting settings
and must be identical across serving replicas.

The `SearchCapabilities` fields are:

| Field | Contract |
| --- | --- |
| `enabled` | Whether this endpoint accepts content-search requests. |
| `positions_enabled` | Whether phrase adjacency and proximity evidence are available. |
| `default_limit` / `max_limit` | Page-size default and hard ceiling. |
| `default_match_mode` / `default_min_should_match` | Membership defaults when the request defers to the server. |
| `max_fuzziness` | Largest accepted edit distance. |
| `analyzer_version` / `projection_version` | Stable identifiers for the text analyzer and vertex-to-document projection. |
| `config_fingerprint` | SHA-256 fingerprint used to detect heterogeneous HA members and bind cursors. |
| `timeout_ms` | Server execution deadline for a page-one search attempt. |
| `max_query_bytes` / `max_query_terms` | Query input and analyzed-term limits. |
| `max_dictionary_visits` / `max_posting_visits` / `max_position_visits` | Deterministic per-query work budgets. |
| `max_in_flight` | Endpoint-local admission slots. |
| `max_document_bytes` / `max_document_tokens` / `max_document_terms` | Per-document indexing limits. |
| `max_live_terms` / `max_live_postings` / `max_position_entries` | Aggregate live-index capacity limits. |
| `compaction_ratio` / `compaction_min_retired` | Retained-to-live compaction trigger. |
| `index_stats` | Current `SearchIndexStats`, including health, logical/physical size, expiration, rebuild, and generation diagnostics. |
| `max_expiration_visits` | Maximum expired documents synchronously purged by one search attempt. |
| `cursor_ttl_seconds` | Lifetime of an endpoint-local retained search session. |
| `max_sessions` / `max_session_hits` / `max_session_bytes` | Endpoint-local retained-session bounds. |

The server environment variables behind these fields are generated in
[`env.md`](env.md). A client should treat the status response, not those
documented defaults, as authoritative for the endpoint it reached.

## Index lifecycle and document projection

When `enabled=false`, no content index is built and calls fail with
`SEARCH_DISABLED`. When enabled, the server publishes store and index changes
through one commit barrier. `positions_enabled=false` omits positions to save
memory; ordinary search still works, while phrase search fails explicitly with
`SEARCH_POSITIONS_DISABLED`.

Every live vertex produces one search document with a key field and zero or
more value-field instances. The current versions are discoverable rather than
inferred; at the time of writing they are `vertex-fields-v2` and
`script-aware-v2`.

| Vertex value | Indexed value text |
| --- | --- |
| `string` | A plain or malformed-JSON string is indexed verbatim. A valid JSON object/array contributes only its non-empty string leaves. Object keys and non-string scalars are not indexed. Each leaf is a separate field instance, visited in sorted object-key order for deterministic projection. |
| `bytes` | Indexed only when the bytes are valid UTF-8. Opaque binary contributes no value text. |
| signed / unsigned integer | Base-10 representation. |
| float | Stable shortest decimal representation. |
| `bool` | `true` or `false`. |
| timestamp | RFC 3339 with nanosecond precision. |
| duration | Go duration text such as `1h30m0s`. |
| nil / unset | No value text. |

The vertex key is always its own field. Phrase and proximity evidence never
cross the key/value boundary or two JSON string leaves. Key matches receive a
higher field weight than value matches. Vertices created implicitly as edge
endpoints have no value, but their keys are immediately searchable.

A write whose projected document exceeds a per-document or aggregate index
capacity limit fails atomically with `SEARCH_INDEX_BUDGET_EXHAUSTED`; the graph
and index do not diverge. An unhealthy derived index reports
`SEARCH_INDEX_HEALTH_INCOMPLETE` and search fails closed with
`SEARCH_INDEX_INCOMPLETE` until a bounded rebuild succeeds.
`SEARCH_INDEX_HEALTH_DISABLED` means indexing is intentionally off,
`SEARCH_INDEX_HEALTH_HEALTHY` is the only serving index state, and
`SEARCH_INDEX_HEALTH_UNSPECIFIED` means the endpoint supplied no recognized
health value.

## Unicode analysis and ranking

The analyzer applies width folding, canonical NFC normalization, Unicode full
case folding, and removal of emoji text/presentation selectors. It preserves
meaningful combining marks, Unicode symbols, and emoji. Punctuation separates
runs; symbols remain searchable rather than disappearing.

Word-like runs contribute a whole-word term plus lower-weight intra-word
bigrams. Unbounded scripts such as CJK use bigrams as their primary terms. A
two-rune non-CJK query also enters the auxiliary infix channel, so `ar` can
recall `search`, while exact whole-word evidence remains stronger. Analyzer
behavior is versioned by `analyzer_version`; changing it is a configuration
change, not silent query drift.

Candidates are scored with field-local BM25 plus positional proximity when
positions are enabled. Results have the stable total order
`(score DESC, raw key ASC)`. BM25 scores are relative to the query and the
endpoint's current corpus: do not compare numeric scores across unrelated
queries, changing corpora, or replicas that have not converged. Given the same
live graph and identical search configuration, replicas converge to the same
ordered hits and score bits.

## Request membership and validation

`SearchVerticesRequest` carries:

| Field | Contract |
| --- | --- |
| `query` | UTF-8 search input. Empty or unanalysable input succeeds with no hits. |
| `limit` | Page size. Zero selects `default_limit`; larger values clamp to `max_limit`. |
| `prefix` | Optional raw vertex-key namespace restriction, applied to membership. |
| `options` | Optional relevance and membership controls below. Omission preserves every server default. |
| `cursor` | Opaque endpoint-sticky continuation. Empty starts a new snapshot. |
| `projection` | `SEARCH_PROJECTION_KEY_SCORE` by default, or `SEARCH_PROJECTION_FULL_VERTEX`. |

`SearchOptions` carries:

| Field | Contract |
| --- | --- |
| `match_mode` | `MATCH_MODE_UNSPECIFIED` defers to `default_match_mode`; `MATCH_MODE_ANY`, `MATCH_MODE_ALL`, and `MATCH_MODE_MIN_SHOULD` explicitly override it. |
| `min_should_match` | With `MATCH_MODE_MIN_SHOULD`, zero selects `default_min_should_match`; a positive value sets the threshold. Invalid with every other mode. |
| `phrase` | Requires word terms to occur adjacently and in order within one field instance. Requires positions. |
| `fuzziness` | Dictionary edit distance from zero through `max_fuzziness` (currently at most 2). |
| `prefix_terms` | Also match dictionary terms that extend a query word, so `lan` can match `lantern`. |

Membership is OR for `MATCH_MODE_ANY`, AND for `MATCH_MODE_ALL`, and “at least
N distinct analyzed query word terms” for `MATCH_MODE_MIN_SHOULD`. Auxiliary
infix grams improve recall and ranking; they do not inflate the distinct-word
threshold.

Phrase currently cannot compose with an explicit `match_mode`, a non-zero
`min_should_match`, `fuzziness`, or `prefix_terms`. Unknown enum values,
fuzziness outside the reported range, thresholds under the wrong mode, and
other ignored/ambiguous combinations fail as `INVALID_ARGUMENT`; the server
does not silently reinterpret them.

## TTL, writes, and snapshot consistency

One search attempt captures a single logical `now`. A vertex that expired at
that instant is excluded even if background GC has not physically removed it.
Ranking and `FULL_VERTEX` hydration use that same liveness instant and the same
commit barrier.

Batch writes and deletes are search-atomic: a search observes the state before
or after the batch, never its midpoint. A concurrent single-key overwrite can
appear on either side of the barrier, but never as a score from one version
hydrated with the unrelated value of another. `FULL_VERTEX` therefore removes
the racy `SearchVertices` then `GetVertices` pattern.

Once page one creates a retained session, later writes, deletes, TTL expiry,
or compaction do not change that session's ranked membership or exact vertex
snapshots. New page-one searches observe the current live graph.

## Projection and page response

`SEARCH_PROJECTION_UNSPECIFIED` normalizes to the lightweight default.
`SEARCH_PROJECTION_KEY_SCORE` returns only `key`, `score`, and
`SEARCH_HIT_PROJECTION_STATUS_KEY_SCORE`. It is the lightweight default.

`SEARCH_PROJECTION_FULL_VERTEX` carries the exact selection-time value and TTL
with `SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT`. A backend that cannot prove that
snapshot fails closed per hit with `SEARCH_HIT_PROJECTION_STATUS_MISSING` or
`SEARCH_HIT_PROJECTION_STATUS_REPLACED`; it never attaches an unrelated value.
`SEARCH_HIT_PROJECTION_STATUS_UNSPECIFIED` is reserved for compatibility with
an endpoint that did not report a status.

`SearchVerticesResponse` carries:

| Field | Contract |
| --- | --- |
| `hits` | The ordered page of projected search hits. |
| `next_cursor` | Opaque continuation; empty means there is no admitted next page. |
| `effective_limit` | Server-selected page size after defaulting/clamping. |
| `truncated` | More ranked hits existed beyond this page. It is not an exact total. |
| `continuation_limited` | A bounded session retained only a prefix of all matches. SDK iterators surface this after the final retained hit. |

Lantern intentionally does not return an approximate count as an exact total.
Use `next_cursor`, `truncated`, and `continuation_limited` to describe what is
available.

Repeat the exact query, limit, prefix, options, projection, and endpoint for
each cursor page. Cursors are signed and bound to the request, configuration,
and endpoint. Treat them as opaque and never persist them beyond
`cursor_ttl_seconds`.

## Typed failures, budgets, and retry decisions

Transport status is the broad category; `SearchErrorDetail.reason` is the
stable search-specific branch. Every defined `SearchErrorReason` is listed
here so a proto addition requires a documentation update:

| Reason | Status and action |
| --- | --- |
| `SEARCH_ERROR_REASON_UNSPECIFIED` | No recognized search detail. Branch on the transport status only. |
| `SEARCH_DISABLED` | `FAILED_PRECONDITION`. Render search as unavailable or use key scans. |
| `SEARCH_POSITIONS_DISABLED` | `FAILED_PRECONDITION`. Disable phrase UI or issue a non-phrase query explicitly. |
| `SEARCH_WORK_BUDGET_EXHAUSTED` | `RESOURCE_EXHAUSTED`. `work_kind` is one of `query_bytes`, `query_terms`, `dictionary_visits`, `posting_visits`, or `position_visits`; narrow the query, namespace, fuzziness, or prefix expansion. |
| `SEARCH_ADMISSION_SATURATED` | `RESOURCE_EXHAUSTED`. A short jittered retry of the unchanged newest query is reasonable. |
| `SEARCH_INDEX_INCOMPLETE` | `FAILED_PRECONDITION`. Rebuild/repair the local derived index before serving it. |
| `SEARCH_INDEX_BUDGET_EXHAUSTED` | `RESOURCE_EXHAUSTED` on a write. Increase index capacity or reduce/index less content; the write was not partially applied. |
| `SEARCH_CURSOR_STALE` | `ABORTED`. The endpoint-local session expired or was evicted; restart from page one. |
| `SEARCH_CURSOR_INVALID` | `INVALID_ARGUMENT`. The cursor was altered or used with a different request, endpoint, or config; restart from page one and fix routing/request reuse. |
| `SEARCH_CONTINUATION_LIMITED` | Terminal iterator/stream condition after the final retained hit when `continuation_limited=true`; narrow the query or increase bounded session capacity. |

Ordinary option validation is `INVALID_ARGUMENT` and may have no search
reason. Caller cancellation and deadline expiry are `CANCELED` and
`DEADLINE_EXCEEDED`; both return no partial page. A work-budget failure is also
terminal for that attempt. Incremental clients should cancel superseded work,
drop stale replies, and issue only the newest input instead of replaying old
queries.

The endpoint limits query bytes/terms, dictionary/posting/position visits,
in-flight work, document size/tokens/terms, aggregate index cardinality,
expiration cleanup, and retained sessions. This containment is part of the
public contract. Inspect capabilities and
[`lantern_search_*` metrics](observability.md#57-query-subsystems--illuminate--scan--search)
before changing a limit.

## Replication, failover, and cursors

Search is a local, eventual read. It does not wait for peer convergence and
does not perform read repair. During replication lag, membership, BM25 corpus
statistics, scores, and pagination snapshots can differ between replicas;
after graph and configuration convergence, deterministic analysis and ordering
converge too.

Running serving replicas with different search-affecting configuration is
prohibited. Readiness becomes `NOT_SERVING` on a fingerprint mismatch, but
replication continues so operators can repair configuration without losing
events. Compare `GetServerStatus.search.config_fingerprint` across every member.

Cursor sessions and signing keys are endpoint-local. Keep a cursor chain on
one endpoint with load-balancer affinity at least as long as
`cursor_ttl_seconds`. Static SDK failover may select another endpoint for page
one, but it must not migrate a non-empty cursor. On failover or a stale/invalid
cursor, discard the chain and restart from page one; the new replica may
legitimately return a different one-page result until convergence.

See the [replication RFC](replication.md#read-consistency-contract),
[HA runbook](ha-runbook.md#43-search-health-and-replica-consistency), and
[bounded pagination ADR](decisions/0008-bounded-search-pagination-sessions.md)
for the underlying operational decisions.

## Maintained examples and command surfaces

The maintained SDK examples compile in CI and cover capability discovery,
one-shot namespace search, phrase/typo options, bounded pagination, typed
disabled handling, cancellation, and incremental latest-query-wins search:

- [Go search example](../sdks/go/example/search.go)
- [Node search example](../sdks/node/example/search.ts)
- [Dart/Flutter search example](../sdks/dart/example/lib/main.dart)

CLI and Admin `/cli` use the same grammar. The standalone command uses flags
with the same meanings (`--mode`, `--min-should`, `--phrase`, `--fuzziness`,
`--prefix-terms`, `--prefix`, `--limit`, `--all`, and `--projection`). One-page
JSON is a lossless response envelope; `all=true`/`--all` streams bounded pages
as NDJSON by default. TSV is an explicit presentation format.

The following commands are parsed by the production grammar in the docs drift
gate:

<!-- search-cli-grammar:start -->
```text
search "rolling update" mode=all limit=20
search "quiet cafe" prefix=place: projection=full-vertex
search "release notes" phrase=true
search serach fuzziness=1
search lan prefix_terms=true
search espresso limit=20 all=true format=ndjson
```
<!-- search-cli-grammar:end -->

The Admin Vertices page exposes the same membership and expansion controls and
disables phrase mode unless the endpoint reports positional postings. The Ops
page shows the discoverable capability, budget, fingerprint, and index-health
snapshot. Surface wording should link back here rather than restating a
partial contract.

## Observability checklist

Before declaring search healthy:

1. `GetServerStatus.search.enabled` is true and `index_stats.health` is
   `SEARCH_INDEX_HEALTH_HEALTHY` on every serving member.
2. `config_fingerprint`, `analyzer_version`, and `projection_version` match
   across replicas.
3. Search call latency, terminal outcomes/reasons, work visits, admission
   saturation, index capacity, expiration cleanup, sessions, and compaction
   are within the [documented SLOs](observability.md#57-query-subsystems--illuminate--scan--search).
4. Load balancing preserves cursor affinity and operational probes exercise
   both an ordinary query and phrase behavior when positions are enabled.

The integration docs gate reflects the proto descriptors for
`SearchOptions`, `SearchVerticesRequest`, `SearchVerticesResponse`, and
`SearchErrorReason`. Adding or renaming a wire option, response field, or
reason without updating this guide fails the ordinary root test suite.
