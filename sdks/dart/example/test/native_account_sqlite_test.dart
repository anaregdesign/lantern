import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/installation_policy.dart';
import 'package:lantern_example/native_credential_store.dart';
import 'package:lantern_example/offline_session.dart';
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
      final temporary = await Directory.systemTemp.createTemp(
        'lantern-native-accounts-',
      );
      // The automatic native owner resolves filesystem aliases before opening
      // SQLite. Inspection connections must use that same serialization lane.
      final directory = Directory(await temporary.resolveSymbolicLinks());
      final probe = '${directory.path}/inspection.db';
      expect(
        SqliteOfflineStore.canonicalAutomaticPath(probe),
        probe,
        reason:
            'inspection and automatic owner must share the canonical SQLite lane',
      );
      sqflite.databaseFactory = databaseFactoryFfi;
      await databaseFactoryFfi.setDatabasesPath(directory.path);
      final store = MemoryCredentials(), providers = Providers();
      final wire = NativeWire(store);
      var offset = Duration.zero;
      DateTime clock() => DateTime.now().toUtc().add(offset);
      final paths = <String, String>{};
      final scopes = <String, String>{};
      final credentialEvents = <String>[];
      final policy = SqliteInstallationPolicy(
        identity: 'isolated-host-fixture',
        version: 1,
        evaluate: (_) async => SqliteInstallationEvidence.verified(
          installationId: 'native-account-host-test',
          legacyAdoptionVerified: true,
        ),
      );
      final controller = NativeSessionController(
        endpoint: endpoint,
        providers: providers,
        store: store,
        openData: (client, session, fresh, lifetime) async {
          final partition = nativeAccountPartition(endpoint, session);
          final binding = LanternSessionBinding.native(
            session,
            securityDomain: 'native-account-test',
          );
          final path =
              '${directory.path}/${offlineDatabaseFileName(endpoint, partition)}';
          paths[partition] = path;
          scopes[partition] = binding.stableIdentity;
          return AppDataSessionFactory(
            lifetime: lifetime,
            client: client,
            binding: binding,
            profile: AppDataProfile.durable,
            legacyPartition: partition,
            databaseFactory: databaseFactoryFfi,
            clock: clock,
            resolveLocation: () async => AppSqliteLocation(path, policy, null),
          ).open(
            activate: false,
            freshCredentials: fresh
                ? OfflineVerifiedCredentials(
                    binding: binding,
                    install: () {
                      credentialEvents.add(session.principal.subject);
                    },
                  )
                : null,
          );
        },
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
      await a.dataSession!.suspend();
      final offline = await SqliteOfflineStore.open(
        path: paths[a.partition]!,
        databaseFactory: databaseFactoryFfi,
      );
      addTearDown(offline.close);
      final scope = scopes[a.partition]!;
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
      await offline.transaction((tx) async {
        final pending = await tx.enqueue(
          OfflineOutboxRecord(
            recordId: 'old-delete',
            operationId: 'old-operation',
            itemIndex: 0,
            partitionId: scope,
            intent: OfflineDeleteEdgeIntent(
              const EdgeRef('orders:a', 'orders:b'),
            ),
            enqueuedAt: now,
            ordinal: 0,
            state: OfflineOutboxState.enqueued,
            attemptCount: 1,
            nextAttemptAt: clock().add(const Duration(minutes: 1)),
            generation: 0,
            receipt: receipt,
          ),
        );
        await tx.putOperation(
          OfflineOperationRecord(
            partitionId: scope,
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
          scope,
          OfflineCacheRecord.value(
            partitionId: scope,
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
      await offline.close();
      providers.subject = 'b';
      await controller.login(issuer, 'mobile');
      final b = controller.account!;
      expect(a.client.isClosed, isTrue);
      expect(a.cancellation.isCanceled, isTrue);
      expect(b.partition, isNot(a.partition));
      await b.dataSession!.suspend();
      final bStore = await SqliteOfflineStore.open(
        path: paths[b.partition]!,
        databaseFactory: databaseFactoryFfi,
      );
      addTearDown(bStore.close);
      expect(
        await bStore.transaction(
          (tx) async =>
              (await tx.scanOutbox(scopes[b.partition]!, limit: 10)).records,
        ),
        isEmpty,
      );
      await bStore.close();
      expect(wire.receiptStatuses, isEmpty);
      expect(wire.dataDispatches, isEmpty);
      offset = const Duration(minutes: 1);
      wire.authorityBinding++;
      wire.registered.add(
        'a',
      ); // UID assignment does not move the account store.
      providers.subject = 'a';
      await controller.login(issuer, 'mobile');
      final again = controller.account!;
      expect(again.partition, a.partition);
      expect(b.client.isClosed, isTrue);
      // No Get, drain or connectivity hint triggers delivery. The existing
      // runtime sees the original due time while opening the qualified owner.
      await wire.receiptSeen.future.timeout(const Duration(seconds: 5));
      await again.dataSession!.suspend();
      final reopened = await SqliteOfflineStore.open(
        path: paths[a.partition]!,
        databaseFactory: databaseFactoryFfi,
      );
      addTearDown(reopened.close);
      final pending = await reopened.transaction(
        (tx) => tx.getOutbox(scope, 'old-delete'),
      );
      expect(pending!.operationId, 'old-operation');
      final inspected =
          await (again.dataSession!.data! as OfflineLanternSession)
              .inspectLegacyOperation('old-operation');
      expect(inspected!.operationId, 'old-operation');
      expect(inspected.items.single.mayHaveDispatched, true);
      expect(pending.receipt!.operationId, id);
      expect(pending.receipt!.mayHaveDispatched, isTrue);
      final cache = await again.dataSession!.data!.getVertex(
        'private-a',
        options: const GetOptions(source: ReadSource.cacheOnly),
      );
      expect(cache.confirmed.state, ConfirmedState.unknown);
      expect(cache.view, isNull);
      expect(wire.receiptStatuses, hasLength(1));
      expect(wire.receiptStatuses.single.$1, 'a');
      expect(wire.receiptStatuses.single.$2, [base64Encode(id.bytes)]);
      expect(wire.dataDispatches, isEmpty);
      final retained = await reopened.transaction(
        (tx) => tx.getOutbox(scope, 'old-delete'),
      );
      expect(credentialEvents, ['a', 'b', 'a']);
      expect(retained!.receipt!.operationId, id);
      expect(retained.receipt!.mayHaveDispatched, isTrue);
      expect(retained.enqueuedAt, now);
      await reopened.close();
    },
  );
}
