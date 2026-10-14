import 'dart:async';
import 'dart:convert';
import 'dart:io' as io;
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/protobuf.dart' as codec;
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/data_demo.dart';

// The root Go integration gate supplies an ephemeral, trusted TLS fixture.
// No live provider credentials or physical-device acceptance are involved.
class _WireBinding extends AutomatedTestWidgetsFlutterBinding {
  @override
  bool get overrideHttpClient => false;
}

void main() {
  final url = io.Platform.environment['LANTERN_DEMO_WIRE_URL'];
  if (url != null) _WireBinding();

  Future<void> show(
    WidgetTester tester,
    LanternClient client, {
    Future<void> Function()? refresh,
  }) async {
    addTearDown(client.close);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: DataDemoPanel(
            client: client,
            prefix: 'flutter-demo:',
            callOptions: () =>
                LanternCallOptions(timeout: const Duration(seconds: 5)),
            onDataChanged: refresh ?? () async {},
          ),
        ),
      ),
    );
  }

  testWidgets('denied optional metadata never blocks or erases data/search', (
    tester,
  ) async {
    final wire = _DemoWire();
    var refreshes = 0;
    await show(
      tester,
      wire.client(),
      refresh: () async {
        refreshes++;
      },
    );
    await tester.tap(find.text('Server search info'));
    await tester.pumpAndSettle();
    expect(find.textContaining('operations.read is required'), findsOneWidget);
    await tester.tap(find.text('Seed CRUD'));
    await tester.pumpAndSettle();
    expect(
      find.text('Seed CRUD completed'),
      findsOneWidget,
      reason: tester
          .widget<Text>(find.byKey(const Key('data-demo-result')))
          .data,
    );
    expect(find.textContaining('Delete temporary vertex'), findsOneWidget);
    expect(wire.calls.where((c) => c == 'GetServerStatus'), hasLength(1));
    expect(wire.calls.where((c) => c == 'SearchVertices'), hasLength(3));
    expect(refreshes, 1);
    await tester.tap(find.text('Server search info'));
    await tester.pumpAndSettle();
    expect(find.text('Seed CRUD completed'), findsOneWidget);
    expect(
      find.textContaining('Add sample edge; Read sample edge'),
      findsOneWidget,
    );
  });

  for (final failure in [
    'SearchVertices',
    'AddEdges',
    'DeleteVertices',
    'PagedContinuation',
  ]) {
    testWidgets(
      '$failure failure preserves progress and cannot replay writes',
      (tester) async {
        final wire = _DemoWire()..failure = failure;
        await show(tester, wire.client());
        await tester.tap(find.text('Seed CRUD'));
        await tester.pumpAndSettle();
        expect(find.textContaining('Stopped at'), findsOneWidget);
        expect(
          find.textContaining(
            'Put sample vertices; Read sample vertex; Put sample edge',
          ),
          findsOneWidget,
        );
        expect(find.text('Seed CRUD completed'), findsNothing);
        expect(wire.calls, isNot(contains('GetServerStatus')));
        if (failure == 'PagedContinuation') {
          expect(
            find.textContaining('Stopped at Paged search:'),
            findsOneWidget,
          );
          expect(wire.calls.where((c) => c == 'SearchVertices'), hasLength(4));
          expect(wire.calls, isNot(contains('DeleteVertices')));
        }
        if (failure == 'AddEdges') {
          expect(wire.calls, isNot(contains('GetEdges')));
          expect(
            find.textContaining('Confirmed:').evaluate().single.widget,
            isA<Text>().having(
              (t) => t.data,
              'confirmed steps',
              isNot(contains('Add sample edge')),
            ),
          );
        }
        expect(
          tester
              .widget<FilledButton>(
                find.widgetWithText(FilledButton, 'Seed CRUD'),
              )
              .onPressed,
          isNull,
        );
        await tester.tap(find.text('Seed CRUD'));
        await tester.pumpAndSettle();
        expect(wire.calls.where((c) => c == 'AddEdges'), hasLength(1));
      },
    );
  }

  testWidgets('mixed Put results retain confirmed items without continuing', (
    tester,
  ) async {
    final wire = _DemoWire()..mixedPut = true;
    await show(tester, wire.client());
    await tester.tap(find.text('Seed CRUD'));
    await tester.pumpAndSettle();
    expect(
      find.textContaining('Stopped at Put sample vertices:'),
      findsOneWidget,
    );
    expect(
      find.text('Confirmed: Put vertex flutter-demo:alice'),
      findsOneWidget,
    );
    expect(wire.calls, ['PutVertices']);
  });

  for (final positions in [false, true]) {
    testWidgets('metadata-capable path respects positions=$positions', (
      tester,
    ) async {
      final wire = _DemoWire()
        ..statusAllowed = true
        ..positions = positions;
      await show(tester, wire.client());
      await tester.tap(find.text('Server search info'));
      await tester.pumpAndSettle();
      expect(find.textContaining('positions=$positions'), findsOneWidget);
      expect(wire.phrases, positions ? [true] : isEmpty);
      expect(wire.calls, isNot(contains('PutVertices')));
    });
  }

  testWidgets('a denied phrase query is a query failure, not metadata denial', (
    tester,
  ) async {
    final wire = _DemoWire()
      ..statusAllowed = true
      ..positions = true
      ..failure = 'SearchVertices';
    await show(tester, wire.client());
    await tester.tap(find.text('Server search info'));
    await tester.pumpAndSettle();
    expect(find.textContaining('Phrase search failed:'), findsOneWidget);
    expect(find.textContaining('operations.read is required'), findsNothing);
  });

  testWidgets('held Add prevents overlap and disposal prevents later work', (
    tester,
  ) async {
    final wire = _DemoWire()..heldAdd = Completer<void>();
    await show(tester, wire.client());
    await tester.tap(find.text('Seed CRUD'));
    await tester.pumpAndSettle();
    expect(wire.calls.last, 'AddEdges');
    expect(
      tester
          .widget<OutlinedButton>(
            find.widgetWithText(OutlinedButton, 'Server search info'),
          )
          .onPressed,
      isNull,
    );
    await tester.tap(find.text('Seed CRUD'));
    await tester.pumpWidget(const SizedBox.shrink());
    wire.heldAdd!.complete();
    await tester.pumpAndSettle();
    expect(wire.calls.last, 'AddEdges');
    expect(wire.calls.where((c) => c == 'AddEdges'), hasLength(1));
    expect(tester.takeException(), isNull);
  });

  for (final subject in ['demo', 'query-denied', 'data-denied']) {
    testWidgets('real Connect demo for $subject', (tester) async {
      final context = io.SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates(
          io.Platform.environment['LANTERN_DEMO_WIRE_CA']!,
        );
      final client = LanternClient.connect(
        Uri.parse(url!),
        httpClientFactory: () => io.HttpClient(context: context),
        tokenProvider: () =>
            io.Platform.environment['LANTERN_DEMO_WIRE_$subject'],
      );
      await show(tester, client);
      await tester.runAsync(() => tester.tap(find.text('Server search info')));
      await _waitFor(tester, 'operations.read is required');
      await tester.runAsync(() => tester.tap(find.text('Seed CRUD')));
      await _waitFor(tester, switch (subject) {
        'demo' => 'Seed CRUD completed',
        'query-denied' => 'Stopped at Search sample vertices:',
        _ => 'Stopped at Put sample vertices:',
      });
      if (subject != 'data-denied') {
        expect(
          find.textContaining('Add sample edge; Read sample edge'),
          findsOneWidget,
        );
      }
      expect(
        tester
            .widget<FilledButton>(
              find.widgetWithText(FilledButton, 'Seed CRUD'),
            )
            .onPressed,
        isNull,
      );
      if (subject == 'demo') {
        await tester.runAsync(() async {
          expect(
            (await client.getEdge(
              const EdgeRef('flutter-demo:alice', 'flutter-demo:bob'),
            )).weight,
            1.5,
          );
          await expectLater(
            client.getVertex('flutter-demo:temporary'),
            throwsA(isA<LanternNotFoundException>()),
          );
        });
      }
    }, skip: url == null);
  }
}

