import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

void main() {
  final initial = DateTime.utc(2026, 7, 22, 12);

  test(
    'cache policies distinguish Missing, Unknown, stale, and expired',
    () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote();
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: testConfig(clock),
      );

      expect(
        (await repository.readVertex(
          'p',
          'missing',
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
      final missing = await repository.readVertex('p', 'missing');
      expect(missing.state, OfflineReadState.missing);
      expect(missing.source, OfflineReadSource.server);

      remote.vertexGetFailures.add(failure(OfflineRemoteErrorKind.unavailable));
      final fallback = await repository.readVertex(
        'p',
        'missing',
        policy: OfflineReadPolicy.serverFirst,
      );
      expect(fallback.state, OfflineReadState.missing);
      remote.vertexGetFailures.add(failure(OfflineRemoteErrorKind.unavailable));
      final unknown = await repository.readVertex(
        'p',
        'unknown',
        policy: OfflineReadPolicy.serverOnly,
      );
      expect(unknown.state, OfflineReadState.unknown);

      remote.vertices['ttl'] = Vertex(
        key: 'ttl',
        value: VertexValue.string('short'),
        expiration: initial.add(const Duration(seconds: 1)),
      );
      expect(
        (await repository.readVertex('p', 'ttl')).state,
        OfflineReadState.fresh,
      );
      clock.advance(const Duration(seconds: 1));
      final expired = await repository.readVertex(
        'p',
        'ttl',
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(expired.state, OfflineReadState.expired);
      expect(expired.value, isNull);
    },
  );

  test('negative-cache deadline saturates inside durable time range', () async {
    final nearMaximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 998);
    final maximum = DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 999);
    final clock = MutableClock(nearMaximum);
    final store = InMemoryOfflineStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
      ),
    );
    addTearDown(repository.dispose);

    expect(
      (await repository.readVertex('p', 'missing')).state,
      OfflineReadState.missing,
    );
    final cached = await store.transaction(
      (transaction) =>
          transaction.getCache('p', const OfflineEntityKey.vertex('missing')),
    );
    expect(cached!.missingUntil, maximum);
    final snapshot = await store.exportSnapshot();
    expect(() => InMemoryOfflineStore.fromSnapshot(snapshot), returnsNormally);
  });

  test(
    'stale values require explicit stale allowance and watch is key scoped',
    () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote()
        ..vertices['v'] = Vertex(
          key: 'v',
          value: VertexValue.string('v'),
          expiration: null,
        );
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxCacheAge: const Duration(seconds: 1),
        ),
      );
      await repository.readVertex('p', 'v');
      clock.advance(const Duration(seconds: 2));
      remote.vertexGetFailures.add(failure(OfflineRemoteErrorKind.unavailable));
      expect(
        (await repository.readVertex(
          'p',
          'v',
          policy: OfflineReadPolicy.cacheFirst,
        )).state,
        OfflineReadState.unknown,
      );
      final stale = await repository.readVertex(
        'p',
        'v',
        policy: OfflineReadPolicy.cacheOnly,
        allowStale: true,
      );
      expect(stale.state, OfflineReadState.stale);
    },
  );

  test(
    'cache read sweeps max-age work before constructing its overlay',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxAge: const Duration(seconds: 1),
        ),
      );
      addTearDown(repository.dispose);
      final write = await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(
          key: 'max-age-overlay',
          value: VertexValue.string('pending'),
          expiresIn: const Duration(hours: 1),
        ),
      );
      clock.advance(const Duration(seconds: 1));

      final snapshot = await repository.readVertex(
        'p',
        'max-age-overlay',
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(snapshot.value, isNull);
      expect(snapshot.hasPendingWrites, isFalse);
      expect(
        (await repository.getWriteStatus(
          'p',
          write.operationId,
        ))!.items.single.state,
        OfflineWriteState.deadLetter,
      );
    },
  );

  test('cache-first watch emits cache before remote revalidation', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote()
      ..vertices['v'] = Vertex(
        key: 'v',
        value: VertexValue.string('cached'),
        expiration: null,
      );
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    await repository.readVertex('p', 'v');
    remote.vertices['v'] = Vertex(
      key: 'v',
      value: VertexValue.string('server'),
      expiration: null,
    );

    final snapshots = await repository.watchVertex('p', 'v').take(2).toList();
    expect((snapshots.first.value!.value as StringValue).value, 'cached');
    expect(snapshots.first.source, OfflineReadSource.cache);
    expect((snapshots.last.value!.value as StringValue).value, 'server');
    expect(snapshots.last.source, OfflineReadSource.server);
  });

  test(
    'cache-first watch survives failed revalidation and local writes',
    () async {
      final clock = MutableClock(initial);
      final remote = FakeOfflineRemote()
        ..vertices['v'] = Vertex(
          key: 'v',
          value: VertexValue.string('cached'),
          expiration: null,
        );
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: testConfig(clock),
      );
      await repository.readVertex('p', 'v');
      remote.vertexGetFailures.add(failure(OfflineRemoteErrorKind.unavailable));
      final pendingArrived = Completer<OfflineSnapshot<Vertex>>();
      final subscription = repository.watchVertex('p', 'v').listen((snapshot) {
        if (snapshot.hasPendingWrites && !pendingArrived.isCompleted) {
          pendingArrived.complete(snapshot);
        }
      }, onError: (Object _) {});
      await Future<void>.delayed(const Duration(milliseconds: 10));
      expect(remote.vertexGetFailures, isEmpty);
      await repository.putVertex(
        partitionId: 'p',
        input: VertexInput(key: 'v', value: VertexValue.string('local')),
      );
      final pending = await pendingArrived.future.timeout(
        const Duration(seconds: 1),
        onTimeout: () => throw StateError('local write was not emitted'),
      );

      expect((pending.value!.value as StringValue).value, 'local');
      await subscription.cancel().timeout(
        const Duration(seconds: 1),
        onTimeout: () => throw StateError('watch cancellation did not finish'),
      );
    },
  );

  test('watch coalesces unrelated partition notifications', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    final snapshots = <OfflineSnapshot<Vertex>>[];
    final subscription = repository
        .watchVertex('p', 'watched', initialPolicy: OfflineReadPolicy.cacheOnly)
        .listen(snapshots.add);
    await Future<void>.delayed(Duration.zero);
    await store.transaction((transaction) {
      transaction.putCache(
        'p',
        OfflineCacheRecord.value(
          partitionId: 'p',
          generation: 0,
          key: const OfflineEntityKey.vertex('other'),
          entity: Vertex(
            key: 'other',
            value: VertexValue.string('other'),
            expiration: null,
          ),
          validatedAt: initial,
          lastAccessAt: initial,
        ),
      );
    });
    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(snapshots, hasLength(1));
    await subscription.cancel();
  });

  test(
    'watch observes a mutation committed during its initial snapshot',
    () async {
      final clock = MutableClock(initial);
      final store = _GapInjectingStore(
        injectedAt: initial,
        injected: Vertex(
          key: 'watched',
          value: VertexValue.string('between'),
          expiration: null,
        ),
      )..arm();
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: testConfig(clock),
      );

      final snapshots = await repository
          .watchVertex(
            'p',
            'watched',
            initialPolicy: OfflineReadPolicy.cacheOnly,
          )
          .take(2)
          .toList()
          .timeout(const Duration(seconds: 1));

      expect(snapshots.first.state, OfflineReadState.unknown);
      expect((snapshots.last.value!.value as StringValue).value, 'between');
    },
  );

  test('late remote read cannot repopulate a wiped partition', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final remote = _DelayedReadRemote();
    final repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    final reading = repository.readVertex(
      'p',
      'late',
      policy: OfflineReadPolicy.serverOnly,
    );
    await remote.started.future;
    await repository.wipePartition('p');
    remote.complete(
      Vertex(key: 'late', value: VertexValue.string('late'), expiration: null),
    );
    final snapshot = await reading;
    expect(snapshot.state, OfflineReadState.unknown);
    expect(
      await store.transaction(
        (transaction) =>
            transaction.getCache('p', const OfflineEntityKey.vertex('late')),
      ),
      isNull,
    );
  });

  test('single-flight identities cannot collide on delimiters', () async {
    final clock = MutableClock(initial);
    final remote = FakeOfflineRemote()
      ..vertices['c'] = Vertex(
        key: 'c',
        value: VertexValue.string('first'),
        expiration: null,
      )
      ..vertices['b:c'] = Vertex(
        key: 'b:c',
        value: VertexValue.string('second'),
        expiration: null,
      );
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    final snapshots = await Future.wait(<Future<OfflineSnapshot<Vertex>>>[
      repository.readVertex('a:b', 'c', policy: OfflineReadPolicy.serverOnly),
      repository.readVertex('a', 'b:c', policy: OfflineReadPolicy.serverOnly),
    ]);
    expect(
      snapshots.map((snapshot) => (snapshot.value!.value as StringValue).value),
      <String>['first', 'second'],
    );
  });

  test('canceling the first same-key caller leaves the second alive', () async {
    final clock = MutableClock(initial);
    final remote = _ControlledReadRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    final firstCancellation = LanternCancellationToken();
    final secondCancellation = LanternCancellationToken();
    final first = repository.readVertex(
      'p',
      'shared',
      policy: OfflineReadPolicy.serverOnly,
      cancellation: firstCancellation,
    );
    await remote.waitUntilStarted('shared');
    final second = repository.readVertex(
      'p',
      'shared',
      policy: OfflineReadPolicy.serverOnly,
      cancellation: secondCancellation,
    );

    firstCancellation.cancel();
    await expectLater(first, throwsA(isA<OfflineCanceledException>()));
    expect(remote.started.where((key) => key == 'shared'), hasLength(1));

    remote.complete('shared');
    expect((await second).state, OfflineReadState.missing);
    expect(remote.started.where((key) => key == 'shared'), hasLength(1));
  });

  test('canceling the second same-key caller leaves the first alive', () async {
    final clock = MutableClock(initial);
    final remote = _ControlledReadRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: testConfig(clock),
    );
    final firstCancellation = LanternCancellationToken();
    final secondCancellation = LanternCancellationToken();
    final first = repository.readVertex(
      'p',
      'shared',
      policy: OfflineReadPolicy.serverOnly,
      cancellation: firstCancellation,
    );
    await remote.waitUntilStarted('shared');
    final second = repository.readVertex(
      'p',
      'shared',
      policy: OfflineReadPolicy.serverOnly,
      cancellation: secondCancellation,
    );

    secondCancellation.cancel();
    await expectLater(second, throwsA(isA<OfflineCanceledException>()));
    expect(remote.started.where((key) => key == 'shared'), hasLength(1));

    remote.complete('shared');
    expect((await first).state, OfflineReadState.missing);
    expect(remote.started.where((key) => key == 'shared'), hasLength(1));
  });

  test('the final waiter cancels transport and releases its permit', () async {
    final clock = MutableClock(initial);
    final remote = _CancelableReadRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
      config: OfflineConfig(
        clock: clock.call,
        idGenerator: testConfig(clock).idGenerator,
        jitter: (_) => Duration.zero,
        maxReadConcurrency: 1,
        maxReadConcurrencyPerPartition: 1,
      ),
    );
    final cancellation = LanternCancellationToken();
    final canceled = repository.readVertex(
      'p',
      'first',
      policy: OfflineReadPolicy.serverOnly,
      cancellation: cancellation,
    );
    await remote.waitUntilStarted('first');

    cancellation.cancel();
    await expectLater(canceled, throwsA(isA<OfflineCanceledException>()));
    await remote.waitUntilCanceled('first');

    final next = repository.readVertex(
      'p',
      'next',
      policy: OfflineReadPolicy.serverOnly,
    );
    await remote.waitUntilStarted('next');
    remote.complete('next');
    expect((await next).state, OfflineReadState.missing);
    expect(remote.canceled.where((key) => key == 'first'), hasLength(1));
  });

  test(
    'dispose fails a deferred caller even when the prior flight never settles',
    () async {
      final clock = MutableClock(initial);
      final remote = _CancellationIgnoringReadRemote();
      addTearDown(remote.release);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: testConfig(clock),
      );
      final cancellation = LanternCancellationToken();
      final first = repository.readVertex(
        'p',
        'shared',
        policy: OfflineReadPolicy.serverOnly,
        cancellation: cancellation,
      );
      await remote.started.future;

      cancellation.cancel();
      await expectLater(first, throwsA(isA<OfflineCanceledException>()));
      await remote.cancellationObserved.future;

      final deferred = repository.readVertex(
        'p',
        'shared',
        policy: OfflineReadPolicy.serverOnly,
      );
      final deferredExpectation = expectLater(
        deferred.timeout(const Duration(seconds: 1)),
        throwsA(isA<OfflineDisposedException>()),
      );

      await repository.dispose().timeout(const Duration(seconds: 1));
      await deferredExpectation;
      expect(remote.startCount, 1);

      remote.release();
      await Future<void>.delayed(Duration.zero);
      expect(remote.startCount, 1);
    },
  );

  test(
    'remote reads honor global, partition, queue, and cancellation bounds',
    () async {
      final clock = MutableClock(initial);
      final remote = _ControlledReadRemote();
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxReadConcurrency: 2,
          maxReadConcurrencyPerPartition: 1,
          maxQueuedReads: 1,
          maxQueuedReadsPerPartition: 1,
        ),
      );
      final preCanceled = LanternCancellationToken()..cancel();
      await expectLater(
        repository.readVertex(
          'p',
          'pre-canceled',
          policy: OfflineReadPolicy.serverOnly,
          cancellation: preCanceled,
        ),
        throwsA(isA<OfflineCanceledException>()),
      );
      expect(remote.started, isNot(contains('pre-canceled')));

      final first = repository.readVertex(
        'p',
        'first',
        policy: OfflineReadPolicy.serverOnly,
      );
      await remote.waitUntilStarted('first');
      final queued = repository.readVertex(
        'p',
        'queued',
        policy: OfflineReadPolicy.serverOnly,
      );
      final otherPartition = repository.readVertex(
        'q',
        'other',
        policy: OfflineReadPolicy.serverOnly,
      );
      await remote.waitUntilStarted('other');
      await expectLater(
        repository.readVertex(
          'p',
          'rejected',
          policy: OfflineReadPolicy.serverOnly,
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(remote.maximumActive, 2);

      remote.complete('first');
      await remote.waitUntilStarted('queued');
      remote.complete('queued');
      remote.complete('other');
      await Future.wait(<Future<Object?>>[first, queued, otherPartition]);

      final blocked = repository.readVertex(
        'p',
        'blocked',
        policy: OfflineReadPolicy.serverOnly,
      );
      await remote.waitUntilStarted('blocked');
      final cancellation = LanternCancellationToken();
      final canceled = repository.readVertex(
        'p',
        'canceled',
        policy: OfflineReadPolicy.serverOnly,
        cancellation: cancellation,
      );
      cancellation.cancel();
      await expectLater(
        canceled.timeout(const Duration(seconds: 1)),
        throwsA(isA<OfflineCanceledException>()),
      );
      remote.complete('blocked');
      await blocked;
      expect(remote.started, isNot(contains('canceled')));
    },
  );

  test(
    'watchers enforce repository, partition, and active-partition bounds',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWatchers: 2,
          maxWatchersPerPartition: 1,
          maxActiveWatcherPartitions: 1,
        ),
      );
      final first = repository
          .watchVertex('p', 'a', initialPolicy: OfflineReadPolicy.cacheOnly)
          .listen((_) {});
      await Future<void>.delayed(Duration.zero);
      await expectLater(
        repository
            .watchVertex('p', 'b', initialPolicy: OfflineReadPolicy.cacheOnly)
            .first,
        throwsA(isA<OfflineCapacityException>()),
      );
      await expectLater(
        repository
            .watchVertex('q', 'a', initialPolicy: OfflineReadPolicy.cacheOnly)
            .first,
        throwsA(isA<OfflineCapacityException>()),
      );
      await first.cancel();
      expect(
        await repository
            .watchVertex('q', 'a', initialPolicy: OfflineReadPolicy.cacheOnly)
            .first,
        isA<OfflineSnapshot<Vertex>>(),
      );
    },
  );

  test('pause, resume, and final cancel release the store listener', () async {
    final clock = MutableClock(initial);
    final store = _TrackingStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    final initialSnapshot = Completer<void>();
    final changedSnapshot = Completer<OfflineSnapshot<Vertex>>();
    final subscription = repository
        .watchVertex('p', 'watched', initialPolicy: OfflineReadPolicy.cacheOnly)
        .listen((snapshot) {
          if (!initialSnapshot.isCompleted) {
            initialSnapshot.complete();
          } else if (snapshot.hasPendingWrites &&
              !changedSnapshot.isCompleted) {
            changedSnapshot.complete(snapshot);
          }
        });
    await initialSnapshot.future.timeout(const Duration(seconds: 1));
    expect(store.activeChangeSubscriptions, 1);

    subscription.pause();
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'watched', value: VertexValue.string('changed')),
    );
    await Future<void>.delayed(Duration.zero);
    expect(changedSnapshot.isCompleted, isFalse);

    subscription.resume();
    expect(
      (await changedSnapshot.future.timeout(
        const Duration(seconds: 1),
      )).hasPendingWrites,
      isTrue,
    );
    await subscription.cancel();
    expect(store.activeChangeSubscriptions, 0);
  });

  test(
    'delayed failing store cancellation releases watcher capacity first',
    () async {
      final clock = MutableClock(initial);
      final store = _ControlledCancelStore();
      addTearDown(store.releaseFirstCancellation);
      final repository = OfflineLanternRepository(
        store: store,
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWatchers: 1,
          maxWatchersPerPartition: 1,
          maxActiveWatcherPartitions: 1,
        ),
      );
      final firstInitial = Completer<void>();
      final first = repository
          .watchVertex('p', 'first', initialPolicy: OfflineReadPolicy.cacheOnly)
          .listen((_) => firstInitial.complete());
      await firstInitial.future.timeout(const Duration(seconds: 1));

      final firstCancellation = expectLater(
        first.cancel(),
        throwsA(isA<StateError>()),
      );
      await store.firstCancellationStarted.future.timeout(
        const Duration(seconds: 1),
      );

      final secondInitial = Completer<void>();
      final second = repository
          .watchVertex(
            'p',
            'second',
            initialPolicy: OfflineReadPolicy.cacheOnly,
          )
          .listen((_) => secondInitial.complete());
      await secondInitial.future.timeout(const Duration(seconds: 1));

      store.releaseFirstCancellation();
      await firstCancellation;
      await second.cancel();
      await repository.dispose();
    },
  );

  test(
    'idle watch cancellation closes and releases watcher capacity',
    () async {
      final clock = MutableClock(initial);
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: FakeOfflineRemote(),
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          maxWatchers: 1,
          maxWatchersPerPartition: 1,
          maxActiveWatcherPartitions: 1,
        ),
      );
      final errors = <Object>[];
      for (var index = 0; index < 3; index++) {
        final cancellation = LanternCancellationToken();
        final initialSnapshot = Completer<void>();
        final done = Completer<void>();
        repository
            .watchVertex(
              'p',
              'key-$index',
              initialPolicy: OfflineReadPolicy.cacheOnly,
              cancellation: cancellation,
            )
            .listen(
              (_) {
                if (!initialSnapshot.isCompleted) initialSnapshot.complete();
              },
              onError: errors.add,
              onDone: () => done.complete(),
            );
        await initialSnapshot.future.timeout(const Duration(seconds: 1));

        cancellation.cancel();
        await done.future.timeout(const Duration(seconds: 1));
      }

      expect(errors, isEmpty);
    },
  );

  test(
    'an oversized confirmed read remains usable and emits capacity diagnostics',
    () async {
      final clock = MutableClock(initial);
      final diagnostics = _RecordingDiagnostics();
      final remote = FakeOfflineRemote()
        ..vertices['large'] = Vertex(
          key: 'large',
          value: VertexValue.string('value'),
          expiration: null,
        );
      final store = InMemoryOfflineStore(
        limits: const OfflineStoreLimits(
          maxCacheBytes: 1,
          maxCacheBytesPerPartition: 1,
        ),
      );
      final repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: OfflineConfig(
          clock: clock.call,
          idGenerator: testConfig(clock).idGenerator,
          jitter: (_) => Duration.zero,
          diagnostics: diagnostics,
        ),
      );

      final snapshot = await repository.readVertex(
        'p',
        'large',
        policy: OfflineReadPolicy.serverOnly,
      );
      expect(snapshot.state, OfflineReadState.fresh);
      expect(snapshot.source, OfflineReadSource.server);
      expect(
        diagnostics.events.map((event) => event.kind),
        contains(OfflineDiagnosticKind.capacityRejected),
      );
      expect(
        await store.transaction(
          (transaction) =>
              transaction.getCache('p', const OfflineEntityKey.vertex('large')),
        ),
        isNull,
      );
    },
  );
}

