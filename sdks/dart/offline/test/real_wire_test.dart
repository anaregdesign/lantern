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
      final durable = await repository.store.transaction((transaction) async {
        return (
          outbox: await transaction.outbox('user'),
          operations: await transaction.operations('user'),
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
      // Freeze replay scheduling while the transport deadline uses real time.
      config: OfflineConfig(
        clock: () => DateTime.utc(2026, 8, 13),
        jitter: (ceiling) => ceiling,
      ),
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
    'receipt reconciliation matrix confirms original results without resend',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_RECEIPT_ENDPOINT'];
      final token = Platform.environment['LANTERN_DART_RECEIPT_TOKEN'];
      if (endpointValue == null ||
          endpointValue.isEmpty ||
          token == null ||
          token.isEmpty) {
        markTestSkipped('set receipt endpoint and token');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final serverClient = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        token: token,
      );
      addTearDown(serverClient.close);
      final capability =
          await serverClient.getReceiptCapability() as ReceiptCapabilityEnabled;
      expect(
        capability.supportedMutations,
        containsAll(<ReceiptMutationKind>{
          ReceiptMutationKind.vertexPut,
          ReceiptMutationKind.vertexDelete,
          ReceiptMutationKind.edgeDelete,
          ReceiptMutationKind.edgeAdd,
        }),
      );

      final prefix =
          'dart-offline-receipt:${DateTime.now().microsecondsSinceEpoch}:';
      final deleteVertexKey = '${prefix}delete-vertex';
      final deleteEdge = EdgeRef(
        '${prefix}delete-tail',
        '${prefix}delete-head',
      );
      final addEdge = EdgeRef('${prefix}add-tail', '${prefix}add-head');
      expect(
        await serverClient.putVertex(
          VertexInput(
            key: deleteVertexKey,
            value: VertexValue.string('delete'),
          ),
        ),
        PutOutcome.appliedAndLive,
      );
      expect(
        await serverClient.putEdge(
          EdgeInput(tail: deleteEdge.tail, head: deleteEdge.head, weight: 1),
        ),
        PutOutcome.appliedAndLive,
      );

      final proxy = await _ResponseDroppingProxy.bind(
        endpoint,
        drops: const <String, int>{
          'PutVertices': 1,
          'DeleteVertices': 1,
          'DeleteEdges': 1,
          'AddEdges': 1,
        },
      );
      addTearDown(proxy.close);
      final client = LanternClient.connect(
        proxy.endpoint,
        allowInsecure: true,
        token: token,
      );
      addTearDown(client.close);
      final online = LanternClientOfflineRemote(client);
      final store = InMemoryOfflineStore();
      final enqueueNow = DateTime.now().toUtc();
      final config = OfflineConfig(
        clock: () => enqueueNow,
        jitter: (ceiling) => ceiling,
        baseRetryDelay: const Duration(seconds: 1),
        maxConcurrency: 1,
        maxConcurrencyPerPartition: 1,
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: online,
        config: config,
      );
      final put = await repository.putVertexIfAbsent(
        partitionId: 'receipt-wire',
        input: VertexInput(
          key: '${prefix}put',
          value: VertexValue.string('value'),
        ),
      );
      final vertexDelete = await repository.deleteVertex(
        partitionId: 'receipt-wire',
        key: deleteVertexKey,
      );
      final edgeDelete = await repository.deleteEdge(
        partitionId: 'receipt-wire',
        edge: deleteEdge,
      );
      final add = await repository.addEdge(
        partitionId: 'receipt-wire',
        input: EdgeInput(
          tail: addEdge.tail,
          head: addEdge.head,
          weight: 4,
          contribId: Uint8List(24)..[23] = 1,
        ),
      );

      expect(await repository.drain('receipt-wire'), 0);
      for (final rpc in <String>[
        'PutVertices',
        'DeleteVertices',
        'DeleteEdges',
        'AddEdges',
      ]) {
        expect(proxy.forwarded(rpc), 1, reason: rpc);
        expect(proxy.dropped(rpc), 1, reason: rpc);
      }
      final pending = await store.transaction(
        (transaction) => transaction.outbox('receipt-wire'),
      );
      expect(pending, hasLength(4));
      expect(pending.map((record) => record.attemptCount), everyElement(1));
      expect(
        pending
            .where((record) => record.intent is OfflineReceiptAddEdgeIntent)
            .single
            .receipt!
            .mutation,
        ReceiptMutationKind.edgeAdd,
      );
      expect((await serverClient.getEdge(addEdge)).weight, 4);

      final snapshot = await store.exportSnapshot();
      await repository.dispose();
      final restarted = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(snapshot),
        remote: online,
        config: OfflineConfig(
          clock: () => enqueueNow.add(const Duration(seconds: 2)),
          jitter: (_) => Duration.zero,
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      addTearDown(restarted.dispose);
      expect(await restarted.drain('receipt-wire'), 4);
      for (final rpc in <String>[
        'PutVertices',
        'DeleteVertices',
        'DeleteEdges',
        'AddEdges',
      ]) {
        expect(proxy.forwarded(rpc), 1, reason: '$rpc was not resent');
      }

      final putStatus = await restarted.getWriteStatus(
        'receipt-wire',
        put.operationId,
      );
      expect(
        (putStatus!.items.single.receiptResult as OfflineVertexPutReceiptResult)
            .outcome,
        PutOutcome.appliedAndLive,
      );
      final vertexDeleteStatus = await restarted.getWriteStatus(
        'receipt-wire',
        vertexDelete.operationId,
      );
      expect(
        (vertexDeleteStatus!.items.single.receiptResult
                as OfflineVertexDeleteReceiptResult)
            .existed,
        isTrue,
      );
      final edgeDeleteStatus = await restarted.getWriteStatus(
        'receipt-wire',
        edgeDelete.operationId,
      );
      expect(
        (edgeDeleteStatus!.items.single.receiptResult
                as OfflineEdgeDeleteReceiptResult)
            .existed,
        isTrue,
      );
      final addStatus = await restarted.getWriteStatus(
        'receipt-wire',
        add.operationId,
      );
      expect(
        (addStatus!.items.single.receiptResult as OfflineEdgeAddReceiptResult)
            .effectiveWeight,
        4,
      );

      final expiredAdd = await restarted.addEdge(
        partitionId: 'receipt-wire',
        input: EdgeInput(
          tail: '${prefix}expired-tail',
          head: '${prefix}expired-head',
          weight: 9,
          expiresAt: DateTime.now().toUtc().subtract(
            const Duration(seconds: 1),
          ),
          contribId: Uint8List(24)..[23] = 2,
        ),
      );
      expect(await restarted.drain('receipt-wire'), 1);
      final expiredStatus = await restarted.getWriteStatus(
        'receipt-wire',
        expiredAdd.operationId,
      );
      expect(
        (expiredStatus!.items.single.receiptResult
                as OfflineEdgeAddReceiptResult)
            .effectiveWeight,
        0,
      );
    },
  );

  test(
    'receipt ambiguity stays status-first across three authenticated replicas',
    () async {
      final binary = Platform.environment['LANTERN_DART_RECEIPT_HA_BINARY'];
      if (binary == null || binary.isEmpty) {
        markTestSkipped('set LANTERN_DART_RECEIPT_HA_BINARY');
        return;
      }
      final token = Platform.environment['LANTERN_DART_RECEIPT_TOKEN'];
      if (token == null || token.isEmpty) {
        throw StateError('LANTERN_DART_RECEIPT_TOKEN is required for HA');
      }

      final cluster = await _ReceiptHaCluster.create(binary, token);
      addTearDown(cluster.close);
      await cluster.start();
      LanternClient connect(Uri endpoint) => LanternClient.connect(
        endpoint,
        allowInsecure: true,
        token: token,
        retryPolicy: const RetryPolicy(maxAttempts: 1),
        defaultTimeout: const Duration(seconds: 2),
      );

      final origin = connect(cluster.a);
      final follower = connect(cluster.b);
      final partitioned = connect(cluster.c);
      addTearDown(origin.close);
      addTearDown(follower.close);
      addTearDown(partitioned.close);
      final originCapability =
          await origin.getReceiptCapability() as ReceiptCapabilityEnabled;
      final followerCapability =
          await follower.getReceiptCapability() as ReceiptCapabilityEnabled;
      final partitionedCapability =
          await partitioned.getReceiptCapability() as ReceiptCapabilityEnabled;
      for (final capability in <ReceiptCapabilityEnabled>[
        followerCapability,
        partitionedCapability,
      ]) {
        expect(
          capability.policy.deploymentEpoch,
          originCapability.policy.deploymentEpoch,
        );
        expect(
          capability.policy.fingerprint,
          orderedEquals(originCapability.policy.fingerprint),
        );
      }
      expect(partitionedCapability.endpoint, isNot(originCapability.endpoint));
      expect(
        originCapability.supportedMutations,
        containsAll(<ReceiptMutationKind>{
          ReceiptMutationKind.vertexPut,
          ReceiptMutationKind.vertexDelete,
          ReceiptMutationKind.edgeDelete,
          ReceiptMutationKind.edgeAdd,
        }),
      );
      final anonymous = LanternClient.connect(
        cluster.c,
        allowInsecure: true,
        retryPolicy: const RetryPolicy(maxAttempts: 1),
      );
      addTearDown(anonymous.close);
      await expectLater(
        anonymous.getReceiptCapability(),
        throwsA(isA<LanternUnauthenticatedException>()),
      );

      final prefix =
          'dart-offline-ha:${DateTime.now().microsecondsSinceEpoch}:';
      final vertexKey = '${prefix}delete-vertex';
      final edgeKey = EdgeRef('${prefix}delete-tail', '${prefix}delete-head');
      final addKey = EdgeRef('${prefix}add-tail', '${prefix}add-head');
      expect(
        await origin.putVertex(
          VertexInput(key: vertexKey, value: VertexValue.string('seed')),
        ),
        PutOutcome.appliedAndLive,
      );
      expect(
        await origin.putEdge(
          EdgeInput(tail: edgeKey.tail, head: edgeKey.head, weight: 1),
        ),
        PutOutcome.appliedAndLive,
      );

      final proxy = await _ResponseDroppingProxy.bind(
        cluster.a,
        drops: const <String, int>{
          'PutVertices': 1,
          'DeleteVertices': 1,
          'DeleteEdges': 1,
          'AddEdges': 1,
        },
      );
      addTearDown(proxy.close);
      final offlineClient = connect(proxy.endpoint);
      addTearDown(offlineClient.close);
      const partitionId = 'receipt-ha-wire';
      final enqueuedAt = DateTime.now().toUtc();
      final replayConfig = OfflineConfig(
        clock: () => enqueuedAt.add(const Duration(seconds: 2)),
        jitter: (_) => Duration.zero,
        maxConcurrency: 1,
        maxConcurrencyPerPartition: 1,
      );
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: LanternClientOfflineRemote(offlineClient),
        config: OfflineConfig(
          clock: () => enqueuedAt,
          jitter: (ceiling) => ceiling,
          baseRetryDelay: const Duration(seconds: 1),
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      addTearDown(repository.dispose);
      final put = await repository.putVertexIfAbsent(
        partitionId: partitionId,
        input: VertexInput(
          key: '${prefix}put',
          value: VertexValue.string('value'),
        ),
      );
      final vertexDelete = await repository.deleteVertex(
        partitionId: partitionId,
        key: vertexKey,
      );
      final edgeDelete = await repository.deleteEdge(
        partitionId: partitionId,
        edge: edgeKey,
      );
      final add = await repository.addEdge(
        partitionId: partitionId,
        input: EdgeInput(
          tail: addKey.tail,
          head: addKey.head,
          weight: 4,
          contribId: Uint8List(24)..[23] = 1,
        ),
      );
      final cases =
          <
            ({
              String rpc,
              OfflineWriteHandle handle,
              ReceiptMutationKind mutation,
              Matcher wireResult,
              Matcher offlineResult,
            })
          >[
            (
              rpc: 'PutVertices',
              handle: put,
              mutation: ReceiptMutationKind.vertexPut,
              wireResult: isA<VertexPutReceipt>().having(
                (receipt) => receipt.outcome,
                'original outcome',
                PutOutcome.appliedAndLive,
              ),
              offlineResult: isA<OfflineVertexPutReceiptResult>().having(
                (result) => result.outcome,
                'original outcome',
                PutOutcome.appliedAndLive,
              ),
            ),
            (
              rpc: 'DeleteVertices',
              handle: vertexDelete,
              mutation: ReceiptMutationKind.vertexDelete,
              wireResult: isA<VertexDeleteReceipt>().having(
                (receipt) => receipt.existed,
                'original existed',
                isTrue,
              ),
              offlineResult: isA<OfflineVertexDeleteReceiptResult>().having(
                (result) => result.existed,
                'original existed',
                isTrue,
              ),
            ),
            (
              rpc: 'DeleteEdges',
              handle: edgeDelete,
              mutation: ReceiptMutationKind.edgeDelete,
              wireResult: isA<EdgeDeleteReceipt>().having(
                (receipt) => receipt.existed,
                'original existed',
                isTrue,
              ),
              offlineResult: isA<OfflineEdgeDeleteReceiptResult>().having(
                (result) => result.existed,
                'original existed',
                isTrue,
              ),
            ),
            (
              rpc: 'AddEdges',
              handle: add,
              mutation: ReceiptMutationKind.edgeAdd,
              wireResult: isA<EdgeAddReceipt>().having(
                (receipt) => receipt.effectiveWeight,
                'original effective weight',
                4,
              ),
              offlineResult: isA<OfflineEdgeAddReceiptResult>().having(
                (result) => result.effectiveWeight,
                'original effective weight',
                4,
              ),
            ),
          ];

      expect(await repository.drain(partitionId), 0);
      final pending = await store.transaction(
        (transaction) => transaction.outbox(partitionId),
      );
      expect(pending, hasLength(cases.length));
      final byRecord = {for (final record in pending) record.recordId: record};
      final evidence = <OfflineReceiptEvidence>[];
      for (final scenario in cases) {
        final record = byRecord[scenario.handle.recordId];
        expect(record, isNotNull, reason: scenario.rpc);
        expect(record!.attemptCount, 1, reason: scenario.rpc);
        expect(record.receipt, isNotNull, reason: scenario.rpc);
        expect(record.receipt!.mayHaveDispatched, isTrue);
        expect(record.receipt!.mutation, scenario.mutation);
        evidence.add(record.receipt!);
        expect(proxy.forwarded(scenario.rpc), 1, reason: scenario.rpc);
        expect(proxy.dropped(scenario.rpc), 1, reason: scenario.rpc);
      }
      expect(
        evidence.map((item) => item.operationId).toSet(),
        hasLength(cases.length),
      );
      expect(
        evidence.map((item) => item.groupId).toSet(),
        hasLength(cases.length),
      );

      expect(await origin.deleteEdge(addKey), isTrue);
      final ids = <ReceiptOperationId>[
        for (final item in evidence) item.operationId,
      ];
      void expectConfirmed(List<ReceiptStatus> statuses) {
        expect(statuses, hasLength(cases.length));
        for (var index = 0; index < cases.length; index++) {
          final receipt = statuses[index].receipt;
          expect(statuses[index].operationId, evidence[index].operationId);
          expect(statuses[index].state, ReceiptStatusState.confirmed);
          expect(receipt, cases[index].wireResult);
          expect(receipt!.operationId, evidence[index].operationId);
          expect(receipt.groupId, evidence[index].groupId);
          expect(receipt.mutation, cases[index].mutation);
          expect(receipt.itemIndex, 0);
          expect(receipt.itemCount, 1);
        }
      }

      expectConfirmed(await _awaitReceiptConfirmation(follower, ids, 'B'));
      await _awaitEdgeGone(follower, addKey);
      final snapshot = await store.exportSnapshot();
      await repository.dispose();
      await cluster.stopA();

      final unknownOnC = await partitioned.getReceiptStatuses(ids);
      expect(unknownOnC, hasLength(cases.length));
      for (var index = 0; index < cases.length; index++) {
        expect(unknownOnC[index].operationId, ids[index]);
        expect(unknownOnC[index].state, ReceiptStatusState.notYetObserved);
        expect(unknownOnC[index].receipt, isNull);
      }

      proxy.routeTo(cluster.c);
      final beforeUnknown = proxy.forwardedRpcs.length;
      final restartedStore = InMemoryOfflineStore.fromSnapshot(snapshot);
      final restored = await restartedStore.transaction(
        (transaction) => transaction.outbox(partitionId),
      );
      expect(restored, hasLength(cases.length));
      for (final record in restored) {
        final original = byRecord[record.recordId]!.receipt!;
        expect(record.receipt!.operationId, original.operationId);
        expect(record.receipt!.groupId, original.groupId);
        expect(record.receipt!.mayHaveDispatched, isTrue);
      }
      final unknownRepository = OfflineLanternRepository(
        store: restartedStore,
        remote: LanternClientOfflineRemote(offlineClient),
        config: replayConfig,
      );
      addTearDown(unknownRepository.dispose);
      expect(await unknownRepository.drain(partitionId), 0);
      final unknownRequests = proxy.forwardedRpcs.skip(beforeUnknown).toList();
      expect(unknownRequests, isNotEmpty);
      expect(unknownRequests.first, 'GetReceiptStatuses');
      expect(
        unknownRequests,
        everyElement(anyOf('GetReceiptStatuses', 'GetReceiptCapability')),
      );
      expect(unknownRequests, contains('GetReceiptCapability'));
      for (final scenario in cases) {
        final status = await unknownRepository.getWriteStatus(
          partitionId,
          scenario.handle.operationId,
        );
        expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
        expect(
          status.items.single.diagnosticCode,
          'receipt_continuity_changed',
        );
        expect(status.items.single.receiptResult, isNull);
        expect(status.items.single.attemptCount, 1);
        expect(proxy.forwarded(scenario.rpc), 1, reason: scenario.rpc);
      }
      expect(
        await unknownRepository.listDeadLetters(partitionId),
        hasLength(cases.length),
      );

      await cluster.relayBtoC();
      final converged = connect(cluster.c);
      addTearDown(converged.close);
      expectConfirmed(await _awaitReceiptConfirmation(converged, ids, 'C'));
      await expectLater(
        converged.getEdge(addKey),
        throwsA(isA<LanternNotFoundException>()),
      );
      for (final scenario in cases) {
        final status = await unknownRepository.getWriteStatus(
          partitionId,
          scenario.handle.operationId,
        );
        expect(status!.items.single.state, OfflineWriteState.outcomeUnknown);
      }

      proxy.routeTo(cluster.c);
      final beforeConfirmed = proxy.forwardedRpcs.length;
      final preUnknownCopy = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(snapshot),
        remote: LanternClientOfflineRemote(offlineClient),
        config: replayConfig,
      );
      addTearDown(preUnknownCopy.dispose);
      expect(await preUnknownCopy.drain(partitionId), cases.length);
      final confirmedRequests = proxy.forwardedRpcs
          .skip(beforeConfirmed)
          .toList();
      expect(confirmedRequests, isNotEmpty);
      expect(confirmedRequests, everyElement('GetReceiptStatuses'));
      for (final scenario in cases) {
        final status = await preUnknownCopy.getWriteStatus(
          partitionId,
          scenario.handle.operationId,
        );
        expect(status!.items.single.state, OfflineWriteState.confirmed);
        expect(status.items.single.receiptResult, scenario.offlineResult);
        expect(status.items.single.attemptCount, 1);
        expect(proxy.forwarded(scenario.rpc), 1, reason: scenario.rpc);
      }
    },
    timeout: const Timeout(Duration(minutes: 3)),
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
        (transaction) async => await transaction.outbox('wire'),
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

Future<List<ReceiptStatus>> _awaitReceiptConfirmation(
  LanternClient client,
  List<ReceiptOperationId> ids,
  String name,
) async {
  final elapsed = Stopwatch()..start();
  while (elapsed.elapsed < const Duration(seconds: 15)) {
    final statuses = await client.getReceiptStatuses(ids);
    if (statuses.every(
      (status) => status.state == ReceiptStatusState.confirmed,
    )) {
      return statuses;
    }
    await Future<void>.delayed(const Duration(milliseconds: 50));
  }
  throw StateError('receipt evidence did not converge on $name');
}

Future<void> _awaitEdgeGone(LanternClient client, EdgeRef edge) async {
  final elapsed = Stopwatch()..start();
  while (elapsed.elapsed < const Duration(seconds: 15)) {
    try {
      await client.getEdge(edge);
    } on LanternNotFoundException {
      return;
    }
    await Future<void>.delayed(const Duration(milliseconds: 50));
  }
  throw StateError('later Edge Delete did not reach B');
}

final class _ReceiptHaCluster {
  _ReceiptHaCluster._(this._binary, this._token, this._directory);

  static Future<_ReceiptHaCluster> create(String binary, String token) async =>
      _ReceiptHaCluster._(
        binary,
        token,
        await Directory.systemTemp.createTemp('lantern-offline-ha-'),
      );

  final String _binary;
  final String _token;
  final Directory _directory;
  final Set<int> _reservedPorts = <int>{};
  final Map<String, Process> _running = <String, Process>{};
  late final Uri a;
  late final Uri b;
  late final Uri c;
  late final int _cMetricsPort;

  Future<void> start() async {
    final aPort = await _freePort();
    a = _endpoint(aPort);
    await _startNode(
      'a',
      a,
      await _freePort(),
      '000000000000000000000000000000a1',
      'fresh',
    );
    final bPort = await _freePort();
    b = _endpoint(bPort);
    await _startNode(
      'b',
      b,
      await _freePort(),
      '000000000000000000000000000000b2',
      'fresh',
      peer: a,
    );
    final cPort = await _freePort();
    c = _endpoint(cPort);
    _cMetricsPort = await _freePort();
    await _startNode(
      'c',
      c,
      _cMetricsPort,
      '000000000000000000000000000000c3',
      'fresh',
    );
  }

  Future<void> stopA() => _stopNode('a');

  Future<void> relayBtoC() async {
    await _stopNode('c');
    await _startNode(
      'c',
      c,
      _cMetricsPort,
      '000000000000000000000000000000c3',
      'restart',
      peer: b,
    );
  }

  Future<void> close() async {
    final processes = _running.values.toList(growable: false);
    _running.clear();
    for (final process in processes) {
      process.kill(ProcessSignal.sigkill);
    }
    try {
      await Future.wait(
        processes.map(
          (process) => process.exitCode.timeout(const Duration(seconds: 5)),
        ),
      );
    } finally {
      await _directory.delete(recursive: true);
    }
  }

  Future<void> _startNode(
    String name,
    Uri endpoint,
    int metricsPort,
    String nodeId,
    String mode, {
    Uri? peer,
  }) async {
    final process = await Process.start(
      _binary,
      const <String>[],
      environment: <String, String>{
        'LANTERN_PORT': '${endpoint.port}',
        'LANTERN_METRICS_ADDR': '127.0.0.1:$metricsPort',
        'LANTERN_LOG_LEVEL': 'warn',
        'LANTERN_AUTH_TOKENS': _token,
        'LANTERN_NODE_ID': nodeId,
        'LANTERN_PEERS': peer == null ? '' : '${peer.host}:${peer.port}',
        'LANTERN_PUMP_BACKOFF_MIN_MS': '50',
        'LANTERN_PUMP_BACKOFF_MAX_MS': '200',
        'LANTERN_ANTI_ENTROPY_INTERVAL_MS': '250',
        'LANTERN_RECEIPT_WAL_MODE': mode,
        'LANTERN_RECEIPT_WAL_PATH': '${_directory.path}/$name.wal',
        'LANTERN_RECEIPT_EPOCH': '42424242424242424242424242424242',
        'LANTERN_RECEIPT_RETENTION': '1h',
        'LANTERN_RECEIPT_MAX_ENTRIES': '128',
        'LANTERN_RECEIPT_MAX_BYTES': '1048576',
        'LANTERN_BACKUP_ENABLED': 'false',
        'LANTERN_BACKUP_RESTORE_ON_START': 'false',
      },
      includeParentEnvironment: false,
    );
    _running[name] = process;
    process.stdout.listen((_) {});
    var stderrTail = '';
    process.stderr.listen((chunk) {
      stderrTail += String.fromCharCodes(chunk);
      if (stderrTail.length > 4096) {
        stderrTail = stderrTail.substring(stderrTail.length - 4096);
      }
    });
    int? exitCode;
    unawaited(
      process.exitCode.then((code) {
        exitCode = code;
      }),
    );

    final probe = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      defaultTimeout: const Duration(seconds: 1),
    );
    try {
      final elapsed = Stopwatch()..start();
      while (elapsed.elapsed < const Duration(seconds: 15)) {
        if (exitCode != null) break;
        try {
          await probe.ping();
          return;
        } on LanternUnavailableException {
          await Future<void>.delayed(const Duration(milliseconds: 50));
        } on LanternDeadlineExceededException {
          await Future<void>.delayed(const Duration(milliseconds: 50));
        } on LanternHealthStatusException {
          await Future<void>.delayed(const Duration(milliseconds: 50));
        }
      }
      final failure = stderrTail.toLowerCase();
      final category = failure.contains('address already in use')
          ? 'listener address in use'
          : failure.contains('permission denied')
          ? 'filesystem permission denied'
          : failure.contains('receipt') || failure.contains('wal')
          ? 'receipt WAL startup failure'
          : 'unclassified startup failure';
      throw StateError(
        'authenticated receipt node $name did not become ready '
        '(exit: ${exitCode ?? 'still running'}, category: $category)',
      );
    } finally {
      await probe.close();
    }
  }

  Future<void> _stopNode(String name) async {
    final process = _running.remove(name);
    if (process == null) throw StateError('receipt node $name is not running');
    process.kill(ProcessSignal.sigkill);
    await process.exitCode.timeout(const Duration(seconds: 5));
  }

  Future<int> _freePort() async {
    for (var attempt = 0; attempt < 16; attempt++) {
      final socket = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final port = socket.port;
      await socket.close();
      if (_reservedPorts.add(port)) return port;
    }
    throw StateError('could not reserve distinct receipt fixture ports');
  }

  static Uri _endpoint(int port) => Uri(
    scheme: 'http',
    host: InternetAddress.loopbackIPv4.address,
    port: port,
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
  Uri _upstreamEndpoint;
  HttpClient _upstream = HttpClient();
  final Map<String, int> _remainingDrops;
  final Map<String, int> _forwarded = <String, int>{};
  final Map<String, int> _dropped = <String, int>{};
  final List<String> _forwardedRpcs = <String>[];

  Uri get endpoint =>
      Uri(scheme: 'http', host: _server.address.host, port: _server.port);

  int forwarded(String rpc) => _forwarded[rpc] ?? 0;

  int dropped(String rpc) => _dropped[rpc] ?? 0;

  List<String> get forwardedRpcs => List<String>.unmodifiable(_forwardedRpcs);

  void routeTo(Uri endpoint) {
    _upstream.close(force: true);
    _upstream = HttpClient()..autoUncompress = false;
    _upstreamEndpoint = endpoint;
  }

  Future<void> close() async {
    await _server.close(force: true);
    _upstream.close(force: true);
  }

  Future<void> _forward(HttpRequest downstream) async {
    final rpc = downstream.uri.pathSegments.isEmpty
        ? ''
        : downstream.uri.pathSegments.last;
    _forwarded[rpc] = (_forwarded[rpc] ?? 0) + 1;
    _forwardedRpcs.add(rpc);
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
