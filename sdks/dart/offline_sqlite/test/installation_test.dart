import 'dart:async';
import 'dart:io';

import 'package:connectrpc/connect.dart' as connect;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  late Directory root;
  late String path;
  late LanternClient client;
  late LanternSessionBinding binding;
  var dataCalls = 0;
  connect.AnyFn<I, O> rejectData<I extends Object, O extends Object>(
    connect.AnyFn<I, O> next,
  ) => (request) async {
    dataCalls++;
    throw StateError('Unexpected data work before installation qualification');
  };
  final factory = databaseFactoryFfiNoIsolate;
  setUp(() async {
    dataCalls = 0;
    root = await Directory.systemTemp.createTemp('lantern-installation-test-');
    path = '${root.path}/store.db';
    client = LanternClient.connect(
      Uri.parse('http://127.0.0.1:1'),
      allowInsecure: true,
      interceptors: [rejectData],
    );
    binding = LanternSessionBinding.application(
      serviceOrigin: client.endpoint,
      securityDomain: 'test',
      accountIdentity: 'account',
      authorityBinding: 'current',
    );
  });
  tearDown(() async {
    await client.close();
    await root.delete(recursive: true);
    expect(dataCalls, 0);
  });
  SqliteInstallationPolicy policy({
    String id = 'installation',
    SqliteRestoreReason? hold,
    bool legacy = false,
  }) => SqliteInstallationPolicy(
    identity: 'explicit-isolated-host-fixture',
    version: 1,
    evaluate: (_) async => hold != null
        ? SqliteInstallationEvidence.held(hold)
        : SqliteInstallationEvidence.verified(
            installationId: id,
            legacyAdoptionVerified: legacy,
          ),
  );
  Future<SqliteSessionStartup> open(
    SqliteInstallationPolicy p, {
    SqliteScopeAdoption? adoption,
    bool transferAllowed = false,
  }) => SqliteOfflineStore.openSession(
    path: path,
    client: client,
    binding: binding,
    installationPolicy: p,
    adoption: adoption,
    databaseFactory: factory,
    transferAllowed: transferAllowed,
  );

  test(
    'I5 fresh and matching reopen stamp installation before automatic owner',
    () async {
      final p = policy();
      final first = await open(p) as SqliteSessionReady;
      final check = await factory.openDatabase(
        path,
        options: OpenDatabaseOptions(readOnly: true, singleInstance: false),
      );
      expect(await check.getVersion(), 9);
      expect(
        (await check.query('installation_binding')).single['installation_id'],
        'installation',
      );
      expect((await check.query('automatic_owner')).single['active'], 1);
      await check.close();
      await first.session.close();
      client = LanternClient.connect(
        binding.serviceOrigin,
        allowInsecure: true,
      );
      final next = await open(p) as SqliteSessionReady;
      await next.session.close();
    },
  );

  for (final reason in [
    SqliteRestoreReason.continuityUnknown,
    SqliteRestoreReason.restoreIndicated,
    SqliteRestoreReason.installationMissing,
  ]) {
    test('I5 $reason does not create or migrate a store', () async {
      final p = policy(hold: reason);
      final held = await open(p) as SqliteRestoreHeld;
      expect(held.reason, reason);
      expect(await File(path).exists(), false);
      final store = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: factory,
      );
      await store.transaction((tx) async {
        final at = DateTime.utc(2020);
        final row = await tx.enqueue(
          OfflineOutboxRecord(
            recordId: 'held-record',
            operationId: 'held-operation',
            itemIndex: 0,
            partitionId: 'legacy',
            generation: 0,
            ordinal: 0,
            intent: OfflinePutVertexIntent(
              Vertex(
                key: 'private',
                value: const StringValue('retained'),
                expiration: at.add(const Duration(minutes: 1)),
              ),
            ),
            enqueuedAt: at,
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            mayHaveDispatched: false,
          ),
        );
        await tx.putOperation(
          OfflineOperationRecord(
            partitionId: 'legacy',
            generation: 0,
            operationId: row.operationId,
            items: [
              OfflineWriteStatus(
                recordId: row.recordId,
                operationId: row.operationId,
                itemIndex: 0,
                state: OfflineWriteState.locallyCommitted,
                attemptCount: 0,
                mayHaveDispatched: false,
              ),
            ],
            updatedAt: at,
          ),
        );
        await tx.setReplayPausedForAuth(
          'legacy',
          true,
          credentialGeneration: 7,
        );
        await tx.setReplayPausedForPolicy('legacy', true);
      });
      await store.close();
      final old = await factory.openDatabase(path);
      await old.execute('DROP TABLE scope_adoptions');
      await old.execute('DROP TABLE installation_binding');
      await old.update('store_metadata', {'value': 'lantern-offline-8'});
      await old.setVersion(8);
      await old.close();
      final before = await File(path).readAsBytes();
      expect(await open(p, transferAllowed: true), isA<SqliteRestoreHeld>());
      expect(await File(path).readAsBytes(), before);
    });
  }

  for (final suffix in ['', '-wal', '-shm', '-journal']) {
    test('I5 existing empty $suffix is not fresh', () async {
      await File('$path$suffix').writeAsBytes([]);
      final result = await open(policy()) as SqliteRestoreHeld;
      expect(result.reason, SqliteRestoreReason.incompleteStore);
      expect(await File('$path$suffix').length(), 0);
    });
  }

  test('I5 missing legacy assertion preserves all DB bytes', () async {
    final store = await SqliteOfflineStore.open(
      path: path,
      databaseFactory: factory,
    );
    await store.transaction((tx) => tx.setReplayPausedForAuth('legacy', true));
    await store.close();
    final before = await File(path).readAsBytes();
    final result =
        await open(
              policy(),
              adoption: SqliteScopeAdoption.verified(
                canonicalPath: SqliteOfflineStore.canonicalAutomaticPath(path),
                sourcePartition: 'legacy',
                destination: binding,
              ),
            )
            as SqliteRestoreHeld;
    expect(result.reason, SqliteRestoreReason.legacyAssertionMissing);
    expect(await File(path).readAsBytes(), before);
  });

  test(
    'I5 mismatch never overwrites marker or binds another authority',
    () async {
      await (await open(policy()) as SqliteSessionReady).session.close();
      client = LanternClient.connect(
        binding.serviceOrigin,
        allowInsecure: true,
      );
      final before = await File(path).readAsBytes();
      final held =
          await open(policy(id: 'different-installation')) as SqliteRestoreHeld;
      expect(held.reason, SqliteRestoreReason.installationMismatch);
      expect(await File(path).readAsBytes(), before);
    },
  );

  test('I5 policy failure is typed and does not create SQLite state', () async {
    await expectLater(
      open(
        SqliteInstallationPolicy(
          identity: 'test',
          version: 1,
          evaluate: (_) async => throw StateError('exclusion API failed'),
        ),
      ),
      throwsA(
        isA<SqliteStartupException>().having(
          (e) => e.reason,
          'reason',
          SqliteStartupFailure.policyFailure,
        ),
      ),
    );
    expect(await File(path).exists(), false);
  });

  test(
    'I6 matching provisional opens share; conflict cannot enter policy',
    () async {
      final entered = Completer<void>(), proceed = Completer<void>();
      var calls = 0, conflictingCalls = 0;
      final p = SqliteInstallationPolicy(
        identity: 'test',
        version: 1,
        evaluate: (_) async {
          calls++;
          entered.complete();
          await proceed.future;
          return SqliteInstallationEvidence.verified(
            installationId: 'installation',
          );
        },
      );
      final first = open(p);
      await entered.future;
      final second = open(p);
      await expectLater(
        open(
          SqliteInstallationPolicy(
            identity: 'test',
            version: 1,
            evaluate: (_) async {
              conflictingCalls++;
              return SqliteInstallationEvidence.verified(
                installationId: 'installation',
              );
            },
          ),
        ),
        throwsA(isA<OfflineOwnerConflictException>()),
      );
      await expectLater(
        SqliteOfflineStore.openAutomatic(path: path, databaseFactory: factory),
        throwsA(isA<OfflineOwnerConflictException>()),
      );
      expect(calls, 1);
      expect(conflictingCalls, 0);
      proceed.complete();
      final a = await first as SqliteSessionReady,
          b = await second as SqliteSessionReady;
      await a.session.close();
      expect(client.isClosed, false);
      await b.session.close();
      expect(client.isClosed, true);
    },
  );

  test(
    'I6 failed qualified connection close stays failed and blocks a successor before policy',
    () async {
      final f = _CloseFaultFactory();
      var policies = 0;
      final p = SqliteInstallationPolicy(
        identity: 'close-fault-host-fixture',
        version: 1,
        evaluate: (_) async {
          policies++;
          return SqliteInstallationEvidence.verified(
            installationId: 'installation',
          );
        },
      );
      final ready =
          await SqliteOfflineStore.openSession(
                path: path,
                client: client,
                binding: binding,
                installationPolicy: p,
                databaseFactory: f,
                transferAllowed: false,
              )
              as SqliteSessionReady;
      f.database.failClose = true;
      final first = ready.session.close();
      await expectLater(first, throwsStateError);
      expect(identical(first, ready.session.close()), true);
      await expectLater(ready.session.close(), throwsStateError);
      expect(f.database.closeCalls, 1);
      client = LanternClient.connect(
        binding.serviceOrigin,
        allowInsecure: true,
      );
      await expectLater(
        SqliteOfflineStore.openSession(
          path: path,
          client: client,
          binding: binding,
          installationPolicy: p,
          databaseFactory: f,
        ),
        throwsA(isA<OfflineOwnerConflictException>()),
      );
      expect(policies, 1);
      // Test-only release of the connection, not the retained native capability.
      // The blocked capability deliberately lives until this test process exits.
      await f.database.delegate.close();
    },
  );
}

