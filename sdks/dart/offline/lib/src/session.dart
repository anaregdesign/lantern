// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
part of 'bound_data_client.dart';

/// A new same-account credential event verified by the application's auth
/// provider. Construct only after successful native/application authentication;
/// a token string change, decoded JWT, counter, Health or connectivity is not
/// verification. An event can be accepted once, including across reopen.
final class OfflineVerifiedCredentials {
  /// [install] publishes the verified credential to the existing TokenProvider.
  /// It runs only after every old network attempt has joined. No credential bytes
  /// are passed to or retained by this package.
  OfflineVerifiedCredentials({required this.binding, required this.install});

  /// Verified identity/authority and credential deadline.
  final LanternSessionBinding binding;

  /// Auth-provider-owned atomic credential publication.
  final FutureOr<void> Function() install;
  bool _accepted = false;
}

/// One refcounted foreground automatic session. Use ordinary CRUD/watch methods;
/// its runtime resumes pending delivery without an application Get or Sync.
/// Lifecycle hooks below belong to the application's foreground/auth bridge.
final class OfflineLanternSession
    with OfflineBoundWrites
    implements LanternDataClient {
  OfflineLanternSession._(this._entry, this._shared) {
    _mutations = _SessionMutations(this);
  }
  static final Map<Object, _SessionEntry> _sessions = {};
  static final OfflineSessionBudget _budget = OfflineSessionBudget();

  /// Opens a qualified store already reserved by its platform adapter, or a
  /// MemoryOfflineStore. Matching handles share one runtime in this isolate.
  static Future<OfflineLanternSession> open({
    required LanternClient client,
    required OfflineStore store,
    required LanternSessionBinding binding,
    required AdmissionDurability durability,
    bool cacheReads = true,
    bool transferAllowed = true,
    bool restoreHeld = false,
    OfflineClock? clock,
    OfflineSessionSchedule? schedule,
    bool Function()? authorityCurrent,
    OfflineReceiptRemote? receiptRemote,
    OfflineBoundMutationRemote? mutationRemote,
    LanternChangeSource? incomingChanges,
    LanternSingleResponderRoute? readRoute,
  }) {
    if (store is! OfflineAutomaticStore) {
      throw const OfflineUnsupportedOperationException();
    }
    return openStore(
      client: client,
      storeKey: (store as OfflineAutomaticStore).canonicalOwnerKey,
      createStore: () async => store,
      closeStore: (_) async {},
      binding: binding,
      durability: durability,
      cacheReads: cacheReads,
      transferAllowed: transferAllowed,
      restoreHeld: restoreHeld,
      clock: clock,
      schedule: schedule,
      authorityCurrent: authorityCurrent,
      receiptRemote: receiptRemote,
      mutationRemote: mutationRemote,
      incomingChanges: incomingChanges,
      readRoute: readRoute,
    );
  }

  /// Platform factory seam: registry reservation happens before [createStore],
  /// which must acquire its native provisional guard before SQLite open/migration.
  /// [storeKey] is the adapter's canonical identity, never an application account
  /// string. The factory owns the returned store and transport until last close.
  static Future<OfflineLanternSession> openStore({
    required LanternClient client,
    required Object storeKey,
    required Future<OfflineStore> Function() createStore,
    required Future<void> Function(OfflineStore) closeStore,
    required LanternSessionBinding binding,
    required AdmissionDurability durability,
    bool cacheReads = true,
    bool transferAllowed = true,
    bool restoreHeld = false,
    OfflineClock? clock,
    bool Function()? authorityCurrent,
    OfflineReceiptRemote? receiptRemote,
    OfflineBoundMutationRemote? mutationRemote,
    LanternChangeSource? incomingChanges,
    LanternSingleResponderRoute? readRoute,
    OfflineSessionSchedule? schedule,
    Object? adapterConfiguration,
  }) async {
    LanternAdapterReads.checkBinding(client, binding);
    if (incomingChanges != null &&
        (!identical(incomingChanges.route.client, client) ||
            incomingChanges.binding.stableIdentity != binding.stableIdentity ||
            incomingChanges.binding.authorityBinding !=
                binding.authorityBinding)) {
      throw const OfflineArgumentException();
    }
    if (readRoute != null &&
        incomingChanges != null &&
        !identical(readRoute, incomingChanges.route)) {
      throw const OfflineArgumentException();
    }
    final effectiveRoute = readRoute ?? incomingChanges?.route;
    if (effectiveRoute != null && !identical(effectiveRoute.client, client)) {
      throw const OfflineArgumentException();
    }
    incomingChanges?.checkCurrent();
    final fingerprint = jsonEncode([
      binding.stableIdentity,
      binding.authorityBinding,
      binding.userId,
      binding.personalPrefix,
      binding.expiresAt?.toUtc().toIso8601String(),
      durability.name,
      cacheReads,
      transferAllowed,
      restoreHeld,
    ]);
    final configuration = (
      client,
      fingerprint,
      clock,
      authorityCurrent,
      receiptRemote,
      mutationRemote,
      incomingChanges,
      effectiveRoute,
      schedule,
      adapterConfiguration,
    );
    var entry = _sessions[storeKey];
    if (entry != null) {
      if (entry.closing || entry.configuration != configuration) {
        throw const OfflineOwnerConflictException();
      }
      if (entry.references == 64) throw const OfflineCapacityException();
      entry.references++;
    } else {
      if (_sessions.length == 16) throw const OfflineCapacityException();
      entry = _SessionEntry(storeKey, configuration);
      _sessions[storeKey] = entry;
      final shared = _SessionShared(
        client,
        binding,
        clock ?? _wallClock,
        authorityCurrent ?? _authorityCurrent,
        closeStore,
        transferAllowed,
        restoreHeld,
      );
      final reserved = entry;
      shared.onReleased = () {
        reserved.closing = true;
        if (identical(_sessions[storeKey], reserved)) {
          _sessions.remove(storeKey);
        }
      };
      entry.future = shared.open(
        createStore,
        durability: durability,
        cacheReads: cacheReads,
        receiptRemote: receiptRemote,
        mutationRemote: mutationRemote,
        schedule: schedule,
        fingerprint: fingerprint,
        incomingChanges: incomingChanges,
        readRoute: effectiveRoute,
      );
    }
    try {
      return OfflineLanternSession._(entry, await entry.future);
    } catch (_) {
      if (identical(_sessions[storeKey], entry)) _sessions.remove(storeKey);
      rethrow;
    }
  }

  static DateTime _wallClock() => DateTime.now().toUtc();
  static bool _authorityCurrent() => true;
  final _SessionEntry _entry;
  final _SessionShared _shared;
  late final LanternMutationControl _mutations;
  final Set<StreamSubscription<Object?>> _subscriptions = {};
  final LanternCancellationToken _waiterLifetime = LanternCancellationToken();
  final Set<Future<void>> _waiterJoins = {};
  bool _closed = false;
  Future<void>? _closing;
  void _check() {
    if (_closed) throw const OfflineDisposedException();
    _shared._checkLifetime();
  }

  OfflineLanternDataClient get _data {
    _check();
    return _shared.data;
  }

  @override
  LanternSessionBinding get binding => _shared.binding;

  /// Read-only bounded lookup of an existing opaque legacy operation identity.
  /// This preserves the offline diagnostic type; it never constructs a modern
  /// OperationId, re-admits, expires, prepares or sends work. Requires a current
  /// verified scope without an auth, policy or restore hold.
  Future<OfflineOperationStatus?> inspectLegacyOperation(
    String operationId,
  ) async {
    if (operationId.isEmpty || utf8.encode(operationId).length > 4096) {
      throw const OfflineArgumentException();
    }
    void check() {
      _check();
      if (_shared.auth ||
          _shared.policy ||
          _shared.restoreHeld ||
          !_shared._credentialLive) {
        throw const OfflineWorkInterruptedException();
      }
    }

    check();
    final result = await _shared.store.transaction((tx) async {
      check();
      if (await tx.generation(binding.stableIdentity) !=
              _shared.data.generation ||
          await tx.authorityBinding(binding.stableIdentity) !=
              binding.authorityBinding) {
        throw const OfflineDisposedException();
      }
      return (await tx.getOperation(
        binding.stableIdentity,
        operationId,
      ))?.status;
    });
    check();
    return result;
  }

  @override
  LanternMutationControl get mutations {
    _check();
    return _mutations;
  }

  /// Stops every network attempt and timer, then joins them. Local admission and
  /// retained operation tickets remain valid in this account lifetime.
  Future<void> suspend() {
    _check();
    return _shared.suspend();
  }

  /// Applies due local expiry first and resumes only eligible durable work.
  Future<void> resume() {
    _check();
    return _shared.resume();
  }

  /// Optional platform hint. It coalesces eligible work but cannot override a
  /// retry deadline, authority pause or prove endpoint reachability.
  void notifyReachability() {
    _check();
    _shared._changed();
  }

  /// Application/native transfer policy (for example metered or battery state).
  /// Pausing joins network work while local admission and expiry continue.
  Future<void> setTransferAllowed(bool allowed) {
    _check();
    return _shared.setTransferAllowed(allowed);
  }

  /// Accepts one newly verified event. Same/old event objects never clear holds.
  /// Different account or full authority ends this immutable lifetime; open the
  /// newly bound session through the factory to invalidate old cache/cursors.
  Future<bool> acceptVerifiedCredentials(
    OfflineVerifiedCredentials event,
  ) async {
    _check();
    final accepted = await _shared.acceptCredentials(event);
    if (_shared.ended && identical(_sessions[_entry.key], _entry)) {
      _entry.closing = true;
      _sessions.remove(_entry.key);
    }
    return accepted;
  }

  Stream<T> _watch<T>(Stream<T> Function() source) {
    _check();
    late StreamController<T> controller;
    StreamSubscription<T>? subscription;
    controller = StreamController<T>(
      sync: true,
      onListen: () {
        try {
          _check();
          final active = subscription = source().listen(
            controller.add,
            onError: controller.addError,
            onDone: () {
              _subscriptions.remove(subscription);
              unawaited(controller.close());
            },
          );
          _subscriptions.add(active);
        } catch (error, stack) {
          controller.addError(error, stack);
          unawaited(controller.close());
        }
      },
      onPause: () => subscription?.pause(),
      onResume: () => subscription?.resume(),
      onCancel: () async {
        final active = subscription;
        _subscriptions.remove(active);
        await active?.cancel();
      },
    );
    return controller.stream;
  }

  @override
  Future<LanternSnapshot<Vertex>> getVertex(
    String key, {
    GetOptions? options,
  }) => _data.getVertex(key, options: options);
  @override
  Future<List<LanternSnapshot<Vertex>>> getVertices(
    Iterable<String> keys, {
    GetOptions? options,
  }) => _data.getVertices(keys, options: options);
  @override
  Future<LanternSnapshot<Edge>> getEdge(EdgeRef edge, {GetOptions? options}) =>
      _data.getEdge(edge, options: options);
  @override
  Future<List<LanternSnapshot<Edge>>> getEdges(
    Iterable<EdgeRef> edges, {
    GetOptions? options,
  }) => _data.getEdges(edges, options: options);
  @override
  Future<LanternTraversalSnapshot> illuminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    TraversalReadOptions? options,
  }) => _data.illuminate(
    seed,
    traversal: traversal,
    weighting: weighting,
    vertexPrefix: vertexPrefix,
    options: options,
  );
  @override
  Stream<LanternSnapshot<Vertex>> watchVertex(
    String key, {
    WatchOptions? options,
  }) => _watch(() => _data.watchVertex(key, options: options));
  @override
  Stream<List<LanternSnapshot<Vertex>>> watchVertices(
    Iterable<String> keys, {
    WatchOptions? options,
  }) => _watch(() => _data.watchVertices(keys, options: options));
  @override
  Stream<LanternSnapshot<Edge>> watchEdge(
    EdgeRef edge, {
    WatchOptions? options,
  }) => _watch(() => _data.watchEdge(edge, options: options));
  @override
  Stream<List<LanternSnapshot<Edge>>> watchEdges(
    Iterable<EdgeRef> edges, {
    WatchOptions? options,
  }) => _watch(() => _data.watchEdges(edges, options: options));
  @override
  Stream<LanternTraversalSnapshot> watchIlluminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    WatchOptions? options,
  }) => _watch(
    () => _data.watchIlluminate(
      seed,
      traversal: traversal,
      weighting: weighting,
      vertexPrefix: vertexPrefix,
      options: options,
    ),
  );
  @override
  Stream<LanternSyncStatus> get syncStatus => _watch(() => _data.syncStatus);
  @override
  Future<void> close() {
    if (_closing != null) return _closing!;
    _closed = true;
    _waiterLifetime.cancel();
    return _closing = (() async {
      await Future.wait(_waiterJoins.toList());
      await Future.wait(_subscriptions.toList().map((s) => s.cancel()));
      _subscriptions.clear();
      if (--_entry.references != 0) return;
      _entry.closing = true;
      // Only successful join followed by release removes the registry entry.
      // If join fails, retain the closed entry and native guard fail-closed.
      await _shared.close();
    })();
  }
}

