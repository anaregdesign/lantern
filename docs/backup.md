# Snapshot backup & restore

> Tracking: [#769](https://github.com/anaregdesign/lantern/issues/769) (epic),
> [#770](https://github.com/anaregdesign/lantern/issues/770) (engine),
> [#771](https://github.com/anaregdesign/lantern/issues/771) (this doc), and
> [#1394](https://github.com/anaregdesign/lantern/issues/1394) (durable
> receipt backup/restore).

## Private recovery for Tarp storage

Each Tarp-backed node owns its `LANTERN_DATA_DIR`. Restart reopens that
directory using the stored node identity. Keep an independent persistent
volume for each node. A backup volume can hold recovery points from multiple
nodes if each producer uses its own `LANTERN_BACKUP_INSTANCE_ID`.

The backup scheduler, manual backup and final shutdown backup produce a
private `lantern-tarp-<instance hash>-<id>/` directory. Its `image/` contains
a native Tarp backup. Its `cut.json` binds that image to the node, namespace,
active generation, application revision, local relay sequence and history
digest. A separate SHA-256 covers the ordered active graph and service records,
including contribution identities, absolute expiration and causal metadata.
The producer obtains those witnesses from a pinned live read. It verifies
the native backup's storage sequence before publishing both members together.
If a foreground commit lands between the two captures, the producer retries
once with writes held during the native copy. Size the volume for the staged
copy and the recovery points selected by the retention policy.

Retain the logged `cut_sha256` independently when confirming a previously
observed backup point matters. To recover that specific point into a new
directory whose parent already exists, run the server binary locally:

```sh
lantern restore-graph-store --source "$RECOVERY_POINT" \
  --target /storage/recovered --instance "$INSTANCE_ID" --cut-sha256 "$CUT_SHA256"
```

An optional `--node-id <32 hex digits>` verifies the expected node. Restore
validates the selected image, opens a private staging directory and compares
the recovered application and graph state with the retained cut. It publishes
the target directory atomically after verification. Point the same node's
`LANTERN_DATA_DIR` at the restored directory. Restore native security material
and state through their separate procedures.

With `LANTERN_BACKUP_RESTORE_ON_START=true`, startup can select the newest
private point for this instance when the configured target directory is
missing. An already initialized directory takes precedence. A damaged newest
point fails startup. `LANTERN_BACKUP_RESTORE_REQUIRED=true` also requires a
recovery point when the target is missing. Startup discovery uses the
available catalog. The explicit command checks continuity with an independently
retained digest even if a volume was rolled back.

Public `BackupSnapshot` and `lantern-cli dump` export the authorized logical
graph. Keep private graph storage recovery points accessible only to operators.

## Configuration

| Setting | Default | Behavior |
| --- | --- | --- |
| `LANTERN_DATA_DIR` | `lantern-data` in the Server binary | Node-owned Tarp database. Give every replica a separate persistent volume. |
| `LANTERN_BACKUP_DIR` | empty | Directory for recovery points. Leave empty to disable discovery and production. |
| `LANTERN_BACKUP_ENABLED` | `false` | Enable periodic production when the destination is configured. |
| `LANTERN_BACKUP_INTERVAL` | `5m` | Interval between scheduled backups. |
| `LANTERN_BACKUP_RETAIN` | `3` | Keep this node's newest N valid points. Zero keeps all. |
| `LANTERN_BACKUP_INSTANCE_ID` | hostname | Stable ownership scope for discovery and retention in a shared backup directory. |
| `LANTERN_BACKUP_RESTORE_ON_START` | `true` | Allow graph-only or receipt restart to recover a missing directory from the configured backup destination. |
| `LANTERN_BACKUP_RESTORE_REQUIRED` | `false` | Fail startup if a required recovery point is absent. Explicit receipt `fresh` initializes a new database and rejects required restore. |

Native management can adopt the production settings for enabled, interval and
retention together. Destination, instance identity and startup restoration
remain externally configured. Reducing retention may delete older valid points
after a successful production attempt. Inspect the retention preview and retain
external copies of points you intend to keep longer.

Use the node-volume layout in the [Compose examples](../deploy/compose/) or
the [Helm chart](../deploy/helm/lantern/). Keep the data directory on a local
filesystem with the locking and flush guarantees required by Tarp. A separate
backup destination protects against loss of the live volume. Each restored
node must retain its assigned identity and join the configured native cluster
through Lantern's authenticated replication path. See the
[HA runbook](ha-runbook.md) and [replication contract](replication.md).

## Public graph export and import

`lantern-cli dump` streams a logical point-in-time graph in protobuf or
NDJSON form. `lantern-cli restore` replays public Put operations with the
caller’s current authority. The export contains effective Edge weights and
the Vertex and Edge expiration times. Contribution identities, insertion
history, receipt results, causal floors and private replication positions are
available through private node recovery. A folded non-finite Edge weight
cannot be submitted as a new public Put.
