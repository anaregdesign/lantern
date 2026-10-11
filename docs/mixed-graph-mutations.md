# Mixed graph mutations

`MutateGraph` publishes unconditional Vertex Puts followed by explicit-ID Edge
Adds in one local visibility cut, one durable receipt group and one WAL entry.
Use it to create or replace records and add their relationships together.

The call requires at least one operation of each kind. Put keys must be unique;
repeated Edge pairs are allowed with distinct, nonzero 24-byte contribution IDs.
Every Add sees the planned Put results at the same application-time sample. Both
endpoints must be live. Adds never create or extend endpoints, including in OFF
mode and during replication or recovery. Conditional Put, Delete, Create and
interleaved operation order are outside this API.

## Guarantees and conflicts

A malformed or unauthorized member, missing endpoint, capacity rejection or
other definite pre-publication failure publishes no member. Successful responses
contain aligned typed Put outcomes and Add effective weights. Current authority
may require whole-call blind acceptance instead of detailed results.

The guarantee is local atomic publication with asynchronous, per-effect HA
conflict resolution. A concurrent newer Vertex can supersede one Put while both
origins' Add contributions survive. Original receipt results remain fixed.
Replicas do not commit simultaneously; there is no global serializability,
cluster-wide invariant transaction, quorum on this data path or 2PC. `sys:*`
authority and `data:*` HLC replication retain their separate protocols.

A receiver records its accepted local projection for recovery, while preserving
origin intent, results and effects for relay. A relay must not erase an origin
Put merely because that Put lost locally. Restart replays the recorded projection
rather than reconsidering omitted effects against a changed graph.

## Capability and retry identity

Check `GetReceiptCapability.graph_mutation`. Independent Put and Add capability
flags alone do not imply mixed support. A certified durable receipt runtime is
required. Recovering, unsupported and faulted runtimes refuse the call.

Persist the complete request, receipt context and contribution IDs before the
first write. After an ambiguous error, query the original receipt IDs. An absence
on another replica is not permission to execute a fresh call. Only reuse the exact
request and identity against the same valid endpoint generation; a continuity
change requires reconciliation. Never split an uncertain mixed call into separate
writes or mint new IDs to retry it. An authorization/output failure can occur
after commit, so that failure alone is not evidence of an abort.

The Go SDK exposes `NewGraphMutationReceiptContext` and `MutateGraph`. It checks
endpoint continuity and makes one mutation RPC, without automatic retry,
chunking or failover. Other maintained clients have regenerated wire types;
additional convenience and offline replay APIs are outside this change.

```go
capability, err := lantern.GetReceiptCapability(ctx)
if err != nil { return err }
identity, err := lantern.NewGraphMutationReceiptContext(capability, 3)
if err != nil { return err }
contribution, err := lantern.NewContribID()
if err != nil { return err }
vertices := []client.VertexInput{
    {Key: "orders:42", Value: "ready"},
    {Key: "customers:7", Value: "customer"},
}
edges := []client.EdgeAddReceiptInput{{
    Edge: client.EdgeInput{Tail: "orders:42", Head: "customers:7", Weight: 1},
    ContribID: contribution,
}}
// Durably save identity, vertices and edges in the application's own storage
// here, before sending. Retain them if the outcome is uncertain.
result, err := lantern.MutateGraph(ctx, vertices, edges, identity)
```

`TestGraphMutationGoSDKRealConnectAndRestart` exercises this facade over real
Connect transport, including changed-intent rejection, response loss/status,
backup staging and restart. The current native security gate also exercises the
facade through production ingress; the focused transport fixture does not
substitute for that gate.

## Bounds and operational compatibility

The advertised limits are 64 total operations and 1 MiB for the complete request.
The complete committed envelope also has a 1 MiB limit; metadata and worst-case
results are checked before staging. Smaller transport and value limits still
apply. Planning can refuse a request within those input limits when it would
exceed any of these internal bounds:

- 4,096 retained contribution rows across touched Edge buckets, counted before
  cloning, including retained expired rows.
- 8 MiB of accounted owned preparation, payload, plan and undo scratch. Search
  reserves conservative old/new projection and analysis bounds before analysis;
  this is not an exact heap/RSS limit.
- Two re-preparation retries, one admitted local mixed call without a waiter
  queue, a one-second pre-WAL budget and 25 ms of exclusive pre-WAL planning.

The budgets are checked at safe points. They do not bound scheduler delays,
mutex waits or fsync. Once WAL is attempted, the existing durable-unknown
fail-stop/recovery protocol takes precedence over cancellation. Receiver capacity
failure stalls the complete envelope and leaves its origin prefix unchanged.
Eventual progress requires delivery, retained evidence and sufficient capacity.

New writers use receipt WAL union V10. The reader accepts frozen V9 kinds with
unchanged semantics and rejects the mixed kind or new tags disguised as V9.
Older readers refuse V10. Upgrade private peers before first use: full Subscribe
consumers must explicitly set `accept_graph_mutations` as well as
`accept_receipt_envelopes`. An unsupported peer may lag until upgraded; the server
never splits a mixed envelope for it. V4 Snapshot/archive state shape remains
unchanged and supports heterogeneous receipt rows in one coherent cut.

WatchChanges remains an invalidation stream. Filtering hides private members and
positions; only the final visible frame carries a resumable cursor. Hidden-only
progress appears on the normal heartbeat. VALUE mode returns a current authorized
image. Clients can refetch between frames, so no atomic client-cache update or
transaction grouping API is promised.
