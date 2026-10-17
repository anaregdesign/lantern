import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_example/app_data_session.dart';

import '../integration_test/support/installed_factory_fixture.dart';

void main() {
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
