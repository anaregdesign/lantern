import 'package:lantern_client/lantern_client.dart';

import 'change_store.dart';
import 'errors.dart';
import 'types.dart';

/// Typed classification of one remote attempt for replay policy.
enum OfflineRemoteErrorKind {
  /// A temporary transport or endpoint failure.
  unavailable,

  /// The attempt crossed its caller or client deadline.
  deadlineExceeded,

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

  /// Writes one already expiration-resolved vertex and returns the
  /// server-authoritative application outcome.
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  });

  /// Writes one already expiration-resolved edge and returns the
  /// server-authoritative application outcome.
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  });
}

/// Optional bounded plural-read port for resident-only CDC recovery.
///
/// Results must align exactly with request indexes. Callers cap each family at
/// [offlineMaxResidentRevalidationBatch] and reject incomplete responses.
abstract interface class OfflineRecoveryRemote {
  /// Revalidates exact resident Vertex keys in one bounded plural call.
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  });

  /// Revalidates exact resident Edge identities in one bounded plural call.
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  });
}

/// [OfflineRemote] adapter over the official [LanternClient].
///
/// The wrapped client invokes its configured [TokenProvider] at send time. The
/// adapter disables the client's nested retry policy. Each singular adapter
/// method issues at most one RPC attempt; [OfflineLanternRepository] owns
/// durable retry accounting.
/// The adapter neither reads nor persists credentials.
final class LanternClientOfflineRemote
    implements OfflineRemote, OfflineRecoveryRemote {
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
      throw mapLanternClientFailure(error);
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
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) async {
    if (edges.isEmpty || edges.length > offlineMaxResidentRevalidationBatch) {
      throw const OfflineArgumentException();
    }
    try {
      final result = await client.getEdges(
        edges,
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
      final present = {
        for (final edge in result.edges)
          OfflineEntityKey.edge(edge.tail, edge.head).canonical: edge,
      };
      final missing = {
        for (final edge in result.missing)
          OfflineEntityKey.edge(edge.tail, edge.head).canonical,
      };
      if (present.length + missing.length != edges.length) {
        throw const OfflineCodecException();
      }
      return List<OfflineRemoteRead<Edge>>.unmodifiable(
        edges.map((edge) {
          final key = OfflineEntityKey.edge(edge.tail, edge.head).canonical;
          final value = present[key];
          if (value != null) return OfflineRemotePresent<Edge>(value);
          if (missing.contains(key)) return const OfflineRemoteMissing<Edge>();
          throw const OfflineCodecException();
        }),
      );
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
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
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) async {
    if (keys.isEmpty || keys.length > offlineMaxResidentRevalidationBatch) {
      throw const OfflineArgumentException();
    }
    try {
      final result = await client.getVertices(
        keys,
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
      final present = {
        for (final vertex in result.vertices) vertex.key: vertex,
      };
      final missing = result.missing.toSet();
      if (present.length + missing.length != keys.length) {
        throw const OfflineCodecException();
      }
      return List<OfflineRemoteRead<Vertex>>.unmodifiable(
        keys.map((key) {
          final value = present[key];
          if (value != null) return OfflineRemotePresent<Vertex>(value);
          if (missing.contains(key)) {
            return const OfflineRemoteMissing<Vertex>();
          }
          throw const OfflineCodecException();
        }),
      );
    } on OfflineException {
      rethrow;
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return await client.putEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: edge.weight,
          expiresAt: edge.expiration,
        ),
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    try {
      return await client.putVertex(
        VertexInput(
          key: vertex.key,
          value: vertex.value,
          expiresAt: vertex.expiration,
        ),
        options: LanternCallOptions(cancellation: cancellation, retry: false),
      );
    } catch (error) {
      throw mapLanternClientFailure(error);
    }
  }
}

/// Maps one official online-client failure into the offline replay taxonomy.
///
/// Custom [OfflineRemote] adapters can use this function to preserve the same
/// retry and terminal-failure policy as [LanternClientOfflineRemote]. A bounded
/// online retry wrapper is classified by its final typed cause while the full
/// wrapper remains available as [OfflineRemoteFailure.cause].
Exception mapLanternClientFailure(Object error) {
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
    LanternDeadlineExceededException() =>
      OfflineRemoteErrorKind.deadlineExceeded,
    LanternUnauthenticatedException() => OfflineRemoteErrorKind.unauthenticated,
    LanternInvalidArgumentException() => OfflineRemoteErrorKind.invalidArgument,
    LanternResourceExhaustedException() =>
      OfflineRemoteErrorKind.resourceExhausted,
    LanternPermissionDeniedException() ||
    LanternFailedPreconditionException() => OfflineRemoteErrorKind.permanent,
    _ => OfflineRemoteErrorKind.unknown,
  }, error);
}
