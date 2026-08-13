import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';

/// Supplies UTC wall-clock time for cache and outbox decisions.
typedef OfflineClock = DateTime Function();

/// Generates an opaque record or operation identifier.
typedef OfflineIdGenerator = String Function();

/// Produces a full-jitter delay no greater than its supplied ceiling.
typedef OfflineJitter = Duration Function(Duration ceiling);

/// Receives content-free diagnostic events.
abstract interface class OfflineDiagnostics {
  /// Receives one aggregate-safe event.
  void record(OfflineDiagnosticEvent event);
}

/// Content-free diagnostic lifecycle category.
enum OfflineDiagnosticKind {
  /// One durable write lifecycle transition.
  writeTransition,

  /// An eligible local cache result was found.
  cacheHit,

  /// No eligible local cache result was found.
  cacheMiss,

  /// A cached value reached its absolute Lantern expiration.
  cacheExpired,

  /// A configured resource or storage bound rejected work.
  capacityRejected,

  /// A replay lease was extended while remote work remained active.
  leaseRenewed,

  /// An abandoned replay lease became eligible for recovery.
  leaseRecovered,

  /// A late result was rejected by partition generation or lease ownership.
  staleOutcomeRejected,

  /// A partition was transactionally wiped.
  partitionWiped,
}

/// A diagnostic event that intentionally excludes keys, values, IDs, tokens,
/// and partition identifiers.
final class OfflineDiagnosticEvent {
  /// Creates a content-free diagnostic event.
  const OfflineDiagnosticEvent({
    required this.kind,
    this.category,
    this.state,
    this.attempt = 0,
  });

  /// Bounded lifecycle category.
  final OfflineDiagnosticKind kind;

  /// Bounded operation category for write events.
  final OfflineOperationCategory? category;

  /// Bounded write lifecycle state for write events.
  final OfflineWriteState? state;

  /// Attempt count, if applicable.
  final int attempt;
}

/// The cache entity family.
enum OfflineEntityKind {
  /// A vertex keyed by its UTF-8 key.
  vertex,

  /// An edge keyed by its collision-free tail/head pair.
  edge,
}

/// A collision-free cache and ordering identity.
final class OfflineEntityKey {
  /// Creates a vertex identity.
  const OfflineEntityKey.vertex(String key)
    : kind = OfflineEntityKind.vertex,
      vertexKey = key,
      tail = null,
      head = null;

  /// Creates an edge identity.
  const OfflineEntityKey.edge(String edgeTail, String edgeHead)
    : kind = OfflineEntityKind.edge,
      vertexKey = null,
      tail = edgeTail,
      head = edgeHead;

  /// Entity family.
  final OfflineEntityKind kind;

  /// Vertex key, for vertex identities only.
  final String? vertexKey;

  /// Edge tail, for edge identities only.
  final String? tail;

  /// Edge head, for edge identities only.
  final String? head;

  /// Canonical delimiter-free map and ordering key.
  String get canonical => switch (kind) {
    OfflineEntityKind.vertex => 'v:${_utf8Length(vertexKey!)}:$vertexKey',
    OfflineEntityKind.edge =>
      'e:${_utf8Length(tail!)}:$tail${_utf8Length(head!)}:$head',
  };

  @override
  bool operator ==(Object other) =>
      other is OfflineEntityKey &&
      kind == other.kind &&
      vertexKey == other.vertexKey &&
      tail == other.tail &&
      head == other.head;

  @override
  int get hashCode => Object.hash(kind, vertexKey, tail, head);
}

/// A cache record holding either an exact entity or a bounded negative marker.
final class OfflineCacheRecord {
  /// Creates an exact confirmed cache record.
  OfflineCacheRecord.value({
    required this.partitionId,
    required this.generation,
    required this.key,
    required Object entity,
    required this.validatedAt,
    required this.lastAccessAt,
    this.versionTag,
  }) : vertex = entity is Vertex ? copyOfflineVertex(entity) : null,
       edge = entity is Edge ? copyOfflineEdge(entity) : null,
       missingUntil = null {
    _validateCacheMetadata();
    if ((vertex == null) == (edge == null)) {
      throw const OfflineArgumentException();
    }
    if (key.kind == OfflineEntityKind.vertex && vertex == null) {
      throw const OfflineArgumentException();
    }
    if (key.kind == OfflineEntityKind.edge && edge == null) {
      throw const OfflineArgumentException();
    }
    if (!_isOptionalDurableTimestamp(expiration)) {
      throw const OfflineArgumentException();
    }
  }

  /// Creates a bounded negative cache marker.
  OfflineCacheRecord.missing({
    required this.partitionId,
    required this.generation,
    required this.key,
    required this.validatedAt,
    required this.lastAccessAt,
    required DateTime missingUntil,
    this.versionTag,
  }) : vertex = null,
       edge = null,
       missingUntil = _validMissingUntil(missingUntil) {
    _validateCacheMetadata();
  }

