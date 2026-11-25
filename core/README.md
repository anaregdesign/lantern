# core

Core implements Lantern's graph storage, TTL handling, queries, and reusable
Go data structures. Each server node persists its graph and replication state
in a local database backed by [Tarp](https://github.com/anaregdesign/tarp).

## Subpackages

| Path | Description |
| --- | --- |
| `cache/` | Generic in-memory cache with TTL. |
| `collection/` | Generic collections: priority queue (`pq`), set, slice helpers. |
| `concurrent/` | Concurrency primitives, including a typed pub/sub (`concurrent/pubsub`). |
| `graph/` | In-memory graph model and traversal/scoring helpers. |
| `graphcache/` | Applies Vertex and Edge TTLs, additive Edge contributions and causal ordering. Maintains derived indexes for queries. |
| [`graphstore/`](graphstore/) | Stores ordered Vertex, Edge, contribution and service records in Tarp. Provides atomic updates, snapshots and recovery. |
| `llm/` | Backend-agnostic abstractions for single-shot, structured-output language models. |
| `model/function/` | Functional-style type aliases used across the other packages. |
| `mutationlog/` | Ordered retained mutation history, bounded live subscriptions and WAL publication boundaries. |
| `nlp/` | Lightweight NLP helpers. |

These packages originated in a standalone toolkit repository and were folded
into this monorepo when the projects were consolidated. They have no
dependencies on the rest of Lantern and are safe to import from anywhere in
this module.

For network delivery, `mutationlog.Log.SubscribeStream` returns a subscription
with separate `Entries`, `Done`, `Err` and `Close` methods. Watch `Done` while
performing a blocking send: live overflow reports `ErrSubscriptionLost` before
queued delivery drains. Revalidate the committed cursor to decide whether
retained replay can resume or a Snapshot is required. `Close` unregisters and
joins the delivery goroutine. The channel helpers use the same subscription
engine; normal log closure still drains the ordered committed tail.
