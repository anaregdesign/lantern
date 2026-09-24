import 'dart:async';
import 'dart:convert';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

final class MutableClock {
  MutableClock(DateTime initial) : now = initial.toUtc();

  DateTime now;

  DateTime call() => now;

  void advance(Duration duration) {
    now = now.add(duration);
  }
}

class FakeOfflineRemote implements OfflineRemote {
  final Map<String, Vertex> vertices = <String, Vertex>{};
  final Map<EdgeRef, Edge> edges = <EdgeRef, Edge>{};
  final List<OfflineRemoteFailure> vertexPutFailures = <OfflineRemoteFailure>[];
  final List<OfflineRemoteFailure> edgePutFailures = <OfflineRemoteFailure>[];
  final List<OfflineRemoteFailure> vertexGetFailures = <OfflineRemoteFailure>[];
  final List<OfflineRemoteFailure> edgeGetFailures = <OfflineRemoteFailure>[];
  final List<PutOutcome> vertexPutOutcomes = <PutOutcome>[];
  final List<PutOutcome> edgePutOutcomes = <PutOutcome>[];
  int vertexPutCalls = 0;
  int edgePutCalls = 0;
  final List<OfflineRemoteFailure> probeFailures = <OfflineRemoteFailure>[];
  int probeCalls = 0;

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) async {
    probeCalls++;
    if (probeFailures.isNotEmpty) throw probeFailures.removeAt(0);
  }

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) async {
    if (edgeGetFailures.isNotEmpty) throw edgeGetFailures.removeAt(0);
    final found = edges[edge];
    return found == null
        ? const OfflineRemoteMissing<Edge>()
        : OfflineRemotePresent<Edge>(found);
  }

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    if (vertexGetFailures.isNotEmpty) throw vertexGetFailures.removeAt(0);
    final found = vertices[key];
    return found == null
        ? const OfflineRemoteMissing<Vertex>()
        : OfflineRemotePresent<Vertex>(found);
  }

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    edgePutCalls++;
    if (edgePutFailures.isNotEmpty) throw edgePutFailures.removeAt(0);
    final outcome = edgePutOutcomes.isEmpty
        ? PutOutcome.appliedAndLive
        : edgePutOutcomes.removeAt(0);
    if (outcome == PutOutcome.appliedAndLive) {
      edges[EdgeRef(edge.tail, edge.head)] = edge;
    } else if (outcome == PutOutcome.expired) {
      edges.remove(EdgeRef(edge.tail, edge.head));
    }
    return outcome;
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    if (vertexPutFailures.isNotEmpty) throw vertexPutFailures.removeAt(0);
    final outcome = vertexPutOutcomes.isEmpty
        ? PutOutcome.appliedAndLive
        : vertexPutOutcomes.removeAt(0);
    if (outcome == PutOutcome.appliedAndLive) {
      vertices[vertex.key] = vertex;
    } else if (outcome == PutOutcome.expired) {
      vertices.remove(vertex.key);
    }
    return outcome;
  }
}

OfflineConfig testConfig(MutableClock clock) => OfflineConfig(
  clock: clock.call,
  idGenerator: _ids(),
  jitter: (_) => Duration.zero,
  baseRetryDelay: const Duration(microseconds: 1),
  maxRetryDelay: const Duration(seconds: 1),
);

OfflineIdGenerator _ids() {
  var index = 0;
  return () => 'test-${++index}';
}

OfflineRemoteFailure failure(OfflineRemoteErrorKind kind) =>
    OfflineRemoteFailure(kind, StateError(kind.name));

InMemoryOfflineStore restoreLegacySnapshot({
  required int schema,
  required List<OfflineOutboxRecord> outbox,
  List<OfflineOperationRecord> operations = const <OfflineOperationRecord>[],
}) {
  return InMemoryOfflineStore.fromSnapshot(
    encodeLegacySnapshot(
      schema: schema,
      outbox: outbox,
      operations: operations,
    ),
  );
}

String encodeLegacySnapshot({
  required int schema,
  required List<OfflineOutboxRecord> outbox,
  List<OfflineOperationRecord> operations = const <OfflineOperationRecord>[],
}) {
  if (schema < 1 || schema >= InMemoryOfflineStore.snapshotSchemaVersion) {
    throw ArgumentError.value(schema, 'schema');
  }
  if (outbox.isEmpty) throw ArgumentError.value(outbox, 'outbox');
  final partitionId = outbox.first.partitionId;
  final generation = outbox.first.generation;
  final nextOrdinal = outbox
      .map((record) => record.ordinal)
      .reduce((left, right) => left > right ? left : right);
  final partition = <String, Object?>{
    'partitionId': partitionId,
    'generation': generation,
    'version': 0,
    'nextOrdinal': nextOrdinal,
    'cache': const <String>[],
    'outbox': outbox
        .map(OfflineCodec.encodeOutboxRecord)
        .toList(growable: false),
    if (schema > 1)
      'operations': operations
          .map(OfflineCodec.encodeOperationRecord)
          .toList(growable: false),
  };
  return jsonEncode(<String, Object?>{
    'schema': schema,
    'partitions': <Object?>[partition],
  });
}

/// Exercises database-style asynchronous operations against the reference store.
final class DelayedOfflineStore implements OfflineStore {
  DelayedOfflineStore(this.inner);

