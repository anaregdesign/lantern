part of 'client.dart';

extension _DirectReads on DirectLanternDataClient {
  Future<LanternSnapshot<Vertex>> _directGetVertex(
    String key, {
    GetOptions? options,
  }) async => (await _directGetVertices([key], options: options)).single;
  Future<List<LanternSnapshot<Vertex>>> _directGetVertices(
    Iterable<String> keys, {
    GetOptions? options,
  }) async {
    _ensureOpen();
    final input = _boundedInputs(keys);
    for (final key in input) {
      _requireEntityKey(key);
    }
    _validateReadOptions(options?.timeout);
    _throwIfCanceled(options?.cancellation);
    final started = Stopwatch()..start();
    final snapshots = <LanternSnapshot<Vertex>>[];
    if (options?.source == ReadSource.cacheOnly) {
      return List.unmodifiable(
        input.map(
          (key) => _snapshot<Vertex>(
            null,
            ConfirmedState.unknown,
            jsonEncode(['v', key]),
            cause: ObservationCause.noCache,
          ),
        ),
      );
    }
    for (var start = 0; start < input.length; start += batchSize) {
      final chunk = input.sublist(
        start,
        _chunkEnd(start, batchSize, input.length),
      );
      try {
        final response = await _read(
          _remainingReadOptions(options, started),
          (o) => _client.getVertices(chunk, batchSize: batchSize, options: o),
        );
        final present = <String, Queue<Vertex>>{};
        final missing = <String, int>{};
        for (final value in response.vertices) {
          (present[value.key] ??= Queue()).add(value);
        }
        for (final key in response.missing) {
          missing.update(key, (n) => n + 1, ifAbsent: () => 1);
        }
        for (final key in chunk) {
          final values = present[key];
          if (values != null && values.isNotEmpty) {
            snapshots.add(
              _snapshot(
                values.removeFirst(),
                ConfirmedState.present,
                jsonEncode(['v', key]),
              ),
            );
          } else if ((missing[key] ?? 0) > 0) {
            missing[key] = missing[key]! - 1;
            snapshots.add(
              _snapshot<Vertex>(
                null,
                ConfirmedState.missing,
                jsonEncode(['v', key]),
              ),
            );
          } else {
            throw _internalSdkException('misaligned Vertex read result');
          }
        }
        if (present.values.any((q) => q.isNotEmpty) ||
            missing.values.any((n) => n != 0)) {
          throw _internalSdkException('extra Vertex read result');
        }
      } on LanternException catch (error) {
        final cause = _observationCause(error);
        if (cause == null) rethrow;
        snapshots.addAll(
          chunk.map(
            (key) => _snapshot<Vertex>(
              null,
              ConfirmedState.unknown,
              jsonEncode(['v', key]),
              cause: cause,
            ),
          ),
        );
      }
    }
    return List.unmodifiable(snapshots);
  }

  Future<LanternSnapshot<Edge>> _directGetEdge(
    EdgeRef edge, {
    GetOptions? options,
  }) async => (await _directGetEdges([edge], options: options)).single;
  Future<List<LanternSnapshot<Edge>>> _directGetEdges(
    Iterable<EdgeRef> edges, {
    GetOptions? options,
  }) async {
    _ensureOpen();
    final input = _boundedInputs(edges);
    for (final edge in input) {
      _requireEntityKey(edge.tail);
      _requireEntityKey(edge.head);
    }
    _validateReadOptions(options?.timeout);
    _throwIfCanceled(options?.cancellation);
    final started = Stopwatch()..start();
    final snapshots = <LanternSnapshot<Edge>>[];
    String identity(EdgeRef edge) => jsonEncode(['e', edge.tail, edge.head]);
    if (options?.source == ReadSource.cacheOnly) {
      return List.unmodifiable(
        input.map(
          (edge) => _snapshot<Edge>(
            null,
            ConfirmedState.unknown,
            identity(edge),
            cause: ObservationCause.noCache,
          ),
        ),
      );
    }
    for (var start = 0; start < input.length; start += batchSize) {
      final chunk = input.sublist(
        start,
        _chunkEnd(start, batchSize, input.length),
      );
      try {
        final response = await _read(
          _remainingReadOptions(options, started),
          (o) => _client.getEdges(chunk, batchSize: batchSize, options: o),
        );
        final present = <EdgeRef, Queue<Edge>>{};
        final missing = <EdgeRef, int>{};
        for (final value in response.edges) {
          (present[EdgeRef(value.tail, value.head)] ??= Queue()).add(value);
        }
        for (final ref in response.missing) {
          missing.update(ref, (n) => n + 1, ifAbsent: () => 1);
        }
        for (final ref in chunk) {
          final values = present[ref];
          if (values != null && values.isNotEmpty) {
            snapshots.add(
              _snapshot(
                values.removeFirst(),
                ConfirmedState.present,
                identity(ref),
              ),
            );
          } else if ((missing[ref] ?? 0) > 0) {
            missing[ref] = missing[ref]! - 1;
            snapshots.add(
              _snapshot<Edge>(null, ConfirmedState.missing, identity(ref)),
            );
          } else {
            throw _internalSdkException('misaligned Edge read result');
          }
        }
        if (present.values.any((q) => q.isNotEmpty) ||
            missing.values.any((n) => n != 0)) {
          throw _internalSdkException('extra Edge read result');
        }
      } on LanternException catch (error) {
        final cause = _observationCause(error);
        if (cause == null) rethrow;
        snapshots.addAll(
          chunk.map(
            (ref) => _snapshot<Edge>(
              null,
              ConfirmedState.unknown,
              identity(ref),
              cause: cause,
            ),
          ),
        );
      }
    }
    return List.unmodifiable(snapshots);
  }

