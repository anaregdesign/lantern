import 'dart:async';
import 'dart:collection';
import 'dart:convert';
import 'dart:io' as io;
import 'dart:math';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/io.dart' as connect_io;
import 'package:connectrpc/protobuf.dart';
import 'package:connectrpc/protocol/connect.dart' as protocol;
import 'package:fixnum/fixnum.dart';

import 'gen/google/protobuf/duration.pb.dart' as $duration;
import 'gen/google/protobuf/timestamp.pb.dart' as $timestamp;
import 'gen/graph/v1/graph.connect.client.dart' as $client;
import 'gen/graph/v1/graph.pb.dart' as $graph;

part 'crud.dart';
part 'data.dart';
part 'decay.dart';
part 'retry.dart';
part 'scan.dart';
part 'search.dart';
part 'status.dart';
part 'traversal.dart';

/// Supplies the bearer token for one transport attempt.
///
/// The provider is invoked for every unary attempt (including retries) and
/// stream subscription. Returning `null` or an empty string sends no
/// authorization header.
typedef TokenProvider = FutureOr<String?> Function();

/// Supplies wall-clock time for relative expiration calculation.
typedef LanternClock = DateTime Function();

/// Creates a native Dart HTTP client owned by [LanternClient].
typedef LanternHttpClientFactory = io.HttpClient Function();

/// The low-level Connect transport accepted for advanced injection.
typedef LanternTransport = connect.Transport;

/// A Connect interceptor accepted by the default or factory transport.
typedef LanternInterceptor = connect.Interceptor;

/// Creates a Connect transport for a normalized endpoint.
typedef LanternTransportFactory =
    LanternTransport Function(
      Uri endpoint,
      List<LanternInterceptor> interceptors,
    );

/// Releases resources owned by an injected transport.
typedef LanternCloseCallback = FutureOr<void> Function();

/// Caller-controlled cancellation shared by unary and streaming RPCs.
final class LanternCancellationToken {
  /// Creates a caller-controlled cancellation token.
  LanternCancellationToken();

  final Set<void Function(Object?)> _listeners = {};
  bool _isCanceled = false;
  Object? _reason;

  /// Whether [cancel] has already been called.
  bool get isCanceled => _isCanceled;

  /// Cancels every active call using this token.
  ///
  /// The optional [reason] is retained as the exception cause but is never
  /// included in its text. Repeated calls are no-ops.
  void cancel([Object? reason]) {
    if (_isCanceled) return;
    _isCanceled = true;
    _reason = reason;
    final listeners = _listeners.toList(growable: false);
    _listeners.clear();
    final failures = <({Object error, StackTrace stackTrace})>[];
    for (final listener in listeners) {
      try {
        listener(reason);
      } catch (error, stackTrace) {
        failures.add((error: error, stackTrace: stackTrace));
      }
    }
    final zone = Zone.current;
    for (final failure in failures) {
      scheduleMicrotask(
        () => zone.handleUncaughtError(failure.error, failure.stackTrace),
      );
    }
  }

  /// Registers [listener] to run once when this token is canceled.
  ///
  /// The returned callback detaches the listener and is safe to call more than
  /// once. When this token is already canceled, delivery is scheduled in a
  /// microtask so callers can still detach before the callback runs. A listener
  /// failure is reported asynchronously after every listener has been offered
  /// the cancellation signal.
  void Function() listen(void Function(Object?) listener) {
    var detached = false;
    void notify(Object? reason) {
      if (detached) return;
      listener(reason);
    }

    if (_isCanceled) {
      scheduleMicrotask(() => notify(_reason));
    } else {
      _listeners.add(notify);
    }
    return () {
      if (detached) return;
      detached = true;
      _listeners.remove(notify);
    };
  }
}

/// Per-call deadline, cancellation, and retry overrides.
final class LanternCallOptions {
  /// Creates call options.
  ///
  /// [timeout] and [deadline] are mutually exclusive. A per-call value replaces
  /// the client's default timeout; cancellation always composes with it.
  LanternCallOptions({
    this.timeout,
    this.deadline,
    this.cancellation,
    this.retry = true,
  }) {
    if (timeout != null && deadline != null) {
      throw ArgumentError('timeout and deadline are mutually exclusive');
    }
    if (timeout != null && timeout! <= Duration.zero) {
      throw ArgumentError.value(timeout, 'timeout', 'must be positive');
    }
  }

  /// Relative timeout replacing the client default for this call.
  final Duration? timeout;

