import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_example/offline_demo.dart';

void main() {
  const partition = 'widget-session';
  const vertexKey = 'widget:profile';
  const edgeTail = 'widget:profile';
  const edgeHead = 'widget:counter';

  testWidgets('shows cache immediately and local Put pending values', (
    tester,
  ) async {
    final remote = _FakeRemote()
      ..vertices[vertexKey] = Vertex(
        key: vertexKey,
        value: VertexValue.string('cached'),
        expiration: null,
      );
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
    );
    addTearDown(repository.dispose);
    await repository.readVertex(
      partition,
      vertexKey,
      policy: OfflineReadPolicy.serverOnly,
    );
    remote.vertexGetFailures.add(_unavailable());

    await _pumpOfflineDemo(
      tester,
      repository,
      partition: partition,
      vertexKey: vertexKey,
      edgeTail: edgeTail,
      edgeHead: edgeHead,
    );

    expect(find.text('cached'), findsOneWidget);
    expect(_chipText(tester, const Key('offline-vertex-pending')), 'confirmed');

    await tester.enterText(
      find.byKey(const Key('offline-value-field')),
      'local',
    );
    await tester.tap(find.byKey(const Key('offline-save-local')));
    await tester.pumpAndSettle();
    expect(
      tester.widget<Text>(find.byKey(const Key('offline-vertex-value'))).data,
      'local',
    );
    expect(_chipText(tester, const Key('offline-vertex-pending')), 'pending');

    final saveEdge = find.byKey(const Key('offline-save-edge-local'));
    await tester.ensureVisible(saveEdge);
    await tester.tap(saveEdge);
    await tester.pumpAndSettle();
    expect(_chipText(tester, const Key('offline-edge-pending')), 'pending');
    expect(
      tester.widget<Text>(find.byKey(const Key('offline-edge-value'))).data,
      '0.25',
    );
  });

  testWidgets('replay changes a local Put from pending to confirmed', (
    tester,
  ) async {
    final remote = _FakeRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
    );
    addTearDown(repository.dispose);
    await _pumpOfflineDemo(
      tester,
      repository,
      partition: partition,
      vertexKey: vertexKey,
      edgeTail: edgeTail,
      edgeHead: edgeHead,
    );

    await tester.enterText(
      find.byKey(const Key('offline-value-field')),
      'confirmed-value',
    );
    await tester.tap(find.byKey(const Key('offline-save-local')));
    await tester.pumpAndSettle();
    expect(_chipText(tester, const Key('offline-vertex-pending')), 'pending');

    await tester.tap(find.byKey(const Key('offline-replay')));
    await tester.pumpAndSettle();

    expect(_chipText(tester, const Key('offline-vertex-pending')), 'confirmed');
    await _scroll(tester, -300);
    expect(find.text('Last write: confirmed'), findsOneWidget);
    expect(
      (remote.vertices[vertexKey]!.value as StringValue).value,
      'confirmed-value',
    );
  });

  testWidgets(
    'dead letters require inspection consent and can retry or delete',
    (tester) async {
      final remote = _FakeRemote();
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: remote,
      );
      addTearDown(repository.dispose);
      await _pumpOfflineDemo(
        tester,
        repository,
        partition: partition,
        vertexKey: vertexKey,
        edgeTail: edgeTail,
        edgeHead: edgeHead,
      );

      await tester.tap(find.byKey(const Key('offline-save-local')));
      await tester.pumpAndSettle();
      remote.vertexPutFailures.add(_invalid());
      await tester.tap(find.byKey(const Key('offline-replay')));
      await tester.pumpAndSettle();
      await _scroll(tester, -500);
      expect(find.text('Dead letters (1)'), findsOneWidget);

      await tester.ensureVisible(find.byTooltip('Inspect'));
      await tester.tap(find.byTooltip('Inspect'));
      await tester.pumpAndSettle();
      expect(find.text('Inspect sensitive local intent?'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, 'Inspect'));
      await tester.pumpAndSettle();
      expect(
        find.text('Authorized intent category: putVertex'),
        findsOneWidget,
      );

      await tester.tap(find.byTooltip('Retry'));
      await tester.pumpAndSettle();
      expect(find.text('Dead letters (0)'), findsOneWidget);
      await _scroll(tester, 600);
      await tester.tap(find.byKey(const Key('offline-replay')));
      await tester.pumpAndSettle();
      expect(
        _chipText(tester, const Key('offline-vertex-pending')),
        'confirmed',
      );
      await _scroll(tester, -300);
      expect(
        tester.widget<Text>(find.byKey(const Key('offline-message'))).data,
        'Replay confirmed 1 item(s)',
      );

      await _scroll(tester, 400);
      await tester.enterText(
        find.byKey(const Key('offline-value-field')),
        'delete-me',
      );
      await tester.tap(find.byKey(const Key('offline-save-local')));
      await tester.pumpAndSettle();
      remote.vertexPutFailures.add(_invalid());
      await tester.tap(find.byKey(const Key('offline-replay')));
      await tester.pumpAndSettle();
      await _scroll(tester, -500);
      expect(find.text('Dead letters (1)'), findsOneWidget);

      await tester.ensureVisible(find.byTooltip('Delete'));
      await tester.tap(find.byTooltip('Delete'));
      await tester.pumpAndSettle();
      expect(find.text('Dead letters (0)'), findsOneWidget);
    },
  );

  testWidgets('unknown snapshots display no source instead of local overlay', (
    tester,
  ) async {
    final remote = _FakeRemote()..vertexGetFailures.add(_unavailable());
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
    );
    addTearDown(repository.dispose);

    await _pumpOfflineDemo(
      tester,
      repository,
      partition: partition,
      vertexKey: vertexKey,
      edgeTail: edgeTail,
      edgeHead: edgeHead,
    );

    expect(
      tester.widget<Text>(find.byKey(const Key('offline-vertex-state'))).data,
      'unknown / no-source',
    );
  });

  testWidgets('lifecycle pause suppresses cancellation and resume probes', (
    tester,
  ) async {
    final remote = _FakeRemote();
    final repository = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: remote,
    );
    addTearDown(repository.dispose);
    await _pumpOfflineDemo(
      tester,
      repository,
      partition: partition,
      vertexKey: vertexKey,
      edgeTail: edgeTail,
      edgeHead: edgeHead,
    );

    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await repository.putVertex(
      partitionId: partition,
      input: VertexInput(key: vertexKey, value: VertexValue.string('paused')),
    );
    await tester.pump();
    expect(find.text('Foreground replay canceled'), findsNothing);

    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();
    expect(remote.probeCalls, 1);
    expect(find.text('Foreground replay canceled'), findsNothing);

    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.detached);
    await tester.pump();
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'foreground CDC starts once, resumes, and stays off after logout',
    (tester) async {
      final source = _TrackingIdentitySource();
      final repository = OfflineLanternRepository(
        store: InMemoryOfflineStore(),
        remote: _FakeRemote(),
      );
      addTearDown(repository.dispose);
      var signedIn = true;
      await _pumpOfflineDemo(
        tester,
        repository,
        partition: partition,
        vertexKey: vertexKey,
        edgeTail: edgeTail,
        edgeHead: edgeHead,
        identitySource: source,
        identityAllowed: () => signedIn,
      );
      await _pumpUntil(tester, () => source.sessions.length == 1);
      expect(source.sessions.single.listening, isTrue);
      source.sessions.single.add(OfflineIdentityCheckpoint(const {}));
      await _pumpUntilAsync(
        tester,
        () async => await repository.store.transaction((t) async {
          final epoch = await t.changeEpoch(partition);
          final unknown = await t.unknownResidents(partition, limit: 1);
          return epoch > 0 && unknown.isEmpty;
        }),
      );

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      await tester.pump();
      expect(source.sessions.single.closed, isFalse);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      expect(source.tokens.single.isCanceled, isTrue);
      await _waitReal(tester, () => source.sessions.single.closed);

      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await _pumpUntil(tester, () => source.sessions.length == 2);
      expect(source.sessions.last.listening, isTrue);
      source.sessions.last.add(OfflineIdentityCheckpoint(const {}));
      await _pumpUntilAsync(
        tester,
        () async => await repository.store.transaction((t) async {
          final epoch = await t.changeEpoch(partition);
          final unknown = await t.unknownResidents(partition, limit: 1);
          return epoch > 1 && unknown.isEmpty;
        }),
      );

      signedIn = false;
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
      await _waitReal(tester, () => source.sessions.last.closed);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pump();
      expect(source.sessions, hasLength(2));
      expect(tester.takeException(), isNull);
    },
  );
}

