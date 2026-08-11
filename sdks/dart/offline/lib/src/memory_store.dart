import 'dart:async';
import 'dart:convert';

import 'codec.dart';
import 'errors.dart';
import 'store.dart';
import 'types.dart';

/// Deterministic, non-production reference implementation of [OfflineStore].
///
/// It provides serialized copy-on-write transactions, defensive ownership,
/// partition generations, monotone ordinals, LRU confirmed-cache eviction, and
/// coarse post-commit change streams. It keeps everything in memory and must
/// not be used as a production persistence adapter.
final class InMemoryOfflineStore implements OfflineStore {
  /// Current canonical reference-store snapshot schema.
  static const int snapshotSchemaVersion = 3;

  /// Creates an empty reference store with explicit capacity bounds.
  InMemoryOfflineStore({this.limits = const OfflineStoreLimits()}) {
    _validateLimits(limits);
  }

  /// Restores a canonical test snapshot into a new process-local store.
  ///
  /// This is deterministic conformance infrastructure, not durable production
  /// persistence. Unknown schemas and malformed records fail closed.
  factory InMemoryOfflineStore.fromSnapshot(
    String snapshot, {
    OfflineStoreLimits limits = const OfflineStoreLimits(),
  }) {
    _validateLimits(limits);
    final store = InMemoryOfflineStore(limits: limits);
    store._state = _decodeMemoryState(snapshot, limits);
    return store;
  }

  /// Enforced confirmed-cache and durable-outbox bounds.
  final OfflineStoreLimits limits;
  _MemoryState _state = _MemoryState.empty();
  Future<void> _tail = Future<void>.value();
  final Map<String, StreamController<OfflineStoreChange>> _changes = {};

  @override
  Stream<OfflineStoreChange> changes(String partitionId) {
    _validatePartition(partitionId);
    return _changes
        .putIfAbsent(
          partitionId,
          () => StreamController<OfflineStoreChange>.broadcast(sync: true),
        )
        .stream;
  }

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) {
    final result = _tail.then((_) async {
      final state = _state.copy();
      final transaction = _MemoryTransaction(state, limits);
      final value = await action(transaction);
      _state = state;
      for (final partitionId in transaction._changedPartitions) {
        final partition = _state.partition(partitionId);
        partition.version++;
        _changes[partitionId]?.add(
          OfflineStoreChange(
            partitionId: partitionId,
            version: partition.version,
            generation: partition.generation,
          ),
        );
      }
      return value;
    });
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  /// Exports one canonical point-in-time test snapshot.
  ///
  /// Applications must not treat this in-memory conformance format as a
  /// production database or encryption boundary.
  Future<String> exportSnapshot() {
    final result = _tail.then((_) => _encodeMemoryState(_state));
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }
}

final class _MemoryTransaction implements OfflineStoreTransaction {
  _MemoryTransaction(this._state, this._limits);

  final _MemoryState _state;
  final OfflineStoreLimits _limits;
  final Set<String> _changedPartitions = <String>{};

  @override
  int generation(String partitionId) {
    _validatePartition(partitionId);
    return _state.partition(partitionId).generation;
  }

  @override
  OfflineCacheRecord? getCache(String partitionId, OfflineEntityKey key) {
    _validatePartition(partitionId);
    final record = _state.partition(partitionId).cache[key.canonical];
    return record == null ? null : _copyCacheRecord(record);
  }

  @override
  void putCache(String partitionId, OfflineCacheRecord record) {
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final stored = _copyCacheRecord(record);
    if (stored.partitionId != partitionId ||
        stored.generation != partition.generation) {
      throw const OfflineArgumentException();
    }
    final key = stored.key.canonical;
    final size = _cacheRecordBytes(stored);
    if (size > _limits.maxCacheBytes ||
        size > _limits.maxCacheBytesPerPartition ||
        _limits.maxCacheRecords == 0 ||
        _limits.maxCacheRecordsPerPartition == 0) {
      throw const OfflineCapacityException();
    }
    partition.cache[key] = stored;
    _evictCache(partitionId, protectedKey: key);
    _changedPartitions.add(partitionId);
  }

  @override
  void deleteCache(String partitionId, OfflineEntityKey key) {
    _validatePartition(partitionId);
    if (_state.partition(partitionId).cache.remove(key.canonical) != null) {
      _changedPartitions.add(partitionId);
    }
  }