  /// Absolute deadline replacing the client default for this call.
  final DateTime? deadline;

  /// Caller-controlled cancellation token.
  final LanternCancellationToken? cancellation;

  /// Whether each RPC issued by this call may use the configured retry policy.
  ///
  /// Set this to false when a higher-level durable coordinator owns the retry
  /// budget. Every RPC is then attempted at most once; a chunked plural call
  /// may still issue multiple RPCs.
  final bool retry;
}

/// Stable SDK error categories suitable for mobile UI handling.
enum LanternCode {
  /// The caller canceled the operation.
  canceled,

  /// Input was invalid regardless of server state.
  invalidArgument,

  /// The configured deadline expired.
  deadlineExceeded,

  /// The requested entity was not found.
  notFound,

  /// The authenticated caller lacks permission.
  permissionDenied,

  /// A server or client resource limit was exhausted.
  resourceExhausted,

  /// An optimistic or endpoint-local continuation can no longer proceed.
  aborted,

  /// The operation is invalid in the current state.
  failedPrecondition,

  /// Authentication is missing or invalid.
  unauthenticated,

  /// The endpoint is temporarily unavailable.
  unavailable,

  /// An internal, unknown, or unmapped failure occurred.
  internal,
}

/// Base class for typed Lantern client failures.
sealed class LanternException implements Exception {
  LanternException._({
    required this.code,
    required this.transportCode,
    required this.transportCodeName,
    required this.message,
    required this.cause,
    required this.headers,
    required this.trailers,
    required this.metadata,
  });

  /// Stable SDK category.
  final LanternCode code;

  /// Original Connect/gRPC-compatible numeric status code.
  final int transportCode;

  /// Original Connect status name.
  final String transportCodeName;

  /// Credential-safe transport message.
  final String message;

  /// Underlying failure, when one exists.
  final Object? cause;

  /// Response headers observed before the failure.
  final Map<String, List<String>> headers;

  /// Response trailers observed before the failure.
  final Map<String, List<String>> trailers;

  /// Union metadata retained when headers and trailers cannot be distinguished.
  final Map<String, List<String>> metadata;

  @override
  String toString() =>
      message.isEmpty ? '[${code.name}]' : '[${code.name}] $message';
}

/// A caller-canceled operation.
final class LanternCanceledException extends LanternException {
  LanternCanceledException._(_ErrorData data)
    : super._(
        code: LanternCode.canceled,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );
}

/// An invalid request.
final class LanternInvalidArgumentException extends LanternException {
  LanternInvalidArgumentException._(_ErrorData data)
    : searchReason = data.searchReason,
      super._(
        code: LanternCode.invalidArgument,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );

  /// Bounded search reason, or unspecified for non-search validation errors.
  final SearchErrorReason searchReason;
}

/// An operation whose deadline expired.
final class LanternDeadlineExceededException extends LanternException {
  LanternDeadlineExceededException._(_ErrorData data)
    : super._(
        code: LanternCode.deadlineExceeded,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );
}

/// A missing entity.
final class LanternNotFoundException extends LanternException {
  LanternNotFoundException._(_ErrorData data)
    : super._(
        code: LanternCode.notFound,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );
}

/// A permission-denied operation.
final class LanternPermissionDeniedException extends LanternException {
  LanternPermissionDeniedException._(_ErrorData data)
    : super._(
        code: LanternCode.permissionDenied,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );
}

/// A resource-exhausted operation.
final class LanternResourceExhaustedException extends LanternException {
  LanternResourceExhaustedException._(_ErrorData data)
    : searchReason = data.searchReason,
      searchWorkKind = data.searchWorkKind,
      super._(
        code: LanternCode.resourceExhausted,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );

  /// Bounded search execution reason, or unspecified for other RPCs.
  final SearchErrorReason searchReason;

  /// Exhausted work counter name, or empty for admission/non-search errors.
  final String searchWorkKind;
}

/// An operation that was aborted because its continuation state changed.
final class LanternAbortedException extends LanternException {
  LanternAbortedException._(_ErrorData data)
    : searchReason = data.searchReason,
      super._(
        code: LanternCode.aborted,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );

  /// Bounded search reason, or unspecified for non-search aborted calls.
  final SearchErrorReason searchReason;
}

/// The endpoint-sticky search session expired or was evicted.
final class LanternSearchCursorStaleException extends LanternException {
  LanternSearchCursorStaleException._(_ErrorData data)
    : super._(
        code: LanternCode.aborted,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );

