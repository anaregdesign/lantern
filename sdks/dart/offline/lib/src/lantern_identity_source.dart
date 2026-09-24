import 'dart:async';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'identity_consumer.dart';
import 'remote.dart';
import 'types.dart';

final _responderPattern = RegExp(r'^[0-9a-f]{32}$');

/// Bridges the official online identity stream to the offline CDC consumer.
///
/// [client] must connect to one real responder for the lifetime of each
/// session. In particular, an ordinary load-balancing URL does not satisfy
/// this contract: a checkpoint and a subsequent plural read could reach
/// different replicas. The application owns endpoint selection, client
/// lifetime, credentials, and foreground scheduling. The adapter checks the
/// responder's node ID around every plural read, but the current Subscribe
/// wire frame does not carry that ID and cannot prove a load balancer's stream
/// routing. Never use the adapter to infer stickiness from the endpoint URL.
///
/// The client acquires its token at each RPC. No credentials or CDC payloads
/// are retained by this source.
final class LanternClientIdentitySource implements OfflineIdentitySource {
  /// Wraps a client whose route is pinned to one real responder.
  const LanternClientIdentitySource(this.client);

  /// Application-owned, responder-pinned client.
  final LanternClient client;

  @override
  Future<OfflineIdentitySession> open({
    required bool bootstrap,
    required Map<String, BigInt> nextExpected,
    required LanternCancellationToken cancellation,
  }) async {
    if (cancellation.isCanceled) throw const OfflineCanceledException();
    if (bootstrap && nextExpected.isNotEmpty) {
      throw const OfflineArgumentException();
    }
    final IdentityNextCursor cursor;
    try {
      cursor = IdentityNextCursor(nextExpected);
    } on LanternInvalidArgumentException {
      throw const OfflineArgumentException();
    }
    final responder = await _readResponder(client, cancellation);
    if (cancellation.isCanceled) throw const OfflineCanceledException();
    return _LanternIdentitySession(
      client: client,
      responderId: responder,
      bootstrap: bootstrap,
      cursor: cursor,
      cancellation: cancellation,
    );
  }
}

Future<String> _readResponder(
  LanternClient client,
  LanternCancellationToken cancellation,
) async {
  if (cancellation.isCanceled) throw const OfflineCanceledException();
  final String responder;
  try {
    responder = (await client.getReplicationStatus(
      options: LanternCallOptions(cancellation: cancellation, retry: false),
    )).nodeId;
  } catch (error) {
    throw _mapIdentityFailure(error, cancellation);
  }
  if (cancellation.isCanceled) throw const OfflineCanceledException();
  if (!_responderPattern.hasMatch(responder) ||
      responder == '00000000000000000000000000000000') {
    throw const OfflineChangeGapException();
  }
  return responder;
}

final class _LanternIdentitySession implements OfflineIdentitySession {
  _LanternIdentitySession({
    required this.client,
    required this.responderId,
    required this.bootstrap,
    required this.cursor,
    required LanternCancellationToken cancellation,
  }) : _reads = LanternClientOfflineRemote(client) {
    _removeCallerCancellation = cancellation.listen((_) => _cancel.cancel());
    _cancel.listen((_) {
      final upstream = _upstream;
      _upstream = null;
      upstream?.cancel().ignore();
      final controller = _controller;
      if (controller != null && !controller.isClosed) {
        unawaited(controller.close());
      }
    });
    _events = _openEvents();
  }

  final LanternClient client;
  @override
  final String responderId;
  final bool bootstrap;
  final IdentityNextCursor cursor;
  final LanternClientOfflineRemote _reads;
  final LanternCancellationToken _cancel = LanternCancellationToken();
  late final void Function() _removeCallerCancellation;
  late final Stream<OfflineIdentityEvent> _events;
  StreamSubscription<IdentityFrame>? _upstream;
  StreamController<OfflineIdentityEvent>? _controller;
  bool _closed = false;

  @override
  Stream<OfflineIdentityEvent> get events => _events;