final class _SessionEntry {
  _SessionEntry(this.key, this.configuration);
  final Object key, configuration;
  late Future<_SessionShared> future;
  int references = 1;
  bool closing = false;
}

final class _SessionShared {
  _SessionShared(
    this.client,
    this.binding,
    this.clock,
    this.authorityCurrent,
    this.closeStore,
    this.transferAllowed,
    this.restoreHeld,
  ) : credentialExpires = binding.expiresAt;
  final LanternClient client;
  final LanternSessionBinding binding;
  final OfflineClock clock;
  final bool Function() authorityCurrent;
  final Future<void> Function(OfflineStore) closeStore;
  bool transferAllowed;
  final bool restoreHeld;
  final Random _retryRandom = Random();
  int _failures = 0;
  bool _endpointObserved = false;
  DateTime? _backoffUntil;
  late OfflineStore store;
  late OfflineAutomaticOwner owner;
  late OfflineSessionRuntime runtime;
  late OfflineLanternDataClient data;
  DateTime? credentialExpires;
  bool ended = false,
      suspended = false,
      auth = false,
      policy = false,
      storage = false;
  bool ready = false;
  Future<void>? _closing, _credentialChange;
  Future<void> _networkJoin = Future.value();
  final List<OfflineMutationDelivery> deliveries = [];
  _AutomaticIncoming? incoming;
  late void Function() onReleased;