  /// Application-defined user/tenant partition that owns this record.
  final String partitionId;

  /// Partition generation captured when the record was validated.
  final int generation;

  /// Entity identity.
  final OfflineEntityKey key;

  /// Confirmed vertex when this is a vertex record.
  final Vertex? vertex;

  /// Confirmed edge when this is an edge record.
  final Edge? edge;

  /// Time the server result was validated.
  final DateTime validatedAt;

  /// Last local access time used by LRU eviction.
  final DateTime lastAccessAt;

  /// Negative-marker expiration, or null for a positive record.
  final DateTime? missingUntil;

  /// Optional opaque gateway version/ETag metadata.
  final String? versionTag;

  /// Whether this is a negative cache marker.
  bool get isMissing => missingUntil != null;

  /// The Lantern absolute expiration for a positive record.
  DateTime? get expiration => vertex?.expiration ?? edge?.expiration;

  /// A defensive entity copy, or null for a negative marker.
  Object? get entity {
    if (vertex != null) return copyOfflineVertex(vertex!);
    if (edge != null) return copyOfflineEdge(edge!);
    return null;
  }

  /// Returns a copy with updated LRU time.
  OfflineCacheRecord accessedAt(DateTime time) => isMissing
      ? OfflineCacheRecord.missing(
          partitionId: partitionId,
          generation: generation,
          key: key,
          validatedAt: validatedAt,
          lastAccessAt: time,
          missingUntil: missingUntil!,
          versionTag: versionTag,
        )
      : OfflineCacheRecord.value(
          partitionId: partitionId,
          generation: generation,
          key: key,
          entity: entity!,
          validatedAt: validatedAt,
          lastAccessAt: time,
          versionTag: versionTag,
        );

  void _validateCacheMetadata() {
    if (partitionId.isEmpty ||
        generation < 0 ||
        generation > _maxDurableInt ||
        !_isDurableTimestamp(validatedAt) ||
        !_isDurableTimestamp(lastAccessAt) ||
        !_isOptionalDurableTimestamp(missingUntil)) {
      throw const OfflineArgumentException();
    }
  }

  static DateTime _validMissingUntil(DateTime value) {
    if (!_isDurableTimestamp(value)) {
      throw const OfflineArgumentException();
    }
    return value;
  }
}

/// The supported, durable mutation category.
enum OfflineOperationCategory {
  /// An unconditional vertex replacement.
  putVertex,

  /// An idempotent edge replacement.
  putEdge,

  /// A legacy Add record retained only for fail-closed migration and inspection.
  addEdge,
}

/// A persistable, already expiration-resolved mutation.
sealed class OfflineIntent {
  /// Creates a supported persisted mutation.
  const OfflineIntent();

  /// Mutation category.
  OfflineOperationCategory get category;

  /// Entity ordering identity.
  OfflineEntityKey get key;

  /// Exact absolute expiration, if any.
  DateTime? get expiration;
}

/// An unconditional persisted vertex replacement.
final class OfflinePutVertexIntent extends OfflineIntent {
  /// Creates a vertex replacement intent from an exact vertex.
  OfflinePutVertexIntent(Vertex vertex) : vertex = copyOfflineVertex(vertex) {
    if (!_isOptionalDurableTimestamp(this.vertex.expiration)) {
      throw const OfflineArgumentException();
    }
  }

  /// Exact target vertex.
  final Vertex vertex;

  @override
  OfflineOperationCategory get category => OfflineOperationCategory.putVertex;

  @override
  OfflineEntityKey get key => OfflineEntityKey.vertex(vertex.key);

  @override
  DateTime? get expiration => vertex.expiration;
}

/// An idempotent persisted edge replacement.
final class OfflinePutEdgeIntent extends OfflineIntent {
  /// Creates an edge replacement intent from an exact edge.
  OfflinePutEdgeIntent(Edge edge) : edge = copyOfflineEdge(edge) {
    if (!_isOptionalDurableTimestamp(this.edge.expiration)) {
      throw const OfflineArgumentException();
    }
  }

  /// Exact target edge.
  final Edge edge;

  @override
  OfflineOperationCategory get category => OfflineOperationCategory.putEdge;

  @override
  OfflineEntityKey get key => OfflineEntityKey.edge(edge.tail, edge.head);

  @override
  DateTime? get expiration => edge.expiration;
}

