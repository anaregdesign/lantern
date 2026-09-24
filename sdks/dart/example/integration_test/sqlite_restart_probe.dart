// Opt-in app entrypoint for a host-controlled physical process restart probe.
// Build once with a fresh lower-case hex LANTERN_SQLITE_RESTART_PROBE_RUN
// (16–64 characters), install once, and launch the same binary three times.
// Wait for SQLITE_RESTART_PROBE_READY1, force-stop/kill without clearing data,
// relaunch, wait for SQLITE_RESTART_PROBE_READY2, kill again, then relaunch and
// require SQLITE_RESTART_PROBE_PASS. Do not use hot restart or reinstall.
//
// Example build target:
// flutter build apk --profile --target integration_test/sqlite_restart_probe.dart
//   --dart-define=LANTERN_SQLITE_RESTART_PROBE_RUN=<fresh-run-hex>
// This intentionally is not named *_test.dart: ordinary CI must never wait for
// an external process kill. It uses no test HTTP override or network endpoint.
import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:path/path.dart' as paths;
import 'package:sqflite/sqflite.dart' as sqlite;

const _userA = 'fixture-user-a';
const _userB = 'fixture-user-b';
const _vertexKey = 'fixture-pending-vertex';
const _cacheKey = 'fixture-confirmed-vertex';
const _edge = EdgeRef('fixture-tail', 'fixture-head');
const _vertexOperation = 'fixture-vertex-operation';
const _edgeOperation = 'fixture-edge-operation';
const _ttl = Duration(hours: 2);
final _status = ValueNotifier<String>('Starting restart probe');
var _launchPhase = 0;

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(
    MaterialApp(
      home: Scaffold(
        body: Center(
          child: ValueListenableBuilder<String>(
            valueListenable: _status,
            builder: (_, status, _) => Text(status),
          ),
        ),
      ),
    ),
  );
  unawaited(
    _run().catchError((Object error) {
      final code = error is _ProbeFailure ? error.code : 'unexpected_failure';
      _status.value = 'Restart probe failed: $code';
      _emit('SQLITE_RESTART_PROBE_FAIL phase=$_launchPhase code=$code');
    }),
  );
}

