import 'dart:async';
import 'dart:io' as io;
import 'package:lantern_client/lantern_client.dart';
import 'package:test/test.dart';

void main() {
  final endpoint = io.Platform.environment['LANTERN_SCOPED_WIRE_URL'];
  if (endpoint == null) {
    test('public CDC requires the certified OIDC wire fixture', () {
      markTestSkipped(
        'Run through tests/integration with the scoped SDK fixture.',
      );
    });
    return;
  }
  test(
    'real public CDC uses opaque checkpoints and excludes denied identities',
    () async {
      final context = io.SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates(
          io.Platform.environment['LANTERN_SCOPED_WIRE_CA']!,
        );
      final sdk = LanternClient.connect(
        Uri.parse(endpoint),
        httpClientFactory: () => io.HttpClient(context: context),
        tokenProvider: () =>
            io.Platform.environment['LANTERN_SCOPED_WIRE_CREDENTIAL'],
        defaultTimeout: const Duration(seconds: 5),
      );
      addTearDown(sdk.close);
      final iterator = StreamIterator(
        sdk.watchChanges(
          prefix: 'orders:',
          bootstrap: true,
          options: LanternCallOptions(timeout: const Duration(seconds: 5)),
        ),
      );
      addTearDown(iterator.cancel);
      expect(await iterator.moveNext(), isTrue);
      expect(iterator.current.bootstrap, isTrue);
      expect(iterator.current.cursor, isNotNull);
      expect(iterator.current.invalidations, isEmpty);
      await sdk.putVertices([
        VertexInput(
          key: 'orders:private:1',
          value: VertexValue.string('hidden'),
        ),
        VertexInput(key: 'orders:1', value: VertexValue.string('visible')),
      ]);
      while (await iterator.moveNext()) {
        if (iterator.current.invalidations.isEmpty) continue;
        final invalidation =
            iterator.current.invalidations.single as VertexInvalidation;
        expect(invalidation.key, 'orders:1');
        expect(invalidation.current, isNull);
        break;
      }
      await iterator.cancel();
      await sdk.putVertices([
        VertexInput(key: 'orders:create:source:a', value: VertexValue.nil()),
        VertexInput(key: 'orders:create:target:b', value: VertexValue.nil()),
      ]);
      final input = EdgeInput(
        tail: 'orders:create:source:a',
        head: 'orders:create:target:b',
        weight: 2,
      );
      expect(
        await sdk.createEdges([
          input,
          EdgeInput(tail: input.tail, head: input.head, weight: 9),
          EdgeInput(
            tail: input.tail,
            head: 'orders:create:target:missing',
            weight: 1,
          ),
          EdgeInput(
            tail: input.tail,
            head: input.head,
            weight: 1,
            expiresAt: DateTime.fromMillisecondsSinceEpoch(1),
          ),
        ]),
        [
          CreateEdgeOutcome.createdAndLive,
          CreateEdgeOutcome.edgeExists,
          CreateEdgeOutcome.endpointNotLive,
          CreateEdgeOutcome.expired,
        ],
      );
      final capability = await sdk.getReceiptCapability();
      expect(capability, isA<ReceiptCapabilityEnabled>());
      final receiptContext = sdk.mintReceiptContext(
        capability: capability as ReceiptCapabilityEnabled,
        mutation: ReceiptMutationKind.edgeCreate,
        itemCount: 1,
      );
      expect(
        await sdk.createEdgeWithReceipt(input, context: receiptContext),
        CreateEdgeOutcome.edgeExists,
      );
      final status = await sdk.getReceiptStatus(
        receiptContext.operationIds.single,
      );
      expect(
        status.receipt,
        isA<EdgeCreateReceipt>().having(
          (r) => r.outcome,
          'outcome',
          CreateEdgeOutcome.edgeExists,
        ),
      );
      await expectLater(
        sdk.createEdge(
          EdgeInput(tail: input.head, head: input.tail, weight: 1),
        ),
        throwsA(
          isA<BatchException>().having(
            (e) => e.cause,
            'cause',
            isA<LanternPermissionDeniedException>(),
          ),
        ),
      );

      await expectLater(
        sdk
            .watchChanges(
              prefix: 'orders:',
              bootstrap: true,
              projection: ChangeProjection.value,
            )
            .first,
        throwsA(isA<LanternPermissionDeniedException>()),
      );
    },
  );
}
