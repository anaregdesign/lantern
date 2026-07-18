import 'dart:io' as io;
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/io.dart' as connect_io;
import 'package:connectrpc/protobuf.dart';
import 'package:connectrpc/protocol/connect.dart' as connect_protocol;
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
  const allowInsecure = bool.fromEnvironment('LANTERN_ALLOW_INSECURE');
  const oldToken = String.fromEnvironment('LANTERN_OLD_TOKEN');
  const newToken = String.fromEnvironment('LANTERN_NEW_TOKEN');
  final endpoint = Uri.parse(endpointValue);
  final prefix = 'physical-api:${DateTime.now().microsecondsSinceEpoch}:';
  late LanternClient client;
  var clientInitialized = false;

  setUpAll(() async {
    expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
    expect(oldToken, isNotEmpty, reason: 'pass LANTERN_OLD_TOKEN');
    expect(newToken, isNotEmpty, reason: 'pass LANTERN_NEW_TOKEN');
    client = LanternClient.connect(
      endpoint,
      token: newToken,
      allowInsecure: allowInsecure,
      retryPolicy: const RetryPolicy(),
      idempotentAdds: true,
    );
    clientInitialized = true;
    await client.ping();
  });

  tearDownAll(() {
    if (clientInitialized) client.close();
  });

  test('missing and rotated runtime tokens', () async {
    String? currentToken;
    final rotating = LanternClient.connect(
      endpoint,
      tokenProvider: () async => currentToken,
      allowInsecure: allowInsecure,
    );
    addTearDown(rotating.close);

    await expectLater(
      rotating.scanVertexKeys(prefix: prefix),
      throwsA(isA<LanternUnauthenticatedException>()),
    );
    currentToken = oldToken;
    expect(await rotating.scanVertexKeys(prefix: prefix), isA<Page<String>>());
    currentToken = newToken;
    expect(await rotating.scanVertexKeys(prefix: prefix), isA<Page<String>>());
  });

  test('every Vertex oneof preserves its exact value', () async {
    final timestamp = DateTime.parse('2026-07-12T01:02:03Z');
    const duration = Duration(seconds: -12, microseconds: -345);
    final inputs = <VertexInput>[
      VertexInput(key: '${prefix}f64', value: VertexValue.float64(1.25)),
      VertexInput(key: '${prefix}f32', value: VertexValue.float32(2.5)),
      VertexInput(key: '${prefix}i32', value: VertexValue.int32(-0x80000000)),
      VertexInput(
        key: '${prefix}i64',
        value: VertexValue.int64(-0x8000000000000000),
      ),
      VertexInput(key: '${prefix}u32', value: VertexValue.uint32(0xffffffff)),
      VertexInput(
        key: '${prefix}u64',
        value: VertexValue.uint64((BigInt.one << 64) - BigInt.one),
      ),
      VertexInput(key: '${prefix}bool', value: VertexValue.boolean(true)),
      VertexInput(key: '${prefix}string', value: VertexValue.string('lantern')),
      VertexInput(
        key: '${prefix}bytes',
        value: VertexValue.bytes(Uint8List.fromList([0, 1, 255])),
      ),
      VertexInput(
        key: '${prefix}timestamp',
        value: VertexValue.timestamp(timestamp),
      ),
      VertexInput(
        key: '${prefix}duration',
        value: VertexValue.duration(duration),
      ),
      VertexInput(key: '${prefix}nil', value: VertexValue.nil()),
      VertexInput(key: '${prefix}unset', value: VertexValue.unset()),
    ];

    expect(
      (await _withTransportDiagnostics(
        client.putVertices(inputs, batchSize: 4),
      )).written,
      13,
    );
    final result = await client.getVertices(
      inputs.map((input) => input.key),
      batchSize: 3,
    );
    expect(result.missing, isEmpty);
    final values = {
      for (final vertex in result.vertices) vertex.key: vertex.value,
    };
    expect((values['${prefix}f64'] as Float64Value).value, 1.25);
    expect((values['${prefix}f32'] as Float32Value).value, 2.5);
    expect((values['${prefix}i32'] as Int32Value).value, -0x80000000);
    expect((values['${prefix}i64'] as Int64Value).value, -0x8000000000000000);
    expect((values['${prefix}u32'] as Uint32Value).value, 0xffffffff);
    expect(
      (values['${prefix}u64'] as Uint64Value).value,
      (BigInt.one << 64) - BigInt.one,
    );
    expect((values['${prefix}bool'] as BoolValue).value, isTrue);
    expect((values['${prefix}string'] as StringValue).value, 'lantern');
    expect((values['${prefix}bytes'] as BytesValue).value, [0, 1, 255]);
    expect((values['${prefix}timestamp'] as TimestampValue).value, timestamp);
    expect((values['${prefix}duration'] as DurationValue).value, duration);
    expect(values['${prefix}nil'], isA<NilValue>());
    expect(values['${prefix}unset'], isA<UnsetValue>());
  });

  test('cursor pages stay bounded and partial failure is explicit', () async {
    final pagePrefix = '${prefix}page:';
    const count = 75;
    await _withTransportDiagnostics(
      client.putVertices(
        List.generate(
          count,
          (index) => VertexInput(
            key: '$pagePrefix${index.toString().padLeft(3, '0')}',
            value: VertexValue.int32(index),
          ),
        ),
        batchSize: count,
      ),
    );

    ScanCursor? cursor;
    var seen = 0;
    do {
      final page = await client.scanVertexKeys(
        prefix: pagePrefix,
        limit: 25,
        cursor: cursor,
      );
      expect(page.items.length, lessThanOrEqualTo(25));
      seen += page.items.length;
      cursor = page.nextCursor;
    } while (cursor != null);
    expect(seen, count);

    final committedKey = '${prefix}partial-ok';
    final invalidKey = List.filled(2048, 'x').join();
    await expectLater(
      client.putVertices([
        VertexInput(key: committedKey, value: VertexValue.nil()),
        VertexInput(key: invalidKey, value: VertexValue.nil()),
      ], batchSize: 1),
      throwsA(
        isA<BatchException>()
            .having((error) => error.committed, 'committed', 1)
            .having(
              (error) => error.cause,
              'cause',
              isA<LanternInvalidArgumentException>(),
            ),
      ),
    );
    expect((await client.getVertex(committedKey)).key, committedKey);
  });

  test(
    'committed Add response loss retries exactly one contribution',
    () async {
      final fault = _CommittedResponseLossTransport(
        endpoint,
        '/graph.v1.LanternService/AddEdges',
      );
      final retrying = LanternClient.connect(
        endpoint,
        token: newToken,
        allowInsecure: allowInsecure,
        transport: fault,
        onClose: fault.close,
        idempotentAdds: true,
        retryPolicy: const RetryPolicy(
          maxAttempts: 3,
          baseDelay: Duration(milliseconds: 1),
          maxDelay: Duration(milliseconds: 2),
        ),
      );
      addTearDown(retrying.close);
      final ref = EdgeRef('${prefix}retry-tail', '${prefix}retry-head');

      expect(
        await _withTransportDiagnostics(
          retrying.addEdge(
            EdgeInput(tail: ref.tail, head: ref.head, weight: 2),
          ),
        ),
        2,
      );
      expect((await client.getEdge(ref)).weight, 2);
      expect(fault.requestsFor('/graph.v1.LanternService/AddEdges'), 2);
    },
  );
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

final class _CommittedResponseLossTransport implements connect.Transport {
  _CommittedResponseLossTransport(Uri endpoint, this._loseProcedure) {
    _httpClient = io.HttpClient();
    _inner = connect_protocol.Transport(
      baseUrl: endpoint.toString(),
      codec: const ProtoCodec(),
      httpClient: connect_io.createHttpClient(_httpClient),
    );
  }

  late final io.HttpClient _httpClient;
  late final connect.Transport _inner;
  final String _loseProcedure;
  final Map<String, int> _requests = {};
  var _lossPending = true;

  int requestsFor(String procedure) => _requests[procedure] ?? 0;

  Future<void> close() async => _httpClient.close(force: true);

  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    _requests.update(spec.procedure, (value) => value + 1, ifAbsent: () => 1);
    final response = await _inner.unary(spec, input, options);
    if (_lossPending && spec.procedure == _loseProcedure) {
      _lossPending = false;
      throw connect.ConnectException(
        connect.Code.unavailable,
        'simulated committed response loss',
      );
    }
    return response;
  }

  @override
  Future<connect.StreamResponse<I, O>>
  stream<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    Stream<I> input, [
    connect.CallOptions? options,
  ]) => _inner.stream(spec, input, options);
}
