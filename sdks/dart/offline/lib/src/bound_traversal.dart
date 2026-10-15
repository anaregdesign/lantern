part of 'bound_data_client.dart';

extension _BoundTraversal on OfflineLanternDataClient {
  void _checkReadAuthority() {
    _fence();
    _session?._checkNetwork();
    _session?.incoming?.source.checkCurrent();
    if (_standaloneReadCause != null || _storageBlocked) {
      throw const OfflineWorkInterruptedException();
    }
    if (_routeDiscontinuous || readRoute != null && !readRoute!.isCurrent()) {
      _routeDiscontinuous = true;
      throw const LanternChangeDiscontinuity();
    }
    _traversalClock.sample();
  }

  void _traversalClockChanged() {
    if (_closed || _session?.ended == true || _clockQueued) return;
    _clockQueued = true;
    // The raw clock is also sampled by the runtime's central alarm before it
    // can rearm a clamped deadline. Defer all I/O outside that clock callback.
    scheduleMicrotask(() {
      _clockQueued = false;
      if (_closed || _session?.ended == true) return;
      _observations.invalidate();
      final session = _session;
      if (session != null) {
        session.runtime.enqueue(
          'traversal-clock',
          OfflineSessionLane.local,
          (_) => _flushTraversalClock(),
        );
      } else {
        unawaited(
          _flushTraversalClock().catchError((Object _) {
            // The hold remains active; subsequent reads surface storageBlocked.
          }),
        );
      }
    });
  }

  Future<void> _invalidateTraversalRoute() =>
      _routeInvalidation ??= _traversalTransaction<void>((tx) async {
        await tx.invalidateTraversals(partitionId);
      });

  Future<void> _flushTraversalClock() => _clockFlush ??=
      _traversalTransaction<void>((_) async {}).whenComplete(() {
        // Final owner close joins the runtime before the facade. Retain an
        // in-flight failure through that join instead of masking it with a
        // later closing-only transaction which happens to succeed.
        if (!_closed && _session?.ended != true) _clockFlush = null;
      });

  // Subtracts only derived eligibility under retained local control. This does
  // not reopen the ordinary read fence or require network/route credentials.
  Future<void> _settleTraversalClockOnClose() async {
    if (!_traversalClock.invalidationPending) return;
    final revision = _traversalClock.rollbackRevision;
    final highWater = _traversalClock.highWater;
    void control() {
      _session?._checkControlLifetime();
      if (_invalid || _client.isClosed || !authorityCurrent()) {
        throw const LanternDataException(DataFailure.closed);
      }
      if (_session == null &&
          binding.expiresAt != null &&
          !highWater.isBefore(binding.expiresAt!)) {
        throw const LanternDataException(DataFailure.closed);
      }
    }

    try {
      control();
      await store.transaction((tx) async {
        Future<void> scope() async {
          control();
          final currentGeneration = await tx.generation(partitionId);
          control();
          final currentAuthority = await tx.authorityBinding(partitionId);
          control();
          if (currentGeneration != generation ||
              currentAuthority != binding.authorityBinding) {
            throw const LanternDataException(DataFailure.closed);
          }
        }

        await scope();
        await tx.effectiveTime(partitionId, highWater);
        control();
        await tx.invalidateTraversals(partitionId);
        await scope();
      });
      control();
      _traversalClock.committed(revision);
    } on OfflineException {
      // The shared owner's ended gate deliberately suppresses scheduling. Keep
      // failure here; failed close must not reach owner/resource release.
      _storageBlocked = true;
      rethrow;
    } on LanternDataException {
      _storageBlocked = true;
      rethrow;
    } catch (_) {
      _storageBlocked = true;
      throw const LanternDataException(DataFailure.storageBlocked);
    }
  }

