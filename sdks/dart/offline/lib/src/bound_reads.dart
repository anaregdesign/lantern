part of 'bound_data_client.dart';

final class _BoundReadBatch<T> {
  _BoundReadBatch(this.values, [this.dependencies = const {}]);
  final List<LanternSnapshot<T>> values;
  final Map<OfflineEntityKey, ConfirmedEvidence<Vertex>> dependencies;
}

ConfirmedEvidence<T> _capEvidence<T>(
  ConfirmedEvidence<T> evidence,
  DateTime? until,
  DateTime now,
) {
  if (until == null ||
      evidence.expiration != null && !until.isBefore(evidence.expiration!)) {
    return offlineEvidenceAt(evidence, now);
  }
  return offlineEvidenceAt(
    ConfirmedEvidence<T>(
      state: evidence.state,
      value: evidence.value,
      origin: evidence.origin,
      observedAt: evidence.observedAt,
      expiration: until,
      missingUntil: evidence.missingUntil,
      freshness: evidence.freshness,
      cause: evidence.cause,
    ),
    now,
  );
}

extension _BoundReads on OfflineLanternDataClient {
  List<OfflineEntityKey> _readKeys(Iterable<OfflineEntityKey> keys) {
    final input = keys.take(LanternCrud.maxBatchSize + 1).toList();
    if (input.length > LanternCrud.maxBatchSize ||
        input.any(
          (key) => key.kind == OfflineEntityKind.vertex
              ? key.vertexKey!.isEmpty
              : key.tail!.isEmpty || key.head!.isEmpty,
        )) {
      throw const OfflineArgumentException();
    }
    return input;
  }

  Future<List<LanternSnapshot<Vertex>>> _readVertices(
    Iterable<String> keys, {
    GetOptions? options,
  }) => _readMany<Vertex>(
    _readKeys(keys.map(OfflineEntityKey.vertex)),
    options,
    (keys, token) async => _BoundReadBatch(
      await _direct.getVertices(
        keys.map((key) => key.vertexKey!),
        options: GetOptions(
          source: ReadSource.serverOnly,
          timeout: options?.timeout,
          cancellation: token,
        ),
      ),
    ),
  );

  Future<List<LanternSnapshot<Edge>>> _readEdges(
    Iterable<EdgeRef> edges, {
    GetOptions? options,
  }) => _readMany<Edge>(
    _readKeys(edges.map((edge) => OfflineEntityKey.edge(edge.tail, edge.head))),
    options,
    (keys, token) => _fetchEdges(
      keys,
      token,
      getEdges: (edges, token) => _direct.getEdges(
        edges,
        options: GetOptions(
          source: ReadSource.serverOnly,
          timeout: options?.timeout,
          cancellation: token,
        ),
      ),
      getVertices: (keys, token) => _direct.getVertices(
        keys,
        options: GetOptions(
          source: ReadSource.serverOnly,
          timeout: options?.timeout,
          cancellation: token,
        ),
      ),
    ),
  );

