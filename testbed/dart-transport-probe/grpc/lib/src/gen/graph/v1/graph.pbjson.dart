// This is a generated file - do not edit.
//
// Generated from graph/v1/graph.proto.

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

@$core.Deprecated('Use reductionDescriptor instead')
const Reduction$json = {
  '1': 'Reduction',
  '2': [
    {'1': 'REDUCTION_UNSPECIFIED', '2': 0},
    {'1': 'REDUCTION_MINIMUM_SPANNING_TREE', '2': 1},
    {'1': 'REDUCTION_SHORTEST_PATH_TREE', '2': 2},
  ],
};

/// Descriptor for `Reduction`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List reductionDescriptor = $convert.base64Decode(
    'CglSZWR1Y3Rpb24SGQoVUkVEVUNUSU9OX1VOU1BFQ0lGSUVEEAASIwofUkVEVUNUSU9OX01JTk'
    'lNVU1fU1BBTk5JTkdfVFJFRRABEiAKHFJFRFVDVElPTl9TSE9SVEVTVF9QQVRIX1RSRUUQAg==');

@$core.Deprecated('Use objectiveDescriptor instead')
const Objective$json = {
  '1': 'Objective',
  '2': [
    {'1': 'OBJECTIVE_UNSPECIFIED', '2': 0},
    {'1': 'OBJECTIVE_MINIMIZE', '2': 1},
    {'1': 'OBJECTIVE_MAXIMIZE', '2': 2},
  ],
};

/// Descriptor for `Objective`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List objectiveDescriptor = $convert.base64Decode(
    'CglPYmplY3RpdmUSGQoVT0JKRUNUSVZFX1VOU1BFQ0lGSUVEEAASFgoST0JKRUNUSVZFX01JTk'
    'lNSVpFEAESFgoST0JKRUNUSVZFX01BWElNSVpFEAI=');

@$core.Deprecated('Use weightingDescriptor instead')
const Weighting$json = {
  '1': 'Weighting',
  '2': [
    {'1': 'WEIGHTING_UNSPECIFIED', '2': 0},
    {'1': 'WEIGHTING_RAW', '2': 1},
    {'1': 'WEIGHTING_TFIDF', '2': 2},
    {'1': 'WEIGHTING_BM25', '2': 3},
  ],
};

/// Descriptor for `Weighting`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List weightingDescriptor = $convert.base64Decode(
    'CglXZWlnaHRpbmcSGQoVV0VJR0hUSU5HX1VOU1BFQ0lGSUVEEAASEQoNV0VJR0hUSU5HX1JBVx'
    'ABEhMKD1dFSUdIVElOR19URklERhACEhIKDldFSUdIVElOR19CTTI1EAM=');

@$core.Deprecated('Use putOutcomeDescriptor instead')
const PutOutcome$json = {
  '1': 'PutOutcome',
  '2': [
    {'1': 'PUT_OUTCOME_UNSPECIFIED', '2': 0},
    {'1': 'PUT_OUTCOME_APPLIED_AND_LIVE', '2': 1},
    {'1': 'PUT_OUTCOME_EXPIRED', '2': 2},
    {'1': 'PUT_OUTCOME_CONDITION_NOT_MET', '2': 3},
    {'1': 'PUT_OUTCOME_SUPERSEDED', '2': 4},
  ],
};

/// Descriptor for `PutOutcome`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List putOutcomeDescriptor = $convert.base64Decode(
    'CgpQdXRPdXRjb21lEhsKF1BVVF9PVVRDT01FX1VOU1BFQ0lGSUVEEAASIAocUFVUX09VVENPTU'
    'VfQVBQTElFRF9BTkRfTElWRRABEhcKE1BVVF9PVVRDT01FX0VYUElSRUQQAhIhCh1QVVRfT1VU'
    'Q09NRV9DT05ESVRJT05fTk9UX01FVBADEhoKFlBVVF9PVVRDT01FX1NVUEVSU0VERUQQBA==');

@$core.Deprecated('Use scanOrderDescriptor instead')
const ScanOrder$json = {
  '1': 'ScanOrder',
  '2': [
    {'1': 'SCAN_ORDER_UNSPECIFIED', '2': 0},
    {'1': 'SCAN_ORDER_ASC', '2': 1},
    {'1': 'SCAN_ORDER_DESC', '2': 2},
  ],
};

/// Descriptor for `ScanOrder`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List scanOrderDescriptor = $convert.base64Decode(
    'CglTY2FuT3JkZXISGgoWU0NBTl9PUkRFUl9VTlNQRUNJRklFRBAAEhIKDlNDQU5fT1JERVJfQV'
    'NDEAESEwoPU0NBTl9PUkRFUl9ERVNDEAI=');

@$core.Deprecated('Use matchModeDescriptor instead')
const MatchMode$json = {
  '1': 'MatchMode',
  '2': [
    {'1': 'MATCH_MODE_UNSPECIFIED', '2': 0},
    {'1': 'MATCH_MODE_ANY', '2': 1},
    {'1': 'MATCH_MODE_ALL', '2': 2},
    {'1': 'MATCH_MODE_MIN_SHOULD', '2': 3},
  ],
};

/// Descriptor for `MatchMode`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List matchModeDescriptor = $convert.base64Decode(
    'CglNYXRjaE1vZGUSGgoWTUFUQ0hfTU9ERV9VTlNQRUNJRklFRBAAEhIKDk1BVENIX01PREVfQU'
    '5ZEAESEgoOTUFUQ0hfTU9ERV9BTEwQAhIZChVNQVRDSF9NT0RFX01JTl9TSE9VTEQQAw==');

@$core.Deprecated('Use searchProjectionDescriptor instead')
const SearchProjection$json = {
  '1': 'SearchProjection',
  '2': [
    {'1': 'SEARCH_PROJECTION_UNSPECIFIED', '2': 0},
    {'1': 'SEARCH_PROJECTION_KEY_SCORE', '2': 1},
    {'1': 'SEARCH_PROJECTION_FULL_VERTEX', '2': 2},
  ],
};

/// Descriptor for `SearchProjection`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List searchProjectionDescriptor = $convert.base64Decode(
    'ChBTZWFyY2hQcm9qZWN0aW9uEiEKHVNFQVJDSF9QUk9KRUNUSU9OX1VOU1BFQ0lGSUVEEAASHw'
    'obU0VBUkNIX1BST0pFQ1RJT05fS0VZX1NDT1JFEAESIQodU0VBUkNIX1BST0pFQ1RJT05fRlVM'
    'TF9WRVJURVgQAg==');

@$core.Deprecated('Use searchHitProjectionStatusDescriptor instead')
const SearchHitProjectionStatus$json = {
  '1': 'SearchHitProjectionStatus',
  '2': [
    {'1': 'SEARCH_HIT_PROJECTION_STATUS_UNSPECIFIED', '2': 0},
    {'1': 'SEARCH_HIT_PROJECTION_STATUS_KEY_SCORE', '2': 1},
    {'1': 'SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT', '2': 2},
    {'1': 'SEARCH_HIT_PROJECTION_STATUS_MISSING', '2': 3},
    {'1': 'SEARCH_HIT_PROJECTION_STATUS_REPLACED', '2': 4},
  ],
};

/// Descriptor for `SearchHitProjectionStatus`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List searchHitProjectionStatusDescriptor = $convert.base64Decode(
    'ChlTZWFyY2hIaXRQcm9qZWN0aW9uU3RhdHVzEiwKKFNFQVJDSF9ISVRfUFJPSkVDVElPTl9TVE'
    'FUVVNfVU5TUEVDSUZJRUQQABIqCiZTRUFSQ0hfSElUX1BST0pFQ1RJT05fU1RBVFVTX0tFWV9T'
    'Q09SRRABEikKJVNFQVJDSF9ISVRfUFJPSkVDVElPTl9TVEFUVVNfU05BUFNIT1QQAhIoCiRTRU'
    'FSQ0hfSElUX1BST0pFQ1RJT05fU1RBVFVTX01JU1NJTkcQAxIpCiVTRUFSQ0hfSElUX1BST0pF'
    'Q1RJT05fU1RBVFVTX1JFUExBQ0VEEAQ=');

@$core.Deprecated('Use searchErrorReasonDescriptor instead')
const SearchErrorReason$json = {
  '1': 'SearchErrorReason',
  '2': [
    {'1': 'SEARCH_ERROR_REASON_UNSPECIFIED', '2': 0},
    {'1': 'SEARCH_DISABLED', '2': 1},
    {'1': 'SEARCH_POSITIONS_DISABLED', '2': 2},
    {'1': 'SEARCH_WORK_BUDGET_EXHAUSTED', '2': 3},
    {'1': 'SEARCH_ADMISSION_SATURATED', '2': 4},
    {'1': 'SEARCH_INDEX_INCOMPLETE', '2': 5},
    {'1': 'SEARCH_INDEX_BUDGET_EXHAUSTED', '2': 6},
    {'1': 'SEARCH_CURSOR_STALE', '2': 7},
    {'1': 'SEARCH_CURSOR_INVALID', '2': 8},
    {'1': 'SEARCH_CONTINUATION_LIMITED', '2': 9},
  ],
};

/// Descriptor for `SearchErrorReason`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List searchErrorReasonDescriptor = $convert.base64Decode(
    'ChFTZWFyY2hFcnJvclJlYXNvbhIjCh9TRUFSQ0hfRVJST1JfUkVBU09OX1VOU1BFQ0lGSUVEEA'
    'ASEwoPU0VBUkNIX0RJU0FCTEVEEAESHQoZU0VBUkNIX1BPU0lUSU9OU19ESVNBQkxFRBACEiAK'
    'HFNFQVJDSF9XT1JLX0JVREdFVF9FWEhBVVNURUQQAxIeChpTRUFSQ0hfQURNSVNTSU9OX1NBVF'
    'VSQVRFRBAEEhsKF1NFQVJDSF9JTkRFWF9JTkNPTVBMRVRFEAUSIQodU0VBUkNIX0lOREVYX0JV'
    'REdFVF9FWEhBVVNURUQQBhIXChNTRUFSQ0hfQ1VSU09SX1NUQUxFEAcSGQoVU0VBUkNIX0NVUl'
    'NPUl9JTlZBTElEEAgSHwobU0VBUkNIX0NPTlRJTlVBVElPTl9MSU1JVEVEEAk=');

@$core.Deprecated('Use searchIndexHealthDescriptor instead')
const SearchIndexHealth$json = {
  '1': 'SearchIndexHealth',
  '2': [
    {'1': 'SEARCH_INDEX_HEALTH_UNSPECIFIED', '2': 0},
    {'1': 'SEARCH_INDEX_HEALTH_DISABLED', '2': 1},
    {'1': 'SEARCH_INDEX_HEALTH_HEALTHY', '2': 2},
    {'1': 'SEARCH_INDEX_HEALTH_INCOMPLETE', '2': 3},
  ],
};

