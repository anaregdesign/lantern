import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:sqflite/sqflite.dart' as sqflite;

import 'support/contribution_change_witness.dart';
import 'support/head_edge_fixture.dart';
import 'support/receipt_attestation.dart';
import 'support/receipt_physical_fixture.dart';

const contributionTarget =
    'integration_test/physical_contribution_delete_test.dart';
const contributionScenarios = <String>{
  'authenticated_https',
  'invalid_contribution_identity',
  'indexed_duplicate_missing_expired',
  'selective_delete_preserves_put_base',
  'identity_cdc_edge_refetch',
  'advertised_contribution_delete_capability',
  'dispatch_marker_before_send',
  'committed_response_loss',
  'physical_sigkill_status_first',
  'original_true_false_replay',
  'no_mutation_resend_after_restart',
};
const _cleanups = {'graph', 'clients', 'fixture', 'journal', 'phase'};

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();
  test(
    'contribution Delete survives signed physical SIGKILL',
    () async {
      final run = ReceiptAttestation.fromBuild(
        target: contributionTarget,
        requiredScenarios: contributionScenarios,
        requiredRestartCleanups: _cleanups,
      );
      final directory = Directory(
        '${await sqflite.getDatabasesPath()}/contribution-${run.runId}',
      );
      final journal = File('${directory.path}/restart.json');
      final intent = File('${directory.path}/intent.json');
      final phase = File(
        '${Directory.systemTemp.path}/lantern-contribution-phase.json',
      );
      final fixture = PhysicalReceiptFixture.fromBuild();
      final direct = fixture.client(fixture.endpoint);
      final proxy = fixture.client(fixture.proxyEndpoint);
      final prefix = 'physical-contribution:${run.runId}:';
      final edge = EdgeRef('${prefix}t', '${prefix}h');
      EdgeContributionRef ref(int id, {String? head}) => EdgeContributionRef(
        tail: edge.tail,
        head: head ?? edge.head,
        contribId: physicalContributionId(run.runId, id),
      );
      void cleanups(ReceiptAttestation attestation, {required bool restarted}) {
        attestation.registerCleanup(() async {
          if (await phase.exists()) {
            await phase.delete();
          }
        }, restartObligation: restarted ? 'phase' : null);
        attestation.registerCleanup(
          () => directory.delete(recursive: true),
          restartObligation: restarted ? 'journal' : null,
        );
        attestation.registerCleanup(
          fixture.close,
          restartObligation: restarted ? 'fixture' : null,
        );
        attestation.registerCleanup(() async {
          await proxy.close();
          await direct.close();
        }, restartObligation: restarted ? 'clients' : null);
        attestation.registerCleanup(() async {
          await direct.deleteEdge(edge);
          await direct.deleteVertices(edgeEndpointKeys([edge]));
        }, restartObligation: restarted ? 'graph' : null);
      }

      if (await directory.exists()) {
        await run.resumeAfterRestart(journal, (attestation) async {
          cleanups(attestation, restarted: true);
          final raw = await intent.readAsBytes();
          expect(
            sha256.convert(raw).toString(),
            attestation.restartReceiptIdentitySha256,
          );
          final saved = jsonDecode(utf8.decode(raw)) as Map<String, dynamic>;
          expect(saved['mayHaveDispatched'], isTrue);
          final context = _context(saved);
          expect(saved['mutation'], 'edgeContributionDelete');
          final restored = EdgeContributionRef(
            tail: saved['tail'] as String,
            head: saved['head'] as String,
            contribId: base64Decode(saved['contribId'] as String),
          );
          expect(restored.tail, edge.tail);
          expect(restored.head, edge.head);
          expect(restored.contribId, ref(2).contribId);
          final before = await _trace(fixture);
          expect((before['trace'] as List).last, 'AwaitingSigkill');
          await attestation.verifyScenario(
            'physical_sigkill_status_first',
            () async {
              final statuses = await proxy.getReceiptStatuses(
                context.operationIds,
              );
              expect(
                statuses.every(
                  (status) => status.state == ReceiptStatusState.confirmed,
                ),
                isTrue,
              );
              expect(
                statuses.map(
                  (status) => (status.receipt! as EdgeContributionDeleteReceipt)
                      .existed,
                ),
                [true, false],
              );
            },
          );
          await attestation.verifyScenario(
            'no_mutation_resend_after_restart',
            () async {
              final after = await _trace(fixture);
              final events = after['trace'] as List;
              final boundary = events.indexOf('AwaitingSigkill');
              expect(boundary, greaterThanOrEqualTo(0));
              expect(events.lastIndexOf('AwaitingSigkill'), boundary);
              expect(
                events.sublist(boundary + 1),
                everyElement('GetReceiptStatuses'),
              );
              expect(after['failures'], 0);
              expect(
                _count(after, 'forwarded', 'DeleteEdgeContributions'),
                _count(before, 'forwarded', 'DeleteEdgeContributions'),
              );
              expect(
                _count(after, 'forwarded', 'GetReceiptStatuses'),
                greaterThan(_count(before, 'forwarded', 'GetReceiptStatuses')),
              );
              expect((await direct.getEdge(edge)).weight, 1);
            },
          );
          await attestation.verifyScenario(
            'original_true_false_replay',
            () async {
              final result = await direct.deleteEdgeContributionsWithReceipt([
                restored,
                restored,
              ], context: context);
              expect(result.map((result) => result.existed), [true, false]);
              expect((await direct.getEdge(edge)).weight, 1);
            },
          );
        });
        return;
      }
      await directory.create(recursive: true);
      await run.prepareForRestart(journal, (attestation) async {
        cleanups(attestation, restarted: false);
        await attestation.verifyScenario('authenticated_https', () async {
          await direct.ping();
          final anonymous = LanternClient.connect(fixture.endpoint);
          try {
            await expectLater(
              anonymous.getReceiptCapability(),
              throwsA(isA<LanternUnauthenticatedException>()),
            );
          } finally {
            await anonymous.close();
          }
          final capability =
              await direct.getReceiptCapability() as ReceiptCapabilityEnabled;
          expect(
            capability.supports(ReceiptMutationKind.edgeContributionDelete),
            isTrue,
          );
        });
        await attestation.verifyScenario(
          'invalid_contribution_identity',
          () async {
            for (final id in [Uint8List(23), Uint8List(24), Uint8List(25)]) {
              expect(
                () => EdgeContributionRef(
                  tail: edge.tail,
                  head: edge.head,
                  contribId: id,
                ),
                throwsA(isA<LanternInvalidArgumentException>()),
              );
            }
          },
        );
        await seedLiveEdgeEndpoints(direct, [edge]);
        await direct.putEdge(
          EdgeInput(tail: edge.tail, head: edge.head, weight: 1),
        );
        final seeded = await direct.addEdges([
          EdgeInput(
            tail: edge.tail,
            head: edge.head,
            weight: 2,
            contribId: ref(1).contribId,
          ),
          EdgeInput(
            tail: edge.tail,
            head: edge.head,
            weight: 3,
            contribId: ref(2).contribId,
          ),
          EdgeInput(
            tail: edge.tail,
            head: edge.head,
            weight: 7,
            contribId: ref(3).contribId,
            // An expired fixture must stay expired despite device clock skew.
            expiresAt: DateTime.utc(2000),
          ),
        ]);
        expect(seeded.effectiveWeights, [3, 6, 6]);
        expect((await direct.getEdge(edge)).weight, 6);
        final changes = ContributionChangeWitness(
          direct,
          prefix: prefix,
          edge: edge,
        );
        try {
          final checkpoint = await changes.ready.timeout(
            const Duration(seconds: 20),
          );
          expect(checkpoint.toBytes(), isNotEmpty);
          await attestation.verifyScenario(
            'indexed_duplicate_missing_expired',
            () async {
              final result = await direct.deleteEdgeContributions([
                ref(1),
                ref(1),
                ref(9),
                ref(3),
                ref(2, head: '${edge.head}:other'),
              ], batchSize: 2);
              expect(result.existed, [true, false, false, false, false]);
              expect(result.deleted, 1);
            },
          );
          await attestation.verifyScenario(
            'identity_cdc_edge_refetch',
            () async {
              final frame = await changes.invalidation.timeout(
                const Duration(seconds: 20),
              );
              final matching = frame.invalidations
                  .whereType<EdgeInvalidation>()
                  .where((item) => item.edge == edge);
              expect(matching, isNotEmpty);
              expect(matching.every((item) => item.current == null), isTrue);
              expect((await direct.getEdge(edge)).weight, 4);
            },
          );
        } finally {
          await changes.close();
        }
        await attestation.verifyScenario(
          'selective_delete_preserves_put_base',
          () async {
            expect((await direct.getEdge(edge)).weight, 4);
          },
        );
        await attestation.verifyScenario(
          'advertised_contribution_delete_capability',
          () async {
            final capability =
                await direct.getReceiptCapability() as ReceiptCapabilityEnabled;
            expect(
              capability.supportedMutations,
              contains(ReceiptMutationKind.edgeContributionDelete),
            );
          },
        );
        final capability =
            await proxy.getReceiptCapability() as ReceiptCapabilityEnabled;
        final context = proxy.mintReceiptContext(
          capability: capability,
          mutation: ReceiptMutationKind.edgeContributionDelete,
          itemCount: 2,
        );
        final saved = <String, Object>{
          'operationIds': context.operationIds
              .map((id) => base64Encode(id.bytes))
              .toList(),
          'groupId': base64Encode(context.groupId.bytes),
          'nodeId': base64Encode(context.endpoint.nodeId),
          'generation': base64Encode(context.endpoint.generation),
          'mayHaveDispatched': true,
          'mutation': 'edgeContributionDelete',
          'tail': edge.tail,
          'head': edge.head,
          'contribId': base64Encode(ref(2).contribId),
        };
        await attestation.verifyScenario(
          'dispatch_marker_before_send',
          () async {
            final temporary = File('${intent.path}.tmp');
            await temporary.writeAsString(jsonEncode(saved), flush: true);
            await temporary.rename(intent.path);
            expect(
              (jsonDecode(await intent.readAsString())
                  as Map)['mayHaveDispatched'],
              isTrue,
            );
          },
        );
        await attestation.verifyScenario('committed_response_loss', () async {
          await expectLater(
            proxy.deleteEdgeContributionsWithReceipt([
              ref(2),
              ref(2),
            ], context: context),
            throwsA(
              isA<ReceiptReconciliationException>().having(
                (error) => error.reason,
                'reason',
                ReceiptReconciliationReason.outcomeUnknown,
              ),
            ),
          );
          final trace = await _trace(fixture);
          expect(_count(trace, 'dropped', 'DeleteEdgeContributions'), 1);
          expect(_count(trace, 'forwarded', 'DeleteEdgeContributions'), 1);
          expect((await direct.getEdge(edge)).weight, 1);
          final statuses = await proxy.getReceiptStatuses(context.operationIds);
          expect(
            statuses.map(
              (status) =>
                  (status.receipt! as EdgeContributionDeleteReceipt).existed,
            ),
            [true, false],
          );
        });
        await fixture.sealProxyHandoff();
        return sha256.convert(await intent.readAsBytes()).toString();
      });
      await run.awaitSigkillOrFail(() async {
        await phase.writeAsString(
          jsonEncode({'phase': 'sigkill_now', 'runId': run.runId}),
          flush: true,
        );
      });
    },
    timeout: const Timeout(Duration(minutes: 30)),
  );
}

