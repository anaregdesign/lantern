import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_example/app_data_session.dart';

import '../integration_test/support/installed_factory_fixture.dart';

void main() {
  late _RelayTlsMaterial tls;
  setUpAll(() async => tls = await _RelayTlsMaterial.create());
  tearDownAll(() => tls.directory.delete(recursive: true));

  test('T18 scenario inventory includes each current profile exactly once', () {
    expect(installedFactoryProfileScenarios.keys.toSet(), {
      ...AppDataProfile.values,
    });
    expect(installedFactoryProfileScenarios.values.toSet(), hasLength(4));
    expect(installedFactoryScenarios, hasLength(7));
    expect(
      installedFactoryScenarios,
      containsAll({
        'immutable_account_a_b_a',
        'shared_remote_wipe_before_swap',
        'installation_restore_ownership',
      }),
    );
    expect(
      installedFactoryScenarios,
      everyElement(matches(RegExp(r'^[a-z][a-z0-9_]*$'))),
    );
  });

  test('real alarms remove fired and canceled ownership', () async {
    final schedule = PhysicalSessionSchedule();
    final fired = Completer<void>();
    schedule.call(Duration.zero, fired.complete);
    final canceled = schedule.call(const Duration(hours: 1), () {
      fail('Canceled alarm fired');
    });
    expect(schedule.activeAlarms, 2);
    await fired.future.timeout(const Duration(seconds: 2));
    expect(schedule.activeAlarms, 1);
    canceled.cancel();
    canceled.cancel();
    expect(schedule.activeAlarms, 0);
  });

  test(
    'relay keeps real HTTP bytes and recovers only by socket availability',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      server.listen((request) async {
        request.response.write('real-server');
        await request.response.close();
      });
      final relay = await PhysicalSocketRelay.open(
        Uri.parse('http://127.0.0.1:${server.port}'),
        allowInsecure: true,
      );
      final client = relay.createHttpClient();
      addTearDown(() async {
        client.close(force: true);
        await relay.close();
        await server.close(force: true);
      });
      Future<String> get() async {
        final request = await client.getUrl(relay.endpoint);
        final response = await request.close();
        return utf8.decodeStream(response);
      }

      expect(await get(), 'real-server');
      relay.setAvailable(false);
      await expectLater(
        get().timeout(const Duration(seconds: 3)),
        throwsA(isA<IOException>()),
      );
      expect(relay.cutConnections, greaterThan(0));
      await expectLater(
        get().timeout(const Duration(seconds: 3)),
        throwsA(isA<IOException>()),
      );
      expect(relay.rejectedConnections, greaterThan(0));
      relay.setAvailable(true);
      expect(await get(), 'real-server');
      client.close(force: true);
      final closing = relay.close();
      expect(identical(closing, relay.close()), isTrue);
      await closing;
      expect(relay.activeConnections, 0);
      expect(relay.createHttpClient, throwsStateError);
    },
  );

  test('relay rejects implicit plaintext and changed routes', () async {
    for (final invalid in [
      'http://example.test',
      'https://user@example.test',
      'https://example.test?query=private',
      'https://example.test#fragment',
    ]) {
      await expectLater(
        PhysicalSocketRelay.open(Uri.parse(invalid)),
        throwsArgumentError,
      );
    }
    final relay = await PhysicalSocketRelay.open(
      Uri.parse('https://example.test'),
    );
    final client = relay.createHttpClient();
    addTearDown(() async {
      client.close(force: true);
      await relay.close();
    });
    await expectLater(
      Future.sync(() => client.getUrl(Uri.parse('https://other.test'))),
      throwsStateError,
    );
    expect(relay.acceptedConnections, 0);
  });

  test(
    'HTTPS relay authenticates the original host and carries TLS bytes',
    () async {
      final server = await HttpServer.bindSecure(
        InternetAddress.loopbackIPv4,
        0,
        tls.serverContext(),
      );
      var requests = 0;
      server.listen((request) async {
        requests++;
        request.response.write('verified-tls-server');
        await request.response.close();
      });
      final relay = await PhysicalSocketRelay.open(
        Uri.parse('https://localhost:${server.port}'),
      );
      final client = relay.createHttpClient(context: tls.trustedContext());
      addTearDown(() async {
        client.close(force: true);
        await relay.close();
        await server.close(force: true);
      });

      final response = await (await client.getUrl(relay.endpoint)).close();
      expect(response.statusCode, HttpStatus.ok);
      expect(await utf8.decodeStream(response), 'verified-tls-server');
      expect(requests, 1);
      expect(relay.acceptedConnections, 1);
    },
  );

  for (final wrongHost in [false, true]) {
    test(
      'HTTPS relay rejects ${wrongHost ? 'wrong host' : 'untrusted CA'}',
      () async {
        final server = await HttpServer.bindSecure(
          InternetAddress.loopbackIPv4,
          0,
          tls.serverContext(),
        );
        var requests = 0;
        server.listen((request) {
          requests++;
          request.response.close();
        }, onError: (Object error) => expect(error, isA<HandshakeException>()));
        final relay = await PhysicalSocketRelay.open(
          Uri.parse(
            'https://${wrongHost ? '127.0.0.1' : 'localhost'}:${server.port}',
          ),
        );
        final client = wrongHost
            ? relay.createHttpClient(context: tls.trustedContext())
            : relay.createHttpClient();
        addTearDown(() async {
          client.close(force: true);
          await relay.close();
          await server.close(force: true);
        });

        await expectLater(
          client.getUrl(relay.endpoint),
          throwsA(isA<HandshakeException>()),
        );
        expect(requests, 0);
      },
    );
  }

  test(
    'HTTPS cancellation closes a handshake that finishes after timeout',
    () async {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final accepted = Completer<Socket>();
      server.listen(accepted.complete);
      final relay = await PhysicalSocketRelay.open(
        Uri.parse('https://localhost:${server.port}'),
      );
      final client = relay.createHttpClient(context: tls.trustedContext())
        ..connectionTimeout = const Duration(milliseconds: 100);
      Socket? peer;
      addTearDown(() async {
        client.close(force: true);
        peer?.destroy();
        await relay.close();
        await server.close();
      });

      final failed = expectLater(
        client.getUrl(relay.endpoint),
        throwsA(isA<SocketException>()),
      );
      final raw = peer = await accepted.future.timeout(
        const Duration(seconds: 2),
      );
      await failed;
      try {
        final secure = await SecureSocket.secureServer(
          raw,
          tls.serverContext(),
        ).timeout(const Duration(seconds: 2));
        peer = secure;
        var applicationBytes = 0;
        await secure
            .forEach((bytes) => applicationBytes += bytes.length)
            .timeout(const Duration(seconds: 2));
        expect(applicationBytes, 0);
      } on IOException {
        // Cancellation may close TCP before the late TLS handshake completes.
      }
      await relay.close().timeout(const Duration(seconds: 2));
      expect(relay.activeConnections, 0);
    },
  );

  test('upstream loss reaches the client without a success fallback', () async {
    final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final port = server.port;
    await server.close();
    final relay = await PhysicalSocketRelay.open(
      Uri.parse('http://127.0.0.1:$port'),
      allowInsecure: true,
    );
    final client = relay.createHttpClient();
    addTearDown(() async {
      client.close(force: true);
      await relay.close();
    });
    await expectLater(
      (() async {
        final request = await client.getUrl(relay.endpoint);
        await request.close();
      })().timeout(const Duration(seconds: 3)),
      throwsA(isA<IOException>()),
    );
    expect(relay.transportDisconnects, greaterThan(0));
    relay.check();
  });
}