Future<void> _waitFor(WidgetTester tester, String text) async {
  for (var i = 0; i < 150; i++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 50)),
    );
    await tester.pump();
    if (find.textContaining(text).evaluate().isNotEmpty) return;
  }
  fail(
    'Timed out waiting for $text; panel: '
    '${tester.widget<Text>(find.byKey(const Key('data-demo-result'))).data}; '
    '${tester.widget<Text>(find.byKey(const Key('data-demo-metadata'))).data}',
  );
}

// Explicit public Connect JSON fixtures; generated SDK classes stay private.
final class _DemoWire implements connect.Transport {
  final calls = <String>[], phrases = <bool>[];
  bool statusAllowed = false, positions = false, mixedPut = false;
  String? failure;
  Completer<void>? heldAdd;
  LanternClient client() =>
      LanternClient.connect(Uri.parse('https://example.test'), transport: this);

  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    // Respect the public transport cancellation contract before dispatch.
    await Future.any<void>([
      if (options?.signal case final signal?)
        signal.future.then<void>((error) => throw error),
      Future<void>.delayed(Duration.zero),
    ]);
    const json = codec.JsonCodec();
    final req =
        jsonDecode(utf8.decode(json.encode(input))) as Map<String, dynamic>;
    final method = spec.procedure.split('/').last;
    calls.add(method);
    if (method == 'SearchVertices') {
      phrases.add((req['options'] as Map?)?['phrase'] == true);
    }
    if (method == 'AddEdges') await heldAdd?.future;
    if (method == failure ||
        method == 'GetServerStatus' && !statusAllowed ||
        failure == 'PagedContinuation' && req.containsKey('cursor')) {
      throw connect.ConnectException(
        method == 'AddEdges'
            ? connect.Code.unavailable
            : connect.Code.permissionDenied,
        'fixture failure',
      );
    }
    final Map<String, Object?> response = switch (method) {
      'PutVertices' => {
        'outcomes': mixedPut ? [1, 4] : [for (final _ in req['vertices']) 1],
      },
      'GetVertices' => {
        'vertices': [
          for (final key in req['keys'])
            {'key': key, 'string': 'quiet cafe graph'},
        ],
      },
      'PutEdges' => {
        'outcomes': [1],
      },
      'AddEdges' => {
        'written': 1,
        'effectiveWeights': [1.5],
      },
      'GetEdges' => {
        'edges': [
          {
            'tail': 'flutter-demo:alice',
            'head': 'flutter-demo:bob',
            'weight': 1.5,
          },
        ],
      },
      'SearchVertices' =>
        failure == 'PagedContinuation' && req['limit'] == 2
            ? {
                'hits': [
                  {'key': 'flutter-demo:alice', 'score': 1},
                ],
                'nextCursor': 'AQ==',
                'effectiveLimit': 2,
              }
            : {'hits': []},
      'DeleteVertices' => {
        'deleted': 1,
        'existed': [true],
      },
      'GetServerStatus' => {
        'search': {'enabled': true, 'positionsEnabled': positions},
      },
      _ => throw StateError('Unexpected RPC $method'),
    };
    final output = spec.outputFactory();
    json.decode(Uint8List.fromList(utf8.encode(jsonEncode(response))), output);
    return connect.UnaryResponse(
      spec,
      connect.Headers(),
      output,
      connect.Headers(),
    );
  }

  @override
  Future<connect.StreamResponse<I, O>>
  stream<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    Stream<I> input, [
    connect.CallOptions? options,
  ]) => throw UnimplementedError();
}
