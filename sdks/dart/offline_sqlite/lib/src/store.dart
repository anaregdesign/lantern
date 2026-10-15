part of '../lantern_client_offline_sqlite.dart';

/// A SQLite-backed [OfflineStore] with atomic, serialized transactions.
///
/// Open one owner per database in an application. Separate owners using the
/// same factory are supported: SQLite arbitrates writes and committed changes
/// are forwarded between live owners in this isolate. Other processes must
/// arrange their own invalidation notifications.
final class SqliteOfflineStore implements OfflineStore, OfflineAutomaticStore {
  SqliteOfflineStore._(
    this._db,
    this._factory,
    this._lane,
    this._laneKey,
    this.limits,
  );

  /// Opens an application-owned path, creating the current schema if empty.
  ///
  /// Unknown schemas and invalid durable records fail closed. [databaseFactory]
  /// defaults to the mobile OS SQLite plugin. Tests may inject an FFI factory;
  /// applications may inject an independently audited protected factory. This
  /// adapter does not implement encryption or store credentials/encryption keys.
  static Future<SqliteOfflineStore> open({
    required String path,
    DatabaseFactory? databaseFactory,
    OfflineStoreLimits limits = const OfflineStoreLimits(),
  }) => _open(
    path: path,
    databaseFactory: databaseFactory,
    limits: limits,
    attempt: _SqlOpenAttempt(),
  );