  Future<LanternTraversalSnapshot> _directIlluminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    TraversalReadOptions? options,
  }) async {
    _ensureOpen();
    final query = LanternTraversalQuery(
      seed,
      traversal: traversal,
      weighting: weighting,
      vertexPrefix: vertexPrefix,
    );
    _validateReadOptions(options?.timeout);
    _throwIfCanceled(options?.cancellation);
    if (options?.source == ReadSource.cacheOnly) {
      return _traversalUnknown(ObservationCause.noCache, query: query);
    }
    try {
      final graph = await _read(
        GetOptions(
          timeout: options?.timeout,
          cancellation: options?.cancellation,
        ),
        (o) => _client.illuminate(
          seed,
          traversal: traversal,
          weighting: weighting,
          vertexPrefix: vertexPrefix,
          options: o,
        ),
        traversal: true,
      );
      _checkTraversalBounds(graph);
      final expirations = [
        ...graph.vertices.values.map((v) => v.expiration),
        ...graph.allEdges.map((e) => e.expiration),
      ].whereType<DateTime>().toList()..sort();
      final now = _client._clock().toUtc();
      final expiration = expirations.isEmpty ? null : expirations.first;
      final expired = expiration != null && !expiration.isAfter(now);
      return LanternTraversalSnapshot(
        query: query,
        pending: _mutations.traversalPending,
        confirmed: ConfirmedEvidence<Graph>(
          state: expired ? ConfirmedState.expired : ConfirmedState.present,
          value: expired ? null : graph,
          expiration: expiration,
          origin: ObservationOrigin.server,
          observedAt: _client._clock().toUtc(),
          freshness: ObservationFreshness.freshObservation,
        ),
        sequence: ++_sequence,
      );
    } on LanternException catch (error) {
      final cause = _observationCause(error);
      if (cause == null) rethrow;
      return _traversalUnknown(cause, query: query);
    }
  }

  void _checkTraversalBounds(Graph graph) {
    if (graph.vertices.length > maxTraversalVertices) {
      throw const LanternDataException(DataFailure.capacity);
    }
    var bytes = 0, edgeCount = 0;
    for (final vertex in graph.vertices.values) {
      bytes +=
          _vertexInputToProto(
            VertexInput(key: vertex.key, value: vertex.value),
            vertex.expiration,
          ).writeToBuffer().length +
          16;
      if (bytes > maxTraversalBytes) {
        throw const LanternDataException(DataFailure.capacity);
      }
    }
    for (final byHead in graph.edges.values) {
      for (final edge in byHead.values) {
        edgeCount++;
        bytes +=
            $graph.Edge(
              tail: edge.tail,
              head: edge.head,
              weight: edge.weight,
              expiration: edge.expiration == null
                  ? null
                  : _timestampToProto(edge.expiration!),
            ).writeToBuffer().length +
            16;
        if (edgeCount > maxTraversalEdges || bytes > maxTraversalBytes) {
          throw const LanternDataException(DataFailure.capacity);
        }
      }
    }
  }
}
