import 'dart:io';

import 'package:lantern_client/lantern_client.dart';

Future<void> main() async {
  final client = LanternClient.connect(
    Uri.parse('https://lantern.example.com'),
  );
  try {
    await client.putVertex(
      VertexInput(
        key: 'user:42',
        value: VertexValue.string('alice'),
        expiresIn: const Duration(minutes: 30),
      ),
    );
    final vertex = await client.getVertex('user:42');
    stdout.writeln((vertex.value as StringValue).value);
  } finally {
    await client.close();
  }
}
