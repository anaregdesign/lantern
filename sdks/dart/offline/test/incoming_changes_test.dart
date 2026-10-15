import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart';
import 'package:lantern_client/src/gen/graph/v1/changes.connect.spec.dart'
    as changes;
import 'package:lantern_client/src/gen/graph/v1/changes.pb.dart' as wire;
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart'
    as graphspec;
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:test/test.dart';

import 'helpers.dart' show FakeOfflineRemote, offlineReceiptCapability;
import 'support/session_clock.dart';

final class _Tail {
  _Tail(this.request);
  final wire.WatchChangesRequest request;
  final frames = StreamController<wire.WatchChangesResponse>();
  bool canceled = false;
  void bootstrap([int cursor = 1]) =>
      frames.add(wire.WatchChangesResponse(bootstrap: true, cursor: [cursor]));
  void invalidate(String key, {int? cursor}) => frames.add(
    wire.WatchChangesResponse(
      cursor: cursor == null ? null : [cursor],
      invalidations: [wire.ChangeInvalidation(vertexKey: key)],
    ),
  );
  void fail(connect.Code code) =>
      frames.addError(connect.ConnectException(code, 'fixture'));
}

final class _Store implements OfflineStore, OfflineAutomaticStore {
  _Store({OfflineStoreLimits limits = const OfflineStoreLimits()})
    : inner = MemoryOfflineStore(limits: limits);
  final MemoryOfflineStore inner;
  String partition = '';
  bool failCursor = false;
  Future<void> Function()? beforeTransaction, afterCursorCommit;
  @override
  Object get canonicalOwnerKey => inner.canonicalOwnerKey;
  @override
  Future<OfflineAutomaticOwner> acquireAutomaticOwner({
    required String scope,
    required String bindingFingerprint,
    required String configurationFingerprint,
  }) => inner.acquireAutomaticOwner(
    scope: scope,
    bindingFingerprint: bindingFingerprint,
    configurationFingerprint: configurationFingerprint,
  );
  @override
  Stream<OfflineStoreChange> changes(String partitionId) =>
      inner.changes(partitionId);
  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction) action,
  ) async {
    await beforeTransaction?.call();
    var cursorChanged = false;
    final result = await inner.transaction((tx) async {
      final before = (await tx.scopedChangeCursor(partition))?.toJson();
      final result = await action(tx);
      cursorChanged =
          before != (await tx.scopedChangeCursor(partition))?.toJson();
      if (failCursor && cursorChanged) {
        failCursor = false;
        throw StateError('injected commit failure');
      }
      return result;
    });
    if (cursorChanged) await afterCursorCommit?.call();
    return result;
  }
}

final class _Writes implements OfflineBoundMutationRemote {
  void Function()? apply;
  Future<void> Function()? afterDispatch;
  int calls = 0;
  @override
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord record, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  }) async {
    beforeDispatch();
    calls++;
    apply?.call();
    await afterDispatch?.call();
    beforePublication();
    return const MutationKnownEffect(PutOutcome.appliedAndLive);
  }
}