final class _DelayedReadRemote extends FakeOfflineRemote {
  final Completer<void> started = Completer<void>();
  final Completer<Vertex> _response = Completer<Vertex>();

  void complete(Vertex vertex) => _response.complete(vertex);

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    if (!started.isCompleted) started.complete();
    return OfflineRemotePresent<Vertex>(await _response.future);
  }
}

final class _ControlledReadRemote extends FakeOfflineRemote {
  final Map<String, Completer<void>> _gates = <String, Completer<void>>{};
  final Map<String, Completer<void>> _startedSignals =
      <String, Completer<void>>{};
  final List<String> started = <String>[];
  var _active = 0;
  var maximumActive = 0;

  Future<void> waitUntilStarted(String key) =>
      _startedSignals.putIfAbsent(key, Completer<void>.new).future;

  void complete(String key) =>
      _gates.putIfAbsent(key, Completer<void>.new).complete();

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    started.add(key);
    _active += 1;
    if (_active > maximumActive) maximumActive = _active;
    _startedSignals.putIfAbsent(key, Completer<void>.new).complete();
    await _gates.putIfAbsent(key, Completer<void>.new).future;
    _active -= 1;
    return const OfflineRemoteMissing<Vertex>();
  }
}

final class _CancelableReadRemote extends FakeOfflineRemote {
  final Map<String, Completer<OfflineRemoteRead<Vertex>>> _responses =
      <String, Completer<OfflineRemoteRead<Vertex>>>{};
  final Map<String, Completer<void>> _startedSignals =
      <String, Completer<void>>{};
  final Map<String, Completer<void>> _canceledSignals =
      <String, Completer<void>>{};
  final List<String> canceled = <String>[];

