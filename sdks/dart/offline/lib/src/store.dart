import 'dart:async';

import 'change_store.dart';
import 'errors.dart';
import 'types.dart';

/// Storage-neutral transaction port used by [OfflineLanternRepository].
///
/// Implementations must serialize transactions and expose their effects only
/// after the callback completes successfully. Before publishing any changed
/// partition, implementations must validate its complete cache/outbox/operation
/// graph and reject an inconsistent commit atomically with
/// [OfflineDurableGraphException]. A transaction must provide defensive
/// ownership for mutable bytes. Partition identifiers are local persistence
/// namespaces; adapters must never treat them as server-side auth or tenant
/// boundaries.
abstract interface class OfflineStore {
  /// Runs [action] against one serializable transaction.
  ///
  /// A callback that returns successfully can still fail at commit with
  /// [OfflineDurableGraphException]. In that case no state or change event may
  /// escape the transaction.
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  );

  /// Emits coarse post-commit changes for a partition.
  ///
  /// Implementations must bound partition notification resources and release
  /// an idle resource after its final listener cancels.
  Stream<OfflineStoreChange> changes(String partitionId);
}

/// One storage transaction.
///
/// Every method must reject use after the enclosing [OfflineStore.transaction]
/// callback finishes, whether that callback commits or rolls back. Operations
/// may complete asynchronously; callers must await each result before continuing
/// dependent work or finishing the transaction callback.
abstract interface class OfflineStoreTransaction {
  /// Returns the current partition generation, creating it at zero if needed.
  FutureOr<int> generation(String partitionId);

  /// Whether replay is durably paused until explicit credential rotation.
  FutureOr<bool> replayPausedForAuth(String partitionId);

  /// Sets the durable partition replay pause atomically with write state.
  FutureOr<void> setReplayPausedForAuth(String partitionId, bool paused);

  /// Reads one confirmed cache record.
  FutureOr<OfflineCacheRecord?> getCache(
    String partitionId,
    OfflineEntityKey key,
  );

  /// Stores one confirmed cache record subject to cache capacity/LRU policy.
  FutureOr<void> putCache(String partitionId, OfflineCacheRecord record);

  /// Removes one cache record.
  FutureOr<void> deleteCache(String partitionId, OfflineEntityKey key);

  /// Updates local LRU access metadata without publishing a semantic change.
  FutureOr<void> touchCache(
    String partitionId,
    OfflineEntityKey key,
    DateTime accessedAt,
  );

  /// Reads all live and terminal outbox records for an identity in FIFO order.
  FutureOr<List<OfflineOutboxRecord>> outboxForKey(
    String partitionId,
    OfflineEntityKey key,
  );

  /// Reads one durable outbox record.
  FutureOr<OfflineOutboxRecord?> getOutbox(String partitionId, String recordId);

  /// Reads all durable records in durable ordinal order.
  FutureOr<List<OfflineOutboxRecord>> outbox(String partitionId);

  /// Reads at most [limit] records after a stable FIFO cursor.
  ///
  /// Supplying [operationId] or [key] scopes the page through an adapter-owned
  /// index. At most one scope may be supplied. Implementations must not inspect
  /// more than [limit] records to produce the page.
  FutureOr<OfflineOutboxScanPage> scanOutbox(
    String partitionId, {
    OfflineOutboxCursor? after,
    String? operationId,
    OfflineEntityKey? key,
    required int limit,
  });

  /// Whether an operation still owns any durable outbox record.
  FutureOr<bool> hasOutboxForOperation(String partitionId, String operationId);

  /// Returns at most [limit] records whose expiration, maximum age, or
  /// dead-letter retention deadline is due at [now].
  ///
  /// Supplying [operationId] or [key] scopes the lookup through an
  /// adapter-owned deadline index. At most one scope may be supplied. The
  /// lookup must not linearly inspect non-due records.
  FutureOr<List<OfflineOutboxRecord>> dueOutbox(
    String partitionId, {
    String? operationId,
    OfflineEntityKey? key,
    required DateTime now,
    required Duration maxAge,
    required Duration deadLetterRetention,
    required int limit,
  });

  /// Adds one unconfirmed mutation and assigns its partition-monotone ordinal.
  /// Retained operation and record IDs are globally unique within a partition;
  /// collisions throw [OfflineIdentityConflictException] atomically. Byte
  /// admission must reserve the record's full bounded lifecycle envelope so
  /// later claim, retry, and terminal transitions cannot exceed capacity.
  /// Migration-only legacy Add intents throw
  /// [OfflineUnsupportedOperationException].
  FutureOr<OfflineOutboxRecord> enqueue(OfflineOutboxRecord record);

  /// Atomically adds one logical operation under one shared FIFO ordinal.
  FutureOr<List<OfflineOutboxRecord>> enqueueAll(
    List<OfflineOutboxRecord> records,
  );

  /// Replaces one existing durable record within its admitted lifecycle
  /// envelope. Implementations reject lease owners and diagnostic codes above
  /// their explicit per-record UTF-8 bounds before mutating durable state.
  FutureOr<void> updateOutbox(OfflineOutboxRecord record);

  /// Removes one terminal confirmed record.
  FutureOr<void> deleteOutbox(String partitionId, String recordId);

  /// Reads one durable logical-operation aggregate.
  FutureOr<OfflineOperationRecord?> getOperation(
    String partitionId,
    String operationId,
  );

  /// Reads every durable operation aggregate in update order.
  FutureOr<List<OfflineOperationRecord>> operations(String partitionId);

  /// Reads at most [limit] operation aggregates after a stable identity cursor.
  ///
  /// Implementations must not inspect more than [limit] aggregates to produce
  /// the page.
  FutureOr<OfflineOperationScanPage> scanOperations(
    String partitionId, {
    String? afterOperationId,
    required int limit,
  });