  Future<_SessionShared> open(
    Future<OfflineStore> Function() createStore, {
    required AdmissionDurability durability,
    required bool cacheReads,
    required String fingerprint,
    OfflineReceiptRemote? receiptRemote,
    OfflineBoundMutationRemote? mutationRemote,
    OfflineSessionSchedule? schedule,
    LanternChangeSource? incomingChanges,
    LanternSingleResponderRoute? readRoute,
  }) async {
    store = await createStore();
    OfflineAutomaticOwner? acquired;
    OfflineSessionRuntime? createdRuntime;
    OfflineLanternDataClient? createdData;
    try {
      if (store is! OfflineAutomaticStore) {
        throw const OfflineUnsupportedOperationException();
      }
      owner = acquired = await (store as OfflineAutomaticStore)
          .acquireAutomaticOwner(
            scope: binding.stableIdentity,
            bindingFingerprint: binding.authorityBinding,
            configurationFingerprint: fingerprint,
          );
      runtime = createdRuntime = OfflineSessionRuntime(
        owner: owner,
        budget: OfflineLanternSession._budget,
        clock: dataOrClock,
        fence: _checkLifetime,
        schedule: schedule,
        onNetworkActivity: _healthChanged,
        onError: (error, stack) {
          _requestStoragePause();
        },
      );
      data = createdData = await OfflineLanternDataClient._open(
        client: client,
        store: store,
        binding: binding,
        durability: durability,
        cacheReads: cacheReads,
        clock: clock,
        authorityCurrent: authorityCurrent,
        receiptRemote: receiptRemote,
        mutationRemote: mutationRemote,
        session: this,
        readRoute: readRoute,
        bootstrapRequired: incomingChanges != null,
      );
      deliveries.add(data._delivery);
      // Two finite workers claim different eligible FIFO heads transactionally;
      // a slow receipt probe cannot monopolize all mutation scheduling.
      deliveries.add(
        OfflineMutationDelivery(
          store: store,
          partitionId: data.partitionId,
          generation: data.generation,
          remote: data._delivery.remote,
          mutationRemote: data._delivery.mutationRemote,
          clock: data.clock,
          fence: _checkLifetime,
          onHandling: data._completion.notifyHandling,
          onDurable: data._completion.durable,
          authorityBinding: binding.authorityBinding,
          onStorageBlocked: _requestStoragePause,
          onAuthPause: _requestAuthPause,
          onPolicyPause: _requestPolicyPause,
          retryDeadline: _retryDeadline,
          onEndpointSuccess: _endpointSuccess,
          controlFence: _checkControlLifetime,
        ),
      );
      final pause = await store.transaction(
        (tx) async => (
          await tx.replayPausedForAuth(data.partitionId),
          await tx.replayPausedForPolicy(data.partitionId),
        ),
      );
      auth = pause.$1;
      policy = pause.$2;
      ready = true;
      if (incomingChanges != null) {
        incoming = _AutomaticIncoming(this, incomingChanges);
        incoming!.register();
      }
      runtime.retain(
        'watch-expiry',
        OfflineSessionLane.local,
        (_) async => _expireWatches(),
      );
      if (!_credentialLive) auth = true;
      if (auth || policy || !transferAllowed || restoreHeld) {
        _networkJoin = runtime.pauseNetwork();
      }
      await _maintain();
      _changed();
      return this;
    } catch (_) {
      // No capability escapes before validation/owner establishment completes.
      await createdRuntime?.close();
      await createdData?.close();
      try {
        await acquired?.release();
      } finally {
        await closeStore(store);
      }
      rethrow;
    }
  }

