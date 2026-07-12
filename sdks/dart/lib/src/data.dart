part of 'client.dart';

const int _minInt32 = -0x80000000;
const int _maxInt32 = 0x7fffffff;
const int _maxUint32 = 0xffffffff;
const int _minInt64 = -0x8000000000000000;
const int _maxInt64 = 0x7fffffffffffffff;
final BigInt _maxUint64 = (BigInt.one << 64) - BigInt.one;
final DateTime _minProtoTimestamp = DateTime.utc(1);
final DateTime _maxProtoTimestamp = DateTime.utc(
  9999,
  12,
  31,
  23,
  59,
  59,
  999,
  999,
);
const int _maxProtoDurationSeconds = 315576000000;
const int _maxProtoDurationMicroseconds =
    _maxProtoDurationSeconds * Duration.microsecondsPerSecond;

/// Exact Protobuf oneof value carried by a [Vertex].
///
/// Use the named factories so a Dart `int` or `double` is never guessed into
/// a lossy wire kind.
sealed class VertexValue {
  const VertexValue();

  /// A Protobuf `double` value.
  factory VertexValue.float64(double value) = Float64Value;

  /// A Protobuf `float` value, normalized to IEEE-754 binary32 precision.
  factory VertexValue.float32(double value) = Float32Value;

  /// A signed 32-bit integer.
  factory VertexValue.int32(int value) = Int32Value;

  /// A signed 64-bit integer.
  factory VertexValue.int64(int value) = Int64Value;

  /// An unsigned 32-bit integer.
  factory VertexValue.uint32(int value) = Uint32Value;

  /// An unsigned 64-bit integer represented without signed truncation.
  factory VertexValue.uint64(BigInt value) = Uint64Value;

  /// A boolean value.
  factory VertexValue.boolean(bool value) = BoolValue;

  /// A UTF-8 string value.
  factory VertexValue.string(String value) = StringValue;

  /// A byte string. The input and every returned view are defensive copies.
  factory VertexValue.bytes(Uint8List value) = BytesValue;

  /// A Protobuf Timestamp, normalized to UTC.
  factory VertexValue.timestamp(DateTime value) = TimestampValue;

  /// A Protobuf Duration.
  factory VertexValue.duration(Duration value) = DurationValue;

  /// An explicit nil oneof arm.
  factory VertexValue.nil() = NilValue;

  /// An unset oneof, distinct from [VertexValue.nil].
  factory VertexValue.unset() = UnsetValue;
}

/// A Protobuf `double` vertex value.
final class Float64Value extends VertexValue {
  /// Creates a finite binary64 value.
  Float64Value(this.value) {
    if (!value.isFinite) {
      throw _invalidArgumentException('float64 must be finite');
    }
  }

  /// Finite IEEE-754 binary64 value.
  final double value;
}

/// A Protobuf `float` vertex value.
final class Float32Value extends VertexValue {
  /// Creates a finite value normalized to binary32 precision.
  Float32Value(double value) : value = _normalizeFloat32(value);

  /// Finite value rounded exactly as the wire's IEEE-754 binary32 field.
  final double value;
}

/// A signed 32-bit vertex value.
final class Int32Value extends VertexValue {
  /// Creates a signed 32-bit value.
  Int32Value(this.value) {
    _requireRange(value, _minInt32, _maxInt32, 'int32');
  }

  /// Value in `[-2^31, 2^31-1]`.
  final int value;
}

/// A signed 64-bit vertex value.
final class Int64Value extends VertexValue {
  /// Creates a signed 64-bit value.
  Int64Value(this.value) {
    _requireRange(value, _minInt64, _maxInt64, 'int64');
  }

  /// Value in `[-2^63, 2^63-1]`.
  final int value;
}

/// An unsigned 32-bit vertex value.
final class Uint32Value extends VertexValue {
  /// Creates an unsigned 32-bit value.
  Uint32Value(this.value) {
    _requireRange(value, 0, _maxUint32, 'uint32');
  }

  /// Value in `[0, 2^32-1]`.
  final int value;
}

/// An unsigned 64-bit vertex value.
final class Uint64Value extends VertexValue {
  /// Creates an unsigned 64-bit value.
  Uint64Value(this.value) {
    if (value < BigInt.zero || value > _maxUint64) {
      throw _invalidArgumentException('uint64 must be in [0, 2^64-1]');
    }
  }

  /// Value in `[0, 2^64-1]`, including values beyond signed Dart `int`.
  final BigInt value;
}

/// A boolean vertex value.
final class BoolValue extends VertexValue {
  /// Creates a boolean value.
  const BoolValue(this.value);

