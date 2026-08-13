part of 'client.dart';

/// Core vertex and edge operations for [LanternClient].
///
/// Plural methods are canonical and automatically split requests into chunks
/// of [defaultBatchSize]. No logical call may exceed [maxBatchSize]. The SDK
/// retries only operations allowed by the fail-closed retry registry.
extension LanternCrud on LanternClient {
  /// Default number of items sent in one plural RPC.
  static const int defaultBatchSize = 1000;

  /// Maximum number of items in one logical batch.
  static const int maxBatchSize = 65536;

  /// Reads one vertex or throws [LanternNotFoundException].
  Future<Vertex> getVertex(String key, {LanternCallOptions? options}) async {
    final result = await getVertices([key], options: options);
    if (result.vertices.isEmpty) {
      throw _notFoundException('vertex "$key" not found');
    }
    return result.vertices.single;
  }

  /// Reads vertices in bounded chunks, preserving present/missing semantics.
  Future<GetVerticesResult> getVertices(
    Iterable<String> keys, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<String>.unmodifiable(keys);
    _validateBatch(input.length, batchSize);
    final callOptions = _freezeCallOptions(options);
    final vertices = <Vertex>[];
    final missing = <String>[];
    for (var offset = 0; offset < input.length; offset += batchSize) {
      _throwIfCanceled(callOptions?.cancellation);
      final end = _chunkEnd(offset, batchSize, input.length);
      final request = $graph.GetVerticesRequest(
        keys: input.sublist(offset, end),
      );
      final response = await _invoke(
        'GetVertices',
        callOptions,
        (raw, headers, signal, onHeader, onTrailer) => raw.getVertices(
          request,
          headers: headers,
          signal: signal,
          onHeader: onHeader,
          onTrailer: onTrailer,
        ),
      );
      vertices.addAll(response.vertices.map(_vertexFromProto));
      missing.addAll(response.missing);
    }
    return GetVerticesResult(vertices: vertices, missing: missing);
  }

  /// Unconditionally writes one vertex and returns its authoritative outcome.
  Future<PutOutcome> putVertex(
    VertexInput input, {
    LanternCallOptions? options,
  }) async {
    final result = await putVertices([input], options: options);
    return result.single.outcome;
  }

  /// Unconditionally writes vertices in bounded chunks.
  Future<List<VertexPutResult>> putVertices(
    Iterable<VertexInput> inputs, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) => _putVertices(
    inputs,
    ifAbsent: false,
    batchSize: batchSize,
    options: options,
  );

  /// Writes one vertex only when no live value exists.
  Future<PutOutcome> putVertexIfAbsent(
    VertexInput input, {
    LanternCallOptions? options,
  }) async {
    final result = await putVerticesIfAbsent([input], options: options);
    return result.single.outcome;
  }

  /// Conditionally writes vertices in bounded chunks.
  ///
  /// If a later chunk fails, [BatchException.committed] covers only prior
  /// chunks whose responses were observed. The failed chunk may already have
  /// committed; retry evaluates the condition again and cannot recover the
  /// original per-item outcomes. Reconcile server state before retrying it.
  Future<List<VertexPutResult>> putVerticesIfAbsent(
    Iterable<VertexInput> inputs, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) => _putVertices(
    inputs,
    ifAbsent: true,
    batchSize: batchSize,
    options: options,
  );

