import 'dart:async';
import 'dart:io';
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
      final legacyAdd = OfflineOutboxRecord(
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
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
      );
      final safePut = OfflineOutboxRecord(
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
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
      );
      final store = restoreLegacySnapshot(
        schema: 3,
        outbox: <OfflineOutboxRecord>[legacyAdd, safePut],
        operations: <OfflineOperationRecord>[
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'mixed-operation',
            items: <OfflineWriteStatus>[
              for (final record in <OfflineOutboxRecord>[legacyAdd, safePut])
                OfflineWriteStatus(
                  recordId: record.recordId,
                  operationId: record.operationId,
                  itemIndex: record.itemIndex,
                  state: OfflineWriteState.locallyCommitted,
                  attemptCount: 0,
                ),
            ],
            updatedAt: initial,
          ),
        ],
      );
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
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
    'auth pause is partition durable and only explicit resume clears it',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      await repository.putVertices(
        partitionId: 'p',
        inputs: <VertexInput>[
          VertexInput(key: 'a', value: VertexValue.string('a')),
          VertexInput(key: 'b', value: VertexValue.string('b')),
        ],
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.unauthenticated),
      );

      expect(await repository.drain('p'), 0);
      expect(remote.vertexPutCalls, 1);
      expect(await repository.isReplayPausedForAuth('p'), isTrue);
      for (final entryPoint in <Future<int> Function()>[
        () => repository.drain('p'),
        () => repository.start('p'),
        () => repository.probeAndDrain('p'),
      ]) {
        await expectLater(
          entryPoint(),
          throwsA(isA<OfflineAuthPausedException>()),
        );
      }
      expect(remote.vertexPutCalls, 1);
      expect(remote.probeCalls, 0);
      expect(
        (await store.transaction(
          (transaction) => transaction.outbox('p'),
        )).map((record) => record.attemptCount),
        everyElement(0),
      );

      final restored = OfflineLanternRepository(
        store: InMemoryOfflineStore.fromSnapshot(await store.exportSnapshot()),
        remote: remote,
        config: testConfig(clock),
      );
      expect(await restored.isReplayPausedForAuth('p'), isTrue);
      expect(await restored.resume('p'), 2);
      expect(remote.vertexPutCalls, 3);
      expect(await restored.isReplayPausedForAuth('p'), isFalse);
    },
  );

  test('schema v3 operation auth pause requires explicit resume', () async {
    final store = InMemoryOfflineStore.fromSnapshot(
      File(
        'test/fixtures/v3_snapshot_auth_pause.json',
      ).readAsStringSync().trim(),
    );
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: () => DateTime.fromMicrosecondsSinceEpoch(1, isUtc: true),
        idGenerator: testConfig(MutableClock(initial)).idGenerator,
        jitter: (_) => Duration.zero,
      ),
    );

    expect(await repository.isReplayPausedForAuth('legacy-user'), isTrue);
    await expectLater(
      repository.drain('legacy-user'),
      throwsA(isA<OfflineAuthPausedException>()),
    );
    expect(remote.vertexPutCalls, 0);
    final migrated = await store.transaction(
      (transaction) => transaction.outbox('legacy-user').single,
    );
    expect(migrated.diagnosticCode, 'unauthenticated');
    expect(await repository.resume('legacy-user'), 1);
    expect(remote.vertexPutCalls, 1);
    expect(await repository.isReplayPausedForAuth('legacy-user'), isFalse);
  });

  test('replay send bounds apply across partitions and entry points', () async {
    final clock = MutableClock(initial);
    final remote = _ConcurrentRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxConcurrency: 2,
        maxConcurrencyPerPartition: 1,
        maxQueuedReplaySends: 2,
        maxQueuedReplaySendsPerPartition: 1,
        maxQueuedReplaysPerPartition: 1,
      ),
    );
    for (final partition in <String>['a', 'b']) {
      await repository.putVertices(
        partitionId: partition,
        inputs: <VertexInput>[
          VertexInput(key: '$partition-1', value: VertexValue.nil()),
          VertexInput(key: '$partition-2', value: VertexValue.nil()),
        ],
      );
    }
    final first = repository.start('a');
    final second = repository.probeAndDrain('b');
    await remote.twoStarted.future;
    final queued = repository.drain('a');
    await expectLater(
      repository.resume('a'),
      throwsA(isA<OfflineCapacityException>()),
    );
    expect(remote.maxActive, 2);
    expect(remote.maxActiveByPartition.values, everyElement(1));
    remote.release();
    expect(await first + await second + await queued, 4);
    expect(remote.maxActive, 2);
    expect(remote.maxActiveByPartition.values, everyElement(1));
  });

  test('canceled queued replay cannot overtake its predecessor', () async {
    final clock = MutableClock(initial);
    final remote = _AuthGateRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxConcurrency: 1,
        maxConcurrencyPerPartition: 1,
        maxQueuedReplaysPerPartition: 1,
      ),
    );
    await repository.putVertices(
      partitionId: 'p',
      inputs: <VertexInput>[
        VertexInput(key: 'a', value: VertexValue.nil()),
        VertexInput(key: 'c', value: VertexValue.nil()),
      ],
    );
    final first = repository.drain('p');
    await remote.started.future;
    final queuedCancellation = LanternCancellationToken();
    final canceled = repository.drain('p', cancellation: queuedCancellation);
    queuedCancellation.cancel();
    await expectLater(canceled, throwsA(isA<OfflineCanceledException>()));

    final third = repository.drain('p');
    await Future<void>.delayed(Duration.zero);
    expect(remote.vertexPutCalls, 1);
    remote.releaseUnauthenticated();
    expect(await first, 0);
    await expectLater(third, throwsA(isA<OfflineAuthPausedException>()));
    expect(remote.vertexPutCalls, 1);
  });

  test(
    'old partition work never sends with credentials rotated after wipe',
    () async {
      final clock = MutableClock(initial);
      var credential = 'old-token';
      final remote = _CredentialRecordingRemote(() => credential);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      await repository.putVertices(
        partitionId: 'old-user',
        inputs: <VertexInput>[
          VertexInput(key: 'old-a', value: VertexValue.nil()),
          VertexInput(key: 'old-b', value: VertexValue.nil()),
        ],
      );
      final draining = repository.drain('old-user');
      final canceledDrain = expectLater(
        draining,
        throwsA(isA<OfflineCanceledException>()),
      );
      await remote.started.future;
      await repository.wipePartition('old-user');
      await canceledDrain;

      credential = 'new-token';
      remote.release();
      await repository.putVertex(
        partitionId: 'new-user',
        input: VertexInput(key: 'new-a', value: VertexValue.nil()),
      );
      expect(await repository.drain('new-user'), 1);
      expect(remote.credentialsForOldKeys, everyElement('old-token'));
      expect(remote.credentialsByKey['old-b'], isNull);
      expect(remote.credentialsByKey['new-a'], 'new-token');
    },
  );

  test('repeated wipe and dispose leave the lifecycle quiesced', () async {
    final clock = MutableClock(initial);
    final remote = _DelayedRemote(Completer<void>());
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.nil()),
    );
    final draining = repository.drain('p');
    final canceledDrain = expectLater(
      draining,
      throwsA(isA<OfflineCanceledException>()),
    );
    await remote.started.future;
    await Future.wait<void>(<Future<void>>[
      repository.wipePartition('p'),
      repository.wipePartition('p'),
    ]);
    await canceledDrain;
    await repository.wipePartition('p');
    await Future.wait<void>(<Future<void>>[
      repository.dispose(),
      repository.dispose(),
    ]);
    expect(
      () => repository.drain('p'),
      throwsA(isA<OfflineDisposedException>()),
    );
  });

  test('failed store wipe keeps the partition closed until retry', () async {
    final clock = MutableClock(initial);
    final store = _FailNextTransactionStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'secret', value: VertexValue.string('secret')),
    );
    store.failNext();

    await expectLater(repository.wipePartition('p'), throwsStateError);
    expect(
      () => repository.readVertex(
        'p',
        'secret',
        policy: OfflineReadPolicy.cacheOnly,
      ),
      throwsA(isA<OfflineCanceledException>()),
    );

    await repository.wipePartition('p');
    expect(
      (await repository.readVertex(
        'p',
        'secret',
        policy: OfflineReadPolicy.cacheOnly,
      )).state,
      OfflineReadState.unknown,
    );
  });

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

  test('dead-letter retention starts at its terminal transition', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxAge: const Duration(hours: 2),
        deadLetterRetention: const Duration(hours: 1),
      ),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'late-dead', value: VertexValue.string('value')),
    );
    clock.advance(const Duration(minutes: 59));
    remote.vertexPutFailures.add(
      failure(OfflineRemoteErrorKind.invalidArgument),
    );
    expect(await repository.drain('p'), 0);

    clock.advance(const Duration(minutes: 2));
    expect(await repository.listDeadLetters('p'), hasLength(1));
    clock.advance(const Duration(minutes: 59));
    expect(await repository.listDeadLetters('p'), isEmpty);
  });

  test(
    'dead-letter transition time is sampled inside its transaction',
    () async {
      final clock = MutableClock(initial);
      final store = _TransactionSignalingStore(InMemoryOfflineStore());
      final remote = _GatedFailureRemote();
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          deadLetterRetention: const Duration(seconds: 1),
        ),
      );
      addTearDown(repository.dispose);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'blocked-failure', value: VertexValue.nil()),
      );
      final draining = repository.drain('p');
      await remote.started.future;

      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;
      final failureTransactionQueued = store.signalNextTransaction();
      remote.release.complete();
      await failureTransactionQueued;
      clock.advance(const Duration(seconds: 2));
      releaseBlocker.complete();
      await blocker;

      expect(await draining, 0);
      final retained = await store.transaction(
        (transaction) => transaction.outbox('p').single,
      );
      expect(retained.state, OfflineOutboxState.deadLetter);
      expect(retained.deadLetteredAt, clock.now);
      expect(await repository.listDeadLetters('p'), hasLength(1));
    },
  );

  test('clock rollback after claim keeps retry metadata monotone', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final remote = _GatedFailureRemote(OfflineRemoteErrorKind.unavailable);
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'rollback-retry', value: VertexValue.nil()),
    );
    final draining = repository.drain('p');
    await remote.started.future;
    clock.advance(const Duration(minutes: -1));
    remote.release.complete();

    expect(await draining, 0);
    expect(remote.vertexPutCalls, 1);
    final retry = await store.transaction(
      (transaction) => transaction.outbox('p').single,
    );
    expect(retry.state, OfflineOutboxState.enqueued);
    expect(retry.nextAttemptAt, isNotNull);
    expect(retry.nextAttemptAt!.isBefore(retry.enqueuedAt), isFalse);
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

  test('retry deadlines saturate inside the durable time range', () async {
    final nearMaximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 998);
    final maximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 999);
    final clock = MutableClock(nearMaximum);
    final store = InMemoryOfflineStore();
    final remote = FakeOfflineRemote()
      ..vertexPutFailures.add(failure(OfflineRemoteErrorKind.unavailable));
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (ceiling) => ceiling,
        baseRetryDelay: const Duration(seconds: 1),
        maxRetryDelay: const Duration(seconds: 1),
      ),
    );
    addTearDown(repository.dispose);
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'maximum-retry', value: VertexValue.nil()),
    );

    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 1);
    final retry = await store.transaction(
      (transaction) => transaction.outbox('p').single,
    );
    expect(retry.state, OfflineOutboxState.enqueued);
    expect(retry.nextAttemptAt, maximum);
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

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
    'authoritative Put outcomes preserve item order and terminalize exactly',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(maxDiagnosticCodeBytes: 19),
      );
      await store.transaction<void>((transaction) {
        transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.vertex('condition'),
            entity: Vertex(
              key: 'condition',
              value: VertexValue.string('old'),
              expiration: null,
            ),
            validatedAt: initial,
            lastAccessAt: initial,
          ),
        );
        transaction.putCache(
          'p',
          OfflineCacheRecord.value(
            partitionId: 'p',
            generation: 0,
            key: const OfflineEntityKey.edge('tail', 'head'),
            entity: Edge(
              tail: 'tail',
              head: 'head',
              weight: 9,
              expiration: null,
            ),
            validatedAt: initial,
            lastAccessAt: initial,
          ),
        );
      });
      final remote = FakeOfflineRemote()
        ..vertexPutOutcomes.addAll(<PutOutcome>[
          PutOutcome.appliedAndLive,
          PutOutcome.expired,
          PutOutcome.conditionNotMet,
          PutOutcome.superseded,
        ])
        ..edgePutOutcomes.add(PutOutcome.expired);
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxConcurrency: 1,
          maxConcurrencyPerPartition: 1,
        ),
      );
      addTearDown(repository.dispose);
      final vertices = await repository.putVertices(
        partitionId: 'p',
        inputs: <VertexInput>[
          VertexInput(key: 'applied', value: VertexValue.string('new')),
          VertexInput(key: 'expired', value: VertexValue.string('new')),
          VertexInput(key: 'condition', value: VertexValue.string('new')),
          VertexInput(key: 'superseded', value: VertexValue.string('new')),
        ],
      );
      final edge = await repository.putEdge(
        partitionId: 'p',
        input: EdgeInput(tail: 'tail', head: 'head', weight: 1),
      );

      expect(await repository.drain('p'), 1);
      final vertexStatus = await repository.getWriteStatus(
        'p',
        vertices.operationId,
      );
      expect(vertexStatus!.items.map((item) => item.state), <OfflineWriteState>[
        OfflineWriteState.confirmed,
        OfflineWriteState.expired,
        OfflineWriteState.deadLetter,
        OfflineWriteState.deadLetter,
      ]);
      expect(
        vertexStatus.items.map((item) => item.attemptCount),
        everyElement(1),
      );
      expect(vertexStatus.items.map((item) => item.diagnosticCode), <String?>[
        null,
        'put_expired',
        'condition_not_met',
        'put_superseded',
      ]);
      final edgeStatus = await repository.getWriteStatus('p', edge.operationId);
      expect(edgeStatus!.items.single.state, OfflineWriteState.expired);
      expect(edgeStatus.items.single.attemptCount, 1);
      expect(edgeStatus.items.single.diagnosticCode, 'put_expired');

      final deadLetters = await store.transaction(
        (transaction) => transaction
            .outbox('p')
            .where((record) => record.state == OfflineOutboxState.deadLetter)
            .toList(growable: false),
      );
      expect(deadLetters, hasLength(2));
      expect(
        deadLetters.map((record) => record.deadLetteredAt),
        everyElement(initial),
      );
      expect(deadLetters.map((record) => record.diagnosticCode), <String?>[
        'condition_not_met',
        'put_superseded',
      ]);
      expect(
        deadLetters.map((record) => record.diagnosticCode!.length),
        everyElement(lessThanOrEqualTo(19)),
      );
      expect(
        (await repository.readVertex(
          'p',
          'condition',
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
      expect(
        (await repository.readEdge(
          'p',
          const EdgeRef('tail', 'head'),
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
    },
  );

  test(
    'response and commit expiration samples remain sticky across rollback',
    () async {
      Future<void> verify({required bool expireBeforeResponse}) async {
        final clock = MutableClock(initial);
        final store = _GateNextTransactionStore();
        final response = Completer<void>();
        final remote = _DelayedRemote(response);
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        final write = await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: expireBeforeResponse ? 'response' : 'commit',
            value: VertexValue.string('value'),
            expiresAt: initial.add(const Duration(seconds: 1)),
          ),
        );
        final draining = repository.drain('p');
        await remote.started.future;
        store.holdNext();
        if (expireBeforeResponse) {
          clock.advance(const Duration(seconds: 2));
        }
        response.complete();
        await store.blocked;
        if (expireBeforeResponse) {
          clock.now = initial;
        } else {
          clock.advance(const Duration(seconds: 2));
        }
        store.release();

        expect(await draining, 0);
        final status = await repository.getWriteStatus('p', write.operationId);
        expect(status!.items.single.state, OfflineWriteState.expired);
        expect(status.items.single.attemptCount, 1);
        expect(status.items.single.diagnosticCode, 'expired_at_commit');
        expect(
          (await repository.readVertex(
            'p',
            expireBeforeResponse ? 'response' : 'commit',
            policy: OfflineReadPolicy.cacheOnly,
          )).state,
          OfflineReadState.unknown,
        );
        await repository.dispose();
      }

      await verify(expireBeforeResponse: true);
      await verify(expireBeforeResponse: false);
    },
  );

  test(
    'observation expires idle work, reclaims capacity, and closes status',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 1,
          maxOutboxRecordsPerPartition: 1,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final first = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'short',
          value: VertexValue.string('short'),
          expiresIn: const Duration(seconds: 1),
        ),
      );
      final statuses = first.statuses.toList();

      clock.advance(const Duration(seconds: 1));
      expect(await repository.listPending('p'), isEmpty);
      expect(
        (await repository.getWriteStatus(
          'p',
          first.operationId,
        ))!.items.single.state,
        OfflineWriteState.expired,
      );
      expect(
        (await statuses).map((status) => status.state),
        <OfflineWriteState>[
          OfflineWriteState.locallyCommitted,
          OfflineWriteState.expired,
        ],
      );
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );

      final second = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'next', value: VertexValue.string('next')),
      );
      expect(second.recordId, isNot(first.recordId));
      expect(await repository.listPending('p'), hasLength(1));
    },
  );

  test(
    'immediately expired enqueue returns a closed terminal stream',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 0,
          maxOutboxRecordsPerPartition: 0,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);

      final handle = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'expired',
          value: VertexValue.string('expired'),
          expiresAt: initial,
        ),
      );
      final statuses = await handle.statuses.toList();
      expect(statuses, hasLength(1));
      expect(statuses.single.state, OfflineWriteState.expired);
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );
    },
  );

  test(
    'enqueue uses commit time without rebasing its absolute expiration',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxOutboxRecords: 1,
          maxOutboxRecordsPerPartition: 1,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;

      final pending = repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'crossed-before-commit',
          value: VertexValue.string('value'),
          expiresIn: const Duration(seconds: 1),
        ),
      );
      clock.advance(const Duration(seconds: 1));
      releaseBlocker.complete();
      await blocker;
      final first = await pending;

      expect(
        (await first.statuses.toList()).single.state,
        OfflineWriteState.expired,
      );
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'capacity-reused', value: VertexValue.nil()),
      );
      expect(await repository.listPending('p'), hasLength(1));
    },
  );

  test('clock rollback cannot revive an already expired enqueue', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore(
      limits: const OfflineStoreLimits(
        maxOutboxRecords: 0,
        maxOutboxRecordsPerPartition: 0,
      ),
    );
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxWriteStatusControllers: 1,
      ),
    );
    addTearDown(repository.dispose);
    final blockerStarted = Completer<void>();
    final releaseBlocker = Completer<void>();
    final blocker = store.transaction<void>((_) async {
      blockerStarted.complete();
      await releaseBlocker.future;
    });
    await blockerStarted.future;

    final pending = repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'cannot-revive',
        value: VertexValue.string('value'),
        expiresAt: initial,
      ),
    );
    clock.advance(const Duration(seconds: -1));
    releaseBlocker.complete();
    await blocker;
    final handle = await pending;

    expect(
      (await handle.statuses.toList()).single.state,
      OfflineWriteState.expired,
    );
    expect(
      await store.transaction((transaction) => transaction.outbox('p')),
      isEmpty,
    );
    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 0);
    final status = await repository.getWriteStatus('p', handle.operationId);
    expect(status!.items.single.attemptCount, 0);
    expect(status.items.single.diagnosticCode, 'expired');
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

  test(
    'repository rejects out-of-range expirations without durable work',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);

      for (final expiration in <DateTime>[
        DateTime.utc(0),
        DateTime.utc(10000),
      ]) {
        await expectLater(
          repository.putVertex(
            partitionId: 'p',
            input: VertexInput(
              key: 'invalid-${expiration.year}',
              value: VertexValue.nil(),
              expiresAt: expiration,
            ),
          ),
          throwsA(isA<OfflineArgumentException>()),
        );
      }
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );
      expect(
        await store.transaction((transaction) => transaction.operations('p')),
        isEmpty,
      );
    },
  );

  test(
    'observation uses transaction time after waiting behind the store',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final write = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'crossed-before-observation',
          value: VertexValue.string('value'),
          expiresIn: const Duration(seconds: 1),
        ),
      );
      final statuses = write.statuses.toList();
      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;

      final observed = repository.getWriteStatus('p', write.operationId);
      clock.advance(const Duration(seconds: 1));
      releaseBlocker.complete();
      await blocker;

      expect((await observed)!.items.single.state, OfflineWriteState.expired);
      expect(
        (await statuses).map((status) => status.state),
        <OfflineWriteState>[
          OfflineWriteState.locallyCommitted,
          OfflineWriteState.expired,
        ],
      );
    },
  );

  test(
    'born-expired plural handles allocate no live status controllers',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWriteStatusControllers: 1,
        ),
      );
      addTearDown(repository.dispose);

      final expired = await repository.putVertices(
        partitionId: 'p',
        inputs: List<VertexInput>.generate(
          64,
          (index) => VertexInput(
            key: 'expired-$index',
            value: VertexValue.string('expired'),
            expiresAt: initial,
          ),
        ),
      );
      expect(
        (await expired.items.last.statuses.toList()).single.state,
        OfflineWriteState.expired,
      );
      expect(
        (await expired.items.last.statuses.toList()).single.state,
        OfflineWriteState.expired,
      );

      final live = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'live', value: VertexValue.string('live')),
      );
      expect(
        (await live.statuses.first).state,
        OfflineWriteState.locallyCommitted,
      );
    },
  );

  test('watchWrite observes idle expiration and closes', () async {
    final clock = MutableClock(initial);
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    addTearDown(repository.dispose);
    final write = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'watched-expiration',
        value: VertexValue.string('value'),
        expiresIn: const Duration(seconds: 1),
      ),
    );
    clock.advance(const Duration(seconds: 1));

    final statuses = await repository
        .watchWrite('p', write.operationId)
        .toList();
    expect(statuses, hasLength(1));
    expect(statuses.single.items.single.state, OfflineWriteState.expired);
  });

  test(
    'watchWrite reports synchronous store capacity and releases watcher',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(maxChangeControllers: 1),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final held = store.changes('held').listen((_) {});
      final terminal = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'already-expired',
          value: VertexValue.nil(),
          expiresAt: initial,
        ),
      );

      await expectLater(
        repository.watchWrite('p', terminal.operationId).toList(),
        throwsA(isA<OfflineCapacityException>()),
      );
      await held.cancel();
      final recovered = await repository
          .watchWrite('p', terminal.operationId)
          .toList();
      expect(recovered.single.isTerminal, isTrue);
    },
  );

  test(
    'lazy maintenance scans a bounded page and prioritizes a target operation',
    () async {
      final clock = MutableClock(initial);
      final store = _InspectingStore(InMemoryOfflineStore());
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxAge: const Duration(hours: 1),
          maxSweepRecordsPerObservation: 2,
        ),
      );
      addTearDown(repository.dispose);
      for (var index = 0; index < 12; index++) {
        await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: 'queued-$index',
            value: VertexValue.string('value'),
          ),
        );
      }
      clock.advance(const Duration(hours: 1));
      store.resetInspection();

      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'fresh', value: VertexValue.string('fresh')),
      );
      expect(store.outboxRecordsInspected, 2);
      expect(store.operationRecordsInspected, lessThanOrEqualTo(2));
      final retained = await store.inner.transaction(
        (transaction) => transaction.outbox('p'),
      );
      expect(
        retained.where(
          (record) => record.state == OfflineOutboxState.deadLetter,
        ),
        hasLength(2),
      );
      final target = retained.lastWhere(
        (record) =>
            record.state == OfflineOutboxState.enqueued &&
            record.intent.key.vertexKey != 'fresh',
      );
      store.resetInspection();

      final targetStatus = await repository.getWriteStatus(
        'p',
        target.operationId,
      );
      expect(store.outboxRecordsInspected, 1);
      expect(targetStatus!.items.single.state, OfflineWriteState.deadLetter);
    },
  );

  test(
    'operation deadline index finds an expired tail beyond the sweep limit',
    () async {
      final clock = MutableClock(initial);
      final store = _InspectingStore(InMemoryOfflineStore());
      await store.inner.transaction((transaction) {
        final assigned = transaction.enqueueAll(
          List<OfflineOutboxRecord>.generate(260, (index) {
            final isTail = index >= 256;
            return OfflineOutboxRecord(
              recordId: 'mixed-record-$index',
              operationId: 'mixed-large-operation',
              itemIndex: index,
              partitionId: 'p',
              intent: OfflinePutVertexIntent(
                Vertex(
                  key: 'mixed-key-$index',
                  value: VertexValue.string('value'),
                  expiration: isTail
                      ? initial.add(const Duration(seconds: 1))
                      : null,
                ),
              ),
              enqueuedAt: initial,
              ordinal: 0,
              state: isTail
                  ? OfflineOutboxState.enqueued
                  : OfflineOutboxState.deadLetter,
              attemptCount: 0,
              generation: 0,
              deadLetteredAt: isTail ? null : initial,
              diagnosticCode: isTail ? null : 'seed',
            );
          }),
        );
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: 'mixed-large-operation',
            items: assigned
                .map(
                  (record) => OfflineWriteStatus(
                    recordId: record.recordId,
                    operationId: record.operationId,
                    itemIndex: record.itemIndex,
                    state: record.state == OfflineOutboxState.deadLetter
                        ? OfflineWriteState.deadLetter
                        : OfflineWriteState.locallyCommitted,
                    attemptCount: 0,
                    diagnosticCode: record.diagnosticCode,
                  ),
                )
                .toList(growable: false),
            updatedAt: initial,
          ),
        );
      });
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxSweepRecordsPerObservation: 2,
        ),
      );
      addTearDown(repository.dispose);
      clock.advance(const Duration(seconds: 1));
      store.resetInspection();

      final statuses = await repository
          .watchWrite('p', 'mixed-large-operation')
          .toList();
      expect(store.outboxRecordsInspected, 4);
      expect(store.maxOutboxBatchInspected, 2);
      expect(statuses.last.isTerminal, isTrue);
      expect(statuses.last.deadLetterCount, 256);
      expect(statuses.last.expiredCount, 4);
    },
  );

  test(
    'generated identity collisions exhaust a bounded retry atomically',
    () async {
      final clock = MutableClock(initial);
      final ids = <String>['op', 'record', 'op', 'record', 'op', 'record'];
      var index = 0;
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: () => ids[index++],
          jitter: (_) => Duration.zero,
          maxGeneratedIdAttempts: 2,
        ),
      );
      addTearDown(repository.dispose);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'first', value: VertexValue.string('first')),
      );
      await expectLater(
        repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: 'second',
            value: VertexValue.string('second'),
          ),
        ),
        throwsA(isA<OfflineIdGenerationException>()),
      );
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        hasLength(1),
      );
    },
  );

  test(
    'caller operation ID collisions never replace retained intent',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      final original = await repository.putVertex(
        partitionId: 'p',
        operationId: 'caller-operation',
        input: VertexInput(key: 'original', value: VertexValue.string('value')),
      );
      for (final key in <String>['original', 'different']) {
        await expectLater(
          repository.putVertex(
            partitionId: 'p',
            operationId: 'caller-operation',
            input: VertexInput(key: key, value: VertexValue.string('value')),
          ),
          throwsA(
            isA<OfflineIdentityConflictException>().having(
              (error) => error.kind,
              'kind',
              OfflineIdentityKind.operation,
            ),
          ),
        );
      }
      final retained = await store.transaction(
        (transaction) => transaction.outbox('p').single,
      );
      expect(retained.recordId, original.recordId);
      expect(retained.intent.key.vertexKey, 'original');
    },
  );

  test(
    'terminal status controllers release their configured capacity',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWriteStatusControllers: 1,
        ),
      );
      addTearDown(repository.dispose);
      final first = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'first', value: VertexValue.string('first')),
      );
      await expectLater(
        repository.putVertex(
          partitionId: 'p',
          input: VertexInput(
            key: 'blocked',
            value: VertexValue.string('blocked'),
          ),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(await repository.drain('p'), 1);
      await Future<void>.delayed(Duration.zero);

      final lateStatuses = await first.statuses.toList();
      expect(lateStatuses.single.state, OfflineWriteState.confirmed);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'after', value: VertexValue.string('after')),
      );
      expect(await repository.listPending('p'), hasLength(1));
    },
  );

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
      final canceledDrain = expectLater(
        draining,
        throwsA(isA<OfflineCanceledException>()),
      );
      await remote.started.future;
      await repository.wipePartition('p');
      await canceledDrain;
      completer.complete();
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

  test('released claim exports a self-consistent restart snapshot', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final remote = _CancelableRemote();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    addTearDown(repository.dispose);
    final write = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'cancel', value: VertexValue.string('cancel')),
    );
    final cancellation = LanternCancellationToken();
    final draining = repository.drain('p', cancellation: cancellation);
    await remote.started.future;
    cancellation.cancel();
    await expectLater(draining, throwsA(isA<OfflineCanceledException>()));

    final restored = InMemoryOfflineStore.fromSnapshot(
      await store.exportSnapshot(),
    );
    final record = await restored.transaction(
      (transaction) => transaction.outbox('p').single,
    );
    final operation = await restored.transaction(
      (transaction) => transaction.getOperation('p', write.operationId)!,
    );
    expect(record.diagnosticCode, 'canceled');
    expect(operation.items.single.diagnosticCode, 'canceled');
  });

  test('transport cancellation releases a consistent durable claim', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final remote = _DelayedRemote(Completer<void>());
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    final operation = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'cancel', value: VertexValue.nil()),
    );
    final cancellation = LanternCancellationToken();
    final draining = repository.drain('p', cancellation: cancellation);
    await remote.started.future;
    cancellation.cancel();

    await expectLater(draining, throwsA(isA<OfflineCanceledException>()));
    final durable = await store.transaction((transaction) {
      return (
        outbox: transaction.outbox('p').single,
        operation: transaction.getOperation('p', operation.operationId)!,
      );
    });
    expect(durable.outbox.state, OfflineOutboxState.enqueued);
    expect(durable.outbox.leaseOwner, isNull);
    expect(durable.outbox.leaseUntil, isNull);
    expect(durable.outbox.attemptCount, 0);
    expect(durable.outbox.diagnosticCode, 'canceled');
    expect(
      durable.operation.items.single.state,
      OfflineWriteState.locallyCommitted,
    );
    expect(durable.operation.items.single.attemptCount, 0);
    expect(durable.operation.items.single.diagnosticCode, 'canceled');
    await repository.dispose();
  });

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
      final write = await repository.putVertex(
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
      final status = await repository.getWriteStatus('p', write.operationId);
      expect(status!.items.single.state, OfflineWriteState.confirmed);
      expect(status.items.single.attemptCount, 1);
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
    'blocked dead-letter authorization is quiesced before wipe and ID reuse',
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
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'old', value: VertexValue.string('old')),
      );
      remote.vertexPutFailures.add(
        failure(OfflineRemoteErrorKind.invalidArgument),
      );
      await repository.drain('p');
      final old = await store.transaction(
        (transaction) => transaction.outbox('p').single,
      );
      final authorization = Completer<bool>();
      final authorizerStarted = Completer<void>();
      final inspection = repository.inspectDeadLetter(
        'p',
        old.recordId,
        authorize: (_) {
          authorizerStarted.complete();
          return authorization.future;
        },
      );
      final canceledInspection = expectLater(
        inspection,
        throwsA(isA<OfflineCanceledException>()),
      );
      await authorizerStarted.future;

      var wipeCompleted = false;
      final wiping = repository.wipePartition('p').whenComplete(() {
        wipeCompleted = true;
      });
      await canceledInspection;
      await wiping.timeout(const Duration(seconds: 1));
      expect(wipeCompleted, isTrue);

      await _seedDeadLetter(
        store,
        partitionId: 'p',
        recordId: old.recordId,
        operationId: old.operationId,
        key: 'new',
        now: initial,
      );
      authorization.complete(true);
      await Future<void>.delayed(Duration.zero);
      final replacement = await repository.inspectDeadLetter(
        'p',
        old.recordId,
        authorize: (_) async => true,
      );
      expect(
        replacement,
        isA<OfflinePutVertexIntent>().having(
          (intent) => intent.vertex.key,
          'key',
          'new',
        ),
      );
    },
  );

  test(
    'dead-letter inspection rejects a same ID reused during authorization',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(repository.dispose);
      await _seedDeadLetter(
        store,
        partitionId: 'p',
        recordId: 'same-record',
        operationId: 'same-operation',
        key: 'old',
        now: initial,
      );

      final inspected = await repository.inspectDeadLetter(
        'p',
        'same-record',
        authorize: (_) async {
          await store.transaction((transaction) {
            transaction.wipePartition('p');
          });
          await _seedDeadLetter(
            store,
            partitionId: 'p',
            recordId: 'same-record',
            operationId: 'same-operation',
            key: 'new',
            now: initial,
          );
          return true;
        },
      );

      expect(inspected, isNull);
      expect(
        await repository.inspectDeadLetter(
          'p',
          'same-record',
          authorize: (_) async => true,
        ),
        isA<OfflinePutVertexIntent>().having(
          (intent) => intent.vertex.key,
          'key',
          'new',
        ),
      );
    },
  );

  test(
    'Store-facing recovery calls are quiesced by lifecycle closure',
    () async {
      Future<void> verify({
        required String name,
        required bool deadLetter,
        required bool dispose,
        required Future<void> Function(
          OfflineLanternRepository repository,
          OfflineWriteHandle handle,
          String recordId,
        )
        action,
      }) async {
        final clock = MutableClock(initial);
        final store = _GateNextTransactionStore();
        final remote = FakeOfflineRemote();
        final repository = OfflineLanternRepository(
          store: store,
          remote: remote,
          config: testConfig(clock),
        );
        final handle = await repository.putVertex(
          partitionId: 'p',
          input: VertexInput(key: name, value: VertexValue.string(name)),
        );
        if (deadLetter) {
          remote.vertexPutFailures.add(
            failure(OfflineRemoteErrorKind.invalidArgument),
          );
          await repository.drain('p');
        }
        final recordId = await store.transaction(
          (transaction) => transaction.outbox('p').single.recordId,
        );
        store.holdNext();
        final active = action(repository, handle, recordId);
        await store.blocked;
        var lifecycleCompleted = false;
        final lifecycle =
            (dispose ? repository.dispose() : repository.wipePartition('p'))
                .whenComplete(() {
                  lifecycleCompleted = true;
                });
        await Future<void>.delayed(Duration.zero);
        expect(lifecycleCompleted, isFalse, reason: name);
        store.release();
        await active;
        await lifecycle;
        await repository.dispose();
      }

      await verify(
        name: 'get-status',
        deadLetter: false,
        dispose: false,
        action: (repository, handle, _) => repository
            .getWriteStatus('p', handle.operationId)
            .then<void>((status) => expect(status, isNotNull)),
      );
      await verify(
        name: 'list-pending',
        deadLetter: false,
        dispose: true,
        action: (repository, _, _) => repository
            .listPending('p')
            .then<void>((items) => expect(items, hasLength(1))),
      );
      await verify(
        name: 'list-dead-letters',
        deadLetter: true,
        dispose: false,
        action: (repository, _, _) => repository
            .listDeadLetters('p')
            .then<void>((items) => expect(items, hasLength(1))),
      );
      await verify(
        name: 'retry-dead-letter',
        deadLetter: true,
        dispose: true,
        action: (repository, _, recordId) =>
            repository.retryDeadLetter('p', recordId),
      );
      await verify(
        name: 'delete-dead-letter',
        deadLetter: true,
        dispose: false,
        action: (repository, _, recordId) =>
            repository.deleteDeadLetter('p', recordId),
      );
      await verify(
        name: 'auth-pause-status',
        deadLetter: false,
        dispose: true,
        action: (repository, _, _) => repository
            .isReplayPausedForAuth('p')
            .then<void>((paused) => expect(paused, isFalse)),
      );
    },
  );

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

  for (final remoteWouldFail in <bool>[false, true]) {
    test('max durable attempt count is terminal before wire '
        '(remoteWouldFail: $remoteWouldFail)', () async {
      const maximum = 0x7fffffffffffffff;
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      await store.transaction<void>((transaction) {
        final assigned = transaction.enqueue(
          OfflineOutboxRecord(
            recordId: 'max-attempt-record',
            operationId: 'max-attempt-operation',
            itemIndex: 0,
            partitionId: 'p',
            intent: OfflinePutVertexIntent(
              Vertex(
                key: 'max-attempt',
                value: VertexValue.nil(),
                expiration: null,
              ),
            ),
            enqueuedAt: initial,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: maximum,
            generation: 0,
          ),
        );
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: 'p',
            generation: 0,
            operationId: assigned.operationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: assigned.recordId,
                operationId: assigned.operationId,
                itemIndex: 0,
                state: OfflineWriteState.locallyCommitted,
                attemptCount: maximum,
              ),
            ],
            updatedAt: initial,
          ),
        );
      });
      final remote = FakeOfflineRemote();
      if (remoteWouldFail) {
        remote.vertexPutFailures.add(
          failure(OfflineRemoteErrorKind.unavailable),
        );
      }
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxAttempts: maximum,
        ),
      );
      addTearDown(repository.dispose);

      expect(await repository.drain('p'), 0);
      expect(remote.vertexPutCalls, 0);
      final terminal = await store.transaction(
        (transaction) => transaction.outbox('p').single,
      );
      expect(terminal.state, OfflineOutboxState.deadLetter);
      expect(terminal.attemptCount, maximum);
      expect(terminal.leaseOwner, isNull);
      expect(terminal.leaseUntil, isNull);
      expect(terminal.diagnosticCode, 'max_attempts');
      final snapshot = await store.exportSnapshot();
      expect(
        () => InMemoryOfflineStore.fromSnapshot(snapshot),
        returnsNormally,
      );
    });
  }

  test('a lower restart attempt budget terminalizes before wire', () async {
    const completedAttempts = 8;
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    await store.transaction<void>((transaction) {
      final assigned = transaction.enqueue(
        OfflineOutboxRecord(
          recordId: 'downgraded-attempt-record',
          operationId: 'downgraded-attempt-operation',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(
              key: 'downgraded-attempt',
              value: VertexValue.nil(),
              expiration: null,
            ),
          ),
          enqueuedAt: initial,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: completedAttempts,
          generation: 0,
        ),
      );
      transaction.putOperation(
        OfflineOperationRecord(
          partitionId: 'p',
          generation: 0,
          operationId: assigned.operationId,
          items: <OfflineWriteStatus>[
            OfflineWriteStatus(
              recordId: assigned.recordId,
              operationId: assigned.operationId,
              itemIndex: 0,
              state: OfflineWriteState.locallyCommitted,
              attemptCount: completedAttempts,
            ),
          ],
          updatedAt: initial,
        ),
      );
    });
    final remote = FakeOfflineRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore.fromSnapshot(await store.exportSnapshot()),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxAttempts: completedAttempts,
      ),
    );
    addTearDown(repository.dispose);

    expect(await repository.drain('p'), 0);
    expect(remote.vertexPutCalls, 0);
    final deadLetters = await repository.listDeadLetters('p');
    expect(deadLetters.single.attemptCount, completedAttempts);
    expect(deadLetters.single.diagnosticCode, 'max_attempts');
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

  test(
    'mixed expiration aggregate remains deterministic across restart',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final first = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final operation = await first.putVertices(
        partitionId: 'p',
        operationId: 'mixed-expiration',
        inputs: <VertexInput>[
          VertexInput(
            key: 'short',
            value: VertexValue.string('short'),
            expiresIn: const Duration(seconds: 1),
          ),
          VertexInput(key: 'live', value: VertexValue.string('live')),
        ],
      );
      clock.advance(const Duration(seconds: 1));
      expect(await first.listPending('p'), hasLength(1));
      await first.dispose();

      final restoredStore = InMemoryOfflineStore.fromSnapshot(
        await store.exportSnapshot(),
      );
      final restarted = OfflineLanternRepository(
        store: restoredStore,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      addTearDown(restarted.dispose);
      final before = await restarted.getWriteStatus('p', operation.operationId);
      expect(before!.items.map((item) => item.state), <OfflineWriteState>[
        OfflineWriteState.expired,
        OfflineWriteState.locallyCommitted,
      ]);
      expect(await restarted.drain('p'), 1);
      final after = await restarted.getWriteStatus('p', operation.operationId);
      expect(after!.isTerminal, isTrue);
      expect(after.expiredCount, 1);
      expect(after.confirmedCount, 1);
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
    final store = _InspectingStore(InMemoryOfflineStore());
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
    addTearDown(repository.dispose);
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
    final renewalsAtCompletion = store.leaseRenewals;
    clock.advance(const Duration(milliseconds: 40));
    await Future<void>.delayed(const Duration(milliseconds: 25));
    expect(
      store.leaseRenewals,
      renewalsAtCompletion,
      reason: 'a terminal send must cancel its lease-renewal Timer',
    );
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

  test(
    'dispose rejects an enqueue queued behind the store atomically',
    () async {
      final clock = MutableClock(initial);
      final store = InMemoryOfflineStore();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );
      final blockerStarted = Completer<void>();
      final releaseBlocker = Completer<void>();
      final blocker = store.transaction<void>((_) async {
        blockerStarted.complete();
        await releaseBlocker.future;
      });
      await blockerStarted.future;
      final pending = repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'disposed-enqueue', value: VertexValue.nil()),
      );

      final disposing = repository.dispose();
      releaseBlocker.complete();
      await blocker;
      await disposing;
      await expectLater(pending, throwsA(isA<OfflineDisposedException>()));
      expect(
        await store.transaction((transaction) => transaction.outbox('p')),
        isEmpty,
      );
      expect(
        await store.transaction((transaction) => transaction.operations('p')),
        isEmpty,
      );
    },
  );

  test('dispose triggered by an enqueue commit returns its handle', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    final disposalStarted = Completer<Future<void>>();
    final changes = store.changes('p').listen((_) {
      if (!disposalStarted.isCompleted) {
        disposalStarted.complete(repository.dispose());
      }
    });
    addTearDown(changes.cancel);

    final handle = await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(
        key: 'committed-before-dispose',
        value: VertexValue.nil(),
      ),
    );
    await (await disposalStarted.future);

    final records = await store.transaction(
      (transaction) => transaction.outbox('p'),
    );
    expect(records.single.recordId, handle.recordId);
    expect(
      (await handle.statuses.toList()).single.state,
      OfflineWriteState.locallyCommitted,
    );
    expect(
      () => repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'after-dispose', value: VertexValue.nil()),
      ),
      throwsA(isA<OfflineDisposedException>()),
    );
  });
}

