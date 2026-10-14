import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';

import 'errors.dart';
import 'mutation_state.dart';
import 'store.dart';
import 'types.dart';

/// Bounded process-local observation of durable Server handling.
/// Canceling one observer never changes accepted work or dispatch proof.
final class OfflineOperationCompletion {
  /// Binds bounded live observers to one validated store/account lifetime.
  OfflineOperationCompletion({
    required this.store,
    required this.partitionId,
    required this.clock,
    required this.fence,
    required this.generation,
    required this.authorityBinding,
    this.maxWaiters = 128,
  });

  /// Serialized transactional storage; no network calls occur inside its transactions.
  final OfflineStore store;

  /// Stable local scope; it is not a wire tenant or an authorization grant.
  final String partitionId;

  /// Captured store generation and nonsecret authority for every status read.
  final int generation;

  /// Original authority; retained status never transfers to another lifetime.
  final String authorityBinding;

  /// Injected wall clock, clamped against persisted observation high-water.
  final OfflineClock clock;

  /// Synchronous owner/account/authority check around every awaited boundary.
  final void Function() fence;

  /// Completion reservations checked before ordinary admission commits.
  final int maxWaiters;
  int _reserved = 0;
  final Map<Object, void Function()> _closeWaiters = {};
  final Map<String, Map<int, OfflineWriteStatus>> _live = {};
  final Map<String, MutationStatus<Object?>> _observerMetadata = {};
  final Map<String, int> _observerRefs = {};

  /// Seeds only a reserved observer's admitted shape, allowing a valid reply to
  /// settle even when all subsequent storage reads fail.
  void admitted(OperationId id, int length) {
    _observerMetadata[id.encoded] = MutationStatus(
      operationId: id,
      retention: MutationRetention.retained,
      items: [
        for (var i = 0; i < length; i++)
          MutationItemStatus(
            index: i,
            state: MutationItemState.stillPending,
            dispatchCertainty: MutationDispatchCertainty.neverDispatched,
          ),
      ],
    );
  }

  final LanternObservationCoordinator _watches =
      LanternObservationCoordinator();

  /// Reserves waiter capacity before ordinary admission commits.
  void reserve() {
    fence();
    if (_reserved >= maxWaiters) {
      throw const LanternDataException(DataFailure.capacity);
    }
    _reserved++;
  }

  /// Releases a reservation when admission rolls back.
  void release() => _reserved--;

  /// A fenced live reply may settle even when its durable result commit fails.
  void handling(OfflineOutboxRecord r, OfflineWriteStatus item) {
    fence();
    (_live[r.operationId] ??= {})[r.itemIndex] = item;
    final base = _observerMetadata[r.operationId];
    if (base != null) {
      final items = List<MutationItemStatus<Object?>>.of(base.items);
      items[r.itemIndex] = MutationItemStatus(
        index: r.itemIndex,
        state: MutationItemState.handled,
        reply: offlineMutationReply<Object?>(item),
        dispatchCertainty: MutationDispatchCertainty.mayHaveDispatched,
      );
      _observerMetadata[r.operationId] = MutationStatus(
        operationId: base.operationId,
        retention: base.retention,
        items: items,
        terminal: items.every((i) => i.state != MutationItemState.stillPending),
      );
    }
    _watches.invalidate();
  }

  /// Drops ephemeral handling after its durable result commits.
  void durable(String operationId) => _live.remove(operationId);

  /// Status changes coalesce; paused subscribers have bounded mailboxes.
  void invalidate() => _watches.invalidate();

