// This is a generated file - do not edit.
//
// Generated from graph/v1/replication.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, unused_import

import 'dart:convert' as $convert;
import 'dart:core' as $core;
import 'dart:typed_data' as $typed_data;

import '../../google/protobuf/duration.pbjson.dart' as $2;
import '../../google/protobuf/timestamp.pbjson.dart' as $1;
import 'graph.pbjson.dart' as $0;

@$core.Deprecated('Use subscribeProjectionDescriptor instead')
const SubscribeProjection$json = {
  '1': 'SubscribeProjection',
  '2': [
    {'1': 'SUBSCRIBE_PROJECTION_UNSPECIFIED', '2': 0},
    {'1': 'SUBSCRIBE_PROJECTION_FULL_MUTATION', '2': 1},
    {'1': 'SUBSCRIBE_PROJECTION_IDENTITY_ONLY', '2': 2},
  ],
};

/// Descriptor for `SubscribeProjection`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List subscribeProjectionDescriptor = $convert.base64Decode(
    'ChNTdWJzY3JpYmVQcm9qZWN0aW9uEiQKIFNVQlNDUklCRV9QUk9KRUNUSU9OX1VOU1BFQ0lGSU'
    'VEEAASJgoiU1VCU0NSSUJFX1BST0pFQ1RJT05fRlVMTF9NVVRBVElPThABEiYKIlNVQlNDUklC'
    'RV9QUk9KRUNUSU9OX0lERU5USVRZX09OTFkQAg==');

@$core.Deprecated('Use identityOperationDescriptor instead')
const IdentityOperation$json = {
  '1': 'IdentityOperation',
  '2': [
    {'1': 'IDENTITY_OPERATION_UNSPECIFIED', '2': 0},
    {'1': 'IDENTITY_OPERATION_PUT_VERTEX', '2': 1},
    {'1': 'IDENTITY_OPERATION_DELETE_VERTEX', '2': 2},
    {'1': 'IDENTITY_OPERATION_ADD_EDGE', '2': 3},
    {'1': 'IDENTITY_OPERATION_PUT_EDGE', '2': 4},
    {'1': 'IDENTITY_OPERATION_DELETE_EDGE', '2': 5},
    {'1': 'IDENTITY_OPERATION_RECEIPT_ONLY', '2': 6},
  ],
};

/// Descriptor for `IdentityOperation`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List identityOperationDescriptor = $convert.base64Decode(
    'ChFJZGVudGl0eU9wZXJhdGlvbhIiCh5JREVOVElUWV9PUEVSQVRJT05fVU5TUEVDSUZJRUQQAB'
    'IhCh1JREVOVElUWV9PUEVSQVRJT05fUFVUX1ZFUlRFWBABEiQKIElERU5USVRZX09QRVJBVElP'
    'Tl9ERUxFVEVfVkVSVEVYEAISHwobSURFTlRJVFlfT1BFUkFUSU9OX0FERF9FREdFEAMSHwobSU'
    'RFTlRJVFlfT1BFUkFUSU9OX1BVVF9FREdFEAQSIgoeSURFTlRJVFlfT1BFUkFUSU9OX0RFTEVU'
    'RV9FREdFEAUSIwofSURFTlRJVFlfT1BFUkFUSU9OX1JFQ0VJUFRfT05MWRAG');

@$core.Deprecated('Use snapshotFormatDescriptor instead')
const SnapshotFormat$json = {
  '1': 'SnapshotFormat',
  '2': [
    {'1': 'SNAPSHOT_FORMAT_UNSPECIFIED', '2': 0},
    {'1': 'SNAPSHOT_FORMAT_GRAPH_ONLY_V1', '2': 1},
    {'1': 'SNAPSHOT_FORMAT_RECEIPT_V2', '2': 2},
  ],
};

/// Descriptor for `SnapshotFormat`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List snapshotFormatDescriptor = $convert.base64Decode(
    'Cg5TbmFwc2hvdEZvcm1hdBIfChtTTkFQU0hPVF9GT1JNQVRfVU5TUEVDSUZJRUQQABIhCh1TTk'
    'FQU0hPVF9GT1JNQVRfR1JBUEhfT05MWV9WMRABEh4KGlNOQVBTSE9UX0ZPUk1BVF9SRUNFSVBU'
    'X1YyEAI=');

@$core.Deprecated('Use snapshotReceiptKindDescriptor instead')
const SnapshotReceiptKind$json = {
  '1': 'SnapshotReceiptKind',
  '2': [
    {'1': 'SNAPSHOT_RECEIPT_KIND_UNSPECIFIED', '2': 0},
    {'1': 'SNAPSHOT_RECEIPT_KIND_PUT_VERTEX', '2': 1},
    {'1': 'SNAPSHOT_RECEIPT_KIND_PUT_EDGE', '2': 2},
    {'1': 'SNAPSHOT_RECEIPT_KIND_ADD_EDGE', '2': 3},
    {'1': 'SNAPSHOT_RECEIPT_KIND_DELETE_VERTEX', '2': 4},
    {'1': 'SNAPSHOT_RECEIPT_KIND_DELETE_EDGE', '2': 5},
  ],
};

/// Descriptor for `SnapshotReceiptKind`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List snapshotReceiptKindDescriptor = $convert.base64Decode(
    'ChNTbmFwc2hvdFJlY2VpcHRLaW5kEiUKIVNOQVBTSE9UX1JFQ0VJUFRfS0lORF9VTlNQRUNJRk'
    'lFRBAAEiQKIFNOQVBTSE9UX1JFQ0VJUFRfS0lORF9QVVRfVkVSVEVYEAESIgoeU05BUFNIT1Rf'
    'UkVDRUlQVF9LSU5EX1BVVF9FREdFEAISIgoeU05BUFNIT1RfUkVDRUlQVF9LSU5EX0FERF9FRE'
    'dFEAMSJwojU05BUFNIT1RfUkVDRUlQVF9LSU5EX0RFTEVURV9WRVJURVgQBBIlCiFTTkFQU0hP'
    'VF9SRUNFSVBUX0tJTkRfREVMRVRFX0VER0UQBQ==');

@$core.Deprecated('Use hLCTimestampDescriptor instead')
const HLCTimestamp$json = {
  '1': 'HLCTimestamp',
  '2': [
    {'1': 'wall_ns', '3': 1, '4': 1, '5': 3, '10': 'wallNs'},
    {'1': 'logical', '3': 2, '4': 1, '5': 13, '10': 'logical'},
    {'1': 'node_id', '3': 3, '4': 1, '5': 12, '10': 'nodeId'},
  ],
};

/// Descriptor for `HLCTimestamp`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List hLCTimestampDescriptor = $convert.base64Decode(
    'CgxITENUaW1lc3RhbXASFwoHd2FsbF9ucxgBIAEoA1IGd2FsbE5zEhgKB2xvZ2ljYWwYAiABKA'
    '1SB2xvZ2ljYWwSFwoHbm9kZV9pZBgDIAEoDFIGbm9kZUlk');

@$core.Deprecated('Use mutationOpDescriptor instead')
const MutationOp$json = {
  '1': 'MutationOp',
  '2': [
    {
      '1': 'put_vertex',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.PutVertexRequest',
      '9': 0,
      '10': 'putVertex'
    },
    {
      '1': 'put_vertices',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.PutVerticesRequest',
      '9': 0,
      '10': 'putVertices'
    },
    {
      '1': 'delete_vertex',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.DeleteVertexRequest',
      '9': 0,
      '10': 'deleteVertex'
    },
    {
      '1': 'delete_vertices',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.DeleteVerticesRequest',
      '9': 0,
      '10': 'deleteVertices'
    },
    {
      '1': 'delete_vertices_by_prefix',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.DeleteVerticesByPrefixRequest',
      '9': 0,
      '10': 'deleteVerticesByPrefix'
    },
    {
      '1': 'add_edge',
      '3': 6,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.AddEdgeRequest',
      '9': 0,
      '10': 'addEdge'
    },
    {
      '1': 'add_edges',
      '3': 7,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.AddEdgesRequest',
      '9': 0,
      '10': 'addEdges'
    },
    {
      '1': 'put_edge',
      '3': 8,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.PutEdgeRequest',
      '9': 0,
      '10': 'putEdge'
    },
    {
      '1': 'put_edges',
      '3': 9,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.PutEdgesRequest',
      '9': 0,
      '10': 'putEdges'
    },
    {
      '1': 'delete_edge',
      '3': 10,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.DeleteEdgeRequest',
      '9': 0,
      '10': 'deleteEdge'
    },
    {
      '1': 'delete_edges',
      '3': 11,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.DeleteEdgesRequest',
      '9': 0,
      '10': 'deleteEdges'
    },
    {
      '1': 'delete_edges_by_prefix',
      '3': 12,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.DeleteEdgesByPrefixRequest',
      '9': 0,
      '10': 'deleteEdgesByPrefix'
    },
    {
      '1': 'replicated_put_vertices',
      '3': 13,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.ReplicatedPutVertices',
      '9': 0,
      '10': 'replicatedPutVertices'
    },
    {
      '1': 'replicated_put_edges',
      '3': 14,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.ReplicatedPutEdges',
      '9': 0,
      '10': 'replicatedPutEdges'
    },
    {
      '1': 'replicated_receipt_edge_delete',
      '3': 15,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.ReplicatedReceiptEdgeDelete',
      '9': 0,
      '10': 'replicatedReceiptEdgeDelete'
    },
    {
      '1': 'replicated_receipt_vertex_put',
      '3': 16,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.ReplicatedReceiptVertexPut',
      '9': 0,
      '10': 'replicatedReceiptVertexPut'
    },
    {
      '1': 'replicated_receipt_vertex_delete',
      '3': 17,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.ReplicatedReceiptVertexDelete',
      '9': 0,
      '10': 'replicatedReceiptVertexDelete'
    },
  ],
  '8': [
    {'1': 'op'},
  ],
};

