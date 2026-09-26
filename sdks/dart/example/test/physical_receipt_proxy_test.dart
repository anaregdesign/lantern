import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

import '../integration_test/support/receipt_physical_fixture.dart';
import '../tool/physical_receipt_proxy.dart';

void main() {
  test('the proxy drops committed responses and seals a restart trace', (
    ) async {
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
      expect(await response.fold<List<int>>([], (bytes, chunk) => bytes..addAll(chunk)), [
        1,
        2,
        3,
      ]);
    }

    const mutations = [
      'PutVertices',
      'DeleteVertices',
      'DeleteEdges',
      'AddEdges',
    ];
    for (final mutation in mutations) {
      await send('GetReceiptStatuses');
      await expectLater(send(mutation), throwsA(isA<Exception>()));
    }
    expect(committed, 1);
    final barrier = await http.postUrl(base.resolve('/_receipt_matrix_status'));
    barrier.headers.set(HttpHeaders.authorizationHeader, 'Bearer synthetic-test-token');
    expect((await barrier.close()).statusCode, HttpStatus.noContent);
    for (var i = 0; i < mutations.length; i++) {
      await send('GetReceiptStatuses');
    }

    final control = await http.getUrl(base.resolve('/_receipt_matrix_status'));
    control.headers.set(HttpHeaders.authorizationHeader, 'Bearer synthetic-test-token');
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
      'GetReceiptStatuses': 8,
      for (final mutation in mutations) mutation: 1,
    });
    expect(status['trace'], [
      for (final mutation in mutations) ...['GetReceiptStatuses', mutation],
      'AwaitingSigkill',
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
  });

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
    ];
    final initial = [
      for (final mutation in mutations) ...['GetReceiptStatuses', mutation],
    ];
    Map<String, Object> snapshot(List<String> events) => {
      'schema': 1,
      'forwarded': {
        'GetReceiptStatuses': 8,
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
      ...List.filled(4, 'GetReceiptStatuses'),
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
      () => ReceiptProxyTrace.parse({...snapshot(initial), 'failures': 1})
          .assertInitialLoss(),
      throwsStateError,
    );
  });
}
