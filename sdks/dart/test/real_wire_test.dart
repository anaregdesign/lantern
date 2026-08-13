import 'dart:async';
import 'dart:convert';
import 'dart:io' as io;
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/io.dart' as connect_io;
import 'package:connectrpc/protobuf.dart';
import 'package:connectrpc/protocol/connect.dart' as connect_protocol;
import 'package:lantern_client/lantern_client.dart';
import 'package:test/test.dart';

void main() {
  final configured = io.Platform.environment['LANTERN_DART_REAL_WIRE_ENDPOINT'];
  if (configured == null || configured.isEmpty) {
    test('real-wire tests require a configured Lantern listener', () {
      markTestSkipped('LANTERN_DART_REAL_WIRE_ENDPOINT is not configured');
    });
    return;
  }

  final endpoint = Uri.parse(configured);
  late LanternClient client;
  late String prefix;

  setUpAll(() async {
    client = LanternClient.connect(
      endpoint,
      allowInsecure: endpoint.scheme == 'http',
      defaultTimeout: const Duration(seconds: 5),
    );
    prefix = 'dart-real-${DateTime.now().microsecondsSinceEpoch}';
    await client.ping();
  });
  tearDownAll(() async {
    await client.close();
  });

  test(
    'every vertex kind round-trips with permanent and exact semantics',
    () async {
      final inputs = <VertexInput>[
        VertexInput(key: '$prefix-f64', value: VertexValue.float64(1.25)),
        VertexInput(key: '$prefix-f32', value: VertexValue.float32(2.5)),
        VertexInput(key: '$prefix-i32', value: VertexValue.int32(-0x80000000)),
        VertexInput(
          key: '$prefix-i64',
          value: VertexValue.int64(-0x8000000000000000),
        ),
        VertexInput(key: '$prefix-u32', value: VertexValue.uint32(0xffffffff)),
        VertexInput(
          key: '$prefix-u64',
          value: VertexValue.uint64((BigInt.one << 64) - BigInt.one),
        ),
        VertexInput(key: '$prefix-bool', value: VertexValue.boolean(true)),
        VertexInput(
          key: '$prefix-string',
          value: VertexValue.string('lantern'),
        ),
        VertexInput(
          key: '$prefix-bytes',
          value: VertexValue.bytes(Uint8List.fromList([0, 1, 255])),
        ),
        VertexInput(
          key: '$prefix-timestamp',
          value: VertexValue.timestamp(DateTime.parse('2026-07-12T01:02:03Z')),
        ),
        VertexInput(
          key: '$prefix-duration',
          value: VertexValue.duration(
            const Duration(seconds: -12, microseconds: -345),
          ),
        ),
        VertexInput(key: '$prefix-nil', value: VertexValue.nil()),
        VertexInput(key: '$prefix-unset', value: VertexValue.unset()),
      ];

      final put = await client.putVertices(inputs, batchSize: 4);
      expect(put, hasLength(inputs.length));
      final read = await client.getVertices(
        inputs.map((input) => input.key),
        batchSize: 3,
      );
      expect(read.missing, isEmpty);
      expect(read.vertices, hasLength(inputs.length));
      final values = {for (final vertex in read.vertices) vertex.key: vertex};
      expect(values['$prefix-f64']?.value, isA<Float64Value>());
      expect(values['$prefix-f32']?.value, isA<Float32Value>());
      expect(values['$prefix-i32']?.value, isA<Int32Value>());
      expect(values['$prefix-i64']?.value, isA<Int64Value>());
      expect(values['$prefix-u32']?.value, isA<Uint32Value>());
      expect(
        (values['$prefix-u64']?.value as Uint64Value).value,
        (BigInt.one << 64) - BigInt.one,
      );
      expect(values['$prefix-bool']?.value, isA<BoolValue>());
      expect(values['$prefix-string']?.value, isA<StringValue>());
      expect((values['$prefix-bytes']?.value as BytesValue).value, [0, 1, 255]);
      expect(values['$prefix-timestamp']?.value, isA<TimestampValue>());
      expect(values['$prefix-duration']?.value, isA<DurationValue>());
      expect(values['$prefix-nil']?.value, isA<NilValue>());
      expect(values['$prefix-unset']?.value, isA<UnsetValue>());
      expect(
        read.vertices.every((vertex) => vertex.expiration == null),
        isTrue,
      );
    },
  );

  test(
    'TTL, born-expired, conditional put, missing, and delete contracts',
    () async {
      final absolute = DateTime.now().toUtc().add(const Duration(minutes: 5));
      expect(
        await client.putVertex(
          VertexInput(
            key: '$prefix-relative',
            value: VertexValue.nil(),
            expiresIn: const Duration(minutes: 5),
          ),
        ),
        PutOutcome.appliedAndLive,
      );
      expect(
        await client.putVertex(
          VertexInput(
            key: '$prefix-absolute',
            value: VertexValue.nil(),
            expiresAt: absolute,
          ),
        ),
        PutOutcome.appliedAndLive,
      );
      expect(
        await client.putVertex(
          VertexInput(
            key: '$prefix-born-expired',
            value: VertexValue.nil(),
            expiresAt: DateTime.now().toUtc().subtract(
              const Duration(hours: 1),
            ),
          ),
        ),
        PutOutcome.expired,
      );
      await expectLater(
        client.getVertex('$prefix-born-expired'),
        throwsA(isA<LanternNotFoundException>()),
      );
      expect(
        await client.putVertexIfAbsent(
          VertexInput(
            key: '$prefix-relative',
            value: VertexValue.string('new'),
          ),
        ),
        PutOutcome.conditionNotMet,
      );
      final conditional = await client.putVerticesIfAbsent([
        VertexInput(key: '$prefix-relative', value: VertexValue.nil()),
        VertexInput(key: '$prefix-new', value: VertexValue.nil()),
      ]);
      expect(conditional.map((result) => result.outcome), [
        PutOutcome.conditionNotMet,
        PutOutcome.appliedAndLive,
      ]);
      expect(await client.deleteVertex('$prefix-new'), isTrue);
      expect(await client.deleteVertex('$prefix-new'), isFalse);
    },
  );

  test(
    'add and put edges stay distinct and preserve caller contrib ID',
    () async {
      final id = Uint8List(24)..[0] = 1;
      final edge = EdgeInput(
        tail: '$prefix-edge-tail',
        head: '$prefix-edge-head',
        weight: 2,
        contribId: id,
      );
      expect(await client.addEdge(edge), 2);
      expect(await client.addEdge(edge), 2, reason: 'same contrib ID dedupes');
      expect(
        await client.addEdge(
          EdgeInput(
            tail: edge.tail,
            head: edge.head,
            weight: 3,
            expiresIn: const Duration(minutes: 5),
          ),
        ),
        5,
      );
      await client.putEdge(
        EdgeInput(tail: edge.tail, head: edge.head, weight: 7),
      );
      final stored = await client.getEdge(EdgeRef(edge.tail, edge.head));
      expect(stored.weight, 7);
      expect(stored.expiration, isNull);
      expect(await client.deleteEdge(EdgeRef(edge.tail, edge.head)), isTrue);
      expect(await client.deleteEdge(EdgeRef(edge.tail, edge.head)), isFalse);
    },
  );

  test(
    'a later real-wire rejection reports the exact committed prefix',
    () async {
      final invalidKey = List.filled(2048, 'x').join();
      await expectLater(
        client.putVertices([
          VertexInput(key: '$prefix-partial-ok', value: VertexValue.nil()),
          VertexInput(key: invalidKey, value: VertexValue.nil()),
        ], batchSize: 1),
        throwsA(
          isA<BatchException>()
              .having((error) => error.committed, 'committed', 1)
              .having(
                (error) => error.cause,
                'cause',
                isA<LanternInvalidArgumentException>(),
              ),
        ),
      );
      expect((await client.getVertex('$prefix-partial-ok')).key, isNotEmpty);
    },
  );

  test('committed response loss retries only dedup-safe Add', () async {
    final safeFault = _CommittedResponseLossTransport(
      endpoint,
      loseProcedure: '/graph.v1.LanternService/AddEdges',
    );
    final safeClient = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      transport: safeFault,
      onClose: safeFault.close,
      idempotentAdds: true,
      retryPolicy: _realWireRetry,
    );
    addTearDown(safeClient.close);
    final safeRef = EdgeRef('$prefix-retry-safe-a', '$prefix-retry-safe-b');
    expect(
      await safeClient.addEdge(
        EdgeInput(tail: safeRef.tail, head: safeRef.head, weight: 2),
      ),
      2,
    );
    expect((await client.getEdge(safeRef)).weight, 2);
    expect(safeFault.requestsFor('/graph.v1.LanternService/AddEdges'), 2);

    final unsafeFault = _CommittedResponseLossTransport(
      endpoint,
      loseProcedure: '/graph.v1.LanternService/AddEdges',
    );
    final unsafeClient = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      transport: unsafeFault,
      onClose: unsafeFault.close,
      retryPolicy: _realWireRetry,
    );
    addTearDown(unsafeClient.close);
    final unsafeRef = EdgeRef(
      '$prefix-retry-unsafe-a',
      '$prefix-retry-unsafe-b',
    );
    await expectLater(
      unsafeClient.addEdge(
        EdgeInput(tail: unsafeRef.tail, head: unsafeRef.head, weight: 3),
      ),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect((await client.getEdge(unsafeRef)).weight, 3);
    expect(unsafeFault.requestsFor('/graph.v1.LanternService/AddEdges'), 1);
  });

  test(
    'ambiguous PutIfAbsent and Delete response loss is never replayed',
    () async {
      final putFault = _CommittedResponseLossTransport(
        endpoint,
        loseProcedure: '/graph.v1.LanternService/PutVertices',
      );
      final putClient = LanternClient.connect(
        endpoint,
        allowInsecure: true,
        transport: putFault,
        onClose: putFault.close,
        retryPolicy: _realWireRetry,
      );
      addTearDown(putClient.close);
      final key = '$prefix-ambiguous-put';
      await expectLater(
        putClient.putVertexIfAbsent(
          VertexInput(key: key, value: VertexValue.string('committed')),
        ),
        throwsA(isA<LanternUnavailableException>()),
      );
      expect((await client.getVertex(key)).key, key);
      expect(putFault.requestsFor('/graph.v1.LanternService/PutVertices'), 1);

      final deleteFault = _CommittedResponseLossTransport(
        endpoint,
        loseProcedure: '/graph.v1.LanternService/DeleteVertices',
      );
      final deleteClient = LanternClient.connect(
        endpoint,
        allowInsecure: true,
        transport: deleteFault,
        onClose: deleteFault.close,
        retryPolicy: _realWireRetry,
      );
      addTearDown(deleteClient.close);
      await expectLater(
        deleteClient.deleteVertex(key),
        throwsA(isA<LanternUnavailableException>()),
      );
      await expectLater(
        client.getVertex(key),
        throwsA(isA<LanternNotFoundException>()),
      );
      expect(
        deleteFault.requestsFor('/graph.v1.LanternService/DeleteVertices'),
        1,
      );
    },
  );

  test('decaying Add response loss does not double the curve', () async {
    final fault = _CommittedResponseLossTransport(
      endpoint,
      loseProcedure: '/graph.v1.LanternService/AddEdges',
    );
    final retrying = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      transport: fault,
      onClose: fault.close,
      idempotentAdds: true,
      retryPolicy: _realWireRetry,
    );
    addTearDown(retrying.close);
    final ref = EdgeRef('$prefix-decay-a', '$prefix-decay-b');
    expect(
      await retrying.addDecayingEdge(
        tail: ref.tail,
        head: ref.head,
        options: const DecayOptions(
          initialWeight: 16,
          ratio: 0.5,
          steps: 5,
          interval: Duration(minutes: 1),
        ),
      ),
      closeTo(16, 1e-5),
    );
    expect((await client.getEdge(ref)).weight, closeTo(16, 1e-5));
    expect(fault.requestsFor('/graph.v1.LanternService/AddEdges'), 2);
  });

  test('cursor pages resume without gaps in both vertex orders', () async {
    final scanPrefix = '$prefix-scan:';
    final expected = List.generate(5, (index) => '$scanPrefix$index');
    await client.putVertices(
      expected.map(
        (key) => VertexInput(key: key, value: VertexValue.string(key)),
      ),
    );

    final first = await client.scanVertices(prefix: scanPrefix, limit: 2);
    final resumed = await client.scanVertices(
      prefix: scanPrefix,
      limit: 2,
      cursor: first.nextCursor,
    );
    expect(
      [...first.items, ...resumed.items].map((vertex) => vertex.key),
      expected.take(4),
    );

    final ascending = await client
        .scanVerticesAll(prefix: scanPrefix, limit: 2)
        .expand((page) => page.items)
        .map((vertex) => vertex.key)
        .toList();
    final descending = await client
        .scanVertexKeysAll(
          prefix: scanPrefix,
          limit: 2,
          order: ScanOrder.descending,
        )
        .expand((page) => page.items)
        .toList();
    expect(ascending, expected);
    expect(descending, expected.reversed);

    await expectLater(
      client.scanVertices(
        prefix: scanPrefix,
        cursor: first.nextCursor,
        order: ScanOrder.descending,
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.scanVertices(
        prefix: scanPrefix,
        cursor: ScanCursor.fromBytes([1, 2, 3]),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
  });

  test('edge intersection scans preserve ascending page boundaries', () async {
    final tailPrefix = '$prefix-edge-scan:';
    final matchingHeads = <String>[];
    final edges = List.generate(5, (index) {
      final head = '$prefix-edge-head:${index.isEven ? 'even' : 'odd'}:$index';
      if (index.isEven) matchingHeads.add(head);
      return EdgeInput(
        tail: '$tailPrefix$index',
        head: head,
        weight: index + 1,
      );
    });
    await client.putEdges(edges);

    final scanned = await client
        .scanEdgesAll(
          tailPrefix: tailPrefix,
          headPrefix: '$prefix-edge-head:even:',
          limit: 1,
        )
        .expand((page) => page.items)
        .toList();
    expect(scanned.map((edge) => edge.head), matchingHeads);
    expect(scanned.map((edge) => edge.tail), [
      '${tailPrefix}0',
      '${tailPrefix}2',
      '${tailPrefix}4',
    ]);
  });

  test(
    'count, dry-run, capped delete, and lost delete response are safe',
    () async {
      final deletePrefix = '$prefix-prefix-delete:';
      await client.putVertices(
        List.generate(
          5,
          (index) =>
              VertexInput(key: '$deletePrefix$index', value: VertexValue.nil()),
        ),
      );
      expect(await client.countVerticesByPrefix(deletePrefix), BigInt.from(5));
      expect(
        await client.deleteVerticesByPrefix(
          deletePrefix,
          limit: 2,
          dryRun: true,
        ),
        BigInt.two,
      );
      expect(await client.countVerticesByPrefix(deletePrefix), BigInt.from(5));

      final fault = _CommittedResponseLossTransport(
        endpoint,
        loseProcedure: '/graph.v1.LanternService/DeleteVerticesByPrefix',
      );
      final deleting = LanternClient.connect(
        endpoint,
        allowInsecure: true,
        transport: fault,
        onClose: fault.close,
        retryPolicy: _realWireRetry,
      );
      addTearDown(deleting.close);
      await expectLater(
        deleting.deleteVerticesByPrefix(deletePrefix, limit: 2),
        throwsA(isA<LanternUnavailableException>()),
      );
      expect(
        fault.requestsFor('/graph.v1.LanternService/DeleteVerticesByPrefix'),
        1,
      );
      expect(await client.countVerticesByPrefix(deletePrefix), BigInt.from(3));
      expect(
        await client.deleteVerticesByPrefix(deletePrefix, limit: 2),
        BigInt.two,
      );
      expect(await client.countVerticesByPrefix(deletePrefix), BigInt.one);

      final edgePrefix = '$prefix-prefix-edge-delete:';
      await client.putEdges(
        List.generate(
          3,
          (index) => EdgeInput(
            tail: '$edgePrefix$index',
            head: '$prefix-prefix-edge-head:$index',
            weight: 1,
          ),
        ),
      );
      expect(
        await client.deleteEdgesByPrefix(
          tailPrefix: edgePrefix,
          limit: 1,
          dryRun: true,
        ),
        BigInt.one,
      );
      expect(
        await client.deleteEdgesByPrefix(tailPrefix: edgePrefix, limit: 1),
        BigInt.one,
      );
    },
  );

  test(
    'canceling a page stream aborts its active real transport call',
    () async {
      final scanPrefix = '$prefix-cancel-scan:';
      await client.putVertices(
        List.generate(
          3,
          (index) =>
              VertexInput(key: '$scanPrefix$index', value: VertexValue.nil()),
        ),
      );
      final blocking = _BlockingResponseTransport(
        endpoint,
        procedure: '/graph.v1.LanternService/ScanVertexKeys',
        blockRequest: 2,
      );
      final scanning = LanternClient.connect(
        endpoint,
        allowInsecure: true,
        transport: blocking,
        onClose: blocking.close,
      );
      addTearDown(scanning.close);
      final subscription = scanning
          .scanVertexKeysAll(prefix: scanPrefix, limit: 1)
          .listen((_) {});
      await blocking.blocked;
      await subscription.cancel();
      expect(blocking.wasCanceled, isTrue);
      expect(blocking.requests, 2);
    },
  );

  test(
    'full-text options and incremental latest-query-wins use real wire',
    () async {
      final searchPrefix = '$prefix-search:';
      await client.putVertices([
        VertexInput(
          key: '${searchPrefix}a',
          value: VertexValue.string('alpha bravo charlie'),
        ),
        VertexInput(
          key: '${searchPrefix}b',
          value: VertexValue.string('alpha charlie'),
        ),
        VertexInput(
          key: '${searchPrefix}c',
          value: VertexValue.string('alphabet soup'),
        ),
      ]);

      Future<List<String>> keys(String query, SearchOptions options) async {
        final result = await client.searchVertices(
          query,
          searchOptions: options,
        );
        expect(result, isA<SearchEnabled>());
        return (result as SearchEnabled).hits.map((hit) => hit.key).toList();
      }

      expect(
        await keys(
          'alpha bravo',
          SearchOptions(prefix: searchPrefix, matchMode: SearchMatchMode.any),
        ),
        containsAll(['${searchPrefix}a', '${searchPrefix}b']),
      );
      expect(
        await keys(
          'alpha bravo',
          SearchOptions(prefix: searchPrefix, matchMode: SearchMatchMode.all),
        ),
        ['${searchPrefix}a'],
      );
      expect(
        await keys(
          'alpha bravo',
          SearchOptions(
            prefix: searchPrefix,
            matchMode: SearchMatchMode.minShouldMatch,
            minShouldMatch: 2,
          ),
        ),
        ['${searchPrefix}a'],
      );
      expect(
        await keys(
          'alpha bravo',
          SearchOptions(prefix: searchPrefix, phrase: true),
        ),
        ['${searchPrefix}a'],
      );
      expect(
        await keys('alpga', SearchOptions(prefix: searchPrefix, fuzziness: 1)),
        containsAll(['${searchPrefix}a', '${searchPrefix}b']),
      );
      expect(
        await keys(
          'alph',
          SearchOptions(prefix: searchPrefix, prefixTerms: true, limit: 1),
        ),
        hasLength(1),
      );

      final session = client.incrementalSearch(
        options: IncrementalSearchOptions(
          debounce: const Duration(milliseconds: 5),
          search: SearchOptions(prefix: searchPrefix),
        ),
      );
      addTearDown(session.dispose);
      final latest = session.updates.firstWhere(
        (update) => update.phase == SearchUpdatePhase.results,
      );
      session.search('br');
      session.search('bravo');
      final update = await latest;
      expect(update.query, 'bravo');
      expect(update.hits.map((hit) => hit.key), ['${searchPrefix}a']);
    },
  );

  test('shared production search conformance manifest', () async {
    final manifest =
        jsonDecode(
              io.File(
                '../../testdata/search/conformance.json',
              ).readAsStringSync(),
            )
            as Map<String, dynamic>;
    expect(manifest['version'], 'search-conformance-v1');

    SearchOptions options(Map<String, dynamic> raw) {
      final mode = switch (raw['match_mode']) {
        'any' => SearchMatchMode.any,
        'all' => SearchMatchMode.all,
        'min-should' => SearchMatchMode.minShouldMatch,
        _ => null,
      };
      return SearchOptions(
        limit: (raw['limit'] as num?)?.toInt() ?? 0,
        prefix: raw['prefix'] as String? ?? '',
        matchMode: mode,
        minShouldMatch: (raw['min_should_match'] as num?)?.toInt(),
        phrase: raw['phrase'] as bool?,
        fuzziness: (raw['fuzziness'] as num?)?.toInt(),
        prefixTerms: raw['prefix_terms'] as bool?,
      );
    }

    final vertices = (manifest['vertices'] as List<dynamic>)
        .cast<Map<String, dynamic>>()
        .map(
          (vertex) => VertexInput(
            key: vertex['key'] as String,
            value: VertexValue.string(vertex['value'] as String),
          ),
        )
        .toList();
    await client.putVertices(vertices);

    for (final fixture
        in (manifest['queries'] as List<dynamic>)
            .cast<Map<String, dynamic>>()) {
      final result = await client.searchVertices(
        fixture['query'] as String,
        searchOptions: options(
          (fixture['options'] as Map<dynamic, dynamic>).cast<String, dynamic>(),
        ),
      );
      expect(result, isA<SearchEnabled>(), reason: fixture['name'] as String);
      expect(
        (result as SearchEnabled).hits.map((hit) => hit.key).toList(),
        (fixture['want_keys'] as List<dynamic>).cast<String>(),
        reason: fixture['name'] as String,
      );
      expect(result.hits.every((hit) => hit.score.isFinite), isTrue);
    }

    for (final fixture
        in (manifest['invalid'] as List<dynamic>)
            .cast<Map<String, dynamic>>()) {
      await expectLater(
        client.searchVertices(
          fixture['query'] as String,
          searchOptions: options(
            (fixture['options'] as Map<dynamic, dynamic>)
                .cast<String, dynamic>(),
          ),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
        reason: fixture['name'] as String,
      );
    }

    final cancellation = (manifest['cancellation'] as Map<dynamic, dynamic>)
        .cast<String, dynamic>();
    final cancellationToken = LanternCancellationToken()
      ..cancel('shared conformance pre-canceled');
    await expectLater(
      client.searchVertices(
        cancellation['query'] as String,
        searchOptions: options(
          (cancellation['options'] as Map<dynamic, dynamic>)
              .cast<String, dynamic>(),
        ),
        options: LanternCallOptions(cancellation: cancellationToken),
      ),
      throwsA(isA<LanternCanceledException>()),
      reason: cancellation['name'] as String,
    );

    final errorEndpoints = <String, String?>{
      'disabled':
          io.Platform.environment['LANTERN_DART_SEARCH_DISABLED_ENDPOINT'],
      'positions-disabled': io
          .Platform
          .environment['LANTERN_DART_SEARCH_POSITIONS_DISABLED_ENDPOINT'],
      'query-budget':
          io.Platform.environment['LANTERN_DART_SEARCH_BUDGET_ENDPOINT'],
    };
    for (final fixture
        in (manifest['typed_errors'] as List<dynamic>)
            .cast<Map<String, dynamic>>()) {
      final environment = fixture['environment'] as String;
      final errorEndpoint = errorEndpoints[environment];
      expect(errorEndpoint, isNotNull, reason: '$environment endpoint missing');
      final errorClient = LanternClient.connect(
        Uri.parse(errorEndpoint!),
        allowInsecure: errorEndpoint.startsWith('http://'),
      );
      addTearDown(errorClient.close);
      final call = errorClient.searchVertices(
        fixture['query'] as String,
        searchOptions: options(
          (fixture['options'] as Map<dynamic, dynamic>).cast<String, dynamic>(),
        ),
      );
      switch (fixture['reason']) {
        case 'search-disabled':
          expect(await call, isA<SearchDisabled>());
        case 'positions-disabled':
          await expectLater(
            call,
            throwsA(
              isA<LanternFailedPreconditionException>().having(
                (error) => error.searchReason,
                'searchReason',
                SearchErrorReason.searchPositionsDisabled,
              ),
            ),
          );
        case 'query_bytes':
          await expectLater(
            call,
            throwsA(
              isA<LanternResourceExhaustedException>()
                  .having(
                    (error) => error.searchReason,
                    'searchReason',
                    SearchErrorReason.searchWorkBudgetExhausted,
                  )
                  .having(
                    (error) => error.searchWorkKind,
                    'searchWorkKind',
                    'query_bytes',
                  ),
            ),
          );
      }
    }
  });

  test(
    'all traversal families preserve family sentinels and Edge TTL',
    () async {
      final graphPrefix = '$prefix-traversal:';
      final seed = '${graphPrefix}seed';
      final expiration = DateTime.now().toUtc().add(const Duration(minutes: 5));
      await client.putEdges([
        EdgeInput(
          tail: seed,
          head: '${graphPrefix}a',
          weight: 3,
          expiresAt: expiration,
        ),
        EdgeInput(
          tail: seed,
          head: '${graphPrefix}b',
          weight: 2,
          expiresAt: expiration,
        ),
        EdgeInput(
          tail: '${graphPrefix}a',
          head: '${graphPrefix}b',
          weight: 1,
          expiresAt: expiration,
        ),
      ]);

      final bfs = await client.illuminate(
        seed,
        traversal: const BfsOptions(step: 1, fanOut: 5),
        weighting: TraversalWeighting.raw,
        vertexPrefix: graphPrefix,
      );
      expect(bfs.vertices.keys, containsAll([seed, '${graphPrefix}a']));
      expect(bfs.edge(seed, '${graphPrefix}a')!.expiration, isNotNull);
      expect(
        bfs
            .edge(seed, '${graphPrefix}a')!
            .expiration!
            .difference(expiration)
            .inMilliseconds
            .abs(),
        lessThan(2),
      );
      expect(bfs.edgeWeights[seed]!['${graphPrefix}a'], 3);

      final ppr = await client.illuminate(
        seed,
        traversal: const PprOptions(topN: 0),
        vertexPrefix: graphPrefix,
      );
      expect(ppr.vertices, contains(seed));
      final community = await client.illuminate(
        seed,
        traversal: const LocalCommunityOptions(maxSize: 0),
        vertexPrefix: graphPrefix,
      );
      expect(community.vertices, contains(seed));
    },
  );

  test(
    'cold-start ranking and explicit status snapshots use real wire',
    () async {
      final degreePrefix = '$prefix-degree:';
      final center = '${degreePrefix}center';
      await client.putVertices([
        VertexInput(key: '${degreePrefix}tie-a', value: VertexValue.nil()),
        VertexInput(key: '${degreePrefix}tie-b', value: VertexValue.nil()),
      ]);
      await client.putEdges([
        EdgeInput(tail: center, head: '${degreePrefix}a', weight: 4),
        EdgeInput(tail: center, head: '${degreePrefix}b', weight: 2),
        EdgeInput(tail: '${degreePrefix}c', head: center, weight: 1),
      ]);

      final outgoing = await client.topVerticesByDegree(
        prefix: degreePrefix,
        direction: DegreeDirection.out,
      );
      expect(outgoing.first.key, center);
      expect(outgoing.first.degree, BigInt.two);
      final incoming = await client.topVerticesByDegree(
        prefix: degreePrefix,
        direction: DegreeDirection.incoming,
        weighted: true,
      );
      expect(incoming.first.weightedDegree, greaterThanOrEqualTo(4));

      final tieOnly = await client.topVerticesByDegree(
        prefix: '${degreePrefix}tie-',
      );
      expect(tieOnly.map((entry) => entry.key), [
        '${degreePrefix}tie-a',
        '${degreePrefix}tie-b',
      ]);

      final server = await client.getServerStatus();
      expect(server.version, isNotEmpty);
      expect(server.startedAt, isNotNull);
      expect(server.vertexCount, greaterThan(BigInt.zero));
      final replication = await client.getReplicationStatus();
      expect(replication.enabled, isFalse);
      expect(replication.peers, isEmpty);
    },
  );

  final disabledSearchEndpoint =
      io.Platform.environment['LANTERN_DART_SEARCH_DISABLED_ENDPOINT'];
  test(
    'search-disabled fixture maps to a calm typed state',
    () async {
      final disabledClient = LanternClient.connect(
        Uri.parse(disabledSearchEndpoint!),
        allowInsecure: disabledSearchEndpoint.startsWith('http://'),
      );
      addTearDown(disabledClient.close);
      final result = await disabledClient.searchVertices('anything');
      expect(result, isA<SearchDisabled>());
    },
    skip: disabledSearchEndpoint == null || disabledSearchEndpoint.isEmpty
        ? 'LANTERN_DART_SEARCH_DISABLED_ENDPOINT is not configured'
        : false,
  );

  test(
    'connection refusal exhausts the bounded real transport budget',
    () async {
      final unavailable = await io.HttpServer.bind(
        io.InternetAddress.loopbackIPv4,
        0,
      );
      final port = unavailable.port;
      await unavailable.close(force: true);
      final retrying = LanternClient.connect(
        Uri.parse('http://127.0.0.1:$port'),
        allowInsecure: true,
        retryPolicy: _realWireRetry,
      );
      addTearDown(retrying.close);
      await expectLater(
        retrying.getVertex('missing'),
        throwsA(isA<LanternRetryExhaustedException>()),
      );
    },
  );
}

const _realWireRetry = RetryPolicy(
  maxAttempts: 3,
  baseDelay: Duration(milliseconds: 1),
  maxDelay: Duration(milliseconds: 2),
);

final class _CommittedResponseLossTransport implements connect.Transport {
  _CommittedResponseLossTransport(Uri endpoint, {required String loseProcedure})
    : _loseProcedure = loseProcedure {
    _httpClient = io.HttpClient();
    _inner = connect_protocol.Transport(
      baseUrl: endpoint.toString(),
      codec: const ProtoCodec(),
      httpClient: connect_io.createHttpClient(_httpClient),
    );
  }

  late final io.HttpClient _httpClient;
  late final connect.Transport _inner;
  final String _loseProcedure;
  final Map<String, int> _requests = {};
  var _lossPending = true;

  int requestsFor(String procedure) => _requests[procedure] ?? 0;

  Future<void> close() async {
    _httpClient.close(force: true);
  }

  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    _requests.update(spec.procedure, (value) => value + 1, ifAbsent: () => 1);
    final response = await _inner.unary(spec, input, options);
    if (_lossPending && spec.procedure == _loseProcedure) {
      _lossPending = false;
      throw connect.ConnectException(
        connect.Code.unavailable,
        'simulated committed response loss',
      );
    }
    return response;
  }

  @override
  Future<connect.StreamResponse<I, O>> stream<
    I extends Object,
    O extends Object
  >(connect.Spec<I, O> spec, Stream<I> input, [connect.CallOptions? options]) {
    return _inner.stream(spec, input, options);
  }
}

final class _BlockingResponseTransport implements connect.Transport {
  _BlockingResponseTransport(
    Uri endpoint, {
    required this.procedure,
    required this.blockRequest,
  }) {
    _httpClient = io.HttpClient();
    _inner = connect_protocol.Transport(
      baseUrl: endpoint.toString(),
      codec: const ProtoCodec(),
      httpClient: connect_io.createHttpClient(_httpClient),
    );
  }

  final String procedure;
  final int blockRequest;
  late final io.HttpClient _httpClient;
  late final connect.Transport _inner;
  final Completer<void> _blocked = Completer<void>();
  var requests = 0;
  var wasCanceled = false;

  Future<void> get blocked => _blocked.future;

  Future<void> close() async {
    _httpClient.close(force: true);
  }

  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    if (spec.procedure == procedure) requests++;
    final response = await _inner.unary(spec, input, options);
    if (spec.procedure == procedure && requests == blockRequest) {
      _blocked.complete();
      await options!.signal!.future;
      wasCanceled = true;
      throw connect.ConnectException(
        connect.Code.canceled,
        'expected page cancellation',
      );
    }
    return response;
  }

  @override
  Future<connect.StreamResponse<I, O>> stream<
    I extends Object,
    O extends Object
  >(connect.Spec<I, O> spec, Stream<I> input, [connect.CallOptions? options]) {
    return _inner.stream(spec, input, options);
  }
}
