import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as wire;
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

/// Real replies, a real SQLite rollback, and the qualified automatic owner.
/// The two callers supply existing disclosed and Head write-only fixtures.
Future<void> verifyConfirmationRollback({
  required LanternClient observer,
  required LanternClient Function(connect.Interceptor) openWriter,
  required EdgeRef pair,
  required bool undisclosed,
}) async {
  sqfliteFfiInit();
  final calls = <String>[];
  final statusIds = <List<int>>[];
  final factory = _ConfirmationFactory(
    undisclosed
        ? OfflineWriteState.acceptedUndisclosed
        : OfflineWriteState.confirmed,
  );
  var observedCommittedMarker = false;
  connect.AnyFn<I, O> intercept<I extends Object, O extends Object>(
    connect.AnyFn<I, O> next,
  ) => (request) async {
    final method = request.spec.procedure.split('/').last;
    calls.add(method);
    if (method == 'AddEdges') {
      expect(
        statusIds,
        isEmpty,
        reason: 'durably proven first send skips status',
      );
      final row = await factory.outbox();
      expect(row.mayHaveDispatched, true);
      expect(row.receipt!.mayHaveDispatched, true);
      expect(row.attemptCount, 1);
      observedCommittedMarker = true;
    }
    if (request is connect.UnaryRequest<I, O> &&
        request.message is wire.GetReceiptStatusesRequest) {
      statusIds.addAll(
        (request.message as wire.GetReceiptStatusesRequest).operationIds.map(
          List<int>.of,
        ),
      );
    }
    return next(request);
  };
  var writer = openWriter(intercept);
  addTearDown(writer.close);
  await observer.putVertices([
    for (final key in [pair.tail, pair.head])
      VertexInput(
        key: key,
        value: const StringValue('endpoint'),
        expiresIn: const Duration(minutes: 5),
      ),
  ]);
  await observer.putEdge(
    EdgeInput(tail: pair.tail, head: pair.head, weight: 2),
  );
  final directory = await Directory.systemTemp.createTemp('confirmation-wire-');
  addTearDown(() => directory.delete(recursive: true));
  var clockOffset = Duration.zero;
  DateTime now() => DateTime.now().toUtc().add(clockOffset);
  final binding = LanternSessionBinding.application(
    serviceOrigin: writer.endpoint,
    securityDomain: 'confirmation-wire',
    accountIdentity: undisclosed ? 'head-writer' : 'writer',
    authorityBinding: 'a',
  );
  final policy = SqliteInstallationPolicy(
    identity: 'confirmation-host-fixture',
    version: 1,
    evaluate: (_) async => SqliteInstallationEvidence.verified(
      installationId: 'isolated-host-installation',
    ),
  );
  Future<OfflineLanternSession> open() async =>
      (await SqliteOfflineStore.openSession(
                path: '${directory.path}/data.db',
                client: writer,
                binding: binding,
                installationPolicy: policy,
                databaseFactory: factory,
                transferAllowed: false,
                clock: now,
              )
              as SqliteSessionReady)
          .session;
  var session = await open();
  addTearDown(() => session.close());
  final ticket = await session.mutations.submit(
    MutationRequest.addEdges([
      EdgeInput(
        tail: pair.tail,
        head: pair.head,
        weight: 3,
        expiresIn: const Duration(minutes: 5),
      ),
    ]),
    options: const MutationOptions(wait: WaitOptions()),
  );
  factory.operation = ticket.reference.operationId.encoded;
  factory.partition = binding.stableIdentity;
  await session.setTransferAllowed(true);
  final live = await ticket.completion!.timeout(
    const Duration(seconds: 5),
    onTimeout: () async {
      final row = await factory.outbox();
      throw StateError(
        'live reply pending: $calls; state=${row.state}, '
        'diagnostic=${row.diagnosticCode}, receipt=${row.receipt?.state}',
      );
    },
  );
  void expectReply(MutationObservation<double> observed) {
    final item = observed.reply!.items.single;
    if (undisclosed) {
      expect(item, isA<MutationAcceptedUndisclosed<double>>());
    } else {
      expect((item as MutationKnownEffect<double>).effect, 5);
    }
  }

  expectReply(live);
  expect(observedCommittedMarker, true);
  expect(statusIds, isEmpty);
  await factory.failed.future.timeout(
    const Duration(seconds: 5),
    onTimeout: () =>
        throw StateError('confirmation transaction did not roll back'),
  );
  expect(factory.rollbacks, 1);
  final before = factory.before!;
  expect(factory.confirmed.isCompleted, isFalse);
  expect(before.mayHaveDispatched, isTrue);
  expect(before.receipt, isNotNull);
  final retained = await factory.outbox();
  expect(
    OfflineCodec.encodeOutboxRecord(retained),
    OfflineCodec.encodeOutboxRecord(before),
  );
  final operation = await factory.operationRecord();
  expect(operation.items.single.state, OfflineWriteState.sending);
  expect(operation.terminalAt, isNull);
  expect(await factory.database.query('cache'), isEmpty);
  expect((await observer.getEdge(pair)).weight, 5);
  final afterReply = List<String>.of(calls);
  await expectLater(
    session.mutations.submit(
      MutationRequest.addEdges([
        EdgeInput(tail: pair.tail, head: pair.head, weight: 1),
      ]),
    ),
    throwsA(
      isA<LanternDataException>().having(
        (e) => e.reason,
        'reason',
        DataFailure.storageBlocked,
      ),
    ),
  );
  expect(calls, afterReply, reason: 'storage fault blocks another unsafe send');
  await session.close();
  // A storage-blocked pass conservatively retains its original claim lease.
  // Move only the injected local clock past that fixed lease; IDs and absolute
  // intent TTL stay byte-for-byte original, and the real Server is unchanged.
  clockOffset =
      before.leaseUntil!.difference(DateTime.now().toUtc()) +
      const Duration(milliseconds: 1);
  writer = openWriter(intercept);
  addTearDown(writer.close);
  session = await open();
  final reopened = await factory.outbox();
  expect(reopened.receipt!.operationId, before.receipt!.operationId);
  expect(reopened.receipt!.groupId, before.receipt!.groupId);
  final originalIntent = before.intent as OfflineReceiptAddEdgeIntent;
  final reopenedIntent = reopened.intent as OfflineReceiptAddEdgeIntent;
  expect(reopenedIntent.contributionId, originalIntent.contributionId);
  expect(reopenedIntent.edge.tail, originalIntent.edge.tail);
  expect(reopenedIntent.edge.head, originalIntent.edge.head);
  expect(reopenedIntent.edge.weight, originalIntent.edge.weight);
  expect(reopenedIntent.edge.expiration, originalIntent.edge.expiration);
  final offset = calls.length;
  await session.setTransferAllowed(true);
  final recovered = await session.mutations
      .reobserve(ticket.reference)
      .timeout(
        const Duration(seconds: 5),
        onTimeout: () async {
          final row = await factory.outbox();
          throw StateError(
            'recovery pending: ${calls.skip(offset).toList()}; '
            'state=${row.state}, lease=${row.leaseUntil}, next=${row.nextAttemptAt}, '
            'diagnostic=${row.diagnosticCode}, now=${now()}',
          );
        },
      );
  expectReply(recovered);
  await factory.confirmed.future.timeout(const Duration(seconds: 5));
  final recoveryCalls = calls.skip(offset).toList();
  expect(recoveryCalls.first, 'GetReceiptStatuses');
  expect(statusIds, [before.receipt!.operationId.bytes]);
  expect(calls.where((m) => m == 'AddEdges'), hasLength(1));
  expect((await observer.getEdge(pair)).weight, 5);
  expect(
    (await factory.operationRecord()).items.single.state,
    undisclosed
        ? OfflineWriteState.acceptedUndisclosed
        : OfflineWriteState.confirmed,
  );
  expect(await factory.database.query('outbox'), isEmpty);
}

