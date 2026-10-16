import 'package:lantern_client/lantern_client.dart';

/// The existing 14 configurations × four weightings, shared by wire profiles.
/// Test support only: the parent runtime never imports an Offline dependency.
List<({TraversalOptions traversal, TraversalWeighting weighting})>
traversalWireMatrix() => [
  for (final traversal in <TraversalOptions>[
    for (final objective in [
      TraversalObjective.minimize,
      TraversalObjective.maximize,
    ])
      for (final reduction in TraversalReduction.values)
        BfsOptions(
          step: 2,
          fanOut: 3,
          objective: objective,
          reduction: reduction,
        ),
    const PprOptions(),
    const PprOptions(topN: 2, restartProbability: 0.25, epsilon: 1e-4),
    for (final objective in [
      TraversalObjective.minimize,
      TraversalObjective.maximize,
    ])
      for (final reduction in TraversalReduction.values)
        LocalCommunityOptions(
          maxSize: 3,
          objective: objective,
          reduction: reduction,
        ),
  ])
    for (final weighting in TraversalWeighting.values)
      (traversal: traversal, weighting: weighting),
];
