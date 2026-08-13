import 'dart:async';
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
  final remote = _ImmediateRemote();
  final repository = OfflineLanternRepository(
    store: store,
    remote: remote,
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
  final durableCounts = await store.transaction((transaction) {
    final outbox = transaction.outbox('performance');
    return (
      outbox: outbox.length,
      operations: transaction.operations('performance').length,
      claims: outbox
          .where((record) => record.state == OfflineOutboxState.sending)
          .length,
      leases: outbox
          .where(
            (record) => record.leaseOwner != null || record.leaseUntil != null,
          )
          .length,
    );
  });

  final recoveryWatch = Stopwatch()..start();
  final reopened = InMemoryOfflineStore.fromSnapshot(
    await store.exportSnapshot(),
    limits: store.limits,
  );
  final reopenedSnapshot = await reopened.exportSnapshot();
  final decodedStatusObjects = await reopened.transaction((transaction) {
    final operations = transaction.operations('performance');
    return operations.length +
        operations.fold<int>(
          0,
          (count, operation) => count + operation.items.length,
        );
  });
  recoveryWatch.stop();

  const changeControllerCycles = 512;
  for (var index = 0; index < changeControllerCycles; index++) {
    final subscription = store
        .changes('resource-controller-$index')
        .listen((_) {});
    await subscription.cancel();
  }

  const terminalStatusWatchCycles = 256;
  for (var index = 0; index < terminalStatusWatchCycles; index++) {
    final events = await repository
        .watchWrite('performance', operation.operationId)
        .toList();
    _require(events.length == 1 && events.single.isTerminal, 'status_watch');
  }

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

  const entityWatchCycles = 128;
  final entityWatchMicros = <int>[];
  for (var index = 0; index < entityWatchCycles; index++) {
    final watch = Stopwatch()..start();
    final first = Completer<OfflineSnapshot<Vertex>>();
    final subscription = readRepository.watchVertex('read', 'cached').listen((
      snapshot,
    ) {
      if (!first.isCompleted) first.complete(snapshot);
    });
    final snapshot = await first.future;
    watch.stop();
    entityWatchMicros.add(watch.elapsedMicroseconds);
    _require(snapshot.value?.key == 'cached', 'entity_watch');
    await subscription.cancel();
  }
  entityWatchMicros.sort();

  final disposeWatch = Stopwatch()..start();
  await readRepository.dispose();
  await repository.dispose();
  disposeWatch.stop();

  final metrics = <String, Object?>{
    'schema': 1,
    'scenario': baseline['scenario'],
    'sampleCount': readMicros.length,
    'itemCount': operation.itemCount,
    'confirmedCount': confirmed,
    'enqueueMillis': enqueueWatch.elapsedMilliseconds,
    'replayMillis': replayWatch.elapsedMilliseconds,
    'recoveryMillis': recoveryWatch.elapsedMilliseconds,
    'disposeMillis': disposeWatch.elapsedMilliseconds,
    'cacheReadMicros': <String, int>{
      'p50': _percentile(readMicros, 0.50),
      'p95': _percentile(readMicros, 0.95),
      'p99': _percentile(readMicros, 0.99),
    },
    'entityWatchMicros': <String, int>{
      'p50': _percentile(entityWatchMicros, 0.50),
      'p95': _percentile(entityWatchMicros, 0.95),
      'p99': _percentile(entityWatchMicros, 0.99),
    },
    'rssDeltaBytes': ProcessInfo.currentRss > rssBefore
        ? ProcessInfo.currentRss - rssBefore
        : 0,
    'snapshotBytes': snapshotBytes,
    'wireSends': remote.putVertexCalls + remote.putEdgeCalls,
    'maximumConcurrentSends': remote.maximumActiveSends,
    'outstandingSends': remote.activeSends,
    'remainingOutboxRecords': durableCounts.outbox,
    'retainedOperationRecords': durableCounts.operations,
    'remainingClaims': durableCounts.claims,
    'remainingLeases': durableCounts.leases,
    'decodedStatusObjects': decodedStatusObjects,
    'resourceCycles': <String, int>{
      'changeControllers': changeControllerCycles,
      'entityWatches': entityWatchCycles,
      'terminalStatusWatches': terminalStatusWatchCycles,
    },
  };
  _require(confirmed == 1001, 'confirmed_count');
  _require(status?.confirmedCount == 1001, 'durable_status');
  _require(
    remote.putVertexCalls + remote.putEdgeCalls == 1001,
    'one_send_per_item',
  );
  _require(
    remote.maximumActiveSends > 0 && remote.maximumActiveSends <= 32,
    'bounded_concurrent_sends',
  );
  _require(remote.activeSends == 0, 'send_cleanup');
  _require(
    durableCounts.outbox == 0 &&
        durableCounts.operations == 1 &&
        durableCounts.claims == 0 &&
        durableCounts.leases == 0,
    'bounded_terminal_state',
  );
  _require(decodedStatusObjects == 1002, 'bounded_decoded_status_objects');
  _require(
    reopenedSnapshot == await store.exportSnapshot(),
    'canonical_reopen',
  );
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
    recoveryWatch.elapsedMilliseconds,
    limits['recoveryMillis'],
    'recovery_millis',
  );
  _within(
    disposeWatch.elapsedMilliseconds,
    limits['disposeMillis'],
    'dispose_millis',
  );
  _within(
    _percentile(readMicros, 0.99),
    limits['cacheReadP99Micros'],
    'cache_read_p99_micros',
  );
  _within(
    _percentile(entityWatchMicros, 0.99),
    limits['entityWatchP99Micros'],
    'entity_watch_p99_micros',
  );
  _within(metrics['rssDeltaBytes'], limits['rssDeltaBytes'], 'rss_delta_bytes');
  _within(snapshotBytes, limits['snapshotBytes'], 'snapshot_bytes');

  final encoded = const JsonEncoder.withIndent('  ').convert(metrics);
  if (outputPath == null) {
    stdout.writeln(encoded);
  } else {
    await File(outputPath).writeAsString('$encoded\n', flush: true);
  }
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
  int putVertexCalls = 0;
  int putEdgeCalls = 0;
  int activeSends = 0;
  int maximumActiveSends = 0;

  Future<PutOutcome> _send() async {
    activeSends += 1;
    if (activeSends > maximumActiveSends) maximumActiveSends = activeSends;
    try {
      await Future<void>.delayed(Duration.zero);
      return PutOutcome.appliedAndLive;
    } finally {
      activeSends -= 1;
    }
  }

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
  }) async {
    putEdgeCalls += 1;
    return _send();
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    putVertexCalls += 1;
    return _send();
  }
}
