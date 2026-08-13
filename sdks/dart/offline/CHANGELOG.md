# Changelog

## Unreleased

- Consume server-authoritative Put outcomes during replay: confirm only a
  still-live `appliedAndLive`, terminalize `expired`, quarantine
  `conditionNotMet`/`superseded`, invalidate contradicted cache entries, and
  keep response/commit time monotone across wall-clock rollback.
- Reject retained operation/record identity collisions atomically with bounded
  generated-ID retries, seal transactions after callbacks, and fail closed on
  contradictory restored durable-state graphs.
- Sweep expiration and retention at public observation/control points, reclaim
  expired capacity without replay, retain dead letters from their transition
  time, use scoped deadline indexes for bounded due work, and bound
  process-local status/change notification resources.
- Add outbox codec schema v2 and reference snapshot schema v5 for exact
  dead-letter transition retention with conservative legacy migration.
- Preserve typed cancellation through the online adapter, isolate same-key
  single-flight waiters, cancel transport only after the final waiter leaves,
  and close idle watches immediately without missing initial store changes.
- Make the first-release durable mutation surface Put-only: retain
  `putVertex`, `putVertices`, `putEdge`, and `putEdges`, and remove offline Add
  enqueue/replay plus Add estimate metadata. Direct-online Add in
  `lantern_client` is unchanged.
- Add snapshot schema v5 durable auth-pause metadata and recover equivalent
  pause state from v1-v4 operation metadata. Legacy Add records in v1-v3
  become inspectable terminal `unsupported_add` dead letters with no overlay,
  retry attempt, or remote call; v4+ Add fails closed and generic retry fails with
  `OfflineUnsupportedOperationException` until #1115 provides
  server-authoritative operation receipts.
- Serialize and bound replay across every foreground entry point, make
  authentication pause durable until explicit resume, suppress nested online
  retries, cancel same-batch sibling token acquisition through a partition auth
  epoch, bound and evict process-local partition runtimes, and quiesce active,
  deferred, and watched partition work before wipe or repository disposal.

## 0.1.0

- Initial experimental storage-neutral offline Repository core.
- Add strict canonical v1 cache/outbox codecs and fresh-process conformance
  snapshots.
- Add partition/generation-safe confirmed caching, negative markers,
  cache-first revalidation, and coalesced local snapshot watches.
- Add atomic plural Put/PutEdge enqueue, pending overlays, expiration-safe
  foreground replay, probe-gated drain, and process-local status streams.
- Add global/per-partition capacity limits, lease recovery, content-free
  diagnostics, partition wipe, and authorized dead-letter controls.
- Add real-Lantern committed-response-loss coverage and maintained Flutter
  cached/pending/replay UX.
