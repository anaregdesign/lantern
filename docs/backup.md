# Snapshot backup & restore

> Tracking: [#769](https://github.com/anaregdesign/lantern/issues/769) (epic),
> [#770](https://github.com/anaregdesign/lantern/issues/770) (engine),
> [#771](https://github.com/anaregdesign/lantern/issues/771) (this doc), and
> [#1394](https://github.com/anaregdesign/lantern/issues/1394) (durable
> receipt backup/restore).

Lantern is an **in-memory** store, so a single instance loses its whole graph
on any restart — including a routine **rolling update** or pod restart. In
graph-only mode, snapshot durability periodically dumps the graph to a mounted
volume and restores the newest dump before serving. Durable
receipt-WAL mode uses the same production schedule for a receipt-bearing
backup set and restores it only at the durable runtime construction boundary,
under the FileWAL lease and before any listener or background worker exists.

The graph-only restore path is primarily the **single-instance** durability
story — any single-pod or single-container deploy. In a **multi-replica**
graph-only cluster restore still runs on boot as a **baseline**:
the restarted pod replays its newest dump, then peer **bootstrap** (snapshot +
tail, see [replication.md](replication.md)) overlays it through the write path,
so HLC ordering lets newer peer state win per key — replicas take priority, the
dump only fills gaps, and a whole-cluster cold start recovers from the dumps
instead of coming up empty.

The historical path is **snapshot-based** durability. The durable receipt path
pairs a receipt-bearing snapshot with exact FileWAL cut/tip evidence. Neither
changes a leaderless-replication invariant.

## Graph-only mode

- **Dump** drives the same `BackupSnapshot` surface the CLI `lantern-cli dump`
  uses — a whole-graph, point-in-time snapshot taken under one lock — and
  writes it as length-delimited protobuf. The record format matches
  `lantern-cli dump --format proto`, but CLI restore replays edges through
  public `PutEdges`, which accepts only finite input weights. Finite-weight
  records can be restored either way; a dump with an edge aggregate folded
  to ±Infinity by earlier finite Adds (or a historical NaN weight) requires
  the server's internal graph-only startup restore, not `lantern-cli restore`.
- **Restore** replays the newest valid dump through the internal zero-HLC
  write path, so absolute expirations and HLC ordering are honoured; an
  entry whose TTL has already elapsed since the dump is **not** resurrected.
- **Derived search recovery** marks the index `INCOMPLETE` before replay,
  restores the live graph, then rebuilds the exact index before it can become
  `HEALTHY`. A bounded rebuild failure leaves graph restore intact but search
  fails closed as `INCOMPLETE`; `DISABLED` remains distinct.
- **Restore accounting** counts only index-aligned
  `PUT_OUTCOME_APPLIED_AND_LIVE` results from `PutVertices` / `PutEdges`, not
  input frame counts. Consequently `lantern_restore_vertices`,
  `lantern_restore_edges`, and restore logs exclude expired or causally
  superseded rows.
- **Per-instance files.** Each writer owns files named
  `lantern-backup-<instance>-<nanos>.lbk` (`instance` defaults to the
  hostname). Writes are atomic (temp file + `rename`); a half-written file is
  never a restore candidate. Restore picks the **newest valid** file and skips
  a corrupt/truncated one for the next-newest. Retention deletes only an
  instance's **own** files.

The `.lbk` format contains graph records only. It does not preserve mutation
receipts, contribution identities, origin cutoffs, or receipt clock
high-water, so it cannot prove receipt continuity after restore. Durable
receipt backup sets use a separate private format and are not CLI restore
inputs.

## Durable receipt-WAL mode

With `LANTERN_RECEIPT_WAL_MODE=fresh|restart`, the same scheduler and
`BackupNow` path produce a receipt backup set instead of an `.lbk`.
The source is selected only from the exact certified `ServingRuntime` and
captures the graph, active receipt Store, runtime-owned retired receipt
catalog, origin frontier, HLC cutoff, live FileWAL tip witness, stable NodeID,
and active endpoint generation under one exclusive committed view. Each
attempt invokes that combined source exactly once and never reopens the live
appendable WAL path.

A receipt backup set has deterministic, unversioned, instance-scoped names:

```text
lantern-receipt-backup-<sha256(instance)>-<20-digit-set-id>.active.lar
lantern-receipt-backup-<sha256(instance)>-<20-digit-set-id>.active.walcut
lantern-receipt-backup-<sha256(instance)>-<20-digit-set-id>.retired.lret
lantern-receipt-backup-<sha256(instance)>-<20-digit-set-id>.set.json
```

The canonical JSON `.set.json` manifest uses internal schema version 1 and is
the commit marker. It binds the exact three ordered member names, formats,
versions, byte sizes, and SHA-256 digests:

1. the existing canonical active graph + Store + origin + HLC archive;
2. the exact canonical `LANTWCUT` version 1 lease-owning FileWAL cut/tip
   witness captured with that archive;
3. the canonical `LANTRET1` retired catalog captured at the same active Store
   clock high-water.

The manifest also commits the instance token, monotonic set ID, UTC backup
timestamp, NodeID, active endpoint generation-chain head, active epoch and
policy fingerprint, retired-policy-set fingerprint, exact receipt clock
high-water, snapshot HLC/local sequence, origin cutoff digest, and complete
WAL frontier. A domain-separated publication commitment covers that metadata
and every ordered member identity. The loader independently decodes each
bounded member, requires canonical bytes, and cross-checks all of that
metadata. It rejects unknown, duplicate, missing, reordered, unsafe,
malformed, noncanonical, trailing, digest-mismatched, size-mismatched, stale,
or cross-cut data. Its fully validated result owns both archive members and
the parsed WAL cut/tip witness, so a later restore layer does not need to
reopen a member or the live WAL.

Startup restore discovery is deliberately stricter than retention collection.
`LoadLatestReceiptBackupSet` enumerates only the configured backup directory,
recognizes canonical current-format `.set.json` names in the requested
instance scope, and recognizes exact owned markers in the obsolete
`lantern-receipt-backup-v1-...` and `lantern-receipt-backup-v2-...`
namespaces only to reject them as unsupported. Discovery chooses the highest
recognized nonzero 20-digit set ID once, then either rejects a selected
obsolete marker or delegates to the canonical set loader. If no such marker
exists it returns
`ErrReceiptBackupSetNotFound`. Once a newest marker is selected, any
unsupported-format, marker, or member validation error fails closed; discovery
never scans backward to an older valid set. Foreign scopes, unrecognized names,
temporary files, and orphan members are not committed-set candidates.
Discovery is read-only and returns validated evidence only; it does not install
that evidence into a runtime.

Before publication, every configured backup-directory component is inspected
without following intermediate symlinks, and every missing component is created
individually. The nearest existing ancestor's parent is synced first so a retry
can certify an entry left by a failed earlier parent sync, and each new child
entry is then made crash-durable by syncing its parent. Directory flushes use
the platform durability primitive, including a write-capable directory handle
with `FlushFileBuffers` on Windows. Persistence creates each member staging
file with `O_CREATE|O_EXCL`; writes all bytes, file-syncs, closes, and renames
it to its final immutable name; directory-syncs after all three member renames;
then creates/writes/file-syncs/closes the manifest staging file, renames the
manifest last as the sole commit point, and directory-syncs again.
An existing observed staging or final name is never replaced. Failed attempts
clean only paths whose exclusive create or successful rename proved ownership;
files left by an interrupted process or racing writer are preserved and
ignored rather than guessed-owned. Once the manifest rename is attempted, a
rename error is publication-ambiguous: the operation returns that error but
preserves all final members unless it owned and removed the marker and synced
the marker's absence. This can leave bounded member orphans, but can never
leave a committed marker without its members.

All receipt-set directory walks stream fixed-size batches and fail after
100,000 entries, counting foreign and orphan names toward that explicit
resource bound. Discovery and ID allocation retain only the highest relevant
ID. Retention keeps a bounded min-heap of the newest valid own sets, then uses
a bounded second streaming pass to collect older candidates. It closes the
directory stream, revalidates each candidate, removes its manifest first,
directory-syncs, removes its members, and directory-syncs again. It never
prunes invalid markers, legacy markers, other instances, or unproven orphan
files.
`LANTERN_BACKUP_RETAIN=0` keeps all sets.
Periodic, manual, and final-shutdown attempts are serialized, and set IDs stay
unique and increasing even if wall time repeats or moves backward.
Existing `lantern_backup_*` timing, failure, vertex, and edge metrics remain
the common production signals. Durable sets additionally publish
`lantern_backup_receipts`, `lantern_backup_origins`,
`lantern_backup_set_members`, and `lantern_backup_set_bytes`; completion logs
retain the existing `backup: wrote dump` event and add set, identity, member,
and byte fields.

The unversioned receipt backup set is the only supported receipt backup-set
format. The producer
requires exact active/retired clock high-water equality and preserves a
nonempty retired catalog rather than discarding or resampling it. Legacy set
formats receive only bounded envelope/name recognition so they fail with
`ErrUnsupportedReceiptBackupSet`; no obsolete member is decoded, installed,
or migrated. A selected set containing the obsolete `LRWLCUT2` witness format
fails with the same unsupported-set error. Runtime-local combined baseline
publication remains the separate private `LANTCBLN` schema-1 sidecar, and
graph-only `.lbk` remains a separate non-receipt contract.

Durable startup restore consumes only the strict newest-set loader above. It
does not call the graph-only `Backupper.RestoreOnStartup` path:

- **`restart` first proves the current runtime.** A complete current WAL,
  journals, generation chain, and committed combined baseline always win;
  the backup is not read, even when restore is required. Backup fallback is
  limited to a missing or damaged sidecar for the newest otherwise-valid
  committed baseline. Under the same FileWAL lease it requires the backup's
  exact NodeID, epoch, policy, generation at the archived cut, graph/archive
  cutoff, WAL offset/digest/rolling-chain witness, and the complete valid
  current suffix. Lease contention, identity or policy mismatch, journal
  failure, ambiguous WAL bytes, a later valid generation, or any other
  unclassified current state fail without fallback. The repaired graph,
  active Store, retired catalog, origins, HLC floor, Log, WAL, and generation
  are installed as one runtime, then a fresh canonical `LANTCBLN` schema-1
  baseline is committed before certification.
- **`fresh` is the total-cluster-loss path.** The configured active epoch must
  differ from the archived active epoch, and existing target bytes still fail
  closed. The configured epoch starts with an empty active Store and a new
  endpoint generation. The archived graph and origins are restored; the
  archived active Store is converted with its original policy into one
  retired-epoch member and unioned deterministically with `LANTRET1`.
  Configured aggregate bounds are charged against the distinct raw union
  before rows expired at the effective high-water are pruned. A canonical
  combined baseline commits before certification.
- **Optional restore is narrow.** With `fresh`, a genuinely absent backup may
  start a complete empty fresh runtime; an invalid selected backup never does.
  With `restart`, an absent backup cannot repair an incomplete current
  runtime, so startup fails. `LANTERN_BACKUP_RESTORE_REQUIRED=true` makes a
  missing fresh backup terminal and is invalid unless restore-on-start is
  enabled. A valid current restart needs no backup.

The generation record remembers when a fresh runtime requires its first
startup-restore baseline. A crash before that marker commits cannot later
turn the empty target into a valid restart. The private identity-bearing
restore barrier commits any pending baseline before `NewRuntimeCertified`;
Snapshot installer, Pump, anti-entropy, and backup construction all depend on
that certification. After the exact production backup source is certified, a
final barrier enables receipt capability, three-state status, and the optional
Vertex Put, exact Vertex Delete, exact Edge Delete, and contribution-keyed Edge
Add receipt contexts only when bearer authentication is configured.
Graph-only, auth-disabled, recovering, faulted, or uncertified deployments
remain fail-closed. Rotating configured bearer tokens does not change receipt
epoch, policy, endpoint generation, or namespace.

### Why per-instance files (the shared-storage decision)

The volume can be shared across replicas, but **none** of the relevant backends
offer safe concurrent-write coordination:

| Backend | Shared? | Concurrent-write safety |
|---|---|---|
| Object storage via FUSE (e.g. GCS, S3) | yes | **no file locking**, last-write-wins, not POSIX; mount latency can stall boot |
| Network file share (NFS / SMB) | yes | mounted **no-lock**; multi-writer coordination is the app's job |
| Per-pod block volume (RWO) | no (pod-scoped) | not shared — safe by construction |
| EmptyDir / tmpfs | no (pod-scoped, ephemeral) | n/a |

So Lantern **never** writes a shared single file and **never** does leader
election — per-instance filenames make concurrent writes collision-free on
every backend, and degrade cleanly to the single-instance case.

## Configuration

| Env var | Default | Meaning |
|---|---|---|
| `LANTERN_BACKUP_ENABLED` | `false` | Master switch for periodic production. Requires `LANTERN_BACKUP_DIR`; writes graph-only `.lbk` files or durable receipt sets according to runtime mode. |
| `LANTERN_BACKUP_DIR` | _(empty)_ | Mounted directory backup files are written to and startup restore reads from. |
| `LANTERN_BACKUP_INTERVAL` | `5m` | Backup cadence (`time.ParseDuration`). |
| `LANTERN_BACKUP_RETAIN` | `3` | Keep newest N valid own dumps/sets; `0` keeps all. |
| `LANTERN_BACKUP_INSTANCE_ID` | _(hostname)_ | Per-instance ownership token used to derive safe filenames. |
| `LANTERN_BACKUP_RESTORE_ON_START` | `true` | Graph-only: replay the newest valid dump. Durable `fresh`: restore the strict newest receipt set or, only when optional and absent, start empty. Durable `restart`: use a set only for eligible current-baseline damage. |
| `LANTERN_BACKUP_RESTORE_REQUIRED` | `false` | Graph-only: fail boot when restore errors. Durable `fresh`: require a valid newest set. Durable `restart`: restore failure is always terminal when the current runtime is incomplete; a valid current runtime remains authoritative. |

> **TTL-vs-interval caveat.** Entries decay, so a dump is only as useful as its
> data is still live at restore time. Keep `LANTERN_DEFAULT_TTL_SECONDS`
> comfortably **above** `LANTERN_BACKUP_INTERVAL` (and above your expected
> restart gap) or much of a restored graph may already be expired.

## Durability via a mounted volume

The feature works on any platform that can mount a directory which survives
container/pod restarts at `LANTERN_BACKUP_DIR`. Point the env vars above at
that path — the server then produces backups periodically. Graph-only mode
also restores the newest dump on boot:

```bash
LANTERN_BACKUP_ENABLED=true
LANTERN_BACKUP_DIR=/data
LANTERN_BACKUP_INTERVAL=5m
LANTERN_DEFAULT_TTL_SECONDS=86400
```

- **Kubernetes** — the [Helm chart](../deploy/helm/lantern/) renders a per-pod
  `ReadWriteOnce` PVC mounted at `LANTERN_BACKUP_DIR` (`backup.persistence`, on
  by default). Set `backup.persistence.storageClass` or provide a cluster
  default StorageClass. Each pod keeps its own dumps; for a single shared dump
  volume point `backup.persistence.existingClaim` at a pre-provisioned RWX claim.
- **Docker Compose / single host** — bind-mount a host directory (see the
  runnable example below).
- **Shared / networked volumes** (NFS, SMB, object-storage FUSE) also work:
  per-instance filenames keep concurrent writers collision-free, but such
  backends offer **no file locking**, so never rely on cross-writer
  coordination, and high mount latency can stall boot — prefer a local/block
  volume when restore time matters.

`EmptyDir` / `tmpfs` are pod-scoped and ephemeral — **don't** use them for
durability.

## Docker Compose (local)

A runnable single-instance example lives at
[deploy/compose/docker-compose.backup.yml](../deploy/compose/docker-compose.backup.yml):

```bash
cd deploy/compose
docker compose -f docker-compose.backup.yml up -d
go run ../../cli put vertex hello world
# wait one LANTERN_BACKUP_INTERVAL, then:
docker compose -f docker-compose.backup.yml down
docker compose -f docker-compose.backup.yml up -d   # restores on boot
go run ../../cli get vertex hello                    # still there
```

## Helm

The chart's `backup` values wire the env + a per-pod PVC mounted at `backup.dir`
(see [deploy/helm/lantern/values.yaml](../deploy/helm/lantern/values.yaml)):

```yaml
backup:
  enabled: true
  dir: /var/lib/lantern/backups
  interval: 5m
  retain: 3
  restoreOnStart: true
  persistence:
    enabled: true
    size: 1Gi
    # or: existingClaim: my-backup-pvc
```

In a multi-replica StatefulSet each pod's `LANTERN_BACKUP_INSTANCE_ID` is its
stable pod name, so dumps never collide. Restore-on-start still runs on each
pod as a baseline; peer bootstrap then overlays newer cluster state via HLC, so
replicas take priority while a whole-cluster cold start recovers from the dumps.
In durable receipt-WAL mode, each stable instance ID selects only its own
strict newest receipt set. Use `fresh` with a new configured epoch for
total-cluster recovery, or `restart` for same-epoch current-WAL recovery and
its narrowly eligible baseline-sidecar repair.

## See also

- [docs/ha-runbook.md](ha-runbook.md) — rolling-upgrade drain (§7) and HA ops.
- [docs/replication.md](replication.md) — the multi-replica peer-bootstrap recovery
  path and the deployment-topology matrix (D7).
- `lantern-cli dump` / `lantern-cli restore` — the on-demand, file-compatible
  CLI half of the graph-only `.lbk` format. They do not consume durable receipt
  backup sets.
