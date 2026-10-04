import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

void main() {
  late InMemoryOfflineStore store;
  late OfflineLanternRepository repository;
  late FakeOfflineRemote remote;
  late _Source source;
  setUp(() {
    store = InMemoryOfflineStore();
    remote = FakeOfflineRemote();
    source = _Source();
    repository = OfflineLanternRepository(
      store: store,
      remote: remote,
      config: testConfig(MutableClock(DateTime.utc(2026, 9, 24))),
    );
  });
  tearDown(() async {
    await repository.dispose();
    for (final s in source.all) {
      await s.close();
    }
  });
  Future<void> seed() async {
    remote.vertices['resident'] = _vertex('old');
    await repository.readVertex('p', 'resident');
  }

  Future<List<int>?> cursor() => store.transaction(
    (tx) async => (await tx.scopedChangeCursor('p'))?.toBytes(),
  );
  test(
    'bootstrap uses pinned plural reads; partial/final commits and cancellation resume',
    () async {
      await seed();
      final session = _Session('first')..vertices['resident'] = _vertex('new');
      source.queue(session);
      final cancellation = LanternCancellationToken();
      final run = repository.consumeScopedChanges(
        'p',
        source: source,
        cancellation: cancellation,
      );
      final stopped = expectLater(
        run,
        throwsA(isA<OfflineCanceledException>()),
      );
      await _wait(() => session.listening);
      session.add(
        OfflineScopedChangeFrame(
          bootstrap: true,
          cursor: OfflineScopedChangeCursor([1]),
        ),
      );
      await _wait(
        () async =>
            (await store.transaction(
              (tx) => tx.unknownResidents('p', limit: 1),
            )).isEmpty &&
            session.reads == 1,
      );
      expect(session.everPaused, isTrue);
      final cached = await store.transaction(
        (tx) => tx.getCache('p', const OfflineEntityKey.vertex('resident')),
      );
      expect(((cached!.entity! as Vertex).value as StringValue).value, 'new');
      session.add(
        OfflineScopedChangeFrame(
          keys: const [OfflineEntityKey.vertex('resident')],
        ),
      );
      await _wait(
        () async =>
            await store.transaction(
              (tx) =>
                  tx.getCache('p', const OfflineEntityKey.vertex('resident')),
            ) ==
            null,
      );
      expect(await cursor(), [1]);
      session.add(
        OfflineScopedChangeFrame(cursor: OfflineScopedChangeCursor([2])),
      );
      await _wait(() async => (await cursor())?.first == 2);
      cancellation.cancel();
      await stopped;
      expect(session.closed, isTrue);
      store = InMemoryOfflineStore.fromSnapshot(await store.exportSnapshot());
      await repository.dispose();
      repository = OfflineLanternRepository(
        store: store,
        remote: remote,
        config: testConfig(MutableClock(DateTime.utc(2026, 9, 24))),
      );
      final next = _Session('first');
      source.queue(next);
      final cancelNext = LanternCancellationToken();
      final resumed = repository.consumeScopedChanges(
        'p',
        source: source,
        cancellation: cancelNext,
      );
      final resumedStopped = expectLater(
        resumed,
        throwsA(isA<OfflineCanceledException>()),
      );
      await _wait(() => next.listening);
      expect(source.opens.last.bootstrap, isFalse);
      expect(source.opens.last.cursor, [2]);
      cancelNext.cancel();
      await resumedStopped;
    },
  );
  test('EOF hides residents and permits only one fresh bootstrap', () async {
    await seed();
    final first = _Session('one')..vertices['resident'] = _vertex('new');
    final next = _Session('two')..vertices['resident'] = _vertex('latest');
    source.queue(first);
    source.queue(next);
    final run = repository.consumeScopedChanges('p', source: source);
    final stopped = expectLater(run, throwsA(isA<OfflineChangeGapException>()));
    await _wait(() => first.listening);
    first.add(
      OfflineScopedChangeFrame(
        bootstrap: true,
        cursor: OfflineScopedChangeCursor([1]),
      ),
    );
    await _wait(() => first.reads == 1);
    await first.finish();
    await _wait(() => next.listening);
    expect(source.opens.length, 2);
    expect(source.opens.last.bootstrap, isTrue);
    expect(
      await store.transaction(
        (tx) => tx.hasUnknownResident(
          'p',
          const OfflineEntityKey.vertex('resident'),
        ),
      ),
      isTrue,
    );
    next.add(
      OfflineScopedChangeFrame(
        bootstrap: true,
        cursor: OfflineScopedChangeCursor([2]),
      ),
    );
    await _wait(() => next.reads == 1);
    await next.finish();
    await stopped;
    expect(source.opens.length, 2);
    expect(await cursor(), isNull);
    expect(
      await store.transaction(
        (tx) => tx.hasUnknownResident(
          'p',
          const OfflineEntityKey.vertex('resident'),
        ),
      ),
      isTrue,
    );
  });
  test(
    'authorization and availability errors hide cache without retry',
    () async {
      for (final error in [
        const OfflineRemoteFailure(
          OfflineRemoteErrorKind.unauthenticated,
          'fixture',
        ),
        const OfflineRemoteFailure(
          OfflineRemoteErrorKind.unavailable,
          'fixture',
        ),
      ]) {
        await seed();
        final s = _Session('one')..vertices['resident'] = _vertex('new');
        source.queue(s);
        final before = source.opens.length;
        final run = repository.consumeScopedChanges('p', source: source);
        final stopped = expectLater(run, throwsA(same(error)));
        await _wait(() => s.listening);
        s.add(
          OfflineScopedChangeFrame(
            bootstrap: true,
            cursor: OfflineScopedChangeCursor([1]),
          ),
        );
        await _wait(() => s.reads == 1);
        s.fail(error);
        await stopped;
        expect(source.opens.length, before + 1);
        expect(await cursor(), isNull);
        expect(
          await store.transaction(
            (tx) => tx.hasUnknownResident(
              'p',
              const OfflineEntityKey.vertex('resident'),
            ),
          ),
          isTrue,
        );
      }
    },
  );
  test(
    'responder drift during recovery cannot commit fetched values',
    () async {
      await seed();
      final s = _Session('one')..vertices['resident'] = _vertex('new');
      s.afterRead = () => s.responderId = 'changed';
      source.queue(s);
      final stopped = expectLater(
        repository.consumeScopedChanges('p', source: source),
        throwsA(isA<OfflineChangeGapException>()),
      );
      await _wait(() => s.listening);
      s.add(
        OfflineScopedChangeFrame(
          bootstrap: true,
          cursor: OfflineScopedChangeCursor([1]),
        ),
      );
      await stopped;
      expect(await cursor(), isNull);
      expect(
        await store.transaction(
          (tx) => tx.getCache('p', const OfflineEntityKey.vertex('resident')),
        ),
        isNull,
      );
    },
  );
  test(
    'logout releases the stream and prevents late frame publication',
    () async {
      final s = _Session('one');
      source.queue(s);
      final run = repository.consumeScopedChanges('p', source: source);
      final stopped = expectLater(
        run,
        throwsA(isA<OfflineCanceledException>()),
      );
      await _wait(() => s.listening);
      s.add(
        OfflineScopedChangeFrame(
          bootstrap: true,
          cursor: OfflineScopedChangeCursor([1]),
        ),
      );
      await _wait(() async => await cursor() != null);
      await repository.wipePartition('p');
      await stopped;
      expect(s.closed, isTrue);
      expect(await cursor(), isNull);
    },
  );
}

