part of 'client.dart';

/// A known authority, route or public-frame discontinuity requires bootstrap.
final class LanternChangeDiscontinuity implements Exception {
  /// Carries no credentials, cursor contents or transport details.
  const LanternChangeDiscontinuity();
}

/// Application-certified affinity, not a claim inferred from a URL or label.
///
/// Certify only a direct node or a route whose deployment guarantees that this
/// exact client, its stream and its reads reach one actual responder. A normal
/// load balancer does not supply that guarantee. [isCurrent] must become false
/// before replacing the route or its responder. This grants no Server authority.
final class LanternSingleResponderRoute {
  /// Explicitly certifies one immutable transport/responder lifetime.
  LanternSingleResponderRoute.certified({
    required this.client,
    required this.responderId,
    required this.isCurrent,
  }) {
    if (responderId.isEmpty || utf8.encode(responderId).length > 512) {
      throw _invalidArgumentException('invalid certified responder');
    }
  }

  /// The same transport is used by WatchChanges and all recovery reads.
  final LanternClient client;

  /// Nonsecret deployment identity; a label alone is not affinity evidence.
  final String responderId;

  /// Synchronous deployment-owned continuity fence.
  final bool Function() isCurrent;
}

/// Maintained public identity observation and same-responder plural reads.
///
/// This primitive owns no reconnect loop, scheduler or reusable read cache.
/// Qualified adapters supply those lifetimes. Credentials remain in the client's
/// TokenProvider and are acquired per attempt.
final class LanternChangeSource {
  /// Requires a full verified binding and explicitly certified route.
  LanternChangeSource({
    required this.route,
    required this.binding,
    required this.currentBinding,
    this.prefix = '',
  }) {
    if (route.client.endpoint !=
        _normalizeEndpoint(
          binding.serviceOrigin,
          allowInsecure: binding.serviceOrigin.scheme == 'http',
        )) {
      throw _invalidArgumentException('change source endpoint mismatch');
    }
    checkCurrent();
  }

  /// Certified actual-responder lifetime shared by tail and reads.
  final LanternSingleResponderRoute route;

  /// Complete immutable account/profile/cut authority, never token-derived.
  final LanternSessionBinding binding;

  /// Application-verified current identity/authority, checked across awaits.
  final LanternSessionBinding Function() currentBinding;

  /// Literal public scope, applying to both Edge endpoints.
  final String prefix;

  /// Rejects a stale account, authority, credential lifetime or actual route.
  void checkCurrent() {
    final current = currentBinding();
    if (route.client.isClosed ||
        !route.isCurrent() ||
        current.stableIdentity != binding.stableIdentity ||
        current.authorityBinding != binding.authorityBinding ||
        current.expiresAt != null &&
            !route.client._clock().toUtc().isBefore(current.expiresAt!)) {
      throw const LanternChangeDiscontinuity();
    }
  }

  /// Opens exactly one identity tail. Reconnect belongs to the adapter runtime.
  LanternChangeSession open({
    required bool bootstrap,
    ChangeCursor? cursor,
    required LanternCancellationToken cancellation,
    void Function(LanternException?)? onEndpoint,
  }) {
    checkCurrent();
    _throwIfCanceled(cancellation);
    if (bootstrap == (cursor != null)) {
      throw _invalidArgumentException('choose bootstrap or resume cursor');
    }
    return LanternChangeSession._(
      this,
      bootstrap,
      cursor,
      cancellation,
      onEndpoint,
    );
  }
}

