import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite/sqflite.dart' as sqflite;

import 'support/physical_result_marker.dart';

void main() {
  final binding = IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  // A directly launched iOS test can receive the platform's semantics request
  // after testWidgets records its leak-check baseline. Wait for that request
  // only in the direct-launch CI path, before the first test begins.
  if (Platform.isIOS &&
      const bool.fromEnvironment('LANTERN_IOS_DIRECT_LAUNCH')) {
    setUpAll(() async {
      final deadline = DateTime.now().add(const Duration(seconds: 10));
      while (!binding.platformDispatcher.semanticsEnabled ||
          binding.debugOutstandingSemanticsHandles == 0) {
        if (DateTime.now().isAfter(deadline)) {
          fail('iOS platform semantics did not initialize before the test');
        }
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
    });
  }

  testWidgets('native mobile real-wire smoke', (tester) async {
    final result = PhysicalResultMarker(
      'lantern-mobile-smoke-result.json',
      kind: 'physical_mobile_smoke_on_device_result',
    );
    await result.recordPhase('setup');
    var bodyPassed = false;
    // Registered first so this runs after all tracked cleanup callbacks.
    addTearDown(() async {
      if (!bodyPassed) {
        await result.recordOutcome('failed', result.phase, failureType: 'body');
        return;
      }
      final cleanupFailureType = result.cleanupFailureType;
      if (cleanupFailureType != null) {
        await result.recordOutcome(
          'failed',
          'cleanup',
          failureType: cleanupFailureType,
        );
      } else {
        await result.recordOutcome('passed', 'complete');
      }
    });
    // This explicit phase marker lets CI distinguish an app-launch stall from
    // an assertion or RPC failure after the integration test body has begun.
    // ignore: avoid_print
    print('MOBILE_SMOKE_BODY_STARTED');
    const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
    expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
    final endpoint = Uri.parse(endpointValue);
    const tokenEndpoint = String.fromEnvironment('LANTERN_TOKEN_ENDPOINT');
    final tokenHttp = HttpClient()
      ..connectionTimeout = const Duration(seconds: 5);
    result.addTrackedTearDown(() => tokenHttp.close(force: true));
    final client = LanternClient.connect(
      endpoint,
      tokenProvider: tokenEndpoint.isEmpty
          ? null
          : () async {
              final uri = Uri.parse(tokenEndpoint);
              if (uri.scheme != 'https' &&
                  !const bool.fromEnvironment('LANTERN_ALLOW_INSECURE')) {
                throw StateError('smoke_token_https_required');
              }
              final request = await tokenHttp
                  .getUrl(uri)
                  .timeout(const Duration(seconds: 5));
              final response = await request.close().timeout(
                const Duration(seconds: 5),
              );
              final body = await utf8
                  .decodeStream(response)
                  .timeout(const Duration(seconds: 5));
              if (response.statusCode != HttpStatus.ok) {
                throw StateError('smoke_token_status');
              }
              final decoded = jsonDecode(body) as Map<String, Object?>;
              final token = decoded['access_token'];
              if (token is! String || token.isEmpty) {
                throw StateError('smoke_token_missing');
              }
              return token;
            },
      allowInsecure: const bool.fromEnvironment('LANTERN_ALLOW_INSECURE'),
      retryPolicy: const RetryPolicy(),
      idempotentAdds: true,
    );
    result.addTrackedTearDown(client.close);
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
    final databaseRoot = Directory(await sqflite.getDatabasesPath());
    await databaseRoot.create(recursive: true);
    final databaseDirectory = await databaseRoot.createTemp(
      'lantern-native-smoke-',
    );
    result.addTrackedTearDown(() => databaseDirectory.delete(recursive: true));
    final databasePath = '${databaseDirectory.path}/offline.db';
    var store = await SqliteOfflineStore.open(path: databasePath);
    var offlineNow = DateTime.now().toUtc();
    var offline = OfflineLanternRepository(
      store: store,
      remote: LanternClientOfflineRemote(client),
      config: OfflineConfig(clock: () => offlineNow),
    );
    result.addTrackedTearDown(() async {
      await offline.dispose();
      await store.close();
    });
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

    final localExpiredKey = '${prefix}expired-before-reopen';
    final localExpired = await offline.putVertex(
      partitionId: partition,
      input: VertexInput(
        key: localExpiredKey,
        value: VertexValue.string('never-send'),
        expiresIn: const Duration(seconds: 1),
      ),
    );
    final vertexExpiration = pendingVertex.value!.expiration;
    final edgeExpiration = pendingEdge.value!.expiration;
    await offline.dispose();
    await store.close();
    offlineNow = offlineNow.add(const Duration(seconds: 30));
    store = await SqliteOfflineStore.open(path: databasePath);
    offline = OfflineLanternRepository(
      store: store,
      remote: LanternClientOfflineRemote(client),
      config: OfflineConfig(clock: () => offlineNow),
    );
    final reopenedVertex = await offline.readVertex(
      partition,
      offlineVertexKey,
      policy: OfflineReadPolicy.cacheOnly,
    );
    final reopenedEdge = await offline.readEdge(
      partition,
      EdgeRef(offlineEdge.tail, offlineEdge.head),
      policy: OfflineReadPolicy.cacheOnly,
    );
    expect(reopenedVertex.hasPendingWrites, isTrue);
    expect(
      (reopenedVertex.value!.value as StringValue).value,
      'queued-offline',
    );
    expect(reopenedVertex.value!.expiration, vertexExpiration);
    expect(reopenedEdge.hasPendingWrites, isTrue);
    expect(reopenedEdge.value!.expiration, edgeExpiration);
    expect(
      (await offline.readVertex(
        partition,
        localExpiredKey,
        policy: OfflineReadPolicy.cacheOnly,
      )).value,
      isNull,
    );

    expect(await offline.probeAndDrain(partition), 2);
    for (final operationId in [put.operationId, edgePut.operationId]) {
      expect(
        (await offline.getWriteStatus(
          partition,
          operationId,
        ))!.items.single.state,
        OfflineWriteState.confirmed,
      );
    }
    expect(
      (await offline.getWriteStatus(
        partition,
        localExpired.operationId,
      ))!.items.single.state,
      OfflineWriteState.expired,
    );
    await expectLater(
      client.getVertex(localExpiredKey),
      throwsA(isA<LanternNotFoundException>()),
    );
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
    final skewedStore = await SqliteOfflineStore.open(
      path: '${databaseDirectory.path}/skewed.db',
    );
    final skewed = OfflineLanternRepository(
      store: skewedStore,
      remote: LanternClientOfflineRemote(client),
      config: OfflineConfig(clock: () => skewedNow),
    );
    result.addTrackedTearDown(() async {
      await skewed.dispose();
      await skewedStore.close();
    });
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
    const otherPartition = 'other-mobile-session';
    final otherKey = '${prefix}other-session';
    await offline.putVertex(
      partitionId: otherPartition,
      input: VertexInput(key: otherKey, value: VertexValue.string('retained')),
    );
    await offline.wipePartition(partition);
    await offline.dispose();
    await store.close();
    store = await SqliteOfflineStore.open(path: databasePath);
    offline = OfflineLanternRepository(
      store: store,
      remote: LanternClientOfflineRemote(client),
    );
    expect(await offline.listPending(partition), isEmpty);
    expect(await offline.getWriteStatus(partition, put.operationId), isNull);
    expect(
      (await offline.readVertex(
        partition,
        offlineVertexKey,
        policy: OfflineReadPolicy.cacheOnly,
      )).value,
      isNull,
    );
    expect(
      (await offline.readVertex(
        otherPartition,
        otherKey,
        policy: OfflineReadPolicy.cacheOnly,
      )).hasPendingWrites,
      isTrue,
    );
    expect(await offline.probeAndDrain(partition), 0);
    await expectLater(
      client.getVertex(wipedKey),
      throwsA(isA<LanternNotFoundException>()),
    );
    // ignore: avoid_print
    print(
      'MOBILE_SMOKE_PASS vertices=${inputs.length} edge=1 scan=true bfs=true '
      'offline_cache=true offline_replay=true authoritative_expiry=true '
      'watch_cleanup=true wipe_zero_send=true sqlite_reopen=true '
      'ttl_preserved=true logout_wipe_persisted=true partition_isolation=true',
    );
    await result.recordPhase('cleanup');
    bodyPassed = true;
  });
}
