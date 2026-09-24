part of 'client.dart';

final _identityOriginPattern = RegExp(r'^[0-9a-f]{32}$');
const _maxIdentityFrameBytes = 1 << 20;
const _maxIdentityChunkItems = 1024;

/// A portable per-origin cursor whose values are the NEXT expected sequence.
///
/// This differs from a durable last-applied checkpoint. An identity stream
/// never uses the responder-local mutation-log position.
final class IdentityNextCursor {
  /// Copies and validates the next sequence expected from each origin.
  IdentityNextCursor(Map<String, BigInt> nextSequences)
    : nextSequences = Map<String, BigInt>.unmodifiable(nextSequences) {
    for (final entry in this.nextSequences.entries) {
      _validateIdentityOrigin(entry.key);
      if (entry.value < BigInt.one || entry.value > _maxUint64) {
        throw _invalidArgumentException(
          'identity cursor sequence must fit uint64 and be positive',
        );
      }
    }
  }

  /// Converts a durable last-applied vector, rejecting uint64 exhaustion.
  factory IdentityNextCursor.fromLastApplied(
    Map<String, BigInt> lastSequences,
  ) {
    for (final last in lastSequences.values) {
      if (last < BigInt.zero || last >= _maxUint64) {
        throw _invalidArgumentException(
          'last-applied identity sequence cannot advance',
        );
      }
    }
    return IdentityNextCursor({
      for (final entry in lastSequences.entries)
        entry.key: entry.value + BigInt.one,
    });
  }

  /// Immutable canonical origin IDs mapped to NEXT expected sequences.
  final Map<String, BigInt> nextSequences;
}

/// One value-free identity stream frame.
sealed class IdentityFrame {
  const IdentityFrame();
}

/// The atomic publication cutoff sent first during bootstrap.
///
/// Values are LAST committed sequences. This is not a cluster-wide
/// consensus or freshness guarantee; the live tail starts at this cut.
final class IdentityCheckpointFrame extends IdentityFrame {
  IdentityCheckpointFrame._(Map<String, BigInt> lastSequences)
    : lastSequences = Map<String, BigInt>.unmodifiable(lastSequences);

  /// Immutable per-origin LAST committed sequence vector.
  final Map<String, BigInt> lastSequences;
}

/// The exact category of a committed graph mutation.
enum IdentityOperation {
  /// A Vertex Put, including a causal barrier or born-expired overwrite.
  putVertex,

  /// A Vertex Delete, including exact victims of a capped prefix Delete.
  deleteVertex,

  /// An additive Edge write.
  addEdge,

  /// An idempotent Edge replacement.
  putEdge,

  /// An Edge Delete, including exact victims of a capped prefix Delete.
  deleteEdge,
}

/// The original mutation's Hybrid Logical Clock coordinate.
final class IdentityHlc {
  const IdentityHlc._({
    required this.wallNanoseconds,
    required this.logical,
    required this.nodeId,
  });

  /// Signed nanoseconds since the Unix epoch, without DateTime precision loss.
  final BigInt wallNanoseconds;

  /// Logical counter within the physical tick.
  final int logical;

  /// Canonical origin node ID.
  final String nodeId;
}

/// One bounded fragment of a committed mutation's exact identities.
///
/// The durable per-origin cursor must advance only after [isLast] is applied.
/// A final fragment can have no identities. No graph values, Edge weights,
/// contribution IDs, or credentials are present in this type.
final class IdentityChunkFrame extends IdentityFrame {
  IdentityChunkFrame._({
    required this.origin,
    required this.sequence,
    required this.hlc,
    required this.operation,
    required this.chunkIndex,
    required this.isLast,
    required this.firstItemIndex,
    required List<String> vertexKeys,
    required List<EdgeRef> edgeKeys,
  }) : vertexKeys = List<String>.unmodifiable(vertexKeys),
       edgeKeys = List<EdgeRef>.unmodifiable(edgeKeys);

  /// Canonical 32-character hexadecimal origin ID.
  final String origin;

  /// Origin-local committed sequence, represented without signed truncation.
  final BigInt sequence;

  /// Causal coordinate of the original mutation.
  final IdentityHlc hlc;

  /// Original operation category.
  final IdentityOperation operation;

  /// Zero-based chunk index within [sequence].
  final int chunkIndex;

  /// True only for this mutation's final chunk.
  final bool isLast;