  /// Stable cursor failure reason.
  SearchErrorReason get searchReason => SearchErrorReason.searchCursorStale;
}

/// The server retained only a bounded prefix of one search snapshot.
final class LanternSearchContinuationLimitedException extends LanternException {
  LanternSearchContinuationLimitedException._()
    : super._(
        code: LanternCode.resourceExhausted,
        transportCode: connect.Code.resourceExhausted.value,
        transportCodeName: connect.Code.resourceExhausted.name,
        message: 'search continuation was limited by the server session cap',
        cause: null,
        headers: const {},
        trailers: const {},
        metadata: const {},
      );

  /// Stable bounded-tail reason.
  SearchErrorReason get searchReason =>
      SearchErrorReason.searchContinuationLimited;
}

/// An operation that failed a state precondition.
final class LanternFailedPreconditionException extends LanternException {
  LanternFailedPreconditionException._(_ErrorData data)
    : searchReason = data.searchReason,
      super._(
        code: LanternCode.failedPrecondition,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );

  /// Bounded search capability reason, or unspecified for other RPCs.
  final SearchErrorReason searchReason;
}

/// The health endpoint replied successfully but reported a non-serving state.
final class LanternHealthStatusException extends LanternException {
  LanternHealthStatusException._(this.status, _ErrorData data)
    : super._(
        code: LanternCode.failedPrecondition,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );

  /// Symbolic gRPC Health status returned by the server.
  final String status;
}

/// An unauthenticated operation.
final class LanternUnauthenticatedException extends LanternException {
  LanternUnauthenticatedException._(_ErrorData data)
    : super._(
        code: LanternCode.unauthenticated,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );
}

/// An unavailable endpoint.
final class LanternUnavailableException extends LanternException {
  LanternUnavailableException._(_ErrorData data)
    : super._(
        code: LanternCode.unavailable,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );
}

/// An internal, unknown, or unmapped transport failure.
final class LanternInternalException extends LanternException {
  LanternInternalException._(_ErrorData data)
    : super._(
        code: LanternCode.internal,
        transportCode: data.transportCode,
        transportCodeName: data.transportCodeName,
        message: data.message,
        cause: data.cause,
        headers: data.headers,
        trailers: data.trailers,
        metadata: data.metadata,
      );
}

/// Reusable Android/iOS-first Lantern client foundation.
final class LanternClient {
  LanternClient._({
    required this.endpoint,
    required LanternInvoker invoker,
    required LanternCloseCallback? closeCallback,
    required io.HttpClient? healthHttpClient,
    required Duration? defaultTimeout,
    required LanternClock clock,
    required RetryPolicy? retryPolicy,
    required bool idempotentAdds,
    required _ContribIdGenerator contribIds,
  }) : _invoker = invoker,
       _closeCallback = closeCallback,
       _healthHttpClient = healthHttpClient,
       _defaultTimeout = defaultTimeout,
       _clock = clock,
       _retryPolicy = retryPolicy?._normalized(),
       _idempotentAdds = idempotentAdds,
       _contribIds = contribIds;

