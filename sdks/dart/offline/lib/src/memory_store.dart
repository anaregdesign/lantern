import 'dart:async';
import 'dart:collection';
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
  static const int snapshotSchemaVersion = 5;

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
    StreamSubscription<OfflineStoreChange>? bridge;
    late final StreamController<OfflineStoreChange> wrapper;
    wrapper = StreamController<OfflineStoreChange>.broadcast(
      sync: true,
      onListen: () {
        try {
          bridge = _changeController(
            partitionId,
          ).stream.listen(wrapper.add, onError: wrapper.addError);
        } catch (error, stackTrace) {
          scheduleMicrotask(() {
            if (wrapper.isClosed) return;
            wrapper.addError(error, stackTrace);
            unawaited(wrapper.close());
          });
        }
      },
      onCancel: () async {
        final retiring = bridge;
        bridge = null;
        await retiring?.cancel();
      },
    );
    return wrapper.stream;
  }

  StreamController<OfflineStoreChange> _changeController(String partitionId) {
    final existing = _changes[partitionId];
    if (existing != null) return existing;
    if (_changes.length >= limits.maxChangeControllers) {
      throw const OfflineCapacityException();
    }
    late final StreamController<OfflineStoreChange> controller;
    controller = StreamController<OfflineStoreChange>.broadcast(
      sync: true,
      onCancel: () {
        if (!controller.hasListener &&
            identical(_changes[partitionId], controller)) {
          _changes.remove(partitionId);
          unawaited(controller.close());
        }
      },
    );
    _changes[partitionId] = controller;
    return controller;
  }

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) {
    final result = _tail.then((_) async {
      final state = _state.copy();
      final transaction = _MemoryTransaction(state, limits);
      late final T value;
      try {
        value = await action(transaction);
      } finally {
        transaction._seal();
      }
      for (final partitionId in transaction._changedPartitions) {
        final partition = state.partition(partitionId);
        partition.version = _checkedIncrement(partition.version);
      }
      _state = state;
      for (final partitionId in transaction._changedPartitions) {
        final partition = _state.partition(partitionId);
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
  var _sealed = false;

  void _seal() => _sealed = true;

  void _ensureOpen() {
    if (_sealed) throw const OfflineTransactionClosedException();
  }

  @override
  int generation(String partitionId) {
    _ensureOpen();
    _validatePartition(partitionId);
    return _state.partition(partitionId).generation;
  }

  @override
  bool replayPausedForAuth(String partitionId) {
    _ensureOpen();
    _validatePartition(partitionId);
    return _state.partition(partitionId).replayPausedForAuth;
  }

  @override
  void setReplayPausedForAuth(String partitionId, bool paused) {
    _ensureOpen();
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    if (partition.replayPausedForAuth == paused) return;
    partition.replayPausedForAuth = paused;
    _changedPartitions.add(partitionId);
  }

  @override
  OfflineCacheRecord? getCache(String partitionId, OfflineEntityKey key) {
    _ensureOpen();
    _validatePartition(partitionId);
    final record = _state.partition(partitionId).cache[key.canonical];
    return record == null ? null : _copyCacheRecord(record);
  }

  @override
  void putCache(String partitionId, OfflineCacheRecord record) {
    _ensureOpen();
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final stored = _copyCacheRecord(record);
    if (stored.partitionId != partitionId ||
        stored.generation != partition.generation) {
      throw const OfflineArgumentException();
    }
    final key = stored.key.canonical;
    final size = _cacheAdmissionBytesFor(stored);
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
    _ensureOpen();
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
    _ensureOpen();
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final record = partition.cache[key.canonical];
    if (record != null) {
      partition.cache[key.canonical] = record.accessedAt(accessedAt);
    }
  }

  @override
  OfflineOutboxRecord? getOutbox(String partitionId, String recordId) {
    _ensureOpen();
    _validatePartition(partitionId);
    final record = _state.partition(partitionId).outbox[recordId];
    return record == null ? null : _copyOutboxRecord(record);
  }

  @override
  List<OfflineOutboxRecord> outbox(String partitionId) {
    _ensureOpen();
    _validatePartition(partitionId);
    final records = _state.partition(partitionId).outbox.values.toList()
      ..sort(_compareOutbox);
    return records.map(_copyOutboxRecord).toList(growable: false);
  }

  @override
  OfflineOutboxScanPage scanOutbox(
    String partitionId, {
    OfflineOutboxCursor? after,
    String? operationId,
    OfflineEntityKey? key,
    required int limit,
  }) {
    _ensureOpen();
    _validatePartition(partitionId);
    if (limit < 1 || (operationId != null && key != null)) {
      throw const OfflineArgumentException();
    }
    if (operationId != null && operationId.isEmpty) {
      throw const OfflineArgumentException();
    }
    final partition = _state.partition(partitionId);
    final index = operationId != null
        ? partition.outboxByOperation[operationId]
        : key != null
        ? partition.outboxByEntity[key.canonical]
        : partition.outboxOrder;
    if (index == null || index.isEmpty) {
      return OfflineOutboxScanPage(
        records: const <OfflineOutboxRecord>[],
        nextCursor: null,
        hasMore: false,
      );
    }
    var cursor = after == null ? index.firstKey() : index.firstKeyAfter(after);
    final records = <OfflineOutboxRecord>[];
    while (cursor != null && records.length < limit) {
      final recordId = index[cursor];
      if (recordId == null) throw StateError('outbox index is inconsistent');
      final record = partition.outbox[recordId];
      if (record == null) throw StateError('outbox index is inconsistent');
      records.add(_copyOutboxRecord(record));
      cursor = index.firstKeyAfter(cursor);
    }
    final nextCursor = records.isEmpty ? null : _cursorFor(records.last);
    return OfflineOutboxScanPage(
      records: records,
      nextCursor: nextCursor,
      hasMore: cursor != null,
    );
  }

  @override
  bool hasOutboxForOperation(String partitionId, String operationId) {
    _ensureOpen();
    _validatePartition(partitionId);
    if (operationId.isEmpty) throw const OfflineArgumentException();
    return _state
            .partition(partitionId)
            .outboxByOperation[operationId]
            ?.isNotEmpty ??
        false;
  }

  @override
  List<OfflineOutboxRecord> dueOutbox(
    String partitionId, {
    String? operationId,
    OfflineEntityKey? key,
    required DateTime now,
    required Duration maxAge,
    required Duration deadLetterRetention,
    required int limit,
  }) {
    _ensureOpen();
    _validatePartition(partitionId);
    if (now != now.toUtc() ||
        maxAge <= Duration.zero ||
        deadLetterRetention <= Duration.zero ||
        limit < 1 ||
        (operationId != null && key != null) ||
        operationId?.isEmpty == true) {
      throw const OfflineArgumentException();
    }
    final partition = _state.partition(partitionId);
    final selected = <String, OfflineOutboxRecord>{};
    final scope = operationId ?? key?.canonical;
    var inspected = 0;
    void collect(
      SplayTreeMap<_DeadlineCursor, String>? index,
      bool Function(DateTime time) isDue,
    ) {
      if (index == null || inspected >= limit) return;
      var cursor = index.firstKey();
      while (cursor != null && isDue(cursor.time) && inspected < limit) {
        inspected += 1;
        final recordId = index[cursor];
        final record = recordId == null ? null : partition.outbox[recordId];
        if (record == null) throw StateError('deadline index is inconsistent');
        selected.putIfAbsent(record.recordId, () => _copyOutboxRecord(record));
        cursor = index.firstKeyAfter(cursor);
      }
    }

    collect(
      scope == null
          ? partition.expirationOrder
          : operationId != null
          ? partition.expirationByOperation[scope]
          : partition.expirationByEntity[scope],
      (time) => !now.isBefore(time),
    );
    collect(
      scope == null
          ? partition.enqueuedOrder
          : operationId != null
          ? partition.enqueuedByOperation[scope]
          : partition.enqueuedByEntity[scope],
      (time) => now.difference(time) >= maxAge,
    );
    collect(
      scope == null
          ? partition.deadLetterOrder
          : operationId != null
          ? partition.deadLetterByOperation[scope]
          : partition.deadLetterByEntity[scope],
      (time) => now.difference(time) >= deadLetterRetention,
    );
    return List<OfflineOutboxRecord>.unmodifiable(selected.values);
  }

  @override
  List<OfflineOutboxRecord> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  ) {
    _ensureOpen();
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final index = partition.outboxByEntity[key.canonical];
    if (index == null) return const <OfflineOutboxRecord>[];
    return index.values
        .map((recordId) => _copyOutboxRecord(partition.outbox[recordId]!))
        .toList(growable: false);
  }

  @override
  OfflineOutboxRecord enqueue(OfflineOutboxRecord record) =>
      enqueueAll(<OfflineOutboxRecord>[record]).single;

  @override
  List<OfflineOutboxRecord> enqueueAll(List<OfflineOutboxRecord> records) {
    _ensureOpen();
    if (records.isEmpty) throw const OfflineArgumentException();
    final partitionId = records.first.partitionId;
    final operationId = records.first.operationId;
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final recordIds = <String>{};
    if (partition.operations.containsKey(operationId) ||
        (partition.outboxByOperation[operationId]?.isNotEmpty ?? false)) {
      throw const OfflineIdentityConflictException(
        OfflineIdentityKind.operation,
      );
    }
    for (var index = 0; index < records.length; index++) {
      final record = records[index];
      if (record.intent is OfflineAddEdgeIntent) {
        throw const OfflineUnsupportedOperationException();
      }
      if (_recordIdExists(partition, record.recordId) ||
          !recordIds.add(record.recordId)) {
        throw const OfflineIdentityConflictException(
          OfflineIdentityKind.record,
        );
      }
      if (record.partitionId != partitionId ||
          record.operationId != operationId ||
          record.itemIndex != index ||
          record.generation != partition.generation) {
        throw const OfflineArgumentException();
      }
      _validateOutboxLifecycleCapacity(record, _limits);
    }
    final ordinal = _checkedIncrement(partition.nextOrdinal);
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
            deadLetteredAt: record.deadLetteredAt,
            diagnosticCode: record.diagnosticCode,
          ),
        )
        .toList(growable: false);
    final retained = assigned
        .where((record) => record.state != OfflineOutboxState.expired)
        .toList(growable: false);
    final addedBytes = retained.fold(
      0,
      (total, record) => total + _outboxAdmissionBytesFor(record, _limits),
    );
    final afterPartitionCount = partition.outbox.length + retained.length;
    final afterPartitionBytes = _outboxBytes(partition, _limits) + addedBytes;
    final afterGlobalCount = _outboxRecordCount(_state) + retained.length;
    final afterGlobalBytes = _outboxStateBytes(_state, _limits) + addedBytes;
    if (afterPartitionCount > _limits.maxOutboxRecordsPerPartition ||
        afterPartitionBytes > _limits.maxOutboxBytesPerPartition ||
        afterGlobalCount > _limits.maxOutboxRecords ||
        afterGlobalBytes > _limits.maxOutboxBytes) {
      throw const OfflineCapacityException();
    }
    partition.nextOrdinal = ordinal;
    for (final record in retained) {
      partition.putOutbox(record);
    }
    _changedPartitions.add(partitionId);
    return assigned.map(_copyOutboxRecord).toList(growable: false);
  }

  @override
  void updateOutbox(OfflineOutboxRecord record) {
    _ensureOpen();
    _validatePartition(record.partitionId);
    final partition = _state.partition(record.partitionId);
    final previous = partition.outbox[record.recordId];
    if (previous == null || !_sameOutboxIdentity(previous, record)) {
      throw const OfflineArgumentException();
    }
    final replacement = _copyOutboxRecord(record);
    _validateOutboxLifecycleCapacity(replacement, _limits);
    partition.putOutbox(replacement);
    _changedPartitions.add(record.partitionId);
  }

  @override
  void deleteOutbox(String partitionId, String recordId) {
    _ensureOpen();
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    final record = partition.outbox[recordId];
    final operation = record == null
        ? null
        : partition.operations[record.operationId];
    if (record != null &&
        operation != null &&
        !_terminalWriteState(operation.items[record.itemIndex].state)) {
      throw const OfflineArgumentException();
    }
    if (partition.removeOutbox(recordId) != null) {
      _changedPartitions.add(partitionId);
    }
  }

  @override
  OfflineOperationRecord? getOperation(String partitionId, String operationId) {
    _ensureOpen();
    _validatePartition(partitionId);
    final record = _state.partition(partitionId).operations[operationId];
    return record == null ? null : _copyOperationRecord(record);
  }

  @override
  List<OfflineOperationRecord> operations(String partitionId) {
    _ensureOpen();
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
  OfflineOperationScanPage scanOperations(
    String partitionId, {
    String? afterOperationId,
    required int limit,
  }) {
    _ensureOpen();
    _validatePartition(partitionId);
    if (limit < 1 || afterOperationId?.isEmpty == true) {
      throw const OfflineArgumentException();
    }
    final partition = _state.partition(partitionId);
    var operationId = afterOperationId == null
        ? partition.operationOrder.firstKey()
        : partition.operationOrder.firstKeyAfter(afterOperationId);
    final operations = <OfflineOperationRecord>[];
    while (operationId != null && operations.length < limit) {
      final operation = partition.operations[operationId];
      if (operation == null) {
        throw StateError('operation index is inconsistent');
      }
      operations.add(_copyOperationRecord(operation));
      operationId = partition.operationOrder.firstKeyAfter(operationId);
    }
    return OfflineOperationScanPage(
      operations: operations,
      nextOperationId: operations.isEmpty ? null : operations.last.operationId,
      hasMore: operationId != null,
    );
  }

  @override
  List<OfflineOperationRecord> dueOperations(
    String partitionId, {
    required DateTime now,
    required Duration retention,
    required int limit,
  }) {
    _ensureOpen();
    _validatePartition(partitionId);
    if (now != now.toUtc() || retention <= Duration.zero || limit < 1) {
      throw const OfflineArgumentException();
    }
    final partition = _state.partition(partitionId);
    final result = <OfflineOperationRecord>[];
    var cursor = partition.operationRetentionOrder.firstKey();
    while (cursor != null &&
        now.difference(cursor.terminalAt) >= retention &&
        result.length < limit) {
      final operation = partition.operations[cursor.operationId];
      if (operation == null) {
        throw StateError('operation retention index is inconsistent');
      }
      result.add(_copyOperationRecord(operation));
      cursor = partition.operationRetentionOrder.firstKeyAfter(cursor);
    }
    return List<OfflineOperationRecord>.unmodifiable(result);
  }

  @override
  void putOperation(OfflineOperationRecord record) {
    _atomicMutation(() {
      _ensureOpen();
      _validatePartition(record.partitionId);
      final partition = _state.partition(record.partitionId);
      if (record.generation != partition.generation) {
        throw const OfflineArgumentException();
      }
      final previous = partition.operations[record.operationId];
      if (previous != null && !_sameOperationTopology(previous, record)) {
        throw const OfflineIdentityConflictException(
          OfflineIdentityKind.operation,
        );
      }
      final itemIds = <String>{};
      for (final item in record.items) {
        final diagnostic = item.diagnosticCode;
        if (diagnostic != null &&
            utf8.encode(diagnostic).length > _limits.maxDiagnosticCodeBytes) {
          throw const OfflineCapacityException();
        }
        final owner = partition.operationByRecordId[item.recordId];
        if (!itemIds.add(item.recordId) ||
            (owner != null && owner != record.operationId)) {
          throw const OfflineIdentityConflictException(
            OfflineIdentityKind.record,
          );
        }
        final outbox = partition.outbox[item.recordId];
        if (outbox != null &&
            (outbox.operationId != record.operationId ||
                outbox.itemIndex != item.itemIndex)) {
          throw const OfflineIdentityConflictException(
            OfflineIdentityKind.record,
          );
        }
        if (!_terminalWriteState(item.state) && outbox == null) {
          throw const OfflineIdentityConflictException(
            OfflineIdentityKind.operation,
          );
        }
      }
      final retainedIds =
          partition.outboxByOperation[record.operationId]?.values;
      if (retainedIds != null) {
        for (final recordId in retainedIds) {
          final outbox = partition.outbox[recordId]!;
          if (outbox.itemIndex >= record.items.length ||
              record.items[outbox.itemIndex].recordId != recordId) {
            throw const OfflineIdentityConflictException(
              OfflineIdentityKind.operation,
            );
          }
        }
      }
      partition.putOperation(_copyOperationRecord(record));
      _evictTerminalOperations(
        protectedPartitionId: record.partitionId,
        protectedOperationId: record.operationId,
      );
      _changedPartitions.add(record.partitionId);
    });
  }

  T _atomicMutation<T>(T Function() action) {
    final previousState = _state.copy();
    final previousChanges = Set<String>.of(_changedPartitions);
    try {
      return action();
    } catch (_) {
      _state.partitions
        ..clear()
        ..addAll(previousState.partitions);
      _changedPartitions
        ..clear()
        ..addAll(previousChanges);
      rethrow;
    }
  }

  @override
  void deleteOperation(String partitionId, String operationId) {
    _ensureOpen();
    _validatePartition(partitionId);
    final partition = _state.partition(partitionId);
    if (partition.outboxByOperation[operationId]?.isNotEmpty ?? false) {
      throw const OfflineArgumentException();
    }
    if (partition.removeOperation(operationId) != null) {
      _changedPartitions.add(partitionId);
    }
  }

  @override
  List<OfflineOutboxRecord> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration maxAge,
    required Duration leaseDuration,
    required int limit,
  }) {
    _ensureOpen();
    _validatePartition(partitionId);
    if (owner.isEmpty ||
        !_isDurableTime(now) ||
        maxAge <= Duration.zero ||
        leaseDuration <= Duration.zero ||
        limit < 1) {
      throw const OfflineArgumentException();
    }
    final leaseUntil = _durableDeadline(now, leaseDuration);
    if (leaseUntil == null) return const <OfflineOutboxRecord>[];
    _validateLeaseOwnerCapacity(owner, _limits);
    final partition = _state.partition(partitionId);
    if (partition.replayPausedForAuth) {
      return const <OfflineOutboxRecord>[];
    }
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
      if (!_liveExpiration(record.absoluteExpiration, now) ||
          now.isBefore(record.enqueuedAt) ||
          now.difference(record.enqueuedAt) >= maxAge) {
        blockedKeys.add(key);
        continue;
      }
      if (record.nextAttemptAt != null && now.isBefore(record.nextAttemptAt!)) {
        blockedKeys.add(key);
        continue;
      }
      final claimedRecord = record.copyWith(
        state: OfflineOutboxState.sending,
        clearNextAttemptAt: true,
        leaseOwner: owner,
        leaseUntil: leaseUntil,
      );
      _validateOutboxLifecycleCapacity(claimedRecord, _limits);
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
    _ensureOpen();
    _validatePartition(partitionId);
    if (recordId.isEmpty ||
        owner.isEmpty ||
        !_isDurableTime(now) ||
        leaseDuration <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    _validateLeaseOwnerCapacity(owner, _limits);
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
    final candidate = _durableDeadline(now, leaseDuration);
    if (candidate == null) return true;
    if (!candidate.isAfter(record.leaseUntil!)) {
      // A wall-clock rollback must never shorten a live lease. The caller
      // still owns a valid lease, so report success without publishing a
      // semantic change when there is nothing to extend.
      return true;
    }
    final renewed = record.copyWith(leaseUntil: candidate);
    _validateOutboxLifecycleCapacity(renewed, _limits);
    partition.outbox[recordId] = renewed;
    _changedPartitions.add(partitionId);
    return true;
  }

  @override
  void wipePartition(String partitionId) {
    _ensureOpen();
    _validatePartition(partitionId);
    final previous = _state.partition(partitionId);
    _state.partitions[partitionId] =
        _MemoryPartition(generation: _checkedIncrement(previous.generation))
          ..version = previous.version
          ..nextOrdinal = previous.nextOrdinal;
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
          _operationBytes(protectedPartition, _limits) >
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
      _state.partitions[victim.partitionId]!.removeOperation(
        victim.operationId,
      );
      _changedPartitions.add(victim.partitionId);
    }
  }

  bool _operationsOverCapacity(String partitionId) {
    final partition = _state.partition(partitionId);
    return partition.operations.length >
            _limits.maxOperationRecordsPerPartition ||
        _operationBytes(partition, _limits) >
            _limits.maxOperationBytesPerPartition ||
        _operationRecordCount(_state) > _limits.maxOperationRecords ||
        _operationStateBytes(_state, _limits) > _limits.maxOperationBytes;
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

bool _recordIdExists(_MemoryPartition partition, String recordId) =>
    partition.outbox.containsKey(recordId) ||
    partition.operationByRecordId.containsKey(recordId);

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
  final Map<String, String> operationByRecordId = <String, String>{};
  final SplayTreeMap<OfflineOutboxCursor, String> outboxOrder =
      SplayTreeMap<OfflineOutboxCursor, String>(_compareOutboxCursor);
  final Map<String, SplayTreeMap<OfflineOutboxCursor, String>>
  outboxByOperation = <String, SplayTreeMap<OfflineOutboxCursor, String>>{};
  final Map<String, SplayTreeMap<OfflineOutboxCursor, String>> outboxByEntity =
      <String, SplayTreeMap<OfflineOutboxCursor, String>>{};
  final SplayTreeMap<String, bool> operationOrder =
      SplayTreeMap<String, bool>();
  final SplayTreeMap<_DeadlineCursor, String> expirationOrder =
      SplayTreeMap<_DeadlineCursor, String>(_compareDeadlineCursor);
  final Map<String, SplayTreeMap<_DeadlineCursor, String>>
  expirationByOperation = <String, SplayTreeMap<_DeadlineCursor, String>>{};
  final Map<String, SplayTreeMap<_DeadlineCursor, String>> expirationByEntity =
      <String, SplayTreeMap<_DeadlineCursor, String>>{};
  final SplayTreeMap<_DeadlineCursor, String> enqueuedOrder =
      SplayTreeMap<_DeadlineCursor, String>(_compareDeadlineCursor);
  final Map<String, SplayTreeMap<_DeadlineCursor, String>> enqueuedByOperation =
      <String, SplayTreeMap<_DeadlineCursor, String>>{};
  final Map<String, SplayTreeMap<_DeadlineCursor, String>> enqueuedByEntity =
      <String, SplayTreeMap<_DeadlineCursor, String>>{};
  final SplayTreeMap<_DeadlineCursor, String> deadLetterOrder =
      SplayTreeMap<_DeadlineCursor, String>(_compareDeadlineCursor);
  final Map<String, SplayTreeMap<_DeadlineCursor, String>>
  deadLetterByOperation = <String, SplayTreeMap<_DeadlineCursor, String>>{};
  final Map<String, SplayTreeMap<_DeadlineCursor, String>> deadLetterByEntity =
      <String, SplayTreeMap<_DeadlineCursor, String>>{};
  final SplayTreeMap<_OperationRetentionCursor, String>
  operationRetentionOrder = SplayTreeMap<_OperationRetentionCursor, String>(
    _compareOperationRetentionCursor,
  );
  final Map<String, _OperationRetentionCursor> operationRetentionById =
      <String, _OperationRetentionCursor>{};
  int generation;
  int version = 0;
  int nextOrdinal = 0;
  bool replayPausedForAuth = false;

  void putOutbox(OfflineOutboxRecord record) {
    final previous = outbox[record.recordId];
    if (previous != null) _removeOutboxIndex(previous);
    outbox[record.recordId] = record;
    final cursor = _cursorFor(record);
    outboxOrder[cursor] = record.recordId;
    (outboxByOperation[record.operationId] ??=
            SplayTreeMap<OfflineOutboxCursor, String>(
              _compareOutboxCursor,
            ))[cursor] =
        record.recordId;
    (outboxByEntity[record.intent.key.canonical] ??=
            SplayTreeMap<OfflineOutboxCursor, String>(
              _compareOutboxCursor,
            ))[cursor] =
        record.recordId;
    _addDeadlineIndexes(record);
    _refreshOperationRetention(record.operationId);
  }

  OfflineOutboxRecord? removeOutbox(String recordId) {
    final previous = outbox.remove(recordId);
    if (previous != null) _removeOutboxIndex(previous);
    return previous;
  }

  void _removeOutboxIndex(OfflineOutboxRecord record) {
    final cursor = _cursorFor(record);
    outboxOrder.remove(cursor);
    final operation = outboxByOperation[record.operationId];
    operation?.remove(cursor);
    if (operation?.isEmpty ?? false) {
      outboxByOperation.remove(record.operationId);
    }
    final entityId = record.intent.key.canonical;
    final entity = outboxByEntity[entityId];
    entity?.remove(cursor);
    if (entity?.isEmpty ?? false) outboxByEntity.remove(entityId);
    _removeDeadlineIndexes(record);
    _refreshOperationRetention(record.operationId);
  }

  void putOperation(OfflineOperationRecord operation) {
    _removeOperationRetention(operation.operationId);
    final previous = operations[operation.operationId];
    if (previous != null) {
      for (final item in previous.items) {
        operationByRecordId.remove(item.recordId);
      }
    }
    operations[operation.operationId] = operation;
    for (final item in operation.items) {
      operationByRecordId[item.recordId] = operation.operationId;
    }
    operationOrder[operation.operationId] = true;
    _refreshOperationRetention(operation.operationId);
  }

  OfflineOperationRecord? removeOperation(String operationId) {
    _removeOperationRetention(operationId);
    operationOrder.remove(operationId);
    final removed = operations.remove(operationId);
    if (removed != null) {
      for (final item in removed.items) {
        operationByRecordId.remove(item.recordId);
      }
    }
    return removed;
  }

  void _addDeadlineIndexes(OfflineOutboxRecord record) {
    if (record.state == OfflineOutboxState.enqueued ||
        record.state == OfflineOutboxState.sending) {
      final expiration = record.absoluteExpiration;
      if (expiration != null) {
        _addDeadline(
          expirationOrder,
          expirationByOperation,
          expirationByEntity,
          _deadlineCursor(record, expiration),
          record,
        );
      }
      _addDeadline(
        enqueuedOrder,
        enqueuedByOperation,
        enqueuedByEntity,
        _deadlineCursor(record, record.enqueuedAt),
        record,
      );
    } else if (record.state == OfflineOutboxState.deadLetter) {
      _addDeadline(
        deadLetterOrder,
        deadLetterByOperation,
        deadLetterByEntity,
        _deadlineCursor(record, record.deadLetteredAt!),
        record,
      );
    }
  }

  void _removeDeadlineIndexes(OfflineOutboxRecord record) {
    final expiration = record.absoluteExpiration;
    if (expiration != null) {
      _removeDeadline(
        expirationOrder,
        expirationByOperation,
        expirationByEntity,
        _deadlineCursor(record, expiration),
        record,
      );
    }
    _removeDeadline(
      enqueuedOrder,
      enqueuedByOperation,
      enqueuedByEntity,
      _deadlineCursor(record, record.enqueuedAt),
      record,
    );
    final deadLetteredAt = record.deadLetteredAt;
    if (deadLetteredAt != null) {
      _removeDeadline(
        deadLetterOrder,
        deadLetterByOperation,
        deadLetterByEntity,
        _deadlineCursor(record, deadLetteredAt),
        record,
      );
    }
  }

  void _refreshOperationRetention(String operationId) {
    _removeOperationRetention(operationId);
    final operation = operations[operationId];
    final terminalAt = operation?.terminalAt;
    if (operation == null ||
        terminalAt == null ||
        (outboxByOperation[operationId]?.isNotEmpty ?? false)) {
      return;
    }
    final cursor = _OperationRetentionCursor(terminalAt, operationId);
    operationRetentionById[operationId] = cursor;
    operationRetentionOrder[cursor] = operationId;
  }

  void _removeOperationRetention(String operationId) {
    final cursor = operationRetentionById.remove(operationId);
    if (cursor != null) operationRetentionOrder.remove(cursor);
  }

  void rebuildIndexes() {
    outboxOrder.clear();
    outboxByOperation.clear();
    outboxByEntity.clear();
    operationOrder.clear();
    operationByRecordId.clear();
    expirationOrder.clear();
    expirationByOperation.clear();
    expirationByEntity.clear();
    enqueuedOrder.clear();
    enqueuedByOperation.clear();
    enqueuedByEntity.clear();
    deadLetterOrder.clear();
    deadLetterByOperation.clear();
    deadLetterByEntity.clear();
    operationRetentionOrder.clear();
    operationRetentionById.clear();
    for (final record in outbox.values) {
      final cursor = _cursorFor(record);
      outboxOrder[cursor] = record.recordId;
      (outboxByOperation[record.operationId] ??=
              SplayTreeMap<OfflineOutboxCursor, String>(
                _compareOutboxCursor,
              ))[cursor] =
          record.recordId;
      (outboxByEntity[record.intent.key.canonical] ??=
              SplayTreeMap<OfflineOutboxCursor, String>(
                _compareOutboxCursor,
              ))[cursor] =
          record.recordId;
      _addDeadlineIndexes(record);
    }
    for (final operationId in operations.keys) {
      operationOrder[operationId] = true;
      for (final item in operations[operationId]!.items) {
        operationByRecordId[item.recordId] = operationId;
      }
      _refreshOperationRetention(operationId);
    }
  }

  _MemoryPartition copy() {
    final result = _MemoryPartition(generation: generation)
      ..version = version
      ..nextOrdinal = nextOrdinal
      ..replayPausedForAuth = replayPausedForAuth;
    result.cache.addAll(
      cache.map((key, value) => MapEntry(key, _copyCacheRecord(value))),
    );
    for (final record in outbox.values) {
      result.putOutbox(_copyOutboxRecord(record));
    }
    for (final operation in operations.values) {
      result.putOperation(_copyOperationRecord(operation));
    }
    return result;
  }
}