  /// Boolean value.
  final bool value;
}

/// A string vertex value.
final class StringValue extends VertexValue {
  /// Creates a string value.
  const StringValue(this.value);

  /// String value.
  final String value;
}

/// A defensively owned byte-string vertex value.
final class BytesValue extends VertexValue {
  /// Creates a defensively owned byte value.
  BytesValue(Uint8List value) : _value = Uint8List.fromList(value);

  final Uint8List _value;

  /// A fresh copy of the bytes.
  Uint8List get value => Uint8List.fromList(_value);
}

/// A UTC timestamp vertex value.
final class TimestampValue extends VertexValue {
  /// Creates a UTC-normalized timestamp value.
  TimestampValue(DateTime value) : value = _normalizeTimestamp(value);

  /// UTC timestamp representable by Protobuf Timestamp.
  final DateTime value;
}

/// A Protobuf Duration vertex value.
final class DurationValue extends VertexValue {
  /// Creates a Protobuf-representable duration value.
  DurationValue(this.value) {
    _validateDuration(value);
  }

  /// Duration representable by Protobuf Duration.
  final Duration value;
}

/// The explicit nil oneof arm.
final class NilValue extends VertexValue {
  /// Creates an explicit nil value.
  const NilValue();
}

/// The absent oneof arm.
final class UnsetValue extends VertexValue {
  /// Creates an unset oneof value.
  const UnsetValue();
}

/// Immutable SDK-facing vertex.
final class Vertex {
  /// Creates a decoded vertex.
  const Vertex({
    required this.key,
    required this.value,
    required this.expiration,
  });

  /// Vertex key.
  final String key;

  /// Exact oneof value, including nil versus unset.
  final VertexValue value;

  /// UTC absolute expiration, or `null` for permanent storage.
  final DateTime? expiration;
}

/// Immutable SDK-facing edge.
final class Edge {
  /// Creates a decoded edge.
  const Edge({
    required this.tail,
    required this.head,
    required this.weight,
    required this.expiration,
  });

  /// Tail vertex key.
  final String tail;

  /// Head vertex key.
  final String head;

  /// Stored IEEE-754 binary32 edge weight, exposed as a Dart `double`.
  final double weight;

  /// UTC absolute expiration, or `null` for permanent storage.
  final DateTime? expiration;
}

/// Input for a vertex write.
final class VertexInput {
  /// Creates a vertex write.
  ///
  /// Omitting both expiration fields means permanent storage. [expiresIn]
  /// is evaluated from the client's injected clock exactly once per logical
  /// call. Device-clock skew therefore affects relative TTLs.
  const VertexInput({
    required this.key,
    required this.value,
    this.expiresIn,
    this.expiresAt,
  });

  /// Vertex key.
  final String key;

  /// Exact value kind to write.
  final VertexValue value;

  /// Positive relative expiration.
  final Duration? expiresIn;

  /// Absolute expiration; normalized to UTC before writing.
  final DateTime? expiresAt;
}

/// Immutable edge identity.
final class EdgeRef {
  /// Creates an edge identity.
  const EdgeRef(this.tail, this.head);

  /// Tail vertex key.
  final String tail;

  /// Head vertex key.
  final String head;

  @override
  bool operator ==(Object other) =>
      other is EdgeRef && other.tail == tail && other.head == head;

  @override
  int get hashCode => Object.hash(tail, head);
}

/// Input for additive or idempotent edge writes.
final class EdgeInput {
  /// Creates an edge write.
  ///
  /// [contribId], when present, must be exactly 24 bytes and non-zero. It is
  /// used only by additive writes; automatic generation belongs to the
  /// resilience layer.
  EdgeInput({
    required this.tail,
    required this.head,
    required double weight,
    this.expiresIn,
    this.expiresAt,
    Uint8List? contribId,
  }) : weight = _normalizeFloat32(weight),
       _contribId = contribId == null ? null : Uint8List.fromList(contribId);

  /// Tail vertex key.
  final String tail;

  /// Head vertex key.
  final String head;

  /// Finite weight normalized to IEEE-754 binary32 precision.
  final double weight;

  /// Positive relative expiration.
  final Duration? expiresIn;

  /// Absolute expiration; normalized to UTC before writing.
  final DateTime? expiresAt;

  final Uint8List? _contribId;

  /// Caller-supplied contribution ID as a defensive copy.
  Uint8List? get contribId =>
      _contribId == null ? null : Uint8List.fromList(_contribId);
}

/// Present and missing vertices from a plural read.
final class GetVerticesResult {
  /// Creates an immutable plural vertex read result.
  GetVerticesResult({
    required Iterable<Vertex> vertices,
    required Iterable<String> missing,
  }) : vertices = List.unmodifiable(vertices),
       missing = List.unmodifiable(missing);

