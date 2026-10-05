// This is a generated file - do not edit.
//
// Generated from graph/v1/security.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, unused_import

import 'dart:convert' as $convert;
import 'dart:core' as $core;
import 'dart:typed_data' as $typed_data;

import '../../google/protobuf/timestamp.pbjson.dart' as $0;

@$core.Deprecated('Use authModeDescriptor instead')
const AuthMode$json = {
  '1': 'AuthMode',
  '2': [
    {'1': 'AUTH_MODE_UNSPECIFIED', '2': 0},
    {'1': 'AUTH_MODE_OFF', '2': 1},
    {'1': 'AUTH_MODE_OIDC', '2': 2},
  ],
};

/// Descriptor for `AuthMode`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List authModeDescriptor = $convert.base64Decode(
    'CghBdXRoTW9kZRIZChVBVVRIX01PREVfVU5TUEVDSUZJRUQQABIRCg1BVVRIX01PREVfT0ZGEA'
    'ESEgoOQVVUSF9NT0RFX09JREMQAg==');

@$core.Deprecated('Use securityPrincipalKindDescriptor instead')
const SecurityPrincipalKind$json = {
  '1': 'SecurityPrincipalKind',
  '2': [
    {'1': 'SECURITY_PRINCIPAL_KIND_UNSPECIFIED', '2': 0},
    {'1': 'SECURITY_PRINCIPAL_KIND_OIDC', '2': 1},
    {'1': 'SECURITY_PRINCIPAL_KIND_MACHINE', '2': 2},
  ],
};

/// Descriptor for `SecurityPrincipalKind`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List securityPrincipalKindDescriptor = $convert.base64Decode(
    'ChVTZWN1cml0eVByaW5jaXBhbEtpbmQSJwojU0VDVVJJVFlfUFJJTkNJUEFMX0tJTkRfVU5TUE'
    'VDSUZJRUQQABIgChxTRUNVUklUWV9QUklOQ0lQQUxfS0lORF9PSURDEAESIwofU0VDVVJJVFlf'
    'UFJJTkNJUEFMX0tJTkRfTUFDSElORRAC');

@$core.Deprecated('Use securityPrincipalStateDescriptor instead')
const SecurityPrincipalState$json = {
  '1': 'SecurityPrincipalState',
  '2': [
    {'1': 'SECURITY_PRINCIPAL_STATE_UNSPECIFIED', '2': 0},
    {'1': 'SECURITY_PRINCIPAL_STATE_ACTIVE', '2': 1},
    {'1': 'SECURITY_PRINCIPAL_STATE_SUSPENDED', '2': 2},
    {'1': 'SECURITY_PRINCIPAL_STATE_DELETED', '2': 3},
  ],
};

/// Descriptor for `SecurityPrincipalState`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List securityPrincipalStateDescriptor = $convert.base64Decode(
    'ChZTZWN1cml0eVByaW5jaXBhbFN0YXRlEigKJFNFQ1VSSVRZX1BSSU5DSVBBTF9TVEFURV9VTl'
    'NQRUNJRklFRBAAEiMKH1NFQ1VSSVRZX1BSSU5DSVBBTF9TVEFURV9BQ1RJVkUQARImCiJTRUNV'
    'UklUWV9QUklOQ0lQQUxfU1RBVEVfU1VTUEVOREVEEAISJAogU0VDVVJJVFlfUFJJTkNJUEFMX1'
    'NUQVRFX0RFTEVURUQQAw==');

@$core.Deprecated('Use securityActionDescriptor instead')
const SecurityAction$json = {
  '1': 'SecurityAction',
  '2': [
    {'1': 'SECURITY_ACTION_UNSPECIFIED', '2': 0},
    {'1': 'SECURITY_ACTION_VERTEX_READ', '2': 1},
    {'1': 'SECURITY_ACTION_VERTEX_WRITE', '2': 2},
    {'1': 'SECURITY_ACTION_VERTEX_DELETE', '2': 3},
    {'1': 'SECURITY_ACTION_EDGE_READ', '2': 4},
    {'1': 'SECURITY_ACTION_EDGE_ADD', '2': 5},
    {'1': 'SECURITY_ACTION_EDGE_WRITE', '2': 6},
    {'1': 'SECURITY_ACTION_EDGE_DELETE', '2': 7},
    {'1': 'SECURITY_ACTION_QUERY', '2': 8},
    {'1': 'SECURITY_ACTION_CDC_IDENTITY', '2': 9},
    {'1': 'SECURITY_ACTION_CDC_VALUE', '2': 10},
    {'1': 'SECURITY_ACTION_EXPORT', '2': 11},
    {'1': 'SECURITY_ACTION_RECEIPT_READ', '2': 12},
    {'1': 'SECURITY_ACTION_OPERATIONS_READ', '2': 13},
    {'1': 'SECURITY_ACTION_SCHEMA_READ', '2': 14},
    {'1': 'SECURITY_ACTION_MANAGE', '2': 15},
    {'1': 'SECURITY_ACTION_EDGE_CREATE', '2': 16},
  ],
};

/// Descriptor for `SecurityAction`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List securityActionDescriptor = $convert.base64Decode(
    'Cg5TZWN1cml0eUFjdGlvbhIfChtTRUNVUklUWV9BQ1RJT05fVU5TUEVDSUZJRUQQABIfChtTRU'
    'NVUklUWV9BQ1RJT05fVkVSVEVYX1JFQUQQARIgChxTRUNVUklUWV9BQ1RJT05fVkVSVEVYX1dS'
    'SVRFEAISIQodU0VDVVJJVFlfQUNUSU9OX1ZFUlRFWF9ERUxFVEUQAxIdChlTRUNVUklUWV9BQ1'
    'RJT05fRURHRV9SRUFEEAQSHAoYU0VDVVJJVFlfQUNUSU9OX0VER0VfQUREEAUSHgoaU0VDVVJJ'
    'VFlfQUNUSU9OX0VER0VfV1JJVEUQBhIfChtTRUNVUklUWV9BQ1RJT05fRURHRV9ERUxFVEUQBx'
    'IZChVTRUNVUklUWV9BQ1RJT05fUVVFUlkQCBIgChxTRUNVUklUWV9BQ1RJT05fQ0RDX0lERU5U'
    'SVRZEAkSHQoZU0VDVVJJVFlfQUNUSU9OX0NEQ19WQUxVRRAKEhoKFlNFQ1VSSVRZX0FDVElPTl'
    '9FWFBPUlQQCxIgChxTRUNVUklUWV9BQ1RJT05fUkVDRUlQVF9SRUFEEAwSIwofU0VDVVJJVFlf'
    'QUNUSU9OX09QRVJBVElPTlNfUkVBRBANEh8KG1NFQ1VSSVRZX0FDVElPTl9TQ0hFTUFfUkVBRB'
    'AOEhoKFlNFQ1VSSVRZX0FDVElPTl9NQU5BR0UQDxIfChtTRUNVUklUWV9BQ1RJT05fRURHRV9D'
    'UkVBVEUQEA==');

@$core.Deprecated('Use securityEffectDescriptor instead')
const SecurityEffect$json = {
  '1': 'SecurityEffect',
  '2': [
    {'1': 'SECURITY_EFFECT_UNSPECIFIED', '2': 0},
    {'1': 'SECURITY_EFFECT_ALLOW', '2': 1},
    {'1': 'SECURITY_EFFECT_DENY', '2': 2},
  ],
};

/// Descriptor for `SecurityEffect`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List securityEffectDescriptor = $convert.base64Decode(
    'Cg5TZWN1cml0eUVmZmVjdBIfChtTRUNVUklUWV9FRkZFQ1RfVU5TUEVDSUZJRUQQABIZChVTRU'
    'NVUklUWV9FRkZFQ1RfQUxMT1cQARIYChRTRUNVUklUWV9FRkZFQ1RfREVOWRAC');

@$core.Deprecated('Use securityEnforcementStateDescriptor instead')
const SecurityEnforcementState$json = {
  '1': 'SecurityEnforcementState',
  '2': [
    {'1': 'SECURITY_ENFORCEMENT_STATE_UNSPECIFIED', '2': 0},
    {'1': 'SECURITY_ENFORCEMENT_STATE_COMMITTED_PENDING', '2': 1},
    {'1': 'SECURITY_ENFORCEMENT_STATE_ENFORCED', '2': 2},
  ],
};

/// Descriptor for `SecurityEnforcementState`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List securityEnforcementStateDescriptor = $convert.base64Decode(
    'ChhTZWN1cml0eUVuZm9yY2VtZW50U3RhdGUSKgomU0VDVVJJVFlfRU5GT1JDRU1FTlRfU1RBVE'
    'VfVU5TUEVDSUZJRUQQABIwCixTRUNVUklUWV9FTkZPUkNFTUVOVF9TVEFURV9DT01NSVRURURf'
    'UEVORElORxABEicKI1NFQ1VSSVRZX0VORk9SQ0VNRU5UX1NUQVRFX0VORk9SQ0VEEAI=');

@$core.Deprecated('Use securityAuthorizationRequirementDescriptor instead')
const SecurityAuthorizationRequirement$json = {
  '1': 'SecurityAuthorizationRequirement',
  '2': [
    {'1': 'SECURITY_AUTHORIZATION_REQUIREMENT_UNSPECIFIED', '2': 0},
    {'1': 'SECURITY_AUTHORIZATION_REQUIREMENT_ORDINARY', '2': 1},
    {'1': 'SECURITY_AUTHORIZATION_REQUIREMENT_REAUTHENTICATION', '2': 2},
  ],
};

/// Descriptor for `SecurityAuthorizationRequirement`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List securityAuthorizationRequirementDescriptor =
    $convert.base64Decode(
        'CiBTZWN1cml0eUF1dGhvcml6YXRpb25SZXF1aXJlbWVudBIyCi5TRUNVUklUWV9BVVRIT1JJWk'
        'FUSU9OX1JFUVVJUkVNRU5UX1VOU1BFQ0lGSUVEEAASLworU0VDVVJJVFlfQVVUSE9SSVpBVElP'
        'Tl9SRVFVSVJFTUVOVF9PUkRJTkFSWRABEjcKM1NFQ1VSSVRZX0FVVEhPUklaQVRJT05fUkVRVU'
        'lSRU1FTlRfUkVBVVRIRU5USUNBVElPThAC');

@$core.Deprecated('Use securityAuthorizationStateDescriptor instead')
const SecurityAuthorizationState$json = {
  '1': 'SecurityAuthorizationState',
  '2': [
    {'1': 'SECURITY_AUTHORIZATION_STATE_UNSPECIFIED', '2': 0},
    {'1': 'SECURITY_AUTHORIZATION_STATE_PENDING', '2': 1},
    {'1': 'SECURITY_AUTHORIZATION_STATE_APPROVED', '2': 2},
    {'1': 'SECURITY_AUTHORIZATION_STATE_DENIED', '2': 3},
  ],
};

