// Run from sdks/dart/offline_sqlite with Flutter test against real h2c servers.
import 'dart:async';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

void main() {
  sqfliteFfiInit();

  final singleEndpoint =
      Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
  final gapEndpoint =
      Platform.environment['LANTERN_DART_IDENTITY_GAP_ENDPOINT'];
  final clusterEndpoints = Platform
      .environment['LANTERN_DART_IDENTITY_CLUSTER_ENDPOINTS']
      ?.split(',')
      .where((value) => value.isNotEmpty)
      .map(Uri.parse)
      .toList(growable: false);

  test(
    'identity stream revalidates SQLite residents and invalidates live Put',
    () async {
      final client = _client(Uri.parse(singleEndpoint!));
      addTearDown(client.close);
      await client.ping();
      final scope = 'identity-happy-${DateTime.now().microsecondsSinceEpoch}';
      final vertexKey = '$scope-vertex';
      final edge = EdgeRef('$scope-tail', '$scope-head');
      await client.putVertex(
        VertexInput(key: vertexKey, value: VertexValue.string('server-v1')),
      );
      await client.putEdge(
        EdgeInput(tail: edge.tail, head: edge.head, weight: 2.5),
      );
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      await fixture.putStaleVertex(vertexKey);
      await fixture.putStaleEdge(edge);
      final repository = fixture.repository(client);
      addTearDown(repository.dispose);
      final source = _WireSource(client);
      final cancellation = LanternCancellationToken();
      final run = repository.consumeIdentityChanges(
        'wire',
        source: source,
        cancellation: cancellation,
      );
      final stopped = expectLater(
        run,
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(cancellation.cancel);
      await _waitUntil(() async {
        final vertex = await repository.readVertex(
          'wire',
          vertexKey,
          policy: OfflineReadPolicy.cacheOnly,
        );
        final checkedEdge = await repository.readEdge(
          'wire',
          edge,
          policy: OfflineReadPolicy.cacheOnly,
        );
        return vertex.value?.value is StringValue &&
            (vertex.value!.value as StringValue).value == 'server-v1' &&
            checkedEdge.value?.weight == 2.5;
      });
      expect(source.opens.single.bootstrap, isTrue);
      expect(source.pluralVertexReads, greaterThan(0));
      expect(source.pluralEdgeReads, greaterThan(0));
      await client.putVertex(
        VertexInput(key: vertexKey, value: VertexValue.string('server-v2')),
      );
      await _waitUntil(
        () async =>
            (await repository.readVertex(
              'wire',
              vertexKey,
              policy: OfflineReadPolicy.cacheOnly,
            )).state ==
            OfflineReadState.unknown,
      );
      final vector = await fixture.store.transaction(
        (transaction) => transaction.changeCursor('wire'),
      );
      expect(vector.sequences, isNotEmpty);
      cancellation.cancel();
      await stopped;
    },
    skip: singleEndpoint == null ? 'real h2c endpoint unavailable' : false,
  );

  test(
    'production adapter bootstraps SQLite and catches live invalidation',
    () async {
      final client = _client(Uri.parse(singleEndpoint!));
      addTearDown(client.close);
      final key = 'identity-adapter-${DateTime.now().microsecondsSinceEpoch}';
      await client.putVertex(
        VertexInput(key: key, value: VertexValue.string('current')),
      );
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      await fixture.putStaleVertex(key);
      final repository = fixture.repository(client);
      addTearDown(repository.dispose);
      final cancellation = LanternCancellationToken();
      final run = repository.consumeIdentityChanges(
        'wire',
        source: LanternClientIdentitySource(client),
        cancellation: cancellation,
      );
      final stopped = expectLater(
        run,
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(cancellation.cancel);
      await _waitUntil(() async {
        final resident = await repository.readVertex(
          'wire',
          key,
          policy: OfflineReadPolicy.cacheOnly,
        );
        return resident.value?.value is StringValue &&
            (resident.value!.value as StringValue).value == 'current';
      });
      await client.putVertex(
        VertexInput(key: key, value: VertexValue.string('newer')),
      );
      await _waitUntil(
        () async =>
            (await repository.readVertex(
              'wire',
              key,
              policy: OfflineReadPolicy.cacheOnly,
            )).state ==
            OfflineReadState.unknown,
      );
      cancellation.cancel();
      await stopped;
    },
    skip: singleEndpoint == null ? 'real h2c endpoint unavailable' : false,
  );

  test(
    'evicted real-wire resume bootstraps SQLite Unknown recovery',
    () async {
      final client = _client(Uri.parse(gapEndpoint!));
      addTearDown(client.close);
      await client.ping();
      final key = 'identity-gap-${DateTime.now().microsecondsSinceEpoch}';
      await client.putVertex(
        VertexInput(key: key, value: VertexValue.string('before')),
      );
      final checkpoint =
          await client.subscribeIdentity(bootstrap: true).first
              as IdentityCheckpointFrame;
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      // Establish a durable completed cursor without invalidating this key.
      await fixture.store.transaction((transaction) async {
        await transaction.resetChangeCursor(
          'wire',
          OfflineChangeCursor(checkpoint.lastSequences),
        );
        await transaction.putCache('wire', fixture.staleRecord(key));
      });
      for (var index = 0; index < 4; index++) {
        await client.putVertex(
          VertexInput(
            key: index == 3 ? key : '$key-$index',
            value: VertexValue.string('after-$index'),
          ),
        );
      }
      final repository = fixture.repository(client);
      addTearDown(repository.dispose);
      final source = _WireSource(client);
      final cancellation = LanternCancellationToken();
      final run = repository.consumeIdentityChanges(
        'wire',
        source: source,
        cancellation: cancellation,
      );
      final stopped = expectLater(
        run,
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(cancellation.cancel);
      await _waitUntil(() => source.opens.length == 2);
      expect(source.opens.map((open) => open.bootstrap), [false, true]);
      for (final entry in checkpoint.lastSequences.entries) {
        expect(
          source.opens.first.nextExpected[entry.key],
          entry.value + BigInt.one,
        );
      }
      await _waitUntil(() async {
        final current = await repository.readVertex(
          'wire',
          key,
          policy: OfflineReadPolicy.cacheOnly,
        );
        return current.value?.value is StringValue &&
            (current.value!.value as StringValue).value == 'after-3';
      });
      cancellation.cancel();
      await stopped;
    },
    skip: gapEndpoint == null ? 'gap h2c endpoint unavailable' : false,
  );

  test(
    'production adapter recovers an evicted SQLite cursor',
    () async {
      final client = _client(Uri.parse(gapEndpoint!));
      addTearDown(client.close);
      final key =
          'identity-adapter-gap-${DateTime.now().microsecondsSinceEpoch}';
      await client.putVertex(
        VertexInput(key: key, value: VertexValue.string('before')),
      );
      final checkpoint =
          await client.subscribeIdentity(bootstrap: true).first
              as IdentityCheckpointFrame;
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      await fixture.store.transaction((transaction) async {
        await transaction.resetChangeCursor(
          'wire',
          OfflineChangeCursor(checkpoint.lastSequences),
        );
        await transaction.putCache('wire', fixture.staleRecord(key));
      });
      for (var index = 0; index < 4; index++) {
        await client.putVertex(
          VertexInput(
            key: index == 3 ? key : '$key-$index',
            value: VertexValue.string('after-$index'),
          ),
        );
      }
      final repository = fixture.repository(client);
      addTearDown(repository.dispose);
      final cancellation = LanternCancellationToken();
      final run = repository.consumeIdentityChanges(
        'wire',
        source: LanternClientIdentitySource(client),
        cancellation: cancellation,
      );
      final stopped = expectLater(
        run,
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(cancellation.cancel);
      await _waitUntil(() async {
        final current = await repository.readVertex(
          'wire',
          key,
          policy: OfflineReadPolicy.cacheOnly,
        );
        return current.value?.value is StringValue &&
            (current.value!.value as StringValue).value == 'after-3';
      });
      cancellation.cancel();
      await stopped;
    },
    skip: gapEndpoint == null ? 'gap h2c endpoint unavailable' : false,
  );

  test(
    'three origins converge and SQLite cursor resumes on another replica',
    () async {
      final endpoints = clusterEndpoints!;
      expect(endpoints, hasLength(3));
      final clients = [for (final endpoint in endpoints) _client(endpoint)];
      addTearDown(() async {
        for (final client in clients) {
          await client.close();
        }
      });
      for (final client in clients) {
        await client.ping();
      }
      final scope = 'identity-cluster-${DateTime.now().microsecondsSinceEpoch}';
      final keys = [for (var i = 0; i < 3; i++) '$scope-$i'];
      // Only the first origin exists at checkpoint time. The other two join
      // after bootstrap, exercising absent-origin -> sequence-one admission.
      await clients[0].putVertex(
        VertexInput(key: keys[0], value: VertexValue.string('initial-0')),
      );
      await _waitUntil(() async {
        try {
          for (final client in clients) {
            await client.getVertex(keys[0]);
          }
          return true;
        } on LanternException {
          return false;
        }
      }, timeout: const Duration(seconds: 25));
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      for (final key in keys) {
        await fixture.putStaleVertex(key);
      }
      var repository = fixture.repository(clients[0]);
      addTearDown(() => repository.dispose());
      final firstSource = _WireSource(clients[0]);
      final firstCancel = LanternCancellationToken();
      final firstRun = repository.consumeIdentityChanges(
        'wire',
        source: firstSource,
        cancellation: firstCancel,
      );
      final firstStopped = expectLater(
        firstRun,
        throwsA(isA<OfflineCanceledException>()),
      );
      await _waitUntil(
        () async =>
            (await fixture.store.transaction(
              (t) => t.unknownResidents('wire', limit: 3),
            )).isEmpty &&
            firstSource.pluralVertexReads > 0,
      );
      final checkpointCursor = await fixture.store.transaction(
        (transaction) => transaction.changeCursor('wire'),
      );
      expect(checkpointCursor.sequences.length, lessThan(3));
      for (var i = 0; i < 3; i++) {
        await clients[i].putVertex(
          VertexInput(key: keys[i], value: VertexValue.string('updated-$i')),
        );
      }
      await _waitUntil(() async {
        final vector = await fixture.store.transaction(
          (transaction) => transaction.changeCursor('wire'),
        );
        if (vector.sequences.length != 3) return false;
        for (final key in keys) {
          if ((await repository.readVertex(
                'wire',
                key,
                policy: OfflineReadPolicy.cacheOnly,
              )).state !=
              OfflineReadState.unknown)
            return false;
        }
        return true;
      }, timeout: const Duration(seconds: 25));
      final before = await fixture.store.transaction(
        (transaction) => transaction.changeCursor('wire'),
      );
      firstCancel.cancel();
      await firstStopped;
      await repository.dispose();
      await fixture.reopen();
      repository = fixture.repository(clients[2]);
      final secondSource = _WireSource(clients[2]);
      final secondCancel = LanternCancellationToken();
      final secondRun = repository.consumeIdentityChanges(
        'wire',
        source: secondSource,
        cancellation: secondCancel,
      );
      final secondStopped = expectLater(
        secondRun,
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(secondCancel.cancel);
      await _waitUntil(() => secondSource.opens.isNotEmpty);
      expect(secondSource.opens.single.bootstrap, isFalse);
      for (final entry in before.sequences.entries) {
        expect(
          secondSource.opens.single.nextExpected[entry.key],
          entry.value + BigInt.one,
        );
      }
      await clients[0].putVertex(
        VertexInput(key: keys[0], value: VertexValue.string('after-switch')),
      );
      await _waitUntil(() async {
        final cursor = await fixture.store.transaction(
          (transaction) => transaction.changeCursor('wire'),
        );
        return cursor.sequences.entries.any(
          (entry) => entry.value > (before.sequences[entry.key] ?? BigInt.zero),
        );
      }, timeout: const Duration(seconds: 25));
      secondCancel.cancel();
      await secondStopped;
    },
    skip: clusterEndpoints == null || clusterEndpoints.length != 3
        ? 'three h2c replica endpoints unavailable'
        : false,
    timeout: const Timeout(Duration(minutes: 2)),
  );
}

LanternClient _client(Uri endpoint) => LanternClient.connect(
  endpoint,
  allowInsecure: endpoint.scheme == 'http',
  defaultTimeout: const Duration(seconds: 5),
);

Future<void> _waitUntil(
  FutureOr<bool> Function() ready, {
  Duration timeout = const Duration(seconds: 12),
}) async {
  final deadline = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(deadline)) {
    if (await ready()) return;
    await Future<void>.delayed(const Duration(milliseconds: 25));
  }
  fail('bounded real-wire condition did not become true');
}

final class _SqliteFixture {
  _SqliteFixture(this.directory, this.path, this.store);

  static Future<_SqliteFixture> open() async {
    final directory = await Directory.systemTemp.createTemp('identity-sqlite-');
    final path = '${directory.path}/offline.db';
    final store = await SqliteOfflineStore.open(
      path: path,
      databaseFactory: databaseFactoryFfi,
    );
    return _SqliteFixture(directory, path, store);
  }

  final Directory directory;
  final String path;
  SqliteOfflineStore store;

  OfflineLanternRepository repository(LanternClient client) =>
      OfflineLanternRepository(
        store: store,
        remote: LanternClientOfflineRemote(client),
      );

  OfflineCacheRecord staleRecord(String key) {
    final now = DateTime.now().toUtc();
    return OfflineCacheRecord.value(
      partitionId: 'wire',
      generation: 0,
      key: OfflineEntityKey.vertex(key),
      entity: Vertex(
        key: key,
        value: VertexValue.string('stale'),
        expiration: null,
      ),
      validatedAt: now,
      lastAccessAt: now,
    );
  }

  Future<void> putStaleVertex(String key) => store.transaction(
    (transaction) => transaction.putCache('wire', staleRecord(key)),
  );

  Future<void> putStaleEdge(EdgeRef edge) {
    final now = DateTime.now().toUtc();
    return store.transaction(
      (transaction) => transaction.putCache(
        'wire',
        OfflineCacheRecord.value(
          partitionId: 'wire',
          generation: 0,
          key: OfflineEntityKey.edge(edge.tail, edge.head),
          entity: Edge(
            tail: edge.tail,
            head: edge.head,
            weight: 0.5,
            expiration: null,
          ),
          validatedAt: now,
          lastAccessAt: now,
        ),
      ),
    );
  }

  Future<void> reopen() async {
    await store.close();
    store = await SqliteOfflineStore.open(
      path: path,
      databaseFactory: databaseFactoryFfi,
    );
  }

  Future<void> close() async {
    await store.close();
    await directory.delete(recursive: true);
  }
}

final class _WireOpen {
  const _WireOpen(this.bootstrap, this.nextExpected);
  final bool bootstrap;
  final Map<String, BigInt> nextExpected;
}

/// Test-only adapter. A production adapter waits for a separately published
/// parent SDK that includes the typed identity stream.
final class _WireSource implements OfflineIdentitySource {
  _WireSource(this.client);

  final LanternClient client;
  final List<_WireOpen> opens = [];
  int pluralVertexReads = 0;
  int pluralEdgeReads = 0;

  @override
  Future<OfflineIdentitySession> open({
    required bool bootstrap,
    required Map<String, BigInt> nextExpected,
    required LanternCancellationToken cancellation,
  }) async {
    opens.add(_WireOpen(bootstrap, Map.of(nextExpected)));
    return _WireSession(this, bootstrap, nextExpected, cancellation);
  }
}

final class _WireSession implements OfflineIdentitySession {
  _WireSession(
    this.source,
    this.bootstrap,
    this.nextExpected,
    this.cancellation,
  );

  final _WireSource source;
  final bool bootstrap;
  final Map<String, BigInt> nextExpected;
  final LanternCancellationToken cancellation;
  late final LanternClientOfflineRemote reads = LanternClientOfflineRemote(
    source.client,
  );

  @override
  String get responderId => source.client.endpoint.toString();

  @override
  Stream<OfflineIdentityEvent> get events => source.client
      .subscribeIdentity(
        bootstrap: bootstrap,
        cursor: bootstrap ? null : IdentityNextCursor(nextExpected),
        options: LanternCallOptions(cancellation: cancellation),
      )
      .transform(
        StreamTransformer<IdentityFrame, OfflineIdentityEvent>.fromHandlers(
          handleData: (frame, sink) {
            switch (frame) {
              case IdentityCheckpointFrame():
                sink.add(OfflineIdentityCheckpoint(frame.lastSequences));
              case IdentityChunkFrame():
                sink.add(
                  OfflineIdentityChunk(
                    origin: frame.origin,
                    sequence: frame.sequence,
                    operation: switch (frame.operation) {
                      IdentityOperation.putVertex =>
                        OfflineIdentityOperation.putVertex,
                      IdentityOperation.deleteVertex =>
                        OfflineIdentityOperation.deleteVertex,
                      IdentityOperation.addEdge =>
                        OfflineIdentityOperation.addEdge,
                      IdentityOperation.putEdge =>
                        OfflineIdentityOperation.putEdge,
                      IdentityOperation.deleteEdge =>
                        OfflineIdentityOperation.deleteEdge,
                      IdentityOperation.receiptOnly =>
                        OfflineIdentityOperation.receiptOnly,
                    },
                    chunkIndex: frame.chunkIndex,
                    isLast: frame.isLast,
                    firstItemIndex: frame.firstItemIndex,
                    keys: [
                      for (final key in frame.vertexKeys)
                        OfflineEntityKey.vertex(key),
                      for (final edge in frame.edgeKeys)
                        OfflineEntityKey.edge(edge.tail, edge.head),
                    ],
                  ),
                );
            }
          },
          handleError: (error, stack, sink) {
            if (error is LanternFailedPreconditionException) {
              sink.addError(const OfflineChangeGapException(), stack);
            } else {
              sink.addError(mapLanternClientFailure(error), stack);
            }
          },
        ),
      );

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) {
    source.pluralVertexReads++;
    return reads.getVertices(keys, cancellation: cancellation);
  }

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) {
    source.pluralEdgeReads++;
    return reads.getEdges(edges, cancellation: cancellation);
  }

  @override
  Future<void> close() async {}
}