  void _checkLifetime() {
    if (ended) throw const OfflineDisposedException();
    _checkControlLifetime();
  }

  void _checkControlLifetime() {
    if (!authorityCurrent() || client.isClosed || (ready && data._invalid)) {
      throw const OfflineDisposedException();
    }
    owner.checkCurrent();
  }

  bool get _credentialLive =>
      credentialExpires == null || dataOrClock().isBefore(credentialExpires!);
  DateTime dataOrClock() => ready ? data.clock() : clock().toUtc();
  bool _credentialInstalling = false;
  bool get _policyAllowsNetwork =>
      !ended &&
      !_credentialInstalling &&
      !suspended &&
      !auth &&
      !policy &&
      !storage &&
      transferAllowed &&
      !restoreHeld &&
      _credentialLive;
  bool get _networkAllowed =>
      _policyAllowsNetwork && !(_backoffUntil?.isAfter(dataOrClock()) ?? false);
  ObservationCause? get _hiddenCause => restoreHeld
      ? ObservationCause.unavailable
      : policy
      ? ObservationCause.permissionDenied
      : auth || !_credentialLive
      ? ObservationCause.unauthenticated
      : incoming?.discontinuous == true
      ? ObservationCause.unavailable
      : null;
  void _checkNetwork() {
    _checkLifetime();
    if (!_credentialLive && !auth) _requestAuthPause();
    if (!_policyAllowsNetwork) throw const OfflineWorkInterruptedException();
  }

