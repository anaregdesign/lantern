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

class NativeClientKind extends $pb.ProtobufEnum {
  static const NativeClientKind NATIVE_CLIENT_KIND_UNSPECIFIED =
      NativeClientKind._(
          0, _omitEnumNames ? '' : 'NATIVE_CLIENT_KIND_UNSPECIFIED');
  static const NativeClientKind NATIVE_CLIENT_KIND_GOOGLE_ID_TOKEN =
      NativeClientKind._(
          1, _omitEnumNames ? '' : 'NATIVE_CLIENT_KIND_GOOGLE_ID_TOKEN');
  static const NativeClientKind NATIVE_CLIENT_KIND_APPLE_NATIVE =
      NativeClientKind._(
          2, _omitEnumNames ? '' : 'NATIVE_CLIENT_KIND_APPLE_NATIVE');
  static const NativeClientKind NATIVE_CLIENT_KIND_APPLE_ANDROID_WEB =
      NativeClientKind._(
          3, _omitEnumNames ? '' : 'NATIVE_CLIENT_KIND_APPLE_ANDROID_WEB');

  static const $core.List<NativeClientKind> values = <NativeClientKind>[
    NATIVE_CLIENT_KIND_UNSPECIFIED,
    NATIVE_CLIENT_KIND_GOOGLE_ID_TOKEN,
    NATIVE_CLIENT_KIND_APPLE_NATIVE,
    NATIVE_CLIENT_KIND_APPLE_ANDROID_WEB,
  ];

  static final $core.List<NativeClientKind?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static NativeClientKind? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const NativeClientKind._(super.value, super.name);
}

class ManagedLogLevel extends $pb.ProtobufEnum {
  static const ManagedLogLevel MANAGED_LOG_LEVEL_UNSPECIFIED =
      ManagedLogLevel._(
          0, _omitEnumNames ? '' : 'MANAGED_LOG_LEVEL_UNSPECIFIED');
  static const ManagedLogLevel MANAGED_LOG_LEVEL_DEBUG =
      ManagedLogLevel._(1, _omitEnumNames ? '' : 'MANAGED_LOG_LEVEL_DEBUG');
  static const ManagedLogLevel MANAGED_LOG_LEVEL_INFO =
      ManagedLogLevel._(2, _omitEnumNames ? '' : 'MANAGED_LOG_LEVEL_INFO');
  static const ManagedLogLevel MANAGED_LOG_LEVEL_WARN =
      ManagedLogLevel._(3, _omitEnumNames ? '' : 'MANAGED_LOG_LEVEL_WARN');
  static const ManagedLogLevel MANAGED_LOG_LEVEL_ERROR =
      ManagedLogLevel._(4, _omitEnumNames ? '' : 'MANAGED_LOG_LEVEL_ERROR');

  static const $core.List<ManagedLogLevel> values = <ManagedLogLevel>[
    MANAGED_LOG_LEVEL_UNSPECIFIED,
    MANAGED_LOG_LEVEL_DEBUG,
    MANAGED_LOG_LEVEL_INFO,
    MANAGED_LOG_LEVEL_WARN,
    MANAGED_LOG_LEVEL_ERROR,
  ];

  static final $core.List<ManagedLogLevel?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static ManagedLogLevel? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const ManagedLogLevel._(super.value, super.name);
}

class LogSettingsSource extends $pb.ProtobufEnum {
  static const LogSettingsSource LOG_SETTINGS_SOURCE_UNSPECIFIED =
      LogSettingsSource._(
          0, _omitEnumNames ? '' : 'LOG_SETTINGS_SOURCE_UNSPECIFIED');
  static const LogSettingsSource LOG_SETTINGS_SOURCE_DEFAULT =
      LogSettingsSource._(
          1, _omitEnumNames ? '' : 'LOG_SETTINGS_SOURCE_DEFAULT');
  static const LogSettingsSource LOG_SETTINGS_SOURCE_ENVIRONMENT =
      LogSettingsSource._(
          2, _omitEnumNames ? '' : 'LOG_SETTINGS_SOURCE_ENVIRONMENT');
  static const LogSettingsSource LOG_SETTINGS_SOURCE_MANAGED =
      LogSettingsSource._(
          3, _omitEnumNames ? '' : 'LOG_SETTINGS_SOURCE_MANAGED');

