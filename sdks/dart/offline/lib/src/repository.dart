import 'dart:async';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'remote.dart';
import 'store.dart';
import 'types.dart';

/// Storage-neutral offline Repository with explicit foreground replay.
///
/// Construction never starts background work. An offline write completes when
/// its local store transaction commits; invoke [drain], [start], or [resume] to
/// attempt bounded remote delivery.
final class OfflineLanternRepository {
  /// Creates an offline Repository over an injected transactional store/remote.
  OfflineLanternRepository({
    required this.store,
    required this.remote,
    OfflineConfig? config,
  }) : config = config ?? OfflineConfig(),
       _disposed = false {
    _readLimiter = _ReadLimiter(
      maxActive: this.config.maxReadConcurrency,
      maxActivePerPartition: this.config.maxReadConcurrencyPerPartition,
      maxQueued: this.config.maxQueuedReads,
      maxQueuedPerPartition: this.config.maxQueuedReadsPerPartition,
    );
    _replayLimiter = _ReplayLimiter(
      maxActive: this.config.maxConcurrency,
      maxActivePerPartition: this.config.maxConcurrencyPerPartition,
      maxQueued: this.config.maxQueuedReplaySends,
      maxQueuedPerPartition: this.config.maxQueuedReplaySendsPerPartition,
    );
  }

  /// Transactional partitioned local store.
  final OfflineStore store;

  /// Exact online remote port.
  final OfflineRemote remote;

  /// Explicit cache, replay, clock, and diagnostics policy.
  final OfflineConfig config;
  bool _disposed;
  final Map<_WriteStatusKey, _WriteStatusChannel> _writeStatuses =
      <_WriteStatusKey, _WriteStatusChannel>{};
  int _reservedWriteStatusControllers = 0;
  final Map<_ReadFlightKey, _ReadFlight> _singleFlights =
      <_ReadFlightKey, _ReadFlight>{};
  final Map<String, Set<_ReadFlightWaiter>> _deferredReadWaiters =
      <String, Set<_ReadFlightWaiter>>{};
  late final _ReadLimiter _readLimiter;
  final Map<String, int> _activeWatchers = <String, int>{};
  int _activeWatcherCount = 0;
  final Map<Future<void> Function(), String> _watchDisposers =
      <Future<void> Function(), String>{};
  final Set<_LeaseRenewal> _leaseRenewals = <_LeaseRenewal>{};
  final Set<Future<void>> _inFlightEnqueues = <Future<void>>{};
  final Map<String, _PartitionRuntime> _partitionRuntimes =
      <String, _PartitionRuntime>{};
  final Map<String, Future<void>> _partitionWipes = <String, Future<void>>{};
  final Set<String> _wipingPartitions = <String>{};
  late final _ReplayLimiter _replayLimiter;
  Future<void>? _disposing;

  /// Reads one vertex according to [policy].
  ///
  /// A stale record is returned only when [allowStale] is true. No policy
  /// returns a value at or after its absolute Lantern expiration.
  Future<OfflineSnapshot<Vertex>> readVertex(
    String partitionId,
    String key, {
    OfflineReadPolicy policy = OfflineReadPolicy.cacheFirst,
    bool allowStale = false,
    LanternCancellationToken? cancellation,
  }) {
    _validatePartitionAndKey(partitionId, key);
    _ensurePartitionActive(partitionId);
    final flightKey = _ReadFlightKey(
      partitionId,
      OfflineEntityKey.vertex(key),
      policy,
      allowStale,
    );
    return _singleFlight(
      flightKey,
      cancellation,
      (flightCancellation) => _runPartitionWork(
        partitionId,
        flightCancellation,
        (ownedCancellation) => _readVertex(
          partitionId,
          key,
          policy: policy,
          allowStale: allowStale,
          cancellation: ownedCancellation,
        ),
      ),
    );
  }

  /// Reads one edge according to [policy].
  ///
  /// A stale record is returned only when [allowStale] is true. No policy
  /// returns a value at or after its absolute Lantern expiration.
  Future<OfflineSnapshot<Edge>> readEdge(
    String partitionId,
    EdgeRef edge, {
    OfflineReadPolicy policy = OfflineReadPolicy.cacheFirst,
    bool allowStale = false,
    LanternCancellationToken? cancellation,
  }) {
    _validatePartitionAndKey(partitionId, edge.tail);
    if (edge.head.isEmpty) throw const OfflineArgumentException();
    _ensurePartitionActive(partitionId);
    final flightKey = _ReadFlightKey(
      partitionId,
      OfflineEntityKey.edge(edge.tail, edge.head),
      policy,
      allowStale,
    );
    return _singleFlight(
      flightKey,
      cancellation,
      (flightCancellation) => _runPartitionWork(
        partitionId,
        flightCancellation,
        (ownedCancellation) => _readEdge(
          partitionId,
          edge,
          policy: policy,
          allowStale: allowStale,
          cancellation: ownedCancellation,
        ),
      ),
    );
  }

  /// Streams the current vertex snapshot and future coarse partition changes.
  ///
  /// Subsequent updates are cache-only: callers explicitly choose when to
  /// revalidate instead of receiving hidden background network activity.
  Stream<OfflineSnapshot<Vertex>> watchVertex(
    String partitionId,
    String key, {
    OfflineReadPolicy initialPolicy = OfflineReadPolicy.cacheFirst,
    bool allowStale = false,
    LanternCancellationToken? cancellation,
  }) {
    _validatePartitionAndKey(partitionId, key);
    return _watchSnapshots<Vertex>(
      partitionId,
      initialPolicy: initialPolicy,
      load: (policy, watchCancellation) => readVertex(
        partitionId,
        key,
        policy: policy,
        allowStale: allowStale,
        cancellation: watchCancellation,
      ),
      equals: _vertexSnapshotEquals,
      cancellation: cancellation,
    );
  }

  /// Streams the current edge snapshot and future coarse partition changes.
  ///
  /// Subsequent updates are cache-only: callers explicitly choose when to
  /// revalidate instead of receiving hidden background network activity.
  Stream<OfflineSnapshot<Edge>> watchEdge(
    String partitionId,
    EdgeRef edge, {
    OfflineReadPolicy initialPolicy = OfflineReadPolicy.cacheFirst,
    bool allowStale = false,
    LanternCancellationToken? cancellation,
  }) {
    _validatePartitionAndKey(partitionId, edge.tail);
    if (edge.head.isEmpty) throw const OfflineArgumentException();
    return _watchSnapshots<Edge>(
      partitionId,
      initialPolicy: initialPolicy,
      load: (policy, watchCancellation) => readEdge(
        partitionId,
        edge,
        policy: policy,
        allowStale: allowStale,
        cancellation: watchCancellation,
      ),
      equals: _edgeSnapshotEquals,
      cancellation: cancellation,
    );
  }

  /// Durably enqueues an unconditional vertex replacement and returns after the
  /// local transaction commits.
  Future<OfflineWriteHandle> putVertex({
    required String partitionId,
    required VertexInput input,
    String? operationId,
  }) async {
    _validatePartitionAndKey(partitionId, input.key);
    _ensurePartitionActive(partitionId);
    final now = config.clock().toUtc();
    final vertex = Vertex(
      key: input.key,
      value: copyOfflineValue(input.value),
      expiration: _resolveExpiration(input.expiresIn, input.expiresAt, now),
    );
    return _enqueue(
      partitionId,
      OfflinePutVertexIntent(vertex),
      now: now,
      operationId: operationId,
    );
  }

