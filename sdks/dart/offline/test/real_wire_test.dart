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

  test('wipe cancels token acquisition before any wire send', () async {
    var requests = 0;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((request) async {
      requests += 1;
      request.response
        ..statusCode = HttpStatus.ok
        ..headers.contentType = ContentType.json
        ..write('{}');
      await request.response.close();
    });
    final providerStarted = Completer<void>();
    final token = Completer<String?>();
    final client = LanternClient.connect(
      Uri.parse('http://${server.address.host}:${server.port}'),
      allowInsecure: true,
      tokenProvider: () {
        if (!providerStarted.isCompleted) providerStarted.complete();
        return token.future;
      },
      defaultTimeout: null,
    );
    addTearDown(client.close);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: LanternClientOfflineRemote(client),
    );
    await repository.putVertex(
      partitionId: 'old-user',
      input: VertexInput(key: 'key', value: VertexValue.nil()),
    );
    final draining = repository.drain('old-user');
    final canceledDrain = expectLater(
      draining,
      throwsA(isA<OfflineCanceledException>()),
    );
    await providerStarted.future;

    await repository.wipePartition('old-user');
    await canceledDrain;
    token.complete('new-user-token');
    await Future<void>.delayed(Duration.zero);
    expect(requests, 0);
  });

  test(
    'auth pause cancels a same-batch sibling before its token can send',
    () async {
      var requests = 0;
      final secondProviderStarted = Completer<void>();
      final blockedToken = Completer<String?>();
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() async {
        if (!blockedToken.isCompleted) blockedToken.complete('rotated-token');
        await server.close(force: true);
      });
      server.listen((request) async {
        requests += 1;
        await secondProviderStarted.future;
        request.response
          ..statusCode = HttpStatus.unauthorized
          ..headers.contentType = ContentType.json
          ..write('{"code":"unauthenticated","message":"expired"}');
        await request.response.close();
      });
      var providerCalls = 0;
      final client = LanternClient.connect(
        Uri.parse('http://${server.address.host}:${server.port}'),
        allowInsecure: true,
        tokenProvider: () {
          providerCalls += 1;
          if (providerCalls == 1) return 'expired-token';
          if (!secondProviderStarted.isCompleted) {
            secondProviderStarted.complete();
          }
          return blockedToken.future;
        },
        defaultTimeout: null,
      );
      addTearDown(client.close);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: LanternClientOfflineRemote(client),
        config: OfflineConfig(maxConcurrency: 2, maxConcurrencyPerPartition: 2),
      );
      addTearDown(repository.dispose);
      await repository.putVertices(
        partitionId: 'user',
        inputs: <VertexInput>[
          VertexInput(key: 'first', value: VertexValue.nil()),
          VertexInput(key: 'second', value: VertexValue.nil()),
        ],
      );

      expect(
        await repository.drain('user').timeout(const Duration(seconds: 2)),
        0,
      );
      expect(providerCalls, 2);
      expect(requests, 1);
      expect(await repository.isReplayPausedForAuth('user'), isTrue);
      final durable = await repository.store.transaction((transaction) {
        return (
          outbox: transaction.outbox('user'),
          operations: transaction.operations('user'),
        );
      });
      expect(
        durable.outbox.map((record) => record.attemptCount),
        everyElement(0),
      );
      expect(
        durable.outbox.map((record) => record.state),
        everyElement(OfflineOutboxState.enqueued),
      );
      expect(
        durable.operations.single.items.map((item) => item.state),
        everyElement(OfflineWriteState.pausedForAuth),
      );

      blockedToken.complete('rotated-token');
      await Future<void>.delayed(Duration.zero);
      expect(requests, 1);
    },
  );

  test(
    'online adapter suppresses nested retries and keeps typed cause',
    () async {
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
                isA<LanternUnavailableException>(),
              ),
        ),
      );
      expect(attempts, 1);
    },
  );

  test('offline mapper unwraps a real retry-exhausted failure', () async {
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

    late LanternRetryExhaustedException exhausted;
    try {
      await client.getVertex('retry-exhausted');
      fail('the real transport must exhaust its bounded attempts');
    } on LanternRetryExhaustedException catch (error) {
      exhausted = error;
    }

    expect(attempts, 2);
    expect(
      mapLanternClientFailure(exhausted),
      isA<OfflineRemoteFailure>()
          .having(
            (failure) => failure.kind,
            'kind',
            OfflineRemoteErrorKind.unavailable,
          )
          .having((failure) => failure.cause, 'cause', same(exhausted)),
    );
  });

  test('online adapter maps typed Connect failures exactly', () async {
    final responses = <({int status, String code})>[
      (status: HttpStatus.badRequest, code: 'invalid_argument'),
      (status: HttpStatus.tooManyRequests, code: 'resource_exhausted'),
      (status: HttpStatus.forbidden, code: 'permission_denied'),
      (status: HttpStatus.serviceUnavailable, code: 'unavailable'),
    ];
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((request) async {
      final response = responses.removeAt(0);
      request.response
        ..statusCode = response.status
        ..headers.contentType = ContentType.json
        ..write('{"code":"${response.code}","message":"mapped"}');
      await request.response.close();
    });
    final client = LanternClient.connect(
      Uri.parse('http://${server.address.host}:${server.port}'),
      allowInsecure: true,
      defaultTimeout: null,
    );
    addTearDown(client.close);
    final remote = LanternClientOfflineRemote(client);

    for (final expected in <({OfflineRemoteErrorKind kind, Type cause})>[
      (
        kind: OfflineRemoteErrorKind.invalidArgument,
        cause: LanternInvalidArgumentException,
      ),
      (
        kind: OfflineRemoteErrorKind.resourceExhausted,
        cause: LanternResourceExhaustedException,
      ),
      (
        kind: OfflineRemoteErrorKind.permanent,
        cause: LanternPermissionDeniedException,
      ),
      (
        kind: OfflineRemoteErrorKind.unavailable,
        cause: LanternUnavailableException,
      ),
    ]) {
      await expectLater(
        remote.getVertex('mapped'),
        throwsA(
          isA<OfflineRemoteFailure>()
              .having((failure) => failure.kind, 'kind', expected.kind)
              .having(
                (failure) => failure.cause.runtimeType,
                'cause type',
                expected.cause,
              ),
        ),
      );
    }
    expect(responses, isEmpty);
  });

  test('online adapter preserves deadline classification', () async {
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
        request.response
          ..statusCode = HttpStatus.serviceUnavailable
          ..headers.contentType = ContentType.json
          ..write('{"code":"unavailable","message":"late"}');
        await request.response.close();
      } on Object {
        // The deadline closes the request before teardown releases the handler.
      }
    });
    final client = LanternClient.connect(
      Uri.parse('http://${server.address.host}:${server.port}'),
      allowInsecure: true,
      defaultTimeout: const Duration(milliseconds: 100),
    );
    addTearDown(client.close);
    final operation = LanternClientOfflineRemote(client).getVertex('deadline');
    await requestStarted.future;
    await expectLater(
      operation,
      throwsA(
        isA<OfflineRemoteFailure>()
            .having(
              (failure) => failure.kind,
              'kind',
              OfflineRemoteErrorKind.deadlineExceeded,
            )
            .having(
              (failure) => failure.cause,
              'cause',
              isA<LanternDeadlineExceededException>(),
            ),
      ),
    );

    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: LanternClientOfflineRemote(client),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'deadline',
      input: VertexInput(key: 'deadline-put', value: VertexValue.nil()),
    );
    expect(await repository.drain('deadline'), 0);
    final pending = (await repository.listPending('deadline')).single;
    expect(pending.attemptCount, 1);
    expect(pending.diagnosticCode, 'deadline_exceeded');
  });

  test(
    'online adapter reacquires a rotated token after auth failure',
    () async {
      var requests = 0;
      final authorizations = <String?>[];
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => server.close(force: true));
      server.listen((request) async {
        requests += 1;
        authorizations.add(
          request.headers.value(HttpHeaders.authorizationHeader),
        );
        request.response.headers.contentType = ContentType.json;
        if (requests == 1) {
          request.response
            ..statusCode = HttpStatus.unauthorized
            ..write('{"code":"unauthenticated","message":"rotate"}');
        } else {
          request.response
            ..statusCode = HttpStatus.notFound
            ..write('{"code":"not_found","message":"missing"}');
        }
        await request.response.close();
      });
      var token = 'expired-token';
      final client = LanternClient.connect(
        Uri.parse('http://${server.address.host}:${server.port}'),
        allowInsecure: true,
        tokenProvider: () => token,
        defaultTimeout: null,
      );
      addTearDown(client.close);
      final remote = LanternClientOfflineRemote(client);

      await expectLater(
        remote.getVertex('auth'),
        throwsA(
          isA<OfflineRemoteFailure>().having(
            (failure) => failure.kind,
            'kind',
            OfflineRemoteErrorKind.unauthenticated,
          ),
        ),
      );
      token = 'rotated-token';
      expect(
        await remote.getVertex('auth'),
        isA<OfflineRemoteMissing<Vertex>>(),
      );
      expect(authorizations, <String>[
        'Bearer expired-token',
        'Bearer rotated-token',
      ]);
    },
  );

  test('each offline attempt permits at most one wire send', () async {
    var sends = 0;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((request) async {
      sends += 1;
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
        maxAttempts: 3,
        baseDelay: Duration(microseconds: 1),
        maxDelay: Duration(microseconds: 1),
      ),
    );
    addTearDown(client.close);
    var now = DateTime.utc(2026, 8, 13);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: LanternClientOfflineRemote(client),
      config: OfflineConfig(
        clock: () => now,
        maxAttempts: 2,
        jitter: (ceiling) => ceiling,
        baseRetryDelay: const Duration(seconds: 1),
      ),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.nil()),
    );

    expect(await repository.drain('p'), 0);
    expect(sends, 1);
    expect((await repository.listPending('p')).single.attemptCount, 1);
    now = now.add(const Duration(seconds: 1));
    expect(await repository.drain('p'), 0);
    expect(sends, 2);
    expect((await repository.listDeadLetters('p')).single.attemptCount, 2);
  });

  test(
    'pre-wire credential failure consumes an adapter attempt only',
    () async {
      var sends = 0;
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      addTearDown(() => server.close(force: true));
      server.listen((request) async {
        sends += 1;
        request.response
          ..statusCode = HttpStatus.ok
          ..headers.contentType = ContentType.json
          ..write('{}');
        await request.response.close();
      });
      final client = LanternClient.connect(
        Uri.parse('http://${server.address.host}:${server.port}'),
        allowInsecure: true,
        tokenProvider: () => throw StateError('credential provider failed'),
        defaultTimeout: null,
      );
      addTearDown(client.close);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: LanternClientOfflineRemote(client),
        config: OfflineConfig(maxAttempts: 1),
      );
      addTearDown(repository.dispose);
      await repository.putVertex(
        partitionId: 'pre-wire',
        input: VertexInput(key: 'key', value: VertexValue.nil()),
      );

      expect(await repository.drain('pre-wire'), 0);
      expect(sends, 0);
      final deadLetter = (await repository.listDeadLetters('pre-wire')).single;
      expect(deadLetter.attemptCount, 1);
      expect(deadLetter.diagnosticCode, 'unknown');
    },
  );

  test(
    'response-dropping proxy loses committed PutVertex and PutEdge responses',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
      if (endpointValue == null || endpointValue.isEmpty) {
        markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final serverClient = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        idempotentAdds: true,
      );
      addTearDown(serverClient.close);
      await serverClient.ping();

      final proxy = await _ResponseDroppingProxy.bind(
        endpoint,
        drops: const <String, int>{'PutVertices': 1, 'PutEdges': 1},
      );
      addTearDown(proxy.close);
      final client = LanternClient.connect(proxy.endpoint, allowInsecure: true);
      addTearDown(client.close);
      await client.ping();

      final online = LanternClientOfflineRemote(client);
      final store = InMemoryOfflineStore();
      final enqueueNow = DateTime.now().toUtc();
      final repository = OfflineLanternRepository(
        store: store,
        remote: online,
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
      expect(proxy.forwarded('PutVertices'), 1);
      expect(proxy.forwarded('PutEdges'), 1);
      expect(proxy.dropped('PutVertices'), 1);
      expect(proxy.dropped('PutEdges'), 1);
      final beforeRestart = await store.transaction(
        (transaction) => transaction.outbox('wire'),
      );
      expect(beforeRestart, hasLength(2));
      expect(
        beforeRestart.map((record) => record.attemptCount),
        everyElement(1),
      );
      expect(
        beforeRestart.map((record) => record.diagnosticCode),
        everyElement('unavailable'),
      );
      final firstVertex = beforeRestart
          .map((record) => record.intent)
          .whereType<OfflinePutVertexIntent>()
          .single;
      final firstEdge = beforeRestart
          .map((record) => record.intent)
          .whereType<OfflinePutEdgeIntent>()
          .single;
      expect(
        (await serverClient.getVertex(vertexKey)).value,
        isA<StringValue>().having((value) => value.value, 'value', 'committed'),
        reason: 'the upstream server committed before the proxy disconnected',
      );
      expect(
        (await serverClient.getEdge(EdgeRef(tail, head))).weight,
        Float32Value(0.75).value,
      );

      final snapshot = await store.exportSnapshot();
      await repository.dispose();
      final restarted = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(snapshot),
        remote: online,
        config: OfflineConfig(
          clock: () => enqueueNow.add(const Duration(seconds: 2)),
          jitter: (_) => Duration.zero,
        ),
      );
      addTearDown(restarted.dispose);
      expect(await restarted.drain('wire'), 2);
      expect(proxy.forwarded('PutVertices'), 2);
      expect(proxy.forwarded('PutEdges'), 2);

      final vertex = await serverClient.getVertex(vertexKey);
      expect((vertex.value as StringValue).value, 'committed');
      expect(
        vertex.expiration,
        firstVertex.vertex.expiration,
        reason: 'replay must not rebase the once-resolved Vertex TTL',
      );
      final edge = await serverClient.getEdge(EdgeRef(tail, head));
      expect(edge.weight, Float32Value(0.75).value);
      expect(
        edge.expiration,
        firstEdge.edge.expiration,
        reason: 'replay must not rebase the once-resolved Edge TTL',
      );
      expect(await restarted.listPending('wire'), isEmpty);
    },
  );

  test('server EXPIRED survives response loss for Vertex and Edge', () async {
    final endpointValue =
        Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
    if (endpointValue == null || endpointValue.isEmpty) {
      markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
      return;
    }
    final endpoint = Uri.parse(endpointValue);
    final serverClient = LanternClient.connect(
      endpoint,
      allowInsecure: endpoint.scheme == 'http',
    );
    addTearDown(serverClient.close);
    await serverClient.ping();

    final prefix =
        'dart-offline-expired-wire:'
        '${DateTime.now().microsecondsSinceEpoch}:';
    final vertexKey = '${prefix}vertex';
    final edgeRef = EdgeRef('${prefix}tail', '${prefix}head');
    expect(
      await serverClient.putVertex(
        VertexInput(
          key: vertexKey,
          value: VertexValue.string('old'),
          expiresIn: const Duration(minutes: 5),
        ),
      ),
      PutOutcome.appliedAndLive,
    );
    expect(
      await serverClient.putEdge(
        EdgeInput(
          tail: edgeRef.tail,
          head: edgeRef.head,
          weight: 9,
          expiresIn: const Duration(minutes: 5),
        ),
      ),
      PutOutcome.appliedAndLive,
    );

    // The offline device is behind the server: this deadline appears live
    // locally while it is already expired at the authoritative server.
    var deviceNow = DateTime.now().toUtc().subtract(const Duration(hours: 2));
    final serverExpiredAt = deviceNow.add(const Duration(hours: 1));
    final proxy = await _ResponseDroppingProxy.bind(
      endpoint,
      drops: const <String, int>{'PutVertices': 1, 'PutEdges': 1},
    );
    addTearDown(proxy.close);
    final client = LanternClient.connect(proxy.endpoint, allowInsecure: true);
    addTearDown(client.close);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: LanternClientOfflineRemote(client),
      config: OfflineConfig(
        clock: () => deviceNow,
        jitter: (ceiling) => ceiling,
        baseRetryDelay: const Duration(microseconds: 1),
      ),
    );
    addTearDown(repository.dispose);

    expect(
      (await repository.readVertex(
        'expired-wire',
        vertexKey,
        policy: OfflineReadPolicy.serverOnly,
      )).state,
      OfflineReadState.fresh,
    );
    expect(
      (await repository.readEdge(
        'expired-wire',
        edgeRef,
        policy: OfflineReadPolicy.serverOnly,
      )).state,
      OfflineReadState.fresh,
    );
    final vertexWrite = await repository.putVertex(
      partitionId: 'expired-wire',
      input: VertexInput(
        key: vertexKey,
        value: VertexValue.string('expired'),
        expiresAt: serverExpiredAt,
      ),
    );
    final edgeWrite = await repository.putEdge(
      partitionId: 'expired-wire',
      input: EdgeInput(
        tail: edgeRef.tail,
        head: edgeRef.head,
        weight: 1,
        expiresAt: serverExpiredAt,
      ),
    );

    // Both delete-like commits happen, but their first responses are lost.
    // Replaying obtains EXPIRED again and safely terminalizes each intent.
    expect(await repository.drain('expired-wire'), 0);
    final responseLost = await repository.listPending('expired-wire');
    expect(responseLost, hasLength(2));
    expect(responseLost.map((record) => record.attemptCount), everyElement(1));
    expect(
      responseLost.map((record) => record.diagnosticCode),
      everyElement('unavailable'),
    );
    expect(proxy.dropped('PutVertices'), 1);
    expect(proxy.dropped('PutEdges'), 1);
    deviceNow = deviceNow.add(const Duration(seconds: 1));
    expect(await repository.drain('expired-wire'), 0);
    expect(await repository.listPending('expired-wire'), isEmpty);
    for (final operationId in <String>[
      vertexWrite.operationId,
      edgeWrite.operationId,
    ]) {
      final status = await repository.getWriteStatus(
        'expired-wire',
        operationId,
      );
      expect(status!.items.single.state, OfflineWriteState.expired);
      expect(status.items.single.attemptCount, 2);
      expect(status.items.single.diagnosticCode, 'put_expired');
    }
    expect(
      (await repository.readVertex(
        'expired-wire',
        vertexKey,
        policy: OfflineReadPolicy.cacheOnly,
      )).state,
      OfflineReadState.unknown,
    );
    expect(
      (await repository.readEdge(
        'expired-wire',
        edgeRef,
        policy: OfflineReadPolicy.cacheOnly,
      )).state,
      OfflineReadState.unknown,
    );
    await expectLater(
      serverClient.getVertex(vertexKey),
      throwsA(isA<LanternNotFoundException>()),
    );
    await expectLater(
      serverClient.getEdge(edgeRef),
      throwsA(isA<LanternNotFoundException>()),
    );
  });

  test(
    'wipe quiesces a delayed response after the real server commit',
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
      final gated = _CommittedResponseGateRemote(
        LanternClientOfflineRemote(client),
      );
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(store: store, remote: gated);
      final key = 'dart-offline-wipe:${DateTime.now().microsecondsSinceEpoch}';
      await repository.putVertex(
        partitionId: 'old-user',
        input: VertexInput(key: key, value: VertexValue.string('committed')),
      );
      final draining = repository.drain('old-user');
      final canceledDrain = expectLater(
        draining,
        throwsA(isA<OfflineCanceledException>()),
      );
      await gated.serverCommitted.future;

      await repository.wipePartition('old-user');
      await canceledDrain;
      expect(await repository.listPending('old-user'), isEmpty);
      expect(
        (await client.getVertex(key)).value,
        isA<StringValue>().having((value) => value.value, 'value', 'committed'),
        reason: 'local wipe cannot roll back a server-accepted mutation',
      );
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
      final serverClient = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        idempotentAdds: true,
      );
      addTearDown(serverClient.close);
      await serverClient.ping();

      final prefix =
          'dart-offline-add-quarantine:'
          '${DateTime.now().microsecondsSinceEpoch}:';
      final edgeRef = EdgeRef('${prefix}tail', '${prefix}head');
      addTearDown(() => serverClient.deleteEdge(edgeRef));
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

      final proxy = await _ResponseDroppingProxy.bind(
        endpoint,
        drops: const <String, int>{'AddEdges': 1},
      );
      addTearDown(proxy.close);
      final legacyClient = LanternClient.connect(
        proxy.endpoint,
        allowInsecure: true,
        idempotentAdds: true,
        retryPolicy: const RetryPolicy(maxAttempts: 1),
      );
      addTearDown(legacyClient.close);

      await expectLater(
        legacyClient.addEdge(
          EdgeInput(
            tail: edgeRef.tail,
            head: edgeRef.head,
            weight: 0.5,
            expiresAt: expiration,
            contribId: contributionId,
          ),
        ),
        throwsA(
          isA<LanternRetryExhaustedException>()
              .having((error) => error.attempts, 'attempts', 1)
              .having(
                (error) => error.cause,
                'cause',
                isA<LanternUnavailableException>(),
              ),
        ),
      );
      expect(proxy.dropped('AddEdges'), 1);
      expect(
        (await serverClient.getEdge(edgeRef)).weight,
        Float32Value(0.5).value,
        reason: 'Add committed upstream before its response was dropped',
      );
      expect(await serverClient.deleteEdge(edgeRef), isTrue);
      await expectLater(
        serverClient.getEdge(edgeRef),
        throwsA(isA<LanternNotFoundException>()),
      );

      final recording = _RecordingRemote(
        LanternClientOfflineRemote(serverClient),
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
        serverClient.getEdge(edgeRef),
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

final class _RecordingRemote implements OfflineRemote {
  _RecordingRemote(this.delegate);

  final OfflineRemote delegate;
  int putVertexCalls = 0;
  int putEdgeCalls = 0;

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
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    putEdgeCalls++;
    return delegate.putEdge(edge, cancellation: cancellation);
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    putVertexCalls++;
    return delegate.putVertex(vertex, cancellation: cancellation);
  }
}

