// Run from sdks/dart/offline_sqlite with flutter test and a real h2c server.
import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import '../../sdks/dart/test/support/traversal_wire_matrix.dart';
import '../../sdks/dart/offline_sqlite/test/support/confirmation_wire.dart';

void main() {
  sqfliteFfiInit();
  test('G3 disclosed reply survives SQLite confirmation rollback', () async {
    final observer = _receiptClient();
    addTearDown(observer.close);
    final id = DateTime.now().microsecondsSinceEpoch;
    await verifyConfirmationRollback(
      observer: observer,
      openWriter: (intercept) => _receiptClient(null, intercept),
      pair: EdgeRef('g3:$id:tail', 'g3:$id:head'),
      undisclosed: false,
    );
  });
  for (final scenario in [
    'Put',
    'Delete',
    'Add/reset',
    'Condition',
    'Endpoint expiry',
  ]) {
    test('G1 durable automatic two-client $scenario', () async {
      final calls = _WireCalls();
      final a = _receiptClient(calls), b = _receiptClient();
      addTearDown(a.close);
      addTearDown(b.close);
      final directory = await Directory.systemTemp.createTemp('g1-two-client-');
      addTearDown(() => directory.delete(recursive: true));
      final session =
          (await SqliteOfflineStore.openSession(
                    path: '${directory.path}/data.db',
                    client: a,
                    binding: _wireBinding(a, 'g1'),
                    installationPolicy: _wireInstallationPolicy(),
                    databaseFactory: databaseFactoryFfi,
                    transferAllowed: false,
                  )
                  as SqliteSessionReady)
              .session;
      addTearDown(session.close);
      final prefix = 'g1:${DateTime.now().microsecondsSinceEpoch}:';
      final key = '${prefix}key',
          tail = '${prefix}tail',
          head = '${prefix}head';
      VertexInput value(String key, String text) => VertexInput(
        key: key,
        value: StringValue(text),
        expiresIn: const Duration(minutes: 5),
      );
      Future<String> current() async =>
          ((await b.getVertex(key)).value as StringValue).value;
      Future<MutationObservation<T>> settle<T>(MutationTicket<T> ticket) =>
          session.mutations
              .reobserve(ticket.reference)
              .timeout(const Duration(seconds: 5));
      Future<T> effect<T>(MutationTicket<T> ticket) async =>
          ((await settle(ticket)).reply!.items.single as MutationKnownEffect<T>)
              .effect;

      switch (scenario) {
        case 'Put':
          await b.putVertex(value(key, 'base'));
          final ticket = await session.mutations.submit(
            MutationRequest.putVertices([value(key, 'A')]),
          );
          final pending = await session.getVertex(
            key,
            options: const GetOptions(source: ReadSource.cacheOnly),
          );
          expect((pending.view!.value as StringValue).value, 'A');
          expect(pending.pending.count, 1);
          await b.putVertex(value(key, 'B-before'));
          expect(calls.count('PutVertices'), 0);
          await session.setTransferAllowed(true);
          expect(await effect(ticket), PutOutcome.appliedAndLive);
          expect(await current(), 'A');
          await b.putVertex(value(key, 'B-after'));
          expect(await current(), 'B-after');
          expect(await effect(ticket), PutOutcome.appliedAndLive);
        case 'Delete':
          await b.putVertex(value(key, 'base'));
          final ticket = await session.mutations.submit(
            MutationRequest.deleteVertices([key]),
          );
          final pending = await session.getVertex(
            key,
            options: const GetOptions(source: ReadSource.cacheOnly),
          );
          expect(pending.projection, PendingProjection.pendingDelete);
          expect(pending.confirmed.state, ConfirmedState.unknown);
          await b.putVertex(value(key, 'B-before'));
          expect(calls.count('DeleteVertices'), 0);
          await session.setTransferAllowed(true);
          expect(await effect(ticket), true);
          await expectLater(
            b.getVertex(key),
            throwsA(isA<LanternNotFoundException>()),
          );
          await b.putVertex(value(key, 'B-after'));
          expect(await current(), 'B-after');
          expect(await effect(ticket), true);
          expect(calls.count('DeleteVertices'), 1);
        case 'Add/reset':
          await b.putVertices([value(tail, 'tail'), value(head, 'head')]);
          final pair = EdgeRef(tail, head);
          await b.putEdge(EdgeInput(tail: tail, head: head, weight: 2));
          final ticket = await session.mutations.submit(
            MutationRequest.addEdges([
              EdgeInput(tail: tail, head: head, weight: 3),
            ]),
          );
          final pending = await session.getEdge(
            pair,
            options: const GetOptions(source: ReadSource.cacheOnly),
          );
          expect(pending.pending.count, 1);
          expect(pending.view, isNull, reason: 'no invented effective weight');
          await b.putEdge(EdgeInput(tail: tail, head: head, weight: 10));
          expect(calls.count('AddEdges'), 0);
          await session.setTransferAllowed(true);
          expect(await effect(ticket), 13);
          expect((await b.getEdge(pair)).weight, 13);
          await b.putEdge(EdgeInput(tail: tail, head: head, weight: 20));
          expect((await b.getEdge(pair)).weight, 20);
          expect(await effect(ticket), 13);
          expect(calls.count('AddEdges'), 1);
        case 'Condition':
          final ticket = await session.mutations.submit(
            MutationRequest.putVerticesIfAbsent([value(key, 'A')]),
          );
          final pending = await session.getVertex(
            key,
            options: const GetOptions(source: ReadSource.cacheOnly),
          );
          expect(pending.pending.count, 1);
          expect(
            pending.view,
            isNull,
            reason: 'absence does not establish the condition',
          );
          await b.putVertex(value(key, 'B-before'));
          expect(calls.count('PutVertices'), 0);
          await session.setTransferAllowed(true);
          expect(await effect(ticket), PutOutcome.conditionNotMet);
          expect(await current(), 'B-before');
          await b.deleteVertex(key);
          expect(await effect(ticket), PutOutcome.conditionNotMet);
          expect(calls.count('PutVertices'), 1);
        case 'Endpoint expiry':
          await b.putVertex(value(head, 'head'));
          final headBefore = await b.getVertex(head);
          calls.holdMethod = 'PutEdges';
          calls.entered = Completer<void>();
          calls.release = Completer<void>();
          addTearDown(() {
            if (!calls.release!.isCompleted) calls.release!.complete();
          });
          final tickets = await session.mutations.submitGroup(
            MutationGroup([
              MutationGroupNode(
                MutationRequest.putVertices([
                  VertexInput(
                    key: tail,
                    value: const StringValue('short'),
                    expiresIn: const Duration(seconds: 1),
                  ),
                ]),
              ),
              MutationGroupNode(
                MutationRequest.putEdges([
                  EdgeInput(
                    tail: tail,
                    head: head,
                    weight: 3,
                    expiresIn: const Duration(minutes: 5),
                  ),
                ]),
                dependencies: [
                  const MutationDependency(
                    node: 0,
                    item: 0,
                    predicate: MutationDependencyPredicate.putAppliedAndLive,
                  ),
                ],
              ),
            ]),
          );
          await session.setTransferAllowed(true);
          expect(
            await effect(tickets[0]).timeout(
              const Duration(seconds: 5),
              onTimeout: () => throw StateError(
                'prerequisite did not settle: ${calls.methods}',
              ),
            ),
            PutOutcome.appliedAndLive,
          );
          await calls.entered!.future.timeout(
            const Duration(seconds: 5),
            onTimeout: () async {
              final status = await session.mutations.getStatus(
                tickets[1].reference.operationId,
              );
              throw StateError(
                'dependent Edge not dispatched: ${calls.methods}; '
                '${status.items.map((item) => (item.state, item.failure)).toList()}',
              );
            },
          );
          var expired = false;
          for (var i = 0; i < 250 && !expired; i++) {
            try {
              await b.getVertex(tail);
            } on LanternNotFoundException {
              expired = true;
            }
            if (!expired)
              await Future<void>.delayed(const Duration(milliseconds: 20));
          }
          expect(expired, true, reason: 'actual Server expiry barrier');
          final result = settle(tickets[1]);
          final rejected = expectLater(
            result,
            throwsA(isA<MutationBatchException<Object?>>()),
          );
          calls.release!.complete();
          await rejected;
          final status = await session.mutations.getStatus(
            tickets[1].reference.operationId,
          );
          // Plain Put has no original receipt evidence after possible dispatch.
          // A later Get establishes current absence, not original handling.
          expect(status.items.single.state, MutationItemState.unknown);
          await expectLater(
            b.getVertex(tail),
            throwsA(isA<LanternNotFoundException>()),
          );
          await expectLater(
            b.getEdge(EdgeRef(tail, head)),
            throwsA(isA<LanternNotFoundException>()),
          );
          final headAfter = await b.getVertex(head);
          expect(
            (headAfter.value as StringValue).value,
            (headBefore.value as StringValue).value,
          );
          expect(headAfter.expiration, headBefore.expiration);
      }
    });
  }
  group('G4 offline real traversal covering matrix', () {
    late LanternClient reference;
    late String prefix, seed;
    late Uri endpoint;
    final matrix = traversalWireMatrix();
    final expected = <Graph>[];
    setUpAll(() async {
      endpoint = Uri.parse(
        Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT']!,
      );
      reference = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
      );
      prefix = 'g4:${DateTime.now().microsecondsSinceEpoch}:';
      seed = '${prefix}seed';
      final a = '${prefix}a', b = '${prefix}b';
      final expires = DateTime.now().toUtc().add(const Duration(minutes: 5));
      await reference.putVertices([
        for (final key in [seed, a, b])
          VertexInput(key: key, value: const NilValue(), expiresAt: expires),
      ]);
      await reference.putEdges([
        EdgeInput(tail: seed, head: a, weight: 3, expiresAt: expires),
        EdgeInput(tail: seed, head: b, weight: 2, expiresAt: expires),
        EdgeInput(tail: a, head: b, weight: 1, expiresAt: expires),
      ]);
      expect(matrix, hasLength(56));
      for (final row in matrix) {
        expected.add(
          await reference.illuminate(
            seed,
            traversal: row.traversal,
            weighting: row.weighting,
            vertexPrefix: prefix,
          ),
        );
      }
    });
    tearDownAll(() async => reference.close());
    for (final profile in ['memory', 'durable', 'durableWithoutReadCache']) {
      test(profile, () async {
        final calls = _WireCalls();
        final client = LanternClient.connect(
          endpoint,
          allowInsecure: endpoint.scheme == 'http',
          interceptors: [calls.intercept],
        );
        addTearDown(client.close);
        final directory = await Directory.systemTemp.createTemp('g4-profile-');
        addTearDown(() => directory.delete(recursive: true));
        final binding = _wireBinding(client, profile);
        final route = LanternSingleResponderRoute.certified(
          client: client,
          responderId: 'fixed-g4-listener',
          isCurrent: () => true,
        );
        final session = profile == 'memory'
            ? await OfflineLanternSession.open(
                client: client,
                binding: binding,
                store: MemoryOfflineStore(),
                durability: AdmissionDurability.memory,
                readRoute: route,
              )
            : (await SqliteOfflineStore.openSession(
                        path: '${directory.path}/data.db',
                        client: client,
                        binding: binding,
                        installationPolicy: _wireInstallationPolicy(),
                        databaseFactory: databaseFactoryFfi,
                        cacheReads: profile != 'durableWithoutReadCache',
                        readRoute: route,
                      )
                      as SqliteSessionReady)
                  .session;
        addTearDown(session.close);
        for (var i = 0; i < matrix.length; i++) {
          final row = matrix[i];
          final snapshot = await session.illuminate(
            seed,
            traversal: row.traversal,
            weighting: row.weighting,
            vertexPrefix: prefix,
            options: const TraversalReadOptions(source: ReadSource.serverOnly),
          );
          expect(
            snapshot.confirmed.state,
            ConfirmedState.present,
            reason: '$profile row $i',
          );
          final graph = snapshot.confirmed.value!, oracle = expected[i];
          expect(graph.vertices.keys, orderedEquals(oracle.vertices.keys));
          expect(
            graph.vertices.values.map(
              (v) => (v.value.runtimeType, v.expiration),
            ),
            orderedEquals(
              oracle.vertices.values.map(
                (v) => (v.value.runtimeType, v.expiration),
              ),
            ),
          );
          expect(
            graph.allEdges.map((e) => (e.tail, e.head, e.weight, e.expiration)),
            orderedEquals(
              oracle.allEdges.map(
                (e) => (e.tail, e.head, e.weight, e.expiration),
              ),
            ),
          );
          expect(
            calls.count('Illuminate'),
            i + 1,
            reason: 'serverOnly must reach the wire',
          );
        }
        expect(calls.count('Illuminate'), 56);
      });
    }
  });
  test(
    'bound Offline accepts without tokens and recovers exact indexed receipt results after SQLite reopen',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_RECEIPT_ENDPOINT'];
      final token = Platform.environment['LANTERN_DART_RECEIPT_TOKEN'];
      final ca = Platform.environment['LANTERN_DART_RECEIPT_CA_FILE'];
      if (endpointValue == null || token == null || ca == null)
        throw StateError(
          'receipt fixture is required for bound Offline qualification',
        );
      final endpoint = Uri.parse(endpointValue);
      HttpClient trusted() => HttpClient(
        context: SecurityContext(withTrustedRoots: false)
          ..setTrustedCertificates(ca),
      );
      final server = LanternClient.connect(
        endpoint,
        token: token,
        httpClientFactory: trusted,
      );
      addTearDown(server.close);
      final prefix = 'bound-sql-wire:${DateTime.now().microsecondsSinceEpoch}:';
      final conditional = '${prefix}conditional',
          deleted = '${prefix}deleted',
          tail = '${prefix}tail',
          head = '${prefix}head';
      await server.putVertices([
        for (final key in [conditional, deleted, tail, head])
          VertexInput(key: key, value: VertexValue.string('original')),
      ]);
      await server.putEdge(EdgeInput(tail: tail, head: head, weight: 1));
      final proxy = await _ResponseDroppingProxy.bind(
        endpoint,
        drops: const {'PutVertices': 1, 'DeleteVertices': 1, 'AddEdges': 1},
        upstreamClient: trusted(),
      );
      addTearDown(proxy.close);
      final directory = await Directory.systemTemp.createTemp(
        'bound-sql-wire-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final path = '${directory.path}/state.db';
      var store = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      addTearDown(() => store.close());
      var now = DateTime.now().toUtc();
      var tokens = 0;
      final binding = LanternSessionBinding.application(
        serviceOrigin: proxy.endpoint,
        securityDomain: 'test',
        accountIdentity: 'one',
        authorityBinding: 'a',
      );
      Future<OfflineLanternDataClient> open() => OfflineLanternDataClient.open(
        client: LanternClient.connect(
          proxy.endpoint,
          allowInsecure: true,
          tokenProvider: () {
            tokens++;
            return token;
          },
        ),
        store: store,
        binding: binding,
        durability: AdmissionDurability.durable,
        clock: () => now,
      );
      var client = await open();
      final condition = await client.mutations.submit(
        MutationRequest.putVerticesIfAbsent([
          VertexInput(
            key: conditional,
            value: VertexValue.string('replacement'),
          ),
        ]),
      );
      final deletion = await client.mutations.submit(
        MutationRequest.deleteVertices([deleted, deleted, '${prefix}missing']),
      );
      final add = await client.mutations.submit(
        MutationRequest.addEdges([
          EdgeInput(tail: tail, head: head, weight: .5),
        ]),
      );
      expect(tokens, 0);
      final before = await store.transaction(
        (tx) => tx.outbox(binding.stableIdentity),
      );
      expect(
        before.every(
          (r) =>
              r.preparation is OfflineAwaitingPreparation &&
              !r.mayHaveDispatched,
        ),
        isTrue,
      );
      await client.mutationDelivery.dispatchPass();
      for (final rpc in ['PutVertices', 'DeleteVertices', 'AddEdges'])
        expect(proxy.dropped(rpc), 1);
      final sent = await store.transaction(
        (tx) => tx.outbox(binding.stableIdentity),
      );
      final ids = {
        for (final r in sent.where((r) => r.mayHaveDispatched))
          r.recordId: r.receipt!.operationId,
      };
      final addId =
          (sent
                      .firstWhere(
                        (r) => r.intent is OfflineReceiptAddEdgeIntent,
                      )
                      .intent
                  as OfflineReceiptAddEdgeIntent)
              .contributionId;
      await client.close();
      await store.close();
      now = now.add(const Duration(seconds: 2));
      store = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      client = await open();
      addTearDown(client.close);
      final recovered = await store.transaction(
        (tx) => tx.outbox(binding.stableIdentity),
      );
      for (final r in recovered.where((r) => r.mayHaveDispatched))
        expect(r.receipt!.operationId, ids[r.recordId]);
      expect(
        (recovered
                    .firstWhere((r) => r.intent is OfflineReceiptAddEdgeIntent)
                    .intent
                as OfflineReceiptAddEdgeIntent)
            .contributionId,
        addId,
      );
      final c = client.mutations.reobserve(condition.reference),
          d = client.mutations.reobserve(deletion.reference),
          a = client.mutations.reobserve(add.reference);
      for (var pass = 0; pass < 4; pass++) {
        await client.mutationDelivery.dispatchPass();
        now = now.add(const Duration(seconds: 2));
      }
      expect(
        ((await c).reply!.items.single as MutationKnownEffect<PutOutcome>)
            .effect,
        PutOutcome.conditionNotMet,
      );
      expect(
        (await d).reply!.items.map(
          (r) => (r as MutationKnownEffect<bool>).effect,
        ),
        [true, false, false],
      );
      expect(
        ((await a).reply!.items.single as MutationKnownEffect<double>).effect,
        1.5,
      );
      expect(proxy.forwarded('PutVertices'), 1);
      expect(proxy.forwarded('AddEdges'), 1);
      expect(proxy.forwarded('DeleteVertices'), 3);
      expect((await server.getEdge(EdgeRef(tail, head))).weight, 1.5);
      expect(
        await store.transaction((tx) => tx.outbox(binding.stableIdentity)),
        isEmpty,
      );
    },
  );
  test(
    'response-dropping proxy loses committed PutVertex and PutEdge responses',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
      if (endpointValue == null || endpointValue.isEmpty) {
        markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final serverClient = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
        idempotentAdds: true,
      );
      addTearDown(serverClient.close);
      await serverClient.ping();

      final proxy = await _ResponseDroppingProxy.bind(
        endpoint,
        drops: const <String, int>{'PutVertices': 1, 'PutEdges': 1},
      );
      addTearDown(proxy.close);
      final client = LanternClient.connect(proxy.endpoint, allowInsecure: true);
      addTearDown(client.close);
      await client.ping();

      final online = LanternClientOfflineRemote(client);
      final directory = await Directory.systemTemp.createTemp('sqlite-wire-');
      addTearDown(() => directory.delete(recursive: true));
      final path = '${directory.path}/offline.db';
      var store = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      addTearDown(() => store.close());
      final enqueueNow = DateTime.now().toUtc();
      final repository = OfflineLanternRepository(
        store: store,
        remote: online,
        config: OfflineConfig(
          clock: () => enqueueNow,
          jitter: (ceiling) => ceiling,
          baseRetryDelay: const Duration(seconds: 1),
        ),
      );
      addTearDown(repository.dispose);
      final prefix =
          'dart-sqlite-wire:${DateTime.now().microsecondsSinceEpoch}:';
      final vertexKey = '${prefix}vertex';
      final tail = '${prefix}tail';
      final head = '${prefix}head';
      await repository.putVertex(
        partitionId: 'wire',
        input: VertexInput(
          key: vertexKey,
          value: VertexValue.string('committed'),
          expiresIn: const Duration(minutes: 5),
        ),
      );
      await repository.putEdge(
        partitionId: 'wire',
        input: EdgeInput(
          tail: tail,
          head: head,
          weight: 0.75,
          expiresIn: const Duration(minutes: 5),
        ),
      );
      expect(await repository.drain('wire'), 0);
      expect(proxy.forwarded('PutVertices'), 1);
      expect(proxy.forwarded('PutEdges'), 1);
      expect(proxy.dropped('PutVertices'), 1);
      expect(proxy.dropped('PutEdges'), 1);
      final beforeRestart = await store.transaction(
        (transaction) async => await transaction.outbox('wire'),
      );
      expect(beforeRestart, hasLength(2));
      expect(
        beforeRestart.map((record) => record.attemptCount),
        everyElement(1),
      );
      expect(
        beforeRestart.map((record) => record.diagnosticCode),
        everyElement('unavailable'),
      );
      final firstVertex = beforeRestart
          .map((record) => record.intent)
          .whereType<OfflinePutVertexIntent>()
          .single;
      final firstEdge = beforeRestart
          .map((record) => record.intent)
          .whereType<OfflinePutEdgeIntent>()
          .single;
      expect(
        (await serverClient.getVertex(vertexKey)).value,
        isA<StringValue>().having((value) => value.value, 'value', 'committed'),
        reason: 'the upstream server committed before the proxy disconnected',
      );
      expect(
        (await serverClient.getEdge(EdgeRef(tail, head))).weight,
        Float32Value(0.75).value,
      );

      await repository.dispose();
      await store.close();
      store = await SqliteOfflineStore.open(
        path: path,
        databaseFactory: databaseFactoryFfi,
      );
      final restarted = OfflineLanternRepository(
        store: store,
        remote: online,
        config: OfflineConfig(
          clock: () => enqueueNow.add(const Duration(seconds: 2)),
          jitter: (_) => Duration.zero,
        ),
      );
      addTearDown(restarted.dispose);
      expect(await restarted.drain('wire'), 2);
      expect(proxy.forwarded('PutVertices'), 2);
      expect(proxy.forwarded('PutEdges'), 2);

      final vertex = await serverClient.getVertex(vertexKey);
      expect((vertex.value as StringValue).value, 'committed');
      expect(
        vertex.expiration,
        firstVertex.vertex.expiration,
        reason: 'replay must not rebase the once-resolved Vertex TTL',
      );
      final edge = await serverClient.getEdge(EdgeRef(tail, head));
      expect(edge.weight, Float32Value(0.75).value);
      expect(
        edge.expiration,
        firstEdge.edge.expiration,
        reason: 'replay must not rebase the once-resolved Edge TTL',
      );
      expect(await restarted.listPending('wire'), isEmpty);
    },
  );

  test(
    'resident-only recovery revalidates plural Vertex and Edge over h2c',
    () async {
      final endpointValue =
          Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
      if (endpointValue == null || endpointValue.isEmpty) {
        markTestSkipped('set LANTERN_DART_REAL_WIRE_ENDPOINT');
        return;
      }
      final endpoint = Uri.parse(endpointValue);
      final client = LanternClient.connect(
        endpoint,
        allowInsecure: endpoint.scheme == 'http',
      );
      addTearDown(client.close);
      await client.ping();
      final prefix = 'recovery:${DateTime.now().microsecondsSinceEpoch}:';
      final present = '${prefix}present';
      final absent = '${prefix}absent';
      final tail = '${prefix}tail';
      final head = '${prefix}head';
      await client.putVertex(
        VertexInput(key: present, value: VertexValue.string('from-server')),
      );
      await client.putEdge(EdgeInput(tail: tail, head: head, weight: 1.5));

      final directory = await Directory.systemTemp.createTemp(
        'sqlite-recovery-',
      );
      addTearDown(() => directory.delete(recursive: true));
      final store = await SqliteOfflineStore.open(
        path: '${directory.path}/offline.db',
        databaseFactory: databaseFactoryFfi,
      );
      addTearDown(store.close);
      final now = DateTime.now().toUtc();
      await store.transaction((transaction) async {
        for (final key in [present, absent]) {
          await transaction.putCache(
            'wire',
            OfflineCacheRecord.value(
              partitionId: 'wire',
              generation: await transaction.generation('wire'),
              key: OfflineEntityKey.vertex(key),
              entity: Vertex(
                key: key,
                value: VertexValue.string('stale'),
                expiration: null,
              ),
              validatedAt: now,
              lastAccessAt: now,
            ),
          );
        }
        await transaction.putCache(
          'wire',
          OfflineCacheRecord.value(
            partitionId: 'wire',
            generation: await transaction.generation('wire'),
            key: OfflineEntityKey.edge(tail, head),
            entity: Edge(tail: tail, head: head, weight: 0.5, expiration: null),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
        await transaction.resetChangeCursor(
          'wire',
          OfflineChangeCursor(const {}),
        );
      });
      final online = LanternClientOfflineRemote(client);
      final repository = OfflineLanternRepository(
        store: store,
        remote: online,
        config: OfflineConfig(clock: () => now),
      );
      addTearDown(repository.dispose);
      expect(
        await repository.revalidateResidentBatch(
          'wire',
          recoveryRemote: online,
          limit: 3,
        ),
        3,
      );
      expect(
        await store.transaction((t) => t.unknownResidents('wire', limit: 3)),
        isEmpty,
      );
      final fresh = await repository.readVertex(
        'wire',
        present,
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect((fresh.value!.value as StringValue).value, 'from-server');
      expect(
        (await repository.readVertex(
          'wire',
          absent,
          policy: OfflineReadPolicy.cacheOnly,
        )).state,
        OfflineReadState.missing,
      );
      expect(
        (await repository.readEdge(
          'wire',
          EdgeRef(tail, head),
          policy: OfflineReadPolicy.cacheOnly,
        )).value!.weight,
        Float32Value(1.5).value,
      );
    },
  );
}

LanternSessionBinding _wireBinding(LanternClient client, String account) =>
    LanternSessionBinding.application(
      serviceOrigin: client.endpoint,
      securityDomain: 'finite-acceptance-wire',
      accountIdentity: account,
      authorityBinding: 'current',
    );

SqliteInstallationPolicy _wireInstallationPolicy() => SqliteInstallationPolicy(
  identity: 'fresh-isolated-acceptance-fixture',
  version: 1,
  evaluate: (_) async =>
      SqliteInstallationEvidence.verified(installationId: 'host-fixture'),
);

final class _WireCalls {
  final methods = <String>[];
  String? holdMethod;
  Completer<void>? entered, release;
  int count(String method) => methods.where((m) => m == method).length;
  connect.AnyFn<I, O> intercept<I extends Object, O extends Object>(
    connect.AnyFn<I, O> next,
  ) => (request) async {
    final method = request.spec.procedure.split('/').last;
    if (method == holdMethod) {
      if (!entered!.isCompleted) entered!.complete();
      await release!.future;
    }
    methods.add(method);
    return next(request);
  };
}

LanternClient _receiptClient([_WireCalls? calls, connect.Interceptor? extra]) {
  final endpoint = Uri.parse(
    Platform.environment['LANTERN_DART_RECEIPT_ENDPOINT']!,
  );
  final ca = Platform.environment['LANTERN_DART_RECEIPT_CA_FILE']!;
  return LanternClient.connect(
    endpoint,
    token: Platform.environment['LANTERN_DART_RECEIPT_TOKEN']!,
    httpClientFactory: () => HttpClient(
      context: SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates(ca),
    ),
    interceptors: [
      if (calls != null) calls.intercept,
      if (extra != null) extra,
    ],
    retryPolicy: const RetryPolicy(maxAttempts: 1),
  );
}

final class _ResponseDroppingProxy {
  _ResponseDroppingProxy._(
    this._server,
    this._upstreamEndpoint,
    Map<String, int> drops,
    HttpClient? upstreamClient,
  ) : _remainingDrops = Map<String, int>.of(drops) {
    _upstream = upstreamClient ?? HttpClient();
    _upstream.autoUncompress = false;
    _server.listen((request) => unawaited(_forward(request)));
  }

  static Future<_ResponseDroppingProxy> bind(
    Uri upstreamEndpoint, {
    required Map<String, int> drops,
    HttpClient? upstreamClient,
  }) async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    return _ResponseDroppingProxy._(
      server,
      upstreamEndpoint,
      drops,
      upstreamClient,
    );
  }

  final HttpServer _server;
  final Uri _upstreamEndpoint;
  late final HttpClient _upstream;
  final Map<String, int> _remainingDrops;
  final Map<String, int> _forwarded = <String, int>{};
  final Map<String, int> _dropped = <String, int>{};

  Uri get endpoint =>
      Uri(scheme: 'http', host: _server.address.host, port: _server.port);

  int forwarded(String rpc) => _forwarded[rpc] ?? 0;

  int dropped(String rpc) => _dropped[rpc] ?? 0;

  Future<void> close() async {
    await _server.close(force: true);
    _upstream.close(force: true);
  }

  Future<void> _forward(HttpRequest downstream) async {
    final rpc = downstream.uri.pathSegments.isEmpty
        ? ''
        : downstream.uri.pathSegments.last;
    _forwarded[rpc] = (_forwarded[rpc] ?? 0) + 1;
    try {
      final target = _upstreamEndpoint.resolveUri(downstream.uri);
      final upstreamRequest = await _upstream.openUrl(
        downstream.method,
        target,
      );
      _copyHeaders(downstream.headers, upstreamRequest.headers);
      await upstreamRequest.addStream(downstream);
      final upstreamResponse = await upstreamRequest.close();
      final responseBytes = await upstreamResponse.fold<BytesBuilder>(
        BytesBuilder(copy: false),
        (builder, bytes) => builder..add(bytes),
      );

      final remaining = _remainingDrops[rpc] ?? 0;
      if (remaining > 0) {
        _remainingDrops[rpc] = remaining - 1;
        _dropped[rpc] = (_dropped[rpc] ?? 0) + 1;
        final socket = await downstream.response.detachSocket(
          writeHeaders: false,
        );
        socket.destroy();
        return;
      }

      downstream.response.statusCode = upstreamResponse.statusCode;
      _copyHeaders(upstreamResponse.headers, downstream.response.headers);
      downstream.response.add(responseBytes.takeBytes());
      await downstream.response.close();
    } on Object {
      try {
        downstream.response.statusCode = HttpStatus.badGateway;
        await downstream.response.close();
      } on Object {
        // The deliberately disconnected response has no writable downstream.
      }
    }
  }

  static void _copyHeaders(HttpHeaders source, HttpHeaders target) {
    const ignored = <String>{
      'connection',
      'content-length',
      'host',
      'keep-alive',
      'proxy-authenticate',
      'proxy-authorization',
      'te',
      'trailer',
      'transfer-encoding',
      'upgrade',
    };
    source.forEach((name, values) {
      if (!ignored.contains(name.toLowerCase())) {
        target.set(name, values);
      }
    });
  }
}

final class _CommittedResponseGateRemote implements OfflineRemote {
  _CommittedResponseGateRemote(this.delegate);

  final OfflineRemote delegate;
  final Completer<void> serverCommitted = Completer<void>();

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.getEdge(edge, cancellation: cancellation);

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) => delegate.getVertex(key, cancellation: cancellation);

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) =>
      delegate.probe(cancellation: cancellation);

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.putEdge(edge, cancellation: cancellation);

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) async {
    final outcome = await delegate.putVertex(
      vertex,
      cancellation: cancellation,
    );
    if (!serverCommitted.isCompleted) serverCommitted.complete();
    final canceled = Completer<void>();
    final removeCancellation = cancellation?.listen((_) {
      if (!canceled.isCompleted) {
        canceled.completeError(
          const OfflineRemoteFailure(
            OfflineRemoteErrorKind.canceled,
            OfflineCanceledException(),
          ),
        );
      }
    });
    try {
      await canceled.future;
    } finally {
      removeCancellation?.call();
    }
    return outcome;
  }
}

