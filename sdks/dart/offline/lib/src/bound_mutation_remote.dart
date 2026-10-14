import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';
import 'types.dart';

/// Qualified one-attempt mutation port, with owner fences around token acquisition.
abstract interface class OfflineBoundMutationRemote {
  /// The pre-send proof has already committed; this method performs no hidden retry.
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord record, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  });
}

/// Exact parent adapter; generated messages never escape the parent package.
final class LanternBoundMutationRemote implements OfflineBoundMutationRemote {
  /// Binds the qualified parent transport without changing its authentication policy.
  const LanternBoundMutationRemote(this.client);

  /// Bound parent transport used only for normalization or qualified remote attempts.
  final LanternClient client;
  @override
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord record, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  }) async {
    final intent = client.freezeIndexedMutation(
      offlineRequest(record.intent),
      admittedAt: record.enqueuedAt,
    );
    return (await client.attemptIndexedMutation(
      intent,
      receiptContext: record.receipt?.context,
      options: LanternCallOptions(cancellation: cancellation, retry: false),
      beforeDispatch: beforeDispatch,
      beforePublication: beforePublication,
    )).items.single;
  }
}

/// Reconstructs frozen public wire intent without rebasing TTL or Add identities.
MutationRequest<Object?> offlineRequest(
  OfflineIntent intent,
) => switch (intent) {
  OfflinePutVertexIntent(:final vertex) => MutationRequest.putVertices([
    _vertex(vertex),
  ]),
  OfflinePutVertexIfAbsentIntent(:final vertex) =>
    MutationRequest.putVerticesIfAbsent([_vertex(vertex)]),
  OfflinePutEdgeIntent(:final edge) => MutationRequest.putEdges([_edge(edge)]),
  OfflineReceiptAddEdgeIntent(:final edge, :final contributionId) =>
    MutationRequest.addEdges([
      EdgeInput(
        tail: edge.tail,
        head: edge.head,
        weight: edge.weight,
        expiresAt: edge.expiration,
        contribId: contributionId,
      ),
    ]),
  OfflineDeleteVertexIntent(:final vertexKey) => MutationRequest.deleteVertices(
    [vertexKey],
  ),
  OfflineDeleteEdgeIntent(:final edge) => MutationRequest.deleteEdges([edge]),
  OfflineDeleteEdgeContributionIntent(:final contribution) =>
    MutationRequest.deleteEdgeContributions([contribution]),
  OfflineAddEdgeIntent() => throw const LanternDataException(
    DataFailure.unsupported,
  ),
};
VertexInput _vertex(Vertex v) =>
    VertexInput(key: v.key, value: v.value, expiresAt: v.expiration);
EdgeInput _edge(Edge e) => EdgeInput(
  tail: e.tail,
  head: e.head,
  weight: e.weight,
  expiresAt: e.expiration,
);

/// Exact durable result family; undisclosed replies carry no invented effect.
OfflineReceiptResult offlineResult(
  OfflineIntent intent,
  MutationKnownEffect<Object?> reply,
) => switch ((intent, reply.effect)) {
  (
    OfflinePutVertexIntent() || OfflinePutVertexIfAbsentIntent(),
    final PutOutcome o,
  ) =>
    OfflineVertexPutReceiptResult(o),
  (OfflinePutEdgeIntent(), final PutOutcome o) => OfflineEdgePutResult(o),
  (OfflineDeleteVertexIntent(), final bool v) =>
    OfflineVertexDeleteReceiptResult(v),
  (OfflineDeleteEdgeIntent(), final bool v) => OfflineEdgeDeleteReceiptResult(
    v,
  ),
  (OfflineDeleteEdgeContributionIntent(), final bool v) =>
    OfflineEdgeContributionDeleteReceiptResult(v),
  (OfflineReceiptAddEdgeIntent(), final double v) =>
    OfflineEdgeAddReceiptResult(v),
  _ => throw const LanternDataException(DataFailure.typeMismatch),
};