  Future<void> waitUntilStarted(String key) =>
      _startedSignals.putIfAbsent(key, Completer<void>.new).future;

  Future<void> waitUntilCanceled(String key) =>
      _canceledSignals.putIfAbsent(key, Completer<void>.new).future;

  void complete(String key) {
    final response = _responses.putIfAbsent(
      key,
      Completer<OfflineRemoteRead<Vertex>>.new,
    );
    if (!response.isCompleted) {
      response.complete(const OfflineRemoteMissing<Vertex>());
    }
  }

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    _startedSignals.putIfAbsent(key, Completer<void>.new).complete();
    final response = _responses.putIfAbsent(
      key,
      Completer<OfflineRemoteRead<Vertex>>.new,
    );
    final removeCancellationListener = cancellation?.listen((_) {
      canceled.add(key);
      _canceledSignals.putIfAbsent(key, Completer<void>.new).complete();
      if (!response.isCompleted) {
        response.completeError(const OfflineCanceledException());
      }
    });
    try {
      return await response.future;
    } finally {
      removeCancellationListener?.call();
    }
  }
}

final class _CancellationIgnoringReadRemote extends FakeOfflineRemote {
  final Completer<void> started = Completer<void>();
  final Completer<void> cancellationObserved = Completer<void>();
  final Completer<OfflineRemoteRead<Vertex>> _response =
      Completer<OfflineRemoteRead<Vertex>>();
  var startCount = 0;

