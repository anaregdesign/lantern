// @dart=3.11
// A single named part supplies identical contracts to Dart and Flutter runners.
// ignore: unnecessary_library_name
library traversal_contract_tests;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as wire;
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

part '../../offline/test/support/traversal_contract.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  late Directory directory;
  final stores = <SqliteOfflineStore>[];
  setUpAll(
    () async => directory = await Directory.systemTemp.createTemp(
      'traversal-contract-',
    ),
  );
  tearDownAll(() async {
    for (final store in stores.reversed) {
      await store.close();
    }
    await directory.delete(recursive: true);
  });

  test(
    'SQLite7 migration is atomic and preserves pending bytes; downgrade fails',
    () async {
      final path = '${directory.path}/migration7.db';
      final initial = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      stores.add(initial);
      final h = TraversalFixture(initial);
      await h.open();
      await h.data!.mutations.submit(
        MutationRequest.putVertices([
          VertexInput(key: 'pending', value: VertexValue.string('original')),
        ]),
      );
      await h.close();
      await initial.close();
      final old = await databaseFactoryFfi.openDatabase(path);
      final before = (await old.query('outbox')).single['payload'] as List<int>;
      await old.execute('DROP TABLE traversals');
      await old.execute('ALTER TABLE partitions DROP COLUMN traversal_epoch');
      await old.update(
        'store_metadata',
        {'value': 'lantern-offline-7'},
        where: 'key=?',
        whereArgs: ['format'],
      );
      await old.setVersion(7);
      await old.close();
      await expectLater(
        SqliteOfflineStore.open(
          path: path,
          databaseFactory: _FailUpgradeFactory(databaseFactoryFfi),
        ),
        throwsStateError,
      );
      final rolledBack = await databaseFactoryFfi.openDatabase(path);
      expect(await rolledBack.getVersion(), 7);
      expect(
        (await rolledBack.query('store_metadata')).single['value'],
        'lantern-offline-7',
      );
      expect(
        (await rolledBack.rawQuery(
          "SELECT name FROM sqlite_master WHERE name='traversals'",
        )),
        isEmpty,
      );
      expect((await rolledBack.query('outbox')).single['payload'], before);
      await rolledBack.close();
      final migrated = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      stores.add(migrated);
      await migrated.close();
      final check = await databaseFactoryFfi.openDatabase(path);
      expect(await check.getVersion(), 8);
      expect((await check.query('outbox')).single['payload'], before);
      expect(await check.query('traversals'), isEmpty);
      await check.setVersion(9);
      await check.close();
      await expectLater(
        SqliteOfflineStore.open(
          path: path,
          databaseFactory: databaseFactoryFfi,
        ),
        throwsA(isA<OfflineSchemaException>()),
      );
    },
  );

  traversalContract(
    create: () async {
      final store = await SqliteOfflineStore.open(
        path: '${directory.path}/${stores.length}.db',
        databaseFactory: databaseFactoryFfi,
      );
      stores.add(store);
      return store;
    },
    reopen: (previous) async {
      final old = previous as SqliteOfflineStore;
      await old.close();
      final store = await SqliteOfflineStore.open(
        path: old.path,
        databaseFactory: databaseFactoryFfi,
      );
      stores.add(store);
      return store;
    },
  );
}

final class _FailUpgradeFactory implements DatabaseFactory {
  _FailUpgradeFactory(this.inner);
  final DatabaseFactory inner;
  @override
  Future<Database> openDatabase(String path, {OpenDatabaseOptions? options}) =>
      inner.openDatabase(
        path,
        options: OpenDatabaseOptions(
          version: options!.version,
          singleInstance: options.singleInstance,
          onConfigure: options.onConfigure,
          onCreate: options.onCreate,
          onDowngrade: options.onDowngrade,
          onUpgrade: (db, old, next) async {
            await options.onUpgrade!(db, old, next);
            throw StateError('injected migration commit failure');
          },
        ),
      );
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
