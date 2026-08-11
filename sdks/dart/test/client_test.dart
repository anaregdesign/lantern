import 'dart:async';
import 'dart:convert';
import 'dart:io' as io;

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/src/client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart';
import 'package:test/test.dart';

void main() {
  test('cancellation listeners fire once and detach idempotently', () {
    final cancellation = LanternCancellationToken();
    final retainedReason = Object();
    final reasons = <Object?>[];
    final detachedReasons = <Object?>[];
    final remove = cancellation.listen(reasons.add);
    final removeDetached = cancellation.listen(detachedReasons.add);

    removeDetached();
    removeDetached();
    cancellation.cancel(retainedReason);
    cancellation.cancel(Object());
    remove();
    remove();

    expect(reasons, hasLength(1));
    expect(reasons.single, same(retainedReason));
    expect(detachedReasons, isEmpty);
  });

  test('a throwing cancellation listener cannot poison fan-out', () async {
    final cancellation = LanternCancellationToken();
    final reason = Object();
    final delivered = <Object?>[];
    final reported = Completer<Object>();

    runZonedGuarded(
      () {
        cancellation.listen((_) => throw StateError('listener failed'));
        cancellation.listen(delivered.add);
        cancellation.cancel(reason);
      },
      (error, _) {
        if (!reported.isCompleted) reported.complete(error);
      },
    );

    expect(delivered, <Object?>[reason]);
    expect(await reported.future, isA<StateError>());
  });

  test('pre-canceled listeners run in a detachable microtask', () async {
    final cancellation = LanternCancellationToken();
    final retainedReason = Object();
    cancellation.cancel(retainedReason);
    var synchronous = true;
    final reasons = <Object?>[];
    final detachedReasons = <Object?>[];
    cancellation.listen((reason) {
      expect(synchronous, isFalse);
      reasons.add(reason);
    });
    final removeDetached = cancellation.listen(detachedReasons.add);
    removeDetached();
    removeDetached();

    synchronous = false;
    await Future<void>.delayed(Duration.zero);

    expect(reasons, hasLength(1));
    expect(reasons.single, same(retainedReason));
    expect(detachedReasons, isEmpty);
  });

  test('health ping uses auth-exempt Connect JSON and maps status', () async {
    final requests = <io.HttpRequest>[];
    final server = await io.HttpServer.bind('127.0.0.1', 0);
    addTearDown(() => server.close(force: true));
    server.listen((request) async {
      requests.add(request);
      await utf8.decoder.bind(request).join();
      request.response.headers.contentType = io.ContentType.json;
      request.response.write(jsonEncode({'status': 'SERVING_STATUS_SERVING'}));
      await request.response.close();
    });

    final client = LanternClient.connect(
      Uri.parse('http://127.0.0.1:${server.port}'),
      allowInsecure: true,
      token: 'must-not-be-sent-to-health',
    );
    await client.ping();
    expect(requests, hasLength(1));
    expect(requests.single.method, 'POST');
    expect(requests.single.uri.path, '/grpc.health.v1.Health/Check');
    expect(requests.single.headers.value('content-type'), contains('json'));
    expect(requests.single.headers.value('connect-protocol-version'), '1');
    expect(requests.single.headers.value('authorization'), isNull);
    await client.close();
  });

  test('health ping preserves non-serving status', () async {
    final server = await io.HttpServer.bind('127.0.0.1', 0);
    addTearDown(() => server.close(force: true));
    server.listen((request) async {
      request.response.headers.contentType = io.ContentType.json;
      request.response.write(jsonEncode({'status': 'NOT_SERVING'}));
      await request.response.close();
    });

    final client = LanternClient.connect(
      Uri.parse('http://127.0.0.1:${server.port}'),
      allowInsecure: true,
    );
    await expectLater(
      client.ping(),
      throwsA(
        isA<LanternHealthStatusException>().having(
          (error) => error.status,
          'status',
          'NOT_SERVING',
        ),
      ),
    );
    await client.close();
  });

  test('normalizes secure endpoints and rejects unsafe forms', () {
    final client = LanternClient.connect(
      Uri.parse('HTTPS://example.test/base/'),
      transport: FakeTransportBuilder().build(),
    );
    expect(client.endpoint, Uri.parse('https://example.test/base'));

    expect(
      () => LanternClient.connect(
        Uri.parse('http://example.test'),
        transport: FakeTransportBuilder().build(),
      ),
      throwsArgumentError,
    );
    expect(
      () => LanternClient.connect(
        Uri.parse('https://example.test?token=secret'),
        transport: FakeTransportBuilder().build(),
      ),
      throwsArgumentError,
    );
    expect(
      () => LanternClient.connect(
        Uri.parse('http://example.test'),
        allowInsecure: true,
        transport: FakeTransportBuilder().build(),
        transportFactory: (_, _) => FakeTransportBuilder().build(),
      ),
      throwsArgumentError,
    );
    expect(
      () => LanternCallOptions(
        timeout: const Duration(seconds: 1),
        deadline: DateTime.now().add(const Duration(seconds: 1)),
      ),
      throwsArgumentError,
    );
  });

  test('sends a fresh bearer token for every unary call', () async {
    var tokenCalls = 0;
    final transport = FakeTransportBuilder()
        .unary<GetServerStatusRequest, GetServerStatusResponse>(
          LanternService.getServerStatus,
          (request, FakeHandlerContext context) {
            expect(
              context.requestHeaders['authorization'],
              'Bearer token-$tokenCalls',
            );
            context.responseHeaders.add('x-request-id', 'request-1');
            context.responseTrailers.add('x-server', 'lantern');
            return GetServerStatusResponse(
              version: 'test',
              goVersion: 'go1.test',
              tlsEnabled: true,
              replicationEnabled: false,
              vertexCount: Int64(7),
              edgeCount: Int64(11),
            );
          },
        )
        .build();
    final invoker = LanternInvoker(
      tokenProvider: () => 'token-${++tokenCalls}',
      transport: transport,
      defaultTimeout: const Duration(seconds: 1),
    );
    final raw = LanternServiceClient(invoker.transport);

    Future<GetServerStatusResponse> call() => invoker.invokeUnary(
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.getServerStatus(
            GetServerStatusRequest(),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    expect((await call()).version, 'test');
    expect((await call()).version, 'test');
    expect(tokenCalls, 2);
  });

  test('maps transport failures without exposing token metadata', () async {
    final transport = FakeTransportBuilder()
        .unary<GetServerStatusRequest, GetServerStatusResponse>(
          LanternService.getServerStatus,
          (request, FakeHandlerContext context) =>
              throw connect.ConnectException(
                connect.Code.unauthenticated,
                'credentials rejected',
                metadata: connect.Headers()..add('www-authenticate', 'Bearer'),
              ),
        )
        .build();
    final invoker = LanternInvoker(
      tokenProvider: () => 'do-not-log-me',
      transport: transport,
    );
    final raw = LanternServiceClient(invoker.transport);

    final error = await expectLater(
      invoker.invokeUnary(
        call:
            ({
              required headers,
              required signal,
              required onHeader,
              required onTrailer,
            }) => raw.getServerStatus(
              GetServerStatusRequest(),
              headers: headers,
              signal: signal,
              onHeader: onHeader,
              onTrailer: onTrailer,
            ),
      ),
      throwsA(
        isA<LanternUnauthenticatedException>()
            .having((value) => value.code, 'code', LanternCode.unauthenticated)
            .having((value) => value.message, 'message', 'credentials rejected')
            .having((value) => value.metadata['www-authenticate'], 'metadata', [
              'Bearer',
            ]),
      ),
    );
    expect(error, isNull);
  });

  test('deadline and caller cancellation map to typed failures', () async {
    final pending = Completer<GetServerStatusResponse>();
    final transport = FakeTransportBuilder()
        .unary<GetServerStatusRequest, GetServerStatusResponse>(
          LanternService.getServerStatus,
          (request, FakeHandlerContext context) async {
            await Future.any<GetServerStatusResponse>([
              pending.future,
              context.signal.future.then<GetServerStatusResponse>(
                (connect.ConnectException error) => throw error,
              ),
            ]);
            return pending.future;
          },
        )
        .build();
    final invoker = LanternInvoker(
      transport: transport,
      defaultTimeout: const Duration(milliseconds: 1),
    );
    final raw = LanternServiceClient(invoker.transport);
    Future<GetServerStatusResponse> call(LanternCallOptions? options) =>
        invoker.invokeUnary(
          options: options,
          call:
              ({
                required headers,
                required signal,
                required onHeader,
                required onTrailer,
              }) => raw.getServerStatus(
                GetServerStatusRequest(),
                headers: headers,
                signal: signal,
                onHeader: onHeader,
                onTrailer: onTrailer,
              ),
        );

    await expectLater(
      call(null),
      throwsA(isA<LanternDeadlineExceededException>()),
    );

    final cancellation = LanternCancellationToken();
    final uncanceledInvoker = LanternInvoker(
      transport: transport,
      defaultTimeout: null,
    );
    final uncanceledRaw = LanternServiceClient(uncanceledInvoker.transport);
    final canceledCall = uncanceledInvoker.invokeUnary(
      options: LanternCallOptions(cancellation: cancellation),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => uncanceledRaw.getServerStatus(
            GetServerStatusRequest(),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    cancellation.cancel('screen disposed');
    await expectLater(canceledCall, throwsA(isA<LanternCanceledException>()));
  });

  test('close is idempotent and rejects later calls', () async {
    var closeCalls = 0;
    final client = LanternClient.connect(
      Uri.parse('https://example.test'),
      transport: FakeTransportBuilder().build(),
      onClose: () => closeCalls++,
    );

    final first = client.close();
    final second = client.close();
    await Future.wait([first, second]);
    expect(closeCalls, 1);
    expect(client.isClosed, isTrue);
    expect(
      () => client.ping(),
      throwsA(isA<LanternFailedPreconditionException>()),
    );
  });

  test('redacts the token from transport diagnostics', () async {
    const secret = 'secret-token';
    final invoker = LanternInvoker(
      transport: FakeTransportBuilder().build(),
      tokenProvider: () => secret,
    );

    final call = invoker.invokeUnary<Object?>(
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) async {
            onHeader(connect.Headers()..add('x-secret', secret));
            onTrailer(connect.Headers()..add('x-trailer-secret', secret));
            throw connect.ConnectException(
              connect.Code.unauthenticated,
              'invalid $secret',
              metadata: connect.Headers()..add('x-metadata-secret', secret),
            );
          },
    );

    try {
      await call;
      fail('expected an authentication error');
    } on LanternUnauthenticatedException catch (error) {
      expect(error.message, 'invalid [REDACTED]');
      expect(error.headers['x-secret'], ['[REDACTED]']);
      expect(error.trailers['x-trailer-secret'], ['[REDACTED]']);
      expect(error.metadata['x-metadata-secret'], ['[REDACTED]']);
      expect(error.toString(), isNot(contains(secret)));
    }
  });

  test(
    'canceling a stream subscription aborts its signal immediately',
    () async {
      final source = StreamController<int>();
      final signalReady = Completer<connect.AbortSignal>();
      final invoker = LanternInvoker(transport: FakeTransportBuilder().build());
      final stream = invoker.invokeStream<int>(
        call:
            ({
              required headers,
              required signal,
              required onHeader,
              required onTrailer,
            }) {
              signalReady.complete(signal);
              return source.stream;
            },
      );

      final subscription = stream.listen((_) {});
      final signal = await signalReady.future;
      await subscription.cancel();

      await expectLater(signal.future, completes);
      await source.close();
    },
  );
}
