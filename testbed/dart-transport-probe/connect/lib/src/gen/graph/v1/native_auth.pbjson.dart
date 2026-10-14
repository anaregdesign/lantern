// This is a generated file - do not edit.
//
// Generated from graph/v1/native_auth.proto.

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
import 'security.pbjson.dart' as $1;

@$core.Deprecated('Use beginNativeLoginRequestDescriptor instead')
const BeginNativeLoginRequest$json = {
  '1': 'BeginNativeLoginRequest',
  '2': [
    {'1': 'issuer', '3': 1, '4': 1, '5': 9, '10': 'issuer'},
    {'1': 'client_profile_id', '3': 2, '4': 1, '5': 9, '10': 'clientProfileId'},
  ],
};

/// Descriptor for `BeginNativeLoginRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List beginNativeLoginRequestDescriptor =
    $convert.base64Decode(
        'ChdCZWdpbk5hdGl2ZUxvZ2luUmVxdWVzdBIWCgZpc3N1ZXIYASABKAlSBmlzc3VlchIqChFjbG'
        'llbnRfcHJvZmlsZV9pZBgCIAEoCVIPY2xpZW50UHJvZmlsZUlk');

@$core.Deprecated('Use nativePluginConfigurationDescriptor instead')
const NativePluginConfiguration$json = {
  '1': 'NativePluginConfiguration',
  '2': [
    {
      '1': 'kind',
      '3': 1,
      '4': 1,
      '5': 14,
      '6': '.graph.v1.NativeClientKind',
      '10': 'kind'
    },
    {'1': 'client_profile_id', '3': 2, '4': 1, '5': 9, '10': 'clientProfileId'},
    {'1': 'client_id', '3': 3, '4': 1, '5': 9, '10': 'clientId'},
    {'1': 'service_origin', '3': 4, '4': 1, '5': 9, '10': 'serviceOrigin'},
    {'1': 'redirect_uri', '3': 5, '4': 1, '5': 9, '10': 'redirectUri'},
    {'1': 'nonce', '3': 6, '4': 1, '5': 9, '10': 'nonce'},
    {'1': 'state', '3': 7, '4': 1, '5': 9, '10': 'state'},
  ],
};

/// Descriptor for `NativePluginConfiguration`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List nativePluginConfigurationDescriptor = $convert.base64Decode(
    'ChlOYXRpdmVQbHVnaW5Db25maWd1cmF0aW9uEi4KBGtpbmQYASABKA4yGi5ncmFwaC52MS5OYX'
    'RpdmVDbGllbnRLaW5kUgRraW5kEioKEWNsaWVudF9wcm9maWxlX2lkGAIgASgJUg9jbGllbnRQ'
    'cm9maWxlSWQSGwoJY2xpZW50X2lkGAMgASgJUghjbGllbnRJZBIlCg5zZXJ2aWNlX29yaWdpbh'
    'gEIAEoCVINc2VydmljZU9yaWdpbhIhCgxyZWRpcmVjdF91cmkYBSABKAlSC3JlZGlyZWN0VXJp'
    'EhQKBW5vbmNlGAYgASgJUgVub25jZRIUCgVzdGF0ZRgHIAEoCVIFc3RhdGU=');

@$core.Deprecated('Use beginNativeLoginResponseDescriptor instead')
const BeginNativeLoginResponse$json = {
  '1': 'BeginNativeLoginResponse',
  '2': [
    {'1': 'attempt_id', '3': 1, '4': 1, '5': 9, '10': 'attemptId'},
    {
      '1': 'completion_secret',
      '3': 2,
      '4': 1,
      '5': 9,
      '10': 'completionSecret'
    },
    {
      '1': 'expires_at',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiresAt'
    },
    {'1': 'affinity', '3': 4, '4': 1, '5': 9, '10': 'affinity'},
    {
      '1': 'configuration',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativePluginConfiguration',
      '10': 'configuration'
    },
  ],
};

/// Descriptor for `BeginNativeLoginResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List beginNativeLoginResponseDescriptor = $convert.base64Decode(
    'ChhCZWdpbk5hdGl2ZUxvZ2luUmVzcG9uc2USHQoKYXR0ZW1wdF9pZBgBIAEoCVIJYXR0ZW1wdE'
    'lkEisKEWNvbXBsZXRpb25fc2VjcmV0GAIgASgJUhBjb21wbGV0aW9uU2VjcmV0EjkKCmV4cGly'
    'ZXNfYXQYAyABKAsyGi5nb29nbGUucHJvdG9idWYuVGltZXN0YW1wUglleHBpcmVzQXQSGgoIYW'
    'ZmaW5pdHkYBCABKAlSCGFmZmluaXR5EkkKDWNvbmZpZ3VyYXRpb24YBSABKAsyIy5ncmFwaC52'
    'MS5OYXRpdmVQbHVnaW5Db25maWd1cmF0aW9uUg1jb25maWd1cmF0aW9u');