  Future<T> _traversalTransaction<T>(
    Future<T> Function(OfflineStoreTransaction) action,
  ) async {
    var invalidated = 0;
    try {
      final value = await store.transaction((tx) async {
        Future<void> sample() async {
          final raw = _traversalClock.sample();
          final effective = await tx.effectiveTime(partitionId, raw);
          _traversalClock.observe(raw, effective);
          if (_traversalClock.invalidationPending &&
              invalidated != _traversalClock.rollbackRevision) {
            await tx.invalidateTraversals(partitionId);
            invalidated = _traversalClock.rollbackRevision;
          }
        }

        _fence();
        if (await tx.generation(partitionId) != generation ||
            await tx.authorityBinding(partitionId) !=
                binding.authorityBinding) {
          throw const LanternDataException(DataFailure.closed);
        }
        if (_session == null) {
          if (await tx.replayPausedForPolicy(partitionId)) {
            _standaloneReadCause = ObservationCause.permissionDenied;
          } else if (await tx.replayPausedForAuth(partitionId)) {
            _standaloneReadCause = ObservationCause.unauthenticated;
          }
        }
        await sample();
        final result = await action(tx);
        await sample();
        _fence();
        return result;
      });
      _traversalClock.committed(invalidated);
      _fence();
      _traversalClock.sample();
      return value;
    } on OfflineCapacityException {
      rethrow;
    } on OfflineArgumentException {
      rethrow;
    } on LanternDataException {
      rethrow;
    } on LanternChangeDiscontinuity {
      rethrow;
    } on OfflineDisposedException {
      rethrow;
    } on OfflineWorkInterruptedException {
      rethrow;
    } catch (_) {
      _storageBlocked = true;
      _session?._requestStoragePause();
      rethrow;
    }
  }

  ObservationCause? get _traversalHidden =>
      _storageBlocked ||
          _routeDiscontinuous ||
          !_traversalClock.eligible ||
          _session?.incoming?.allowReads == false
      ? ObservationCause.unavailable
      : _standaloneReadCause ?? _session?._hiddenCause;

  bool _canReuseTraversal(OfflineTraversalRecord r, int epoch) {
    final now = clock();
    final route = readRoute;
    return cacheReads &&
        route != null &&
        route.isCurrent() &&
        _traversalHidden == null &&
        r.generation == generation &&
        r.partitionId == partitionId &&
        r.authorityBinding == binding.authorityBinding &&
        r.validityEpoch == epoch &&
        r.responderId == route.responderId &&
        (r.ownerTag == _traversalOwner ||
            route.continuesRetainedResponder?.call(r.responderId) == true) &&
        now.isBefore(r.historyUntil);
  }

  LanternTraversalSnapshot _traversalSnapshot(
    LanternTraversalQuery query,
    PendingMetadata pending, {
    ConfirmedEvidence<Graph>? evidence,
    ObservationCause cause = ObservationCause.noCache,
    DateTime? nextExpiry,
  }) {
    final result = LanternTraversalSnapshot(
      query: query,
      pending: pending,
      sequence: ++_sequence,
      confirmed:
          evidence ??
          ConfirmedEvidence<Graph>(
            state: ConfirmedState.unknown,
            origin: ObservationOrigin.none,
            observedAt: clock(),
            freshness: ObservationFreshness.unavailable,
            cause: cause,
          ),
    );
    if (nextExpiry != null) _localExpiries[result] = nextExpiry;
    return result;
  }

