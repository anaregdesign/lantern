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
}

Future<void> _pumpOfflineDemo(
  WidgetTester tester,
  OfflineLanternRepository repository, {
  required String partition,
  required String vertexKey,
  required String edgeTail,
  required String edgeHead,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      home: OfflineDemoScreen(
        repository: repository,
        partitionId: partition,
        vertexKey: vertexKey,
        edgeTail: edgeTail,
        edgeHead: edgeHead,
      ),
    ),
  );
  await tester.pumpAndSettle();
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
  Future<void> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    if (vertexPutFailures.isNotEmpty) throw vertexPutFailures.removeAt(0);
    vertices[vertex.key] = vertex;
  }

  @override
  Future<void> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    edges[EdgeRef(edge.tail, edge.head)] = edge;
  }
}
