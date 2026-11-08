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
  static const SecurityAction SECURITY_ACTION_SECURITY_READ = SecurityAction._(
      17, _omitEnumNames ? '' : 'SECURITY_ACTION_SECURITY_READ');
  static const SecurityAction SECURITY_ACTION_SETTINGS_READ = SecurityAction._(
      18, _omitEnumNames ? '' : 'SECURITY_ACTION_SETTINGS_READ');
  static const SecurityAction SECURITY_ACTION_SETTINGS_WRITE = SecurityAction._(
      19, _omitEnumNames ? '' : 'SECURITY_ACTION_SETTINGS_WRITE');

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
    SECURITY_ACTION_SECURITY_READ,
    SECURITY_ACTION_SETTINGS_READ,
    SECURITY_ACTION_SETTINGS_WRITE,
  ];

  static final $core.List<SecurityAction?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 19);
  static SecurityAction? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityAction._(super.value, super.name);
}

/// Role grants expose exactly two independent verbs on typed resources.
class SecurityPermission extends $pb.ProtobufEnum {
  static const SecurityPermission SECURITY_PERMISSION_UNSPECIFIED =
      SecurityPermission._(
          0, _omitEnumNames ? '' : 'SECURITY_PERMISSION_UNSPECIFIED');
  static const SecurityPermission SECURITY_PERMISSION_READ =
      SecurityPermission._(1, _omitEnumNames ? '' : 'SECURITY_PERMISSION_READ');
  static const SecurityPermission SECURITY_PERMISSION_WRITE =
      SecurityPermission._(
          2, _omitEnumNames ? '' : 'SECURITY_PERMISSION_WRITE');

  static const $core.List<SecurityPermission> values = <SecurityPermission>[
    SECURITY_PERMISSION_UNSPECIFIED,
    SECURITY_PERMISSION_READ,
    SECURITY_PERMISSION_WRITE,
  ];

  static final $core.List<SecurityPermission?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static SecurityPermission? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityPermission._(super.value, super.name);
}

class SecurityResource extends $pb.ProtobufEnum {
  static const SecurityResource SECURITY_RESOURCE_UNSPECIFIED =
      SecurityResource._(
          0, _omitEnumNames ? '' : 'SECURITY_RESOURCE_UNSPECIFIED');
  static const SecurityResource SECURITY_RESOURCE_DATA =
      SecurityResource._(1, _omitEnumNames ? '' : 'SECURITY_RESOURCE_DATA');
  static const SecurityResource SECURITY_RESOURCE_SECURITY =
      SecurityResource._(2, _omitEnumNames ? '' : 'SECURITY_RESOURCE_SECURITY');
  static const SecurityResource SECURITY_RESOURCE_SETTINGS =
      SecurityResource._(3, _omitEnumNames ? '' : 'SECURITY_RESOURCE_SETTINGS');
  static const SecurityResource SECURITY_RESOURCE_OPERATIONS =
      SecurityResource._(
          4, _omitEnumNames ? '' : 'SECURITY_RESOURCE_OPERATIONS');
  static const SecurityResource SECURITY_RESOURCE_SCHEMA =
      SecurityResource._(5, _omitEnumNames ? '' : 'SECURITY_RESOURCE_SCHEMA');

  static const $core.List<SecurityResource> values = <SecurityResource>[
    SECURITY_RESOURCE_UNSPECIFIED,
    SECURITY_RESOURCE_DATA,
    SECURITY_RESOURCE_SECURITY,
    SECURITY_RESOURCE_SETTINGS,
    SECURITY_RESOURCE_OPERATIONS,
    SECURITY_RESOURCE_SCHEMA,
  ];

  static final $core.List<SecurityResource?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 5);
  static SecurityResource? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SecurityResource._(super.value, super.name);
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

