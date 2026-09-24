import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

const _originA = '00000000000000000000000000000001';
const _originB = '00000000000000000000000000000002';
const _originC = '00000000000000000000000000000003';
const _key = OfflineEntityKey.vertex('resident');

void main() {
  late InMemoryOfflineStore store;
  late FakeOfflineRemote remote;
  late OfflineLanternRepository repository;
  late _Source source;
  late MutableClock clock;

  setUp(() {
    store = InMemoryOfflineStore();
    remote = FakeOfflineRemote();
    clock = MutableClock(DateTime.utc(2026, 9, 24));
    repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(clock),
    );
    source = _Source();
  });
  tearDown(() async {
    await repository.dispose();
    await source.closeAll();
  });

  test(
    'bootstrap revalidates resident from the checkpoint responder',
    () async {
      remote.vertices['resident'] = _vertex('old');
      await repository.readVertex('p', 'resident');
      final session = _Session('first')..vertices['resident'] = _vertex('new');
      source.queue(session);
      final cancellation = LanternCancellationToken();
      final consume = repository.consumeIdentityChanges(
        'p',
        source: source,
        cancellation: cancellation,
      );
      await _waitUntil(() => session.listening);
      session.add(OfflineIdentityCheckpoint({_originA: BigInt.zero}));
      await _waitUntil(() => session.vertexReads == 1);
      expect(session.everPaused, isTrue);
      await _waitUntil(
        () async =>
            !await store.transaction((t) => t.hasUnknownResident('p', _key)),
      );
      final snapshot = await repository.readVertex(
        'p',
        'resident',
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect((snapshot.value!.value as StringValue).value, 'new');
      expect(source.opens.single.bootstrap, isTrue);
      expect(source.opens.single.nextExpected, isEmpty);
      cancellation.cancel();
      await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
      expect(session.closed, isTrue);
    },
  );

  test(
    'partial mutation survives snapshot reopen and duplicate chunk zero',
    () async {
      final first = _Session('endpoint-a');
      source.queue(first);
      final cancellation = LanternCancellationToken();
      final consume = repository.consumeIdentityChanges(
        'p',
        source: source,
        cancellation: cancellation,
      );
      await _waitUntil(() => first.listening);
      first.add(OfflineIdentityCheckpoint({_originA: BigInt.zero}));
      first.add(_chunk(0, false));
      await _waitUntil(
        () async => await store.transaction((t) => t.changeEpoch('p')) == 2,
      );
      expect(
        (await store.transaction(
          (t) => t.changeCursor('p'),
        )).sequences[_originA],
        BigInt.zero,
      );
      cancellation.cancel();
      await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
      final snapshot = await store.exportSnapshot();
      await repository.dispose();
      store = InMemoryOfflineStore.fromSnapshot(snapshot);
      repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(clock),
      );
      source = _Source();
      final second = _Session('endpoint-b');
      source.queue(second);
      final again = LanternCancellationToken();
      final resumed = repository.consumeIdentityChanges(
        'p',
        source: source,
        cancellation: again,
      );
      await _waitUntil(() => second.listening);
      expect(source.opens.single.bootstrap, isFalse);
      expect(source.opens.single.nextExpected[_originA], BigInt.one);
      second.add(_chunk(0, false));
      second.add(_chunk(1, true));
      await _waitUntil(
        () async =>
            (await store.transaction(
              (t) => t.changeCursor('p'),
            )).sequences[_originA] ==
            BigInt.one,
      );
      again.cancel();
      await expectLater(resumed, throwsA(isA<OfflineCanceledException>()));
    },
  );

  test(
    'missing chunk resets Unknown and repeats checkpoint recovery',
    () async {
      remote.vertices['resident'] = _vertex('old');
      await repository.readVertex('p', 'resident');
      final first = _Session('one');
      final second = _Session('two')..vertices['resident'] = _vertex('new');
      source
        ..queue(first)
        ..queue(second);
      final cancellation = LanternCancellationToken();
      final consume = repository.consumeIdentityChanges(
        'p',
        source: source,
        cancellation: cancellation,
      );
      await _waitUntil(() => first.listening);
      first.add(OfflineIdentityCheckpoint({_originA: BigInt.zero}));
      await _waitUntil(() => first.vertexReads == 1);
      first.add(_chunk(0, false, key: const OfflineEntityKey.vertex('other')));
      first.add(_chunk(2, true, key: const OfflineEntityKey.vertex('other')));
      await _waitUntil(() => second.listening);
      expect(first.closed, isTrue);
      expect(source.opens.map((open) => open.bootstrap), [true, true]);
      expect(
        (await repository.readVertex(
          'p',
          'resident',
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
      second.add(OfflineIdentityCheckpoint({_originA: BigInt.one}));
      await _waitUntil(() => second.vertexReads == 1);
      await _waitUntil(
        () async =>
            !await store.transaction((t) => t.hasUnknownResident('p', _key)),
      );
      expect(
        (await repository.readVertex(
          'p',
          'resident',
          policy: OfflineReadPolicy.cacheOnly,
        )).value!.value,
        isA<StringValue>(),
      );
      cancellation.cancel();
      await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
    },
  );

  test('changed responder during plural recovery remains Unknown', () async {
    remote.vertices['resident'] = _vertex('old');
    await repository.readVertex('p', 'resident');
    final commits = <OfflineStoreChange>[];
    final changes = store.changes('p').listen(commits.add);
    final session = _Session('one')..vertices['resident'] = _vertex('new');
    session.afterVertexRead = () => session.responderId = 'changed';
    source
      ..queue(session)
      ..queue(_Session('two'));
    final cancellation = LanternCancellationToken();
    final consume = repository.consumeIdentityChanges(
      'p',
      source: source,
      cancellation: cancellation,
    );
    await _waitUntil(() => session.listening);
    session.add(OfflineIdentityCheckpoint({_originA: BigInt.zero}));
    await _waitUntil(() => source.opens.length == 2);
    await Future<void>.delayed(Duration.zero);
    // Checkpoint reset and fail-closed reset commit; no changed-responder
    // plural result is ever published as a confirmed cache row in between.
    expect(commits.length, 2);
    expect(
      await store.transaction((t) => t.hasUnknownResident('p', _key)),
      isTrue,
    );
    cancellation.cancel();
    await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
    await changes.cancel();
  });

  test(
    'three origins are contiguous and duplicate final is idempotent',
    () async {
      final session = _Session('endpoint');
      source.queue(session);
      final cancellation = LanternCancellationToken();
      final consume = repository.consumeIdentityChanges(
        'p',
        source: source,
        cancellation: cancellation,
      );
      await _waitUntil(() => session.listening);
      session.add(OfflineIdentityCheckpoint(const {}));
      for (final origin in [_originA, _originB, _originC]) {
        session.add(_chunk(0, true, origin: origin));
        session.add(_chunk(0, true, origin: origin));
      }
      await _waitUntil(
        () async =>
            (await store.transaction(
              (t) => t.changeCursor('p'),
            )).sequences.length ==
            3,
      );
      final cursor = await store.transaction((t) => t.changeCursor('p'));
      expect(cursor.sequences.values, everyElement(BigInt.one));
      cancellation.cancel();
      await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
    },
  );

  test('wipe cancels stream and rejects a second active session', () async {
    final session = _Session('endpoint');
    source.queue(session);
    final consume = repository.consumeIdentityChanges('p', source: source);
    final canceled = expectLater(
      consume,
      throwsA(isA<OfflineCanceledException>()),
    );
    await _waitUntil(() => session.listening);
    expect(
      () => repository.consumeIdentityChanges('p', source: source),
      throwsA(isA<OfflineCapacityException>()),
    );
    await repository.wipePartition('p');
    await canceled;
    expect(session.closed, isTrue);
    expect(
      (await store.transaction((t) => t.changeCursor('p'))).sequences,
      isEmpty,
    );
  });

  test('malformed category and future origin sequence fail closed', () async {
    expect(
      () => OfflineIdentityChunk(
        origin: _originA,
        sequence: BigInt.one,
        operation: OfflineIdentityOperation.addEdge,
        chunkIndex: 0,
        isLast: true,
        firstItemIndex: 0,
        keys: const [_key],
      ),
      throwsA(isA<OfflineArgumentException>()),
    );
    final first = _Session('one');
    final second = _Session('two');
    source
      ..queue(first)
      ..queue(second);
    final cancellation = LanternCancellationToken();
    final consume = repository.consumeIdentityChanges(
      'p',
      source: source,
      cancellation: cancellation,
    );
    await _waitUntil(() => first.listening);
    first.add(OfflineIdentityCheckpoint(const {}));
    first.add(_chunk(0, true, sequence: BigInt.from(2)));
    await _waitUntil(() => second.listening);
    expect(source.opens.length, 2);
    expect(source.opens.last.bootstrap, isTrue);
    cancellation.cancel();
    await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
  });

  test(
    'category changes within one mutation trigger checkpoint recovery',
    () async {
      const edge = OfflineEntityKey.edge('tail', 'head');
      final first = _Session('one');
      final second = _Session('two');
      source
        ..queue(first)
        ..queue(second);
      final cancellation = LanternCancellationToken();
      final consume = repository.consumeIdentityChanges(
        'p',
        source: source,
        cancellation: cancellation,
      );
      await _waitUntil(() => first.listening);
      first.add(OfflineIdentityCheckpoint(const {}));
      first.add(
        OfflineIdentityChunk(
          origin: _originA,
          sequence: BigInt.one,
          operation: OfflineIdentityOperation.addEdge,
          chunkIndex: 0,
          isLast: false,
          firstItemIndex: 0,
          keys: const [edge],
        ),
      );
      first.add(
        OfflineIdentityChunk(
          origin: _originA,
          sequence: BigInt.one,
          operation: OfflineIdentityOperation.putEdge,
          chunkIndex: 1,
          isLast: true,
          firstItemIndex: 1,
          keys: const [edge],
        ),
      );
      await _waitUntil(() => second.listening);
      expect(source.opens.last.bootstrap, isTrue);
      cancellation.cancel();
      await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
    },
  );

  test(
    'open-time retention gap hides residents before bootstrap retry',
    () async {
      remote.vertices['resident'] = _vertex('old');
      await repository.readVertex('p', 'resident');
      await store.transaction(
        (t) => t.applyChangeChunk(
          'p',
          OfflineChangeChunk(
            origin: _originA,
            sequence: BigInt.one,
            chunkIndex: 0,
            isLast: true,
            keys: const [OfflineEntityKey.vertex('other')],
          ),
        ),
      );
      source.fail(const OfflineChangeGapException());
      final second = _Session('new-endpoint')
        ..vertices['resident'] = _vertex('new');
      source.queue(second);
      final cancellation = LanternCancellationToken();
      final consume = repository.consumeIdentityChanges(
        'p',
        source: source,
        cancellation: cancellation,
      );
      await _waitUntil(() => second.listening);
      expect(source.opens.map((open) => open.bootstrap), [false, true]);
      expect(source.opens.first.nextExpected[_originA], BigInt.from(2));
      expect(
        await store.transaction((t) => t.hasUnknownResident('p', _key)),
        isTrue,
      );
      second.add(OfflineIdentityCheckpoint({_originA: BigInt.one}));
      await _waitUntil(() => second.vertexReads == 1);
      await _waitUntil(
        () async =>
            !await store.transaction((t) => t.hasUnknownResident('p', _key)),
      );
      cancellation.cancel();
      await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
    },
  );

  test('invalid responder identity triggers the same Unknown path', () async {
    remote.vertices['resident'] = _vertex('old');
    await repository.readVertex('p', 'resident');
    final invalid = _Session('');
    final valid = _Session('new-endpoint');
    source
      ..queue(invalid)
      ..queue(valid);
    final cancellation = LanternCancellationToken();
    final consume = repository.consumeIdentityChanges(
      'p',
      source: source,
      cancellation: cancellation,
    );
    await _waitUntil(() => valid.listening);
    expect(invalid.closed, isTrue);
    expect(
      await store.transaction((t) => t.hasUnknownResident('p', _key)),
      isTrue,
    );
    cancellation.cancel();
    await expectLater(consume, throwsA(isA<OfflineCanceledException>()));
  });

  test('second gap after a valid chunk stops with durable Unknown', () async {
    remote.vertices['resident'] = _vertex('old');
    await repository.readVertex('p', 'resident');
    final first = _Session('one');
    final second = _Session('two');
    source
      ..queue(first)
      ..queue(second);
    final consume = repository.consumeIdentityChanges('p', source: source);
    final failed = expectLater(
      consume,
      throwsA(isA<OfflineChangeGapException>()),
    );
    await _waitUntil(() => first.listening);
    first.add(OfflineIdentityCheckpoint(const {}));
    await _waitUntil(() => first.vertexReads == 1);
    first.add(_chunk(0, true, key: const OfflineEntityKey.vertex('other')));
    first.add(
      _chunk(
        0,
        true,
        key: const OfflineEntityKey.vertex('other'),
        sequence: BigInt.from(3),
      ),
    );
    await _waitUntil(() => second.listening);
    second.add(OfflineIdentityCheckpoint({_originA: BigInt.one}));
    await _waitUntil(() => second.vertexReads == 1);
    second.add(
      _chunk(
        0,
        true,
        key: const OfflineEntityKey.vertex('other'),
        sequence: BigInt.from(2),
      ),
    );
    second.add(
      _chunk(
        0,
        true,
        key: const OfflineEntityKey.vertex('other'),
        sequence: BigInt.from(4),
      ),
    );
    await failed;
    expect(source.opens.length, 2);
    expect(
      await store.transaction((t) => t.hasUnknownResident('p', _key)),
      isTrue,
    );
  });

  test('cancellation during plural recovery leaves durable Unknown', () async {
    remote.vertices['resident'] = _vertex('old');
    await repository.readVertex('p', 'resident');
    final session = _Session('endpoint')..vertexGate = Completer<void>();
    source.queue(session);
    final cancellation = LanternCancellationToken();
    final consume = repository.consumeIdentityChanges(
      'p',
      source: source,
      cancellation: cancellation,
    );
    final canceled = expectLater(
      consume,
      throwsA(isA<OfflineCanceledException>()),
    );
    await _waitUntil(() => session.listening);
    session.add(OfflineIdentityCheckpoint({_originA: BigInt.zero}));
    await _waitUntil(() => session.vertexReads == 1);
    expect(session.paused, isTrue);
    expect(
      await store.transaction((t) => t.hasUnknownResident('p', _key)),
      isTrue,
    );
    session.add(_chunk(0, true));
    expect(
      (await store.transaction((t) => t.changeCursor('p'))).sequences[_originA],
      BigInt.zero,
    );
    cancellation.cancel();
    session.vertexGate!.complete();
    await canceled;
    expect(
      await store.transaction((t) => t.hasUnknownResident('p', _key)),
      isTrue,
    );
    expect(session.closed, isTrue);
  });
}

Vertex _vertex(String value) =>
    Vertex(key: 'resident', value: VertexValue.string(value), expiration: null);

OfflineIdentityChunk _chunk(
  int index,
  bool last, {
  String origin = _originA,
  BigInt? sequence,
  OfflineEntityKey key = _key,
}) => OfflineIdentityChunk(
  origin: origin,
  sequence: sequence ?? BigInt.one,
  operation: OfflineIdentityOperation.putVertex,
  chunkIndex: index,
  isLast: last,
  firstItemIndex: index,
  keys: [key],
);

Future<void> _waitUntil(FutureOr<bool> Function() ready) async {
  for (var i = 0; i < 200; i++) {
    if (await ready()) return;
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
  fail('condition did not become true');
}

final class _Open {
  const _Open(this.bootstrap, this.nextExpected);
  final bool bootstrap;
  final Map<String, BigInt> nextExpected;
}

final class _Source implements OfflineIdentitySource {
  final List<Object> sessions = [];
  final List<_Open> opens = [];

  void queue(_Session session) => sessions.add(session);

  void fail(Object error) => sessions.add(error);

  @override
  Future<OfflineIdentitySession> open({
    required bool bootstrap,
    required Map<String, BigInt> nextExpected,
    required LanternCancellationToken cancellation,
  }) async {
    opens.add(_Open(bootstrap, Map.of(nextExpected)));
    if (sessions.isEmpty) throw const OfflineChangeGapException();
    final next = sessions.removeAt(0);
    if (next is _Session) return next;
    throw next;
  }

  Future<void> closeAll() async {
    for (final session in sessions) {
      if (session is _Session) await session.close();
    }
  }
}

final class _Session implements OfflineIdentitySession {
  _Session(this.responderId) {
    _events = StreamController<OfflineIdentityEvent>(
      sync: true,
      onListen: () => listening = true,
      onPause: () {
        paused = true;
        everPaused = true;
      },
      onResume: () => paused = false,
    );
  }

  @override
  String responderId;
  late final StreamController<OfflineIdentityEvent> _events;
  final Map<String, Vertex> vertices = {};
  final Map<EdgeRef, Edge> edges = {};
  void Function()? afterVertexRead;
  Completer<void>? vertexGate;
  int vertexReads = 0;
  bool listening = false;
  bool paused = false;
  bool everPaused = false;
  bool closed = false;

  void add(OfflineIdentityEvent event) => _events.add(event);

  @override
  Stream<OfflineIdentityEvent> get events => _events.stream;

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) async {
    vertexReads++;
    await vertexGate?.future;
    if (cancellation?.isCanceled ?? false) {
      throw const OfflineCanceledException();
    }
    afterVertexRead?.call();
    return [
      for (final key in keys)
        if (vertices[key] case final vertex?)
          OfflineRemotePresent<Vertex>(vertex)
        else
          const OfflineRemoteMissing<Vertex>(),
    ];
  }

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) async => [
    for (final edge in edges)
      if (this.edges[edge] case final value?)
        OfflineRemotePresent<Edge>(value)
      else
        const OfflineRemoteMissing<Edge>(),
  ];

  @override
  Future<void> close() async {
    if (closed) return;
    closed = true;
    if (listening) {
      await _events.close();
    } else {
      unawaited(_events.close());
    }
  }
}
