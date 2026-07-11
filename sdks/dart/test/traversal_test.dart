import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('BFS emits final positive defaults and shared traversal axes', () async {
    late graph.IlluminateRequest captured;
    final transport = _capture((request) {
      captured = request.clone();
      return graph.IlluminateResponse(graph: graph.Graph());
    });
    final result = await _client(transport).illuminate(
      'seed',
      traversal: const BfsOptions(
        step: 2,
        fanOut: 3,
        objective: TraversalObjective.minimize,
        reduction: TraversalReduction.shortestPathTree,
      ),
      weighting: TraversalWeighting.bm25,
      vertexPrefix: 'p:',
    );

    expect(result.vertices, isEmpty);
    expect(captured.whichParams(), graph.IlluminateRequest_Params.bfs);
    expect(captured.bfs.step, 2);
    expect(captured.bfs.fanOut, 3);
    expect(captured.bfs.objective, graph.Objective.OBJECTIVE_MINIMIZE);
    expect(
      captured.bfs.reduction,
      graph.Reduction.REDUCTION_SHORTEST_PATH_TREE,
    );
    expect(captured.weighting, graph.Weighting.WEIGHTING_BM25);
    expect(captured.vertexPrefix, 'p:');
  });

  test('PPR and community preserve family-native zero sentinels', () async {
    final requests = <graph.IlluminateRequest>[];
    final transport = _capture((request) {
      requests.add(request.clone());
      return graph.IlluminateResponse(graph: graph.Graph());
    });
    final client = _client(transport);

    await client.illuminate('seed', traversal: const PprOptions());
    await client.illuminate(
      'seed',
      traversal: const LocalCommunityOptions(
        restartProbability: 0.25,
        epsilon: 0.001,
        objective: TraversalObjective.maximize,
        reduction: TraversalReduction.minimumSpanningTree,
      ),
    );

    expect(requests[0].whichParams(), graph.IlluminateRequest_Params.ppr);
    expect(requests[0].ppr.topN, 0);
    expect(requests[0].ppr.restartProb, 0);
    expect(requests[0].ppr.epsilon, 0);
    expect(requests[1].whichParams(), graph.IlluminateRequest_Params.community);
    expect(requests[1].community.maxSize, 0);
    expect(requests[1].community.restartProb, closeTo(0.25, 1e-6));
    expect(requests[1].community.epsilon, closeTo(0.001, 1e-6));
    expect(
      requests[1].community.reduction,
      graph.Reduction.REDUCTION_MINIMUM_SPANNING_TREE,
    );
  });

  test(
    'Graph retains edge expiration and deterministic derived views',
    () async {
      final expiration = DateTime.parse('2026-07-12T12:34:56Z');
      final transport = _capture(
        (request) => graph.IlluminateResponse(
          graph: graph.Graph(
            vertices: [
              graph.Vertex(key: 'b', nil: true),
              graph.Vertex(key: 'a', string: 'a'),
            ],
            edges: [
              graph.Edge(tail: 'b', head: 'a', weight: 2),
              graph.Edge(
                tail: 'a',
                head: 'b',
                weight: 1.5,
                expiration: Timestamp.fromDateTime(expiration),
              ),
            ],
          ),
        ),
      );
      final result = await _client(
        transport,
      ).illuminate('a', traversal: const BfsOptions(step: 1, fanOut: 2));

      expect(result.vertices.keys, ['a', 'b']);
      expect(result.allEdges.map((edge) => '${edge.tail}:${edge.head}'), [
        'a:b',
        'b:a',
      ]);
      expect(result.edge('a', 'b')!.expiration, expiration);
      expect(result.edgeWeights['a']!['b'], 1.5);
      expect(result.outgoing('a').single.head, 'b');
      expect(result.incoming('a').single.tail, 'b');
      expect(result.adjacentKeys('a'), ['b']);
      expect(
        () => result.edges['a']!['c'] = result.edges['a']!['b']!,
        throwsUnsupportedError,
      );
    },
  );

  test('invalid family domains fail locally without a request', () async {
    var calls = 0;
    final transport = _capture((request) {
      calls++;
      return graph.IlluminateResponse(graph: graph.Graph());
    });
    final client = _client(transport);
    final invalid = <TraversalOptions>[
      const BfsOptions(step: 0, fanOut: 1),
      const BfsOptions(step: 1, fanOut: 0),
      const PprOptions(topN: -1),
      const PprOptions(restartProbability: 1),
      const PprOptions(epsilon: 0),
      const LocalCommunityOptions(maxSize: -1),
      const LocalCommunityOptions(restartProbability: double.nan),
    ];
    for (final traversal in invalid) {
      await expectLater(
        client.illuminate('seed', traversal: traversal),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    }
    expect(calls, 0);
  });
}

connect.Transport _capture(
  graph.IlluminateResponse Function(graph.IlluminateRequest request) handler,
) => FakeTransportBuilder()
    .unary<graph.IlluminateRequest, graph.IlluminateResponse>(
      LanternService.illuminate,
      (request, context) => handler(request),
    )
    .build();

LanternClient _client(connect.Transport transport) => LanternClient.connect(
  Uri.parse('https://example.test'),
  transport: transport,
);