  Future<_BoundReadBatch<Edge>> _fetchEdges(
    List<OfflineEntityKey> keys,
    LanternCancellationToken? cancellation, {
    required Future<List<LanternSnapshot<Edge>>> Function(
      List<EdgeRef>,
      LanternCancellationToken?,
    )
    getEdges,
    required Future<List<LanternSnapshot<Vertex>>> Function(
      List<String>,
      LanternCancellationToken?,
    )
    getVertices,
  }) async {
    final edges = await getEdges([
      for (final key in keys) EdgeRef(key.tail!, key.head!),
    ], cancellation);
    final endpoints = <String>{
      for (var i = 0; i < keys.length; i++)
        if (edges[i].confirmed.state == ConfirmedState.present) ...[
          keys[i].tail!,
          keys[i].head!,
        ],
    }.toList();
    final vertices = <LanternSnapshot<Vertex>>[];
    // A page of 32 unrelated Edges can have 64 distinct dependencies. Keep the
    // same per-RPC bound for those reads; the enclosing epoch fences the page.
    for (var offset = 0; offset < endpoints.length; offset += 32) {
      vertices.addAll(
        await getVertices(
          endpoints.sublist(offset, min(offset + 32, endpoints.length)),
          cancellation,
        ),
      );
    }
    final dependencies = {
      for (var i = 0; i < endpoints.length; i++)
        OfflineEntityKey.vertex(endpoints[i]): vertices[i].confirmed,
    };
    final values = <LanternSnapshot<Edge>>[];
    for (var i = 0; i < keys.length; i++) {
      var evidence = edges[i].confirmed;
      if (evidence.state == ConfirmedState.present) {
        for (final endpoint in [keys[i].tail!, keys[i].head!]) {
          final dependency = dependencies[OfflineEntityKey.vertex(endpoint)]!;
          if (dependency.state != ConfirmedState.present) {
            evidence = ConfirmedEvidence(
              state: ConfirmedState.unknown,
              origin: ObservationOrigin.none,
              observedAt: evidence.observedAt,
              freshness: ObservationFreshness.invalidated,
              cause: dependency.cause ?? ObservationCause.unavailable,
            );
            break;
          }
          evidence = _capEvidence(evidence, dependency.expiration, clock());
        }
      }
      values.add(LanternSnapshot(confirmed: evidence, view: evidence.value));
    }
    return _BoundReadBatch(values, dependencies);
  }

  Future<void> _retainRead<T>(
    OfflineStoreTransaction tx,
    OfflineEntityKey key,
    ConfirmedEvidence<T> c,
  ) async {
    if (c.state == ConfirmedState.present) {
      await tx.putCache(
        partitionId,
        OfflineCacheRecord.value(
          partitionId: partitionId,
          generation: generation,
          key: key,
          entity: c.value as Object,
          validatedAt: c.observedAt,
          lastAccessAt: clock(),
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
          lastAccessAt: clock(),
          missingUntil: c.observedAt.add(const Duration(seconds: 30)),
        ),
      );
    }
  }

