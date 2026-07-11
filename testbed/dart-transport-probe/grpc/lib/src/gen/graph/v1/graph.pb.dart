// This is a generated file - do not edit.
//
// Generated from graph/v1/graph.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:core' as $core;

import 'package:fixnum/fixnum.dart' as $fixnum;
import 'package:protobuf/protobuf.dart' as $pb;
import 'package:protobuf/well_known_types/google/protobuf/duration.pb.dart'
    as $2;
import 'package:protobuf/well_known_types/google/protobuf/timestamp.pb.dart'
    as $1;

import 'graph.pbenum.dart';

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

export 'graph.pbenum.dart';

enum Vertex_Value {
  float64,
  float32,
  int32,
  int64,
  uint32,
  uint64,
  bool_16,
  string,
  bytes,
  timestamp,
  duration,
  nil,
  notSet
}

class Vertex extends $pb.GeneratedMessage {
  factory Vertex({
    $core.String? key,
    $1.Timestamp? expiration,
    $core.double? float64,
    $core.double? float32,
    $core.int? int32,
    $fixnum.Int64? int64,
    $core.int? uint32,
    $fixnum.Int64? uint64,
    $core.bool? bool_16,
    $core.String? string,
    $core.List<$core.int>? bytes,
    $1.Timestamp? timestamp,
    $2.Duration? duration,
    $core.bool? nil,
  }) {
    final result = create();
    if (key != null) result.key = key;
    if (expiration != null) result.expiration = expiration;
    if (float64 != null) result.float64 = float64;
    if (float32 != null) result.float32 = float32;
    if (int32 != null) result.int32 = int32;
    if (int64 != null) result.int64 = int64;
    if (uint32 != null) result.uint32 = uint32;
    if (uint64 != null) result.uint64 = uint64;
    if (bool_16 != null) result.bool_16 = bool_16;
    if (string != null) result.string = string;
    if (bytes != null) result.bytes = bytes;
    if (timestamp != null) result.timestamp = timestamp;
    if (duration != null) result.duration = duration;
    if (nil != null) result.nil = nil;
    return result;
  }

  Vertex._();