Future<void> _seedDeadLetter(
  OfflineStore store, {
  required String partitionId,
  required String recordId,
  required String operationId,
  required String key,
  required DateTime now,
}) => store.transaction((transaction) {
  final generation = transaction.generation(partitionId);
  final record = transaction.enqueue(
    OfflineOutboxRecord(
      recordId: recordId,
      operationId: operationId,
      itemIndex: 0,
      partitionId: partitionId,
      intent: OfflinePutVertexIntent(
        Vertex(key: key, value: VertexValue.string(key), expiration: null),
      ),
      enqueuedAt: now,
      ordinal: 0,
      state: OfflineOutboxState.deadLetter,
      attemptCount: 1,
      generation: generation,
      deadLetteredAt: now,
      diagnosticCode: 'invalid_argument',
    ),
  );
  transaction.putOperation(
    OfflineOperationRecord(
      partitionId: partitionId,
      generation: generation,
      operationId: operationId,
      items: <OfflineWriteStatus>[
        OfflineWriteStatus(
          recordId: record.recordId,
          operationId: operationId,
          itemIndex: 0,
          state: OfflineWriteState.deadLetter,
          attemptCount: 1,
          diagnosticCode: 'invalid_argument',
        ),
      ],
      updatedAt: now,
      terminalAt: now,
    ),
  );
});

