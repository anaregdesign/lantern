# Changelog

## Unreleased

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
