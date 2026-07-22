import 'dart:async';

import 'package:lantern_client/lantern_client.dart';

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
  final vertex = Vertex(
    key: 'key',
    value: VertexValue.string('value'),
    expiration: null,
  );

  try {
    await store.transaction<void>((transaction) {
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

  final claimed = await store.transaction(
    (transaction) => transaction.claim(
      partitionId,
      owner: 'owner',
      now: now,
      leaseDuration: const Duration(seconds: 1),
      limit: 2,
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

final class _ConformanceRollback implements Exception {
  const _ConformanceRollback();
}