  static const $core.List<LogSettingsSource> values = <LogSettingsSource>[
    LOG_SETTINGS_SOURCE_UNSPECIFIED,
    LOG_SETTINGS_SOURCE_DEFAULT,
    LOG_SETTINGS_SOURCE_ENVIRONMENT,
    LOG_SETTINGS_SOURCE_MANAGED,
  ];

  static final $core.List<LogSettingsSource?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static LogSettingsSource? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const LogSettingsSource._(super.value, super.name);
}

class LogPolicyAdoptionState extends $pb.ProtobufEnum {
  static const LogPolicyAdoptionState LOG_POLICY_ADOPTION_STATE_UNSPECIFIED =
      LogPolicyAdoptionState._(
          0, _omitEnumNames ? '' : 'LOG_POLICY_ADOPTION_STATE_UNSPECIFIED');
  static const LogPolicyAdoptionState LOG_POLICY_ADOPTION_STATE_UNADOPTED =
      LogPolicyAdoptionState._(
          1, _omitEnumNames ? '' : 'LOG_POLICY_ADOPTION_STATE_UNADOPTED');

  /// No policy has committed, but this node has a durable format floor.
  static const LogPolicyAdoptionState
      LOG_POLICY_ADOPTION_STATE_FENCED_UNADOPTED = LogPolicyAdoptionState._(2,
          _omitEnumNames ? '' : 'LOG_POLICY_ADOPTION_STATE_FENCED_UNADOPTED');
  static const LogPolicyAdoptionState LOG_POLICY_ADOPTION_STATE_MANAGED =
      LogPolicyAdoptionState._(
          3, _omitEnumNames ? '' : 'LOG_POLICY_ADOPTION_STATE_MANAGED');

  static const $core.List<LogPolicyAdoptionState> values =
      <LogPolicyAdoptionState>[
    LOG_POLICY_ADOPTION_STATE_UNSPECIFIED,
    LOG_POLICY_ADOPTION_STATE_UNADOPTED,
    LOG_POLICY_ADOPTION_STATE_FENCED_UNADOPTED,
    LOG_POLICY_ADOPTION_STATE_MANAGED,
  ];

  static final $core.List<LogPolicyAdoptionState?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static LogPolicyAdoptionState? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const LogPolicyAdoptionState._(super.value, super.name);
}

class LogSettingsApplicationState extends $pb.ProtobufEnum {
  static const LogSettingsApplicationState
      LOG_SETTINGS_APPLICATION_STATE_UNSPECIFIED =
      LogSettingsApplicationState._(0,
          _omitEnumNames ? '' : 'LOG_SETTINGS_APPLICATION_STATE_UNSPECIFIED');
  static const LogSettingsApplicationState
      LOG_SETTINGS_APPLICATION_STATE_APPLIED = LogSettingsApplicationState._(
          1, _omitEnumNames ? '' : 'LOG_SETTINGS_APPLICATION_STATE_APPLIED');
  static const LogSettingsApplicationState
      LOG_SETTINGS_APPLICATION_STATE_PENDING = LogSettingsApplicationState._(
          2, _omitEnumNames ? '' : 'LOG_SETTINGS_APPLICATION_STATE_PENDING');
  static const LogSettingsApplicationState
      LOG_SETTINGS_APPLICATION_STATE_ERROR = LogSettingsApplicationState._(
          3, _omitEnumNames ? '' : 'LOG_SETTINGS_APPLICATION_STATE_ERROR');

  static const $core.List<LogSettingsApplicationState> values =
      <LogSettingsApplicationState>[
    LOG_SETTINGS_APPLICATION_STATE_UNSPECIFIED,
    LOG_SETTINGS_APPLICATION_STATE_APPLIED,
    LOG_SETTINGS_APPLICATION_STATE_PENDING,
    LOG_SETTINGS_APPLICATION_STATE_ERROR,
  ];

  static final $core.List<LogSettingsApplicationState?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static LogSettingsApplicationState? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const LogSettingsApplicationState._(super.value, super.name);
}

