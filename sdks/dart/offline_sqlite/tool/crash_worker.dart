import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

const _partition = 'crash-probe';
const _receiptPartition = 'receipt-crash-probe';
const _origin = '00112233445566778899aabbccddeeff';
const _keys = ['first', 'last', 'untouched', 'pending-a', 'pending-b'];
final _now = DateTime.utc(2026, 9, 24);

/// Internal subprocess for crash_probe.dart; never prints stored content.
Future<void> main(List<String> arguments) async {
  try {
    if (arguments.length != 4) throw StateError('arguments');
    final [mode, scenario, boundary, path] = arguments;
    if (!['crash', 'verify'].contains(mode) ||
        ![
          'schema',
          'enqueue',
          'claim',
          'confirmation',
          'cursor-chunk',
          'cursor-final',
          'checkpoint-reset',
          'wipe',
          'receipt',
        ].contains(scenario) ||
        !['before', 'after'].contains(boundary) ||
        (scenario == 'receipt' && boundary != 'after')) {
      throw StateError('arguments');
    }
    sqfliteFfiInit();
    if (scenario == 'receipt' && mode == 'crash') {
      await _crashReceipt(path);
    } else if (scenario == 'receipt') {
      await _verifyReceipt(path);
    } else if (mode == 'crash') {
      await _crash(path, scenario, boundary);
    } else {
      await _verify(path, scenario, boundary);
    }
    if (mode == 'verify') {
      stdout.writeln(jsonEncode({'event': 'verified'}));
      await stdout.flush();
    }
  } catch (error) {
    stderr.writeln('crash_worker_failed:${error.runtimeType}');
    exitCode = 1;
  }
}

Future<void> _crash(String path, String scenario, String boundary) async {
  final store = await SqliteOfflineStore.open(
    path: path,
    databaseFactory: scenario == 'schema' && boundary == 'before'
        ? _SchemaBarrierFactory(databaseFactoryFfi)
        : databaseFactoryFfi,
  );
  // Schema creation has committed by the time open returns.
  if (scenario == 'schema') await _readyAndHold();
  await _seed(store, scenario);
  await store.transaction((transaction) async {
    await _mutate(transaction, scenario);
    // All writes have reached SQLite, but the transaction callback has not
    // returned. SIGKILL must recover the entire previously committed state.
    if (boundary == 'before') await _readyAndHold();
  });
  // The database remains open: this probes acknowledged commit durability,
  // without letting a graceful close checkpoint or otherwise flush the file.
  await _readyAndHold();
}

Future<void> _readyAndHold() async {
  final keepAlive = Timer.periodic(const Duration(seconds: 1), (_) {});
  stdout.writeln(jsonEncode({'event': 'ready', 'pid': pid}));
  await stdout.flush();
  try {
    await Completer<void>().future;
  } finally {
    keepAlive.cancel();
  }
}

Future<void> _verify(String path, String scenario, String boundary) async {
  if (scenario == 'schema') {
    // Inspect before the production open can create a missing schema. A killed
    // onCreate callback must leave neither its tables nor its user_version.
    final raw = await databaseFactoryFfi.openDatabase(
      path,
      options: OpenDatabaseOptions(singleInstance: false),
    );
    try {
      final tables = await raw.rawQuery(
        "SELECT name FROM sqlite_master WHERE type='table' "
        "AND name NOT LIKE 'sqlite_%' AND name != 'android_metadata'",
      );
      final version = await raw.getVersion();
      _require(
        boundary == 'before'
            ? tables.isEmpty && version == 0
            : tables.isNotEmpty && version == 4,
      );
    } finally {
      await raw.close();
    }
  }
  final reference = InMemoryOfflineStore();
  if (scenario != 'schema') {
    await _seed(reference, scenario);
    if (boundary == 'after') {
      await reference.transaction(
        (transaction) => _mutate(transaction, scenario),
      );
    }
  }
  final store = await SqliteOfflineStore.open(
    path: path,
    databaseFactory: databaseFactoryFfi,
  );
  try {
    _require(await _snapshot(store) == await _snapshot(reference));
    if (scenario == 'cursor-chunk') {
      // Reopening must retain partial chunk progress, although the public
      // last-applied cursor is still unchanged until the final chunk.
      await _finishChunk(store, committed: boundary == 'after');
      await _finishChunk(reference, committed: boundary == 'after');
      _require(await _snapshot(store) == await _snapshot(reference));
    }
  } finally {
    await store.close();
  }
}