  /// Atomically enqueues a logical plural vertex replacement operation.
  Future<OfflineWriteOperation> putVertices({
    required String partitionId,
    required Iterable<VertexInput> inputs,
    String? operationId,
  }) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    final items = inputs.toList(growable: false);
    if (items.isEmpty) throw const OfflineArgumentException();
    final now = config.clock().toUtc();
    final intents = items
        .map((input) {
          _validatePartitionAndKey(partitionId, input.key);
          final vertex = Vertex(
            key: input.key,
            value: copyOfflineValue(input.value),
            expiration: _resolveExpiration(
              input.expiresIn,
              input.expiresAt,
              now,
            ),
          );
          return () => OfflinePutVertexIntent(vertex);
        })
        .toList(growable: false);
    return _enqueueOperation(
      partitionId,
      intents,
      now: now,
      operationId: operationId,
    );
  }

  /// Durably enqueues an idempotent edge replacement and returns after the
  /// local transaction commits.
  Future<OfflineWriteHandle> putEdge({
    required String partitionId,
    required EdgeInput input,
    String? operationId,
  }) async {
    _validatePartitionAndKey(partitionId, input.tail);
    _ensurePartitionActive(partitionId);
    if (input.head.isEmpty) throw const OfflineArgumentException();
    final now = config.clock().toUtc();
    final edge = Edge(
      tail: input.tail,
      head: input.head,
      weight: normalizeOfflineFloat32(input.weight),
      expiration: _resolveExpiration(input.expiresIn, input.expiresAt, now),
    );
    return _enqueue(
      partitionId,
      OfflinePutEdgeIntent(edge),
      now: now,
      operationId: operationId,
    );
  }

  /// Atomically enqueues a logical plural edge replacement operation.
  Future<OfflineWriteOperation> putEdges({
    required String partitionId,
    required Iterable<EdgeInput> inputs,
    String? operationId,
  }) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    final items = inputs.toList(growable: false);
    if (items.isEmpty) throw const OfflineArgumentException();
    final now = config.clock().toUtc();
    final intents = items
        .map((input) {
          _validatePartitionAndKey(partitionId, input.tail);
          if (input.head.isEmpty) throw const OfflineArgumentException();
          final edge = Edge(
            tail: input.tail,
            head: input.head,
            weight: normalizeOfflineFloat32(input.weight),
            expiration: _resolveExpiration(
              input.expiresIn,
              input.expiresAt,
              now,
            ),
          );
          return () => OfflinePutEdgeIntent(edge);
        })
        .toList(growable: false);
    return _enqueueOperation(
      partitionId,
      intents,
      now: now,
      operationId: operationId,
    );
  }

  /// Returns the latest durable aggregate status for one logical write.
  Future<OfflineOperationStatus?> getWriteStatus(
    String partitionId,
    String operationId,
  ) {
    _validatePartition(partitionId);
    _validateId(operationId);
    _ensurePartitionActive(partitionId);
    return _runPartitionWork(partitionId, null, (_) async {
      await _expireOrAgeOut(partitionId, operationId: operationId);
      return _loadWriteStatus(partitionId, operationId);
    });
  }

  Future<OfflineOperationStatus?> _loadWriteStatus(
    String partitionId,
    String operationId,
  ) async {
    final record = await store.transaction(
      (transaction) => transaction.getOperation(partitionId, operationId),
    );
    return record?.status;
  }

  /// Watches one durable logical write, including after a process restart.
  ///
  /// The stream emits the current aggregate first and closes after every item
  /// reaches a terminal state. A missing or retained-away operation fails with
  /// [OfflineArgumentException].
  Stream<OfflineOperationStatus> watchWrite(
    String partitionId,
    String operationId,
  ) {
    _validatePartition(partitionId);
    _validateId(operationId);
    _ensurePartitionActive(partitionId);
    StreamSubscription<OfflineStoreChange>? changes;
    OfflineOperationStatus? previous;
    var registered = false;
    var canceled = false;
    var changeTail = Future<void>.value();
    late final StreamController<OfflineOperationStatus> controller;
    late final Future<void> Function() closeWatch;

    void release() {
      if (!registered) return;
      registered = false;
      _releaseWatcher(partitionId);
      _watchDisposers.remove(closeWatch);
    }

    Future<void> finish() async {
      canceled = true;
      Object? cancellationError;
      StackTrace? cancellationStackTrace;
      try {
        await changes?.cancel();
      } catch (error, stackTrace) {
        cancellationError = error;
        cancellationStackTrace = stackTrace;
      } finally {
        release();
        if (!controller.isClosed) await controller.close();
      }
      if (cancellationError != null) {
        Error.throwWithStackTrace(cancellationError, cancellationStackTrace!);
      }
    }

    Future<void> emit() async {
      final current = await getWriteStatus(partitionId, operationId);
      if (current == null) throw const OfflineArgumentException();
      if (canceled || controller.isClosed) return;
      if (previous == null || !_operationStatusEquals(previous!, current)) {
        previous = current;
        controller.add(current);
      }
      if (current.isTerminal) await finish();
    }

    Future<void> start() async {
      try {
        changes = store
            .changes(partitionId)
            .listen(
              (_) {
                changeTail = changeTail.then((_) async {
                  if (canceled) return;
                  try {
                    await emit();
                  } catch (error, stackTrace) {
                    if (!canceled && !controller.isClosed) {
                      controller.addError(error, stackTrace);
                      await finish();
                    }
                  }
                });
              },
              onError: (Object error, StackTrace stackTrace) {
                if (!canceled && !controller.isClosed) {
                  controller.addError(error, stackTrace);
                  unawaited(finish());
                }
              },
            );
        await emit();
      } catch (error, stackTrace) {
        if (!canceled && !controller.isClosed) {
          controller.addError(error, stackTrace);
          await finish();
        }
      }
    }

    closeWatch = finish;
    controller = StreamController<OfflineOperationStatus>(
      sync: true,
      onListen: () {
        try {
          _ensurePartitionActive(partitionId);
          _registerWatcher(partitionId);
          registered = true;
          _watchDisposers[closeWatch] = partitionId;
          unawaited(start());
        } catch (error, stackTrace) {
          controller.addError(error, stackTrace);
          unawaited(controller.close());
        }
      },
      onCancel: () async {
        canceled = true;
        await changes?.cancel();
        release();
      },
    );
    controller.onPause = () => changes?.pause();
    controller.onResume = () => changes?.resume();
    return controller.stream;
  }

  /// Explicitly performs bounded foreground replay for one partition.
  ///
  /// Replay invocations for the same partition are serialized.
  /// [OfflineConfig.maxConcurrency] and
  /// [OfflineConfig.maxConcurrencyPerPartition] govern sends across every
  /// replay entry point. Returns the number of items transactionally confirmed by this
  /// invocation. An unauthenticated failure durably pauses the partition
  /// without incrementing its attempt count; call [resume] only after
  /// credentials rotate. Other entry points throw [OfflineAuthPausedException]
  /// while that pause remains active.
  Future<int> drain(
    String partitionId, {
    LanternCancellationToken? cancellation,
  }) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _partitionRuntime(partitionId).scheduleReplay(
      cancellation,
      (ownedCancellation) =>
          _drainOwned(partitionId, cancellation: ownedCancellation),
    );
  }

  Future<int> _drainOwned(
    String partitionId, {
    required LanternCancellationToken cancellation,
  }) async {
    if (await _isReplayPausedForAuth(partitionId)) {
      throw const OfflineAuthPausedException();
    }
    final owner = config.idGenerator();
    var confirmed = 0;
    var pausedForAuth = false;
    while (!pausedForAuth) {
      _throwIfCanceled(cancellation);
      await _expireOrAgeOut(partitionId);
      final claimResult = await store.transaction((transaction) {
        final sampledAt = config.clock().toUtc();
        final recovered = transaction
            .outbox(partitionId)
            .where(
              (record) =>
                  record.state == OfflineOutboxState.sending &&
                  record.leaseUntil != null &&
                  !sampledAt.isBefore(record.leaseUntil!),
            )
            .toList(growable: false);
        for (final record in recovered) {
          _updateOperationStatus(
            transaction,
            record,
            OfflineWriteState.locallyCommitted,
            attemptCount: record.attemptCount,
            now: _transitionTime(transaction, record, sampledAt),
          );
        }
        final claimed = transaction.claim(
          partitionId,
          owner: owner,
          now: sampledAt,
          maxAge: config.maxAge,
          leaseDuration: config.leaseDuration,
          limit: config.maxConcurrencyPerPartition,
        );
        for (final record in claimed) {
          _updateOperationStatus(
            transaction,
            record,
            OfflineWriteState.sending,
            attemptCount: record.attemptCount,
            now: _transitionTime(transaction, record, sampledAt),
          );
        }
        return (claimed: claimed, recovered: recovered);
      });
      final claimed = claimResult.claimed;
      for (final record in claimResult.recovered) {
        _recordDiagnostic(
          OfflineDiagnosticEvent(
            kind: OfflineDiagnosticKind.leaseRecovered,
            category: record.intent.category,
            attempt: record.attemptCount,
          ),
        );
      }
      if (claimed.isEmpty) break;
      for (final record in claimed) {
        _emit(
          record,
          OfflineWriteState.sending,
          attemptCount: record.attemptCount,
        );
      }
      final outcomes = await Future.wait(
        claimed.map(
          (record) => _replayWithRenewal(
            partitionId,
            record,
            owner: owner,
            cancellation: cancellation,
          ),
        ),
      );
      for (final outcome in outcomes) {
        confirmed += outcome.confirmed ? 1 : 0;
        pausedForAuth = pausedForAuth || outcome.pausedForAuth;
      }
    }
    return confirmed;
  }

  /// Probes the real Lantern endpoint and drains only after the probe succeeds.
  Future<int> probeAndDrain(
    String partitionId, {
    LanternCancellationToken? cancellation,
  }) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _partitionRuntime(partitionId).scheduleReplay(cancellation, (
      ownedCancellation,
    ) async {
      if (await _isReplayPausedForAuth(partitionId)) {
        throw const OfflineAuthPausedException();
      }
      _throwIfCanceled(ownedCancellation);
      await remote.probe(cancellation: ownedCancellation);
      return _drainOwned(partitionId, cancellation: ownedCancellation);
    });
  }

  /// Alias for an explicit foreground [drain].
  Future<int> start(
    String partitionId, {
    LanternCancellationToken? cancellation,
  }) => drain(partitionId, cancellation: cancellation);

  /// Clears a durable authentication pause and performs foreground replay.
  ///
  /// Applications must rotate credentials before calling this method. Neither
  /// [drain], [start], nor [probeAndDrain] clears the pause.
  Future<int> resume(
    String partitionId, {
    LanternCancellationToken? cancellation,
  }) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _partitionRuntime(partitionId).scheduleReplay(cancellation, (
      ownedCancellation,
    ) async {
      await _clearReplayAuthPause(partitionId);
      return _drainOwned(partitionId, cancellation: ownedCancellation);
    });
  }

  /// Whether replay is durably paused waiting for credential rotation.
  Future<bool> isReplayPausedForAuth(String partitionId) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _runPartitionWork(
      partitionId,
      null,
      (_) => _isReplayPausedForAuth(partitionId),
    );
  }

  /// Lists content-free summaries for currently replayable mutation records.
  Future<List<PendingSummary>> listPending(String partitionId) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _runPartitionWork(partitionId, null, (_) async {
      await _expireOrAgeOut(partitionId);
      return store.transaction((transaction) {
        final now = config.clock().toUtc();
        return transaction
            .outbox(partitionId)
            .where(
              (record) =>
                  (record.state == OfflineOutboxState.enqueued ||
                      record.state == OfflineOutboxState.sending) &&
                  _live(record.absoluteExpiration, now) &&
                  now.difference(record.enqueuedAt) < config.maxAge,
            )
            .map(
              (record) => PendingSummary(
                recordId: record.recordId,
                operationId: record.operationId,
                category: record.intent.category,
                state: record.state,
                age: now.difference(record.enqueuedAt),
                attemptCount: record.attemptCount,
                diagnosticCode: record.diagnosticCode,
              ),
            )
            .toList(growable: false);
      });
    });
  }

  /// Lists content-free dead-letter summaries for a partition.
  Future<List<DeadLetterSummary>> listDeadLetters(String partitionId) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _runPartitionWork(partitionId, null, (_) async {
      await _expireOrAgeOut(partitionId);
      return store.transaction((transaction) {
        final now = config.clock().toUtc();
        return transaction
            .outbox(partitionId)
            .where(
              (record) =>
                  record.state == OfflineOutboxState.deadLetter &&
                  now.difference(record.deadLetteredAt!) <
                      config.deadLetterRetention,
            )
            .map(
              (record) => DeadLetterSummary(
                recordId: record.recordId,
                category: record.intent.category,
                state: record.state,
                age: now.difference(record.deadLetteredAt!),
                attemptCount: record.attemptCount,
                diagnosticCode: record.diagnosticCode,
              ),
            )
            .toList(growable: false);
      });
    });
  }

  /// Returns a sensitive intent only after [authorize] grants application access.
  Future<OfflineIntent?> inspectDeadLetter(
    String partitionId,
    String recordId, {
    required OfflineDeadLetterAuthorizer authorize,
  }) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _runPartitionWork(partitionId, null, (cancellation) async {
      await _expireOrAgeOut(partitionId, recordId: recordId);
      final inspected = await store.transaction((transaction) {
        final record = transaction.getOutbox(partitionId, recordId);
        if (record == null || record.state != OfflineOutboxState.deadLetter) {
          return null;
        }
        final summary = DeadLetterSummary(
          recordId: record.recordId,
          category: record.intent.category,
          state: record.state,
          age: config.clock().toUtc().difference(record.deadLetteredAt!),
          attemptCount: record.attemptCount,
          diagnosticCode: record.diagnosticCode,
        );
        return (
          summary: summary,
          intent: copyOfflineIntent(record.intent),
          operationId: record.operationId,
          itemIndex: record.itemIndex,
          generation: record.generation,
          ordinal: record.ordinal,
          enqueuedAt: record.enqueuedAt,
        );
      });
      if (inspected == null) return null;
      _throwIfCanceled(cancellation);
      if (!await authorize(inspected.summary)) {
        throw const OfflineAuthorizationException();
      }
      _throwIfCanceled(cancellation);
      final unchanged = await store.transaction((transaction) {
        final current = transaction.getOutbox(partitionId, recordId);
        return current != null &&
            current.state == OfflineOutboxState.deadLetter &&
            current.recordId == inspected.summary.recordId &&
            current.operationId == inspected.operationId &&
            current.itemIndex == inspected.itemIndex &&
            current.generation == inspected.generation &&
            current.ordinal == inspected.ordinal &&
            current.enqueuedAt == inspected.enqueuedAt;
      });
      _throwIfCanceled(cancellation);
      return unchanged ? copyOfflineIntent(inspected.intent) : null;
    });
  }

  /// Returns a dead-letter item to explicit replay after application inspection.
  Future<void> retryDeadLetter(String partitionId, String recordId) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _runPartitionWork(partitionId, null, (_) async {
      await _expireOrAgeOut(partitionId, recordId: recordId);
      final result = await store.transaction((transaction) {
        final record = transaction.getOutbox(partitionId, recordId);
        if (record == null || record.state != OfflineOutboxState.deadLetter) {
          throw const OfflineArgumentException();
        }
        if (record.intent is OfflineAddEdgeIntent) {
          throw const OfflineUnsupportedOperationException();
        }
        final now = config.clock().toUtc();
        if (!_live(record.absoluteExpiration, now)) {
          _updateOperationStatus(
            transaction,
            record,
            OfflineWriteState.expired,
            attemptCount: record.attemptCount,
            diagnosticCode: 'expired',
            now: now,
          );
          transaction.deleteOutbox(partitionId, recordId);
          return (record: record, expired: true);
        }
        transaction.updateOutbox(
          record.copyWith(
            state: OfflineOutboxState.enqueued,
            attemptCount: 0,
            clearNextAttemptAt: true,
            clearLeaseOwner: true,
            clearLeaseUntil: true,
            clearDeadLetteredAt: true,
            clearDiagnosticCode: true,
          ),
        );
        _updateOperationStatus(
          transaction,
          record,
          OfflineWriteState.locallyCommitted,
          attemptCount: 0,
          now: now,
        );
        return (record: record, expired: false);
      });
      _emit(
        result.record,
        result.expired
            ? OfflineWriteState.expired
            : OfflineWriteState.locallyCommitted,
        attemptCount: result.expired ? result.record.attemptCount : 0,
        diagnosticCode: result.expired ? 'expired' : null,
      );
    });
  }

  /// Deletes one inspected terminal dead-letter item.
  Future<void> deleteDeadLetter(String partitionId, String recordId) {
    _validatePartition(partitionId);
    _ensurePartitionActive(partitionId);
    return _runPartitionWork(partitionId, null, (_) async {
      await _expireOrAgeOut(partitionId, recordId: recordId);
      await store.transaction((transaction) {
        final record = transaction.getOutbox(partitionId, recordId);
        if (record == null || record.state != OfflineOutboxState.deadLetter) {
          throw const OfflineArgumentException();
        }
        transaction.deleteOutbox(partitionId, recordId);
      });
    });
  }

  /// Quiesces partition work, then transactionally wipes local state for logout.
  ///
  /// The ordering is security-sensitive: new reads, sends, probes, and token
  /// acquisition are blocked first; owned work and watchers are canceled and
  /// awaited; only then does the store increment generation and remove cache,
  /// outbox, operation, dead-letter, and lease state. Rotate credentials only
  /// after this Future completes. A mutation already accepted by Lantern cannot
  /// be recalled: wipe provides local isolation and prevents further sends, not
  /// remote rollback.
  ///
  /// [partitionId] is solely a local persistence namespace. It is never sent on
  /// the wire and is not a Lantern tenant, authorization, or identity boundary.
  Future<void> wipePartition(String partitionId) {
    _validatePartition(partitionId);
    _ensureActive();
    final existing = _partitionWipes[partitionId];
    if (existing != null) return existing;
    final runtime =
        _partitionRuntimes[partitionId] ?? _partitionRuntime(partitionId);
    _wipingPartitions.add(partitionId);
    runtime.beginQuiesce();
    var stateWiped = false;
    late final Future<void> wiping;
    wiping =
        _wipePartitionOwned(
          partitionId,
          runtime,
          markStateWiped: () => stateWiped = true,
        ).whenComplete(() {
          if (!identical(_partitionWipes[partitionId], wiping)) return;
          _partitionWipes.remove(partitionId);
          if (stateWiped) {
            _wipingPartitions.remove(partitionId);
            if (identical(_partitionRuntimes[partitionId], runtime)) {
              _partitionRuntimes.remove(partitionId);
            }
          }
        });
    _partitionWipes[partitionId] = wiping;
    return wiping;
  }

  Future<void> _wipePartitionOwned(
    String partitionId,
    _PartitionRuntime runtime, {
    required void Function() markStateWiped,
  }) async {
    Object? firstError;
    StackTrace? firstStackTrace;
    Future<void> cleanup(Future<void> Function() action) async {
      try {
        await action();
      } catch (error, stackTrace) {
        firstError ??= error;
        firstStackTrace ??= stackTrace;
      }
    }

    final flights = _singleFlights.entries
        .where((entry) => entry.key.partitionId == partitionId)
        .map((entry) => entry.value)
        .toSet()
        .toList(growable: false);
    _singleFlights.removeWhere((key, _) => key.partitionId == partitionId);
    for (final flight in flights) {
      flight.cancel(const OfflineCanceledException());
    }
    final deferredReadWaiters = _takeDeferredReadWaiters(partitionId);
    for (final waiter in deferredReadWaiters) {
      waiter.removeCancellationRegistration();
      waiter.completeError(
        const OfflineCanceledException(),
        StackTrace.current,
      );
    }
    final watchers = _watchDisposers.entries
        .where((entry) => entry.value == partitionId)
        .map((entry) => entry.key)
        .toList(growable: false);
    for (final close in watchers) {
      await cleanup(close);
    }
    for (final waiter in deferredReadWaiters) {
      await cleanup(() => waiter.settled);
    }
    await cleanup(runtime.waitQuiesced);
    for (final flight in flights) {
      await cleanup(() => flight.settled);
    }
    final renewals = _leaseRenewals
        .where((renewal) => renewal.partitionId == partitionId)
        .toList(growable: false);
    for (final renewal in renewals) {
      await cleanup(renewal.stop);
      _leaseRenewals.remove(renewal);
    }
    var stateWiped = false;
    await cleanup(() async {
      await store.transaction((transaction) {
        transaction.wipePartition(partitionId);
      });
      stateWiped = true;
      markStateWiped();
    });
    final ids = _writeStatuses.keys
        .where((id) => id.partitionId == partitionId)
        .toList(growable: false);
    for (final id in ids) {
      final channel = _writeStatuses.remove(id);
      if (channel != null) await cleanup(channel.close);
    }
    if (stateWiped) {
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.partitionWiped,
        ),
      );
    }
    if (firstError != null) {
      Error.throwWithStackTrace(firstError!, firstStackTrace!);
    }
  }

  /// Quiesces every owned call, queue, timer, lease, and watcher permanently.
  Future<void> dispose() {
    final existing = _disposing;
    if (existing != null) return existing;
    _disposed = true;
    final disposing = _disposeOwned();
    _disposing = disposing;
    return disposing;
  }

  Future<void> _disposeOwned() async {
    Object? firstError;
    StackTrace? firstStackTrace;
    Future<void> cleanup(Future<void> Function() action) async {
      try {
        await action();
      } catch (error, stackTrace) {
        firstError ??= error;
        firstStackTrace ??= stackTrace;
      }
    }

    final enqueues = _inFlightEnqueues.toList(growable: false);
    for (final enqueue in enqueues) {
      await cleanup(() => enqueue);
    }
    final channels = _writeStatuses.values.toList(growable: false);
    _writeStatuses.clear();
    final flights = _singleFlights.values.toSet().toList(growable: false);
    _singleFlights.clear();
    final deferredReadWaiters = _deferredReadWaiters.values
        .expand((waiters) => waiters)
        .toList(growable: false);
    _deferredReadWaiters.clear();
    for (final waiter in deferredReadWaiters) {
      waiter.removeCancellationRegistration();
      waiter.completeError(
        const OfflineDisposedException(),
        StackTrace.current,
      );
    }
    for (final flight in flights) {
      flight.cancel(const OfflineDisposedException());
    }
    _readLimiter.dispose();
    _replayLimiter.dispose();
    final runtimes = _partitionRuntimes.values.toList(growable: false);
    for (final runtime in runtimes) {
      runtime.beginQuiesce(error: const OfflineDisposedException());
    }
    final watchers = _watchDisposers.keys.toList(growable: false);
    _watchDisposers.clear();
    for (final close in watchers) {
      await cleanup(close);
    }
    for (final waiter in deferredReadWaiters) {
      await cleanup(() => waiter.settled);
    }
    for (final runtime in runtimes) {
      await cleanup(runtime.waitQuiesced);
    }
    for (final flight in flights) {
      await cleanup(() => flight.settled);
    }
    final wipes = _partitionWipes.values.toList(growable: false);
    for (final wipe in wipes) {
      await cleanup(() => wipe);
    }
    final renewals = _leaseRenewals.toList(growable: false);
    _leaseRenewals.clear();
    for (final renewal in renewals) {
      await cleanup(renewal.stop);
    }
    for (final channel in channels) {
      await cleanup(channel.close);
    }
    if (firstError != null) {
      Error.throwWithStackTrace(firstError!, firstStackTrace!);
    }
  }

  Stream<OfflineSnapshot<T>> _watchSnapshots<T>(
    String partitionId, {
    required OfflineReadPolicy initialPolicy,
    required Future<OfflineSnapshot<T>> Function(
      OfflineReadPolicy policy,
      LanternCancellationToken cancellation,
    )
    load,
    required bool Function(OfflineSnapshot<T>, OfflineSnapshot<T>) equals,
    required LanternCancellationToken? cancellation,
  }) {
    _ensurePartitionActive(partitionId);
    OfflineSnapshot<T>? previous;
    StreamSubscription<OfflineStoreChange>? changes;
    final watchCancellation = LanternCancellationToken();
    void Function()? removeCancellationListener;
    var canceled = false;
    var registered = false;
    var paused = false;
    var initialComplete = false;
    var pendingChange = false;
    var changeTail = Future<void>.value();
    Future<void>? closing;
    late final StreamController<OfflineSnapshot<T>> controller;
    late final Future<void> Function() closeWatch;

    void releaseWatcher() {
      if (!registered) return;
      registered = false;
      _releaseWatcher(partitionId);
      _watchDisposers.remove(closeWatch);
    }

    Future<void> emit(OfflineReadPolicy policy) async {
      _throwIfCanceled(watchCancellation);
      final current = await load(policy, watchCancellation);
      if (canceled || controller.isClosed) return;
      if (previous != null && equals(previous!, current)) return;
      previous = current;
      controller.add(current);
    }

    void enqueueChange() {
      changeTail = changeTail.then((_) async {
        if (canceled) return;
        try {
          await emit(OfflineReadPolicy.cacheOnly);
        } catch (error, stackTrace) {
          if (!canceled &&
              !controller.isClosed &&
              error is! OfflineCanceledException) {
            controller.addError(error, stackTrace);
          }
        }
      });
    }

    void subscribeToChanges() {
      changes = store
          .changes(partitionId)
          .listen(
            (_) {
              if (canceled) return;
              if (!initialComplete) {
                pendingChange = true;
                return;
              }
              enqueueChange();
            },
            onError: (Object error, StackTrace stackTrace) {
              if (!canceled && !controller.isClosed) {
                controller.addError(error, stackTrace);
                unawaited(closeWatch());
              }
            },
          );
      if (paused) changes!.pause();
    }

    Future<void> startWatching() async {
      try {
        // Register the change listener before the initial read. Changes that
        // arrive while the initial snapshot is in flight are reconciled after
        // that snapshot, so every continuing policy has a no-gap handoff.
        subscribeToChanges();
        if (initialPolicy == OfflineReadPolicy.cacheFirst) {
          await emit(OfflineReadPolicy.cacheOnly);
          if (canceled) return;
          try {
            await emit(OfflineReadPolicy.serverOnly);
          } catch (error, stackTrace) {
            if (!canceled &&
                !controller.isClosed &&
                error is! OfflineCanceledException) {
              controller.addError(error, stackTrace);
            }
          }
        } else {
          await emit(initialPolicy);
        }
      } catch (error, stackTrace) {
        if (error is OfflineCanceledException) {
          await closeWatch();
        } else if (!canceled && !controller.isClosed) {
          controller.addError(error, stackTrace);
          await closeWatch();
        }
      } finally {
        initialComplete = true;
        if (pendingChange && !canceled) {
          pendingChange = false;
          enqueueChange();
        }
      }
    }

    closeWatch = () {
      final existing = closing;
      if (existing != null) return existing;
      final result = () async {
        canceled = true;
        watchCancellation.cancel();
        removeCancellationListener?.call();
        removeCancellationListener = null;
        final subscription = changes;
        changes = null;
        previous = null;
        releaseWatcher();
        try {
          await subscription?.cancel();
        } finally {
          if (!controller.isClosed) await controller.close();
        }
      }();
      closing = result;
      return result;
    };
    controller = StreamController<OfflineSnapshot<T>>(
      sync: true,
      onListen: () {
        try {
          _ensurePartitionActive(partitionId);
          if (cancellation?.isCanceled ?? false) {
            unawaited(controller.close());
            return;
          }
          _registerWatcher(partitionId);
          registered = true;
          _watchDisposers[closeWatch] = partitionId;
          removeCancellationListener = cancellation?.listen((_) {
            unawaited(closeWatch());
          });
          unawaited(startWatching());
        } catch (error, stackTrace) {
          controller.addError(error, stackTrace);
          unawaited(controller.close());
        }
      },
      onCancel: closeWatch,
    );
    controller.onPause = () {
      paused = true;
      changes?.pause();
    };
    controller.onResume = () {
      paused = false;
      changes?.resume();
    };
    return controller.stream;
  }

  Future<OfflineSnapshot<Vertex>> _readVertex(
    String partitionId,
    String key, {
    required OfflineReadPolicy policy,
    required bool allowStale,
    required LanternCancellationToken? cancellation,
  }) async {
    await _expireOrAgeOut(partitionId, entityKey: OfflineEntityKey.vertex(key));
    final local = await _localVertex(partitionId, key);
    if (_usesLocalFirst(policy) && _eligible(local, allowStale: allowStale)) {
      return local;
    }
    if (policy == OfflineReadPolicy.cacheOnly) {
      return _withoutIneligible(local);
    }
    final generation = await store.transaction(
      (transaction) => transaction.generation(partitionId),
    );
    try {
      _throwIfCanceled(cancellation);
      final read = await _withReadPermit(
        partitionId,
        cancellation,
        () => remote.getVertex(key, cancellation: cancellation),
      );
      _throwIfCanceled(cancellation);
      final now = config.clock().toUtc();
      switch (read) {
        case OfflineRemotePresent<Vertex>(:final value):
          if (!_live(value.expiration, now)) {
            final applied = await _removeCache(
              partitionId,
              OfflineEntityKey.vertex(key),
              generation: generation,
            );
            if (!applied) {
              _recordDiagnostic(
                const OfflineDiagnosticEvent(
                  kind: OfflineDiagnosticKind.staleOutcomeRejected,
                ),
              );
              return _unknown<Vertex>();
            }
            return OfflineSnapshot<Vertex>(
              state: OfflineReadState.expired,
              source: OfflineReadSource.server,
              expiredAt: value.expiration,
            );
          }
          final stored = await _storeCache(
            partitionId,
            OfflineCacheRecord.value(
              partitionId: partitionId,
              generation: generation,
              key: OfflineEntityKey.vertex(key),
              entity: value,
              validatedAt: now,
              lastAccessAt: now,
            ),
          );
          if (stored == _CacheStoreOutcome.staleGeneration) {
            return _unknown<Vertex>();
          }
          if (stored == _CacheStoreOutcome.capacityRejected) {
            return _localVertex(
              partitionId,
              key,
              fallback: OfflineSnapshot<Vertex>(
                state: OfflineReadState.fresh,
                source: OfflineReadSource.server,
                value: value,
                validatedAt: now,
              ),
            );
          }
        case OfflineRemoteMissing<Vertex>():
          final stored = await _storeCache(
            partitionId,
            OfflineCacheRecord.missing(
              partitionId: partitionId,
              generation: generation,
              key: OfflineEntityKey.vertex(key),
              validatedAt: now,
              lastAccessAt: now,
              missingUntil: _durableDeadline(now, config.missingTtl),
            ),
          );
          if (stored == _CacheStoreOutcome.staleGeneration) {
            return _unknown<Vertex>();
          }
          if (stored == _CacheStoreOutcome.capacityRejected) {
            return _localVertex(
              partitionId,
              key,
              fallback: OfflineSnapshot<Vertex>(
                state: OfflineReadState.missing,
                source: OfflineReadSource.server,
                validatedAt: now,
              ),
            );
          }
      }
      final currentGeneration = await store.transaction(
        (transaction) => transaction.generation(partitionId),
      );
      if (currentGeneration != generation) return _unknown<Vertex>();
      return _withSource(
        await _localVertex(partitionId, key),
        OfflineReadSource.server,
      );
    } on OfflineRemoteFailure catch (error) {
      if (error.kind == OfflineRemoteErrorKind.canceled) {
        throw const OfflineCanceledException();
      }
      if (policy == OfflineReadPolicy.serverFirst &&
          _eligible(local, allowStale: allowStale)) {
        return local;
      }
      return OfflineSnapshot<Vertex>(
        state: OfflineReadState.unknown,
        source: null,
        cause: error,
      );
    } on OfflineCanceledException {
      rethrow;
    }
  }

  Future<OfflineSnapshot<Edge>> _readEdge(
    String partitionId,
    EdgeRef edge, {
    required OfflineReadPolicy policy,
    required bool allowStale,
    required LanternCancellationToken? cancellation,
  }) async {
    await _expireOrAgeOut(
      partitionId,
      entityKey: OfflineEntityKey.edge(edge.tail, edge.head),
    );
    final local = await _localEdge(partitionId, edge);
    if (_usesLocalFirst(policy) && _eligible(local, allowStale: allowStale)) {
      return local;
    }
    if (policy == OfflineReadPolicy.cacheOnly) {
      return _withoutIneligible(local);
    }
    final generation = await store.transaction(
      (transaction) => transaction.generation(partitionId),
    );
    try {
      _throwIfCanceled(cancellation);
      final read = await _withReadPermit(
        partitionId,
        cancellation,
        () => remote.getEdge(edge, cancellation: cancellation),
      );
      _throwIfCanceled(cancellation);
      final now = config.clock().toUtc();
      switch (read) {
        case OfflineRemotePresent<Edge>(:final value):
          if (!_live(value.expiration, now)) {
            await _removeCache(
              partitionId,
              OfflineEntityKey.edge(edge.tail, edge.head),
              generation: generation,
            );
            final currentGeneration = await store.transaction(
              (transaction) => transaction.generation(partitionId),
            );
            if (currentGeneration != generation) {
              _recordDiagnostic(
                const OfflineDiagnosticEvent(
                  kind: OfflineDiagnosticKind.staleOutcomeRejected,
                ),
              );
              return _unknown<Edge>();
            }
            return OfflineSnapshot<Edge>(
              state: OfflineReadState.expired,
              source: OfflineReadSource.server,
              expiredAt: value.expiration,
            );
          }
          final stored = await _storeCache(
            partitionId,
            OfflineCacheRecord.value(
              partitionId: partitionId,
              generation: generation,
              key: OfflineEntityKey.edge(edge.tail, edge.head),
              entity: value,
              validatedAt: now,
              lastAccessAt: now,
            ),
          );
          if (stored == _CacheStoreOutcome.staleGeneration) {
            return _unknown<Edge>();
          }
          if (stored == _CacheStoreOutcome.capacityRejected) {
            return _localEdge(
              partitionId,
              edge,
              fallback: OfflineSnapshot<Edge>(
                state: OfflineReadState.fresh,
                source: OfflineReadSource.server,
                value: value,
                validatedAt: now,
              ),
            );
          }
        case OfflineRemoteMissing<Edge>():
          final stored = await _storeCache(
            partitionId,
            OfflineCacheRecord.missing(
              partitionId: partitionId,
              generation: generation,
              key: OfflineEntityKey.edge(edge.tail, edge.head),
              validatedAt: now,
              lastAccessAt: now,
              missingUntil: _durableDeadline(now, config.missingTtl),
            ),
          );
          if (stored == _CacheStoreOutcome.staleGeneration) {
            return _unknown<Edge>();
          }
          if (stored == _CacheStoreOutcome.capacityRejected) {
            return _localEdge(
              partitionId,
              edge,
              fallback: OfflineSnapshot<Edge>(
                state: OfflineReadState.missing,
                source: OfflineReadSource.server,
                validatedAt: now,
              ),
            );
          }
      }
      final currentGeneration = await store.transaction(
        (transaction) => transaction.generation(partitionId),
      );
      if (currentGeneration != generation) return _unknown<Edge>();
      return _withSource(
        await _localEdge(partitionId, edge),
        OfflineReadSource.server,
      );
    } on OfflineRemoteFailure catch (error) {
      if (error.kind == OfflineRemoteErrorKind.canceled) {
        throw const OfflineCanceledException();
      }
      if (policy == OfflineReadPolicy.serverFirst &&
          _eligible(local, allowStale: allowStale)) {
        return local;
      }
      return OfflineSnapshot<Edge>(
        state: OfflineReadState.unknown,
        source: null,
        cause: error,
      );
    } on OfflineCanceledException {
      rethrow;
    }
  }

  Future<OfflineSnapshot<Vertex>> _localVertex(
    String partitionId,
    String key, {
    OfflineSnapshot<Vertex>? fallback,
  }) async {
    final snapshot = await store.transaction((transaction) {
      final now = config.clock().toUtc();
      final identity = OfflineEntityKey.vertex(key);
      final record = transaction.getCache(partitionId, identity);
      var base = record == null && fallback != null
          ? fallback
          : _baseVertex(transaction, partitionId, identity, record, now);
      final pending = _livePending(
        transaction.outboxForKey(partitionId, identity),
        now,
      );
      for (final item in pending) {
        if (item.intent case OfflinePutVertexIntent(:final vertex)) {
          base = OfflineSnapshot<Vertex>(
            state: OfflineReadState.fresh,
            source: base.source,
            value: copyOfflineVertex(vertex),
            validatedAt: base.validatedAt,
            hasPendingWrites: true,
          );
        }
      }
      return pending.isEmpty ? base : _markPending(base);
    });
    _recordReadDiagnostic(snapshot);
    return snapshot;
  }

  Future<OfflineSnapshot<Edge>> _localEdge(
    String partitionId,
    EdgeRef edge, {
    OfflineSnapshot<Edge>? fallback,
  }) async {
    final snapshot = await store.transaction((transaction) {
      final now = config.clock().toUtc();
      final identity = OfflineEntityKey.edge(edge.tail, edge.head);
      final record = transaction.getCache(partitionId, identity);
      var base = record == null && fallback != null
          ? fallback
          : _baseEdge(transaction, partitionId, identity, record, now);
      final pending = _livePending(
        transaction.outboxForKey(partitionId, identity),
        now,
      );
      for (final item in pending) {
        switch (item.intent) {
          case OfflinePutEdgeIntent(:final edge):
            base = OfflineSnapshot<Edge>(
              state: OfflineReadState.fresh,
              source: base.source,
              value: copyOfflineEdge(edge),
              validatedAt: base.validatedAt,
              hasPendingWrites: true,
            );
          case OfflineAddEdgeIntent():
            // Migration-only Add records are filtered out by [_livePending].
            break;
          case OfflinePutVertexIntent():
            throw StateError('vertex intent has edge ordering key');
        }
      }
      return pending.isEmpty ? base : _markPending(base);
    });
    _recordReadDiagnostic(snapshot);
    return snapshot;
  }

  OfflineSnapshot<Vertex> _baseVertex(
    OfflineStoreTransaction transaction,
    String partitionId,
    OfflineEntityKey key,
    OfflineCacheRecord? record,
    DateTime now,
  ) {
    if (record == null) return _unknown<Vertex>();
    if (record.isMissing) {
      if (_live(record.missingUntil, now)) {
        transaction.touchCache(partitionId, key, now);
        return OfflineSnapshot<Vertex>(
          state: OfflineReadState.missing,
          source: OfflineReadSource.cache,
          validatedAt: record.validatedAt,
        );
      }
      transaction.deleteCache(partitionId, key);
      return _unknown<Vertex>();
    }
    if (!_live(record.expiration, now)) {
      transaction.deleteCache(partitionId, key);
      return OfflineSnapshot<Vertex>(
        state: OfflineReadState.expired,
        source: OfflineReadSource.cache,
        expiredAt: record.expiration,
      );
    }
    transaction.touchCache(partitionId, key, now);
    return OfflineSnapshot<Vertex>(
      state: _fresh(record.validatedAt, now)
          ? OfflineReadState.fresh
          : OfflineReadState.stale,
      source: OfflineReadSource.cache,
      value: copyOfflineVertex(record.vertex!),
      validatedAt: record.validatedAt,
    );
  }

  OfflineSnapshot<Edge> _baseEdge(
    OfflineStoreTransaction transaction,
    String partitionId,
    OfflineEntityKey key,
    OfflineCacheRecord? record,
    DateTime now,
  ) {
    if (record == null) return _unknown<Edge>();
    if (record.isMissing) {
      if (_live(record.missingUntil, now)) {
        transaction.touchCache(partitionId, key, now);
        return OfflineSnapshot<Edge>(
          state: OfflineReadState.missing,
          source: OfflineReadSource.cache,
          validatedAt: record.validatedAt,
        );
      }
      transaction.deleteCache(partitionId, key);
      return _unknown<Edge>();
    }
    if (!_live(record.expiration, now)) {
      transaction.deleteCache(partitionId, key);
      return OfflineSnapshot<Edge>(
        state: OfflineReadState.expired,
        source: OfflineReadSource.cache,
        expiredAt: record.expiration,
      );
    }
    transaction.touchCache(partitionId, key, now);
    return OfflineSnapshot<Edge>(
      state: _fresh(record.validatedAt, now)
          ? OfflineReadState.fresh
          : OfflineReadState.stale,
      source: OfflineReadSource.cache,
      value: copyOfflineEdge(record.edge!),
      validatedAt: record.validatedAt,
    );
  }

  Future<OfflineWriteHandle> _enqueue(
    String partitionId,
    OfflineIntent intent, {
    required DateTime now,
    required String? operationId,
  }) async => (await _enqueueOperation(
    partitionId,
    <OfflineIntent Function()>[() => intent],
    now: now,
    operationId: operationId,
  )).items.single;

  Future<OfflineWriteOperation> _enqueueOperation(
    String partitionId,
    List<OfflineIntent Function()> intentBuilders, {
    required DateTime now,
    required String? operationId,
  }) {
    final operation = _runPartitionWork(
      partitionId,
      null,
      (_) => _enqueueOperationOwned(
        partitionId,
        intentBuilders,
        now: now,
        operationId: operationId,
      ),
    );
    late final Future<void> settled;
    settled = operation
        .then<void>((_) {}, onError: (Object _, StackTrace _) {})
        .whenComplete(() => _inFlightEnqueues.remove(settled));
    _inFlightEnqueues.add(settled);
    return operation;
  }

  Future<OfflineWriteOperation> _enqueueOperationOwned(
    String partitionId,
    List<OfflineIntent Function()> intentBuilders, {
    required DateTime now,
    required String? operationId,
  }) async {
    if (intentBuilders.isEmpty) throw const OfflineArgumentException();
    await _expireOrAgeOut(partitionId);
    _ensureActive();
    final intents = intentBuilders
        .map((builder) => builder())
        .toList(growable: false);
    final liveControllerCount = intents
        .where((intent) => _live(intent.expiration, now))
        .length;
    if (_writeStatuses.length +
            _reservedWriteStatusControllers +
            liveControllerCount >
        config.maxWriteStatusControllers) {
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.capacityRejected,
        ),
      );
      throw const OfflineCapacityException();
    }
    _reservedWriteStatusControllers += liveControllerCount;
    late final String opId;
    late final List<OfflineOutboxRecord> records;
    try {
      for (var attempt = 0; ; attempt++) {
        final candidateOperationId = operationId ?? config.idGenerator();
        _validateId(candidateOperationId);
        final recordIds = List<String>.generate(
          intents.length,
          (_) => config.idGenerator(),
          growable: false,
        );
        for (final recordId in recordIds) {
          _validateId(recordId);
        }
        try {
          final assigned = await store.transaction((transaction) {
            _ensureActive();
            final committedAt = config.clock().toUtc();
            final generation = transaction.generation(partitionId);
            final pending = List<OfflineOutboxRecord>.generate(intents.length, (
              index,
            ) {
              final intent = intents[index];
              final state =
                  !_live(intent.expiration, now) ||
                      !_live(intent.expiration, committedAt)
                  ? OfflineOutboxState.expired
                  : OfflineOutboxState.enqueued;
              return OfflineOutboxRecord(
                recordId: recordIds[index],
                operationId: candidateOperationId,
                itemIndex: index,
                partitionId: partitionId,
                intent: intent,
                enqueuedAt: now,
                ordinal: 0,
                state: state,
                attemptCount: 0,
                generation: generation,
                diagnosticCode: state == OfflineOutboxState.expired
                    ? 'expired'
                    : null,
              );
            }, growable: false);
            final result = transaction.enqueueAll(pending);
            for (final record in result) {
              if (record.state == OfflineOutboxState.expired) {
                transaction.deleteOutbox(partitionId, record.recordId);
              }
            }
            _putInitialOperation(
              transaction,
              result,
              committedAt.isAfter(now) ? committedAt : now,
            );
            return result;
          });
          opId = candidateOperationId;
          records = assigned;
          break;
        } on OfflineIdentityConflictException catch (conflict) {
          if (conflict.kind == OfflineIdentityKind.operation &&
              operationId != null) {
            rethrow;
          }
          if (attempt + 1 >= config.maxGeneratedIdAttempts) {
            throw const OfflineIdGenerationException();
          }
        }
      }
    } on OfflineCapacityException {
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.capacityRejected,
        ),
      );
      rethrow;
    } finally {
      _reservedWriteStatusControllers -= liveControllerCount;
    }
    final handles = <OfflineWriteHandle>[];
    for (final record in records) {
      final initial = record.state == OfflineOutboxState.expired
          ? OfflineWriteState.expired
          : OfflineWriteState.locallyCommitted;
      final handle = initial == OfflineWriteState.expired
          ? _terminalHandle(record, initial)
          : _handle(partitionId, record, initial);
      _emit(record, initial, attemptCount: record.attemptCount);
      handles.add(handle);
    }
    return OfflineWriteOperation(operationId: opId, items: handles);
  }

  Future<_ReplayOutcome> _replayWithRenewal(
    String partitionId,
    OfflineOutboxRecord claimed, {
    required String owner,
    required LanternCancellationToken? cancellation,
  }) async {
    late final _ReplayPermit permit;
    try {
      permit = await _replayLimiter.acquire(partitionId, cancellation);
    } on OfflineCapacityException {
      await _releaseClaim(partitionId, claimed, owner);
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.capacityRejected,
        ),
      );
      rethrow;
    } on OfflineCanceledException {
      await _releaseClaim(partitionId, claimed, owner);
      rethrow;
    } on OfflineDisposedException {
      await _releaseClaim(partitionId, claimed, owner);
      rethrow;
    }
    _LeaseRenewal? renewal;
    try {
      _throwIfCanceled(cancellation);
      final claimState = await _claimSendState(partitionId, claimed, owner);
      if (claimState == _ClaimSendState.pausedForAuth) {
        await _pauseClaimForAuth(partitionId, claimed, owner);
        return const _ReplayOutcome(pausedForAuth: true);
      }
      if (claimState == _ClaimSendState.stale) {
        await _releaseClaim(partitionId, claimed, owner);
        return const _ReplayOutcome();
      }
      renewal = _startLeaseRenewal(partitionId, claimed, owner);
      return await _replayOne(
        partitionId,
        claimed,
        owner: owner,
        cancellation: cancellation,
      );
    } finally {
      if (renewal != null) {
        await renewal.stop();
        _leaseRenewals.remove(renewal);
      }
      permit.release();
    }
  }

  Future<_ClaimSendState> _claimSendState(
    String partitionId,
    OfflineOutboxRecord claimed,
    String owner,
  ) => store.transaction((transaction) {
    final current = transaction.getOutbox(partitionId, claimed.recordId);
    final now = config.clock().toUtc();
    final valid =
        current != null &&
        current.generation == claimed.generation &&
        current.state == OfflineOutboxState.sending &&
        current.leaseOwner == owner &&
        current.leaseUntil != null &&
        now.isBefore(current.leaseUntil!) &&
        transaction.generation(partitionId) == claimed.generation;
    if (!valid) return _ClaimSendState.stale;
    return transaction.replayPausedForAuth(partitionId)
        ? _ClaimSendState.pausedForAuth
        : _ClaimSendState.sendable;
  });

  Future<void> _pauseClaimForAuth(
    String partitionId,
    OfflineOutboxRecord claimed,
    String owner,
  ) async {
    final paused = await store.transaction((transaction) {
      final current = transaction.getOutbox(partitionId, claimed.recordId);
      if (current == null ||
          !transaction.replayPausedForAuth(partitionId) ||
          current.generation != claimed.generation ||
          current.state != OfflineOutboxState.sending ||
          current.leaseOwner != owner ||
          transaction.generation(partitionId) != claimed.generation) {
        return false;
      }
      transaction.updateOutbox(
        current.copyWith(
          state: OfflineOutboxState.enqueued,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          diagnosticCode: _authPauseDiagnostic,
        ),
      );
      _updateOperationStatus(
        transaction,
        current,
        OfflineWriteState.pausedForAuth,
        attemptCount: current.attemptCount,
        diagnosticCode: _authPauseDiagnostic,
        now: config.clock().toUtc(),
      );
      return true;
    });
    if (paused) {
      _emit(
        claimed,
        OfflineWriteState.pausedForAuth,
        attemptCount: claimed.attemptCount,
        diagnosticCode: _authPauseDiagnostic,
      );
    }
  }

  Future<_ReplayOutcome> _replayOne(
    String partitionId,
    OfflineOutboxRecord claimed, {
    required String owner,
    required LanternCancellationToken? cancellation,
  }) async {
    if (claimed.intent is OfflineAddEdgeIntent) {
      return _deadLetterUnsupportedAdd(partitionId, claimed, owner);
    }
    if (claimed.attemptCount >= config.maxAttempts ||
        claimed.attemptCount >= _maxDurableAttemptCount) {
      return _deadLetterAttemptsExhausted(partitionId, claimed, owner);
    }
    final now = config.clock().toUtc();
    if (!_live(claimed.absoluteExpiration, now)) {
      await _expireRecord(partitionId, claimed, owner);
      return const _ReplayOutcome();
    }
    if (now.difference(claimed.enqueuedAt) >= config.maxAge) {
      await _expireOrAgeOut(partitionId, recordId: claimed.recordId);
      return const _ReplayOutcome(deadLetter: true);
    }
    try {
      final Object? confirmedEntity = switch (claimed.intent) {
        OfflinePutVertexIntent(:final vertex) => await _putVertexRemote(
          vertex,
          cancellation,
        ),
        OfflinePutEdgeIntent(:final edge) => await _putEdgeRemote(
          edge,
          cancellation,
        ),
        OfflineAddEdgeIntent() => throw StateError(
          'legacy Add reached the remote replay switch',
        ),
      };
      var cacheRejected = false;
      final applied = await store.transaction((transaction) {
        final committedAt = config.clock().toUtc();
        final current = transaction.getOutbox(partitionId, claimed.recordId);
        if (current == null ||
            current.generation != claimed.generation ||
            current.state != OfflineOutboxState.sending ||
            current.leaseOwner != owner ||
            current.leaseUntil == null ||
            !committedAt.isBefore(current.leaseUntil!) ||
            transaction.generation(partitionId) != claimed.generation) {
          return false;
        }
        if (_live(claimed.absoluteExpiration, committedAt)) {
          try {
            transaction.putCache(
              partitionId,
              OfflineCacheRecord.value(
                partitionId: partitionId,
                generation: claimed.generation,
                key: claimed.intent.key,
                entity: confirmedEntity!,
                validatedAt: committedAt,
                lastAccessAt: committedAt,
              ),
            );
          } on OfflineCapacityException {
            // A confirmed remote operation must not be replayed solely because
            // its value cannot fit the bounded cache.
            cacheRejected = true;
          }
        }
        _updateOperationStatus(
          transaction,
          current,
          OfflineWriteState.confirmed,
          attemptCount: current.attemptCount + 1,
          now: committedAt,
        );
        transaction.deleteOutbox(partitionId, claimed.recordId);
        return true;
      });
      if (cacheRejected) {
        _recordDiagnostic(
          const OfflineDiagnosticEvent(
            kind: OfflineDiagnosticKind.capacityRejected,
          ),
        );
      }
      if (applied) {
        _emit(
          claimed,
          OfflineWriteState.confirmed,
          attemptCount: claimed.attemptCount + 1,
        );
      } else {
        _recordDiagnostic(
          OfflineDiagnosticEvent(
            kind: OfflineDiagnosticKind.staleOutcomeRejected,
            category: claimed.intent.category,
            attempt: claimed.attemptCount + 1,
          ),
        );
      }
      return _ReplayOutcome(confirmed: applied);
    } on OfflineRemoteFailure catch (failure) {
      return _recordReplayFailure(partitionId, claimed, owner, failure);
    } on OfflineCanceledException {
      await _releaseClaim(partitionId, claimed, owner);
      rethrow;
    }
  }

  Future<_ReplayOutcome> _deadLetterUnsupportedAdd(
    String partitionId,
    OfflineOutboxRecord claimed,
    String owner,
  ) async {
    final applied = await store.transaction((transaction) {
      final sampledAt = config.clock().toUtc();
      final current = transaction.getOutbox(partitionId, claimed.recordId);
      if (current == null ||
          current.generation != claimed.generation ||
          current.state != OfflineOutboxState.sending ||
          current.leaseOwner != owner ||
          current.leaseUntil == null ||
          !sampledAt.isBefore(current.leaseUntil!) ||
          transaction.generation(partitionId) != claimed.generation) {
        return false;
      }
      final transitionAt = _transitionTime(transaction, current, sampledAt);
      transaction.updateOutbox(
        current.copyWith(
          state: OfflineOutboxState.deadLetter,
          clearNextAttemptAt: true,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          deadLetteredAt: transitionAt,
          diagnosticCode: 'unsupported_add',
        ),
      );
      _updateOperationStatus(
        transaction,
        current,
        OfflineWriteState.deadLetter,
        attemptCount: current.attemptCount,
        diagnosticCode: 'unsupported_add',
        now: transitionAt,
      );
      return true;
    });
    if (applied) {
      _emit(
        claimed,
        OfflineWriteState.deadLetter,
        attemptCount: claimed.attemptCount,
        diagnosticCode: 'unsupported_add',
      );
    }
    return _ReplayOutcome(deadLetter: applied);
  }

  Future<_ReplayOutcome> _deadLetterAttemptsExhausted(
    String partitionId,
    OfflineOutboxRecord claimed,
    String owner,
  ) async {
    final applied = await store.transaction((transaction) {
      final sampledAt = config.clock().toUtc();
      final current = transaction.getOutbox(partitionId, claimed.recordId);
      if (current == null ||
          current.generation != claimed.generation ||
          current.state != OfflineOutboxState.sending ||
          current.leaseOwner != owner ||
          current.leaseUntil == null ||
          !sampledAt.isBefore(current.leaseUntil!) ||
          transaction.generation(partitionId) != claimed.generation) {
        return false;
      }
      final transitionAt = _transitionTime(transaction, current, sampledAt);
      transaction.updateOutbox(
        current.copyWith(
          state: OfflineOutboxState.deadLetter,
          clearNextAttemptAt: true,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          deadLetteredAt: transitionAt,
          diagnosticCode: 'max_attempts',
        ),
      );
      _updateOperationStatus(
        transaction,
        current,
        OfflineWriteState.deadLetter,
        attemptCount: current.attemptCount,
        diagnosticCode: 'max_attempts',
        now: transitionAt,
      );
      return true;
    });
    if (applied) {
      _emit(
        claimed,
        OfflineWriteState.deadLetter,
        attemptCount: claimed.attemptCount,
        diagnosticCode: 'max_attempts',
      );
    }
    return _ReplayOutcome(deadLetter: applied);
  }

  Future<Object?> _putVertexRemote(
    Vertex vertex,
    LanternCancellationToken? cancellation,
  ) async {
    await remote.putVertex(vertex, cancellation: cancellation);
    return vertex;
  }

  Future<Object?> _putEdgeRemote(
    Edge edge,
    LanternCancellationToken? cancellation,
  ) async {
    await remote.putEdge(edge, cancellation: cancellation);
    return edge;
  }

  Future<_ReplayOutcome> _recordReplayFailure(
    String partitionId,
    OfflineOutboxRecord claimed,
    String owner,
    OfflineRemoteFailure failure,
  ) async {
    if (failure.kind == OfflineRemoteErrorKind.canceled) {
      await _releaseClaim(partitionId, claimed, owner);
      throw const OfflineCanceledException();
    }
    final outcome = await store.transaction((transaction) {
      final sampledAt = config.clock().toUtc();
      final current = transaction.getOutbox(partitionId, claimed.recordId);
      if (current == null ||
          current.generation != claimed.generation ||
          current.state != OfflineOutboxState.sending ||
          current.leaseOwner != owner ||
          current.leaseUntil == null ||
          !sampledAt.isBefore(current.leaseUntil!) ||
          transaction.generation(partitionId) != claimed.generation) {
        return const _ReplayOutcome();
      }
      final transitionAt = _transitionTime(transaction, current, sampledAt);
      if (failure.kind == OfflineRemoteErrorKind.unauthenticated) {
        transaction.setReplayPausedForAuth(partitionId, true);
        transaction.updateOutbox(
          current.copyWith(
            state: OfflineOutboxState.enqueued,
            clearLeaseOwner: true,
            clearLeaseUntil: true,
            diagnosticCode: _authPauseDiagnostic,
          ),
        );
        _updateOperationStatus(
          transaction,
          current,
          OfflineWriteState.pausedForAuth,
          attemptCount: current.attemptCount,
          diagnosticCode: _authPauseDiagnostic,
          now: transitionAt,
        );
        return const _ReplayOutcome(pausedForAuth: true);
      }
      final attempts = current.attemptCount + 1;
      final terminal =
          failure.kind == OfflineRemoteErrorKind.invalidArgument ||
          failure.kind == OfflineRemoteErrorKind.permanent ||
          attempts >= config.maxAttempts ||
          sampledAt.difference(current.enqueuedAt) >= config.maxAge;
      if (terminal) {
        transaction.updateOutbox(
          current.copyWith(
            state: OfflineOutboxState.deadLetter,
            attemptCount: attempts,
            clearNextAttemptAt: true,
            clearLeaseOwner: true,
            clearLeaseUntil: true,
            deadLetteredAt: transitionAt,
            diagnosticCode: _diagnosticCode(failure.kind),
          ),
        );
        _updateOperationStatus(
          transaction,
          current,
          OfflineWriteState.deadLetter,
          attemptCount: attempts,
          diagnosticCode: _diagnosticCode(failure.kind),
          now: transitionAt,
        );
        return const _ReplayOutcome(deadLetter: true);
      }
      transaction.updateOutbox(
        current.copyWith(
          state: OfflineOutboxState.enqueued,
          attemptCount: attempts,
          nextAttemptAt: _durableDeadline(transitionAt, _retryDelay(attempts)),
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          diagnosticCode: _diagnosticCode(failure.kind),
        ),
      );
      _updateOperationStatus(
        transaction,
        current,
        OfflineWriteState.retryScheduled,
        attemptCount: attempts,
        diagnosticCode: _diagnosticCode(failure.kind),
        now: transitionAt,
      );
      return const _ReplayOutcome(retryScheduled: true);
    });
    if (outcome.pausedForAuth) {
      _emit(
        claimed,
        OfflineWriteState.pausedForAuth,
        attemptCount: claimed.attemptCount,
        diagnosticCode: _authPauseDiagnostic,
      );
    } else if (outcome.deadLetter) {
      _emit(
        claimed,
        OfflineWriteState.deadLetter,
        attemptCount: claimed.attemptCount + 1,
        diagnosticCode: _diagnosticCode(failure.kind),
      );
    } else if (outcome.retryScheduled) {
      _emit(
        claimed,
        OfflineWriteState.retryScheduled,
        attemptCount: claimed.attemptCount + 1,
        diagnosticCode: _diagnosticCode(failure.kind),
      );
    }
    return outcome;
  }

  Future<bool> _isReplayPausedForAuth(String partitionId) => store.transaction(
    (transaction) => transaction.replayPausedForAuth(partitionId),
  );

  Future<void> _clearReplayAuthPause(String partitionId) async {
    final resumed = await store.transaction((transaction) {
      final now = config.clock().toUtc();
      transaction.setReplayPausedForAuth(partitionId, false);
      final records = transaction
          .outbox(partitionId)
          .where(
            (record) =>
                record.state == OfflineOutboxState.enqueued &&
                record.diagnosticCode == _authPauseDiagnostic,
          )
          .toList(growable: false);
      for (final record in records) {
        transaction.updateOutbox(record.copyWith(clearDiagnosticCode: true));
        _updateOperationStatus(
          transaction,
          record,
          OfflineWriteState.locallyCommitted,
          attemptCount: record.attemptCount,
          now: now,
        );
      }
      return records;
    });
    for (final record in resumed) {
      _emit(
        record,
        OfflineWriteState.locallyCommitted,
        attemptCount: record.attemptCount,
      );
    }
  }

  Future<void> _releaseClaim(
    String partitionId,
    OfflineOutboxRecord claimed,
    String owner,
  ) async {
    final released = await store.transaction((transaction) {
      final current = transaction.getOutbox(partitionId, claimed.recordId);
      if (current != null &&
          current.generation == claimed.generation &&
          current.state == OfflineOutboxState.sending &&
          current.leaseOwner == owner &&
          current.leaseUntil != null &&
          config.clock().toUtc().isBefore(current.leaseUntil!) &&
          transaction.generation(partitionId) == claimed.generation) {
        transaction.updateOutbox(
          current.copyWith(
            state: OfflineOutboxState.enqueued,
            clearLeaseOwner: true,
            clearLeaseUntil: true,
            diagnosticCode: 'canceled',
          ),
        );
        _updateOperationStatus(
          transaction,
          current,
          OfflineWriteState.locallyCommitted,
          attemptCount: current.attemptCount,
          diagnosticCode: 'canceled',
          now: config.clock().toUtc(),
        );
        return true;
      }
      return false;
    });
    if (released) {
      _emit(
        claimed,
        OfflineWriteState.locallyCommitted,
        attemptCount: claimed.attemptCount,
        diagnosticCode: 'canceled',
      );
    }
  }

  Future<void> _expireOrAgeOut(
    String partitionId, {
    String? operationId,
    String? recordId,
    OfflineEntityKey? entityKey,
  }) async {
    final terminal = await store.transaction((transaction) {
      final now = config.clock().toUtc();
      final statuses = <(OfflineOutboxRecord, OfflineWriteState)>[];
      final records = <OfflineOutboxRecord>[];
      if (recordId != null) {
        final record = transaction.getOutbox(partitionId, recordId);
        if (record != null) records.add(record);
      } else {
        records.addAll(
          transaction.dueOutbox(
            partitionId,
            operationId: operationId,
            key: entityKey,
            now: now,
            maxAge: config.maxAge,
            deadLetterRetention: config.deadLetterRetention,
            limit: config.maxSweepRecordsPerObservation,
          ),
        );
      }
      for (final record in records) {
        if (record.state == OfflineOutboxState.deadLetter &&
            now.difference(record.deadLetteredAt!) >=
                config.deadLetterRetention) {
          transaction.deleteOutbox(partitionId, record.recordId);
          continue;
        }
        if (record.state != OfflineOutboxState.enqueued &&
            record.state != OfflineOutboxState.sending) {
          continue;
        }
        if (!_live(record.absoluteExpiration, now)) {
          _updateOperationStatus(
            transaction,
            record,
            OfflineWriteState.expired,
            attemptCount: record.attemptCount,
            diagnosticCode: 'expired',
            now: now,
          );
          transaction.deleteOutbox(partitionId, record.recordId);
          statuses.add((record, OfflineWriteState.expired));
        } else if (now.difference(record.enqueuedAt) >= config.maxAge) {
          final transitionAt = _transitionTime(transaction, record, now);
          transaction.updateOutbox(
            record.copyWith(
              state: OfflineOutboxState.deadLetter,
              clearNextAttemptAt: true,
              clearLeaseOwner: true,
              clearLeaseUntil: true,
              deadLetteredAt: transitionAt,
              diagnosticCode: 'max_age',
            ),
          );
          _updateOperationStatus(
            transaction,
            record,
            OfflineWriteState.deadLetter,
            attemptCount: record.attemptCount,
            diagnosticCode: 'max_age',
            now: transitionAt,
          );
          statuses.add((record, OfflineWriteState.deadLetter));
        }
      }
      final operationRecords = <OfflineOperationRecord>[];
      if (operationId != null) {
        final operation = transaction.getOperation(partitionId, operationId);
        if (operation != null) operationRecords.add(operation);
      } else {
        operationRecords.addAll(
          transaction.dueOperations(
            partitionId,
            now: now,
            retention: config.operationRetention,
            limit: config.maxSweepRecordsPerObservation,
          ),
        );
      }
      for (final operation in operationRecords) {
        final terminalAt = operation.terminalAt;
        if (terminalAt != null &&
            !transaction.hasOutboxForOperation(
              partitionId,
              operation.operationId,
            ) &&
            now.difference(terminalAt) >= config.operationRetention) {
          transaction.deleteOperation(partitionId, operation.operationId);
        }
      }
      return statuses;
    });
    for (final (record, state) in terminal) {
      _emit(
        record,
        state,
        attemptCount: record.attemptCount,
        diagnosticCode: state == OfflineWriteState.deadLetter
            ? 'max_age'
            : 'expired',
      );
    }
  }

  Future<void> _expireRecord(
    String partitionId,
    OfflineOutboxRecord claimed,
    String owner,
  ) async {
    final expired = await store.transaction((transaction) {
      final current = transaction.getOutbox(partitionId, claimed.recordId);
      if (current == null ||
          current.generation != claimed.generation ||
          current.state != OfflineOutboxState.sending ||
          current.leaseOwner != owner ||
          transaction.generation(partitionId) != claimed.generation) {
        return false;
      }
      _updateOperationStatus(
        transaction,
        current,
        OfflineWriteState.expired,
        attemptCount: current.attemptCount,
        diagnosticCode: 'expired',
        now: config.clock().toUtc(),
      );
      transaction.deleteOutbox(partitionId, current.recordId);
      return true;
    });
    if (expired) {
      _emit(
        claimed,
        OfflineWriteState.expired,
        attemptCount: claimed.attemptCount,
        diagnosticCode: 'expired',
      );
    }
  }

  Future<_CacheStoreOutcome> _storeCache(
    String partitionId,
    OfflineCacheRecord record,
  ) async {
    try {
      final stored = await store.transaction((transaction) {
        if (transaction.generation(partitionId) != record.generation) {
          return false;
        }
        transaction.putCache(partitionId, record);
        return true;
      });
      if (!stored) {
        _recordDiagnostic(
          const OfflineDiagnosticEvent(
            kind: OfflineDiagnosticKind.staleOutcomeRejected,
          ),
        );
        return _CacheStoreOutcome.staleGeneration;
      }
      return _CacheStoreOutcome.stored;
    } on OfflineCapacityException {
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.capacityRejected,
        ),
      );
      return _CacheStoreOutcome.capacityRejected;
    }
  }

  Future<bool> _removeCache(
    String partitionId,
    OfflineEntityKey key, {
    required int generation,
  }) => store.transaction((transaction) {
    if (transaction.generation(partitionId) != generation) return false;
    transaction.deleteCache(partitionId, key);
    return true;
  });

  void _putInitialOperation(
    OfflineStoreTransaction transaction,
    List<OfflineOutboxRecord> records,
    DateTime now,
  ) {
    final items = records
        .map(
          (record) => OfflineWriteStatus(
            recordId: record.recordId,
            operationId: record.operationId,
            itemIndex: record.itemIndex,
            state: record.state == OfflineOutboxState.expired
                ? OfflineWriteState.expired
                : OfflineWriteState.locallyCommitted,
            attemptCount: record.attemptCount,
            diagnosticCode: record.diagnosticCode,
          ),
        )
        .toList(growable: false);
    final status = OfflineOperationStatus(
      operationId: records.first.operationId,
      items: items,
    );
    transaction.putOperation(
      OfflineOperationRecord(
        partitionId: records.first.partitionId,
        generation: records.first.generation,
        operationId: records.first.operationId,
        items: items,
        updatedAt: now,
        terminalAt: status.isTerminal ? now : null,
      ),
    );
  }

  void _updateOperationStatus(
    OfflineStoreTransaction transaction,
    OfflineOutboxRecord outbox,
    OfflineWriteState state, {
    required int attemptCount,
    required DateTime now,
    String? diagnosticCode,
  }) {
    final operation = transaction.getOperation(
      outbox.partitionId,
      outbox.operationId,
    );
    if (operation == null ||
        operation.generation != outbox.generation ||
        outbox.itemIndex >= operation.items.length ||
        operation.items[outbox.itemIndex].recordId != outbox.recordId) {
      throw const OfflineCodecException();
    }
    final items = operation.items.toList(growable: false);
    items[outbox.itemIndex] = OfflineWriteStatus(
      recordId: outbox.recordId,
      operationId: outbox.operationId,
      itemIndex: outbox.itemIndex,
      state: state,
      attemptCount: attemptCount,
      diagnosticCode: diagnosticCode,
    );
    final status = OfflineOperationStatus(
      operationId: outbox.operationId,
      items: items,
    );
    final transitionAt = _latestTime(<DateTime>[
      now,
      outbox.enqueuedAt,
      operation.updatedAt,
    ]);
    transaction.putOperation(
      OfflineOperationRecord(
        partitionId: operation.partitionId,
        generation: operation.generation,
        operationId: operation.operationId,
        items: items,
        updatedAt: transitionAt,
        terminalAt: status.isTerminal
            ? operation.terminalAt ?? transitionAt
            : null,
      ),
    );
  }

  DateTime _transitionTime(
    OfflineStoreTransaction transaction,
    OfflineOutboxRecord outbox,
    DateTime sampledAt,
  ) {
    final operation = transaction.getOperation(
      outbox.partitionId,
      outbox.operationId,
    );
    return _latestTime(<DateTime>[
      sampledAt,
      outbox.enqueuedAt,
      if (operation != null) operation.updatedAt,
    ]);
  }

  OfflineWriteHandle _handle(
    String partitionId,
    OfflineOutboxRecord record,
    OfflineWriteState initial,
  ) {
    final id = _WriteStatusKey(partitionId, record.recordId);
    final first = OfflineWriteStatus(
      recordId: record.recordId,
      operationId: record.operationId,
      itemIndex: record.itemIndex,
      state: initial,
      attemptCount: record.attemptCount,
      diagnosticCode: record.diagnosticCode,
    );
    final channel = _writeStatuses.putIfAbsent(
      id,
      () => _WriteStatusChannel(first),
    );
    return OfflineWriteHandle(
      recordId: record.recordId,
      operationId: record.operationId,
      itemIndex: record.itemIndex,
      statuses: channel.statuses,
    );
  }

  OfflineWriteHandle _terminalHandle(
    OfflineOutboxRecord record,
    OfflineWriteState state,
  ) {
    final status = OfflineWriteStatus(
      recordId: record.recordId,
      operationId: record.operationId,
      itemIndex: record.itemIndex,
      state: state,
      attemptCount: record.attemptCount,
      diagnosticCode: record.diagnosticCode,
    );
    return OfflineWriteHandle(
      recordId: record.recordId,
      operationId: record.operationId,
      itemIndex: record.itemIndex,
      statuses: Stream<OfflineWriteStatus>.multi((controller) {
        controller
          ..add(status)
          ..close();
      }, isBroadcast: true),
    );
  }

  void _emit(
    OfflineOutboxRecord record,
    OfflineWriteState state, {
    required int attemptCount,
    String? diagnosticCode,
  }) {
    final id = _WriteStatusKey(record.partitionId, record.recordId);
    final status = OfflineWriteStatus(
      recordId: record.recordId,
      operationId: record.operationId,
      itemIndex: record.itemIndex,
      state: state,
      attemptCount: attemptCount,
      diagnosticCode: diagnosticCode,
    );
    final channel = _writeStatuses[id];
    channel?.add(status);
    _recordDiagnostic(
      OfflineDiagnosticEvent(
        kind: OfflineDiagnosticKind.writeTransition,
        category: record.intent.category,
        state: state,
        attempt: attemptCount,
      ),
    );
    if (state == OfflineWriteState.confirmed ||
        state == OfflineWriteState.deadLetter ||
        state == OfflineWriteState.expired) {
      if (channel != null) {
        if (identical(_writeStatuses[id], channel)) {
          _writeStatuses.remove(id);
        }
        unawaited(channel.close());
      } else {
        _writeStatuses.remove(id);
      }
    }
  }

  Future<T> _singleFlight<T>(
    _ReadFlightKey key,
    LanternCancellationToken? cancellation,
    Future<T> Function(LanternCancellationToken cancellation) task,
  ) {
    if (_disposed) {
      return Future<T>.error(const OfflineDisposedException());
    }
    if (_partitionIsClosing(key.partitionId)) {
      return Future<T>.error(const OfflineCanceledException());
    }
    if (cancellation?.isCanceled ?? false) {
      return Future<T>.error(const OfflineCanceledException());
    }
    final existing = _singleFlights[key];
    if (existing != null) {
      if (existing.acceptingWaiters) {
        return existing.addWaiter<T>(cancellation);
      }
      return _waitForReadFlight<T>(key, existing, cancellation, task);
    }
    late final _ReadFlight flight;
    flight = _ReadFlight(
      task: (flightCancellation) async => task(flightCancellation),
      onSettled: () {
        if (identical(_singleFlights[key], flight)) {
          _singleFlights.remove(key);
        }
      },
    );
    _singleFlights[key] = flight;
    final waiter = flight.addWaiter<T>(cancellation);
    flight.start();
    return waiter;
  }

  Future<T> _waitForReadFlight<T>(
    _ReadFlightKey key,
    _ReadFlight previous,
    LanternCancellationToken? cancellation,
    Future<T> Function(LanternCancellationToken cancellation) task,
  ) {
    if (cancellation?.isCanceled ?? false) {
      return Future<T>.error(const OfflineCanceledException());
    }
    final waiter = _TypedReadFlightWaiter<T>();
    final partitionWaiters = _deferredReadWaiters.putIfAbsent(
      key.partitionId,
      () => <_ReadFlightWaiter>{},
    );
    partitionWaiters.add(waiter);
    waiter.removeCancellationListener = cancellation?.listen((_) {
      if (!_removeDeferredReadWaiter(key.partitionId, waiter)) return;
      waiter.removeCancellationRegistration();
      waiter.completeError(
        const OfflineCanceledException(),
        StackTrace.current,
      );
    });
    previous.settled.whenComplete(() {
      if (waiter.isCompleted) {
        _removeDeferredReadWaiter(key.partitionId, waiter);
        waiter.removeCancellationRegistration();
        return;
      }
      _removeDeferredReadWaiter(key.partitionId, waiter);
      waiter.removeCancellationRegistration();
      if (_disposed) {
        waiter.completeError(
          const OfflineDisposedException(),
          StackTrace.current,
        );
        return;
      }
      if (_partitionIsClosing(key.partitionId)) {
        waiter.completeError(
          const OfflineCanceledException(),
          StackTrace.current,
        );
        return;
      }
      _singleFlight(key, cancellation, task).then(
        waiter.complete,
        onError: (Object error, StackTrace stackTrace) {
          waiter.completeError(error, stackTrace);
        },
      );
    });
    return waiter.future;
  }

  bool _removeDeferredReadWaiter(String partitionId, _ReadFlightWaiter waiter) {
    final waiters = _deferredReadWaiters[partitionId];
    if (waiters == null || !waiters.remove(waiter)) return false;
    if (waiters.isEmpty) _deferredReadWaiters.remove(partitionId);
    return true;
  }

  List<_ReadFlightWaiter> _takeDeferredReadWaiters(String partitionId) =>
      _deferredReadWaiters.remove(partitionId)?.toList(growable: false) ??
      const <_ReadFlightWaiter>[];

  bool _fresh(DateTime validatedAt, DateTime now) =>
      now.difference(validatedAt) <= config.maxCacheAge;

  bool _usesLocalFirst(OfflineReadPolicy policy) =>
      policy == OfflineReadPolicy.cacheOnly ||
      policy == OfflineReadPolicy.cacheFirst;

  bool _eligible<T>(OfflineSnapshot<T> snapshot, {required bool allowStale}) =>
      snapshot.state == OfflineReadState.fresh ||
      snapshot.state == OfflineReadState.missing ||
      (allowStale && snapshot.state == OfflineReadState.stale) ||
      (snapshot.hasPendingWrites && snapshot.value != null);

  bool _operationStatusEquals(
    OfflineOperationStatus left,
    OfflineOperationStatus right,
  ) {
    if (left.operationId != right.operationId ||
        left.items.length != right.items.length) {
      return false;
    }
    for (var index = 0; index < left.items.length; index++) {
      final leftItem = left.items[index];
      final rightItem = right.items[index];
      if (leftItem.recordId != rightItem.recordId ||
          leftItem.itemIndex != rightItem.itemIndex ||
          leftItem.state != rightItem.state ||
          leftItem.attemptCount != rightItem.attemptCount ||
          leftItem.diagnosticCode != rightItem.diagnosticCode) {
        return false;
      }
    }
    return true;
  }

  OfflineSnapshot<T> _withoutIneligible<T>(OfflineSnapshot<T> snapshot) {
    if (_eligible(snapshot, allowStale: false)) return snapshot;
    return snapshot.state == OfflineReadState.expired
        ? snapshot
        : _unknown<T>();
  }

  OfflineSnapshot<T> _withSource<T>(
    OfflineSnapshot<T> snapshot,
    OfflineReadSource source,
  ) => OfflineSnapshot<T>(
    state: snapshot.state,
    source: source,
    value: snapshot.value,
    validatedAt: snapshot.validatedAt,
    expiredAt: snapshot.expiredAt,
    cause: snapshot.cause,
    hasPendingWrites: snapshot.hasPendingWrites,
  );

  OfflineSnapshot<T> _markPending<T>(OfflineSnapshot<T> snapshot) =>
      OfflineSnapshot<T>(
        state: snapshot.state,
        source: snapshot.source,
        value: snapshot.value,
        validatedAt: snapshot.validatedAt,
        expiredAt: snapshot.expiredAt,
        cause: snapshot.cause,
        hasPendingWrites: true,
      );

  OfflineSnapshot<T> _unknown<T>() =>
      OfflineSnapshot<T>(state: OfflineReadState.unknown, source: null);

  List<OfflineOutboxRecord> _livePending(
    List<OfflineOutboxRecord> records,
    DateTime now,
  ) => records
      .where(
        (record) =>
            (record.state == OfflineOutboxState.enqueued ||
                record.state == OfflineOutboxState.sending) &&
            record.intent is! OfflineAddEdgeIntent &&
            _live(record.absoluteExpiration, now) &&
            now.difference(record.enqueuedAt) < config.maxAge,
      )
      .toList(growable: false);

  bool _live(DateTime? expiration, DateTime now) =>
      expiration == null || now.isBefore(expiration);

  Duration _retryDelay(int completedAttempts) {
    var micros = config.baseRetryDelay.inMicroseconds;
    for (var index = 1; index < completedAttempts; index++) {
      if (micros >= config.maxRetryDelay.inMicroseconds ~/ 2) {
        micros = config.maxRetryDelay.inMicroseconds;
        break;
      }
      micros *= 2;
    }
    final ceiling = Duration(
      microseconds: micros > config.maxRetryDelay.inMicroseconds
          ? config.maxRetryDelay.inMicroseconds
          : micros,
    );
    final result = config.jitter(ceiling);
    if (result < Duration.zero || result > ceiling) {
      throw const OfflineArgumentException();
    }
    return result;
  }

  _LeaseRenewal _startLeaseRenewal(
    String partitionId,
    OfflineOutboxRecord record,
    String owner,
  ) {
    final renewal = _LeaseRenewal(
      partitionId: partitionId,
      interval: config.leaseRenewalInterval,
      renew: () async {
        try {
          final renewed = await store.transaction(
            (transaction) => transaction.renewLease(
              partitionId,
              record.recordId,
              owner: owner,
              generation: record.generation,
              now: config.clock().toUtc(),
              leaseDuration: config.leaseDuration,
            ),
          );
          _recordDiagnostic(
            OfflineDiagnosticEvent(
              kind: renewed
                  ? OfflineDiagnosticKind.leaseRenewed
                  : OfflineDiagnosticKind.staleOutcomeRejected,
              category: record.intent.category,
              attempt: record.attemptCount,
            ),
          );
          return renewed;
        } catch (_) {
          _recordDiagnostic(
            OfflineDiagnosticEvent(
              kind: OfflineDiagnosticKind.staleOutcomeRejected,
              category: record.intent.category,
              attempt: record.attemptCount,
            ),
          );
          return false;
        }
      },
    );
    _leaseRenewals.add(renewal);
    renewal.start();
    return renewal;
  }

  Future<T> _withReadPermit<T>(
    String partitionId,
    LanternCancellationToken? cancellation,
    Future<T> Function() task,
  ) async {
    late final _ReadPermit permit;
    try {
      permit = await _readLimiter.acquire(partitionId, cancellation);
    } on OfflineCapacityException {
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.capacityRejected,
        ),
      );
      rethrow;
    }
    try {
      _throwIfCanceled(cancellation);
      return await task();
    } finally {
      permit.release();
    }
  }

  void _registerWatcher(String partitionId) {
    final partitionCount = _activeWatchers[partitionId] ?? 0;
    final overGlobal = _activeWatcherCount >= config.maxWatchers;
    final overPartition = partitionCount >= config.maxWatchersPerPartition;
    final overPartitions =
        partitionCount == 0 &&
        _activeWatchers.length >= config.maxActiveWatcherPartitions;
    if (overGlobal || overPartition || overPartitions) {
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.capacityRejected,
        ),
      );
      throw const OfflineCapacityException();
    }
    _activeWatcherCount += 1;
    _activeWatchers[partitionId] = partitionCount + 1;
  }

  void _releaseWatcher(String partitionId) {
    final count = _activeWatchers[partitionId];
    if (count == null) return;
    _activeWatcherCount -= 1;
    if (count == 1) {
      _activeWatchers.remove(partitionId);
    } else {
      _activeWatchers[partitionId] = count - 1;
    }
  }

  void _recordReadDiagnostic<T>(OfflineSnapshot<T> snapshot) {
    final kind = switch (snapshot.state) {
      OfflineReadState.expired => OfflineDiagnosticKind.cacheExpired,
      OfflineReadState.unknown => OfflineDiagnosticKind.cacheMiss,
      _ =>
        snapshot.source == OfflineReadSource.cache
            ? OfflineDiagnosticKind.cacheHit
            : OfflineDiagnosticKind.cacheMiss,
    };
    _recordDiagnostic(OfflineDiagnosticEvent(kind: kind));
  }

  void _recordDiagnostic(OfflineDiagnosticEvent event) {
    try {
      config.diagnostics?.record(event);
    } catch (_) {
      // Diagnostics are observational and must never change repository state.
    }
  }

  _PartitionRuntime _partitionRuntime(String partitionId) {
    _ensurePartitionActive(partitionId);
    final existing = _partitionRuntimes[partitionId];
    if (existing != null) return existing;
    if (_partitionRuntimes.length >= config.maxActivePartitionRuntimes) {
      _recordDiagnostic(
        const OfflineDiagnosticEvent(
          kind: OfflineDiagnosticKind.capacityRejected,
        ),
      );
      throw const OfflineCapacityException();
    }
    late final _PartitionRuntime runtime;
    runtime = _PartitionRuntime(
      maxQueuedReplays: config.maxQueuedReplaysPerPartition,
      onIdle: () => _evictPartitionRuntime(partitionId, runtime),
    );
    _partitionRuntimes[partitionId] = runtime;
    return runtime;
  }

  void _evictPartitionRuntime(String partitionId, _PartitionRuntime runtime) {
    if (_partitionIsClosing(partitionId) ||
        !runtime.canEvict ||
        !identical(_partitionRuntimes[partitionId], runtime)) {
      return;
    }
    _partitionRuntimes.remove(partitionId);
  }

  Future<T> _runPartitionWork<T>(
    String partitionId,
    LanternCancellationToken? cancellation,
    Future<T> Function(LanternCancellationToken cancellation) task,
  ) => _partitionRuntime(partitionId).run(cancellation, task);

  void _ensureActive() {
    if (_disposed) throw const OfflineDisposedException();
  }

  void _ensurePartitionActive(String partitionId) {
    _ensureActive();
    if (_partitionIsClosing(partitionId)) {
      throw const OfflineCanceledException();
    }
  }

  bool _partitionIsClosing(String partitionId) =>
      _wipingPartitions.contains(partitionId) ||
      (_partitionRuntimes[partitionId]?.isClosing ?? false);
}