class LogSettingsError extends $pb.ProtobufEnum {
  static const LogSettingsError LOG_SETTINGS_ERROR_UNSPECIFIED =
      LogSettingsError._(
          0, _omitEnumNames ? '' : 'LOG_SETTINGS_ERROR_UNSPECIFIED');
  static const LogSettingsError LOG_SETTINGS_ERROR_NONE =
      LogSettingsError._(1, _omitEnumNames ? '' : 'LOG_SETTINGS_ERROR_NONE');
  static const LogSettingsError LOG_SETTINGS_ERROR_INVALID_POLICY =
      LogSettingsError._(
          2, _omitEnumNames ? '' : 'LOG_SETTINGS_ERROR_INVALID_POLICY');
  static const LogSettingsError LOG_SETTINGS_ERROR_OWNER_UNAVAILABLE =
      LogSettingsError._(
          3, _omitEnumNames ? '' : 'LOG_SETTINGS_ERROR_OWNER_UNAVAILABLE');
  static const LogSettingsError LOG_SETTINGS_ERROR_APPLY_FAILED =
      LogSettingsError._(
          4, _omitEnumNames ? '' : 'LOG_SETTINGS_ERROR_APPLY_FAILED');

  static const $core.List<LogSettingsError> values = <LogSettingsError>[
    LOG_SETTINGS_ERROR_UNSPECIFIED,
    LOG_SETTINGS_ERROR_NONE,
    LOG_SETTINGS_ERROR_INVALID_POLICY,
    LOG_SETTINGS_ERROR_OWNER_UNAVAILABLE,
    LOG_SETTINGS_ERROR_APPLY_FAILED,
  ];

  static final $core.List<LogSettingsError?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static LogSettingsError? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const LogSettingsError._(super.value, super.name);
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

class SecurityChangeRejectionReason extends $pb.ProtobufEnum {
  static const SecurityChangeRejectionReason
      SECURITY_CHANGE_REJECTION_REASON_UNSPECIFIED =
      SecurityChangeRejectionReason._(0,
          _omitEnumNames ? '' : 'SECURITY_CHANGE_REJECTION_REASON_UNSPECIFIED');
  static const SecurityChangeRejectionReason
      SECURITY_CHANGE_REJECTION_REASON_INVALID_CHANGES =
      SecurityChangeRejectionReason._(
          1,
          _omitEnumNames
              ? ''
              : 'SECURITY_CHANGE_REJECTION_REASON_INVALID_CHANGES');
  static const SecurityChangeRejectionReason
      SECURITY_CHANGE_REJECTION_REASON_UNKNOWN_ROLE =
      SecurityChangeRejectionReason._(
          2,
          _omitEnumNames
              ? ''
              : 'SECURITY_CHANGE_REJECTION_REASON_UNKNOWN_ROLE');
  static const SecurityChangeRejectionReason
      SECURITY_CHANGE_REJECTION_REASON_ISSUER_VALIDATION =
      SecurityChangeRejectionReason._(
          3,
          _omitEnumNames
              ? ''
              : 'SECURITY_CHANGE_REJECTION_REASON_ISSUER_VALIDATION');
  static const SecurityChangeRejectionReason
      SECURITY_CHANGE_REJECTION_REASON_ENVIRONMENT_OWNED =
      SecurityChangeRejectionReason._(
          4,
          _omitEnumNames
              ? ''
              : 'SECURITY_CHANGE_REJECTION_REASON_ENVIRONMENT_OWNED');
  static const SecurityChangeRejectionReason
      SECURITY_CHANGE_REJECTION_REASON_LAST_ADMINISTRATOR =
      SecurityChangeRejectionReason._(
          5,
          _omitEnumNames
              ? ''
              : 'SECURITY_CHANGE_REJECTION_REASON_LAST_ADMINISTRATOR');
  static const SecurityChangeRejectionReason
      SECURITY_CHANGE_REJECTION_REASON_REVISION_CONFLICT =
      SecurityChangeRejectionReason._(
          6,
          _omitEnumNames
              ? ''
              : 'SECURITY_CHANGE_REJECTION_REASON_REVISION_CONFLICT');

  static const $core.List<SecurityChangeRejectionReason> values =
      <SecurityChangeRejectionReason>[
    SECURITY_CHANGE_REJECTION_REASON_UNSPECIFIED,
    SECURITY_CHANGE_REJECTION_REASON_INVALID_CHANGES,
    SECURITY_CHANGE_REJECTION_REASON_UNKNOWN_ROLE,
    SECURITY_CHANGE_REJECTION_REASON_ISSUER_VALIDATION,
    SECURITY_CHANGE_REJECTION_REASON_ENVIRONMENT_OWNED,
    SECURITY_CHANGE_REJECTION_REASON_LAST_ADMINISTRATOR,
    SECURITY_CHANGE_REJECTION_REASON_REVISION_CONFLICT,
  ];

