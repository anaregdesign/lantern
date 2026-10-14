import 'dart:convert';
import 'package:lantern_client/lantern_client.dart';
import 'errors.dart';
import 'store.dart';
import 'types.dart';

/// Advances one item, preserving exact results and common admission metadata.
Future<void> advanceOfflineMutation(
  OfflineStoreTransaction tx,
  OfflineOutboxRecord record,
  OfflineWriteState state,
  DateTime now, {
  OfflineReceiptResult? result,
  String? diagnostic,
  int? attempts,
}) async {
  final op = await tx.getOperation(record.partitionId, record.operationId);
  if (op == null) throw const OfflineDurableGraphException();
  final at = now.isBefore(op.updatedAt) ? op.updatedAt : now;
  final items = List<OfflineWriteStatus>.of(op.items);
  items[record.itemIndex] = OfflineWriteStatus(
    recordId: record.recordId,
    operationId: record.operationId,
    itemIndex: record.itemIndex,
    state: state,
    attemptCount: attempts ?? record.attemptCount,
    mayHaveDispatched: record.mayHaveDispatched,
    receiptResult: result,
    diagnosticCode: diagnostic,
  );
  await tx.putOperation(
    OfflineOperationRecord(
      partitionId: op.partitionId,
      generation: op.generation,
      operationId: op.operationId,
      items: items,
      updatedAt: at,
      terminalAt: items.every((i) => offlineMutationTerminal(i.state))
          ? op.terminalAt ?? at
          : null,
      kind: op.kind,
      guardUntil: op.guardUntil,
      groupId: op.groupId,
    ),
  );
}

/// Result certainty, independent of dispatch proof.
bool offlineMutationTerminal(OfflineWriteState s) => switch (s) {
  OfflineWriteState.confirmed ||
  OfflineWriteState.acceptedUndisclosed ||
  OfflineWriteState.deadLetter ||
  OfflineWriteState.expired ||
  OfflineWriteState.outcomeUnknown => true,
  _ => false,
};

/// Exact predicate evaluation. FIFO itself only requires terminality.
bool offlineDependencySatisfied(
  OfflineWriteStatus item,
  MutationDependencyPredicate p,
) => switch (p) {
  MutationDependencyPredicate.handled =>
    item.state == OfflineWriteState.confirmed ||
        item.state == OfflineWriteState.acceptedUndisclosed,
  MutationDependencyPredicate.knownEffect =>
    item.state == OfflineWriteState.confirmed && item.receiptResult != null,
  MutationDependencyPredicate.putAppliedAndLive =>
    item.state == OfflineWriteState.confirmed &&
        switch (item.receiptResult) {
          OfflineVertexPutReceiptResult(:final outcome) ||
          OfflineEdgePutResult(
            :final outcome,
          ) => outcome == PutOutcome.appliedAndLive,
          _ => false,
        },
  MutationDependencyPredicate.deleteExisted =>
    item.state == OfflineWriteState.confirmed &&
        switch (item.receiptResult) {
          OfflineVertexDeleteReceiptResult(:final existed) ||
          OfflineEdgeDeleteReceiptResult(:final existed) ||
          OfflineEdgeContributionDeleteReceiptResult(:final existed) => existed,
          _ => false,
        },
};