/// Descriptor for `SearchIndexHealth`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List searchIndexHealthDescriptor = $convert.base64Decode(
    'ChFTZWFyY2hJbmRleEhlYWx0aBIjCh9TRUFSQ0hfSU5ERVhfSEVBTFRIX1VOU1BFQ0lGSUVEEA'
    'ASIAocU0VBUkNIX0lOREVYX0hFQUxUSF9ESVNBQkxFRBABEh8KG1NFQVJDSF9JTkRFWF9IRUFM'
    'VEhfSEVBTFRIWRACEiIKHlNFQVJDSF9JTkRFWF9IRUFMVEhfSU5DT01QTEVURRAD');

@$core.Deprecated('Use vertexDescriptor instead')
const Vertex$json = {
  '1': 'Vertex',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {
      '1': 'expiration',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiration'
    },
    {'1': 'float64', '3': 10, '4': 1, '5': 1, '9': 0, '10': 'float64'},
    {'1': 'float32', '3': 11, '4': 1, '5': 2, '9': 0, '10': 'float32'},
    {'1': 'int32', '3': 12, '4': 1, '5': 5, '9': 0, '10': 'int32'},
    {'1': 'int64', '3': 13, '4': 1, '5': 3, '9': 0, '10': 'int64'},
    {'1': 'uint32', '3': 14, '4': 1, '5': 13, '9': 0, '10': 'uint32'},
    {'1': 'uint64', '3': 15, '4': 1, '5': 4, '9': 0, '10': 'uint64'},
    {'1': 'bool', '3': 16, '4': 1, '5': 8, '9': 0, '10': 'bool'},
    {'1': 'string', '3': 17, '4': 1, '5': 9, '9': 0, '10': 'string'},
    {'1': 'bytes', '3': 18, '4': 1, '5': 12, '9': 0, '10': 'bytes'},
    {
      '1': 'timestamp',
      '3': 19,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '9': 0,
      '10': 'timestamp'
    },
    {
      '1': 'duration',
      '3': 20,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Duration',
      '9': 0,
      '10': 'duration'
    },
    {'1': 'nil', '3': 30, '4': 1, '5': 8, '9': 0, '10': 'nil'},
  ],
  '8': [
    {'1': 'value'},
  ],
};

/// Descriptor for `Vertex`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List vertexDescriptor = $convert.base64Decode(
    'CgZWZXJ0ZXgSEAoDa2V5GAEgASgJUgNrZXkSOgoKZXhwaXJhdGlvbhgCIAEoCzIaLmdvb2dsZS'
    '5wcm90b2J1Zi5UaW1lc3RhbXBSCmV4cGlyYXRpb24SGgoHZmxvYXQ2NBgKIAEoAUgAUgdmbG9h'
    'dDY0EhoKB2Zsb2F0MzIYCyABKAJIAFIHZmxvYXQzMhIWCgVpbnQzMhgMIAEoBUgAUgVpbnQzMh'
    'IWCgVpbnQ2NBgNIAEoA0gAUgVpbnQ2NBIYCgZ1aW50MzIYDiABKA1IAFIGdWludDMyEhgKBnVp'
    'bnQ2NBgPIAEoBEgAUgZ1aW50NjQSFAoEYm9vbBgQIAEoCEgAUgRib29sEhgKBnN0cmluZxgRIA'
    'EoCUgAUgZzdHJpbmcSFgoFYnl0ZXMYEiABKAxIAFIFYnl0ZXMSOgoJdGltZXN0YW1wGBMgASgL'
    'MhouZ29vZ2xlLnByb3RvYnVmLlRpbWVzdGFtcEgAUgl0aW1lc3RhbXASNwoIZHVyYXRpb24YFC'
    'ABKAsyGS5nb29nbGUucHJvdG9idWYuRHVyYXRpb25IAFIIZHVyYXRpb24SEgoDbmlsGB4gASgI'
    'SABSA25pbEIHCgV2YWx1ZQ==');

@$core.Deprecated('Use edgeDescriptor instead')
const Edge$json = {
  '1': 'Edge',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
    {'1': 'weight', '3': 3, '4': 1, '5': 2, '10': 'weight'},
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

/// Descriptor for `Edge`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List edgeDescriptor = $convert.base64Decode(
    'CgRFZGdlEhIKBHRhaWwYASABKAlSBHRhaWwSEgoEaGVhZBgCIAEoCVIEaGVhZBIWCgZ3ZWlnaH'
    'QYAyABKAJSBndlaWdodBI6CgpleHBpcmF0aW9uGAQgASgLMhouZ29vZ2xlLnByb3RvYnVmLlRp'
    'bWVzdGFtcFIKZXhwaXJhdGlvbg==');

@$core.Deprecated('Use graphDescriptor instead')
const Graph$json = {
  '1': 'Graph',
  '2': [
    {
      '1': 'vertices',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertices'
    },
    {
      '1': 'edges',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Edge',
      '10': 'edges'
    },
  ],
};

/// Descriptor for `Graph`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List graphDescriptor = $convert.base64Decode(
    'CgVHcmFwaBIsCgh2ZXJ0aWNlcxgBIAMoCzIQLmdyYXBoLnYxLlZlcnRleFIIdmVydGljZXMSJA'
    'oFZWRnZXMYAiADKAsyDi5ncmFwaC52MS5FZGdlUgVlZGdlcw==');

@$core.Deprecated('Use illuminateRequestDescriptor instead')
const IlluminateRequest$json = {
  '1': 'IlluminateRequest',
  '2': [
    {'1': 'seed', '3': 1, '4': 1, '5': 9, '10': 'seed'},
    {
      '1': 'weighting',
      '3': 8,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.Weighting',
      '10': 'weighting'
    },
    {'1': 'vertex_prefix', '3': 9, '4': 1, '5': 9, '10': 'vertexPrefix'},
    {
      '1': 'bfs',
      '3': 12,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.BfsParams',
      '9': 0,
      '10': 'bfs'
    },
    {
      '1': 'ppr',
      '3': 13,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.PprParams',
      '9': 0,
      '10': 'ppr'
    },
    {
      '1': 'community',
      '3': 14,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.LocalCommunityParams',
      '9': 0,
      '10': 'community'
    },
  ],
  '8': [
    {'1': 'params'},
  ],
  '9': [
    {'1': 2, '2': 3},
    {'1': 3, '2': 4},
    {'1': 4, '2': 5},
    {'1': 5, '2': 6},
    {'1': 6, '2': 7},
    {'1': 7, '2': 8},
    {'1': 10, '2': 11},
    {'1': 11, '2': 12},
  ],
  '10': [
    'step',
    'k',
    'tfidf',
    'optimization',
    'algorithm',
    'objective',
    'restart_prob',
    'epsilon'
  ],
};

/// Descriptor for `IlluminateRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List illuminateRequestDescriptor = $convert.base64Decode(
    'ChFJbGx1bWluYXRlUmVxdWVzdBISCgRzZWVkGAEgASgJUgRzZWVkEjEKCXdlaWdodGluZxgIIA'
    'EoDjITLmdyYXBoLnYxLldlaWdodGluZ1IJd2VpZ2h0aW5nEiMKDXZlcnRleF9wcmVmaXgYCSAB'
    'KAlSDHZlcnRleFByZWZpeBInCgNiZnMYDCABKAsyEy5ncmFwaC52MS5CZnNQYXJhbXNIAFIDYm'
    'ZzEicKA3BwchgNIAEoCzITLmdyYXBoLnYxLlBwclBhcmFtc0gAUgNwcHISPgoJY29tbXVuaXR5'
    'GA4gASgLMh4uZ3JhcGgudjEuTG9jYWxDb21tdW5pdHlQYXJhbXNIAFIJY29tbXVuaXR5QggKBn'
    'BhcmFtc0oECAIQA0oECAMQBEoECAQQBUoECAUQBkoECAYQB0oECAcQCEoECAoQC0oECAsQDFIE'
    'c3RlcFIBa1IFdGZpZGZSDG9wdGltaXphdGlvblIJYWxnb3JpdGhtUglvYmplY3RpdmVSDHJlc3'
    'RhcnRfcHJvYlIHZXBzaWxvbg==');

@$core.Deprecated('Use bfsParamsDescriptor instead')
const BfsParams$json = {
  '1': 'BfsParams',
  '2': [
    {'1': 'step', '3': 1, '4': 1, '5': 13, '10': 'step'},
    {'1': 'fan_out', '3': 2, '4': 1, '5': 13, '10': 'fanOut'},
    {
      '1': 'objective',
      '3': 3,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.Objective',
      '10': 'objective'
    },
    {
      '1': 'reduction',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.Reduction',
      '10': 'reduction'
    },
  ],
};

/// Descriptor for `BfsParams`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List bfsParamsDescriptor = $convert.base64Decode(
    'CglCZnNQYXJhbXMSEgoEc3RlcBgBIAEoDVIEc3RlcBIXCgdmYW5fb3V0GAIgASgNUgZmYW5PdX'
    'QSMQoJb2JqZWN0aXZlGAMgASgOMhMuZ3JhcGgudjEuT2JqZWN0aXZlUglvYmplY3RpdmUSMQoJ'
    'cmVkdWN0aW9uGAQgASgOMhMuZ3JhcGgudjEuUmVkdWN0aW9uUglyZWR1Y3Rpb24=');

@$core.Deprecated('Use localCommunityParamsDescriptor instead')
const LocalCommunityParams$json = {
  '1': 'LocalCommunityParams',
  '2': [
    {'1': 'max_size', '3': 1, '4': 1, '5': 13, '10': 'maxSize'},
    {'1': 'restart_prob', '3': 2, '4': 1, '5': 2, '10': 'restartProb'},
    {'1': 'epsilon', '3': 3, '4': 1, '5': 2, '10': 'epsilon'},
    {
      '1': 'reduction',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.Reduction',
      '10': 'reduction'
    },
    {
      '1': 'objective',
      '3': 5,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.Objective',
      '10': 'objective'
    },
  ],
};

/// Descriptor for `LocalCommunityParams`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List localCommunityParamsDescriptor = $convert.base64Decode(
    'ChRMb2NhbENvbW11bml0eVBhcmFtcxIZCghtYXhfc2l6ZRgBIAEoDVIHbWF4U2l6ZRIhCgxyZX'
    'N0YXJ0X3Byb2IYAiABKAJSC3Jlc3RhcnRQcm9iEhgKB2Vwc2lsb24YAyABKAJSB2Vwc2lsb24S'
    'MQoJcmVkdWN0aW9uGAQgASgOMhMuZ3JhcGgudjEuUmVkdWN0aW9uUglyZWR1Y3Rpb24SMQoJb2'
    'JqZWN0aXZlGAUgASgOMhMuZ3JhcGgudjEuT2JqZWN0aXZlUglvYmplY3RpdmU=');

