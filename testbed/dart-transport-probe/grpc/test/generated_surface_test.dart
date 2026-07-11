import 'package:fixnum/fixnum.dart';
import 'package:lantern_grpc_transport_probe/src/gen/graph/v1/graph.pb.dart';
import 'package:protobuf/well_known_types/google/protobuf/duration.pb.dart'
    as wkt_duration;
import 'package:protobuf/well_known_types/google/protobuf/timestamp.pb.dart'
    as wkt_timestamp;
import 'package:test/test.dart';

void main() {
  test('generated scalar, WKT, and oneof surface round-trips', () {
    final uint64Vertex = Vertex(key: 'uint64', uint64: Int64(-1));
    final uint64RoundTrip = Vertex.fromBuffer(uint64Vertex.writeToBuffer());
    expect(uint64RoundTrip.whichValue(), Vertex_Value.uint64);
    expect(uint64RoundTrip.uint64, Int64(-1));

    final timestamp = wkt_timestamp.Timestamp(
      seconds: Int64(1_234_567),
      nanos: 890,
    );
    final timestampRoundTrip = Vertex.fromBuffer(
      Vertex(key: 'timestamp', timestamp: timestamp).writeToBuffer(),
    );
    expect(timestampRoundTrip.whichValue(), Vertex_Value.timestamp);
    expect(timestampRoundTrip.timestamp.seconds, timestamp.seconds);
    expect(timestampRoundTrip.timestamp.nanos, timestamp.nanos);

    final duration = wkt_duration.Duration(seconds: Int64(-12), nanos: -345);
    final durationRoundTrip = Vertex.fromBuffer(
      Vertex(key: 'duration', duration: duration).writeToBuffer(),
    );
    expect(durationRoundTrip.whichValue(), Vertex_Value.duration);
    expect(durationRoundTrip.duration.seconds, duration.seconds);
    expect(durationRoundTrip.duration.nanos, duration.nanos);
  });
}
