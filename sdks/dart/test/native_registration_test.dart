import 'dart:convert';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/graph/v1/native_auth.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/native_auth.pb.dart' as wire;
import 'package:lantern_client/src/gen/graph/v1/security.pb.dart' as security;
import 'package:test/test.dart';

import 'native_auth_test.dart' as fixtures;

const uid = '0123456789abcdef0123456789abcdef';
wire.NativeRegistrationReview registrationReview() {
  final original = fixtures.review();
  return wire.NativeRegistrationReview(
    profile: original.profile,
    expectedCut: original.expectedCut,
    changeId: original.changeId,
    actor: original.actor,
    intentDigest: original.intentDigest,
    userId: uid,
    clientProfileId: 'mobile',
    issuerConfigRevision: Int64.ONE,
  );
}

security.CurrentSecurityChangeResult status(
  wire.NativeRegistrationReview review, {
  bool applied = true,
}) => security.CurrentSecurityChangeResult(
  profile: review.profile,
  changeId: review.changeId,
  intentDigest: review.intentDigest,
  original: applied
      ? security.CurrentSecurityOriginalOutcome(
          changeId: review.changeId,
          intentDigest: review.intentDigest,
          disposition: security
              .CurrentSecurityDisposition
              .CURRENT_SECURITY_DISPOSITION_APPLIED,
        )
      : null,
);