@$core.Deprecated('Use pprParamsDescriptor instead')
const PprParams$json = {
  '1': 'PprParams',
  '2': [
    {'1': 'top_n', '3': 1, '4': 1, '5': 13, '10': 'topN'},
    {'1': 'restart_prob', '3': 2, '4': 1, '5': 2, '10': 'restartProb'},
    {'1': 'epsilon', '3': 3, '4': 1, '5': 2, '10': 'epsilon'},
  ],
};

/// Descriptor for `PprParams`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List pprParamsDescriptor = $convert.base64Decode(
    'CglQcHJQYXJhbXMSEwoFdG9wX24YASABKA1SBHRvcE4SIQoMcmVzdGFydF9wcm9iGAIgASgCUg'
    'tyZXN0YXJ0UHJvYhIYCgdlcHNpbG9uGAMgASgCUgdlcHNpbG9u');

@$core.Deprecated('Use illuminateResponseDescriptor instead')
const IlluminateResponse$json = {
  '1': 'IlluminateResponse',
  '2': [
    {
      '1': 'graph',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Graph',
      '10': 'graph'
    },
  ],
};

/// Descriptor for `IlluminateResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List illuminateResponseDescriptor = $convert.base64Decode(
    'ChJJbGx1bWluYXRlUmVzcG9uc2USJQoFZ3JhcGgYASABKAsyDy5ncmFwaC52MS5HcmFwaFIFZ3'
    'JhcGg=');

@$core.Deprecated('Use getVertexRequestDescriptor instead')
const GetVertexRequest$json = {
  '1': 'GetVertexRequest',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
  ],
};

/// Descriptor for `GetVertexRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getVertexRequestDescriptor =
    $convert.base64Decode('ChBHZXRWZXJ0ZXhSZXF1ZXN0EhAKA2tleRgBIAEoCVIDa2V5');

@$core.Deprecated('Use getVertexResponseDescriptor instead')
const GetVertexResponse$json = {
  '1': 'GetVertexResponse',
  '2': [
    {
      '1': 'vertex',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertex'
    },
  ],
};

/// Descriptor for `GetVertexResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getVertexResponseDescriptor = $convert.base64Decode(
    'ChFHZXRWZXJ0ZXhSZXNwb25zZRIoCgZ2ZXJ0ZXgYASABKAsyEC5ncmFwaC52MS5WZXJ0ZXhSBn'
    'ZlcnRleA==');

@$core.Deprecated('Use getVerticesRequestDescriptor instead')
const GetVerticesRequest$json = {
  '1': 'GetVerticesRequest',
  '2': [
    {'1': 'keys', '3': 1, '4': 3, '5': 9, '10': 'keys'},
  ],
};

/// Descriptor for `GetVerticesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getVerticesRequestDescriptor = $convert
    .base64Decode('ChJHZXRWZXJ0aWNlc1JlcXVlc3QSEgoEa2V5cxgBIAMoCVIEa2V5cw==');

@$core.Deprecated('Use getVerticesResponseDescriptor instead')
const GetVerticesResponse$json = {
  '1': 'GetVerticesResponse',
  '2': [
    {
      '1': 'vertices',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertices'
    },
    {'1': 'missing', '3': 2, '4': 3, '5': 9, '10': 'missing'},
  ],
};

/// Descriptor for `GetVerticesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getVerticesResponseDescriptor = $convert.base64Decode(
    'ChNHZXRWZXJ0aWNlc1Jlc3BvbnNlEiwKCHZlcnRpY2VzGAEgAygLMhAuZ3JhcGgudjEuVmVydG'
    'V4Ugh2ZXJ0aWNlcxIYCgdtaXNzaW5nGAIgAygJUgdtaXNzaW5n');

@$core.Deprecated('Use putVertexRequestDescriptor instead')
const PutVertexRequest$json = {
  '1': 'PutVertexRequest',
  '2': [
    {
      '1': 'vertex',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertex'
    },
    {'1': 'if_absent', '3': 2, '4': 1, '5': 8, '10': 'ifAbsent'},
  ],
};

/// Descriptor for `PutVertexRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putVertexRequestDescriptor = $convert.base64Decode(
    'ChBQdXRWZXJ0ZXhSZXF1ZXN0EigKBnZlcnRleBgBIAEoCzIQLmdyYXBoLnYxLlZlcnRleFIGdm'
    'VydGV4EhsKCWlmX2Fic2VudBgCIAEoCFIIaWZBYnNlbnQ=');

@$core.Deprecated('Use putVertexResponseDescriptor instead')
const PutVertexResponse$json = {
  '1': 'PutVertexResponse',
  '2': [
    {
      '1': 'outcome',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.PutOutcome',
      '10': 'outcome'
    },
  ],
};

/// Descriptor for `PutVertexResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putVertexResponseDescriptor = $convert.base64Decode(
    'ChFQdXRWZXJ0ZXhSZXNwb25zZRIuCgdvdXRjb21lGAEgASgOMhQuZ3JhcGgudjEuUHV0T3V0Y2'
    '9tZVIHb3V0Y29tZQ==');

@$core.Deprecated('Use putVerticesRequestDescriptor instead')
const PutVerticesRequest$json = {
  '1': 'PutVerticesRequest',
  '2': [
    {
      '1': 'vertices',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertices'
    },
    {'1': 'if_absent', '3': 2, '4': 1, '5': 8, '10': 'ifAbsent'},
  ],
};

/// Descriptor for `PutVerticesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putVerticesRequestDescriptor = $convert.base64Decode(
    'ChJQdXRWZXJ0aWNlc1JlcXVlc3QSLAoIdmVydGljZXMYASADKAsyEC5ncmFwaC52MS5WZXJ0ZX'
    'hSCHZlcnRpY2VzEhsKCWlmX2Fic2VudBgCIAEoCFIIaWZBYnNlbnQ=');

@$core.Deprecated('Use putVerticesResponseDescriptor instead')
const PutVerticesResponse$json = {
  '1': 'PutVerticesResponse',
  '2': [
    {
      '1': 'outcomes',
      '3': 1,
      '4': 3,
      '5': 14,
      '6': '.graph.v1.PutOutcome',
      '10': 'outcomes'
    },
  ],
};

/// Descriptor for `PutVerticesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putVerticesResponseDescriptor = $convert.base64Decode(
    'ChNQdXRWZXJ0aWNlc1Jlc3BvbnNlEjAKCG91dGNvbWVzGAEgAygOMhQuZ3JhcGgudjEuUHV0T3'
    'V0Y29tZVIIb3V0Y29tZXM=');

@$core.Deprecated('Use deleteVertexRequestDescriptor instead')
const DeleteVertexRequest$json = {
  '1': 'DeleteVertexRequest',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
  ],
};

/// Descriptor for `DeleteVertexRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteVertexRequestDescriptor = $convert
    .base64Decode('ChNEZWxldGVWZXJ0ZXhSZXF1ZXN0EhAKA2tleRgBIAEoCVIDa2V5');

@$core.Deprecated('Use deleteVertexResponseDescriptor instead')
const DeleteVertexResponse$json = {
  '1': 'DeleteVertexResponse',
  '2': [
    {'1': 'existed', '3': 1, '4': 1, '5': 8, '10': 'existed'},
  ],
};

/// Descriptor for `DeleteVertexResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteVertexResponseDescriptor =
    $convert.base64Decode(
        'ChREZWxldGVWZXJ0ZXhSZXNwb25zZRIYCgdleGlzdGVkGAEgASgIUgdleGlzdGVk');

@$core.Deprecated('Use deleteVerticesRequestDescriptor instead')
const DeleteVerticesRequest$json = {
  '1': 'DeleteVerticesRequest',
  '2': [
    {'1': 'keys', '3': 1, '4': 3, '5': 9, '10': 'keys'},
  ],
};

/// Descriptor for `DeleteVerticesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteVerticesRequestDescriptor =
    $convert.base64Decode(
        'ChVEZWxldGVWZXJ0aWNlc1JlcXVlc3QSEgoEa2V5cxgBIAMoCVIEa2V5cw==');

@$core.Deprecated('Use deleteVerticesResponseDescriptor instead')
const DeleteVerticesResponse$json = {
  '1': 'DeleteVerticesResponse',
  '2': [
    {'1': 'deleted', '3': 1, '4': 1, '5': 5, '10': 'deleted'},
  ],
};

/// Descriptor for `DeleteVerticesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteVerticesResponseDescriptor =
    $convert.base64Decode(
        'ChZEZWxldGVWZXJ0aWNlc1Jlc3BvbnNlEhgKB2RlbGV0ZWQYASABKAVSB2RlbGV0ZWQ=');

@$core.Deprecated('Use scanVerticesRequestDescriptor instead')
const ScanVerticesRequest$json = {
  '1': 'ScanVerticesRequest',
  '2': [
    {'1': 'prefix', '3': 1, '4': 1, '5': 9, '10': 'prefix'},
    {'1': 'limit', '3': 2, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'cursor', '3': 3, '4': 1, '5': 12, '10': 'cursor'},
    {
      '1': 'order',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.ScanOrder',
      '10': 'order'
    },
  ],
};

/// Descriptor for `ScanVerticesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List scanVerticesRequestDescriptor = $convert.base64Decode(
    'ChNTY2FuVmVydGljZXNSZXF1ZXN0EhYKBnByZWZpeBgBIAEoCVIGcHJlZml4EhQKBWxpbWl0GA'
    'IgASgNUgVsaW1pdBIWCgZjdXJzb3IYAyABKAxSBmN1cnNvchIpCgVvcmRlchgEIAEoDjITLmdy'
    'YXBoLnYxLlNjYW5PcmRlclIFb3JkZXI=');

@$core.Deprecated('Use scanVerticesResponseDescriptor instead')
const ScanVerticesResponse$json = {
  '1': 'ScanVerticesResponse',
  '2': [
    {
      '1': 'vertices',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertices'
    },
    {'1': 'next_cursor', '3': 2, '4': 1, '5': 12, '10': 'nextCursor'},
  ],
};

/// Descriptor for `ScanVerticesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List scanVerticesResponseDescriptor = $convert.base64Decode(
    'ChRTY2FuVmVydGljZXNSZXNwb25zZRIsCgh2ZXJ0aWNlcxgBIAMoCzIQLmdyYXBoLnYxLlZlcn'
    'RleFIIdmVydGljZXMSHwoLbmV4dF9jdXJzb3IYAiABKAxSCm5leHRDdXJzb3I=');