Future<void> _crashReceipt(String path) async {
  final token = _receiptToken();
  final direct = _receiptClient(
    _receiptEndpoint('LANTERN_DART_RECEIPT_ENDPOINT'),
    token,
  );
  final remote = _receiptClient(
    _receiptEndpoint('LANTERN_DART_RECEIPT_PROXY_ENDPOINT'),
    token,
  );
  final store = await SqliteOfflineStore.open(
    path: path,
    databaseFactory: databaseFactoryFfi,
  );
  final repository = OfflineLanternRepository(
    store: store,
    remote: LanternClientOfflineRemote(remote),
    config: OfflineConfig(
      maxConcurrency: 1,
      maxConcurrencyPerPartition: 1,
      baseRetryDelay: const Duration(minutes: 5),
      maxRetryDelay: const Duration(minutes: 5),
      jitter: (ceiling) => ceiling,
    ),
  );
  try {
    final capability = await direct.getReceiptCapability();
    if (capability is! ReceiptCapabilityEnabled) {
      throw StateError('receipt_capability_disabled');
    }
    _require(
      const <ReceiptMutationKind>{
        ReceiptMutationKind.vertexPut,
        ReceiptMutationKind.vertexDelete,
        ReceiptMutationKind.edgeDelete,
        ReceiptMutationKind.edgeAdd,
      }.every(capability.supports),
    );

    final prefix = 'receipt-crash:${DateTime.now().microsecondsSinceEpoch}:$pid:';
    final putKey = '${prefix}conditional';
    final deleteKey = '${prefix}delete-vertex';
    // Per-key FIFO would hold Add behind an ambiguous Delete of that same edge.
    final deleteEdge = EdgeRef('${prefix}delete-tail', '${prefix}delete-head');
    final addEdge = EdgeRef('${prefix}add-tail', '${prefix}add-head');
    _require(
      await direct.putVertex(
            VertexInput(key: putKey, value: VertexValue.string('original')),
          ) ==
          PutOutcome.appliedAndLive,
    );
    _require(
      await direct.putVertex(
            VertexInput(key: deleteKey, value: VertexValue.string('delete')),
          ) ==
          PutOutcome.appliedAndLive,
    );
    _require(
      await direct.putEdge(
            EdgeInput(tail: deleteEdge.tail, head: deleteEdge.head, weight: 2),
          ) ==
          PutOutcome.appliedAndLive,
    );
    _require(
      await direct.putEdge(
            EdgeInput(tail: addEdge.tail, head: addEdge.head, weight: 3),
          ) ==
          PutOutcome.appliedAndLive,
    );
    // The receipt Add must follow a real Delete of its edge.
    _require(await direct.deleteEdge(addEdge));
    await _requireEdgeMissing(direct, addEdge);

    await repository.putVertexIfAbsent(
      partitionId: _receiptPartition,
      operationId: '${prefix}put',
      input: VertexInput(key: putKey, value: VertexValue.string('replacement')),
    );
    await repository.deleteVertex(
      partitionId: _receiptPartition,
      operationId: '${prefix}delete-vertex',
      key: deleteKey,
    );
    await repository.deleteEdge(
      partitionId: _receiptPartition,
      operationId: '${prefix}delete-edge',
      edge: deleteEdge,
    );
    await repository.addEdge(
      partitionId: _receiptPartition,
      operationId: '${prefix}add',
      input: EdgeInput(
        tail: addEdge.tail,
        head: addEdge.head,
        weight: 4,
        contribId: Uint8List(24)..[23] = 1,
      ),
    );
    final prepared = await store.transaction(
      (transaction) => transaction.outbox(_receiptPartition),
    );
    _require(
      prepared.length == 4 &&
          prepared.every(
            (record) =>
                record.receipt != null &&
                record.receipt!.endpoint == capability.endpoint &&
                record.receipt!.policy.deploymentEpoch ==
                    capability.policy.deploymentEpoch &&
                record.receipt!.itemIndex == 0 &&
                record.receipt!.itemCount == 1 &&
                record.attemptCount == 0 &&
                record.state == OfflineOutboxState.enqueued,
          ) &&
          prepared.map((record) => record.receipt!.operationId).toSet().length ==
              4 &&
          prepared.map((record) => record.receipt!.groupId).toSet().length ==
              4 &&
          prepared.map((record) => record.receipt!.mutation).toSet().length == 4,
    );

    _require(await repository.drain(_receiptPartition) == 0);
    final unresolved = await store.transaction(
      (transaction) => transaction.outbox(_receiptPartition),
    );
    _require(
      unresolved.length == 4 &&
          unresolved.every(
            (record) =>
                record.state == OfflineOutboxState.enqueued &&
                record.attemptCount == 1 &&
                record.receipt!.state ==
                    OfflineReceiptReconciliationState.statusRequired &&
                record.diagnosticCode == 'receipt_response_unknown',
          ),
    );
    _require((await direct.getEdge(addEdge)).weight == 4);
    _require(await direct.deleteEdge(addEdge));
    await _requireEdgeMissing(direct, addEdge);
    await _readyAndHold();
  } finally {
    await repository.dispose();
    await store.close();
    await remote.close();
    await direct.close();
  }
}