OfflineOutboxCursor _cursorFor(OfflineOutboxRecord record) =>
    OfflineOutboxCursor(
      ordinal: record.ordinal,
      itemIndex: record.itemIndex,
      recordId: record.recordId,
    );

int _compareOutboxCursor(OfflineOutboxCursor left, OfflineOutboxCursor right) {
  final ordinal = left.ordinal.compareTo(right.ordinal);
  if (ordinal != 0) return ordinal;
  final item = left.itemIndex.compareTo(right.itemIndex);
  return item != 0 ? item : left.recordId.compareTo(right.recordId);
}

final class _DeadlineCursor {
  const _DeadlineCursor(this.time, this.ordinal, this.itemIndex, this.recordId);

  final DateTime time;
  final int ordinal;
  final int itemIndex;
  final String recordId;
}

_DeadlineCursor _deadlineCursor(OfflineOutboxRecord record, DateTime time) =>
    _DeadlineCursor(time, record.ordinal, record.itemIndex, record.recordId);

int _compareDeadlineCursor(_DeadlineCursor left, _DeadlineCursor right) {
  final time = left.time.compareTo(right.time);
  if (time != 0) return time;
  final ordinal = left.ordinal.compareTo(right.ordinal);
  if (ordinal != 0) return ordinal;
  final item = left.itemIndex.compareTo(right.itemIndex);
  return item != 0 ? item : left.recordId.compareTo(right.recordId);
}