  @override
  void touchCache(
    String partitionId,
    OfflineEntityKey key,
    DateTime accessedAt,
  ) {
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final record = partition.cache[key.canonical];
    if (record != null) {
      partition.cache[key.canonical] = record.accessedAt(accessedAt);
    }
  }

  @override
  OfflineOutboxRecord? getOutbox(String partitionId, String recordId) {
    _validatePartition(partitionId);
    final record = _state.partition(partitionId).outbox[recordId];
    return record == null ? null : _copyOutboxRecord(record);
  }

  @override
  List<OfflineOutboxRecord> outbox(String partitionId) {
    _validatePartition(partitionId);
    final records = _state.partition(partitionId).outbox.values.toList()
      ..sort(_compareOutbox);
    return records.map(_copyOutboxRecord).toList(growable: false);
  }

  @override
  List<OfflineOutboxRecord> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  ) {
    return outbox(
      partitionId,
    ).where((record) => record.intent.key == key).toList(growable: false);
  }

  @override
  OfflineOutboxRecord enqueue(OfflineOutboxRecord record) =>
      enqueueAll(<OfflineOutboxRecord>[record]).single;

  @override
  List<OfflineOutboxRecord> enqueueAll(List<OfflineOutboxRecord> records) {
    if (records.isEmpty) throw const OfflineArgumentException();
    final partitionId = records.first.partitionId;
    final operationId = records.first.operationId;
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final recordIds = <String>{};
    for (var index = 0; index < records.length; index++) {
      final record = records[index];
      if (record.partitionId != partitionId ||
          record.operationId != operationId ||
          record.itemIndex != index ||
          record.generation != partition.generation ||
          partition.outbox.containsKey(record.recordId) ||
          !recordIds.add(record.recordId)) {
        throw const OfflineArgumentException();
      }
    }
    final ordinal = partition.nextOrdinal + 1;
    final assigned = records
        .map(
          (record) => OfflineOutboxRecord(
            recordId: record.recordId,
            operationId: record.operationId,
            itemIndex: record.itemIndex,
            partitionId: record.partitionId,
            intent: record.intent,
            enqueuedAt: record.enqueuedAt,
            ordinal: ordinal,
            state: record.state,
            attemptCount: record.attemptCount,
            generation: record.generation,
            nextAttemptAt: record.nextAttemptAt,
            leaseOwner: record.leaseOwner,
            leaseUntil: record.leaseUntil,
            diagnosticCode: record.diagnosticCode,
          ),
        )
        .toList(growable: false);
    final addedBytes = assigned.fold(
      0,
      (total, record) => total + _outboxBytesFor(record),
    );
    final afterPartitionCount = partition.outbox.length + assigned.length;
    final afterPartitionBytes = _outboxBytes(partition) + addedBytes;
    final afterGlobalCount = _outboxRecordCount(_state) + assigned.length;
    final afterGlobalBytes = _outboxStateBytes(_state) + addedBytes;
    if (afterPartitionCount > _limits.maxOutboxRecordsPerPartition ||
        afterPartitionBytes > _limits.maxOutboxBytesPerPartition ||
        afterGlobalCount > _limits.maxOutboxRecords ||
        afterGlobalBytes > _limits.maxOutboxBytes) {
      throw const OfflineCapacityException();
    }
    partition.nextOrdinal = ordinal;
    for (final record in assigned) {
      partition.outbox[record.recordId] = record;
    }
    _changedPartitions.add(partitionId);
    return assigned.map(_copyOutboxRecord).toList(growable: false);
  }

  @override
  void updateOutbox(OfflineOutboxRecord record) {
    _validatePartition(record.partitionId);
    final partition = _state.partition(record.partitionId);
    final previous = partition.outbox[record.recordId];
    if (previous == null ||
        previous.ordinal != record.ordinal ||
        previous.generation != record.generation) {
      throw const OfflineArgumentException();
    }
    final replacement = _copyOutboxRecord(record);
    partition.outbox[record.recordId] = replacement;
    _changedPartitions.add(record.partitionId);
  }

  @override
  void deleteOutbox(String partitionId, String recordId) {
    _validatePartition(partitionId);
    if (_state.partition(partitionId).outbox.remove(recordId) != null) {
      _changedPartitions.add(partitionId);
    }
  }

  @override
  OfflineOperationRecord? getOperation(String partitionId, String operationId) {
    _validatePartition(partitionId);
    final record = _state.partition(partitionId).operations[operationId];
    return record == null ? null : _copyOperationRecord(record);
  }

  @override
  List<OfflineOperationRecord> operations(String partitionId) {
    _validatePartition(partitionId);
    final records = _state.partition(partitionId).operations.values.toList()
      ..sort((left, right) {
        final updated = left.updatedAt.compareTo(right.updatedAt);
        return updated != 0
            ? updated
            : left.operationId.compareTo(right.operationId);
      });
    return records.map(_copyOperationRecord).toList(growable: false);
  }

  @override
  void putOperation(OfflineOperationRecord record) {
    _validatePartition(record.partitionId);
    final partition = _state.partition(record.partitionId);
    if (record.generation != partition.generation) {
      throw const OfflineArgumentException();
    }
    partition.operations[record.operationId] = _copyOperationRecord(record);
    _evictTerminalOperations(
      protectedPartitionId: record.partitionId,
      protectedOperationId: record.operationId,
    );
    _changedPartitions.add(record.partitionId);
  }

  @override
  void deleteOperation(String partitionId, String operationId) {
    _validatePartition(partitionId);
    if (_state.partition(partitionId).operations.remove(operationId) != null) {
      _changedPartitions.add(partitionId);
    }
  }

  @override
  List<OfflineOutboxRecord> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration leaseDuration,
    required int limit,
  }) {
    _validatePartition(partitionId);
    if (owner.isEmpty || leaseDuration <= Duration.zero || limit < 1) {
      throw const OfflineArgumentException();
    }
    final partition = _state.partition(partitionId);
    final all = partition.outbox.values.toList()..sort(_compareOutbox);
    var changed = false;
    for (final record in all) {
      if (record.state == OfflineOutboxState.sending &&
          record.leaseUntil != null &&
          !now.isBefore(record.leaseUntil!)) {
        partition.outbox[record.recordId] = record.copyWith(
          state: OfflineOutboxState.enqueued,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
        );
        changed = true;
      }
    }
    final live = partition.outbox.values.toList()..sort(_compareOutbox);
    final blockedKeys = <String>{};
    final claimed = <OfflineOutboxRecord>[];
    for (final record in live) {
      final key = record.intent.key.canonical;
      if (record.state == OfflineOutboxState.sending) {
        blockedKeys.add(key);
        continue;
      }
      if (claimed.length == limit) continue;
      if (record.state != OfflineOutboxState.enqueued ||
          blockedKeys.contains(key)) {
        continue;
      }
      if (record.nextAttemptAt != null && now.isBefore(record.nextAttemptAt!)) {
        blockedKeys.add(key);
        continue;
      }
      final claimedRecord = record.copyWith(
        state: OfflineOutboxState.sending,
        leaseOwner: owner,
        leaseUntil: now.add(leaseDuration),
      );
      partition.outbox[record.recordId] = claimedRecord;
      blockedKeys.add(key);
      claimed.add(_copyOutboxRecord(claimedRecord));
      changed = true;
    }
    if (changed) _changedPartitions.add(partitionId);
    return List<OfflineOutboxRecord>.unmodifiable(claimed);
  }

  @override
  bool renewLease(
    String partitionId,
    String recordId, {
    required String owner,
    required int generation,
    required DateTime now,
    required Duration leaseDuration,
  }) {
    _validatePartition(partitionId);
    if (recordId.isEmpty || owner.isEmpty || leaseDuration <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    final partition = _state.partition(partitionId);
    final record = partition.outbox[recordId];
    if (record == null ||
        record.generation != generation ||
        partition.generation != generation ||
        record.state != OfflineOutboxState.sending ||
        record.leaseOwner != owner ||
        record.leaseUntil == null ||
        !now.isBefore(record.leaseUntil!)) {
      return false;
    }
    partition.outbox[recordId] = record.copyWith(
      leaseUntil: now.add(leaseDuration),
    );
    _changedPartitions.add(partitionId);
    return true;
  }

  @override
  void wipePartition(String partitionId) {
    _validatePartition(partitionId);
    final previous = _state.partition(partitionId);
    _state.partitions[partitionId] = _MemoryPartition(
      generation: previous.generation + 1,
    )..nextOrdinal = previous.nextOrdinal;
    _changedPartitions.add(partitionId);
  }

  void _evictCache(String partitionId, {required String protectedKey}) {
    final partition = _state.partition(partitionId);
    while (partition.cache.length > _limits.maxCacheRecordsPerPartition ||
        _cacheRecordsBytes(partition.cache.values) >
            _limits.maxCacheBytesPerPartition) {
      _evictOldestCache(
        partitionId: partitionId,
        protectedPartitionId: partitionId,
        protectedKey: protectedKey,
      );
    }

    while (_cacheRecordCount(_state) > _limits.maxCacheRecords ||
        _cacheStateBytes(_state) > _limits.maxCacheBytes) {
      _evictOldestCache(
        protectedPartitionId: partitionId,
        protectedKey: protectedKey,
      );
    }
  }

  void _evictTerminalOperations({
    required String protectedPartitionId,
    required String protectedOperationId,
  }) {
    while (_operationsOverCapacity(protectedPartitionId)) {
      final protectedPartition = _state.partition(protectedPartitionId);
      final partitionOverCapacity =
          protectedPartition.operations.length >
              _limits.maxOperationRecordsPerPartition ||
          _operationBytes(protectedPartition) >
              _limits.maxOperationBytesPerPartition;
      final candidates =
          <({String partitionId, String operationId, DateTime terminalAt})>[];
      for (final entry in _state.partitions.entries) {
        if (partitionOverCapacity && entry.key != protectedPartitionId) {
          continue;
        }
        for (final operation in entry.value.operations.values) {
          if (entry.key == protectedPartitionId &&
              operation.operationId == protectedOperationId) {
            continue;
          }
          if (entry.value.outbox.values.any(
            (record) => record.operationId == operation.operationId,
          )) {
            continue;
          }
          final terminalAt = operation.terminalAt;
          if (terminalAt != null) {
            candidates.add((
              partitionId: entry.key,
              operationId: operation.operationId,
              terminalAt: terminalAt,
            ));
          }
        }
      }
      candidates.sort((left, right) {
        final terminal = left.terminalAt.compareTo(right.terminalAt);
        if (terminal != 0) return terminal;
        final partition = left.partitionId.compareTo(right.partitionId);
        return partition != 0
            ? partition
            : left.operationId.compareTo(right.operationId);
      });
      if (candidates.isEmpty) throw const OfflineCapacityException();
      final victim = candidates.first;
      _state.partitions[victim.partitionId]!.operations.remove(
        victim.operationId,
      );
      _changedPartitions.add(victim.partitionId);
    }
  }

  bool _operationsOverCapacity(String partitionId) {
    final partition = _state.partition(partitionId);
    return partition.operations.length >
            _limits.maxOperationRecordsPerPartition ||
        _operationBytes(partition) > _limits.maxOperationBytesPerPartition ||
        _operationRecordCount(_state) > _limits.maxOperationRecords ||
        _operationStateBytes(_state) > _limits.maxOperationBytes;
  }

  void _evictOldestCache({
    String? partitionId,
    required String protectedPartitionId,
    required String protectedKey,
  }) {
    final candidates =
        <({String partitionId, String key, DateTime accessedAt})>[];
    for (final entry in _state.partitions.entries) {
      if (partitionId != null && entry.key != partitionId) continue;
      for (final record in entry.value.cache.entries) {
        if (entry.key == protectedPartitionId && record.key == protectedKey) {
          continue;
        }
        candidates.add((
          partitionId: entry.key,
          key: record.key,
          accessedAt: record.value.lastAccessAt,
        ));
      }
    }
    candidates.sort((left, right) {
      final time = left.accessedAt.compareTo(right.accessedAt);
      if (time != 0) return time;
      final partition = left.partitionId.compareTo(right.partitionId);
      return partition != 0 ? partition : left.key.compareTo(right.key);
    });
    if (candidates.isEmpty) {
      _state.partition(protectedPartitionId).cache.remove(protectedKey);
      throw const OfflineCapacityException();
    }
    final oldest = candidates.first;
    _state.partition(oldest.partitionId).cache.remove(oldest.key);
    _changedPartitions.add(oldest.partitionId);
  }
}