Future<void> _verifyReceipt(String path) async {
  final token = _receiptToken();
  final direct = _receiptClient(
    _receiptEndpoint('LANTERN_DART_RECEIPT_ENDPOINT'),
    token,
  );
  final remote = _receiptClient(
    _receiptEndpoint('LANTERN_DART_RECEIPT_PROXY_ENDPOINT'),
    token,
  );
  final store = await SqliteOfflineStore.open(
    path: path,
    databaseFactory: databaseFactoryFfi,
  );
  final repository = OfflineLanternRepository(
    store: store,
    remote: LanternClientOfflineRemote(remote),
    config: OfflineConfig(
      // Virtual time clears the durable backoff without waiting five minutes.
      clock: () => DateTime.now().toUtc().add(const Duration(minutes: 6)),
      maxConcurrency: 1,
      maxConcurrencyPerPartition: 1,
      jitter: (_) => Duration.zero,
    ),
  );
  try {
    final pending = await store.transaction(
      (transaction) => transaction.outbox(_receiptPartition),
    );
    _require(
      pending.length == 4 &&
          pending.every(
            (record) =>
                record.receipt != null &&
                record.state == OfflineOutboxState.enqueued &&
                record.attemptCount == 1 &&
                record.diagnosticCode == 'receipt_response_unknown',
          ),
    );
    for (final record in pending) {
      final status = await repository.getWriteStatus(
        _receiptPartition,
        record.operationId,
      );
      _require(
        status != null &&
            status.items.single.state == OfflineWriteState.retryScheduled &&
            status.items.single.attemptCount == 1,
      );
    }

    _require(await repository.drain(_receiptPartition) == 4);
    for (final record in pending) {
      final status = await repository.getWriteStatus(
        _receiptPartition,
        record.operationId,
      );
      _require(
        status != null &&
            status.items.single.state == OfflineWriteState.confirmed &&
            status.items.single.attemptCount == 1 &&
            status.items.single.diagnosticCode == null,
      );
      final result = status!.items.single.receiptResult;
      _require(switch ((record.intent, result)) {
        (
          OfflinePutVertexIfAbsentIntent(),
          OfflineVertexPutReceiptResult(
            outcome: PutOutcome.conditionNotMet,
          ),
        ) =>
          true,
        (
          OfflineDeleteVertexIntent(),
          OfflineVertexDeleteReceiptResult(existed: true),
        ) =>
          true,
        (
          OfflineDeleteEdgeIntent(),
          OfflineEdgeDeleteReceiptResult(existed: true),
        ) =>
          true,
        (
          OfflineReceiptAddEdgeIntent(),
          OfflineEdgeAddReceiptResult(effectiveWeight: 4.0),
        ) =>
          true,
        _ => false,
      });
    }
    _require(
      (await store.transaction(
        (transaction) => transaction.outbox(_receiptPartition),
      )).isEmpty,
    );
    final put = pending
        .map((record) => record.intent)
        .whereType<OfflinePutVertexIfAbsentIntent>()
        .single;
    final deletedVertex = pending
        .map((record) => record.intent)
        .whereType<OfflineDeleteVertexIntent>()
        .single;
    final deletedEdge = pending
        .map((record) => record.intent)
        .whereType<OfflineDeleteEdgeIntent>()
        .single;
    final add = pending
        .map((record) => record.intent)
        .whereType<OfflineReceiptAddEdgeIntent>()
        .single;
    final original = (await direct.getVertex(put.vertex.key)).value;
    _require(original is StringValue && original.value == 'original');
    await _requireVertexMissing(direct, deletedVertex.vertexKey);
    await _requireEdgeMissing(direct, deletedEdge.edge);
    await _requireEdgeMissing(direct, EdgeRef(add.edge.tail, add.edge.head));
  } finally {
    await repository.dispose();
    await store.close();
    await remote.close();
    await direct.close();
  }
}