  Future<LanternTraversalSnapshot> _localTraversal(
    LanternTraversalQuery query, {
    bool serverOnly = false,
    ObservationCause? cause,
  }) async {
    _fence();
    if (readRoute != null && !readRoute!.isCurrent()) {
      _routeDiscontinuous = true;
    }
    if (_routeDiscontinuous) await _invalidateTraversalRoute();
    await _session?.incoming?.verifyContinuity();
    final revision = _readRevision;
    final captured = await _traversalTransaction((tx) async {
      final epoch = await tx.traversalValidityEpoch(partitionId);
      final record = !serverOnly && cacheReads && readRoute != null
          ? await tx.getTraversal(partitionId, query.canonicalKey)
          : null;
      final eligible = record != null && _canReuseTraversal(record, epoch);
      if (eligible) {
        await tx.touchTraversal(partitionId, query.canonicalKey, clock());
      }
      final pending = offlinePendingSummary(await tx.outbox(partitionId));
      return (
        record: eligible ? record : null,
        epoch: epoch,
        pending: pending,
        phase: _traversalClock.phase,
      );
    });
    final record = captured.record;
    if (record != null &&
        revision == _readRevision &&
        captured.phase == _traversalClock.phase &&
        _canReuseTraversal(record, captured.epoch)) {
      final at = clock();
      return _traversalSnapshot(
        query,
        captured.pending,
        evidence: ConfirmedEvidence<Graph>(
          state: ConfirmedState.present,
          value: record.graph,
          origin: ObservationOrigin.retainedCache,
          observedAt: record.observedAt,
          expiration: record.sourceExpiration,
          freshness: ObservationFreshness.historical,
        ),
        nextExpiry: at.isBefore(record.freshUntil)
            ? record.freshUntil
            : record.historyUntil,
      );
    }
    return _traversalSnapshot(
      query,
      captured.pending,
      cause: _traversalHidden ?? cause ?? ObservationCause.noCache,
    );
  }