final class _MemoryState {
  _MemoryState(this.partitions);

  factory _MemoryState.empty() => _MemoryState(<String, _MemoryPartition>{});

  final Map<String, _MemoryPartition> partitions;

  _MemoryPartition partition(String partitionId) =>
      partitions.putIfAbsent(partitionId, _MemoryPartition.new);

  _MemoryState copy() => _MemoryState(
    partitions.map(
      (partitionId, partition) => MapEntry(partitionId, partition.copy()),
    ),
  );
}

final class _MemoryPartition {
  _MemoryPartition({this.generation = 0});

  final Map<String, OfflineCacheRecord> cache = <String, OfflineCacheRecord>{};
  final Map<String, OfflineOutboxRecord> outbox =
      <String, OfflineOutboxRecord>{};
  final Map<String, OfflineOperationRecord> operations =
      <String, OfflineOperationRecord>{};
  int generation;
  int version = 0;
  int nextOrdinal = 0;

  _MemoryPartition copy() {
    final result = _MemoryPartition(generation: generation)
      ..version = version
      ..nextOrdinal = nextOrdinal;
    result.cache.addAll(
      cache.map((key, value) => MapEntry(key, _copyCacheRecord(value))),
    );
    result.outbox.addAll(
      outbox.map((key, value) => MapEntry(key, _copyOutboxRecord(value))),
    );
    result.operations.addAll(
      operations.map(
        (key, value) => MapEntry(key, _copyOperationRecord(value)),
      ),
    );
    return result;
  }
}