ReceiptContext _context(Map<String, dynamic> value) => ReceiptContext(
  operationIds: (value['operationIds'] as List).map(
    (id) => ReceiptOperationId(base64Decode(id as String)),
  ),
  groupId: ReceiptGroupId(base64Decode(value['groupId'] as String)),
  endpoint: ReceiptEndpoint(
    nodeId: base64Decode(value['nodeId'] as String),
    generation: base64Decode(value['generation'] as String),
  ),
  mutation: ReceiptMutationKind.edgeContributionDelete,
);

Future<Map<String, dynamic>> _trace(PhysicalReceiptFixture fixture) async {
  final http = HttpClient();
  try {
    final request = await http.getUrl(
      fixture.proxyEndpoint.resolve('/_receipt_matrix_status'),
    );
    request.headers.set(
      HttpHeaders.authorizationHeader,
      'Bearer ${await fixture.token()}',
    );
    final response = await request.close();
    expect(response.statusCode, HttpStatus.ok);
    return jsonDecode(await utf8.decodeStream(response))
        as Map<String, dynamic>;
  } finally {
    http.close(force: true);
  }
}

int _count(Map<String, dynamic> trace, String family, String rpc) =>
    (trace[family] as Map<String, dynamic>)[rpc] as int? ?? 0;
