import 'dart:convert';
import 'dart:io';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

Future<void> main(List<String> arguments) async {
  final outputPath = _outputPath(arguments);
  final baseline =
      jsonDecode(await File('tool/performance_baseline.json').readAsString())
          as Map<String, Object?>;
  final limits = baseline['limits']! as Map<String, Object?>;
  final now = DateTime.utc(2026, 1, 1);
  final rssBefore = ProcessInfo.currentRss;
  final store = InMemoryOfflineStore(
    limits: const OfflineStoreLimits(
      maxOutboxRecords: 1100,
      maxOutboxRecordsPerPartition: 1100,
    ),
  );
  final repository = OfflineLanternRepository(
    store: store,
    remote: _ImmediateRemote(),
    config: OfflineConfig(
      clock: () => now,
      maxConcurrency: 32,
      jitter: (_) => Duration.zero,
    ),
  );

  final enqueueWatch = Stopwatch()..start();
  final operation = await repository.putVertices(
    partitionId: 'performance',
    operationId: 'performance-operation',
    inputs: List<VertexInput>.generate(
      1001,
      (index) => VertexInput(
        key: 'key-$index',
        value: VertexValue.int32(index),
        expiresIn: const Duration(hours: 1),
      ),
      growable: false,
    ),
  );
  enqueueWatch.stop();

  final replayWatch = Stopwatch()..start();
  final confirmed = await repository.drain('performance');
  replayWatch.stop();
  final status = await repository.getWriteStatus(
    'performance',
    operation.operationId,
  );
  final snapshotBytes = utf8.encode(await store.exportSnapshot()).length;

  final readStore = InMemoryOfflineStore();
  await readStore.transaction<void>((transaction) {
    transaction.putCache(
      'read',
      OfflineCacheRecord.value(
        partitionId: 'read',
        generation: 0,
        key: const OfflineEntityKey.vertex('cached'),
        entity: Vertex(
          key: 'cached',
          value: VertexValue.string('cached'),
          expiration: null,
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    );
  });
  final readRepository = OfflineLanternRepository(
    store: readStore,
    remote: _ImmediateRemote(),
    config: OfflineConfig(clock: () => now, jitter: (_) => Duration.zero),
  );
  final readMicros = <int>[];
  for (var index = 0; index < 500; index++) {
    final watch = Stopwatch()..start();
    await readRepository.readVertex(
      'read',
      'cached',
      policy: OfflineReadPolicy.cacheOnly,
    );
    watch.stop();
    readMicros.add(watch.elapsedMicroseconds);
  }
  readMicros.sort();

  final metrics = <String, Object?>{
    'schema': 1,
    'scenario': baseline['scenario'],
    'sampleCount': readMicros.length,
    'itemCount': operation.itemCount,
    'confirmedCount': confirmed,
    'enqueueMillis': enqueueWatch.elapsedMilliseconds,
    'replayMillis': replayWatch.elapsedMilliseconds,
    'cacheReadMicros': <String, int>{
      'p50': _percentile(readMicros, 0.50),
      'p95': _percentile(readMicros, 0.95),
      'p99': _percentile(readMicros, 0.99),
    },
    'rssDeltaBytes': ProcessInfo.currentRss > rssBefore
        ? ProcessInfo.currentRss - rssBefore
        : 0,
    'snapshotBytes': snapshotBytes,
  };
  _require(confirmed == 1001, 'confirmed_count');
  _require(status?.confirmedCount == 1001, 'durable_status');
  _within(
    enqueueWatch.elapsedMilliseconds,
    limits['enqueueMillis'],
    'enqueue_millis',
  );
  _within(
    replayWatch.elapsedMilliseconds,
    limits['replayMillis'],
    'replay_millis',
  );
  _within(
    _percentile(readMicros, 0.99),
    limits['cacheReadP99Micros'],
    'cache_read_p99_micros',
  );
  _within(metrics['rssDeltaBytes'], limits['rssDeltaBytes'], 'rss_delta_bytes');
  _within(snapshotBytes, limits['snapshotBytes'], 'snapshot_bytes');

  final encoded = const JsonEncoder.withIndent('  ').convert(metrics);
  if (outputPath == null) {
    stdout.writeln(encoded);
  } else {
    await File(outputPath).writeAsString('$encoded\n', flush: true);
  }
  await readRepository.dispose();
  await repository.dispose();
}

String? _outputPath(List<String> arguments) {
  if (arguments.isEmpty) return null;
  if (arguments.length == 2 && arguments.first == '--output') {
    return arguments.last;
  }
  throw const FormatException('usage: --output <path>');
}

int _percentile(List<int> sorted, double percentile) {
  final index = ((sorted.length - 1) * percentile).ceil();
  return sorted[index];
}

void _within(Object? actual, Object? limit, String label) {
  if (actual is! int || limit is! int || actual > limit) {
    throw StateError('offline_performance_baseline:$label');
  }
}

void _require(bool condition, String label) {
  if (!condition) throw StateError('offline_performance_baseline:$label');
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
