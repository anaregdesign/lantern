import 'dart:convert';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'types.dart';

/// Canonical strict JSON codec for offline cache and outbox records.
///
/// Version one deliberately fails closed on unknown schema, discriminator,
/// field, float payload, range, or byte encoding. It preserves every public
/// [VertexValue] kind, including nil versus unset and exact IEEE float bits.
/// Legacy Add intents remain decodable only so stores can quarantine them.
final class OfflineCodec {
  OfflineCodec._();

  /// Current persisted JSON schema version.
  static const int schemaVersion = 1;

  /// Encodes one exact confirmed cache record to canonical JSON.
  static String encodeCacheRecord(OfflineCacheRecord record) =>
      jsonEncode(_cacheToMap(record));

  /// Decodes one strict v1 confirmed cache record.
  static OfflineCacheRecord decodeCacheRecord(String source) {
    final value = _decodeObject(source);
    _expectKeys(value, _cacheKeys);
    _expectSchema(value, 'cache');
    final partitionId = _nonEmpty(value['partitionId']);
    final generation = _nonNegativeInt(value['generation']);
    final key = _keyFromMap(_object(value['key']));
    final validatedAt = _timeFromString(value['validatedAt']);
    final lastAccessAt = _timeFromString(value['lastAccessAt']);
    final versionTag = _nullableString(value['versionTag']);
    final record = _string(value['record']);
    return switch (record) {
      'value' when value['missingUntil'] == null => OfflineCacheRecord.value(
        partitionId: partitionId,
        generation: generation,
        key: key,
        entity: _entityFromMap(_object(value['entity']), expectedKey: key),
        validatedAt: validatedAt,
        lastAccessAt: lastAccessAt,
        versionTag: versionTag,
      ),
      'missing' when value['entity'] == null => OfflineCacheRecord.missing(
        partitionId: partitionId,
        generation: generation,
        key: key,
        validatedAt: validatedAt,
        lastAccessAt: lastAccessAt,
        missingUntil: _timeFromString(value['missingUntil']),
        versionTag: versionTag,
      ),
      _ => throw const OfflineCodecException(),
    };
  }

  /// Encodes one exact outbox record to canonical JSON.
  static String encodeOutboxRecord(OfflineOutboxRecord record) =>
      jsonEncode(_outboxToMap(record));

  /// Decodes one strict v1 outbox record.
  static OfflineOutboxRecord decodeOutboxRecord(String source) {
    final value = _decodeObject(source);
    _expectKeys(value, _outboxKeys);
    _expectSchema(value, 'outbox');
    final state = _outboxState(_string(value['state']));
    final record = OfflineOutboxRecord(
      recordId: _nonEmpty(value['recordId']),
      operationId: _nonEmpty(value['operationId']),
      itemIndex: _nonNegativeInt(value['itemIndex']),
      partitionId: _nonEmpty(value['partitionId']),
      intent: _intentFromMap(_object(value['intent'])),
      enqueuedAt: _timeFromString(value['enqueuedAt']),
      ordinal: _positiveInt(value['ordinal']),
      state: state,
      attemptCount: _nonNegativeInt(value['attemptCount']),
      generation: _nonNegativeInt(value['generation']),
      nextAttemptAt: _nullableTime(value['nextAttemptAt']),
      leaseOwner: _nullableString(value['leaseOwner']),
      leaseUntil: _nullableTime(value['leaseUntil']),
      diagnosticCode: _nullableString(value['diagnosticCode']),
    );
    final hasLease = record.leaseOwner != null && record.leaseUntil != null;
    if ((state == OfflineOutboxState.sending) != hasLease) {
      throw const OfflineCodecException();
    }
    return record;
  }

  /// Encodes one content-free durable operation aggregate.
  static String encodeOperationRecord(OfflineOperationRecord record) =>
      jsonEncode(_operationToMap(record));

