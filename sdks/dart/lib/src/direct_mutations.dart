part of 'client.dart';

final class _DirectOperation<T> {
  _DirectOperation(this.id, this.admittedAt, this.work)
    : _dispatched = Uint8List(work.length) {
    // Advanced admission need not attach a waiter. Keep rejected work observable
    // through status without an unhandled Future error.
    unawaited(
      completion.future.then<void>(
        (_) {},
        onError: (Object _, StackTrace _) {},
      ),
    );
  }
  final OperationId id;
  final DateTime admittedAt;
  final _FrozenMutation<T> work;
  final Completer<MutationBatchReply<T>> completion = Completer();
  final List<MutationItemStatus<T>> progress = [];
  bool terminal = false;
  final Uint8List _dispatched;
  bool _everDispatched = false;
  MutationDispatchCertainty get dispatchCertainty => _everDispatched
      ? MutationDispatchCertainty.mayHaveDispatched
      : MutationDispatchCertainty.neverDispatched;
  void markDispatched(int start, int end) {
    _everDispatched = true;
    _dispatched.fillRange(start, end, 1);
  }

  MutationBatchReply<T>? _reply;
  MutationBatchException<T>? _failure;
  final Map<Object, Completer<MutationBatchReply<T>>> _observers = {};
  void finishReply(MutationBatchReply<T> reply) {
    terminal = true;
    _reply = reply;
    completion.complete(reply);
    for (final observer in _observers.values.toList()) {
      if (!observer.isCompleted) observer.complete(reply);
    }
    _observers.clear();
  }

  void finishError(MutationBatchException<T> error) {
    terminal = true;
    _failure = error;
    completion.completeError(error);
    for (final observer in _observers.values.toList()) {
      if (!observer.isCompleted) observer.completeError(error);
    }
    _observers.clear();
  }

  Future<MutationBatchReply<T>> observe(WaitOptions? options) async {
    final key = Object(), observer = Completer<MutationBatchReply<T>>();
    Timer? timer;
    void fail(DataFailure reason) {
      _observers.remove(key);
      if (!observer.isCompleted) {
        observer.completeError(
          LanternDataException(
            reason,
            reference: id,
            dispatchCertainty: dispatchCertainty,
          ),
        );
      }
    }

    if (terminal) {
      if (_failure != null) throw _failure!;
      return _reply!;
    }
    _observers[key] = observer;
    if (options?.cancellation?.isCanceled ?? false) {
      fail(DataFailure.waitCanceled);
    }
    final detach = options?.cancellation?.listen(
      (_) => fail(DataFailure.waitCanceled),
    );
    if (options?.timeout != null) {
      timer = Timer(options!.timeout!, () => fail(DataFailure.waitTimeout));
    }
    try {
      return await observer.future;
    } finally {
      _observers.remove(key);
      timer?.cancel();
      detach?.call();
    }
  }

  MutationStatus<T> get status => MutationStatus(
    retention: MutationRetention.retained,
    operationId: id,
    items: [
      for (var i = 0; i < work.length; i++)
        i < progress.length
            ? progress[i]
            : MutationItemStatus<T>(
                index: i,
                state: MutationItemState.stillPending,
                dispatchCertainty: _dispatched[i] == 0
                    ? MutationDispatchCertainty.neverDispatched
                    : MutationDispatchCertainty.mayHaveDispatched,
              ),
    ],
    terminal: terminal,
  );
}

final class _DirectMutationControl implements LanternMutationControl {
  _DirectMutationControl(this.owner);
  final DirectLanternDataClient owner;
  final Map<OperationId, _DirectOperation<Object?>> _operations = {};
  final Set<Future<void>> _running = {};
  final Map<String, Map<MutationKind, int>> _pending = {};
  int _pendingItems = 0;
  void _adjustPending(
    _FrozenMutation<Object?> work,
    int start,
    int end,
    int delta,
  ) {
    for (var i = start; i < end; i++) {
      final identity = work.identities[i];
      final kinds = _pending[identity] ??= {};
      final count = (kinds[work.kind] ?? 0) + delta;
      if (count == 0) {
        kinds.remove(work.kind);
      } else {
        kinds[work.kind] = count;
      }
      if (kinds.isEmpty) _pending.remove(identity);
    }
    _pendingItems += (end - start) * delta;
  }

