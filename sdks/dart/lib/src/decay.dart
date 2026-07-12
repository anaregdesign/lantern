part of 'client.dart';

/// Maximum contribution fan-out for one decaying edge helper call.
const int maxDecaySteps = 16;

/// Geometric staircase parameters for [LanternDecay.addDecayingEdge].
final class DecayOptions {
  /// Creates a geometric decay specification.
  const DecayOptions({
    required this.initialWeight,
    required this.ratio,
    required this.steps,
    required this.interval,
  });

  /// Live contribution at time zero; finite and non-zero.
  final double initialWeight;

  /// Per-step multiplier in the open interval `(0, 1)`.
  final double ratio;

  /// Number of steps in `[1, maxDecaySteps]`.
  final int steps;

  /// Positive wall-clock duration of one step.
  final Duration interval;
}

/// Creates [DecayOptions] from a half-life and sampling horizon.
DecayOptions halfLifeDecay({
  required double initialWeight,
  required Duration halfLife,
  required Duration interval,
  required Duration horizon,
}) {
  if (halfLife <= Duration.zero ||
      interval <= Duration.zero ||
      horizon <= Duration.zero) {
    throw _invalidArgumentException(
      'halfLife, interval, and horizon must be positive',
    );
  }
  final ratio = pow(
    0.5,
    interval.inMicroseconds / halfLife.inMicroseconds,
  ).toDouble();
  var steps = (horizon.inMicroseconds / interval.inMicroseconds).ceil();
  if (steps < 1) steps = 1;
  if (steps > maxDecaySteps) steps = maxDecaySteps;
  return DecayOptions(
    initialWeight: initialWeight,
    ratio: ratio,
    steps: steps,
    interval: interval,
  );
}

/// Expands a target geometric curve into staggered-TTL additive writes.
///
/// For `initialWeight=16`, `ratio=.5`, and five steps, contribution weights
/// are `8, 4, 2, 1, 1`; their live sum starts at exactly 16 and telescopes to
/// zero as each absolute expiration passes.
List<EdgeInput> decayContributions({
  required String tail,
  required String head,
  required DecayOptions options,
  required DateTime base,
}) {
  _validateDecay(options);
  final output = <EdgeInput>[];
  for (var step = 1; step <= options.steps; step++) {
    final exponent = step - 1;
    final contribution = step < options.steps
        ? options.initialWeight *
              pow(options.ratio, exponent) *
              (1 - options.ratio)
        : options.initialWeight * pow(options.ratio, exponent);
    final weight = _normalizeFloat32(contribution.toDouble());
    if (weight == 0) continue;
    DateTime expiration;
    try {
      expiration = _normalizeTimestamp(
        base.toUtc().add(options.interval * step),
      );
    } on LanternException {
      rethrow;
    } catch (_) {
      throw _invalidArgumentException('decay horizon exceeds timestamp range');
    }
    output.add(
      EdgeInput(tail: tail, head: head, weight: weight, expiresAt: expiration),
    );
  }
  if (output.isEmpty) {
    throw _invalidArgumentException('decay curve underflows float32 to zero');
  }
  return List.unmodifiable(output);
}

/// Decaying-edge conveniences for [LanternClient].
extension LanternDecay on LanternClient {
  /// Adds one geometrically decaying edge and returns its effective live sum.
  Future<double> addDecayingEdge({
    required String tail,
    required String head,
    required DecayOptions options,
    LanternCallOptions? callOptions,
  }) async {
    final inputs = decayContributions(
      tail: tail,
      head: head,
      options: options,
      base: _clock(),
    );
    final result = await addEdges(inputs, options: callOptions);
    return result.effectiveWeights.isEmpty ? 0 : result.effectiveWeights.last;
  }
}

void _validateDecay(DecayOptions options) {
  if (!options.initialWeight.isFinite || options.initialWeight == 0) {
    throw _invalidArgumentException(
      'initialWeight must be finite and non-zero',
    );
  }
  if (!options.ratio.isFinite || options.ratio <= 0 || options.ratio >= 1) {
    throw _invalidArgumentException(
      'ratio must be in the open interval (0, 1)',
    );
  }
  if (options.steps < 1 || options.steps > maxDecaySteps) {
    throw _invalidArgumentException('steps must be in [1, $maxDecaySteps]');
  }
  if (options.interval <= Duration.zero) {
    throw _invalidArgumentException('interval must be positive');
  }
}
