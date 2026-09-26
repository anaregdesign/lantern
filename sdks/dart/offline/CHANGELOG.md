# Changelog

## Unreleased

- Require published `lantern_client ^0.3.1` for receipt-capable offline writes.
- Add storage-neutral, status-first bounded-receipt reconciliation for
  conditional Vertex Put, exact Vertex Delete, exact Edge Delete, and
  contribution-keyed Edge Add. Persist
  each operation ID, one-item logical group, endpoint NodeID/generation, policy
  evidence, explicit Add contribution ID, and exact original result; retry a mutation only after
  `NOT_YET_OBSERVED` plus exact same-endpoint continuity proof.
- Preserve lookup failures as unresolved retry state and terminalize
  `NO_LONGER_PROVABLE`, changed continuity, exhausted mutation attempts, and
  max age as explicit `outcomeUnknown` work. Never infer `false`, zero, or
  success, and reject generic replay of receipt dead letters.
- Advance strict outbox and operation codecs for receipt evidence/results while
  retaining strict legacy readers. Preserve the new payloads through the
  storage-neutral reference store and migrate SQLite schema v1/v2/v3 payloads
  and reservations atomically into schema v4. Keep legacy Add records quarantined
  as `unsupported_add`; only the distinct receipt Add intent is sendable.
- Persist a monotone pre-dispatch marker before receipt mutations. Refresh an
  aged provisional ID and group only when durable evidence proves no send
  could have started; treat missing legacy markers as possibly dispatched.
- Raise the minimum diagnostic-code capacity to 28 UTF-8 bytes so every
  receipt reconciliation terminal code remains durably representable.
- Preserve signed zero, infinities, and semantic NaN in retained Edge Add
  receipt results without admitting non-finite mutation inputs.

## 0.3.0

- Add a production `LanternClientIdentitySource` for the typed identity-only
  stream in hosted `lantern_client 0.3.0`. The explicit foreground consumer
  bootstraps Unknown residents, revalidates bounded plural batches, applies
  contiguous per-origin chunks, and fails closed on gaps, changed responders,
  cancellation, and logout.
- Retain bounded key-only Unknown residents and a durable change epoch across
  checkpoint reset and fresh-process snapshot restore (schema v7). Reject late
  remote Get results and error fallbacks after CDC invalidation; revalidate
  resident keys through bounded plural reads without starting a CDC stream.

## 0.2.0

- Allow asynchronous `OfflineStoreTransaction` methods through `FutureOr`, and
  await the public port throughout Repository and reusable conformance code.
- Add atomic identity invalidation and per-origin CDC cursor/chunk persistence,
  with uint64-safe cursors and reference snapshot schema v6. Checkpoint reset
  clears confirmed cache while preserving pending intent; network subscription
  remains a separate capability.
- Expand reusable Store conformance across lease CAS/concurrent claimers,
  generation isolation, notification ordering/cleanup, capacity/LRU, and
  same-limit reopen; run canonical lease recovery in a fresh Dart VM and add
  checked resource/mobile exact-revision evidence gates. Committed-response
  loss now runs through a loopback response-dropping proxy after the real
  Connect server commits, before the SDK adapter observes a response.
- Preserve deadline-exceeded as a typed offline remote failure, expose the
  shared online-client failure mapper with retry-exhausted unwrapping, and add
  exact Connect mapping, token-rotation, cancellation, and retry-ownership
  evidence.
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
- Prepare the independent pub.dev package with a hosted `lantern_client`
  constraint and a storage-neutral publish archive.

## 0.1.0 (unpublished development baseline)

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