  /// Creates a client for [endpoint].
  ///
  /// HTTPS is required unless [allowInsecure] is true. [token] is for trusted
  /// internal applications and tests; public mobile apps should use
  /// [tokenProvider] with short-lived user/device credentials.
  ///
  /// [transport], [transportFactory], and [httpClientFactory] are mutually
  /// exclusive. Interceptors cannot be added to an already-created [transport].
  /// A client created by [httpClientFactory] is owned and closed by this object.
  /// [clock] is sampled once per logical CRUD call that resolves relative TTLs.
  /// Supplying [retryPolicy] opts into bounded retry for explicitly classified
  /// operations. [idempotentAdds] stamps missing contribution IDs once per
  /// in-memory logical Add call, making only that call safe to replay. Automatic
  /// IDs are not persisted across process death; durable outboxes must persist
  /// caller-supplied 24-byte IDs with their intents.
  factory LanternClient.connect(
    Uri endpoint, {
    TokenProvider? tokenProvider,
    String? token,
    Duration? defaultTimeout = const Duration(seconds: 10),
    bool allowInsecure = false,
    LanternTransport? transport,
    LanternTransportFactory? transportFactory,
    LanternHttpClientFactory? httpClientFactory,
    Iterable<LanternInterceptor> interceptors = const [],
    LanternCloseCallback? onClose,
    LanternClock? clock,
    RetryPolicy? retryPolicy,
    bool idempotentAdds = false,
  }) {
    final normalized = _normalizeEndpoint(
      endpoint,
      allowInsecure: allowInsecure,
    );
    if (tokenProvider != null && token != null) {
      throw ArgumentError('token and tokenProvider are mutually exclusive');
    }
    if (token != null && token.isEmpty) {
      throw ArgumentError.value(token, 'token', 'must not be empty');
    }
    if (defaultTimeout != null && defaultTimeout <= Duration.zero) {
      throw ArgumentError.value(
        defaultTimeout,
        'defaultTimeout',
        'must be positive',
      );
    }
    final injectionCount = [
      transport != null,
      transportFactory != null,
      httpClientFactory != null,
    ].where((value) => value).length;
    if (injectionCount > 1) {
      throw ArgumentError(
        'transport, transportFactory, and httpClientFactory are mutually exclusive',
      );
    }
    final interceptorList = List<LanternInterceptor>.unmodifiable(interceptors);
    if (transport != null && interceptorList.isNotEmpty) {
      throw ArgumentError(
        'interceptors must be configured by an injected transport',
      );
    }

    io.HttpClient? ownedHttpClient;
    io.HttpClient? healthHttpClient;
    final resolvedTransport = switch ((transport, transportFactory)) {
      (final injected?, _) => injected,
      (_, final factory?) => factory(normalized, interceptorList),
      _ => () {
        ownedHttpClient = httpClientFactory?.call() ?? io.HttpClient();
        healthHttpClient = ownedHttpClient;
        return protocol.Transport(
          baseUrl: normalized.toString(),
          codec: const ProtoCodec(),
          httpClient: connect_io.createHttpClient(ownedHttpClient!),
          interceptors: interceptorList,
        );
      }(),
    };
    final provider = tokenProvider ?? (token == null ? null : () => token);
    return LanternClient._(
      endpoint: normalized,
      invoker: LanternInvoker(
        transport: resolvedTransport,
        tokenProvider: provider,
        defaultTimeout: defaultTimeout,
        unknownIsUnavailable: transport == null && transportFactory == null,
      ),
      closeCallback: () async {
        ownedHttpClient?.close(force: true);
        await onClose?.call();
      },
      healthHttpClient: healthHttpClient,
      defaultTimeout: defaultTimeout,
      clock: clock ?? DateTime.now,
      retryPolicy: retryPolicy,
      idempotentAdds: idempotentAdds,
      contribIds: _ContribIdGenerator.secure(),
    );
  }

  /// Normalized endpoint used by this client.
  final Uri endpoint;

  // Retained for the CRUD/query facades that build on this foundation.
  // ignore: unused_field
  final LanternInvoker _invoker;
  final LanternCloseCallback? _closeCallback;
  final io.HttpClient? _healthHttpClient;
  final Duration? _defaultTimeout;
  final LanternClock _clock;
  final _NormalizedRetryPolicy? _retryPolicy;
  final bool _idempotentAdds;
  final _ContribIdGenerator _contribIds;
  Future<void>? _closing;

  /// Whether shutdown has started.
  bool get isClosed => _closing != null;

  /// Probes the gRPC Health-v1 endpoint and resolves only when it is serving.
  ///
  /// Health checks intentionally do not send the configured bearer token: the
  /// server's health surface is auth-exempt and this keeps readiness probes
  /// safe to use before application credentials are available. A client built
  /// around an injected transport has no raw HTTP path and must use its own
  /// health probe; calling [ping] on it returns [LanternInvalidArgumentException].
  Future<void> ping({LanternCallOptions? options}) async {
    _ensureOpen();
    final httpClient = _healthHttpClient;
    if (httpClient == null) {
      throw _invalidArgumentException(
        'ping requires a URL-backed client; provide a raw HTTP health probe',
      );
    }
    final context = _InvocationContext(options, _defaultTimeout);
    io.HttpClientRequest? request;
    try {
      final activeRequest = await _raceAbort(
        httpClient.postUrl(_healthEndpoint(endpoint)),
        context.signal,
      );
      request = activeRequest;
      activeRequest.headers.contentType = io.ContentType.json;
      activeRequest.headers.set('Connect-Protocol-Version', '1');
      activeRequest.add(utf8.encode('{}'));
      final response = await _raceAbort(
        activeRequest.close(),
        context.signal,
        onAbort: (error) => request?.abort(error),
      );
      context.onHeader(_connectHeadersFromIo(response.headers));
      final body = await _raceAbort(
        response.transform(utf8.decoder).join(),
        context.signal,
        onAbort: (error) => request?.abort(error),
      );
      if (response.statusCode != io.HttpStatus.ok) {
        throw connect.ConnectException(
          _codeForHttpStatus(response.statusCode),
          'health probe returned HTTP ${response.statusCode}',
        );
      }
      final decoded = jsonDecode(body);
      if (decoded is! Map || decoded['status'] is! String) {
        throw connect.ConnectException(
          connect.Code.internal,
          'health response missing status',
        );
      }
      final status = decoded['status'] as String;
      if (!_isServingStatus(status)) {
        throw LanternHealthStatusException._(
          status,
          _ErrorData(
            transportCode: connect.Code.failedPrecondition.value,
            transportCodeName: connect.Code.failedPrecondition.name,
            message:
                'server health status = ${status.isEmpty ? '(empty)' : status}',
            headers: _headersToMap(context.headers),
            trailers: _headersToMap(context.trailers),
            metadata: const {},
          ),
        );
      }
    } on connect.ConnectException catch (error) {
      throw _mapConnectException(error, context);
    } on LanternException {
      rethrow;
    } catch (error) {
      throw _mapUnknownException(error, context);
    } finally {
      context.dispose();
    }
  }

