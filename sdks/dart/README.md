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
await client.putVertex(
  VertexInput(
    key: 'user:42',
    value: VertexValue.string('alice'),
    expiresIn: const Duration(minutes: 30),
  ),
);
final vertex = await client.getVertex('user:42');
```

`VertexValue` has named factories for every wire oneof kind; bare Dart numbers
are never guessed into a Protobuf numeric kind. `VertexValue.nil()` and
`VertexValue.unset()` remain distinct. `VertexInput` and `EdgeInput` omit wire
expiration when both expiration fields are absent, which means permanent
storage. `expiresIn` must be positive, is mutually exclusive with `expiresAt`,
and is converted to one absolute UTC instant from the client's clock before
chunking. Device-clock skew therefore affects relative TTLs.

Plural CRUD calls use 1,000-item chunks by default and reject logical batches
above 65,536 items. A later chunk failure throws `BatchException` with the
confirmed committed count. `addEdge(s)` accumulates weight and is
non-idempotent unless the caller supplies an exact 24-byte `contribId`;
`putEdge(s)` overwrites weight and expiration idempotently.

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

Automatic IDs do not turn two application calls into one operation and do not
survive process restart. For an offline/durable outbox, generate or accept a
caller ID, persist its exact 24 bytes with the intent, and reuse it on replay.
This package does not implement an offline queue. A contribution ID deduplicates
only while the server retains that contribution.

`addDecayingEdge` expands a geometric curve into at most 16 staggered-TTL
contributions whose initial live sum is exact. With `idempotentAdds` enabled,
the entire expanded call is also safe against an ambiguous response loss.

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
request/response types, the raw Connect client, and replication service remain
under `lib/src/gen`.

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
dart analyze
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
smoke checklist. The SDK has no offline cache or background-delivery promise.
Applications considering persistence must follow the proposed
[offline Repository contract](https://github.com/anaregdesign/lantern/blob/main/docs/decisions/0002-dart-offline-repository-contract.md),
which keeps cache/outbox policy outside the core client and excludes ambiguous
mutations from generic replay.
