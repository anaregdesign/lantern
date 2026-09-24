part of '../lantern_client_offline_sqlite.dart';

// Canonical lifecycle reservations mirror the storage-neutral reference contract.
bool _sameOutboxIdentity(OfflineOutboxRecord left, OfflineOutboxRecord right) {
  OfflineOutboxRecord normalized(OfflineOutboxRecord record) => record.copyWith(
    state: OfflineOutboxState.enqueued,
    attemptCount: 0,
    clearNextAttemptAt: true,
    clearLeaseOwner: true,
    clearLeaseUntil: true,
    clearDeadLetteredAt: true,
    clearDiagnosticCode: true,
  );

  return OfflineCodec.encodeOutboxRecord(normalized(left)) ==
      OfflineCodec.encodeOutboxRecord(normalized(right));
}

bool _sameOperationTopology(
  OfflineOperationRecord left,
  OfflineOperationRecord right,
) {
  if (left.partitionId != right.partitionId ||
      left.generation != right.generation ||
      left.operationId != right.operationId ||
      left.items.length != right.items.length) {
    return false;
  }
  for (var index = 0; index < left.items.length; index++) {
    final leftItem = left.items[index];
    final rightItem = right.items[index];
    if (leftItem.recordId != rightItem.recordId ||
        leftItem.operationId != rightItem.operationId ||
        leftItem.itemIndex != rightItem.itemIndex) {
      return false;
    }
  }
  return true;
}

int _cacheRecordBytes(OfflineCacheRecord record) =>
    utf8.encode(OfflineCodec.encodeCacheRecord(record)).length;

int _cacheAdmissionBytesFor(OfflineCacheRecord record) {
  final base = record.accessedAt(_unixEpoch);
  return _cacheRecordBytes(base) +
      (_maxQuotedUtcTimestampJsonBytes - _quotedUnixEpochJsonBytes);
}

int _outboxBytesFor(OfflineOutboxRecord record) =>
    utf8.encode(OfflineCodec.encodeOutboxRecord(record)).length;

int _outboxAdmissionBytesFor(
  OfflineOutboxRecord record,
  OfflineStoreLimits limits,
) {
  _validateOutboxLifecycleCapacity(record, limits);
  final base = OfflineOutboxRecord(
    recordId: record.recordId,
    operationId: record.operationId,
    itemIndex: record.itemIndex,
    partitionId: record.partitionId,
    intent: record.intent,
    enqueuedAt: record.enqueuedAt,
    ordinal: record.ordinal,
    state: OfflineOutboxState.enqueued,
    attemptCount: 0,
    generation: record.generation,
  );
  return _outboxBytesFor(base) + _outboxLifecycleReservationBytes(limits);
}

// This is a mechanical upper bound over every mutable field in the canonical
// JSON. A signed-64 attempt counter grows from `0` to at most 19 digits. The
// longest state, `deadLetter`, is two bytes longer than `enqueued`. Each of the
// three nullable timestamps grows from `null` to at most a quoted 18-byte
// decimal microsecond value within years 0001..9999. JSON string escaping
// expands one UTF-8 input byte by at most six bytes, including control chars.
const int _maxSignedInt64JsonBytes = 19;
const int _zeroJsonBytes = 1;
const int _longestStateJsonBytes = 10;
const int _baseStateJsonBytes = 8;
const int _maxQuotedUtcTimestampJsonBytes = 20;
const int _quotedUnixEpochJsonBytes = 3;
const int _nullJsonBytes = 4;
const int _mutableTimestampCount = 3;
const int _outboxFixedLifecycleGrowthBytes =
    (_maxSignedInt64JsonBytes - _zeroJsonBytes) +
    (_longestStateJsonBytes - _baseStateJsonBytes) +
    (_mutableTimestampCount *
        (_maxQuotedUtcTimestampJsonBytes - _nullJsonBytes));

int _outboxLifecycleReservationBytes(OfflineStoreLimits limits) =>
    _outboxFixedLifecycleGrowthBytes +
    _optionalJsonStringGrowth(limits.maxLeaseOwnerBytes) +
    _optionalJsonStringGrowth(limits.maxDiagnosticCodeBytes);