  /// Closes owned networking resources exactly once.
  Future<void> close() => _closing ??= _closeOnce();

  Future<void> _closeOnce() async => _closeCallback?.call();

  void _ensureOpen() {
    if (isClosed) throw _closedException();
  }
}

/// Internal shared invocation engine used by every Dart facade method.
///
/// This class lives under `lib/src` and is not exported by the package barrel.
final class LanternInvoker {
  /// Creates an invocation engine around [transport].
  LanternInvoker({
    required this.transport,
    TokenProvider? tokenProvider,
    Duration? defaultTimeout,
    bool unknownIsUnavailable = false,
  }) : _tokenProvider = tokenProvider,
       _defaultTimeout = defaultTimeout,
       _unknownIsUnavailable = unknownIsUnavailable;

  /// Low-level transport used by generated clients.
  final LanternTransport transport;

  final TokenProvider? _tokenProvider;
  final Duration? _defaultTimeout;
  final bool _unknownIsUnavailable;

  /// Invokes a unary generated-client method with common policy.
  Future<T> invokeUnary<T>({
    required LanternUnaryCall<T> call,
    LanternCallOptions? options,
  }) async {
    final context = _InvocationContext(options, _defaultTimeout);
    try {
      final headers = await _requestHeaders(context);
      return await call(
        headers: headers,
        signal: context.signal,
        onHeader: context.onHeader,
        onTrailer: context.onTrailer,
      );
    } on connect.ConnectException catch (error) {
      throw _mapConnectException(error, context);
    } on LanternException {
      rethrow;
    } catch (error) {
      throw _mapUnknownException(
        error,
        context,
        unknownIsUnavailable: _unknownIsUnavailable,
      );
    } finally {
      context.dispose();
    }
  }

  /// Invokes a streaming generated-client method with common policy.
  ///
  /// Canceling the returned subscription cancels the underlying RPC signal.
  Stream<T> invokeStream<T>({
    required LanternStreamCall<T> call,
    LanternCallOptions? options,
  }) {
    _InvocationContext? context;
    late final StreamController<T> controller;
    StreamSubscription<T>? subscription;

    // Keep the mapping/finally policy in an async* body, but put a controller
    // in front of it so cancellation of the public subscription is observable
    // immediately.  Dart async* cancellation otherwise waits for the current
    // await (which can leave a network read alive until its timeout). Create
    // the context on listen so an un-listened stream owns no timer or token
    // listener.
    controller = StreamController<T>(
      sync: true,
      onListen: () {
        final invocation = context = _InvocationContext(
          options,
          _defaultTimeout,
        );
        final body = _invokeStreamBody(call, invocation);
        subscription = body.listen(
          controller.add,
          onError: (Object error, StackTrace stack) {
            controller.addError(error, stack);
          },
          onDone: controller.close,
        );
      },
      onCancel: () {
        final invocation = context;
        invocation?.signal.cancel();
        invocation?.dispose();
        final active = subscription;
        if (active != null) {
          // Do not make the caller wait for a non-cooperative injected stream
          // to finish.  The signal is already canceled, and Connect's native
          // transport observes it to abort the underlying request.
          unawaited(active.cancel());
        }
      },
    );
    return controller.stream;
  }