/// Descriptor for `SecurityAuthorizationState`. Decode as a `google.protobuf.EnumDescriptorProto`.
final $typed_data.Uint8List securityAuthorizationStateDescriptor = $convert.base64Decode(
    'ChpTZWN1cml0eUF1dGhvcml6YXRpb25TdGF0ZRIsCihTRUNVUklUWV9BVVRIT1JJWkFUSU9OX1'
    'NUQVRFX1VOU1BFQ0lGSUVEEAASKAokU0VDVVJJVFlfQVVUSE9SSVpBVElPTl9TVEFURV9QRU5E'
    'SU5HEAESKQolU0VDVVJJVFlfQVVUSE9SSVpBVElPTl9TVEFURV9BUFBST1ZFRBACEicKI1NFQ1'
    'VSSVRZX0FVVEhPUklaQVRJT05fU1RBVEVfREVOSUVEEAM=');

@$core.Deprecated('Use getAuthCapabilitiesRequestDescriptor instead')
const GetAuthCapabilitiesRequest$json = {
  '1': 'GetAuthCapabilitiesRequest',
};

/// Descriptor for `GetAuthCapabilitiesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getAuthCapabilitiesRequestDescriptor =
    $convert.base64Decode('ChpHZXRBdXRoQ2FwYWJpbGl0aWVzUmVxdWVzdA==');

@$core.Deprecated('Use loginIssuerDescriptor instead')
const LoginIssuer$json = {
  '1': 'LoginIssuer',
  '2': [
    {'1': 'issuer', '3': 1, '4': 1, '5': 9, '10': 'issuer'},
    {'1': 'label', '3': 2, '4': 1, '5': 9, '10': 'label'},
  ],
};

/// Descriptor for `LoginIssuer`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List loginIssuerDescriptor = $convert.base64Decode(
    'CgtMb2dpbklzc3VlchIWCgZpc3N1ZXIYASABKAlSBmlzc3VlchIUCgVsYWJlbBgCIAEoCVIFbG'
    'FiZWw=');

@$core.Deprecated('Use getAuthCapabilitiesResponseDescriptor instead')
const GetAuthCapabilitiesResponse$json = {
  '1': 'GetAuthCapabilitiesResponse',
  '2': [
    {
      '1': 'mode',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.AuthMode',
      '10': 'mode'
    },
    {
      '1': 'login_issuers',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.LoginIssuer',
      '10': 'loginIssuers'
    },
    {'1': 'login_path', '3': 3, '4': 1, '5': 9, '10': 'loginPath'},
    {'1': 'protocol_version', '3': 4, '4': 1, '5': 13, '10': 'protocolVersion'},
    {'1': 'ready', '3': 5, '4': 1, '5': 8, '10': 'ready'},
  ],
};

/// Descriptor for `GetAuthCapabilitiesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getAuthCapabilitiesResponseDescriptor = $convert.base64Decode(
    'ChtHZXRBdXRoQ2FwYWJpbGl0aWVzUmVzcG9uc2USJgoEbW9kZRgBIAEoDjISLmdyYXBoLnYxLk'
    'F1dGhNb2RlUgRtb2RlEjoKDWxvZ2luX2lzc3VlcnMYAiADKAsyFS5ncmFwaC52MS5Mb2dpbklz'
    'c3VlclIMbG9naW5Jc3N1ZXJzEh0KCmxvZ2luX3BhdGgYAyABKAlSCWxvZ2luUGF0aBIpChBwcm'
    '90b2NvbF92ZXJzaW9uGAQgASgNUg9wcm90b2NvbFZlcnNpb24SFAoFcmVhZHkYBSABKAhSBXJl'
    'YWR5');

@$core.Deprecated('Use securityIdentityDescriptor instead')
const SecurityIdentity$json = {
  '1': 'SecurityIdentity',
  '2': [
    {
      '1': 'kind',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityPrincipalKind',
      '10': 'kind'
    },
    {'1': 'issuer', '3': 2, '4': 1, '5': 9, '10': 'issuer'},
    {'1': 'subject', '3': 3, '4': 1, '5': 9, '10': 'subject'},
    {'1': 'machine_name', '3': 4, '4': 1, '5': 9, '10': 'machineName'},
  ],
};

/// Descriptor for `SecurityIdentity`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityIdentityDescriptor = $convert.base64Decode(
    'ChBTZWN1cml0eUlkZW50aXR5EjMKBGtpbmQYASABKA4yHy5ncmFwaC52MS5TZWN1cml0eVByaW'
    '5jaXBhbEtpbmRSBGtpbmQSFgoGaXNzdWVyGAIgASgJUgZpc3N1ZXISGAoHc3ViamVjdBgDIAEo'
    'CVIHc3ViamVjdBIhCgxtYWNoaW5lX25hbWUYBCABKAlSC21hY2hpbmVOYW1l');

@$core.Deprecated('Use securityPrefixPairDescriptor instead')
const SecurityPrefixPair$json = {
  '1': 'SecurityPrefixPair',
  '2': [
    {'1': 'tail_prefix', '3': 1, '4': 1, '5': 9, '10': 'tailPrefix'},
    {'1': 'head_prefix', '3': 2, '4': 1, '5': 9, '10': 'headPrefix'},
  ],
};

/// Descriptor for `SecurityPrefixPair`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityPrefixPairDescriptor = $convert.base64Decode(
    'ChJTZWN1cml0eVByZWZpeFBhaXISHwoLdGFpbF9wcmVmaXgYASABKAlSCnRhaWxQcmVmaXgSHw'
    'oLaGVhZF9wcmVmaXgYAiABKAlSCmhlYWRQcmVmaXg=');

@$core.Deprecated('Use securityRuleDescriptor instead')
const SecurityRule$json = {
  '1': 'SecurityRule',
  '2': [
    {'1': 'id', '3': 1, '4': 1, '5': 9, '10': 'id'},
    {
      '1': 'effect',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityEffect',
      '10': 'effect'
    },
    {
      '1': 'action',
      '3': 3,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityAction',
      '10': 'action'
    },
    {'1': 'prefix', '3': 4, '4': 1, '5': 9, '9': 0, '10': 'prefix'},
    {'1': 'global', '3': 5, '4': 1, '5': 8, '9': 0, '10': 'global'},
    {
      '1': 'pair',
      '3': 6,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityPrefixPair',
      '9': 0,
      '10': 'pair'
    },
  ],
  '8': [
    {'1': 'resource'},
  ],
};

/// Descriptor for `SecurityRule`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityRuleDescriptor = $convert.base64Decode(
    'CgxTZWN1cml0eVJ1bGUSDgoCaWQYASABKAlSAmlkEjAKBmVmZmVjdBgCIAEoDjIYLmdyYXBoLn'
    'YxLlNlY3VyaXR5RWZmZWN0UgZlZmZlY3QSMAoGYWN0aW9uGAMgASgOMhguZ3JhcGgudjEuU2Vj'
    'dXJpdHlBY3Rpb25SBmFjdGlvbhIYCgZwcmVmaXgYBCABKAlIAFIGcHJlZml4EhgKBmdsb2JhbB'
    'gFIAEoCEgAUgZnbG9iYWwSMgoEcGFpchgGIAEoCzIcLmdyYXBoLnYxLlNlY3VyaXR5UHJlZml4'
    'UGFpckgAUgRwYWlyQgoKCHJlc291cmNl');

@$core.Deprecated('Use securityRoleDescriptor instead')
const SecurityRole$json = {
  '1': 'SecurityRole',
  '2': [
    {'1': 'id', '3': 1, '4': 1, '5': 9, '10': 'id'},
    {'1': 'name', '3': 2, '4': 1, '5': 9, '10': 'name'},
    {
      '1': 'rules',
      '3': 3,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRule',
      '10': 'rules'
    },
    {'1': 'env_owned', '3': 4, '4': 1, '5': 8, '10': 'envOwned'},
  ],
};

/// Descriptor for `SecurityRole`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityRoleDescriptor = $convert.base64Decode(
    'CgxTZWN1cml0eVJvbGUSDgoCaWQYASABKAlSAmlkEhIKBG5hbWUYAiABKAlSBG5hbWUSLAoFcn'
    'VsZXMYAyADKAsyFi5ncmFwaC52MS5TZWN1cml0eVJ1bGVSBXJ1bGVzEhsKCWVudl9vd25lZBgE'
    'IAEoCFIIZW52T3duZWQ=');

@$core.Deprecated('Use securityRoleAssignmentDescriptor instead')
const SecurityRoleAssignment$json = {
  '1': 'SecurityRoleAssignment',
  '2': [
    {
      '1': 'identity',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
    {'1': 'role_id', '3': 2, '4': 1, '5': 9, '10': 'roleId'},
    {'1': 'env_owned', '3': 3, '4': 1, '5': 8, '10': 'envOwned'},
  ],
};

/// Descriptor for `SecurityRoleAssignment`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityRoleAssignmentDescriptor = $convert.base64Decode(
    'ChZTZWN1cml0eVJvbGVBc3NpZ25tZW50EjYKCGlkZW50aXR5GAEgASgLMhouZ3JhcGgudjEuU2'
    'VjdXJpdHlJZGVudGl0eVIIaWRlbnRpdHkSFwoHcm9sZV9pZBgCIAEoCVIGcm9sZUlkEhsKCWVu'
    'dl9vd25lZBgDIAEoCFIIZW52T3duZWQ=');

@$core.Deprecated('Use securityUserDescriptor instead')
const SecurityUser$json = {
  '1': 'SecurityUser',
  '2': [
    {
      '1': 'identity',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
    {
      '1': 'state',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityPrincipalState',
      '10': 'state'
    },
    {
      '1': 'assignments',
      '3': 3,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRoleAssignment',
      '10': 'assignments'
    },
  ],
};

/// Descriptor for `SecurityUser`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityUserDescriptor = $convert.base64Decode(
    'CgxTZWN1cml0eVVzZXISNgoIaWRlbnRpdHkYASABKAsyGi5ncmFwaC52MS5TZWN1cml0eUlkZW'
    '50aXR5UghpZGVudGl0eRI2CgVzdGF0ZRgCIAEoDjIgLmdyYXBoLnYxLlNlY3VyaXR5UHJpbmNp'
    'cGFsU3RhdGVSBXN0YXRlEkIKC2Fzc2lnbm1lbnRzGAMgAygLMiAuZ3JhcGgudjEuU2VjdXJpdH'
    'lSb2xlQXNzaWdubWVudFILYXNzaWdubWVudHM=');

@$core.Deprecated('Use securityIssuerDescriptor instead')
const SecurityIssuer$json = {
  '1': 'SecurityIssuer',
  '2': [
    {'1': 'issuer', '3': 1, '4': 1, '5': 9, '10': 'issuer'},
    {'1': 'enabled', '3': 2, '4': 1, '5': 8, '10': 'enabled'},
    {'1': 'client_id', '3': 3, '4': 1, '5': 9, '10': 'clientId'},
    {'1': 'api_audience', '3': 4, '4': 1, '5': 9, '10': 'apiAudience'},
    {'1': 'redirect_uri', '3': 5, '4': 1, '5': 9, '10': 'redirectUri'},
    {'1': 'algorithms', '3': 6, '4': 3, '5': 9, '10': 'algorithms'},
    {
      '1': 'secret_ref',
      '3': 7,
      '4': 1,
      '5': 9,
      '9': 0,
      '10': 'secretRef',
      '17': true
    },
    {'1': 'config_revision', '3': 8, '4': 1, '5': 4, '10': 'configRevision'},
    {'1': 'env_owned', '3': 9, '4': 1, '5': 8, '10': 'envOwned'},
    {'1': 'deleted', '3': 10, '4': 1, '5': 8, '10': 'deleted'},
    {
      '1': 'has_secret_binding',
      '3': 11,
      '4': 1,
      '5': 8,
      '10': 'hasSecretBinding'
    },
    {
      '1': 'human_subject_namespace_qualified',
      '3': 12,
      '4': 1,
      '5': 8,
      '10': 'humanSubjectNamespaceQualified'
    },
  ],
  '8': [
    {'1': '_secret_ref'},
  ],
};

