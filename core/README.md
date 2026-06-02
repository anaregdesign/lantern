# core

Shared core building blocks reused across the Lantern monorepo (server, client,
CLI). Collects general-purpose data structures and algorithms that are not
specific to the gRPC/transport layer.

## Subpackages

| Path | Description |
| --- | --- |
| `cache/` | Generic in-memory cache with TTL, and a graph-specialized cache (`cache/graph`). |
| `collection/` | Generic collections: priority queue (`pq`), set, slice helpers. |
| `concurrent/` | Concurrency primitives, including a typed pub/sub (`concurrent/pubsub`). |
| `graph/` | In-memory graph model and traversal/scoring helpers. |
| `model/function/` | Functional-style type aliases used across the other packages. |
| `nlp/` | Lightweight NLP helpers. |

These packages originated in a standalone toolkit repository and were folded
into this monorepo when the projects were consolidated. They have no
dependencies on the rest of Lantern and are safe to import from anywhere in
this module.