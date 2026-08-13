import 'dart:async';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'store.dart';
import 'types.dart';

/// Creates one empty store instance for the reusable adapter conformance suite.
typedef OfflineStoreFactory = FutureOr<OfflineStore> Function();

/// Closes and reopens [store] through the adapter's real durable boundary.
///
/// The returned store must use the same configured limits and contain exactly
/// the state committed by [store]. The reference adapter implements this with
/// its canonical snapshot; a production adapter must close and reopen its own
/// database.
typedef OfflineStoreReopener =
    FutureOr<OfflineStore> Function(OfflineStore store);

/// Runs the storage-neutral transaction, ordering, lease, and wipe contract.
///
/// Adapter packages should invoke this function from their own test runner. It
/// throws [StateError] with a content-free contract label on any mismatch.
Future<void> runStoreConformanceSuite(
  OfflineStoreFactory factory, {
  required OfflineStoreReopener reopen,
  int maxCapacityProbeRecords = 4096,
  int maxNotificationControllerProbe = 1024,
}) async {
  if (maxCapacityProbeRecords < 2 || maxNotificationControllerProbe < 2) {
    throw const OfflineArgumentException();
  }
  var store = await factory();
  final now = DateTime.utc(2026, 1, 1);
  const partitionId = 'conformance';
  const operationId = 'operation';
  OfflineStoreTransaction? escaped;
  final vertex = Vertex(
    key: 'key',
    value: VertexValue.string('value'),
    expiration: null,
  );

  try {
    await store.transaction<void>((transaction) {
      escaped = transaction;
      transaction.putCache(
        partitionId,
        OfflineCacheRecord.value(
          partitionId: partitionId,
          generation: 0,
          key: const OfflineEntityKey.vertex('key'),
          entity: vertex,
          validatedAt: now,
          lastAccessAt: now,
        ),
      );
      throw const _ConformanceRollback();
    });
  } on _ConformanceRollback {
    // Expected: the transaction must expose no partial state.
  }
  final rolledBack = await store.transaction(
    (transaction) =>
        transaction.getCache(
          partitionId,
          const OfflineEntityKey.vertex('key'),
        ) ==
        null,
  );
  _require(rolledBack, 'transaction_rollback');
  try {
    escaped!.generation(partitionId);
    _require(false, 'sealed_transaction');
  } on OfflineTransactionClosedException {
    // Required: an escaped transaction cannot mutate or observe later state.
  }

  await _requireInvalidCommit(
    store,
    partitionId,
    (transaction) => transaction.enqueue(
      _record(
        recordId: 'bare-record',
        operationId: 'bare-operation',
        itemIndex: 0,
        key: 'bare',
        now: now,
      ),
    ),
    'bare_enqueue_graph',
    (transaction) => transaction.getOutbox(partitionId, 'bare-record') == null,
  );

  await _requireInvalidCommit(
    store,
    partitionId,
    (transaction) => transaction.putCache(
      partitionId,
      OfflineCacheRecord.value(
        partitionId: partitionId,
        generation: 0,
        key: const OfflineEntityKey.vertex('expected'),
        entity: Vertex(
          key: 'different',
          value: VertexValue.nil(),
          expiration: null,
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    ),
    'cache_vertex_identity_graph',
    (transaction) =>
        transaction.getCache(
          partitionId,
          const OfflineEntityKey.vertex('expected'),
        ) ==
        null,
  );
  await _requireInvalidCommit(
    store,
    partitionId,
    (transaction) => transaction.putCache(
      partitionId,
      OfflineCacheRecord.value(
        partitionId: partitionId,
        generation: 0,
        key: const OfflineEntityKey.edge('tail', 'head'),
        entity: Edge(
          tail: 'tail',
          head: 'different',
          weight: 1,
          expiration: null,
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    ),
    'cache_edge_identity_graph',
    (transaction) =>
        transaction.getCache(
          partitionId,
          const OfflineEntityKey.edge('tail', 'head'),
        ) ==
        null,
  );
  await _requireInvalidCommit(
    store,
    partitionId,
    (transaction) => transaction.putCache(
      partitionId,
      OfflineCacheRecord.missing(
        partitionId: partitionId,
        generation: 0,
        key: const OfflineEntityKey.vertex('missing-order'),
        validatedAt: now,
        lastAccessAt: now,
        missingUntil: now.subtract(const Duration(microseconds: 1)),
      ),
    ),
    'cache_missing_order_graph',
    (transaction) =>
        transaction.getCache(
          partitionId,
          const OfflineEntityKey.vertex('missing-order'),
        ) ==
        null,
  );

  Future<void> requireInvalidExpiredAggregate({
    required String suffix,
    required int statusAttemptCount,
    String? recordDiagnostic,
    String? statusDiagnostic,
    required DateTime updatedAt,
  }) async {
    final recordId = 'expired-$suffix-record';
    final expiredOperationId = 'expired-$suffix-operation';
    await _requireInvalidCommit(
      store,
      partitionId,
      (transaction) {
        final assigned = transaction.enqueue(
          _expiredRecord(
            recordId: recordId,
            operationId: expiredOperationId,
            now: now,
            attemptCount: 2,
            diagnosticCode: recordDiagnostic,
          ),
        );
        transaction.putOperation(
          OfflineOperationRecord(
            partitionId: partitionId,
            generation: 0,
            operationId: expiredOperationId,
            items: <OfflineWriteStatus>[
              OfflineWriteStatus(
                recordId: assigned.recordId,
                operationId: assigned.operationId,
                itemIndex: assigned.itemIndex,
                state: OfflineWriteState.expired,
                attemptCount: statusAttemptCount,
                diagnosticCode: statusDiagnostic,
              ),
            ],
            updatedAt: updatedAt,
            terminalAt: updatedAt,
          ),
        );
      },
      'expired_${suffix}_graph',
      (transaction) =>
          transaction.getOutbox(partitionId, recordId) == null &&
          transaction.getOperation(partitionId, expiredOperationId) == null,
    );
  }

  await requireInvalidExpiredAggregate(
    suffix: 'attempt_mismatch',
    statusAttemptCount: 1,
    updatedAt: now,
  );
  await requireInvalidExpiredAggregate(
    suffix: 'diagnostic_mismatch',
    statusAttemptCount: 2,
    recordDiagnostic: 'expired_input',
    statusDiagnostic: 'different',
    updatedAt: now,
  );
  await requireInvalidExpiredAggregate(
    suffix: 'updated_before_enqueue',
    statusAttemptCount: 2,
    updatedAt: now.subtract(const Duration(microseconds: 1)),
  );

  final assigned = await store.transaction((transaction) {
    final records = transaction.enqueueAll(<OfflineOutboxRecord>[
      _record(
        recordId: 'record-0',
        operationId: operationId,
        itemIndex: 0,
        key: 'a',
        now: now,
      ),
      _record(
        recordId: 'record-1',
        operationId: operationId,
        itemIndex: 1,
        key: 'b',
        now: now,
      ),
    ]);
    transaction.putOperation(
      OfflineOperationRecord(
        partitionId: partitionId,
        generation: 0,
        operationId: operationId,
        items: records
            .map(
              (record) => OfflineWriteStatus(
                recordId: record.recordId,
                operationId: record.operationId,
                itemIndex: record.itemIndex,
                state: OfflineWriteState.locallyCommitted,
                attemptCount: 0,
              ),
            )
            .toList(growable: false),
        updatedAt: now,
      ),
    );
    return records;
  });
  _require(
    assigned.length == 2 &&
        assigned.first.ordinal > 0 &&
        assigned.first.ordinal == assigned.last.ordinal,
    'atomic_plural_ordinal',
  );
  final firstPage = await store.transaction(
    (transaction) => transaction.scanOutbox(partitionId, limit: 1),
  );
  _require(
    firstPage.records.length == 1 &&
        firstPage.records.single.recordId == assigned.first.recordId &&
        firstPage.nextCursor != null &&
        firstPage.hasMore,
    'bounded_outbox_scan_first_page',
  );
  final secondPage = await store.transaction(
    (transaction) => transaction.scanOutbox(
      partitionId,
      after: firstPage.nextCursor,
      limit: 1,
    ),
  );
  _require(
    secondPage.records.length == 1 &&
        secondPage.records.single.recordId == assigned.last.recordId &&
        !secondPage.hasMore,
    'bounded_outbox_scan_cursor',
  );
  final scopedPage = await store.transaction(
    (transaction) => transaction.scanOutbox(
      partitionId,
      key: const OfflineEntityKey.vertex('b'),
      limit: 1,
    ),
  );
  _require(
    scopedPage.records.single.recordId == assigned.last.recordId,
    'bounded_outbox_scan_scope',
  );
  final operationPage = await store.transaction(
    (transaction) => transaction.scanOperations(partitionId, limit: 1),
  );
  _require(
    operationPage.operations.single.operationId == operationId &&
        !operationPage.hasMore,
    'bounded_operation_scan',
  );
  final duePage = await store.transaction(
    (transaction) => transaction.dueOutbox(
      partitionId,
      now: now.add(const Duration(hours: 1)),
      maxAge: const Duration(hours: 1),
      deadLetterRetention: const Duration(hours: 1),
      limit: 1,
    ),
  );
  _require(duePage.length == 1, 'bounded_due_outbox');
  final scopedDue = await store.transaction(
    (transaction) => transaction.dueOutbox(
      partitionId,
      key: const OfflineEntityKey.vertex('b'),
      now: now.add(const Duration(hours: 1)),
      maxAge: const Duration(hours: 1),
      deadLetterRetention: const Duration(hours: 1),
      limit: 1,
    ),
  );
  _require(
    scopedDue.single.recordId == assigned.last.recordId,
    'indexed_due_outbox_scope',
  );
  await store.transaction<void>((transaction) {
    transaction.putOperation(
      OfflineOperationRecord(
        partitionId: partitionId,
        generation: 0,
        operationId: 'terminal-operation',
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: 'terminal-record',
            operationId: 'terminal-operation',
            itemIndex: 0,
            state: OfflineWriteState.confirmed,
            attemptCount: 1,
          ),
        ],
        updatedAt: now,
        terminalAt: now,
      ),
    );
  });
  final operationNotYetDue = await store.transaction(
    (transaction) => transaction.dueOperations(
      partitionId,
      now: now.add(const Duration(hours: 1) - Duration(microseconds: 1)),
      retention: const Duration(hours: 1),
      limit: 1,
    ),
  );
  _require(operationNotYetDue.isEmpty, 'operation_retention_before_boundary');
  final dueOperation = await store.transaction(
    (transaction) => transaction.dueOperations(
      partitionId,
      now: now.add(const Duration(hours: 1)),
      retention: const Duration(hours: 1),
      limit: 1,
    ),
  );
  _require(
    dueOperation.single.operationId == 'terminal-operation',
    'indexed_due_operation',
  );
  OfflineStoreTransaction? committed;
  await store.transaction<void>((transaction) {
    committed = transaction;
  });
  _requireClosed(
    () => committed!.generation(partitionId),
    'sealed_commit_read',
  );

  final bytes = Uint8List.fromList(<int>[1, 2, 3]);
  await store.transaction<void>((transaction) {
    transaction.putCache(
      partitionId,
      OfflineCacheRecord.value(
        partitionId: partitionId,
        generation: 0,
        key: const OfflineEntityKey.vertex('bytes'),
        entity: Vertex(
          key: 'bytes',
          value: VertexValue.bytes(bytes),
          expiration: null,
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    );
  });
  bytes[0] = 9;
  final returnedBytes = await store.transaction((transaction) {
    final value =
        transaction
                .getCache(partitionId, const OfflineEntityKey.vertex('bytes'))!
                .vertex!
                .value
            as BytesValue;
    return value.value;
  });
  returnedBytes[0] = 8;
  final ownsBytes = await store.transaction((transaction) {
    final value =
        transaction
                .getCache(partitionId, const OfflineEntityKey.vertex('bytes'))!
                .vertex!
                .value
            as BytesValue;
    return value.value.first == 1;
  });
  _require(ownsBytes, 'defensive_byte_ownership');
  final touchAt = now.add(const Duration(microseconds: 1));
  final touchChanges = <OfflineStoreChange>[];
  final touchSubscription = store.changes(partitionId).listen(touchChanges.add);
  try {
    await store.transaction<void>(
      (transaction) => transaction.touchCache(
        partitionId,
        const OfflineEntityKey.vertex('bytes'),
        touchAt,
      ),
    );
    await Future<void>.delayed(Duration.zero);
    final touched = await store.transaction(
      (transaction) =>
          transaction
              .getCache(partitionId, const OfflineEntityKey.vertex('bytes'))!
              .lastAccessAt ==
          touchAt,
    );
    _require(touched && touchChanges.isEmpty, 'durable_touch_without_change');
  } finally {
    await touchSubscription.cancel();
  }
  _requireClosed(
    () => committed!.deleteOutbox(partitionId, assigned.first.recordId),
    'sealed_commit_mutation',
  );

  for (final differentIntent in <bool>[false, true]) {
    try {
      await store.transaction<void>((transaction) {
        transaction.enqueueAll(<OfflineOutboxRecord>[
          _record(
            recordId: differentIntent ? 'collision-record' : 'record-0',
            operationId: operationId,
            itemIndex: 0,
            key: differentIntent ? 'collision' : 'a',
            now: now,
          ),
        ]);
      });
      _require(false, 'operation_collision_$differentIntent');
    } on OfflineIdentityConflictException catch (error) {
      _require(
        error.kind == OfflineIdentityKind.operation,
        'typed_operation_collision_$differentIntent',
      );
    }
  }
  for (final differentIntent in <bool>[false, true]) {
    try {
      await store.transaction<void>((transaction) {
        transaction.enqueueAll(<OfflineOutboxRecord>[
          _record(
            recordId: assigned.first.recordId,
            operationId: 'record-collision-$differentIntent',
            itemIndex: 0,
            key: differentIntent ? 'different' : 'a',
            now: now,
          ),
        ]);
      });
      _require(false, 'record_collision_$differentIntent');
    } on OfflineIdentityConflictException catch (error) {
      _require(
        error.kind == OfflineIdentityKind.record,
        'typed_record_collision_$differentIntent',
      );
    }
  }
  try {
    await store.transaction<void>((transaction) {
      transaction.enqueueAll(<OfflineOutboxRecord>[
        _record(
          recordId: 'unaggregated-first',
          operationId: 'unaggregated-operation',
          itemIndex: 0,
          key: 'first',
          now: now,
        ),
      ]);
      transaction.enqueueAll(<OfflineOutboxRecord>[
        _record(
          recordId: 'unaggregated-second',
          operationId: 'unaggregated-operation',
          itemIndex: 0,
          key: 'second',
          now: now,
        ),
      ]);
    });
    _require(false, 'unaggregated_operation_collision');
  } on OfflineIdentityConflictException catch (error) {
    _require(
      error.kind == OfflineIdentityKind.operation,
      'typed_unaggregated_operation_collision',
    );
  }
  final unaggregatedRolledBack = await store.transaction(
    (transaction) => transaction
        .scanOutbox(
          partitionId,
          operationId: 'unaggregated-operation',
          limit: 1,
        )
        .records
        .isEmpty,
  );
  _require(unaggregatedRolledBack, 'unaggregated_operation_rollback');

  try {
    await store.transaction<void>((transaction) {
      final incomplete = transaction.enqueueAll(<OfflineOutboxRecord>[
        _record(
          recordId: 'incomplete-0',
          operationId: 'incomplete-operation',
          itemIndex: 0,
          key: 'incomplete-a',
          now: now,
        ),
        _record(
          recordId: 'incomplete-1',
          operationId: 'incomplete-operation',
          itemIndex: 1,
          key: 'incomplete-b',
          now: now,
        ),
      ]);
      transaction.putOperation(
        OfflineOperationRecord(
          partitionId: partitionId,
          generation: 0,
          operationId: 'incomplete-operation',
          items: <OfflineWriteStatus>[
            OfflineWriteStatus(
              recordId: incomplete.first.recordId,
              operationId: 'incomplete-operation',
              itemIndex: 0,
              state: OfflineWriteState.locallyCommitted,
              attemptCount: 0,
            ),
          ],
          updatedAt: now,
        ),
      );
    });
    _require(false, 'incomplete_operation_topology');
  } on OfflineIdentityConflictException catch (error) {
    _require(
      error.kind == OfflineIdentityKind.operation,
      'typed_incomplete_operation_topology',
    );
  }
  final incompleteRolledBack = await store.transaction(
    (transaction) => transaction
        .scanOutbox(partitionId, operationId: 'incomplete-operation', limit: 1)
        .records
        .isEmpty,
  );
  _require(incompleteRolledBack, 'incomplete_operation_rollback');

  try {
    await store.transaction<void>((transaction) {
      final current = transaction.getOutbox(
        partitionId,
        assigned.first.recordId,
      )!;
      transaction.updateOutbox(
        current.copyWith(
          state: OfflineOutboxState.sending,
          leaseOwner: 'mismatched-owner',
          leaseUntil: now.add(const Duration(seconds: 1)),
        ),
      );
    });
    _require(false, 'outbox_status_mismatch_graph');
  } on OfflineDurableGraphException {
    // Required: the outbox lifecycle and operation item are one graph.
  }
  final mismatchRolledBack = await store.transaction((transaction) {
    final record = transaction.getOutbox(partitionId, assigned.first.recordId)!;
    final operation = transaction.getOperation(partitionId, operationId)!;
    return record.state == OfflineOutboxState.enqueued &&
        operation.items.first.state == OfflineWriteState.locallyCommitted;
  });
  _require(mismatchRolledBack, 'outbox_status_mismatch_rollback');

  await _requireInvalidCommit(
    store,
    partitionId,
    (transaction) {
      final current = transaction.getOutbox(
        partitionId,
        assigned.first.recordId,
      )!;
      transaction.updateOutbox(
        current.copyWith(diagnosticCode: 'unauthenticated'),
      );
      final operation = transaction.getOperation(partitionId, operationId)!;
      final items = operation.items.toList(growable: false);
      items[current.itemIndex] = OfflineWriteStatus(
        recordId: current.recordId,
        operationId: current.operationId,
        itemIndex: current.itemIndex,
        state: OfflineWriteState.pausedForAuth,
        attemptCount: current.attemptCount,
        diagnosticCode: 'unauthenticated',
      );
      transaction.putOperation(
        OfflineOperationRecord(
          partitionId: operation.partitionId,
          generation: operation.generation,
          operationId: operation.operationId,
          items: items,
          updatedAt: now,
        ),
      );
    },
    'auth_marker_without_pause_graph',
    (transaction) {
      final record = transaction.getOutbox(
        partitionId,
        assigned.first.recordId,
      )!;
      final operation = transaction.getOperation(partitionId, operationId)!;
      return !transaction.replayPausedForAuth(partitionId) &&
          record.diagnosticCode == null &&
          operation.items.first.state == OfflineWriteState.locallyCommitted;
    },
  );

  await _requireInvalidCommit(
    store,
    partitionId,
    (transaction) {
      final operation = transaction.getOperation(partitionId, operationId)!;
      final items = operation.items.toList(growable: false);
      final current = items.first;
      items[0] = OfflineWriteStatus(
        recordId: current.recordId,
        operationId: current.operationId,
        itemIndex: current.itemIndex,
        state: OfflineWriteState.pausedForAuth,
        attemptCount: current.attemptCount,
      );
      transaction.putOperation(
        OfflineOperationRecord(
          partitionId: operation.partitionId,
          generation: operation.generation,
          operationId: operation.operationId,
          items: items,
          updatedAt: now,
        ),
      );
    },
    'auth_state_without_pause_graph',
    (transaction) {
      final operation = transaction.getOperation(partitionId, operationId)!;
      return !transaction.replayPausedForAuth(partitionId) &&
          operation.items.first.state == OfflineWriteState.locallyCommitted &&
          operation.items.first.diagnosticCode == null;
    },
  );

  try {
    await store.transaction<void>(
      (transaction) => transaction.deleteOperation(partitionId, operationId),
    );
    _require(false, 'delete_referenced_operation');
  } on OfflineArgumentException {
    // Required: retained outbox records must never become orphaned.
  }
  final referencePreserved = await store.transaction(
    (transaction) =>
        transaction.getOperation(partitionId, operationId) != null &&
        transaction.hasOutboxForOperation(partitionId, operationId),
  );
  _require(referencePreserved, 'referenced_operation_preserved');

  await store.transaction<void>((transaction) {
    final stale = transaction.enqueue(
      _record(
        recordId: 'stale-record',
        operationId: 'stale-operation',
        itemIndex: 0,
        key: 'stale',
        now: now.subtract(const Duration(days: 2)),
      ),
    );
    transaction.putOperation(
      OfflineOperationRecord(
        partitionId: partitionId,
        generation: 0,
        operationId: stale.operationId,
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: stale.recordId,
            operationId: stale.operationId,
            itemIndex: 0,
            state: OfflineWriteState.locallyCommitted,
            attemptCount: 0,
          ),
        ],
        updatedAt: now,
      ),
    );
  });

  await store.transaction<void>((transaction) {
    final retry = _enqueueOperation(
      transaction,
      _record(
        recordId: 'retry-record',
        operationId: 'retry-operation',
        itemIndex: 0,
        key: 'retry',
        now: now,
      ),
      now,
    );
    transaction.updateOutbox(
      retry.copyWith(
        attemptCount: 1,
        nextAttemptAt: now.add(const Duration(milliseconds: 250)),
        diagnosticCode: 'unavailable',
      ),
    );
    _putMatchingStatus(
      transaction,
      retry,
      OfflineWriteState.retryScheduled,
      attemptCount: 1,
      diagnosticCode: 'unavailable',
      now: now,
    );

    final deadLetter = _enqueueOperation(
      transaction,
      _record(
        recordId: 'dead-letter-record',
        operationId: 'dead-letter-operation',
        itemIndex: 0,
        key: 'dead-letter',
        now: now,
      ),
      now,
    );
    final terminalAt = now.add(const Duration(microseconds: 1));
    transaction.updateOutbox(
      deadLetter.copyWith(
        state: OfflineOutboxState.deadLetter,
        deadLetteredAt: terminalAt,
        diagnosticCode: 'permanent',
      ),
    );
    _putMatchingStatus(
      transaction,
      deadLetter,
      OfflineWriteState.deadLetter,
      attemptCount: 0,
      diagnosticCode: 'permanent',
      now: terminalAt,
    );

    for (final transition in <({String id, OfflineWriteState state})>[
      (id: 'confirmed', state: OfflineWriteState.confirmed),
      (id: 'expired', state: OfflineWriteState.expired),
    ]) {
      final record = _enqueueOperation(
        transaction,
        _record(
          recordId: '${transition.id}-record',
          operationId: '${transition.id}-operation',
          itemIndex: 0,
          key: transition.id,
          now: now,
        ),
        now,
      );
      _putMatchingStatus(
        transaction,
        record,
        transition.state,
        attemptCount: 1,
        now: terminalAt,
      );
      transaction.deleteOutbox(partitionId, record.recordId);
    }
  });

  final deadLetterBeforeRetention = await store.transaction(
    (transaction) => transaction.dueOutbox(
      partitionId,
      operationId: 'dead-letter-operation',
      now: now.add(const Duration(hours: 1)),
      maxAge: const Duration(days: 10),
      deadLetterRetention: const Duration(hours: 1),
      limit: 1,
    ),
  );
  _require(
    deadLetterBeforeRetention.isEmpty,
    'dead_letter_retention_before_boundary',
  );
  final deadLetterDue = await store.transaction(
    (transaction) => transaction.dueOutbox(
      partitionId,
      operationId: 'dead-letter-operation',
      now: now.add(const Duration(hours: 1, microseconds: 1)),
      maxAge: const Duration(days: 10),
      deadLetterRetention: const Duration(hours: 1),
      limit: 1,
    ),
  );
  _require(
    deadLetterDue.single.recordId == 'dead-letter-record',
    'dead_letter_retention_at_boundary',
  );

  final claimed = await store.transaction((transaction) {
    final records = transaction.claim(
      partitionId,
      owner: 'owner',
      now: now,
      maxAge: const Duration(days: 1),
      leaseDuration: const Duration(seconds: 1),
      limit: 3,
    );
    for (final record in records) {
      _putMatchingStatus(
        transaction,
        record,
        OfflineWriteState.sending,
        attemptCount: record.attemptCount,
        now: now,
      );
    }
    return records;
  });
  _require(claimed.length == 2, 'independent_key_claim');
  final renewed = await store.transaction(
    (transaction) => transaction.renewLease(
      partitionId,
      claimed.first.recordId,
      owner: 'owner',
      generation: 0,
      now: now.add(const Duration(milliseconds: 500)),
      leaseDuration: const Duration(seconds: 1),
    ),
  );
  _require(renewed, 'lease_cas');

  final recoveryAt = now.add(const Duration(seconds: 2));
  final recovered = await store.transaction((transaction) {
    final expiredLeases = transaction
        .outbox(partitionId)
        .where(
          (record) =>
              record.state == OfflineOutboxState.sending &&
              record.leaseUntil != null &&
              !recoveryAt.isBefore(record.leaseUntil!),
        )
        .toList(growable: false);
    for (final record in expiredLeases) {
      _putMatchingStatus(
        transaction,
        record,
        OfflineWriteState.locallyCommitted,
        attemptCount: record.attemptCount,
        now: recoveryAt,
      );
    }
    final reclaimed = transaction.claim(
      partitionId,
      owner: 'recovered-owner',
      now: recoveryAt,
      maxAge: const Duration(days: 1),
      leaseDuration: const Duration(seconds: 1),
      limit: 4,
    );
    for (final record in reclaimed) {
      _putMatchingStatus(
        transaction,
        record,
        OfflineWriteState.sending,
        attemptCount: record.attemptCount,
        now: recoveryAt,
      );
    }
    return (expired: expiredLeases, reclaimed: reclaimed);
  });
  _require(
    recovered.expired.length == 2 && recovered.reclaimed.length == 3,
    'expired_lease_recovery_and_retry_reclaim',
  );

  store = await reopen(store);
  final restartSafe = await store.transaction((transaction) {
    final cache = transaction.getCache(
      partitionId,
      const OfflineEntityKey.vertex('bytes'),
    );
    final operation = transaction.getOperation(partitionId, operationId);
    return cache != null &&
        cache.lastAccessAt == touchAt &&
        operation != null &&
        transaction.getOutbox(partitionId, 'dead-letter-record')?.state ==
            OfflineOutboxState.deadLetter &&
        transaction.getOutbox(partitionId, 'confirmed-record') == null &&
        transaction.getOutbox(partitionId, 'expired-record') == null &&
        transaction.outbox(partitionId).length == 5;
  });
  _require(restartSafe, 'successful_commits_reopen_same_limits');

  await store.transaction<void>(
    (transaction) => transaction.setReplayPausedForAuth(partitionId, true),
  );
  final authPaused = await store.transaction((transaction) {
    return transaction.replayPausedForAuth(partitionId) &&
        transaction
            .claim(
              partitionId,
              owner: 'paused-owner',
              now: now.add(const Duration(seconds: 2)),
              maxAge: const Duration(days: 1),
              leaseDuration: const Duration(seconds: 1),
              limit: 2,
            )
            .isEmpty;
  });
  _require(authPaused, 'durable_auth_pause');

  await store.transaction<void>(
    (transaction) => transaction.wipePartition(partitionId),
  );
  final wiped = await store.transaction((transaction) {
    return transaction.generation(partitionId) == 1 &&
        !transaction.replayPausedForAuth(partitionId) &&
        transaction.outbox(partitionId).isEmpty &&
        transaction.operations(partitionId).isEmpty &&
        transaction.getCache(
              partitionId,
              const OfflineEntityKey.vertex('key'),
            ) ==
            null;
  });
  _require(wiped, 'partition_wipe_generation');

  await _runLeaseCasConformance(store, now);
  await _runNotificationConformance(
    store,
    now,
    maxControllerProbe: maxNotificationControllerProbe,
  );
  store = await _runCapacityAndRetentionConformance(
    store,
    reopen,
    now,
    maxProbeRecords: maxCapacityProbeRecords,
  );

  final finalState = await store.transaction((transaction) {
    return transaction.generation('conformance-lease') == 1 &&
        transaction.generation('conformance-notifications') == 1 &&
        transaction.generation('conformance-capacity') == 1 &&
        transaction.generation('conformance-cache-capacity') == 1;
  });
  _require(finalState, 'resource_contracts_reopen_cleanly');
}

Future<void> _runLeaseCasConformance(OfflineStore store, DateTime now) async {
  const partitionId = 'conformance-lease';
  final enqueued = await store.transaction((transaction) {
    return _enqueueOperation(
      transaction,
      _record(
        recordId: 'lease-record',
        operationId: 'lease-operation',
        itemIndex: 0,
        key: 'lease-key',
        now: now,
        partitionId: partitionId,
      ),
      now,
    );
  });

  Future<List<OfflineOutboxRecord>> claim(String owner) =>
      store.transaction((transaction) {
        final claimed = transaction.claim(
          partitionId,
          owner: owner,
          now: now,
          maxAge: const Duration(days: 1),
          leaseDuration: const Duration(seconds: 2),
          limit: 1,
        );
        for (final record in claimed) {
          _putMatchingStatus(
            transaction,
            record,
            OfflineWriteState.sending,
            attemptCount: record.attemptCount,
            now: now,
          );
        }
        return claimed;
      });

  final competing = await Future.wait(<Future<List<OfflineOutboxRecord>>>[
    claim('claimer-a'),
    claim('claimer-b'),
  ]);
  _require(
    competing.fold<int>(0, (total, records) => total + records.length) == 1,
    'concurrent_claimers_single_winner',
  );
  final winner = competing.singleWhere((records) => records.isNotEmpty).single;
  final owner = winner.leaseOwner!;

  for (final negative in <({String owner, int generation, DateTime now})>[
    (owner: 'wrong-owner', generation: winner.generation, now: now),
    (owner: owner, generation: winner.generation + 1, now: now),
    (
      owner: owner,
      generation: winner.generation,
      now: now.add(const Duration(seconds: 2)),
    ),
  ]) {
    final renewed = await store.transaction(
      (transaction) => transaction.renewLease(
        partitionId,
        winner.recordId,
        owner: negative.owner,
        generation: negative.generation,
        now: negative.now,
        leaseDuration: const Duration(seconds: 2),
      ),
    );
    _require(!renewed, 'lease_renew_negative_cas');
  }

  final renewed = await store.transaction(
    (transaction) => transaction.renewLease(
      partitionId,
      winner.recordId,
      owner: owner,
      generation: winner.generation,
      now: now.add(const Duration(milliseconds: 250)),
      leaseDuration: const Duration(seconds: 3),
    ),
  );
  _require(renewed, 'lease_renew_positive_cas');
  final renewedRecord = await store.transaction(
    (transaction) => transaction.getOutbox(partitionId, winner.recordId),
  );
  _require(
    renewedRecord?.leaseUntil == now.add(const Duration(milliseconds: 3250)),
    'lease_renew_exact_deadline',
  );

  final wrongRelease = await store.transaction(
    (transaction) => _releaseConformanceClaim(
      transaction,
      partitionId,
      winner,
      owner: 'wrong-owner',
      now: now.add(const Duration(milliseconds: 500)),
    ),
  );
  _require(!wrongRelease, 'lease_release_negative_cas');
  final released = await store.transaction(
    (transaction) => _releaseConformanceClaim(
      transaction,
      partitionId,
      winner,
      owner: owner,
      now: now.add(const Duration(milliseconds: 500)),
    ),
  );
  _require(released, 'lease_release_positive_cas');

  final reclaimed = await store.transaction((transaction) {
    final claimed = transaction.claim(
      partitionId,
      owner: 'claimer-c',
      now: now.add(const Duration(milliseconds: 750)),
      maxAge: const Duration(days: 1),
      leaseDuration: const Duration(seconds: 2),
      limit: 1,
    );
    for (final record in claimed) {
      _putMatchingStatus(
        transaction,
        record,
        OfflineWriteState.sending,
        attemptCount: record.attemptCount,
        now: now.add(const Duration(milliseconds: 750)),
      );
    }
    return claimed;
  });
  _require(
    reclaimed.single.recordId == enqueued.recordId &&
        reclaimed.single.leaseOwner == 'claimer-c',
    'released_claim_reclaimable',
  );

  await store.transaction<void>(
    (transaction) => transaction.wipePartition(partitionId),
  );
  final lateRelease = await store.transaction(
    (transaction) => _releaseConformanceClaim(
      transaction,
      partitionId,
      reclaimed.single,
      owner: 'claimer-c',
      now: now.add(const Duration(seconds: 1)),
    ),
  );
  _require(!lateRelease, 'late_generation_release_rejected');
}

bool _releaseConformanceClaim(
  OfflineStoreTransaction transaction,
  String partitionId,
  OfflineOutboxRecord claimed, {
  required String owner,
  required DateTime now,
}) {
  final current = transaction.getOutbox(partitionId, claimed.recordId);
  if (current == null ||
      current.generation != claimed.generation ||
      current.state != OfflineOutboxState.sending ||
      current.leaseOwner != owner ||
      current.leaseUntil == null ||
      !now.isBefore(current.leaseUntil!) ||
      transaction.generation(partitionId) != claimed.generation) {
    return false;
  }
  transaction.updateOutbox(
    current.copyWith(
      state: OfflineOutboxState.enqueued,
      clearLeaseOwner: true,
      clearLeaseUntil: true,
      diagnosticCode: 'canceled',
    ),
  );
  _putMatchingStatus(
    transaction,
    current,
    OfflineWriteState.locallyCommitted,
    attemptCount: current.attemptCount,
    diagnosticCode: 'canceled',
    now: now,
  );
  return true;
}

Future<void> _runNotificationConformance(
  OfflineStore store,
  DateTime now, {
  required int maxControllerProbe,
}) async {
  const partitionId = 'conformance-notifications';
  final observedFuture = store.changes(partitionId).take(4).toList();
  await Future.wait<void>(
    List<Future<void>>.generate(3, (index) {
      return store.transaction<void>((transaction) {
        final key = 'notification-$index';
        transaction.putCache(
          partitionId,
          OfflineCacheRecord.value(
            partitionId: partitionId,
            generation: 0,
            key: OfflineEntityKey.vertex(key),
            entity: Vertex(
              key: key,
              value: VertexValue.int32(index),
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
      });
    }),
  );
  await store.transaction<void>(
    (transaction) => transaction.wipePartition(partitionId),
  );
  final observed = await _waitForConformance(
    observedFuture,
    'notification_no_gap_order_and_wipe',
  );
  _require(
    observed.map((change) => change.version).join(',') == '1,2,3,4' &&
        observed.take(3).every((change) => change.generation == 0) &&
        observed.last.generation == 1,
    'notification_no_gap_order_and_wipe',
  );

  final active = <StreamSubscription<OfflineStoreChange>>[];
  final rejection = Completer<Object>();
  try {
    for (var index = 0; index < maxControllerProbe; index++) {
      try {
        active.add(
          store
              .changes('conformance-controller-$index')
              .listen(
                (_) {},
                onError: (Object error) {
                  if (!rejection.isCompleted) rejection.complete(error);
                },
              ),
        );
      } catch (error) {
        if (!rejection.isCompleted) rejection.complete(error);
        break;
      }
    }
    final rejected = await _waitForConformance(
      rejection.future,
      'bounded_notification_controllers',
    );
    _require(
      rejected is OfflineCapacityException && active.isNotEmpty,
      'bounded_notification_controllers',
    );
    for (final subscription in active) {
      await subscription.cancel();
    }
    active.clear();

    final replacementEvent = Completer<OfflineStoreChange>();
    final replacement = store
        .changes('conformance-controller-replacement')
        .listen(replacementEvent.complete);
    try {
      await store.transaction<void>((transaction) {
        transaction.putCache(
          'conformance-controller-replacement',
          OfflineCacheRecord.missing(
            partitionId: 'conformance-controller-replacement',
            generation: 0,
            key: const OfflineEntityKey.vertex('missing'),
            validatedAt: now,
            lastAccessAt: now,
            missingUntil: now.add(const Duration(seconds: 1)),
          ),
        );
      });
      final change = await _waitForConformance(
        replacementEvent.future,
        'notification_controller_cleanup',
      );
      _require(change.version == 1, 'notification_controller_cleanup');
    } finally {
      await replacement.cancel();
    }
  } finally {
    for (final subscription in active) {
      await subscription.cancel();
    }
  }
}

Future<OfflineStore> _runCapacityAndRetentionConformance(
  OfflineStore store,
  OfflineStoreReopener reopen,
  DateTime now, {
  required int maxProbeRecords,
}) async {
  const partitionId = 'conformance-capacity';
  var admitted = 0;
  String? rejectedRecordId;
  for (var index = 0; index < maxProbeRecords; index++) {
    try {
      await store.transaction<void>((transaction) {
        _enqueueOperation(
          transaction,
          _record(
            recordId: 'capacity-record-$index',
            operationId: 'capacity-operation-$index',
            itemIndex: 0,
            key: 'capacity-key-$index',
            now: now,
            partitionId: partitionId,
          ),
          now,
        );
      });
      admitted += 1;
    } on OfflineCapacityException {
      rejectedRecordId = 'capacity-record-$index';
      break;
    }
  }
  _require(
    admitted > 0 && rejectedRecordId != null,
    'bounded_outbox_operation_capacity',
  );
  final beforeReopen = await store.transaction((transaction) {
    return (
      outbox: transaction.outbox(partitionId).length,
      operations: transaction.operations(partitionId).length,
      rejected: transaction.getOutbox(partitionId, rejectedRecordId!),
    );
  });
  _require(
    beforeReopen.outbox == admitted &&
        beforeReopen.operations == admitted &&
        beforeReopen.rejected == null,
    'capacity_rejection_atomic',
  );
  store = await reopen(store);
  final afterReopen = await store.transaction((transaction) {
    return (
      outbox: transaction.outbox(partitionId).length,
      operations: transaction.operations(partitionId).length,
    );
  });
  _require(
    afterReopen.outbox == admitted && afterReopen.operations == admitted,
    'capacity_accounting_reopens',
  );
  await store.transaction<void>(
    (transaction) => transaction.wipePartition(partitionId),
  );

  const cachePartition = 'conformance-cache-capacity';
  var evicted = false;
  var lastKey = '';
  for (var index = 0; index < maxProbeRecords; index++) {
    lastKey = 'cache-$index';
    await store.transaction<void>((transaction) {
      transaction.putCache(
        cachePartition,
        OfflineCacheRecord.value(
          partitionId: cachePartition,
          generation: 0,
          key: OfflineEntityKey.vertex(lastKey),
          entity: Vertex(
            key: lastKey,
            value: VertexValue.int32(index),
            expiration: null,
          ),
          validatedAt: now,
          lastAccessAt: now.add(Duration(microseconds: index)),
        ),
      );
    });
    if (index > 0) {
      evicted = await store.transaction(
        (transaction) =>
            transaction.getCache(
              cachePartition,
              const OfflineEntityKey.vertex('cache-0'),
            ) ==
            null,
      );
      if (evicted) break;
    }
  }
  final newestRetained = await store.transaction(
    (transaction) =>
        transaction.getCache(
          cachePartition,
          OfflineEntityKey.vertex(lastKey),
        ) !=
        null,
  );
  _require(evicted && newestRetained, 'cache_capacity_lru_only');
  await store.transaction<void>(
    (transaction) => transaction.wipePartition(cachePartition),
  );
  return reopen(store);
}

OfflineOutboxRecord _record({
  required String recordId,
  required String operationId,
  required int itemIndex,
  required String key,
  required DateTime now,
  String partitionId = 'conformance',
}) => OfflineOutboxRecord(
  recordId: recordId,
  operationId: operationId,
  itemIndex: itemIndex,
  partitionId: partitionId,
  intent: OfflinePutVertexIntent(
    Vertex(key: key, value: VertexValue.string(key), expiration: null),
  ),
  enqueuedAt: now,
  ordinal: 0,
  state: OfflineOutboxState.enqueued,
  attemptCount: 0,
  generation: 0,
);

OfflineOutboxRecord _expiredRecord({
  required String recordId,
  required String operationId,
  required DateTime now,
  required int attemptCount,
  String? diagnosticCode,
}) => OfflineOutboxRecord(
  recordId: recordId,
  operationId: operationId,
  itemIndex: 0,
  partitionId: 'conformance',
  intent: OfflinePutVertexIntent(
    Vertex(key: recordId, value: VertexValue.nil(), expiration: now),
  ),
  enqueuedAt: now,
  ordinal: 0,
  state: OfflineOutboxState.expired,
  attemptCount: attemptCount,
  generation: 0,
  diagnosticCode: diagnosticCode,
);

OfflineOutboxRecord _enqueueOperation(
  OfflineStoreTransaction transaction,
  OfflineOutboxRecord record,
  DateTime now,
) {
  final assigned = transaction.enqueue(record);
  transaction.putOperation(
    OfflineOperationRecord(
      partitionId: assigned.partitionId,
      generation: assigned.generation,
      operationId: assigned.operationId,
      items: <OfflineWriteStatus>[
        OfflineWriteStatus(
          recordId: assigned.recordId,
          operationId: assigned.operationId,
          itemIndex: assigned.itemIndex,
          state: OfflineWriteState.locallyCommitted,
          attemptCount: assigned.attemptCount,
        ),
      ],
      updatedAt: now,
    ),
  );
  return assigned;
}

void _putMatchingStatus(
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
  )!;
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
    operationId: operation.operationId,
    items: items,
  );
  final updatedAt = <DateTime>[
    operation.updatedAt,
    outbox.enqueuedAt,
    now,
  ].reduce((latest, value) => value.isAfter(latest) ? value : latest);
  transaction.putOperation(
    OfflineOperationRecord(
      partitionId: operation.partitionId,
      generation: operation.generation,
      operationId: operation.operationId,
      items: items,
      updatedAt: updatedAt,
      terminalAt: status.isTerminal ? operation.terminalAt ?? updatedAt : null,
    ),
  );
}

void _require(bool condition, String label) {
  if (!condition) throw StateError('offline_store_conformance:$label');
}

Future<T> _waitForConformance<T>(Future<T> future, String label) async {
  try {
    return await future.timeout(const Duration(seconds: 5));
  } on TimeoutException {
    throw StateError('offline_store_conformance:$label');
  }
}

void _requireClosed(void Function() action, String label) {
  try {
    action();
    _require(false, label);
  } on OfflineTransactionClosedException {
    // Required.
  }
}

final class _ConformanceRollback implements Exception {
  const _ConformanceRollback();
}

Future<void> _requireInvalidCommit(
  OfflineStore store,
  String partitionId,
  void Function(OfflineStoreTransaction transaction) mutate,
  String label,
  bool Function(OfflineStoreTransaction transaction) unchanged,
) async {
  final before = await store.transaction(
    (transaction) => (
      generation: transaction.generation(partitionId),
      outboxCount: transaction.outbox(partitionId).length,
      operationCount: transaction.operations(partitionId).length,
    ),
  );
  final changes = <OfflineStoreChange>[];
  final subscription = store.changes(partitionId).listen(changes.add);
  try {
    try {
      await store.transaction<void>(mutate);
      _require(false, label);
    } on OfflineDurableGraphException {
      // Required.
    }
    await Future<void>.delayed(Duration.zero);
    final preserved = await store.transaction(
      (transaction) =>
          unchanged(transaction) &&
          transaction.generation(partitionId) == before.generation &&
          transaction.outbox(partitionId).length == before.outboxCount &&
          transaction.operations(partitionId).length == before.operationCount,
    );
    _require(preserved && changes.isEmpty, '${label}_atomic');
  } finally {
    await subscription.cancel();
  }
}
