import 'dart:convert';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:lantern_client/lantern_client.dart';

Set<String> edgeEndpointKeys(Iterable<EdgeRef> edges) => {
  for (final edge in edges) ...[edge.tail, edge.head],
};

/// Seed once, before any Edge mutation or CDC subscription. These fixture
/// endpoints have independent lifetimes, including across a physical restart;
/// the owning test must delete them after its Edge/CDC assertions complete.
Future<void> seedLiveEdgeEndpoints(
  LanternClient client,
  Iterable<EdgeRef> edges, {
  VertexValue? value,
  Duration? expiresIn,
}) async {
  final keys = edgeEndpointKeys(edges);
  if (keys.isEmpty) return;
  final results = await client.putVertices([
    for (final key in keys)
      VertexInput(
        key: key,
        value: value ?? VertexValue.nil(),
        expiresIn: expiresIn,
      ),
  ]);
  if (results.length != keys.length ||
      results.any((result) => result.outcome != PutOutcome.appliedAndLive)) {
    throw StateError('physical_endpoint_seed_not_live');
  }
}

/// The attested run ID identifies a new test intent. Derivation preserves its
/// original contribution ID after process death; it is not a credential and
/// must never be used to refresh an already dispatched intent.
Uint8List physicalContributionId(String runId, int intent) {
  if (runId.isEmpty || intent < 1) {
    throw ArgumentError('Physical contribution requires a run and intent');
  }
  final bytes = sha256
      .convert(utf8.encode('lantern-physical-contribution:$runId:$intent'))
      .bytes;
  return Uint8List.fromList(bytes.sublist(0, 24));
}
