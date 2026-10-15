part of 'bound_data_client.dart';

/// One stream and one independently permitted resident job in the owned runtime.
final class _AutomaticIncoming {
  _AutomaticIncoming(this.owner, this.source);
  final _SessionShared owner;
  final LanternChangeSource source;
  LanternChangeSession? _tail;
  bool _running = false,
      _disabled = false,
      _bootstrap = true,
      _boundary = false,
      discontinuous = false;
  int revision = 0;
  String? _residentAfter;
  int _residentPages = 0;
  DateTime? _residentNotBefore;
  OfflineLanternDataClient get data => owner.data;
  bool get allowReads => !discontinuous && (_disabled || _boundary);

  void register() {
    owner.runtime.retain('incoming', OfflineSessionLane.incoming, _run);
    owner.runtime.retain(
      'incoming-residents',
      OfflineSessionLane.read,
      _recover,
    );
  }

  void wake({DateTime? dueAt}) {
    if (_disabled || _running || !owner._policyAllowsNetwork) return;
    owner.runtime.enqueue(
      'incoming',
      OfflineSessionLane.incoming,
      _run,
      dueAt: dueAt,
      retain: true,
    );
  }

  void requireBootstrap() {
    _bootstrap = true;
    _boundary = false;
    discontinuous = true;
    revision++;
    _resetRecovery();
  }

  void _resetRecovery() {
    _residentAfter = null;
    _residentPages = 0;
    _residentNotBefore = null;
  }

  DateTime? _recoveryDue(DateTime? requested) {
    var due = requested;
    final now = data.clock();
    for (final floor in [_residentNotBefore, owner._backoffUntil]) {
      if (floor != null &&
          floor.isAfter(now) &&
          (due == null || floor.isAfter(due))) {
        due = floor;
      }
    }
    return due;
  }

  Future<void> verifyContinuity() async {
    try {
      source.checkCurrent();
    } on LanternChangeDiscontinuity {
      await discontinue();
    }
  }

  Future<void> discontinue() async {
    if (discontinuous) return;
    requireBootstrap();
    try {
      await owner.store.transaction((tx) async {
        owner._checkLifetime();
        await tx.resetScopedChangeCursor(data.partitionId, null);
        owner._checkLifetime();
      });
    } catch (_) {
      owner._requestStoragePause();
      rethrow;
    }
    final tail = _tail;
    _tail = null;
    await tail?.close();
    wake();
  }

  void _queueRecovery({DateTime? dueAt}) {
    if (!_boundary || _tail == null || !owner._policyAllowsNetwork) return;
    owner.runtime.enqueue(
      'incoming-residents',
      OfflineSessionLane.read,
      _recover,
      dueAt: _recoveryDue(dueAt),
      retain: true,
    );
  }

