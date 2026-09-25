part of 'client.dart';

const int _receiptEpochLength = 16;
const int _receiptOperationIdLength = 49;
const int _receiptOperationRandomLength = 24;
const int _receiptGroupIdLength = 16;
const int _receiptEndpointComponentLength = 16;
const int _receiptFingerprintLength = 32;
const int _receiptIntentDigestLength = 32;
const int _receiptOperationIdVersion = 1;
const int _receiptStatusBatchSize = 10000;
const Duration _minimumReceiptRetention = Duration(hours: 1);
const Duration _maximumReceiptRetention = Duration(days: 30);
final BigInt _receiptMaxSignedInt64 = BigInt.from(_maxInt64);

/// Supplies cryptographically random bytes used to mint receipt identities.
///
/// [LanternClient.connect] uses secure platform entropy by default. Tests can
/// inject a deterministic source that returns exactly the requested length.
typedef ReceiptRandomSource = Uint8List Function(int length);

/// A deployment epoch encoded into every receipt operation ID.
final class ReceiptEpoch {
  /// Creates a deployment epoch from exactly 16 nonzero bytes.
  ReceiptEpoch(Uint8List bytes)
    : _bytes = _validatedReceiptBytes(
        bytes,
        expectedLength: _receiptEpochLength,
        field: 'receipt deployment epoch',
      );

  final Uint8List _bytes;

  /// Returns a defensive copy of the epoch bytes.
  Uint8List get bytes => Uint8List.fromList(_bytes);

  @override
  bool operator ==(Object other) =>
      other is ReceiptEpoch && _receiptBytesEqual(_bytes, other._bytes);

  @override
  int get hashCode => Object.hashAll(_bytes);
}

/// An immutable, globally scoped receipt operation identity.
final class ReceiptOperationId {
  /// Parses and validates a version-1 receipt operation ID.
  ReceiptOperationId(Uint8List bytes)
    : _bytes = _validatedReceiptOperationId(bytes),
      epoch = ReceiptEpoch(
        Uint8List.fromList(bytes.sublist(1, 1 + _receiptEpochLength)),
      ),
      issuedAt = _receiptOperationIdIssuedAt(bytes);

  final Uint8List _bytes;

  /// The deployment epoch encoded into this operation ID.
  final ReceiptEpoch epoch;

  /// The UTC issuance time encoded into this operation ID.
  final DateTime issuedAt;

  /// Returns a defensive copy of the operation ID bytes.
  Uint8List get bytes => Uint8List.fromList(_bytes);

  @override
  bool operator ==(Object other) =>
      other is ReceiptOperationId && _receiptBytesEqual(_bytes, other._bytes);

  @override
  int get hashCode => Object.hashAll(_bytes);
}

/// An immutable logical-call group identity shared by one mutation batch.
final class ReceiptGroupId {
  /// Creates a group ID from exactly 16 nonzero bytes.
  ReceiptGroupId(Uint8List bytes)
    : _bytes = _validatedReceiptBytes(
        bytes,
        expectedLength: _receiptGroupIdLength,
        field: 'receipt group ID',
      );

  final Uint8List _bytes;

  /// Returns a defensive copy of the group ID bytes.
  Uint8List get bytes => Uint8List.fromList(_bytes);

  @override
  bool operator ==(Object other) =>
      other is ReceiptGroupId && _receiptBytesEqual(_bytes, other._bytes);

  @override
  int get hashCode => Object.hashAll(_bytes);
}

/// The server identity that bounds safe replay of a destructive mutation.
final class ReceiptEndpoint {
  /// Creates a validated endpoint continuity marker.
  ReceiptEndpoint({required Uint8List nodeId, required Uint8List generation})
    : _nodeId = _validatedReceiptBytes(
        nodeId,
        expectedLength: _receiptEndpointComponentLength,
        field: 'receipt endpoint node ID',
      ),
      _generation = _validatedReceiptBytes(
        generation,
        expectedLength: _receiptEndpointComponentLength,
        field: 'receipt endpoint generation',
      );

  final Uint8List _nodeId;
  final Uint8List _generation;

  /// Returns a defensive copy of the endpoint node ID.
  Uint8List get nodeId => Uint8List.fromList(_nodeId);

  /// Returns a defensive copy of the endpoint generation.
  Uint8List get generation => Uint8List.fromList(_generation);

  @override
  bool operator ==(Object other) =>
      other is ReceiptEndpoint &&
      _receiptBytesEqual(_nodeId, other._nodeId) &&
      _receiptBytesEqual(_generation, other._generation);

  @override
  int get hashCode =>
      Object.hash(Object.hashAll(_nodeId), Object.hashAll(_generation));
}

