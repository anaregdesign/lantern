// This is a generated file - do not edit.
//
// Generated from graph/v1/changes.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

class ChangeProjection extends $pb.ProtobufEnum {
  static const ChangeProjection CHANGE_PROJECTION_UNSPECIFIED =
      ChangeProjection._(
          0, _omitEnumNames ? '' : 'CHANGE_PROJECTION_UNSPECIFIED');
  static const ChangeProjection CHANGE_PROJECTION_IDENTITY =
      ChangeProjection._(1, _omitEnumNames ? '' : 'CHANGE_PROJECTION_IDENTITY');
  static const ChangeProjection CHANGE_PROJECTION_VALUE =
      ChangeProjection._(2, _omitEnumNames ? '' : 'CHANGE_PROJECTION_VALUE');

  static const $core.List<ChangeProjection> values = <ChangeProjection>[
    CHANGE_PROJECTION_UNSPECIFIED,
    CHANGE_PROJECTION_IDENTITY,
    CHANGE_PROJECTION_VALUE,
  ];

  static final $core.List<ChangeProjection?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static ChangeProjection? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const ChangeProjection._(super.value, super.name);
}

const $core.bool _omitEnumNames =
    $core.bool.fromEnvironment('protobuf.omit_enum_names');
