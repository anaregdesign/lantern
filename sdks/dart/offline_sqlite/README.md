# lantern_client_offline_sqlite

Opt-in SQLite persistence for `lantern_client_offline`, using `sqflite` and the
SQLite engine supplied by Android and iOS. Neither the online SDK nor the pure
Dart offline core imports this Flutter package. This initial adapter is not yet
published; the repository uses path dependencies during development.

The source candidate implements the common bound client with automatic foreground delivery:

```dart
final startup = await SqliteOfflineStore.openSession(
  path: applicationDatabasePath,
  client: transport,
  binding: sessionBinding,
  installationPolicy: applicationInstallationPolicy,
);
if (startup case SqliteRestoreHeld(:final reason)) {
  // Keep the original database and show attention without a data client.
  return showRestoreHold(reason);
}
final data = (startup as SqliteSessionReady).session;
final ticket = await data.mutations.submit(MutationRequest.deleteVertices([
  'note:42',
])); // Durable admission without capability, credentials or network.
```

Ordinary CRUD Futures await Server handling. The bounded session owner handles
preparation, original-receipt recovery and retries. The application/native bridge
provides foreground and verified credential events. The maintained
[Flutter app](../example/README.md#common-data-ownership-and-migration) composes
Direct, Memory, Durable and durable-outbox without read cache. Applications do
not schedule delivery or expose a manual Sync action. The legacy explicit Repository
example below remains for published clients and qualification tools.

`installationPolicy` is required and must be one stable application-owned object
for matching handles. It verifies backup/transfer exclusion and an independent
installation signal under the core reservation and native guard, before SQLite
write-open, migration or automatic work. A held result exposes no admission or
network capability and performs no expiry, authority binding or schema upgrade.
The application still owns the transport when startup is held or fails.

Schema9 adds a singleton installation binding and a bounded per-partition adoption
record. A markerless legacy database requires both an exact
`SqliteScopeAdoption.verified` account mapping and an independently trusted
`legacyAdoptionVerified` policy assertion. Login, a new UUID, an existing filename
or newly configured backup exclusion do not establish that assertion. Without it,
the maintained integration holds the store. Missing/mismatched installation
evidence is never replaced to make a database pass. Matching evidence permits
ordinary current-store reopen under the declared policy; it does not establish
arbitrary unreported copy/rollback detection.

Adoption stays at the original canonical database path. It rewrites relational
and embedded scope identities in one transaction, including any schema upgrade;
the runtime uses only the destination binding's stable identity. Original
operation/receipt/group/contribution IDs, values, TTL, retention, dispatch
certainty, leases, FIFO and dependencies are retained. Destination byte budgets
are recomputed before commit; conflict, corruption or insufficient space rolls
back the entire change. Old granting cache/traversal/CDC views are invalidated.
An uncertain commit is resolved by reopening the adoption record, not by admitting
the same application write again. Plain reopen does not clear an authentication
pause; legacy null-authority adoption also preserves policy pause. Old opaque
operation strings remain diagnostic identities, not newly minted `OperationId`s.

The qualified factory holds native ownership through database close. A failed
close is memoized and retains the guard; do not delete the lock file, reset the
database or start a replacement owner. The application account controller must
retain retirement failure and prevent successor activation. The low-level
`open`/`openAutomatic` adapter primitives do not establish installation policy;
manual tools and host fixtures remain responsible for their explicit scope.

```dart
final store = await SqliteOfflineStore.open(path: applicationDatabasePath);
final repository = OfflineLanternRepository(
  store: store,
  remote: LanternClientOfflineRemote(client),
);

final write = await repository.putVertex(
  partitionId: applicationAccountScope,
  input: VertexInput(key: 'note:42', value: VertexValue.string('Draft')),
);
// The SQLite transaction has committed. Remote confirmation is separate.
await repository.probeAndDrain(applicationAccountScope);
final status = await repository.getWriteStatus(
  applicationAccountScope,
  write.operationId,
);

// Stop owned network/watch/replay work before closing its database.
await repository.dispose();
await store.close();
```

The application supplies the database path, identity binding, device protection,
backup policy, and any additional encryption. The default OS SQLite factory does
not provide application-level database encryption. A different audited factory
can be injected through `databaseFactory`; no token, credential, encryption key,
or key acquisition callback is persisted by this package. A local `partitionId`
is a namespace, not server-side authorization. A shared mutable remote requires
`wipePartition` before swapping to another account. The maintained native app
instead closes/joins independently bound immutable account transports and retains
each account's original intent for A→B→A recovery. Before an explicitly chosen wipe,
close/join the old lifetime and discard its tickets first. Explicit wipe erases
the old generation's invocation-ID guards and receipts; it cannot undo Server
work. A new explicit submission, including a still-fresh reused caller ID, can
execute again. Wipe is not automatic retry, storage-error recovery or cache clearing.

## Storage behavior

- Indexed SQL tables hold canonical cache, outbox, operation, and CDC metadata.
  The adapter does not mirror the entire database in memory.
- Successful transactions are committed before change notifications are sent.
- Common delivery's bounded claim fences observe committed lease/generation,
  authority and high-water changes before notifications or transaction completion,
  including quiet clock advances. They cover this adapter's same-isolate lane;
  the automatic factory additionally acquires a native exclusive lifetime guard.
  Legacy schema5 and earlier have no persisted authority proof: migration hides
  old cache/cursors and retains bounded revalidation identities. Same-authority
  current-format reopen preserves eligible history and immutable outbox evidence.
  SQL transaction rollback includes capacity checks, FIFO ordinals, claims,
  renewable leases, and operation status. Network calls stay outside database
  transactions.
- `synchronous=FULL` is configured on every opened connection. This is a SQLite
  durability setting, not a claim to survive arbitrary storage hardware faults.
- Cache capacity may evict least-recently-used confirmed records. Pending
  outbox work is never evicted to admit another write.
- Unconditional Put retains its idempotent path. Receipt-bearing Vertex
  PutIfAbsent and exact Vertex/Edge Delete preserve identity, endpoint/policy
  evidence, reconciliation state, and exact original results across reopen.
  Receipt-backed Edge Add also retains its explicit contribution ID and
  original effective weight, including non-finite results. Targeted contribution
  Delete retains its exact contribution ID and original `existed` boolean in
  the core codec, including `false`. It uses existing SQL payload tables and
  does not require another database schema migration. Legacy Add stays
  quarantined. Reopening preserves absolute expiration and retry deadlines;
  replay never extends TTL.
- The offline 0.6.0 / parent 0.5.0 source candidates persist terminal
  `acceptedUndisclosed` operation metadata through operation codec v4 (historical v3 is migrated). The owned outbox and
  overlay are retired atomically with cache invalidation, without an invented
  result or mutation resend after reopen. This requires no SQLite table-schema
  change. Host tests do not qualify hosted archives or physical devices.
- Unknown schemas, noncanonical records, damaged indexes, and inconsistent
  cache/outbox/operation state fail closed. Schema 2 added a key-only recovery
  table and a partition change epoch. Schema 3 introduced receipt evidence;
  schema 4 atomically rewrites schema 1/2/3 outbox payloads and reservations,
  treating absent dispatch markers as possibly sent before validating the full
  graph and configured capacities. An unsupported version never resets the
  database. Schema 6 adds persistent clock/authority metadata, structured-ID guard
  deadlines and indexed prerequisite references, and atomically rewrites outbox
  v5/operation v4 reservations. Migration validates the entire graph and preserves
  IDs/TTL/dispatch proof before any network work. An interrupted migration rolls
  back; insufficient capacity fails open rather than dropping accepted state.
  Schema 7 adds monotone owner epoch/token history and persisted
  credential-generation/policy pauses. It follows schema 6 in merged #1768;
  migration preserves the existing auth pause. Schema 8 adds the independent
  indexed Traversal result lane and semantic validity epoch; older schemas start
  with no derived results and retain their existing mutation bytes. Schema 9 adds
  one installation binding and bounded per-partition adoption records. Qualified
  migration and adoption commit together; unknown continuity returns a held
  startup before write-open. Markerless legacy stores require independent trusted
  installation provenance and an exact verified scope mapping. The maintained
  mobile default holds them until an integrator can supply that evidence.

Automatic opens use a dedicated native `flock` sidecar, acquired before SQLite
open/migration. The shared C Code Asset supports Android/iOS and host VM tests;
mobile SQLite remains the OS engine through sqflite. Same-isolate matching handles
share; other isolate/process opens conflict before work. Canonical paths resolve
symlinks, pin file identity and reject DB/sidecar hardlinks. Sidecars are not removed
on close. The 16-entry opaque guard table has no finalizer: isolate abandonment
remains locked until process exit. A new process can acquire a new epoch and
reconcile original receipts after process death. No PID or wall-clock takeover.

See [runtime boundaries](../offline/docs/session-runtime-development.md) for
acquisition/release failure ordering, credential/work fences, transfer/restore
holds and host-only evidence. Manual store connections still share a same-isolate
transaction lane, but do not supply an automatic capability. Cross-process live
change delivery, external file replacement and network filesystems are outside
this owner contract. Installation/backup continuity must be supplied independently.

## CDC storage boundary

Public scoped CDC uses `scopedChangeCursor`, `applyScopedChangeFrame` and
`resetScopedChangeCursor`. Schema 5 stores one bounded opaque BLOB checkpoint
in an indexed row per partition. Frame invalidation and optional completion
cursor commit together; key-bearing partial frames advance the read epoch but
preserve the prior checkpoint. Empty progress persists its cursor and ordinary
commit notification without advancing read/derived epochs or renewing cache.
A gap/bootstrap hides confirmed values as bounded key-only
Unknown work, with indexed scans and epoch-checked plural completion. Outbox
records and receipt dispatch evidence survive unchanged; wipe removes both CDC
protocols and recovery state.

Schema 1/2/3/4 upgrades validate the existing schema and payload graph, create
opaque state, hide legacy cache as Unknown and clear private origin progress
atomically. They never infer a public checkpoint from origin vectors. Corrupt or
unsupported state fails closed without resetting the database. Copied snapshots
and native-file continuity are separate Server concerns.

The legacy origin-vector methods remain private/historical conformance only.
The application composes public WatchChanges using the maintained Flutter
[bridge](../example/lib/scoped_change_source.dart), with a pinned responder and
foreground cancellation. Run `runScopedChangeStoreConformanceSuite` over the
actual close/reopen boundary. Host FFI tests verify storage logic, not physical
Android/iOS qualification or availability of an unpublished parent API.
An ordinary `serverOnly` Get cannot clear a checkpoint-Unknown resident marker;
only the explicit plural recovery batch does so, while pending Put overlays
remain visible.

This adapter does not start a CDC subscription. The offline core's explicit
foreground `consumeIdentityChanges` session accepts an application-injected
responder-pinned identity source; this package supplies only its durable SQL
store. Ordinary Get and resident plural revalidation use the same change-epoch
barrier. Cursor storage alone never establishes freshness.

## Verification

From this package directory:

```sh
flutter pub get --enforce-lockfile
flutter analyze --no-pub
flutter test --no-pub
dart run tool/automatic_owner_probe.dart
dart run tool/crash_probe.dart
LANTERN_DART_RECEIPT_ENDPOINT=https://localhost:6396 \
LANTERN_DART_RECEIPT_CA_FILE="$RECEIPT_CA_FILE" \
LANTERN_DART_RECEIPT_TOKEN="$RECEIPT_TOKEN" \
  dart run tool/crash_probe.dart --receipt
dart run tool/performance_probe.dart
LANTERN_DART_REAL_WIRE_ENDPOINT=http://127.0.0.1:6397 \
  flutter test --no-pub ../../../tests/integration/dart_offline_sqlite_test.dart
LANTERN_DART_REAL_WIRE_ENDPOINT=http://127.0.0.1:6397 \
LANTERN_DART_RECEIPT_ENDPOINT=https://localhost:6396 \
LANTERN_DART_RECEIPT_CA_FILE="$RECEIPT_CA_FILE" \
LANTERN_DART_RECEIPT_TOKEN="$RECEIPT_TOKEN" \
LANTERN_DART_IDENTITY_GAP_ENDPOINT=https://localhost:6399 \
LANTERN_DART_IDENTITY_GAP_CA_FILE="$IDENTITY_GAP_CA_FILE" \
LANTERN_DART_IDENTITY_CLUSTER_ENDPOINTS=https://localhost:6400,https://localhost:6401,https://localhost:6402 \
LANTERN_DART_IDENTITY_CA_FILE="$IDENTITY_CLUSTER_CA_FILE" \
  flutter test --no-pub ../../../tests/integration/dart_identity_offline_sqlite_test.dart
```

Host tests inject `sqflite_common_ffi` as a development-only dependency and run
the same SQL implementation. The normal Android/iOS application uses `sqflite`
and does not bundle the host test engine. Tests run the shared store and CDC
conformance suites against actual close/reopen boundaries, exercise a real
SQLite disk-full error, reject unknown/corrupt databases, and replay through a
real Lantern server after a proxy drops committed responses. The crash probe
kills a separate process at transaction boundaries; it is distinct from a Dart
exception or an in-memory snapshot test.

The `--receipt` crash gate requires a live local authenticated receipt-WAL
server and fails if either the endpoint or token is missing. CI starts a
separate fixture with `testbed/scripts/dart_receipt_fixture.sh` and passes its
temporary token to the test; the local command above expects `RECEIPT_TOKEN`
to be set from that file. The graph-only server remains available for existing
real-wire tests. A response-dropping proxy consumes five committed receipt
responses before the writer is SIGKILLed with SQLite open. A fresh process
reopens the file, checks the original conditional Put, exact Deletes, and
contribution-keyed Add-after-Delete and targeted contribution Delete results from status, and verifies that no
mutation was resent or the later-deleted Add edge resurrected. The existing
eight crash scenarios and separate cross-process claim gate still run unchanged.
The receipt-bearing Delete uses a different edge because an ambiguous
same-edge Delete would block Add under per-key FIFO. Direct Deletes of the Add
target must each return `true`; its absence is checked before Add, after the
committed Add is deleted, and again after SQLite reopens.

The identity test uses a test-only adapter over the typed parent SDK stream,
real Connect/h2c nodes, and a reopened SQLite FFI database. It exercises
bootstrap revalidation, live invalidation, an evicted resume gap, and durable
cursor reuse across three replicas. CI starts its fixtures through
`testbed/scripts/dart_identity_fixture.sh`; the production runtime graph has
no new platform dependency.

The same crash gate also starts independent claimers in separate VMs, verifies
one claim winner and durable lease renewal, kills the owner, and checks expiry
recovery and stale-owner rejection. Disk-full coverage preserves the complete
canonical pending outbox and operation records across failure and reopen.

The maintained Flutter example's native smoke exercises OS SQLite on Android
and iOS, including reopen, TTL, replay, and logout isolation. Simulator and host
results do not replace the exact-revision physical-device evidence required by
#1163. Publication remains a separate future step after #1162.

The [2026-09-24 physical records](../example/evidence/2026-09-24-sqlite/README.md)
verify process-kill restart, original TTL, logout wipe and local user isolation
on both Android and iOS. They explicitly retain the tested commit and do not
claim the complete transport/privacy/publication matrix.

`SqliteOfflineStore.openSession` also accepts optional `readRoute`; when an
incoming source exists both must use the same certified capability. Polling-only
Traversal retention and explicit cross-lifetime continuation follow the
[Offline contract](../offline/README.md#bounded-traversal-history). Result LRU
and byte budgets are independent of the exact cache and durable outbox. Host
SQLite conformance does not replace the stopped Android/iPhone qualification.