/// A legacy persisted Add retained only for fail-closed migration and inspection.
///
/// [OfflineLanternRepository] cannot enqueue or replay this intent. The type
/// remains decodable so experimental snapshots can become inspectable terminal
/// records instead of being sent blindly or discarded.
final class OfflineAddEdgeIntent extends OfflineIntent {
  /// Creates a migration-only Add intent with one exact contribution ID.
  OfflineAddEdgeIntent(Edge edge, Uint8List contributionId)
    : edge = copyOfflineEdge(edge),
      _contributionId = Uint8List.fromList(contributionId) {
    if (_contributionId.length != 24 ||
        !_contributionId.any((byte) => byte != 0) ||
        !_isOptionalDurableTimestamp(this.edge.expiration)) {
      throw const OfflineArgumentException();
    }
  }

  /// Exact legacy additive contribution.
  final Edge edge;
  final Uint8List _contributionId;

  /// A defensive copy of the persisted contribution ID.
  Uint8List get contributionId => Uint8List.fromList(_contributionId);

  @override
  OfflineOperationCategory get category => OfflineOperationCategory.addEdge;

  @override
  OfflineEntityKey get key => OfflineEntityKey.edge(edge.tail, edge.head);

  @override
  DateTime? get expiration => edge.expiration;
}

/// Durable outbox lifecycle state.
enum OfflineOutboxState {
  /// Locally committed and eligible for a future claim.
  enqueued,

  /// Claimed by a bounded replay lease.
  sending,

  /// Removed from automatic replay for safe inspection.
  deadLetter,

  /// Reached its absolute Lantern expiration before confirmation.
  expired,
}

/// Exact durable metadata for one mutation item.
final class OfflineOutboxRecord {
  /// Creates a durable mutation item.
  OfflineOutboxRecord({
    required this.recordId,
    required this.operationId,
    required this.itemIndex,
    required this.partitionId,
    required OfflineIntent intent,
    required this.enqueuedAt,
    required this.ordinal,
    required this.state,
    required this.attemptCount,
    required this.generation,
    this.nextAttemptAt,
    this.leaseOwner,
    this.leaseUntil,
    this.deadLetteredAt,
    this.diagnosticCode,
  }) : intent = copyOfflineIntent(intent) {
    final hasLease = leaseOwner != null && leaseUntil != null;
    if (recordId.isEmpty ||
        operationId.isEmpty ||
        itemIndex < 0 ||
        itemIndex > _maxDurableInt ||
        partitionId.isEmpty ||
        !_isDurableTimestamp(enqueuedAt) ||
        ordinal < 0 ||
        ordinal > _maxDurableInt ||
        attemptCount < 0 ||
        attemptCount > _maxDurableInt ||
        generation < 0 ||
        generation > _maxDurableInt ||
        (leaseOwner == null) != (leaseUntil == null) ||
        (state == OfflineOutboxState.sending) != hasLease ||
        (leaseOwner != null && leaseOwner!.isEmpty) ||
        !_isOptionalDurableTimestamp(nextAttemptAt) ||
        (nextAttemptAt != null &&
            (state != OfflineOutboxState.enqueued ||
                nextAttemptAt!.isBefore(enqueuedAt))) ||
        !_isOptionalDurableTimestamp(leaseUntil) ||
        (leaseUntil != null && !leaseUntil!.isAfter(enqueuedAt)) ||
        !_isOptionalDurableTimestamp(deadLetteredAt) ||
        (deadLetteredAt != null && deadLetteredAt!.isBefore(enqueuedAt)) ||
        (state == OfflineOutboxState.deadLetter) != (deadLetteredAt != null) ||
        diagnosticCode?.isEmpty == true) {
      throw const OfflineArgumentException();
    }
  }

  /// Stable opaque item identifier.
  final String recordId;

  /// Stable logical-call identifier shared by operation items.
  final String operationId;

  /// Zero-based item index within the logical operation.
  final int itemIndex;

  /// Application-defined user/tenant partition.
  final String partitionId;

  /// Exact supported intent.
  final OfflineIntent intent;

  /// Enqueue timestamp.
  final DateTime enqueuedAt;

  /// Durable monotone per-partition ordering ordinal.
  final int ordinal;

  /// Current outbox lifecycle state.
  final OfflineOutboxState state;

  /// Completed durable adapter attempts, each permitting at most one RPC.
  final int attemptCount;

  /// Partition generation that owns this record.
  final int generation;

  /// Earliest next retry time.
  final DateTime? nextAttemptAt;

  /// Current bounded lease owner.
  final String? leaseOwner;

  /// Current bounded lease expiration.
  final DateTime? leaseUntil;

  /// First transition time into [OfflineOutboxState.deadLetter].
  final DateTime? deadLetteredAt;

  /// Content-free terminal diagnostic code.
  final String? diagnosticCode;

  /// Returns the intent expiration.
  DateTime? get absoluteExpiration => intent.expiration;

