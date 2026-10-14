part of 'client.dart';

/// Current-profile inspection for cache and foreground subscription ownership.
extension LanternCurrentAuthority on LanternClient {
  /// Reads a complete profile/cut/credential binding with one authenticated
  /// attempt. This is a cache partition, never authority for another call.
  /// Cancel old work and discard granting views when it changes, preserving
  /// possibly dispatched mutation identities for status-only recovery.
  /// Legacy scalar versions, missing fields and unknown profiles are refused.
  Future<String> getCurrentAuthorityBinding({
    LanternCallOptions? options,
  }) async {
    final response = await _getCurrentPrincipal(options);
    return _currentAuthorityBinding(response.version);
  }

  /// Reads the immutable Server-installed user ID without an extra Role.
  /// Returns null for an authenticated UID-less or machine Principal. Signed-out
  /// calls throw their authentication error; no cached ID or empty string is used.
  Future<String?> getCurrentUserId({LanternCallOptions? options}) async {
    final response = await _getCurrentPrincipal(options);
    _currentAuthorityBinding(response.version);
    return response.hasUserId() ? response.userId : null;
  }

  Future<$security.GetCurrentPrincipalResponse> _getCurrentPrincipal(
    LanternCallOptions? options,
  ) async {
    _ensureOpen();
    final raw = $security_client.LanternSecurityServiceClient(
      _invoker.transport,
    );
    final response = await _invoker.invokeUnary(
      options: _freezeCallOptions(options),
      call:
          ({
            required headers,
            required signal,
            required onHeader,
            required onTrailer,
          }) => raw.getCurrentPrincipal(
            $security.GetCurrentPrincipalRequest(),
            headers: headers,
            signal: signal,
            onHeader: onHeader,
            onTrailer: onTrailer,
          ),
    );
    if (response.hasUserId() &&
        (!_validUserId(response.userId) ||
            response.identity.kind !=
                $security.SecurityPrincipalKind.SECURITY_PRINCIPAL_KIND_OIDC)) {
      throw _userIdContractError();
    }
    return response;
  }
}

bool _validUserId(String id) =>
    RegExp(r'^[0-9a-f]{32}$').hasMatch(id) && id != '0' * 32;

LanternFailedPreconditionException _userIdContractError() =>
    LanternFailedPreconditionException._(
      _ErrorData(
        transportCode: connect.Code.failedPrecondition.value,
        transportCodeName: connect.Code.failedPrecondition.name,
        message: 'invalid current user ID',
        headers: const {},
        trailers: const {},
        metadata: const {},
      ),
    );

String _currentAuthorityBinding($security.SecurityVersion version) {
  Never invalid() => throw LanternFailedPreconditionException._(
    _ErrorData(
      transportCode: connect.Code.failedPrecondition.value,
      transportCodeName: connect.Code.failedPrecondition.name,
      message: 'invalid current authority contract',
      headers: const {},
      trailers: const {},
      metadata: const {},
    ),
  );
  String bytes(List<int> value, int size, {bool zero = false}) {
    if (value.length != size || !zero && !value.any((byte) => byte != 0)) {
      invalid();
    }
    return value.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();
  }

  final p = version.currentProfile, c = version.currentCut;
  if (version.revision != Int64.ZERO ||
      version.digest.isNotEmpty ||
      version.generation.isNotEmpty ||
      p.version != 2 ||
      c.version != 1 ||
      c.sequence == Int64.ZERO) {
    invalid();
  }
  final domain = bytes(p.domain, 32),
      cohort = bytes(p.cohort, 32),
      generation = bytes(p.generation, 16);
  if (bytes(c.domain, 32) != domain ||
      bytes(c.cohort, 32) != cohort ||
      bytes(c.generation, 16) != generation) {
    invalid();
  }
  final profile = [
    'current-v2',
    domain,
    cohort,
    generation,
    bytes(p.protocol, 32),
    bytes(p.timeProfile, 32),
    bytes(p.membership, 32),
    bytes(p.configuration, 32),
  ].join(':');
  final cut = [
    'cut-v1',
    domain,
    cohort,
    generation,
    _uint64FromFixnum(c.sequence).toString(),
    bytes(c.previous, 32, zero: true),
    bytes(c.projection, 32),
    bytes(c.frontier, 32),
    bytes(c.fences, 32),
    bytes(c.policy, 32),
  ].join(':');
  return '$profile/$cut/${bytes(version.admissionBinding, 32)}';
}