  Future<List<VertexPutResult>> _putVertices(
    Iterable<VertexInput> inputs, {
    required bool ifAbsent,
    required int batchSize,
    required LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<VertexInput>.unmodifiable(inputs);
    _validateBatch(input.length, batchSize);
    final sampledAt = _clock().toUtc();
    final expirations = _resolveExpirations(input, sampledAt);
    final initiallyLive = _initialLiveness(expirations, sampledAt);
    final callOptions = _freezeCallOptions(options);
    final results = <VertexPutResult>[];
    for (var offset = 0; offset < input.length; offset += batchSize) {
      try {
        _throwIfCanceled(callOptions?.cancellation);
        final end = _chunkEnd(offset, batchSize, input.length);
        final chunk = <$graph.Vertex>[];
        for (var index = offset; index < end; index++) {
          chunk.add(_vertexInputToProto(input[index], expirations[index]));
        }
        final request = $graph.PutVerticesRequest(
          vertices: chunk,
          ifAbsent: ifAbsent,
        );
        final response = await _invoke(
          ifAbsent ? 'PutVertexIfAbsent' : 'PutVertices',
          callOptions,
          (raw, headers, signal, onHeader, onTrailer) => raw.putVertices(
            request,
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
        );
        if (response.outcomes.length != end - offset) {
          throw _internalSdkException(
            'server returned misaligned vertex Put outcomes',
          );
        }
        final chunkResults = <VertexPutResult>[];
        for (var index = 0; index < response.outcomes.length; index++) {
          final inputIndex = offset + index;
          chunkResults.add(
            VertexPutResult(
              key: input[inputIndex].key,
              outcome: _putOutcomeFromProto(response.outcomes[index]),
            ),
          );
        }
        results.addAll(chunkResults);
      } on Exception catch (error) {
        _throwBatchOrCause(results.length, error);
      }
    }
    if (results.isEmpty) return List.unmodifiable(results);
    final observedAt = _clock().toUtc();
    return List.unmodifiable([
      for (var index = 0; index < results.length; index++)
        VertexPutResult(
          key: results[index].key,
          outcome: _clientBoundedPutOutcome(
            results[index].outcome,
            initiallyLive[index],
            expirations[index],
            observedAt,
          ),
        ),
    ]);
  }

  /// Deletes one vertex and reports whether it existed.
  Future<bool> deleteVertex(String key, {LanternCallOptions? options}) async =>
      (await deleteVertices([key], options: options)) == 1;

  /// Deletes vertices in bounded chunks and returns the number removed.
  Future<int> deleteVertices(
    Iterable<String> keys, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<String>.unmodifiable(keys);
    _validateBatch(input.length, batchSize);
    final callOptions = _freezeCallOptions(options);
    var deleted = 0;
    for (var offset = 0; offset < input.length; offset += batchSize) {
      try {
        _throwIfCanceled(callOptions?.cancellation);
        final end = _chunkEnd(offset, batchSize, input.length);
        final request = $graph.DeleteVerticesRequest(
          keys: input.sublist(offset, end),
        );
        final response = await _invoke(
          'DeleteVertices',
          callOptions,
          (raw, headers, signal, onHeader, onTrailer) => raw.deleteVertices(
            request,
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
        );
        deleted += response.deleted;
      } on Exception catch (error) {
        _throwBatchOrCause(deleted, error);
      }
    }
    return deleted;
  }

  /// Reads one edge or throws [LanternNotFoundException].
  Future<Edge> getEdge(EdgeRef edge, {LanternCallOptions? options}) async {
    final result = await getEdges([edge], options: options);
    if (result.edges.isEmpty) {
      throw _notFoundException(
        'edge "${edge.tail}" -> "${edge.head}" not found',
      );
    }
    return result.edges.single;
  }

  /// Reads edges in bounded chunks, preserving present/missing semantics.
  Future<GetEdgesResult> getEdges(
    Iterable<EdgeRef> edges, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<EdgeRef>.unmodifiable(edges);
    _validateBatch(input.length, batchSize);
    final callOptions = _freezeCallOptions(options);
    final present = <Edge>[];
    final missing = <EdgeRef>[];
    for (var offset = 0; offset < input.length; offset += batchSize) {
      _throwIfCanceled(callOptions?.cancellation);
      final end = _chunkEnd(offset, batchSize, input.length);
      final request = $graph.GetEdgesRequest(
        edges: input
            .sublist(offset, end)
            .map((edge) => $graph.EdgeKey(tail: edge.tail, head: edge.head)),
      );
      final response = await _invoke(
        'GetEdges',
        callOptions,
        (raw, headers, signal, onHeader, onTrailer) => raw.getEdges(
          request,
          headers: headers,
          signal: signal,
          onHeader: onHeader,
          onTrailer: onTrailer,
        ),
      );
      present.addAll(response.edges.map(_edgeFromProto));
      missing.addAll(
        response.missing.map((edge) => EdgeRef(edge.tail, edge.head)),
      );
    }
    return GetEdgesResult(edges: present, missing: missing);
  }

  /// Additively writes one edge and returns its effective live weight.
  ///
  /// This operation is non-idempotent without a caller-supplied `contribId`.
  Future<double> addEdge(EdgeInput edge, {LanternCallOptions? options}) async {
    final result = await addEdges([edge], options: options);
    return result.effectiveWeights.single;
  }

  /// Additively writes edges in bounded chunks.
  Future<AddEdgesResult> addEdges(
    Iterable<EdgeInput> edges, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<EdgeInput>.unmodifiable(edges);
    _validateBatch(input.length, batchSize);
    final expirations = _resolveExpirations(input, _clock());
    var validatedContribIds = input
        .map(_validatedContribId)
        .toList(growable: false);
    if (_idempotentAdds) {
      validatedContribIds = _contribIds.fillMissing(validatedContribIds);
    }
    final additiveSafe = validatedContribIds.every((value) => value != null);
    final callOptions = _freezeCallOptions(options);
    final weights = <double>[];
    var written = 0;
    for (var offset = 0; offset < input.length; offset += batchSize) {
      try {
        _throwIfCanceled(callOptions?.cancellation);
        final end = _chunkEnd(offset, batchSize, input.length);
        final wireEdges = <$graph.Edge>[];
        final contribIds = <List<int>>[];
        var hasContribId = false;
        for (var index = offset; index < end; index++) {
          final edge = input[index];
          wireEdges.add(_edgeInputToProto(edge, expirations[index]));
          final id = validatedContribIds[index];
          contribIds.add(id ?? const []);
          hasContribId |= id != null;
        }
        final request = $graph.AddEdgesRequest(
          edges: wireEdges,
          contribIds: hasContribId ? contribIds : null,
        );
        final response = await _invoke(
          'AddEdges',
          callOptions,
          (raw, headers, signal, onHeader, onTrailer) => raw.addEdges(
            request,
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
          additiveSafe: additiveSafe,
        );
        if (response.effectiveWeights.length != end - offset) {
          throw _internalSdkException(
            'server returned misaligned additive edge results',
          );
        }
        written += response.written;
        weights.addAll(response.effectiveWeights);
      } on Exception catch (error) {
        _throwBatchOrCause(written, error);
      }
    }
    return AddEdgesResult(written: written, effectiveWeights: weights);
  }

  /// Idempotently overwrites one edge and returns its authoritative outcome.
  Future<PutOutcome> putEdge(
    EdgeInput edge, {
    LanternCallOptions? options,
  }) async {
    return (await putEdges([edge], options: options)).single.outcome;
  }

  /// Idempotently overwrites edges with index-aligned authoritative outcomes.
  Future<List<EdgePutResult>> putEdges(
    Iterable<EdgeInput> edges, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<EdgeInput>.unmodifiable(edges);
    _validateBatch(input.length, batchSize);
    final sampledAt = _clock().toUtc();
    final expirations = _resolveExpirations(input, sampledAt);
    final initiallyLive = _initialLiveness(expirations, sampledAt);
    final callOptions = _freezeCallOptions(options);
    final results = <EdgePutResult>[];
    for (var offset = 0; offset < input.length; offset += batchSize) {
      try {
        _throwIfCanceled(callOptions?.cancellation);
        final end = _chunkEnd(offset, batchSize, input.length);
        final chunk = <$graph.Edge>[];
        for (var index = offset; index < end; index++) {
          chunk.add(_edgeInputToProto(input[index], expirations[index]));
        }
        final request = $graph.PutEdgesRequest(edges: chunk);
        final response = await _invoke(
          'PutEdges',
          callOptions,
          (raw, headers, signal, onHeader, onTrailer) => raw.putEdges(
            request,
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
        );
        if (response.outcomes.length != end - offset) {
          throw _internalSdkException(
            'server returned misaligned edge Put outcomes',
          );
        }
        final chunkResults = <EdgePutResult>[];
        for (var index = 0; index < response.outcomes.length; index++) {
          final inputIndex = offset + index;
          final inputEdge = input[inputIndex];
          chunkResults.add(
            EdgePutResult(
              tail: inputEdge.tail,
              head: inputEdge.head,
              outcome: _putOutcomeFromProto(response.outcomes[index]),
            ),
          );
        }
        results.addAll(chunkResults);
      } on Exception catch (error) {
        _throwBatchOrCause(results.length, error);
      }
    }
    if (results.isEmpty) return List.unmodifiable(results);
    final observedAt = _clock().toUtc();
    return List.unmodifiable([
      for (var index = 0; index < results.length; index++)
        EdgePutResult(
          tail: results[index].tail,
          head: results[index].head,
          outcome: _clientBoundedPutOutcome(
            results[index].outcome,
            initiallyLive[index],
            expirations[index],
            observedAt,
          ),
        ),
    ]);
  }

  /// Deletes one edge and reports whether it existed.
  Future<bool> deleteEdge(EdgeRef edge, {LanternCallOptions? options}) async =>
      (await deleteEdges([edge], options: options)) == 1;

  /// Deletes edges in bounded chunks and returns the number removed.
  Future<int> deleteEdges(
    Iterable<EdgeRef> edges, {
    int batchSize = defaultBatchSize,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<EdgeRef>.unmodifiable(edges);
    _validateBatch(input.length, batchSize);
    final callOptions = _freezeCallOptions(options);
    var deleted = 0;
    for (var offset = 0; offset < input.length; offset += batchSize) {
      try {
        _throwIfCanceled(callOptions?.cancellation);
        final end = _chunkEnd(offset, batchSize, input.length);
        final request = $graph.DeleteEdgesRequest(
          edges: input
              .sublist(offset, end)
              .map((edge) => $graph.EdgeKey(tail: edge.tail, head: edge.head)),
        );
        final response = await _invoke(
          'DeleteEdges',
          callOptions,
          (raw, headers, signal, onHeader, onTrailer) => raw.deleteEdges(
            request,
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
        );
        deleted += response.deleted;
      } on Exception catch (error) {
        _throwBatchOrCause(deleted, error);
      }
    }
    return deleted;
  }

  Future<T> _invoke<T>(
    String method,
    LanternCallOptions? options,
    Future<T> Function(
      $client.LanternServiceClient raw,
      connect.Headers headers,
      connect.AbortSignal signal,
      void Function(connect.Headers) onHeader,
      void Function(connect.Headers) onTrailer,
    )
    call, {
    bool additiveSafe = false,
  }) {
    return _runWithRetry(
      method: method,
      additiveSafe: additiveSafe,
      options: options,
      attempt: () {
        final raw = $client.LanternServiceClient(_invoker.transport);
        return _invoker.invokeUnary(
          options: options,
          call:
              ({
                required headers,
                required signal,
                required onHeader,
                required onTrailer,
              }) => call(raw, headers, signal, onHeader, onTrailer),
        );
      },
    );
  }
}

int _chunkEnd(int offset, int batchSize, int length) {
  final end = offset + batchSize;
  return end < length ? end : length;
}

void _validateBatch(int length, int batchSize) {
  if (batchSize <= 0 || batchSize > LanternCrud.maxBatchSize) {
    throw _invalidArgumentException(
      'batchSize must be in [1, ${LanternCrud.maxBatchSize}]',
    );
  }
  if (length > LanternCrud.maxBatchSize) {
    throw _invalidArgumentException(
      'logical batch exceeds ${LanternCrud.maxBatchSize} items',
    );
  }
}

List<DateTime?> _resolveExpirations<T>(List<T> inputs, DateTime sampledNow) {
  return List<DateTime?>.generate(inputs.length, (index) {
    final input = inputs[index];
    final (expiresIn, expiresAt) = switch (input) {
      VertexInput(:final expiresIn, :final expiresAt) => (expiresIn, expiresAt),
      EdgeInput(:final expiresIn, :final expiresAt) => (expiresIn, expiresAt),
      _ => throw StateError('unsupported expiration input'),
    };
    if (expiresIn != null && expiresAt != null) {
      throw _invalidArgumentException(
        'expiresIn and expiresAt are mutually exclusive',
      );
    }
    if (expiresIn != null) {
      if (expiresIn <= Duration.zero) {
        throw _invalidArgumentException('expiresIn must be positive');
      }
      try {
        return _normalizeTimestamp(sampledNow.toUtc().add(expiresIn));
      } on LanternException {
        rethrow;
      } catch (_) {
        throw _invalidArgumentException('expiresIn exceeds timestamp range');
      }
    }
    return expiresAt == null ? null : _normalizeTimestamp(expiresAt);
  }, growable: false);
}

List<bool> _initialLiveness(List<DateTime?> expirations, DateTime sampledAt) =>
    List<bool>.generate(
      expirations.length,
      (index) =>
          expirations[index] == null || expirations[index]!.isAfter(sampledAt),
      growable: false,
    );

List<int>? _validatedContribId(EdgeInput edge) {
  final value = edge._contribId;
  if (value == null) return null;
  if (value.length != 24) {
    throw _invalidArgumentException('contribId must be exactly 24 bytes');
  }
  if (!value.any((byte) => byte != 0)) {
    throw _invalidArgumentException('contribId must not be all zero');
  }
  return List<int>.from(value);
}

Never _throwBatchOrCause(int committed, Exception cause) {
  if (committed == 0) throw cause;
  throw BatchException(committed: committed, cause: cause);
}

void _throwIfCanceled(LanternCancellationToken? cancellation) {
  if (cancellation?.isCanceled ?? false) {
    throw LanternCanceledException._(
      _ErrorData(
        transportCode: connect.Code.canceled.value,
        transportCodeName: connect.Code.canceled.name,
        message: 'operation canceled',
        cause: cancellation?._reason,
        headers: const {},
        trailers: const {},
        metadata: const {},
      ),
    );
  }
}

LanternException _notFoundException(String message) =>
    LanternNotFoundException._(
      _ErrorData(
        transportCode: connect.Code.notFound.value,
        transportCodeName: connect.Code.notFound.name,
        message: message,
        headers: const {},
        trailers: const {},
        metadata: const {},
      ),
    );
