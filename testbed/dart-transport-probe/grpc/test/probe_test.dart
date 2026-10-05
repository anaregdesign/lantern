import 'package:fixnum/fixnum.dart';
import 'package:grpc/grpc.dart';
import 'package:lantern_grpc_transport_probe/probe.dart';
import 'package:lantern_grpc_transport_probe/src/gen/graph/v1/graph.pb.dart';
import 'package:test/test.dart';

void main() {
  test(
    'observer preserves results, five-second RPC options, auth and ownership',
    () async {
      final channel = _Channel();
      final events = <String>[];
      final result = await runProbe(
        Uri.parse('http://unused.invalid'),
        token: 'test-token',
        clientChannel: channel,
        onRpc: ({required method, required state, code, stackTrace}) {
          events.add('$method:$state');
        },
      );
      expect(result['transport'], 'grpc-http2');
      expect(result['int64'], '922337203685477580');
      expect(result['streamRecordsBeforeCancel'], 1);
      expect(events, [
        'PutVertex:start',
        'PutVertex:success',
        'GetVertex:start',
        'GetVertex:success',
        'BackupSnapshot:start',
        'BackupSnapshot:success',
      ]);
      expect(channel.optionsSeen, hasLength(3));
      for (final options in channel.optionsSeen) {
        expect(options.timeout, const Duration(seconds: 5));
        expect(options.metadata['authorization'], 'Bearer test-token');
      }
      expect(channel.streamCancelled, isTrue);
      expect(channel.shutDown, isFalse);
    },
  );

  for (final method in ['PutVertex', 'GetVertex', 'BackupSnapshot']) {
    test(
      '$method failure reports exact phase and rethrows original error',
      () async {
        final error = GrpcError.deadlineExceeded('private-message');
        final channel = _Channel(failingMethod: method, failure: error);
        final failures = <String>[];
        await expectLater(
          runProbe(
            Uri.parse('http://unused.invalid'),
            clientChannel: channel,
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
        expect(channel.shutDown, isFalse);
        expect(channel.methods.last, method);
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
            clientChannel: _Channel(
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

  test('observer exceptions cannot alter the successful probe', () async {
    final result = await runProbe(
      Uri.parse('http://unused.invalid'),
      clientChannel: _Channel(),
      onRpc: ({required method, required state, code, stackTrace}) {
        throw StateError('broken diagnostic sink');
      },
    );
    expect(result['streamRecordsBeforeCancel'], 1);
  });

  test('status classifier admits only fixed codes, never raw errors', () {
    expect(probeErrorCode(GrpcError.cancelled('secret')), 'cancelled');
    expect(probeErrorCode(const GrpcError.custom(999, 'secret')), 'unknown');
    expect(probeErrorCode(FormatException('secret')), 'configuration');
    expect(probeErrorCode(Object()), 'unknown');
  });
}

class _Channel extends ClientChannel {
  _Channel({
    this.failingMethod,
    this.failure,
    this.badRead = false,
    this.emptyStream = false,
  }) : super('unused.invalid');

  final String? failingMethod;
  final Object? failure;
  final bool badRead;
  final bool emptyStream;
  final optionsSeen = <CallOptions>[];
  final methods = <String>[];
  bool streamCancelled = false;
  bool shutDown = false;

  @override
  ClientCall<Q, R> createCall<Q, R>(
    ClientMethod<Q, R> method,
    Stream<Q> requests,
    CallOptions options,
  ) {
    final name = method.path.split('/').last;
    methods.add(name);
    optionsSeen.add(options);
    Stream<R> response() async* {
      await requests.single;
      if (name == failingMethod) throw failure!;
      switch (name) {
        case 'PutVertex':
          yield PutVertexResponse() as R;
        case 'GetVertex':
          yield GetVertexResponse(
                vertex: Vertex(
                  int64: badRead
                      ? Int64.ZERO
                      : Int64.parseInt('922337203685477580'),
                ),
              )
              as R;
        case 'BackupSnapshot':
          if (!emptyStream) {
            yield BackupSnapshotResponse(vertex: Vertex(key: 'test')) as R;
          }
        default:
          throw StateError('unexpected method');
      }
    }

    return _Call<Q, R>(response(), () => streamCancelled = true);
  }

  @override
  Future<void> shutdown() async {
    shutDown = true;
  }
}

class _Call<Q, R> implements ClientCall<Q, R> {
  _Call(this.response, this.onCancel);
  @override
  final Stream<R> response;
  final void Function() onCancel;
  @override
  Future<Map<String, String>> get headers async => {};
  @override
  Future<Map<String, String>> get trailers async => {};
  @override
  Future<void> cancel() async {
    onCancel();
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
