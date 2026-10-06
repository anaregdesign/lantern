import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' show Code, ConnectException;
import 'package:fixnum/fixnum.dart';
import 'package:lantern_connect_transport_probe/probe.dart';
import 'package:lantern_connect_transport_probe/src/gen/graph/v1/graph.pb.dart';
import 'package:test/test.dart';

void main() {
  test(
    'observer preserves results, unary deadlines, auth and client ownership',
    () async {
      final client = _HttpClient();
      final events = <String>[];
      final result = await runProbe(
        Uri.parse('http://unused.invalid'),
        token: 'test-token',
        httpClient: client,
        onRpc: ({required method, required state, code, stackTrace}) {
          events.add('$method:$state');
        },
      );
      expect(result['transport'], 'connect-http1');
      expect(result['int64'], '922337203685477580');
      expect(result['streamRecordsBeforeCancel'], 1);
      expect(result['headerCallback'], isTrue);
      expect(result['trailerCallback'], isTrue);
      expect(events, [
        'PutVertex:start',
        'PutVertex:success',
        'GetVertex:start',
        'GetVertex:success',
        'BackupSnapshot:start',
        'BackupSnapshot:success',
      ]);
      expect(client.requests, hasLength(3));
      for (final request in client.requests) {
        expect(request.headers.value('authorization'), 'Bearer test-token');
      }
      for (final request in client.requests.take(2)) {
        final timeout = int.parse(request.headers.value('connect-timeout-ms')!);
        expect(timeout, inInclusiveRange(1, 5000));
      }
      // The existing Connect stream uses cancellation, without a timeout header.
      expect(client.requests.last.headers.value('connect-timeout-ms'), isNull);
      expect(client.requests.last.aborted, isTrue);
      expect(client.closed, isFalse);
    },
  );

  for (final method in ['PutVertex', 'GetVertex', 'BackupSnapshot']) {
    test(
      '$method failure reports exact phase and rethrows original error',
      () async {
        final error = ConnectException(
          Code.deadlineExceeded,
          'private-message',
        );
        final client = _HttpClient(failingMethod: method, failure: error);
        final failures = <String>[];
        await expectLater(
          runProbe(
            Uri.parse('http://unused.invalid'),
            httpClient: client,
            onRpc: ({required method, required state, code, stackTrace}) {
              if (state == 'failure') {
                failures.add('$method:$code');
                expect(stackTrace, isNotNull);
              }
            },
          ),
          throwsA(same(error)),
        );
        expect(failures, ['$method:deadline_exceeded']);
        expect(client.closed, isFalse);
        expect(client.methods.last, method);
      },
    );
  }

  for (final emptyStream in [false, true]) {
    test(
      'contract assertion is attributed to ${emptyStream ? 'stream' : 'read'} RPC',
      () async {
        final failures = <String>[];
        await expectLater(
          runProbe(
            Uri.parse('http://unused.invalid'),
            httpClient: _HttpClient(
              badRead: !emptyStream,
              emptyStream: emptyStream,
            ),
            onRpc: ({required method, required state, code, stackTrace}) {
              if (state == 'failure') failures.add('$method:$code');
            },
          ),
          throwsStateError,
        );
        expect(failures, [
          '${emptyStream ? 'BackupSnapshot' : 'GetVertex'}:assertion',
        ]);
      },
    );
  }

  test('owned cleanup occurs and is observed after a failed RPC', () async {
    final error = ConnectException(Code.deadlineExceeded, 'private-message');
    final client = _HttpClient(failingMethod: 'GetVertex', failure: error);
    final events = <String>[];
    await expectLater(
      HttpOverrides.runZoned(
        () => runProbe(
          Uri.parse('http://unused.invalid'),
          onRpc: ({required method, required state, code, stackTrace}) {
            events.add('$method:$state');
          },
        ),
        createHttpClient: (_) => client,
      ),
      throwsA(same(error)),
    );
    expect(client.closed, isTrue);
    expect(events.sublist(events.length - 3), [
      'GetVertex:failure',
      'ChannelShutdown:start',
      'ChannelShutdown:success',
    ]);
  });

  test('observer exceptions cannot alter the successful probe', () async {
    final result = await runProbe(
      Uri.parse('http://unused.invalid'),
      httpClient: _HttpClient(),
      onRpc: ({required method, required state, code, stackTrace}) {
        throw StateError('broken diagnostic sink');
      },
    );
    expect(result['streamRecordsBeforeCancel'], 1);
  });

  test('status classifier admits only fixed codes, never raw errors', () {
    expect(
      probeErrorCode(ConnectException(Code.canceled, 'secret')),
      'cancelled',
    );
    expect(
      probeErrorCode(ConnectException(Code.unauthenticated, 'secret')),
      'unauthenticated',
    );
    expect(probeErrorCode(FormatException('secret')), 'configuration');
    expect(probeErrorCode(Object()), 'unknown');
  });
}