  /// Decodes one strict v1 durable operation aggregate.
  static OfflineOperationRecord decodeOperationRecord(String source) {
    final value = _decodeObject(source);
    _expectKeys(value, _operationKeys);
    _expectSchema(value, 'operation');
    final operationId = _nonEmpty(value['operationId']);
    final rawItems = value['items'];
    if (rawItems is! List<Object?> || rawItems.isEmpty) {
      throw const OfflineCodecException();
    }
    final items = <OfflineWriteStatus>[];
    for (final rawItem in rawItems) {
      final item = _object(rawItem);
      _expectKeys(item, _operationItemKeys);
      items.add(
        OfflineWriteStatus(
          recordId: _nonEmpty(item['recordId']),
          operationId: operationId,
          itemIndex: _nonNegativeInt(item['itemIndex']),
          state: _writeState(_string(item['state'])),
          attemptCount: _nonNegativeInt(item['attemptCount']),
          diagnosticCode: _nullableString(item['diagnosticCode']),
        ),
      );
    }
    try {
      return OfflineOperationRecord(
        partitionId: _nonEmpty(value['partitionId']),
        generation: _nonNegativeInt(value['generation']),
        operationId: operationId,
        items: items,
        updatedAt: _timeFromString(value['updatedAt']),
        terminalAt: _nullableTime(value['terminalAt']),
      );
    } on OfflineArgumentException {
      throw const OfflineCodecException();
    }
  }
}

const Set<String> _cacheKeys = <String>{
  'schema',
  'type',
  'partitionId',
  'generation',
  'key',
  'record',
  'entity',
  'validatedAt',
  'lastAccessAt',
  'missingUntil',
  'versionTag',
};

const Set<String> _outboxKeys = <String>{
  'schema',
  'type',
  'recordId',
  'operationId',
  'itemIndex',
  'partitionId',
  'intent',
  'enqueuedAt',
  'ordinal',
  'state',
  'attemptCount',
  'generation',
  'nextAttemptAt',
  'leaseOwner',
  'leaseUntil',
  'diagnosticCode',
};

const Set<String> _operationKeys = <String>{
  'schema',
  'type',
  'partitionId',
  'generation',
  'operationId',
  'items',
  'updatedAt',
  'terminalAt',
};

const Set<String> _operationItemKeys = <String>{
  'recordId',
  'itemIndex',
  'state',
  'attemptCount',
  'diagnosticCode',
};

Map<String, Object?> _cacheToMap(OfflineCacheRecord record) =>
    <String, Object?>{
      'schema': OfflineCodec.schemaVersion,
      'type': 'cache',
      'partitionId': record.partitionId,
      'generation': record.generation,
      'key': _keyToMap(record.key),
      'record': record.isMissing ? 'missing' : 'value',
      'entity': record.isMissing ? null : _entityToMap(record.entity!),
      'validatedAt': _timeToString(record.validatedAt),
      'lastAccessAt': _timeToString(record.lastAccessAt),
      'missingUntil': record.missingUntil == null
          ? null
          : _timeToString(record.missingUntil!),
      'versionTag': record.versionTag,
    };

Map<String, Object?> _outboxToMap(OfflineOutboxRecord record) =>
    <String, Object?>{
      'schema': OfflineCodec.schemaVersion,
      'type': 'outbox',
      'recordId': record.recordId,
      'operationId': record.operationId,
      'itemIndex': record.itemIndex,
      'partitionId': record.partitionId,
      'intent': _intentToMap(record.intent),
      'enqueuedAt': _timeToString(record.enqueuedAt),
      'ordinal': record.ordinal,
      'state': record.state.name,
      'attemptCount': record.attemptCount,
      'generation': record.generation,
      'nextAttemptAt': record.nextAttemptAt == null
          ? null
          : _timeToString(record.nextAttemptAt!),
      'leaseOwner': record.leaseOwner,
      'leaseUntil': record.leaseUntil == null
          ? null
          : _timeToString(record.leaseUntil!),
      'diagnosticCode': record.diagnosticCode,
    };

Map<String, Object?> _operationToMap(OfflineOperationRecord record) =>
    <String, Object?>{
      'schema': OfflineCodec.schemaVersion,
      'type': 'operation',
      'partitionId': record.partitionId,
      'generation': record.generation,
      'operationId': record.operationId,
      'items': record.items
          .map(
            (item) => <String, Object?>{
              'recordId': item.recordId,
              'itemIndex': item.itemIndex,
              'state': item.state.name,
              'attemptCount': item.attemptCount,
              'diagnosticCode': item.diagnosticCode,
            },
          )
          .toList(growable: false),
      'updatedAt': _timeToString(record.updatedAt),
      'terminalAt': record.terminalAt == null
          ? null
          : _timeToString(record.terminalAt!),
    };

Map<String, Object?> _keyToMap(OfflineEntityKey key) => switch (key.kind) {
  OfflineEntityKind.vertex => <String, Object?>{
    'kind': 'vertex',
    'key': key.vertexKey,
    'tail': null,
    'head': null,
  },
  OfflineEntityKind.edge => <String, Object?>{
    'kind': 'edge',
    'key': null,
    'tail': key.tail,
    'head': key.head,
  },
};