  void release() {
    if (!_response.isCompleted) {
      _response.complete(const OfflineRemoteMissing<Vertex>());
    }
  }

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    startCount += 1;
    if (!started.isCompleted) started.complete();
    final removeCancellationListener = cancellation?.listen((_) {
      if (!cancellationObserved.isCompleted) cancellationObserved.complete();
    });
    try {
      return await _response.future;
    } finally {
      removeCancellationListener?.call();
    }
  }
}

final class _GapInjectingStore implements OfflineStore {
  _GapInjectingStore({required this.injectedAt, required this.injected});

  final InMemoryOfflineStore _delegate = InMemoryOfflineStore();
  final DateTime injectedAt;
  final Vertex injected;
  var _armed = false;
  var _injected = false;
  var _callsAfterArm = 0;

  void arm() => _armed = true;

  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      _delegate.changes(partitionId);

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) async {
    final wasArmed = _armed;
    final result = await _delegate.transaction(action);
    if (wasArmed) _callsAfterArm += 1;
    if (wasArmed && _callsAfterArm == 2 && !_injected) {
      _injected = true;
      unawaited(
        _delegate.transaction((transaction) {
          transaction.putCache(
            'p',
            OfflineCacheRecord.value(
              partitionId: 'p',
              generation: 0,
              key: OfflineEntityKey.vertex(injected.key),
              entity: injected,
              validatedAt: injectedAt,
              lastAccessAt: injectedAt,
            ),
          );
        }),
      );
    }
    return result;
  }
}

