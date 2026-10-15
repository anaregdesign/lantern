import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';

import 'automatic_owner.dart';
import 'bound_mutation_remote.dart';
import 'bound_writes.dart';
import 'codec.dart';
import 'errors.dart';
import 'mutation_admission.dart';
import 'mutation_delivery.dart';
import 'mutation_state.dart';
import 'operation_completion.dart';
import 'pending_projection.dart';
import 'remote.dart';
import 'scoped_change_store.dart';
import 'session_runtime.dart';
import 'store.dart';
import 'traversal_clock.dart';
import 'traversal_result.dart';
import 'types.dart';

part 'bound_reads.dart';
part 'bound_traversal.dart';
part 'incoming_changes.dart';
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
    this.readRoute,
    this._traversalClock,
  ) {
    _observations = LanternObservationCoordinator(
      scheduler: _session?.runtime.observationScheduler,
    );
    _direct = LanternAdapterReads.bound(
      _client,
      binding: binding,
      checkAuthority: _checkReadAuthority,
      onEndpoint: _session?._readEndpoint,
      schedulerOwnsPermits: _session != null,
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
      if (c.observationInvalidated) {
        _readRevision++;
        _observations.invalidate();
      }
    });
    _traversalClock.onChanged = _traversalClockChanged;
  }

  /// Opens validated storage before any remote work. Scope derives from verified identity.
  static Future<OfflineLanternDataClient> open({
    required LanternClient client,
    required OfflineStore store,
    required LanternSessionBinding binding,
    required AdmissionDurability durability,
    bool cacheReads = true,
    LanternSingleResponderRoute? readRoute,
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
    readRoute: readRoute,
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
    LanternSingleResponderRoute? readRoute,
    OfflineClock? clock,
    bool Function()? authorityCurrent,
    OfflineReceiptRemote? receiptRemote,
    OfflineBoundMutationRemote? mutationRemote,
    _SessionShared? session,
    bool bootstrapRequired = false,
  }) async {
    LanternAdapterReads.checkBinding(client, binding);
    if (durability == AdmissionDurability.none) {
      throw const OfflineArgumentException();
    }
    if (readRoute != null && !identical(readRoute.client, client)) {
      throw const OfflineArgumentException();
    }
    final now = clock ?? () => DateTime.now().toUtc();
    final timing = OfflineTraversalClock(now);
    final scope = binding.stableIdentity;
    final opened = await store.transaction((tx) async {
      final raw = timing.sample();
      final at = await tx.effectiveTime(scope, raw);
      timing.observe(raw, at);
      await tx.bindAuthority(scope, binding.authorityBinding);
      if (timing.invalidationPending || bootstrapRequired) {
        await tx.invalidateTraversals(scope);
      }
      return (
        generation: await tx.generation(scope),
        revision: timing.rollbackRevision,
      );
    });
    timing.committed(opened.revision);
    DateTime effectiveClock() {
      timing.sample();
      return timing.highWater;
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
      readRoute,
      timing,
    );
  }

  final LanternClient _client;
  final _SessionShared? _session;

  /// Optional certified actual responder shared by reads and incoming changes.
  final LanternSingleResponderRoute? readRoute;
  final OfflineTraversalClock _traversalClock;
  final String _traversalOwner = List.generate(
    16,
    (_) => Random.secure().nextInt(256).toRadixString(16).padLeft(2, '0'),
  ).join();
  int _readRevision = 0;
  bool _routeDiscontinuous = false;
  bool _routeInvalidated = false;
  ObservationCause? _standaloneReadCause;
  bool _clockQueued = false;
  Future<void>? _clockFlush, _routeInvalidation;

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
  Future<void>? _closing;
  int _sequence = 0;
  // Weak metadata travels with an immutable snapshot until the coordinator
  // accepts it. Rejected captures cannot update the active query's deadline.
  final _localExpiries = Expando<DateTime>();
  @override
  LanternMutationControl get mutations => _mutations;
  void _fence() {
    _session?._checkLifetime();
    if (_closed || _invalid || _client.isClosed) {
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
    await _session?.incoming?.verifyContinuity();
    final result = await store.transaction((tx) async {
      if (await tx.generation(partitionId) != generation ||
          await tx.authorityBinding(partitionId) != binding.authorityBinding) {
        throw const LanternDataException(DataFailure.closed);
      }
      final now = await tx.effectiveTime(partitionId, clock().toUtc());
      var record = cacheReads ? await tx.getCache(partitionId, key) : null;
      DateTime? dependencyExpiry;
      if (record?.edge != null) {
        for (final endpoint in [key.tail!, key.head!]) {
          final dependency = await tx.getCache(
            partitionId,
            OfflineEntityKey.vertex(endpoint),
          );
          final confirmed = offlineConfirmed<Vertex>(dependency, now);
          if (confirmed.state != ConfirmedState.present) {
            record = null;
            break;
          }
          final expiration = confirmed.expiration;
          if (expiration != null &&
              (dependencyExpiry == null ||
                  expiration.isBefore(dependencyExpiry))) {
            dependencyExpiry = expiration;
          }
        }
      }
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
            ? _capEvidence(
                offlineConfirmed<T>(record, now),
                dependencyExpiry,
                now,
              )
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
      consider(dependencyExpiry);
      consider(evidence?.expiration);
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

  @override
  Future<LanternSnapshot<Vertex>> getVertex(
    String key, {
    GetOptions? options,
  }) async => (await getVertices([key], options: options)).single;
  @override
  Future<List<LanternSnapshot<Vertex>>> getVertices(
    Iterable<String> keys, {
    GetOptions? options,
  }) => _readVertices(keys, options: options);

  @override
  Future<LanternSnapshot<Edge>> getEdge(
    EdgeRef edge, {
    GetOptions? options,
  }) async => (await getEdges([edge], options: options)).single;
  @override
  Future<List<LanternSnapshot<Edge>>> getEdges(
    Iterable<EdgeRef> edges, {
    GetOptions? options,
  }) => _readEdges(edges, options: options);

  @override
  Future<LanternTraversalSnapshot> illuminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    TraversalReadOptions? options,
  }) => _readTraversal(
    seed,
    traversal: traversal,
    weighting: weighting,
    vertexPrefix: vertexPrefix,
    options: options,
  );

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
  }) => watchVertices([key], options: options).map((values) => values.single);
  @override
  Stream<LanternSnapshot<Edge>> watchEdge(
    EdgeRef edge, {
    WatchOptions? options,
  }) => watchEdges([edge], options: options).map((values) => values.single);
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
      captureLocal: () => _localTraversal(
        query,
        serverOnly: options?.source == ReadSource.serverOnly,
      ),
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
  /// Joins this lifetime and persists detected traversal invalidation before success.
  /// A failed close stays failed; caller-owned storage must not be treated as
  /// safely reopened merely because the public lifetime is already closed.
  Future<void> close() {
    if (_closing != null) return _closing!;
    _closed = true;
    _traversalClock.onChanged = null;
    return _closing = _close().catchError((Object error, StackTrace stack) {
      _storageBlocked = true;
      if (error is OfflineException || error is LanternDataException) {
        Error.throwWithStackTrace(error, stack);
      }
      throw const LanternDataException(DataFailure.storageBlocked);
    });
  }

  Future<void> _close() async {
    await _delivery.close();
    await _completion.close();
    await _observations.close();
    await _changes.cancel();
    await _direct.close();
    for (final pending in [_clockFlush, _routeInvalidation]) {
      try {
        await pending;
      } on OfflineDisposedException {
        // Ordinary lifetime cancellation cannot acknowledge invalidation.
      } on OfflineWorkInterruptedException {
        // The closing-only transaction below settles the remaining obligation.
      } on LanternDataException catch (error) {
        if (error.reason != DataFailure.closed) rethrow;
      }
    }
    // Join accepted local transactions. Their post-commit continuations check
    // the closed lifetime before sampling raw time or admitting further work.
    await store.transaction((_) async {});
    await _settleTraversalInvalidationOnClose();
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