  DateTime? _highWater;
  int _bytes = 0, _items = 0, _waiters = 0;
  DateTime _now() {
    final sampled = owner._client._clock().toUtc();
    if (_highWater == null || sampled.isAfter(_highWater!)) {
      _highWater = sampled;
    }
    return _highWater!;
  }

  void _prune(DateTime now) {
    for (final entry in _operations.entries.toList()) {
      if (entry.value.terminal &&
          !owner.operationIdPolicy.retentionDeadline(entry.key).isAfter(now)) {
        _operations.remove(entry.key);
        _bytes -= entry.value.work.bytes;
        _items -= entry.value.work.length;
      }
    }
  }

  void _checkId(OperationId id, DateTime now, Set<OperationId> group) {
    if (!owner.operationIdPolicy.isFreshAt(id, now)) {
      throw const LanternDataException(DataFailure.operationIdExpired);
    }
    if (_operations.containsKey(id) || !group.add(id)) {
      throw const LanternDataException(DataFailure.operationIdCollision);
    }
  }

  void _preflight(List<MutationRequest<Object?>> requests) {
    if (active + requests.length >
            owner.maxConcurrentMutations + owner.maxQueuedMutations ||
        _operations.length + requests.length > owner.maxOperations ||
        _items + requests.fold(0, (n, r) => n + r.inputs.length) >
            owner.maxMutationItems) {
      throw const LanternDataException(DataFailure.capacity);
    }
    var remaining = owner.maxMutationBytes - _bytes;
    for (final request in requests) {
      remaining -= _minimumMutationReservation(request, remaining);
    }
  }

  void _reserve(List<_DirectOperation<Object?>> values) {
    if (active + values.length >
            owner.maxConcurrentMutations + owner.maxQueuedMutations ||
        _operations.length + values.length > owner.maxOperations ||
        _items + values.fold(0, (n, v) => n + v.work.length) >
            owner.maxMutationItems ||
        _bytes + values.fold(0, (n, v) => n + v.work.bytes) >
            owner.maxMutationBytes) {
      throw const LanternDataException(DataFailure.capacity);
    }
    for (final value in values) {
      _operations[value.id] = value;
      _items += value.work.length;
      _bytes += value.work.bytes;
      _adjustPending(value.work, 0, value.work.length, 1);
    }
    owner._observations.invalidate();
  }

  MutationTicket<T> _ticket<T>(
    _DirectOperation<T> value, {
    Future<MutationObservation<T>>? completion,
  }) => MutationTicket(
    reference: MutationReference<T>(value.id),
    admittedAt: value.admittedAt,
    durability: AdmissionDurability.memory,
    completion: completion,
  );
  @override
  Future<MutationTicket<T>> submit<T>(
    MutationRequest<T> request, {
    MutationOptions? options,
  }) async {
    owner._ensureOpen();
    owner._ensureAuthority();
    _throwIfCanceled(options?.admissionCancellation);
    _validateWait(options?.wait);
    final wantsCompletion = options?.wait != null;
    if (wantsCompletion && _waiters >= owner.maxWaiters) {
      throw const LanternDataException(DataFailure.capacity);
    }
    if (wantsCompletion) _waiters++;
    try {
      final now = _now();
      _prune(now);
      final id = options?.operationId ?? OperationId.generate(issuedAt: now);
      _checkId(id, now, {});
      _preflight([request]);
      final work = _freezeMutation(
        request,
        now,
        owner._client._contribIds,
        maxReservedBytes: owner.maxMutationBytes - _bytes,
      );
      _throwIfCanceled(options?.admissionCancellation);
      final operation = _DirectOperation<T>(id, now, work);
      _reserve([operation]);
      _start(operation);
      final completion = wantsCompletion
          ? _submittedCompletion(operation, options?.wait)
          : null;
      return _ticket(operation, completion: completion);
    } catch (_) {
      if (wantsCompletion) _waiters--;
      rethrow;
    }
  }

  Future<MutationObservation<T>> _submittedCompletion<T>(
    _DirectOperation<T> operation,
    WaitOptions? options,
  ) {
    final future = Future<MutationObservation<T>>.sync(() async {
      try {
        return MutationObservation(
          retention: MutationRetention.retained,
          reply: await operation.observe(options),
        );
      } finally {
        _waiters--;
      }
    });
    unawaited(future.then<void>((_) {}, onError: (Object _, StackTrace _) {}));
    return future;
  }

