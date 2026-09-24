import 'package:lantern_client/lantern_client.dart';

import 'change_store.dart';
import 'codec.dart';
import 'conformance.dart';
import 'errors.dart';
import 'types.dart';

/// Verifies atomic CDC invalidation, chunk recovery, and portable cursors.
///
/// Like the main store suite, [reopen] must cross the adapter's real persistence
/// boundary. This tests the storage prerequisite of identity-only Subscribe;
/// it does not imply that the network CDC consumer is implemented.
Future<void> runChangeStoreConformanceSuite(
  OfflineStoreFactory factory, {
  required OfflineStoreReopener reopen,
}) async {
  var store = await factory();
  const partition = 'change-conformance';
  const origin = '00000000000000000000000000000001';
  const otherOrigin = '00000000000000000000000000000002';
  final sequence = BigInt.parse('9223372036854775809');
  final now = DateTime.utc(2026);

  Future<void> cache(String key) => store.transaction((transaction) async {
    await transaction.putCache(
      partition,
      OfflineCacheRecord.value(
        partitionId: partition,
        generation: await transaction.generation(partition),
        key: OfflineEntityKey.vertex(key),
        entity: Vertex(
          key: key,
          value: VertexValue.string('content'),
          expiration: now.add(const Duration(days: 1)),
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    );
  });
  Future<bool> cached(String key) => store.transaction(
    (transaction) async =>
        await transaction.getCache(partition, OfflineEntityKey.vertex(key)) !=
        null,
  );
  Future<OfflineChangeCursor> cursor() =>
      store.transaction((transaction) => transaction.changeCursor(partition));
  final first = OfflineChangeChunk(
    origin: origin,
    sequence: sequence,
    chunkIndex: 0,
    isLast: false,
    keys: const [OfflineEntityKey.vertex('first')],
  );
  final last = OfflineChangeChunk(
    origin: origin,
    sequence: sequence,
    chunkIndex: 1,
    isLast: true,
    vertexPrefixes: const ['literal%_'],
  );
  await cache('first');
  await cache('literal%_match');
  await cache('literalXXmatch');
  try {
    await store.transaction<void>((transaction) async {
      await transaction.applyChangeChunk(partition, first);
      throw const _AbortChange();
    });
  } on _AbortChange {
    // Expected: neither invalidation nor partial progress may commit.
  }
  _require(await cached('first'), 'change_rollback_cache');
  _require((await cursor()).sequences.isEmpty, 'change_rollback_cursor');
  await _expectGap(
    () => store.transaction(
      (transaction) => transaction.applyChangeChunk(partition, last),
    ),
  );
  await store.transaction(
    (transaction) => transaction.applyChangeChunk(partition, first),
  );
  _require(!await cached('first'), 'change_partial_invalidation');
  _require(
    (await cursor()).sequences[origin] == BigInt.zero,
    'change_partial_no_advance',
  );
  store = await reopen(store);
  await cache('first');
  await store.transaction(
    (transaction) => transaction.applyChangeChunk(partition, first),
  );
  _require(await cached('first'), 'change_duplicate_chunk_no_reinvalidate');
  await store.transaction(
    (transaction) => transaction.applyChangeChunk(partition, last),
  );
  _require(!await cached('literal%_match'), 'change_literal_prefix');
  _require(await cached('literalXXmatch'), 'change_prefix_no_wildcard');
  _require(
    (await cursor()).sequences[origin] == sequence,
    'change_final_cursor',
  );
  store = await reopen(store);
  _require(
    (await cursor()).sequences[origin] == sequence,
    'change_uint64_reopen',
  );
  await store.transaction(
    (transaction) => transaction.applyChangeChunk(
      partition,
      OfflineChangeChunk(
        origin: otherOrigin,
        sequence: BigInt.one,
        chunkIndex: 0,
        isLast: true,
      ),
    ),
  );
  _require((await cursor()).sequences.length == 2, 'change_vector_origins');
  final retained = await store.transaction((transaction) async {
    final record = await transaction.enqueue(
      OfflineOutboxRecord(
        recordId: 'pending-record',
        operationId: 'pending-operation',
        itemIndex: 0,
        partitionId: partition,
        intent: OfflinePutVertexIntent(
          Vertex(key: 'pending', value: VertexValue.nil(), expiration: null),
        ),
        enqueuedAt: now,
        ordinal: 0,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: await transaction.generation(partition),
      ),
    );
    await transaction.putOperation(
      OfflineOperationRecord(
        partitionId: partition,
        generation: record.generation,
        operationId: record.operationId,
        items: [
          OfflineWriteStatus(
            recordId: record.recordId,
            operationId: record.operationId,
            itemIndex: 0,
            state: OfflineWriteState.locallyCommitted,
            attemptCount: 0,
          ),
        ],
        updatedAt: now,
        terminalAt: null,
      ),
    );
    return OfflineCodec.encodeOutboxRecord(record);
  });
  await store.transaction(
    (transaction) => transaction.resetChangeCursor(
      partition,
      OfflineChangeCursor({origin: sequence + BigInt.one}),
    ),
  );
  _require(!await cached('first'), 'change_checkpoint_unknown');
  _require(!await cached('literalXXmatch'), 'change_checkpoint_all_resident');
  _require((await cursor()).sequences.length == 1, 'change_checkpoint_vector');
  final pending = await store.transaction(
    (transaction) => transaction.getOutbox(partition, 'pending-record'),
  );
  _require(
    pending != null && OfflineCodec.encodeOutboxRecord(pending) == retained,
    'change_checkpoint_preserves_pending_intent',
  );
  await store.transaction(
    (transaction) => transaction.wipePartition(partition),
  );
  store = await reopen(store);
  _require((await cursor()).sequences.isEmpty, 'change_wipe_cursor');
}

Future<void> _expectGap(Future<void> Function() action) async {
  try {
    await action();
  } on OfflineChangeGapException {
    return;
  }
  throw StateError('change_chunk_gap');
}

void _require(bool condition, String label) {
  if (!condition) throw StateError(label);
}

final class _AbortChange implements Exception {
  const _AbortChange();
}