OfflineCacheRecord _copyCacheRecord(OfflineCacheRecord record) =>
    record.isMissing
    ? OfflineCacheRecord.missing(
        partitionId: record.partitionId,
        generation: record.generation,
        key: record.key,
        validatedAt: record.validatedAt.toUtc(),
        lastAccessAt: record.lastAccessAt.toUtc(),
        missingUntil: record.missingUntil!.toUtc(),
        versionTag: record.versionTag,
      )
    : OfflineCacheRecord.value(
        partitionId: record.partitionId,
        generation: record.generation,
        key: record.key,
        entity: record.entity!,
        validatedAt: record.validatedAt.toUtc(),
        lastAccessAt: record.lastAccessAt.toUtc(),
        versionTag: record.versionTag,
      );

OfflineOutboxRecord _copyOutboxRecord(OfflineOutboxRecord record) =>
    OfflineOutboxRecord(
      recordId: record.recordId,
      operationId: record.operationId,
      itemIndex: record.itemIndex,
      partitionId: record.partitionId,
      intent: record.intent,
      enqueuedAt: record.enqueuedAt.toUtc(),
      ordinal: record.ordinal,
      state: record.state,
      attemptCount: record.attemptCount,
      generation: record.generation,
      nextAttemptAt: record.nextAttemptAt?.toUtc(),
      leaseOwner: record.leaseOwner,
      leaseUntil: record.leaseUntil?.toUtc(),
      diagnosticCode: record.diagnosticCode,
    );