@$core.Deprecated('Use scanVertexKeysRequestDescriptor instead')
const ScanVertexKeysRequest$json = {
  '1': 'ScanVertexKeysRequest',
  '2': [
    {'1': 'prefix', '3': 1, '4': 1, '5': 9, '10': 'prefix'},
    {'1': 'limit', '3': 2, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'cursor', '3': 3, '4': 1, '5': 12, '10': 'cursor'},
    {
      '1': 'order',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.ScanOrder',
      '10': 'order'
    },
  ],
};

/// Descriptor for `ScanVertexKeysRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List scanVertexKeysRequestDescriptor = $convert.base64Decode(
    'ChVTY2FuVmVydGV4S2V5c1JlcXVlc3QSFgoGcHJlZml4GAEgASgJUgZwcmVmaXgSFAoFbGltaX'
    'QYAiABKA1SBWxpbWl0EhYKBmN1cnNvchgDIAEoDFIGY3Vyc29yEikKBW9yZGVyGAQgASgOMhMu'
    'Z3JhcGgudjEuU2Nhbk9yZGVyUgVvcmRlcg==');

@$core.Deprecated('Use scanVertexKeysResponseDescriptor instead')
const ScanVertexKeysResponse$json = {
  '1': 'ScanVertexKeysResponse',
  '2': [
    {'1': 'keys', '3': 1, '4': 3, '5': 9, '10': 'keys'},
    {'1': 'next_cursor', '3': 2, '4': 1, '5': 12, '10': 'nextCursor'},
  ],
};

/// Descriptor for `ScanVertexKeysResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List scanVertexKeysResponseDescriptor =
    $convert.base64Decode(
        'ChZTY2FuVmVydGV4S2V5c1Jlc3BvbnNlEhIKBGtleXMYASADKAlSBGtleXMSHwoLbmV4dF9jdX'
        'Jzb3IYAiABKAxSCm5leHRDdXJzb3I=');

@$core.Deprecated('Use searchOptionsDescriptor instead')
const SearchOptions$json = {
  '1': 'SearchOptions',
  '2': [
    {
      '1': 'match_mode',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.MatchMode',
      '10': 'matchMode'
    },
    {'1': 'min_should_match', '3': 2, '4': 1, '5': 13, '10': 'minShouldMatch'},
    {'1': 'phrase', '3': 3, '4': 1, '5': 8, '10': 'phrase'},
    {'1': 'fuzziness', '3': 4, '4': 1, '5': 13, '10': 'fuzziness'},
    {'1': 'prefix_terms', '3': 5, '4': 1, '5': 8, '10': 'prefixTerms'},
  ],
};

/// Descriptor for `SearchOptions`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List searchOptionsDescriptor = $convert.base64Decode(
    'Cg1TZWFyY2hPcHRpb25zEjIKCm1hdGNoX21vZGUYASABKA4yEy5ncmFwaC52MS5NYXRjaE1vZG'
    'VSCW1hdGNoTW9kZRIoChBtaW5fc2hvdWxkX21hdGNoGAIgASgNUg5taW5TaG91bGRNYXRjaBIW'
    'CgZwaHJhc2UYAyABKAhSBnBocmFzZRIcCglmdXp6aW5lc3MYBCABKA1SCWZ1enppbmVzcxIhCg'
    'xwcmVmaXhfdGVybXMYBSABKAhSC3ByZWZpeFRlcm1z');

@$core.Deprecated('Use searchVerticesRequestDescriptor instead')
const SearchVerticesRequest$json = {
  '1': 'SearchVerticesRequest',
  '2': [
    {'1': 'query', '3': 1, '4': 1, '5': 9, '10': 'query'},
    {'1': 'limit', '3': 2, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'prefix', '3': 3, '4': 1, '5': 9, '10': 'prefix'},
    {
      '1': 'options',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SearchOptions',
      '10': 'options'
    },
    {'1': 'cursor', '3': 5, '4': 1, '5': 12, '10': 'cursor'},
    {
      '1': 'projection',
      '3': 6,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SearchProjection',
      '10': 'projection'
    },
  ],
};

/// Descriptor for `SearchVerticesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List searchVerticesRequestDescriptor = $convert.base64Decode(
    'ChVTZWFyY2hWZXJ0aWNlc1JlcXVlc3QSFAoFcXVlcnkYASABKAlSBXF1ZXJ5EhQKBWxpbWl0GA'
    'IgASgNUgVsaW1pdBIWCgZwcmVmaXgYAyABKAlSBnByZWZpeBIxCgdvcHRpb25zGAQgASgLMhcu'
    'Z3JhcGgudjEuU2VhcmNoT3B0aW9uc1IHb3B0aW9ucxIWCgZjdXJzb3IYBSABKAxSBmN1cnNvch'
    'I6Cgpwcm9qZWN0aW9uGAYgASgOMhouZ3JhcGgudjEuU2VhcmNoUHJvamVjdGlvblIKcHJvamVj'
    'dGlvbg==');

@$core.Deprecated('Use searchVerticesResponseDescriptor instead')
const SearchVerticesResponse$json = {
  '1': 'SearchVerticesResponse',
  '2': [
    {
      '1': 'hits',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SearchHit',
      '10': 'hits'
    },
    {'1': 'next_cursor', '3': 2, '4': 1, '5': 12, '10': 'nextCursor'},
    {'1': 'effective_limit', '3': 3, '4': 1, '5': 13, '10': 'effectiveLimit'},
    {'1': 'truncated', '3': 4, '4': 1, '5': 8, '10': 'truncated'},
    {
      '1': 'continuation_limited',
      '3': 5,
      '4': 1,
      '5': 8,
      '10': 'continuationLimited'
    },
  ],
};

/// Descriptor for `SearchVerticesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List searchVerticesResponseDescriptor = $convert.base64Decode(
    'ChZTZWFyY2hWZXJ0aWNlc1Jlc3BvbnNlEicKBGhpdHMYASADKAsyEy5ncmFwaC52MS5TZWFyY2'
    'hIaXRSBGhpdHMSHwoLbmV4dF9jdXJzb3IYAiABKAxSCm5leHRDdXJzb3ISJwoPZWZmZWN0aXZl'
    'X2xpbWl0GAMgASgNUg5lZmZlY3RpdmVMaW1pdBIcCgl0cnVuY2F0ZWQYBCABKAhSCXRydW5jYX'
    'RlZBIxChRjb250aW51YXRpb25fbGltaXRlZBgFIAEoCFITY29udGludWF0aW9uTGltaXRlZA==');

@$core.Deprecated('Use searchHitDescriptor instead')
const SearchHit$json = {
  '1': 'SearchHit',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {'1': 'score', '3': 2, '4': 1, '5': 1, '10': 'score'},
    {
      '1': 'vertex',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '10': 'vertex'
    },
    {
      '1': 'projection_status',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SearchHitProjectionStatus',
      '10': 'projectionStatus'
    },
  ],
};

/// Descriptor for `SearchHit`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List searchHitDescriptor = $convert.base64Decode(
    'CglTZWFyY2hIaXQSEAoDa2V5GAEgASgJUgNrZXkSFAoFc2NvcmUYAiABKAFSBXNjb3JlEigKBn'
    'ZlcnRleBgDIAEoCzIQLmdyYXBoLnYxLlZlcnRleFIGdmVydGV4ElAKEXByb2plY3Rpb25fc3Rh'
    'dHVzGAQgASgOMiMuZ3JhcGgudjEuU2VhcmNoSGl0UHJvamVjdGlvblN0YXR1c1IQcHJvamVjdG'
    'lvblN0YXR1cw==');

@$core.Deprecated('Use countVerticesByPrefixRequestDescriptor instead')
const CountVerticesByPrefixRequest$json = {
  '1': 'CountVerticesByPrefixRequest',
  '2': [
    {'1': 'prefix', '3': 1, '4': 1, '5': 9, '10': 'prefix'},
  ],
};

/// Descriptor for `CountVerticesByPrefixRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List countVerticesByPrefixRequestDescriptor =
    $convert.base64Decode(
        'ChxDb3VudFZlcnRpY2VzQnlQcmVmaXhSZXF1ZXN0EhYKBnByZWZpeBgBIAEoCVIGcHJlZml4');

@$core.Deprecated('Use countVerticesByPrefixResponseDescriptor instead')
const CountVerticesByPrefixResponse$json = {
  '1': 'CountVerticesByPrefixResponse',
  '2': [
    {'1': 'count', '3': 1, '4': 1, '5': 4, '10': 'count'},
  ],
};

/// Descriptor for `CountVerticesByPrefixResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List countVerticesByPrefixResponseDescriptor =
    $convert.base64Decode(
        'Ch1Db3VudFZlcnRpY2VzQnlQcmVmaXhSZXNwb25zZRIUCgVjb3VudBgBIAEoBFIFY291bnQ=');

@$core.Deprecated('Use deleteVerticesByPrefixRequestDescriptor instead')
const DeleteVerticesByPrefixRequest$json = {
  '1': 'DeleteVerticesByPrefixRequest',
  '2': [
    {'1': 'prefix', '3': 1, '4': 1, '5': 9, '10': 'prefix'},
    {'1': 'limit', '3': 2, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'dry_run', '3': 3, '4': 1, '5': 8, '10': 'dryRun'},
  ],
};

/// Descriptor for `DeleteVerticesByPrefixRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteVerticesByPrefixRequestDescriptor =
    $convert.base64Decode(
        'Ch1EZWxldGVWZXJ0aWNlc0J5UHJlZml4UmVxdWVzdBIWCgZwcmVmaXgYASABKAlSBnByZWZpeB'
        'IUCgVsaW1pdBgCIAEoDVIFbGltaXQSFwoHZHJ5X3J1bhgDIAEoCFIGZHJ5UnVu');

@$core.Deprecated('Use deleteVerticesByPrefixResponseDescriptor instead')
const DeleteVerticesByPrefixResponse$json = {
  '1': 'DeleteVerticesByPrefixResponse',
  '2': [
    {'1': 'deleted', '3': 1, '4': 1, '5': 4, '10': 'deleted'},
  ],
};

/// Descriptor for `DeleteVerticesByPrefixResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteVerticesByPrefixResponseDescriptor =
    $convert.base64Decode(
        'Ch5EZWxldGVWZXJ0aWNlc0J5UHJlZml4UmVzcG9uc2USGAoHZGVsZXRlZBgBIAEoBFIHZGVsZX'
        'RlZA==');