  /// Reads typed retained status without tokens, capability preparation or mutation sends.
  Future<MutationStatus<T>> status<T>(OperationId id) async {
    fence();
    OfflineOperationRecord? op;
    try {
      op = await store.transaction((tx) async {
        if (await tx.generation(partitionId) != generation ||
            await tx.authorityBinding(partitionId) != authorityBinding) {
          throw const LanternDataException(DataFailure.closed);
        }
        return tx.getOperation(partitionId, id.encoded);
      });
    } on LanternDataException {
      rethrow;
    } catch (_) {
      fence();
      final eligible = _observerMetadata[id.encoded];
      if (eligible != null && _live[id.encoded] != null) {
        return _typedStatus<T>(eligible);
      }
      throw LanternDataException(DataFailure.storageBlocked, reference: id);
    }
    fence();
    if (op == null) {
      return MutationStatus(
        retention:
            clock().toUtc().isBefore(
              const OperationIdPolicy().retentionDeadline(id),
            )
            ? MutationRetention.unknown
            : MutationRetention.evicted,
        operationId: id,
      );
    }
    final kind = op.kind;
    if (kind != null) {
      final Object sample = switch (kind) {
        MutationKind.putVertices ||
        MutationKind.putVerticesIfAbsent ||
        MutationKind.putEdges => PutOutcome.appliedAndLive,
        MutationKind.addEdges => 0.0,
        _ => false,
      };
      if (sample is! T) {
        throw const LanternDataException(DataFailure.typeMismatch);
      }
    }
    final items = <MutationItemStatus<T>>[];
    for (final durable in op.items) {
      final item = _live[id.encoded]?[durable.itemIndex] ?? durable;
      final reply = offlineMutationReply<T>(item);
      final certainty = item.mayHaveDispatched
          ? MutationDispatchCertainty.mayHaveDispatched
          : MutationDispatchCertainty.neverDispatched;
      final terminal = offlineMutationTerminal(item.state);
      final reason =
          item.state == OfflineWriteState.confirmed &&
              item.receiptResult == null
          ? DataFailure.resultUnavailable
          : item.diagnosticCode == 'dependency_failed'
          ? DataFailure.dependencyFailed
          : item.state == OfflineWriteState.expired
          ? DataFailure.expiredBeforeDispatch
          : item.state == OfflineWriteState.outcomeUnknown
          ? DataFailure.outcomeUnknown
          : DataFailure.rejected;
      items.add(
        MutationItemStatus(
          index: item.itemIndex,
          state: reply != null
              ? MutationItemState.handled
              : !terminal
              ? MutationItemState.stillPending
              : item.state == OfflineWriteState.confirmed ||
                    item.state == OfflineWriteState.outcomeUnknown
              ? MutationItemState.unknown
              : MutationItemState.failed,
          reply: reply,
          dispatchCertainty: certainty,
          failure: terminal && reply == null
              ? LanternDataException(
                  reason,
                  reference: id,
                  dispatchCertainty: certainty,
                )
              : null,
        ),
      );
    }
    final status = MutationStatus<T>(
      retention: MutationRetention.retained,
      operationId: id,
      items: items,
      terminal: items.every((i) => i.state != MutationItemState.stillPending),
    );
    if (_observerRefs.containsKey(id.encoded)) {
      _observerMetadata[id.encoded] = _typedStatus<Object?>(status);
    }
    return status;
  }