OfflineOperationRecord _copyOperationRecord(OfflineOperationRecord record) =>
    OfflineOperationRecord(
      partitionId: record.partitionId,
      generation: record.generation,
      operationId: record.operationId,
      items: record.items
          .map(
            (item) => OfflineWriteStatus(
              recordId: item.recordId,
              operationId: item.operationId,
              itemIndex: item.itemIndex,
              state: item.state,
              attemptCount: item.attemptCount,
              diagnosticCode: item.diagnosticCode,
            ),
          )
          .toList(growable: false),
      updatedAt: record.updatedAt.toUtc(),
      terminalAt: record.terminalAt?.toUtc(),
    );

int _cacheRecordBytes(OfflineCacheRecord record) =>
    utf8.encode(OfflineCodec.encodeCacheRecord(record)).length;

int _cacheRecordsBytes(Iterable<OfflineCacheRecord> records) =>
    records.fold(0, (total, record) => total + _cacheRecordBytes(record));

int _cacheRecordCount(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + partition.cache.length,
);

int _cacheStateBytes(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + _cacheRecordsBytes(partition.cache.values),
);

int _outboxBytes(_MemoryPartition partition) => partition.outbox.values.fold(
  0,
  (total, record) => total + _outboxBytesFor(record),
);

int _outboxRecordCount(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + partition.outbox.length,
);

int _outboxStateBytes(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + _outboxBytes(partition),
);

int _outboxBytesFor(OfflineOutboxRecord record) =>
    utf8.encode(OfflineCodec.encodeOutboxRecord(record)).length;

int _operationBytes(_MemoryPartition partition) =>
    _operationRecordsBytes(partition.operations.values);

int _operationRecordsBytes(Iterable<OfflineOperationRecord> records) =>
    records.fold(0, (total, record) => total + _operationRecordBytes(record));