/// Descriptor for `MutationOp`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List mutationOpDescriptor = $convert.base64Decode(
    'CgpNdXRhdGlvbk9wEjsKCnB1dF92ZXJ0ZXgYASABKAsyGi5ncmFwaC52MS5QdXRWZXJ0ZXhSZX'
    'F1ZXN0SABSCXB1dFZlcnRleBJBCgxwdXRfdmVydGljZXMYAiABKAsyHC5ncmFwaC52MS5QdXRW'
    'ZXJ0aWNlc1JlcXVlc3RIAFILcHV0VmVydGljZXMSRAoNZGVsZXRlX3ZlcnRleBgDIAEoCzIdLm'
    'dyYXBoLnYxLkRlbGV0ZVZlcnRleFJlcXVlc3RIAFIMZGVsZXRlVmVydGV4EkoKD2RlbGV0ZV92'
    'ZXJ0aWNlcxgEIAEoCzIfLmdyYXBoLnYxLkRlbGV0ZVZlcnRpY2VzUmVxdWVzdEgAUg5kZWxldG'
    'VWZXJ0aWNlcxJkChlkZWxldGVfdmVydGljZXNfYnlfcHJlZml4GAUgASgLMicuZ3JhcGgudjEu'
    'RGVsZXRlVmVydGljZXNCeVByZWZpeFJlcXVlc3RIAFIWZGVsZXRlVmVydGljZXNCeVByZWZpeB'
    'I1CghhZGRfZWRnZRgGIAEoCzIYLmdyYXBoLnYxLkFkZEVkZ2VSZXF1ZXN0SABSB2FkZEVkZ2US'
    'OAoJYWRkX2VkZ2VzGAcgASgLMhkuZ3JhcGgudjEuQWRkRWRnZXNSZXF1ZXN0SABSCGFkZEVkZ2'
    'VzEjUKCHB1dF9lZGdlGAggASgLMhguZ3JhcGgudjEuUHV0RWRnZVJlcXVlc3RIAFIHcHV0RWRn'
    'ZRI4CglwdXRfZWRnZXMYCSABKAsyGS5ncmFwaC52MS5QdXRFZGdlc1JlcXVlc3RIAFIIcHV0RW'
    'RnZXMSPgoLZGVsZXRlX2VkZ2UYCiABKAsyGy5ncmFwaC52MS5EZWxldGVFZGdlUmVxdWVzdEgA'
    'UgpkZWxldGVFZGdlEkEKDGRlbGV0ZV9lZGdlcxgLIAEoCzIcLmdyYXBoLnYxLkRlbGV0ZUVkZ2'
    'VzUmVxdWVzdEgAUgtkZWxldGVFZGdlcxJbChZkZWxldGVfZWRnZXNfYnlfcHJlZml4GAwgASgL'
    'MiQuZ3JhcGgudjEuRGVsZXRlRWRnZXNCeVByZWZpeFJlcXVlc3RIAFITZGVsZXRlRWRnZXNCeV'
    'ByZWZpeBJZChdyZXBsaWNhdGVkX3B1dF92ZXJ0aWNlcxgNIAEoCzIfLmdyYXBoLnYxLlJlcGxp'
    'Y2F0ZWRQdXRWZXJ0aWNlc0gAUhVyZXBsaWNhdGVkUHV0VmVydGljZXMSUAoUcmVwbGljYXRlZF'
    '9wdXRfZWRnZXMYDiABKAsyHC5ncmFwaC52MS5SZXBsaWNhdGVkUHV0RWRnZXNIAFIScmVwbGlj'
    'YXRlZFB1dEVkZ2VzEmwKHnJlcGxpY2F0ZWRfcmVjZWlwdF9lZGdlX2RlbGV0ZRgPIAEoCzIlLm'
    'dyYXBoLnYxLlJlcGxpY2F0ZWRSZWNlaXB0RWRnZURlbGV0ZUgAUhtyZXBsaWNhdGVkUmVjZWlw'
    'dEVkZ2VEZWxldGUSaQodcmVwbGljYXRlZF9yZWNlaXB0X3ZlcnRleF9wdXQYECABKAsyJC5ncm'
    'FwaC52MS5SZXBsaWNhdGVkUmVjZWlwdFZlcnRleFB1dEgAUhpyZXBsaWNhdGVkUmVjZWlwdFZl'
    'cnRleFB1dBJyCiByZXBsaWNhdGVkX3JlY2VpcHRfdmVydGV4X2RlbGV0ZRgRIAEoCzInLmdyYX'
    'BoLnYxLlJlcGxpY2F0ZWRSZWNlaXB0VmVydGV4RGVsZXRlSABSHXJlcGxpY2F0ZWRSZWNlaXB0'
    'VmVydGV4RGVsZXRlQgQKAm9w');

@$core.Deprecated('Use replicatedReceiptEdgeDeleteItemDescriptor instead')
const ReplicatedReceiptEdgeDeleteItem$json = {
  '1': 'ReplicatedReceiptEdgeDeleteItem',
  '2': [
    {
      '1': 'key',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.EdgeKey',
      '10': 'key'
    },
    {
      '1': 'receipt',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.MutationReceipt',
      '10': 'receipt'
    },
    {
      '1': 'causally_accepted',
      '3': 3,
      '4': 1,
      '5': 8,
      '10': 'causallyAccepted'
    },
  ],
};

/// Descriptor for `ReplicatedReceiptEdgeDeleteItem`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedReceiptEdgeDeleteItemDescriptor =
    $convert.base64Decode(
        'Ch9SZXBsaWNhdGVkUmVjZWlwdEVkZ2VEZWxldGVJdGVtEiMKA2tleRgBIAEoCzIRLmdyYXBoLn'
        'YxLkVkZ2VLZXlSA2tleRIzCgdyZWNlaXB0GAIgASgLMhkuZ3JhcGgudjEuTXV0YXRpb25SZWNl'
        'aXB0UgdyZWNlaXB0EisKEWNhdXNhbGx5X2FjY2VwdGVkGAMgASgIUhBjYXVzYWxseUFjY2VwdG'
        'Vk');

@$core.Deprecated('Use replicatedReceiptEdgeDeleteDescriptor instead')
const ReplicatedReceiptEdgeDelete$json = {
  '1': 'ReplicatedReceiptEdgeDelete',
  '2': [
    {'1': 'deployment_epoch', '3': 1, '4': 1, '5': 12, '10': 'deploymentEpoch'},
    {
      '1': 'policy_fingerprint',
      '3': 2,
      '4': 1,
      '5': 12,
      '10': 'policyFingerprint'
    },
    {
      '1': 'tombstone_expiration',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'tombstoneExpiration'
    },
    {
      '1': 'items',
      '3': 4,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ReplicatedReceiptEdgeDeleteItem',
      '10': 'items'
    },
  ],
};

/// Descriptor for `ReplicatedReceiptEdgeDelete`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedReceiptEdgeDeleteDescriptor = $convert.base64Decode(
    'ChtSZXBsaWNhdGVkUmVjZWlwdEVkZ2VEZWxldGUSKQoQZGVwbG95bWVudF9lcG9jaBgBIAEoDF'
    'IPZGVwbG95bWVudEVwb2NoEi0KEnBvbGljeV9maW5nZXJwcmludBgCIAEoDFIRcG9saWN5Rmlu'
    'Z2VycHJpbnQSTQoUdG9tYnN0b25lX2V4cGlyYXRpb24YAyABKAsyGi5nb29nbGUucHJvdG9idW'
    'YuVGltZXN0YW1wUhN0b21ic3RvbmVFeHBpcmF0aW9uEj8KBWl0ZW1zGAQgAygLMikuZ3JhcGgu'
    'djEuUmVwbGljYXRlZFJlY2VpcHRFZGdlRGVsZXRlSXRlbVIFaXRlbXM=');

