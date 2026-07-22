import 'dart:io';
import 'dart:typed_data';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:test/test.dart';

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

    test('preserves exact edge float bits and Add contribution IDs', () {
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
        () => OfflineCodec.decodeOutboxRecord(
          OfflineCodec.encodeOutboxRecord(
            leaseRecord(state: OfflineOutboxState.sending),
          ),
        ),
        throwsA(isA<OfflineCodecException>()),
      );
      expect(
        () => OfflineCodec.decodeOutboxRecord(
          OfflineCodec.encodeOutboxRecord(
            leaseRecord(
              state: OfflineOutboxState.enqueued,
              leaseOwner: 'owner',
              leaseUntil: time.add(const Duration(seconds: 1)),
            ),
          ),
        ),
        throwsA(isA<OfflineCodecException>()),
      );
    });

    test('matches deterministic v1 fixtures byte-for-byte', () {
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
        expect(encoded, fixture);
      }
    });

    test('round trips content-free durable operation aggregates', () {
      final record = OfflineOperationRecord(
        partitionId: 'partition',
        generation: 2,
        operationId: 'operation',
        items: const <OfflineWriteStatus>[
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
  });
}