void _addDeadline(
  SplayTreeMap<_DeadlineCursor, String> all,
  Map<String, SplayTreeMap<_DeadlineCursor, String>> byOperation,
  Map<String, SplayTreeMap<_DeadlineCursor, String>> byEntity,
  _DeadlineCursor cursor,
  OfflineOutboxRecord record,
) {
  all[cursor] = record.recordId;
  (byOperation[record.operationId] ??= SplayTreeMap<_DeadlineCursor, String>(
    _compareDeadlineCursor,
  ))[cursor] = record.recordId;
  (byEntity[record.intent.key.canonical] ??=
          SplayTreeMap<_DeadlineCursor, String>(
            _compareDeadlineCursor,
          ))[cursor] =
      record.recordId;
}

void _removeDeadline(
  SplayTreeMap<_DeadlineCursor, String> all,
  Map<String, SplayTreeMap<_DeadlineCursor, String>> byOperation,
  Map<String, SplayTreeMap<_DeadlineCursor, String>> byEntity,
  _DeadlineCursor cursor,
  OfflineOutboxRecord record,
) {
  all.remove(cursor);
  final operation = byOperation[record.operationId];
  operation?.remove(cursor);
  if (operation?.isEmpty ?? false) byOperation.remove(record.operationId);
  final entityId = record.intent.key.canonical;
  final entity = byEntity[entityId];
  entity?.remove(cursor);
  if (entity?.isEmpty ?? false) byEntity.remove(entityId);
}