class RegistrationProfileKind extends $pb.ProtobufEnum {
  static const RegistrationProfileKind REGISTRATION_PROFILE_KIND_UNSPECIFIED =
      RegistrationProfileKind._(
          0, _omitEnumNames ? '' : 'REGISTRATION_PROFILE_KIND_UNSPECIFIED');
  static const RegistrationProfileKind REGISTRATION_PROFILE_KIND_NATIVE =
      RegistrationProfileKind._(
          1, _omitEnumNames ? '' : 'REGISTRATION_PROFILE_KIND_NATIVE');
  static const RegistrationProfileKind REGISTRATION_PROFILE_KIND_BROWSER =
      RegistrationProfileKind._(
          2, _omitEnumNames ? '' : 'REGISTRATION_PROFILE_KIND_BROWSER');

  static const $core.List<RegistrationProfileKind> values =
      <RegistrationProfileKind>[
    REGISTRATION_PROFILE_KIND_UNSPECIFIED,
    REGISTRATION_PROFILE_KIND_NATIVE,
    REGISTRATION_PROFILE_KIND_BROWSER,
  ];

  static final $core.List<RegistrationProfileKind?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static RegistrationProfileKind? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const RegistrationProfileKind._(super.value, super.name);
}

/// One cluster-wide endpoint predicate, captured with the immutable Role cut.
class EdgeEndpointMode extends $pb.ProtobufEnum {
  static const EdgeEndpointMode EDGE_ENDPOINT_MODE_UNSPECIFIED =
      EdgeEndpointMode._(
          0, _omitEnumNames ? '' : 'EDGE_ENDPOINT_MODE_UNSPECIFIED');
  static const EdgeEndpointMode EDGE_ENDPOINT_MODE_READ_READ =
      EdgeEndpointMode._(
          1, _omitEnumNames ? '' : 'EDGE_ENDPOINT_MODE_READ_READ');
  static const EdgeEndpointMode EDGE_ENDPOINT_MODE_WRITE_WRITE =
      EdgeEndpointMode._(
          2, _omitEnumNames ? '' : 'EDGE_ENDPOINT_MODE_WRITE_WRITE');
  static const EdgeEndpointMode EDGE_ENDPOINT_MODE_READ_WRITE =
      EdgeEndpointMode._(
          3, _omitEnumNames ? '' : 'EDGE_ENDPOINT_MODE_READ_WRITE');
  static const EdgeEndpointMode EDGE_ENDPOINT_MODE_WRITE_READ =
      EdgeEndpointMode._(
          4, _omitEnumNames ? '' : 'EDGE_ENDPOINT_MODE_WRITE_READ');

  static const $core.List<EdgeEndpointMode> values = <EdgeEndpointMode>[
    EDGE_ENDPOINT_MODE_UNSPECIFIED,
    EDGE_ENDPOINT_MODE_READ_READ,
    EDGE_ENDPOINT_MODE_WRITE_WRITE,
    EDGE_ENDPOINT_MODE_READ_WRITE,
    EDGE_ENDPOINT_MODE_WRITE_READ,
  ];

