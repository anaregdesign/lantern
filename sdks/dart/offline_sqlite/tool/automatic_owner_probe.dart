// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
// Finite host-only native ownership qualification. No physical device claims.
import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_client_offline_sqlite/src/owner_native.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

const _timeout = Duration(seconds: 30);
void require(bool condition, String label) {
  if (!condition) throw StateError(label);
}

Future<(SqliteOfflineStore, OfflineAutomaticOwner)> open(String path) async {
  final store = await SqliteOfflineStore.openAutomatic(
    path: path,
    databaseFactory: databaseFactoryFfiNoIsolate,
  );
  try {
    final owner = await store.acquireAutomaticOwner(
      scope: 'scope',
      bindingFingerprint: 'binding',
      configurationFingerprint: 'configuration',
    );
    return (store, owner);
  } catch (_) {
    await store.close();
    rethrow;
  }
}

Future<void> contender((String, SendPort) input) async {
  final commands = ReceivePort();
  input.$2.send(commands.sendPort);
  NativeOfflineOwner? guard;
  await for (final command in commands) {
    if (command == 'acquire') {
      try {
        guard = NativeOfflineOwner.acquire(input.$1);
        input.$2.send('owned');
      } on OfflineOwnerConflictException {
        input.$2.send('conflict');
      }
    } else if (command == 'release') {
      guard?.release();
      input.$2.send('released');
      commands.close();
    } else if (command == 'exit') {
      // Deliberately leave the native guard pinned when this isolate ends.
      commands.close();
      Isolate.exit(input.$2, 'exited');
    }
  }
}

Future<String> otherProcess(String path) async {
  final result = await Process.run(Platform.resolvedExecutable, [
    'run',
    Platform.script.toFilePath(),
    'try',
    path,
  ]).timeout(_timeout);
  require(result.exitCode == 0, 'child exit: ${result.stderr}');
  return (result.stdout as String).trim().split('\n').last;
}

