import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('native mobile real-wire smoke', (tester) async {
    const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
    expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
    final endpoint = Uri.parse(endpointValue);
    final client = LanternClient.connect(
      endpoint,
      allowInsecure: const bool.fromEnvironment('LANTERN_ALLOW_INSECURE'),
      retryPolicy: const RetryPolicy(),
      idempotentAdds: true,
    );
    addTearDown(client.close);
    await client.ping();

    final prefix = 'mobile-smoke:${DateTime.now().microsecondsSinceEpoch}:';
    final inputs = <VertexInput>[
      VertexInput(key: '${prefix}f64', value: VertexValue.float64(1.25)),
      VertexInput(key: '${prefix}f32', value: VertexValue.float32(2.5)),
      VertexInput(key: '${prefix}i32', value: VertexValue.int32(-7)),
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
      VertexInput(key: '${prefix}string', value: VertexValue.string('mobile')),
      VertexInput(
        key: '${prefix}bytes',
        value: VertexValue.bytes(Uint8List.fromList([0, 1, 255])),
      ),
      VertexInput(
        key: '${prefix}timestamp',
        value: VertexValue.timestamp(DateTime.parse('2026-07-12T00:00:00Z')),
      ),
      VertexInput(
        key: '${prefix}duration',
        value: VertexValue.duration(const Duration(seconds: 3)),
      ),
      VertexInput(key: '${prefix}nil', value: VertexValue.nil()),
      VertexInput(key: '${prefix}unset', value: VertexValue.unset()),
    ];
    expect((await client.putVertices(inputs)).written, inputs.length);
    expect(
      (await client.getVertices(inputs.map((input) => input.key))).missing,
      isEmpty,
    );

    final edge = EdgeInput(
      tail: '${prefix}tail',
      head: '${prefix}head',
      weight: 1,
      expiresIn: const Duration(minutes: 1),
    );
    expect(await client.addEdge(edge), 1);
    expect(
      await client.scanVertexKeys(prefix: prefix, limit: 3),
      isA<Page<String>>(),
    );
    expect(
      await client.illuminate(
        edge.tail,
        traversal: const BfsOptions(step: 1, fanOut: 2),
        vertexPrefix: prefix,
      ),
      isA<Graph>(),
    );
    // ignore: avoid_print
    print(
      'MOBILE_SMOKE_PASS vertices=${inputs.length} edge=1 scan=true bfs=true',
    );
  });
}