  SyncPause get pause => storage
      ? SyncPause.storage
      : restoreHeld
      ? SyncPause.restore
      : auth || !_credentialLive
      ? SyncPause.auth
      : policy
      ? SyncPause.policy
      : !transferAllowed
      ? SyncPause.transfer
      : SyncPause.none;
  SyncLifecycle get lifecycle => ended
      ? SyncLifecycle.closed
      : suspended
      ? SyncLifecycle.suspended
      : SyncLifecycle.foreground;
  SyncReachability get reachability =>
      (_backoffUntil?.isAfter(dataOrClock()) ?? false)
      ? SyncReachability.backoff
      : runtime.finiteNetworkInFlightCount > 0
      ? (_failures > 0
            ? SyncReachability.recovering
            : SyncReachability.synchronizing)
      : _endpointObserved
      ? SyncReachability.idle
      : SyncReachability.unknown;
  void _healthChanged() {
    if (ready && !ended) data._observations.invalidateKey('sync');
  }

  void _endpointSuccess() {
    _endpointObserved = true;
    // A sibling success cannot erase a later throttling deadline.
    if (!(_backoffUntil?.isAfter(dataOrClock()) ?? false)) _failures = 0;
    _healthChanged();
  }

  void _readEndpoint(LanternException? failure) {
    if (failure == null) {
      _endpointSuccess();
      return;
    }
    if (failure is LanternUnauthenticatedException ||
        failure is LanternPermissionDeniedException ||
        failure is LanternCanceledException ||
        failure is LanternInvalidArgumentException) {
      return;
    }
    _retryDeadline(failure);
  }

  DateTime _retryDeadline(Object? failure) {
    final now = data.clock();
    final exponent = _failures;
    _failures = min(_failures + 1, 9);
    final ceiling = min(30000, 100 * (1 << exponent));
    final delay = 100 + _retryRandom.nextInt(max(1, ceiling - 99));
    var due = now.add(Duration(milliseconds: delay));
    final retryAfter = _supportedRetryAfter(failure, now);
    if (retryAfter != null && retryAfter.isAfter(due)) due = retryAfter;
    if (_backoffUntil != null && _backoffUntil!.isAfter(due)) {
      due = _backoffUntil!;
    }
    _backoffUntil = due;
    runtime.deferNetworkUntil(due);
    _healthChanged();
    return due;
  }

  Future<void> setTransferAllowed(bool allowed) async {
    _checkLifetime();
    transferAllowed = allowed;
    if (!allowed) {
      _networkJoin = runtime.pauseNetwork();
      await _networkJoin;
    } else {
      await runtime.resumeNetwork(when: () => _policyAllowsNetwork);
    }
    _checkLifetime();
    _healthChanged();
    _changed();
  }

  void endLifetime() {
    if (ended) return;
    // A store/account generation change closes the synchronous gate now. The
    // asynchronous close is retained so a later handle.close observes failures.
    unawaited(close().catchError((Object _) {}));
  }

  void _requestAuthPause() {
    if (ended || auth) return;
    auth = true;
    _networkJoin = runtime.pauseNetwork();
    if (ready) data._observations.invalidate();
  }

  void _requestPolicyPause() {
    if (ended || policy) return;
    policy = true;
    _networkJoin = runtime.pauseNetwork();
    if (ready) data._observations.invalidate();
  }

