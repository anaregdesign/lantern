# Generic query ranges and shared ranking statistics

Part of #1612 and the [OIDC/RBAC design](decisions/0012-oidc-prefix-rbac.md).
This Core delivery provides range execution primitives; it does not activate
OIDC or interpret Roles, Issuers, Principals or permission policies.

The Server supplies an immutable `graphcache.QueryView` with sorted, disjoint
half-open ranges in the existing projected keyspace. Core restricts Vertex
candidates and both Edge endpoints. Prefix scans/counts seek admitted intervals,
search intersects matching posting IDs before scoring/top-k, and traversal never
crosses an excluded node or Edge. Actual degree and snapshot results use that
same induced graph. Private replication continues to capture the full graph.

DF, N, document-length totals and Graph TF-IDF/BM25 weights belong to the existing
GraphCache/search-index corpus. They are reused across query ranges. Private
same-corpus application data may change visible scores/order; independent graph
instances and internal system metadata are never combined into this corpus.
There is no per-Role/Principal statistics cache, full-corpus permission walk or
permission-driven statistics invalidation. Policy compilation remains a Server
responsibility.

PPR charges physical adjacency work before range rejection to preserve the work
bound. Constrained results use stable key ordering; graph path-dependent final
scores may differ between ranges. A range is a query constraint, not proof of
authentication or serving authority.

Paired Core tests cover range isolation, shared statistics, updates/deletes/TTL,
restoration and bounded traversal. A real Connect gate covers pre-top-k filtering
and hidden-path exclusion. Paired benchmarks compare initial/reused ranges and
mixed mutation workloads with allocation/retained-memory diagnostics. The
`shared_ranking` scenario is diagnostic until full OFF/namespace-OFF/OIDC and
capacity qualification supplies comparable evidence and performance floors.