OfflineEntityKey _keyFromMap(Map<String, Object?> value) {
  _expectKeys(value, const <String>{'kind', 'key', 'tail', 'head'});
  return switch (_string(value['kind'])) {
    'vertex' when value['tail'] == null && value['head'] == null =>
      OfflineEntityKey.vertex(_string(value['key'])),
    'edge' when value['key'] == null => OfflineEntityKey.edge(
      _string(value['tail']),
      _string(value['head']),
    ),
    _ => throw const OfflineCodecException(),
  };
}

Map<String, Object?> _entityToMap(Object entity) => switch (entity) {
  Vertex vertex => <String, Object?>{
    'kind': 'vertex',
    'key': vertex.key,
    'value': _valueToMap(vertex.value),
    'expiration': vertex.expiration == null
        ? null
        : _timeToString(vertex.expiration!),
    'tail': null,
    'head': null,
    'weight': null,
  },
  Edge edge => <String, Object?>{
    'kind': 'edge',
    'key': null,
    'value': null,
    'expiration': edge.expiration == null
        ? null
        : _timeToString(edge.expiration!),
    'tail': edge.tail,
    'head': edge.head,
    'weight': _floatBits(edge.weight, 4),
  },
  _ => throw const OfflineCodecException(),
};

Object _entityFromMap(
  Map<String, Object?> value, {
  required OfflineEntityKey expectedKey,
}) {
  _expectKeys(value, const <String>{
    'kind',
    'key',
    'value',
    'expiration',
    'tail',
    'head',
    'weight',
  });
  final expiration = _nullableTime(value['expiration']);
  return switch (_string(value['kind'])) {
    'vertex'
        when expectedKey.kind == OfflineEntityKind.vertex &&
            value['tail'] == null &&
            value['head'] == null &&
            value['weight'] == null &&
            _string(value['key']) == expectedKey.vertexKey =>
      Vertex(
        key: expectedKey.vertexKey!,
        value: _valueFromMap(_object(value['value'])),
        expiration: expiration,
      ),
    'edge'
        when expectedKey.kind == OfflineEntityKind.edge &&
            value['key'] == null &&
            value['value'] == null &&
            _string(value['tail']) == expectedKey.tail &&
            _string(value['head']) == expectedKey.head =>
      Edge(
        tail: expectedKey.tail!,
        head: expectedKey.head!,
        weight: _floatFromBits(_string(value['weight']), 4),
        expiration: expiration,
      ),
    _ => throw const OfflineCodecException(),
  };
}

Map<String, Object?> _intentToMap(OfflineIntent intent) => switch (intent) {
  OfflinePutVertexIntent(:final vertex) => <String, Object?>{
    'kind': 'putVertex',
    'entity': _entityToMap(vertex),
    'contributionId': null,
  },
  OfflinePutEdgeIntent(:final edge) => <String, Object?>{
    'kind': 'putEdge',
    'entity': _entityToMap(edge),
    'contributionId': null,
  },
  OfflineAddEdgeIntent(:final edge, :final contributionId) => <String, Object?>{
    'kind': 'addEdge',
    'entity': _entityToMap(edge),
    'contributionId': _base64UrlNoPadding(contributionId),
  },
};

OfflineIntent _intentFromMap(Map<String, Object?> value) {
  _expectKeys(value, const <String>{'kind', 'entity', 'contributionId'});
  final kind = _string(value['kind']);
  final entityMap = _object(value['entity']);
  return switch (kind) {
    'putVertex' => () {
      final key = OfflineEntityKey.vertex(_string(entityMap['key']));
      return OfflinePutVertexIntent(
        _entityFromMap(entityMap, expectedKey: key) as Vertex,
      );
    }(),
    'putEdge' => () {
      final key = OfflineEntityKey.edge(
        _string(entityMap['tail']),
        _string(entityMap['head']),
      );
      return OfflinePutEdgeIntent(
        _entityFromMap(entityMap, expectedKey: key) as Edge,
      );
    }(),
    'addEdge' => () {
      final key = OfflineEntityKey.edge(
        _string(entityMap['tail']),
        _string(entityMap['head']),
      );
      return OfflineAddEdgeIntent(
        _entityFromMap(entityMap, expectedKey: key) as Edge,
        _base64(value['contributionId'], length: 24),
      );
    }(),
    _ => throw const OfflineCodecException(),
  };
}