@$core.Deprecated('Use topVerticesByDegreeRequestDescriptor instead')
const TopVerticesByDegreeRequest$json = {
  '1': 'TopVerticesByDegreeRequest',
  '2': [
    {'1': 'prefix', '3': 1, '4': 1, '5': 9, '10': 'prefix'},
    {'1': 'k', '3': 2, '4': 1, '5': 13, '10': 'k'},
    {
      '1': 'direction',
      '3': 3,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.TopVerticesByDegreeRequest.Direction',
      '10': 'direction'
    },
    {'1': 'weighted', '3': 4, '4': 1, '5': 8, '10': 'weighted'},
  ],
  '4': [TopVerticesByDegreeRequest_Direction$json],
};

@$core.Deprecated('Use topVerticesByDegreeRequestDescriptor instead')
const TopVerticesByDegreeRequest_Direction$json = {
  '1': 'Direction',
  '2': [
    {'1': 'DIRECTION_UNSPECIFIED', '2': 0},
    {'1': 'DIRECTION_OUT', '2': 1},
    {'1': 'DIRECTION_IN', '2': 2},
    {'1': 'DIRECTION_BOTH', '2': 3},
  ],
};

/// Descriptor for `TopVerticesByDegreeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List topVerticesByDegreeRequestDescriptor = $convert.base64Decode(
    'ChpUb3BWZXJ0aWNlc0J5RGVncmVlUmVxdWVzdBIWCgZwcmVmaXgYASABKAlSBnByZWZpeBIMCg'
    'FrGAIgASgNUgFrEkwKCWRpcmVjdGlvbhgDIAEoDjIuLmdyYXBoLnYxLlRvcFZlcnRpY2VzQnlE'
    'ZWdyZWVSZXF1ZXN0LkRpcmVjdGlvblIJZGlyZWN0aW9uEhoKCHdlaWdodGVkGAQgASgIUgh3ZW'
    'lnaHRlZCJfCglEaXJlY3Rpb24SGQoVRElSRUNUSU9OX1VOU1BFQ0lGSUVEEAASEQoNRElSRUNU'
    'SU9OX09VVBABEhAKDERJUkVDVElPTl9JThACEhIKDkRJUkVDVElPTl9CT1RIEAM=');

@$core.Deprecated('Use topVerticesByDegreeResponseDescriptor instead')
const TopVerticesByDegreeResponse$json = {
  '1': 'TopVerticesByDegreeResponse',
  '2': [
    {
      '1': 'entries',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.TopVerticesByDegreeResponse.Entry',
      '10': 'entries'
    },
  ],
  '3': [TopVerticesByDegreeResponse_Entry$json],
};

@$core.Deprecated('Use topVerticesByDegreeResponseDescriptor instead')
const TopVerticesByDegreeResponse_Entry$json = {
  '1': 'Entry',
  '2': [
    {'1': 'key', '3': 1, '4': 1, '5': 9, '10': 'key'},
    {'1': 'degree', '3': 2, '4': 1, '5': 4, '10': 'degree'},
    {'1': 'weighted_degree', '3': 3, '4': 1, '5': 1, '10': 'weightedDegree'},
  ],
};

/// Descriptor for `TopVerticesByDegreeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List topVerticesByDegreeResponseDescriptor = $convert.base64Decode(
    'ChtUb3BWZXJ0aWNlc0J5RGVncmVlUmVzcG9uc2USRQoHZW50cmllcxgBIAMoCzIrLmdyYXBoLn'
    'YxLlRvcFZlcnRpY2VzQnlEZWdyZWVSZXNwb25zZS5FbnRyeVIHZW50cmllcxpaCgVFbnRyeRIQ'
    'CgNrZXkYASABKAlSA2tleRIWCgZkZWdyZWUYAiABKARSBmRlZ3JlZRInCg93ZWlnaHRlZF9kZW'
    'dyZWUYAyABKAFSDndlaWdodGVkRGVncmVl');

@$core.Deprecated('Use getEdgeRequestDescriptor instead')
const GetEdgeRequest$json = {
  '1': 'GetEdgeRequest',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
  ],
};

/// Descriptor for `GetEdgeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getEdgeRequestDescriptor = $convert.base64Decode(
    'Cg5HZXRFZGdlUmVxdWVzdBISCgR0YWlsGAEgASgJUgR0YWlsEhIKBGhlYWQYAiABKAlSBGhlYW'
    'Q=');

@$core.Deprecated('Use getEdgeResponseDescriptor instead')
const GetEdgeResponse$json = {
  '1': 'GetEdgeResponse',
  '2': [
    {'1': 'edge', '3': 1, '4': 1, '5': 11, '6': '.graph.v1.Edge', '10': 'edge'},
  ],
};

/// Descriptor for `GetEdgeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getEdgeResponseDescriptor = $convert.base64Decode(
    'Cg9HZXRFZGdlUmVzcG9uc2USIgoEZWRnZRgBIAEoCzIOLmdyYXBoLnYxLkVkZ2VSBGVkZ2U=');

@$core.Deprecated('Use getEdgesRequestDescriptor instead')
const GetEdgesRequest$json = {
  '1': 'GetEdgesRequest',
  '2': [
    {
      '1': 'edges',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.EdgeKey',
      '10': 'edges'
    },
  ],
};

/// Descriptor for `GetEdgesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getEdgesRequestDescriptor = $convert.base64Decode(
    'Cg9HZXRFZGdlc1JlcXVlc3QSJwoFZWRnZXMYASADKAsyES5ncmFwaC52MS5FZGdlS2V5UgVlZG'
    'dlcw==');

@$core.Deprecated('Use getEdgesResponseDescriptor instead')
const GetEdgesResponse$json = {
  '1': 'GetEdgesResponse',
  '2': [
    {
      '1': 'edges',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Edge',
      '10': 'edges'
    },
    {
      '1': 'missing',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.EdgeKey',
      '10': 'missing'
    },
  ],
};

/// Descriptor for `GetEdgesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getEdgesResponseDescriptor = $convert.base64Decode(
    'ChBHZXRFZGdlc1Jlc3BvbnNlEiQKBWVkZ2VzGAEgAygLMg4uZ3JhcGgudjEuRWRnZVIFZWRnZX'
    'MSKwoHbWlzc2luZxgCIAMoCzIRLmdyYXBoLnYxLkVkZ2VLZXlSB21pc3Npbmc=');

@$core.Deprecated('Use deleteEdgeRequestDescriptor instead')
const DeleteEdgeRequest$json = {
  '1': 'DeleteEdgeRequest',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
  ],
};

/// Descriptor for `DeleteEdgeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteEdgeRequestDescriptor = $convert.base64Decode(
    'ChFEZWxldGVFZGdlUmVxdWVzdBISCgR0YWlsGAEgASgJUgR0YWlsEhIKBGhlYWQYAiABKAlSBG'
    'hlYWQ=');

@$core.Deprecated('Use deleteEdgeResponseDescriptor instead')
const DeleteEdgeResponse$json = {
  '1': 'DeleteEdgeResponse',
  '2': [
    {'1': 'existed', '3': 1, '4': 1, '5': 8, '10': 'existed'},
  ],
};

/// Descriptor for `DeleteEdgeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteEdgeResponseDescriptor =
    $convert.base64Decode(
        'ChJEZWxldGVFZGdlUmVzcG9uc2USGAoHZXhpc3RlZBgBIAEoCFIHZXhpc3RlZA==');

@$core.Deprecated('Use edgeKeyDescriptor instead')
const EdgeKey$json = {
  '1': 'EdgeKey',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
  ],
};

/// Descriptor for `EdgeKey`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List edgeKeyDescriptor = $convert.base64Decode(
    'CgdFZGdlS2V5EhIKBHRhaWwYASABKAlSBHRhaWwSEgoEaGVhZBgCIAEoCVIEaGVhZA==');

@$core.Deprecated('Use scanEdgesRequestDescriptor instead')
const ScanEdgesRequest$json = {
  '1': 'ScanEdgesRequest',
  '2': [
    {'1': 'tail_prefix', '3': 1, '4': 1, '5': 9, '10': 'tailPrefix'},
    {'1': 'head_prefix', '3': 2, '4': 1, '5': 9, '10': 'headPrefix'},
    {'1': 'limit', '3': 3, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'cursor', '3': 4, '4': 1, '5': 12, '10': 'cursor'},
  ],
};

/// Descriptor for `ScanEdgesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List scanEdgesRequestDescriptor = $convert.base64Decode(
    'ChBTY2FuRWRnZXNSZXF1ZXN0Eh8KC3RhaWxfcHJlZml4GAEgASgJUgp0YWlsUHJlZml4Eh8KC2'
    'hlYWRfcHJlZml4GAIgASgJUgpoZWFkUHJlZml4EhQKBWxpbWl0GAMgASgNUgVsaW1pdBIWCgZj'
    'dXJzb3IYBCABKAxSBmN1cnNvcg==');

@$core.Deprecated('Use scanEdgesResponseDescriptor instead')
const ScanEdgesResponse$json = {
  '1': 'ScanEdgesResponse',
  '2': [
    {
      '1': 'edges',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Edge',
      '10': 'edges'
    },
    {'1': 'next_cursor', '3': 2, '4': 1, '5': 12, '10': 'nextCursor'},
  ],
};

/// Descriptor for `ScanEdgesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List scanEdgesResponseDescriptor = $convert.base64Decode(
    'ChFTY2FuRWRnZXNSZXNwb25zZRIkCgVlZGdlcxgBIAMoCzIOLmdyYXBoLnYxLkVkZ2VSBWVkZ2'
    'VzEh8KC25leHRfY3Vyc29yGAIgASgMUgpuZXh0Q3Vyc29y');

@$core.Deprecated('Use deleteEdgesRequestDescriptor instead')
const DeleteEdgesRequest$json = {
  '1': 'DeleteEdgesRequest',
  '2': [
    {
      '1': 'edges',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.EdgeKey',
      '10': 'edges'
    },
  ],
};

/// Descriptor for `DeleteEdgesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteEdgesRequestDescriptor = $convert.base64Decode(
    'ChJEZWxldGVFZGdlc1JlcXVlc3QSJwoFZWRnZXMYASADKAsyES5ncmFwaC52MS5FZGdlS2V5Ug'
    'VlZGdlcw==');

@$core.Deprecated('Use deleteEdgesResponseDescriptor instead')
const DeleteEdgesResponse$json = {
  '1': 'DeleteEdgesResponse',
  '2': [
    {'1': 'deleted', '3': 1, '4': 1, '5': 5, '10': 'deleted'},
  ],
};

/// Descriptor for `DeleteEdgesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteEdgesResponseDescriptor =
    $convert.base64Decode(
        'ChNEZWxldGVFZGdlc1Jlc3BvbnNlEhgKB2RlbGV0ZWQYASABKAVSB2RlbGV0ZWQ=');