@$core.Deprecated('Use googleNativeCredentialDescriptor instead')
const GoogleNativeCredential$json = {
  '1': 'GoogleNativeCredential',
  '2': [
    {'1': 'id_token', '3': 1, '4': 1, '5': 9, '10': 'idToken'},
  ],
};

/// Descriptor for `GoogleNativeCredential`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List googleNativeCredentialDescriptor =
    $convert.base64Decode(
        'ChZHb29nbGVOYXRpdmVDcmVkZW50aWFsEhkKCGlkX3Rva2VuGAEgASgJUgdpZFRva2Vu');

@$core.Deprecated('Use appleNativeCredentialDescriptor instead')
const AppleNativeCredential$json = {
  '1': 'AppleNativeCredential',
  '2': [
    {'1': 'identity_token', '3': 1, '4': 1, '5': 9, '10': 'identityToken'},
    {
      '1': 'authorization_code',
      '3': 2,
      '4': 1,
      '5': 9,
      '10': 'authorizationCode'
    },
    {'1': 'state', '3': 3, '4': 1, '5': 9, '10': 'state'},
  ],
};

/// Descriptor for `AppleNativeCredential`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List appleNativeCredentialDescriptor = $convert.base64Decode(
    'ChVBcHBsZU5hdGl2ZUNyZWRlbnRpYWwSJQoOaWRlbnRpdHlfdG9rZW4YASABKAlSDWlkZW50aX'
    'R5VG9rZW4SLQoSYXV0aG9yaXphdGlvbl9jb2RlGAIgASgJUhFhdXRob3JpemF0aW9uQ29kZRIU'
    'CgVzdGF0ZRgDIAEoCVIFc3RhdGU=');

@$core.Deprecated('Use completeNativeLoginRequestDescriptor instead')
const CompleteNativeLoginRequest$json = {
  '1': 'CompleteNativeLoginRequest',
  '2': [
    {'1': 'attempt_id', '3': 1, '4': 1, '5': 9, '10': 'attemptId'},
    {
      '1': 'completion_secret',
      '3': 2,
      '4': 1,
      '5': 9,
      '10': 'completionSecret'
    },
    {
      '1': 'google',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.GoogleNativeCredential',
      '9': 0,
      '10': 'google'
    },
    {
      '1': 'apple',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.AppleNativeCredential',
      '9': 0,
      '10': 'apple'
    },
  ],
  '8': [
    {'1': 'provider'},
  ],
};

/// Descriptor for `CompleteNativeLoginRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List completeNativeLoginRequestDescriptor = $convert.base64Decode(
    'ChpDb21wbGV0ZU5hdGl2ZUxvZ2luUmVxdWVzdBIdCgphdHRlbXB0X2lkGAEgASgJUglhdHRlbX'
    'B0SWQSKwoRY29tcGxldGlvbl9zZWNyZXQYAiABKAlSEGNvbXBsZXRpb25TZWNyZXQSOgoGZ29v'
    'Z2xlGAMgASgLMiAuZ3JhcGgudjEuR29vZ2xlTmF0aXZlQ3JlZGVudGlhbEgAUgZnb29nbGUSNw'
    'oFYXBwbGUYBCABKAsyHy5ncmFwaC52MS5BcHBsZU5hdGl2ZUNyZWRlbnRpYWxIAFIFYXBwbGVC'
    'CgoIcHJvdmlkZXI=');

@$core.Deprecated('Use nativeSessionDescriptor instead')
const NativeSession$json = {
  '1': 'NativeSession',
  '2': [
    {
      '1': 'credential_profile',
      '3': 1,
      '4': 1,
      '5': 9,
      '10': 'credentialProfile'
    },
    {'1': 'client_profile_id', '3': 2, '4': 1, '5': 9, '10': 'clientProfileId'},
    {'1': 'service_origin', '3': 3, '4': 1, '5': 9, '10': 'serviceOrigin'},
    {
      '1': 'created_at',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'createdAt'
    },
    {
      '1': 'expires_at',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiresAt'
    },
    {
      '1': 'auth_time',
      '3': 6,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'authTime'
    },
    {
      '1': 'identity',
      '3': 7,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'identity'
    },
    {
      '1': 'version',
      '3': 8,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityVersion',
      '10': 'version'
    },
    {
      '1': 'user_id',
      '3': 9,
      '4': 1,
      '5': 9,
      '9': 0,
      '10': 'userId',
      '17': true
    },
    {
      '1': 'personal_prefix',
      '3': 10,
      '4': 1,
      '5': 9,
      '9': 1,
      '10': 'personalPrefix',
      '17': true
    },
  ],
  '8': [
    {'1': '_user_id'},
    {'1': '_personal_prefix'},
  ],
};

