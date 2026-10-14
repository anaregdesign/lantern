import 'dart:async';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';
import 'bound_mutation_remote.dart';
import 'claim_fence.dart';
import 'errors.dart';
import 'mutation_state.dart';
import 'remote.dart';
import 'store.dart';
import 'types.dart';

/// Bounded delivery seam for the automatic owner introduced by #1769.
/// This is qualified infrastructure, never an application Sync requirement.
final class OfflineMutationDelivery {
  /// Binds one owned pass engine and finite lifecycle limits; scheduling belongs to the runtime.
  OfflineMutationDelivery({
    required this.store,
    required this.partitionId,
    required this.generation,
    required this.remote,
    required this.mutationRemote,
    required this.clock,
    required this.fence,
    required this.onHandling,
    required this.onStorageBlocked,
    required this.authorityBinding,
    required this.onDurable,
    this.maxAge = const Duration(days: 7),
    this.maxAttempts = 8,
    this.leaseDuration = const Duration(minutes: 2),
    this.retryDelay = const Duration(seconds: 1),
  });

  /// Serialized transactional storage; no network calls occur inside its transactions.
  final OfflineStore store;

  /// Stable local scope; it is not a wire tenant or an authorization grant.
  final String partitionId;

  /// Captured account/store generation fencing this lifetime.
  final int generation;

  /// Receipt preparation/status/continuity port; never used by admission.
  final OfflineReceiptRemote remote;

  /// One-attempt exact mutation port with post-token and publication fences.
  final OfflineBoundMutationRemote mutationRemote;

  /// Injected wall clock, clamped against persisted observation high-water.
  final OfflineClock clock;

  /// Synchronous owner/account/authority check around every awaited boundary.
  final void Function() fence;

  /// Receives eligible live handling before attempting durable confirmation.
  final void Function(OfflineOutboxRecord, OfflineWriteStatus) onHandling;

  /// Reports failed persistence and holds all further dispatch in this lifetime.
  final void Function() onStorageBlocked;

  /// Nonsecret authority generation captured for this account scope.
  final String authorityBinding;

  /// Drops process-local handling after the exact durable confirmation commits.
  final void Function(String) onDurable;

  /// Absolute operation age, lease lifetime and bounded retry-delay settings.
  final Duration maxAge, leaseDuration, retryDelay;

  /// Maximum persisted possibly-dispatched attempts per item.
  final int maxAttempts;
  bool _blocked = false, _running = false;
  final LanternCancellationToken _cancellation = LanternCancellationToken();
  Future<void>? _flight;

  /// Storage failure is monotone for this lifetime; reopen must reconcile evidence.
  bool get storageBlocked => _blocked;

  /// Performs one finite owner pass. Repeated orchestration belongs to #1769.
  Future<int> dispatchPass({int limit = 16}) async {
    if (limit < 1 || limit > 64) throw const OfflineArgumentException();
    if (_blocked) return 0;
    if (_running) throw const OfflineArgumentException();
    _running = true;
    final done = Completer<void>();
    _flight = done.future;
    try {
      fence();
      final owner = OperationId.generate(issuedAt: clock()).encoded;
      final claimed = await _transaction((tx) async {
        fence();
        if (await tx.generation(partitionId) != generation) {
          throw const LanternDataException(DataFailure.closed);
        }
        final now = await tx.effectiveTime(partitionId, clock().toUtc());
        final reconciliation = <OfflineOutboxRecord>[];
        // Expire never-sent work locally. Possible sends retain receipt evidence.
        for (final r in await tx.dueOutbox(
          partitionId,
          now: now,
          maxAge: maxAge,
          deadLetterRetention: const Duration(days: 7),
          limit: limit,
        )) {
          if (r.state != OfflineOutboxState.enqueued) continue;
          if (!r.mayHaveDispatched) {
            await advanceOfflineMutation(
              tx,
              r,
              OfflineWriteState.expired,
              now,
              diagnostic: 'expired',
            );
            await tx.deleteOutbox(partitionId, r.recordId);
          } else {
            final fifo = await tx.outboxForKey(partitionId, r.intent.key);
            final first = fifo
                .where(
                  (v) =>
                      v.state == OfflineOutboxState.enqueued ||
                      v.state == OfflineOutboxState.sending,
                )
                .first;
            if (first.recordId == r.recordId) {
              final current = r.copyWith(
                state: OfflineOutboxState.sending,
                leaseOwner: owner,
                leaseUntil: now.add(leaseDuration),
                clearNextAttemptAt: true,
                clearDiagnosticCode: true,
              );
              await tx.updateOutbox(current);
              reconciliation.add(current);
            }
          }
        }
        final ready = reconciliation.length < limit
            ? await tx.claim(
                partitionId,
                owner: owner,
                now: now,
                maxAge: maxAge,
                leaseDuration: leaseDuration,
                limit: limit - reconciliation.length,
              )
            : <OfflineOutboxRecord>[];
        final records = [...reconciliation, ...ready];
        // Claims and expired lease recovery must match aggregate state atomically.
        for (final r in await tx.outbox(partitionId)) {
          final op = await tx.getOperation(partitionId, r.operationId);
          if (op == null) throw const OfflineDurableGraphException();
          if (r.state == OfflineOutboxState.sending) {
            await advanceOfflineMutation(tx, r, OfflineWriteState.sending, now);
          } else if (r.state == OfflineOutboxState.enqueued &&
              op.items[r.itemIndex].state == OfflineWriteState.sending) {
            await advanceOfflineMutation(
              tx,
              r,
              OfflineWriteState.locallyCommitted,
              now,
            );
          }
        }
        fence();
        return records;
      });
      var handled = 0;
      for (final r in claimed) {
        if (_blocked) break;
        fence();
        if (await _deliver(r, owner)) handled++;
      }
      return handled;
    } finally {
      _running = false;
      done.complete();
      _flight = null;
    }
  }