  /// Returns an immutable modified copy.
  OfflineOutboxRecord copyWith({
    OfflineOutboxState? state,
    int? attemptCount,
    DateTime? nextAttemptAt,
    bool clearNextAttemptAt = false,
    String? leaseOwner,
    bool clearLeaseOwner = false,
    DateTime? leaseUntil,
    bool clearLeaseUntil = false,
    DateTime? deadLetteredAt,
    bool clearDeadLetteredAt = false,
    String? diagnosticCode,
    bool clearDiagnosticCode = false,
  }) => OfflineOutboxRecord(
    recordId: recordId,
    operationId: operationId,
    itemIndex: itemIndex,
    partitionId: partitionId,
    intent: intent,
    enqueuedAt: enqueuedAt,
    ordinal: ordinal,
    state: state ?? this.state,
    attemptCount: attemptCount ?? this.attemptCount,
    generation: generation,
    nextAttemptAt: clearNextAttemptAt
        ? null
        : nextAttemptAt ?? this.nextAttemptAt,
    leaseOwner: clearLeaseOwner ? null : leaseOwner ?? this.leaseOwner,
    leaseUntil: clearLeaseUntil ? null : leaseUntil ?? this.leaseUntil,
    deadLetteredAt: clearDeadLetteredAt
        ? null
        : deadLetteredAt ?? this.deadLetteredAt,
    diagnosticCode: clearDiagnosticCode
        ? null
        : diagnosticCode ?? this.diagnosticCode,
  );
}

/// A read source with precise cache/server provenance.
enum OfflineReadSource {
  /// The local confirmed cache (possibly with a pending overlay) supplied it.
  cache,

  /// A successful exact remote response supplied it.
  server,
}

/// Explicit cache/read certainty state.
enum OfflineReadState {
  /// A confirmed entry is within its configured freshness age.
  fresh,

  /// An unexpired confirmed entry exceeded configured freshness age.
  stale,

  /// A bounded confirmed remote absence is cached.
  missing,

  /// A previously known record reached absolute Lantern expiration.
  expired,

  /// No eligible confirmed answer is available, including transport failure.
  unknown,
}

/// Selects remote/cache read behavior.
enum OfflineReadPolicy {
  /// Use only a locally eligible cache answer.
  cacheOnly,

  /// Use a fresh cache answer, otherwise try remote.
  cacheFirst,

  /// Try remote first, then use an eligible cache fallback on failure.
  serverFirst,

  /// Use remote only while still updating the confirmed cache on success.
  serverOnly,
}

/// One immutable cache/server snapshot, optionally with a pending local overlay.
final class OfflineSnapshot<T> {
  /// Creates an explicit immutable read snapshot.
  const OfflineSnapshot({
    required this.state,
    required this.source,
    this.value,
    this.validatedAt,
    this.expiredAt,
    this.cause,
    this.hasPendingWrites = false,
  });

  /// Read certainty state.
  final OfflineReadState state;

  /// Source that supplied the underlying confirmed result, if one exists.
  final OfflineReadSource? source;

  /// Exact entity value when available.
  final T? value;

  /// Confirmed remote validation time, if any.
  final DateTime? validatedAt;

  /// Absolute expiration reached by an evicted record, if any.
  final DateTime? expiredAt;

  /// Typed transport cause for [OfflineReadState.unknown], if any.
  final Object? cause;

  /// Whether one or more live local mutations are overlaid.
  final bool hasPendingWrites;
}

/// Observable write lifecycle state.
enum OfflineWriteState {
  /// The local durable transaction committed.
  locallyCommitted,

  /// A bounded replay lease is actively sending.
  sending,

  /// The remote response was transactionally confirmed locally.
  confirmed,

  /// A retryable transport failure scheduled a retry.
  retryScheduled,

  /// Authentication paused replay without burning an attempt.
  pausedForAuth,

  /// The item became inspectable and requires explicit action.
  deadLetter,

  /// The item reached its resolved absolute expiration.
  expired,

  /// A legacy snapshot no longer contained this item's terminal outcome.
  outcomeUnknown,
}

/// One immutable write status transition.
final class OfflineWriteStatus {
  /// Creates a write status transition.
  OfflineWriteStatus({
    required this.recordId,
    required this.operationId,
    required this.itemIndex,
    required this.state,
    required this.attemptCount,
    this.diagnosticCode,
  }) {
    if (recordId.isEmpty ||
        operationId.isEmpty ||
        itemIndex < 0 ||
        itemIndex > _maxDurableInt ||
        attemptCount < 0 ||
        attemptCount > _maxDurableInt ||
        diagnosticCode?.isEmpty == true) {
      throw const OfflineArgumentException();
    }
  }

  /// Stable item identifier.
  final String recordId;

  /// Stable logical operation identifier.
  final String operationId;

  /// Zero-based item index within the logical operation.
  final int itemIndex;

  /// Current lifecycle state.
  final OfflineWriteState state;