final class _OperationRetentionCursor {
  const _OperationRetentionCursor(this.terminalAt, this.operationId);

  final DateTime terminalAt;
  final String operationId;
}

int _compareOperationRetentionCursor(
  _OperationRetentionCursor left,
  _OperationRetentionCursor right,
) {
  final terminal = left.terminalAt.compareTo(right.terminalAt);
  return terminal != 0
      ? terminal
      : left.operationId.compareTo(right.operationId);
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
      deadLetteredAt: record.deadLetteredAt?.toUtc(),
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

int _cacheAdmissionBytesFor(OfflineCacheRecord record) {
  final base = record.accessedAt(_unixEpoch);
  return _cacheRecordBytes(base) +
      (_maxQuotedUtcTimestampJsonBytes - _quotedUnixEpochJsonBytes);
}

int _cacheRecordsBytes(Iterable<OfflineCacheRecord> records) =>
    records.fold(0, (total, record) => total + _cacheAdmissionBytesFor(record));

int _cacheRecordCount(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + partition.cache.length,
);

int _cacheStateBytes(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + _cacheRecordsBytes(partition.cache.values),
);

int _outboxBytes(_MemoryPartition partition, OfflineStoreLimits limits) =>
    partition.outbox.values.fold(
      0,
      (total, record) => total + _outboxAdmissionBytesFor(record, limits),
    );

int _outboxRecordCount(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + partition.outbox.length,
);

int _outboxStateBytes(_MemoryState state, OfflineStoreLimits limits) => state
    .partitions
    .values
    .fold(0, (total, partition) => total + _outboxBytes(partition, limits));

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

int _operationBytes(_MemoryPartition partition, OfflineStoreLimits limits) =>
    _operationRecordsBytes(partition.operations.values, limits);

int _operationRecordsBytes(
  Iterable<OfflineOperationRecord> records,
  OfflineStoreLimits limits,
) => records.fold(
  0,
  (total, record) => total + _operationAdmissionBytesFor(record, limits),
);

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

int _operationRecordCount(_MemoryState state) => state.partitions.values.fold(
  0,
  (total, partition) => total + partition.operations.length,
);

int _operationStateBytes(_MemoryState state, OfflineStoreLimits limits) => state
    .partitions
    .values
    .fold(0, (total, partition) => total + _operationBytes(partition, limits));

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

int _compareOutbox(OfflineOutboxRecord left, OfflineOutboxRecord right) {
  final ordinal = left.ordinal.compareTo(right.ordinal);
  return ordinal != 0 ? ordinal : left.itemIndex.compareTo(right.itemIndex);
}

bool _liveExpiration(DateTime? expiration, DateTime now) =>
    expiration == null || now.isBefore(expiration);

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
            'replayPausedForAuth': partition.replayPausedForAuth,
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
    final schemaValue = root['schema'];
    if (schemaValue is! int) throw const OfflineSchemaException();
    final schema = schemaValue;
    if (schema != 1 &&
        schema != 2 &&
        schema != 3 &&
        schema != 4 &&
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
            : schema == InMemoryOfflineStore.snapshotSchemaVersion
            ? const <String>{
                'partitionId',
                'generation',
                'version',
                'nextOrdinal',
                'replayPausedForAuth',
                'cache',
                'outbox',
                'operations',
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
        ..nextOrdinal = _snapshotNonNegativeInt(encoded['nextOrdinal'])
        ..replayPausedForAuth =
            schema == InMemoryOfflineStore.snapshotSchemaVersion
            ? _snapshotBool(encoded['replayPausedForAuth'])
            : false;
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
        final decodedRecord = OfflineCodec.decodeOutboxRecord(item);
        if (schema >= 4 && decodedRecord.intent is OfflineAddEdgeIntent) {
          throw const OfflineCodecException();
        }
        final record = schema < 4
            ? _quarantineLegacyAdd(decodedRecord)
            : decodedRecord;
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
            limits,
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
        if (schema < 4) _migrateLegacyAddOperations(partition);
        for (final record in partition.outbox.values) {
          final operation = partition.operations[record.operationId];
          if (operation == null ||
              record.itemIndex >= operation.items.length ||
              operation.items[record.itemIndex].recordId != record.recordId) {
            throw const OfflineCodecException();
          }
        }
      }
      if (schema < 4) _migrateLegacyDeadLetterTransitions(partition);
      if (schema < InMemoryOfflineStore.snapshotSchemaVersion) {
        _migrateReplayAuthPause(partition);
      }
      _validatePartitionGraph(partition, legacySnapshot: schema < 4);
      if (schema == InMemoryOfflineStore.snapshotSchemaVersion &&
          !partition.replayPausedForAuth &&
          _hasReplayAuthPauseMarker(partition)) {
        throw const OfflineCodecException();
      }
      partition.rebuildIndexes();
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

void _migrateLegacyDeadLetterTransitions(_MemoryPartition partition) {
  for (final record in partition.outbox.values.toList(growable: false)) {
    if (record.state != OfflineOutboxState.deadLetter) continue;
    final operation = partition.operations[record.operationId];
    final transition =
        operation != null && operation.updatedAt.isAfter(record.enqueuedAt)
        ? operation.updatedAt
        : record.enqueuedAt;
    partition.outbox[record.recordId] = record.copyWith(
      deadLetteredAt: transition,
    );
  }
}

void _validatePartitionGraph(
  _MemoryPartition partition, {
  required bool legacySnapshot,
}) {
  final operationByRecordId = <String, OfflineOperationRecord>{};
  for (final operation in partition.operations.values) {
    if (operation.terminalAt != null &&
        operation.terminalAt!.isAfter(operation.updatedAt)) {
      throw const OfflineCodecException();
    }
    for (final item in operation.items) {
      if (operationByRecordId.containsKey(item.recordId)) {
        throw const OfflineCodecException();
      }
      operationByRecordId[item.recordId] = operation;
    }
  }

  final ordinalOperations = <int, String>{};
  final operationOrdinals = <String, int>{};
  final expired = <String>[];
  for (final record in partition.outbox.values) {
    final operation = partition.operations[record.operationId];
    if (operation == null ||
        record.itemIndex >= operation.items.length ||
        operation.items[record.itemIndex].recordId != record.recordId ||
        operationByRecordId[record.recordId] != operation ||
        !_outboxStatusMatches(record, operation.items[record.itemIndex])) {
      throw const OfflineCodecException();
    }
    final ordinalOperation = ordinalOperations.putIfAbsent(
      record.ordinal,
      () => record.operationId,
    );
    final operationOrdinal = operationOrdinals.putIfAbsent(
      record.operationId,
      () => record.ordinal,
    );
    if (record.ordinal < 1 ||
        ordinalOperation != record.operationId ||
        operationOrdinal != record.ordinal ||
        operation.updatedAt.isBefore(record.enqueuedAt) ||
        (record.state == OfflineOutboxState.enqueued &&
            (record.leaseOwner != null || record.leaseUntil != null)) ||
        (record.state == OfflineOutboxState.sending &&
            record.nextAttemptAt != null) ||
        (record.state == OfflineOutboxState.deadLetter &&
            (record.nextAttemptAt != null ||
                record.leaseOwner != null ||
                record.leaseUntil != null)) ||
        (record.nextAttemptAt != null &&
            record.nextAttemptAt!.isBefore(record.enqueuedAt)) ||
        (record.leaseUntil != null &&
            !record.leaseUntil!.isAfter(record.enqueuedAt)) ||
        (record.deadLetteredAt != null &&
            (record.deadLetteredAt!.isBefore(record.enqueuedAt) ||
                record.deadLetteredAt!.isAfter(operation.updatedAt)))) {
      throw const OfflineCodecException();
    }
    final terminalAt = operation.terminalAt;
    if (terminalAt != null &&
        (terminalAt.isBefore(record.enqueuedAt) ||
            (record.deadLetteredAt != null &&
                terminalAt.isBefore(record.deadLetteredAt!)))) {
      throw const OfflineCodecException();
    }
    if (record.state == OfflineOutboxState.expired) {
      if (!legacySnapshot) throw const OfflineCodecException();
      expired.add(record.recordId);
    }
  }

  for (final operation in partition.operations.values) {
    for (final item in operation.items) {
      if (partition.outbox.containsKey(item.recordId)) continue;
      if (!_terminalWriteState(item.state)) {
        throw const OfflineCodecException();
      }
    }
  }
  for (final recordId in expired) {
    partition.outbox.remove(recordId);
  }
}

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
              status.state == OfflineWriteState.pausedForAuth),
    OfflineOutboxState.sending => status.state == OfflineWriteState.sending,
    OfflineOutboxState.deadLetter =>
      status.state == OfflineWriteState.deadLetter,
    OfflineOutboxState.expired => status.state == OfflineWriteState.expired,
  };
}