final class _ReadLimiter {
  _ReadLimiter({
    required this.maxActive,
    required this.maxActivePerPartition,
    required this.maxQueued,
    required this.maxQueuedPerPartition,
  });

  final int maxActive;
  final int maxActivePerPartition;
  final int maxQueued;
  final int maxQueuedPerPartition;
  final List<_ReadWaiter> _queued = <_ReadWaiter>[];
  final Map<String, int> _activeByPartition = <String, int>{};
  final Map<String, int> _queuedByPartition = <String, int>{};
  var _active = 0;
  var _disposed = false;

  Future<_ReadPermit> acquire(
    String partitionId,
    LanternCancellationToken? cancellation,
  ) {
    if (_disposed) {
      return Future<_ReadPermit>.error(const OfflineDisposedException());
    }
    if (cancellation?.isCanceled ?? false) {
      return Future<_ReadPermit>.error(const OfflineCanceledException());
    }
    if (_canStart(partitionId)) {
      return Future<_ReadPermit>.value(_start(partitionId));
    }
    final partitionQueued = _queuedByPartition[partitionId] ?? 0;
    if (_queued.length >= maxQueued ||
        partitionQueued >= maxQueuedPerPartition) {
      return Future<_ReadPermit>.error(const OfflineCapacityException());
    }
    final waiter = _ReadWaiter(
      partitionId: partitionId,
      cancellation: cancellation,
    );
    _queued.add(waiter);
    _queuedByPartition[partitionId] = partitionQueued + 1;
    if (cancellation != null) {
      waiter.removeCancellationListener = cancellation.listen((_) {
        _removeCanceled(waiter);
      });
    }
    _drain();
    return waiter.completer.future;
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    final queued = _queued.toList(growable: false);
    _queued.clear();
    _queuedByPartition.clear();
    for (final waiter in queued) {
      waiter.removeCancellationRegistration();
      waiter.completer.completeError(const OfflineDisposedException());
    }
  }

