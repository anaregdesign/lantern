# Orderly current-authority custody

This Compose example documents the #1727/#1609 mount and lifecycle contract. It
is not a provisioning tool, deployment approval, legacy migration or recovery
procedure. The [current authority contract](../../docs/security-current-authority.md#orderly-floor-custody-and-process-restart)
defines the native time, identity, quorum and independent data prerequisites.

Before using the example, provide an approved immutable image and independently
provisioned current-v2 cohort. Each `operator/node-N` directory contains its
original canonical `node.json`, the exact genesis bytes, signed membership and
operator public key, that node's separate workload TLS/voting/origin key material,
public TLS files, and the IdP trust roots referenced by the configuration. An
origin-free member has no origin key. Preserve the original member/incarnation,
scope, identities, file bytes and paths across normal restart. No script here
issues production secrets or changes DNS, host trust, host time or deployments.

Use fixed absolute paths inside the node document: inputs beneath `/operator`,
M at `/journal/membership`, P at `/journal/protocol.wal`, B at
`/journal/materialized.wal`, and `FloorsFile` at `/custody/floors.json`. The private
listen port and signed workload origins must agree, with actual node names and
certificates valid for the selected network. Mount each node's own original
inputs read-only. Pre-create its private journal and custody directories with
the configured UID/GID and restrictive permissions; do not let Compose silently
create missing operator directories or key files. Files must be private regular
files. Symlink/hardlink aliases across custody and other inputs are rejected.

The sample data WAL uses `/journal/business.wal` and independent fixed node IDs,
receipt epoch and limits. Those sample values must match the independently chosen
data contract. Sys CLEAN does not certify data persistence, replication or
enforcement. This example does not configure the separate data workload peer
plane; use its existing provisioning contract if data HA is required.

For initial startup set `LANTERN_SYS_MODE=fresh` and `LANTERN_DATA_MODE=fresh`, with
all native family and custody bodies absent. The runtime records RUNNING before
opening update-capable M/P/B state. A surviving `.state.lease` file alone is
expected and must not be unlinked. Wait for actual current and data readiness;
successful process creation or existing floor files are not readiness.

For normal stop, use SIGTERM with enough time for readiness drain, public/private
handler shutdown, native producer joins, all journal/resource closes, and both
durable checkpoint writes. The sample allows 90 seconds around a five-second
drain and thirty-second public HTTP shutdown. This is a budget, not a promise
that every storage device completes within it. The service manager's overall
stop deadline must leave room for all these steps. Inspect each process's exit
status and `server stopped cleanly` log. Each custody pair must show CLEAN with
the same cycle/binding and SHA-256 of the exact final floor bytes. Do not regard
`docker stop` returning, or a stopped container, as sufficient evidence.

For an intact restart, retain all original mounts and keys, set
`LANTERN_SYS_MODE=resume` and `LANTERN_DATA_MODE=restart`, then start the processes.
Every successful startup consumes CLEAN into a new RUNNING cycle. A new native
anchor, valid membership/TLS, quorum/current authority and the independent data
readiness are required before protected operations resume. A lone restarted
member stays unavailable even with intact CLEAN/floors. Existing durable sessions
retain their normal expiry/current-policy checks; old callback, purpose and output
permits are process-local and do not survive.

On refusal, preserve the original files and logs. RUNNING, a missing/mixed pair,
a mismatched hash/binding, invalid canonical document/permissions, a missing
journal/tip, or a family behind an independently retained floor requires diagnosis.
Do not switch to fresh, delete sidecars, rewrite provisioning, or manufacture
new floors from the suspect local journals. Initialization/worker failure,
deadline expiry, panic and pre-checkpoint kill do not create CLEAN. A final CLEAN
publication can report an uncertain rename/directory-sync result even though
complete CLEAN bytes are visible later; the next startup may use only a fully
matching pair and still performs all ordinary native validation. The failed
process itself never resumes service.

Normal OS restart availability additionally depends on the native kernel/storage
contract and intact persistent state. The focused container gate is evidence of
whole-process lifecycle, not physical reboot or power-loss flushing. Backup/VM
rollback/clone return to the same cluster, same-cluster recovery without a normal
checkpoint, actual legacy cutover and production deployment remain outside this
example. A separate volume is not an external monotonic witness against rolling
back the whole bundle.

The bounded native fixture is
[`current_custody_container.py`](../../.github/scripts/current_custody_container.py).
It takes already built immutable server/security-test/integration-test binaries,
their exact source HEAD/tree, a cached immutable runtime image, and the existing
selected native source configuration. It creates only new labeled task networks
and private volumes, uses real public/private TLS, restarts three product
processes twice, checks communication loss/recovery and isolated resume, and
proves a pre-CLEAN kill is refused. Paired tests cover persistence and cleanup
faults, malformed/aliased custody and independent native-floor failures. Original
logs and private fixture volumes are retained; no production secrets, image pulls
or host configuration changes are part of that campaign.
