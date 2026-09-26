part of '../lantern_client_offline_sqlite.dart';

/// A SQLite-backed [OfflineStore] with atomic, serialized transactions.
///
/// Open one owner per database in an application. Separate owners using the
/// same factory are supported: SQLite arbitrates writes and committed changes
/// are forwarded between live owners in this isolate. Other processes must
/// arrange their own invalidation notifications.
final class SqliteOfflineStore implements OfflineStore {
  SqliteOfflineStore._(
    this._db,
    this._factory,
    this._lane,
    this._laneKey,
    this.limits,
  );

  /// Opens an application-owned path, creating schema version three if empty.
  ///
  /// Unknown schemas and invalid durable records fail closed. [databaseFactory]
  /// defaults to the mobile OS SQLite plugin. Tests may inject an FFI factory;
  /// applications may inject an independently audited protected factory. This
  /// adapter does not implement encryption or store credentials/encryption keys.
  static Future<SqliteOfflineStore> open({
    required String path,
    DatabaseFactory? databaseFactory,
    OfflineStoreLimits limits = const OfflineStoreLimits(),
  }) async {
    if (path.isEmpty || path == inMemoryDatabasePath) {
      throw const OfflineArgumentException();
    }
    _validateLimits(limits);
    final factory = databaseFactory ?? defaultDatabaseFactory();
    final key = paths.normalize(
      paths.isAbsolute(path)
          ? path
          : paths.join(await factory.getDatabasesPath(), path),
    );
    final lanes = _lanes[factory] ??= <String, _SqlLane>{};
    final lane = lanes.putIfAbsent(key, _SqlLane.new);
    lane.references++;
    try {
      return await _databaseCall(
        () => lane.run(() async {
          final db = await factory.openDatabase(
            path,
            options: OpenDatabaseOptions(
              version: _schemaVersion,
              singleInstance: false,
              onConfigure: (db) => _databaseCall(() async {
                await db.execute('PRAGMA foreign_keys = ON');
                await db.rawQuery('PRAGMA busy_timeout = 5000');
                await db.execute('PRAGMA synchronous = FULL');
              }),
              onCreate: (db, version) =>
                  _databaseCall(() => _createSchema(db, version)),
              onUpgrade: (db, oldVersion, newVersion) => _databaseCall(
                () => _upgradeSchema(db, oldVersion, newVersion, limits),
              ),
              onDowngrade: (_, _, _) async =>
                  throw const OfflineSchemaException(),
            ),
          );
          try {
            await _checkSchema(db);
            await db.transaction((sql) async {
              final tx = _SqlTransaction(sql, limits);
              await tx.validateAll();
              await tx.finish();
            }, exclusive: true);
            final store = SqliteOfflineStore._(db, factory, lane, key, limits);
            lane.owners.add(store);
            return store;
          } catch (_) {
            await db.close();
            rethrow;
          }
        }),
      );
    } catch (_) {
      if (--lane.references == 0) lanes.remove(key);
      rethrow;
    }
  }

  static final _lanes = Expando<Map<String, _SqlLane>>();
  final Database _db;
  final DatabaseFactory _factory;
  final _SqlLane _lane;
  final String _laneKey;

  /// Logical record and lifecycle byte limits enforced before every commit.
  final OfflineStoreLimits limits;

  /// The opened SQLite database path.
  String get path => _db.path;

  final Map<String, StreamController<OfflineStoreChange>> _changes = {};
  Future<void>? _closing;
  bool _closed = false;

  @override
  Future<T> transaction<T>(
    FutureOr<T> Function(OfflineStoreTransaction transaction) action,
  ) {
    if (_closed) return Future<T>.error(const OfflineDisposedException());
    return _databaseCall(
      () => _lane.run(() async {
        final changes = <OfflineStoreChange>[];
        final value = await _db.transaction<T>((sql) async {
          final tx = _SqlTransaction(sql, limits);
          late final T result;
          var callbackCompleted = false;
          try {
            result = await action(tx);
            callbackCompleted = true;
          } finally {
            await tx.finish(rejectPending: callbackCompleted);
          }
          await tx.validateCommit();
          for (final partition in tx.changed) {
            final metadata = (await sql.query(
              'partitions',
              where: 'partition_id=?',
              whereArgs: [partition],
            )).single;
            final version = _checkedIncrement(metadata['version']! as int);
            await sql.update(
              'partitions',
              {'version': version},
              where: 'partition_id=?',
              whereArgs: [partition],
            );
            changes.add(
              OfflineStoreChange(
                partitionId: partition,
                version: version,
                generation: metadata['generation']! as int,
              ),
            );
          }
          return result;
        }, exclusive: true);
        for (final owner in List<SqliteOfflineStore>.of(_lane.owners)) {
          for (final change in changes) {
            owner._changes[change.partitionId]?.add(change);
          }
        }
        return value;
      }),
    );
  }

  @override
  Stream<OfflineStoreChange> changes(String partitionId) {
    _validatePartition(partitionId);
    StreamSubscription<OfflineStoreChange>? subscription;
    late final StreamController<OfflineStoreChange> wrapper;
    wrapper = StreamController<OfflineStoreChange>.broadcast(
      sync: true,
      onListen: () {
        try {
          if (_closed) throw const OfflineDisposedException();
          subscription = _controller(partitionId).stream.listen(
            wrapper.add,
            onError: wrapper.addError,
            onDone: wrapper.close,
          );
        } catch (error, stack) {
          scheduleMicrotask(() {
            if (!wrapper.isClosed) {
              wrapper.addError(error, stack);
              unawaited(wrapper.close());
            }
          });
        }
      },
      onCancel: () async {
        final old = subscription;
        subscription = null;
        await old?.cancel();
      },
    );
    return wrapper.stream;
  }

  StreamController<OfflineStoreChange> _controller(String partitionId) {
    final previous = _changes[partitionId];
    if (previous != null) return previous;
    if (_changes.length >= limits.maxChangeControllers) {
      throw const OfflineCapacityException();
    }
    late final StreamController<OfflineStoreChange> controller;
    controller = StreamController<OfflineStoreChange>.broadcast(
      sync: true,
      onCancel: () {
        if (!controller.hasListener &&
            identical(_changes[partitionId], controller)) {
          _changes.remove(partitionId);
          unawaited(controller.close());
        }
      },
    );
    _changes[partitionId] = controller;
    return controller;
  }

  /// Drains accepted transactions, closes this connection, and releases streams.
  ///
  /// Idempotent. New transactions fail with [OfflineDisposedException].
  Future<void> close() {
    if (_closing != null) return _closing!;
    _closed = true;
    return _closing = _databaseCall(
      () => _lane.run(() async {
        _lane.owners.remove(this);
        for (final controller in _changes.values.toList()) {
          unawaited(controller.close());
        }
        _changes.clear();
        try {
          await _db.close();
        } finally {
          if (--_lane.references == 0) _lanes[_factory]?.remove(_laneKey);
        }
      }),
    );
  }

  /// Releases this store; equivalent to [close].
  Future<void> dispose() => close();
}

// Some SQLite factories execute all native calls on one isolate. Letting a
// second connection synchronously wait for a writer lock there can prevent the
// first connection from committing. Serialize owners before entering SQLite;
// SQLite itself still arbitrates writers in other isolates/processes.
final class _SqlLane {
  Future<void> _tail = Future<void>.value();
  final Set<SqliteOfflineStore> owners = {};
  int references = 0;

  Future<T> run<T>(Future<T> Function() action) {
    final result = _tail.then((_) => action());
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }
}