  static final $core.List<EdgeEndpointMode?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static EdgeEndpointMode? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const EdgeEndpointMode._(super.value, super.name);
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

/// Closed business values only. No arbitrary configuration keys or secret values.
class OperationalGroup extends $pb.ProtobufEnum {
  static const OperationalGroup OPERATIONAL_GROUP_UNSPECIFIED =
      OperationalGroup._(
          0, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_UNSPECIFIED');
  static const OperationalGroup OPERATIONAL_GROUP_SLOW_RPC =
      OperationalGroup._(1, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_SLOW_RPC');
  static const OperationalGroup OPERATIONAL_GROUP_RATE =
      OperationalGroup._(2, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_RATE');
  static const OperationalGroup OPERATIONAL_GROUP_REQUEST =
      OperationalGroup._(3, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_REQUEST');
  static const OperationalGroup OPERATIONAL_GROUP_BACKUP =
      OperationalGroup._(4, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_BACKUP');
  static const OperationalGroup OPERATIONAL_GROUP_CAPACITY =
      OperationalGroup._(5, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_CAPACITY');
  static const OperationalGroup OPERATIONAL_GROUP_LIFECYCLE =
      OperationalGroup._(
          6, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_LIFECYCLE');
  static const OperationalGroup OPERATIONAL_GROUP_SEARCH =
      OperationalGroup._(7, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_SEARCH');
  static const OperationalGroup OPERATIONAL_GROUP_REPLICATION =
      OperationalGroup._(
          8, _omitEnumNames ? '' : 'OPERATIONAL_GROUP_REPLICATION');

  static const $core.List<OperationalGroup> values = <OperationalGroup>[
    OPERATIONAL_GROUP_UNSPECIFIED,
    OPERATIONAL_GROUP_SLOW_RPC,
    OPERATIONAL_GROUP_RATE,
    OPERATIONAL_GROUP_REQUEST,
    OPERATIONAL_GROUP_BACKUP,
    OPERATIONAL_GROUP_CAPACITY,
    OPERATIONAL_GROUP_LIFECYCLE,
    OPERATIONAL_GROUP_SEARCH,
    OPERATIONAL_GROUP_REPLICATION,
  ];

  static final $core.List<OperationalGroup?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 8);
  static OperationalGroup? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const OperationalGroup._(super.value, super.name);
}

class OperationalSearchMode extends $pb.ProtobufEnum {
  static const OperationalSearchMode OPERATIONAL_SEARCH_MODE_UNSPECIFIED =
      OperationalSearchMode._(
          0, _omitEnumNames ? '' : 'OPERATIONAL_SEARCH_MODE_UNSPECIFIED');
  static const OperationalSearchMode OPERATIONAL_SEARCH_MODE_ANY =
      OperationalSearchMode._(
          1, _omitEnumNames ? '' : 'OPERATIONAL_SEARCH_MODE_ANY');
  static const OperationalSearchMode OPERATIONAL_SEARCH_MODE_ALL =
      OperationalSearchMode._(
          2, _omitEnumNames ? '' : 'OPERATIONAL_SEARCH_MODE_ALL');
  static const OperationalSearchMode OPERATIONAL_SEARCH_MODE_MIN_SHOULD =
      OperationalSearchMode._(
          3, _omitEnumNames ? '' : 'OPERATIONAL_SEARCH_MODE_MIN_SHOULD');

  static const $core.List<OperationalSearchMode> values =
      <OperationalSearchMode>[
    OPERATIONAL_SEARCH_MODE_UNSPECIFIED,
    OPERATIONAL_SEARCH_MODE_ANY,
    OPERATIONAL_SEARCH_MODE_ALL,
    OPERATIONAL_SEARCH_MODE_MIN_SHOULD,
  ];

  static final $core.List<OperationalSearchMode?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static OperationalSearchMode? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const OperationalSearchMode._(super.value, super.name);
}

/// Field identity is closed and carries provenance only, never arbitrary values.
class OperationalSetting extends $pb.ProtobufEnum {
  static const OperationalSetting OPERATIONAL_SETTING_UNSPECIFIED =
      OperationalSetting._(
          0, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_UNSPECIFIED');
  static const OperationalSetting OPERATIONAL_SETTING_SLOW_RPC_THRESHOLD_MS =
      OperationalSetting._(
          1, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_SLOW_RPC_THRESHOLD_MS');
  static const OperationalSetting OPERATIONAL_SETTING_RATE_RPS =
      OperationalSetting._(
          2, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_RATE_RPS');
  static const OperationalSetting OPERATIONAL_SETTING_RATE_BURST =
      OperationalSetting._(
          3, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_RATE_BURST');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_DELETE_BY_PREFIX_DEFAULT_LIMIT =
      OperationalSetting._(
          4,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_DELETE_BY_PREFIX_DEFAULT_LIMIT');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_DELETE_BY_PREFIX_MAX_LIMIT =
      OperationalSetting._(
          5,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_DELETE_BY_PREFIX_MAX_LIMIT');
  static const OperationalSetting OPERATIONAL_SETTING_REQUEST_ILLUMINATE_MAX_K =
      OperationalSetting._(6,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_REQUEST_ILLUMINATE_MAX_K');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_ILLUMINATE_MAX_STEP = OperationalSetting._(
          7,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_ILLUMINATE_MAX_STEP');
  static const OperationalSetting OPERATIONAL_SETTING_REQUEST_MAX_BATCH_SIZE =
      OperationalSetting._(8,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_REQUEST_MAX_BATCH_SIZE');
  static const OperationalSetting OPERATIONAL_SETTING_REQUEST_MAX_KEY_LEN =
      OperationalSetting._(
          9, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_REQUEST_MAX_KEY_LEN');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_SCAN_DEFAULT_LIMIT = OperationalSetting._(
          10,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_SCAN_DEFAULT_LIMIT');
  static const OperationalSetting OPERATIONAL_SETTING_REQUEST_SCAN_MAX_LIMIT =
      OperationalSetting._(11,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_REQUEST_SCAN_MAX_LIMIT');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_PUSHES = OperationalSetting._(
          12,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_PUSHES');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_RESULTS = OperationalSetting._(
          13,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_RESULTS');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_TOUCHED_EDGES =
      OperationalSetting._(
          14,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_TOUCHED_EDGES');
  static const OperationalSetting
      OPERATIONAL_SETTING_REQUEST_TRAVERSAL_TIMEOUT_MS = OperationalSetting._(
          15,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REQUEST_TRAVERSAL_TIMEOUT_MS');
  static const OperationalSetting OPERATIONAL_SETTING_BACKUP_ENABLED =
      OperationalSetting._(
          16, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_BACKUP_ENABLED');
  static const OperationalSetting OPERATIONAL_SETTING_BACKUP_INTERVAL_NANOS =
      OperationalSetting._(17,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_BACKUP_INTERVAL_NANOS');
  static const OperationalSetting OPERATIONAL_SETTING_BACKUP_RETAIN =
      OperationalSetting._(
          18, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_BACKUP_RETAIN');
  static const OperationalSetting OPERATIONAL_SETTING_CAPACITY_GC_EDGE_BUDGET =
      OperationalSetting._(19,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_CAPACITY_GC_EDGE_BUDGET');
  static const OperationalSetting
      OPERATIONAL_SETTING_CAPACITY_GC_INTERVAL_SECONDS = OperationalSetting._(
          20,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_CAPACITY_GC_INTERVAL_SECONDS');
  static const OperationalSetting OPERATIONAL_SETTING_CAPACITY_MAX_EDGES =
      OperationalSetting._(
          21, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_CAPACITY_MAX_EDGES');
  static const OperationalSetting
      OPERATIONAL_SETTING_CAPACITY_MAX_EDGE_CAUSAL_ENTRIES =
      OperationalSetting._(
          22,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_CAPACITY_MAX_EDGE_CAUSAL_ENTRIES');
  static const OperationalSetting
      OPERATIONAL_SETTING_CAPACITY_MAX_VERTEX_CAUSAL_ENTRIES =
      OperationalSetting._(
          23,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_CAPACITY_MAX_VERTEX_CAUSAL_ENTRIES');
  static const OperationalSetting OPERATIONAL_SETTING_CAPACITY_MAX_VERTICES =
      OperationalSetting._(24,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_CAPACITY_MAX_VERTICES');
  static const OperationalSetting
      OPERATIONAL_SETTING_LIFECYCLE_DRAIN_DELAY_SECONDS = OperationalSetting._(
          25,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_LIFECYCLE_DRAIN_DELAY_SECONDS');
  static const OperationalSetting
      OPERATIONAL_SETTING_LIFECYCLE_MAX_REPLICATION_LAG = OperationalSetting._(
          26,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_LIFECYCLE_MAX_REPLICATION_LAG');
  static const OperationalSetting
      OPERATIONAL_SETTING_LIFECYCLE_SHUTDOWN_TIMEOUT_SECONDS =
      OperationalSetting._(
          27,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_LIFECYCLE_SHUTDOWN_TIMEOUT_SECONDS');
  static const OperationalSetting OPERATIONAL_SETTING_SEARCH_DEFAULT_LIMIT =
      OperationalSetting._(
          28, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_SEARCH_DEFAULT_LIMIT');
  static const OperationalSetting OPERATIONAL_SETTING_SEARCH_MAX_LIMIT =
      OperationalSetting._(
          29, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_SEARCH_MAX_LIMIT');
  static const OperationalSetting OPERATIONAL_SETTING_SEARCH_DEFAULT_MODE =
      OperationalSetting._(
          30, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_SEARCH_DEFAULT_MODE');
  static const OperationalSetting
      OPERATIONAL_SETTING_SEARCH_DEFAULT_MIN_SHOULD = OperationalSetting._(
          31,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_SEARCH_DEFAULT_MIN_SHOULD');
  static const OperationalSetting OPERATIONAL_SETTING_SEARCH_TIMEOUT_MS =
      OperationalSetting._(
          32, _omitEnumNames ? '' : 'OPERATIONAL_SETTING_SEARCH_TIMEOUT_MS');
  static const OperationalSetting OPERATIONAL_SETTING_SEARCH_MAX_QUERY_BYTES =
      OperationalSetting._(33,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_SEARCH_MAX_QUERY_BYTES');
  static const OperationalSetting OPERATIONAL_SETTING_SEARCH_MAX_QUERY_TERMS =
      OperationalSetting._(34,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTING_SEARCH_MAX_QUERY_TERMS');
  static const OperationalSetting
      OPERATIONAL_SETTING_SEARCH_MAX_DICTIONARY_VISITS = OperationalSetting._(
          35,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_SEARCH_MAX_DICTIONARY_VISITS');
  static const OperationalSetting
      OPERATIONAL_SETTING_SEARCH_MAX_POSTING_VISITS = OperationalSetting._(
          36,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_SEARCH_MAX_POSTING_VISITS');
  static const OperationalSetting
      OPERATIONAL_SETTING_SEARCH_MAX_POSITION_VISITS = OperationalSetting._(
          37,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_SEARCH_MAX_POSITION_VISITS');
  static const OperationalSetting
      OPERATIONAL_SETTING_SEARCH_MAX_EXPIRATION_VISITS = OperationalSetting._(
          38,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_SEARCH_MAX_EXPIRATION_VISITS');
  static const OperationalSetting
      OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_GAP_WARN_THRESHOLD =
      OperationalSetting._(
          39,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_GAP_WARN_THRESHOLD');
  static const OperationalSetting
      OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_INTERVAL_MS =
      OperationalSetting._(
          40,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_INTERVAL_MS');
  static const OperationalSetting
      OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS =
      OperationalSetting._(
          41,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS');
  static const OperationalSetting
      OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MAX_MS =
      OperationalSetting._(
          42,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MAX_MS');
  static const OperationalSetting
      OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MIN_MS =
      OperationalSetting._(
          43,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MIN_MS');

  static const $core.List<OperationalSetting> values = <OperationalSetting>[
    OPERATIONAL_SETTING_UNSPECIFIED,
    OPERATIONAL_SETTING_SLOW_RPC_THRESHOLD_MS,
    OPERATIONAL_SETTING_RATE_RPS,
    OPERATIONAL_SETTING_RATE_BURST,
    OPERATIONAL_SETTING_REQUEST_DELETE_BY_PREFIX_DEFAULT_LIMIT,
    OPERATIONAL_SETTING_REQUEST_DELETE_BY_PREFIX_MAX_LIMIT,
    OPERATIONAL_SETTING_REQUEST_ILLUMINATE_MAX_K,
    OPERATIONAL_SETTING_REQUEST_ILLUMINATE_MAX_STEP,
    OPERATIONAL_SETTING_REQUEST_MAX_BATCH_SIZE,
    OPERATIONAL_SETTING_REQUEST_MAX_KEY_LEN,
    OPERATIONAL_SETTING_REQUEST_SCAN_DEFAULT_LIMIT,
    OPERATIONAL_SETTING_REQUEST_SCAN_MAX_LIMIT,
    OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_PUSHES,
    OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_RESULTS,
    OPERATIONAL_SETTING_REQUEST_TRAVERSAL_MAX_TOUCHED_EDGES,
    OPERATIONAL_SETTING_REQUEST_TRAVERSAL_TIMEOUT_MS,
    OPERATIONAL_SETTING_BACKUP_ENABLED,
    OPERATIONAL_SETTING_BACKUP_INTERVAL_NANOS,
    OPERATIONAL_SETTING_BACKUP_RETAIN,
    OPERATIONAL_SETTING_CAPACITY_GC_EDGE_BUDGET,
    OPERATIONAL_SETTING_CAPACITY_GC_INTERVAL_SECONDS,
    OPERATIONAL_SETTING_CAPACITY_MAX_EDGES,
    OPERATIONAL_SETTING_CAPACITY_MAX_EDGE_CAUSAL_ENTRIES,
    OPERATIONAL_SETTING_CAPACITY_MAX_VERTEX_CAUSAL_ENTRIES,
    OPERATIONAL_SETTING_CAPACITY_MAX_VERTICES,
    OPERATIONAL_SETTING_LIFECYCLE_DRAIN_DELAY_SECONDS,
    OPERATIONAL_SETTING_LIFECYCLE_MAX_REPLICATION_LAG,
    OPERATIONAL_SETTING_LIFECYCLE_SHUTDOWN_TIMEOUT_SECONDS,
    OPERATIONAL_SETTING_SEARCH_DEFAULT_LIMIT,
    OPERATIONAL_SETTING_SEARCH_MAX_LIMIT,
    OPERATIONAL_SETTING_SEARCH_DEFAULT_MODE,
    OPERATIONAL_SETTING_SEARCH_DEFAULT_MIN_SHOULD,
    OPERATIONAL_SETTING_SEARCH_TIMEOUT_MS,
    OPERATIONAL_SETTING_SEARCH_MAX_QUERY_BYTES,
    OPERATIONAL_SETTING_SEARCH_MAX_QUERY_TERMS,
    OPERATIONAL_SETTING_SEARCH_MAX_DICTIONARY_VISITS,
    OPERATIONAL_SETTING_SEARCH_MAX_POSTING_VISITS,
    OPERATIONAL_SETTING_SEARCH_MAX_POSITION_VISITS,
    OPERATIONAL_SETTING_SEARCH_MAX_EXPIRATION_VISITS,
    OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_GAP_WARN_THRESHOLD,
    OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_INTERVAL_MS,
    OPERATIONAL_SETTING_REPLICATION_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS,
    OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MAX_MS,
    OPERATIONAL_SETTING_REPLICATION_PUMP_BACKOFF_MIN_MS,
  ];

  static final $core.List<OperationalSetting?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 43);
  static OperationalSetting? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const OperationalSetting._(super.value, super.name);
}

class OperationalSettingsSource extends $pb.ProtobufEnum {
  static const OperationalSettingsSource
      OPERATIONAL_SETTINGS_SOURCE_UNSPECIFIED = OperationalSettingsSource._(
          0, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_SOURCE_UNSPECIFIED');
  static const OperationalSettingsSource OPERATIONAL_SETTINGS_SOURCE_DEFAULT =
      OperationalSettingsSource._(
          1, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_SOURCE_DEFAULT');
  static const OperationalSettingsSource
      OPERATIONAL_SETTINGS_SOURCE_ENVIRONMENT = OperationalSettingsSource._(
          2, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_SOURCE_ENVIRONMENT');
  static const OperationalSettingsSource OPERATIONAL_SETTINGS_SOURCE_DERIVED =
      OperationalSettingsSource._(
          3, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_SOURCE_DERIVED');
  static const OperationalSettingsSource OPERATIONAL_SETTINGS_SOURCE_MANAGED =
      OperationalSettingsSource._(
          4, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_SOURCE_MANAGED');

  static const $core.List<OperationalSettingsSource> values =
      <OperationalSettingsSource>[
    OPERATIONAL_SETTINGS_SOURCE_UNSPECIFIED,
    OPERATIONAL_SETTINGS_SOURCE_DEFAULT,
    OPERATIONAL_SETTINGS_SOURCE_ENVIRONMENT,
    OPERATIONAL_SETTINGS_SOURCE_DERIVED,
    OPERATIONAL_SETTINGS_SOURCE_MANAGED,
  ];

  static final $core.List<OperationalSettingsSource?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static OperationalSettingsSource? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const OperationalSettingsSource._(super.value, super.name);
}

class OperationalSettingsChangeMode extends $pb.ProtobufEnum {
  static const OperationalSettingsChangeMode
      OPERATIONAL_SETTINGS_CHANGE_MODE_UNSPECIFIED =
      OperationalSettingsChangeMode._(0,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_CHANGE_MODE_UNSPECIFIED');
  static const OperationalSettingsChangeMode
      OPERATIONAL_SETTINGS_CHANGE_MODE_UNAVAILABLE =
      OperationalSettingsChangeMode._(1,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_CHANGE_MODE_UNAVAILABLE');
  static const OperationalSettingsChangeMode
      OPERATIONAL_SETTINGS_CHANGE_MODE_HOT = OperationalSettingsChangeMode._(
          2, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_CHANGE_MODE_HOT');
  static const OperationalSettingsChangeMode
      OPERATIONAL_SETTINGS_CHANGE_MODE_WORKER_RESTART =
      OperationalSettingsChangeMode._(
          3,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_CHANGE_MODE_WORKER_RESTART');
  static const OperationalSettingsChangeMode
      OPERATIONAL_SETTINGS_CHANGE_MODE_PROCESS_RESTART =
      OperationalSettingsChangeMode._(
          4,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_CHANGE_MODE_PROCESS_RESTART');
  static const OperationalSettingsChangeMode
      OPERATIONAL_SETTINGS_CHANGE_MODE_COORDINATED_RESTART =
      OperationalSettingsChangeMode._(
          5,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_CHANGE_MODE_COORDINATED_RESTART');

  static const $core.List<OperationalSettingsChangeMode> values =
      <OperationalSettingsChangeMode>[
    OPERATIONAL_SETTINGS_CHANGE_MODE_UNSPECIFIED,
    OPERATIONAL_SETTINGS_CHANGE_MODE_UNAVAILABLE,
    OPERATIONAL_SETTINGS_CHANGE_MODE_HOT,
    OPERATIONAL_SETTINGS_CHANGE_MODE_WORKER_RESTART,
    OPERATIONAL_SETTINGS_CHANGE_MODE_PROCESS_RESTART,
    OPERATIONAL_SETTINGS_CHANGE_MODE_COORDINATED_RESTART,
  ];

  static final $core.List<OperationalSettingsChangeMode?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 5);
  static OperationalSettingsChangeMode? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const OperationalSettingsChangeMode._(super.value, super.name);
}

class OperationalSettingsApplicationState extends $pb.ProtobufEnum {
  static const OperationalSettingsApplicationState
      OPERATIONAL_SETTINGS_APPLICATION_STATE_UNSPECIFIED =
      OperationalSettingsApplicationState._(
          0,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_APPLICATION_STATE_UNSPECIFIED');
  static const OperationalSettingsApplicationState
      OPERATIONAL_SETTINGS_APPLICATION_STATE_UNAVAILABLE =
      OperationalSettingsApplicationState._(
          1,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_APPLICATION_STATE_UNAVAILABLE');
  static const OperationalSettingsApplicationState
      OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED =
      OperationalSettingsApplicationState._(
          2,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED');
  static const OperationalSettingsApplicationState
      OPERATIONAL_SETTINGS_APPLICATION_STATE_PENDING =
      OperationalSettingsApplicationState._(
          3,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_APPLICATION_STATE_PENDING');
  static const OperationalSettingsApplicationState
      OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR =
      OperationalSettingsApplicationState._(4,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR');

  static const $core.List<OperationalSettingsApplicationState> values =
      <OperationalSettingsApplicationState>[
    OPERATIONAL_SETTINGS_APPLICATION_STATE_UNSPECIFIED,
    OPERATIONAL_SETTINGS_APPLICATION_STATE_UNAVAILABLE,
    OPERATIONAL_SETTINGS_APPLICATION_STATE_APPLIED,
    OPERATIONAL_SETTINGS_APPLICATION_STATE_PENDING,
    OPERATIONAL_SETTINGS_APPLICATION_STATE_ERROR,
  ];

  static final $core.List<OperationalSettingsApplicationState?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static OperationalSettingsApplicationState? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const OperationalSettingsApplicationState._(super.value, super.name);
}

class OperationalSettingsError extends $pb.ProtobufEnum {
  static const OperationalSettingsError OPERATIONAL_SETTINGS_ERROR_UNSPECIFIED =
      OperationalSettingsError._(
          0, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_ERROR_UNSPECIFIED');
  static const OperationalSettingsError OPERATIONAL_SETTINGS_ERROR_NONE =
      OperationalSettingsError._(
          1, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_ERROR_NONE');
  static const OperationalSettingsError
      OPERATIONAL_SETTINGS_ERROR_INVALID_POLICY = OperationalSettingsError._(
          2, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_ERROR_INVALID_POLICY');
  static const OperationalSettingsError
      OPERATIONAL_SETTINGS_ERROR_OWNER_UNAVAILABLE = OperationalSettingsError._(
          3,
          _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_ERROR_OWNER_UNAVAILABLE');
  static const OperationalSettingsError
      OPERATIONAL_SETTINGS_ERROR_APPLY_FAILED = OperationalSettingsError._(
          4, _omitEnumNames ? '' : 'OPERATIONAL_SETTINGS_ERROR_APPLY_FAILED');
  static const OperationalSettingsError
      OPERATIONAL_SETTINGS_ERROR_DEPENDENCY_UNAVAILABLE =
      OperationalSettingsError._(
          5,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_ERROR_DEPENDENCY_UNAVAILABLE');
  static const OperationalSettingsError
      OPERATIONAL_SETTINGS_ERROR_COMPATIBILITY_MISMATCH =
      OperationalSettingsError._(
          6,
          _omitEnumNames
              ? ''
              : 'OPERATIONAL_SETTINGS_ERROR_COMPATIBILITY_MISMATCH');

  static const $core.List<OperationalSettingsError> values =
      <OperationalSettingsError>[
    OPERATIONAL_SETTINGS_ERROR_UNSPECIFIED,
    OPERATIONAL_SETTINGS_ERROR_NONE,
    OPERATIONAL_SETTINGS_ERROR_INVALID_POLICY,
    OPERATIONAL_SETTINGS_ERROR_OWNER_UNAVAILABLE,
    OPERATIONAL_SETTINGS_ERROR_APPLY_FAILED,
    OPERATIONAL_SETTINGS_ERROR_DEPENDENCY_UNAVAILABLE,
    OPERATIONAL_SETTINGS_ERROR_COMPATIBILITY_MISMATCH,
  ];

  static final $core.List<OperationalSettingsError?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 6);
  static OperationalSettingsError? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const OperationalSettingsError._(super.value, super.name);
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