Future<void> main(List<String> args) async {
  sqfliteFfiInit();
  if (args.isNotEmpty && args.first == 'try') {
    try {
      final guard = NativeOfflineOwner.acquire(args[1]);
      guard.release();
      stdout.writeln('acquired');
    } on OfflineOwnerConflictException {
      stdout.writeln('conflict');
    }
    return;
  }
  if (args.isNotEmpty && args.first == 'hold') {
    final (store, owner) = await open(args[1]);
    await seedReceipt(store);
    stdout.writeln('epoch:${owner.epoch}');
    await stdin.drain<void>();
    return;
  }
  final root = await Directory.systemTemp.createTemp('lantern-owner-');
  final results = <String, bool>{};
  try {
    final path = '${root.path}/race.db';
    final ports = [ReceivePort(), ReceivePort()];
    final streams = [for (final p in ports) StreamIterator<Object?>(p)];
    final isolates = [
      for (final p in ports) await Isolate.spawn(contender, (path, p.sendPort)),
    ];
    final commands = <SendPort>[];
    for (final s in streams) {
      require(await s.moveNext().timeout(_timeout), 'barrier ready');
      commands.add(s.current! as SendPort);
    }
    for (final c in commands) {
      c.send('acquire');
    }
    final outcomes = <Object?>[];
    for (final s in streams) {
      require(await s.moveNext().timeout(_timeout), 'barrier result');
      outcomes.add(s.current);
    }
    require(
      outcomes.where((o) => o == 'owned').length == 1 &&
          outcomes.where((o) => o == 'conflict').length == 1,
      'one isolate winner',
    );
    require(await otherProcess(path) == 'conflict', 'loser close retains lock');
    final alias = '${root.path}/alias';
    await Link(alias).create(root.path);
    require(
      await otherProcess('$alias/race.db') == 'conflict',
      'directory alias',
    );
    results['O1_native_isolate_barrier_loser_close_alias'] = true;
    for (final c in commands) {
      c.send('release');
    }
    for (final s in streams) {
      await s.moveNext().timeout(_timeout);
      await s.cancel();
    }
    for (final p in ports) {
      p.close();
    }
    for (final i in isolates) {
      i.kill();
    }
    require(await otherProcess(path) == 'acquired', 'released guard');

    final (store, owner) = await open(path);
    require(owner.epoch == 1, 'first durable epoch');
    final databaseAlias = '${root.path}/db-alias';
    await Link(databaseAlias).create(path);
    require(
      await otherProcess(databaseAlias) == 'conflict',
      'existing DB alias',
    );
    await owner.release();
    await store.close();
    final (reopened, next) = await open(databaseAlias);
    require(next.epoch == 2, 'monotone epoch across alias');
    await next.release();
    await reopened.close();
    results['O1_durable_epoch_symlink'] = true;

    final hard = '${root.path}/hard.db';
    await Process.run('ln', [
      path,
      hard,
    ]).then((r) => require(r.exitCode == 0, 'hardlink'));
    var rejected = false;
    try {
      NativeOfflineOwner.acquire(hard);
    } on OfflineArgumentException {
      rejected = true;
    }
    require(rejected, 'DB hardlink rejected');
    await File(hard).delete();
    final lockLink = '${root.path}/lock-link';
    await Process.run('ln', [
      '$path.lantern-owner',
      lockLink,
    ]).then((r) => require(r.exitCode == 0, 'sidecar hardlink'));
    rejected = false;
    try {
      NativeOfflineOwner.acquire(path);
    } on OfflineArgumentException {
      rejected = true;
    }
    require(rejected, 'sidecar hardlink rejected');
    await File(lockLink).delete();
    final one = NativeOfflineOwner.acquire(path);
    final two = NativeOfflineOwner.acquire('${root.path}/different.db');
    one.release();
    two.release();
    results['O2_hardlinks_rejected_distinct_databases'] = true;

    final orphanPath = '${root.path}/orphan.db';
    final port = ReceivePort();
    final stream = StreamIterator<Object?>(port);
    final isolated = await Isolate.spawn(contender, (
      orphanPath,
      port.sendPort,
    ));
    await stream.moveNext().timeout(_timeout);
    final command = stream.current! as SendPort;
    command.send('acquire');
    await stream.moveNext().timeout(_timeout);
    require(stream.current == 'owned', 'orphan acquisition');
    command.send('exit');
    await stream.moveNext().timeout(_timeout);
    require(stream.current == 'exited', 'isolate exit acknowledged');
    rejected = false;
    try {
      NativeOfflineOwner.acquire(orphanPath);
    } on OfflineOwnerConflictException {
      rejected = true;
    }
    require(
      rejected && await otherProcess(orphanPath) == 'conflict',
      'orphan remains held',
    );
    await stream.cancel();
    port.close();
    isolated.kill();
    results['O4_isolate_exit_keeps_native_guard'] = true;

    final killedPath = '${root.path}/killed.db';
    final child = await Process.start(Platform.resolvedExecutable, [
      'run',
      Platform.script.toFilePath(),
      'hold',
      killedPath,
    ]);
    final stderr = child.stderr.transform(utf8.decoder).join();
    final line = await child.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .firstWhere((s) => s.startsWith('epoch:'))
        .timeout(_timeout);
    require(line == 'epoch:1', 'child durable epoch');
    require(await otherProcess(killedPath) == 'conflict', 'live child held');
    child.kill(ProcessSignal.sigkill);
    await child.exitCode.timeout(_timeout);
    await stderr;
    final (adopted, adoptedOwner) = await open(killedPath);
    require(adoptedOwner.epoch == 2, 'process death adopts stale active epoch');
    final original = await adopted.transaction(
      (tx) => tx.getOutbox('scope', 'receipt-record'),
    );
    require(
      original?.receipt?.operationId == evidence().operationId &&
          original?.mayHaveDispatched == true,
      'original uncertainty survives process death',
    );
    final remote = _OriginalReceiptOnly();
    final delivery = OfflineMutationDelivery(
      store: adopted,
      partitionId: 'scope',
      generation: 0,
      remote: remote,
      mutationRemote: remote,
      clock: () => probeTime.add(const Duration(seconds: 1)),
      fence: adoptedOwner.checkCurrent,
      onHandling: (_, _) {},
      onStorageBlocked: () => throw StateError('storage'),
      authorityBinding: 'binding',
      onDurable: (_) {},
    );
    await delivery.dispatchPass(limit: 1);
    require(
      remote.lookups == 1,
      'restart status-first, no preparation or send',
    );
    final operation = await adopted.transaction(
      (tx) => tx.getOperation('scope', 'receipt-operation'),
    );
    require(
      operation?.items.single.state == OfflineWriteState.confirmed,
      'original receipt reconciled',
    );
    await delivery.close();
    await adoptedOwner.release();
    await adopted.close();
    results['O4_process_exit_stale_epoch_adoption'] = true;
    stdout.writeln(
      jsonEncode({'outcome': 'PASS', 'hostOnly': true, 'fixtures': results}),
    );
  } finally {
    await root.delete(recursive: true);
  }
}

