part of '../automatic_session_test.dart';

// Focused R2-R4 boundaries for the qualified automatic first-send decision.
void firstSendContract() {
  Future<OfflineLanternSession> reopen(Harness h) async {
    expect(h.client.isClosed, true);
    h.client = LanternClient.connect(
      endpoint,
      allowInsecure: true,
      clock: h.clock.call,
      transport: FakeTransportBuilder().build(),
    );
    return h.open();
  }

  Future<MutationTicket<double>> admitAdd(
    Harness h,
    OfflineLanternSession session,
  ) => session.mutations.submit(
    MutationRequest.addEdges([
      EdgeInput(
        tail: 'tail',
        head: 'head',
        weight: 3,
        expiresAt: h.clock.now.add(const Duration(minutes: 20)),
      ),
    ]),
  );

  void sameIntent(OfflineOutboxRecord current, OfflineOutboxRecord original) {
    expect(current.recordId, original.recordId);
    expect(current.operationId, original.operationId);
    expect(current.intent.expiration, original.intent.expiration);
    expect(
      (current.intent as OfflineReceiptAddEdgeIntent).contributionId,
      (original.intent as OfflineReceiptAddEdgeIntent).contributionId,
    );
  }

  for (final mode in ['fresh', 'stale', 'final-delay']) {
    test('R2/R4 prepared false trusted reopen $mode', () async {
      final h = Harness();
      addTearDown(h.close);
      h.remote.reply = const MutationKnownEffect<double>(3);
      final entered = Completer<void>(), gate = Completer<void>();
      h.receipt.beforeReceiptCapabilityReturn = () {
        entered.complete();
        return gate.future;
      };
      final first = await h.open();
      final ticket = await admitAdd(h, first);
      await h.tick();
      await entered.future.timeout(const Duration(seconds: 2));
      final prepared = (await h.rows()).single;
      expect(prepared.preparation, isA<OfflinePrepared>());
      expect(prepared.mayHaveDispatched, false);
      expect(prepared.receipt!.mayHaveDispatched, false);
      expect(prepared.attemptCount, 0);
      expect(h.receipt.receiptStatusCalls, 0);
      final closing = first.close();
      await flush();
      gate.complete();
      await closing.timeout(const Duration(seconds: 2));
      expect(h.remote.calls, 0);
      final retained = (await h.rows()).single;
      sameIntent(retained, prepared);
      expect(retained.receipt!.operationId, prepared.receipt!.operationId);
      expect(retained.mayHaveDispatched, false);
      expect(retained.attemptCount, 0);
      h.receipt.beforeReceiptCapabilityReturn = null;
      if (mode != 'fresh') {
        h.clock.now = h.clock.now.add(
          mode == 'stale'
              ? const Duration(minutes: 5)
              : const Duration(minutes: 3, seconds: 50),
        );
        h.receipt.receiptCapability = offlineReceiptCapability(
          serverNow: h.clock.now,
        );
      }
      var finalBoundaryReached = false;
      h.remote.before = (record) async {
        final persisted = (await h.rows()).single;
        expect(persisted.mayHaveDispatched, true);
        expect(persisted.receipt!.mayHaveDispatched, true);
        expect(persisted.attemptCount, 1);
        sameIntent(persisted, prepared);
        finalBoundaryReached = true;
        if (mode == 'final-delay') {
          h.clock.now = h.clock.now.add(const Duration(seconds: 20));
          // The lease and intent still permit dispatch; only receipt freshness
          // has aged beyond its original allowance at beforeDispatch.
          expect(h.clock.now.isBefore(persisted.leaseUntil!), true);
          expect(h.clock.now.isBefore(persisted.intent.expiration!), true);
        }
      };
      final next = await reopen(h);
      await h.tick();
      if (mode == 'stale') {
        final refreshed = (await h.rows()).single;
        sameIntent(refreshed, prepared);
        expect(
          refreshed.receipt!.operationId,
          isNot(prepared.receipt!.operationId),
        );
        expect(refreshed.receipt!.groupId, isNot(prepared.receipt!.groupId));
        expect(refreshed.mayHaveDispatched, false);
        expect(refreshed.attemptCount, 0);
        expect(h.remote.calls, 0);
        await h.tick(1000);
      }
      expect(finalBoundaryReached, true);
      expect(h.receipt.receiptStatusCalls, 0);
      expect(h.receipt.receiptPrepareCalls, mode == 'stale' ? 2 : 1);
      if (mode == 'final-delay') {
        expect(h.remote.calls, 0);
        final held = (await h.rows()).single;
        sameIntent(held, prepared);
        expect(held.receipt!.operationId, prepared.receipt!.operationId);
        expect(held.mayHaveDispatched, true);
        expect(held.receipt!.mayHaveDispatched, true);
        expect(held.diagnosticCode, 'receipt_protocol');
      } else {
        expect(h.remote.calls, 1);
        final sent = h.remote.sent.single;
        sameIntent(sent, prepared);
        if (mode == 'fresh') {
          expect(sent.receipt!.operationId, prepared.receipt!.operationId);
          expect(sent.receipt!.groupId, prepared.receipt!.groupId);
        }
        expect(await h.rows(), isEmpty);
        expect(
          (await next.mutations.reobserve(
            ticket.reference,
          )).reply!.items.single,
          isA<MutationKnownEffect<double>>().having(
            (r) => r.effect,
            'effect',
            3,
          ),
        );
      }
    });
  }

  test(
    'R3 committed marker zero wire then denied absence keeps policy hold',
    () async {
      final h = Harness();
      addTearDown(h.close);
      final entered = Completer<void>(), gate = Completer<void>();
      h.remote.before = (_) {
        entered.complete();
        return gate.future;
      };
      final first = await h.open();
      await admitAdd(h, first);
      await h.tick();
      await entered.future.timeout(const Duration(seconds: 2));
      final marked = (await h.rows()).single;
      expect(marked.mayHaveDispatched, true);
      expect(marked.receipt!.mayHaveDispatched, true);
      expect(marked.attemptCount, 1);
      expect(h.remote.calls, 0);
      final closing = first.close();
      await flush();
      gate.complete();
      await closing.timeout(const Duration(seconds: 2));
      expect(h.remote.calls, 0);
      h.remote.before = null;
      h.receipt.receiptCalls.clear();
      h.receipt.receiptStatusFailures.add(
        const OfflineRemoteFailure(
          OfflineRemoteErrorKind.permanent,
          'permission_denied',
        ),
      );
      final next = await reopen(h);
      final health = <LanternSyncStatus>[];
      final sub = next.syncStatus.listen(health.add);
      addTearDown(sub.cancel);
      await h.tick(1000);
      expect(h.receipt.receiptCalls, ['status']);
      expect(h.receipt.receiptStatusIds, [marked.receipt!.operationId]);
      expect(h.remote.calls, 0);
      expect(health.any((s) => s.pause == SyncPause.policy), true);
      expect(
        await h.store.transaction(
          (tx) => tx.replayPausedForPolicy(h.identity.stableIdentity),
        ),
        true,
      );
      final held = (await h.rows()).single;
      sameIntent(held, marked);
      expect(held.receipt!.operationId, marked.receipt!.operationId);
      expect(held.receipt!.groupId, marked.receipt!.groupId);
      expect(held.mayHaveDispatched, true);
      expect(held.attemptCount, 1);
      await next.resume();
      await next.setTransferAllowed(false);
      await next.setTransferAllowed(true);
      await h.tick(1000);
      expect(h.remote.calls, 0);
      expect(h.receipt.receiptStatusCalls, 1);
      expect(
        (await h.rows()).single.receipt!.operationId,
        marked.receipt!.operationId,
      );
    },
  );

  test(
    'R4 first-send current capability mismatch does not dispatch or remint',
    () async {
      final h = Harness();
      addTearDown(h.close);
      OfflineOutboxRecord? prepared;
      h.receipt.beforeReceiptPreparationReturn = () async {
        h.receipt.receiptCapability = offlineReceiptCapability(
          serverNow: h.clock.now,
          generation: 99,
        );
      };
      h.receipt.beforeReceiptCapabilityReturn = () async {
        prepared = (await h.rows()).single;
        expect(prepared!.receipt, isNotNull);
        expect(prepared!.mayHaveDispatched, false);
      };
      final session = await h.open();
      final ticket = await admitAdd(h, session);
      await h.tick();
      expect(prepared, isNotNull);
      expect(h.remote.calls, 0);
      expect(h.receipt.receiptStatusCalls, 0);
      expect(h.receipt.receiptPrepareCalls, 1);
      // Definitely unsent terminal work retires the outbox. Its retained
      // aggregate still records zero attempts and the continuity rejection.
      expect(await h.rows(), isEmpty);
      final terminal = (await h.store.transaction(
        (tx) => tx.getOperation(
          h.identity.stableIdentity,
          ticket.reference.operationId.encoded,
        ),
      ))!.items.single;
      expect(terminal.recordId, prepared!.recordId);
      expect(terminal.diagnosticCode, 'receipt_continuity');
      expect(terminal.mayHaveDispatched, false);
      expect(terminal.attemptCount, 0);
      expect(
        (await session.mutations.getStatus(
          ticket.reference.operationId,
        )).terminal,
        true,
      );
    },
  );
}
