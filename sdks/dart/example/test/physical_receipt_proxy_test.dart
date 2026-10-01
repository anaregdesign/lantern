import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';

import '../integration_test/support/receipt_physical_fixture.dart';
import '../tool/physical_receipt_proxy.dart';

void main() {
  test(
    'receipt transport recognizes bounded unavailable without widening denial',
    () async {
      final socket = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final port = socket.port;
      await socket.close();
      final client = LanternClient.connect(
        Uri.parse('http://127.0.0.1:$port'),
        allowInsecure: true,
        retryPolicy: const RetryPolicy(maxAttempts: 1),
        defaultTimeout: const Duration(seconds: 2),
      );
      addTearDown(client.close);

      Object? failure;
      try {
        await client.getReceiptCapability();
      } on Object catch (error) {
        failure = error;
      }
      expect(failure, isA<LanternRetryExhaustedException>());
      final exhausted = failure! as LanternRetryExhaustedException;
      expect(exhausted.cause, isA<LanternUnavailableException>());
      expect(receiptUnavailableCause(exhausted), same(exhausted.cause));
      expect(receiptUnavailableCause(exhausted.cause), same(exhausted.cause));
      expect(receiptUnavailableCause(StateError('unrelated')), isNull);
      expect(receiptTransientTransportCause(exhausted), same(exhausted.cause));
      expect(receiptTransientTransportCause(StateError('unrelated')), isNull);

      expect(
        isIosLocalNetworkDeniedCause(const SocketException('No route to host')),
        isTrue,
      );
      expect(
        isIosLocalNetworkDeniedCause(
          const SocketException('Operation not permitted'),
        ),
        isTrue,
      );
      expect(
        isIosLocalNetworkDeniedCause(
          const SocketException('Connection refused'),
        ),
        isFalse,
      );
      expect(
        isIosLocalNetworkDeniedCause(
          HandshakeException('certificate rejected'),
        ),
        isFalse,
      );
    },
  );

  test(
    'radio timeout is transient but cannot prove iOS privacy denial',
    () async {
      final blocked = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      blocked.listen((_) {});
      addTearDown(() => blocked.close(force: true));
      final client = LanternClient.connect(
        Uri.parse('http://127.0.0.1:${blocked.port}'),
        allowInsecure: true,
        retryPolicy: const RetryPolicy(maxAttempts: 1),
        defaultTimeout: const Duration(milliseconds: 250),
      );
      addTearDown(client.close);

      Object? failure;
      try {
        await client.getReceiptCapability().timeout(const Duration(seconds: 3));
      } on Object catch (error) {
        failure = error;
      }
      expect(failure, isA<LanternDeadlineExceededException>());
      expect(receiptTransientTransportCause(failure!), same(failure));
      expect(receiptUnavailableCause(failure), isNull);

      final wrapped = LanternRetryExhaustedException(
        attempts: 1,
        cause: failure as LanternException,
      );
      expect(receiptTransientTransportCause(wrapped), same(failure));
      expect(receiptUnavailableCause(wrapped), isNull);
    },
  );

  test('token request recovers after more than three DNS failures', () async {
    var attempts = 0;
    final waits = <Duration>[];
    final connection = await retryReceiptTokenConnect(() async {
      attempts++;
      if (attempts < 5) {
        // The open step may succeed while the response step sees a reset.
        throw const SocketException('transient DNS recovery');
      }
      return 'connected';
    }, pause: (duration) async => waits.add(duration));
    expect(connection, 'connected');
    expect(attempts, 5);
    expect(waits, [
      const Duration(milliseconds: 500),
      const Duration(seconds: 1),
      const Duration(milliseconds: 1500),
      const Duration(seconds: 2),
    ]);
  });

  test('persistent token socket failure has an attempt bound', () async {
    var attempts = 0;
    await expectLater(
      retryReceiptTokenConnect(
        () async {
          attempts++;
          throw const SocketException('persistent');
        },
        pause: (_) async {},
        maxAttempts: 4,
      ),
      throwsA(isA<SocketException>()),
    );
    expect(attempts, 4);
  });

  test('token request cannot exceed its wall-clock bound', () async {
    final never = Completer<void>();
    final stopwatch = Stopwatch()..start();
    await expectLater(
      retryReceiptTokenConnect(
        () => never.future,
        recoveryWindow: const Duration(milliseconds: 30),
      ),
      throwsA(isA<TimeoutException>()),
    );
    expect(stopwatch.elapsed, lessThan(const Duration(seconds: 1)));
  });

  test('token connection does not retry TLS or application failures', () async {
    for (final error in [
      HandshakeException('untrusted certificate'),
      StateError('invalid response'),
      HttpException('non-200 response'),
      const FormatException('invalid token body'),
    ]) {
      var attempts = 0;
      await expectLater(
        retryReceiptTokenConnect<void>(() async {
          attempts++;
          throw error;
        }, pause: (_) async => fail('Unexpected retry delay')),
        throwsA(same(error)),
      );
      expect(attempts, 1);
    }
  });

  test(
    'the proxy drops committed responses and seals a restart trace',
    () async {
      var committed = 0;
      final upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      upstream.listen((request) async {
        await request.drain<void>();
        if (request.uri.path.endsWith('/AddEdges')) committed++;
        request.response.headers.contentType = ContentType.binary;
        request.response.add([1, 2, 3]);
        await request.response.close();
      });
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final proxy = ReceiptResponseDropProxy(
        server,
        Uri.parse('http://127.0.0.1:${upstream.port}'),
        'synthetic-test-token',
      );
      final http = HttpClient();
      addTearDown(() async {
        http.close(force: true);
        await proxy.close();
        await upstream.close(force: true);
      });
      final base = Uri.parse('http://127.0.0.1:${server.port}');

      Future<void> send(String rpc) async {
        final request = await http.postUrl(
          base.resolve('/graph.v1.LanternService/$rpc'),
        );
        request.headers.contentType = ContentType.binary;
        request.add([4, 5, 6]);
        final response = await request.close();
        expect(
          await response.fold<List<int>>(
            [],
            (bytes, chunk) => bytes..addAll(chunk),
          ),
          [1, 2, 3],
        );
      }

      const mutations = [
        'PutVertices',
        'DeleteVertices',
        'DeleteEdges',
        'AddEdges',
        'DeleteEdgeContributions',
      ];
      for (final mutation in mutations) {
        await send('GetReceiptStatuses');
        await expectLater(send(mutation), throwsA(isA<Exception>()));
      }
      expect(committed, 1);
      final barrier = await http.postUrl(
        base.resolve('/_receipt_matrix_status'),
      );
      barrier.headers.set(
        HttpHeaders.authorizationHeader,
        'Bearer synthetic-test-token',
      );
      expect((await barrier.close()).statusCode, HttpStatus.noContent);
      for (var i = 0; i < mutations.length; i++) {
        await send('GetReceiptStatuses');
      }

      final control = await http.getUrl(
        base.resolve('/_receipt_matrix_status'),
      );
      control.headers.set(
        HttpHeaders.authorizationHeader,
        'Bearer synthetic-test-token',
      );
      final response = await control.close();
      expect(response.statusCode, HttpStatus.ok);
      final status =
          jsonDecode(await utf8.decodeStream(response)) as Map<String, dynamic>;
      expect(status['schema'], 1);
      expect(status['failures'], 0);
      expect(status['dropped'], {
        for (final mutation in mutations) mutation: 1,
      });
      expect(status['forwarded'], {
        'GetReceiptStatuses': 10,
        for (final mutation in mutations) mutation: 1,
      });
      expect(status['trace'], [
        for (final mutation in mutations) ...['GetReceiptStatuses', mutation],
        'AwaitingSigkill',
        'GetReceiptStatuses',
        'GetReceiptStatuses',
        'GetReceiptStatuses',
        'GetReceiptStatuses',
        'GetReceiptStatuses',
      ]);

      final duplicateBarrier = await http.postUrl(
        base.resolve('/_receipt_matrix_status'),
      );
      duplicateBarrier.headers.set(
        HttpHeaders.authorizationHeader,
        'Bearer synthetic-test-token',
      );
      expect((await duplicateBarrier.close()).statusCode, HttpStatus.conflict);

      final noAuth = await http.getUrl(base.resolve('/_receipt_matrix_status'));
      expect((await noAuth.close()).statusCode, HttpStatus.unauthorized);
      expect(committed, 1);
    },
  );

  test('loopback HTTPS rejects untrusted TLS before forwarding', () async {
    final directory = await Directory.systemTemp.createTemp(
      'lantern-receipt-proxy-tls-',
    );
    addTearDown(() => directory.delete(recursive: true));
    final authority = File('${directory.path}/ca.pem');
    final authorityKey = File('${directory.path}/ca.key');
    final certificate = File('${directory.path}/server.pem');
    final privateKey = File('${directory.path}/server.key');
    final request = File('${directory.path}/server.csr');
    final extensions = File('${directory.path}/server.ext');
    await extensions.writeAsString(
      'subjectAltName=IP:127.0.0.1\n'
      'basicConstraints=critical,CA:FALSE\n'
      'extendedKeyUsage=serverAuth\n'
      'keyUsage=critical,digitalSignature,keyEncipherment\n',
    );
    Future<void> generate(List<String> arguments) async {
      final result = await Process.run('openssl', arguments);
      expect(result.exitCode, 0, reason: 'Ephemeral TLS fixture is required');
    }

    await generate([
      'req',
      '-x509',
      '-newkey',
      'rsa:2048',
      '-nodes',
      '-sha256',
      '-days',
      '1',
      '-subj',
      '/CN=Lantern Test CA',
      '-addext',
      'basicConstraints=critical,CA:TRUE',
      '-keyout',
      authorityKey.path,
      '-out',
      authority.path,
    ]);
    await generate([
      'req',
      '-new',
      '-newkey',
      'rsa:2048',
      '-nodes',
      '-sha256',
      '-subj',
      '/CN=localhost',
      '-keyout',
      privateKey.path,
      '-out',
      request.path,
    ]);
    await generate([
      'x509',
      '-req',
      '-in',
      request.path,
      '-CA',
      authority.path,
      '-CAkey',
      authorityKey.path,
      '-CAcreateserial',
      '-out',
      certificate.path,
      '-days',
      '1',
      '-sha256',
      '-extfile',
      extensions.path,
    ]);

    final upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var committed = 0;
    upstream.listen((request) async {
      await request.drain<void>();
      if (request.uri.path.endsWith('/PutVertices')) committed++;
      request.response.add([1, 2, 3]);
      await request.response.close();
    });
    final context = SecurityContext()
      ..useCertificateChain(certificate.path)
      ..usePrivateKey(privateKey.path);
    final server = await HttpServer.bindSecure(
      InternetAddress.loopbackIPv4,
      0,
      context,
    );
    final proxy = ReceiptResponseDropProxy(
      server,
      Uri.parse('http://127.0.0.1:${upstream.port}'),
      'synthetic-test-token',
    );
    final trustedContext = SecurityContext(withTrustedRoots: false)
      ..setTrustedCertificates(authority.path);
    final trusted = HttpClient(context: trustedContext);
    final untrusted = HttpClient(
      context: SecurityContext(withTrustedRoots: false),
    );
    addTearDown(() async {
      trusted.close(force: true);
      untrusted.close(force: true);
      await proxy.close();
      await upstream.close(force: true);
    });
    final base = Uri.parse('https://127.0.0.1:${server.port}');

    await expectLater(
      untrusted.postUrl(
        base.resolve('/graph.v1.LanternService/GetReceiptStatuses'),
      ),
      throwsA(isA<HandshakeException>()),
    );
    expect(committed, 0);

    Future<void> send(String rpc) async {
      final request = await trusted.postUrl(
        base.resolve('/graph.v1.LanternService/$rpc'),
      );
      request.add([4, 5, 6]);
      final response = await request.close();
      expect(
        await response.fold<List<int>>(
          [],
          (bytes, chunk) => bytes..addAll(chunk),
        ),
        [1, 2, 3],
      );
    }

    await send('GetReceiptStatuses');
    await expectLater(send('PutVertices'), throwsA(isA<Exception>()));
    expect(committed, 1);

    final control = await trusted.getUrl(
      base.resolve('/_receipt_matrix_status'),
    );
    control.headers.set(
      HttpHeaders.authorizationHeader,
      'Bearer synthetic-test-token',
    );
    final response = await control.close();
    expect(response.statusCode, HttpStatus.ok);
    final trace = ReceiptProxyTrace.parse(
      jsonDecode(await utf8.decodeStream(response)),
    );
    expect(trace.failures, 0);
    expect(trace.forwarded['GetReceiptStatuses'], 1);
    expect(trace.dropped['PutVertices'], 1);
  });

  test(
    'contribution-only proxy drops its committed response and rejects other mutations',
    () async {
      var committed = 0;
      final upstream = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      upstream.listen((request) async {
        await request.drain<void>();
        committed++;
        request.response.add([1]);
        await request.response.close();
      });
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final proxy = ReceiptResponseDropProxy(
        server,
        Uri.parse('http://127.0.0.1:${upstream.port}'),
        'synthetic-test-token',
        mutationRpcs: const {'DeleteEdgeContributions'},
      );
      final http = HttpClient();
      addTearDown(() async {
        http.close(force: true);
        await proxy.close();
        await upstream.close(force: true);
      });
      final base = Uri.parse('http://127.0.0.1:${server.port}');
      final request = await http.postUrl(
        base.resolve('/graph.v1.LanternService/DeleteEdgeContributions'),
      );
      Object? dropped;
      try {
        await (await request.close()).drain<void>();
      } catch (error) {
        dropped = error;
      }
      expect(dropped, isNotNull);
      expect(committed, 1);
      final rejected = await http.postUrl(
        base.resolve('/graph.v1.LanternService/DeleteEdges'),
      );
      expect((await rejected.close()).statusCode, HttpStatus.badRequest);
      expect(committed, 1);
      final control = await http.getUrl(
        base.resolve('/_receipt_matrix_status'),
      );
      control.headers.set(
        HttpHeaders.authorizationHeader,
        'Bearer synthetic-test-token',
      );
      final trace =
          jsonDecode(await utf8.decodeStream(await control.close())) as Map;
      expect((trace['dropped'] as Map)['DeleteEdgeContributions'], 1);
      expect((trace['forwarded'] as Map)['DeleteEdgeContributions'], 1);
    },
  );

  test('private proxy configuration refuses nonlocal plaintext upstream', () {
    final base = {
      'listenHost': '127.0.0.1',
      'listenPort': 1234,
      'certificateChain': '/private/cert.pem',
      'privateKey': '/private/key.pem',
      'upstream': 'http://127.0.0.1:6380',
      'controlToken': 'synthetic-test-token',
    };
    expect(ReceiptProxyConfig.parse(jsonEncode(base)).upstream.scheme, 'http');
    for (final upstream in [
      'http://external.example:6380',
      'http://127.0.0.1:6380/private',
      'ftp://127.0.0.1:6380',
    ]) {
      expect(
        () => ReceiptProxyConfig.parse(
          jsonEncode({...base, 'upstream': upstream}),
        ),
        throwsFormatException,
      );
    }
    expect(
      () => ReceiptProxyConfig.parse(jsonEncode({...base, 'listenPort': 0})),
      throwsFormatException,
    );
  });

  test('a forged or incomplete proxy trace cannot prove a relaunch', () {
    const mutations = [
      'PutVertices',
      'DeleteVertices',
      'DeleteEdges',
      'AddEdges',
      'DeleteEdgeContributions',
    ];
    final initial = [
      for (final mutation in mutations) ...['GetReceiptStatuses', mutation],
    ];
    Map<String, Object> snapshot(List<String> events) => {
      'schema': 1,
      'forwarded': {
        'GetReceiptStatuses': 10,
        for (final mutation in mutations) mutation: 1,
      },
      'dropped': {for (final mutation in mutations) mutation: 1},
      'trace': events,
      'failures': 0,
    };

    ReceiptProxyTrace.parse(snapshot(initial)).assertInitialLoss();
    final recovered = snapshot([
      ...initial,
      'AwaitingSigkill',
      ...List.filled(5, 'GetReceiptStatuses'),
    ]);
    ReceiptProxyTrace.parse(recovered).assertRecoveredWithoutResend();

    for (final events in [
      <String>[...initial, 'AwaitingSigkill'],
      <String>[...initial, 'AwaitingSigkill', 'PutVertices'],
      <String>[...initial.skip(1)],
    ]) {
      final trace = ReceiptProxyTrace.parse(snapshot(events));
      expect(
        () => events.contains('AwaitingSigkill')
            ? trace.assertRecoveredWithoutResend()
            : trace.assertInitialLoss(),
        throwsStateError,
      );
    }
    expect(
      () => ReceiptProxyTrace.parse(snapshot(['UntrustedOperatorPass'])),
      throwsStateError,
    );
    expect(
      () => ReceiptProxyTrace.parse({
        ...snapshot(initial),
        'failures': 1,
      }).assertInitialLoss(),
      throwsStateError,
    );
  });
}
