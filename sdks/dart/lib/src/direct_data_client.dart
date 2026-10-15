part of 'client.dart';

/// Direct bound profile. No durable outbox, hidden read cache or mutation replay.
final class DirectLanternDataClient implements LanternDataClient {
  /// Owns one low-level client for this immutable verified binding.
  DirectLanternDataClient(
    this._client, {
    required this.binding,
    this.rpcTimeout = const Duration(seconds: 10),
    this.batchSize = 1000,
    this.maxOperations = 128,
    this.maxMutationItems = 65536,
    this.maxMutationBytes = 32 << 20,
    this.maxWaiters = 128,
    this.maxConcurrentReads = 4,
    this.maxQueuedReads = 32,
    this.maxConcurrentMutations = 2,
    this.maxQueuedMutations = 32,
    this.operationIdPolicy = const OperationIdPolicy(),
    this.maxTraversalVertices = 4096,
    this.maxTraversalEdges = 16384,
    this.maxTraversalBytes = 1 << 20,
    this.maxTraversalQueries = 16,
    int maxWatchQueries = 64,
    int maxWatchSubscribers = 64,
    int watchMailboxSize = 8,
  }) {
    LanternAdapterReads.checkBinding(_client, binding);
    _validateBatch(0, batchSize);
    operationIdPolicy._validate();
    if (rpcTimeout <= Duration.zero ||
        maxOperations <= 0 ||
        maxMutationItems <= 0 ||
        maxMutationBytes <= 0 ||
        maxWaiters <= 0 ||
        maxConcurrentReads <= 0 ||
        maxQueuedReads < 0 ||
        maxConcurrentMutations <= 0 ||
        maxQueuedMutations < 0 ||
        maxTraversalVertices <= 0 ||
        maxTraversalEdges <= 0 ||
        maxTraversalBytes <= 0 ||
        maxTraversalQueries <= 0 ||
        maxWatchQueries <= 0 ||
        maxWatchSubscribers <= 0 ||
        watchMailboxSize <= 0) {
      throw _invalidArgumentException('session limits must be positive');
    }
    _observations = _ObservationCoordinator(
      maxQueries: maxWatchQueries,
      maxSubscribers: maxWatchSubscribers,
      mailboxSize: watchMailboxSize,
      maxTraversalQueries: maxTraversalQueries,
    );
    _mutations = _DirectMutationControl(this);
    _readPermits = _BoundedPermits(maxConcurrentReads, maxQueuedReads);
    _sendPermits = _BoundedPermits(maxConcurrentMutations, maxQueuedMutations);
    _traversalPermits = _BoundedPermits(1, maxQueuedReads);
  }
  final LanternClient _client;
  void Function()? _adapterAuthority;
  void Function(LanternException? failure)? _adapterEndpoint;
  bool _adapterRead = false;
  bool _schedulerOwnsReadPermits = false;
  @override
  final LanternSessionBinding binding;

  /// Finite per-attempt deadline; mutation waiters do not change this.
  final Duration rpcTimeout;

  /// Wire chunk limit, separate from logical admission size.
  final int batchSize;

  /// Retained operation cap; admission fails rather than dropping reuse guards.
  final int maxOperations;

  /// Complete lifecycle item reservation cap.
  final int maxMutationItems;

  /// Complete lifecycle byte reservation cap.
  final int maxMutationBytes;

  /// Bounded active completion waiter count.
  final int maxWaiters;

  /// Concurrent exact entity/traversal read attempts in this scope.
  final int maxConcurrentReads;

  /// Maximum queued read attempts; overload is a typed local failure.
  final int maxQueuedReads;

  /// Concurrent mutation attempts; work is never automatically replayed.
  final int maxConcurrentMutations;

  /// Total queued active mutation reservation, independent of retained history.
  final int maxQueuedMutations;

  /// Shared fresh-ID admission/retention policy; never Server idempotency.
  final OperationIdPolicy operationIdPolicy;

  /// Complete traversal response vertex cap; over-limit results fail untruncated.
  final int maxTraversalVertices;

  /// Complete traversal response Edge cap.
  final int maxTraversalEdges;

  /// Bounded encoded traversal payload bytes.
  final int maxTraversalBytes;

  /// Active typed traversal identity cap, separate from entity/status queries.
  final int maxTraversalQueries;
  late final _BoundedPermits _readPermits, _sendPermits, _traversalPermits;
  SyncReachability _reachability = SyncReachability.unknown;
  SyncPause _pause = SyncPause.none;
  late final _ObservationCoordinator _observations;
  late final _DirectMutationControl _mutations;
  final LanternCancellationToken _lifetime = LanternCancellationToken();
  final Set<Future<Object?>> _reads = {};
  Future<void>? _closing;
  bool _closed = false;
  int _sequence = 0;
  @override
  LanternMutationControl get mutations => _mutations;
  void _ensureOpen() {
    if (_closed || _client.isClosed) {
      throw const LanternDataException(DataFailure.closed);
    }
  }