  Future<LanternTraversalSnapshot> _readTraversal(
    String seed, {
    required TraversalOptions traversal,
    required TraversalWeighting weighting,
    required String vertexPrefix,
    TraversalReadOptions? options,
  }) async {
    _fence();
    final query = LanternTraversalQuery(
      seed,
      traversal: traversal,
      weighting: weighting,
      vertexPrefix: vertexPrefix,
    );
    final source = options?.source ?? ReadSource.automatic;
    if (options?.timeout != null && options!.timeout! <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    LanternAdapterCancellation.checkCanceled(options?.cancellation);
    final session = _session;
    if (source == ReadSource.cacheOnly ||
        session != null &&
            (!session._networkAllowed ||
                session.incoming?.allowReads == false)) {
      return _localTraversal(
        query,
        serverOnly: source == ReadSource.serverOnly,
      );
    }
    Future<LanternTraversalSnapshot> attempt(OfflineSessionWork? work) async {
      void check() {
        work?.checkCurrent();
        LanternAdapterCancellation.checkCanceled(options?.cancellation);
        _checkReadAuthority();
      }

      final captured = await _traversalTransaction(
        (tx) async => (
          read: await tx.changeEpoch(partitionId),
          valid: await tx.traversalValidityEpoch(partitionId),
        ),
      );
      final phase = _traversalClock.phase, revision = _readRevision;
      final retain = _traversalClock.eligible;
      final token = LanternAdapterCancellation.guarded(check);
      final detachWork = work?.cancellation.listen((_) => token.cancel());
      final detachCaller = options?.cancellation?.listen((_) => token.cancel());
      try {
        check();
        final result = await _direct.illuminate(
          seed,
          traversal: traversal,
          weighting: weighting,
          vertexPrefix: vertexPrefix,
          options: TraversalReadOptions(
            source: ReadSource.serverOnly,
            timeout: options?.timeout,
            cancellation: token,
          ),
        );
        check();
        final observedAt = _traversalClock.sample();
        final cause = result.confirmed.cause;
        if (cause == ObservationCause.unauthenticated ||
            cause == ObservationCause.permissionDenied) {
          if (session != null) {
            await session._pauseFromRead(cause!);
          } else {
            await store.transaction((tx) async {
              if (cause == ObservationCause.unauthenticated) {
                await tx.setReplayPausedForAuth(partitionId, true);
              } else {
                await tx.setReplayPausedForPolicy(partitionId, true);
              }
              await tx.invalidateTraversals(partitionId);
            });
          }
          return _localTraversal(query, serverOnly: true, cause: cause);
        }
        if (result.confirmed.state == ConfirmedState.unknown) {
          return _localTraversal(
            query,
            serverOnly: source == ReadSource.serverOnly,
            cause: cause,
          );
        }
        OfflineTraversalRecord? saved;
        final settled = await _traversalTransaction((tx) async {
          check();
          final current =
              captured.read == await tx.changeEpoch(partitionId) &&
              captured.valid == await tx.traversalValidityEpoch(partitionId) &&
              revision == _readRevision &&
              (!retain || phase == _traversalClock.phase);
          final graph = result.confirmed.value;
          if (current &&
              graph != null &&
              cacheReads &&
              readRoute != null &&
              retain &&
              phase == _traversalClock.phase &&
              _traversalClock.eligible) {
            final expiration = OfflineTraversalRecord.expirationOf(graph);
            final security = session?.credentialExpires ?? binding.expiresAt;
            final fresh = OfflineTraversalRecord.deadline(
              observedAt,
              OfflineTraversalRecord.freshAge,
              expiration,
              security,
            );
            final history = OfflineTraversalRecord.deadline(
              observedAt,
              OfflineTraversalRecord.historyAge,
              expiration,
              security,
            );
            if (clock().isBefore(fresh)) {
              saved = OfflineTraversalRecord(
                partitionId: partitionId,
                generation: generation,
                authorityBinding: binding.authorityBinding,
                responderId: readRoute!.responderId,
                ownerTag: _traversalOwner,
                queryKey: query.canonicalKey,
                validityEpoch: captured.valid,
                graph: graph,
                observedAt: observedAt,
                freshUntil: fresh,
                historyUntil: history,
                sourceExpiration: expiration,
                securityUntil: security,
                lastAccessAt: clock(),
              );
              // Oversized records fail typed capacity; never truncate or hide it.
              OfflineTraversalCodec.encode(saved!);
              await tx.putTraversal(partitionId, saved!);
            }
          }
          check();
          return (
            current: current,
            pending: offlinePendingSummary(await tx.outbox(partitionId)),
          );
        });
        check();
        if (!settled.current ||
            revision != _readRevision ||
            retain && phase != _traversalClock.phase) {
          return _localTraversal(
            query,
            serverOnly: source == ReadSource.serverOnly,
            cause: ObservationCause.unavailable,
          );
        }
        final c = result.confirmed;
        final expiry = c.expiration;
        final now = clock();
        if (retain && phase != _traversalClock.phase) {
          return _localTraversal(
            query,
            serverOnly: true,
            cause: ObservationCause.unavailable,
          );
        }
        final expired = expiry != null && !now.isBefore(expiry);
        return _traversalSnapshot(
          query,
          settled.pending,
          evidence: ConfirmedEvidence<Graph>(
            state: expired ? ConfirmedState.expired : c.state,
            value: expired ? null : c.value,
            origin: c.origin,
            observedAt: observedAt,
            expiration: expiry,
            freshness: c.freshness,
            cause: c.cause,
          ),
          nextExpiry: saved?.freshUntil,
        );
      } finally {
        detachWork?.call();
        detachCaller?.call();
      }
    }

    try {
      return session == null
          ? await attempt(null)
          : await session.runtime.readOnce(
              attempt,
              traversal: true,
              cancellation: options?.cancellation,
            );
    } on LanternChangeDiscontinuity {
      _routeDiscontinuous = true;
      await _invalidateTraversalRoute();
      await session?.incoming?.discontinue();
      return _localTraversal(
        query,
        serverOnly: true,
        cause: ObservationCause.unavailable,
      );
    } on OfflineWorkInterruptedException {
      return _localTraversal(
        query,
        serverOnly: source == ReadSource.serverOnly,
        cause: ObservationCause.unavailable,
      );
    } on OfflineCapacityException {
      throw const LanternDataException(DataFailure.capacity);
    }
  }
}