  Future<void> _run(OfflineSessionWork work) async {
    _running = true;
    LanternChangeSession? session;
    StreamIterator<ChangeFrame>? frames;
    void Function()? detach;
    DateTime? retry;
    try {
      owner._checkNetwork();
      work.checkCurrent();
      source.checkCurrent();
      // Persisted bytes alone do not certify the actual route of a new owner.
      // Only this lifetime's established boundary may authorize resume.
      final cursor = _bootstrap
          ? null
          : await owner.store.transaction(
              (tx) => tx.scopedChangeCursor(data.partitionId),
            );
      work.checkCurrent();
      final bootstrap = _bootstrap || cursor == null;
      if (bootstrap) _resetRecovery();
      session = source.open(
        bootstrap: bootstrap,
        cursor: cursor == null || bootstrap
            ? null
            : ChangeCursor(cursor.toBytes()),
        cancellation: work.cancellation,
        onEndpoint: owner._readEndpoint,
      );
      _tail = session;
      final active = frames = StreamIterator(session.frames);
      detach = work.cancellation.listen((_) => active.cancel().ignore());
      var first = true;
      while (await active.moveNext()) {
        work.checkCurrent();
        source.checkCurrent();
        final frame = active.current;
        if (frame.bootstrap != (first && bootstrap) ||
            first && bootstrap && frame.cursor == null) {
          throw const LanternChangeDiscontinuity();
        }
        await owner.store.transaction((tx) async {
          work.checkCurrent();
          source.checkCurrent();
          if (await tx.generation(data.partitionId) != data.generation ||
              await tx.authorityBinding(data.partitionId) !=
                  data.binding.authorityBinding) {
            throw const OfflineDisposedException();
          }
          if (frame.bootstrap) {
            await tx.resetScopedChangeCursor(
              data.partitionId,
              OfflineScopedChangeCursor(frame.cursor!.toBytes()),
            );
          } else {
            await tx.applyScopedChangeFrame(
              data.partitionId,
              OfflineScopedChangeFrame(
                keys: frame.invalidations.map(
                  (item) => switch (item) {
                    VertexInvalidation(:final key) => OfflineEntityKey.vertex(
                      key,
                    ),
                    EdgeInvalidation(:final edge) => OfflineEntityKey.edge(
                      edge.tail,
                      edge.head,
                    ),
                  },
                ),
                cursor: frame.cursor == null
                    ? null
                    : OfflineScopedChangeCursor(frame.cursor!.toBytes()),
              ),
            );
          }
          work.checkCurrent();
          source.checkCurrent();
        });
        work.checkCurrent();
        source.checkCurrent();
        final invalidate =
            first || frame.bootstrap || frame.invalidations.isNotEmpty;
        first = false;
        _bootstrap = false;
        discontinuous = false;
        _boundary = true;
        owner._endpointSuccess();
        _queueRecovery();
        // Resume readiness needs one wake even without identities. Later opaque
        // progress must not discard an otherwise valid in-flight observation.
        if (invalidate) data._observations.invalidate();
      }
      work.checkCurrent();
      throw const LanternChangeDiscontinuity();
    } catch (error, stack) {
      _boundary = false;
      revision++;
      if (work.cancellation.isCanceled ||
          error is OfflineWorkInterruptedException ||
          session != null && !identical(_tail, session)) {
        return;
      }
      work.checkCurrent();
      if (error is LanternUnauthenticatedException ||
          error is LanternPermissionDeniedException) {
        await owner._pauseFromRead(
          error is LanternUnauthenticatedException
              ? ObservationCause.unauthenticated
              : ObservationCause.permissionDenied,
        );
      } else if (error is LanternException && error.transportCode == 12) {
        _disabled = true;
        await owner.store.transaction((tx) async {
          work.checkCurrent();
          await tx.advanceReadEpoch(data.partitionId);
          work.checkCurrent();
        });
        data._observations.invalidate();
      } else if (error is LanternUnavailableException ||
          error is LanternDeadlineExceededException ||
          error is LanternResourceExhaustedException) {
        await owner.store.transaction((tx) async {
          work.checkCurrent();
          await tx.advanceReadEpoch(data.partitionId);
          work.checkCurrent();
        });
        retry = owner._retryDeadline(error);
      } else if (error is LanternChangeDiscontinuity ||
          error is LanternFailedPreconditionException ||
          error is LanternInternalException) {
        requireBootstrap();
        await owner.store.transaction((tx) async {
          work.checkCurrent();
          await tx.resetScopedChangeCursor(data.partitionId, null);
          work.checkCurrent();
        });
        retry = owner._retryDeadline(error);
      } else {
        // Disk/corruption failures never masquerade as an unsupported source.
        owner._requestStoragePause();
        Error.throwWithStackTrace(error, stack);
      }
    } finally {
      detach?.call();
      try {
        await frames?.cancel();
      } finally {
        await session?.close();
        if (identical(_tail, session)) _tail = null;
        _running = false;
        if (!owner.ended && (retry != null || discontinuous)) {
          wake(dueAt: retry);
        }
      }
    }
  }

