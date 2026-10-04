// This is a generated file - do not edit.
//
// Generated from graph/v1/changes.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'changes.pbenum.dart';
import 'graph.pb.dart' as $1;

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

export 'changes.pbenum.dart';

class WatchChangesRequest extends $pb.GeneratedMessage {
  factory WatchChangesRequest({
    $core.String? prefix,
    ChangeProjection? projection,
    $core.bool? bootstrap,
    $core.List<$core.int>? cursor,
  }) {
    final result = create();
    if (prefix != null) result.prefix = prefix;
    if (projection != null) result.projection = projection;
    if (bootstrap != null) result.bootstrap = bootstrap;
    if (cursor != null) result.cursor = cursor;
    return result;
  }

  WatchChangesRequest._();

  factory WatchChangesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory WatchChangesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'WatchChangesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'prefix')
    ..aE<ChangeProjection>(2, _omitFieldNames ? '' : 'projection',
        enumValues: ChangeProjection.values)
    ..aOB(3, _omitFieldNames ? '' : 'bootstrap')
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'cursor', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  WatchChangesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  WatchChangesRequest copyWith(void Function(WatchChangesRequest) updates) =>
      super.copyWith((message) => updates(message as WatchChangesRequest))
          as WatchChangesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static WatchChangesRequest create() => WatchChangesRequest._();
  @$core.override
  WatchChangesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static WatchChangesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<WatchChangesRequest>(create);
  static WatchChangesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get prefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set prefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearPrefix() => $_clearField(1);

