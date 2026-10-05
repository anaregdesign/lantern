// This is a generated file - do not edit.
//
// Generated from graph/v1/security.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

class AuthMode extends $pb.ProtobufEnum {
  static const AuthMode AUTH_MODE_UNSPECIFIED =
      AuthMode._(0, _omitEnumNames ? '' : 'AUTH_MODE_UNSPECIFIED');
  static const AuthMode AUTH_MODE_OFF =
      AuthMode._(1, _omitEnumNames ? '' : 'AUTH_MODE_OFF');
  static const AuthMode AUTH_MODE_OIDC =
      AuthMode._(2, _omitEnumNames ? '' : 'AUTH_MODE_OIDC');

  static const $core.List<AuthMode> values = <AuthMode>[
    AUTH_MODE_UNSPECIFIED,
    AUTH_MODE_OFF,
    AUTH_MODE_OIDC,
  ];

  static final $core.List<AuthMode?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static AuthMode? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const AuthMode._(super.value, super.name);
}

class SecurityPrincipalKind extends $pb.ProtobufEnum {
  static const SecurityPrincipalKind SECURITY_PRINCIPAL_KIND_UNSPECIFIED =
      SecurityPrincipalKind._(
          0, _omitEnumNames ? '' : 'SECURITY_PRINCIPAL_KIND_UNSPECIFIED');
  static const SecurityPrincipalKind SECURITY_PRINCIPAL_KIND_OIDC =
      SecurityPrincipalKind._(
          1, _omitEnumNames ? '' : 'SECURITY_PRINCIPAL_KIND_OIDC');
  static const SecurityPrincipalKind SECURITY_PRINCIPAL_KIND_MACHINE =
      SecurityPrincipalKind._(
          2, _omitEnumNames ? '' : 'SECURITY_PRINCIPAL_KIND_MACHINE');

  static const $core.List<SecurityPrincipalKind> values =
      <SecurityPrincipalKind>[
    SECURITY_PRINCIPAL_KIND_UNSPECIFIED,
    SECURITY_PRINCIPAL_KIND_OIDC,
    SECURITY_PRINCIPAL_KIND_MACHINE,
  ];

  static final $core.List<SecurityPrincipalKind?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static SecurityPrincipalKind? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityPrincipalKind._(super.value, super.name);
}

class SecurityPrincipalState extends $pb.ProtobufEnum {
  static const SecurityPrincipalState SECURITY_PRINCIPAL_STATE_UNSPECIFIED =
      SecurityPrincipalState._(
          0, _omitEnumNames ? '' : 'SECURITY_PRINCIPAL_STATE_UNSPECIFIED');
  static const SecurityPrincipalState SECURITY_PRINCIPAL_STATE_ACTIVE =
      SecurityPrincipalState._(
          1, _omitEnumNames ? '' : 'SECURITY_PRINCIPAL_STATE_ACTIVE');
  static const SecurityPrincipalState SECURITY_PRINCIPAL_STATE_SUSPENDED =
      SecurityPrincipalState._(
          2, _omitEnumNames ? '' : 'SECURITY_PRINCIPAL_STATE_SUSPENDED');
  static const SecurityPrincipalState SECURITY_PRINCIPAL_STATE_DELETED =
      SecurityPrincipalState._(
          3, _omitEnumNames ? '' : 'SECURITY_PRINCIPAL_STATE_DELETED');

  static const $core.List<SecurityPrincipalState> values =
      <SecurityPrincipalState>[
    SECURITY_PRINCIPAL_STATE_UNSPECIFIED,
    SECURITY_PRINCIPAL_STATE_ACTIVE,
    SECURITY_PRINCIPAL_STATE_SUSPENDED,
    SECURITY_PRINCIPAL_STATE_DELETED,
  ];

  static final $core.List<SecurityPrincipalState?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static SecurityPrincipalState? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityPrincipalState._(super.value, super.name);
}

