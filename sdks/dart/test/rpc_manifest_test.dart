import 'dart:io';

import 'package:lantern_client/src/gen/graph/v1/graph.connect.client.dart';
import 'package:lantern_client/src/gen/graph/v1/graph.connect.spec.dart';
import 'package:test/test.dart';

void main() {
  test('LanternService generated symbols compile', () {
    LanternServiceClient? client;
    expect(client, isNull);
    expect(LanternService.name, 'graph.v1.LanternService');
  });

  test('every app-facing RPC is intentionally classified', () {
    final source = File(
      'lib/src/gen/graph/v1/graph.connect.spec.dart',
    ).readAsStringSync();
    final generated = RegExp(
      r"'/\$name/([A-Za-z]+)'",
    ).allMatches(source).map((match) => match.group(1)!).toSet();

    expect(generated, _appFacingRpcs);
  });

  test('public barrel does not export replication internals', () {
    final barrel = File('lib/lantern_client.dart').readAsStringSync();
    final exports = barrel
        .split('\n')
        .where((line) => line.trimLeft().startsWith('export '))
        .join('\n');
    expect(exports, isNot(contains('replication')));
    expect(exports, isNot(contains('connect.client')));
  });
}

const _appFacingRpcs = <String>{
  'AddEdge',
  'AddEdges',
  'BackupSnapshot',
  'CountVerticesByPrefix',
  'DeleteEdge',
  'DeleteEdges',
  'DeleteEdgesByPrefix',
  'DeleteVertex',
  'DeleteVertices',
  'DeleteVerticesByPrefix',
  'GetEdge',
  'GetEdges',
  'GetReplicationStatus',
  'GetServerStatus',
  'GetVertex',
  'GetVertices',
  'Illuminate',
  'PutEdge',
  'PutEdges',
  'PutVertex',
  'PutVertices',
  'ScanEdges',
  'ScanVertexKeys',
  'ScanVertices',
  'SearchVertices',
  'TopVerticesByDegree',
};