final class _GateNextTransactionStore implements OfflineStore {
  final InMemoryOfflineStore _delegate = InMemoryOfflineStore();
  Completer<void>? _nextGate;
  Completer<void>? _activeGate;
  Completer<void>? _blocked;

  Future<void> get blocked =>
      _blocked?.future ??
      Future<void>.error(StateError('no transaction is held'));

  void holdNext() {
    if (_nextGate != null || _activeGate != null) {
      throw StateError('a transaction is already held');
    }
    _nextGate = Completer<void>();
    _blocked = Completer<void>();
  }

  void release() {
    final gate = _activeGate;
    if (gate == null) throw StateError('no transaction is active');
    if (!gate.isCompleted) gate.complete();
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      _delegate.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) async {
    final gate = _nextGate;
    if (gate != null) {
      _nextGate = null;
      _activeGate = gate;
      final blocked = _blocked!;
      if (!blocked.isCompleted) blocked.complete();
      await gate.future;
      if (identical(_activeGate, gate)) _activeGate = null;
    }
    return _delegate.transaction(action);
  }
}

final class _ThrowingDiagnostics implements OfflineDiagnostics {
  const _ThrowingDiagnostics();

  @override
  void record(OfflineDiagnosticEvent event) {
    throw StateError('diagnostics unavailable');
  }
}