@$core.Deprecated('Use replicatedReceiptVertexPutItemDescriptor instead')
const ReplicatedReceiptVertexPutItem$json = {
  '1': 'ReplicatedReceiptVertexPutItem',
  '2': [
    {
      '1': 'original',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'original'
    },
    {
      '1': 'receipt',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.MutationReceipt',
      '10': 'receipt'
    },
    {
      '1': 'accepted',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.ReplicatedPutVertex',
      '10': 'accepted'
    },
  ],
};

/// Descriptor for `ReplicatedReceiptVertexPutItem`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedReceiptVertexPutItemDescriptor =
    $convert.base64Decode(
        'Ch5SZXBsaWNhdGVkUmVjZWlwdFZlcnRleFB1dEl0ZW0SLAoIb3JpZ2luYWwYASABKAsyEC5ncm'
        'FwaC52MS5WZXJ0ZXhSCG9yaWdpbmFsEjMKB3JlY2VpcHQYAiABKAsyGS5ncmFwaC52MS5NdXRh'
        'dGlvblJlY2VpcHRSB3JlY2VpcHQSOQoIYWNjZXB0ZWQYAyABKAsyHS5ncmFwaC52MS5SZXBsaW'
        'NhdGVkUHV0VmVydGV4UghhY2NlcHRlZA==');

@$core.Deprecated('Use replicatedReceiptVertexPutDescriptor instead')
const ReplicatedReceiptVertexPut$json = {
  '1': 'ReplicatedReceiptVertexPut',
  '2': [
    {'1': 'deployment_epoch', '3': 1, '4': 1, '5': 12, '10': 'deploymentEpoch'},
    {
      '1': 'policy_fingerprint',
      '3': 2,
      '4': 1,
      '5': 12,
      '10': 'policyFingerprint'
    },
    {'1': 'if_absent', '3': 3, '4': 1, '5': 8, '10': 'ifAbsent'},
    {
      '1': 'items',
      '3': 4,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ReplicatedReceiptVertexPutItem',
      '10': 'items'
    },
  ],
};

/// Descriptor for `ReplicatedReceiptVertexPut`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedReceiptVertexPutDescriptor = $convert.base64Decode(
    'ChpSZXBsaWNhdGVkUmVjZWlwdFZlcnRleFB1dBIpChBkZXBsb3ltZW50X2Vwb2NoGAEgASgMUg'
    '9kZXBsb3ltZW50RXBvY2gSLQoScG9saWN5X2ZpbmdlcnByaW50GAIgASgMUhFwb2xpY3lGaW5n'
    'ZXJwcmludBIbCglpZl9hYnNlbnQYAyABKAhSCGlmQWJzZW50Ej4KBWl0ZW1zGAQgAygLMiguZ3'
    'JhcGgudjEuUmVwbGljYXRlZFJlY2VpcHRWZXJ0ZXhQdXRJdGVtUgVpdGVtcw==');

@$core.Deprecated('Use replicatedReceiptVertexDeleteItemDescriptor instead')
const ReplicatedReceiptVertexDeleteItem$json = {
  '1': 'ReplicatedReceiptVertexDeleteItem',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {
      '1': 'receipt',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.MutationReceipt',
      '10': 'receipt'
    },
    {
      '1': 'causally_accepted',
      '3': 3,
      '4': 1,
      '5': 8,
      '10': 'causallyAccepted'
    },
  ],
};

/// Descriptor for `ReplicatedReceiptVertexDeleteItem`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedReceiptVertexDeleteItemDescriptor =
    $convert.base64Decode(
        'CiFSZXBsaWNhdGVkUmVjZWlwdFZlcnRleERlbGV0ZUl0ZW0SEAoDa2V5GAEgASgJUgNrZXkSMw'
        'oHcmVjZWlwdBgCIAEoCzIZLmdyYXBoLnYxLk11dGF0aW9uUmVjZWlwdFIHcmVjZWlwdBIrChFj'
        'YXVzYWxseV9hY2NlcHRlZBgDIAEoCFIQY2F1c2FsbHlBY2NlcHRlZA==');

@$core.Deprecated('Use replicatedReceiptVertexDeleteDescriptor instead')
const ReplicatedReceiptVertexDelete$json = {
  '1': 'ReplicatedReceiptVertexDelete',
  '2': [
    {'1': 'deployment_epoch', '3': 1, '4': 1, '5': 12, '10': 'deploymentEpoch'},
    {
      '1': 'policy_fingerprint',
      '3': 2,
      '4': 1,
      '5': 12,
      '10': 'policyFingerprint'
    },
    {
      '1': 'tombstone_expiration',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'tombstoneExpiration'
    },
    {
      '1': 'items',
      '3': 4,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ReplicatedReceiptVertexDeleteItem',
      '10': 'items'
    },
  ],
};

/// Descriptor for `ReplicatedReceiptVertexDelete`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedReceiptVertexDeleteDescriptor = $convert.base64Decode(
    'Ch1SZXBsaWNhdGVkUmVjZWlwdFZlcnRleERlbGV0ZRIpChBkZXBsb3ltZW50X2Vwb2NoGAEgAS'
    'gMUg9kZXBsb3ltZW50RXBvY2gSLQoScG9saWN5X2ZpbmdlcnByaW50GAIgASgMUhFwb2xpY3lG'
    'aW5nZXJwcmludBJNChR0b21ic3RvbmVfZXhwaXJhdGlvbhgDIAEoCzIaLmdvb2dsZS5wcm90b2'
    'J1Zi5UaW1lc3RhbXBSE3RvbWJzdG9uZUV4cGlyYXRpb24SQQoFaXRlbXMYBCADKAsyKy5ncmFw'
    'aC52MS5SZXBsaWNhdGVkUmVjZWlwdFZlcnRleERlbGV0ZUl0ZW1SBWl0ZW1z');

@$core.Deprecated('Use vertexCausalBarrierDescriptor instead')
const VertexCausalBarrier$json = {
  '1': 'VertexCausalBarrier',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
  ],
};

/// Descriptor for `VertexCausalBarrier`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List vertexCausalBarrierDescriptor = $convert
    .base64Decode('ChNWZXJ0ZXhDYXVzYWxCYXJyaWVyEhAKA2tleRgBIAEoCVIDa2V5');

@$core.Deprecated('Use replicatedPutVertexDescriptor instead')
const ReplicatedPutVertex$json = {
  '1': 'ReplicatedPutVertex',
  '2': [
    {
      '1': 'live',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '9': 0,
      '10': 'live'
    },
    {
      '1': 'causal_barrier',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.VertexCausalBarrier',
      '9': 0,
      '10': 'causalBarrier'
    },
  ],
  '8': [
    {'1': 'outcome'},
  ],
};

/// Descriptor for `ReplicatedPutVertex`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedPutVertexDescriptor = $convert.base64Decode(
    'ChNSZXBsaWNhdGVkUHV0VmVydGV4EiYKBGxpdmUYASABKAsyEC5ncmFwaC52MS5WZXJ0ZXhIAF'
    'IEbGl2ZRJGCg5jYXVzYWxfYmFycmllchgCIAEoCzIdLmdyYXBoLnYxLlZlcnRleENhdXNhbEJh'
    'cnJpZXJIAFINY2F1c2FsQmFycmllckIJCgdvdXRjb21l');

@$core.Deprecated('Use replicatedPutVerticesDescriptor instead')
const ReplicatedPutVertices$json = {
  '1': 'ReplicatedPutVertices',
  '2': [
    {
      '1': 'entries',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ReplicatedPutVertex',
      '10': 'entries'
    },
  ],
};

/// Descriptor for `ReplicatedPutVertices`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedPutVerticesDescriptor = $convert.base64Decode(
    'ChVSZXBsaWNhdGVkUHV0VmVydGljZXMSNwoHZW50cmllcxgBIAMoCzIdLmdyYXBoLnYxLlJlcG'
    'xpY2F0ZWRQdXRWZXJ0ZXhSB2VudHJpZXM=');

@$core.Deprecated('Use edgeCausalBarrierDescriptor instead')
const EdgeCausalBarrier$json = {
  '1': 'EdgeCausalBarrier',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
  ],
};

