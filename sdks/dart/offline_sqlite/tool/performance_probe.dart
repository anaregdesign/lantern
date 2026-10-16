import 'dart:convert';
import 'dart:io';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import '../../offline/tool/automatic_baseline.dart';
import '../../offline/tool/automatic_diagnostics.dart';
import 'sql_diagnostics.dart';

/// Content-free host baseline, including logical WAL write amplification.
Future<void> main(List<String> arguments) async {
  if (arguments.isNotEmpty &&
      (arguments.length != 2 || arguments.first != '--diagnostic')) {
    throw ArgumentError('expected --diagnostic <sidecar>');
  }
  final diagnostics = arguments.isEmpty
      ? null
      : AutomaticDiagnostics(write: probeJsonWriter(File(arguments[1])));
  diagnostics?.emit('processStart', {
    'dart': Platform.version,
    'os': Platform.operatingSystem,
    'osVersion': Platform.operatingSystemVersion,
    'logicalProcessors': Platform.numberOfProcessors,
    'process': probeProcessCounters(),
  });
  if (diagnostics == null) {
    await _runProbe(null);
  } else {
    try {
      await _runProbe(diagnostics);
    } catch (_) {
      // SQL exceptions can contain arguments. The sidecar has the bounded
      // phase evidence; never export raw errors or stack traces in this mode.
      diagnostics.emit('processFailure', {
        'timeoutLatched': diagnostics.timedOut,
      });
      exitCode = 1;
    }
  }
}

Future<void> _runProbe(AutomaticDiagnostics? diagnostics) async {
  sqfliteFfiInit();
  final directory = await Directory.systemTemp.createTemp('sqlite-baseline-');
  final path = '${directory.path}/store.db';
  const count = 100;
  final writes = <int>[];
  final reads = <int>[];
  var payloadBytes = 0;
  final factory = _WalFactory(databaseFactoryFfi);
  SqliteOfflineStore? store;
  OfflineLanternRepository? repository;
  try {
    store = await SqliteOfflineStore.open(path: path, databaseFactory: factory);
    final initialWalBytes = await _size('$path-wal');
    final now = DateTime.utc(2026, 9, 24);
    for (var index = 0; index < count; index++) {
      final record = OfflineCacheRecord.value(
        partitionId: 'baseline',
        generation: 0,
        key: OfflineEntityKey.vertex('key-$index'),
        entity: Vertex(
          key: 'key-$index',
          value: VertexValue.string('x' * 1024),
          expiration: null,
        ),
        validatedAt: now,
        lastAccessAt: now,
      );
      payloadBytes += utf8
          .encode(OfflineCodec.encodeCacheRecord(record))
          .length;
      final stopwatch = Stopwatch()..start();
      await store.transaction((t) => t.putCache('baseline', record));
      writes.add(stopwatch.elapsedMicroseconds);
    }
    final walBytes = (await _size('$path-wal')) - initialWalBytes;
    for (var index = 0; index < count; index++) {
      final stopwatch = Stopwatch()..start();
      final record = await store.transaction(
        (t) => t.getCache('baseline', OfflineEntityKey.vertex('key-$index')),
      );
      if (record == null) throw StateError('baseline_missing');
      reads.add(stopwatch.elapsedMicroseconds);
    }
    await store.close();
    final diskBytes = await _size(path);
    final restart = Stopwatch()..start();
    store = await SqliteOfflineStore.open(path: path, databaseFactory: factory);
    final restartUs = restart.elapsedMicroseconds;
    final lifecycleWalBefore = await _size('$path-wal');
    repository = OfflineLanternRepository(
      store: store,
      remote: _ImmediateRemote(),
      config: OfflineConfig(clock: () => now, jitter: (_) => Duration.zero),
    );
    final enqueue = Stopwatch()..start();
    final operation = await repository.putVertices(
      partitionId: 'lifecycle',
      inputs: List.generate(
        count,
        (index) => VertexInput(
          key: 'pending-$index',
          value: VertexValue.int32(index),
          expiresIn: const Duration(hours: 1),
        ),
      ),
    );
    enqueue.stop();
    final replay = Stopwatch()..start();
    final confirmed = await repository.drain('lifecycle');
    replay.stop();
    final status = await repository.getWriteStatus(
      'lifecycle',
      operation.operationId,
    );
    if (confirmed != count || status?.confirmedCount != count) {
      throw StateError('baseline_replay');
    }
    final lifecycleWalBytes = (await _size('$path-wal')) - lifecycleWalBefore;
    final result = {
      'contentFree': true,
      'platform': Platform.operatingSystem,
      'revision': Platform.environment['GITHUB_SHA'] ?? 'local-working-tree',
      'records': count,
      'journal': 'wal',
      'synchronous': 'full',
      'cache_write_us': _percentiles(writes),
      'cache_read_us': _percentiles(reads),
      'reopen_us': restartUs,
      'database_bytes': diskBytes,
      'database_bytes_per_record': diskBytes / count,
      'canonical_payload_bytes': payloadBytes,
      'wal_growth_bytes': walBytes,
      'wal_growth_per_payload_byte': walBytes / payloadBytes,
      'lifecycle_items': count,
      'enqueue_batch_us': enqueue.elapsedMicroseconds,
      'replay_batch_us': replay.elapsedMicroseconds,
      'replay_remote': 'immediate_stub',
      'lifecycle_wal_growth_bytes': lifecycleWalBytes,
      'rss_bytes': ProcessInfo.currentRss,
    };
    diagnostics?.emit('legacyControl', result);
    // Broad host regression budgets, fixed before the initial measurement.
    if (writes.any((us) => us > 1000000) ||
        reads.any((us) => us > 1000000) ||
        restartUs > 5000000 ||
        diskBytes > 8 * 1024 * 1024 ||
        walBytes > 64 * 1024 * 1024 ||
        enqueue.elapsedMilliseconds > 5000 ||
        replay.elapsedMilliseconds > 30000 ||
        lifecycleWalBytes > 128 * 1024 * 1024) {
      throw StateError('baseline_budget');
    }
    result['automatic'] = await _automaticBaseline(
      '${directory.path}/automatic.db',
      diagnostics,
    );
    if (diagnostics != null) result['qualifies'] = false;
    stdout.writeln(jsonEncode(result));
  } finally {
    if (diagnostics?.cleanupConfirmed ?? true) {
      await repository?.dispose();
      await store?.close();
      await directory.delete(recursive: true);
    } else {
      // The outer120s watchdog owns an unjoined failed process. Never delete
      // or release a live qualified database as a diagnostic shortcut.
      diagnostics!.emit('cleanupUnconfirmed', {});
    }
  }
}