final class _ConcurrentRemote extends FakeOfflineRemote {
  final Completer<void> twoStarted = Completer<void>();
  final Completer<void> _release = Completer<void>();
  final Map<String, int> _activeByPartition = <String, int>{};
  final Map<String, int> maxActiveByPartition = <String, int>{};
  var active = 0;
  var maxActive = 0;

  void release() {
    if (!_release.isCompleted) _release.complete();
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    final partition = vertex.key.split('-').first;
    active += 1;
    _activeByPartition[partition] = (_activeByPartition[partition] ?? 0) + 1;
    maxActive = active > maxActive ? active : maxActive;
    final partitionActive = _activeByPartition[partition]!;
    final previous = maxActiveByPartition[partition] ?? 0;
    if (partitionActive > previous) {
      maxActiveByPartition[partition] = partitionActive;
    }
    if (active == 2 && !twoStarted.isCompleted) twoStarted.complete();
    final canceled = Completer<void>();
    final removeCancellation = cancellation?.listen((_) {
      if (!canceled.isCompleted) {
        canceled.completeError(failure(OfflineRemoteErrorKind.canceled));
      }
    });
    try {
      await Future.any<void>(<Future<void>>[_release.future, canceled.future]);
      vertices[vertex.key] = vertex;
      return PutOutcome.appliedAndLive;
    } finally {
      removeCancellation?.call();
      active -= 1;
      _activeByPartition[partition] = partitionActive - 1;
    }
  }
}