void _migrateReplayAuthPause(_MemoryPartition partition) {
  var paused = false;
  for (final record in partition.outbox.values.toList(growable: false)) {
    if (record.state != OfflineOutboxState.enqueued) continue;
    final operation = partition.operations[record.operationId];
    final item = operation != null && record.itemIndex < operation.items.length
        ? operation.items[record.itemIndex]
        : null;
    final operationPaused =
        item?.recordId == record.recordId &&
        item?.state == OfflineWriteState.pausedForAuth &&
        item?.diagnosticCode == 'unauthenticated';
    final outboxPaused = record.diagnosticCode == 'unauthenticated';
    if (!operationPaused && !outboxPaused) continue;
    paused = true;
    if (record.diagnosticCode != 'unauthenticated') {
      partition.outbox[record.recordId] = record.copyWith(
        diagnosticCode: 'unauthenticated',
      );
    }
  }
  partition.replayPausedForAuth = paused;
}

bool _hasReplayAuthPauseMarker(_MemoryPartition partition) {
  for (final record in partition.outbox.values) {
    if (record.state != OfflineOutboxState.enqueued) continue;
    final operation = partition.operations[record.operationId];
    if (operation == null || record.itemIndex >= operation.items.length) {
      continue;
    }
    final status = operation.items[record.itemIndex];
    if (record.diagnosticCode == 'unauthenticated' ||
        (status.state == OfflineWriteState.pausedForAuth &&
            status.diagnosticCode == 'unauthenticated')) {
      return true;
    }
  }
  return false;
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
    deadLetteredAt: record.enqueuedAt,
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
  if (changed) partition.version = _checkedIncrement(partition.version);
}