  @$pb.TagNumber(2)
  ChangeProjection get projection => $_getN(1);
  @$pb.TagNumber(2)
  set projection(ChangeProjection value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasProjection() => $_has(1);
  @$pb.TagNumber(2)
  void clearProjection() => $_clearField(2);

  /// Bootstrap opens a tail at an atomic publication cut. Keep it open while
  /// rebuilding the authorized resident cache with ordinary read RPCs.
  @$pb.TagNumber(3)
  $core.bool get bootstrap => $_getBF(2);
  @$pb.TagNumber(3)
  set bootstrap($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasBootstrap() => $_has(2);
  @$pb.TagNumber(3)
  void clearBootstrap() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.List<$core.int> get cursor => $_getN(3);
  @$pb.TagNumber(4)
  set cursor($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasCursor() => $_has(3);
  @$pb.TagNumber(4)
  void clearCursor() => $_clearField(4);
}

enum ChangeInvalidation_Identity { vertexKey, edgeKey, notSet }

enum ChangeInvalidation_CurrentImage { vertex, edge, notSet }

class ChangeInvalidation extends $pb.GeneratedMessage {
  factory ChangeInvalidation({
    $core.String? vertexKey,
    $1.EdgeKey? edgeKey,
    $1.Vertex? vertex,
    $1.Edge? edge,
  }) {
    final result = create();
    if (vertexKey != null) result.vertexKey = vertexKey;
    if (edgeKey != null) result.edgeKey = edgeKey;
    if (vertex != null) result.vertex = vertex;
    if (edge != null) result.edge = edge;
    return result;
  }

  ChangeInvalidation._();

  factory ChangeInvalidation.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ChangeInvalidation.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, ChangeInvalidation_Identity>
      _ChangeInvalidation_IdentityByTag = {
    1: ChangeInvalidation_Identity.vertexKey,
    2: ChangeInvalidation_Identity.edgeKey,
    0: ChangeInvalidation_Identity.notSet
  };
  static const $core.Map<$core.int, ChangeInvalidation_CurrentImage>
      _ChangeInvalidation_CurrentImageByTag = {
    3: ChangeInvalidation_CurrentImage.vertex,
    4: ChangeInvalidation_CurrentImage.edge,
    0: ChangeInvalidation_CurrentImage.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ChangeInvalidation',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [1, 2])
    ..oo(1, [3, 4])
    ..aOS(1, _omitFieldNames ? '' : 'vertexKey')
    ..aOM<$1.EdgeKey>(2, _omitFieldNames ? '' : 'edgeKey',
        subBuilder: $1.EdgeKey.create)
    ..aOM<$1.Vertex>(3, _omitFieldNames ? '' : 'vertex',
        subBuilder: $1.Vertex.create)
    ..aOM<$1.Edge>(4, _omitFieldNames ? '' : 'edge', subBuilder: $1.Edge.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ChangeInvalidation clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ChangeInvalidation copyWith(void Function(ChangeInvalidation) updates) =>
      super.copyWith((message) => updates(message as ChangeInvalidation))
          as ChangeInvalidation;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ChangeInvalidation create() => ChangeInvalidation._();
  @$core.override
  ChangeInvalidation createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ChangeInvalidation getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ChangeInvalidation>(create);
  static ChangeInvalidation? _defaultInstance;

  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  ChangeInvalidation_Identity whichIdentity() =>
      _ChangeInvalidation_IdentityByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  void clearIdentity() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(3)
  @$pb.TagNumber(4)
  ChangeInvalidation_CurrentImage whichCurrentImage() =>
      _ChangeInvalidation_CurrentImageByTag[$_whichOneof(1)]!;
  @$pb.TagNumber(3)
  @$pb.TagNumber(4)
  void clearCurrentImage() => $_clearField($_whichOneof(1));

  @$pb.TagNumber(1)
  $core.String get vertexKey => $_getSZ(0);
  @$pb.TagNumber(1)
  set vertexKey($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVertexKey() => $_has(0);
  @$pb.TagNumber(1)
  void clearVertexKey() => $_clearField(1);

  @$pb.TagNumber(2)
  $1.EdgeKey get edgeKey => $_getN(1);
  @$pb.TagNumber(2)
  set edgeKey($1.EdgeKey value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasEdgeKey() => $_has(1);
  @$pb.TagNumber(2)
  void clearEdgeKey() => $_clearField(2);
  @$pb.TagNumber(2)
  $1.EdgeKey ensureEdgeKey() => $_ensure(1);

  @$pb.TagNumber(3)
  $1.Vertex get vertex => $_getN(2);
  @$pb.TagNumber(3)
  set vertex($1.Vertex value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasVertex() => $_has(2);
  @$pb.TagNumber(3)
  void clearVertex() => $_clearField(3);
  @$pb.TagNumber(3)
  $1.Vertex ensureVertex() => $_ensure(2);

  @$pb.TagNumber(4)
  $1.Edge get edge => $_getN(3);
  @$pb.TagNumber(4)
  set edge($1.Edge value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasEdge() => $_has(3);
  @$pb.TagNumber(4)
  void clearEdge() => $_clearField(4);
  @$pb.TagNumber(4)
  $1.Edge ensureEdge() => $_ensure(3);
}

class WatchChangesResponse extends $pb.GeneratedMessage {
  factory WatchChangesResponse({
    $core.Iterable<ChangeInvalidation>? invalidations,
    $core.List<$core.int>? cursor,
    $core.bool? bootstrap,
  }) {
    final result = create();
    if (invalidations != null) result.invalidations.addAll(invalidations);
    if (cursor != null) result.cursor = cursor;
    if (bootstrap != null) result.bootstrap = bootstrap;
    return result;
  }

  WatchChangesResponse._();

  factory WatchChangesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory WatchChangesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'WatchChangesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<ChangeInvalidation>(1, _omitFieldNames ? '' : 'invalidations',
        subBuilder: ChangeInvalidation.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'cursor', $pb.PbFieldType.OY)
    ..aOB(3, _omitFieldNames ? '' : 'bootstrap')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  WatchChangesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  WatchChangesResponse copyWith(void Function(WatchChangesResponse) updates) =>
      super.copyWith((message) => updates(message as WatchChangesResponse))
          as WatchChangesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static WatchChangesResponse create() => WatchChangesResponse._();
  @$core.override
  WatchChangesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static WatchChangesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<WatchChangesResponse>(create);
  static WatchChangesResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<ChangeInvalidation> get invalidations => $_getList(0);

  /// Persist cursor only after applying every invalidation in this frame. A
  /// mutation may span frames; only its final visible frame carries a cursor.
  @$pb.TagNumber(2)
  $core.List<$core.int> get cursor => $_getN(1);
  @$pb.TagNumber(2)
  set cursor($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearCursor() => $_clearField(2);

  /// Bootstrap/periodic progress frames have no invalidations and fixed-sized
  /// encrypted cursors. Hidden-only commits never cause an extra frame.
  @$pb.TagNumber(3)
  $core.bool get bootstrap => $_getBF(2);
  @$pb.TagNumber(3)
  set bootstrap($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasBootstrap() => $_has(2);
  @$pb.TagNumber(3)
  void clearBootstrap() => $_clearField(3);
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