/// Descriptor for `EdgeCausalBarrier`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List edgeCausalBarrierDescriptor = $convert.base64Decode(
    'ChFFZGdlQ2F1c2FsQmFycmllchISCgR0YWlsGAEgASgJUgR0YWlsEhIKBGhlYWQYAiABKAlSBG'
    'hlYWQ=');

@$core.Deprecated('Use replicatedPutEdgeDescriptor instead')
const ReplicatedPutEdge$json = {
  '1': 'ReplicatedPutEdge',
  '2': [
    {
      '1': 'live',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Edge',
      '9': 0,
      '10': 'live'
    },
    {
      '1': 'causal_barrier',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.EdgeCausalBarrier',
      '9': 0,
      '10': 'causalBarrier'
    },
  ],
  '8': [
    {'1': 'outcome'},
  ],
};

/// Descriptor for `ReplicatedPutEdge`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedPutEdgeDescriptor = $convert.base64Decode(
    'ChFSZXBsaWNhdGVkUHV0RWRnZRIkCgRsaXZlGAEgASgLMg4uZ3JhcGgudjEuRWRnZUgAUgRsaX'
    'ZlEkQKDmNhdXNhbF9iYXJyaWVyGAIgASgLMhsuZ3JhcGgudjEuRWRnZUNhdXNhbEJhcnJpZXJI'
    'AFINY2F1c2FsQmFycmllckIJCgdvdXRjb21l');

@$core.Deprecated('Use replicatedPutEdgesDescriptor instead')
const ReplicatedPutEdges$json = {
  '1': 'ReplicatedPutEdges',
  '2': [
    {
      '1': 'entries',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ReplicatedPutEdge',
      '10': 'entries'
    },
  ],
};

/// Descriptor for `ReplicatedPutEdges`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicatedPutEdgesDescriptor = $convert.base64Decode(
    'ChJSZXBsaWNhdGVkUHV0RWRnZXMSNQoHZW50cmllcxgBIAMoCzIbLmdyYXBoLnYxLlJlcGxpY2'
    'F0ZWRQdXRFZGdlUgdlbnRyaWVz');

@$core.Deprecated('Use mutationDescriptor instead')
const Mutation$json = {
  '1': 'Mutation',
  '2': [
    {'1': 'seq', '3': 1, '4': 1, '5': 4, '10': 'seq'},
    {
      '1': 'hlc',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
    {'1': 'origin', '3': 3, '4': 1, '5': 12, '10': 'origin'},
    {
      '1': 'op',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.MutationOp',
      '10': 'op'
    },
    {
      '1': 'tombstone_expiration',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'tombstoneExpiration'
    },
  ],
};

/// Descriptor for `Mutation`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List mutationDescriptor = $convert.base64Decode(
    'CghNdXRhdGlvbhIQCgNzZXEYASABKARSA3NlcRIoCgNobGMYAiABKAsyFi5ncmFwaC52MS5ITE'
    'NUaW1lc3RhbXBSA2hsYxIWCgZvcmlnaW4YAyABKAxSBm9yaWdpbhIkCgJvcBgEIAEoCzIULmdy'
    'YXBoLnYxLk11dGF0aW9uT3BSAm9wEk0KFHRvbWJzdG9uZV9leHBpcmF0aW9uGAUgASgLMhouZ2'
    '9vZ2xlLnByb3RvYnVmLlRpbWVzdGFtcFITdG9tYnN0b25lRXhwaXJhdGlvbg==');

@$core.Deprecated('Use subscribeRequestDescriptor instead')
const SubscribeRequest$json = {
  '1': 'SubscribeRequest',
  '2': [
    {
      '1': 'from_seq_per_origin',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SubscribeRequest.FromSeqPerOriginEntry',
      '10': 'fromSeqPerOrigin'
    },
    {'1': 'from_local_seq', '3': 2, '4': 1, '5': 4, '10': 'fromLocalSeq'},
    {
      '1': 'projection',
      '3': 3,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SubscribeProjection',
      '10': 'projection'
    },
    {'1': 'bootstrap', '3': 4, '4': 1, '5': 8, '10': 'bootstrap'},
    {
      '1': 'accept_receipt_envelopes',
      '3': 5,
      '4': 1,
      '5': 8,
      '10': 'acceptReceiptEnvelopes'
    },
  ],
  '3': [SubscribeRequest_FromSeqPerOriginEntry$json],
};

@$core.Deprecated('Use subscribeRequestDescriptor instead')
const SubscribeRequest_FromSeqPerOriginEntry$json = {
  '1': 'FromSeqPerOriginEntry',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {'1': 'value', '3': 2, '4': 1, '5': 4, '10': 'value'},
  ],
  '7': {'7': true},
};

/// Descriptor for `SubscribeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List subscribeRequestDescriptor = $convert.base64Decode(
    'ChBTdWJzY3JpYmVSZXF1ZXN0El8KE2Zyb21fc2VxX3Blcl9vcmlnaW4YASADKAsyMC5ncmFwaC'
    '52MS5TdWJzY3JpYmVSZXF1ZXN0LkZyb21TZXFQZXJPcmlnaW5FbnRyeVIQZnJvbVNlcVBlck9y'
    'aWdpbhIkCg5mcm9tX2xvY2FsX3NlcRgCIAEoBFIMZnJvbUxvY2FsU2VxEj0KCnByb2plY3Rpb2'
    '4YAyABKA4yHS5ncmFwaC52MS5TdWJzY3JpYmVQcm9qZWN0aW9uUgpwcm9qZWN0aW9uEhwKCWJv'
    'b3RzdHJhcBgEIAEoCFIJYm9vdHN0cmFwEjgKGGFjY2VwdF9yZWNlaXB0X2VudmVsb3BlcxgFIA'
    'EoCFIWYWNjZXB0UmVjZWlwdEVudmVsb3BlcxpDChVGcm9tU2VxUGVyT3JpZ2luRW50cnkSEAoD'
    'a2V5GAEgASgJUgNrZXkSFAoFdmFsdWUYAiABKARSBXZhbHVlOgI4AQ==');

@$core.Deprecated('Use identityCheckpointDescriptor instead')
const IdentityCheckpoint$json = {
  '1': 'IdentityCheckpoint',
  '2': [
    {
      '1': 'last_seq_per_origin',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.IdentityCheckpoint.LastSeqPerOriginEntry',
      '10': 'lastSeqPerOrigin'
    },
  ],
  '3': [IdentityCheckpoint_LastSeqPerOriginEntry$json],
};

@$core.Deprecated('Use identityCheckpointDescriptor instead')
const IdentityCheckpoint_LastSeqPerOriginEntry$json = {
  '1': 'LastSeqPerOriginEntry',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {'1': 'value', '3': 2, '4': 1, '5': 4, '10': 'value'},
  ],
  '7': {'7': true},
};

/// Descriptor for `IdentityCheckpoint`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List identityCheckpointDescriptor = $convert.base64Decode(
    'ChJJZGVudGl0eUNoZWNrcG9pbnQSYQoTbGFzdF9zZXFfcGVyX29yaWdpbhgBIAMoCzIyLmdyYX'
    'BoLnYxLklkZW50aXR5Q2hlY2twb2ludC5MYXN0U2VxUGVyT3JpZ2luRW50cnlSEGxhc3RTZXFQ'
    'ZXJPcmlnaW4aQwoVTGFzdFNlcVBlck9yaWdpbkVudHJ5EhAKA2tleRgBIAEoCVIDa2V5EhQKBX'
    'ZhbHVlGAIgASgEUgV2YWx1ZToCOAE=');