final class _CloseFaultFactory implements DatabaseFactory {
  late _CloseFaultDatabase database;
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    final db = await databaseFactoryFfiNoIsolate.openDatabase(
      path,
      options: options,
    );
    if (options?.readOnly == true) return db;
    return database = _CloseFaultDatabase(db);
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

final class _CloseFaultDatabase implements Database {
  _CloseFaultDatabase(this.delegate);
  final Database delegate;
  bool failClose = false;
  int closeCalls = 0;
  @override
  String get path => delegate.path;
  @override
  Future<void> close() async {
    closeCalls++;
    if (failClose) throw StateError('injected connection close failure');
    await delegate.close();
  }

  @override
  Future<List<Map<String, Object?>>> rawQuery(
    String sql, [
    List<Object?>? arguments,
  ]) => delegate.rawQuery(sql, arguments);
  @override
  Future<List<Map<String, Object?>>> query(
    String table, {
    bool? distinct,
    List<String>? columns,
    String? where,
    List<Object?>? whereArgs,
    String? groupBy,
    String? having,
    String? orderBy,
    int? limit,
    int? offset,
  }) => delegate.query(
    table,
    distinct: distinct,
    columns: columns,
    where: where,
    whereArgs: whereArgs,
    groupBy: groupBy,
    having: having,
    orderBy: orderBy,
    limit: limit,
    offset: offset,
  );
  @override
  Future<T> transaction<T>(
    Future<T> Function(Transaction) action, {
    bool? exclusive,
  }) => delegate.transaction(action, exclusive: exclusive);
  @override
  Future<int> update(
    String table,
    Map<String, Object?> values, {
    String? where,
    List<Object?>? whereArgs,
    ConflictAlgorithm? conflictAlgorithm,
  }) => delegate.update(
    table,
    values,
    where: where,
    whereArgs: whereArgs,
    conflictAlgorithm: conflictAlgorithm,
  );
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
