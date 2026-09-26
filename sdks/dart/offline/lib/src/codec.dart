import 'dart:convert';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'types.dart';

/// Canonical strict JSON codec for offline cache and outbox records.
///
/// Each record family deliberately fails closed on unknown schema,
/// discriminator, field, float payload, range, or byte encoding. It preserves
/// every public [VertexValue] kind, including nil versus unset and exact IEEE
/// float bits. Legacy Add intents and v1 outbox records remain decodable only
/// so stores can migrate them safely.
final class OfflineCodec {
  OfflineCodec._();

  /// Current persisted JSON schema version.
  static const int schemaVersion = 1;

  /// Current outbox record schema version.
  static const int outboxSchemaVersion = 4;

  /// Current operation aggregate schema version.
  static const int operationSchemaVersion = 2;

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

  /// Decodes one strict outbox record, including the v1 retention migration.
  static OfflineOutboxRecord decodeOutboxRecord(String source) {
    final value = _decodeObject(source);
    final schema = _recordSchema(
      value,
      'outbox',
      supported: const <int>{1, 2, 3, 4},
    );
    _expectKeys(value, switch (schema) {
      1 => _outboxKeysV1,
      2 => _outboxKeysV2,
      _ => _outboxKeys,
    });
    final state = _outboxState(_string(value['state']));
    final enqueuedAt = _timeFromString(value['enqueuedAt']);
    try {
      return OfflineOutboxRecord(
        recordId: _nonEmpty(value['recordId']),
        operationId: _nonEmpty(value['operationId']),
        itemIndex: _nonNegativeInt(value['itemIndex']),
        partitionId: _nonEmpty(value['partitionId']),
        intent: _intentFromMap(_object(value['intent'])),
        enqueuedAt: enqueuedAt,
        ordinal: _positiveInt(value['ordinal']),
        state: state,
        attemptCount: _nonNegativeInt(value['attemptCount']),
        generation: _nonNegativeInt(value['generation']),
        receipt: schema < 3 || value['receipt'] == null
            ? null
            : _receiptEvidenceFromMap(
                _object(value['receipt']),
                legacy: schema == 3,
              ),
        nextAttemptAt: _nullableTime(value['nextAttemptAt']),
        leaseOwner: _nullableString(value['leaseOwner']),
        leaseUntil: _nullableTime(value['leaseUntil']),
        deadLetteredAt: schema == 1
            ? state == OfflineOutboxState.deadLetter
                  ? enqueuedAt
                  : null
            : _nullableTime(value['deadLetteredAt']),
        diagnosticCode: _nullableString(value['diagnosticCode']),
      );
    } on OfflineArgumentException {
      throw const OfflineCodecException();
    }
  }

  /// Encodes one content-free durable operation aggregate.
  static String encodeOperationRecord(OfflineOperationRecord record) =>
      jsonEncode(_operationToMap(record));

  /// Enforces monotone dispatch evidence and receipt-only unsent rekeying.
  ///
  /// Both store adapters use this guard before replacing a durable outbox row.
  static bool sameOutboxIdentity(
    OfflineOutboxRecord previous,
    OfflineOutboxRecord next,
  ) {
    final oldReceipt = previous.receipt;
    final newReceipt = next.receipt;
    final replaced =
        oldReceipt != null &&
        newReceipt != null &&
        (oldReceipt.operationId != newReceipt.operationId ||
            oldReceipt.groupId != newReceipt.groupId);
    if (oldReceipt?.mayHaveDispatched == true &&
        newReceipt?.mayHaveDispatched == false) {
      return false;
    }
    if (replaced) {
      final original = oldReceipt;
      final replacement = newReceipt;
      if (original.mayHaveDispatched ||
          replacement.mayHaveDispatched ||
          previous.state != OfflineOutboxState.sending ||
          next.state != OfflineOutboxState.sending ||
          previous.leaseOwner != next.leaseOwner ||
          previous.leaseUntil != next.leaseUntil ||
          previous.attemptCount != next.attemptCount ||
          previous.nextAttemptAt != next.nextAttemptAt ||
          previous.diagnosticCode != next.diagnosticCode ||
          original.reconciliationAttemptCount !=
              replacement.reconciliationAttemptCount ||
          original.state ==
              OfflineReceiptReconciliationState.noLongerProvable ||
          replacement.state !=
              OfflineReceiptReconciliationState.statusRequired) {
        return false;
      }
    }

    OfflineOutboxRecord normalized(OfflineOutboxRecord record) =>
        record.copyWith(
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          receipt: record.receipt?.copyWith(
            operationId: replaced ? newReceipt.operationId : null,
            groupId: replaced ? newReceipt.groupId : null,
            state: OfflineReceiptReconciliationState.statusRequired,
            mayHaveDispatched: true,
            reconciliationAttemptCount: 0,
          ),
          clearNextAttemptAt: true,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          clearDeadLetteredAt: true,
          clearDiagnosticCode: true,
        );

    return encodeOutboxRecord(normalized(previous)) ==
        encodeOutboxRecord(normalized(next));
  }