int _optionalJsonStringGrowth(int maxUtf8Bytes) => (6 * maxUtf8Bytes) - 2;

void _validateOutboxLifecycleCapacity(
  OfflineOutboxRecord record,
  OfflineStoreLimits limits,
) {
  final owner = record.leaseOwner;
  if (owner != null) _validateLeaseOwnerCapacity(owner, limits);
  final diagnostic = record.diagnosticCode;
  if (diagnostic != null &&
      utf8.encode(diagnostic).length > limits.maxDiagnosticCodeBytes) {
    throw const OfflineCapacityException();
  }
}

void _validateLeaseOwnerCapacity(String owner, OfflineStoreLimits limits) {
  if (utf8.encode(owner).length > limits.maxLeaseOwnerBytes) {
    throw const OfflineCapacityException();
  }
}

int _operationRecordBytes(OfflineOperationRecord record) =>
    utf8.encode(OfflineCodec.encodeOperationRecord(record)).length;

int _operationAdmissionBytesFor(
  OfflineOperationRecord record,
  OfflineStoreLimits limits,
) {
  _validateOperationLifecycleCapacity(record, limits);
  final base = OfflineOperationRecord(
    partitionId: record.partitionId,
    generation: record.generation,
    operationId: record.operationId,
    items: record.items
        .map(
          (item) => OfflineWriteStatus(
            recordId: item.recordId,
            operationId: item.operationId,
            itemIndex: item.itemIndex,
            state: OfflineWriteState.locallyCommitted,
            attemptCount: 0,
          ),
        )
        .toList(growable: false),
    updatedAt: _unixEpoch,
  );
  final perItemGrowth =
      (_maxSignedInt64JsonBytes - _zeroJsonBytes) +
      _optionalJsonStringGrowth(limits.maxDiagnosticCodeBytes);
  final timestampGrowth =
      (_maxQuotedUtcTimestampJsonBytes - _quotedUnixEpochJsonBytes) +
      (_maxQuotedUtcTimestampJsonBytes - _nullJsonBytes);
  return _operationRecordBytes(base) +
      (record.items.length * perItemGrowth) +
      timestampGrowth;
}

void _validateOperationLifecycleCapacity(
  OfflineOperationRecord record,
  OfflineStoreLimits limits,
) {
  for (final item in record.items) {
    final diagnostic = item.diagnosticCode;
    if (diagnostic != null &&
        utf8.encode(diagnostic).length > limits.maxDiagnosticCodeBytes) {
      throw const OfflineCapacityException();
    }
  }
}

final DateTime _unixEpoch = DateTime.fromMicrosecondsSinceEpoch(0, isUtc: true);
final DateTime _maximumDurableTime = DateTime.utc(
  9999,
  12,
  31,
  23,
  59,
  59,
  999,
  999,
);

bool _isDurableTime(DateTime value) =>
    value.isUtc && value.year >= 1 && value.year <= 9999;

DateTime? _durableDeadline(DateTime now, Duration duration) {
  DateTime candidate;
  try {
    candidate = now.add(duration);
  } on Object {
    candidate = _maximumDurableTime;
  }
  if (candidate.isAfter(_maximumDurableTime)) {
    candidate = _maximumDurableTime;
  }
  return candidate.isAfter(now) ? candidate : null;
}

bool _isQuarantinedLegacyAdd(OfflineOutboxRecord record) =>
    record.intent is OfflineAddEdgeIntent &&
    record.state == OfflineOutboxState.deadLetter &&
    record.nextAttemptAt == null &&
    record.leaseOwner == null &&
    record.leaseUntil == null &&
    record.deadLetteredAt != null &&
    record.diagnosticCode == 'unsupported_add';

bool _terminalWriteState(OfflineWriteState state) =>
    state == OfflineWriteState.confirmed ||
    state == OfflineWriteState.deadLetter ||
    state == OfflineWriteState.expired ||
    state == OfflineWriteState.outcomeUnknown;