/// Descriptor for `SecurityIssuer`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityIssuerDescriptor = $convert.base64Decode(
    'Cg5TZWN1cml0eUlzc3VlchIWCgZpc3N1ZXIYASABKAlSBmlzc3VlchIYCgdlbmFibGVkGAIgAS'
    'gIUgdlbmFibGVkEhsKCWNsaWVudF9pZBgDIAEoCVIIY2xpZW50SWQSIQoMYXBpX2F1ZGllbmNl'
    'GAQgASgJUgthcGlBdWRpZW5jZRIhCgxyZWRpcmVjdF91cmkYBSABKAlSC3JlZGlyZWN0VXJpEh'
    '4KCmFsZ29yaXRobXMYBiADKAlSCmFsZ29yaXRobXMSIgoKc2VjcmV0X3JlZhgHIAEoCUgAUglz'
    'ZWNyZXRSZWaIAQESJwoPY29uZmlnX3JldmlzaW9uGAggASgEUg5jb25maWdSZXZpc2lvbhIbCg'
    'llbnZfb3duZWQYCSABKAhSCGVudk93bmVkEhgKB2RlbGV0ZWQYCiABKAhSB2RlbGV0ZWQSLAoS'
    'aGFzX3NlY3JldF9iaW5kaW5nGAsgASgIUhBoYXNTZWNyZXRCaW5kaW5nEkkKIWh1bWFuX3N1Ym'
    'plY3RfbmFtZXNwYWNlX3F1YWxpZmllZBgMIAEoCFIeaHVtYW5TdWJqZWN0TmFtZXNwYWNlUXVh'
    'bGlmaWVkQg0KC19zZWNyZXRfcmVm');

@$core.Deprecated('Use securityVersionDescriptor instead')
const SecurityVersion$json = {
  '1': 'SecurityVersion',
  '2': [
    {'1': 'revision', '3': 1, '4': 1, '5': 4, '10': 'revision'},
    {'1': 'digest', '3': 2, '4': 1, '5': 12, '10': 'digest'},
    {'1': 'generation', '3': 3, '4': 1, '5': 12, '10': 'generation'},
  ],
};

/// Descriptor for `SecurityVersion`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityVersionDescriptor = $convert.base64Decode(
    'Cg9TZWN1cml0eVZlcnNpb24SGgoIcmV2aXNpb24YASABKARSCHJldmlzaW9uEhYKBmRpZ2VzdB'
    'gCIAEoDFIGZGlnZXN0Eh4KCmdlbmVyYXRpb24YAyABKAxSCmdlbmVyYXRpb24=');

@$core.Deprecated('Use getCurrentPrincipalRequestDescriptor instead')
const GetCurrentPrincipalRequest$json = {
  '1': 'GetCurrentPrincipalRequest',
};

/// Descriptor for `GetCurrentPrincipalRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getCurrentPrincipalRequestDescriptor =
    $convert.base64Decode('ChpHZXRDdXJyZW50UHJpbmNpcGFsUmVxdWVzdA==');

@$core.Deprecated('Use getCurrentPrincipalResponseDescriptor instead')
const GetCurrentPrincipalResponse$json = {
  '1': 'GetCurrentPrincipalResponse',
  '2': [
    {
      '1': 'identity',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
    {
      '1': 'roles',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRole',
      '10': 'roles'
    },
    {
      '1': 'version',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {
      '1': 'expires_at',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiresAt'
    },
    {
      '1': 'recent_authentication',
      '3': 5,
      '4': 1,
      '5': 8,
      '10': 'recentAuthentication'
    },
    {'1': 'csrf_token', '3': 6, '4': 1, '5': 9, '10': 'csrfToken'},
  ],
};

/// Descriptor for `GetCurrentPrincipalResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getCurrentPrincipalResponseDescriptor = $convert.base64Decode(
    'ChtHZXRDdXJyZW50UHJpbmNpcGFsUmVzcG9uc2USNgoIaWRlbnRpdHkYASABKAsyGi5ncmFwaC'
    '52MS5TZWN1cml0eUlkZW50aXR5UghpZGVudGl0eRIsCgVyb2xlcxgCIAMoCzIWLmdyYXBoLnYx'
    'LlNlY3VyaXR5Um9sZVIFcm9sZXMSMwoHdmVyc2lvbhgDIAEoCzIZLmdyYXBoLnYxLlNlY3VyaX'
    'R5VmVyc2lvblIHdmVyc2lvbhI5CgpleHBpcmVzX2F0GAQgASgLMhouZ29vZ2xlLnByb3RvYnVm'
    'LlRpbWVzdGFtcFIJZXhwaXJlc0F0EjMKFXJlY2VudF9hdXRoZW50aWNhdGlvbhgFIAEoCFIUcm'
    'VjZW50QXV0aGVudGljYXRpb24SHQoKY3NyZl90b2tlbhgGIAEoCVIJY3NyZlRva2Vu');

@$core.Deprecated('Use browserSessionDescriptor instead')
const BrowserSession$json = {
  '1': 'BrowserSession',
  '2': [
    {
      '1': 'mode',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.AuthMode',
      '10': 'mode'
    },
    {
      '1': 'principal',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.GetCurrentPrincipalResponse',
      '10': 'principal'
    },
  ],
};

/// Descriptor for `BrowserSession`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List browserSessionDescriptor = $convert.base64Decode(
    'Cg5Ccm93c2VyU2Vzc2lvbhImCgRtb2RlGAEgASgOMhIuZ3JhcGgudjEuQXV0aE1vZGVSBG1vZG'
    'USQwoJcHJpbmNpcGFsGAIgASgLMiUuZ3JhcGgudjEuR2V0Q3VycmVudFByaW5jaXBhbFJlc3Bv'
    'bnNlUglwcmluY2lwYWw=');

@$core.Deprecated('Use sessionRevocationDescriptor instead')
const SessionRevocation$json = {
  '1': 'SessionRevocation',
  '2': [
    {
      '1': 'version',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {
      '1': 'enforcement',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityEnforcementState',
      '10': 'enforcement'
    },
  ],
};

/// Descriptor for `SessionRevocation`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List sessionRevocationDescriptor = $convert.base64Decode(
    'ChFTZXNzaW9uUmV2b2NhdGlvbhIzCgd2ZXJzaW9uGAEgASgLMhkuZ3JhcGgudjEuU2VjdXJpdH'
    'lWZXJzaW9uUgd2ZXJzaW9uEkQKC2VuZm9yY2VtZW50GAIgASgOMiIuZ3JhcGgudjEuU2VjdXJp'
    'dHlFbmZvcmNlbWVudFN0YXRlUgtlbmZvcmNlbWVudA==');

@$core.Deprecated('Use listIssuersRequestDescriptor instead')
const ListIssuersRequest$json = {
  '1': 'ListIssuersRequest',
  '2': [
    {'1': 'limit', '3': 1, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'cursor', '3': 2, '4': 1, '5': 9, '10': 'cursor'},
    {'1': 'exact', '3': 3, '4': 1, '5': 9, '10': 'exact'},
  ],
};

/// Descriptor for `ListIssuersRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listIssuersRequestDescriptor = $convert.base64Decode(
    'ChJMaXN0SXNzdWVyc1JlcXVlc3QSFAoFbGltaXQYASABKA1SBWxpbWl0EhYKBmN1cnNvchgCIA'
    'EoCVIGY3Vyc29yEhQKBWV4YWN0GAMgASgJUgVleGFjdA==');

@$core.Deprecated('Use listRolesRequestDescriptor instead')
const ListRolesRequest$json = {
  '1': 'ListRolesRequest',
  '2': [
    {'1': 'limit', '3': 1, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'cursor', '3': 2, '4': 1, '5': 9, '10': 'cursor'},
    {'1': 'exact', '3': 3, '4': 1, '5': 9, '10': 'exact'},
  ],
};

/// Descriptor for `ListRolesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listRolesRequestDescriptor = $convert.base64Decode(
    'ChBMaXN0Um9sZXNSZXF1ZXN0EhQKBWxpbWl0GAEgASgNUgVsaW1pdBIWCgZjdXJzb3IYAiABKA'
    'lSBmN1cnNvchIUCgVleGFjdBgDIAEoCVIFZXhhY3Q=');

@$core.Deprecated('Use listUsersRequestDescriptor instead')
const ListUsersRequest$json = {
  '1': 'ListUsersRequest',
  '2': [
    {'1': 'limit', '3': 1, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'cursor', '3': 2, '4': 1, '5': 9, '10': 'cursor'},
    {'1': 'exact', '3': 3, '4': 1, '5': 9, '10': 'exact'},
  ],
};

/// Descriptor for `ListUsersRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listUsersRequestDescriptor = $convert.base64Decode(
    'ChBMaXN0VXNlcnNSZXF1ZXN0EhQKBWxpbWl0GAEgASgNUgVsaW1pdBIWCgZjdXJzb3IYAiABKA'
    'lSBmN1cnNvchIUCgVleGFjdBgDIAEoCVIFZXhhY3Q=');

@$core.Deprecated('Use listSecurityAuditRequestDescriptor instead')
const ListSecurityAuditRequest$json = {
  '1': 'ListSecurityAuditRequest',
  '2': [
    {'1': 'limit', '3': 1, '4': 1, '5': 13, '10': 'limit'},
    {'1': 'cursor', '3': 2, '4': 1, '5': 9, '10': 'cursor'},
    {'1': 'exact', '3': 3, '4': 1, '5': 9, '10': 'exact'},
  ],
};

/// Descriptor for `ListSecurityAuditRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listSecurityAuditRequestDescriptor =
    $convert.base64Decode(
        'ChhMaXN0U2VjdXJpdHlBdWRpdFJlcXVlc3QSFAoFbGltaXQYASABKA1SBWxpbWl0EhYKBmN1cn'
        'NvchgCIAEoCVIGY3Vyc29yEhQKBWV4YWN0GAMgASgJUgVleGFjdA==');

@$core.Deprecated('Use getIssuerRequestDescriptor instead')
const GetIssuerRequest$json = {
  '1': 'GetIssuerRequest',
  '2': [
    {'1': 'issuer', '3': 1, '4': 1, '5': 9, '10': 'issuer'},
  ],
};

/// Descriptor for `GetIssuerRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getIssuerRequestDescriptor = $convert
    .base64Decode('ChBHZXRJc3N1ZXJSZXF1ZXN0EhYKBmlzc3VlchgBIAEoCVIGaXNzdWVy');