final class _TrackingStore implements OfflineStore {
  final InMemoryOfflineStore _delegate = InMemoryOfflineStore();
  var activeChangeSubscriptions = 0;

  @override
  Stream<OfflineStoreChange> changes(String partitionId) {
    StreamSubscription<OfflineStoreChange>? upstream;
    var active = false;
    late final StreamController<OfflineStoreChange> controller;

    Future<void> cancelUpstream() async {
      if (active) {
        active = false;
        activeChangeSubscriptions -= 1;
      }
      await upstream?.cancel();
    }

    controller = StreamController<OfflineStoreChange>(
      sync: true,
      onListen: () {
        active = true;
        activeChangeSubscriptions += 1;
        upstream = _delegate
            .changes(partitionId)
            .listen(
              controller.add,
              onError: controller.addError,
              onDone: controller.close,
            );
      },
      onPause: () => upstream?.pause(),
      onResume: () => upstream?.resume(),
      onCancel: cancelUpstream,
    );
    return controller.stream;
  }

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) => _delegate.transaction(action);
}

final class _ControlledCancelStore implements OfflineStore {
  final InMemoryOfflineStore _delegate = InMemoryOfflineStore();
  final Completer<void> firstCancellationStarted = Completer<void>();
  final Completer<void> _releaseFirstCancellation = Completer<void>();
  var _cancellationCount = 0;

