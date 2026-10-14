part of 'client.dart';

/// Parent-internal frozen attempt shared by direct and qualified adapters.
/// No transport retry is nested inside an indexed attempt.
final class _FrozenMutation<T> {
  _FrozenMutation(this.kind, this.wire, this.expirations, this.identities)
    : bytes = wire.fold(0, (n, item) => n + item.writeToBuffer().length + 128);
  final MutationKind kind;
  final List<$protobuf.GeneratedMessage> wire;
  final List<DateTime?> expirations;
  final List<String> identities;
  int get length => wire.length;
  final int bytes;
}

_FrozenMutation<T> _freezeMutation<T>(
  MutationRequest<T> request,
  DateTime now,
  _ContribIdGenerator ids,
) {
  final input = (request as _MutationRequest<T>).inputs;
  final wire = <$protobuf.GeneratedMessage>[];
  final identities = <String>[];
  final expiration = <DateTime?>[];
  switch (request.kind) {
    case MutationKind.putVertices:
    case MutationKind.putVerticesIfAbsent:
      final values = input.cast<VertexInput>();
      expiration.addAll(_resolveExpirations(values, now));
      for (var i = 0; i < values.length; i++) {
        _requireEntityKey(values[i].key);
        wire.add(_vertexInputToProto(values[i], expiration[i])..freeze());
        identities.add(jsonEncode(['v', values[i].key]));
      }
    case MutationKind.putEdges:
    case MutationKind.addEdges:
      final values = input.cast<EdgeInput>();
      expiration.addAll(_resolveExpirations(values, now));
      final contributions = request.kind == MutationKind.addEdges
          ? ids.fillMissing(
              values.map(_validatedContribId).toList(growable: false),
            )
          : const <List<int>?>[];
      for (var i = 0; i < values.length; i++) {
        final value = values[i];
        _requireEntityKey(value.tail);
        _requireEntityKey(value.head);
        final edge = _edgeInputToProto(value, expiration[i]);
        wire.add(
          request.kind == MutationKind.addEdges
              ? ($graph.AddEdgesRequest(
                  edges: [edge],
                  contribIds: [contributions[i]!],
                )..freeze())
              : (edge..freeze()),
        );
        identities.add(jsonEncode(['e', value.tail, value.head]));
      }
    case MutationKind.deleteVertices:
      for (final key in input.cast<String>()) {
        _requireEntityKey(key);
        wire.add($graph.Vertex(key: key)..freeze());
        identities.add(jsonEncode(['v', key]));
      }
    case MutationKind.deleteEdges:
      for (final edge in input.cast<EdgeRef>()) {
        _requireEntityKey(edge.tail);
        _requireEntityKey(edge.head);
        wire.add($graph.EdgeKey(tail: edge.tail, head: edge.head)..freeze());
        identities.add(jsonEncode(['e', edge.tail, edge.head]));
      }
    case MutationKind.deleteEdgeContributions:
      for (final key in _edgeContributionKeys(
        input.cast<EdgeContributionRef>(),
      )) {
        wire.add(key..freeze());
        identities.add(jsonEncode(['e', key.tail, key.head]));
      }
  }
  if (request.kind != MutationKind.addEdges &&
      expiration.any((instant) => instant != null && !instant.isAfter(now))) {
    throw _invalidArgumentException('Put expiration must be after admission');
  }
  return _FrozenMutation(
    request.kind,
    List.unmodifiable(wire),
    List.unmodifiable(expiration),
    List.unmodifiable(identities),
  );
}

void _requireEntityKey(String key) {
  if (key.isEmpty) {
    throw _invalidArgumentException('entity key must not be empty');
  }
}