/// Descriptor for `NativeSession`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List nativeSessionDescriptor = $convert.base64Decode(
    'Cg1OYXRpdmVTZXNzaW9uEi0KEmNyZWRlbnRpYWxfcHJvZmlsZRgBIAEoCVIRY3JlZGVudGlhbF'
    'Byb2ZpbGUSKgoRY2xpZW50X3Byb2ZpbGVfaWQYAiABKAlSD2NsaWVudFByb2ZpbGVJZBIlCg5z'
    'ZXJ2aWNlX29yaWdpbhgDIAEoCVINc2VydmljZU9yaWdpbhI5CgpjcmVhdGVkX2F0GAQgASgLMh'
    'ouZ29vZ2xlLnByb3RvYnVmLlRpbWVzdGFtcFIJY3JlYXRlZEF0EjkKCmV4cGlyZXNfYXQYBSAB'
    'KAsyGi5nb29nbGUucHJvdG9idWYuVGltZXN0YW1wUglleHBpcmVzQXQSNwoJYXV0aF90aW1lGA'
    'YgASgLMhouZ29vZ2xlLnByb3RvYnVmLlRpbWVzdGFtcFIIYXV0aFRpbWUSNgoIaWRlbnRpdHkY'
    'ByABKAsyGi5ncmFwaC52MS5TZWN1cml0eUlkZW50aXR5UghpZGVudGl0eRIzCgd2ZXJzaW9uGA'
    'ggASgLMhkuZ3JhcGgudjEuU2VjdXJpdHlWZXJzaW9uUgd2ZXJzaW9uEhwKB3VzZXJfaWQYCSAB'
    'KAlIAFIGdXNlcklkiAEBEiwKD3BlcnNvbmFsX3ByZWZpeBgKIAEoCUgBUg5wZXJzb25hbFByZW'
    'ZpeIgBAUIKCghfdXNlcl9pZEISChBfcGVyc29uYWxfcHJlZml4');

@$core.Deprecated('Use completeNativeLoginResponseDescriptor instead')
const CompleteNativeLoginResponse$json = {
  '1': 'CompleteNativeLoginResponse',
  '2': [
    {'1': 'credential', '3': 1, '4': 1, '5': 9, '10': 'credential'},
    {
      '1': 'session',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeSession',
      '10': 'session'
    },
  ],
};

/// Descriptor for `CompleteNativeLoginResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List completeNativeLoginResponseDescriptor =
    $convert.base64Decode(
        'ChtDb21wbGV0ZU5hdGl2ZUxvZ2luUmVzcG9uc2USHgoKY3JlZGVudGlhbBgBIAEoCVIKY3JlZG'
        'VudGlhbBIxCgdzZXNzaW9uGAIgASgLMhcuZ3JhcGgudjEuTmF0aXZlU2Vzc2lvblIHc2Vzc2lv'
        'bg==');

@$core.Deprecated('Use getNativeSessionRequestDescriptor instead')
const GetNativeSessionRequest$json = {
  '1': 'GetNativeSessionRequest',
};

/// Descriptor for `GetNativeSessionRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getNativeSessionRequestDescriptor =
    $convert.base64Decode('ChdHZXROYXRpdmVTZXNzaW9uUmVxdWVzdA==');

@$core.Deprecated('Use getNativeSessionResponseDescriptor instead')
const GetNativeSessionResponse$json = {
  '1': 'GetNativeSessionResponse',
  '2': [
    {
      '1': 'session',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeSession',
      '10': 'session'
    },
    {
      '1': 'roles',
      '3': 2,
      '4': 3,
      '5': 11,
      '6': '.graph.v1.SecurityRole',
      '10': 'roles'
    },
  ],
};

/// Descriptor for `GetNativeSessionResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getNativeSessionResponseDescriptor = $convert.base64Decode(
    'ChhHZXROYXRpdmVTZXNzaW9uUmVzcG9uc2USMQoHc2Vzc2lvbhgBIAEoCzIXLmdyYXBoLnYxLk'
    '5hdGl2ZVNlc3Npb25SB3Nlc3Npb24SLAoFcm9sZXMYAiADKAsyFi5ncmFwaC52MS5TZWN1cml0'
    'eVJvbGVSBXJvbGVz');

@$core.Deprecated('Use prepareNativeLogoutRequestDescriptor instead')
const PrepareNativeLogoutRequest$json = {
  '1': 'PrepareNativeLogoutRequest',
};

/// Descriptor for `PrepareNativeLogoutRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List prepareNativeLogoutRequestDescriptor =
    $convert.base64Decode('ChpQcmVwYXJlTmF0aXZlTG9nb3V0UmVxdWVzdA==');

@$core.Deprecated('Use prepareNativeLogoutResponseDescriptor instead')
const PrepareNativeLogoutResponse$json = {
  '1': 'PrepareNativeLogoutResponse',
  '2': [
    {
      '1': 'review',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentSessionRevocationReview',
      '10': 'review'
    },
  ],
};