  bool _canStart(String partitionId) =>
      _active < maxActive &&
      (_activeByPartition[partitionId] ?? 0) < maxActivePerPartition;

  _ReadPermit _start(String partitionId) {
    _active += 1;
    _activeByPartition[partitionId] =
        (_activeByPartition[partitionId] ?? 0) + 1;
    return _ReadPermit(() {
      _active -= 1;
      final partitionActive = _activeByPartition[partitionId]!;
      if (partitionActive == 1) {
        _activeByPartition.remove(partitionId);
      } else {
        _activeByPartition[partitionId] = partitionActive - 1;
      }
      _drain();
    });
  }

  void _drain() {
    if (_disposed) return;
    while (_active < maxActive) {
      final index = _queued.indexWhere(
        (waiter) =>
            (waiter.cancellation?.isCanceled ?? false) ||
            _canStart(waiter.partitionId),
      );
      if (index < 0) return;
      final waiter = _queued.removeAt(index);
      _decrementQueued(waiter.partitionId);
      waiter.removeCancellationRegistration();
      if (waiter.cancellation?.isCanceled ?? false) {
        waiter.completer.completeError(const OfflineCanceledException());
        continue;
      }
      waiter.completer.complete(_start(waiter.partitionId));
    }
  }

  void _removeCanceled(_ReadWaiter waiter) {
    if (!(waiter.cancellation?.isCanceled ?? false)) return;
    final removed = _queued.remove(waiter);
    waiter.removeCancellationRegistration();
    if (!removed) return;
    _decrementQueued(waiter.partitionId);
    waiter.completer.completeError(const OfflineCanceledException());
    _drain();
  }

