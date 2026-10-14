part of 'client.dart';

/// Immutable endpoint and verified application/account ownership.
final class LanternSessionBinding {
  /// Explicit binding for application-verified auth or no-auth datasets.
  LanternSessionBinding.application({
    required this.serviceOrigin,
    required this.securityDomain,
    required this.accountIdentity,
    required this.authorityBinding,
    this.userId,
    this.personalPrefix,
    this.expiresAt,
  }) {
    if (serviceOrigin.userInfo.isNotEmpty ||
        serviceOrigin.hasQuery ||
        serviceOrigin.hasFragment ||
        securityDomain.isEmpty ||
        accountIdentity.isEmpty ||
        authorityBinding.isEmpty) {
      throw _invalidArgumentException('invalid explicit session binding');
    }
  }

  /// Uses verified native identity. UID remains metadata in the stable identity.
  factory LanternSessionBinding.native(
    NativeSession session, {
    required String securityDomain,
  }) => LanternSessionBinding.application(
    serviceOrigin: session.serviceOrigin,
    securityDomain: securityDomain,
    accountIdentity: jsonEncode([
      session.principal.issuer,
      session.principal.subject,
      session.clientProfileId,
    ]),
    authorityBinding: session.authorityBinding,
    userId: session.userId,
    personalPrefix: session.personalPrefix,
    expiresAt: session.expiresAt,
  );

  /// Trusted endpoint, without credential bytes.
  final Uri serviceOrigin;

  /// Explicit application/dataset security boundary.
  final String securityDomain;

  /// Application-verified account or explicit no-auth dataset identity.
  final String accountIdentity;

  /// Current authorization/session generation.
  final String authorityBinding;

  /// UID metadata; does not change storage identity.
  final String? userId;

  /// Optional personal root; confers no permissions.
  final String? personalPrefix;

  /// Verified credential/session deadline; metadata cannot extend it.
  final DateTime? expiresAt;

  /// Stable storage identity which survives UID assignment and token refresh.
  String get stableIdentity =>
      jsonEncode([serviceOrigin.toString(), securityDomain, accountIdentity]);
  @override
  String toString() => 'LanternSessionBinding(redacted)';
}

/// One bound pure-Dart application contract across direct and stored profiles.
abstract interface class LanternDataClient {
  /// Fixed verified lifetime ownership; no per-call partition selector.
  LanternSessionBinding get binding;

  /// Advanced admission/status control; ordinary writes await Server handling.
  LanternMutationControl get mutations;

  /// Bounded entity reads preserving request positions.
  Future<List<LanternSnapshot<Vertex>>> getVertices(
    Iterable<String> keys, {
    GetOptions? options,
  });

  /// Singular facade over getVertices.
  Future<LanternSnapshot<Vertex>> getVertex(String key, {GetOptions? options});

  /// Bounded edge reads preserving duplicate positions.
  Future<List<LanternSnapshot<Edge>>> getEdges(
    Iterable<EdgeRef> edges, {
    GetOptions? options,
  });

  /// Singular facade over getEdges.
  Future<LanternSnapshot<Edge>> getEdge(EdgeRef edge, {GetOptions? options});

  /// Unconditional indexed Vertex Put.
  Future<MutationBatchReply<PutOutcome>> putVertices(
    Iterable<VertexInput> inputs, {
    MutationOptions? options,
  });

  /// Singular unconditional Vertex Put.
  Future<MutationReply<PutOutcome>> putVertex(
    VertexInput input, {
    MutationOptions? options,
  });

  /// Conditional indexed Vertex Put.
  Future<MutationBatchReply<PutOutcome>> putVerticesIfAbsent(
    Iterable<VertexInput> inputs, {
    MutationOptions? options,
  });

  /// Singular conditional Vertex Put.
  Future<MutationReply<PutOutcome>> putVertexIfAbsent(
    VertexInput input, {
    MutationOptions? options,
  });

  /// Indexed Edge Put.
  Future<MutationBatchReply<PutOutcome>> putEdges(
    Iterable<EdgeInput> inputs, {
    MutationOptions? options,
  });

  /// Singular Edge Put.
  Future<MutationReply<PutOutcome>> putEdge(
    EdgeInput input, {
    MutationOptions? options,
  });

  /// Indexed additive writes with exact binary32 Server results.
  Future<MutationBatchReply<double>> addEdges(
    Iterable<EdgeInput> inputs, {
    MutationOptions? options,
  });

  /// Singular additive write.
  Future<MutationReply<double>> addEdge(
    EdgeInput input, {
    MutationOptions? options,
  });

  /// Indexed Vertex Delete, preserving false and duplicate results.
  Future<MutationBatchReply<bool>> deleteVertices(
    Iterable<String> keys, {
    MutationOptions? options,
  });

  /// Singular Vertex Delete.
  Future<MutationReply<bool>> deleteVertex(
    String key, {
    MutationOptions? options,
  });

  /// Indexed Edge Delete.
  Future<MutationBatchReply<bool>> deleteEdges(
    Iterable<EdgeRef> edges, {
    MutationOptions? options,
  });

  /// Singular Edge Delete.
  Future<MutationReply<bool>> deleteEdge(
    EdgeRef edge, {
    MutationOptions? options,
  });

  /// Indexed targeted contribution Delete.
  Future<MutationBatchReply<bool>> deleteEdgeContributions(
    Iterable<EdgeContributionRef> contributions, {
    MutationOptions? options,
  });

  /// Singular targeted contribution Delete.
  Future<MutationReply<bool>> deleteEdgeContribution(
    EdgeContributionRef contribution, {
    MutationOptions? options,
  });

  /// Existing typed Server traversal, with separate result certainty.
  Future<LanternTraversalSnapshot> illuminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    TraversalReadOptions? options,
  });

  /// Shared bounded Vertex observations.
  Stream<List<LanternSnapshot<Vertex>>> watchVertices(
    Iterable<String> keys, {
    WatchOptions? options,
  });

  /// Singular Vertex watch.
  Stream<LanternSnapshot<Vertex>> watchVertex(
    String key, {
    WatchOptions? options,
  });

  /// Shared bounded Edge observations.
  Stream<List<LanternSnapshot<Edge>>> watchEdges(
    Iterable<EdgeRef> edges, {
    WatchOptions? options,
  });

  /// Singular Edge watch.
  Stream<LanternSnapshot<Edge>> watchEdge(
    EdgeRef edge, {
    WatchOptions? options,
  });

  /// Active typed traversal observations with explicit coverage.
  Stream<LanternTraversalSnapshot> watchIlluminate(
    String seed, {
    required TraversalOptions traversal,
    TraversalWeighting weighting = TraversalWeighting.serverDefault,
    String vertexPrefix = '',
    WatchOptions? options,
  });

  /// Content-free session health.
  Stream<LanternSyncStatus> get syncStatus;

  /// Cancels and joins owned work before ending this bound lifetime.
  Future<void> close();
}