final class _CredentialRecordingRemote extends FakeOfflineRemote {
  _CredentialRecordingRemote(this.credentialProvider);

  final String Function() credentialProvider;
  final Completer<void> started = Completer<void>();
  final Completer<void> _release = Completer<void>();
  final Map<String, String> credentialsByKey = <String, String>{};

  Iterable<String> get credentialsForOldKeys => credentialsByKey.entries
      .where((entry) => entry.key.startsWith('old-'))
      .map((entry) => entry.value);

  void release() {
    if (!_release.isCompleted) _release.complete();
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    credentialsByKey[vertex.key] = credentialProvider();
    if (!started.isCompleted) started.complete();
    if (vertex.key.startsWith('old-')) {
      final canceled = Completer<void>();
      final removeCancellation = cancellation?.listen((_) {
        if (!canceled.isCompleted) {
          canceled.completeError(failure(OfflineRemoteErrorKind.canceled));
        }
      });
      try {
        await Future.any<void>(<Future<void>>[
          _release.future,
          canceled.future,
        ]);
      } finally {
        removeCancellation?.call();
      }
    }
    vertices[vertex.key] = vertex;
    return PutOutcome.appliedAndLive;
  }
}

final class _AuthGateRemote extends FakeOfflineRemote {
  final Completer<void> started = Completer<void>();
  final Completer<void> _release = Completer<void>();