int _operationRecordBytes(OfflineOperationRecord record) =>
    128 +
    utf8.encode(record.partitionId).length +
    utf8.encode(record.operationId).length +
    record.items.fold(
      0,
      (total, item) => total + 192 + utf8.encode(item.recordId).length,
    );

int _operationRecordCount(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + partition.operations.length,
);

int _operationStateBytes(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + _operationBytes(partition),
);

int _compareOutbox(OfflineOutboxRecord left, OfflineOutboxRecord right) {
  final ordinal = left.ordinal.compareTo(right.ordinal);
  return ordinal != 0 ? ordinal : left.itemIndex.compareTo(right.itemIndex);
}

String _encodeMemoryState(_MemoryState state) {
  final partitionIds = state.partitions.keys.toList()..sort();
  return jsonEncode(<String, Object?>{
    'schema': InMemoryOfflineStore.snapshotSchemaVersion,
    'partitions': partitionIds
        .map((partitionId) {
          final partition = state.partitions[partitionId]!;
          final cacheKeys = partition.cache.keys.toList()..sort();
          final outbox = partition.outbox.values.toList()..sort(_compareOutbox);
          final operations = partition.operations.values.toList()
            ..sort(
              (left, right) => left.operationId.compareTo(right.operationId),
            );
          return <String, Object?>{
            'partitionId': partitionId,
            'generation': partition.generation,
            'version': partition.version,
            'nextOrdinal': partition.nextOrdinal,
            'cache': cacheKeys
                .map(
                  (key) =>
                      OfflineCodec.encodeCacheRecord(partition.cache[key]!),
                )
                .toList(growable: false),
            'outbox': outbox
                .map(OfflineCodec.encodeOutboxRecord)
                .toList(growable: false),
            'operations': operations
                .map(OfflineCodec.encodeOperationRecord)
                .toList(growable: false),
          };
        })
        .toList(growable: false),
  });
}

_MemoryState _decodeMemoryState(String source, OfflineStoreLimits limits) {
  try {
    final decoded = jsonDecode(source);
    final root = _snapshotObject(decoded);
    _expectSnapshotKeys(root, const <String>{'schema', 'partitions'});
    final schema = root['schema'];
    if (schema != 1 &&
        schema != 2 &&
        schema != InMemoryOfflineStore.snapshotSchemaVersion) {
      throw const OfflineSchemaException();
    }
    final partitionsValue = root['partitions'];
    if (partitionsValue is! List<Object?>) {
      throw const OfflineCodecException();
    }
    final state = _MemoryState.empty();
    for (final value in partitionsValue) {
      final encoded = _snapshotObject(value);
      _expectSnapshotKeys(
        encoded,
        schema == 1
            ? const <String>{
                'partitionId',
                'generation',
                'version',
                'nextOrdinal',
                'cache',
                'outbox',
              }
            : const <String>{
                'partitionId',
                'generation',
                'version',
                'nextOrdinal',
                'cache',
                'outbox',
                'operations',
              },
      );
      final partitionId = _snapshotNonEmpty(encoded['partitionId']);
      if (state.partitions.containsKey(partitionId)) {
        throw const OfflineCodecException();
      }
      final generation = _snapshotNonNegativeInt(encoded['generation']);
      final partition = _MemoryPartition(generation: generation)
        ..version = _snapshotNonNegativeInt(encoded['version'])
        ..nextOrdinal = _snapshotNonNegativeInt(encoded['nextOrdinal']);
      final cache = _snapshotStrings(encoded['cache']);
      for (final item in cache) {
        final record = OfflineCodec.decodeCacheRecord(item);
        if (record.partitionId != partitionId ||
            record.generation != generation ||
            partition.cache.containsKey(record.key.canonical)) {
          throw const OfflineCodecException();
        }
        partition.cache[record.key.canonical] = record;
      }
      final outbox = _snapshotStrings(encoded['outbox']);
      var largestOrdinal = 0;
      for (final item in outbox) {
        final record = _quarantineLegacyAdd(
          OfflineCodec.decodeOutboxRecord(item),
        );
        if (record.partitionId != partitionId ||
            record.generation != generation ||
            record.ordinal > partition.nextOrdinal ||
            partition.outbox.containsKey(record.recordId)) {
          throw const OfflineCodecException();
        }
        if (record.ordinal > largestOrdinal) largestOrdinal = record.ordinal;
        partition.outbox[record.recordId] = record;
      }
      if (largestOrdinal > partition.nextOrdinal) {
        throw const OfflineCodecException();
      }
      if (schema == 1) {
        partition.operations.addAll(
          _migrateV1Operations(
            partitionId,
            generation,
            partition.outbox.values,
          ),
        );
      } else {
        final operations = _snapshotStrings(encoded['operations']);
        for (final item in operations) {
          final operation = OfflineCodec.decodeOperationRecord(item);
          if (operation.partitionId != partitionId ||
              operation.generation != generation ||
              partition.operations.containsKey(operation.operationId)) {
            throw const OfflineCodecException();
          }
          partition.operations[operation.operationId] = operation;
        }
        _migrateLegacyAddOperations(partition);
        for (final record in partition.outbox.values) {
          final operation = partition.operations[record.operationId];
          if (operation == null ||
              record.itemIndex >= operation.items.length ||
              operation.items[record.itemIndex].recordId != record.recordId) {
            throw const OfflineCodecException();
          }
        }
      }
      state.partitions[partitionId] = partition;
    }

    _validateSnapshotCapacity(state, limits);
    return state;
  } on OfflineException {
    rethrow;
  } catch (_) {
    throw const OfflineCodecException();
  }
}

