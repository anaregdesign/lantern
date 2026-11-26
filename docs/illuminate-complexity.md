# How much work does an `Illuminate` neighborhood query do?

For a BFS request, Lantern expands the outgoing Edges of each visited tail
using a resident adjacency index. The node's Tarp database holds the Vertex
payloads, Edge records and individual weight contributions. The adjacency index
determines which neighbors to examine. Fetching payloads and calculating a live
weight can also require storage reads.

This guide covers the BFS family of `Illuminate`. Personalized PageRank and
local-community requests use different traversal algorithms and work budgets.
The Server dispatches the selected family and applies any requested BFS reduction.
[RPC implementation](../server/internal/dataruntime/executor.go)

## How BFS expands the frontier

`neighborContext` starts from the seed and looks up each frontier tail's outgoing
bucket with `c.edges.headsOf(t)`. It checks Vertex liveness and query predicates
before selecting the top neighbors. The same sampled time determines Edge
expiration throughout the walk. A seen set ensures each tail is expanded at
most once. [BFS implementation](../core/graphcache/traversal.go)

Let $F$ be the number of tails expanded and $C$ the sum of their stored
out-degrees. Finding those buckets and enumerating their entries takes
$O(F+C)$ expected index work. $C$ includes candidates that subsequently fail a
liveness, access or prefix check. A small response can therefore require a
large scan at a high-degree tail.

### Weighting, fan-out and storage reads

A valid retained Edge aggregate supplies its sum and expiration directly. When
the next contribution expiry has passed, calculating the aggregate can require
loading and examining the underlying contributions. Vertex payloads needed for
the result can likewise be loaded from a committed Tarp snapshot. Storage
latency depends on record sizes, the number of contributing records read and
cache residency. [Edge aggregation](../core/graphcache/edge.go) ·
[Contribution loading](../core/graphcache/storage_weight.go) ·
[Vertex loading](../core/graphcache/storage_residency.go)

For each expanded tail, BFS scores eligible heads using raw weight, TF-IDF or
BM25. The objective selects the largest or smallest $k$ scores, with ascending
Vertex keys breaking equal-score ties. For $m$ scored heads and $1 \le k < m$,
bounded-heap selection takes $O(m\log(k+1))$ time. The traversal still
examines the candidate heads before pruning. Only the selected heads can enter
the next frontier. [Top and bottom selection](../core/collection/pq/map.go)

For example, in a tree with six eligible outgoing Edges per expanded tail, a
fan-out of six and a depth of three, BFS expands $1+6+36=43$ tails and examines
$6+36+216=258$ candidate Edges. Giving the seed another thousand outgoing
Edges adds a thousand candidates to examine even if fan-out stays at six.

### Optional directed-arborescence and shortest-path reductions

The Server first collects the bounded neighborhood. If requested, the MST
reduction then computes a minimum or maximum rooted directed arborescence
using Chu–Liu/Edmonds. The SPT reduction computes a shortest-path tree over the
collected graph. The size and structure of that intermediate graph determine
this extra work. [Reduction selection](../server/internal/dataruntime/optimizer.go) ·
[Graph algorithms](../core/graph/model.go)

## Comparison with an indexed relational walk

A relational representation can index Edges by tail.

```sql
CREATE TABLE edges (tail TEXT, head TEXT, weight REAL);
CREATE INDEX idx_edges_tail ON edges (tail);
```

A recursive CTE can expand the outgoing Edges of each frontier row.

```sql
WITH RECURSIVE walk(v, depth) AS (
    SELECT :seed, 0
    UNION ALL
    SELECT e.head, walk.depth + 1
    FROM walk
    JOIN edges e ON e.tail = walk.v
    WHERE walk.depth < :hops
)
SELECT * FROM walk;
```

This example illustrates adjacency lookup. An equivalent application query
also needs the required TTL checks, access predicates, per-tail fan-out and
handling of revisited vertices. In a nested-loop plan using the tail index,
each frontier row seeks its tail and scans all matching entries. For a tree of
height $H$, $F$ frontier rows and $C$ matching entries, that plan does
$O(FH+C)$ lookup and enumeration work. The optimizer may choose a different
plan, especially when a frontier reaches a large fraction of the table.

Both implementations pay for candidate enumeration and requested results.
A latency comparison needs the tail-degree distribution, contribution counts,
Vertex sizes, cache state and the actual SQL execution plan. Lantern's
node-local persistence and replication semantics are described in the
[storage and recovery guide](backup.md) and the [replication guide](replication.md).
