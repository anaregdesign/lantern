import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:lantern_client/lantern_client.dart';

import 'native_credential_store.dart';
import 'native_providers.dart';
import 'app_data_session.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';

part 'native_registration_controller.dart';

/// One immutable credential/client generation and its account's durable outbox.
final class NativeAccount {
  NativeAccount(
    this.credential,
    this.client,
    this.session,
    this.partition,
    this.dataSession,
  );
  final String credential;
  final LanternClient client;
  final NativeSession session;
  final String partition;
  final AppDataSession? dataSession;
  final cancellation = LanternCancellationToken();
  Future<void>? _closing;
  Future<void> close() {
    cancellation.cancel();
    return _closing ??= closeAccountData(dataSession, client);
  }

  @override
  String toString() => 'NativeAccount(redacted)';
}

/// App-specific lifecycle: plugins and persistence never enter lantern_client.
/// A transition invalidates old callbacks synchronously; storage/installation
/// is serialized so a late result cannot overwrite or delete a newer account.
final class NativeSessionController extends ChangeNotifier {
  NativeSessionController({
    required this.endpoint,
    required this.providers,
    required this.store,
    LanternClient Function(String? credential)? createClient,
    Future<AppDataSession?> Function(
      LanternClient client,
      NativeSession session,
      bool freshlyVerified,
      AppDataLifetime lifetime,
    )?
    openData,
    AppDataProfile profile = AppDataProfile.durable,
    bool pinnedResponder = false,
    String securityDomain = 'lantern-example',
  }) : _createClient =
           createClient ??
           ((credential) => LanternClient.connect(
             endpoint,
             tokenProvider: () => credential,
             defaultTimeout: const Duration(seconds: 15),
           )),
       _openData =
           openData ??
           ((client, session, fresh, lifetime) {
             final binding = LanternSessionBinding.native(
               session,
               securityDomain: securityDomain,
             );
             return AppDataSessionFactory(
               client: client,
               lifetime: lifetime,
               binding: binding,
               profile: profile,
               legacyPartition: nativeAccountPartition(endpoint, session),
               pinnedResponder:
                   pinnedResponder && session.personalPrefix != null,
               changePrefix: session.personalPrefix ?? '',
             ).open(
               activate: false,
               freshCredentials: fresh
                   ? OfflineVerifiedCredentials(
                       binding: binding,
                       // A newly completed provider login issued this immutable transport's
                       // credential. The previous account was joined before this factory;
                       // data transfer stays disabled until this one event is accepted.
                       install: () {},
                     )
                   : null,
             );
           });
  final Uri endpoint;
  final NativeProviders providers;
  final NativeCredentialStore store;
  final LanternClient Function(String?) _createClient;
  final Future<AppDataSession?> Function(
    LanternClient,
    NativeSession,
    bool,
    AppDataLifetime,
  )
  _openData;
  NativeAccount? account;
  List<NativeRegistrationReview> registrationReviews = const [];
  bool busy = false;
  String message = 'Sign in or explicitly register a Lantern account.';
  int _generation = 0;
  bool _closed = false;
  LanternCancellationToken? _pending;
  Future<void> _serial = Future.value();
  Timer? _expiry;
  Object? _retirementFailure;
  StackTrace? _retirementTrace;
  Future<void>? _closing;
  AppDataLifetime? _dataLifetime;

  Future<void> _closeAccount(NativeAccount? value) async {
    try {
      await value?.close();
    } catch (error, stack) {
      _retirementFailure ??= error;
      _retirementTrace ??= stack;
      rethrow;
    }
  }