  void _ensureAuthority() {
    if (_adapterAuthority != null) {
      _adapterAuthority!();
      return;
    }
    final expiresAt = binding.expiresAt;
    if (expiresAt != null && !expiresAt.isAfter(_mutations._now())) {
      throw LanternUnauthenticatedException._(
        _ErrorData(
          transportCode: connect.Code.unauthenticated.value,
          transportCodeName: connect.Code.unauthenticated.name,
          message: 'bound session expired',
          headers: const {},
          trailers: const {},
          metadata: const {},
        ),
      );
    }
  }

  Future<T> _read<T>(
    GetOptions? options,
    Future<T> Function(LanternCallOptions) call, {
    bool traversal = false,
  }) async {
    _ensureOpen();
    _validateReadOptions(options?.timeout);
    _throwIfCanceled(options?.cancellation);
    _ensureAuthority();
    final token = LanternCancellationToken._guarded(() {
      _adapterAuthority?.call();
      options?.cancellation?._checkCurrent();
    });
    final detachLifetime = _lifetime.listen((_) => token.cancel());
    final detachCaller = options?.cancellation?.listen((_) => token.cancel());
    final work = Future<T>(() async {
      final started = Stopwatch()..start();
      final timeout = options?.timeout ?? rpcTimeout;
      var timedOut = false;
      final timer = Timer(timeout, () {
        timedOut = true;
        token.cancel();
      });
      void Function()? release, releaseTraversal;
      try {
        if (traversal && !_schedulerOwnsReadPermits) {
          releaseTraversal = await _traversalPermits.acquire(token);
        }
        if (!_schedulerOwnsReadPermits) {
          release = await _readPermits.acquire(token);
        }
        final remaining = timeout - started.elapsed;
        if (remaining <= Duration.zero) {
          timedOut = true;
          token.cancel();
        }
        _throwIfCanceled(token);
        final result = await call(
          LanternCallOptions(
            timeout: remaining,
            cancellation: token,
            retry: false,
          ),
        );
        _throwIfCanceled(token);
        options?.cancellation?._checkCurrent();
        _adapterEndpoint?.call(null);
        _reachability = SyncReachability.idle;
        _pause = SyncPause.none;
        _observations.invalidateKey('sync');
        return result;
      } on LanternException catch (error) {
        options?.cancellation?._checkCurrent();
        _adapterEndpoint?.call(timedOut ? _boundedReadDeadline() : error);
        if (timedOut) {
          final deadline = _boundedReadDeadline();
          _recordEndpointFailure(deadline);
          throw deadline;
        }
        _recordEndpointFailure(error);
        rethrow;
      } finally {
        timer.cancel();
        release?.call();
        releaseTraversal?.call();
      }
    });
    _reads.add(work);
    try {
      final value = await work;
      _ensureOpen();
      _ensureAuthority();
      return value;
    } finally {
      _reads.remove(work);
      detachLifetime();
      detachCaller?.call();
    }
  }

  GetOptions _remainingReadOptions(GetOptions? options, Stopwatch started) {
    final remaining = (options?.timeout ?? rpcTimeout) - started.elapsed;
    if (remaining <= Duration.zero) throw _boundedReadDeadline();
    return GetOptions(
      source: options?.source ?? ReadSource.automatic,
      timeout: remaining,
      cancellation: options?.cancellation,
    );
  }

  PendingMetadata _pending(String identity) => _mutations.pending(identity);
  LanternSnapshot<T> _snapshot<T>(
    T? value,
    ConfirmedState state,
    String identity, {
    ObservationCause? cause,
    bool lost = false,
    ObservationFreshness? freshness,
    ObservationMode observationMode = ObservationMode.oneShot,
  }) {
    final expiration = switch (value) {
      Vertex(:final expiration) || Edge(:final expiration) => expiration,
      _ => null,
    };
    final now = _client._clock().toUtc();
    final expired = expiration != null && !expiration.isAfter(now);
    final present = expired ? null : value;
    return LanternSnapshot(
      confirmed: ConfirmedEvidence<T>(
        state: expired ? ConfirmedState.expired : state,
        value: present,
        origin: cause == null
            ? ObservationOrigin.server
            : ObservationOrigin.none,
        observedAt: now,
        expiration: expiration,
        freshness:
            freshness ??
            (cause == null
                ? ObservationFreshness.freshObservation
                : ObservationFreshness.unavailable),
        cause: cause,
      ),
      view: present,
      pending: _pending(identity),
      sequence: ++_sequence,
      observationLost: lost,
      observationMode: observationMode,
    );
  }

