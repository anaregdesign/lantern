import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:connectrpc/connect.dart' as connect;
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite/sqflite.dart' as sqflite;

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  final result = _PhysicalResultMarker();

  _recordPhysicalTest(
    'physical pinned identity CDC recovers native SQLite residents',
    result,
    () async {
      // ignore: avoid_print
      print('IDENTITY_CDC_BODY_STARTED');
      const endpointValue = String.fromEnvironment('LANTERN_ENDPOINT');
      const tokenEndpointValue = String.fromEnvironment(
        'LANTERN_TOKEN_ENDPOINT',
      );
      const pinned = bool.fromEnvironment(
        'LANTERN_OFFLINE_CDC_PINNED_RESPONDER',
      );
      const allowInsecure = bool.fromEnvironment('LANTERN_ALLOW_INSECURE');
      expect(endpointValue, isNotEmpty, reason: 'pass LANTERN_ENDPOINT');
      expect(
        tokenEndpointValue,
        isNotEmpty,
        reason: 'pass LANTERN_TOKEN_ENDPOINT',
      );
      expect(pinned, isTrue, reason: 'prove endpoint routes to one responder');
      final endpoint = Uri.parse(endpointValue);
      final tokenEndpoint = Uri.parse(tokenEndpointValue);
      if (!allowInsecure) {
        expect(endpoint.scheme, 'https');
        expect(tokenEndpoint.scheme, 'https');
      }
      await result.recordPhase('authenticated_rpc');

      var tokenFetches = 0;
      String? previousIssuedToken;
      String? cachedToken;
      Future<String>? tokenInFlight;
      Future<String> acquireToken() async {
        // Use a fresh connection for each actual refresh. The token provider
        // retains the result until the foreground owner requests renewal.
        final tokenHttp = HttpClient()
          ..connectionTimeout = const Duration(seconds: 10);
        try {
          final request = await tokenHttp
              .getUrl(tokenEndpoint)
              .timeout(const Duration(seconds: 10));
          request.persistentConnection = false;
          final response = await request.close().timeout(
            const Duration(seconds: 10),
          );
          final body = await utf8
              .decodeStream(response)
              .timeout(const Duration(seconds: 10));
          if (response.statusCode != HttpStatus.ok) {
            throw StateError('identity_token_status');
          }
          final decoded = jsonDecode(body);
          if (decoded is! Map<String, Object?> ||
              decoded['access_token'] is! String ||
              (decoded['access_token']! as String).isEmpty) {
            throw StateError('identity_token_missing');
          }
          final issuedToken = decoded['access_token']! as String;
          if (previousIssuedToken == issuedToken) {
            throw StateError('identity_token_not_rotated');
          }
          previousIssuedToken = issuedToken;
          tokenFetches++;
          return issuedToken;
        } finally {
          tokenHttp.close(force: true);
        }
      }

      Future<String> fetchToken() async {
        final cached = cachedToken;
        if (cached != null) return cached;
        final pending = tokenInFlight ??= acquireToken();
        try {
          return cachedToken = await pending;
        } finally {
          if (identical(tokenInFlight, pending)) tokenInFlight = null;
        }
      }

      final client = LanternClient.connect(
        endpoint,
        tokenProvider: fetchToken,
        allowInsecure: allowInsecure,
        defaultTimeout: const Duration(seconds: 20),
      );
      result.addTrackedTearDown(client.close);
      await client.ping();
      final responder = (await client.getReplicationStatus()).nodeId;
      expect(responder, matches(RegExp(r'^[0-9a-f]{32}$')));
      await result.recordPhase('seed_remote');

      final unique = DateTime.now().microsecondsSinceEpoch;
      final vertexKey = 'physical-cdc:$unique:vertex';
      final edge = EdgeRef(
        'physical-cdc:$unique:tail',
        'physical-cdc:$unique:head',
      );
      await client.putVertex(
        VertexInput(
          key: vertexKey,
          value: VertexValue.string('server-v1'),
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await client.putEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 1,
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await result.recordPhase('seed_sqlite');

      final databaseRoot = Directory(await sqflite.getDatabasesPath());
      await databaseRoot.create(recursive: true);
      final directory = await databaseRoot.createTemp('lantern-identity-cdc-');
      result.addTrackedTearDown(() => directory.delete(recursive: true));
      final path = '${directory.path}/offline.db';
      var store = await SqliteOfflineStore.open(path: path);
      var repository = OfflineLanternRepository(
        store: store,
        remote: LanternClientOfflineRemote(client),
      );
      result.addTrackedTearDown(() async {
        await repository.dispose();
        await store.close();
      });
      const partition = 'physical-identity-cdc';
      final now = DateTime.now().toUtc();
      await store.transaction((transaction) async {
        await transaction.putCache(
          partition,
          OfflineCacheRecord.value(
            partitionId: partition,
            generation: 0,
            key: OfflineEntityKey.vertex(vertexKey),
            entity: Vertex(
              key: vertexKey,
              value: VertexValue.string('stale'),
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
        await transaction.putCache(
          partition,
          OfflineCacheRecord.value(
            partitionId: partition,
            generation: 0,
            key: OfflineEntityKey.edge(edge.tail, edge.head),
            entity: Edge(
              tail: edge.tail,
              head: edge.head,
              weight: 0.25,
              expiration: null,
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
      });
      await result.recordPhase('checkpoint_revalidation');

      final source = LanternClientIdentitySource(client);
      final foreground = LanternCancellationToken();
      final firstRun = repository.consumeIdentityChanges(
        partition,
        source: source,
        cancellation: foreground,
      );
      final firstStopped = expectLater(
        _diagnoseFailure(firstRun, 'first'),
        throwsA(isA<OfflineCanceledException>()),
      );
      result.addTrackedTearDown(foreground.cancel);
      await _waitUntil(() async {
        final vertex = await repository.readVertex(
          partition,
          vertexKey,
          policy: OfflineReadPolicy.cacheOnly,
        );
        final checkedEdge = await repository.readEdge(
          partition,
          edge,
          policy: OfflineReadPolicy.cacheOnly,
        );
        return vertex.value?.value is StringValue &&
            (vertex.value!.value as StringValue).value == 'server-v1' &&
            checkedEdge.value?.weight == 1;
      });
      final initialCursor = await store.transaction(
        (transaction) => transaction.changeCursor(partition),
      );
      expect(initialCursor.sequences[responder], isNotNull);
      await result.recordPhase('live_invalidation');
      final tokenFetchesAtCheckpoint = tokenFetches;
      cachedToken = null;

      await client.putVertex(
        VertexInput(
          key: vertexKey,
          value: VertexValue.string('server-v2'),
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await client.putEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 2,
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await _waitUntil(() async {
        final vertex = await repository.readVertex(
          partition,
          vertexKey,
          policy: OfflineReadPolicy.cacheOnly,
        );
        final checkedEdge = await repository.readEdge(
          partition,
          edge,
          policy: OfflineReadPolicy.cacheOnly,
        );
        return vertex.state == OfflineReadState.unknown &&
            checkedEdge.state == OfflineReadState.unknown;
      });
      final liveCursor = await store.transaction(
        (transaction) => transaction.changeCursor(partition),
      );
      expect(
        liveCursor.sequences[responder],
        greaterThan(initialCursor.sequences[responder]!),
      );
      expect(tokenFetches, greaterThan(tokenFetchesAtCheckpoint));
      foreground.cancel();
      await firstStopped;
      await result.recordPhase('sqlite_reopen');

      await repository.dispose();
      await store.close();
      store = await SqliteOfflineStore.open(path: path);
      repository = OfflineLanternRepository(
        store: store,
        remote: LanternClientOfflineRemote(client),
      );
      expect(
        (await store.transaction(
          (transaction) => transaction.changeCursor(partition),
        )).sequences,
        liveCursor.sequences,
      );
      expect(
        (await repository.readVertex(
          partition,
          vertexKey,
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
      expect(
        (await repository.readEdge(
          partition,
          edge,
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
      // A live chunk removes confirmed rows. Explicit reads then refill the
      // cache; checkpoint recovery markers are a separate gap path.
      expect(
        ((await repository.readVertex(
                  partition,
                  vertexKey,
                  policy: OfflineReadPolicy.serverOnly,
                )).value?.value
                as StringValue?)
            ?.value,
        'server-v2',
      );
      expect(
        (await repository.readEdge(
          partition,
          edge,
          policy: OfflineReadPolicy.serverOnly,
        )).value?.weight,
        2,
      );
      await result.recordPhase('foreground_resume');

      final resumed = LanternCancellationToken();
      result.addTrackedTearDown(resumed.cancel);
      final secondRun = repository.consumeIdentityChanges(
        partition,
        source: source,
        cancellation: resumed,
      );
      final secondStopped = expectLater(
        _diagnoseFailure(secondRun, 'second'),
        throwsA(isA<OfflineCanceledException>()),
      );
      await client.putVertex(
        VertexInput(
          key: vertexKey,
          value: VertexValue.string('server-v3'),
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await client.putEdge(
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 3,
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await _waitUntil(() async {
        final vertex = await repository.readVertex(
          partition,
          vertexKey,
          policy: OfflineReadPolicy.cacheOnly,
        );
        final checkedEdge = await repository.readEdge(
          partition,
          edge,
          policy: OfflineReadPolicy.cacheOnly,
        );
        return vertex.state == OfflineReadState.unknown &&
            checkedEdge.state == OfflineReadState.unknown;
      });
      expect((await client.getReplicationStatus()).nodeId, responder);
      await result.recordPhase('partition_wipe');
      await repository.wipePartition(partition);
      await secondStopped;
      expect(
        (await store.transaction(
          (transaction) => transaction.changeCursor(partition),
        )).sequences,
        isEmpty,
      );
      expect(
        (await repository.readVertex(
          partition,
          vertexKey,
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.unknown,
      );
      // Content-free marker for an exact-code physical Android/iOS record.
      // ignore: avoid_print
      print(
        'IDENTITY_CDC_PASS checkpoint=true live_vertex=true live_edge=true '
        'durable_unknown=true resume=true wipe=true responder_stable=true '
        'token_refresh=true',
      );
    },
  );
}

void _recordPhysicalTest(
  String description,
  _PhysicalResultMarker result,
  Future<void> Function() body,
) {
  test(description, () async {
    await result.recordPhase('setup');
    var bodyPassed = false;
    // addTearDown runs in reverse registration order. This finalizer therefore
    // sees all test-owned cleanup callbacks before recording a pass.
    addTearDown(() async {
      if (!bodyPassed) return;
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
    try {
      await body();
      await result.recordPhase('cleanup');
      bodyPassed = true;
    } catch (error) {
      try {
        await result.recordOutcome(
          'failed',
          result.phase,
          failureType: _safeFailureType(error),
        );
      } catch (_) {
        // Preserve the test failure when the diagnostic file cannot be written.
      }
      rethrow;
    }
  });
}

String _safeFailureType(Object error) => switch (error) {
  TestFailure() || AssertionError() => 'assertion',
  TimeoutException() => 'timeout',
  SocketException() || HandshakeException() => 'network',
  OfflineRemoteFailure() || connect.ConnectException() => 'remote',
  FileSystemException() => 'filesystem',
  FormatException() || TypeError() => 'format',
  StateError() => 'state',
  _ => 'other',
};

class _PhysicalResultMarker {
  _PhysicalResultMarker()
    : _startedAt = DateTime.now().toUtc(),
      _file = File(
        '${Directory.systemTemp.path}/lantern-identity-cdc-result.json',
      );

  final DateTime _startedAt;
  final File _file;
  String phase = 'setup';
  String? cleanupFailureType;

  void addTrackedTearDown(FutureOr<dynamic> Function() cleanup) {
    addTearDown(() async {
      try {
        await cleanup();
      } catch (error) {
        cleanupFailureType ??= _safeFailureType(error);
        rethrow;
      }
    });
  }

  Future<void> recordPhase(String nextPhase) =>
      recordOutcome('running', nextPhase);

  Future<void> recordOutcome(
    String status,
    String nextPhase, {
    String? failureType,
  }) async {
    phase = nextPhase;
    final fields = <String, Object>{
      'schema': 1,
      'kind': 'physical_identity_cdc_on_device_result',
      'contentFree': true,
      'status': status,
      'phase': phase,
      'startedAt': _startedAt.toIso8601String(),
      'updatedAt': DateTime.now().toUtc().toIso8601String(),
    };
    if (failureType != null) fields['failureType'] = failureType;
    final encoded = jsonEncode(fields);
    final temporaryFile = File('${_file.path}.tmp');
    await temporaryFile.writeAsString(encoded, flush: true);
    await temporaryFile.rename(_file.path);
  }
}

Future<void> _waitUntil(FutureOr<bool> Function() ready) async {
  final deadline = DateTime.now().add(const Duration(seconds: 60));
  while (DateTime.now().isBefore(deadline)) {
    if (await ready()) return;
    await Future<void>.delayed(const Duration(milliseconds: 25));
  }
  fail('bounded physical identity CDC condition did not become true');
}

Future<void> _diagnoseFailure(Future<void> run, String phase) async {
  try {
    await run;
  } on OfflineRemoteFailure catch (error) {
    final sdkCause = error.cause;
    final transportCause = sdkCause is LanternException ? sdkCause.cause : null;
    final socketCause = transportCause is connect.ConnectException
        ? transportCause.cause
        : null;
    printOnFailure(
      'identity CDC $phase failure: ${error.kind.name} '
      'cause=${sdkCause.runtimeType} '
      'transport=${transportCause.runtimeType} '
      'socket=${socketCause.runtimeType}',
    );
    rethrow;
  }
}