  void _decrementQueued(String partitionId) {
    final count = _queuedByPartition[partitionId]!;
    if (count == 1) {
      _queuedByPartition.remove(partitionId);
    } else {
      _queuedByPartition[partitionId] = count - 1;
    }
  }
}

final class _ReadWaiter {
  _ReadWaiter({required this.partitionId, required this.cancellation});

  final String partitionId;
  final LanternCancellationToken? cancellation;
  final Completer<_ReadPermit> completer = Completer<_ReadPermit>();
  void Function()? removeCancellationListener;

  void removeCancellationRegistration() {
    removeCancellationListener?.call();
    removeCancellationListener = null;
  }
}

final class _ReadPermit {
  _ReadPermit(this._onRelease);

  final void Function() _onRelease;
  var _released = false;

  void release() {
    if (_released) return;
    _released = true;
    _onRelease();
  }
}

final class _PartitionRuntime {
  _PartitionRuntime({required this.maxQueuedReplays, required this.onIdle});

  final int maxQueuedReplays;
  final void Function() onIdle;
  final LanternCancellationToken _cancellation = LanternCancellationToken();
  final Completer<void> _quiesced = Completer<void>();
  Future<void> _replayTail = Future<void>.value();
  var _active = 0;
  var _replayRequests = 0;
  var _closing = false;
  Object _closingError = const OfflineCanceledException();

