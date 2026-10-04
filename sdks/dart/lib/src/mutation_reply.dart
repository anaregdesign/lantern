part of 'client.dart';

/// A disclosed mutation effect or an explicit acknowledgement without effects.
sealed class MutationReply<T> {
  const MutationReply._();
}

/// The mutation's effect was disclosed by the Server.
final class MutationKnownEffect<T> extends MutationReply<T> {
  /// Creates a disclosed effect.
  const MutationKnownEffect(this.effect) : super._();

  /// The authoritative disclosed effect.
  final T effect;
}

/// The complete request was handled, without asserting any effect.
final class MutationAcceptedUndisclosed<T> extends MutationReply<T> {
  /// Creates an effect-free acknowledgement.
  const MutationAcceptedUndisclosed() : super._();
}

/// Adapts a primitive SDK facade without fabricating a bool, weight or count.
///
/// A real failure or partial batch remains a failure requiring reconciliation.
Future<MutationReply<T>> captureMutationReply<T>(
  Future<T> Function() call,
) async {
  try {
    return MutationKnownEffect(await call());
  } on MutationAcceptance {
    return MutationAcceptedUndisclosed<T>();
  }
}
