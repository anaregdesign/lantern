import 'dart:async';

import 'types.dart';

/// Storage-neutral transaction port used by [OfflineLanternRepository].
///
/// Implementations must serialize transactions and expose their effects only
/// after the callback completes successfully. A transaction must provide
/// defensive ownership for mutable bytes.
abstract interface class OfflineStore {
  /// Runs [action] against one serializable transaction.
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  );

  /// Emits coarse post-commit changes for a partition.
  Stream<OfflineStoreChange> changes(String partitionId);
}

/// One storage transaction.
abstract interface class OfflineStoreTransaction {
  /// Returns the current partition generation, creating it at zero if needed.
  int generation(String partitionId);

  /// Reads one confirmed cache record.
  OfflineCacheRecord? getCache(String partitionId, OfflineEntityKey key);

  /// Stores one confirmed cache record subject to cache capacity/LRU policy.
  void putCache(String partitionId, OfflineCacheRecord record);

  /// Removes one cache record.
  void deleteCache(String partitionId, OfflineEntityKey key);

  /// Updates local LRU access metadata without publishing a semantic change.
  void touchCache(
    String partitionId,
    OfflineEntityKey key,
    DateTime accessedAt,
  );

  /// Reads all live and terminal outbox records for an identity in FIFO order.
  List<OfflineOutboxRecord> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  );

  /// Reads one durable outbox record.
  OfflineOutboxRecord? getOutbox(String partitionId, String recordId);

  /// Reads all durable records in durable ordinal order.
  List<OfflineOutboxRecord> outbox(String partitionId);

  /// Adds one unconfirmed mutation and assigns its partition-monotone ordinal.
  OfflineOutboxRecord enqueue(OfflineOutboxRecord record);

  /// Atomically adds one logical operation under one shared FIFO ordinal.
  List<OfflineOutboxRecord> enqueueAll(List<OfflineOutboxRecord> records);

  /// Replaces one existing durable record.
  void updateOutbox(OfflineOutboxRecord record);

  /// Removes one terminal confirmed record.
  void deleteOutbox(String partitionId, String recordId);

  /// Reads one durable logical-operation aggregate.
  OfflineOperationRecord? getOperation(String partitionId, String operationId);

  /// Reads every durable operation aggregate in update order.
  List<OfflineOperationRecord> operations(String partitionId);

  /// Inserts or replaces one operation aggregate under the active generation.
  void putOperation(OfflineOperationRecord record);

  /// Removes one retained operation aggregate.
  void deleteOperation(String partitionId, String operationId);

  /// Claims FIFO-ready records on independent ordering keys with bounded leases.
  List<OfflineOutboxRecord> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration leaseDuration,
    required int limit,
  });

  /// Extends one live lease only when owner, generation, state, and old lease
  /// still match.
  bool renewLease(
    String partitionId,
    String recordId, {
    required String owner,
    required int generation,
    required DateTime now,
    required Duration leaseDuration,
  });

  /// Transactionally deletes every partition record and increments generation.
  void wipePartition(String partitionId);
}

/// A coarse partition post-commit notification.
final class OfflineStoreChange {
  /// Creates a post-commit change notification.
  const OfflineStoreChange({
    required this.partitionId,
    required this.version,
    required this.generation,
  });

  /// Partition whose data changed.
  final String partitionId;

  /// Monotone in-memory commit version for this partition.
  final int version;

  /// Partition generation after the commit.
  final int generation;
}