  bool get isClosing => _closing;

  bool get canEvict => !_closing && _active == 0 && _replayRequests == 0;

  Future<T> run<T>(
    LanternCancellationToken? callerCancellation,
    Future<T> Function(LanternCancellationToken cancellation) task,
  ) {
    if (_closing) return Future<T>.error(_closingError);
    if (callerCancellation?.isCanceled ?? false) {
      _notifyIdle();
      return Future<T>.error(const OfflineCanceledException());
    }
    _active += 1;
    final owned = LanternCancellationToken();
    final removeOwned = _cancellation.listen(owned.cancel);
    final removeCaller = callerCancellation?.listen(owned.cancel);
    return Future<T>.sync(() {
      if (_closing) throw _closingError;
      _throwIfCanceled(owned);
      return task(owned);
    }).whenComplete(() {
      removeCaller?.call();
      removeOwned();
      _active -= 1;
      _completeQuiescedIfReady();
      _notifyIdle();
    });
  }

  Future<T> scheduleReplay<T>(
    LanternCancellationToken? callerCancellation,
    Future<T> Function(LanternCancellationToken cancellation) task,
  ) {
    if (_closing) return Future<T>.error(_closingError);
    if (callerCancellation?.isCanceled ?? false) {
      _notifyIdle();
      return Future<T>.error(const OfflineCanceledException());
    }
    if (_replayRequests >= 1 + maxQueuedReplays) {
      return Future<T>.error(const OfflineCapacityException());
    }
    _replayRequests += 1;
    final previous = _replayTail;
    final result = run<T>(callerCancellation, (ownedCancellation) async {
      await _awaitWithCancellation(previous, ownedCancellation);
      return task(ownedCancellation);
    });
    final settled = result.whenComplete(() {
      _replayRequests -= 1;
      _notifyIdle();
    });
    final resultTail = settled.then<void>(
      (_) {},
      onError: (Object _, StackTrace _) {},
    );
    _replayTail = Future.wait<void>(<Future<void>>[previous, resultTail]);
    return settled;
  }