  Stream<T> _invokeStreamBody<T>(
    LanternStreamCall<T> call,
    _InvocationContext context,
  ) async* {
    try {
      final headers = await _requestHeaders(context);
      yield* call(
        headers: headers,
        signal: context.signal,
        onHeader: context.onHeader,
        onTrailer: context.onTrailer,
      );
    } on connect.ConnectException catch (error) {
      throw _mapConnectException(error, context);
    } on LanternException {
      rethrow;
    } catch (error) {
      throw _mapUnknownException(
        error,
        context,
        unknownIsUnavailable: _unknownIsUnavailable,
      );
    } finally {
      context.signal.cancel();
      context.dispose();
    }
  }

  Future<connect.Headers> _requestHeaders(_InvocationContext context) async {
    final headers = connect.Headers();
    final provider = _tokenProvider;
    if (provider == null) return headers;
    final signal = context.signal;
    String? token;
    try {
      token = (await Future.any<_TokenProviderResult>([
        Future<String?>.sync(provider).then(_TokenProviderResult.value),
        signal.future.then<_TokenProviderResult>(
          (error) => throw _SignalAbort(error),
        ),
      ])).token;
    } on _SignalAbort catch (error) {
      // Preserve cancellation/deadline status from our signal. Provider
      // failures are handled below and never expose provider-supplied text.
      throw error.exception;
    } catch (error) {
      // A token provider is application code and may throw a ConnectException
      // whose message contains credential material. Keep the public message
      // fixed while retaining the original exception as a diagnostic cause.
      throw connect.ConnectException(
        connect.Code.unknown,
        'token provider failed',
        cause: error,
      );
    }
    if (token != null && token.isNotEmpty) {
      context.token = token;
      headers['authorization'] = 'Bearer $token';
    }
    return headers;
  }
}

/// Result wrapper used to distinguish a token provider's failure from the
/// caller-controlled signal in the race above.
final class _TokenProviderResult {
  const _TokenProviderResult.value(this.token);

  final String? token;
}

/// Internal marker preserving cancellation/deadline errors from [_CallSignal]
/// without allowing provider-thrown [connect.ConnectException] values to leak
/// their messages through the public SDK error.
final class _SignalAbort {
  const _SignalAbort(this.exception);

  final connect.ConnectException exception;
}

/// Callback shape for a unary generated-client method.
typedef LanternUnaryCall<T> =
    Future<T> Function({
      required connect.Headers headers,
      required connect.AbortSignal signal,
      required void Function(connect.Headers) onHeader,
      required void Function(connect.Headers) onTrailer,
    });

/// Callback shape for a streaming generated-client method.
typedef LanternStreamCall<T> =
    Stream<T> Function({
      required connect.Headers headers,
      required connect.AbortSignal signal,
      required void Function(connect.Headers) onHeader,
      required void Function(connect.Headers) onTrailer,
    });

final class _InvocationContext {
  _InvocationContext(LanternCallOptions? options, Duration? defaultTimeout)
    : signal = _CallSignal(
        timeout:
            options?.timeout ??
            (options?.deadline == null ? defaultTimeout : null),
        deadline: options?.deadline,
        cancellation: options?.cancellation,
      );

  final _CallSignal signal;
  connect.Headers headers = connect.Headers();
  connect.Headers trailers = connect.Headers();
  String? token;

  void onHeader(connect.Headers value) => headers = connect.Headers.from(value);

  void onTrailer(connect.Headers value) =>
      trailers = connect.Headers.from(value);

  void dispose() => signal.dispose();
}

final class _CallSignal implements connect.AbortSignal {
  _CallSignal({
    Duration? timeout,
    DateTime? deadline,
    LanternCancellationToken? cancellation,
  }) {
    final now = DateTime.now();
    final requestedDeadline =
        deadline ?? (timeout == null ? null : now.add(timeout));
    this.deadline = requestedDeadline;
    if (requestedDeadline != null) {
      final remaining = requestedDeadline.difference(now);
      if (remaining <= Duration.zero) {
        scheduleMicrotask(_expire);
      } else {
        _timer = Timer(remaining, _expire);
      }
    }
    _removeCancellationListener = cancellation?.listen(_cancelFromCaller);
  }

  final Completer<connect.ConnectException> _completer = Completer();
  Timer? _timer;
  void Function()? _removeCancellationListener;

  @override
  late final DateTime? deadline;

  @override
  Future<connect.ConnectException> get future => _completer.future;

