part of 'native_session_controller.dart';

/// Signup shares the account generation/lifecycle owner. The only durable app
/// state is an exact review, keyed by endpoint and verified identity/profile.
/// A retained original is status-only; it never becomes a replay queue.
extension NativeRegistrationActions on NativeSessionController {
  Future<void> _loadRegistrationReviews() => _exclusive(() async {
    registrationReviews = List.unmodifiable(await store.registrations());
    _changed();
  });

  Future<void> register(String issuer, String profile) =>
      _registration(issuer, profile);

  Future<void> recoverRegistration(NativeRegistrationReview original) =>
      _registration(
        original.principal.issuer,
        original.clientProfileId,
        original: original,
      );

  Future<void> _registration(
    String issuer,
    String profile, {
    NativeRegistrationReview? original,
  }) async {
    final generation = _transition(), cancel = _pending!;
    final bootstrap = _createClient(null);
    var retained = original != null;
    try {
      await _exclusive(() => _retire(revoke: true));
      if (!_current(generation)) return;
      final attempt = await bootstrap.beginNativeRegistration(
        issuer: issuer,
        clientProfileId: profile,
        recoveryReview: original,
        options: LanternCallOptions(cancellation: cancel),
      );
      final provider = await _untilCanceled(
        providers.authenticate(attempt.configuration),
        cancel,
      );
      if (!_current(generation)) return;
      final prepared = await bootstrap.prepareNativeRegistration(
        attempt,
        provider,
        options: LanternCallOptions(cancellation: cancel),
      );
      if (!_current(generation)) return;
      if (original != null) {
        final outcome = await bootstrap.getNativeRegistrationStatus(
          prepared,
          options: LanternCallOptions(cancellation: cancel),
        );
        await _finishRegistrationReview(original, outcome);
        if (_current(generation)) message = _registrationMessage(outcome);
        return;
      }
      // Verify identity first; plugin account names never select recovery state.
      var dispatch = false;
      await _exclusive(() async {
        if (!_current(generation)) return;
        final reviews = await store.registrations();
        final partition = nativeRegistrationPartition(prepared.review);
        if (reviews.any((r) => nativeRegistrationPartition(r) == partition)) {
          registrationReviews = List.unmodifiable(reviews);
          message =
              'This account has an unresolved registration. Check its saved original below before registering again.';
          return;
        }
        reviews.add(prepared.review);
        await store.saveRegistrations(
          reviews,
        ); // Must succeed before any Apply.
        registrationReviews = List.unmodifiable(reviews);
        retained = true;
        dispatch = _current(generation);
        // If cancellation happened during persistence, no Apply was dispatched
        // in this process. Remove only this provably unsent review.
        if (!dispatch) {
          reviews.removeLast();
          await store.saveRegistrations(reviews);
          registrationReviews = List.unmodifiable(reviews);
        }
      });
      if (!dispatch) return;
      final result = await bootstrap.applyNativeRegistration(
        prepared,
        options: LanternCallOptions(cancellation: cancel),
      );
      await _finishRegistrationReview(prepared.review, result.outcome);
      if (!_current(generation)) return;
      if (result.sessionCredential case final issued?) {
        await _inspectAndInstall(
          generation,
          StoredNativeCredential.fromSession(issued.credential, issued.session),
        );
      } else {
        message = _registrationMessage(result.outcome);
      }
    } on Object {
      if (_current(generation)) {
        message = retained
            ? 'Registration outcome is not confirmed. Keep the saved original and check its status with the same account; do not register again.'
            : 'Registration did not reach Apply. Check provider sign-in and whether registration is enabled.';
      }
    } finally {
      await bootstrap.close();
      if (_current(generation)) {
        busy = false;
        _changed();
      }
    }
  }

  Future<void> _finishRegistrationReview(
    NativeRegistrationReview original,
    NativeRegistrationOutcome outcome,
  ) async {
    if (outcome == NativeRegistrationOutcome.unresolved) return;
    await _exclusive(() async {
      final originalBytes = original.toBytes();
      final reviews = await store.registrations();
      reviews.removeWhere((r) => listEquals(r.toBytes(), originalBytes));
      await store.saveRegistrations(reviews);
      registrationReviews = List.unmodifiable(reviews);
    });
  }
}

String _registrationMessage(
  NativeRegistrationOutcome outcome,
) => switch (outcome) {
  NativeRegistrationOutcome.applied =>
    'Registration confirmed. Sign in to obtain a new session.',
  NativeRegistrationOutcome.rejected =>
    'The original registration was rejected. A new explicit attempt is now possible.',
  NativeRegistrationOutcome.unresolved =>
    'Original registration is still unresolved. Keep this review and check status again; do not register again.',
};
