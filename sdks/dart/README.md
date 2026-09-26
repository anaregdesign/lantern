# lantern_client

Official pure-Dart package for [Lantern](https://github.com/anaregdesign/lantern),
an in-memory graph key-vertex store with TTL-aware vertices and edges.

The package includes exact vertex values, strict TTL handling, bounded core
CRUD, and the reusable transport foundation used by later query and traversal
facades. Generated RPC request/response types remain private.

## Scope

- Android and iOS are the first-class production targets.
- Dart VM is supported for tests and command-line integration tooling.
- This is a pure Dart package: it has no Flutter runtime dependency, plugin
  declaration, platform channel, or native implementation.
- Flutter Web and desktop support are not guaranteed by v0.1.

The public import is:

```dart
import 'package:lantern_client/lantern_client.dart';

final client = LanternClient.connect(
  Uri.parse('https://lantern.example.com'),
  tokenProvider: () async => session.accessToken,
);
final outcome = await client.putVertex(
  VertexInput(
    key: 'user:42',
    value: VertexValue.string('alice'),
    expiresIn: const Duration(minutes: 30),
  ),
);
if (outcome != PutOutcome.appliedAndLive) {
  throw StateError('user:42 was not live after Put: $outcome');
}
final vertex = await client.getVertex('user:42');
```

`VertexValue` has named factories for every wire oneof kind; bare Dart numbers
are never guessed into a Protobuf numeric kind. `VertexValue.nil()` and
`VertexValue.unset()` remain distinct. `VertexInput` and `EdgeInput` omit wire
expiration when both expiration fields are absent, which means permanent
storage. `expiresIn` must be positive, is mutually exclusive with `expiresAt`,
and is converted to one absolute UTC instant from the client's clock before
chunking. After every chunk succeeds, the same clock is sampled once before
the plural call returns;
`appliedAndLive` is downgraded to `expired` when that absolute expiration is
already past locally. Bounded server outcomes remain authoritative otherwise,
so `conditionNotMet` and `superseded` are never reclassified. Device-clock skew
therefore affects relative TTLs and can only make the client more conservative.

Plural CRUD calls use 1,000-item chunks by default and reject logical batches
above 65,536 items. A later chunk failure throws `BatchException` with the
input-prefix length whose responses were fully observed and validated; despite
the existing `committed` field name, this is not an `appliedAndLive` count.
`addEdge(s)` accumulates weight and is
non-idempotent unless the caller supplies an exact 24-byte `contribId`;
`putEdge(s)` overwrites weight and expiration idempotently.

For `putVerticesIfAbsent`, that count covers only prior chunks whose responses
were observed. The failed chunk may already have committed; its original
per-item outcomes are unknown, and replay performs a new condition evaluation
that may return `conditionNotMet`. Reconcile server state before retrying it.

Every singular Put resolves to a typed `PutOutcome`; plural Vertex and Edge
Puts both resolve to immutable, request-index-aligned result Lists. The server
clock decides `appliedAndLive`, `expired`, `conditionNotMet`, or `superseded`
at application time. An unconditional born-expired Put removes a prior live
item and reports `expired`; `putVertexIfAbsent` checks an existing live value
first and reports `conditionNotMet` without overwriting it. Unknown,
unspecified, or length-misaligned wire outcomes fail closed as
`LanternInternalException`.

## Mobile retries and additive safety

Retries are opt-in and bounded. The default `RetryPolicy()` makes three total
attempts, uses full-jitter exponential backoff from 100 ms with a 2 second
per-delay cap, and retries `unavailable` only. One absolute deadline and one
cancellation token cover both attempts and backoff. There is no connectivity
preflight, endpoint discovery, or background execution guarantee.

```dart
final client = LanternClient.connect(
  Uri.parse('https://lantern.example.com'),
  retryPolicy: const RetryPolicy(),
  idempotentAdds: true,
);
```

Reads and stable Put calls may retry. Add calls retry only when every
contribution has a stable ID. `idempotentAdds: true` fills missing IDs with the
canonical 24-byte format once per in-memory logical call; caller-supplied IDs
always win. Put-if-absent, Delete, capped prefix Delete, streams, and unknown
operations are never replayed because a committed response loss would change
their observable result.

Set `LanternCallOptions(retry: false)` when a higher-level durable coordinator
owns retry accounting. Each RPC is then attempted at most once while retaining
the client's deadline, cancellation, token-provider, and typed failure behavior.
A chunked plural logical call may still issue multiple RPCs.

Automatic IDs do not turn two application calls into one operation and do not
survive process restart. This package does not implement an offline queue. A
contribution ID deduplicates only while the server retains that contribution.
The experimental `lantern_client_offline` package therefore admits Put only;
durable Add remains disabled until its client layer adopts the certified
receipt family.

`addDecayingEdge` expands a geometric curve into at most 16 staggered-TTL
contributions whose initial live sum is exact. With `idempotentAdds` enabled,
the entire expanded call is also safe against an ambiguous response loss.

## Bounded online mutation receipts

Receipt-bearing Vertex Put, exact Vertex Delete, Edge Delete, and
contribution-keyed Edge Add are explicit APIs alongside the existing
receipt-less methods. Existing methods retain their established retry and
ambiguous-result behavior. Receipt-bearing Add requires a distinct explicit,
nonzero 24-byte contribution ID for every item and returns the exact original
effective weight. Finite Add inputs can accumulate to signed infinity, which
remains an authoritative result. The online package does not persist receipt
state.

Fetch capability from the target endpoint, mint one immutable context, persist
its mutation kind plus exact operation/group/endpoint bytes if recovery must
survive process death, and use that same context for the mutation:

```dart
final capability = await client.getReceiptCapability();
if (capability case ReceiptCapabilityEnabled(
  supportedMutations: final supported,
)) {
  if (!supported.contains(ReceiptMutationKind.vertexPut) ||
      !supported.contains(ReceiptMutationKind.vertexDelete)) {
    throw StateError('endpoint does not support Vertex receipts');
  }
  final putContext = client.mintReceiptContext(
    capability: capability,
    mutation: ReceiptMutationKind.vertexPut,
    itemCount: 1,
  );
  final put = await client.putVertexWithReceipt(
    VertexInput(key: 'user:42', value: VertexValue.string('alice')),
    context: putContext,
    ifAbsent: true,
  );
  print('put outcome: ${put.outcome}');

  final deleteContext = client.mintReceiptContext(
    capability: capability,
    mutation: ReceiptMutationKind.vertexDelete,
    itemCount: 1,
  );
  try {
    final result = await client.deleteVertexWithReceipt(
      'user:42',
      context: deleteContext,
    );
    print('existed: ${result.existed}');
  } on ReceiptReconciliationException catch (error) {
    final status = await client.getReceiptStatus(
      error.context.operationIds.single,
    );
    switch (status.state) {
      case ReceiptStatusState.confirmed:
        final receipt = status.receipt! as VertexDeleteReceipt;
        print('original existed: ${receipt.existed}');
      case ReceiptStatusState.notYetObserved:
        print('not observed; execution remains uncertain');
      case ReceiptStatusState.noLongerProvable:
        print('receipt evidence is no longer available');
    }
  }
}
```

Operation IDs encode the enabled deployment epoch and a server-time-based
issuance timestamp; operation and logical-call entropy comes from
cryptographically secure platform randomness by default. The clock and random
source are injectable for deterministic tests. Constructors validate exact
byte lengths, nonzero identity components, one shared epoch, unique operation
IDs, mutation-family capability, and request-index alignment before network
I/O. Vertex Put supports the same `ifAbsent` intent as its receipt-less
counterpart and returns immutable per-item `PutOutcome` values; receipt-bearing
Vertex Delete preserves an explicit result for every request index, including
`false`. Receipt-bearing Edge Add preserves every request-index-aligned
effective weight, including a born-expired `0`, and rejects missing, duplicate,
zero, or wrong-sized contribution IDs before network I/O.

With `RetryPolicy` configured, a response-loss retry reuses the exact context
only after a read-only capability check confirms the same deployment epoch,
node ID, endpoint generation, and mutation-family support. The retry request
echoes that marker, so a load balancer cannot silently move the replay to
another endpoint. Bearer-token rotation is independent of continuity. Disabled
capability, removed family support, changed continuity, an exhausted ambiguous
attempt, or an untrusted response throws `ReceiptReconciliationException` with
the exact context for status lookup. Confirmed status exposes a sealed
`MutationReceipt` as `VertexPutReceipt`, `VertexDeleteReceipt`, or
`EdgeDeleteReceipt`, or `EdgeAddReceipt`. `notYetObserved` is not proof of
non-execution, and
`noLongerProvable` forbids automatic mutation replay.

## Cursor-paged mobile lists

`scanVertices`, `scanVertexKeys`, and `scanEdges` fetch exactly one bounded
unary page. Each `Page<T>` keeps the item list immutable and exposes an opaque
`ScanCursor?`; `null` unambiguously means end of range. Vertex and keys scans
default to ascending order and support explicit descending order. Edge scans
follow the wire's ascending `(tail, head)` order.

```dart
final subscription = client
    .scanVertexKeysAll(prefix: 'feed:', limit: 100)
    .listen((page) {
      // Append only this page to the visible ListView model.
      visibleKeys.addAll(page.items);
    });

// A screen should release its active page request when disposed.
await subscription.cancel();
```

The `*All` helpers remain page streams: they preserve page boundaries, fetch
at most one page at a time, stop fetching while paused, and propagate stream
cancellation to an active RPC. Prefer `scanVertexKeys` when values are not
needed; it avoids transferring and decoding vertex payloads. Smaller pages
reduce peak memory and cancellation latency, while larger pages reduce RPC
overhead and may use more mobile data before a screen can render or stop.

Prefix count and Delete operations are separate unary calls. Vertex Delete
requires a non-empty prefix, edge Delete requires at least one of tail/head
prefix, and `dryRun` previews the server-bounded count. A positive `limit`
caps one call only. The SDK never retries or silently loops prefix Delete.

## Search, discovery, and traversal

The [canonical SearchVertices contract](https://github.com/anaregdesign/lantern/blob/main/docs/search.md)
is authoritative
for document projection, Unicode analysis, relative BM25 scoring, TTL
consistency, budgets, typed reasons, endpoint-sticky cursors, and HA. The
maintained [Flutter example](https://github.com/anaregdesign/lantern/blob/main/sdks/dart/example/lib/main.dart)
compiles both one-shot and incremental flows, including capability discovery,
phrase/typo options, pagination, disabled handling, and cancellation.

`searchVertices` exposes prefix scope, limit, any/all/min-should-match modes,
phrase matching, fuzziness, and prefix-term expansion. Nullable relevance
fields preserve the distinction between omitted server defaults and explicit
values. `SearchMatchMode.minShouldMatch` accepts a null/zero
`minShouldMatch` as the server-threshold sentinel. Non-zero thresholds under
another mode and phrase combined with an explicit mode/fuzziness/prefix terms
fail locally before transport. A server with search disabled returns the `SearchDisabled` result,
which lets UI code render a calm unavailable state without classifying an
exception. Only the typed `SearchErrorReason.searchDisabled` detail maps to
that result; missing positional postings remain a
`LanternFailedPreconditionException` with
`SearchErrorReason.searchPositionsDisabled`, so phrase capability failures are
never mistaken for a disabled index. `getServerStatus().search` exposes the
endpoint's positions flag, defaults, limits, implementation versions, and HA
configuration fingerprint.
`LanternResourceExhaustedException.searchReason` distinguishes work-budget
exhaustion from admission saturation, while `searchWorkKind` identifies the
exhausted counter. Cancellation, deadline, and work-budget failures are
terminal for that attempt; retry unchanged only for admission saturation with
jittered backoff, and issue only the newest input in incremental UI flows.

`searchVerticesPage` returns one immutable, endpoint-sticky ranked page with
`nextCursor`, `effectiveLimit`, `truncated`, and `continuationLimited`. Repeat
the same `SearchOptions` with that cursor. Expired or evicted sessions throw a
typed `LanternAbortedException` with `SearchErrorReason.searchCursorStale`;
tamper or request/config/endpoint mismatch is a
`LanternInvalidArgumentException` with `searchCursorInvalid`. Restart from page
one explicitly. `searchVerticesStream` follows pages lazily and never builds an
unbounded list; a bounded tail throws
`LanternSearchContinuationLimitedException` after the final retained hit.

`SearchProjection.keyScore` is the lightweight default.
`SearchProjection.fullVertex` includes the exact value/TTL snapshot selected
with ranking and avoids a racy follow-up read.

```dart
final result = await client.searchVertices(
  'quiet cafe',
  searchOptions: const SearchOptions(
    prefix: 'place:',
    matchMode: SearchMatchMode.all,
  ),
);
switch (result) {
  case SearchEnabled(:final hits):
    showHits(hits);
  case SearchDisabled():
    showSearchUnavailable();
}

await for (final hit in client.searchVerticesStream(
  'quiet cafe',
  searchOptions: const SearchOptions(
    limit: 50,
    prefix: 'place:',
    projection: SearchProjection.fullVertex,
  ),
)) {
  showHit(hit.key, hit.vertex);
}
```

For search-as-you-type, create one screen-owned `IncrementalSearch`, listen to
its core-Dart `updates` stream, call `search` for each edit, and `dispose` it
with the screen. It debounces input, cancels superseded calls, drops stale
successes and errors by epoch, and emits an explicit idle state for cleared or
too-short input. It has no Flutter or state-management dependency.

`illuminate` requires exactly one sealed traversal family:

- `BfsOptions` requires positive `step` and `fanOut`.
- `PprOptions(topN: 0)` retains every positive-mass vertex.
- `LocalCommunityOptions(maxSize: 0)` lets the sweep choose its natural size.

Nullable restart probability and epsilon preserve the server defaults;
explicit values are validated locally. Shared `TraversalWeighting` and
`vertexPrefix` apply to every family. The returned immutable `Graph` indexes
vertices by key and stores complete `Edge` objects by tail/head, including
expiration. `edgeWeights` is a derived compatibility view and never replaces
the TTL-bearing data.

`topVerticesByDegree` provides prefix-scoped cold-start ranking for out, in,
or both directions. `getServerStatus` and `getReplicationStatus` each fetch
one immutable snapshot; the SDK never starts status polling implicitly.

`LanternClient.connect` requires HTTPS by default; pass `allowInsecure: true`
only for local development. Supply a short-lived `tokenProvider` for
application calls. `ping()` uses the auth-exempt gRPC Health-v1 Connect+JSON
endpoint and throws
`LanternHealthStatusException` when the server is not serving. Generated
request/response types and the raw Connect client remain under `lib/src/gen`.

`subscribeIdentity(bootstrap: true)` exposes an identity-only CDC checkpoint
followed by exact Vertex/Edge key chunks. Each chunk has an origin-local
sequence, operation category, HLC, index, and final marker; no graph value,
weight, or contribution ID is exposed. Persist invalidation before advancing
the origin cursor on the final chunk. The checkpoint stores the **last**
committed sequence, while `IdentityNextCursor.fromLastApplied` converts a
durable last-applied vector into the **next** sequence expected by a resumed
stream. `failedPrecondition` means the retained tail has a gap and requires a
new checkpoint plus resident-key revalidation. The stream is scoped to one
deployment-wide graph; it does not provide tenant filtering or automatic
offline persistence. The default unary timeout is disabled for this long-lived
stream, but an explicit `LanternCallOptions(timeout: ...)` still applies.

```dart
final tail = client.subscribeIdentity(bootstrap: true);
await for (final frame in tail) {
  switch (frame) {
    case IdentityCheckpointFrame(:final lastSequences):
      // Durably mark resident keys Unknown and save lastSequences.
    case IdentityChunkFrame(:final vertexKeys, :final edgeKeys, :final isLast):
      // Durably invalidate these exact identities; advance on isLast.
  }
}
```

```dart
await client.ping();
// Cancel screen-owned calls with LanternCallOptions(cancellation: token).
await client.close();
```

## Generate wire code

From the repository root, run:

```bash
sdks/dart/scripts/codegen.sh
```

The script generates from the canonical root `proto/` workspace with these
immutable pins:

- `buf.build/protocolbuffers/dart:v22.5.0`
- `buf.build/connectrpc/dart:v1.0.0`

The committed `pubspec.lock` resolves the accepted runtime set exactly to
`connectrpc 1.0.0`, `protobuf 4.2.0`, and `fixnum 1.1.1` for reproducible CI.
Published dependency constraints allow compatible Connect 1.x and Protobuf
4.x releases so applications can resolve a shared dependency graph; the
Protobuf `<5.0.0` ceiling is required by Connect-Dart 1.x.

The supported toolchain policy is recorded in
[ADR 0001](https://github.com/anaregdesign/lantern/blob/main/docs/decisions/0001-dart-mobile-transport.md):
CI tests the package's Dart 3.11 floor and the pinned current Flutter/Dart pair.
Generator/runtime compatibility pins move together through reviewed PRs.

Generated files are committed under `lib/src/gen` and must never be edited by
hand. The script removes only that directory before regeneration; it never
uses root `buf generate --clean`.

## Package checks

```bash
cd sdks/dart
dart pub get --enforce-lockfile
dart format --output=none --set-exit-if-changed \
  lib/lantern_client.dart lib/src/*.dart test
dart analyze lib test
dart test
dart doc --validate-links
dart pub publish --dry-run
```

See [ADR 0001](https://github.com/anaregdesign/lantern/blob/main/docs/decisions/0001-dart-mobile-transport.md)
for the transport decision, supported toolchain evidence, and gRPC fallback
triggers.

The maintained [Flutter example](https://github.com/anaregdesign/lantern/tree/main/sdks/dart/example)
demonstrates runtime token refresh,
secure endpoint configuration, app/screen lifecycle ownership, bounded paging,
incremental search, traversal, typed failure states, and the physical-device
smoke checklist. The full app is a repository integration fixture rather than
part of the `lantern_client` publish archive because it also exercises the
unpublished offline child by path. The archive retains a standalone online
example while remaining dependency-closed. The core SDK has no implicit offline
cache or background-delivery promise. The accepted
[offline Repository and package contract](https://github.com/anaregdesign/lantern/blob/main/docs/decisions/0002-dart-offline-repository-contract.md)
defines the official opt-in `lantern_client_offline` direction: a
storage-adapter-driven cache/outbox engine that remains separate from this
online package and excludes ambiguous mutations from generic replay.
