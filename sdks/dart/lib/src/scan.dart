part of 'client.dart';

/// Lexicographic order for vertex and vertex-key cursor scans.
enum ScanOrder {
  /// Lowest key first. This is the server default.
  ascending,

  /// Highest key first.
  descending,
}

/// Opaque cursor returned by one scan page and accepted by its next request.
///
/// Cursor bytes have no client-side meaning. [toBytes] returns a defensive
/// copy solely for persistence; applications must not parse or alter it.
final class ScanCursor {
  ScanCursor._(Iterable<int> bytes) : _bytes = _copyCursorBytes(bytes);

  /// Restores cursor bytes previously obtained from [toBytes].
  factory ScanCursor.fromBytes(Iterable<int> bytes) => ScanCursor._(bytes);

  final Uint8List _bytes;

  /// Returns a defensive copy suitable for durable storage.
  Uint8List toBytes() => Uint8List.fromList(_bytes);
}

/// One bounded scan page with an explicit continuation boundary.
final class Page<T> {
  /// Creates an immutable page.
  Page({required Iterable<T> items, this.nextCursor})
    : items = List<T>.unmodifiable(items);

  /// Items returned by this unary page request.
  final List<T> items;

  /// Opaque cursor for the next page, or `null` at the end of the range.
  final ScanCursor? nextCursor;

  /// Whether another page is available.
  bool get hasMore => nextCursor != null;
}

