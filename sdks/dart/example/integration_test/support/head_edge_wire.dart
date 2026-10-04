import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';

import 'head_edge_fixture.dart';

/// The same production Server/Role assertions run on host and physical phones.
/// The caller owns trust, credentials and cleanup; host success is not device
/// or real-provider evidence.
Future<({List<EdgeRef> edges, List<Vertex> original})> verifyHeadEdgeWire({
  required LanternClient observer,
  required LanternClient writer,
  required String runId,
  required bool allowInsecure,
  LanternClient? anonymousClient,
  required Future<void> Function(String) recordPhase,
  required void Function(FutureOr<dynamic> Function()) registerCleanup,
}) async {
  final endpoint = observer.endpoint;
  final edges = [
    for (final name in ['direct', 'receipt', 'offline'])
      EdgeRef(
        'physical-head:tails:$runId:$name',
        'physical-head:heads:$runId:$name',
      ),
  ];
  final missing = EdgeRef(
    'physical-head:tails:$runId:missing',
    'physical-head:heads:$runId:missing',
  );
  registerCleanup(() async {
    await observer.deleteEdges([...edges, missing]);
    await observer.deleteVertices(edgeEndpointKeys([...edges, missing]));
  });
  await seedLiveEdgeEndpoints(
    observer,
    edges,
    value: VertexValue.string('independent-endpoint-$runId'),
    expiresIn: const Duration(hours: 8),
  );
  final original = await observer.getVertices(edgeEndpointKeys(edges));

  await recordPhase('role_boundaries');
  await writer.getVertex(edges.first.tail);
  await expectLater(
    writer.getVertex(edges.first.head),
    throwsA(isA<LanternPermissionDeniedException>()),
  );
  await expectLater(
    writer.putVertex(
      VertexInput(
        key: edges.first.head,
        value: VertexValue.string('overwrite'),
      ),
    ),
    throwsA(isA<LanternPermissionDeniedException>()),
  );
  await expectLater(
    writer.deleteVertex(edges.first.head),
    throwsA(isA<LanternPermissionDeniedException>()),
  );
  final anonymous =
      anonymousClient ??
      LanternClient.connect(endpoint, allowInsecure: allowInsecure);
  registerCleanup(anonymous.close);
  await expectLater(
    anonymous.addEdge(
      EdgeInput(tail: edges.first.tail, head: edges.first.head, weight: 2),
    ),
    throwsA(isA<LanternUnauthenticatedException>()),
  );
  await expectLater(
    writer.addEdge(
      EdgeInput(
        tail: 'physical-head:unreadable:$runId',
        head: edges.first.head,
        weight: 2,
      ),
    ),
    throwsA(isA<LanternPermissionDeniedException>()),
  );
  await expectLater(
    writer.putEdge(
      EdgeInput(
        tail: edges.first.tail,
        head: 'physical-head:heads:denied:$runId',
        weight: 2,
      ),
    ),
    throwsA(isA<LanternPermissionDeniedException>()),
  );

  await recordPhase('whole_request_ack');
  expect(
    await captureMutationReply(
      () => writer.addEdge(
        EdgeInput(tail: edges.first.tail, head: edges.first.head, weight: 2),
      ),
    ),
    isA<MutationAcceptedUndisclosed<double>>(),
  );
  expect((await observer.getEdge(edges.first)).weight, 2);
  // Endpoint failure is undisclosed and the protected batch has no effects.
  expect(
    await captureMutationReply(
      () => writer.addEdges([
        EdgeInput(tail: edges.first.tail, head: edges.first.head, weight: 2),
        EdgeInput(tail: missing.tail, head: missing.head, weight: 2),
      ]),
    ),
    isA<MutationAcceptedUndisclosed<AddEdgesResult>>(),
  );
  expect((await observer.getEdge(edges.first)).weight, 2);
  expect((await observer.getVertices([missing.tail, missing.head])).missing, [
    missing.tail,
    missing.head,
  ]);
  expect(
    await captureMutationReply(
      () => writer.putEdge(
        EdgeInput(tail: edges.first.tail, head: edges.first.head, weight: 3),
      ),
    ),
    isA<MutationAcceptedUndisclosed<PutOutcome>>(),
  );
  expect((await observer.getEdge(edges.first)).weight, 3);
  expect(
    await captureMutationReply(() => writer.deleteEdge(edges.first)),
    isA<MutationAcceptedUndisclosed<bool>>(),
  );
  await expectLater(
    observer.getEdge(edges.first),
    throwsA(isA<LanternNotFoundException>()),
  );

  await recordPhase('receipt_disclosure');
  final capability =
      await writer.getReceiptCapability() as ReceiptCapabilityEnabled;
  final context = writer.mintReceiptContext(
    capability: capability,
    mutation: ReceiptMutationKind.edgeAdd,
    itemCount: 1,
  );
  final input = EdgeInput(
    tail: edges[1].tail,
    head: edges[1].head,
    weight: 4,
    contribId: physicalContributionId(runId, 1),
  );
  expect(
    await captureMutationReply(
      () => writer.addEdgeWithReceipt(input, context: context),
    ),
    isA<MutationAcceptedUndisclosed<ReceiptEdgeAddResult>>(),
  );
  final status = (await writer.getReceiptStatuses(context.operationIds)).single;
  expect(status.state, ReceiptStatusState.effectUndisclosed);
  expect(status.receipt, isNull);
  expect(
    await captureMutationReply(
      () => writer.addEdgeWithReceipt(input, context: context),
    ),
    isA<MutationAcceptedUndisclosed<ReceiptEdgeAddResult>>(),
  );
  expect((await observer.getEdge(edges[1])).weight, 4);
  final endpoints = await observer.getVertices(edgeEndpointKeys(edges));
  expect(endpoints.missing, isEmpty);
  expect(
    endpoints.vertices.map(
      (vertex) =>
          (vertex.key, vertex.expiration, (vertex.value as StringValue).value),
    ),
    original.vertices.map(
      (vertex) =>
          (vertex.key, vertex.expiration, (vertex.value as StringValue).value),
    ),
  );

  return (edges: edges, original: original.vertices);
}
