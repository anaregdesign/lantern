import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite/sqflite.dart' as sqflite;

import 'support/direct_launch_binding.dart';
import 'support/head_edge_fixture.dart';
import 'support/head_edge_wire.dart';
import 'support/physical_result_marker.dart';

void main() {
  directLaunchIntegrationTest('physical Head-managed blind acceptance', () async {
    final result = PhysicalResultMarker(
      'lantern-head-edge-result.json',
      kind: 'physical_head_edge_on_device_result',
    );
    await result.recordPhase('setup');
    var bodyPassed = false;
    addTearDown(() async {
      if (!bodyPassed) {
        await result.recordOutcome('failed', result.phase, failureType: 'body');
      } else if (result.cleanupFailureType != null) {
        await result.recordOutcome(
          'failed',
          'cleanup',
          failureType: result.cleanupFailureType,
        );
      } else {
        await result.recordOutcome('passed', 'complete');
      }
    });
    const allowInsecure = bool.fromEnvironment('LANTERN_ALLOW_INSECURE');
    Uri fixtureUri(String value) {
      final uri = Uri.tryParse(value);
      if (uri == null ||
          uri.host.isEmpty ||
          uri.userInfo.isNotEmpty ||
          uri.hasFragment ||
          (uri.scheme != 'https' && !(allowInsecure && uri.scheme == 'http'))) {
        throw StateError('physical_head_https_required');
      }
      return uri;
    }

    final endpoint = fixtureUri(
      const String.fromEnvironment('LANTERN_ENDPOINT'),
    );
    final tokenHttp = HttpClient()
      ..connectionTimeout = const Duration(seconds: 10);
    result.addTrackedTearDown(() => tokenHttp.close(force: true));
    Future<String> token(Uri address) async {
      final request = await tokenHttp
          .getUrl(address)
          .timeout(const Duration(seconds: 10));
      final response = await request.close().timeout(
        const Duration(seconds: 10),
      );
      final bytes = <int>[];
      await for (final chunk in response.timeout(const Duration(seconds: 10))) {
        bytes.addAll(chunk);
        if (bytes.length > 32 * 1024) {
          throw StateError('physical_head_token_size');
        }
      }
      if (response.statusCode != HttpStatus.ok) {
        throw StateError('physical_head_token_status');
      }
      final decoded = jsonDecode(utf8.decode(bytes));
      if (decoded is! Map<String, Object?> ||
          decoded['access_token'] is! String ||
          (decoded['access_token']! as String).isEmpty) {
        throw StateError('physical_head_token_format');
      }
      return decoded['access_token']! as String;
    }

    LanternClient connect(String tokenAddress) {
      final address = fixtureUri(tokenAddress);
      return LanternClient.connect(
        endpoint,
        tokenProvider: () => token(address),
        allowInsecure: allowInsecure,
        retryPolicy: const RetryPolicy(maxAttempts: 1),
      );
    }

    final observer = connect(
      const String.fromEnvironment('LANTERN_TOKEN_ENDPOINT'),
    );
    final writer = connect(
      const String.fromEnvironment('LANTERN_HEAD_WRITER_TOKEN_ENDPOINT'),
    );
    result.addTrackedTearDown(observer.close);
    result.addTrackedTearDown(writer.close);
    final runId = DateTime.now().microsecondsSinceEpoch.toString();
    final fixture = await verifyHeadEdgeWire(
      observer: observer,
      writer: writer,
      runId: runId,
      allowInsecure: allowInsecure,
      recordPhase: result.recordPhase,
      registerCleanup: result.addTrackedTearDown,
    );
    final edges = fixture.edges;
    await result.recordPhase('offline_sqlite');
    final root = Directory(await sqflite.getDatabasesPath());
    await root.create(recursive: true);
    final directory = await root.createTemp('lantern-head-acceptance-');
    result.addTrackedTearDown(() => directory.delete(recursive: true));
    final path = '${directory.path}/offline.db';
    var store = await SqliteOfflineStore.open(path: path);
    final remote = _CountingRemote(LanternClientOfflineRemote(writer));
    var repository = OfflineLanternRepository(store: store, remote: remote);
    result.addTrackedTearDown(() async {
      await repository.dispose();
      await store.close();
    });
    const partition = 'head-writer';
    final write = await repository.putEdge(
      partitionId: partition,
      input: EdgeInput(tail: edges[2].tail, head: edges[2].head, weight: 5),
    );
    expect(await repository.drain(partition), 0);
    expect(remote.putEdgeCalls, 1);
    await repository.dispose();
    await store.close();
    store = await SqliteOfflineStore.open(path: path);
    repository = OfflineLanternRepository(store: store, remote: remote);
    final persisted = await repository.getWriteStatus(
      partition,
      write.operationId,
    );
    expect(persisted!.isTerminal, isTrue);
    expect(persisted.acceptedUndisclosedCount, 1);
    expect(persisted.confirmedCount, 0);
    expect(persisted.items.single.receiptResult, isNull);
    expect(await repository.listPending(partition), isEmpty);
    expect(await repository.drain(partition), 0);
    expect(remote.putEdgeCalls, 1);
    final cached = await repository.readEdge(
      partition,
      edges[2],
      policy: OfflineReadPolicy.cacheOnly,
    );
    expect(cached.state, OfflineReadState.unknown);
    expect(cached.value, isNull);
    expect(cached.hasPendingWrites, isFalse);
    expect((await observer.getEdge(edges[2])).weight, 5);
    final endpoints = await observer.getVertices(edgeEndpointKeys(edges));
    expect(endpoints.missing, isEmpty);
    expect(
      endpoints.vertices.map(
        (vertex) => (
          vertex.key,
          vertex.expiration,
          (vertex.value as StringValue).value,
        ),
      ),
      fixture.original.map(
        (vertex) => (
          vertex.key,
          vertex.expiration,
          (vertex.value as StringValue).value,
        ),
      ),
    );
    await result.recordPhase('cleanup');
    bodyPassed = true;
    // ignore: avoid_print
    print(
      'HEAD_EDGE_PASS role=true ack=true receipt=true sqlite=true no_resend=true',
    );
  });
}

final class _CountingRemote implements OfflineRemote {
  _CountingRemote(this.delegate);
  final OfflineRemote delegate;
  int putEdgeCalls = 0;

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) =>
      delegate.probe(cancellation: cancellation);
  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) => delegate.getVertex(key, cancellation: cancellation);
  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.getEdge(edge, cancellation: cancellation);
  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) => delegate.putVertex(vertex, cancellation: cancellation);
  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) {
    putEdgeCalls++;
    return delegate.putEdge(edge, cancellation: cancellation);
  }
}
