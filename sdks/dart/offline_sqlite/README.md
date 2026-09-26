# lantern_client_offline_sqlite

Opt-in SQLite persistence for `lantern_client_offline`, using `sqflite` and the
SQLite engine supplied by Android and iOS. Neither the online SDK nor the pure
Dart offline core imports this Flutter package. This initial adapter is not yet
published; the repository uses path dependencies during development.

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
is a namespace, not server-side authorization. Await `wipePartition` before
rotating an account's credentials, as required by the offline core contract.

## Storage behavior

- Indexed SQL tables hold canonical cache, outbox, operation, and CDC metadata.
  The adapter does not mirror the entire database in memory.
- Successful transactions are committed before change notifications are sent.
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
  original effective weight, including non-finite results. Legacy Add stays
  quarantined. Reopening preserves absolute expiration and retry deadlines;
  replay never extends TTL.
- Unknown schemas, noncanonical records, damaged indexes, and inconsistent
  cache/outbox/operation state fail closed. Schema 2 added a key-only recovery
  table and a partition change epoch. Schema 3 atomically rewrites schema 1/2
  outbox and operation payloads/reservations for receipt evidence before
  validating the full graph and configured capacities. An unsupported version
  never resets the database.

Use one store owner per database in the application. Separate connections in
the same isolate share a transaction lane and post-commit notifications.
SQLite arbitrates cross-process writes, but cross-isolate/process live change
delivery is not provided: those owners must be coordinated by the application.
Database paths are application-owned and should have a single canonical spelling.

## CDC storage boundary

`changeCursor`, `applyChangeChunk`, and `resetChangeCursor` implement the storage
prerequisite for identity-only CDC in #1116. Origin sequences retain the complete
uint64 range as decimal text. Cache invalidation and chunk progress commit
together; every accepted chunk also advances the durable change epoch, while
only the final chunk advances the origin's last-applied sequence. Checkpoint
reset preserves bounded resident identities as key-only Unknown work and
removes confirmed values, while keeping pending outbox work. Bounded scans and
epoch-checked completion survive reopen. Partition wipe removes CDC and
recovery state.
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
dart run tool/crash_probe.dart
LANTERN_DART_RECEIPT_ENDPOINT=http://127.0.0.1:6396 \
LANTERN_DART_RECEIPT_TOKEN="$RECEIPT_TOKEN" \
  dart run tool/crash_probe.dart --receipt
dart run tool/performance_probe.dart
LANTERN_DART_REAL_WIRE_ENDPOINT=http://127.0.0.1:6397 \
  flutter test --no-pub ../../../tests/integration/dart_offline_sqlite_test.dart
LANTERN_DART_REAL_WIRE_ENDPOINT=http://127.0.0.1:6397 \
LANTERN_DART_IDENTITY_GAP_ENDPOINT=http://127.0.0.1:6399 \
LANTERN_DART_IDENTITY_CLUSTER_ENDPOINTS=http://127.0.0.1:6400,http://127.0.0.1:6401,http://127.0.0.1:6402 \
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
real-wire tests. A response-dropping proxy consumes four committed receipt
responses before the writer is SIGKILLed with SQLite open. A fresh process
reopens the file, checks the original conditional Put, exact Deletes, and
contribution-keyed Add-after-Delete results from status, and verifies that no
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