final class _Fixture {
  final clock = Clock(), writes = _Writes();
  final _Store store;
  final endpoint = Uri.parse('http://localhost:6380');
  final tails = <_Tail>[], vertexReads = <List<String>>[];
  final vertices = <String, graph.Vertex>{};
  final edges = <graph.Edge>[];
  final edgeReadWeights = <double>[];
  int edgeReads = 0;
  bool affinity = true;
  connect.Code? streamFailure, readFailure;
  Future<void> Function()? beforeRead;
  late final LanternClient client;
  late LanternSessionBinding current;
  late final LanternSessionBinding binding;
  late final LanternChangeSource source;
  OfflineLanternSession? session;
  final subscriptions = <StreamSubscription<Object?>>[];
  _Fixture({OfflineStoreLimits limits = const OfflineStoreLimits()})
    : store = _Store(limits: limits) {
    binding = current = LanternSessionBinding.application(
      serviceOrigin: endpoint,
      securityDomain: 'fixture',
      accountIdentity: 'account',
      authorityBinding: 'cut',
    );
    store.partition = binding.stableIdentity;
    final transport = FakeTransportBuilder()
        .server<wire.WatchChangesRequest, wire.WatchChangesResponse>(
          changes.LanternChangeService.watchChanges,
          (request, context) async* {
            final tail = _Tail(request);
            tails.add(tail);
            if (streamFailure case final code?) {
              throw connect.ConnectException(code, 'fixture');
            }
            unawaited(
              context.signal.future.then((_) {
                if (!tail.frames.isClosed) unawaited(tail.frames.close());
              }),
            );
            try {
              yield* tail.frames.stream;
            } finally {
              tail.canceled = true;
            }
          },
        )
        .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
          graphspec.LanternService.getVertices,
          (request, _) async {
            vertexReads.add(request.keys.toList());
            await beforeRead?.call();
            if (readFailure case final code?) {
              throw connect.ConnectException(code, 'fixture');
            }
            return graph.GetVerticesResponse(
              vertices: [
                for (final key in request.keys.toSet()) ?vertices[key],
              ],
              missing: [
                for (final key in request.keys)
                  if (!vertices.containsKey(key)) key,
              ],
            );
          },
        )
        .unary<graph.GetEdgesRequest, graph.GetEdgesResponse>(
          graphspec.LanternService.getEdges,
          (request, _) async {
            edgeReads++;
            edgeReadWeights.addAll(edges.map((edge) => edge.weight));
            return graph.GetEdgesResponse(edges: edges);
          },
        )
        .build();
    client = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      clock: clock.call,
      transport: transport,
    );
    source = LanternChangeSource(
      route: LanternSingleResponderRoute.certified(
        client: client,
        responderId: 'direct-node',
        isCurrent: () => affinity,
      ),
      binding: binding,
      currentBinding: () => current,
    );
  }
  Future<OfflineLanternSession> open({
    bool cache = true,
    bool incoming = true,
  }) async {
    final receipt = FakeOfflineRemote()
      ..receiptCapability = offlineReceiptCapability(serverNow: clock.now);
    return session = await OfflineLanternSession.openStore(
      client: client,
      storeKey: store,
      createStore: () async => store,
      closeStore: (_) async {},
      binding: binding,
      durability: AdmissionDurability.memory,
      cacheReads: cache,
      incomingChanges: incoming ? source : null,
      clock: clock.call,
      schedule: clock.schedule,
      receiptRemote: receipt,
      mutationRemote: writes,
    );
  }

  void value(String key, String value) =>
      vertices[key] = graph.Vertex(key: key, string: value);
  Future<void> tick([int ms = 50]) => clock.advance(Duration(milliseconds: ms));
  Future<List<int>?> cursor() => store.transaction(
    (tx) async => (await tx.scopedChangeCursor(store.partition))?.toBytes(),
  );
  Future<LanternSnapshot<Vertex>> cached(String key) => session!.getVertex(
    key,
    options: const GetOptions(source: ReadSource.cacheOnly),
  );
  Future<void> seed(int count) => store.transaction((tx) async {
    for (var i = 0; i < count; i++) {
      final key = 'resident:$i';
      await tx.putCache(
        store.partition,
        OfflineCacheRecord.value(
          partitionId: store.partition,
          generation: await tx.generation(store.partition),
          key: OfflineEntityKey.vertex(key),
          entity: Vertex(
            key: key,
            value: VertexValue.string('old'),
            expiration: null,
          ),
          validatedAt: clock.now,
          lastAccessAt: clock.now,
        ),
      );
      value(key, 'new');
    }
  });
  Future<void> close() async {
    for (final sub in subscriptions) {
      await sub.cancel();
    }
    await session?.close();
    await client.close();
    for (final tail in tails) {
      if (!tail.frames.isClosed) unawaited(tail.frames.close());
    }
    expect(clock.active, 0);
  }
}

String? _text(LanternSnapshot<Vertex> s) =>
    (s.confirmed.value?.value as StringValue?)?.value;
const watching = WatchOptions(pollInterval: Duration(minutes: 1));

