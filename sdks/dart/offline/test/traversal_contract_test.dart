// @dart=3.11
// A single named part supplies identical contracts to Dart and Flutter runners.
// ignore: unnecessary_library_name
library traversal_contract_tests;

import 'dart:async';
import 'dart:convert';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.pb.dart' as wire;
import 'package:lantern_client_offline/lantern_client_offline.dart';

import 'package:test/test.dart';

part 'support/traversal_contract.dart';

void main() {
  traversalContract(
    create: () async => InMemoryOfflineStore(),
    reopen: (store) async => InMemoryOfflineStore.fromSnapshot(
      await (store as InMemoryOfflineStore).exportSnapshot(),
    ),
  );

  test(
    'memory10 migrates empty derived lane and preserves admitted mutation bytes',
    () async {
      final store = InMemoryOfflineStore();
      final h = TraversalFixture(store);
      await h.open();
      await h.data!.mutations.submit(
        MutationRequest.putVertices([
          VertexInput(key: 'pending', value: VertexValue.string('original')),
        ]),
      );
      await h.close();
      final encoded =
          jsonDecode(await store.exportSnapshot()) as Map<String, dynamic>;
      encoded['schema'] = 10;
      final partition =
          (encoded['partitions'] as List).single as Map<String, dynamic>;
      partition.remove('traversals');
      partition.remove('traversalEpoch');
      final pending = (partition['outbox'] as List).toList();
      final migrated = InMemoryOfflineStore.fromSnapshot(jsonEncode(encoded));
      final result =
          jsonDecode(await migrated.exportSnapshot()) as Map<String, dynamic>;
      final next =
          (result['partitions'] as List).single as Map<String, dynamic>;
      expect(result['schema'], 11);
      expect(next['traversals'], isEmpty);
      expect(next['traversalEpoch'], 0);
      expect(next['outbox'], pending);
      result['schema'] = 12;
      expect(
        () => InMemoryOfflineStore.fromSnapshot(jsonEncode(result)),
        throwsA(isA<OfflineSchemaException>()),
      );
    },
  );
}
