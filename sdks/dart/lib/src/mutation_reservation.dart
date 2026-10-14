part of 'client.dart';

/// Rejects payload amplification before protobuf/byte copies. UTF-8 wire size is
/// never less than these UTF-16 lengths; exact bytes are checked after freeze.
int _minimumMutationReservation(MutationRequest<Object?> request, int budget) {
  var bytes = 256 + request.inputs.length * 128;
  if (bytes > budget) throw const LanternDataException(DataFailure.capacity);
  for (final input in request.inputs) {
    bytes += switch (input) {
      VertexInput(:final key, :final value) =>
        key.length +
            switch (value) {
              BytesValue() => value._value.length,
              StringValue() => value.value.length,
              _ => 0,
            },
      EdgeInput(:final tail, :final head) => tail.length + head.length + 24,
      String key => key.length,
      EdgeRef(:final tail, :final head) => tail.length + head.length,
      EdgeContributionRef(:final tail, :final head) =>
        tail.length + head.length + 24,
      _ => throw _invalidArgumentException('invalid mutation input type'),
    };
    if (bytes > budget) throw const LanternDataException(DataFailure.capacity);
  }
  return bytes;
}

/// Retained status is content-free and bounded; preserve typed codes, not raw
/// transport headers, messages, or arbitrary provider exception payloads.
Exception _boundedMutationFailure(Exception cause) {
  if (cause is LanternDataException) return cause;
  if (cause is! LanternException)
    return _internalSdkException('mutation failed');
  final data = _ErrorData(
    transportCode: cause.transportCode,
    transportCodeName: cause.transportCodeName,
    message: 'mutation failed',
    headers: const {},
    trailers: const {},
    metadata: const {},
  );
  return switch (cause.code) {
    LanternCode.canceled => LanternCanceledException._(data),
    LanternCode.invalidArgument => LanternInvalidArgumentException._(data),
    LanternCode.deadlineExceeded => LanternDeadlineExceededException._(data),
    LanternCode.notFound => LanternNotFoundException._(data),
    LanternCode.permissionDenied => LanternPermissionDeniedException._(data),
    LanternCode.resourceExhausted => LanternResourceExhaustedException._(data),
    LanternCode.aborted => LanternAbortedException._(data),
    LanternCode.failedPrecondition => LanternFailedPreconditionException._(
      data,
    ),
    LanternCode.unauthenticated => LanternUnauthenticatedException._(data),
    LanternCode.unavailable => LanternUnavailableException._(data),
    LanternCode.internal => LanternInternalException._(data),
  };
}
