# Snapshot backup & restore

> Tracking: [#769](https://github.com/anaregdesign/lantern/issues/769) (epic),
> [#770](https://github.com/anaregdesign/lantern/issues/770) (engine),
> [#771](https://github.com/anaregdesign/lantern/issues/771) (this doc), and
> [#1394](https://github.com/anaregdesign/lantern/issues/1394) (durable
> receipt backup/restore).

Lantern is an **in-memory** store, so a single instance loses its whole graph
on any restart — including a routine **rolling update** or pod restart. In
graph-only mode, snapshot durability periodically dumps the graph to a mounted
volume and restores the newest dump before serving. Private durable
receipt-WAL mode uses the same production schedule for a receipt-bearing
backup set; startup restore for that set is a later layer.

The graph-only restore path is primarily the **single-instance** durability
story — any single-pod or single-container deploy. In a **multi-replica**
graph-only cluster restore still runs on boot as a **baseline**:
the restarted pod replays its newest dump, then peer **bootstrap** (snapshot +
tail, see [replication.md](replication.md)) overlays it through the write path,
so HLC ordering lets newer peer state win per key — replicas take priority, the
dump only fills gaps, and a whole-cluster cold start recovers from the dumps
instead of coming up empty.

The historical path is **snapshot-based** durability. The private durable path
pairs a receipt-bearing snapshot with exact FileWAL cut/tip evidence. Neither
changes a leaderless-replication invariant.

## Graph-only mode

- **Dump** drives the same `BackupSnapshot` surface the CLI `lantern-cli dump`
  uses — a whole-graph, point-in-time snapshot taken under one lock — and
  writes it as length-delimited protobuf. The on-disk format is **identical**
  to `lantern-cli dump --format proto`, so a server-written dump loads with
  `lantern-cli restore` and vice-versa.
- **Restore** replays the newest valid dump through the normal write path, so
  absolute expirations and HLC ordering are honoured; an entry whose TTL has
  already elapsed since the dump is **not** resurrected.
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
high-water, so it cannot prove receipt continuity after restore.

## Private durable receipt-WAL mode

With `LANTERN_RECEIPT_WAL_MODE=fresh|restart`, the same scheduler and
`BackupNow` path produce a private receipt backup set instead of an `.lbk`.
The source is selected only from the exact certified `ServingRuntime` and
captures the graph, active receipt Store, runtime-owned retired receipt
catalog, origin frontier, HLC cutoff, live FileWAL tip witness, stable NodeID,
and active endpoint generation under one exclusive committed view. Each
attempt invokes that combined source exactly once and never reopens the live
appendable WAL path.

A v1 set has deterministic, instance-scoped names:

```text
lantern-receipt-backup-v1-<sha256(instance)>-<20-digit-set-id>.active.lar
lantern-receipt-backup-v1-<sha256(instance)>-<20-digit-set-id>.active.walcut
lantern-receipt-backup-v1-<sha256(instance)>-<20-digit-set-id>.set.json
```

The canonical JSON `.set.json` manifest is the commit marker. It binds the
exact two member names, formats, versions, byte sizes, SHA-256 digests,
instance token, monotonic set ID, UTC backup timestamp, NodeID, and generation.
The loader rejects unknown, duplicate, missing, reordered, unsafe, malformed,
noncanonical, trailing, digest-mismatched, or size-mismatched data. Its fully
validated result owns the archive bytes and parsed WAL cut/tip witnesses so a
later restore layer does not need to reopen either member or the live WAL.

Startup restore discovery is deliberately stricter than retention collection.
`LoadLatestReceiptBackupSet` enumerates only the configured backup directory,
recognizes canonical current-format `.set.json` names in the requested
instance scope, chooses the highest recognized nonzero 20-digit set ID once,
and then delegates to the canonical set loader. If no such marker exists it
returns `ErrReceiptBackupSetNotFound`. Once a newest marker is selected, any
marker or member validation error fails closed; discovery never scans backward
to an older valid set. Foreign scopes, unrecognized names, temporary files, and
orphan members are not committed-set candidates. Discovery is read-only and
returns validated evidence only; it does not install that evidence into a
runtime.

Persistence orders durability as follows: create each final member with
`O_CREATE|O_EXCL`; write all bytes, file-sync, and close it; directory-sync;
then create the final manifest with `O_CREATE|O_EXCL`, write/file-sync/close it
last, and directory-sync again. A manifest is a commit marker only after the
strict loader accepts its complete canonical contents and bound members, so a
visible partial marker fails closed. This protocol uses no file locking or hard
links and never replaces an existing name. Failed attempts clean only paths
whose exclusive create proved ownership; files left by an interrupted process
or racing writer are preserved and ignored rather than guessed-owned. Once an
attempt owns a manifest, cleanup preserves every member unless removing that
manifest and syncing its absence both succeed. Retention counts only fully
validated committed sets for this instance and removes each old manifest first,
directory-syncs, removes its members, and directory-syncs again.
`LANTERN_BACKUP_RETAIN=0` keeps all sets.
Periodic, manual, and final-shutdown attempts are serialized, and set IDs stay
unique and increasing even if wall time repeats or moves backward.
Existing `lantern_backup_*` timing, failure, vertex, and edge metrics remain
the common production signals. Durable sets additionally publish
`lantern_backup_receipts`, `lantern_backup_origins`,
`lantern_backup_set_members`, and `lantern_backup_set_bytes`; completion logs
retain the existing `backup: wrote dump` event and add set, identity, member,
and byte fields.

The v1 set remains active-epoch-only and has no retired-catalog member. If the
one-cut source contains any retired evidence, production rejects the attempt
before encoding a member or creating a staging/final file. An empty retired
catalog preserves the existing two-member bytes and manifest contract.
Runtime-local baseline publication uses a separate private v2 sidecar; it does
not silently change these scheduler backup members.

**Durable receipt backup installation and startup wiring are not implemented
in this layer.**
`LANTERN_BACKUP_RESTORE_ON_START` must be `false` in durable receipt-WAL mode;
the legacy graph-only replay path remains rejected because it cannot certify
receipt continuity. Receipt capability/status and receipt-enabled client
writes also remain disabled.

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
| `LANTERN_BACKUP_ENABLED` | `false` | Master switch for periodic production. Requires `LANTERN_BACKUP_DIR`; writes graph-only `.lbk` files or private durable receipt sets according to runtime mode. |
| `LANTERN_BACKUP_DIR` | _(empty)_ | Mounted directory backup files are written to; graph-only startup restore also reads from it. |
| `LANTERN_BACKUP_INTERVAL` | `5m` | Backup cadence (`time.ParseDuration`). |
| `LANTERN_BACKUP_RETAIN` | `3` | Keep newest N valid own dumps/sets; `0` keeps all. |
| `LANTERN_BACKUP_INSTANCE_ID` | _(hostname)_ | Per-instance ownership token used to derive safe filenames. |
| `LANTERN_BACKUP_RESTORE_ON_START` | `true` | Graph-only: replay the newest dump before serving. Durable receipt-WAL mode currently requires this to be `false`. |
| `LANTERN_BACKUP_RESTORE_REQUIRED` | `false` | Graph-only: fail boot when restore errors (else warn + continue). Durable receipt restore is not implemented. |

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
This restore description applies only to graph-only mode; durable receipt-WAL
deployments must set `backup.restoreOnStart: false` until the receipt-set restore
layer lands.

## See also

- [docs/ha-runbook.md](ha-runbook.md) — rolling-upgrade drain (§7) and HA ops.
- [docs/replication.md](replication.md) — the multi-replica peer-bootstrap recovery
  path and the deployment-topology matrix (D7).
- `lantern-cli dump` / `lantern-cli restore` — the on-demand, file-compatible
  CLI half of the graph-only `.lbk` format. They do not consume private receipt
  backup sets.