  static final $core.List<SecurityChangeRejectionReason?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 6);
  static SecurityChangeRejectionReason? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityChangeRejectionReason._(super.value, super.name);
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

class CurrentSecurityDisposition extends $pb.ProtobufEnum {
  static const CurrentSecurityDisposition
      CURRENT_SECURITY_DISPOSITION_UNSPECIFIED = CurrentSecurityDisposition._(
          0, _omitEnumNames ? '' : 'CURRENT_SECURITY_DISPOSITION_UNSPECIFIED');
  static const CurrentSecurityDisposition CURRENT_SECURITY_DISPOSITION_APPLIED =
      CurrentSecurityDisposition._(
          1, _omitEnumNames ? '' : 'CURRENT_SECURITY_DISPOSITION_APPLIED');
  static const CurrentSecurityDisposition
      CURRENT_SECURITY_DISPOSITION_REJECTED_CAS = CurrentSecurityDisposition._(
          2, _omitEnumNames ? '' : 'CURRENT_SECURITY_DISPOSITION_REJECTED_CAS');
  static const CurrentSecurityDisposition
      CURRENT_SECURITY_DISPOSITION_REJECTED_ADMIN =
      CurrentSecurityDisposition._(3,
          _omitEnumNames ? '' : 'CURRENT_SECURITY_DISPOSITION_REJECTED_ADMIN');
  static const CurrentSecurityDisposition
      CURRENT_SECURITY_DISPOSITION_REJECTED_AUTHORITY =
      CurrentSecurityDisposition._(
          4,
          _omitEnumNames
              ? ''
              : 'CURRENT_SECURITY_DISPOSITION_REJECTED_AUTHORITY');
  static const CurrentSecurityDisposition
      CURRENT_SECURITY_DISPOSITION_REJECTED_PURPOSE =
      CurrentSecurityDisposition._(
          5,
          _omitEnumNames
              ? ''
              : 'CURRENT_SECURITY_DISPOSITION_REJECTED_PURPOSE');
  static const CurrentSecurityDisposition
      CURRENT_SECURITY_DISPOSITION_REJECTED_CAPACITY =
      CurrentSecurityDisposition._(
          6,
          _omitEnumNames
              ? ''
              : 'CURRENT_SECURITY_DISPOSITION_REJECTED_CAPACITY');
  static const CurrentSecurityDisposition
      CURRENT_SECURITY_DISPOSITION_REJECTED_INVARIANT =
      CurrentSecurityDisposition._(
          7,
          _omitEnumNames
              ? ''
              : 'CURRENT_SECURITY_DISPOSITION_REJECTED_INVARIANT');

  static const $core.List<CurrentSecurityDisposition> values =
      <CurrentSecurityDisposition>[
    CURRENT_SECURITY_DISPOSITION_UNSPECIFIED,
    CURRENT_SECURITY_DISPOSITION_APPLIED,
    CURRENT_SECURITY_DISPOSITION_REJECTED_CAS,
    CURRENT_SECURITY_DISPOSITION_REJECTED_ADMIN,
    CURRENT_SECURITY_DISPOSITION_REJECTED_AUTHORITY,
    CURRENT_SECURITY_DISPOSITION_REJECTED_PURPOSE,
    CURRENT_SECURITY_DISPOSITION_REJECTED_CAPACITY,
    CURRENT_SECURITY_DISPOSITION_REJECTED_INVARIANT,
  ];

  static final $core.List<CurrentSecurityDisposition?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 7);
  static CurrentSecurityDisposition? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const CurrentSecurityDisposition._(super.value, super.name);
}

class CurrentSecurityProgress extends $pb.ProtobufEnum {
  static const CurrentSecurityProgress CURRENT_SECURITY_PROGRESS_UNSPECIFIED =
      CurrentSecurityProgress._(
          0, _omitEnumNames ? '' : 'CURRENT_SECURITY_PROGRESS_UNSPECIFIED');