@$core.Deprecated('Use deleteEdgesByPrefixRequestDescriptor instead')
const DeleteEdgesByPrefixRequest$json = {
  '1': 'DeleteEdgesByPrefixRequest',
  '2': [
    {'1': 'tail_prefix', '3': 1, '4': 1, '5': 9, '10': 'tailPrefix'},
    {'1': 'head_prefix', '3': 2, '4': 1, '5': 9, '10': 'headPrefix'},
    {'1': 'limit', '3': 3, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'dry_run', '3': 4, '4': 1, '5': 8, '10': 'dryRun'},
  ],
};

/// Descriptor for `DeleteEdgesByPrefixRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteEdgesByPrefixRequestDescriptor =
    $convert.base64Decode(
        'ChpEZWxldGVFZGdlc0J5UHJlZml4UmVxdWVzdBIfCgt0YWlsX3ByZWZpeBgBIAEoCVIKdGFpbF'
        'ByZWZpeBIfCgtoZWFkX3ByZWZpeBgCIAEoCVIKaGVhZFByZWZpeBIUCgVsaW1pdBgDIAEoDVIF'
        'bGltaXQSFwoHZHJ5X3J1bhgEIAEoCFIGZHJ5UnVu');

@$core.Deprecated('Use deleteEdgesByPrefixResponseDescriptor instead')
const DeleteEdgesByPrefixResponse$json = {
  '1': 'DeleteEdgesByPrefixResponse',
  '2': [
    {'1': 'deleted', '3': 1, '4': 1, '5': 4, '10': 'deleted'},
  ],
};

/// Descriptor for `DeleteEdgesByPrefixResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List deleteEdgesByPrefixResponseDescriptor =
    $convert.base64Decode(
        'ChtEZWxldGVFZGdlc0J5UHJlZml4UmVzcG9uc2USGAoHZGVsZXRlZBgBIAEoBFIHZGVsZXRlZA'
        '==');

@$core.Deprecated('Use addEdgeRequestDescriptor instead')
const AddEdgeRequest$json = {
  '1': 'AddEdgeRequest',
  '2': [
    {'1': 'edge', '3': 1, '4': 1, '5': 11, '6': '.graph.v1.Edge', '10': 'edge'},
    {'1': 'contrib_id', '3': 2, '4': 1, '5': 12, '10': 'contribId'},
  ],
};

/// Descriptor for `AddEdgeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List addEdgeRequestDescriptor = $convert.base64Decode(
    'Cg5BZGRFZGdlUmVxdWVzdBIiCgRlZGdlGAEgASgLMg4uZ3JhcGgudjEuRWRnZVIEZWRnZRIdCg'
    'pjb250cmliX2lkGAIgASgMUgljb250cmliSWQ=');

@$core.Deprecated('Use addEdgeResponseDescriptor instead')
const AddEdgeResponse$json = {
  '1': 'AddEdgeResponse',
  '2': [
    {'1': 'effective_weight', '3': 1, '4': 1, '5': 2, '10': 'effectiveWeight'},
  ],
};

/// Descriptor for `AddEdgeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List addEdgeResponseDescriptor = $convert.base64Decode(
    'Cg9BZGRFZGdlUmVzcG9uc2USKQoQZWZmZWN0aXZlX3dlaWdodBgBIAEoAlIPZWZmZWN0aXZlV2'
    'VpZ2h0');

@$core.Deprecated('Use addEdgesRequestDescriptor instead')
const AddEdgesRequest$json = {
  '1': 'AddEdgesRequest',
  '2': [
    {
      '1': 'edges',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Edge',
      '10': 'edges'
    },
    {'1': 'contrib_ids', '3': 2, '4': 3, '5': 12, '10': 'contribIds'},
  ],
};

/// Descriptor for `AddEdgesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List addEdgesRequestDescriptor = $convert.base64Decode(
    'Cg9BZGRFZGdlc1JlcXVlc3QSJAoFZWRnZXMYASADKAsyDi5ncmFwaC52MS5FZGdlUgVlZGdlcx'
    'IfCgtjb250cmliX2lkcxgCIAMoDFIKY29udHJpYklkcw==');

@$core.Deprecated('Use addEdgesResponseDescriptor instead')
const AddEdgesResponse$json = {
  '1': 'AddEdgesResponse',
  '2': [
    {'1': 'written', '3': 1, '4': 1, '5': 5, '10': 'written'},
    {
      '1': 'effective_weights',
      '3': 2,
      '4': 3,
      '5': 2,
      '10': 'effectiveWeights'
    },
  ],
};

/// Descriptor for `AddEdgesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List addEdgesResponseDescriptor = $convert.base64Decode(
    'ChBBZGRFZGdlc1Jlc3BvbnNlEhgKB3dyaXR0ZW4YASABKAVSB3dyaXR0ZW4SKwoRZWZmZWN0aX'
    'ZlX3dlaWdodHMYAiADKAJSEGVmZmVjdGl2ZVdlaWdodHM=');

@$core.Deprecated('Use putEdgeRequestDescriptor instead')
const PutEdgeRequest$json = {
  '1': 'PutEdgeRequest',
  '2': [
    {'1': 'edge', '3': 1, '4': 1, '5': 11, '6': '.graph.v1.Edge', '10': 'edge'},
  ],
};

/// Descriptor for `PutEdgeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putEdgeRequestDescriptor = $convert.base64Decode(
    'Cg5QdXRFZGdlUmVxdWVzdBIiCgRlZGdlGAEgASgLMg4uZ3JhcGgudjEuRWRnZVIEZWRnZQ==');

@$core.Deprecated('Use putEdgeResponseDescriptor instead')
const PutEdgeResponse$json = {
  '1': 'PutEdgeResponse',
  '2': [
    {
      '1': 'outcome',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.PutOutcome',
      '10': 'outcome'
    },
  ],
};

/// Descriptor for `PutEdgeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putEdgeResponseDescriptor = $convert.base64Decode(
    'Cg9QdXRFZGdlUmVzcG9uc2USLgoHb3V0Y29tZRgBIAEoDjIULmdyYXBoLnYxLlB1dE91dGNvbW'
    'VSB291dGNvbWU=');

@$core.Deprecated('Use putEdgesRequestDescriptor instead')
const PutEdgesRequest$json = {
  '1': 'PutEdgesRequest',
  '2': [
    {
      '1': 'edges',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.Edge',
      '10': 'edges'
    },
  ],
};

/// Descriptor for `PutEdgesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putEdgesRequestDescriptor = $convert.base64Decode(
    'Cg9QdXRFZGdlc1JlcXVlc3QSJAoFZWRnZXMYASADKAsyDi5ncmFwaC52MS5FZGdlUgVlZGdlcw'
    '==');

@$core.Deprecated('Use putEdgesResponseDescriptor instead')
const PutEdgesResponse$json = {
  '1': 'PutEdgesResponse',
  '2': [
    {
      '1': 'outcomes',
      '3': 1,
      '4': 3,
      '5': 14,
      '6': '.graph.v1.PutOutcome',
      '10': 'outcomes'
    },
  ],
};

/// Descriptor for `PutEdgesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List putEdgesResponseDescriptor = $convert.base64Decode(
    'ChBQdXRFZGdlc1Jlc3BvbnNlEjAKCG91dGNvbWVzGAEgAygOMhQuZ3JhcGgudjEuUHV0T3V0Y2'
    '9tZVIIb3V0Y29tZXM=');

@$core.Deprecated('Use getServerStatusRequestDescriptor instead')
const GetServerStatusRequest$json = {
  '1': 'GetServerStatusRequest',
};

/// Descriptor for `GetServerStatusRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getServerStatusRequestDescriptor =
    $convert.base64Decode('ChZHZXRTZXJ2ZXJTdGF0dXNSZXF1ZXN0');

@$core.Deprecated('Use searchErrorDetailDescriptor instead')
const SearchErrorDetail$json = {
  '1': 'SearchErrorDetail',
  '2': [
    {
      '1': 'reason',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SearchErrorReason',
      '10': 'reason'
    },
    {'1': 'work_kind', '3': 2, '4': 1, '5': 9, '10': 'workKind'},
  ],
};

/// Descriptor for `SearchErrorDetail`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List searchErrorDetailDescriptor = $convert.base64Decode(
    'ChFTZWFyY2hFcnJvckRldGFpbBIzCgZyZWFzb24YASABKA4yGy5ncmFwaC52MS5TZWFyY2hFcn'
    'JvclJlYXNvblIGcmVhc29uEhsKCXdvcmtfa2luZBgCIAEoCVIId29ya0tpbmQ=');

@$core.Deprecated('Use searchCapabilitiesDescriptor instead')
const SearchCapabilities$json = {
  '1': 'SearchCapabilities',
  '2': [
    {'1': 'enabled', '3': 1, '4': 1, '5': 8, '10': 'enabled'},
    {
      '1': 'positions_enabled',
      '3': 2,
      '4': 1,
      '5': 8,
      '10': 'positionsEnabled'
    },
    {'1': 'default_limit', '3': 3, '4': 1, '5': 13, '10': 'defaultLimit'},
    {'1': 'max_limit', '3': 4, '4': 1, '5': 13, '10': 'maxLimit'},
    {
      '1': 'default_match_mode',
      '3': 5,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.MatchMode',
      '10': 'defaultMatchMode'
    },
    {
      '1': 'default_min_should_match',
      '3': 6,
      '4': 1,
      '5': 13,
      '10': 'defaultMinShouldMatch'
    },
    {'1': 'max_fuzziness', '3': 7, '4': 1, '5': 13, '10': 'maxFuzziness'},
    {'1': 'analyzer_version', '3': 8, '4': 1, '5': 9, '10': 'analyzerVersion'},
    {
      '1': 'projection_version',
      '3': 9,
      '4': 1,
      '5': 9,
      '10': 'projectionVersion'
    },
    {
      '1': 'config_fingerprint',
      '3': 10,
      '4': 1,
      '5': 9,
      '10': 'configFingerprint'
    },
    {'1': 'timeout_ms', '3': 11, '4': 1, '5': 13, '10': 'timeoutMs'},
    {'1': 'max_query_bytes', '3': 12, '4': 1, '5': 13, '10': 'maxQueryBytes'},
    {'1': 'max_query_terms', '3': 13, '4': 1, '5': 13, '10': 'maxQueryTerms'},
    {
      '1': 'max_dictionary_visits',
      '3': 14,
      '4': 1,
      '5': 4,
      '10': 'maxDictionaryVisits'
    },
    {
      '1': 'max_posting_visits',
      '3': 15,
      '4': 1,
      '5': 4,
      '10': 'maxPostingVisits'
    },
    {
      '1': 'max_position_visits',
      '3': 16,
      '4': 1,
      '5': 4,
      '10': 'maxPositionVisits'
    },
    {'1': 'max_in_flight', '3': 17, '4': 1, '5': 13, '10': 'maxInFlight'},
    {
      '1': 'max_document_bytes',
      '3': 18,
      '4': 1,
      '5': 13,
      '10': 'maxDocumentBytes'
    },
    {
      '1': 'max_document_tokens',
      '3': 19,
      '4': 1,
      '5': 13,
      '10': 'maxDocumentTokens'
    },
    {
      '1': 'max_document_terms',
      '3': 20,
      '4': 1,
      '5': 13,
      '10': 'maxDocumentTerms'
    },
    {'1': 'max_live_terms', '3': 21, '4': 1, '5': 4, '10': 'maxLiveTerms'},
    {
      '1': 'max_live_postings',
      '3': 22,
      '4': 1,
      '5': 4,
      '10': 'maxLivePostings'
    },
    {
      '1': 'max_position_entries',
      '3': 23,
      '4': 1,
      '5': 4,
      '10': 'maxPositionEntries'
    },
    {'1': 'compaction_ratio', '3': 24, '4': 1, '5': 1, '10': 'compactionRatio'},
    {
      '1': 'compaction_min_retired',
      '3': 25,
      '4': 1,
      '5': 4,
      '10': 'compactionMinRetired'
    },
    {
      '1': 'index_stats',
      '3': 26,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SearchIndexStats',
      '10': 'indexStats'
    },
    {
      '1': 'max_expiration_visits',
      '3': 27,
      '4': 1,
      '5': 4,
      '10': 'maxExpirationVisits'
    },
    {
      '1': 'cursor_ttl_seconds',
      '3': 28,
      '4': 1,
      '5': 13,
      '10': 'cursorTtlSeconds'
    },
    {'1': 'max_sessions', '3': 29, '4': 1, '5': 13, '10': 'maxSessions'},
    {'1': 'max_session_hits', '3': 30, '4': 1, '5': 13, '10': 'maxSessionHits'},
    {
      '1': 'max_session_bytes',
      '3': 31,
      '4': 1,
      '5': 4,
      '10': 'maxSessionBytes'
    },
  ],
};

