// This is a generated file - do not edit.
//
// Generated from graph/v1/security_peer.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

class RenewPolicyLeaseRequest extends $pb.GeneratedMessage {
  factory RenewPolicyLeaseRequest({
    $core.List<$core.int>? receiver,
    $core.List<$core.int>? bootNonce,
    $core.List<$core.int>? challenge,
    $core.List<$core.int>? knownDigest,
  }) {
    final result = create();
    if (receiver != null) result.receiver = receiver;
    if (bootNonce != null) result.bootNonce = bootNonce;
    if (challenge != null) result.challenge = challenge;
    if (knownDigest != null) result.knownDigest = knownDigest;
    return result;
  }

  RenewPolicyLeaseRequest._();

  factory RenewPolicyLeaseRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory RenewPolicyLeaseRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'RenewPolicyLeaseRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'receiver', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'bootNonce', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'challenge', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'knownDigest', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RenewPolicyLeaseRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RenewPolicyLeaseRequest copyWith(
          void Function(RenewPolicyLeaseRequest) updates) =>
      super.copyWith((message) => updates(message as RenewPolicyLeaseRequest))
          as RenewPolicyLeaseRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static RenewPolicyLeaseRequest create() => RenewPolicyLeaseRequest._();
  @$core.override
  RenewPolicyLeaseRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static RenewPolicyLeaseRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<RenewPolicyLeaseRequest>(create);
  static RenewPolicyLeaseRequest? _defaultInstance;

  /// Must equal the authenticated workload's operator-approved stable ID.
  @$pb.TagNumber(1)
  $core.List<$core.int> get receiver => $_getN(0);
  @$pb.TagNumber(1)
  set receiver($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasReceiver() => $_has(0);
  @$pb.TagNumber(1)
  void clearReceiver() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.List<$core.int> get bootNonce => $_getN(1);
  @$pb.TagNumber(2)
  set bootNonce($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasBootNonce() => $_has(1);
  @$pb.TagNumber(2)
  void clearBootNonce() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get challenge => $_getN(2);
  @$pb.TagNumber(3)
  set challenge($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasChallenge() => $_has(2);
  @$pb.TagNumber(3)
  void clearChallenge() => $_clearField(3);

  /// Optional exact current signed revision digest; not an authorization proof.
  @$pb.TagNumber(4)
  $core.List<$core.int> get knownDigest => $_getN(3);
  @$pb.TagNumber(4)
  set knownDigest($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasKnownDigest() => $_has(3);
  @$pb.TagNumber(4)
  void clearKnownDigest() => $_clearField(4);
}

class RenewPolicyLeaseResponse extends $pb.GeneratedMessage {
  factory RenewPolicyLeaseResponse({
    $core.List<$core.int>? signedLease,
    $core.List<$core.int>? signedCheckpoint,
  }) {
    final result = create();
    if (signedLease != null) result.signedLease = signedLease;
    if (signedCheckpoint != null) result.signedCheckpoint = signedCheckpoint;
    return result;
  }

  RenewPolicyLeaseResponse._();

  factory RenewPolicyLeaseResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory RenewPolicyLeaseResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'RenewPolicyLeaseResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'signedLease', $pb.PbFieldType.OY)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'signedCheckpoint', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RenewPolicyLeaseResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  RenewPolicyLeaseResponse copyWith(
          void Function(RenewPolicyLeaseResponse) updates) =>
      super.copyWith((message) => updates(message as RenewPolicyLeaseResponse))
          as RenewPolicyLeaseResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static RenewPolicyLeaseResponse create() => RenewPolicyLeaseResponse._();
  @$core.override
  RenewPolicyLeaseResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static RenewPolicyLeaseResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<RenewPolicyLeaseResponse>(create);
  static RenewPolicyLeaseResponse? _defaultInstance;

  /// Original writer signature, bound to receiver/boot/challenge and policy cut.
  @$pb.TagNumber(1)
  $core.List<$core.int> get signedLease => $_getN(0);
  @$pb.TagNumber(1)
  set signedLease($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSignedLease() => $_has(0);
  @$pb.TagNumber(1)
  void clearSignedLease() => $_clearField(1);

  /// Complete original signed cut; omitted only for an identical known digest.
  @$pb.TagNumber(2)
  $core.List<$core.int> get signedCheckpoint => $_getN(1);
  @$pb.TagNumber(2)
  set signedCheckpoint($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasSignedCheckpoint() => $_has(1);
  @$pb.TagNumber(2)
  void clearSignedCheckpoint() => $_clearField(2);
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