  /// Present vertices in server response order.
  final List<Vertex> vertices;

  /// Missing keys in server response order.
  final List<String> missing;
}

/// Result of a plural vertex put.
final class PutVerticesResult {
  /// Creates an immutable plural vertex put result.
  PutVerticesResult({
    required this.written,
    required Iterable<String> skippedKeys,
  }) : skippedKeys = List.unmodifiable(skippedKeys);

  /// Number of values actually written.
  final int written;

  /// Existing keys skipped by a conditional put.
  final List<String> skippedKeys;
}

/// Present and missing edges from a plural read.
final class GetEdgesResult {
  /// Creates an immutable plural edge read result.
  GetEdgesResult({
    required Iterable<Edge> edges,
    required Iterable<EdgeRef> missing,
  }) : edges = List.unmodifiable(edges),
       missing = List.unmodifiable(missing);

  /// Present edges in server response order.
  final List<Edge> edges;

  /// Missing edge identities in server response order.
  final List<EdgeRef> missing;
}

/// Result of a plural additive edge write.
final class AddEdgesResult {
  /// Creates an immutable plural additive edge result.
  AddEdgesResult({
    required this.written,
    required Iterable<double> effectiveWeights,
  }) : effectiveWeights = List.unmodifiable(effectiveWeights);

  /// Number of accepted contributions.
  final int written;

  /// Post-accumulation weights aligned with the input edges.
  final List<double> effectiveWeights;
}

/// A plural write failed after one or more prior chunks committed.
final class BatchException implements Exception {
  /// Creates a partial-progress error.
  const BatchException({required this.committed, required this.cause});

  /// Number of writes confirmed committed before the failed chunk.
  final int committed;

  /// Typed failure reported by the failed chunk.
  final Exception cause;

  @override
  String toString() => 'BatchException(committed: $committed, cause: $cause)';
}

double _normalizeFloat32(double value) {
  if (!value.isFinite) {
    throw _invalidArgumentException('float32 must be finite');
  }
  final data = ByteData(4)..setFloat32(0, value, Endian.big);
  final normalized = data.getFloat32(0, Endian.big);
  if (!normalized.isFinite) {
    throw _invalidArgumentException('float32 is outside the finite wire range');
  }
  return normalized;
}

void _requireRange(int value, int min, int max, String kind) {
  if (value < min || value > max) {
    throw _invalidArgumentException('$kind is outside its wire range');
  }
}

DateTime _normalizeTimestamp(DateTime value) {
  final utc = value.toUtc();
  if (utc.isBefore(_minProtoTimestamp) || utc.isAfter(_maxProtoTimestamp)) {
    throw _invalidArgumentException(
      'timestamp must be between 0001-01-01 and 9999-12-31 UTC',
    );
  }
  return utc;
}

void _validateDuration(Duration value) {
  if (value.inMicroseconds < -_maxProtoDurationMicroseconds ||
      value.inMicroseconds > _maxProtoDurationMicroseconds) {
    throw _invalidArgumentException('duration is outside the Protobuf range');
  }
}

$timestamp.Timestamp _timestampToProto(DateTime value) =>
    $timestamp.Timestamp.fromDateTime(_normalizeTimestamp(value));

DateTime _timestampFromProto($timestamp.Timestamp value) {
  if (value.seconds < Int64(-62135596800) ||
      value.seconds > Int64(253402300799) ||
      value.nanos < 0 ||
      value.nanos > 999999999) {
    throw _internalSdkException('server returned an invalid timestamp');
  }
  return value.toDateTime().toUtc();
}

$duration.Duration _durationToProto(Duration value) {
  _validateDuration(value);
  final micros = value.inMicroseconds;
  return $duration.Duration(
    seconds: Int64(micros ~/ Duration.microsecondsPerSecond),
    nanos: micros.remainder(Duration.microsecondsPerSecond).toInt() * 1000,
  );
}

Duration _durationFromProto($duration.Duration value) {
  final seconds = value.seconds.toInt();
  if (seconds < -_maxProtoDurationSeconds ||
      seconds > _maxProtoDurationSeconds ||
      value.nanos <= -1000000000 ||
      value.nanos >= 1000000000 ||
      (seconds < 0 && value.nanos > 0) ||
      (seconds > 0 && value.nanos < 0)) {
    throw _internalSdkException('server returned an invalid duration');
  }
  final microseconds =
      seconds * Duration.microsecondsPerSecond + value.nanos ~/ 1000;
  if (microseconds < -_maxProtoDurationMicroseconds ||
      microseconds > _maxProtoDurationMicroseconds) {
    throw _internalSdkException('server returned an invalid duration');
  }
  return Duration(microseconds: microseconds);
}