final class _ConfirmationFactory implements DatabaseFactory {
  _ConfirmationFactory(this.expectedState);
  final OfflineWriteState expectedState;
  final failed = Completer<void>();
  final confirmed = Completer<void>();
  String? operation, partition;
  int rollbacks = 0;
  OfflineOutboxRecord? before;
  late _ConfirmationDatabase database;
  Future<OfflineOutboxRecord> outbox() async => OfflineCodec.decodeOutboxRecord(
    utf8.decode(
      (await database.query('outbox')).single['payload']! as Uint8List,
    ),
  );
  Future<OfflineOperationRecord> operationRecord() async =>
      OfflineCodec.decodeOperationRecord(
        utf8.decode(
          (await database.query('operations')).single['payload']! as Uint8List,
        ),
      );
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    final opened = await databaseFactoryFfi.openDatabase(
      path,
      options: options,
    );
    // Installation inspection uses sqflite's native getVersion extension.
    // Only the writable store connection needs the transaction fault wrapper.
    if (options?.readOnly == true) return opened;
    return database = _ConfirmationDatabase(opened, this);
  }

  @override
  Future<String> getDatabasesPath() => databaseFactoryFfi.getDatabasesPath();
  @override
  Future<bool> databaseExists(String path) =>
      databaseFactoryFfi.databaseExists(path);
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

