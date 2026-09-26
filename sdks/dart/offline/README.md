# lantern_client_offline

Experimental, storage-neutral offline Repository support for
[`lantern_client`](https://pub.dev/packages/lantern_client). It provides a strict
versioned cache/outbox codec, a deterministic non-production in-memory reference
store, latency-compensated Put writes, receipt-reconciled mutations, and
explicit foreground replay.

The initial independent `0.2.0` release under
[#1162](https://github.com/anaregdesign/lantern/issues/1162) used an exact-code
physical Android/iOS matrix and a one-time interactive OAuth publication.
The published `0.3.0` identity CDC bridge is tracked under
[#1314](https://github.com/anaregdesign/lantern/issues/1314) and depends on
hosted `lantern_client 0.3.0`. The `0.4.0` receipt release candidate under
[#1398](https://github.com/anaregdesign/lantern/issues/1398) and
[#1115](https://github.com/anaregdesign/lantern/issues/1115) requires hosted
`lantern_client ^0.3.1`. The maintained Flutter example and unpublished SQLite
adapter use local path overrides for development. `0.4.0` remains release
preparation, not a published or physically qualified package; the private
pub.dev offline OIDC binding also requires package-admin verification.

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
handle for remote confirmation, retry, expiry, dead-letter, or
`outcomeUnknown` status. Unconditional `PutVertex` and `PutEdge` retain their
idempotent replay path. `putVertexIfAbsent`/`putVerticesIfAbsent`,
`deleteVertex`/`deleteVertices`, and `deleteEdge`/`deleteEdges` use bounded
server receipts and retain each exact original Put/Delete result.
`addEdge`/`addEdges` require explicit, distinct nonzero 24-byte contribution
IDs, persist them with receipt evidence, and retain each exact original
effective weight, including signed infinity and semantic NaN if returned by
Lantern. Add inputs remain finite. Capped prefix Delete remains outside the
durable API.

Per-item handle streams are process-local conveniences. `getWriteStatus` and
`watchWrite` read a content-free durable operation aggregate, preserve mixed
plural progress, and remain reconstructible after a new repository process.
Terminal operation metadata has explicit retention and capacity limits.
Caller-supplied operation IDs and generated record IDs are collision checked
inside the enqueue transaction. Collisions fail with a typed error and never
replace retained work; generated IDs use a bounded retry budget.

Plural writes atomically enqueue every item in one logical local operation
with stable item indexes. Relative TTL is resolved to one absolute instant at
enqueue and is never rebased during replay. Each receipt-bearing item persists
its operation ID, one-item logical receipt group, endpoint NodeID/generation,
policy fingerprint, and exact `itemIndex=0`/`itemCount=1` receipt topology
before its first mutation send.

Receipt replay checks status before every mutation send, including after an
ambiguous response, lease recovery, or process restart. A freshly enqueued
receipt also retains a never-dispatched marker. If the server clock shows its
provisional ID nearing the five-minute admission limit, replay may replace
that ID and group under a live claim *before* status lookup, but only while the
durable marker proves no send could have begun. The marker becomes
`mayHaveDispatched` before the first mutation RPC; missing markers in older
records mean it may already have been sent and can never authorize rekeying.
The freshness check includes elapsed time since the capability request began,
including response latency; a delayed preparation cannot make an old ID safe.
A retained `CONFIRMED` receipt completes the local aggregate with the exact
original result. `NOT_YET_OBSERVED` permits one send only after mutation
support, endpoint continuity, deployment epoch, retention, caps, and policy
fingerprint still match the persisted evidence.
A lookup failure remains retryable and unresolved; `NO_LONGER_PROVABLE`,
changed continuity, exhausted attempts, or local max age becomes terminal
`outcomeUnknown`, never `false`, zero, or success. Receipt dead letters cannot
be generically retried. Token rotation alone does not change endpoint
continuity: rotate credentials, then call `resume` to perform status-first
reconciliation.

CI runs the real-wire receipt matrix against a separate authenticated
receipt-WAL server while retaining the graph-only fixture for older Put tests.
The receipt endpoint and ephemeral token are required for that CI run; the
SQLite adapter's separate `--receipt` crash gate proves process-kill recovery
with a file-backed store.

Snapshots written by the earlier experimental Add implementation remain
readable for migration only. Those legacy `OfflineAddEdgeIntent` records are
distinct from the current receipt-backed `OfflineReceiptAddEdgeIntent`.
Opening snapshot schema v1, v2, or v3 converts
every legacy Add item to an inspectable terminal dead letter with diagnostic
`unsupported_add`, without incrementing its attempt count, overlaying it on
reads, or invoking the remote. Schema v4 fails closed on Add. Schema v5 accepts
only the exact terminal `unsupported_add` shape produced by that migration;
every live or noncanonical Add still fails closed. `retryDeadLetter`
rejects a migrated item with
`OfflineUnsupportedOperationException`; applications may inspect it through
their authorization callback and then retain or delete it. Current Add is
admitted only through its separate receipt-bearing API and never upgrades,
replays, or aliases a legacy persisted Add record.

`readVertex`/`readEdge` expose cache-only, cache-first, and server-only policies.
After a checkpoint reset, a bounded resident identity stays Unknown until the
explicit plural recovery batch revalidates it. An ordinary `serverOnly` Get
does not clear this marker or return its unverified response; a pending Put
overlay remains visible. Accepted CDC chunks advance a durable epoch, so an
ordinary Get already in flight cannot publish a late confirmed result.
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
Public read, list, status, watch, enqueue, and replay entry points lazily sweep
expired work transactionally, so TTL expiry and capacity reclamation do not
depend on a network drain. Dead-letter retention begins at the dead-letter
transition rather than at original enqueue. Adapter-owned deadline indexes are
scoped by partition, operation, and entity, so each observation inspects at most
`maxSweepRecordsPerObservation` due records without walking live FIFO entries.

The Repository serializes replay entry points per partition and applies one
repository-wide/per-partition send limit to every entry point. A wrapped online
client's nested retry policy is suppressed. Each `attemptCount` increment is one
completed durable adapter attempt that permits at most one singular RPC; a
credential-provider or cancellation failure can finish before any wire send.
There are no hidden nested transport attempts.
Receipt-less Put replay consumes the online SDK's server-authoritative
`PutOutcome`. `appliedAndLive` confirms only while the resolved expiration is
live before send, at response observation, and at the local commit; a clock
rollback cannot revive an already expired sample. `expired` terminalizes and
invalidates older confirmed cache state. `conditionNotMet` and `superseded`
become inspectable dead letters because the attempted value is not the
authoritative server value. An observed outcome consumes one attempt; local
pre-send expiration consumes none.
Receipt-bearing operations instead retain their original typed result in the
durable operation aggregate. Their outbox codec includes immutable receipt
identity and continuity evidence; confirmed receipt replay invalidates any
possibly stale cache entry rather than claiming the receipt describes current
graph state after later mutations.
`Unauthenticated` sets a durable partition pause without burning an attempt;
the partition auth epoch also cancels same-batch sibling token acquisition
before another send can start.
`drain`, `start`, and `probeAndDrain` then fail with
`OfflineAuthPausedException` without acquiring a token or sending. Rotate the
credential and call `resume` explicitly to clear the pause.
Retry backoff is stored as `nextAttemptAt`; the Repository never owns one Timer
per record and does not sleep between foreground drain invocations.

`OfflineConfig` makes cache freshness, negative-cache TTL, replay attempts and
age, dead-letter/operation retention, lease duration/renewal, read/replay
concurrency, the active partition-runtime cap, queue/watch/status-controller
limits, bounded sweep work, and jitter explicit. Idle partition runtimes are
released, while concurrent unique partitions fail with
`OfflineCapacityException` before the process-local map can exceed that cap.
`OfflineStoreLimits` bounds global and per-partition cache, outbox, and
operation-metadata bytes and record counts, plus per-record lease-owner and
diagnostic-code bytes. Receipt reconciliation requires at least 28 diagnostic
UTF-8 bytes; smaller custom limits fail closed at configuration time. Outbox
admission charges each immutable payload for its full bounded lifecycle
envelope, so claim, retry, and dead-letter metadata cannot make an accepted
snapshot exceed the same configured byte limits.
Confirmed cache and retained terminal operation entries may be evicted under
pressure; live outbox and non-terminal operation records are never discarded
to admit a new write.

`InMemoryOfflineStore` is useful for deterministic tests and examples only; it
does not survive process termination. Production adapters must preserve the
`OfflineStore` transaction, generation, byte-accounting, lease, and canonical
codec contracts. Every changed partition—and every durably mutated partition
such as an LRU-only `touchCache`—must form a complete cache/outbox/operation
graph before commit. Invalid graphs fail atomically with
`OfflineDurableGraphException`, publish no state or change notification, and
must not strand capacity. Cache keys must match their embedded Vertex or Edge,
negative markers must not end before validation, and every retained outbox item
must have a request-index-aligned aggregate status whose lifecycle matches.
Claim/recovery and aggregate status transitions therefore commit together;
`wipePartition` is the explicit barrier that cancels earlier enqueue
obligations in the same transaction.

Adapter packages must invoke `runStoreConformanceSuite` from their own tests and
provide its required `reopen` callback. That callback crosses the adapter's real
close/reopen persistence boundary under identical limits; every accepted
commit must survive it. The suite includes concurrent claimers, lease renew and
release CAS failures, generation barriers, monotone no-gap change delivery,
notification-controller cleanup, LRU-only cache pressure, and atomic
outbox/operation capacity rejection. Configure the adapter's test limits below
the default probe bounds or raise `maxCapacityProbeRecords` and
`maxNotificationControllerProbe` explicitly. `exportSnapshot` and
`InMemoryOfflineStore.fromSnapshot` exist
only for deterministic fresh-process conformance tests; snapshot schema v7
persists operation aggregates, exact dead-letter transition time, durable
auth pause, per-origin CDC chunk progress, the change epoch, and key-only
Unknown residents. Schema v6 restores with an empty resident queue and epoch
zero; schemas v1–v5 restore with empty CDC state. Restore transactionally reconstructs active v1 metadata, recovers
auth pause from v1-v4 durable metadata, quarantines legacy Add records only
from v1-v3, reopens only that exact terminal quarantine in v5–v7, migrates v1-v3
outbox retention metadata conservatively, and fails
closed when cache, outbox, operation, ordinal, generation, lease, or state
relationships contradict each other. A child Dart VM restores canonical bytes,
recovers an expired lease, and proves stable IDs/order/TTL before re-export; this
is a process-neutral codec/state-machine test, not an fsync or OS-kill claim.
Transaction objects are sealed after both commit and rollback. The reference
snapshot is not a persistence adapter. The
checked-in
`tool/performance_probe.dart` records content-free p50/p95/p99, RSS,
enqueue/read/watch/replay/recovery/dispose latency, exact send and
terminal-state counts,
bounded concurrent/outstanding sends, decoded status objects, remaining
claims/leases, controller/watch lifecycle cycles, and snapshot-size evidence
against conservative checked-in bounds. Lease-renewal Timer cleanup is a
blocking Repository test. See the ADR at
`docs/decisions/0002-dart-offline-repository-contract.md`.

## Security

Every operation requires an application-defined non-empty partition ID.
That `partitionId` is only a local persistence namespace: it is never sent on
the Lantern wire and is not a server tenant, identity, or authorization
boundary. Tenant isolation belongs to the application, gateway, credential
scope, and storage/security domain; distinct partition IDs alone do not create
that isolation. `wipePartition` first blocks new partition work, cancels and awaits
owned reads, sends, probes, leases, and watchers, then transactionally removes
cache, outbox, operations, dead letters, and leases while incrementing the
generation. Rotate to another user's credential only after wipe completes;
old-partition intent can therefore never acquire the new token. A mutation the
server already accepted cannot be recalled: wipe guarantees local isolation
and no further sends, not remote rollback. `dispose` similarly completes only
after every repository-owned call, queue, timer, lease, and watcher quiesces.
The package does not receive storage encryption keys and does not emit keys,
values, contribution IDs, tokens, or partition identifiers in diagnostics.

The maintained Flutter example shows cached/pending Put state, explicit
probe/replay, lifecycle cancellation/resume, and authorized dead-letter
controls. It now uses the opt-in sibling `lantern_client_offline_sqlite` adapter
through `sqflite`. Applications own protected storage, any additional encryption,
and backup policy, and must call `wipePartition` before a different user can open
the same application session. The core retains no platform dependencies.

Transaction operations return `FutureOr<T>` so adapters can use asynchronous
database APIs. Await every operation inside the transaction callback. The
`changeCursor`, `applyChangeChunk`, and `resetChangeCursor` atomically persist
identity-only invalidation and per-origin progress. Accepted partial and final
chunks advance a durable partition change epoch. A checkpoint reset removes
confirmed values but retains bounded resident identities as Unknown until
`revalidateResidentBatch` finishes plural Get calls and their epoch-checked
cache commits. Late ordinary Get results and server-first failure fallbacks
cannot restore a record invalidated during the read. Adapter tests run
`runChangeStoreConformanceSuite` across a real reopen boundary.

`consumeIdentityChanges(partitionId, source: ...)` is an explicit foreground CDC
session. The application injects an `OfflineIdentitySource` that opens an
identity-only Subscribe stream and performs plural reads against that same
responder. The source must pin a real responder for the entire checkpoint and
revalidation, propagate stream pause/resume, map retention and slow-subscriber
gaps to `OfflineChangeGapException`, and acquire credentials at call time.
The core owns a single session per partition and cancels it on logout or
disposal. It resumes each origin at the durable last-applied sequence plus one,
checks sequence, chunk, operation, and item-index continuity, and advances the
cursor only with a final chunk. A gap hides confirmed cache as durable Unknown
and permits one checkpoint retry per invocation. The stream is read one frame
at a time, so recovery does not create an unbounded Dart event queue. The
server's bounded stream buffer may still gap during a slow revalidation; that
also triggers Unknown recovery. No network subscription starts at Repository
construction, and the application remains responsible for foreground timing.

The production bridge is `LanternClientIdentitySource(pinnedClient)`. Pass the
same application-owned client to `LanternClientOfflineRemote` for ordinary
reads and writes, then explicitly run the foreground CDC session:

```dart
final cancellation = LanternCancellationToken();
final source = LanternClientIdentitySource(pinnedClient);
await repository.consumeIdentityChanges(
  partitionId,
  source: source,
  cancellation: cancellation,
);
```

Cancel that token when the app leaves the foreground or the account logs out;
the repository also cancels an active session on partition wipe or disposal.
The client must route its stream, status checks, and plural reads to one real
responder for the session. The adapter checks the node ID around plural reads,
but the Subscribe frames do not expose the responder ID; an ordinary
load-balancing endpoint cannot be validated as pinned by this adapter.
Application configuration must guarantee that routing property. The client
acquires its configured credentials at each RPC call.

The `0.3.0` CDC bridge requires hosted `lantern_client 0.3.0` with
`subscribeIdentity`; the `0.4.0` receipt candidate requires hosted
`lantern_client ^0.3.1` for receipt APIs. The initial offline `0.2.0`
package stays on its published parent constraint.