  /// Waits without token acquisition, capability preparation or resubmission.
  Future<MutationObservation<T>> observe<T>(
    MutationReference<T> ref,
    WaitOptions? options, {
    bool reserved = false,
  }) async {
    if (options?.timeout != null && options!.timeout! <= Duration.zero) {
      throw const OfflineArgumentException();
    }
    if (!reserved) reserve();
    _observerRefs.update(
      ref.operationId.encoded,
      (n) => n + 1,
      ifAbsent: () => 1,
    );
    final key = Object();
    final done = Completer<MutationObservation<T>>();
    StreamSubscription<OfflineStoreChange>? subscription;
    Timer? timeout;
    void Function()? detach;
    var loading = false, dirty = false;
    void fail(DataFailure reason) async {
      if (done.isCompleted) return;
      var certainty = MutationDispatchCertainty.mayHaveDispatched;
      try {
        final latest = await status<Object?>(ref.operationId);
        if (latest.retention == MutationRetention.retained &&
            latest.items.every(
              (i) =>
                  i.dispatchCertainty ==
                  MutationDispatchCertainty.neverDispatched,
            )) {
          certainty = MutationDispatchCertainty.neverDispatched;
        }
      } catch (_) {}
      if (!done.isCompleted) {
        done.completeError(
          LanternDataException(
            reason,
            reference: ref.operationId,
            dispatchCertainty: certainty,
          ),
        );
      }
    }

    Future<void> load() async {
      if (loading) {
        dirty = true;
        return;
      }
      loading = true;
      try {
        do {
          dirty = false;
          final s = await status<T>(ref.operationId);
          if (done.isCompleted) return;
          if (s.retention != MutationRetention.retained) {
            done.complete(MutationObservation(retention: s.retention));
            return;
          }
          if (s.terminal) {
            if (s.items.every((i) => i.reply != null)) {
              done.complete(
                MutationObservation(
                  retention: MutationRetention.retained,
                  reply: MutationBatchReply([
                    for (final i in s.items) i.reply!,
                  ]),
                ),
              );
            } else if (s.items.any(
              (i) =>
                  i.state == MutationItemState.unknown &&
                  i.failure is LanternDataException &&
                  (i.failure! as LanternDataException).reason ==
                      DataFailure.resultUnavailable,
            )) {
              done.complete(
                const MutationObservation(retention: MutationRetention.unknown),
              );
            } else {
              done.completeError(
                MutationBatchException<T>(
                  items: s.items,
                  cause: s.items.firstWhere((i) => i.failure != null).failure!,
                  reference: ref.operationId,
                ),
              );
            }
            return;
          }
        } while (dirty && !done.isCompleted);
      } catch (e, st) {
        if (!done.isCompleted) done.completeError(e, st);
      } finally {
        loading = false;
      }
    }

    _closeWaiters[key] = () => fail(DataFailure.closed);
    try {
      subscription = store
          .changes(partitionId)
          .listen((_) => unawaited(load()), onError: done.completeError);
      // Live replies need a wakeup before/without the storage commit.
      final live = _liveNotifications.stream.listen((_) => unawaited(load()));
      detach = options?.cancellation?.listen(
        (_) => fail(DataFailure.waitCanceled),
      );
      if (options?.cancellation?.isCanceled ?? false) {
        fail(DataFailure.waitCanceled);
      }
      if (options?.timeout != null) {
        timeout = Timer(options!.timeout!, () => fail(DataFailure.waitTimeout));
      }
      unawaited(load());
      try {
        return await done.future;
      } finally {
        await live.cancel();
      }
    } finally {
      timeout?.cancel();
      detach?.call();
      await subscription?.cancel();
      _closeWaiters.remove(key);
      final count = (_observerRefs[ref.operationId.encoded] ?? 1) - 1;
      if (count == 0) {
        _observerRefs.remove(ref.operationId.encoded);
        _observerMetadata.remove(ref.operationId.encoded);
      } else {
        _observerRefs[ref.operationId.encoded] = count;
      }
      release();
    }
  }

  final StreamController<void> _liveNotifications =
      StreamController<void>.broadcast(sync: true);

  /// Publishes one coalesced wakeup after eligible live handling.
  void notifyHandling(OfflineOutboxRecord r, OfflineWriteStatus item) {
    handling(r, item);
    _liveNotifications.add(null);
  }

  /// Observes bounded status metadata, never sending.
  Stream<MutationStatus<Object?>> watch(OperationId id) => _watches.observe(
    id.encoded,
    initial: (lost) => MutationStatus(
      retention: MutationRetention.unknown,
      operationId: id,
      observationLost: lost,
    ),
    load: (_) => status<Object?>(id),
    markLost: (s, lost) => MutationStatus(
      retention: s.retention,
      operationId: id,
      items: s.items,
      terminal: s.terminal,
      observationLost: lost,
    ),
  );

  /// Settles this lifetime's process-local observers and releases bounded registrations.
  Future<void> close() async {
    for (final close in _closeWaiters.values.toList()) {
      close();
    }
    await _watches.close();
    await _liveNotifications.close();
    _live.clear();
  }
}

MutationStatus<T> _typedStatus<T>(MutationStatus<Object?> status) =>
    MutationStatus<T>(
      retention: status.retention,
      operationId: status.operationId,
      terminal: status.terminal,
      items: [
        for (final item in status.items)
          MutationItemStatus<T>(
            index: item.index,
            state: item.state,
            failure: item.failure,
            dispatchCertainty: item.dispatchCertainty,
            reply: switch (item.reply) {
              MutationKnownEffect<Object?>(:final effect) when effect is T =>
                MutationKnownEffect<T>(effect),
              MutationAcceptedUndisclosed<Object?>() =>
                MutationAcceptedUndisclosed<T>(),
              null => null,
              _ => throw const LanternDataException(DataFailure.typeMismatch),
            },
          ),
      ],
    );
