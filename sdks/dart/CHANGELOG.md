# Changelog

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
