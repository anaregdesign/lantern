# Orderly current-authority custody

The maintained custody campaign is
[`current_custody_container.py`](../../.github/scripts/current_custody_container.py).
It owns the bounded #1727/#1609 lifecycle proof. For deployment inputs, use the
[single current configuration](../../deploy/examples/auth/README.md#environment-json-startup)
and [authenticated HA example](../../deploy/compose/oidc-ha/README.md). This
directory defines no alternate startup configuration or provisioning path.

Provision identity/trust once, run explicit `init-security` on each actual
target once, then supply the same complete current JSON on every ordinary start
and restart. Preserve node identity, fixed topology, historical trust, all native
history/custody, application WALs, stable data node IDs/epoch and operator
journal. Initial administrator input never resets later policy. Missing or
partial state, an identity-binding mismatch or inconsistent custody fails closed;
an existing deployment is never repaired by fresh initialization or trust
regeneration.

## Bounded campaign

Provide already built, same-source Linux Server, security-test exporter and
integration-test driver binaries matching the cached runtime image architecture.
Record their hashes, exact source HEAD/tree and build provenance. Select a new
private evidence directory and a unique `lantern-1727-` resource name. The script
requires a cached immutable `sha256:` image ID, and creates only new labeled task
networks and private volumes. Its arguments are:

```sh
python3 -B .github/scripts/current_custody_container.py \
  --server /absolute/private/binaries/server \
  --exporter /absolute/private/binaries/security.test \
  --driver /absolute/private/binaries/integration.test \
  --config /absolute/private/native/ntp.conf \
  --image "$IMMUTABLE_CACHED_IMAGE_ID" \
  --name "$NEW_LANTERN_1727_NAME" \
  --head "$EXACT_SOURCE_HEAD" --tree "$EXACT_SOURCE_TREE" \
  --evidence /absolute/private/new-custody-evidence
```

The controller generates isolated synthetic trust from the original exporter,
supplies each node's required `startup.json` through
[`security_startup.py`](../../.github/scripts/security_startup.py) for explicit
initialization and serving, and keeps operator keys outside Server mounts. It
uses real public/private TLS, orderly restart, partition/reconnect and isolated
resume, and verifies that a pre-CLEAN kill is refused. Its `--mixed-ha` variant
requires a new `lantern-1610-` name and additionally exercises independent data
WALs and private replication; it is a separate bounded functional campaign.
Original logs, failed phases and private volumes are retained. No image pulls,
host clock/trust changes, deployment or shared-resource pruning are performed.

This controller supplies **functional/lifecycle evidence only**, regardless of
binary. Label controlled UTC as synthetic; running a normal binary here does
not satisfy configured-source qualification. **Configured-source integration**
belongs only to the approved shared
[`current-sdk4.yml`](../../.github/workflows/current-sdk4.yml) `source-cohort-v4`
gate: the same three-member cohort, six sequential constructors
`init1..3` then `resume1..3`, and aggregate limits of at most 12 acquisition
attempts and 12 writes. Preserve that contract; per-case or repeated live-source
cohorts cannot replace it. Follow the distinct proof kinds and the
[current-authority contract](../../docs/security-current-authority.md#functional-validation-and-configured-source-integration).
Neither proof establishes real-provider, physical-device, OS reboot or power-loss
behavior. Avoid repeated live-source probing or fresh cohorts after failure.

## Custody and restart invariants

Normal SIGTERM shutdown must allow readiness drain, public/private handler
shutdown, native producer joins, resource closes and final durable checkpoint
writes. Inspect the exit status, `server stopped cleanly` log and each matching
CLEAN cycle/binding plus SHA-256 of the exact final floor bytes. A stopped
container or successful `docker stop` alone does not prove that checkpoint.

Resume consumes CLEAN into a new RUNNING cycle before update-capable M/P/B opens.
Fresh native time, unexpired membership/TLS, quorum/current authority and
independent data readiness remain required. A lone member with intact custody
can remain unavailable. Sys CLEAN does not certify application persistence,
replication convergence or authorization enforcement. Existing durable sessions
retain expiry and current-policy checks; process-local attempts/permits do not
survive restart.

Preserve original files and logs on RUNNING, missing/mixed custody, mismatched
hash/binding, unsafe permissions/aliases, missing journals/tips or native-floor
refusal. Keep surviving lease sidecars. Do not switch to fresh, manufacture
floors, delete retained state or regenerate trust. A final CLEAN publication may
report uncertain rename/directory-sync failure even when complete bytes later
appear; the next owner still validates the complete matching pair and every
ordinary native boundary. Separate custody storage is not an external monotonic
witness against whole-bundle rollback. See the
[detailed custody contract](../../docs/security-current-authority.md#orderly-floor-custody-and-process-restart).