  static Future<SqliteOfflineStore> _open({
    required String path,
    DatabaseFactory? databaseFactory,
    required OfflineStoreLimits limits,
    required _SqlOpenAttempt attempt,
    _SqlInitialization? initialization,
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
            key,
            options: OpenDatabaseOptions(
              version: _schemaVersion,
              singleInstance: false,
              onConfigure: (db) => _databaseCall(() async {
                attempt.openingConnection = db;
                await db.execute('PRAGMA foreign_keys = ON');
                await db.rawQuery('PRAGMA busy_timeout = 5000');
                await db.execute('PRAGMA synchronous = FULL');
              }),
              onCreate: (db, version) => _databaseCall(() async {
                await _createSchema(db, version);
                await initialization?.initialize(db, limits, created: true);
              }),
              onUpgrade: (db, oldVersion, newVersion) =>
                  _databaseCall(() async {
                    await _upgradeSchema(db, oldVersion, newVersion, limits);
                    await initialization?.initialize(db, limits);
                  }),
              onDowngrade: (_, _, _) async =>
                  throw const OfflineSchemaException(),
            ),
          );
          attempt.openReturned = true;
          try {
            await _checkSchema(db);
            await db.transaction((sql) async {
              if (initialization != null && !initialization.initialized) {
                await initialization.initialize(sql, limits);
              }
              final tx = _SqlTransaction(sql, limits);
              await tx.validateAll();
              await tx.finish();
            }, exclusive: true);
            final store = SqliteOfflineStore._(db, factory, lane, key, limits);
            lane.owners.add(store);
            return store;
          } catch (_) {
            try {
              await db.close();
            } catch (_) {
              attempt.cleanupFailed = true;
              rethrow;
            }
            rethrow;
          }
        }),
      );
    } catch (_) {
      if (--lane.references == 0) lanes.remove(key);
      // A failed create/upgrade can throw before openDatabase returns. Join the
      // connection's close as well; never assume plugin cleanup released it.
      if (!attempt.openReturned && attempt.openingConnection != null) {
        try {
          await attempt.openingConnection!.close();
        } catch (_) {
          attempt.cleanupFailed = true;
          rethrow;
        }
      }
      rethrow;
    }
  }

  /// Opens refcounted foreground CRUD/watch handles with native ownership before
  /// SQLite migration. Matching same-isolate handles share one bounded runtime;
  /// another isolate/process fails before any network work.
  static Future<SqliteSessionStartup> openSession({
    required String path,
    required LanternClient client,
    required LanternSessionBinding binding,
    required SqliteInstallationPolicy installationPolicy,
    SqliteScopeAdoption? adoption,
    DatabaseFactory? databaseFactory,
    OfflineStoreLimits limits = const OfflineStoreLimits(),
    bool cacheReads = true,
    bool transferAllowed = true,
    bool restoreHeld = false,
    OfflineClock? clock,
    OfflineSessionSchedule? schedule,
    LanternChangeSource? incomingChanges,
    LanternSingleResponderRoute? readRoute,
  }) async {
    try {
      return SqliteSessionReady(
        await OfflineLanternSession.openStore(
          client: client,
          storeKey: canonicalAutomaticPath(path),
          createStore: () => _openQualified(
            path: path,
            policy: installationPolicy,
            binding: binding,
            adoption: adoption,
            databaseFactory: databaseFactory,
            limits: limits,
          ),
          closeStore: (store) => (store as SqliteOfflineStore).close(),
          binding: binding,
          durability: AdmissionDurability.durable,
          cacheReads: cacheReads,
          transferAllowed: transferAllowed,
          restoreHeld: restoreHeld,
          clock: clock,
          schedule: schedule,
          incomingChanges: incomingChanges,
          readRoute: readRoute,
          adapterConfiguration: (
            databaseFactory,
            limits,
            installationPolicy,
            installationPolicy.identity,
            installationPolicy.version,
            adoption,
          ),
        ),
      );
    } on _RestoreHold catch (hold) {
      return SqliteRestoreHeld(hold.reason);
    }
  }

  static final _lanes = Expando<Map<String, _SqlLane>>();
  NativeOfflineOwner? _reservation;
  _SqlAutomaticOwner? _automaticOwner;
  bool _qualifiedInstallation = false;
  NativeOfflineOwner? _qualifiedGuard;

  static Future<SqliteOfflineStore> _openQualified({
    required String path,
    required SqliteInstallationPolicy policy,
    required LanternSessionBinding binding,
    required SqliteScopeAdoption? adoption,
    DatabaseFactory? databaseFactory,
    required OfflineStoreLimits limits,
  }) async {
    final guard = NativeOfflineOwner.acquire(path);
    final attempt = _SqlOpenAttempt();
    SqliteOfflineStore? store;
    try {
      final initialization = _SqlInitialization(
        policy,
        binding,
        adoption,
        guard,
      );
      await _databaseCall(
        () => initialization.inspect(
          databaseFactory ?? defaultDatabaseFactory(),
          attempt,
        ),
      );
      store = await _open(
        path: guard.path,
        databaseFactory: databaseFactory,
        limits: limits,
        attempt: attempt,
        initialization: initialization,
      );
      store._qualifiedInstallation = true;
      store._qualifiedGuard = guard;
      store._reservation = guard;
      guard.check();
      return store;
    } catch (_) {
      if (store != null) {
        // close owns the retained capability; a failed close does not unlock.
        await store.close();
      } else if (!attempt.cleanupFailed) {
        guard.release();
      }
      rethrow;
    }
  }

  /// Resolves an application-owned DB name before the same-isolate runtime
  /// registry matches handles. This alone does not acquire ownership.
  static String canonicalAutomaticPath(String path) =>
      NativeOfflineOwner.canonicalPath(path);

  /// Reserves native ownership before opening or migrating SQLite. Only a store
  /// opened this way may furnish an automatic runtime owner. The default mobile
  /// engine is still sqflite; host tests may inject their OS SQLite factory.
  static Future<SqliteOfflineStore> openAutomatic({
    required String path,
    DatabaseFactory? databaseFactory,
    OfflineStoreLimits limits = const OfflineStoreLimits(),
  }) async {
    final guard = NativeOfflineOwner.acquire(path);
    SqliteOfflineStore? store;
    try {
      store = await open(
        path: guard.path,
        databaseFactory: databaseFactory,
        limits: limits,
      );
      guard.check();
      store._reservation = guard;
      return store;
    } catch (_) {
      try {
        await store?.close();
      } finally {
        guard.release();
      }
      rethrow;
    }
  }

  @override
  Object get canonicalOwnerKey => _laneKey;

  @override
  Future<OfflineAutomaticOwner> acquireAutomaticOwner({
    required String scope,
    required String bindingFingerprint,
    required String configurationFingerprint,
  }) async {
    final guard = _reservation;
    if (_closed || guard == null || _automaticOwner != null) {
      throw const OfflineOwnerConflictException();
    }
    for (final value in [scope, bindingFingerprint, configurationFingerprint]) {
      if (value.isEmpty || value.length > 4096) {
        throw const OfflineArgumentException();
      }
    }
    // Remove the provisional capability synchronously so concurrent acquisition
    // cannot publish two lifetimes, even within the same isolate.
    _reservation = null;
    String? acquiredToken;
    try {
      final random = Random.secure();
      final token = acquiredToken = List.generate(
        32,
        (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
      ).join();
      return await _databaseCall(
        () => _lane.run(() async {
          guard.check();
          final epoch = await _db.transaction((sql) async {
            final rows = await sql.query('automatic_owner', where: 'id=1');
            final previous = rows.isEmpty ? 0 : rows.single['epoch'];
            if (previous is! int || previous < 0) {
              throw const OfflineSchemaException();
            }
            final next = _checkedIncrement(previous);
            await sql.insert('automatic_owner', {
              'id': 1,
              'epoch': next,
              'token': token,
              'active': 1,
              'scope': scope,
              'binding': bindingFingerprint,
              'configuration': configurationFingerprint,
            }, conflictAlgorithm: ConflictAlgorithm.replace);
            guard.check();
            return next;
          }, exclusive: true);
          guard.check();
          final owner = _SqlAutomaticOwner(this, guard, epoch, token);
          _automaticOwner = owner;
          return owner;
        }),
      );
    } catch (_) {
      // Network has not been exposed. Cleanup is conditional even when the
      // outcome of the acquisition commit is uncertain.
      try {
        if (acquiredToken != null) {
          await _lane.run(
            () => _db.update(
              'automatic_owner',
              {'active': 0},
              where: 'id=1 AND token=?',
              whereArgs: [acquiredToken],
            ),
          );
        }
      } finally {
        if (!_qualifiedInstallation) guard.release();
      }
      rethrow;
    }
  }

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
        final fenceUpdates =
            <
              _SqlClaimGuard,
              ({Map<String, Object?>? metadata, OfflineOutboxRecord? current})
            >{};
        final value = await _db.transaction<T>((sql) async {
          await _automaticOwner?.checkTransaction(sql);
          final tx = _SqlTransaction(sql, limits, _lane, () => !_closed);
          late final T result;
          var callbackCompleted = false;
          try {
            result = await action(tx);
            callbackCompleted = true;
          } finally {
            await tx.finish(rejectPending: callbackCompleted);
          }
          await tx.validateCommit();
          await _automaticOwner?.checkTransaction(sql);
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
                authorityBinding: metadata['authority_binding'] as String?,
                observationInvalidated: tx.observationInvalidated.contains(
                  partition,
                ),
              ),
            );
          }
          // Snapshot only bounded active guards, including quiet high-water
          // changes. Install them synchronously after commit, before any event
          // or transaction completion can expose the replacement claim.
          final scopes = <String, List<_SqlClaimGuard>>{};
          for (final guard in {
            ..._lane.fences.where(
              (g) =>
                  tx.changed.contains(g.claim.partitionId) ||
                  tx.mutated.contains(g.claim.partitionId),
            ),
            ...tx.pendingFences,
          }) {
            (scopes[guard.claim.partitionId] ??= []).add(guard);
          }
          for (final entry in scopes.entries) {
            final metadataRows = await sql.query(
              'partitions',
              where: 'partition_id=?',
              whereArgs: [entry.key],
            );
            final metadata = metadataRows.isEmpty ? null : metadataRows.single;
            final ids = entry.value
                .map((g) => g.claim.recordId)
                .toSet()
                .toList();
            final records = <String, OfflineOutboxRecord>{};
            for (var start = 0; start < ids.length; start += 64) {
              final end = start + 64 < ids.length ? start + 64 : ids.length;
              final chunk = ids.sublist(start, end);
              for (final row in await sql.query(
                'outbox',
                where:
                    'partition_id=? AND record_id IN (${List.filled(chunk.length, '?').join(',')})',
                whereArgs: [entry.key, ...chunk],
              )) {
                final record = _outbox(row);
                records[record.recordId] = record;
              }
            }
            for (final guard in entry.value) {
              fenceUpdates[guard] = (
                metadata: metadata,
                current: records[guard.claim.recordId],
              );
            }
          }
          return result;
        }, exclusive: true);
        for (final entry in fenceUpdates.entries) {
          entry.key.committed(entry.value.metadata, entry.value.current);
        }
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
    final owner = _automaticOwner;
    final reservation = _reservation;
    _reservation = null;
    return _closing = (() async {
      Object? releaseError;
      StackTrace? releaseStack;
      try {
        await owner?.release();
        if (!_qualifiedInstallation) reservation?.release();
      } catch (error, stack) {
        releaseError = error;
        releaseStack = stack;
      }
      await _databaseCall(
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
      if (releaseError != null) {
        Error.throwWithStackTrace(releaseError, releaseStack!);
      }
      // A qualified owner keeps its native guard through connection shutdown.
      // Any failed cleanup above retains it and the memoized close failure.
      _qualifiedGuard?.release();
    })();
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
  final Set<_SqlClaimGuard> fences = {};
  int references = 0;

  Future<T> run<T>(Future<T> Function() action) {
    final result = _tail.then((_) => action());
    _tail = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }
}

