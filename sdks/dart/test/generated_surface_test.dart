import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/src/gen/google/protobuf/duration.pb.dart'
    as wkt_duration;
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart'
    as wkt_timestamp;
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('internal generated graph value types compile and round-trip', () {
    final vertices = <graph.Vertex>[
      graph.Vertex(key: 'float64', float64: 1.25),
      graph.Vertex(key: 'float32', float32: 2.5),
      graph.Vertex(key: 'int32', int32: -3),
      graph.Vertex(key: 'int64', int64: Int64.parseInt('-9223372036854775808')),
      graph.Vertex(key: 'uint32', uint32: 4_294_967_295),
      graph.Vertex(key: 'uint64', uint64: Int64(-1)),
    ];
    const expectedKinds = <graph.Vertex_Value>[
      graph.Vertex_Value.float64,
      graph.Vertex_Value.float32,
      graph.Vertex_Value.int32,
      graph.Vertex_Value.int64,
      graph.Vertex_Value.uint32,
      graph.Vertex_Value.uint64,
    ];

    for (var index = 0; index < vertices.length; index++) {
      final roundTrip = graph.Vertex.fromBuffer(
        vertices[index].writeToBuffer(),
      );
      expect(roundTrip.whichValue(), expectedKinds[index]);
    }

    final edge = graph.Edge(tail: 'tail', head: 'head', weight: 1.5);
    final value = graph.Graph(vertices: vertices, edges: [edge]);
    final roundTrip = graph.Graph.fromBuffer(value.writeToBuffer());
    expect(roundTrip.vertices, hasLength(vertices.length));
    expect(roundTrip.edges.single.tail, 'tail');
    expect(roundTrip.edges.single.head, 'head');
  });

  test('generated timestamp and duration Well-Known Types round-trip', () {
    final timestamp = wkt_timestamp.Timestamp(
      seconds: Int64(1_234_567),
      nanos: 890,
    );
    final duration = wkt_duration.Duration(seconds: Int64(-12), nanos: -345);
    final timestampVertex = graph.Vertex(
      key: 'timestamp',
      timestamp: timestamp,
    );
    final durationVertex = graph.Vertex(key: 'duration', duration: duration);

    final timestampRoundTrip = graph.Vertex.fromBuffer(
      timestampVertex.writeToBuffer(),
    );
    final durationRoundTrip = graph.Vertex.fromBuffer(
      durationVertex.writeToBuffer(),
    );
    expect(timestampRoundTrip.whichValue(), graph.Vertex_Value.timestamp);
    expect(timestampRoundTrip.timestamp.seconds, timestamp.seconds);
    expect(timestampRoundTrip.timestamp.nanos, timestamp.nanos);
    expect(durationRoundTrip.whichValue(), graph.Vertex_Value.duration);
    expect(durationRoundTrip.duration.seconds, duration.seconds);
    expect(durationRoundTrip.duration.nanos, duration.nanos);
  });
}
