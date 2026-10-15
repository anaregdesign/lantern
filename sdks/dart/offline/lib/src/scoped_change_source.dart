import 'dart:async';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';
import 'remote.dart';
import 'scoped_change_consumer.dart';
import 'scoped_change_store.dart';
import 'types.dart';

/// Storage-neutral adapter over the parent's maintained public CDC primitive.
final class LanternScopedChangeSource implements OfflineScopedChangeSource {
  /// No legacy authority label or URL-derived affinity is supplied implicitly.
  const LanternScopedChangeSource({required this.source});

  /// Fully bound, explicitly certified single-responder public source.
  final LanternChangeSource source;

  @override
  Future<OfflineScopedChangeSession> open({
    required bool bootstrap,
    required OfflineScopedChangeCursor? cursor,
    required LanternCancellationToken cancellation,
  }) async {
    try {
      return _ScopedSession(
        source.open(
          bootstrap: bootstrap,
          cursor: cursor == null ? null : ChangeCursor(cursor.toBytes()),
          cancellation: cancellation,
        ),
      );
    } on LanternChangeDiscontinuity {
      throw const OfflineChangeGapException();
    }
  }
}

final class _ScopedSession implements OfflineScopedChangeSession {
  _ScopedSession(this.session);
  final LanternChangeSession session;

  @override
  String get responderId {
    session.source.checkCurrent();
    return session.source.route.responderId;
  }

  @override
  String get authorityBinding {
    session.source.checkCurrent();
    return session.source.binding.authorityBinding;
  }

  @override
  Stream<OfflineScopedChangeFrame> get frames => session.frames
      .map(
        (frame) => OfflineScopedChangeFrame(
          bootstrap: frame.bootstrap,
          cursor: frame.cursor == null
              ? null
              : OfflineScopedChangeCursor(frame.cursor!.toBytes()),
          keys: frame.invalidations.map(
            (item) => switch (item) {
              VertexInvalidation(:final key) => OfflineEntityKey.vertex(key),
              EdgeInvalidation(:final edge) => OfflineEntityKey.edge(
                edge.tail,
                edge.head,
              ),
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
                sink.addError(
                  error is LanternChangeDiscontinuity ||
                          error is LanternFailedPreconditionException ||
                          error is LanternInternalException
                      ? const OfflineChangeGapException()
                      : mapLanternClientFailure(error),
                  stack,
                );
              },
        ),
      );

  List<OfflineRemoteRead<T>> _results<T>(List<LanternSnapshot<T>> values) => [
    for (final value in values)
      switch (value.confirmed.state) {
        ConfirmedState.present => OfflineRemotePresent(
          value.confirmed.value as T,
        ),
        ConfirmedState.missing ||
        ConfirmedState.expired => const OfflineRemoteMissing(),
        ConfirmedState.unknown => throw OfflineRemoteFailure(switch (value
            .confirmed
            .cause) {
          ObservationCause.unauthenticated =>
            OfflineRemoteErrorKind.unauthenticated,
          ObservationCause.permissionDenied => OfflineRemoteErrorKind.permanent,
          _ => OfflineRemoteErrorKind.unavailable,
        }, value.confirmed.cause ?? ObservationCause.unavailable),
      },
  ];

  @override
  Future<List<OfflineRemoteRead<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) async =>
      _results(await session.getVertices(keys, cancellation: cancellation));

  @override
  Future<List<OfflineRemoteRead<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) async =>
      _results(await session.getEdges(edges, cancellation: cancellation));

  @override
  Future<void> close() => session.close();
}