/// Descriptor for `PrepareNativeLogoutResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List prepareNativeLogoutResponseDescriptor =
    $convert.base64Decode(
        'ChtQcmVwYXJlTmF0aXZlTG9nb3V0UmVzcG9uc2USQAoGcmV2aWV3GAEgASgLMiguZ3JhcGgudj'
        'EuQ3VycmVudFNlc3Npb25SZXZvY2F0aW9uUmV2aWV3UgZyZXZpZXc=');

@$core.Deprecated('Use applyNativeLogoutRequestDescriptor instead')
const ApplyNativeLogoutRequest$json = {
  '1': 'ApplyNativeLogoutRequest',
  '2': [
    {
      '1': 'review',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentSessionRevocationReview',
      '10': 'review'
    },
  ],
};

/// Descriptor for `ApplyNativeLogoutRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applyNativeLogoutRequestDescriptor =
    $convert.base64Decode(
        'ChhBcHBseU5hdGl2ZUxvZ291dFJlcXVlc3QSQAoGcmV2aWV3GAEgASgLMiguZ3JhcGgudjEuQ3'
        'VycmVudFNlc3Npb25SZXZvY2F0aW9uUmV2aWV3UgZyZXZpZXc=');

@$core.Deprecated('Use applyNativeLogoutResponseDescriptor instead')
const ApplyNativeLogoutResponse$json = {
  '1': 'ApplyNativeLogoutResponse',
  '2': [
    {
      '1': 'clear_local_session',
      '3': 1,
      '4': 1,
      '5': 8,
      '10': 'clearLocalSession'
    },
    {
      '1': 'current_result',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentSecurityChangeResult',
      '10': 'currentResult'
    },
  ],
};

/// Descriptor for `ApplyNativeLogoutResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applyNativeLogoutResponseDescriptor = $convert.base64Decode(
    'ChlBcHBseU5hdGl2ZUxvZ291dFJlc3BvbnNlEi4KE2NsZWFyX2xvY2FsX3Nlc3Npb24YASABKA'
    'hSEWNsZWFyTG9jYWxTZXNzaW9uEkwKDmN1cnJlbnRfcmVzdWx0GAIgASgLMiUuZ3JhcGgudjEu'
    'Q3VycmVudFNlY3VyaXR5Q2hhbmdlUmVzdWx0Ug1jdXJyZW50UmVzdWx0');

@$core.Deprecated('Use nativeRegistrationReviewDescriptor instead')
const NativeRegistrationReview$json = {
  '1': 'NativeRegistrationReview',
  '2': [
    {
      '1': 'profile',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentAuthorityProfile',
      '10': 'profile'
    },
    {
      '1': 'expected_cut',
      '3': 2,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentSemanticCut',
      '10': 'expectedCut'
    },
    {
      '1': 'change_id',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentSecurityChangeID',
      '10': 'changeId'
    },
    {
      '1': 'actor',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.SecurityIdentity',
      '10': 'actor'
    },
    {'1': 'intent_digest', '3': 5, '4': 1, '5': 12, '10': 'intentDigest'},
    {'1': 'user_id', '3': 6, '4': 1, '5': 9, '10': 'userId'},
    {'1': 'client_profile_id', '3': 7, '4': 1, '5': 9, '10': 'clientProfileId'},
    {
      '1': 'issuer_config_revision',
      '3': 8,
      '4': 1,
      '5': 4,
      '10': 'issuerConfigRevision'
    },
  ],
};

/// Descriptor for `NativeRegistrationReview`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List nativeRegistrationReviewDescriptor = $convert.base64Decode(
    'ChhOYXRpdmVSZWdpc3RyYXRpb25SZXZpZXcSOwoHcHJvZmlsZRgBIAEoCzIhLmdyYXBoLnYxLk'
    'N1cnJlbnRBdXRob3JpdHlQcm9maWxlUgdwcm9maWxlEj8KDGV4cGVjdGVkX2N1dBgCIAEoCzIc'
    'LmdyYXBoLnYxLkN1cnJlbnRTZW1hbnRpY0N1dFILZXhwZWN0ZWRDdXQSPgoJY2hhbmdlX2lkGA'
    'MgASgLMiEuZ3JhcGgudjEuQ3VycmVudFNlY3VyaXR5Q2hhbmdlSURSCGNoYW5nZUlkEjAKBWFj'
    'dG9yGAQgASgLMhouZ3JhcGgudjEuU2VjdXJpdHlJZGVudGl0eVIFYWN0b3ISIwoNaW50ZW50X2'
    'RpZ2VzdBgFIAEoDFIMaW50ZW50RGlnZXN0EhcKB3VzZXJfaWQYBiABKAlSBnVzZXJJZBIqChFj'
    'bGllbnRfcHJvZmlsZV9pZBgHIAEoCVIPY2xpZW50UHJvZmlsZUlkEjQKFmlzc3Vlcl9jb25maW'
    'dfcmV2aXNpb24YCCABKARSFGlzc3VlckNvbmZpZ1JldmlzaW9u');

