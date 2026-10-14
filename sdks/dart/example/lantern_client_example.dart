import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';

Future<void> main() async {
  final endpoint = Uri.parse('https://lantern.example.com');
  final wire = LanternClient.connect(endpoint);
  final LanternDataClient client = DirectLanternDataClient(
    wire,
    binding: LanternSessionBinding.application(
      serviceOrigin: endpoint,
      securityDomain: 'example-dataset',
      accountIdentity: 'explicit-no-auth',
      authorityBinding: 'example-lifetime',
    ),
  );
  try {
    final outcome = await client.putVertex(
      VertexInput(
        key: 'user:42',
        value: VertexValue.string('alice'),
        expiresIn: const Duration(minutes: 30),
      ),
    );
    switch (outcome) {
      case MutationKnownEffect(:final effect):
        stdout.writeln('Server Put handling: $effect');
      case MutationAcceptedUndisclosed():
        stdout.writeln('Server handled Put without disclosing an effect');
    }
    final snapshot = await client.getVertex('user:42');
    if (snapshot.view case Vertex(value: StringValue(:final value))) {
      stdout.writeln(value);
    } else {
      stdout.writeln('Vertex observation: ${snapshot.confirmed.state}');
    }

    // Receipt preparation/reconciliation stays an explicit low-level API.

    final capability = await wire.getReceiptCapability();
    if (capability case ReceiptCapabilityEnabled(
      supportedMutations: final supported,
    )) {
      if (!supported.containsAll({
        ReceiptMutationKind.vertexPut,
        ReceiptMutationKind.vertexDelete,
        ReceiptMutationKind.edgeAdd,
      })) {
        return;
      }
      const receiptKey = 'user:receipt-example';
      final putContext = wire.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.vertexPut,
        itemCount: 1,
      );
      final put = await wire.putVertexWithReceipt(
        VertexInput(key: receiptKey, value: VertexValue.string('receipt')),
        context: putContext,
      );
      stdout.writeln('receipt Put outcome: ${put.outcome}');

      final addContext = wire.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.edgeAdd,
        itemCount: 1,
      );
      final random = Random.secure();
      final contributionNonce = Uint8List.fromList(
        List<int>.generate(16, (_) => random.nextInt(256)),
      );
      final add = await wire.addEdgeWithReceipt(
        EdgeInput(
          tail: 'user:42',
          head: 'group:receipt-example',
          weight: 1,
          contribId: contributionIdFrom(
            nonce: contributionNonce,
            sequence: BigInt.one,
            index: 0,
          ),
        ),
        context: addContext,
      );
      stdout.writeln('receipt Add effective weight: ${add.effectiveWeight}');

      final receiptContext = wire.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.vertexDelete,
        itemCount: 1,
      );
      try {
        final result = await wire.deleteVertexWithReceipt(
          receiptKey,
          context: receiptContext,
        );
        stdout.writeln('vertex existed: ${result.existed}');
      } on ReceiptReconciliationException catch (error) {
        final status = await wire.getReceiptStatus(
          error.context.operationIds.single,
        );
        switch (status.state) {
          case ReceiptStatusState.confirmed:
            final receipt = status.receipt! as VertexDeleteReceipt;
            stdout.writeln('original vertex existed: ${receipt.existed}');
          case ReceiptStatusState.notYetObserved:
            stdout.writeln('receipt not observed; outcome remains uncertain');
          case ReceiptStatusState.noLongerProvable:
            stdout.writeln('receipt outcome is no longer provable');
          case ReceiptStatusState.effectUndisclosed:
            stdout.writeln('request handled; original effect undisclosed');
        }
      }
    }
  } finally {
    await client.close();
  }
}