Uri _receiptEndpoint(String name) {
  final value = Platform.environment[name];
  final endpoint = value == null ? null : Uri.tryParse(value);
  if (endpoint == null ||
      endpoint.scheme != 'http' ||
      !['127.0.0.1', 'localhost', '::1'].contains(endpoint.host) ||
      endpoint.userInfo.isNotEmpty) {
    throw StateError('receipt_endpoint');
  }
  return endpoint;
}

String _receiptToken() {
  final token = Platform.environment['LANTERN_DART_RECEIPT_TOKEN'];
  if (token == null || token.isEmpty) throw StateError('receipt_token');
  return token;
}

LanternClient _receiptClient(Uri endpoint, String token) =>
    LanternClient.connect(
      endpoint,
      allowInsecure: endpoint.scheme == 'http',
      token: token,
    );

Future<void> _requireVertexMissing(LanternClient client, String key) async {
  try {
    await client.getVertex(key);
  } on LanternNotFoundException {
    return;
  }
  throw StateError('receipt_vertex_resurrected');
}

Future<void> _requireEdgeMissing(LanternClient client, EdgeRef edge) async {
  try {
    await client.getEdge(edge);
  } on LanternNotFoundException {
    return;
  }
  throw StateError('receipt_edge_resurrected');
}

Future<void> _seed(OfflineStore store, String scenario) async {
  await store.transaction((transaction) async {
    // A second partition detects accidental cross-partition cleanup.
    await transaction.putCache('other', _cache('other', 'untouched'));
    if (scenario == 'enqueue') return;
    await transaction.resetChangeCursor(
      _partition,
      OfflineChangeCursor({_origin: BigInt.from(7)}),
    );
    for (final key in ['first', 'last', 'untouched']) {
      await transaction.putCache(_partition, _cache(_partition, key));
    }
    await _enqueue(transaction);
    if (scenario == 'confirmation') await _claim(transaction);
    if (scenario == 'cursor-final') {
      await transaction.applyChangeChunk(_partition, _chunk(0));
    }
  });
}

Future<void> _mutate(
  OfflineStoreTransaction transaction,
  String scenario,
) async {
  switch (scenario) {
    case 'enqueue':
      await _enqueue(transaction);
    case 'claim':
      await _claim(transaction);
    case 'confirmation':
      final records = await transaction.outbox(_partition);
      await _status(transaction, records, OfflineWriteState.confirmed);
      for (final record in records) {
        final vertex = (record.intent as OfflinePutVertexIntent).vertex;
        await transaction.putCache(
          _partition,
          OfflineCacheRecord.value(
            partitionId: _partition,
            generation: record.generation,
            key: record.intent.key,
            entity: vertex,
            validatedAt: _now,
            lastAccessAt: _now,
          ),
        );
        await transaction.deleteOutbox(_partition, record.recordId);
      }
    case 'cursor-chunk':
      await transaction.applyChangeChunk(_partition, _chunk(0));
    case 'cursor-final':
      await transaction.applyChangeChunk(_partition, _chunk(1));
    case 'checkpoint-reset':
      await transaction.resetChangeCursor(
        _partition,
        OfflineChangeCursor({_origin: BigInt.from(9)}),
      );
    case 'wipe':
      await transaction.wipePartition(_partition);
    default:
      throw StateError('scenario');
  }
}

Future<void> _enqueue(OfflineStoreTransaction transaction) async {
  final generation = await transaction.generation(_partition);
  final records = await transaction.enqueueAll([
    for (var index = 0; index < 2; index++)
      OfflineOutboxRecord(
        partitionId: _partition,
        generation: generation,
        recordId: 'record-$index',
        operationId: 'operation',
        itemIndex: index,
        ordinal: 0,
        intent: OfflinePutVertexIntent(
          Vertex(
            key: index == 0 ? 'pending-a' : 'pending-b',
            value: VertexValue.string('durable'),
            expiration: _now.add(const Duration(hours: 1)),
          ),
        ),
        enqueuedAt: _now,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
      ),
  ]);
  await _status(transaction, records, OfflineWriteState.locallyCommitted);
}

Future<void> _claim(OfflineStoreTransaction transaction) async {
  final records = await transaction.claim(
    _partition,
    owner: 'crash-worker',
    now: _now,
    maxAge: const Duration(days: 1),
    leaseDuration: const Duration(minutes: 1),
    limit: 2,
  );
  _require(records.length == 2);
  await _status(transaction, records, OfflineWriteState.sending);
}

