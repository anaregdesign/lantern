@TestOn('vm')
library;

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

void main() {
  test('online adapter preserves typed transport cancellation', () async {
    final requestStarted = Completer<void>();
    final releaseResponse = Completer<void>();
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() async {
      if (!releaseResponse.isCompleted) releaseResponse.complete();
      await server.close(force: true);
    });
    server.listen((request) async {
      if (!requestStarted.isCompleted) requestStarted.complete();
      await releaseResponse.future;
      try {
        request.response.statusCode = HttpStatus.serviceUnavailable;
        await request.response.close();
      } on Object {
        // Caller cancellation may close the request before test teardown.
      }
    });
    final client = LanternClient.connect(
      Uri.parse('http://${server.address.host}:${server.port}'),
      allowInsecure: true,
      defaultTimeout: null,
    );
    addTearDown(client.close);
    final remote = LanternClientOfflineRemote(client);
    final cancellation = LanternCancellationToken();

    final reading = remote.getVertex('cancel', cancellation: cancellation);
    await requestStarted.future;
    cancellation.cancel('screen disposed');

    await expectLater(reading, throwsA(isA<OfflineCanceledException>()));
  });

  test('online adapter classifies a retry-exhausted typed cause', () async {
    var attempts = 0;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((request) async {
      attempts += 1;
      request.response
        ..statusCode = HttpStatus.serviceUnavailable
        ..headers.contentType = ContentType.json
        ..write('{"code":"unavailable","message":"down"}');
      await request.response.close();
    });
    final client = LanternClient.connect(
      Uri.parse('http://${server.address.host}:${server.port}'),
      allowInsecure: true,
      retryPolicy: const RetryPolicy(
        maxAttempts: 2,
        baseDelay: Duration(microseconds: 1),
        maxDelay: Duration(microseconds: 1),
      ),
    );
    addTearDown(client.close);
    final remote = LanternClientOfflineRemote(client);

    await expectLater(
      remote.getVertex('retry'),
      throwsA(
        isA<OfflineRemoteFailure>()
            .having(
              (failure) => failure.kind,
              'kind',
              OfflineRemoteErrorKind.unavailable,
            )
            .having(
              (failure) => failure.cause,
              'cause',
              isA<LanternRetryExhaustedException>().having(
                (wrapper) => wrapper.cause,
                'typed cause',
                isA<LanternUnavailableException>(),
              ),
            ),
      ),
    );
    expect(attempts, 2);
  });

  test(
    'real server commit plus response loss replays PutVertex and PutEdge',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
      if (endpointValue == null || endpointValue.isEmpty) {
        markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        idempotentAdds: true,
      );
      addTearDown(client.close);
      await client.ping();

      final online = LanternClientOfflineRemote(client);
      final dropping = _ResponseDroppingRemote(
        online,
        dropNextPutVertex: true,
        dropNextPutEdge: true,
      );
      final store = InMemoryOfflineStore();
      final enqueueNow = DateTime.now().toUtc();
      final repository = OfflineLanternRepository(
        store: store,
        remote: dropping,
        config: OfflineConfig(
          clock: () => enqueueNow,
          jitter: (ceiling) => ceiling,
          baseRetryDelay: const Duration(seconds: 1),
        ),
      );
      final prefix =
          'dart-offline-wire:${DateTime.now().microsecondsSinceEpoch}:';
      final vertexKey = '${prefix}vertex';
      final tail = '${prefix}tail';
      final head = '${prefix}head';
      await repository.putVertex(
        partitionId: 'wire',
        input: VertexInput(
          key: vertexKey,
          value: VertexValue.string('committed'),
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await repository.putEdge(
        partitionId: 'wire',
        input: EdgeInput(
          tail: tail,
          head: head,
          weight: 0.75,
          expiresIn: const Duration(minutes: 5),
        ),
      );
      expect(await repository.drain('wire'), 0);
      expect(dropping.putVertexCalls, 1);
      expect(dropping.putEdgeCalls, 1);
      final beforeRestart = await store.transaction(
        (transaction) => transaction.outbox('wire'),
      );
      expect(beforeRestart, hasLength(2));
      final firstEdge = beforeRestart
          .map((record) => record.intent)
          .whereType<OfflinePutEdgeIntent>()
          .single;

      final snapshot = await store.exportSnapshot();
      await repository.dispose();
      final recording = _ResponseDroppingRemote(online);
      final restarted = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(snapshot),
        remote: recording,
        config: OfflineConfig(
          clock: () => enqueueNow.add(const Duration(seconds: 2)),
          jitter: (_) => Duration.zero,
        ),
      );
      addTearDown(restarted.dispose);
      expect(await restarted.drain('wire'), 2);
      expect(
        recording.edgeExpirations.single,
        firstEdge.edge.expiration,
        reason: 'replay must not rebase the once-resolved TTL',
      );

      final vertex = await client.getVertex(vertexKey);
      expect((vertex.value as StringValue).value, 'committed');
      final edge = await client.getEdge(EdgeRef(tail, head));
      expect(edge.weight, Float32Value(0.75).value);
      expect(await restarted.listPending('wire'), isEmpty);
    },
  );

  test(
    'legacy Add response loss cannot resurrect an Edge deleted before restart',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
      if (endpointValue == null || endpointValue.isEmpty) {
        markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        idempotentAdds: true,
      );
      addTearDown(client.close);
      await client.ping();

      final prefix =
          'dart-offline-add-quarantine:'
          '${DateTime.now().microsecondsSinceEpoch}:';
      final edgeRef = EdgeRef('${prefix}tail', '${prefix}head');
      addTearDown(() => client.deleteEdge(edgeRef));
      final contributionId = Uint8List.fromList(
        List<int>.generate(24, (index) => index + 1),
      );
      final enqueuedAt = DateTime.now().toUtc();
      final expiration = enqueuedAt.add(const Duration(minutes: 5));
      final legacyRecord = OfflineOutboxRecord(
        recordId: '${prefix}record',
        operationId: '${prefix}operation',
        itemIndex: 0,
        partitionId: 'legacy-wire',
        intent: OfflineAddEdgeIntent(
          Edge(
            tail: edgeRef.tail,
            head: edgeRef.head,
            weight: Float32Value(0.5).value,
            expiration: expiration,
          ),
          contributionId,
        ),
        enqueuedAt: enqueuedAt,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 1,
        generation: 0,
        nextAttemptAt: enqueuedAt.add(const Duration(seconds: 1)),
        diagnosticCode: 'unavailable',
      );
      final legacyStore = restoreLegacySnapshot(
        schema: 2,
        outbox: <OfflineOutboxRecord>[legacyRecord],
        operations: <OfflineOperationRecord>[
          OfflineOperationRecord(
            partitionId: legacyRecord.partitionId,
            generation: legacyRecord.generation,
            operationId: legacyRecord.operationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: legacyRecord.recordId,
                operationId: legacyRecord.operationId,
                itemIndex: legacyRecord.itemIndex,
                state: OfflineWriteState.retryScheduled,
                attemptCount: legacyRecord.attemptCount,
                diagnosticCode: legacyRecord.diagnosticCode,
              ),
            ],
            updatedAt: enqueuedAt,
          ),
        ],
      );

      // The direct-online commit succeeds, but the retained retry record above
      // models the old adapter losing that response before local confirmation.
      expect(
        await client.addEdge(
          EdgeInput(
            tail: edgeRef.tail,
            head: edgeRef.head,
            weight: 0.5,
            expiresAt: expiration,
            contribId: contributionId,
          ),
        ),
        Float32Value(0.5).value,
      );
      expect(await client.deleteEdge(edgeRef), isTrue);
      await expectLater(
        client.getEdge(edgeRef),
        throwsA(isA<LanternNotFoundException>()),
      );

      final recording = _ResponseDroppingRemote(
        LanternClientOfflineRemote(client),
      );
      final restarted = OfflineLanternRepository(
        store: legacyStore,
        remote: recording,
        config: OfflineConfig(
          clock: () => enqueuedAt.add(const Duration(seconds: 2)),
          jitter: (_) => Duration.zero,
        ),
      );
      addTearDown(restarted.dispose);

      expect(await restarted.drain('legacy-wire'), 0);
      expect(recording.putVertexCalls, 0);
      expect(recording.putEdgeCalls, 0);
      final deadLetters = await restarted.listDeadLetters('legacy-wire');
      expect(deadLetters, hasLength(1));
      expect(deadLetters.single.category, OfflineOperationCategory.addEdge);
      expect(deadLetters.single.attemptCount, 1);
      expect(deadLetters.single.diagnosticCode, 'unsupported_add');
      final status = await restarted.getWriteStatus(
        'legacy-wire',
        '${prefix}operation',
      );
      expect(status!.isTerminal, isTrue);
      expect(status.deadLetterCount, 1);
      expect(status.items.single.attemptCount, 1);
      expect(status.items.single.diagnosticCode, 'unsupported_add');
      await expectLater(
        client.getEdge(edgeRef),
        throwsA(isA<LanternNotFoundException>()),
      );
    },
  );

  test(
    '1001-item replay persists partial item-at-a-time progress across restart',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
      if (endpointValue == null || endpointValue.isEmpty) {
        markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
      );
      addTearDown(client.close);
      await client.ping();

      final online = LanternClientOfflineRemote(client);
      final partial = _FailingAfterRemote(online, succeedBefore: 100);
      final enqueueNow = DateTime.now().toUtc();
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 1100,
          maxOutboxRecordsPerPartition: 1100,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: partial,
        config: OfflineConfig(
          clock: () => enqueueNow,
          maxConcurrency: 32,
          jitter: (ceiling) => ceiling,
          baseRetryDelay: const Duration(seconds: 1),
        ),
      );
      final prefix =
          'dart-offline-large:${DateTime.now().microsecondsSinceEpoch}:';
      final operation = await repository.putVertices(
        partitionId: 'large-wire',
        operationId: '${prefix}operation',
        inputs: List<VertexInput>.generate(
          1001,
          (index) => VertexInput(
            key: '$prefix$index',
            value: VertexValue.int32(index),
            expiresIn: const Duration(minutes: 5),
          ),
          growable: false,
        ),
      );

      expect(await repository.drain('large-wire'), 100);
      final partialStatus = await repository.getWriteStatus(
        'large-wire',
        operation.operationId,
      );
      expect(partialStatus!.confirmedCount, 100);
      expect(
        partialStatus.items.where(
          (item) => item.state == OfflineWriteState.retryScheduled,
        ),
        hasLength(901),
      );

      final snapshot = await store.exportSnapshot();
      await repository.dispose();
      final restarted = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(
          snapshot,
          limits: const OfflineStoreLimits(
            maxOutboxRecords: 1100,
            maxOutboxRecordsPerPartition: 1100,
          ),
        ),
        remote: online,
        config: OfflineConfig(
          clock: () => enqueueNow.add(const Duration(seconds: 2)),
          maxConcurrency: 32,
          jitter: (_) => Duration.zero,
        ),
      );
      addTearDown(restarted.dispose);

      expect(await restarted.drain('large-wire'), 901);
      final completed = await restarted.getWriteStatus(
        'large-wire',
        operation.operationId,
      );
      expect(completed!.isTerminal, isTrue);
      expect(completed.confirmedCount, 1001);
      expect(
        (await client.getVertex('$prefix${1000}')).value,
        isA<Int32Value>().having((value) => value.value, 'value', 1000),
      );
    },
    timeout: const Timeout(Duration(minutes: 3)),
  );
}

