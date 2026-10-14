// This is a generated file - do not edit.
//
// Generated from graph/v1/native_auth.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:fixnum/fixnum.dart' as $fixnum;
import 'package:protobuf/protobuf.dart' as $pb;

import '../../google/protobuf/timestamp.pb.dart' as $0;
import 'security.pb.dart' as $1;

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

class BeginNativeLoginRequest extends $pb.GeneratedMessage {
  factory BeginNativeLoginRequest({
    $core.String? issuer,
    $core.String? clientProfileId,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    if (clientProfileId != null) result.clientProfileId = clientProfileId;
    return result;
  }

  BeginNativeLoginRequest._();

  factory BeginNativeLoginRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BeginNativeLoginRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BeginNativeLoginRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'issuer')
    ..aOS(2, _omitFieldNames ? '' : 'clientProfileId')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeLoginRequest clone() =>
      BeginNativeLoginRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeLoginRequest copyWith(
          void Function(BeginNativeLoginRequest) updates) =>
      super.copyWith((message) => updates(message as BeginNativeLoginRequest))
          as BeginNativeLoginRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BeginNativeLoginRequest create() => BeginNativeLoginRequest._();
  @$core.override
  BeginNativeLoginRequest createEmptyInstance() => create();
  static $pb.PbList<BeginNativeLoginRequest> createRepeated() =>
      $pb.PbList<BeginNativeLoginRequest>();
  @$core.pragma('dart2js:noInline')
  static BeginNativeLoginRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BeginNativeLoginRequest>(create);
  static BeginNativeLoginRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get issuer => $_getSZ(0);
  @$pb.TagNumber(1)
  set issuer($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearIssuer() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get clientProfileId => $_getSZ(1);
  @$pb.TagNumber(2)
  set clientProfileId($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasClientProfileId() => $_has(1);
  @$pb.TagNumber(2)
  void clearClientProfileId() => $_clearField(2);
}

class NativePluginConfiguration extends $pb.GeneratedMessage {
  factory NativePluginConfiguration({
    $1.NativeClientKind? kind,
    $core.String? clientProfileId,
    $core.String? clientId,
    $core.String? serviceOrigin,
    $core.String? redirectUri,
    $core.String? nonce,
    $core.String? state,
  }) {
    final result = create();
    if (kind != null) result.kind = kind;
    if (clientProfileId != null) result.clientProfileId = clientProfileId;
    if (clientId != null) result.clientId = clientId;
    if (serviceOrigin != null) result.serviceOrigin = serviceOrigin;
    if (redirectUri != null) result.redirectUri = redirectUri;
    if (nonce != null) result.nonce = nonce;
    if (state != null) result.state = state;
    return result;
  }

  NativePluginConfiguration._();

  factory NativePluginConfiguration.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory NativePluginConfiguration.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'NativePluginConfiguration',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..e<$1.NativeClientKind>(
        1, _omitFieldNames ? '' : 'kind', $pb.PbFieldType.OE,
        defaultOrMaker: $1.NativeClientKind.NATIVE_CLIENT_KIND_UNSPECIFIED,
        valueOf: $1.NativeClientKind.valueOf,
        enumValues: $1.NativeClientKind.values)
    ..aOS(2, _omitFieldNames ? '' : 'clientProfileId')
    ..aOS(3, _omitFieldNames ? '' : 'clientId')
    ..aOS(4, _omitFieldNames ? '' : 'serviceOrigin')
    ..aOS(5, _omitFieldNames ? '' : 'redirectUri')
    ..aOS(6, _omitFieldNames ? '' : 'nonce')
    ..aOS(7, _omitFieldNames ? '' : 'state')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  NativePluginConfiguration clone() =>
      NativePluginConfiguration()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  NativePluginConfiguration copyWith(
          void Function(NativePluginConfiguration) updates) =>
      super.copyWith((message) => updates(message as NativePluginConfiguration))
          as NativePluginConfiguration;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static NativePluginConfiguration create() => NativePluginConfiguration._();
  @$core.override
  NativePluginConfiguration createEmptyInstance() => create();
  static $pb.PbList<NativePluginConfiguration> createRepeated() =>
      $pb.PbList<NativePluginConfiguration>();
  @$core.pragma('dart2js:noInline')
  static NativePluginConfiguration getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<NativePluginConfiguration>(create);
  static NativePluginConfiguration? _defaultInstance;

  @$pb.TagNumber(1)
  $1.NativeClientKind get kind => $_getN(0);
  @$pb.TagNumber(1)
  set kind($1.NativeClientKind value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasKind() => $_has(0);
  @$pb.TagNumber(1)
  void clearKind() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get clientProfileId => $_getSZ(1);
  @$pb.TagNumber(2)
  set clientProfileId($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasClientProfileId() => $_has(1);
  @$pb.TagNumber(2)
  void clearClientProfileId() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get clientId => $_getSZ(2);
  @$pb.TagNumber(3)
  set clientId($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasClientId() => $_has(2);
  @$pb.TagNumber(3)
  void clearClientId() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get serviceOrigin => $_getSZ(3);
  @$pb.TagNumber(4)
  set serviceOrigin($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasServiceOrigin() => $_has(3);
  @$pb.TagNumber(4)
  void clearServiceOrigin() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get redirectUri => $_getSZ(4);
  @$pb.TagNumber(5)
  set redirectUri($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasRedirectUri() => $_has(4);
  @$pb.TagNumber(5)
  void clearRedirectUri() => $_clearField(5);

  /// Apple forwards the raw nonce unchanged. Google initialize uses no nonce.
  @$pb.TagNumber(6)
  $core.String get nonce => $_getSZ(5);
  @$pb.TagNumber(6)
  set nonce($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasNonce() => $_has(5);
  @$pb.TagNumber(6)
  void clearNonce() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get state => $_getSZ(6);
  @$pb.TagNumber(7)
  set state($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasState() => $_has(6);
  @$pb.TagNumber(7)
  void clearState() => $_clearField(7);
}

class BeginNativeLoginResponse extends $pb.GeneratedMessage {
  factory BeginNativeLoginResponse({
    $core.String? attemptId,
    $core.String? completionSecret,
    $0.Timestamp? expiresAt,
    $core.String? affinity,
    NativePluginConfiguration? configuration,
  }) {
    final result = create();
    if (attemptId != null) result.attemptId = attemptId;
    if (completionSecret != null) result.completionSecret = completionSecret;
    if (expiresAt != null) result.expiresAt = expiresAt;
    if (affinity != null) result.affinity = affinity;
    if (configuration != null) result.configuration = configuration;
    return result;
  }

  BeginNativeLoginResponse._();

  factory BeginNativeLoginResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BeginNativeLoginResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BeginNativeLoginResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'attemptId')
    ..aOS(2, _omitFieldNames ? '' : 'completionSecret')
    ..aOM<$0.Timestamp>(3, _omitFieldNames ? '' : 'expiresAt',
        subBuilder: $0.Timestamp.create)
    ..aOS(4, _omitFieldNames ? '' : 'affinity')
    ..aOM<NativePluginConfiguration>(5, _omitFieldNames ? '' : 'configuration',
        subBuilder: NativePluginConfiguration.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeLoginResponse clone() =>
      BeginNativeLoginResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeLoginResponse copyWith(
          void Function(BeginNativeLoginResponse) updates) =>
      super.copyWith((message) => updates(message as BeginNativeLoginResponse))
          as BeginNativeLoginResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BeginNativeLoginResponse create() => BeginNativeLoginResponse._();
  @$core.override
  BeginNativeLoginResponse createEmptyInstance() => create();
  static $pb.PbList<BeginNativeLoginResponse> createRepeated() =>
      $pb.PbList<BeginNativeLoginResponse>();
  @$core.pragma('dart2js:noInline')
  static BeginNativeLoginResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BeginNativeLoginResponse>(create);
  static BeginNativeLoginResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get attemptId => $_getSZ(0);
  @$pb.TagNumber(1)
  set attemptId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAttemptId() => $_has(0);
  @$pb.TagNumber(1)
  void clearAttemptId() => $_clearField(1);

  /// Ephemeral app proof. Never send to a provider or persist in app storage.
  @$pb.TagNumber(2)
  $core.String get completionSecret => $_getSZ(1);
  @$pb.TagNumber(2)
  set completionSecret($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCompletionSecret() => $_has(1);
  @$pb.TagNumber(2)
  void clearCompletionSecret() => $_clearField(2);

  @$pb.TagNumber(3)
  $0.Timestamp get expiresAt => $_getN(2);
  @$pb.TagNumber(3)
  set expiresAt($0.Timestamp value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasExpiresAt() => $_has(2);
  @$pb.TagNumber(3)
  void clearExpiresAt() => $_clearField(3);
  @$pb.TagNumber(3)
  $0.Timestamp ensureExpiresAt() => $_ensure(2);

  @$pb.TagNumber(4)
  $core.String get affinity => $_getSZ(3);
  @$pb.TagNumber(4)
  set affinity($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasAffinity() => $_has(3);
  @$pb.TagNumber(4)
  void clearAffinity() => $_clearField(4);

  @$pb.TagNumber(5)
  NativePluginConfiguration get configuration => $_getN(4);
  @$pb.TagNumber(5)
  set configuration(NativePluginConfiguration value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasConfiguration() => $_has(4);
  @$pb.TagNumber(5)
  void clearConfiguration() => $_clearField(5);
  @$pb.TagNumber(5)
  NativePluginConfiguration ensureConfiguration() => $_ensure(4);
}

class GoogleNativeCredential extends $pb.GeneratedMessage {
  factory GoogleNativeCredential({
    $core.String? idToken,
  }) {
    final result = create();
    if (idToken != null) result.idToken = idToken;
    return result;
  }

  GoogleNativeCredential._();

  factory GoogleNativeCredential.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GoogleNativeCredential.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GoogleNativeCredential',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'idToken')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GoogleNativeCredential clone() =>
      GoogleNativeCredential()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GoogleNativeCredential copyWith(
          void Function(GoogleNativeCredential) updates) =>
      super.copyWith((message) => updates(message as GoogleNativeCredential))
          as GoogleNativeCredential;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GoogleNativeCredential create() => GoogleNativeCredential._();
  @$core.override
  GoogleNativeCredential createEmptyInstance() => create();
  static $pb.PbList<GoogleNativeCredential> createRepeated() =>
      $pb.PbList<GoogleNativeCredential>();
  @$core.pragma('dart2js:noInline')
  static GoogleNativeCredential getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GoogleNativeCredential>(create);
  static GoogleNativeCredential? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get idToken => $_getSZ(0);
  @$pb.TagNumber(1)
  set idToken($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIdToken() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdToken() => $_clearField(1);
}

class AppleNativeCredential extends $pb.GeneratedMessage {
  factory AppleNativeCredential({
    $core.String? identityToken,
    $core.String? authorizationCode,
    $core.String? state,
  }) {
    final result = create();
    if (identityToken != null) result.identityToken = identityToken;
    if (authorizationCode != null) result.authorizationCode = authorizationCode;
    if (state != null) result.state = state;
    return result;
  }

  AppleNativeCredential._();

  factory AppleNativeCredential.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AppleNativeCredential.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AppleNativeCredential',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'identityToken')
    ..aOS(2, _omitFieldNames ? '' : 'authorizationCode')
    ..aOS(3, _omitFieldNames ? '' : 'state')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AppleNativeCredential clone() =>
      AppleNativeCredential()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AppleNativeCredential copyWith(
          void Function(AppleNativeCredential) updates) =>
      super.copyWith((message) => updates(message as AppleNativeCredential))
          as AppleNativeCredential;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AppleNativeCredential create() => AppleNativeCredential._();
  @$core.override
  AppleNativeCredential createEmptyInstance() => create();
  static $pb.PbList<AppleNativeCredential> createRepeated() =>
      $pb.PbList<AppleNativeCredential>();
  @$core.pragma('dart2js:noInline')
  static AppleNativeCredential getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AppleNativeCredential>(create);
  static AppleNativeCredential? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get identityToken => $_getSZ(0);
  @$pb.TagNumber(1)
  set identityToken($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentityToken() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentityToken() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get authorizationCode => $_getSZ(1);
  @$pb.TagNumber(2)
  set authorizationCode($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasAuthorizationCode() => $_has(1);
  @$pb.TagNumber(2)
  void clearAuthorizationCode() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get state => $_getSZ(2);
  @$pb.TagNumber(3)
  set state($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasState() => $_has(2);
  @$pb.TagNumber(3)
  void clearState() => $_clearField(3);
}

enum CompleteNativeLoginRequest_Provider { google, apple, notSet }

class CompleteNativeLoginRequest extends $pb.GeneratedMessage {
  factory CompleteNativeLoginRequest({
    $core.String? attemptId,
    $core.String? completionSecret,
    GoogleNativeCredential? google,
    AppleNativeCredential? apple,
  }) {
    final result = create();
    if (attemptId != null) result.attemptId = attemptId;
    if (completionSecret != null) result.completionSecret = completionSecret;
    if (google != null) result.google = google;
    if (apple != null) result.apple = apple;
    return result;
  }

  CompleteNativeLoginRequest._();

  factory CompleteNativeLoginRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CompleteNativeLoginRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, CompleteNativeLoginRequest_Provider>
      _CompleteNativeLoginRequest_ProviderByTag = {
    3: CompleteNativeLoginRequest_Provider.google,
    4: CompleteNativeLoginRequest_Provider.apple,
    0: CompleteNativeLoginRequest_Provider.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CompleteNativeLoginRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [3, 4])
    ..aOS(1, _omitFieldNames ? '' : 'attemptId')
    ..aOS(2, _omitFieldNames ? '' : 'completionSecret')
    ..aOM<GoogleNativeCredential>(3, _omitFieldNames ? '' : 'google',
        subBuilder: GoogleNativeCredential.create)
    ..aOM<AppleNativeCredential>(4, _omitFieldNames ? '' : 'apple',
        subBuilder: AppleNativeCredential.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CompleteNativeLoginRequest clone() =>
      CompleteNativeLoginRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CompleteNativeLoginRequest copyWith(
          void Function(CompleteNativeLoginRequest) updates) =>
      super.copyWith(
              (message) => updates(message as CompleteNativeLoginRequest))
          as CompleteNativeLoginRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CompleteNativeLoginRequest create() => CompleteNativeLoginRequest._();
  @$core.override
  CompleteNativeLoginRequest createEmptyInstance() => create();
  static $pb.PbList<CompleteNativeLoginRequest> createRepeated() =>
      $pb.PbList<CompleteNativeLoginRequest>();
  @$core.pragma('dart2js:noInline')
  static CompleteNativeLoginRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CompleteNativeLoginRequest>(create);
  static CompleteNativeLoginRequest? _defaultInstance;

  CompleteNativeLoginRequest_Provider whichProvider() =>
      _CompleteNativeLoginRequest_ProviderByTag[$_whichOneof(0)]!;
  void clearProvider() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $core.String get attemptId => $_getSZ(0);
  @$pb.TagNumber(1)
  set attemptId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAttemptId() => $_has(0);
  @$pb.TagNumber(1)
  void clearAttemptId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get completionSecret => $_getSZ(1);
  @$pb.TagNumber(2)
  set completionSecret($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCompletionSecret() => $_has(1);
  @$pb.TagNumber(2)
  void clearCompletionSecret() => $_clearField(2);

  @$pb.TagNumber(3)
  GoogleNativeCredential get google => $_getN(2);
  @$pb.TagNumber(3)
  set google(GoogleNativeCredential value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasGoogle() => $_has(2);
  @$pb.TagNumber(3)
  void clearGoogle() => $_clearField(3);
  @$pb.TagNumber(3)
  GoogleNativeCredential ensureGoogle() => $_ensure(2);

  @$pb.TagNumber(4)
  AppleNativeCredential get apple => $_getN(3);
  @$pb.TagNumber(4)
  set apple(AppleNativeCredential value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasApple() => $_has(3);
  @$pb.TagNumber(4)
  void clearApple() => $_clearField(4);
  @$pb.TagNumber(4)
  AppleNativeCredential ensureApple() => $_ensure(3);
}

class NativeSession extends $pb.GeneratedMessage {
  factory NativeSession({
    $core.String? credentialProfile,
    $core.String? clientProfileId,
    $core.String? serviceOrigin,
    $0.Timestamp? createdAt,
    $0.Timestamp? expiresAt,
    $0.Timestamp? authTime,
    $1.SecurityIdentity? identity,
    $1.SecurityVersion? version,
    $core.String? userId,
    $core.String? personalPrefix,
  }) {
    final result = create();
    if (credentialProfile != null) result.credentialProfile = credentialProfile;
    if (clientProfileId != null) result.clientProfileId = clientProfileId;
    if (serviceOrigin != null) result.serviceOrigin = serviceOrigin;
    if (createdAt != null) result.createdAt = createdAt;
    if (expiresAt != null) result.expiresAt = expiresAt;
    if (authTime != null) result.authTime = authTime;
    if (identity != null) result.identity = identity;
    if (version != null) result.version = version;
    if (userId != null) result.userId = userId;
    if (personalPrefix != null) result.personalPrefix = personalPrefix;
    return result;
  }

  NativeSession._();

  factory NativeSession.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory NativeSession.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'NativeSession',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'credentialProfile')
    ..aOS(2, _omitFieldNames ? '' : 'clientProfileId')
    ..aOS(3, _omitFieldNames ? '' : 'serviceOrigin')
    ..aOM<$0.Timestamp>(4, _omitFieldNames ? '' : 'createdAt',
        subBuilder: $0.Timestamp.create)
    ..aOM<$0.Timestamp>(5, _omitFieldNames ? '' : 'expiresAt',
        subBuilder: $0.Timestamp.create)
    ..aOM<$0.Timestamp>(6, _omitFieldNames ? '' : 'authTime',
        subBuilder: $0.Timestamp.create)
    ..aOM<$1.SecurityIdentity>(7, _omitFieldNames ? '' : 'identity',
        subBuilder: $1.SecurityIdentity.create)
    ..aOM<$1.SecurityVersion>(8, _omitFieldNames ? '' : 'version',
        subBuilder: $1.SecurityVersion.create)
    ..aOS(9, _omitFieldNames ? '' : 'userId')
    ..aOS(10, _omitFieldNames ? '' : 'personalPrefix')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  NativeSession clone() => NativeSession()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  NativeSession copyWith(void Function(NativeSession) updates) =>
      super.copyWith((message) => updates(message as NativeSession))
          as NativeSession;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static NativeSession create() => NativeSession._();
  @$core.override
  NativeSession createEmptyInstance() => create();
  static $pb.PbList<NativeSession> createRepeated() =>
      $pb.PbList<NativeSession>();
  @$core.pragma('dart2js:noInline')
  static NativeSession getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<NativeSession>(create);
  static NativeSession? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get credentialProfile => $_getSZ(0);
  @$pb.TagNumber(1)
  set credentialProfile($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasCredentialProfile() => $_has(0);
  @$pb.TagNumber(1)
  void clearCredentialProfile() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get clientProfileId => $_getSZ(1);
  @$pb.TagNumber(2)
  set clientProfileId($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasClientProfileId() => $_has(1);
  @$pb.TagNumber(2)
  void clearClientProfileId() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get serviceOrigin => $_getSZ(2);
  @$pb.TagNumber(3)
  set serviceOrigin($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasServiceOrigin() => $_has(2);
  @$pb.TagNumber(3)
  void clearServiceOrigin() => $_clearField(3);

  @$pb.TagNumber(4)
  $0.Timestamp get createdAt => $_getN(3);
  @$pb.TagNumber(4)
  set createdAt($0.Timestamp value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasCreatedAt() => $_has(3);
  @$pb.TagNumber(4)
  void clearCreatedAt() => $_clearField(4);
  @$pb.TagNumber(4)
  $0.Timestamp ensureCreatedAt() => $_ensure(3);

  @$pb.TagNumber(5)
  $0.Timestamp get expiresAt => $_getN(4);
  @$pb.TagNumber(5)
  set expiresAt($0.Timestamp value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasExpiresAt() => $_has(4);
  @$pb.TagNumber(5)
  void clearExpiresAt() => $_clearField(5);
  @$pb.TagNumber(5)
  $0.Timestamp ensureExpiresAt() => $_ensure(4);

  /// Absent means unknown. Session creation or token iat never fills this field.
  @$pb.TagNumber(6)
  $0.Timestamp get authTime => $_getN(5);
  @$pb.TagNumber(6)
  set authTime($0.Timestamp value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasAuthTime() => $_has(5);
  @$pb.TagNumber(6)
  void clearAuthTime() => $_clearField(6);
  @$pb.TagNumber(6)
  $0.Timestamp ensureAuthTime() => $_ensure(5);

  @$pb.TagNumber(7)
  $1.SecurityIdentity get identity => $_getN(6);
  @$pb.TagNumber(7)
  set identity($1.SecurityIdentity value) => $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasIdentity() => $_has(6);
  @$pb.TagNumber(7)
  void clearIdentity() => $_clearField(7);
  @$pb.TagNumber(7)
  $1.SecurityIdentity ensureIdentity() => $_ensure(6);

  @$pb.TagNumber(8)
  $1.SecurityVersion get version => $_getN(7);
  @$pb.TagNumber(8)
  set version($1.SecurityVersion value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasVersion() => $_has(7);
  @$pb.TagNumber(8)
  void clearVersion() => $_clearField(8);
  @$pb.TagNumber(8)
  $1.SecurityVersion ensureVersion() => $_ensure(7);

  /// Trusted installed Principal metadata, absent for a UID-less legacy user.
  @$pb.TagNumber(9)
  $core.String get userId => $_getSZ(8);
  @$pb.TagNumber(9)
  set userId($core.String value) => $_setString(8, value);
  @$pb.TagNumber(9)
  $core.bool hasUserId() => $_has(8);
  @$pb.TagNumber(9)
  void clearUserId() => $_clearField(9);

  /// Logical prefix; callers must not prepend the internal physical 'data:'.
  @$pb.TagNumber(10)
  $core.String get personalPrefix => $_getSZ(9);
  @$pb.TagNumber(10)
  set personalPrefix($core.String value) => $_setString(9, value);
  @$pb.TagNumber(10)
  $core.bool hasPersonalPrefix() => $_has(9);
  @$pb.TagNumber(10)
  void clearPersonalPrefix() => $_clearField(10);
}

class CompleteNativeLoginResponse extends $pb.GeneratedMessage {
  factory CompleteNativeLoginResponse({
    $core.String? credential,
    NativeSession? session,
  }) {
    final result = create();
    if (credential != null) result.credential = credential;
    if (session != null) result.session = session;
    return result;
  }

  CompleteNativeLoginResponse._();

  factory CompleteNativeLoginResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CompleteNativeLoginResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CompleteNativeLoginResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'credential')
    ..aOM<NativeSession>(2, _omitFieldNames ? '' : 'session',
        subBuilder: NativeSession.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CompleteNativeLoginResponse clone() =>
      CompleteNativeLoginResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CompleteNativeLoginResponse copyWith(
          void Function(CompleteNativeLoginResponse) updates) =>
      super.copyWith(
              (message) => updates(message as CompleteNativeLoginResponse))
          as CompleteNativeLoginResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CompleteNativeLoginResponse create() =>
      CompleteNativeLoginResponse._();
  @$core.override
  CompleteNativeLoginResponse createEmptyInstance() => create();
  static $pb.PbList<CompleteNativeLoginResponse> createRepeated() =>
      $pb.PbList<CompleteNativeLoginResponse>();
  @$core.pragma('dart2js:noInline')
  static CompleteNativeLoginResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CompleteNativeLoginResponse>(create);
  static CompleteNativeLoginResponse? _defaultInstance;

  /// Disclosed only after durable Apply and current revalidation/output fencing.
  @$pb.TagNumber(1)
  $core.String get credential => $_getSZ(0);
  @$pb.TagNumber(1)
  set credential($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasCredential() => $_has(0);
  @$pb.TagNumber(1)
  void clearCredential() => $_clearField(1);

  @$pb.TagNumber(2)
  NativeSession get session => $_getN(1);
  @$pb.TagNumber(2)
  set session(NativeSession value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasSession() => $_has(1);
  @$pb.TagNumber(2)
  void clearSession() => $_clearField(2);
  @$pb.TagNumber(2)
  NativeSession ensureSession() => $_ensure(1);
}

class GetNativeSessionRequest extends $pb.GeneratedMessage {
  factory GetNativeSessionRequest() => create();

  GetNativeSessionRequest._();

  factory GetNativeSessionRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetNativeSessionRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetNativeSessionRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeSessionRequest clone() =>
      GetNativeSessionRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeSessionRequest copyWith(
          void Function(GetNativeSessionRequest) updates) =>
      super.copyWith((message) => updates(message as GetNativeSessionRequest))
          as GetNativeSessionRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetNativeSessionRequest create() => GetNativeSessionRequest._();
  @$core.override
  GetNativeSessionRequest createEmptyInstance() => create();
  static $pb.PbList<GetNativeSessionRequest> createRepeated() =>
      $pb.PbList<GetNativeSessionRequest>();
  @$core.pragma('dart2js:noInline')
  static GetNativeSessionRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetNativeSessionRequest>(create);
  static GetNativeSessionRequest? _defaultInstance;
}

class GetNativeSessionResponse extends $pb.GeneratedMessage {
  factory GetNativeSessionResponse({
    NativeSession? session,
    $core.Iterable<$1.SecurityRole>? roles,
  }) {
    final result = create();
    if (session != null) result.session = session;
    if (roles != null) result.roles.addAll(roles);
    return result;
  }

  GetNativeSessionResponse._();

  factory GetNativeSessionResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetNativeSessionResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetNativeSessionResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<NativeSession>(1, _omitFieldNames ? '' : 'session',
        subBuilder: NativeSession.create)
    ..pc<$1.SecurityRole>(2, _omitFieldNames ? '' : 'roles', $pb.PbFieldType.PM,
        subBuilder: $1.SecurityRole.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeSessionResponse clone() =>
      GetNativeSessionResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeSessionResponse copyWith(
          void Function(GetNativeSessionResponse) updates) =>
      super.copyWith((message) => updates(message as GetNativeSessionResponse))
          as GetNativeSessionResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetNativeSessionResponse create() => GetNativeSessionResponse._();
  @$core.override
  GetNativeSessionResponse createEmptyInstance() => create();
  static $pb.PbList<GetNativeSessionResponse> createRepeated() =>
      $pb.PbList<GetNativeSessionResponse>();
  @$core.pragma('dart2js:noInline')
  static GetNativeSessionResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetNativeSessionResponse>(create);
  static GetNativeSessionResponse? _defaultInstance;

  @$pb.TagNumber(1)
  NativeSession get session => $_getN(0);
  @$pb.TagNumber(1)
  set session(NativeSession value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasSession() => $_has(0);
  @$pb.TagNumber(1)
  void clearSession() => $_clearField(1);
  @$pb.TagNumber(1)
  NativeSession ensureSession() => $_ensure(0);

  @$pb.TagNumber(2)
  $pb.PbList<$1.SecurityRole> get roles => $_getList(1);
}

class PrepareNativeLogoutRequest extends $pb.GeneratedMessage {
  factory PrepareNativeLogoutRequest() => create();

  PrepareNativeLogoutRequest._();

  factory PrepareNativeLogoutRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PrepareNativeLogoutRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PrepareNativeLogoutRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeLogoutRequest clone() =>
      PrepareNativeLogoutRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeLogoutRequest copyWith(
          void Function(PrepareNativeLogoutRequest) updates) =>
      super.copyWith(
              (message) => updates(message as PrepareNativeLogoutRequest))
          as PrepareNativeLogoutRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PrepareNativeLogoutRequest create() => PrepareNativeLogoutRequest._();
  @$core.override
  PrepareNativeLogoutRequest createEmptyInstance() => create();
  static $pb.PbList<PrepareNativeLogoutRequest> createRepeated() =>
      $pb.PbList<PrepareNativeLogoutRequest>();
  @$core.pragma('dart2js:noInline')
  static PrepareNativeLogoutRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PrepareNativeLogoutRequest>(create);
  static PrepareNativeLogoutRequest? _defaultInstance;
}

class PrepareNativeLogoutResponse extends $pb.GeneratedMessage {
  factory PrepareNativeLogoutResponse({
    $1.CurrentSessionRevocationReview? review,
  }) {
    final result = create();
    if (review != null) result.review = review;
    return result;
  }

  PrepareNativeLogoutResponse._();

  factory PrepareNativeLogoutResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PrepareNativeLogoutResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PrepareNativeLogoutResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<$1.CurrentSessionRevocationReview>(1, _omitFieldNames ? '' : 'review',
        subBuilder: $1.CurrentSessionRevocationReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeLogoutResponse clone() =>
      PrepareNativeLogoutResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeLogoutResponse copyWith(
          void Function(PrepareNativeLogoutResponse) updates) =>
      super.copyWith(
              (message) => updates(message as PrepareNativeLogoutResponse))
          as PrepareNativeLogoutResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PrepareNativeLogoutResponse create() =>
      PrepareNativeLogoutResponse._();
  @$core.override
  PrepareNativeLogoutResponse createEmptyInstance() => create();
  static $pb.PbList<PrepareNativeLogoutResponse> createRepeated() =>
      $pb.PbList<PrepareNativeLogoutResponse>();
  @$core.pragma('dart2js:noInline')
  static PrepareNativeLogoutResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PrepareNativeLogoutResponse>(create);
  static PrepareNativeLogoutResponse? _defaultInstance;

  /// Retain before Apply. Status recovery uses GetSecurityChangeStatus and the
  /// same ID/intent, potentially under a later session for this exact Principal.
  @$pb.TagNumber(1)
  $1.CurrentSessionRevocationReview get review => $_getN(0);
  @$pb.TagNumber(1)
  set review($1.CurrentSessionRevocationReview value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasReview() => $_has(0);
  @$pb.TagNumber(1)
  void clearReview() => $_clearField(1);
  @$pb.TagNumber(1)
  $1.CurrentSessionRevocationReview ensureReview() => $_ensure(0);
}

class ApplyNativeLogoutRequest extends $pb.GeneratedMessage {
  factory ApplyNativeLogoutRequest({
    $1.CurrentSessionRevocationReview? review,
  }) {
    final result = create();
    if (review != null) result.review = review;
    return result;
  }

  ApplyNativeLogoutRequest._();

  factory ApplyNativeLogoutRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplyNativeLogoutRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplyNativeLogoutRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<$1.CurrentSessionRevocationReview>(1, _omitFieldNames ? '' : 'review',
        subBuilder: $1.CurrentSessionRevocationReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeLogoutRequest clone() =>
      ApplyNativeLogoutRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeLogoutRequest copyWith(
          void Function(ApplyNativeLogoutRequest) updates) =>
      super.copyWith((message) => updates(message as ApplyNativeLogoutRequest))
          as ApplyNativeLogoutRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplyNativeLogoutRequest create() => ApplyNativeLogoutRequest._();
  @$core.override
  ApplyNativeLogoutRequest createEmptyInstance() => create();
  static $pb.PbList<ApplyNativeLogoutRequest> createRepeated() =>
      $pb.PbList<ApplyNativeLogoutRequest>();
  @$core.pragma('dart2js:noInline')
  static ApplyNativeLogoutRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplyNativeLogoutRequest>(create);
  static ApplyNativeLogoutRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $1.CurrentSessionRevocationReview get review => $_getN(0);
  @$pb.TagNumber(1)
  set review($1.CurrentSessionRevocationReview value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasReview() => $_has(0);
  @$pb.TagNumber(1)
  void clearReview() => $_clearField(1);
  @$pb.TagNumber(1)
  $1.CurrentSessionRevocationReview ensureReview() => $_ensure(0);
}

class ApplyNativeLogoutResponse extends $pb.GeneratedMessage {
  factory ApplyNativeLogoutResponse({
    $core.bool? clearLocalSession,
    $1.CurrentSecurityChangeResult? currentResult,
  }) {
    final result = create();
    if (clearLocalSession != null) result.clearLocalSession = clearLocalSession;
    if (currentResult != null) result.currentResult = currentResult;
    return result;
  }

  ApplyNativeLogoutResponse._();

  factory ApplyNativeLogoutResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplyNativeLogoutResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplyNativeLogoutResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'clearLocalSession')
    ..aOM<$1.CurrentSecurityChangeResult>(
        2, _omitFieldNames ? '' : 'currentResult',
        subBuilder: $1.CurrentSecurityChangeResult.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeLogoutResponse clone() =>
      ApplyNativeLogoutResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeLogoutResponse copyWith(
          void Function(ApplyNativeLogoutResponse) updates) =>
      super.copyWith((message) => updates(message as ApplyNativeLogoutResponse))
          as ApplyNativeLogoutResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplyNativeLogoutResponse create() => ApplyNativeLogoutResponse._();
  @$core.override
  ApplyNativeLogoutResponse createEmptyInstance() => create();
  static $pb.PbList<ApplyNativeLogoutResponse> createRepeated() =>
      $pb.PbList<ApplyNativeLogoutResponse>();
  @$core.pragma('dart2js:noInline')
  static ApplyNativeLogoutResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplyNativeLogoutResponse>(create);
  static ApplyNativeLogoutResponse? _defaultInstance;

  /// Local app teardown is independent of the durable original outcome.
  @$pb.TagNumber(1)
  $core.bool get clearLocalSession => $_getBF(0);
  @$pb.TagNumber(1)
  set clearLocalSession($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasClearLocalSession() => $_has(0);
  @$pb.TagNumber(1)
  void clearClearLocalSession() => $_clearField(1);

  /// Absence means unknown/unavailable for disclosure, never rollback.
  @$pb.TagNumber(2)
  $1.CurrentSecurityChangeResult get currentResult => $_getN(1);
  @$pb.TagNumber(2)
  set currentResult($1.CurrentSecurityChangeResult value) =>
      $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasCurrentResult() => $_has(1);
  @$pb.TagNumber(2)
  void clearCurrentResult() => $_clearField(2);
  @$pb.TagNumber(2)
  $1.CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(1);
}

/// Exact Server-minted business review. Persist before Apply. Possession alone
/// grants no authority; unknown status never permits restamping or a new intent.
class NativeRegistrationReview extends $pb.GeneratedMessage {
  factory NativeRegistrationReview({
    $1.CurrentAuthorityProfile? profile,
    $1.CurrentSemanticCut? expectedCut,
    $1.CurrentSecurityChangeID? changeId,
    $1.SecurityIdentity? actor,
    $core.List<$core.int>? intentDigest,
    $core.String? userId,
    $core.String? clientProfileId,
    $fixnum.Int64? issuerConfigRevision,
  }) {
    final result = create();
    if (profile != null) result.profile = profile;
    if (expectedCut != null) result.expectedCut = expectedCut;
    if (changeId != null) result.changeId = changeId;
    if (actor != null) result.actor = actor;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (userId != null) result.userId = userId;
    if (clientProfileId != null) result.clientProfileId = clientProfileId;
    if (issuerConfigRevision != null)
      result.issuerConfigRevision = issuerConfigRevision;
    return result;
  }

  NativeRegistrationReview._();

  factory NativeRegistrationReview.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory NativeRegistrationReview.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'NativeRegistrationReview',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<$1.CurrentAuthorityProfile>(1, _omitFieldNames ? '' : 'profile',
        subBuilder: $1.CurrentAuthorityProfile.create)
    ..aOM<$1.CurrentSemanticCut>(2, _omitFieldNames ? '' : 'expectedCut',
        subBuilder: $1.CurrentSemanticCut.create)
    ..aOM<$1.CurrentSecurityChangeID>(3, _omitFieldNames ? '' : 'changeId',
        subBuilder: $1.CurrentSecurityChangeID.create)
    ..aOM<$1.SecurityIdentity>(4, _omitFieldNames ? '' : 'actor',
        subBuilder: $1.SecurityIdentity.create)
    ..a<$core.List<$core.int>>(
        5, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..aOS(6, _omitFieldNames ? '' : 'userId')
    ..aOS(7, _omitFieldNames ? '' : 'clientProfileId')
    ..a<$fixnum.Int64>(
        8, _omitFieldNames ? '' : 'issuerConfigRevision', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  NativeRegistrationReview clone() =>
      NativeRegistrationReview()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  NativeRegistrationReview copyWith(
          void Function(NativeRegistrationReview) updates) =>
      super.copyWith((message) => updates(message as NativeRegistrationReview))
          as NativeRegistrationReview;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static NativeRegistrationReview create() => NativeRegistrationReview._();
  @$core.override
  NativeRegistrationReview createEmptyInstance() => create();
  static $pb.PbList<NativeRegistrationReview> createRepeated() =>
      $pb.PbList<NativeRegistrationReview>();
  @$core.pragma('dart2js:noInline')
  static NativeRegistrationReview getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<NativeRegistrationReview>(create);
  static NativeRegistrationReview? _defaultInstance;

  @$pb.TagNumber(1)
  $1.CurrentAuthorityProfile get profile => $_getN(0);
  @$pb.TagNumber(1)
  set profile($1.CurrentAuthorityProfile value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasProfile() => $_has(0);
  @$pb.TagNumber(1)
  void clearProfile() => $_clearField(1);
  @$pb.TagNumber(1)
  $1.CurrentAuthorityProfile ensureProfile() => $_ensure(0);

  @$pb.TagNumber(2)
  $1.CurrentSemanticCut get expectedCut => $_getN(1);
  @$pb.TagNumber(2)
  set expectedCut($1.CurrentSemanticCut value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasExpectedCut() => $_has(1);
  @$pb.TagNumber(2)
  void clearExpectedCut() => $_clearField(2);
  @$pb.TagNumber(2)
  $1.CurrentSemanticCut ensureExpectedCut() => $_ensure(1);

  @$pb.TagNumber(3)
  $1.CurrentSecurityChangeID get changeId => $_getN(2);
  @$pb.TagNumber(3)
  set changeId($1.CurrentSecurityChangeID value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasChangeId() => $_has(2);
  @$pb.TagNumber(3)
  void clearChangeId() => $_clearField(3);
  @$pb.TagNumber(3)
  $1.CurrentSecurityChangeID ensureChangeId() => $_ensure(2);

  @$pb.TagNumber(4)
  $1.SecurityIdentity get actor => $_getN(3);
  @$pb.TagNumber(4)
  set actor($1.SecurityIdentity value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasActor() => $_has(3);
  @$pb.TagNumber(4)
  void clearActor() => $_clearField(4);
  @$pb.TagNumber(4)
  $1.SecurityIdentity ensureActor() => $_ensure(3);

  @$pb.TagNumber(5)
  $core.List<$core.int> get intentDigest => $_getN(4);
  @$pb.TagNumber(5)
  set intentDigest($core.List<$core.int> value) => $_setBytes(4, value);
  @$pb.TagNumber(5)
  $core.bool hasIntentDigest() => $_has(4);
  @$pb.TagNumber(5)
  void clearIntentDigest() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get userId => $_getSZ(5);
  @$pb.TagNumber(6)
  set userId($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasUserId() => $_has(5);
  @$pb.TagNumber(6)
  void clearUserId() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get clientProfileId => $_getSZ(6);
  @$pb.TagNumber(7)
  set clientProfileId($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasClientProfileId() => $_has(6);
  @$pb.TagNumber(7)
  void clearClientProfileId() => $_clearField(7);

  @$pb.TagNumber(8)
  $fixnum.Int64 get issuerConfigRevision => $_getI64(7);
  @$pb.TagNumber(8)
  set issuerConfigRevision($fixnum.Int64 value) => $_setInt64(7, value);
  @$pb.TagNumber(8)
  $core.bool hasIssuerConfigRevision() => $_has(7);
  @$pb.TagNumber(8)
  void clearIssuerConfigRevision() => $_clearField(8);
}

class BeginNativeRegistrationRequest extends $pb.GeneratedMessage {
  factory BeginNativeRegistrationRequest({
    $core.String? issuer,
    $core.String? clientProfileId,
    NativeRegistrationReview? recoveryReview,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    if (clientProfileId != null) result.clientProfileId = clientProfileId;
    if (recoveryReview != null) result.recoveryReview = recoveryReview;
    return result;
  }

  BeginNativeRegistrationRequest._();

  factory BeginNativeRegistrationRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BeginNativeRegistrationRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BeginNativeRegistrationRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'issuer')
    ..aOS(2, _omitFieldNames ? '' : 'clientProfileId')
    ..aOM<NativeRegistrationReview>(3, _omitFieldNames ? '' : 'recoveryReview',
        subBuilder: NativeRegistrationReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeRegistrationRequest clone() =>
      BeginNativeRegistrationRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeRegistrationRequest copyWith(
          void Function(BeginNativeRegistrationRequest) updates) =>
      super.copyWith(
              (message) => updates(message as BeginNativeRegistrationRequest))
          as BeginNativeRegistrationRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BeginNativeRegistrationRequest create() =>
      BeginNativeRegistrationRequest._();
  @$core.override
  BeginNativeRegistrationRequest createEmptyInstance() => create();
  static $pb.PbList<BeginNativeRegistrationRequest> createRepeated() =>
      $pb.PbList<BeginNativeRegistrationRequest>();
  @$core.pragma('dart2js:noInline')
  static BeginNativeRegistrationRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BeginNativeRegistrationRequest>(create);
  static BeginNativeRegistrationRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get issuer => $_getSZ(0);
  @$pb.TagNumber(1)
  set issuer($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearIssuer() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get clientProfileId => $_getSZ(1);
  @$pb.TagNumber(2)
  set clientProfileId($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasClientProfileId() => $_has(1);
  @$pb.TagNumber(2)
  void clearClientProfileId() => $_clearField(2);

  /// Presence selects recovery-only authentication for this original review.
  @$pb.TagNumber(3)
  NativeRegistrationReview get recoveryReview => $_getN(2);
  @$pb.TagNumber(3)
  set recoveryReview(NativeRegistrationReview value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasRecoveryReview() => $_has(2);
  @$pb.TagNumber(3)
  void clearRecoveryReview() => $_clearField(3);
  @$pb.TagNumber(3)
  NativeRegistrationReview ensureRecoveryReview() => $_ensure(2);
}

class BeginNativeRegistrationResponse extends $pb.GeneratedMessage {
  factory BeginNativeRegistrationResponse({
    BeginNativeLoginResponse? attempt,
  }) {
    final result = create();
    if (attempt != null) result.attempt = attempt;
    return result;
  }

  BeginNativeRegistrationResponse._();

  factory BeginNativeRegistrationResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BeginNativeRegistrationResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BeginNativeRegistrationResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<BeginNativeLoginResponse>(1, _omitFieldNames ? '' : 'attempt',
        subBuilder: BeginNativeLoginResponse.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeRegistrationResponse clone() =>
      BeginNativeRegistrationResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginNativeRegistrationResponse copyWith(
          void Function(BeginNativeRegistrationResponse) updates) =>
      super.copyWith(
              (message) => updates(message as BeginNativeRegistrationResponse))
          as BeginNativeRegistrationResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BeginNativeRegistrationResponse create() =>
      BeginNativeRegistrationResponse._();
  @$core.override
  BeginNativeRegistrationResponse createEmptyInstance() => create();
  static $pb.PbList<BeginNativeRegistrationResponse> createRepeated() =>
      $pb.PbList<BeginNativeRegistrationResponse>();
  @$core.pragma('dart2js:noInline')
  static BeginNativeRegistrationResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BeginNativeRegistrationResponse>(
          create);
  static BeginNativeRegistrationResponse? _defaultInstance;

  /// Share the provider challenge shape; registration purpose stays Server-owned.
  @$pb.TagNumber(1)
  BeginNativeLoginResponse get attempt => $_getN(0);
  @$pb.TagNumber(1)
  set attempt(BeginNativeLoginResponse value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasAttempt() => $_has(0);
  @$pb.TagNumber(1)
  void clearAttempt() => $_clearField(1);
  @$pb.TagNumber(1)
  BeginNativeLoginResponse ensureAttempt() => $_ensure(0);
}

enum PrepareNativeRegistrationRequest_Provider { google, apple, notSet }

class PrepareNativeRegistrationRequest extends $pb.GeneratedMessage {
  factory PrepareNativeRegistrationRequest({
    $core.String? attemptId,
    $core.String? completionSecret,
    GoogleNativeCredential? google,
    AppleNativeCredential? apple,
    NativeRegistrationReview? recoveryReview,
  }) {
    final result = create();
    if (attemptId != null) result.attemptId = attemptId;
    if (completionSecret != null) result.completionSecret = completionSecret;
    if (google != null) result.google = google;
    if (apple != null) result.apple = apple;
    if (recoveryReview != null) result.recoveryReview = recoveryReview;
    return result;
  }

  PrepareNativeRegistrationRequest._();

  factory PrepareNativeRegistrationRequest.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PrepareNativeRegistrationRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, PrepareNativeRegistrationRequest_Provider>
      _PrepareNativeRegistrationRequest_ProviderByTag = {
    3: PrepareNativeRegistrationRequest_Provider.google,
    4: PrepareNativeRegistrationRequest_Provider.apple,
    0: PrepareNativeRegistrationRequest_Provider.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PrepareNativeRegistrationRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [3, 4])
    ..aOS(1, _omitFieldNames ? '' : 'attemptId')
    ..aOS(2, _omitFieldNames ? '' : 'completionSecret')
    ..aOM<GoogleNativeCredential>(3, _omitFieldNames ? '' : 'google',
        subBuilder: GoogleNativeCredential.create)
    ..aOM<AppleNativeCredential>(4, _omitFieldNames ? '' : 'apple',
        subBuilder: AppleNativeCredential.create)
    ..aOM<NativeRegistrationReview>(5, _omitFieldNames ? '' : 'recoveryReview',
        subBuilder: NativeRegistrationReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeRegistrationRequest clone() =>
      PrepareNativeRegistrationRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeRegistrationRequest copyWith(
          void Function(PrepareNativeRegistrationRequest) updates) =>
      super.copyWith(
              (message) => updates(message as PrepareNativeRegistrationRequest))
          as PrepareNativeRegistrationRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PrepareNativeRegistrationRequest create() =>
      PrepareNativeRegistrationRequest._();
  @$core.override
  PrepareNativeRegistrationRequest createEmptyInstance() => create();
  static $pb.PbList<PrepareNativeRegistrationRequest> createRepeated() =>
      $pb.PbList<PrepareNativeRegistrationRequest>();
  @$core.pragma('dart2js:noInline')
  static PrepareNativeRegistrationRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PrepareNativeRegistrationRequest>(
          create);
  static PrepareNativeRegistrationRequest? _defaultInstance;

  PrepareNativeRegistrationRequest_Provider whichProvider() =>
      _PrepareNativeRegistrationRequest_ProviderByTag[$_whichOneof(0)]!;
  void clearProvider() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $core.String get attemptId => $_getSZ(0);
  @$pb.TagNumber(1)
  set attemptId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAttemptId() => $_has(0);
  @$pb.TagNumber(1)
  void clearAttemptId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get completionSecret => $_getSZ(1);
  @$pb.TagNumber(2)
  set completionSecret($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCompletionSecret() => $_has(1);
  @$pb.TagNumber(2)
  void clearCompletionSecret() => $_clearField(2);

  @$pb.TagNumber(3)
  GoogleNativeCredential get google => $_getN(2);
  @$pb.TagNumber(3)
  set google(GoogleNativeCredential value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasGoogle() => $_has(2);
  @$pb.TagNumber(3)
  void clearGoogle() => $_clearField(3);
  @$pb.TagNumber(3)
  GoogleNativeCredential ensureGoogle() => $_ensure(2);

  @$pb.TagNumber(4)
  AppleNativeCredential get apple => $_getN(3);
  @$pb.TagNumber(4)
  set apple(AppleNativeCredential value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasApple() => $_has(3);
  @$pb.TagNumber(4)
  void clearApple() => $_clearField(4);
  @$pb.TagNumber(4)
  AppleNativeCredential ensureApple() => $_ensure(3);

  /// Must match Begin exactly; absent for a new registration.
  @$pb.TagNumber(5)
  NativeRegistrationReview get recoveryReview => $_getN(4);
  @$pb.TagNumber(5)
  set recoveryReview(NativeRegistrationReview value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasRecoveryReview() => $_has(4);
  @$pb.TagNumber(5)
  void clearRecoveryReview() => $_clearField(5);
  @$pb.TagNumber(5)
  NativeRegistrationReview ensureRecoveryReview() => $_ensure(4);
}

class PrepareNativeRegistrationResponse extends $pb.GeneratedMessage {
  factory PrepareNativeRegistrationResponse({
    NativeRegistrationReview? review,
    $core.String? proof,
    $0.Timestamp? expiresAt,
    $core.String? affinity,
  }) {
    final result = create();
    if (review != null) result.review = review;
    if (proof != null) result.proof = proof;
    if (expiresAt != null) result.expiresAt = expiresAt;
    if (affinity != null) result.affinity = affinity;
    return result;
  }

  PrepareNativeRegistrationResponse._();

  factory PrepareNativeRegistrationResponse.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PrepareNativeRegistrationResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PrepareNativeRegistrationResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<NativeRegistrationReview>(1, _omitFieldNames ? '' : 'review',
        subBuilder: NativeRegistrationReview.create)
    ..aOS(2, _omitFieldNames ? '' : 'proof')
    ..aOM<$0.Timestamp>(3, _omitFieldNames ? '' : 'expiresAt',
        subBuilder: $0.Timestamp.create)
    ..aOS(4, _omitFieldNames ? '' : 'affinity')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeRegistrationResponse clone() =>
      PrepareNativeRegistrationResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareNativeRegistrationResponse copyWith(
          void Function(PrepareNativeRegistrationResponse) updates) =>
      super.copyWith((message) =>
              updates(message as PrepareNativeRegistrationResponse))
          as PrepareNativeRegistrationResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PrepareNativeRegistrationResponse create() =>
      PrepareNativeRegistrationResponse._();
  @$core.override
  PrepareNativeRegistrationResponse createEmptyInstance() => create();
  static $pb.PbList<PrepareNativeRegistrationResponse> createRepeated() =>
      $pb.PbList<PrepareNativeRegistrationResponse>();
  @$core.pragma('dart2js:noInline')
  static PrepareNativeRegistrationResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PrepareNativeRegistrationResponse>(
          create);
  static PrepareNativeRegistrationResponse? _defaultInstance;

  @$pb.TagNumber(1)
  NativeRegistrationReview get review => $_getN(0);
  @$pb.TagNumber(1)
  set review(NativeRegistrationReview value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasReview() => $_has(0);
  @$pb.TagNumber(1)
  void clearReview() => $_clearField(1);
  @$pb.TagNumber(1)
  NativeRegistrationReview ensureReview() => $_ensure(0);

  /// Volatile process-bound proof; never persist or send to a provider.
  @$pb.TagNumber(2)
  $core.String get proof => $_getSZ(1);
  @$pb.TagNumber(2)
  set proof($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasProof() => $_has(1);
  @$pb.TagNumber(2)
  void clearProof() => $_clearField(2);

  @$pb.TagNumber(3)
  $0.Timestamp get expiresAt => $_getN(2);
  @$pb.TagNumber(3)
  set expiresAt($0.Timestamp value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasExpiresAt() => $_has(2);
  @$pb.TagNumber(3)
  void clearExpiresAt() => $_clearField(3);
  @$pb.TagNumber(3)
  $0.Timestamp ensureExpiresAt() => $_ensure(2);

  @$pb.TagNumber(4)
  $core.String get affinity => $_getSZ(3);
  @$pb.TagNumber(4)
  set affinity($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasAffinity() => $_has(3);
  @$pb.TagNumber(4)
  void clearAffinity() => $_clearField(4);
}

class ApplyNativeRegistrationRequest extends $pb.GeneratedMessage {
  factory ApplyNativeRegistrationRequest({
    NativeRegistrationReview? review,
    $core.String? proof,
  }) {
    final result = create();
    if (review != null) result.review = review;
    if (proof != null) result.proof = proof;
    return result;
  }

  ApplyNativeRegistrationRequest._();

  factory ApplyNativeRegistrationRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplyNativeRegistrationRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplyNativeRegistrationRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<NativeRegistrationReview>(1, _omitFieldNames ? '' : 'review',
        subBuilder: NativeRegistrationReview.create)
    ..aOS(2, _omitFieldNames ? '' : 'proof')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeRegistrationRequest clone() =>
      ApplyNativeRegistrationRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeRegistrationRequest copyWith(
          void Function(ApplyNativeRegistrationRequest) updates) =>
      super.copyWith(
              (message) => updates(message as ApplyNativeRegistrationRequest))
          as ApplyNativeRegistrationRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplyNativeRegistrationRequest create() =>
      ApplyNativeRegistrationRequest._();
  @$core.override
  ApplyNativeRegistrationRequest createEmptyInstance() => create();
  static $pb.PbList<ApplyNativeRegistrationRequest> createRepeated() =>
      $pb.PbList<ApplyNativeRegistrationRequest>();
  @$core.pragma('dart2js:noInline')
  static ApplyNativeRegistrationRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplyNativeRegistrationRequest>(create);
  static ApplyNativeRegistrationRequest? _defaultInstance;

  @$pb.TagNumber(1)
  NativeRegistrationReview get review => $_getN(0);
  @$pb.TagNumber(1)
  set review(NativeRegistrationReview value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasReview() => $_has(0);
  @$pb.TagNumber(1)
  void clearReview() => $_clearField(1);
  @$pb.TagNumber(1)
  NativeRegistrationReview ensureReview() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.String get proof => $_getSZ(1);
  @$pb.TagNumber(2)
  set proof($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasProof() => $_has(1);
  @$pb.TagNumber(2)
  void clearProof() => $_clearField(2);
}

class ApplyNativeRegistrationResponse extends $pb.GeneratedMessage {
  factory ApplyNativeRegistrationResponse({
    $1.CurrentSecurityChangeResult? currentResult,
    $core.String? credential,
    NativeSession? session,
    $core.bool? signInRequired,
  }) {
    final result = create();
    if (currentResult != null) result.currentResult = currentResult;
    if (credential != null) result.credential = credential;
    if (session != null) result.session = session;
    if (signInRequired != null) result.signInRequired = signInRequired;
    return result;
  }

  ApplyNativeRegistrationResponse._();

  factory ApplyNativeRegistrationResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplyNativeRegistrationResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplyNativeRegistrationResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<$1.CurrentSecurityChangeResult>(
        1, _omitFieldNames ? '' : 'currentResult',
        subBuilder: $1.CurrentSecurityChangeResult.create)
    ..aOS(2, _omitFieldNames ? '' : 'credential')
    ..aOM<NativeSession>(3, _omitFieldNames ? '' : 'session',
        subBuilder: NativeSession.create)
    ..aOB(4, _omitFieldNames ? '' : 'signInRequired')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeRegistrationResponse clone() =>
      ApplyNativeRegistrationResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplyNativeRegistrationResponse copyWith(
          void Function(ApplyNativeRegistrationResponse) updates) =>
      super.copyWith(
              (message) => updates(message as ApplyNativeRegistrationResponse))
          as ApplyNativeRegistrationResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplyNativeRegistrationResponse create() =>
      ApplyNativeRegistrationResponse._();
  @$core.override
  ApplyNativeRegistrationResponse createEmptyInstance() => create();
  static $pb.PbList<ApplyNativeRegistrationResponse> createRepeated() =>
      $pb.PbList<ApplyNativeRegistrationResponse>();
  @$core.pragma('dart2js:noInline')
  static ApplyNativeRegistrationResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplyNativeRegistrationResponse>(
          create);
  static ApplyNativeRegistrationResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $1.CurrentSecurityChangeResult get currentResult => $_getN(0);
  @$pb.TagNumber(1)
  set currentResult($1.CurrentSecurityChangeResult value) =>
      $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasCurrentResult() => $_has(0);
  @$pb.TagNumber(1)
  void clearCurrentResult() => $_clearField(1);
  @$pb.TagNumber(1)
  $1.CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(0);

  /// Only the first Apply invocation can disclose a newly issued credential.
  @$pb.TagNumber(2)
  $core.String get credential => $_getSZ(1);
  @$pb.TagNumber(2)
  set credential($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCredential() => $_has(1);
  @$pb.TagNumber(2)
  void clearCredential() => $_clearField(2);

  @$pb.TagNumber(3)
  NativeSession get session => $_getN(2);
  @$pb.TagNumber(3)
  set session(NativeSession value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasSession() => $_has(2);
  @$pb.TagNumber(3)
  void clearSession() => $_clearField(3);
  @$pb.TagNumber(3)
  NativeSession ensureSession() => $_ensure(2);

  /// Registration Applied, but this invocation has no new session credential.
  @$pb.TagNumber(4)
  $core.bool get signInRequired => $_getBF(3);
  @$pb.TagNumber(4)
  set signInRequired($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasSignInRequired() => $_has(3);
  @$pb.TagNumber(4)
  void clearSignInRequired() => $_clearField(4);
}

class GetNativeRegistrationStatusRequest extends $pb.GeneratedMessage {
  factory GetNativeRegistrationStatusRequest({
    NativeRegistrationReview? review,
    $core.String? proof,
  }) {
    final result = create();
    if (review != null) result.review = review;
    if (proof != null) result.proof = proof;
    return result;
  }

  GetNativeRegistrationStatusRequest._();

  factory GetNativeRegistrationStatusRequest.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetNativeRegistrationStatusRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetNativeRegistrationStatusRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<NativeRegistrationReview>(1, _omitFieldNames ? '' : 'review',
        subBuilder: NativeRegistrationReview.create)
    ..aOS(2, _omitFieldNames ? '' : 'proof')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeRegistrationStatusRequest clone() =>
      GetNativeRegistrationStatusRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeRegistrationStatusRequest copyWith(
          void Function(GetNativeRegistrationStatusRequest) updates) =>
      super.copyWith((message) =>
              updates(message as GetNativeRegistrationStatusRequest))
          as GetNativeRegistrationStatusRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetNativeRegistrationStatusRequest create() =>
      GetNativeRegistrationStatusRequest._();
  @$core.override
  GetNativeRegistrationStatusRequest createEmptyInstance() => create();
  static $pb.PbList<GetNativeRegistrationStatusRequest> createRepeated() =>
      $pb.PbList<GetNativeRegistrationStatusRequest>();
  @$core.pragma('dart2js:noInline')
  static GetNativeRegistrationStatusRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetNativeRegistrationStatusRequest>(
          create);
  static GetNativeRegistrationStatusRequest? _defaultInstance;

  @$pb.TagNumber(1)
  NativeRegistrationReview get review => $_getN(0);
  @$pb.TagNumber(1)
  set review(NativeRegistrationReview value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasReview() => $_has(0);
  @$pb.TagNumber(1)
  void clearReview() => $_clearField(1);
  @$pb.TagNumber(1)
  NativeRegistrationReview ensureReview() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.String get proof => $_getSZ(1);
  @$pb.TagNumber(2)
  set proof($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasProof() => $_has(1);
  @$pb.TagNumber(2)
  void clearProof() => $_clearField(2);
}

class GetNativeRegistrationStatusResponse extends $pb.GeneratedMessage {
  factory GetNativeRegistrationStatusResponse({
    $1.CurrentSecurityChangeResult? currentResult,
  }) {
    final result = create();
    if (currentResult != null) result.currentResult = currentResult;
    return result;
  }

  GetNativeRegistrationStatusResponse._();

  factory GetNativeRegistrationStatusResponse.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetNativeRegistrationStatusResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetNativeRegistrationStatusResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<$1.CurrentSecurityChangeResult>(
        1, _omitFieldNames ? '' : 'currentResult',
        subBuilder: $1.CurrentSecurityChangeResult.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeRegistrationStatusResponse clone() =>
      GetNativeRegistrationStatusResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetNativeRegistrationStatusResponse copyWith(
          void Function(GetNativeRegistrationStatusResponse) updates) =>
      super.copyWith((message) =>
              updates(message as GetNativeRegistrationStatusResponse))
          as GetNativeRegistrationStatusResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetNativeRegistrationStatusResponse create() =>
      GetNativeRegistrationStatusResponse._();
  @$core.override
  GetNativeRegistrationStatusResponse createEmptyInstance() => create();
  static $pb.PbList<GetNativeRegistrationStatusResponse> createRepeated() =>
      $pb.PbList<GetNativeRegistrationStatusResponse>();
  @$core.pragma('dart2js:noInline')
  static GetNativeRegistrationStatusResponse getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<
          GetNativeRegistrationStatusResponse>(create);
  static GetNativeRegistrationStatusResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $1.CurrentSecurityChangeResult get currentResult => $_getN(0);
  @$pb.TagNumber(1)
  set currentResult($1.CurrentSecurityChangeResult value) =>
      $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasCurrentResult() => $_has(0);
  @$pb.TagNumber(1)
  void clearCurrentResult() => $_clearField(1);
  @$pb.TagNumber(1)
  $1.CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(0);
}

/// Native provider inputs are accepted only here. Ordinary RPC authentication
/// uses the returned opaque native credential through the existing Bearer path.
/// These unary methods are never automatically retried after possible dispatch.
class LanternNativeAuthServiceApi {
  final $pb.RpcClient _client;

  LanternNativeAuthServiceApi(this._client);

  $async.Future<BeginNativeLoginResponse> beginNativeLogin(
          $pb.ClientContext? ctx, BeginNativeLoginRequest request) =>
      _client.invoke<BeginNativeLoginResponse>(ctx, 'LanternNativeAuthService',
          'BeginNativeLogin', request, BeginNativeLoginResponse());
  $async.Future<CompleteNativeLoginResponse> completeNativeLogin(
          $pb.ClientContext? ctx, CompleteNativeLoginRequest request) =>
      _client.invoke<CompleteNativeLoginResponse>(
          ctx,
          'LanternNativeAuthService',
          'CompleteNativeLogin',
          request,
          CompleteNativeLoginResponse());
  $async.Future<GetNativeSessionResponse> getNativeSession(
          $pb.ClientContext? ctx, GetNativeSessionRequest request) =>
      _client.invoke<GetNativeSessionResponse>(ctx, 'LanternNativeAuthService',
          'GetNativeSession', request, GetNativeSessionResponse());
  $async.Future<PrepareNativeLogoutResponse> prepareNativeLogout(
          $pb.ClientContext? ctx, PrepareNativeLogoutRequest request) =>
      _client.invoke<PrepareNativeLogoutResponse>(
          ctx,
          'LanternNativeAuthService',
          'PrepareNativeLogout',
          request,
          PrepareNativeLogoutResponse());
  $async.Future<ApplyNativeLogoutResponse> applyNativeLogout(
          $pb.ClientContext? ctx, ApplyNativeLogoutRequest request) =>
      _client.invoke<ApplyNativeLogoutResponse>(ctx, 'LanternNativeAuthService',
          'ApplyNativeLogout', request, ApplyNativeLogoutResponse());
  $async.Future<BeginNativeRegistrationResponse> beginNativeRegistration(
          $pb.ClientContext? ctx, BeginNativeRegistrationRequest request) =>
      _client.invoke<BeginNativeRegistrationResponse>(
          ctx,
          'LanternNativeAuthService',
          'BeginNativeRegistration',
          request,
          BeginNativeRegistrationResponse());
  $async.Future<PrepareNativeRegistrationResponse> prepareNativeRegistration(
          $pb.ClientContext? ctx, PrepareNativeRegistrationRequest request) =>
      _client.invoke<PrepareNativeRegistrationResponse>(
          ctx,
          'LanternNativeAuthService',
          'PrepareNativeRegistration',
          request,
          PrepareNativeRegistrationResponse());
  $async.Future<ApplyNativeRegistrationResponse> applyNativeRegistration(
          $pb.ClientContext? ctx, ApplyNativeRegistrationRequest request) =>
      _client.invoke<ApplyNativeRegistrationResponse>(
          ctx,
          'LanternNativeAuthService',
          'ApplyNativeRegistration',
          request,
          ApplyNativeRegistrationResponse());
  $async.Future<GetNativeRegistrationStatusResponse>
      getNativeRegistrationStatus($pb.ClientContext? ctx,
              GetNativeRegistrationStatusRequest request) =>
          _client.invoke<GetNativeRegistrationStatusResponse>(
              ctx,
              'LanternNativeAuthService',
              'GetNativeRegistrationStatus',
              request,
              GetNativeRegistrationStatusResponse());
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
