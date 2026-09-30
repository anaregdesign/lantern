import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/io.dart' as connect_io;
import 'package:connectrpc/protobuf.dart';
import 'package:connectrpc/protocol/connect.dart' as connect_protocol;
import 'package:lantern_client/lantern_client.dart';
import 'package:test/test.dart';

void main() {
  final endpointValue = Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
  final receiptValue = Platform.environment['LANTERN_DART_RECEIPT_ENDPOINT'];
  final token = Platform.environment['LANTERN_DART_RECEIPT_TOKEN'];
  final prefix = 'dart-contribution-${DateTime.now().microsecondsSinceEpoch}';

  test(
    'contribution Delete preserves Put base, other rows and indexed misses',
    () async {
      final endpoint = Uri.parse(endpointValue!);
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        token: token,
      );
      addTearDown(client.close);
      final edge = EdgeRef('$prefix-direct-t', '$prefix-direct-h');
      EdgeContributionRef ref(int id, {String? head}) => EdgeContributionRef(
        tail: edge.tail,
        head: head ?? edge.head,
        contribId: Uint8List(24)..[23] = id,
      );
      await client.putEdge(
        EdgeInput(tail: edge.tail, head: edge.head, weight: 1),
      );
      for (final (id, weight) in [(1, 2.0), (2, 3.0)]) {
        await client.addEdge(
          EdgeInput(
            tail: edge.tail,
            head: edge.head,
            weight: weight,
            contribId: ref(id).contribId,
          ),
        );
      }
      await client.addEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 7,
          contribId: ref(3).contribId,
          expiresAt: DateTime.now().toUtc().subtract(
            const Duration(seconds: 1),
          ),
        ),
      );
      expect((await client.getEdge(edge)).weight, 6);
      final result = await client.deleteEdgeContributions([
        ref(1),
        ref(1),
        ref(9),
        ref(3),
        ref(2, head: '$prefix-wrong'),
      ], batchSize: 2);
      expect(result.existed, [true, false, false, false, false]);
      expect(result.deleted, 1);
      expect((await client.getEdge(edge)).weight, 4);
      expect(await client.deleteEdgeContribution(ref(2)), isTrue);
      expect((await client.getEdge(edge)).weight, 1);
      expect(await client.deleteEdge(edge), isTrue);
      expect(await client.deleteEdgeContribution(ref(2)), isFalse);
    },
    skip: endpointValue == null ? 'real wire endpoint is required' : false,
  );

  test(
    'contribution receipt loss, client restart, status and replay retain originals',
    () async {
      final endpoint = Uri.parse(receiptValue!);
      final direct = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        token: token,
      );
      addTearDown(direct.close);
      final edge = EdgeRef('$prefix-receipt-t', '$prefix-receipt-h');
      final ref = EdgeContributionRef(
        tail: edge.tail,
        head: edge.head,
        contribId: Uint8List(24)..[23] = 1,
      );
      await direct.putEdge(
        EdgeInput(tail: edge.tail, head: edge.head, weight: 1),
      );
      await direct.addEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 2,
          contribId: ref.contribId,
        ),
      );
      final capability =
          await direct.getReceiptCapability() as ReceiptCapabilityEnabled;
      expect(
        capability.supports(ReceiptMutationKind.edgeContributionDelete),
        isTrue,
      );
      final context = direct.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.edgeContributionDelete,
        itemCount: 2,
      );
      final fault = _CommittedContributionLoss(endpoint);
      final uncertain = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        token: token,
        transport: fault,
        onClose: fault.close,
      );
      await expectLater(
        uncertain.deleteEdgeContributionsWithReceipt([
          ref,
          ref,
        ], context: context),
        throwsA(
          isA<ReceiptReconciliationException>().having(
            (error) => error.reason,
            'reason',
            ReceiptReconciliationReason.outcomeUnknown,
          ),
        ),
      );
      expect(fault.mutations, 1);
      await uncertain.close();
      final persisted = ReceiptContext(
        operationIds: context.operationIds.map(
          (id) => ReceiptOperationId(id.bytes),
        ),
        groupId: ReceiptGroupId(context.groupId.bytes),
        endpoint: ReceiptEndpoint(
          nodeId: context.endpoint.nodeId,
          generation: context.endpoint.generation,
        ),
        mutation: context.mutation,
      );
      final restarted = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        token: token,
      );
      addTearDown(restarted.close);
      final statuses = await restarted.getReceiptStatuses(
        persisted.operationIds,
      );
      expect(
        statuses.map((status) => status.state),
        everyElement(ReceiptStatusState.confirmed),
      );
      expect(
        statuses.map(
          (status) =>
              (status.receipt! as EdgeContributionDeleteReceipt).existed,
        ),
        [true, false],
      );
      expect((await direct.getEdge(edge)).weight, 1);
      final replay = await restarted.deleteEdgeContributionsWithReceipt([
        ref,
        ref,
      ], context: persisted);
      expect(replay.map((result) => result.existed), [true, false]);
      final changed = EdgeContributionRef(
        tail: edge.tail,
        head: edge.head,
        contribId: Uint8List(24)..[23] = 2,
      );
      await expectLater(
        restarted.deleteEdgeContributionsWithReceipt([
          changed,
          ref,
        ], context: persisted),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      final unobserved = restarted.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.edgeContributionDelete,
        itemCount: 1,
      );
      expect(
        (await restarted.getReceiptStatus(
          unobserved.operationIds.single,
        )).state,
        ReceiptStatusState.notYetObserved,
      );
      expect(await direct.deleteEdge(edge), isTrue);
    },
    skip: receiptValue == null || token == null
        ? 'receipt endpoint/token required'
        : false,
  );

  test(
    'contribution receipt CDC invalidates only its affected edge pair',
    () async {
      final endpoint = Uri.parse(receiptValue!);
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        token: token,
      );
      addTearDown(client.close);
      final edge = EdgeRef('$prefix-cdc-t', '$prefix-cdc-h');
      final ref = EdgeContributionRef(
        tail: edge.tail,
        head: edge.head,
        contribId: Uint8List(24)..[23] = 1,
      );
      await client.putEdge(
        EdgeInput(tail: edge.tail, head: edge.head, weight: 1),
      );
      await client.addEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 2,
          contribId: ref.contribId,
        ),
      );
      final ready = Completer<void>();
      final invalidation = Completer<IdentityChunkFrame>();
      final subscription = client
          .subscribeIdentity(bootstrap: true)
          .listen(
            (frame) {
              if (frame is IdentityCheckpointFrame && !ready.isCompleted)
                ready.complete();
              if (frame is IdentityChunkFrame &&
                  frame.operation == IdentityOperation.deleteEdgeContribution &&
                  frame.edgeKeys.contains(edge)) {
                if (!invalidation.isCompleted) invalidation.complete(frame);
              }
            },
            onError: (Object error, StackTrace stack) {
              if (!ready.isCompleted) ready.completeError(error, stack);
              if (!invalidation.isCompleted)
                invalidation.completeError(error, stack);
            },
          );
      addTearDown(subscription.cancel);
      await ready.future.timeout(const Duration(seconds: 10));
      expect(await client.deleteEdgeContribution(ref), isTrue);
      final frame = await invalidation.future.timeout(
        const Duration(seconds: 10),
      );
      expect(frame.vertexKeys, isEmpty);
      expect(frame.edgeKeys, [edge]);
      expect((await client.getEdge(edge)).weight, 1);
      await subscription.cancel();
      expect(await client.deleteEdge(edge), isTrue);
    },
    skip: receiptValue == null || token == null
        ? 'identity responder required'
        : false,
  );
}

final class _CommittedContributionLoss implements connect.Transport {
  _CommittedContributionLoss(Uri endpoint) : _http = HttpClient() {
    _inner = connect_protocol.Transport(
      baseUrl: endpoint.toString(),
      codec: const ProtoCodec(),
      httpClient: connect_io.createHttpClient(_http),
    );
  }
  final HttpClient _http;
  late final connect.Transport _inner;
  int mutations = 0;
  Future<void> close() async => _http.close(force: true);
  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    final response = await _inner.unary(spec, input, options);
    if (spec.procedure.endsWith('/DeleteEdgeContributions')) {
      mutations++;
      throw connect.ConnectException(
        connect.Code.unavailable,
        'committed response lost',
      );
    }
    return response;
  }

  @override
  Future<connect.StreamResponse<I, O>>
  stream<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    Stream<I> input, [
    connect.CallOptions? options,
  ]) => _inner.stream(spec, input, options);
}
