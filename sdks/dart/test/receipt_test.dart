import 'dart:async';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test('receipt identity values validate and defensively copy bytes', () {
    final epochBytes = _bytes(16, 1);
    final epoch = ReceiptEpoch(epochBytes);
    epochBytes[0] = 9;
    expect(epoch.bytes, everyElement(1));
    final exposedEpoch = epoch.bytes..[0] = 8;
    expect(exposedEpoch.first, 8);
    expect(epoch.bytes.first, 1);
    expect(ReceiptEpoch(_bytes(16, 1)), epoch);

    final operationBytes = _operationIdBytes(
      epoch: 1,
      random: 2,
      issuedAtMilliseconds: 1234,
    );
    final operationId = ReceiptOperationId(operationBytes);
    operationBytes[0] = 2;
    expect(operationId.bytes.first, 1);
    expect(
      operationId.issuedAt,
      DateTime.fromMillisecondsSinceEpoch(1234, isUtc: true),
    );
    expect(operationId.epoch, epoch);

    final groupBytes = _bytes(16, 3);
    final groupId = ReceiptGroupId(groupBytes);
    groupBytes[0] = 4;
    expect(groupId.bytes, everyElement(3));

    final node = _bytes(16, 5);
    final generation = _bytes(16, 6);
    final endpoint = ReceiptEndpoint(nodeId: node, generation: generation);
    node[0] = 7;
    generation[0] = 8;
    expect(endpoint.nodeId, everyElement(5));
    expect(endpoint.generation, everyElement(6));
    expect(
      ReceiptEndpoint(nodeId: _bytes(16, 5), generation: _bytes(16, 6)),
      endpoint,
    );

    expect(
      () => ReceiptEpoch(Uint8List(15)),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptEpoch(Uint8List(16)),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptGroupId(Uint8List(16)),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptEndpoint(nodeId: Uint8List(16), generation: _bytes(16, 1)),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptEndpoint(nodeId: _bytes(16, 1), generation: Uint8List(15)),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptOperationId(Uint8List(48)),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptOperationId(
        _operationIdBytes(epoch: 1, random: 2, issuedAtMilliseconds: 1)
          ..[0] = 2,
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptOperationId(
        _operationIdBytes(epoch: 0, random: 2, issuedAtMilliseconds: 1),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptOperationId(
        _operationIdBytes(epoch: 1, random: 0, issuedAtMilliseconds: 1),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(
      () => ReceiptOperationId(
        _operationIdBytes(epoch: 1, random: 2, issuedAtMilliseconds: 1)
          ..[17] = 0x80,
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
  });

  test(
    'capability decoding and deterministic minting preserve exact data',
    () async {
      final times = <DateTime>[
        DateTime.parse('2026-07-12T00:00:10Z'),
        DateTime.parse('2026-07-12T00:00:15Z'),
      ];
      var randomCall = 0;
      final transport = FakeTransportBuilder()
          .unary<
            graph.GetReceiptCapabilityRequest,
            graph.GetReceiptCapabilityResponse
          >(
            LanternService.getReceiptCapability,
            (request, context) => _capabilityResponse(
              serverNowMilliseconds: DateTime.parse(
                '2026-07-12T01:00:00Z',
              ).millisecondsSinceEpoch,
            ),
          )
          .build();
      final client = _client(
        transport,
        clock: () => times.removeAt(0),
        receiptRandomSource: (length) {
          randomCall++;
          return _bytes(length, randomCall);
        },
      );

      final capability =
          await client.getReceiptCapability() as ReceiptCapabilityEnabled;
      expect(capability.enabled, isTrue);
      expect(capability.policy.deploymentEpoch, ReceiptEpoch(_bytes(16, 1)));
      expect(capability.policy.retention, const Duration(hours: 24));
      expect(capability.policy.maxEntries, BigInt.from(1000));
      expect(capability.policy.maxBytes, BigInt.from(1000000));
      expect(capability.endpoint.nodeId, everyElement(2));
      expect(capability.endpoint.generation, everyElement(3));
      expect(capability.observedAt, DateTime.parse('2026-07-12T00:00:10Z'));
      expect(capability.supportedMutations, {
        ReceiptMutationKind.vertexPut,
        ReceiptMutationKind.vertexDelete,
        ReceiptMutationKind.edgeDelete,
        ReceiptMutationKind.edgeAdd,
      });
      expect(
        () => capability.supportedMutations.remove(
          ReceiptMutationKind.edgeDelete,
        ),
        throwsUnsupportedError,
      );

      final fingerprint = capability.policy.fingerprint..[0] = 99;
      expect(fingerprint.first, 99);
      expect(capability.policy.fingerprint.first, 4);

      final receiptContext = client.mintReceiptContext(
        capability: capability,
        mutation: ReceiptMutationKind.edgeDelete,
        itemCount: 2,
      );
      expect(receiptContext.groupId.bytes, everyElement(1));
      expect(receiptContext.operationIds, hasLength(2));
      expect(receiptContext.operationIds[0].bytes.sublist(25), everyElement(2));
      expect(receiptContext.operationIds[1].bytes.sublist(25), everyElement(3));
      expect(
        receiptContext.operationIds.map((value) => value.issuedAt).toSet(),
        {DateTime.parse('2026-07-12T01:00:05Z')},
      );
      expect(receiptContext.epoch, capability.policy.deploymentEpoch);
      expect(receiptContext.endpoint, capability.endpoint);
      expect(
        () =>
            receiptContext.operationIds.add(receiptContext.operationIds.first),
        throwsUnsupportedError,
      );
      final exposed = receiptContext.operationIds.first.bytes..[1] = 55;
      expect(exposed[1], 55);
      expect(receiptContext.operationIds.first.bytes[1], 1);
    },
  );

  test(
    'invalid minting and mixed contexts fail before mutation transport',
    () async {
      var deleteCalls = 0;
      var putCalls = 0;
      var vertexDeleteCalls = 0;
      final transport = FakeTransportBuilder()
          .unary<
            graph.GetReceiptCapabilityRequest,
            graph.GetReceiptCapabilityResponse
          >(
            LanternService.getReceiptCapability,
            (request, context) => _capabilityResponse(),
          )
          .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
            LanternService.deleteEdges,
            (request, context) {
              deleteCalls++;
              return graph.DeleteEdgesResponse();
            },
          )
          .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
            LanternService.putVertices,
            (request, context) {
              putCalls++;
              return graph.PutVerticesResponse();
            },
          )
          .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
            LanternService.deleteVertices,
            (request, context) {
              vertexDeleteCalls++;
              return graph.DeleteVerticesResponse();
            },
          )
          .build();
      final zeroEntropy = _client(
        transport,
        receiptRandomSource: Uint8List.new,
      );
      final capability =
          await zeroEntropy.getReceiptCapability() as ReceiptCapabilityEnabled;
      expect(
        () => zeroEntropy.mintReceiptContext(
          capability: capability,
          mutation: ReceiptMutationKind.edgeDelete,
          itemCount: 1,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );

      final shortEntropy = _client(
        transport,
        receiptRandomSource: (length) => Uint8List(length - 1),
      );
      expect(
        () => shortEntropy.mintReceiptContext(
          capability: capability,
          mutation: ReceiptMutationKind.edgeDelete,
          itemCount: 1,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      var entropyCalls = 0;
      final duplicateEntropy = _client(
        transport,
        receiptRandomSource: (length) {
          entropyCalls++;
          return _bytes(length, entropyCalls == 1 ? 1 : 2);
        },
      );
      expect(
        () => duplicateEntropy.mintReceiptContext(
          capability: capability,
          mutation: ReceiptMutationKind.edgeDelete,
          itemCount: 2,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      expect(
        () => ReceiptContext(
          operationIds: [
            _operationId(epoch: 1, random: 1),
            _operationId(epoch: 2, random: 2),
          ],
          groupId: _group(),
          endpoint: _endpoint(),
          mutation: ReceiptMutationKind.edgeDelete,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      final duplicate = _operationId(epoch: 1, random: 1);
      expect(
        () => ReceiptContext(
          operationIds: [duplicate, duplicate],
          groupId: _group(),
          endpoint: _endpoint(),
          mutation: ReceiptMutationKind.edgeDelete,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      final sourceIds = <ReceiptOperationId>[duplicate];
      final copiedContext = ReceiptContext(
        operationIds: sourceIds,
        groupId: _group(),
        endpoint: _endpoint(),
        mutation: ReceiptMutationKind.edgeDelete,
      );
      sourceIds.clear();
      expect(copiedContext.operationIds, hasLength(1));

      final validContext = _receiptContext(count: 1);
      await expectLater(
        zeroEntropy.deleteEdgesWithReceipt(const [
          EdgeRef('a', 'b'),
          EdgeRef('b', 'c'),
        ], context: validContext),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      await expectLater(
        zeroEntropy.deleteEdgeWithReceipt(
          const EdgeRef('', 'b'),
          context: validContext,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      await expectLater(
        zeroEntropy.putVertexWithReceipt(
          VertexInput(key: '', value: VertexValue.nil()),
          context: _receiptContext(
            count: 1,
            mutation: ReceiptMutationKind.vertexPut,
          ),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      await expectLater(
        zeroEntropy.deleteVertexWithReceipt(
          '',
          context: _receiptContext(
            count: 1,
            mutation: ReceiptMutationKind.vertexDelete,
          ),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      expect(deleteCalls, 0);
      expect(putCalls, 0);
      expect(vertexDeleteCalls, 0);
    },
  );

  test(
    'capability distinguishes disabled, unavailable, and malformed',
    () async {
      final disabled = _client(
        FakeTransportBuilder()
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(
              LanternService.getReceiptCapability,
              (request, context) => graph.GetReceiptCapabilityResponse(),
            )
            .build(),
      );
      expect(
        await disabled.getReceiptCapability(),
        isA<ReceiptCapabilityDisabled>().having(
          (value) => value.enabled,
          'enabled',
          isFalse,
        ),
      );

      final unavailable = _client(
        FakeTransportBuilder()
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(
              LanternService.getReceiptCapability,
              (request, context) => throw connect.ConnectException(
                connect.Code.unavailable,
                'down',
              ),
            )
            .build(),
      );
      await expectLater(
        unavailable.getReceiptCapability(),
        throwsA(isA<LanternUnavailableException>()),
      );

      final malformed = _client(
        FakeTransportBuilder()
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(
              LanternService.getReceiptCapability,
              (request, context) => graph.GetReceiptCapabilityResponse(
                enabled: false,
                endpoint: graph.ReceiptEndpoint(
                  nodeId: _bytes(16, 2),
                  generation: _bytes(16, 3),
                ),
              ),
            )
            .build(),
      );
      await expectLater(
        malformed.getReceiptCapability(),
        throwsA(
          isA<LanternInternalException>().having(
            (error) => error.isSdkProtocolViolation,
            'isSdkProtocolViolation',
            isTrue,
          ),
        ),
      );

      final outOfOrder = _client(
        FakeTransportBuilder()
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(
              LanternService.getReceiptCapability,
              (request, context) => _capabilityResponse(
                supportedMutations: const [
                  graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_DELETE_VERTEX,
                  graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_PUT_VERTEX,
                ],
              ),
            )
            .build(),
      );
      await expectLater(
        outOfOrder.getReceiptCapability(),
        throwsA(
          isA<LanternInternalException>().having(
            (error) => error.isSdkProtocolViolation,
            'isSdkProtocolViolation',
            isTrue,
          ),
        ),
      );

      final serverInternal = _client(
        FakeTransportBuilder()
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(
              LanternService.getReceiptCapability,
              (request, context) => throw connect.ConnectException(
                connect.Code.internal,
                'server internal',
              ),
            )
            .build(),
      );
      await expectLater(
        serverInternal.getReceiptCapability(),
        throwsA(
          isA<LanternInternalException>().having(
            (error) => error.isSdkProtocolViolation,
            'isSdkProtocolViolation',
            isFalse,
          ),
        ),
      );

      final limited = _client(
        FakeTransportBuilder()
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(
              LanternService.getReceiptCapability,
              (request, context) => _capabilityResponse(
                supportedMutations: const [
                  graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_DELETE_EDGE,
                ],
              ),
            )
            .build(),
      );
      final capability =
          await limited.getReceiptCapability() as ReceiptCapabilityEnabled;
      expect(capability.supports(ReceiptMutationKind.edgeDelete), isTrue);
      expect(capability.supports(ReceiptMutationKind.vertexPut), isFalse);
      expect(
        () => limited.mintReceiptContext(
          capability: capability,
          mutation: ReceiptMutationKind.vertexPut,
          itemCount: 1,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );

  test(
    'plural and singular status APIs preserve all states and false',
    () async {
      final ids = [
        _operationId(epoch: 1, random: 1),
        _operationId(epoch: 1, random: 2),
        _operationId(epoch: 1, random: 3),
      ];
      var calls = 0;
      final transport = FakeTransportBuilder()
          .unary<
            graph.GetReceiptStatusesRequest,
            graph.GetReceiptStatusesResponse
          >(LanternService.getReceiptStatuses, (request, context) {
            calls++;
            if (request.operationIds.length == 1) {
              return graph.GetReceiptStatusesResponse(
                statuses: [
                  graph.ReceiptStatus(
                    operationId: request.operationIds.single,
                    state: graph
                        .MutationReceiptState
                        .MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED,
                  ),
                ],
              );
            }
            expect(request.operationIds, [
              ids[0].bytes,
              ids[1].bytes,
              ids[2].bytes,
            ]);
            return graph.GetReceiptStatusesResponse(
              statuses: [
                _confirmedStatus(ids[0], existed: false),
                graph.ReceiptStatus(
                  operationId: ids[1].bytes,
                  state: graph
                      .MutationReceiptState
                      .MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED,
                ),
                graph.ReceiptStatus(
                  operationId: ids[2].bytes,
                  state: graph
                      .MutationReceiptState
                      .MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE,
                ),
              ],
            );
          })
          .build();
      final client = _client(transport);

      final statuses = await client.getReceiptStatuses(ids);
      expect(statuses.map((status) => status.state), [
        ReceiptStatusState.confirmed,
        ReceiptStatusState.notYetObserved,
        ReceiptStatusState.noLongerProvable,
      ]);
      final receipt = statuses.first.receipt! as EdgeDeleteReceipt;
      expect(receipt.mutation, ReceiptMutationKind.edgeDelete);
      expect(receipt.operationId, ids.first);
      expect(receipt.groupId, _group());
      expect(receipt.itemIndex, 0);
      expect(receipt.itemCount, 3);
      expect(receipt.existed, isFalse);
      expect(
        receipt.deadline,
        ids.first.issuedAt.add(const Duration(hours: 1)),
      );
      final digest = receipt.intentSha256..[0] = 9;
      expect(digest.first, 9);
      expect(receipt.intentSha256.first, 7);
      expect(statuses[1].receipt, isNull);
      expect(statuses[2].receipt, isNull);
      expect(() => statuses.add(statuses.first), throwsUnsupportedError);

      final singular = await client.getReceiptStatus(ids.first);
      expect(singular.state, ReceiptStatusState.notYetObserved);
      expect(calls, 2);
    },
  );

  test('status decoding rejects misalignment and missing results', () async {
    final id = _operationId(epoch: 1, random: 1);
    final misaligned = _client(
      FakeTransportBuilder()
          .unary<
            graph.GetReceiptStatusesRequest,
            graph.GetReceiptStatusesResponse
          >(
            LanternService.getReceiptStatuses,
            (request, context) => graph.GetReceiptStatusesResponse(
              statuses: [
                graph.ReceiptStatus(
                  operationId: _operationId(epoch: 1, random: 9).bytes,
                  state: graph
                      .MutationReceiptState
                      .MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED,
                ),
              ],
            ),
          )
          .build(),
    );
    await expectLater(
      misaligned.getReceiptStatus(id),
      throwsA(
        isA<LanternInternalException>().having(
          (error) => error.isSdkProtocolViolation,
          'isSdkProtocolViolation',
          isTrue,
        ),
      ),
    );

    final missingResult = _client(
      FakeTransportBuilder()
          .unary<
            graph.GetReceiptStatusesRequest,
            graph.GetReceiptStatusesResponse
          >(
            LanternService.getReceiptStatuses,
            (request, context) => graph.GetReceiptStatusesResponse(
              statuses: [_confirmedStatus(id, result: graph.ReceiptResult())],
            ),
          )
          .build(),
    );
    await expectLater(
      missingResult.getReceiptStatus(id),
      throwsA(
        isA<LanternInternalException>().having(
          (error) => error.isSdkProtocolViolation,
          'isSdkProtocolViolation',
          isTrue,
        ),
      ),
    );
  });

  test('status decodes all typed receipt results', () async {
    final putId = _operationId(epoch: 1, random: 1);
    final deleteId = _operationId(epoch: 1, random: 2);
    final addId = _operationId(epoch: 1, random: 3);
    final client = _client(
      FakeTransportBuilder()
          .unary<
            graph.GetReceiptStatusesRequest,
            graph.GetReceiptStatusesResponse
          >(
            LanternService.getReceiptStatuses,
            (request, context) => graph.GetReceiptStatusesResponse(
              statuses: [
                _confirmedStatus(
                  putId,
                  result: graph.ReceiptResult(
                    putVertexOutcome:
                        graph.PutOutcome.PUT_OUTCOME_CONDITION_NOT_MET,
                  ),
                ),
                _confirmedStatus(
                  deleteId,
                  result: graph.ReceiptResult(deleteVertexExisted: false),
                ),
                _confirmedStatus(
                  addId,
                  result: graph.ReceiptResult(addEdgeEffectiveWeight: 5),
                ),
              ],
            ),
          )
          .build(),
    );

    final statuses = await client.getReceiptStatuses([
      putId,
      deleteId,
      addId,
    ]);
    final put = statuses[0].receipt! as VertexPutReceipt;
    expect(put.mutation, ReceiptMutationKind.vertexPut);
    expect(put.outcome, PutOutcome.conditionNotMet);
    final delete = statuses[1].receipt! as VertexDeleteReceipt;
    expect(delete.mutation, ReceiptMutationKind.vertexDelete);
    expect(delete.existed, isFalse);
    final add = statuses[2].receipt! as EdgeAddReceipt;
    expect(add.mutation, ReceiptMutationKind.edgeAdd);
    expect(add.effectiveWeight, 5);
  });

  test(
    'Edge Add status preserves signed zero and non-finite results',
    () async {
      final id = _operationId(epoch: 1, random: 1);
      LanternClient clientFor(
        double effectiveWeight, {
        bool binaryRoundTrip = true,
      }) => _client(
        FakeTransportBuilder()
            .unary<
              graph.GetReceiptStatusesRequest,
              graph.GetReceiptStatusesResponse
            >(
              LanternService.getReceiptStatuses,
              (request, context) => graph.GetReceiptStatusesResponse(
                statuses: [
                  _confirmedStatus(
                    id,
                    result: graph.ReceiptResult(
                      addEdgeEffectiveWeight: effectiveWeight,
                    ),
                    binaryRoundTrip: binaryRoundTrip,
                  ),
                ],
              ),
            )
            .build(),
      );

      for (final binaryRoundTrip in [true, false]) {
        final signedZero = await clientFor(
          -0.0,
          binaryRoundTrip: binaryRoundTrip,
        ).getReceiptStatus(id);
        final signedZeroReceipt = signedZero.receipt! as EdgeAddReceipt;
        expect(signedZeroReceipt.effectiveWeight, 0);
        expect(signedZeroReceipt.effectiveWeight.isNegative, isTrue);

        for (final infinity in [
          double.infinity,
          double.negativeInfinity,
        ]) {
          final status = await clientFor(
            infinity,
            binaryRoundTrip: binaryRoundTrip,
          ).getReceiptStatus(id);
          expect(
            (status.receipt! as EdgeAddReceipt).effectiveWeight,
            infinity,
          );
        }
        final nanStatus = await clientFor(
          double.nan,
          binaryRoundTrip: binaryRoundTrip,
        ).getReceiptStatus(id);
        expect(
          (nanStatus.receipt! as EdgeAddReceipt).effectiveWeight.isNaN,
          isTrue,
        );
      }
      for (final overflow in [double.maxFinite, -double.maxFinite]) {
        await expectLater(
          clientFor(
            overflow,
            binaryRoundTrip: false,
          ).getReceiptStatus(id),
          throwsA(
            isA<LanternInternalException>().having(
              (error) => error.isSdkProtocolViolation,
              'isSdkProtocolViolation',
              isTrue,
            ),
          ),
        );
      }
    },
  );

  test('receipt-bearing Edge Add preserves non-finite results', () async {
    final context = _receiptContext(
      count: 3,
      mutation: ReceiptMutationKind.edgeAdd,
    );
    final client = _client(
      FakeTransportBuilder()
          .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
            LanternService.addEdges,
            (request, callContext) => graph.AddEdgesResponse(
              written: 3,
              effectiveWeights: [
                double.infinity,
                double.negativeInfinity,
                double.nan,
              ],
            ),
          )
          .build(),
    );

    final results = await client.addEdgesWithReceipt(
      [
        EdgeInput(
          tail: 'positive',
          head: 'infinity',
          weight: 1,
          contribId: _bytes(24, 1),
        ),
        EdgeInput(
          tail: 'negative',
          head: 'infinity',
          weight: -1,
          contribId: _bytes(24, 2),
        ),
        EdgeInput(
          tail: 'semantic',
          head: 'nan',
          weight: 1,
          contribId: _bytes(24, 3),
        ),
      ],
      context: context,
    );
    expect(results[0].effectiveWeight, double.infinity);
    expect(results[1].effectiveWeight, double.negativeInfinity);
    expect(results[2].effectiveWeight.isNaN, isTrue);
  });

  test('receipt-bearing Vertex Put is plural canonical and exact', () async {
    final pluralContext = _receiptContext(
      count: 2,
      mutation: ReceiptMutationKind.vertexPut,
    );
    final singularContext = _receiptContext(
      count: 1,
      randomStart: 9,
      mutation: ReceiptMutationKind.vertexPut,
    );
    final requests = <graph.PutVerticesRequest>[];
    final transport = FakeTransportBuilder()
        .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
          LanternService.putVertices,
          (request, context) {
            requests.add(request.deepCopy());
            if (request.vertices.length == 1) {
              return graph.PutVerticesResponse(
                outcomes: [graph.PutOutcome.PUT_OUTCOME_EXPIRED],
              );
            }
            return graph.PutVerticesResponse(
              outcomes: [
                graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE,
                graph.PutOutcome.PUT_OUTCOME_CONDITION_NOT_MET,
              ],
            );
          },
        )
        .build();
    final client = _client(transport);

    final results = await client.putVerticesWithReceipt(
      [
        VertexInput(key: 'a', value: VertexValue.string('one')),
        VertexInput(key: 'b', value: VertexValue.string('two')),
      ],
      context: pluralContext,
      ifAbsent: true,
    );
    expect(results.map((result) => result.key), ['a', 'b']);
    expect(
      results.map((result) => result.operationId),
      pluralContext.operationIds,
    );
    expect(results.map((result) => result.outcome), [
      PutOutcome.appliedAndLive,
      PutOutcome.conditionNotMet,
    ]);
    expect(() => results.add(results.first), throwsUnsupportedError);
    expect(requests.first.ifAbsent, isTrue);
    expect(
      requests.first.receiptContext.operationIds,
      pluralContext.operationIds.map((value) => value.bytes),
    );

    final singular = await client.putVertexWithReceipt(
      VertexInput(key: 'c', value: VertexValue.nil()),
      context: singularContext,
    );
    expect(singular.key, 'c');
    expect(singular.operationId, singularContext.operationIds.single);
    expect(singular.outcome, PutOutcome.expired);
    expect(requests, hasLength(2));
    expect(requests.last.vertices, hasLength(1));
  });

  test('receipt-bearing Vertex Delete preserves exact false results', () async {
    final pluralContext = _receiptContext(
      count: 2,
      mutation: ReceiptMutationKind.vertexDelete,
    );
    final singularContext = _receiptContext(
      count: 1,
      randomStart: 9,
      mutation: ReceiptMutationKind.vertexDelete,
    );
    final requests = <graph.DeleteVerticesRequest>[];
    final transport = FakeTransportBuilder()
        .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
          LanternService.deleteVertices,
          (request, context) {
            requests.add(request.deepCopy());
            if (request.keys.length == 1) {
              return graph.DeleteVerticesResponse(deleted: 0, existed: [false]);
            }
            return graph.DeleteVerticesResponse(
              deleted: 1,
              existed: [true, false],
            );
          },
        )
        .build();
    final client = _client(transport);

    final results = await client.deleteVerticesWithReceipt([
      'present',
      'absent',
    ], context: pluralContext);
    expect(results.map((result) => result.key), ['present', 'absent']);
    expect(
      results.map((result) => result.operationId),
      pluralContext.operationIds,
    );
    expect(results.map((result) => result.existed), [true, false]);
    expect(() => results.add(results.first), throwsUnsupportedError);

    final singular = await client.deleteVertexWithReceipt(
      'still-absent',
      context: singularContext,
    );
    expect(singular.key, 'still-absent');
    expect(singular.operationId, singularContext.operationIds.single);
    expect(singular.existed, isFalse);
    expect(requests, hasLength(2));
    expect(requests.last.keys, ['still-absent']);
  });

  test(
    'Vertex Put response loss proves capability and reuses exact request',
    () async {
      final receiptContext = _receiptContext(
        count: 1,
        mutation: ReceiptMutationKind.vertexPut,
      );
      final requests = <graph.PutVerticesRequest>[];
      var putCalls = 0;
      var capabilityCalls = 0;
      var tokenCalls = 0;
      final transport = FakeTransportBuilder()
          .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
            LanternService.putVertices,
            (request, context) {
              putCalls++;
              requests.add(request.deepCopy());
              if (putCalls == 1) {
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'response lost',
                );
              }
              return graph.PutVerticesResponse(
                outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
              );
            },
          )
          .unary<
            graph.GetReceiptCapabilityRequest,
            graph.GetReceiptCapabilityResponse
          >(LanternService.getReceiptCapability, (request, context) {
            capabilityCalls++;
            return _capabilityResponse();
          })
          .build();
      final client = _client(
        transport,
        retryPolicy: _fastRetry,
        tokenProvider: () => 'token-${++tokenCalls}',
      );

      final result = await client.putVertexWithReceipt(
        VertexInput(
          key: 'retry',
          value: VertexValue.string('stable'),
          expiresIn: const Duration(minutes: 5),
        ),
        context: receiptContext,
      );
      expect(result.outcome, PutOutcome.appliedAndLive);
      expect(putCalls, 2);
      expect(capabilityCalls, 1);
      expect(tokenCalls, 3);
      expect(requests[0].writeToBuffer(), requests[1].writeToBuffer());
    },
  );

  test(
    'family mismatch and removed capability fail before unsafe replay',
    () async {
      var putCalls = 0;
      var deleteCalls = 0;
      var capabilityCalls = 0;
      final transport = FakeTransportBuilder()
          .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
            LanternService.putVertices,
            (request, context) {
              putCalls++;
              throw connect.ConnectException(
                connect.Code.unavailable,
                'response lost',
              );
            },
          )
          .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
            LanternService.deleteVertices,
            (request, context) {
              deleteCalls++;
              return graph.DeleteVerticesResponse();
            },
          )
          .unary<
            graph.GetReceiptCapabilityRequest,
            graph.GetReceiptCapabilityResponse
          >(LanternService.getReceiptCapability, (request, context) {
            capabilityCalls++;
            return _capabilityResponse(
              supportedMutations: const [
                graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_DELETE_EDGE,
              ],
            );
          })
          .build();
      final client = _client(transport, retryPolicy: _fastRetry);
      final putContext = _receiptContext(
        count: 1,
        mutation: ReceiptMutationKind.vertexPut,
      );

      await expectLater(
        client.deleteVertexWithReceipt('key', context: putContext),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      expect(deleteCalls, 0);

      await expectLater(
        client.putVertexWithReceipt(
          VertexInput(key: 'key', value: VertexValue.nil()),
          context: putContext,
        ),
        throwsA(
          isA<ReceiptReconciliationException>()
              .having(
                (error) => error.reason,
                'reason',
                ReceiptReconciliationReason.mutationUnavailable,
              )
              .having((error) => error.context, 'context', same(putContext)),
        ),
      );
      expect(putCalls, 1);
      expect(capabilityCalls, 1);
    },
  );

  test('receipt-bearing Edge Delete is plural canonical and exact', () async {
    final pluralContext = _receiptContext(count: 2);
    final singularContext = _receiptContext(count: 1, randomStart: 9);
    final requests = <graph.DeleteEdgesRequest>[];
    final transport = FakeTransportBuilder()
        .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
          LanternService.deleteEdges,
          (request, context) {
            requests.add(request.deepCopy());
            if (request.edges.length == 1) {
              return graph.DeleteEdgesResponse(deleted: 0, existed: [false]);
            }
            return graph.DeleteEdgesResponse(
              deleted: 1,
              existed: [true, false],
            );
          },
        )
        .build();
    final client = _client(transport);

    final results = await client.deleteEdgesWithReceipt(const [
      EdgeRef('a', 'b'),
      EdgeRef('b', 'c'),
    ], context: pluralContext);
    expect(results.map((result) => result.edge), [
      const EdgeRef('a', 'b'),
      const EdgeRef('b', 'c'),
    ]);
    expect(
      results.map((result) => result.operationId),
      pluralContext.operationIds,
    );
    expect(results.map((result) => result.existed), [true, false]);
    expect(() => results.add(results.first), throwsUnsupportedError);
    expect(
      requests.first.receiptContext.operationIds,
      pluralContext.operationIds.map((value) => value.bytes),
    );
    expect(
      requests.first.receiptContext.logicalCallId,
      pluralContext.groupId.bytes,
    );
    expect(
      requests.first.receiptContext.endpoint.nodeId,
      pluralContext.endpoint.nodeId,
    );
    expect(
      requests.first.receiptContext.endpoint.generation,
      pluralContext.endpoint.generation,
    );

    final singular = await client.deleteEdgeWithReceipt(
      const EdgeRef('x', 'y'),
      context: singularContext,
    );
    expect(singular.edge, const EdgeRef('x', 'y'));
    expect(singular.operationId, singularContext.operationIds.single);
    expect(singular.existed, isFalse);
    expect(requests, hasLength(2));
    expect(requests.last.edges, hasLength(1));
  });

  test('receipt-bearing Edge Add is plural canonical and exact', () async {
    final pluralContext = _receiptContext(
      count: 2,
      mutation: ReceiptMutationKind.edgeAdd,
    );
    final singularContext = _receiptContext(
      count: 1,
      randomStart: 9,
      mutation: ReceiptMutationKind.edgeAdd,
    );
    final requests = <graph.AddEdgesRequest>[];
    final transport = FakeTransportBuilder()
        .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
          LanternService.addEdges,
          (request, context) {
            requests.add(request.deepCopy());
            if (request.edges.length == 1) {
              return graph.AddEdgesResponse(
                written: 1,
                effectiveWeights: [-0.0],
              );
            }
            return graph.AddEdgesResponse(
              written: 2,
              effectiveWeights: [2, 5],
            );
          },
        )
        .build();
    final client = _client(transport);

    final results = await client.addEdgesWithReceipt(
      [
        EdgeInput(
          tail: 'a',
          head: 'b',
          weight: 2,
          contribId: _bytes(24, 5),
        ),
        EdgeInput(
          tail: 'a',
          head: 'b',
          weight: 3,
          contribId: _bytes(24, 6),
        ),
      ],
      context: pluralContext,
    );
    expect(results.map((result) => result.edge), [
      const EdgeRef('a', 'b'),
      const EdgeRef('a', 'b'),
    ]);
    expect(
      results.map((result) => result.operationId),
      pluralContext.operationIds,
    );
    expect(results.map((result) => result.effectiveWeight), [2, 5]);
    expect(() => results.add(results.first), throwsUnsupportedError);
    expect(requests.first.contribIds, [
      _bytes(24, 5),
      _bytes(24, 6),
    ]);
    expect(
      requests.first.receiptContext.operationIds,
      pluralContext.operationIds.map((value) => value.bytes),
    );

    final singular = await client.addEdgeWithReceipt(
      EdgeInput(
        tail: 'x',
        head: 'y',
        weight: -0.0,
        contribId: _bytes(24, 7),
      ),
      context: singularContext,
    );
    expect(singular.edge, const EdgeRef('x', 'y'));
    expect(singular.operationId, singularContext.operationIds.single);
    expect(singular.effectiveWeight, 0);
    expect(singular.effectiveWeight.isNegative, isTrue);
    expect(requests, hasLength(2));
    expect(requests.last.contribIds.single, _bytes(24, 7));
    expect(requests.last.edges.single.weight, 0);
    expect(requests.last.edges.single.weight.isNegative, isTrue);
  });

  test('receipt-bearing Edge Add rejects contribution IDs before RPC', () async {
    var calls = 0;
    final client = _client(
      FakeTransportBuilder()
          .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
            LanternService.addEdges,
            (request, context) {
              calls++;
              return graph.AddEdgesResponse();
            },
          )
          .build(),
    );
    final context = _receiptContext(
      count: 1,
      mutation: ReceiptMutationKind.edgeAdd,
    );

    for (final contributionId in <Uint8List?>[
      null,
      Uint8List(24),
      _bytes(23, 1),
      _bytes(25, 1),
    ]) {
      await expectLater(
        client.addEdgeWithReceipt(
          EdgeInput(
            tail: 'a',
            head: 'b',
            weight: 1,
            contribId: contributionId,
          ),
          context: context,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    }
    for (final weight in [
      double.nan,
      double.infinity,
      double.negativeInfinity,
      double.maxFinite,
      -double.maxFinite,
    ]) {
      expect(
        () => EdgeInput(
          tail: 'a',
          head: 'b',
          weight: weight,
          contribId: _bytes(24, 1),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    }
    await expectLater(
      client.addEdgesWithReceipt(
        [
          EdgeInput(
            tail: 'a',
            head: 'b',
            weight: 1,
            contribId: _bytes(24, 1),
          ),
          EdgeInput(
            tail: 'b',
            head: 'c',
            weight: 2,
            contribId: _bytes(24, 1),
          ),
        ],
        context: _receiptContext(
          count: 2,
          mutation: ReceiptMutationKind.edgeAdd,
        ),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    await expectLater(
      client.addEdgeWithReceipt(
        EdgeInput(
          tail: 'a',
          head: 'b',
          weight: 1,
          contribId: _bytes(24, 2),
        ),
        context: _receiptContext(count: 1),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(calls, 0);
  });

  test(
    'Edge Add response loss proves continuity and reuses exact request',
    () async {
      final receiptContext = _receiptContext(
        count: 1,
        mutation: ReceiptMutationKind.edgeAdd,
      );
      final requests = <graph.AddEdgesRequest>[];
      var addCalls = 0;
      var capabilityCalls = 0;
      var tokenCalls = 0;
      final transport = FakeTransportBuilder()
          .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
            LanternService.addEdges,
            (request, context) {
              addCalls++;
              requests.add(request.deepCopy());
              if (addCalls == 1) {
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'response lost',
                );
              }
              return graph.AddEdgesResponse(
                written: 1,
                effectiveWeights: [3],
              );
            },
          )
          .unary<
            graph.GetReceiptCapabilityRequest,
            graph.GetReceiptCapabilityResponse
          >(LanternService.getReceiptCapability, (request, context) {
            capabilityCalls++;
            return _capabilityResponse();
          })
          .build();
      final client = _client(
        transport,
        retryPolicy: _fastRetry,
        tokenProvider: () => 'token-${++tokenCalls}',
      );

      final result = await client.addEdgeWithReceipt(
        EdgeInput(
          tail: 'a',
          head: 'b',
          weight: 3,
          contribId: _bytes(24, 8),
        ),
        context: receiptContext,
      );
      expect(result.effectiveWeight, 3);
      expect(addCalls, 2);
      expect(capabilityCalls, 1);
      expect(tokenCalls, 3);
      expect(requests[0].writeToBuffer(), requests[1].writeToBuffer());
    },
  );

  test(
    'response loss retries only after continuity and reuses context',
    () async {
      final receiptContext = _receiptContext(count: 1);
      final requests = <graph.DeleteEdgesRequest>[];
      var deleteCalls = 0;
      var capabilityCalls = 0;
      var tokenCalls = 0;
      final transport = FakeTransportBuilder()
          .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
            LanternService.deleteEdges,
            (request, context) {
              deleteCalls++;
              requests.add(request.deepCopy());
              if (deleteCalls == 1) {
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'response lost',
                );
              }
              return graph.DeleteEdgesResponse(deleted: 1, existed: [true]);
            },
          )
          .unary<
            graph.GetReceiptCapabilityRequest,
            graph.GetReceiptCapabilityResponse
          >(LanternService.getReceiptCapability, (request, context) {
            capabilityCalls++;
            return _capabilityResponse();
          })
          .build();
      final client = _client(
        transport,
        retryPolicy: _fastRetry,
        tokenProvider: () => 'token-${++tokenCalls}',
      );

      final result = await client.deleteEdgeWithReceipt(
        const EdgeRef('a', 'b'),
        context: receiptContext,
      );
      expect(result.existed, isTrue);
      expect(deleteCalls, 2);
      expect(capabilityCalls, 1);
      expect(tokenCalls, 3);
      expect(
        requests[0].receiptContext.operationIds,
        requests[1].receiptContext.operationIds,
      );
      expect(
        requests[0].receiptContext.logicalCallId,
        requests[1].receiptContext.logicalCallId,
      );
      expect(
        requests[0].receiptContext.endpoint,
        requests[1].receiptContext.endpoint,
      );
    },
  );

  test(
    'later terminal failure retains prior response-loss ambiguity',
    () async {
      final receiptContext = _receiptContext(count: 1);
      var deleteCalls = 0;
      var tokenCalls = 0;
      final transport = FakeTransportBuilder()
          .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
            LanternService.deleteEdges,
            (request, context) {
              deleteCalls++;
              throw connect.ConnectException(
                connect.Code.unavailable,
                'response lost',
              );
            },
          )
          .unary<
            graph.GetReceiptCapabilityRequest,
            graph.GetReceiptCapabilityResponse
          >(
            LanternService.getReceiptCapability,
            (request, context) => _capabilityResponse(),
          )
          .build();
      final client = _client(
        transport,
        retryPolicy: _fastRetry,
        tokenProvider: () {
          tokenCalls++;
          if (tokenCalls == 3) {
            throw StateError('rotated token unavailable');
          }
          return 'token-$tokenCalls';
        },
      );

      await expectLater(
        client.deleteEdgeWithReceipt(
          const EdgeRef('a', 'b'),
          context: receiptContext,
        ),
        throwsA(
          isA<ReceiptReconciliationException>()
              .having(
                (error) => error.reason,
                'reason',
                ReceiptReconciliationReason.outcomeUnknown,
              )
              .having((error) => error.context, 'context', same(receiptContext))
              .having(
                (error) => error.cause,
                'cause',
                isA<LanternInternalException>(),
              ),
        ),
      );
      expect(deleteCalls, 1);
      expect(tokenCalls, 3);
    },
  );

  test(
    'changed continuity and disabled capability stop destructive replay',
    () async {
      Future<void> verify({
        required graph.GetReceiptCapabilityResponse capability,
        required ReceiptReconciliationReason reason,
      }) async {
        var deleteCalls = 0;
        var capabilityCalls = 0;
        final receiptContext = _receiptContext(count: 1);
        final transport = FakeTransportBuilder()
            .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
              LanternService.deleteEdges,
              (request, context) {
                deleteCalls++;
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'response lost',
                );
              },
            )
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(LanternService.getReceiptCapability, (request, context) {
              capabilityCalls++;
              return capability;
            })
            .build();
        final client = _client(transport, retryPolicy: _fastRetry);

        await expectLater(
          client.deleteEdgeWithReceipt(
            const EdgeRef('a', 'b'),
            context: receiptContext,
          ),
          throwsA(
            isA<ReceiptReconciliationException>()
                .having((error) => error.reason, 'reason', reason)
                .having(
                  (error) => error.context,
                  'context',
                  same(receiptContext),
                ),
          ),
        );
        expect(deleteCalls, 1);
        expect(capabilityCalls, 1);
      }

      await verify(
        capability: _capabilityResponse(epoch: 9),
        reason: ReceiptReconciliationReason.deploymentEpochChanged,
      );
      await verify(
        capability: _capabilityResponse(node: 9),
        reason: ReceiptReconciliationReason.nodeChanged,
      );
      await verify(
        capability: _capabilityResponse(generation: 9),
        reason: ReceiptReconciliationReason.generationChanged,
      );
      await verify(
        capability: graph.GetReceiptCapabilityResponse(),
        reason: ReceiptReconciliationReason.capabilityDisabled,
      );
    },
  );

  test('intent conflicts stay invalid and never retry', () async {
    var deleteCalls = 0;
    var capabilityCalls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
          LanternService.deleteEdges,
          (request, context) {
            deleteCalls++;
            throw connect.ConnectException(
              connect.Code.invalidArgument,
              'operation ID intent conflict',
            );
          },
        )
        .unary<
          graph.GetReceiptCapabilityRequest,
          graph.GetReceiptCapabilityResponse
        >(LanternService.getReceiptCapability, (request, context) {
          capabilityCalls++;
          return _capabilityResponse();
        })
        .build();
    final client = _client(transport, retryPolicy: _fastRetry);

    await expectLater(
      client.deleteEdgeWithReceipt(
        const EdgeRef('a', 'b'),
        context: _receiptContext(count: 1),
      ),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    expect(deleteCalls, 1);
    expect(capabilityCalls, 0);
  });

  test(
    'retry suppression and absent policy preserve uncertain context',
    () async {
      Future<void> verify({
        required RetryPolicy? retryPolicy,
        LanternCallOptions? options,
      }) async {
        var deleteCalls = 0;
        var capabilityCalls = 0;
        final receiptContext = _receiptContext(count: 1);
        final transport = FakeTransportBuilder()
            .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
              LanternService.deleteEdges,
              (request, context) {
                deleteCalls++;
                throw connect.ConnectException(
                  connect.Code.unavailable,
                  'response lost',
                );
              },
            )
            .unary<
              graph.GetReceiptCapabilityRequest,
              graph.GetReceiptCapabilityResponse
            >(LanternService.getReceiptCapability, (request, context) {
              capabilityCalls++;
              return _capabilityResponse();
            })
            .build();
        final client = _client(transport, retryPolicy: retryPolicy);

        await expectLater(
          client.deleteEdgeWithReceipt(
            const EdgeRef('a', 'b'),
            context: receiptContext,
            options: options,
          ),
          throwsA(
            isA<ReceiptReconciliationException>()
                .having(
                  (error) => error.reason,
                  'reason',
                  ReceiptReconciliationReason.outcomeUnknown,
                )
                .having(
                  (error) => error.context,
                  'context',
                  same(receiptContext),
                ),
          ),
        );
        expect(deleteCalls, 1);
        expect(capabilityCalls, 0);
      }

      await verify(retryPolicy: null);
      await verify(
        retryPolicy: _fastRetry,
        options: LanternCallOptions(retry: false),
      );
    },
  );

  test('preflight cancellation and deadline avoid transport', () async {
    var deleteCalls = 0;
    final transport = FakeTransportBuilder()
        .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
          LanternService.deleteEdges,
          (request, context) {
            deleteCalls++;
            return graph.DeleteEdgesResponse();
          },
        )
        .build();
    final client = _client(transport);
    final cancellation = LanternCancellationToken()..cancel();
    final receiptContext = _receiptContext(count: 1);

    await expectLater(
      client.deleteEdgeWithReceipt(
        const EdgeRef('a', 'b'),
        context: receiptContext,
        options: LanternCallOptions(cancellation: cancellation),
      ),
      throwsA(isA<LanternCanceledException>()),
    );
    await expectLater(
      client.deleteEdgeWithReceipt(
        const EdgeRef('a', 'b'),
        context: receiptContext,
        options: LanternCallOptions(
          deadline: DateTime.now().subtract(const Duration(seconds: 1)),
        ),
      ),
      throwsA(isA<LanternDeadlineExceededException>()),
    );
    expect(deleteCalls, 0);
  });

  test(
    'in-flight cancellation and deadline retain uncertain context',
    () async {
      Future<void> verify({
        required LanternCallOptions options,
        void Function()? afterStart,
      }) async {
        final started = Completer<void>();
        final receiptContext = _receiptContext(count: 1);
        final transport = FakeTransportBuilder()
            .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
              LanternService.deleteEdges,
              (request, context) async {
                if (!started.isCompleted) started.complete();
                final error = await context.signal.future;
                throw error;
              },
            )
            .build();
        final client = _client(transport, defaultTimeout: null);
        final call = client.deleteEdgeWithReceipt(
          const EdgeRef('a', 'b'),
          context: receiptContext,
          options: options,
        );
        await started.future;
        afterStart?.call();
        await expectLater(
          call,
          throwsA(
            isA<ReceiptReconciliationException>()
                .having(
                  (error) => error.reason,
                  'reason',
                  ReceiptReconciliationReason.outcomeUnknown,
                )
                .having(
                  (error) => error.context,
                  'context',
                  same(receiptContext),
                ),
          ),
        );
      }

      final cancellation = LanternCancellationToken();
      await verify(
        options: LanternCallOptions(cancellation: cancellation),
        afterStart: cancellation.cancel,
      );
      await verify(
        options: LanternCallOptions(timeout: const Duration(milliseconds: 5)),
      );
    },
  );

  test(
    'malformed successful delete response requires reconciliation',
    () async {
      final receiptContext = _receiptContext(count: 2);
      final client = _client(
        FakeTransportBuilder()
            .unary<graph.DeleteEdgesRequest, graph.DeleteEdgesResponse>(
              LanternService.deleteEdges,
              (request, context) =>
                  graph.DeleteEdgesResponse(deleted: 1, existed: [true]),
            )
            .build(),
      );

      await expectLater(
        client.deleteEdgesWithReceipt(const [
          EdgeRef('a', 'b'),
          EdgeRef('b', 'c'),
        ], context: receiptContext),
        throwsA(
          isA<ReceiptReconciliationException>()
              .having(
                (error) => error.reason,
                'reason',
                ReceiptReconciliationReason.outcomeUnknown,
              )
              .having(
                (error) => error.context,
                'context',
                same(receiptContext),
              ),
        ),
      );
    },
  );

  test(
    'malformed successful Vertex responses require reconciliation',
    () async {
      final putContext = _receiptContext(
        count: 2,
        mutation: ReceiptMutationKind.vertexPut,
      );
      final deleteContext = _receiptContext(
        count: 2,
        randomStart: 9,
        mutation: ReceiptMutationKind.vertexDelete,
      );
      final client = _client(
        FakeTransportBuilder()
            .unary<graph.PutVerticesRequest, graph.PutVerticesResponse>(
              LanternService.putVertices,
              (request, context) => graph.PutVerticesResponse(
                outcomes: [graph.PutOutcome.PUT_OUTCOME_APPLIED_AND_LIVE],
              ),
            )
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (request, context) => graph.DeleteVerticesResponse(
                deleted: 2,
                existed: [true, false],
              ),
            )
            .build(),
      );

      await expectLater(
        client.putVerticesWithReceipt([
          VertexInput(key: 'a', value: VertexValue.nil()),
          VertexInput(key: 'b', value: VertexValue.nil()),
        ], context: putContext),
        throwsA(
          isA<ReceiptReconciliationException>().having(
            (error) => error.context,
            'context',
            same(putContext),
          ),
        ),
      );
      await expectLater(
        client.deleteVerticesWithReceipt(['a', 'b'], context: deleteContext),
        throwsA(
          isA<ReceiptReconciliationException>().having(
            (error) => error.context,
            'context',
            same(deleteContext),
          ),
        ),
      );
    },
  );

  test(
    'malformed successful Edge Add response requires reconciliation',
    () async {
      final addContext = _receiptContext(
        count: 1,
        mutation: ReceiptMutationKind.edgeAdd,
      );
      final malformedResponses = [
        graph.AddEdgesResponse(
          written: 1,
          effectiveWeights: [double.maxFinite],
        ),
        graph.AddEdgesResponse(
          written: 1,
          effectiveWeights: [-double.maxFinite],
        ),
        graph.AddEdgesResponse(
          written: 0,
          effectiveWeights: [1],
        ),
      ];
      for (final response in malformedResponses) {
        final client = _client(
          FakeTransportBuilder()
              .unary<graph.AddEdgesRequest, graph.AddEdgesResponse>(
                LanternService.addEdges,
                (request, context) => response,
              )
              .build(),
        );

        await expectLater(
          client.addEdgeWithReceipt(
            EdgeInput(
              tail: 'a',
              head: 'b',
              weight: 1,
              contribId: _bytes(24, 9),
            ),
            context: addContext,
          ),
          throwsA(
            isA<ReceiptReconciliationException>().having(
              (error) => error.context,
              'context',
              same(addContext),
            ),
          ),
        );
      }
    },
  );
}

const RetryPolicy _fastRetry = RetryPolicy(
  maxAttempts: 2,
  baseDelay: Duration(microseconds: 1),
  maxDelay: Duration(microseconds: 1),
);

LanternClient _client(
  connect.Transport transport, {
  RetryPolicy? retryPolicy,
  TokenProvider? tokenProvider,
  LanternClock? clock,
  ReceiptRandomSource? receiptRandomSource,
  Duration? defaultTimeout = const Duration(seconds: 10),
}) => LanternClient.connect(
  Uri.parse('https://example.test'),
  transport: transport,
  retryPolicy: retryPolicy,
  tokenProvider: tokenProvider,
  clock: clock,
  receiptRandomSource: receiptRandomSource,
  defaultTimeout: defaultTimeout,
);

graph.GetReceiptCapabilityResponse _capabilityResponse({
  int epoch = 1,
  int node = 2,
  int generation = 3,
  int serverNowMilliseconds = 1000,
  List<graph.ReceiptMutationKind>? supportedMutations,
}) => graph.GetReceiptCapabilityResponse(
  enabled: true,
  policy: graph.ReceiptPolicy(
    deploymentEpoch: _bytes(16, epoch),
    fingerprint: _bytes(32, 4),
    retentionMs: Int64(const Duration(hours: 24).inMilliseconds),
    maxEntries: Int64(1000),
    maxBytes: Int64(1000000),
  ),
  endpoint: graph.ReceiptEndpoint(
    nodeId: _bytes(16, node),
    generation: _bytes(16, generation),
  ),
  serverNowUnixMs: Int64(serverNowMilliseconds),
  supportedMutations:
      supportedMutations ??
      const [
        graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_PUT_VERTEX,
        graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_DELETE_VERTEX,
        graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_DELETE_EDGE,
        graph.ReceiptMutationKind.RECEIPT_MUTATION_KIND_ADD_EDGE,
      ],
);

ReceiptContext _receiptContext({
  required int count,
  int randomStart = 1,
  ReceiptMutationKind mutation = ReceiptMutationKind.edgeDelete,
}) => ReceiptContext(
  operationIds: List<ReceiptOperationId>.generate(
    count,
    (index) => _operationId(epoch: 1, random: randomStart + index),
  ),
  groupId: _group(),
  endpoint: _endpoint(),
  mutation: mutation,
);

ReceiptEndpoint _endpoint() =>
    ReceiptEndpoint(nodeId: _bytes(16, 2), generation: _bytes(16, 3));

ReceiptGroupId _group() => ReceiptGroupId(_bytes(16, 4));

ReceiptOperationId _operationId({
  required int epoch,
  required int random,
  int issuedAtMilliseconds = 1000,
}) => ReceiptOperationId(
  _operationIdBytes(
    epoch: epoch,
    random: random,
    issuedAtMilliseconds: issuedAtMilliseconds,
  ),
);

Uint8List _operationIdBytes({
  required int epoch,
  required int random,
  required int issuedAtMilliseconds,
}) {
  final bytes = Uint8List(49);
  bytes[0] = 1;
  bytes.fillRange(1, 17, epoch);
  var timestamp = issuedAtMilliseconds;
  for (var index = 24; index >= 17; index--) {
    bytes[index] = timestamp & 0xff;
    timestamp >>= 8;
  }
  bytes.fillRange(25, 49, random);
  return bytes;
}

Uint8List _bytes(int length, int value) =>
    Uint8List.fromList(List<int>.filled(length, value));

graph.ReceiptStatus _confirmedStatus(
  ReceiptOperationId operationId, {
  bool existed = true,
  graph.ReceiptResult? result,
  bool binaryRoundTrip = true,
}) {
  final status = graph.ReceiptStatus(
    operationId: operationId.bytes,
    state: graph.MutationReceiptState.MUTATION_RECEIPT_STATE_CONFIRMED,
    receipt: graph.MutationReceipt(
      operationId: operationId.bytes,
      logicalCallId: _group().bytes,
      itemIndex: 0,
      itemCount: 3,
      intentSha256: _bytes(32, 7),
      deadlineUnixMs: Int64(
        operationId.issuedAt
            .add(const Duration(hours: 1))
            .millisecondsSinceEpoch,
      ),
      originalResult: result ?? graph.ReceiptResult(deleteEdgeExisted: existed),
    ),
  );
  return binaryRoundTrip
      ? graph.ReceiptStatus.fromBuffer(status.writeToBuffer())
      : status;
}