  /// Completed durable adapter attempt count.
  final int attemptCount;

  /// Content-free bounded diagnostic code.
  final String? diagnosticCode;
}

/// Durable aggregate status for one logical write operation.
final class OfflineOperationStatus {
  /// Creates an immutable operation status.
  OfflineOperationStatus({
    required this.operationId,
    required List<OfflineWriteStatus> items,
  }) : items = List<OfflineWriteStatus>.unmodifiable(items) {
    if (operationId.isEmpty || this.items.isEmpty) {
      throw const OfflineArgumentException();
    }
    final recordIds = <String>{};
    for (var index = 0; index < this.items.length; index++) {
      final item = this.items[index];
      if (item.operationId != operationId ||
          item.itemIndex != index ||
          !recordIds.add(item.recordId)) {
        throw const OfflineArgumentException();
      }
    }
  }

  /// Stable logical operation identifier.
  final String operationId;

  /// Latest durable status for every item in input order.
  final List<OfflineWriteStatus> items;

  /// Whether every item reached a terminal state.
  bool get isTerminal => items.every((item) => item.state.isTerminal);

  /// Number of items confirmed by Lantern.
  int get confirmedCount =>
      items.where((item) => item.state == OfflineWriteState.confirmed).length;

  /// Number of items requiring explicit dead-letter action.
  int get deadLetterCount =>
      items.where((item) => item.state == OfflineWriteState.deadLetter).length;

  /// Number of items that expired before confirmation.
  int get expiredCount =>
      items.where((item) => item.state == OfflineWriteState.expired).length;
}

/// Durable content-free metadata backing [OfflineOperationStatus].
final class OfflineOperationRecord {
  /// Creates one storage-owned operation record.
  OfflineOperationRecord({
    required this.partitionId,
    required this.generation,
    required this.operationId,
    required List<OfflineWriteStatus> items,
    required this.updatedAt,
    this.terminalAt,
  }) : items = List<OfflineWriteStatus>.unmodifiable(items) {
    final status = OfflineOperationStatus(
      operationId: operationId,
      items: this.items,
    );
    if (partitionId.isEmpty ||
        generation < 0 ||
        generation > _maxDurableInt ||
        !_isDurableTimestamp(updatedAt) ||
        !_isOptionalDurableTimestamp(terminalAt) ||
        (terminalAt != null && terminalAt!.isAfter(updatedAt)) ||
        (terminalAt != null) != status.isTerminal) {
      throw const OfflineArgumentException();
    }
  }

  /// Owning signed-in partition.
  final String partitionId;

  /// Partition generation that owns this record.
  final int generation;

  /// Stable logical operation identifier.
  final String operationId;

  /// Latest per-item statuses in input order.
  final List<OfflineWriteStatus> items;

  /// Last durable transition time.
  final DateTime updatedAt;

  /// First time all items became terminal.
  final DateTime? terminalAt;

  /// Public aggregate view.
  OfflineOperationStatus get status =>
      OfflineOperationStatus(operationId: operationId, items: items);
}

extension on OfflineWriteState {
  /// Whether this status no longer owns replayable outbox work.
  bool get isTerminal =>
      this == OfflineWriteState.confirmed ||
      this == OfflineWriteState.deadLetter ||
      this == OfflineWriteState.expired ||
      this == OfflineWriteState.outcomeUnknown;
}

/// Handle returned only after an offline write has committed locally.
final class OfflineWriteHandle {
  /// Creates a locally committed write handle.
  const OfflineWriteHandle({
    required this.recordId,
    required this.operationId,
    required this.itemIndex,
    required this.statuses,
  });

  /// Stable durable item identifier.
  final String recordId;

  /// Stable logical operation identifier.
  final String operationId;

  /// Zero-based operation item index.
  final int itemIndex;

  /// Process-local status transition stream for this item.
  final Stream<OfflineWriteStatus> statuses;
}

/// Locally committed handles for every item in one logical plural write.
final class OfflineWriteOperation {
  /// Creates an immutable logical-operation handle.
  OfflineWriteOperation({
    required this.operationId,
    required List<OfflineWriteHandle> items,
  }) : items = List<OfflineWriteHandle>.unmodifiable(items) {
    if (operationId.isEmpty || this.items.isEmpty) {
      throw const OfflineArgumentException();
    }
    for (var index = 0; index < this.items.length; index++) {
      final item = this.items[index];
      if (item.operationId != operationId || item.itemIndex != index) {
        throw const OfflineArgumentException();
      }
    }
  }

  /// Stable logical-call identifier shared by every item.
  final String operationId;

  /// Durable per-item handles in zero-based input order.
  final List<OfflineWriteHandle> items;

  /// Number of mutation items committed by the local transaction.
  int get itemCount => items.length;
}

