import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  group('effect-undisclosed acknowledgement', () {
    test('typed replies keep genuine failures and partial batches', () async {
      expect(
        await captureMutationReply(() async => 17),
        isA<MutationKnownEffect<int>>().having((r) => r.effect, 'effect', 17),
      );
      expect(
        await captureMutationReply<int>(() async {
          throw const MutationAcceptance();
        }),
        isA<MutationAcceptedUndisclosed<int>>(),
      );
      await expectLater(
        captureMutationReply<int>(() async {
          throw BatchException(committed: 2, cause: const MutationAcceptance());
        }),
        throwsA(isA<BatchException>()),
      );
      await expectLater(
        captureMutationReply<int>(() async {
          throw StateError('failure');
        }),
        throwsStateError,
      );
    });

    for (final family in _Family.values) {
      for (final failLast in [false, true]) {
        test(
          '${family.name}: new chunks once, later failure=$failLast',
          () async {
            final observed = <String>[];
            var calls = 0;
            bool accepted(List<String> tails) {
              calls++;
              observed.addAll(tails);
              if (failLast && calls == 3) {
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'lost',
                );
              }
              return calls == 2;
            }

            final transport = FakeTransportBuilder()
                .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
                  LanternService.addEdges,
                  (request, context) =>
                      accepted(request.edges.map((e) => e.tail).toList())
                      ? graph.AddEdgesResponse(acceptance: _acceptance())
                      : graph.AddEdgesResponse(
                          written: request.edges.length,
                          effectiveWeights: List.filled(
                            request.edges.length,
                            2,
                          ),
                        ),
                )
                .unary<graph.CreateEdgesRequest, graph.CreateEdgesResponse>(
                  LanternService.createEdges,
                  (request, context) =>
                      accepted(request.edges.map((e) => e.tail).toList())
                      ? graph.CreateEdgesResponse(acceptance: _acceptance())
                      : graph.CreateEdgesResponse(
                          outcomes: List.filled(
                            request.edges.length,
                            graph
                                .CreateEdgeOutcome
                                .CREATE_EDGE_OUTCOME_EDGE_EXISTS,
                          ),
                        ),
                )
                .unary<graph.PutEdgesRequest, graph.PutEdgesResponse>(
                  LanternService.putEdges,
                  (request, context) =>
                      accepted(request.edges.map((e) => e.tail).toList())
                      ? graph.PutEdgesResponse(acceptance: _acceptance())
                      : graph.PutEdgesResponse(
                          outcomes: List.filled(
                            request.edges.length,
                            graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE,
                          ),
                        ),
                )
                .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
                  LanternService.deleteEdges,
                  (request, context) =>
                      accepted(request.edges.map((e) => e.tail).toList())
                      ? graph.DeleteEdgesResponse(acceptance: _acceptance())
                      : graph.DeleteEdgesResponse(
                          deleted: 0,
                          existed: List.filled(request.edges.length, false),
                        ),
                )
                .unary<
                  graph.DeleteEdgeContributionsRequest,
                  graph.DeleteEdgeContributionsResponse
                >(
                  LanternService.deleteEdgeContributions,
                  (request, context) =>
                      accepted(
                        request.contributions.map((e) => e.tail).toList(),
                      )
                      ? graph.DeleteEdgeContributionsResponse(
                          acceptance: _acceptance(),
                        )
                      : graph.DeleteEdgeContributionsResponse(
                          existed: List.filled(
                            request.contributions.length,
                            false,
                          ),
                        ),
                )
                .build();
            final client = _client(transport);
            Future<Object?> call() => _write(client, family, 5, batchSize: 2);
            if (failLast) {
              await expectLater(
                captureMutationReply(call),
                throwsA(
                  isA<BatchException>()
                      .having((e) => e.committed, 'handled input prefix', 4)
                      .having(
                        (e) => e.cause,
                        'real failure',
                        isA<LanternUnavailableException>(),
                      ),
                ),
              );
            } else {
              expect(
                await captureMutationReply(call),
                isA<MutationAcceptedUndisclosed<Object?>>(),
              );
            }
            expect(calls, 3);
            expect(observed, List.generate(5, (i) => 'tail:$i'));
          },
        );
      }
    }

    test('unknown or mixed acceptance fails closed', () async {
      for (final response in [
        graph.AddEdgesResponse(acceptance: graph.MutationAcceptance()),
        graph.AddEdgesResponse(
          acceptance: _acceptance(),
          effectiveWeights: [0],
        ),
        graph.AddEdgesResponse(
          acceptance: graph.MutationAcceptance.fromBuffer([
            ..._acceptance().writeToBuffer(),
            0xf8,
            0x07,
            1,
          ]),
        ),
        graph.AddEdgesResponse.fromBuffer([
          ...graph.AddEdgesResponse(acceptance: _acceptance()).writeToBuffer(),
          0xf8,
          0x07,
          1,
        ]),
      ]) {
        final client = _client(
          FakeTransportBuilder()
              .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
                LanternService.addEdges,
                (request, context) => response,
              )
              .build(),
        );
        await expectLater(
          client.addEdge(EdgeInput(tail: 'tail', head: 'head', weight: 1)),
          throwsA(isA<LanternInternalException>()),
        );
      }
    });

    test('prefix deletion never fabricates a count', () async {
      final client = _client(
        FakeTransportBuilder()
            .unary<
              graph.DeleteEdgesByPrefixRequest,
              graph.DeleteEdgesByPrefixResponse
            >(
              LanternService.deleteEdgesByPrefix,
              (request, context) =>
                  graph.DeleteEdgesByPrefixResponse(acceptance: _acceptance()),
            )
            .build(),
      );
      expect(
        await captureMutationReply(
          () => client.deleteEdgesByPrefix(headPrefix: 'head:'),
        ),
        isA<MutationAcceptedUndisclosed<BigInt>>(),
      );
    });

    test('receipt acknowledgements are terminal and never retried', () async {
      var calls = 0;
      final transport = FakeTransportBuilder()
          .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
            LanternService.addEdges,
            (request, context) {
              calls++;
              expect(request.hasReceiptContext(), isTrue);
              return graph.AddEdgesResponse(acceptance: _acceptance());
            },
          )
          .unary<graph.CreateEdgesRequest, graph.CreateEdgesResponse>(
            LanternService.createEdges,
            (request, context) {
              calls++;
              return graph.CreateEdgesResponse(acceptance: _acceptance());
            },
          )
          .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
            LanternService.deleteEdges,
            (request, context) {
              calls++;
              return graph.DeleteEdgesResponse(acceptance: _acceptance());
            },
          )
          .unary<
            graph.DeleteEdgeContributionsRequest,
            graph.DeleteEdgeContributionsResponse
          >(LanternService.deleteEdgeContributions, (request, context) {
            calls++;
            return graph.DeleteEdgeContributionsResponse(
              acceptance: _acceptance(),
            );
          })
          .build();
      final client = _client(transport);
      final edge = EdgeInput(
        tail: 'tail',
        head: 'head',
        weight: 1,
        contribId: Uint8List.fromList(List.filled(24, 1)),
      );
      final callsToMake = <Future<Object?> Function()>[
        () => client.addEdgesWithReceipt([
          edge,
        ], context: _context(ReceiptMutationKind.edgeAdd)),
        () => client.createEdgesWithReceipt([
          EdgeInput(tail: 'tail', head: 'head', weight: 1),
        ], context: _context(ReceiptMutationKind.edgeCreate)),
        () => client.deleteEdgesWithReceipt([
          const EdgeRef('tail', 'head'),
        ], context: _context(ReceiptMutationKind.edgeDelete)),
        () => client.deleteEdgeContributionsWithReceipt([
          EdgeContributionRef(
            tail: 'tail',
            head: 'head',
            contribId: edge.contribId!,
          ),
        ], context: _context(ReceiptMutationKind.edgeContributionDelete)),
      ];
      for (final call in callsToMake) {
        expect(
          await captureMutationReply(call),
          isA<MutationAcceptedUndisclosed<Object?>>(),
        );
      }
      expect(calls, 4);
    });

    test('undisclosed receipt status has no original result', () async {
      final id = _context(ReceiptMutationKind.edgeAdd).operationIds.single;
      for (final includeReceipt in [false, true]) {
        final client = _client(
          FakeTransportBuilder()
              .unary<
                graph.GetReceiptStatusesRequest,
                graph.GetReceiptStatusesResponse
              >(
                LanternService.getReceiptStatuses,
                (request, context) => graph.GetReceiptStatusesResponse(
                  statuses: [
                    graph.ReceiptStatus(
                      operationId: id.bytes,
                      state: graph
                          .MutationReceiptState
                          .MUTATION_RECEIPT_STATE_EFFECT_UNDISCLOSED,
                      receipt: includeReceipt ? graph.MutationReceipt() : null,
                    ),
                  ],
                ),
              )
              .build(),
        );
        if (includeReceipt) {
          await expectLater(
            client.getReceiptStatus(id),
            throwsA(isA<LanternInternalException>()),
          );
        } else {
          final status = await client.getReceiptStatus(id);
          expect(status.state, ReceiptStatusState.effectUndisclosed);
          expect(status.receipt, isNull);
        }
      }
    });
  });
}