bool _outboxStatusMatches(
  OfflineOutboxRecord record,
  OfflineWriteStatus status,
) {
  if (status.recordId != record.recordId ||
      status.operationId != record.operationId ||
      status.itemIndex != record.itemIndex ||
      status.attemptCount != record.attemptCount ||
      status.diagnosticCode != record.diagnosticCode) {
    return false;
  }
  return switch (record.state) {
    OfflineOutboxState.enqueued =>
      (status.state == OfflineWriteState.retryScheduled) ==
              (record.nextAttemptAt != null) &&
          (status.state == OfflineWriteState.locallyCommitted ||
              status.state == OfflineWriteState.retryScheduled ||
              (status.state == OfflineWriteState.pausedForAuth &&
                  record.diagnosticCode == 'unauthenticated')),
    OfflineOutboxState.sending => status.state == OfflineWriteState.sending,
    OfflineOutboxState.deadLetter =>
      status.state == OfflineWriteState.deadLetter,
    OfflineOutboxState.expired => status.state == OfflineWriteState.expired,
  };
}

void _validateLimits(OfflineStoreLimits limits) {
  if (limits.maxCacheRecords < 0 ||
      limits.maxCacheBytes < 0 ||
      limits.maxOutboxRecords < 0 ||
      limits.maxOutboxBytes < 0 ||
      limits.maxOperationRecords < 0 ||
      limits.maxOperationBytes < 0 ||
      limits.maxCacheRecordsPerPartition < 0 ||
      limits.maxCacheBytesPerPartition < 0 ||
      limits.maxOutboxRecordsPerPartition < 0 ||
      limits.maxOutboxBytesPerPartition < 0 ||
      limits.maxOperationRecordsPerPartition < 0 ||
      limits.maxOperationBytesPerPartition < 0 ||
      limits.maxLeaseOwnerBytes < 1 ||
      limits.maxDiagnosticCodeBytes < 19 ||
      limits.maxChangeControllers < 1) {
    throw const OfflineArgumentException();
  }
}

void _validatePartition(String partitionId) {
  if (partitionId.isEmpty) throw const OfflineArgumentException();
}

int _checkedIncrement(int value) {
  if (value < 0 || value >= _maxDurableInt) {
    throw const OfflineCapacityException();
  }
  return value + 1;
}

const int _maxDurableInt = 0x7fffffffffffffff;

// SQLite prefix projection follows Dart String.startsWith code-unit semantics,
// including a prefix that ends within a supplementary character.
Uint8List _prefixBytes(String text) {
  final bytes = Uint8List(text.length * 2);
  for (var i = 0; i < text.length; i++) {
    final unit = text.codeUnitAt(i);
    bytes[i * 2] = unit >> 8;
    bytes[i * 2 + 1] = unit & 255;
  }
  return bytes;
}

Uint8List? _prefixEnd(Uint8List prefix) {
  for (var i = prefix.length - 1; i >= 0; i--) {
    if (prefix[i] < 255) {
      final end = Uint8List.fromList(prefix.sublist(0, i + 1));
      end[i]++;
      return end;
    }
  }
  return null;
}

Uint8List _encode(String text) => Uint8List.fromList(utf8.encode(text));
String _decode(Object? value) {
  if (value is! List<int>) throw const OfflineCodecException();
  try {
    return utf8.decode(value);
  } catch (_) {
    throw const OfflineCodecException();
  }
}

OfflineCacheRecord _cache(Map<String, Object?> row) =>
    OfflineCodec.decodeCacheRecord(_decode(row['payload']));
OfflineOutboxRecord _outbox(Map<String, Object?> row) =>
    OfflineCodec.decodeOutboxRecord(_decode(row['payload']));
OfflineOperationRecord _operation(Map<String, Object?> row) =>
    OfflineCodec.decodeOperationRecord(_decode(row['payload']));

void _validateCacheIdentity(OfflineCacheRecord record) {
  if (record.isMissing) {
    if (record.missingUntil!.isBefore(record.validatedAt)) {
      throw const OfflineDurableGraphException();
    }
  } else if (record.key.kind == OfflineEntityKind.vertex
      ? record.vertex?.key != record.key.vertexKey
      : record.edge?.tail != record.key.tail ||
            record.edge?.head != record.key.head) {
    throw const OfflineDurableGraphException();
  }
}