extension _IndexedMutationAttempts on LanternClient {
  Future<List<MutationReply<T>>> _attemptIndexed<T>(
    _FrozenMutation<T> work,
    int start,
    int end,
    LanternCallOptions options,
    void Function() dispatched,
  ) async {
    _ensureOpen();
    final chunk = work.wire.sublist(start, end);
    final raw = $client.LanternServiceClient(_invoker.transport);
    final response = await _invoker.invokeUnary<$protobuf.GeneratedMessage>(
      options: options,
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) {
            dispatched();
            return switch (work.kind) {
              MutationKind.putVertices ||
              MutationKind.putVerticesIfAbsent => raw.putVertices(
                $graph.PutVerticesRequest(
                  vertices: chunk.cast<$graph.Vertex>(),
                  ifAbsent: work.kind == MutationKind.putVerticesIfAbsent,
                ),
                headers: headers,
                signal: signal,
                onHeader: onHeader,
                onTrailer: onTrailer,
              ),
              MutationKind.putEdges => raw.putEdges(
                $graph.PutEdgesRequest(edges: chunk.cast<$graph.Edge>()),
                headers: headers,
                signal: signal,
                onHeader: onHeader,
                onTrailer: onTrailer,
              ),
              MutationKind.addEdges => raw.addEdges(
                $graph.AddEdgesRequest(
                  edges: chunk.cast<$graph.AddEdgesRequest>().expand(
                    (r) => r.edges,
                  ),
                  contribIds: chunk.cast<$graph.AddEdgesRequest>().expand(
                    (r) => r.contribIds,
                  ),
                ),
                headers: headers,
                signal: signal,
                onHeader: onHeader,
                onTrailer: onTrailer,
              ),
              MutationKind.deleteVertices => raw.deleteVertices(
                $graph.DeleteVerticesRequest(
                  keys: chunk.cast<$graph.Vertex>().map((v) => v.key),
                ),
                headers: headers,
                signal: signal,
                onHeader: onHeader,
                onTrailer: onTrailer,
              ),
              MutationKind.deleteEdges => raw.deleteEdges(
                $graph.DeleteEdgesRequest(edges: chunk.cast<$graph.EdgeKey>()),
                headers: headers,
                signal: signal,
                onHeader: onHeader,
                onTrailer: onTrailer,
              ),
              MutationKind.deleteEdgeContributions =>
                raw.deleteEdgeContributions(
                  $graph.DeleteEdgeContributionsRequest(
                    contributions: chunk.cast<$graph.EdgeContributionKey>(),
                  ),
                  headers: headers,
                  signal: signal,
                  onHeader: onHeader,
                  onTrailer: onTrailer,
                ),
            };
          },
    );
    final count = end - start;
    final acceptance = switch (response) {
      $graph.PutEdgesResponse r => r.hasAcceptance() ? r.acceptance : null,
      $graph.AddEdgesResponse r => r.hasAcceptance() ? r.acceptance : null,
      $graph.DeleteEdgesResponse r => r.hasAcceptance() ? r.acceptance : null,
      $graph.DeleteEdgeContributionsResponse r =>
        r.hasAcceptance() ? r.acceptance : null,
      _ => null,
    };
    if (_mutationAcceptedUndisclosed(response, acceptance)) {
      return List.generate(count, (_) => MutationAcceptedUndisclosed<T>());
    }
    final List<Object> effects = switch (response) {
      $graph.PutVerticesResponse r =>
        r.outcomes.map(_putOutcomeFromProto).toList(),
      $graph.PutEdgesResponse r =>
        r.outcomes.map(_putOutcomeFromProto).toList(),
      $graph.AddEdgesResponse r => r.effectiveWeights.toList(),
      $graph.DeleteVerticesResponse r => _exactDeleted(
        count,
        r.deleted,
        r.existed,
      ),
      $graph.DeleteEdgesResponse r => _exactDeleted(
        count,
        r.deleted,
        r.existed,
      ),
      $graph.DeleteEdgeContributionsResponse r => _exactDeleted(
        count,
        r.deleted,
        r.existed,
      ),
      _ => throw _internalSdkException('invalid indexed mutation response'),
    };
    if (effects.length != count) {
      throw _internalSdkException('misaligned indexed mutation results');
    }
    return [for (final effect in effects) MutationKnownEffect(effect as T)];
  }
}

List<bool> _exactDeleted(int count, int deleted, List<bool> existed) {
  if (existed.length != count || deleted != existed.where((v) => v).length) {
    throw _internalSdkException('misaligned indexed Delete results or count');
  }
  return List.unmodifiable(existed);
}

/// Internal adapter seam for qualified coordinators importing the src library.
/// Generated wire messages remain private. Preparation acquires no token.
final class IndexedMutationIntent<T> {
  IndexedMutationIntent._(this._work);
  final _FrozenMutation<T> _work;

  /// Frozen input length, including duplicate identities.
  int get length => _work.length;