  void releaseFirstCancellation() {
    if (!_releaseFirstCancellation.isCompleted) {
      _releaseFirstCancellation.complete();
    }
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) {
    StreamSubscription<OfflineStoreChange>? upstream;
    late final StreamController<OfflineStoreChange> controller;

    controller = StreamController<OfflineStoreChange>(
      sync: true,
      onListen: () {
        upstream = _delegate
            .changes(partitionId)
            .listen(
              controller.add,
              onError: controller.addError,
              onDone: controller.close,
            );
      },
      onPause: () => upstream?.pause(),
      onResume: () => upstream?.resume(),
      onCancel: () async {
        _cancellationCount += 1;
        if (_cancellationCount == 1) {
          if (!firstCancellationStarted.isCompleted) {
            firstCancellationStarted.complete();
          }
          await _releaseFirstCancellation.future;
          await upstream?.cancel();
          throw StateError('store cancellation failed');
        }
        await upstream?.cancel();
      },
    );
    return controller.stream;
  }

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) => _delegate.transaction(action);
}

final class _RecordingDiagnostics implements OfflineDiagnostics {
  final List<OfflineDiagnosticEvent> events = <OfflineDiagnosticEvent>[];

  @override
  void record(OfflineDiagnosticEvent event) => events.add(event);
}