Vertex _vertex(String value) => Vertex(
  key: 'resident',
  value: VertexValue.string(value),
  expiration: DateTime.utc(2027),
);
Future<void> _wait(FutureOr<bool> Function() ready) async {
  for (var i = 0; i < 200; i++) {
    if (await ready()) return;
    await Future<void>.delayed(const Duration(milliseconds: 5));
  }
  fail('condition did not become true');
}

final class _Open {
  const _Open(this.bootstrap, this.cursor);
  final bool bootstrap;
  final List<int>? cursor;
}

final class _Source implements OfflineScopedChangeSource {
  final List<_Session> pending = [];
  final List<_Session> all = [];
  final List<_Open> opens = [];
  void queue(_Session s) {
    pending.add(s);
    all.add(s);
  }

  @override
  Future<OfflineScopedChangeSession> open({
    required bool bootstrap,
    required OfflineScopedChangeCursor? cursor,
    required LanternCancellationToken cancellation,
  }) async {
    opens.add(_Open(bootstrap, cursor?.toBytes()));
    if (pending.isEmpty) throw const OfflineChangeGapException();
    return pending.removeAt(0);
  }
}

final class _Session implements OfflineScopedChangeSession {
  _Session(this.responderId) {
    controller = StreamController<OfflineScopedChangeFrame>(
      sync: true,
      onListen: () => listening = true,
      onPause: () => everPaused = true,
    );
  }
  @override
  String responderId;
  late final StreamController<OfflineScopedChangeFrame> controller;
  final Map<String, Vertex> vertices = {};
  int reads = 0;
  bool listening = false;
  bool everPaused = false;
  bool closed = false;
  void Function()? afterRead;
  void add(OfflineScopedChangeFrame frame) => controller.add(frame);
  void fail(Object error) => controller.addError(error);
  Future<void> finish() => controller.close();
  @override
  Stream<OfflineScopedChangeFrame> get frames => controller.stream;
  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) async {
    reads++;
    afterRead?.call();
    return [
      for (final key in keys)
        if (vertices[key] case final value?)
          OfflineRemotePresent<Vertex>(value)
        else
          const OfflineRemoteMissing<Vertex>(),
    ];
  }

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) async => [for (final _ in edges) const OfflineRemoteMissing<Edge>()];
  @override
  Future<void> close() async {
    if (closed) return;
    closed = true;
    if (listening) {
      await controller.close();
    } else {
      unawaited(controller.close());
    }
  }
}