  /// First projected identity index in this chunk.
  final int firstItemIndex;

  /// Exact Vertex keys to invalidate.
  final List<String> vertexKeys;

  /// Exact Edge identities to invalidate.
  final List<EdgeRef> edgeKeys;
}

/// Value-free, deployment-scoped replication changes for cache invalidation.
extension LanternChanges on LanternClient {
  /// Opens one identity-only Subscribe stream.
  ///
  /// Set [bootstrap] for an atomic checkpoint followed by live chunks. On
  /// resume, pass a [cursor] of NEXT expected sequences; absent origins start
  /// at their first retained mutation. A `failedPrecondition` gap requires a
  /// fresh bootstrap and resident-key revalidation. No retry, endpoint
  /// discovery, OS scheduling, or tenant filtering is performed here.
  /// Unexpected clean EOF also requires bootstrap and resident-key
  /// revalidation, and is reported as `failedPrecondition`.
  ///
  /// This long-lived stream ignores the client's default unary timeout. An
  /// explicit [LanternCallOptions.timeout] or deadline still applies. Cancel
  /// the returned subscription or its cancellation token to release the RPC.
  Stream<IdentityFrame> subscribeIdentity({
    IdentityNextCursor? cursor,
    bool bootstrap = false,
    LanternCallOptions? options,
  }) {
    _ensureOpen();
    if (bootstrap && (cursor?.nextSequences.isNotEmpty ?? false)) {
      throw _invalidArgumentException(
        'identity bootstrap requires an empty cursor',
      );
    }
    final request = $replication.SubscribeRequest(
      fromSeqPerOrigin: (cursor?.nextSequences ?? const <String, BigInt>{})
          .entries
          .map((entry) => MapEntry(entry.key, _uint64ToFixnum(entry.value))),
      projection:
          $replication.SubscribeProjection.SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
      bootstrap: bootstrap,
    );
    final raw = $replication_client.LanternReplicationServiceClient(
      _invoker.transport,
    );
    final stream = _invoker.invokeStream<$replication.SubscribeResponse>(
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.subscribe(
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
    return _decodeIdentityFrames(
      stream,
      bootstrap: bootstrap,
      cancellation: options?.cancellation,
    );
  }
}

Stream<IdentityFrame> _decodeIdentityFrames(
  Stream<$replication.SubscribeResponse> source, {
  required bool bootstrap,
  LanternCancellationToken? cancellation,
}) {
  StreamSubscription<$replication.SubscribeResponse>? upstream;
  late final StreamController<IdentityFrame> controller;
  var checkpointSeen = false;
  var stopped = false;
  var paused = false;

  void fail(Object error, StackTrace stack) {
    if (stopped) return;
    stopped = true;
    controller.addError(error, stack);
    final active = upstream;
    if (active != null) active.cancel().ignore();
    unawaited(controller.close());
  }

  controller = StreamController<IdentityFrame>(
    sync: true,
    onListen: () {
      final active = source.listen(
        (response) {
          if (stopped) return;
          try {
            switch (response.whichEvent()) {
              case $replication.SubscribeResponse_Event.checkpoint:
                if (!bootstrap || checkpointSeen) {
                  throw _internalSdkException('unexpected identity checkpoint');
                }
                if (response.writeToBuffer().length > _maxIdentityFrameBytes) {
                  throw _internalSdkException(
                    'identity checkpoint exceeds size limit',
                  );
                }
                checkpointSeen = true;
                controller.add(_decodeIdentityCheckpoint(response.checkpoint));
              case $replication.SubscribeResponse_Event.identityChunk:
                if (bootstrap && !checkpointSeen) {
                  throw _internalSdkException(
                    'identity chunk preceded checkpoint',
                  );
                }
                controller.add(_decodeIdentityChunk(response));
              case $replication.SubscribeResponse_Event.mutation:
              case $replication.SubscribeResponse_Event.notSet:
                throw _internalSdkException('unexpected identity stream frame');
            }
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
          } else if (bootstrap && !checkpointSeen) {
            fail(
              _internalSdkException(
                'identity bootstrap ended without checkpoint',
              ),
              StackTrace.current,
            );
          } else {
            fail(
              LanternFailedPreconditionException._(
                _ErrorData(
                  transportCode: connect.Code.failedPrecondition.value,
                  transportCodeName: connect.Code.failedPrecondition.name,
                  message:
                      'identity stream ended unexpectedly; bootstrap and revalidate resident keys',
                  headers: {},
                  trailers: {},
                  metadata: {},
                ),
              ),
              StackTrace.current,
            );
          }
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
      final active = upstream;
      if (active != null) active.cancel().ignore();
    },
  );
  return controller.stream;
}

IdentityCheckpointFrame _decodeIdentityCheckpoint(
  $replication.IdentityCheckpoint raw,
) {
  final sequences = <String, BigInt>{};
  for (final entry in raw.lastSeqPerOrigin.entries) {
    if (!_isValidIdentityOrigin(entry.key)) {
      throw _internalSdkException('identity checkpoint has invalid origin');
    }
    sequences[entry.key] = _uint64FromFixnum(entry.value);
  }
  return IdentityCheckpointFrame._(sequences);
}

IdentityChunkFrame _decodeIdentityChunk(
  $replication.SubscribeResponse response,
) {
  if (response.writeToBuffer().length > _maxIdentityFrameBytes) {
    throw _internalSdkException('identity frame exceeds size limit');
  }
  final raw = response.identityChunk;
  final origin = _identityOriginFromBytes(raw.origin);
  final sequence = _uint64FromFixnum(raw.seq);
  if (sequence == BigInt.zero || !raw.hasHlc()) {
    throw _internalSdkException('identity chunk is missing sequence or HLC');
  }
  final hlcOrigin = _identityOriginFromBytes(raw.hlc.nodeId);
  if (hlcOrigin != origin) {
    throw _internalSdkException('identity chunk HLC origin mismatch');
  }
  final count = raw.vertexKeys.length + raw.edgeKeys.length;
  if (count > _maxIdentityChunkItems || (!raw.isLast && count == 0)) {
    throw _internalSdkException('identity chunk has invalid item count');
  }
  if (raw.chunkIndex == 0 && raw.firstItemIndex != 0) {
    throw _internalSdkException('identity chunk starts at invalid item index');
  }
  if (raw.vertexKeys.any((key) => key.isEmpty) ||
      raw.edgeKeys.any((edge) => edge.tail.isEmpty || edge.head.isEmpty)) {
    throw _internalSdkException('identity chunk has an empty graph identity');
  }
  final operation = switch (raw.operation.value) {
    1 => IdentityOperation.putVertex,
    2 => IdentityOperation.deleteVertex,
    3 => IdentityOperation.addEdge,
    4 => IdentityOperation.putEdge,
    5 => IdentityOperation.deleteEdge,
    _ => throw _internalSdkException('identity chunk has unknown operation'),
  };
  if (switch (operation) {
    IdentityOperation.putVertex ||
    IdentityOperation.deleteVertex => raw.edgeKeys.isNotEmpty,
    IdentityOperation.addEdge ||
    IdentityOperation.putEdge ||
    IdentityOperation.deleteEdge => raw.vertexKeys.isNotEmpty,
  }) {
    throw _internalSdkException(
      'identity chunk mixes operation and key family',
    );
  }
  final edges = <EdgeRef>[];
  for (final edge in raw.edgeKeys) {
    edges.add(EdgeRef(edge.tail, edge.head));
  }
  return IdentityChunkFrame._(
    origin: origin,
    sequence: sequence,
    hlc: IdentityHlc._(
      wallNanoseconds: BigInt.parse(raw.hlc.wallNs.toString()),
      logical: raw.hlc.logical,
      nodeId: hlcOrigin,
    ),
    operation: operation,
    chunkIndex: raw.chunkIndex,
    isLast: raw.isLast,
    firstItemIndex: raw.firstItemIndex,
    vertexKeys: raw.vertexKeys,
    edgeKeys: edges,
  );
}

void _validateIdentityOrigin(String value) {
  if (!_isValidIdentityOrigin(value)) {
    throw _invalidArgumentException(
      'identity origin must be a nonzero lowercase 16-byte hex ID',
    );
  }
}

bool _isValidIdentityOrigin(String value) =>
    _identityOriginPattern.hasMatch(value) &&
    value != '00000000000000000000000000000000';

String _identityOriginFromBytes(List<int> value) {
  if (value.length != 16 || value.every((byte) => byte == 0)) {
    throw _internalSdkException('identity origin must be 16 nonzero bytes');
  }
  return value.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();
}
