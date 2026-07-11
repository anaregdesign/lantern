import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('decay contributions telescope to the requested live weight', () {
    final base = DateTime.parse('2026-07-12T00:00:00Z');
    final contributions = decayContributions(
      tail: 'a',
      head: 'b',
      options: const DecayOptions(
        initialWeight: 16,
        ratio: 0.5,
        steps: 5,
        interval: Duration(minutes: 1),
      ),
      base: base,
    );

    expect(contributions.map((value) => value.weight), [8, 4, 2, 1, 1]);
    expect(
      contributions.map((value) => value.expiresAt),
      List.generate(5, (index) => base.add(Duration(minutes: index + 1))),
    );
    expect(
      contributions.fold<double>(0, (sum, value) => sum + value.weight),
      16,
    );
  });

  test('negative reinforcement and half-life helpers preserve semantics', () {
    final options = halfLifeDecay(
      initialWeight: -8,
      halfLife: const Duration(minutes: 2),
      interval: const Duration(minutes: 1),
      horizon: const Duration(minutes: 6),
    );
    expect(options.ratio, closeTo(0.70710678, 1e-7));
    expect(options.steps, 6);
    final contributions = decayContributions(
      tail: 'a',
      head: 'b',
      options: options,
      base: DateTime.parse('2026-07-12T00:00:00Z'),
    );
    expect(contributions.every((value) => value.weight < 0), isTrue);
    expect(
      contributions.fold<double>(0, (sum, value) => sum + value.weight),
      closeTo(-8, 1e-5),
    );
  });

  test('invalid decay specifications fail locally', () {
    final invalid = [
      const DecayOptions(
        initialWeight: 0,
        ratio: 0.5,
        steps: 2,
        interval: Duration(seconds: 1),
      ),
      const DecayOptions(
        initialWeight: 1,
        ratio: 1,
        steps: 2,
        interval: Duration(seconds: 1),
      ),
      const DecayOptions(
        initialWeight: 1,
        ratio: 0.5,
        steps: 17,
        interval: Duration(seconds: 1),
      ),
      const DecayOptions(
        initialWeight: 1,
        ratio: 0.5,
        steps: 2,
        interval: Duration.zero,
      ),
    ];
    for (final options in invalid) {
      expect(
        () => decayContributions(
          tail: 'a',
          head: 'b',
          options: options,
          base: DateTime.now(),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    }
  });

  test(
    'AddDecayingEdge sends one curve and returns final effective weight',
    () async {
      late graph.AddEdgesRequest captured;
      final transport =
          FakeTransportBuilder()
              .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
                LanternService.addEdges,
                (request, context) {
                  captured = request.deepCopy();
                  return graph.AddEdgesResponse(
                    written: request.edges.length,
                    effectiveWeights: [8, 12, 14, 15, 16],
                  );
                },
              )
              .build();
      final client = LanternClient.connect(
        Uri.parse('https://example.test'),
        transport: transport,
        clock: () => DateTime.parse('2026-07-12T00:00:00Z'),
        idempotentAdds: true,
        retryPolicy: const RetryPolicy(
          maxAttempts: 2,
          baseDelay: Duration(microseconds: 1),
          maxDelay: Duration(microseconds: 1),
        ),
      );

      final effective = await client.addDecayingEdge(
        tail: 'a',
        head: 'b',
        options: const DecayOptions(
          initialWeight: 16,
          ratio: 0.5,
          steps: 5,
          interval: Duration(minutes: 1),
        ),
      );
      expect(effective, 16);
      expect(captured.edges.map((edge) => edge.weight), [8, 4, 2, 1, 1]);
      expect(captured.contribIds, hasLength(5));
      expect(captured.contribIds.every((id) => id.length == 24), isTrue);
    },
  );

  test('decaying Add without IDs is not retried', () async {
    var calls = 0;
    final transport =
        FakeTransportBuilder().unary<
          graph.AddEdgesRequest,
          graph.AddEdgesResponse
        >(LanternService.addEdges, (request, context) {
          calls++;
          throw connect.ConnectException(connect.Code.unavailable, 'lost');
        }).build();
    final client = LanternClient.connect(
      Uri.parse('https://example.test'),
      transport: transport,
      retryPolicy: const RetryPolicy(
        maxAttempts: 3,
        baseDelay: Duration(microseconds: 1),
        maxDelay: Duration(microseconds: 1),
      ),
    );

    await expectLater(
      client.addDecayingEdge(
        tail: 'a',
        head: 'b',
        options: const DecayOptions(
          initialWeight: 1,
          ratio: 0.5,
          steps: 2,
          interval: Duration(seconds: 1),
        ),
      ),
      throwsA(isA<LanternUnavailableException>()),
    );
    expect(calls, 1);
  });
}