/// One cancellable, fully fenced public tail with bounded exact plural reads.
final class LanternChangeSession {
  LanternChangeSession._(
    this.source,
    bool bootstrap,
    ChangeCursor? cursor,
    LanternCancellationToken caller,
    void Function(LanternException?)? onEndpoint,
  ) {
    _token = LanternCancellationToken._guarded(() {
      source.checkCurrent();
      caller._guard?.call();
      _throwIfCanceled(caller);
    });
    _detach = caller.listen((_) => _token.cancel());
    _reads = LanternAdapterReads.bound(
      source.route.client,
      binding: source.binding,
      checkAuthority: _check,
      onEndpoint: onEndpoint,
    );
    frames = _ObservationStream(() {
      _check();
      if (_listened) {
        throw _invalidArgumentException('change session already listened');
      }
      _listened = true;
      late StreamController<ChangeFrame> controller;
      controller = StreamController<ChangeFrame>(
        sync: true,
        onListen: () {
          _upstream = source.route.client
              .watchChanges(
                prefix: source.prefix,
                bootstrap: bootstrap,
                cursor: cursor,
                options: LanternCallOptions(cancellation: _token, retry: false),
              )
              .listen(
                (frame) {
                  try {
                    _check();
                    for (final invalidation in frame.invalidations) {
                      final covered = switch (invalidation) {
                        VertexInvalidation(:final key, :final current) =>
                          current == null && key.startsWith(source.prefix),
                        EdgeInvalidation(:final edge, :final current) =>
                          current == null &&
                              edge.tail.startsWith(source.prefix) &&
                              edge.head.startsWith(source.prefix),
                      };
                      if (!covered) throw const LanternChangeDiscontinuity();
                    }
                    controller.add(frame);
                  } catch (error, stack) {
                    controller.addError(error, stack);
                    _token.cancel();
                  }
                },
                onError: (Object error, StackTrace stack) {
                  // Transport mapping must not erase an authority/route fence
                  // that changed while acquiring credentials or receiving data.
                  try {
                    _check();
                  } catch (fenceError, fenceStack) {
                    controller.addError(fenceError, fenceStack);
                    return;
                  }
                  controller.addError(error, stack);
                },
                onDone: () => unawaited(controller.close()),
              );
        },
        onPause: () => _upstream?.pause(),
        onResume: () => _upstream?.resume(),
        onCancel: () => _upstream?.cancel(),
      );
      return controller.stream;
    });
  }

  /// Immutable certified scope for this subscription.
  final LanternChangeSource source;

  /// Single-subscription, value-free frames; pause propagates to transport.
  late final Stream<ChangeFrame> frames;
  late final LanternCancellationToken _token;
  late final void Function() _detach;
  late final DirectLanternDataClient _reads;
  StreamSubscription<ChangeFrame>? _upstream;
  bool _closed = false, _listened = false;
  Future<void>? _closing;

  void _check() {
    if (_closed) throw const LanternDataException(DataFailure.closed);
    _token._guard?.call();
    _throwIfCanceled(_token);
  }

  Future<T> _read<T>(
    LanternCancellationToken? cancellation,
    Future<T> Function(LanternCancellationToken) call,
  ) async {
    _check();
    final token = LanternCancellationToken._guarded(() {
      _check();
      cancellation?._guard?.call();
      _throwIfCanceled(cancellation);
    });
    final detachSession = _token.listen((_) => token.cancel());
    final detachCaller = cancellation?.listen((_) => token.cancel());
    try {
      final result = await call(token);
      token._checkCurrent();
      return result;
    } finally {
      detachSession();
      detachCaller?.call();
    }
  }

  /// One bounded, request-aligned read under the same route and authority.
  Future<List<LanternSnapshot<Vertex>>> getVertices(
    List<String> keys, {
    LanternCancellationToken? cancellation,
  }) => _read(
    cancellation,
    (token) => _reads.getVertices(
      keys,
      options: GetOptions(
        source: ReadSource.serverOnly,
        timeout: const Duration(seconds: 15),
        cancellation: token,
      ),
    ),
  );

  /// One bounded, request-aligned Edge read under the same fences.
  Future<List<LanternSnapshot<Edge>>> getEdges(
    List<EdgeRef> edges, {
    LanternCancellationToken? cancellation,
  }) => _read(
    cancellation,
    (token) => _reads.getEdges(
      edges,
      options: GetOptions(
        source: ReadSource.serverOnly,
        timeout: const Duration(seconds: 15),
        cancellation: token,
      ),
    ),
  );

  /// Cancels and joins this tail and its reads, never the shared transport.
  Future<void> close() => _closing ??= (() async {
    _closed = true;
    _detach();
    _token.cancel();
    await _upstream?.cancel();
    await _reads.close();
  })();
}
