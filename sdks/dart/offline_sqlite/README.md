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
- The initial offline write surface remains unconditional Put only. Reopening
  preserves absolute expiration and retry deadlines; replay never extends TTL.
- Unknown schemas, noncanonical records, damaged indexes, and inconsistent
  cache/outbox/operation state fail closed. Schema 2 adds a key-only recovery
  table and a partition change epoch. Schema 1 migrates transactionally to 2
  without dropping cache, cursor, or pending writes. An unsupported version
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

This adapter does not start a CDC subscription. The identity-only server stream
and gap/bootstrap orchestration remain separate #1116 work. Ordinary Get and
resident plural revalidation now use the same change-epoch barrier. Cursor
storage alone never establishes freshness.

## Verification

From this package directory:

```sh
flutter pub get --enforce-lockfile
flutter analyze --no-pub
flutter test --no-pub
dart run tool/crash_probe.dart
dart run tool/performance_probe.dart
LANTERN_DART_REAL_WIRE_ENDPOINT=http://127.0.0.1:6397 \
  flutter test --no-pub ../../../tests/integration/dart_offline_sqlite_test.dart
```

Host tests inject `sqflite_common_ffi` as a development-only dependency and run
the same SQL implementation. The normal Android/iOS application uses `sqflite`
and does not bundle the host test engine. Tests run the shared store and CDC
conformance suites against actual close/reopen boundaries, exercise a real
SQLite disk-full error, reject unknown/corrupt databases, and replay through a
real Lantern server after a proxy drops committed responses. The crash probe
kills a separate process at transaction boundaries; it is distinct from a Dart
exception or an in-memory snapshot test.

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
