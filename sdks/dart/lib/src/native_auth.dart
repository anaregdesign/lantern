part of 'client.dart';

/// Pure-Dart native authentication. Plugins, secure storage and account lifecycle
/// remain application-owned. Every call is attempted once without automatic retry.
extension LanternNativeAuth on LanternClient {
  /// Begins a bounded native transaction. Suppresses the old account's bearer.
  Future<NativeLoginAttempt> beginNativeLogin({
    required String issuer,
    required String clientProfileId,
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    if (!{
          'https://accounts.google.com',
          'https://appleid.apple.com',
        }.contains(issuer) ||
        !_nativeProfileId(clientProfileId)) {
      throw _invalidArgumentException('invalid native issuer/profile');
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
          }) => raw.beginNativeLogin(
            $native.BeginNativeLoginRequest(
              issuer: issuer,
              clientProfileId: clientProfileId,
            ),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    final c = result.configuration;
    final kind = switch (c.kind) {
      $security.NativeClientKind.NATIVE_CLIENT_KIND_GOOGLE_ID_TOKEN =>
        NativeProviderKind.google,
      $security.NativeClientKind.NATIVE_CLIENT_KIND_APPLE_NATIVE =>
        NativeProviderKind.appleNative,
      $security.NativeClientKind.NATIVE_CLIENT_KIND_APPLE_ANDROID_WEB =>
        NativeProviderKind.appleAndroidWeb,
      _ => throw _nativeContractError(),
    };
    if (!_nativeRandom(result.attemptId) ||
        !_nativeRandom(result.completionSecret) ||
        !result.hasExpiresAt() ||
        result.affinity.isEmpty ||
        result.affinity.length > 57 ||
        c.clientProfileId != clientProfileId ||
        c.clientId.isEmpty ||
        c.clientId.length > 512 ||
        c.serviceOrigin != endpoint.origin ||
        (kind == NativeProviderKind.google) !=
            (issuer == 'https://accounts.google.com')) {
      throw _nativeContractError();
    }
    if (kind == NativeProviderKind.google
        ? c.nonce.isNotEmpty || c.state.isNotEmpty || c.redirectUri.isNotEmpty
        : !_nativeRandom(c.nonce) ||
              !c.state.startsWith('${result.affinity}.') ||
              c.state.length > 101) {
      throw _nativeContractError();
    }
    if (kind == NativeProviderKind.appleAndroidWeb
        ? c.redirectUri !=
              '${endpoint.origin}/auth/native/apple/callback/$clientProfileId'
        : c.redirectUri.isNotEmpty) {
      throw _nativeContractError();
    }
    return NativeLoginAttempt._(
      endpoint,
      result.attemptId,
      result.completionSecret,
      _timestampFromProto(result.expiresAt),
      result.affinity,
      NativePluginConfiguration._(
        kind,
        c.clientProfileId,
        c.clientId,
        Uri.parse(c.serviceOrigin),
        c.redirectUri.isEmpty ? null : Uri.parse(c.redirectUri),
        c.nonce.isEmpty ? null : c.nonce,
        c.state.isEmpty ? null : c.state,
      ),
    );
  }

  /// Exchanges plugin inputs once. A timeout may leave an undisclosed session
  /// until fixed expiry; do not retry the consumed transaction or Apple code.
  Future<NativeSessionCredential> completeNativeLogin(
    NativeLoginAttempt attempt,
    NativeProviderCredential credential, {
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    if (attempt._endpoint != endpoint) {
      throw _invalidArgumentException(
        'native attempt belongs to another endpoint',
      );
    }
    final request = $native.CompleteNativeLoginRequest(
      attemptId: attempt._attempt,
      completionSecret: attempt._secret,
    );
    switch (credential) {
      case GoogleNativeCredential():
        if (attempt.configuration.kind != NativeProviderKind.google ||
            !_nativeToken(credential._idToken)) {
          throw _invalidArgumentException('invalid Google native input');
        }
        request.google = $native.GoogleNativeCredential(
          idToken: credential._idToken,
        );
      case AppleNativeCredential():
        if (attempt.configuration.kind == NativeProviderKind.google ||
            !_nativeToken(credential._identityToken) ||
            credential._code.isEmpty ||
            credential._code.length > 4096 ||
            credential._state != attempt.configuration.state) {
          throw _invalidArgumentException('invalid Apple native input');
        }
        request.apple = $native.AppleNativeCredential(
          identityToken: credential._identityToken,
          authorizationCode: credential._code,
          state: credential._state,
        );
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
          }) => raw.completeNativeLogin(
            request,
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    if (!result.credential.startsWith('lnu1_') ||
        !_nativeRandom(result.credential.substring(5))) {
      throw _nativeContractError();
    }
    final session = _nativeSession(result.session, endpoint);
    if (session.clientProfileId != attempt.configuration.clientProfileId ||
        (attempt.configuration.kind == NativeProviderKind.google
            ? session.principal.issuer != 'https://accounts.google.com'
            : session.principal.issuer != 'https://appleid.apple.com')) {
      throw _nativeContractError();
    }
    return NativeSessionCredential._(result.credential, session);
  }

  /// Inspects the native session using the existing TokenProvider.
  Future<NativeSessionView> getNativeSession({
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    final raw = $native_client.LanternNativeAuthServiceClient(
      _invoker.transport,
    );
    final result = await _invoker.invokeUnary(
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.getNativeSession(
            $native.GetNativeSessionRequest(),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    return NativeSessionView._(
      _nativeSession(result.session, endpoint),
      List.unmodifiable(result.roles.map((r) => NativeRole._(r.id, r.name))),
    );
  }

  /// Prepares the current session's revocation identity; retain before Apply.
  Future<NativeLogoutReview> prepareNativeLogout({
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    final raw = $native_client.LanternNativeAuthServiceClient(
      _invoker.transport,
    );
    final result = await _invoker.invokeUnary(
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.prepareNativeLogout(
            $native.PrepareNativeLogoutRequest(),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    _validateNativeReview(result.review);
    return NativeLogoutReview._(endpoint, result.review..freeze());
  }

  /// Dispatches the exact prepared review once. After uncertainty use
  /// [getNativeLogoutStatus], potentially under a later session of this Principal.
  Future<NativeLogoutResult> applyNativeLogout(
    NativeLogoutReview review, {
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    _nativeReviewEndpoint(review, endpoint);
    final raw = $native_client.LanternNativeAuthServiceClient(
      _invoker.transport,
    );
    final result = await _invoker.invokeUnary(
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.applyNativeLogout(
            $native.ApplyNativeLogoutRequest(review: review._wire),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    if (!result.clearLocalSession) throw _nativeContractError();
    return NativeLogoutResult._(
      true,
      result.hasCurrentResult()
          ? _nativeLogoutOutcome(result.currentResult, review)
          : NativeLogoutOutcome.unresolved,
    );
  }

  /// Reads the exact original logout outcome; never resubmits the mutation.
  Future<NativeLogoutOutcome> getNativeLogoutStatus(
    NativeLogoutReview review, {
    LanternCallOptions? options,
  }) async {
    _nativeEndpoint(this);
    _nativeReviewEndpoint(review, endpoint);
    final raw = $security_client.LanternSecurityServiceClient(
      _invoker.transport,
    );
    final w = review._wire;
    final result = await _invoker.invokeUnary(
      redactResponse: true,
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.getSecurityChangeStatus(
            $security.GetSecurityChangeStatusRequest(
              currentProfile: w.profile,
              currentChangeId: w.changeId,
              currentIntentDigest: w.intentDigest,
            ),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    return result.hasCurrentResult()
        ? _nativeLogoutOutcome(result.currentResult, review)
        : NativeLogoutOutcome.unresolved;
  }
}

void _nativeEndpoint(LanternClient client) {
  client._ensureOpen();
  if (client.endpoint.scheme != 'https') {
    throw _invalidArgumentException('native authentication requires HTTPS');
  }
}

bool _nativeProfileId(String value) =>
    RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$').hasMatch(value);
bool _nativeToken(String value) =>
    value.isNotEmpty && utf8.encode(value).length <= 16 << 10;
bool _nativeRandom(String value) {
  if (!RegExp(r'^[A-Za-z0-9_-]{43}$').hasMatch(value)) return false;
  try {
    return base64UrlEncode(
          base64Url.decode(base64Url.normalize(value)),
        ).replaceAll('=', '') ==
        value;
  } on Object {
    return false;
  }
}

LanternException _nativeContractError() => LanternFailedPreconditionException._(
  _ErrorData(
    transportCode: connect.Code.failedPrecondition.value,
    transportCodeName: connect.Code.failedPrecondition.name,
    message: 'invalid native authentication contract',
    headers: const {},
    trailers: const {},
    metadata: const {},
  ),
);
NativeSession _nativeSession($native.NativeSession s, Uri endpoint) {
  if (s.credentialProfile != 'native_end_user' ||
      !_nativeProfileId(s.clientProfileId) ||
      s.serviceOrigin != endpoint.origin ||
      !s.hasCreatedAt() ||
      !s.hasExpiresAt() ||
      s.identity.kind !=
          $security.SecurityPrincipalKind.SECURITY_PRINCIPAL_KIND_OIDC ||
      !{
        'https://accounts.google.com',
        'https://appleid.apple.com',
      }.contains(s.identity.issuer) ||
      s.identity.subject.isEmpty ||
      s.identity.machineName.isNotEmpty) {
    throw _nativeContractError();
  }
  final created = _timestampFromProto(s.createdAt),
      expires = _timestampFromProto(s.expiresAt);
  if (!expires.isAfter(created) ||
      expires.difference(created) > const Duration(hours: 8)) {
    throw _nativeContractError();
  }
  return NativeSession._(
    NativePrincipal._(s.identity.issuer, s.identity.subject),
    s.clientProfileId,
    Uri.parse(s.serviceOrigin),
    created,
    expires,
    s.hasAuthTime() ? _timestampFromProto(s.authTime) : null,
    _currentAuthorityBinding(s.version),
  );
}

void _nativeReviewEndpoint(NativeLogoutReview review, Uri endpoint) {
  if (review._endpoint != endpoint) {
    throw _invalidArgumentException(
      'native logout review belongs to another endpoint',
    );
  }
}

void _validateNativeReview($security.CurrentSessionRevocationReview r) {
  bool bytes(List<int> b, int n) => b.length == n && b.any((v) => v != 0);
  bool equal(List<int> a, List<int> b) => base64Encode(a) == base64Encode(b);
  final p = r.profile, c = r.expectedCut, id = r.changeId;
  if (r.writeToBuffer().length > 16 << 10 ||
      p.version != 2 ||
      c.version != 1 ||
      c.sequence == Int64.ZERO ||
      id.version != 1 ||
      id.namespace == Int64.ZERO ||
      !bytes(id.nonce, 16) ||
      !bytes(r.intentDigest, 32) ||
      r.sessionLineage == Int64.ZERO ||
      !RegExp(r'^[0-9a-f]{64}$').hasMatch(r.sessionDigest) ||
      r.actor.kind !=
          $security.SecurityPrincipalKind.SECURITY_PRINCIPAL_KIND_OIDC ||
      r.actor.subject.isEmpty ||
      !{
        'https://accounts.google.com',
        'https://appleid.apple.com',
      }.contains(r.actor.issuer)) {
    throw _nativeContractError();
  }
  for (final b in [
    p.domain,
    p.cohort,
    p.protocol,
    p.timeProfile,
    p.membership,
    p.configuration,
    c.domain,
    c.cohort,
    c.projection,
    c.frontier,
    c.fences,
    c.policy,
    id.domain,
    id.cohort,
  ]) {
    if (!bytes(b, 32)) throw _nativeContractError();
  }
  if (!bytes(p.generation, 16) ||
      !equal(c.generation, p.generation) ||
      !equal(p.domain, c.domain) ||
      !equal(p.cohort, c.cohort) ||
      !equal(p.domain, id.domain) ||
      !equal(p.cohort, id.cohort) ||
      c.previous.length != 32) {
    throw _nativeContractError();
  }
}

NativeLogoutOutcome _nativeLogoutOutcome(
  $security.CurrentSecurityChangeResult result,
  NativeLogoutReview review,
) {
  final w = review._wire;
  if (result.profile != w.profile ||
      result.changeId != w.changeId ||
      base64Encode(result.intentDigest) != base64Encode(w.intentDigest)) {
    throw _nativeContractError();
  }
  if (!result.hasOriginal()) return NativeLogoutOutcome.unresolved;
  final original = result.original;
  if (original.changeId != w.changeId ||
      base64Encode(original.intentDigest) != base64Encode(w.intentDigest)) {
    throw _nativeContractError();
  }
  return switch (original.disposition) {
    $security.CurrentSecurityDisposition.CURRENT_SECURITY_DISPOSITION_APPLIED =>
      NativeLogoutOutcome.applied,
    $security
        .CurrentSecurityDisposition
        .CURRENT_SECURITY_DISPOSITION_REJECTED_CAS ||
    $security
        .CurrentSecurityDisposition
        .CURRENT_SECURITY_DISPOSITION_REJECTED_ADMIN ||
    $security
        .CurrentSecurityDisposition
        .CURRENT_SECURITY_DISPOSITION_REJECTED_AUTHORITY ||
    $security
        .CurrentSecurityDisposition
        .CURRENT_SECURITY_DISPOSITION_REJECTED_PURPOSE ||
    $security
        .CurrentSecurityDisposition
        .CURRENT_SECURITY_DISPOSITION_REJECTED_CAPACITY ||
    $security
        .CurrentSecurityDisposition
        .CURRENT_SECURITY_DISPOSITION_REJECTED_INVARIANT =>
      NativeLogoutOutcome.rejected,
    _ => throw _nativeContractError(),
  };
}