  /// Cancels and joins the active pass; immutable pre-send evidence is retained.
  Future<void> close() async {
    _cancellation.cancel();
    await _flight;
  }

  Future<T> _transaction<T>(
    Future<T> Function(OfflineStoreTransaction) action,
  ) async {
    try {
      return await store.transaction(action);
    } on LanternDataException {
      rethrow;
    } on OfflineCanceledException {
      rethrow;
    } on Object {
      _blocked = true;
      onStorageBlocked();
      rethrow;
    }
  }

  Future<bool> _deliver(OfflineOutboxRecord r, String owner) async {
    OfflineClaimFence? claimFence;
    try {
      if (!r.mayHaveDispatched &&
          r.intent.expiration != null &&
          !clock().toUtc().isBefore(r.intent.expiration!)) {
        await _terminal(r, owner, OfflineWriteState.expired, 'expired');
        return false;
      }
      final dependencies = await _transaction((tx) async {
        final current = await _claim(tx, r, owner);
        if (tx is! OfflineStoreClaimTransaction) {
          throw const OfflineUnsupportedOperationException();
        }
        claimFence = await (tx as OfflineStoreClaimTransaction)
            .captureClaimFence(current, authorityBinding: authorityBinding);
        var pending = false;
        for (final d in r.dependencies) {
          final op = await tx.getOperation(partitionId, d.operationId);
          if (op == null || d.itemIndex >= op.items.length) {
            throw const OfflineDurableGraphException();
          }
          final item = op.items[d.itemIndex];
          if (!offlineMutationTerminal(item.state)) {
            pending = true;
          } else if (!offlineDependencySatisfied(item, d.predicate)) {
            return -1;
          }
        }
        fence();
        return pending ? 0 : 1;
      });
      if (dependencies < 0 && !r.mayHaveDispatched) {
        await _terminal(
          r,
          owner,
          OfflineWriteState.deadLetter,
          'dependency_failed',
        );
        return false;
      }
      if (dependencies == 0 && !r.mayHaveDispatched) {
        await _retry(r, owner, null);
        return false;
      }
      if (r.intent is OfflineAddEdgeIntent) {
        await _terminal(
          r,
          owner,
          OfflineWriteState.deadLetter,
          'unsupported_add',
        );
        return false;
      }
      if (r.preparation is OfflineAwaitingPreparation) {
        final requestedAt = clock().toUtc();
        final elapsed = Stopwatch()..start();
        final preparation = await remote.prepareReceipts(
          offlineReceiptKind(r.intent)!,
          itemCount: 1,
          cancellation: _cancellation,
        );
        fence();
        if (preparation.evidence.length != 1 ||
            preparation.mutation != offlineReceiptKind(r.intent)) {
          throw const OfflineRemoteProtocolException();
        }
        if (!preparation.evidence.single.freshFor(
          _freshServerTime(
            preparation.capability.serverNow,
            requestedAt,
            elapsed,
            clock(),
          ),
        )) {
          await _retry(r, owner, 'preparation_stale');
          return false;
        }
        r = await _transaction((tx) async {
          final current = await _claim(tx, r, owner);
          final updated = current.copyWith(
            preparation: OfflinePrepared(preparation.evidence.single),
          );
          await tx.updateOutbox(updated);
          fence();
          return updated;
        });
        fence();
      }
      OfflineReceiptCapabilityEnabled? capability;
      Stopwatch? age;
      DateTime? capabilityRequestedAt;
      if (r.receipt != null) {
        final evidence = r.receipt!;
        final status = await remote.getReceiptStatus(
          evidence.operationId,
          cancellation: _cancellation,
        );
        fence();
        if (status.operationId != evidence.operationId) {
          throw const OfflineRemoteProtocolException();
        }
        if (status.state == ReceiptStatusState.effectUndisclosed) {
          await _handled(r, owner, null, claimFence!);
          return true;
        }
        if (status.state == ReceiptStatusState.confirmed) {
          if (status.groupId != evidence.groupId ||
              status.mutation != evidence.mutation ||
              status.itemIndex != 0 ||
              status.itemCount != 1) {
            throw const OfflineRemoteProtocolException();
          }
          await _handled(r, owner, status.result!, claimFence!);
          return true;
        }
        if (status.state == ReceiptStatusState.noLongerProvable) {
          await _terminal(
            r,
            owner,
            OfflineWriteState.outcomeUnknown,
            'receipt_no_longer_provable',
          );
          return false;
        }
        capabilityRequestedAt = clock().toUtc();
        age = Stopwatch()..start();
        final observed = await remote.getReceiptCapability(
          cancellation: _cancellation,
        );
        fence();
        if (observed is! OfflineReceiptCapabilityEnabled ||
            !_sameCapability(evidence, observed)) {
          await _terminal(
            r,
            owner,
            r.mayHaveDispatched
                ? OfflineWriteState.outcomeUnknown
                : OfflineWriteState.deadLetter,
            'receipt_continuity',
          );
          return false;
        }
        capability = observed;
        if (!evidence.freshFor(
          _freshServerTime(
            capability.serverNow,
            capabilityRequestedAt,
            age,
            clock(),
          ),
        )) {
          if (r.mayHaveDispatched) {
            await _terminal(
              r,
              owner,
              OfflineWriteState.outcomeUnknown,
              'receipt_stale',
            );
            return false;
          }
          // Only a live never-dispatched claim may reprepare its provisional IDs.
          final refresh = await remote.prepareReceipts(
            evidence.mutation,
            itemCount: 1,
            cancellation: _cancellation,
          );
          fence();
          if (!_sameCapability(evidence, refresh.capability) ||
              refresh.evidence.length != 1) {
            throw const OfflineRemoteProtocolException();
          }
          r = await _transaction((tx) async {
            final current = await _claim(tx, r, owner);
            final updated = current.copyWith(receipt: refresh.evidence.single);
            await tx.updateOutbox(updated);
            fence();
            return updated;
          });
          // Status-first remains mandatory after refreshed evidence commits.
          await _retry(r, owner, null);
          return false;
        }
      }
      final now = await store.transaction(
        (tx) => tx.effectiveTime(partitionId, clock().toUtc()),
      );
      if ((r.intent.expiration != null &&
              !now.isBefore(r.intent.expiration!)) ||
          now.difference(r.enqueuedAt) >= maxAge ||
          r.attemptCount >= maxAttempts) {
        await _terminal(
          r,
          owner,
          r.mayHaveDispatched
              ? OfflineWriteState.outcomeUnknown
              : OfflineWriteState.expired,
          'expired',
        );
        return false;
      }
      r = await _transaction((tx) async {
        final current = await _claim(tx, r, owner);
        final updated = current.copyWith(
          mayHaveDispatched: true,
          attemptCount: current.attemptCount + 1,
          receipt: current.receipt?.copyWith(
            mayHaveDispatched: true,
            state: OfflineReceiptReconciliationState.notYetObserved,
          ),
        );
        await tx.updateOutbox(updated);
        await tx.invalidateMutationCache(partitionId, current.intent.key);
        await advanceOfflineMutation(
          tx,
          updated,
          OfflineWriteState.sending,
          await tx.effectiveTime(partitionId, clock().toUtc()),
        );
        fence();
        return updated;
      });
      fence();
      final reply = await mutationRemote.sendBoundMutation(
        r,
        cancellation: _cancellation,
        beforeDispatch: () {
          fence();
          final sendAt = claimFence!.checkCurrent(clock().toUtc());
          if (r.intent.expiration != null &&
              !sendAt.isBefore(r.intent.expiration!)) {
            throw const LanternDataException(DataFailure.expiredBeforeDispatch);
          }
          if (capability != null &&
              !r.receipt!.freshFor(
                _freshServerTime(
                  capability.serverNow,
                  capabilityRequestedAt!,
                  age!,
                  clock(),
                ),
              )) {
            throw const OfflineRemoteProtocolException();
          }
        },
        beforePublication: () {
          fence();
          claimFence!.checkCurrent(clock().toUtc());
        },
      );
      fence();
      await _handled(
        r,
        owner,
        reply is MutationKnownEffect<Object?>
            ? offlineResult(r.intent, reply)
            : null,
        claimFence!,
      );
      return true;
    } on IndexedMutationAttemptException catch (e) {
      if (e.cause is OfflineClaimLostException) {
        fence();
        return false;
      }
      // The durable pre-send marker is conservative even when the adapter proves this attempt unsent.
      if (e.cause is LanternUnauthenticatedException) {
        await _retry(r, owner, 'unauthenticated');
      } else if (e.cause is LanternPermissionDeniedException) {
        await _terminal(
          r,
          owner,
          r.mayHaveDispatched
              ? OfflineWriteState.outcomeUnknown
              : OfflineWriteState.deadLetter,
          'permission_denied',
        );
      } else if (e.cause is LanternInvalidArgumentException ||
          e.cause is LanternFailedPreconditionException ||
          e.cause is LanternNotFoundException) {
        await _terminal(
          r,
          owner,
          r.mayHaveDispatched
              ? OfflineWriteState.outcomeUnknown
              : OfflineWriteState.deadLetter,
          'rejected',
        );
      } else {
        await _retry(r, owner, 'unavailable');
      }
      return false;
    } on OfflineRemoteFailure catch (e) {
      if (e.kind == OfflineRemoteErrorKind.unauthenticated) {
        await _retry(r, owner, 'unauthenticated');
      } else if (e.kind == OfflineRemoteErrorKind.invalidArgument ||
          e.kind == OfflineRemoteErrorKind.permanent) {
        await _terminal(
          r,
          owner,
          r.mayHaveDispatched
              ? OfflineWriteState.outcomeUnknown
              : OfflineWriteState.deadLetter,
          'permission_denied',
        );
      } else {
        await _retry(r, owner, 'unavailable');
      }
      return false;
    } on OfflineReceiptCapabilityException {
      await _terminal(
        r,
        owner,
        OfflineWriteState.deadLetter,
        'receipt_unavailable',
      );
      return false;
    } on OfflineRemoteProtocolException {
      await _terminal(
        r,
        owner,
        r.mayHaveDispatched
            ? OfflineWriteState.outcomeUnknown
            : OfflineWriteState.deadLetter,
        'receipt_protocol',
      );
      return false;
    } on OfflineClaimLostException {
      fence();
      return false;
    } on LanternDataException catch (error) {
      if (error.reason == DataFailure.closed) {
        // An expired/stolen item lease discards its late callback. A changed
        // owner/account/authority still fails this synchronous lifetime fence.
        fence();
        return false;
      }
      rethrow;
    } finally {
      claimFence?.release();
    }
  }