  void releaseUnauthenticated() {
    if (!_release.isCompleted) _release.complete();
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls += 1;
    if (!started.isCompleted) started.complete();
    await _release.future;
    throw failure(OfflineRemoteErrorKind.unauthenticated);
  }
}

final class _FailNextTransactionStore implements OfflineStore {
  final InMemoryOfflineStore _delegate = InMemoryOfflineStore();
  var _failNext = false;

  void failNext() => _failNext = true;

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      _delegate.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) {
    if (_failNext) {
      _failNext = false;
      return Future<T>.error(StateError('wipe failed'));
    }
    return _delegate.transaction(action);
  }
}

final class _DelayedRemote extends FakeOfflineRemote {
  _DelayedRemote(this.completer);

  final Completer<void> completer;
  final Completer<void> started = Completer<void>();

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    if (!started.isCompleted) started.complete();
    final canceled = Completer<void>();
    final removeCancellation = cancellation?.listen((_) {
      if (!canceled.isCompleted) {
        canceled.completeError(failure(OfflineRemoteErrorKind.canceled));
      }
    });
    try {
      await Future.any<void>(<Future<void>>[completer.future, canceled.future]);
      return PutOutcome.appliedAndLive;
    } finally {
      removeCancellation?.call();
    }
  }
}