/// The active bounded-receipt policy reported by a Lantern endpoint.
final class ReceiptPolicy {
  ReceiptPolicy._({
    required this.deploymentEpoch,
    required this.retention,
    required this.maxEntries,
    required this.maxBytes,
    required Uint8List fingerprint,
  }) : _fingerprint = Uint8List.fromList(fingerprint);

  /// The deployment epoch accepted by the endpoint.
  final ReceiptEpoch deploymentEpoch;

  /// The server retention window for receipt evidence.
  final Duration retention;

  /// The maximum number of receipt entries retained by the endpoint.
  final BigInt maxEntries;

  /// The maximum receipt bytes retained by the endpoint.
  final BigInt maxBytes;

  final Uint8List _fingerprint;

  /// Returns a defensive copy of the policy fingerprint.
  Uint8List get fingerprint => Uint8List.fromList(_fingerprint);
}

/// Capability information returned by [LanternReceipts.getReceiptCapability].
sealed class ReceiptCapability {
  const ReceiptCapability();

  /// Whether bounded mutation receipts are currently enabled.
  bool get enabled;
}

/// Receipt capability is disabled or temporarily uncertified.
final class ReceiptCapabilityDisabled extends ReceiptCapability {
  /// Creates the disabled capability value.
  const ReceiptCapabilityDisabled();

  @override
  bool get enabled => false;
}

/// Receipt capability is enabled with an active policy and endpoint marker.
final class ReceiptCapabilityEnabled extends ReceiptCapability {
  const ReceiptCapabilityEnabled._({
    required this.policy,
    required this.endpoint,
    required this.serverNow,
    required this.observedAt,
  });

  /// The active receipt policy.
  final ReceiptPolicy policy;

  /// The endpoint continuity marker that mutations must echo.
  final ReceiptEndpoint endpoint;

  /// The server's UTC time when it produced the capability response.
  final DateTime serverNow;

  /// The local injected-clock time when the response was observed.
  final DateTime observedAt;

  @override
  bool get enabled => true;
}

/// Immutable receipt identities for one index-aligned mutation call.
///
/// Persist this value before sending the mutation when the caller must recover
/// from a lost response. Reuse the exact context for status lookup or an
/// endpoint-safe retry rather than minting replacement identities.
final class ReceiptContext {
  /// Creates and validates a persisted receipt context.
  ReceiptContext({
    required Iterable<ReceiptOperationId> operationIds,
    required this.groupId,
    required this.endpoint,
  }) : operationIds = List<ReceiptOperationId>.unmodifiable(operationIds) {
    if (this.operationIds.isEmpty) {
      throw _invalidArgumentException(
        'receipt operation IDs must not be empty',
      );
    }
    if (this.operationIds.length > LanternCrud.maxBatchSize) {
      throw _invalidArgumentException(
        'receipt context exceeds ${LanternCrud.maxBatchSize} operation IDs',
      );
    }
    final epoch = this.operationIds.first.epoch;
    final seen = <ReceiptOperationId>{};
    for (var index = 0; index < this.operationIds.length; index++) {
      final operationId = this.operationIds[index];
      if (operationId.epoch != epoch) {
        throw _invalidArgumentException(
          'receipt operation ID $index uses a different deployment epoch',
        );
      }
      if (!seen.add(operationId)) {
        throw _invalidArgumentException(
          'receipt operation ID $index is duplicated',
        );
      }
    }
  }

  /// The operation IDs aligned to mutation request indices.
  final List<ReceiptOperationId> operationIds;

  /// The logical-call group ID shared by every operation.
  final ReceiptGroupId groupId;

  /// The endpoint continuity marker captured before the mutation.
  final ReceiptEndpoint endpoint;

  /// The deployment epoch shared by all [operationIds].
  ReceiptEpoch get epoch => operationIds.first.epoch;
}

/// The three possible read-only receipt lookup states.
enum ReceiptStatusState {
  /// The server retains an exact receipt and original operation result.
  confirmed,

  /// The server has not observed the operation, but execution remains unknown.
  notYetObserved,

  /// The retention boundary means execution can no longer be proven.
  noLongerProvable,
}

/// An exact retained Edge Delete receipt.
final class EdgeDeleteReceipt {
  EdgeDeleteReceipt._({
    required this.operationId,
    required this.groupId,
    required this.itemIndex,
    required this.itemCount,
    required Uint8List intentSha256,
    required this.deadline,
    required this.existed,
  }) : _intentSha256 = Uint8List.fromList(intentSha256);

  /// The operation ID whose result this receipt records.
  final ReceiptOperationId operationId;

  /// The logical-call group ID.
  final ReceiptGroupId groupId;

  /// The operation's original zero-based request index.
  final int itemIndex;