  Future<OfflineOutboxRecord> _claim(
    OfflineStoreTransaction tx,
    OfflineOutboxRecord r,
    String owner,
  ) async {
    fence();
    final now = await tx.effectiveTime(partitionId, clock().toUtc());
    final current = await tx.getOutbox(partitionId, r.recordId);
    if (await tx.authorityBinding(partitionId) != authorityBinding) {
      throw const LanternDataException(DataFailure.closed);
    }
    if (current == null ||
        current.generation != generation ||
        await tx.generation(partitionId) != generation ||
        current.state != OfflineOutboxState.sending ||
        current.leaseOwner != owner ||
        current.leaseUntil == null ||
        !now.isBefore(current.leaseUntil!)) {
      throw const LanternDataException(DataFailure.closed);
    }
    fence();
    return current;
  }

  Future<void> _handled(
    OfflineOutboxRecord r,
    String owner,
    OfflineReceiptResult? result,
    OfflineClaimFence claimFence,
  ) async {
    // Validate against the admitted family before either live or durable publication.
    final state = result == null
        ? OfflineWriteState.acceptedUndisclosed
        : OfflineWriteState.confirmed;
    final item = OfflineWriteStatus(
      recordId: r.recordId,
      operationId: r.operationId,
      itemIndex: r.itemIndex,
      state: state,
      attemptCount: r.attemptCount,
      mayHaveDispatched: true,
      receiptResult: result,
    );
    if (result != null) {
      offlineResult(
        r.intent,
        offlineMutationReply<Object?>(item)! as MutationKnownEffect<Object?>,
      );
    }
    fence();
    claimFence.checkCurrent(clock().toUtc());
    fence();
    onHandling(r, item);
    await _transaction((tx) async {
      var current = await _claim(tx, r, owner);
      if (!current.mayHaveDispatched) {
        current = current.copyWith(
          mayHaveDispatched: true,
          receipt: current.receipt?.copyWith(mayHaveDispatched: true),
        );
        await tx.updateOutbox(current);
      }
      await tx.invalidateMutationCache(partitionId, r.intent.key);
      await advanceOfflineMutation(
        tx,
        current,
        state,
        await tx.effectiveTime(partitionId, clock().toUtc()),
        result: result,
      );
      await tx.deleteOutbox(partitionId, r.recordId);
      fence();
    });
    fence();
    onDurable(r.operationId);
  }

