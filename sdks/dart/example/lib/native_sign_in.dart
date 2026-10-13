import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import 'native_credential_store.dart';
import 'native_providers.dart';
import 'native_session_controller.dart';

/// Optional native flow in the maintained app. The existing transport demo stays
/// separately selectable; native login never needs a BFF/token endpoint.
final class NativeSignInScreen extends StatefulWidget {
  const NativeSignInScreen({
    super.key,
    required this.endpoint,
    required this.googleProfile,
    required this.appleProfile,
    required this.builder,
  });
  final Uri endpoint;
  final String googleProfile, appleProfile;
  final Widget Function(BuildContext, NativeAccount) builder;
  @override
  State<NativeSignInScreen> createState() => _NativeSignInScreenState();
}

final class _NativeSignInScreenState extends State<NativeSignInScreen> {
  late final NativeSessionController _controller;
  late final AppLifecycleListener _lifecycle;
  @override
  void initState() {
    super.initState();
    _controller = NativeSessionController(
      endpoint: widget.endpoint,
      providers: PlatformNativeProviders.instance,
      store: SecureNativeCredentialStore(widget.endpoint),
    )..addListener(_changed);
    _lifecycle = AppLifecycleListener(
      onResume: () => unawaited(_controller.inspect()),
    );
    unawaited(_controller.restore());
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _lifecycle.dispose();
    _controller.removeListener(_changed);
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final account = _controller.account;
    return Scaffold(
      appBar: AppBar(
        title: const Text('Lantern native sign-in'),
        actions: [
          if (account != null)
            TextButton(
              onPressed: _controller.busy
                  ? null
                  : () => unawaited(_controller.signOut()),
              child: const Text('Sign out'),
            ),
        ],
      ),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(12),
            child: Text(
              _controller.message,
              key: const Key('native-session-state'),
            ),
          ),
          if (_controller.busy) ...[
            const LinearProgressIndicator(),
            TextButton(
              onPressed: _controller.cancelLogin,
              child: const Text('Cancel'),
            ),
          ],
          if (account == null && !_controller.busy) ...[
            if (widget.googleProfile.isNotEmpty) ...[
              FilledButton(
                onPressed: () => unawaited(
                  _controller.login(
                    'https://accounts.google.com',
                    widget.googleProfile,
                  ),
                ),
                child: const Text('Sign in with Google'),
              ),
              TextButton(
                onPressed: () => unawaited(
                  _controller.login(
                    'https://accounts.google.com',
                    widget.googleProfile,
                    lightweight: true,
                  ),
                ),
                child: const Text('Use restored Google sign-in'),
              ),
            ],
            if (widget.appleProfile.isNotEmpty &&
                (Platform.isIOS || Platform.isAndroid))
              FilledButton(
                onPressed: () => unawaited(
                  _controller.login(
                    'https://appleid.apple.com',
                    widget.appleProfile,
                  ),
                ),
                child: const Text('Sign in with Apple'),
              ),
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text(
                'An existing registered Principal is required. Sessions expire after eight hours. Sign-out does not revoke provider consent.',
              ),
            ),
          ],
          if (account != null && !_controller.busy)
            Expanded(
              child: Navigator(
                // Pushed offline screens belong to this account too. Replacing the
                // account drops its entire navigation stack and all granting views.
                key: ValueKey(account),
                onGenerateRoute: (_) => MaterialPageRoute<void>(
                  builder: (context) => widget.builder(context, account),
                ),
              ),
            ),
        ],
      ),
    );
  }
}
