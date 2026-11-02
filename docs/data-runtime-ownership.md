# Data execution ownership

Implementation scope: #1987 and #1988, based on main `5641a9b3`.

## Boundary

`internal/dataruntime` owns the in-process data executor and replication
producer. Keep the graph, WAL/HLC/origin state, receipt coordinators,
publication certificates and faults, Snapshot admission/install/capture/tails,
and durable startup/recovery together. Moving just `ServingRuntime` or the
capture value types would leave two owners or require callbacks into service.

`service` owns Connect request/response wrapping, content-type and header
handling, transport interruption, and the HTTP listener lifecycle. It consumes
the executor; it cannot access its private backend, Store, locks or fault flags.
The executor keeps the existing typed protobuf operation API and error codes.
Public logical/physical mapping, authorization and Snapshot wait/recheck belong
to its public-operation entry point, outside Connect wrappers. Existing service
construction names may delegate to the executor for in-process consumers.

Dependency direction is provider -> service + dataruntime + backup/metrics;
service -> dataruntime; backup -> dataruntime; dataruntime -> core/pb and existing
lower-level internal packages. Dataruntime imports neither service nor backup.
Backup retains its small source/install/legacy consumer interfaces. Its codec
implements the runtime's baseline contract. Metrics already uses callbacks and
query observations without importing service; retain those contracts.

## Identity and publication

The executor is the single owner of the publication gate, receipt-origin cut,
Snapshot latches, pending mutations and failed frontiers. ServingRuntime retains
durable resource custody and certifies the exact installed executor, replication,
graph, Store, retired-catalog, log, HLC, origins and coordinator identities.
Do not substitute epoch/policy equality for pointer/provenance proof. Preserve
the existing production installation and receipt activation barriers, owner
lease, generation bindings and same-cut WAL witness.

A receipt mutation retains operation admission -> exclusive publication cut ->
exclusive receipt-origin cut -> Store transaction -> graph transaction -> origin
stage -> Log commit. The post-ring callback publishes Store -> graph -> origin
while Log excludes readers and the outer gates are held. Legacy graph-first
WAL failures retain the original mutation for append-only repair; indeterminate
receipt commits remain fail-stop. A closed CDC fault channel is never reused.

Baseline installation retains exclusive operation admission. Prepare sidecars
outside the publication cut and retry drift at most three times. The final cut
retains publication -> receipt-origin -> Store snapshot -> retired catalog ->
graph -> origin -> HLC -> Log boundary, with the existing callback release order
HLC -> origin -> graph -> retired -> Store. Keep rejection, panic, uncertain WAL,
committed cleanup and restart/fresh-restore behavior unchanged.

## Reads, Snapshot and callbacks

Snapshot admission/verified finish retain install custody -> exclusive
Snapshot-read latch -> checked publication gate. Recheck original authority and
context after waiting. Completion belongs to one admitted attempt; rejected or
unverified finish never clears its fault.

Public reads hold the shared Snapshot-read latch and compare fresh pointer
certificates around detached captures, with three bounded attempts then the
shared committed fallback. Never replace pointer proof with numeric generation
alone. Capture graph/receipt/retired/origin/log/HLC and the WAL witness at one
committed cut; clone returned values before releasing it. Reserve Snapshot tails
at that cut and preserve bounded lifetime and failure cleanup.

Publication observers run after gate release and cannot re-enter the owner.
Scrapes read atomic waiter counters without acquiring the publication gate.
Capture callbacks cannot re-enter the owner or retain mutable state. Preserve
existing callback ordering and resource shutdown joins.

## Delivery and verification

Review this design independently before implementation. First move the cohesive
executor and its paired white-box tests mechanically, then adapt the public
operation/transport seam and provider/backup consumers. Keep behavioral changes
out of the move. Keep wire tests at the transport boundary and focused private
fault/concurrency tests with their owner; do not export production state for
tests. Update validation consumers when test package paths change so named
selections cannot silently become empty.

Run focused concurrency/publication, WAL/recovery/fault, Snapshot/capture/tail,
backup/fresh restore and authorization/receipt regressions. Independently review
the immutable candidate, then execute the affected local closure under #1974
(full fallback where selected), required PR CI, normal squash merge, and exact
main CI. Pin supported Go and linter before expensive execution. No module,
wire/storage format, persisted compatibility, trust policy, release/deployment,
performance protocol or previous release-evidence changes are in scope.

Independent design review approved the cohesive boundary and typed `PublicAPI`
facade before implementation. Factory semantics remain unchanged: certification
selects the exact installed primary; constructors do not claim to be singletons.
Private fault/concurrency regressions move with the executor and use plain public
operations. Separate service tests exercise actual h2c boxing, namespace/error,
receipt and streaming forwarding; plain-operation tests are not wire evidence.