@$core.Deprecated('Use identityChunkDescriptor instead')
const IdentityChunk$json = {
  '1': 'IdentityChunk',
  '2': [
    {'1': 'origin', '3': 1, '4': 1, '5': 12, '10': 'origin'},
    {'1': 'seq', '3': 2, '4': 1, '5': 4, '10': 'seq'},
    {
      '1': 'hlc',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
    {
      '1': 'operation',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.IdentityOperation',
      '10': 'operation'
    },
    {'1': 'chunk_index', '3': 5, '4': 1, '5': 13, '10': 'chunkIndex'},
    {'1': 'is_last', '3': 6, '4': 1, '5': 8, '10': 'isLast'},
    {'1': 'vertex_keys', '3': 7, '4': 3, '5': 9, '10': 'vertexKeys'},
    {
      '1': 'edge_keys',
      '3': 8,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.EdgeKey',
      '10': 'edgeKeys'
    },
    {'1': 'first_item_index', '3': 9, '4': 1, '5': 13, '10': 'firstItemIndex'},
  ],
};

/// Descriptor for `IdentityChunk`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List identityChunkDescriptor = $convert.base64Decode(
    'Cg1JZGVudGl0eUNodW5rEhYKBm9yaWdpbhgBIAEoDFIGb3JpZ2luEhAKA3NlcRgCIAEoBFIDc2'
    'VxEigKA2hsYxgDIAEoCzIWLmdyYXBoLnYxLkhMQ1RpbWVzdGFtcFIDaGxjEjkKCW9wZXJhdGlv'
    'bhgEIAEoDjIbLmdyYXBoLnYxLklkZW50aXR5T3BlcmF0aW9uUglvcGVyYXRpb24SHwoLY2h1bm'
    'tfaW5kZXgYBSABKA1SCmNodW5rSW5kZXgSFwoHaXNfbGFzdBgGIAEoCFIGaXNMYXN0Eh8KC3Zl'
    'cnRleF9rZXlzGAcgAygJUgp2ZXJ0ZXhLZXlzEi4KCWVkZ2Vfa2V5cxgIIAMoCzIRLmdyYXBoLn'
    'YxLkVkZ2VLZXlSCGVkZ2VLZXlzEigKEGZpcnN0X2l0ZW1faW5kZXgYCSABKA1SDmZpcnN0SXRl'
    'bUluZGV4');

@$core.Deprecated('Use subscribeResponseDescriptor instead')
const SubscribeResponse$json = {
  '1': 'SubscribeResponse',
  '2': [
    {
      '1': 'mutation',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Mutation',
      '9': 0,
      '10': 'mutation'
    },
    {
      '1': 'checkpoint',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.IdentityCheckpoint',
      '9': 0,
      '10': 'checkpoint'
    },
    {
      '1': 'identity_chunk',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.IdentityChunk',
      '9': 0,
      '10': 'identityChunk'
    },
  ],
  '8': [
    {'1': 'event'},
  ],
};

/// Descriptor for `SubscribeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List subscribeResponseDescriptor = $convert.base64Decode(
    'ChFTdWJzY3JpYmVSZXNwb25zZRIwCghtdXRhdGlvbhgBIAEoCzISLmdyYXBoLnYxLk11dGF0aW'
    '9uSABSCG11dGF0aW9uEj4KCmNoZWNrcG9pbnQYAiABKAsyHC5ncmFwaC52MS5JZGVudGl0eUNo'
    'ZWNrcG9pbnRIAFIKY2hlY2twb2ludBJACg5pZGVudGl0eV9jaHVuaxgDIAEoCzIXLmdyYXBoLn'
    'YxLklkZW50aXR5Q2h1bmtIAFINaWRlbnRpdHlDaHVua0IHCgVldmVudA==');

@$core.Deprecated('Use snapshotRequestDescriptor instead')
const SnapshotRequest$json = {
  '1': 'SnapshotRequest',
  '2': [
    {
      '1': 'required_format',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SnapshotFormat',
      '10': 'requiredFormat'
    },
  ],
};

/// Descriptor for `SnapshotRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotRequestDescriptor = $convert.base64Decode(
    'Cg9TbmFwc2hvdFJlcXVlc3QSQQoPcmVxdWlyZWRfZm9ybWF0GAEgASgOMhguZ3JhcGgudjEuU2'
    '5hcHNob3RGb3JtYXRSDnJlcXVpcmVkRm9ybWF0');

@$core.Deprecated('Use snapshotReceiptMetadataDescriptor instead')
const SnapshotReceiptMetadata$json = {
  '1': 'SnapshotReceiptMetadata',
  '2': [
    {
      '1': 'active_policy',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.ReceiptPolicy',
      '10': 'activePolicy'
    },
    {
      '1': 'clock_high_water_unix_ms',
      '3': 2,
      '4': 1,
      '5': 4,
      '10': 'clockHighWaterUnixMs'
    },
    {
      '1': 'origin_cutoffs',
      '3': 3,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.OriginState',
      '10': 'originCutoffs'
    },
    {
      '1': 'retired_policies',
      '3': 4,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ReceiptPolicy',
      '10': 'retiredPolicies'
    },
  ],
};

/// Descriptor for `SnapshotReceiptMetadata`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotReceiptMetadataDescriptor = $convert.base64Decode(
    'ChdTbmFwc2hvdFJlY2VpcHRNZXRhZGF0YRI8Cg1hY3RpdmVfcG9saWN5GAEgASgLMhcuZ3JhcG'
    'gudjEuUmVjZWlwdFBvbGljeVIMYWN0aXZlUG9saWN5EjYKGGNsb2NrX2hpZ2hfd2F0ZXJfdW5p'
    'eF9tcxgCIAEoBFIUY2xvY2tIaWdoV2F0ZXJVbml4TXMSPAoOb3JpZ2luX2N1dG9mZnMYAyADKA'
    'syFS5ncmFwaC52MS5PcmlnaW5TdGF0ZVINb3JpZ2luQ3V0b2ZmcxJCChByZXRpcmVkX3BvbGlj'
    'aWVzGAQgAygLMhcuZ3JhcGgudjEuUmVjZWlwdFBvbGljeVIPcmV0aXJlZFBvbGljaWVz');

@$core.Deprecated('Use snapshotHeaderDescriptor instead')
const SnapshotHeader$json = {
  '1': 'SnapshotHeader',
  '2': [
    {
      '1': 'cutoff_seq_per_origin',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SnapshotHeader.CutoffSeqPerOriginEntry',
      '10': 'cutoffSeqPerOrigin'
    },
    {
      '1': 'cutoff_hlc',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'cutoffHlc'
    },
    {'1': 'cutoff_local_seq', '3': 3, '4': 1, '5': 4, '10': 'cutoffLocalSeq'},
    {
      '1': 'format',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SnapshotFormat',
      '10': 'format'
    },
    {
      '1': 'receipt_metadata',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotReceiptMetadata',
      '10': 'receiptMetadata'
    },
  ],
  '3': [SnapshotHeader_CutoffSeqPerOriginEntry$json],
};

@$core.Deprecated('Use snapshotHeaderDescriptor instead')
const SnapshotHeader_CutoffSeqPerOriginEntry$json = {
  '1': 'CutoffSeqPerOriginEntry',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {'1': 'value', '3': 2, '4': 1, '5': 4, '10': 'value'},
  ],
  '7': {'7': true},
};

/// Descriptor for `SnapshotHeader`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotHeaderDescriptor = $convert.base64Decode(
    'Cg5TbmFwc2hvdEhlYWRlchJjChVjdXRvZmZfc2VxX3Blcl9vcmlnaW4YASADKAsyMC5ncmFwaC'
    '52MS5TbmFwc2hvdEhlYWRlci5DdXRvZmZTZXFQZXJPcmlnaW5FbnRyeVISY3V0b2ZmU2VxUGVy'
    'T3JpZ2luEjUKCmN1dG9mZl9obGMYAiABKAsyFi5ncmFwaC52MS5ITENUaW1lc3RhbXBSCWN1dG'
    '9mZkhsYxIoChBjdXRvZmZfbG9jYWxfc2VxGAMgASgEUg5jdXRvZmZMb2NhbFNlcRIwCgZmb3Jt'
    'YXQYBCABKA4yGC5ncmFwaC52MS5TbmFwc2hvdEZvcm1hdFIGZm9ybWF0EkwKEHJlY2VpcHRfbW'
    'V0YWRhdGEYBSABKAsyIS5ncmFwaC52MS5TbmFwc2hvdFJlY2VpcHRNZXRhZGF0YVIPcmVjZWlw'
    'dE1ldGFkYXRhGkUKF0N1dG9mZlNlcVBlck9yaWdpbkVudHJ5EhAKA2tleRgBIAEoCVIDa2V5Eh'
    'QKBXZhbHVlGAIgASgEUgV2YWx1ZToCOAE=');

