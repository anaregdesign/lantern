part of 'client.dart';

/// Direction for traversal weight-sensitive optimization.
enum TraversalObjective {
  /// Omit the choice; the server currently defaults to maximize.
  serverDefault,

  /// Treat weights as costs and prefer smaller values.
  minimize,

  /// Treat weights as relevance and prefer larger values.
  maximize,
}

/// Optional post-traversal tree view.
enum TraversalReduction {
  /// Return the full discovered graph.
  none,

  /// Return a rooted directed spanning-tree view.
  minimumSpanningTree,

  /// Return a rooted shortest-path-tree view.
  shortestPathTree,
}

/// Shared edge-weight transform applied before traversal.
enum TraversalWeighting {
  /// Omit the choice; the server currently defaults to raw.
  serverDefault,

  /// Use stored weights verbatim.
  raw,

  /// Apply the TF-IDF hub suppressor.
  tfidf,

  /// Apply Okapi BM25 edge weighting.
  bm25,
}

/// Exactly one typed Illuminate family.
sealed class TraversalOptions {
  const TraversalOptions();
}

/// Greedy, per-hop top-k BFS options.
final class BfsOptions extends TraversalOptions {
  /// Creates BFS options. [step] and [fanOut] must be positive.
  const BfsOptions({
    required this.step,
    required this.fanOut,
    this.objective = TraversalObjective.serverDefault,
    this.reduction = TraversalReduction.none,
  });

  /// BFS depth; must be positive.
  final int step;

  /// Per-hop top-k ceiling; must be positive.
  final int fanOut;

  /// Direction used by pruning and reduction.
  final TraversalObjective objective;

  /// Optional tree view.
  final TraversalReduction reduction;
}

/// Personalized PageRank options.
final class PprOptions extends TraversalOptions {
  /// Creates PPR options. A zero [topN] retains every positive-mass vertex.
  const PprOptions({this.topN = 0, this.restartProbability, this.epsilon});

  /// Result cap; zero retains all positive mass.
  final int topN;

  /// Optional teleport probability in `(0, 1)`.
  final double? restartProbability;

  /// Optional positive forward-push threshold.
  final double? epsilon;
}

/// PageRank-Nibble local-community options.
final class LocalCommunityOptions extends TraversalOptions {
  /// Creates community options. A zero [maxSize] leaves the sweep unbounded.
  const LocalCommunityOptions({
    this.maxSize = 0,
    this.restartProbability,
    this.epsilon,
    this.objective = TraversalObjective.serverDefault,
    this.reduction = TraversalReduction.none,
  });

  /// Community upper bound; zero leaves the sweep unbounded.
  final int maxSize;

  /// Optional teleport probability in `(0, 1)`.
  final double? restartProbability;

  /// Optional positive forward-push threshold.
  final double? epsilon;

  /// Cost direction for reduction only.
  final TraversalObjective objective;

  /// Optional tree view of retained community membership.
  final TraversalReduction reduction;
}

/// Immutable Illuminate result that retains complete Edge values and TTLs.
final class Graph {
  /// Creates a deterministic graph, with duplicate keys/pairs resolved last.
  Graph({required Iterable<Vertex> vertices, required Iterable<Edge> edges})
    : vertices = _indexVertices(vertices),
      edges = _indexEdges(edges);

  /// Vertices indexed and iterated by key.
  final Map<String, Vertex> vertices;

  /// Complete edges indexed by tail then head, both in key order.
  final Map<String, Map<String, Edge>> edges;

  /// Deterministic flattened complete Edge sequence.
  List<Edge> get allEdges =>
      List<Edge>.unmodifiable(edges.values.expand((byHead) => byHead.values));

  /// Weight-only compatibility view derived from [edges].
  Map<String, Map<String, double>> get edgeWeights {
    final outer = SplayTreeMap<String, Map<String, double>>();
    for (final entry in edges.entries) {
      outer[entry.key] = Map<String, double>.unmodifiable(
        SplayTreeMap<String, double>.from(
          entry.value.map((head, edge) => MapEntry(head, edge.weight)),
        ),
      );
    }
    return Map<String, Map<String, double>>.unmodifiable(outer);
  }

  /// Complete edge for one ordered pair, when present.
  Edge? edge(String tail, String head) => edges[tail]?[head];

  /// Outgoing edges in deterministic head-key order.
  List<Edge> outgoing(String tail) =>
      List<Edge>.unmodifiable(edges[tail]?.values ?? const <Edge>[]);

  /// Incoming edges in deterministic `(tail, head)` order.
  List<Edge> incoming(String head) => List<Edge>.unmodifiable(
    edges.values.expand((byHead) => [if (byHead[head] case final edge?) edge]),
  );

  /// Unique adjacent vertex keys in deterministic order.
  List<String> adjacentKeys(String key) {
    final adjacent = SplayTreeSet<String>();
    adjacent.addAll(edges[key]?.keys ?? const <String>[]);
    for (final tail in edges.keys) {
      if (edges[tail]!.containsKey(key)) adjacent.add(tail);
    }
    return List<String>.unmodifiable(adjacent);
  }
}