final class _ImmediateRemote implements OfflineRemote {
  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) async => const OfflineRemoteMissing<Edge>();

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async => const OfflineRemoteMissing<Vertex>();

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) async {}

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async => PutOutcome.appliedAndLive;

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async => PutOutcome.appliedAndLive;
}

Map<String, int> _percentiles(List<int> values) {
  values.sort();
  return {
    for (final p in [50, 95, 99])
      'p$p': values[((values.length * p / 100).ceil() - 1)],
  };
}

Future<int> _size(String path) async =>
    await File(path).exists() ? await File(path).length() : 0;

final class _WalFactory implements DatabaseFactory {
  _WalFactory(this.delegate, [this.profile]);
  final SqlProbeProfile? profile;
  final DatabaseFactory delegate;
  late Database database;
  @override
  Future<Database> openDatabase(
    String path, {
    OpenDatabaseOptions? options,
  }) async {
    final database = await delegate.openDatabase(path, options: options);
    if (options?.readOnly == true) return database;
    await database.rawQuery('PRAGMA journal_mode = WAL');
    await database.rawQuery('PRAGMA wal_autocheckpoint = 0');
    return this.database = profile == null
        ? database
        : ProfileDatabase(database, profile!);
  }

  @override
  Future<bool> databaseExists(String path) => delegate.databaseExists(path);
  @override
  Future<String> getDatabasesPath() => delegate.getDatabasesPath();

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

Future<Map<String, Object?>> _automaticBaseline(
  String path,
  AutomaticDiagnostics? diagnostics,
) async {
  final profile = diagnostics == null ? null : SqlProbeProfile(diagnostics);
  diagnostics?.phase = 'automatic-setup';
  diagnostics?.database = profile!.snapshot;
  final factory = _WalFactory(databaseFactoryFfi, profile);
  final policy = SqliteInstallationPolicy(
    identity: 'automatic-host-baseline',
    version: 1,
    evaluate: (_) async => SqliteInstallationEvidence.verified(
      installationId: 'isolated-host-baseline',
    ),
  );
  int? settledWal;
  var peakWal = 0, retainedBytes = 0;
  final result = await automaticBaseline(
    diagnostics: diagnostics,
    stoppedSnapshot: () => _stoppedSnapshot(factory.database),
    itemsPerScope: [100],
    open: (_, client, binding, schedule) async {
      final session =
          (await SqliteOfflineStore.openSession(
                    path: path,
                    client: client,
                    binding: binding,
                    installationPolicy: policy,
                    databaseFactory: factory,
                    transferAllowed: false,
                    schedule: schedule,
                    readRoute: LanternSingleResponderRoute.certified(
                      client: client,
                      responderId: 'baseline',
                      isCurrent: () => true,
                    ),
                  )
                  as SqliteSessionReady)
              .session;
      if (diagnostics != null) {
        final flutter =
            jsonDecode(
                  (await Process.run('flutter', [
                        '--version',
                        '--machine',
                      ])).stdout
                      as String,
                )
                as Map<String, dynamic>;
        diagnostics.emit('environment', {
          'flutter': flutter['frameworkVersion'],
          'flutterRevision': flutter['frameworkRevision'],
          'architecture': (await Process.run('uname', [
            '-m',
          ])).stdout.toString().trim(),
          'sqliteVersion': (await factory.database.rawQuery(
            'SELECT sqlite_version() AS version',
          )).single['version'],
          'journalMode': (await factory.database.rawQuery(
            'PRAGMA journal_mode',
          )).single.values.single,
          'synchronous': (await factory.database.rawQuery(
            'PRAGMA synchronous',
          )).single.values.single,
          'process': probeProcessCounters(),
        });
      }
      Future<BaselineCounts> inspect() async {
        final values = await factory.database.rawQuery('''
          SELECT (SELECT COUNT(*) FROM outbox) AS outbox,
            (SELECT COUNT(*) FROM outbox WHERE lease_until IS NOT NULL) AS claims,
            (SELECT COUNT(*) FROM operations) AS operations,
            (SELECT COALESCE(SUM(reserved_bytes),0) FROM cache) +
            (SELECT COALESCE(SUM(reserved_bytes),0) FROM traversals) +
            (SELECT COALESCE(SUM(reserved_bytes),0) FROM operations) AS retained
        ''');
        final row = values.single;
        retainedBytes = row['retained']! as int;
        final wal = await _size('$path-wal');
        if (wal > peakWal) peakWal = wal;
        if (row['outbox'] == 0) settledWal ??= wal;
        return (
          outbox: row['outbox']! as int,
          claims: row['claims']! as int,
          operations: row['operations']! as int,
          memoryBytes: 0,
        );
      }

      return (session: session, inspect: inspect);
    },
  );
  final bytes = await _size(path);
  final reopen = Stopwatch()..start();
  final store = await SqliteOfflineStore.open(
    path: path,
    databaseFactory: factory,
  );
  await store.close();
  reopen.stop();
  final cacheWal = peakWal - settledWal!;
  baselineRequire(bytes <= 8 * 1024 * 1024, 'database_bytes');
  baselineRequire(cacheWal <= 64 * 1024 * 1024, 'cache_wal_bytes');
  baselineRequire(settledWal! <= 128 * 1024 * 1024, 'lifecycle_wal_bytes');
  baselineRequire(reopen.elapsedMilliseconds <= 5000, 'reopen_budget');
  return {
    ...result,
    'journal': 'wal',
    'synchronous': 'full',
    'databaseBytes': bytes,
    'retainedBytes': retainedBytes,
    'cacheWalGrowthBytes': cacheWal,
    'lifecycleWalBytes': settledWal,
    'peakWalBytes': peakWal,
    'reopenMillis': reopen.elapsedMilliseconds,
  };
}

Future<Map<String, Object?>> _stoppedSnapshot(Database database) async {
  final rows = await database.query('outbox');
  final operations = await database.query('operations');
  if (rows.length > 100 || operations.length > 1) {
    throw StateError('snapshot_capacity');
  }
  final states = <String, int>{}, outcomes = <String, int>{};
  var leased = 0, dispatched = 0, neverDispatched = 0, attempts = 0;
  var retries = 0, dueRetries = 0, totalItems = 0;
  final now = DateTime.now().toUtc();
  for (final row in rows) {
    final record = OfflineCodec.decodeOutboxRecord(
      utf8.decode(row['payload']! as List<int>),
    );
    states.update(record.state.name, (n) => n + 1, ifAbsent: () => 1);
    if (record.leaseUntil != null) leased++;
    if (record.mayHaveDispatched) {
      dispatched++;
    } else {
      neverDispatched++;
    }
    attempts += record.attemptCount;
    if (record.nextAttemptAt != null) {
      retries++;
      if (!record.nextAttemptAt!.isAfter(now)) dueRetries++;
    }
  }
  for (final row in operations) {
    final record = OfflineCodec.decodeOperationRecord(
      utf8.decode(row['payload']! as List<int>),
    );
    for (final item in record.items) {
      totalItems++;
      outcomes.update(item.state.name, (n) => n + 1, ifAbsent: () => 1);
    }
  }
  return {
    'outboxByState': states,
    'outboxCount': rows.length,
    'leased': leased,
    'dispatchedMarkers': dispatched,
    'neverDispatchedMarkers': neverDispatched,
    'attemptsTotal': attempts,
    'retryDeadlines': retries,
    'retryDeadlinesDue': dueRetries,
    'operationItems': totalItems,
    'itemOutcomes': outcomes,
    'partitionHolds': await database.query(
      'partitions',
      columns: ['auth_paused', 'policy_paused'],
    ),
  };
}
