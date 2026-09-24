import 'dart:convert';
import 'dart:io';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

const _partition = 'claim-probe';
const _recordId = 'record';
const _operationId = 'operation';
final _now = DateTime.utc(2026, 9, 24);
final _expiration = _now.add(const Duration(hours: 1));

/// Child process for claim_probe.dart; protocol output contains no store data.
Future<void> main(List<String> arguments) async {
  SqliteOfflineStore? store;
  try {
    _require(arguments.length == 3);
    final [mode, path, owner] = arguments;
    _require(['seed', 'actor', 'verify'].contains(mode));
    _require(owner == 'a' || owner == 'b');
    _emit({'event': 'started', 'pid': pid});
    sqfliteFfiInit();
    store = await SqliteOfflineStore.open(
      path: path,
      databaseFactory: databaseFactoryFfi,
    );
    if (mode == 'seed') {
      await _seed(store);
      _emit({'event': 'seeded'});
      return;
    }
    if (mode == 'verify') {
      await _verify(store, owner);
      _emit({'event': 'verified'});
      return;
    }
    _emit({'event': 'ready'});
    await for (final command
        in stdin.transform(utf8.decoder).transform(const LineSplitter())) {
      switch (command) {
        case 'claim':
          final count = await _claim(store, owner, _now);
          _emit({'event': 'claimed', 'count': count});
        case 'renew':
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
          final count = await _claim(
            store,
            owner,
            _now.add(const Duration(seconds: 15)),
          );
          _emit({'event': 'blocked', 'count': count});
        case 'recover':
          final count = await _claim(
            store,
            owner,
            _now.add(const Duration(seconds: 32)),
          );
          _emit({'event': 'recovered', 'count': count});
        case 'stale-renew':
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
          await _verify(store, owner);
          _emit({'event': 'verified'});
        case 'stop':
          _emit({'event': 'stopped'});
          return;
        default:
          throw StateError('protocol');
      }
    }
    throw StateError('unexpected_eof');
  } catch (_) {
    stderr.writeln('claim_worker_failed');
    exitCode = 1;
  } finally {
    await store?.close();
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
      _require(rows.length == 1);
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
      );
      final operations = await transaction.operations(_partition);
      _require(operations.length == 1);
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
      );
    });

void _require(bool condition) {
  if (!condition) throw StateError('claim_contract');
}

void _emit(Map<String, Object?> event) => stdout.writeln(jsonEncode(event));