@$core.Deprecated('Use beginNativeRegistrationRequestDescriptor instead')
const BeginNativeRegistrationRequest$json = {
  '1': 'BeginNativeRegistrationRequest',
  '2': [
    {'1': 'issuer', '3': 1, '4': 1, '5': 9, '10': 'issuer'},
    {'1': 'client_profile_id', '3': 2, '4': 1, '5': 9, '10': 'clientProfileId'},
    {
      '1': 'recovery_review',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeRegistrationReview',
      '10': 'recoveryReview'
    },
  ],
};

/// Descriptor for `BeginNativeRegistrationRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List beginNativeRegistrationRequestDescriptor =
    $convert.base64Decode(
        'Ch5CZWdpbk5hdGl2ZVJlZ2lzdHJhdGlvblJlcXVlc3QSFgoGaXNzdWVyGAEgASgJUgZpc3N1ZX'
        'ISKgoRY2xpZW50X3Byb2ZpbGVfaWQYAiABKAlSD2NsaWVudFByb2ZpbGVJZBJLCg9yZWNvdmVy'
        'eV9yZXZpZXcYAyABKAsyIi5ncmFwaC52MS5OYXRpdmVSZWdpc3RyYXRpb25SZXZpZXdSDnJlY2'
        '92ZXJ5UmV2aWV3');

@$core.Deprecated('Use beginNativeRegistrationResponseDescriptor instead')
const BeginNativeRegistrationResponse$json = {
  '1': 'BeginNativeRegistrationResponse',
  '2': [
    {
      '1': 'attempt',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.BeginNativeLoginResponse',
      '10': 'attempt'
    },
  ],
};

/// Descriptor for `BeginNativeRegistrationResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List beginNativeRegistrationResponseDescriptor =
    $convert.base64Decode(
        'Ch9CZWdpbk5hdGl2ZVJlZ2lzdHJhdGlvblJlc3BvbnNlEjwKB2F0dGVtcHQYASABKAsyIi5ncm'
        'FwaC52MS5CZWdpbk5hdGl2ZUxvZ2luUmVzcG9uc2VSB2F0dGVtcHQ=');

@$core.Deprecated('Use prepareNativeRegistrationRequestDescriptor instead')
const PrepareNativeRegistrationRequest$json = {
  '1': 'PrepareNativeRegistrationRequest',
  '2': [
    {'1': 'attempt_id', '3': 1, '4': 1, '5': 9, '10': 'attemptId'},
    {
      '1': 'completion_secret',
      '3': 2,
      '4': 1,
      '5': 9,
      '10': 'completionSecret'
    },
    {
      '1': 'google',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.GoogleNativeCredential',
      '9': 0,
      '10': 'google'
    },
    {
      '1': 'apple',
      '3': 4,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.AppleNativeCredential',
      '9': 0,
      '10': 'apple'
    },
    {
      '1': 'recovery_review',
      '3': 5,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeRegistrationReview',
      '10': 'recoveryReview'
    },
  ],
  '8': [
    {'1': 'provider'},
  ],
};

/// Descriptor for `PrepareNativeRegistrationRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List prepareNativeRegistrationRequestDescriptor = $convert.base64Decode(
    'CiBQcmVwYXJlTmF0aXZlUmVnaXN0cmF0aW9uUmVxdWVzdBIdCgphdHRlbXB0X2lkGAEgASgJUg'
    'lhdHRlbXB0SWQSKwoRY29tcGxldGlvbl9zZWNyZXQYAiABKAlSEGNvbXBsZXRpb25TZWNyZXQS'
    'OgoGZ29vZ2xlGAMgASgLMiAuZ3JhcGgudjEuR29vZ2xlTmF0aXZlQ3JlZGVudGlhbEgAUgZnb2'
    '9nbGUSNwoFYXBwbGUYBCABKAsyHy5ncmFwaC52MS5BcHBsZU5hdGl2ZUNyZWRlbnRpYWxIAFIF'
    'YXBwbGUSSwoPcmVjb3ZlcnlfcmV2aWV3GAUgASgLMiIuZ3JhcGgudjEuTmF0aXZlUmVnaXN0cm'
    'F0aW9uUmV2aWV3Ug5yZWNvdmVyeVJldmlld0IKCghwcm92aWRlcg==');

@$core.Deprecated('Use prepareNativeRegistrationResponseDescriptor instead')
const PrepareNativeRegistrationResponse$json = {
  '1': 'PrepareNativeRegistrationResponse',
  '2': [
    {
      '1': 'review',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeRegistrationReview',
      '10': 'review'
    },
    {'1': 'proof', '3': 2, '4': 1, '5': 9, '10': 'proof'},
    {
      '1': 'expires_at',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.google.protobuf.Timestamp',
      '10': 'expiresAt'
    },
    {'1': 'affinity', '3': 4, '4': 1, '5': 9, '10': 'affinity'},
  ],
};

