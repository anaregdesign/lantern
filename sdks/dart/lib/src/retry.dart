part of 'client.dart';

/// Opt-in bounded retry policy for mobile network failures.
///
/// The zero/default fields normalize to three total attempts, 100 ms base
/// delay, a 2 second per-delay cap, and retrying `unavailable` only.
final class RetryPolicy {
  /// Creates a retry policy. Supplying this object opts the client into retry.
  const RetryPolicy({
    this.maxAttempts = 0,
    this.baseDelay = Duration.zero,
    this.maxDelay = Duration.zero,
    this.retryResourceExhausted = false,
  });

  /// Total attempts including the first; values below one normalize to three.
  final int maxAttempts;

  /// Exponential backoff seed; non-positive values normalize to 100 ms.
  final Duration baseDelay;

  /// Maximum one backoff delay; non-positive values normalize to 2 seconds.
  final Duration maxDelay;

  /// Whether `resource_exhausted` joins the default `unavailable` status.
  final bool retryResourceExhausted;

  _NormalizedRetryPolicy _normalized() => _NormalizedRetryPolicy(
    maxAttempts: maxAttempts < 1 ? 3 : maxAttempts,
    baseDelay: baseDelay <= Duration.zero
        ? const Duration(milliseconds: 100)
        : baseDelay,
    maxDelay: maxDelay <= Duration.zero ? const Duration(seconds: 2) : maxDelay,
    retryResourceExhausted: retryResourceExhausted,
  );
}

/// A retry-eligible operation exhausted its bounded attempt budget.
final class LanternRetryExhaustedException implements Exception {
  /// Creates an exhausted-attempt wrapper around the final typed cause.
  const LanternRetryExhaustedException({
    required this.attempts,
    required this.cause,
  });

  /// Total attempts performed.
  final int attempts;

  /// Final typed transport failure.
  final LanternException cause;

  @override
  String toString() =>
      'LanternRetryExhaustedException(attempts: $attempts, cause: $cause)';
}

/// Internal retry classes, exposed only through the package's `src` library
/// so the coverage gate can force a decision for every generated RPC.
enum RpcRetryClass {
  /// Unary read/query/status operation.
  read,

  /// Idempotent state-setting write.
  stablePut,

  /// Additive write requiring stable contribution IDs.
  additive,

  /// Observable-result mutation that must not be replayed.
  never,

  /// Streaming operation that must not be replayed.
  stream,
}

/// Fail-closed retry classification registry used by SDK facades and tests.
final class RetryRegistry {
  RetryRegistry._();

  /// Classification for every generated Lantern service method.
  static const Map<String, RpcRetryClass> classifications = {
    'Illuminate': RpcRetryClass.read,
    'GetVertex': RpcRetryClass.read,
    'GetVertices': RpcRetryClass.read,
    'PutVertex': RpcRetryClass.stablePut,
    'PutVertices': RpcRetryClass.stablePut,
    'DeleteVertex': RpcRetryClass.never,
    'DeleteVertices': RpcRetryClass.never,
    'ScanVertices': RpcRetryClass.read,
    'ScanVertexKeys': RpcRetryClass.read,
    'SearchVertices': RpcRetryClass.read,
    'CountVerticesByPrefix': RpcRetryClass.read,
    'DeleteVerticesByPrefix': RpcRetryClass.never,
    'TopVerticesByDegree': RpcRetryClass.read,
    'GetEdge': RpcRetryClass.read,
    'GetEdges': RpcRetryClass.read,
    'AddEdge': RpcRetryClass.additive,
    'AddEdges': RpcRetryClass.additive,
    'PutEdge': RpcRetryClass.stablePut,
    'PutEdges': RpcRetryClass.stablePut,
    'DeleteEdge': RpcRetryClass.never,
    'DeleteEdges': RpcRetryClass.never,
    'DeleteEdgesByPrefix': RpcRetryClass.never,
    'ScanEdges': RpcRetryClass.read,
    'GetServerStatus': RpcRetryClass.read,
    'GetReplicationStatus': RpcRetryClass.read,
    'BackupSnapshot': RpcRetryClass.stream,
    // High-level facades whose result semantics differ from their shared wire
    // request or whose operation expands into AddEdges.
    'PutVertexIfAbsent': RpcRetryClass.never,
    'PutVerticesIfAbsent': RpcRetryClass.never,
    'AddDecayingEdge': RpcRetryClass.additive,
    'ScanVerticesAll': RpcRetryClass.read,
    'ScanVertexKeysAll': RpcRetryClass.read,
    'ScanEdgesAll': RpcRetryClass.read,
  };

  /// Returns [RpcRetryClass.never] for unknown methods.
  static RpcRetryClass classify(String method) =>
      classifications[method] ?? RpcRetryClass.never;

  static bool _allows(String method, {required bool additiveSafe}) {
    return switch (classify(method)) {
      RpcRetryClass.read || RpcRetryClass.stablePut => true,
      RpcRetryClass.additive => additiveSafe,
      RpcRetryClass.never || RpcRetryClass.stream => false,
    };
  }
}

