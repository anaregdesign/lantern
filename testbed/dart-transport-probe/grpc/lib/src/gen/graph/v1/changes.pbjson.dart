// This is a generated file - do not edit.
//
// Generated from graph/v1/changes.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports
// ignore_for_file: unused_import

import 'dart:convert' as $convert;
import 'dart:core' as $core;
import 'dart:typed_data' as $typed_data;

@$core.Deprecated('Use changeProjectionDescriptor instead')
const ChangeProjection$json = {
  '1': 'ChangeProjection',
  '2': [
    {'1': 'CHANGE_PROJECTION_UNSPECIFIED', '2': 0},
    {'1': 'CHANGE_PROJECTION_IDENTITY', '2': 1},
    {'1': 'CHANGE_PROJECTION_VALUE', '2': 2},
  ],
};

/// Descriptor for `ChangeProjection`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List changeProjectionDescriptor = $convert.base64Decode(
    'ChBDaGFuZ2VQcm9qZWN0aW9uEiEKHUNIQU5HRV9QUk9KRUNUSU9OX1VOU1BFQ0lGSUVEEAASHg'
    'oaQ0hBTkdFX1BST0pFQ1RJT05fSURFTlRJVFkQARIbChdDSEFOR0VfUFJPSkVDVElPTl9WQUxV'
    'RRAC');

@$core.Deprecated('Use watchChangesRequestDescriptor instead')
const WatchChangesRequest$json = {
  '1': 'WatchChangesRequest',
  '2': [
    {'1': 'prefix', '3': 1, '4': 1, '5': 9, '10': 'prefix'},
    {
      '1': 'projection',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.ChangeProjection',
      '10': 'projection'
    },
    {'1': 'bootstrap', '3': 3, '4': 1, '5': 8, '10': 'bootstrap'},
    {'1': 'cursor', '3': 4, '4': 1, '5': 12, '10': 'cursor'},
  ],
};

/// Descriptor for `WatchChangesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List watchChangesRequestDescriptor = $convert.base64Decode(
    'ChNXYXRjaENoYW5nZXNSZXF1ZXN0EhYKBnByZWZpeBgBIAEoCVIGcHJlZml4EjoKCnByb2plY3'
    'Rpb24YAiABKA4yGi5ncmFwaC52MS5DaGFuZ2VQcm9qZWN0aW9uUgpwcm9qZWN0aW9uEhwKCWJv'
    'b3RzdHJhcBgDIAEoCFIJYm9vdHN0cmFwEhYKBmN1cnNvchgEIAEoDFIGY3Vyc29y');

@$core.Deprecated('Use changeInvalidationDescriptor instead')
const ChangeInvalidation$json = {
  '1': 'ChangeInvalidation',
  '2': [
    {'1': 'vertex_key', '3': 1, '4': 1, '5': 9, '9': 0, '10': 'vertexKey'},
    {
      '1': 'edge_key',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.EdgeKey',
      '9': 0,
      '10': 'edgeKey'
    },
    {
      '1': 'vertex',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '9': 1,
      '10': 'vertex'
    },
    {
      '1': 'edge',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Edge',
      '9': 1,
      '10': 'edge'
    },
  ],
  '8': [
    {'1': 'identity'},
    {'1': 'current_image'},
  ],
};

/// Descriptor for `ChangeInvalidation`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List changeInvalidationDescriptor = $convert.base64Decode(
    'ChJDaGFuZ2VJbnZhbGlkYXRpb24SHwoKdmVydGV4X2tleRgBIAEoCUgAUgl2ZXJ0ZXhLZXkSLg'
    'oIZWRnZV9rZXkYAiABKAsyES5ncmFwaC52MS5FZGdlS2V5SABSB2VkZ2VLZXkSKgoGdmVydGV4'
    'GAMgASgLMhAuZ3JhcGgudjEuVmVydGV4SAFSBnZlcnRleBIkCgRlZGdlGAQgASgLMg4uZ3JhcG'
    'gudjEuRWRnZUgBUgRlZGdlQgoKCGlkZW50aXR5Qg8KDWN1cnJlbnRfaW1hZ2U=');

@$core.Deprecated('Use watchChangesResponseDescriptor instead')
const WatchChangesResponse$json = {
  '1': 'WatchChangesResponse',
  '2': [
    {
      '1': 'invalidations',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ChangeInvalidation',
      '10': 'invalidations'
    },
    {'1': 'cursor', '3': 2, '4': 1, '5': 12, '10': 'cursor'},
    {'1': 'bootstrap', '3': 3, '4': 1, '5': 8, '10': 'bootstrap'},
  ],
};

/// Descriptor for `WatchChangesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List watchChangesResponseDescriptor = $convert.base64Decode(
    'ChRXYXRjaENoYW5nZXNSZXNwb25zZRJCCg1pbnZhbGlkYXRpb25zGAEgAygLMhwuZ3JhcGgudj'
    'EuQ2hhbmdlSW52YWxpZGF0aW9uUg1pbnZhbGlkYXRpb25zEhYKBmN1cnNvchgCIAEoDFIGY3Vy'
    'c29yEhwKCWJvb3RzdHJhcBgDIAEoCFIJYm9vdHN0cmFw');