/// Content-free public dead-letter metadata.
final class DeadLetterSummary {
  /// Creates a dead-letter summary.
  const DeadLetterSummary({
    required this.recordId,
    required this.category,
    required this.state,
    required this.age,
    required this.attemptCount,
    required this.diagnosticCode,
  });

  /// Stable item identifier.
  final String recordId;

  /// Mutation category.
  final OfflineOperationCategory category;

  /// Terminal state.
  final OfflineOutboxState state;

  /// Age at inspection time.
  final Duration age;

  /// Completed durable adapter attempts.
  final int attemptCount;

  /// Content-free failure code.
  final String? diagnosticCode;
}

/// Content-free public metadata for one non-terminal mutation.
final class PendingSummary {
  /// Creates a pending mutation summary.
  const PendingSummary({
    required this.recordId,
    required this.operationId,
    required this.category,
    required this.state,
    required this.age,
    required this.attemptCount,
    required this.diagnosticCode,
  });

  /// Stable item identifier.
  final String recordId;

  /// Stable logical operation identifier.
  final String operationId;

  /// Mutation category.
  final OfflineOperationCategory category;

  /// Current non-terminal state.
  final OfflineOutboxState state;

  /// Age at inspection time.
  final Duration age;

  /// Completed durable adapter attempts.
  final int attemptCount;

  /// Content-free retry or pause code.
  final String? diagnosticCode;
}

/// Application callback required before returning a sensitive dead-letter intent.
typedef OfflineDeadLetterAuthorizer =
    FutureOr<bool> Function(DeadLetterSummary summary);

/// Explicit per-store capacity limits.
final class OfflineStoreLimits {
  /// Creates bounded reference-store limits.
  const OfflineStoreLimits({
    this.maxCacheRecords = 1000,
    this.maxCacheBytes = 8 * 1024 * 1024,
    this.maxOutboxRecords = 1000,
    this.maxOutboxBytes = 8 * 1024 * 1024,
    this.maxOperationRecords = 1000,
    this.maxOperationBytes = 8 * 1024 * 1024,
    this.maxCacheRecordsPerPartition = 1000,
    this.maxCacheBytesPerPartition = 8 * 1024 * 1024,
    this.maxOutboxRecordsPerPartition = 1000,
    this.maxOutboxBytesPerPartition = 8 * 1024 * 1024,
    this.maxOperationRecordsPerPartition = 1000,
    this.maxOperationBytesPerPartition = 8 * 1024 * 1024,
    this.maxLeaseOwnerBytes = 256,
    this.maxDiagnosticCodeBytes = 128,
    this.maxChangeControllers = 256,
  });

  /// Maximum confirmed cache records across the store.
  final int maxCacheRecords;

  /// Maximum admission-reserved confirmed-cache bytes across the store.
  ///
  /// Each record reserves the maximum encoded last-access timestamp so a later
  /// LRU touch cannot make a canonical snapshot exceed its admission limit.
  final int maxCacheBytes;

  /// Maximum outbox items across the store.
  final int maxOutboxRecords;

  /// Maximum admission-reserved outbox bytes across the store.
  ///
  /// Each retained record is charged for its immutable payload plus the full
  /// bounded lifecycle envelope, not only its current encoded state.
  final int maxOutboxBytes;

  /// Maximum durable operation aggregates across the store.
  final int maxOperationRecords;

  /// Maximum admission-reserved durable operation bytes across the store.
  ///
  /// Each aggregate reserves the full bounded lifecycle envelope for every
  /// item plus both mutable timestamps.
  final int maxOperationBytes;

  /// Maximum confirmed cache records in one partition.
  final int maxCacheRecordsPerPartition;

  /// Maximum admission-reserved confirmed-cache bytes in one partition.
  final int maxCacheBytesPerPartition;

  /// Maximum outbox items in one partition.
  final int maxOutboxRecordsPerPartition;

  /// Maximum admission-reserved outbox bytes in one partition.
  final int maxOutboxBytesPerPartition;

  /// Maximum durable operation aggregates in one partition.
  final int maxOperationRecordsPerPartition;

  /// Maximum admission-reserved durable operation bytes in one partition.
  final int maxOperationBytesPerPartition;

  /// Maximum UTF-8 bytes in one outbox lease owner.
  ///
  /// Outbox byte admission reserves this full amount so a later claim cannot
  /// exceed the store's global or per-partition byte budget.
  final int maxLeaseOwnerBytes;

  /// Maximum UTF-8 bytes in one outbox diagnostic code.
  ///
  /// Outbox byte admission reserves this full amount so retry and terminal
  /// transitions cannot exceed the store's byte budgets. The minimum is 19,
  /// which admits every SDK-owned lifecycle and migration diagnostic.
  final int maxDiagnosticCodeBytes;