Map<String, Object?> _valueToMap(VertexValue value) => switch (value) {
  Float64Value(:final value) => <String, Object?>{
    'kind': 'float64',
    'data': _floatBits(value, 8),
  },
  Float32Value(:final value) => <String, Object?>{
    'kind': 'float32',
    'data': _floatBits(value, 4),
  },
  Int32Value(:final value) => <String, Object?>{'kind': 'int32', 'data': value},
  Int64Value(:final value) => <String, Object?>{
    'kind': 'int64',
    'data': value.toString(),
  },
  Uint32Value(:final value) => <String, Object?>{
    'kind': 'uint32',
    'data': value,
  },
  Uint64Value(:final value) => <String, Object?>{
    'kind': 'uint64',
    'data': value.toString(),
  },
  BoolValue(:final value) => <String, Object?>{'kind': 'bool', 'data': value},
  StringValue(:final value) => <String, Object?>{
    'kind': 'string',
    'data': value,
  },
  BytesValue(:final value) => <String, Object?>{
    'kind': 'bytes',
    'data': _base64UrlNoPadding(value),
  },
  TimestampValue(:final value) => <String, Object?>{
    'kind': 'timestamp',
    'data': _timeToString(value),
  },
  DurationValue(:final value) => <String, Object?>{
    'kind': 'duration',
    'data': value.inMicroseconds.toString(),
  },
  NilValue() => <String, Object?>{'kind': 'nil', 'data': null},
  UnsetValue() => <String, Object?>{'kind': 'unset', 'data': null},
};

VertexValue _valueFromMap(Map<String, Object?> value) {
  _expectKeys(value, const <String>{'kind', 'data'});
  return switch (_string(value['kind'])) {
    'float64' => VertexValue.float64(_floatFromBits(_string(value['data']), 8)),
    'float32' => VertexValue.float32(_floatFromBits(_string(value['data']), 4)),
    'int32' => VertexValue.int32(
      _intInRange(value['data'], -0x80000000, 0x7fffffff),
    ),
    'int64' => VertexValue.int64(
      _decimalInt(value['data'], -0x8000000000000000, 0x7fffffffffffffff),
    ),
    'uint32' => VertexValue.uint32(_intInRange(value['data'], 0, 0xffffffff)),
    'uint64' => VertexValue.uint64(
      _decimalBigInt(
        value['data'],
        BigInt.zero,
        (BigInt.one << 64) - BigInt.one,
      ),
    ),
    'bool' => VertexValue.boolean(_bool(value['data'])),
    'string' => VertexValue.string(_string(value['data'])),
    'bytes' => VertexValue.bytes(_base64(value['data'])),
    'timestamp' => VertexValue.timestamp(_timeFromString(value['data'])),
    'duration' => VertexValue.duration(
      Duration(
        microseconds: _decimalInt(
          value['data'],
          -315576000000000000,
          315576000000000000,
        ),
      ),
    ),
    'nil' when value['data'] == null => VertexValue.nil(),
    'unset' when value['data'] == null => VertexValue.unset(),
    _ => throw const OfflineCodecException(),
  };
}

String _floatBits(double value, int length) {
  if (!value.isFinite) throw const OfflineArgumentException();
  final data = ByteData(length);
  if (length == 4) {
    data.setFloat32(0, normalizeOfflineFloat32(value), Endian.big);
  } else {
    data.setFloat64(0, value, Endian.big);
  }
  return List<String>.generate(
    length,
    (index) => data.getUint8(index).toRadixString(16).padLeft(2, '0'),
  ).join();
}

double _floatFromBits(String value, int length) {
  if (value.length != length * 2 || !RegExp(r'^[0-9a-f]+$').hasMatch(value)) {
    throw const OfflineCodecException();
  }
  final data = ByteData(length);
  for (var index = 0; index < length; index++) {
    data.setUint8(
      index,
      int.parse(value.substring(index * 2, index * 2 + 2), radix: 16),
    );
  }
  final result = length == 4
      ? data.getFloat32(0, Endian.big)
      : data.getFloat64(0, Endian.big);
  if (!result.isFinite) throw const OfflineCodecException();
  return result;
}

String _timeToString(DateTime value) =>
    value.toUtc().microsecondsSinceEpoch.toString();