/// Descriptor for `SearchCapabilities`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List searchCapabilitiesDescriptor = $convert.base64Decode(
    'ChJTZWFyY2hDYXBhYmlsaXRpZXMSGAoHZW5hYmxlZBgBIAEoCFIHZW5hYmxlZBIrChFwb3NpdG'
    'lvbnNfZW5hYmxlZBgCIAEoCFIQcG9zaXRpb25zRW5hYmxlZBIjCg1kZWZhdWx0X2xpbWl0GAMg'
    'ASgNUgxkZWZhdWx0TGltaXQSGwoJbWF4X2xpbWl0GAQgASgNUghtYXhMaW1pdBJBChJkZWZhdW'
    'x0X21hdGNoX21vZGUYBSABKA4yEy5ncmFwaC52MS5NYXRjaE1vZGVSEGRlZmF1bHRNYXRjaE1v'
    'ZGUSNwoYZGVmYXVsdF9taW5fc2hvdWxkX21hdGNoGAYgASgNUhVkZWZhdWx0TWluU2hvdWxkTW'
    'F0Y2gSIwoNbWF4X2Z1enppbmVzcxgHIAEoDVIMbWF4RnV6emluZXNzEikKEGFuYWx5emVyX3Zl'
    'cnNpb24YCCABKAlSD2FuYWx5emVyVmVyc2lvbhItChJwcm9qZWN0aW9uX3ZlcnNpb24YCSABKA'
    'lSEXByb2plY3Rpb25WZXJzaW9uEi0KEmNvbmZpZ19maW5nZXJwcmludBgKIAEoCVIRY29uZmln'
    'RmluZ2VycHJpbnQSHQoKdGltZW91dF9tcxgLIAEoDVIJdGltZW91dE1zEiYKD21heF9xdWVyeV'
    '9ieXRlcxgMIAEoDVINbWF4UXVlcnlCeXRlcxImCg9tYXhfcXVlcnlfdGVybXMYDSABKA1SDW1h'
    'eFF1ZXJ5VGVybXMSMgoVbWF4X2RpY3Rpb25hcnlfdmlzaXRzGA4gASgEUhNtYXhEaWN0aW9uYX'
    'J5VmlzaXRzEiwKEm1heF9wb3N0aW5nX3Zpc2l0cxgPIAEoBFIQbWF4UG9zdGluZ1Zpc2l0cxIu'
    'ChNtYXhfcG9zaXRpb25fdmlzaXRzGBAgASgEUhFtYXhQb3NpdGlvblZpc2l0cxIiCg1tYXhfaW'
    '5fZmxpZ2h0GBEgASgNUgttYXhJbkZsaWdodBIsChJtYXhfZG9jdW1lbnRfYnl0ZXMYEiABKA1S'
    'EG1heERvY3VtZW50Qnl0ZXMSLgoTbWF4X2RvY3VtZW50X3Rva2VucxgTIAEoDVIRbWF4RG9jdW'
    '1lbnRUb2tlbnMSLAoSbWF4X2RvY3VtZW50X3Rlcm1zGBQgASgNUhBtYXhEb2N1bWVudFRlcm1z'
    'EiQKDm1heF9saXZlX3Rlcm1zGBUgASgEUgxtYXhMaXZlVGVybXMSKgoRbWF4X2xpdmVfcG9zdG'
    'luZ3MYFiABKARSD21heExpdmVQb3N0aW5ncxIwChRtYXhfcG9zaXRpb25fZW50cmllcxgXIAEo'
    'BFISbWF4UG9zaXRpb25FbnRyaWVzEikKEGNvbXBhY3Rpb25fcmF0aW8YGCABKAFSD2NvbXBhY3'
    'Rpb25SYXRpbxI0ChZjb21wYWN0aW9uX21pbl9yZXRpcmVkGBkgASgEUhRjb21wYWN0aW9uTWlu'
    'UmV0aXJlZBI7CgtpbmRleF9zdGF0cxgaIAEoCzIaLmdyYXBoLnYxLlNlYXJjaEluZGV4U3RhdH'
    'NSCmluZGV4U3RhdHMSMgoVbWF4X2V4cGlyYXRpb25fdmlzaXRzGBsgASgEUhNtYXhFeHBpcmF0'
    'aW9uVmlzaXRzEiwKEmN1cnNvcl90dGxfc2Vjb25kcxgcIAEoDVIQY3Vyc29yVHRsU2Vjb25kcx'
    'IhCgxtYXhfc2Vzc2lvbnMYHSABKA1SC21heFNlc3Npb25zEigKEG1heF9zZXNzaW9uX2hpdHMY'
    'HiABKA1SDm1heFNlc3Npb25IaXRzEioKEW1heF9zZXNzaW9uX2J5dGVzGB8gASgEUg9tYXhTZX'
    'NzaW9uQnl0ZXM=');

@$core.Deprecated('Use searchIndexStatsDescriptor instead')
const SearchIndexStats$json = {
  '1': 'SearchIndexStats',
  '2': [
    {
      '1': 'health',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SearchIndexHealth',
      '10': 'health'
    },
    {'1': 'documents', '3': 2, '4': 1, '5': 4, '10': 'documents'},
    {'1': 'live_terms', '3': 3, '4': 1, '5': 4, '10': 'liveTerms'},
    {
      '1': 'retained_term_slots',
      '3': 4,
      '4': 1,
      '5': 4,
      '10': 'retainedTermSlots'
    },
    {
      '1': 'retained_ordinals',
      '3': 5,
      '4': 1,
      '5': 4,
      '10': 'retainedOrdinals'
    },
    {'1': 'postings', '3': 6, '4': 1, '5': 4, '10': 'postings'},
    {'1': 'position_entries', '3': 7, '4': 1, '5': 4, '10': 'positionEntries'},
    {
      '1': 'estimated_live_bytes',
      '3': 8,
      '4': 1,
      '5': 4,
      '10': 'estimatedLiveBytes'
    },
    {
      '1': 'estimated_retained_bytes',
      '3': 9,
      '4': 1,
      '5': 4,
      '10': 'estimatedRetainedBytes'
    },
    {'1': 'rebuild_count', '3': 10, '4': 1, '5': 4, '10': 'rebuildCount'},
    {
      '1': 'last_rebuild_duration',
      '3': 11,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Duration',
      '10': 'lastRebuildDuration'
    },
    {
      '1': 'physical_documents',
      '3': 12,
      '4': 1,
      '5': 4,
      '10': 'physicalDocuments'
    },
    {
      '1': 'expired_documents',
      '3': 13,
      '4': 1,
      '5': 4,
      '10': 'expiredDocuments'
    },
    {
      '1': 'expiration_queue_entries',
      '3': 14,
      '4': 1,
      '5': 4,
      '10': 'expirationQueueEntries'
    },
    {
      '1': 'expiration_purged',
      '3': 15,
      '4': 1,
      '5': 4,
      '10': 'expirationPurged'
    },
    {
      '1': 'last_expiration_purge_duration',
      '3': 16,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Duration',
      '10': 'lastExpirationPurgeDuration'
    },
    {'1': 'generation', '3': 17, '4': 1, '5': 4, '10': 'generation'},
  ],
};

/// Descriptor for `SearchIndexStats`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List searchIndexStatsDescriptor = $convert.base64Decode(
    'ChBTZWFyY2hJbmRleFN0YXRzEjMKBmhlYWx0aBgBIAEoDjIbLmdyYXBoLnYxLlNlYXJjaEluZG'
    'V4SGVhbHRoUgZoZWFsdGgSHAoJZG9jdW1lbnRzGAIgASgEUglkb2N1bWVudHMSHQoKbGl2ZV90'
    'ZXJtcxgDIAEoBFIJbGl2ZVRlcm1zEi4KE3JldGFpbmVkX3Rlcm1fc2xvdHMYBCABKARSEXJldG'
    'FpbmVkVGVybVNsb3RzEisKEXJldGFpbmVkX29yZGluYWxzGAUgASgEUhByZXRhaW5lZE9yZGlu'
    'YWxzEhoKCHBvc3RpbmdzGAYgASgEUghwb3N0aW5ncxIpChBwb3NpdGlvbl9lbnRyaWVzGAcgAS'
    'gEUg9wb3NpdGlvbkVudHJpZXMSMAoUZXN0aW1hdGVkX2xpdmVfYnl0ZXMYCCABKARSEmVzdGlt'
    'YXRlZExpdmVCeXRlcxI4Chhlc3RpbWF0ZWRfcmV0YWluZWRfYnl0ZXMYCSABKARSFmVzdGltYX'
    'RlZFJldGFpbmVkQnl0ZXMSIwoNcmVidWlsZF9jb3VudBgKIAEoBFIMcmVidWlsZENvdW50Ek0K'
    'FWxhc3RfcmVidWlsZF9kdXJhdGlvbhgLIAEoCzIZLmdvb2dsZS5wcm90b2J1Zi5EdXJhdGlvbl'
    'ITbGFzdFJlYnVpbGREdXJhdGlvbhItChJwaHlzaWNhbF9kb2N1bWVudHMYDCABKARSEXBoeXNp'
    'Y2FsRG9jdW1lbnRzEisKEWV4cGlyZWRfZG9jdW1lbnRzGA0gASgEUhBleHBpcmVkRG9jdW1lbn'
    'RzEjgKGGV4cGlyYXRpb25fcXVldWVfZW50cmllcxgOIAEoBFIWZXhwaXJhdGlvblF1ZXVlRW50'
    'cmllcxIrChFleHBpcmF0aW9uX3B1cmdlZBgPIAEoBFIQZXhwaXJhdGlvblB1cmdlZBJeCh5sYX'
    'N0X2V4cGlyYXRpb25fcHVyZ2VfZHVyYXRpb24YECABKAsyGS5nb29nbGUucHJvdG9idWYuRHVy'
    'YXRpb25SG2xhc3RFeHBpcmF0aW9uUHVyZ2VEdXJhdGlvbhIeCgpnZW5lcmF0aW9uGBEgASgEUg'
    'pnZW5lcmF0aW9u');