/// Ephemeral guard for this lane's supported same-isolate commit coordination.
/// Other processes still require WP3's exclusive runtime-owner protocol.
final class _SqlClaimGuard {
  _SqlClaimGuard(this.claim, this.authority, this.storeOpen, this.lane);
  final OfflineOutboxRecord claim;
  final String authority;
  final bool Function() storeOpen;
  final _SqlLane lane;
  bool _active = false, _retired = false;
  Map<String, Object?>? _metadata;
  OfflineOutboxRecord? _current;
  late final OfflineClaimFence fence = OfflineClaimFence(
    checkCurrent: (observedAt) {
      final metadata = _metadata;
      if (!_active || _retired || !storeOpen() || metadata == null) {
        throw const OfflineClaimLostException();
      }
      return checkOfflineClaim(
        expected: claim,
        current: _current,
        generation: metadata['generation']! as int,
        authorityBinding: metadata['authority_binding'] as String?,
        expectedAuthority: authority,
        highWater: DateTime.fromMicrosecondsSinceEpoch(
          metadata['time_high_water']! as int,
          isUtc: true,
        ),
        observedAt: observedAt,
      );
    },
    onRelease: () {
      _retired = true;
      _active = false;
      lane.fences.remove(this);
    },
  );

  void committed(Map<String, Object?>? metadata, OfflineOutboxRecord? current) {
    if (_retired) return;
    _metadata = metadata;
    _current = current;
    _active = true;
    lane.fences.add(this);
  }
}
