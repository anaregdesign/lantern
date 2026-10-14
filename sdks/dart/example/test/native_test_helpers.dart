import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/protobuf.dart' as codec;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/native_credential_store.dart';
import 'package:lantern_example/native_providers.dart';

const issuer = 'https://accounts.google.com';
final endpoint = Uri.parse('https://example.test');
String random(int n) => base64UrlEncode(List.filled(32, n)).replaceAll('=', '');
String bytes(int n, [int size = 32]) => base64Encode(List.filled(size, n));

final class MemoryCredentials implements NativeCredentialStore {
  StoredNativeCredential? saved;
  final records = <String, List<NativeLogoutReview>>{};
  List<NativeRegistrationReview> registrationRecords = [];
  @override
  Future<List<NativeRegistrationReview>> registrations() async =>
      List.of(registrationRecords);
  @override
  Future<void> saveRegistrations(List<NativeRegistrationReview> reviews) async {
    registrationRecords = List.of(reviews);
  }

  @override
  Future<StoredNativeCredential?> read() async => saved;
  @override
  Future<void> write(StoredNativeCredential value) async {
    saved = value;
  }

  @override
  Future<void> clear() async {
    saved = null;
  }

  @override
  Future<List<NativeLogoutReview>> pending(String p) async =>
      List.of(records[p] ?? []);
  @override
  Future<void> savePending(String p, List<NativeLogoutReview> r) async {
    records[p] = List.of(r);
  }
}

final class Providers implements NativeProviders {
  String subject = 'a';
  bool appleUsable = true, appleFails = false;
  Completer<NativeProviderCredential>? held;
  final called = Completer<void>();
  var authentications = 0;
  @override
  Future<NativeProviderCredential> authenticate(
    NativePluginConfiguration c, {
    bool lightweight = false,
  }) async {
    authentications++;
    if (!called.isCompleted) called.complete();
    if (held case final pending?) return pending.future;
    return c.kind == NativeProviderKind.google
        ? GoogleNativeCredential(idToken: subject)
        : AppleNativeCredential(
            identityToken: subject,
            authorizationCode: 'one-code',
            state: c.state!,
          );
  }

  @override
  Future<void> signOutGoogle() async {}
  @override
  Future<bool> appleCredentialUsable(String subject) async {
    if (appleFails) throw StateError('Apple local credential-state failed');
    return appleUsable;
  }
}

