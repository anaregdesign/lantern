part of 'codec.dart';

/// Strict, bounded derived-result codec sharing the exact entity value codec.
abstract final class OfflineTraversalCodec {
  /// Current derived record schema, independent of entity/outbox schemas.
  static const schemaVersion = 1;

  /// Encodes a complete result without reducing Edge values to weights.
  static String encode(OfflineTraversalRecord r) {
    final encoded = jsonEncode({
      'schema': schemaVersion,
      'type': 'traversal',
      'partitionId': r.partitionId,
      'generation': r.generation,
      'authorityBinding': r.authorityBinding,
      'responderId': r.responderId,
      'ownerTag': r.ownerTag,
      'queryKey': r.queryKey,
      'validityEpoch': r.validityEpoch,
      'observedAt': _timeToString(r.observedAt),
      'freshUntil': _timeToString(r.freshUntil),
      'historyUntil': _timeToString(r.historyUntil),
      'sourceExpiration': r.sourceExpiration == null
          ? null
          : _timeToString(r.sourceExpiration!),
      'securityUntil': r.securityUntil == null
          ? null
          : _timeToString(r.securityUntil!),
      'lastAccessAt': _timeToString(r.lastAccessAt),
      'vertices': r.graph.vertices.values.map(_entityToMap).toList(),
      'edges': r.graph.allEdges.map(_entityToMap).toList(),
    });
    if (utf8.encode(encoded).length +
            32 -
            _timeToString(r.lastAccessAt).length >
        OfflineTraversalRecord.maxBytes) {
      throw const OfflineCapacityException();
    }
    return encoded;
  }

  /// Reserved bytes include the full future LRU timestamp envelope.
  static int reservedBytes(OfflineTraversalRecord r) =>
      utf8.encode(encode(r)).length + 32 - _timeToString(r.lastAccessAt).length;

  /// Rejects unknown fields/versions, duplicate graph identities and bad caps.
  static OfflineTraversalRecord decode(String source) {
    if (utf8.encode(source).length > OfflineTraversalRecord.maxBytes) {
      throw const OfflineCodecException();
    }
    final m = _decodeObject(source);
    _expectKeys(m, const {
      'schema',
      'type',
      'partitionId',
      'generation',
      'authorityBinding',
      'responderId',
      'ownerTag',
      'queryKey',
      'validityEpoch',
      'observedAt',
      'freshUntil',
      'historyUntil',
      'sourceExpiration',
      'securityUntil',
      'lastAccessAt',
      'vertices',
      'edges',
    });
    if (m['schema'] != schemaVersion || m['type'] != 'traversal') {
      throw const OfflineSchemaException();
    }
    final vertices = m['vertices'], edges = m['edges'];
    if (vertices is! List<Object?> ||
        edges is! List<Object?> ||
        vertices.length > OfflineTraversalRecord.maxVertices ||
        edges.length > OfflineTraversalRecord.maxEdges) {
      throw const OfflineCodecException();
    }
    final keys = <String>{};
    final vs = <Vertex>[];
    final es = <Edge>[];
    for (final v in vertices) {
      final map = _object(v);
      final key = OfflineEntityKey.vertex(_string(map['key']));
      if (!keys.add(key.canonical)) throw const OfflineCodecException();
      vs.add(_entityFromMap(map, expectedKey: key) as Vertex);
    }
    for (final e in edges) {
      final map = _object(e);
      final key = OfflineEntityKey.edge(
        _string(map['tail']),
        _string(map['head']),
      );
      if (!keys.add(key.canonical)) throw const OfflineCodecException();
      es.add(_entityFromMap(map, expectedKey: key) as Edge);
    }
    try {
      return OfflineTraversalRecord(
        partitionId: _nonEmpty(m['partitionId']),
        generation: _nonNegativeInt(m['generation']),
        authorityBinding: _nonEmpty(m['authorityBinding']),
        responderId: _nonEmpty(m['responderId']),
        ownerTag: _nonEmpty(m['ownerTag']),
        queryKey: _nonEmpty(m['queryKey']),
        validityEpoch: _nonNegativeInt(m['validityEpoch']),
        graph: Graph(vertices: vs, edges: es),
        observedAt: _timeFromString(m['observedAt']),
        freshUntil: _timeFromString(m['freshUntil']),
        historyUntil: _timeFromString(m['historyUntil']),
        sourceExpiration: _nullableTime(m['sourceExpiration']),
        securityUntil: _nullableTime(m['securityUntil']),
        lastAccessAt: _timeFromString(m['lastAccessAt']),
      );
    } on OfflineArgumentException {
      throw const OfflineCodecException();
    }
  }
}
