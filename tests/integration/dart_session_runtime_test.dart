// Run from sdks/dart/offline. The selected real Connect endpoint is mandatory.
import 'dart:io';
import '../../sdks/dart/offline/test/real_wire_test.dart' as wire;

void main() {
  final endpoint = Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
  if (endpoint == null || endpoint.isEmpty) {
    throw StateError('LANTERN_DART_REAL_WIRE_ENDPOINT is required');
  }
  wire.runSessionRuntimeWireTests(Uri.parse(endpoint));
}