class SecurityAction extends $pb.ProtobufEnum {
  static const SecurityAction SECURITY_ACTION_UNSPECIFIED =
      SecurityAction._(0, _omitEnumNames ? '' : 'SECURITY_ACTION_UNSPECIFIED');
  static const SecurityAction SECURITY_ACTION_VERTEX_READ =
      SecurityAction._(1, _omitEnumNames ? '' : 'SECURITY_ACTION_VERTEX_READ');
  static const SecurityAction SECURITY_ACTION_VERTEX_WRITE =
      SecurityAction._(2, _omitEnumNames ? '' : 'SECURITY_ACTION_VERTEX_WRITE');
  static const SecurityAction SECURITY_ACTION_VERTEX_DELETE = SecurityAction._(
      3, _omitEnumNames ? '' : 'SECURITY_ACTION_VERTEX_DELETE');
  static const SecurityAction SECURITY_ACTION_EDGE_READ =
      SecurityAction._(4, _omitEnumNames ? '' : 'SECURITY_ACTION_EDGE_READ');
  static const SecurityAction SECURITY_ACTION_EDGE_ADD =
      SecurityAction._(5, _omitEnumNames ? '' : 'SECURITY_ACTION_EDGE_ADD');
  static const SecurityAction SECURITY_ACTION_EDGE_WRITE =
      SecurityAction._(6, _omitEnumNames ? '' : 'SECURITY_ACTION_EDGE_WRITE');
  static const SecurityAction SECURITY_ACTION_EDGE_DELETE =
      SecurityAction._(7, _omitEnumNames ? '' : 'SECURITY_ACTION_EDGE_DELETE');
  static const SecurityAction SECURITY_ACTION_QUERY =
      SecurityAction._(8, _omitEnumNames ? '' : 'SECURITY_ACTION_QUERY');
  static const SecurityAction SECURITY_ACTION_CDC_IDENTITY =
      SecurityAction._(9, _omitEnumNames ? '' : 'SECURITY_ACTION_CDC_IDENTITY');
  static const SecurityAction SECURITY_ACTION_CDC_VALUE =
      SecurityAction._(10, _omitEnumNames ? '' : 'SECURITY_ACTION_CDC_VALUE');
  static const SecurityAction SECURITY_ACTION_EXPORT =
      SecurityAction._(11, _omitEnumNames ? '' : 'SECURITY_ACTION_EXPORT');
  static const SecurityAction SECURITY_ACTION_RECEIPT_READ = SecurityAction._(
      12, _omitEnumNames ? '' : 'SECURITY_ACTION_RECEIPT_READ');
  static const SecurityAction SECURITY_ACTION_OPERATIONS_READ =
      SecurityAction._(
          13, _omitEnumNames ? '' : 'SECURITY_ACTION_OPERATIONS_READ');
  static const SecurityAction SECURITY_ACTION_SCHEMA_READ =
      SecurityAction._(14, _omitEnumNames ? '' : 'SECURITY_ACTION_SCHEMA_READ');
  static const SecurityAction SECURITY_ACTION_MANAGE =
      SecurityAction._(15, _omitEnumNames ? '' : 'SECURITY_ACTION_MANAGE');
  static const SecurityAction SECURITY_ACTION_EDGE_CREATE =
      SecurityAction._(16, _omitEnumNames ? '' : 'SECURITY_ACTION_EDGE_CREATE');

  static const $core.List<SecurityAction> values = <SecurityAction>[
    SECURITY_ACTION_UNSPECIFIED,
    SECURITY_ACTION_VERTEX_READ,
    SECURITY_ACTION_VERTEX_WRITE,
    SECURITY_ACTION_VERTEX_DELETE,
    SECURITY_ACTION_EDGE_READ,
    SECURITY_ACTION_EDGE_ADD,
    SECURITY_ACTION_EDGE_WRITE,
    SECURITY_ACTION_EDGE_DELETE,
    SECURITY_ACTION_QUERY,
    SECURITY_ACTION_CDC_IDENTITY,
    SECURITY_ACTION_CDC_VALUE,
    SECURITY_ACTION_EXPORT,
    SECURITY_ACTION_RECEIPT_READ,
    SECURITY_ACTION_OPERATIONS_READ,
    SECURITY_ACTION_SCHEMA_READ,
    SECURITY_ACTION_MANAGE,
    SECURITY_ACTION_EDGE_CREATE,
  ];

  static final $core.List<SecurityAction?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 16);
  static SecurityAction? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityAction._(super.value, super.name);
}

class SecurityEffect extends $pb.ProtobufEnum {
  static const SecurityEffect SECURITY_EFFECT_UNSPECIFIED =
      SecurityEffect._(0, _omitEnumNames ? '' : 'SECURITY_EFFECT_UNSPECIFIED');
  static const SecurityEffect SECURITY_EFFECT_ALLOW =
      SecurityEffect._(1, _omitEnumNames ? '' : 'SECURITY_EFFECT_ALLOW');
  static const SecurityEffect SECURITY_EFFECT_DENY =
      SecurityEffect._(2, _omitEnumNames ? '' : 'SECURITY_EFFECT_DENY');