Future<void> _run() async {
  const run = String.fromEnvironment('LANTERN_SQLITE_RESTART_PROBE_RUN');
  _require(RegExp(r'^[a-f0-9]{16,64}$').hasMatch(run), 'run_opt_in_required');
  // This target never opens the example's application database, deletes a
  // directory, or resets an old probe. Each fresh run owns a distinct fixture.
  final root = Directory(
    paths.join(
      await sqlite.getDatabasesPath(),
      'lantern-sqlite-restart-probe-$run',
    ),
  );
  await root.create(recursive: true);
  final control = await sqlite.openDatabase(
    paths.join(root.path, 'probe-control.db'),
    version: 1,
    singleInstance: false,
    onConfigure: (database) async {
      await database.rawQuery('PRAGMA busy_timeout = 5000');
      await database.execute('PRAGMA synchronous = FULL');
    },
    onCreate: (database, _) async {
      await database.execute(
        'CREATE TABLE probe_state ('
        'id INTEGER PRIMARY KEY CHECK(id=1), phase INTEGER NOT NULL, '
        'started_at INTEGER NOT NULL, last_pid INTEGER NOT NULL)',
      );
      await database.insert('probe_state', {
        'id': 1,
        'phase': 0,
        'started_at': DateTime.now().toUtc().microsecondsSinceEpoch,
        'last_pid': 0,
      });
    },
  );
  final state = await control.query('probe_state');
  _require(state.length == 1, 'control_shape');
  final row = state.single;
  final completed = row['phase'];
  final startedMicros = row['started_at'];
  final lastPid = row['last_pid'];
  _require(
    completed is int && completed >= 0 && completed <= 2,
    'fresh_run_required',
  );
  _require(startedMicros is int && lastPid is int, 'control_shape');
  _launchPhase = (completed as int) + 1;
  _require(completed == 0 || lastPid != pid, 'process_not_restarted');
  final startedAt = DateTime.fromMicrosecondsSinceEpoch(
    startedMicros as int,
    isUtc: true,
  );
  final expiration = startedAt.add(_ttl);
  _require(DateTime.now().toUtc().isBefore(expiration), 'fixture_ttl_elapsed');

  final store = await SqliteOfflineStore.open(
    path: paths.join(root.path, 'offline.db'),
  );
  final repository = OfflineLanternRepository(
    store: store,
    remote: const _NoNetwork(),
    config: OfflineConfig(
      // Only initial admission uses the persisted creation sample. Later
      // launches use the real clock and compare to the original expiration.
      clock: completed == 0 ? () => startedAt : () => DateTime.now().toUtc(),
    ),
  );
  if (completed == 0) {
    await _requireEmpty(store, _userA, generation: 0);
    await _requireEmpty(store, _userB, generation: 0);
    for (final partition in [_userA, _userB]) {
      await repository.putVertex(
        partitionId: partition,
        operationId: _vertexOperation,
        input: VertexInput(
          key: _vertexKey,
          value: VertexValue.int64(partition == _userA ? -7 : 42),
          expiresIn: _ttl,
        ),
      );
      await repository.putEdge(
        partitionId: partition,
        operationId: _edgeOperation,
        input: EdgeInput(
          tail: _edge.tail,
          head: _edge.head,
          weight: partition == _userA ? 0.5 : 0.75,
          expiresIn: _ttl,
        ),
      );
      await store.transaction(
        (transaction) => transaction.putCache(
          partition,
          OfflineCacheRecord.value(
            partitionId: partition,
            generation: 0,
            key: const OfflineEntityKey.vertex(_cacheKey),
            entity: Vertex(
              key: _cacheKey,
              value: VertexValue.boolean(true),
              expiration: null,
            ),
            validatedAt: startedAt,
            lastAccessAt: startedAt,
          ),
        ),
      );
    }
    await _requirePending(store, repository, _userA, expiration);
    await _requirePending(store, repository, _userB, expiration);
    await _mark(control, 1);
    _ready('SQLITE_RESTART_PROBE_READY1', 'Pending writes committed; kill app');
    // No close/dispose occurs before the host kills this process. The live
    // Flutter app keeps both databases open, with no outstanding transaction.
    return;
  }
  if (completed == 1) {
    await _requirePending(store, repository, _userA, expiration);
    await _requirePending(store, repository, _userB, expiration);
    await repository.wipePartition(_userA);
    await _requireEmpty(store, _userA, generation: 1);
    // The same logical keys in the next user's partition retain distinct
    // values, pending operations, and confirmed cache across account rotation.
    await _requirePending(store, repository, _userB, expiration);
    await _mark(control, 2);
    _ready('SQLITE_RESTART_PROBE_READY2', 'Logout committed; kill app again');
    return;
  }
  await _requireEmpty(store, _userA, generation: 1);
  await _requirePending(store, repository, _userB, expiration);
  _require(
    (await repository.readVertex(
          _userA,
          _vertexKey,
          policy: OfflineReadPolicy.cacheOnly,
        )).value ==
        null,
    'wiped_overlay_visible',
  );
  _require(
    (await repository.readEdge(
          _userA,
          _edge,
          policy: OfflineReadPolicy.cacheOnly,
        )).value ==
        null,
    'wiped_edge_visible',
  );
  await _mark(control, 3);
  await repository.dispose();
  await store.close();
  await control.close();
  _status.value = 'Restart probe passed';
  _emit(
    'SQLITE_RESTART_PROBE_PASS pending_reopen=true ttl_preserved=true '
    'logout_wipe=true user_switch_isolated=true',
  );
}