@$core.Deprecated('Use getIssuerResponseDescriptor instead')
const GetIssuerResponse$json = {
  '1': 'GetIssuerResponse',
  '2': [
    {
      '1': 'issuer',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIssuer',
      '10': 'issuer'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
  ],
};

/// Descriptor for `GetIssuerResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getIssuerResponseDescriptor = $convert.base64Decode(
    'ChFHZXRJc3N1ZXJSZXNwb25zZRIwCgZpc3N1ZXIYASABKAsyGC5ncmFwaC52MS5TZWN1cml0eU'
    'lzc3VlclIGaXNzdWVyEjMKB3ZlcnNpb24YAiABKAsyGS5ncmFwaC52MS5TZWN1cml0eVZlcnNp'
    'b25SB3ZlcnNpb24=');

@$core.Deprecated('Use getRoleRequestDescriptor instead')
const GetRoleRequest$json = {
  '1': 'GetRoleRequest',
  '2': [
    {'1': 'id', '3': 1, '4': 1, '5': 9, '10': 'id'},
  ],
};

/// Descriptor for `GetRoleRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getRoleRequestDescriptor =
    $convert.base64Decode('Cg5HZXRSb2xlUmVxdWVzdBIOCgJpZBgBIAEoCVICaWQ=');

@$core.Deprecated('Use getRoleResponseDescriptor instead')
const GetRoleResponse$json = {
  '1': 'GetRoleResponse',
  '2': [
    {
      '1': 'role',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityRole',
      '10': 'role'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
  ],
};

/// Descriptor for `GetRoleResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getRoleResponseDescriptor = $convert.base64Decode(
    'Cg9HZXRSb2xlUmVzcG9uc2USKgoEcm9sZRgBIAEoCzIWLmdyYXBoLnYxLlNlY3VyaXR5Um9sZV'
    'IEcm9sZRIzCgd2ZXJzaW9uGAIgASgLMhkuZ3JhcGgudjEuU2VjdXJpdHlWZXJzaW9uUgd2ZXJz'
    'aW9u');

@$core.Deprecated('Use getUserRequestDescriptor instead')
const GetUserRequest$json = {
  '1': 'GetUserRequest',
  '2': [
    {
      '1': 'identity',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
  ],
};

/// Descriptor for `GetUserRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getUserRequestDescriptor = $convert.base64Decode(
    'Cg5HZXRVc2VyUmVxdWVzdBI2CghpZGVudGl0eRgBIAEoCzIaLmdyYXBoLnYxLlNlY3VyaXR5SW'
    'RlbnRpdHlSCGlkZW50aXR5');

@$core.Deprecated('Use getUserResponseDescriptor instead')
const GetUserResponse$json = {
  '1': 'GetUserResponse',
  '2': [
    {
      '1': 'user',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityUser',
      '10': 'user'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
  ],
};

/// Descriptor for `GetUserResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getUserResponseDescriptor = $convert.base64Decode(
    'Cg9HZXRVc2VyUmVzcG9uc2USKgoEdXNlchgBIAEoCzIWLmdyYXBoLnYxLlNlY3VyaXR5VXNlcl'
    'IEdXNlchIzCgd2ZXJzaW9uGAIgASgLMhkuZ3JhcGgudjEuU2VjdXJpdHlWZXJzaW9uUgd2ZXJz'
    'aW9u');

@$core.Deprecated('Use listIssuersResponseDescriptor instead')
const ListIssuersResponse$json = {
  '1': 'ListIssuersResponse',
  '2': [
    {
      '1': 'issuers',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityIssuer',
      '10': 'issuers'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {'1': 'next_cursor', '3': 3, '4': 1, '5': 9, '10': 'nextCursor'},
  ],
};

/// Descriptor for `ListIssuersResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listIssuersResponseDescriptor = $convert.base64Decode(
    'ChNMaXN0SXNzdWVyc1Jlc3BvbnNlEjIKB2lzc3VlcnMYASADKAsyGC5ncmFwaC52MS5TZWN1cm'
    'l0eUlzc3VlclIHaXNzdWVycxIzCgd2ZXJzaW9uGAIgASgLMhkuZ3JhcGgudjEuU2VjdXJpdHlW'
    'ZXJzaW9uUgd2ZXJzaW9uEh8KC25leHRfY3Vyc29yGAMgASgJUgpuZXh0Q3Vyc29y');

@$core.Deprecated('Use listRolesResponseDescriptor instead')
const ListRolesResponse$json = {
  '1': 'ListRolesResponse',
  '2': [
    {
      '1': 'roles',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRole',
      '10': 'roles'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {'1': 'next_cursor', '3': 3, '4': 1, '5': 9, '10': 'nextCursor'},
  ],
};

/// Descriptor for `ListRolesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listRolesResponseDescriptor = $convert.base64Decode(
    'ChFMaXN0Um9sZXNSZXNwb25zZRIsCgVyb2xlcxgBIAMoCzIWLmdyYXBoLnYxLlNlY3VyaXR5Um'
    '9sZVIFcm9sZXMSMwoHdmVyc2lvbhgCIAEoCzIZLmdyYXBoLnYxLlNlY3VyaXR5VmVyc2lvblIH'
    'dmVyc2lvbhIfCgtuZXh0X2N1cnNvchgDIAEoCVIKbmV4dEN1cnNvcg==');

@$core.Deprecated('Use listUsersResponseDescriptor instead')
const ListUsersResponse$json = {
  '1': 'ListUsersResponse',
  '2': [
    {
      '1': 'users',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityUser',
      '10': 'users'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {'1': 'next_cursor', '3': 3, '4': 1, '5': 9, '10': 'nextCursor'},
  ],
};

/// Descriptor for `ListUsersResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listUsersResponseDescriptor = $convert.base64Decode(
    'ChFMaXN0VXNlcnNSZXNwb25zZRIsCgV1c2VycxgBIAMoCzIWLmdyYXBoLnYxLlNlY3VyaXR5VX'
    'NlclIFdXNlcnMSMwoHdmVyc2lvbhgCIAEoCzIZLmdyYXBoLnYxLlNlY3VyaXR5VmVyc2lvblIH'
    'dmVyc2lvbhIfCgtuZXh0X2N1cnNvchgDIAEoCVIKbmV4dEN1cnNvcg==');

@$core.Deprecated('Use listRoleAssignmentsRequestDescriptor instead')
const ListRoleAssignmentsRequest$json = {
  '1': 'ListRoleAssignmentsRequest',
  '2': [
    {
      '1': 'identity',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
  ],
};

/// Descriptor for `ListRoleAssignmentsRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listRoleAssignmentsRequestDescriptor =
    $convert.base64Decode(
        'ChpMaXN0Um9sZUFzc2lnbm1lbnRzUmVxdWVzdBI2CghpZGVudGl0eRgBIAEoCzIaLmdyYXBoLn'
        'YxLlNlY3VyaXR5SWRlbnRpdHlSCGlkZW50aXR5');

@$core.Deprecated('Use listRoleAssignmentsResponseDescriptor instead')
const ListRoleAssignmentsResponse$json = {
  '1': 'ListRoleAssignmentsResponse',
  '2': [
    {
      '1': 'assignments',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRoleAssignment',
      '10': 'assignments'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
  ],
};

/// Descriptor for `ListRoleAssignmentsResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listRoleAssignmentsResponseDescriptor =
    $convert.base64Decode(
        'ChtMaXN0Um9sZUFzc2lnbm1lbnRzUmVzcG9uc2USQgoLYXNzaWdubWVudHMYASADKAsyIC5ncm'
        'FwaC52MS5TZWN1cml0eVJvbGVBc3NpZ25tZW50Ugthc3NpZ25tZW50cxIzCgd2ZXJzaW9uGAIg'
        'ASgLMhkuZ3JhcGgudjEuU2VjdXJpdHlWZXJzaW9uUgd2ZXJzaW9u');

@$core.Deprecated('Use securityAuditRecordDescriptor instead')
const SecurityAuditRecord$json = {
  '1': 'SecurityAuditRecord',
  '2': [
    {'1': 'revision', '3': 1, '4': 1, '5': 4, '10': 'revision'},
    {'1': 'change_id', '3': 2, '4': 1, '5': 9, '10': 'changeId'},
    {'1': 'intent_digest', '3': 3, '4': 1, '5': 9, '10': 'intentDigest'},
    {'1': 'actor_digest', '3': 4, '4': 1, '5': 9, '10': 'actorDigest'},
    {
      '1': 'occurred_at',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'occurredAt'
    },
    {'1': 'operation', '3': 6, '4': 1, '5': 9, '10': 'operation'},
    {'1': 'target_digests', '3': 7, '4': 3, '5': 9, '10': 'targetDigests'},
    {
      '1': 'additional_targets',
      '3': 8,
      '4': 1,
      '5': 13,
      '10': 'additionalTargets'
    },
    {'1': 'outcome', '3': 9, '4': 1, '5': 9, '10': 'outcome'},
  ],
};

/// Descriptor for `SecurityAuditRecord`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityAuditRecordDescriptor = $convert.base64Decode(
    'ChNTZWN1cml0eUF1ZGl0UmVjb3JkEhoKCHJldmlzaW9uGAEgASgEUghyZXZpc2lvbhIbCgljaG'
    'FuZ2VfaWQYAiABKAlSCGNoYW5nZUlkEiMKDWludGVudF9kaWdlc3QYAyABKAlSDGludGVudERp'
    'Z2VzdBIhCgxhY3Rvcl9kaWdlc3QYBCABKAlSC2FjdG9yRGlnZXN0EjsKC29jY3VycmVkX2F0GA'
    'UgASgLMhouZ29vZ2xlLnByb3RvYnVmLlRpbWVzdGFtcFIKb2NjdXJyZWRBdBIcCglvcGVyYXRp'
    'b24YBiABKAlSCW9wZXJhdGlvbhIlCg50YXJnZXRfZGlnZXN0cxgHIAMoCVINdGFyZ2V0RGlnZX'
    'N0cxItChJhZGRpdGlvbmFsX3RhcmdldHMYCCABKA1SEWFkZGl0aW9uYWxUYXJnZXRzEhgKB291'
    'dGNvbWUYCSABKAlSB291dGNvbWU=');

@$core.Deprecated('Use listSecurityAuditResponseDescriptor instead')
const ListSecurityAuditResponse$json = {
  '1': 'ListSecurityAuditResponse',
  '2': [
    {
      '1': 'records',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityAuditRecord',
      '10': 'records'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {'1': 'next_cursor', '3': 3, '4': 1, '5': 9, '10': 'nextCursor'},
  ],
};

/// Descriptor for `ListSecurityAuditResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List listSecurityAuditResponseDescriptor = $convert.base64Decode(
    'ChlMaXN0U2VjdXJpdHlBdWRpdFJlc3BvbnNlEjcKB3JlY29yZHMYASADKAsyHS5ncmFwaC52MS'
    '5TZWN1cml0eUF1ZGl0UmVjb3JkUgdyZWNvcmRzEjMKB3ZlcnNpb24YAiABKAsyGS5ncmFwaC52'
    'MS5TZWN1cml0eVZlcnNpb25SB3ZlcnNpb24SHwoLbmV4dF9jdXJzb3IYAyABKAlSCm5leHRDdX'
    'Jzb3I=');

@$core.Deprecated('Use getRoleTemplatesRequestDescriptor instead')
const GetRoleTemplatesRequest$json = {
  '1': 'GetRoleTemplatesRequest',
  '2': [
    {'1': 'prefix', '3': 1, '4': 1, '5': 9, '10': 'prefix'},
  ],
};

/// Descriptor for `GetRoleTemplatesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getRoleTemplatesRequestDescriptor =
    $convert.base64Decode(
        'ChdHZXRSb2xlVGVtcGxhdGVzUmVxdWVzdBIWCgZwcmVmaXgYASABKAlSBnByZWZpeA==');

@$core.Deprecated('Use getRoleTemplatesResponseDescriptor instead')
const GetRoleTemplatesResponse$json = {
  '1': 'GetRoleTemplatesResponse',
  '2': [
    {
      '1': 'roles',
      '3': 1,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRole',
      '10': 'roles'
    },
    {
      '1': 'version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
  ],
};

/// Descriptor for `GetRoleTemplatesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getRoleTemplatesResponseDescriptor = $convert.base64Decode(
    'ChhHZXRSb2xlVGVtcGxhdGVzUmVzcG9uc2USLAoFcm9sZXMYASADKAsyFi5ncmFwaC52MS5TZW'
    'N1cml0eVJvbGVSBXJvbGVzEjMKB3ZlcnNpb24YAiABKAsyGS5ncmFwaC52MS5TZWN1cml0eVZl'
    'cnNpb25SB3ZlcnNpb24=');

@$core.Deprecated('Use securityEdgeIdentityDescriptor instead')
const SecurityEdgeIdentity$json = {
  '1': 'SecurityEdgeIdentity',
  '2': [
    {'1': 'tail', '3': 1, '4': 1, '5': 9, '10': 'tail'},
    {'1': 'head', '3': 2, '4': 1, '5': 9, '10': 'head'},
  ],
};

/// Descriptor for `SecurityEdgeIdentity`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityEdgeIdentityDescriptor = $convert.base64Decode(
    'ChRTZWN1cml0eUVkZ2VJZGVudGl0eRISCgR0YWlsGAEgASgJUgR0YWlsEhIKBGhlYWQYAiABKA'
    'lSBGhlYWQ=');