final class _CancelableRemote extends FakeOfflineRemote {
  final Completer<void> started = Completer<void>();

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    if (!started.isCompleted) started.complete();
    final canceled = Completer<void>();
    final remove = cancellation?.listen((_) => canceled.complete());
    await canceled.future;
    remove?.call();
    throw failure(OfflineRemoteErrorKind.canceled);
  }
}

final class _GatedFailureRemote extends FakeOfflineRemote {
  _GatedFailureRemote([this.kind = OfflineRemoteErrorKind.invalidArgument]);

  final OfflineRemoteErrorKind kind;
  final Completer<void> started = Completer<void>();
  final Completer<void> release = Completer<void>();

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexPutCalls++;
    started.complete();
    await release.future;
    throw failure(kind);
  }
}

final class _TransactionSignalingStore implements OfflineStore {
  _TransactionSignalingStore(this.inner);

  final InMemoryOfflineStore inner;
  Completer<void>? _nextTransaction;

  Future<void> signalNextTransaction() {
    final completer = Completer<void>();
    _nextTransaction = completer;
    return completer.future;
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) {
    _nextTransaction?.complete();
    _nextTransaction = null;
    return inner.transaction(action);
  }
}

final class _InspectingStore implements OfflineStore {
  _InspectingStore(this.inner);

