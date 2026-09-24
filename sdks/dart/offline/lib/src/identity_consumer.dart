import 'dart:async';
import 'dart:convert';

import 'package:lantern_client/lantern_client.dart';

import 'change_store.dart';
import 'errors.dart';
import 'remote.dart';
import 'repository.dart';
import 'types.dart';

/// An identity-only event from one explicitly selected Lantern responder.
///
/// This port deliberately does not depend on the newer online SDK's CDC API:
/// the first offline release still resolves against hosted lantern_client 0.2.0.
sealed class OfflineIdentityEvent {
  /// Creates an identity-only event.
  const OfflineIdentityEvent();
}

/// An atomic publication cut, expressed as LAST committed origin sequences.
final class OfflineIdentityCheckpoint extends OfflineIdentityEvent {
  /// Copies and validates the bounded checkpoint vector.
  OfflineIdentityCheckpoint(Map<String, BigInt> lastApplied)
    : cursor = OfflineChangeCursor(lastApplied);

  /// Exact per-origin checkpoint from the stream responder.
  final OfflineChangeCursor cursor;
}

/// The category of one explicit graph mutation.
enum OfflineIdentityOperation {
  /// Vertex Put, including born-expired overwrites.
  putVertex,

  /// Vertex Delete, including exact capped-prefix victims.
  deleteVertex,

  /// Additive Edge write.
  addEdge,

  /// Idempotent Edge replacement.
  putEdge,

  /// Edge Delete, including exact capped-prefix victims.
  deleteEdge,
}

/// One bounded, value-free fragment of a committed mutation.
final class OfflineIdentityChunk extends OfflineIdentityEvent {
  /// Validates exact identities and their operation family before admission.
  OfflineIdentityChunk({
    required this.origin,
    required this.sequence,
    required this.operation,
    required this.chunkIndex,
    required this.isLast,
    required this.firstItemIndex,
    required Iterable<OfflineEntityKey> keys,
  }) : keys = List<OfflineEntityKey>.unmodifiable(keys) {
    OfflineChangeChunk(
      origin: origin,
      sequence: sequence,
      chunkIndex: chunkIndex,
      isLast: isLast,
      keys: this.keys,
    );
    if (firstItemIndex < 0 ||
        firstItemIndex > 0x7fffffff ||
        (!isLast && this.keys.isEmpty)) {
      throw const OfflineArgumentException();
    }
    final vertexOperation =
        operation == OfflineIdentityOperation.putVertex ||
        operation == OfflineIdentityOperation.deleteVertex;
    if (this.keys.any(
      (key) =>
          key.kind !=
          (vertexOperation ? OfflineEntityKind.vertex : OfflineEntityKind.edge),
    )) {
      throw const OfflineArgumentException();
    }
  }

  /// Canonical origin ID.
  final String origin;

  /// Origin-local uint64 mutation sequence.
  final BigInt sequence;

  /// Exact mutation category, fixed across all chunks of this sequence.
  final OfflineIdentityOperation operation;

  /// Zero-based chunk position.
  final int chunkIndex;

  /// Whether this completes the mutation.
  final bool isLast;

  /// First original mutation item represented by this chunk.
  final int firstItemIndex;

  /// Exact Vertex or Edge identities without graph payloads.
  final List<OfflineEntityKey> keys;

  OfflineChangeChunk _storageChunk() => OfflineChangeChunk(
    origin: origin,
    sequence: sequence,
    chunkIndex: chunkIndex,
    isLast: isLast,
    keys: keys,
  );
}

/// Supplies a stream and plural reads pinned to the same responder.
///
/// An implementation may choose another endpoint only when opening a *new*
/// session. It must classify a retention gap or slow-subscriber overflow as
/// [OfflineChangeGapException]. It must acquire credentials at call time and
/// honor [cancellation]. Registration before the first listener and paused
/// delivery must remain bounded; this core reads only one frame at a time.
/// Local partition IDs are never sent on this port.
abstract interface class OfflineIdentitySource {
  /// Opens one explicitly foreground, non-retrying stream session.
  ///
  /// [nextExpected] is a per-origin NEXT sequence, not the durable LAST cursor.
  /// Bootstrap sends a checkpoint before any live chunk and requires an empty
  /// vector. The stream's registered tail and plural reads share one responder.
  Future<OfflineIdentitySession> open({
    required bool bootstrap,
    required Map<String, BigInt> nextExpected,
    required LanternCancellationToken cancellation,
  });
}