final class _RelayTlsMaterial {
  _RelayTlsMaterial(
    this.directory,
    this.authority,
    this.certificate,
    this.privateKey,
  );
  final Directory directory;
  final File authority, certificate, privateKey;

  static Future<_RelayTlsMaterial> create() async {
    final directory = await Directory.systemTemp.createTemp(
      'lantern-relay-tls-',
    );
    final authority = File('${directory.path}/ca.pem');
    final authorityKey = File('${directory.path}/ca.key');
    final certificate = File('${directory.path}/localhost.pem');
    final privateKey = File('${directory.path}/localhost.key');
    final request = File('${directory.path}/localhost.csr');
    final extensions = File('${directory.path}/localhost.ext');
    try {
      Future<void> generate(List<String> arguments) async {
        final result = await Process.run('openssl', arguments);
        expect(result.exitCode, 0, reason: 'Ephemeral TLS fixture is required');
      }

      await extensions.writeAsString(
        'subjectAltName=DNS:localhost\n'
        'basicConstraints=critical,CA:FALSE\n'
        'extendedKeyUsage=serverAuth\n'
        'keyUsage=critical,digitalSignature,keyEncipherment\n',
      );
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
      return _RelayTlsMaterial(directory, authority, certificate, privateKey);
    } catch (_) {
      await directory.delete(recursive: true);
      rethrow;
    }
  }

  SecurityContext serverContext() => SecurityContext()
    ..useCertificateChain(certificate.path)
    ..usePrivateKey(privateKey.path);

  SecurityContext trustedContext() =>
      SecurityContext(withTrustedRoots: false)
        ..setTrustedCertificates(authority.path);
}