Future<void> _status(
  OfflineStoreTransaction transaction,
  List<OfflineOutboxRecord> records,
  OfflineWriteState state,
) async {
  await transaction.putOperation(
    OfflineOperationRecord(
      partitionId: _partition,
      generation: records.first.generation,
      operationId: 'operation',
      items: [
        for (final record in records)
          OfflineWriteStatus(
            recordId: record.recordId,
            operationId: record.operationId,
            itemIndex: record.itemIndex,
            state: state,
            attemptCount: record.attemptCount,
          ),
      ],
      updatedAt: _now,
      terminalAt: state == OfflineWriteState.confirmed ? _now : null,
    ),
  );
}

OfflineCacheRecord _cache(String partition, String key) =>
    OfflineCacheRecord.value(
      partitionId: partition,
      generation: 0,
      key: OfflineEntityKey.vertex(key),
      entity: Vertex(
        key: key,
        value: VertexValue.string('cached'),
        expiration: null,
      ),
      validatedAt: _now,
      lastAccessAt: _now,
    );

OfflineChangeChunk _chunk(int index) => OfflineChangeChunk(
  origin: _origin,
  sequence: BigInt.from(8),
  chunkIndex: index,
  isLast: index == 1,
  keys: [OfflineEntityKey.vertex(index == 0 ? 'first' : 'last')],
);

Future<void> _finishChunk(OfflineStore store, {required bool committed}) async {
  try {
    await store.transaction(
      (transaction) => transaction.applyChangeChunk(_partition, _chunk(1)),
    );
    _require(committed);
  } on OfflineChangeGapException {
    _require(!committed);
  }
}

Future<String> _snapshot(OfflineStore store) => store.transaction((
  transaction,
) async {
  final partitions = <Object?>[];
  for (final partition in [_partition, 'other']) {
    final cache = <String?>[];
    for (final key in _keys) {
      final record = await transaction.getCache(
        partition,
        OfflineEntityKey.vertex(key),
      );
      cache.add(record == null ? null : OfflineCodec.encodeCacheRecord(record));
    }
    partitions.add({
      'generation': await transaction.generation(partition),
      'paused': await transaction.replayPausedForAuth(partition),
      'cache': cache,
      'outbox': (await transaction.outbox(
        partition,
      )).map(OfflineCodec.encodeOutboxRecord).toList(),
      'operations': (await transaction.operations(
        partition,
      )).map(OfflineCodec.encodeOperationRecord).toList(),
      'cursor': (await transaction.changeCursor(partition)).toJson(),
      'changeEpoch': await transaction.changeEpoch(partition),
      'unknownResidents': [
        for (final key in await transaction.unknownResidents(
          partition,
          limit: 128,
        ))
          key.canonical,
      ],
    });
  }
  return jsonEncode(partitions);
});

void _require(bool condition) {
  if (!condition) throw StateError('crash_state_mismatch');
}

/// Inserts a process barrier inside the real SQLite schema transaction.
final class _SchemaBarrierFactory implements DatabaseFactory {
  _SchemaBarrierFactory(this.inner);
  final DatabaseFactory inner;

  @override
  Future<Database> openDatabase(String path, {OpenDatabaseOptions? options}) {
    final original = options!;
    return inner.openDatabase(
      path,
      options: OpenDatabaseOptions(
        version: original.version,
        onConfigure: original.onConfigure,
        onCreate: (database, version) async {
          await original.onCreate!(database, version);
          await _readyAndHold();
        },
        onUpgrade: original.onUpgrade,
        onDowngrade: original.onDowngrade,
        onOpen: original.onOpen,
        readOnly: original.readOnly,
        singleInstance: original.singleInstance,
      ),
    );
  }

  @override
  Future<String> getDatabasesPath() => inner.getDatabasesPath();
  @override
  Future<void> setDatabasesPath(String path) => inner.setDatabasesPath(path);
  @override
  Future<void> deleteDatabase(String path) => inner.deleteDatabase(path);
  @override
  Future<bool> databaseExists(String path) => inner.databaseExists(path);
  @override
  Future<void> writeDatabaseBytes(String path, Uint8List bytes) =>
      inner.writeDatabaseBytes(path, bytes);
  @override
  Future<Uint8List> readDatabaseBytes(String path) =>
      inner.readDatabaseBytes(path);
}
