import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'change_store.dart';
import 'codec.dart';
import 'conformance.dart';
import 'errors.dart';
import 'scoped_change_store.dart';
import 'store.dart';
import 'types.dart';

/// Verifies opaque public CDC transactions across the real reopen boundary.
Future<void> runScopedChangeStoreConformanceSuite(
  OfflineStoreFactory factory, {
  required OfflineStoreReopener reopen,
}) async {
  var store = await factory();
  const partition = 'scoped-change-conformance';
  final first = OfflineScopedChangeCursor([1, 0, 255]);
  final progress = OfflineScopedChangeCursor([2, 0, 254]);
  final terminal = OfflineScopedChangeCursor([3, 0, 253]);
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
  Future<(int, int)> epochs() => store.transaction(
    (tx) async => (
      await tx.changeEpoch(partition),
      await tx.traversalValidityEpoch(partition),
    ),
  );
  Future<void> notified(
    Future<void> Function() action, {
    required bool invalidated,
  }) async {
    final changes = <OfflineStoreChange>[];
    final subscription = store.changes(partition).listen(changes.add);
    try {
      await action();
      _require(
        changes.length == 1 &&
            changes.single.observationInvalidated == invalidated,
        'public frame retains one commit notification with the correct invalidation',
      );
    } finally {
      await subscription.cancel();
    }
  }

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
  await cache('unresolved');
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
  final beforeReset = await epochs();
  await notified(
    () =>
        store.transaction((tx) => tx.resetScopedChangeCursor(partition, first)),
    invalidated: true,
  );
  _require(
    await epochs() == (beforeReset.$1 + 1, beforeReset.$2 + 1),
    'empty bootstrap reset advances both invalidation epochs',
  );
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
  final beforeProgress = await epochs();
  Future<String> lastCacheBytes() => store.transaction(
    (tx) async => OfflineCodec.encodeCacheRecord(
      (await tx.getCache(partition, const OfflineEntityKey.vertex('last')))!,
    ),
  );
  final history = await lastCacheBytes();
  await notified(
    () => store.transaction(
      (tx) => tx.applyScopedChangeFrame(
        partition,
        OfflineScopedChangeFrame(cursor: progress),
      ),
    ),
    invalidated: false,
  );
  store = await reopen(store);
  _require(
    _equal(await cursor(), progress.toBytes()) &&
        await epochs() == beforeProgress &&
        await lastCacheBytes() == history &&
        await cached('first') &&
        await store.transaction(
          (tx) => tx.hasUnknownResident(
            partition,
            const OfflineEntityKey.vertex('unresolved'),
          ),
        ) &&
        await store.transaction(
              (tx) async => OfflineCodec.encodeOutboxRecord(
                (await tx.getOutbox(partition, 'pending-record'))!,
              ),
            ) ==
            retained,
    'empty progress persists without changing epochs, history, Unknown or pending bytes',
  );
  final partial = OfflineScopedChangeFrame(
    keys: const [OfflineEntityKey.vertex('first')],
  );
  await notified(
    () => store.transaction(
      (tx) => tx.applyScopedChangeFrame(partition, partial),
    ),
    invalidated: true,
  );
  _require(
    !await cached('first') && await cached('last'),
    'partial invalidates exact key',
  );
  _require(_equal(await cursor(), progress.toBytes()), 'partial keeps cursor');
  _require(
    await epochs() == (beforeProgress.$1 + 1, beforeProgress.$2 + 1),
    'nonempty partial advances both epochs',
  );
  store = await reopen(store);
  _require(
    !await cached('first') && _equal(await cursor(), progress.toBytes()),
    'partial survives reopen',
  );
  final afterPartial = await epochs();
  await notified(
    () => store.transaction(
      (tx) => tx.applyScopedChangeFrame(
        partition,
        OfflineScopedChangeFrame(cursor: terminal),
      ),
    ),
    invalidated: false,
  );
  store = await reopen(store);
  _require(
    _equal(await cursor(), terminal.toBytes()) &&
        await epochs() == afterPartial &&
        await lastCacheBytes() == history,
    'empty terminal checkpoint does not repeat partial invalidation or renew history',
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
    await cached('last') &&
        _equal(await cursor(), terminal.toBytes()) &&
        await epochs() == afterPartial,
    'rollback includes cache and cursor',
  );
  await notified(
    () => store.transaction((tx) async {
      await tx.applyScopedChangeFrame(partition, finalFrame);
      await tx.applyScopedChangeFrame(
        partition,
        OfflineScopedChangeFrame(cursor: last),
      );
    }),
    invalidated: true,
  );
  _require(
    await epochs() == (afterPartial.$1 + 1, afterPartial.$2 + 1),
    'empty progress preserves accumulated invalidation in the same transaction',
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

  // Availability advances publication fences without renewing retained history.
  await store.transaction((tx) => tx.resetScopedChangeCursor(partition, first));
  await cache('tail');
  await cache('head');
  await cache('unrelated');
  const edgeKey = OfflineEntityKey.edge('tail', 'head');
  Future<void> cacheEdge() => store.transaction((tx) async {
    await tx.putCache(
      partition,
      OfflineCacheRecord.value(
        partitionId: partition,
        generation: await tx.generation(partition),
        key: edgeKey,
        entity: Edge(
          tail: 'tail',
          head: 'head',
          weight: 3,
          expiration: now.add(const Duration(hours: 1)),
        ),
        validatedAt: now,
        lastAccessAt: now,
      ),
    );
  });
  Future<OfflineCacheRecord?> retainedEdge() =>
      store.transaction((tx) => tx.getCache(partition, edgeKey));
  await cacheEdge();
  final beforeHistory = await retainedEdge();
  final beforeAvailability = await store.transaction(
    (tx) => tx.changeEpoch(partition),
  );
  await store.transaction((tx) => tx.advanceReadEpoch(partition));
  store = await reopen(store);
  final afterHistory = await retainedEdge();
  _require(
    afterHistory != null &&
        beforeHistory != null &&
        afterHistory.validatedAt == beforeHistory.validatedAt &&
        afterHistory.expiration == beforeHistory.expiration &&
        afterHistory.edge?.weight == beforeHistory.edge?.weight &&
        await cached('tail') &&
        await cached('head') &&
        _equal(await cursor(), first.toBytes()) &&
        await store.transaction((tx) => tx.changeEpoch(partition)) ==
            beforeAvailability + 1,
    'availability retains original history/cursor and advances epoch across reopen',
  );
  try {
    await store.transaction((tx) async {
      await tx.advanceReadEpoch(partition);
      await tx.applyScopedChangeFrame(
        partition,
        OfflineScopedChangeFrame(
          keys: const [OfflineEntityKey.vertex('tail')],
          cursor: last,
        ),
      );
      throw const OfflineCanceledException();
    });
  } on OfflineCanceledException {
    /* epoch, dependencies and cursor are one transaction */
  }
  _require(
    await retainedEdge() != null &&
        await cached('tail') &&
        _equal(await cursor(), first.toBytes()) &&
        await store.transaction((tx) => tx.changeEpoch(partition)) ==
            beforeAvailability + 1,
    'rollback preserves epoch, endpoint, Edge and completion cursor',
  );
  await store.transaction(
    (tx) => tx.applyScopedChangeFrame(
      partition,
      OfflineScopedChangeFrame(
        keys: const [OfflineEntityKey.vertex('tail')],
        cursor: last,
      ),
    ),
  );
  store = await reopen(store);
  _require(
    await retainedEdge() == null &&
        !await cached('tail') &&
        await cached('head') &&
        await cached('unrelated') &&
        _equal(await cursor(), last.toBytes()),
    'Vertex invalidation hides dependent Edge without evicting unrelated Vertices',
  );
  await cache('tail');
  await cacheEdge();
  await store.transaction(
    (tx) => tx.invalidateMutationCache(
      partition,
      const OfflineEntityKey.vertex('head'),
    ),
  );
  _require(
    await retainedEdge() == null &&
        !await cached('head') &&
        await cached('tail') &&
        await cached('unrelated'),
    'Vertex settlement invalidates dependent Edge evidence',
  );

  const range = 'scoped-resident-range';
  final identities = [
    for (var i = 0; i < 8; i++) OfflineEntityKey.vertex('key:$i'),
  ];
  await store.transaction((tx) async {
    for (final key in identities) {
      await tx.putCache(
        range,
        OfflineCacheRecord.missing(
          partitionId: range,
          generation: await tx.generation(range),
          key: key,
          validatedAt: now,
          lastAccessAt: now,
          missingUntil: now.add(const Duration(minutes: 1)),
        ),
      );
    }
    await tx.resetScopedChangeCursor(range, first);
  });
  Future<List<OfflineEntityKey>> page({String? after}) => store.transaction(
    (tx) => tx.unknownResidents(range, limit: 3, after: after),
  );
  final firstPage = await page();
  _require(firstPage.length == 3, 'bounded first resident range');
  final boundary = firstPage.last.canonical;
  await store.transaction((tx) async {
    final epoch = await tx.changeEpoch(range);
    for (final key in firstPage) {
      await tx.putCache(
        range,
        OfflineCacheRecord.missing(
          partitionId: range,
          generation: await tx.generation(range),
          key: key,
          validatedAt: now,
          lastAccessAt: now,
          missingUntil: now.add(const Duration(minutes: 1)),
        ),
      );
      _require(
        await tx.completeUnknownResident(range, key, expectedEpoch: epoch),
        'current completion removes page identity',
      );
      await tx.deleteCache(range, key);
    }
    // Reintroduce one identity behind the boundary, without recreating the
    // removed boundary itself. Scheduling position is not durable completion.
    await tx.putCache(
      range,
      OfflineCacheRecord.missing(
        partitionId: range,
        generation: await tx.generation(range),
        key: firstPage.first,
        validatedAt: now,
        lastAccessAt: now,
        missingUntil: now.add(const Duration(minutes: 1)),
      ),
    );
    await tx.resetScopedChangeCursor(range, first);
  });
  final secondPage = await page(after: boundary);
  final thirdPage = await page(after: secondPage.last.canonical);
  final later = [...secondPage, ...thirdPage];
  _require(
    secondPage.length == 3 &&
        thirdPage.length == 2 &&
        later.toSet().length == 5 &&
        later.every((key) => !firstPage.contains(key)),
    'exclusive range continues past removed boundary without skips or duplicates',
  );
  _require(
    (await page(after: thirdPage.last.canonical)).isEmpty &&
        (await page()).first == firstPage.first,
    'range exhaustion wraps to reintroduced earlier Unknown identity',
  );
  store = await reopen(store);
  final reopened = await page();
  _require(
    reopened.first == firstPage.first && reopened.length == 3,
    'new ephemeral cursor reaches unresolved residents after actual reopen',
  );
  await store.transaction((tx) async {
    for (final key in [firstPage.first, ...later]) {
      _require(
        await tx.hasUnknownResident(range, key) &&
            await tx.getCache(range, key) == null,
        'pagination/reopen never resolves Unknown or publishes a cached value',
      );
    }
    _require(
      !await tx.hasUnknownResident(range, firstPage.last),
      'removed boundary stays removed across reopen',
    );
  });
}

bool _equal(List<int>? a, List<int> b) =>
    a != null &&
    a.length == b.length &&
    List.generate(a.length, (i) => a[i] == b[i]).every((v) => v);
void _require(bool ok, String contract) {
  if (!ok) throw StateError(contract);
}
