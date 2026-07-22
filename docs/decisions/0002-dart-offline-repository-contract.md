# 0002: Dart offline Repository and package contract

- Status: Accepted
- Date: 2026-07-12
- Accepted: 2026-07-19
- Issue: #1021

## Context

`lantern_client` v0.1 is an online, pure-Dart client. Flutter applications may
need locally cached reads or durable mutation replay, but those features cross
boundaries the online client intentionally does not own: database choice,
Keychain/Keystore integration, signed-in tenant partitioning, OS background
work, schema migration, eviction, and product-specific conflict UX.

Lantern also has contracts that make a generic queue unsafe unless it is
designed before the first attempt. Relative TTL becomes one absolute instant;
Add is idempotent only with the same persisted 24-byte contribution ID;
PutIfAbsent and Delete counts change after an ambiguously committed replay;
and a capped prefix Delete can remove a second page if replayed.

Flutter's
[offline-first guidance](https://docs.flutter.dev/app-architecture/design-patterns/offline-first)
places local/remote coordination in an application Repository. The core SDK
must not imply that network-type reachability, lifecycle callbacks, or a token
stored beside a mutation can make delivery durable.

## Decision

Keep `lantern_client` free of an implicit read cache and mutation outbox. Ship
an official, opt-in `lantern_client_offline` package that provides the
storage-neutral codec, state machine, cache/outbox engine, and replay
coordinator defined below. Applications compose that package as their
Repository and still own identity, encryption policy, and OS scheduling.

The SDK supplies the online primitives that a Repository needs—immutable exact
values, absolute expiration, caller-supplied contribution IDs, typed errors,
cancellation, and per-call token acquisition—but does not depend on SQLite,
secure storage, connectivity, Flutter, or a state-management library.

### Reference-core implementation

The accepted contract now has an experimental pure-Dart reference core at
`sdks/dart/offline` (`lantern_client_offline`, `publish_to: none`). It supplies
the v1 fail-closed JSON codec and deterministic fixtures, immutable public
ports/types, a non-production `InMemoryOfflineStore`, confirmed-cache policy
engine, latency-compensated Put/Add overlays, and explicit bounded replay over
an injected `OfflineRemote`. It is intentionally not a persistence or
encryption implementation.

The core admits only unconditional `PutVertex`, `PutEdge`, and stable-ID
`AddEdge`; it keeps one outbox record per item plus one content-free durable
aggregate per logical operation, resolves relative TTL once, and rejects late
lease/generation responses after a partition wipe. Calling a
write method means the local transaction committed—not that remote delivery was
confirmed. Replay is foreground-only through explicit `drain`, `start`, or
`resume` calls. The reference store exists for tests and conformance examples;
production adapters must preserve its serializable transaction, defensive-byte,
partition-generation, durable FIFO ordinal, capacity, renewable lease,
operation/dead-letter retention, and codec semantics. They can execute
`runStoreConformanceSuite` in their own test runner against an empty adapter.

The package has no storage key API and makes no encryption claim. Applications
must provide at-rest encryption and key lifecycle policy in their store adapter.
Schema open/migration failures must fail closed as `OfflineSchemaException`;
malformed v1 record bytes or v2 reference snapshots must fail closed as
`OfflineCodecException` and must never be sent to `OfflineRemote`. Reference
snapshot schema v2 adds operation aggregates; opening schema v1 reconstructs
active operation metadata transactionally and marks outcomes no longer present
in the legacy outbox as `outcomeUnknown`. Cache and outbox capacity failures use
`OfflineCapacityException`; adapters may evict only confirmed cache records,
never live unconfirmed outbox records.

`lantern_client_offline` takes an injected transactional `OfflineStore`; it
does not bundle a database. The storage-neutral contract graduates only after
its deterministic host gates are paired with the maintained example on at least
one physical Android or iOS device. A separately versioned production adapter
may be proposed later after passing the reusable conformance suite; no default
adapter or package publication is implied by core graduation. Persistence built
directly into `lantern_client` remains rejected: it would force policy and
platform dependencies on every client and make user isolation an SDK-global
concern.

## Alternatives

| Boundary | Decision | Reason |
| --- | --- | --- |
| App-owned Repository hooks/examples only | Rejected as the final product boundary | Preserves maximum flexibility but makes every mobile application rebuild the same crash-sensitive sync engine. Examples remain valuable for integration and policy ownership. |
| Official opt-in `lantern_client_offline` with storage adapter | Chosen | Provides Firestore-like offline ergonomics while keeping persistence opt-in and the online SDK pure Dart. Experimental status makes the evidence gate explicit. |
| Persistence inside `lantern_client` | Rejected | Forces a database and platform policy on all callers, couples logout/encryption to transport, and risks implicit unsafe replay. |

## Read-cache contract

Every record belongs to a non-empty application-defined `partitionId` that
identifies the signed-in user and tenant. A canonical storage key is:

```
lantern-cache / schemaVersion / partitionId / entityKind / entityKey
```

`entityKind` is `vertex` or `edge`. A Vertex key is its UTF-8 key bytes. An
Edge key is the collision-free, length-prefixed UTF-8 pair `(tail, head)`; it
must not be a delimiter-joined string.

Schema v1 preserves every public kind without numeric guessing:

- value discriminator: float64, float32, int32, int64, uint32, uint64, bool,
  string, bytes, timestamp, duration, nil, or unset;
- signed/unsigned 64-bit values encoded as canonical decimal strings;
- float32 stored as its exact IEEE-754 32-bit payload and float64 as 64-bit;
- bytes as raw bytes or canonical base64, never a lossy text conversion;
- timestamp and absolute expiration as UTC microseconds plus a schema tag;
- duration as signed microseconds within the Protobuf duration domain;
- Edge stores tail, head, exact float32 weight, and absolute expiration.

The record also carries `validatedAt`, optional ETag/version metadata supplied
by an application gateway, and its last access time for eviction. A codec must
fail closed on an unknown schema/kind instead of converting it to unset or nil.

Reads return an immutable snapshot with an explicit state:

```dart
enum OfflineReadState { fresh, stale, missing, expired, unknown }

final class OfflineSnapshot<T> {
  OfflineReadState state;
  OfflineReadSource? source;
  T? value;
  DateTime? validatedAt;
  bool hasPendingWrites;
  bool isEstimate;
}
```

Fields above are abbreviated API-sketch notation; the implemented type is
immutable. Cache-first watches emit local state immediately, attempt one server
revalidation, coalesce identical snapshots, and remain subscribed to local
changes even when that revalidation fails.

- `Fresh` requires `now < expiration` (when present) and application freshness
  age within its configured bound.
- `Stale` is returned only by an explicitly stale-allowed Repository method
  and still requires `now < expiration`. Stale allowance can never extend a
  Lantern expiration.
- At or after expiration, the Repository deletes/quarantines the payload and
  returns `Expired`, never its value.
- A confirmed remote NotFound/Delete may write a bounded negative `Missing`
  marker. Unknown transport failure is `Unknown`, not `Missing`.
- HA replicas can temporarily disagree. Without a server invalidation/version
  feed, remote Deletes are observed on the next successful revalidation; the
  application chooses a finite maximum stale age based on that risk.

Disk bytes, record count, per-partition bytes, and in-memory decoded objects
all have explicit caps. Writes apply backpressure or evict least-recently-used
read records; they never evict unconfirmed outbox records to make room. A full
outbox rejects new enqueue with a typed capacity error.

Logout transactionally deletes the correct partition's cache, outbox,
dead-letter records, leases, and encryption-key reference before another user
can open it. Applications own at-rest encryption and Keychain/Keystore key
management. The SDK never receives the storage encryption key.

## Mutation-outbox contract

The durable record is committed before the first network attempt. One record
represents one mutation item; a plural application call commits one record per
item in a single enqueue transaction. Records from that call share an
`operationId` and have stable, zero-based `itemIndex` values. This makes
per-item progress durable without inventing a terminal state for a batch whose
items have mixed confirmation or expiration outcomes.

The same transaction also creates or updates a content-free operation
aggregate. `getWriteStatus` and `watchWrite` reconstruct its latest per-item
states after process restart; the process-local handle stream is only a
convenience. Enqueue, claim, confirmation, retry, authentication pause,
dead-letter, expiry, and lease recovery update outbox and aggregate metadata
atomically. Terminal aggregates have bounded retention and may be evicted;
non-terminal aggregates may not be evicted to admit new work.

The storage model contains:

```dart
final class OutboxRecord {
  String recordId;              // stable application UUID, not a token
  String operationId;           // stable logical-call UUID for batch grouping
  int itemIndex;
  int schemaVersion;
  String partitionId;
  OfflineIntent intent;         // exact, versioned serialized single-item intent
  DateTime enqueuedAt;
  DateTime? absoluteExpiration; // relative TTL resolved once at enqueue
  String orderingKey;
  int attemptCount;
  DateTime? nextAttemptAt;
  OutboxState state;
  String? leaseOwner;
  DateTime? leaseUntil;
  List<int>? contributionId;    // required exact 24 bytes for Add
  String? diagnosticCode;       // credential/content-free
}
```

The abbreviated typed operation union initially admits only single-item Put
and Add intents:

```dart
sealed class OfflineIntent {}
final class PutVertexIntent extends OfflineIntent {}
final class PutEdgeIntent extends OfflineIntent {}
final class AddEdgeIntent extends OfflineIntent {} // contributionId is required
```

The payload stores the exact input value, including its wire kind and float
payload, plus the resolved absolute expiration; it is not a closure or a
generated request object. Enqueue samples the clock once per logical call and
resolves each item's relative TTL against that instant before committing all
records. Add contribution IDs are generated and persisted in the same
transaction as the intent. A process restart, replay-batch reconstruction,
and every retry reuse identical bytes. If `now >= absoluteExpiration` before
confirmation, that item becomes `expired` and is never rebased.

Put is safe to replay to its final state. Add is replayable only when every
claimed record has a persisted valid contribution ID. The coordinator may batch compatible records as a future transport
optimization, but the accepted baseline deliberately sends one item per RPC and
commits each record's resulting state transactionally after its response. A
1,001-item real-wire restart scenario proves that already-confirmed items are
not replayed while the remaining items resume safely. A crash therefore
does not rebuild already-confirmed items into a later batch. If an Add response
is lost, replaying the same IDs is the reconciliation; if the server can no
longer retain that contribution because the intent itself expired, the
Repository expires the item rather than minting a new ID.

PutIfAbsent, singular/plural Delete result semantics, and capped prefix Delete
are excluded from the generic outbox. An application-specific workflow may
place one in `ambiguous` only when it also provides a domain reconciliation
screen/API. It can never convert an unknown outcome into `false`, zero, or
success, and no helper loops destructive prefix calls.

Per-key ordering is FIFO by `orderingKey`; a Vertex uses its key and an Edge
uses its collision-free length-prefixed `(tail, head)` identity. A logical
plural call does not create a global ordering barrier: independent keys may run
concurrently up to an application cap, while records for the same key remain
ordered by enqueue sequence. One storage transaction claims a bounded set of
compatible records with leases. After a crash, lease expiry returns `sending`
work to `enqueued`; stable IDs and per-record states make the next attempt safe.
While an RPC remains active, the coordinator renews its lease with an
owner/generation/state CAS. A lost lease makes the late response ineligible for
local confirmation.

Distinct remote reads have repository-wide and per-partition active and queued
caps. Snapshot watchers have repository-wide, per-partition, and active
partition caps. These bounds apply only to remote legs and active subscriptions;
cache-only reads remain local, and queued cancellation cannot start a remote
request.

The offline package exposes a control surface that lets the application make
unsafe and failed work inspectable without exposing payloads through logs.
Names are illustrative, but the typed capabilities are required:

```dart
abstract interface class OfflineRepositoryControl {
  Future<List<DeadLetterSummary>> listDeadLetters(String partitionId);
  Future<void> deleteDeadLetter(String partitionId, String recordId);
  Future<void> retryDeadLetter(String partitionId, String recordId);
  Future<void> resolveAmbiguous(
    String partitionId,
    String recordId,
    AmbiguousResolution resolution,
  );
  Future<void> wipePartition(String partitionId);
}
```

`DeadLetterSummary` exposes only record ID, operation category, state, age,
attempt count, and diagnostic code by default. Reading the sensitive intent is
a separate application-authorized action. `resolveAmbiguous` exists only for
an application-specific unsafe workflow with domain reconciliation; the
generic Put/Add outbox neither accepts unsafe intents nor fabricates an
outcome.

## State machine

```mermaid
stateDiagram-v2
    [*] --> Enqueued: durable enqueue transaction
    Enqueued --> Sending: bounded claim + lease
    Sending --> Confirmed: response and progress committed
    Sending --> Enqueued: retryable failure / lease expiry
    Sending --> Ambiguous: app-specific unsafe extension has unknown outcome
    Enqueued --> Expired: absolute expiration reached
    Sending --> Expired: absolute expiration reached before safe replay
    Enqueued --> DeadLetter: poison / migration / attempt policy
    Sending --> DeadLetter: permanent typed failure
    Ambiguous --> Confirmed: app reconciliation proves commit
    Ambiguous --> DeadLetter: app chooses no further mutation
    DeadLetter --> Enqueued: explicit inspected retry only
    Confirmed --> [*]
    Expired --> [*]
```

Backoff is full-jitter, bounded, and cancelable. `unavailable` is eligible;
authentication failure pauses the partition for fresh credentials rather than
burning attempts. Resource exhaustion follows explicit app policy. Permanent
invalid argument and unmigratable/corrupt payloads go to dead-letter. Capacity,
maximum attempts/age, lease duration, concurrency, and dead-letter retention
are required constructor configuration, not hidden constants.

Replay starts from an explicit foreground action/resume or after an actual
successful Lantern probe. A network-type plugin may reduce futile attempts but
is never proof of server reachability. iOS suspension and Android Doze make any
background replay best-effort; there is no “delivered after kill” guarantee
without application-owned OS background task or push infrastructure.

## Security and observability threat model

| Threat/failure | Required control |
| --- | --- |
| Token disclosure from disk | Never serialize tokens; call `TokenProvider` at send time and keep auth metadata out of diagnostics. |
| Cross-user/tenant data leak | Partition every key/record; atomically wipe on logout/user switch; do not reuse a partition identifier. |
| Device backup or file extraction | Application chooses encryption and platform key storage; document backup exclusion/rotation policy. |
| Corrupt/tampered record | Authenticated storage where required, strict schema/length/range checks, quarantine/dead-letter without sending. |
| Crash between network commit and local confirmation | Put replays; Add reuses persisted IDs; unsafe conditional/Delete operations remain explicit ambiguous. |
| Crash during migration | Copy-on-write/versioned migration with transaction marker; old schema remains readable until commit or is quarantined. |
| Disk exhaustion/queue flood | Per-partition/global byte and count caps, backpressure, bounded claims, read-cache eviction before outbox loss. |
| Sensitive telemetry | Default metrics expose category, state, age bucket, attempts, counts, and error code only—never keys, values, graph content, IDs, or credentials. |
| Poison item retry loop | Maximum attempts/age, permanent-error classification, application-controlled dead-letter inspect/delete/retry. |
| Logout during send | Cancel partition workers, revoke lease, wipe transactionally, and discard late responses using partition generation/epoch. |

## Golden scenarios

1. **Add crash/replay:** persist an Add with fixed 24-byte IDs, commit remotely,
   lose the response, restart, replay identical bytes, and observe one live
   contribution/effective weight.
2. **Partial batch crash:** confirm chunk/items 0–999, crash before the next
   chunk, restart from persisted progress, and never resend an unsafe confirmed
   item. A stable-ID Add may safely replay an uncertain current chunk.
3. **TTL expires offline:** resolve relative TTL at enqueue, advance past the
   absolute instant, and mark expired without network I/O or TTL rebasing.
4. **Auth rotation:** persist no credential, receive Unauthenticated, pause,
   obtain a new token at send time, then confirm the same record/IDs.
5. **Logout/user switch:** cancel active work, wipe user A's partition and key
   reference, open user B, and prove neither cached values nor outbox metadata
   are observable.
6. **Capped Delete:** generic enqueue rejects the operation before storage or
   network I/O. An application-specific unsafe workflow surfaces ambiguous
   after response loss and never automatically sends a second page.
7. **Expired cached read:** a previously fresh value reaches Lantern absolute
   expiration while offline; even stale-allowed read returns Expired and
   destroys/quarantines the payload.
8. **Corruption/migration:** invalid kind/length/schema never becomes a wire
   request; it is quarantined with a content-free diagnostic and recoverable
   dead-letter action.

## Follow-up scopes

Each scope is an independent Issue. Later scopes may use evidence or artifacts
from an earlier scope, but none may add implicit persistence to
`lantern_client`:

1. [#1111](https://github.com/anaregdesign/lantern/issues/1111) publishes
   storage-neutral v1 cache/outbox codec fixtures and cross-process golden
   tests, without adding persistence to `lantern_client`.
2. [#1112](https://github.com/anaregdesign/lantern/issues/1112) implements the
   experimental `lantern_client_offline` package with an injected transactional
   store, partition wipe, capacity limits, and fresh/stale/expired states.
3. [#1113](https://github.com/anaregdesign/lantern/issues/1113) adds a maintained
   Flutter integration with foreground-resume replay and dead-letter controls,
   excluding OS background delivery promises.
4. [#1114](https://github.com/anaregdesign/lantern/issues/1114) combines the
   storage-neutral crash/conformance gates with the maintained example running
   on at least one physical Android or iOS device, then decides whether the
   package contract is ready for separate release planning. Production adapter,
   Keychain/Keystore, and publication work remain separate follow-up scopes.
5. [#1115](https://github.com/anaregdesign/lantern/issues/1115) adds
   server-authoritative operation receipts so mutations whose public result is
   ambiguous after response loss can be reconciled safely.
6. [#1116](https://github.com/anaregdesign/lantern/issues/1116) adds a
   client-facing revision/change stream for bounded invalidation after server
   Deletes, expiration, and HA convergence.
7. [#1162](https://github.com/anaregdesign/lantern/issues/1162) implements a
   separately versioned production SQLite adapter and runs the reusable store
   conformance suite plus physical restart/isolation checks.
8. [#1163](https://github.com/anaregdesign/lantern/issues/1163) owns hosted
   dependency conversion, versioning, pub.dev OIDC, and the independent
   `sdks/dart/offline/vX.Y.Z` release contract.

## Consequences

The online SDK remains deterministic, dependency-light, and honest about what
it can confirm. The official offline package removes repeated sync-engine work
without taking ownership of application identity, encryption, UX, or OS
lifecycle. It cannot silently change TTL, mint an Add ID after enqueue, replay
ambiguous Deletes, or persist credentials; those are compatibility constraints
for every offline-package version.