Map<String, OfflineOperationRecord> _migrateV1Operations(
  String partitionId,
  int generation,
  Iterable<OfflineOutboxRecord> outbox,
  OfflineStoreLimits limits,
) {
  final recordsByOperation = outbox.toList(growable: false);
  final groups = <String, List<OfflineOutboxRecord>>{};
  final usedRecordIds = <String>{
    for (final record in recordsByOperation) record.recordId,
  };
  for (final record in recordsByOperation) {
    groups.putIfAbsent(record.operationId, () => []).add(record);
  }
  final operations = <String, OfflineOperationRecord>{};
  final operationIds = groups.keys.toList(growable: false)..sort();
  for (final operationId in operationIds) {
    final records = groups[operationId]!
      ..sort((left, right) => left.itemIndex.compareTo(right.itemIndex));
    final maximumIndex = records.last.itemIndex;
    final operationByteLimit =
        limits.maxOperationBytes < limits.maxOperationBytesPerPartition
        ? limits.maxOperationBytes
        : limits.maxOperationBytesPerPartition;
    final capacityBound = operationByteLimit ~/ _minimumOperationItemBytes;
    final maximumItemsByCapacity = capacityBound < _maxLegacyMigrationItems
        ? capacityBound
        : _maxLegacyMigrationItems;
    if (maximumIndex >= maximumItemsByCapacity ||
        maximumIndex >= _maxDurableInt) {
      throw const OfflineCapacityException();
    }
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
          recordId: _allocateV1UnknownRecordId(
            operationId,
            index,
            usedRecordIds,
          ),
          operationId: operationId,
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
    final status = OfflineOperationStatus(
      operationId: operationId,
      items: items,
    );
    operations[operationId] = OfflineOperationRecord(
      partitionId: partitionId,
      generation: generation,
      operationId: operationId,
      items: items,
      updatedAt: updatedAt,
      terminalAt: status.isTerminal ? updatedAt : null,
    );
  }
  return operations;
}

