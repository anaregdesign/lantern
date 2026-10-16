import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/installation_policy.dart';
import 'package:lantern_example/native_session_controller.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'native_test_helpers.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  for (final failCleanup in [true, false]) {
    test(
      'R2 real SQLite pre-return validation; cleanup failure=$failCleanup',
      () async {
        final dir = await Directory.systemTemp.createTemp(
          'lantern-startup-fault-',
        );
        final factory = _StartupFaultFactory(failCleanup);
        final store = MemoryCredentials(), providers = Providers();
        final wire = NativeWire(store);
        final policy = SqliteInstallationPolicy(
          identity: 'startup-host-test',
          version: 1,
          evaluate: (_) async =>
              SqliteInstallationEvidence.verified(installationId: 'host-test'),
        );
        final opened = <String>[];
        AppDataStartupFailure? failure;
        LanternSessionBinding? firstBinding;
        final c = NativeSessionController(
          endpoint: endpoint,
          providers: providers,
          store: store,
          createClient: (credential) => LanternClient.connect(
            endpoint,
            transport: wire,
            tokenProvider: () => credential,
          ),
          openData: (client, session, fresh, lifetime) async {
            final subject = session.principal.subject;
            opened.add(subject);
            final binding = LanternSessionBinding.native(
              session,
              securityDomain: 'startup-fault',
            );
            firstBinding ??= binding;
            try {
              return await AppDataSessionFactory(
                client: client,
                binding: binding,
                profile: AppDataProfile.durable,
                legacyPartition: subject,
                lifetime: lifetime,
                databaseFactory: subject == 'a'
                    ? factory
                    : databaseFactoryFfiNoIsolate,
                resolveLocation: () async =>
                    AppSqliteLocation('${dir.path}/$subject.db', policy, null),
              ).open(activate: false);
            } on AppDataStartupFailure catch (error) {
              failure = error;
              rethrow;
            }
          },
        );
        await c.login(issuer, 'mobile');
        expect(c.account, isNull);
        expect(factory.database.closeCalls, 1);
        expect(wire.dataDispatches, isEmpty);
        providers.subject = 'b';
        await c.login(issuer, 'mobile');
        if (failCleanup) {
          expect(failure!.startupError, isA<OfflineSchemaException>());
          expect(failure!.retirementError, same(factory.retirementError));
          expect(
            failure.toString(),
            'AppDataStartupFailure(retirementBlocked)',
          );
          expect(opened, ['a']);
          expect(providers.authentications, 1);
          expect(c.account, isNull);
          await expectLater(c.close(), throwsA(same(failure)));
          final otherClient = LanternClient.connect(endpoint, transport: wire);
          // Core reservation remains held, as does the native guard independently.
          await expectLater(
            SqliteOfflineStore.openSession(
              path: '${dir.path}/a.db',
              client: otherClient,
              binding: firstBinding!,
              installationPolicy: policy,
              databaseFactory: databaseFactoryFfiNoIsolate,
            ),
            throwsA(isA<OfflineOwnerConflictException>()),
          );
          await expectLater(
            SqliteOfflineStore.openAutomatic(
              path: '${dir.path}/a.db',
              databaseFactory: databaseFactoryFfiNoIsolate,
            ),
            throwsA(isA<OfflineOwnerConflictException>()),
          );
          await otherClient.close();
          // Test process cleanup only; the retained guard is not released/reused.
          await factory.database.delegate.close();
        } else {
          expect(failure, isNull);
          expect(opened, ['a', 'b']);
          expect(c.account!.session.principal.subject, 'b');
          await c.close();
          // Successful cleanup allows the same path to acquire its native guard.
          // It still fails the real schema check and does not admit corrupt data.
          await expectLater(
            SqliteOfflineStore.openAutomatic(
              path: '${dir.path}/a.db',
              databaseFactory: databaseFactoryFfiNoIsolate,
            ),
            throwsA(isA<OfflineSchemaException>()),
          );
        }
        await dir.delete(recursive: true);
      },
    );
  }
}

final class _StartupFaultFactory implements DatabaseFactory {
  _StartupFaultFactory(this.failCleanup);
  final bool failCleanup;
  final retirementError = StateError('injected close failure');
  late _StartupFaultDatabase database;
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    final delegate = await databaseFactoryFfiNoIsolate.openDatabase(
      path,
      options: options,
    );
    // Corrupt actual on-disk metadata before the adapter's post-open validation.
    // This is before any SqliteOfflineStore or automatic session is returned.
    await delegate.execute(
      "UPDATE store_metadata SET value='invalid' WHERE key='format'",
    );
    return database = _StartupFaultDatabase(
      delegate,
      failCleanup ? retirementError : null,
    );
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

final class _StartupFaultDatabase implements Database {
  _StartupFaultDatabase(this.delegate, this.failure);
  final Database delegate;
  final Object? failure;
  int closeCalls = 0;
  @override
  Future<void> close() async {
    closeCalls++;
    if (failure != null) throw failure!;
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
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
