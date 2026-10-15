// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

Future<OfflineAutomaticOwner> acquire(SqliteOfflineStore store) =>
    store.acquireAutomaticOwner(
      scope: 'scope',
      bindingFingerprint: 'binding',
      configurationFingerprint: 'configuration',
    );

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  late Directory root;
  setUp(() async {
    root = await Directory.systemTemp.createTemp('lantern-owner-test-');
  });
  tearDown(() => root.delete(recursive: true));

  test(
    'O3 failure after native reservation releases before any SQLite work',
    () async {
      final factory = _FaultFactory()..failOpen = true;
      final path = '${root.path}/db';
      await expectLater(
        SqliteOfflineStore.openAutomatic(path: path, databaseFactory: factory),
        throwsStateError,
      );
      expect(await File(path).exists(), false);
      factory.failOpen = false;
      final store = await SqliteOfflineStore.openAutomatic(
        path: path,
        databaseFactory: factory,
      );
      expect((await acquire(store)).epoch, 1);
      await store.close();
    },
  );

  for (final fault in ['beforeCommit', 'afterCommit']) {
    test(
      'O3 $fault preserves monotone history and permits next owner',
      () async {
        final factory = _FaultFactory();
        final path = '${root.path}/db';
        final store = await SqliteOfflineStore.openAutomatic(
          path: path,
          databaseFactory: factory,
        );
        factory.database.fault = fault;
        await expectLater(acquire(store), throwsStateError);
        await store.close();
        final next = await SqliteOfflineStore.openAutomatic(
          path: path,
          databaseFactory: factory,
        );
        expect((await acquire(next)).epoch, fault == 'beforeCommit' ? 1 : 2);
        await next.close();
      },
    );
  }

  test(
    'O3 row cleanup fault invalidates old guard and permits stale adoption',
    () async {
      final factory = _FaultFactory();
      final path = '${root.path}/db';
      final store = await SqliteOfflineStore.openAutomatic(
        path: path,
        databaseFactory: factory,
      );
      final owner = await acquire(store);
      factory.database.fault = 'beforeCommit';
      await expectLater(owner.release(), throwsStateError);
      expect(owner.checkCurrent, throwsA(isA<OfflineDisposedException>()));
      await expectLater(
        store.transaction((_) async => 1),
        throwsA(isA<OfflineDisposedException>()),
      );
      final next = await SqliteOfflineStore.openAutomatic(
        path: path,
        databaseFactory: factory,
      );
      expect((await acquire(next)).epoch, 2);
      await next.close();
      // Idempotent release reports its original cleanup failure, never re-closes FD.
      await expectLater(store.close(), throwsStateError);
    },
  );

  test(
    'O1 official SQLite handles share through aliases; only last close unlocks',
    () async {
      final path = '${root.path}/Shared.db';
      final client = LanternClient.connect(
        Uri.parse('http://127.0.0.1:6380'),
        allowInsecure: true,
      );
      final binding = LanternSessionBinding.application(
        serviceOrigin: client.endpoint,
        securityDomain: 'domain',
        accountIdentity: 'account',
        authorityBinding: 'authority',
      );
      final one = await SqliteOfflineStore.openSession(
        path: path,
        client: client,
        binding: binding,
        databaseFactory: databaseFactoryFfiNoIsolate,
      );
      final alias = '${root.path}/alias';
      await Link(alias).create(path);
      final two = await SqliteOfflineStore.openSession(
        path: alias,
        client: client,
        binding: binding,
        databaseFactory: databaseFactoryFfiNoIsolate,
      );
      OfflineLanternSession? caseAlias;
      final lower = '${root.path}/shared.db';
      if (await File(lower).exists()) {
        caseAlias = await SqliteOfflineStore.openSession(
          path: lower,
          client: client,
          binding: binding,
          databaseFactory: databaseFactoryFfiNoIsolate,
        );
      }
      await one.close();
      expect(client.isClosed, false);
      await expectLater(
        SqliteOfflineStore.openAutomatic(
          path: path,
          databaseFactory: databaseFactoryFfiNoIsolate,
        ),
        throwsA(isA<OfflineOwnerConflictException>()),
      );
      await caseAlias?.close();
      await two.close();
      expect(client.isClosed, true);
      final next = await SqliteOfflineStore.openAutomatic(
        path: path,
        databaseFactory: databaseFactoryFfiNoIsolate,
      );
      expect((await acquire(next)).epoch, 2);
      await next.close();
    },
  );

  test(
    'SQL6 migration preserves auth hold; new generation and policy survive reopen',
    () async {
      final path = '${root.path}/migration.db';
      final initial = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfiNoIsolate,
      );
      await initial.transaction(
        (tx) => tx.setReplayPausedForAuth('scope', true),
      );
      await initial.close();
      final old = await databaseFactoryFfiNoIsolate.openDatabase(path);
      await old.execute('DROP TABLE traversals');
      await old.execute('ALTER TABLE partitions DROP COLUMN traversal_epoch');
      await old.execute('DROP TABLE automatic_owner');
      await old.execute('ALTER TABLE partitions DROP COLUMN policy_paused');
      await old.execute(
        'ALTER TABLE partitions DROP COLUMN paused_credential_generation',
      );
      await old.update(
        'store_metadata',
        {'value': 'lantern-offline-6'},
        where: 'key=?',
        whereArgs: ['format'],
      );
      await old.setVersion(6);
      await old.close();
      final migrated = await SqliteOfflineStore.openAutomatic(
        path: path,
        databaseFactory: databaseFactoryFfiNoIsolate,
      );
      expect((await acquire(migrated)).epoch, 1);
      await migrated.transaction((tx) async {
        expect(await tx.replayPausedForAuth('scope'), true);
        expect(await tx.pausedCredentialGeneration('scope'), isNull);
        expect(await tx.replayPausedForPolicy('scope'), false);
        await tx.setReplayPausedForAuth('scope', true, credentialGeneration: 7);
        await tx.setReplayPausedForPolicy('scope', true);
      });
      await migrated.close();
      final next = await SqliteOfflineStore.openAutomatic(
        path: path,
        databaseFactory: databaseFactoryFfiNoIsolate,
      );
      expect((await acquire(next)).epoch, 2);
      await next.transaction((tx) async {
        expect(await tx.pausedCredentialGeneration('scope'), 7);
        expect(await tx.replayPausedForPolicy('scope'), true);
        await tx.setReplayPausedForAuth('scope', false);
        expect(await tx.pausedCredentialGeneration('scope'), isNull);
        expect(await tx.replayPausedForPolicy('scope'), true);
      });
      await next.close();
    },
  );

  test('manual store never fabricates an automatic capability', () async {
    final store = await SqliteOfflineStore.open(
      path: '${root.path}/db',
      databaseFactory: databaseFactoryFfiNoIsolate,
    );
    await expectLater(
      acquire(store),
      throwsA(isA<OfflineOwnerConflictException>()),
    );
    await store.close();
  });
}

final class _FaultFactory implements DatabaseFactory {
  bool failOpen = false;
  late _FaultDatabase database;
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    if (failOpen) throw StateError('injected open failure');
    return database = _FaultDatabase(
      await databaseFactoryFfiNoIsolate.openDatabase(path, options: options),
    );
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

final class _FaultDatabase implements Database {
  _FaultDatabase(this.delegate);
  final Database delegate;
  String? fault;
  @override
  String get path => delegate.path;
  @override
  Future<void> close() => delegate.close();
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
  Future<T> transaction<T>(
    Future<T> Function(Transaction) action, {
    bool? exclusive,
  }) async {
    final fail = fault;
    fault = null;
    final result = await delegate.transaction((tx) async {
      final result = await action(tx);
      if (fail == 'beforeCommit') throw StateError('injected before commit');
      return result;
    }, exclusive: exclusive);
    if (fail == 'afterCommit') throw StateError('injected after commit');
    return result;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