  @override
  Future<LanternSnapshot<Vertex>> getVertex(
    String key, {
    GetOptions? options,
  }) => _directGetVertex(key, options: options);
  @override
  Future<List<LanternSnapshot<Vertex>>> getVertices(
    Iterable<String> keys, {
    GetOptions? options,
  }) => _directGetVertices(keys, options: options);
  @override
  Future<LanternSnapshot<Edge>> getEdge(EdgeRef edge, {GetOptions? options}) =>
      _directGetEdge(edge, options: options);
  @override
  Future<List<LanternSnapshot<Edge>>> getEdges(
    Iterable<EdgeRef> edges, {
    GetOptions? options,
  }) => _directGetEdges(edges, options: options);
  @override
  Future<MutationReply<PutOutcome>> putVertex(
    VertexInput input, {
    MutationOptions? options,
  }) async => (await putVertices([input], options: options)).items.single;
  @override
  Future<MutationBatchReply<PutOutcome>> putVertices(
    Iterable<VertexInput> inputs, {
    MutationOptions? options,
  }) => _mutations.execute(MutationRequest.putVertices(inputs), options);
  @override
  Future<MutationReply<PutOutcome>> putVertexIfAbsent(
    VertexInput input, {
    MutationOptions? options,
  }) async =>
      (await putVerticesIfAbsent([input], options: options)).items.single;
  @override
  Future<MutationBatchReply<PutOutcome>> putVerticesIfAbsent(
    Iterable<VertexInput> inputs, {
    MutationOptions? options,
  }) =>
      _mutations.execute(MutationRequest.putVerticesIfAbsent(inputs), options);
  @override
  Future<MutationReply<PutOutcome>> putEdge(
    EdgeInput input, {
    MutationOptions? options,
  }) async => (await putEdges([input], options: options)).items.single;
  @override
  Future<MutationBatchReply<PutOutcome>> putEdges(
    Iterable<EdgeInput> inputs, {
    MutationOptions? options,
  }) => _mutations.execute(MutationRequest.putEdges(inputs), options);
  @override
  Future<MutationReply<double>> addEdge(
    EdgeInput input, {
    MutationOptions? options,
  }) async => (await addEdges([input], options: options)).items.single;
  @override
  Future<MutationBatchReply<double>> addEdges(
    Iterable<EdgeInput> inputs, {
    MutationOptions? options,
  }) => _mutations.execute(MutationRequest.addEdges(inputs), options);
  @override
  Future<MutationReply<bool>> deleteVertex(
    String input, {
    MutationOptions? options,
  }) async => (await deleteVertices([input], options: options)).items.single;
  @override
  Future<MutationBatchReply<bool>> deleteVertices(
    Iterable<String> inputs, {
    MutationOptions? options,
  }) => _mutations.execute(MutationRequest.deleteVertices(inputs), options);
  @override
  Future<MutationReply<bool>> deleteEdge(
    EdgeRef input, {
    MutationOptions? options,
  }) async => (await deleteEdges([input], options: options)).items.single;
  @override
  Future<MutationBatchReply<bool>> deleteEdges(
    Iterable<EdgeRef> inputs, {
    MutationOptions? options,
  }) => _mutations.execute(MutationRequest.deleteEdges(inputs), options);
  @override
  Future<MutationReply<bool>> deleteEdgeContribution(
    EdgeContributionRef input, {
    MutationOptions? options,
  }) async =>
      (await deleteEdgeContributions([input], options: options)).items.single;
  @override
  Future<MutationBatchReply<bool>> deleteEdgeContributions(
    Iterable<EdgeContributionRef> inputs, {
    MutationOptions? options,
  }) => _mutations.execute(
    MutationRequest.deleteEdgeContributions(inputs),
    options,
  );
  @override
  Future<LanternTraversalSnapshot> illuminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    TraversalReadOptions? options,
  }) => _directIlluminate(
    seed,
    traversal: traversal,
    weighting: weighting,
    vertexPrefix: vertexPrefix,
    options: options,
  );
  LanternTraversalSnapshot _traversalUnknown(
    ObservationCause cause, {
    bool lost = false,
    LanternTraversalQuery? query,
    ObservationMode observationMode = ObservationMode.oneShot,
  }) => LanternTraversalSnapshot(
    query: query,
    observationMode: observationMode,
    pending: _mutations.traversalPending,
    confirmed: ConfirmedEvidence<Graph>(
      state: ConfirmedState.unknown,
      origin: ObservationOrigin.none,
      observedAt: _client._clock().toUtc(),
      freshness: ObservationFreshness.unavailable,
      cause: cause,
    ),
    sequence: ++_sequence,
    observationLost: lost,
  );
  WatchOptions _watchOptions(WatchOptions? options) {
    _ensureOpen();
    final resolved = options ?? const WatchOptions();
    if (resolved.pollInterval <= Duration.zero) {
      throw _invalidArgumentException('poll interval must be positive');
    }
    if (resolved.coverage == WatchCoverage.strictLive) {
      throw const LanternDataException(DataFailure.unsupported);
    }
    return resolved;
  }

  @override
  Stream<LanternSnapshot<Vertex>> watchVertex(
    String key, {
    WatchOptions? options,
  }) => _directWatchVertex(key, options: options);
  @override
  Stream<List<LanternSnapshot<Vertex>>> watchVertices(
    Iterable<String> keys, {
    WatchOptions? options,
  }) => _directWatchVertices(keys, options: options);
  @override
  Stream<LanternSnapshot<Edge>> watchEdge(
    EdgeRef edge, {
    WatchOptions? options,
  }) => _directWatchEdge(edge, options: options);
  @override
  Stream<List<LanternSnapshot<Edge>>> watchEdges(
    Iterable<EdgeRef> edges, {
    WatchOptions? options,
  }) => _directWatchEdges(edges, options: options);
  @override
  Stream<LanternTraversalSnapshot> watchIlluminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    WatchOptions? options,
  }) => _directWatchIlluminate(
    seed,
    traversal: traversal,
    weighting: weighting,
    vertexPrefix: vertexPrefix,
    options: options,
  );
  @override
  Stream<LanternSyncStatus> get syncStatus => _observations.observe(
    'sync',
    initial: (_) => _syncSnapshot(),
    load: (_) async => _syncSnapshot(),
    markLost: (value, lost) => _syncSnapshot(lost: lost),
  );
  LanternSyncStatus _syncSnapshot({bool lost = false}) => LanternSyncStatus(
    closed: _closed,
    lifecycle: _closed ? SyncLifecycle.closed : SyncLifecycle.foreground,
    observationLost: lost,
    activeMutations: _mutations.active,
    terminalAttentionItems: _mutations.attention,
    reachability: _mutations.active > 0
        ? SyncReachability.synchronizing
        : _reachability,
    pause: _pause,
  );
  void _recordEndpointFailure(LanternException error) {
    switch (_observationCause(error)) {
      case ObservationCause.unavailable:
        _reachability = SyncReachability.offline;
      case ObservationCause.unauthenticated:
        _pause = SyncPause.auth;
      case ObservationCause.permissionDenied:
        _pause = SyncPause.policy;
      default:
        break;
    }
    _observations.invalidateKey('sync');
  }

  @override
  Future<void> close() {
    if (_closing != null) return _closing!;
    _closed = true;
    return _closing = _close();
  }

  Future<void> _close() async {
    _lifetime.cancel();
    _observations.invalidateKey('sync');
    await _observations.close();
    await _mutations.close();
    await Future.wait(
      _reads.toList().map(
        (f) => f.then<void>((_) {}, onError: (Object _, StackTrace _) {}),
      ),
    );
    if (!_adapterRead) await _client.close();
  }
}