@$core.Deprecated('Use snapshotFooterDescriptor instead')
const SnapshotFooter$json = {
  '1': 'SnapshotFooter',
  '2': [
    {'1': 'vertex_count', '3': 1, '4': 1, '5': 4, '10': 'vertexCount'},
    {'1': 'edge_count', '3': 2, '4': 1, '5': 4, '10': 'edgeCount'},
    {
      '1': 'vertex_causal_barrier_count',
      '3': 3,
      '4': 1,
      '5': 4,
      '10': 'vertexCausalBarrierCount'
    },
    {
      '1': 'edge_causal_barrier_count',
      '3': 4,
      '4': 1,
      '5': 4,
      '10': 'edgeCausalBarrierCount'
    },
    {
      '1': 'vertex_tombstone_count',
      '3': 5,
      '4': 1,
      '5': 4,
      '10': 'vertexTombstoneCount'
    },
    {
      '1': 'edge_tombstone_count',
      '3': 6,
      '4': 1,
      '5': 4,
      '10': 'edgeTombstoneCount'
    },
    {
      '1': 'active_receipt_count',
      '3': 7,
      '4': 1,
      '5': 4,
      '10': 'activeReceiptCount'
    },
    {'1': 'origin_count', '3': 8, '4': 1, '5': 4, '10': 'originCount'},
    {
      '1': 'retired_epoch_count',
      '3': 9,
      '4': 1,
      '5': 4,
      '10': 'retiredEpochCount'
    },
    {
      '1': 'retired_receipt_count',
      '3': 10,
      '4': 1,
      '5': 4,
      '10': 'retiredReceiptCount'
    },
  ],
};

/// Descriptor for `SnapshotFooter`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotFooterDescriptor = $convert.base64Decode(
    'Cg5TbmFwc2hvdEZvb3RlchIhCgx2ZXJ0ZXhfY291bnQYASABKARSC3ZlcnRleENvdW50Eh0KCm'
    'VkZ2VfY291bnQYAiABKARSCWVkZ2VDb3VudBI9Cht2ZXJ0ZXhfY2F1c2FsX2JhcnJpZXJfY291'
    'bnQYAyABKARSGHZlcnRleENhdXNhbEJhcnJpZXJDb3VudBI5ChllZGdlX2NhdXNhbF9iYXJyaW'
    'VyX2NvdW50GAQgASgEUhZlZGdlQ2F1c2FsQmFycmllckNvdW50EjQKFnZlcnRleF90b21ic3Rv'
    'bmVfY291bnQYBSABKARSFHZlcnRleFRvbWJzdG9uZUNvdW50EjAKFGVkZ2VfdG9tYnN0b25lX2'
    'NvdW50GAYgASgEUhJlZGdlVG9tYnN0b25lQ291bnQSMAoUYWN0aXZlX3JlY2VpcHRfY291bnQY'
    'ByABKARSEmFjdGl2ZVJlY2VpcHRDb3VudBIhCgxvcmlnaW5fY291bnQYCCABKARSC29yaWdpbk'
    'NvdW50Ei4KE3JldGlyZWRfZXBvY2hfY291bnQYCSABKARSEXJldGlyZWRFcG9jaENvdW50EjIK'
    'FXJldGlyZWRfcmVjZWlwdF9jb3VudBgKIAEoBFITcmV0aXJlZFJlY2VpcHRDb3VudA==');

@$core.Deprecated('Use snapshotReceiptContributionDescriptor instead')
const SnapshotReceiptContribution$json = {
  '1': 'SnapshotReceiptContribution',
  '2': [
    {'1': 'contribution_id', '3': 1, '4': 1, '5': 12, '10': 'contributionId'},
  ],
};

/// Descriptor for `SnapshotReceiptContribution`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotReceiptContributionDescriptor =
    $convert.base64Decode(
        'ChtTbmFwc2hvdFJlY2VpcHRDb250cmlidXRpb24SJwoPY29udHJpYnV0aW9uX2lkGAEgASgMUg'
        '5jb250cmlidXRpb25JZA==');

@$core.Deprecated('Use snapshotReceiptDescriptor instead')
const SnapshotReceipt$json = {
  '1': 'SnapshotReceipt',
  '2': [
    {'1': 'operation_id', '3': 1, '4': 1, '5': 12, '10': 'operationId'},
    {'1': 'logical_call_id', '3': 2, '4': 1, '5': 12, '10': 'logicalCallId'},
    {'1': 'item_index', '3': 3, '4': 1, '5': 13, '10': 'itemIndex'},
    {'1': 'item_count', '3': 4, '4': 1, '5': 13, '10': 'itemCount'},
    {
      '1': 'kind',
      '3': 5,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SnapshotReceiptKind',
      '10': 'kind'
    },
    {'1': 'intent_sha256', '3': 6, '4': 1, '5': 12, '10': 'intentSha256'},
    {'1': 'deadline_unix_ms', '3': 7, '4': 1, '5': 4, '10': 'deadlineUnixMs'},
    {'1': 'original_result', '3': 8, '4': 1, '5': 12, '10': 'originalResult'},
    {
      '1': 'contribution',
      '3': 9,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotReceiptContribution',
      '10': 'contribution'
    },
  ],
};

/// Descriptor for `SnapshotReceipt`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotReceiptDescriptor = $convert.base64Decode(
    'Cg9TbmFwc2hvdFJlY2VpcHQSIQoMb3BlcmF0aW9uX2lkGAEgASgMUgtvcGVyYXRpb25JZBImCg'
    '9sb2dpY2FsX2NhbGxfaWQYAiABKAxSDWxvZ2ljYWxDYWxsSWQSHQoKaXRlbV9pbmRleBgDIAEo'
    'DVIJaXRlbUluZGV4Eh0KCml0ZW1fY291bnQYBCABKA1SCWl0ZW1Db3VudBIxCgRraW5kGAUgAS'
    'gOMh0uZ3JhcGgudjEuU25hcHNob3RSZWNlaXB0S2luZFIEa2luZBIjCg1pbnRlbnRfc2hhMjU2'
    'GAYgASgMUgxpbnRlbnRTaGEyNTYSKAoQZGVhZGxpbmVfdW5peF9tcxgHIAEoBFIOZGVhZGxpbm'
    'VVbml4TXMSJwoPb3JpZ2luYWxfcmVzdWx0GAggASgMUg5vcmlnaW5hbFJlc3VsdBJJCgxjb250'
    'cmlidXRpb24YCSABKAsyJS5ncmFwaC52MS5TbmFwc2hvdFJlY2VpcHRDb250cmlidXRpb25SDG'
    'NvbnRyaWJ1dGlvbg==');

@$core.Deprecated('Use snapshotVertexDescriptor instead')
const SnapshotVertex$json = {
  '1': 'SnapshotVertex',
  '2': [
    {
      '1': 'vertex',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertex'
    },
    {
      '1': 'hlc',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
  ],
};

/// Descriptor for `SnapshotVertex`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotVertexDescriptor = $convert.base64Decode(
    'Cg5TbmFwc2hvdFZlcnRleBIoCgZ2ZXJ0ZXgYASABKAsyEC5ncmFwaC52MS5WZXJ0ZXhSBnZlcn'
    'RleBIoCgNobGMYAiABKAsyFi5ncmFwaC52MS5ITENUaW1lc3RhbXBSA2hsYw==');

@$core.Deprecated('Use snapshotVertexCausalBarrierDescriptor instead')
const SnapshotVertexCausalBarrier$json = {
  '1': 'SnapshotVertexCausalBarrier',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {
      '1': 'hlc',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
  ],
};

/// Descriptor for `SnapshotVertexCausalBarrier`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotVertexCausalBarrierDescriptor =
    $convert.base64Decode(
        'ChtTbmFwc2hvdFZlcnRleENhdXNhbEJhcnJpZXISEAoDa2V5GAEgASgJUgNrZXkSKAoDaGxjGA'
        'IgASgLMhYuZ3JhcGgudjEuSExDVGltZXN0YW1wUgNobGM=');

@$core.Deprecated('Use snapshotEdgeContributionDescriptor instead')
const SnapshotEdgeContribution$json = {
  '1': 'SnapshotEdgeContribution',
  '2': [
    {'1': 'weight', '3': 1, '4': 1, '5': 2, '10': 'weight'},
    {
      '1': 'expiration',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiration'
    },
    {'1': 'contrib_id', '3': 3, '4': 1, '5': 12, '10': 'contribId'},
    {
      '1': 'hlc',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
  ],
};

/// Descriptor for `SnapshotEdgeContribution`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotEdgeContributionDescriptor = $convert.base64Decode(
    'ChhTbmFwc2hvdEVkZ2VDb250cmlidXRpb24SFgoGd2VpZ2h0GAEgASgCUgZ3ZWlnaHQSOgoKZX'
    'hwaXJhdGlvbhgCIAEoCzIaLmdvb2dsZS5wcm90b2J1Zi5UaW1lc3RhbXBSCmV4cGlyYXRpb24S'
    'HQoKY29udHJpYl9pZBgDIAEoDFIJY29udHJpYklkEigKA2hsYxgEIAEoCzIWLmdyYXBoLnYxLk'
    'hMQ1RpbWVzdGFtcFIDaGxj');

