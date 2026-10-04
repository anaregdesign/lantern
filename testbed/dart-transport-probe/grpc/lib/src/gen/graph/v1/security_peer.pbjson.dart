// This is a generated file - do not edit.
//
// Generated from graph/v1/security_peer.proto.

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

@$core.Deprecated('Use renewPolicyLeaseRequestDescriptor instead')
const RenewPolicyLeaseRequest$json = {
  '1': 'RenewPolicyLeaseRequest',
  '2': [
    {'1': 'receiver', '3': 1, '4': 1, '5': 12, '10': 'receiver'},
    {'1': 'boot_nonce', '3': 2, '4': 1, '5': 12, '10': 'bootNonce'},
    {'1': 'challenge', '3': 3, '4': 1, '5': 12, '10': 'challenge'},
    {'1': 'known_digest', '3': 4, '4': 1, '5': 12, '10': 'knownDigest'},
  ],
};

/// Descriptor for `RenewPolicyLeaseRequest`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List renewPolicyLeaseRequestDescriptor = $convert.base64Decode(
    'ChdSZW5ld1BvbGljeUxlYXNlUmVxdWVzdBIaCghyZWNlaXZlchgBIAEoDFIIcmVjZWl2ZXISHQ'
    'oKYm9vdF9ub25jZRgCIAEoDFIJYm9vdE5vbmNlEhwKCWNoYWxsZW5nZRgDIAEoDFIJY2hhbGxl'
    'bmdlEiEKDGtub3duX2RpZ2VzdBgEIAEoDFILa25vd25EaWdlc3Q=');

@$core.Deprecated('Use renewPolicyLeaseResponseDescriptor instead')
const RenewPolicyLeaseResponse$json = {
  '1': 'RenewPolicyLeaseResponse',
  '2': [
    {'1': 'signed_lease', '3': 1, '4': 1, '5': 12, '10': 'signedLease'},
    {
      '1': 'signed_checkpoint',
      '3': 2,
      '4': 1,
      '5': 12,
      '10': 'signedCheckpoint'
    },
  ],
};

/// Descriptor for `RenewPolicyLeaseResponse`. Decode as a `google.protobuf.DescriptorProto`.
final $typed_data.Uint8List renewPolicyLeaseResponseDescriptor =
    $convert.base64Decode(
        'ChhSZW5ld1BvbGljeUxlYXNlUmVzcG9uc2USIQoMc2lnbmVkX2xlYXNlGAEgASgMUgtzaWduZW'
        'RMZWFzZRIrChFzaWduZWRfY2hlY2twb2ludBgCIAEoDFIQc2lnbmVkQ2hlY2twb2ludA==');
