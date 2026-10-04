part of 'client.dart';

/// An opaque, scope-bound public CDC checkpoint. Store only after applying
/// every invalidation in its frame; intermediate frames have no checkpoint.
final class ChangeCursor {
  /// Copies a nonempty checkpoint previously issued by the Server.
  ChangeCursor(List<int> bytes) : _bytes = Uint8List.fromList(bytes) {
    if (bytes.isEmpty ||
        bytes.length > 8192 ||
        bytes.any((byte) => byte < 0 || byte > 255)) {
      throw _invalidArgumentException('invalid opaque change cursor');
    }
  }
  final Uint8List _bytes;

  /// Returns a copy for application-owned durable storage.
  Uint8List toBytes() => Uint8List.fromList(_bytes);
}

/// Public invalidation projection. Value mode additionally requires explicit
/// CDC value and data-read grants; its image is the current local live value.
enum ChangeProjection {
  /// Committed resource identities only.
  identity,

  /// Identities plus authorized current local live images.
  value,
}

/// One exact committed resource identity, without mutation origin metadata.
sealed class ChangeInvalidation {
  const ChangeInvalidation();
}

/// An invalidated Vertex and optional current local image.
final class VertexInvalidation extends ChangeInvalidation {
  /// Creates a checked Vertex invalidation.
  const VertexInvalidation(this.key, this.current);

  /// Public logical key.
  final String key;

  /// Present only when value projection was requested and authorized.
  final Vertex? current;
}

/// An invalidated Edge and optional current local image.
final class EdgeInvalidation extends ChangeInvalidation {
  /// Creates a checked Edge invalidation.
  const EdgeInvalidation(this.edge, this.current);

  /// Exact public logical Edge identity.
  final EdgeRef edge;

  /// Present only when value projection was requested and authorized.
  final Edge? current;
}

/// Bounded invalidations or a periodic progress frame. Hidden-only commits
/// cause no extra frame. Bootstrap is a publication cut, not a cache snapshot.
final class ChangeFrame {
  ChangeFrame._(
    Iterable<ChangeInvalidation> invalidations,
    this.cursor,
    this.bootstrap,
  ) : invalidations = List.unmodifiable(invalidations);

  /// Apply all entries before persisting [cursor].
  final List<ChangeInvalidation> invalidations;

  /// Opaque checkpoint; absent on intermediate mutation frames.
  final ChangeCursor? cursor;

  /// The first frame of a requested bootstrap tail.
  final bool bootstrap;
}