Future<void> _pumpOfflineDemo(
  WidgetTester tester,
  OfflineLanternRepository repository, {
  required String partition,
  required String vertexKey,
  required String edgeTail,
  required String edgeHead,
  OfflineIdentitySource? identitySource,
  bool Function()? identityAllowed,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      home: OfflineDemoScreen(
        repository: repository,
        partitionId: partition,
        vertexKey: vertexKey,
        edgeTail: edgeTail,
        edgeHead: edgeHead,
        identitySource: identitySource,
        identityAllowed: identityAllowed,
      ),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> _pumpUntil(WidgetTester tester, bool Function() done) async {
  for (var attempt = 0; attempt < 100; attempt++) {
    await tester.pump(const Duration(milliseconds: 10));
    if (done()) return;
  }
  fail('foreground CDC lifecycle did not settle');
}

Future<void> _pumpUntilAsync(
  WidgetTester tester,
  Future<bool> Function() done,
) async {
  for (var attempt = 0; attempt < 100; attempt++) {
    await tester.pump(const Duration(milliseconds: 10));
    if (await done()) return;
  }
  fail('foreground CDC did not apply its checkpoint');
}

Future<void> _waitReal(WidgetTester tester, bool Function() done) async {
  await tester.runAsync(() async {
    final deadline = DateTime.now().add(const Duration(seconds: 2));
    while (!done()) {
      if (DateTime.now().isAfter(deadline)) {
        fail('foreground CDC cancellation did not settle');
      }
      await Future<void>.delayed(const Duration(milliseconds: 1));
    }
  });
}

Future<void> _scroll(WidgetTester tester, double dy) async {
  await tester.drag(find.byKey(const Key('offline-demo-list')), Offset(0, dy));
  await tester.pumpAndSettle();
}

String? _chipText(WidgetTester tester, Key key) {
  final chip = tester.widget<Chip>(find.byKey(key));
  return (chip.label as Text).data;
}

OfflineRemoteFailure _unavailable() => OfflineRemoteFailure(
  OfflineRemoteErrorKind.unavailable,
  StateError('offline'),
);

OfflineRemoteFailure _invalid() => OfflineRemoteFailure(
  OfflineRemoteErrorKind.invalidArgument,
  StateError('invalid'),
);

final class _FakeRemote implements OfflineRemote {
  final Map<String, Vertex> vertices = <String, Vertex>{};
  final Map<EdgeRef, Edge> edges = <EdgeRef, Edge>{};
  final List<OfflineRemoteFailure> vertexGetFailures = [];
  final List<OfflineRemoteFailure> vertexPutFailures = [];
  var probeCalls = 0;

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) async {
    probeCalls += 1;
  }

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    if (vertexGetFailures.isNotEmpty) throw vertexGetFailures.removeAt(0);
    final vertex = vertices[key];
    return vertex == null
        ? const OfflineRemoteMissing<Vertex>()
        : OfflineRemotePresent<Vertex>(vertex);
  }

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) async {
    final value = edges[edge];
    return value == null
        ? const OfflineRemoteMissing<Edge>()
        : OfflineRemotePresent<Edge>(value);
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    if (vertexPutFailures.isNotEmpty) throw vertexPutFailures.removeAt(0);
    vertices[vertex.key] = vertex;
    return PutOutcome.appliedAndLive;
  }

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    edges[EdgeRef(edge.tail, edge.head)] = edge;
    return PutOutcome.appliedAndLive;
  }
}

final class _TrackingIdentitySource implements OfflineIdentitySource {
  final List<_TrackingIdentitySession> sessions = [];
  final List<LanternCancellationToken> tokens = [];

  @override
  Future<OfflineIdentitySession> open({
    required bool bootstrap,
    required Map<String, BigInt> nextExpected,
    required LanternCancellationToken cancellation,
  }) async {
    final session = _TrackingIdentitySession();
    sessions.add(session);
    tokens.add(cancellation);
    return session;
  }
}

final class _TrackingIdentitySession implements OfflineIdentitySession {
  _TrackingIdentitySession() {
    _frames = StreamController<OfflineIdentityEvent>(
      sync: true,
      onListen: () => listening = true,
    );
  }

  late final StreamController<OfflineIdentityEvent> _frames;
  bool listening = false;
  bool closed = false;

  void add(OfflineIdentityEvent frame) => _frames.add(frame);

  @override
  String get responderId => 'test-responder';

  @override
  Stream<OfflineIdentityEvent> get events => _frames.stream;

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) async => [for (final _ in keys) const OfflineRemoteMissing<Vertex>()];

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
      await _frames.close();
    } else {
      unawaited(_frames.close());
    }
  }
}
