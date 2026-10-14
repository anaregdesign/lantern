import 'package:lantern_client/lantern_client.dart';
import 'types.dart';

/// Folds the complete ordered sequence above independent confirmed evidence.
LanternSnapshot<T> projectOfflinePending<T>(
  ConfirmedEvidence<T> confirmed,
  List<OfflineOutboxRecord> records,
  DateTime now, {
  int sequence = 0,
  bool serverOnly = false,
}) {
  T? view = confirmed.value;
  var projection = PendingProjection.none;
  var count = 0, unresolved = 0, mask = 0;
  for (final r in records) {
    if (r.state == OfflineOutboxState.deadLetter ||
        r.state == OfflineOutboxState.expired ||
        (!r.mayHaveDispatched &&
            r.intent.expiration != null &&
            !now.isBefore(r.intent.expiration!))) {
      continue;
    }
    count++;
    switch (r.intent) {
      case OfflinePutVertexIntent(:final vertex):
        view = vertex as T;
        projection = PendingProjection.exactPendingPut;
        unresolved = 0;
        mask = 0;
      case OfflinePutEdgeIntent(:final edge):
        view = edge as T;
        projection = PendingProjection.exactPendingPut;
        unresolved = 0;
        mask = 0;
      case OfflineDeleteVertexIntent() || OfflineDeleteEdgeIntent():
        view = null;
        projection = PendingProjection.pendingDelete;
        unresolved = 0;
        mask = 0;
      case OfflineAddEdgeIntent():
        // Migration quarantine is never an active speculative mutation.
        throw StateError('unquarantined legacy Add');
      case OfflineReceiptAddEdgeIntent() ||
          OfflineDeleteEdgeContributionIntent() ||
          OfflinePutVertexIfAbsentIntent():
        // A speculative prior Put cannot supply an authoritative Add/condition base.
        view = confirmed.value;
        projection = PendingProjection.unresolvedPending;
        unresolved++;
        mask |=
            1 <<
            switch (r.intent) {
              OfflineReceiptAddEdgeIntent() => MutationKind.addEdges.index,
              OfflineDeleteEdgeContributionIntent() =>
                MutationKind.deleteEdgeContributions.index,
              _ => MutationKind.putVerticesIfAbsent.index,
            };
    }
  }
  return LanternSnapshot(
    confirmed: confirmed,
    view: serverOnly ? confirmed.value : view,
    projection: serverOnly ? PendingProjection.none : projection,
    pending: PendingMetadata(
      count: count,
      unresolvedCount: unresolved,
      unresolvedKindMask: mask,
      wholeSequenceExact: unresolved == 0,
    ),
    sequence: sequence,
  );
}

/// Eligible retained evidence keeps its original time and TTL on reopen.
ConfirmedEvidence<T> offlineConfirmed<T>(
  OfflineCacheRecord? record,
  DateTime now,
) {
  if (record == null) {
    return ConfirmedEvidence(
      state: ConfirmedState.unknown,
      origin: ObservationOrigin.none,
      observedAt: now,
      freshness: ObservationFreshness.unavailable,
      cause: ObservationCause.noCache,
    );
  }
  final expired =
      record.expiration != null && !now.isBefore(record.expiration!) ||
      record.missingUntil != null && !now.isBefore(record.missingUntil!);
  if (!expired &&
      now.difference(record.validatedAt) >= const Duration(minutes: 5)) {
    return ConfirmedEvidence<T>(
      state: ConfirmedState.unknown,
      origin: ObservationOrigin.none,
      observedAt: record.validatedAt,
      freshness: ObservationFreshness.unavailable,
      cause: ObservationCause.noCache,
    );
  }
  return ConfirmedEvidence(
    state: expired
        ? ConfirmedState.expired
        : record.isMissing
        ? ConfirmedState.missing
        : ConfirmedState.present,
    value: expired || record.isMissing ? null : record.entity as T,
    origin: ObservationOrigin.retainedCache,
    observedAt: record.validatedAt,
    expiration: record.expiration,
    missingUntil: record.missingUntil,
    freshness: expired
        ? ObservationFreshness.unavailable
        : ObservationFreshness.historical,
  );
}

/// Aggregate pending metadata for typed traversal without graph splicing.
PendingMetadata offlinePendingSummary(List<OfflineOutboxRecord> records) =>
    PendingMetadata(
      count: records
          .where(
            (r) =>
                r.state != OfflineOutboxState.deadLetter &&
                r.state != OfflineOutboxState.expired,
          )
          .length,
      wholeSequenceExact: false,
    );

/// Publication-time TTL fence; freshness and observation time are never rebased.
ConfirmedEvidence<T> offlineEvidenceAt<T>(
  ConfirmedEvidence<T> evidence,
  DateTime now,
) {
  if (evidence.expiration != null && !now.isBefore(evidence.expiration!) ||
      evidence.missingUntil != null && !now.isBefore(evidence.missingUntil!)) {
    return ConfirmedEvidence<T>(
      state: ConfirmedState.expired,
      origin: evidence.origin,
      observedAt: evidence.observedAt,
      expiration: evidence.expiration,
      missingUntil: evidence.missingUntil,
      freshness: ObservationFreshness.unavailable,
    );
  }
  return evidence;
}