@$core.Deprecated('Use snapshotEdgeDescriptor instead')
const SnapshotEdge$json = {
  '1': 'SnapshotEdge',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
    {
      '1': 'hlc',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
    {
      '1': 'contributions',
      '3': 4,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SnapshotEdgeContribution',
      '10': 'contributions'
    },
  ],
};

/// Descriptor for `SnapshotEdge`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotEdgeDescriptor = $convert.base64Decode(
    'CgxTbmFwc2hvdEVkZ2USEgoEdGFpbBgBIAEoCVIEdGFpbBISCgRoZWFkGAIgASgJUgRoZWFkEi'
    'gKA2hsYxgDIAEoCzIWLmdyYXBoLnYxLkhMQ1RpbWVzdGFtcFIDaGxjEkgKDWNvbnRyaWJ1dGlv'
    'bnMYBCADKAsyIi5ncmFwaC52MS5TbmFwc2hvdEVkZ2VDb250cmlidXRpb25SDWNvbnRyaWJ1dG'
    'lvbnM=');

@$core.Deprecated('Use snapshotEdgeCausalBarrierDescriptor instead')
const SnapshotEdgeCausalBarrier$json = {
  '1': 'SnapshotEdgeCausalBarrier',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
    {
      '1': 'hlc',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
  ],
};

/// Descriptor for `SnapshotEdgeCausalBarrier`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotEdgeCausalBarrierDescriptor = $convert.base64Decode(
    'ChlTbmFwc2hvdEVkZ2VDYXVzYWxCYXJyaWVyEhIKBHRhaWwYASABKAlSBHRhaWwSEgoEaGVhZB'
    'gCIAEoCVIEaGVhZBIoCgNobGMYAyABKAsyFi5ncmFwaC52MS5ITENUaW1lc3RhbXBSA2hsYw==');

@$core.Deprecated('Use snapshotVertexTombstoneDescriptor instead')
const SnapshotVertexTombstone$json = {
  '1': 'SnapshotVertexTombstone',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {
      '1': 'hlc',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
    {
      '1': 'expiration',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiration'
    },
  ],
};

/// Descriptor for `SnapshotVertexTombstone`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotVertexTombstoneDescriptor = $convert.base64Decode(
    'ChdTbmFwc2hvdFZlcnRleFRvbWJzdG9uZRIQCgNrZXkYASABKAlSA2tleRIoCgNobGMYAiABKA'
    'syFi5ncmFwaC52MS5ITENUaW1lc3RhbXBSA2hsYxI6CgpleHBpcmF0aW9uGAMgASgLMhouZ29v'
    'Z2xlLnByb3RvYnVmLlRpbWVzdGFtcFIKZXhwaXJhdGlvbg==');

@$core.Deprecated('Use snapshotEdgeTombstoneDescriptor instead')
const SnapshotEdgeTombstone$json = {
  '1': 'SnapshotEdgeTombstone',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
    {
      '1': 'hlc',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'hlc'
    },
    {
      '1': 'expiration',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiration'
    },
  ],
};

/// Descriptor for `SnapshotEdgeTombstone`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotEdgeTombstoneDescriptor = $convert.base64Decode(
    'ChVTbmFwc2hvdEVkZ2VUb21ic3RvbmUSEgoEdGFpbBgBIAEoCVIEdGFpbBISCgRoZWFkGAIgAS'
    'gJUgRoZWFkEigKA2hsYxgDIAEoCzIWLmdyYXBoLnYxLkhMQ1RpbWVzdGFtcFIDaGxjEjoKCmV4'
    'cGlyYXRpb24YBCABKAsyGi5nb29nbGUucHJvdG9idWYuVGltZXN0YW1wUgpleHBpcmF0aW9u');

@$core.Deprecated('Use snapshotResponseDescriptor instead')
const SnapshotResponse$json = {
  '1': 'SnapshotResponse',
  '2': [
    {
      '1': 'header',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotHeader',
      '9': 0,
      '10': 'header'
    },
    {
      '1': 'vertex',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotVertex',
      '9': 0,
      '10': 'vertex'
    },
    {
      '1': 'edge',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotEdge',
      '9': 0,
      '10': 'edge'
    },
    {
      '1': 'footer',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotFooter',
      '9': 0,
      '10': 'footer'
    },
    {
      '1': 'vertex_causal_barrier',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotVertexCausalBarrier',
      '9': 0,
      '10': 'vertexCausalBarrier'
    },
    {
      '1': 'edge_causal_barrier',
      '3': 6,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotEdgeCausalBarrier',
      '9': 0,
      '10': 'edgeCausalBarrier'
    },
    {
      '1': 'vertex_tombstone',
      '3': 7,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotVertexTombstone',
      '9': 0,
      '10': 'vertexTombstone'
    },
    {
      '1': 'edge_tombstone',
      '3': 8,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotEdgeTombstone',
      '9': 0,
      '10': 'edgeTombstone'
    },
    {
      '1': 'receipt',
      '3': 9,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SnapshotReceipt',
      '9': 0,
      '10': 'receipt'
    },
  ],
  '8': [
    {'1': 'entry'},
  ],
};

/// Descriptor for `SnapshotResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotResponseDescriptor = $convert.base64Decode(
    'ChBTbmFwc2hvdFJlc3BvbnNlEjIKBmhlYWRlchgBIAEoCzIYLmdyYXBoLnYxLlNuYXBzaG90SG'
    'VhZGVySABSBmhlYWRlchIyCgZ2ZXJ0ZXgYAiABKAsyGC5ncmFwaC52MS5TbmFwc2hvdFZlcnRl'
    'eEgAUgZ2ZXJ0ZXgSLAoEZWRnZRgDIAEoCzIWLmdyYXBoLnYxLlNuYXBzaG90RWRnZUgAUgRlZG'
    'dlEjIKBmZvb3RlchgEIAEoCzIYLmdyYXBoLnYxLlNuYXBzaG90Rm9vdGVySABSBmZvb3RlchJb'
    'ChV2ZXJ0ZXhfY2F1c2FsX2JhcnJpZXIYBSABKAsyJS5ncmFwaC52MS5TbmFwc2hvdFZlcnRleE'
    'NhdXNhbEJhcnJpZXJIAFITdmVydGV4Q2F1c2FsQmFycmllchJVChNlZGdlX2NhdXNhbF9iYXJy'
    'aWVyGAYgASgLMiMuZ3JhcGgudjEuU25hcHNob3RFZGdlQ2F1c2FsQmFycmllckgAUhFlZGdlQ2'
    'F1c2FsQmFycmllchJOChB2ZXJ0ZXhfdG9tYnN0b25lGAcgASgLMiEuZ3JhcGgudjEuU25hcHNo'
    'b3RWZXJ0ZXhUb21ic3RvbmVIAFIPdmVydGV4VG9tYnN0b25lEkgKDmVkZ2VfdG9tYnN0b25lGA'
    'ggASgLMh8uZ3JhcGgudjEuU25hcHNob3RFZGdlVG9tYnN0b25lSABSDWVkZ2VUb21ic3RvbmUS'
    'NQoHcmVjZWlwdBgJIAEoCzIZLmdyYXBoLnYxLlNuYXBzaG90UmVjZWlwdEgAUgdyZWNlaXB0Qg'
    'cKBWVudHJ5');

@$core.Deprecated('Use peerStatusRequestDescriptor instead')
const PeerStatusRequest$json = {
  '1': 'PeerStatusRequest',
};

/// Descriptor for `PeerStatusRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List peerStatusRequestDescriptor =
    $convert.base64Decode('ChFQZWVyU3RhdHVzUmVxdWVzdA==');

@$core.Deprecated('Use originStateDescriptor instead')
const OriginState$json = {
  '1': 'OriginState',
  '2': [
    {'1': 'origin', '3': 1, '4': 1, '5': 12, '10': 'origin'},
    {'1': 'last_seq', '3': 2, '4': 1, '5': 4, '10': 'lastSeq'},
    {
      '1': 'last_hlc',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.HLCTimestamp',
      '10': 'lastHlc'
    },
  ],
};

/// Descriptor for `OriginState`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List originStateDescriptor = $convert.base64Decode(
    'CgtPcmlnaW5TdGF0ZRIWCgZvcmlnaW4YASABKAxSBm9yaWdpbhIZCghsYXN0X3NlcRgCIAEoBF'
    'IHbGFzdFNlcRIxCghsYXN0X2hsYxgDIAEoCzIWLmdyYXBoLnYxLkhMQ1RpbWVzdGFtcFIHbGFz'
    'dEhsYw==');