@$core.Deprecated('Use getServerStatusResponseDescriptor instead')
const GetServerStatusResponse$json = {
  '1': 'GetServerStatusResponse',
  '2': [
    {'1': 'version', '3': 1, '4': 1, '5': 9, '10': 'version'},
    {'1': 'go_version', '3': 2, '4': 1, '5': 9, '10': 'goVersion'},
    {
      '1': 'started_at',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'startedAt'
    },
    {
      '1': 'uptime',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Duration',
      '10': 'uptime'
    },
    {
      '1': 'default_ttl',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Duration',
      '10': 'defaultTtl'
    },
    {'1': 'max_batch_size', '3': 6, '4': 1, '5': 13, '10': 'maxBatchSize'},
    {'1': 'max_key_bytes', '3': 7, '4': 1, '5': 13, '10': 'maxKeyBytes'},
    {
      '1': 'scan_default_limit',
      '3': 8,
      '4': 1,
      '5': 13,
      '10': 'scanDefaultLimit'
    },
    {'1': 'scan_max_limit', '3': 9, '4': 1, '5': 13, '10': 'scanMaxLimit'},
    {'1': 'tls_enabled', '3': 10, '4': 1, '5': 8, '10': 'tlsEnabled'},
    {
      '1': 'replication_enabled',
      '3': 11,
      '4': 1,
      '5': 8,
      '10': 'replicationEnabled'
    },
    {'1': 'vertex_count', '3': 12, '4': 1, '5': 4, '10': 'vertexCount'},
    {'1': 'edge_count', '3': 13, '4': 1, '5': 4, '10': 'edgeCount'},
    {
      '1': 'search',
      '3': 14,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SearchCapabilities',
      '10': 'search'
    },
  ],
};

/// Descriptor for `GetServerStatusResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getServerStatusResponseDescriptor = $convert.base64Decode(
    'ChdHZXRTZXJ2ZXJTdGF0dXNSZXNwb25zZRIYCgd2ZXJzaW9uGAEgASgJUgd2ZXJzaW9uEh0KCm'
    'dvX3ZlcnNpb24YAiABKAlSCWdvVmVyc2lvbhI5CgpzdGFydGVkX2F0GAMgASgLMhouZ29vZ2xl'
    'LnByb3RvYnVmLlRpbWVzdGFtcFIJc3RhcnRlZEF0EjEKBnVwdGltZRgEIAEoCzIZLmdvb2dsZS'
    '5wcm90b2J1Zi5EdXJhdGlvblIGdXB0aW1lEjoKC2RlZmF1bHRfdHRsGAUgASgLMhkuZ29vZ2xl'
    'LnByb3RvYnVmLkR1cmF0aW9uUgpkZWZhdWx0VHRsEiQKDm1heF9iYXRjaF9zaXplGAYgASgNUg'
    'xtYXhCYXRjaFNpemUSIgoNbWF4X2tleV9ieXRlcxgHIAEoDVILbWF4S2V5Qnl0ZXMSLAoSc2Nh'
    'bl9kZWZhdWx0X2xpbWl0GAggASgNUhBzY2FuRGVmYXVsdExpbWl0EiQKDnNjYW5fbWF4X2xpbW'
    'l0GAkgASgNUgxzY2FuTWF4TGltaXQSHwoLdGxzX2VuYWJsZWQYCiABKAhSCnRsc0VuYWJsZWQS'
    'LwoTcmVwbGljYXRpb25fZW5hYmxlZBgLIAEoCFIScmVwbGljYXRpb25FbmFibGVkEiEKDHZlcn'
    'RleF9jb3VudBgMIAEoBFILdmVydGV4Q291bnQSHQoKZWRnZV9jb3VudBgNIAEoBFIJZWRnZUNv'
    'dW50EjQKBnNlYXJjaBgOIAEoCzIcLmdyYXBoLnYxLlNlYXJjaENhcGFiaWxpdGllc1IGc2Vhcm'
    'No');

@$core.Deprecated('Use replicationPeerDescriptor instead')
const ReplicationPeer$json = {
  '1': 'ReplicationPeer',
  '2': [
    {'1': 'address', '3': 1, '4': 1, '5': 9, '10': 'address'},
    {
      '1': 'state',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.ReplicationPeer.State',
      '10': 'state'
    },
    {
      '1': 'last_event_at',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'lastEventAt'
    },
    {'1': 'applied_seq', '3': 4, '4': 1, '5': 4, '10': 'appliedSeq'},
    {'1': 'error', '3': 5, '4': 1, '5': 9, '10': 'error'},
  ],
  '4': [ReplicationPeer_State$json],
};

@$core.Deprecated('Use replicationPeerDescriptor instead')
const ReplicationPeer_State$json = {
  '1': 'State',
  '2': [
    {'1': 'STATE_UNSPECIFIED', '2': 0},
    {'1': 'STATE_CONNECTING', '2': 1},
    {'1': 'STATE_STREAMING', '2': 2},
    {'1': 'STATE_BACKOFF', '2': 3},
    {'1': 'STATE_CLOSED', '2': 4},
  ],
};

/// Descriptor for `ReplicationPeer`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List replicationPeerDescriptor = $convert.base64Decode(
    'Cg9SZXBsaWNhdGlvblBlZXISGAoHYWRkcmVzcxgBIAEoCVIHYWRkcmVzcxI1CgVzdGF0ZRgCIA'
    'EoDjIfLmdyYXBoLnYxLlJlcGxpY2F0aW9uUGVlci5TdGF0ZVIFc3RhdGUSPgoNbGFzdF9ldmVu'
    'dF9hdBgDIAEoCzIaLmdvb2dsZS5wcm90b2J1Zi5UaW1lc3RhbXBSC2xhc3RFdmVudEF0Eh8KC2'
    'FwcGxpZWRfc2VxGAQgASgEUgphcHBsaWVkU2VxEhQKBWVycm9yGAUgASgJUgVlcnJvciJuCgVT'
    'dGF0ZRIVChFTVEFURV9VTlNQRUNJRklFRBAAEhQKEFNUQVRFX0NPTk5FQ1RJTkcQARITCg9TVE'
    'FURV9TVFJFQU1JTkcQAhIRCg1TVEFURV9CQUNLT0ZGEAMSEAoMU1RBVEVfQ0xPU0VEEAQ=');

@$core.Deprecated('Use getReplicationStatusRequestDescriptor instead')
const GetReplicationStatusRequest$json = {
  '1': 'GetReplicationStatusRequest',
};

/// Descriptor for `GetReplicationStatusRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getReplicationStatusRequestDescriptor =
    $convert.base64Decode('ChtHZXRSZXBsaWNhdGlvblN0YXR1c1JlcXVlc3Q=');

@$core.Deprecated('Use getReplicationStatusResponseDescriptor instead')
const GetReplicationStatusResponse$json = {
  '1': 'GetReplicationStatusResponse',
  '2': [
    {'1': 'node_id', '3': 1, '4': 1, '5': 9, '10': 'nodeId'},
    {
      '1': 'local_now',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'localNow'
    },
    {'1': 'enabled', '3': 3, '4': 1, '5': 8, '10': 'enabled'},
    {
      '1': 'peers',
      '3': 10,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.ReplicationPeer',
      '10': 'peers'
    },
  ],
};

/// Descriptor for `GetReplicationStatusResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getReplicationStatusResponseDescriptor = $convert.base64Decode(
    'ChxHZXRSZXBsaWNhdGlvblN0YXR1c1Jlc3BvbnNlEhcKB25vZGVfaWQYASABKAlSBm5vZGVJZB'
    'I3Cglsb2NhbF9ub3cYAiABKAsyGi5nb29nbGUucHJvdG9idWYuVGltZXN0YW1wUghsb2NhbE5v'
    'dxIYCgdlbmFibGVkGAMgASgIUgdlbmFibGVkEi8KBXBlZXJzGAogAygLMhkuZ3JhcGgudjEuUm'
    'VwbGljYXRpb25QZWVyUgVwZWVycw==');

@$core.Deprecated('Use backupSnapshotRequestDescriptor instead')
const BackupSnapshotRequest$json = {
  '1': 'BackupSnapshotRequest',
  '2': [
    {'1': 'vertex_prefix', '3': 1, '4': 1, '5': 9, '10': 'vertexPrefix'},
  ],
};

/// Descriptor for `BackupSnapshotRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List backupSnapshotRequestDescriptor = $convert.base64Decode(
    'ChVCYWNrdXBTbmFwc2hvdFJlcXVlc3QSIwoNdmVydGV4X3ByZWZpeBgBIAEoCVIMdmVydGV4UH'
    'JlZml4');

@$core.Deprecated('Use backupSnapshotResponseDescriptor instead')
const BackupSnapshotResponse$json = {
  '1': 'BackupSnapshotResponse',
  '2': [
    {
      '1': 'vertex',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Vertex',
      '9': 0,
      '10': 'vertex'
    },
    {
      '1': 'edge',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.Edge',
      '9': 0,
      '10': 'edge'
    },
  ],
  '8': [
    {'1': 'record'},
  ],
};

/// Descriptor for `BackupSnapshotResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List backupSnapshotResponseDescriptor = $convert.base64Decode(
    'ChZCYWNrdXBTbmFwc2hvdFJlc3BvbnNlEioKBnZlcnRleBgBIAEoCzIQLmdyYXBoLnYxLlZlcn'
    'RleEgAUgZ2ZXJ0ZXgSJAoEZWRnZRgCIAEoCzIOLmdyYXBoLnYxLkVkZ2VIAFIEZWRnZUIICgZy'
    'ZWNvcmQ=');