/// Builds Lantern's canonical 24-byte contribution ID.
///
/// The first 16 bytes are [nonce]. The final eight bytes are the big-endian
/// uint64 `(sequence << 16) | index`. Persist the returned bytes with a durable
/// outbox intent when replay must survive process death.
Uint8List contributionIdFrom({
  required Uint8List nonce,
  required BigInt sequence,
  required int index,
}) {
  if (nonce.length != 16) {
    throw _invalidArgumentException('contribution nonce must be 16 bytes');
  }
  final maxSequence = (BigInt.one << 48) - BigInt.one;
  if (sequence < BigInt.zero || sequence > maxSequence) {
    throw _invalidArgumentException('contribution sequence must fit uint48');
  }
  if (index < 0 || index > 0xffff) {
    throw _invalidArgumentException('contribution index must fit uint16');
  }
  final result = Uint8List(24)..setRange(0, 16, nonce);
  var packed = (sequence << 16) | BigInt.from(index);
  for (var offset = 23; offset >= 16; offset--) {
    result[offset] = (packed & BigInt.from(0xff)).toInt();
    packed >>= 8;
  }
  return result;
}

final class _ContribIdGenerator {
  _ContribIdGenerator(this._nonce);

  factory _ContribIdGenerator.secure() {
    final random = Random.secure();
    return _ContribIdGenerator(
      Uint8List.fromList(List.generate(16, (_) => random.nextInt(256))),
    );
  }

  final Uint8List _nonce;
  BigInt _sequence = BigInt.zero;

  List<List<int>?> fillMissing(List<List<int>?> supplied) {
    if (!supplied.any((value) => value == null)) return supplied;
    _sequence += BigInt.one;
    return List<List<int>?>.generate(supplied.length, (index) {
      final existing = supplied[index];
      return existing ??
          contributionIdFrom(nonce: _nonce, sequence: _sequence, index: index);
    }, growable: false);
  }
}

final class _NormalizedRetryPolicy {
  _NormalizedRetryPolicy({
    required this.maxAttempts,
    required this.baseDelay,
    required this.maxDelay,
    required this.retryResourceExhausted,
  });

  final int maxAttempts;
  final Duration baseDelay;
  final Duration maxDelay;
  final bool retryResourceExhausted;
  final Random _random = Random();

  bool retryable(LanternException error) {
    if (error.code == LanternCode.canceled ||
        error.code == LanternCode.deadlineExceeded) {
      return false;
    }
    return error.code == LanternCode.unavailable ||
        (retryResourceExhausted && error.code == LanternCode.resourceExhausted);
  }

  Duration delay(int completedAttempts) {
    var ceilingMicros = baseDelay.inMicroseconds;
    for (var i = 1; i < completedAttempts; i++) {
      if (ceilingMicros >= maxDelay.inMicroseconds ~/ 2) {
        ceilingMicros = maxDelay.inMicroseconds;
        break;
      }
      ceilingMicros *= 2;
    }
    if (ceilingMicros > maxDelay.inMicroseconds) {
      ceilingMicros = maxDelay.inMicroseconds;
    }
    final jittered = (_random.nextDouble() * ceilingMicros).floor();
    return Duration(microseconds: jittered);
  }
}

extension _RetryClient on LanternClient {
  LanternCallOptions? _freezeCallOptions(LanternCallOptions? options) {
    final deadline = options?.deadline;
    if (deadline != null) {
      return LanternCallOptions(
        deadline: deadline,
        cancellation: options?.cancellation,
      );
    }
    final timeout = options?.timeout ?? _defaultTimeout;
    if (timeout == null) {
      return options?.cancellation == null
          ? null
          : LanternCallOptions(cancellation: options?.cancellation);
    }
    return LanternCallOptions(
      deadline: DateTime.now().add(timeout),
      cancellation: options?.cancellation,
    );
  }

  Future<T> _runWithRetry<T>({
    required String method,
    required bool additiveSafe,
    required LanternCallOptions? options,
    required Future<T> Function() attempt,
  }) async {
    final policy = _retryPolicy;
    if (policy == null ||
        !RetryRegistry._allows(method, additiveSafe: additiveSafe)) {
      return attempt();
    }

    LanternException? lastError;
    for (
      var attemptIndex = 0;
      attemptIndex < policy.maxAttempts;
      attemptIndex++
    ) {
      if (attemptIndex > 0) {
        await _waitForRetry(policy.delay(attemptIndex), options);
      }
      try {
        return await attempt();
      } on LanternException catch (error) {
        if (!policy.retryable(error)) rethrow;
        lastError = error;
      }
    }
    throw LanternRetryExhaustedException(
      attempts: policy.maxAttempts,
      cause: lastError!,
    );
  }
}

Future<void> _waitForRetry(Duration delay, LanternCallOptions? options) async {
  final context = _InvocationContext(options, null);
  try {
    await _raceAbort(Future<void>.delayed(delay), context.signal);
  } on connect.ConnectException catch (error) {
    throw _mapConnectException(error, context);
  } finally {
    context.dispose();
  }
}
