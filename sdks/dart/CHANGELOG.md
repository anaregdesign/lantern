# Changelog

## Unreleased

## 0.4.1

- Keep release preflight and protected publication reachable after deliberately
  skipped optional CI jobs, while requiring a successful direct Gate/preflight
  and blocking canceled runs. The `0.4.0` tag passed its Gate but did not publish.

- Add plural-canonical targeted Edge contribution Delete and receipt-bearing
  facades with nonzero 24-byte caller-owned IDs, aligned duplicate/missing
  results, and strict response validation. Whole-edge Delete stays separate.
- Decode distinct contribution Delete receipt originals and identity CDC
  operation 7; invalidate and re-fetch the affected Edge. Uncertain results
  never authorize a blind cross-endpoint retry.
- Require same-source signed Android/iPhone response-loss, real SIGKILL/relaunch,
  installed-binary evidence and exact archive verification before publication.
- Existing offline outbox codecs do not support contribution Delete. Their
  adapters reject unknown receipt results instead of treating them as Edge Delete.

## 0.4.0 (unpublished)

- The immutable tag passed package/native gates and physical qualification, but
  implicit job-status propagation skipped release preflight and publication.
  No pub.dev package or GitHub Release was created; use `0.4.1`.

## 0.3.3

- Honor SDK deadlines and caller cancellation while a native HTTP connection
  is opening. Abort a request that opens after the call has already been
  canceled.

## 0.3.2

- Make receipt-less Edge Add (singular, plural, and decaying) single-attempt
  even with contribution IDs and an opt-in retry policy. A lost response stays
  ambiguous instead of risking reapplication after an intervening Delete or
  expiry. Receipt-backed Add retains status-checked recovery on certified
  endpoints. The published 0.3.1 archive still has the unsafe plain-Add retry.

## 0.3.1

- Add immutable online bounded-receipt capability, identity, endpoint,
  reconciliation, and three-state status APIs for Vertex Put, exact Vertex
  Delete, Edge Delete, and contribution-keyed Edge Add. Receipt-bearing
  Add requires explicit nonzero 24-byte contribution IDs and retains each
  original effective weight, including born-expired zero, signed infinity, and
  semantic NaN. All mutations retain exact request-aligned original results
  and permit opt-in same-endpoint retry only after continuity and family
  support are revalidated; existing receipt-less mutations are unchanged.
  Locally detected malformed successful responses are explicitly distinguished
  from genuine server or transport `INTERNAL` failures so durable adapters can
  fail closed without suppressing retryable uncertainty.

- Regenerate the internal replication wire schema so D4 Delete mutations carry
  the origin's absolute tombstone deadline. The public client API is unchanged.

## 0.3.0

- Add a typed, value-free identity-only CDC stream with atomic bootstrap
  checkpoints and portable per-origin NEXT-sequence cursors. Stream pause,
  cancellation, and Connect errors now propagate through the transport;
  long-lived streams can omit the default unary timeout.

## 0.2.0

- Return bounded, server-clock-authoritative per-item `PutOutcome` values from
  every Vertex/Edge Put facade, replacing the earlier aggregate/bool return
  shapes. Plural calls now return request-aligned immutable Lists and fail
  closed on unknown or length-misaligned outcomes.
- Expose detachable, fan-out-safe `LanternCancellationToken.listen` lifecycle
  notifications so companion packages can isolate shared work from individual
  callers without polling.
- Add the per-call `LanternCallOptions.retry` override so a higher-level durable
  coordinator can suppress the client's retry policy. Each RPC is attempted at
  most once, while a chunked plural logical call may still issue multiple RPCs.
- Add optional causal-metadata capacity and retention snapshots to
  `ServerStatus`, while preserving `null` as the signal that an older server
  does not support the status surface.
- Make the pub.dev archive dependency-closed: exclude the repository-only
  Flutter/offline packages and retain a standalone online example.

## 0.1.3

- Resolve the package lock without descending into the Flutter example on the
  pure-Dart pub.dev OIDC runner. This patch changes release automation only;
  the SDK API and runtime behavior are unchanged from 0.1.2.

## 0.1.2

- Bootstrap pub.dev authentication with the official `dart-lang/setup-dart`
  GitHub OIDC flow so headless releases cannot fall back to interactive Google
  OAuth.
- Bound the publish job with a hard timeout. This patch changes release
  automation only; the SDK API and runtime behavior are unchanged from 0.1.1.

## 0.1.1

- Accept the Dart offline contract and set an official, opt-in
  `lantern_client_offline` package as the product direction while keeping the
  online `lantern_client` free of implicit persistence and platform storage
  dependencies.
- Specify a single-item durable mutation model, stable logical-call grouping,
  per-key ordering, explicit dead-letter/ambiguous controls, and independently
  implementable codec, package, integration, and server follow-ups.

## 0.1.0

- Scaffold the pure-Dart `lantern_client` package.
- Add reproducible Connect-Dart and Protobuf code generation.
- Add immutable public `Vertex`, `Edge`, input, reference, and batch result
  models while keeping generated Protobuf types internal.
- Add exact sealed `VertexValue` variants for every oneof kind, including
  explicit nil versus unset, full uint64 range, and defensive byte ownership.
- Add strict permanent/relative/absolute expiration contracts and an injected
  clock for once-per-call relative TTL calculation.
- Add singular/plural Vertex and Edge CRUD with 1,000-item chunking, a 65,536
  logical ceiling, partial-progress errors, conditional puts, and distinct
  additive versus overwrite edge operations.
- Add opt-in bounded, cancellation-aware mobile retry with a fail-closed RPC
  matrix and one deadline budget across attempts and backoff.
- Add canonical 24-byte contribution IDs, optional automatic Add stamping, and
  committed-response-loss protection without replaying ambiguous mutations.
- Add geometric decaying-edge expansion with an exact initial live sum and a
  maintained 16-step ceiling.
- Add immutable cursor pages, asc/desc vertex and keys scans, ascending edge
  intersection scans, and lazy backpressure-aware page streams.
- Add exact prefix counts plus locally scoped dry-run/capped prefix Deletes
  that are never automatically replayed or destructively looped.
- Add complete full-text search controls, a typed search-disabled result, and
  a pure-Dart latest-query-wins incremental search session.
- Add sealed BFS/PPR/local-community traversal families and an immutable Graph
  that retains complete Edge expiration with a derived weight-only view.
- Add prefix-scoped degree ranking plus explicit immutable server and
  replication status snapshots.
- Add a maintained Android/iOS Flutter example with runtime token refresh,
  lifecycle cancellation/refetch, bounded lists, typed UI states, and all
  major SDK surfaces compiled in CI.
- Add Android emulator and iOS simulator real-wire gates, Android/iOS build
  gates, mobile security/troubleshooting guidance, and a physical-device smoke
  evidence checklist.
- Document the initial app-owned offline Repository contract, including exact
  cache freshness, durable contribution IDs, crash-safe outbox states,
  ambiguous-operation exclusions, threat controls, and golden scenarios; the
  core SDK still has no implicit persistence.
- Add the secure, injectable `LanternClient` transport foundation with
  per-call token providers, deadlines, cancellation, typed errors, and close.
- Add auth-exempt gRPC Health-v1 `ping()` probing with typed non-serving status
  errors.