  /// Local absence/timeout/retirement never proves safe nonexecution.
  static const CurrentSecurityProgress CURRENT_SECURITY_PROGRESS_UNRESOLVED =
      CurrentSecurityProgress._(
          1, _omitEnumNames ? '' : 'CURRENT_SECURITY_PROGRESS_UNRESOLVED');
  static const CurrentSecurityProgress
      CURRENT_SECURITY_PROGRESS_ORIGIN_DURABLE = CurrentSecurityProgress._(
          2, _omitEnumNames ? '' : 'CURRENT_SECURITY_PROGRESS_ORIGIN_DURABLE');
  static const CurrentSecurityProgress CURRENT_SECURITY_PROGRESS_CHOSEN =
      CurrentSecurityProgress._(
          3, _omitEnumNames ? '' : 'CURRENT_SECURITY_PROGRESS_CHOSEN');
  static const CurrentSecurityProgress CURRENT_SECURITY_PROGRESS_APPLIED =
      CurrentSecurityProgress._(
          4, _omitEnumNames ? '' : 'CURRENT_SECURITY_PROGRESS_APPLIED');

  static const $core.List<CurrentSecurityProgress> values =
      <CurrentSecurityProgress>[
    CURRENT_SECURITY_PROGRESS_UNSPECIFIED,
    CURRENT_SECURITY_PROGRESS_UNRESOLVED,
    CURRENT_SECURITY_PROGRESS_ORIGIN_DURABLE,
    CURRENT_SECURITY_PROGRESS_CHOSEN,
    CURRENT_SECURITY_PROGRESS_APPLIED,
  ];

  static final $core.List<CurrentSecurityProgress?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static CurrentSecurityProgress? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const CurrentSecurityProgress._(super.value, super.name);
}

class CurrentAuthorizationStopObservation extends $pb.ProtobufEnum {
  static const CurrentAuthorizationStopObservation
      CURRENT_AUTHORIZATION_STOP_OBSERVATION_UNSPECIFIED =
      CurrentAuthorizationStopObservation._(
          0,
          _omitEnumNames
              ? ''
              : 'CURRENT_AUTHORIZATION_STOP_OBSERVATION_UNSPECIFIED');
  static const CurrentAuthorizationStopObservation
      CURRENT_AUTHORIZATION_STOP_OBSERVATION_NOT_OBSERVED =
      CurrentAuthorizationStopObservation._(
          1,
          _omitEnumNames
              ? ''
              : 'CURRENT_AUTHORIZATION_STOP_OBSERVATION_NOT_OBSERVED');
  static const CurrentAuthorizationStopObservation
      CURRENT_AUTHORIZATION_STOP_OBSERVATION_WAITING =
      CurrentAuthorizationStopObservation._(
          2,
          _omitEnumNames
              ? ''
              : 'CURRENT_AUTHORIZATION_STOP_OBSERVATION_WAITING');

  /// New authorizations using old cuts have stopped under this profile. This
  /// does not assert physical delivery or termination of an already authorized A.
  static const CurrentAuthorizationStopObservation
      CURRENT_AUTHORIZATION_STOP_OBSERVATION_OLD_CUT_NEW_AUTHORIZATIONS_STOPPED =
      CurrentAuthorizationStopObservation._(
          3,
          _omitEnumNames
              ? ''
              : 'CURRENT_AUTHORIZATION_STOP_OBSERVATION_OLD_CUT_NEW_AUTHORIZATIONS_STOPPED');

  static const $core.List<CurrentAuthorizationStopObservation> values =
      <CurrentAuthorizationStopObservation>[
    CURRENT_AUTHORIZATION_STOP_OBSERVATION_UNSPECIFIED,
    CURRENT_AUTHORIZATION_STOP_OBSERVATION_NOT_OBSERVED,
    CURRENT_AUTHORIZATION_STOP_OBSERVATION_WAITING,
    CURRENT_AUTHORIZATION_STOP_OBSERVATION_OLD_CUT_NEW_AUTHORIZATIONS_STOPPED,
  ];

  static final $core.List<CurrentAuthorizationStopObservation?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static CurrentAuthorizationStopObservation? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const CurrentAuthorizationStopObservation._(super.value, super.name);
}

const $core.bool _omitEnumNames =
    $core.bool.fromEnvironment('protobuf.omit_enum_names');