  /// The original request item count.
  final int itemCount;

  final Uint8List _intentSha256;

  /// The receipt retention deadline.
  final DateTime deadline;

  /// Whether the requested edge existed when it was deleted.
  final bool existed;

  /// Returns a defensive copy of the canonical intent SHA-256 digest.
  Uint8List get intentSha256 => Uint8List.fromList(_intentSha256);
}

/// A read-only receipt status aligned to one requested operation ID.
final class ReceiptStatus {
  const ReceiptStatus._({
    required this.operationId,
    required this.state,
    this.receipt,
  });

  /// The requested operation ID.
  final ReceiptOperationId operationId;

  /// The current evidence state.
  final ReceiptStatusState state;

  /// The exact Edge Delete receipt when [state] is
  /// [ReceiptStatusState.confirmed].
  final EdgeDeleteReceipt? receipt;
}

/// An exact, request-index-aligned result from receipt-bearing Edge Delete.
final class ReceiptEdgeDeleteResult {
  const ReceiptEdgeDeleteResult._({
    required this.edge,
    required this.operationId,
    required this.existed,
  });

  /// The requested edge identity.
  final EdgeRef edge;

  /// The stable operation ID used for this request item.
  final ReceiptOperationId operationId;

  /// Whether the edge existed when the server deleted it.
  final bool existed;
}

/// Why a receipt-bearing destructive call requires read-only reconciliation.
enum ReceiptReconciliationReason {
  /// The request may have reached the endpoint, but no response was obtained.
  outcomeUnknown,

  /// Receipt capability was disabled before a safe replay could occur.
  capabilityDisabled,

  /// The active deployment epoch changed.
  deploymentEpochChanged,

  /// The responding node identity changed.
  nodeChanged,

  /// The endpoint generation changed.
  generationChanged,

  /// The endpoint rejected the supplied continuity marker.
  continuityRejected,
}

/// A destructive receipt operation stopped before unsafe replay.
///
/// [context] is the exact caller context and can be persisted or passed to
/// [LanternReceipts.getReceiptStatuses] for read-only reconciliation.
final class ReceiptReconciliationException implements Exception {
  /// Creates a reconciliation exception.
  const ReceiptReconciliationException({
    required this.reason,
    required this.context,
    required this.message,
    this.cause,
  });

  /// The reason automatic mutation replay stopped.
  final ReceiptReconciliationReason reason;

  /// The exact operation context used by the uncertain mutation.
  final ReceiptContext context;

  /// A human-readable explanation.
  final String message;

  /// The underlying transport or protocol failure, when available.
  final Object? cause;

  @override
  String toString() {
    final suffix = cause == null ? '' : ': $cause';
    return 'ReceiptReconciliationException($reason): $message$suffix';
  }
}