final class _ResponseDroppingRemote implements OfflineRemote {
  _ResponseDroppingRemote(
    this.delegate, {
    this.dropNextPutVertex = false,
    this.dropNextPutEdge = false,
  });

  final OfflineRemote delegate;
  bool dropNextPutVertex;
  bool dropNextPutEdge;
  int putVertexCalls = 0;
  int putEdgeCalls = 0;
  final List<DateTime?> edgeExpirations = <DateTime?>[];

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.getEdge(edge, cancellation: cancellation);

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) => delegate.getVertex(key, cancellation: cancellation);

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) =>
      delegate.probe(cancellation: cancellation);

  @override
  Future<void> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    putEdgeCalls++;
    edgeExpirations.add(edge.expiration);
    await delegate.putEdge(edge, cancellation: cancellation);
    if (dropNextPutEdge) {
      dropNextPutEdge = false;
      throw OfflineRemoteFailure(
        OfflineRemoteErrorKind.unavailable,
        StateError('response dropped after real commit'),
      );
    }
  }

  @override
  Future<void> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    putVertexCalls++;
    await delegate.putVertex(vertex, cancellation: cancellation);
    if (dropNextPutVertex) {
      dropNextPutVertex = false;
      throw OfflineRemoteFailure(
        OfflineRemoteErrorKind.unavailable,
        StateError('response dropped after real commit'),
      );
    }
  }
}

final class _FailingAfterRemote implements OfflineRemote {
  _FailingAfterRemote(this.delegate, {required this.succeedBefore});

  final OfflineRemote delegate;
  final int succeedBefore;
  var putVertexCalls = 0;

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.getEdge(edge, cancellation: cancellation);

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) => delegate.getVertex(key, cancellation: cancellation);

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) =>
      delegate.probe(cancellation: cancellation);

  @override
  Future<void> putEdge(Edge edge, {LanternCancellationToken? cancellation}) =>
      delegate.putEdge(edge, cancellation: cancellation);

  @override
  Future<void> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) {
    putVertexCalls += 1;
    if (putVertexCalls > succeedBefore) {
      throw OfflineRemoteFailure(
        OfflineRemoteErrorKind.unavailable,
        StateError('simulated outage'),
      );
    }
    return delegate.putVertex(vertex, cancellation: cancellation);
  }
}
