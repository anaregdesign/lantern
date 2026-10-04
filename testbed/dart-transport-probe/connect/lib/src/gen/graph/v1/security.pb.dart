// This is a generated file - do not edit.
//
// Generated from graph/v1/security.proto.

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
  GetAuthCapabilitiesRequest clone() =>
      GetAuthCapabilitiesRequest()..mergeFromMessage(this);
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
  static $pb.PbList<GetAuthCapabilitiesRequest> createRepeated() =>
      $pb.PbList<GetAuthCapabilitiesRequest>();
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
  LoginIssuer clone() => LoginIssuer()..mergeFromMessage(this);
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
  static $pb.PbList<LoginIssuer> createRepeated() => $pb.PbList<LoginIssuer>();
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
  }) {
    final result = create();
    if (mode != null) result.mode = mode;
    if (loginIssuers != null) result.loginIssuers.addAll(loginIssuers);
    if (loginPath != null) result.loginPath = loginPath;
    if (protocolVersion != null) result.protocolVersion = protocolVersion;
    if (ready != null) result.ready = ready;
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
    ..e<AuthMode>(1, _omitFieldNames ? '' : 'mode', $pb.PbFieldType.OE,
        defaultOrMaker: AuthMode.AUTH_MODE_UNSPECIFIED,
        valueOf: AuthMode.valueOf,
        enumValues: AuthMode.values)
    ..pc<LoginIssuer>(
        2, _omitFieldNames ? '' : 'loginIssuers', $pb.PbFieldType.PM,
        subBuilder: LoginIssuer.create)
    ..aOS(3, _omitFieldNames ? '' : 'loginPath')
    ..a<$core.int>(
        4, _omitFieldNames ? '' : 'protocolVersion', $pb.PbFieldType.OU3)
    ..aOB(5, _omitFieldNames ? '' : 'ready')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetAuthCapabilitiesResponse clone() =>
      GetAuthCapabilitiesResponse()..mergeFromMessage(this);
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
  static $pb.PbList<GetAuthCapabilitiesResponse> createRepeated() =>
      $pb.PbList<GetAuthCapabilitiesResponse>();
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
    ..e<SecurityPrincipalKind>(
        1, _omitFieldNames ? '' : 'kind', $pb.PbFieldType.OE,
        defaultOrMaker:
            SecurityPrincipalKind.SECURITY_PRINCIPAL_KIND_UNSPECIFIED,
        valueOf: SecurityPrincipalKind.valueOf,
        enumValues: SecurityPrincipalKind.values)
    ..aOS(2, _omitFieldNames ? '' : 'issuer')
    ..aOS(3, _omitFieldNames ? '' : 'subject')
    ..aOS(4, _omitFieldNames ? '' : 'machineName')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityIdentity clone() => SecurityIdentity()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityIdentity> createRepeated() =>
      $pb.PbList<SecurityIdentity>();
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

/// Selectors use literal logical-key prefixes. Empty prefix explicitly means
/// all data. Pair is a retired diagnostic shape: Server rejects every pair
/// selector and independent Edge action in a Role. Edge permissions are derived
/// from Vertex grants; pair must never be interpreted as permission authority.
class SecurityPrefixPair extends $pb.GeneratedMessage {
  factory SecurityPrefixPair({
    $core.String? tailPrefix,
    $core.String? headPrefix,
  }) {
    final result = create();
    if (tailPrefix != null) result.tailPrefix = tailPrefix;
    if (headPrefix != null) result.headPrefix = headPrefix;
    return result;
  }

  SecurityPrefixPair._();

  factory SecurityPrefixPair.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SecurityPrefixPair.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityPrefixPair',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tailPrefix')
    ..aOS(2, _omitFieldNames ? '' : 'headPrefix')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityPrefixPair clone() => SecurityPrefixPair()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityPrefixPair copyWith(void Function(SecurityPrefixPair) updates) =>
      super.copyWith((message) => updates(message as SecurityPrefixPair))
          as SecurityPrefixPair;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SecurityPrefixPair create() => SecurityPrefixPair._();
  @$core.override
  SecurityPrefixPair createEmptyInstance() => create();
  static $pb.PbList<SecurityPrefixPair> createRepeated() =>
      $pb.PbList<SecurityPrefixPair>();
  @$core.pragma('dart2js:noInline')
  static SecurityPrefixPair getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityPrefixPair>(create);
  static SecurityPrefixPair? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tailPrefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set tailPrefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTailPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearTailPrefix() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get headPrefix => $_getSZ(1);
  @$pb.TagNumber(2)
  set headPrefix($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHeadPrefix() => $_has(1);
  @$pb.TagNumber(2)
  void clearHeadPrefix() => $_clearField(2);
}

enum SecurityRule_Resource { prefix, global, pair, notSet }

class SecurityRule extends $pb.GeneratedMessage {
  factory SecurityRule({
    $core.String? id,
    SecurityEffect? effect,
    SecurityAction? action,
    $core.String? prefix,
    $core.bool? global,
    SecurityPrefixPair? pair,
  }) {
    final result = create();
    if (id != null) result.id = id;
    if (effect != null) result.effect = effect;
    if (action != null) result.action = action;
    if (prefix != null) result.prefix = prefix;
    if (global != null) result.global = global;
    if (pair != null) result.pair = pair;
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
    5: SecurityRule_Resource.global,
    6: SecurityRule_Resource.pair,
    0: SecurityRule_Resource.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityRule',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [4, 5, 6])
    ..aOS(1, _omitFieldNames ? '' : 'id')
    ..e<SecurityEffect>(2, _omitFieldNames ? '' : 'effect', $pb.PbFieldType.OE,
        defaultOrMaker: SecurityEffect.SECURITY_EFFECT_UNSPECIFIED,
        valueOf: SecurityEffect.valueOf,
        enumValues: SecurityEffect.values)
    ..e<SecurityAction>(3, _omitFieldNames ? '' : 'action', $pb.PbFieldType.OE,
        defaultOrMaker: SecurityAction.SECURITY_ACTION_UNSPECIFIED,
        valueOf: SecurityAction.valueOf,
        enumValues: SecurityAction.values)
    ..aOS(4, _omitFieldNames ? '' : 'prefix')
    ..aOB(5, _omitFieldNames ? '' : 'global')
    ..aOM<SecurityPrefixPair>(6, _omitFieldNames ? '' : 'pair',
        subBuilder: SecurityPrefixPair.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRule clone() => SecurityRule()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityRule> createRepeated() =>
      $pb.PbList<SecurityRule>();
  @$core.pragma('dart2js:noInline')
  static SecurityRule getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityRule>(create);
  static SecurityRule? _defaultInstance;

  SecurityRule_Resource whichResource() =>
      _SecurityRule_ResourceByTag[$_whichOneof(0)]!;
  void clearResource() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $core.String get id => $_getSZ(0);
  @$pb.TagNumber(1)
  set id($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasId() => $_has(0);
  @$pb.TagNumber(1)
  void clearId() => $_clearField(1);

  @$pb.TagNumber(2)
  SecurityEffect get effect => $_getN(1);
  @$pb.TagNumber(2)
  set effect(SecurityEffect value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasEffect() => $_has(1);
  @$pb.TagNumber(2)
  void clearEffect() => $_clearField(2);

  @$pb.TagNumber(3)
  SecurityAction get action => $_getN(2);
  @$pb.TagNumber(3)
  set action(SecurityAction value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasAction() => $_has(2);
  @$pb.TagNumber(3)
  void clearAction() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.String get prefix => $_getSZ(3);
  @$pb.TagNumber(4)
  set prefix($core.String value) => $_setString(3, value);
  @$pb.TagNumber(4)
  $core.bool hasPrefix() => $_has(3);
  @$pb.TagNumber(4)
  void clearPrefix() => $_clearField(4);

  @$pb.TagNumber(5)
  $core.bool get global => $_getBF(4);
  @$pb.TagNumber(5)
  set global($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasGlobal() => $_has(4);
  @$pb.TagNumber(5)
  void clearGlobal() => $_clearField(5);

  @$pb.TagNumber(6)
  SecurityPrefixPair get pair => $_getN(5);
  @$pb.TagNumber(6)
  set pair(SecurityPrefixPair value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasPair() => $_has(5);
  @$pb.TagNumber(6)
  void clearPair() => $_clearField(6);
  @$pb.TagNumber(6)
  SecurityPrefixPair ensurePair() => $_ensure(5);
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
    ..pc<SecurityRule>(3, _omitFieldNames ? '' : 'rules', $pb.PbFieldType.PM,
        subBuilder: SecurityRule.create)
    ..aOB(4, _omitFieldNames ? '' : 'envOwned')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRole clone() => SecurityRole()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityRole> createRepeated() =>
      $pb.PbList<SecurityRole>();
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
  SecurityRoleAssignment clone() =>
      SecurityRoleAssignment()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityRoleAssignment> createRepeated() =>
      $pb.PbList<SecurityRoleAssignment>();
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
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (state != null) result.state = state;
    if (assignments != null) result.assignments.addAll(assignments);
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
    ..e<SecurityPrincipalState>(
        2, _omitFieldNames ? '' : 'state', $pb.PbFieldType.OE,
        defaultOrMaker:
            SecurityPrincipalState.SECURITY_PRINCIPAL_STATE_UNSPECIFIED,
        valueOf: SecurityPrincipalState.valueOf,
        enumValues: SecurityPrincipalState.values)
    ..pc<SecurityRoleAssignment>(
        3, _omitFieldNames ? '' : 'assignments', $pb.PbFieldType.PM,
        subBuilder: SecurityRoleAssignment.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityUser clone() => SecurityUser()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityUser> createRepeated() =>
      $pb.PbList<SecurityUser>();
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
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityIssuer clone() => SecurityIssuer()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityIssuer> createRepeated() =>
      $pb.PbList<SecurityIssuer>();
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
}

class SecurityVersion extends $pb.GeneratedMessage {
  factory SecurityVersion({
    $fixnum.Int64? revision,
    $core.List<$core.int>? digest,
    $core.List<$core.int>? generation,
  }) {
    final result = create();
    if (revision != null) result.revision = revision;
    if (digest != null) result.digest = digest;
    if (generation != null) result.generation = generation;
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
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityVersion clone() => SecurityVersion()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityVersion> createRepeated() =>
      $pb.PbList<SecurityVersion>();
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
  GetCurrentPrincipalRequest clone() =>
      GetCurrentPrincipalRequest()..mergeFromMessage(this);
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
  static $pb.PbList<GetCurrentPrincipalRequest> createRepeated() =>
      $pb.PbList<GetCurrentPrincipalRequest>();
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
    $0.Timestamp? expiresAt,
    $core.bool? recentAuthentication,
    $core.String? csrfToken,
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (roles != null) result.roles.addAll(roles);
    if (version != null) result.version = version;
    if (expiresAt != null) result.expiresAt = expiresAt;
    if (recentAuthentication != null)
      result.recentAuthentication = recentAuthentication;
    if (csrfToken != null) result.csrfToken = csrfToken;
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
    ..pc<SecurityRole>(2, _omitFieldNames ? '' : 'roles', $pb.PbFieldType.PM,
        subBuilder: SecurityRole.create)
    ..aOM<SecurityVersion>(3, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOM<$0.Timestamp>(4, _omitFieldNames ? '' : 'expiresAt',
        subBuilder: $0.Timestamp.create)
    ..aOB(5, _omitFieldNames ? '' : 'recentAuthentication')
    ..aOS(6, _omitFieldNames ? '' : 'csrfToken')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetCurrentPrincipalResponse clone() =>
      GetCurrentPrincipalResponse()..mergeFromMessage(this);
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
  static $pb.PbList<GetCurrentPrincipalResponse> createRepeated() =>
      $pb.PbList<GetCurrentPrincipalResponse>();
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
  $0.Timestamp get expiresAt => $_getN(3);
  @$pb.TagNumber(4)
  set expiresAt($0.Timestamp value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasExpiresAt() => $_has(3);
  @$pb.TagNumber(4)
  void clearExpiresAt() => $_clearField(4);
  @$pb.TagNumber(4)
  $0.Timestamp ensureExpiresAt() => $_ensure(3);

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
}

/// Same-origin /auth/session and /auth/logout HTTP responses. OIDC tokens never
/// cross this boundary; principal is absent only when mode is explicitly OFF.
class BrowserSession extends $pb.GeneratedMessage {
  factory BrowserSession({
    AuthMode? mode,
    GetCurrentPrincipalResponse? principal,
  }) {
    final result = create();
    if (mode != null) result.mode = mode;
    if (principal != null) result.principal = principal;
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
    ..e<AuthMode>(1, _omitFieldNames ? '' : 'mode', $pb.PbFieldType.OE,
        defaultOrMaker: AuthMode.AUTH_MODE_UNSPECIFIED,
        valueOf: AuthMode.valueOf,
        enumValues: AuthMode.values)
    ..aOM<GetCurrentPrincipalResponse>(2, _omitFieldNames ? '' : 'principal',
        subBuilder: GetCurrentPrincipalResponse.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BrowserSession clone() => BrowserSession()..mergeFromMessage(this);
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
  static $pb.PbList<BrowserSession> createRepeated() =>
      $pb.PbList<BrowserSession>();
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
}

class SessionRevocation extends $pb.GeneratedMessage {
  factory SessionRevocation({
    SecurityVersion? version,
    SecurityEnforcementState? enforcement,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (enforcement != null) result.enforcement = enforcement;
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
    ..e<SecurityEnforcementState>(
        2, _omitFieldNames ? '' : 'enforcement', $pb.PbFieldType.OE,
        defaultOrMaker:
            SecurityEnforcementState.SECURITY_ENFORCEMENT_STATE_UNSPECIFIED,
        valueOf: SecurityEnforcementState.valueOf,
        enumValues: SecurityEnforcementState.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SessionRevocation clone() => SessionRevocation()..mergeFromMessage(this);
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
  static $pb.PbList<SessionRevocation> createRepeated() =>
      $pb.PbList<SessionRevocation>();
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
    ..a<$core.int>(1, _omitFieldNames ? '' : 'limit', $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListIssuersRequest clone() => ListIssuersRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ListIssuersRequest> createRepeated() =>
      $pb.PbList<ListIssuersRequest>();
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
    ..a<$core.int>(1, _omitFieldNames ? '' : 'limit', $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRolesRequest clone() => ListRolesRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ListRolesRequest> createRepeated() =>
      $pb.PbList<ListRolesRequest>();
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
    ..a<$core.int>(1, _omitFieldNames ? '' : 'limit', $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListUsersRequest clone() => ListUsersRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ListUsersRequest> createRepeated() =>
      $pb.PbList<ListUsersRequest>();
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
    ..a<$core.int>(1, _omitFieldNames ? '' : 'limit', $pb.PbFieldType.OU3)
    ..aOS(2, _omitFieldNames ? '' : 'cursor')
    ..aOS(3, _omitFieldNames ? '' : 'exact')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSecurityAuditRequest clone() =>
      ListSecurityAuditRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ListSecurityAuditRequest> createRepeated() =>
      $pb.PbList<ListSecurityAuditRequest>();
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
  GetIssuerRequest clone() => GetIssuerRequest()..mergeFromMessage(this);
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
  static $pb.PbList<GetIssuerRequest> createRepeated() =>
      $pb.PbList<GetIssuerRequest>();
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
  GetIssuerResponse clone() => GetIssuerResponse()..mergeFromMessage(this);
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
  static $pb.PbList<GetIssuerResponse> createRepeated() =>
      $pb.PbList<GetIssuerResponse>();
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
  GetRoleRequest clone() => GetRoleRequest()..mergeFromMessage(this);
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
  static $pb.PbList<GetRoleRequest> createRepeated() =>
      $pb.PbList<GetRoleRequest>();
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
  GetRoleResponse clone() => GetRoleResponse()..mergeFromMessage(this);
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
  static $pb.PbList<GetRoleResponse> createRepeated() =>
      $pb.PbList<GetRoleResponse>();
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
  GetUserRequest clone() => GetUserRequest()..mergeFromMessage(this);
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
  static $pb.PbList<GetUserRequest> createRepeated() =>
      $pb.PbList<GetUserRequest>();
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
  GetUserResponse clone() => GetUserResponse()..mergeFromMessage(this);
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
  static $pb.PbList<GetUserResponse> createRepeated() =>
      $pb.PbList<GetUserResponse>();
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
    ..pc<SecurityIssuer>(
        1, _omitFieldNames ? '' : 'issuers', $pb.PbFieldType.PM,
        subBuilder: SecurityIssuer.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListIssuersResponse clone() => ListIssuersResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ListIssuersResponse> createRepeated() =>
      $pb.PbList<ListIssuersResponse>();
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
    ..pc<SecurityRole>(1, _omitFieldNames ? '' : 'roles', $pb.PbFieldType.PM,
        subBuilder: SecurityRole.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRolesResponse clone() => ListRolesResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ListRolesResponse> createRepeated() =>
      $pb.PbList<ListRolesResponse>();
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
    ..pc<SecurityUser>(1, _omitFieldNames ? '' : 'users', $pb.PbFieldType.PM,
        subBuilder: SecurityUser.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListUsersResponse clone() => ListUsersResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ListUsersResponse> createRepeated() =>
      $pb.PbList<ListUsersResponse>();
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
  ListRoleAssignmentsRequest clone() =>
      ListRoleAssignmentsRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ListRoleAssignmentsRequest> createRepeated() =>
      $pb.PbList<ListRoleAssignmentsRequest>();
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
    ..pc<SecurityRoleAssignment>(
        1, _omitFieldNames ? '' : 'assignments', $pb.PbFieldType.PM,
        subBuilder: SecurityRoleAssignment.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListRoleAssignmentsResponse clone() =>
      ListRoleAssignmentsResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ListRoleAssignmentsResponse> createRepeated() =>
      $pb.PbList<ListRoleAssignmentsResponse>();
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
    $0.Timestamp? occurredAt,
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
    ..aOM<$0.Timestamp>(5, _omitFieldNames ? '' : 'occurredAt',
        subBuilder: $0.Timestamp.create)
    ..aOS(6, _omitFieldNames ? '' : 'operation')
    ..pPS(7, _omitFieldNames ? '' : 'targetDigests')
    ..a<$core.int>(
        8, _omitFieldNames ? '' : 'additionalTargets', $pb.PbFieldType.OU3)
    ..aOS(9, _omitFieldNames ? '' : 'outcome')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityAuditRecord clone() => SecurityAuditRecord()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityAuditRecord> createRepeated() =>
      $pb.PbList<SecurityAuditRecord>();
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
  $0.Timestamp get occurredAt => $_getN(4);
  @$pb.TagNumber(5)
  set occurredAt($0.Timestamp value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasOccurredAt() => $_has(4);
  @$pb.TagNumber(5)
  void clearOccurredAt() => $_clearField(5);
  @$pb.TagNumber(5)
  $0.Timestamp ensureOccurredAt() => $_ensure(4);

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
  }) {
    final result = create();
    if (records != null) result.records.addAll(records);
    if (version != null) result.version = version;
    if (nextCursor != null) result.nextCursor = nextCursor;
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
    ..pc<SecurityAuditRecord>(
        1, _omitFieldNames ? '' : 'records', $pb.PbFieldType.PM,
        subBuilder: SecurityAuditRecord.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..aOS(3, _omitFieldNames ? '' : 'nextCursor')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ListSecurityAuditResponse clone() =>
      ListSecurityAuditResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ListSecurityAuditResponse> createRepeated() =>
      $pb.PbList<ListSecurityAuditResponse>();
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
  GetRoleTemplatesRequest clone() =>
      GetRoleTemplatesRequest()..mergeFromMessage(this);
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
  static $pb.PbList<GetRoleTemplatesRequest> createRepeated() =>
      $pb.PbList<GetRoleTemplatesRequest>();
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
    ..pc<SecurityRole>(1, _omitFieldNames ? '' : 'roles', $pb.PbFieldType.PM,
        subBuilder: SecurityRole.create)
    ..aOM<SecurityVersion>(2, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetRoleTemplatesResponse clone() =>
      GetRoleTemplatesResponse()..mergeFromMessage(this);
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
  static $pb.PbList<GetRoleTemplatesResponse> createRepeated() =>
      $pb.PbList<GetRoleTemplatesResponse>();
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
  SecurityEdgeIdentity clone() =>
      SecurityEdgeIdentity()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityEdgeIdentity> createRepeated() =>
      $pb.PbList<SecurityEdgeIdentity>();
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
  }) {
    final result = create();
    if (identity != null) result.identity = identity;
    if (action != null) result.action = action;
    if (logicalKey != null) result.logicalKey = logicalKey;
    if (edge != null) result.edge = edge;
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
    ..e<SecurityAction>(2, _omitFieldNames ? '' : 'action', $pb.PbFieldType.OE,
        defaultOrMaker: SecurityAction.SECURITY_ACTION_UNSPECIFIED,
        valueOf: SecurityAction.valueOf,
        enumValues: SecurityAction.values)
    ..aOS(3, _omitFieldNames ? '' : 'logicalKey')
    ..aOM<SecurityEdgeIdentity>(4, _omitFieldNames ? '' : 'edge',
        subBuilder: SecurityEdgeIdentity.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExplainAccessRequest clone() =>
      ExplainAccessRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ExplainAccessRequest> createRepeated() =>
      $pb.PbList<ExplainAccessRequest>();
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

  @$pb.TagNumber(2)
  SecurityAction get action => $_getN(1);
  @$pb.TagNumber(2)
  set action(SecurityAction value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasAction() => $_has(1);
  @$pb.TagNumber(2)
  void clearAction() => $_clearField(2);

  /// Omit both selectors for a global capability. Supplying both is invalid.
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
}

class SecurityRuleMatch extends $pb.GeneratedMessage {
  factory SecurityRuleMatch({
    $core.String? roleId,
    $core.String? ruleId,
    SecurityEffect? effect,
    SecurityAction? action,
    $core.String? endpoint,
  }) {
    final result = create();
    if (roleId != null) result.roleId = roleId;
    if (ruleId != null) result.ruleId = ruleId;
    if (effect != null) result.effect = effect;
    if (action != null) result.action = action;
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
    ..e<SecurityEffect>(3, _omitFieldNames ? '' : 'effect', $pb.PbFieldType.OE,
        defaultOrMaker: SecurityEffect.SECURITY_EFFECT_UNSPECIFIED,
        valueOf: SecurityEffect.valueOf,
        enumValues: SecurityEffect.values)
    ..e<SecurityAction>(4, _omitFieldNames ? '' : 'action', $pb.PbFieldType.OE,
        defaultOrMaker: SecurityAction.SECURITY_ACTION_UNSPECIFIED,
        valueOf: SecurityAction.valueOf,
        enumValues: SecurityAction.values)
    ..aOS(5, _omitFieldNames ? '' : 'endpoint')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityRuleMatch clone() => SecurityRuleMatch()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityRuleMatch> createRepeated() =>
      $pb.PbList<SecurityRuleMatch>();
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
  SecurityEffect get effect => $_getN(2);
  @$pb.TagNumber(3)
  set effect(SecurityEffect value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasEffect() => $_has(2);
  @$pb.TagNumber(3)
  void clearEffect() => $_clearField(3);

  /// Actual grantable capability required by this part of the operation.
  @$pb.TagNumber(4)
  SecurityAction get action => $_getN(3);
  @$pb.TagNumber(4)
  set action(SecurityAction value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasAction() => $_has(3);
  @$pb.TagNumber(4)
  void clearAction() => $_clearField(4);

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
    ..pc<SecurityRuleMatch>(
        2, _omitFieldNames ? '' : 'matches', $pb.PbFieldType.PM,
        subBuilder: SecurityRuleMatch.create)
    ..aOM<SecurityVersion>(3, _omitFieldNames ? '' : 'version',
        subBuilder: SecurityVersion.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ExplainAccessResponse clone() =>
      ExplainAccessResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ExplainAccessResponse> createRepeated() =>
      $pb.PbList<ExplainAccessResponse>();
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
  ValidateIssuerRequest clone() =>
      ValidateIssuerRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ValidateIssuerRequest> createRepeated() =>
      $pb.PbList<ValidateIssuerRequest>();
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
  ValidateIssuerResponse clone() =>
      ValidateIssuerResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ValidateIssuerResponse> createRepeated() =>
      $pb.PbList<ValidateIssuerResponse>();
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
    ..e<SecurityPrincipalState>(
        2, _omitFieldNames ? '' : 'state', $pb.PbFieldType.OE,
        defaultOrMaker:
            SecurityPrincipalState.SECURITY_PRINCIPAL_STATE_UNSPECIFIED,
        valueOf: SecurityPrincipalState.valueOf,
        enumValues: SecurityPrincipalState.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityUserStateChange clone() =>
      SecurityUserStateChange()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityUserStateChange> createRepeated() =>
      $pb.PbList<SecurityUserStateChange>();
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
    0: SecurityChange_Operation.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SecurityChange',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [1, 2, 3, 4, 5, 6, 7, 8, 9, 10])
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
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SecurityChange clone() => SecurityChange()..mergeFromMessage(this);
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
  static $pb.PbList<SecurityChange> createRepeated() =>
      $pb.PbList<SecurityChange>();
  @$core.pragma('dart2js:noInline')
  static SecurityChange getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SecurityChange>(create);
  static SecurityChange? _defaultInstance;

  SecurityChange_Operation whichOperation() =>
      _SecurityChange_OperationByTag[$_whichOneof(0)]!;
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
}

class ApplySecurityChangesRequest extends $pb.GeneratedMessage {
  factory ApplySecurityChangesRequest({
    $fixnum.Int64? expectedRevision,
    $core.List<$core.int>? changeId,
    $core.Iterable<SecurityChange>? changes,
  }) {
    final result = create();
    if (expectedRevision != null) result.expectedRevision = expectedRevision;
    if (changeId != null) result.changeId = changeId;
    if (changes != null) result.changes.addAll(changes);
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
    ..pc<SecurityChange>(
        3, _omitFieldNames ? '' : 'changes', $pb.PbFieldType.PM,
        subBuilder: SecurityChange.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangesRequest clone() =>
      ApplySecurityChangesRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ApplySecurityChangesRequest> createRepeated() =>
      $pb.PbList<ApplySecurityChangesRequest>();
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
}

class ApplySecurityChangesResponse extends $pb.GeneratedMessage {
  factory ApplySecurityChangesResponse({
    SecurityVersion? version,
    $core.Iterable<$core.bool>? applied,
    $core.bool? replayed,
    SecurityEnforcementState? enforcement,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (applied != null) result.applied.addAll(applied);
    if (replayed != null) result.replayed = replayed;
    if (enforcement != null) result.enforcement = enforcement;
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
    ..e<SecurityEnforcementState>(
        4, _omitFieldNames ? '' : 'enforcement', $pb.PbFieldType.OE,
        defaultOrMaker:
            SecurityEnforcementState.SECURITY_ENFORCEMENT_STATE_UNSPECIFIED,
        valueOf: SecurityEnforcementState.valueOf,
        enumValues: SecurityEnforcementState.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangesResponse clone() =>
      ApplySecurityChangesResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ApplySecurityChangesResponse> createRepeated() =>
      $pb.PbList<ApplySecurityChangesResponse>();
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
}

class ApplySecurityChangeRequest extends $pb.GeneratedMessage {
  factory ApplySecurityChangeRequest({
    $fixnum.Int64? expectedRevision,
    $core.List<$core.int>? changeId,
    SecurityChange? change,
  }) {
    final result = create();
    if (expectedRevision != null) result.expectedRevision = expectedRevision;
    if (changeId != null) result.changeId = changeId;
    if (change != null) result.change = change;
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
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangeRequest clone() =>
      ApplySecurityChangeRequest()..mergeFromMessage(this);
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
  static $pb.PbList<ApplySecurityChangeRequest> createRepeated() =>
      $pb.PbList<ApplySecurityChangeRequest>();
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
}

class ApplySecurityChangeResponse extends $pb.GeneratedMessage {
  factory ApplySecurityChangeResponse({
    SecurityVersion? version,
    $core.bool? applied,
    $core.bool? replayed,
    SecurityEnforcementState? enforcement,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (applied != null) result.applied = applied;
    if (replayed != null) result.replayed = replayed;
    if (enforcement != null) result.enforcement = enforcement;
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
    ..e<SecurityEnforcementState>(
        4, _omitFieldNames ? '' : 'enforcement', $pb.PbFieldType.OE,
        defaultOrMaker:
            SecurityEnforcementState.SECURITY_ENFORCEMENT_STATE_UNSPECIFIED,
        valueOf: SecurityEnforcementState.valueOf,
        enumValues: SecurityEnforcementState.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ApplySecurityChangeResponse clone() =>
      ApplySecurityChangeResponse()..mergeFromMessage(this);
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
  static $pb.PbList<ApplySecurityChangeResponse> createRepeated() =>
      $pb.PbList<ApplySecurityChangeResponse>();
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
}

class GetSecurityChangeStatusRequest extends $pb.GeneratedMessage {
  factory GetSecurityChangeStatusRequest({
    $core.List<$core.int>? changeId,
  }) {
    final result = create();
    if (changeId != null) result.changeId = changeId;
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
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeStatusRequest clone() =>
      GetSecurityChangeStatusRequest()..mergeFromMessage(this);
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
  static $pb.PbList<GetSecurityChangeStatusRequest> createRepeated() =>
      $pb.PbList<GetSecurityChangeStatusRequest>();
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
}

class GetSecurityChangeStatusResponse extends $pb.GeneratedMessage {
  factory GetSecurityChangeStatusResponse({
    SecurityVersion? version,
    $core.Iterable<$core.bool>? applied,
    $core.bool? replayed,
    SecurityEnforcementState? enforcement,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (applied != null) result.applied.addAll(applied);
    if (replayed != null) result.replayed = replayed;
    if (enforcement != null) result.enforcement = enforcement;
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
    ..p<$core.bool>(2, _omitFieldNames ? '' : 'applied', $pb.PbFieldType.KB)
    ..aOB(3, _omitFieldNames ? '' : 'replayed')
    ..e<SecurityEnforcementState>(
        4, _omitFieldNames ? '' : 'enforcement', $pb.PbFieldType.OE,
        defaultOrMaker:
            SecurityEnforcementState.SECURITY_ENFORCEMENT_STATE_UNSPECIFIED,
        valueOf: SecurityEnforcementState.valueOf,
        enumValues: SecurityEnforcementState.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetSecurityChangeStatusResponse clone() =>
      GetSecurityChangeStatusResponse()..mergeFromMessage(this);
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
  static $pb.PbList<GetSecurityChangeStatusResponse> createRepeated() =>
      $pb.PbList<GetSecurityChangeStatusResponse>();
  @$core.pragma('dart2js:noInline')
  static GetSecurityChangeStatusResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetSecurityChangeStatusResponse>(
          create);
  static GetSecurityChangeStatusResponse? _defaultInstance;

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
}

/// Security is a separate control plane. Logical data RPCs cannot write sys:.
/// User resources contain identity and Role membership, never direct grants.
class LanternSecurityServiceApi {
  final $pb.RpcClient _client;

  LanternSecurityServiceApi(this._client);

  $async.Future<GetAuthCapabilitiesResponse> getAuthCapabilities(
          $pb.ClientContext? ctx, GetAuthCapabilitiesRequest request) =>
      _client.invoke<GetAuthCapabilitiesResponse>(ctx, 'LanternSecurityService',
          'GetAuthCapabilities', request, GetAuthCapabilitiesResponse());
  $async.Future<GetCurrentPrincipalResponse> getCurrentPrincipal(
          $pb.ClientContext? ctx, GetCurrentPrincipalRequest request) =>
      _client.invoke<GetCurrentPrincipalResponse>(ctx, 'LanternSecurityService',
          'GetCurrentPrincipal', request, GetCurrentPrincipalResponse());
  $async.Future<ListIssuersResponse> listIssuers(
          $pb.ClientContext? ctx, ListIssuersRequest request) =>
      _client.invoke<ListIssuersResponse>(ctx, 'LanternSecurityService',
          'ListIssuers', request, ListIssuersResponse());
  $async.Future<GetIssuerResponse> getIssuer(
          $pb.ClientContext? ctx, GetIssuerRequest request) =>
      _client.invoke<GetIssuerResponse>(ctx, 'LanternSecurityService',
          'GetIssuer', request, GetIssuerResponse());
  $async.Future<ListRolesResponse> listRoles(
          $pb.ClientContext? ctx, ListRolesRequest request) =>
      _client.invoke<ListRolesResponse>(ctx, 'LanternSecurityService',
          'ListRoles', request, ListRolesResponse());
  $async.Future<GetRoleResponse> getRole(
          $pb.ClientContext? ctx, GetRoleRequest request) =>
      _client.invoke<GetRoleResponse>(
          ctx, 'LanternSecurityService', 'GetRole', request, GetRoleResponse());
  $async.Future<ListUsersResponse> listUsers(
          $pb.ClientContext? ctx, ListUsersRequest request) =>
      _client.invoke<ListUsersResponse>(ctx, 'LanternSecurityService',
          'ListUsers', request, ListUsersResponse());
  $async.Future<GetUserResponse> getUser(
          $pb.ClientContext? ctx, GetUserRequest request) =>
      _client.invoke<GetUserResponse>(
          ctx, 'LanternSecurityService', 'GetUser', request, GetUserResponse());
  $async.Future<ListRoleAssignmentsResponse> listRoleAssignments(
          $pb.ClientContext? ctx, ListRoleAssignmentsRequest request) =>
      _client.invoke<ListRoleAssignmentsResponse>(ctx, 'LanternSecurityService',
          'ListRoleAssignments', request, ListRoleAssignmentsResponse());
  $async.Future<ListSecurityAuditResponse> listSecurityAudit(
          $pb.ClientContext? ctx, ListSecurityAuditRequest request) =>
      _client.invoke<ListSecurityAuditResponse>(ctx, 'LanternSecurityService',
          'ListSecurityAudit', request, ListSecurityAuditResponse());
  $async.Future<GetRoleTemplatesResponse> getRoleTemplates(
          $pb.ClientContext? ctx, GetRoleTemplatesRequest request) =>
      _client.invoke<GetRoleTemplatesResponse>(ctx, 'LanternSecurityService',
          'GetRoleTemplates', request, GetRoleTemplatesResponse());
  $async.Future<ExplainAccessResponse> explainAccess(
          $pb.ClientContext? ctx, ExplainAccessRequest request) =>
      _client.invoke<ExplainAccessResponse>(ctx, 'LanternSecurityService',
          'ExplainAccess', request, ExplainAccessResponse());
  $async.Future<ValidateIssuerResponse> validateIssuer(
          $pb.ClientContext? ctx, ValidateIssuerRequest request) =>
      _client.invoke<ValidateIssuerResponse>(ctx, 'LanternSecurityService',
          'ValidateIssuer', request, ValidateIssuerResponse());

  /// Plural is canonical and atomic, with request-index-aligned outcomes.
  $async.Future<ApplySecurityChangesResponse> applySecurityChanges(
          $pb.ClientContext? ctx, ApplySecurityChangesRequest request) =>
      _client.invoke<ApplySecurityChangesResponse>(
          ctx,
          'LanternSecurityService',
          'ApplySecurityChanges',
          request,
          ApplySecurityChangesResponse());
  $async.Future<ApplySecurityChangeResponse> applySecurityChange(
          $pb.ClientContext? ctx, ApplySecurityChangeRequest request) =>
      _client.invoke<ApplySecurityChangeResponse>(ctx, 'LanternSecurityService',
          'ApplySecurityChange', request, ApplySecurityChangeResponse());
  $async.Future<GetSecurityChangeStatusResponse> getSecurityChangeStatus(
          $pb.ClientContext? ctx, GetSecurityChangeStatusRequest request) =>
      _client.invoke<GetSecurityChangeStatusResponse>(
          ctx,
          'LanternSecurityService',
          'GetSecurityChangeStatus',
          request,
          GetSecurityChangeStatusResponse());
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