/// One responder-pinned stream and bounded plural-read session.
abstract interface class OfflineIdentitySession
    implements OfflineRecoveryRemote {
  /// Stable, non-secret responder identity for this session.
  ///
  /// A changed value before or after revalidation is a fail-closed gap. An
  /// adapter must not silently fail over a plural read inside this session.
  String get responderId;

  /// Single-subscription stream, with pause/resume propagated to transport.
  Stream<OfflineIdentityEvent> get events;

  /// Closes the stream and releases all responder resources, even if [events]
  /// was never listened to. This operation must be idempotent.
  Future<void> close();
}

final _maximumSequence = (BigInt.one << 64) - BigInt.one;

/// Runs one bounded foreground subscription owned by [repository].
///
/// The repository provides partition quiescence and a single active session;
/// this function does not start background work. A gap hides all confirmed
/// cache rows as durable Unknown before trying one fresh checkpoint. Another
/// gap stops, leaving Unknown residents for a later explicit invocation.
Future<void> runOfflineIdentityConsumer({
  required OfflineLanternRepository repository,
  required String partitionId,
  required OfflineIdentitySource source,
  required LanternCancellationToken cancellation,
}) async {
  var forceBootstrap = false;
  var recoveryAttempts = 0;
  while (true) {
    _checkCancellation(cancellation);
    final recovery = await repository.store.transaction(
      (transaction) async => (
        cursor: await transaction.changeCursor(partitionId),
        hasUnknownResidents: (await transaction.unknownResidents(
          partitionId,
          limit: 1,
        )).isNotEmpty,
      ),
    );
    final durable = recovery.cursor;
    // A prior checkpoint may have committed before resident revalidation was
    // interrupted. Its cursor alone cannot prove those Unknown keys fresh.
    final bootstrap =
        forceBootstrap ||
        durable.sequences.isEmpty ||
        recovery.hasUnknownResidents;
    final nextExpected = <String, BigInt>{};
    if (!bootstrap) {
      for (final entry in durable.sequences.entries) {
        if (entry.value == _maximumSequence) {
          forceBootstrap = true;
          break;
        }
        nextExpected[entry.key] = entry.value + BigInt.one;
      }
    }
    if (forceBootstrap && !bootstrap) continue;
    OfflineIdentitySession? session;
    StreamIterator<OfflineIdentityEvent>? iterator;
    void Function()? removeCancellation;
    try {
      final activeSession = await source.open(
        bootstrap: bootstrap,
        nextExpected: Map<String, BigInt>.unmodifiable(nextExpected),
        cancellation: cancellation,
      );
      session = activeSession;
      _checkCancellation(cancellation);
      final responder = activeSession.responderId;
      if (responder.isEmpty || utf8.encode(responder).length > 512) {
        throw const OfflineChangeGapException();
      }
      final activeIterator = StreamIterator<OfflineIdentityEvent>(
        activeSession.events,
      );
      iterator = activeIterator;
      removeCancellation = cancellation.listen((_) {
        activeIterator.cancel().ignore();
      });
      if (bootstrap) {
        final first = await _next(activeIterator, cancellation);
        if (first is! OfflineIdentityCheckpoint) {
          throw const OfflineChangeGapException();
        }
        await repository.store.transaction(
          (transaction) =>
              transaction.resetChangeCursor(partitionId, first.cursor),
        );
        while (true) {
          _checkCancellation(cancellation);
          final keys = await repository.store.transaction(
            (transaction) => transaction.unknownResidents(
              partitionId,
              limit: offlineMaxResidentRevalidationBatch,
            ),
          );
          if (keys.isEmpty) break;
          _checkResponder(activeSession, responder);
          final completed = await repository.revalidateResidentBatch(
            partitionId,
            recoveryRemote: activeSession,
            cancellation: cancellation,
            beforeCommit: () => _checkResponder(activeSession, responder),
          );
          _checkResponder(activeSession, responder);
          if (completed == 0) throw const OfflineChangeGapException();
        }
      }
      final cursor = bootstrap
          ? await repository.store.transaction(
              (transaction) => transaction.changeCursor(partitionId),
            )
          : durable;
      final progress = _IdentityAssembler(cursor);
      while (true) {
        _checkCancellation(cancellation);
        final frame = await _next(activeIterator, cancellation);
        if (frame is! OfflineIdentityChunk) {
          throw const OfflineChangeGapException();
        }
        _checkResponder(activeSession, responder);
        await progress.apply(repository, partitionId, frame);
      }
    } on OfflineCanceledException {
      rethrow;
    } catch (error, stack) {
      // A broken stream can no longer prove that any confirmed row is fresh.
      await repository.store.transaction(
        (transaction) => transaction.resetChangeCursor(
          partitionId,
          OfflineChangeCursor(const {}),
        ),
      );
      _checkCancellation(cancellation);
      if (error is OfflineChangeGapException && recoveryAttempts == 0) {
        recoveryAttempts = 1;
        forceBootstrap = true;
        continue;
      }
      Error.throwWithStackTrace(error, stack);
    } finally {
      removeCancellation?.call();
      try {
        await iterator?.cancel();
      } catch (_) {
        if (!cancellation.isCanceled) rethrow;
      }
      await session?.close();
    }
  }
}

