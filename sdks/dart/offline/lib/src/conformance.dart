import 'dart:async';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'store.dart';
import 'types.dart';

/// Creates one empty store instance for the reusable adapter conformance suite.
typedef OfflineStoreFactory = FutureOr<OfflineStore> Function();

/// Runs the storage-neutral transaction, ordering, lease, and wipe contract.
///
/// Adapter packages should invoke this function from their own test runner. It
/// throws [StateError] with a content-free contract label on any mismatch.
Future<void> runStoreConformanceSuite(OfflineStoreFactory factory) async {
  final store = await factory();
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

  final claimed = await store.transaction(
    (transaction) => transaction.claim(
      partitionId,
      owner: 'owner',
      now: now,
      maxAge: const Duration(days: 1),
      leaseDuration: const Duration(seconds: 1),
      limit: 3,
    ),
  );
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

  await store.transaction<void>(
    (transaction) => transaction.wipePartition(partitionId),
  );
  final wiped = await store.transaction((transaction) {
    return transaction.generation(partitionId) == 1 &&
        transaction.outbox(partitionId).isEmpty &&
        transaction.operations(partitionId).isEmpty &&
        transaction.getCache(
              partitionId,
              const OfflineEntityKey.vertex('key'),
            ) ==
            null;
  });
  _require(wiped, 'partition_wipe_generation');
}

OfflineOutboxRecord _record({
  required String recordId,
  required String operationId,
  required int itemIndex,
  required String key,
  required DateTime now,
}) => OfflineOutboxRecord(
  recordId: recordId,
  operationId: operationId,
  itemIndex: itemIndex,
  partitionId: 'conformance',
  intent: OfflinePutVertexIntent(
    Vertex(key: key, value: VertexValue.string(key), expiration: null),
  ),
  enqueuedAt: now,
  ordinal: 0,
  state: OfflineOutboxState.enqueued,
  attemptCount: 0,
  generation: 0,
);

void _require(bool condition, String label) {
  if (!condition) throw StateError('offline_store_conformance:$label');
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