OfflineOutboxRecord _quarantineLegacyAdd(OfflineOutboxRecord record) {
  if (record.intent is! OfflineAddEdgeIntent ||
      (record.state == OfflineOutboxState.deadLetter &&
          record.leaseOwner == null &&
          record.leaseUntil == null &&
          record.nextAttemptAt == null &&
          record.diagnosticCode == 'unsupported_add')) {
    return record;
  }
  return record.copyWith(
    state: OfflineOutboxState.deadLetter,
    clearNextAttemptAt: true,
    clearLeaseOwner: true,
    clearLeaseUntil: true,
    diagnosticCode: 'unsupported_add',
  );
}

void _migrateLegacyAddOperations(_MemoryPartition partition) {
  var changed = false;
  for (final record in partition.outbox.values) {
    if (record.intent is! OfflineAddEdgeIntent) continue;
    final operation = partition.operations[record.operationId];
    if (operation == null ||
        record.itemIndex >= operation.items.length ||
        operation.items[record.itemIndex].recordId != record.recordId) {
      throw const OfflineCodecException();
    }
    final current = operation.items[record.itemIndex];
    if (current.state == OfflineWriteState.deadLetter &&
        current.attemptCount == record.attemptCount &&
        current.diagnosticCode == 'unsupported_add') {
      continue;
    }
    final items = operation.items.toList(growable: false);
    items[record.itemIndex] = OfflineWriteStatus(
      recordId: record.recordId,
      operationId: record.operationId,
      itemIndex: record.itemIndex,
      state: OfflineWriteState.deadLetter,
      attemptCount: record.attemptCount,
      diagnosticCode: 'unsupported_add',
    );
    final status = OfflineOperationStatus(
      operationId: operation.operationId,
      items: items,
    );
    final updatedAt = operation.updatedAt.isAfter(record.enqueuedAt)
        ? operation.updatedAt
        : record.enqueuedAt;
    partition.operations[operation.operationId] = OfflineOperationRecord(
      partitionId: operation.partitionId,
      generation: operation.generation,
      operationId: operation.operationId,
      items: items,
      updatedAt: updatedAt,
      terminalAt: status.isTerminal ? operation.terminalAt ?? updatedAt : null,
    );
    changed = true;
  }
  if (changed) partition.version += 1;
}