/// Public authorized CDC operations; private replication is a separate plane.
extension LanternScopedChanges on LanternClient {
  /// Opens one subscription without automatic retry/reconnect. Exactly one of
  /// [bootstrap] or [cursor] is required. Keep a bootstrap tail open while
  /// rebuilding resident keys with ordinary authorized reads. A gap requires
  /// rebuilding; do not manufacture cursor positions. Cancel the subscription
  /// or [LanternCallOptions.cancellation] to release server work.
  ///
  /// [prefix] is literal and applies to both Edge endpoints. Client default
  /// unary timeouts are ignored; explicit call deadlines still apply.
  Stream<ChangeFrame> watchChanges({
    String prefix = '',
    ChangeProjection projection = ChangeProjection.identity,
    bool bootstrap = false,
    ChangeCursor? cursor,
    LanternCallOptions? options,
  }) {
    _ensureOpen();
    if (bootstrap == (cursor != null)) {
      throw _invalidArgumentException(
        'choose bootstrap or an opaque resume cursor',
      );
    }
    final request = $changes.WatchChangesRequest(
      prefix: prefix,
      projection: projection == ChangeProjection.identity
          ? $changes.ChangeProjection.CHANGE_PROJECTION_IDENTITY
          : $changes.ChangeProjection.CHANGE_PROJECTION_VALUE,
      bootstrap: bootstrap,
      cursor: cursor?.toBytes(),
    );
    final raw = $changes_client.LanternChangeServiceClient(_invoker.transport);
    final source = _invoker.invokeStream<$changes.WatchChangesResponse>(
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.watchChanges(
            request,
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
      options: LanternCallOptions(
        timeout: options?.timeout,
        deadline: options?.deadline,
        cancellation: options?.cancellation,
        retry: false,
        disableDefaultTimeout: true,
      ),
    );
    return _decodeScopedChanges(
      source,
      projection,
      bootstrap,
      options?.cancellation,
    );
  }
}

bool _changeHasUnknown($protobuf.GeneratedMessage message) {
  if (message.unknownFields.isNotEmpty) return true;
  for (final tag in message.info_.fieldInfo.keys) {
    final value = message.getField(tag);
    if (value is $protobuf.GeneratedMessage &&
        message.hasField(tag) &&
        _changeHasUnknown(value)) {
      return true;
    }
    if (value is Iterable) {
      for (final child in value) {
        if (child is $protobuf.GeneratedMessage && _changeHasUnknown(child)) {
          return true;
        }
      }
    }
  }
  return false;
}

ChangeFrame _decodeScopedChange(
  $changes.WatchChangesResponse raw,
  ChangeProjection projection,
) {
  if (_changeHasUnknown(raw) ||
      raw.writeToBuffer().length > 1 << 20 ||
      raw.invalidations.length > 1024 ||
      raw.cursor.length > 8192 ||
      (raw.invalidations.isEmpty && raw.cursor.isEmpty) ||
      raw.bootstrap && (raw.invalidations.isNotEmpty || raw.cursor.isEmpty)) {
    throw _internalSdkException('malformed public change frame');
  }
  final invalidations = <ChangeInvalidation>[];
  for (final item in raw.invalidations) {
    if (projection == ChangeProjection.identity &&
        item.whichCurrentImage() !=
            $changes.ChangeInvalidation_CurrentImage.notSet) {
      throw _internalSdkException('identity CDC cannot contain a value');
    }
    switch (item.whichIdentity()) {
      case $changes.ChangeInvalidation_Identity.vertexKey:
        if (item.vertexKey.isEmpty ||
            item.hasEdge() ||
            item.hasVertex() && item.vertex.key != item.vertexKey) {
          throw _internalSdkException('invalid Vertex invalidation');
        }
        invalidations.add(
          VertexInvalidation(
            item.vertexKey,
            item.hasVertex() ? _vertexFromProto(item.vertex) : null,
          ),
        );
      case $changes.ChangeInvalidation_Identity.edgeKey:
        final key = item.edgeKey;
        if (key.tail.isEmpty ||
            key.head.isEmpty ||
            item.hasVertex() ||
            item.hasEdge() &&
                (item.edge.tail != key.tail || item.edge.head != key.head)) {
          throw _internalSdkException('invalid Edge invalidation');
        }
        invalidations.add(
          EdgeInvalidation(
            EdgeRef(key.tail, key.head),
            item.hasEdge() ? _edgeFromProto(item.edge) : null,
          ),
        );
      case $changes.ChangeInvalidation_Identity.notSet:
        throw _internalSdkException('unknown public change identity');
    }
  }
  return ChangeFrame._(
    invalidations,
    raw.cursor.isEmpty ? null : ChangeCursor(raw.cursor),
    raw.bootstrap,
  );
}

Stream<ChangeFrame> _decodeScopedChanges(
  Stream<$changes.WatchChangesResponse> source,
  ChangeProjection projection,
  bool bootstrap,
  LanternCancellationToken? cancellation,
) {
  StreamSubscription<$changes.WatchChangesResponse>? upstream;
  late StreamController<ChangeFrame> controller;
  var stopped = false, paused = false, bootstrapped = false;
  void fail(Object error, StackTrace stack) {
    if (stopped) return;
    stopped = true;
    controller.addError(error, stack);
    upstream?.cancel().ignore();
    unawaited(controller.close());
  }

  controller = StreamController<ChangeFrame>(
    sync: true,
    onListen: () {
      final active = source.listen(
        (raw) {
          if (stopped) return;
          try {
            final frame = _decodeScopedChange(raw, projection);
            if (frame.bootstrap && (!bootstrap || bootstrapped) ||
                !frame.bootstrap && bootstrap && !bootstrapped) {
              throw _internalSdkException(
                'unexpected public change bootstrap order',
              );
            }
            bootstrapped = bootstrapped || frame.bootstrap;
            controller.add(frame);
          } catch (error, stack) {
            fail(error, stack);
          }
        },
        onError: (Object error, StackTrace stack) => fail(error, stack),
        onDone: () {
          if (stopped) return;
          if (cancellation?.isCanceled ?? false) {
            stopped = true;
            unawaited(controller.close());
            return;
          }
          fail(
            LanternFailedPreconditionException._(
              _ErrorData(
                transportCode: connect.Code.failedPrecondition.value,
                transportCodeName: connect.Code.failedPrecondition.name,
                message:
                    'public change stream ended; resume with the last applied cursor or rebuild after a gap',
                headers: {},
                trailers: {},
                metadata: {},
              ),
            ),
            StackTrace.current,
          );
        },
      );
      upstream = active;
      if (paused) active.pause();
      if (stopped) active.cancel().ignore();
    },
    onPause: () {
      paused = true;
      upstream?.pause();
    },
    onResume: () {
      paused = false;
      upstream?.resume();
    },
    onCancel: () {
      stopped = true;
      return upstream?.cancel();
    },
  );
  return controller.stream;
}
