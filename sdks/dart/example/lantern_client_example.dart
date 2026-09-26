import 'dart:io';

import 'package:lantern_client/lantern_client.dart';

Future<void> main() async {
  final client = LanternClient.connect(
    Uri.parse('https://lantern.example.com'),
  );
  try {
    final outcome = await client.putVertex(
      VertexInput(
        key: 'user:42',
        value: VertexValue.string('alice'),
        expiresIn: const Duration(minutes: 30),
      ),
    );
    if (outcome != PutOutcome.appliedAndLive) {
      throw StateError('user:42 was not live after Put: $outcome');
    }
    final vertex = await client.getVertex('user:42');
    stdout.writeln((vertex.value as StringValue).value);

    final capability = await client.getReceiptCapability();
    if (capability case ReceiptCapabilityEnabled(
      supportedMutations: final supported,
    )) {
      if (!supported.containsAll({
        ReceiptMutationKind.vertexPut,
        ReceiptMutationKind.vertexDelete,
      })) {
        return;
      }
      const receiptKey = 'user:receipt-example';
      final putContext = client.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.vertexPut,
        itemCount: 1,
      );
      final put = await client.putVertexWithReceipt(
        VertexInput(key: receiptKey, value: VertexValue.string('receipt')),
        context: putContext,
      );
      stdout.writeln('receipt Put outcome: ${put.outcome}');

      final receiptContext = client.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.vertexDelete,
        itemCount: 1,
      );
      try {
        final result = await client.deleteVertexWithReceipt(
          receiptKey,
          context: receiptContext,
        );
        stdout.writeln('vertex existed: ${result.existed}');
      } on ReceiptReconciliationException catch (error) {
        final status = await client.getReceiptStatus(
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
        }
      }
    }
  } finally {
    await client.close();
  }
}
