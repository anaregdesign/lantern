import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/duration.pb.dart'
    as wkt_duration;
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart'
    as wkt_timestamp;
import 'package:test/test.dart';

void main() {
  test('public graph value types compile and round-trip', () {
    final vertices = <Vertex>[
      Vertex(key: 'float64', float64: 1.25),
      Vertex(key: 'float32', float32: 2.5),
      Vertex(key: 'int32', int32: -3),
      Vertex(key: 'int64', int64: Int64.parseInt('-9223372036854775808')),
      Vertex(key: 'uint32', uint32: 4_294_967_295),
      Vertex(key: 'uint64', uint64: Int64(-1)),
    ];
    const expectedKinds = <Vertex_Value>[
      Vertex_Value.float64,
      Vertex_Value.float32,
      Vertex_Value.int32,
      Vertex_Value.int64,
      Vertex_Value.uint32,
      Vertex_Value.uint64,
    ];

    for (var index = 0; index < vertices.length; index++) {
      final roundTrip = Vertex.fromBuffer(vertices[index].writeToBuffer());
      expect(roundTrip.whichValue(), expectedKinds[index]);
    }

    final edge = Edge(tail: 'tail', head: 'head', weight: 1.5);
    final graph = Graph(vertices: vertices, edges: [edge]);
    final roundTrip = Graph.fromBuffer(graph.writeToBuffer());
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
    final timestampVertex = Vertex(key: 'timestamp', timestamp: timestamp);
    final durationVertex = Vertex(key: 'duration', duration: duration);

    final timestampRoundTrip = Vertex.fromBuffer(
      timestampVertex.writeToBuffer(),
    );
    final durationRoundTrip = Vertex.fromBuffer(durationVertex.writeToBuffer());
    expect(timestampRoundTrip.whichValue(), Vertex_Value.timestamp);
    expect(timestampRoundTrip.timestamp.seconds, timestamp.seconds);
    expect(timestampRoundTrip.timestamp.nanos, timestamp.nanos);
    expect(durationRoundTrip.whichValue(), Vertex_Value.duration);
    expect(durationRoundTrip.duration.seconds, duration.seconds);
    expect(durationRoundTrip.duration.nanos, duration.nanos);
  });
}