class _HttpClient implements HttpClient {
  _HttpClient({
    this.failingMethod,
    this.failure,
    this.badRead = false,
    this.emptyStream = false,
  });
  final String? failingMethod;
  final Object? failure;
  final bool badRead;
  final bool emptyStream;
  final requests = <_Request>[];
  final methods = <String>[];
  bool closed = false;

  @override
  Future<HttpClientRequest> openUrl(String method, Uri url) async {
    final name = url.pathSegments.last;
    methods.add(name);
    if (name == failingMethod) throw failure!;
    final Uint8List body;
    final streaming = name == 'BackupSnapshot';
    switch (name) {
      case 'PutVertex':
        body = PutVertexResponse().writeToBuffer();
      case 'GetVertex':
        body =
            GetVertexResponse(
              vertex: Vertex(
                int64:
                    badRead ? Int64.ZERO : Int64.parseInt('922337203685477580'),
              ),
            ).writeToBuffer();
      case 'BackupSnapshot':
        final record =
            BackupSnapshotResponse(vertex: Vertex(key: 'test')).writeToBuffer();
        body = Uint8List.fromList([
          if (!emptyStream) ..._envelope(0, record),
          ..._envelope(2, [123, 125]), // Empty Connect end-stream JSON object.
        ]);
      default:
        throw StateError('unexpected method');
    }
    final request = _Request(_Response(body, streaming));
    requests.add(request);
    return request;
  }

  @override
  void close({bool force = false}) {
    closed = true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

List<int> _envelope(int flags, List<int> bytes) {
  final prefix =
      ByteData(5)
        ..setUint8(0, flags)
        ..setUint32(1, bytes.length);
  return [...prefix.buffer.asUint8List(), ...bytes];
}

class _Request implements HttpClientRequest {
  _Request(this.response);
  final _Response response;
  @override
  final headers = _Headers();
  bool aborted = false;
  @override
  void add(List<int> data) {}
  @override
  Future<HttpClientResponse> close() async => response;
  @override
  void abort([Object? exception, StackTrace? stackTrace]) {
    aborted = true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Response extends Stream<List<int>> implements HttpClientResponse {
  _Response(this.body, bool streaming) {
    headers.add(
      'content-type',
      streaming ? 'application/connect+proto' : 'application/proto',
    );
  }
  final Uint8List body;
  @override
  final headers = _Headers();
  @override
  int get statusCode => 200;
  @override
  StreamSubscription<List<int>> listen(
    void Function(List<int>)? onData, {
    Function? onError,
    void Function()? onDone,
    bool? cancelOnError,
  }) => Stream<List<int>>.value(body).listen(
    onData,
    onError: onError,
    onDone: onDone,
    cancelOnError: cancelOnError,
  );
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}

class _Headers implements HttpHeaders {
  final values = <String, List<String>>{};
  @override
  void add(String name, Object value, {bool preserveHeaderCase = false}) {
    values.putIfAbsent(name.toLowerCase(), () => []).add(value.toString());
  }

  @override
  String? value(String name) => values[name.toLowerCase()]?.join(',');
  @override
  void removeAll(String name) {
    values.remove(name.toLowerCase());
  }

  @override
  void forEach(void Function(String, List<String>) action) =>
      values.forEach(action);
  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