@$core.Deprecated('Use explainAccessRequestDescriptor instead')
const ExplainAccessRequest$json = {
  '1': 'ExplainAccessRequest',
  '2': [
    {
      '1': 'identity',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
    {
      '1': 'action',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityAction',
      '10': 'action'
    },
    {
      '1': 'logical_key',
      '3': 3,
      '4': 1,
      '5': 9,
      '9': 0,
      '10': 'logicalKey',
      '17': true
    },
    {
      '1': 'edge',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityEdgeIdentity',
      '10': 'edge'
    },
  ],
  '8': [
    {'1': '_logical_key'},
  ],
};

/// Descriptor for `ExplainAccessRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List explainAccessRequestDescriptor = $convert.base64Decode(
    'ChRFeHBsYWluQWNjZXNzUmVxdWVzdBI2CghpZGVudGl0eRgBIAEoCzIaLmdyYXBoLnYxLlNlY3'
    'VyaXR5SWRlbnRpdHlSCGlkZW50aXR5EjAKBmFjdGlvbhgCIAEoDjIYLmdyYXBoLnYxLlNlY3Vy'
    'aXR5QWN0aW9uUgZhY3Rpb24SJAoLbG9naWNhbF9rZXkYAyABKAlIAFIKbG9naWNhbEtleYgBAR'
    'IyCgRlZGdlGAQgASgLMh4uZ3JhcGgudjEuU2VjdXJpdHlFZGdlSWRlbnRpdHlSBGVkZ2VCDgoM'
    'X2xvZ2ljYWxfa2V5');

@$core.Deprecated('Use securityRuleMatchDescriptor instead')
const SecurityRuleMatch$json = {
  '1': 'SecurityRuleMatch',
  '2': [
    {'1': 'role_id', '3': 1, '4': 1, '5': 9, '10': 'roleId'},
    {'1': 'rule_id', '3': 2, '4': 1, '5': 9, '10': 'ruleId'},
    {
      '1': 'effect',
      '3': 3,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityEffect',
      '10': 'effect'
    },
    {
      '1': 'action',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityAction',
      '10': 'action'
    },
    {'1': 'endpoint', '3': 5, '4': 1, '5': 9, '10': 'endpoint'},
  ],
};

/// Descriptor for `SecurityRuleMatch`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityRuleMatchDescriptor = $convert.base64Decode(
    'ChFTZWN1cml0eVJ1bGVNYXRjaBIXCgdyb2xlX2lkGAEgASgJUgZyb2xlSWQSFwoHcnVsZV9pZB'
    'gCIAEoCVIGcnVsZUlkEjAKBmVmZmVjdBgDIAEoDjIYLmdyYXBoLnYxLlNlY3VyaXR5RWZmZWN0'
    'UgZlZmZlY3QSMAoGYWN0aW9uGAQgASgOMhguZ3JhcGgudjEuU2VjdXJpdHlBY3Rpb25SBmFjdG'
    'lvbhIaCghlbmRwb2ludBgFIAEoCVIIZW5kcG9pbnQ=');

@$core.Deprecated('Use explainAccessResponseDescriptor instead')
const ExplainAccessResponse$json = {
  '1': 'ExplainAccessResponse',
  '2': [
    {'1': 'allowed', '3': 1, '4': 1, '5': 8, '10': 'allowed'},
    {
      '1': 'matches',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRuleMatch',
      '10': 'matches'
    },
    {
      '1': 'version',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
  ],
};

/// Descriptor for `ExplainAccessResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List explainAccessResponseDescriptor = $convert.base64Decode(
    'ChVFeHBsYWluQWNjZXNzUmVzcG9uc2USGAoHYWxsb3dlZBgBIAEoCFIHYWxsb3dlZBI1CgdtYX'
    'RjaGVzGAIgAygLMhsuZ3JhcGgudjEuU2VjdXJpdHlSdWxlTWF0Y2hSB21hdGNoZXMSMwoHdmVy'
    'c2lvbhgDIAEoCzIZLmdyYXBoLnYxLlNlY3VyaXR5VmVyc2lvblIHdmVyc2lvbg==');

@$core.Deprecated('Use validateIssuerRequestDescriptor instead')
const ValidateIssuerRequest$json = {
  '1': 'ValidateIssuerRequest',
  '2': [
    {
      '1': 'issuer',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIssuer',
      '10': 'issuer'
    },
  ],
};

/// Descriptor for `ValidateIssuerRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List validateIssuerRequestDescriptor = $convert.base64Decode(
    'ChVWYWxpZGF0ZUlzc3VlclJlcXVlc3QSMAoGaXNzdWVyGAEgASgLMhguZ3JhcGgudjEuU2VjdX'
    'JpdHlJc3N1ZXJSBmlzc3Vlcg==');

@$core.Deprecated('Use validateIssuerResponseDescriptor instead')
const ValidateIssuerResponse$json = {
  '1': 'ValidateIssuerResponse',
  '2': [
    {'1': 'valid', '3': 1, '4': 1, '5': 8, '10': 'valid'},
  ],
};

/// Descriptor for `ValidateIssuerResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List validateIssuerResponseDescriptor =
    $convert.base64Decode(
        'ChZWYWxpZGF0ZUlzc3VlclJlc3BvbnNlEhQKBXZhbGlkGAEgASgIUgV2YWxpZA==');

@$core.Deprecated('Use securityUserStateChangeDescriptor instead')
const SecurityUserStateChange$json = {
  '1': 'SecurityUserStateChange',
  '2': [
    {
      '1': 'identity',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
    {
      '1': 'state',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityPrincipalState',
      '10': 'state'
    },
  ],
};

/// Descriptor for `SecurityUserStateChange`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityUserStateChangeDescriptor = $convert.base64Decode(
    'ChdTZWN1cml0eVVzZXJTdGF0ZUNoYW5nZRI2CghpZGVudGl0eRgBIAEoCzIaLmdyYXBoLnYxLl'
    'NlY3VyaXR5SWRlbnRpdHlSCGlkZW50aXR5EjYKBXN0YXRlGAIgASgOMiAuZ3JhcGgudjEuU2Vj'
    'dXJpdHlQcmluY2lwYWxTdGF0ZVIFc3RhdGU=');

@$core.Deprecated('Use securityChangeDescriptor instead')
const SecurityChange$json = {
  '1': 'SecurityChange',
  '2': [
    {
      '1': 'put_issuer',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIssuer',
      '9': 0,
      '10': 'putIssuer'
    },
    {
      '1': 'disable_issuer',
      '3': 2,
      '4': 1,
      '5': 9,
      '9': 0,
      '10': 'disableIssuer'
    },
    {
      '1': 'delete_issuer',
      '3': 3,
      '4': 1,
      '5': 9,
      '9': 0,
      '10': 'deleteIssuer'
    },
    {
      '1': 'put_role',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityRole',
      '9': 0,
      '10': 'putRole'
    },
    {'1': 'delete_role', '3': 5, '4': 1, '5': 9, '9': 0, '10': 'deleteRole'},
    {
      '1': 'put_user',
      '3': 6,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityUserStateChange',
      '9': 0,
      '10': 'putUser'
    },
    {
      '1': 'delete_user',
      '3': 7,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '9': 0,
      '10': 'deleteUser'
    },
    {
      '1': 'put_assignment',
      '3': 8,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityRoleAssignment',
      '9': 0,
      '10': 'putAssignment'
    },
    {
      '1': 'delete_assignment',
      '3': 9,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityRoleAssignment',
      '9': 0,
      '10': 'deleteAssignment'
    },
    {
      '1': 'revoke_user_sessions',
      '3': 10,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '9': 0,
      '10': 'revokeUserSessions'
    },
  ],
  '8': [
    {'1': 'operation'},
  ],
};

