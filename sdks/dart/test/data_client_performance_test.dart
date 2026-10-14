import 'dart:convert';
import 'dart:io';

import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('bounded direct 1001-item bookkeeping comparison', () async {
    // Same finite 1001-item shape as the existing offline performance probe.
    // This is host/source bookkeeping evidence, not deployment/release latency.
    final transport = FakeTransportBuilder()
        .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
          LanternService.deleteVertices,
          (request, _) => graph.DeleteVerticesResponse(
            deleted: request.keys.length,
            existed: List.filled(request.keys.length, true),
          ),
        )
        .build();
    final endpoint = Uri.parse('http://localhost:6380');
    final raw = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      transport: transport,
    );
    final direct = DirectLanternDataClient(
      raw,
      binding: LanternSessionBinding.application(
        serviceOrigin: endpoint,
        securityDomain: 'performance-fixture',
        accountIdentity: 'no-auth',
        authorityBinding: 'fixture',
      ),
      maxOperations: 64,
    );
    addTearDown(direct.close);
    final keys = List.generate(1001, (i) => 'performance:$i');
    final before = ProcessInfo.currentRss;
    final baseline = <int>[], candidate = <int>[];
    for (var i = 0; i < 50; i++) {
      final timer = Stopwatch()..start();
      expect(await raw.deleteVertices(keys), 1001);
      baseline.add(timer.elapsedMicroseconds);
      timer.reset();
      final result = await direct.deleteVertices(keys);
      expect(result.items, hasLength(1001));
      expect(
        result.items.every((r) => (r as MutationKnownEffect<bool>).effect),
        isTrue,
      );
      candidate.add(timer.elapsedMicroseconds);
    }
    baseline.sort();
    candidate.sort();
    final rss = ProcessInfo.currentRss - before;
    // Retain the existing source-probe ceilings (5 s admission / 512 MiB RSS).
    expect(candidate.last, lessThan(5000000));
    expect(rss, lessThan(536870912));
    stdout.writeln(
      jsonEncode({
        'schema': 1,
        'scenario': 'direct_1001_indexed_50',
        'baselineP50Micros': baseline[25],
        'baselineP99Micros': baseline.last,
        'directP50Micros': candidate[25],
        'directP99Micros': candidate.last,
        'rssDeltaBytes': rss,
        'logicalItems': 50050,
        'result': 'pass',
      }),
    );
  });
}
