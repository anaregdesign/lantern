import 'dart:async';
import 'dart:convert';
import 'dart:math';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';
import 'automatic_owner.dart';
import 'bound_mutation_remote.dart';
import 'bound_writes.dart';
import 'errors.dart';
import 'mutation_admission.dart';
import 'mutation_delivery.dart';
import 'mutation_state.dart';
import 'operation_completion.dart';
import 'pending_projection.dart';
import 'remote.dart';
import 'session_runtime.dart';
import 'store.dart';
import 'types.dart';

part 'session.dart';

/// Common bound Offline facade. Admission never acquires tokens or performs RPC.
/// Automatic ownership is composed by #1769; applications use ordinary CRUD.
final class OfflineLanternDataClient
    with OfflineBoundWrites
    implements LanternDataClient {
  OfflineLanternDataClient._(
    this._client,
    this.store,
    this.binding,
    this.partitionId,
    this.generation,
    this.durability,
    this.cacheReads,
    this.clock,
    this.authorityCurrent,
    OfflineReceiptRemote? receiptRemote,
    OfflineBoundMutationRemote? mutationRemote,
    this._session,
  ) {
    _observations = LanternObservationCoordinator(
      scheduler: _session?.runtime.observationScheduler,
    );
    _direct = _session == null
        ? DirectLanternDataClient(_client, binding: binding)
        : LanternAdapterReads.bound(
            _client,
            binding: binding,
            checkAuthority: _session._checkNetwork,
            onEndpoint: _session._readEndpoint,
          );
    _completion = OfflineOperationCompletion(
      store: store,
      partitionId: partitionId,
      clock: clock,
      fence: _fence,
      generation: generation,
      authorityBinding: binding.authorityBinding,
    );
    _admission = OfflineMutationAdmission(
      store: store,
      client: _client,
      partitionId: partitionId,
      generation: generation,
      clock: clock,
      fence: _fence,
      authorityBinding: binding.authorityBinding,
    );
    _mutations = _OfflineMutationControl(this);
    _delivery = OfflineMutationDelivery(
      store: store,
      partitionId: partitionId,
      generation: generation,
      remote:
          receiptRemote ??
          LanternClientOfflineRemote(
            _client,
            attemptTimeout: _session == null
                ? null
                : const Duration(seconds: 15),
          ),
      mutationRemote: mutationRemote ?? LanternBoundMutationRemote(_client),
      clock: clock,
      fence: _fence,
      onHandling: _completion.notifyHandling,
      onDurable: _completion.durable,
      authorityBinding: binding.authorityBinding,
      onAuthPause: _session?._requestAuthPause,
      onPolicyPause: _session?._requestPolicyPause,
      retryDeadline: _session?._retryDeadline,
      onEndpointSuccess: _session?._endpointSuccess,
      controlFence: _session?._checkControlLifetime,
      onStorageBlocked: () {
        _session?._requestStoragePause();
        _storageBlocked = true;
        _observations.invalidate();
      },
    );
    _changes = store.changes(partitionId).listen((c) {
      if (c.generation != generation ||
          c.authorityBinding != binding.authorityBinding) {
        _invalid = true;
        _session?.endLifetime();
      }
      _session?._changed();
      _completion.invalidate();
      if (c.observationInvalidated) _observations.invalidate();
    });
  }

  /// Opens validated storage before any remote work. Scope derives from verified identity.
  static Future<OfflineLanternDataClient> open({
    required LanternClient client,
    required OfflineStore store,
    required LanternSessionBinding binding,
    required AdmissionDurability durability,
    bool cacheReads = true,
    OfflineClock? clock,
    bool Function()? authorityCurrent,
    OfflineReceiptRemote? receiptRemote,
    OfflineBoundMutationRemote? mutationRemote,
  }) => _open(
    client: client,
    store: store,
    binding: binding,
    durability: durability,
    cacheReads: cacheReads,
    clock: clock,
    authorityCurrent: authorityCurrent,
    receiptRemote: receiptRemote,
    mutationRemote: mutationRemote,
  );

  static Future<OfflineLanternDataClient> _open({
    required LanternClient client,
    required OfflineStore store,
    required LanternSessionBinding binding,
    required AdmissionDurability durability,
    bool cacheReads = true,
    OfflineClock? clock,
    bool Function()? authorityCurrent,
    OfflineReceiptRemote? receiptRemote,
    OfflineBoundMutationRemote? mutationRemote,
    _SessionShared? session,
  }) async {
    if (durability == AdmissionDurability.none) {
      throw const OfflineArgumentException();
    }
    final now = clock ?? () => DateTime.now().toUtc();
    final scope = binding.stableIdentity;
    final opened = await store.transaction((tx) async {
      final at = await tx.effectiveTime(scope, now().toUtc());
      await tx.bindAuthority(scope, binding.authorityBinding);
      return (generation: await tx.generation(scope), at: at);
    });
    var floor = opened.at;
    DateTime effectiveClock() {
      final sample = now().toUtc();
      if (sample.isAfter(floor)) floor = sample;
      return floor;
    }

    return OfflineLanternDataClient._(
      client,
      store,
      binding,
      scope,
      opened.generation,
      durability,
      cacheReads,
      effectiveClock,
      authorityCurrent ?? () => true,
      receiptRemote,
      mutationRemote,
      session,
    );
  }

  final LanternClient _client;
  final _SessionShared? _session;

  /// Transactional local state; partition is not a Server authorization claim.
  final OfflineStore store;
  @override
  final LanternSessionBinding binding;

  /// Stable local scope; it is not a wire tenant or an authorization grant.
  final String partitionId;

  /// Captured account/store generation fencing this lifetime.
  final int generation;

  /// Truthful local admission durability, independent of Server handling.
  final AdmissionDurability durability;

  /// Whether eligible confirmed reads may be retained in this profile.
  final bool cacheReads;

  /// Injected wall clock, clamped against persisted observation high-water.
  final OfflineClock clock;

  /// Synchronous application-verified authority fence for this immutable lifetime.
  final bool Function() authorityCurrent;
  late final DirectLanternDataClient _direct;
  late final OfflineMutationAdmission _admission;
  late final OfflineOperationCompletion _completion;
  late final _OfflineMutationControl _mutations;
  late final StreamSubscription<OfflineStoreChange> _changes;
  late final LanternObservationCoordinator _observations;

  /// Qualified automatic-owner delivery port, excluded from ordinary CRUD semantics.
  late final OfflineMutationDelivery _delivery;
  bool _closed = false, _invalid = false, _storageBlocked = false;
  int _sequence = 0;
  // Weak metadata travels with an immutable snapshot until the coordinator
  // accepts it. Rejected captures cannot update the active query's deadline.
  final _localExpiries = Expando<DateTime>();
  @override
  LanternMutationControl get mutations => _mutations;
  void _fence() {
    _session?._checkLifetime();
    if (_closed || _invalid) {
      throw const LanternDataException(DataFailure.closed);
    }
    if (!authorityCurrent() ||
        _session == null &&
            binding.expiresAt != null &&
            !clock().toUtc().isBefore(binding.expiresAt!)) {
      throw const LanternDataException(DataFailure.closed);
    }
  }

  Future<LanternSnapshot<T>> _local<T>(
    OfflineEntityKey key, {
    ConfirmedEvidence<T>? evidence,
    bool serverOnly = false,
  }) async {
    _fence();
    final result = await store.transaction((tx) async {
      if (await tx.generation(partitionId) != generation ||
          await tx.authorityBinding(partitionId) != binding.authorityBinding) {
        throw const LanternDataException(DataFailure.closed);
      }
      final now = await tx.effectiveTime(partitionId, clock().toUtc());
      final record = cacheReads ? await tx.getCache(partitionId, key) : null;
      final pause = _session?._hiddenCause;
      if (pause != null) {
        evidence = ConfirmedEvidence<T>(
          state: ConfirmedState.unknown,
          origin: ObservationOrigin.none,
          observedAt: now,
          freshness: ObservationFreshness.unavailable,
          cause: pause,
        );
      }
      final pending = await tx.outboxForKey(partitionId, key);
      _fence();
      final projected = projectOfflinePending<T>(
        evidence == null
            ? offlineConfirmed<T>(record, now)
            : offlineEvidenceAt(evidence!, now),
        pending,
        now,
        sequence: ++_sequence,
        serverOnly: serverOnly,
      );
      DateTime? expiry;
      void consider(DateTime? at) {
        if (at != null &&
            at.isAfter(now) &&
            (expiry == null || at.isBefore(expiry!))) {
          expiry = at;
        }
      }

      consider(record?.expiration);
      consider(record?.missingUntil);
      for (final row in pending) {
        consider(row.intent.expiration);
      }
      final confirmed = evidence?.value;
      if (confirmed is Vertex) consider(confirmed.expiration);
      if (confirmed is Edge) consider(confirmed.expiration);
      if (evidence?.state == ConfirmedState.missing) {
        consider(evidence!.observedAt.add(const Duration(seconds: 30)));
      }
      if (expiry != null) _localExpiries[projected] = expiry;
      return projected;
    });
    _fence();
    return result;
  }

  Future<LanternSnapshot<T>> _localForSource<T>(
    OfflineEntityKey key,
    ReadSource? source,
  ) => _local<T>(
    key,
    serverOnly: source == ReadSource.serverOnly,
    evidence: source == ReadSource.serverOnly
        ? ConfirmedEvidence<T>(
            state: ConfirmedState.unknown,
            origin: ObservationOrigin.none,
            observedAt: clock().toUtc(),
            freshness: ObservationFreshness.unavailable,
            cause: _session?._hiddenCause ?? ObservationCause.unavailable,
          )
        : null,
  );

  Future<LanternSnapshot<T>> _get<T>(
    OfflineEntityKey key,
    GetOptions? options,
    Future<LanternSnapshot<T>> Function(LanternCancellationToken?) fetch,
  ) async {
    final o = options ?? const GetOptions();
    LanternAdapterCancellation.checkCanceled(o.cancellation);
    final local = await _localForSource<T>(key, o.source);
    LanternAdapterCancellation.checkCanceled(o.cancellation);
    if (o.source == ReadSource.cacheOnly ||
        o.source == ReadSource.automatic &&
            (local.projection == PendingProjection.exactPendingPut ||
                local.projection == PendingProjection.pendingDelete)) {
      return local;
    }
    final session = _session;
    if (session != null && !session._networkAllowed) return local;
    Future<LanternSnapshot<T>> attempt(OfflineSessionWork? work) async {
      final epoch = await store.transaction(
        (tx) => tx.changeEpoch(partitionId),
      );
      work?.checkCurrent();
      final server = await fetch(work?.cancellation ?? o.cancellation);
      work?.checkCurrent();
      if (session != null &&
          (server.confirmed.cause == ObservationCause.unauthenticated ||
              server.confirmed.cause == ObservationCause.permissionDenied)) {
        await session._pauseFromRead(server.confirmed.cause!);
        return _localForSource<T>(key, o.source);
      }
      _fence();
      if (server.confirmed.state == ConfirmedState.unknown) {
        if (server.confirmed.cause == ObservationCause.permissionDenied ||
            server.confirmed.cause == ObservationCause.unauthenticated ||
            o.source == ReadSource.serverOnly) {
          if (server.confirmed.cause == ObservationCause.permissionDenied ||
              server.confirmed.cause == ObservationCause.unauthenticated) {
            await store.transaction((tx) async {
              await tx.invalidateMutationCache(partitionId, key);
              _fence();
            });
          }
          return _local<T>(key, evidence: server.confirmed, serverOnly: true);
        }
        final fallback = await _local<T>(key);
        if (fallback.confirmed.state == ConfirmedState.unknown) {
          return _local<T>(key, evidence: server.confirmed);
        }
        return fallback;
      }
      if (cacheReads) {
        await store.transaction((tx) async {
          work?.checkCurrent();
          if (await tx.generation(partitionId) != generation ||
              await tx.changeEpoch(partitionId) != epoch) {
            return;
          }
          final c = server.confirmed;
          if (c.state == ConfirmedState.present) {
            await tx.putCache(
              partitionId,
              OfflineCacheRecord.value(
                partitionId: partitionId,
                generation: generation,
                key: key,
                entity: c.value as Object,
                validatedAt: c.observedAt,
                lastAccessAt: clock().toUtc(),
              ),
            );
          } else if (c.state == ConfirmedState.missing) {
            await tx.putCache(
              partitionId,
              OfflineCacheRecord.missing(
                partitionId: partitionId,
                generation: generation,
                key: key,
                validatedAt: c.observedAt,
                lastAccessAt: clock().toUtc(),
                missingUntil: c.observedAt.add(const Duration(seconds: 30)),
              ),
            );
          }
          work?.checkCurrent();
          _fence();
        });
      }
      work?.checkCurrent();
      _fence();
      if (await store.transaction((tx) => tx.changeEpoch(partitionId)) !=
          epoch) {
        return _local<T>(
          key,
          evidence: ConfirmedEvidence<T>(
            state: ConfirmedState.unknown,
            origin: ObservationOrigin.none,
            observedAt: clock().toUtc(),
            freshness: ObservationFreshness.invalidated,
          ),
          serverOnly: o.source == ReadSource.serverOnly,
        );
      }
      return _local<T>(
        key,
        evidence: server.confirmed,
        serverOnly: o.source == ReadSource.serverOnly,
      );
    }

    if (session == null) return attempt(null);
    try {
      return await session.runtime.readOnce(
        attempt,
        cancellation: o.cancellation,
      );
    } on OfflineWorkInterruptedException {
      _fence();
      LanternAdapterCancellation.checkCanceled(o.cancellation);
      final fallback = await _localForSource<T>(key, o.source);
      LanternAdapterCancellation.checkCanceled(o.cancellation);
      return fallback;
    }
  }

  @override
  Future<LanternSnapshot<Vertex>> getVertex(
    String key, {
    GetOptions? options,
  }) => _get(
    OfflineEntityKey.vertex(key),
    options,
    (token) => _direct.getVertex(
      key,
      options: GetOptions(
        source: ReadSource.serverOnly,
        timeout: options?.timeout,
        cancellation: token,
      ),
    ),
  );
  @override
  Future<List<LanternSnapshot<Vertex>>> getVertices(
    Iterable<String> keys, {
    GetOptions? options,
  }) async {
    final input = keys.take(LanternCrud.maxBatchSize + 1).toList();
    if (input.length > LanternCrud.maxBatchSize) {
      throw const OfflineArgumentException();
    }
    final observed = <String, LanternSnapshot<Vertex>>{};
    final result = <LanternSnapshot<Vertex>>[];
    for (final key in input) {
      result.add(observed[key] ??= await getVertex(key, options: options));
    }
    return result;
  }

  @override
  Future<LanternSnapshot<Edge>> getEdge(EdgeRef edge, {GetOptions? options}) =>
      _get(
        OfflineEntityKey.edge(edge.tail, edge.head),
        options,
        (token) => _direct.getEdge(
          edge,
          options: GetOptions(
            source: ReadSource.serverOnly,
            timeout: options?.timeout,
            cancellation: token,
          ),
        ),
      );
  @override
  Future<List<LanternSnapshot<Edge>>> getEdges(
    Iterable<EdgeRef> edges, {
    GetOptions? options,
  }) async {
    final input = edges.take(LanternCrud.maxBatchSize + 1).toList();
    if (input.length > LanternCrud.maxBatchSize) {
      throw const OfflineArgumentException();
    }
    final observed = <OfflineEntityKey, LanternSnapshot<Edge>>{};
    final result = <LanternSnapshot<Edge>>[];
    for (final edge in input) {
      final key = OfflineEntityKey.edge(edge.tail, edge.head);
      result.add(observed[key] ??= await getEdge(edge, options: options));
    }
    return result;
  }

  @override
  Future<LanternTraversalSnapshot> illuminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    TraversalReadOptions? options,
  }) async {
    _fence();
    Future<LanternTraversalSnapshot> attempt(OfflineSessionWork? work) async {
      final result = await _direct.illuminate(
        seed,
        traversal: traversal,
        weighting: weighting,
        vertexPrefix: vertexPrefix,
        options: TraversalReadOptions(
          source: options?.source ?? ReadSource.automatic,
          timeout: options?.timeout,
          cancellation: work?.cancellation ?? options?.cancellation,
        ),
      );
      work?.checkCurrent();
      _fence();
      final pending = await store.transaction((tx) => tx.outbox(partitionId));
      work?.checkCurrent();
      _fence();
      return LanternTraversalSnapshot(
        confirmed: result.confirmed,
        query: result.query,
        pending: offlinePendingSummary(pending),
        sequence: ++_sequence,
      );
    }

    final session = _session;
    if (session == null || options?.source == ReadSource.cacheOnly) {
      return attempt(null);
    }
    if (!session._networkAllowed) {
      return LanternTraversalSnapshot(
        confirmed: _unknown<Graph>(false).confirmed,
        query: LanternTraversalQuery(
          seed,
          traversal: traversal,
          weighting: weighting,
          vertexPrefix: vertexPrefix,
        ),
        pending: offlinePendingSummary(
          await store.transaction((tx) => tx.outbox(partitionId)),
        ),
        sequence: ++_sequence,
      );
    }
    return session.runtime.readOnce(
      attempt,
      traversal: true,
      cancellation: options?.cancellation,
    );
  }

  Stream<T> _watch<T>(
    String key,
    T Function(bool) initial,
    Future<T> Function(LanternCancellationToken) load,
    T Function(T, bool) lost,
    WatchOptions? options, {
    Future<T> Function()? captureLocal,
  }) {
    _fence();
    final o = options ?? const WatchOptions();
    if (o.coverage == WatchCoverage.strictLive) {
      throw const LanternDataException(DataFailure.unsupported);
    }
    if (o.pollInterval <= Duration.zero) throw const OfflineArgumentException();
    final canonical = '$key:${o.pollInterval.inMicroseconds}:${o.source.name}';
    return _observations.observe(
      canonical,
      initial: initial,
      captureLocal: captureLocal,
      onCurrent: _session == null
          ? null
          : (value) => _session.trackExpiry(canonical, value),
      onStopped: _session == null
          ? null
          : () => _session.trackExpiry(canonical, null),
      load: load,
      markLost: lost,
      interval: o.pollInterval,
      pauseAfter: _deniedObservation,
      traversal: key.startsWith('t:'),
      local: o.source == ReadSource.cacheOnly,
    );
  }

  LanternSnapshot<T> _unknown<T>(bool lost) => LanternSnapshot(
    confirmed: ConfirmedEvidence(
      state: ConfirmedState.unknown,
      origin: ObservationOrigin.none,
      observedAt: clock().toUtc(),
      freshness: ObservationFreshness.unavailable,
    ),
    observationLost: lost,
  );
  LanternSnapshot<T> _lost<T>(LanternSnapshot<T> s, bool lost) =>
      LanternSnapshot(
        confirmed: s.confirmed,
        view: s.view,
        projection: s.projection,
        pending: s.pending,
        sequence: s.sequence,
        observationLost: lost,
        observationMode: ObservationMode.pollingObservation,
      );
  @override
  Stream<LanternSnapshot<Vertex>> watchVertex(
    String key, {
    WatchOptions? options,
  }) => _watch(
    'v:$key:${options?.source}',
    _unknown<Vertex>,
    (cancellation) => getVertex(
      key,
      options: GetOptions(
        source: options?.source ?? ReadSource.automatic,
        cancellation: cancellation,
      ),
    ),
    _lost,
    options,
    captureLocal: () =>
        _localForSource<Vertex>(OfflineEntityKey.vertex(key), options?.source),
  );
  @override
  Stream<LanternSnapshot<Edge>> watchEdge(
    EdgeRef edge, {
    WatchOptions? options,
  }) => _watch(
    'e:${edge.tail.length}:${edge.tail}:${edge.head}:${options?.source}',
    _unknown<Edge>,
    (cancellation) => getEdge(
      edge,
      options: GetOptions(
        source: options?.source ?? ReadSource.automatic,
        cancellation: cancellation,
      ),
    ),
    _lost,
    options,
    captureLocal: () => _localForSource<Edge>(
      OfflineEntityKey.edge(edge.tail, edge.head),
      options?.source,
    ),
  );
  @override
  Stream<List<LanternSnapshot<Vertex>>> watchVertices(
    Iterable<String> keys, {
    WatchOptions? options,
  }) {
    final input = List<String>.unmodifiable(
      keys.take(LanternCrud.maxBatchSize + 1),
    );
    if (input.length > LanternCrud.maxBatchSize) {
      throw const OfflineArgumentException();
    }
    return _watch(
      'vs:${jsonEncode(input)}:${options?.source}',
      (lost) => [for (final _ in input) _unknown<Vertex>(lost)],
      (cancellation) => getVertices(
        input,
        options: GetOptions(
          source: options?.source ?? ReadSource.automatic,
          cancellation: cancellation,
        ),
      ),
      (s, lost) => [for (final v in s) _lost(v, lost)],
      options,
      captureLocal: () async => [
        for (final key in input)
          await _localForSource<Vertex>(
            OfflineEntityKey.vertex(key),
            options?.source,
          ),
      ],
    );
  }

  @override
  Stream<List<LanternSnapshot<Edge>>> watchEdges(
    Iterable<EdgeRef> edges, {
    WatchOptions? options,
  }) {
    final input = List<EdgeRef>.unmodifiable(
      edges.take(LanternCrud.maxBatchSize + 1),
    );
    if (input.length > LanternCrud.maxBatchSize) {
      throw const OfflineArgumentException();
    }
    return _watch(
      'es:${jsonEncode([for (final e in input) OfflineEntityKey.edge(e.tail, e.head).canonical])}:${options?.source}',
      (lost) => [for (final _ in input) _unknown<Edge>(lost)],
      (cancellation) => getEdges(
        input,
        options: GetOptions(
          source: options?.source ?? ReadSource.automatic,
          cancellation: cancellation,
        ),
      ),
      (s, lost) => [for (final v in s) _lost(v, lost)],
      options,
      captureLocal: () async => [
        for (final edge in input)
          await _localForSource<Edge>(
            OfflineEntityKey.edge(edge.tail, edge.head),
            options?.source,
          ),
      ],
    );
  }

  @override
  Stream<LanternTraversalSnapshot> watchIlluminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    WatchOptions? options,
  }) {
    final query = LanternTraversalQuery(
      seed,
      traversal: traversal,
      weighting: weighting,
      vertexPrefix: vertexPrefix,
    );
    return _watch(
      't:${query.canonicalKey}:${options?.source}',
      (lost) => LanternTraversalSnapshot(
        confirmed: _unknown<Graph>(lost).confirmed,
        observationLost: lost,
        query: query,
      ),
      (cancellation) => illuminate(
        seed,
        traversal: traversal,
        weighting: weighting,
        vertexPrefix: vertexPrefix,
        options: TraversalReadOptions(
          source: options?.source ?? ReadSource.automatic,
          cancellation: cancellation,
        ),
      ),
      (s, lost) => LanternTraversalSnapshot(
        confirmed: s.confirmed,
        pending: s.pending,
        query: s.query,
        sequence: s.sequence,
        observationLost: lost,
        observationMode: ObservationMode.pollingObservation,
      ),
      options,
    );
  }

  @override
  Stream<LanternSyncStatus> get syncStatus => _observations.observe(
    'sync',
    local: true,
    initial: (lost) => LanternSyncStatus(
      closed: _closed,
      pause:
          _session?.pause ??
          (_storageBlocked ? SyncPause.storage : SyncPause.none),
      reachability: _session?.reachability ?? SyncReachability.unknown,
      lifecycle: _session?.lifecycle ?? SyncLifecycle.foreground,
      deferred: _session?.runtime.networkDeferred ?? false,
      observationLost: lost,
    ),
    load: (_) async {
      _fence();
      final state = await store.transaction((tx) async {
        if (await tx.generation(partitionId) != generation ||
            await tx.authorityBinding(partitionId) !=
                binding.authorityBinding) {
          throw const LanternDataException(DataFailure.closed);
        }
        return (
          records: await tx.outbox(partitionId),
          operations: await tx.operations(partitionId),
          authPaused: await tx.replayPausedForAuth(partitionId),
        );
      });
      _fence();
      return LanternSyncStatus(
        activeMutations: state.records
            .where(
              (r) =>
                  r.state == OfflineOutboxState.enqueued ||
                  r.state == OfflineOutboxState.sending,
            )
            .length,
        terminalAttentionItems: state.operations.fold(
          0,
          (total, op) =>
              total +
              op.items
                  .where(
                    (item) =>
                        item.state == OfflineWriteState.deadLetter ||
                        item.state == OfflineWriteState.expired ||
                        item.state == OfflineWriteState.outcomeUnknown ||
                        item.state == OfflineWriteState.confirmed &&
                            item.receiptResult == null,
                  )
                  .length,
        ),
        closed: _closed,
        reachability: _session?.reachability ?? SyncReachability.unknown,
        lifecycle: _session?.lifecycle ?? SyncLifecycle.foreground,
        deferred: _session?.runtime.networkDeferred ?? false,
        pause:
            _session?.pause ??
            (_storageBlocked
                ? SyncPause.storage
                : state.authPaused
                ? SyncPause.auth
                : SyncPause.none),
      );
    },
    markLost: (s, lost) => LanternSyncStatus(
      activeMutations: s.activeMutations,
      closed: s.closed,
      pause: s.pause,
      reachability: s.reachability,
      lifecycle: s.lifecycle,
      deferred: s.deferred,
      terminalAttentionItems: s.terminalAttentionItems,
      observationLost: lost,
    ),
  );
  @override
  /// Settles this lifetime's process-local observers and releases bounded registrations.
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    await _delivery.close();
    await _completion.close();
    await _observations.close();
    await _changes.cancel();
    await _direct.close();
  }
}