/// A loopback HTTP proxy that consumes an upstream response and then closes the
/// downstream socket before writing any status line or response bytes.
///
/// This keeps response-loss evidence on the transport boundary: the Lantern
/// server has completed the real Connect request while the SDK adapter sees
/// only a disconnected downstream connection.
final class _ResponseDroppingProxy {
  _ResponseDroppingProxy._(
    this._server,
    this._upstreamEndpoint,
    Map<String, int> drops,
  ) : _remainingDrops = Map<String, int>.of(drops) {
    _upstream.autoUncompress = false;
    _server.listen((request) => unawaited(_forward(request)));
  }

  static Future<_ResponseDroppingProxy> bind(
    Uri upstreamEndpoint, {
    required Map<String, int> drops,
  }) async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    return _ResponseDroppingProxy._(server, upstreamEndpoint, drops);
  }

  final HttpServer _server;
  final Uri _upstreamEndpoint;
  final HttpClient _upstream = HttpClient();
  final Map<String, int> _remainingDrops;
  final Map<String, int> _forwarded = <String, int>{};
  final Map<String, int> _dropped = <String, int>{};

  Uri get endpoint =>
      Uri(scheme: 'http', host: _server.address.host, port: _server.port);

  int forwarded(String rpc) => _forwarded[rpc] ?? 0;

  int dropped(String rpc) => _dropped[rpc] ?? 0;

  Future<void> close() async {
    await _server.close(force: true);
    _upstream.close(force: true);
  }

  Future<void> _forward(HttpRequest downstream) async {
    final rpc = downstream.uri.pathSegments.isEmpty
        ? ''
        : downstream.uri.pathSegments.last;
    _forwarded[rpc] = (_forwarded[rpc] ?? 0) + 1;
    try {
      final target = _upstreamEndpoint.resolveUri(downstream.uri);
      final upstreamRequest = await _upstream.openUrl(
        downstream.method,
        target,
      );
      _copyHeaders(downstream.headers, upstreamRequest.headers);
      await upstreamRequest.addStream(downstream);
      final upstreamResponse = await upstreamRequest.close();
      final responseBytes = await upstreamResponse.fold<BytesBuilder>(
        BytesBuilder(copy: false),
        (builder, bytes) => builder..add(bytes),
      );

      final remaining = _remainingDrops[rpc] ?? 0;
      if (remaining > 0) {
        _remainingDrops[rpc] = remaining - 1;
        _dropped[rpc] = (_dropped[rpc] ?? 0) + 1;
        final socket = await downstream.response.detachSocket(
          writeHeaders: false,
        );
        socket.destroy();
        return;
      }

      downstream.response.statusCode = upstreamResponse.statusCode;
      _copyHeaders(upstreamResponse.headers, downstream.response.headers);
      downstream.response.add(responseBytes.takeBytes());
      await downstream.response.close();
    } on Object {
      try {
        downstream.response.statusCode = HttpStatus.badGateway;
        await downstream.response.close();
      } on Object {
        // The deliberately disconnected response has no writable downstream.
      }
    }
  }

  static void _copyHeaders(HttpHeaders source, HttpHeaders target) {
    const ignored = <String>{
      'connection',
      'content-length',
      'host',
      'keep-alive',
      'proxy-authenticate',
      'proxy-authorization',
      'te',
      'trailer',
      'transfer-encoding',
      'upgrade',
    };
    source.forEach((name, values) {
      if (!ignored.contains(name.toLowerCase())) {
        target.set(name, values);
      }
    });
  }
}

final class _CommittedResponseGateRemote implements OfflineRemote {
  _CommittedResponseGateRemote(this.delegate);

  final OfflineRemote delegate;
  final Completer<void> serverCommitted = Completer<void>();

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
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.putEdge(edge, cancellation: cancellation);

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    final outcome = await delegate.putVertex(
      vertex,
      cancellation: cancellation,
    );
    if (!serverCommitted.isCompleted) serverCommitted.complete();
    final canceled = Completer<void>();
    final removeCancellation = cancellation?.listen((_) {
      if (!canceled.isCompleted) {
        canceled.completeError(
          const OfflineRemoteFailure(
            OfflineRemoteErrorKind.canceled,
            OfflineCanceledException(),
          ),
        );
      }
    });
    try {
      await canceled.future;
    } finally {
      removeCancellation?.call();
    }
    return outcome;
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
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.putEdge(edge, cancellation: cancellation);

  @override
  Future<PutOutcome> putVertex(
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
