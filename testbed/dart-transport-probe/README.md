# Dart transport probe

This spike compares the two viable native-mobile transports for the future
Lantern Dart SDK. Connect is the accepted transport; gRPC remains the tested
fallback. This is testbed code only and does not introduce a production SDK
API.

| Candidate     | Runtime pins                         | Buf plugin pins                                          | Wire path                               |
| ------------- | ------------------------------------ | -------------------------------------------------------- | --------------------------------------- |
| Connect       | `connectrpc 1.0.0`, `protobuf 4.2.0` | `connectrpc/dart:v1.0.0`, `protocolbuffers/dart:v22.5.0` | Connect + Protobuf over native HTTP/1.1 |
| gRPC fallback | `grpc 5.1.0`, `protobuf 6.0.0`       | `protocolbuffers/dart:v25.0.0` with `grpc`               | gRPC + Protobuf over HTTP/2             |

The candidates intentionally live in separate Dart packages. Their current
protobuf constraints cannot resolve in one package: Connect-Dart requires
`protobuf <5`, while gRPC-Dart 5.1.0 requires `protobuf ^6.0.0`.

## Generate and verify

Run from the repository root:

```bash
testbed/dart-transport-probe/scripts/codegen.sh
(cd testbed/dart-transport-probe/connect && dart pub get && dart analyze && dart test)
(cd testbed/dart-transport-probe/grpc && dart pub get && dart analyze && dart test)
git diff --exit-code -- testbed/dart-transport-probe
```

Generated files under each `lib/src/gen/` are committed and must only be
changed by `codegen.sh`.

## Dart VM real-wire probe

Start Lantern, then run either candidate:

```bash
LANTERN_PORT=6433 LANTERN_METRICS_ADDR=:9143 go run ./server/cmd
(cd testbed/dart-transport-probe/connect && dart run tool/probe.dart http://127.0.0.1:6433)
(cd testbed/dart-transport-probe/grpc && dart run tool/probe.dart http://127.0.0.1:6433)
```

For protected TLS, build the production Server and `server/cmd/authfixture`,
then use `testbed/scripts/dart_transport_fixture.sh start <absolute-server>
<absolute-authfixture> <new-private-state-directory> 6434`. This supervises
native OIDC security state and qualified-clock admission with a named machine
Principal. Its Role grants read/write/export only under `probe/connect/` and
`probe/grpc/`; no security, receipt, CDC or private peer capability is added.
The fixture's public certificate is valid only for `localhost`, preserving a
separate hostname-mismatch check. No external IdP or retired static-token
setting is used.

Read the CA path from private `metadata.json` (`ca_file`) and set
`LANTERN_PROBE_CA_CERT` to it. Pass `<state>/token`, a private token **file**, as
the second CLI argument; the token value is never a shell argument. Finish with
`bash testbed/scripts/dart_transport_fixture.sh stop <state>`; teardown failure
invalidates the run. Keep metadata and native diagnostics private. This local
fixture does not qualify production clocks, Google or physical devices.

Each successful probe checks a generated `int64` round-trip, unary Put/Get,
auth metadata, a `BackupSnapshot` server stream, and cancellation after the
first record. The Connect candidate also verifies response header/trailer
callbacks and uses `TimeoutSignal`. Both probes accept an injected native
client (`HttpClient` or `ClientChannel`) in addition to the CA-path helper.

## Android and iOS

`mobile.sh` creates a disposable Flutter harness, applies only the local
network policy required by the generated Android/iOS shells, and runs the same
contract over plaintext plus an authenticated self-signed TLS endpoint:

```bash
testbed/dart-transport-probe/scripts/mobile.sh \
  connect <device-id> http://<host>:6433 https://localhost:6434 \
  /path/to/private/state/token /path/to/ca.pem
testbed/dart-transport-probe/scripts/mobile.sh \
  grpc <device-id> http://<host>:6433 https://localhost:6434 \
  /path/to/private/state/token /path/to/ca.pem
```

The maintained native fixture targets the iOS simulator on `localhost`; use
`127.0.0.1` for its plaintext `<host>`. A physical device or Android emulator
needs separately scoped routing and a certificate for that actual host; the
localhost-only fixture must not be relabeled as physical qualification.
The test proves:

- plaintext local development connectivity;
- TLS fails closed before CA injection;
- missing bearer metadata is rejected after TLS trust is established;
- trusted TLS + bearer metadata succeeds;
- unary CRUD, `int64`, server streaming, and cancellation work on-device.

The script prints the generated debug APK byte size or iOS simulator app size.
For Flutter's integration-test driver it converts the supplied PEM CA to a
single-certificate DER trust anchor. The CI `simctl` driver injects a
per-client callback that accepts only the expected hostname and exact leaf or
CA PEM. The hostname-mismatch test separately injects the trusted CA bytes
without a pin callback, so an untrusted-CA error cannot stand in for hostname
validation. It never
uses a global or allow-all certificate bypass. Android separately proves
app-level custom CA injection for both candidates.
On Flutter 3.44.6 / Dart 3.12.2 with Android emulator 36.6.11, the measured
debug APKs were 78,879,151 bytes (Connect) and 79,412,635 bytes (gRPC). This is
a harness-level comparison, not a production release-size forecast.
The GitHub-hosted iOS 18.5 Simulator app bundles measured 98,116 KiB (Connect)
and 100,500 KiB (gRPC).

The pull-request workflow reruns generator drift, Dart analysis/tests, and the
full iOS simulator matrix. CI builds and launches a disposable app directly
with `simctl`, then observes a success marker over the real Lantern wire path;
this avoids coupling the gate to Flutter's integration-test VM service. See
the decision record in
`docs/decisions/0001-dart-mobile-transport.md`.