  Future<void> _recover(OfflineSessionWork work) async {
    final tail = _tail, startedRevision = revision;
    if (tail == null || !_boundary) return;
    void check() {
      work.checkCurrent();
      source.checkCurrent();
      if (!identical(tail, _tail) ||
          !_boundary ||
          revision != startedRevision) {
        throw const OfflineWorkInterruptedException();
      }
    }

    try {
      check();
      // A stop can interrupt the 32nd selected page before its epilogue. Its
      // replacement still owes the window floor before selecting page 33.
      if (_residentPages == 32) {
        _residentPages = 0;
        _residentNotBefore = data.clock().add(const Duration(seconds: 1));
      }
      final due = _recoveryDue(null);
      if (due != null && due.isAfter(data.clock())) {
        // enqueue preserves an earlier pending deadline. One already queued
        // turn may enter early, but only re-arms this job without scanning/RPC.
        _queueRecovery(dueAt: due);
        return;
      }
      // An elapsed floor must not turn later CDC wakes into immediate work;
      // ordinary forward pages still use the runtime debounce.
      _residentNotBefore = null;
      final keys = await owner.store.transaction(
        (tx) => tx.unknownResidents(
          data.partitionId,
          limit: 32,
          after: _residentAfter,
        ),
      );
      check();
      if (keys.isNotEmpty) {
        // Selection progress is independent of a successful epoch-valid commit.
        // A stale page stays Unknown, but must not monopolize the next turn.
        _residentAfter = keys.last.canonical;
        _residentPages++;
      }
      final vertices = keys
          .where((key) => key.kind == OfflineEntityKind.vertex)
          .toList();
      final edges = keys
          .where((key) => key.kind == OfflineEntityKind.edge)
          .toList();
      if (vertices.isNotEmpty) {
        await data._readMany<Vertex>(
          vertices,
          GetOptions(
            source: ReadSource.serverOnly,
            cancellation: work.cancellation,
          ),
          (keys, token) async => _BoundReadBatch(
            await tail.getVertices([
              for (final key in keys) key.vertexKey!,
            ], cancellation: token),
          ),
          recovering: true,
        );
      }
      check();
      if (edges.isNotEmpty) {
        await data._readMany<Edge>(
          edges,
          GetOptions(
            source: ReadSource.serverOnly,
            cancellation: work.cancellation,
          ),
          (keys, token) => data._fetchEdges(
            keys,
            token,
            getEdges: (keys, token) => tail.getEdges(keys, cancellation: token),
            getVertices: (keys, token) =>
                tail.getVertices(keys, cancellation: token),
          ),
          recovering: true,
        );
      }
      check();
      final later = keys.isEmpty
          ? <OfflineEntityKey>[]
          : await owner.store.transaction(
              (tx) => tx.unknownResidents(
                data.partitionId,
                limit: 1,
                after: _residentAfter,
              ),
            );
      check();
      if (later.isEmpty) {
        final head = await owner.store.transaction(
          (tx) => tx.unknownResidents(data.partitionId, limit: 1),
        );
        check();
        if (head.isEmpty) {
          _resetRecovery();
          return;
        }
        _residentAfter = null;
      }
      if (later.isEmpty || _residentPages == 32) {
        _residentPages = 0;
        _residentNotBefore = data.clock().add(const Duration(seconds: 1));
      }
      _queueRecovery(dueAt: data.clock().add(owner.runtime.debounce));
    } catch (error, stack) {
      // Closing a replaced tail may surface a typed closed-read error rather
      // than work cancellation. It cannot pause/reset the replacement runtime.
      if (work.cancellation.isCanceled ||
          !identical(tail, _tail) ||
          !_boundary ||
          revision != startedRevision ||
          error is OfflineWorkInterruptedException) {
        return;
      }
      if (error is LanternChangeDiscontinuity) {
        await discontinue();
      } else {
        Error.throwWithStackTrace(error, stack);
      }
    }
  }
}
