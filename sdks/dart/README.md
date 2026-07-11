# lantern_client

Official pure-Dart package for [Lantern](https://github.com/anaregdesign/lantern),
an in-memory graph key-vertex store with TTL-aware vertices and edges.

The package includes the reusable transport foundation used by the later CRUD,
query, and traversal facades: secure endpoint validation, per-call bearer-token
providers, deadlines, cancellation, typed failures, health probing, and
deterministic cleanup. Generated RPC request/response types remain private.

## Scope

- Android and iOS are the first-class production targets.
- Dart VM is supported for tests and command-line integration tooling.
- This is a pure Dart package: it has no Flutter runtime dependency, plugin
  declaration, platform channel, or native implementation.
- Flutter Web and desktop support are not guaranteed by v0.1.

The public import is:

```dart
import 'package:lantern_client/lantern_client.dart';

final vertex = Vertex(key: 'user:42', string: 'alice');
final edge = Edge(tail: 'user:42', head: 'item:7', weight: 1);
final graph = Graph(vertices: [vertex], edges: [edge]);
```

`Vertex`, `Edge`, `Graph`, the generated vertex oneof discriminator, and the
`LanternClient` foundation are exported. `LanternClient.connect` requires HTTPS
by default; pass `allowInsecure: true` only for local development. Supply a
short-lived `tokenProvider` for application calls. `ping()` uses the
auth-exempt gRPC Health-v1 Connect+JSON endpoint and throws
`LanternHealthStatusException` when the server is not serving. Generated
request/response types, the raw Connect client, and replication service remain
under `lib/src/gen`.

```dart
final client = LanternClient.connect(
  Uri.parse('https://lantern.example.com'),
  tokenProvider: () async => session.accessToken,
);
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

Generated files are committed under `lib/src/gen` and must never be edited by
hand. The script removes only that directory before regeneration; it never
uses root `buf generate --clean`.

## Package checks

```bash
cd sdks/dart
dart pub get --enforce-lockfile
dart format --output=none --set-exit-if-changed lib/lantern_client.dart test
dart analyze
dart test
dart doc --validate-links
dart pub publish --dry-run
```

See [ADR 0001](https://github.com/anaregdesign/lantern/blob/main/docs/decisions/0001-dart-mobile-transport.md)
for the transport decision, supported toolchain evidence, and gRPC fallback
triggers.
