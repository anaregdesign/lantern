import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';

/// Typed classification of one remote attempt for replay policy.
enum OfflineRemoteErrorKind {
  /// A temporary transport or endpoint failure.
  unavailable,

  /// Credentials must be refreshed before another attempt.
  unauthenticated,

  /// The mutation is invalid and must not be retried.
  invalidArgument,

  /// The caller canceled the attempt.
  canceled,

  /// A server resource limit rejected this attempt.
  resourceExhausted,

  /// A permanent server-side failure rejected the intent.
  permanent,

  /// An unmapped failure that remains retryable within policy bounds.
  unknown,
}

/// A typed remote failure that retains, but never serializes, its cause.
final class OfflineRemoteFailure implements Exception {
  /// Creates a classified remote failure.
  const OfflineRemoteFailure(this.kind, this.cause);

  /// Bounded replay-policy classification.
  final OfflineRemoteErrorKind kind;

  /// Original in-memory typed transport cause.
  final Object cause;
}

/// Exact remote read result.
sealed class OfflineRemoteRead<T> {
  /// Creates a remote read result.
  const OfflineRemoteRead();
}

/// A present exact remote entity.
final class OfflineRemotePresent<T> extends OfflineRemoteRead<T> {
  /// Creates a present remote read.
  const OfflineRemotePresent(this.value);

  /// Exact remote entity.
  final T value;
}

/// A confirmed remote absence.
final class OfflineRemoteMissing<T> extends OfflineRemoteRead<T> {
  /// Creates a confirmed remote absence.
  const OfflineRemoteMissing();
}

/// Network port used by [OfflineLanternRepository].
///
/// Implementations must acquire credentials at send time and honor cancellation.
/// They must return exact values and expirations.
abstract interface class OfflineRemote {
  /// Probes the real Lantern health surface without implying mutation delivery.
  Future<void> probe({LanternCancellationToken? cancellation});

  /// Gets one exact vertex or a confirmed absence.
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  });

  /// Gets one exact edge or a confirmed absence.
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  });

  /// Writes one already expiration-resolved vertex.
  Future<void> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  });

  /// Writes one already expiration-resolved edge.
  Future<void> putEdge(Edge edge, {LanternCancellationToken? cancellation});
}

/// [OfflineRemote] adapter over the official [LanternClient].
///
/// The wrapped client invokes its configured [TokenProvider] at send time. The
/// adapter disables the client's nested retry policy so each method is exactly
/// one wire attempt; [OfflineLanternRepository] owns durable retry accounting.
/// The adapter neither reads nor persists credentials.
final class LanternClientOfflineRemote implements OfflineRemote {
  /// Wraps one online Lantern client.
  const LanternClientOfflineRemote(this.client);

  /// Underlying online client.
  final LanternClient client;

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) async {
    try {
      await client.ping(
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw _mapFailure(error);
    }
  }

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return OfflineRemotePresent<Edge>(
        await client.getEdge(
          edge,
          options: LanternCallOptions(cancellation: cancellation, retry: false),
        ),
      );
    } on LanternNotFoundException {
      return const OfflineRemoteMissing<Edge>();
    } catch (error) {
      throw _mapFailure(error);
    }
  }

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return OfflineRemotePresent<Vertex>(
        await client.getVertex(
          key,
          options: LanternCallOptions(cancellation: cancellation, retry: false),
        ),
      );
    } on LanternNotFoundException {
      return const OfflineRemoteMissing<Vertex>();
    } catch (error) {
      throw _mapFailure(error);
    }
  }

  @override
  Future<void> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      await client.putEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: edge.weight,
          expiresAt: edge.expiration,
        ),
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw _mapFailure(error);
    }
  }

  @override
  Future<void> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      await client.putVertex(
        VertexInput(
          key: vertex.key,
          value: vertex.value,
          expiresAt: vertex.expiration,
        ),
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw _mapFailure(error);
    }
  }
}

Exception _mapFailure(Object error) {
  if (error is OfflineRemoteFailure) return error;
  if (error is OfflineCanceledException) return error;
  final classified = error is LanternRetryExhaustedException
      ? error.cause
      : error;
  if (classified is LanternCanceledException) {
    return const OfflineCanceledException();
  }
  return OfflineRemoteFailure(switch (classified) {
    LanternUnavailableException() => OfflineRemoteErrorKind.unavailable,
    LanternUnauthenticatedException() => OfflineRemoteErrorKind.unauthenticated,
    LanternInvalidArgumentException() => OfflineRemoteErrorKind.invalidArgument,
    LanternResourceExhaustedException() =>
      OfflineRemoteErrorKind.resourceExhausted,
    LanternPermissionDeniedException() ||
    LanternFailedPreconditionException() => OfflineRemoteErrorKind.permanent,
    _ => OfflineRemoteErrorKind.unknown,
  }, error);
}