  void beginQuiesce({Object error = const OfflineCanceledException()}) {
    if (_closing) return;
    _closing = true;
    _closingError = error;
    _cancellation.cancel(error);
    _completeQuiescedIfReady();
  }

  Future<void> waitQuiesced() {
    _completeQuiescedIfReady();
    return _quiesced.future;
  }

  void _completeQuiescedIfReady() {
    if (_closing && _active == 0 && !_quiesced.isCompleted) {
      _quiesced.complete();
    }
  }

  void _notifyIdle() {
    if (canEvict) onIdle();
  }
}

Future<void> _awaitWithCancellation(
  Future<void> future,
  LanternCancellationToken cancellation,
) async {
  _throwIfCanceled(cancellation);
  final canceled = Completer<void>();
  final removeCancellation = cancellation.listen((_) {
    if (!canceled.isCompleted) {
      canceled.completeError(const OfflineCanceledException());
    }
  });
  try {
    await Future.any<void>(<Future<void>>[future, canceled.future]);
    _throwIfCanceled(cancellation);
  } finally {
    removeCancellation();
  }
}

final class _ReplayLimiter {
  _ReplayLimiter({
    required this.maxActive,
    required this.maxActivePerPartition,
    required this.maxQueued,
    required this.maxQueuedPerPartition,
  });

  final int maxActive;
  final int maxActivePerPartition;
  final int maxQueued;
  final int maxQueuedPerPartition;
  final List<_ReplayWaiter> _queued = <_ReplayWaiter>[];
  final Map<String, int> _activeByPartition = <String, int>{};
  final Map<String, int> _queuedByPartition = <String, int>{};
  var _active = 0;
  var _disposed = false;

  Future<_ReplayPermit> acquire(
    String partitionId,
    LanternCancellationToken? cancellation,
  ) {
    if (_disposed) {
      return Future<_ReplayPermit>.error(const OfflineDisposedException());
    }
    if (cancellation?.isCanceled ?? false) {
      return Future<_ReplayPermit>.error(const OfflineCanceledException());
    }
    if (_canStart(partitionId)) {
      return Future<_ReplayPermit>.value(_start(partitionId));
    }
    final partitionQueued = _queuedByPartition[partitionId] ?? 0;
    if (_queued.length >= maxQueued ||
        partitionQueued >= maxQueuedPerPartition) {
      return Future<_ReplayPermit>.error(const OfflineCapacityException());
    }
    final waiter = _ReplayWaiter(
      partitionId: partitionId,
      cancellation: cancellation,
    );
    _queued.add(waiter);
    _queuedByPartition[partitionId] = partitionQueued + 1;
    waiter.removeCancellationListener = cancellation?.listen((_) {
      _removeCanceled(waiter);
    });
    return waiter.completer.future;
  }

  void dispose() {
    if (_disposed) return;
    _disposed = true;
    final queued = _queued.toList(growable: false);
    _queued.clear();
    _queuedByPartition.clear();
    for (final waiter in queued) {
      waiter.removeCancellationRegistration();
      waiter.completer.completeError(const OfflineDisposedException());
    }
  }

  bool _canStart(String partitionId) =>
      _active < maxActive &&
      (_activeByPartition[partitionId] ?? 0) < maxActivePerPartition;

  _ReplayPermit _start(String partitionId) {
    _active += 1;
    _activeByPartition[partitionId] =
        (_activeByPartition[partitionId] ?? 0) + 1;
    return _ReplayPermit(() {
      _active -= 1;
      final partitionActive = _activeByPartition[partitionId]!;
      if (partitionActive == 1) {
        _activeByPartition.remove(partitionId);
      } else {
        _activeByPartition[partitionId] = partitionActive - 1;
      }
      _drain();
    });
  }

