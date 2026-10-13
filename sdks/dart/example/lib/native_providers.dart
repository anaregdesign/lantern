import 'dart:io';

import 'package:google_sign_in/google_sign_in.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:sign_in_with_apple/sign_in_with_apple.dart';

/// The app owns plugins; the pure Dart SDK receives only typed input values.
abstract interface class NativeProviders {
  Future<NativeProviderCredential> authenticate(
    NativePluginConfiguration configuration, {
    bool lightweight = false,
  });
  Future<void> signOutGoogle();
  Future<bool> appleCredentialUsable(String subject);
}

/// One owner per app process. Google initialization is attempted exactly once.
final class PlatformNativeProviders implements NativeProviders {
  PlatformNativeProviders._();
  static final instance = PlatformNativeProviders._();
  Future<void>? _googleInitialization;
  String? _googleBackend;

  @override
  Future<NativeProviderCredential> authenticate(
    NativePluginConfiguration configuration, {
    bool lightweight = false,
  }) async {
    if (!Platform.isAndroid && !Platform.isIOS) {
      throw StateError('Native sign-in requires Android or iOS.');
    }
    if (configuration.kind == NativeProviderKind.google) {
      if (_googleBackend != null && _googleBackend != configuration.clientId) {
        throw StateError(
          'Google client configuration changed; restart the app.',
        );
      }
      _googleBackend = configuration.clientId;
      const clientId = String.fromEnvironment('LANTERN_GOOGLE_IOS_CLIENT_ID');
      _googleInitialization ??= GoogleSignIn.instance.initialize(
        clientId: Platform.isIOS && clientId.isNotEmpty ? clientId : null,
        serverClientId: configuration.clientId,
        nonce: null,
      );
      await _googleInitialization;
      final account = lightweight
          ? await GoogleSignIn.instance.attemptLightweightAuthentication()
          : await GoogleSignIn.instance.authenticate();
      final token = account?.authentication.idToken;
      if (token == null || token.isEmpty) {
        throw StateError('No Google ID token; choose interactive sign-in.');
      }
      return GoogleNativeCredential(idToken: token);
    }
    final web = configuration.kind == NativeProviderKind.appleAndroidWeb;
    if (web != Platform.isAndroid || lightweight) {
      throw StateError('Apple profile does not match this platform.');
    }
    final value = await SignInWithApple.getAppleIDCredential(
      scopes: const [],
      nonce: configuration.nonce,
      state: configuration.state,
      webAuthenticationOptions: web
          ? WebAuthenticationOptions(
              clientId: configuration.clientId,
              redirectUri: configuration.redirectUri!,
            )
          : null,
    );
    if (value.identityToken == null || value.state != configuration.state) {
      throw StateError(
        'Apple sign-in returned incomplete transaction context.',
      );
    }
    return AppleNativeCredential(
      identityToken: value.identityToken!,
      authorizationCode: value.authorizationCode,
      state: value.state!,
    );
  }

  @override
  Future<void> signOutGoogle() async {
    if (_googleInitialization != null) {
      await _googleInitialization;
      await GoogleSignIn.instance.signOut();
    }
  }

  @override
  Future<bool> appleCredentialUsable(String subject) async =>
      !Platform.isIOS ||
      await SignInWithApple.getCredentialState(subject) ==
          CredentialState.authorized;
}