/// Bounded mutation receipt support for [LanternClient].
extension LanternReceipts on LanternClient {
  /// Reads the endpoint's bounded-receipt capability without mutating state.
  Future<ReceiptCapability> getReceiptCapability({
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final callOptions = _freezeCallOptions(options);
    _throwIfCanceled(callOptions?.cancellation);
    final response = await _invoke(
      'GetReceiptCapability',
      callOptions,
      (raw, headers, signal, onHeader, onTrailer) => raw.getReceiptCapability(
        $graph.GetReceiptCapabilityRequest(),
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    final observedAt = _clock().toUtc();
    if (!response.enabled) {
      if (response.hasPolicy() ||
          response.hasEndpoint() ||
          response.hasServerNowUnixMs()) {
        throw _internalSdkException(
          'disabled receipt capability returned identity-bearing fields',
        );
      }
      return const ReceiptCapabilityDisabled();
    }
    if (!response.hasPolicy() ||
        !response.hasEndpoint() ||
        !response.hasServerNowUnixMs()) {
      throw _internalSdkException(
        'enabled receipt capability omitted required fields',
      );
    }
    return ReceiptCapabilityEnabled._(
      policy: _receiptPolicyFromProto(response.policy),
      endpoint: _receiptEndpointFromProto(response.endpoint),
      serverNow: _receiptDateTimeFromUint64(
        response.serverNowUnixMs,
        field: 'receipt capability server time',
      ),
      observedAt: observedAt,
    );
  }

  /// Mints an immutable context for an index-aligned receipt mutation.
  ///
  /// This method performs no network I/O. [capability] should be freshly
  /// fetched from the endpoint that will receive the mutation.
  ReceiptContext mintReceiptContext({
    required ReceiptCapabilityEnabled capability,
    required int itemCount,
  }) {
    if (itemCount <= 0 || itemCount > LanternCrud.maxBatchSize) {
      throw _invalidArgumentException(
        'receipt itemCount must be in [1, ${LanternCrud.maxBatchSize}]',
      );
    }
    final now = _clock().toUtc();
    final elapsed = now.difference(capability.observedAt);
    if (elapsed.isNegative) {
      throw _invalidArgumentException(
        'clock moved backwards since receipt capability observation',
      );
    }
    late final DateTime issuedAt;
    try {
      issuedAt = capability.serverNow.add(elapsed);
    } on ArgumentError {
      throw _invalidArgumentException(
        'receipt issuance time is outside the supported range',
      );
    }
    if (issuedAt.millisecondsSinceEpoch < 0) {
      throw _invalidArgumentException(
        'receipt issuance time must not precede the Unix epoch',
      );
    }
    final groupId = ReceiptGroupId(
      _readReceiptRandomBytes(
        _receiptGroupIdLength,
        field: 'receipt group ID entropy',
      ),
    );
    final operationIds = List<ReceiptOperationId>.generate(
      itemCount,
      (_) => ReceiptOperationId(
        _mintReceiptOperationId(
          capability.policy.deploymentEpoch,
          issuedAt,
          _readReceiptRandomBytes(
            _receiptOperationRandomLength,
            field: 'receipt operation ID entropy',
          ),
        ),
      ),
      growable: false,
    );
    return ReceiptContext(
      operationIds: operationIds,
      groupId: groupId,
      endpoint: capability.endpoint,
    );
  }

  /// Looks up receipt evidence in request order.
  ///
  /// The read is chunked at the server's 10,000-item ceiling. Duplicate
  /// operation IDs are preserved and each response must align exactly.
  Future<List<ReceiptStatus>> getReceiptStatuses(
    Iterable<ReceiptOperationId> operationIds, {
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final ids = List<ReceiptOperationId>.unmodifiable(operationIds);
    if (ids.length > LanternCrud.maxBatchSize) {
      throw _invalidArgumentException(
        'receipt status lookup exceeds ${LanternCrud.maxBatchSize} IDs',
      );
    }
    if (ids.isEmpty) return const <ReceiptStatus>[];
    final callOptions = _freezeCallOptions(options);
    final statuses = <ReceiptStatus>[];
    for (
      var offset = 0;
      offset < ids.length;
      offset += _receiptStatusBatchSize
    ) {
      _throwIfCanceled(callOptions?.cancellation);
      final end = _chunkEnd(offset, _receiptStatusBatchSize, ids.length);
      final chunk = ids.sublist(offset, end);
      final response = await _invoke(
        'GetReceiptStatuses',
        callOptions,
        (raw, headers, signal, onHeader, onTrailer) => raw.getReceiptStatuses(
          $graph.GetReceiptStatusesRequest(
            operationIds: chunk.map((operationId) => operationId.bytes),
          ),
          headers: headers,
          signal: signal,
          onHeader: onHeader,
          onTrailer: onTrailer,
        ),
      );
      if (response.statuses.length != chunk.length) {
        throw _internalSdkException(
          'receipt status response length does not match its request',
        );
      }
      for (var index = 0; index < chunk.length; index++) {
        statuses.add(
          _receiptStatusFromProto(chunk[index], response.statuses[index]),
        );
      }
    }
    return List<ReceiptStatus>.unmodifiable(statuses);
  }

  /// Looks up one receipt by forwarding to plural [getReceiptStatuses].
  Future<ReceiptStatus> getReceiptStatus(
    ReceiptOperationId operationId, {
    LanternCallOptions? options,
  }) async {
    final statuses = await getReceiptStatuses(<ReceiptOperationId>[
      operationId,
    ], options: options);
    return statuses.single;
  }

  /// Deletes edges with stable receipt identities and exact original results.
  ///
  /// Existing receipt-less [LanternCrud.deleteEdges] behavior is unchanged.
  /// Automatic retries reuse [context] only after a read-only capability check
  /// proves that deployment epoch, node, and generation are unchanged.
  Future<List<ReceiptEdgeDeleteResult>> deleteEdgesWithReceipt(
    Iterable<EdgeRef> edges, {
    required ReceiptContext context,
    LanternCallOptions? options,
  }) async {
    _ensureOpen();
    final input = List<EdgeRef>.unmodifiable(edges);
    if (input.isEmpty) {
      throw _invalidArgumentException(
        'receipt-bearing Edge Delete must not be empty',
      );
    }
    if (input.length > LanternCrud.maxBatchSize) {
      throw _invalidArgumentException(
        'receipt-bearing Edge Delete exceeds ${LanternCrud.maxBatchSize} items',
      );
    }
    if (context.operationIds.length != input.length) {
      throw _invalidArgumentException(
        'receipt operation IDs must align exactly with Edge Delete items',
      );
    }
    for (var index = 0; index < input.length; index++) {
      if (input[index].tail.isEmpty || input[index].head.isEmpty) {
        throw _invalidArgumentException(
          'receipt Edge Delete item $index has an empty endpoint key',
        );
      }
    }
    final callOptions = _freezeCallOptions(options);
    final request = $graph.DeleteEdgesRequest(
      edges: input.map(
        (edge) => $graph.EdgeKey(tail: edge.tail, head: edge.head),
      ),
      receiptContext: $graph.MutationReceiptContext(
        operationIds: context.operationIds.map(
          (operationId) => operationId.bytes,
        ),
        logicalCallId: context.groupId.bytes,
        endpoint: $graph.ReceiptEndpoint(
          nodeId: context.endpoint.nodeId,
          generation: context.endpoint.generation,
        ),
      ),
    );
    final response = await _invokeReceiptMutation(
      method: 'DeleteEdgesWithReceipt',
      context: context,
      options: callOptions,
      call: (raw, headers, signal, onHeader, onTrailer) => raw.deleteEdges(
        request,
        headers: headers,
        signal: signal,
        onHeader: onHeader,
        onTrailer: onTrailer,
      ),
    );
    if (response.existed.length != input.length) {
      throw ReceiptReconciliationException(
        reason: ReceiptReconciliationReason.outcomeUnknown,
        context: context,
        message: 'Edge Delete returned misaligned receipt results',
        cause: _internalSdkException(
          'DeleteEdges existed count does not match the request',
        ),
      );
    }
    final existedCount = response.existed.where((value) => value).length;
    if (response.deleted != existedCount) {
      throw ReceiptReconciliationException(
        reason: ReceiptReconciliationReason.outcomeUnknown,
        context: context,
        message: 'Edge Delete returned inconsistent receipt results',
        cause: _internalSdkException(
          'DeleteEdges deleted count does not match existed values',
        ),
      );
    }
    return List<ReceiptEdgeDeleteResult>.unmodifiable(
      List<ReceiptEdgeDeleteResult>.generate(
        input.length,
        (index) => ReceiptEdgeDeleteResult._(
          edge: input[index],
          operationId: context.operationIds[index],
          existed: response.existed[index],
        ),
        growable: false,
      ),
    );
  }

  /// Deletes one edge by forwarding to plural [deleteEdgesWithReceipt].
  Future<ReceiptEdgeDeleteResult> deleteEdgeWithReceipt(
    EdgeRef edge, {
    required ReceiptContext context,
    LanternCallOptions? options,
  }) async {
    if (context.operationIds.length != 1) {
      throw _invalidArgumentException(
        'deleteEdgeWithReceipt requires exactly one receipt operation ID',
      );
    }
    final results = await deleteEdgesWithReceipt(
      <EdgeRef>[edge],
      context: context,
      options: options,
    );
    return results.single;
  }
}

extension on LanternClient {
  Uint8List _readReceiptRandomBytes(int length, {required String field}) {
    final bytes = _receiptRandomSource(length);
    if (bytes.length != length) {
      throw _invalidArgumentException(
        '$field source returned ${bytes.length} bytes; expected $length',
      );
    }
    return _validatedReceiptBytes(bytes, expectedLength: length, field: field);
  }

  Future<T> _invokeReceiptMutation<T>({
    required String method,
    required ReceiptContext context,
    required LanternCallOptions? options,
    required Future<T> Function(
      $client.LanternServiceClient raw,
      connect.Headers headers,
      connect.AbortSignal signal,
      void Function(connect.Headers) onHeader,
      void Function(connect.Headers) onTrailer,
    )
    call,
  }) async {
    final policy = options?.retry == false ? null : _retryPolicy;
    var completedAttempts = 0;
    var outcomeUncertain = false;
    while (true) {
      var requestStarted = false;
      try {
        _throwIfCanceled(options?.cancellation);
        _throwIfReceiptDeadlineExpired(options?.deadline);
        final raw = $client.LanternServiceClient(_invoker.transport);
        return await _invoker.invokeUnary(
          options: options,
          onRequestStarted: () {
            requestStarted = true;
          },
          call:
              ({
                required headers,
                required signal,
                required onHeader,
                required onTrailer,
              }) => call(raw, headers, signal, onHeader, onTrailer),
        );
      } on LanternFailedPreconditionException catch (error) {
        throw ReceiptReconciliationException(
          reason: ReceiptReconciliationReason.continuityRejected,
          context: context,
          message: '$method endpoint continuity was rejected',
          cause: error,
        );
      } on LanternException catch (error) {
        final ambiguous =
            requestStarted && _receiptFailureMayBeAmbiguous(error);
        final retryable = policy?.retryable(error) ?? false;
        if (!ambiguous && !retryable) {
          if (!outcomeUncertain) rethrow;
          throw ReceiptReconciliationException(
            reason: ReceiptReconciliationReason.outcomeUnknown,
            context: context,
            message: '$method remained uncertain after a later failure',
            cause: error,
          );
        }
        outcomeUncertain = outcomeUncertain || ambiguous;
        completedAttempts++;
        if (!retryable || completedAttempts >= policy!.maxAttempts) {
          final cause = retryable
              ? LanternRetryExhaustedException(
                  attempts: completedAttempts,
                  cause: error,
                )
              : error;
          if (!outcomeUncertain) throw cause;
          throw ReceiptReconciliationException(
            reason: ReceiptReconciliationReason.outcomeUnknown,
            context: context,
            message: '$method may have executed without a response',
            cause: cause,
          );
        }
        try {
          await _waitForRetry(policy.delay(completedAttempts), options);
          final capability = await getReceiptCapability(
            options: _receiptPreflightOptions(options),
          );
          _validateReceiptContinuity(context, capability);
        } on ReceiptReconciliationException {
          rethrow;
        } on LanternException catch (preflightError) {
          if (!outcomeUncertain) rethrow;
          throw ReceiptReconciliationException(
            reason: ReceiptReconciliationReason.outcomeUnknown,
            context: context,
            message: '$method could not prove endpoint continuity',
            cause: preflightError,
          );
        }
      }
    }
  }

  LanternCallOptions _receiptPreflightOptions(LanternCallOptions? options) {
    if (options == null) {
      return LanternCallOptions(retry: false, disableDefaultTimeout: true);
    }
    return LanternCallOptions(
      deadline: options.deadline,
      cancellation: options.cancellation,
      retry: false,
      disableDefaultTimeout: options.deadline == null,
    );
  }
}

ReceiptRandomSource _secureReceiptRandomSource() {
  final random = Random.secure();
  return (length) {
    Uint8List bytes;
    do {
      bytes = Uint8List.fromList(
        List<int>.generate(length, (_) => random.nextInt(256)),
      );
    } while (bytes.every((byte) => byte == 0));
    return bytes;
  };
}

ReceiptPolicy _receiptPolicyFromProto($graph.ReceiptPolicy value) {
  if (!value.hasRetentionMs() ||
      !value.hasMaxEntries() ||
      !value.hasMaxBytes()) {
    throw _internalSdkException(
      'receipt policy omitted required scalar fields',
    );
  }
  final retentionMilliseconds = _uint64FromFixnum(value.retentionMs);
  if (retentionMilliseconds > _receiptMaxSignedInt64) {
    throw _internalSdkException('receipt retention exceeds the Dart range');
  }
  final retention = Duration(milliseconds: retentionMilliseconds.toInt());
  if (retention < _minimumReceiptRetention ||
      retention > _maximumReceiptRetention) {
    throw _internalSdkException(
      'receipt retention is outside the supported policy range',
    );
  }
  final maxEntries = _uint64FromFixnum(value.maxEntries);
  final maxBytes = _uint64FromFixnum(value.maxBytes);
  if (maxEntries <= BigInt.zero || maxBytes <= BigInt.zero) {
    throw _internalSdkException('receipt policy capacity must be positive');
  }
  final fingerprint = Uint8List.fromList(value.fingerprint);
  if (fingerprint.length != _receiptFingerprintLength) {
    throw _internalSdkException(
      'receipt policy fingerprint must be $_receiptFingerprintLength bytes',
    );
  }
  late final ReceiptEpoch epoch;
  try {
    epoch = ReceiptEpoch(Uint8List.fromList(value.deploymentEpoch));
  } on LanternInvalidArgumentException catch (error) {
    throw _internalSdkException('receipt policy has an invalid epoch: $error');
  }
  return ReceiptPolicy._(
    deploymentEpoch: epoch,
    retention: retention,
    maxEntries: maxEntries,
    maxBytes: maxBytes,
    fingerprint: fingerprint,
  );
}

ReceiptEndpoint _receiptEndpointFromProto($graph.ReceiptEndpoint value) {
  try {
    return ReceiptEndpoint(
      nodeId: Uint8List.fromList(value.nodeId),
      generation: Uint8List.fromList(value.generation),
    );
  } on LanternInvalidArgumentException catch (error) {
    throw _internalSdkException(
      'receipt capability has an invalid endpoint: $error',
    );
  }
}

ReceiptStatus _receiptStatusFromProto(
  ReceiptOperationId expectedOperationId,
  $graph.ReceiptStatus value,
) {
  late final ReceiptOperationId operationId;
  try {
    operationId = ReceiptOperationId(Uint8List.fromList(value.operationId));
  } on LanternInvalidArgumentException catch (error) {
    throw _internalSdkException(
      'receipt status has an invalid operation ID: $error',
    );
  }
  if (operationId != expectedOperationId) {
    throw _internalSdkException(
      'receipt status operation ID is not request-index aligned',
    );
  }
  switch (value.state) {
    case $graph.MutationReceiptState.MUTATION_RECEIPT_STATE_CONFIRMED:
      if (!value.hasReceipt()) {
        throw _internalSdkException(
          'confirmed receipt status omitted its receipt',
        );
      }
      return ReceiptStatus._(
        operationId: operationId,
        state: ReceiptStatusState.confirmed,
        receipt: _edgeDeleteReceiptFromProto(operationId, value.receipt),
      );
    case $graph.MutationReceiptState.MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED:
      if (value.hasReceipt()) {
        throw _internalSdkException(
          'not-yet-observed receipt status included a receipt',
        );
      }
      return ReceiptStatus._(
        operationId: operationId,
        state: ReceiptStatusState.notYetObserved,
      );
    case $graph.MutationReceiptState.MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE:
      if (value.hasReceipt()) {
        throw _internalSdkException(
          'no-longer-provable receipt status included a receipt',
        );
      }
      return ReceiptStatus._(
        operationId: operationId,
        state: ReceiptStatusState.noLongerProvable,
      );
    case $graph.MutationReceiptState.MUTATION_RECEIPT_STATE_UNSPECIFIED:
    default:
      throw _internalSdkException('receipt status has an unknown state');
  }
}

EdgeDeleteReceipt _edgeDeleteReceiptFromProto(
  ReceiptOperationId expectedOperationId,
  $graph.MutationReceipt value,
) {
  if (!value.hasItemCount() ||
      !value.hasDeadlineUnixMs() ||
      !value.hasOriginalResult()) {
    throw _internalSdkException('confirmed receipt omitted required fields');
  }
  late final ReceiptOperationId operationId;
  late final ReceiptGroupId groupId;
  try {
    operationId = ReceiptOperationId(Uint8List.fromList(value.operationId));
    groupId = ReceiptGroupId(Uint8List.fromList(value.logicalCallId));
  } on LanternInvalidArgumentException catch (error) {
    throw _internalSdkException(
      'confirmed receipt has invalid identity fields: $error',
    );
  }
  if (operationId != expectedOperationId) {
    throw _internalSdkException(
      'confirmed receipt operation ID does not match its status',
    );
  }
  if (value.itemCount <= 0 ||
      value.itemCount > LanternCrud.maxBatchSize ||
      value.itemIndex >= value.itemCount) {
    throw _internalSdkException('confirmed receipt has invalid item alignment');
  }
  final intentSha256 = Uint8List.fromList(value.intentSha256);
  if (intentSha256.length != _receiptIntentDigestLength) {
    throw _internalSdkException(
      'confirmed receipt intent digest must be '
      '$_receiptIntentDigestLength bytes',
    );
  }
  final deadline = _receiptDateTimeFromUint64(
    value.deadlineUnixMs,
    field: 'receipt deadline',
  );
  if (!deadline.isAfter(operationId.issuedAt)) {
    throw _internalSdkException(
      'confirmed receipt deadline must follow issuance',
    );
  }
  if (value.originalResult.whichResult() !=
      $graph.ReceiptResult_Result.deleteEdgeExisted) {
    throw _internalSdkException(
      'confirmed receipt result is not a public Edge Delete result',
    );
  }
  return EdgeDeleteReceipt._(
    operationId: operationId,
    groupId: groupId,
    itemIndex: value.itemIndex,
    itemCount: value.itemCount,
    intentSha256: intentSha256,
    deadline: deadline,
    existed: value.originalResult.deleteEdgeExisted,
  );
}

void _validateReceiptContinuity(
  ReceiptContext context,
  ReceiptCapability capability,
) {
  if (capability is ReceiptCapabilityDisabled) {
    throw ReceiptReconciliationException(
      reason: ReceiptReconciliationReason.capabilityDisabled,
      context: context,
      message: 'receipt capability is no longer enabled',
    );
  }
  final enabled = capability as ReceiptCapabilityEnabled;
  if (enabled.policy.deploymentEpoch != context.epoch) {
    throw ReceiptReconciliationException(
      reason: ReceiptReconciliationReason.deploymentEpochChanged,
      context: context,
      message: 'receipt deployment epoch changed',
    );
  }
  if (!_receiptBytesEqual(enabled.endpoint._nodeId, context.endpoint._nodeId)) {
    throw ReceiptReconciliationException(
      reason: ReceiptReconciliationReason.nodeChanged,
      context: context,
      message: 'receipt endpoint node changed',
    );
  }
  if (!_receiptBytesEqual(
    enabled.endpoint._generation,
    context.endpoint._generation,
  )) {
    throw ReceiptReconciliationException(
      reason: ReceiptReconciliationReason.generationChanged,
      context: context,
      message: 'receipt endpoint generation changed',
    );
  }
}

bool _receiptFailureMayBeAmbiguous(LanternException error) =>
    error is LanternUnavailableException ||
    error is LanternDeadlineExceededException ||
    error is LanternCanceledException ||
    error is LanternInternalException;

Uint8List _validatedReceiptOperationId(Uint8List bytes) {
  if (bytes.length != _receiptOperationIdLength) {
    throw _invalidArgumentException(
      'receipt operation ID must be exactly '
      '$_receiptOperationIdLength bytes',
    );
  }
  if (bytes[0] != _receiptOperationIdVersion) {
    throw _invalidArgumentException(
      'receipt operation ID has an unsupported version',
    );
  }
  _validatedReceiptBytes(
    Uint8List.fromList(bytes.sublist(1, 1 + _receiptEpochLength)),
    expectedLength: _receiptEpochLength,
    field: 'receipt operation ID deployment epoch',
  );
  _receiptOperationIdIssuedAt(bytes);
  _validatedReceiptBytes(
    Uint8List.fromList(bytes.sublist(25)),
    expectedLength: _receiptOperationRandomLength,
    field: 'receipt operation ID random component',
  );
  return Uint8List.fromList(bytes);
}

DateTime _receiptOperationIdIssuedAt(Uint8List bytes) {
  var timestamp = BigInt.zero;
  for (var index = 17; index < 25; index++) {
    timestamp = (timestamp << 8) | BigInt.from(bytes[index]);
  }
  if (timestamp > _receiptMaxSignedInt64) {
    throw _invalidArgumentException(
      'receipt operation ID timestamp exceeds signed 64-bit range',
    );
  }
  try {
    return DateTime.fromMillisecondsSinceEpoch(timestamp.toInt(), isUtc: true);
  } on ArgumentError {
    throw _invalidArgumentException(
      'receipt operation ID timestamp is outside the Dart range',
    );
  }
}

Uint8List _mintReceiptOperationId(
  ReceiptEpoch epoch,
  DateTime issuedAt,
  Uint8List random,
) {
  final timestamp = issuedAt.toUtc().millisecondsSinceEpoch;
  if (timestamp < 0) {
    throw _invalidArgumentException(
      'receipt issuance time must not precede the Unix epoch',
    );
  }
  final bytes = Uint8List(_receiptOperationIdLength);
  bytes[0] = _receiptOperationIdVersion;
  bytes.setRange(1, 17, epoch._bytes);
  var remaining = timestamp;
  for (var index = 24; index >= 17; index--) {
    bytes[index] = remaining & 0xff;
    remaining >>= 8;
  }
  bytes.setRange(25, _receiptOperationIdLength, random);
  return bytes;
}

DateTime _receiptDateTimeFromUint64(Int64 value, {required String field}) {
  final milliseconds = _uint64FromFixnum(value);
  if (milliseconds > _receiptMaxSignedInt64) {
    throw _internalSdkException('$field exceeds signed 64-bit range');
  }
  try {
    return DateTime.fromMillisecondsSinceEpoch(
      milliseconds.toInt(),
      isUtc: true,
    );
  } on ArgumentError {
    throw _internalSdkException('$field is outside the Dart range');
  }
}

Uint8List _validatedReceiptBytes(
  Uint8List bytes, {
  required int expectedLength,
  required String field,
}) {
  if (bytes.length != expectedLength) {
    throw _invalidArgumentException(
      '$field must be exactly $expectedLength bytes',
    );
  }
  if (bytes.every((byte) => byte == 0)) {
    throw _invalidArgumentException('$field must not be all zero');
  }
  return Uint8List.fromList(bytes);
}

bool _receiptBytesEqual(Uint8List left, Uint8List right) {
  if (left.length != right.length) return false;
  var difference = 0;
  for (var index = 0; index < left.length; index++) {
    difference |= left[index] ^ right[index];
  }
  return difference == 0;
}

void _throwIfReceiptDeadlineExpired(DateTime? deadline) {
  if (deadline == null || deadline.isAfter(DateTime.now())) return;
  throw LanternDeadlineExceededException._(
    _ErrorData(
      transportCode: connect.Code.deadlineExceeded.value,
      transportCodeName: connect.Code.deadlineExceeded.name,
      message: 'operation exceeded deadline',
      headers: const {},
      trailers: const {},
      metadata: const {},
    ),
  );
}