  final InMemoryOfflineStore inner;
  int outboxRecordsInspected = 0;
  int maxOutboxBatchInspected = 0;
  int operationRecordsInspected = 0;
  int leaseRenewals = 0;

  void resetInspection() {
    outboxRecordsInspected = 0;
    maxOutboxBatchInspected = 0;
    operationRecordsInspected = 0;
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) => inner.transaction(
    (transaction) => action(_InspectingTransaction(this, transaction)),
  );
}

final class _InspectingTransaction implements OfflineStoreTransaction {
  const _InspectingTransaction(this.store, this.inner);

  final _InspectingStore store;
  final OfflineStoreTransaction inner;

  @override
  List<OfflineOutboxRecord> dueOutbox(
    String partitionId, {
    String? operationId,
    OfflineEntityKey? key,
    required DateTime now,
    required Duration maxAge,
    required Duration deadLetterRetention,
    required int limit,
  }) {
    final records = inner.dueOutbox(
      partitionId,
      operationId: operationId,
      key: key,
      now: now,
      maxAge: maxAge,
      deadLetterRetention: deadLetterRetention,
      limit: limit,
    );
    store.outboxRecordsInspected += records.length;
    if (records.length > store.maxOutboxBatchInspected) {
      store.maxOutboxBatchInspected = records.length;
    }
    return records;
  }

  @override
  List<OfflineOperationRecord> dueOperations(
    String partitionId, {
    required DateTime now,
    required Duration retention,
    required int limit,
  }) {
    final operations = inner.dueOperations(
      partitionId,
      now: now,
      retention: retention,
      limit: limit,
    );
    store.operationRecordsInspected += operations.length;
    return operations;
  }

  @override
  List<OfflineOutboxRecord> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration maxAge,
    required Duration leaseDuration,
    required int limit,
  }) => inner.claim(
    partitionId,
    owner: owner,
    now: now,
    maxAge: maxAge,
    leaseDuration: leaseDuration,
    limit: limit,
  );

  @override
  void deleteCache(String partitionId, OfflineEntityKey key) =>
      inner.deleteCache(partitionId, key);

  @override
  void deleteOperation(String partitionId, String operationId) =>
      inner.deleteOperation(partitionId, operationId);

  @override
  void deleteOutbox(String partitionId, String recordId) =>
      inner.deleteOutbox(partitionId, recordId);

  @override
  OfflineCacheRecord? getCache(String partitionId, OfflineEntityKey key) =>
      inner.getCache(partitionId, key);

  @override
  OfflineOperationRecord? getOperation(
    String partitionId,
    String operationId,
  ) => inner.getOperation(partitionId, operationId);

  @override
  OfflineOutboxRecord? getOutbox(String partitionId, String recordId) =>
      inner.getOutbox(partitionId, recordId);

  @override
  int generation(String partitionId) => inner.generation(partitionId);

  @override
  bool replayPausedForAuth(String partitionId) =>
      inner.replayPausedForAuth(partitionId);

  @override
  void setReplayPausedForAuth(String partitionId, bool paused) =>
      inner.setReplayPausedForAuth(partitionId, paused);

  @override
  bool hasOutboxForOperation(String partitionId, String operationId) =>
      inner.hasOutboxForOperation(partitionId, operationId);

  @override
  OfflineOutboxRecord enqueue(OfflineOutboxRecord record) =>
      inner.enqueue(record);

  @override
  List<OfflineOutboxRecord> enqueueAll(List<OfflineOutboxRecord> records) =>
      inner.enqueueAll(records);

  @override
  List<OfflineOperationRecord> operations(String partitionId) =>
      inner.operations(partitionId);

  @override
  List<OfflineOutboxRecord> outbox(String partitionId) =>
      inner.outbox(partitionId);

  @override
  List<OfflineOutboxRecord> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  ) => inner.outboxForKey(partitionId, key);

  @override
  void putCache(String partitionId, OfflineCacheRecord record) =>
      inner.putCache(partitionId, record);

  @override
  void putOperation(OfflineOperationRecord record) =>
      inner.putOperation(record);

  @override
  bool renewLease(
    String partitionId,
    String recordId, {
    required String owner,
    required int generation,
    required DateTime now,
    required Duration leaseDuration,
  }) {
    store.leaseRenewals += 1;
    return inner.renewLease(
      partitionId,
      recordId,
      owner: owner,
      generation: generation,
      now: now,
      leaseDuration: leaseDuration,
    );
  }

  @override
  OfflineOperationScanPage scanOperations(
    String partitionId, {
    String? afterOperationId,
    required int limit,
  }) => inner.scanOperations(
    partitionId,
    afterOperationId: afterOperationId,
    limit: limit,
  );

  @override
  OfflineOutboxScanPage scanOutbox(
    String partitionId, {
    OfflineOutboxCursor? after,
    String? operationId,
    OfflineEntityKey? key,
    required int limit,
  }) => inner.scanOutbox(
    partitionId,
    after: after,
    operationId: operationId,
    key: key,
    limit: limit,
  );

  @override
  void touchCache(
    String partitionId,
    OfflineEntityKey key,
    DateTime accessedAt,
  ) => inner.touchCache(partitionId, key, accessedAt);

  @override
  void updateOutbox(OfflineOutboxRecord record) => inner.updateOutbox(record);

  @override
  void wipePartition(String partitionId) => inner.wipePartition(partitionId);
}