  Future<List<LanternSnapshot<T>>> _readMany<T>(
    List<OfflineEntityKey> input,
    GetOptions? options,
    Future<_BoundReadBatch<T>> Function(
      List<OfflineEntityKey>,
      LanternCancellationToken?,
    )
    fetch, {
    bool recovering = false,
  }) async {
    _fence();
    final o = options ?? const GetOptions();
    if (o.timeout != null && o.timeout! <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    LanternAdapterCancellation.checkCanceled(o.cancellation);
    final result = <OfflineEntityKey, LanternSnapshot<T>>{};
    final stamps = <OfflineEntityKey, (int, int?)>{};
    for (final key in input.toSet()) {
      result[key] = await _localForSource<T>(key, o.source);
    }
    LanternAdapterCancellation.checkCanceled(o.cancellation);
    final session = _session, incoming = _session?.incoming;
    final eligible = result.keys.where((key) {
      final local = result[key]!;
      return o.source != ReadSource.cacheOnly &&
          (session == null || session._networkAllowed) &&
          (incoming == null || incoming.allowReads) &&
          (recovering ||
              o.source != ReadSource.automatic ||
              local.projection != PendingProjection.exactPendingPut &&
                  local.projection != PendingProjection.pendingDelete);
    }).toList();
    for (var offset = 0; offset < eligible.length; offset += 32) {
      final batch = eligible.sublist(offset, min(offset + 32, eligible.length));
      Future<void> attempt(OfflineSessionWork? work) async {
        final epoch = await store.transaction(
          (tx) => tx.changeEpoch(partitionId),
        );
        final revision = incoming?.revision;
        void check() {
          work?.checkCurrent();
          LanternAdapterCancellation.checkCanceled(o.cancellation);
          _fence();
          incoming?.source.checkCurrent();
        }

        final token = LanternAdapterCancellation.guarded(check);
        final detachWork = work?.cancellation.listen((_) => token.cancel());
        final detachCaller = o.cancellation?.listen((_) => token.cancel());
        late _BoundReadBatch<T> response;
        try {
          check();
          response = await fetch(batch, token);
          check();
        } finally {
          detachWork?.call();
          detachCaller?.call();
        }
        if (response.values.length != batch.length) {
          throw const OfflineRemoteProtocolException();
        }
        for (final value in response.values) {
          final cause = value.confirmed.cause;
          if (cause == ObservationCause.unauthenticated ||
              cause == ObservationCause.permissionDenied) {
            if (session != null) {
              await session._pauseFromRead(cause!);
            } else {
              await store.transaction(
                (tx) => tx.resetScopedChangeCursor(partitionId, null),
              );
            }
            for (final key in batch) {
              result[key] = await _local<T>(
                key,
                evidence: value.confirmed,
                serverOnly: true,
              );
            }
            return;
          }
        }
        var current = false;
        await store.transaction((tx) async {
          check();
          if (await tx.generation(partitionId) != generation ||
              await tx.authorityBinding(partitionId) !=
                  binding.authorityBinding ||
              await tx.changeEpoch(partitionId) != epoch ||
              incoming?.revision != revision) {
            return;
          }
          if (cacheReads) {
            for (final entry in response.dependencies.entries) {
              await _retainRead(tx, entry.key, entry.value);
            }
          }
          for (var i = 0; i < batch.length; i++) {
            final c = offlineEvidenceAt(response.values[i].confirmed, clock());
            if (c.state == ConfirmedState.unknown) continue;
            if (recovering &&
                !await tx.completeUnknownResident(
                  partitionId,
                  batch[i],
                  expectedEpoch: epoch,
                )) {
              continue;
            }
            if (recovering) await tx.deleteCache(partitionId, batch[i]);
            if (cacheReads) await _retainRead(tx, batch[i], c);
          }
          check();
          current = true;
        });
        check();
        current &=
            incoming?.revision == revision &&
            await store.transaction((tx) => tx.changeEpoch(partitionId)) ==
                epoch;
        for (var i = 0; i < batch.length; i++) {
          final key = batch[i], evidence = response.values[i].confirmed;
          if (current) stamps[key] = (epoch, revision);
          if (!current) {
            result[key] = await _localForSource<T>(key, o.source);
          } else if (evidence.state != ConfirmedState.unknown ||
              o.source == ReadSource.serverOnly) {
            result[key] = await _local<T>(
              key,
              evidence: evidence,
              serverOnly: o.source == ReadSource.serverOnly,
            );
          } else {
            final local = await _local<T>(key);
            result[key] = local.confirmed.state == ConfirmedState.unknown
                ? await _local<T>(key, evidence: evidence)
                : local;
          }
        }
        if (recovering && current) _observations.invalidate();
      }

      try {
        if (session == null) {
          await attempt(null);
        } else {
          await session.runtime.readOnce(attempt, cancellation: o.cancellation);
        }
      } on OfflineWorkInterruptedException {
        _fence();
        LanternAdapterCancellation.checkCanceled(o.cancellation);
        for (final key in batch) {
          result[key] = await _localForSource<T>(key, o.source);
        }
      } on LanternChangeDiscontinuity {
        if (incoming == null) rethrow;
        await incoming.discontinue();
        for (final key in batch) {
          result[key] = await _localForSource<T>(key, o.source);
        }
      }
    }
    _fence();
    LanternAdapterCancellation.checkCanceled(o.cancellation);
    final epoch = await store.transaction((tx) => tx.changeEpoch(partitionId));
    for (final key in stamps.keys) {
      if (stamps[key] != (epoch, incoming?.revision)) {
        result[key] = await _localForSource<T>(key, o.source);
      }
    }
    _fence();
    LanternAdapterCancellation.checkCanceled(o.cancellation);
    return List.unmodifiable(input.map((key) => result[key]!));
  }
}