  static const $core.List<SecurityEffect> values = <SecurityEffect>[
    SECURITY_EFFECT_UNSPECIFIED,
    SECURITY_EFFECT_ALLOW,
    SECURITY_EFFECT_DENY,
  ];

  static final $core.List<SecurityEffect?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static SecurityEffect? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityEffect._(super.value, super.name);
}

class SecurityEnforcementState extends $pb.ProtobufEnum {
  static const SecurityEnforcementState SECURITY_ENFORCEMENT_STATE_UNSPECIFIED =
      SecurityEnforcementState._(
          0, _omitEnumNames ? '' : 'SECURITY_ENFORCEMENT_STATE_UNSPECIFIED');
  static const SecurityEnforcementState
      SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING = SecurityEnforcementState._(
          1,
          _omitEnumNames ? '' : 'SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING');
  static const SecurityEnforcementState SECURITY_ENFORCEMENT_STATE_ENFORCED =
      SecurityEnforcementState._(
          2, _omitEnumNames ? '' : 'SECURITY_ENFORCEMENT_STATE_ENFORCED');

  static const $core.List<SecurityEnforcementState> values =
      <SecurityEnforcementState>[
    SECURITY_ENFORCEMENT_STATE_UNSPECIFIED,
    SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING,
    SECURITY_ENFORCEMENT_STATE_ENFORCED,
  ];

  static final $core.List<SecurityEnforcementState?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static SecurityEnforcementState? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityEnforcementState._(super.value, super.name);
}

class SecurityAuthorizationRequirement extends $pb.ProtobufEnum {
  static const SecurityAuthorizationRequirement
      SECURITY_AUTHORIZATION_REQUIREMENT_UNSPECIFIED =
      SecurityAuthorizationRequirement._(
          0,
          _omitEnumNames
              ? ''
              : 'SECURITY_AUTHORIZATION_REQUIREMENT_UNSPECIFIED');
  static const SecurityAuthorizationRequirement
      SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY =
      SecurityAuthorizationRequirement._(1,
          _omitEnumNames ? '' : 'SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY');
  static const SecurityAuthorizationRequirement
      SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION =
      SecurityAuthorizationRequirement._(
          2,
          _omitEnumNames
              ? ''
              : 'SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION');

  static const $core.List<SecurityAuthorizationRequirement> values =
      <SecurityAuthorizationRequirement>[
    SECURITY_AUTHORIZATION_REQUIREMENT_UNSPECIFIED,
    SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY,
    SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION,
  ];

  static final $core.List<SecurityAuthorizationRequirement?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static SecurityAuthorizationRequirement? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityAuthorizationRequirement._(super.value, super.name);
}

class SecurityAuthorizationState extends $pb.ProtobufEnum {
  static const SecurityAuthorizationState
      SECURITY_AUTHORIZATION_STATE_UNSPECIFIED = SecurityAuthorizationState._(
          0, _omitEnumNames ? '' : 'SECURITY_AUTHORIZATION_STATE_UNSPECIFIED');
  static const SecurityAuthorizationState SECURITY_AUTHORIZATION_STATE_PENDING =
      SecurityAuthorizationState._(
          1, _omitEnumNames ? '' : 'SECURITY_AUTHORIZATION_STATE_PENDING');
  static const SecurityAuthorizationState
      SECURITY_AUTHORIZATION_STATE_APPROVED = SecurityAuthorizationState._(
          2, _omitEnumNames ? '' : 'SECURITY_AUTHORIZATION_STATE_APPROVED');
  static const SecurityAuthorizationState SECURITY_AUTHORIZATION_STATE_DENIED =
      SecurityAuthorizationState._(
          3, _omitEnumNames ? '' : 'SECURITY_AUTHORIZATION_STATE_DENIED');

  static const $core.List<SecurityAuthorizationState> values =
      <SecurityAuthorizationState>[
    SECURITY_AUTHORIZATION_STATE_UNSPECIFIED,
    SECURITY_AUTHORIZATION_STATE_PENDING,
    SECURITY_AUTHORIZATION_STATE_APPROVED,
    SECURITY_AUTHORIZATION_STATE_DENIED,
  ];

  static final $core.List<SecurityAuthorizationState?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static SecurityAuthorizationState? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityAuthorizationState._(super.value, super.name);
}

const $core.bool _omitEnumNames =
    $core.bool.fromEnvironment('protobuf.omit_enum_names');
