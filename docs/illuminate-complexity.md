# Why `Illuminate` beats a B-tree self-join for neighborhood search

Lantern's `Illuminate` walks the neighborhood of a seed vertex by following an
in-memory adjacency map. A relational database does the same walk with a
**B-tree index and a self-join** (or a recursive CTE). Both return the same
$k$-hop subgraph, but they pay different asymptotic costs to get there.

This note explains the difference, grounds it in the actual traversal code, and
states the cases where the gap narrows. It is reference material — there is no
behavioral contract here.

## TL;DR

| Approach | $k$-hop neighborhood cost |
|---|---|
| `Illuminate` — index-free adjacency (hash-linked) | $O(E_{sub})$ — proportional to the edges of the visited subgraph |
| B-tree RDB — self-join / recursive CTE | $O(E_{sub} \cdot \log E)$ — every edge hop pays a tree descent |

Roughly: **the graph traversal is faster by the height of the B-tree's index,
$\log E$**. And critically, `Illuminate`'s cost depends only on the *visited
neighborhood* $E_{sub}$, while the relational plan stays coupled to the *whole
table* $E$ through that $\log E$ factor.

## Notation

| Symbol | Meaning |
|---|---|
| $N$ | total live vertices in the store |
| $E$ | total live edges in the store |
| $h$ | hop limit (the `step` argument to `Illuminate`) |
| $k$ | per-hop top-$k$ fan-out kept by the prune |
| $V_{sub},\,E_{sub}$ | vertices / edges of the *visited* subgraph (the result plus what was scanned to find it) |
| $\deg(t)$ | out-degree of vertex $t$ |

For both engines the result itself can grow as $O(k^{h})$ in the worst case
(branching factor $k$, depth $h$). That term is **common to both** approaches,
so it is not where the difference lives — the difference is the per-edge
constant.

## How `Illuminate` traverses

The traversal lives in [`core/graphcache/traversal.go`](../core/graphcache/traversal.go)
(`neighborContext`), with the base BFS / spanning-tree primitives in
[`core/graph/model.go`](../core/graph/model.go). The server's `Illuminate` RPC
calls `Neighbor*`, then applies the requested reduction
(`algorithm` × `objective` × `weighting`; see [`README.md`](../README.md)).

### Index-free adjacency is the whole trick

Each vertex's out-edges are reachable by a **direct hash lookup**, not an index
probe. Under a single read lock, the walk expands a frontier and, for every tail
`t`, pulls its heads straight out of the per-tail edge bucket:

```go
heads, ok := c.edges.headsOf(t) // O(1) hash hit, then O(deg(t)) to enumerate
```

There is no per-neighbor tree descent. The cost of expanding one vertex is
$O(\deg t)$, so the cost of the whole walk is the sum of the degrees of the
vertices it actually visits — exactly what the code comment in
`neighborContext` states:

> This turns the per-call cost ... into **O(sum of degrees of visited tails)**.

$$
\sum_{t \,\in\, \text{visited}} \deg(t) \;=\; O(E_{sub})
$$

The base BFS (`ConnectedGraphContext` in `model.go`) is the textbook
$O(V_{sub} + E_{sub})$: each reachable vertex is enqueued once, each edge
visited once.

### Per-hop top-$k$ pruning

At each hop, every tail's candidate heads are scored (raw weight, or TF-IDF
re-weighting) and pruned to the $k$ edges at the objective-selected extreme
(`Top(k)` for MAX, `Bottom(k)` for MIN). Selection over a tail of degree
$\deg(t)$ is near-linear with a small selection factor, so the walk stays

$$
O\!\left(\textstyle\sum_t \deg(t)\,\log k\right) \;=\; O(E_{sub}\log k)
$$

and the pruning *shrinks* the frontier handed to the next hop, which usually
makes $E_{sub}$ far smaller than the reachable component.

At an equal-score boundary, both BFS pruning and PageRank top-$N$ retain
ascending Lantern vertex keys. That determines membership reproducibly; the
map-shaped response itself has no iteration-order guarantee.

### Optional directed-arborescence / SPT reduction

When the caller asks for `reduction=mst`, the server computes a minimum or
maximum rooted **directed arborescence** over the already-collected subgraph
with Chu–Liu/Edmonds; it does not project asymmetric Lantern edges to an
undirected graph or apply Prim. `reduction=spt` uses Dijkstra-style selection.
The worst-case arborescence complexity is:

$$
O\!\left(V_{sub} E_{sub}\right)
$$

Both are functions of the **visited subgraph only** — never of $N$ or $E$.

> The walk processes tails through a worker pool, so wall-clock time also
> benefits from parallelism, but that is a constant-factor effect and does not
> change the asymptotics above.

## The relational B-tree self-join

Model the edges as a table with a B-tree index on the tail key:

```sql
CREATE TABLE edges (tail TEXT, head TEXT, weight REAL);
CREATE INDEX idx_edges_tail ON edges (tail);
```

