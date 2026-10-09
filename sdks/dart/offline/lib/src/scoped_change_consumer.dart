import 'dart:async';
import 'dart:convert';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'remote.dart';
import 'repository.dart';
import 'scoped_change_store.dart';

/// Opens an application-owned, single-responder public identity subscription.
///
/// Map the online SDK's typed WatchChanges frames to this port. No private
/// replication status, fake origins or dynamic SDK probe is needed. A load
/// balancer URL is not responder affinity. Acquire credentials per call and
/// classify only retention/policy/malformed-frame gaps as
/// [OfflineChangeGapException], never authorization or availability failures.
abstract interface class OfflineScopedChangeSource {
  /// Opens one foreground tail with exactly one bootstrap or resume cursor.
  Future<OfflineScopedChangeSession> open({
    required bool bootstrap,
    required OfflineScopedChangeCursor? cursor,
    required LanternCancellationToken cancellation,
  });
}

/// Public identity frames and plural reads pinned to the same actual node.
abstract interface class OfflineScopedChangeSession
    implements OfflineRecoveryRemote {
  /// Application-owned route identity, stable throughout stream and reads.
  /// This label grants no permission and never substitutes for Server auth.
  String get responderId;

  /// Complete profile/cut/credential binding read by the online SDK. Update
  /// this value when credentials or scope change; the consumer rejects late
  /// frames/read completions and hides confirmed data while preserving pending
  /// mutation identities. This application-owned label grants no permission.
  String get authorityBinding;

  /// Single subscription with transport pause/resume and bounded buffering.
  Stream<OfflineScopedChangeFrame> get frames;

  /// Idempotently releases the tail, including before its first listener.
  Future<void> close();
}

/// Repository-owned consumer; applications use consumeScopedChanges.
Future<void> runOfflineScopedChangeConsumer({
  required OfflineLanternRepository repository,
  required String partitionId,
  required OfflineScopedChangeSource source,
  required LanternCancellationToken cancellation,
}) async {
  var recoveries = 0;
  while (true) {
    _checkCancellation(cancellation);
    final state = await repository.store.transaction(
      (tx) async => (
        cursor: await tx.scopedChangeCursor(partitionId),
        hasUnknown: (await tx.unknownResidents(
          partitionId,
          limit: 1,
        )).isNotEmpty,
      ),
    );
    final bootstrap = state.cursor == null || state.hasUnknown;
    OfflineScopedChangeSession? session;
    StreamIterator<OfflineScopedChangeFrame>? iterator;
    void Function()? removeCancellation;
    try {
      final opened = await source.open(
        bootstrap: bootstrap,
        cursor: bootstrap ? null : state.cursor,
        cancellation: cancellation,
      );
      session = opened;
      _checkCancellation(cancellation);
      final responder = opened.responderId;
      final authority = opened.authorityBinding;
      _checkResponder(opened, responder, authority);
      final active = StreamIterator<OfflineScopedChangeFrame>(opened.frames);
      iterator = active;
      removeCancellation = cancellation.listen((_) => active.cancel().ignore());
      if (bootstrap) {
        final first = await _next(active, cancellation);
        if (!first.bootstrap || first.cursor == null) {
          throw const OfflineChangeGapException();
        }
        _checkResponder(opened, responder, authority);
        await repository.store.transaction(
          (tx) => tx.resetScopedChangeCursor(partitionId, first.cursor),
        );
        while (true) {
          _checkCancellation(cancellation);
          final unfinished = await repository.store.transaction(
            (tx) => tx.unknownResidents(partitionId, limit: 1),
          );
          if (unfinished.isEmpty) break;
          _checkResponder(opened, responder, authority);
          final completed = await repository.revalidateResidentBatch(
            partitionId,
            recoveryRemote: opened,
            cancellation: cancellation,
            beforeCommit: () => _checkResponder(opened, responder, authority),
          );
          _checkResponder(opened, responder, authority);
          if (completed == 0) throw const OfflineChangeGapException();
        }
      }
      while (true) {
        _checkCancellation(cancellation);
        final frame = await _next(active, cancellation);
        if (frame.bootstrap) throw const OfflineChangeGapException();
        _checkResponder(opened, responder, authority);
        await repository.store.transaction(
          (tx) => tx.applyScopedChangeFrame(partitionId, frame),
        );
      }
    } catch (error, stack) {
      if (cancellation.isCanceled) throw const OfflineCanceledException();
      // A broken tail hides confirmed data before reporting its error. Only
      // one declared gap may open a fresh bootstrap during this invocation.
      await repository.store.transaction(
        (tx) => tx.resetScopedChangeCursor(partitionId, null),
      );
      if (error is OfflineChangeGapException && recoveries++ == 0) continue;
      Error.throwWithStackTrace(error, stack);
    } finally {
      removeCancellation?.call();
      try {
        await iterator?.cancel();
      } finally {
        await session?.close();
      }
    }
  }
}

void _checkCancellation(LanternCancellationToken cancellation) {
  if (cancellation.isCanceled) throw const OfflineCanceledException();
}

void _checkResponder(
  OfflineScopedChangeSession session,
  String expected,
  String authority,
) {
  if (expected.isEmpty ||
      utf8.encode(expected).length > 512 ||
      session.responderId != expected ||
      authority.isEmpty ||
      utf8.encode(authority).length > 4096 ||
      session.authorityBinding != authority) {
    throw const OfflineChangeGapException();
  }
}

Future<OfflineScopedChangeFrame> _next(
  StreamIterator<OfflineScopedChangeFrame> iterator,
  LanternCancellationToken cancellation,
) async {
  _checkCancellation(cancellation);
  if (!await iterator.moveNext()) {
    _checkCancellation(cancellation);
    throw const OfflineChangeGapException();
  }
  _checkCancellation(cancellation);
  return iterator.current;
}
