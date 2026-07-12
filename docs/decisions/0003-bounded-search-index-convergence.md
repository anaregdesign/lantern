# 0003: Bound the derived search index without weakening graph convergence

Status: Accepted

## Context

Lantern's graph is authoritative. The full-text index is a local derived view
whose token, term, posting, position, and retained high-water amplification can
be much larger than the stored vertex count. Replicas and backup files may also
contain values written under different limits.

Rejecting such a value during replication or restore would make graph state
diverge. Silently indexing only part of it would make search results incomplete
while presenting the index as healthy.

## Decision

Local client writes are analyzed and checked against the final batch state
before cache, HLC, or mutation-log changes. Any per-document or aggregate
limit failure rejects the complete plural write with a typed
`SEARCH_INDEX_BUDGET_EXHAUSTED` detail.

Replication apply and restore always apply an accepted source mutation to the
graph. If the local search limits cannot represent its resulting document or
aggregate state, the node atomically leaves the previous index in place and
marks it `incomplete`. `SearchVertices` then fails closed with
`SEARCH_INDEX_INCOMPLETE`; point reads, scans, traversal, replication, and
backup remain available.

A bounded rebuild analyzes every live vertex into a replacement index and
swaps it atomically only after all limits succeed. It restores `healthy` state.
Removing or resizing the offending values is therefore required before a
rebuild can succeed.

Valid UTF-8 byte values are eligible for text indexing and share the projected
document byte limit. Arbitrary binary values are stored normally but contribute
only their key to full-text search.

Term IDs and document ordinals are reusable during steady state. Deletes and
TTL expiry trigger an atomic compaction when retained/live ratios and a minimum
retired-slot floor are crossed; an empty index immediately returns to fresh
small allocations.

## Consequences

- Graph convergence never depends on local search configuration.
- Search never claims that a partial derived view is healthy.
- Mixed-limit HA members are visible through the capability fingerprint,
  status snapshot, and bounded Prometheus gauges.
- `GOMEMLIMIT` remains an outer runtime safety net, not a substitute for the
  deterministic index budgets.
