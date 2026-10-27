import 'dart:convert';
import 'dart:io';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'claim_protocol.dart';

const _partition = 'claim-probe';
const _recordId = 'record';
const _operationId = 'operation';
final _now = DateTime.utc(2026, 9, 24);
final _expiration = _now.add(const Duration(hours: 1));

/// Child process for claim_probe.dart; protocol output contains no store data.
Future<void> main(List<String> arguments) async {
  SqliteOfflineStore? store;
  var stage = ClaimWorkerStage.arguments;
  void phase(ClaimWorkerStage value) {
    stage = value;
    stderr.writeln('\n${jsonEncode(claimWorkerPhase(value))}');
  }

  void fail(Object error) {
    final marker = claimWorkerFailure(stage, error);
    _emit(marker);
    stderr.writeln('\n${jsonEncode(marker)}');
    exitCode = 1;
  }

  phase(stage);
  try {
    _require(arguments.length == 3, ClaimReason.arguments);
    final [mode, path, owner] = arguments;
    _require(
      ['seed', 'actor', 'verify'].contains(mode),
      ClaimReason.workerMode,
    );
    _require(owner == 'a' || owner == 'b', ClaimReason.workerOwner);
    _emit({'event': 'started', 'pid': pid});
    phase(ClaimWorkerStage.opening);
    sqfliteFfiInit();
    store = await SqliteOfflineStore.open(
      path: path,
      databaseFactory: databaseFactoryFfi,
    );
    if (mode == 'seed') {
      phase(ClaimWorkerStage.seed);
      await _seed(store);
      _emit({'event': 'seeded'});
      return;
    }
    if (mode == 'verify') {
      phase(ClaimWorkerStage.verify);
      await _verify(store, owner);
      _emit({'event': 'verified'});
      return;
    }
    phase(ClaimWorkerStage.ready);
    _emit({'event': 'ready'});
    await for (final command
        in stdin.transform(utf8.decoder).transform(const LineSplitter())) {
      switch (command) {
        case 'claim':
          phase(ClaimWorkerStage.claim);
          final count = await _claim(store, owner, _now);
          _emit({'event': 'claimed', 'count': count});
        case 'renew':
          phase(ClaimWorkerStage.renew);
          final renewed = await store.transaction(
            (transaction) => transaction.renewLease(
              _partition,
              _recordId,
              owner: owner,
              generation: 0,
              now: _now.add(const Duration(seconds: 1)),
              leaseDuration: const Duration(seconds: 30),
            ),
          );
          _emit({'event': 'renewed', 'accepted': renewed});
        case 'before-expiry':
          phase(ClaimWorkerStage.beforeExpiry);
          final count = await _claim(
            store,
            owner,
            _now.add(const Duration(seconds: 15)),
          );
          _emit({'event': 'blocked', 'count': count});
        case 'recover':
          phase(ClaimWorkerStage.recover);
          final count = await _claim(
            store,
            owner,
            _now.add(const Duration(seconds: 32)),
          );
          _emit({'event': 'recovered', 'count': count});
        case 'stale-renew':
          phase(ClaimWorkerStage.staleRenew);
          final renewed = await store.transaction(
            (transaction) => transaction.renewLease(
              _partition,
              _recordId,
              owner: owner == 'a' ? 'b' : 'a',
              generation: 0,
              now: _now.add(const Duration(seconds: 33)),
              leaseDuration: const Duration(seconds: 30),
            ),
          );
          _emit({'event': 'stale-renewed', 'accepted': renewed});
        case 'verify':
          phase(ClaimWorkerStage.verify);
          await _verify(store, owner);
          _emit({'event': 'verified'});
        case 'stop':
          phase(ClaimWorkerStage.stop);
          _emit({'event': 'stopped'});
          return;
        default:
          throw const ClaimProblem(ClaimReason.command);
      }
    }
    throw const ClaimProblem(ClaimReason.unexpectedEof);
  } on Object catch (error) {
    fail(error);
  } finally {
    phase(ClaimWorkerStage.closing);
    try {
      await store?.close();
    } on Object catch (error) {
      fail(error);
    }
  }
}