  /// Maximum partitions with an active reference-store change controller.
  final int maxChangeControllers;
}

/// Repository behavior, retry, and cache bounds.
final class OfflineConfig {
  /// Creates an explicit bounded repository configuration.
  OfflineConfig({
    this.maxCacheAge = const Duration(minutes: 5),
    this.missingTtl = const Duration(seconds: 30),
    this.maxAttempts = 8,
    this.maxAge = const Duration(days: 7),
    this.deadLetterRetention = const Duration(days: 30),
    this.operationRetention = const Duration(days: 30),
    this.maxConcurrency = 4,
    int? maxConcurrencyPerPartition,
    this.maxQueuedReplaySends = 128,
    this.maxQueuedReplaySendsPerPartition = 32,
    this.maxQueuedReplaysPerPartition = 1,
    this.maxActivePartitionRuntimes = 128,
    this.maxReadConcurrency = 8,
    this.maxReadConcurrencyPerPartition = 4,
    this.maxQueuedReads = 128,
    this.maxQueuedReadsPerPartition = 32,
    this.maxWatchers = 256,
    this.maxWatchersPerPartition = 64,
    this.maxActiveWatcherPartitions = 16,
    this.maxWriteStatusControllers = 1024,
    this.maxGeneratedIdAttempts = 8,
    this.maxSweepRecordsPerObservation = 256,
    this.leaseDuration = const Duration(seconds: 30),
    Duration? leaseRenewalInterval,
    this.baseRetryDelay = const Duration(milliseconds: 100),
    this.maxRetryDelay = const Duration(seconds: 30),
    OfflineClock? clock,
    OfflineIdGenerator? idGenerator,
    OfflineJitter? jitter,
    this.diagnostics,
  }) : clock = clock ?? _utcNow,
       idGenerator = idGenerator ?? _opaqueId,
       jitter = jitter ?? _fullJitter,
       maxConcurrencyPerPartition =
           maxConcurrencyPerPartition ?? maxConcurrency,
       leaseRenewalInterval =
           leaseRenewalInterval ??
           Duration(microseconds: leaseDuration.inMicroseconds ~/ 3) {
    if (maxCacheAge < Duration.zero ||
        missingTtl <= Duration.zero ||
        maxAttempts < 1 ||
        maxAttempts > _maxDurableInt ||
        maxAge <= Duration.zero ||
        deadLetterRetention <= Duration.zero ||
        operationRetention <= Duration.zero ||
        maxConcurrency < 1 ||
        this.maxConcurrencyPerPartition < 1 ||
        this.maxConcurrencyPerPartition > maxConcurrency ||
        maxQueuedReplaySends < 0 ||
        maxQueuedReplaySendsPerPartition < 0 ||
        maxQueuedReplaysPerPartition < 0 ||
        maxActivePartitionRuntimes < 1 ||
        maxReadConcurrency < 1 ||
        maxReadConcurrencyPerPartition < 1 ||
        maxQueuedReads < 0 ||
        maxQueuedReadsPerPartition < 0 ||
        maxWatchers < 1 ||
        maxWatchersPerPartition < 1 ||
        maxActiveWatcherPartitions < 1 ||
        maxWriteStatusControllers < 1 ||
        maxGeneratedIdAttempts < 1 ||
        maxSweepRecordsPerObservation < 1 ||
        leaseDuration <= Duration.zero ||
        this.leaseRenewalInterval <= Duration.zero ||
        this.leaseRenewalInterval >= leaseDuration ||
        baseRetryDelay <= Duration.zero ||
        maxRetryDelay <= Duration.zero) {
      throw const OfflineArgumentException();
    }
  }

  /// Maximum confirmed cache age treated as fresh.
  final Duration maxCacheAge;

  /// Bounded lifetime for confirmed negative cache markers.
  final Duration missingTtl;

  /// Maximum send attempts before dead-lettering.
  final int maxAttempts;

  /// Maximum local outbox age before dead-lettering.
  final Duration maxAge;

  /// Maximum retained age of a dead-letter record before automatic deletion.
  final Duration deadLetterRetention;

  /// Maximum retention after every item in an operation becomes terminal.
  final Duration operationRetention;

  /// Maximum replay sends active across the repository.
  final int maxConcurrency;

  /// Maximum replay sends active for one partition.
  final int maxConcurrencyPerPartition;

  /// Maximum replay sends waiting across the repository.
  final int maxQueuedReplaySends;

  /// Maximum replay sends waiting for one partition.
  final int maxQueuedReplaySendsPerPartition;

  /// Maximum serialized replay invocations waiting for one partition.
  final int maxQueuedReplaysPerPartition;

  /// Maximum partitions with active or queued process-local work.
  final int maxActivePartitionRuntimes;

  /// Maximum distinct remote reads active across the repository.
  final int maxReadConcurrency;