/// Descriptor for `SecurityChange`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityChangeDescriptor = $convert.base64Decode(
    'Cg5TZWN1cml0eUNoYW5nZRI5CgpwdXRfaXNzdWVyGAEgASgLMhguZ3JhcGgudjEuU2VjdXJpdH'
    'lJc3N1ZXJIAFIJcHV0SXNzdWVyEicKDmRpc2FibGVfaXNzdWVyGAIgASgJSABSDWRpc2FibGVJ'
    'c3N1ZXISJQoNZGVsZXRlX2lzc3VlchgDIAEoCUgAUgxkZWxldGVJc3N1ZXISMwoIcHV0X3JvbG'
    'UYBCABKAsyFi5ncmFwaC52MS5TZWN1cml0eVJvbGVIAFIHcHV0Um9sZRIhCgtkZWxldGVfcm9s'
    'ZRgFIAEoCUgAUgpkZWxldGVSb2xlEj4KCHB1dF91c2VyGAYgASgLMiEuZ3JhcGgudjEuU2VjdX'
    'JpdHlVc2VyU3RhdGVDaGFuZ2VIAFIHcHV0VXNlchI9CgtkZWxldGVfdXNlchgHIAEoCzIaLmdy'
    'YXBoLnYxLlNlY3VyaXR5SWRlbnRpdHlIAFIKZGVsZXRlVXNlchJJCg5wdXRfYXNzaWdubWVudB'
    'gIIAEoCzIgLmdyYXBoLnYxLlNlY3VyaXR5Um9sZUFzc2lnbm1lbnRIAFINcHV0QXNzaWdubWVu'
    'dBJPChFkZWxldGVfYXNzaWdubWVudBgJIAEoCzIgLmdyYXBoLnYxLlNlY3VyaXR5Um9sZUFzc2'
    'lnbm1lbnRIAFIQZGVsZXRlQXNzaWdubWVudBJOChRyZXZva2VfdXNlcl9zZXNzaW9ucxgKIAEo'
    'CzIaLmdyYXBoLnYxLlNlY3VyaXR5SWRlbnRpdHlIAFIScmV2b2tlVXNlclNlc3Npb25zQgsKCW'
    '9wZXJhdGlvbg==');

@$core.Deprecated('Use applySecurityChangesRequestDescriptor instead')
const ApplySecurityChangesRequest$json = {
  '1': 'ApplySecurityChangesRequest',
  '2': [
    {
      '1': 'expected_revision',
      '3': 1,
      '4': 1,
      '5': 4,
      '10': 'expectedRevision'
    },
    {'1': 'change_id', '3': 2, '4': 1, '5': 12, '10': 'changeId'},
    {
      '1': 'changes',
      '3': 3,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityChange',
      '10': 'changes'
    },
    {
      '1': 'authorization_proof',
      '3': 4,
      '4': 1,
      '5': 12,
      '10': 'authorizationProof'
    },
  ],
};

/// Descriptor for `ApplySecurityChangesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applySecurityChangesRequestDescriptor = $convert.base64Decode(
    'ChtBcHBseVNlY3VyaXR5Q2hhbmdlc1JlcXVlc3QSKwoRZXhwZWN0ZWRfcmV2aXNpb24YASABKA'
    'RSEGV4cGVjdGVkUmV2aXNpb24SGwoJY2hhbmdlX2lkGAIgASgMUghjaGFuZ2VJZBIyCgdjaGFu'
    'Z2VzGAMgAygLMhguZ3JhcGgudjEuU2VjdXJpdHlDaGFuZ2VSB2NoYW5nZXMSLwoTYXV0aG9yaX'
    'phdGlvbl9wcm9vZhgEIAEoDFISYXV0aG9yaXphdGlvblByb29m');

@$core.Deprecated('Use applySecurityChangesResponseDescriptor instead')
const ApplySecurityChangesResponse$json = {
  '1': 'ApplySecurityChangesResponse',
  '2': [
    {
      '1': 'version',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {'1': 'applied', '3': 2, '4': 3, '5': 8, '10': 'applied'},
    {'1': 'replayed', '3': 3, '4': 1, '5': 8, '10': 'replayed'},
    {
      '1': 'enforcement',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityEnforcementState',
      '10': 'enforcement'
    },
  ],
};

/// Descriptor for `ApplySecurityChangesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applySecurityChangesResponseDescriptor = $convert.base64Decode(
    'ChxBcHBseVNlY3VyaXR5Q2hhbmdlc1Jlc3BvbnNlEjMKB3ZlcnNpb24YASABKAsyGS5ncmFwaC'
    '52MS5TZWN1cml0eVZlcnNpb25SB3ZlcnNpb24SGAoHYXBwbGllZBgCIAMoCFIHYXBwbGllZBIa'
    'CghyZXBsYXllZBgDIAEoCFIIcmVwbGF5ZWQSRAoLZW5mb3JjZW1lbnQYBCABKA4yIi5ncmFwaC'
    '52MS5TZWN1cml0eUVuZm9yY2VtZW50U3RhdGVSC2VuZm9yY2VtZW50');

@$core.Deprecated('Use applySecurityChangeRequestDescriptor instead')
const ApplySecurityChangeRequest$json = {
  '1': 'ApplySecurityChangeRequest',
  '2': [
    {
      '1': 'expected_revision',
      '3': 1,
      '4': 1,
      '5': 4,
      '10': 'expectedRevision'
    },
    {'1': 'change_id', '3': 2, '4': 1, '5': 12, '10': 'changeId'},
    {
      '1': 'change',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityChange',
      '10': 'change'
    },
    {
      '1': 'authorization_proof',
      '3': 4,
      '4': 1,
      '5': 12,
      '10': 'authorizationProof'
    },
  ],
};

/// Descriptor for `ApplySecurityChangeRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applySecurityChangeRequestDescriptor = $convert.base64Decode(
    'ChpBcHBseVNlY3VyaXR5Q2hhbmdlUmVxdWVzdBIrChFleHBlY3RlZF9yZXZpc2lvbhgBIAEoBF'
    'IQZXhwZWN0ZWRSZXZpc2lvbhIbCgljaGFuZ2VfaWQYAiABKAxSCGNoYW5nZUlkEjAKBmNoYW5n'
    'ZRgDIAEoCzIYLmdyYXBoLnYxLlNlY3VyaXR5Q2hhbmdlUgZjaGFuZ2USLwoTYXV0aG9yaXphdG'
    'lvbl9wcm9vZhgEIAEoDFISYXV0aG9yaXphdGlvblByb29m');

@$core.Deprecated('Use securityChangeReviewDescriptor instead')
const SecurityChangeReview$json = {
  '1': 'SecurityChangeReview',
  '2': [
    {
      '1': 'expected_version',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'expectedVersion'
    },
    {'1': 'change_id', '3': 2, '4': 1, '5': 12, '10': 'changeId'},
    {
      '1': 'changes',
      '3': 3,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityChange',
      '10': 'changes'
    },
  ],
};

/// Descriptor for `SecurityChangeReview`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityChangeReviewDescriptor = $convert.base64Decode(
    'ChRTZWN1cml0eUNoYW5nZVJldmlldxJEChBleHBlY3RlZF92ZXJzaW9uGAEgASgLMhkuZ3JhcG'
    'gudjEuU2VjdXJpdHlWZXJzaW9uUg9leHBlY3RlZFZlcnNpb24SGwoJY2hhbmdlX2lkGAIgASgM'
    'UghjaGFuZ2VJZBIyCgdjaGFuZ2VzGAMgAygLMhguZ3JhcGgudjEuU2VjdXJpdHlDaGFuZ2VSB2'
    'NoYW5nZXM=');

@$core
    .Deprecated('Use securityOperationAuthorizationRequiredDescriptor instead')
const SecurityOperationAuthorizationRequired$json = {
  '1': 'SecurityOperationAuthorizationRequired',
  '2': [
    {'1': 'change_id', '3': 1, '4': 1, '5': 12, '10': 'changeId'},
    {
      '1': 'expected_version',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'expectedVersion'
    },
    {'1': 'intent_digest', '3': 3, '4': 1, '5': 12, '10': 'intentDigest'},
  ],
};

/// Descriptor for `SecurityOperationAuthorizationRequired`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List securityOperationAuthorizationRequiredDescriptor =
    $convert.base64Decode(
        'CiZTZWN1cml0eU9wZXJhdGlvbkF1dGhvcml6YXRpb25SZXF1aXJlZBIbCgljaGFuZ2VfaWQYAS'
        'ABKAxSCGNoYW5nZUlkEkQKEGV4cGVjdGVkX3ZlcnNpb24YAiABKAsyGS5ncmFwaC52MS5TZWN1'
        'cml0eVZlcnNpb25SD2V4cGVjdGVkVmVyc2lvbhIjCg1pbnRlbnRfZGlnZXN0GAMgASgMUgxpbn'
        'RlbnREaWdlc3Q=');

@$core.Deprecated('Use prepareSecurityChangesRequestDescriptor instead')
const PrepareSecurityChangesRequest$json = {
  '1': 'PrepareSecurityChangesRequest',
  '2': [
    {
      '1': 'review',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityChangeReview',
      '10': 'review'
    },
  ],
};

/// Descriptor for `PrepareSecurityChangesRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List prepareSecurityChangesRequestDescriptor =
    $convert.base64Decode(
        'Ch1QcmVwYXJlU2VjdXJpdHlDaGFuZ2VzUmVxdWVzdBI2CgZyZXZpZXcYASABKAsyHi5ncmFwaC'
        '52MS5TZWN1cml0eUNoYW5nZVJldmlld1IGcmV2aWV3');

@$core.Deprecated('Use prepareSecurityChangesResponseDescriptor instead')
const PrepareSecurityChangesResponse$json = {
  '1': 'PrepareSecurityChangesResponse',
  '2': [
    {
      '1': 'expected_version',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'expectedVersion'
    },
    {'1': 'change_id', '3': 2, '4': 1, '5': 12, '10': 'changeId'},
    {'1': 'intent_digest', '3': 3, '4': 1, '5': 12, '10': 'intentDigest'},
    {
      '1': 'requirement',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityAuthorizationRequirement',
      '10': 'requirement'
    },
    {
      '1': 'retained_commit',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.GetSecurityChangeStatusResponse',
      '10': 'retainedCommit'
    },
  ],
};

/// Descriptor for `PrepareSecurityChangesResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List prepareSecurityChangesResponseDescriptor = $convert.base64Decode(
    'Ch5QcmVwYXJlU2VjdXJpdHlDaGFuZ2VzUmVzcG9uc2USRAoQZXhwZWN0ZWRfdmVyc2lvbhgBIA'
    'EoCzIZLmdyYXBoLnYxLlNlY3VyaXR5VmVyc2lvblIPZXhwZWN0ZWRWZXJzaW9uEhsKCWNoYW5n'
    'ZV9pZBgCIAEoDFIIY2hhbmdlSWQSIwoNaW50ZW50X2RpZ2VzdBgDIAEoDFIMaW50ZW50RGlnZX'
    'N0EkwKC3JlcXVpcmVtZW50GAQgASgOMiouZ3JhcGgudjEuU2VjdXJpdHlBdXRob3JpemF0aW9u'
    'UmVxdWlyZW1lbnRSC3JlcXVpcmVtZW50ElIKD3JldGFpbmVkX2NvbW1pdBgFIAEoCzIpLmdyYX'
    'BoLnYxLkdldFNlY3VyaXR5Q2hhbmdlU3RhdHVzUmVzcG9uc2VSDnJldGFpbmVkQ29tbWl0');

@$core
    .Deprecated('Use beginSecurityChangeAuthorizationRequestDescriptor instead')
const BeginSecurityChangeAuthorizationRequest$json = {
  '1': 'BeginSecurityChangeAuthorizationRequest',
  '2': [
    {
      '1': 'review',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityChangeReview',
      '10': 'review'
    },
  ],
};