/// Descriptor for `PrepareNativeRegistrationResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List prepareNativeRegistrationResponseDescriptor =
    $convert.base64Decode(
        'CiFQcmVwYXJlTmF0aXZlUmVnaXN0cmF0aW9uUmVzcG9uc2USOgoGcmV2aWV3GAEgASgLMiIuZ3'
        'JhcGgudjEuTmF0aXZlUmVnaXN0cmF0aW9uUmV2aWV3UgZyZXZpZXcSFAoFcHJvb2YYAiABKAlS'
        'BXByb29mEjkKCmV4cGlyZXNfYXQYAyABKAsyGi5nb29nbGUucHJvdG9idWYuVGltZXN0YW1wUg'
        'lleHBpcmVzQXQSGgoIYWZmaW5pdHkYBCABKAlSCGFmZmluaXR5');

@$core.Deprecated('Use applyNativeRegistrationRequestDescriptor instead')
const ApplyNativeRegistrationRequest$json = {
  '1': 'ApplyNativeRegistrationRequest',
  '2': [
    {
      '1': 'review',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeRegistrationReview',
      '10': 'review'
    },
    {'1': 'proof', '3': 2, '4': 1, '5': 9, '10': 'proof'},
  ],
};

/// Descriptor for `ApplyNativeRegistrationRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applyNativeRegistrationRequestDescriptor =
    $convert.base64Decode(
        'Ch5BcHBseU5hdGl2ZVJlZ2lzdHJhdGlvblJlcXVlc3QSOgoGcmV2aWV3GAEgASgLMiIuZ3JhcG'
        'gudjEuTmF0aXZlUmVnaXN0cmF0aW9uUmV2aWV3UgZyZXZpZXcSFAoFcHJvb2YYAiABKAlSBXBy'
        'b29m');

@$core.Deprecated('Use applyNativeRegistrationResponseDescriptor instead')
const ApplyNativeRegistrationResponse$json = {
  '1': 'ApplyNativeRegistrationResponse',
  '2': [
    {
      '1': 'current_result',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentSecurityChangeResult',
      '10': 'currentResult'
    },
    {'1': 'credential', '3': 2, '4': 1, '5': 9, '10': 'credential'},
    {
      '1': 'session',
      '3': 3,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeSession',
      '10': 'session'
    },
    {'1': 'sign_in_required', '3': 4, '4': 1, '5': 8, '10': 'signInRequired'},
  ],
};

/// Descriptor for `ApplyNativeRegistrationResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List applyNativeRegistrationResponseDescriptor = $convert.base64Decode(
    'Ch9BcHBseU5hdGl2ZVJlZ2lzdHJhdGlvblJlc3BvbnNlEkwKDmN1cnJlbnRfcmVzdWx0GAEgAS'
    'gLMiUuZ3JhcGgudjEuQ3VycmVudFNlY3VyaXR5Q2hhbmdlUmVzdWx0Ug1jdXJyZW50UmVzdWx0'
    'Eh4KCmNyZWRlbnRpYWwYAiABKAlSCmNyZWRlbnRpYWwSMQoHc2Vzc2lvbhgDIAEoCzIXLmdyYX'
    'BoLnYxLk5hdGl2ZVNlc3Npb25SB3Nlc3Npb24SKAoQc2lnbl9pbl9yZXF1aXJlZBgEIAEoCFIO'
    'c2lnbkluUmVxdWlyZWQ=');

@$core.Deprecated('Use getNativeRegistrationStatusRequestDescriptor instead')
const GetNativeRegistrationStatusRequest$json = {
  '1': 'GetNativeRegistrationStatusRequest',
  '2': [
    {
      '1': 'review',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.NativeRegistrationReview',
      '10': 'review'
    },
    {'1': 'proof', '3': 2, '4': 1, '5': 9, '10': 'proof'},
  ],
};

/// Descriptor for `GetNativeRegistrationStatusRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getNativeRegistrationStatusRequestDescriptor =
    $convert.base64Decode(
        'CiJHZXROYXRpdmVSZWdpc3RyYXRpb25TdGF0dXNSZXF1ZXN0EjoKBnJldmlldxgBIAEoCzIiLm'
        'dyYXBoLnYxLk5hdGl2ZVJlZ2lzdHJhdGlvblJldmlld1IGcmV2aWV3EhQKBXByb29mGAIgASgJ'
        'UgVwcm9vZg==');

@$core.Deprecated('Use getNativeRegistrationStatusResponseDescriptor instead')
const GetNativeRegistrationStatusResponse$json = {
  '1': 'GetNativeRegistrationStatusResponse',
  '2': [
    {
      '1': 'current_result',
      '3': 1,
      '4': 1,
      '5': 11,
      '6': '.graph.v1.CurrentSecurityChangeResult',
      '10': 'currentResult'
    },
  ],
};

