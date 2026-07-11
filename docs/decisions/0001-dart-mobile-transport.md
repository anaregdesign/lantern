# 0001: Dart mobile transport

- Status: Accepted
- Date: 2026-07-11
- Issue: #1006

## Context

Lantern's primary listener accepts the Connect protocol over native HTTP/1.1
as well as gRPC over HTTP/2. The planned Dart SDK needs unary RPCs and the
server-streaming `BackupSnapshot` RPC on Android and iOS. Connect-Dart provides
the thinner protocol fit, but its stable 1.0.0 release pins `protobuf <5` and
therefore cannot share the current gRPC-Dart 5.1.0 / protobuf 6 dependency set.

## Decision

Adopt Connect-Dart with the Connect protocol and the native `dart:io`
HTTP/1.1 client. Use these exact initial pins:

- runtime: `connectrpc 1.0.0`, `protobuf 4.2.0`, `fixnum 1.1.1`;
- code generation: `buf.build/connectrpc/dart:v1.0.0` and
  `buf.build/protocolbuffers/dart:v22.5.0`;
- Dart floor: 3.10.0, allowing the maintained lints and test toolchain;
- validated Flutter toolchain: Flutter 3.44.6 / Dart 3.12.2;
- supported production platforms: Android and iOS native only; Flutter Web is
  outside this decision.

Keep gRPC-Dart 5.1.0 + protobuf 6.0.0 + protoc plugin 25.0.0 as the documented
fallback. Switch before a later Dart SDK release if generated code requires
patches, a future Lantern schema cannot compile under protobuf 4.x,
Connect-Dart becomes incompatible with the supported Flutter toolchain, or a
security/maintenance issue cannot be resolved without vendoring or forking.

## Evidence

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

The same contract passed for both candidates in independently built apps on a
GitHub-hosted iOS 18.5 Simulator. The debug app bundles measured 98,116 KiB for
Connect and 100,500 KiB for gRPC. The gate launches each app directly with
`simctl` and observes a success marker written only after the full contract
passes, avoiding Flutter integration-test VM-service startup flakiness.

On iOS, directly loading the test CA through `SecurityContext` failed with
`BAD_PKCS12_DATA` under the validated toolchain for both candidates. This is a
shared Dart runtime behavior, not a transport-specific failure. The accepted
gate uses each candidate's per-client certificate callback to require both the
expected hostname and an exact leaf/CA PEM match; it separately proves an
unpinned hostname mismatch fails closed. It never uses a global or allow-all
verification bypass. Android also proves app-level custom CA injection. The
production SDK therefore exposes native client injection for platform trust,
private PKI, mTLS, and strict pinning instead of owning certificate policy.

## Consequences

The official SDK will stay on protobuf 4.x until a compatible Connect-Dart
release raises its ceiling. Its public client will expose transport/HTTP-client
injection and cancellation instead of hiding the runtime. gRPC-Dart remains a
tested escape hatch, but it will not be shipped as a second production backend
unless a fallback trigger fires. Connect's thinner dependency set and smaller
Android/iOS debug artifacts reinforce the protocol fit, but correctness and
maintainability—not the harness size delta—are the deciding criteria.
