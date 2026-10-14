// This is a generated file - do not edit.
//
// Generated from graph/v1/native_auth.proto.

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
    {'1': 'user_uid', '3': 9, '4': 1, '5': 9, '10': 'userUid'},
    {'1': 'personal_prefix', '3': 10, '4': 1, '5': 9, '10': 'personalPrefix'},
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
    'ggASgLMhkuZ3JhcGgudjEuU2VjdXJpdHlWZXJzaW9uUgd2ZXJzaW9uEhkKCHVzZXJfdWlkGAkg'
    'ASgJUgd1c2VyVWlkEicKD3BlcnNvbmFsX3ByZWZpeBgKIAEoCVIOcGVyc29uYWxQcmVmaXg=');

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