/// Cursor-paged scans and explicitly scoped prefix operations.
extension LanternScan on LanternClient {
  /// Fetches one bounded page of vertices.
  Future<Page<Vertex>> scanVertices({
    String prefix = '',
    int limit = 0,
    ScanCursor? cursor,
    ScanOrder order = ScanOrder.ascending,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    _validatePageLimit(limit);
    final request = $graph.ScanVerticesRequest(
      prefix: prefix,
      limit: limit,
      cursor: cursor?._bytes,
      order: _scanOrderToProto(order),
    );
    final response = await _invoke(
      'ScanVertices',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.scanVertices(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return Page(
      items: response.vertices.map(_vertexFromProto),
      nextCursor: _nextCursor(response.nextCursor),
    );
  }

  /// Lazily fetches vertex pages with single-page backpressure.
  Stream<Page<Vertex>> scanVerticesAll({
    String prefix = '',
    int limit = 0,
    ScanCursor? cursor,
    ScanOrder order = ScanOrder.ascending,
    LanternCallOptions? options,
  }) => _scanPageStream(
    initialCursor: cursor,
    options: options,
    fetch: (next, linkedOptions) => scanVertices(
      prefix: prefix,
      limit: limit,
      cursor: next,
      order: order,
      options: linkedOptions,
    ),
  );

  /// Fetches one keys-only page without decoding vertex values.
  Future<Page<String>> scanVertexKeys({
    required String prefix,
    int limit = 0,
    ScanCursor? cursor,
    ScanOrder order = ScanOrder.ascending,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    _requireNonEmpty(prefix, 'prefix');
    _validatePageLimit(limit);
    final request = $graph.ScanVertexKeysRequest(
      prefix: prefix,
      limit: limit,
      cursor: cursor?._bytes,
      order: _scanOrderToProto(order),
    );
    final response = await _invoke(
      'ScanVertexKeys',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.scanVertexKeys(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return Page(
      items: response.keys,
      nextCursor: _nextCursor(response.nextCursor),
    );
  }

  /// Lazily fetches keys-only pages with single-page backpressure.
  Stream<Page<String>> scanVertexKeysAll({
    required String prefix,
    int limit = 0,
    ScanCursor? cursor,
    ScanOrder order = ScanOrder.ascending,
    LanternCallOptions? options,
  }) => _scanPageStream(
    initialCursor: cursor,
    options: options,
    fetch: (next, linkedOptions) => scanVertexKeys(
      prefix: prefix,
      limit: limit,
      cursor: next,
      order: order,
      options: linkedOptions,
    ),
  );

  /// Fetches one ascending `(tail, head)` edge page.
  Future<Page<Edge>> scanEdges({
    String tailPrefix = '',
    String headPrefix = '',
    int limit = 0,
    ScanCursor? cursor,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    _validatePageLimit(limit);
    final request = $graph.ScanEdgesRequest(
      tailPrefix: tailPrefix,
      headPrefix: headPrefix,
      limit: limit,
      cursor: cursor?._bytes,
    );
    final response = await _invoke(
      'ScanEdges',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.scanEdges(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return Page(
      items: response.edges.map(_edgeFromProto),
      nextCursor: _nextCursor(response.nextCursor),
    );
  }

  /// Lazily fetches ascending edge pages with single-page backpressure.
  Stream<Page<Edge>> scanEdgesAll({
    String tailPrefix = '',
    String headPrefix = '',
    int limit = 0,
    ScanCursor? cursor,
    LanternCallOptions? options,
  }) => _scanPageStream(
    initialCursor: cursor,
    options: options,
    fetch: (next, linkedOptions) => scanEdges(
      tailPrefix: tailPrefix,
      headPrefix: headPrefix,
      limit: limit,
      cursor: next,
      options: linkedOptions,
    ),
  );

  /// Counts live vertex keys under [prefix] without page materialization.
  Future<BigInt> countVerticesByPrefix(
    String prefix, {
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final request = $graph.CountVerticesByPrefixRequest(prefix: prefix);
    final response = await _invoke(
      'CountVerticesByPrefix',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.countVerticesByPrefix(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return _uint64ToBigInt(response.count);
  }

  /// Deletes or previews at most one server-bounded vertex-prefix page.
  ///
  /// This operation is never retried and never loops destructively.
  Future<BigInt> deleteVerticesByPrefix(
    String prefix, {
    int limit = 0,
    bool dryRun = false,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    _requireNonEmpty(prefix, 'prefix');
    _validatePageLimit(limit);
    final request = $graph.DeleteVerticesByPrefixRequest(
      prefix: prefix,
      limit: limit,
      dryRun: dryRun,
    );
    final response = await _invoke(
      'DeleteVerticesByPrefix',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.deleteVerticesByPrefix(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return _uint64ToBigInt(response.deleted);
  }

  /// Deletes or previews at most one server-bounded edge-prefix page.
  ///
  /// At least one prefix is required. This operation is never retried and
  /// never loops destructively.
  Future<BigInt> deleteEdgesByPrefix({
    String tailPrefix = '',
    String headPrefix = '',
    int limit = 0,
    bool dryRun = false,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    if (tailPrefix.isEmpty && headPrefix.isEmpty) {
      throw _invalidArgumentException(
        'tailPrefix or headPrefix must be non-empty',
      );
    }
    _validatePageLimit(limit);
    final request = $graph.DeleteEdgesByPrefixRequest(
      tailPrefix: tailPrefix,
      headPrefix: headPrefix,
      limit: limit,
      dryRun: dryRun,
    );
    final response = await _invoke(
      'DeleteEdgesByPrefix',
      _freezeCallOptions(options),
      (raw, headers, signal, onHeader, onTrailer) => raw.deleteEdgesByPrefix(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    return _uint64ToBigInt(response.deleted);
  }
}

Stream<Page<T>> _scanPageStream<T>({
  required ScanCursor? initialCursor,
  required LanternCallOptions? options,
  required Future<Page<T>> Function(
    ScanCursor? cursor,
    LanternCallOptions options,
  )
  fetch,
}) {
  late StreamController<Page<T>> controller;
  final streamCancellation = LanternCancellationToken();
  final callerCancellation = options?.cancellation;
  void Function()? unlinkCaller;
  final linkedOptions = LanternCallOptions(
    timeout: options?.timeout,
    deadline: options?.deadline,
    cancellation: streamCancellation,
  );
  var canceled = false;
  var paused = false;
  Completer<void>? resumed;

  Future<void> pump() async {
    var cursor = initialCursor;
    try {
      while (!canceled) {
        while (paused && !canceled) {
          resumed ??= Completer<void>();
          await resumed!.future;
        }
        if (canceled) break;
        _throwIfCanceled(streamCancellation);
        final page = await fetch(cursor, linkedOptions);
        if (canceled) break;
        controller.add(page);
        if (!page.hasMore) break;
        cursor = page.nextCursor;
        // Let the delivered event pause or cancel its subscription before the
        // next unary page request starts.
        await Future<void>.delayed(Duration.zero);
      }
    } catch (error, stackTrace) {
      if (!canceled) controller.addError(error, stackTrace);
    } finally {
      unlinkCaller?.call();
      if (!controller.isClosed) await controller.close();
    }
  }

  controller = StreamController<Page<T>>(
    onListen: () {
      unlinkCaller = callerCancellation?.listen((reason) {
        streamCancellation.cancel(reason ?? 'caller canceled scan');
        // A caller token must also wake a stream paused between page RPCs so
        // the pump can observe cancellation without starting another request.
        paused = false;
        resumed?.complete();
        resumed = null;
      });
      pump();
    },
    onPause: () => paused = true,
    onResume: () {
      paused = false;
      resumed?.complete();
      resumed = null;
    },
    onCancel: () {
      canceled = true;
      streamCancellation.cancel('scan subscription canceled');
      resumed?.complete();
      resumed = null;
    },
  );
  return controller.stream;
}

$graph.ScanOrder _scanOrderToProto(ScanOrder order) => switch (order) {
  ScanOrder.ascending => $graph.ScanOrder.SCAN_ORDER_ASC,
  ScanOrder.descending => $graph.ScanOrder.SCAN_ORDER_DESC,
};

ScanCursor? _nextCursor(Iterable<int> bytes) {
  final copied = _copyCursorBytes(bytes);
  return copied.isEmpty ? null : ScanCursor._(copied);
}

Uint8List _copyCursorBytes(Iterable<int> bytes) {
  final list = List<int>.unmodifiable(bytes);
  if (list.any((byte) => byte < 0 || byte > 255)) {
    throw _invalidArgumentException('cursor bytes must be in [0, 255]');
  }
  return Uint8List.fromList(list);
}

void _validatePageLimit(int limit) {
  if (limit < 0 || limit > 0xffffffff) {
    throw _invalidArgumentException('limit must fit uint32');
  }
}

void _requireNonEmpty(String value, String name) {
  if (value.isEmpty) throw _invalidArgumentException('$name must be non-empty');
}

BigInt _uint64ToBigInt(Int64 value) => BigInt.parse(value.toStringUnsigned());
