part of 'client.dart';

extension _DirectWatch on DirectLanternDataClient {
  Stream<LanternSnapshot<Vertex>> _directWatchVertex(
    String key, {
    WatchOptions? options,
  }) => _directWatchVertices([
    key,
  ], options: options).map((values) => values.single);
  Stream<List<LanternSnapshot<Vertex>>> _directWatchVertices(
    Iterable<String> keys, {
    WatchOptions? options,
  }) {
    final policy = _watchOptions(options), input = _boundedInputs(keys);
    for (final key in input) {
      _requireEntityKey(key);
    }
    final query = jsonEncode([
      'vertices',
      input,
      policy.source.name,
      policy.pollInterval.inMicroseconds,
    ]);
    return _observations.observe(
      query,
      initial: (lost) => List.unmodifiable(
        input.map(
          (key) => _snapshot<Vertex>(
            null,
            ConfirmedState.unknown,
            jsonEncode(['v', key]),
            cause: lost
                ? ObservationCause.observationLost
                : ObservationCause.noCache,
            lost: lost,
            observationMode: ObservationMode.pollingObservation,
          ),
        ),
      ),
      load: (cancel) => getVertices(
        input,
        options: GetOptions(source: policy.source, cancellation: cancel),
      ),
      markLost: (values, lost) => List.unmodifiable(
        values.map((value) => _markSnapshotLost(value, lost)),
      ),
      pauseAfter: (values) =>
          values.any((s) => _authorityFailure(s.confirmed.cause)),
      interval: policy.source == ReadSource.cacheOnly || input.isEmpty
          ? null
          : policy.pollInterval,
    );
  }

  Stream<LanternSnapshot<Edge>> _directWatchEdge(
    EdgeRef edge, {
    WatchOptions? options,
  }) => _directWatchEdges([
    edge,
  ], options: options).map((values) => values.single);
  Stream<List<LanternSnapshot<Edge>>> _directWatchEdges(
    Iterable<EdgeRef> edges, {
    WatchOptions? options,
  }) {
    final policy = _watchOptions(options), input = _boundedInputs(edges);
    for (final edge in input) {
      _requireEntityKey(edge.tail);
      _requireEntityKey(edge.head);
    }
    final query = jsonEncode([
      'edges',
      input.map((e) => [e.tail, e.head]).toList(),
      policy.source.name,
      policy.pollInterval.inMicroseconds,
    ]);
    return _observations.observe(
      query,
      initial: (lost) => List.unmodifiable(
        input.map(
          (edge) => _snapshot<Edge>(
            null,
            ConfirmedState.unknown,
            jsonEncode(['e', edge.tail, edge.head]),
            cause: lost
                ? ObservationCause.observationLost
                : ObservationCause.noCache,
            lost: lost,
            observationMode: ObservationMode.pollingObservation,
          ),
        ),
      ),
      load: (cancel) => getEdges(
        input,
        options: GetOptions(source: policy.source, cancellation: cancel),
      ),
      markLost: (values, lost) => List.unmodifiable(
        values.map((value) => _markSnapshotLost(value, lost)),
      ),
      pauseAfter: (values) =>
          values.any((s) => _authorityFailure(s.confirmed.cause)),
      interval: policy.source == ReadSource.cacheOnly || input.isEmpty
          ? null
          : policy.pollInterval,
    );
  }

  Stream<LanternTraversalSnapshot> _directWatchIlluminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    WatchOptions? options,
  }) {
    final policy = _watchOptions(options);
    final query = LanternTraversalQuery(
      seed,
      traversal: traversal,
      weighting: weighting,
      vertexPrefix: vertexPrefix,
    );
    final key = jsonEncode([
      'traversal',
      query.canonicalKey,
      policy.source.name,
      policy.pollInterval.inMicroseconds,
    ]);
    return _observations.observe(
      key,
      traversal: true,
      initial: (lost) => _traversalUnknown(
        lost ? ObservationCause.observationLost : ObservationCause.noCache,
        lost: lost,
        query: query,
        observationMode: ObservationMode.pollingObservation,
      ),
      load: (cancel) => illuminate(
        seed,
        traversal: traversal,
        weighting: weighting,
        vertexPrefix: vertexPrefix,
        options: TraversalReadOptions(
          source: policy.source,
          cancellation: cancel,
        ),
      ),
      markLost: (value, lost) => LanternTraversalSnapshot(
        query: value.query,
        observationMode: ObservationMode.pollingObservation,
        confirmed: value.confirmed,
        pending: value.pending,
        sequence: value.sequence,
        observationLost: lost,
      ),
      pauseAfter: (value) => _authorityFailure(value.confirmed.cause),
      interval: policy.source == ReadSource.cacheOnly
          ? null
          : policy.pollInterval,
    );
  }
}