Int64 _uint64ToFixnum(BigInt value) {
  final bytes = List<int>.filled(8, 0);
  var remaining = value;
  for (var index = 7; index >= 0; index--) {
    bytes[index] = (remaining & BigInt.from(0xff)).toInt();
    remaining >>= 8;
  }
  return Int64.fromBytesBigEndian(bytes);
}

BigInt _uint64FromFixnum(Int64 value) {
  var result = BigInt.zero;
  for (final byte in value.toBytes().reversed) {
    result = (result << 8) | BigInt.from(byte);
  }
  return result;
}

$graph.Vertex _vertexInputToProto(VertexInput input, DateTime? expiration) {
  final vertex = $graph.Vertex(key: input.key);
  if (expiration != null) vertex.expiration = _timestampToProto(expiration);
  switch (input.value) {
    case Float64Value(:final value):
      vertex.float64 = value;
    case Float32Value(:final value):
      vertex.float32 = value;
    case Int32Value(:final value):
      vertex.int32 = value;
    case Int64Value(:final value):
      vertex.int64 = Int64(value);
    case Uint32Value(:final value):
      vertex.uint32 = value;
    case Uint64Value(:final value):
      vertex.uint64 = _uint64ToFixnum(value);
    case BoolValue(:final value):
      vertex.bool_16 = value;
    case StringValue(:final value):
      vertex.string = value;
    case BytesValue(:final value):
      vertex.bytes = value;
    case TimestampValue(:final value):
      vertex.timestamp = _timestampToProto(value);
    case DurationValue(:final value):
      vertex.duration = _durationToProto(value);
    case NilValue():
      vertex.nil = true;
    case UnsetValue():
      break;
  }
  return vertex;
}

Vertex _vertexFromProto($graph.Vertex value) {
  final decoded = switch (value.whichValue()) {
    $graph.Vertex_Value.float64 => VertexValue.float64(
      _finiteFloatFromProto(value.float64, 'float64'),
    ),
    $graph.Vertex_Value.float32 => VertexValue.float32(
      _finiteFloatFromProto(value.float32, 'float32'),
    ),
    $graph.Vertex_Value.int32 => VertexValue.int32(value.int32),
    $graph.Vertex_Value.int64 => VertexValue.int64(value.int64.toInt()),
    $graph.Vertex_Value.uint32 => VertexValue.uint32(value.uint32),
    $graph.Vertex_Value.uint64 => VertexValue.uint64(
      _uint64FromFixnum(value.uint64),
    ),
    $graph.Vertex_Value.bool_16 => VertexValue.boolean(value.bool_16),
    $graph.Vertex_Value.string => VertexValue.string(value.string),
    $graph.Vertex_Value.bytes => VertexValue.bytes(
      Uint8List.fromList(value.bytes),
    ),
    $graph.Vertex_Value.timestamp => VertexValue.timestamp(
      _timestampFromProto(value.timestamp),
    ),
    $graph.Vertex_Value.duration => VertexValue.duration(
      _durationFromProto(value.duration),
    ),
    $graph.Vertex_Value.nil => VertexValue.nil(),
    $graph.Vertex_Value.notSet => VertexValue.unset(),
  };
  return Vertex(
    key: value.key,
    value: decoded,
    expiration: value.hasExpiration()
        ? _timestampFromProto(value.expiration)
        : null,
  );
}

$graph.Edge _edgeInputToProto(EdgeInput input, DateTime? expiration) {
  final edge = $graph.Edge(
    tail: input.tail,
    head: input.head,
    weight: input.weight,
  );
  if (expiration != null) edge.expiration = _timestampToProto(expiration);
  return edge;
}

Edge _edgeFromProto($graph.Edge value) => Edge(
  tail: value.tail,
  head: value.head,
  weight: _finiteFloatFromProto(value.weight, 'edge weight'),
  expiration: value.hasExpiration()
      ? _timestampFromProto(value.expiration)
      : null,
);

LanternException _internalSdkException(String message) =>
    LanternInternalException._(
      _ErrorData(
        transportCode: connect.Code.internal.value,
        transportCodeName: connect.Code.internal.name,
        message: message,
        headers: const {},
        trailers: const {},
        metadata: const {},
      ),
    );

double _finiteFloatFromProto(double value, String field) {
  if (!value.isFinite) {
    throw _internalSdkException('server returned a non-finite $field');
  }
  return value;
}
