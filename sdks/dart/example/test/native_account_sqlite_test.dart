import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_example/native_session_controller.dart';
import 'package:sqflite/sqflite.dart' as sqflite;
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

import 'native_test_helpers.dart'
    show MemoryCredentials, NativeWire, Providers, endpoint, issuer;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  test(
    'actual app SQLite A-B-A retains unknown mutation identity and dispatch evidence',
    () async {
      final directory = await Directory.systemTemp.createTemp(
        'lantern-native-accounts-',
      );
      sqflite.databaseFactory = databaseFactoryFfi;
      await databaseFactoryFfi.setDatabasesPath(directory.path);
      final store = MemoryCredentials(), providers = Providers();
      final wire = NativeWire(store);
      final controller = NativeSessionController(
        endpoint: endpoint,
        providers: providers,
        store: store,
        createClient: (credential) => LanternClient.connect(
          endpoint,
          tokenProvider: () => credential,
          transport: wire,
        ),
      );
      addTearDown(() async {
        await controller.close();
        await directory.delete(recursive: true);
      });
      await controller.login(issuer, 'mobile');
      final a = controller.account!;
      final offline = a.offline!;
      final now = DateTime.now().toUtc().subtract(const Duration(minutes: 1));
      Uint8List filled(int n, int value) =>
          Uint8List.fromList(List.filled(n, value));
      final idBytes = Uint8List(49)..[0] = 1;
      idBytes.setRange(1, 17, filled(16, 1));
      ByteData.sublistView(idBytes).setUint64(17, now.millisecondsSinceEpoch);
      idBytes.fillRange(25, 49, 6);
      final id = ReceiptOperationId(idBytes);
      final receipt = OfflineReceiptEvidence(
        operationId: id,
        groupId: ReceiptGroupId(filled(16, 5)),
        endpoint: ReceiptEndpoint(
          nodeId: filled(16, 2),
          generation: filled(16, 3),
        ),
        mutation: ReceiptMutationKind.edgeDelete,
        policy: OfflineReceiptPolicy(
          deploymentEpoch: ReceiptEpoch(filled(16, 1)),
          retention: const Duration(hours: 24),
          maxEntries: BigInt.from(1000),
          maxBytes: BigInt.from(1000000),
          fingerprint: filled(32, 4),
        ),
        itemIndex: 0,
        itemCount: 1,
        state: OfflineReceiptReconciliationState.statusRequired,
        mayHaveDispatched: true,
      );
      // A concrete durable unknown-outcome fixture: dispatch happened in account
      // A, its response was lost. Account transitions must preserve these facts.
      await offline.store.transaction((tx) async {
        final pending = await tx.enqueue(
          OfflineOutboxRecord(
            recordId: 'old-delete',
            operationId: 'old-operation',
            itemIndex: 0,
            partitionId: a.partition,
            intent: OfflineDeleteEdgeIntent(
              const EdgeRef('orders:a', 'orders:b'),
            ),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 1,
            nextAttemptAt: now.add(const Duration(seconds: 1)),
            generation: 0,
            receipt: receipt,
          ),
        );
        await tx.putOperation(
          OfflineOperationRecord(
            partitionId: a.partition,
            generation: 0,
            operationId: pending.operationId,
            items: [
              OfflineWriteStatus(
                recordId: pending.recordId,
                operationId: pending.operationId,
                itemIndex: 0,
                state: OfflineWriteState.retryScheduled,
                attemptCount: 1,
              ),
            ],
            updatedAt: now,
          ),
        );
        await tx.putCache(
          a.partition,
          OfflineCacheRecord.value(
            partitionId: a.partition,
            generation: 0,
            key: const OfflineEntityKey.vertex('private-a'),
            entity: Vertex(
              key: 'private-a',
              expiration: null,
              value: VertexValue.string('old granted view'),
            ),
            validatedAt: now,
            lastAccessAt: now,
          ),
        );
      });
      providers.subject = 'b';
      await controller.login(issuer, 'mobile');
      final b = controller.account!;
      expect(a.client.isClosed, isTrue);
      expect(a.cancellation.isCanceled, isTrue);
      expect(b.partition, isNot(a.partition));
      expect(await b.offline!.repository.listPending(b.partition), isEmpty);
      expect(await b.offline!.repository.drain(b.partition), 0);
      expect(wire.receiptStatuses, isEmpty);
      expect(wire.dataDispatches, isEmpty);
      providers.subject = 'a';
      await controller.login(issuer, 'mobile');
      final again = controller.account!;
      expect(again.partition, a.partition);
      expect(b.client.isClosed, isTrue);
      final pending = await again.offline!.store.transaction(
        (tx) => tx.getOutbox(a.partition, 'old-delete'),
      );
      expect(pending!.operationId, 'old-operation');
      expect(pending.receipt!.operationId, id);
      expect(pending.receipt!.mayHaveDispatched, isTrue);
      final cache = await again.offline!.repository.readVertex(
        a.partition,
        'private-a',
        policy: OfflineReadPolicy.cacheOnly,
      );
      expect(cache.state, OfflineReadState.unknown);
      expect(cache.value, isNull);
      await again.offline!.repository.drain(a.partition);
      expect(wire.receiptStatuses, hasLength(1));
      expect(wire.receiptStatuses.single.$1, 'a');
      expect(wire.receiptStatuses.single.$2, [base64Encode(id.bytes)]);
      expect(wire.dataDispatches, isEmpty);
      final retained = await again.offline!.store.transaction(
        (tx) => tx.getOutbox(a.partition, 'old-delete'),
      );
      expect(retained!.receipt!.operationId, id);
      expect(retained.receipt!.mayHaveDispatched, isTrue);
    },
  );
}