final probeTime = DateTime.utc(2026, 10, 14);
Uint8List bytes(int length, int value) =>
    Uint8List.fromList(List.filled(length, value));
OfflineReceiptEvidence evidence() {
  final epoch = ReceiptEpoch(bytes(16, 1));
  final id = Uint8List(49)..[0] = 1;
  id.setRange(1, 17, epoch.bytes);
  var millis = probeTime.millisecondsSinceEpoch;
  for (var i = 24; i >= 17; i--) {
    id[i] = millis & 255;
    millis >>= 8;
  }
  id.fillRange(25, 49, 6);
  return OfflineReceiptEvidence(
    operationId: ReceiptOperationId(id),
    groupId: ReceiptGroupId(bytes(16, 5)),
    endpoint: ReceiptEndpoint(nodeId: bytes(16, 2), generation: bytes(16, 3)),
    mutation: ReceiptMutationKind.edgeDelete,
    policy: OfflineReceiptPolicy(
      deploymentEpoch: epoch,
      retention: const Duration(hours: 24),
      maxEntries: BigInt.from(1024),
      maxBytes: BigInt.from(1048576),
      fingerprint: bytes(32, 4),
    ),
    itemIndex: 0,
    itemCount: 1,
    mayHaveDispatched: true,
    state: OfflineReceiptReconciliationState.statusRequired,
  );
}

Future<void> seedReceipt(SqliteOfflineStore store) =>
    store.transaction((tx) async {
      await tx.bindAuthority('scope', 'binding');
      final row = await tx.enqueue(
        OfflineOutboxRecord(
          recordId: 'receipt-record',
          operationId: 'receipt-operation',
          itemIndex: 0,
          partitionId: 'scope',
          intent: OfflineDeleteEdgeIntent(const EdgeRef('tail', 'head')),
          enqueuedAt: probeTime,
          ordinal: 0,
          state: OfflineOutboxState.enqueued,
          attemptCount: 1,
          generation: 0,
          receipt: evidence(),
          mayHaveDispatched: true,
          nextAttemptAt: probeTime.add(const Duration(seconds: 1)),
          diagnosticCode: 'unavailable',
        ),
      );
      await tx.putOperation(
        OfflineOperationRecord(
          partitionId: 'scope',
          generation: 0,
          operationId: row.operationId,
          items: [
            OfflineWriteStatus(
              recordId: row.recordId,
              operationId: row.operationId,
              itemIndex: 0,
              state: OfflineWriteState.retryScheduled,
              attemptCount: 1,
              diagnosticCode: 'unavailable',
            ),
          ],
          updatedAt: probeTime,
        ),
      );
    });

final class _OriginalReceiptOnly
    implements OfflineReceiptRemote, OfflineBoundMutationRemote {
  int lookups = 0;
  @override
  Future<OfflineReceiptStatus> getReceiptStatus(
    ReceiptOperationId id, {
    LanternCancellationToken? cancellation,
  }) async {
    final original = evidence();
    require(id == original.operationId, 'no replacement receipt');
    lookups++;
    return OfflineReceiptStatus(
      operationId: id,
      state: ReceiptStatusState.confirmed,
      groupId: original.groupId,
      mutation: original.mutation,
      itemIndex: 0,
      itemCount: 1,
      result: const OfflineEdgeDeleteReceiptResult(true),
    );
  }

  @override
  dynamic noSuchMethod(Invocation invocation) =>
      throw StateError('unexpected preparation/send');
}
