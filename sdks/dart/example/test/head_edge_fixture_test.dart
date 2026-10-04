import 'dart:io' as io;

import 'package:connectrpc/io.dart' as connect_io;
import 'package:connectrpc/protobuf.dart';
import 'package:connectrpc/protocol/connect.dart' as connect_protocol;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';

import '../integration_test/support/head_edge_fixture.dart';
import '../integration_test/support/head_edge_wire.dart';

void main() {
  test('contribution identity survives restart but isolates new intents', () {
    final original = physicalContributionId('0123456789abcdef', 1);
    expect(original, physicalContributionId('0123456789abcdef', 1));
    expect(original, hasLength(24));
    expect(original.any((byte) => byte != 0), isTrue);
    expect(original, isNot(physicalContributionId('0123456789abcdef', 2)));
    expect(original, isNot(physicalContributionId('fedcba9876543210', 1)));
    // A caller cannot mutate a later reconstruction of the dispatched ID.
    original[0] ^= 255;
    expect(original, isNot(physicalContributionId('0123456789abcdef', 1)));
  });

  test('endpoint cleanup includes shared endpoints exactly once', () {
    expect(
      edgeEndpointKeys([
        const EdgeRef('tail', 'head'),
        const EdgeRef('tail', 'other'),
        const EdgeRef('head', 'head'),
      ]),
      {'tail', 'head', 'other'},
    );
  });

  test('missing run or intent cannot create a fixture identity', () {
    expect(() => physicalContributionId('', 1), throwsArgumentError);
    expect(() => physicalContributionId('run', 0), throwsArgumentError);
  });

  final endpointValue = io.Platform.environment['LANTERN_HEAD_WIRE_ENDPOINT'];
  test(
    'physical Head assertions execute on the production Server wire',
    () async {
      final endpoint = Uri.parse(endpointValue!);
      LanternClient connect(String? credential) {
        final tokenPath = credential == null
            ? null
            : io.Platform.environment[credential]!;
        final caPath = io.Platform.environment['LANTERN_HEAD_WIRE_CA_FILE'];
        final context = caPath == null
            ? io.SecurityContext.defaultContext
            : (io.SecurityContext(withTrustedRoots: false)
                ..setTrustedCertificates(caPath));
        final http = io.HttpClient(context: context);
        final transport = connect_protocol.Transport(
          baseUrl: endpoint.toString(),
          codec: const ProtoCodec(),
          httpClient: connect_io.createHttpClient(http),
        );
        return LanternClient.connect(
          endpoint,
          token: tokenPath == null
              ? null
              : io.File(tokenPath).readAsStringSync().trim(),
          allowInsecure: false,
          transport: transport,
          onClose: () => http.close(force: true),
          retryPolicy: const RetryPolicy(maxAttempts: 1),
        );
      }

      final observer = connect('LANTERN_HEAD_WIRE_OBSERVER_TOKEN_FILE');
      final writer = connect('LANTERN_HEAD_WIRE_WRITER_TOKEN_FILE');
      addTearDown(observer.close);
      addTearDown(writer.close);
      await verifyHeadEdgeWire(
        observer: observer,
        writer: writer,
        runId: DateTime.now().microsecondsSinceEpoch.toString(),
        allowInsecure: false,
        anonymousClient: connect(null),
        recordPhase: (_) async {},
        registerCleanup: addTearDown,
      );
    },
    skip: endpointValue == null || endpointValue.isEmpty,
  );
}