  /// Decodes one strict durable operation aggregate.
  static OfflineOperationRecord decodeOperationRecord(String source) {
    final value = _decodeObject(source);
    final schema = _recordSchema(
      value,
      'operation',
      supported: const <int>{1, 2},
    );
    _expectKeys(value, schema == 1 ? _operationKeysV1 : _operationKeys);
    final operationId = _nonEmpty(value['operationId']);
    final rawItems = value['items'];
    if (rawItems is! List<Object?> || rawItems.isEmpty) {
      throw const OfflineCodecException();
    }
    try {
      final items = <OfflineWriteStatus>[];
      for (final rawItem in rawItems) {
        final item = _object(rawItem);
        _expectKeys(
          item,
          schema == 1 ? _operationItemKeysV1 : _operationItemKeys,
        );
        items.add(
          OfflineWriteStatus(
            recordId: _nonEmpty(item['recordId']),
            operationId: operationId,
            itemIndex: _nonNegativeInt(item['itemIndex']),
            state: _writeState(_string(item['state'])),
            attemptCount: _nonNegativeInt(item['attemptCount']),
            receiptResult: schema == 1 || item['receiptResult'] == null
                ? null
                : _receiptResultFromMap(_object(item['receiptResult'])),
            diagnosticCode: _nullableString(item['diagnosticCode']),
          ),
        );
      }
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

const Set<String> _outboxKeysV1 = <String>{
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

const Set<String> _outboxKeysV2 = <String>{..._outboxKeysV1, 'deadLetteredAt'};

const Set<String> _outboxKeys = <String>{..._outboxKeysV2, 'receipt'};

const Set<String> _operationKeysV1 = <String>{
  'schema',
  'type',
  'partitionId',
  'generation',
  'operationId',
  'items',
  'updatedAt',
  'terminalAt',
};

const Set<String> _operationKeys = _operationKeysV1;

const Set<String> _operationItemKeysV1 = <String>{
  'recordId',
  'itemIndex',
  'state',
  'attemptCount',
  'diagnosticCode',
};

const Set<String> _operationItemKeys = <String>{
  ..._operationItemKeysV1,
  'receiptResult',
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
      'schema': OfflineCodec.outboxSchemaVersion,
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
      'receipt': record.receipt == null
          ? null
          : _receiptEvidenceToMap(record.receipt!),
      'nextAttemptAt': record.nextAttemptAt == null
          ? null
          : _timeToString(record.nextAttemptAt!),
      'leaseOwner': record.leaseOwner,
      'leaseUntil': record.leaseUntil == null
          ? null
          : _timeToString(record.leaseUntil!),
      'deadLetteredAt': record.deadLetteredAt == null
          ? null
          : _timeToString(record.deadLetteredAt!),
      'diagnosticCode': record.diagnosticCode,
    };

Map<String, Object?> _operationToMap(OfflineOperationRecord record) =>
    <String, Object?>{
      'schema': OfflineCodec.operationSchemaVersion,
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
              'receiptResult': item.receiptResult == null
                  ? null
                  : _receiptResultToMap(item.receiptResult!),
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
  OfflinePutVertexIfAbsentIntent(:final vertex) => <String, Object?>{
    'kind': 'putVertexIfAbsent',
    'entity': _entityToMap(vertex),
    'contributionId': null,
  },
  OfflineDeleteVertexIntent(:final vertexKey) => <String, Object?>{
    'kind': 'deleteVertex',
    'entity': <String, Object?>{
      'kind': 'vertex',
      'key': vertexKey,
      'value': null,
      'expiration': null,
      'tail': null,
      'head': null,
      'weight': null,
    },
    'contributionId': null,
  },
  OfflineDeleteEdgeIntent(:final edge) => <String, Object?>{
    'kind': 'deleteEdge',
    'entity': <String, Object?>{
      'kind': 'edge',
      'key': null,
      'value': null,
      'expiration': null,
      'tail': edge.tail,
      'head': edge.head,
      'weight': null,
    },
    'contributionId': null,
  },
  OfflinePutEdgeIntent(:final edge) => <String, Object?>{
    'kind': 'putEdge',
    'entity': _entityToMap(edge),
    'contributionId': null,
  },
  OfflineReceiptAddEdgeIntent(:final edge, :final contributionId) =>
    <String, Object?>{
      'kind': 'receiptAddEdge',
      'entity': _entityToMap(edge),
      'contributionId': _base64UrlNoPadding(contributionId),
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
  _expectKeys(entityMap, const <String>{
    'kind',
    'key',
    'value',
    'expiration',
    'tail',
    'head',
    'weight',
  });
  return switch (kind) {
    'putVertex' when value['contributionId'] == null => () {
      final key = OfflineEntityKey.vertex(_string(entityMap['key']));
      return OfflinePutVertexIntent(
        _entityFromMap(entityMap, expectedKey: key) as Vertex,
      );
    }(),
    'putVertexIfAbsent' when value['contributionId'] == null => () {
      final key = OfflineEntityKey.vertex(_string(entityMap['key']));
      return OfflinePutVertexIfAbsentIntent(
        _entityFromMap(entityMap, expectedKey: key) as Vertex,
      );
    }(),
    'deleteVertex'
        when value['contributionId'] == null &&
            entityMap['kind'] == 'vertex' &&
            entityMap['value'] == null &&
            entityMap['expiration'] == null &&
            entityMap['tail'] == null &&
            entityMap['head'] == null &&
            entityMap['weight'] == null =>
      OfflineDeleteVertexIntent(_nonEmpty(entityMap['key'])),
    'deleteEdge'
        when value['contributionId'] == null &&
            entityMap['kind'] == 'edge' &&
            entityMap['key'] == null &&
            entityMap['value'] == null &&
            entityMap['expiration'] == null &&
            entityMap['weight'] == null =>
      OfflineDeleteEdgeIntent(
        EdgeRef(_nonEmpty(entityMap['tail']), _nonEmpty(entityMap['head'])),
      ),
    'putEdge' when value['contributionId'] == null => () {
      final key = OfflineEntityKey.edge(
        _string(entityMap['tail']),
        _string(entityMap['head']),
      );
      return OfflinePutEdgeIntent(
        _entityFromMap(entityMap, expectedKey: key) as Edge,
      );
    }(),
    'receiptAddEdge' => () {
      final key = OfflineEntityKey.edge(
        _string(entityMap['tail']),
        _string(entityMap['head']),
      );
      return OfflineReceiptAddEdgeIntent(
        _entityFromMap(entityMap, expectedKey: key) as Edge,
        _base64(value['contributionId'], length: 24),
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

Map<String, Object?> _receiptEvidenceToMap(OfflineReceiptEvidence evidence) =>
    <String, Object?>{
      'operationId': _base64UrlNoPadding(evidence.operationId.bytes),
      'groupId': _base64UrlNoPadding(evidence.groupId.bytes),
      'nodeId': _base64UrlNoPadding(evidence.endpoint.nodeId),
      'generation': _base64UrlNoPadding(evidence.endpoint.generation),
      'mutation': evidence.mutation.name,
      'itemIndex': evidence.itemIndex,
      'itemCount': evidence.itemCount,
      'state': evidence.state.name,
      'mayHaveDispatched': evidence.mayHaveDispatched,
      'reconciliationAttemptCount': evidence.reconciliationAttemptCount,
      'policy': <String, Object?>{
        'deploymentEpoch': _base64UrlNoPadding(
          evidence.policy.deploymentEpoch.bytes,
        ),
        'retentionMicros': evidence.policy.retention.inMicroseconds.toString(),
        'maxEntries': evidence.policy.maxEntries.toString(),
        'maxBytes': evidence.policy.maxBytes.toString(),
        'fingerprint': _base64UrlNoPadding(evidence.policy.fingerprint),
      },
    };

OfflineReceiptEvidence _receiptEvidenceFromMap(
  Map<String, Object?> value, {
  required bool legacy,
}) {
  _expectKeys(value, <String>{
    'operationId',
    'groupId',
    'nodeId',
    'generation',
    'mutation',
    'itemIndex',
    'itemCount',
    'state',
    if (!legacy) 'mayHaveDispatched',
    'reconciliationAttemptCount',
    'policy',
  });
  final policy = _object(value['policy']);
  _expectKeys(policy, const <String>{
    'deploymentEpoch',
    'retentionMicros',
    'maxEntries',
    'maxBytes',
    'fingerprint',
  });
  try {
    return OfflineReceiptEvidence(
      operationId: ReceiptOperationId(
        _base64(value['operationId'], length: 49),
      ),
      groupId: ReceiptGroupId(_base64(value['groupId'], length: 16)),
      endpoint: ReceiptEndpoint(
        nodeId: _base64(value['nodeId'], length: 16),
        generation: _base64(value['generation'], length: 16),
      ),
      mutation: _receiptMutation(_string(value['mutation'])),
      policy: OfflineReceiptPolicy(
        deploymentEpoch: ReceiptEpoch(
          _base64(policy['deploymentEpoch'], length: 16),
        ),
        retention: Duration(
          microseconds: _decimalInt(
            policy['retentionMicros'],
            1,
            0x7fffffffffffffff,
          ),
        ),
        maxEntries: _decimalBigInt(
          policy['maxEntries'],
          BigInt.one,
          (BigInt.one << 64) - BigInt.one,
        ),
        maxBytes: _decimalBigInt(
          policy['maxBytes'],
          BigInt.one,
          (BigInt.one << 64) - BigInt.one,
        ),
        fingerprint: _base64(policy['fingerprint'], length: 32),
      ),
      itemIndex: _nonNegativeInt(value['itemIndex']),
      itemCount: _positiveInt(value['itemCount']),
      state: _receiptReconciliationState(_string(value['state'])),
      mayHaveDispatched: legacy ? true : _bool(value['mayHaveDispatched']),
      reconciliationAttemptCount: _nonNegativeInt(
        value['reconciliationAttemptCount'],
      ),
    );
  } on OfflineArgumentException {
    throw const OfflineCodecException();
  } on LanternException {
    throw const OfflineCodecException();
  }
}

Map<String, Object?> _receiptResultToMap(OfflineReceiptResult result) =>
    switch (result) {
      OfflineVertexPutReceiptResult(:final outcome) => <String, Object?>{
        'kind': 'vertexPut',
        'outcome': outcome.name,
        'existed': null,
        'effectiveWeight': null,
      },
      OfflineVertexDeleteReceiptResult(:final existed) => <String, Object?>{
        'kind': 'vertexDelete',
        'outcome': null,
        'existed': existed,
        'effectiveWeight': null,
      },
      OfflineEdgeDeleteReceiptResult(:final existed) => <String, Object?>{
        'kind': 'edgeDelete',
        'outcome': null,
        'existed': existed,
        'effectiveWeight': null,
      },
      OfflineEdgeAddReceiptResult(:final effectiveWeight) => <String, Object?>{
        'kind': 'edgeAdd',
        'outcome': null,
        'existed': null,
        'effectiveWeight': _floatBits(effectiveWeight, 4, allowNonFinite: true),
      },
    };

OfflineReceiptResult _receiptResultFromMap(Map<String, Object?> value) {
  _expectKeys(value, const <String>{
    'kind',
    'outcome',
    'existed',
    'effectiveWeight',
  });
  return switch (_string(value['kind'])) {
    'vertexPut'
        when value['existed'] == null && value['effectiveWeight'] == null =>
      OfflineVertexPutReceiptResult(_putOutcome(_string(value['outcome']))),
    'vertexDelete'
        when value['outcome'] == null && value['effectiveWeight'] == null =>
      OfflineVertexDeleteReceiptResult(_bool(value['existed'])),
    'edgeDelete'
        when value['outcome'] == null && value['effectiveWeight'] == null =>
      OfflineEdgeDeleteReceiptResult(_bool(value['existed'])),
    'edgeAdd' when value['outcome'] == null && value['existed'] == null =>
      OfflineEdgeAddReceiptResult(
        _floatFromBits(
          _string(value['effectiveWeight']),
          4,
          allowNonFinite: true,
        ),
      ),
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

String _floatBits(double value, int length, {bool allowNonFinite = false}) {
  if (!allowNonFinite && !value.isFinite) {
    throw const OfflineArgumentException();
  }
  final data = ByteData(length);
  if (length == 4) {
    data.setFloat32(
      0,
      allowNonFinite ? value : normalizeOfflineFloat32(value),
      Endian.big,
    );
  } else {
    data.setFloat64(0, value, Endian.big);
  }
  return List<String>.generate(
    length,
    (index) => data.getUint8(index).toRadixString(16).padLeft(2, '0'),
  ).join();
}

double _floatFromBits(String value, int length, {bool allowNonFinite = false}) {
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
  if (!allowNonFinite && !result.isFinite) {
    throw const OfflineCodecException();
  }
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

int _recordSchema(
  Map<String, Object?> value,
  String type, {
  required Set<int> supported,
}) {
  final schema = value['schema'];
  if (schema is! int || !supported.contains(schema) || value['type'] != type) {
    throw const OfflineCodecException();
  }
  return schema;
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
  if ((length != null && text.length != _unpaddedBase64Length(length)) ||
      !RegExp(r'^[A-Za-z0-9_-]*$').hasMatch(text)) {
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

int _unpaddedBase64Length(int byteLength) => ((byteLength * 4) + 2) ~/ 3;

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

ReceiptMutationKind _receiptMutation(String value) => switch (value) {
  'vertexPut' => ReceiptMutationKind.vertexPut,
  'vertexDelete' => ReceiptMutationKind.vertexDelete,
  'edgeDelete' => ReceiptMutationKind.edgeDelete,
  'edgeAdd' => ReceiptMutationKind.edgeAdd,
  _ => throw const OfflineCodecException(),
};

OfflineReceiptReconciliationState _receiptReconciliationState(String value) =>
    switch (value) {
      'statusRequired' => OfflineReceiptReconciliationState.statusRequired,
      'notYetObserved' => OfflineReceiptReconciliationState.notYetObserved,
      'lookupUnknown' => OfflineReceiptReconciliationState.lookupUnknown,
      'noLongerProvable' => OfflineReceiptReconciliationState.noLongerProvable,
      _ => throw const OfflineCodecException(),
    };

PutOutcome _putOutcome(String value) => switch (value) {
  'appliedAndLive' => PutOutcome.appliedAndLive,
  'expired' => PutOutcome.expired,
  'conditionNotMet' => PutOutcome.conditionNotMet,
  'superseded' => PutOutcome.superseded,
  _ => throw const OfflineCodecException(),
};
