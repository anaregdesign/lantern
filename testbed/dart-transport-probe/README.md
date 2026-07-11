# Dart transport probe

This spike compares the two viable native-mobile transports for the future
Lantern Dart SDK. It is testbed code only and does not introduce a production
SDK API.

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

For a TLS/auth server, set `LANTERN_TLS_CERT_FILE`,
`LANTERN_TLS_KEY_FILE`, and `LANTERN_AUTH_TOKENS`, pass the bearer token as
the second CLI argument, and set `LANTERN_PROBE_CA_CERT` to the trusted CA
PEM path.

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
  connect <device-id> http://<host>:6433 https://<host>:6434 \
  probe-token /path/to/ca.pem
testbed/dart-transport-probe/scripts/mobile.sh \
  grpc <device-id> http://<host>:6433 https://<host>:6434 \
  probe-token /path/to/ca.pem
```

Use `10.0.2.2` as `<host>` for the standard Android emulator and `127.0.0.1`
for the iOS simulator. The test proves:

- plaintext local development connectivity;
- TLS fails closed before CA injection;
- missing bearer metadata is rejected after TLS trust is established;
- trusted TLS + bearer metadata succeeds;
- unary CRUD, `int64`, server streaming, and cancellation work on-device.

The script prints the generated debug APK byte size or iOS simulator app size.
It converts the supplied PEM CA to the single-certificate DER form required by
`SecurityContext` on iOS before injecting it into either candidate.
On Flutter 3.44.6 / Dart 3.12.2 with Android emulator 36.6.11, the measured
debug APKs were 78,879,151 bytes (Connect) and 79,412,635 bytes (gRPC). This is
a harness-level comparison, not a production release-size forecast.

The pull-request workflow reruns generator drift, Dart analysis/tests, and the
full iOS simulator matrix. See the decision record in
`docs/decisions/0001-dart-mobile-transport.md`.