  Future<MutationBatchReply<T>> execute<T>(
    MutationRequest<T> request,
    MutationOptions? options,
  ) async {
    owner._ensureOpen();
    if ((request as _MutationRequest<T>).inputs.isEmpty) {
      return MutationBatchReply<T>(const []);
    }
    _validateWait(options?.wait);
    if (_waiters >= owner.maxWaiters) {
      throw const LanternDataException(DataFailure.capacity);
    }
    // Reserve waiter capacity before admission; no await occurs before it.
    _waiters++;
    try {
      final ticket = await submit(
        request,
        options: MutationOptions(
          admissionCancellation: options?.admissionCancellation,
          operationId: options?.operationId,
        ),
      );
      return await (_operations[ticket.reference.operationId]
              as _DirectOperation<T>)
          .observe(options?.wait);
    } finally {
      _waiters--;
    }
  }

  void _start<T>(
    _DirectOperation<T> operation, {
    Future<void> Function()? prerequisites,
  }) {
    late final Future<void> future;
    future = Future<void>(() async {
      try {
        await prerequisites?.call();
        await _deliver(operation);
      } on Exception catch (cause) {
        _failUnsent(operation, cause);
      }
    }).whenComplete(() => _running.remove(future));
    _running.add(future);
  }

  void _ensureDispatchEligible<T>(
    _DirectOperation<T> operation,
    int start,
    int end,
  ) {
    owner._ensureOpen();
    owner._ensureAuthority();
    final now = _now();
    if (operation.work.expirations
        .skip(start)
        .take(end - start)
        .any((e) => e != null && !e.isAfter(now))) {
      throw LanternDataException(
        DataFailure.expiredBeforeDispatch,
        reference: operation.id,
        dispatchCertainty: operation.dispatchCertainty,
      );
    }
  }

  Future<void> _deliver<T>(_DirectOperation<T> operation) async {
    final work = operation.work;
    final replies = <MutationReply<T>>[];
    for (var start = 0; start < work.length; start += owner.batchSize) {
      final end = _chunkEnd(start, owner.batchSize, work.length);
      var dispatched = false;
      try {
        _ensureDispatchEligible(operation, start, end);
        final release = await owner._sendPermits.acquire(owner._lifetime);
        late final List<MutationReply<T>> result;
        try {
          _ensureDispatchEligible(operation, start, end);
          result = await owner._client._attemptIndexed<T>(
            work,
            start,
            end,
            LanternCallOptions(
              timeout: owner.rpcTimeout,
              retry: false,
              cancellation: owner._lifetime,
            ),
            () {
              // The invoker has acquired the token. Fence the final synchronous
              // boundary before recording dispatch or entering the raw RPC.
              _ensureDispatchEligible(operation, start, end);
              dispatched = true;
              operation.markDispatched(start, end);
              owner._observations.invalidateKey(
                'status:${operation.id.encoded}',
              );
            },
          );
          // Authority eligibility is separate from entity TTL. A late authority
          // failure fences this sent chunk without rewriting Server Put effects.
          owner._ensureOpen();
          owner._ensureAuthority();
        } finally {
          release();
        }
        _adjustPending(work, start, end, -1);
        replies.addAll(result);
        for (var i = start; i < end; i++) {
          operation.progress.add(
            MutationItemStatus(
              index: i,
              state: MutationItemState.handled,
              reply: result[i - start],
            ),
          );
        }
        owner._observations.invalidate();
      } on Exception catch (cause) {
        if (cause is LanternException) owner._recordEndpointFailure(cause);
        final boundedCause = _boundedMutationFailure(cause);
        _adjustPending(work, start, work.length, -1);
        for (var i = start; i < work.length; i++) {
          operation.progress.add(
            MutationItemStatus<T>(
              index: i,
              state: i < end
                  ? (dispatched
                        ? MutationItemState.unknown
                        : MutationItemState.failed)
                  : MutationItemState.notAttempted,
              failure: i < end ? boundedCause : null,
              dispatchCertainty: i < end && dispatched
                  ? MutationDispatchCertainty.mayHaveDispatched
                  : MutationDispatchCertainty.neverDispatched,
            ),
          );
        }
        operation.finishError(
          MutationBatchException<T>(
            items: operation.progress,
            cause: boundedCause,
            reference: operation.id,
          ),
        );
        owner._observations.invalidate();
        return;
      }
    }
    operation.finishReply(MutationBatchReply(replies));
    owner._observations.invalidate();
  }