enum _Family { add, create, put, delete, contributionDelete }

Future<Object?> _write(
  LanternClient client,
  _Family family,
  int count, {
  required int batchSize,
}) {
  final edges = List.generate(
    count,
    (i) => EdgeInput(tail: 'tail:$i', head: 'head:$i', weight: 1),
  );
  return switch (family) {
    _Family.add => client.addEdges(edges, batchSize: batchSize),
    _Family.create => client.createEdges(edges, batchSize: batchSize),
    _Family.put => client.putEdges(edges, batchSize: batchSize),
    _Family.delete => client.deleteEdges([
      for (final e in edges) EdgeRef(e.tail, e.head),
    ], batchSize: batchSize),
    _Family.contributionDelete => client.deleteEdgeContributions([
      for (final e in edges)
        EdgeContributionRef(
          tail: e.tail,
          head: e.head,
          contribId: Uint8List.fromList(List.filled(24, 1)),
        ),
    ], batchSize: batchSize),
  };
}

graph.MutationAcceptance _acceptance() => graph.MutationAcceptance(
  kind: graph
      .MutationAcceptanceKind
      .MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED,
);

LanternClient _client(connect.Transport transport) {
  final client = LanternClient.connect(
    Uri.parse('https://example.test'),
    transport: transport,
  );
  addTearDown(client.close);
  return client;
}

ReceiptContext _context(ReceiptMutationKind mutation) {
  final bytes = Uint8List(49)..[0] = 1;
  bytes.fillRange(1, 17, 1);
  bytes[23] = 3;
  bytes[24] = 232;
  bytes.fillRange(25, 49, 2);
  return ReceiptContext(
    operationIds: [ReceiptOperationId(bytes)],
    groupId: ReceiptGroupId(Uint8List.fromList(List.filled(16, 3))),
    endpoint: ReceiptEndpoint(
      nodeId: Uint8List.fromList(List.filled(16, 4)),
      generation: Uint8List.fromList(List.filled(16, 5)),
    ),
    mutation: mutation,
  );
}
