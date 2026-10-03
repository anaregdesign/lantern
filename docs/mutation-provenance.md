# Generic mutation effects and receipt provenance

This Core delivery tracks #1603/#1613. It extends the internal primitives needed
by Server authorization without teaching Core OIDC, Roles, namespaces or
permission policies. Server adapters and durable wire formats are separate
dependent deliveries.

Vertex/Edge batch items may carry a generic restriction against reducing a live
resource's lifetime. The planner checks only touched resources under the same
atomic storage lock and application-time sample, simulates request-order
duplicates and rejects before staged effects. A born-expired overwrite is a
reduction; shortening a live resource is a reduction. Unrestricted calls retain
the existing behavior and add no restriction-map allocation.

Receipt-aware Vertex Put can capture the actual original reduction bit for
accepted positions. Core stores that bit on reserved receipt rows before
staging and preserves it through immutable duplicates, snapshots, retirement
and dominance checks. Replay never recomputes the original effect from the
current graph. Server decides which permissions that original effect requires.

An Intent may also retain a bounded opaque exact resource identity: Vertex key
or Edge tail/head. The zero value denotes unproven legacy provenance, not an
all-keys grant. Core validates shape/UTF-8/size, owns the strings and includes
provenance in logical byte caps and duplicate/restore comparisons. Public
receipt responses do not acquire these private fields automatically.

Paired tests cover atomic reductions/duplicates, receipt provenance, owned
bytes, capacity, replay/snapshot/retired state and immutable effect flags.
Complete Server RPC authorization, native WAL/replication/archive formats and
real Connect result disclosure tests remain #1603/#1613 work. This layer does
not activate a new public mutation family.