Future<void> _mark(sqlite.Database control, int phase) =>
    control.transaction((transaction) async {
      final updated = await transaction.update(
        'probe_state',
        {'phase': phase, 'last_pid': pid},
        where: 'id=1 AND phase=?',
        whereArgs: [phase - 1],
      );
      _require(updated == 1, 'phase_transition');
    });

Future<void> _requireEmpty(
  SqliteOfflineStore store,
  String partition, {
  required int generation,
}) => store.transaction((transaction) async {
  _require(
    await transaction.generation(partition) == generation,
    'partition_generation',
  );
  _require((await transaction.outbox(partition)).isEmpty, 'outbox_not_empty');
  _require(
    (await transaction.operations(partition)).isEmpty,
    'operations_not_empty',
  );
  _require(
    await transaction.getCache(
          partition,
          const OfflineEntityKey.vertex(_cacheKey),
        ) ==
        null,
    'cache_not_empty',
  );
  _require(
    (await transaction.changeCursor(partition)).sequences.isEmpty,
    'cursor_not_empty',
  );
});

Future<void> _requirePending(
  SqliteOfflineStore store,
  OfflineLanternRepository repository,
  String partition,
  DateTime expiration,
) async {
  final vertex = await repository.readVertex(
    partition,
    _vertexKey,
    policy: OfflineReadPolicy.cacheOnly,
  );
  final edge = await repository.readEdge(
    partition,
    _edge,
    policy: OfflineReadPolicy.cacheOnly,
  );
  _require(
    vertex.hasPendingWrites && edge.hasPendingWrites,
    'pending_overlay_missing',
  );
  _require(
    vertex.value?.expiration == expiration &&
        edge.value?.expiration == expiration,
    'original_ttl_changed',
  );
  _require(
    vertex.value?.value is Int64Value &&
        (vertex.value!.value as Int64Value).value ==
            (partition == _userA ? -7 : 42),
    'vertex_value_changed',
  );
  _require(
    edge.value?.weight == (partition == _userA ? 0.5 : 0.75),
    'edge_value_changed',
  );
  for (final operation in [_vertexOperation, _edgeOperation]) {
    final status = await repository.getWriteStatus(partition, operation);
    _require(
      status?.items.length == 1 &&
          status!.items.single.state == OfflineWriteState.locallyCommitted &&
          status.items.single.attemptCount == 0,
      'operation_changed',
    );
  }
  await store.transaction((transaction) async {
    _require(
      await transaction.generation(partition) == 0,
      'sibling_generation_changed',
    );
    final outbox = await transaction.outbox(partition);
    _require(
      outbox.length == 2 &&
          outbox.every(
            (record) =>
                record.absoluteExpiration == expiration &&
                record.state == OfflineOutboxState.enqueued &&
                record.attemptCount == 0,
          ),
      'durable_outbox_changed',
    );
    _require(
      await transaction.getCache(
            partition,
            const OfflineEntityKey.vertex(_cacheKey),
          ) !=
          null,
      'confirmed_cache_missing',
    );
  });
}

void _ready(String marker, String visible) {
  _status.value = visible;
  _emit(marker);
}

void _emit(String marker) {
  // Markers deliberately contain no database path, keys, values, credentials,
  // run identifier, device identity, or raw exception content.
  // ignore: avoid_print
  print(marker);
}

void _require(bool condition, String code) {
  if (!condition) throw _ProbeFailure(code);
}

final class _ProbeFailure implements Exception {
  const _ProbeFailure(this.code);
  final String code;
}

final class _NoNetwork implements OfflineRemote {
  const _NoNetwork();
  Never _unexpected() => throw const _ProbeFailure('unexpected_network');
  @override
  Future<void> probe({LanternCancellationToken? cancellation}) async =>
      _unexpected();
  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async => _unexpected();
  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) async => _unexpected();
  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async => _unexpected();
  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async => _unexpected();
}