  void _requestStoragePause() {
    if (ended || storage) return;
    storage = true;
    _networkJoin = runtime.pauseNetwork();
    if (ready) {
      data._storageBlocked = true;
      data._observations.invalidate();
    }
  }

  Future<void> _pauseFromRead(ObservationCause cause) async {
    incoming?.requireBootstrap();
    if (cause == ObservationCause.unauthenticated) {
      _requestAuthPause();
    } else {
      _requestPolicyPause();
    }
    try {
      await store.transaction((tx) async {
        _checkLifetime();
        await tx.resetScopedChangeCursor(data.partitionId, null);
        if (cause == ObservationCause.unauthenticated) {
          await tx.setReplayPausedForAuth(
            data.partitionId,
            true,
            credentialGeneration: runtime.credentialGeneration,
          );
        } else {
          await tx.setReplayPausedForPolicy(data.partitionId, true);
        }
        _checkLifetime();
      });
    } catch (_) {
      _requestStoragePause();
      rethrow;
    }
  }

  // At most one entry per bounded coordinator query; last unsubscribe removes
  // it. A single retained local slot handles TTL even when network is paused.
  final Map<String, DateTime> _watchExpiries = {};
  void _expireWatches() {
    _checkLifetime();
    _watchExpiries.removeWhere((_, due) => !due.isAfter(data.clock()));
    data._observations.invalidate();
  }

  void trackExpiry(String query, Object? value) {
    if (ended) return;
    DateTime? earliest;
    void consider(DateTime? at) {
      if (at != null &&
          at.isAfter(data.clock()) &&
          (earliest == null || at.isBefore(earliest!))) {
        earliest = at;
      }
    }

    void snapshot(Object? object) {
      if (object is LanternTraversalSnapshot) {
        consider(data._localExpiries[object]);
      }
      if (object is LanternSnapshot<Object?>) {
        consider(data._localExpiries[object]);
        final confirmed = object.confirmed.value;
        final view = object.view;
        if (confirmed is Vertex) consider(confirmed.expiration);
        if (confirmed is Edge) consider(confirmed.expiration);
        if (view is Vertex) consider(view.expiration);
        if (view is Edge) consider(view.expiration);
      }
    }

    if (value is List<Object?>) {
      for (final v in value) {
        snapshot(v);
      }
    } else {
      snapshot(value);
    }
    if (earliest == null) {
      _watchExpiries.remove(query);
    } else {
      _watchExpiries[query] = earliest!;
    }
    runtime.clearPending('watch-expiry');
    if (_watchExpiries.isNotEmpty) {
      final next = _watchExpiries.values.reduce(
        (a, b) => a.isBefore(b) ? a : b,
      );
      runtime.enqueue(
        'watch-expiry',
        OfflineSessionLane.local,
        (_) async => _expireWatches(),
        dueAt: next,
        retain: true,
      );
    }
  }

  void _changed() {
    if (!ready || ended) return;
    incoming?.wake();
    if (!_policyAllowsNetwork) {
      runtime.enqueue(
        'maintenance',
        OfflineSessionLane.local,
        (_) => _maintain(),
      );
      return;
    }
    for (var i = 0; i < deliveries.length; i++) {
      _queueWorker(i);
    }
    runtime.enqueue(
      'maintenance',
      OfflineSessionLane.local,
      (_) => _maintain(),
    );
  }

  void _queueWorker(int index, {DateTime? dueAt}) {
    runtime.enqueue('delivery:$index', OfflineSessionLane.reconcile, (
      work,
    ) async {
      _checkNetwork();
      await deliveries[index].dispatchPass(limit: 1, work: work);
      work.checkCurrent();
      if (!_policyAllowsNetwork) return;
      final hints = await _hints();
      work.checkCurrent();
      if (hints.nextAttemptAt != null) {
        final floor = data.clock().add(runtime.debounce);
        _queueWorker(
          index,
          dueAt: hints.nextAttemptAt!.isAfter(floor)
              ? hints.nextAttemptAt
              : floor,
        );
      }
    }, dueAt: dueAt);
  }