Map<String, OfflineOperationRecord> _migrateV1Operations(
  String partitionId,
  int generation,
  Iterable<OfflineOutboxRecord> outbox,
) {
  final groups = <String, List<OfflineOutboxRecord>>{};
  for (final record in outbox) {
    groups.putIfAbsent(record.operationId, () => []).add(record);
  }
  final operations = <String, OfflineOperationRecord>{};
  for (final entry in groups.entries) {
    final records = entry.value
      ..sort((left, right) => left.itemIndex.compareTo(right.itemIndex));
    final maximumIndex = records.last.itemIndex;
    final byIndex = <int, OfflineOutboxRecord>{};
    for (final record in records) {
      if (byIndex[record.itemIndex] != null) {
        throw const OfflineCodecException();
      }
      byIndex[record.itemIndex] = record;
    }
    final updatedAt = records
        .map((record) => record.enqueuedAt)
        .reduce((left, right) => left.isAfter(right) ? left : right)
        .toUtc();
    final items = List<OfflineWriteStatus>.generate(maximumIndex + 1, (index) {
      final record = byIndex[index];
      if (record == null) {
        return OfflineWriteStatus(
          recordId: 'migrated-unknown-$index',
          operationId: entry.key,
          itemIndex: index,
          state: OfflineWriteState.outcomeUnknown,
          attemptCount: 0,
          diagnosticCode: 'migrated_v1_unknown',
        );
      }
      return OfflineWriteStatus(
        recordId: record.recordId,
        operationId: record.operationId,
        itemIndex: record.itemIndex,
        state: switch (record.state) {
          OfflineOutboxState.enqueued
              when record.diagnosticCode == 'unauthenticated' =>
            OfflineWriteState.pausedForAuth,
          OfflineOutboxState.enqueued when record.attemptCount > 0 =>
            OfflineWriteState.retryScheduled,
          OfflineOutboxState.enqueued => OfflineWriteState.locallyCommitted,
          OfflineOutboxState.sending => OfflineWriteState.sending,
          OfflineOutboxState.deadLetter => OfflineWriteState.deadLetter,
          OfflineOutboxState.expired => OfflineWriteState.expired,
        },
        attemptCount: record.attemptCount,
        diagnosticCode: record.diagnosticCode,
      );
    });
    final status = OfflineOperationStatus(operationId: entry.key, items: items);
    operations[entry.key] = OfflineOperationRecord(
      partitionId: partitionId,
      generation: generation,
      operationId: entry.key,
      items: items,
      updatedAt: updatedAt,
      terminalAt: status.isTerminal ? updatedAt : null,
    );
  }
  return operations;
}

void _validateSnapshotCapacity(_MemoryState state, OfflineStoreLimits limits) {
  if (_cacheRecordCount(state) > limits.maxCacheRecords ||
      _cacheStateBytes(state) > limits.maxCacheBytes ||
      _outboxRecordCount(state) > limits.maxOutboxRecords ||
      _outboxStateBytes(state) > limits.maxOutboxBytes ||
      _operationRecordCount(state) > limits.maxOperationRecords ||
      _operationStateBytes(state) > limits.maxOperationBytes) {
    throw const OfflineCapacityException();
  }
  for (final partition in state.partitions.values) {
    if (partition.cache.length > limits.maxCacheRecordsPerPartition ||
        _cacheRecordsBytes(partition.cache.values) >
            limits.maxCacheBytesPerPartition ||
        partition.outbox.length > limits.maxOutboxRecordsPerPartition ||
        _outboxBytes(partition) > limits.maxOutboxBytesPerPartition ||
        partition.operations.length > limits.maxOperationRecordsPerPartition ||
        _operationBytes(partition) > limits.maxOperationBytesPerPartition) {
      throw const OfflineCapacityException();
    }
  }
}

Map<String, Object?> _snapshotObject(Object? value) {
  if (value is! Map<Object?, Object?>) throw const OfflineCodecException();
  final result = <String, Object?>{};
  for (final entry in value.entries) {
    final key = entry.key;
    if (key is! String) throw const OfflineCodecException();
    result[key] = entry.value;
  }
  return result;
}

List<String> _snapshotStrings(Object? value) {
  if (value is! List<Object?>) throw const OfflineCodecException();
  final result = <String>[];
  for (final item in value) {
    if (item is! String) throw const OfflineCodecException();
    result.add(item);
  }
  return result;
}

void _expectSnapshotKeys(Map<String, Object?> value, Set<String> expected) {
  if (value.length != expected.length ||
      !value.keys.toSet().containsAll(expected)) {
    throw const OfflineCodecException();
  }
}

String _snapshotNonEmpty(Object? value) {
  if (value is! String || value.isEmpty) throw const OfflineCodecException();
  return value;
}

int _snapshotNonNegativeInt(Object? value) {
  if (value is! int || value < 0 || value > 0x7fffffffffffffff) {
    throw const OfflineCodecException();
  }
  return value;
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
      limits.maxOperationBytesPerPartition < 0) {
    throw const OfflineArgumentException();
  }
}

void _validatePartition(String partitionId) {
  if (partitionId.isEmpty) throw const OfflineArgumentException();
}