  Future<void> _terminal(
    OfflineOutboxRecord r,
    String owner,
    OfflineWriteState state,
    String diagnostic,
  ) => _transaction((tx) async {
    final current = await _claim(tx, r, owner);
    final now = await tx.effectiveTime(partitionId, clock().toUtc());
    if (current.mayHaveDispatched) {
      await tx.invalidateMutationCache(partitionId, r.intent.key);
    }
    await advanceOfflineMutation(
      tx,
      current,
      state,
      now,
      diagnostic: diagnostic,
    );
    if (state == OfflineWriteState.outcomeUnknown && current.receipt != null) {
      await tx.updateOutbox(
        current.copyWith(
          state: OfflineOutboxState.deadLetter,
          deadLetteredAt: now,
          clearLeaseOwner: true,
          clearLeaseUntil: true,
          diagnosticCode: diagnostic,
          receipt: current.receipt!.copyWith(
            state: OfflineReceiptReconciliationState.noLongerProvable,
          ),
        ),
      );
    } else {
      await tx.deleteOutbox(partitionId, r.recordId);
    }
    fence();
  });
  Future<void> _retry(
    OfflineOutboxRecord r,
    String owner,
    String? diagnostic,
  ) => _transaction((tx) async {
    final current = await _claim(tx, r, owner);
    final now = await tx.effectiveTime(partitionId, clock().toUtc());
    final auth = diagnostic == 'unauthenticated';
    final updated = current.copyWith(
      state: OfflineOutboxState.enqueued,
      nextAttemptAt: auth ? null : now.add(retryDelay),
      clearNextAttemptAt: auth,
      clearLeaseOwner: true,
      clearLeaseUntil: true,
      diagnosticCode: diagnostic,
      clearDiagnosticCode: diagnostic == null,
    );
    await tx.updateOutbox(updated);
    if (auth) await tx.setReplayPausedForAuth(partitionId, true);
    await advanceOfflineMutation(
      tx,
      updated,
      auth ? OfflineWriteState.pausedForAuth : OfflineWriteState.retryScheduled,
      now,
      diagnostic: diagnostic,
    );
    fence();
  });
}

bool _sameCapability(
  OfflineReceiptEvidence e,
  OfflineReceiptCapabilityEnabled c,
) =>
    e.endpoint == c.endpoint &&
    e.policy.deploymentEpoch == c.policy.deploymentEpoch &&
    e.policy.retention == c.policy.retention &&
    e.policy.maxEntries == c.policy.maxEntries &&
    e.policy.maxBytes == c.policy.maxBytes &&
    c.supports(e.mutation) &&
    _bytes(e.policy.fingerprint, c.policy.fingerprint);
bool _bytes(List<int> a, List<int> b) =>
    a.length == b.length &&
    [for (var i = 0; i < a.length; i++) a[i] == b[i]].every((v) => v);

DateTime _freshServerTime(
  DateTime serverNow,
  DateTime requestedAt,
  Stopwatch elapsed,
  DateTime observedAt,
) {
  final wall = observedAt.toUtc().difference(requestedAt);
  return serverNow.add(wall > elapsed.elapsed ? wall : elapsed.elapsed);
}