  Future<OfflineAutomaticSchedule> _hints() => store.transaction((tx) async {
    _checkLifetime();
    final now = await tx.effectiveTime(data.partitionId, data.clock());
    return tx.automaticSchedule(
      data.partitionId,
      now: now,
      maxAge: deliveries.first.maxAge,
    );
  });
  Future<void> _maintain() async {
    _checkLifetime();
    if (!_credentialLive && !auth) _requestAuthPause();
    if (auth) {
      await store.transaction((tx) async {
        _checkLifetime();
        if (!await tx.replayPausedForAuth(data.partitionId)) {
          await tx.setReplayPausedForAuth(
            data.partitionId,
            true,
            credentialGeneration: runtime.credentialGeneration,
          );
        }
      });
    }
    await deliveries.first.expireLocally();
    final hints = await _hints();
    DateTime? due = deliveries.first.moreLocalExpiry
        ? data.clock().add(runtime.debounce)
        : hints.nextLocalExpiry;
    if (_credentialLive &&
        credentialExpires != null &&
        (due == null || credentialExpires!.isBefore(due))) {
      due = credentialExpires;
    }
    if (due != null && !ended) {
      runtime.enqueue(
        'maintenance',
        OfflineSessionLane.local,
        (_) => _maintain(),
        dueAt: due,
      );
    }
  }

  Future<void> suspend() async {
    suspended = true;
    _healthChanged();
    await runtime.suspend();
    _checkLifetime();
  }

  Future<void> resume() async {
    _checkLifetime();
    await runtime.suspend();
    await _maintain();
    suspended = false;
    _healthChanged();
    data._observations.resume();
    await runtime.resumeNetwork(when: () => _policyAllowsNetwork);
    _checkLifetime();
    runtime.resume();
    _changed();
  }

  Future<bool> acceptCredentials(OfflineVerifiedCredentials event) async {
    if (event._accepted) return false;
    if (_credentialChange != null) throw const OfflineArgumentException();
    _checkLifetime();
    if (event.binding.stableIdentity != binding.stableIdentity ||
        event.binding.authorityBinding != binding.authorityBinding) {
      await close();
      return false;
    }
    if (event.binding.expiresAt != null &&
        !data.clock().isBefore(event.binding.expiresAt!)) {
      return false;
    }
    event._accepted = true;
    final done = Completer<void>();
    _credentialChange = done.future;
    _credentialInstalling = true;
    try {
      _networkJoin = runtime.pauseNetwork();
      await _networkJoin;
      _checkLifetime();
      await event.install();
      _checkLifetime();
      runtime.advanceCredentialGeneration();
      credentialExpires = event.binding.expiresAt;
      await store.transaction((tx) async {
        _checkLifetime();
        final now = await tx.effectiveTime(data.partitionId, data.clock());
        OfflineOutboxCursor? cursor;
        do {
          final page = await tx.scanOutbox(
            data.partitionId,
            after: cursor,
            limit: 32,
          );
          for (final row in page.records) {
            final op = await tx.getOperation(data.partitionId, row.operationId);
            if (row.state != OfflineOutboxState.enqueued ||
                op?.items[row.itemIndex].state !=
                    OfflineWriteState.pausedForAuth) {
              continue;
            }
            final eligible = row.copyWith(clearDiagnosticCode: true);
            await tx.updateOutbox(eligible);
            await advanceOfflineMutation(
              tx,
              eligible,
              OfflineWriteState.locallyCommitted,
              now,
            );
          }
          cursor = page.hasMore ? page.nextCursor : null;
        } while (cursor != null);
        await tx.setReplayPausedForAuth(data.partitionId, false);
        _checkLifetime();
      });
      auth = false;
      _credentialInstalling = false;
      data._observations.resume();
      await runtime.resumeNetwork(when: () => _policyAllowsNetwork);
      await _maintain();
      _changed();
      return true;
    } catch (_) {
      _requestStoragePause();
      rethrow;
    } finally {
      _credentialInstalling = false;
      _credentialChange = null;
      done.complete();
    }
  }

  Future<void> close() {
    if (_closing != null) return _closing!;
    ended = true;
    incoming?._resetRecovery();
    return _closing = (() async {
      await runtime.close();
      await _credentialChange;
      for (final delivery in deliveries) {
        await delivery.close();
      }
      await data.close();
      // Join admissions/local captures accepted before the lifetime gate closed.
      await store.transaction((_) async {});
      // A failed join never reaches release. Qualified adapters retain native
      // ownership through store close even after invalidating the owner row.
      Object? failure;
      StackTrace? trace;
      Future<void> join(Future<void> Function() close) async {
        try {
          await close();
        } catch (error, stack) {
          failure ??= error;
          trace ??= stack;
        }
      }

      await join(owner.release);
      await join(() => closeStore(store));
      await join(client.close);
      if (failure != null) Error.throwWithStackTrace(failure!, trace!);
      // Retain the bounded registry reservation on any failed teardown. A
      // transport cleanup error cannot erase a preceding durable-store error.
      onReleased();
    })();
  }
}

