import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/security.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/security.pb.dart' as wire;
import 'package:test/test.dart';

wire.SecurityVersion version() {
  List<int> b(int value, [int size = 32]) => List.filled(size, value);
  return wire.SecurityVersion(
    currentProfile: wire.CurrentAuthorityProfile(
      version: 2,
      domain: b(1),
      cohort: b(2),
      generation: b(3, 16),
      protocol: b(4),
      timeProfile: b(5),
      membership: b(6),
      configuration: b(7),
    ),
    currentCut: wire.CurrentSemanticCut(
      version: 1,
      domain: b(1),
      cohort: b(2),
      generation: b(3, 16),
      sequence: Int64(3),
      previous: b(8),
      projection: b(9),
      frontier: b(10),
      fences: b(11),
      policy: b(12),
    ),
    admissionBinding: b(13),
  );
}

void main() {
  test(
    'current inspection preserves full cache binding and refuses scalar or malformed versions',
    () async {
      var current = version(), calls = 0;
      final client = LanternClient.connect(
        Uri.parse('https://example.test'),
        tokenProvider: () => 'current-fixture',
        transport: FakeTransportBuilder()
            .unary<
              wire.GetCurrentPrincipalRequest,
              wire.GetCurrentPrincipalResponse
            >(LanternSecurityService.getCurrentPrincipal, (request, context) {
              calls++;
              return wire.GetCurrentPrincipalResponse(version: current);
            })
            .build(),
      );
      addTearDown(client.close);
      final original = await client.getCurrentAuthorityBinding();
      expect(original, startsWith('current-v2:'));
      current = version()..admissionBinding = List.filled(32, 99);
      expect(await client.getCurrentAuthorityBinding(), isNot(original));
      current = version();
      current.currentCut.frontier = List.filled(32, 99);
      expect(await client.getCurrentAuthorityBinding(), isNot(original));
      current = version();
      current.currentProfile.timeProfile = List.filled(32, 99);
      expect(await client.getCurrentAuthorityBinding(), isNot(original));
      for (final change in <void Function(wire.SecurityVersion)>[
        (v) => v.revision = Int64.ONE,
        (v) => v.currentProfile.version = 3,
        (v) => v.currentCut.generation = List.filled(16, 99),
        (v) => v.clearAdmissionBinding(),
      ]) {
        current = version();
        change(current);
        await expectLater(
          client.getCurrentAuthorityBinding(),
          throwsA(isA<LanternFailedPreconditionException>()),
        );
      }
      expect(calls, 8);
    },
  );
}
