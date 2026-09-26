import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

import 'helpers.dart';

void main() {
  final time = DateTime.utc(2026, 7, 22, 7, 2, 3, 4, 5);

  group('OfflineCodec', () {
    test('round trips every public VertexValue kind canonically', () {
      final values = <VertexValue>[
        VertexValue.float64(1.5),
        VertexValue.float32(1.25),
        VertexValue.int32(-123),
        VertexValue.int64(-0x7fffffffffffffff),
        VertexValue.uint32(0xffffffff),
        VertexValue.uint64((BigInt.one << 64) - BigInt.one),
        VertexValue.boolean(true),
        VertexValue.string('日本語'),
        VertexValue.bytes(Uint8List.fromList(<int>[0, 255, 2])),
        VertexValue.timestamp(time),
        VertexValue.duration(const Duration(microseconds: -123456)),
        VertexValue.nil(),
        VertexValue.unset(),
      ];
      for (final value in values) {
        final record = OfflineCacheRecord.value(
          partitionId: 'partition',
          generation: 3,
          key: const OfflineEntityKey.vertex('key'),
          entity: Vertex(
            key: 'key',
            value: value,
            expiration: time.add(const Duration(minutes: 1)),
          ),
          validatedAt: time,
          lastAccessAt: time,
        );
        final encoded = OfflineCodec.encodeCacheRecord(record);
        final decoded = OfflineCodec.decodeCacheRecord(encoded);
        expect(OfflineCodec.encodeCacheRecord(decoded), encoded);
        expect(decoded.vertex!.value.runtimeType, value.runtimeType);
        if (value case BytesValue(:final value)) {
          expect((decoded.vertex!.value as BytesValue).value, value);
        }
      }
    });

    test('preserves legacy Add bytes for fail-closed migration', () {
      final record = OfflineOutboxRecord(
        recordId: 'r',
        operationId: 'o',
        itemIndex: 0,
        partitionId: 'p',
        intent: OfflineAddEdgeIntent(
          Edge(
            tail: 'a:b',
            head: 'c',
            weight: Float32Value(0.1).value,
            expiration: time,
          ),
          Uint8List.fromList(List<int>.generate(24, (index) => index + 1)),
        ),
        enqueuedAt: time,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
      );
      final decoded = OfflineCodec.decodeOutboxRecord(
        OfflineCodec.encodeOutboxRecord(record),
      );
      final intent = decoded.intent as OfflineAddEdgeIntent;
      expect(
        intent.contributionId,
        List<int>.generate(24, (index) => index + 1),
      );
      expect(
        intent.edge.weight,
        (record.intent as OfflineAddEdgeIntent).edge.weight,
      );
      expect(
        OfflineEntityKey.edge('a:b', 'c').canonical,
        isNot(OfflineEntityKey.edge('a', 'b:c').canonical),
      );
    });

    test('bounds durable intent expirations to the canonical time range', () {
      for (final invalid in <DateTime>[DateTime.utc(0), DateTime.utc(10000)]) {
        expect(
          () => OfflinePutVertexIntent(
            Vertex(key: 'v', value: VertexValue.nil(), expiration: invalid),
          ),
          throwsA(isA<OfflineArgumentException>()),
        );
        expect(
          () => OfflinePutEdgeIntent(
            Edge(tail: 'a', head: 'b', weight: 1, expiration: invalid),
          ),
          throwsA(isA<OfflineArgumentException>()),
        );
        expect(
          () => OfflineAddEdgeIntent(
            Edge(tail: 'a', head: 'b', weight: 1, expiration: invalid),
            Uint8List.fromList(List<int>.filled(24, 1)),
          ),
          throwsA(isA<OfflineArgumentException>()),
        );
      }

      for (final boundary in <DateTime>[
        DateTime.utc(1),
        DateTime.utc(9999, 12, 31, 23, 59, 59, 999, 999),
      ]) {
        final record = OfflineOutboxRecord(
          recordId: 'record-${boundary.year}',
          operationId: 'operation-${boundary.year}',
          itemIndex: 0,
          partitionId: 'p',
          intent: OfflinePutVertexIntent(
            Vertex(key: 'v', value: VertexValue.nil(), expiration: boundary),
          ),
          enqueuedAt: time,
          ordinal: 1,
          state: OfflineOutboxState.enqueued,
          attemptCount: 0,
          generation: 0,
        );
        final decoded = OfflineCodec.decodeOutboxRecord(
          OfflineCodec.encodeOutboxRecord(record),
        );
        expect(decoded.absoluteExpiration, boundary);
      }
    });

    test('fails closed on unknown schema, kind, and noncanonical bytes', () {
      expect(
        () => OfflineCodec.decodeCacheRecord(
          '{"schema":2,"type":"cache","key":{},"record":"missing","entity":null,"validatedAt":"0","lastAccessAt":"0","missingUntil":"0"}',
        ),
        throwsA(isA<OfflineCodecException>()),
      );
      expect(
        () => OfflineCodec.decodeCacheRecord(
          '{"schema":1,"type":"cache","key":{"kind":"vertex","key":"k","tail":null,"head":null},"record":"value","entity":{"kind":"vertex","key":"k","value":{"kind":"bytes","data":"AA"},"expiration":null,"tail":null,"head":null,"weight":null},"validatedAt":"0","lastAccessAt":"0","missingUntil":null}',
        ),
        throwsA(isA<OfflineCodecException>()),
      );
    });

    test('uses canonical unpadded base64url and strict lease state', () {
      final bytes = OfflineCacheRecord.value(
        partitionId: 'partition',
        generation: 0,
        key: const OfflineEntityKey.vertex('bytes'),
        entity: Vertex(
          key: 'bytes',
          value: VertexValue.bytes(Uint8List.fromList(<int>[251, 255])),
          expiration: null,
        ),
        validatedAt: time,
        lastAccessAt: time,
      );
      final encoded = OfflineCodec.encodeCacheRecord(bytes);
      expect(encoded, contains(r'"data":"-_8"'));
      expect(encoded, isNot(contains('=')));
      expect(
        () =>
            OfflineCodec.decodeCacheRecord(encoded.replaceFirst('-_8', '+/8=')),
        throwsA(isA<OfflineCodecException>()),
      );

      OfflineOutboxRecord leaseRecord({
        required OfflineOutboxState state,
        String? leaseOwner,
        DateTime? leaseUntil,
      }) => OfflineOutboxRecord(
        recordId: 'record',
        operationId: 'operation',
        itemIndex: 0,
        partitionId: 'partition',
        intent: OfflinePutVertexIntent(
          Vertex(
            key: 'key',
            value: VertexValue.string('value'),
            expiration: null,
          ),
        ),
        enqueuedAt: time,
        ordinal: 1,
        state: state,
        attemptCount: 0,
        generation: 0,
        leaseOwner: leaseOwner,
        leaseUntil: leaseUntil,
      );
      expect(
        () => leaseRecord(state: OfflineOutboxState.sending),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => leaseRecord(
          state: OfflineOutboxState.enqueued,
          leaseOwner: 'owner',
          leaseUntil: time.add(const Duration(seconds: 1)),
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
    });

    test('matches cache fixture and migrates legacy Add v1 canonically', () {
      for (final path in <String>[
        'test/fixtures/v1_cache_vertex.json',
        'test/fixtures/v1_outbox_add.json',
      ]) {
        final fixture = File(path).readAsStringSync().trim();
        final encoded = path.contains('cache')
            ? OfflineCodec.encodeCacheRecord(
                OfflineCodec.decodeCacheRecord(fixture),
              )
            : OfflineCodec.encodeOutboxRecord(
                OfflineCodec.decodeOutboxRecord(fixture),
              );
        if (path.contains('cache')) {
          expect(encoded, fixture);
        } else {
          expect(
            OfflineCodec.encodeOutboxRecord(
              OfflineCodec.decodeOutboxRecord(encoded),
            ),
            encoded,
          );
          expect(encoded, contains('"schema":4'));
          expect(encoded, contains('"deadLetteredAt":null'));
          expect(encoded, contains('"receipt":null'));
        }
      }
    });

    test('migrates the canonical outbox v2 transition fixture', () {
      final fixture = File(
        'test/fixtures/v2_outbox_dead_letter.json',
      ).readAsStringSync().trim();
      final decoded = OfflineCodec.decodeOutboxRecord(fixture);
      final encoded = OfflineCodec.encodeOutboxRecord(decoded);

      expect(encoded, contains('"schema":4'));
      expect(encoded, contains('"receipt":null'));
      expect(
        OfflineCodec.encodeOutboxRecord(
          OfflineCodec.decodeOutboxRecord(encoded),
        ),
        encoded,
      );
      expect(
        decoded.deadLetteredAt,
        DateTime.fromMicrosecondsSinceEpoch(1000000, isUtc: true),
      );
    });

    test('round trips content-free durable operation aggregates', () {
      final record = OfflineOperationRecord(
        partitionId: 'partition',
        generation: 2,
        operationId: 'operation',
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: 'record-0',
            operationId: 'operation',
            itemIndex: 0,
            state: OfflineWriteState.confirmed,
            attemptCount: 1,
          ),
          OfflineWriteStatus(
            recordId: 'record-1',
            operationId: 'operation',
            itemIndex: 1,
            state: OfflineWriteState.deadLetter,
            attemptCount: 3,
            diagnosticCode: 'permanent',
          ),
        ],
        updatedAt: time,
        terminalAt: time,
      );
      final encoded = OfflineCodec.encodeOperationRecord(record);
      final decoded = OfflineCodec.decodeOperationRecord(encoded);

      expect(OfflineCodec.encodeOperationRecord(decoded), encoded);
      expect(decoded.status.isTerminal, isTrue);
      expect(decoded.status.confirmedCount, 1);
      expect(
        () => OfflineCodec.decodeOperationRecord(
          encoded.replaceFirst('"type":"operation"', '"type":"unknown"'),
        ),
        throwsA(isA<OfflineCodecException>()),
      );
    });

    test('round trips strict receipt evidence and exact original results', () {
      final capability = offlineReceiptCapability();
      final evidence = OfflineReceiptEvidence(
        operationId: testReceiptOperationId(
          epoch: capability.policy.deploymentEpoch,
          random: 1,
        ),
        groupId: ReceiptGroupId(testBytes(16, 21)),
        endpoint: capability.endpoint,
        mutation: ReceiptMutationKind.vertexPut,
        policy: capability.policy,
        itemIndex: 0,
        itemCount: 1,
        state: OfflineReceiptReconciliationState.lookupUnknown,
        reconciliationAttemptCount: 2,
      );
      final record = OfflineOutboxRecord(
        recordId: 'receipt-record',
        operationId: 'receipt-operation',
        itemIndex: 0,
        partitionId: 'partition',
        intent: OfflinePutVertexIfAbsentIntent(
          Vertex(
            key: 'key',
            value: VertexValue.string('value'),
            expiration: null,
          ),
        ),
        enqueuedAt: time,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 1,
        generation: 0,
        receipt: evidence,
        nextAttemptAt: time.add(const Duration(seconds: 1)),
        diagnosticCode: 'receipt_status_unavailable',
      );
      final encodedRecord = OfflineCodec.encodeOutboxRecord(record);
      final decodedRecord = OfflineCodec.decodeOutboxRecord(encodedRecord);
      expect(OfflineCodec.encodeOutboxRecord(decodedRecord), encodedRecord);
      expect(decodedRecord.receipt!.operationId, evidence.operationId);
      expect(decodedRecord.receipt!.groupId, evidence.groupId);
      expect(decodedRecord.receipt!.endpoint, evidence.endpoint);
      expect(
        decodedRecord.receipt!.policy.fingerprint,
        evidence.policy.fingerprint,
      );
      expect(
        decodedRecord.receipt!.state,
        OfflineReceiptReconciliationState.lookupUnknown,
      );

      final operation = OfflineOperationRecord(
        partitionId: 'partition',
        generation: 0,
        operationId: 'receipt-operation',
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: 'receipt-record',
            operationId: 'receipt-operation',
            itemIndex: 0,
            state: OfflineWriteState.confirmed,
            attemptCount: 1,
            receiptResult: const OfflineVertexPutReceiptResult(
              PutOutcome.conditionNotMet,
            ),
          ),
        ],
        updatedAt: time,
        terminalAt: time,
      );
      final encodedOperation = OfflineCodec.encodeOperationRecord(operation);
      final decodedOperation = OfflineCodec.decodeOperationRecord(
        encodedOperation,
      );
      expect(
        OfflineCodec.encodeOperationRecord(decodedOperation),
        encodedOperation,
      );
      expect(
        (decodedOperation.items.single.receiptResult
                as OfflineVertexPutReceiptResult)
            .outcome,
        PutOutcome.conditionNotMet,
      );
    });

    test('v3 receipt without a dispatch marker is never treated as unsent', () {
      final capability = offlineReceiptCapability();
      final record = OfflineOutboxRecord(
        recordId: 'unmarked',
        operationId: 'unmarked-operation',
        itemIndex: 0,
        partitionId: 'p',
        intent: OfflineDeleteVertexIntent('target'),
        enqueuedAt: time,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
        receipt: OfflineReceiptEvidence(
          operationId: testReceiptOperationId(
            epoch: capability.policy.deploymentEpoch,
            random: 2,
          ),
          groupId: ReceiptGroupId(testBytes(16, 21)),
          endpoint: capability.endpoint,
          mutation: ReceiptMutationKind.vertexDelete,
          policy: capability.policy,
          itemIndex: 0,
          itemCount: 1,
          state: OfflineReceiptReconciliationState.statusRequired,
          mayHaveDispatched: false,
        ),
      );
      final encoded = OfflineCodec.encodeOutboxRecord(record);
      expect(
        OfflineCodec.decodeOutboxRecord(encoded).receipt!.mayHaveDispatched,
        isFalse,
      );
      final old = jsonDecode(encoded) as Map<String, Object?>;
      final evidence = old['receipt']! as Map<String, Object?>;
      evidence.remove('mayHaveDispatched');
      expect(
        () => OfflineCodec.decodeOutboxRecord(jsonEncode(old)),
        throwsA(isA<OfflineCodecException>()),
      );
      old['schema'] = 3;
      final migrated = OfflineCodec.decodeOutboxRecord(jsonEncode(old));
      expect(migrated.receipt!.mayHaveDispatched, isTrue);
      expect(migrated.receipt!.operationId, record.receipt!.operationId);
      expect(
        OfflineCodec.encodeOutboxRecord(migrated),
        contains('"mayHaveDispatched":true'),
      );
      old['schema'] = 4;
      evidence['mayHaveDispatched'] = 'false';
      expect(
        () => OfflineCodec.decodeOutboxRecord(jsonEncode(old)),
        throwsA(isA<OfflineCodecException>()),
      );
    });

    test('round trips receipt Add intent and exact effective weight', () {
      final capability = offlineReceiptCapability();
      final contributionId = testBytes(24, 33);
      final evidence = OfflineReceiptEvidence(
        operationId: testReceiptOperationId(
          epoch: capability.policy.deploymentEpoch,
          random: 8,
        ),
        groupId: ReceiptGroupId(testBytes(16, 22)),
        endpoint: capability.endpoint,
        mutation: ReceiptMutationKind.edgeAdd,
        policy: capability.policy,
        itemIndex: 0,
        itemCount: 1,
        state: OfflineReceiptReconciliationState.statusRequired,
      );
      final record = OfflineOutboxRecord(
        recordId: 'add-record',
        operationId: 'add-operation',
        itemIndex: 0,
        partitionId: 'partition',
        intent: OfflineReceiptAddEdgeIntent(
          Edge(
            tail: 'tail',
            head: 'head',
            weight: normalizeOfflineFloat32(0.1),
            expiration: time,
          ),
          contributionId,
        ),
        enqueuedAt: time,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
        receipt: evidence,
      );
      final encodedRecord = OfflineCodec.encodeOutboxRecord(record);
      final decodedRecord = OfflineCodec.decodeOutboxRecord(encodedRecord);
      final decodedIntent = decodedRecord.intent as OfflineReceiptAddEdgeIntent;
      expect(OfflineCodec.encodeOutboxRecord(decodedRecord), encodedRecord);
      expect(decodedIntent.contributionId, contributionId);
      expect(decodedIntent.edge.weight, normalizeOfflineFloat32(0.1));
      expect(decodedRecord.receipt!.mutation, ReceiptMutationKind.edgeAdd);

      final operation = OfflineOperationRecord(
        partitionId: 'partition',
        generation: 0,
        operationId: 'add-operation',
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: 'add-record',
            operationId: 'add-operation',
            itemIndex: 0,
            state: OfflineWriteState.confirmed,
            attemptCount: 1,
            receiptResult: OfflineEdgeAddReceiptResult(5),
          ),
        ],
        updatedAt: time,
        terminalAt: time,
      );
      final encodedOperation = OfflineCodec.encodeOperationRecord(operation);
      final decodedOperation = OfflineCodec.decodeOperationRecord(
        encodedOperation,
      );
      expect(
        OfflineCodec.encodeOperationRecord(decodedOperation),
        encodedOperation,
      );
      expect(
        (decodedOperation.items.single.receiptResult
                as OfflineEdgeAddReceiptResult)
            .effectiveWeight,
        5,
      );
    });

    test('preserves non-finite Add results but rejects non-finite intents', () {
      for (final weight in <double>[
        -0.0,
        double.infinity,
        double.negativeInfinity,
        double.nan,
      ]) {
        final operation = OfflineOperationRecord(
          partitionId: 'partition',
          generation: 0,
          operationId: 'add-operation',
          items: <OfflineWriteStatus>[
            OfflineWriteStatus(
              recordId: 'add-record',
              operationId: 'add-operation',
              itemIndex: 0,
              state: OfflineWriteState.confirmed,
              attemptCount: 1,
              receiptResult: OfflineEdgeAddReceiptResult(weight),
            ),
          ],
          updatedAt: time,
          terminalAt: time,
        );
        final encoded = OfflineCodec.encodeOperationRecord(operation);
        final decoded = OfflineCodec.decodeOperationRecord(encoded);
        final result =
            decoded.items.single.receiptResult as OfflineEdgeAddReceiptResult;
        expect(OfflineCodec.encodeOperationRecord(decoded), encoded);
        if (weight.isNaN) {
          expect(result.effectiveWeight.isNaN, isTrue);
        } else {
          expect(result.effectiveWeight, weight);
          if (weight == 0) expect(result.effectiveWeight.isNegative, isTrue);
        }
      }

      for (final weight in <double>[
        double.infinity,
        double.negativeInfinity,
        double.nan,
        double.maxFinite,
      ]) {
        expect(
          () => OfflineReceiptAddEdgeIntent(
            Edge(tail: 'tail', head: 'head', weight: weight, expiration: null),
            testBytes(24, 1),
          ),
          throwsA(isA<OfflineArgumentException>()),
        );
      }
      expect(
        () => OfflineEdgeAddReceiptResult(double.maxFinite),
        throwsA(isA<OfflineArgumentException>()),
      );
    });

    test('rejects malformed or mismatched receipt persistence', () {
      final capability = offlineReceiptCapability();
      final fingerprint = testBytes(32, 7);
      final copiedPolicy = OfflineReceiptPolicy(
        deploymentEpoch: capability.policy.deploymentEpoch,
        retention: const Duration(hours: 1),
        maxEntries: BigInt.one,
        maxBytes: BigInt.one,
        fingerprint: fingerprint,
      );
      fingerprint[0] = 0;
      final exposedFingerprint = copiedPolicy.fingerprint;
      exposedFingerprint[1] = 0;
      expect(copiedPolicy.fingerprint, everyElement(7));
      expect(
        () => OfflineReceiptPolicy(
          deploymentEpoch: capability.policy.deploymentEpoch,
          retention: const Duration(minutes: 59),
          maxEntries: BigInt.one,
          maxBytes: BigInt.one,
          fingerprint: testBytes(32, 1),
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => OfflineReceiptPolicy(
          deploymentEpoch: capability.policy.deploymentEpoch,
          retention: const Duration(days: 31),
          maxEntries: BigInt.one,
          maxBytes: BigInt.one,
          fingerprint: testBytes(32, 1),
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => OfflineReceiptPolicy(
          deploymentEpoch: capability.policy.deploymentEpoch,
          retention: const Duration(hours: 1),
          maxEntries: BigInt.one,
          maxBytes: BigInt.one,
          fingerprint: Uint8List(32),
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => OfflineReceiptEvidence(
          operationId: testReceiptOperationId(
            epoch: ReceiptEpoch(testBytes(16, 99)),
            random: 1,
          ),
          groupId: ReceiptGroupId(testBytes(16, 21)),
          endpoint: capability.endpoint,
          mutation: ReceiptMutationKind.vertexDelete,
          policy: capability.policy,
          itemIndex: 0,
          itemCount: 1,
          state: OfflineReceiptReconciliationState.statusRequired,
        ),
        throwsA(isA<OfflineArgumentException>()),
      );

      final valid = OfflineOutboxRecord(
        recordId: 'receipt-record',
        operationId: 'receipt-operation',
        itemIndex: 0,
        partitionId: 'partition',
        intent: OfflineDeleteVertexIntent('key'),
        enqueuedAt: time,
        ordinal: 1,
        state: OfflineOutboxState.enqueued,
        attemptCount: 0,
        generation: 0,
        receipt: OfflineReceiptEvidence(
          operationId: testReceiptOperationId(
            epoch: capability.policy.deploymentEpoch,
            random: 1,
          ),
          groupId: ReceiptGroupId(testBytes(16, 21)),
          endpoint: capability.endpoint,
          mutation: ReceiptMutationKind.vertexDelete,
          policy: capability.policy,
          itemIndex: 0,
          itemCount: 1,
          state: OfflineReceiptReconciliationState.statusRequired,
        ),
      );
      expect(
        () => valid.copyWith(state: OfflineOutboxState.expired),
        throwsA(isA<OfflineArgumentException>()),
      );
      expect(
        () => valid.copyWith(
          receipt: valid.receipt!.copyWith(
            state: OfflineReceiptReconciliationState.noLongerProvable,
          ),
        ),
        throwsA(isA<OfflineArgumentException>()),
      );
      final map =
          jsonDecode(OfflineCodec.encodeOutboxRecord(valid))
              as Map<String, Object?>;
      final receipt = map['receipt']! as Map<String, Object?>;
      receipt['operationId'] = base64Url
          .encode(Uint8List(48))
          .replaceAll('=', '');
      expect(
        () => OfflineCodec.decodeOutboxRecord(jsonEncode(map)),
        throwsA(isA<OfflineCodecException>()),
      );

      final putMap =
          jsonDecode(
                OfflineCodec.encodeOutboxRecord(
                  OfflineOutboxRecord(
                    recordId: 'put-record',
                    operationId: 'put-operation',
                    itemIndex: 0,
                    partitionId: 'partition',
                    intent: OfflinePutVertexIntent(
                      Vertex(
                        key: 'key',
                        value: VertexValue.string('value'),
                        expiration: null,
                      ),
                    ),
                    enqueuedAt: time,
                    ordinal: 1,
                    state: OfflineOutboxState.enqueued,
                    attemptCount: 0,
                    generation: 0,
                  ),
                ),
              )
              as Map<String, Object?>;
      final putIntent = putMap['intent']! as Map<String, Object?>;
      putIntent['contributionId'] = 'AQ';
      expect(
        () => OfflineCodec.decodeOutboxRecord(jsonEncode(putMap)),
        throwsA(isA<OfflineCodecException>()),
      );
    });

    test('reads operation schema v1 without receipt results', () {
      final current = OfflineOperationRecord(
        partitionId: 'partition',
        generation: 0,
        operationId: 'operation',
        items: <OfflineWriteStatus>[
          OfflineWriteStatus(
            recordId: 'record',
            operationId: 'operation',
            itemIndex: 0,
            state: OfflineWriteState.confirmed,
            attemptCount: 1,
          ),
        ],
        updatedAt: time,
        terminalAt: time,
      );
      final map =
          jsonDecode(OfflineCodec.encodeOperationRecord(current))
              as Map<String, Object?>;
      map['schema'] = 1;
      final item =
          (map['items']! as List<Object?>).single as Map<String, Object?>;
      item.remove('receiptResult');

      final decoded = OfflineCodec.decodeOperationRecord(jsonEncode(map));
      expect(decoded.items.single.receiptResult, isNull);
      expect(
        OfflineCodec.encodeOperationRecord(decoded),
        contains('"schema":2'),
      );
    });
  });
}