Future<void> _seed(SqliteOfflineStore store) => store.transaction((
  transaction,
) async {
  final record = await transaction.enqueue(
    OfflineOutboxRecord(
      recordId: _recordId,
      operationId: _operationId,
      itemIndex: 0,
      partitionId: _partition,
      intent: OfflinePutVertexIntent(
        Vertex(
          key: 'fixture',
          value: VertexValue.int64(-0x8000000000000000),
          expiration: _expiration,
        ),
      ),
      enqueuedAt: _now,
      ordinal: 0,
      state: OfflineOutboxState.enqueued,
      attemptCount: 0,
      generation: 0,
    ),
  );
  await _status(transaction, record, _now, OfflineWriteState.locallyCommitted);
});

Future<int> _claim(SqliteOfflineStore store, String owner, DateTime now) =>
    store.transaction((transaction) async {
      final records = await transaction.claim(
        _partition,
        owner: owner,
        now: now,
        maxAge: const Duration(days: 1),
        leaseDuration: const Duration(seconds: 10),
        limit: 1,
      );
      for (final record in records) {
        await _status(transaction, record, now, OfflineWriteState.sending);
      }
      return records.length;
    });

Future<void> _status(
  OfflineStoreTransaction transaction,
  OfflineOutboxRecord record,
  DateTime now,
  OfflineWriteState state,
) async {
  await transaction.putOperation(
    OfflineOperationRecord(
      partitionId: _partition,
      generation: 0,
      operationId: _operationId,
      items: [
        OfflineWriteStatus(
          recordId: record.recordId,
          operationId: record.operationId,
          itemIndex: 0,
          state: state,
          attemptCount: 0,
        ),
      ],
      updatedAt: now,
    ),
  );
}

Future<void> _verify(SqliteOfflineStore store, String owner) =>
    store.transaction((transaction) async {
      final rows = await transaction.outbox(_partition);
      _require(rows.length == 1, ClaimReason.outboxCount);
      final expected = OfflineOutboxRecord(
        recordId: _recordId,
        operationId: _operationId,
        itemIndex: 0,
        partitionId: _partition,
        intent: OfflinePutVertexIntent(
          Vertex(
            key: 'fixture',
            value: VertexValue.int64(-0x8000000000000000),
            expiration: _expiration,
          ),
        ),
        enqueuedAt: _now,
        ordinal: 1,
        state: OfflineOutboxState.sending,
        attemptCount: 0,
        generation: 0,
        leaseOwner: owner,
        leaseUntil: _now.add(const Duration(seconds: 42)),
      );
      _require(
        OfflineCodec.encodeOutboxRecord(rows.single) ==
            OfflineCodec.encodeOutboxRecord(expected),
        ClaimReason.outboxRecord,
      );
      final operations = await transaction.operations(_partition);
      _require(operations.length == 1, ClaimReason.operationCount);
      final expectedOperation = OfflineOperationRecord(
        partitionId: _partition,
        generation: 0,
        operationId: _operationId,
        items: [
          OfflineWriteStatus(
            recordId: _recordId,
            operationId: _operationId,
            itemIndex: 0,
            state: OfflineWriteState.sending,
            attemptCount: 0,
          ),
        ],
        updatedAt: _now.add(const Duration(seconds: 32)),
      );
      _require(
        OfflineCodec.encodeOperationRecord(operations.single) ==
            OfflineCodec.encodeOperationRecord(expectedOperation),
        ClaimReason.operationRecord,
      );
    });

void _require(bool condition, ClaimReason reason) {
  if (!condition) {
    throw ClaimProblem(reason);
  }
}

void _emit(Map<String, Object?> event) => stdout.writeln(jsonEncode(event));
