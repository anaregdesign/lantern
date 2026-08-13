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
