# Staged mutation lifecycle (#1667)

Baseline: `93d537892f3025d4a1666dcab36c5748d5cfb9f6`. This refactor preserves
existing mutation contracts; it adds no public transaction API or mixed batch.

## Ownership and phases

Every graph transaction has one goroutine owner. The owner must resolve it
before reentering the same GraphCache. Results are detached from the journal.
Abort is idempotent, including after Commit. Commit after close panics for
graph transactions. Planning, accepted projections, results, dictionary/index
undo, TTL/HLC barriers and contribution identity remain operation-specific.

| Path | Locks held after successful preparation | Prepared state | Commit prerequisite |
| --- | --- | --- | --- |
| Edge Delete, contribution Delete | `mu -> publicationGate` | Prepare computes undo/results without applying; Begin also calls Apply | Apply required, including an empty projection |
| Edge Add | `mu -> publicationGate -> searchCommitMu` | Begin applies hidden Edge and endpoint/search effects | Applied |
| Edge Create/replay | `mu -> publicationGate` | Begin applies hidden Edge effects; endpoints unchanged | Open, including no effect |
| Vertex Put/Delete | `mu -> publicationGate -> searchCommitMu` | Begin applies hidden effects when accepted | Open; empty/no-effect stages valid with `applied=false` |
| SystemMetadata | Independent metadata mutex | Prepared next image | Commit installs image then unlocks; repeated close is harmless |

User projection/document callbacks execute outside exclusive graph locks.
Preparation retries preserve the existing lock order. Graph rollback runs
before release; locks release in reverse acquisition order. A preparation or
Apply panic uses each operation's existing sparse rollback and cleanup path.
SystemMetadata does not share the graph lifecycle or release callback.

Receipt commits acquire service publication, receipt origin cut, Store,
GraphCache, origin tracker, then Log. They stage all fallible changes before
entering WAL. The log installs WAL then ring/sequence; its post-ring callback
releases Store, GraphCache, then origin, while Log readers are excluded. All
three release before dispatcher backpressure; the outer service gates remain
held. The callback performs no new graph mutation. SystemMetadata instead
uses `CommitWithPublication`: WAL, image installation, then ring.

Cancellation is checked at the existing typed-adapter boundaries. After the
WAL call starts, cancellation/timeouts do not certify abort. A definite WAL
abort rolls back open transactions. Other errors and panics retain existing
log/service fail-stop classification and recovery requirements. The refactor
does not reinterpret error identities or change family-specific Connect codes.
Replicated adapters retain `pending.receiptWAL`, original result bytes and
receiver-local projections; they restore HLC floors only at existing points.

Standalone Create is staged even without receipts when Log/Clock exist.
Legacy graph-only Put/Add/Delete stay graph-first, retaining `pending.applied`
and exact retry evidence after failed append. They are not converted to
receipt atomicity. Whole-state capture retains graph, receipts, retired
catalog, origins, Log position, HLC and the exact durable-tip witness.

## Measurement plan and qualification

Predeclared diagnostic comparison: same Go toolchain, host, benchmark selector,
`-benchtime=100x -count=3 -benchmem`; compare medians, no selected samples.
Budget: no added allocations/op, at most 5% bytes/op and 5% ns/op increase.
Timing failures require investigation; shared-host results cannot qualify
production latency or throughput. The short iteration count bounds retained
receipt fixtures and is identical for baseline/final.

```sh
go test ./core/graphcache -run '^$' -bench 'Benchmark(EdgeCreateTransaction|EdgeDeleteTransactionAbort|GraphCacheStagingCosts|PutVertex_Search|AddEdges_ExistingBatch)$' -benchmem -benchtime=100x -count=3
go test ./server/service -run '^$' -bench 'BenchmarkPublicReceipt(VertexPut|VertexDelete|EdgeAdd|EdgeDelete)Admission$' -benchmem -benchtime=100x -count=3
```

The development host is Darwin arm64, Go 1.27.0, shared with other work. A
writable isolated Go cache is required in the restricted execution environment.
Baseline/final raw logs and hashes are retained with the task evidence.

The one declared diagnostic comparison completed for 19 benchmark cases:
allocations/op were unchanged in every case, maximum median bytes/op increase
was 0.014%, and maximum median time increase was 4.76% (Edge Delete Abort).
All diagnostic medians were within the predeclared budgets. Raw
[baseline/final logs and comparison](evidence/staged-mutation-lifecycle/) and
[SHA-256 manifest](evidence/staged-mutation-lifecycle/sha256.json) retain all
three repetitions. These shared-host observations are not final qualification.

Final quiet-host measurement remains pending: matched exact-source/image
search-enabled batch/receipt workloads, throughput, latency, allocations,
lock hold/writer delay, relay lag, retained and peak memory. Existing workload
floors remain unchanged. Native HA partition/rejoin and final-stack evidence
remain pending independently of unit, held-WAL, replay, crash/reopen and
Snapshot+tail tests. Coordinate final evidence with #1610; historical source
or unit success does not close these exits.

## Characterization and implementation proof

The added paired GraphCache transaction tests passed on unchanged production
source before extraction and on the refactor. They cover local and replicated
Delete/contribution Prepare, Apply, Begin, pre-Apply Commit rejection followed
by Abort, detached results, nil/double close, and empty/no-effect Vertex
Commit/Abort with `applied=false`. `staged_lifecycle_test.go` checks ownership
and both lock sets during rollback panic cleanup. Add retains its original
Edge-rollback-before-Vertex-cleanup boundary.

`staged_publication_test.go` uses real Store, graph, origin and Log stages to
check Store-before-graph-before-origin release, continued outer gate ownership,
panic before WAL, interrupted release after ring installation, and panic after
the successful commit call returns. Partial publication is unavailable through
the service receipt view. `create_edges_test.go` covers receipt-present and
receipt-absent definite abort/retry, indeterminate outcome, WAL panic,
cancellation before WAL, and cancellation inside a successful WAL call.

Existing selected tests retain original receipt/WAL bytes, held-WAL graph and
whole-cut visibility, exact relay retry evidence, capacity recovery, multi-hop
Vertex effects, crash/reopen, current-format rejection and Snapshot/durable-tip
capture. Legacy graph-first retry tests and Core post-ring dispatcher tests
remain part of validation. SystemMetadata and its independent journal were
not routed through the shared release callback.
