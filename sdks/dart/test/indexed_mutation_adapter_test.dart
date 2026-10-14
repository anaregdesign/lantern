import 'dart:async';
import 'dart:typed_data';

import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/lantern_client_adapter.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as graph;
import 'package:test/test.dart';

void main() {
  test(
    'adapter freezes public intent without token, preserving TTL/value/IDs',
    () {
      var tokens = 0;
      final now = DateTime.utc(2026);
      final client = LanternClient.connect(
        Uri.parse('http://localhost:6380'),
        allowInsecure: true,
        clock: () => now,
        tokenProvider: () {
          tokens++;
          return null;
        },
        transport: FakeTransportBuilder().build(),
      );
      addTearDown(client.close);
      final put = client.freezeIndexedMutation(
        MutationRequest.putVertices([
          VertexInput(
            key: 'a',
            value: VertexValue.bytes(Uint8List.fromList([1, 2])),
            expiresIn: const Duration(seconds: 5),
          ),
        ]),
      );
      final normalized = put.normalizedRequest.inputs.single as VertexInput;
      expect(normalized.expiresIn, isNull);
      expect(normalized.expiresAt, now.add(const Duration(seconds: 5)));
      expect((normalized.value as BytesValue).value, [1, 2]);
      final add = client.freezeIndexedMutation(
        MutationRequest.addEdges([
          EdgeInput(
            tail: 'a',
            head: 'b',
            weight: 0.1,
            expiresIn: const Duration(seconds: 5),
          ),
        ]),
      );
      final first = add.normalizedRequest.inputs.single as EdgeInput;
      final again = add.normalizedRequest.inputs.single as EdgeInput;
      expect(first.contribId, hasLength(24));
      expect(first.contribId, again.contribId);
      expect(first.weight, EdgeInput(tail: 'a', head: 'b', weight: 0.1).weight);
      expect(tokens, 0);
    },
  );
  test(
    'qualified attempt consumes normalized intent with original indexed effect',
    () async {
      final client = LanternClient.connect(
        Uri.parse('http://localhost:6380'),
        allowInsecure: true,
        transport: FakeTransportBuilder()
            .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
              LanternService.deleteVertices,
              (r, _) => graph.DeleteVerticesResponse(
                deleted: 1,
                existed: [true, false],
              ),
            )
            .build(),
      );
      addTearDown(client.close);
      final intent = client.freezeIndexedMutation(
        MutationRequest.deleteVertices(['a', 'a']),
      );
      final reply = await client.attemptIndexedMutation(intent);
      expect(reply.items.map((r) => (r as MutationKnownEffect<bool>).effect), [
        true,
        false,
      ]);
    },
  );
  for (final publication in [false, true]) {
    test(
      'qualified owner fence rejects ${publication ? 'late publication with possible dispatch' : 'after token before dispatch'}',
      () async {
        final entered = Completer<void>(), token = Completer<String?>();
        var calls = 0;
        var eligible = true;
        final client = LanternClient.connect(
          Uri.parse('http://localhost:6380'),
          allowInsecure: true,
          tokenProvider: () {
            entered.complete();
            return token.future;
          },
          transport: FakeTransportBuilder()
              .unary<graph.DeleteVerticesRequest, graph.DeleteVerticesResponse>(
                LanternService.deleteVertices,
                (r, _) {
                  calls++;
                  eligible = false;
                  return graph.DeleteVerticesResponse(existed: [false]);
                },
              )
              .build(),
        );
        addTearDown(client.close);
        final intent = client.freezeIndexedMutation(
          MutationRequest.deleteVertices(['a']),
        );
        void fence() {
          if (!eligible) throw const LanternDataException(DataFailure.closed);
        }

        final outcome = client
            .attemptIndexedMutation(
              intent,
              beforeDispatch: fence,
              beforePublication: fence,
            )
            .then<Object>((r) => r, onError: (Object error) => error);
        await entered.future;
        if (!publication) eligible = false;
        token.complete(null);
        final failure = await outcome as IndexedMutationAttemptException;
        expect(failure.mayHaveDispatched, publication);
        expect(failure.cause, isA<LanternDataException>());
        expect(calls, publication ? 1 : 0);
      },
    );
  }
  test('adapter byte ceiling is checked before payload amplification', () {
    final client = LanternClient.connect(
      Uri.parse('http://localhost:6380'),
      allowInsecure: true,
      transport: FakeTransportBuilder().build(),
    );
    addTearDown(client.close);
    final input = VertexInput(
      key: 'a',
      value: VertexValue.bytes(Uint8List(1 << 20)),
    );
    expect(
      () => client.freezeIndexedMutation(
        MutationRequest.putVertices([input]),
        maxReservedBytes: 512,
      ),
      throwsA(
        isA<LanternDataException>().having(
          (e) => e.reason,
          'reason',
          DataFailure.capacity,
        ),
      ),
    );
  });
}
