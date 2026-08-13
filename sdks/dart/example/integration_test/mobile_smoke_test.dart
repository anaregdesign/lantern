import 'dart:async';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('native mobile real-wire smoke', (tester) async {
    const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
    expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
    final endpoint = Uri.parse(endpointValue);
    final client = LanternClient.connect(
      endpoint,
      allowInsecure: const bool.fromEnvironment('LANTERN_ALLOW_INSECURE'),
      retryPolicy: const RetryPolicy(),
      idempotentAdds: true,
    );
    addTearDown(client.close);
    await client.ping();

    final prefix = 'mobile-smoke:${DateTime.now().microsecondsSinceEpoch}:';
    final inputs = <VertexInput>[
      VertexInput(key: '${prefix}f64', value: VertexValue.float64(1.25)),
      VertexInput(key: '${prefix}f32', value: VertexValue.float32(2.5)),
      VertexInput(key: '${prefix}i32', value: VertexValue.int32(-7)),
      VertexInput(
        key: '${prefix}i64',
        value: VertexValue.int64(-0x8000000000000000),
      ),
      VertexInput(key: '${prefix}u32', value: VertexValue.uint32(0xffffffff)),
      VertexInput(
        key: '${prefix}u64',
        value: VertexValue.uint64((BigInt.one << 64) - BigInt.one),
      ),
      VertexInput(key: '${prefix}bool', value: VertexValue.boolean(true)),
      VertexInput(key: '${prefix}string', value: VertexValue.string('mobile')),
      VertexInput(
        key: '${prefix}bytes',
        value: VertexValue.bytes(Uint8List.fromList([0, 1, 255])),
      ),
      VertexInput(
        key: '${prefix}timestamp',
        value: VertexValue.timestamp(DateTime.parse('2026-07-12T00:00:00Z')),
      ),
      VertexInput(
        key: '${prefix}duration',
        value: VertexValue.duration(const Duration(seconds: 3)),
      ),
      VertexInput(key: '${prefix}nil', value: VertexValue.nil()),
      VertexInput(key: '${prefix}unset', value: VertexValue.unset()),
    ];
    expect(
      (await client.putVertices(inputs)).map((result) => result.outcome),
      everyElement(PutOutcome.appliedAndLive),
    );
    expect(
      (await client.getVertices(inputs.map((input) => input.key))).missing,
      isEmpty,
    );

    final edge = EdgeInput(
      tail: '${prefix}tail',
      head: '${prefix}head',
      weight: 1,
      expiresIn: const Duration(minutes: 1),
    );
    expect(await client.addEdge(edge), 1);
    expect(
      await client.scanVertexKeys(prefix: prefix, limit: 3),
      isA<Page<String>>(),
    );
    expect(
      await client.illuminate(
        edge.tail,
        traversal: const BfsOptions(step: 1, fanOut: 2),
        vertexPrefix: prefix,
      ),
      isA<Graph>(),
    );
    final offline = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: LanternClientOfflineRemote(client),
    );
    addTearDown(offline.dispose);
    const partition = 'mobile-smoke-session';
    final offlineVertexKey = '${prefix}offline-vertex';
    final put = await offline.putVertex(
      partitionId: partition,
      input: VertexInput(
        key: offlineVertexKey,
        value: VertexValue.string('queued-offline'),
        expiresIn: const Duration(minutes: 2),
      ),
    );
    final putStatuses = put.statuses.toList();
    final offlineEdge = EdgeInput(
      tail: '${prefix}offline-tail',
      head: '${prefix}offline-head',
      weight: 0.5,
      expiresIn: const Duration(minutes: 2),
    );
    final edgePut = await offline.putEdge(
      partitionId: partition,
      input: offlineEdge,
    );
    final edgePutStatuses = edgePut.statuses.toList();
    final pendingVertex = await offline.readVertex(
      partition,
      offlineVertexKey,
      policy: OfflineReadPolicy.cacheOnly,
    );
    final pendingEdge = await offline.readEdge(
      partition,
      EdgeRef(offlineEdge.tail, offlineEdge.head),
      policy: OfflineReadPolicy.cacheOnly,
    );
    expect(pendingVertex.hasPendingWrites, isTrue);
    expect(pendingEdge.hasPendingWrites, isTrue);

    expect(await offline.probeAndDrain(partition), 2);
    expect((await putStatuses).last.state, OfflineWriteState.confirmed);
    expect((await edgePutStatuses).last.state, OfflineWriteState.confirmed);
    final cachedVertex = await offline.readVertex(
      partition,
      offlineVertexKey,
      policy: OfflineReadPolicy.cacheOnly,
    );
    final cachedEdge = await offline.readEdge(
      partition,
      EdgeRef(offlineEdge.tail, offlineEdge.head),
      policy: OfflineReadPolicy.cacheOnly,
    );
    expect(cachedVertex.hasPendingWrites, isFalse);
    expect(cachedVertex.source, OfflineReadSource.cache);
    expect(cachedEdge.hasPendingWrites, isFalse);
    expect(
      (await client.getVertex(offlineVertexKey)).value,
      isA<StringValue>(),
    );
    expect(
      await client.getEdge(EdgeRef(offlineEdge.tail, offlineEdge.head)),
      isA<Edge>().having((value) => value.weight, 'weight', 0.5),
    );

    // The device clock is deliberately behind the server. The resolved
    // expiration remains locally live, but the authoritative server outcome
    // must terminalize both items as expired and remove any older cache state.
    final skewedNow = DateTime.now().toUtc().subtract(const Duration(hours: 2));
    final serverExpiredAt = skewedNow.add(const Duration(hours: 1));
    final skewed = OfflineLanternRepository(
      store: InMemoryOfflineStore(),
      remote: LanternClientOfflineRemote(client),
      config: OfflineConfig(clock: () => skewedNow),
    );
    addTearDown(skewed.dispose);
    final expiredVertexKey = '${prefix}server-expired-vertex';
    final expiredEdge = EdgeRef(
      '${prefix}server-expired-tail',
      '${prefix}server-expired-head',
    );
    final expiredVertex = await skewed.putVertex(
      partitionId: partition,
      input: VertexInput(
        key: expiredVertexKey,
        value: VertexValue.string('must-not-confirm'),
        expiresAt: serverExpiredAt,
      ),
    );
    final expiredEdgeWrite = await skewed.putEdge(
      partitionId: partition,
      input: EdgeInput(
        tail: expiredEdge.tail,
        head: expiredEdge.head,
        weight: 3,
        expiresAt: serverExpiredAt,
      ),
    );
    expect(await skewed.drain(partition), 0);
    for (final operationId in <String>[
      expiredVertex.operationId,
      expiredEdgeWrite.operationId,
    ]) {
      expect(
        (await skewed.getWriteStatus(
          partition,
          operationId,
        ))!.items.single.state,
        OfflineWriteState.expired,
      );
    }
    await expectLater(
      client.getVertex(expiredVertexKey),
      throwsA(isA<LanternNotFoundException>()),
    );
    await expectLater(
      client.getEdge(expiredEdge),
      throwsA(isA<LanternNotFoundException>()),
    );

    // A pending item that is wiped before replay owns no remote side effect.
    final wipedKey = '${prefix}wiped-before-send';
    await offline.putVertex(
      partitionId: partition,
      input: VertexInput(
        key: wipedKey,
        value: VertexValue.string('local-only'),
      ),
    );
    final watched = Completer<OfflineSnapshot<Vertex>>();
    final watch = offline.watchVertex(partition, wipedKey).listen((snapshot) {
      if (!watched.isCompleted) watched.complete(snapshot);
    });
    expect((await watched.future).hasPendingWrites, isTrue);
    await watch.cancel();
    await offline.wipePartition(partition);
    expect(await offline.listPending(partition), isEmpty);
    await expectLater(
      client.getVertex(wipedKey),
      throwsA(isA<LanternNotFoundException>()),
    );
    // ignore: avoid_print
    print(
      'MOBILE_SMOKE_PASS vertices=${inputs.length} edge=1 scan=true bfs=true '
      'offline_cache=true offline_replay=true authoritative_expiry=true '
      'watch_cleanup=true wipe_zero_send=true',
    );
  });
}
