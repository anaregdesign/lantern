import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'change_store.dart';
import 'codec.dart';
import 'conformance.dart';
import 'errors.dart';
import 'scoped_change_store.dart';
import 'types.dart';

/// Verifies opaque public CDC transactions across the real reopen boundary.
Future<void> runScopedChangeStoreConformanceSuite(
  OfflineStoreFactory factory, {
  required OfflineStoreReopener reopen,
}) async {
  var store = await factory();
  const partition = 'scoped-change-conformance';
  final first = OfflineScopedChangeCursor([1, 0, 255]);
  final last = OfflineScopedChangeCursor([9, 8, 7]);
  final now = DateTime.utc(2026);
  Future<void> cache(String key) => store.transaction((tx) async {
    await tx.putCache(
      partition,
      OfflineCacheRecord.value(
        partitionId: partition,
        generation: await tx.generation(partition),
        key: OfflineEntityKey.vertex(key),
        entity: Vertex(
          key: key,
          value: VertexValue.string('resident'),
          expiration: now.add(const Duration(days: 1)),
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    );
  });
  Future<bool> cached(String key) => store.transaction(
    (tx) async =>
        await tx.getCache(partition, OfflineEntityKey.vertex(key)) != null,
  );
  Future<List<int>?> cursor() => store.transaction(
    (tx) async => (await tx.scopedChangeCursor(partition))?.toBytes(),
  );
  final receiptEpoch = ReceiptEpoch(Uint8List.fromList(List.filled(16, 1)));
  final receiptPolicy = OfflineReceiptPolicy(
    deploymentEpoch: receiptEpoch,
    retention: const Duration(hours: 24),
    maxEntries: BigInt.from(1024),
    maxBytes: BigInt.from(1024 * 1024),
    fingerprint: Uint8List.fromList(List.filled(32, 4)),
  );
  final receiptId = Uint8List.fromList(List<int>.filled(49, 6))..[0] = 1;
  receiptId.setRange(1, 17, receiptEpoch.bytes);
  var timestamp = now.millisecondsSinceEpoch;
  for (var index = 24; index >= 17; index--) {
    receiptId[index] = timestamp & 0xff;
    timestamp >>= 8;
  }
  final evidence = OfflineReceiptEvidence(
    operationId: ReceiptOperationId(receiptId),
    groupId: ReceiptGroupId(Uint8List.fromList(List.filled(16, 5))),
    endpoint: ReceiptEndpoint(
      nodeId: Uint8List.fromList(List.filled(16, 2)),
      generation: Uint8List.fromList(List.filled(16, 3)),
    ),
    mutation: ReceiptMutationKind.vertexPut,
    policy: receiptPolicy,
    itemIndex: 0,
    itemCount: 1,
    state: OfflineReceiptReconciliationState.statusRequired,
    mayHaveDispatched: true,
  );
  await cache('first');
  await cache('last');
  final retained = await store.transaction((tx) async {
    final record = await tx.enqueue(
      OfflineOutboxRecord(
        recordId: 'pending-record',
        operationId: 'pending-operation',
        itemIndex: 0,
        partitionId: partition,
        intent: OfflinePutVertexIfAbsentIntent(
          Vertex(key: 'pending', value: VertexValue.nil(), expiration: null),
        ),
        enqueuedAt: now,
        ordinal: 0,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: await tx.generation(partition),
        receipt: evidence,
      ),
    );
    await tx.putOperation(
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
  await store.transaction((tx) => tx.resetScopedChangeCursor(partition, first));
  try {
    await store.transaction(
      (tx) => tx.applyChangeChunk(
        partition,
        OfflineChangeChunk(
          origin: '01' * 16,
          sequence: BigInt.one,
          chunkIndex: 0,
          isLast: true,
          keys: const [OfflineEntityKey.vertex('last')],
        ),
      ),
    );
    throw StateError('private progress mixed with public checkpoint');
  } on OfflineChangeGapException {
    /* protocols require an explicit reset before switching */
  }
  _require(
    _equal(await cursor(), first.toBytes()),
    'mixed protocol keeps cursor',
  );

  _require(!await cached('first'), 'bootstrap hides resident');
  final resetEpoch = await store.transaction((tx) => tx.changeEpoch(partition));
  for (final key in ['first', 'last']) {
    await cache(key);
    _require(
      await store.transaction(
        (tx) => tx.completeUnknownResident(
          partition,
          OfflineEntityKey.vertex(key),
          expectedEpoch: resetEpoch,
        ),
      ),
      'revalidation completes',
    );
  }
  final partial = OfflineScopedChangeFrame(
    keys: const [OfflineEntityKey.vertex('first')],
  );
  await store.transaction(
    (tx) => tx.applyScopedChangeFrame(partition, partial),
  );
  _require(
    !await cached('first') && await cached('last'),
    'partial invalidates exact key',
  );
  _require(_equal(await cursor(), first.toBytes()), 'partial keeps cursor');
  store = await reopen(store);
  _require(
    !await cached('first') && _equal(await cursor(), first.toBytes()),
    'partial survives reopen',
  );
  final finalFrame = OfflineScopedChangeFrame(
    keys: const [OfflineEntityKey.vertex('last')],
    cursor: last,
  );
  try {
    await store.transaction((tx) async {
      await tx.applyScopedChangeFrame(partition, finalFrame);
      throw const OfflineCanceledException();
    });
  } on OfflineCanceledException {
    /* deliberate transaction rollback */
  }
  _require(
    await cached('last') && _equal(await cursor(), first.toBytes()),
    'rollback includes cache and cursor',
  );
  await store.transaction(
    (tx) => tx.applyScopedChangeFrame(partition, finalFrame),
  );
  store = await reopen(store);
  _require(
    !await cached('last') && _equal(await cursor(), last.toBytes()),
    'final atomic reopen',
  );
  await cache('resident');
  await store.transaction((tx) => tx.resetScopedChangeCursor(partition, last));
  final epoch = await store.transaction((tx) => tx.changeEpoch(partition));
  await cache('resident');
  await store.transaction(
    (tx) => tx.applyScopedChangeFrame(
      partition,
      OfflineScopedChangeFrame(
        keys: const [OfflineEntityKey.vertex('resident')],
      ),
    ),
  );
  _require(
    !await store.transaction(
      (tx) => tx.completeUnknownResident(
        partition,
        const OfflineEntityKey.vertex('resident'),
        expectedEpoch: epoch,
      ),
    ),
    'partial fences late read',
  );
  await store.transaction((tx) => tx.resetScopedChangeCursor(partition, null));
  _require(await cursor() == null, 'gap clears checkpoint');
  final pending = await store.transaction(
    (tx) => tx.getOutbox(partition, 'pending-record'),
  );
  _require(
    pending != null && OfflineCodec.encodeOutboxRecord(pending) == retained,
    'gap preserves pending bytes',
  );
  _require(
    (await store.transaction(
      (tx) => tx.changeCursor(partition),
    )).sequences.isEmpty,
    'opaque does not invent origins',
  );
  _require(
    await store.transaction((tx) => tx.scopedChangeCursor('other')) == null,
    'partition isolation',
  );
  await store.transaction((tx) => tx.resetScopedChangeCursor(partition, first));
  await store.transaction((tx) => tx.wipePartition(partition));
  _require(await cursor() == null, 'wipe clears checkpoint');
  try {
    await store.transaction(
      (tx) => tx.applyScopedChangeFrame(partition, finalFrame),
    );
    throw StateError('frame admitted before bootstrap');
  } on OfflineChangeGapException {
    /* required fail-closed response */
  }
}

bool _equal(List<int>? a, List<int> b) =>
    a != null &&
    a.length == b.length &&
    List.generate(a.length, (i) => a[i] == b[i]).every((v) => v);
void _require(bool ok, String contract) {
  if (!ok) throw StateError(contract);
}