  /// Maximum distinct remote reads active for one partition.
  final int maxReadConcurrencyPerPartition;

  /// Maximum remote reads waiting across the repository.
  final int maxQueuedReads;

  /// Maximum remote reads waiting for one partition.
  final int maxQueuedReadsPerPartition;

  /// Maximum active entity snapshot watchers across the repository.
  final int maxWatchers;

  /// Maximum active entity snapshot watchers for one partition.
  final int maxWatchersPerPartition;

  /// Maximum partitions that may own active entity snapshot watchers.
  final int maxActiveWatcherPartitions;

  /// Maximum live per-item process-local status controllers.
  final int maxWriteStatusControllers;

  /// Maximum attempts to allocate each generated operation or record ID.
  final int maxGeneratedIdAttempts;

  /// Maximum due records and retained operations mutated by one lazy sweep.
  final int maxSweepRecordsPerObservation;

  /// Lease duration protecting one claimed send.
  final Duration leaseDuration;

  /// Interval used to renew a lease while its remote request remains active.
  final Duration leaseRenewalInterval;

  /// Initial full-jitter retry ceiling.
  final Duration baseRetryDelay;

  /// Maximum full-jitter retry ceiling.
  final Duration maxRetryDelay;

  /// Injected wall clock.
  final OfflineClock clock;

  /// Injected opaque record/operation ID generator.
  final OfflineIdGenerator idGenerator;

  /// Injected full-jitter sampler.
  final OfflineJitter jitter;

  /// Optional content-free diagnostics sink.
  final OfflineDiagnostics? diagnostics;
}

DateTime _utcNow() => DateTime.now().toUtc();

String _opaqueId() {
  final random = Random.secure();
  final bytes = List<int>.generate(16, (_) => random.nextInt(256));
  return bytes.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();
}

Duration _fullJitter(Duration ceiling) {
  if (ceiling <= Duration.zero) return Duration.zero;
  return Duration(
    microseconds: (Random().nextDouble() * ceiling.inMicroseconds).round(),
  );
}

int _utf8Length(String value) => utf8.encode(value).length;

const int _maxDurableInt = 0x7fffffffffffffff;

bool _isDurableTimestamp(DateTime value) =>
    value.isUtc && value.year >= 1 && value.year <= 9999;

bool _isOptionalDurableTimestamp(DateTime? value) =>
    value == null || _isDurableTimestamp(value);

/// Creates a defensive exact vertex copy for storage boundaries.
Vertex copyOfflineVertex(Vertex vertex) => Vertex(
  key: vertex.key,
  value: copyOfflineValue(vertex.value),
  expiration: vertex.expiration?.toUtc(),
);

/// Creates a defensive exact edge copy for storage boundaries.
Edge copyOfflineEdge(Edge edge) => Edge(
  tail: edge.tail,
  head: edge.head,
  weight: normalizeOfflineFloat32(edge.weight),
  expiration: edge.expiration?.toUtc(),
);

/// Creates a defensive exact vertex-value copy for storage boundaries.
VertexValue copyOfflineValue(VertexValue value) => switch (value) {
  Float64Value(:final value) => VertexValue.float64(value),
  Float32Value(:final value) => VertexValue.float32(value),
  Int32Value(:final value) => VertexValue.int32(value),
  Int64Value(:final value) => VertexValue.int64(value),
  Uint32Value(:final value) => VertexValue.uint32(value),
  Uint64Value(:final value) => VertexValue.uint64(value),
  BoolValue(:final value) => VertexValue.boolean(value),
  StringValue(:final value) => VertexValue.string(value),
  BytesValue(:final value) => VertexValue.bytes(value),
  TimestampValue(:final value) => VertexValue.timestamp(value),
  DurationValue(:final value) => VertexValue.duration(value),
  NilValue() => VertexValue.nil(),
  UnsetValue() => VertexValue.unset(),
};

/// Creates a defensive exact durable-intent copy for storage boundaries.
OfflineIntent copyOfflineIntent(OfflineIntent intent) => switch (intent) {
  OfflinePutVertexIntent(:final vertex) => OfflinePutVertexIntent(vertex),
  OfflinePutEdgeIntent(:final edge) => OfflinePutEdgeIntent(edge),
  OfflineAddEdgeIntent(:final edge, :final contributionId) =>
    OfflineAddEdgeIntent(edge, contributionId),
};

/// Normalizes a finite number to exact IEEE-754 binary32 precision.
double normalizeOfflineFloat32(double value) {
  if (!value.isFinite) throw const OfflineArgumentException();
  final bytes = ByteData(4)..setFloat32(0, value, Endian.big);
  final normalized = bytes.getFloat32(0, Endian.big);
  if (!normalized.isFinite) throw const OfflineArgumentException();
  return normalized;
}