  void cancel([Object? reason]) {
    _complete(
      connect.ConnectException(
        connect.Code.canceled,
        'operation canceled',
        cause: reason,
      ),
    );
  }

  void _cancelFromCaller(Object? reason) => cancel(reason);

  void _expire() {
    _complete(
      connect.ConnectException(
        connect.Code.deadlineExceeded,
        'operation exceeded deadline',
      ),
    );
  }

  void _complete(connect.ConnectException error) {
    if (!_completer.isCompleted) _completer.complete(error);
  }

  void dispose() {
    _timer?.cancel();
    _removeCancellationListener?.call();
    _removeCancellationListener = null;
  }
}

Uri _normalizeEndpoint(Uri endpoint, {required bool allowInsecure}) {
  final scheme = endpoint.scheme.toLowerCase();
  if (!endpoint.hasScheme || endpoint.host.isEmpty) {
    throw ArgumentError.value(endpoint, 'endpoint', 'must be an absolute URI');
  }
  if (scheme != 'https' && scheme != 'http') {
    throw ArgumentError.value(endpoint, 'endpoint', 'must use https or http');
  }
  if (scheme == 'http' && !allowInsecure) {
    throw ArgumentError.value(
      endpoint,
      'endpoint',
      'plaintext requires allowInsecure: true',
    );
  }
  if (endpoint.userInfo.isNotEmpty ||
      endpoint.hasQuery ||
      endpoint.hasFragment) {
    throw ArgumentError.value(
      endpoint,
      'endpoint',
      'userinfo, query, and fragment are not supported',
    );
  }
  final normalized = endpoint.normalizePath();
  final path = normalized.path.replaceFirst(RegExp(r'/+$'), '');
  return normalized.replace(scheme: scheme, path: path);
}

Uri _healthEndpoint(Uri endpoint) {
  final path = endpoint.path.isEmpty ? '' : endpoint.path;
  return endpoint.replace(path: '$path/grpc.health.v1.Health/Check');
}

bool _isServingStatus(String status) =>
    status == 'SERVING' || status == 'SERVING_STATUS_SERVING';

connect.Code _codeForHttpStatus(int statusCode) => switch (statusCode) {
  400 => connect.Code.invalidArgument,
  401 => connect.Code.unauthenticated,
  403 => connect.Code.permissionDenied,
  404 => connect.Code.notFound,
  408 => connect.Code.deadlineExceeded,
  409 => connect.Code.aborted,
  429 => connect.Code.resourceExhausted,
  >= 500 && < 600 => connect.Code.unavailable,
  _ => connect.Code.unknown,
};

connect.Headers _connectHeadersFromIo(io.HttpHeaders source) {
  final headers = connect.Headers();
  source.forEach((name, values) {
    for (final value in values) {
      headers.add(name, value);
    }
  });
  return headers;
}

Future<T> _raceAbort<T>(
  Future<T> operation,
  _CallSignal signal, {
  void Function(Object reason)? onAbort,
}) {
  final abort = signal.future.then<T>((error) {
    onAbort?.call(error);
    throw error;
  });
  return Future.any<T>([operation, abort]);
}

LanternException _invalidArgumentException(String message) {
  return LanternInvalidArgumentException._(
    _ErrorData(
      transportCode: connect.Code.invalidArgument.value,
      transportCodeName: connect.Code.invalidArgument.name,
      message: message,
      headers: const {},
      trailers: const {},
      metadata: const {},
    ),
  );
}

LanternException _closedException() {
  return LanternFailedPreconditionException._(
    _ErrorData(
      transportCode: connect.Code.failedPrecondition.value,
      transportCodeName: connect.Code.failedPrecondition.name,
      message: 'client is closed',
      headers: const {},
      trailers: const {},
      metadata: const {},
    ),
  );
}

LanternException _mapConnectException(
  connect.ConnectException error,
  _InvocationContext context,
) {
  final searchError = _searchErrorDetail(error);
  final data = _ErrorData(
    transportCode: error.code.value,
    transportCodeName: error.code.name,
    message: _redactSecret(error.message, context.token),
    cause: error.cause,
    headers: _headersToMap(context.headers, context.token),
    trailers: _headersToMap(context.trailers, context.token),
    metadata: _headersToMap(error.metadata, context.token),
    searchReason: searchError.reason,
    searchWorkKind: searchError.workKind,
  );
  return switch (error.code) {
    connect.Code.canceled => LanternCanceledException._(data),
    connect.Code.invalidArgument => LanternInvalidArgumentException._(data),
    connect.Code.deadlineExceeded => LanternDeadlineExceededException._(data),
    connect.Code.notFound => LanternNotFoundException._(data),
    connect.Code.permissionDenied => LanternPermissionDeniedException._(data),
    connect.Code.resourceExhausted => LanternResourceExhaustedException._(data),
    connect.Code.aborted =>
      searchError.reason == SearchErrorReason.searchCursorStale
          ? LanternSearchCursorStaleException._(data)
          : LanternAbortedException._(data),
    connect.Code.failedPrecondition => LanternFailedPreconditionException._(
      data,
    ),
    connect.Code.unauthenticated => LanternUnauthenticatedException._(data),
    connect.Code.unavailable => LanternUnavailableException._(data),
    _ => LanternInternalException._(data),
  };
}

