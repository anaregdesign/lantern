# Changelog

## Unreleased

## 0.4.0

- Add `current_authority_binding` and `current_user_id` for validated,
  authenticated Principal reads. Bindings partition caches rather
  than authorize later calls; UID-less or machine Principals return no user ID.
- Add public `watch_changes` with scope-bound opaque checkpoints, identity or
  current-value projections, and checked bootstrap/resume frames. Streams stay
  on one endpoint without automatic reconnect, cursor reset, or persistence.
- Add plural-first `create_edges` / `create_edge` and their `_with_options`
  variants with validated `CreateEdgeOutcome` results. The Server checks
  existing live endpoints and authority; Create never retries automatically
  and remains rejected in HA.
- Distinguish accepted Edge mutations with undisclosed effects from RPC
  failures through `LanternError::MutationAcceptedUndisclosed` and the
  `MutationReply` adapters. Preserve real/partial failures and suppress restore
  progress counts when effects are undisclosed instead of fabricating results.
- Decode typed conditional Create effects and original results in legacy
  full-mutation CDC. Accept ordered Vertex Put / Edge Add identity chunks, but
  reject unsupported mixed full-mutation envelopes before advancing a cursor.
- Refresh private bindings for current authority, registration/native sessions,
  and mixed graph mutations without exposing generated service clients or
  receipt-backed mutation write/replay APIs. Expand protocol and real-wire
  conformance coverage for scoped CDC, current authority, UID reads, and
  undisclosed effects.
- Update the pinned rustls dependency to 0.23.46.

## 0.3.0

- Add true, single-endpoint server-streaming `Subscribe`: checkpoint-first
  identity invalidation and portable next-expected per-origin resume, explicit
  full-mutation mode with all 21 graph/receipt arms, typed gaps and category-7
  contribution invalidation. Streams use verified TLS/mTLS and per-open bearer
  auth with independent idle/lifetime budgets; they never retry.
- Add graph-only prefix-induced `BackupSnapshot`, bounded protobuf or Rust
  NDJSON archive writers, a separately persisted SHA-256/count manifest, and
  preflight-checked, two-pass Vertex-before-Edge public-Put restore with
  partial-progress reporting. Nonfinite folded weights and elapsed deadlines
  fail before any writes instead of being silently dropped.
- Add authenticated singleton/HA production-wire CDC and backup/restore
  conformance, receipt-WAL envelope fixtures, cancellation and malformed-frame
  tests, and a secure opt-in CDC/backup example.
- Correct the published-crate README wording and guard packaged README text
  against stale prepublication claims.

## 0.2.0

- Add typed singular and plural deletion of one caller-known edge contribution
  without removing other contributions or the Put base. Validate identities
  and index-aligned results; never retry an uncertain receipt-less Delete.
- Regenerate private bindings for the new contribution Delete and receipt/CDC
  wire variants without adding a partial Rust receipt API.

## 0.1.0

- Initial standalone crate scaffold, checked-in private gRPC bindings, and
  repository-only codegen and real-wire smoke gates.
- Single-endpoint Tonic transport with verified TLS and optional mTLS,
  per-attempt bearer auth, gRPC Health, bounded deadlines and message
  limits, fail-closed typed errors, opt-in classified retry, and real
  Go-server h2c/TLS/auth failure tests.
- Exact protobuf value kinds and expiration, bounded plural-first Vertex/Edge
  Get/Put/conditional Put/Delete/Add with validated result shapes, prepared
  live-contribution IDs, shared batch deadline and partial-error accounting.
  Real-server h2c and controlled malformed-response tests.
- Bounded vertex/key/edge scan pages and lazy streams, one-shot prefix
  count/deletes, typed paged/streaming search with projection and rich
  failure details, typed BFS/PPR/community traversal, degree ranking, and
  explicit server/replication status snapshots. Real-server h2c and
  controlled malformed-response tests.
- Maintained `rustls-pki-types` PEM parsing, a secure end-to-end example,
  native real-wire conformance, isolated archive/advisory release gates,
  and a first-publication/OIDC runbook.