/// Validates explicit and implicit FIFO arcs at the adapters' commit/open boundary.
void validateOfflineDependencies(
  List<OfflineOutboxRecord> records,
  List<OfflineOperationRecord> operations,
) {
  final ops = {for (final op in operations) op.operationId: op};
  final groups = <String, Set<String>>{};
  for (final op in operations) {
    if (op.kind != null) {
      final id = _structuredId(op.operationId);
      _structuredId(op.groupId!);
      if (op.guardUntil != const OperationIdPolicy().retentionDeadline(id) ||
          op.items.any(
            (item) =>
                item.recordId != '${op.operationId}:${item.itemIndex}' ||
                ((item.state == OfflineWriteState.confirmed ||
                        item.state == OfflineWriteState.acceptedUndisclosed) &&
                    !item.mayHaveDispatched) ||
                (item.receiptResult != null &&
                    !_resultMatches(op.kind!, item.receiptResult!)),
          )) {
        throw const OfflineDurableGraphException();
      }
    }
    if (op.groupId != null) {
      final members = groups[op.groupId!] ??= <String>{};
      members.add(op.operationId);
      if (members.length > 64) throw const OfflineDurableGraphException();
    }
  }
  final groupArcs = <String, Set<String>>{};
  final active = {for (final r in records) r.recordId: r};
  final arcs = {for (final r in records) r.recordId: <String>{}};
  final degree = {for (final r in records) r.recordId: 0};
  void arc(String from, String to) {
    if (arcs[from]!.add(to)) degree[to] = degree[to]! + 1;
  }

  final sorted = List<OfflineOutboxRecord>.of(records)
    ..sort((a, b) {
      final o = a.ordinal.compareTo(b.ordinal);
      return o != 0 ? o : a.itemIndex.compareTo(b.itemIndex);
    });
  final predecessor = <String, String>{};
  for (final r in sorted) {
    final owning = ops[r.operationId];
    if (owning?.kind != null && !_intentMatches(owning!.kind!, r.intent)) {
      throw const OfflineDurableGraphException();
    }
    if (r.state == OfflineOutboxState.deadLetter ||
        r.state == OfflineOutboxState.expired) {
      continue;
    }
    final prior = predecessor[r.intent.key.canonical];
    if (prior != null) arc(prior, r.recordId);
    predecessor[r.intent.key.canonical] = r.recordId;
    for (final d in r.dependencies) {
      final op = ops[d.operationId];
      final dependent = ops[r.operationId];
      if (op == null ||
          dependent == null ||
          op.groupId == null ||
          op.groupId != dependent.groupId ||
          d.itemIndex >= op.items.length ||
          op.generation != r.generation) {
        throw const OfflineDurableGraphException();
      }
      final declared = groupArcs[op.groupId!] ??= <String>{};
      declared.add(
        '${r.operationId}:${d.operationId}:${d.itemIndex}:${d.predicate.name}',
      );
      if (declared.length > 256) throw const OfflineDurableGraphException();
      final item = op.items[d.itemIndex];
      if (active.containsKey(item.recordId)) arc(item.recordId, r.recordId);
    }
  }
  final ready = degree.keys.where((k) => degree[k] == 0).toList();
  var seen = 0;
  while (ready.isNotEmpty) {
    final id = ready.removeLast();
    seen++;
    for (final next in arcs[id]!) {
      degree[next] = degree[next]! - 1;
      if (degree[next] == 0) ready.add(next);
    }
  }
  if (seen != records.length) throw const OfflineDurableGraphException();
}

OperationId _structuredId(String encoded) {
  try {
    final parts = encoded.split(':');
    if (parts.length != 3 || parts.first != '1') {
      throw const OfflineDurableGraphException();
    }
    final id = OperationId(
      issuedAt: DateTime.fromMicrosecondsSinceEpoch(
        int.parse(parts[1]),
        isUtc: true,
      ),
      nonce: base64Url.decode(parts[2]),
    );
    if (id.encoded != encoded) throw const OfflineDurableGraphException();
    return id;
  } catch (_) {
    throw const OfflineDurableGraphException();
  }
}

bool _intentMatches(MutationKind kind, OfflineIntent intent) => switch (kind) {
  MutationKind.putVertices => intent is OfflinePutVertexIntent,
  MutationKind.putVerticesIfAbsent => intent is OfflinePutVertexIfAbsentIntent,
  MutationKind.putEdges => intent is OfflinePutEdgeIntent,
  MutationKind.addEdges => intent is OfflineReceiptAddEdgeIntent,
  MutationKind.deleteVertices => intent is OfflineDeleteVertexIntent,
  MutationKind.deleteEdges => intent is OfflineDeleteEdgeIntent,
  MutationKind.deleteEdgeContributions =>
    intent is OfflineDeleteEdgeContributionIntent,
};

bool _resultMatches(MutationKind kind, OfflineReceiptResult result) =>
    switch (kind) {
      MutationKind.putVertices || MutationKind.putVerticesIfAbsent =>
        result is OfflineVertexPutReceiptResult,
      MutationKind.putEdges => result is OfflineEdgePutResult,
      MutationKind.addEdges => result is OfflineEdgeAddReceiptResult,
      MutationKind.deleteVertices => result is OfflineVertexDeleteReceiptResult,
      MutationKind.deleteEdges => result is OfflineEdgeDeleteReceiptResult,
      MutationKind.deleteEdgeContributions =>
        result is OfflineEdgeContributionDeleteReceiptResult,
    };

/// Converts historical handling without reinterpreting source TTL.
MutationReply<T>? offlineMutationReply<T>(OfflineWriteStatus item) {
  if (item.state == OfflineWriteState.acceptedUndisclosed) {
    return MutationAcceptedUndisclosed<T>();
  }
  if (item.state != OfflineWriteState.confirmed || item.receiptResult == null) {
    return null;
  }
  final Object value = switch (item.receiptResult!) {
    OfflineVertexPutReceiptResult(:final outcome) ||
    OfflineEdgePutResult(:final outcome) => outcome,
    OfflineVertexDeleteReceiptResult(:final existed) ||
    OfflineEdgeDeleteReceiptResult(:final existed) ||
    OfflineEdgeContributionDeleteReceiptResult(:final existed) => existed,
    OfflineEdgeAddReceiptResult(:final effectiveWeight) => effectiveWeight,
  };
  if (value is! T) throw const LanternDataException(DataFailure.typeMismatch);
  return MutationKnownEffect<T>(value as T);
}
