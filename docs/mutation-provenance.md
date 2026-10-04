# Generic mutation effects and receipt provenance

The implementation tracks #1603/#1613. Core owns generic atomic lifetime
restrictions and immutable receipt evidence without interpreting OIDC, Roles,
namespaces or permission policies. Server resolves the verified Principal and
current Role policy, maps public logical keys to physical `data:` resources,
and authorizes exact actions before effects or original-result disclosure.
See [ADR 0012](decisions/0012-oidc-prefix-rbac.md) and
[ADR 0010](decisions/0010-bounded-mutation-receipts.md) for the complete contract.

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
Server adapters carry original logical resource/action evidence through native
WAL, private replication, snapshots, retirement and recovery. Real Connect tests
cover exact admission, response-loss replay/status, unknown scoped receipt IDs
and permission loss. Public CDC does not disclose private receipt envelopes.
Final immutable-source acceptance remains #1610. Conditional existing-endpoint
Create is a distinct #1626 family; provenance alone does not activate it.