Future<OfflineIdentityEvent?> _next(
  StreamIterator<OfflineIdentityEvent> iterator,
  LanternCancellationToken cancellation,
) async {
  if (!await iterator.moveNext()) {
    _checkCancellation(cancellation);
    return null;
  }
  _checkCancellation(cancellation);
  return iterator.current;
}

void _checkCancellation(LanternCancellationToken cancellation) {
  if (cancellation.isCanceled) throw const OfflineCanceledException();
}

void _checkResponder(OfflineIdentitySession session, String expected) {
  if (session.responderId != expected) {
    throw const OfflineChangeGapException();
  }
}

final class _IdentityAssembler {
  _IdentityAssembler(OfflineChangeCursor cursor)
    : _completed = Map<String, BigInt>.of(cursor.sequences);

  final Map<String, BigInt> _completed;
  final Map<String, _PendingIdentity> _pending = {};

  Future<void> apply(
    OfflineLanternRepository repository,
    String partitionId,
    OfflineIdentityChunk chunk,
  ) async {
    final completed = _completed[chunk.origin] ?? BigInt.zero;
    if (chunk.sequence <= completed) return; // fully committed replay
    if (completed == _maximumSequence ||
        chunk.sequence != completed + BigInt.one) {
      throw const OfflineChangeGapException();
    }
    final pending = _pending[chunk.origin];
    if (pending == null) {
      if (chunk.chunkIndex != 0 || chunk.firstItemIndex != 0) {
        throw const OfflineChangeGapException();
      }
    } else if (chunk.chunkIndex != pending.nextChunk ||
        chunk.firstItemIndex != pending.nextItem ||
        chunk.operation != pending.operation) {
      throw const OfflineChangeGapException();
    }
    await repository.store.transaction(
      (transaction) =>
          transaction.applyChangeChunk(partitionId, chunk._storageChunk()),
    );
    if (chunk.isLast) {
      _pending.remove(chunk.origin);
      _completed[chunk.origin] = chunk.sequence;
    } else {
      _pending[chunk.origin] = _PendingIdentity(
        chunk.operation,
        chunk.chunkIndex + 1,
        chunk.firstItemIndex + chunk.keys.length,
      );
    }
  }
}

final class _PendingIdentity {
  const _PendingIdentity(this.operation, this.nextChunk, this.nextItem);

  final OfflineIdentityOperation operation;
  final int nextChunk;
  final int nextItem;
}