  /// Bounded lifecycle reservation estimate including original wire bytes.
  int get reservedBytes => _work.bytes;

  /// Exact normalized public intent: absolute TTL and fixed Add contribution IDs.
  /// It can be encoded by a qualified store without rebasing or private proto.
  MutationRequest<T> get normalizedRequest => _normalizedMutationRequest(_work);
}

/// A single no-retry attempt failed with explicit dispatch certainty.
final class IndexedMutationAttemptException implements Exception {
  /// Creates an adapter failure without guessing whether a sent request applied.
  const IndexedMutationAttemptException({
    required this.cause,
    required this.mayHaveDispatched,
  });

  /// Original typed failure.
  final Exception cause;

  /// Whether transport dispatch was possible.
  final bool mayHaveDispatched;
}

/// Parent-internal exact primitives; excluded from the application barrel.
extension LanternIndexedMutations on LanternClient {
  /// Freezes typed inputs/relative TTL/contribution IDs before any network call.
  IndexedMutationIntent<T> freezeIndexedMutation<T>(
    MutationRequest<T> request, {
    DateTime? admittedAt,
  }) => IndexedMutationIntent._(
    _freezeMutation(request, admittedAt ?? _clock().toUtc(), _contribIds),
  );

  /// Executes one indexed chunk without retry, TTL rewriting or count reduction.
  Future<MutationBatchReply<T>> attemptIndexedMutation<T>(
    IndexedMutationIntent<T> intent, {
    int start = 0,
    int? count,
    LanternCallOptions? options,
  }) async {
    final end = start + (count ?? intent.length - start);
    if (start < 0 ||
        end < start ||
        end > intent.length ||
        end - start > LanternCrud.maxBatchSize) {
      throw _invalidArgumentException('invalid indexed attempt range');
    }
    if (end == start) return MutationBatchReply<T>(const []);
    var dispatched = false;
    try {
      return MutationBatchReply(
        await _attemptIndexed(
          intent._work,
          start,
          end,
          options ?? LanternCallOptions(retry: false),
          () => dispatched = true,
        ),
      );
    } on Exception catch (cause) {
      throw IndexedMutationAttemptException(
        cause: cause,
        mayHaveDispatched: dispatched,
      );
    }
  }
}

MutationRequest<T> _normalizedMutationRequest<T>(_FrozenMutation<T> work) {
  VertexInput vertex($graph.Vertex wire) {
    final value = _vertexFromProto(wire);
    return VertexInput(
      key: value.key,
      value: value.value,
      expiresAt: value.expiration,
    );
  }

  EdgeInput edge($graph.Edge wire, {Uint8List? id}) {
    final value = _edgeFromProto(wire);
    return EdgeInput(
      tail: value.tail,
      head: value.head,
      weight: value.weight,
      expiresAt: value.expiration,
      contribId: id,
    );
  }

  final MutationRequest<Object?> request = switch (work.kind) {
    MutationKind.putVertices => MutationRequest.putVertices(
      work.wire.cast<$graph.Vertex>().map(vertex),
    ),
    MutationKind.putVerticesIfAbsent => MutationRequest.putVerticesIfAbsent(
      work.wire.cast<$graph.Vertex>().map(vertex),
    ),
    MutationKind.putEdges => MutationRequest.putEdges(
      work.wire.cast<$graph.Edge>().map(edge),
    ),
    MutationKind.addEdges => MutationRequest.addEdges(
      work.wire.cast<$graph.AddEdgesRequest>().map(
        (r) =>
            edge(r.edges.single, id: Uint8List.fromList(r.contribIds.single)),
      ),
    ),
    MutationKind.deleteVertices => MutationRequest.deleteVertices(
      work.wire.cast<$graph.Vertex>().map((v) => v.key),
    ),
    MutationKind.deleteEdges => MutationRequest.deleteEdges(
      work.wire.cast<$graph.EdgeKey>().map((e) => EdgeRef(e.tail, e.head)),
    ),
    MutationKind.deleteEdgeContributions =>
      MutationRequest.deleteEdgeContributions(
        work.wire.cast<$graph.EdgeContributionKey>().map(
          (r) => EdgeContributionRef(
            tail: r.tail,
            head: r.head,
            contribId: Uint8List.fromList(r.contribId),
          ),
        ),
      ),
  };
  return request as MutationRequest<T>;
}
