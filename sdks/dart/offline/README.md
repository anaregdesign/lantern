# lantern_client_offline

Experimental, storage-neutral offline Repository support for
[`lantern_client`](https://pub.dev/packages/lantern_client). It provides a strict
versioned cache/outbox codec, a deterministic non-production in-memory reference
store, latency-compensated Put writes, and explicit foreground replay.

It is pure Dart and deliberately does **not** bundle SQLite, Flutter,
connectivity, secure storage, state management, scheduling, or encryption. An
application supplies a partitioned transactional `OfflineStore`, owns at-rest
encryption and logout identity policy, and calls `drain` or `resume` explicitly.
The package never persists credentials and never promises delivery while an app
is suspended or killed.

```dart
final repository = OfflineLanternRepository(
  store: InMemoryOfflineStore(),
  remote: LanternClientOfflineRemote(client),
);

final write = await repository.putVertex(
  partitionId: 'signed-in-user',
  input: VertexInput(
    key: 'profile:42',
    value: VertexValue.string('Ada'),
    expiresIn: const Duration(minutes: 30),
  ),
);
final statuses = write.statuses.listen((status) {
  // locallyCommitted -> sending -> confirmed, deadLetter, or expired
});
await repository.probeAndDrain('signed-in-user');
await statuses.cancel();

// This aggregate is durable and can be reconstructed after process restart.
final operation = repository.watchWrite(
  'signed-in-user',
  write.operationId,
);
```

An offline write Future means the local transaction has committed. Watch its
handle for remote confirmation, retry, expiry, or dead-letter status. Only
unconditional `PutVertex` and `PutEdge` are admitted. Add, conditional puts,
and Deletes intentionally have no durable API in the first release.

Per-item handle streams are process-local conveniences. `getWriteStatus` and
`watchWrite` read a content-free durable operation aggregate, preserve mixed
plural progress, and remain reconstructible after a new repository process.
Terminal operation metadata has explicit retention and capacity limits.

`putVertices` and `putEdges` atomically enqueue every item in one logical
operation with stable item indexes. Relative TTL is resolved to one absolute
instant at enqueue and is never rebased during replay.

Snapshots written by the earlier experimental Add implementation remain
readable for migration only. Snapshot schema v3 converts every legacy Add item
to an inspectable terminal dead letter with diagnostic `unsupported_add`,
without incrementing its attempt count, overlaying it on reads, or invoking the
remote. `retryDeadLetter` rejects that item with
`OfflineUnsupportedOperationException`; applications may inspect it through
their authorization callback and then retain or delete it. Durable Add can be
considered again only after #1115 supplies server-authoritative operation
receipts and the offline package has conformance and response-loss evidence for
that receipt contract.

`readVertex`/`readEdge` expose cache-only, cache-first, and server-only policies.
`watchVertex`/`watchEdge` emit the cache immediately, revalidate once against
Lantern, coalesce identical snapshots, and then follow local store changes.
Snapshots distinguish fresh, stale, missing, expired, and unknown states and
carry `hasPendingWrites` for exact Put overlays.
No stale policy serves a value at or after its Lantern expiration.
Distinct remote reads, queued reads, active watchers, per-partition watchers,
and watcher-owning partitions all have explicit bounds. Queued cancellation
does not consume a remote permit, and lifecycle cancellation closes a watch
without surfacing a cancellation error as application failure. Same-key reads
share one remote flight while retaining per-caller cancellation; the underlying
call is canceled only after its final waiter leaves. Watches subscribe to local
changes before their initial snapshot and reconcile any mutation that arrives
during that handoff.

Replay is never inferred from network type. Call `drain`, `start`, or `resume`
from explicit foreground work, or `probeAndDrain` to require a successful real
Lantern health probe first. Use `listPending`, `listDeadLetters`,
`inspectDeadLetter`, `retryDeadLetter`, and `deleteDeadLetter` for recovery UI;
sensitive intent inspection requires an application authorization callback.

`OfflineConfig` makes cache freshness, negative-cache TTL, replay attempts and
age, dead-letter/operation retention, lease duration/renewal, read/replay
concurrency, queue/watch limits, and jitter explicit. `OfflineStoreLimits`
bounds global and per-partition cache, outbox, and operation-metadata bytes and
record counts. Confirmed cache and retained terminal operation entries may be
evicted under pressure; live outbox and non-terminal operation records are
never discarded to admit a new write.

`InMemoryOfflineStore` is useful for deterministic tests and examples only; it
does not survive process termination. Production adapters must preserve the
`OfflineStore` transaction, generation, byte-accounting, lease, and canonical
codec contracts. Adapter packages can invoke `runStoreConformanceSuite` from
their own tests. `exportSnapshot` and `InMemoryOfflineStore.fromSnapshot` exist
only for deterministic fresh-process conformance tests; snapshot schema v3
persists operation aggregates, transactionally migrates active v1 metadata,
and quarantines legacy Add records from v1/v2 snapshots. They are not a
persistence adapter. The checked-in
`tool/performance_probe.dart` records content-free p50/p95/p99, RSS, replay,
enqueue, and snapshot-size evidence against conservative bounds. See the ADR at
`docs/decisions/0002-dart-offline-repository-contract.md`.

## Security

Every operation requires an application-defined non-empty partition ID.
`wipePartition` transactionally removes cache, outbox, dead letters, and leases
and increments the partition generation so late replay responses are discarded.
The package does not receive storage encryption keys and does not emit keys,
values, contribution IDs, tokens, or partition identifiers in diagnostics.

The maintained Flutter example shows cached/pending Put state, explicit
probe/replay, lifecycle cancellation/resume, and authorized dead-letter
controls. Its in-memory store is still non-durable; a production application
must inject a transactional encrypted store and call `wipePartition` before a
different user can open the same application session.