/// Descriptor for `GetNativeRegistrationStatusResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List getNativeRegistrationStatusResponseDescriptor =
    $convert.base64Decode(
        'CiNHZXROYXRpdmVSZWdpc3RyYXRpb25TdGF0dXNSZXNwb25zZRJMCg5jdXJyZW50X3Jlc3VsdB'
        'gBIAEoCzIlLmdyYXBoLnYxLkN1cnJlbnRTZWN1cml0eUNoYW5nZVJlc3VsdFINY3VycmVudFJl'
        'c3VsdA==');

const $core.Map<$core.String, $core.dynamic> LanternNativeAuthServiceBase$json =
    {
  '1': 'LanternNativeAuthService',
  '2': [
    {
      '1': 'BeginNativeLogin',
      '2': '.graph.v1.BeginNativeLoginRequest',
      '3': '.graph.v1.BeginNativeLoginResponse'
    },
    {
      '1': 'CompleteNativeLogin',
      '2': '.graph.v1.CompleteNativeLoginRequest',
      '3': '.graph.v1.CompleteNativeLoginResponse'
    },
    {
      '1': 'GetNativeSession',
      '2': '.graph.v1.GetNativeSessionRequest',
      '3': '.graph.v1.GetNativeSessionResponse'
    },
    {
      '1': 'PrepareNativeLogout',
      '2': '.graph.v1.PrepareNativeLogoutRequest',
      '3': '.graph.v1.PrepareNativeLogoutResponse'
    },
    {
      '1': 'ApplyNativeLogout',
      '2': '.graph.v1.ApplyNativeLogoutRequest',
      '3': '.graph.v1.ApplyNativeLogoutResponse'
    },
    {
      '1': 'BeginNativeRegistration',
      '2': '.graph.v1.BeginNativeRegistrationRequest',
      '3': '.graph.v1.BeginNativeRegistrationResponse'
    },
    {
      '1': 'PrepareNativeRegistration',
      '2': '.graph.v1.PrepareNativeRegistrationRequest',
      '3': '.graph.v1.PrepareNativeRegistrationResponse'
    },
    {
      '1': 'ApplyNativeRegistration',
      '2': '.graph.v1.ApplyNativeRegistrationRequest',
      '3': '.graph.v1.ApplyNativeRegistrationResponse'
    },
    {
      '1': 'GetNativeRegistrationStatus',
      '2': '.graph.v1.GetNativeRegistrationStatusRequest',
      '3': '.graph.v1.GetNativeRegistrationStatusResponse'
    },
  ],
};

@$core.Deprecated('Use lanternNativeAuthServiceDescriptor instead')
const $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
    LanternNativeAuthServiceBase$messageJson = {
  '.graph.v1.BeginNativeLoginRequest': BeginNativeLoginRequest$json,
  '.graph.v1.BeginNativeLoginResponse': BeginNativeLoginResponse$json,
  '.google.protobuf.Timestamp': $0.Timestamp$json,
  '.graph.v1.NativePluginConfiguration': NativePluginConfiguration$json,
  '.graph.v1.CompleteNativeLoginRequest': CompleteNativeLoginRequest$json,
  '.graph.v1.GoogleNativeCredential': GoogleNativeCredential$json,
  '.graph.v1.AppleNativeCredential': AppleNativeCredential$json,
  '.graph.v1.CompleteNativeLoginResponse': CompleteNativeLoginResponse$json,
  '.graph.v1.NativeSession': NativeSession$json,
  '.graph.v1.SecurityIdentity': $1.SecurityIdentity$json,
  '.graph.v1.SecurityVersion': $1.SecurityVersion$json,
  '.graph.v1.CurrentAuthorityProfile': $1.CurrentAuthorityProfile$json,
  '.graph.v1.CurrentSemanticCut': $1.CurrentSemanticCut$json,
  '.graph.v1.GetNativeSessionRequest': GetNativeSessionRequest$json,
  '.graph.v1.GetNativeSessionResponse': GetNativeSessionResponse$json,
  '.graph.v1.SecurityRole': $1.SecurityRole$json,
  '.graph.v1.SecurityRule': $1.SecurityRule$json,
  '.graph.v1.SecurityPrefixPair': $1.SecurityPrefixPair$json,
  '.graph.v1.SecurityPrincipalUIDPrefix': $1.SecurityPrincipalUIDPrefix$json,
  '.graph.v1.PrepareNativeLogoutRequest': PrepareNativeLogoutRequest$json,
  '.graph.v1.PrepareNativeLogoutResponse': PrepareNativeLogoutResponse$json,
  '.graph.v1.CurrentSessionRevocationReview':
      $1.CurrentSessionRevocationReview$json,
  '.graph.v1.CurrentSecurityChangeID': $1.CurrentSecurityChangeID$json,
  '.graph.v1.ApplyNativeLogoutRequest': ApplyNativeLogoutRequest$json,
  '.graph.v1.ApplyNativeLogoutResponse': ApplyNativeLogoutResponse$json,
  '.graph.v1.CurrentSecurityChangeResult': $1.CurrentSecurityChangeResult$json,
  '.graph.v1.CurrentSecurityOriginalOutcome':
      $1.CurrentSecurityOriginalOutcome$json,
  '.graph.v1.CurrentControlCommit': $1.CurrentControlCommit$json,
  '.graph.v1.CurrentSecurityItemOutcome': $1.CurrentSecurityItemOutcome$json,
  '.graph.v1.BeginNativeRegistrationRequest':
      BeginNativeRegistrationRequest$json,
  '.graph.v1.NativeRegistrationReview': NativeRegistrationReview$json,
  '.graph.v1.BeginNativeRegistrationResponse':
      BeginNativeRegistrationResponse$json,
  '.graph.v1.PrepareNativeRegistrationRequest':
      PrepareNativeRegistrationRequest$json,
  '.graph.v1.PrepareNativeRegistrationResponse':
      PrepareNativeRegistrationResponse$json,
  '.graph.v1.ApplyNativeRegistrationRequest':
      ApplyNativeRegistrationRequest$json,
  '.graph.v1.ApplyNativeRegistrationResponse':
      ApplyNativeRegistrationResponse$json,
  '.graph.v1.GetNativeRegistrationStatusRequest':
      GetNativeRegistrationStatusRequest$json,
  '.graph.v1.GetNativeRegistrationStatusResponse':
      GetNativeRegistrationStatusResponse$json,
};

