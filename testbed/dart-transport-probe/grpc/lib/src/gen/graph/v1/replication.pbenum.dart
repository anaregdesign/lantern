// This is a generated file - do not edit.
//
// Generated from graph/v1/replication.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

class SubscribeProjection extends $pb.ProtobufEnum {
  /// Omitted projection preserves the historical full-Mutation stream.
  static const SubscribeProjection SUBSCRIBE_PROJECTION_UNSPECIFIED =
      SubscribeProjection._(
          0, _omitEnumNames ? '' : 'SUBSCRIBE_PROJECTION_UNSPECIFIED');
  static const SubscribeProjection SUBSCRIBE_PROJECTION_FULL_MUTATION =
      SubscribeProjection._(
          1, _omitEnumNames ? '' : 'SUBSCRIBE_PROJECTION_FULL_MUTATION');
  static const SubscribeProjection SUBSCRIBE_PROJECTION_IDENTITY_ONLY =
      SubscribeProjection._(
          2, _omitEnumNames ? '' : 'SUBSCRIBE_PROJECTION_IDENTITY_ONLY');

  static const $core.List<SubscribeProjection> values = <SubscribeProjection>[
    SUBSCRIBE_PROJECTION_UNSPECIFIED,
    SUBSCRIBE_PROJECTION_FULL_MUTATION,
    SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
  ];

  static final $core.List<SubscribeProjection?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static SubscribeProjection? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SubscribeProjection._(super.value, super.name);
}

class IdentityOperation extends $pb.ProtobufEnum {
  static const IdentityOperation IDENTITY_OPERATION_UNSPECIFIED =
      IdentityOperation._(
          0, _omitEnumNames ? '' : 'IDENTITY_OPERATION_UNSPECIFIED');
  static const IdentityOperation IDENTITY_OPERATION_PUT_VERTEX =
      IdentityOperation._(
          1, _omitEnumNames ? '' : 'IDENTITY_OPERATION_PUT_VERTEX');
  static const IdentityOperation IDENTITY_OPERATION_DELETE_VERTEX =
      IdentityOperation._(
          2, _omitEnumNames ? '' : 'IDENTITY_OPERATION_DELETE_VERTEX');
  static const IdentityOperation IDENTITY_OPERATION_ADD_EDGE =
      IdentityOperation._(
          3, _omitEnumNames ? '' : 'IDENTITY_OPERATION_ADD_EDGE');
  static const IdentityOperation IDENTITY_OPERATION_PUT_EDGE =
      IdentityOperation._(
          4, _omitEnumNames ? '' : 'IDENTITY_OPERATION_PUT_EDGE');
  static const IdentityOperation IDENTITY_OPERATION_DELETE_EDGE =
      IdentityOperation._(
          5, _omitEnumNames ? '' : 'IDENTITY_OPERATION_DELETE_EDGE');

  static const $core.List<IdentityOperation> values = <IdentityOperation>[
    IDENTITY_OPERATION_UNSPECIFIED,
    IDENTITY_OPERATION_PUT_VERTEX,
    IDENTITY_OPERATION_DELETE_VERTEX,
    IDENTITY_OPERATION_ADD_EDGE,
    IDENTITY_OPERATION_PUT_EDGE,
    IDENTITY_OPERATION_DELETE_EDGE,
  ];

  static final $core.List<IdentityOperation?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 5);
  static IdentityOperation? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const IdentityOperation._(super.value, super.name);
}

const $core.bool _omitEnumNames =
    $core.bool.fromEnvironment('protobuf.omit_enum_names');
