# Snapshot backup & restore

> Tracking: [#769](https://github.com/anaregdesign/lantern/issues/769) (epic),
> [#770](https://github.com/anaregdesign/lantern/issues/770) (engine),
> [#771](https://github.com/anaregdesign/lantern/issues/771) (this doc).

Lantern is an **in-memory** store, so a single instance loses its whole graph
on any restart — including a routine **rolling update** or pod restart. The
snapshot-durability feature is the insurance for that:
the server **periodically dumps the whole graph to a mounted volume** and
**restores the newest dump on startup**, before it begins serving.

This is primarily the **single-instance** durability story — any single-pod
or single-container deploy. In a **multi-replica** cluster restore still runs
on boot as a **baseline**:
the restarted pod replays its newest dump, then peer **bootstrap** (snapshot +
tail, see [replication.md](replication.md)) overlays it through the write path,
so HLC ordering lets newer peer state win per key — replicas take priority, the
dump only fills gaps, and a whole-cluster cold start recovers from the dumps
instead of coming up empty.

It is **snapshot-based** durability — complementary to, and distinct from, the
write-ahead-log hook deferred in the replication RFC (D1). It changes no
leaderless-replication invariant.

## How it works

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
- **Restore accounting** reports actual `PutVerticesResponse.written` /
  `PutEdgesResponse.written`, not input frame counts. Consequently
  `lantern_restore_vertices`, `lantern_restore_edges`, and restore logs exclude
  born-expired or otherwise skipped rows.
- **Per-instance files.** Each writer owns files named
  `lantern-backup-<instance>-<nanos>.lbk` (`instance` defaults to the
  hostname). Writes are atomic (temp file + `rename`); a half-written file is
  never a restore candidate. Restore picks the **newest valid** file and skips
  a corrupt/truncated one for the next-newest. Retention deletes only an
  instance's **own** files.

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
| `LANTERN_BACKUP_ENABLED` | `false` | Master switch for the periodic dump loop. Requires `LANTERN_BACKUP_DIR`. |
| `LANTERN_BACKUP_DIR` | _(empty)_ | Mounted directory dumps are written to / read from. |
| `LANTERN_BACKUP_INTERVAL` | `5m` | Dump cadence (`time.ParseDuration`). |
| `LANTERN_BACKUP_RETAIN` | `3` | Keep newest N own dumps; `0` keeps all. |
| `LANTERN_BACKUP_INSTANCE_ID` | _(hostname)_ | Per-instance filename token. |
| `LANTERN_BACKUP_RESTORE_ON_START` | `true` | Replay the newest dump on boot, before serving, as a baseline (peers then overlay it via HLC). Set `false` to skip restore — pure peer bootstrap / start empty. |
| `LANTERN_BACKUP_RESTORE_REQUIRED` | `false` | Fail boot when a restore errors (else warn + continue). |

> **TTL-vs-interval caveat.** Entries decay, so a dump is only as useful as its
> data is still live at restore time. Keep `LANTERN_DEFAULT_TTL_SECONDS`
> comfortably **above** `LANTERN_BACKUP_INTERVAL` (and above your expected
> restart gap) or much of a restored graph may already be expired.

## Durability via a mounted volume

The feature works on any platform that can mount a directory which survives
container/pod restarts at `LANTERN_BACKUP_DIR`. Point the env vars above at
that path — the server then dumps periodically and restores the newest dump
on boot:

```bash
LANTERN_BACKUP_ENABLED=true
LANTERN_BACKUP_DIR=/data
LANTERN_BACKUP_INTERVAL=5m
LANTERN_DEFAULT_TTL_SECONDS=86400
```

- **Kubernetes** — the [Helm chart](../deploy/helm/lantern/) renders a per-pod
  `ReadWriteOnce` PVC mounted at `LANTERN_BACKUP_DIR` (`backup.persistence`, on
  by default). On GKE Autopilot this binds the default `standard-rwo`
  StorageClass. Each pod keeps its own dumps; for a single shared dump volume
  point `backup.persistence.existingClaim` at a pre-provisioned RWX claim.
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

## See also

- [docs/ha-runbook.md](ha-runbook.md) — rolling-upgrade drain (§7) and HA ops.
- [docs/replication.md](replication.md) — the multi-replica peer-bootstrap recovery
  path and the deployment-topology matrix (D7).
- `lantern-cli dump` / `lantern-cli restore` — the on-demand, file-compatible
  CLI half of the same format.
