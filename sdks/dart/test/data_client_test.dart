import 'dart:async';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

final _endpoint = Uri.parse('http://localhost:6380');
LanternSessionBinding binding({String account = 'test', String? uid}) =>
    LanternSessionBinding.application(
      serviceOrigin: _endpoint,
      securityDomain: 'test-dataset',
      accountIdentity: account,
      authorityBinding: 'test-generation',
      userId: uid,
    );
DirectLanternDataClient direct(
  LanternTransport transport, {
  LanternClock? clock,
  TokenProvider? token,
  int batchSize = 1000,
  int maxOperations = 128,
  int maxWaiters = 128,
}) => DirectLanternDataClient(
  LanternClient.connect(
    _endpoint,
    allowInsecure: true,
    transport: transport,
    clock: clock,
    tokenProvider: token,
    retryPolicy: const RetryPolicy(maxAttempts: 3),
  ),
  binding: binding(),
  batchSize: batchSize,
  maxOperations: maxOperations,
  maxWaiters: maxWaiters,
);
VertexInput vertex(String key, {DateTime? expiresAt}) =>
    VertexInput(key: key, value: const NilValue(), expiresAt: expiresAt);
Future<void> settle() => Future<void>.delayed(const Duration(milliseconds: 25));

void main() {
  test('ordinary plural Delete preserves duplicate/false positions', () async {
    final client = direct(
      FakeTransportBuilder()
          .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
            LanternService.deleteVertices,
            (r, _) {
              expect(r.keys, ['k', 'k', 'missing']);
              return graph.DeleteVerticesResponse(
                deleted: 1,
                existed: [true, false, false],
              );
            },
          )
          .build(),
    );
    addTearDown(client.close);
    final result = await client.deleteVertices(['k', 'k', 'missing']);
    expect(result.items.map((r) => (r as MutationKnownEffect<bool>).effect), [
      true,
      false,
      false,
    ]);
    expect(() => result.items.clear(), throwsUnsupportedError);
  });
  test(
    'Put reply keeps original applied handling after response-time expiry',
    () async {
      var now = DateTime.utc(2026);
      final expiration = now.add(const Duration(seconds: 1));
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (r, _) {
                now = expiration.add(const Duration(seconds: 1));
                return graph.PutVerticesResponse(
                  outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
                );
              },
            )
            .build(),
        clock: () => now,
      );
      addTearDown(client.close);
      expect(
        (await client.putVertex(vertex('k', expiresAt: expiration))
                as MutationKnownEffect<PutOutcome>)
            .effect,
        PutOutcome.appliedAndLive,
      );
    },
  );
  test('born-expired Put rejects the whole input before token/RPC', () async {
    var tokens = 0;
    final now = DateTime.utc(2026);
    final client = direct(
      FakeTransportBuilder().build(),
      clock: () => now,
      token: () {
        tokens++;
        return null;
      },
      batchSize: 1,
    );
    addTearDown(client.close);
    await expectLater(
      client.putVertices([vertex('live'), vertex('expired', expiresAt: now)]),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(tokens, 0);
  });
  test(
    'mixed chunks retain handling, ambiguous failure and unsent suffix',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder().unary<
          graph.DeleteEdgesRequest,
          graph.DeleteEdgesResponse
        >(LanternService.deleteEdges, (r, _) {
          calls++;
          if (calls == 1) {
            return graph.DeleteEdgesResponse(deleted: 0, existed: [false]);
          }
          if (calls == 2) {
            return graph.DeleteEdgesResponse(
              acceptance: graph.MutationAcceptance(
                kind: graph
                    .MutationAcceptanceKind
                    .MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED,
              ),
            );
          }
          throw connect.ConnectException(
            connect.Code.unavailable,
            'lost reply',
          );
        }).build(),
        batchSize: 1,
      );
      addTearDown(client.close);
      try {
        await client.deleteEdges(List.filled(4, const EdgeRef('a', 'b')));
        fail('expected partial failure');
      } on MutationBatchException<bool> catch (error) {
        expect(error.items.map((i) => i.state), [
          MutationItemState.handled,
          MutationItemState.handled,
          MutationItemState.unknown,
          MutationItemState.notAttempted,
        ]);
        expect(error.items[0].reply, isA<MutationKnownEffect<bool>>());
        expect(error.items[1].reply, isA<MutationAcceptedUndisclosed<bool>>());
        expect(error.items.map((i) => i.index), [0, 1, 2, 3]);
      }
      expect(
        calls,
        3,
      ); // Configured low-level retry must not nest inside attempts.
    },
  );
  test(
    'mixed known and undisclosed chunks complete an aligned batch',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder().unary<
          graph.PutEdgesRequest,
          graph.PutEdgesResponse
        >(LanternService.putEdges, (r, _) {
          calls++;
          return calls == 1
              ? graph.PutEdgesResponse(
                  outcomes: [graph.PutOutcome.PUT_OUTCOME_SUPERSEDED],
                )
              : graph.PutEdgesResponse(
                  acceptance: graph.MutationAcceptance(
                    kind: graph
                        .MutationAcceptanceKind
                        .MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED,
                  ),
                );
        }).build(),
        batchSize: 1,
      );
      addTearDown(client.close);
      final result = await client.putEdges(
        List.generate(2, (_) => EdgeInput(tail: 'a', head: 'b', weight: 1)),
      );
      expect(
        (result.items.first as MutationKnownEffect<PutOutcome>).effect,
        PutOutcome.superseded,
      );
      expect(result.items.last, isA<MutationAcceptedUndisclosed<PutOutcome>>());
    },
  );
  test(
    'malformed Delete count/length fails closed for entire observed chunk',
    () async {
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (r, _) => graph.DeleteVerticesResponse(
                deleted: 1,
                existed: [false, false],
              ),
            )
            .build(),
      );
      addTearDown(client.close);
      await expectLater(
        client.deleteVertices(['a', 'b']),
        throwsA(
          isA<MutationBatchException<bool>>().having(
            (e) => e.items.every((i) => i.state == MutationItemState.unknown),
            'all uncertain',
            true,
          ),
        ),
      );
    },
  );
  test(
    'pre-dispatch token failure is failed, with a definitely unsent suffix',
    () async {
      final client = direct(
        FakeTransportBuilder().build(),
        batchSize: 1,
        token: () => throw StateError('private token provider failure'),
      );
      addTearDown(client.close);
      await expectLater(
        client.deleteVertices(['a', 'b']),
        throwsA(
          isA<MutationBatchException<bool>>().having(
            (e) => e.items.map((i) => i.state).toList(),
            'states',
            [MutationItemState.failed, MutationItemState.notAttempted],
          ),
        ),
      );
    },
  );
  test(
    'cacheOnly and empty plurals do not acquire tokens or admit operations',
    () async {
      var tokens = 0;
      final client = direct(
        FakeTransportBuilder().build(),
        token: () {
          tokens++;
          return null;
        },
      );
      addTearDown(client.close);
      final snapshot = await client.getVertex(
        'k',
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      expect(snapshot.confirmed.state, ConfirmedState.unknown);
      expect(snapshot.confirmed.cause, ObservationCause.noCache);
      expect(await client.getVertices([]), isEmpty);
      expect((await client.deleteVertices([])).items, isEmpty);
      expect(tokens, 0);
    },
  );
  test(
    'reads preserve duplicate Missing, present and Expired without payload',
    () async {
      final now = DateTime.utc(2026);
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (r, _) => graph.GetVerticesResponse(
                vertices: [graph.Vertex(key: 'a', string: 'known')],
                missing: ['m', 'm'],
              ),
            )
            .unary<graph.GetEdgesRequest, graph.GetEdgesResponse>(
              LanternService.getEdges,
              (r, _) => graph.GetEdgesResponse(
                edges: [
                  graph.Edge(
                    tail: 'a',
                    head: 'b',
                    weight: 1,
                    expiration: Timestamp(
                      seconds: Int64(
                        DateTime.utc(2025).millisecondsSinceEpoch ~/ 1000,
                      ),
                    ),
                  ),
                ],
              ),
            )
            .build(),
        clock: () => now,
      );
      addTearDown(client.close);
      expect(
        (await client.getVertices([
          'm',
          'a',
          'm',
        ])).map((s) => s.confirmed.state),
        [
          ConfirmedState.missing,
          ConfirmedState.present,
          ConfirmedState.missing,
        ],
      );
      final expired = await client.getEdge(const EdgeRef('a', 'b'));
      expect(expired.confirmed.state, ConfirmedState.expired);
      expect(expired.view, isNull);
    },
  );
  for (final code in [
    connect.Code.unavailable,
    connect.Code.unauthenticated,
    connect.Code.permissionDenied,
  ]) {
    test(
      '$code reads are sanitized typed Unknown and retain no hidden fallback',
      () async {
        var available = true;
        final client = direct(
          FakeTransportBuilder()
              .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
                LanternService.getVertices,
                (r, _) {
                  if (available) {
                    return graph.GetVerticesResponse(
                      vertices: [graph.Vertex(key: 'a', string: 'known')],
                    );
                  }
                  throw connect.ConnectException(code, 'sensitive diagnostics');
                },
              )
              .build(),
        );
        addTearDown(client.close);
        expect(
          (await client.getVertex('a')).confirmed.state,
          ConfirmedState.present,
        );
        available = false;
        final unavailable = await client.getVertex(
          'a',
          options: const GetOptions(source: ReadSource.serverOnly),
        );
        expect(unavailable.confirmed.state, ConfirmedState.unknown);
        expect(unavailable.view, isNull);
        expect(unavailable.confirmed.cause, isNotNull);
      },
    );
  }
  test(
    'admission is separate from Server completion; canceled waiter never replays',
    () async {
      final response = Completer<graph.AddEdgesResponse>();
      var calls = 0, tokens = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
              LanternService.addEdges,
              (r, _) {
                calls++;
                expect(r.contribIds.single, hasLength(24));
                return response.future;
              },
            )
            .build(),
        token: () {
          tokens++;
          return null;
        },
      );
      addTearDown(client.close);
      final ticket = await client.mutations.submit(
        MutationRequest.addEdges([EdgeInput(tail: 'a', head: 'b', weight: 1)]),
      );
      expect(ticket.durability, AdmissionDurability.memory);
      expect(tokens, 0); // Admission returns before scheduled dispatch.
      final cancellation = LanternCancellationToken();
      final wait = client.mutations.reobserve(
        ticket.reference,
        options: WaitOptions(cancellation: cancellation),
      );
      await settle();
      cancellation.cancel();
      await expectLater(
        wait,
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.waitCanceled,
          ),
        ),
      );
      response.complete(
        graph.AddEdgesResponse(written: 1, effectiveWeights: [double.infinity]),
      );
      final observed = await client.mutations.reobserve(ticket.reference);
      expect(
        (observed.reply!.items.single as MutationKnownEffect<double>).effect,
        double.infinity,
      );
      expect(calls, 1);
    },
  );
  test('ordinary Future remains pending until exact Server handling', () async {
    final response = Completer<graph.DeleteVerticesResponse>();
    final client = direct(
      FakeTransportBuilder()
          .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
            LanternService.deleteVertices,
            (r, _) => response.future,
          )
          .build(),
    );
    addTearDown(client.close);
    var completed = false;
    final future = client.deleteVertex('a').then((value) {
      completed = true;
      return value;
    });
    await settle();
    expect(completed, isFalse);
    response.complete(graph.DeleteVerticesResponse(existed: [false]));
    expect((await future as MutationKnownEffect<bool>).effect, isFalse);
  });
  test(
    'structured identity collision/age and high-water reject without sending',
    () async {
      var now = DateTime.utc(2026), calls = 0;
      final id = OperationId(
        issuedAt: now,
        nonce: Uint8List.fromList(List.filled(24, 1)),
      );
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (r, _) {
                calls++;
                return graph.DeleteVerticesResponse(existed: [false]);
              },
            )
            .build(),
        clock: () => now,
      );
      addTearDown(client.close);
      await client.deleteVertex('a', options: MutationOptions(operationId: id));
      await expectLater(
        client.deleteVertex('a', options: MutationOptions(operationId: id)),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.operationIdCollision,
          ),
        ),
      );
      now = now.add(const Duration(minutes: 6));
      expect(
        (await client.mutations.getStatus(id)).retention,
        MutationRetention.evicted,
      );
      now = now.subtract(const Duration(minutes: 6));
      await expectLater(
        client.deleteVertex('a', options: MutationOptions(operationId: id)),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.operationIdExpired,
          ),
        ),
      );
      expect(calls, 1);
      expect(
        (await client.mutations.reobserve(
          MutationReference<bool>(id),
        )).retention,
        MutationRetention.evicted,
      );
    },
  );
  test(
    'retained guard capacity rejects admission, preserves existing status',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (r, _) {
                calls++;
                return graph.DeleteVerticesResponse(existed: [false]);
              },
            )
            .build(),
        maxOperations: 1,
      );
      addTearDown(client.close);
      final ticket = await client.mutations.submit(
        MutationRequest.deleteVertices(['a']),
      );
      await client.mutations.reobserve(ticket.reference);
      await expectLater(
        client.deleteVertex('b'),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.capacity,
          ),
        ),
      );
      expect(
        (await client.mutations.getStatus(
          ticket.reference.operationId,
        )).terminal,
        isTrue,
      );
      expect(calls, 1);
    },
  );
  test(
    'failed explicit dependency never dispatches; implicit FIFO cycle rejects atomically',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (r, _) {
                calls++;
                return graph.DeleteVerticesResponse(existed: [false]);
              },
            )
            .build(),
      );
      addTearDown(client.close);
      final tickets = await client.mutations.submitGroup(
        MutationGroup([
          MutationGroupNode(MutationRequest.deleteVertices(['a'])),
          MutationGroupNode(
            MutationRequest.deleteVertices(['b']),
            dependencies: [
              const MutationDependency(
                node: 0,
                item: 0,
                predicate: MutationDependencyPredicate.deleteExisted,
              ),
            ],
          ),
        ]),
      );
      await expectLater(
        client.mutations.reobserve(tickets[1].reference),
        throwsA(isA<MutationBatchException<Object?>>()),
      );
      expect(calls, 1);
      await expectLater(
        client.mutations.submitGroup(
          MutationGroup([
            MutationGroupNode(
              MutationRequest.deleteVertices(['same']),
              dependencies: [const MutationDependency(node: 1, item: 0)],
            ),
            MutationGroupNode(MutationRequest.deleteVertices(['same'])),
          ]),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      expect(calls, 1);
    },
  );
  test(
    'read lane capacity rejects atomically without starting another token',
    () async {
      var tokens = 0;
      final reply = Completer<graph.GetVerticesResponse>();
      final low = LanternClient.connect(
        _endpoint,
        allowInsecure: true,
        tokenProvider: () {
          tokens++;
          return null;
        },
        transport: FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (r, _) => reply.future,
            )
            .build(),
      );
      final client = DirectLanternDataClient(
        low,
        binding: binding(),
        maxConcurrentReads: 1,
        maxQueuedReads: 0,
      );
      addTearDown(client.close);
      final first = client.getVertex('a');
      await settle();
      await expectLater(
        client.getVertex('b'),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.capacity,
          ),
        ),
      );
      expect(tokens, 1);
      reply.complete(graph.GetVerticesResponse(missing: ['a']));
      expect((await first).confirmed.state, ConfirmedState.missing);
    },
  );
  test(
    'authority-denied watch stays registered without polling around denial',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (r, _) {
                calls++;
                throw connect.ConnectException(
                  connect.Code.permissionDenied,
                  'denied',
                );
              },
            )
            .build(),
      );
      addTearDown(client.close);
      final events = <LanternSnapshot<Vertex>>[];
      final subscription = client
          .watchVertex(
            'a',
            options: const WatchOptions(
              pollInterval: Duration(milliseconds: 2),
            ),
          )
          .listen(events.add);
      await Future<void>.delayed(const Duration(milliseconds: 55));
      expect(events.last.confirmed.cause, ObservationCause.permissionDenied);
      expect(calls, 1);
      await subscription.cancel();
    },
  );
  test(
    'typed group success preserves result types for later typed reobserve',
    () async {
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (r, _) => graph.PutVerticesResponse(
                outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
              ),
            )
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (r, _) =>
                  graph.DeleteVerticesResponse(deleted: 1, existed: [true]),
            )
            .build(),
      );
      addTearDown(client.close);
      final tickets = await client.mutations.submitGroup(
        MutationGroup([
          MutationGroupNode(MutationRequest.putVertices([vertex('a')])),
          MutationGroupNode(
            MutationRequest.deleteVertices(['a']),
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
      final result = await client.mutations.reobserve(
        MutationReference<bool>(tickets[1].reference.operationId),
      );
      expect(
        (result.reply!.items.single as MutationKnownEffect<bool>).effect,
        isTrue,
      );
    },
  );
  test(
    'UID assignment does not change account identity and endpoint mismatch rejects',
    () {
      expect(binding(uid: 'assigned').stableIdentity, binding().stableIdentity);
      expect(
        binding(account: 'other').stableIdentity,
        isNot(binding().stableIdentity),
      );
      final client = LanternClient.connect(
        Uri.parse('http://other:6380'),
        allowInsecure: true,
        transport: FakeTransportBuilder().build(),
      );
      addTearDown(client.close);
      expect(
        () => DirectLanternDataClient(client, binding: binding()),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );
  test(
    'shared watch owns one fresh read, last cancellation releases timer',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (r, _) {
                calls++;
                return graph.GetVerticesResponse(
                  vertices: [graph.Vertex(key: 'a', string: 'same')],
                );
              },
            )
            .build(),
      );
      addTearDown(client.close);
      final first = <LanternSnapshot<Vertex>>[],
          second = <LanternSnapshot<Vertex>>[];
      final watch = client.watchVertex(
        'a',
        options: const WatchOptions(pollInterval: Duration(milliseconds: 50)),
      );
      final a = watch.listen(first.add), b = watch.listen(second.add);
      await settle();
      expect(calls, 1);
      expect(first.first.confirmed.state, ConfirmedState.unknown);
      expect(second.last.confirmed.state, ConfirmedState.present);
      await a.cancel();
      await Future<void>.delayed(const Duration(milliseconds: 60));
      expect(calls, greaterThan(1));
      await b.cancel();
      final atStop = calls;
      await Future<void>.delayed(const Duration(milliseconds: 75));
      expect(calls, atStop);
    },
  );
  test(
    'paused watch overflow emits observationLost and fresh bounded revalidation',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (r, _) {
                calls++;
                return graph.GetVerticesResponse(
                  vertices: [graph.Vertex(key: 'a', string: 'same')],
                );
              },
            )
            .build(),
      );
      addTearDown(client.close);
      final events = <LanternSnapshot<Vertex>>[];
      final subscription = client
          .watchVertex(
            'a',
            options: const WatchOptions(
              pollInterval: Duration(milliseconds: 2),
            ),
          )
          .listen(events.add);
      await settle();
      subscription.pause();
      final delivered = events.length;
      await Future<void>.delayed(const Duration(milliseconds: 55));
      expect(events, hasLength(delivered));
      subscription.resume();
      await settle();
      expect(events.skip(delivered).any((e) => e.observationLost), isTrue);
      expect(events.last.confirmed.state, ConfirmedState.present);
      expect(calls, greaterThan(8));
      await subscription.cancel();
    },
  );
  test(
    'strictLive fails before traversal RPC; known empty graph remains present',
    () async {
      var calls = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.IlluminateRequest, graph.IlluminateResponse>(
              LanternService.illuminate,
              (r, _) {
                calls++;
                return graph.IlluminateResponse(graph: graph.Graph());
              },
            )
            .build(),
      );
      addTearDown(client.close);
      expect(
        () => client.watchIlluminate(
          'a',
          traversal: const PprOptions(),
          options: const WatchOptions(coverage: WatchCoverage.strictLive),
        ),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.unsupported,
          ),
        ),
      );
      final result = await client.illuminate(
        'a',
        traversal: const PprOptions(),
      );
      expect(result.confirmed.state, ConfirmedState.present);
      expect(result.confirmed.value!.vertices, isEmpty);
      expect(calls, 1);
    },
  );
  test(
    'typed traversal covering matrix encodes exactly the existing low-level family',
    () async {
      final requests = <String>[];
      final transport = FakeTransportBuilder()
          .unary<graph.IlluminateRequest, graph.IlluminateResponse>(
            LanternService.illuminate,
            (r, _) {
              requests.add(r.writeToJson());
              return graph.IlluminateResponse(graph: graph.Graph());
            },
          )
          .build();
      final low = LanternClient.connect(
        _endpoint,
        allowInsecure: true,
        transport: transport,
      );
      final client = DirectLanternDataClient(low, binding: binding());
      addTearDown(client.close);
      final families = <TraversalOptions>[
        for (final objective in [
          TraversalObjective.minimize,
          TraversalObjective.maximize,
        ])
          for (final reduction in TraversalReduction.values)
            BfsOptions(
              step: 2,
              fanOut: 3,
              objective: objective,
              reduction: reduction,
            ),
        const PprOptions(),
        const PprOptions(topN: 8, restartProbability: 0.25, epsilon: 1e-4),
        for (final reduction in TraversalReduction.values)
          LocalCommunityOptions(maxSize: 20, reduction: reduction),
      ];
      for (final family in families) {
        for (final weighting in TraversalWeighting.values) {
          await low.illuminate(
            'a',
            traversal: family,
            weighting: weighting,
            vertexPrefix: 'p:',
          );
          await client.illuminate(
            'a',
            traversal: family,
            weighting: weighting,
            vertexPrefix: 'p:',
          );
          expect(requests[requests.length - 1], requests[requests.length - 2]);
        }
      }
      await expectLater(
        client.illuminate(
          'a',
          traversal: const BfsOptions(step: 0, fanOut: 1),
          options: const TraversalReadOptions(source: ReadSource.cacheOnly),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );
  test(
    'watch initial read raced by Put never republishes the older value',
    () async {
      final firstReply = Completer<graph.GetVerticesResponse>();
      var reads = 0;
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (r, _) {
                reads++;
                if (reads == 1) return firstReply.future;
                return graph.GetVerticesResponse(
                  vertices: [graph.Vertex(key: 'a', string: 'new')],
                );
              },
            )
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (r, _) => graph.PutVerticesResponse(
                outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
              ),
            )
            .build(),
      );
      addTearDown(client.close);
      final events = <LanternSnapshot<Vertex>>[];
      final sub = client.watchVertex('a').listen(events.add);
      await settle();
      await client.putVertex(vertex('a'));
      firstReply.complete(
        graph.GetVerticesResponse(
          vertices: [graph.Vertex(key: 'a', string: 'old')],
        ),
      );
      await settle();
      expect(
        events
            .where((e) => e.view?.value is StringValue)
            .map((e) => (e.view!.value as StringValue).value),
        ['new'],
      );
      expect(events.any((e) => e.pending.count == 1), isTrue);
      await sub.cancel();
    },
  );
  test(
    'a watch listener can start a mutation without reentrant stream errors',
    () async {
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
              LanternService.getVertices,
              (r, _) => graph.GetVerticesResponse(
                vertices: [graph.Vertex(key: 'a', string: 'same')],
              ),
            )
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (r, _) => graph.PutVerticesResponse(
                outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
              ),
            )
            .build(),
      );
      addTearDown(client.close);
      final events = <LanternSnapshot<Vertex>>[], errors = <Object>[];
      final completion = Completer<void>();
      var started = false;
      final sub = client.watchVertex('a').listen((event) {
        events.add(event);
        if (!started && event.confirmed.state == ConfirmedState.present) {
          started = true;
          unawaited(
            client.putVertex(vertex('a')).then((_) => completion.complete()),
          );
        }
      }, onError: errors.add);
      await completion.future;
      await settle();
      expect(errors, isEmpty);
      expect(events.any((e) => e.pending.count == 1), isTrue);
      await sub.cancel();
    },
  );
  test(
    'expired verified session hides reads and rejects dispatch before token',
    () async {
      var tokens = 0;
      final now = DateTime.utc(2026);
      final low = LanternClient.connect(
        _endpoint,
        allowInsecure: true,
        clock: () => now,
        tokenProvider: () {
          tokens++;
          return null;
        },
        transport: FakeTransportBuilder().build(),
      );
      final client = DirectLanternDataClient(
        low,
        binding: LanternSessionBinding.application(
          serviceOrigin: _endpoint,
          securityDomain: 'test',
          accountIdentity: 'account',
          authorityBinding: 'generation',
          expiresAt: now,
        ),
      );
      addTearDown(client.close);
      expect(
        (await client.getVertex('a')).confirmed.cause,
        ObservationCause.unauthenticated,
      );
      await expectLater(
        client.deleteVertex('a'),
        throwsA(isA<LanternUnauthenticatedException>()),
      );
      expect(tokens, 0);
    },
  );
  test(
    'admission cancellation is atomic and close fences read/watch publication',
    () async {
      var tokens = 0;
      final client = direct(
        FakeTransportBuilder().build(),
        token: () {
          tokens++;
          return null;
        },
      );
      final admission = LanternCancellationToken()..cancel();
      await expectLater(
        client.deleteVertex(
          'a',
          options: MutationOptions(admissionCancellation: admission),
        ),
        throwsA(isA<LanternCanceledException>()),
      );
      final errors = <Object>[];
      final sub = client
          .watchVertex(
            'a',
            options: const WatchOptions(source: ReadSource.cacheOnly),
          )
          .listen((_) {}, onError: errors.add);
      await settle();
      await client.close();
      await settle();
      expect(errors, [isA<LanternDataException>()]);
      await expectLater(
        client.getVertex('a'),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.closed,
          ),
        ),
      );
      expect(tokens, 0);
      await sub.cancel();
    },
  );
  test('one read timeout covers the whole chunked logical call', () async {
    var calls = 0;
    final client = direct(
      FakeTransportBuilder()
          .unary<graph.GetVerticesRequest, graph.GetVerticesResponse>(
            LanternService.getVertices,
            (r, _) async {
              calls++;
              await Future<void>.delayed(const Duration(milliseconds: 20));
              return graph.GetVerticesResponse(missing: r.keys);
            },
          )
          .build(),
      batchSize: 1,
    );
    addTearDown(client.close);
    final result = await client.getVertices([
      'a',
      'b',
      'c',
    ], options: const GetOptions(timeout: Duration(milliseconds: 35)));
    expect(result.first.confirmed.state, ConfirmedState.missing);
    expect(result.last.confirmed.state, ConfirmedState.unknown);
    expect(calls, 2);
  });
  test(
    'advanced submit optionally reserves one bounded completion observer',
    () async {
      final response = Completer<graph.DeleteVerticesResponse>();
      final client = direct(
        FakeTransportBuilder()
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (r, _) => response.future,
            )
            .build(),
        maxWaiters: 1,
      );
      addTearDown(client.close);
      final cancel = LanternCancellationToken();
      final ticket = await client.mutations.submit(
        MutationRequest.deleteVertices(['a']),
        options: MutationOptions(wait: WaitOptions(cancellation: cancel)),
      );
      expect(ticket.completion, isNotNull);
      await expectLater(
        client.deleteVertex('b'),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.capacity,
          ),
        ),
      );
      cancel.cancel();
      await expectLater(
        ticket.completion!,
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.waitCanceled,
          ),
        ),
      );
      response.complete(graph.DeleteVerticesResponse(existed: [false]));
      expect(
        (await client.mutations.reobserve(
          ticket.reference,
        )).reply!.items.single,
        isA<MutationKnownEffect<bool>>(),
      );
    },
  );
  test(
    'traversal response and active identity caps fail without truncation',
    () async {
      final low = LanternClient.connect(
        _endpoint,
        allowInsecure: true,
        transport: FakeTransportBuilder()
            .unary<graph.IlluminateRequest, graph.IlluminateResponse>(
              LanternService.illuminate,
              (r, _) => graph.IlluminateResponse(
                graph: graph.Graph(
                  vertices: [
                    graph.Vertex(key: 'a'),
                    graph.Vertex(key: 'b'),
                  ],
                ),
              ),
            )
            .build(),
      );
      final client = DirectLanternDataClient(
        low,
        binding: binding(),
        maxTraversalVertices: 1,
        maxTraversalQueries: 1,
      );
      addTearDown(client.close);
      await expectLater(
        client.illuminate('a', traversal: const PprOptions()),
        throwsA(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.capacity,
          ),
        ),
      );
      final sub = client
          .watchIlluminate(
            'a',
            traversal: const PprOptions(),
            options: const WatchOptions(source: ReadSource.cacheOnly),
          )
          .listen((_) {});
      await settle();
      await expectLater(
        client.watchIlluminate(
          'b',
          traversal: const PprOptions(),
          options: const WatchOptions(source: ReadSource.cacheOnly),
        ),
        emitsError(
          isA<LanternDataException>().having(
            (e) => e.reason,
            'reason',
            DataFailure.capacity,
          ),
        ),
      );
      await sub.cancel();
    },
  );
}