/// Descriptor for `LanternNativeAuthService`. Decode as a `google.protobuf.ServiceDescriptorProto`.
final $typed_data.Uint8List lanternNativeAuthServiceDescriptor = $convert.base64Decode(
    'ChhMYW50ZXJuTmF0aXZlQXV0aFNlcnZpY2USWQoQQmVnaW5OYXRpdmVMb2dpbhIhLmdyYXBoLn'
    'YxLkJlZ2luTmF0aXZlTG9naW5SZXF1ZXN0GiIuZ3JhcGgudjEuQmVnaW5OYXRpdmVMb2dpblJl'
    'c3BvbnNlEmIKE0NvbXBsZXRlTmF0aXZlTG9naW4SJC5ncmFwaC52MS5Db21wbGV0ZU5hdGl2ZU'
    'xvZ2luUmVxdWVzdBolLmdyYXBoLnYxLkNvbXBsZXRlTmF0aXZlTG9naW5SZXNwb25zZRJZChBH'
    'ZXROYXRpdmVTZXNzaW9uEiEuZ3JhcGgudjEuR2V0TmF0aXZlU2Vzc2lvblJlcXVlc3QaIi5ncm'
    'FwaC52MS5HZXROYXRpdmVTZXNzaW9uUmVzcG9uc2USYgoTUHJlcGFyZU5hdGl2ZUxvZ291dBIk'
    'LmdyYXBoLnYxLlByZXBhcmVOYXRpdmVMb2dvdXRSZXF1ZXN0GiUuZ3JhcGgudjEuUHJlcGFyZU'
    '5hdGl2ZUxvZ291dFJlc3BvbnNlElwKEUFwcGx5TmF0aXZlTG9nb3V0EiIuZ3JhcGgudjEuQXBw'
    'bHlOYXRpdmVMb2dvdXRSZXF1ZXN0GiMuZ3JhcGgudjEuQXBwbHlOYXRpdmVMb2dvdXRSZXNwb2'
    '5zZRJuChdCZWdpbk5hdGl2ZVJlZ2lzdHJhdGlvbhIoLmdyYXBoLnYxLkJlZ2luTmF0aXZlUmVn'
    'aXN0cmF0aW9uUmVxdWVzdBopLmdyYXBoLnYxLkJlZ2luTmF0aXZlUmVnaXN0cmF0aW9uUmVzcG'
    '9uc2USdAoZUHJlcGFyZU5hdGl2ZVJlZ2lzdHJhdGlvbhIqLmdyYXBoLnYxLlByZXBhcmVOYXRp'
    'dmVSZWdpc3RyYXRpb25SZXF1ZXN0GisuZ3JhcGgudjEuUHJlcGFyZU5hdGl2ZVJlZ2lzdHJhdG'
    'lvblJlc3BvbnNlEm4KF0FwcGx5TmF0aXZlUmVnaXN0cmF0aW9uEiguZ3JhcGgudjEuQXBwbHlO'
    'YXRpdmVSZWdpc3RyYXRpb25SZXF1ZXN0GikuZ3JhcGgudjEuQXBwbHlOYXRpdmVSZWdpc3RyYX'
    'Rpb25SZXNwb25zZRJ6ChtHZXROYXRpdmVSZWdpc3RyYXRpb25TdGF0dXMSLC5ncmFwaC52MS5H'
    'ZXROYXRpdmVSZWdpc3RyYXRpb25TdGF0dXNSZXF1ZXN0Gi0uZ3JhcGgudjEuR2V0TmF0aXZlUm'
    'VnaXN0cmF0aW9uU3RhdHVzUmVzcG9uc2U=');