  Future<T> _exclusive<T>(Future<T> Function() work) {
    final result = _serial.then((_) {
      if (_retirementFailure != null) {
        Error.throwWithStackTrace(_retirementFailure!, _retirementTrace!);
      }
      return work();
    });
    _serial = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  bool _current(int generation) => !_closed && generation == _generation;
  void _changed() {
    if (!_closed) notifyListeners();
  }

  int _transition() {
    final generation = ++_generation;
    _dataLifetime?.invalidate();
    _pending?.cancel();
    _pending = LanternCancellationToken();
    account?.cancellation.cancel();
    _expiry?.cancel();
    busy = true;
    _changed();
    return generation;
  }

  /// Cancels UI/HTTP ownership even when the platform dialog cannot be dismissed.
  void cancelLogin() {
    final generation = ++_generation;
    _dataLifetime?.invalidate();
    _pending?.cancel();
    final old = account;
    account = null;
    old?.cancellation.cancel();
    _expiry?.cancel();
    if (old != null) {
      unawaited(
        _exclusive(() async {
          await _closeAccount(old);
          if (_current(generation)) await store.clear();
        }).catchError((Object _) {
          // Local access is already closed; never restore it after storage failure.
        }),
      );
    }
    busy = false;
    message = 'Sign-in canceled. A late provider result will be ignored.';
    _changed();
  }

  Future<void> login(
    String issuer,
    String profile, {
    bool lightweight = false,
  }) async {
    final generation = _transition(), cancel = _pending!;
    final bootstrap = _createClient(null);
    try {
      await _exclusive(() => _retire(revoke: true));
      if (!_current(generation)) return;
      final attempt = await bootstrap.beginNativeLogin(
        issuer: issuer,
        clientProfileId: profile,
        options: LanternCallOptions(cancellation: cancel),
      );
      final provider = await _untilCanceled(
        providers.authenticate(attempt.configuration, lightweight: lightweight),
        cancel,
      );
      if (!_current(generation)) return;
      final issued = await bootstrap.completeNativeLogin(
        attempt,
        provider,
        options: LanternCallOptions(cancellation: cancel),
      );
      if (!_current(generation)) return;
      await _inspectAndInstall(
        generation,
        StoredNativeCredential.fromSession(issued.credential, issued.session),
        freshlyVerified: true,
      );
    } on Object {
      if (_current(generation)) {
        message =
            'Sign-in did not complete. Start a new attempt; an earlier exchange may have completed.';
      }
    } finally {
      await bootstrap.close();
      if (_current(generation)) {
        busy = false;
        _changed();
      }
    }
  }

  /// Restores only a Lantern credential; never silently runs a provider login.
  Future<void> restore() async {
    if (account != null || _closed) return;
    final generation = _transition();
    try {
      await _loadRegistrationReviews();
      final saved = await _exclusive(store.read);
      if (!_current(generation)) return;
      if (saved == null) {
        message = 'Sign in or explicitly register a Lantern account.';
        return;
      }
      if (!saved.expiresAt.isAfter(DateTime.now())) {
        await _exclusive(store.clear);
        message = 'Session expired. Sign in again.';
        return;
      }
      await _inspectAndInstall(generation, saved);
    } on Object {
      if (_current(generation)) {
        message = 'Saved session could not be verified. Sign in again.';
      }
    } finally {
      if (_current(generation)) {
        busy = false;
        _changed();
      }
    }
  }

  Future<void> _inspectAndInstall(
    int generation,
    StoredNativeCredential saved, {
    bool freshlyVerified = false,
  }) async {
    final client = _createClient(saved.credential);
    AppDataSession? dataSession;
    var installed = false, cleanupJoined = false;
    Object? startupFailure;
    try {
      final view = await client.getNativeSession(
        options: LanternCallOptions(cancellation: _pending),
      );
      if (!saved.matches(view.session) ||
          !view.session.expiresAt.isAfter(DateTime.now())) {
        throw StateError('Stored session binding changed.');
      }
      if (!_current(generation)) return;
      final partition = nativeAccountPartition(endpoint, view.session);
      await _exclusive(() async {
        if (!_current(generation)) return;
        final old = account;
        account = null;
        _changed();
        await _closeAccount(old);
        if (!_current(generation)) return;
        final lifetime = _dataLifetime = AppDataLifetime(
          current: () => _current(generation),
        );
        // This serialized interval owns startup and its cleanup, even before a
        // session exists. A successor and close must join the entire interval.
        try {
          dataSession = await _openData(
            client,
            view.session,
            freshlyVerified,
            lifetime,
          );
          lifetime.check();
          await store.write(saved);
          if (!lifetime.isCurrent) {
            await store.clear();
            lifetime.check();
          }
          await dataSession?.activate();
          lifetime.check();
          account = NativeAccount(
            saved.credential,
            client,
            view.session,
            partition,
            dataSession,
          );
          installed = true;
          _expiry = Timer(
            view.session.expiresAt.difference(DateTime.now()),
            () => unawaited(signOut(revoke: false)),
          );
          message =
              'Signed in. Session expires at ${view.session.expiresAt.toLocal()}.';
          _changed();
          // Resolve old revocation identities under the same exact account only.
          // A status read can fail; retained records never become a replay queue.
          try {
            final pending = await store.pending(partition),
                keep = <NativeLogoutReview>[];
            for (final review in pending) {
              if (!_current(generation)) {
                keep.add(review);
                continue;
              }
              try {
                if (await client.getNativeLogoutStatus(
                      review,
                      options: LanternCallOptions(
                        cancellation: _pending,
                        timeout: const Duration(seconds: 5),
                      ),
                    ) ==
                    NativeLogoutOutcome.unresolved) {
                  keep.add(review);
                }
              } on Object {
                keep.add(review);
              }
            }
            await store.savePending(partition, keep);
          } on Object {
            /* Retain recovery records when secure storage is unavailable. */
          }
        } catch (error) {
          startupFailure = error;
          if (error is AppDataStartupFailure) {
            _retirementFailure ??= error;
            _retirementTrace ??= error.startupTrace;
          }
          rethrow;
        } finally {
          if (!installed) {
            lifetime.invalidate();
            cleanupJoined = true;
            try {
              await closeAccountData(dataSession, client);
            } catch (error, stack) {
              _retirementFailure ??= error;
              _retirementTrace ??= stack;
              if (startupFailure == null) rethrow;
            }
          }
        }
      });
    } catch (error) {
      startupFailure = error;
      rethrow;
    } finally {
      if (!installed && !cleanupJoined) {
        try {
          await closeAccountData(dataSession, client);
        } catch (error, stack) {
          _retirementFailure ??= error;
          _retirementTrace ??= stack;
          if (startupFailure == null) rethrow;
        }
      }
    }
  }

  /// Rechecks foreground policy/expiry and iOS Apple local credential state.
  /// A plugin state is a local teardown signal, never Server revocation proof.
  Future<void> inspect() async {
    final old = account;
    var generation = _generation;
    if (old == null || busy || _closed) return;
    try {
      if (old.session.principal.issuer == 'https://appleid.apple.com' &&
          !await providers.appleCredentialUsable(
            old.session.principal.subject,
          )) {
        if (_current(generation)) await signOut();
        return;
      }
      final view = await old.client.getNativeSession(
        options: LanternCallOptions(cancellation: old.cancellation),
      );
      if (!_current(generation)) return;
      if (!StoredNativeCredential.fromSession(
        old.credential,
        old.session,
      ).matches(view.session)) {
        throw StateError('Session identity changed.');
      }
      if (view.session.authorityBinding != old.session.authorityBinding) {
        generation = _transition();
        await _inspectAndInstall(
          generation,
          StoredNativeCredential.fromSession(old.credential, view.session),
        );
        if (_current(generation)) {
          busy = false;
          _changed();
        }
      }
    } on Object {
      if (_current(generation)) await signOut(revoke: false);
    }
  }

  Future<void> signOut({bool revoke = true}) async {
    final generation = _transition();
    try {
      await _exclusive(() => _retire(revoke: revoke));
      try {
        await providers.signOutGoogle();
      } on Object {
        /* Local Lantern teardown is independent. */
      }
      if (_current(generation)) {
        message =
            'Signed out locally. Provider consent and account deletion are separate.';
      }
    } on Object {
      if (_current(generation)) {
        message = 'Session stopped. Secure storage could not be updated.';
      }
    } finally {
      if (_current(generation)) {
        busy = false;
        _changed();
      }
    }
  }

  Future<void> _retire({required bool revoke}) async {
    final old = account;
    account = null;
    _changed();
    if (old != null) {
      await _closeAccount(
        old,
      ); // Joins old work, preserving its durable intent.
      if (revoke) {
        final revoker = _createClient(old.credential);
        try {
          final review = await revoker.prepareNativeLogout();
          final pending = await store.pending(old.partition);
          pending.add(review);
          await store.savePending(
            old.partition,
            pending,
          ); // Before possible dispatch.
          final result = await revoker.applyNativeLogout(review);
          if (result.outcome != NativeLogoutOutcome.unresolved) {
            pending.removeLast();
            await store.savePending(old.partition, pending);
          }
        } on Object {
          /* Unknown is retained; never resubmit an uncertain mutation. */
        } finally {
          await revoker.close();
        }
      }
    }
    await store.clear();
  }

  Future<void> close() => _closing ??= _close();

  Future<void> _close() async {
    _closed = true;
    ++_generation;
    _dataLifetime?.invalidate();
    _pending?.cancel();
    _expiry?.cancel();
    await _exclusive(() async {
      final old = account;
      account = null;
      await _closeAccount(old);
    });
  }

  @override
  void dispose() {
    unawaited(close());
    super.dispose();
  }
}

Future<T> _untilCanceled<T>(Future<T> work, LanternCancellationToken cancel) {
  final result = Completer<Never>();
  final remove = cancel.listen((_) {
    result.completeError(StateError('Sign-in canceled.'));
  });
  return Future.any<T>([work, result.future]).whenComplete(remove);
}
