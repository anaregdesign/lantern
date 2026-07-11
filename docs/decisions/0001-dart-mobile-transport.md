# 0001: Dart mobile transport

- Status: Proposed — iOS simulator gate pending
- Date: 2026-07-11
- Issue: #1006

## Context

Lantern's primary listener accepts the Connect protocol over native HTTP/1.1
as well as gRPC over HTTP/2. The planned Dart SDK needs unary RPCs and the
server-streaming `BackupSnapshot` RPC on Android and iOS. Connect-Dart provides
the thinner protocol fit, but its stable 1.0.0 release pins `protobuf <5` and
therefore cannot share the current gRPC-Dart 5.1.0 / protobuf 6 dependency set.

## Proposed decision

Adopt Connect-Dart with the Connect protocol and the native `dart:io`
HTTP/1.1 client, contingent on the repository's iOS simulator gate passing.
Use these exact initial pins:

- runtime: `connectrpc 1.0.0`, `protobuf 4.2.0`, `fixnum 1.1.1`;
- code generation: `buf.build/connectrpc/dart:v1.0.0` and
  `buf.build/protocolbuffers/dart:v22.5.0`;
- Dart floor: 3.7.0, matching Connect-Dart's supported language floor;
- validated Flutter toolchain: Flutter 3.44.6 / Dart 3.12.2;
- supported production platforms: Android and iOS native only; Flutter Web is
  outside this decision.

Keep gRPC-Dart 5.1.0 + protobuf 6.0.0 + protoc plugin 25.0.0 as the documented
fallback. Switch before the first Dart SDK release if the iOS gate fails for a
transport-caused reason, generated code requires patches, a future Lantern
schema cannot compile under protobuf 4.x, Connect-Dart becomes incompatible
with the supported Flutter toolchain, or a security/maintenance issue cannot
be resolved without vendoring or forking.

## Evidence so far

Both candidates resolve without dependency overrides in separate packages,
generate the full Lantern schema plus Well-Known Types, and compile without
patches. Dart VM and Android emulator real-wire runs cover:

- Connect HTTP/1.1 and gRPC HTTP/2 plaintext;
- trusted TLS and fail-closed untrusted TLS;
- rejected missing bearer metadata and successful authenticated calls;
- unary Put/Get with an out-of-JavaScript-safe-range `int64`;
- `BackupSnapshot` server streaming and client cancellation;
- Connect response headers/trailers, `TimeoutSignal`, and `CancelableSignal`;
- generated `uint64`, timestamp, duration, and oneof serialization;
- custom trust stores and native HTTP/channel injection.

Android used Flutter 3.44.6 / Dart 3.12.2 and emulator 36.6.11. The disposable
debug harness measured 78,879,151 bytes for Connect and 79,412,635 bytes for
gRPC. No Android-specific socket or TLS workaround was needed beyond the
generated app's explicit localhost cleartext policy.

The final acceptance step is the same plaintext/TLS/auth/stream contract on a
GitHub-hosted iOS simulator. This record must move to `Accepted` only after that
gate passes; an unavailable local Xcode license is an environment limitation,
not evidence against either transport.

## Consequences

The official SDK will stay on protobuf 4.x until a compatible Connect-Dart
release raises its ceiling. Its public client will expose transport/HTTP-client
injection and cancellation instead of hiding the runtime. gRPC-Dart remains a
tested escape hatch, but it will not be shipped as a second production backend
unless a fallback trigger fires.