/// Descriptor for `BeginSecurityChangeAuthorizationRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List beginSecurityChangeAuthorizationRequestDescriptor =
    $convert.base64Decode(
        'CidCZWdpblNlY3VyaXR5Q2hhbmdlQXV0aG9yaXphdGlvblJlcXVlc3QSNgoGcmV2aWV3GAEgAS'
        'gLMh4uZ3JhcGgudjEuU2VjdXJpdHlDaGFuZ2VSZXZpZXdSBnJldmlldw==');

@$core.Deprecated(
    'Use beginSecurityChangeAuthorizationResponseDescriptor instead')
const BeginSecurityChangeAuthorizationResponse$json = {
  '1': 'BeginSecurityChangeAuthorizationResponse',
  '2': [
    {'1': 'authorization_id', '3': 1, '4': 1, '5': 12, '10': 'authorizationId'},
    {'1': 'start_url', '3': 2, '4': 1, '5': 9, '10': 'startUrl'},
    {
      '1': 'expires_at',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiresAt'
    },
  ],
};

/// Descriptor for `BeginSecurityChangeAuthorizationResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List beginSecurityChangeAuthorizationResponseDescriptor =
    $convert.base64Decode(
        'CihCZWdpblNlY3VyaXR5Q2hhbmdlQXV0aG9yaXphdGlvblJlc3BvbnNlEikKEGF1dGhvcml6YX'
        'Rpb25faWQYASABKAxSD2F1dGhvcml6YXRpb25JZBIbCglzdGFydF91cmwYAiABKAlSCHN0YXJ0'
        'VXJsEjkKCmV4cGlyZXNfYXQYAyABKAsyGi5nb29nbGUucHJvdG9idWYuVGltZXN0YW1wUglleH'
        'BpcmVzQXQ=');

@$core.Deprecated('Use getSecurityChangeAuthorizationRequestDescriptor instead')
const GetSecurityChangeAuthorizationRequest$json = {
  '1': 'GetSecurityChangeAuthorizationRequest',
  '2': [
    {'1': 'authorization_id', '3': 1, '4': 1, '5': 12, '10': 'authorizationId'},
  ],
};

/// Descriptor for `GetSecurityChangeAuthorizationRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getSecurityChangeAuthorizationRequestDescriptor =
    $convert.base64Decode(
        'CiVHZXRTZWN1cml0eUNoYW5nZUF1dGhvcml6YXRpb25SZXF1ZXN0EikKEGF1dGhvcml6YXRpb2'
        '5faWQYASABKAxSD2F1dGhvcml6YXRpb25JZA==');

@$core
    .Deprecated('Use getSecurityChangeAuthorizationResponseDescriptor instead')
const GetSecurityChangeAuthorizationResponse$json = {
  '1': 'GetSecurityChangeAuthorizationResponse',
  '2': [
    {'1': 'authorization_id', '3': 1, '4': 1, '5': 12, '10': 'authorizationId'},
    {
      '1': 'state',
      '3': 2,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityAuthorizationState',
      '10': 'state'
    },
    {
      '1': 'authorization_proof',
      '3': 3,
      '4': 1,
      '5': 12,
      '10': 'authorizationProof'
    },
    {
      '1': 'expires_at',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiresAt'
    },
  ],
};

/// Descriptor for `GetSecurityChangeAuthorizationResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getSecurityChangeAuthorizationResponseDescriptor =
    $convert.base64Decode(
        'CiZHZXRTZWN1cml0eUNoYW5nZUF1dGhvcml6YXRpb25SZXNwb25zZRIpChBhdXRob3JpemF0aW'
        '9uX2lkGAEgASgMUg9hdXRob3JpemF0aW9uSWQSOgoFc3RhdGUYAiABKA4yJC5ncmFwaC52MS5T'
        'ZWN1cml0eUF1dGhvcml6YXRpb25TdGF0ZVIFc3RhdGUSLwoTYXV0aG9yaXphdGlvbl9wcm9vZh'
        'gDIAEoDFISYXV0aG9yaXphdGlvblByb29mEjkKCmV4cGlyZXNfYXQYBCABKAsyGi5nb29nbGUu'
        'cHJvdG9idWYuVGltZXN0YW1wUglleHBpcmVzQXQ=');

@$core.Deprecated('Use applySecurityChangeResponseDescriptor instead')
const ApplySecurityChangeResponse$json = {
  '1': 'ApplySecurityChangeResponse',
  '2': [
    {
      '1': 'version',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {'1': 'applied', '3': 2, '4': 1, '5': 8, '10': 'applied'},
    {'1': 'replayed', '3': 3, '4': 1, '5': 8, '10': 'replayed'},
    {
      '1': 'enforcement',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityEnforcementState',
      '10': 'enforcement'
    },
  ],
};

/// Descriptor for `ApplySecurityChangeResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applySecurityChangeResponseDescriptor = $convert.base64Decode(
    'ChtBcHBseVNlY3VyaXR5Q2hhbmdlUmVzcG9uc2USMwoHdmVyc2lvbhgBIAEoCzIZLmdyYXBoLn'
    'YxLlNlY3VyaXR5VmVyc2lvblIHdmVyc2lvbhIYCgdhcHBsaWVkGAIgASgIUgdhcHBsaWVkEhoK'
    'CHJlcGxheWVkGAMgASgIUghyZXBsYXllZBJECgtlbmZvcmNlbWVudBgEIAEoDjIiLmdyYXBoLn'
    'YxLlNlY3VyaXR5RW5mb3JjZW1lbnRTdGF0ZVILZW5mb3JjZW1lbnQ=');

@$core.Deprecated('Use getSecurityChangeStatusRequestDescriptor instead')
const GetSecurityChangeStatusRequest$json = {
  '1': 'GetSecurityChangeStatusRequest',
  '2': [
    {'1': 'change_id', '3': 1, '4': 1, '5': 12, '10': 'changeId'},
  ],
};

/// Descriptor for `GetSecurityChangeStatusRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getSecurityChangeStatusRequestDescriptor =
    $convert.base64Decode(
        'Ch5HZXRTZWN1cml0eUNoYW5nZVN0YXR1c1JlcXVlc3QSGwoJY2hhbmdlX2lkGAEgASgMUghjaG'
        'FuZ2VJZA==');