@$core.Deprecated('Use peerStatusResponseDescriptor instead')
const PeerStatusResponse$json = {
  '1': 'PeerStatusResponse',
  '2': [
    {'1': 'self_origin', '3': 1, '4': 1, '5': 12, '10': 'selfOrigin'},
    {
      '1': 'origins',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.OriginState',
      '10': 'origins'
    },
    {
      '1': 'search_config_fingerprint',
      '3': 3,
      '4': 1,
      '5': 9,
      '10': 'searchConfigFingerprint'
    },
    {
      '1': 'required_snapshot_format',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SnapshotFormat',
      '10': 'requiredSnapshotFormat'
    },
  ],
};

/// Descriptor for `PeerStatusResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List peerStatusResponseDescriptor = $convert.base64Decode(
    'ChJQZWVyU3RhdHVzUmVzcG9uc2USHwoLc2VsZl9vcmlnaW4YASABKAxSCnNlbGZPcmlnaW4SLw'
    'oHb3JpZ2lucxgCIAMoCzIVLmdyYXBoLnYxLk9yaWdpblN0YXRlUgdvcmlnaW5zEjoKGXNlYXJj'
    'aF9jb25maWdfZmluZ2VycHJpbnQYAyABKAlSF3NlYXJjaENvbmZpZ0ZpbmdlcnByaW50ElIKGH'
    'JlcXVpcmVkX3NuYXBzaG90X2Zvcm1hdBgEIAEoDjIYLmdyYXBoLnYxLlNuYXBzaG90Rm9ybWF0'
    'UhZyZXF1aXJlZFNuYXBzaG90Rm9ybWF0');

const $core.Map<$core.String, $core.dynamic>
    LanternReplicationServiceBase$json = {
  '1': 'LanternReplicationService',
  '2': [
    {
      '1': 'Subscribe',
      '2': '.graph.v1.SubscribeRequest',
      '3': '.graph.v1.SubscribeResponse',
      '6': true
    },
    {
      '1': 'Snapshot',
      '2': '.graph.v1.SnapshotRequest',
      '3': '.graph.v1.SnapshotResponse',
      '6': true
    },
    {
      '1': 'PeerStatus',
      '2': '.graph.v1.PeerStatusRequest',
      '3': '.graph.v1.PeerStatusResponse'
    },
  ],
};

@$core.Deprecated('Use lanternReplicationServiceDescriptor instead')
const $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
    LanternReplicationServiceBase$messageJson = {
  '.graph.v1.SubscribeRequest': SubscribeRequest$json,
  '.graph.v1.SubscribeRequest.FromSeqPerOriginEntry':
      SubscribeRequest_FromSeqPerOriginEntry$json,
  '.graph.v1.SubscribeResponse': SubscribeResponse$json,
  '.graph.v1.Mutation': Mutation$json,
  '.graph.v1.HLCTimestamp': HLCTimestamp$json,
  '.graph.v1.MutationOp': MutationOp$json,
  '.graph.v1.PutVertexRequest': $0.PutVertexRequest$json,
  '.graph.v1.Vertex': $0.Vertex$json,
  '.google.protobuf.Timestamp': $1.Timestamp$json,
  '.google.protobuf.Duration': $2.Duration$json,
  '.graph.v1.PutVerticesRequest': $0.PutVerticesRequest$json,
  '.graph.v1.DeleteVertexRequest': $0.DeleteVertexRequest$json,
  '.graph.v1.DeleteVerticesRequest': $0.DeleteVerticesRequest$json,
  '.graph.v1.DeleteVerticesByPrefixRequest':
      $0.DeleteVerticesByPrefixRequest$json,
  '.graph.v1.AddEdgeRequest': $0.AddEdgeRequest$json,
  '.graph.v1.Edge': $0.Edge$json,
  '.graph.v1.AddEdgesRequest': $0.AddEdgesRequest$json,
  '.graph.v1.PutEdgeRequest': $0.PutEdgeRequest$json,
  '.graph.v1.PutEdgesRequest': $0.PutEdgesRequest$json,
  '.graph.v1.DeleteEdgeRequest': $0.DeleteEdgeRequest$json,
  '.graph.v1.DeleteEdgesRequest': $0.DeleteEdgesRequest$json,
  '.graph.v1.EdgeKey': $0.EdgeKey$json,
  '.graph.v1.DeleteEdgesByPrefixRequest': $0.DeleteEdgesByPrefixRequest$json,
  '.graph.v1.ReplicatedPutVertices': ReplicatedPutVertices$json,
  '.graph.v1.ReplicatedPutVertex': ReplicatedPutVertex$json,
  '.graph.v1.VertexCausalBarrier': VertexCausalBarrier$json,
  '.graph.v1.ReplicatedPutEdges': ReplicatedPutEdges$json,
  '.graph.v1.ReplicatedPutEdge': ReplicatedPutEdge$json,
  '.graph.v1.EdgeCausalBarrier': EdgeCausalBarrier$json,
  '.graph.v1.ReplicatedReceiptEdgeDelete': ReplicatedReceiptEdgeDelete$json,
  '.graph.v1.ReplicatedReceiptEdgeDeleteItem':
      ReplicatedReceiptEdgeDeleteItem$json,
  '.graph.v1.MutationReceipt': $0.MutationReceipt$json,
  '.graph.v1.ReceiptResult': $0.ReceiptResult$json,
  '.graph.v1.ReplicatedReceiptVertexPut': ReplicatedReceiptVertexPut$json,
  '.graph.v1.ReplicatedReceiptVertexPutItem':
      ReplicatedReceiptVertexPutItem$json,
  '.graph.v1.ReplicatedReceiptVertexDelete': ReplicatedReceiptVertexDelete$json,
  '.graph.v1.ReplicatedReceiptVertexDeleteItem':
      ReplicatedReceiptVertexDeleteItem$json,
  '.graph.v1.IdentityCheckpoint': IdentityCheckpoint$json,
  '.graph.v1.IdentityCheckpoint.LastSeqPerOriginEntry':
      IdentityCheckpoint_LastSeqPerOriginEntry$json,
  '.graph.v1.IdentityChunk': IdentityChunk$json,
  '.graph.v1.SnapshotRequest': SnapshotRequest$json,
  '.graph.v1.SnapshotResponse': SnapshotResponse$json,
  '.graph.v1.SnapshotHeader': SnapshotHeader$json,
  '.graph.v1.SnapshotHeader.CutoffSeqPerOriginEntry':
      SnapshotHeader_CutoffSeqPerOriginEntry$json,
  '.graph.v1.SnapshotReceiptMetadata': SnapshotReceiptMetadata$json,
  '.graph.v1.ReceiptPolicy': $0.ReceiptPolicy$json,
  '.graph.v1.OriginState': OriginState$json,
  '.graph.v1.SnapshotVertex': SnapshotVertex$json,
  '.graph.v1.SnapshotEdge': SnapshotEdge$json,
  '.graph.v1.SnapshotEdgeContribution': SnapshotEdgeContribution$json,
  '.graph.v1.SnapshotFooter': SnapshotFooter$json,
  '.graph.v1.SnapshotVertexCausalBarrier': SnapshotVertexCausalBarrier$json,
  '.graph.v1.SnapshotEdgeCausalBarrier': SnapshotEdgeCausalBarrier$json,
  '.graph.v1.SnapshotVertexTombstone': SnapshotVertexTombstone$json,
  '.graph.v1.SnapshotEdgeTombstone': SnapshotEdgeTombstone$json,
  '.graph.v1.SnapshotReceipt': SnapshotReceipt$json,
  '.graph.v1.SnapshotReceiptContribution': SnapshotReceiptContribution$json,
  '.graph.v1.PeerStatusRequest': PeerStatusRequest$json,
  '.graph.v1.PeerStatusResponse': PeerStatusResponse$json,
};

/// Descriptor for `LanternReplicationService`. Decode as a `google.protobuf.ServiceDescriptorProto`.
final $typed_data.Uint8List lanternReplicationServiceDescriptor = $convert.base64Decode(
    'ChlMYW50ZXJuUmVwbGljYXRpb25TZXJ2aWNlEkYKCVN1YnNjcmliZRIaLmdyYXBoLnYxLlN1Yn'
    'NjcmliZVJlcXVlc3QaGy5ncmFwaC52MS5TdWJzY3JpYmVSZXNwb25zZTABEkMKCFNuYXBzaG90'
    'EhkuZ3JhcGgudjEuU25hcHNob3RSZXF1ZXN0GhouZ3JhcGgudjEuU25hcHNob3RSZXNwb25zZT'
    'ABEkcKClBlZXJTdGF0dXMSGy5ncmFwaC52MS5QZWVyU3RhdHVzUmVxdWVzdBocLmdyYXBoLnYx'
    'LlBlZXJTdGF0dXNSZXNwb25zZQ==');