final class _OfflineMutationControl implements LanternMutationControl {
  _OfflineMutationControl(this.owner);
  final OfflineLanternDataClient owner;
  @override
  Future<MutationTicket<T>> submit<T>(
    MutationRequest<T> request, {
    MutationOptions? options,
  }) async {
    owner._fence();
    if (owner._storageBlocked) {
      throw const LanternDataException(DataFailure.storageBlocked);
    }
    if (request.inputs.isEmpty) {
      final id =
          options?.operationId ?? OperationId.generate(issuedAt: owner.clock());
      return MutationTicket(
        reference: MutationReference<T>(id),
        admittedAt: owner.clock().toUtc(),
        durability: AdmissionDurability.none,
        completion: Future.value(
          MutationObservation(
            retention: MutationRetention.retained,
            reply: MutationBatchReply<T>(const []),
          ),
        ),
      );
    }
    if (options?.wait?.timeout != null &&
        options!.wait!.timeout! <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    final wait = options?.wait;
    if (wait != null) owner._completion.reserve();
    ({OperationId id, DateTime at}) admission;
    try {
      admission = await owner._admission.admit(request, options);
    } catch (_) {
      if (wait != null) owner._completion.release();
      rethrow;
    }
    if (wait != null) {
      owner._completion.admitted(admission.id, request.inputs.length);
    }
    final ref = MutationReference<T>(admission.id);
    return MutationTicket(
      reference: ref,
      admittedAt: admission.at,
      durability: owner.durability,
      completion: wait == null
          ? null
          : owner._completion.observe(ref, wait, reserved: true),
    );
  }

  @override
  Future<MutationObservation<T>> reobserve<T>(
    MutationReference<T> reference, {
    WaitOptions? options,
  }) => owner._completion.observe(reference, options);
  @override
  Future<MutationStatus<Object?>> getStatus(OperationId id) =>
      owner._completion.status(id);
  @override
  Stream<MutationStatus<Object?>> watchStatus(OperationId id) =>
      owner._completion.watch(id);
  @override
  Future<List<MutationTicket<Object?>>> submitGroup(MutationGroup group) async {
    owner._fence();
    if (owner._storageBlocked) {
      throw const LanternDataException(DataFailure.storageBlocked);
    }
    final admissions = await owner._admission.admitGroup(group.nodes);
    return [
      for (final a in admissions)
        MutationTicket(
          reference: MutationReference<Object?>(a.id),
          admittedAt: a.at,
          durability: owner.durability,
        ),
    ];
  }
}

/// Qualified automatic-owner access, kept out of the ordinary application barrel.
extension OfflineRuntimeBindings on OfflineLanternDataClient {
  /// Bounded delivery; the session runtime alone owns scheduling and retry.
  OfflineMutationDelivery get mutationDelivery => _delivery;
}

bool _deniedObservation(Object? value) {
  bool denied(ObservationCause? cause) =>
      cause == ObservationCause.permissionDenied ||
      cause == ObservationCause.unauthenticated;
  if (value is LanternSnapshot<Object?>) return denied(value.confirmed.cause);
  if (value is LanternTraversalSnapshot) return denied(value.confirmed.cause);
  if (value is List<LanternSnapshot<Object?>>) {
    return value.any((s) => denied(s.confirmed.cause));
  }
  return false;
}
