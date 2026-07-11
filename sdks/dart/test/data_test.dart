import 'dart:typed_data';

import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/duration.pb.dart'
    as wkt_duration;
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart'
    as wkt_timestamp;
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('all exact value kinds encode without guessing or collapsing', () async {
    late List<graph.Vertex> encoded;
    final transport =
        FakeTransportBuilder().unary<
          graph.PutVerticesRequest,
          graph.PutVerticesResponse
        >(LanternService.putVertices, (request, context) {
          encoded = request.vertices.toList();
          return graph.PutVerticesResponse(written: request.vertices.length);
        }).build();
    final client = LanternClient.connect(
      Uri.parse('https://example.test'),
      transport: transport,
    );

    final result = await client.putVertices([
      VertexInput(key: 'f64', value: VertexValue.float64(1.25)),
      VertexInput(key: 'f32', value: VertexValue.float32(2.5)),
      VertexInput(key: 'i32', value: VertexValue.int32(-0x80000000)),
      VertexInput(key: 'i64', value: VertexValue.int64(-0x8000000000000000)),
      VertexInput(key: 'u32', value: VertexValue.uint32(0xffffffff)),
      VertexInput(
        key: 'u64',
        value: VertexValue.uint64((BigInt.one << 64) - BigInt.one),
      ),
      VertexInput(key: 'bool', value: VertexValue.boolean(true)),
      VertexInput(key: 'string', value: VertexValue.string('lantern')),
      VertexInput(
        key: 'bytes',
        value: VertexValue.bytes(Uint8List.fromList([1, 2, 3])),
      ),
      VertexInput(
        key: 'timestamp',
        value: VertexValue.timestamp(DateTime.parse('2026-07-12T01:02:03Z')),
      ),
      VertexInput(
        key: 'duration',
        value: VertexValue.duration(
          const Duration(seconds: -12, microseconds: -345),
        ),
      ),
      VertexInput(key: 'nil', value: VertexValue.nil()),
      VertexInput(key: 'unset', value: VertexValue.unset()),
    ]);

    expect(result.written, 13);
    expect(encoded.map((value) => value.whichValue()), [
      graph.Vertex_Value.float64,
      graph.Vertex_Value.float32,
      graph.Vertex_Value.int32,
      graph.Vertex_Value.int64,
      graph.Vertex_Value.uint32,
      graph.Vertex_Value.uint64,
      graph.Vertex_Value.bool_16,
      graph.Vertex_Value.string,
      graph.Vertex_Value.bytes,
      graph.Vertex_Value.timestamp,
      graph.Vertex_Value.duration,
      graph.Vertex_Value.nil,
      graph.Vertex_Value.notSet,
    ]);
    expect(encoded[2].int32, -0x80000000);
    expect(encoded[3].int64, Int64.MIN_VALUE);
    expect(encoded[4].uint32, 0xffffffff);
    expect(encoded[5].uint64, Int64(-1));
    expect(encoded[8].bytes, [1, 2, 3]);
    expect(encoded[9].timestamp.toDateTime().isUtc, isTrue);
    expect(encoded[10].duration.seconds, Int64(-12));
    expect(encoded[10].duration.nanos, -345000);
    expect(encoded[11].nil, isTrue);
  });

  test('all exact value kinds decode including nil versus unset', () async {
    final timestamp = wkt_timestamp.Timestamp.fromDateTime(
      DateTime.parse('2026-07-12T01:02:03.456Z'),
    );
    final transport =
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (request, context) => graph.GetVerticesResponse(
                vertices: [
                  graph.Vertex(key: 'f64', float64: 1.25),
                  graph.Vertex(key: 'f32', float32: 2.5),
                  graph.Vertex(key: 'i32', int32: -7),
                  graph.Vertex(key: 'i64', int64: Int64.MIN_VALUE),
                  graph.Vertex(key: 'u32', uint32: 0xffffffff),
                  graph.Vertex(key: 'u64', uint64: Int64(-1)),
                  graph.Vertex(key: 'bool', bool_16: true),
                  graph.Vertex(key: 'string', string: 'lantern'),
                  graph.Vertex(key: 'bytes', bytes: [1, 2, 3]),
                  graph.Vertex(key: 'timestamp', timestamp: timestamp),
                  graph.Vertex(
                    key: 'duration',
                    duration: wkt_duration.Duration(
                      seconds: Int64(12),
                      nanos: 345000,
                    ),
                  ),
                  graph.Vertex(key: 'nil', nil: true),
                  graph.Vertex(key: 'unset'),
                ],
              ),
            )
            .build();
    final client = LanternClient.connect(
      Uri.parse('https://example.test'),
      transport: transport,
    );

    final result = await client.getVertices(List.filled(13, 'unused'));
    expect(result.vertices[0].value, isA<Float64Value>());
    expect(result.vertices[1].value, isA<Float32Value>());
    expect(result.vertices[2].value, isA<Int32Value>());
    expect(result.vertices[3].value, isA<Int64Value>());
    expect(result.vertices[4].value, isA<Uint32Value>());
    expect(
      (result.vertices[5].value as Uint64Value).value,
      (BigInt.one << 64) - BigInt.one,
    );
    expect(result.vertices[6].value, isA<BoolValue>());
    expect(result.vertices[7].value, isA<StringValue>());
    expect(result.vertices[8].value, isA<BytesValue>());
    expect(
      (result.vertices[9].value as TimestampValue).value,
      DateTime.parse('2026-07-12T01:02:03.456Z'),
    );
    expect(
      (result.vertices[10].value as DurationValue).value,
      const Duration(seconds: 12, microseconds: 345),
    );
    expect(result.vertices[11].value, isA<NilValue>());
    expect(result.vertices[12].value, isA<UnsetValue>());
  });

  test('bytes and contribution IDs are defensively owned', () {
    final source = Uint8List.fromList([1, 2, 3]);
    final bytes = VertexValue.bytes(source) as BytesValue;
    source[0] = 9;
    expect(bytes.value, [1, 2, 3]);
    final returned = bytes.value..[1] = 9;
    expect(returned, [1, 9, 3]);
    expect(bytes.value, [1, 2, 3]);

    final id = Uint8List(24)..[0] = 1;
    final edge = EdgeInput(tail: 'a', head: 'b', weight: 1, contribId: id);
    id[0] = 9;
    expect(edge.contribId?.first, 1);
    final returnedId = edge.contribId!..[0] = 8;
    expect(returnedId.first, 8);
    expect(edge.contribId?.first, 1);
  });

  test(
    'numeric, floating point, timestamp, and duration bounds fail locally',
    () {
      final invalid = <Object? Function()>[
        () => VertexValue.float64(double.nan),
        () => VertexValue.float32(double.infinity),
        () => VertexValue.int32(0x80000000),
        () => VertexValue.uint32(-1),
        () => VertexValue.uint64(BigInt.one << 64),
        () => VertexValue.timestamp(DateTime.utc(10000)),
        () => VertexValue.duration(
          const Duration(microseconds: 315576000000000001),
        ),
      ];
      for (final create in invalid) {
        expect(create, throwsA(isA<LanternInvalidArgumentException>()));
      }
    },
  );

  test('non-finite wire values fail closed as server errors', () async {
    final transport =
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (request, context) => graph.GetVerticesResponse(
                vertices: [graph.Vertex(key: 'bad', float64: double.nan)],
              ),
            )
            .build();
    final client = LanternClient.connect(
      Uri.parse('https://example.test'),
      transport: transport,
    );

    await expectLater(
      client.getVertex('bad'),
      throwsA(isA<LanternInternalException>()),
    );
  });
}