String _allocateV1UnknownRecordId(
  String operationId,
  int itemIndex,
  Set<String> usedRecordIds,
) {
  final base = 'migrated-unknown-$operationId-$itemIndex';
  var candidate = base;
  var collision = 0;
  while (!usedRecordIds.add(candidate)) {
    collision += 1;
    candidate = '$base-$collision';
  }
  return candidate;
}

void _validateSnapshotCapacity(_MemoryState state, OfflineStoreLimits limits) {
  if (_cacheRecordCount(state) > limits.maxCacheRecords ||
      _cacheStateBytes(state) > limits.maxCacheBytes ||
      _outboxRecordCount(state) > limits.maxOutboxRecords ||
      _outboxStateBytes(state, limits) > limits.maxOutboxBytes ||
      _operationRecordCount(state) > limits.maxOperationRecords ||
      _operationStateBytes(state, limits) > limits.maxOperationBytes) {
    throw const OfflineCapacityException();
  }
  for (final partition in state.partitions.values) {
    if (partition.cache.length > limits.maxCacheRecordsPerPartition ||
        _cacheRecordsBytes(partition.cache.values) >
            limits.maxCacheBytesPerPartition ||
        partition.outbox.length > limits.maxOutboxRecordsPerPartition ||
        _outboxBytes(partition, limits) > limits.maxOutboxBytesPerPartition ||
        partition.operations.length > limits.maxOperationRecordsPerPartition ||
        _operationBytes(partition, limits) >
            limits.maxOperationBytesPerPartition) {
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

bool _snapshotBool(Object? value) {
  if (value is! bool) throw const OfflineCodecException();
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

// Every canonical migrated-v1 placeholder contains five fixed JSON keys, a
// non-empty operation-scoped record ID, the `outcomeUnknown` state, and the
// `migrated_v1_unknown` diagnostic. Ninety-six bytes is a deliberately
// conservative lower bound for that encoded item. The explicit item ceiling
// additionally makes hostile sparse legacy snapshots bounded independently of
// allocator behavior before the exact post-migration byte check runs.
const int _minimumOperationItemBytes = 96;
const int _maxLegacyMigrationItems = 4096;
