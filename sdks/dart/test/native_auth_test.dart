import 'dart:async';
import 'dart:convert';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/test.dart';
import 'package:fixnum/fixnum.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client/src/gen/google/protobuf/timestamp.pb.dart';
import 'package:lantern_client/src/gen/graph/v1/native_auth.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/native_auth.pb.dart' as wire;
import 'package:lantern_client/src/gen/graph/v1/security.connect.spec.dart';
import 'package:lantern_client/src/gen/graph/v1/security.pb.dart' as security;
import 'package:test/test.dart';

import 'security_test.dart' show version;

String random(int n) => base64UrlEncode(List.filled(32, n)).replaceAll('=', '');
const google = 'https://accounts.google.com';
final endpoint = Uri.parse('https://example.test');

wire.BeginNativeLoginResponse start({bool apple = false}) =>
    wire.BeginNativeLoginResponse(
      attemptId: random(1),
      completionSecret: random(2),
      affinity: 'v2.1.${random(3)}',
      expiresAt: Timestamp.fromDateTime(
        DateTime.now().add(const Duration(minutes: 10)),
      ),
      configuration: wire.NativePluginConfiguration(
        kind: apple
            ? security.NativeClientKind.NATIVE_CLIENT_KIND_APPLE_NATIVE
            : security.NativeClientKind.NATIVE_CLIENT_KIND_GOOGLE_ID_TOKEN,
        clientProfileId: 'mobile',
        clientId: 'backend-client',
        serviceOrigin: endpoint.origin,
        nonce: apple ? random(4) : '',
        state: apple ? 'v2.1.${random(3)}.${random(5)}' : '',
      ),
    );
wire.NativeSession session() => wire.NativeSession(
  credentialProfile: 'native_end_user',
  clientProfileId: 'mobile',
  serviceOrigin: endpoint.origin,
  identity: security.SecurityIdentity(
    kind: security.SecurityPrincipalKind.SECURITY_PRINCIPAL_KIND_OIDC,
    issuer: google,
    subject: 'exact-subject',
  ),
  createdAt: Timestamp.fromDateTime(DateTime.utc(2026, 10, 13)),
  expiresAt: Timestamp.fromDateTime(DateTime.utc(2026, 10, 13, 8)),
  version: version(),
);
security.CurrentSessionRevocationReview review() {
  final v = version();
  return security.CurrentSessionRevocationReview(
    profile: v.currentProfile,
    expectedCut: v.currentCut,
    changeId: security.CurrentSecurityChangeID(
      version: 1,
      domain: v.currentProfile.domain,
      cohort: v.currentProfile.cohort,
      namespace: Int64.ONE,
      nonce: List.filled(16, 8),
    ),
    actor: session().identity,
    intentDigest: List.filled(32, 9),
    sessionDigest: List.filled(64, 'a').join(),
    sessionLineage: Int64.ONE,
  );
}