// Uses the public Connect codec/transport only. The app and its tests never
// import generated SDK classes; JSON here is explicit wire fixture evidence.
final class NativeWire implements connect.Transport {
  NativeWire(this.store, {this.providerIssuer = issuer});
  final String providerIssuer;
  final MemoryCredentials store;
  final now = DateTime.now().toUtc().subtract(const Duration(minutes: 1));
  final subjects = <String, String>{};
  int completions = 0, applies = 0, statuses = 0;
  int registrationPrepares = 0,
      registrationApplies = 0,
      registrationStatuses = 0;
  bool loseRegistrationResponse = false, registrationUnknown = false;
  final registered = <String>{};
  Map<String, dynamic>? recoveryReview;
  String uid(String subject) => subject == 'a'
      ? 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
      : 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
  int authorityBinding = 13;
  int sessionReads = 0, holdSessionRead = 0;
  final sessionBlocked = Completer<void>(), releaseSession = Completer<void>();
  final sent = <String?>[];
  final receiptStatuses = <(String, Object?)>[];
  final dataDispatches = <String>[];
  final profile = <String, Object>{
    'version': 2,
    'domain': bytes(1),
    'cohort': bytes(2),
    'generation': bytes(3, 16),
    'protocol': bytes(4),
    'timeProfile': bytes(5),
    'membership': bytes(6),
    'configuration': bytes(7),
  };
  Map<String, Object> get cut => {
    'version': 1,
    'domain': bytes(1),
    'cohort': bytes(2),
    'generation': bytes(3, 16),
    'sequence': '1',
    'previous': bytes(8),
    'projection': bytes(9),
    'frontier': bytes(10),
    'fences': bytes(11),
    'policy': bytes(12),
  };
  Map<String, Object> identity(String subject) => {
    'kind': 1,
    'issuer': providerIssuer,
    'subject': subject,
  };
  Map<String, Object> session(String subject) => {
    'credentialProfile': 'native_end_user',
    if (registered.contains(subject)) 'userId': uid(subject),
    if (registered.contains(subject))
      'personalPrefix': 'users:${uid(subject)}:',
    'clientProfileId': 'mobile',
    'serviceOrigin': endpoint.origin,
    'identity': identity(subject),
    'createdAt': now.toIso8601String(),
    'expiresAt': now.add(const Duration(hours: 8)).toIso8601String(),
    'version': {
      'currentProfile': profile,
      'currentCut': cut,
      'admissionBinding': bytes(authorityBinding),
    },
  };
  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    const json = codec.JsonCodec();
    final req =
        jsonDecode(utf8.decode(json.encode(input))) as Map<String, dynamic>;
    final authorization = options?.headers?['authorization'];
    sent.add(authorization);
    final credential = authorization?.replaceFirst('Bearer ', '');
    final subject = subjects[credential] ?? '';
    Map<String, Object?> response;
    switch (spec.procedure.split('/').last) {
      case 'BeginNativeLogin':
      case 'BeginNativeRegistration':
        recoveryReview = req['recoveryReview'] as Map<String, dynamic>?;
        expect(authorization, isNull);
        response = {
          'attemptId': random(1),
          'completionSecret': random(2),
          'affinity': 'v2.1.${random(3)}',
          'expiresAt': now.add(const Duration(minutes: 10)).toIso8601String(),
          'configuration': {
            'kind': providerIssuer == issuer ? 1 : 2,
            if (providerIssuer != issuer) 'nonce': random(4),
            if (providerIssuer != issuer)
              'state': 'v2.1.${random(3)}.${random(5)}',
            'clientProfileId': 'mobile',
            'clientId': 'backend',
            'serviceOrigin': endpoint.origin,
          },
        };
        if (spec.procedure.endsWith('/BeginNativeRegistration')) {
          response = {'attempt': response};
        }
      case 'CompleteNativeLogin':
        expect(authorization, isNull);
        completions++;
        final subject =
            (providerIssuer == issuer
                    ? (req['google'] as Map<String, dynamic>)['idToken']
                    : (req['apple'] as Map<String, dynamic>)['identityToken'])
                as String;
        final issued = 'lnu1_${random(20 + completions)}';
        subjects[issued] = subject;
        response = {'credential': issued, 'session': session(subject)};
      case 'PrepareNativeRegistration':
        expect(authorization, isNull);
        registrationPrepares++;
        final registering =
            (providerIssuer == issuer
                    ? (req['google'] as Map<String, dynamic>)['idToken']
                    : (req['apple'] as Map<String, dynamic>)['identityToken'])
                as String;
        Map<String, dynamic> review;
        if (recoveryReview case final original?) {
          expect(req['recoveryReview'], original);
          if ((original['actor'] as Map<String, dynamic>)['subject'] !=
              registering) {
            throw connect.ConnectException(
              connect.Code.permissionDenied,
              'another account',
            );
          }
          review = original;
        } else {
          review = {
            'profile': profile,
            'expectedCut': cut,
            'changeId': {
              'version': 1,
              'domain': bytes(1),
              'cohort': bytes(2),
              'namespace': '1',
              'nonce': bytes(70 + registrationPrepares, 16),
            },
            'actor': identity(registering),
            'intentDigest': bytes(72),
            'userId': uid(registering),
            'clientProfileId': 'mobile',
            'issuerConfigRevision': '1',
          };
        }
        response = {
          'review': review,
          'proof': random(71),
          'affinity': 'v2.1.${random(3)}',
          'expiresAt': now.add(const Duration(minutes: 10)).toIso8601String(),
        };
      case 'ApplyNativeRegistration':
        expect(authorization, isNull);
        expect(
          store.registrationRecords,
          isNotEmpty,
          reason: 'review must be durable before Apply',
        );
        registrationApplies++;
        final review = req['review'] as Map<String, dynamic>;
        final registering =
            (review['actor'] as Map<String, dynamic>)['subject'] as String;
        registered.add(registering);
        final issued = 'lnu1_${random(100 + registrationApplies)}';
        subjects[issued] = registering;
        if (loseRegistrationResponse) {
          throw connect.ConnectException(
            connect.Code.unavailable,
            'lost registration response',
          );
        }
        response = {
          'currentResult': registrationStatus(review),
          'credential': issued,
          'session': session(registering),
        };
      case 'GetNativeRegistrationStatus':
        expect(authorization, isNull);
        registrationStatuses++;
        response = {
          'currentResult': registrationStatus(
            req['review'] as Map<String, dynamic>,
          ),
        };
      case 'GetNativeSession':
        if (++sessionReads == holdSessionRead) {
          sessionBlocked.complete();
          await releaseSession.future;
        }
        if (subject.isEmpty) {
          throw connect.ConnectException(
            connect.Code.unauthenticated,
            'unknown',
          );
        }
        response = {'session': session(subject)};
      case 'PrepareNativeLogout':
        response = {
          'review': {
            'profile': profile,
            'expectedCut': cut,
            'changeId': {
              'version': 1,
              'domain': bytes(1),
              'cohort': bytes(2),
              'namespace': '1',
              'nonce': bytes(31 + applies, 16),
            },
            'actor': identity(subject),
            'intentDigest': bytes(30),
            'sessionDigest': List.filled(64, 'a').join(),
            'sessionLineage': '1',
          },
        };
      case 'ApplyNativeLogout':
        expect(
          store.records.values.expand((r) => r),
          isNotEmpty,
          reason: 'retain review before dispatch',
        );
        applies++;
        throw connect.ConnectException(
          connect.Code.unavailable,
          'response lost',
        );
      case 'GetReceiptStatuses':
        receiptStatuses.add((subject, req['operationIds']));
        throw connect.ConnectException(
          connect.Code.unavailable,
          'original receipt status unavailable',
        );
      case 'PutVertices':
      case 'DeleteEdges':
        dataDispatches.add(subject);
        throw StateError('Uncertain operation must use status first');
      case 'GetSecurityChangeStatus':
        statuses++;
        response = {
          'currentResult': {
            'profile': req['currentProfile'],
            'changeId': req['currentChangeId'],
            'intentDigest': req['currentIntentDigest'],
            'original': {
              'changeId': req['currentChangeId'],
              'intentDigest': req['currentIntentDigest'],
              'disposition': 1,
            },
          },
        };
      default:
        throw StateError('Unexpected test RPC');
    }
    final output = spec.outputFactory();
    json.decode(Uint8List.fromList(utf8.encode(jsonEncode(response))), output);
    return connect.UnaryResponse(
      spec,
      connect.Headers(),
      output,
      connect.Headers(),
    );
  }

  Map<String, Object?> registrationStatus(Map<String, dynamic> review) => {
    'profile': review['profile'],
    'changeId': review['changeId'],
    'intentDigest': review['intentDigest'],
    if (!registrationUnknown)
      'original': {
        'changeId': review['changeId'],
        'intentDigest': review['intentDigest'],
        'disposition': 1,
      },
  };

  @override
  Future<connect.StreamResponse<I, O>>
  stream<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    Stream<I> input, [
    connect.CallOptions? options,
  ]) => throw UnimplementedError();
}