  final InMemoryOfflineStore inner;

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) => inner.transaction(
    (transaction) => action(_DelayedTransaction(transaction)),
  );
}

final class _DelayedTransaction implements OfflineStoreTransaction {
  const _DelayedTransaction(this.inner);

  final OfflineStoreTransaction inner;

  @override
  Future<int> generation(String partitionId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.generation(partitionId);
  }

  @override
  Future<bool> replayPausedForAuth(String partitionId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.replayPausedForAuth(partitionId);
  }

  @override
  Future<void> setReplayPausedForAuth(String partitionId, bool paused) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.setReplayPausedForAuth(partitionId, paused);
  }

  @override
  Future<OfflineCacheRecord?> getCache(
    String partitionId,
    OfflineEntityKey key,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.getCache(partitionId, key);
  }

  @override
  Future<void> putCache(String partitionId, OfflineCacheRecord record) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.putCache(partitionId, record);
  }

  @override
  Future<void> deleteCache(String partitionId, OfflineEntityKey key) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.deleteCache(partitionId, key);
  }

  @override
  Future<void> touchCache(
    String partitionId,
    OfflineEntityKey key,
    DateTime accessedAt,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.touchCache(partitionId, key, accessedAt);
  }

  @override
  Future<List<OfflineOutboxRecord>> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.outboxForKey(partitionId, key);
  }

  @override
  Future<OfflineOutboxRecord?> getOutbox(
    String partitionId,
    String recordId,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.getOutbox(partitionId, recordId);
  }

  @override
  Future<List<OfflineOutboxRecord>> outbox(String partitionId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.outbox(partitionId);
  }

  @override
  Future<OfflineOutboxScanPage> scanOutbox(
    String partitionId, {
    OfflineOutboxCursor? after,
    String? operationId,
    OfflineEntityKey? key,
    required int limit,
  }) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.scanOutbox(
      partitionId,
      after: after,
      operationId: operationId,
      key: key,
      limit: limit,
    );
  }

  @override
  Future<bool> hasOutboxForOperation(
    String partitionId,
    String operationId,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.hasOutboxForOperation(partitionId, operationId);
  }

  @override
  Future<List<OfflineOutboxRecord>> dueOutbox(
    String partitionId, {
    String? operationId,
    OfflineEntityKey? key,
    required DateTime now,
    required Duration maxAge,
    required Duration deadLetterRetention,
    required int limit,
  }) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.dueOutbox(
      partitionId,
      operationId: operationId,
      key: key,
      now: now,
      maxAge: maxAge,
      deadLetterRetention: deadLetterRetention,
      limit: limit,
    );
  }

  @override
  Future<OfflineOutboxRecord> enqueue(OfflineOutboxRecord record) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.enqueue(record);
  }

  @override
  Future<List<OfflineOutboxRecord>> enqueueAll(
    List<OfflineOutboxRecord> records,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.enqueueAll(records);
  }

  @override
  Future<void> updateOutbox(OfflineOutboxRecord record) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.updateOutbox(record);
  }

  @override
  Future<void> deleteOutbox(String partitionId, String recordId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.deleteOutbox(partitionId, recordId);
  }

  @override
  Future<OfflineOperationRecord?> getOperation(
    String partitionId,
    String operationId,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.getOperation(partitionId, operationId);
  }

  @override
  Future<List<OfflineOperationRecord>> operations(String partitionId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.operations(partitionId);
  }

  @override
  Future<OfflineOperationScanPage> scanOperations(
    String partitionId, {
    String? afterOperationId,
    required int limit,
  }) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.scanOperations(
      partitionId,
      afterOperationId: afterOperationId,
      limit: limit,
    );
  }

  @override
  Future<List<OfflineOperationRecord>> dueOperations(
    String partitionId, {
    required DateTime now,
    required Duration retention,
    required int limit,
  }) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.dueOperations(
      partitionId,
      now: now,
      retention: retention,
      limit: limit,
    );
  }

  @override
  Future<void> putOperation(OfflineOperationRecord record) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.putOperation(record);
  }

  @override
  Future<void> deleteOperation(String partitionId, String operationId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.deleteOperation(partitionId, operationId);
  }

  @override
  Future<List<OfflineOutboxRecord>> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration maxAge,
    required Duration leaseDuration,
    required int limit,
  }) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.claim(
      partitionId,
      owner: owner,
      now: now,
      maxAge: maxAge,
      leaseDuration: leaseDuration,
      limit: limit,
    );
  }

  @override
  Future<bool> renewLease(
    String partitionId,
    String recordId, {
    required String owner,
    required int generation,
    required DateTime now,
    required Duration leaseDuration,
  }) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.renewLease(
      partitionId,
      recordId,
      owner: owner,
      generation: generation,
      now: now,
      leaseDuration: leaseDuration,
    );
  }

  @override
  Future<OfflineChangeCursor> changeCursor(String partitionId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.changeCursor(partitionId);
  }

  @override
  Future<void> applyChangeChunk(
    String partitionId,
    OfflineChangeChunk chunk,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.applyChangeChunk(partitionId, chunk);
  }

  @override
  Future<void> resetChangeCursor(
    String partitionId,
    OfflineChangeCursor checkpoint,
  ) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.resetChangeCursor(partitionId, checkpoint);
  }

  @override
  Future<void> wipePartition(String partitionId) async {
    await Future<void>.delayed(Duration.zero);
    return await inner.wipePartition(partitionId);
  }
}