@$core.Deprecated('Use getSecurityChangeStatusResponseDescriptor instead')
const GetSecurityChangeStatusResponse$json = {
  '1': 'GetSecurityChangeStatusResponse',
  '2': [
    {
      '1': 'version',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {'1': 'change_id', '3': 2, '4': 1, '5': 12, '10': 'changeId'},
    {
      '1': 'enforcement',
      '3': 4,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.SecurityEnforcementState',
      '10': 'enforcement'
    },
  ],
};

/// Descriptor for `GetSecurityChangeStatusResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getSecurityChangeStatusResponseDescriptor =
    $convert.base64Decode(
        'Ch9HZXRTZWN1cml0eUNoYW5nZVN0YXR1c1Jlc3BvbnNlEjMKB3ZlcnNpb24YASABKAsyGS5ncm'
        'FwaC52MS5TZWN1cml0eVZlcnNpb25SB3ZlcnNpb24SGwoJY2hhbmdlX2lkGAIgASgMUghjaGFu'
        'Z2VJZBJECgtlbmZvcmNlbWVudBgEIAEoDjIiLmdyYXBoLnYxLlNlY3VyaXR5RW5mb3JjZW1lbn'
        'RTdGF0ZVILZW5mb3JjZW1lbnQ=');

const $core.Map<$core.String, $core.dynamic> LanternSecurityServiceBase$json = {
  '1': 'LanternSecurityService',
  '2': [
    {
      '1': 'GetAuthCapabilities',
      '2': '.graph.v1.GetAuthCapabilitiesRequest',
      '3': '.graph.v1.GetAuthCapabilitiesResponse'
    },
    {
      '1': 'GetCurrentPrincipal',
      '2': '.graph.v1.GetCurrentPrincipalRequest',
      '3': '.graph.v1.GetCurrentPrincipalResponse'
    },
    {
      '1': 'ListIssuers',
      '2': '.graph.v1.ListIssuersRequest',
      '3': '.graph.v1.ListIssuersResponse'
    },
    {
      '1': 'GetIssuer',
      '2': '.graph.v1.GetIssuerRequest',
      '3': '.graph.v1.GetIssuerResponse'
    },
    {
      '1': 'ListRoles',
      '2': '.graph.v1.ListRolesRequest',
      '3': '.graph.v1.ListRolesResponse'
    },
    {
      '1': 'GetRole',
      '2': '.graph.v1.GetRoleRequest',
      '3': '.graph.v1.GetRoleResponse'
    },
    {
      '1': 'ListUsers',
      '2': '.graph.v1.ListUsersRequest',
      '3': '.graph.v1.ListUsersResponse'
    },
    {
      '1': 'GetUser',
      '2': '.graph.v1.GetUserRequest',
      '3': '.graph.v1.GetUserResponse'
    },
    {
      '1': 'ListRoleAssignments',
      '2': '.graph.v1.ListRoleAssignmentsRequest',
      '3': '.graph.v1.ListRoleAssignmentsResponse'
    },
    {
      '1': 'ListSecurityAudit',
      '2': '.graph.v1.ListSecurityAuditRequest',
      '3': '.graph.v1.ListSecurityAuditResponse'
    },
    {
      '1': 'GetRoleTemplates',
      '2': '.graph.v1.GetRoleTemplatesRequest',
      '3': '.graph.v1.GetRoleTemplatesResponse'
    },
    {
      '1': 'ExplainAccess',
      '2': '.graph.v1.ExplainAccessRequest',
      '3': '.graph.v1.ExplainAccessResponse'
    },
    {
      '1': 'ValidateIssuer',
      '2': '.graph.v1.ValidateIssuerRequest',
      '3': '.graph.v1.ValidateIssuerResponse'
    },
    {
      '1': 'PrepareSecurityChanges',
      '2': '.graph.v1.PrepareSecurityChangesRequest',
      '3': '.graph.v1.PrepareSecurityChangesResponse'
    },
    {
      '1': 'BeginSecurityChangeAuthorization',
      '2': '.graph.v1.BeginSecurityChangeAuthorizationRequest',
      '3': '.graph.v1.BeginSecurityChangeAuthorizationResponse'
    },
    {
      '1': 'GetSecurityChangeAuthorization',
      '2': '.graph.v1.GetSecurityChangeAuthorizationRequest',
      '3': '.graph.v1.GetSecurityChangeAuthorizationResponse'
    },
    {
      '1': 'ApplySecurityChanges',
      '2': '.graph.v1.ApplySecurityChangesRequest',
      '3': '.graph.v1.ApplySecurityChangesResponse'
    },
    {
      '1': 'ApplySecurityChange',
      '2': '.graph.v1.ApplySecurityChangeRequest',
      '3': '.graph.v1.ApplySecurityChangeResponse'
    },
    {
      '1': 'GetSecurityChangeStatus',
      '2': '.graph.v1.GetSecurityChangeStatusRequest',
      '3': '.graph.v1.GetSecurityChangeStatusResponse'
    },
  ],
};

@$core.Deprecated('Use lanternSecurityServiceDescriptor instead')
const $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
    LanternSecurityServiceBase$messageJson = {
  '.graph.v1.GetAuthCapabilitiesRequest': GetAuthCapabilitiesRequest$json,
  '.graph.v1.GetAuthCapabilitiesResponse': GetAuthCapabilitiesResponse$json,
  '.graph.v1.LoginIssuer': LoginIssuer$json,
  '.graph.v1.GetCurrentPrincipalRequest': GetCurrentPrincipalRequest$json,
  '.graph.v1.GetCurrentPrincipalResponse': GetCurrentPrincipalResponse$json,
  '.graph.v1.SecurityIdentity': SecurityIdentity$json,
  '.graph.v1.SecurityRole': SecurityRole$json,
  '.graph.v1.SecurityRule': SecurityRule$json,
  '.graph.v1.SecurityPrefixPair': SecurityPrefixPair$json,
  '.graph.v1.SecurityVersion': SecurityVersion$json,
  '.google.protobuf.Timestamp': $0.Timestamp$json,
  '.graph.v1.ListIssuersRequest': ListIssuersRequest$json,
  '.graph.v1.ListIssuersResponse': ListIssuersResponse$json,
  '.graph.v1.SecurityIssuer': SecurityIssuer$json,
  '.graph.v1.GetIssuerRequest': GetIssuerRequest$json,
  '.graph.v1.GetIssuerResponse': GetIssuerResponse$json,
  '.graph.v1.ListRolesRequest': ListRolesRequest$json,
  '.graph.v1.ListRolesResponse': ListRolesResponse$json,
  '.graph.v1.GetRoleRequest': GetRoleRequest$json,
  '.graph.v1.GetRoleResponse': GetRoleResponse$json,
  '.graph.v1.ListUsersRequest': ListUsersRequest$json,
  '.graph.v1.ListUsersResponse': ListUsersResponse$json,
  '.graph.v1.SecurityUser': SecurityUser$json,
  '.graph.v1.SecurityRoleAssignment': SecurityRoleAssignment$json,
  '.graph.v1.GetUserRequest': GetUserRequest$json,
  '.graph.v1.GetUserResponse': GetUserResponse$json,
  '.graph.v1.ListRoleAssignmentsRequest': ListRoleAssignmentsRequest$json,
  '.graph.v1.ListRoleAssignmentsResponse': ListRoleAssignmentsResponse$json,
  '.graph.v1.ListSecurityAuditRequest': ListSecurityAuditRequest$json,
  '.graph.v1.ListSecurityAuditResponse': ListSecurityAuditResponse$json,
  '.graph.v1.SecurityAuditRecord': SecurityAuditRecord$json,
  '.graph.v1.GetRoleTemplatesRequest': GetRoleTemplatesRequest$json,
  '.graph.v1.GetRoleTemplatesResponse': GetRoleTemplatesResponse$json,
  '.graph.v1.ExplainAccessRequest': ExplainAccessRequest$json,
  '.graph.v1.SecurityEdgeIdentity': SecurityEdgeIdentity$json,
  '.graph.v1.ExplainAccessResponse': ExplainAccessResponse$json,
  '.graph.v1.SecurityRuleMatch': SecurityRuleMatch$json,
  '.graph.v1.ValidateIssuerRequest': ValidateIssuerRequest$json,
  '.graph.v1.ValidateIssuerResponse': ValidateIssuerResponse$json,
  '.graph.v1.PrepareSecurityChangesRequest': PrepareSecurityChangesRequest$json,
  '.graph.v1.SecurityChangeReview': SecurityChangeReview$json,
  '.graph.v1.SecurityChange': SecurityChange$json,
  '.graph.v1.SecurityUserStateChange': SecurityUserStateChange$json,
  '.graph.v1.PrepareSecurityChangesResponse':
      PrepareSecurityChangesResponse$json,
  '.graph.v1.GetSecurityChangeStatusResponse':
      GetSecurityChangeStatusResponse$json,
  '.graph.v1.BeginSecurityChangeAuthorizationRequest':
      BeginSecurityChangeAuthorizationRequest$json,
  '.graph.v1.BeginSecurityChangeAuthorizationResponse':
      BeginSecurityChangeAuthorizationResponse$json,
  '.graph.v1.GetSecurityChangeAuthorizationRequest':
      GetSecurityChangeAuthorizationRequest$json,
  '.graph.v1.GetSecurityChangeAuthorizationResponse':
      GetSecurityChangeAuthorizationResponse$json,
  '.graph.v1.ApplySecurityChangesRequest': ApplySecurityChangesRequest$json,
  '.graph.v1.ApplySecurityChangesResponse': ApplySecurityChangesResponse$json,
  '.graph.v1.ApplySecurityChangeRequest': ApplySecurityChangeRequest$json,
  '.graph.v1.ApplySecurityChangeResponse': ApplySecurityChangeResponse$json,
  '.graph.v1.GetSecurityChangeStatusRequest':
      GetSecurityChangeStatusRequest$json,
};

/// Descriptor for `LanternSecurityService`. Decode as a `google.protobuf.ServiceDescriptorProto`.
final $typed_data.Uint8List lanternSecurityServiceDescriptor = $convert.base64Decode(
    'ChZMYW50ZXJuU2VjdXJpdHlTZXJ2aWNlEmIKE0dldEF1dGhDYXBhYmlsaXRpZXMSJC5ncmFwaC'
    '52MS5HZXRBdXRoQ2FwYWJpbGl0aWVzUmVxdWVzdBolLmdyYXBoLnYxLkdldEF1dGhDYXBhYmls'
    'aXRpZXNSZXNwb25zZRJiChNHZXRDdXJyZW50UHJpbmNpcGFsEiQuZ3JhcGgudjEuR2V0Q3Vycm'
    'VudFByaW5jaXBhbFJlcXVlc3QaJS5ncmFwaC52MS5HZXRDdXJyZW50UHJpbmNpcGFsUmVzcG9u'
    'c2USSgoLTGlzdElzc3VlcnMSHC5ncmFwaC52MS5MaXN0SXNzdWVyc1JlcXVlc3QaHS5ncmFwaC'
    '52MS5MaXN0SXNzdWVyc1Jlc3BvbnNlEkQKCUdldElzc3VlchIaLmdyYXBoLnYxLkdldElzc3Vl'
    'clJlcXVlc3QaGy5ncmFwaC52MS5HZXRJc3N1ZXJSZXNwb25zZRJECglMaXN0Um9sZXMSGi5ncm'
    'FwaC52MS5MaXN0Um9sZXNSZXF1ZXN0GhsuZ3JhcGgudjEuTGlzdFJvbGVzUmVzcG9uc2USPgoH'
    'R2V0Um9sZRIYLmdyYXBoLnYxLkdldFJvbGVSZXF1ZXN0GhkuZ3JhcGgudjEuR2V0Um9sZVJlc3'
    'BvbnNlEkQKCUxpc3RVc2VycxIaLmdyYXBoLnYxLkxpc3RVc2Vyc1JlcXVlc3QaGy5ncmFwaC52'
    'MS5MaXN0VXNlcnNSZXNwb25zZRI+CgdHZXRVc2VyEhguZ3JhcGgudjEuR2V0VXNlclJlcXVlc3'
    'QaGS5ncmFwaC52MS5HZXRVc2VyUmVzcG9uc2USYgoTTGlzdFJvbGVBc3NpZ25tZW50cxIkLmdy'
    'YXBoLnYxLkxpc3RSb2xlQXNzaWdubWVudHNSZXF1ZXN0GiUuZ3JhcGgudjEuTGlzdFJvbGVBc3'
    'NpZ25tZW50c1Jlc3BvbnNlElwKEUxpc3RTZWN1cml0eUF1ZGl0EiIuZ3JhcGgudjEuTGlzdFNl'
    'Y3VyaXR5QXVkaXRSZXF1ZXN0GiMuZ3JhcGgudjEuTGlzdFNlY3VyaXR5QXVkaXRSZXNwb25zZR'
    'JZChBHZXRSb2xlVGVtcGxhdGVzEiEuZ3JhcGgudjEuR2V0Um9sZVRlbXBsYXRlc1JlcXVlc3Qa'
    'Ii5ncmFwaC52MS5HZXRSb2xlVGVtcGxhdGVzUmVzcG9uc2USUAoNRXhwbGFpbkFjY2VzcxIeLm'
    'dyYXBoLnYxLkV4cGxhaW5BY2Nlc3NSZXF1ZXN0Gh8uZ3JhcGgudjEuRXhwbGFpbkFjY2Vzc1Jl'
    'c3BvbnNlElMKDlZhbGlkYXRlSXNzdWVyEh8uZ3JhcGgudjEuVmFsaWRhdGVJc3N1ZXJSZXF1ZX'
    'N0GiAuZ3JhcGgudjEuVmFsaWRhdGVJc3N1ZXJSZXNwb25zZRJrChZQcmVwYXJlU2VjdXJpdHlD'
    'aGFuZ2VzEicuZ3JhcGgudjEuUHJlcGFyZVNlY3VyaXR5Q2hhbmdlc1JlcXVlc3QaKC5ncmFwaC'
    '52MS5QcmVwYXJlU2VjdXJpdHlDaGFuZ2VzUmVzcG9uc2USiQEKIEJlZ2luU2VjdXJpdHlDaGFu'
    'Z2VBdXRob3JpemF0aW9uEjEuZ3JhcGgudjEuQmVnaW5TZWN1cml0eUNoYW5nZUF1dGhvcml6YX'
    'Rpb25SZXF1ZXN0GjIuZ3JhcGgudjEuQmVnaW5TZWN1cml0eUNoYW5nZUF1dGhvcml6YXRpb25S'
    'ZXNwb25zZRKDAQoeR2V0U2VjdXJpdHlDaGFuZ2VBdXRob3JpemF0aW9uEi8uZ3JhcGgudjEuR2'
    'V0U2VjdXJpdHlDaGFuZ2VBdXRob3JpemF0aW9uUmVxdWVzdBowLmdyYXBoLnYxLkdldFNlY3Vy'
    'aXR5Q2hhbmdlQXV0aG9yaXphdGlvblJlc3BvbnNlEmUKFEFwcGx5U2VjdXJpdHlDaGFuZ2VzEi'
    'UuZ3JhcGgudjEuQXBwbHlTZWN1cml0eUNoYW5nZXNSZXF1ZXN0GiYuZ3JhcGgudjEuQXBwbHlT'
    'ZWN1cml0eUNoYW5nZXNSZXNwb25zZRJiChNBcHBseVNlY3VyaXR5Q2hhbmdlEiQuZ3JhcGgudj'
    'EuQXBwbHlTZWN1cml0eUNoYW5nZVJlcXVlc3QaJS5ncmFwaC52MS5BcHBseVNlY3VyaXR5Q2hh'
    'bmdlUmVzcG9uc2USbgoXR2V0U2VjdXJpdHlDaGFuZ2VTdGF0dXMSKC5ncmFwaC52MS5HZXRTZW'
    'N1cml0eUNoYW5nZVN0YXR1c1JlcXVlc3QaKS5ncmFwaC52MS5HZXRTZWN1cml0eUNoYW5nZVN0'
    'YXR1c1Jlc3BvbnNl');