void main() {
  test('registration begin rejects an absent provider attempt', () async {
    final client = LanternClient.connect(
      fixtures.endpoint,
      transport: FakeTransportBuilder()
          .unary(
            LanternNativeAuthService.beginNativeRegistration,
            (wire.BeginNativeRegistrationRequest req, ctx) =>
                wire.BeginNativeRegistrationResponse(),
          )
          .build(),
    );
    addTearDown(client.close);
    await expectLater(
      client.beginNativeRegistration(
        issuer: fixtures.google,
        clientProfileId: 'mobile',
      ),
      throwsA(isA<LanternFailedPreconditionException>()),
    );
  });
  test(
    'signup suppresses old bearer, preserves exact review and returns installed personal area',
    () async {
      final review = registrationReview(), start = fixtures.start();
      final client = LanternClient.connect(
        fixtures.endpoint,
        tokenProvider: () =>
            throw StateError('old account must not authorize registration'),
        transport: FakeTransportBuilder()
            .unary(LanternNativeAuthService.beginNativeRegistration, (
              wire.BeginNativeRegistrationRequest req,
              ctx,
            ) {
              expect(req.hasRecoveryReview(), isFalse);
              expect(ctx.requestHeaders['authorization'], isNull);
              return wire.BeginNativeRegistrationResponse(attempt: start);
            })
            .unary(LanternNativeAuthService.prepareNativeRegistration, (
              wire.PrepareNativeRegistrationRequest req,
              ctx,
            ) {
              expect(req.google.idToken, 'provider-secret');
              expect(req.hasRecoveryReview(), isFalse);
              expect(
                ctx.requestHeaders['Lantern-Native-Affinity'],
                start.affinity,
              );
              return wire.PrepareNativeRegistrationResponse(
                review: review,
                proof: fixtures.random(7),
                affinity: start.affinity,
                expiresAt: start.expiresAt,
              );
            })
            .unary(LanternNativeAuthService.applyNativeRegistration, (
              wire.ApplyNativeRegistrationRequest req,
              ctx,
            ) {
              expect(req.review, review);
              expect(req.proof, fixtures.random(7));
              return wire.ApplyNativeRegistrationResponse(
                currentResult: status(review),
                credential: 'lnu1_${fixtures.random(8)}',
                session: fixtures.session()
                  ..userId = uid
                  ..personalPrefix = 'users:$uid:',
              );
            })
            .build(),
      );
      addTearDown(client.close);
      final attempt = await client.beginNativeRegistration(
        issuer: fixtures.google,
        clientProfileId: 'mobile',
      );
      final prepared = await client.prepareNativeRegistration(
        attempt,
        const GoogleNativeCredential(idToken: 'provider-secret'),
      );
      final restored = NativeRegistrationReview.fromBytes(
        prepared.review.toBytes(),
      );
      expect(restored.toBytes(), prepared.review.toBytes());
      expect(restored.principal.subject, 'exact-subject');
      expect(prepared.toString(), isNot(contains(fixtures.random(7))));
      final result = await client.applyNativeRegistration(prepared);
      expect(result.outcome, NativeRegistrationOutcome.applied);
      expect(result.signInRequired, isFalse);
      expect(result.sessionCredential!.session.userId, uid);
      expect(result.sessionCredential!.session.personalPrefix, 'users:$uid:');
      expect(result.sessionCredential!.session.authTime, isNull);
    },
  );

  test(
    'Apply loss stays one dispatch; fresh recovery binds original and cannot Apply',
    () async {
      final review = registrationReview(), start = fixtures.start();
      var applies = 0, statuses = 0, prepares = 0;
      final client = LanternClient.connect(
        fixtures.endpoint,
        retryPolicy: const RetryPolicy(maxAttempts: 4),
        transport: FakeTransportBuilder()
            .unary(LanternNativeAuthService.beginNativeRegistration, (
              wire.BeginNativeRegistrationRequest req,
              ctx,
            ) {
              if (prepares != 0) expect(req.recoveryReview, review);
              return wire.BeginNativeRegistrationResponse(attempt: start);
            })
            .unary(LanternNativeAuthService.prepareNativeRegistration, (
              wire.PrepareNativeRegistrationRequest req,
              ctx,
            ) {
              if (prepares++ != 0) expect(req.recoveryReview, review);
              return wire.PrepareNativeRegistrationResponse(
                review: review,
                proof: fixtures.random(7),
                affinity: start.affinity,
                expiresAt: start.expiresAt,
              );
            })
            .unary(LanternNativeAuthService.applyNativeRegistration, (
              wire.ApplyNativeRegistrationRequest req,
              ctx,
            ) {
              applies++;
              throw connect.ConnectException(
                connect.Code.unavailable,
                'secret ${req.proof}',
              );
            })
            .unary(LanternNativeAuthService.getNativeRegistrationStatus, (
              wire.GetNativeRegistrationStatusRequest req,
              ctx,
            ) {
              statuses++;
              expect(req.review, review);
              return wire.GetNativeRegistrationStatusResponse(
                currentResult: status(review, applied: false),
              );
            })
            .build(),
      );
      addTearDown(client.close);
      final attempt = await client.beginNativeRegistration(
        issuer: fixtures.google,
        clientProfileId: 'mobile',
      );
      final prepared = await client.prepareNativeRegistration(
        attempt,
        const GoogleNativeCredential(idToken: 'provider-secret'),
      );
      await expectLater(
        client.applyNativeRegistration(prepared),
        throwsA(
          isA<LanternUnavailableException>().having(
            (e) => e.toString(),
            'redacted',
            isNot(contains('secret')),
          ),
        ),
      );
      final original = NativeRegistrationReview.fromBytes(
        prepared.review.toBytes(),
      );
      final recovery = await client.beginNativeRegistration(
        issuer: fixtures.google,
        clientProfileId: 'mobile',
        recoveryReview: original,
      );
      final recovered = await client.prepareNativeRegistration(
        recovery,
        const GoogleNativeCredential(idToken: 'fresh-provider-secret'),
      );
      expect(recovered.recoveryOnly, isTrue);
      await expectLater(
        client.applyNativeRegistration(recovered),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      expect(
        await client.getNativeRegistrationStatus(recovered),
        NativeRegistrationOutcome.unresolved,
      );
      expect((applies, statuses), (1, 1));
    },
  );

  test(
    'recovery rejects changed original, endpoint and profile before mutation',
    () async {
      final original = registrationReview(), start = fixtures.start();
      var returned = registrationReview()..actor.subject = 'another-account';
      final client = LanternClient.connect(
        fixtures.endpoint,
        transport: FakeTransportBuilder()
            .unary(
              LanternNativeAuthService.beginNativeRegistration,
              (wire.BeginNativeRegistrationRequest req, ctx) =>
                  wire.BeginNativeRegistrationResponse(attempt: start),
            )
            .unary(
              LanternNativeAuthService.prepareNativeRegistration,
              (wire.PrepareNativeRegistrationRequest req, ctx) =>
                  wire.PrepareNativeRegistrationResponse(
                    review: returned,
                    proof: fixtures.random(7),
                    affinity: start.affinity,
                    expiresAt: start.expiresAt,
                  ),
            )
            .build(),
      );
      addTearDown(client.close);
      final saved = NativeRegistrationReview.fromBytes(
        utf8.encode(
          jsonEncode({
            'version': 1,
            'endpoint': fixtures.endpoint.toString(),
            'review': base64Encode(original.writeToBuffer()),
          }),
        ),
      );
      await expectLater(
        client.beginNativeRegistration(
          issuer: fixtures.google,
          clientProfileId: 'other',
          recoveryReview: saved,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      final attempt = await client.beginNativeRegistration(
        issuer: fixtures.google,
        clientProfileId: 'mobile',
        recoveryReview: saved,
      );
      await expectLater(
        client.prepareNativeRegistration(
          attempt,
          const GoogleNativeCredential(idToken: 'provider-secret'),
        ),
        throwsA(isA<LanternFailedPreconditionException>()),
      );
      returned = registrationReview()..intentDigest = List.filled(32, 99);
      await expectLater(
        client.prepareNativeRegistration(
          attempt,
          const GoogleNativeCredential(idToken: 'provider-secret'),
        ),
        throwsA(isA<LanternFailedPreconditionException>()),
      );
      final other = LanternClient.connect(Uri.parse('https://other.test'));
      addTearDown(other.close);
      await expectLater(
        other.beginNativeRegistration(
          issuer: fixtures.google,
          clientProfileId: 'mobile',
          recoveryReview: saved,
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );
}
