import 'dart:async';

import 'package:connectrpc/connect.dart' as connect;
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';
import 'package:test/test.dart';

import '../tool/automatic_baseline.dart';
import 'helpers.dart';
import 'support/session_clock.dart';

void main() {
  test(
    'G5 two scopes progress through 100 fixed turns under full outbox and hot Traversal',
    () async {
      final clocks = [Clock(), Clock()];
      final stores = [MemoryOfflineStore(), MemoryOfflineStore()];
      final traffic = BaselineTraffic();
      final wires = [
        for (final clock in clocks)
          BaselineWire(clock.now.add(const Duration(hours: 1)), traffic),
      ];
      final heldTraversal = Completer<void>(), heldSend = Completer<void>();
      var firstTraversal = true, retried = false;
      final traversalRequests = <String, int>{};
      wires[0].before = (method, request) async {
        if (method == 'Illuminate') {
          final shape = request.keys.toList().join(',');
          traversalRequests.update(shape, (n) => n + 1, ifAbsent: () => 1);
        }
        if (method == 'Illuminate' && firstTraversal) {
          firstTraversal = false;
          await heldTraversal.future;
        }
        if (method == 'PutVertices' && wires[0].sends == 1) {
          await heldSend.future;
        } else if (method == 'PutVertices' && !retried) {
          retried = true;
          throw connect.ConnectException(
            connect.Code.resourceExhausted,
            'fixed retry',
            metadata: connect.Headers()..add('retry-after', '1'),
          );
        }
      };
      final receipt = FakeOfflineRemote()
        ..receiptCapability = offlineReceiptCapability(
          serverNow: clocks[1].now,
        );
      final recoveredSend = _CommittedLostReply(receipt);
      final sessions = <OfflineLanternSession>[];
      final identities = <LanternSessionBinding>[];
      final subscriptions = <StreamSubscription<Object?>>[];
      addTearDown(() async {
        if (!heldTraversal.isCompleted) heldTraversal.complete();
        if (!heldSend.isCompleted) heldSend.complete();
        for (final sub in subscriptions) {
          await sub.cancel();
        }
        for (final session in sessions.reversed) {
          await session.close();
        }
      });
      for (var i = 0; i < 2; i++) {
        final client = LanternClient.connect(
          Uri.parse('http://localhost:6380'),
          allowInsecure: true,
          transport: wires[i],
          clock: clocks[i].call,
          retryPolicy: const RetryPolicy(maxAttempts: 1),
        );
        final identity = LanternSessionBinding.application(
          serviceOrigin: client.endpoint,
          securityDomain: 'fixed-composition',
          accountIdentity: 'scope-$i',
          authorityBinding: 'a',
        );
        identities.add(identity);
        sessions.add(
          await OfflineLanternSession.openStore(
            client: client,
            storeKey: stores[i],
            createStore: () async => stores[i],
            closeStore: (_) async {},
            binding: identity,
            durability: AdmissionDurability.memory,
            clock: clocks[i].call,
            schedule: clocks[i].schedule,
            transferAllowed: false,
            receiptRemote: i == 1 ? receipt : null,
            mutationRemote: i == 1 ? recoveredSend : null,
            readRoute: LanternSingleResponderRoute.certified(
              client: client,
              responderId: 'fixed-$i',
              isCurrent: () => true,
            ),
          ),
        );
      }
      final a = sessions[0], b = sessions[1];
      final admitted = await a.mutations.submit(
        MutationRequest.putVertices([
          for (var i = 0; i < 1000; i++)
            VertexInput(
              key: 'full-$i',
              value: const NilValue(),
              expiresAt: clocks[0].now.add(
                i == 999
                    ? const Duration(milliseconds: 2250)
                    : const Duration(hours: 1),
              ),
            ),
        ]),
      );
      Future<List<String>> admissionRows() => stores[0].transaction(
        (tx) async => [
          for (final row in await tx.outbox(identities[0].stableIdentity))
            OfflineCodec.encodeOutboxRecord(row),
          for (final row in await tx.operations(identities[0].stableIdentity))
            OfflineCodec.encodeOperationRecord(row),
        ],
      );
      final beforeOverflow = await admissionRows();
      await expectLater(
        a.mutations.submit(
          MutationRequest.putVertices([
            VertexInput(key: 'overflow', value: const NilValue()),
          ]),
        ),
        throwsA(isA<OfflineCapacityException>()),
      );
      expect(await admissionRows(), beforeOverflow);
      final receiptTicket = await b.mutations.submit(
        MutationRequest.putVerticesIfAbsent([
          VertexInput(
            key: 'receipt',
            value: const NilValue(),
            expiresAt: clocks[1].now.add(const Duration(hours: 1)),
          ),
        ]),
      );
      var ordinary = 0, slowResults = 0, hotResults = 0, hotUnknown = 0;
      subscriptions.add(
        a
            .watchIlluminate(
              'v0',
              traversal: const BfsOptions(step: 2, fanOut: 2),
            )
            .listen((s) {
              if (s.confirmed.state == ConfirmedState.present) slowResults++;
            }),
      );
      subscriptions.add(
        a.watchIlluminate('v0', traversal: const PprOptions()).listen((s) {
          if (s.confirmed.state == ConfirmedState.present) hotResults++;
          if (s.confirmed.state == ConfirmedState.unknown) hotUnknown++;
        }),
      );
      subscriptions.add(
        b
            .watchVertex(
              'v0',
              options: const WatchOptions(pollInterval: Duration(seconds: 1)),
            )
            .listen((s) {
              if (s.confirmed.state == ConfirmedState.present) ordinary++;
            }),
      );
      await a.setTransferAllowed(true);
      await b.setTransferAllowed(true);
      int? pausedCalls;
      for (var turn = 0; turn < 100; turn++) {
        // A real local invalidation source marks existing queries dirty; it does
        // not create another scheduler or force a manual network read.
        await stores[0].transaction(
          (tx) => tx.invalidateTraversals(identities[0].stableIdentity),
        );
        if (turn == 25) {
          expect(firstTraversal, false);
          expect(ordinary, greaterThan(0));
          expect(
            (await b.mutations.getStatus(
              receiptTicket.reference.operationId,
            )).terminal,
            true,
          );
          expect(receipt.receiptStatusCalls, greaterThanOrEqualTo(1));
          expect(recoveredSend.calls, 1);
          heldTraversal.complete();
          heldSend.complete();
        }
        if (turn == 40) {
          await a.setTransferAllowed(false);
          pausedCalls = wires[0].calls;
        }
        // Leave enough of the fixed 100 turns for the 32-record maintenance
        // pages to reach the tail of the full 1,000-record queue.
        if (turn == 89) {
          expect(wires[0].calls, pausedCalls);
          final status = await a.mutations.getStatus(
            admitted.reference.operationId,
          );
          expect(
            status.items.last.state,
            MutationItemState.failed,
            reason: 'never-sent TTL advances during transfer pause',
          );
        }
        if (turn == 90) await a.setTransferAllowed(true);
        await clocks[0].advance(const Duration(milliseconds: 50));
        await clocks[1].advance(const Duration(milliseconds: 50));
        expect(clocks[0].active + clocks[1].active, lessThanOrEqualTo(2));
        if (turn >= 2 && turn < 19) {
          expect(
            wires[0].sends,
            2,
            reason: 'fixed Retry-After holds new dispatch without a hint storm',
          );
        }
      }
      expect(retried, true);
      expect(slowResults, greaterThan(0));
      // Continuous mutation/invalidation may fence a hot result back to
      // Unknown. Fairness requires bounded refresh attempts, not stale Graph
      // publication; the ordinary scope has already completed by turn25.
      expect(hotResults + hotUnknown, greaterThan(0));
      expect(traversalRequests, hasLength(2));
      expect(traversalRequests.values, everyElement(inInclusiveRange(2, 101)));
      expect(traffic.peakSends, lessThanOrEqualTo(4));
      expect(traffic.peakReads, lessThanOrEqualTo(8));
      expect(traffic.peakTraversals, lessThanOrEqualTo(2));
      expect(
        wires.every(
          (w) => w.peakSends <= 2 && w.peakReads <= 4 && w.peakTraversals <= 1,
        ),
        true,
      );
      expect(clocks.every((c) => c.callbacks <= 2000), true);
      for (final sub in subscriptions) {
        await sub.cancel();
      }
      subscriptions.clear();
      for (final session in sessions.reversed) {
        await session.close();
      }
      expect(traffic.active, 0);
      expect(clocks.every((c) => c.active == 0), true);
      for (var i = 0; i < 2; i++) {
        final rows = await stores[i].transaction(
          (tx) => tx.outbox(identities[i].stableIdentity),
        );
        expect(
          rows.where((r) => r.leaseOwner != null || r.leaseUntil != null),
          isEmpty,
        );
        final owner = await stores[i].acquireAutomaticOwner(
          scope: identities[i].stableIdentity,
          bindingFingerprint: 'a',
          configurationFingerprint: 'after-joined',
        );
        await owner.release();
      }
      expect(
        (await stores[0].transaction(
          (tx) => tx.outbox(identities[0].stableIdentity),
        )).length,
        greaterThan(0),
        reason: 'teardown does not force-drain retained work',
      );
    },
  );
}

final class _CommittedLostReply implements OfflineBoundMutationRemote {
  _CommittedLostReply(this.receipt);
  final FakeOfflineRemote receipt;
  int calls = 0;
  @override
  Future<MutationReply<Object?>> sendBoundMutation(
    OfflineOutboxRecord record, {
    required void Function() beforeDispatch,
    required void Function() beforePublication,
    LanternCancellationToken? cancellation,
  }) async {
    beforeDispatch();
    calls++;
    final evidence = record.receipt!;
    receipt.receiptStatuses[evidence.operationId] = OfflineReceiptStatus(
      operationId: evidence.operationId,
      state: ReceiptStatusState.confirmed,
      groupId: evidence.groupId,
      mutation: evidence.mutation,
      itemIndex: 0,
      itemCount: 1,
      result: const OfflineVertexPutReceiptResult(PutOutcome.appliedAndLive),
    );
    throw const OfflineRemoteFailure(
      OfflineRemoteErrorKind.unavailable,
      'lost reply',
    );
  }
}
