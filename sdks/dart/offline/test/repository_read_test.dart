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

  test('watch cancellation is not surfaced as a stream error', () async {
    final clock = MutableClock(initial);
    final store = InMemoryOfflineStore();
    final repository = OfflineLanternRepository(
      store: store,
      remote: FakeOfflineRemote(),
      config: testConfig(clock),
    );
    final cancellation = LanternCancellationToken();
    final errors = <Object>[];
    final subscription = repository
        .watchVertex(
          'p',
          'key',
          initialPolicy: OfflineReadPolicy.cacheOnly,
          cancellation: cancellation,
        )
        .listen((_) {}, onError: errors.add);
    await Future<void>.delayed(Duration.zero);
    cancellation.cancel();
    await repository.putVertex(
      partitionId: 'p',
      input: VertexInput(key: 'key', value: VertexValue.string('value')),
    );
    await Future<void>.delayed(const Duration(milliseconds: 10));

    expect(errors, isEmpty);
    await subscription.cancel();
  });

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

final class _RecordingDiagnostics implements OfflineDiagnostics {
  final List<OfflineDiagnosticEvent> events = <OfflineDiagnosticEvent>[];

  @override
  void record(OfflineDiagnosticEvent event) => events.add(event);
}
