// Run from sdks/dart/offline_sqlite over public CDC and native private replicas.
import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import '../../sdks/dart/example/lib/scoped_change_source.dart';

void main() {
  sqfliteFfiInit();

  final singleEndpoint =
      Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
  final receiptEndpoint = Platform.environment['LANTERN_DART_RECEIPT_ENDPOINT'];
  final receiptToken = Platform.environment['LANTERN_DART_RECEIPT_TOKEN'];
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
      final run = repository.consumeScopedChanges(
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
        (transaction) => transaction.scopedChangeCursor('wire'),
      );
      expect(vector, isNotNull);
      cancellation.cancel();
      await stopped;
    },
    skip: singleEndpoint == null ? 'real h2c endpoint unavailable' : false,
  );

  test(
    'production offline contribution Delete replays through SQLite and CDC',
    () async {
      final endpoint = Uri.parse(receiptEndpoint!);
      final client = _client(endpoint, token: receiptToken);
      addTearDown(client.close);
      final prefix =
          'identity-contribution-${DateTime.now().microsecondsSinceEpoch}';
      final edge = EdgeRef('$prefix-t', '$prefix-h');
      EdgeContributionRef target(int id) => EdgeContributionRef(
        tail: edge.tail,
        head: edge.head,
        contribId: Uint8List(24)..[23] = id,
      );
      await client.putEdge(
        EdgeInput(tail: edge.tail, head: edge.head, weight: 1),
      );
      await client.addEdges([
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 2,
          contribId: target(1).contribId,
        ),
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 3,
          contribId: target(2).contribId,
        ),
        EdgeInput(
          tail: edge.tail,
          head: edge.head,
          weight: 7,
          contribId: target(3).contribId,
          expiresAt: DateTime.utc(2000),
        ),
      ]);
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      var repository = fixture.repository(client);
      await repository.readEdge(
        'wire',
        edge,
        policy: OfflineReadPolicy.serverOnly,
      );
      final operation = await repository.deleteEdgeContributions(
        partitionId: 'wire',
        contributions: [target(1), target(1), target(9), target(3)],
        operationId: 'targeted-contribution',
      );
      final before = await fixture.store.transaction(
        (tx) async => await tx.outbox('wire'),
      );
      final pending = await repository.readEdge(
        'wire',
        edge,
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(pending.value!.weight, 6);
      expect(pending.hasPendingWrites, isTrue);
      await repository.dispose();
      await fixture.reopen();
      repository = fixture.repository(client);
      addTearDown(() => repository.dispose());
      final reopened = await fixture.store.transaction(
        (tx) async => await tx.outbox('wire'),
      );
      expect(
        reopened.map((item) => item.receipt!.operationId),
        before.map((item) => item.receipt!.operationId),
      );
      expect(
        reopened.map(
          (item) => (item.intent as OfflineDeleteEdgeContributionIntent)
              .contribution
              .contribId,
        ),
        before.map(
          (item) => (item.intent as OfflineDeleteEdgeContributionIntent)
              .contribution
              .contribId,
        ),
      );
      expect(await repository.drain('wire'), 4);
      final status = await repository.getWriteStatus(
        'wire',
        operation.operationId,
      );
      expect(
        status!.items.map(
          (item) =>
              (item.receiptResult as OfflineEdgeContributionDeleteReceiptResult)
                  .existed,
        ),
        [true, false, false, false],
      );
      expect((await repository.readEdge('wire', edge)).value!.weight, 4);
      final cancellation = LanternCancellationToken();
      final run = repository.consumeScopedChanges(
        'wire',
        source: LanternScopedChangeSource(
          client: client,
          responderId: client.endpoint.toString(),
        ),
        cancellation: cancellation,
      );
      final stopped = expectLater(
        run,
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(cancellation.cancel);
      try {
        await _waitUntil(
          () async =>
              (await fixture.store.transaction(
                (tx) => tx.scopedChangeCursor('wire'),
              )) !=
              null,
        );
        await client.deleteEdgeContributions([target(2), target(2)]);
        await _waitUntil(
          () async =>
              (await repository.readEdge(
                'wire',
                edge,
                policy: OfflineReadPolicy.cacheOnly,
              )).state ==
              OfflineReadState.unknown,
        );
        expect(
          (await repository.readEdge(
            'wire',
            edge,
            policy: OfflineReadPolicy.serverOnly,
          )).value!.weight,
          1,
        );
      } finally {
        cancellation.cancel();
        await stopped;
      }
      final cursor = await fixture.store.transaction(
        (tx) => tx.scopedChangeCursor('wire'),
      );
      await repository.dispose();
      await fixture.reopen();
      repository = fixture.repository(client);
      expect(
        (await fixture.store.transaction(
          (tx) => tx.scopedChangeCursor('wire'),
        ))?.toBytes(),
        cursor?.toBytes(),
      );
      final retained = await repository.getWriteStatus(
        'wire',
        operation.operationId,
      );
      expect(
        retained!.items.map(
          (item) =>
              (item.receiptResult as OfflineEdgeContributionDeleteReceiptResult)
                  .existed,
        ),
        [true, false, false, false],
      );
      expect(
        (await repository.readEdge(
          'wire',
          edge,
          policy: OfflineReadPolicy.cacheOnly,
        )).value!.weight,
        1,
      );
      await client.deleteEdge(edge);
    },
    skip: receiptEndpoint == null
        ? 'authenticated receipt endpoint unavailable'
        : false,
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
      final run = repository.consumeScopedChanges(
        'wire',
        source: LanternScopedChangeSource(
          client: client,
          responderId: client.endpoint.toString(),
        ),
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
      final checkpoint = await client
          .watchChanges(bootstrap: true, projection: ChangeProjection.identity)
          .first;
      expect(checkpoint.bootstrap, isTrue);
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      // Establish a durable completed cursor without invalidating this key.
      await fixture.store.transaction((transaction) async {
        await transaction.resetScopedChangeCursor(
          'wire',
          OfflineScopedChangeCursor(checkpoint.cursor!.toBytes()),
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
      final run = repository.consumeScopedChanges(
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
      expect(
        source.opens.first.cursor!.toBytes(),
        checkpoint.cursor!.toBytes(),
      );
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
      final checkpoint = await client
          .watchChanges(bootstrap: true, projection: ChangeProjection.identity)
          .first;
      expect(checkpoint.bootstrap, isTrue);
      final fixture = await _SqliteFixture.open();
      addTearDown(fixture.close);
      await fixture.store.transaction((transaction) async {
        await transaction.resetScopedChangeCursor(
          'wire',
          OfflineScopedChangeCursor(checkpoint.cursor!.toBytes()),
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
      final run = repository.consumeScopedChanges(
        'wire',
        source: LanternScopedChangeSource(
          client: client,
          responderId: client.endpoint.toString(),
        ),
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
    'native replicas converge and compatible responder resumes opaque SQLite state',
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
      final firstStopped = expectLater(
        repository.consumeScopedChanges(
          'wire',
          source: firstSource,
          cancellation: firstCancel,
        ),
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(firstCancel.cancel);
      await _waitUntil(
        () async =>
            (await fixture.store.transaction(
              (t) => t.unknownResidents('wire', limit: 3),
            )).isEmpty &&
            firstSource.pluralVertexReads > 0,
      );
      final checkpoint = await fixture.store.transaction(
        (t) => t.scopedChangeCursor('wire'),
      );
      expect(checkpoint, isNotNull);
      for (var i = 0; i < 3; i++) {
        await clients[i].putVertex(
          VertexInput(key: keys[i], value: VertexValue.string('updated-$i')),
        );
      }
      await _waitUntil(() async {
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
      // Prove data convergence through public reads, without exposing origins.
      await _waitUntil(() async {
        try {
          for (final client in clients) {
            for (var i = 0; i < 3; i++) {
              final vertex = await client.getVertex(keys[i]);
              if ((vertex.value as StringValue).value != 'updated-$i')
                return false;
            }
          }
          return true;
        } on LanternException {
          return false;
        }
      }, timeout: const Duration(seconds: 25));
      // Compatible nodes share the cursor ring and authorization binding.
      // Resume succeeds only when the new node proves contiguous retained history.
      for (final key in keys) {
        await repository.readVertex(
          'wire',
          key,
          policy: OfflineReadPolicy.serverOnly,
        );
      }
      final before = await fixture.store.transaction(
        (t) => t.scopedChangeCursor('wire'),
      );
      firstCancel.cancel();
      await firstStopped;
      await repository.dispose();
      await fixture.reopen();
      expect(
        (await fixture.store.transaction(
          (t) => t.scopedChangeCursor('wire'),
        ))!.toBytes(),
        before!.toBytes(),
      );
      repository = fixture.repository(clients[2]);
      final secondSource = _WireSource(clients[2]);
      final secondCancel = LanternCancellationToken();
      final secondStopped = expectLater(
        repository.consumeScopedChanges(
          'wire',
          source: secondSource,
          cancellation: secondCancel,
        ),
        throwsA(isA<OfflineCanceledException>()),
      );
      addTearDown(secondCancel.cancel);
      await _waitUntil(() => secondSource.opens.isNotEmpty);
      expect(secondSource.opens.single.bootstrap, isFalse);
      expect(secondSource.opens.first.cursor!.toBytes(), before.toBytes());
      expect(secondSource.pluralVertexReads, 0);
      for (var i = 0; i < 3; i++) {
        final resident = await repository.readVertex(
          'wire',
          keys[i],
          policy: OfflineReadPolicy.cacheOnly,
        );
        expect((resident.value!.value as StringValue).value, 'updated-$i');
      }
      final resumed = await fixture.store.transaction(
        (t) => t.scopedChangeCursor('wire'),
      );
      expect(resumed!.toBytes(), before.toBytes());
      await clients[0].putVertex(
        VertexInput(key: keys[0], value: VertexValue.string('after-switch')),
      );
      await _waitUntil(
        () async =>
            (await repository.readVertex(
              'wire',
              keys[0],
              policy: OfflineReadPolicy.cacheOnly,
            )).state ==
            OfflineReadState.unknown,
        timeout: const Duration(seconds: 25),
      );
      secondCancel.cancel();
      await secondStopped;
    },
    skip: clusterEndpoints == null || clusterEndpoints.length != 3
        ? 'three native TLS replica endpoints unavailable'
        : false,
    timeout: const Timeout(Duration(minutes: 2)),
  );
}

LanternClient _client(Uri endpoint, {String? token}) {
  final context = SecurityContext(withTrustedRoots: true);
  for (final key in [
    'LANTERN_DART_RECEIPT_CA_FILE',
    'LANTERN_DART_IDENTITY_CA_FILE',
    'LANTERN_DART_IDENTITY_GAP_CA_FILE',
  ]) {
    final file = Platform.environment[key];
    if (file != null) context.setTrustedCertificates(file);
  }
  return LanternClient.connect(
    endpoint,
    token: token,
    allowInsecure: endpoint.scheme == 'http',
    httpClientFactory: () => HttpClient(context: context),
    defaultTimeout: const Duration(seconds: 5),
  );
}

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
  const _WireOpen(this.bootstrap, this.cursor);
  final bool bootstrap;
  final OfflineScopedChangeCursor? cursor;
}

// Instrument the maintained application bridge, preserving its single tail.
final class _WireSource implements OfflineScopedChangeSource {
  _WireSource(this.client);
  final LanternClient client;
  final List<_WireOpen> opens = [];
  int pluralVertexReads = 0;
  int pluralEdgeReads = 0;
  @override
  Future<OfflineScopedChangeSession> open({
    required bool bootstrap,
    required OfflineScopedChangeCursor? cursor,
    required LanternCancellationToken cancellation,
  }) async {
    opens.add(_WireOpen(bootstrap, cursor));
    final session = await LanternScopedChangeSource(
      client: client,
      responderId: client.endpoint.toString(),
    ).open(bootstrap: bootstrap, cursor: cursor, cancellation: cancellation);
    return _WireSession(this, session);
  }
}

final class _WireSession implements OfflineScopedChangeSession {
  _WireSession(this.source, this.session);
  final _WireSource source;
  final OfflineScopedChangeSession session;
  @override
  String get responderId => session.responderId;
  @override
  Stream<OfflineScopedChangeFrame> get frames => session.frames;
  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) {
    source.pluralVertexReads++;
    return session.getVertices(keys, cancellation: cancellation);
  }

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) {
    source.pluralEdgeReads++;
    return session.getEdges(edges, cancellation: cancellation);
  }

  @override
  Future<void> close() => session.close();
}