ObservationCause? _observationCause(LanternException error) => switch (error) {
  LanternUnavailableException() ||
  LanternDeadlineExceededException() => ObservationCause.unavailable,
  LanternUnauthenticatedException() => ObservationCause.unauthenticated,
  LanternPermissionDeniedException() => ObservationCause.permissionDenied,
  _ => null,
};

LanternSnapshot<T> _markSnapshotLost<T>(LanternSnapshot<T> value, bool lost) =>
    LanternSnapshot(
      confirmed: value.confirmed,
      view: value.view,
      projection: value.projection,
      pending: value.pending,
      sequence: value.sequence,
      observationLost: lost,
      observationMode: ObservationMode.pollingObservation,
    );

void _validateReadOptions(Duration? timeout) {
  if (timeout != null && timeout <= Duration.zero) {
    throw _invalidArgumentException('read timeout must be positive');
  }
}

bool _authorityFailure(ObservationCause? cause) =>
    cause == ObservationCause.unauthenticated ||
    cause == ObservationCause.permissionDenied;

LanternDeadlineExceededException _boundedReadDeadline() =>
    LanternDeadlineExceededException._(
      _ErrorData(
        transportCode: connect.Code.deadlineExceeded.value,
        transportCodeName: connect.Code.deadlineExceeded.name,
        message: 'bounded observation deadline exceeded',
        headers: const {},
        trailers: const {},
        metadata: const {},
      ),
    );
