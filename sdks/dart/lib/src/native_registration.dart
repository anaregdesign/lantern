part of 'client.dart';

/// Explicit signup and status-only recovery. All calls suppress the old bearer
/// and are attempted once; persistence and provider plugins belong to the app.
extension LanternNativeRegistration on LanternClient {
  /// Begins new registration or recovery of the exact supplied original review.
  Future<NativeRegistrationAttempt> beginNativeRegistration({
    required String issuer,
    required String clientProfileId,
    NativeRegistrationReview? recoveryReview,
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    _validateNativeProfile(issuer, clientProfileId);
    if (recoveryReview != null) {
      _registrationEndpoint(recoveryReview, endpoint);
      if (recoveryReview.principal.issuer != issuer ||
          recoveryReview.clientProfileId != clientProfileId) {
        throw _invalidArgumentException(
          'registration recovery profile changed',
        );
      }
    }
    final raw = $native_client.LanternNativeAuthServiceClient(
      _invoker.transport,
    );
    final result = await _invoker.invokeUnary(
      authenticated: false,
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.beginNativeRegistration(
            $native.BeginNativeRegistrationRequest(
              issuer: issuer,
              clientProfileId: clientProfileId,
              recoveryReview: recoveryReview?._wire,
            ),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    return NativeRegistrationAttempt._(
      _nativeAttempt(result.attempt, endpoint, issuer, clientProfileId),
      recoveryReview,
    );
  }

  /// Verifies provider inputs once. This does not dispatch registration.
  /// Save [NativeRegistrationPrepared.review] before Apply; discard the proof
  /// on app restart. Recovery returns authorization only for the original review.
  Future<NativeRegistrationPrepared> prepareNativeRegistration(
    NativeRegistrationAttempt attempt,
    NativeProviderCredential credential, {
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    final a = attempt._attempt;
    if (a._endpoint != endpoint) {
      throw _invalidArgumentException(
        'native attempt belongs to another endpoint',
      );
    }
    final provider = _nativeProvider(a.configuration, credential);
    final request = $native.PrepareNativeRegistrationRequest(
      attemptId: a._attempt,
      completionSecret: a._secret,
      google: provider.google,
      apple: provider.apple,
      recoveryReview: attempt._recovery?._wire,
    );
    final raw = $native_client.LanternNativeAuthServiceClient(
      _invoker.transport,
    );
    final result = await _invoker.invokeUnary(
      authenticated: false,
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.prepareNativeRegistration(
            request,
            headers: headers..['Lantern-Native-Affinity'] = a.affinity,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    _validateRegistrationReview(result.review);
    final w = result.review;
    if (!_nativeRandom(result.proof) ||
        result.affinity != a.affinity ||
        !result.hasExpiresAt() ||
        _timestampFromProto(result.expiresAt).isAfter(a.expiresAt) ||
        w.clientProfileId != a.configuration.clientProfileId ||
        (a.configuration.kind == NativeProviderKind.google
            ? w.actor.issuer != 'https://accounts.google.com'
            : w.actor.issuer != 'https://appleid.apple.com') ||
        (attempt._recovery != null && w != attempt._recovery._wire)) {
      throw _nativeContractError();
    }
    return NativeRegistrationPrepared._(
      NativeRegistrationReview._(endpoint, w..freeze()),
      result.proof,
      result.affinity,
      _timestampFromProto(result.expiresAt),
      attempt.recoveryOnly,
      a.configuration,
    );
  }

  /// Applies exactly the persisted review once. After any uncertain response,
  /// use status; never prepare another ID or replay merely because it is unknown.
  Future<NativeRegistrationResult> applyNativeRegistration(
    NativeRegistrationPrepared prepared, {
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    _registrationEndpoint(prepared.review, endpoint);
    if (prepared.recoveryOnly) {
      throw _invalidArgumentException('registration recovery is status-only');
    }
    final raw = $native_client.LanternNativeAuthServiceClient(
      _invoker.transport,
    );
    final result = await _invoker.invokeUnary(
      authenticated: false,
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.applyNativeRegistration(
            $native.ApplyNativeRegistrationRequest(
              review: prepared.review._wire,
              proof: prepared._proof,
            ),
            headers: headers..['Lantern-Native-Affinity'] = prepared._affinity,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    final outcome = result.hasCurrentResult()
        ? _registrationOutcome(result.currentResult, prepared.review)
        : NativeRegistrationOutcome.unresolved;
    NativeSessionCredential? issued;
    if (result.credential.isNotEmpty || result.hasSession()) {
      if (outcome != NativeRegistrationOutcome.applied ||
          result.signInRequired) {
        throw _nativeContractError();
      }
      issued = _nativeIssuedSession(
        result.credential,
        result.session,
        endpoint,
        prepared._configuration,
      );
      final s = issued.session, r = prepared.review._wire;
      if (s.principal.issuer != r.actor.issuer ||
          s.principal.subject != r.actor.subject ||
          s.userId != r.userId) {
        throw _nativeContractError();
      }
    }
    if (result.signInRequired !=
        (outcome == NativeRegistrationOutcome.applied && issued == null)) {
      throw _nativeContractError();
    }
    return NativeRegistrationResult._(outcome, issued);
  }

  /// Inspects the original only. It never registers, replays, or issues a session.
  Future<NativeRegistrationOutcome> getNativeRegistrationStatus(
    NativeRegistrationPrepared prepared, {
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    _registrationEndpoint(prepared.review, endpoint);
    final raw = $native_client.LanternNativeAuthServiceClient(
      _invoker.transport,
    );
    final result = await _invoker.invokeUnary(
      authenticated: false,
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.getNativeRegistrationStatus(
            $native.GetNativeRegistrationStatusRequest(
              review: prepared.review._wire,
              proof: prepared._proof,
            ),
            headers: headers..['Lantern-Native-Affinity'] = prepared._affinity,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    return result.hasCurrentResult()
        ? _registrationOutcome(result.currentResult, prepared.review)
        : NativeRegistrationOutcome.unresolved;
  }
}

void _registrationEndpoint(NativeRegistrationReview review, Uri endpoint) {
  if (review.endpoint != endpoint) {
    throw _invalidArgumentException(
      'registration review belongs to another endpoint',
    );
  }
}

void _validateRegistrationReview($native.NativeRegistrationReview r) {
  _validateNativeOperationReview(
    r.profile,
    r.expectedCut,
    r.changeId,
    r.actor,
    r.intentDigest,
    r.writeToBuffer().length,
  );
  if (!_validUserId(r.userId) ||
      !_nativeProfileId(r.clientProfileId) ||
      r.issuerConfigRevision == Int64.ZERO) {
    throw _nativeContractError();
  }
}

NativeRegistrationOutcome _registrationOutcome(
  $security.CurrentSecurityChangeResult result,
  NativeRegistrationReview review,
) {
  final w = review._wire;
  return _nativeOperationOutcome(result, w.profile, w.changeId, w.intentDigest);
}