final class _FailingAfterRemote implements OfflineRemote {
  _FailingAfterRemote(this.delegate, {required this.succeedBefore});

  final OfflineRemote delegate;
  final int succeedBefore;
  var putVertexCalls = 0;

  @override
  Future<OfflineRemoteRead<Edge>> getEdge(
    EdgeRef edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.getEdge(edge, cancellation: cancellation);

  @override
  Future<OfflineRemoteRead<Vertex>> getVertex(
    String key, {
    LanternCancellationToken? cancellation,
  }) => delegate.getVertex(key, cancellation: cancellation);

  @override
  Future<void> probe({LanternCancellationToken? cancellation}) =>
      delegate.probe(cancellation: cancellation);

  @override
  Future<PutOutcome> putEdge(
    Edge edge, {
    LanternCancellationToken? cancellation,
  }) => delegate.putEdge(edge, cancellation: cancellation);

  @override
  Future<PutOutcome> putVertex(
    Vertex vertex, {
    LanternCancellationToken? cancellation,
  }) {
    putVertexCalls += 1;
    if (putVertexCalls > succeedBefore) {
      throw OfflineRemoteFailure(
        OfflineRemoteErrorKind.unavailable,
        StateError('simulated outage'),
      );
    }
    return delegate.putVertex(vertex, cancellation: cancellation);
  }
}
