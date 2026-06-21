# Snapshot backup & restore (Tier-B durability)

> Tracking: [#769](https://github.com/anaregdesign/lantern/issues/769) (epic),
> [#770](https://github.com/anaregdesign/lantern/issues/770) (engine),
> [#771](https://github.com/anaregdesign/lantern/issues/771) (this doc).

Lantern is an **in-memory** store, so a single instance loses its whole graph
on any restart — including the routine **rolling update** of a serverless
container revision. The snapshot-durability feature is the insurance for that:
the server **periodically dumps the whole graph to a mounted volume** and
**restores the newest dump on startup**, before it begins serving.

This is the **Tier B** (single-instance) durability story — Cloud Run, Azure
Container Apps, App Runner, or any single-container deploy. In a **Tier A**
multi-replica cluster, a restarted pod recovers from its **peers** (snapshot +
tail bootstrap, see [replication.md](replication.md)); there backups serve only
whole-cluster-loss recovery and restore-on-start is gated off by default.

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
| Cloud Run — Cloud Storage (GCS FUSE) | yes | **no file locking**, last-write-wins, not POSIX; mount must finish < 30 s or boot fails |
| Cloud Run — NFS (Filestore) | yes | mounted **no-lock** |
| ACA — Azure Files (SMB/NFS) | yes (across replicas/revisions) | multi-writer coordination is the app's job |
| ACA — EmptyDir | no (replica-scoped, ephemeral) | n/a |

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
| `LANTERN_BACKUP_RESTORE_ON_START` | `true` | Restore the newest dump on boot (before serving). |
| `LANTERN_BACKUP_RESTORE_FORCE` | `false` | Restore even when peers are configured (multi-peer mode otherwise relies on peer bootstrap). |
| `LANTERN_BACKUP_RESTORE_REQUIRED` | `false` | Fail boot when a restore errors (else warn + continue). |

> **TTL-vs-interval caveat.** Entries decay, so a dump is only as useful as its
> data is still live at restore time. Keep `LANTERN_DEFAULT_TTL_SECONDS`
> comfortably **above** `LANTERN_BACKUP_INTERVAL` (and above your expected
> restart gap) or much of a restored graph may already be expired.

## Cloud Run

Mount a Cloud Storage bucket (GCS FUSE) — or a Filestore NFS share — at
`LANTERN_BACKUP_DIR`, single instance:

```bash
gcloud run deploy lantern \
  --image ghcr.io/anaregdesign/lantern:latest \
  --port 6380 --use-http2 \
  --min-instances 1 --max-instances 1 \
  --add-volume name=backups,type=cloud-storage,bucket=YOUR_BUCKET \
  --add-volume-mount volume=backups,mount-path=/data \
  --set-env-vars LANTERN_BACKUP_ENABLED=true,LANTERN_BACKUP_DIR=/data,LANTERN_BACKUP_INTERVAL=5m,LANTERN_DEFAULT_TTL_SECONDS=86400,LANTERN_DRAIN_DELAY_SECONDS=5
```

Notes: `--use-http2` is required for Lantern's h2c (Connect / gRPC) surface;
GCS FUSE provides **no locking** (per-instance files handle it); the volume
mount must complete within Cloud Run's 30 s startup budget. For lower-latency
durability use a Filestore NFS volume (`type=cloud-storage` → an NFS volume)
instead of GCS.

## Azure Container Apps

Define an Azure Files (SMB) share on the environment, then mount it at
`LANTERN_BACKUP_DIR`:

```bash
az containerapp env storage set -n my-env -g my-rg \
  --storage-name backups --storage-type AzureFile \
  --azure-file-account-name STORAGE --azure-file-account-key KEY \
  --azure-file-share-name SHARE --access-mode ReadWrite
```

```yaml
# app.yaml — properties.template excerpt
template:
  containers:
    - name: lantern
      image: ghcr.io/anaregdesign/lantern:latest
      env:
        - name: LANTERN_BACKUP_ENABLED
          value: "true"
        - name: LANTERN_BACKUP_DIR
          value: /data
        - name: LANTERN_BACKUP_INTERVAL
          value: 5m
        - name: LANTERN_DEFAULT_TTL_SECONDS
          value: "86400"
      volumeMounts:
        - volumeName: backups
          mountPath: /data
  scale:
    minReplicas: 1
    maxReplicas: 1
  volumes:
    - name: backups
      storageType: AzureFile
      storageName: backups
```

`EmptyDir` is replica-scoped and ephemeral — **don't** use it for durability.

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
stable pod name, so dumps never collide; restore-on-start stays gated off there
(peer bootstrap is the recovery path) unless you also set
`LANTERN_BACKUP_RESTORE_FORCE=true`.

## See also

- [docs/ha-runbook.md](ha-runbook.md) — rolling-upgrade drain (§7) and HA ops.
- [docs/replication.md](replication.md) — the Tier-A peer-bootstrap recovery
  path and the deployment-topology matrix (D7).
- `lantern-cli dump` / `lantern-cli restore` — the on-demand, file-compatible
  CLI half of the same format.