/// Typed traversal operations.
extension LanternTraversal on LanternClient {
  /// Runs exactly one traversal family and retains full Edge expiration.
  Future<Graph> illuminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final request = $graph.IlluminateRequest(
      seed: seed,
      weighting: _weightingToProto(weighting),
      vertexPrefix: vertexPrefix,
    );
    switch (traversal) {
      case BfsOptions(
        :final step,
        :final fanOut,
        :final objective,
        :final reduction,
      ):
        if (step <= 0 || fanOut <= 0) {
          throw _invalidArgumentException(
            'BFS step and fanOut must both be positive',
          );
        }
        _validateUint32(step, 'step');
        _validateUint32(fanOut, 'fanOut');
        request.bfs = $graph.BfsParams(
          step: step,
          fanOut: fanOut,
          objective: _objectiveToProto(objective),
          reduction: _reductionToProto(reduction),
        );
      case PprOptions(:final topN, :final restartProbability, :final epsilon):
        _validateUint32(topN, 'topN');
        _validateProbabilityAndEpsilon(restartProbability, epsilon);
        request.ppr = $graph.PprParams(
          topN: topN,
          restartProb: restartProbability == null
              ? null
              : _normalizeFloat32(restartProbability),
          epsilon: epsilon == null ? null : _normalizeFloat32(epsilon),
        );
      case LocalCommunityOptions(
        :final maxSize,
        :final restartProbability,
        :final epsilon,
        :final objective,
        :final reduction,
      ):
        _validateUint32(maxSize, 'maxSize');
        _validateProbabilityAndEpsilon(restartProbability, epsilon);
        request.community = $graph.LocalCommunityParams(
          maxSize: maxSize,
          restartProb: restartProbability == null
              ? null
              : _normalizeFloat32(restartProbability),
          epsilon: epsilon == null ? null : _normalizeFloat32(epsilon),
          objective: _objectiveToProto(objective),
          reduction: _reductionToProto(reduction),
        );
    }
    final response = await _invoke(
      'Illuminate',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.illuminate(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    if (!response.hasGraph()) {
      throw _internalSdkException('server returned Illuminate without Graph');
    }
    return Graph(
      vertices: response.graph.vertices.map(_vertexFromProto),
      edges: response.graph.edges.map(_edgeFromProto),
    );
  }
}

Map<String, Vertex> _indexVertices(Iterable<Vertex> vertices) {
  final indexed = SplayTreeMap<String, Vertex>();
  for (final vertex in vertices) {
    indexed[vertex.key] = vertex;
  }
  return Map<String, Vertex>.unmodifiable(indexed);
}

Map<String, Map<String, Edge>> _indexEdges(Iterable<Edge> edges) {
  final mutable = SplayTreeMap<String, SplayTreeMap<String, Edge>>();
  for (final edge in edges) {
    (mutable[edge.tail] ??= SplayTreeMap())[edge.head] = edge;
  }
  return Map<String, Map<String, Edge>>.unmodifiable(
    mutable.map(
      (tail, byHead) => MapEntry(tail, Map<String, Edge>.unmodifiable(byHead)),
    ),
  );
}

void _validateProbabilityAndEpsilon(double? probability, double? epsilon) {
  if (probability != null &&
      (!probability.isFinite || probability <= 0 || probability >= 1)) {
    throw _invalidArgumentException(
      'restartProbability must be in the open interval (0, 1)',
    );
  }
  if (epsilon != null && (!epsilon.isFinite || epsilon <= 0)) {
    throw _invalidArgumentException('epsilon must be finite and positive');
  }
}

$graph.Objective _objectiveToProto(TraversalObjective objective) =>
    switch (objective) {
      TraversalObjective.serverDefault =>
        $graph.Objective.OBJECTIVE_UNSPECIFIED,
      TraversalObjective.minimize => $graph.Objective.OBJECTIVE_MINIMIZE,
      TraversalObjective.maximize => $graph.Objective.OBJECTIVE_MAXIMIZE,
    };

$graph.Reduction _reductionToProto(TraversalReduction reduction) =>
    switch (reduction) {
      TraversalReduction.none => $graph.Reduction.REDUCTION_UNSPECIFIED,
      TraversalReduction.minimumSpanningTree =>
        $graph.Reduction.REDUCTION_MINIMUM_SPANNING_TREE,
      TraversalReduction.shortestPathTree =>
        $graph.Reduction.REDUCTION_SHORTEST_PATH_TREE,
    };

$graph.Weighting _weightingToProto(TraversalWeighting weighting) =>
    switch (weighting) {
      TraversalWeighting.serverDefault =>
        $graph.Weighting.WEIGHTING_UNSPECIFIED,
      TraversalWeighting.raw => $graph.Weighting.WEIGHTING_RAW,
      TraversalWeighting.tfidf => $graph.Weighting.WEIGHTING_TFIDF,
      TraversalWeighting.bm25 => $graph.Weighting.WEIGHTING_BM25,
    };