  /// Returns at most [limit] unreferenced terminal aggregates whose retention
  /// deadline is due, using an adapter-owned deadline index.
  FutureOr<List<OfflineOperationRecord>> dueOperations(
    String partitionId, {
    required DateTime now,
    required Duration retention,
    required int limit,
  });

  /// Inserts or replaces one operation aggregate under the active generation.
  /// An existing aggregate may only be advanced when every item identity and
  /// index is unchanged; replacement topology throws an identity conflict.
  FutureOr<void> putOperation(OfflineOperationRecord record);

  /// Removes one retained operation aggregate.
  FutureOr<void> deleteOperation(String partitionId, String operationId);

  /// Claims FIFO-ready records on independent ordering keys with bounded leases.
  /// The public [owner] is subject to the adapter's explicit UTF-8 byte bound.
  /// The same transaction must advance each claimed aggregate item to
  /// [OfflineWriteState.sending], and must advance any expired lease recovered
  /// by the call back to a matching non-terminal status before commit.
  FutureOr<List<OfflineOutboxRecord>> claim(
    String partitionId, {
    required String owner,
    required DateTime now,
    required Duration maxAge,
    required Duration leaseDuration,
    required int limit,
  });

  /// Extends one live lease only when owner, generation, state, and old lease
  /// still match. A wall-clock rollback never shortens the existing deadline.
  FutureOr<bool> renewLease(
    String partitionId,
    String recordId, {
    required String owner,
    required int generation,
    required DateTime now,
    required Duration leaseDuration,
  });

  /// Reads the last fully applied sequence per CDC origin.
  FutureOr<OfflineChangeCursor> changeCursor(String partitionId);

  /// Durable partition-wide invalidation epoch for remote read barriers.
  ///
  /// Every accepted CDC chunk, including a partial chunk, and every checkpoint
  /// reset advances this value in the same transaction as cache invalidation.
  FutureOr<int> changeEpoch(String partitionId);

  /// Returns up to [limit] Unknown resident identities in stable key order.
  ///
  /// Repeated calls start at the first unfinished identity; resolving a key
  /// removes it durably. No graph values are loaded or returned.
  FutureOr<List<OfflineEntityKey>> unknownResidents(
    String partitionId, {
    required int limit,
  });

  /// Completes one resident revalidation only if [expectedEpoch] is current.
  ///
  /// The caller must have committed a fresh or missing [getCache] result, or
  /// confirmed that the entity has expired. A rejected completion leaves the
  /// key Unknown, so a concurrent CDC chunk cannot be bypassed.
  FutureOr<bool> completeUnknownResident(
    String partitionId,
    OfflineEntityKey key, {
    required int expectedEpoch,
  });

  /// Atomically invalidates confirmed cache entries and records chunk progress.
  /// The last-applied cursor advances only when the final chunk commits.
  FutureOr<void> applyChangeChunk(String partitionId, OfflineChangeChunk chunk);

  /// Hides confirmed cache, retains bounded resident identities as Unknown,
  /// clears CDC progress, and installs [checkpoint]. Durable outbox records and
  /// their pending writes are preserved.
  FutureOr<void> resetChangeCursor(
    String partitionId,
    OfflineChangeCursor checkpoint,
  );

  /// Transactionally deletes every partition record and increments generation.
  /// This is an explicit barrier that cancels enqueue-topology obligations
  /// created earlier in the same transaction.
  FutureOr<void> wipePartition(String partitionId);
}

/// Stable FIFO position used by bounded outbox maintenance scans.
final class OfflineOutboxCursor {
  /// Creates an exact durable-record position.
  const OfflineOutboxCursor({
    required this.ordinal,
    required this.itemIndex,
    required this.recordId,
  });

  /// Shared plural-operation FIFO ordinal.
  final int ordinal;

  /// Stable zero-based position inside the operation.
  final int itemIndex;

  /// Stable final tie-breaker.
  final String recordId;

  @override
  bool operator ==(Object other) =>
      other is OfflineOutboxCursor &&
      ordinal == other.ordinal &&
      itemIndex == other.itemIndex &&
      recordId == other.recordId;

  @override
  int get hashCode => Object.hash(ordinal, itemIndex, recordId);
}

/// One bounded FIFO outbox page.
final class OfflineOutboxScanPage {
  /// Creates an immutable scan page.
  OfflineOutboxScanPage({
    required List<OfflineOutboxRecord> records,
    required this.nextCursor,
    required this.hasMore,
  }) : records = List<OfflineOutboxRecord>.unmodifiable(records) {
    if (records.isEmpty != (nextCursor == null)) {
      throw const OfflineArgumentException();
    }
  }

  /// Records inspected by this page, never exceeding the requested limit.
  final List<OfflineOutboxRecord> records;

  /// Position of the final returned record, or null for an empty page.
  final OfflineOutboxCursor? nextCursor;

  /// Whether another record exists after [nextCursor] in the same scope.
  final bool hasMore;
}

/// One bounded operation-aggregate page in stable identity order.
final class OfflineOperationScanPage {
  /// Creates an immutable scan page.
  OfflineOperationScanPage({
    required List<OfflineOperationRecord> operations,
    required this.nextOperationId,
    required this.hasMore,
  }) : operations = List<OfflineOperationRecord>.unmodifiable(operations) {
    if (operations.isEmpty != (nextOperationId == null)) {
      throw const OfflineArgumentException();
    }
  }

  /// Aggregates inspected by this page, never exceeding the requested limit.
  final List<OfflineOperationRecord> operations;

  /// Identity of the final returned aggregate, or null for an empty page.
  final String? nextOperationId;

  /// Whether another aggregate exists after [nextOperationId].
  final bool hasMore;
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