final class _SessionMutations implements LanternMutationControl {
  _SessionMutations(this.handle);
  final OfflineLanternSession handle;
  LanternMutationControl get control => handle._data.mutations;
  @override
  Future<MutationTicket<T>> submit<T>(
    MutationRequest<T> request, {
    MutationOptions? options,
  }) async {
    final target = control;
    if (options?.wait == null) return target.submit(request, options: options);
    final waiter = _HandleWait(handle, options!.wait!);
    try {
      final ticket = await target.submit(
        request,
        options: MutationOptions(
          operationId: options.operationId,
          admissionCancellation: options.admissionCancellation,
          wait: waiter.options,
        ),
      );
      return MutationTicket(
        reference: ticket.reference,
        admittedAt: ticket.admittedAt,
        durability: ticket.durability,
        completion: waiter.track(ticket.completion!),
      );
    } catch (_) {
      waiter.dispose();
      rethrow;
    }
  }

  @override
  Future<List<MutationTicket<Object?>>> submitGroup(MutationGroup group) =>
      control.submitGroup(group);
  @override
  Future<MutationObservation<T>> reobserve<T>(
    MutationReference<T> reference, {
    WaitOptions? options,
  }) {
    final target = control;
    final waiter = _HandleWait(handle, options ?? const WaitOptions());
    return waiter.track(target.reobserve(reference, options: waiter.options));
  }

  @override
  Future<MutationStatus<Object?>> getStatus(OperationId id) =>
      control.getStatus(id);
  @override
  Stream<MutationStatus<Object?>> watchStatus(OperationId id) =>
      handle._watch(() => control.watchStatus(id));
}

final class _HandleWait {
  _HandleWait(this.handle, WaitOptions source) {
    detachOwner = handle._waiterLifetime.listen((_) => token.cancel());
    detachCaller = source.cancellation?.listen((_) => token.cancel());
    if (handle._closed || (source.cancellation?.isCanceled ?? false)) {
      token.cancel();
    }
    options = WaitOptions(timeout: source.timeout, cancellation: token);
  }
  final OfflineLanternSession handle;
  final LanternCancellationToken token = LanternCancellationToken();
  late final WaitOptions options;
  late final void Function() detachOwner;
  void Function()? detachCaller;
  void dispose() {
    detachOwner();
    detachCaller?.call();
  }

  Future<T> track<T>(Future<T> source) {
    final result = (() async {
      try {
        return await source;
      } on LanternDataException catch (error) {
        if (handle._closed && error.reason == DataFailure.waitCanceled) {
          throw LanternDataException(
            DataFailure.closed,
            reference: error.reference,
            dispatchCertainty: error.dispatchCertainty,
          );
        }
        rethrow;
      } finally {
        dispose();
      }
    })();
    late final Future<void> joined;
    joined = result
        .then<void>((_) {}, onError: (Object _, StackTrace _) {})
        .whenComplete(() {
          handle._waiterJoins.remove(joined);
        });
    handle._waiterJoins.add(joined);
    return result;
  }
}

// Accept only bounded, content-free standard Retry-After forms. Raw metadata is
// never copied to persistence or status. Longer deadlines survive in each row.
DateTime? _supportedRetryAfter(Object? error, DateTime now) {
  if (error is OfflineRemoteFailure) error = error.cause;
  if (error is! LanternException) return null;
  for (final metadata in [error.headers, error.trailers, error.metadata]) {
    for (final entry in metadata.entries) {
      if (entry.key.toLowerCase() != 'retry-after' || entry.value.length != 1) {
        continue;
      }
      final value = entry.value.single.trim();
      if (value.length > 64) continue;
      if (RegExp(r'^\d{1,9}$').hasMatch(value)) {
        return now.add(Duration(seconds: int.parse(value)));
      }
      final match = RegExp(
        r'^[A-Z][a-z]{2}, (\d{2}) ([A-Z][a-z]{2}) (\d{4}) (\d{2}):(\d{2}):(\d{2}) GMT$',
      ).firstMatch(value);
      if (match == null) continue;
      const months = [
        'Jan',
        'Feb',
        'Mar',
        'Apr',
        'May',
        'Jun',
        'Jul',
        'Aug',
        'Sep',
        'Oct',
        'Nov',
        'Dec',
      ];
      final month = months.indexOf(match[2]!) + 1;
      final day = int.parse(match[1]!), year = int.parse(match[3]!);
      final hour = int.parse(match[4]!),
          minute = int.parse(match[5]!),
          second = int.parse(match[6]!);
      if (month == 0 ||
          day < 1 ||
          day > 31 ||
          hour > 23 ||
          minute > 59 ||
          second > 59) {
        continue;
      }
      final at = DateTime.utc(year, month, day, hour, minute, second);
      if (at.day == day && at.isAfter(now)) return at;
    }
  }
  return null;
}
