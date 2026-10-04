import 'dart:async';

import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

/// Application composition for the online public CDC and hosted offline port.
///
/// The application explicitly certifies a direct, single-responder client;
/// [responderId] is a local route label and conveys no Server permission. Do
/// not use an ordinary load-balancing URL or silently replace this client
/// during a session. The online SDK owns transport/auth/deadline enforcement.
final class LanternScopedChangeSource implements OfflineScopedChangeSource {
  const LanternScopedChangeSource({
    required this.client,
    required this.responderId,
    this.prefix = '',
  });

  final LanternClient client;
  final String responderId;
  final String prefix;

  @override
  Future<OfflineScopedChangeSession> open({
    required bool bootstrap,
    required OfflineScopedChangeCursor? cursor,
    required LanternCancellationToken cancellation,
  }) async {
    if (cancellation.isCanceled) throw const OfflineCanceledException();
    if (bootstrap == (cursor != null) || responderId.isEmpty) {
      throw const OfflineArgumentException();
    }
    return _Session(
      client,
      responderId,
      prefix,
      bootstrap,
      cursor,
      cancellation,
    );
  }
}

final class _Session implements OfflineScopedChangeSession {
  _Session(
    this.client,
    this.responderId,
    String prefix,
    bool bootstrap,
    OfflineScopedChangeCursor? cursor,
    LanternCancellationToken caller,
  ) {
    _removeCancellation = caller.listen((_) => _cancel.cancel());
    late Stream<ChangeFrame> upstream;
    try {
      upstream = client.watchChanges(
        prefix: prefix,
        projection: ChangeProjection.identity,
        bootstrap: bootstrap,
        cursor: cursor == null ? null : ChangeCursor(cursor.toBytes()),
        options: LanternCallOptions(cancellation: _cancel, retry: false),
      );
    } catch (_) {
      _removeCancellation();
      _cancel.cancel();
      rethrow;
    }
    // Stream transformations preserve pause/resume; no eager subscription,
    // background reconnect, credential storage or unbounded queue is added.
    frames = upstream
        .map(
          (frame) => OfflineScopedChangeFrame(
            bootstrap: frame.bootstrap,
            cursor: frame.cursor == null
                ? null
                : OfflineScopedChangeCursor(frame.cursor!.toBytes()),
            keys: frame.invalidations.map(
              (item) => switch (item) {
                VertexInvalidation(:final key, :final current)
                    when current == null =>
                  OfflineEntityKey.vertex(key),
                EdgeInvalidation(:final edge, :final current)
                    when current == null =>
                  OfflineEntityKey.edge(edge.tail, edge.head),
                _ => throw const OfflineChangeGapException(),
              },
            ),
          ),
        )
        .transform(
          StreamTransformer.fromHandlers(
            handleError:
                (
                  Object error,
                  StackTrace stack,
                  EventSink<OfflineScopedChangeFrame> sink,
                ) {
                  final mapped =
                      (error is LanternCanceledException &&
                              !_cancel.isCanceled) ||
                          error is LanternFailedPreconditionException ||
                          error is LanternInternalException ||
                          error is OfflineArgumentException ||
                          error is OfflineCapacityException ||
                          error is OfflineChangeGapException
                      ? const OfflineChangeGapException()
                      : mapLanternClientFailure(error);
                  sink.addError(mapped, stack);
                },
          ),
        );
    _reads = LanternClientOfflineRemote(client);
  }
  final LanternClient client;
  @override
  final String responderId;
  @override
  late final Stream<OfflineScopedChangeFrame> frames;
  late final LanternClientOfflineRemote _reads;
  final _cancel = LanternCancellationToken();
  late final void Function() _removeCancellation;
  bool _closed = false;

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) {
    if (_closed) throw const OfflineCanceledException();
    return _reads.getVertices(keys, cancellation: cancellation ?? _cancel);
  }

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) {
    if (_closed) throw const OfflineCanceledException();
    return _reads.getEdges(edges, cancellation: cancellation ?? _cancel);
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    _removeCancellation();
    _cancel.cancel();
  }
}
