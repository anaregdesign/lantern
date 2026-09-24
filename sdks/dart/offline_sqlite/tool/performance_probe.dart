import 'dart:convert';
import 'dart:io';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

/// Content-free host baseline, including logical WAL write amplification.
Future<void> main() async {
  sqfliteFfiInit();
  final directory = await Directory.systemTemp.createTemp('sqlite-baseline-');
  final path = '${directory.path}/store.db';
  const count = 100;
  final writes = <int>[];
  final reads = <int>[];
  var payloadBytes = 0;
  final factory = _WalFactory(databaseFactoryFfi);
  SqliteOfflineStore? store;
  OfflineLanternRepository? repository;
  try {
    store = await SqliteOfflineStore.open(path: path, databaseFactory: factory);
    final initialWalBytes = await _size('$path-wal');
    final now = DateTime.utc(2026, 9, 24);
    for (var index = 0; index < count; index++) {
      final record = OfflineCacheRecord.value(
        partitionId: 'baseline',
        generation: 0,
        key: OfflineEntityKey.vertex('key-$index'),
        entity: Vertex(
          key: 'key-$index',
          value: VertexValue.string('x' * 1024),
          expiration: null,
        ),
        validatedAt: now,
        lastAccessAt: now,
      );
      payloadBytes += utf8
          .encode(OfflineCodec.encodeCacheRecord(record))
          .length;
      final stopwatch = Stopwatch()..start();
      await store.transaction((t) => t.putCache('baseline', record));
      writes.add(stopwatch.elapsedMicroseconds);
    }
    final walBytes = (await _size('$path-wal')) - initialWalBytes;
    for (var index = 0; index < count; index++) {
      final stopwatch = Stopwatch()..start();
      final record = await store.transaction(
        (t) => t.getCache('baseline', OfflineEntityKey.vertex('key-$index')),
      );
      if (record == null) throw StateError('baseline_missing');
      reads.add(stopwatch.elapsedMicroseconds);
    }
    await store.close();
    final diskBytes = await _size(path);
    final restart = Stopwatch()..start();
    store = await SqliteOfflineStore.open(path: path, databaseFactory: factory);
    final restartUs = restart.elapsedMicroseconds;
    final lifecycleWalBefore = await _size('$path-wal');
    repository = OfflineLanternRepository(
      store: store,
      remote: _ImmediateRemote(),
      config: OfflineConfig(clock: () => now, jitter: (_) => Duration.zero),
    );
    final enqueue = Stopwatch()..start();
    final operation = await repository.putVertices(
      partitionId: 'lifecycle',
      inputs: List.generate(
        count,
        (index) => VertexInput(
          key: 'pending-$index',
          value: VertexValue.int32(index),
          expiresIn: const Duration(hours: 1),
        ),
      ),
    );
    enqueue.stop();
    final replay = Stopwatch()..start();
    final confirmed = await repository.drain('lifecycle');
    replay.stop();
    final status = await repository.getWriteStatus(
      'lifecycle',
      operation.operationId,
    );
    if (confirmed != count || status?.confirmedCount != count) {
      throw StateError('baseline_replay');
    }
    final lifecycleWalBytes = (await _size('$path-wal')) - lifecycleWalBefore;
    final result = {
      'contentFree': true,
      'platform': Platform.operatingSystem,
      'revision': Platform.environment['GITHUB_SHA'] ?? 'local-working-tree',
      'records': count,
      'journal': 'wal',
      'synchronous': 'full',
      'cache_write_us': _percentiles(writes),
      'cache_read_us': _percentiles(reads),
      'reopen_us': restartUs,
      'database_bytes': diskBytes,
      'database_bytes_per_record': diskBytes / count,
      'canonical_payload_bytes': payloadBytes,
      'wal_growth_bytes': walBytes,
      'wal_growth_per_payload_byte': walBytes / payloadBytes,
      'lifecycle_items': count,
      'enqueue_batch_us': enqueue.elapsedMicroseconds,
      'replay_batch_us': replay.elapsedMicroseconds,
      'replay_remote': 'immediate_stub',
      'lifecycle_wal_growth_bytes': lifecycleWalBytes,
      'rss_bytes': ProcessInfo.currentRss,
    };
    stdout.writeln(jsonEncode(result));
    // Broad host regression budgets, fixed before the initial measurement.
    if (writes.any((us) => us > 1000000) ||
        reads.any((us) => us > 1000000) ||
        restartUs > 5000000 ||
        diskBytes > 8 * 1024 * 1024 ||
        walBytes > 64 * 1024 * 1024 ||
        enqueue.elapsedMilliseconds > 5000 ||
        replay.elapsedMilliseconds > 30000 ||
        lifecycleWalBytes > 128 * 1024 * 1024) {
      throw StateError('baseline_budget');
    }
  } finally {
    await repository?.dispose();
    await store?.close();
    await directory.delete(recursive: true);
  }
}

final class _ImmediateRemote implements OfflineRemote {
  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) async => const OfflineRemoteMissing<Edge>();

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async => const OfflineRemoteMissing<Vertex>();

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) async {}

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async => PutOutcome.appliedAndLive;

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async => PutOutcome.appliedAndLive;
}

Map<String, int> _percentiles(List<int> values) {
  values.sort();
  return {
    for (final p in [50, 95, 99])
      'p$p': values[((values.length * p / 100).ceil() - 1)],
  };
}

Future<int> _size(String path) async =>
    await File(path).exists() ? await File(path).length() : 0;

final class _WalFactory implements DatabaseFactory {
  _WalFactory(this.delegate);
  final DatabaseFactory delegate;
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    final database = await delegate.openDatabase(path, options: options);
    await database.rawQuery('PRAGMA journal_mode = WAL');
    await database.rawQuery('PRAGMA wal_autocheckpoint = 0');
    return database;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
