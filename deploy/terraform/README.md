# Lantern Terraform modules

Infrastructure-as-Code for the **single-instance (Tier B)** Lantern deploys —
Google **Cloud Run** and Azure **Container Apps** — with snapshot durability
wired in (a mounted volume + `LANTERN_BACKUP_*`, see
[../../docs/backup.md](../../docs/backup.md)).

These are the serverless PaaS targets the replication RFC classifies as Tier B
([../../docs/replication.md](../../docs/replication.md) D7): a single instance,
no replicated cluster. Lantern is in-memory, so durability across a revision
rotation comes from the periodic backup + restore-on-startup engine — the
modules provision the volume and set the env so that works out of the box.

For the multi-replica **Tier A** HA topology use the Helm chart
([../helm/lantern](../helm/lantern)); for local experiments use
[../compose](../compose).

| Module | Platform | Volume backend |
|---|---|---|
| [`cloudrun/`](cloudrun) | Google Cloud Run | Cloud Storage (GCS FUSE) bucket |
| [`aca/`](aca) | Azure Container Apps | Azure Files (SMB) share |

## Usage

Each module is standalone. Provide a backend / credentials for your cloud, set
the required variables, and apply:

```bash
cd cloudrun   # or: cd aca
terraform init
terraform plan  -var project_id=my-proj -var backup_bucket_name=my-lantern-backups
terraform apply -var project_id=my-proj -var backup_bucket_name=my-lantern-backups
```

```bash
cd aca
terraform init
terraform apply -var storage_account_name=mylanternsa   # 3-24 lowercase alphanumeric, globally unique
```

The `backend` block is intentionally omitted — configure remote state to suit
your org.

## Notes

- **Single instance.** Both modules pin `min = max = 1`. Running multiple
  instances as a replicated cluster is unsupported on these platforms (RFC D7).
- **Per-instance files, no locking.** GCS FUSE and Azure Files give no
  cross-writer locking; the backup engine writes per-instance filenames, so
  even a transient two-revision cutover window is collision-free.
- **30 s mount budget (Cloud Run).** A slow GCS mount fails container start;
  the module uses the second-generation execution environment automatically
  (required for GCS volumes).
- **HTTP/2.** Both expose the port as h2c / `transport = http2` so Lantern's
  Connect / gRPC surface works end-to-end.
- **Drain.** `LANTERN_DRAIN_DELAY_SECONDS` is set so revision cutover sheds
  in-flight requests cleanly (#768).
- **TTL vs interval.** `default_ttl_seconds` defaults to 24 h — keep it well
  above `backup_interval` so a restored dump still carries live data.
