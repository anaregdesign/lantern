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
    if (capability case ReceiptCapabilityEnabled()) {
      final receiptContext = client.mintReceiptContext(
        capability: capability,
        itemCount: 1,
      );
      try {
        final result = await client.deleteEdgeWithReceipt(
          const EdgeRef('user:42', 'group:example'),
          context: receiptContext,
        );
        stdout.writeln('edge existed: ${result.existed}');
      } on ReceiptReconciliationException catch (error) {
        final status = await client.getReceiptStatus(
          error.context.operationIds.single,
        );
        switch (status.state) {
          case ReceiptStatusState.confirmed:
            stdout.writeln('original edge existed: ${status.receipt!.existed}');
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
