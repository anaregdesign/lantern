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

To preserve a receipt epoch across loss of its data volume, stop admitting
mutations to that node, drain accepted operations and quiesce incoming
replication. Keep writers fenced, take a final backup and record its
`cut_sha256`, instance ID and node ID on independent storage. A scheduled
backup taken before the last acknowledged mutation can omit an operation ID
whose result a client already received. Starting that image under the same
epoch could let a retry apply the operation twice.

To recover the witnessed final point, run the server binary locally. The
target must be a new directory under an existing parent.

```sh
lantern restore-graph-store --source "$RECOVERY_POINT" \
  --target /storage/recovered --instance "$INSTANCE_ID" \
  --node-id "$NODE_ID" --cut-sha256 "$CUT_SHA256"
```

The `--node-id` argument verifies the expected 32-hex-digit node ID. Restore
validates the selected image, opens a private staging directory and compares
the recovered application and graph state with the retained cut. It publishes
the target directory atomically after verification. Point the same node's
`LANTERN_DATA_DIR` at the restored directory. Restore native security material
and state through their separate procedures. Start a recovered receipt node
with `LANTERN_RECEIPT_MODE=restart` and its original configured epoch.
If there is no independent evidence of the final cut, keep that epoch out
of service.

In graph-only mode, `LANTERN_BACKUP_RESTORE_ON_START=true` allows startup
to select the newest private point for this instance when the target directory
is missing. An existing initialized directory takes precedence. A damaged
newest point fails startup. With `LANTERN_BACKUP_RESTORE_REQUIRED=true`,
startup opens only a directory with a valid Lantern manifest, including one
just restored from a recovery point. Receipt restart requires an
existing data directory, including one explicitly restored as described above.

Public `BackupSnapshot` and `lantern-cli dump` export the authorized logical
graph. Keep private graph storage recovery points accessible only to operators.

## Configuration

| Setting | Default | Behavior |
| --- | --- | --- |
| `LANTERN_DATA_DIR` | `tarp.db` in the Server binary | Node-owned Tarp database. Give every replica a separate persistent volume. |
| `LANTERN_BACKUP_DIR` | empty | Directory for recovery points. Leave empty to disable discovery and production. |
| `LANTERN_BACKUP_ENABLED` | `false` | Enable periodic production when the destination is configured. |
| `LANTERN_BACKUP_INTERVAL` | `5m` | Interval between scheduled backups. |
| `LANTERN_BACKUP_RETAIN` | `3` | Keep this node's newest N valid points. Zero keeps all. |
| `LANTERN_BACKUP_INSTANCE_ID` | hostname | Stable ownership scope for discovery and retention in a shared backup directory. |
| `LANTERN_BACKUP_RESTORE_ON_START` | `true` | In graph-only mode, recover a missing directory from the configured backup destination. |
| `LANTERN_BACKUP_RESTORE_REQUIRED` | `false` | Require a recovery point for a missing graph-only directory. Explicit receipt `fresh` initializes a new database and rejects required restore. |

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

## Compacting the node database

Tarp cleans obsolete DATA records and repacks small segments while the node
serves requests. A key-ordered checkpoint places related Vertex, Edge and
contribution values together on disk for range reads. The ordered keys already
determine scan and cursor order before the checkpoint. A checkpoint can raise
foreground write latency while it copies records.

Lantern samples write activity every 30 seconds on a serving node. Automatic
checkpoints start at 128 MiB of live records.

- **First pass after open.** A generation replacement or 16 MiB of new graph
  input qualifies after two minutes of write inactivity. A smaller backlog of
  at least 1 MiB also qualifies after an hour.
- **Later passes on the same generation.** After 30 seconds of write inactivity,
  the input threshold is the larger of 4 MiB and one sixteenth of the bytes
  retired by the previous pass. The minimum interval is the larger of 30 seconds
  and 32 times the previous pass duration. An hour of pending graph changes
  also qualifies a smaller backlog once the node is quiet and the interval
  has elapsed. A generation replacement uses the two-minute quiet period.

Tarp sorts new records and rewrites affected segments. Updates spread across
many segments, or repeated updates to shared metadata in one segment, can
still cause substantial I/O.

The worker lets retired generations, pinned readers and backups drain before
starting. It uses the previous pass’s duration and retirement statistics to
pace subsequent work. The `graphstore key-ordered compaction completed` log
reports the generation, duration, graph input bytes, and bytes unlinked or
still pinned. Those retirement counters describe old files. Filesystem
write volume requires a separate measurement.

For a planned maintenance window after a bulk import, stop one node and run
the following against its existing data directory. In HA, restart the node
and let it catch up before moving to the next one.

```sh
lantern compact-graph-store --target "$LANTERN_DATA_DIR"
```

Use the node's configured data path. Tarp acquires exclusive ownership and
performs normal open-time recovery before Lantern verifies the graph manifest.
Opening a different Tarp directory after a crash can update its recovery files,
even when Lantern rejects it. Allow disk space for replacement segments as well
as the existing data. The command reports how many
bytes Tarp unlinked. A zero-byte result can mean that all records were live.
The engine preserves graph contents, relay positions and receipt decisions.
See [Tarp’s key-ordered checkpoint contract](https://github.com/anaregdesign/tarp/blob/v0.15.0/docs/key-ordered-compaction.md)
and [the Lantern workload measurements](https://github.com/anaregdesign/lantern/issues/2251#issuecomment-7175705919).

## Public graph export and import

`lantern-cli dump` streams a logical point-in-time graph in protobuf or
NDJSON form. `lantern-cli restore` replays public Put operations with the
caller’s current authority. The export contains effective Edge weights and
the Vertex and Edge expiration times. Contribution identities, insertion
history, receipt results, causal floors and private replication positions are
available through private node recovery. A folded non-finite Edge weight
cannot be submitted as a new public Put.

The private producer exports the time and duration of the last successful backup, the number of members and total bytes in that point, and a failed-attempt counter. The corresponding series are `lantern_backup_last_success_timestamp_seconds`, `lantern_backup_duration_seconds`, `lantern_backup_set_members`, `lantern_backup_set_bytes` and `lantern_backup_failures_total`.
