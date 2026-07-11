// This is a generated file - do not edit.
//
// Generated from graph/v1/replication.proto.

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
    'ByZWZpeEIECgJvcA==');

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
  ],
};

/// Descriptor for `Mutation`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List mutationDescriptor = $convert.base64Decode(
    'CghNdXRhdGlvbhIQCgNzZXEYASABKARSA3NlcRIoCgNobGMYAiABKAsyFi5ncmFwaC52MS5ITE'
    'NUaW1lc3RhbXBSA2hsYxIWCgZvcmlnaW4YAyABKAxSBm9yaWdpbhIkCgJvcBgEIAEoCzIULmdy'
    'YXBoLnYxLk11dGF0aW9uT3BSAm9w');

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
    'aWdpbhpDChVGcm9tU2VxUGVyT3JpZ2luRW50cnkSEAoDa2V5GAEgASgJUgNrZXkSFAoFdmFsdW'
    'UYAiABKARSBXZhbHVlOgI4AQ==');

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
      '10': 'mutation'
    },
  ],
};

/// Descriptor for `SubscribeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List subscribeResponseDescriptor = $convert.base64Decode(
    'ChFTdWJzY3JpYmVSZXNwb25zZRIuCghtdXRhdGlvbhgBIAEoCzISLmdyYXBoLnYxLk11dGF0aW'
    '9uUghtdXRhdGlvbg==');

@$core.Deprecated('Use snapshotRequestDescriptor instead')
const SnapshotRequest$json = {
  '1': 'SnapshotRequest',
};

/// Descriptor for `SnapshotRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotRequestDescriptor =
    $convert.base64Decode('Cg9TbmFwc2hvdFJlcXVlc3Q=');

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
    '9mZkhsYxpFChdDdXRvZmZTZXFQZXJPcmlnaW5FbnRyeRIQCgNrZXkYASABKAlSA2tleRIUCgV2'
    'YWx1ZRgCIAEoBFIFdmFsdWU6AjgB');

@$core.Deprecated('Use snapshotFooterDescriptor instead')
const SnapshotFooter$json = {
  '1': 'SnapshotFooter',
  '2': [
    {'1': 'vertex_count', '3': 1, '4': 1, '5': 4, '10': 'vertexCount'},
    {'1': 'edge_count', '3': 2, '4': 1, '5': 4, '10': 'edgeCount'},
  ],
};

/// Descriptor for `SnapshotFooter`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotFooterDescriptor = $convert.base64Decode(
    'Cg5TbmFwc2hvdEZvb3RlchIhCgx2ZXJ0ZXhfY291bnQYASABKARSC3ZlcnRleENvdW50Eh0KCm'
    'VkZ2VfY291bnQYAiABKARSCWVkZ2VDb3VudA==');

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
  ],
};

/// Descriptor for `SnapshotEdgeContribution`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List snapshotEdgeContributionDescriptor = $convert.base64Decode(
    'ChhTbmFwc2hvdEVkZ2VDb250cmlidXRpb24SFgoGd2VpZ2h0GAEgASgCUgZ3ZWlnaHQSOgoKZX'
    'hwaXJhdGlvbhgCIAEoCzIaLmdvb2dsZS5wcm90b2J1Zi5UaW1lc3RhbXBSCmV4cGlyYXRpb24S'
    'HQoKY29udHJpYl9pZBgDIAEoDFIJY29udHJpYklk');

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
    'dlEjIKBmZvb3RlchgEIAEoCzIYLmdyYXBoLnYxLlNuYXBzaG90Rm9vdGVySABSBmZvb3RlckIH'
    'CgVlbnRyeQ==');

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
  ],
};

/// Descriptor for `PeerStatusResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List peerStatusResponseDescriptor = $convert.base64Decode(
    'ChJQZWVyU3RhdHVzUmVzcG9uc2USHwoLc2VsZl9vcmlnaW4YASABKAxSCnNlbGZPcmlnaW4SLw'
    'oHb3JpZ2lucxgCIAMoCzIVLmdyYXBoLnYxLk9yaWdpblN0YXRlUgdvcmlnaW5z');