A $k$-hop neighborhood is then a chain of $k$ self-joins, or a recursive CTE:

```sql
WITH RECURSIVE walk(v, depth) AS (
    SELECT :seed, 0
    UNION ALL
    SELECT e.head, walk.depth + 1
    FROM walk
    JOIN edges e ON e.tail = walk.v        -- one B-tree descent per frontier row
    WHERE walk.depth < :hops
)
SELECT * FROM walk;
```

Each hop resolves the next frontier by probing `idx_edges_tail` once **per
frontier row**. A B-tree probe is $O(\log E)$ (the index is over all $E$ edges),
so the total is

$$
\sum_{\text{hop}} \big(|\text{frontier}| \cdot \log E\big)\;\approx\;O(E_{sub}\cdot \log E).
$$

The $\log E$ tree descent rides along on **every edge traversed** and never
goes away — that is the structural cost a graph adjacency list avoids.

## Side by side

| | `Illuminate` | B-tree self-join |
|---|---|---|
| Neighbor lookup | hash hit, $O(1)$ + $O(\deg)$ | index range scan, $O(\log E)$ |
| $k$-hop cost | $O(E_{sub})$ (+$\log k$ prune) | $O(E_{sub}\cdot\log E)$ |
| Depends on total size? | **No** — only $E_{sub}$ | Yes — via $\log E$ |
| Variable hop count | natural BFS loop | needs recursive CTE / $k$ static joins |
| Memory access | pointer chase, cache-local | random B-tree page descent |

## Why the gap exists

1. **The logarithmic factor.** Adjacency is index-free: a neighbor is one
   pointer/hash hop away ($O(1)$). A B-tree pays $O(\log E)$ for the same step.
   At $E = 10^9$, $\log_2 E \approx 30$ — the per-edge constant differs by an
   order of magnitude or more.
2. **Total-size independence.** `Illuminate`'s cost is bounded by the visited
   neighborhood $E_{sub}$. The relational plan stays tied to the whole table
   through $\log E$, so it gets slower as the database grows even when the
   neighborhood does not.
3. **Constant factors.** Pointer chasing through an in-memory adjacency map is
   cache-local; descending B-tree pages is random access, and intermediate join
   rows can balloon at high-degree vertices before the next join prunes them.
4. **Hop variability.** A variable or data-dependent hop count is a plain `for`
   loop over the BFS frontier in `Illuminate`; in SQL it forces a recursive CTE
   whose plan and intermediate materialization are at the optimizer's mercy.

## Worked example

Seed with branching factor $k = 6$ over $h = 3$ hops, against a store of
$E = 10^9$ edges. The visited subgraph is on the order of
$E_{sub} \approx \sum_{i=1}^{3} 6^{i} \approx 258$ edges.

- `Illuminate`: ~$E_{sub} \approx 2.6\times10^2$ pointer-hop units of work.
- B-tree self-join: ~$E_{sub}\cdot\log_2 E \approx 2.6\times10^2 \times 30
  \approx 7.7\times10^3$ units — about **30× more**, and that ratio widens as
  $E$ grows.

## Caveats — where the gap narrows or reverses

The comparison above is specifically about **multi-hop neighborhood traversal**.
The relational engine is not strictly worse; it is optimized for a different
shape of work:

- **Durability and transactions.** Lantern is in-memory and decaying by design
  (TTLs on vertices and edges); an RDBMS gives you persistence, ACID, and
  point-in-time recovery that `Illuminate` does not.
- **Set-oriented queries.** A single-hop join that touches a *large fraction* of
  the table is a sequential/hash-join job where the per-row $\log E$ never
  dominates — the relational engine shines there.
- **Arbitrary ad-hoc queries.** Secondary indexes, multi-column predicates, and
  the optimizer make the RDBMS far more flexible than a fixed adjacency walk.
- **Result-size floor.** Both engines must still produce the $O(k^{h})$ result;
  if the neighborhood is most of the graph, the $\log E$ advantage is a smaller
  slice of the total.
- **On-disk graphs.** The index-free-adjacency win assumes the adjacency map is
  in memory. A disk-resident graph reintroduces page I/O on the neighbor hop.

The takeaway is narrow and load-bearing: **for decaying, multi-hop
neighborhood queries — exactly Lantern's workload — index-free adjacency removes
the $\log E$ factor a B-tree self-join cannot avoid.**

## References

- [`core/graphcache/traversal.go`](../core/graphcache/traversal.go) —
  `neighborContext`: the hop-limited, top-$k$ neighborhood walk.
- [`core/graph/model.go`](../core/graph/model.go) — `ConnectedGraphContext`
  (BFS, $O(V+E)$) and `spanningTreeContext` (Prim, $O((V+E)\log V)$).
- [`README.md`](../README.md) — the `Illuminate` surface and the
  `algorithm` × `objective` × `weighting` reduction axes.
- [`replication.md`](replication.md), [`backup.md`](backup.md) — companion
  architecture notes.