  Stream<OfflineIdentityEvent> _openEvents() {
    late final StreamController<OfflineIdentityEvent> controller;
    controller = StreamController<OfflineIdentityEvent>(
      sync: true,
      onListen: () {
        if (_closed || _cancel.isCanceled) {
          unawaited(controller.close());
          return;
        }
        _controller = controller;
        try {
          _upstream = client
              .subscribeIdentity(
                bootstrap: bootstrap,
                cursor: bootstrap ? null : cursor,
                options: LanternCallOptions(cancellation: _cancel),
              )
              .listen(
                (frame) {
                  if (_closed) return;
                  try {
                    controller.add(_convertFrame(frame));
                  } catch (error, stack) {
                    _fail(controller, const OfflineChangeGapException(), stack);
                  }
                },
                onError: (Object error, StackTrace stack) {
                  _fail(controller, _mapIdentityFailure(error, _cancel), stack);
                },
                onDone: () {
                  if (_closed) return;
                  if (_cancel.isCanceled) {
                    unawaited(controller.close());
                  } else {
                    _fail(
                      controller,
                      const OfflineChangeGapException(),
                      StackTrace.current,
                    );
                  }
                },
              );
        } catch (error, stack) {
          _fail(controller, _mapIdentityFailure(error, _cancel), stack);
        }
      },
      onPause: () => _upstream?.pause(),
      onResume: () => _upstream?.resume(),
      onCancel: () {
        _upstream?.cancel().ignore();
        _upstream = null;
      },
    );
    return controller.stream;
  }

  void _fail(
    StreamController<OfflineIdentityEvent> controller,
    Object error,
    StackTrace stack,
  ) {
    if (_closed || controller.isClosed) return;
    controller.addError(error, stack);
    _upstream?.cancel().ignore();
    _upstream = null;
    unawaited(controller.close());
  }

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) => _readPinned(
    cancellation,
    () => _reads.getVertices(keys, cancellation: _cancel),
  );

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) => _readPinned(
    cancellation,
    () => _reads.getEdges(edges, cancellation: _cancel),
  );

  Future<T> _readPinned<T>(
    LanternCancellationToken? cancellation,
    Future<T> Function() read,
  ) async {
    if (cancellation?.isCanceled ?? false) {
      throw const OfflineCanceledException();
    }
    final remove = cancellation?.listen((_) => _cancel.cancel());
    try {
      _ensureActive();
      await _checkResponder();
      final result = await read();
      await _checkResponder();
      _ensureActive();
      return result;
    } on OfflineCanceledException {
      if (!_cancel.isCanceled) throw const OfflineChangeGapException();
      rethrow;
    } finally {
      remove?.call();
    }
  }

  Future<void> _checkResponder() async {
    final actual = await _readResponder(client, _cancel);
    if (actual != responderId) throw const OfflineChangeGapException();
  }

  void _ensureActive() {
    if (_closed || _cancel.isCanceled) throw const OfflineCanceledException();
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _removeCallerCancellation();
    _cancel.cancel();
  }
}

OfflineIdentityEvent _convertFrame(IdentityFrame frame) => switch (frame) {
  IdentityCheckpointFrame(:final lastSequences) => OfflineIdentityCheckpoint(
    lastSequences,
  ),
  IdentityChunkFrame(
    :final origin,
    :final sequence,
    :final operation,
    :final chunkIndex,
    :final isLast,
    :final firstItemIndex,
    :final vertexKeys,
    :final edgeKeys,
  ) =>
    OfflineIdentityChunk(
      origin: origin,
      sequence: sequence,
      operation: _mapIdentityOperation(operation),
      chunkIndex: chunkIndex,
      isLast: isLast,
      firstItemIndex: firstItemIndex,
      keys: [
        for (final key in vertexKeys) OfflineEntityKey.vertex(key),
        for (final edge in edgeKeys)
          OfflineEntityKey.edge(edge.tail, edge.head),
      ],
    ),
};

OfflineIdentityOperation _mapIdentityOperation(IdentityOperation operation) {
  if (operation == IdentityOperation.putVertex) {
    return OfflineIdentityOperation.putVertex;
  }
  if (operation == IdentityOperation.deleteVertex) {
    return OfflineIdentityOperation.deleteVertex;
  }
  if (operation == IdentityOperation.addEdge) {
    return OfflineIdentityOperation.addEdge;
  }
  if (operation == IdentityOperation.putEdge) {
    return OfflineIdentityOperation.putEdge;
  }
  if (operation == IdentityOperation.deleteEdge) {
    return OfflineIdentityOperation.deleteEdge;
  }
  // The hosted parent 0.3.0 predates receipt-only markers. Treat any newer
  // operation as a cursor gap until both packages add a typed mapping.
  throw const OfflineChangeGapException();
}

Exception _mapIdentityFailure(
  Object error,
  LanternCancellationToken cancellation,
) {
  // A remote Canceled response also ends continuity. Only this session's own
  // cancellation may leave confirmed residents untouched.
  if (error is LanternCanceledException && !cancellation.isCanceled) {
    return const OfflineChangeGapException();
  }
  if (error is LanternFailedPreconditionException ||
      error is LanternInternalException ||
      error is OfflineCodecException ||
      error is OfflineArgumentException) {
    return const OfflineChangeGapException();
  }
  return mapLanternClientFailure(error);
}
