import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('plural reads chunk and preserve present and missing values', () async {
    final requests = <List<String>>[];
    final transport =
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (request, context) {
                requests.add(request.keys.toList());
                return graph.GetVerticesResponse(
                  vertices: request.keys
                      .where((key) => key != 'missing')
                      .map((key) => graph.Vertex(key: key, string: key)),
                  missing: request.keys.where((key) => key == 'missing'),
                );
              },
            )
            .build();
    final client = _client(transport);

    final result = await client.getVertices([
      'a',
      'missing',
      'b',
      'c',
      'a',
    ], batchSize: 2);
    expect(requests, [
      ['a', 'missing'],
      ['b', 'c'],
      ['a'],
    ]);
    expect(result.vertices.map((value) => value.key), ['a', 'b', 'c', 'a']);
    expect(result.missing, ['missing']);
  });

  test('duplicate edge refs remain explicit and index-preserving', () async {
    late List<graph.EdgeKey> requested;
    final transport =
        FakeTransportBuilder().unary<
          graph.GetEdgesRequest,
          graph.GetEdgesResponse
        >(LanternService.getEdges, (request, context) {
          requested = request.edges.toList();
          return graph.GetEdgesResponse(
            edges: request.edges.map(
              (edge) => graph.Edge(tail: edge.tail, head: edge.head, weight: 1),
            ),
          );
        }).build();
    final client = _client(transport);
    const ref = EdgeRef('a', 'b');

    final result = await client.getEdges([ref, ref]);
    expect(requested, hasLength(2));
    expect(result.edges, hasLength(2));
    expect(
      result.edges.every((edge) => edge.tail == 'a' && edge.head == 'b'),
      isTrue,
    );
  });

  test(
    'relative TTL samples the injected clock once before chunking',
    () async {
      var clockCalls = 0;
      final expirations = <DateTime?>[];
      final transport =
          FakeTransportBuilder().unary<
            graph.PutVerticesRequest,
            graph.PutVerticesResponse
          >(LanternService.putVertices, (request, context) {
            expirations.addAll(
              request.vertices.map(
                (value) =>
                    value.hasExpiration()
                        ? value.expiration.toDateTime()
                        : null,
              ),
            );
            return graph.PutVerticesResponse(written: request.vertices.length);
          }).build();
      final client = LanternClient.connect(
        Uri.parse('https://example.test'),
        transport: transport,
        clock: () {
          clockCalls++;
          return DateTime.parse('2026-07-12T00:00:00Z');
        },
      );

      final result = await client.putVertices([
        VertexInput(
          key: 'a',
          value: VertexValue.nil(),
          expiresIn: const Duration(minutes: 5),
        ),
        VertexInput(
          key: 'b',
          value: VertexValue.nil(),
          expiresIn: const Duration(minutes: 5),
        ),
        VertexInput(key: 'permanent', value: VertexValue.nil()),
      ], batchSize: 1);

      expect(result.written, 3);
      expect(clockCalls, 1);
      expect(expirations, [
        DateTime.parse('2026-07-12T00:05:00Z'),
        DateTime.parse('2026-07-12T00:05:00Z'),
        null,
      ]);
    },
  );

  test('invalid TTL and contrib ID fail before network I/O', () async {
    var calls = 0;
    final transport =
        FakeTransportBuilder()
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (request, context) {
                calls++;
                return graph.PutVerticesResponse();
              },
            )
            .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
              LanternService.addEdges,
              (request, context) {
                calls++;
                return graph.AddEdgesResponse();
              },
            )
            .build();
    final client = _client(transport);

    await expectLater(
      client.putVertex(
        VertexInput(
          key: 'zero',
          value: VertexValue.nil(),
          expiresIn: Duration.zero,
        ),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.putVertex(
        VertexInput(
          key: 'both',
          value: VertexValue.nil(),
          expiresIn: const Duration(seconds: 1),
          expiresAt: DateTime.now(),
        ),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.addEdge(
        EdgeInput(tail: 'a', head: 'b', weight: 1, contribId: Uint8List(23)),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.addEdge(
        EdgeInput(tail: 'a', head: 'b', weight: 1, contribId: Uint8List(24)),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(calls, 0);
  });

  test(
    'partial write reports confirmed committed count and typed cause',
    () async {
      var calls = 0;
      final transport =
          FakeTransportBuilder().unary<
            graph.PutVerticesRequest,
            graph.PutVerticesResponse
          >(LanternService.putVertices, (request, context) {
            calls++;
            if (calls == 2) {
              throw connect.ConnectException(
                connect.Code.unavailable,
                'forced second-chunk failure',
              );
            }
            return graph.PutVerticesResponse(written: request.vertices.length);
          }).build();
      final client = _client(transport);

      await expectLater(
        client.putVertices(
          List.generate(
            3,
            (index) =>
                VertexInput(key: '$index', value: VertexValue.int32(index)),
          ),
          batchSize: 2,
        ),
        throwsA(
          isA<BatchException>()
              .having((error) => error.committed, 'committed', 2)
              .having(
                (error) => error.cause,
                'cause',
                isA<LanternUnavailableException>(),
              ),
        ),
      );
    },
  );

  test('server batch rejection remains a typed failure', () async {
    final transport =
        FakeTransportBuilder()
            .unary<graph.PutEdgesRequest, graph.PutEdgesResponse>(
              LanternService.putEdges,
              (request, context) =>
                  throw connect.ConnectException(
                    connect.Code.resourceExhausted,
                    'server max batch exceeded',
                  ),
            )
            .build();
    final client = _client(transport);

    await expectLater(
      client.putEdges([EdgeInput(tail: 'a', head: 'b', weight: 1)]),
      throwsA(isA<LanternResourceExhaustedException>()),
    );
  });

  test('cancellation between chunks reports partial progress', () async {
    final cancellation = LanternCancellationToken();
    final transport =
        FakeTransportBuilder()
            .unary<graph.PutEdgesRequest, graph.PutEdgesResponse>(
              LanternService.putEdges,
              (request, context) {
                cancellation.cancel('stop after first chunk');
                return graph.PutEdgesResponse(written: request.edges.length);
              },
            )
            .build();
    final client = _client(transport);

    await expectLater(
      client.putEdges(
        [
          EdgeInput(tail: 'a', head: 'b', weight: 1),
          EdgeInput(tail: 'b', head: 'c', weight: 1),
        ],
        batchSize: 1,
        options: LanternCallOptions(cancellation: cancellation),
      ),
      throwsA(
        isA<BatchException>()
            .having((error) => error.committed, 'committed', 1)
            .having(
              (error) => error.cause,
              'cause',
              isA<LanternCanceledException>(),
            ),
      ),
    );
  });

  test(
    'add is additive, put is overwrite, and contrib IDs stay aligned',
    () async {
      final id = Uint8List(24)..[0] = 7;
      final addRequests = <graph.AddEdgesRequest>[];
      final putRequests = <graph.PutEdgesRequest>[];
      final transport =
          FakeTransportBuilder()
              .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
                LanternService.addEdges,
                (request, context) {
                  addRequests.add(request.deepCopy());
                  return graph.AddEdgesResponse(
                    written: request.edges.length,
                    effectiveWeights: [3, 5],
                  );
                },
              )
              .unary<graph.PutEdgesRequest, graph.PutEdgesResponse>(
                LanternService.putEdges,
                (request, context) {
                  putRequests.add(request.deepCopy());
                  return graph.PutEdgesResponse(written: request.edges.length);
                },
              )
              .build();
      final client = _client(transport);
      final inputs = [
        EdgeInput(tail: 'a', head: 'b', weight: 1),
        EdgeInput(tail: 'b', head: 'c', weight: 2, contribId: id),
      ];

      final addResult = await client.addEdges(inputs);
      expect(addResult.written, 2);
      expect(addResult.effectiveWeights, [3, 5]);
      expect(addRequests.single.contribIds, [<int>[], id]);
      expect(await client.putEdges(inputs), 2);
      expect(putRequests.single.edges.map((edge) => edge.weight), [1, 2]);
    },
  );

  test(
    'singular facades retain missing, skipped, and existed contracts',
    () async {
      final transport =
          FakeTransportBuilder()
              .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
                LanternService.getVertices,
                (request, context) =>
                    graph.GetVerticesResponse(missing: request.keys),
              )
              .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
                LanternService.putVertices,
                (request, context) => graph.PutVerticesResponse(
                  written: request.ifAbsent ? 0 : request.vertices.length,
                  skippedKeys:
                      request.ifAbsent ? [request.vertices.single.key] : [],
                ),
              )
              .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
                LanternService.deleteVertices,
                (request, context) => graph.DeleteVerticesResponse(deleted: 0),
              )
              .unary<graph.GetEdgesRequest, graph.GetEdgesResponse>(
                LanternService.getEdges,
                (request, context) =>
                    graph.GetEdgesResponse(missing: request.edges),
              )
              .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
                LanternService.deleteEdges,
                (request, context) => graph.DeleteEdgesResponse(deleted: 0),
              )
              .build();
      final client = _client(transport);

      await expectLater(
        client.getVertex('missing'),
        throwsA(isA<LanternNotFoundException>()),
      );
      expect(
        await client.putVertexIfAbsent(
          VertexInput(key: 'existing', value: VertexValue.nil()),
        ),
        isFalse,
      );
      expect(await client.deleteVertex('missing'), isFalse);
      await expectLater(
        client.getEdge(const EdgeRef('a', 'b')),
        throwsA(isA<LanternNotFoundException>()),
      );
      expect(await client.deleteEdge(const EdgeRef('a', 'b')), isFalse);
    },
  );

  test('empty batches do no I/O and batch ceilings fail locally', () async {
    final client = _client(FakeTransportBuilder().build());
    expect((await client.getVertices([])).vertices, isEmpty);
    expect((await client.putVertices([])).written, 0);
    expect(await client.deleteVertices([]), 0);
    expect((await client.getEdges([])).edges, isEmpty);
    expect((await client.addEdges([])).effectiveWeights, isEmpty);
    expect(await client.putEdges([]), 0);
    expect(await client.deleteEdges([]), 0);

    await expectLater(
      client.getVertices(List.filled(65537, 'key')),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.getVertices(['key'], batchSize: 0),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
  });
}

LanternClient _client(connect.Transport transport) => LanternClient.connect(
  Uri.parse('https://example.test'),
  transport: transport,
);
