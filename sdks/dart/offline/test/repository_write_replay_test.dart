import 'dart:async';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

void main() {
  final initial = DateTime.utc(2026, 7, 22, 12);

  test(
    'local Put overlays are immediately visible and replay exactly',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      final put = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'v', value: VertexValue.string('local')),
      );
      final snapshot = await repository.readVertex(
        'p',
        'v',
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(snapshot.value!.value, isA<StringValue>());
      expect(snapshot.hasPendingWrites, isTrue);
      expect(await put.statuses.first, isA<OfflineWriteStatus>());

      await repository.putEdge(
        partitionId: 'p',
        input: EdgeInput(tail: 'a', head: 'b', weight: 0.1),
      );
      final edge = await repository.readEdge(
        'p',
        const EdgeRef('a', 'b'),
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(edge.hasPendingWrites, isTrue);
      expect(edge.value!.weight, Float32Value(0.1).value);
      final records = await store.transaction(
        (transaction) => transaction.outbox('p'),
      );
      expect(records, hasLength(2));
      expect(await repository.drain('p'), 2);
      expect(remote.edgePutCalls, 1);
      expect((await put.statuses.first).state, OfflineWriteState.confirmed);
    },
  );

  test(
    'legacy Add is never overlaid or sent and becomes inspectable terminal work',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      late final OfflineOutboxRecord legacyAdd;
      await store.transaction((transaction) {
        final assigned = transaction.enqueueAll(<OfflineOutboxRecord>[
          OfflineOutboxRecord(
            recordId: 'legacy-add',
            operationId: 'mixed-operation',
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflineAddEdgeIntent(
              Edge(
                tail: 'a',
                head: 'b',
                weight: Float32Value(0.5).value,
                expiration: initial.add(const Duration(hours: 1)),
              ),
              Uint8List.fromList(List<int>.generate(24, (index) => index + 1)),
            ),
            enqueuedAt: initial,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            generation: 0,
          ),
          OfflineOutboxRecord(
            recordId: 'safe-put',
            operationId: 'mixed-operation',
            itemIndex: 1,
            partitionId: 'p',
            intent: OfflinePutVertexIntent(
              Vertex(
                key: 'safe',
                value: VertexValue.string('value'),
                expiration: null,
              ),
            ),
            enqueuedAt: initial,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            generation: 0,
          ),
        ]);
        legacyAdd = assigned.first;
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'mixed-operation',
            items: assigned
                .map(
                  (record) => OfflineWriteStatus(
                    recordId: record.recordId,
                    operationId: record.operationId,
                    itemIndex: record.itemIndex,
                    state: OfflineWriteState.locallyCommitted,
                    attemptCount: 0,
                  ),
                )
                .toList(growable: false),
            updatedAt: initial,
          ),
        );
      });

      final beforeDrain = await repository.readEdge(
        'p',
        const EdgeRef('a', 'b'),
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(beforeDrain.value, isNull);
      expect(beforeDrain.hasPendingWrites, isFalse);

      expect(await repository.drain('p'), 1);
      expect(remote.vertexPutCalls, 1);
      expect(remote.edgePutCalls, 0);
      final status = await repository.getWriteStatus('p', 'mixed-operation');
      expect(status!.isTerminal, isTrue);
      expect(status.confirmedCount, 1);
      expect(status.deadLetterCount, 1);
      expect(status.items.first.attemptCount, 0);
      expect(status.items.first.diagnosticCode, 'unsupported_add');
      final deadLetter = (await repository.listDeadLetters('p')).single;
      expect(deadLetter.category, OfflineOperationCategory.addEdge);
      expect(deadLetter.diagnosticCode, 'unsupported_add');
      final inspected = await repository.inspectDeadLetter(
        'p',
        legacyAdd.recordId,
        authorize: (_) async => true,
      );
      expect(inspected, isA<OfflineAddEdgeIntent>());
      await expectLater(
        repository.retryDeadLetter('p', legacyAdd.recordId),
        throwsA(isA<OfflineUnsupportedOperationException>()),
      );
      expect(
        (await repository.listDeadLetters('p')).single.recordId,
        'legacy-add',
      );
    },
  );

  test(
    'replay retries, pauses authentication, and dead-letters invalid intents',
    () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote();
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (ceiling) => ceiling,
          baseRetryDelay: const Duration(seconds: 1),
        ),
      );
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'retry', value: VertexValue.string('retry')),
      );
      remote.vertexPutFailures.add(failure(OfflineRemoteErrorKind.unavailable));
      expect(await repository.drain('p'), 0);
      expect(remote.vertexPutCalls, 1);
      clock.advance(const Duration(seconds: 1));
      expect(await repository.drain('p'), 1);
      expect(remote.vertices['retry']!.value, isA<StringValue>());

      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'auth', value: VertexValue.string('auth')),
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.unauthenticated),
      );
      expect(await repository.drain('p'), 0);
      final auth = await store.transaction(
        (transaction) => transaction
            .outbox('p')
            .singleWhere((record) => record.intent.key.vertexKey == 'auth'),
      );
      expect(auth.attemptCount, 0);
      expect(await repository.resume('p'), 1);

      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'bad', value: VertexValue.string('bad')),
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.invalidArgument),
      );
      expect(await repository.drain('p'), 0);
      final dead = await repository.listDeadLetters('p');
      expect(dead, hasLength(1));
      expect(dead.single.category, OfflineOperationCategory.putVertex);
      await expectLater(
        repository.inspectDeadLetter(
          'p',
          dead.single.recordId,
          authorize: (_) async => false,
        ),
        throwsA(isA<OfflineAuthorizationException>()),
      );
      final intent = await repository.inspectDeadLetter(
        'p',
        dead.single.recordId,
        authorize: (_) => true,
      );
      expect(intent, isA<OfflinePutVertexIntent>());
    },
  );

  test(
    'operation retention preserves retryable dead-letter metadata',
    () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote();
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          operationRetention: const Duration(hours: 1),
          deadLetterRetention: const Duration(days: 1),
        ),
      );
      final write = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'retryable',
          value: VertexValue.string('value'),
        ),
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.invalidArgument),
      );
      expect(await repository.drain('p'), 0);
      final deadLetter = (await repository.listDeadLetters('p')).single;

      clock.advance(const Duration(hours: 2));
      expect(await repository.drain('p'), 0);
      expect(
        await repository.getWriteStatus('p', write.operationId),
        isNotNull,
      );

      await repository.retryDeadLetter('p', deadLetter.recordId);
      expect(
        (await repository.getWriteStatus(
          'p',
          write.operationId,
        ))!.items.single.state,
        OfflineWriteState.locallyCommitted,
      );
      expect(await repository.drain('p'), 1);
    },
  );

  test('expiration removes a pending overlay before replay', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'short',
        value: VertexValue.string('short'),
        expiresIn: const Duration(seconds: 1),
      ),
    );
    expect(
      (await repository.readVertex(
        'p',
        'short',
        policy: OfflineReadPolicy.cacheOnly,
      )).hasPendingWrites,
      isTrue,
    );
    clock.advance(const Duration(seconds: 1));
    final expiredOverlay = await repository.readVertex(
      'p',
      'short',
      policy: OfflineReadPolicy.cacheOnly,
    );
    expect(expiredOverlay.state, OfflineReadState.unknown);
    expect(expiredOverlay.value, isNull);
  });

  test(
    'wipe rejects late responses and canceled drain sends nothing',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final completer = Completer<void>();
      final remote = _DelayedRemote(completer);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'late', value: VertexValue.string('late')),
      );
      final draining = repository.drain('p');
      await Future<void>.delayed(Duration.zero);
      await repository.wipePartition('p');
      completer.complete();
      expect(await draining, 0);
      expect(
        await repository.readVertex(
          'p',
          'late',
          policy: OfflineReadPolicy.cacheOnly,
        ),
        isA<OfflineSnapshot<Vertex>>().having(
          (snapshot) => snapshot.state,
          'state',
          OfflineReadState.unknown,
        ),
      );

      final cancellation = LanternCancellationToken()..cancel();
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'cancel', value: VertexValue.string('cancel')),
      );
      await expectLater(
        repository.drain('p', cancellation: cancellation),
        throwsA(isA<OfflineCanceledException>()),
      );
    },
  );

  test(
    'late responses after lease expiration are rejected then safely replayed',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final completer = Completer<void>();
      final remote = _DelayedRemote(completer);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          leaseDuration: const Duration(seconds: 1),
        ),
      );
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'lease', value: VertexValue.string('lease')),
      );
      final draining = repository.drain('p');
      await remote.started.future;
      final leased = await store.transaction(
        (transaction) => transaction.outbox('p').single,
      );
      expect(leased.leaseUntil, initial.add(const Duration(seconds: 1)));
      clock.advance(const Duration(seconds: 1));
      expect(
        repository.config.clock(),
        initial.add(const Duration(seconds: 1)),
      );
      completer.complete();
      expect(await draining, 1);
      expect(remote.vertexPutCalls, 2);
      expect(
        (await repository.readVertex(
          'p',
          'lease',
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.fresh,
      );
    },
  );

  test(
    'plural enqueue is atomic and shares operation ordering metadata',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final operation = await repository.putVertices(
        partitionId: 'p',
        operationId: 'operation',
        inputs: <VertexInput>[
          VertexInput(
            key: 'a',
            value: VertexValue.string('a'),
            expiresIn: const Duration(minutes: 1),
          ),
          VertexInput(
            key: 'b',
            value: VertexValue.string('b'),
            expiresIn: const Duration(minutes: 1),
          ),
        ],
      );
      expect(operation.operationId, 'operation');
      expect(operation.itemCount, 2);
      final records = await store.transaction(
        (transaction) => transaction.outbox('p'),
      );
      expect(records.map((record) => record.ordinal), everyElement(1));
      expect(records.map((record) => record.itemIndex), <int>[0, 1]);
      expect(
        records.map((record) => record.absoluteExpiration),
        everyElement(initial.add(const Duration(minutes: 1))),
      );

      final bounded = OfflineLanternRepository(
        store: InMemoryOfflineStore(
          limits: const OfflineStoreLimits(
            maxOutboxRecords: 1,
            maxOutboxRecordsPerPartition: 1,
          ),
        ),
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      await expectLater(
        bounded.putVertices(
          partitionId: 'p',
          inputs: <VertexInput>[
            VertexInput(key: 'a', value: VertexValue.string('a')),
            VertexInput(key: 'b', value: VertexValue.string('b')),
          ],
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(
        await bounded.store.transaction(
          (transaction) => transaction.outbox('p'),
        ),
        isEmpty,
      );
    },
  );

  test('dead-letter authorization never holds the store transaction', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'a',
      input: VertexInput(key: 'bad', value: VertexValue.string('bad')),
    );
    remote.vertexPutFailures.add(
      failure(OfflineRemoteErrorKind.invalidArgument),
    );
    await repository.drain('a');
    final dead = (await repository.listDeadLetters('a')).single;
    final authorization = Completer<bool>();
    final authorizerStarted = Completer<void>();
    final inspecting = repository.inspectDeadLetter(
      'a',
      dead.recordId,
      authorize: (_) {
        authorizerStarted.complete();
        return authorization.future;
      },
    );
    await authorizerStarted.future;
    await repository
        .putVertex(
          partitionId: 'b',
          input: VertexInput(key: 'free', value: VertexValue.string('free')),
        )
        .timeout(const Duration(seconds: 1));
    authorization.complete(true);
    expect(await inspecting, isA<OfflinePutVertexIntent>());
  });

  test(
    'wiping a prefix partition does not close another status stream',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final handle = await repository.putVertex(
        partitionId: 'tenant:user',
        input: VertexInput(key: 'key', value: VertexValue.string('value')),
      );
      final statuses = handle.statuses.toList();
      await Future<void>.delayed(Duration.zero);
      await repository.wipePartition('tenant');
      expect(await repository.drain('tenant:user'), 1);
      expect((await statuses).last.state, OfflineWriteState.confirmed);
    },
  );

  test('a real probe is required before probe-triggered replay', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.string('value')),
    );
    remote.probeFailures.add(failure(OfflineRemoteErrorKind.unavailable));
    await expectLater(
      repository.probeAndDrain('p'),
      throwsA(isA<OfflineRemoteFailure>()),
    );
    expect(remote.vertexPutCalls, 0);
    expect(await repository.probeAndDrain('p'), 1);
    expect(remote.probeCalls, 2);
  });

  test('expired dead-letter retry never returns work to replay', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'short',
        value: VertexValue.string('short'),
        expiresIn: const Duration(seconds: 1),
      ),
    );
    remote.vertexPutFailures.add(
      failure(OfflineRemoteErrorKind.invalidArgument),
    );
    await repository.drain('p');
    final recordId = (await repository.listDeadLetters('p')).single.recordId;
    clock.advance(const Duration(seconds: 1));
    await repository.retryDeadLetter('p', recordId);
    expect(await repository.listPending('p'), isEmpty);
    expect(await repository.listDeadLetters('p'), isEmpty);
    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 1);
  });

  test(
    'durable operation status survives repository and store restart',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final first = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      final operation = await first.putVertices(
        partitionId: 'p',
        operationId: 'durable-operation',
        inputs: <VertexInput>[
          VertexInput(key: 'a', value: VertexValue.string('a')),
          VertexInput(key: 'b', value: VertexValue.string('b')),
        ],
      );
      await first.dispose();

      final restarted = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      final transitions = restarted
          .watchWrite('p', operation.operationId)
          .toList();
      await Future<void>.delayed(Duration.zero);
      expect(
        (await restarted.getWriteStatus(
          'p',
          operation.operationId,
        ))!.items.map((item) => item.state),
        everyElement(OfflineWriteState.locallyCommitted),
      );
      expect(await restarted.drain('p'), 2);
      final observed = await transitions;
      expect(observed.first.isTerminal, isFalse);
      expect(observed.last.isTerminal, isTrue);
      expect(observed.last.confirmedCount, 2);

      final restoredStore = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      final freshProcess = OfflineLanternRepository(
        store: restoredStore,
        remote: remote,
        config: testConfig(clock),
      );
      final durable = await freshProcess.getWriteStatus(
        'p',
        operation.operationId,
      );
      expect(durable!.isTerminal, isTrue);
      expect(durable.confirmedCount, 2);
    },
  );

  test('diagnostic sink failures cannot interrupt write transitions', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        diagnostics: const _ThrowingDiagnostics(),
      ),
    );
    final handle = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.string('value')),
    );

    expect(await repository.drain('p'), 1);
    expect(
      (await repository.getWriteStatus(
        'p',
        handle.operationId,
      ))!.items.single.state,
      OfflineWriteState.confirmed,
    );
  });

  test('long remote work renews its lease before confirmation', () async {
    final clock = MutableClock(initial);
    final completion = Completer<void>();
    final remote = _DelayedRemote(completion);
    final store = InMemoryOfflineStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        leaseDuration: const Duration(milliseconds: 60),
        leaseRenewalInterval: const Duration(milliseconds: 10),
      ),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'lease', value: VertexValue.string('lease')),
    );
    final draining = repository.drain('p');
    await remote.started.future;
    clock.advance(const Duration(milliseconds: 40));
    await Future<void>.delayed(const Duration(milliseconds: 25));
    final renewed = await store.transaction(
      (transaction) => transaction.outbox('p').single.leaseUntil,
    );
    expect(renewed, initial.add(const Duration(milliseconds: 100)));
    clock.advance(const Duration(milliseconds: 40));
    completion.complete();

    expect(await draining, 1);
    expect(remote.vertexPutCalls, 1);
  });

  test('disposed repositories reject new work', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    await repository.dispose();
    expect(
      () => repository.readVertex('p', 'key'),
      throwsA(isA<OfflineDisposedException>()),
    );
  });
}

final class _ThrowingDiagnostics implements OfflineDiagnostics {
  const _ThrowingDiagnostics();

  @override
  void record(OfflineDiagnosticEvent event) {
    throw StateError('diagnostics unavailable');
  }
}

final class _DelayedRemote extends FakeOfflineRemote {
  _DelayedRemote(this.completer);

  final Completer<void> completer;
  final Completer<void> started = Completer<void>();

  @override
  Future<void> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) {
    vertexPutCalls++;
    if (!started.isCompleted) started.complete();
    return completer.future;
  }
}