({SearchErrorReason reason, String workKind}) _searchErrorDetail(
  connect.ConnectException error,
) {
  for (final detail in error.details) {
    if (detail.type != 'graph.v1.SearchErrorDetail') continue;
    try {
      final decoded = $graph.SearchErrorDetail.fromBuffer(detail.value);
      final reason = switch (decoded.reason) {
        $graph.SearchErrorReason.SEARCH_DISABLED =>
          SearchErrorReason.searchDisabled,
        $graph.SearchErrorReason.SEARCH_POSITIONS_DISABLED =>
          SearchErrorReason.searchPositionsDisabled,
        $graph.SearchErrorReason.SEARCH_WORK_BUDGET_EXHAUSTED =>
          SearchErrorReason.searchWorkBudgetExhausted,
        $graph.SearchErrorReason.SEARCH_ADMISSION_SATURATED =>
          SearchErrorReason.searchAdmissionSaturated,
        $graph.SearchErrorReason.SEARCH_INDEX_INCOMPLETE =>
          SearchErrorReason.searchIndexIncomplete,
        $graph.SearchErrorReason.SEARCH_INDEX_BUDGET_EXHAUSTED =>
          SearchErrorReason.searchIndexBudgetExhausted,
        $graph.SearchErrorReason.SEARCH_CURSOR_STALE =>
          SearchErrorReason.searchCursorStale,
        $graph.SearchErrorReason.SEARCH_CURSOR_INVALID =>
          SearchErrorReason.searchCursorInvalid,
        $graph.SearchErrorReason.SEARCH_CONTINUATION_LIMITED =>
          SearchErrorReason.searchContinuationLimited,
        _ => SearchErrorReason.unspecified,
      };
      return (reason: reason, workKind: decoded.workKind);
    } on Object {
      continue;
    }
  }
  return (reason: SearchErrorReason.unspecified, workKind: '');
}

LanternException _mapUnknownException(
  Object error,
  _InvocationContext context, {
  bool unknownIsUnavailable = false,
}) {
  final unavailable = unknownIsUnavailable || error is io.IOException;
  final code = unavailable ? connect.Code.unavailable : connect.Code.unknown;
  final data = _ErrorData(
    transportCode: code.value,
    transportCodeName: code.name,
    message: 'transport failed',
    cause: error,
    headers: _headersToMap(context.headers, context.token),
    trailers: _headersToMap(context.trailers, context.token),
    metadata: const {},
  );
  return unavailable
      ? LanternUnavailableException._(data)
      : LanternInternalException._(data);
}

Map<String, List<String>> _headersToMap(
  connect.Headers headers, [
  String? secret,
]) {
  final values = <String, List<String>>{};
  for (final entry in headers.entries) {
    values
        .putIfAbsent(entry.name, () => [])
        .add(_redactSecret(entry.value, secret));
  }
  return Map.unmodifiable(
    values.map(
      (name, items) => MapEntry(name, List<String>.unmodifiable(items)),
    ),
  );
}

String _redactSecret(String value, String? secret) {
  if (secret == null || secret.isEmpty) return value;
  return value.replaceAll(secret, '[REDACTED]');
}

final class _ErrorData {
  const _ErrorData({
    required this.transportCode,
    required this.transportCodeName,
    required this.message,
    this.cause,
    required this.headers,
    required this.trailers,
    required this.metadata,
    this.searchReason = SearchErrorReason.unspecified,
    this.searchWorkKind = '',
  });

  final int transportCode;
  final String transportCodeName;
  final String message;
  final Object? cause;
  final Map<String, List<String>> headers;
  final Map<String, List<String>> trailers;
  final Map<String, List<String>> metadata;
  final SearchErrorReason searchReason;
  final String searchWorkKind;
}