  factory Vertex.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Vertex.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, Vertex_Value> _Vertex_ValueByTag = {
    10: Vertex_Value.float64,
    11: Vertex_Value.float32,
    12: Vertex_Value.int32,
    13: Vertex_Value.int64,
    14: Vertex_Value.uint32,
    15: Vertex_Value.uint64,
    16: Vertex_Value.bool_16,
    17: Vertex_Value.string,
    18: Vertex_Value.bytes,
    19: Vertex_Value.timestamp,
    20: Vertex_Value.duration,
    30: Vertex_Value.nil,
    0: Vertex_Value.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Vertex',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 30])
    ..aOS(1, _omitFieldNames ? '' : 'key')
    ..aOM<$1.Timestamp>(2, _omitFieldNames ? '' : 'expiration',
        subBuilder: $1.Timestamp.create)
    ..aD(10, _omitFieldNames ? '' : 'float64')
    ..aD(11, _omitFieldNames ? '' : 'float32', fieldType: $pb.PbFieldType.OF)
    ..aI(12, _omitFieldNames ? '' : 'int32')
    ..aInt64(13, _omitFieldNames ? '' : 'int64')
    ..aI(14, _omitFieldNames ? '' : 'uint32', fieldType: $pb.PbFieldType.OU3)
    ..a<$fixnum.Int64>(15, _omitFieldNames ? '' : 'uint64', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOB(16, _omitFieldNames ? '' : 'bool')
    ..aOS(17, _omitFieldNames ? '' : 'string')
    ..a<$core.List<$core.int>>(
        18, _omitFieldNames ? '' : 'bytes', $pb.PbFieldType.OY)
    ..aOM<$1.Timestamp>(19, _omitFieldNames ? '' : 'timestamp',
        subBuilder: $1.Timestamp.create)
    ..aOM<$2.Duration>(20, _omitFieldNames ? '' : 'duration',
        subBuilder: $2.Duration.create)
    ..aOB(30, _omitFieldNames ? '' : 'nil')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Vertex clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Vertex copyWith(void Function(Vertex) updates) =>
      super.copyWith((message) => updates(message as Vertex)) as Vertex;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Vertex create() => Vertex._();
  @$core.override
  Vertex createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Vertex getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Vertex>(create);
  static Vertex? _defaultInstance;

  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  @$pb.TagNumber(15)
  @$pb.TagNumber(16)
  @$pb.TagNumber(17)
  @$pb.TagNumber(18)
  @$pb.TagNumber(19)
  @$pb.TagNumber(20)
  @$pb.TagNumber(30)
  Vertex_Value whichValue() => _Vertex_ValueByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(10)
  @$pb.TagNumber(11)
  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  @$pb.TagNumber(15)
  @$pb.TagNumber(16)
  @$pb.TagNumber(17)
  @$pb.TagNumber(18)
  @$pb.TagNumber(19)
  @$pb.TagNumber(20)
  @$pb.TagNumber(30)
  void clearValue() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $core.String get key => $_getSZ(0);
  @$pb.TagNumber(1)
  set key($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasKey() => $_has(0);
  @$pb.TagNumber(1)
  void clearKey() => $_clearField(1);

  @$pb.TagNumber(2)
  $1.Timestamp get expiration => $_getN(1);
  @$pb.TagNumber(2)
  set expiration($1.Timestamp value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasExpiration() => $_has(1);
  @$pb.TagNumber(2)
  void clearExpiration() => $_clearField(2);
  @$pb.TagNumber(2)
  $1.Timestamp ensureExpiration() => $_ensure(1);

  @$pb.TagNumber(10)
  $core.double get float64 => $_getN(2);
  @$pb.TagNumber(10)
  set float64($core.double value) => $_setDouble(2, value);
  @$pb.TagNumber(10)
  $core.bool hasFloat64() => $_has(2);
  @$pb.TagNumber(10)
  void clearFloat64() => $_clearField(10);

  @$pb.TagNumber(11)
  $core.double get float32 => $_getN(3);
  @$pb.TagNumber(11)
  set float32($core.double value) => $_setFloat(3, value);
  @$pb.TagNumber(11)
  $core.bool hasFloat32() => $_has(3);
  @$pb.TagNumber(11)
  void clearFloat32() => $_clearField(11);

  @$pb.TagNumber(12)
  $core.int get int32 => $_getIZ(4);
  @$pb.TagNumber(12)
  set int32($core.int value) => $_setSignedInt32(4, value);
  @$pb.TagNumber(12)
  $core.bool hasInt32() => $_has(4);
  @$pb.TagNumber(12)
  void clearInt32() => $_clearField(12);

  @$pb.TagNumber(13)
  $fixnum.Int64 get int64 => $_getI64(5);
  @$pb.TagNumber(13)
  set int64($fixnum.Int64 value) => $_setInt64(5, value);
  @$pb.TagNumber(13)
  $core.bool hasInt64() => $_has(5);
  @$pb.TagNumber(13)
  void clearInt64() => $_clearField(13);

  @$pb.TagNumber(14)
  $core.int get uint32 => $_getIZ(6);
  @$pb.TagNumber(14)
  set uint32($core.int value) => $_setUnsignedInt32(6, value);
  @$pb.TagNumber(14)
  $core.bool hasUint32() => $_has(6);
  @$pb.TagNumber(14)
  void clearUint32() => $_clearField(14);

  @$pb.TagNumber(15)
  $fixnum.Int64 get uint64 => $_getI64(7);
  @$pb.TagNumber(15)
  set uint64($fixnum.Int64 value) => $_setInt64(7, value);
  @$pb.TagNumber(15)
  $core.bool hasUint64() => $_has(7);
  @$pb.TagNumber(15)
  void clearUint64() => $_clearField(15);

  @$pb.TagNumber(16)
  $core.bool get bool_16 => $_getBF(8);
  @$pb.TagNumber(16)
  set bool_16($core.bool value) => $_setBool(8, value);
  @$pb.TagNumber(16)
  $core.bool hasBool_16() => $_has(8);
  @$pb.TagNumber(16)
  void clearBool_16() => $_clearField(16);

  @$pb.TagNumber(17)
  $core.String get string => $_getSZ(9);
  @$pb.TagNumber(17)
  set string($core.String value) => $_setString(9, value);
  @$pb.TagNumber(17)
  $core.bool hasString() => $_has(9);
  @$pb.TagNumber(17)
  void clearString() => $_clearField(17);

  @$pb.TagNumber(18)
  $core.List<$core.int> get bytes => $_getN(10);
  @$pb.TagNumber(18)
  set bytes($core.List<$core.int> value) => $_setBytes(10, value);
  @$pb.TagNumber(18)
  $core.bool hasBytes() => $_has(10);
  @$pb.TagNumber(18)
  void clearBytes() => $_clearField(18);

  @$pb.TagNumber(19)
  $1.Timestamp get timestamp => $_getN(11);
  @$pb.TagNumber(19)
  set timestamp($1.Timestamp value) => $_setField(19, value);
  @$pb.TagNumber(19)
  $core.bool hasTimestamp() => $_has(11);
  @$pb.TagNumber(19)
  void clearTimestamp() => $_clearField(19);
  @$pb.TagNumber(19)
  $1.Timestamp ensureTimestamp() => $_ensure(11);

  @$pb.TagNumber(20)
  $2.Duration get duration => $_getN(12);
  @$pb.TagNumber(20)
  set duration($2.Duration value) => $_setField(20, value);
  @$pb.TagNumber(20)
  $core.bool hasDuration() => $_has(12);
  @$pb.TagNumber(20)
  void clearDuration() => $_clearField(20);
  @$pb.TagNumber(20)
  $2.Duration ensureDuration() => $_ensure(12);

  /// nil signals that the vertex carries no value (an "existence-only"
  /// marker). The bool itself is always true when present; the variant
  /// exists so the oneof can distinguish "explicitly nil" from "unset".
  @$pb.TagNumber(30)
  $core.bool get nil => $_getBF(13);
  @$pb.TagNumber(30)
  set nil($core.bool value) => $_setBool(13, value);
  @$pb.TagNumber(30)
  $core.bool hasNil() => $_has(13);
  @$pb.TagNumber(30)
  void clearNil() => $_clearField(30);
}

class Edge extends $pb.GeneratedMessage {
  factory Edge({
    $core.String? tail,
    $core.String? head,
    $core.double? weight,
    $1.Timestamp? expiration,
  }) {
    final result = create();
    if (tail != null) result.tail = tail;
    if (head != null) result.head = head;
    if (weight != null) result.weight = weight;
    if (expiration != null) result.expiration = expiration;
    return result;
  }

  Edge._();

  factory Edge.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Edge.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Edge',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tail')
    ..aOS(2, _omitFieldNames ? '' : 'head')
    ..aD(3, _omitFieldNames ? '' : 'weight', fieldType: $pb.PbFieldType.OF)
    ..aOM<$1.Timestamp>(4, _omitFieldNames ? '' : 'expiration',
        subBuilder: $1.Timestamp.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Edge clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Edge copyWith(void Function(Edge) updates) =>
      super.copyWith((message) => updates(message as Edge)) as Edge;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Edge create() => Edge._();
  @$core.override
  Edge createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Edge getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Edge>(create);
  static Edge? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tail => $_getSZ(0);
  @$pb.TagNumber(1)
  set tail($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTail() => $_has(0);
  @$pb.TagNumber(1)
  void clearTail() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get head => $_getSZ(1);
  @$pb.TagNumber(2)
  set head($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHead() => $_has(1);
  @$pb.TagNumber(2)
  void clearHead() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.double get weight => $_getN(2);
  @$pb.TagNumber(3)
  set weight($core.double value) => $_setFloat(2, value);
  @$pb.TagNumber(3)
  $core.bool hasWeight() => $_has(2);
  @$pb.TagNumber(3)
  void clearWeight() => $_clearField(3);

  @$pb.TagNumber(4)
  $1.Timestamp get expiration => $_getN(3);
  @$pb.TagNumber(4)
  set expiration($1.Timestamp value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasExpiration() => $_has(3);
  @$pb.TagNumber(4)
  void clearExpiration() => $_clearField(4);
  @$pb.TagNumber(4)
  $1.Timestamp ensureExpiration() => $_ensure(3);
}

class Graph extends $pb.GeneratedMessage {
  factory Graph({
    $core.Iterable<Vertex>? vertices,
    $core.Iterable<Edge>? edges,
  }) {
    final result = create();
    if (vertices != null) result.vertices.addAll(vertices);
    if (edges != null) result.edges.addAll(edges);
    return result;
  }

  Graph._();

  factory Graph.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Graph.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Graph',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Vertex>(1, _omitFieldNames ? '' : 'vertices',
        subBuilder: Vertex.create)
    ..pPM<Edge>(2, _omitFieldNames ? '' : 'edges', subBuilder: Edge.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Graph clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Graph copyWith(void Function(Graph) updates) =>
      super.copyWith((message) => updates(message as Graph)) as Graph;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Graph create() => Graph._();
  @$core.override
  Graph createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static Graph getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Graph>(create);
  static Graph? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<Vertex> get vertices => $_getList(0);

  @$pb.TagNumber(2)
  $pb.PbList<Edge> get edges => $_getList(1);
}

enum IlluminateRequest_Params { bfs, ppr, community, notSet }

class IlluminateRequest extends $pb.GeneratedMessage {
  factory IlluminateRequest({
    $core.String? seed,
    Weighting? weighting,
    $core.String? vertexPrefix,
    BfsParams? bfs,
    PprParams? ppr,
    LocalCommunityParams? community,
  }) {
    final result = create();
    if (seed != null) result.seed = seed;
    if (weighting != null) result.weighting = weighting;
    if (vertexPrefix != null) result.vertexPrefix = vertexPrefix;
    if (bfs != null) result.bfs = bfs;
    if (ppr != null) result.ppr = ppr;
    if (community != null) result.community = community;
    return result;
  }

  IlluminateRequest._();

  factory IlluminateRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory IlluminateRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, IlluminateRequest_Params>
      _IlluminateRequest_ParamsByTag = {
    12: IlluminateRequest_Params.bfs,
    13: IlluminateRequest_Params.ppr,
    14: IlluminateRequest_Params.community,
    0: IlluminateRequest_Params.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'IlluminateRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [12, 13, 14])
    ..aOS(1, _omitFieldNames ? '' : 'seed')
    ..aE<Weighting>(8, _omitFieldNames ? '' : 'weighting',
        enumValues: Weighting.values)
    ..aOS(9, _omitFieldNames ? '' : 'vertexPrefix')
    ..aOM<BfsParams>(12, _omitFieldNames ? '' : 'bfs',
        subBuilder: BfsParams.create)
    ..aOM<PprParams>(13, _omitFieldNames ? '' : 'ppr',
        subBuilder: PprParams.create)
    ..aOM<LocalCommunityParams>(14, _omitFieldNames ? '' : 'community',
        subBuilder: LocalCommunityParams.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  IlluminateRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  IlluminateRequest copyWith(void Function(IlluminateRequest) updates) =>
      super.copyWith((message) => updates(message as IlluminateRequest))
          as IlluminateRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static IlluminateRequest create() => IlluminateRequest._();
  @$core.override
  IlluminateRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static IlluminateRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<IlluminateRequest>(create);
  static IlluminateRequest? _defaultInstance;

  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  IlluminateRequest_Params whichParams() =>
      _IlluminateRequest_ParamsByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(12)
  @$pb.TagNumber(13)
  @$pb.TagNumber(14)
  void clearParams() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $core.String get seed => $_getSZ(0);
  @$pb.TagNumber(1)
  set seed($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSeed() => $_has(0);
  @$pb.TagNumber(1)
  void clearSeed() => $_clearField(1);

  /// weighting and vertex_prefix are the axes genuinely shared by every
  /// traversal family, so they stay top-level.
  @$pb.TagNumber(8)
  Weighting get weighting => $_getN(1);
  @$pb.TagNumber(8)
  set weighting(Weighting value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasWeighting() => $_has(1);
  @$pb.TagNumber(8)
  void clearWeighting() => $_clearField(8);

  /// vertex_prefix, when non-empty, restricts the traversal frontier to the
  /// vertices whose key has this prefix. The seed is always retained as the
  /// anchor even if it does not match. Empty = no filter. Applied BEFORE
  /// per-hop top-k and BEFORE any reduction (induced-subgraph semantics:
  /// non-matching vertices are not traversable bridges).
  @$pb.TagNumber(9)
  $core.String get vertexPrefix => $_getSZ(2);
  @$pb.TagNumber(9)
  set vertexPrefix($core.String value) => $_setString(2, value);
  @$pb.TagNumber(9)
  $core.bool hasVertexPrefix() => $_has(2);
  @$pb.TagNumber(9)
  void clearVertexPrefix() => $_clearField(9);

  @$pb.TagNumber(12)
  BfsParams get bfs => $_getN(3);
  @$pb.TagNumber(12)
  set bfs(BfsParams value) => $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasBfs() => $_has(3);
  @$pb.TagNumber(12)
  void clearBfs() => $_clearField(12);
  @$pb.TagNumber(12)
  BfsParams ensureBfs() => $_ensure(3);

  @$pb.TagNumber(13)
  PprParams get ppr => $_getN(4);
  @$pb.TagNumber(13)
  set ppr(PprParams value) => $_setField(13, value);
  @$pb.TagNumber(13)
  $core.bool hasPpr() => $_has(4);
  @$pb.TagNumber(13)
  void clearPpr() => $_clearField(13);
  @$pb.TagNumber(13)
  PprParams ensurePpr() => $_ensure(4);

  @$pb.TagNumber(14)
  LocalCommunityParams get community => $_getN(5);
  @$pb.TagNumber(14)
  set community(LocalCommunityParams value) => $_setField(14, value);
  @$pb.TagNumber(14)
  $core.bool hasCommunity() => $_has(5);
  @$pb.TagNumber(14)
  void clearCommunity() => $_clearField(14);
  @$pb.TagNumber(14)
  LocalCommunityParams ensureCommunity() => $_ensure(5);
}

/// BfsParams tunes the greedy per-hop top-k BFS walk and its optional
/// post-traversal tree reduction.
class BfsParams extends $pb.GeneratedMessage {
  factory BfsParams({
    $core.int? step,
    $core.int? fanOut,
    Objective? objective,
    Reduction? reduction,
  }) {
    final result = create();
    if (step != null) result.step = step;
    if (fanOut != null) result.fanOut = fanOut;
    if (objective != null) result.objective = objective;
    if (reduction != null) result.reduction = reduction;
    return result;
  }

  BfsParams._();

  factory BfsParams.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BfsParams.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BfsParams',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'step', fieldType: $pb.PbFieldType.OU3)
    ..aI(2, _omitFieldNames ? '' : 'fanOut', fieldType: $pb.PbFieldType.OU3)
    ..aE<Objective>(3, _omitFieldNames ? '' : 'objective',
        enumValues: Objective.values)
    ..aE<Reduction>(4, _omitFieldNames ? '' : 'reduction',
        enumValues: Reduction.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BfsParams clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BfsParams copyWith(void Function(BfsParams) updates) =>
      super.copyWith((message) => updates(message as BfsParams)) as BfsParams;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BfsParams create() => BfsParams._();
  @$core.override
  BfsParams createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BfsParams getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<BfsParams>(create);
  static BfsParams? _defaultInstance;

  /// step is the BFS depth and MUST be positive. 0 is INVALID_ARGUMENT.
  @$pb.TagNumber(1)
  $core.int get step => $_getIZ(0);
  @$pb.TagNumber(1)
  set step($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasStep() => $_has(0);
  @$pb.TagNumber(1)
  void clearStep() => $_clearField(1);

  /// fan_out is the per-hop top-k prune: at each hop only the fan_out
  /// strongest (or cheapest, under OBJECTIVE_MINIMIZE) edges survive.
  /// It MUST be positive. 0 is INVALID_ARGUMENT. (Formerly the overloaded "k".)
  @$pb.TagNumber(2)
  $core.int get fanOut => $_getIZ(1);
  @$pb.TagNumber(2)
  set fanOut($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasFanOut() => $_has(1);
  @$pb.TagNumber(2)
  void clearFanOut() => $_clearField(2);

  /// objective governs BOTH the per-hop pruning and the reduction direction
  /// (#560), so a cost-minimiser is never handed a candidate set already
  /// pruned to the costliest edges. UNSPECIFIED = MAXIMIZE.
  @$pb.TagNumber(3)
  Objective get objective => $_getN(2);
  @$pb.TagNumber(3)
  set objective(Objective value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasObjective() => $_has(2);
  @$pb.TagNumber(3)
  void clearObjective() => $_clearField(3);

  /// reduction optionally reduces the discovered neighbourhood to a tree
  /// rooted at the seed. UNSPECIFIED = no reduction (raw subgraph).
  @$pb.TagNumber(4)
  Reduction get reduction => $_getN(3);
  @$pb.TagNumber(4)
  set reduction(Reduction value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasReduction() => $_has(3);
  @$pb.TagNumber(4)
  void clearReduction() => $_clearField(4);
}

/// LocalCommunityParams extracts the conductance-optimal local community
/// around the seed (#845): PageRank-Nibble — the same ACL forward-push the
/// PPR family runs, followed by a sweep cut that orders touched vertices by
/// p(v)/deg(v) and takes the prefix minimising directed weighted
/// conductance. Unlike the PPR relevance star, the response preserves
/// structure: it is the INDUCED SUBGRAPH on the selected members — the real
/// live edges among them with their expirations and weighting-transformed
/// weights (weighting RAW/UNSPECIFIED = the verbatim stored weight), in the
/// same response shape as the BFS family. Because the returned weights carry
/// the weighting transform, a reduction over the community honours weighting
/// too.
class LocalCommunityParams extends $pb.GeneratedMessage {
  factory LocalCommunityParams({
    $core.int? maxSize,
    $core.double? restartProb,
    $core.double? epsilon,
    Reduction? reduction,
    Objective? objective,
  }) {
    final result = create();
    if (maxSize != null) result.maxSize = maxSize;
    if (restartProb != null) result.restartProb = restartProb;
    if (epsilon != null) result.epsilon = epsilon;
    if (reduction != null) result.reduction = reduction;
    if (objective != null) result.objective = objective;
    return result;
  }

  LocalCommunityParams._();

  factory LocalCommunityParams.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory LocalCommunityParams.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'LocalCommunityParams',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'maxSize', fieldType: $pb.PbFieldType.OU3)
    ..aD(2, _omitFieldNames ? '' : 'restartProb', fieldType: $pb.PbFieldType.OF)
    ..aD(3, _omitFieldNames ? '' : 'epsilon', fieldType: $pb.PbFieldType.OF)
    ..aE<Reduction>(4, _omitFieldNames ? '' : 'reduction',
        enumValues: Reduction.values)
    ..aE<Objective>(5, _omitFieldNames ? '' : 'objective',
        enumValues: Objective.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LocalCommunityParams clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  LocalCommunityParams copyWith(void Function(LocalCommunityParams) updates) =>
      super.copyWith((message) => updates(message as LocalCommunityParams))
          as LocalCommunityParams;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static LocalCommunityParams create() => LocalCommunityParams._();
  @$core.override
  LocalCommunityParams createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static LocalCommunityParams getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<LocalCommunityParams>(create);
  static LocalCommunityParams? _defaultInstance;

  /// max_size is an UPPER BOUND on community size — NOT an exact count. The
  /// sweep stops at the conductance minimum, which may come before max_size.
  /// 0 = unbounded (the sweep alone decides). (The PPR family's top_n by
  /// contrast always returns exactly N regardless of the natural boundary.)
  @$pb.TagNumber(1)
  $core.int get maxSize => $_getIZ(0);
  @$pb.TagNumber(1)
  set maxSize($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasMaxSize() => $_has(0);
  @$pb.TagNumber(1)
  void clearMaxSize() => $_clearField(1);

  /// restart_prob (α) — locality knob, same semantics and defaults as
  /// PprParams.restart_prob: higher α → tighter, more seed-proximate
  /// community. Honoured only in (0,1); unset falls back to 0.15.
  @$pb.TagNumber(2)
  $core.double get restartProb => $_getN(1);
  @$pb.TagNumber(2)
  set restartProb($core.double value) => $_setFloat(1, value);
  @$pb.TagNumber(2)
  $core.bool hasRestartProb() => $_has(1);
  @$pb.TagNumber(2)
  void clearRestartProb() => $_clearField(2);

  /// epsilon (ε) — push accuracy / work budget, same semantics and defaults
  /// as PprParams.epsilon: smaller ε → more accurate mass and a larger
  /// touched set. Honoured only when > 0; unset falls back to 1e-4.
  @$pb.TagNumber(3)
  $core.double get epsilon => $_getN(2);
  @$pb.TagNumber(3)
  set epsilon($core.double value) => $_setFloat(2, value);
  @$pb.TagNumber(3)
  $core.bool hasEpsilon() => $_has(2);
  @$pb.TagNumber(3)
  void clearEpsilon() => $_clearField(3);

  /// reduction optionally reduces the community to a tree VIEW rooted at the
  /// seed (for visualization, explanation paths, token-bounded consumption).
  /// The induced subgraph stays the source of truth — density is exactly
  /// what the sweep selected for. Sweep prefixes need not be connected, so
  /// the tree spans only the seed's reachable component within the
  /// community; unreachable members are still returned as ISOLATED vertices
  /// (membership preserved, no edges fabricated). UNSPECIFIED = the full
  /// induced subgraph (the default).
  @$pb.TagNumber(4)
  Reduction get reduction => $_getN(3);
  @$pb.TagNumber(4)
  set reduction(Reduction value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasReduction() => $_has(3);
  @$pb.TagNumber(4)
  void clearReduction() => $_clearField(4);

  /// objective sets the direction/cost mapping for the reduction ONLY
  /// (membership is decided by PPR mass, not by this): MINIMIZE → identity
  /// cost, MAXIMIZE/UNSPECIFIED → inverse cost, exactly as the BFS family.
  /// Ignored when reduction is UNSPECIFIED.
  @$pb.TagNumber(5)
  Objective get objective => $_getN(4);
  @$pb.TagNumber(5)
  set objective(Objective value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasObjective() => $_has(4);
  @$pb.TagNumber(5)
  void clearObjective() => $_clearField(5);
}

/// PprParams runs seed-anchored Personalized PageRank (Random-Walk-with-
/// Restart) by local forward-push (Andersen–Chung–Lang) instead of the BFS
/// walk (#801). The result is a relevance star rooted at the seed — the
/// seed→v edge weight is π[v], the PPR mass v accumulates for this seed.
/// PPR is intrinsically a relevance maximiser and has no per-hop step
/// semantics, which is why neither knob exists here.
class PprParams extends $pb.GeneratedMessage {
  factory PprParams({
    $core.int? topN,
    $core.double? restartProb,
    $core.double? epsilon,
  }) {
    final result = create();
    if (topN != null) result.topN = topN;
    if (restartProb != null) result.restartProb = restartProb;
    if (epsilon != null) result.epsilon = epsilon;
    return result;
  }

  PprParams._();

  factory PprParams.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PprParams.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PprParams',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'topN', fieldType: $pb.PbFieldType.OU3)
    ..aD(2, _omitFieldNames ? '' : 'restartProb', fieldType: $pb.PbFieldType.OF)
    ..aD(3, _omitFieldNames ? '' : 'epsilon', fieldType: $pb.PbFieldType.OF)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PprParams clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PprParams copyWith(void Function(PprParams) updates) =>
      super.copyWith((message) => updates(message as PprParams)) as PprParams;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PprParams create() => PprParams._();
  @$core.override
  PprParams createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PprParams getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<PprParams>(create);
  static PprParams? _defaultInstance;

  /// top_n caps the star to the top-N vertices by mass. 0 = every
  /// positive-mass vertex. (Formerly the overloaded "k".)
  @$pb.TagNumber(1)
  $core.int get topN => $_getIZ(0);
  @$pb.TagNumber(1)
  set topN($core.int value) => $_setUnsignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTopN() => $_has(0);
  @$pb.TagNumber(1)
  void clearTopN() => $_clearField(1);

  /// restart_prob (α) is the teleport-to-seed probability — the locality
  /// knob: higher α (≈0.5) yields a tighter, seed-proximate set, lower α
  /// (≈0.15) a broader one. Honoured only in (0,1); unset/out-of-range
  /// falls back to the server default 0.15.
  @$pb.TagNumber(2)
  $core.double get restartProb => $_getN(1);
  @$pb.TagNumber(2)
  set restartProb($core.double value) => $_setFloat(1, value);
  @$pb.TagNumber(2)
  $core.bool hasRestartProb() => $_has(1);
  @$pb.TagNumber(2)
  void clearRestartProb() => $_clearField(2);

  /// epsilon (ε) is the forward-push residual threshold: smaller ε is more
  /// accurate but touches more vertices (work is O(1/(α·ε)), independent of
  /// graph size). Honoured only when > 0; unset falls back to the server
  /// default 1e-4.
  @$pb.TagNumber(3)
  $core.double get epsilon => $_getN(2);
  @$pb.TagNumber(3)
  set epsilon($core.double value) => $_setFloat(2, value);
  @$pb.TagNumber(3)
  $core.bool hasEpsilon() => $_has(2);
  @$pb.TagNumber(3)
  void clearEpsilon() => $_clearField(3);
}

class IlluminateResponse extends $pb.GeneratedMessage {
  factory IlluminateResponse({
    Graph? graph,
  }) {
    final result = create();
    if (graph != null) result.graph = graph;
    return result;
  }

  IlluminateResponse._();

  factory IlluminateResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory IlluminateResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'IlluminateResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<Graph>(1, _omitFieldNames ? '' : 'graph', subBuilder: Graph.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  IlluminateResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  IlluminateResponse copyWith(void Function(IlluminateResponse) updates) =>
      super.copyWith((message) => updates(message as IlluminateResponse))
          as IlluminateResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static IlluminateResponse create() => IlluminateResponse._();
  @$core.override
  IlluminateResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static IlluminateResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<IlluminateResponse>(create);
  static IlluminateResponse? _defaultInstance;

  @$pb.TagNumber(1)
  Graph get graph => $_getN(0);
  @$pb.TagNumber(1)
  set graph(Graph value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasGraph() => $_has(0);
  @$pb.TagNumber(1)
  void clearGraph() => $_clearField(1);
  @$pb.TagNumber(1)
  Graph ensureGraph() => $_ensure(0);
}

class GetVertexRequest extends $pb.GeneratedMessage {
  factory GetVertexRequest({
    $core.String? key,
  }) {
    final result = create();
    if (key != null) result.key = key;
    return result;
  }

  GetVertexRequest._();

  factory GetVertexRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetVertexRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetVertexRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'key')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVertexRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVertexRequest copyWith(void Function(GetVertexRequest) updates) =>
      super.copyWith((message) => updates(message as GetVertexRequest))
          as GetVertexRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetVertexRequest create() => GetVertexRequest._();
  @$core.override
  GetVertexRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetVertexRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetVertexRequest>(create);
  static GetVertexRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get key => $_getSZ(0);
  @$pb.TagNumber(1)
  set key($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasKey() => $_has(0);
  @$pb.TagNumber(1)
  void clearKey() => $_clearField(1);
}

class GetVertexResponse extends $pb.GeneratedMessage {
  factory GetVertexResponse({
    Vertex? vertex,
  }) {
    final result = create();
    if (vertex != null) result.vertex = vertex;
    return result;
  }

  GetVertexResponse._();

  factory GetVertexResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetVertexResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetVertexResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<Vertex>(1, _omitFieldNames ? '' : 'vertex', subBuilder: Vertex.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVertexResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVertexResponse copyWith(void Function(GetVertexResponse) updates) =>
      super.copyWith((message) => updates(message as GetVertexResponse))
          as GetVertexResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetVertexResponse create() => GetVertexResponse._();
  @$core.override
  GetVertexResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetVertexResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetVertexResponse>(create);
  static GetVertexResponse? _defaultInstance;

  @$pb.TagNumber(1)
  Vertex get vertex => $_getN(0);
  @$pb.TagNumber(1)
  set vertex(Vertex value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVertex() => $_has(0);
  @$pb.TagNumber(1)
  void clearVertex() => $_clearField(1);
  @$pb.TagNumber(1)
  Vertex ensureVertex() => $_ensure(0);
}

/// GetVerticesRequest reads several vertices in one round trip. Subject to
/// the same MaxBatchSize / MaxKeyLen guard rails as the write RPCs.
class GetVerticesRequest extends $pb.GeneratedMessage {
  factory GetVerticesRequest({
    $core.Iterable<$core.String>? keys,
  }) {
    final result = create();
    if (keys != null) result.keys.addAll(keys);
    return result;
  }

  GetVerticesRequest._();

  factory GetVerticesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetVerticesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetVerticesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPS(1, _omitFieldNames ? '' : 'keys')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVerticesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVerticesRequest copyWith(void Function(GetVerticesRequest) updates) =>
      super.copyWith((message) => updates(message as GetVerticesRequest))
          as GetVerticesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetVerticesRequest create() => GetVerticesRequest._();
  @$core.override
  GetVerticesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetVerticesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetVerticesRequest>(create);
  static GetVerticesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<$core.String> get keys => $_getList(0);
}

class GetVerticesResponse extends $pb.GeneratedMessage {
  factory GetVerticesResponse({
    $core.Iterable<Vertex>? vertices,
    $core.Iterable<$core.String>? missing,
  }) {
    final result = create();
    if (vertices != null) result.vertices.addAll(vertices);
    if (missing != null) result.missing.addAll(missing);
    return result;
  }

  GetVerticesResponse._();

  factory GetVerticesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetVerticesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetVerticesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Vertex>(1, _omitFieldNames ? '' : 'vertices',
        subBuilder: Vertex.create)
    ..pPS(2, _omitFieldNames ? '' : 'missing')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVerticesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetVerticesResponse copyWith(void Function(GetVerticesResponse) updates) =>
      super.copyWith((message) => updates(message as GetVerticesResponse))
          as GetVerticesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetVerticesResponse create() => GetVerticesResponse._();
  @$core.override
  GetVerticesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetVerticesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetVerticesResponse>(create);
  static GetVerticesResponse? _defaultInstance;

  /// vertices contains every key that was present at the time of the call.
  /// Order is unspecified; clients should match by Vertex.key.
  @$pb.TagNumber(1)
  $pb.PbList<Vertex> get vertices => $_getList(0);

  /// missing lists the requested keys that did not exist (or had expired).
  @$pb.TagNumber(2)
  $pb.PbList<$core.String> get missing => $_getList(1);
}

/// PutVertexRequest writes a single vertex with upsert semantics. The
/// Vertex.key field selects the target row and Vertex.expiration controls
/// the TTL (absolute time). This is the singular convenience wrapper over
/// PutVertices and shares its guard rails.
class PutVertexRequest extends $pb.GeneratedMessage {
  factory PutVertexRequest({
    Vertex? vertex,
    $core.bool? ifAbsent,
  }) {
    final result = create();
    if (vertex != null) result.vertex = vertex;
    if (ifAbsent != null) result.ifAbsent = ifAbsent;
    return result;
  }

  PutVertexRequest._();

  factory PutVertexRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutVertexRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutVertexRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<Vertex>(1, _omitFieldNames ? '' : 'vertex', subBuilder: Vertex.create)
    ..aOB(2, _omitFieldNames ? '' : 'ifAbsent')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVertexRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVertexRequest copyWith(void Function(PutVertexRequest) updates) =>
      super.copyWith((message) => updates(message as PutVertexRequest))
          as PutVertexRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutVertexRequest create() => PutVertexRequest._();
  @$core.override
  PutVertexRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutVertexRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutVertexRequest>(create);
  static PutVertexRequest? _defaultInstance;

  @$pb.TagNumber(1)
  Vertex get vertex => $_getN(0);
  @$pb.TagNumber(1)
  set vertex(Vertex value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVertex() => $_has(0);
  @$pb.TagNumber(1)
  void clearVertex() => $_clearField(1);
  @$pb.TagNumber(1)
  Vertex ensureVertex() => $_ensure(0);

  /// When true, the write applies only if no live vertex exists at the key.
  /// An existing live vertex leaves value and expiration untouched (SET NX).
  @$pb.TagNumber(2)
  $core.bool get ifAbsent => $_getBF(1);
  @$pb.TagNumber(2)
  set ifAbsent($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasIfAbsent() => $_has(1);
  @$pb.TagNumber(2)
  void clearIfAbsent() => $_clearField(2);
}

class PutVertexResponse extends $pb.GeneratedMessage {
  factory PutVertexResponse({
    $core.bool? written,
  }) {
    final result = create();
    if (written != null) result.written = written;
    return result;
  }

  PutVertexResponse._();

  factory PutVertexResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutVertexResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutVertexResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'written')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVertexResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVertexResponse copyWith(void Function(PutVertexResponse) updates) =>
      super.copyWith((message) => updates(message as PutVertexResponse))
          as PutVertexResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutVertexResponse create() => PutVertexResponse._();
  @$core.override
  PutVertexResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutVertexResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutVertexResponse>(create);
  static PutVertexResponse? _defaultInstance;

  /// True when the write was applied; false when it was skipped — either
  /// because if_absent found a live vertex already at the key, or because the
  /// vertex was born expired (expiration already past) and discarded. Always
  /// true for an unconditional put with a live expiration.
  @$pb.TagNumber(1)
  $core.bool get written => $_getBF(0);
  @$pb.TagNumber(1)
  set written($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasWritten() => $_has(0);
  @$pb.TagNumber(1)
  void clearWritten() => $_clearField(1);
}

/// PutVerticesRequest writes vertices with upsert semantics: each Vertex.key
/// replaces any existing value at that key. Use the Vertex.expiration field
/// (absolute time) to control TTL.
class PutVerticesRequest extends $pb.GeneratedMessage {
  factory PutVerticesRequest({
    $core.Iterable<Vertex>? vertices,
    $core.bool? ifAbsent,
  }) {
    final result = create();
    if (vertices != null) result.vertices.addAll(vertices);
    if (ifAbsent != null) result.ifAbsent = ifAbsent;
    return result;
  }

  PutVerticesRequest._();

  factory PutVerticesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutVerticesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutVerticesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Vertex>(1, _omitFieldNames ? '' : 'vertices',
        subBuilder: Vertex.create)
    ..aOB(2, _omitFieldNames ? '' : 'ifAbsent')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVerticesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVerticesRequest copyWith(void Function(PutVerticesRequest) updates) =>
      super.copyWith((message) => updates(message as PutVerticesRequest))
          as PutVerticesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutVerticesRequest create() => PutVerticesRequest._();
  @$core.override
  PutVerticesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutVerticesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutVerticesRequest>(create);
  static PutVerticesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<Vertex> get vertices => $_getList(0);

  /// When true, each write applies only if no live vertex exists at its key;
  /// keys with a live vertex are left untouched and reported in
  /// PutVerticesResponse.skipped_keys.
  @$pb.TagNumber(2)
  $core.bool get ifAbsent => $_getBF(1);
  @$pb.TagNumber(2)
  set ifAbsent($core.bool value) => $_setBool(1, value);
  @$pb.TagNumber(2)
  $core.bool hasIfAbsent() => $_has(1);
  @$pb.TagNumber(2)
  void clearIfAbsent() => $_clearField(2);
}

class PutVerticesResponse extends $pb.GeneratedMessage {
  factory PutVerticesResponse({
    $core.int? written,
    $core.Iterable<$core.String>? skippedKeys,
  }) {
    final result = create();
    if (written != null) result.written = written;
    if (skippedKeys != null) result.skippedKeys.addAll(skippedKeys);
    return result;
  }

  PutVerticesResponse._();

  factory PutVerticesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutVerticesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutVerticesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'written')
    ..pPS(2, _omitFieldNames ? '' : 'skippedKeys')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVerticesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutVerticesResponse copyWith(void Function(PutVerticesResponse) updates) =>
      super.copyWith((message) => updates(message as PutVerticesResponse))
          as PutVerticesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutVerticesResponse create() => PutVerticesResponse._();
  @$core.override
  PutVerticesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutVerticesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutVerticesResponse>(create);
  static PutVerticesResponse? _defaultInstance;

  /// Number of vertices actually written. For an unconditional put this equals
  /// the request size on success; under if_absent it excludes skipped keys. A
  /// vertex whose expiration is already past is discarded and counts as neither
  /// written nor skipped (it appears in neither this count nor skipped_keys).
  @$pb.TagNumber(1)
  $core.int get written => $_getIZ(0);
  @$pb.TagNumber(1)
  set written($core.int value) => $_setSignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasWritten() => $_has(0);
  @$pb.TagNumber(1)
  void clearWritten() => $_clearField(1);

  /// Keys skipped because a live vertex already existed (if_absent only). A
  /// born-expired vertex is NOT reported here — it is silently discarded.
  @$pb.TagNumber(2)
  $pb.PbList<$core.String> get skippedKeys => $_getList(1);
}

/// DeleteVertexRequest removes the vertex at `key`. Edges incident to the
/// removed vertex are NOT eagerly cascaded: the periodic GC loop reaps
/// orphaned (tail, head) rows on its next tick, and reads against missing
/// endpoints return NotFound in the meantime.
class DeleteVertexRequest extends $pb.GeneratedMessage {
  factory DeleteVertexRequest({
    $core.String? key,
  }) {
    final result = create();
    if (key != null) result.key = key;
    return result;
  }

  DeleteVertexRequest._();

  factory DeleteVertexRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteVertexRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteVertexRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'key')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVertexRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVertexRequest copyWith(void Function(DeleteVertexRequest) updates) =>
      super.copyWith((message) => updates(message as DeleteVertexRequest))
          as DeleteVertexRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteVertexRequest create() => DeleteVertexRequest._();
  @$core.override
  DeleteVertexRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteVertexRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteVertexRequest>(create);
  static DeleteVertexRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get key => $_getSZ(0);
  @$pb.TagNumber(1)
  set key($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasKey() => $_has(0);
  @$pb.TagNumber(1)
  void clearKey() => $_clearField(1);
}

class DeleteVertexResponse extends $pb.GeneratedMessage {
  factory DeleteVertexResponse({
    $core.bool? existed,
  }) {
    final result = create();
    if (existed != null) result.existed = existed;
    return result;
  }

  DeleteVertexResponse._();

  factory DeleteVertexResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteVertexResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteVertexResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'existed')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVertexResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVertexResponse copyWith(void Function(DeleteVertexResponse) updates) =>
      super.copyWith((message) => updates(message as DeleteVertexResponse))
          as DeleteVertexResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteVertexResponse create() => DeleteVertexResponse._();
  @$core.override
  DeleteVertexResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteVertexResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteVertexResponse>(create);
  static DeleteVertexResponse? _defaultInstance;

  /// existed is true if the vertex was present and removed by this call.
  @$pb.TagNumber(1)
  $core.bool get existed => $_getBF(0);
  @$pb.TagNumber(1)
  set existed($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasExisted() => $_has(0);
  @$pb.TagNumber(1)
  void clearExisted() => $_clearField(1);
}

/// DeleteVerticesRequest removes several vertices in one round trip. Same
/// cascade semantics as DeleteVertex (edges reaped lazily by the GC loop).
/// Subject to the same MaxBatchSize / MaxKeyLen guard rails as the put RPCs.
class DeleteVerticesRequest extends $pb.GeneratedMessage {
  factory DeleteVerticesRequest({
    $core.Iterable<$core.String>? keys,
  }) {
    final result = create();
    if (keys != null) result.keys.addAll(keys);
    return result;
  }

  DeleteVerticesRequest._();

  factory DeleteVerticesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteVerticesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteVerticesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPS(1, _omitFieldNames ? '' : 'keys')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesRequest copyWith(
          void Function(DeleteVerticesRequest) updates) =>
      super.copyWith((message) => updates(message as DeleteVerticesRequest))
          as DeleteVerticesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteVerticesRequest create() => DeleteVerticesRequest._();
  @$core.override
  DeleteVerticesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteVerticesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteVerticesRequest>(create);
  static DeleteVerticesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<$core.String> get keys => $_getList(0);
}

class DeleteVerticesResponse extends $pb.GeneratedMessage {
  factory DeleteVerticesResponse({
    $core.int? deleted,
  }) {
    final result = create();
    if (deleted != null) result.deleted = deleted;
    return result;
  }

  DeleteVerticesResponse._();

  factory DeleteVerticesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteVerticesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteVerticesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'deleted')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesResponse copyWith(
          void Function(DeleteVerticesResponse) updates) =>
      super.copyWith((message) => updates(message as DeleteVerticesResponse))
          as DeleteVerticesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteVerticesResponse create() => DeleteVerticesResponse._();
  @$core.override
  DeleteVerticesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteVerticesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteVerticesResponse>(create);
  static DeleteVerticesResponse? _defaultInstance;

  /// Number of keys the server attempted to delete (equals len(keys) on success).
  @$pb.TagNumber(1)
  $core.int get deleted => $_getIZ(0);
  @$pb.TagNumber(1)
  set deleted($core.int value) => $_setSignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDeleted() => $_has(0);
  @$pb.TagNumber(1)
  void clearDeleted() => $_clearField(1);
}

/// ScanVerticesRequest streams vertices whose key starts with `prefix` in
/// lexicographic order. `limit` caps the number returned in one call; the
/// server enforces a default when `limit == 0` and a hard maximum (see
/// RateLimitConfig / ScanConfig on the server). `cursor` MUST be treated as
/// opaque bytes by clients — pass back exactly what the previous response
/// returned in `next_cursor`. An empty `cursor` starts from the beginning.
class ScanVerticesRequest extends $pb.GeneratedMessage {
  factory ScanVerticesRequest({
    $core.String? prefix,
    $core.int? limit,
    $core.List<$core.int>? cursor,
    ScanOrder? order,
  }) {
    final result = create();
    if (prefix != null) result.prefix = prefix;
    if (limit != null) result.limit = limit;
    if (cursor != null) result.cursor = cursor;
    if (order != null) result.order = order;
    return result;
  }

  ScanVerticesRequest._();

  factory ScanVerticesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ScanVerticesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ScanVerticesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'prefix')
    ..aI(2, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'cursor', $pb.PbFieldType.OY)
    ..aE<ScanOrder>(4, _omitFieldNames ? '' : 'order',
        enumValues: ScanOrder.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVerticesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVerticesRequest copyWith(void Function(ScanVerticesRequest) updates) =>
      super.copyWith((message) => updates(message as ScanVerticesRequest))
          as ScanVerticesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ScanVerticesRequest create() => ScanVerticesRequest._();
  @$core.override
  ScanVerticesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ScanVerticesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ScanVerticesRequest>(create);
  static ScanVerticesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get prefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set prefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearPrefix() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get limit => $_getIZ(1);
  @$pb.TagNumber(2)
  set limit($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLimit() => $_has(1);
  @$pb.TagNumber(2)
  void clearLimit() => $_clearField(2);

  /// NOTE: opaque to the caller; not interchangeable with cursors from
  /// other Scan* RPCs (e.g. ScanEdges). Cross-feeding is rejected with
  /// INVALID_ARGUMENT rather than silently restarting the scan.
  @$pb.TagNumber(3)
  $core.List<$core.int> get cursor => $_getN(2);
  @$pb.TagNumber(3)
  set cursor($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCursor() => $_has(2);
  @$pb.TagNumber(3)
  void clearCursor() => $_clearField(3);

  /// order selects ascending (default) or descending key order. The cursor
  /// is order-bound: a next_cursor minted by an ascending page is rejected
  /// with INVALID_ARGUMENT if fed back into a descending scan (and vice
  /// versa), matching the cross-RPC cursor-rejection precedent.
  @$pb.TagNumber(4)
  ScanOrder get order => $_getN(3);
  @$pb.TagNumber(4)
  set order(ScanOrder value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasOrder() => $_has(3);
  @$pb.TagNumber(4)
  void clearOrder() => $_clearField(4);
}

class ScanVerticesResponse extends $pb.GeneratedMessage {
  factory ScanVerticesResponse({
    $core.Iterable<Vertex>? vertices,
    $core.List<$core.int>? nextCursor,
  }) {
    final result = create();
    if (vertices != null) result.vertices.addAll(vertices);
    if (nextCursor != null) result.nextCursor = nextCursor;
    return result;
  }

  ScanVerticesResponse._();

  factory ScanVerticesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ScanVerticesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ScanVerticesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Vertex>(1, _omitFieldNames ? '' : 'vertices',
        subBuilder: Vertex.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'nextCursor', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVerticesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVerticesResponse copyWith(void Function(ScanVerticesResponse) updates) =>
      super.copyWith((message) => updates(message as ScanVerticesResponse))
          as ScanVerticesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ScanVerticesResponse create() => ScanVerticesResponse._();
  @$core.override
  ScanVerticesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ScanVerticesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ScanVerticesResponse>(create);
  static ScanVerticesResponse? _defaultInstance;

  /// vertices returned in the requested key order (ascending by default,
  /// descending when order = SCAN_ORDER_DESC). May be shorter than `limit`
  /// when the underlying range is exhausted.
  @$pb.TagNumber(1)
  $pb.PbList<Vertex> get vertices => $_getList(0);

  /// next_cursor is non-empty when more results are available. An empty
  /// value signals end of stream.
  @$pb.TagNumber(2)
  $core.List<$core.int> get nextCursor => $_getN(1);
  @$pb.TagNumber(2)
  set nextCursor($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasNextCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearNextCursor() => $_clearField(2);
}

/// ScanVertexKeysRequest streams vertex KEYS (no values) whose key starts
/// with `prefix`, in lexicographic order — the wire-efficient backbone of
/// the Redis-familiar `keys` CLI verb. It reuses ScanVertices' limit clamp
/// but carries its OWN opaque cursor kind, NOT interchangeable with any other
/// Scan* cursor (cross-feeding is rejected with INVALID_ARGUMENT). Unlike
/// ScanVertices, a non-empty `prefix` is REQUIRED: an empty prefix is
/// rejected with INVALID_ARGUMENT, so there is no whole-keyspace dump.
/// Plural-only.
class ScanVertexKeysRequest extends $pb.GeneratedMessage {
  factory ScanVertexKeysRequest({
    $core.String? prefix,
    $core.int? limit,
    $core.List<$core.int>? cursor,
    ScanOrder? order,
  }) {
    final result = create();
    if (prefix != null) result.prefix = prefix;
    if (limit != null) result.limit = limit;
    if (cursor != null) result.cursor = cursor;
    if (order != null) result.order = order;
    return result;
  }

  ScanVertexKeysRequest._();

  factory ScanVertexKeysRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ScanVertexKeysRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ScanVertexKeysRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'prefix')
    ..aI(2, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'cursor', $pb.PbFieldType.OY)
    ..aE<ScanOrder>(4, _omitFieldNames ? '' : 'order',
        enumValues: ScanOrder.values)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVertexKeysRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVertexKeysRequest copyWith(
          void Function(ScanVertexKeysRequest) updates) =>
      super.copyWith((message) => updates(message as ScanVertexKeysRequest))
          as ScanVertexKeysRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ScanVertexKeysRequest create() => ScanVertexKeysRequest._();
  @$core.override
  ScanVertexKeysRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ScanVertexKeysRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ScanVertexKeysRequest>(create);
  static ScanVertexKeysRequest? _defaultInstance;

  /// prefix is REQUIRED and must be non-empty; an empty prefix is rejected
  /// with INVALID_ARGUMENT.
  @$pb.TagNumber(1)
  $core.String get prefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set prefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearPrefix() => $_clearField(1);

  /// limit caps the number of keys returned in one call. Zero falls back to
  /// the server's ScanDefaultLimit and the value is clamped to ScanMaxLimit
  /// — the same knobs as ScanVertices.
  @$pb.TagNumber(2)
  $core.int get limit => $_getIZ(1);
  @$pb.TagNumber(2)
  set limit($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLimit() => $_has(1);
  @$pb.TagNumber(2)
  void clearLimit() => $_clearField(2);

  /// NOTE: opaque to the caller; its own cursor kind, NOT interchangeable
  /// with cursors from other Scan* RPCs (ScanVertices / ScanEdges).
  /// Cross-feeding is rejected with INVALID_ARGUMENT. An empty cursor starts
  /// from the beginning.
  @$pb.TagNumber(3)
  $core.List<$core.int> get cursor => $_getN(2);
  @$pb.TagNumber(3)
  set cursor($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCursor() => $_has(2);
  @$pb.TagNumber(3)
  void clearCursor() => $_clearField(3);

  /// order selects ascending (default) or descending key order, with the
  /// same order-bound cursor rule as ScanVertices.
  @$pb.TagNumber(4)
  ScanOrder get order => $_getN(3);
  @$pb.TagNumber(4)
  set order(ScanOrder value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasOrder() => $_has(3);
  @$pb.TagNumber(4)
  void clearOrder() => $_clearField(4);
}

class ScanVertexKeysResponse extends $pb.GeneratedMessage {
  factory ScanVertexKeysResponse({
    $core.Iterable<$core.String>? keys,
    $core.List<$core.int>? nextCursor,
  }) {
    final result = create();
    if (keys != null) result.keys.addAll(keys);
    if (nextCursor != null) result.nextCursor = nextCursor;
    return result;
  }

  ScanVertexKeysResponse._();

  factory ScanVertexKeysResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ScanVertexKeysResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ScanVertexKeysResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPS(1, _omitFieldNames ? '' : 'keys')
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'nextCursor', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVertexKeysResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanVertexKeysResponse copyWith(
          void Function(ScanVertexKeysResponse) updates) =>
      super.copyWith((message) => updates(message as ScanVertexKeysResponse))
          as ScanVertexKeysResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ScanVertexKeysResponse create() => ScanVertexKeysResponse._();
  @$core.override
  ScanVertexKeysResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ScanVertexKeysResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ScanVertexKeysResponse>(create);
  static ScanVertexKeysResponse? _defaultInstance;

  /// keys returned in the requested order (ascending by default, descending
  /// when order = SCAN_ORDER_DESC). May be shorter than `limit` when the
  /// underlying range is exhausted.
  @$pb.TagNumber(1)
  $pb.PbList<$core.String> get keys => $_getList(0);

  /// next_cursor is non-empty when more results are available. An empty
  /// value signals end of stream.
  @$pb.TagNumber(2)
  $core.List<$core.int> get nextCursor => $_getN(1);
  @$pb.TagNumber(2)
  set nextCursor($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasNextCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearNextCursor() => $_clearField(2);
}

/// SearchOptions carries the optional relevance controls for SearchVertices
/// (#892): match mode, phrase adjacency, and prefix/fuzzy term expansion. Every
/// field is optional and the zero value reproduces the default OR-union search,
/// so leaving options unset is identical to the pre-#892 request.
class SearchOptions extends $pb.GeneratedMessage {
  factory SearchOptions({
    MatchMode? matchMode,
    $core.int? minShouldMatch,
    $core.bool? phrase,
    $core.int? fuzziness,
    $core.bool? prefixTerms,
  }) {
    final result = create();
    if (matchMode != null) result.matchMode = matchMode;
    if (minShouldMatch != null) result.minShouldMatch = minShouldMatch;
    if (phrase != null) result.phrase = phrase;
    if (fuzziness != null) result.fuzziness = fuzziness;
    if (prefixTerms != null) result.prefixTerms = prefixTerms;
    return result;
  }

  SearchOptions._();

  factory SearchOptions.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SearchOptions.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SearchOptions',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aE<MatchMode>(1, _omitFieldNames ? '' : 'matchMode',
        enumValues: MatchMode.values)
    ..aI(2, _omitFieldNames ? '' : 'minShouldMatch',
        fieldType: $pb.PbFieldType.OU3)
    ..aOB(3, _omitFieldNames ? '' : 'phrase')
    ..aI(4, _omitFieldNames ? '' : 'fuzziness', fieldType: $pb.PbFieldType.OU3)
    ..aOB(5, _omitFieldNames ? '' : 'prefixTerms')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchOptions clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchOptions copyWith(void Function(SearchOptions) updates) =>
      super.copyWith((message) => updates(message as SearchOptions))
          as SearchOptions;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SearchOptions create() => SearchOptions._();
  @$core.override
  SearchOptions createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SearchOptions getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SearchOptions>(create);
  static SearchOptions? _defaultInstance;

  /// match_mode selects AND / OR / minimum-should-match membership (#890).
  @$pb.TagNumber(1)
  MatchMode get matchMode => $_getN(0);
  @$pb.TagNumber(1)
  set matchMode(MatchMode value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasMatchMode() => $_has(0);
  @$pb.TagNumber(1)
  void clearMatchMode() => $_clearField(1);

  /// min_should_match is the minimum number of distinct query word terms a
  /// vertex must carry when match_mode is MATCH_MODE_MIN_SHOULD; clamped to
  /// [1, number of query word terms]. Ignored for other modes.
  @$pb.TagNumber(2)
  $core.int get minShouldMatch => $_getIZ(1);
  @$pb.TagNumber(2)
  set minShouldMatch($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasMinShouldMatch() => $_has(1);
  @$pb.TagNumber(2)
  void clearMinShouldMatch() => $_clearField(2);

  /// phrase requires the query's word terms to occur adjacently, in order
  /// (#889) — the precision counterpart to the OR-union. It takes precedence
  /// over match_mode.
  @$pb.TagNumber(3)
  $core.bool get phrase => $_getBF(2);
  @$pb.TagNumber(3)
  set phrase($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasPhrase() => $_has(2);
  @$pb.TagNumber(3)
  void clearPhrase() => $_clearField(3);

  /// fuzziness is the maximum edit distance (0, 1, or 2) at which a query word
  /// also matches dictionary terms, so a typo still finds the term (#891). 0
  /// disables fuzzy matching.
  @$pb.TagNumber(4)
  $core.int get fuzziness => $_getIZ(3);
  @$pb.TagNumber(4)
  set fuzziness($core.int value) => $_setUnsignedInt32(3, value);
  @$pb.TagNumber(4)
  $core.bool hasFuzziness() => $_has(3);
  @$pb.TagNumber(4)
  void clearFuzziness() => $_clearField(4);

  /// prefix_terms also matches dictionary terms that extend a query word, so
  /// "lan" finds "lantern" (#891).
  @$pb.TagNumber(5)
  $core.bool get prefixTerms => $_getBF(4);
  @$pb.TagNumber(5)
  set prefixTerms($core.bool value) => $_setBool(4, value);
  @$pb.TagNumber(5)
  $core.bool hasPrefixTerms() => $_has(4);
  @$pb.TagNumber(5)
  void clearPrefixTerms() => $_clearField(5);
}

/// SearchVerticesRequest runs a relevance-ranked full-text search over vertex
/// *content* (key + value), as opposed to ScanVertices' lexicographic
/// key-prefix walk. `query` is analysed with the same pipeline used to build
/// the server-side index; an empty or unanalysable query yields zero hits
/// (not an error). `limit` caps the number of ranked hits returned; the
/// server enforces a default when `limit == 0` and a hard maximum (see
/// SearchConfig). `prefix`, when non-empty, restricts hits to vertices whose
/// key carries that prefix, composing content relevance with a namespace
/// scope.
///
/// This RPC requires the server-side search index to be enabled
/// (LANTERN_SEARCH_ENABLED, on by default). When the index is disabled the
/// server returns FAILED_PRECONDITION rather than silently returning no
/// hits. Plural-only — ranked search is inherently plural.
class SearchVerticesRequest extends $pb.GeneratedMessage {
  factory SearchVerticesRequest({
    $core.String? query,
    $core.int? limit,
    $core.String? prefix,
    SearchOptions? options,
  }) {
    final result = create();
    if (query != null) result.query = query;
    if (limit != null) result.limit = limit;
    if (prefix != null) result.prefix = prefix;
    if (options != null) result.options = options;
    return result;
  }

  SearchVerticesRequest._();

  factory SearchVerticesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SearchVerticesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SearchVerticesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'query')
    ..aI(2, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..aOS(3, _omitFieldNames ? '' : 'prefix')
    ..aOM<SearchOptions>(4, _omitFieldNames ? '' : 'options',
        subBuilder: SearchOptions.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchVerticesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchVerticesRequest copyWith(
          void Function(SearchVerticesRequest) updates) =>
      super.copyWith((message) => updates(message as SearchVerticesRequest))
          as SearchVerticesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SearchVerticesRequest create() => SearchVerticesRequest._();
  @$core.override
  SearchVerticesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SearchVerticesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SearchVerticesRequest>(create);
  static SearchVerticesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get query => $_getSZ(0);
  @$pb.TagNumber(1)
  set query($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasQuery() => $_has(0);
  @$pb.TagNumber(1)
  void clearQuery() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get limit => $_getIZ(1);
  @$pb.TagNumber(2)
  set limit($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLimit() => $_has(1);
  @$pb.TagNumber(2)
  void clearLimit() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.String get prefix => $_getSZ(2);
  @$pb.TagNumber(3)
  set prefix($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasPrefix() => $_has(2);
  @$pb.TagNumber(3)
  void clearPrefix() => $_clearField(3);

  /// options carries the optional match-mode / phrase / fuzzy controls (#892);
  /// unset means the default OR-union search.
  @$pb.TagNumber(4)
  SearchOptions get options => $_getN(3);
  @$pb.TagNumber(4)
  set options(SearchOptions value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasOptions() => $_has(3);
  @$pb.TagNumber(4)
  void clearOptions() => $_clearField(4);
  @$pb.TagNumber(4)
  SearchOptions ensureOptions() => $_ensure(3);
}

class SearchVerticesResponse extends $pb.GeneratedMessage {
  factory SearchVerticesResponse({
    $core.Iterable<SearchHit>? hits,
  }) {
    final result = create();
    if (hits != null) result.hits.addAll(hits);
    return result;
  }

  SearchVerticesResponse._();

  factory SearchVerticesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SearchVerticesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SearchVerticesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<SearchHit>(1, _omitFieldNames ? '' : 'hits',
        subBuilder: SearchHit.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchVerticesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchVerticesResponse copyWith(
          void Function(SearchVerticesResponse) updates) =>
      super.copyWith((message) => updates(message as SearchVerticesResponse))
          as SearchVerticesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SearchVerticesResponse create() => SearchVerticesResponse._();
  @$core.override
  SearchVerticesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SearchVerticesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SearchVerticesResponse>(create);
  static SearchVerticesResponse? _defaultInstance;

  /// hits in descending relevance order (BM25). May be shorter than `limit`
  /// when fewer vertices match; empty when nothing matches.
  @$pb.TagNumber(1)
  $pb.PbList<SearchHit> get hits => $_getList(0);
}

/// SearchHit pairs a matching vertex key with the relevance score the index
/// assigned it (BM25; higher is more relevant). The value and TTL are not
/// included — callers that need them issue a follow-up GetVertices with the
/// returned keys.
class SearchHit extends $pb.GeneratedMessage {
  factory SearchHit({
    $core.String? key,
    $core.double? score,
  }) {
    final result = create();
    if (key != null) result.key = key;
    if (score != null) result.score = score;
    return result;
  }

  SearchHit._();

  factory SearchHit.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SearchHit.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SearchHit',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'key')
    ..aD(2, _omitFieldNames ? '' : 'score')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchHit clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SearchHit copyWith(void Function(SearchHit) updates) =>
      super.copyWith((message) => updates(message as SearchHit)) as SearchHit;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SearchHit create() => SearchHit._();
  @$core.override
  SearchHit createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static SearchHit getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<SearchHit>(create);
  static SearchHit? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get key => $_getSZ(0);
  @$pb.TagNumber(1)
  set key($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasKey() => $_has(0);
  @$pb.TagNumber(1)
  void clearKey() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.double get score => $_getN(1);
  @$pb.TagNumber(2)
  set score($core.double value) => $_setDouble(1, value);
  @$pb.TagNumber(2)
  $core.bool hasScore() => $_has(1);
  @$pb.TagNumber(2)
  void clearScore() => $_clearField(2);
}

/// CountVerticesByPrefixRequest returns the number of live vertex keys with
/// the given prefix. Cheap (radix-only) and not subject to `limit`.
class CountVerticesByPrefixRequest extends $pb.GeneratedMessage {
  factory CountVerticesByPrefixRequest({
    $core.String? prefix,
  }) {
    final result = create();
    if (prefix != null) result.prefix = prefix;
    return result;
  }

  CountVerticesByPrefixRequest._();

  factory CountVerticesByPrefixRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CountVerticesByPrefixRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CountVerticesByPrefixRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'prefix')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CountVerticesByPrefixRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CountVerticesByPrefixRequest copyWith(
          void Function(CountVerticesByPrefixRequest) updates) =>
      super.copyWith(
              (message) => updates(message as CountVerticesByPrefixRequest))
          as CountVerticesByPrefixRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CountVerticesByPrefixRequest create() =>
      CountVerticesByPrefixRequest._();
  @$core.override
  CountVerticesByPrefixRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CountVerticesByPrefixRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CountVerticesByPrefixRequest>(create);
  static CountVerticesByPrefixRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get prefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set prefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearPrefix() => $_clearField(1);
}

class CountVerticesByPrefixResponse extends $pb.GeneratedMessage {
  factory CountVerticesByPrefixResponse({
    $fixnum.Int64? count,
  }) {
    final result = create();
    if (count != null) result.count = count;
    return result;
  }

  CountVerticesByPrefixResponse._();

  factory CountVerticesByPrefixResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory CountVerticesByPrefixResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'CountVerticesByPrefixResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(1, _omitFieldNames ? '' : 'count', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CountVerticesByPrefixResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  CountVerticesByPrefixResponse copyWith(
          void Function(CountVerticesByPrefixResponse) updates) =>
      super.copyWith(
              (message) => updates(message as CountVerticesByPrefixResponse))
          as CountVerticesByPrefixResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static CountVerticesByPrefixResponse create() =>
      CountVerticesByPrefixResponse._();
  @$core.override
  CountVerticesByPrefixResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static CountVerticesByPrefixResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<CountVerticesByPrefixResponse>(create);
  static CountVerticesByPrefixResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get count => $_getI64(0);
  @$pb.TagNumber(1)
  set count($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasCount() => $_has(0);
  @$pb.TagNumber(1)
  void clearCount() => $_clearField(1);
}

/// DeleteVerticesByPrefixRequest deletes up to `limit` vertices whose key
/// starts with `prefix`. `limit == 0` lets the server apply its configured
/// default (see RateLimitConfig / ScanConfig). When `dry_run` is true, no
/// deletion is performed and the response reports the number that *would*
/// be deleted.
class DeleteVerticesByPrefixRequest extends $pb.GeneratedMessage {
  factory DeleteVerticesByPrefixRequest({
    $core.String? prefix,
    $core.int? limit,
    $core.bool? dryRun,
  }) {
    final result = create();
    if (prefix != null) result.prefix = prefix;
    if (limit != null) result.limit = limit;
    if (dryRun != null) result.dryRun = dryRun;
    return result;
  }

  DeleteVerticesByPrefixRequest._();

  factory DeleteVerticesByPrefixRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteVerticesByPrefixRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteVerticesByPrefixRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'prefix')
    ..aI(2, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..aOB(3, _omitFieldNames ? '' : 'dryRun')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesByPrefixRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesByPrefixRequest copyWith(
          void Function(DeleteVerticesByPrefixRequest) updates) =>
      super.copyWith(
              (message) => updates(message as DeleteVerticesByPrefixRequest))
          as DeleteVerticesByPrefixRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteVerticesByPrefixRequest create() =>
      DeleteVerticesByPrefixRequest._();
  @$core.override
  DeleteVerticesByPrefixRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteVerticesByPrefixRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteVerticesByPrefixRequest>(create);
  static DeleteVerticesByPrefixRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get prefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set prefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearPrefix() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get limit => $_getIZ(1);
  @$pb.TagNumber(2)
  set limit($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLimit() => $_has(1);
  @$pb.TagNumber(2)
  void clearLimit() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.bool get dryRun => $_getBF(2);
  @$pb.TagNumber(3)
  set dryRun($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasDryRun() => $_has(2);
  @$pb.TagNumber(3)
  void clearDryRun() => $_clearField(3);
}

class DeleteVerticesByPrefixResponse extends $pb.GeneratedMessage {
  factory DeleteVerticesByPrefixResponse({
    $fixnum.Int64? deleted,
  }) {
    final result = create();
    if (deleted != null) result.deleted = deleted;
    return result;
  }

  DeleteVerticesByPrefixResponse._();

  factory DeleteVerticesByPrefixResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteVerticesByPrefixResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteVerticesByPrefixResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(1, _omitFieldNames ? '' : 'deleted', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesByPrefixResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteVerticesByPrefixResponse copyWith(
          void Function(DeleteVerticesByPrefixResponse) updates) =>
      super.copyWith(
              (message) => updates(message as DeleteVerticesByPrefixResponse))
          as DeleteVerticesByPrefixResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteVerticesByPrefixResponse create() =>
      DeleteVerticesByPrefixResponse._();
  @$core.override
  DeleteVerticesByPrefixResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteVerticesByPrefixResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteVerticesByPrefixResponse>(create);
  static DeleteVerticesByPrefixResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get deleted => $_getI64(0);
  @$pb.TagNumber(1)
  set deleted($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDeleted() => $_has(0);
  @$pb.TagNumber(1)
  void clearDeleted() => $_clearField(1);
}

/// TopVerticesByDegreeRequest ranks the most-connected vertices under a key
/// prefix by their (weighted) degree — the seed-selection question a cold-start
/// visualization or recommendation front end hits when it has no better anchor
/// (#900). It exposes the degree index the store already maintains for BM25
/// edge weighting rather than shipping the whole subgraph over the wire.
class TopVerticesByDegreeRequest extends $pb.GeneratedMessage {
  factory TopVerticesByDegreeRequest({
    $core.String? prefix,
    $core.int? k,
    TopVerticesByDegreeRequest_Direction? direction,
    $core.bool? weighted,
  }) {
    final result = create();
    if (prefix != null) result.prefix = prefix;
    if (k != null) result.k = k;
    if (direction != null) result.direction = direction;
    if (weighted != null) result.weighted = weighted;
    return result;
  }

  TopVerticesByDegreeRequest._();

  factory TopVerticesByDegreeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory TopVerticesByDegreeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'TopVerticesByDegreeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'prefix')
    ..aI(2, _omitFieldNames ? '' : 'k', fieldType: $pb.PbFieldType.OU3)
    ..aE<TopVerticesByDegreeRequest_Direction>(
        3, _omitFieldNames ? '' : 'direction',
        enumValues: TopVerticesByDegreeRequest_Direction.values)
    ..aOB(4, _omitFieldNames ? '' : 'weighted')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TopVerticesByDegreeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TopVerticesByDegreeRequest copyWith(
          void Function(TopVerticesByDegreeRequest) updates) =>
      super.copyWith(
              (message) => updates(message as TopVerticesByDegreeRequest))
          as TopVerticesByDegreeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static TopVerticesByDegreeRequest create() => TopVerticesByDegreeRequest._();
  @$core.override
  TopVerticesByDegreeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static TopVerticesByDegreeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<TopVerticesByDegreeRequest>(create);
  static TopVerticesByDegreeRequest? _defaultInstance;

  /// prefix is required and must be non-empty: there is no whole-graph ranking
  /// (an empty prefix is rejected with INVALID_ARGUMENT). Only vertices whose
  /// key starts with prefix are candidates.
  @$pb.TagNumber(1)
  $core.String get prefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set prefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearPrefix() => $_clearField(1);

  /// k caps the number of ranked entries returned. 0 lets the server apply its
  /// configured default; the server also clamps k to a hard cap (see
  /// ScanConfig), mirroring the scan `limit` knobs.
  @$pb.TagNumber(2)
  $core.int get k => $_getIZ(1);
  @$pb.TagNumber(2)
  set k($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasK() => $_has(1);
  @$pb.TagNumber(2)
  void clearK() => $_clearField(2);

  @$pb.TagNumber(3)
  TopVerticesByDegreeRequest_Direction get direction => $_getN(2);
  @$pb.TagNumber(3)
  set direction(TopVerticesByDegreeRequest_Direction value) =>
      $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasDirection() => $_has(2);
  @$pb.TagNumber(3)
  void clearDirection() => $_clearField(3);

  /// weighted ranks by the summed live edge weight in the chosen direction
  /// instead of the raw live edge count.
  @$pb.TagNumber(4)
  $core.bool get weighted => $_getBF(3);
  @$pb.TagNumber(4)
  set weighted($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasWeighted() => $_has(3);
  @$pb.TagNumber(4)
  void clearWeighted() => $_clearField(4);
}

class TopVerticesByDegreeResponse_Entry extends $pb.GeneratedMessage {
  factory TopVerticesByDegreeResponse_Entry({
    $core.String? key,
    $fixnum.Int64? degree,
    $core.double? weightedDegree,
  }) {
    final result = create();
    if (key != null) result.key = key;
    if (degree != null) result.degree = degree;
    if (weightedDegree != null) result.weightedDegree = weightedDegree;
    return result;
  }

  TopVerticesByDegreeResponse_Entry._();

  factory TopVerticesByDegreeResponse_Entry.fromBuffer(
          $core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory TopVerticesByDegreeResponse_Entry.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'TopVerticesByDegreeResponse.Entry',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'key')
    ..a<$fixnum.Int64>(2, _omitFieldNames ? '' : 'degree', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aD(3, _omitFieldNames ? '' : 'weightedDegree')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TopVerticesByDegreeResponse_Entry clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TopVerticesByDegreeResponse_Entry copyWith(
          void Function(TopVerticesByDegreeResponse_Entry) updates) =>
      super.copyWith((message) =>
              updates(message as TopVerticesByDegreeResponse_Entry))
          as TopVerticesByDegreeResponse_Entry;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static TopVerticesByDegreeResponse_Entry create() =>
      TopVerticesByDegreeResponse_Entry._();
  @$core.override
  TopVerticesByDegreeResponse_Entry createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static TopVerticesByDegreeResponse_Entry getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<TopVerticesByDegreeResponse_Entry>(
          create);
  static TopVerticesByDegreeResponse_Entry? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get key => $_getSZ(0);
  @$pb.TagNumber(1)
  set key($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasKey() => $_has(0);
  @$pb.TagNumber(1)
  void clearKey() => $_clearField(1);

  /// degree is the live edge count in the chosen direction.
  @$pb.TagNumber(2)
  $fixnum.Int64 get degree => $_getI64(1);
  @$pb.TagNumber(2)
  set degree($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasDegree() => $_has(1);
  @$pb.TagNumber(2)
  void clearDegree() => $_clearField(2);

  /// weighted_degree is the summed live edge weight in the chosen direction.
  @$pb.TagNumber(3)
  $core.double get weightedDegree => $_getN(2);
  @$pb.TagNumber(3)
  set weightedDegree($core.double value) => $_setDouble(2, value);
  @$pb.TagNumber(3)
  $core.bool hasWeightedDegree() => $_has(2);
  @$pb.TagNumber(3)
  void clearWeightedDegree() => $_clearField(3);
}

/// TopVerticesByDegreeResponse lists the ranked vertices in descending order of
/// the chosen metric (weighted_degree when the request set `weighted`, else
/// degree). Counts follow the live-visibility rule (#750): expired vertices and
/// fully decayed edges do not contribute. Results are point-in-time
/// best-effort, like GetServerStatus counts; for DIRECTION_IN / DIRECTION_BOTH
/// the edge scan reads weights outside the write-blocking lock (#920), so an
/// edge added or deleted mid-scan may be only partially reflected.
class TopVerticesByDegreeResponse extends $pb.GeneratedMessage {
  factory TopVerticesByDegreeResponse({
    $core.Iterable<TopVerticesByDegreeResponse_Entry>? entries,
  }) {
    final result = create();
    if (entries != null) result.entries.addAll(entries);
    return result;
  }

  TopVerticesByDegreeResponse._();

  factory TopVerticesByDegreeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory TopVerticesByDegreeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'TopVerticesByDegreeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<TopVerticesByDegreeResponse_Entry>(
        1, _omitFieldNames ? '' : 'entries',
        subBuilder: TopVerticesByDegreeResponse_Entry.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TopVerticesByDegreeResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  TopVerticesByDegreeResponse copyWith(
          void Function(TopVerticesByDegreeResponse) updates) =>
      super.copyWith(
              (message) => updates(message as TopVerticesByDegreeResponse))
          as TopVerticesByDegreeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static TopVerticesByDegreeResponse create() =>
      TopVerticesByDegreeResponse._();
  @$core.override
  TopVerticesByDegreeResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static TopVerticesByDegreeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<TopVerticesByDegreeResponse>(create);
  static TopVerticesByDegreeResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<TopVerticesByDegreeResponse_Entry> get entries => $_getList(0);
}

class GetEdgeRequest extends $pb.GeneratedMessage {
  factory GetEdgeRequest({
    $core.String? tail,
    $core.String? head,
  }) {
    final result = create();
    if (tail != null) result.tail = tail;
    if (head != null) result.head = head;
    return result;
  }

  GetEdgeRequest._();

  factory GetEdgeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetEdgeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetEdgeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tail')
    ..aOS(2, _omitFieldNames ? '' : 'head')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgeRequest copyWith(void Function(GetEdgeRequest) updates) =>
      super.copyWith((message) => updates(message as GetEdgeRequest))
          as GetEdgeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetEdgeRequest create() => GetEdgeRequest._();
  @$core.override
  GetEdgeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetEdgeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetEdgeRequest>(create);
  static GetEdgeRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tail => $_getSZ(0);
  @$pb.TagNumber(1)
  set tail($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTail() => $_has(0);
  @$pb.TagNumber(1)
  void clearTail() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get head => $_getSZ(1);
  @$pb.TagNumber(2)
  set head($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHead() => $_has(1);
  @$pb.TagNumber(2)
  void clearHead() => $_clearField(2);
}

class GetEdgeResponse extends $pb.GeneratedMessage {
  factory GetEdgeResponse({
    Edge? edge,
  }) {
    final result = create();
    if (edge != null) result.edge = edge;
    return result;
  }

  GetEdgeResponse._();

  factory GetEdgeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetEdgeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetEdgeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<Edge>(1, _omitFieldNames ? '' : 'edge', subBuilder: Edge.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgeResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgeResponse copyWith(void Function(GetEdgeResponse) updates) =>
      super.copyWith((message) => updates(message as GetEdgeResponse))
          as GetEdgeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetEdgeResponse create() => GetEdgeResponse._();
  @$core.override
  GetEdgeResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetEdgeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetEdgeResponse>(create);
  static GetEdgeResponse? _defaultInstance;

  @$pb.TagNumber(1)
  Edge get edge => $_getN(0);
  @$pb.TagNumber(1)
  set edge(Edge value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasEdge() => $_has(0);
  @$pb.TagNumber(1)
  void clearEdge() => $_clearField(1);
  @$pb.TagNumber(1)
  Edge ensureEdge() => $_ensure(0);
}

/// GetEdgesRequest reads several edges in one round trip. Subject to the
/// same MaxBatchSize / MaxKeyLen guard rails as the write RPCs.
class GetEdgesRequest extends $pb.GeneratedMessage {
  factory GetEdgesRequest({
    $core.Iterable<EdgeKey>? edges,
  }) {
    final result = create();
    if (edges != null) result.edges.addAll(edges);
    return result;
  }

  GetEdgesRequest._();

  factory GetEdgesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetEdgesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetEdgesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<EdgeKey>(1, _omitFieldNames ? '' : 'edges',
        subBuilder: EdgeKey.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgesRequest copyWith(void Function(GetEdgesRequest) updates) =>
      super.copyWith((message) => updates(message as GetEdgesRequest))
          as GetEdgesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetEdgesRequest create() => GetEdgesRequest._();
  @$core.override
  GetEdgesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetEdgesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetEdgesRequest>(create);
  static GetEdgesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<EdgeKey> get edges => $_getList(0);
}

class GetEdgesResponse extends $pb.GeneratedMessage {
  factory GetEdgesResponse({
    $core.Iterable<Edge>? edges,
    $core.Iterable<EdgeKey>? missing,
  }) {
    final result = create();
    if (edges != null) result.edges.addAll(edges);
    if (missing != null) result.missing.addAll(missing);
    return result;
  }

  GetEdgesResponse._();

  factory GetEdgesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetEdgesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetEdgesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Edge>(1, _omitFieldNames ? '' : 'edges', subBuilder: Edge.create)
    ..pPM<EdgeKey>(2, _omitFieldNames ? '' : 'missing',
        subBuilder: EdgeKey.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetEdgesResponse copyWith(void Function(GetEdgesResponse) updates) =>
      super.copyWith((message) => updates(message as GetEdgesResponse))
          as GetEdgesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetEdgesResponse create() => GetEdgesResponse._();
  @$core.override
  GetEdgesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetEdgesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetEdgesResponse>(create);
  static GetEdgesResponse? _defaultInstance;

  /// edges contains every (tail, head) pair that was present at the time of
  /// the call. Order is unspecified; clients should match by (Edge.tail,
  /// Edge.head).
  @$pb.TagNumber(1)
  $pb.PbList<Edge> get edges => $_getList(0);

  /// missing lists the requested (tail, head) pairs that did not exist (or
  /// had expired).
  @$pb.TagNumber(2)
  $pb.PbList<EdgeKey> get missing => $_getList(1);
}

class DeleteEdgeRequest extends $pb.GeneratedMessage {
  factory DeleteEdgeRequest({
    $core.String? tail,
    $core.String? head,
  }) {
    final result = create();
    if (tail != null) result.tail = tail;
    if (head != null) result.head = head;
    return result;
  }

  DeleteEdgeRequest._();

  factory DeleteEdgeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteEdgeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteEdgeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tail')
    ..aOS(2, _omitFieldNames ? '' : 'head')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgeRequest copyWith(void Function(DeleteEdgeRequest) updates) =>
      super.copyWith((message) => updates(message as DeleteEdgeRequest))
          as DeleteEdgeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteEdgeRequest create() => DeleteEdgeRequest._();
  @$core.override
  DeleteEdgeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteEdgeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteEdgeRequest>(create);
  static DeleteEdgeRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tail => $_getSZ(0);
  @$pb.TagNumber(1)
  set tail($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTail() => $_has(0);
  @$pb.TagNumber(1)
  void clearTail() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get head => $_getSZ(1);
  @$pb.TagNumber(2)
  set head($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHead() => $_has(1);
  @$pb.TagNumber(2)
  void clearHead() => $_clearField(2);
}

class DeleteEdgeResponse extends $pb.GeneratedMessage {
  factory DeleteEdgeResponse({
    $core.bool? existed,
  }) {
    final result = create();
    if (existed != null) result.existed = existed;
    return result;
  }

  DeleteEdgeResponse._();

  factory DeleteEdgeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteEdgeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteEdgeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOB(1, _omitFieldNames ? '' : 'existed')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgeResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgeResponse copyWith(void Function(DeleteEdgeResponse) updates) =>
      super.copyWith((message) => updates(message as DeleteEdgeResponse))
          as DeleteEdgeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteEdgeResponse create() => DeleteEdgeResponse._();
  @$core.override
  DeleteEdgeResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteEdgeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteEdgeResponse>(create);
  static DeleteEdgeResponse? _defaultInstance;

  /// existed is true if the edge was present and removed by this call.
  @$pb.TagNumber(1)
  $core.bool get existed => $_getBF(0);
  @$pb.TagNumber(1)
  set existed($core.bool value) => $_setBool(0, value);
  @$pb.TagNumber(1)
  $core.bool hasExisted() => $_has(0);
  @$pb.TagNumber(1)
  void clearExisted() => $_clearField(1);
}

/// EdgeKey identifies an edge by its (tail, head) pair without weight.
class EdgeKey extends $pb.GeneratedMessage {
  factory EdgeKey({
    $core.String? tail,
    $core.String? head,
  }) {
    final result = create();
    if (tail != null) result.tail = tail;
    if (head != null) result.head = head;
    return result;
  }

  EdgeKey._();

  factory EdgeKey.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory EdgeKey.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'EdgeKey',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tail')
    ..aOS(2, _omitFieldNames ? '' : 'head')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  EdgeKey clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  EdgeKey copyWith(void Function(EdgeKey) updates) =>
      super.copyWith((message) => updates(message as EdgeKey)) as EdgeKey;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static EdgeKey create() => EdgeKey._();
  @$core.override
  EdgeKey createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static EdgeKey getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<EdgeKey>(create);
  static EdgeKey? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tail => $_getSZ(0);
  @$pb.TagNumber(1)
  set tail($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTail() => $_has(0);
  @$pb.TagNumber(1)
  void clearTail() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get head => $_getSZ(1);
  @$pb.TagNumber(2)
  set head($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHead() => $_has(1);
  @$pb.TagNumber(2)
  void clearHead() => $_clearField(2);
}

/// ScanEdgesRequest streams edges whose tail key starts with `tail_prefix`
/// AND whose head key starts with `head_prefix`, in ascending (tail, head)
/// order. Either prefix may be empty to disable the corresponding filter
/// (both empty scans every edge). `limit` caps the number returned in one
/// call; the server enforces a default when `limit == 0` and a hard maximum
/// (see ScanConfig on the server). `cursor` MUST be treated as opaque bytes
/// by clients — pass back exactly what the previous response returned in
/// `next_cursor`. An empty `cursor` starts from the beginning.
///
/// Implementation note: v1 walks the tail-side prefix index and applies
/// `head_prefix` as a post-filter; for highly selective `head_prefix`
/// queries the server may visit many tails to fill one page. Latency is
/// reported in the standard scan histogram so operators can spot
/// pathological filters.
class ScanEdgesRequest extends $pb.GeneratedMessage {
  factory ScanEdgesRequest({
    $core.String? tailPrefix,
    $core.String? headPrefix,
    $core.int? limit,
    $core.List<$core.int>? cursor,
  }) {
    final result = create();
    if (tailPrefix != null) result.tailPrefix = tailPrefix;
    if (headPrefix != null) result.headPrefix = headPrefix;
    if (limit != null) result.limit = limit;
    if (cursor != null) result.cursor = cursor;
    return result;
  }

  ScanEdgesRequest._();

  factory ScanEdgesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ScanEdgesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ScanEdgesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tailPrefix')
    ..aOS(2, _omitFieldNames ? '' : 'headPrefix')
    ..aI(3, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        4, _omitFieldNames ? '' : 'cursor', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanEdgesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanEdgesRequest copyWith(void Function(ScanEdgesRequest) updates) =>
      super.copyWith((message) => updates(message as ScanEdgesRequest))
          as ScanEdgesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ScanEdgesRequest create() => ScanEdgesRequest._();
  @$core.override
  ScanEdgesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ScanEdgesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ScanEdgesRequest>(create);
  static ScanEdgesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tailPrefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set tailPrefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTailPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearTailPrefix() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get headPrefix => $_getSZ(1);
  @$pb.TagNumber(2)
  set headPrefix($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHeadPrefix() => $_has(1);
  @$pb.TagNumber(2)
  void clearHeadPrefix() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.int get limit => $_getIZ(2);
  @$pb.TagNumber(3)
  set limit($core.int value) => $_setUnsignedInt32(2, value);
  @$pb.TagNumber(3)
  $core.bool hasLimit() => $_has(2);
  @$pb.TagNumber(3)
  void clearLimit() => $_clearField(3);

  /// NOTE: opaque to the caller; not interchangeable with cursors from
  /// other Scan* RPCs (e.g. ScanVertices). Cross-feeding is rejected with
  /// INVALID_ARGUMENT rather than silently restarting the scan.
  @$pb.TagNumber(4)
  $core.List<$core.int> get cursor => $_getN(3);
  @$pb.TagNumber(4)
  set cursor($core.List<$core.int> value) => $_setBytes(3, value);
  @$pb.TagNumber(4)
  $core.bool hasCursor() => $_has(3);
  @$pb.TagNumber(4)
  void clearCursor() => $_clearField(4);
}

class ScanEdgesResponse extends $pb.GeneratedMessage {
  factory ScanEdgesResponse({
    $core.Iterable<Edge>? edges,
    $core.List<$core.int>? nextCursor,
  }) {
    final result = create();
    if (edges != null) result.edges.addAll(edges);
    if (nextCursor != null) result.nextCursor = nextCursor;
    return result;
  }

  ScanEdgesResponse._();

  factory ScanEdgesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ScanEdgesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ScanEdgesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Edge>(1, _omitFieldNames ? '' : 'edges', subBuilder: Edge.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'nextCursor', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanEdgesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ScanEdgesResponse copyWith(void Function(ScanEdgesResponse) updates) =>
      super.copyWith((message) => updates(message as ScanEdgesResponse))
          as ScanEdgesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ScanEdgesResponse create() => ScanEdgesResponse._();
  @$core.override
  ScanEdgesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ScanEdgesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ScanEdgesResponse>(create);
  static ScanEdgesResponse? _defaultInstance;

  /// edges returned in ascending (tail, head) order. May be shorter than
  /// `limit` when the underlying range is exhausted.
  @$pb.TagNumber(1)
  $pb.PbList<Edge> get edges => $_getList(0);

  /// next_cursor is non-empty when more results are available. An empty
  /// value signals end of stream.
  @$pb.TagNumber(2)
  $core.List<$core.int> get nextCursor => $_getN(1);
  @$pb.TagNumber(2)
  set nextCursor($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasNextCursor() => $_has(1);
  @$pb.TagNumber(2)
  void clearNextCursor() => $_clearField(2);
}

/// DeleteEdgesRequest removes several edges in one round trip. Subject to the
/// MaxBatchSize / MaxKeyLen guard rails.
class DeleteEdgesRequest extends $pb.GeneratedMessage {
  factory DeleteEdgesRequest({
    $core.Iterable<EdgeKey>? edges,
  }) {
    final result = create();
    if (edges != null) result.edges.addAll(edges);
    return result;
  }

  DeleteEdgesRequest._();

  factory DeleteEdgesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteEdgesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteEdgesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<EdgeKey>(1, _omitFieldNames ? '' : 'edges',
        subBuilder: EdgeKey.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesRequest copyWith(void Function(DeleteEdgesRequest) updates) =>
      super.copyWith((message) => updates(message as DeleteEdgesRequest))
          as DeleteEdgesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteEdgesRequest create() => DeleteEdgesRequest._();
  @$core.override
  DeleteEdgesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteEdgesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteEdgesRequest>(create);
  static DeleteEdgesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<EdgeKey> get edges => $_getList(0);
}

class DeleteEdgesResponse extends $pb.GeneratedMessage {
  factory DeleteEdgesResponse({
    $core.int? deleted,
  }) {
    final result = create();
    if (deleted != null) result.deleted = deleted;
    return result;
  }

  DeleteEdgesResponse._();

  factory DeleteEdgesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteEdgesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteEdgesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'deleted')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesResponse copyWith(void Function(DeleteEdgesResponse) updates) =>
      super.copyWith((message) => updates(message as DeleteEdgesResponse))
          as DeleteEdgesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteEdgesResponse create() => DeleteEdgesResponse._();
  @$core.override
  DeleteEdgesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteEdgesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteEdgesResponse>(create);
  static DeleteEdgesResponse? _defaultInstance;

  /// Number of edges the server attempted to delete (equals len(edges) on success).
  @$pb.TagNumber(1)
  $core.int get deleted => $_getIZ(0);
  @$pb.TagNumber(1)
  set deleted($core.int value) => $_setSignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDeleted() => $_has(0);
  @$pb.TagNumber(1)
  void clearDeleted() => $_clearField(1);
}

/// DeleteEdgesByPrefixRequest deletes up to `limit` live edges whose tail key
/// starts with `tail_prefix` AND whose head key starts with `head_prefix`.
/// Either prefix may be empty to disable the corresponding filter, but at
/// least one MUST be non-empty — an all-empty request is rejected with
/// INVALID_ARGUMENT so a bulk edge wipe is always explicitly scoped. `limit ==
/// 0` lets the server apply its configured default (see ScanConfig); callers
/// loop on the returned `deleted` count until it reaches zero to drain a large
/// matching set. When `dry_run` is true, no deletion is performed and the
/// response reports the number that *would* be deleted (capped at the same
/// effective limit).
///
/// Implementation note: like ScanEdges, v1 walks the tail-side prefix index
/// and applies `head_prefix` as a post-filter; a highly selective
/// `head_prefix` may visit many tails to reach `limit`. Latency is reported in
/// the standard scan histogram so operators can spot pathological filters.
class DeleteEdgesByPrefixRequest extends $pb.GeneratedMessage {
  factory DeleteEdgesByPrefixRequest({
    $core.String? tailPrefix,
    $core.String? headPrefix,
    $core.int? limit,
    $core.bool? dryRun,
  }) {
    final result = create();
    if (tailPrefix != null) result.tailPrefix = tailPrefix;
    if (headPrefix != null) result.headPrefix = headPrefix;
    if (limit != null) result.limit = limit;
    if (dryRun != null) result.dryRun = dryRun;
    return result;
  }

  DeleteEdgesByPrefixRequest._();

  factory DeleteEdgesByPrefixRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteEdgesByPrefixRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteEdgesByPrefixRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tailPrefix')
    ..aOS(2, _omitFieldNames ? '' : 'headPrefix')
    ..aI(3, _omitFieldNames ? '' : 'limit', fieldType: $pb.PbFieldType.OU3)
    ..aOB(4, _omitFieldNames ? '' : 'dryRun')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesByPrefixRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesByPrefixRequest copyWith(
          void Function(DeleteEdgesByPrefixRequest) updates) =>
      super.copyWith(
              (message) => updates(message as DeleteEdgesByPrefixRequest))
          as DeleteEdgesByPrefixRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteEdgesByPrefixRequest create() => DeleteEdgesByPrefixRequest._();
  @$core.override
  DeleteEdgesByPrefixRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteEdgesByPrefixRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteEdgesByPrefixRequest>(create);
  static DeleteEdgesByPrefixRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get tailPrefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set tailPrefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasTailPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearTailPrefix() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.String get headPrefix => $_getSZ(1);
  @$pb.TagNumber(2)
  set headPrefix($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasHeadPrefix() => $_has(1);
  @$pb.TagNumber(2)
  void clearHeadPrefix() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.int get limit => $_getIZ(2);
  @$pb.TagNumber(3)
  set limit($core.int value) => $_setUnsignedInt32(2, value);
  @$pb.TagNumber(3)
  $core.bool hasLimit() => $_has(2);
  @$pb.TagNumber(3)
  void clearLimit() => $_clearField(3);

  @$pb.TagNumber(4)
  $core.bool get dryRun => $_getBF(3);
  @$pb.TagNumber(4)
  set dryRun($core.bool value) => $_setBool(3, value);
  @$pb.TagNumber(4)
  $core.bool hasDryRun() => $_has(3);
  @$pb.TagNumber(4)
  void clearDryRun() => $_clearField(4);
}

class DeleteEdgesByPrefixResponse extends $pb.GeneratedMessage {
  factory DeleteEdgesByPrefixResponse({
    $fixnum.Int64? deleted,
  }) {
    final result = create();
    if (deleted != null) result.deleted = deleted;
    return result;
  }

  DeleteEdgesByPrefixResponse._();

  factory DeleteEdgesByPrefixResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory DeleteEdgesByPrefixResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'DeleteEdgesByPrefixResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(1, _omitFieldNames ? '' : 'deleted', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesByPrefixResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  DeleteEdgesByPrefixResponse copyWith(
          void Function(DeleteEdgesByPrefixResponse) updates) =>
      super.copyWith(
              (message) => updates(message as DeleteEdgesByPrefixResponse))
          as DeleteEdgesByPrefixResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static DeleteEdgesByPrefixResponse create() =>
      DeleteEdgesByPrefixResponse._();
  @$core.override
  DeleteEdgesByPrefixResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static DeleteEdgesByPrefixResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<DeleteEdgesByPrefixResponse>(create);
  static DeleteEdgesByPrefixResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get deleted => $_getI64(0);
  @$pb.TagNumber(1)
  set deleted($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasDeleted() => $_has(0);
  @$pb.TagNumber(1)
  void clearDeleted() => $_clearField(1);
}

/// AddEdgeRequest accumulates weight onto a single (tail, head) pair: repeated
/// calls with the same endpoints sum their weights. This is the singular
/// convenience wrapper over AddEdges and shares its semantics.
class AddEdgeRequest extends $pb.GeneratedMessage {
  factory AddEdgeRequest({
    Edge? edge,
    $core.List<$core.int>? contribId,
  }) {
    final result = create();
    if (edge != null) result.edge = edge;
    if (contribId != null) result.contribId = contribId;
    return result;
  }

  AddEdgeRequest._();

  factory AddEdgeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AddEdgeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AddEdgeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<Edge>(1, _omitFieldNames ? '' : 'edge', subBuilder: Edge.create)
    ..a<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'contribId', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgeRequest copyWith(void Function(AddEdgeRequest) updates) =>
      super.copyWith((message) => updates(message as AddEdgeRequest))
          as AddEdgeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AddEdgeRequest create() => AddEdgeRequest._();
  @$core.override
  AddEdgeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static AddEdgeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AddEdgeRequest>(create);
  static AddEdgeRequest? _defaultInstance;

  @$pb.TagNumber(1)
  Edge get edge => $_getN(0);
  @$pb.TagNumber(1)
  set edge(Edge value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasEdge() => $_has(0);
  @$pb.TagNumber(1)
  void clearEdge() => $_clearField(1);
  @$pb.TagNumber(1)
  Edge ensureEdge() => $_ensure(0);

  /// contrib_id is an optional 24-byte idempotency key for the contribution.
  /// When set (non-empty and not all-zero), the server records the weight at
  /// most once per distinct id: re-sending the same request (e.g. a
  /// transport-level retry) is an exact no-op instead of double-counting the
  /// additive weight. An empty value preserves the legacy non-idempotent
  /// additive behavior. The id is opaque to the server; the Go SDK derives it
  /// from a per-client random nonce plus a monotonic call sequence when
  /// idempotent adds are enabled. Forwarded into contrib_ids[0] of the
  /// one-element AddEdges batch this wraps.
  @$pb.TagNumber(2)
  $core.List<$core.int> get contribId => $_getN(1);
  @$pb.TagNumber(2)
  set contribId($core.List<$core.int> value) => $_setBytes(1, value);
  @$pb.TagNumber(2)
  $core.bool hasContribId() => $_has(1);
  @$pb.TagNumber(2)
  void clearContribId() => $_clearField(2);
}

class AddEdgeResponse extends $pb.GeneratedMessage {
  factory AddEdgeResponse({
    $core.double? effectiveWeight,
  }) {
    final result = create();
    if (effectiveWeight != null) result.effectiveWeight = effectiveWeight;
    return result;
  }

  AddEdgeResponse._();

  factory AddEdgeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AddEdgeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AddEdgeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aD(1, _omitFieldNames ? '' : 'effectiveWeight',
        fieldType: $pb.PbFieldType.OF)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgeResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgeResponse copyWith(void Function(AddEdgeResponse) updates) =>
      super.copyWith((message) => updates(message as AddEdgeResponse))
          as AddEdgeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AddEdgeResponse create() => AddEdgeResponse._();
  @$core.override
  AddEdgeResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static AddEdgeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AddEdgeResponse>(create);
  static AddEdgeResponse? _defaultInstance;

  /// effective_weight is the sum of live contributions on (tail, head)
  /// immediately after applying (or, when contrib_id dedupes a replay, after
  /// observing) this request, as seen by the serving node. This turns AddEdge
  /// into a race-free increment-then-check counter: a caller writing
  /// weight=1 with a TTL reads back the rolling-window count in one round trip
  /// and can enforce a cap without a separate GetEdge. When contrib_id makes
  /// the add a no-op, this is the current live sum (the value a retry wants).
  /// Note: with replication enabled the value is the serving node's local view
  /// at apply time — the same async-replica caveat as a Redis INCR read.
  @$pb.TagNumber(1)
  $core.double get effectiveWeight => $_getN(0);
  @$pb.TagNumber(1)
  set effectiveWeight($core.double value) => $_setFloat(0, value);
  @$pb.TagNumber(1)
  $core.bool hasEffectiveWeight() => $_has(0);
  @$pb.TagNumber(1)
  void clearEffectiveWeight() => $_clearField(1);
}

/// AddEdgesRequest accumulates weight onto each (tail, head) pair: repeated
/// calls with the same endpoints sum their weights. Additive writes are
/// non-idempotent unless an index-aligned contrib_ids entry is supplied.
class AddEdgesRequest extends $pb.GeneratedMessage {
  factory AddEdgesRequest({
    $core.Iterable<Edge>? edges,
    $core.Iterable<$core.List<$core.int>>? contribIds,
  }) {
    final result = create();
    if (edges != null) result.edges.addAll(edges);
    if (contribIds != null) result.contribIds.addAll(contribIds);
    return result;
  }

  AddEdgesRequest._();

  factory AddEdgesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AddEdgesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AddEdgesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Edge>(1, _omitFieldNames ? '' : 'edges', subBuilder: Edge.create)
    ..p<$core.List<$core.int>>(
        2, _omitFieldNames ? '' : 'contribIds', $pb.PbFieldType.PY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgesRequest copyWith(void Function(AddEdgesRequest) updates) =>
      super.copyWith((message) => updates(message as AddEdgesRequest))
          as AddEdgesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AddEdgesRequest create() => AddEdgesRequest._();
  @$core.override
  AddEdgesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static AddEdgesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AddEdgesRequest>(create);
  static AddEdgesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<Edge> get edges => $_getList(0);

  /// contrib_ids is an optional index-aligned list of 24-byte idempotency
  /// keys, one per entry in edges. When contrib_ids[i] is set (non-empty and
  /// not all-zero) the server records edges[i] at most once per distinct id,
  /// making a retried batch safe to replay without double-counting weight. A
  /// shorter list, a missing entry, or an empty/zero value leaves the
  /// corresponding edge on the legacy non-idempotent additive path. Each id is
  /// opaque to the server.
  ///
  /// Canonical 24-byte layout (the one spec; drift breaks cross-SDK dedup):
  ///   bytes [0:16] = client nonce / origin NodeID
  ///   bytes [16:24] = big-endian uint64 (seq<<16)|idx
  /// where seq is a per-client/per-mutation monotonic counter and idx is the
  /// edge's position within its batch (folding idx into the low 16 bits lets
  /// one batch carry up to 65 536 distinct ids under a single seq). Three
  /// hand-written encoders must stay byte-identical: sdks/go/client.go
  /// nextContribIDs, sdks/node/src/contrib.ts contribIdFrom, and
  /// server/service/apply.go contribIDFor. Golden-vector tests in all three
  /// suites (#922) fail CI on any unilateral change.
  @$pb.TagNumber(2)
  $pb.PbList<$core.List<$core.int>> get contribIds => $_getList(1);
}

class AddEdgesResponse extends $pb.GeneratedMessage {
  factory AddEdgesResponse({
    $core.int? written,
    $core.Iterable<$core.double>? effectiveWeights,
  }) {
    final result = create();
    if (written != null) result.written = written;
    if (effectiveWeights != null)
      result.effectiveWeights.addAll(effectiveWeights);
    return result;
  }

  AddEdgesResponse._();

  factory AddEdgesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory AddEdgesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'AddEdgesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'written')
    ..p<$core.double>(
        2, _omitFieldNames ? '' : 'effectiveWeights', $pb.PbFieldType.KF)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  AddEdgesResponse copyWith(void Function(AddEdgesResponse) updates) =>
      super.copyWith((message) => updates(message as AddEdgesResponse))
          as AddEdgesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static AddEdgesResponse create() => AddEdgesResponse._();
  @$core.override
  AddEdgesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static AddEdgesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<AddEdgesResponse>(create);
  static AddEdgesResponse? _defaultInstance;

  /// Number of edges whose weight contributions were accepted.
  @$pb.TagNumber(1)
  $core.int get written => $_getIZ(0);
  @$pb.TagNumber(1)
  set written($core.int value) => $_setSignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasWritten() => $_has(0);
  @$pb.TagNumber(1)
  void clearWritten() => $_clearField(1);

  /// effective_weights is index-aligned with the request edges: entry i is the
  /// sum of live contributions on edges[i]'s (tail, head) immediately after
  /// applying (or deduping) it, as seen by the serving node. Empty only for an
  /// empty request. See AddEdgeResponse.effective_weight for the counter
  /// semantics and the replication caveat.
  @$pb.TagNumber(2)
  $pb.PbList<$core.double> get effectiveWeights => $_getList(1);
}

/// PutEdgeRequest overwrites a single (tail, head) pair, replacing any
/// existing weight and expiration. This is the singular convenience wrapper
/// over PutEdges and shares its idempotent semantics.
class PutEdgeRequest extends $pb.GeneratedMessage {
  factory PutEdgeRequest({
    Edge? edge,
  }) {
    final result = create();
    if (edge != null) result.edge = edge;
    return result;
  }

  PutEdgeRequest._();

  factory PutEdgeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutEdgeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutEdgeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<Edge>(1, _omitFieldNames ? '' : 'edge', subBuilder: Edge.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgeRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgeRequest copyWith(void Function(PutEdgeRequest) updates) =>
      super.copyWith((message) => updates(message as PutEdgeRequest))
          as PutEdgeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutEdgeRequest create() => PutEdgeRequest._();
  @$core.override
  PutEdgeRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutEdgeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutEdgeRequest>(create);
  static PutEdgeRequest? _defaultInstance;

  @$pb.TagNumber(1)
  Edge get edge => $_getN(0);
  @$pb.TagNumber(1)
  set edge(Edge value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasEdge() => $_has(0);
  @$pb.TagNumber(1)
  void clearEdge() => $_clearField(1);
  @$pb.TagNumber(1)
  Edge ensureEdge() => $_ensure(0);
}

class PutEdgeResponse extends $pb.GeneratedMessage {
  factory PutEdgeResponse() => create();

  PutEdgeResponse._();

  factory PutEdgeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutEdgeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutEdgeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgeResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgeResponse copyWith(void Function(PutEdgeResponse) updates) =>
      super.copyWith((message) => updates(message as PutEdgeResponse))
          as PutEdgeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutEdgeResponse create() => PutEdgeResponse._();
  @$core.override
  PutEdgeResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutEdgeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutEdgeResponse>(create);
  static PutEdgeResponse? _defaultInstance;
}

/// PutEdgesRequest overwrites each (tail, head) pair, replacing any existing
/// weight and expiration. This operation is idempotent.
class PutEdgesRequest extends $pb.GeneratedMessage {
  factory PutEdgesRequest({
    $core.Iterable<Edge>? edges,
  }) {
    final result = create();
    if (edges != null) result.edges.addAll(edges);
    return result;
  }

  PutEdgesRequest._();

  factory PutEdgesRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutEdgesRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutEdgesRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..pPM<Edge>(1, _omitFieldNames ? '' : 'edges', subBuilder: Edge.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgesRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgesRequest copyWith(void Function(PutEdgesRequest) updates) =>
      super.copyWith((message) => updates(message as PutEdgesRequest))
          as PutEdgesRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutEdgesRequest create() => PutEdgesRequest._();
  @$core.override
  PutEdgesRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutEdgesRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutEdgesRequest>(create);
  static PutEdgesRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbList<Edge> get edges => $_getList(0);
}

class PutEdgesResponse extends $pb.GeneratedMessage {
  factory PutEdgesResponse({
    $core.int? written,
  }) {
    final result = create();
    if (written != null) result.written = written;
    return result;
  }

  PutEdgesResponse._();

  factory PutEdgesResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PutEdgesResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PutEdgesResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aI(1, _omitFieldNames ? '' : 'written')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgesResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PutEdgesResponse copyWith(void Function(PutEdgesResponse) updates) =>
      super.copyWith((message) => updates(message as PutEdgesResponse))
          as PutEdgesResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PutEdgesResponse create() => PutEdgesResponse._();
  @$core.override
  PutEdgesResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static PutEdgesResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PutEdgesResponse>(create);
  static PutEdgesResponse? _defaultInstance;

  /// Number of edges accepted (overwritten or created).
  @$pb.TagNumber(1)
  $core.int get written => $_getIZ(0);
  @$pb.TagNumber(1)
  set written($core.int value) => $_setSignedInt32(0, value);
  @$pb.TagNumber(1)
  $core.bool hasWritten() => $_has(0);
  @$pb.TagNumber(1)
  void clearWritten() => $_clearField(1);
}

/// GetServerStatusRequest carries no parameters — the response is a flat
/// snapshot of the server's identity, build, configuration ceilings, and
/// current live counts. Intended for the admin UI's "Ops" tab and any
/// lightweight smoke-test tooling that just needs to confirm "the server
/// is up and roughly configured as expected".
class GetServerStatusRequest extends $pb.GeneratedMessage {
  factory GetServerStatusRequest() => create();

  GetServerStatusRequest._();

  factory GetServerStatusRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetServerStatusRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetServerStatusRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetServerStatusRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetServerStatusRequest copyWith(
          void Function(GetServerStatusRequest) updates) =>
      super.copyWith((message) => updates(message as GetServerStatusRequest))
          as GetServerStatusRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetServerStatusRequest create() => GetServerStatusRequest._();
  @$core.override
  GetServerStatusRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetServerStatusRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetServerStatusRequest>(create);
  static GetServerStatusRequest? _defaultInstance;
}

class GetServerStatusResponse extends $pb.GeneratedMessage {
  factory GetServerStatusResponse({
    $core.String? version,
    $core.String? goVersion,
    $1.Timestamp? startedAt,
    $2.Duration? uptime,
    $2.Duration? defaultTtl,
    $core.int? maxBatchSize,
    $core.int? maxKeyBytes,
    $core.int? scanDefaultLimit,
    $core.int? scanMaxLimit,
    $core.bool? tlsEnabled,
    $core.bool? replicationEnabled,
    $fixnum.Int64? vertexCount,
    $fixnum.Int64? edgeCount,
  }) {
    final result = create();
    if (version != null) result.version = version;
    if (goVersion != null) result.goVersion = goVersion;
    if (startedAt != null) result.startedAt = startedAt;
    if (uptime != null) result.uptime = uptime;
    if (defaultTtl != null) result.defaultTtl = defaultTtl;
    if (maxBatchSize != null) result.maxBatchSize = maxBatchSize;
    if (maxKeyBytes != null) result.maxKeyBytes = maxKeyBytes;
    if (scanDefaultLimit != null) result.scanDefaultLimit = scanDefaultLimit;
    if (scanMaxLimit != null) result.scanMaxLimit = scanMaxLimit;
    if (tlsEnabled != null) result.tlsEnabled = tlsEnabled;
    if (replicationEnabled != null)
      result.replicationEnabled = replicationEnabled;
    if (vertexCount != null) result.vertexCount = vertexCount;
    if (edgeCount != null) result.edgeCount = edgeCount;
    return result;
  }

  GetServerStatusResponse._();

  factory GetServerStatusResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetServerStatusResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetServerStatusResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'version')
    ..aOS(2, _omitFieldNames ? '' : 'goVersion')
    ..aOM<$1.Timestamp>(3, _omitFieldNames ? '' : 'startedAt',
        subBuilder: $1.Timestamp.create)
    ..aOM<$2.Duration>(4, _omitFieldNames ? '' : 'uptime',
        subBuilder: $2.Duration.create)
    ..aOM<$2.Duration>(5, _omitFieldNames ? '' : 'defaultTtl',
        subBuilder: $2.Duration.create)
    ..aI(6, _omitFieldNames ? '' : 'maxBatchSize',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(7, _omitFieldNames ? '' : 'maxKeyBytes',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(8, _omitFieldNames ? '' : 'scanDefaultLimit',
        fieldType: $pb.PbFieldType.OU3)
    ..aI(9, _omitFieldNames ? '' : 'scanMaxLimit',
        fieldType: $pb.PbFieldType.OU3)
    ..aOB(10, _omitFieldNames ? '' : 'tlsEnabled')
    ..aOB(11, _omitFieldNames ? '' : 'replicationEnabled')
    ..a<$fixnum.Int64>(
        12, _omitFieldNames ? '' : 'vertexCount', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        13, _omitFieldNames ? '' : 'edgeCount', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetServerStatusResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetServerStatusResponse copyWith(
          void Function(GetServerStatusResponse) updates) =>
      super.copyWith((message) => updates(message as GetServerStatusResponse))
          as GetServerStatusResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetServerStatusResponse create() => GetServerStatusResponse._();
  @$core.override
  GetServerStatusResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetServerStatusResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetServerStatusResponse>(create);
  static GetServerStatusResponse? _defaultInstance;

  /// Build/version stamp. Falls back to "dev" when the binary was built
  /// without VCS info or without LANTERN_VERSION set.
  @$pb.TagNumber(1)
  $core.String get version => $_getSZ(0);
  @$pb.TagNumber(1)
  set version($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVersion() => $_has(0);
  @$pb.TagNumber(1)
  void clearVersion() => $_clearField(1);

  /// Reports `runtime.Version()` (e.g. "go1.26.4").
  @$pb.TagNumber(2)
  $core.String get goVersion => $_getSZ(1);
  @$pb.TagNumber(2)
  set goVersion($core.String value) => $_setString(1, value);
  @$pb.TagNumber(2)
  $core.bool hasGoVersion() => $_has(1);
  @$pb.TagNumber(2)
  void clearGoVersion() => $_clearField(2);

  /// Wall-clock instant the server process started serving requests.
  /// Captured when the Connect listener starts accepting, not at process
  /// start, so the value reflects "ready to serve" rather than "wire
  /// init done".
  @$pb.TagNumber(3)
  $1.Timestamp get startedAt => $_getN(2);
  @$pb.TagNumber(3)
  set startedAt($1.Timestamp value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasStartedAt() => $_has(2);
  @$pb.TagNumber(3)
  void clearStartedAt() => $_clearField(3);
  @$pb.TagNumber(3)
  $1.Timestamp ensureStartedAt() => $_ensure(2);

  /// Convenience field: now - started_at on the server side, so clients
  /// do not have to know the server's clock to display uptime.
  @$pb.TagNumber(4)
  $2.Duration get uptime => $_getN(3);
  @$pb.TagNumber(4)
  set uptime($2.Duration value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasUptime() => $_has(3);
  @$pb.TagNumber(4)
  void clearUptime() => $_clearField(4);
  @$pb.TagNumber(4)
  $2.Duration ensureUptime() => $_ensure(3);

  /// The default TTL applied to vertices/edges when the caller does not
  /// specify Expiration (LANTERN_DEFAULT_TTL_SECONDS).
  @$pb.TagNumber(5)
  $2.Duration get defaultTtl => $_getN(4);
  @$pb.TagNumber(5)
  set defaultTtl($2.Duration value) => $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasDefaultTtl() => $_has(4);
  @$pb.TagNumber(5)
  void clearDefaultTtl() => $_clearField(5);
  @$pb.TagNumber(5)
  $2.Duration ensureDefaultTtl() => $_ensure(4);

  /// Validation ceiling for batch RPCs (LANTERN_MAX_BATCH_SIZE).
  @$pb.TagNumber(6)
  $core.int get maxBatchSize => $_getIZ(5);
  @$pb.TagNumber(6)
  set maxBatchSize($core.int value) => $_setUnsignedInt32(5, value);
  @$pb.TagNumber(6)
  $core.bool hasMaxBatchSize() => $_has(5);
  @$pb.TagNumber(6)
  void clearMaxBatchSize() => $_clearField(6);

  /// Validation ceiling for vertex/edge keys (LANTERN_MAX_KEY_BYTES).
  @$pb.TagNumber(7)
  $core.int get maxKeyBytes => $_getIZ(6);
  @$pb.TagNumber(7)
  set maxKeyBytes($core.int value) => $_setUnsignedInt32(6, value);
  @$pb.TagNumber(7)
  $core.bool hasMaxKeyBytes() => $_has(6);
  @$pb.TagNumber(7)
  void clearMaxKeyBytes() => $_clearField(7);

  /// Per-call defaults / hard caps for prefix-scan pagination
  /// (LANTERN_SCAN_DEFAULT_LIMIT / LANTERN_SCAN_MAX_LIMIT).
  @$pb.TagNumber(8)
  $core.int get scanDefaultLimit => $_getIZ(7);
  @$pb.TagNumber(8)
  set scanDefaultLimit($core.int value) => $_setUnsignedInt32(7, value);
  @$pb.TagNumber(8)
  $core.bool hasScanDefaultLimit() => $_has(7);
  @$pb.TagNumber(8)
  void clearScanDefaultLimit() => $_clearField(8);

  @$pb.TagNumber(9)
  $core.int get scanMaxLimit => $_getIZ(8);
  @$pb.TagNumber(9)
  set scanMaxLimit($core.int value) => $_setUnsignedInt32(8, value);
  @$pb.TagNumber(9)
  $core.bool hasScanMaxLimit() => $_has(8);
  @$pb.TagNumber(9)
  void clearScanMaxLimit() => $_clearField(9);

  /// True when the server is terminating TLS
  /// (LANTERN_TLS_CERT_FILE + LANTERN_TLS_KEY_FILE both set).
  @$pb.TagNumber(10)
  $core.bool get tlsEnabled => $_getBF(9);
  @$pb.TagNumber(10)
  set tlsEnabled($core.bool value) => $_setBool(9, value);
  @$pb.TagNumber(10)
  $core.bool hasTlsEnabled() => $_has(9);
  @$pb.TagNumber(10)
  void clearTlsEnabled() => $_clearField(10);

  /// True when this server is wired to a mutation log + HLC clock and is
  /// therefore a member of a replication group. False on single-node
  /// deployments.
  @$pb.TagNumber(11)
  $core.bool get replicationEnabled => $_getBF(10);
  @$pb.TagNumber(11)
  set replicationEnabled($core.bool value) => $_setBool(10, value);
  @$pb.TagNumber(11)
  $core.bool hasReplicationEnabled() => $_has(10);
  @$pb.TagNumber(11)
  void clearReplicationEnabled() => $_clearField(11);

  /// Live counts pulled from the in-memory graph cache. Cheap to compute
  /// (index sizes, no scan). Intended for at-a-glance dashboards — these
  /// are not transactional snapshots and may include not-yet-collected
  /// expired entries bounded by the GC tick.
  @$pb.TagNumber(12)
  $fixnum.Int64 get vertexCount => $_getI64(11);
  @$pb.TagNumber(12)
  set vertexCount($fixnum.Int64 value) => $_setInt64(11, value);
  @$pb.TagNumber(12)
  $core.bool hasVertexCount() => $_has(11);
  @$pb.TagNumber(12)
  void clearVertexCount() => $_clearField(12);

  @$pb.TagNumber(13)
  $fixnum.Int64 get edgeCount => $_getI64(12);
  @$pb.TagNumber(13)
  set edgeCount($fixnum.Int64 value) => $_setInt64(12, value);
  @$pb.TagNumber(13)
  $core.bool hasEdgeCount() => $_has(12);
  @$pb.TagNumber(13)
  void clearEdgeCount() => $_clearField(13);
}

/// ReplicationPeer is one row of the GetReplicationStatus snapshot.
/// Each row models the local node's view of a single outbound peer
/// connection owned by the replication pump (#185). Fields are
/// best-effort point-in-time samples; clients should treat consecutive
/// snapshots as monotonically advancing rather than transactional.
class ReplicationPeer extends $pb.GeneratedMessage {
  factory ReplicationPeer({
    $core.String? address,
    ReplicationPeer_State? state,
    $1.Timestamp? lastEventAt,
    $fixnum.Int64? appliedSeq,
    $core.String? error,
  }) {
    final result = create();
    if (address != null) result.address = address;
    if (state != null) result.state = state;
    if (lastEventAt != null) result.lastEventAt = lastEventAt;
    if (appliedSeq != null) result.appliedSeq = appliedSeq;
    if (error != null) result.error = error;
    return result;
  }

  ReplicationPeer._();

  factory ReplicationPeer.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory ReplicationPeer.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'ReplicationPeer',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'address')
    ..aE<ReplicationPeer_State>(2, _omitFieldNames ? '' : 'state',
        enumValues: ReplicationPeer_State.values)
    ..aOM<$1.Timestamp>(3, _omitFieldNames ? '' : 'lastEventAt',
        subBuilder: $1.Timestamp.create)
    ..a<$fixnum.Int64>(
        4, _omitFieldNames ? '' : 'appliedSeq', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOS(5, _omitFieldNames ? '' : 'error')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ReplicationPeer clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  ReplicationPeer copyWith(void Function(ReplicationPeer) updates) =>
      super.copyWith((message) => updates(message as ReplicationPeer))
          as ReplicationPeer;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static ReplicationPeer create() => ReplicationPeer._();
  @$core.override
  ReplicationPeer createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static ReplicationPeer getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<ReplicationPeer>(create);
  static ReplicationPeer? _defaultInstance;

  /// address is the dial target as configured in LANTERN_PEERS (or
  /// resolved from DNS discovery). Stable across reconnects for a given
  /// peer, so it doubles as the row identity for admin UI displays.
  @$pb.TagNumber(1)
  $core.String get address => $_getSZ(0);
  @$pb.TagNumber(1)
  set address($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasAddress() => $_has(0);
  @$pb.TagNumber(1)
  void clearAddress() => $_clearField(1);

  @$pb.TagNumber(2)
  ReplicationPeer_State get state => $_getN(1);
  @$pb.TagNumber(2)
  set state(ReplicationPeer_State value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasState() => $_has(1);
  @$pb.TagNumber(2)
  void clearState() => $_clearField(2);

  /// last_event_at is the wall-clock instant the pump last received any
  /// frame (Subscribe or Snapshot) from this peer. Unset when nothing
  /// has been received yet. Combined with the response-level local_now
  /// it yields the "how stale is this stream" diagnostic the admin UI
  /// shows as a lag bar.
  @$pb.TagNumber(3)
  $1.Timestamp get lastEventAt => $_getN(2);
  @$pb.TagNumber(3)
  set lastEventAt($1.Timestamp value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasLastEventAt() => $_has(2);
  @$pb.TagNumber(3)
  void clearLastEventAt() => $_clearField(3);
  @$pb.TagNumber(3)
  $1.Timestamp ensureLastEventAt() => $_ensure(2);

  /// applied_seq is the highest peer-local sequence number the pump has
  /// successfully consumed from this peer. Zero on a fresh connection.
  @$pb.TagNumber(4)
  $fixnum.Int64 get appliedSeq => $_getI64(3);
  @$pb.TagNumber(4)
  set appliedSeq($fixnum.Int64 value) => $_setInt64(3, value);
  @$pb.TagNumber(4)
  $core.bool hasAppliedSeq() => $_has(3);
  @$pb.TagNumber(4)
  void clearAppliedSeq() => $_clearField(4);

  /// error carries the last non-recoverable error message reported by
  /// the per-peer session loop. Cleared on the next successful frame.
  @$pb.TagNumber(5)
  $core.String get error => $_getSZ(4);
  @$pb.TagNumber(5)
  set error($core.String value) => $_setString(4, value);
  @$pb.TagNumber(5)
  $core.bool hasError() => $_has(4);
  @$pb.TagNumber(5)
  void clearError() => $_clearField(5);
}

/// GetReplicationStatusRequest carries no parameters. The response is a
/// flat snapshot of the local node's view of every outbound peer.
class GetReplicationStatusRequest extends $pb.GeneratedMessage {
  factory GetReplicationStatusRequest() => create();

  GetReplicationStatusRequest._();

  factory GetReplicationStatusRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetReplicationStatusRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetReplicationStatusRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetReplicationStatusRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetReplicationStatusRequest copyWith(
          void Function(GetReplicationStatusRequest) updates) =>
      super.copyWith(
              (message) => updates(message as GetReplicationStatusRequest))
          as GetReplicationStatusRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetReplicationStatusRequest create() =>
      GetReplicationStatusRequest._();
  @$core.override
  GetReplicationStatusRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetReplicationStatusRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetReplicationStatusRequest>(create);
  static GetReplicationStatusRequest? _defaultInstance;
}

class GetReplicationStatusResponse extends $pb.GeneratedMessage {
  factory GetReplicationStatusResponse({
    $core.String? nodeId,
    $1.Timestamp? localNow,
    $core.bool? enabled,
    $core.Iterable<ReplicationPeer>? peers,
  }) {
    final result = create();
    if (nodeId != null) result.nodeId = nodeId;
    if (localNow != null) result.localNow = localNow;
    if (enabled != null) result.enabled = enabled;
    if (peers != null) result.peers.addAll(peers);
    return result;
  }

  GetReplicationStatusResponse._();

  factory GetReplicationStatusResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory GetReplicationStatusResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'GetReplicationStatusResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'nodeId')
    ..aOM<$1.Timestamp>(2, _omitFieldNames ? '' : 'localNow',
        subBuilder: $1.Timestamp.create)
    ..aOB(3, _omitFieldNames ? '' : 'enabled')
    ..pPM<ReplicationPeer>(10, _omitFieldNames ? '' : 'peers',
        subBuilder: ReplicationPeer.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetReplicationStatusResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  GetReplicationStatusResponse copyWith(
          void Function(GetReplicationStatusResponse) updates) =>
      super.copyWith(
              (message) => updates(message as GetReplicationStatusResponse))
          as GetReplicationStatusResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static GetReplicationStatusResponse create() =>
      GetReplicationStatusResponse._();
  @$core.override
  GetReplicationStatusResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static GetReplicationStatusResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<GetReplicationStatusResponse>(create);
  static GetReplicationStatusResponse? _defaultInstance;

  /// node_id is the local HLC NodeID rendered as lowercase hex (32 chars).
  /// Stable for the lifetime of the process; either configured via
  /// LANTERN_NODE_ID or randomly generated at startup.
  @$pb.TagNumber(1)
  $core.String get nodeId => $_getSZ(0);
  @$pb.TagNumber(1)
  set nodeId($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasNodeId() => $_has(0);
  @$pb.TagNumber(1)
  void clearNodeId() => $_clearField(1);

  /// local_now is the server's wall-clock at the moment the snapshot
  /// was taken. Provided so clients can compute per-peer staleness
  /// (local_now - last_event_at) without trusting their own clock.
  @$pb.TagNumber(2)
  $1.Timestamp get localNow => $_getN(1);
  @$pb.TagNumber(2)
  set localNow($1.Timestamp value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasLocalNow() => $_has(1);
  @$pb.TagNumber(2)
  void clearLocalNow() => $_clearField(2);
  @$pb.TagNumber(2)
  $1.Timestamp ensureLocalNow() => $_ensure(1);

  /// enabled is false on a single-instance deployment (no peers
  /// configured AND no DNS discovery). When false, peers is empty but
  /// the response is still well-formed — handlers never return
  /// Unimplemented for this RPC.
  @$pb.TagNumber(3)
  $core.bool get enabled => $_getBF(2);
  @$pb.TagNumber(3)
  set enabled($core.bool value) => $_setBool(2, value);
  @$pb.TagNumber(3)
  $core.bool hasEnabled() => $_has(2);
  @$pb.TagNumber(3)
  void clearEnabled() => $_clearField(3);

  /// peers is the per-peer slice. Order is the supervisor's iteration
  /// order and is intentionally unspecified; clients sort by address
  /// for display.
  @$pb.TagNumber(10)
  $pb.PbList<ReplicationPeer> get peers => $_getList(3);
}

/// BackupSnapshotRequest parameterises a whole-graph backup stream.
/// vertex_prefix, when non-empty, restricts the backup to the induced
/// subgraph over vertices whose key has this prefix (an edge is included
/// only when BOTH endpoints match). Empty = the whole graph.
class BackupSnapshotRequest extends $pb.GeneratedMessage {
  factory BackupSnapshotRequest({
    $core.String? vertexPrefix,
  }) {
    final result = create();
    if (vertexPrefix != null) result.vertexPrefix = vertexPrefix;
    return result;
  }

  BackupSnapshotRequest._();

  factory BackupSnapshotRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BackupSnapshotRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BackupSnapshotRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'vertexPrefix')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BackupSnapshotRequest clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BackupSnapshotRequest copyWith(
          void Function(BackupSnapshotRequest) updates) =>
      super.copyWith((message) => updates(message as BackupSnapshotRequest))
          as BackupSnapshotRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BackupSnapshotRequest create() => BackupSnapshotRequest._();
  @$core.override
  BackupSnapshotRequest createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BackupSnapshotRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BackupSnapshotRequest>(create);
  static BackupSnapshotRequest? _defaultInstance;

  @$pb.TagNumber(1)
  $core.String get vertexPrefix => $_getSZ(0);
  @$pb.TagNumber(1)
  set vertexPrefix($core.String value) => $_setString(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVertexPrefix() => $_has(0);
  @$pb.TagNumber(1)
  void clearVertexPrefix() => $_clearField(1);
}

enum BackupSnapshotResponse_Record { vertex, edge, notSet }

/// BackupSnapshotResponse is one frame of the BackupSnapshot stream:
/// either a live vertex or a folded live edge (weight summed across
/// contributions, expiration = the furthest-future contribution). The two
/// kinds are interleaved; consumers route on the oneof.
class BackupSnapshotResponse extends $pb.GeneratedMessage {
  factory BackupSnapshotResponse({
    Vertex? vertex,
    Edge? edge,
  }) {
    final result = create();
    if (vertex != null) result.vertex = vertex;
    if (edge != null) result.edge = edge;
    return result;
  }

  BackupSnapshotResponse._();

  factory BackupSnapshotResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory BackupSnapshotResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, BackupSnapshotResponse_Record>
      _BackupSnapshotResponse_RecordByTag = {
    1: BackupSnapshotResponse_Record.vertex,
    2: BackupSnapshotResponse_Record.edge,
    0: BackupSnapshotResponse_Record.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'BackupSnapshotResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [1, 2])
    ..aOM<Vertex>(1, _omitFieldNames ? '' : 'vertex', subBuilder: Vertex.create)
    ..aOM<Edge>(2, _omitFieldNames ? '' : 'edge', subBuilder: Edge.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BackupSnapshotResponse clone() => deepCopy();
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  BackupSnapshotResponse copyWith(
          void Function(BackupSnapshotResponse) updates) =>
      super.copyWith((message) => updates(message as BackupSnapshotResponse))
          as BackupSnapshotResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static BackupSnapshotResponse create() => BackupSnapshotResponse._();
  @$core.override
  BackupSnapshotResponse createEmptyInstance() => create();
  @$core.pragma('dart2js:noInline')
  static BackupSnapshotResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<BackupSnapshotResponse>(create);
  static BackupSnapshotResponse? _defaultInstance;

  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  BackupSnapshotResponse_Record whichRecord() =>
      _BackupSnapshotResponse_RecordByTag[$_whichOneof(0)]!;
  @$pb.TagNumber(1)
  @$pb.TagNumber(2)
  void clearRecord() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  Vertex get vertex => $_getN(0);
  @$pb.TagNumber(1)
  set vertex(Vertex value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVertex() => $_has(0);
  @$pb.TagNumber(1)
  void clearVertex() => $_clearField(1);
  @$pb.TagNumber(1)
  Vertex ensureVertex() => $_ensure(0);

  @$pb.TagNumber(2)
  Edge get edge => $_getN(1);
  @$pb.TagNumber(2)
  set edge(Edge value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasEdge() => $_has(1);
  @$pb.TagNumber(2)
  void clearEdge() => $_clearField(2);
  @$pb.TagNumber(2)
  Edge ensureEdge() => $_ensure(1);
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