  void _failUnsent<T>(_DirectOperation<T> operation, Exception cause) {
    if (operation.terminal) return;
    _adjustPending(
      operation.work,
      operation.progress.length,
      operation.work.length,
      -1,
    );
    for (var i = operation.progress.length; i < operation.work.length; i++) {
      operation.progress.add(
        MutationItemStatus<T>(
          index: i,
          state: MutationItemState.notAttempted,
          failure: _boundedMutationFailure(cause),
          dispatchCertainty: MutationDispatchCertainty.neverDispatched,
        ),
      );
    }
    operation.finishError(
      MutationBatchException<T>(
        items: operation.progress,
        cause: _boundedMutationFailure(cause),
        reference: operation.id,
      ),
    );
    owner._observations.invalidate();
  }

  PendingMetadata pending(String identity) {
    final kinds = _pending[identity] ?? const <MutationKind, int>{};
    final count = kinds.values.fold(0, (n, v) => n + v);
    final mask = kinds.keys.fold(0, (n, kind) => n | (1 << kind.index));
    return PendingMetadata(
      count: count,
      unresolvedCount: count,
      wholeSequenceExact: count == 0,
      unresolvedKindMask: mask,
    );
  }

  PendingMetadata get traversalPending => PendingMetadata(
    count: _pendingItems,
    unresolvedCount: _pendingItems,
    wholeSequenceExact: _pendingItems == 0,
  );
  int get attention => _operations.values.fold(
    0,
    (total, op) =>
        total +
        (op.terminal
            ? op.progress
                  .where((i) => i.state != MutationItemState.handled)
                  .length
            : 0),
  );
  int get active => _operations.values.where((op) => !op.terminal).length;
  MutationRetention _absent(OperationId id) =>
      owner.operationIdPolicy.retentionDeadline(id).isAfter(_now())
      ? MutationRetention.unknown
      : MutationRetention.evicted;
  @override
  Future<MutationStatus<Object?>> getStatus(OperationId operationId) async {
    owner._ensureOpen();
    _prune(_now());
    return _operations[operationId]?.status ??
        MutationStatus(
          retention: _absent(operationId),
          operationId: operationId,
        );
  }

  @override
  Future<MutationObservation<T>> reobserve<T>(
    MutationReference<T> reference, {
    WaitOptions? options,
  }) async {
    owner._ensureOpen();
    _validateWait(options);
    _prune(_now());
    final operation = _operations[reference.operationId];
    if (operation == null) {
      return MutationObservation(retention: _absent(reference.operationId));
    }
    if (operation is! _DirectOperation<T>) {
      throw const LanternDataException(DataFailure.typeMismatch);
    }
    if (_waiters >= owner.maxWaiters) {
      throw const LanternDataException(DataFailure.capacity);
    }
    _waiters++;
    try {
      return MutationObservation(
        retention: MutationRetention.retained,
        reply: await operation.observe(options),
      );
    } finally {
      _waiters--;
    }
  }

