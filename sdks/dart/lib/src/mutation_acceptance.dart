part of 'client.dart';

/// The complete logical mutation was handled with its effects undisclosed.
///
/// This is an acknowledgement, not a transport failure, permission rejection,
/// confirmation of a change, or authorization to retry. Primitive facades
/// use this signal; [captureMutationReply] exposes an explicit reply instead.
final class MutationAcceptance implements Exception {
  /// Creates the effect-free acknowledgement.
  const MutationAcceptance();

  @override
  String toString() => 'mutation handled; effect undisclosed';
}

bool _mutationAcceptedUndisclosed(
  $protobuf.GeneratedMessage response,
  $graph.MutationAcceptance? acceptance,
) {
  if (acceptance == null) return false;
  if (acceptance.kind !=
          $graph
              .MutationAcceptanceKind
              .MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED ||
      response.unknownFields.isNotEmpty ||
      acceptance.unknownFields.isNotEmpty) {
    throw _internalSdkException('unknown mutation acceptance');
  }
  for (final field in response.info_.fieldInfo.values) {
    if (field.name != 'acceptance' && response.hasField(field.tagNumber)) {
      throw _internalSdkException(
        'mutation acceptance carried detailed effects',
      );
    }
  }
  return true;
}