DateTime _timeFromString(Object? value) {
  final micros = _decimalInt(
    value,
    DateTime.utc(1).microsecondsSinceEpoch,
    DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 999).microsecondsSinceEpoch,
  );
  return DateTime.fromMicrosecondsSinceEpoch(micros, isUtc: true);
}

DateTime? _nullableTime(Object? value) =>
    value == null ? null : _timeFromString(value);

Map<String, Object?> _decodeObject(String source) {
  try {
    return _object(jsonDecode(source));
  } on OfflineCodecException {
    rethrow;
  } catch (_) {
    throw const OfflineCodecException();
  }
}

Map<String, Object?> _object(Object? value) {
  if (value is! Map<Object?, Object?>) throw const OfflineCodecException();
  final result = <String, Object?>{};
  for (final entry in value.entries) {
    if (entry.key is! String) throw const OfflineCodecException();
    result[entry.key! as String] = entry.value;
  }
  return result;
}

void _expectKeys(Map<String, Object?> value, Set<String> expected) {
  if (value.length != expected.length ||
      !value.keys.toSet().containsAll(expected)) {
    throw const OfflineCodecException();
  }
}

void _expectSchema(Map<String, Object?> value, String type) {
  if (value['schema'] != OfflineCodec.schemaVersion || value['type'] != type) {
    throw const OfflineCodecException();
  }
}

String _string(Object? value) {
  if (value is! String) throw const OfflineCodecException();
  return value;
}

String _nonEmpty(Object? value) {
  final result = _string(value);
  if (result.isEmpty) throw const OfflineCodecException();
  return result;
}

String? _nullableString(Object? value) => value == null ? null : _string(value);

bool _bool(Object? value) {
  if (value is! bool) throw const OfflineCodecException();
  return value;
}

int _nonNegativeInt(Object? value) => _intInRange(value, 0, 0x7fffffffffffffff);

int _positiveInt(Object? value) => _intInRange(value, 1, 0x7fffffffffffffff);

int _intInRange(Object? value, int minimum, int maximum) {
  if (value is! int || value < minimum || value > maximum) {
    throw const OfflineCodecException();
  }
  return value;
}

int _decimalInt(Object? value, int minimum, int maximum) {
  final parsed = _decimalBigInt(
    value,
    BigInt.from(minimum),
    BigInt.from(maximum),
  );
  return parsed.toInt();
}

BigInt _decimalBigInt(Object? value, BigInt minimum, BigInt maximum) {
  if (value is! String ||
      !RegExp(r'^-?(0|[1-9][0-9]*)$').hasMatch(value) ||
      value == '-0') {
    throw const OfflineCodecException();
  }
  final parsed = BigInt.tryParse(value);
  if (parsed == null || parsed < minimum || parsed > maximum) {
    throw const OfflineCodecException();
  }
  return parsed;
}

Uint8List _base64(Object? value, {int? length}) {
  final text = _string(value);
  if (!RegExp(r'^[A-Za-z0-9_-]*$').hasMatch(text)) {
    throw const OfflineCodecException();
  }
  Uint8List bytes;
  try {
    bytes = Uint8List.fromList(base64Url.decode(base64Url.normalize(text)));
  } catch (_) {
    throw const OfflineCodecException();
  }
  if (_base64UrlNoPadding(bytes) != text ||
      (length != null && bytes.length != length)) {
    throw const OfflineCodecException();
  }
  return bytes;
}

String _base64UrlNoPadding(List<int> bytes) =>
    base64Url.encode(bytes).replaceAll('=', '');

OfflineOutboxState _outboxState(String value) => switch (value) {
  'enqueued' => OfflineOutboxState.enqueued,
  'sending' => OfflineOutboxState.sending,
  'deadLetter' => OfflineOutboxState.deadLetter,
  'expired' => OfflineOutboxState.expired,
  _ => throw const OfflineCodecException(),
};

OfflineWriteState _writeState(String value) => switch (value) {
  'locallyCommitted' => OfflineWriteState.locallyCommitted,
  'sending' => OfflineWriteState.sending,
  'confirmed' => OfflineWriteState.confirmed,
  'retryScheduled' => OfflineWriteState.retryScheduled,
  'pausedForAuth' => OfflineWriteState.pausedForAuth,
  'deadLetter' => OfflineWriteState.deadLetter,
  'expired' => OfflineWriteState.expired,
  'outcomeUnknown' => OfflineWriteState.outcomeUnknown,
  _ => throw const OfflineCodecException(),
};