  void _drain() {
    if (_disposed) return;
    while (_active < maxActive) {
      final index = _queued.indexWhere(
        (waiter) =>
            (waiter.cancellation?.isCanceled ?? false) ||
            _canStart(waiter.partitionId),
      );
      if (index < 0) return;
      final waiter = _queued.removeAt(index);
      _decrementQueued(waiter.partitionId);
      waiter.removeCancellationRegistration();
      if (waiter.cancellation?.isCanceled ?? false) {
        waiter.completer.completeError(const OfflineCanceledException());
      } else {
        waiter.completer.complete(_start(waiter.partitionId));
      }
    }
  }

  void _removeCanceled(_ReplayWaiter waiter) {
    if (!(waiter.cancellation?.isCanceled ?? false)) return;
    final removed = _queued.remove(waiter);
    waiter.removeCancellationRegistration();
    if (!removed) return;
    _decrementQueued(waiter.partitionId);
    waiter.completer.completeError(const OfflineCanceledException());
    _drain();
  }

  void _decrementQueued(String partitionId) {
    final count = _queuedByPartition[partitionId]!;
    if (count == 1) {
      _queuedByPartition.remove(partitionId);
    } else {
      _queuedByPartition[partitionId] = count - 1;
    }
  }
}

final class _ReplayWaiter {
  _ReplayWaiter({required this.partitionId, required this.cancellation});

  final String partitionId;
  final LanternCancellationToken? cancellation;
  final Completer<_ReplayPermit> completer = Completer<_ReplayPermit>();
  void Function()? removeCancellationListener;

  void removeCancellationRegistration() {
    removeCancellationListener?.call();
    removeCancellationListener = null;
  }
}

final class _ReplayPermit {
  _ReplayPermit(this._onRelease);

  final void Function() _onRelease;
  var _released = false;

  void release() {
    if (_released) return;
    _released = true;
    _onRelease();
  }
}

final class _LeaseRenewal {
  _LeaseRenewal({
    required this.partitionId,
    required this.interval,
    required this.renew,
  });

  final String partitionId;
  final Duration interval;
  final Future<bool> Function() renew;
  Timer? _timer;
  Future<void>? _inFlight;
  var _stopped = false;

  void start() => _schedule();

  Future<void> stop() async {
    if (_stopped) {
      await _inFlight;
      return;
    }
    _stopped = true;
    _timer?.cancel();
    await _inFlight;
  }

  void _schedule() {
    if (_stopped) return;
    _timer = Timer(interval, () {
      if (_stopped) return;
      _inFlight = _tick();
    });
  }

  Future<void> _tick() async {
    final renewed = await renew();
    if (!renewed) {
      _stopped = true;
      return;
    }
    _schedule();
  }
}

enum _CacheStoreOutcome { stored, staleGeneration, capacityRejected }

enum _ClaimSendState { sendable, pausedForAuth, stale }

const _authPauseDiagnostic = 'unauthenticated';

final class _ReplayOutcome {
  const _ReplayOutcome({
    this.confirmed = false,
    this.pausedForAuth = false,
    this.retryScheduled = false,
    this.deadLetter = false,
  });

  final bool confirmed;
  final bool pausedForAuth;
  final bool retryScheduled;
  final bool deadLetter;
}

DateTime? _resolveExpiration(
  Duration? expiresIn,
  DateTime? expiresAt,
  DateTime now,
) {
  if (expiresIn != null && expiresAt != null) {
    throw const OfflineArgumentException();
  }
  if (expiresIn != null) {
    if (expiresIn <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    try {
      final resolved = now.add(expiresIn).toUtc();
      if (!_durableTimestamp(resolved)) {
        throw const OfflineArgumentException();
      }
      return resolved;
    } catch (_) {
      throw const OfflineArgumentException();
    }
  }
  if (expiresAt == null) return null;
  final resolved = expiresAt.toUtc();
  if (!_durableTimestamp(resolved)) {
    throw const OfflineArgumentException();
  }
  return resolved;
}

String _diagnosticCode(OfflineRemoteErrorKind kind) => switch (kind) {
  OfflineRemoteErrorKind.unavailable => 'unavailable',
  OfflineRemoteErrorKind.unauthenticated => 'unauthenticated',
  OfflineRemoteErrorKind.invalidArgument => 'invalid_argument',
  OfflineRemoteErrorKind.canceled => 'canceled',
  OfflineRemoteErrorKind.resourceExhausted => 'resource_exhausted',
  OfflineRemoteErrorKind.permanent => 'permanent',
  OfflineRemoteErrorKind.unknown => 'unknown',
};

const int _maxDurableAttemptCount = 0x7fffffffffffffff;

bool _durableTimestamp(DateTime value) =>
    value.isUtc && value.year >= 1 && value.year <= 9999;

final DateTime _maximumDurableTimestamp = DateTime.utc(
  9999,
  12,
  31,
  23,
  59,
  59,
  999,
  999,
);

DateTime _durableDeadline(DateTime base, Duration delay) {
  try {
    final candidate = base.add(delay);
    return candidate.isAfter(_maximumDurableTimestamp)
        ? _maximumDurableTimestamp
        : candidate;
  } on Object {
    return _maximumDurableTimestamp;
  }
}

DateTime _latestTime(Iterable<DateTime> values) =>
    values.reduce((latest, value) => value.isAfter(latest) ? value : latest);

void _validatePartition(String partitionId) {
  if (partitionId.isEmpty) throw const OfflineArgumentException();
}

void _validatePartitionAndKey(String partitionId, String key) {
  _validatePartition(partitionId);
  if (key.isEmpty) throw const OfflineArgumentException();
}

void _validateId(String value) {
  if (value.isEmpty) throw const OfflineArgumentException();
}

void _throwIfCanceled(LanternCancellationToken? cancellation) {
  if (cancellation?.isCanceled ?? false) throw const OfflineCanceledException();
}

final class _WriteStatusKey {
  const _WriteStatusKey(this.partitionId, this.recordId);

  final String partitionId;
  final String recordId;

  @override
  bool operator ==(Object other) =>
      other is _WriteStatusKey &&
      partitionId == other.partitionId &&
      recordId == other.recordId;

  @override
  int get hashCode => Object.hash(partitionId, recordId);
}

final class _WriteStatusChannel {
  _WriteStatusChannel(this._latest);

  OfflineWriteStatus _latest;
  final StreamController<OfflineWriteStatus> _controller =
      StreamController<OfflineWriteStatus>.broadcast();

  Stream<OfflineWriteStatus> get statuses =>
      Stream<OfflineWriteStatus>.multi((controller) {
        controller.add(_latest);
        final subscription = _controller.stream.listen(
          controller.add,
          onError: controller.addError,
          onDone: controller.close,
        );
        controller.onCancel = subscription.cancel;
      }, isBroadcast: true);

  void add(OfflineWriteStatus status) {
    _latest = status;
    if (!_controller.isClosed) _controller.add(status);
  }

  Future<void> close() =>
      _controller.isClosed ? Future<void>.value() : _controller.close();
}

final class _ReadFlight {
  _ReadFlight({required this.task, required this.onSettled});

  final Future<Object?> Function(LanternCancellationToken cancellation) task;
  final void Function() onSettled;
  final LanternCancellationToken _cancellation = LanternCancellationToken();
  final Set<_ReadFlightWaiter> _waiters = <_ReadFlightWaiter>{};
  final Completer<void> _settled = Completer<void>();
  var _started = false;
  var _acceptingWaiters = true;
  var _isSettled = false;

  bool get acceptingWaiters => _acceptingWaiters;

  Future<void> get settled => _settled.future;

  Future<T> addWaiter<T>(LanternCancellationToken? cancellation) {
    if (!_acceptingWaiters) {
      return Future<T>.error(const OfflineCanceledException());
    }
    if (cancellation?.isCanceled ?? false) {
      return Future<T>.error(const OfflineCanceledException());
    }
    final waiter = _TypedReadFlightWaiter<T>();
    _waiters.add(waiter);
    waiter.removeCancellationListener = cancellation?.listen((_) {
      _cancelWaiter(waiter);
    });
    return waiter.future;
  }

  void start() {
    if (_started) return;
    _started = true;
    unawaited(_run());
  }

  void cancel(Object error) {
    if (_isSettled) return;
    _acceptingWaiters = false;
    _cancellation.cancel(error);
    final waiters = _waiters.toList(growable: false);
    _waiters.clear();
    for (final waiter in waiters) {
      waiter.removeCancellationRegistration();
      waiter.completeError(error, StackTrace.current);
    }
  }

  void _cancelWaiter(_ReadFlightWaiter waiter) {
    if (!_waiters.remove(waiter)) return;
    waiter.removeCancellationRegistration();
    waiter.completeError(const OfflineCanceledException(), StackTrace.current);
    if (_waiters.isEmpty && !_isSettled) {
      _acceptingWaiters = false;
      _cancellation.cancel();
    }
  }

  Future<void> _run() async {
    try {
      final value = await task(_cancellation);
      final waiters = _waiters.toList(growable: false);
      _waiters.clear();
      for (final waiter in waiters) {
        waiter.removeCancellationRegistration();
        waiter.complete(value);
      }
    } catch (error, stackTrace) {
      final waiters = _waiters.toList(growable: false);
      _waiters.clear();
      for (final waiter in waiters) {
        waiter.removeCancellationRegistration();
        waiter.completeError(error, stackTrace);
      }
    } finally {
      _acceptingWaiters = false;
      _isSettled = true;
      onSettled();
      _settled.complete();
    }
  }
}

abstract interface class _ReadFlightWaiter {
  bool get isCompleted;

  Future<void> get settled;

  set removeCancellationListener(void Function()? value);

  void removeCancellationRegistration();

  void complete(Object? value);

  void completeError(Object error, StackTrace stackTrace);
}

final class _TypedReadFlightWaiter<T> implements _ReadFlightWaiter {
  final Completer<T> _completer = Completer<T>();
  void Function()? _removeCancellationListener;

  Future<T> get future => _completer.future;

  @override
  bool get isCompleted => _completer.isCompleted;

  @override
  Future<void> get settled => _completer.future.then<void>(
    (_) {},
    onError: (Object _, StackTrace _) {},
  );

  @override
  set removeCancellationListener(void Function()? value) {
    _removeCancellationListener = value;
  }

  @override
  void removeCancellationRegistration() {
    _removeCancellationListener?.call();
    _removeCancellationListener = null;
  }

  @override
  void complete(Object? value) {
    if (!_completer.isCompleted) _completer.complete(value as T);
  }

  @override
  void completeError(Object error, StackTrace stackTrace) {
    if (!_completer.isCompleted) {
      _completer.completeError(error, stackTrace);
    }
  }
}

final class _ReadFlightKey {
  const _ReadFlightKey(
    this.partitionId,
    this.key,
    this.policy,
    this.allowStale,
  );

  final String partitionId;
  final OfflineEntityKey key;
  final OfflineReadPolicy policy;
  final bool allowStale;

  @override
  bool operator ==(Object other) =>
      other is _ReadFlightKey &&
      partitionId == other.partitionId &&
      key == other.key &&
      policy == other.policy &&
      allowStale == other.allowStale;

  @override
  int get hashCode => Object.hash(partitionId, key, policy, allowStale);
}

bool _vertexSnapshotEquals(
  OfflineSnapshot<Vertex> left,
  OfflineSnapshot<Vertex> right,
) =>
    _snapshotMetadataEquals(left, right) &&
    _vertexEquals(left.value, right.value);

bool _edgeSnapshotEquals(
  OfflineSnapshot<Edge> left,
  OfflineSnapshot<Edge> right,
) =>
    _snapshotMetadataEquals(left, right) &&
    _edgeEquals(left.value, right.value);

bool _snapshotMetadataEquals<T>(
  OfflineSnapshot<T> left,
  OfflineSnapshot<T> right,
) =>
    left.state == right.state &&
    left.source == right.source &&
    left.validatedAt == right.validatedAt &&
    left.expiredAt == right.expiredAt &&
    identical(left.cause, right.cause) &&
    left.hasPendingWrites == right.hasPendingWrites;

bool _vertexEquals(Vertex? left, Vertex? right) {
  if (identical(left, right)) return true;
  if (left == null || right == null) return false;
  return left.key == right.key &&
      left.expiration == right.expiration &&
      _valueEquals(left.value, right.value);
}

bool _edgeEquals(Edge? left, Edge? right) {
  if (identical(left, right)) return true;
  if (left == null || right == null) return false;
  return left.tail == right.tail &&
      left.head == right.head &&
      left.expiration == right.expiration &&
      _floatBitsEqual(left.weight, right.weight, 4);
}

bool _valueEquals(VertexValue left, VertexValue right) => switch ((
  left,
  right,
)) {
  (Float64Value(value: final a), Float64Value(value: final b)) =>
    _floatBitsEqual(a, b, 8),
  (Float32Value(value: final a), Float32Value(value: final b)) =>
    _floatBitsEqual(a, b, 4),
  (Int32Value(value: final a), Int32Value(value: final b)) => a == b,
  (Int64Value(value: final a), Int64Value(value: final b)) => a == b,
  (Uint32Value(value: final a), Uint32Value(value: final b)) => a == b,
  (Uint64Value(value: final a), Uint64Value(value: final b)) => a == b,
  (BoolValue(value: final a), BoolValue(value: final b)) => a == b,
  (StringValue(value: final a), StringValue(value: final b)) => a == b,
  (BytesValue(value: final a), BytesValue(value: final b)) => _bytesEqual(a, b),
  (TimestampValue(value: final a), TimestampValue(value: final b)) => a == b,
  (DurationValue(value: final a), DurationValue(value: final b)) => a == b,
  (NilValue(), NilValue()) || (UnsetValue(), UnsetValue()) => true,
  _ => false,
};

bool _bytesEqual(List<int> left, List<int> right) {
  if (left.length != right.length) return false;
  for (var index = 0; index < left.length; index++) {
    if (left[index] != right[index]) return false;
  }
  return true;
}

bool _floatBitsEqual(double left, double right, int bytes) {
  final leftBits = ByteData(bytes);
  final rightBits = ByteData(bytes);
  if (bytes == 4) {
    leftBits.setFloat32(0, left, Endian.big);
    rightBits.setFloat32(0, right, Endian.big);
  } else {
    leftBits.setFloat64(0, left, Endian.big);
    rightBits.setFloat64(0, right, Endian.big);
  }
  for (var index = 0; index < bytes; index++) {
    if (leftBits.getUint8(index) != rightBits.getUint8(index)) return false;
  }
  return true;
}