void main() {
  late _Fixture f;
  setUp(() => f = _Fixture());
  tearDown(() => f.close());

  test(
    'T03 bootstrap independently recovers indexed plural pages of 32',
    () async {
      await f.seed(65);
      await f.open();
      await f.tick();
      expect(f.tails.single.request.bootstrap, isTrue);
      expect(f.vertexReads, isEmpty);
      f.tails.single.bootstrap();
      await flush();
      expect(
        (await f.cached('resident:0')).confirmed.state,
        ConfirmedState.unknown,
      );
      for (var i = 0; i < 4; i++) {
        await f.tick();
      }
      expect(f.vertexReads.map((keys) => keys.length), [32, 32, 1]);
      expect(_text(await f.cached('resident:0')), 'new');
      expect(await f.cursor(), [1]);
      expect(
        (await f.session!.syncStatus.first).reachability,
        SyncReachability.idle,
      );
    },
  );

  test(
    'T03 partial invalidates; final installs cursor; active watches coalesce',
    () async {
      f.value('one', 'initial');
      await f.open();
      final a = <LanternSnapshot<Vertex>>[],
          b = <List<LanternSnapshot<Vertex>>>[];
      final one = f.session!
          .watchVertex('one', options: watching)
          .listen(a.add);
      final two = f.session!
          .watchVertices(['one'], options: watching)
          .listen(b.add);
      f.subscriptions.addAll([one, two]);
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      await f.tick();
      expect(f.vertexReads, [
        ['one'],
      ]);
      expect(_text(a.last), 'initial');
      expect(_text(b.last.single), 'initial');
      f.value('one', 'next');
      f.tails.single.invalidate('one');
      await flush();
      expect(await f.cursor(), [1]);
      expect((await f.cached('one')).confirmed.state, ConfirmedState.unknown);
      await f.tick();
      expect(_text(a.last), 'next');
      expect(f.vertexReads.length, 2);
      f.tails.single.frames.add(wire.WatchChangesResponse(cursor: [2]));
      await flush();
      expect(await f.cursor(), [2]);
      await one.cancel();
      f.value('one', 'last');
      f.tails.single.invalidate('one', cursor: 3);
      await flush();
      await f.tick();
      expect(_text(b.last.single), 'last');
      await two.cancel();
      final calls = f.vertexReads.length;
      f.tails.single.invalidate('one', cursor: 4);
      await flush();
      await f.tick(1000);
      expect(f.vertexReads.length, calls);
    },
  );

  for (final event in ['control', 'cursor-only progress', 'identity change']) {
    test(
      'R1794 $event fences only semantically invalidated observations',
      () async {
        f.value('one', 'initial');
        await f.open();
        final values = <LanternSnapshot<Vertex>>[];
        f.subscriptions.add(
          f.session!.watchVertex('one', options: watching).listen(values.add),
        );
        await f.tick();
        f.tails.single.bootstrap();
        await flush();
        final release = Completer<void>();
        addTearDown(() {
          if (!release.isCompleted) release.complete();
        });
        f.beforeRead = () => release.future;
        await f.tick();
        expect(f.vertexReads, [
          ['one'],
        ]);
        if (event == 'cursor-only progress') {
          // The authenticated wire fixture sends these progress frames every
          // 100 ms. No identity, authority or route changed during this read.
          f.tails.single.frames.add(wire.WatchChangesResponse(cursor: [2]));
          await flush();
          expect(await f.cursor(), [2]);
        } else if (event == 'identity change') {
          f.tails.single.invalidate('one', cursor: 2);
          await flush();
        }
        release.complete();
        await flush();
        // Do not advance the scheduler or poll again to hide a discarded reply.
        expect(
          values.map(_text),
          event == 'identity change'
              ? isNot(contains('initial'))
              : contains('initial'),
        );
        expect(f.vertexReads, hasLength(1));
        if (event == 'identity change') {
          f.value('one', 'updated');
          await f.tick();
          expect(_text(values.last), 'updated');
          expect(f.vertexReads, hasLength(2));
        }
      },
    );
  }

  test(
    'R1794 first resumed progress wakes a blocked long-poll watch',
    () async {
      f.value('one', 'initial');
      await f.open(cache: false);
      final values = <LanternSnapshot<Vertex>>[];
      f.subscriptions.add(
        f.session!
            .watchVertex(
              'one',
              options: const WatchOptions(pollInterval: Duration(minutes: 10)),
            )
            .listen(values.add),
      );
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      await f.tick();
      expect(_text(values.last), 'initial');
      await f.session!.suspend();
      await f.session!.resume();
      await f.tick();
      expect(f.tails, hasLength(2));
      expect(f.tails.last.request.bootstrap, isFalse);
      expect(values.last.confirmed.state, ConfirmedState.unknown);
      expect(f.vertexReads, hasLength(1));

      final release = Completer<void>();
      addTearDown(() {
        if (!release.isCompleted) release.complete();
      });
      f.beforeRead = () => release.future;
      f.value('one', 'resumed');
      f.tails.last.frames.add(wire.WatchChangesResponse(cursor: [2]));
      await flush();
      await f.tick();
      expect(f.vertexReads, hasLength(2));
      f.tails.last.frames.add(wire.WatchChangesResponse(cursor: [3]));
      await flush();
      release.complete();
      await flush();
      expect(_text(values.last), 'resumed');
      expect(f.vertexReads, hasLength(2));
    },
  );

  for (final boundary in [
    'cancel before',
    'route before',
    'cancel after',
    'route after',
  ]) {
    test(
      'R1794 $boundary empty commit cannot establish late readiness',
      () async {
        f.value('one', 'initial');
        await f.open(cache: false);
        final values = <LanternSnapshot<Vertex>>[];
        f.subscriptions.add(
          f.session!.watchVertex('one', options: watching).listen(values.add),
        );
        await f.tick();
        f.tails.single.bootstrap();
        await flush();
        await f.tick();
        expect(_text(values.last), 'initial');
        await f.session!.suspend();
        await f.session!.resume();
        await f.tick();
        expect(values.last.confirmed.state, ConfirmedState.unknown);
        final entered = Completer<void>(), release = Completer<void>();
        addTearDown(() {
          if (!release.isCompleted) release.complete();
        });
        Future<void> hold() async {
          f.store.beforeTransaction = null;
          f.store.afterCursorCommit = null;
          entered.complete();
          await release.future;
        }

        if (boundary.endsWith('before')) {
          f.store.beforeTransaction = hold;
        } else {
          f.store.afterCursorCommit = hold;
        }
        values.clear();
        f.value('one', 'late');
        f.tails.last.frames.add(wire.WatchChangesResponse(cursor: [2]));
        await entered.future;
        Future<void>? stopping;
        if (boundary.startsWith('cancel')) {
          stopping = f.session!.suspend();
        } else {
          f.affinity = false;
        }
        await flush();
        release.complete();
        await stopping;
        await flush();
        expect(f.vertexReads, hasLength(1));
        expect(values.map(_text), isNot(contains('late')));
        expect(
          await f.cursor(),
          boundary.startsWith('route')
              ? isNull
              : boundary.endsWith('after')
              ? [2]
              : [1],
        );
      },
    );
  }

  test(
    'T08 ordinary loss preserves historical evidence and resumes completed cursor',
    () async {
      await f.seed(65);
      await f.open();
      await f.tick();
      f.tails.single.bootstrap(7);
      await flush();
      await f.tick();
      f.tails.single.invalidate('other');
      await flush();
      f.tails.single.fail(connect.Code.unavailable);
      await flush();
      expect(_text(await f.cached('resident:0')), 'new');
      expect(
        (await f.cached('resident:0')).confirmed.freshness,
        ObservationFreshness.historical,
      );
      expect(await f.cursor(), [7]);
      await f.tick(30000);
      expect(f.tails.length, 2);
      expect(f.tails.last.request.bootstrap, isFalse);
      expect(f.tails.last.request.cursor, [7]);
      f.tails.last.frames.add(wire.WatchChangesResponse(cursor: [8]));
      await flush();
      expect(await f.cursor(), [8]);
      final reads = f.vertexReads.length;
      await f.tick(49);
      expect(f.vertexReads, hasLength(reads));
      await f.tick(1);
      expect(f.vertexReads, hasLength(reads + 1));
      expect(_text(await f.cached(f.vertexReads.last.first)), 'new');
    },
  );

  for (final failure in ['gap', 'malformed', 'eof']) {
    test('T08 $failure hides history and requires a new bootstrap', () async {
      await f.seed(1);
      await f.open();
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      await f.tick();
      switch (failure) {
        case 'gap':
          f.tails.single.fail(connect.Code.failedPrecondition);
        case 'malformed':
          f.tails.single.frames.add(wire.WatchChangesResponse());
        case 'eof':
          unawaited(f.tails.single.frames.close());
      }
      await flush();
      expect(
        (await f.cached('resident:0')).confirmed.state,
        ConfirmedState.unknown,
      );
      expect(await f.cursor(), isNull);
      await f.tick(30000);
      expect(f.tails.last.request.bootstrap, isTrue);
    });
  }

  for (final empty in [false, true]) {
    test(
      'T10 ${empty ? 'empty' : 'key-bearing'} cursor transaction failure publishes neither invalidation nor completion',
      () async {
        await f.seed(1);
        await f.open();
        await f.tick();
        f.tails.single.bootstrap();
        await flush();
        await f.tick();
        f.store.failCursor = true;
        if (empty) {
          f.tails.single.frames.add(wire.WatchChangesResponse(cursor: [2]));
        } else {
          f.tails.single.invalidate('resident:0', cursor: 2);
        }
        await flush();
        expect(await f.cursor(), [1]);
        expect((await f.session!.syncStatus.first).pause, SyncPause.storage);
        expect(_text(await f.cached('resident:0')), 'new');
        await f.tick(30000);
        expect(f.tails.length, 1);
      },
    );
  }

  test(
    'T11 absent source polls boundedly; strict-live remains unsupported',
    () async {
      f.value('one', 'first');
      await f.open(incoming: false);
      final values = <LanternSnapshot<Vertex>>[];
      f.subscriptions.add(
        f.session!
            .watchVertex(
              'one',
              options: const WatchOptions(pollInterval: Duration(seconds: 1)),
            )
            .listen(values.add),
      );
      await flush();
      await f.tick();
      expect(_text(values.last), 'first');
      expect(f.tails, isEmpty);
      f.value('one', 'second');
      await f.tick(1000);
      expect(_text(values.last), 'second');
      expect(values.last.observationMode, ObservationMode.pollingObservation);
      expect(f.vertexReads.length, 2);
      await expectLater(
        f.session!
            .watchVertex(
              'one',
              options: const WatchOptions(coverage: WatchCoverage.strictLive),
            )
            .first,
        throwsA(isA<LanternDataException>()),
      );
    },
  );

  test(
    'T11 unimplemented stream falls back but denial cannot poll around rejection',
    () async {
      for (final code in [
        connect.Code.unimplemented,
        connect.Code.permissionDenied,
      ]) {
        if (f.session != null) {
          await f.close();
          f = _Fixture();
        }
        f.streamFailure = code;
        f.value('one', 'allowed');
        await f.open();
        final values = <LanternSnapshot<Vertex>>[];
        f.subscriptions.add(
          f.session!.watchVertex('one', options: watching).listen(values.add),
        );
        await f.tick();
        await f.tick();
        if (code == connect.Code.unimplemented) {
          expect(_text(values.last), 'allowed');
          expect(
            values.last.observationMode,
            ObservationMode.pollingObservation,
          );
        } else {
          expect(f.vertexReads, isEmpty);
          expect(values.last.confirmed.state, ConfirmedState.unknown);
          expect((await f.session!.syncStatus.first).pause, SyncPause.policy);
        }
        await f.tick(60000);
        expect(f.tails.length, 1);
      }
    },
  );

  test(
    'T15 no-read-cache active watch refetches and never replays last success on loss',
    () async {
      f.value('one', 'first');
      await f.open(cache: false);
      final values = <LanternSnapshot<Vertex>>[];
      f.subscriptions.add(
        f.session!.watchVertex('one', options: watching).listen(values.add),
      );
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      await f.tick();
      expect(_text(values.last), 'first');
      expect((await f.cached('one')).confirmed.state, ConfirmedState.unknown);
      f.value('one', 'second');
      f.tails.single.invalidate('one', cursor: 2);
      await flush();
      await f.tick();
      expect(_text(values.last), 'second');
      f.tails.single.fail(connect.Code.unavailable);
      await flush();
      expect(values.last.confirmed.state, ConfirmedState.unknown);
      expect(_text(values.last), isNull);
    },
  );

  test(
    'T16 route/authority drift during a read cannot publish or retain its result',
    () async {
      for (final authority in [false, true]) {
        if (f.session != null) {
          await f.close();
          f = _Fixture();
        }
        f.value('one', 'stale');
        await f.open();
        await f.tick();
        f.tails.single.bootstrap();
        await flush();
        final held = Completer<void>(), dispatched = Completer<void>();
        f.beforeRead = () {
          dispatched.complete();
          return held.future;
        };
        final read = f.session!.getVertex('one');
        await flush();
        await f.tick();
        await dispatched.future;
        if (authority) {
          f.current = LanternSessionBinding.application(
            serviceOrigin: f.endpoint,
            securityDomain: 'fixture',
            accountIdentity: 'account',
            authorityBinding: 'changed',
          );
        } else {
          f.affinity = false;
        }
        held.complete();
        expect((await read).confirmed.state, ConfirmedState.unknown);
        expect((await f.cached('one')).confirmed.state, ConfirmedState.unknown);
        expect(await f.cursor(), isNull);
      }
    },
  );

  test(
    'T17 incoming recovery is independent of a blocked outgoing mutation',
    () async {
      await f.seed(1);
      await f.open();
      final held = Completer<void>();
      f.writes.afterDispatch = () => held.future;
      final ticket = await f.session!.mutations.submit(
        MutationRequest.putVertices([
          const VertexInput(key: 'outgoing', value: StringValue('pending')),
        ]),
      );
      try {
        await f.tick();
        expect(f.writes.calls, 1);
        f.tails.single.bootstrap();
        await flush();
        await f.tick();
        expect(_text(await f.cached('resident:0')), 'new');
        expect(
          (await f.session!.mutations.getStatus(
            ticket.reference.operationId,
          )).items.single.state,
          isNot(MutationItemState.handled),
        );
      } finally {
        held.complete();
      }
      await flush();
    },
  );

  test(
    'T15 active Edge expires with its endpoint without rewriting Edge TTL',
    () async {
      f.value('tail', 'tail');
      f.value('head', 'head');
      final endpointExpiry = f.clock.now.add(const Duration(seconds: 1));
      final edgeExpiry = f.clock.now.add(const Duration(minutes: 1));
      f.vertices['tail']!.expiration = Timestamp.fromDateTime(endpointExpiry);
      f.edges.add(
        graph.Edge(
          tail: 'tail',
          head: 'head',
          weight: 2,
          expiration: Timestamp.fromDateTime(edgeExpiry),
        ),
      );
      await f.open();
      final values = <LanternSnapshot<Edge>>[];
      f.subscriptions.add(
        f.session!
            .watchEdge(const EdgeRef('tail', 'head'), options: watching)
            .listen(values.add),
      );
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      await f.tick();
      expect(values.last.confirmed.state, ConfirmedState.present);
      expect(values.last.confirmed.expiration, endpointExpiry);
      expect(values.last.confirmed.value!.expiration, edgeExpiry);
      f.readFailure = connect.Code.unavailable;
      await f.tick(1000);
      expect(values.last.confirmed.state, isNot(ConfirmedState.present));
      expect(
        (await f.session!.getEdge(
          const EdgeRef('tail', 'head'),
          options: const GetOptions(source: ReadSource.cacheOnly),
        )).confirmed.state,
        isNot(ConfirmedState.present),
      );
    },
  );

  test('T15 Vertex settlement refreshes an active dependent Edge', () async {
    f.value('tail', 'tail');
    f.value('head', 'head');
    f.edges.add(graph.Edge(tail: 'tail', head: 'head', weight: 2));
    await f.open();
    final values = <LanternSnapshot<Edge>>[];
    f.subscriptions.add(
      f.session!
          .watchEdge(const EdgeRef('tail', 'head'), options: watching)
          .listen(values.add),
    );
    await f.tick();
    f.tails.single.bootstrap();
    await flush();
    await f.tick();
    expect(values.last.confirmed.value!.weight, 2);
    f.writes.apply = () {
      f.value('tail', 'updated');
      f.edges[0] = graph.Edge(tail: 'tail', head: 'head', weight: 7);
    };
    await f.session!.mutations.submit(
      MutationRequest.putVertices([
        const VertexInput(key: 'tail', value: StringValue('updated')),
      ]),
    );
    await f.tick();
    await f.tick();
    expect(values.last.confirmed.value!.weight, 7);
    // Admission and settlement are separate committed notifications. Even if
    // admission's refresh sees the new remote value, settlement refreshes again.
    expect(f.edgeReadWeights, [2, 7, 7]);
    await f.tick(1000);
    expect(f.edgeReads, 3);
  });

  test(
    'T15 automatic CDC overflow reports loss and coalesces revalidation',
    () async {
      f.value('one', 'initial');
      await f.open();
      final values = <LanternSnapshot<Vertex>>[];
      final sub = f.session!
          .watchVertex('one', options: watching)
          .listen(values.add);
      f.subscriptions.add(sub);
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      await f.tick();
      expect(_text(values.last), 'initial');
      sub.pause();
      for (var i = 0; i < 12; i++) {
        f.value('one', 'value:$i');
        f.tails.single.invalidate('one', cursor: i + 2);
        await flush();
      }
      final before = f.vertexReads.length;
      sub.resume();
      await flush();
      await f.tick();
      expect(values.any((s) => s.observationLost), isTrue);
      expect(_text(values.last), 'value:11');
      expect(f.vertexReads.length, before + 1);
    },
  );

  test(
    'T11 invalidation between plural chunks rejects the earlier chunk',
    () async {
      for (var i = 0; i < 33; i++) {
        f.value('key:$i', 'old');
      }
      await f.open();
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      final held = Completer<void>(), second = Completer<void>();
      f.beforeRead = () async {
        if (f.vertexReads.length == 2) {
          second.complete();
          await held.future;
        }
      };
      final read = f.session!.getVertices([
        for (var i = 0; i < 33; i++) 'key:$i',
      ], options: const GetOptions(source: ReadSource.serverOnly));
      try {
        await flush();
        await f.tick();
        await f.tick();
        await second.future;
        f.tails.single.invalidate('key:0', cursor: 2);
        await flush();
      } finally {
        held.complete();
      }
      final result = await read;
      expect(result, hasLength(33));
      expect(result.first.confirmed.state, ConfirmedState.unknown);
      expect(result.last.confirmed.state, ConfirmedState.unknown);
      expect(f.vertexReads.map((keys) => keys.length), [32, 1]);
      expect(await f.cursor(), [2]);
    },
  );

  test(
    'T10 authenticated refresh joins the old tail and bootstraps after auth pause',
    () async {
      await f.seed(1);
      await f.open();
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      await f.tick();
      final previous = f.tails.single;
      previous.fail(connect.Code.unauthenticated);
      await flush();
      expect((await f.session!.syncStatus.first).pause, SyncPause.auth);
      expect(
        (await f.cached('resident:0')).confirmed.state,
        ConfirmedState.unknown,
      );
      expect(await f.cursor(), isNull);
      await f.tick(60000);
      expect(f.tails.length, 1);
      var installed = false;
      final accepted = await f.session!.acceptVerifiedCredentials(
        OfflineVerifiedCredentials(
          binding: f.binding,
          install: () {
            expect(previous.canceled, isTrue);
            installed = true;
          },
        ),
      );
      expect(accepted, isTrue);
      expect(installed, isTrue);
      await f.tick();
      expect(f.tails.length, 2);
      expect(f.tails.last.request.bootstrap, isTrue);
      f.value('resident:0', 'refreshed');
      f.tails.last.bootstrap(2);
      await flush();
      await f.tick();
      expect(_text(await f.cached('resident:0')), 'refreshed');
      expect((await f.session!.syncStatus.first).pause, SyncPause.none);
    },
  );

  test(
    'T11 resume cannot clear permission pause or poll around the rejected source',
    () async {
      f.streamFailure = connect.Code.permissionDenied;
      await f.open();
      await f.tick();
      expect((await f.session!.syncStatus.first).pause, SyncPause.policy);
      await f.session!.suspend();
      await f.session!.resume();
      await f.tick(60000);
      expect(f.tails.length, 1);
      expect(f.vertexReads, isEmpty);
      expect((await f.session!.syncStatus.first).pause, SyncPause.policy);
    },
  );

  test('T09 close cancels the tail and removes the shared alarm', () async {
    await f.open();
    await f.tick();
    f.tails.single.bootstrap();
    await flush();
    await f.session!.close();
    expect(f.tails.single.canceled, isTrue);
    expect(f.clock.active, 0);
    await f.tick(60000);
    expect(f.tails.length, 1);
  });

  test('T15 Edge dependency reads keep the 32-identity RPC bound', () async {
    for (var i = 0; i < 32; i++) {
      f.value('tail:$i', 'tail');
      f.value('head:$i', 'head');
      f.edges.add(graph.Edge(tail: 'tail:$i', head: 'head:$i', weight: 1));
    }
    await f.open();
    await f.tick();
    f.tails.single.bootstrap();
    await flush();
    final read = f.session!.getEdges([
      for (var i = 0; i < 32; i++) EdgeRef('tail:$i', 'head:$i'),
    ]);
    await flush();
    await f.tick();
    expect(
      (await read).every((s) => s.confirmed.state == ConfirmedState.present),
      isTrue,
    );
    expect(f.vertexReads.map((keys) => keys.length), [32, 32]);
  });

  test(
    'T17 hot first resident page cannot starve unrelated later residents',
    () async {
      await f.seed(65);
      await f.open();
      await f.tick();
      f.tails.single.bootstrap();
      await flush();
      var cursor = 1;
      f.beforeRead = () async {
        if (f.vertexReads.last.contains('resident:0')) {
          // A legitimate update racing every first-page read prevents its commit.
          // Other pages must still receive bounded recovery work.
          f.tails.single.invalidate('resident:0', cursor: ++cursor);
          await flush();
        }
      };
      for (var i = 0; i < 6; i++) {
        await f.tick();
      }
      expect(_text(await f.cached('resident:64')), 'new');
      expect(f.vertexReads.length, lessThanOrEqualTo(6));
    },
  );

  for (final interruptWindow in [false, true]) {
    test(
      'R1 ${interruptWindow ? 'interrupted' : 'completed'} window and wrap floors survive CDC wakes and continuous resume',
      () async {
        await f.close();
        f = _Fixture(
          limits: const OfflineStoreLimits(
            maxCacheRecords: 2048,
            maxCacheRecordsPerPartition: 2048,
          ),
        );
        await f.seed(
          1056,
        ); // 33 full pages: the production 32-page window is unchanged.
        await f.open();
        await f.tick();
        f.tails.single.bootstrap();
        await flush();
        var cursor = 1;
        final lastPage = Completer<void>();
        addTearDown(() {
          if (!lastPage.isCompleted) lastPage.complete();
        });
        f.beforeRead = () async {
          f.tails.last.invalidate(f.vertexReads.last.first, cursor: ++cursor);
          await flush(); // Reject every response under the real read-epoch fence.
          if (interruptWindow && f.vertexReads.length == 32) {
            await lastPage.future;
          }
        };
        for (var i = 0; i < 32; i++) {
          await f.tick();
        }
        expect(f.vertexReads, hasLength(32));
        final visited = f.vertexReads.expand((keys) => keys).toSet();
        expect(visited, hasLength(1024));
        expect(f.vertexReads.every((keys) => keys.length == 32), isTrue);
        if (interruptWindow) {
          final stopping = f.session!.suspend();
          await flush();
          lastPage.complete();
          await stopping;
          await f.session!.resume();
          await f.tick();
          f.tails.last.frames.add(
            wire.WatchChangesResponse(cursor: [++cursor]),
          );
          await flush();
          await f.tick();
          expect(f.vertexReads, hasLength(32));
        }
        final windowEnd = f.clock.now;
        final ticket = await f.session!.mutations.submit(
          MutationRequest.putVertices([
            const VertexInput(
              key: 'outgoing',
              value: StringValue('independent'),
            ),
          ]),
        );
        for (var i = 0; i < 5; i++) {
          if (i.isEven) {
            f.tails.last.frames.add(
              wire.WatchChangesResponse(cursor: [++cursor]),
            );
          } else {
            f.tails.last.invalidate('resident:0', cursor: ++cursor);
          }
          await flush();
          await f.tick(100);
          expect(f.vertexReads, hasLength(32));
        }
        expect(f.writes.calls, 1);
        expect(
          (await f.session!.mutations.getStatus(
            ticket.reference.operationId,
          )).items.single.state,
          MutationItemState.handled,
        );
        expect(
          (await f.session!.syncStatus.first).reachability,
          SyncReachability.idle,
        );
        await f.session!.suspend();
        await f.session!.resume();
        await f.tick();
        expect(f.tails.last.request.bootstrap, isFalse);
        f.tails.last.frames.add(wire.WatchChangesResponse(cursor: [++cursor]));
        await flush();
        await f.clock.advance(
          windowEnd
              .add(const Duration(milliseconds: 999))
              .difference(f.clock.now),
        );
        expect(f.vertexReads, hasLength(32));
        await f.tick(1);
        expect(f.vertexReads, hasLength(33));
        expect(f.vertexReads.last.any(visited.contains), isFalse);
        expect(
          (await f.cached('resident:0')).confirmed.state,
          ConfirmedState.unknown,
        );
        expect(
          await f.store.transaction(
            (tx) => tx.hasUnknownResident(
              f.store.partition,
              const OfflineEntityKey.vertex('resident:0'),
            ),
          ),
          isTrue,
        );
        // The end-of-range floor also survives hot events and wraps only on expiry.
        for (var i = 0; i < 9; i++) {
          if (i.isEven) {
            f.tails.last.frames.add(
              wire.WatchChangesResponse(cursor: [++cursor]),
            );
          } else {
            f.tails.last.invalidate('resident:0', cursor: ++cursor);
          }
          await flush();
          await f.tick(100);
          expect(f.vertexReads, hasLength(33));
        }
        await f.tick(99);
        expect(f.vertexReads, hasLength(33));
        await f.tick(1);
        expect(f.vertexReads, hasLength(34));
        expect(f.vertexReads.last, f.vertexReads.first);
        expect(
          (await f.cached('resident:0')).confirmed.state,
          ConfirmedState.unknown,
        );
        f.beforeRead = null;
        await f.tick();
        expect(f.vertexReads, hasLength(35));
        expect(_text(await f.cached(f.vertexReads.last.first)), 'new');
        expect(f.clock.active, lessThanOrEqualTo(1));
      },
    );
  }

  for (final boundary in ['gap', 'auth', 'close']) {
    test(
      'R1 $boundary fences held recovery and its pagination state',
      () async {
        await f.seed(65);
        await f.open();
        await f.tick();
        f.tails.single.bootstrap();
        await flush();
        await f.tick();
        final firstPage = f.vertexReads.single.toList();
        final held = Completer<void>(), entered = Completer<void>();
        f.beforeRead = () {
          entered.complete();
          return held.future;
        };
        await f.tick();
        await entered.future;
        final oldPage = f.vertexReads.last.toList();
        f.beforeRead = null;
        if (boundary == 'close') {
          final closing = f.session!.close();
          await flush();
          held.complete();
          await closing;
          expect(f.clock.active, 0);
          expect(
            await f.store.transaction(
              (tx) => tx.hasUnknownResident(
                f.store.partition,
                OfflineEntityKey.vertex(oldPage.first),
              ),
            ),
            isTrue,
          );
          await f.tick(30000);
          expect(f.vertexReads, hasLength(2));
          return;
        }
        f.tails.single.fail(
          boundary == 'gap'
              ? connect.Code.failedPrecondition
              : connect.Code.unauthenticated,
        );
        await flush();
        expect(await f.cursor(), isNull);
        held.complete();
        await flush();
        expect(
          (await f.session!.syncStatus.first).pause,
          boundary == 'auth' ? SyncPause.auth : SyncPause.none,
        );
        expect(
          (await f.cached(oldPage.first)).confirmed.state,
          ConfirmedState.unknown,
        );
        if (boundary == 'auth') {
          expect((await f.session!.syncStatus.first).pause, SyncPause.auth);
          expect(
            await f.session!.acceptVerifiedCredentials(
              OfflineVerifiedCredentials(binding: f.binding, install: () {}),
            ),
            isTrue,
          );
        }
        await f.tick(30000);
        expect(f.tails.last.request.bootstrap, isTrue);
        f.tails.last.bootstrap(9);
        await flush();
        await f.tick();
        expect(
          f.vertexReads[2],
          firstPage,
        ); // Old completion cannot reset/advance it.
        for (var i = 0; i < 2; i++) {
          await f.tick();
        }
        expect(f.vertexReads.skip(2).map((keys) => keys.length), [32, 32, 1]);
        expect(_text(await f.cached('resident:64')), 'new');
      },
    );
  }
}
