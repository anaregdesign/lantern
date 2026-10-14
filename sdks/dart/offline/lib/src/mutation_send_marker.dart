import 'mutation_state.dart';
import 'store.dart';
import 'types.dart';

/// Commits conservative dispatch proof for receipt-less legacy Put attempts too.
/// A legacy sender cannot leave a v5 record claiming a possible send was unsent.
Future<OfflineOutboxRecord?> markOfflineReceiptlessDispatch({
  required OfflineStore store,
  required OfflineOutboxRecord claimed,
  required String owner,
  required OfflineClock clock,
  required void Function() fence,
}) => store.transaction((tx) async {
  fence();
  final now = await tx.effectiveTime(claimed.partitionId, clock().toUtc());
  final current = await tx.getOutbox(claimed.partitionId, claimed.recordId);
  if (current == null ||
      current.preparation is! OfflinePreparationNotRequired ||
      current.generation != claimed.generation ||
      await tx.generation(claimed.partitionId) != claimed.generation ||
      await tx.replayPausedForAuth(claimed.partitionId) ||
      current.state != OfflineOutboxState.sending ||
      current.leaseOwner != owner ||
      current.leaseUntil == null ||
      !now.isBefore(current.leaseUntil!)) {
    return null;
  }
  final updated = current.copyWith(mayHaveDispatched: true);
  await tx.updateOutbox(updated);
  await advanceOfflineMutation(tx, updated, OfflineWriteState.sending, now);
  fence();
  return updated;
});