final class _ConfirmationDatabase implements Database {
  _ConfirmationDatabase(this.delegate, this.fault);
  final Database delegate;
  final _ConfirmationFactory fault;
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
    var injected = false, stagedSuccess = false;
    try {
      final committed = await delegate.transaction((tx) async {
        final id = fault.operation, scope = fault.partition;
        final before = id == null || scope == null
            ? <Map<String, Object?>>[]
            : await tx.query(
                'outbox',
                where: 'partition_id=? AND operation_id=?',
                whereArgs: [scope, id],
              );
        final previous = before.isEmpty
            ? null
            : OfflineCodec.decodeOperationRecord(
                utf8.decode(
                  (await tx.query(
                        'operations',
                        where: 'partition_id=? AND operation_id=?',
                        whereArgs: [scope, id],
                      )).single['payload']!
                      as Uint8List,
                ),
              );
        final result = await action(tx);
        if (previous != null && previous.terminalAt == null) {
          final rows = await tx.query(
            'operations',
            where:
                'partition_id=? AND operation_id=? AND terminal_at IS NOT NULL',
            whereArgs: [scope, id],
          );
          if (rows.isNotEmpty) {
            final staged = OfflineCodec.decodeOperationRecord(
              utf8.decode(rows.single['payload']! as Uint8List),
            );
            final retained = OfflineCodec.decodeOutboxRecord(
              utf8.decode(before.single['payload']! as Uint8List),
            );
            expect(staged.partitionId, scope);
            expect(staged.operationId, id);
            expect(staged.items.single.state, fault.expectedState);
            expect(staged.items.single.itemIndex, 0);
            expect(staged.items.single.recordId, retained.recordId);
            expect(previous.items.single.recordId, retained.recordId);
            expect(
              await tx.query(
                'outbox',
                where: 'partition_id=? AND operation_id=?',
                whereArgs: [scope, id],
              ),
              isEmpty,
            );
            stagedSuccess = true;
            if (fault.rollbacks == 0) {
              fault.before = retained;
              fault.rollbacks++;
              injected = true;
              throw StateError(
                'injected confirmation rollback before SQLite commit',
              );
            }
          }
        }
        return result;
      }, exclusive: exclusive);
      // A callback return is only staged success. Join the native commit first.
      if (stagedSuccess && !fault.confirmed.isCompleted) {
        fault.confirmed.complete();
      }
      return committed;
    } catch (_) {
      if (injected && !fault.failed.isCompleted) {
        fault.failed.complete();
      }
      rethrow;
    }
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