  @override
  Stream<MutationStatus<Object?>> watchStatus(OperationId operationId) =>
      owner._observations.observe(
        'status:${operationId.encoded}',
        initial: (_) =>
            _operations[operationId]?.status ??
            MutationStatus<Object?>(
              retention: _absent(operationId),
              operationId: operationId,
            ),
        load: (_) => getStatus(operationId),
        markLost: (value, lost) => MutationStatus<Object?>(
          retention: value.retention,
          operationId: value.operationId,
          items: value.items,
          terminal: value.terminal,
          observationLost: lost,
        ),
      );
  @override
  Future<List<MutationTicket<Object?>>> submitGroup(MutationGroup group) async {
    owner._ensureOpen();
    if (group.nodes.isEmpty) return const [];
    owner._ensureAuthority();
    if (group.nodes.length > 64 ||
        group.nodes.fold(0, (n, v) => n + v.dependencies.length) > 256) {
      throw _invalidArgumentException('group exceeds node/arc bounds');
    }
    final now = _now();
    _prune(now);
    _preflight(group.nodes.map((n) => n.request).toList());
    var remainingBytes = owner.maxMutationBytes - _bytes;
    final ids = <OperationId>{};
    final values = <_DirectOperation<Object?>>[];
    for (final node in group.nodes) {
      final id = OperationId.generate(issuedAt: now);
      _checkId(id, now, ids);
      values.add(switch (node.request.kind) {
        MutationKind.putVertices ||
        MutationKind.putVerticesIfAbsent ||
        MutationKind.putEdges => _DirectOperation<PutOutcome>(
          id,
          now,
          _freezeMutation(
            node.request as MutationRequest<PutOutcome>,
            now,
            owner._client._contribIds,
            maxReservedBytes: remainingBytes,
          ),
        ),
        MutationKind.addEdges => _DirectOperation<double>(
          id,
          now,
          _freezeMutation(
            node.request as MutationRequest<double>,
            now,
            owner._client._contribIds,
            maxReservedBytes: remainingBytes,
          ),
        ),
        _ => _DirectOperation<bool>(
          id,
          now,
          _freezeMutation(
            node.request as MutationRequest<bool>,
            now,
            owner._client._contribIds,
            maxReservedBytes: remainingBytes,
          ),
        ),
      });
      remainingBytes -= values.last.work.bytes;
    }
    final prerequisites = List.generate(values.length, (_) => <int>{});
    final previous = <String, int>{};
    for (var i = 0; i < values.length; i++) {
      for (final identity in values[i].work.identities.toSet()) {
        final predecessor = previous[identity];
        if (predecessor != null) prerequisites[i].add(predecessor);
        previous[identity] = i;
      }
      for (final dependency in group.nodes[i].dependencies) {
        if (dependency.node < 0 ||
            dependency.node >= values.length ||
            dependency.item < 0 ||
            dependency.item >= values[dependency.node].work.length) {
          throw _invalidArgumentException('invalid group item dependency');
        }
        prerequisites[i].add(dependency.node);
      }
    }
    final ordered = <int>[];
    final remaining = List.generate(values.length, (i) => i).toSet();
    while (remaining.isNotEmpty) {
      final ready = remaining
          .where((i) => prerequisites[i].every(ordered.contains))
          .toList();
      if (ready.isEmpty) {
        throw _invalidArgumentException(
          'cyclic group dependencies including identity FIFO',
        );
      }
      ordered.addAll(ready);
      remaining.removeAll(ready);
    }
    _reserve(values);
    for (final i in ordered) {
      _startGroup(values[i], () async {
        // FIFO predecessors must settle; only explicit dependencies require success.
        for (final prerequisite in prerequisites[i]) {
          try {
            await values[prerequisite].completion.future;
          } on Exception {
            /* inspect explicit predicate below */
          }
        }
        for (final dependency in group.nodes[i].dependencies) {
          final item = values[dependency.node].progress[dependency.item];
          final reply = item.reply;
          final satisfied =
              item.state == MutationItemState.handled &&
              switch (dependency.predicate) {
                MutationDependencyPredicate.handled => true,
                MutationDependencyPredicate.knownEffect =>
                  reply is MutationKnownEffect<Object?>,
                MutationDependencyPredicate.putAppliedAndLive =>
                  reply is MutationKnownEffect<Object?> &&
                      reply.effect == PutOutcome.appliedAndLive,
                MutationDependencyPredicate.deleteExisted =>
                  reply is MutationKnownEffect<Object?> && reply.effect == true,
              };
          if (!satisfied) {
            throw const LanternDataException(DataFailure.dependencyFailed);
          }
        }
      });
    }
    return List.unmodifiable(values.map((value) => _ticket(value)));
  }

  void _startGroup(
    _DirectOperation<Object?> operation,
    Future<void> Function() prerequisites,
  ) {
    switch (operation) {
      case _DirectOperation<PutOutcome> value:
        _start<PutOutcome>(value, prerequisites: prerequisites);
      case _DirectOperation<double> value:
        _start<double>(value, prerequisites: prerequisites);
      case _DirectOperation<bool> value:
        _start<bool>(value, prerequisites: prerequisites);
      default:
        throw const LanternDataException(DataFailure.typeMismatch);
    }
  }

  Future<void> close() async => Future.wait(_running.toList());
}

void _validateWait(WaitOptions? options) {
  if (options?.timeout != null && options!.timeout! <= Duration.zero) {
    throw _invalidArgumentException('wait timeout must be positive');
  }
}
