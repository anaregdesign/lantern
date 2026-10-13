import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:google_sign_in_platform_interface/google_sign_in_platform_interface.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/native_providers.dart';
import 'package:sign_in_with_apple_platform_interface/sign_in_with_apple_platform_interface.dart';

import 'native_test_helpers.dart'
    show MemoryCredentials, NativeWire, endpoint, issuer;

final class GooglePlatform extends GoogleSignInPlatform {
  final initializations = <InitParameters>[];
  int interactive = 0, restored = 0, signouts = 0;
  static const result = AuthenticationResults(
    user: GoogleSignInUserData(email: 'unused@example.test', id: 'subject'),
    authenticationTokens: AuthenticationTokenData(
      idToken: 'same-valid-id-token',
    ),
  );
  @override
  Future<void> init(InitParameters params) async => initializations.add(params);
  @override
  Future<AuthenticationResults> authenticate(
    AuthenticateParameters params,
  ) async {
    interactive++;
    return result;
  }

  @override
  Future<AuthenticationResults?> attemptLightweightAuthentication(
    AttemptLightweightAuthenticationParameters params,
  ) async {
    restored++;
    return result;
  }

  @override
  Future<void> signOut(SignOutParams params) async {
    signouts++;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => throw StateError(
    'Unexpected provider authorization call: ${invocation.memberName}',
  );
}

final class ApplePlatform extends SignInWithApplePlatform {
  CredentialState state = CredentialState.authorized;
  bool fail = false;
  final subjects = <String>[];
  @override
  Future<CredentialState> getCredentialState(String userIdentifier) async {
    subjects.add(userIdentifier);
    if (fail) throw StateError('Native plugin unavailable');
    return state;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test(
    'actual plugin adapter initializes once across interactive and restored Google login',
    () async {
      final platform = GooglePlatform(),
          previous = GoogleSignInPlatform.instance;
      GoogleSignInPlatform.instance = platform;
      debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
      addTearDown(() {
        GoogleSignInPlatform.instance = previous;
        debugDefaultTargetPlatformOverride = null;
      });
      final client = LanternClient.connect(
        endpoint,
        transport: NativeWire(MemoryCredentials()),
      );
      addTearDown(client.close);
      final configuration = (await client.beginNativeLogin(
        issuer: issuer,
        clientProfileId: 'mobile',
      )).configuration;
      final providers = PlatformNativeProviders.instance;
      for (final lightweight in [false, false, true]) {
        expect(
          await providers.authenticate(configuration, lightweight: lightweight),
          isA<GoogleNativeCredential>(),
        );
      }
      await providers.signOutGoogle();
      expect(platform.initializations, hasLength(1));
      expect(
        platform.initializations.single.serverClientId,
        configuration.clientId,
      );
      expect(platform.initializations.single.nonce, isNull);
      expect(platform.interactive, 2);
      expect(platform.restored, 1);
      expect(platform.signouts, 1);
    },
  );
  test(
    'Apple adapter preserves rejected local states and plugin errors',
    () async {
      final platform = ApplePlatform(),
          previous = SignInWithApplePlatform.instance;
      SignInWithApplePlatform.instance = platform;
      debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
      addTearDown(() {
        SignInWithApplePlatform.instance = previous;
        debugDefaultTargetPlatformOverride = null;
      });
      final providers = PlatformNativeProviders.instance;
      expect(await providers.appleCredentialUsable('exact-subject'), isTrue);
      for (final state in [CredentialState.revoked, CredentialState.notFound]) {
        platform.state = state;
        expect(await providers.appleCredentialUsable('exact-subject'), isFalse);
      }
      platform.fail = true;
      await expectLater(
        providers.appleCredentialUsable('exact-subject'),
        throwsStateError,
      );
      expect(platform.subjects, everyElement('exact-subject'));
    },
  );
}