void main() {
  test(
    'bootstrap suppresses old bearer; session uses TokenProvider and original auth time',
    () async {
      var providerCalls = 0, completes = 0;
      final client = LanternClient.connect(
        endpoint,
        tokenProvider: () {
          providerCalls++;
          return 'lnu1_${random(7)}';
        },
        retryPolicy: const RetryPolicy(),
        transport: FakeTransportBuilder()
            .unary(LanternNativeAuthService.beginNativeLogin, (
              wire.BeginNativeLoginRequest req,
              ctx,
            ) {
              expect(ctx.requestHeaders['authorization'], isNull);
              return start();
            })
            .unary(LanternNativeAuthService.completeNativeLogin, (
              wire.CompleteNativeLoginRequest req,
              ctx,
            ) {
              completes++;
              expect(ctx.requestHeaders['authorization'], isNull);
              expect(req.google.idToken, 'provider-secret');
              return wire.CompleteNativeLoginResponse(
                credential: 'lnu1_${random(7)}',
                session: session(),
              );
            })
            .unary(LanternNativeAuthService.getNativeSession, (
              wire.GetNativeSessionRequest req,
              ctx,
            ) {
              expect(
                ctx.requestHeaders['authorization'],
                'Bearer lnu1_${random(7)}',
              );
              return wire.GetNativeSessionResponse(session: session());
            })
            .build(),
      );
      addTearDown(client.close);
      for (var n = 0; n < 3; n++) {
        final attempt = await client.beginNativeLogin(
          issuer: google,
          clientProfileId: 'mobile',
        );
        expect(attempt.configuration.nonce, isNull);
        expect(attempt.toString(), isNot(contains(random(2))));
        final result = await client.completeNativeLogin(
          attempt,
          const GoogleNativeCredential(idToken: 'provider-secret'),
        );
        expect(result.session.authTime, isNull);
        expect(result.toString(), isNot(contains(result.credential)));
      }
      expect(providerCalls, 0);
      expect(completes, 3);
      expect(
        (await client.getNativeSession()).session.principal.subject,
        'exact-subject',
      );
      expect(providerCalls, 1);
    },
  );
  test(
    'response loss is attempted once and all native error metadata is redacted',
    () async {
      var calls = 0;
      final client = LanternClient.connect(
        endpoint,
        tokenProvider: () => throw StateError('must not call'),
        retryPolicy: const RetryPolicy(maxAttempts: 4),
        transport: FakeTransportBuilder()
            .unary(
              LanternNativeAuthService.beginNativeLogin,
              (wire.BeginNativeLoginRequest req, ctx) => start(),
            )
            .unary(LanternNativeAuthService.completeNativeLogin, (
              wire.CompleteNativeLoginRequest req,
              ctx,
            ) {
              calls++;
              ctx.responseHeaders.add('provider', 'provider-secret');
              throw connect.ConnectException(
                connect.Code.unavailable,
                'provider-secret ${req.completionSecret}',
                metadata: connect.Headers()..add('leak', 'provider-secret'),
                cause: 'provider-secret',
              );
            })
            .build(),
      );
      addTearDown(client.close);
      final attempt = await client.beginNativeLogin(
        issuer: google,
        clientProfileId: 'mobile',
      );
      try {
        await client.completeNativeLogin(
          attempt,
          const GoogleNativeCredential(idToken: 'provider-secret'),
        );
        fail('expected loss');
      } on LanternUnavailableException catch (e) {
        expect(e.toString(), isNot(contains('provider-secret')));
        expect(e.headers, isEmpty);
        expect(e.metadata, isEmpty);
        expect(e.cause, isNull);
      }
      expect(calls, 1);
    },
  );
  test(
    'wrong endpoint/profile/origin and malformed metadata fail before installation',
    () async {
      var response = start();
      final transport = FakeTransportBuilder()
          .unary(
            LanternNativeAuthService.beginNativeLogin,
            (wire.BeginNativeLoginRequest req, ctx) => response,
          )
          .build();
      final client = LanternClient.connect(endpoint, transport: transport);
      addTearDown(client.close);
      final attempt = await client.beginNativeLogin(
        issuer: google,
        clientProfileId: 'mobile',
      );
      final other = LanternClient.connect(
        Uri.parse('https://other.test'),
        transport: transport,
      );
      addTearDown(other.close);
      await expectLater(
        other.completeNativeLogin(
          attempt,
          const GoogleNativeCredential(idToken: 'secret'),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      await expectLater(
        client.completeNativeLogin(
          attempt,
          const AppleNativeCredential(
            identityToken: 'secret',
            authorizationCode: 'code',
            state: 'state',
          ),
        ),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
      response = start()..configuration.serviceOrigin = 'https://other.test';
      await expectLater(
        client.beginNativeLogin(issuer: google, clientProfileId: 'mobile'),
        throwsA(isA<LanternFailedPreconditionException>()),
      );
      response = start()..configuration.nonce = random(4);
      await expectLater(
        client.beginNativeLogin(issuer: google, clientProfileId: 'mobile'),
        throwsA(isA<LanternFailedPreconditionException>()),
      );
      final insecure = LanternClient.connect(
        Uri.parse('http://localhost'),
        allowInsecure: true,
        transport: transport,
      );
      addTearDown(insecure.close);
      await expectLater(
        insecure.beginNativeLogin(issuer: google, clientProfileId: 'mobile'),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );
  test(
    'logout retains exact identity across serialization and status-first uncertainty',
    () async {
      final prepared = review();
      var applyCalls = 0, statusCalls = 0;
      final client = LanternClient.connect(
        endpoint,
        tokenProvider: () => 'lnu1_${random(7)}',
        transport: FakeTransportBuilder()
            .unary(
              LanternNativeAuthService.prepareNativeLogout,
              (wire.PrepareNativeLogoutRequest req, ctx) =>
                  wire.PrepareNativeLogoutResponse(review: prepared),
            )
            .unary(LanternNativeAuthService.applyNativeLogout, (
              wire.ApplyNativeLogoutRequest req,
              ctx,
            ) {
              applyCalls++;
              expect(req.review, prepared);
              return wire.ApplyNativeLogoutResponse(clearLocalSession: true);
            })
            .unary(LanternSecurityService.getSecurityChangeStatus, (
              security.GetSecurityChangeStatusRequest req,
              ctx,
            ) {
              statusCalls++;
              expect(req.currentChangeId, prepared.changeId);
              expect(req.currentIntentDigest, prepared.intentDigest);
              return security.GetSecurityChangeStatusResponse(
                currentResult: security.CurrentSecurityChangeResult(
                  profile: prepared.profile,
                  changeId: prepared.changeId,
                  intentDigest: prepared.intentDigest,
                  original: security.CurrentSecurityOriginalOutcome(
                    changeId: prepared.changeId,
                    intentDigest: prepared.intentDigest,
                    disposition: security
                        .CurrentSecurityDisposition
                        .CURRENT_SECURITY_DISPOSITION_APPLIED,
                  ),
                ),
              );
            })
            .build(),
      );
      addTearDown(client.close);
      final original = await client.prepareNativeLogout();
      final restored = NativeLogoutReview.fromBytes(original.toBytes());
      expect(
        (await client.applyNativeLogout(restored)).outcome,
        NativeLogoutOutcome.unresolved,
      );
      expect(
        await client.getNativeLogoutStatus(restored),
        NativeLogoutOutcome.applied,
      );
      expect(applyCalls, 1);
      expect(statusCalls, 1);
      expect(
        () => NativeLogoutReview.fromBytes(List.filled(40000, 0)),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    },
  );
  test(
    'native calls observe cancellation without invoking old token provider',
    () async {
      final started = Completer<void>();
      final client = LanternClient.connect(
        endpoint,
        tokenProvider: () => throw StateError('old credential'),
        transport: FakeTransportBuilder().unary(
          LanternNativeAuthService.beginNativeLogin,
          (wire.BeginNativeLoginRequest req, ctx) async {
            started.complete();
            throw await ctx.signal.future;
          },
        ).build(),
      );
      addTearDown(client.close);
      final cancellation = LanternCancellationToken();
      final result = client.beginNativeLogin(
        issuer: google,
        clientProfileId: 'mobile',
        options: LanternCallOptions(cancellation: cancellation),
      );
      final assertion = expectLater(
        result,
        throwsA(isA<LanternCanceledException>()),
      );
      await started.future;
      cancellation.cancel('private reason');
      await assertion;
    },
  );
}
