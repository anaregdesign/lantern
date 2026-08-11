import 'dart:async';

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
  Future<void> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    edgePutCalls++;
    if (edgePutFailures.isNotEmpty) throw edgePutFailures.removeAt(0);
    edges[EdgeRef(edge.tail, edge.head)] = edge;
  }

  @override
  Future<void> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    if (vertexPutFailures.isNotEmpty) throw vertexPutFailures.removeAt(0);
    vertices[vertex.key] = vertex;
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
