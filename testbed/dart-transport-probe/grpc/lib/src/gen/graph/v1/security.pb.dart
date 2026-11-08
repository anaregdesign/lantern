// This is a generated file - do not edit.
//
// Generated from graph/v1/security.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:fixnum/fixnum.dart' as $fixnum;
import 'package:protobuf/protobuf.dart' as $pb;
import 'package:protobuf/well_known_types/google/protobuf/timestamp.pb.dart'
    as $1;

import 'security.pbenum.dart';

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

export 'security.pbenum.dart';

class GetAuthCapabilitiesRequest extends $pb.GeneratedMessage {
  factory GetAuthCapabilitiesRequest() => create();

  GetAuthCapabilitiesRequest._();

  factory GetAuthCapabilitiesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetAuthCapabilitiesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetAuthCapabilitiesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAuthCapabilitiesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAuthCapabilitiesRequest copyWith(
          void Function(GetAuthCapabilitiesRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetAuthCapabilitiesRequest))
          as GetAuthCapabilitiesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetAuthCapabilitiesRequest create() => GetAuthCapabilitiesRequest._();
  @$core.override
  GetAuthCapabilitiesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetAuthCapabilitiesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetAuthCapabilitiesRequest>(create);
  static GetAuthCapabilitiesRequest? _defaultInstance;
}

class LoginIssuer extends $pb.GeneratedMessage {
  factory LoginIssuer({
    $core.String? issuer,
    $core.String? label,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    if (label != null) result.label = label;
    return result;
  }

  LoginIssuer._();

  factory LoginIssuer.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory LoginIssuer.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'LoginIssuer',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'issuer')
    ..aOS(2, _omitFieldNames ? '' : 'label')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LoginIssuer clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LoginIssuer copyWith(void Function(LoginIssuer) updates) =>
      super.copyWith((message) => updates(message as LoginIssuer))
          as LoginIssuer;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static LoginIssuer create() => LoginIssuer._();
  @$core.override
  LoginIssuer createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static LoginIssuer getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<LoginIssuer>(create);
  static LoginIssuer? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get issuer => $_getSZ(0);
  @$pb.TagNumber(1)
  set issuer($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearIssuer() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get label => $_getSZ(1);
  @$pb.TagNumber(2)
  set label($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLabel() => $_has(1);
  @$pb.TagNumber(2)
  void clearLabel() => $_clearField(2);
}

class GetAuthCapabilitiesResponse extends $pb.GeneratedMessage {
  factory GetAuthCapabilitiesResponse({
    AuthMode? mode,
    $core.Iterable<LoginIssuer>? loginIssuers,
    $core.String? loginPath,
    $core.int? protocolVersion,
    $core.bool? ready,
    CurrentAuthorityProfile? currentProfile,
    $core.bool? currentOriginEnabled,
    $core.int? currentMember,
  }) {
    final result = create();
    if (mode != null) result.mode = mode;
    if (loginIssuers != null) result.loginIssuers.addAll(loginIssuers);
    if (loginPath != null) result.loginPath = loginPath;
    if (protocolVersion != null) result.protocolVersion = protocolVersion;
    if (ready != null) result.ready = ready;
    if (currentProfile != null) result.currentProfile = currentProfile;
    if (currentOriginEnabled != null)
      result.currentOriginEnabled = currentOriginEnabled;
    if (currentMember != null) result.currentMember = currentMember;
    return result;
  }

  GetAuthCapabilitiesResponse._();

  factory GetAuthCapabilitiesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetAuthCapabilitiesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetAuthCapabilitiesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aE<AuthMode>(1, _omitFieldNames ? '' : 'mode',
        enumValues: AuthMode.values)
    ..pPM<LoginIssuer>(2, _omitFieldNames ? '' : 'loginIssuers',
        subBuilder: LoginIssuer.create)
    ..aOS(3, _omitFieldNames ? '' : 'loginPath')
    ..aI(4, _omitFieldNames ? '' : 'protocolVersion',
        fieldType: $pb.PbFieldType.OU3)
    ..aOB(5, _omitFieldNames ? '' : 'ready')
    ..aOM<CurrentAuthorityProfile>(6, _omitFieldNames ? '' : 'currentProfile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOB(7, _omitFieldNames ? '' : 'currentOriginEnabled')
    ..aI(8, _omitFieldNames ? '' : 'currentMember',
        fieldType: $pb.PbFieldType.OU3)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAuthCapabilitiesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAuthCapabilitiesResponse copyWith(
          void Function(GetAuthCapabilitiesResponse) updates) =>
      super.copyWith(
              (message) => updates(message as GetAuthCapabilitiesResponse))
          as GetAuthCapabilitiesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetAuthCapabilitiesResponse create() =>
      GetAuthCapabilitiesResponse._();
  @$core.override
  GetAuthCapabilitiesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetAuthCapabilitiesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetAuthCapabilitiesResponse>(create);
  static GetAuthCapabilitiesResponse? _defaultInstance;

  @$pb.TagNumber(1)
  AuthMode get mode => $_getN(0);
  @$pb.TagNumber(1)
  set mode(AuthMode value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasMode() => $_has(0);
  @$pb.TagNumber(1)
  void clearMode() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<LoginIssuer> get loginIssuers => $_getList(1);

  @$pb.TagNumber(3)
  $core.String get loginPath => $_getSZ(2);
  @$pb.TagNumber(3)
  set loginPath($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasLoginPath() => $_has(2);
  @$pb.TagNumber(3)
  void clearLoginPath() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.int get protocolVersion => $_getIZ(3);
  @$pb.TagNumber(4)
  set protocolVersion($core.int value) => $_setUnsignedInt32(3, value);
  @$pb.TagNumber(4)
  $core.bool hasProtocolVersion() => $_has(3);
  @$pb.TagNumber(4)
  void clearProtocolVersion() => $_clearField(4);

  /// Explicit OFF is ready; OIDC readiness requires current serving authority.
  /// False never implies OFF, and clients must reject unknown protocol versions.
  @$pb.TagNumber(5)
  $core.bool get ready => $_getBF(4);
  @$pb.TagNumber(5)
  set ready($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasReady() => $_has(4);
  @$pb.TagNumber(5)
  void clearReady() => $_clearField(5);

  /// Required when protocol_version=2. Configuration identity, never a lease.
  @$pb.TagNumber(6)
  CurrentAuthorityProfile get currentProfile => $_getN(5);
  @$pb.TagNumber(6)
  set currentProfile(CurrentAuthorityProfile value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasCurrentProfile() => $_has(5);
  @$pb.TagNumber(6)
  void clearCurrentProfile() => $_clearField(6);
  @$pb.TagNumber(6)
  CurrentAuthorityProfile ensureCurrentProfile() => $_ensure(5);

  /// Routing capability only: an eligible member without an owned origin may
  /// read/complete original work, but cannot mint a new change or login session.
  @$pb.TagNumber(7)
  $core.bool get currentOriginEnabled => $_getBF(6);
  @$pb.TagNumber(7)
  set currentOriginEnabled($core.bool value) => $_setBool(6, value);
  @$pb.TagNumber(7)
  $core.bool hasCurrentOriginEnabled() => $_has(6);
  @$pb.TagNumber(7)
  void clearCurrentOriginEnabled() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.int get currentMember => $_getIZ(7);
  @$pb.TagNumber(8)
  set currentMember($core.int value) => $_setUnsignedInt32(7, value);
  @$pb.TagNumber(8)
  $core.bool hasCurrentMember() => $_has(7);
  @$pb.TagNumber(8)
  void clearCurrentMember() => $_clearField(8);
}

class SecurityIdentity extends $pb.GeneratedMessage {
  factory SecurityIdentity({
    SecurityPrincipalKind? kind,
    $core.String? issuer,
    $core.String? subject,
    $core.String? machineName,
  }) {
    final result = create();
    if (kind != null) result.kind = kind;
    if (issuer != null) result.issuer = issuer;
    if (subject != null) result.subject = subject;
    if (machineName != null) result.machineName = machineName;
    return result;
  }

  SecurityIdentity._();

  factory SecurityIdentity.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityIdentity.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityIdentity',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aE<SecurityPrincipalKind>(1, _omitFieldNames ? '' : 'kind',
        enumValues: SecurityPrincipalKind.values)
    ..aOS(2, _omitFieldNames ? '' : 'issuer')
    ..aOS(3, _omitFieldNames ? '' : 'subject')
    ..aOS(4, _omitFieldNames ? '' : 'machineName')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityIdentity clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityIdentity copyWith(void Function(SecurityIdentity) updates) =>
      super.copyWith((message) => updates(message as SecurityIdentity))
          as SecurityIdentity;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityIdentity create() => SecurityIdentity._();
  @$core.override
  SecurityIdentity createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityIdentity getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityIdentity>(create);
  static SecurityIdentity? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityPrincipalKind get kind => $_getN(0);
  @$pb.TagNumber(1)
  set kind(SecurityPrincipalKind value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasKind() => $_has(0);
  @$pb.TagNumber(1)
  void clearKind() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get issuer => $_getSZ(1);
  @$pb.TagNumber(2)
  set issuer($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasIssuer() => $_has(1);
  @$pb.TagNumber(2)
  void clearIssuer() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get subject => $_getSZ(2);
  @$pb.TagNumber(3)
  set subject($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasSubject() => $_has(2);
  @$pb.TagNumber(3)
  void clearSubject() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get machineName => $_getSZ(3);
  @$pb.TagNumber(4)
  set machineName($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasMachineName() => $_has(3);
  @$pb.TagNumber(4)
  void clearMachineName() => $_clearField(4);
}

/// Inserts the installed Principal's Server-owned UID between byte literals.
/// Before must end with ':' and after must start with ':'. Neither is empty.
/// This is one typed UID slot, not a template language or client-supplied UID.
class SecurityPrincipalUIDPrefix extends $pb.GeneratedMessage {
  factory SecurityPrincipalUIDPrefix({
    $core.String? literalBefore,
    $core.String? literalAfter,
  }) {
    final result = create();
    if (literalBefore != null) result.literalBefore = literalBefore;
    if (literalAfter != null) result.literalAfter = literalAfter;
    return result;
  }

  SecurityPrincipalUIDPrefix._();

  factory SecurityPrincipalUIDPrefix.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityPrincipalUIDPrefix.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityPrincipalUIDPrefix',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'literalBefore')
    ..aOS(2, _omitFieldNames ? '' : 'literalAfter')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityPrincipalUIDPrefix clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityPrincipalUIDPrefix copyWith(
          void Function(SecurityPrincipalUIDPrefix) updates) =>
      super.copyWith(
              (message) => updates(message as SecurityPrincipalUIDPrefix))
          as SecurityPrincipalUIDPrefix;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityPrincipalUIDPrefix create() => SecurityPrincipalUIDPrefix._();
  @$core.override
  SecurityPrincipalUIDPrefix createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityPrincipalUIDPrefix getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityPrincipalUIDPrefix>(create);
  static SecurityPrincipalUIDPrefix? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get literalBefore => $_getSZ(0);
  @$pb.TagNumber(1)
  set literalBefore($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasLiteralBefore() => $_has(0);
  @$pb.TagNumber(1)
  void clearLiteralBefore() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get literalAfter => $_getSZ(1);
  @$pb.TagNumber(2)
  set literalAfter($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLiteralAfter() => $_has(1);
  @$pb.TagNumber(2)
  void clearLiteralAfter() => $_clearField(2);
}

enum SecurityRule_Resource { prefix, principalUidPrefix, notSet }

class SecurityRule extends $pb.GeneratedMessage {
  factory SecurityRule({
    $core.String? id,
    $core.String? prefix,
    SecurityPrincipalUIDPrefix? principalUidPrefix,
    SecurityPermission? permission,
    SecurityResource? resourceKind,
  }) {
    final result = create();
    if (id != null) result.id = id;
    if (prefix != null) result.prefix = prefix;
    if (principalUidPrefix != null)
      result.principalUidPrefix = principalUidPrefix;
    if (permission != null) result.permission = permission;
    if (resourceKind != null) result.resourceKind = resourceKind;
    return result;
  }

  SecurityRule._();

  factory SecurityRule.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityRule.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, SecurityRule_Resource>
      _SecurityRule_ResourceByTag = {
    4: SecurityRule_Resource.prefix,
    7: SecurityRule_Resource.principalUidPrefix,
    0: SecurityRule_Resource.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityRule',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [4, 7])
    ..aOS(1, _omitFieldNames ? '' : 'id')
    ..aOS(4, _omitFieldNames ? '' : 'prefix')
    ..aOM<SecurityPrincipalUIDPrefix>(
        7, _omitFieldNames ? '' : 'principalUidPrefix',
        subBuilder: SecurityPrincipalUIDPrefix.create)
    ..aE<SecurityPermission>(8, _omitFieldNames ? '' : 'permission',
        enumValues: SecurityPermission.values)
    ..aE<SecurityResource>(9, _omitFieldNames ? '' : 'resourceKind',
        enumValues: SecurityResource.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRule clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRule copyWith(void Function(SecurityRule) updates) =>
      super.copyWith((message) => updates(message as SecurityRule))
          as SecurityRule;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityRule create() => SecurityRule._();
  @$core.override
  SecurityRule createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityRule getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityRule>(create);
  static SecurityRule? _defaultInstance;

  @$pb.TagNumber(4)
  @$pb.TagNumber(7)
  SecurityRule_Resource whichResource() =>
      _SecurityRule_ResourceByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(4)
  @$pb.TagNumber(7)
  void clearResource() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $core.String get id => $_getSZ(0);
  @$pb.TagNumber(1)
  set id($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasId() => $_has(0);
  @$pb.TagNumber(1)
  void clearId() => $_clearField(1);

  @$pb.TagNumber(4)
  $core.String get prefix => $_getSZ(1);
  @$pb.TagNumber(4)
  set prefix($core.String value) => $_setString(1, value);
  @$pb.TagNumber(4)
  $core.bool hasPrefix() => $_has(1);
  @$pb.TagNumber(4)
  void clearPrefix() => $_clearField(4);

  @$pb.TagNumber(7)
  SecurityPrincipalUIDPrefix get principalUidPrefix => $_getN(2);
  @$pb.TagNumber(7)
  set principalUidPrefix(SecurityPrincipalUIDPrefix value) =>
      $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasPrincipalUidPrefix() => $_has(2);
  @$pb.TagNumber(7)
  void clearPrincipalUidPrefix() => $_clearField(7);
  @$pb.TagNumber(7)
  SecurityPrincipalUIDPrefix ensurePrincipalUidPrefix() => $_ensure(2);

  /// New tags intentionally reject old effect/action bytes under strict decoding.
  @$pb.TagNumber(8)
  SecurityPermission get permission => $_getN(3);
  @$pb.TagNumber(8)
  set permission(SecurityPermission value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasPermission() => $_has(3);
  @$pb.TagNumber(8)
  void clearPermission() => $_clearField(8);

  @$pb.TagNumber(9)
  SecurityResource get resourceKind => $_getN(4);
  @$pb.TagNumber(9)
  set resourceKind(SecurityResource value) => $_setField(9, value);
  @$pb.TagNumber(9)
  $core.bool hasResourceKind() => $_has(4);
  @$pb.TagNumber(9)
  void clearResourceKind() => $_clearField(9);
}

class SecurityRole extends $pb.GeneratedMessage {
  factory SecurityRole({
    $core.String? id,
    $core.String? name,
    $core.Iterable<SecurityRule>? rules,
    $core.bool? envOwned,
  }) {
    final result = create();
    if (id != null) result.id = id;
    if (name != null) result.name = name;
    if (rules != null) result.rules.addAll(rules);
    if (envOwned != null) result.envOwned = envOwned;
    return result;
  }

  SecurityRole._();

  factory SecurityRole.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityRole.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityRole',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'id')
    ..aOS(2, _omitFieldNames ? '' : 'name')
    ..pPM<SecurityRule>(3, _omitFieldNames ? '' : 'rules',
        subBuilder: SecurityRule.create)
    ..aOB(4, _omitFieldNames ? '' : 'envOwned')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRole clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRole copyWith(void Function(SecurityRole) updates) =>
      super.copyWith((message) => updates(message as SecurityRole))
          as SecurityRole;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityRole create() => SecurityRole._();
  @$core.override
  SecurityRole createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityRole getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityRole>(create);
  static SecurityRole? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get id => $_getSZ(0);
  @$pb.TagNumber(1)
  set id($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasId() => $_has(0);
  @$pb.TagNumber(1)
  void clearId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get name => $_getSZ(1);
  @$pb.TagNumber(2)
  set name($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasName() => $_has(1);
  @$pb.TagNumber(2)
  void clearName() => $_clearField(2);

  @$pb.TagNumber(3)
  $pb.PbList<SecurityRule> get rules => $_getList(2);

  /// Read-only: an environment-owned membership protects this Role policy.
  @$pb.TagNumber(4)
  $core.bool get envOwned => $_getBF(3);
  @$pb.TagNumber(4)
  set envOwned($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasEnvOwned() => $_has(3);
  @$pb.TagNumber(4)
  void clearEnvOwned() => $_clearField(4);
}

class SecurityRoleAssignment extends $pb.GeneratedMessage {
  factory SecurityRoleAssignment({
    SecurityIdentity? identity,
    $core.String? roleId,
    $core.bool? envOwned,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (roleId != null) result.roleId = roleId;
    if (envOwned != null) result.envOwned = envOwned;
    return result;
  }

  SecurityRoleAssignment._();

  factory SecurityRoleAssignment.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityRoleAssignment.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityRoleAssignment',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIdentity>(1, _omitFieldNames ? '' : 'identity',
        subBuilder: SecurityIdentity.create)
    ..aOS(2, _omitFieldNames ? '' : 'roleId')
    ..aOB(3, _omitFieldNames ? '' : 'envOwned')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRoleAssignment clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRoleAssignment copyWith(
          void Function(SecurityRoleAssignment) updates) =>
      super.copyWith((message) => updates(message as SecurityRoleAssignment))
          as SecurityRoleAssignment;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityRoleAssignment create() => SecurityRoleAssignment._();
  @$core.override
  SecurityRoleAssignment createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityRoleAssignment getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityRoleAssignment>(create);
  static SecurityRoleAssignment? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIdentity get identity => $_getN(0);
  @$pb.TagNumber(1)
  set identity(SecurityIdentity value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentity() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentity() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIdentity ensureIdentity() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.String get roleId => $_getSZ(1);
  @$pb.TagNumber(2)
  set roleId($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasRoleId() => $_has(1);
  @$pb.TagNumber(2)
  void clearRoleId() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get envOwned => $_getBF(2);
  @$pb.TagNumber(3)
  set envOwned($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasEnvOwned() => $_has(2);
  @$pb.TagNumber(3)
  void clearEnvOwned() => $_clearField(3);
}

class SecurityUser extends $pb.GeneratedMessage {
  factory SecurityUser({
    SecurityIdentity? identity,
    SecurityPrincipalState? state,
    $core.Iterable<SecurityRoleAssignment>? assignments,
    $core.String? userId,
    $core.String? verifiedEmail,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (state != null) result.state = state;
    if (assignments != null) result.assignments.addAll(assignments);
    if (userId != null) result.userId = userId;
    if (verifiedEmail != null) result.verifiedEmail = verifiedEmail;
    return result;
  }

  SecurityUser._();

  factory SecurityUser.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityUser.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityUser',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIdentity>(1, _omitFieldNames ? '' : 'identity',
        subBuilder: SecurityIdentity.create)
    ..aE<SecurityPrincipalState>(2, _omitFieldNames ? '' : 'state',
        enumValues: SecurityPrincipalState.values)
    ..pPM<SecurityRoleAssignment>(3, _omitFieldNames ? '' : 'assignments',
        subBuilder: SecurityRoleAssignment.create)
    ..aOS(4, _omitFieldNames ? '' : 'userId')
    ..aOS(5, _omitFieldNames ? '' : 'verifiedEmail')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityUser clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityUser copyWith(void Function(SecurityUser) updates) =>
      super.copyWith((message) => updates(message as SecurityUser))
          as SecurityUser;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityUser create() => SecurityUser._();
  @$core.override
  SecurityUser createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityUser getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityUser>(create);
  static SecurityUser? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIdentity get identity => $_getN(0);
  @$pb.TagNumber(1)
  set identity(SecurityIdentity value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentity() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentity() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIdentity ensureIdentity() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityPrincipalState get state => $_getN(1);
  @$pb.TagNumber(2)
  set state(SecurityPrincipalState value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasState() => $_has(1);
  @$pb.TagNumber(2)
  void clearState() => $_clearField(2);

  @$pb.TagNumber(3)
  $pb.PbList<SecurityRoleAssignment> get assignments => $_getList(2);

  /// Read-only, allocated once by explicit verified registration. Ordinary user
  /// management never accepts this value as input; deleted tombstones retain it.
  @$pb.TagNumber(4)
  $core.String get userId => $_getSZ(3);
  @$pb.TagNumber(4)
  set userId($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasUserId() => $_has(3);
  @$pb.TagNumber(4)
  void clearUserId() => $_clearField(4);

  /// Optional verified display metadata, never authentication or permission identity.
  @$pb.TagNumber(5)
  $core.String get verifiedEmail => $_getSZ(4);
  @$pb.TagNumber(5)
  set verifiedEmail($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasVerifiedEmail() => $_has(4);
  @$pb.TagNumber(5)
  void clearVerifiedEmail() => $_clearField(5);
}

class SecurityIssuer extends $pb.GeneratedMessage {
  factory SecurityIssuer({
    $core.String? issuer,
    $core.bool? enabled,
    $core.String? clientId,
    $core.String? apiAudience,
    $core.String? redirectUri,
    $core.Iterable<$core.String>? algorithms,
    $core.String? secretRef,
    $fixnum.Int64? configRevision,
    $core.bool? envOwned,
    $core.bool? deleted,
    $core.bool? hasSecretBinding,
    $core.bool? humanSubjectNamespaceQualified,
    $core.Iterable<SecurityNativeClient>? nativeClients,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    if (enabled != null) result.enabled = enabled;
    if (clientId != null) result.clientId = clientId;
    if (apiAudience != null) result.apiAudience = apiAudience;
    if (redirectUri != null) result.redirectUri = redirectUri;
    if (algorithms != null) result.algorithms.addAll(algorithms);
    if (secretRef != null) result.secretRef = secretRef;
    if (configRevision != null) result.configRevision = configRevision;
    if (envOwned != null) result.envOwned = envOwned;
    if (deleted != null) result.deleted = deleted;
    if (hasSecretBinding != null) result.hasSecretBinding = hasSecretBinding;
    if (humanSubjectNamespaceQualified != null)
      result.humanSubjectNamespaceQualified = humanSubjectNamespaceQualified;
    if (nativeClients != null) result.nativeClients.addAll(nativeClients);
    return result;
  }

  SecurityIssuer._();

  factory SecurityIssuer.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityIssuer.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityIssuer',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'issuer')
    ..aOB(2, _omitFieldNames ? '' : 'enabled')
    ..aOS(3, _omitFieldNames ? '' : 'clientId')
    ..aOS(4, _omitFieldNames ? '' : 'apiAudience')
    ..aOS(5, _omitFieldNames ? '' : 'redirectUri')
    ..pPS(6, _omitFieldNames ? '' : 'algorithms')
    ..aOS(7, _omitFieldNames ? '' : 'secretRef')
    ..a<$fixnum.Int64>(
        8, _omitFieldNames ? '' : 'configRevision', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOB(9, _omitFieldNames ? '' : 'envOwned')
    ..aOB(10, _omitFieldNames ? '' : 'deleted')
    ..aOB(11, _omitFieldNames ? '' : 'hasSecretBinding')
    ..aOB(12, _omitFieldNames ? '' : 'humanSubjectNamespaceQualified')
    ..pPM<SecurityNativeClient>(13, _omitFieldNames ? '' : 'nativeClients',
        subBuilder: SecurityNativeClient.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityIssuer clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityIssuer copyWith(void Function(SecurityIssuer) updates) =>
      super.copyWith((message) => updates(message as SecurityIssuer))
          as SecurityIssuer;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityIssuer create() => SecurityIssuer._();
  @$core.override
  SecurityIssuer createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityIssuer getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityIssuer>(create);
  static SecurityIssuer? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get issuer => $_getSZ(0);
  @$pb.TagNumber(1)
  set issuer($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearIssuer() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.bool get enabled => $_getBF(1);
  @$pb.TagNumber(2)
  set enabled($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasEnabled() => $_has(1);
  @$pb.TagNumber(2)
  void clearEnabled() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get clientId => $_getSZ(2);
  @$pb.TagNumber(3)
  set clientId($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasClientId() => $_has(2);
  @$pb.TagNumber(3)
  void clearClientId() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get apiAudience => $_getSZ(3);
  @$pb.TagNumber(4)
  set apiAudience($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasApiAudience() => $_has(3);
  @$pb.TagNumber(4)
  void clearApiAudience() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.String get redirectUri => $_getSZ(4);
  @$pb.TagNumber(5)
  set redirectUri($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasRedirectUri() => $_has(4);
  @$pb.TagNumber(5)
  void clearRedirectUri() => $_clearField(5);

  @$pb.TagNumber(6)
  $pb.PbList<$core.String> get algorithms => $_getList(5);

  /// Write-only handle to an operator binding; reads never return the handle.
  @$pb.TagNumber(7)
  $core.String get secretRef => $_getSZ(6);
  @$pb.TagNumber(7)
  set secretRef($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasSecretRef() => $_has(6);
  @$pb.TagNumber(7)
  void clearSecretRef() => $_clearField(7);

  @$pb.TagNumber(8)
  $fixnum.Int64 get configRevision => $_getI64(7);
  @$pb.TagNumber(8)
  set configRevision($fixnum.Int64 value) => $_setInt64(7, value);
  @$pb.TagNumber(8)
  $core.bool hasConfigRevision() => $_has(7);
  @$pb.TagNumber(8)
  void clearConfigRevision() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.bool get envOwned => $_getBF(8);
  @$pb.TagNumber(9)
  set envOwned($core.bool value) => $_setBool(8, value);
  @$pb.TagNumber(9)
  $core.bool hasEnvOwned() => $_has(8);
  @$pb.TagNumber(9)
  void clearEnvOwned() => $_clearField(9);

  @$pb.TagNumber(10)
  $core.bool get deleted => $_getBF(9);
  @$pb.TagNumber(10)
  set deleted($core.bool value) => $_setBool(9, value);
  @$pb.TagNumber(10)
  $core.bool hasDeleted() => $_has(9);
  @$pb.TagNumber(10)
  void clearDeleted() => $_clearField(10);

  @$pb.TagNumber(11)
  $core.bool get hasSecretBinding => $_getBF(10);
  @$pb.TagNumber(11)
  set hasSecretBinding($core.bool value) => $_setBool(10, value);
  @$pb.TagNumber(11)
  $core.bool hasHasSecretBinding() => $_has(10);
  @$pb.TagNumber(11)
  void clearHasSecretBinding() => $_clearField(11);

  /// Trusted issuance contract: OAuth client subjects cannot collide with or
  /// impersonate exact enrolled end-users. Qualify before enabling.
  @$pb.TagNumber(12)
  $core.bool get humanSubjectNamespaceQualified => $_getBF(11);
  @$pb.TagNumber(12)
  set humanSubjectNamespaceQualified($core.bool value) => $_setBool(11, value);
  @$pb.TagNumber(12)
  $core.bool hasHumanSubjectNamespaceQualified() => $_has(11);
  @$pb.TagNumber(12)
  void clearHumanSubjectNamespaceQualified() => $_clearField(12);

  /// Canonically ordered by id; native-only Issuers omit browser/API fields.
  @$pb.TagNumber(13)
  $pb.PbList<SecurityNativeClient> get nativeClients => $_getList(12);
}

class SecurityNativeClient extends $pb.GeneratedMessage {
  factory SecurityNativeClient({
    $core.String? id,
    NativeClientKind? kind,
    $core.String? clientId,
    $core.String? serviceOrigin,
    $core.Iterable<$core.String>? presenters,
    $core.String? redirectUri,
    $core.String? androidPackage,
    $core.String? secretRef,
    $core.bool? hasSecretBinding,
  }) {
    final result = create();
    if (id != null) result.id = id;
    if (kind != null) result.kind = kind;
    if (clientId != null) result.clientId = clientId;
    if (serviceOrigin != null) result.serviceOrigin = serviceOrigin;
    if (presenters != null) result.presenters.addAll(presenters);
    if (redirectUri != null) result.redirectUri = redirectUri;
    if (androidPackage != null) result.androidPackage = androidPackage;
    if (secretRef != null) result.secretRef = secretRef;
    if (hasSecretBinding != null) result.hasSecretBinding = hasSecretBinding;
    return result;
  }

  SecurityNativeClient._();

  factory SecurityNativeClient.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityNativeClient.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityNativeClient',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'id')
    ..aE<NativeClientKind>(2, _omitFieldNames ? '' : 'kind',
        enumValues: NativeClientKind.values)
    ..aOS(3, _omitFieldNames ? '' : 'clientId')
    ..aOS(4, _omitFieldNames ? '' : 'serviceOrigin')
    ..pPS(5, _omitFieldNames ? '' : 'presenters')
    ..aOS(6, _omitFieldNames ? '' : 'redirectUri')
    ..aOS(7, _omitFieldNames ? '' : 'androidPackage')
    ..aOS(8, _omitFieldNames ? '' : 'secretRef')
    ..aOB(9, _omitFieldNames ? '' : 'hasSecretBinding')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityNativeClient clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityNativeClient copyWith(void Function(SecurityNativeClient) updates) =>
      super.copyWith((message) => updates(message as SecurityNativeClient))
          as SecurityNativeClient;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityNativeClient create() => SecurityNativeClient._();
  @$core.override
  SecurityNativeClient createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityNativeClient getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityNativeClient>(create);
  static SecurityNativeClient? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get id => $_getSZ(0);
  @$pb.TagNumber(1)
  set id($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasId() => $_has(0);
  @$pb.TagNumber(1)
  void clearId() => $_clearField(1);

  @$pb.TagNumber(2)
  NativeClientKind get kind => $_getN(1);
  @$pb.TagNumber(2)
  set kind(NativeClientKind value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasKind() => $_has(1);
  @$pb.TagNumber(2)
  void clearKind() => $_clearField(2);

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
  $pb.PbList<$core.String> get presenters => $_getList(4);

  @$pb.TagNumber(6)
  $core.String get redirectUri => $_getSZ(5);
  @$pb.TagNumber(6)
  set redirectUri($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasRedirectUri() => $_has(5);
  @$pb.TagNumber(6)
  void clearRedirectUri() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.String get androidPackage => $_getSZ(6);
  @$pb.TagNumber(7)
  set androidPackage($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasAndroidPackage() => $_has(6);
  @$pb.TagNumber(7)
  void clearAndroidPackage() => $_clearField(7);

  /// Write-only operator binding handle. Omission preserves the exact existing
  /// profile's binding; a new Apple profile requires an explicit handle.
  @$pb.TagNumber(8)
  $core.String get secretRef => $_getSZ(7);
  @$pb.TagNumber(8)
  set secretRef($core.String value) => $_setString(7, value);
  @$pb.TagNumber(8)
  $core.bool hasSecretRef() => $_has(7);
  @$pb.TagNumber(8)
  void clearSecretRef() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.bool get hasSecretBinding => $_getBF(8);
  @$pb.TagNumber(9)
  set hasSecretBinding($core.bool value) => $_setBool(8, value);
  @$pb.TagNumber(9)
  $core.bool hasHasSecretBinding() => $_has(8);
  @$pb.TagNumber(9)
  void clearHasSecretBinding() => $_clearField(9);
}

class SecurityVersion extends $pb.GeneratedMessage {
  factory SecurityVersion({
    $fixnum.Int64? revision,
    $core.List<$core.int>? digest,
    $core.List<$core.int>? generation,
    CurrentAuthorityProfile? currentProfile,
    CurrentSemanticCut? currentCut,
    $core.List<$core.int>? admissionBinding,
  }) {
    final result = create();
    if (revision != null) result.revision = revision;
    if (digest != null) result.digest = digest;
    if (generation != null) result.generation = generation;
    if (currentProfile != null) result.currentProfile = currentProfile;
    if (currentCut != null) result.currentCut = currentCut;
    if (admissionBinding != null) result.admissionBinding = admissionBinding;
    return result;
  }

  SecurityVersion._();

  factory SecurityVersion.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityVersion.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityVersion',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'revision', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'digest', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'generation', $pb.PbFieldType.OY)
    ..aOM<CurrentAuthorityProfile>(4, _omitFieldNames ? '' : 'currentProfile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOM<CurrentSemanticCut>(5, _omitFieldNames ? '' : 'currentCut',
        subBuilder: CurrentSemanticCut.create)
    ..a<$core.List<$core.int>>(
        6, _omitFieldNames ? '' : 'admissionBinding', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityVersion clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityVersion copyWith(void Function(SecurityVersion) updates) =>
      super.copyWith((message) => updates(message as SecurityVersion))
          as SecurityVersion;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityVersion create() => SecurityVersion._();
  @$core.override
  SecurityVersion createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityVersion getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityVersion>(create);
  static SecurityVersion? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get revision => $_getI64(0);
  @$pb.TagNumber(1)
  set revision($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasRevision() => $_has(0);
  @$pb.TagNumber(1)
  void clearRevision() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get digest => $_getN(1);
  @$pb.TagNumber(2)
  set digest($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDigest() => $_has(1);
  @$pb.TagNumber(2)
  void clearDigest() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get generation => $_getN(2);
  @$pb.TagNumber(3)
  set generation($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasGeneration() => $_has(2);
  @$pb.TagNumber(3)
  void clearGeneration() => $_clearField(3);

  /// Current protocol uses these fields and leaves the legacy scalar fields
  /// above empty. Never compare cuts from different profiles by sequence.
  @$pb.TagNumber(4)
  CurrentAuthorityProfile get currentProfile => $_getN(3);
  @$pb.TagNumber(4)
  set currentProfile(CurrentAuthorityProfile value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasCurrentProfile() => $_has(3);
  @$pb.TagNumber(4)
  void clearCurrentProfile() => $_clearField(4);
  @$pb.TagNumber(4)
  CurrentAuthorityProfile ensureCurrentProfile() => $_ensure(3);

  @$pb.TagNumber(5)
  CurrentSemanticCut get currentCut => $_getN(4);
  @$pb.TagNumber(5)
  set currentCut(CurrentSemanticCut value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentCut() => $_has(4);
  @$pb.TagNumber(5)
  void clearCurrentCut() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSemanticCut ensureCurrentCut() => $_ensure(4);

  @$pb.TagNumber(6)
  $core.List<$core.int> get admissionBinding => $_getN(5);
  @$pb.TagNumber(6)
  set admissionBinding($core.List<$core.int> value) => $_setBytes(5, value);
  @$pb.TagNumber(6)
  $core.bool hasAdmissionBinding() => $_has(5);
  @$pb.TagNumber(6)
  void clearAdmissionBinding() => $_clearField(6);
}

class GetCurrentPrincipalRequest extends $pb.GeneratedMessage {
  factory GetCurrentPrincipalRequest() => create();

  GetCurrentPrincipalRequest._();

  factory GetCurrentPrincipalRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetCurrentPrincipalRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetCurrentPrincipalRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetCurrentPrincipalRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetCurrentPrincipalRequest copyWith(
          void Function(GetCurrentPrincipalRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetCurrentPrincipalRequest))
          as GetCurrentPrincipalRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetCurrentPrincipalRequest create() => GetCurrentPrincipalRequest._();
  @$core.override
  GetCurrentPrincipalRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetCurrentPrincipalRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetCurrentPrincipalRequest>(create);
  static GetCurrentPrincipalRequest? _defaultInstance;
}

class GetCurrentPrincipalResponse extends $pb.GeneratedMessage {
  factory GetCurrentPrincipalResponse({
    SecurityIdentity? identity,
    $core.Iterable<SecurityRole>? roles,
    SecurityVersion? version,
    $1.Timestamp? expiresAt,
    $core.bool? recentAuthentication,
    $core.String? csrfToken,
    $core.String? userId,
    $core.String? verifiedEmail,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (roles != null) result.roles.addAll(roles);
    if (version != null) result.version = version;
    if (expiresAt != null) result.expiresAt = expiresAt;
    if (recentAuthentication != null)
      result.recentAuthentication = recentAuthentication;
    if (csrfToken != null) result.csrfToken = csrfToken;
    if (userId != null) result.userId = userId;
    if (verifiedEmail != null) result.verifiedEmail = verifiedEmail;
    return result;
  }

  GetCurrentPrincipalResponse._();

  factory GetCurrentPrincipalResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetCurrentPrincipalResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetCurrentPrincipalResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIdentity>(1, _omitFieldNames ? '' : 'identity',
        subBuilder: SecurityIdentity.create)
    ..pPM<SecurityRole>(2, _omitFieldNames ? '' : 'roles',
        subBuilder: SecurityRole.create)
    ..aOM<SecurityVersion>(3, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOM<$1.Timestamp>(4, _omitFieldNames ? '' : 'expiresAt',
        subBuilder: $1.Timestamp.create)
    ..aOB(5, _omitFieldNames ? '' : 'recentAuthentication')
    ..aOS(6, _omitFieldNames ? '' : 'csrfToken')
    ..aOS(7, _omitFieldNames ? '' : 'userId')
    ..aOS(8, _omitFieldNames ? '' : 'verifiedEmail')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetCurrentPrincipalResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetCurrentPrincipalResponse copyWith(
          void Function(GetCurrentPrincipalResponse) updates) =>
      super.copyWith(
              (message) => updates(message as GetCurrentPrincipalResponse))
          as GetCurrentPrincipalResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetCurrentPrincipalResponse create() =>
      GetCurrentPrincipalResponse._();
  @$core.override
  GetCurrentPrincipalResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetCurrentPrincipalResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetCurrentPrincipalResponse>(create);
  static GetCurrentPrincipalResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIdentity get identity => $_getN(0);
  @$pb.TagNumber(1)
  set identity(SecurityIdentity value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentity() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentity() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIdentity ensureIdentity() => $_ensure(0);

  @$pb.TagNumber(2)
  $pb.PbList<SecurityRole> get roles => $_getList(1);

  @$pb.TagNumber(3)
  SecurityVersion get version => $_getN(2);
  @$pb.TagNumber(3)
  set version(SecurityVersion value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasVersion() => $_has(2);
  @$pb.TagNumber(3)
  void clearVersion() => $_clearField(3);
  @$pb.TagNumber(3)
  SecurityVersion ensureVersion() => $_ensure(2);

  @$pb.TagNumber(4)
  $1.Timestamp get expiresAt => $_getN(3);
  @$pb.TagNumber(4)
  set expiresAt($1.Timestamp value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasExpiresAt() => $_has(3);
  @$pb.TagNumber(4)
  void clearExpiresAt() => $_clearField(4);
  @$pb.TagNumber(4)
  $1.Timestamp ensureExpiresAt() => $_ensure(3);

  @$pb.TagNumber(5)
  $core.bool get recentAuthentication => $_getBF(4);
  @$pb.TagNumber(5)
  set recentAuthentication($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasRecentAuthentication() => $_has(4);
  @$pb.TagNumber(5)
  void clearRecentAuthentication() => $_clearField(5);

  /// Opaque browser CSRF proof, returned only on the browser session surface.
  @$pb.TagNumber(6)
  $core.String get csrfToken => $_getSZ(5);
  @$pb.TagNumber(6)
  set csrfToken($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasCsrfToken() => $_has(5);
  @$pb.TagNumber(6)
  void clearCsrfToken() => $_clearField(6);

  /// Immutable Server-installed user ID; absent for UID-less or machine Principals.
  /// This self metadata requires no additional Role and never grants data access.
  @$pb.TagNumber(7)
  $core.String get userId => $_getSZ(6);
  @$pb.TagNumber(7)
  set userId($core.String value) => $_setString(6, value);
  @$pb.TagNumber(7)
  $core.bool hasUserId() => $_has(6);
  @$pb.TagNumber(7)
  void clearUserId() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.String get verifiedEmail => $_getSZ(7);
  @$pb.TagNumber(8)
  set verifiedEmail($core.String value) => $_setString(7, value);
  @$pb.TagNumber(8)
  $core.bool hasVerifiedEmail() => $_has(7);
  @$pb.TagNumber(8)
  void clearVerifiedEmail() => $_clearField(8);
}

/// Same-origin /auth/session and /auth/logout HTTP responses. OIDC tokens never
/// cross this boundary; principal is absent only when mode is explicitly OFF.
class BrowserSession extends $pb.GeneratedMessage {
  factory BrowserSession({
    AuthMode? mode,
    GetCurrentPrincipalResponse? principal,
    CurrentAuthorityProfile? currentProfile,
  }) {
    final result = create();
    if (mode != null) result.mode = mode;
    if (principal != null) result.principal = principal;
    if (currentProfile != null) result.currentProfile = currentProfile;
    return result;
  }

  BrowserSession._();

  factory BrowserSession.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BrowserSession.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BrowserSession',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aE<AuthMode>(1, _omitFieldNames ? '' : 'mode',
        enumValues: AuthMode.values)
    ..aOM<GetCurrentPrincipalResponse>(2, _omitFieldNames ? '' : 'principal',
        subBuilder: GetCurrentPrincipalResponse.create)
    ..aOM<CurrentAuthorityProfile>(3, _omitFieldNames ? '' : 'currentProfile',
        subBuilder: CurrentAuthorityProfile.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BrowserSession clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BrowserSession copyWith(void Function(BrowserSession) updates) =>
      super.copyWith((message) => updates(message as BrowserSession))
          as BrowserSession;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BrowserSession create() => BrowserSession._();
  @$core.override
  BrowserSession createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BrowserSession getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BrowserSession>(create);
  static BrowserSession? _defaultInstance;

  @$pb.TagNumber(1)
  AuthMode get mode => $_getN(0);
  @$pb.TagNumber(1)
  set mode(AuthMode value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasMode() => $_has(0);
  @$pb.TagNumber(1)
  void clearMode() => $_clearField(1);

  @$pb.TagNumber(2)
  GetCurrentPrincipalResponse get principal => $_getN(1);
  @$pb.TagNumber(2)
  set principal(GetCurrentPrincipalResponse value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasPrincipal() => $_has(1);
  @$pb.TagNumber(2)
  void clearPrincipal() => $_clearField(2);
  @$pb.TagNumber(2)
  GetCurrentPrincipalResponse ensurePrincipal() => $_ensure(1);

  @$pb.TagNumber(3)
  CurrentAuthorityProfile get currentProfile => $_getN(2);
  @$pb.TagNumber(3)
  set currentProfile(CurrentAuthorityProfile value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasCurrentProfile() => $_has(2);
  @$pb.TagNumber(3)
  void clearCurrentProfile() => $_clearField(3);
  @$pb.TagNumber(3)
  CurrentAuthorityProfile ensureCurrentProfile() => $_ensure(2);
}

class SessionRevocation extends $pb.GeneratedMessage {
  factory SessionRevocation({
    SecurityVersion? version,
    SecurityEnforcementState? enforcement,
    CurrentSecurityChangeResult? currentResult,
    $core.bool? localCookieCleared,
    CurrentSessionRevocationReview? currentReview,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (enforcement != null) result.enforcement = enforcement;
    if (currentResult != null) result.currentResult = currentResult;
    if (localCookieCleared != null)
      result.localCookieCleared = localCookieCleared;
    if (currentReview != null) result.currentReview = currentReview;
    return result;
  }

  SessionRevocation._();

  factory SessionRevocation.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SessionRevocation.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SessionRevocation',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityVersion>(1, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aE<SecurityEnforcementState>(2, _omitFieldNames ? '' : 'enforcement',
        enumValues: SecurityEnforcementState.values)
    ..aOM<CurrentSecurityChangeResult>(
        3, _omitFieldNames ? '' : 'currentResult',
        subBuilder: CurrentSecurityChangeResult.create)
    ..aOB(4, _omitFieldNames ? '' : 'localCookieCleared')
    ..aOM<CurrentSessionRevocationReview>(
        5, _omitFieldNames ? '' : 'currentReview',
        subBuilder: CurrentSessionRevocationReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SessionRevocation clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SessionRevocation copyWith(void Function(SessionRevocation) updates) =>
      super.copyWith((message) => updates(message as SessionRevocation))
          as SessionRevocation;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SessionRevocation create() => SessionRevocation._();
  @$core.override
  SessionRevocation createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SessionRevocation getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SessionRevocation>(create);
  static SessionRevocation? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityVersion get version => $_getN(0);
  @$pb.TagNumber(1)
  set version(SecurityVersion value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityVersion ensureVersion() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityEnforcementState get enforcement => $_getN(1);
  @$pb.TagNumber(2)
  set enforcement(SecurityEnforcementState value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasEnforcement() => $_has(1);
  @$pb.TagNumber(2)
  void clearEnforcement() => $_clearField(2);

  @$pb.TagNumber(3)
  CurrentSecurityChangeResult get currentResult => $_getN(2);
  @$pb.TagNumber(3)
  set currentResult(CurrentSecurityChangeResult value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasCurrentResult() => $_has(2);
  @$pb.TagNumber(3)
  void clearCurrentResult() => $_clearField(3);
  @$pb.TagNumber(3)
  CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(2);

  /// Local cookie removal is independent of durable cluster completion.
  @$pb.TagNumber(4)
  $core.bool get localCookieCleared => $_getBF(3);
  @$pb.TagNumber(4)
  set localCookieCleared($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasLocalCookieCleared() => $_has(3);
  @$pb.TagNumber(4)
  void clearLocalCookieCleared() => $_clearField(4);

  /// First, retain this server-minted review before dispatching logout Apply.
  @$pb.TagNumber(5)
  CurrentSessionRevocationReview get currentReview => $_getN(4);
  @$pb.TagNumber(5)
  set currentReview(CurrentSessionRevocationReview value) =>
      $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentReview() => $_has(4);
  @$pb.TagNumber(5)
  void clearCurrentReview() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSessionRevocationReview ensureCurrentReview() => $_ensure(4);
}

class CurrentSessionRevocationReview extends $pb.GeneratedMessage {
  factory CurrentSessionRevocationReview({
    CurrentAuthorityProfile? profile,
    CurrentSemanticCut? expectedCut,
    CurrentSecurityChangeID? changeId,
    SecurityIdentity? actor,
    $core.List<$core.int>? intentDigest,
    $core.String? sessionDigest,
    $fixnum.Int64? sessionLineage,
  }) {
    final result = create();
    if (profile != null) result.profile = profile;
    if (expectedCut != null) result.expectedCut = expectedCut;
    if (changeId != null) result.changeId = changeId;
    if (actor != null) result.actor = actor;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (sessionDigest != null) result.sessionDigest = sessionDigest;
    if (sessionLineage != null) result.sessionLineage = sessionLineage;
    return result;
  }

  CurrentSessionRevocationReview._();

  factory CurrentSessionRevocationReview.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSessionRevocationReview.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSessionRevocationReview',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentAuthorityProfile>(1, _omitFieldNames ? '' : 'profile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOM<CurrentSemanticCut>(2, _omitFieldNames ? '' : 'expectedCut',
        subBuilder: CurrentSemanticCut.create)
    ..aOM<CurrentSecurityChangeID>(3, _omitFieldNames ? '' : 'changeId',
        subBuilder: CurrentSecurityChangeID.create)
    ..aOM<SecurityIdentity>(4, _omitFieldNames ? '' : 'actor',
        subBuilder: SecurityIdentity.create)
    ..a<$core.List<$core.int>>(
        5, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..aOS(6, _omitFieldNames ? '' : 'sessionDigest')
    ..a<$fixnum.Int64>(
        7, _omitFieldNames ? '' : 'sessionLineage', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSessionRevocationReview clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSessionRevocationReview copyWith(
          void Function(CurrentSessionRevocationReview) updates) =>
      super.copyWith(
              (message) => updates(message as CurrentSessionRevocationReview))
          as CurrentSessionRevocationReview;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSessionRevocationReview create() =>
      CurrentSessionRevocationReview._();
  @$core.override
  CurrentSessionRevocationReview createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSessionRevocationReview getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSessionRevocationReview>(create);
  static CurrentSessionRevocationReview? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentAuthorityProfile get profile => $_getN(0);
  @$pb.TagNumber(1)
  set profile(CurrentAuthorityProfile value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasProfile() => $_has(0);
  @$pb.TagNumber(1)
  void clearProfile() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentAuthorityProfile ensureProfile() => $_ensure(0);

  @$pb.TagNumber(2)
  CurrentSemanticCut get expectedCut => $_getN(1);
  @$pb.TagNumber(2)
  set expectedCut(CurrentSemanticCut value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasExpectedCut() => $_has(1);
  @$pb.TagNumber(2)
  void clearExpectedCut() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentSemanticCut ensureExpectedCut() => $_ensure(1);

  @$pb.TagNumber(3)
  CurrentSecurityChangeID get changeId => $_getN(2);
  @$pb.TagNumber(3)
  set changeId(CurrentSecurityChangeID value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasChangeId() => $_has(2);
  @$pb.TagNumber(3)
  void clearChangeId() => $_clearField(3);
  @$pb.TagNumber(3)
  CurrentSecurityChangeID ensureChangeId() => $_ensure(2);

  @$pb.TagNumber(4)
  SecurityIdentity get actor => $_getN(3);
  @$pb.TagNumber(4)
  set actor(SecurityIdentity value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasActor() => $_has(3);
  @$pb.TagNumber(4)
  void clearActor() => $_clearField(4);
  @$pb.TagNumber(4)
  SecurityIdentity ensureActor() => $_ensure(3);

  @$pb.TagNumber(5)
  $core.List<$core.int> get intentDigest => $_getN(4);
  @$pb.TagNumber(5)
  set intentDigest($core.List<$core.int> value) => $_setBytes(4, value);
  @$pb.TagNumber(5)
  $core.bool hasIntentDigest() => $_has(4);
  @$pb.TagNumber(5)
  void clearIntentDigest() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get sessionDigest => $_getSZ(5);
  @$pb.TagNumber(6)
  set sessionDigest($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasSessionDigest() => $_has(5);
  @$pb.TagNumber(6)
  void clearSessionDigest() => $_clearField(6);

  @$pb.TagNumber(7)
  $fixnum.Int64 get sessionLineage => $_getI64(6);
  @$pb.TagNumber(7)
  set sessionLineage($fixnum.Int64 value) => $_setInt64(6, value);
  @$pb.TagNumber(7)
  $core.bool hasSessionLineage() => $_has(6);
  @$pb.TagNumber(7)
  void clearSessionLineage() => $_clearField(7);
}

class CurrentLogoutRequest extends $pb.GeneratedMessage {
  factory CurrentLogoutRequest({
    CurrentAuthorityProfile? profile,
    $core.bool? prepareOnly,
    CurrentSessionRevocationReview? review,
    $core.bool? localOnly,
  }) {
    final result = create();
    if (profile != null) result.profile = profile;
    if (prepareOnly != null) result.prepareOnly = prepareOnly;
    if (review != null) result.review = review;
    if (localOnly != null) result.localOnly = localOnly;
    return result;
  }

  CurrentLogoutRequest._();

  factory CurrentLogoutRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentLogoutRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentLogoutRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentAuthorityProfile>(1, _omitFieldNames ? '' : 'profile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOB(2, _omitFieldNames ? '' : 'prepareOnly')
    ..aOM<CurrentSessionRevocationReview>(3, _omitFieldNames ? '' : 'review',
        subBuilder: CurrentSessionRevocationReview.create)
    ..aOB(4, _omitFieldNames ? '' : 'localOnly')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentLogoutRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentLogoutRequest copyWith(void Function(CurrentLogoutRequest) updates) =>
      super.copyWith((message) => updates(message as CurrentLogoutRequest))
          as CurrentLogoutRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentLogoutRequest create() => CurrentLogoutRequest._();
  @$core.override
  CurrentLogoutRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentLogoutRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentLogoutRequest>(create);
  static CurrentLogoutRequest? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentAuthorityProfile get profile => $_getN(0);
  @$pb.TagNumber(1)
  set profile(CurrentAuthorityProfile value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasProfile() => $_has(0);
  @$pb.TagNumber(1)
  void clearProfile() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentAuthorityProfile ensureProfile() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.bool get prepareOnly => $_getBF(1);
  @$pb.TagNumber(2)
  set prepareOnly($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasPrepareOnly() => $_has(1);
  @$pb.TagNumber(2)
  void clearPrepareOnly() => $_clearField(2);

  @$pb.TagNumber(3)
  CurrentSessionRevocationReview get review => $_getN(2);
  @$pb.TagNumber(3)
  set review(CurrentSessionRevocationReview value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasReview() => $_has(2);
  @$pb.TagNumber(3)
  void clearReview() => $_clearField(3);
  @$pb.TagNumber(3)
  CurrentSessionRevocationReview ensureReview() => $_ensure(2);

  /// Same-origin local cookie deletion makes no cluster completion claim.
  @$pb.TagNumber(4)
  $core.bool get localOnly => $_getBF(3);
  @$pb.TagNumber(4)
  set localOnly($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasLocalOnly() => $_has(3);
  @$pb.TagNumber(4)
  void clearLocalOnly() => $_clearField(4);
}

class ListIssuersRequest extends $pb.GeneratedMessage {
  factory ListIssuersRequest({
    $core.int? limit,
    $core.String? cursor,
    $core.String? exact,
  }) {
    final result = create();
    if (limit != null) result.limit = limit;
    if (cursor != null) result.cursor = cursor;
    if (exact != null) result.exact = exact;
    return result;
  }

  ListIssuersRequest._();

  factory ListIssuersRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListIssuersRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListIssuersRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListIssuersRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListIssuersRequest copyWith(void Function(ListIssuersRequest) updates) =>
      super.copyWith((message) => updates(message as ListIssuersRequest))
          as ListIssuersRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListIssuersRequest create() => ListIssuersRequest._();
  @$core.override
  ListIssuersRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListIssuersRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListIssuersRequest>(create);
  static ListIssuersRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get limit => $_getIZ(0);
  @$pb.TagNumber(1)
  set limit($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasLimit() => $_has(0);
  @$pb.TagNumber(1)
  void clearLimit() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get cursor => $_getSZ(1);
  @$pb.TagNumber(2)
  set cursor($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearCursor() => $_clearField(2);

  /// Optional exact URL, Role ID or subject selector within the selected list.
  @$pb.TagNumber(3)
  $core.String get exact => $_getSZ(2);
  @$pb.TagNumber(3)
  set exact($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasExact() => $_has(2);
  @$pb.TagNumber(3)
  void clearExact() => $_clearField(3);
}

class ListRolesRequest extends $pb.GeneratedMessage {
  factory ListRolesRequest({
    $core.int? limit,
    $core.String? cursor,
    $core.String? exact,
  }) {
    final result = create();
    if (limit != null) result.limit = limit;
    if (cursor != null) result.cursor = cursor;
    if (exact != null) result.exact = exact;
    return result;
  }

  ListRolesRequest._();

  factory ListRolesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListRolesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListRolesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRolesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRolesRequest copyWith(void Function(ListRolesRequest) updates) =>
      super.copyWith((message) => updates(message as ListRolesRequest))
          as ListRolesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListRolesRequest create() => ListRolesRequest._();
  @$core.override
  ListRolesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListRolesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListRolesRequest>(create);
  static ListRolesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get limit => $_getIZ(0);
  @$pb.TagNumber(1)
  set limit($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasLimit() => $_has(0);
  @$pb.TagNumber(1)
  void clearLimit() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get cursor => $_getSZ(1);
  @$pb.TagNumber(2)
  set cursor($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearCursor() => $_clearField(2);

  /// Optional exact URL, Role ID or subject selector within the selected list.
  @$pb.TagNumber(3)
  $core.String get exact => $_getSZ(2);
  @$pb.TagNumber(3)
  set exact($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasExact() => $_has(2);
  @$pb.TagNumber(3)
  void clearExact() => $_clearField(3);
}

class ListUsersRequest extends $pb.GeneratedMessage {
  factory ListUsersRequest({
    $core.int? limit,
    $core.String? cursor,
    $core.String? exact,
  }) {
    final result = create();
    if (limit != null) result.limit = limit;
    if (cursor != null) result.cursor = cursor;
    if (exact != null) result.exact = exact;
    return result;
  }

  ListUsersRequest._();

  factory ListUsersRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListUsersRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListUsersRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListUsersRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListUsersRequest copyWith(void Function(ListUsersRequest) updates) =>
      super.copyWith((message) => updates(message as ListUsersRequest))
          as ListUsersRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListUsersRequest create() => ListUsersRequest._();
  @$core.override
  ListUsersRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListUsersRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListUsersRequest>(create);
  static ListUsersRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get limit => $_getIZ(0);
  @$pb.TagNumber(1)
  set limit($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasLimit() => $_has(0);
  @$pb.TagNumber(1)
  void clearLimit() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get cursor => $_getSZ(1);
  @$pb.TagNumber(2)
  set cursor($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearCursor() => $_clearField(2);

  /// Optional exact URL, Role ID or subject selector within the selected list.
  @$pb.TagNumber(3)
  $core.String get exact => $_getSZ(2);
  @$pb.TagNumber(3)
  set exact($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasExact() => $_has(2);
  @$pb.TagNumber(3)
  void clearExact() => $_clearField(3);
}

class ListSecurityAuditRequest extends $pb.GeneratedMessage {
  factory ListSecurityAuditRequest({
    $core.int? limit,
    $core.String? cursor,
    $core.String? exact,
  }) {
    final result = create();
    if (limit != null) result.limit = limit;
    if (cursor != null) result.cursor = cursor;
    if (exact != null) result.exact = exact;
    return result;
  }

  ListSecurityAuditRequest._();

  factory ListSecurityAuditRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListSecurityAuditRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListSecurityAuditRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSecurityAuditRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSecurityAuditRequest copyWith(
          void Function(ListSecurityAuditRequest) updates) =>
      super.copyWith((message) => updates(message as ListSecurityAuditRequest))
          as ListSecurityAuditRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListSecurityAuditRequest create() => ListSecurityAuditRequest._();
  @$core.override
  ListSecurityAuditRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListSecurityAuditRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListSecurityAuditRequest>(create);
  static ListSecurityAuditRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get limit => $_getIZ(0);
  @$pb.TagNumber(1)
  set limit($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasLimit() => $_has(0);
  @$pb.TagNumber(1)
  void clearLimit() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get cursor => $_getSZ(1);
  @$pb.TagNumber(2)
  set cursor($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearCursor() => $_clearField(2);

  /// Optional exact URL, Role ID or subject selector within the selected list.
  @$pb.TagNumber(3)
  $core.String get exact => $_getSZ(2);
  @$pb.TagNumber(3)
  set exact($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasExact() => $_has(2);
  @$pb.TagNumber(3)
  void clearExact() => $_clearField(3);
}

class GetIssuerRequest extends $pb.GeneratedMessage {
  factory GetIssuerRequest({
    $core.String? issuer,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    return result;
  }

  GetIssuerRequest._();

  factory GetIssuerRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetIssuerRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetIssuerRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'issuer')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetIssuerRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetIssuerRequest copyWith(void Function(GetIssuerRequest) updates) =>
      super.copyWith((message) => updates(message as GetIssuerRequest))
          as GetIssuerRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetIssuerRequest create() => GetIssuerRequest._();
  @$core.override
  GetIssuerRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetIssuerRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetIssuerRequest>(create);
  static GetIssuerRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get issuer => $_getSZ(0);
  @$pb.TagNumber(1)
  set issuer($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearIssuer() => $_clearField(1);
}

class GetIssuerResponse extends $pb.GeneratedMessage {
  factory GetIssuerResponse({
    SecurityIssuer? issuer,
    SecurityVersion? version,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    if (version != null) result.version = version;
    return result;
  }

  GetIssuerResponse._();

  factory GetIssuerResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetIssuerResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetIssuerResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIssuer>(1, _omitFieldNames ? '' : 'issuer',
        subBuilder: SecurityIssuer.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetIssuerResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetIssuerResponse copyWith(void Function(GetIssuerResponse) updates) =>
      super.copyWith((message) => updates(message as GetIssuerResponse))
          as GetIssuerResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetIssuerResponse create() => GetIssuerResponse._();
  @$core.override
  GetIssuerResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetIssuerResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetIssuerResponse>(create);
  static GetIssuerResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIssuer get issuer => $_getN(0);
  @$pb.TagNumber(1)
  set issuer(SecurityIssuer value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearIssuer() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIssuer ensureIssuer() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);
}

class GetRoleRequest extends $pb.GeneratedMessage {
  factory GetRoleRequest({
    $core.String? id,
  }) {
    final result = create();
    if (id != null) result.id = id;
    return result;
  }

  GetRoleRequest._();

  factory GetRoleRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetRoleRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetRoleRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'id')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleRequest copyWith(void Function(GetRoleRequest) updates) =>
      super.copyWith((message) => updates(message as GetRoleRequest))
          as GetRoleRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetRoleRequest create() => GetRoleRequest._();
  @$core.override
  GetRoleRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetRoleRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetRoleRequest>(create);
  static GetRoleRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get id => $_getSZ(0);
  @$pb.TagNumber(1)
  set id($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasId() => $_has(0);
  @$pb.TagNumber(1)
  void clearId() => $_clearField(1);
}

class GetRoleResponse extends $pb.GeneratedMessage {
  factory GetRoleResponse({
    SecurityRole? role,
    SecurityVersion? version,
  }) {
    final result = create();
    if (role != null) result.role = role;
    if (version != null) result.version = version;
    return result;
  }

  GetRoleResponse._();

  factory GetRoleResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetRoleResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetRoleResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityRole>(1, _omitFieldNames ? '' : 'role',
        subBuilder: SecurityRole.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleResponse copyWith(void Function(GetRoleResponse) updates) =>
      super.copyWith((message) => updates(message as GetRoleResponse))
          as GetRoleResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetRoleResponse create() => GetRoleResponse._();
  @$core.override
  GetRoleResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetRoleResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetRoleResponse>(create);
  static GetRoleResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityRole get role => $_getN(0);
  @$pb.TagNumber(1)
  set role(SecurityRole value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasRole() => $_has(0);
  @$pb.TagNumber(1)
  void clearRole() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityRole ensureRole() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);
}

class GetUserRequest extends $pb.GeneratedMessage {
  factory GetUserRequest({
    SecurityIdentity? identity,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    return result;
  }

  GetUserRequest._();

  factory GetUserRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetUserRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetUserRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIdentity>(1, _omitFieldNames ? '' : 'identity',
        subBuilder: SecurityIdentity.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetUserRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetUserRequest copyWith(void Function(GetUserRequest) updates) =>
      super.copyWith((message) => updates(message as GetUserRequest))
          as GetUserRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetUserRequest create() => GetUserRequest._();
  @$core.override
  GetUserRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetUserRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetUserRequest>(create);
  static GetUserRequest? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIdentity get identity => $_getN(0);
  @$pb.TagNumber(1)
  set identity(SecurityIdentity value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentity() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentity() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIdentity ensureIdentity() => $_ensure(0);
}

class GetUserResponse extends $pb.GeneratedMessage {
  factory GetUserResponse({
    SecurityUser? user,
    SecurityVersion? version,
  }) {
    final result = create();
    if (user != null) result.user = user;
    if (version != null) result.version = version;
    return result;
  }

  GetUserResponse._();

  factory GetUserResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetUserResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetUserResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityUser>(1, _omitFieldNames ? '' : 'user',
        subBuilder: SecurityUser.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetUserResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetUserResponse copyWith(void Function(GetUserResponse) updates) =>
      super.copyWith((message) => updates(message as GetUserResponse))
          as GetUserResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetUserResponse create() => GetUserResponse._();
  @$core.override
  GetUserResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetUserResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetUserResponse>(create);
  static GetUserResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityUser get user => $_getN(0);
  @$pb.TagNumber(1)
  set user(SecurityUser value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasUser() => $_has(0);
  @$pb.TagNumber(1)
  void clearUser() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityUser ensureUser() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);
}

class ListIssuersResponse extends $pb.GeneratedMessage {
  factory ListIssuersResponse({
    $core.Iterable<SecurityIssuer>? issuers,
    SecurityVersion? version,
    $core.String? nextCursor,
  }) {
    final result = create();
    if (issuers != null) result.issuers.addAll(issuers);
    if (version != null) result.version = version;
    if (nextCursor != null) result.nextCursor = nextCursor;
    return result;
  }

  ListIssuersResponse._();

  factory ListIssuersResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListIssuersResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListIssuersResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<SecurityIssuer>(1, _omitFieldNames ? '' : 'issuers',
        subBuilder: SecurityIssuer.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListIssuersResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListIssuersResponse copyWith(void Function(ListIssuersResponse) updates) =>
      super.copyWith((message) => updates(message as ListIssuersResponse))
          as ListIssuersResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListIssuersResponse create() => ListIssuersResponse._();
  @$core.override
  ListIssuersResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListIssuersResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListIssuersResponse>(create);
  static ListIssuersResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<SecurityIssuer> get issuers => $_getList(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.String get nextCursor => $_getSZ(2);
  @$pb.TagNumber(3)
  set nextCursor($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasNextCursor() => $_has(2);
  @$pb.TagNumber(3)
  void clearNextCursor() => $_clearField(3);
}

class ListRolesResponse extends $pb.GeneratedMessage {
  factory ListRolesResponse({
    $core.Iterable<SecurityRole>? roles,
    SecurityVersion? version,
    $core.String? nextCursor,
  }) {
    final result = create();
    if (roles != null) result.roles.addAll(roles);
    if (version != null) result.version = version;
    if (nextCursor != null) result.nextCursor = nextCursor;
    return result;
  }

  ListRolesResponse._();

  factory ListRolesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListRolesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListRolesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<SecurityRole>(1, _omitFieldNames ? '' : 'roles',
        subBuilder: SecurityRole.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRolesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRolesResponse copyWith(void Function(ListRolesResponse) updates) =>
      super.copyWith((message) => updates(message as ListRolesResponse))
          as ListRolesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListRolesResponse create() => ListRolesResponse._();
  @$core.override
  ListRolesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListRolesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListRolesResponse>(create);
  static ListRolesResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<SecurityRole> get roles => $_getList(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.String get nextCursor => $_getSZ(2);
  @$pb.TagNumber(3)
  set nextCursor($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasNextCursor() => $_has(2);
  @$pb.TagNumber(3)
  void clearNextCursor() => $_clearField(3);
}

class ListUsersResponse extends $pb.GeneratedMessage {
  factory ListUsersResponse({
    $core.Iterable<SecurityUser>? users,
    SecurityVersion? version,
    $core.String? nextCursor,
  }) {
    final result = create();
    if (users != null) result.users.addAll(users);
    if (version != null) result.version = version;
    if (nextCursor != null) result.nextCursor = nextCursor;
    return result;
  }

  ListUsersResponse._();

  factory ListUsersResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListUsersResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListUsersResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<SecurityUser>(1, _omitFieldNames ? '' : 'users',
        subBuilder: SecurityUser.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListUsersResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListUsersResponse copyWith(void Function(ListUsersResponse) updates) =>
      super.copyWith((message) => updates(message as ListUsersResponse))
          as ListUsersResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListUsersResponse create() => ListUsersResponse._();
  @$core.override
  ListUsersResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListUsersResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListUsersResponse>(create);
  static ListUsersResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<SecurityUser> get users => $_getList(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.String get nextCursor => $_getSZ(2);
  @$pb.TagNumber(3)
  set nextCursor($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasNextCursor() => $_has(2);
  @$pb.TagNumber(3)
  void clearNextCursor() => $_clearField(3);
}

class ListRoleAssignmentsRequest extends $pb.GeneratedMessage {
  factory ListRoleAssignmentsRequest({
    SecurityIdentity? identity,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    return result;
  }

  ListRoleAssignmentsRequest._();

  factory ListRoleAssignmentsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListRoleAssignmentsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListRoleAssignmentsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIdentity>(1, _omitFieldNames ? '' : 'identity',
        subBuilder: SecurityIdentity.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRoleAssignmentsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRoleAssignmentsRequest copyWith(
          void Function(ListRoleAssignmentsRequest) updates) =>
      super.copyWith(
              (message) => updates(message as ListRoleAssignmentsRequest))
          as ListRoleAssignmentsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListRoleAssignmentsRequest create() => ListRoleAssignmentsRequest._();
  @$core.override
  ListRoleAssignmentsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListRoleAssignmentsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListRoleAssignmentsRequest>(create);
  static ListRoleAssignmentsRequest? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIdentity get identity => $_getN(0);
  @$pb.TagNumber(1)
  set identity(SecurityIdentity value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentity() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentity() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIdentity ensureIdentity() => $_ensure(0);
}

class ListRoleAssignmentsResponse extends $pb.GeneratedMessage {
  factory ListRoleAssignmentsResponse({
    $core.Iterable<SecurityRoleAssignment>? assignments,
    SecurityVersion? version,
  }) {
    final result = create();
    if (assignments != null) result.assignments.addAll(assignments);
    if (version != null) result.version = version;
    return result;
  }

  ListRoleAssignmentsResponse._();

  factory ListRoleAssignmentsResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListRoleAssignmentsResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListRoleAssignmentsResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<SecurityRoleAssignment>(1, _omitFieldNames ? '' : 'assignments',
        subBuilder: SecurityRoleAssignment.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRoleAssignmentsResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRoleAssignmentsResponse copyWith(
          void Function(ListRoleAssignmentsResponse) updates) =>
      super.copyWith(
              (message) => updates(message as ListRoleAssignmentsResponse))
          as ListRoleAssignmentsResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListRoleAssignmentsResponse create() =>
      ListRoleAssignmentsResponse._();
  @$core.override
  ListRoleAssignmentsResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListRoleAssignmentsResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListRoleAssignmentsResponse>(create);
  static ListRoleAssignmentsResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<SecurityRoleAssignment> get assignments => $_getList(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);
}

class SecurityAuditRecord extends $pb.GeneratedMessage {
  factory SecurityAuditRecord({
    $fixnum.Int64? revision,
    $core.String? changeId,
    $core.String? intentDigest,
    $core.String? actorDigest,
    $1.Timestamp? occurredAt,
    $core.String? operation,
    $core.Iterable<$core.String>? targetDigests,
    $core.int? additionalTargets,
    $core.String? outcome,
  }) {
    final result = create();
    if (revision != null) result.revision = revision;
    if (changeId != null) result.changeId = changeId;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (actorDigest != null) result.actorDigest = actorDigest;
    if (occurredAt != null) result.occurredAt = occurredAt;
    if (operation != null) result.operation = operation;
    if (targetDigests != null) result.targetDigests.addAll(targetDigests);
    if (additionalTargets != null) result.additionalTargets = additionalTargets;
    if (outcome != null) result.outcome = outcome;
    return result;
  }

  SecurityAuditRecord._();

  factory SecurityAuditRecord.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityAuditRecord.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityAuditRecord',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'revision', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOS(2, _omitFieldNames ? '' : 'changeId')
    ..aOS(3, _omitFieldNames ? '' : 'intentDigest')
    ..aOS(4, _omitFieldNames ? '' : 'actorDigest')
    ..aOM<$1.Timestamp>(5, _omitFieldNames ? '' : 'occurredAt',
        subBuilder: $1.Timestamp.create)
    ..aOS(6, _omitFieldNames ? '' : 'operation')
    ..pPS(7, _omitFieldNames ? '' : 'targetDigests')
    ..aI(8, _omitFieldNames ? '' : 'additionalTargets',
        fieldType: $pb.PbFieldType.OU3)
    ..aOS(9, _omitFieldNames ? '' : 'outcome')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityAuditRecord clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityAuditRecord copyWith(void Function(SecurityAuditRecord) updates) =>
      super.copyWith((message) => updates(message as SecurityAuditRecord))
          as SecurityAuditRecord;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityAuditRecord create() => SecurityAuditRecord._();
  @$core.override
  SecurityAuditRecord createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityAuditRecord getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityAuditRecord>(create);
  static SecurityAuditRecord? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get revision => $_getI64(0);
  @$pb.TagNumber(1)
  set revision($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasRevision() => $_has(0);
  @$pb.TagNumber(1)
  void clearRevision() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get changeId => $_getSZ(1);
  @$pb.TagNumber(2)
  set changeId($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get intentDigest => $_getSZ(2);
  @$pb.TagNumber(3)
  set intentDigest($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasIntentDigest() => $_has(2);
  @$pb.TagNumber(3)
  void clearIntentDigest() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get actorDigest => $_getSZ(3);
  @$pb.TagNumber(4)
  set actorDigest($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasActorDigest() => $_has(3);
  @$pb.TagNumber(4)
  void clearActorDigest() => $_clearField(4);

  @$pb.TagNumber(5)
  $1.Timestamp get occurredAt => $_getN(4);
  @$pb.TagNumber(5)
  set occurredAt($1.Timestamp value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasOccurredAt() => $_has(4);
  @$pb.TagNumber(5)
  void clearOccurredAt() => $_clearField(5);
  @$pb.TagNumber(5)
  $1.Timestamp ensureOccurredAt() => $_ensure(4);

  @$pb.TagNumber(6)
  $core.String get operation => $_getSZ(5);
  @$pb.TagNumber(6)
  set operation($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasOperation() => $_has(5);
  @$pb.TagNumber(6)
  void clearOperation() => $_clearField(6);

  @$pb.TagNumber(7)
  $pb.PbList<$core.String> get targetDigests => $_getList(6);

  @$pb.TagNumber(8)
  $core.int get additionalTargets => $_getIZ(7);
  @$pb.TagNumber(8)
  set additionalTargets($core.int value) => $_setUnsignedInt32(7, value);
  @$pb.TagNumber(8)
  $core.bool hasAdditionalTargets() => $_has(7);
  @$pb.TagNumber(8)
  void clearAdditionalTargets() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.String get outcome => $_getSZ(8);
  @$pb.TagNumber(9)
  set outcome($core.String value) => $_setString(8, value);
  @$pb.TagNumber(9)
  $core.bool hasOutcome() => $_has(8);
  @$pb.TagNumber(9)
  void clearOutcome() => $_clearField(9);
}

class ListSecurityAuditResponse extends $pb.GeneratedMessage {
  factory ListSecurityAuditResponse({
    $core.Iterable<SecurityAuditRecord>? records,
    SecurityVersion? version,
    $core.String? nextCursor,
    $core.Iterable<CurrentSecurityAuditRecord>? currentRecords,
  }) {
    final result = create();
    if (records != null) result.records.addAll(records);
    if (version != null) result.version = version;
    if (nextCursor != null) result.nextCursor = nextCursor;
    if (currentRecords != null) result.currentRecords.addAll(currentRecords);
    return result;
  }

  ListSecurityAuditResponse._();

  factory ListSecurityAuditResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ListSecurityAuditResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ListSecurityAuditResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<SecurityAuditRecord>(1, _omitFieldNames ? '' : 'records',
        subBuilder: SecurityAuditRecord.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..pPM<CurrentSecurityAuditRecord>(
        4, _omitFieldNames ? '' : 'currentRecords',
        subBuilder: CurrentSecurityAuditRecord.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSecurityAuditResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSecurityAuditResponse copyWith(
          void Function(ListSecurityAuditResponse) updates) =>
      super.copyWith((message) => updates(message as ListSecurityAuditResponse))
          as ListSecurityAuditResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ListSecurityAuditResponse create() => ListSecurityAuditResponse._();
  @$core.override
  ListSecurityAuditResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ListSecurityAuditResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ListSecurityAuditResponse>(create);
  static ListSecurityAuditResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<SecurityAuditRecord> get records => $_getList(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.String get nextCursor => $_getSZ(2);
  @$pb.TagNumber(3)
  set nextCursor($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasNextCursor() => $_has(2);
  @$pb.TagNumber(3)
  void clearNextCursor() => $_clearField(3);

  @$pb.TagNumber(4)
  $pb.PbList<CurrentSecurityAuditRecord> get currentRecords => $_getList(3);
}

class GetRoleTemplatesRequest extends $pb.GeneratedMessage {
  factory GetRoleTemplatesRequest({
    $core.String? prefix,
  }) {
    final result = create();
    if (prefix != null) result.prefix = prefix;
    return result;
  }

  GetRoleTemplatesRequest._();

  factory GetRoleTemplatesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetRoleTemplatesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetRoleTemplatesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'prefix')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleTemplatesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleTemplatesRequest copyWith(
          void Function(GetRoleTemplatesRequest) updates) =>
      super.copyWith((message) => updates(message as GetRoleTemplatesRequest))
          as GetRoleTemplatesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetRoleTemplatesRequest create() => GetRoleTemplatesRequest._();
  @$core.override
  GetRoleTemplatesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetRoleTemplatesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetRoleTemplatesRequest>(create);
  static GetRoleTemplatesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get prefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set prefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearPrefix() => $_clearField(1);
}

class GetRoleTemplatesResponse extends $pb.GeneratedMessage {
  factory GetRoleTemplatesResponse({
    $core.Iterable<SecurityRole>? roles,
    SecurityVersion? version,
  }) {
    final result = create();
    if (roles != null) result.roles.addAll(roles);
    if (version != null) result.version = version;
    return result;
  }

  GetRoleTemplatesResponse._();

  factory GetRoleTemplatesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetRoleTemplatesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetRoleTemplatesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<SecurityRole>(1, _omitFieldNames ? '' : 'roles',
        subBuilder: SecurityRole.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleTemplatesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleTemplatesResponse copyWith(
          void Function(GetRoleTemplatesResponse) updates) =>
      super.copyWith((message) => updates(message as GetRoleTemplatesResponse))
          as GetRoleTemplatesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetRoleTemplatesResponse create() => GetRoleTemplatesResponse._();
  @$core.override
  GetRoleTemplatesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetRoleTemplatesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetRoleTemplatesResponse>(create);
  static GetRoleTemplatesResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<SecurityRole> get roles => $_getList(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);
}

class SecurityEdgeIdentity extends $pb.GeneratedMessage {
  factory SecurityEdgeIdentity({
    $core.String? tail,
    $core.String? head,
  }) {
    final result = create();
    if (tail != null) result.tail = tail;
    if (head != null) result.head = head;
    return result;
  }

  SecurityEdgeIdentity._();

  factory SecurityEdgeIdentity.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityEdgeIdentity.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityEdgeIdentity',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tail')
    ..aOS(2, _omitFieldNames ? '' : 'head')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityEdgeIdentity clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityEdgeIdentity copyWith(void Function(SecurityEdgeIdentity) updates) =>
      super.copyWith((message) => updates(message as SecurityEdgeIdentity))
          as SecurityEdgeIdentity;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityEdgeIdentity create() => SecurityEdgeIdentity._();
  @$core.override
  SecurityEdgeIdentity createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityEdgeIdentity getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityEdgeIdentity>(create);
  static SecurityEdgeIdentity? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tail => $_getSZ(0);
  @$pb.TagNumber(1)
  set tail($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTail() => $_has(0);
  @$pb.TagNumber(1)
  void clearTail() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get head => $_getSZ(1);
  @$pb.TagNumber(2)
  set head($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHead() => $_has(1);
  @$pb.TagNumber(2)
  void clearHead() => $_clearField(2);
}

class ExplainAccessRequest extends $pb.GeneratedMessage {
  factory ExplainAccessRequest({
    SecurityIdentity? identity,
    SecurityAction? action,
    $core.String? logicalKey,
    SecurityEdgeIdentity? edge,
    SecurityPermission? permission,
    SecurityResource? resourceKind,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (action != null) result.action = action;
    if (logicalKey != null) result.logicalKey = logicalKey;
    if (edge != null) result.edge = edge;
    if (permission != null) result.permission = permission;
    if (resourceKind != null) result.resourceKind = resourceKind;
    return result;
  }

  ExplainAccessRequest._();

  factory ExplainAccessRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ExplainAccessRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ExplainAccessRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIdentity>(1, _omitFieldNames ? '' : 'identity',
        subBuilder: SecurityIdentity.create)
    ..aE<SecurityAction>(2, _omitFieldNames ? '' : 'action',
        enumValues: SecurityAction.values)
    ..aOS(3, _omitFieldNames ? '' : 'logicalKey')
    ..aOM<SecurityEdgeIdentity>(4, _omitFieldNames ? '' : 'edge',
        subBuilder: SecurityEdgeIdentity.create)
    ..aE<SecurityPermission>(5, _omitFieldNames ? '' : 'permission',
        enumValues: SecurityPermission.values)
    ..aE<SecurityResource>(6, _omitFieldNames ? '' : 'resourceKind',
        enumValues: SecurityResource.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExplainAccessRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExplainAccessRequest copyWith(void Function(ExplainAccessRequest) updates) =>
      super.copyWith((message) => updates(message as ExplainAccessRequest))
          as ExplainAccessRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ExplainAccessRequest create() => ExplainAccessRequest._();
  @$core.override
  ExplainAccessRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ExplainAccessRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ExplainAccessRequest>(create);
  static ExplainAccessRequest? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIdentity get identity => $_getN(0);
  @$pb.TagNumber(1)
  set identity(SecurityIdentity value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentity() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentity() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIdentity ensureIdentity() => $_ensure(0);

  /// Only derived Edge operation diagnostics use action + edge.
  @$pb.TagNumber(2)
  SecurityAction get action => $_getN(1);
  @$pb.TagNumber(2)
  set action(SecurityAction value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasAction() => $_has(1);
  @$pb.TagNumber(2)
  void clearAction() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get logicalKey => $_getSZ(2);
  @$pb.TagNumber(3)
  set logicalKey($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasLogicalKey() => $_has(2);
  @$pb.TagNumber(3)
  void clearLogicalKey() => $_clearField(3);

  @$pb.TagNumber(4)
  SecurityEdgeIdentity get edge => $_getN(3);
  @$pb.TagNumber(4)
  set edge(SecurityEdgeIdentity value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasEdge() => $_has(3);
  @$pb.TagNumber(4)
  void clearEdge() => $_clearField(4);
  @$pb.TagNumber(4)
  SecurityEdgeIdentity ensureEdge() => $_ensure(3);

  @$pb.TagNumber(5)
  SecurityPermission get permission => $_getN(4);
  @$pb.TagNumber(5)
  set permission(SecurityPermission value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasPermission() => $_has(4);
  @$pb.TagNumber(5)
  void clearPermission() => $_clearField(5);

  @$pb.TagNumber(6)
  SecurityResource get resourceKind => $_getN(5);
  @$pb.TagNumber(6)
  set resourceKind(SecurityResource value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasResourceKind() => $_has(5);
  @$pb.TagNumber(6)
  void clearResourceKind() => $_clearField(6);
}

class SecurityRuleMatch extends $pb.GeneratedMessage {
  factory SecurityRuleMatch({
    $core.String? roleId,
    $core.String? ruleId,
    SecurityPermission? permission,
    SecurityResource? resourceKind,
    $core.String? endpoint,
  }) {
    final result = create();
    if (roleId != null) result.roleId = roleId;
    if (ruleId != null) result.ruleId = ruleId;
    if (permission != null) result.permission = permission;
    if (resourceKind != null) result.resourceKind = resourceKind;
    if (endpoint != null) result.endpoint = endpoint;
    return result;
  }

  SecurityRuleMatch._();

  factory SecurityRuleMatch.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityRuleMatch.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityRuleMatch',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'roleId')
    ..aOS(2, _omitFieldNames ? '' : 'ruleId')
    ..aE<SecurityPermission>(3, _omitFieldNames ? '' : 'permission',
        enumValues: SecurityPermission.values)
    ..aE<SecurityResource>(4, _omitFieldNames ? '' : 'resourceKind',
        enumValues: SecurityResource.values)
    ..aOS(5, _omitFieldNames ? '' : 'endpoint')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRuleMatch clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRuleMatch copyWith(void Function(SecurityRuleMatch) updates) =>
      super.copyWith((message) => updates(message as SecurityRuleMatch))
          as SecurityRuleMatch;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityRuleMatch create() => SecurityRuleMatch._();
  @$core.override
  SecurityRuleMatch createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityRuleMatch getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityRuleMatch>(create);
  static SecurityRuleMatch? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get roleId => $_getSZ(0);
  @$pb.TagNumber(1)
  set roleId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasRoleId() => $_has(0);
  @$pb.TagNumber(1)
  void clearRoleId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get ruleId => $_getSZ(1);
  @$pb.TagNumber(2)
  set ruleId($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasRuleId() => $_has(1);
  @$pb.TagNumber(2)
  void clearRuleId() => $_clearField(2);

  @$pb.TagNumber(3)
  SecurityPermission get permission => $_getN(2);
  @$pb.TagNumber(3)
  set permission(SecurityPermission value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasPermission() => $_has(2);
  @$pb.TagNumber(3)
  void clearPermission() => $_clearField(3);

  @$pb.TagNumber(4)
  SecurityResource get resourceKind => $_getN(3);
  @$pb.TagNumber(4)
  set resourceKind(SecurityResource value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasResourceKind() => $_has(3);
  @$pb.TagNumber(4)
  void clearResourceKind() => $_clearField(4);

  /// "tail" or "head" for a derived Edge check; empty for ordinary grants.
  @$pb.TagNumber(5)
  $core.String get endpoint => $_getSZ(4);
  @$pb.TagNumber(5)
  set endpoint($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasEndpoint() => $_has(4);
  @$pb.TagNumber(5)
  void clearEndpoint() => $_clearField(5);
}

class ExplainAccessResponse extends $pb.GeneratedMessage {
  factory ExplainAccessResponse({
    $core.bool? allowed,
    $core.Iterable<SecurityRuleMatch>? matches,
    SecurityVersion? version,
  }) {
    final result = create();
    if (allowed != null) result.allowed = allowed;
    if (matches != null) result.matches.addAll(matches);
    if (version != null) result.version = version;
    return result;
  }

  ExplainAccessResponse._();

  factory ExplainAccessResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ExplainAccessResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ExplainAccessResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'allowed')
    ..pPM<SecurityRuleMatch>(2, _omitFieldNames ? '' : 'matches',
        subBuilder: SecurityRuleMatch.create)
    ..aOM<SecurityVersion>(3, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExplainAccessResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExplainAccessResponse copyWith(
          void Function(ExplainAccessResponse) updates) =>
      super.copyWith((message) => updates(message as ExplainAccessResponse))
          as ExplainAccessResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ExplainAccessResponse create() => ExplainAccessResponse._();
  @$core.override
  ExplainAccessResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ExplainAccessResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ExplainAccessResponse>(create);
  static ExplainAccessResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.bool get allowed => $_getBF(0);
  @$pb.TagNumber(1)
  set allowed($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAllowed() => $_has(0);
  @$pb.TagNumber(1)
  void clearAllowed() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<SecurityRuleMatch> get matches => $_getList(1);

  @$pb.TagNumber(3)
  SecurityVersion get version => $_getN(2);
  @$pb.TagNumber(3)
  set version(SecurityVersion value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasVersion() => $_has(2);
  @$pb.TagNumber(3)
  void clearVersion() => $_clearField(3);
  @$pb.TagNumber(3)
  SecurityVersion ensureVersion() => $_ensure(2);
}

class ValidateIssuerRequest extends $pb.GeneratedMessage {
  factory ValidateIssuerRequest({
    SecurityIssuer? issuer,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    return result;
  }

  ValidateIssuerRequest._();

  factory ValidateIssuerRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ValidateIssuerRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ValidateIssuerRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIssuer>(1, _omitFieldNames ? '' : 'issuer',
        subBuilder: SecurityIssuer.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ValidateIssuerRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ValidateIssuerRequest copyWith(
          void Function(ValidateIssuerRequest) updates) =>
      super.copyWith((message) => updates(message as ValidateIssuerRequest))
          as ValidateIssuerRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ValidateIssuerRequest create() => ValidateIssuerRequest._();
  @$core.override
  ValidateIssuerRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ValidateIssuerRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ValidateIssuerRequest>(create);
  static ValidateIssuerRequest? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIssuer get issuer => $_getN(0);
  @$pb.TagNumber(1)
  set issuer(SecurityIssuer value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearIssuer() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIssuer ensureIssuer() => $_ensure(0);
}

class ValidateIssuerResponse extends $pb.GeneratedMessage {
  factory ValidateIssuerResponse({
    $core.bool? valid,
  }) {
    final result = create();
    if (valid != null) result.valid = valid;
    return result;
  }

  ValidateIssuerResponse._();

  factory ValidateIssuerResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ValidateIssuerResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ValidateIssuerResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'valid')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ValidateIssuerResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ValidateIssuerResponse copyWith(
          void Function(ValidateIssuerResponse) updates) =>
      super.copyWith((message) => updates(message as ValidateIssuerResponse))
          as ValidateIssuerResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ValidateIssuerResponse create() => ValidateIssuerResponse._();
  @$core.override
  ValidateIssuerResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ValidateIssuerResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ValidateIssuerResponse>(create);
  static ValidateIssuerResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.bool get valid => $_getBF(0);
  @$pb.TagNumber(1)
  set valid($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasValid() => $_has(0);
  @$pb.TagNumber(1)
  void clearValid() => $_clearField(1);
}

class SecurityUserStateChange extends $pb.GeneratedMessage {
  factory SecurityUserStateChange({
    SecurityIdentity? identity,
    SecurityPrincipalState? state,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (state != null) result.state = state;
    return result;
  }

  SecurityUserStateChange._();

  factory SecurityUserStateChange.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityUserStateChange.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityUserStateChange',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityIdentity>(1, _omitFieldNames ? '' : 'identity',
        subBuilder: SecurityIdentity.create)
    ..aE<SecurityPrincipalState>(2, _omitFieldNames ? '' : 'state',
        enumValues: SecurityPrincipalState.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityUserStateChange clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityUserStateChange copyWith(
          void Function(SecurityUserStateChange) updates) =>
      super.copyWith((message) => updates(message as SecurityUserStateChange))
          as SecurityUserStateChange;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityUserStateChange create() => SecurityUserStateChange._();
  @$core.override
  SecurityUserStateChange createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityUserStateChange getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityUserStateChange>(create);
  static SecurityUserStateChange? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityIdentity get identity => $_getN(0);
  @$pb.TagNumber(1)
  set identity(SecurityIdentity value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasIdentity() => $_has(0);
  @$pb.TagNumber(1)
  void clearIdentity() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIdentity ensureIdentity() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityPrincipalState get state => $_getN(1);
  @$pb.TagNumber(2)
  set state(SecurityPrincipalState value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasState() => $_has(1);
  @$pb.TagNumber(2)
  void clearState() => $_clearField(2);
}

class SecurityRegistrationProfile extends $pb.GeneratedMessage {
  factory SecurityRegistrationProfile({
    $core.String? issuer,
    $core.String? clientProfileId,
    RegistrationProfileKind? kind,
  }) {
    final result = create();
    if (issuer != null) result.issuer = issuer;
    if (clientProfileId != null) result.clientProfileId = clientProfileId;
    if (kind != null) result.kind = kind;
    return result;
  }

  SecurityRegistrationProfile._();

  factory SecurityRegistrationProfile.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityRegistrationProfile.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityRegistrationProfile',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'issuer')
    ..aOS(2, _omitFieldNames ? '' : 'clientProfileId')
    ..aE<RegistrationProfileKind>(3, _omitFieldNames ? '' : 'kind',
        enumValues: RegistrationProfileKind.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRegistrationProfile clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRegistrationProfile copyWith(
          void Function(SecurityRegistrationProfile) updates) =>
      super.copyWith(
              (message) => updates(message as SecurityRegistrationProfile))
          as SecurityRegistrationProfile;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityRegistrationProfile create() =>
      SecurityRegistrationProfile._();
  @$core.override
  SecurityRegistrationProfile createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityRegistrationProfile getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityRegistrationProfile>(create);
  static SecurityRegistrationProfile? _defaultInstance;

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

  @$pb.TagNumber(3)
  RegistrationProfileKind get kind => $_getN(2);
  @$pb.TagNumber(3)
  set kind(RegistrationProfileKind value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasKind() => $_has(2);
  @$pb.TagNumber(3)
  void clearKind() => $_clearField(3);
}

/// Default absent/disabled. Expansion requires operation-bound reauthentication.
class SecurityRegistrationPolicy extends $pb.GeneratedMessage {
  factory SecurityRegistrationPolicy({
    $core.int? version,
    $core.bool? enabled,
    $core.String? roleId,
    $core.Iterable<SecurityRegistrationProfile>? profiles,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (enabled != null) result.enabled = enabled;
    if (roleId != null) result.roleId = roleId;
    if (profiles != null) result.profiles.addAll(profiles);
    return result;
  }

  SecurityRegistrationPolicy._();

  factory SecurityRegistrationPolicy.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityRegistrationPolicy.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityRegistrationPolicy',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..aOB(2, _omitFieldNames ? '' : 'enabled')
    ..aOS(3, _omitFieldNames ? '' : 'roleId')
    ..pPM<SecurityRegistrationProfile>(4, _omitFieldNames ? '' : 'profiles',
        subBuilder: SecurityRegistrationProfile.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRegistrationPolicy clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRegistrationPolicy copyWith(
          void Function(SecurityRegistrationPolicy) updates) =>
      super.copyWith(
              (message) => updates(message as SecurityRegistrationPolicy))
          as SecurityRegistrationPolicy;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityRegistrationPolicy create() => SecurityRegistrationPolicy._();
  @$core.override
  SecurityRegistrationPolicy createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityRegistrationPolicy getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityRegistrationPolicy>(create);
  static SecurityRegistrationPolicy? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.bool get enabled => $_getBF(1);
  @$pb.TagNumber(2)
  set enabled($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasEnabled() => $_has(1);
  @$pb.TagNumber(2)
  void clearEnabled() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get roleId => $_getSZ(2);
  @$pb.TagNumber(3)
  set roleId($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasRoleId() => $_has(2);
  @$pb.TagNumber(3)
  void clearRoleId() => $_clearField(3);

  @$pb.TagNumber(4)
  $pb.PbList<SecurityRegistrationProfile> get profiles => $_getList(3);
}

class GetRegistrationPolicyRequest extends $pb.GeneratedMessage {
  factory GetRegistrationPolicyRequest() => create();

  GetRegistrationPolicyRequest._();

  factory GetRegistrationPolicyRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetRegistrationPolicyRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetRegistrationPolicyRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRegistrationPolicyRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRegistrationPolicyRequest copyWith(
          void Function(GetRegistrationPolicyRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetRegistrationPolicyRequest))
          as GetRegistrationPolicyRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetRegistrationPolicyRequest create() =>
      GetRegistrationPolicyRequest._();
  @$core.override
  GetRegistrationPolicyRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetRegistrationPolicyRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetRegistrationPolicyRequest>(create);
  static GetRegistrationPolicyRequest? _defaultInstance;
}

class GetRegistrationPolicyResponse extends $pb.GeneratedMessage {
  factory GetRegistrationPolicyResponse({
    SecurityRegistrationPolicy? policy,
    SecurityVersion? version,
  }) {
    final result = create();
    if (policy != null) result.policy = policy;
    if (version != null) result.version = version;
    return result;
  }

  GetRegistrationPolicyResponse._();

  factory GetRegistrationPolicyResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetRegistrationPolicyResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetRegistrationPolicyResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityRegistrationPolicy>(1, _omitFieldNames ? '' : 'policy',
        subBuilder: SecurityRegistrationPolicy.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRegistrationPolicyResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRegistrationPolicyResponse copyWith(
          void Function(GetRegistrationPolicyResponse) updates) =>
      super.copyWith(
              (message) => updates(message as GetRegistrationPolicyResponse))
          as GetRegistrationPolicyResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetRegistrationPolicyResponse create() =>
      GetRegistrationPolicyResponse._();
  @$core.override
  GetRegistrationPolicyResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetRegistrationPolicyResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetRegistrationPolicyResponse>(create);
  static GetRegistrationPolicyResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityRegistrationPolicy get policy => $_getN(0);
  @$pb.TagNumber(1)
  set policy(SecurityRegistrationPolicy value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasPolicy() => $_has(0);
  @$pb.TagNumber(1)
  void clearPolicy() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityRegistrationPolicy ensurePolicy() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);
}

class SecurityEdgePolicy extends $pb.GeneratedMessage {
  factory SecurityEdgePolicy({
    $core.int? version,
    EdgeEndpointMode? mode,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (mode != null) result.mode = mode;
    return result;
  }

  SecurityEdgePolicy._();

  factory SecurityEdgePolicy.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityEdgePolicy.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityEdgePolicy',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..aE<EdgeEndpointMode>(2, _omitFieldNames ? '' : 'mode',
        enumValues: EdgeEndpointMode.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityEdgePolicy clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityEdgePolicy copyWith(void Function(SecurityEdgePolicy) updates) =>
      super.copyWith((message) => updates(message as SecurityEdgePolicy))
          as SecurityEdgePolicy;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityEdgePolicy create() => SecurityEdgePolicy._();
  @$core.override
  SecurityEdgePolicy createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityEdgePolicy getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityEdgePolicy>(create);
  static SecurityEdgePolicy? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  EdgeEndpointMode get mode => $_getN(1);
  @$pb.TagNumber(2)
  set mode(EdgeEndpointMode value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasMode() => $_has(1);
  @$pb.TagNumber(2)
  void clearMode() => $_clearField(2);
}

class GetEdgePolicyRequest extends $pb.GeneratedMessage {
  factory GetEdgePolicyRequest() => create();

  GetEdgePolicyRequest._();

  factory GetEdgePolicyRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetEdgePolicyRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetEdgePolicyRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgePolicyRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgePolicyRequest copyWith(void Function(GetEdgePolicyRequest) updates) =>
      super.copyWith((message) => updates(message as GetEdgePolicyRequest))
          as GetEdgePolicyRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetEdgePolicyRequest create() => GetEdgePolicyRequest._();
  @$core.override
  GetEdgePolicyRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetEdgePolicyRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetEdgePolicyRequest>(create);
  static GetEdgePolicyRequest? _defaultInstance;
}

class GetEdgePolicyResponse extends $pb.GeneratedMessage {
  factory GetEdgePolicyResponse({
    SecurityEdgePolicy? policy,
    SecurityVersion? version,
  }) {
    final result = create();
    if (policy != null) result.policy = policy;
    if (version != null) result.version = version;
    return result;
  }

  GetEdgePolicyResponse._();

  factory GetEdgePolicyResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetEdgePolicyResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetEdgePolicyResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityEdgePolicy>(1, _omitFieldNames ? '' : 'policy',
        subBuilder: SecurityEdgePolicy.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgePolicyResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgePolicyResponse copyWith(
          void Function(GetEdgePolicyResponse) updates) =>
      super.copyWith((message) => updates(message as GetEdgePolicyResponse))
          as GetEdgePolicyResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetEdgePolicyResponse create() => GetEdgePolicyResponse._();
  @$core.override
  GetEdgePolicyResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetEdgePolicyResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetEdgePolicyResponse>(create);
  static GetEdgePolicyResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityEdgePolicy get policy => $_getN(0);
  @$pb.TagNumber(1)
  set policy(SecurityEdgePolicy value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasPolicy() => $_has(0);
  @$pb.TagNumber(1)
  void clearPolicy() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityEdgePolicy ensurePolicy() => $_ensure(0);

  @$pb.TagNumber(2)
  SecurityVersion get version => $_getN(1);
  @$pb.TagNumber(2)
  set version(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureVersion() => $_ensure(1);
}

/// Typed business intent only. The committed owner allocates the generation.
class ManagedLogPolicy extends $pb.GeneratedMessage {
  factory ManagedLogPolicy({
    $core.int? version,
    ManagedLogLevel? level,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (level != null) result.level = level;
    return result;
  }

  ManagedLogPolicy._();

  factory ManagedLogPolicy.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ManagedLogPolicy.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ManagedLogPolicy',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..aE<ManagedLogLevel>(2, _omitFieldNames ? '' : 'level',
        enumValues: ManagedLogLevel.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ManagedLogPolicy clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ManagedLogPolicy copyWith(void Function(ManagedLogPolicy) updates) =>
      super.copyWith((message) => updates(message as ManagedLogPolicy))
          as ManagedLogPolicy;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ManagedLogPolicy create() => ManagedLogPolicy._();
  @$core.override
  ManagedLogPolicy createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ManagedLogPolicy getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ManagedLogPolicy>(create);
  static ManagedLogPolicy? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  ManagedLogLevel get level => $_getN(1);
  @$pb.TagNumber(2)
  set level(ManagedLogLevel value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasLevel() => $_has(1);
  @$pb.TagNumber(2)
  void clearLevel() => $_clearField(2);
}

class GetLogSettingsRequest extends $pb.GeneratedMessage {
  factory GetLogSettingsRequest() => create();

  GetLogSettingsRequest._();

  factory GetLogSettingsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetLogSettingsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetLogSettingsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetLogSettingsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetLogSettingsRequest copyWith(
          void Function(GetLogSettingsRequest) updates) =>
      super.copyWith((message) => updates(message as GetLogSettingsRequest))
          as GetLogSettingsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetLogSettingsRequest create() => GetLogSettingsRequest._();
  @$core.override
  GetLogSettingsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetLogSettingsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetLogSettingsRequest>(create);
  static GetLogSettingsRequest? _defaultInstance;
}

/// Correlation with the exact retained signed adoption intent. This is not the
/// signed proof, an authorization credential or an internal journal position.
class LogPolicyPreparationReference extends $pb.GeneratedMessage {
  factory LogPolicyPreparationReference({
    CurrentSecurityChangeID? changeId,
    $core.List<$core.int>? intentDigest,
    $core.List<$core.int>? actorDigest,
  }) {
    final result = create();
    if (changeId != null) result.changeId = changeId;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (actorDigest != null) result.actorDigest = actorDigest;
    return result;
  }

  LogPolicyPreparationReference._();

  factory LogPolicyPreparationReference.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory LogPolicyPreparationReference.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'LogPolicyPreparationReference',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentSecurityChangeID>(1, _omitFieldNames ? '' : 'changeId',
        subBuilder: CurrentSecurityChangeID.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'actorDigest', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogPolicyPreparationReference clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LogPolicyPreparationReference copyWith(
          void Function(LogPolicyPreparationReference) updates) =>
      super.copyWith(
              (message) => updates(message as LogPolicyPreparationReference))
          as LogPolicyPreparationReference;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static LogPolicyPreparationReference create() =>
      LogPolicyPreparationReference._();
  @$core.override
  LogPolicyPreparationReference createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static LogPolicyPreparationReference getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<LogPolicyPreparationReference>(create);
  static LogPolicyPreparationReference? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentSecurityChangeID get changeId => $_getN(0);
  @$pb.TagNumber(1)
  set changeId(CurrentSecurityChangeID value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasChangeId() => $_has(0);
  @$pb.TagNumber(1)
  void clearChangeId() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentSecurityChangeID ensureChangeId() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.List<$core.int> get intentDigest => $_getN(1);
  @$pb.TagNumber(2)
  set intentDigest($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasIntentDigest() => $_has(1);
  @$pb.TagNumber(2)
  void clearIntentDigest() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get actorDigest => $_getN(2);
  @$pb.TagNumber(3)
  set actorDigest($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasActorDigest() => $_has(2);
  @$pb.TagNumber(3)
  void clearActorDigest() => $_clearField(3);
}

class GetLogSettingsResponse extends $pb.GeneratedMessage {
  factory GetLogSettingsResponse({
    $core.int? schemaVersion,
    $core.int? member,
    ManagedLogLevel? effectiveLevel,
    LogSettingsSource? source,
    ManagedLogPolicy? desired,
    $fixnum.Int64? desiredGeneration,
    $fixnum.Int64? appliedGeneration,
    LogPolicyAdoptionState? adoption,
    LogSettingsApplicationState? application,
    LogSettingsError? error,
    $core.bool? restartRequired,
    SecurityVersion? version,
    CurrentSemanticCut? appliedCut,
    LogPolicyPreparationReference? preparation,
  }) {
    final result = create();
    if (schemaVersion != null) result.schemaVersion = schemaVersion;
    if (member != null) result.member = member;
    if (effectiveLevel != null) result.effectiveLevel = effectiveLevel;
    if (source != null) result.source = source;
    if (desired != null) result.desired = desired;
    if (desiredGeneration != null) result.desiredGeneration = desiredGeneration;
    if (appliedGeneration != null) result.appliedGeneration = appliedGeneration;
    if (adoption != null) result.adoption = adoption;
    if (application != null) result.application = application;
    if (error != null) result.error = error;
    if (restartRequired != null) result.restartRequired = restartRequired;
    if (version != null) result.version = version;
    if (appliedCut != null) result.appliedCut = appliedCut;
    if (preparation != null) result.preparation = preparation;
    return result;
  }

  GetLogSettingsResponse._();

  factory GetLogSettingsResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetLogSettingsResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetLogSettingsResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'schemaVersion',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(2, _omitFieldNames ? '' : 'member', fieldType: $pb.PbFieldType.OU3)
    ..aE<ManagedLogLevel>(3, _omitFieldNames ? '' : 'effectiveLevel',
        enumValues: ManagedLogLevel.values)
    ..aE<LogSettingsSource>(4, _omitFieldNames ? '' : 'source',
        enumValues: LogSettingsSource.values)
    ..aOM<ManagedLogPolicy>(5, _omitFieldNames ? '' : 'desired',
        subBuilder: ManagedLogPolicy.create)
    ..a<$fixnum.Int64>(
        6, _omitFieldNames ? '' : 'desiredGeneration', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        7, _omitFieldNames ? '' : 'appliedGeneration', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aE<LogPolicyAdoptionState>(8, _omitFieldNames ? '' : 'adoption',
        enumValues: LogPolicyAdoptionState.values)
    ..aE<LogSettingsApplicationState>(9, _omitFieldNames ? '' : 'application',
        enumValues: LogSettingsApplicationState.values)
    ..aE<LogSettingsError>(10, _omitFieldNames ? '' : 'error',
        enumValues: LogSettingsError.values)
    ..aOB(11, _omitFieldNames ? '' : 'restartRequired')
    ..aOM<SecurityVersion>(12, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOM<CurrentSemanticCut>(13, _omitFieldNames ? '' : 'appliedCut',
        subBuilder: CurrentSemanticCut.create)
    ..aOM<LogPolicyPreparationReference>(
        14, _omitFieldNames ? '' : 'preparation',
        subBuilder: LogPolicyPreparationReference.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetLogSettingsResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetLogSettingsResponse copyWith(
          void Function(GetLogSettingsResponse) updates) =>
      super.copyWith((message) => updates(message as GetLogSettingsResponse))
          as GetLogSettingsResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetLogSettingsResponse create() => GetLogSettingsResponse._();
  @$core.override
  GetLogSettingsResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetLogSettingsResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetLogSettingsResponse>(create);
  static GetLogSettingsResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get schemaVersion => $_getIZ(0);
  @$pb.TagNumber(1)
  set schemaVersion($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSchemaVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearSchemaVersion() => $_clearField(1);

  /// Fixed configured member identity, never a private endpoint or listener.
  @$pb.TagNumber(2)
  $core.int get member => $_getIZ(1);
  @$pb.TagNumber(2)
  set member($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasMember() => $_has(1);
  @$pb.TagNumber(2)
  void clearMember() => $_clearField(2);

  @$pb.TagNumber(3)
  ManagedLogLevel get effectiveLevel => $_getN(2);
  @$pb.TagNumber(3)
  set effectiveLevel(ManagedLogLevel value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasEffectiveLevel() => $_has(2);
  @$pb.TagNumber(3)
  void clearEffectiveLevel() => $_clearField(3);

  @$pb.TagNumber(4)
  LogSettingsSource get source => $_getN(3);
  @$pb.TagNumber(4)
  set source(LogSettingsSource value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasSource() => $_has(3);
  @$pb.TagNumber(4)
  void clearSource() => $_clearField(4);

  /// Absent only in verified unadopted state; never an unavailable-state fallback.
  @$pb.TagNumber(5)
  ManagedLogPolicy get desired => $_getN(4);
  @$pb.TagNumber(5)
  set desired(ManagedLogPolicy value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasDesired() => $_has(4);
  @$pb.TagNumber(5)
  void clearDesired() => $_clearField(5);
  @$pb.TagNumber(5)
  ManagedLogPolicy ensureDesired() => $_ensure(4);

  @$pb.TagNumber(6)
  $fixnum.Int64 get desiredGeneration => $_getI64(5);
  @$pb.TagNumber(6)
  set desiredGeneration($fixnum.Int64 value) => $_setInt64(5, value);
  @$pb.TagNumber(6)
  $core.bool hasDesiredGeneration() => $_has(5);
  @$pb.TagNumber(6)
  void clearDesiredGeneration() => $_clearField(6);

  @$pb.TagNumber(7)
  $fixnum.Int64 get appliedGeneration => $_getI64(6);
  @$pb.TagNumber(7)
  set appliedGeneration($fixnum.Int64 value) => $_setInt64(6, value);
  @$pb.TagNumber(7)
  $core.bool hasAppliedGeneration() => $_has(6);
  @$pb.TagNumber(7)
  void clearAppliedGeneration() => $_clearField(7);

  @$pb.TagNumber(8)
  LogPolicyAdoptionState get adoption => $_getN(7);
  @$pb.TagNumber(8)
  set adoption(LogPolicyAdoptionState value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasAdoption() => $_has(7);
  @$pb.TagNumber(8)
  void clearAdoption() => $_clearField(8);

  @$pb.TagNumber(9)
  LogSettingsApplicationState get application => $_getN(8);
  @$pb.TagNumber(9)
  set application(LogSettingsApplicationState value) => $_setField(9, value);
  @$pb.TagNumber(9)
  $core.bool hasApplication() => $_has(8);
  @$pb.TagNumber(9)
  void clearApplication() => $_clearField(9);

  /// Closed content-free diagnostics; raw provider errors and paths are private.
  @$pb.TagNumber(10)
  LogSettingsError get error => $_getN(9);
  @$pb.TagNumber(10)
  set error(LogSettingsError value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasError() => $_has(9);
  @$pb.TagNumber(10)
  void clearError() => $_clearField(10);

  @$pb.TagNumber(11)
  $core.bool get restartRequired => $_getBF(10);
  @$pb.TagNumber(11)
  set restartRequired($core.bool value) => $_setBool(10, value);
  @$pb.TagNumber(11)
  $core.bool hasRestartRequired() => $_has(10);
  @$pb.TagNumber(11)
  void clearRestartRequired() => $_clearField(11);

  @$pb.TagNumber(12)
  SecurityVersion get version => $_getN(11);
  @$pb.TagNumber(12)
  set version(SecurityVersion value) => $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasVersion() => $_has(11);
  @$pb.TagNumber(12)
  void clearVersion() => $_clearField(12);
  @$pb.TagNumber(12)
  SecurityVersion ensureVersion() => $_ensure(11);

  /// The exact cut successfully installed by this node, not a peer acknowledgement.
  @$pb.TagNumber(13)
  CurrentSemanticCut get appliedCut => $_getN(12);
  @$pb.TagNumber(13)
  set appliedCut(CurrentSemanticCut value) => $_setField(13, value);
  @$pb.TagNumber(13)
  $core.bool hasAppliedCut() => $_has(12);
  @$pb.TagNumber(13)
  void clearAppliedCut() => $_clearField(13);
  @$pb.TagNumber(13)
  CurrentSemanticCut ensureAppliedCut() => $_ensure(12);

  /// Required for fenced-but-unadopted state; may remain after adoption.
  @$pb.TagNumber(14)
  LogPolicyPreparationReference get preparation => $_getN(13);
  @$pb.TagNumber(14)
  set preparation(LogPolicyPreparationReference value) => $_setField(14, value);
  @$pb.TagNumber(14)
  $core.bool hasPreparation() => $_has(13);
  @$pb.TagNumber(14)
  void clearPreparation() => $_clearField(14);
  @$pb.TagNumber(14)
  LogPolicyPreparationReference ensurePreparation() => $_ensure(13);
}

class OperationalSlowRPCValues extends $pb.GeneratedMessage {
  factory OperationalSlowRPCValues({
    $fixnum.Int64? thresholdMs,
  }) {
    final result = create();
    if (thresholdMs != null) result.thresholdMs = thresholdMs;
    return result;
  }

  OperationalSlowRPCValues._();

  factory OperationalSlowRPCValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalSlowRPCValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalSlowRPCValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aInt64(1, _omitFieldNames ? '' : 'thresholdMs')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalSlowRPCValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalSlowRPCValues copyWith(
          void Function(OperationalSlowRPCValues) updates) =>
      super.copyWith((message) => updates(message as OperationalSlowRPCValues))
          as OperationalSlowRPCValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalSlowRPCValues create() => OperationalSlowRPCValues._();
  @$core.override
  OperationalSlowRPCValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalSlowRPCValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalSlowRPCValues>(create);
  static OperationalSlowRPCValues? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get thresholdMs => $_getI64(0);
  @$pb.TagNumber(1)
  set thresholdMs($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasThresholdMs() => $_has(0);
  @$pb.TagNumber(1)
  void clearThresholdMs() => $_clearField(1);
}

class OperationalRateValues extends $pb.GeneratedMessage {
  factory OperationalRateValues({
    $core.double? rps,
    $fixnum.Int64? burst,
  }) {
    final result = create();
    if (rps != null) result.rps = rps;
    if (burst != null) result.burst = burst;
    return result;
  }

  OperationalRateValues._();

  factory OperationalRateValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalRateValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalRateValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aD(1, _omitFieldNames ? '' : 'rps')
    ..aInt64(2, _omitFieldNames ? '' : 'burst')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalRateValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalRateValues copyWith(
          void Function(OperationalRateValues) updates) =>
      super.copyWith((message) => updates(message as OperationalRateValues))
          as OperationalRateValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalRateValues create() => OperationalRateValues._();
  @$core.override
  OperationalRateValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalRateValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalRateValues>(create);
  static OperationalRateValues? _defaultInstance;

  @$pb.TagNumber(1)
  $core.double get rps => $_getN(0);
  @$pb.TagNumber(1)
  set rps($core.double value) => $_setDouble(0, value);
  @$pb.TagNumber(1)
  $core.bool hasRps() => $_has(0);
  @$pb.TagNumber(1)
  void clearRps() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get burst => $_getI64(1);
  @$pb.TagNumber(2)
  set burst($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasBurst() => $_has(1);
  @$pb.TagNumber(2)
  void clearBurst() => $_clearField(2);
}

class OperationalRequestValues extends $pb.GeneratedMessage {
  factory OperationalRequestValues({
    $fixnum.Int64? deleteByPrefixDefaultLimit,
    $fixnum.Int64? deleteByPrefixMaxLimit,
    $fixnum.Int64? illuminateMaxK,
    $fixnum.Int64? illuminateMaxStep,
    $fixnum.Int64? maxBatchSize,
    $fixnum.Int64? maxKeyLen,
    $fixnum.Int64? scanDefaultLimit,
    $fixnum.Int64? scanMaxLimit,
    $fixnum.Int64? traversalMaxPushes,
    $fixnum.Int64? traversalMaxResults,
    $fixnum.Int64? traversalMaxTouchedEdges,
    $fixnum.Int64? traversalTimeoutMs,
  }) {
    final result = create();
    if (deleteByPrefixDefaultLimit != null)
      result.deleteByPrefixDefaultLimit = deleteByPrefixDefaultLimit;
    if (deleteByPrefixMaxLimit != null)
      result.deleteByPrefixMaxLimit = deleteByPrefixMaxLimit;
    if (illuminateMaxK != null) result.illuminateMaxK = illuminateMaxK;
    if (illuminateMaxStep != null) result.illuminateMaxStep = illuminateMaxStep;
    if (maxBatchSize != null) result.maxBatchSize = maxBatchSize;
    if (maxKeyLen != null) result.maxKeyLen = maxKeyLen;
    if (scanDefaultLimit != null) result.scanDefaultLimit = scanDefaultLimit;
    if (scanMaxLimit != null) result.scanMaxLimit = scanMaxLimit;
    if (traversalMaxPushes != null)
      result.traversalMaxPushes = traversalMaxPushes;
    if (traversalMaxResults != null)
      result.traversalMaxResults = traversalMaxResults;
    if (traversalMaxTouchedEdges != null)
      result.traversalMaxTouchedEdges = traversalMaxTouchedEdges;
    if (traversalTimeoutMs != null)
      result.traversalTimeoutMs = traversalTimeoutMs;
    return result;
  }

  OperationalRequestValues._();

  factory OperationalRequestValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalRequestValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalRequestValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aInt64(1, _omitFieldNames ? '' : 'deleteByPrefixDefaultLimit')
    ..aInt64(2, _omitFieldNames ? '' : 'deleteByPrefixMaxLimit')
    ..aInt64(3, _omitFieldNames ? '' : 'illuminateMaxK')
    ..aInt64(4, _omitFieldNames ? '' : 'illuminateMaxStep')
    ..aInt64(5, _omitFieldNames ? '' : 'maxBatchSize')
    ..aInt64(6, _omitFieldNames ? '' : 'maxKeyLen')
    ..aInt64(7, _omitFieldNames ? '' : 'scanDefaultLimit')
    ..aInt64(8, _omitFieldNames ? '' : 'scanMaxLimit')
    ..aInt64(9, _omitFieldNames ? '' : 'traversalMaxPushes')
    ..aInt64(10, _omitFieldNames ? '' : 'traversalMaxResults')
    ..aInt64(11, _omitFieldNames ? '' : 'traversalMaxTouchedEdges')
    ..aInt64(12, _omitFieldNames ? '' : 'traversalTimeoutMs')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalRequestValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalRequestValues copyWith(
          void Function(OperationalRequestValues) updates) =>
      super.copyWith((message) => updates(message as OperationalRequestValues))
          as OperationalRequestValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalRequestValues create() => OperationalRequestValues._();
  @$core.override
  OperationalRequestValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalRequestValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalRequestValues>(create);
  static OperationalRequestValues? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get deleteByPrefixDefaultLimit => $_getI64(0);
  @$pb.TagNumber(1)
  set deleteByPrefixDefaultLimit($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDeleteByPrefixDefaultLimit() => $_has(0);
  @$pb.TagNumber(1)
  void clearDeleteByPrefixDefaultLimit() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get deleteByPrefixMaxLimit => $_getI64(1);
  @$pb.TagNumber(2)
  set deleteByPrefixMaxLimit($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDeleteByPrefixMaxLimit() => $_has(1);
  @$pb.TagNumber(2)
  void clearDeleteByPrefixMaxLimit() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get illuminateMaxK => $_getI64(2);
  @$pb.TagNumber(3)
  set illuminateMaxK($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasIlluminateMaxK() => $_has(2);
  @$pb.TagNumber(3)
  void clearIlluminateMaxK() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get illuminateMaxStep => $_getI64(3);
  @$pb.TagNumber(4)
  set illuminateMaxStep($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasIlluminateMaxStep() => $_has(3);
  @$pb.TagNumber(4)
  void clearIlluminateMaxStep() => $_clearField(4);

  @$pb.TagNumber(5)
  $fixnum.Int64 get maxBatchSize => $_getI64(4);
  @$pb.TagNumber(5)
  set maxBatchSize($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasMaxBatchSize() => $_has(4);
  @$pb.TagNumber(5)
  void clearMaxBatchSize() => $_clearField(5);

  @$pb.TagNumber(6)
  $fixnum.Int64 get maxKeyLen => $_getI64(5);
  @$pb.TagNumber(6)
  set maxKeyLen($fixnum.Int64 value) => $_setInt64(5, value);
  @$pb.TagNumber(6)
  $core.bool hasMaxKeyLen() => $_has(5);
  @$pb.TagNumber(6)
  void clearMaxKeyLen() => $_clearField(6);

  @$pb.TagNumber(7)
  $fixnum.Int64 get scanDefaultLimit => $_getI64(6);
  @$pb.TagNumber(7)
  set scanDefaultLimit($fixnum.Int64 value) => $_setInt64(6, value);
  @$pb.TagNumber(7)
  $core.bool hasScanDefaultLimit() => $_has(6);
  @$pb.TagNumber(7)
  void clearScanDefaultLimit() => $_clearField(7);

  @$pb.TagNumber(8)
  $fixnum.Int64 get scanMaxLimit => $_getI64(7);
  @$pb.TagNumber(8)
  set scanMaxLimit($fixnum.Int64 value) => $_setInt64(7, value);
  @$pb.TagNumber(8)
  $core.bool hasScanMaxLimit() => $_has(7);
  @$pb.TagNumber(8)
  void clearScanMaxLimit() => $_clearField(8);

  @$pb.TagNumber(9)
  $fixnum.Int64 get traversalMaxPushes => $_getI64(8);
  @$pb.TagNumber(9)
  set traversalMaxPushes($fixnum.Int64 value) => $_setInt64(8, value);
  @$pb.TagNumber(9)
  $core.bool hasTraversalMaxPushes() => $_has(8);
  @$pb.TagNumber(9)
  void clearTraversalMaxPushes() => $_clearField(9);

  @$pb.TagNumber(10)
  $fixnum.Int64 get traversalMaxResults => $_getI64(9);
  @$pb.TagNumber(10)
  set traversalMaxResults($fixnum.Int64 value) => $_setInt64(9, value);
  @$pb.TagNumber(10)
  $core.bool hasTraversalMaxResults() => $_has(9);
  @$pb.TagNumber(10)
  void clearTraversalMaxResults() => $_clearField(10);

  @$pb.TagNumber(11)
  $fixnum.Int64 get traversalMaxTouchedEdges => $_getI64(10);
  @$pb.TagNumber(11)
  set traversalMaxTouchedEdges($fixnum.Int64 value) => $_setInt64(10, value);
  @$pb.TagNumber(11)
  $core.bool hasTraversalMaxTouchedEdges() => $_has(10);
  @$pb.TagNumber(11)
  void clearTraversalMaxTouchedEdges() => $_clearField(11);

  @$pb.TagNumber(12)
  $fixnum.Int64 get traversalTimeoutMs => $_getI64(11);
  @$pb.TagNumber(12)
  set traversalTimeoutMs($fixnum.Int64 value) => $_setInt64(11, value);
  @$pb.TagNumber(12)
  $core.bool hasTraversalTimeoutMs() => $_has(11);
  @$pb.TagNumber(12)
  void clearTraversalTimeoutMs() => $_clearField(12);
}

class OperationalBackupValues extends $pb.GeneratedMessage {
  factory OperationalBackupValues({
    $core.bool? enabled,
    $fixnum.Int64? intervalNanos,
    $fixnum.Int64? retain,
  }) {
    final result = create();
    if (enabled != null) result.enabled = enabled;
    if (intervalNanos != null) result.intervalNanos = intervalNanos;
    if (retain != null) result.retain = retain;
    return result;
  }

  OperationalBackupValues._();

  factory OperationalBackupValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalBackupValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalBackupValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'enabled')
    ..aInt64(2, _omitFieldNames ? '' : 'intervalNanos')
    ..aInt64(3, _omitFieldNames ? '' : 'retain')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalBackupValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalBackupValues copyWith(
          void Function(OperationalBackupValues) updates) =>
      super.copyWith((message) => updates(message as OperationalBackupValues))
          as OperationalBackupValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalBackupValues create() => OperationalBackupValues._();
  @$core.override
  OperationalBackupValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalBackupValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalBackupValues>(create);
  static OperationalBackupValues? _defaultInstance;

  @$pb.TagNumber(1)
  $core.bool get enabled => $_getBF(0);
  @$pb.TagNumber(1)
  set enabled($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasEnabled() => $_has(0);
  @$pb.TagNumber(1)
  void clearEnabled() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get intervalNanos => $_getI64(1);
  @$pb.TagNumber(2)
  set intervalNanos($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasIntervalNanos() => $_has(1);
  @$pb.TagNumber(2)
  void clearIntervalNanos() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get retain => $_getI64(2);
  @$pb.TagNumber(3)
  set retain($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasRetain() => $_has(2);
  @$pb.TagNumber(3)
  void clearRetain() => $_clearField(3);
}

class OperationalCapacityValues extends $pb.GeneratedMessage {
  factory OperationalCapacityValues({
    $fixnum.Int64? gcEdgeBudget,
    $fixnum.Int64? gcIntervalSeconds,
    $fixnum.Int64? maxEdges,
    $fixnum.Int64? maxEdgeCausalEntries,
    $fixnum.Int64? maxVertexCausalEntries,
    $fixnum.Int64? maxVertices,
  }) {
    final result = create();
    if (gcEdgeBudget != null) result.gcEdgeBudget = gcEdgeBudget;
    if (gcIntervalSeconds != null) result.gcIntervalSeconds = gcIntervalSeconds;
    if (maxEdges != null) result.maxEdges = maxEdges;
    if (maxEdgeCausalEntries != null)
      result.maxEdgeCausalEntries = maxEdgeCausalEntries;
    if (maxVertexCausalEntries != null)
      result.maxVertexCausalEntries = maxVertexCausalEntries;
    if (maxVertices != null) result.maxVertices = maxVertices;
    return result;
  }

  OperationalCapacityValues._();

  factory OperationalCapacityValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalCapacityValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalCapacityValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aInt64(1, _omitFieldNames ? '' : 'gcEdgeBudget')
    ..aInt64(2, _omitFieldNames ? '' : 'gcIntervalSeconds')
    ..aInt64(3, _omitFieldNames ? '' : 'maxEdges')
    ..aInt64(4, _omitFieldNames ? '' : 'maxEdgeCausalEntries')
    ..aInt64(5, _omitFieldNames ? '' : 'maxVertexCausalEntries')
    ..aInt64(6, _omitFieldNames ? '' : 'maxVertices')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalCapacityValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalCapacityValues copyWith(
          void Function(OperationalCapacityValues) updates) =>
      super.copyWith((message) => updates(message as OperationalCapacityValues))
          as OperationalCapacityValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalCapacityValues create() => OperationalCapacityValues._();
  @$core.override
  OperationalCapacityValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalCapacityValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalCapacityValues>(create);
  static OperationalCapacityValues? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get gcEdgeBudget => $_getI64(0);
  @$pb.TagNumber(1)
  set gcEdgeBudget($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasGcEdgeBudget() => $_has(0);
  @$pb.TagNumber(1)
  void clearGcEdgeBudget() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get gcIntervalSeconds => $_getI64(1);
  @$pb.TagNumber(2)
  set gcIntervalSeconds($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasGcIntervalSeconds() => $_has(1);
  @$pb.TagNumber(2)
  void clearGcIntervalSeconds() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get maxEdges => $_getI64(2);
  @$pb.TagNumber(3)
  set maxEdges($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasMaxEdges() => $_has(2);
  @$pb.TagNumber(3)
  void clearMaxEdges() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get maxEdgeCausalEntries => $_getI64(3);
  @$pb.TagNumber(4)
  set maxEdgeCausalEntries($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasMaxEdgeCausalEntries() => $_has(3);
  @$pb.TagNumber(4)
  void clearMaxEdgeCausalEntries() => $_clearField(4);

  @$pb.TagNumber(5)
  $fixnum.Int64 get maxVertexCausalEntries => $_getI64(4);
  @$pb.TagNumber(5)
  set maxVertexCausalEntries($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasMaxVertexCausalEntries() => $_has(4);
  @$pb.TagNumber(5)
  void clearMaxVertexCausalEntries() => $_clearField(5);

  @$pb.TagNumber(6)
  $fixnum.Int64 get maxVertices => $_getI64(5);
  @$pb.TagNumber(6)
  set maxVertices($fixnum.Int64 value) => $_setInt64(5, value);
  @$pb.TagNumber(6)
  $core.bool hasMaxVertices() => $_has(5);
  @$pb.TagNumber(6)
  void clearMaxVertices() => $_clearField(6);
}

class OperationalLifecycleValues extends $pb.GeneratedMessage {
  factory OperationalLifecycleValues({
    $fixnum.Int64? drainDelaySeconds,
    $fixnum.Int64? maxReplicationLag,
    $fixnum.Int64? shutdownTimeoutSeconds,
  }) {
    final result = create();
    if (drainDelaySeconds != null) result.drainDelaySeconds = drainDelaySeconds;
    if (maxReplicationLag != null) result.maxReplicationLag = maxReplicationLag;
    if (shutdownTimeoutSeconds != null)
      result.shutdownTimeoutSeconds = shutdownTimeoutSeconds;
    return result;
  }

  OperationalLifecycleValues._();

  factory OperationalLifecycleValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalLifecycleValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalLifecycleValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aInt64(1, _omitFieldNames ? '' : 'drainDelaySeconds')
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'maxReplicationLag', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aInt64(3, _omitFieldNames ? '' : 'shutdownTimeoutSeconds')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalLifecycleValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalLifecycleValues copyWith(
          void Function(OperationalLifecycleValues) updates) =>
      super.copyWith(
              (message) => updates(message as OperationalLifecycleValues))
          as OperationalLifecycleValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalLifecycleValues create() => OperationalLifecycleValues._();
  @$core.override
  OperationalLifecycleValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalLifecycleValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalLifecycleValues>(create);
  static OperationalLifecycleValues? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get drainDelaySeconds => $_getI64(0);
  @$pb.TagNumber(1)
  set drainDelaySeconds($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDrainDelaySeconds() => $_has(0);
  @$pb.TagNumber(1)
  void clearDrainDelaySeconds() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get maxReplicationLag => $_getI64(1);
  @$pb.TagNumber(2)
  set maxReplicationLag($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasMaxReplicationLag() => $_has(1);
  @$pb.TagNumber(2)
  void clearMaxReplicationLag() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get shutdownTimeoutSeconds => $_getI64(2);
  @$pb.TagNumber(3)
  set shutdownTimeoutSeconds($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasShutdownTimeoutSeconds() => $_has(2);
  @$pb.TagNumber(3)
  void clearShutdownTimeoutSeconds() => $_clearField(3);
}

class OperationalSearchValues extends $pb.GeneratedMessage {
  factory OperationalSearchValues({
    $fixnum.Int64? defaultLimit,
    $fixnum.Int64? maxLimit,
    OperationalSearchMode? defaultMode,
    $fixnum.Int64? defaultMinShould,
    $fixnum.Int64? timeoutMs,
    $fixnum.Int64? maxQueryBytes,
    $fixnum.Int64? maxQueryTerms,
    $fixnum.Int64? maxDictionaryVisits,
    $fixnum.Int64? maxPostingVisits,
    $fixnum.Int64? maxPositionVisits,
    $fixnum.Int64? maxExpirationVisits,
  }) {
    final result = create();
    if (defaultLimit != null) result.defaultLimit = defaultLimit;
    if (maxLimit != null) result.maxLimit = maxLimit;
    if (defaultMode != null) result.defaultMode = defaultMode;
    if (defaultMinShould != null) result.defaultMinShould = defaultMinShould;
    if (timeoutMs != null) result.timeoutMs = timeoutMs;
    if (maxQueryBytes != null) result.maxQueryBytes = maxQueryBytes;
    if (maxQueryTerms != null) result.maxQueryTerms = maxQueryTerms;
    if (maxDictionaryVisits != null)
      result.maxDictionaryVisits = maxDictionaryVisits;
    if (maxPostingVisits != null) result.maxPostingVisits = maxPostingVisits;
    if (maxPositionVisits != null) result.maxPositionVisits = maxPositionVisits;
    if (maxExpirationVisits != null)
      result.maxExpirationVisits = maxExpirationVisits;
    return result;
  }

  OperationalSearchValues._();

  factory OperationalSearchValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalSearchValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalSearchValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aInt64(1, _omitFieldNames ? '' : 'defaultLimit')
    ..aInt64(2, _omitFieldNames ? '' : 'maxLimit')
    ..aE<OperationalSearchMode>(3, _omitFieldNames ? '' : 'defaultMode',
        enumValues: OperationalSearchMode.values)
    ..aInt64(4, _omitFieldNames ? '' : 'defaultMinShould')
    ..aInt64(5, _omitFieldNames ? '' : 'timeoutMs')
    ..aInt64(6, _omitFieldNames ? '' : 'maxQueryBytes')
    ..aInt64(7, _omitFieldNames ? '' : 'maxQueryTerms')
    ..aInt64(8, _omitFieldNames ? '' : 'maxDictionaryVisits')
    ..aInt64(9, _omitFieldNames ? '' : 'maxPostingVisits')
    ..aInt64(10, _omitFieldNames ? '' : 'maxPositionVisits')
    ..aInt64(11, _omitFieldNames ? '' : 'maxExpirationVisits')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalSearchValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalSearchValues copyWith(
          void Function(OperationalSearchValues) updates) =>
      super.copyWith((message) => updates(message as OperationalSearchValues))
          as OperationalSearchValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalSearchValues create() => OperationalSearchValues._();
  @$core.override
  OperationalSearchValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalSearchValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalSearchValues>(create);
  static OperationalSearchValues? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get defaultLimit => $_getI64(0);
  @$pb.TagNumber(1)
  set defaultLimit($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDefaultLimit() => $_has(0);
  @$pb.TagNumber(1)
  void clearDefaultLimit() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get maxLimit => $_getI64(1);
  @$pb.TagNumber(2)
  set maxLimit($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasMaxLimit() => $_has(1);
  @$pb.TagNumber(2)
  void clearMaxLimit() => $_clearField(2);

  @$pb.TagNumber(3)
  OperationalSearchMode get defaultMode => $_getN(2);
  @$pb.TagNumber(3)
  set defaultMode(OperationalSearchMode value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasDefaultMode() => $_has(2);
  @$pb.TagNumber(3)
  void clearDefaultMode() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get defaultMinShould => $_getI64(3);
  @$pb.TagNumber(4)
  set defaultMinShould($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasDefaultMinShould() => $_has(3);
  @$pb.TagNumber(4)
  void clearDefaultMinShould() => $_clearField(4);

  @$pb.TagNumber(5)
  $fixnum.Int64 get timeoutMs => $_getI64(4);
  @$pb.TagNumber(5)
  set timeoutMs($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasTimeoutMs() => $_has(4);
  @$pb.TagNumber(5)
  void clearTimeoutMs() => $_clearField(5);

  @$pb.TagNumber(6)
  $fixnum.Int64 get maxQueryBytes => $_getI64(5);
  @$pb.TagNumber(6)
  set maxQueryBytes($fixnum.Int64 value) => $_setInt64(5, value);
  @$pb.TagNumber(6)
  $core.bool hasMaxQueryBytes() => $_has(5);
  @$pb.TagNumber(6)
  void clearMaxQueryBytes() => $_clearField(6);

  @$pb.TagNumber(7)
  $fixnum.Int64 get maxQueryTerms => $_getI64(6);
  @$pb.TagNumber(7)
  set maxQueryTerms($fixnum.Int64 value) => $_setInt64(6, value);
  @$pb.TagNumber(7)
  $core.bool hasMaxQueryTerms() => $_has(6);
  @$pb.TagNumber(7)
  void clearMaxQueryTerms() => $_clearField(7);

  @$pb.TagNumber(8)
  $fixnum.Int64 get maxDictionaryVisits => $_getI64(7);
  @$pb.TagNumber(8)
  set maxDictionaryVisits($fixnum.Int64 value) => $_setInt64(7, value);
  @$pb.TagNumber(8)
  $core.bool hasMaxDictionaryVisits() => $_has(7);
  @$pb.TagNumber(8)
  void clearMaxDictionaryVisits() => $_clearField(8);

  @$pb.TagNumber(9)
  $fixnum.Int64 get maxPostingVisits => $_getI64(8);
  @$pb.TagNumber(9)
  set maxPostingVisits($fixnum.Int64 value) => $_setInt64(8, value);
  @$pb.TagNumber(9)
  $core.bool hasMaxPostingVisits() => $_has(8);
  @$pb.TagNumber(9)
  void clearMaxPostingVisits() => $_clearField(9);

  @$pb.TagNumber(10)
  $fixnum.Int64 get maxPositionVisits => $_getI64(9);
  @$pb.TagNumber(10)
  set maxPositionVisits($fixnum.Int64 value) => $_setInt64(9, value);
  @$pb.TagNumber(10)
  $core.bool hasMaxPositionVisits() => $_has(9);
  @$pb.TagNumber(10)
  void clearMaxPositionVisits() => $_clearField(10);

  @$pb.TagNumber(11)
  $fixnum.Int64 get maxExpirationVisits => $_getI64(10);
  @$pb.TagNumber(11)
  set maxExpirationVisits($fixnum.Int64 value) => $_setInt64(10, value);
  @$pb.TagNumber(11)
  $core.bool hasMaxExpirationVisits() => $_has(10);
  @$pb.TagNumber(11)
  void clearMaxExpirationVisits() => $_clearField(11);
}

class OperationalReplicationValues extends $pb.GeneratedMessage {
  factory OperationalReplicationValues({
    $fixnum.Int64? antiEntropyGapWarnThreshold,
    $fixnum.Int64? antiEntropyIntervalMs,
    $fixnum.Int64? antiEntropySubscribeTimeoutMs,
    $fixnum.Int64? pumpBackoffMaxMs,
    $fixnum.Int64? pumpBackoffMinMs,
  }) {
    final result = create();
    if (antiEntropyGapWarnThreshold != null)
      result.antiEntropyGapWarnThreshold = antiEntropyGapWarnThreshold;
    if (antiEntropyIntervalMs != null)
      result.antiEntropyIntervalMs = antiEntropyIntervalMs;
    if (antiEntropySubscribeTimeoutMs != null)
      result.antiEntropySubscribeTimeoutMs = antiEntropySubscribeTimeoutMs;
    if (pumpBackoffMaxMs != null) result.pumpBackoffMaxMs = pumpBackoffMaxMs;
    if (pumpBackoffMinMs != null) result.pumpBackoffMinMs = pumpBackoffMinMs;
    return result;
  }

  OperationalReplicationValues._();

  factory OperationalReplicationValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalReplicationValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalReplicationValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aInt64(1, _omitFieldNames ? '' : 'antiEntropyGapWarnThreshold')
    ..aInt64(2, _omitFieldNames ? '' : 'antiEntropyIntervalMs')
    ..aInt64(3, _omitFieldNames ? '' : 'antiEntropySubscribeTimeoutMs')
    ..aInt64(4, _omitFieldNames ? '' : 'pumpBackoffMaxMs')
    ..aInt64(5, _omitFieldNames ? '' : 'pumpBackoffMinMs')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalReplicationValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalReplicationValues copyWith(
          void Function(OperationalReplicationValues) updates) =>
      super.copyWith(
              (message) => updates(message as OperationalReplicationValues))
          as OperationalReplicationValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalReplicationValues create() =>
      OperationalReplicationValues._();
  @$core.override
  OperationalReplicationValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalReplicationValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalReplicationValues>(create);
  static OperationalReplicationValues? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get antiEntropyGapWarnThreshold => $_getI64(0);
  @$pb.TagNumber(1)
  set antiEntropyGapWarnThreshold($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAntiEntropyGapWarnThreshold() => $_has(0);
  @$pb.TagNumber(1)
  void clearAntiEntropyGapWarnThreshold() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get antiEntropyIntervalMs => $_getI64(1);
  @$pb.TagNumber(2)
  set antiEntropyIntervalMs($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasAntiEntropyIntervalMs() => $_has(1);
  @$pb.TagNumber(2)
  void clearAntiEntropyIntervalMs() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get antiEntropySubscribeTimeoutMs => $_getI64(2);
  @$pb.TagNumber(3)
  set antiEntropySubscribeTimeoutMs($fixnum.Int64 value) =>
      $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasAntiEntropySubscribeTimeoutMs() => $_has(2);
  @$pb.TagNumber(3)
  void clearAntiEntropySubscribeTimeoutMs() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get pumpBackoffMaxMs => $_getI64(3);
  @$pb.TagNumber(4)
  set pumpBackoffMaxMs($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasPumpBackoffMaxMs() => $_has(3);
  @$pb.TagNumber(4)
  void clearPumpBackoffMaxMs() => $_clearField(4);

  @$pb.TagNumber(5)
  $fixnum.Int64 get pumpBackoffMinMs => $_getI64(4);
  @$pb.TagNumber(5)
  set pumpBackoffMinMs($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasPumpBackoffMinMs() => $_has(4);
  @$pb.TagNumber(5)
  void clearPumpBackoffMinMs() => $_clearField(5);
}

enum OperationalPolicyValues_Group {
  slowRpc,
  rate,
  request,
  backup,
  capacity,
  lifecycle,
  search,
  replication,
  notSet
}

class OperationalPolicyValues extends $pb.GeneratedMessage {
  factory OperationalPolicyValues({
    OperationalSlowRPCValues? slowRpc,
    OperationalRateValues? rate,
    OperationalRequestValues? request,
    OperationalBackupValues? backup,
    OperationalCapacityValues? capacity,
    OperationalLifecycleValues? lifecycle,
    OperationalSearchValues? search,
    OperationalReplicationValues? replication,
  }) {
    final result = create();
    if (slowRpc != null) result.slowRpc = slowRpc;
    if (rate != null) result.rate = rate;
    if (request != null) result.request = request;
    if (backup != null) result.backup = backup;
    if (capacity != null) result.capacity = capacity;
    if (lifecycle != null) result.lifecycle = lifecycle;
    if (search != null) result.search = search;
    if (replication != null) result.replication = replication;
    return result;
  }

  OperationalPolicyValues._();

  factory OperationalPolicyValues.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalPolicyValues.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, OperationalPolicyValues_Group>
      _OperationalPolicyValues_GroupByTag = {
    1: OperationalPolicyValues_Group.slowRpc,
    2: OperationalPolicyValues_Group.rate,
    3: OperationalPolicyValues_Group.request,
    4: OperationalPolicyValues_Group.backup,
    5: OperationalPolicyValues_Group.capacity,
    6: OperationalPolicyValues_Group.lifecycle,
    7: OperationalPolicyValues_Group.search,
    8: OperationalPolicyValues_Group.replication,
    0: OperationalPolicyValues_Group.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalPolicyValues',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [1, 2, 3, 4, 5, 6, 7, 8])
    ..aOM<OperationalSlowRPCValues>(1, _omitFieldNames ? '' : 'slowRpc',
        subBuilder: OperationalSlowRPCValues.create)
    ..aOM<OperationalRateValues>(2, _omitFieldNames ? '' : 'rate',
        subBuilder: OperationalRateValues.create)
    ..aOM<OperationalRequestValues>(3, _omitFieldNames ? '' : 'request',
        subBuilder: OperationalRequestValues.create)
    ..aOM<OperationalBackupValues>(4, _omitFieldNames ? '' : 'backup',
        subBuilder: OperationalBackupValues.create)
    ..aOM<OperationalCapacityValues>(5, _omitFieldNames ? '' : 'capacity',
        subBuilder: OperationalCapacityValues.create)
    ..aOM<OperationalLifecycleValues>(6, _omitFieldNames ? '' : 'lifecycle',
        subBuilder: OperationalLifecycleValues.create)
    ..aOM<OperationalSearchValues>(7, _omitFieldNames ? '' : 'search',
        subBuilder: OperationalSearchValues.create)
    ..aOM<OperationalReplicationValues>(8, _omitFieldNames ? '' : 'replication',
        subBuilder: OperationalReplicationValues.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalPolicyValues clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalPolicyValues copyWith(
          void Function(OperationalPolicyValues) updates) =>
      super.copyWith((message) => updates(message as OperationalPolicyValues))
          as OperationalPolicyValues;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalPolicyValues create() => OperationalPolicyValues._();
  @$core.override
  OperationalPolicyValues createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalPolicyValues getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalPolicyValues>(create);
  static OperationalPolicyValues? _defaultInstance;

  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  @$pb.TagNumber(3)
  @$pb.TagNumber(4)
  @$pb.TagNumber(5)
  @$pb.TagNumber(6)
  @$pb.TagNumber(7)
  @$pb.TagNumber(8)
  OperationalPolicyValues_Group whichGroup() =>
      _OperationalPolicyValues_GroupByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  @$pb.TagNumber(3)
  @$pb.TagNumber(4)
  @$pb.TagNumber(5)
  @$pb.TagNumber(6)
  @$pb.TagNumber(7)
  @$pb.TagNumber(8)
  void clearGroup() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  OperationalSlowRPCValues get slowRpc => $_getN(0);
  @$pb.TagNumber(1)
  set slowRpc(OperationalSlowRPCValues value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasSlowRpc() => $_has(0);
  @$pb.TagNumber(1)
  void clearSlowRpc() => $_clearField(1);
  @$pb.TagNumber(1)
  OperationalSlowRPCValues ensureSlowRpc() => $_ensure(0);

  @$pb.TagNumber(2)
  OperationalRateValues get rate => $_getN(1);
  @$pb.TagNumber(2)
  set rate(OperationalRateValues value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasRate() => $_has(1);
  @$pb.TagNumber(2)
  void clearRate() => $_clearField(2);
  @$pb.TagNumber(2)
  OperationalRateValues ensureRate() => $_ensure(1);

  @$pb.TagNumber(3)
  OperationalRequestValues get request => $_getN(2);
  @$pb.TagNumber(3)
  set request(OperationalRequestValues value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasRequest() => $_has(2);
  @$pb.TagNumber(3)
  void clearRequest() => $_clearField(3);
  @$pb.TagNumber(3)
  OperationalRequestValues ensureRequest() => $_ensure(2);

  @$pb.TagNumber(4)
  OperationalBackupValues get backup => $_getN(3);
  @$pb.TagNumber(4)
  set backup(OperationalBackupValues value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasBackup() => $_has(3);
  @$pb.TagNumber(4)
  void clearBackup() => $_clearField(4);
  @$pb.TagNumber(4)
  OperationalBackupValues ensureBackup() => $_ensure(3);

  @$pb.TagNumber(5)
  OperationalCapacityValues get capacity => $_getN(4);
  @$pb.TagNumber(5)
  set capacity(OperationalCapacityValues value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCapacity() => $_has(4);
  @$pb.TagNumber(5)
  void clearCapacity() => $_clearField(5);
  @$pb.TagNumber(5)
  OperationalCapacityValues ensureCapacity() => $_ensure(4);

  @$pb.TagNumber(6)
  OperationalLifecycleValues get lifecycle => $_getN(5);
  @$pb.TagNumber(6)
  set lifecycle(OperationalLifecycleValues value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasLifecycle() => $_has(5);
  @$pb.TagNumber(6)
  void clearLifecycle() => $_clearField(6);
  @$pb.TagNumber(6)
  OperationalLifecycleValues ensureLifecycle() => $_ensure(5);

  @$pb.TagNumber(7)
  OperationalSearchValues get search => $_getN(6);
  @$pb.TagNumber(7)
  set search(OperationalSearchValues value) => $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasSearch() => $_has(6);
  @$pb.TagNumber(7)
  void clearSearch() => $_clearField(7);
  @$pb.TagNumber(7)
  OperationalSearchValues ensureSearch() => $_ensure(6);

  @$pb.TagNumber(8)
  OperationalReplicationValues get replication => $_getN(7);
  @$pb.TagNumber(8)
  set replication(OperationalReplicationValues value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasReplication() => $_has(7);
  @$pb.TagNumber(8)
  void clearReplication() => $_clearField(8);
  @$pb.TagNumber(8)
  OperationalReplicationValues ensureReplication() => $_ensure(7);
}

/// The authority allocates generation; zero expected_generation means adoption.
class OperationalPolicyChange extends $pb.GeneratedMessage {
  factory OperationalPolicyChange({
    $core.int? version,
    $fixnum.Int64? expectedGeneration,
    OperationalPolicyValues? values,
    $core.String? compatibilityFingerprint,
    $core.bool? retentionReductionAcknowledged,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (expectedGeneration != null)
      result.expectedGeneration = expectedGeneration;
    if (values != null) result.values = values;
    if (compatibilityFingerprint != null)
      result.compatibilityFingerprint = compatibilityFingerprint;
    if (retentionReductionAcknowledged != null)
      result.retentionReductionAcknowledged = retentionReductionAcknowledged;
    return result;
  }

  OperationalPolicyChange._();

  factory OperationalPolicyChange.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalPolicyChange.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalPolicyChange',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'expectedGeneration', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOM<OperationalPolicyValues>(3, _omitFieldNames ? '' : 'values',
        subBuilder: OperationalPolicyValues.create)
    ..aOS(4, _omitFieldNames ? '' : 'compatibilityFingerprint')
    ..aOB(5, _omitFieldNames ? '' : 'retentionReductionAcknowledged')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalPolicyChange clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalPolicyChange copyWith(
          void Function(OperationalPolicyChange) updates) =>
      super.copyWith((message) => updates(message as OperationalPolicyChange))
          as OperationalPolicyChange;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalPolicyChange create() => OperationalPolicyChange._();
  @$core.override
  OperationalPolicyChange createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalPolicyChange getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalPolicyChange>(create);
  static OperationalPolicyChange? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get expectedGeneration => $_getI64(1);
  @$pb.TagNumber(2)
  set expectedGeneration($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasExpectedGeneration() => $_has(1);
  @$pb.TagNumber(2)
  void clearExpectedGeneration() => $_clearField(2);

  @$pb.TagNumber(3)
  OperationalPolicyValues get values => $_getN(2);
  @$pb.TagNumber(3)
  set values(OperationalPolicyValues value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasValues() => $_has(2);
  @$pb.TagNumber(3)
  void clearValues() => $_clearField(3);
  @$pb.TagNumber(3)
  OperationalPolicyValues ensureValues() => $_ensure(2);

  /// Search engine compatibility metadata, resolved by Preview, never editable.
  @$pb.TagNumber(4)
  $core.String get compatibilityFingerprint => $_getSZ(3);
  @$pb.TagNumber(4)
  set compatibilityFingerprint($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasCompatibilityFingerprint() => $_has(3);
  @$pb.TagNumber(4)
  void clearCompatibilityFingerprint() => $_clearField(4);

  /// Explicit acknowledgement, sealed in the exact immutable reviewed intent.
  @$pb.TagNumber(5)
  $core.bool get retentionReductionAcknowledged => $_getBF(4);
  @$pb.TagNumber(5)
  set retentionReductionAcknowledged($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasRetentionReductionAcknowledged() => $_has(4);
  @$pb.TagNumber(5)
  void clearRetentionReductionAcknowledged() => $_clearField(5);
}

class ManagedOperationalPolicy extends $pb.GeneratedMessage {
  factory ManagedOperationalPolicy({
    $core.int? version,
    $fixnum.Int64? generation,
    OperationalPolicyValues? values,
    $core.String? compatibilityFingerprint,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (generation != null) result.generation = generation;
    if (values != null) result.values = values;
    if (compatibilityFingerprint != null)
      result.compatibilityFingerprint = compatibilityFingerprint;
    return result;
  }

  ManagedOperationalPolicy._();

  factory ManagedOperationalPolicy.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ManagedOperationalPolicy.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ManagedOperationalPolicy',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'generation', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOM<OperationalPolicyValues>(3, _omitFieldNames ? '' : 'values',
        subBuilder: OperationalPolicyValues.create)
    ..aOS(4, _omitFieldNames ? '' : 'compatibilityFingerprint')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ManagedOperationalPolicy clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ManagedOperationalPolicy copyWith(
          void Function(ManagedOperationalPolicy) updates) =>
      super.copyWith((message) => updates(message as ManagedOperationalPolicy))
          as ManagedOperationalPolicy;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ManagedOperationalPolicy create() => ManagedOperationalPolicy._();
  @$core.override
  ManagedOperationalPolicy createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ManagedOperationalPolicy getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ManagedOperationalPolicy>(create);
  static ManagedOperationalPolicy? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get generation => $_getI64(1);
  @$pb.TagNumber(2)
  set generation($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasGeneration() => $_has(1);
  @$pb.TagNumber(2)
  void clearGeneration() => $_clearField(2);

  @$pb.TagNumber(3)
  OperationalPolicyValues get values => $_getN(2);
  @$pb.TagNumber(3)
  set values(OperationalPolicyValues value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasValues() => $_has(2);
  @$pb.TagNumber(3)
  void clearValues() => $_clearField(3);
  @$pb.TagNumber(3)
  OperationalPolicyValues ensureValues() => $_ensure(2);

  @$pb.TagNumber(4)
  $core.String get compatibilityFingerprint => $_getSZ(3);
  @$pb.TagNumber(4)
  set compatibilityFingerprint($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasCompatibilityFingerprint() => $_has(3);
  @$pb.TagNumber(4)
  void clearCompatibilityFingerprint() => $_clearField(4);
}

class OperationalSettingProvenance extends $pb.GeneratedMessage {
  factory OperationalSettingProvenance({
    OperationalSetting? setting,
    OperationalSettingsSource? source,
  }) {
    final result = create();
    if (setting != null) result.setting = setting;
    if (source != null) result.source = source;
    return result;
  }

  OperationalSettingProvenance._();

  factory OperationalSettingProvenance.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalSettingProvenance.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalSettingProvenance',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aE<OperationalSetting>(1, _omitFieldNames ? '' : 'setting',
        enumValues: OperationalSetting.values)
    ..aE<OperationalSettingsSource>(2, _omitFieldNames ? '' : 'source',
        enumValues: OperationalSettingsSource.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalSettingProvenance clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalSettingProvenance copyWith(
          void Function(OperationalSettingProvenance) updates) =>
      super.copyWith(
              (message) => updates(message as OperationalSettingProvenance))
          as OperationalSettingProvenance;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalSettingProvenance create() =>
      OperationalSettingProvenance._();
  @$core.override
  OperationalSettingProvenance createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalSettingProvenance getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalSettingProvenance>(create);
  static OperationalSettingProvenance? _defaultInstance;

  @$pb.TagNumber(1)
  OperationalSetting get setting => $_getN(0);
  @$pb.TagNumber(1)
  set setting(OperationalSetting value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasSetting() => $_has(0);
  @$pb.TagNumber(1)
  void clearSetting() => $_clearField(1);

  @$pb.TagNumber(2)
  OperationalSettingsSource get source => $_getN(1);
  @$pb.TagNumber(2)
  set source(OperationalSettingsSource value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasSource() => $_has(1);
  @$pb.TagNumber(2)
  void clearSource() => $_clearField(2);
}

class OperationalGroupSettings extends $pb.GeneratedMessage {
  factory OperationalGroupSettings({
    OperationalGroup? group,
    ManagedOperationalPolicy? desired,
    OperationalPolicyValues? effective,
    $core.Iterable<OperationalSettingProvenance>? provenance,
    $fixnum.Int64? appliedGeneration,
    CurrentSemanticCut? appliedCut,
    OperationalSettingsChangeMode? changeMode,
    LogPolicyAdoptionState? adoption,
    OperationalSettingsApplicationState? application,
    OperationalSettingsError? error,
    $core.bool? restartRequired,
    LogPolicyPreparationReference? preparation,
    $core.String? effectiveCompatibilityFingerprint,
  }) {
    final result = create();
    if (group != null) result.group = group;
    if (desired != null) result.desired = desired;
    if (effective != null) result.effective = effective;
    if (provenance != null) result.provenance.addAll(provenance);
    if (appliedGeneration != null) result.appliedGeneration = appliedGeneration;
    if (appliedCut != null) result.appliedCut = appliedCut;
    if (changeMode != null) result.changeMode = changeMode;
    if (adoption != null) result.adoption = adoption;
    if (application != null) result.application = application;
    if (error != null) result.error = error;
    if (restartRequired != null) result.restartRequired = restartRequired;
    if (preparation != null) result.preparation = preparation;
    if (effectiveCompatibilityFingerprint != null)
      result.effectiveCompatibilityFingerprint =
          effectiveCompatibilityFingerprint;
    return result;
  }

  OperationalGroupSettings._();

  factory OperationalGroupSettings.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalGroupSettings.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalGroupSettings',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aE<OperationalGroup>(1, _omitFieldNames ? '' : 'group',
        enumValues: OperationalGroup.values)
    ..aOM<ManagedOperationalPolicy>(2, _omitFieldNames ? '' : 'desired',
        subBuilder: ManagedOperationalPolicy.create)
    ..aOM<OperationalPolicyValues>(3, _omitFieldNames ? '' : 'effective',
        subBuilder: OperationalPolicyValues.create)
    ..pPM<OperationalSettingProvenance>(4, _omitFieldNames ? '' : 'provenance',
        subBuilder: OperationalSettingProvenance.create)
    ..a<$fixnum.Int64>(
        5, _omitFieldNames ? '' : 'appliedGeneration', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOM<CurrentSemanticCut>(6, _omitFieldNames ? '' : 'appliedCut',
        subBuilder: CurrentSemanticCut.create)
    ..aE<OperationalSettingsChangeMode>(7, _omitFieldNames ? '' : 'changeMode',
        enumValues: OperationalSettingsChangeMode.values)
    ..aE<LogPolicyAdoptionState>(8, _omitFieldNames ? '' : 'adoption',
        enumValues: LogPolicyAdoptionState.values)
    ..aE<OperationalSettingsApplicationState>(
        9, _omitFieldNames ? '' : 'application',
        enumValues: OperationalSettingsApplicationState.values)
    ..aE<OperationalSettingsError>(10, _omitFieldNames ? '' : 'error',
        enumValues: OperationalSettingsError.values)
    ..aOB(11, _omitFieldNames ? '' : 'restartRequired')
    ..aOM<LogPolicyPreparationReference>(
        12, _omitFieldNames ? '' : 'preparation',
        subBuilder: LogPolicyPreparationReference.create)
    ..aOS(13, _omitFieldNames ? '' : 'effectiveCompatibilityFingerprint')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalGroupSettings clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalGroupSettings copyWith(
          void Function(OperationalGroupSettings) updates) =>
      super.copyWith((message) => updates(message as OperationalGroupSettings))
          as OperationalGroupSettings;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalGroupSettings create() => OperationalGroupSettings._();
  @$core.override
  OperationalGroupSettings createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalGroupSettings getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalGroupSettings>(create);
  static OperationalGroupSettings? _defaultInstance;

  @$pb.TagNumber(1)
  OperationalGroup get group => $_getN(0);
  @$pb.TagNumber(1)
  set group(OperationalGroup value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasGroup() => $_has(0);
  @$pb.TagNumber(1)
  void clearGroup() => $_clearField(1);

  @$pb.TagNumber(2)
  ManagedOperationalPolicy get desired => $_getN(1);
  @$pb.TagNumber(2)
  set desired(ManagedOperationalPolicy value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasDesired() => $_has(1);
  @$pb.TagNumber(2)
  void clearDesired() => $_clearField(2);
  @$pb.TagNumber(2)
  ManagedOperationalPolicy ensureDesired() => $_ensure(1);

  /// Absent for an unavailable owner or explicitly invalid bootstrap values.
  /// Invalid bootstrap keeps installed change mode and provenance, with ERROR.
  @$pb.TagNumber(3)
  OperationalPolicyValues get effective => $_getN(2);
  @$pb.TagNumber(3)
  set effective(OperationalPolicyValues value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasEffective() => $_has(2);
  @$pb.TagNumber(3)
  void clearEffective() => $_clearField(3);
  @$pb.TagNumber(3)
  OperationalPolicyValues ensureEffective() => $_ensure(2);

  @$pb.TagNumber(4)
  $pb.PbList<OperationalSettingProvenance> get provenance => $_getList(3);

  @$pb.TagNumber(5)
  $fixnum.Int64 get appliedGeneration => $_getI64(4);
  @$pb.TagNumber(5)
  set appliedGeneration($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasAppliedGeneration() => $_has(4);
  @$pb.TagNumber(5)
  void clearAppliedGeneration() => $_clearField(5);

  @$pb.TagNumber(6)
  CurrentSemanticCut get appliedCut => $_getN(5);
  @$pb.TagNumber(6)
  set appliedCut(CurrentSemanticCut value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasAppliedCut() => $_has(5);
  @$pb.TagNumber(6)
  void clearAppliedCut() => $_clearField(6);
  @$pb.TagNumber(6)
  CurrentSemanticCut ensureAppliedCut() => $_ensure(5);

  @$pb.TagNumber(7)
  OperationalSettingsChangeMode get changeMode => $_getN(6);
  @$pb.TagNumber(7)
  set changeMode(OperationalSettingsChangeMode value) => $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasChangeMode() => $_has(6);
  @$pb.TagNumber(7)
  void clearChangeMode() => $_clearField(7);

  @$pb.TagNumber(8)
  LogPolicyAdoptionState get adoption => $_getN(7);
  @$pb.TagNumber(8)
  set adoption(LogPolicyAdoptionState value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasAdoption() => $_has(7);
  @$pb.TagNumber(8)
  void clearAdoption() => $_clearField(8);

  @$pb.TagNumber(9)
  OperationalSettingsApplicationState get application => $_getN(8);
  @$pb.TagNumber(9)
  set application(OperationalSettingsApplicationState value) =>
      $_setField(9, value);
  @$pb.TagNumber(9)
  $core.bool hasApplication() => $_has(8);
  @$pb.TagNumber(9)
  void clearApplication() => $_clearField(9);

  @$pb.TagNumber(10)
  OperationalSettingsError get error => $_getN(9);
  @$pb.TagNumber(10)
  set error(OperationalSettingsError value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasError() => $_has(9);
  @$pb.TagNumber(10)
  void clearError() => $_clearField(10);

  @$pb.TagNumber(11)
  $core.bool get restartRequired => $_getBF(10);
  @$pb.TagNumber(11)
  set restartRequired($core.bool value) => $_setBool(10, value);
  @$pb.TagNumber(11)
  $core.bool hasRestartRequired() => $_has(10);
  @$pb.TagNumber(11)
  void clearRestartRequired() => $_clearField(11);

  @$pb.TagNumber(12)
  LogPolicyPreparationReference get preparation => $_getN(11);
  @$pb.TagNumber(12)
  set preparation(LogPolicyPreparationReference value) => $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasPreparation() => $_has(11);
  @$pb.TagNumber(12)
  void clearPreparation() => $_clearField(12);
  @$pb.TagNumber(12)
  LogPolicyPreparationReference ensurePreparation() => $_ensure(11);

  /// Actual local Search engine fingerprint, including the excluded baseline.
  @$pb.TagNumber(13)
  $core.String get effectiveCompatibilityFingerprint => $_getSZ(12);
  @$pb.TagNumber(13)
  set effectiveCompatibilityFingerprint($core.String value) =>
      $_setString(12, value);
  @$pb.TagNumber(13)
  $core.bool hasEffectiveCompatibilityFingerprint() => $_has(12);
  @$pb.TagNumber(13)
  void clearEffectiveCompatibilityFingerprint() => $_clearField(13);
}

class GetOperationalSettingsRequest extends $pb.GeneratedMessage {
  factory GetOperationalSettingsRequest() => create();

  GetOperationalSettingsRequest._();

  factory GetOperationalSettingsRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetOperationalSettingsRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetOperationalSettingsRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetOperationalSettingsRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetOperationalSettingsRequest copyWith(
          void Function(GetOperationalSettingsRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetOperationalSettingsRequest))
          as GetOperationalSettingsRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetOperationalSettingsRequest create() =>
      GetOperationalSettingsRequest._();
  @$core.override
  GetOperationalSettingsRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetOperationalSettingsRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetOperationalSettingsRequest>(create);
  static GetOperationalSettingsRequest? _defaultInstance;
}

class GetOperationalSettingsResponse extends $pb.GeneratedMessage {
  factory GetOperationalSettingsResponse({
    $core.int? schemaVersion,
    $core.int? member,
    $core.Iterable<OperationalGroupSettings>? groups,
    SecurityVersion? version,
  }) {
    final result = create();
    if (schemaVersion != null) result.schemaVersion = schemaVersion;
    if (member != null) result.member = member;
    if (groups != null) result.groups.addAll(groups);
    if (version != null) result.version = version;
    return result;
  }

  GetOperationalSettingsResponse._();

  factory GetOperationalSettingsResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetOperationalSettingsResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetOperationalSettingsResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'schemaVersion',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(2, _omitFieldNames ? '' : 'member', fieldType: $pb.PbFieldType.OU3)
    ..pPM<OperationalGroupSettings>(3, _omitFieldNames ? '' : 'groups',
        subBuilder: OperationalGroupSettings.create)
    ..aOM<SecurityVersion>(4, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetOperationalSettingsResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetOperationalSettingsResponse copyWith(
          void Function(GetOperationalSettingsResponse) updates) =>
      super.copyWith(
              (message) => updates(message as GetOperationalSettingsResponse))
          as GetOperationalSettingsResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetOperationalSettingsResponse create() =>
      GetOperationalSettingsResponse._();
  @$core.override
  GetOperationalSettingsResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetOperationalSettingsResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetOperationalSettingsResponse>(create);
  static GetOperationalSettingsResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get schemaVersion => $_getIZ(0);
  @$pb.TagNumber(1)
  set schemaVersion($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSchemaVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearSchemaVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get member => $_getIZ(1);
  @$pb.TagNumber(2)
  set member($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasMember() => $_has(1);
  @$pb.TagNumber(2)
  void clearMember() => $_clearField(2);

  /// Exactly eight groups in the stable OperationalGroup ordering.
  @$pb.TagNumber(3)
  $pb.PbList<OperationalGroupSettings> get groups => $_getList(2);

  @$pb.TagNumber(4)
  SecurityVersion get version => $_getN(3);
  @$pb.TagNumber(4)
  set version(SecurityVersion value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasVersion() => $_has(3);
  @$pb.TagNumber(4)
  void clearVersion() => $_clearField(4);
  @$pb.TagNumber(4)
  SecurityVersion ensureVersion() => $_ensure(3);
}

/// Connected-node sample only; this does not predict cluster-wide deletions or
/// freeze the catalog for future scheduled backup production.
class OperationalBackupRetentionPreview extends $pb.GeneratedMessage {
  factory OperationalBackupRetentionPreview({
    $fixnum.Int64? validBackups,
    $fixnum.Int64? validBytes,
    $fixnum.Int64? pruneBackups,
    $fixnum.Int64? pruneBytes,
    $core.bool? complete,
    $core.String? fingerprint,
  }) {
    final result = create();
    if (validBackups != null) result.validBackups = validBackups;
    if (validBytes != null) result.validBytes = validBytes;
    if (pruneBackups != null) result.pruneBackups = pruneBackups;
    if (pruneBytes != null) result.pruneBytes = pruneBytes;
    if (complete != null) result.complete = complete;
    if (fingerprint != null) result.fingerprint = fingerprint;
    return result;
  }

  OperationalBackupRetentionPreview._();

  factory OperationalBackupRetentionPreview.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalBackupRetentionPreview.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalBackupRetentionPreview',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'validBackups', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'validBytes', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        3, _omitFieldNames ? '' : 'pruneBackups', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        4, _omitFieldNames ? '' : 'pruneBytes', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOB(5, _omitFieldNames ? '' : 'complete')
    ..aOS(6, _omitFieldNames ? '' : 'fingerprint')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalBackupRetentionPreview clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalBackupRetentionPreview copyWith(
          void Function(OperationalBackupRetentionPreview) updates) =>
      super.copyWith((message) =>
              updates(message as OperationalBackupRetentionPreview))
          as OperationalBackupRetentionPreview;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalBackupRetentionPreview create() =>
      OperationalBackupRetentionPreview._();
  @$core.override
  OperationalBackupRetentionPreview createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalBackupRetentionPreview getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalBackupRetentionPreview>(
          create);
  static OperationalBackupRetentionPreview? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get validBackups => $_getI64(0);
  @$pb.TagNumber(1)
  set validBackups($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasValidBackups() => $_has(0);
  @$pb.TagNumber(1)
  void clearValidBackups() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get validBytes => $_getI64(1);
  @$pb.TagNumber(2)
  set validBytes($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasValidBytes() => $_has(1);
  @$pb.TagNumber(2)
  void clearValidBytes() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get pruneBackups => $_getI64(2);
  @$pb.TagNumber(3)
  set pruneBackups($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasPruneBackups() => $_has(2);
  @$pb.TagNumber(3)
  void clearPruneBackups() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get pruneBytes => $_getI64(3);
  @$pb.TagNumber(4)
  set pruneBytes($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasPruneBytes() => $_has(3);
  @$pb.TagNumber(4)
  void clearPruneBytes() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.bool get complete => $_getBF(4);
  @$pb.TagNumber(5)
  set complete($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasComplete() => $_has(4);
  @$pb.TagNumber(5)
  void clearComplete() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.String get fingerprint => $_getSZ(5);
  @$pb.TagNumber(6)
  set fingerprint($core.String value) => $_setString(5, value);
  @$pb.TagNumber(6)
  $core.bool hasFingerprint() => $_has(5);
  @$pb.TagNumber(6)
  void clearFingerprint() => $_clearField(6);
}

class PreviewOperationalPolicyRequest extends $pb.GeneratedMessage {
  factory PreviewOperationalPolicyRequest({
    OperationalPolicyValues? values,
  }) {
    final result = create();
    if (values != null) result.values = values;
    return result;
  }

  PreviewOperationalPolicyRequest._();

  factory PreviewOperationalPolicyRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PreviewOperationalPolicyRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PreviewOperationalPolicyRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<OperationalPolicyValues>(1, _omitFieldNames ? '' : 'values',
        subBuilder: OperationalPolicyValues.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PreviewOperationalPolicyRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PreviewOperationalPolicyRequest copyWith(
          void Function(PreviewOperationalPolicyRequest) updates) =>
      super.copyWith(
              (message) => updates(message as PreviewOperationalPolicyRequest))
          as PreviewOperationalPolicyRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PreviewOperationalPolicyRequest create() =>
      PreviewOperationalPolicyRequest._();
  @$core.override
  PreviewOperationalPolicyRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PreviewOperationalPolicyRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PreviewOperationalPolicyRequest>(
          create);
  static PreviewOperationalPolicyRequest? _defaultInstance;

  @$pb.TagNumber(1)
  OperationalPolicyValues get values => $_getN(0);
  @$pb.TagNumber(1)
  set values(OperationalPolicyValues value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasValues() => $_has(0);
  @$pb.TagNumber(1)
  void clearValues() => $_clearField(1);
  @$pb.TagNumber(1)
  OperationalPolicyValues ensureValues() => $_ensure(0);
}

class PreviewOperationalPolicyResponse extends $pb.GeneratedMessage {
  factory PreviewOperationalPolicyResponse({
    $core.int? schemaVersion,
    OperationalGroup? group,
    $core.String? compatibilityFingerprint,
    SecurityVersion? version,
    OperationalBackupRetentionPreview? backupRetention,
    OperationalCapacityPreview? capacityUsage,
  }) {
    final result = create();
    if (schemaVersion != null) result.schemaVersion = schemaVersion;
    if (group != null) result.group = group;
    if (compatibilityFingerprint != null)
      result.compatibilityFingerprint = compatibilityFingerprint;
    if (version != null) result.version = version;
    if (backupRetention != null) result.backupRetention = backupRetention;
    if (capacityUsage != null) result.capacityUsage = capacityUsage;
    return result;
  }

  PreviewOperationalPolicyResponse._();

  factory PreviewOperationalPolicyResponse.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PreviewOperationalPolicyResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PreviewOperationalPolicyResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'schemaVersion',
        fieldType: $pb.PbFieldType.OU3)
    ..aE<OperationalGroup>(2, _omitFieldNames ? '' : 'group',
        enumValues: OperationalGroup.values)
    ..aOS(3, _omitFieldNames ? '' : 'compatibilityFingerprint')
    ..aOM<SecurityVersion>(4, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOM<OperationalBackupRetentionPreview>(
        5, _omitFieldNames ? '' : 'backupRetention',
        subBuilder: OperationalBackupRetentionPreview.create)
    ..aOM<OperationalCapacityPreview>(6, _omitFieldNames ? '' : 'capacityUsage',
        subBuilder: OperationalCapacityPreview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PreviewOperationalPolicyResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PreviewOperationalPolicyResponse copyWith(
          void Function(PreviewOperationalPolicyResponse) updates) =>
      super.copyWith(
              (message) => updates(message as PreviewOperationalPolicyResponse))
          as PreviewOperationalPolicyResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PreviewOperationalPolicyResponse create() =>
      PreviewOperationalPolicyResponse._();
  @$core.override
  PreviewOperationalPolicyResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PreviewOperationalPolicyResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PreviewOperationalPolicyResponse>(
          create);
  static PreviewOperationalPolicyResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get schemaVersion => $_getIZ(0);
  @$pb.TagNumber(1)
  set schemaVersion($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSchemaVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearSchemaVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  OperationalGroup get group => $_getN(1);
  @$pb.TagNumber(2)
  set group(OperationalGroup value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasGroup() => $_has(1);
  @$pb.TagNumber(2)
  void clearGroup() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get compatibilityFingerprint => $_getSZ(2);
  @$pb.TagNumber(3)
  set compatibilityFingerprint($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCompatibilityFingerprint() => $_has(2);
  @$pb.TagNumber(3)
  void clearCompatibilityFingerprint() => $_clearField(3);

  @$pb.TagNumber(4)
  SecurityVersion get version => $_getN(3);
  @$pb.TagNumber(4)
  set version(SecurityVersion value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasVersion() => $_has(3);
  @$pb.TagNumber(4)
  void clearVersion() => $_clearField(4);
  @$pb.TagNumber(4)
  SecurityVersion ensureVersion() => $_ensure(3);

  @$pb.TagNumber(5)
  OperationalBackupRetentionPreview get backupRetention => $_getN(4);
  @$pb.TagNumber(5)
  set backupRetention(OperationalBackupRetentionPreview value) =>
      $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasBackupRetention() => $_has(4);
  @$pb.TagNumber(5)
  void clearBackupRetention() => $_clearField(5);
  @$pb.TagNumber(5)
  OperationalBackupRetentionPreview ensureBackupRetention() => $_ensure(4);

  @$pb.TagNumber(6)
  OperationalCapacityPreview get capacityUsage => $_getN(5);
  @$pb.TagNumber(6)
  set capacityUsage(OperationalCapacityPreview value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasCapacityUsage() => $_has(5);
  @$pb.TagNumber(6)
  void clearCapacityUsage() => $_clearField(6);
  @$pb.TagNumber(6)
  OperationalCapacityPreview ensureCapacityUsage() => $_ensure(5);
}

enum SecurityChange_Operation {
  putIssuer,
  disableIssuer,
  deleteIssuer,
  putRole,
  deleteRole,
  putUser,
  deleteUser,
  putAssignment,
  deleteAssignment,
  revokeUserSessions,
  putRegistrationPolicy,
  putLogPolicy,
  putOperationalPolicy,
  putEdgePolicy,
  notSet
}

class SecurityChange extends $pb.GeneratedMessage {
  factory SecurityChange({
    SecurityIssuer? putIssuer,
    $core.String? disableIssuer,
    $core.String? deleteIssuer,
    SecurityRole? putRole,
    $core.String? deleteRole,
    SecurityUserStateChange? putUser,
    SecurityIdentity? deleteUser,
    SecurityRoleAssignment? putAssignment,
    SecurityRoleAssignment? deleteAssignment,
    SecurityIdentity? revokeUserSessions,
    SecurityRegistrationPolicy? putRegistrationPolicy,
    ManagedLogPolicy? putLogPolicy,
    OperationalPolicyChange? putOperationalPolicy,
    SecurityEdgePolicy? putEdgePolicy,
  }) {
    final result = create();
    if (putIssuer != null) result.putIssuer = putIssuer;
    if (disableIssuer != null) result.disableIssuer = disableIssuer;
    if (deleteIssuer != null) result.deleteIssuer = deleteIssuer;
    if (putRole != null) result.putRole = putRole;
    if (deleteRole != null) result.deleteRole = deleteRole;
    if (putUser != null) result.putUser = putUser;
    if (deleteUser != null) result.deleteUser = deleteUser;
    if (putAssignment != null) result.putAssignment = putAssignment;
    if (deleteAssignment != null) result.deleteAssignment = deleteAssignment;
    if (revokeUserSessions != null)
      result.revokeUserSessions = revokeUserSessions;
    if (putRegistrationPolicy != null)
      result.putRegistrationPolicy = putRegistrationPolicy;
    if (putLogPolicy != null) result.putLogPolicy = putLogPolicy;
    if (putOperationalPolicy != null)
      result.putOperationalPolicy = putOperationalPolicy;
    if (putEdgePolicy != null) result.putEdgePolicy = putEdgePolicy;
    return result;
  }

  SecurityChange._();

  factory SecurityChange.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityChange.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, SecurityChange_Operation>
      _SecurityChange_OperationByTag = {
    1: SecurityChange_Operation.putIssuer,
    2: SecurityChange_Operation.disableIssuer,
    3: SecurityChange_Operation.deleteIssuer,
    4: SecurityChange_Operation.putRole,
    5: SecurityChange_Operation.deleteRole,
    6: SecurityChange_Operation.putUser,
    7: SecurityChange_Operation.deleteUser,
    8: SecurityChange_Operation.putAssignment,
    9: SecurityChange_Operation.deleteAssignment,
    10: SecurityChange_Operation.revokeUserSessions,
    11: SecurityChange_Operation.putRegistrationPolicy,
    12: SecurityChange_Operation.putLogPolicy,
    13: SecurityChange_Operation.putOperationalPolicy,
    14: SecurityChange_Operation.putEdgePolicy,
    0: SecurityChange_Operation.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityChange',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14])
    ..aOM<SecurityIssuer>(1, _omitFieldNames ? '' : 'putIssuer',
        subBuilder: SecurityIssuer.create)
    ..aOS(2, _omitFieldNames ? '' : 'disableIssuer')
    ..aOS(3, _omitFieldNames ? '' : 'deleteIssuer')
    ..aOM<SecurityRole>(4, _omitFieldNames ? '' : 'putRole',
        subBuilder: SecurityRole.create)
    ..aOS(5, _omitFieldNames ? '' : 'deleteRole')
    ..aOM<SecurityUserStateChange>(6, _omitFieldNames ? '' : 'putUser',
        subBuilder: SecurityUserStateChange.create)
    ..aOM<SecurityIdentity>(7, _omitFieldNames ? '' : 'deleteUser',
        subBuilder: SecurityIdentity.create)
    ..aOM<SecurityRoleAssignment>(8, _omitFieldNames ? '' : 'putAssignment',
        subBuilder: SecurityRoleAssignment.create)
    ..aOM<SecurityRoleAssignment>(9, _omitFieldNames ? '' : 'deleteAssignment',
        subBuilder: SecurityRoleAssignment.create)
    ..aOM<SecurityIdentity>(10, _omitFieldNames ? '' : 'revokeUserSessions',
        subBuilder: SecurityIdentity.create)
    ..aOM<SecurityRegistrationPolicy>(
        11, _omitFieldNames ? '' : 'putRegistrationPolicy',
        subBuilder: SecurityRegistrationPolicy.create)
    ..aOM<ManagedLogPolicy>(12, _omitFieldNames ? '' : 'putLogPolicy',
        subBuilder: ManagedLogPolicy.create)
    ..aOM<OperationalPolicyChange>(
        13, _omitFieldNames ? '' : 'putOperationalPolicy',
        subBuilder: OperationalPolicyChange.create)
    ..aOM<SecurityEdgePolicy>(14, _omitFieldNames ? '' : 'putEdgePolicy',
        subBuilder: SecurityEdgePolicy.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityChange clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityChange copyWith(void Function(SecurityChange) updates) =>
      super.copyWith((message) => updates(message as SecurityChange))
          as SecurityChange;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityChange create() => SecurityChange._();
  @$core.override
  SecurityChange createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityChange getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityChange>(create);
  static SecurityChange? _defaultInstance;

  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  @$pb.TagNumber(3)
  @$pb.TagNumber(4)
  @$pb.TagNumber(5)
  @$pb.TagNumber(6)
  @$pb.TagNumber(7)
  @$pb.TagNumber(8)
  @$pb.TagNumber(9)
  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  SecurityChange_Operation whichOperation() =>
      _SecurityChange_OperationByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  @$pb.TagNumber(3)
  @$pb.TagNumber(4)
  @$pb.TagNumber(5)
  @$pb.TagNumber(6)
  @$pb.TagNumber(7)
  @$pb.TagNumber(8)
  @$pb.TagNumber(9)
  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  void clearOperation() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  SecurityIssuer get putIssuer => $_getN(0);
  @$pb.TagNumber(1)
  set putIssuer(SecurityIssuer value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasPutIssuer() => $_has(0);
  @$pb.TagNumber(1)
  void clearPutIssuer() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityIssuer ensurePutIssuer() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.String get disableIssuer => $_getSZ(1);
  @$pb.TagNumber(2)
  set disableIssuer($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDisableIssuer() => $_has(1);
  @$pb.TagNumber(2)
  void clearDisableIssuer() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get deleteIssuer => $_getSZ(2);
  @$pb.TagNumber(3)
  set deleteIssuer($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasDeleteIssuer() => $_has(2);
  @$pb.TagNumber(3)
  void clearDeleteIssuer() => $_clearField(3);

  @$pb.TagNumber(4)
  SecurityRole get putRole => $_getN(3);
  @$pb.TagNumber(4)
  set putRole(SecurityRole value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasPutRole() => $_has(3);
  @$pb.TagNumber(4)
  void clearPutRole() => $_clearField(4);
  @$pb.TagNumber(4)
  SecurityRole ensurePutRole() => $_ensure(3);

  @$pb.TagNumber(5)
  $core.String get deleteRole => $_getSZ(4);
  @$pb.TagNumber(5)
  set deleteRole($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasDeleteRole() => $_has(4);
  @$pb.TagNumber(5)
  void clearDeleteRole() => $_clearField(5);

  @$pb.TagNumber(6)
  SecurityUserStateChange get putUser => $_getN(5);
  @$pb.TagNumber(6)
  set putUser(SecurityUserStateChange value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasPutUser() => $_has(5);
  @$pb.TagNumber(6)
  void clearPutUser() => $_clearField(6);
  @$pb.TagNumber(6)
  SecurityUserStateChange ensurePutUser() => $_ensure(5);

  @$pb.TagNumber(7)
  SecurityIdentity get deleteUser => $_getN(6);
  @$pb.TagNumber(7)
  set deleteUser(SecurityIdentity value) => $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasDeleteUser() => $_has(6);
  @$pb.TagNumber(7)
  void clearDeleteUser() => $_clearField(7);
  @$pb.TagNumber(7)
  SecurityIdentity ensureDeleteUser() => $_ensure(6);

  @$pb.TagNumber(8)
  SecurityRoleAssignment get putAssignment => $_getN(7);
  @$pb.TagNumber(8)
  set putAssignment(SecurityRoleAssignment value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasPutAssignment() => $_has(7);
  @$pb.TagNumber(8)
  void clearPutAssignment() => $_clearField(8);
  @$pb.TagNumber(8)
  SecurityRoleAssignment ensurePutAssignment() => $_ensure(7);

  @$pb.TagNumber(9)
  SecurityRoleAssignment get deleteAssignment => $_getN(8);
  @$pb.TagNumber(9)
  set deleteAssignment(SecurityRoleAssignment value) => $_setField(9, value);
  @$pb.TagNumber(9)
  $core.bool hasDeleteAssignment() => $_has(8);
  @$pb.TagNumber(9)
  void clearDeleteAssignment() => $_clearField(9);
  @$pb.TagNumber(9)
  SecurityRoleAssignment ensureDeleteAssignment() => $_ensure(8);

  @$pb.TagNumber(10)
  SecurityIdentity get revokeUserSessions => $_getN(9);
  @$pb.TagNumber(10)
  set revokeUserSessions(SecurityIdentity value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasRevokeUserSessions() => $_has(9);
  @$pb.TagNumber(10)
  void clearRevokeUserSessions() => $_clearField(10);
  @$pb.TagNumber(10)
  SecurityIdentity ensureRevokeUserSessions() => $_ensure(9);

  @$pb.TagNumber(11)
  SecurityRegistrationPolicy get putRegistrationPolicy => $_getN(10);
  @$pb.TagNumber(11)
  set putRegistrationPolicy(SecurityRegistrationPolicy value) =>
      $_setField(11, value);
  @$pb.TagNumber(11)
  $core.bool hasPutRegistrationPolicy() => $_has(10);
  @$pb.TagNumber(11)
  void clearPutRegistrationPolicy() => $_clearField(11);
  @$pb.TagNumber(11)
  SecurityRegistrationPolicy ensurePutRegistrationPolicy() => $_ensure(10);

  @$pb.TagNumber(12)
  ManagedLogPolicy get putLogPolicy => $_getN(11);
  @$pb.TagNumber(12)
  set putLogPolicy(ManagedLogPolicy value) => $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasPutLogPolicy() => $_has(11);
  @$pb.TagNumber(12)
  void clearPutLogPolicy() => $_clearField(12);
  @$pb.TagNumber(12)
  ManagedLogPolicy ensurePutLogPolicy() => $_ensure(11);

  @$pb.TagNumber(13)
  OperationalPolicyChange get putOperationalPolicy => $_getN(12);
  @$pb.TagNumber(13)
  set putOperationalPolicy(OperationalPolicyChange value) =>
      $_setField(13, value);
  @$pb.TagNumber(13)
  $core.bool hasPutOperationalPolicy() => $_has(12);
  @$pb.TagNumber(13)
  void clearPutOperationalPolicy() => $_clearField(13);
  @$pb.TagNumber(13)
  OperationalPolicyChange ensurePutOperationalPolicy() => $_ensure(12);

  @$pb.TagNumber(14)
  SecurityEdgePolicy get putEdgePolicy => $_getN(13);
  @$pb.TagNumber(14)
  set putEdgePolicy(SecurityEdgePolicy value) => $_setField(14, value);
  @$pb.TagNumber(14)
  $core.bool hasPutEdgePolicy() => $_has(13);
  @$pb.TagNumber(14)
  void clearPutEdgePolicy() => $_clearField(14);
  @$pb.TagNumber(14)
  SecurityEdgePolicy ensurePutEdgePolicy() => $_ensure(13);
}

class ApplySecurityChangesRequest extends $pb.GeneratedMessage {
  factory ApplySecurityChangesRequest({
    $fixnum.Int64? expectedRevision,
    $core.List<$core.int>? changeId,
    $core.Iterable<SecurityChange>? changes,
    $core.List<$core.int>? authorizationProof,
    CurrentSecurityReview? currentReview,
  }) {
    final result = create();
    if (expectedRevision != null) result.expectedRevision = expectedRevision;
    if (changeId != null) result.changeId = changeId;
    if (changes != null) result.changes.addAll(changes);
    if (authorizationProof != null)
      result.authorizationProof = authorizationProof;
    if (currentReview != null) result.currentReview = currentReview;
    return result;
  }

  ApplySecurityChangesRequest._();

  factory ApplySecurityChangesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplySecurityChangesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplySecurityChangesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'expectedRevision', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..pPM<SecurityChange>(3, _omitFieldNames ? '' : 'changes',
        subBuilder: SecurityChange.create)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'authorizationProof', $pb.PbFieldType.OY)
    ..aOM<CurrentSecurityReview>(5, _omitFieldNames ? '' : 'currentReview',
        subBuilder: CurrentSecurityReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangesRequest copyWith(
          void Function(ApplySecurityChangesRequest) updates) =>
      super.copyWith(
              (message) => updates(message as ApplySecurityChangesRequest))
          as ApplySecurityChangesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangesRequest create() =>
      ApplySecurityChangesRequest._();
  @$core.override
  ApplySecurityChangesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplySecurityChangesRequest>(create);
  static ApplySecurityChangesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get expectedRevision => $_getI64(0);
  @$pb.TagNumber(1)
  set expectedRevision($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasExpectedRevision() => $_has(0);
  @$pb.TagNumber(1)
  void clearExpectedRevision() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get changeId => $_getN(1);
  @$pb.TagNumber(2)
  set changeId($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);

  @$pb.TagNumber(3)
  $pb.PbList<SecurityChange> get changes => $_getList(2);

  /// Purpose proof is excluded from retained canonical business intent.
  @$pb.TagNumber(4)
  $core.List<$core.int> get authorizationProof => $_getN(3);
  @$pb.TagNumber(4)
  set authorizationProof($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasAuthorizationProof() => $_has(3);
  @$pb.TagNumber(4)
  void clearAuthorizationProof() => $_clearField(4);

  /// Protocol 2: legacy expected_revision/change_id/changes must be absent.
  @$pb.TagNumber(5)
  CurrentSecurityReview get currentReview => $_getN(4);
  @$pb.TagNumber(5)
  set currentReview(CurrentSecurityReview value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentReview() => $_has(4);
  @$pb.TagNumber(5)
  void clearCurrentReview() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSecurityReview ensureCurrentReview() => $_ensure(4);
}

class ApplySecurityChangesResponse extends $pb.GeneratedMessage {
  factory ApplySecurityChangesResponse({
    SecurityVersion? version,
    $core.Iterable<$core.bool>? applied,
    $core.bool? replayed,
    SecurityEnforcementState? enforcement,
    CurrentSecurityChangeResult? currentResult,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (applied != null) result.applied.addAll(applied);
    if (replayed != null) result.replayed = replayed;
    if (enforcement != null) result.enforcement = enforcement;
    if (currentResult != null) result.currentResult = currentResult;
    return result;
  }

  ApplySecurityChangesResponse._();

  factory ApplySecurityChangesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplySecurityChangesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplySecurityChangesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityVersion>(1, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..p<$core.bool>(2, _omitFieldNames ? '' : 'applied', $pb.PbFieldType.KB)
    ..aOB(3, _omitFieldNames ? '' : 'replayed')
    ..aE<SecurityEnforcementState>(4, _omitFieldNames ? '' : 'enforcement',
        enumValues: SecurityEnforcementState.values)
    ..aOM<CurrentSecurityChangeResult>(
        5, _omitFieldNames ? '' : 'currentResult',
        subBuilder: CurrentSecurityChangeResult.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangesResponse copyWith(
          void Function(ApplySecurityChangesResponse) updates) =>
      super.copyWith(
              (message) => updates(message as ApplySecurityChangesResponse))
          as ApplySecurityChangesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangesResponse create() =>
      ApplySecurityChangesResponse._();
  @$core.override
  ApplySecurityChangesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplySecurityChangesResponse>(create);
  static ApplySecurityChangesResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityVersion get version => $_getN(0);
  @$pb.TagNumber(1)
  set version(SecurityVersion value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityVersion ensureVersion() => $_ensure(0);

  @$pb.TagNumber(2)
  $pb.PbList<$core.bool> get applied => $_getList(1);

  @$pb.TagNumber(3)
  $core.bool get replayed => $_getBF(2);
  @$pb.TagNumber(3)
  set replayed($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasReplayed() => $_has(2);
  @$pb.TagNumber(3)
  void clearReplayed() => $_clearField(3);

  @$pb.TagNumber(4)
  SecurityEnforcementState get enforcement => $_getN(3);
  @$pb.TagNumber(4)
  set enforcement(SecurityEnforcementState value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasEnforcement() => $_has(3);
  @$pb.TagNumber(4)
  void clearEnforcement() => $_clearField(4);

  @$pb.TagNumber(5)
  CurrentSecurityChangeResult get currentResult => $_getN(4);
  @$pb.TagNumber(5)
  set currentResult(CurrentSecurityChangeResult value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentResult() => $_has(4);
  @$pb.TagNumber(5)
  void clearCurrentResult() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(4);
}

class ApplySecurityChangeRequest extends $pb.GeneratedMessage {
  factory ApplySecurityChangeRequest({
    $fixnum.Int64? expectedRevision,
    $core.List<$core.int>? changeId,
    SecurityChange? change,
    $core.List<$core.int>? authorizationProof,
    CurrentSecurityReview? currentReview,
  }) {
    final result = create();
    if (expectedRevision != null) result.expectedRevision = expectedRevision;
    if (changeId != null) result.changeId = changeId;
    if (change != null) result.change = change;
    if (authorizationProof != null)
      result.authorizationProof = authorizationProof;
    if (currentReview != null) result.currentReview = currentReview;
    return result;
  }

  ApplySecurityChangeRequest._();

  factory ApplySecurityChangeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplySecurityChangeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplySecurityChangeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'expectedRevision', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..aOM<SecurityChange>(3, _omitFieldNames ? '' : 'change',
        subBuilder: SecurityChange.create)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'authorizationProof', $pb.PbFieldType.OY)
    ..aOM<CurrentSecurityReview>(5, _omitFieldNames ? '' : 'currentReview',
        subBuilder: CurrentSecurityReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangeRequest copyWith(
          void Function(ApplySecurityChangeRequest) updates) =>
      super.copyWith(
              (message) => updates(message as ApplySecurityChangeRequest))
          as ApplySecurityChangeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangeRequest create() => ApplySecurityChangeRequest._();
  @$core.override
  ApplySecurityChangeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplySecurityChangeRequest>(create);
  static ApplySecurityChangeRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get expectedRevision => $_getI64(0);
  @$pb.TagNumber(1)
  set expectedRevision($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasExpectedRevision() => $_has(0);
  @$pb.TagNumber(1)
  void clearExpectedRevision() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get changeId => $_getN(1);
  @$pb.TagNumber(2)
  set changeId($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);

  @$pb.TagNumber(3)
  SecurityChange get change => $_getN(2);
  @$pb.TagNumber(3)
  set change(SecurityChange value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasChange() => $_has(2);
  @$pb.TagNumber(3)
  void clearChange() => $_clearField(3);
  @$pb.TagNumber(3)
  SecurityChange ensureChange() => $_ensure(2);

  @$pb.TagNumber(4)
  $core.List<$core.int> get authorizationProof => $_getN(3);
  @$pb.TagNumber(4)
  set authorizationProof($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasAuthorizationProof() => $_has(3);
  @$pb.TagNumber(4)
  void clearAuthorizationProof() => $_clearField(4);

  /// Exactly one change; the same review/identity as the plural operation.
  @$pb.TagNumber(5)
  CurrentSecurityReview get currentReview => $_getN(4);
  @$pb.TagNumber(5)
  set currentReview(CurrentSecurityReview value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentReview() => $_has(4);
  @$pb.TagNumber(5)
  void clearCurrentReview() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSecurityReview ensureCurrentReview() => $_ensure(4);
}

class SecurityChangeReview extends $pb.GeneratedMessage {
  factory SecurityChangeReview({
    SecurityVersion? expectedVersion,
    $core.List<$core.int>? changeId,
    $core.Iterable<SecurityChange>? changes,
  }) {
    final result = create();
    if (expectedVersion != null) result.expectedVersion = expectedVersion;
    if (changeId != null) result.changeId = changeId;
    if (changes != null) result.changes.addAll(changes);
    return result;
  }

  SecurityChangeReview._();

  factory SecurityChangeReview.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityChangeReview.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityChangeReview',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityVersion>(1, _omitFieldNames ? '' : 'expectedVersion',
        subBuilder: SecurityVersion.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..pPM<SecurityChange>(3, _omitFieldNames ? '' : 'changes',
        subBuilder: SecurityChange.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityChangeReview clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityChangeReview copyWith(void Function(SecurityChangeReview) updates) =>
      super.copyWith((message) => updates(message as SecurityChangeReview))
          as SecurityChangeReview;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityChangeReview create() => SecurityChangeReview._();
  @$core.override
  SecurityChangeReview createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityChangeReview getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityChangeReview>(create);
  static SecurityChangeReview? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityVersion get expectedVersion => $_getN(0);
  @$pb.TagNumber(1)
  set expectedVersion(SecurityVersion value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasExpectedVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearExpectedVersion() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityVersion ensureExpectedVersion() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.List<$core.int> get changeId => $_getN(1);
  @$pb.TagNumber(2)
  set changeId($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);

  @$pb.TagNumber(3)
  $pb.PbList<SecurityChange> get changes => $_getList(2);
}

/// Apply error detail: this invocation was definitely refused before durable
/// persistence because operation authorization was missing/invalid/expired.
/// It does not settle any earlier invocation whose response was lost.
class SecurityOperationAuthorizationRequired extends $pb.GeneratedMessage {
  factory SecurityOperationAuthorizationRequired({
    $core.List<$core.int>? changeId,
    SecurityVersion? expectedVersion,
    $core.List<$core.int>? intentDigest,
  }) {
    final result = create();
    if (changeId != null) result.changeId = changeId;
    if (expectedVersion != null) result.expectedVersion = expectedVersion;
    if (intentDigest != null) result.intentDigest = intentDigest;
    return result;
  }

  SecurityOperationAuthorizationRequired._();

  factory SecurityOperationAuthorizationRequired.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityOperationAuthorizationRequired.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityOperationAuthorizationRequired',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'expectedVersion',
        subBuilder: SecurityVersion.create)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityOperationAuthorizationRequired clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityOperationAuthorizationRequired copyWith(
          void Function(SecurityOperationAuthorizationRequired) updates) =>
      super.copyWith((message) =>
              updates(message as SecurityOperationAuthorizationRequired))
          as SecurityOperationAuthorizationRequired;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityOperationAuthorizationRequired create() =>
      SecurityOperationAuthorizationRequired._();
  @$core.override
  SecurityOperationAuthorizationRequired createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityOperationAuthorizationRequired getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<
          SecurityOperationAuthorizationRequired>(create);
  static SecurityOperationAuthorizationRequired? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get changeId => $_getN(0);
  @$pb.TagNumber(1)
  set changeId($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasChangeId() => $_has(0);
  @$pb.TagNumber(1)
  void clearChangeId() => $_clearField(1);

  @$pb.TagNumber(2)
  SecurityVersion get expectedVersion => $_getN(1);
  @$pb.TagNumber(2)
  set expectedVersion(SecurityVersion value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasExpectedVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearExpectedVersion() => $_clearField(2);
  @$pb.TagNumber(2)
  SecurityVersion ensureExpectedVersion() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.List<$core.int> get intentDigest => $_getN(2);
  @$pb.TagNumber(3)
  set intentDigest($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasIntentDigest() => $_has(2);
  @$pb.TagNumber(3)
  void clearIntentDigest() => $_clearField(3);
}

/// Definitive refusal of this decoded Apply invocation before any persistence
/// attempt. This never settles earlier invocations of the same change ID.
class SecurityChangePrecommitRejected extends $pb.GeneratedMessage {
  factory SecurityChangePrecommitRejected({
    $core.List<$core.int>? changeId,
    $fixnum.Int64? expectedRevision,
    SecurityChangeRejectionReason? reason,
  }) {
    final result = create();
    if (changeId != null) result.changeId = changeId;
    if (expectedRevision != null) result.expectedRevision = expectedRevision;
    if (reason != null) result.reason = reason;
    return result;
  }

  SecurityChangePrecommitRejected._();

  factory SecurityChangePrecommitRejected.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityChangePrecommitRejected.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityChangePrecommitRejected',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'expectedRevision', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aE<SecurityChangeRejectionReason>(3, _omitFieldNames ? '' : 'reason',
        enumValues: SecurityChangeRejectionReason.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityChangePrecommitRejected clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityChangePrecommitRejected copyWith(
          void Function(SecurityChangePrecommitRejected) updates) =>
      super.copyWith(
              (message) => updates(message as SecurityChangePrecommitRejected))
          as SecurityChangePrecommitRejected;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityChangePrecommitRejected create() =>
      SecurityChangePrecommitRejected._();
  @$core.override
  SecurityChangePrecommitRejected createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SecurityChangePrecommitRejected getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityChangePrecommitRejected>(
          create);
  static SecurityChangePrecommitRejected? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get changeId => $_getN(0);
  @$pb.TagNumber(1)
  set changeId($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasChangeId() => $_has(0);
  @$pb.TagNumber(1)
  void clearChangeId() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get expectedRevision => $_getI64(1);
  @$pb.TagNumber(2)
  set expectedRevision($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasExpectedRevision() => $_has(1);
  @$pb.TagNumber(2)
  void clearExpectedRevision() => $_clearField(2);

  @$pb.TagNumber(3)
  SecurityChangeRejectionReason get reason => $_getN(2);
  @$pb.TagNumber(3)
  set reason(SecurityChangeRejectionReason value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasReason() => $_has(2);
  @$pb.TagNumber(3)
  void clearReason() => $_clearField(3);
}

class PrepareSecurityChangesRequest extends $pb.GeneratedMessage {
  factory PrepareSecurityChangesRequest({
    SecurityChangeReview? review,
    CurrentSecurityReview? currentReview,
  }) {
    final result = create();
    if (review != null) result.review = review;
    if (currentReview != null) result.currentReview = currentReview;
    return result;
  }

  PrepareSecurityChangesRequest._();

  factory PrepareSecurityChangesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PrepareSecurityChangesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PrepareSecurityChangesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityChangeReview>(1, _omitFieldNames ? '' : 'review',
        subBuilder: SecurityChangeReview.create)
    ..aOM<CurrentSecurityReview>(2, _omitFieldNames ? '' : 'currentReview',
        subBuilder: CurrentSecurityReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareSecurityChangesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareSecurityChangesRequest copyWith(
          void Function(PrepareSecurityChangesRequest) updates) =>
      super.copyWith(
              (message) => updates(message as PrepareSecurityChangesRequest))
          as PrepareSecurityChangesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PrepareSecurityChangesRequest create() =>
      PrepareSecurityChangesRequest._();
  @$core.override
  PrepareSecurityChangesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PrepareSecurityChangesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PrepareSecurityChangesRequest>(create);
  static PrepareSecurityChangesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityChangeReview get review => $_getN(0);
  @$pb.TagNumber(1)
  set review(SecurityChangeReview value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasReview() => $_has(0);
  @$pb.TagNumber(1)
  void clearReview() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityChangeReview ensureReview() => $_ensure(0);

  /// First preparation supplies profile/cut/changes only. The enrolled origin
  /// allocates the identity. A retained review resolves its original operation.
  @$pb.TagNumber(2)
  CurrentSecurityReview get currentReview => $_getN(1);
  @$pb.TagNumber(2)
  set currentReview(CurrentSecurityReview value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasCurrentReview() => $_has(1);
  @$pb.TagNumber(2)
  void clearCurrentReview() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentSecurityReview ensureCurrentReview() => $_ensure(1);
}

class PrepareSecurityChangesResponse extends $pb.GeneratedMessage {
  factory PrepareSecurityChangesResponse({
    SecurityVersion? expectedVersion,
    $core.List<$core.int>? changeId,
    $core.List<$core.int>? intentDigest,
    SecurityAuthorizationRequirement? requirement,
    GetSecurityChangeStatusResponse? retainedCommit,
    CurrentSecurityReview? currentReview,
    CurrentSecurityChangeResult? currentResult,
  }) {
    final result = create();
    if (expectedVersion != null) result.expectedVersion = expectedVersion;
    if (changeId != null) result.changeId = changeId;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (requirement != null) result.requirement = requirement;
    if (retainedCommit != null) result.retainedCommit = retainedCommit;
    if (currentReview != null) result.currentReview = currentReview;
    if (currentResult != null) result.currentResult = currentResult;
    return result;
  }

  PrepareSecurityChangesResponse._();

  factory PrepareSecurityChangesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PrepareSecurityChangesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PrepareSecurityChangesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityVersion>(1, _omitFieldNames ? '' : 'expectedVersion',
        subBuilder: SecurityVersion.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..aE<SecurityAuthorizationRequirement>(
        4, _omitFieldNames ? '' : 'requirement',
        enumValues: SecurityAuthorizationRequirement.values)
    ..aOM<GetSecurityChangeStatusResponse>(
        5, _omitFieldNames ? '' : 'retainedCommit',
        subBuilder: GetSecurityChangeStatusResponse.create)
    ..aOM<CurrentSecurityReview>(6, _omitFieldNames ? '' : 'currentReview',
        subBuilder: CurrentSecurityReview.create)
    ..aOM<CurrentSecurityChangeResult>(
        7, _omitFieldNames ? '' : 'currentResult',
        subBuilder: CurrentSecurityChangeResult.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareSecurityChangesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PrepareSecurityChangesResponse copyWith(
          void Function(PrepareSecurityChangesResponse) updates) =>
      super.copyWith(
              (message) => updates(message as PrepareSecurityChangesResponse))
          as PrepareSecurityChangesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PrepareSecurityChangesResponse create() =>
      PrepareSecurityChangesResponse._();
  @$core.override
  PrepareSecurityChangesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PrepareSecurityChangesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PrepareSecurityChangesResponse>(create);
  static PrepareSecurityChangesResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityVersion get expectedVersion => $_getN(0);
  @$pb.TagNumber(1)
  set expectedVersion(SecurityVersion value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasExpectedVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearExpectedVersion() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityVersion ensureExpectedVersion() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.List<$core.int> get changeId => $_getN(1);
  @$pb.TagNumber(2)
  set changeId($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get intentDigest => $_getN(2);
  @$pb.TagNumber(3)
  set intentDigest($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasIntentDigest() => $_has(2);
  @$pb.TagNumber(3)
  void clearIntentDigest() => $_clearField(3);

  @$pb.TagNumber(4)
  SecurityAuthorizationRequirement get requirement => $_getN(3);
  @$pb.TagNumber(4)
  set requirement(SecurityAuthorizationRequirement value) =>
      $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasRequirement() => $_has(3);
  @$pb.TagNumber(4)
  void clearRequirement() => $_clearField(4);

  /// A known committed ID is recovered using its original retained proof.
  @$pb.TagNumber(5)
  GetSecurityChangeStatusResponse get retainedCommit => $_getN(4);
  @$pb.TagNumber(5)
  set retainedCommit(GetSecurityChangeStatusResponse value) =>
      $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasRetainedCommit() => $_has(4);
  @$pb.TagNumber(5)
  void clearRetainedCommit() => $_clearField(5);
  @$pb.TagNumber(5)
  GetSecurityChangeStatusResponse ensureRetainedCommit() => $_ensure(4);

  @$pb.TagNumber(6)
  CurrentSecurityReview get currentReview => $_getN(5);
  @$pb.TagNumber(6)
  set currentReview(CurrentSecurityReview value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasCurrentReview() => $_has(5);
  @$pb.TagNumber(6)
  void clearCurrentReview() => $_clearField(6);
  @$pb.TagNumber(6)
  CurrentSecurityReview ensureCurrentReview() => $_ensure(5);

  @$pb.TagNumber(7)
  CurrentSecurityChangeResult get currentResult => $_getN(6);
  @$pb.TagNumber(7)
  set currentResult(CurrentSecurityChangeResult value) => $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasCurrentResult() => $_has(6);
  @$pb.TagNumber(7)
  void clearCurrentResult() => $_clearField(7);
  @$pb.TagNumber(7)
  CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(6);
}

class BeginSecurityChangeAuthorizationRequest extends $pb.GeneratedMessage {
  factory BeginSecurityChangeAuthorizationRequest({
    SecurityChangeReview? review,
    CurrentSecurityReview? currentReview,
  }) {
    final result = create();
    if (review != null) result.review = review;
    if (currentReview != null) result.currentReview = currentReview;
    return result;
  }

  BeginSecurityChangeAuthorizationRequest._();

  factory BeginSecurityChangeAuthorizationRequest.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BeginSecurityChangeAuthorizationRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BeginSecurityChangeAuthorizationRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityChangeReview>(1, _omitFieldNames ? '' : 'review',
        subBuilder: SecurityChangeReview.create)
    ..aOM<CurrentSecurityReview>(2, _omitFieldNames ? '' : 'currentReview',
        subBuilder: CurrentSecurityReview.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginSecurityChangeAuthorizationRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginSecurityChangeAuthorizationRequest copyWith(
          void Function(BeginSecurityChangeAuthorizationRequest) updates) =>
      super.copyWith((message) =>
              updates(message as BeginSecurityChangeAuthorizationRequest))
          as BeginSecurityChangeAuthorizationRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BeginSecurityChangeAuthorizationRequest create() =>
      BeginSecurityChangeAuthorizationRequest._();
  @$core.override
  BeginSecurityChangeAuthorizationRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BeginSecurityChangeAuthorizationRequest getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<
          BeginSecurityChangeAuthorizationRequest>(create);
  static BeginSecurityChangeAuthorizationRequest? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityChangeReview get review => $_getN(0);
  @$pb.TagNumber(1)
  set review(SecurityChangeReview value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasReview() => $_has(0);
  @$pb.TagNumber(1)
  void clearReview() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityChangeReview ensureReview() => $_ensure(0);

  @$pb.TagNumber(2)
  CurrentSecurityReview get currentReview => $_getN(1);
  @$pb.TagNumber(2)
  set currentReview(CurrentSecurityReview value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasCurrentReview() => $_has(1);
  @$pb.TagNumber(2)
  void clearCurrentReview() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentSecurityReview ensureCurrentReview() => $_ensure(1);
}

class BeginSecurityChangeAuthorizationResponse extends $pb.GeneratedMessage {
  factory BeginSecurityChangeAuthorizationResponse({
    $core.List<$core.int>? authorizationId,
    $core.String? startUrl,
    $1.Timestamp? expiresAt,
    CurrentAuthorityProfile? currentProfile,
    $core.String? attemptAffinity,
  }) {
    final result = create();
    if (authorizationId != null) result.authorizationId = authorizationId;
    if (startUrl != null) result.startUrl = startUrl;
    if (expiresAt != null) result.expiresAt = expiresAt;
    if (currentProfile != null) result.currentProfile = currentProfile;
    if (attemptAffinity != null) result.attemptAffinity = attemptAffinity;
    return result;
  }

  BeginSecurityChangeAuthorizationResponse._();

  factory BeginSecurityChangeAuthorizationResponse.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BeginSecurityChangeAuthorizationResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BeginSecurityChangeAuthorizationResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'authorizationId', $pb.PbFieldType.OY)
    ..aOS(2, _omitFieldNames ? '' : 'startUrl')
    ..aOM<$1.Timestamp>(3, _omitFieldNames ? '' : 'expiresAt',
        subBuilder: $1.Timestamp.create)
    ..aOM<CurrentAuthorityProfile>(4, _omitFieldNames ? '' : 'currentProfile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOS(5, _omitFieldNames ? '' : 'attemptAffinity')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginSecurityChangeAuthorizationResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BeginSecurityChangeAuthorizationResponse copyWith(
          void Function(BeginSecurityChangeAuthorizationResponse) updates) =>
      super.copyWith((message) =>
              updates(message as BeginSecurityChangeAuthorizationResponse))
          as BeginSecurityChangeAuthorizationResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BeginSecurityChangeAuthorizationResponse create() =>
      BeginSecurityChangeAuthorizationResponse._();
  @$core.override
  BeginSecurityChangeAuthorizationResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BeginSecurityChangeAuthorizationResponse getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<
          BeginSecurityChangeAuthorizationResponse>(create);
  static BeginSecurityChangeAuthorizationResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get authorizationId => $_getN(0);
  @$pb.TagNumber(1)
  set authorizationId($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAuthorizationId() => $_has(0);
  @$pb.TagNumber(1)
  void clearAuthorizationId() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get startUrl => $_getSZ(1);
  @$pb.TagNumber(2)
  set startUrl($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasStartUrl() => $_has(1);
  @$pb.TagNumber(2)
  void clearStartUrl() => $_clearField(2);

  @$pb.TagNumber(3)
  $1.Timestamp get expiresAt => $_getN(2);
  @$pb.TagNumber(3)
  set expiresAt($1.Timestamp value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasExpiresAt() => $_has(2);
  @$pb.TagNumber(3)
  void clearExpiresAt() => $_clearField(3);
  @$pb.TagNumber(3)
  $1.Timestamp ensureExpiresAt() => $_ensure(2);

  @$pb.TagNumber(4)
  CurrentAuthorityProfile get currentProfile => $_getN(3);
  @$pb.TagNumber(4)
  set currentProfile(CurrentAuthorityProfile value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasCurrentProfile() => $_has(3);
  @$pb.TagNumber(4)
  void clearCurrentProfile() => $_clearField(4);
  @$pb.TagNumber(4)
  CurrentAuthorityProfile ensureCurrentProfile() => $_ensure(3);

  /// Opaque, process/attempt-bound routing hint, not authentication authority.
  @$pb.TagNumber(5)
  $core.String get attemptAffinity => $_getSZ(4);
  @$pb.TagNumber(5)
  set attemptAffinity($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasAttemptAffinity() => $_has(4);
  @$pb.TagNumber(5)
  void clearAttemptAffinity() => $_clearField(5);
}

class GetSecurityChangeAuthorizationRequest extends $pb.GeneratedMessage {
  factory GetSecurityChangeAuthorizationRequest({
    $core.List<$core.int>? authorizationId,
    CurrentAuthorityProfile? currentProfile,
    $core.String? attemptAffinity,
  }) {
    final result = create();
    if (authorizationId != null) result.authorizationId = authorizationId;
    if (currentProfile != null) result.currentProfile = currentProfile;
    if (attemptAffinity != null) result.attemptAffinity = attemptAffinity;
    return result;
  }

  GetSecurityChangeAuthorizationRequest._();

  factory GetSecurityChangeAuthorizationRequest.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetSecurityChangeAuthorizationRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetSecurityChangeAuthorizationRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'authorizationId', $pb.PbFieldType.OY)
    ..aOM<CurrentAuthorityProfile>(2, _omitFieldNames ? '' : 'currentProfile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOS(3, _omitFieldNames ? '' : 'attemptAffinity')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeAuthorizationRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeAuthorizationRequest copyWith(
          void Function(GetSecurityChangeAuthorizationRequest) updates) =>
      super.copyWith((message) =>
              updates(message as GetSecurityChangeAuthorizationRequest))
          as GetSecurityChangeAuthorizationRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeAuthorizationRequest create() =>
      GetSecurityChangeAuthorizationRequest._();
  @$core.override
  GetSecurityChangeAuthorizationRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeAuthorizationRequest getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<
          GetSecurityChangeAuthorizationRequest>(create);
  static GetSecurityChangeAuthorizationRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get authorizationId => $_getN(0);
  @$pb.TagNumber(1)
  set authorizationId($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAuthorizationId() => $_has(0);
  @$pb.TagNumber(1)
  void clearAuthorizationId() => $_clearField(1);

  @$pb.TagNumber(2)
  CurrentAuthorityProfile get currentProfile => $_getN(1);
  @$pb.TagNumber(2)
  set currentProfile(CurrentAuthorityProfile value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasCurrentProfile() => $_has(1);
  @$pb.TagNumber(2)
  void clearCurrentProfile() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentAuthorityProfile ensureCurrentProfile() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.String get attemptAffinity => $_getSZ(2);
  @$pb.TagNumber(3)
  set attemptAffinity($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasAttemptAffinity() => $_has(2);
  @$pb.TagNumber(3)
  void clearAttemptAffinity() => $_clearField(3);
}

class GetSecurityChangeAuthorizationResponse extends $pb.GeneratedMessage {
  factory GetSecurityChangeAuthorizationResponse({
    $core.List<$core.int>? authorizationId,
    SecurityAuthorizationState? state,
    $core.List<$core.int>? authorizationProof,
    $1.Timestamp? expiresAt,
    CurrentAuthorityProfile? currentProfile,
  }) {
    final result = create();
    if (authorizationId != null) result.authorizationId = authorizationId;
    if (state != null) result.state = state;
    if (authorizationProof != null)
      result.authorizationProof = authorizationProof;
    if (expiresAt != null) result.expiresAt = expiresAt;
    if (currentProfile != null) result.currentProfile = currentProfile;
    return result;
  }

  GetSecurityChangeAuthorizationResponse._();

  factory GetSecurityChangeAuthorizationResponse.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetSecurityChangeAuthorizationResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetSecurityChangeAuthorizationResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'authorizationId', $pb.PbFieldType.OY)
    ..aE<SecurityAuthorizationState>(2, _omitFieldNames ? '' : 'state',
        enumValues: SecurityAuthorizationState.values)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'authorizationProof', $pb.PbFieldType.OY)
    ..aOM<$1.Timestamp>(4, _omitFieldNames ? '' : 'expiresAt',
        subBuilder: $1.Timestamp.create)
    ..aOM<CurrentAuthorityProfile>(5, _omitFieldNames ? '' : 'currentProfile',
        subBuilder: CurrentAuthorityProfile.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeAuthorizationResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeAuthorizationResponse copyWith(
          void Function(GetSecurityChangeAuthorizationResponse) updates) =>
      super.copyWith((message) =>
              updates(message as GetSecurityChangeAuthorizationResponse))
          as GetSecurityChangeAuthorizationResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeAuthorizationResponse create() =>
      GetSecurityChangeAuthorizationResponse._();
  @$core.override
  GetSecurityChangeAuthorizationResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeAuthorizationResponse getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<
          GetSecurityChangeAuthorizationResponse>(create);
  static GetSecurityChangeAuthorizationResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get authorizationId => $_getN(0);
  @$pb.TagNumber(1)
  set authorizationId($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAuthorizationId() => $_has(0);
  @$pb.TagNumber(1)
  void clearAuthorizationId() => $_clearField(1);

  @$pb.TagNumber(2)
  SecurityAuthorizationState get state => $_getN(1);
  @$pb.TagNumber(2)
  set state(SecurityAuthorizationState value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasState() => $_has(1);
  @$pb.TagNumber(2)
  void clearState() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get authorizationProof => $_getN(2);
  @$pb.TagNumber(3)
  set authorizationProof($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasAuthorizationProof() => $_has(2);
  @$pb.TagNumber(3)
  void clearAuthorizationProof() => $_clearField(3);

  @$pb.TagNumber(4)
  $1.Timestamp get expiresAt => $_getN(3);
  @$pb.TagNumber(4)
  set expiresAt($1.Timestamp value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasExpiresAt() => $_has(3);
  @$pb.TagNumber(4)
  void clearExpiresAt() => $_clearField(4);
  @$pb.TagNumber(4)
  $1.Timestamp ensureExpiresAt() => $_ensure(3);

  @$pb.TagNumber(5)
  CurrentAuthorityProfile get currentProfile => $_getN(4);
  @$pb.TagNumber(5)
  set currentProfile(CurrentAuthorityProfile value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentProfile() => $_has(4);
  @$pb.TagNumber(5)
  void clearCurrentProfile() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentAuthorityProfile ensureCurrentProfile() => $_ensure(4);
}

class ApplySecurityChangeResponse extends $pb.GeneratedMessage {
  factory ApplySecurityChangeResponse({
    SecurityVersion? version,
    $core.bool? applied,
    $core.bool? replayed,
    SecurityEnforcementState? enforcement,
    CurrentSecurityChangeResult? currentResult,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (applied != null) result.applied = applied;
    if (replayed != null) result.replayed = replayed;
    if (enforcement != null) result.enforcement = enforcement;
    if (currentResult != null) result.currentResult = currentResult;
    return result;
  }

  ApplySecurityChangeResponse._();

  factory ApplySecurityChangeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ApplySecurityChangeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ApplySecurityChangeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityVersion>(1, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOB(2, _omitFieldNames ? '' : 'applied')
    ..aOB(3, _omitFieldNames ? '' : 'replayed')
    ..aE<SecurityEnforcementState>(4, _omitFieldNames ? '' : 'enforcement',
        enumValues: SecurityEnforcementState.values)
    ..aOM<CurrentSecurityChangeResult>(
        5, _omitFieldNames ? '' : 'currentResult',
        subBuilder: CurrentSecurityChangeResult.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangeResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangeResponse copyWith(
          void Function(ApplySecurityChangeResponse) updates) =>
      super.copyWith(
              (message) => updates(message as ApplySecurityChangeResponse))
          as ApplySecurityChangeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangeResponse create() =>
      ApplySecurityChangeResponse._();
  @$core.override
  ApplySecurityChangeResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ApplySecurityChangeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ApplySecurityChangeResponse>(create);
  static ApplySecurityChangeResponse? _defaultInstance;

  @$pb.TagNumber(1)
  SecurityVersion get version => $_getN(0);
  @$pb.TagNumber(1)
  set version(SecurityVersion value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityVersion ensureVersion() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.bool get applied => $_getBF(1);
  @$pb.TagNumber(2)
  set applied($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasApplied() => $_has(1);
  @$pb.TagNumber(2)
  void clearApplied() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get replayed => $_getBF(2);
  @$pb.TagNumber(3)
  set replayed($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasReplayed() => $_has(2);
  @$pb.TagNumber(3)
  void clearReplayed() => $_clearField(3);

  @$pb.TagNumber(4)
  SecurityEnforcementState get enforcement => $_getN(3);
  @$pb.TagNumber(4)
  set enforcement(SecurityEnforcementState value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasEnforcement() => $_has(3);
  @$pb.TagNumber(4)
  void clearEnforcement() => $_clearField(4);

  @$pb.TagNumber(5)
  CurrentSecurityChangeResult get currentResult => $_getN(4);
  @$pb.TagNumber(5)
  set currentResult(CurrentSecurityChangeResult value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentResult() => $_has(4);
  @$pb.TagNumber(5)
  void clearCurrentResult() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(4);
}

class GetSecurityChangeStatusRequest extends $pb.GeneratedMessage {
  factory GetSecurityChangeStatusRequest({
    $core.List<$core.int>? changeId,
    CurrentSecurityChangeID? currentChangeId,
    CurrentAuthorityProfile? currentProfile,
    $core.List<$core.int>? currentIntentDigest,
  }) {
    final result = create();
    if (changeId != null) result.changeId = changeId;
    if (currentChangeId != null) result.currentChangeId = currentChangeId;
    if (currentProfile != null) result.currentProfile = currentProfile;
    if (currentIntentDigest != null)
      result.currentIntentDigest = currentIntentDigest;
    return result;
  }

  GetSecurityChangeStatusRequest._();

  factory GetSecurityChangeStatusRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetSecurityChangeStatusRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetSecurityChangeStatusRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..aOM<CurrentSecurityChangeID>(2, _omitFieldNames ? '' : 'currentChangeId',
        subBuilder: CurrentSecurityChangeID.create)
    ..aOM<CurrentAuthorityProfile>(3, _omitFieldNames ? '' : 'currentProfile',
        subBuilder: CurrentAuthorityProfile.create)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'currentIntentDigest', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeStatusRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeStatusRequest copyWith(
          void Function(GetSecurityChangeStatusRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetSecurityChangeStatusRequest))
          as GetSecurityChangeStatusRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeStatusRequest create() =>
      GetSecurityChangeStatusRequest._();
  @$core.override
  GetSecurityChangeStatusRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeStatusRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetSecurityChangeStatusRequest>(create);
  static GetSecurityChangeStatusRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get changeId => $_getN(0);
  @$pb.TagNumber(1)
  set changeId($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasChangeId() => $_has(0);
  @$pb.TagNumber(1)
  void clearChangeId() => $_clearField(1);

  @$pb.TagNumber(2)
  CurrentSecurityChangeID get currentChangeId => $_getN(1);
  @$pb.TagNumber(2)
  set currentChangeId(CurrentSecurityChangeID value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasCurrentChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearCurrentChangeId() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentSecurityChangeID ensureCurrentChangeId() => $_ensure(1);

  @$pb.TagNumber(3)
  CurrentAuthorityProfile get currentProfile => $_getN(2);
  @$pb.TagNumber(3)
  set currentProfile(CurrentAuthorityProfile value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasCurrentProfile() => $_has(2);
  @$pb.TagNumber(3)
  void clearCurrentProfile() => $_clearField(3);
  @$pb.TagNumber(3)
  CurrentAuthorityProfile ensureCurrentProfile() => $_ensure(2);

  @$pb.TagNumber(4)
  $core.List<$core.int> get currentIntentDigest => $_getN(3);
  @$pb.TagNumber(4)
  set currentIntentDigest($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasCurrentIntentDigest() => $_has(3);
  @$pb.TagNumber(4)
  void clearCurrentIntentDigest() => $_clearField(4);
}

class GetSecurityChangeStatusResponse extends $pb.GeneratedMessage {
  factory GetSecurityChangeStatusResponse({
    SecurityVersion? version,
    $core.List<$core.int>? changeId,
    SecurityEnforcementState? enforcement,
    CurrentSecurityChangeResult? currentResult,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (changeId != null) result.changeId = changeId;
    if (enforcement != null) result.enforcement = enforcement;
    if (currentResult != null) result.currentResult = currentResult;
    return result;
  }

  GetSecurityChangeStatusResponse._();

  factory GetSecurityChangeStatusResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetSecurityChangeStatusResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetSecurityChangeStatusResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<SecurityVersion>(1, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'changeId', $pb.PbFieldType.OY)
    ..aE<SecurityEnforcementState>(4, _omitFieldNames ? '' : 'enforcement',
        enumValues: SecurityEnforcementState.values)
    ..aOM<CurrentSecurityChangeResult>(
        5, _omitFieldNames ? '' : 'currentResult',
        subBuilder: CurrentSecurityChangeResult.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeStatusResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeStatusResponse copyWith(
          void Function(GetSecurityChangeStatusResponse) updates) =>
      super.copyWith(
              (message) => updates(message as GetSecurityChangeStatusResponse))
          as GetSecurityChangeStatusResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeStatusResponse create() =>
      GetSecurityChangeStatusResponse._();
  @$core.override
  GetSecurityChangeStatusResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeStatusResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetSecurityChangeStatusResponse>(
          create);
  static GetSecurityChangeStatusResponse? _defaultInstance;

  /// The original retained commit, not a snapshot of currently effective policy.
  @$pb.TagNumber(1)
  SecurityVersion get version => $_getN(0);
  @$pb.TagNumber(1)
  set version(SecurityVersion value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);
  @$pb.TagNumber(1)
  SecurityVersion ensureVersion() => $_ensure(0);

  /// Echoes the exact requested immutable ID. Unknown or retired IDs are indeterminate.
  @$pb.TagNumber(2)
  $core.List<$core.int> get changeId => $_getN(1);
  @$pb.TagNumber(2)
  set changeId($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);

  @$pb.TagNumber(4)
  SecurityEnforcementState get enforcement => $_getN(2);
  @$pb.TagNumber(4)
  set enforcement(SecurityEnforcementState value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasEnforcement() => $_has(2);
  @$pb.TagNumber(4)
  void clearEnforcement() => $_clearField(4);

  @$pb.TagNumber(5)
  CurrentSecurityChangeResult get currentResult => $_getN(3);
  @$pb.TagNumber(5)
  set currentResult(CurrentSecurityChangeResult value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasCurrentResult() => $_has(3);
  @$pb.TagNumber(5)
  void clearCurrentResult() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSecurityChangeResult ensureCurrentResult() => $_ensure(3);
}

/// Public current authority protocol 2. These are identity/status data only;
/// replay/current authority is always verified independently by the owner.
class CurrentAuthorityProfile extends $pb.GeneratedMessage {
  factory CurrentAuthorityProfile({
    $core.int? version,
    $core.List<$core.int>? domain,
    $core.List<$core.int>? cohort,
    $core.List<$core.int>? generation,
    $core.List<$core.int>? protocol,
    $core.List<$core.int>? timeProfile,
    $core.List<$core.int>? membership,
    $core.List<$core.int>? configuration,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (domain != null) result.domain = domain;
    if (cohort != null) result.cohort = cohort;
    if (generation != null) result.generation = generation;
    if (protocol != null) result.protocol = protocol;
    if (timeProfile != null) result.timeProfile = timeProfile;
    if (membership != null) result.membership = membership;
    if (configuration != null) result.configuration = configuration;
    return result;
  }

  CurrentAuthorityProfile._();

  factory CurrentAuthorityProfile.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentAuthorityProfile.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentAuthorityProfile',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'domain', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'cohort', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'generation', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        5, _omitFieldNames ? '' : 'protocol', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        6, _omitFieldNames ? '' : 'timeProfile', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        7, _omitFieldNames ? '' : 'membership', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        8, _omitFieldNames ? '' : 'configuration', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentAuthorityProfile clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentAuthorityProfile copyWith(
          void Function(CurrentAuthorityProfile) updates) =>
      super.copyWith((message) => updates(message as CurrentAuthorityProfile))
          as CurrentAuthorityProfile;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentAuthorityProfile create() => CurrentAuthorityProfile._();
  @$core.override
  CurrentAuthorityProfile createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentAuthorityProfile getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentAuthorityProfile>(create);
  static CurrentAuthorityProfile? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get domain => $_getN(1);
  @$pb.TagNumber(2)
  set domain($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDomain() => $_has(1);
  @$pb.TagNumber(2)
  void clearDomain() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get cohort => $_getN(2);
  @$pb.TagNumber(3)
  set cohort($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCohort() => $_has(2);
  @$pb.TagNumber(3)
  void clearCohort() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.List<$core.int> get generation => $_getN(3);
  @$pb.TagNumber(4)
  set generation($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasGeneration() => $_has(3);
  @$pb.TagNumber(4)
  void clearGeneration() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.List<$core.int> get protocol => $_getN(4);
  @$pb.TagNumber(5)
  set protocol($core.List<$core.int> value) => $_setBytes(4, value);
  @$pb.TagNumber(5)
  $core.bool hasProtocol() => $_has(4);
  @$pb.TagNumber(5)
  void clearProtocol() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.List<$core.int> get timeProfile => $_getN(5);
  @$pb.TagNumber(6)
  set timeProfile($core.List<$core.int> value) => $_setBytes(5, value);
  @$pb.TagNumber(6)
  $core.bool hasTimeProfile() => $_has(5);
  @$pb.TagNumber(6)
  void clearTimeProfile() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.List<$core.int> get membership => $_getN(6);
  @$pb.TagNumber(7)
  set membership($core.List<$core.int> value) => $_setBytes(6, value);
  @$pb.TagNumber(7)
  $core.bool hasMembership() => $_has(6);
  @$pb.TagNumber(7)
  void clearMembership() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.List<$core.int> get configuration => $_getN(7);
  @$pb.TagNumber(8)
  set configuration($core.List<$core.int> value) => $_setBytes(7, value);
  @$pb.TagNumber(8)
  $core.bool hasConfiguration() => $_has(7);
  @$pb.TagNumber(8)
  void clearConfiguration() => $_clearField(8);
}

class CurrentSemanticCut extends $pb.GeneratedMessage {
  factory CurrentSemanticCut({
    $core.int? version,
    $core.List<$core.int>? domain,
    $core.List<$core.int>? cohort,
    $core.List<$core.int>? generation,
    $fixnum.Int64? sequence,
    $core.List<$core.int>? previous,
    $core.List<$core.int>? projection,
    $core.List<$core.int>? frontier,
    $core.List<$core.int>? fences,
    $core.List<$core.int>? policy,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (domain != null) result.domain = domain;
    if (cohort != null) result.cohort = cohort;
    if (generation != null) result.generation = generation;
    if (sequence != null) result.sequence = sequence;
    if (previous != null) result.previous = previous;
    if (projection != null) result.projection = projection;
    if (frontier != null) result.frontier = frontier;
    if (fences != null) result.fences = fences;
    if (policy != null) result.policy = policy;
    return result;
  }

  CurrentSemanticCut._();

  factory CurrentSemanticCut.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSemanticCut.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSemanticCut',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'domain', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'cohort', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'generation', $pb.PbFieldType.OY)
    ..a<$fixnum.Int64>(
        5, _omitFieldNames ? '' : 'sequence', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$core.List<$core.int>>(
        6, _omitFieldNames ? '' : 'previous', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        7, _omitFieldNames ? '' : 'projection', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        8, _omitFieldNames ? '' : 'frontier', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        9, _omitFieldNames ? '' : 'fences', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        10, _omitFieldNames ? '' : 'policy', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSemanticCut clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSemanticCut copyWith(void Function(CurrentSemanticCut) updates) =>
      super.copyWith((message) => updates(message as CurrentSemanticCut))
          as CurrentSemanticCut;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSemanticCut create() => CurrentSemanticCut._();
  @$core.override
  CurrentSemanticCut createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSemanticCut getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSemanticCut>(create);
  static CurrentSemanticCut? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get domain => $_getN(1);
  @$pb.TagNumber(2)
  set domain($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDomain() => $_has(1);
  @$pb.TagNumber(2)
  void clearDomain() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get cohort => $_getN(2);
  @$pb.TagNumber(3)
  set cohort($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCohort() => $_has(2);
  @$pb.TagNumber(3)
  void clearCohort() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.List<$core.int> get generation => $_getN(3);
  @$pb.TagNumber(4)
  set generation($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasGeneration() => $_has(3);
  @$pb.TagNumber(4)
  void clearGeneration() => $_clearField(4);

  @$pb.TagNumber(5)
  $fixnum.Int64 get sequence => $_getI64(4);
  @$pb.TagNumber(5)
  set sequence($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasSequence() => $_has(4);
  @$pb.TagNumber(5)
  void clearSequence() => $_clearField(5);

  @$pb.TagNumber(6)
  $core.List<$core.int> get previous => $_getN(5);
  @$pb.TagNumber(6)
  set previous($core.List<$core.int> value) => $_setBytes(5, value);
  @$pb.TagNumber(6)
  $core.bool hasPrevious() => $_has(5);
  @$pb.TagNumber(6)
  void clearPrevious() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.List<$core.int> get projection => $_getN(6);
  @$pb.TagNumber(7)
  set projection($core.List<$core.int> value) => $_setBytes(6, value);
  @$pb.TagNumber(7)
  $core.bool hasProjection() => $_has(6);
  @$pb.TagNumber(7)
  void clearProjection() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.List<$core.int> get frontier => $_getN(7);
  @$pb.TagNumber(8)
  set frontier($core.List<$core.int> value) => $_setBytes(7, value);
  @$pb.TagNumber(8)
  $core.bool hasFrontier() => $_has(7);
  @$pb.TagNumber(8)
  void clearFrontier() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.List<$core.int> get fences => $_getN(8);
  @$pb.TagNumber(9)
  set fences($core.List<$core.int> value) => $_setBytes(8, value);
  @$pb.TagNumber(9)
  $core.bool hasFences() => $_has(8);
  @$pb.TagNumber(9)
  void clearFences() => $_clearField(9);

  @$pb.TagNumber(10)
  $core.List<$core.int> get policy => $_getN(9);
  @$pb.TagNumber(10)
  set policy($core.List<$core.int> value) => $_setBytes(9, value);
  @$pb.TagNumber(10)
  $core.bool hasPolicy() => $_has(9);
  @$pb.TagNumber(10)
  void clearPolicy() => $_clearField(10);
}

class CurrentSecurityChangeID extends $pb.GeneratedMessage {
  factory CurrentSecurityChangeID({
    $core.int? version,
    $core.List<$core.int>? domain,
    $core.List<$core.int>? cohort,
    $fixnum.Int64? namespace,
    $core.List<$core.int>? nonce,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (domain != null) result.domain = domain;
    if (cohort != null) result.cohort = cohort;
    if (namespace != null) result.namespace = namespace;
    if (nonce != null) result.nonce = nonce;
    return result;
  }

  CurrentSecurityChangeID._();

  factory CurrentSecurityChangeID.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSecurityChangeID.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSecurityChangeID',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'domain', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'cohort', $pb.PbFieldType.OY)
    ..a<$fixnum.Int64>(
        4, _omitFieldNames ? '' : 'namespace', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$core.List<$core.int>>(
        5, _omitFieldNames ? '' : 'nonce', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityChangeID clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityChangeID copyWith(
          void Function(CurrentSecurityChangeID) updates) =>
      super.copyWith((message) => updates(message as CurrentSecurityChangeID))
          as CurrentSecurityChangeID;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSecurityChangeID create() => CurrentSecurityChangeID._();
  @$core.override
  CurrentSecurityChangeID createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSecurityChangeID getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSecurityChangeID>(create);
  static CurrentSecurityChangeID? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get domain => $_getN(1);
  @$pb.TagNumber(2)
  set domain($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDomain() => $_has(1);
  @$pb.TagNumber(2)
  void clearDomain() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get cohort => $_getN(2);
  @$pb.TagNumber(3)
  set cohort($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCohort() => $_has(2);
  @$pb.TagNumber(3)
  void clearCohort() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get namespace => $_getI64(3);
  @$pb.TagNumber(4)
  set namespace($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasNamespace() => $_has(3);
  @$pb.TagNumber(4)
  void clearNamespace() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.List<$core.int> get nonce => $_getN(4);
  @$pb.TagNumber(5)
  set nonce($core.List<$core.int> value) => $_setBytes(4, value);
  @$pb.TagNumber(5)
  $core.bool hasNonce() => $_has(4);
  @$pb.TagNumber(5)
  void clearNonce() => $_clearField(5);
}

class CurrentSecurityReview extends $pb.GeneratedMessage {
  factory CurrentSecurityReview({
    CurrentAuthorityProfile? profile,
    CurrentSemanticCut? expectedCut,
    CurrentSecurityChangeID? changeId,
    SecurityIdentity? actor,
    $core.List<$core.int>? intentDigest,
    $core.Iterable<SecurityChange>? changes,
  }) {
    final result = create();
    if (profile != null) result.profile = profile;
    if (expectedCut != null) result.expectedCut = expectedCut;
    if (changeId != null) result.changeId = changeId;
    if (actor != null) result.actor = actor;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (changes != null) result.changes.addAll(changes);
    return result;
  }

  CurrentSecurityReview._();

  factory CurrentSecurityReview.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSecurityReview.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSecurityReview',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentAuthorityProfile>(1, _omitFieldNames ? '' : 'profile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOM<CurrentSemanticCut>(2, _omitFieldNames ? '' : 'expectedCut',
        subBuilder: CurrentSemanticCut.create)
    ..aOM<CurrentSecurityChangeID>(3, _omitFieldNames ? '' : 'changeId',
        subBuilder: CurrentSecurityChangeID.create)
    ..aOM<SecurityIdentity>(4, _omitFieldNames ? '' : 'actor',
        subBuilder: SecurityIdentity.create)
    ..a<$core.List<$core.int>>(
        5, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..pPM<SecurityChange>(6, _omitFieldNames ? '' : 'changes',
        subBuilder: SecurityChange.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityReview clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityReview copyWith(
          void Function(CurrentSecurityReview) updates) =>
      super.copyWith((message) => updates(message as CurrentSecurityReview))
          as CurrentSecurityReview;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSecurityReview create() => CurrentSecurityReview._();
  @$core.override
  CurrentSecurityReview createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSecurityReview getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSecurityReview>(create);
  static CurrentSecurityReview? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentAuthorityProfile get profile => $_getN(0);
  @$pb.TagNumber(1)
  set profile(CurrentAuthorityProfile value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasProfile() => $_has(0);
  @$pb.TagNumber(1)
  void clearProfile() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentAuthorityProfile ensureProfile() => $_ensure(0);

  @$pb.TagNumber(2)
  CurrentSemanticCut get expectedCut => $_getN(1);
  @$pb.TagNumber(2)
  set expectedCut(CurrentSemanticCut value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasExpectedCut() => $_has(1);
  @$pb.TagNumber(2)
  void clearExpectedCut() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentSemanticCut ensureExpectedCut() => $_ensure(1);

  @$pb.TagNumber(3)
  CurrentSecurityChangeID get changeId => $_getN(2);
  @$pb.TagNumber(3)
  set changeId(CurrentSecurityChangeID value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasChangeId() => $_has(2);
  @$pb.TagNumber(3)
  void clearChangeId() => $_clearField(3);
  @$pb.TagNumber(3)
  CurrentSecurityChangeID ensureChangeId() => $_ensure(2);

  @$pb.TagNumber(4)
  SecurityIdentity get actor => $_getN(3);
  @$pb.TagNumber(4)
  set actor(SecurityIdentity value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasActor() => $_has(3);
  @$pb.TagNumber(4)
  void clearActor() => $_clearField(4);
  @$pb.TagNumber(4)
  SecurityIdentity ensureActor() => $_ensure(3);

  @$pb.TagNumber(5)
  $core.List<$core.int> get intentDigest => $_getN(4);
  @$pb.TagNumber(5)
  set intentDigest($core.List<$core.int> value) => $_setBytes(4, value);
  @$pb.TagNumber(5)
  $core.bool hasIntentDigest() => $_has(4);
  @$pb.TagNumber(5)
  void clearIntentDigest() => $_clearField(5);

  @$pb.TagNumber(6)
  $pb.PbList<SecurityChange> get changes => $_getList(5);
}

class CurrentControlCommit extends $pb.GeneratedMessage {
  factory CurrentControlCommit({
    $core.int? version,
    $core.List<$core.int>? domain,
    $core.List<$core.int>? cohort,
    $core.List<$core.int>? membership,
    $core.List<$core.int>? configuration,
    $fixnum.Int64? slot,
    $core.List<$core.int>? value,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (domain != null) result.domain = domain;
    if (cohort != null) result.cohort = cohort;
    if (membership != null) result.membership = membership;
    if (configuration != null) result.configuration = configuration;
    if (slot != null) result.slot = slot;
    if (value != null) result.value = value;
    return result;
  }

  CurrentControlCommit._();

  factory CurrentControlCommit.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentControlCommit.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentControlCommit',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'version', fieldType: $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'domain', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'cohort', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'membership', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        5, _omitFieldNames ? '' : 'configuration', $pb.PbFieldType.OY)
    ..a<$fixnum.Int64>(6, _omitFieldNames ? '' : 'slot', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$core.List<$core.int>>(
        7, _omitFieldNames ? '' : 'value', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentControlCommit clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentControlCommit copyWith(void Function(CurrentControlCommit) updates) =>
      super.copyWith((message) => updates(message as CurrentControlCommit))
          as CurrentControlCommit;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentControlCommit create() => CurrentControlCommit._();
  @$core.override
  CurrentControlCommit createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentControlCommit getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentControlCommit>(create);
  static CurrentControlCommit? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get version => $_getIZ(0);
  @$pb.TagNumber(1)
  set version($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get domain => $_getN(1);
  @$pb.TagNumber(2)
  set domain($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDomain() => $_has(1);
  @$pb.TagNumber(2)
  void clearDomain() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get cohort => $_getN(2);
  @$pb.TagNumber(3)
  set cohort($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCohort() => $_has(2);
  @$pb.TagNumber(3)
  void clearCohort() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.List<$core.int> get membership => $_getN(3);
  @$pb.TagNumber(4)
  set membership($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasMembership() => $_has(3);
  @$pb.TagNumber(4)
  void clearMembership() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.List<$core.int> get configuration => $_getN(4);
  @$pb.TagNumber(5)
  set configuration($core.List<$core.int> value) => $_setBytes(4, value);
  @$pb.TagNumber(5)
  $core.bool hasConfiguration() => $_has(4);
  @$pb.TagNumber(5)
  void clearConfiguration() => $_clearField(5);

  @$pb.TagNumber(6)
  $fixnum.Int64 get slot => $_getI64(5);
  @$pb.TagNumber(6)
  set slot($fixnum.Int64 value) => $_setInt64(5, value);
  @$pb.TagNumber(6)
  $core.bool hasSlot() => $_has(5);
  @$pb.TagNumber(6)
  void clearSlot() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.List<$core.int> get value => $_getN(6);
  @$pb.TagNumber(7)
  set value($core.List<$core.int> value) => $_setBytes(6, value);
  @$pb.TagNumber(7)
  $core.bool hasValue() => $_has(6);
  @$pb.TagNumber(7)
  void clearValue() => $_clearField(7);
}

class CurrentSecurityItemOutcome extends $pb.GeneratedMessage {
  factory CurrentSecurityItemOutcome({
    $core.int? index,
    $core.String? kind,
    CurrentSecurityDisposition? disposition,
  }) {
    final result = create();
    if (index != null) result.index = index;
    if (kind != null) result.kind = kind;
    if (disposition != null) result.disposition = disposition;
    return result;
  }

  CurrentSecurityItemOutcome._();

  factory CurrentSecurityItemOutcome.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSecurityItemOutcome.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSecurityItemOutcome',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'index', fieldType: $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'kind')
    ..aE<CurrentSecurityDisposition>(3, _omitFieldNames ? '' : 'disposition',
        enumValues: CurrentSecurityDisposition.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityItemOutcome clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityItemOutcome copyWith(
          void Function(CurrentSecurityItemOutcome) updates) =>
      super.copyWith(
              (message) => updates(message as CurrentSecurityItemOutcome))
          as CurrentSecurityItemOutcome;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSecurityItemOutcome create() => CurrentSecurityItemOutcome._();
  @$core.override
  CurrentSecurityItemOutcome createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSecurityItemOutcome getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSecurityItemOutcome>(create);
  static CurrentSecurityItemOutcome? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get index => $_getIZ(0);
  @$pb.TagNumber(1)
  set index($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasIndex() => $_has(0);
  @$pb.TagNumber(1)
  void clearIndex() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get kind => $_getSZ(1);
  @$pb.TagNumber(2)
  set kind($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasKind() => $_has(1);
  @$pb.TagNumber(2)
  void clearKind() => $_clearField(2);

  @$pb.TagNumber(3)
  CurrentSecurityDisposition get disposition => $_getN(2);
  @$pb.TagNumber(3)
  set disposition(CurrentSecurityDisposition value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasDisposition() => $_has(2);
  @$pb.TagNumber(3)
  void clearDisposition() => $_clearField(3);
}

class CurrentSecurityOriginalOutcome extends $pb.GeneratedMessage {
  factory CurrentSecurityOriginalOutcome({
    CurrentSecurityChangeID? changeId,
    $core.List<$core.int>? intentDigest,
    $core.List<$core.int>? handoffDigest,
    CurrentControlCommit? commit,
    CurrentSecurityDisposition? disposition,
    $core.Iterable<CurrentSecurityItemOutcome>? items,
    CurrentSemanticCut? observedCut,
    CurrentSemanticCut? resultingCut,
  }) {
    final result = create();
    if (changeId != null) result.changeId = changeId;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (handoffDigest != null) result.handoffDigest = handoffDigest;
    if (commit != null) result.commit = commit;
    if (disposition != null) result.disposition = disposition;
    if (items != null) result.items.addAll(items);
    if (observedCut != null) result.observedCut = observedCut;
    if (resultingCut != null) result.resultingCut = resultingCut;
    return result;
  }

  CurrentSecurityOriginalOutcome._();

  factory CurrentSecurityOriginalOutcome.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSecurityOriginalOutcome.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSecurityOriginalOutcome',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentSecurityChangeID>(1, _omitFieldNames ? '' : 'changeId',
        subBuilder: CurrentSecurityChangeID.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'handoffDigest', $pb.PbFieldType.OY)
    ..aOM<CurrentControlCommit>(4, _omitFieldNames ? '' : 'commit',
        subBuilder: CurrentControlCommit.create)
    ..aE<CurrentSecurityDisposition>(5, _omitFieldNames ? '' : 'disposition',
        enumValues: CurrentSecurityDisposition.values)
    ..pPM<CurrentSecurityItemOutcome>(6, _omitFieldNames ? '' : 'items',
        subBuilder: CurrentSecurityItemOutcome.create)
    ..aOM<CurrentSemanticCut>(7, _omitFieldNames ? '' : 'observedCut',
        subBuilder: CurrentSemanticCut.create)
    ..aOM<CurrentSemanticCut>(8, _omitFieldNames ? '' : 'resultingCut',
        subBuilder: CurrentSemanticCut.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityOriginalOutcome clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityOriginalOutcome copyWith(
          void Function(CurrentSecurityOriginalOutcome) updates) =>
      super.copyWith(
              (message) => updates(message as CurrentSecurityOriginalOutcome))
          as CurrentSecurityOriginalOutcome;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSecurityOriginalOutcome create() =>
      CurrentSecurityOriginalOutcome._();
  @$core.override
  CurrentSecurityOriginalOutcome createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSecurityOriginalOutcome getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSecurityOriginalOutcome>(create);
  static CurrentSecurityOriginalOutcome? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentSecurityChangeID get changeId => $_getN(0);
  @$pb.TagNumber(1)
  set changeId(CurrentSecurityChangeID value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasChangeId() => $_has(0);
  @$pb.TagNumber(1)
  void clearChangeId() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentSecurityChangeID ensureChangeId() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.List<$core.int> get intentDigest => $_getN(1);
  @$pb.TagNumber(2)
  set intentDigest($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasIntentDigest() => $_has(1);
  @$pb.TagNumber(2)
  void clearIntentDigest() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get handoffDigest => $_getN(2);
  @$pb.TagNumber(3)
  set handoffDigest($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasHandoffDigest() => $_has(2);
  @$pb.TagNumber(3)
  void clearHandoffDigest() => $_clearField(3);

  @$pb.TagNumber(4)
  CurrentControlCommit get commit => $_getN(3);
  @$pb.TagNumber(4)
  set commit(CurrentControlCommit value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasCommit() => $_has(3);
  @$pb.TagNumber(4)
  void clearCommit() => $_clearField(4);
  @$pb.TagNumber(4)
  CurrentControlCommit ensureCommit() => $_ensure(3);

  @$pb.TagNumber(5)
  CurrentSecurityDisposition get disposition => $_getN(4);
  @$pb.TagNumber(5)
  set disposition(CurrentSecurityDisposition value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasDisposition() => $_has(4);
  @$pb.TagNumber(5)
  void clearDisposition() => $_clearField(5);

  @$pb.TagNumber(6)
  $pb.PbList<CurrentSecurityItemOutcome> get items => $_getList(5);

  @$pb.TagNumber(7)
  CurrentSemanticCut get observedCut => $_getN(6);
  @$pb.TagNumber(7)
  set observedCut(CurrentSemanticCut value) => $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasObservedCut() => $_has(6);
  @$pb.TagNumber(7)
  void clearObservedCut() => $_clearField(7);
  @$pb.TagNumber(7)
  CurrentSemanticCut ensureObservedCut() => $_ensure(6);

  @$pb.TagNumber(8)
  CurrentSemanticCut get resultingCut => $_getN(7);
  @$pb.TagNumber(8)
  set resultingCut(CurrentSemanticCut value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasResultingCut() => $_has(7);
  @$pb.TagNumber(8)
  void clearResultingCut() => $_clearField(8);
  @$pb.TagNumber(8)
  CurrentSemanticCut ensureResultingCut() => $_ensure(7);
}

class CurrentSecurityChangeResult extends $pb.GeneratedMessage {
  factory CurrentSecurityChangeResult({
    CurrentAuthorityProfile? profile,
    CurrentSecurityChangeID? changeId,
    $core.List<$core.int>? intentDigest,
    CurrentSecurityProgress? progress,
    CurrentSecurityOriginalOutcome? original,
    CurrentAuthorizationStopObservation? stopObservation,
  }) {
    final result = create();
    if (profile != null) result.profile = profile;
    if (changeId != null) result.changeId = changeId;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (progress != null) result.progress = progress;
    if (original != null) result.original = original;
    if (stopObservation != null) result.stopObservation = stopObservation;
    return result;
  }

  CurrentSecurityChangeResult._();

  factory CurrentSecurityChangeResult.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSecurityChangeResult.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSecurityChangeResult',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentAuthorityProfile>(1, _omitFieldNames ? '' : 'profile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOM<CurrentSecurityChangeID>(2, _omitFieldNames ? '' : 'changeId',
        subBuilder: CurrentSecurityChangeID.create)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..aE<CurrentSecurityProgress>(4, _omitFieldNames ? '' : 'progress',
        enumValues: CurrentSecurityProgress.values)
    ..aOM<CurrentSecurityOriginalOutcome>(5, _omitFieldNames ? '' : 'original',
        subBuilder: CurrentSecurityOriginalOutcome.create)
    ..aE<CurrentAuthorizationStopObservation>(
        6, _omitFieldNames ? '' : 'stopObservation',
        enumValues: CurrentAuthorizationStopObservation.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityChangeResult clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityChangeResult copyWith(
          void Function(CurrentSecurityChangeResult) updates) =>
      super.copyWith(
              (message) => updates(message as CurrentSecurityChangeResult))
          as CurrentSecurityChangeResult;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSecurityChangeResult create() =>
      CurrentSecurityChangeResult._();
  @$core.override
  CurrentSecurityChangeResult createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSecurityChangeResult getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSecurityChangeResult>(create);
  static CurrentSecurityChangeResult? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentAuthorityProfile get profile => $_getN(0);
  @$pb.TagNumber(1)
  set profile(CurrentAuthorityProfile value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasProfile() => $_has(0);
  @$pb.TagNumber(1)
  void clearProfile() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentAuthorityProfile ensureProfile() => $_ensure(0);

  @$pb.TagNumber(2)
  CurrentSecurityChangeID get changeId => $_getN(1);
  @$pb.TagNumber(2)
  set changeId(CurrentSecurityChangeID value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentSecurityChangeID ensureChangeId() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.List<$core.int> get intentDigest => $_getN(2);
  @$pb.TagNumber(3)
  set intentDigest($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasIntentDigest() => $_has(2);
  @$pb.TagNumber(3)
  void clearIntentDigest() => $_clearField(3);

  @$pb.TagNumber(4)
  CurrentSecurityProgress get progress => $_getN(3);
  @$pb.TagNumber(4)
  set progress(CurrentSecurityProgress value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasProgress() => $_has(3);
  @$pb.TagNumber(4)
  void clearProgress() => $_clearField(4);

  @$pb.TagNumber(5)
  CurrentSecurityOriginalOutcome get original => $_getN(4);
  @$pb.TagNumber(5)
  set original(CurrentSecurityOriginalOutcome value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasOriginal() => $_has(4);
  @$pb.TagNumber(5)
  void clearOriginal() => $_clearField(5);
  @$pb.TagNumber(5)
  CurrentSecurityOriginalOutcome ensureOriginal() => $_ensure(4);

  @$pb.TagNumber(6)
  CurrentAuthorizationStopObservation get stopObservation => $_getN(5);
  @$pb.TagNumber(6)
  set stopObservation(CurrentAuthorizationStopObservation value) =>
      $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasStopObservation() => $_has(5);
  @$pb.TagNumber(6)
  void clearStopObservation() => $_clearField(6);
}

class CurrentSecurityAuditRecord extends $pb.GeneratedMessage {
  factory CurrentSecurityAuditRecord({
    CurrentSecurityChangeResult? result,
    $core.List<$core.int>? actorDigest,
    $core.String? operation,
  }) {
    final result$ = create();
    if (result != null) result$.result = result;
    if (actorDigest != null) result$.actorDigest = actorDigest;
    if (operation != null) result$.operation = operation;
    return result$;
  }

  CurrentSecurityAuditRecord._();

  factory CurrentSecurityAuditRecord.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSecurityAuditRecord.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSecurityAuditRecord',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentSecurityChangeResult>(1, _omitFieldNames ? '' : 'result',
        subBuilder: CurrentSecurityChangeResult.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'actorDigest', $pb.PbFieldType.OY)
    ..aOS(3, _omitFieldNames ? '' : 'operation')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityAuditRecord clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityAuditRecord copyWith(
          void Function(CurrentSecurityAuditRecord) updates) =>
      super.copyWith(
              (message) => updates(message as CurrentSecurityAuditRecord))
          as CurrentSecurityAuditRecord;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSecurityAuditRecord create() => CurrentSecurityAuditRecord._();
  @$core.override
  CurrentSecurityAuditRecord createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSecurityAuditRecord getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSecurityAuditRecord>(create);
  static CurrentSecurityAuditRecord? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentSecurityChangeResult get result => $_getN(0);
  @$pb.TagNumber(1)
  set result(CurrentSecurityChangeResult value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasResult() => $_has(0);
  @$pb.TagNumber(1)
  void clearResult() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentSecurityChangeResult ensureResult() => $_ensure(0);

  @$pb.TagNumber(2)
  $core.List<$core.int> get actorDigest => $_getN(1);
  @$pb.TagNumber(2)
  set actorDigest($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasActorDigest() => $_has(1);
  @$pb.TagNumber(2)
  void clearActorDigest() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get operation => $_getSZ(2);
  @$pb.TagNumber(3)
  set operation($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasOperation() => $_has(2);
  @$pb.TagNumber(3)
  void clearOperation() => $_clearField(3);
}

/// This invocation was refused before H, without settling any earlier
/// ambiguous invocation. Preserve the original identity for status-first use.
class CurrentSecurityInvocationRejected extends $pb.GeneratedMessage {
  factory CurrentSecurityInvocationRejected({
    CurrentAuthorityProfile? profile,
    CurrentSecurityChangeID? changeId,
    $core.List<$core.int>? intentDigest,
    $core.bool? purposeRequired,
  }) {
    final result = create();
    if (profile != null) result.profile = profile;
    if (changeId != null) result.changeId = changeId;
    if (intentDigest != null) result.intentDigest = intentDigest;
    if (purposeRequired != null) result.purposeRequired = purposeRequired;
    return result;
  }

  CurrentSecurityInvocationRejected._();

  factory CurrentSecurityInvocationRejected.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CurrentSecurityInvocationRejected.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CurrentSecurityInvocationRejected',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<CurrentAuthorityProfile>(1, _omitFieldNames ? '' : 'profile',
        subBuilder: CurrentAuthorityProfile.create)
    ..aOM<CurrentSecurityChangeID>(2, _omitFieldNames ? '' : 'changeId',
        subBuilder: CurrentSecurityChangeID.create)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'intentDigest', $pb.PbFieldType.OY)
    ..aOB(4, _omitFieldNames ? '' : 'purposeRequired')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityInvocationRejected clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CurrentSecurityInvocationRejected copyWith(
          void Function(CurrentSecurityInvocationRejected) updates) =>
      super.copyWith((message) =>
              updates(message as CurrentSecurityInvocationRejected))
          as CurrentSecurityInvocationRejected;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CurrentSecurityInvocationRejected create() =>
      CurrentSecurityInvocationRejected._();
  @$core.override
  CurrentSecurityInvocationRejected createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CurrentSecurityInvocationRejected getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CurrentSecurityInvocationRejected>(
          create);
  static CurrentSecurityInvocationRejected? _defaultInstance;

  @$pb.TagNumber(1)
  CurrentAuthorityProfile get profile => $_getN(0);
  @$pb.TagNumber(1)
  set profile(CurrentAuthorityProfile value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasProfile() => $_has(0);
  @$pb.TagNumber(1)
  void clearProfile() => $_clearField(1);
  @$pb.TagNumber(1)
  CurrentAuthorityProfile ensureProfile() => $_ensure(0);

  @$pb.TagNumber(2)
  CurrentSecurityChangeID get changeId => $_getN(1);
  @$pb.TagNumber(2)
  set changeId(CurrentSecurityChangeID value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasChangeId() => $_has(1);
  @$pb.TagNumber(2)
  void clearChangeId() => $_clearField(2);
  @$pb.TagNumber(2)
  CurrentSecurityChangeID ensureChangeId() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.List<$core.int> get intentDigest => $_getN(2);
  @$pb.TagNumber(3)
  set intentDigest($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasIntentDigest() => $_has(2);
  @$pb.TagNumber(3)
  void clearIntentDigest() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.bool get purposeRequired => $_getBF(3);
  @$pb.TagNumber(4)
  set purposeRequired($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasPurposeRequired() => $_has(3);
  @$pb.TagNumber(4)
  void clearPurposeRequired() => $_clearField(4);
}

/// Connected-member local-admission guidance. Counters are sampled separately;
/// footprints include retained Put barriers. This neither freezes usage nor
/// predicts admission or cluster-wide capacity. Existing data is not evicted.
class OperationalCapacityPreview extends $pb.GeneratedMessage {
  factory OperationalCapacityPreview({
    $core.int? member,
    $fixnum.Int64? effectiveGeneration,
    $fixnum.Int64? vertexFootprint,
    $fixnum.Int64? edgeFootprint,
    $fixnum.Int64? vertexCausalEntries,
    $fixnum.Int64? edgeCausalEntries,
    $core.bool? verticesOverLimit,
    $core.bool? edgesOverLimit,
    $core.bool? vertexCausalOverLimit,
    $core.bool? edgeCausalOverLimit,
  }) {
    final result = create();
    if (member != null) result.member = member;
    if (effectiveGeneration != null)
      result.effectiveGeneration = effectiveGeneration;
    if (vertexFootprint != null) result.vertexFootprint = vertexFootprint;
    if (edgeFootprint != null) result.edgeFootprint = edgeFootprint;
    if (vertexCausalEntries != null)
      result.vertexCausalEntries = vertexCausalEntries;
    if (edgeCausalEntries != null) result.edgeCausalEntries = edgeCausalEntries;
    if (verticesOverLimit != null) result.verticesOverLimit = verticesOverLimit;
    if (edgesOverLimit != null) result.edgesOverLimit = edgesOverLimit;
    if (vertexCausalOverLimit != null)
      result.vertexCausalOverLimit = vertexCausalOverLimit;
    if (edgeCausalOverLimit != null)
      result.edgeCausalOverLimit = edgeCausalOverLimit;
    return result;
  }

  OperationalCapacityPreview._();

  factory OperationalCapacityPreview.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OperationalCapacityPreview.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OperationalCapacityPreview',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'member', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'effectiveGeneration', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        3, _omitFieldNames ? '' : 'vertexFootprint', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        4, _omitFieldNames ? '' : 'edgeFootprint', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        5, _omitFieldNames ? '' : 'vertexCausalEntries', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        6, _omitFieldNames ? '' : 'edgeCausalEntries', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOB(7, _omitFieldNames ? '' : 'verticesOverLimit')
    ..aOB(8, _omitFieldNames ? '' : 'edgesOverLimit')
    ..aOB(9, _omitFieldNames ? '' : 'vertexCausalOverLimit')
    ..aOB(10, _omitFieldNames ? '' : 'edgeCausalOverLimit')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalCapacityPreview clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OperationalCapacityPreview copyWith(
          void Function(OperationalCapacityPreview) updates) =>
      super.copyWith(
              (message) => updates(message as OperationalCapacityPreview))
          as OperationalCapacityPreview;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OperationalCapacityPreview create() => OperationalCapacityPreview._();
  @$core.override
  OperationalCapacityPreview createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static OperationalCapacityPreview getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OperationalCapacityPreview>(create);
  static OperationalCapacityPreview? _defaultInstance;

  @$pb.TagNumber(1)
  $core.int get member => $_getIZ(0);
  @$pb.TagNumber(1)
  set member($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasMember() => $_has(0);
  @$pb.TagNumber(1)
  void clearMember() => $_clearField(1);

  /// Owner snapshot generation, not a native applied-cut certificate.
  @$pb.TagNumber(2)
  $fixnum.Int64 get effectiveGeneration => $_getI64(1);
  @$pb.TagNumber(2)
  set effectiveGeneration($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasEffectiveGeneration() => $_has(1);
  @$pb.TagNumber(2)
  void clearEffectiveGeneration() => $_clearField(2);

  @$pb.TagNumber(3)
  $fixnum.Int64 get vertexFootprint => $_getI64(2);
  @$pb.TagNumber(3)
  set vertexFootprint($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasVertexFootprint() => $_has(2);
  @$pb.TagNumber(3)
  void clearVertexFootprint() => $_clearField(3);

  @$pb.TagNumber(4)
  $fixnum.Int64 get edgeFootprint => $_getI64(3);
  @$pb.TagNumber(4)
  set edgeFootprint($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasEdgeFootprint() => $_has(3);
  @$pb.TagNumber(4)
  void clearEdgeFootprint() => $_clearField(4);

  @$pb.TagNumber(5)
  $fixnum.Int64 get vertexCausalEntries => $_getI64(4);
  @$pb.TagNumber(5)
  set vertexCausalEntries($fixnum.Int64 value) => $_setInt64(4, value);
  @$pb.TagNumber(5)
  $core.bool hasVertexCausalEntries() => $_has(4);
  @$pb.TagNumber(5)
  void clearVertexCausalEntries() => $_clearField(5);

  @$pb.TagNumber(6)
  $fixnum.Int64 get edgeCausalEntries => $_getI64(5);
  @$pb.TagNumber(6)
  set edgeCausalEntries($fixnum.Int64 value) => $_setInt64(5, value);
  @$pb.TagNumber(6)
  $core.bool hasEdgeCausalEntries() => $_has(5);
  @$pb.TagNumber(6)
  void clearEdgeCausalEntries() => $_clearField(6);

  @$pb.TagNumber(7)
  $core.bool get verticesOverLimit => $_getBF(6);
  @$pb.TagNumber(7)
  set verticesOverLimit($core.bool value) => $_setBool(6, value);
  @$pb.TagNumber(7)
  $core.bool hasVerticesOverLimit() => $_has(6);
  @$pb.TagNumber(7)
  void clearVerticesOverLimit() => $_clearField(7);

  @$pb.TagNumber(8)
  $core.bool get edgesOverLimit => $_getBF(7);
  @$pb.TagNumber(8)
  set edgesOverLimit($core.bool value) => $_setBool(7, value);
  @$pb.TagNumber(8)
  $core.bool hasEdgesOverLimit() => $_has(7);
  @$pb.TagNumber(8)
  void clearEdgesOverLimit() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.bool get vertexCausalOverLimit => $_getBF(8);
  @$pb.TagNumber(9)
  set vertexCausalOverLimit($core.bool value) => $_setBool(8, value);
  @$pb.TagNumber(9)
  $core.bool hasVertexCausalOverLimit() => $_has(8);
  @$pb.TagNumber(9)
  void clearVertexCausalOverLimit() => $_clearField(9);

  @$pb.TagNumber(10)
  $core.bool get edgeCausalOverLimit => $_getBF(9);
  @$pb.TagNumber(10)
  set edgeCausalOverLimit($core.bool value) => $_setBool(9, value);
  @$pb.TagNumber(10)
  $core.bool hasEdgeCausalOverLimit() => $_has(9);
  @$pb.TagNumber(10)
  void clearEdgeCausalOverLimit() => $_clearField(10);
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
