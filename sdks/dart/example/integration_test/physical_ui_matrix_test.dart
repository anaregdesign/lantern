import 'dart:io' as io;
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/main.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('physical UI values, discovery, and navigation cancellation', (
    tester,
  ) async {
    const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
    const tokenEndpointValue = String.fromEnvironment('LANTERN_TOKEN_ENDPOINT');
    const token = String.fromEnvironment('LANTERN_TOKEN');
    const allowInsecure = bool.fromEnvironment('LANTERN_ALLOW_INSECURE');
    expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
    expect(
      tokenEndpointValue,
      isNotEmpty,
      reason: 'pass LANTERN_TOKEN_ENDPOINT',
    );
    expect(token, isNotEmpty, reason: 'pass LANTERN_TOKEN');
    final endpoint = Uri.parse(endpointValue);
    await _preflightTokenEndpoint(tester, Uri.parse(tokenEndpointValue));
    final client = LanternClient.connect(
      endpoint,
      token: token,
      allowInsecure: allowInsecure,
    );
    addTearDown(client.close);

    tester.printToConsole('UI_MATRIX seed oneofs');
    final timestamp = DateTime.parse('2026-07-12T01:02:03Z');
    const duration = Duration(seconds: -12, microseconds: -345);
    await _withTransportDiagnostics(
      client.putVertices([
        VertexInput(
          key: 'flutter-demo:00-f64',
          value: VertexValue.float64(1.25),
        ),
        VertexInput(
          key: 'flutter-demo:01-f32',
          value: VertexValue.float32(2.5),
        ),
        VertexInput(
          key: 'flutter-demo:02-i32',
          value: VertexValue.int32(-0x80000000),
        ),
        VertexInput(
          key: 'flutter-demo:03-i64',
          value: VertexValue.int64(-0x8000000000000000),
        ),
        VertexInput(
          key: 'flutter-demo:04-u32',
          value: VertexValue.uint32(0xffffffff),
        ),
        VertexInput(
          key: 'flutter-demo:05-u64',
          value: VertexValue.uint64((BigInt.one << 64) - BigInt.one),
        ),
        VertexInput(
          key: 'flutter-demo:06-bool',
          value: VertexValue.boolean(true),
        ),
        VertexInput(
          key: 'flutter-demo:07-string',
          value: VertexValue.string('line\n"quoted"'),
        ),
        VertexInput(
          key: 'flutter-demo:08-bytes',
          value: VertexValue.bytes(Uint8List.fromList([0, 1, 255])),
        ),
        VertexInput(
          key: 'flutter-demo:09-timestamp',
          value: VertexValue.timestamp(timestamp),
        ),
        VertexInput(
          key: 'flutter-demo:10-duration',
          value: VertexValue.duration(duration),
        ),
        VertexInput(key: 'flutter-demo:11-nil', value: VertexValue.nil()),
        VertexInput(key: 'flutter-demo:12-unset', value: VertexValue.unset()),
      ]),
    );
    tester.printToConsole('UI_MATRIX seed complete');

    await tester.pumpWidget(
      LanternExampleApp(
        configuration: DemoConfiguration(
          endpoint: endpoint,
          tokenEndpoint: Uri.parse(tokenEndpointValue),
          allowInsecure: allowInsecure,
        ),
      ),
    );
    tester.printToConsole('UI_MATRIX widget attached');
    await _pumpUntilState(tester, 'ready: Visible page data');
    tester.printToConsole('UI_MATRIX discovery ready');

    final exactUint64 = find.textContaining('uint64=18446744073709551615');
    await tester.scrollUntilVisible(
      exactUint64,
      250,
      scrollable: find.byType(Scrollable).last,
    );
    expect(exactUint64, findsOneWidget);
    final exactBytes = find.textContaining('bytes=base64:AAH/');
    await tester.scrollUntilVisible(
      exactBytes,
      250,
      scrollable: find.byType(Scrollable).last,
    );
    expect(exactBytes, findsOneWidget);
    final exactDuration = find.textContaining('duration_us=-12000345');
    await tester.scrollUntilVisible(
      exactDuration,
      250,
      scrollable: find.byType(Scrollable).last,
    );
    expect(exactDuration, findsOneWidget);
    tester.printToConsole('UI_MATRIX exact values visible');

    await tester.tap(find.text('Seed CRUD'));
    await _pumpUntilState(tester, 'ready: Visible page data');
    tester.printToConsole('UI_MATRIX CRUD ready');
    await tester.enterText(find.byKey(const Key('search-field')), 'quiet cafe');
    await _pumpUntilState(tester, 'ready: Latest search results');
    tester.printToConsole('UI_MATRIX incremental search ready');

    await tester.tap(find.text('BFS'));
    await _pumpUntilState(tester, 'ready: BfsOptions graph');
    expect(find.textContaining('expires='), findsWidgets);
    await tester.tap(find.text('PPR'));
    await _pumpUntilState(tester, 'ready: PprOptions graph');
    await tester.tap(find.text('Community'));
    await _pumpUntilState(tester, 'ready: LocalCommunityOptions graph');
    tester.printToConsole('UI_MATRIX traversal families ready');

    await tester.enterText(find.byKey(const Key('search-field')), 'quiet');
    await tester.pump(const Duration(milliseconds: 20));
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump(const Duration(seconds: 1));
    expect(tester.takeException(), isNull);
    tester.printToConsole('UI_MATRIX navigation cancellation ready');
  });
}

Future<void> _preflightTokenEndpoint(WidgetTester tester, Uri endpoint) async {
  final http = io.HttpClient()..connectionTimeout = const Duration(seconds: 5);
  try {
    final request = await http
        .getUrl(endpoint)
        .timeout(const Duration(seconds: 5));
    final response = await request.close().timeout(const Duration(seconds: 5));
    await response.drain<void>().timeout(const Duration(seconds: 5));
    expect(response.statusCode, io.HttpStatus.ok);
    tester.printToConsole('UI_MATRIX token BFF preflight ready');
  } finally {
    http.close(force: true);
  }
}

Future<T> _withTransportDiagnostics<T>(Future<T> operation) async {
  try {
    return await operation;
  } on Object catch (error) {
    printOnFailure('transport cause: ${_causeChain(error).join(' -> ')}');
    rethrow;
  }
}

List<Object> _causeChain(Object error) {
  final chain = <Object>[error];
  Object? current = error;
  while (true) {
    current = switch (current) {
      BatchException(:final cause) => cause,
      LanternRetryExhaustedException(:final cause) => cause,
      LanternException(:final cause) => cause,
      connect.ConnectException(:final cause) => cause,
      _ => null,
    };
    if (current == null || identical(current, chain.last)) return chain;
    chain.add(current);
  }
}

Future<void> _pumpUntilState(
  WidgetTester tester,
  String expected, {
  int attempts = 150,
}) async {
  for (var attempt = 0; attempt < attempts; attempt++) {
    await tester.runAsync(
      () => Future<void>.delayed(const Duration(milliseconds: 100)),
    );
    await tester.pump();
    if (find.text(expected).evaluate().isNotEmpty) return;
  }
  fail('timed out waiting for UI state: $expected');
}
