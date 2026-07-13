// This is a generated file - do not edit.
//
// Generated from graph/v1/replication.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:fixnum/fixnum.dart' as $fixnum;
import 'package:protobuf/protobuf.dart' as $pb;

import '../../google/protobuf/timestamp.pb.dart' as $1;
import 'graph.pb.dart' as $0;

export 'package:protobuf/protobuf.dart' show GeneratedMessageGenericExtensions;

/// HLCTimestamp is the wire form of core/hlc.Timestamp. All replicated
/// mutations carry one of these as their causal coordinate.
///
///   wall_ns: physical wall-clock component, nanoseconds since Unix epoch.
///   logical: per-tick logical counter, incremented on collision.
///   node_id: 16-byte origin node identifier (matches core/hlc.NodeID).
///
/// The triple (wall_ns, logical, node_id) gives a strict total order; see
/// docs/replication.md §5 and core/hlc/hlc.go.
class HLCTimestamp extends $pb.GeneratedMessage {
  factory HLCTimestamp({
    $fixnum.Int64? wallNs,
    $core.int? logical,
    $core.List<$core.int>? nodeId,
  }) {
    final result = create();
    if (wallNs != null) result.wallNs = wallNs;
    if (logical != null) result.logical = logical;
    if (nodeId != null) result.nodeId = nodeId;
    return result;
  }

  HLCTimestamp._();

  factory HLCTimestamp.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory HLCTimestamp.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'HLCTimestamp',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aInt64(1, _omitFieldNames ? '' : 'wallNs')
    ..a<$core.int>(2, _omitFieldNames ? '' : 'logical', $pb.PbFieldType.OU3)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'nodeId', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  HLCTimestamp clone() => HLCTimestamp()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  HLCTimestamp copyWith(void Function(HLCTimestamp) updates) =>
      super.copyWith((message) => updates(message as HLCTimestamp))
          as HLCTimestamp;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static HLCTimestamp create() => HLCTimestamp._();
  @$core.override
  HLCTimestamp createEmptyInstance() => create();
  static $pb.PbList<HLCTimestamp> createRepeated() =>
      $pb.PbList<HLCTimestamp>();
  @$core.pragma('dart2js:noInline')
  static HLCTimestamp getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<HLCTimestamp>(create);
  static HLCTimestamp? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get wallNs => $_getI64(0);
  @$pb.TagNumber(1)
  set wallNs($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasWallNs() => $_has(0);
  @$pb.TagNumber(1)
  void clearWallNs() => $_clearField(1);

  @$pb.TagNumber(2)
  $core.int get logical => $_getIZ(1);
  @$pb.TagNumber(2)
  set logical($core.int value) => $_setUnsignedInt32(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLogical() => $_has(1);
  @$pb.TagNumber(2)
  void clearLogical() => $_clearField(2);

  @$pb.TagNumber(3)
  $core.List<$core.int> get nodeId => $_getN(2);
  @$pb.TagNumber(3)
  set nodeId($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasNodeId() => $_has(2);
  @$pb.TagNumber(3)
  void clearNodeId() => $_clearField(3);
}

enum MutationOp_Op {
  putVertex,
  putVertices,
  deleteVertex,
  deleteVertices,
  deleteVerticesByPrefix,
  addEdge,
  addEdges,
  putEdge,
  putEdges,
  deleteEdge,
  deleteEdges,
  deleteEdgesByPrefix,
  notSet
}

/// MutationOp is the discriminated payload of a single mutation. Each
/// variant mirrors the request message of the corresponding write RPC so the
/// existing server handlers can be reused verbatim when applying replicated
/// mutations.
///
/// Singular variants (PutVertex, AddEdge, ...) are retained as distinct
/// arms — they are the wire form chosen by the client and round-tripping
/// them faithfully simplifies CDC consumers that mirror RPC semantics.
class MutationOp extends $pb.GeneratedMessage {
  factory MutationOp({
    $0.PutVertexRequest? putVertex,
    $0.PutVerticesRequest? putVertices,
    $0.DeleteVertexRequest? deleteVertex,
    $0.DeleteVerticesRequest? deleteVertices,
    $0.DeleteVerticesByPrefixRequest? deleteVerticesByPrefix,
    $0.AddEdgeRequest? addEdge,
    $0.AddEdgesRequest? addEdges,
    $0.PutEdgeRequest? putEdge,
    $0.PutEdgesRequest? putEdges,
    $0.DeleteEdgeRequest? deleteEdge,
    $0.DeleteEdgesRequest? deleteEdges,
    $0.DeleteEdgesByPrefixRequest? deleteEdgesByPrefix,
  }) {
    final result = create();
    if (putVertex != null) result.putVertex = putVertex;
    if (putVertices != null) result.putVertices = putVertices;
    if (deleteVertex != null) result.deleteVertex = deleteVertex;
    if (deleteVertices != null) result.deleteVertices = deleteVertices;
    if (deleteVerticesByPrefix != null)
      result.deleteVerticesByPrefix = deleteVerticesByPrefix;
    if (addEdge != null) result.addEdge = addEdge;
    if (addEdges != null) result.addEdges = addEdges;
    if (putEdge != null) result.putEdge = putEdge;
    if (putEdges != null) result.putEdges = putEdges;
    if (deleteEdge != null) result.deleteEdge = deleteEdge;
    if (deleteEdges != null) result.deleteEdges = deleteEdges;
    if (deleteEdgesByPrefix != null)
      result.deleteEdgesByPrefix = deleteEdgesByPrefix;
    return result;
  }

  MutationOp._();

  factory MutationOp.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory MutationOp.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, MutationOp_Op> _MutationOp_OpByTag = {
    1: MutationOp_Op.putVertex,
    2: MutationOp_Op.putVertices,
    3: MutationOp_Op.deleteVertex,
    4: MutationOp_Op.deleteVertices,
    5: MutationOp_Op.deleteVerticesByPrefix,
    6: MutationOp_Op.addEdge,
    7: MutationOp_Op.addEdges,
    8: MutationOp_Op.putEdge,
    9: MutationOp_Op.putEdges,
    10: MutationOp_Op.deleteEdge,
    11: MutationOp_Op.deleteEdges,
    12: MutationOp_Op.deleteEdgesByPrefix,
    0: MutationOp_Op.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'MutationOp',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12])
    ..aOM<$0.PutVertexRequest>(1, _omitFieldNames ? '' : 'putVertex',
        subBuilder: $0.PutVertexRequest.create)
    ..aOM<$0.PutVerticesRequest>(2, _omitFieldNames ? '' : 'putVertices',
        subBuilder: $0.PutVerticesRequest.create)
    ..aOM<$0.DeleteVertexRequest>(3, _omitFieldNames ? '' : 'deleteVertex',
        subBuilder: $0.DeleteVertexRequest.create)
    ..aOM<$0.DeleteVerticesRequest>(4, _omitFieldNames ? '' : 'deleteVertices',
        subBuilder: $0.DeleteVerticesRequest.create)
    ..aOM<$0.DeleteVerticesByPrefixRequest>(
        5, _omitFieldNames ? '' : 'deleteVerticesByPrefix',
        subBuilder: $0.DeleteVerticesByPrefixRequest.create)
    ..aOM<$0.AddEdgeRequest>(6, _omitFieldNames ? '' : 'addEdge',
        subBuilder: $0.AddEdgeRequest.create)
    ..aOM<$0.AddEdgesRequest>(7, _omitFieldNames ? '' : 'addEdges',
        subBuilder: $0.AddEdgesRequest.create)
    ..aOM<$0.PutEdgeRequest>(8, _omitFieldNames ? '' : 'putEdge',
        subBuilder: $0.PutEdgeRequest.create)
    ..aOM<$0.PutEdgesRequest>(9, _omitFieldNames ? '' : 'putEdges',
        subBuilder: $0.PutEdgesRequest.create)
    ..aOM<$0.DeleteEdgeRequest>(10, _omitFieldNames ? '' : 'deleteEdge',
        subBuilder: $0.DeleteEdgeRequest.create)
    ..aOM<$0.DeleteEdgesRequest>(11, _omitFieldNames ? '' : 'deleteEdges',
        subBuilder: $0.DeleteEdgesRequest.create)
    ..aOM<$0.DeleteEdgesByPrefixRequest>(
        12, _omitFieldNames ? '' : 'deleteEdgesByPrefix',
        subBuilder: $0.DeleteEdgesByPrefixRequest.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  MutationOp clone() => MutationOp()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  MutationOp copyWith(void Function(MutationOp) updates) =>
      super.copyWith((message) => updates(message as MutationOp)) as MutationOp;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static MutationOp create() => MutationOp._();
  @$core.override
  MutationOp createEmptyInstance() => create();
  static $pb.PbList<MutationOp> createRepeated() => $pb.PbList<MutationOp>();
  @$core.pragma('dart2js:noInline')
  static MutationOp getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<MutationOp>(create);
  static MutationOp? _defaultInstance;

  MutationOp_Op whichOp() => _MutationOp_OpByTag[$_whichOneof(0)]!;
  void clearOp() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  $0.PutVertexRequest get putVertex => $_getN(0);
  @$pb.TagNumber(1)
  set putVertex($0.PutVertexRequest value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasPutVertex() => $_has(0);
  @$pb.TagNumber(1)
  void clearPutVertex() => $_clearField(1);
  @$pb.TagNumber(1)
  $0.PutVertexRequest ensurePutVertex() => $_ensure(0);

  @$pb.TagNumber(2)
  $0.PutVerticesRequest get putVertices => $_getN(1);
  @$pb.TagNumber(2)
  set putVertices($0.PutVerticesRequest value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasPutVertices() => $_has(1);
  @$pb.TagNumber(2)
  void clearPutVertices() => $_clearField(2);
  @$pb.TagNumber(2)
  $0.PutVerticesRequest ensurePutVertices() => $_ensure(1);

  @$pb.TagNumber(3)
  $0.DeleteVertexRequest get deleteVertex => $_getN(2);
  @$pb.TagNumber(3)
  set deleteVertex($0.DeleteVertexRequest value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasDeleteVertex() => $_has(2);
  @$pb.TagNumber(3)
  void clearDeleteVertex() => $_clearField(3);
  @$pb.TagNumber(3)
  $0.DeleteVertexRequest ensureDeleteVertex() => $_ensure(2);

  @$pb.TagNumber(4)
  $0.DeleteVerticesRequest get deleteVertices => $_getN(3);
  @$pb.TagNumber(4)
  set deleteVertices($0.DeleteVerticesRequest value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasDeleteVertices() => $_has(3);
  @$pb.TagNumber(4)
  void clearDeleteVertices() => $_clearField(4);
  @$pb.TagNumber(4)
  $0.DeleteVerticesRequest ensureDeleteVertices() => $_ensure(3);

  @$pb.TagNumber(5)
  $0.DeleteVerticesByPrefixRequest get deleteVerticesByPrefix => $_getN(4);
  @$pb.TagNumber(5)
  set deleteVerticesByPrefix($0.DeleteVerticesByPrefixRequest value) =>
      $_setField(5, value);
  @$pb.TagNumber(5)
  $core.bool hasDeleteVerticesByPrefix() => $_has(4);
  @$pb.TagNumber(5)
  void clearDeleteVerticesByPrefix() => $_clearField(5);
  @$pb.TagNumber(5)
  $0.DeleteVerticesByPrefixRequest ensureDeleteVerticesByPrefix() =>
      $_ensure(4);

  @$pb.TagNumber(6)
  $0.AddEdgeRequest get addEdge => $_getN(5);
  @$pb.TagNumber(6)
  set addEdge($0.AddEdgeRequest value) => $_setField(6, value);
  @$pb.TagNumber(6)
  $core.bool hasAddEdge() => $_has(5);
  @$pb.TagNumber(6)
  void clearAddEdge() => $_clearField(6);
  @$pb.TagNumber(6)
  $0.AddEdgeRequest ensureAddEdge() => $_ensure(5);

  @$pb.TagNumber(7)
  $0.AddEdgesRequest get addEdges => $_getN(6);
  @$pb.TagNumber(7)
  set addEdges($0.AddEdgesRequest value) => $_setField(7, value);
  @$pb.TagNumber(7)
  $core.bool hasAddEdges() => $_has(6);
  @$pb.TagNumber(7)
  void clearAddEdges() => $_clearField(7);
  @$pb.TagNumber(7)
  $0.AddEdgesRequest ensureAddEdges() => $_ensure(6);

  @$pb.TagNumber(8)
  $0.PutEdgeRequest get putEdge => $_getN(7);
  @$pb.TagNumber(8)
  set putEdge($0.PutEdgeRequest value) => $_setField(8, value);
  @$pb.TagNumber(8)
  $core.bool hasPutEdge() => $_has(7);
  @$pb.TagNumber(8)
  void clearPutEdge() => $_clearField(8);
  @$pb.TagNumber(8)
  $0.PutEdgeRequest ensurePutEdge() => $_ensure(7);

  @$pb.TagNumber(9)
  $0.PutEdgesRequest get putEdges => $_getN(8);
  @$pb.TagNumber(9)
  set putEdges($0.PutEdgesRequest value) => $_setField(9, value);
  @$pb.TagNumber(9)
  $core.bool hasPutEdges() => $_has(8);
  @$pb.TagNumber(9)
  void clearPutEdges() => $_clearField(9);
  @$pb.TagNumber(9)
  $0.PutEdgesRequest ensurePutEdges() => $_ensure(8);

  @$pb.TagNumber(10)
  $0.DeleteEdgeRequest get deleteEdge => $_getN(9);
  @$pb.TagNumber(10)
  set deleteEdge($0.DeleteEdgeRequest value) => $_setField(10, value);
  @$pb.TagNumber(10)
  $core.bool hasDeleteEdge() => $_has(9);
  @$pb.TagNumber(10)
  void clearDeleteEdge() => $_clearField(10);
  @$pb.TagNumber(10)
  $0.DeleteEdgeRequest ensureDeleteEdge() => $_ensure(9);

  @$pb.TagNumber(11)
  $0.DeleteEdgesRequest get deleteEdges => $_getN(10);
  @$pb.TagNumber(11)
  set deleteEdges($0.DeleteEdgesRequest value) => $_setField(11, value);
  @$pb.TagNumber(11)
  $core.bool hasDeleteEdges() => $_has(10);
  @$pb.TagNumber(11)
  void clearDeleteEdges() => $_clearField(11);
  @$pb.TagNumber(11)
  $0.DeleteEdgesRequest ensureDeleteEdges() => $_ensure(10);

  @$pb.TagNumber(12)
  $0.DeleteEdgesByPrefixRequest get deleteEdgesByPrefix => $_getN(11);
  @$pb.TagNumber(12)
  set deleteEdgesByPrefix($0.DeleteEdgesByPrefixRequest value) =>
      $_setField(12, value);
  @$pb.TagNumber(12)
  $core.bool hasDeleteEdgesByPrefix() => $_has(11);
  @$pb.TagNumber(12)
  void clearDeleteEdgesByPrefix() => $_clearField(12);
  @$pb.TagNumber(12)
  $0.DeleteEdgesByPrefixRequest ensureDeleteEdgesByPrefix() => $_ensure(11);
}

/// Mutation is the unit of replication: a sequenced, HLC-stamped,
/// origin-tagged graph write. `seq` is assigned by the originating node's
/// mutation log (see core/mutationlog) and is strictly monotone within a
/// single origin.
///
///   seq:    per-origin monotone sequence number, assigned at append time.
///   hlc:    causal timestamp stamped at append time.
///   origin: 16-byte node identifier of the node that first accepted the
///           write; mirrors hlc.node_id but is kept as an explicit field so
///           anti-entropy can index by origin without inspecting the HLC.
///   op:     the actual write payload (see MutationOp).
class Mutation extends $pb.GeneratedMessage {
  factory Mutation({
    $fixnum.Int64? seq,
    HLCTimestamp? hlc,
    $core.List<$core.int>? origin,
    MutationOp? op,
  }) {
    final result = create();
    if (seq != null) result.seq = seq;
    if (hlc != null) result.hlc = hlc;
    if (origin != null) result.origin = origin;
    if (op != null) result.op = op;
    return result;
  }

  Mutation._();

  factory Mutation.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory Mutation.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'Mutation',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(1, _omitFieldNames ? '' : 'seq', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOM<HLCTimestamp>(2, _omitFieldNames ? '' : 'hlc',
        subBuilder: HLCTimestamp.create)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'origin', $pb.PbFieldType.OY)
    ..aOM<MutationOp>(4, _omitFieldNames ? '' : 'op',
        subBuilder: MutationOp.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Mutation clone() => Mutation()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  Mutation copyWith(void Function(Mutation) updates) =>
      super.copyWith((message) => updates(message as Mutation)) as Mutation;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static Mutation create() => Mutation._();
  @$core.override
  Mutation createEmptyInstance() => create();
  static $pb.PbList<Mutation> createRepeated() => $pb.PbList<Mutation>();
  @$core.pragma('dart2js:noInline')
  static Mutation getDefault() =>
      _defaultInstance ??= $pb.GeneratedMessage.$_defaultFor<Mutation>(create);
  static Mutation? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get seq => $_getI64(0);
  @$pb.TagNumber(1)
  set seq($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSeq() => $_has(0);
  @$pb.TagNumber(1)
  void clearSeq() => $_clearField(1);

  @$pb.TagNumber(2)
  HLCTimestamp get hlc => $_getN(1);
  @$pb.TagNumber(2)
  set hlc(HLCTimestamp value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasHlc() => $_has(1);
  @$pb.TagNumber(2)
  void clearHlc() => $_clearField(2);
  @$pb.TagNumber(2)
  HLCTimestamp ensureHlc() => $_ensure(1);

  @$pb.TagNumber(3)
  $core.List<$core.int> get origin => $_getN(2);
  @$pb.TagNumber(3)
  set origin($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasOrigin() => $_has(2);
  @$pb.TagNumber(3)
  void clearOrigin() => $_clearField(3);

  @$pb.TagNumber(4)
  MutationOp get op => $_getN(3);
  @$pb.TagNumber(4)
  set op(MutationOp value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasOp() => $_has(3);
  @$pb.TagNumber(4)
  void clearOp() => $_clearField(4);
  @$pb.TagNumber(4)
  MutationOp ensureOp() => $_ensure(3);
}

/// SubscribeRequest opens a stream of replicated mutations starting at
/// the per-origin cursor in `from_seq_per_origin`.
///
/// Under the leaderless Subscribe contract (#415, B-3), every replica's
/// local mutation log carries entries from every cluster origin (each
/// stamped with its writer's HLC NodeID). A consumer can therefore pick
/// any one replica and see every committed cluster mutation; on failover
/// to a different replica it resumes by passing the highest `seq` it
/// has already observed FOR EACH origin.
///
/// Semantics:
///
///   - Empty map (or unset field) requests every entry the server still
///     retains. This is the new-consumer / cold-start case.
///   - For each origin key present in the map, the server delivers only
///     entries with `seq >= from_seq_per_origin[origin]` for that
///     origin. Entries for origins NOT in the map are delivered from
///     the oldest retained entry; this lets a consumer that has only
///     ever talked to a subset of replicas naturally pick up entries
///     from a newly-joined origin.
///   - If the resulting overall earliest requested seq is below the
///     server's first retained log seq the call fails with
///     FAILED_PRECONDITION and the caller must snapshot + resubscribe.
///
/// Keys are 32-character lowercase hexadecimal encodings of the 16-byte
/// HLC NodeID (matching `HLCTimestamp.node_id` and `Mutation.origin`).
/// Hex was chosen over raw bytes because proto3 map keys forbid `bytes`
/// and because the hex form already appears in `lantern_replication_*`
/// Prometheus labels and in admin UI surfaces, keeping the consumer
/// debug experience uniform across wire, metrics, and UI.
class SubscribeRequest extends $pb.GeneratedMessage {
  factory SubscribeRequest({
    $core.Iterable<$core.MapEntry<$core.String, $fixnum.Int64>>?
        fromSeqPerOrigin,
    $fixnum.Int64? fromLocalSeq,
  }) {
    final result = create();
    if (fromSeqPerOrigin != null)
      result.fromSeqPerOrigin.addEntries(fromSeqPerOrigin);
    if (fromLocalSeq != null) result.fromLocalSeq = fromLocalSeq;
    return result;
  }

  SubscribeRequest._();

  factory SubscribeRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SubscribeRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SubscribeRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..m<$core.String, $fixnum.Int64>(
        1, _omitFieldNames ? '' : 'fromSeqPerOrigin',
        entryClassName: 'SubscribeRequest.FromSeqPerOriginEntry',
        keyFieldType: $pb.PbFieldType.OS,
        valueFieldType: $pb.PbFieldType.OU6,
        packageName: const $pb.PackageName('graph.v1'))
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'fromLocalSeq', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscribeRequest clone() => SubscribeRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscribeRequest copyWith(void Function(SubscribeRequest) updates) =>
      super.copyWith((message) => updates(message as SubscribeRequest))
          as SubscribeRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SubscribeRequest create() => SubscribeRequest._();
  @$core.override
  SubscribeRequest createEmptyInstance() => create();
  static $pb.PbList<SubscribeRequest> createRepeated() =>
      $pb.PbList<SubscribeRequest>();
  @$core.pragma('dart2js:noInline')
  static SubscribeRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SubscribeRequest>(create);
  static SubscribeRequest? _defaultInstance;

  /// Per-origin resume cursor. Keys are 32-char lowercase hex of the
  /// 16-byte HLC NodeID; values are the next local `seq` the consumer
  /// expects from that origin.
  @$pb.TagNumber(1)
  $pb.PbMap<$core.String, $fixnum.Int64> get fromSeqPerOrigin => $_getMap(0);

  /// Next entry in this responder's replica-local mutation log. This is only
  /// valid when resuming against the SAME responder that emitted
  /// SnapshotHeader.cutoff_local_seq; zero uses the portable per-origin path.
  /// Keeping the two sequence domains separate prevents a stale per-origin
  /// cursor from bypassing ring-buffer gap detection.
  @$pb.TagNumber(2)
  $fixnum.Int64 get fromLocalSeq => $_getI64(1);
  @$pb.TagNumber(2)
  set fromLocalSeq($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasFromLocalSeq() => $_has(1);
  @$pb.TagNumber(2)
  void clearFromLocalSeq() => $_clearField(2);
}

/// SubscribeResponse carries one replicated mutation per message.
class SubscribeResponse extends $pb.GeneratedMessage {
  factory SubscribeResponse({
    Mutation? mutation,
  }) {
    final result = create();
    if (mutation != null) result.mutation = mutation;
    return result;
  }

  SubscribeResponse._();

  factory SubscribeResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SubscribeResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SubscribeResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<Mutation>(1, _omitFieldNames ? '' : 'mutation',
        subBuilder: Mutation.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscribeResponse clone() => SubscribeResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SubscribeResponse copyWith(void Function(SubscribeResponse) updates) =>
      super.copyWith((message) => updates(message as SubscribeResponse))
          as SubscribeResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SubscribeResponse create() => SubscribeResponse._();
  @$core.override
  SubscribeResponse createEmptyInstance() => create();
  static $pb.PbList<SubscribeResponse> createRepeated() =>
      $pb.PbList<SubscribeResponse>();
  @$core.pragma('dart2js:noInline')
  static SubscribeResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SubscribeResponse>(create);
  static SubscribeResponse? _defaultInstance;

  @$pb.TagNumber(1)
  Mutation get mutation => $_getN(0);
  @$pb.TagNumber(1)
  set mutation(Mutation value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasMutation() => $_has(0);
  @$pb.TagNumber(1)
  void clearMutation() => $_clearField(1);
  @$pb.TagNumber(1)
  Mutation ensureMutation() => $_ensure(0);
}

/// SnapshotRequest opens a server-streaming snapshot of the entire live
/// graph state at a single causal cutoff. The request body is intentionally
/// empty in this phase (#184); future revisions may add prefix / shard
/// filters without breaking the wire contract.
class SnapshotRequest extends $pb.GeneratedMessage {
  factory SnapshotRequest() => create();

  SnapshotRequest._();

  factory SnapshotRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SnapshotRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SnapshotRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotRequest clone() => SnapshotRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotRequest copyWith(void Function(SnapshotRequest) updates) =>
      super.copyWith((message) => updates(message as SnapshotRequest))
          as SnapshotRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SnapshotRequest create() => SnapshotRequest._();
  @$core.override
  SnapshotRequest createEmptyInstance() => create();
  static $pb.PbList<SnapshotRequest> createRepeated() =>
      $pb.PbList<SnapshotRequest>();
  @$core.pragma('dart2js:noInline')
  static SnapshotRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SnapshotRequest>(create);
  static SnapshotRequest? _defaultInstance;
}

/// SnapshotHeader is always the FIRST SnapshotResponse on the wire. It
/// freezes the per-origin watermark and the snapshot-open HLC the server
/// used to materialise the snapshot.
///
/// A bootstrapping peer MUST persist `cutoff_seq_per_origin`,
/// `cutoff_local_seq`, and `cutoff_hlc` before applying any payload entries and
/// MUST resume Subscribe against the SAME responder with both
/// `from_seq_per_origin = {origin: seq+1 for each (origin, seq) in
/// cutoff_seq_per_origin}` and `from_local_seq = cutoff_local_seq+1` so the
/// snapshot and the live tail stitch without gap or overlap.
///
/// Keys in `cutoff_seq_per_origin` are 32-char lowercase hex of the
/// 16-byte HLC NodeID, matching `SubscribeRequest.from_seq_per_origin`.
/// Values are the local seq of the last entry the server had applied
/// from each origin when the snapshot started. An empty map indicates
/// the server has not yet applied any origin (cold cluster) and the
/// resume Subscribe should pass an empty cursor.
class SnapshotHeader extends $pb.GeneratedMessage {
  factory SnapshotHeader({
    $core.Iterable<$core.MapEntry<$core.String, $fixnum.Int64>>?
        cutoffSeqPerOrigin,
    HLCTimestamp? cutoffHlc,
    $fixnum.Int64? cutoffLocalSeq,
  }) {
    final result = create();
    if (cutoffSeqPerOrigin != null)
      result.cutoffSeqPerOrigin.addEntries(cutoffSeqPerOrigin);
    if (cutoffHlc != null) result.cutoffHlc = cutoffHlc;
    if (cutoffLocalSeq != null) result.cutoffLocalSeq = cutoffLocalSeq;
    return result;
  }

  SnapshotHeader._();

  factory SnapshotHeader.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SnapshotHeader.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SnapshotHeader',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..m<$core.String, $fixnum.Int64>(
        1, _omitFieldNames ? '' : 'cutoffSeqPerOrigin',
        entryClassName: 'SnapshotHeader.CutoffSeqPerOriginEntry',
        keyFieldType: $pb.PbFieldType.OS,
        valueFieldType: $pb.PbFieldType.OU6,
        packageName: const $pb.PackageName('graph.v1'))
    ..aOM<HLCTimestamp>(2, _omitFieldNames ? '' : 'cutoffHlc',
        subBuilder: HLCTimestamp.create)
    ..a<$fixnum.Int64>(
        3, _omitFieldNames ? '' : 'cutoffLocalSeq', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotHeader clone() => SnapshotHeader()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotHeader copyWith(void Function(SnapshotHeader) updates) =>
      super.copyWith((message) => updates(message as SnapshotHeader))
          as SnapshotHeader;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SnapshotHeader create() => SnapshotHeader._();
  @$core.override
  SnapshotHeader createEmptyInstance() => create();
  static $pb.PbList<SnapshotHeader> createRepeated() =>
      $pb.PbList<SnapshotHeader>();
  @$core.pragma('dart2js:noInline')
  static SnapshotHeader getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SnapshotHeader>(create);
  static SnapshotHeader? _defaultInstance;

  @$pb.TagNumber(1)
  $pb.PbMap<$core.String, $fixnum.Int64> get cutoffSeqPerOrigin => $_getMap(0);

  @$pb.TagNumber(2)
  HLCTimestamp get cutoffHlc => $_getN(1);
  @$pb.TagNumber(2)
  set cutoffHlc(HLCTimestamp value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasCutoffHlc() => $_has(1);
  @$pb.TagNumber(2)
  void clearCutoffHlc() => $_clearField(2);
  @$pb.TagNumber(2)
  HLCTimestamp ensureCutoffHlc() => $_ensure(1);

  /// Replica-local mutation-log position at snapshot open. It is deliberately
  /// separate from the portable per-origin watermarks and only resumes a tail
  /// against the same responder.
  @$pb.TagNumber(3)
  $fixnum.Int64 get cutoffLocalSeq => $_getI64(2);
  @$pb.TagNumber(3)
  set cutoffLocalSeq($fixnum.Int64 value) => $_setInt64(2, value);
  @$pb.TagNumber(3)
  $core.bool hasCutoffLocalSeq() => $_has(2);
  @$pb.TagNumber(3)
  void clearCutoffLocalSeq() => $_clearField(3);
}

/// SnapshotFooter is always the LAST SnapshotResponse on the wire. It carries
/// the running counts the server actually streamed so receivers can detect
/// truncation (channel-close, send-error mid-stream) without re-walking
/// their freshly-imported state.
class SnapshotFooter extends $pb.GeneratedMessage {
  factory SnapshotFooter({
    $fixnum.Int64? vertexCount,
    $fixnum.Int64? edgeCount,
  }) {
    final result = create();
    if (vertexCount != null) result.vertexCount = vertexCount;
    if (edgeCount != null) result.edgeCount = edgeCount;
    return result;
  }

  SnapshotFooter._();

  factory SnapshotFooter.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SnapshotFooter.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SnapshotFooter',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$fixnum.Int64>(
        1, _omitFieldNames ? '' : 'vertexCount', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..a<$fixnum.Int64>(
        2, _omitFieldNames ? '' : 'edgeCount', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotFooter clone() => SnapshotFooter()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotFooter copyWith(void Function(SnapshotFooter) updates) =>
      super.copyWith((message) => updates(message as SnapshotFooter))
          as SnapshotFooter;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SnapshotFooter create() => SnapshotFooter._();
  @$core.override
  SnapshotFooter createEmptyInstance() => create();
  static $pb.PbList<SnapshotFooter> createRepeated() =>
      $pb.PbList<SnapshotFooter>();
  @$core.pragma('dart2js:noInline')
  static SnapshotFooter getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SnapshotFooter>(create);
  static SnapshotFooter? _defaultInstance;

  @$pb.TagNumber(1)
  $fixnum.Int64 get vertexCount => $_getI64(0);
  @$pb.TagNumber(1)
  set vertexCount($fixnum.Int64 value) => $_setInt64(0, value);
  @$pb.TagNumber(1)
  $core.bool hasVertexCount() => $_has(0);
  @$pb.TagNumber(1)
  void clearVertexCount() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get edgeCount => $_getI64(1);
  @$pb.TagNumber(2)
  set edgeCount($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasEdgeCount() => $_has(1);
  @$pb.TagNumber(2)
  void clearEdgeCount() => $_clearField(2);
}

/// SnapshotVertex is the snapshot-time representation of a single live
/// vertex. The HLC field carries the last LWW timestamp the source node
/// recorded for the key (zero when the vertex has never been touched by a
/// replicated Put, i.e. local-only writes); receivers feed it back through
/// PutVertexWithExpirationHLC so subsequent older replays (#182) are
/// correctly rejected.
class SnapshotVertex extends $pb.GeneratedMessage {
  factory SnapshotVertex({
    $0.Vertex? vertex,
    HLCTimestamp? hlc,
  }) {
    final result = create();
    if (vertex != null) result.vertex = vertex;
    if (hlc != null) result.hlc = hlc;
    return result;
  }

  SnapshotVertex._();

  factory SnapshotVertex.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SnapshotVertex.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SnapshotVertex',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOM<$0.Vertex>(1, _omitFieldNames ? '' : 'vertex',
        subBuilder: $0.Vertex.create)
    ..aOM<HLCTimestamp>(2, _omitFieldNames ? '' : 'hlc',
        subBuilder: HLCTimestamp.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotVertex clone() => SnapshotVertex()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotVertex copyWith(void Function(SnapshotVertex) updates) =>
      super.copyWith((message) => updates(message as SnapshotVertex))
          as SnapshotVertex;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SnapshotVertex create() => SnapshotVertex._();
  @$core.override
  SnapshotVertex createEmptyInstance() => create();
  static $pb.PbList<SnapshotVertex> createRepeated() =>
      $pb.PbList<SnapshotVertex>();
  @$core.pragma('dart2js:noInline')
  static SnapshotVertex getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SnapshotVertex>(create);
  static SnapshotVertex? _defaultInstance;

  @$pb.TagNumber(1)
  $0.Vertex get vertex => $_getN(0);
  @$pb.TagNumber(1)
  set vertex($0.Vertex value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasVertex() => $_has(0);
  @$pb.TagNumber(1)
  void clearVertex() => $_clearField(1);
  @$pb.TagNumber(1)
  $0.Vertex ensureVertex() => $_ensure(0);

  @$pb.TagNumber(2)
  HLCTimestamp get hlc => $_getN(1);
  @$pb.TagNumber(2)
  set hlc(HLCTimestamp value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasHlc() => $_has(1);
  @$pb.TagNumber(2)
  void clearHlc() => $_clearField(2);
  @$pb.TagNumber(2)
  HLCTimestamp ensureHlc() => $_ensure(1);
}

/// SnapshotEdgeContribution is one additive (or LWW-imported) entry inside
/// an edge's weight bucket. Snapshot preserves the per-contribution
/// decomposition so that the receiver's ContribID dedup (#182) keeps
/// suppressing duplicates when peer-pump later re-delivers the same
/// contribution from the live tail.
///
///   contrib_id: 24-byte ContribID; empty/zero when the contribution
///               originated from a local non-replicated AddEdge and dedup
///               is disabled (the legacy zero-id semantics).
class SnapshotEdgeContribution extends $pb.GeneratedMessage {
  factory SnapshotEdgeContribution({
    $core.double? weight,
    $1.Timestamp? expiration,
    $core.List<$core.int>? contribId,
  }) {
    final result = create();
    if (weight != null) result.weight = weight;
    if (expiration != null) result.expiration = expiration;
    if (contribId != null) result.contribId = contribId;
    return result;
  }

  SnapshotEdgeContribution._();

  factory SnapshotEdgeContribution.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SnapshotEdgeContribution.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SnapshotEdgeContribution',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.double>(1, _omitFieldNames ? '' : 'weight', $pb.PbFieldType.OF)
    ..aOM<$1.Timestamp>(2, _omitFieldNames ? '' : 'expiration',
        subBuilder: $1.Timestamp.create)
    ..a<$core.List<$core.int>>(
        3, _omitFieldNames ? '' : 'contribId', $pb.PbFieldType.OY)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotEdgeContribution clone() =>
      SnapshotEdgeContribution()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotEdgeContribution copyWith(
          void Function(SnapshotEdgeContribution) updates) =>
      super.copyWith((message) => updates(message as SnapshotEdgeContribution))
          as SnapshotEdgeContribution;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SnapshotEdgeContribution create() => SnapshotEdgeContribution._();
  @$core.override
  SnapshotEdgeContribution createEmptyInstance() => create();
  static $pb.PbList<SnapshotEdgeContribution> createRepeated() =>
      $pb.PbList<SnapshotEdgeContribution>();
  @$core.pragma('dart2js:noInline')
  static SnapshotEdgeContribution getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SnapshotEdgeContribution>(create);
  static SnapshotEdgeContribution? _defaultInstance;

  @$pb.TagNumber(1)
  $core.double get weight => $_getN(0);
  @$pb.TagNumber(1)
  set weight($core.double value) => $_setFloat(0, value);
  @$pb.TagNumber(1)
  $core.bool hasWeight() => $_has(0);
  @$pb.TagNumber(1)
  void clearWeight() => $_clearField(1);

  /// Absolute expiration. An absent timestamp means a permanent contribution.
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

  @$pb.TagNumber(3)
  $core.List<$core.int> get contribId => $_getN(2);
  @$pb.TagNumber(3)
  set contribId($core.List<$core.int> value) => $_setBytes(2, value);
  @$pb.TagNumber(3)
  $core.bool hasContribId() => $_has(2);
  @$pb.TagNumber(3)
  void clearContribId() => $_clearField(3);
}

/// SnapshotEdge is the snapshot-time representation of a single live edge.
/// `hlc` carries the bucket's lastHLC (the most recent Put-LWW position;
/// zero when no LWW write has happened) so receivers can apply each
/// contribution via AddEdgeWithExpirationContribHLC with the right LWW
/// floor, keeping ContribID dedup intact.
class SnapshotEdge extends $pb.GeneratedMessage {
  factory SnapshotEdge({
    $core.String? tail,
    $core.String? head,
    HLCTimestamp? hlc,
    $core.Iterable<SnapshotEdgeContribution>? contributions,
  }) {
    final result = create();
    if (tail != null) result.tail = tail;
    if (head != null) result.head = head;
    if (hlc != null) result.hlc = hlc;
    if (contributions != null) result.contributions.addAll(contributions);
    return result;
  }

  SnapshotEdge._();

  factory SnapshotEdge.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SnapshotEdge.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SnapshotEdge',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..aOS(1, _omitFieldNames ? '' : 'tail')
    ..aOS(2, _omitFieldNames ? '' : 'head')
    ..aOM<HLCTimestamp>(3, _omitFieldNames ? '' : 'hlc',
        subBuilder: HLCTimestamp.create)
    ..pc<SnapshotEdgeContribution>(
        4, _omitFieldNames ? '' : 'contributions', $pb.PbFieldType.PM,
        subBuilder: SnapshotEdgeContribution.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotEdge clone() => SnapshotEdge()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotEdge copyWith(void Function(SnapshotEdge) updates) =>
      super.copyWith((message) => updates(message as SnapshotEdge))
          as SnapshotEdge;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SnapshotEdge create() => SnapshotEdge._();
  @$core.override
  SnapshotEdge createEmptyInstance() => create();
  static $pb.PbList<SnapshotEdge> createRepeated() =>
      $pb.PbList<SnapshotEdge>();
  @$core.pragma('dart2js:noInline')
  static SnapshotEdge getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SnapshotEdge>(create);
  static SnapshotEdge? _defaultInstance;

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
  HLCTimestamp get hlc => $_getN(2);
  @$pb.TagNumber(3)
  set hlc(HLCTimestamp value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasHlc() => $_has(2);
  @$pb.TagNumber(3)
  void clearHlc() => $_clearField(3);
  @$pb.TagNumber(3)
  HLCTimestamp ensureHlc() => $_ensure(2);

  @$pb.TagNumber(4)
  $pb.PbList<SnapshotEdgeContribution> get contributions => $_getList(3);
}

enum SnapshotResponse_Entry { header, vertex, edge, footer, notSet }

/// SnapshotResponse is the union type streamed from `rpc Snapshot`. The frame
/// order is always: exactly one SnapshotHeader, then zero or more
/// SnapshotVertex frames, then zero or more SnapshotEdge frames, then
/// exactly one SnapshotFooter. Receivers SHOULD treat any other order as a
/// protocol violation.
class SnapshotResponse extends $pb.GeneratedMessage {
  factory SnapshotResponse({
    SnapshotHeader? header,
    SnapshotVertex? vertex,
    SnapshotEdge? edge,
    SnapshotFooter? footer,
  }) {
    final result = create();
    if (header != null) result.header = header;
    if (vertex != null) result.vertex = vertex;
    if (edge != null) result.edge = edge;
    if (footer != null) result.footer = footer;
    return result;
  }

  SnapshotResponse._();

  factory SnapshotResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory SnapshotResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static const $core.Map<$core.int, SnapshotResponse_Entry>
      _SnapshotResponse_EntryByTag = {
    1: SnapshotResponse_Entry.header,
    2: SnapshotResponse_Entry.vertex,
    3: SnapshotResponse_Entry.edge,
    4: SnapshotResponse_Entry.footer,
    0: SnapshotResponse_Entry.notSet
  };
  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'SnapshotResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..oo(0, [1, 2, 3, 4])
    ..aOM<SnapshotHeader>(1, _omitFieldNames ? '' : 'header',
        subBuilder: SnapshotHeader.create)
    ..aOM<SnapshotVertex>(2, _omitFieldNames ? '' : 'vertex',
        subBuilder: SnapshotVertex.create)
    ..aOM<SnapshotEdge>(3, _omitFieldNames ? '' : 'edge',
        subBuilder: SnapshotEdge.create)
    ..aOM<SnapshotFooter>(4, _omitFieldNames ? '' : 'footer',
        subBuilder: SnapshotFooter.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotResponse clone() => SnapshotResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  SnapshotResponse copyWith(void Function(SnapshotResponse) updates) =>
      super.copyWith((message) => updates(message as SnapshotResponse))
          as SnapshotResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static SnapshotResponse create() => SnapshotResponse._();
  @$core.override
  SnapshotResponse createEmptyInstance() => create();
  static $pb.PbList<SnapshotResponse> createRepeated() =>
      $pb.PbList<SnapshotResponse>();
  @$core.pragma('dart2js:noInline')
  static SnapshotResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<SnapshotResponse>(create);
  static SnapshotResponse? _defaultInstance;

  SnapshotResponse_Entry whichEntry() =>
      _SnapshotResponse_EntryByTag[$_whichOneof(0)]!;
  void clearEntry() => $_clearField($_whichOneof(0));

  @$pb.TagNumber(1)
  SnapshotHeader get header => $_getN(0);
  @$pb.TagNumber(1)
  set header(SnapshotHeader value) => $_setField(1, value);
  @$pb.TagNumber(1)
  $core.bool hasHeader() => $_has(0);
  @$pb.TagNumber(1)
  void clearHeader() => $_clearField(1);
  @$pb.TagNumber(1)
  SnapshotHeader ensureHeader() => $_ensure(0);

  @$pb.TagNumber(2)
  SnapshotVertex get vertex => $_getN(1);
  @$pb.TagNumber(2)
  set vertex(SnapshotVertex value) => $_setField(2, value);
  @$pb.TagNumber(2)
  $core.bool hasVertex() => $_has(1);
  @$pb.TagNumber(2)
  void clearVertex() => $_clearField(2);
  @$pb.TagNumber(2)
  SnapshotVertex ensureVertex() => $_ensure(1);

  @$pb.TagNumber(3)
  SnapshotEdge get edge => $_getN(2);
  @$pb.TagNumber(3)
  set edge(SnapshotEdge value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasEdge() => $_has(2);
  @$pb.TagNumber(3)
  void clearEdge() => $_clearField(3);
  @$pb.TagNumber(3)
  SnapshotEdge ensureEdge() => $_ensure(2);

  @$pb.TagNumber(4)
  SnapshotFooter get footer => $_getN(3);
  @$pb.TagNumber(4)
  set footer(SnapshotFooter value) => $_setField(4, value);
  @$pb.TagNumber(4)
  $core.bool hasFooter() => $_has(3);
  @$pb.TagNumber(4)
  void clearFooter() => $_clearField(4);
  @$pb.TagNumber(4)
  SnapshotFooter ensureFooter() => $_ensure(3);
}

/// PeerStatusRequest is intentionally empty — the responder always
/// returns its full per-origin map. Future revisions may add an
/// optional origin filter without breaking the wire contract.
class PeerStatusRequest extends $pb.GeneratedMessage {
  factory PeerStatusRequest() => create();

  PeerStatusRequest._();

  factory PeerStatusRequest.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PeerStatusRequest.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PeerStatusRequest',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PeerStatusRequest clone() => PeerStatusRequest()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PeerStatusRequest copyWith(void Function(PeerStatusRequest) updates) =>
      super.copyWith((message) => updates(message as PeerStatusRequest))
          as PeerStatusRequest;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PeerStatusRequest create() => PeerStatusRequest._();
  @$core.override
  PeerStatusRequest createEmptyInstance() => create();
  static $pb.PbList<PeerStatusRequest> createRepeated() =>
      $pb.PbList<PeerStatusRequest>();
  @$core.pragma('dart2js:noInline')
  static PeerStatusRequest getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PeerStatusRequest>(create);
  static PeerStatusRequest? _defaultInstance;
}

/// OriginState is the responder's last-applied position for a single
/// origin. origin is the 16-byte HLC NodeID; last_seq is the highest
/// per-origin seq ever passed through ApplyMutation (or appended to the
/// local log for the responder's own origin); last_hlc is the HLC of
/// that last-applied mutation.
class OriginState extends $pb.GeneratedMessage {
  factory OriginState({
    $core.List<$core.int>? origin,
    $fixnum.Int64? lastSeq,
    HLCTimestamp? lastHlc,
  }) {
    final result = create();
    if (origin != null) result.origin = origin;
    if (lastSeq != null) result.lastSeq = lastSeq;
    if (lastHlc != null) result.lastHlc = lastHlc;
    return result;
  }

  OriginState._();

  factory OriginState.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory OriginState.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'OriginState',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'origin', $pb.PbFieldType.OY)
    ..a<$fixnum.Int64>(2, _omitFieldNames ? '' : 'lastSeq', $pb.PbFieldType.OU6,
        defaultOrMaker: $fixnum.Int64.ZERO)
    ..aOM<HLCTimestamp>(3, _omitFieldNames ? '' : 'lastHlc',
        subBuilder: HLCTimestamp.create)
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OriginState clone() => OriginState()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  OriginState copyWith(void Function(OriginState) updates) =>
      super.copyWith((message) => updates(message as OriginState))
          as OriginState;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static OriginState create() => OriginState._();
  @$core.override
  OriginState createEmptyInstance() => create();
  static $pb.PbList<OriginState> createRepeated() => $pb.PbList<OriginState>();
  @$core.pragma('dart2js:noInline')
  static OriginState getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<OriginState>(create);
  static OriginState? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get origin => $_getN(0);
  @$pb.TagNumber(1)
  set origin($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasOrigin() => $_has(0);
  @$pb.TagNumber(1)
  void clearOrigin() => $_clearField(1);

  @$pb.TagNumber(2)
  $fixnum.Int64 get lastSeq => $_getI64(1);
  @$pb.TagNumber(2)
  set lastSeq($fixnum.Int64 value) => $_setInt64(1, value);
  @$pb.TagNumber(2)
  $core.bool hasLastSeq() => $_has(1);
  @$pb.TagNumber(2)
  void clearLastSeq() => $_clearField(2);

  @$pb.TagNumber(3)
  HLCTimestamp get lastHlc => $_getN(2);
  @$pb.TagNumber(3)
  set lastHlc(HLCTimestamp value) => $_setField(3, value);
  @$pb.TagNumber(3)
  $core.bool hasLastHlc() => $_has(2);
  @$pb.TagNumber(3)
  void clearLastHlc() => $_clearField(3);
  @$pb.TagNumber(3)
  HLCTimestamp ensureLastHlc() => $_ensure(2);
}

/// PeerStatusResponse carries the responder's per-origin convergence
/// map. Order is unspecified; callers index by origin.
///
/// self_origin names the responder's OWN 16-byte HLC NodeID so that
/// anti-entropy callers can pick out the row that represents this
/// peer's own writes without having to pre-configure peer NodeIDs.
class PeerStatusResponse extends $pb.GeneratedMessage {
  factory PeerStatusResponse({
    $core.List<$core.int>? selfOrigin,
    $core.Iterable<OriginState>? origins,
    $core.String? searchConfigFingerprint,
  }) {
    final result = create();
    if (selfOrigin != null) result.selfOrigin = selfOrigin;
    if (origins != null) result.origins.addAll(origins);
    if (searchConfigFingerprint != null)
      result.searchConfigFingerprint = searchConfigFingerprint;
    return result;
  }

  PeerStatusResponse._();

  factory PeerStatusResponse.fromBuffer($core.List<$core.int> data,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromBuffer(data, registry);
  factory PeerStatusResponse.fromJson($core.String json,
          [$pb.ExtensionRegistry registry = $pb.ExtensionRegistry.EMPTY]) =>
      create()..mergeFromJson(json, registry);

  static final $pb.BuilderInfo _i = $pb.BuilderInfo(
      _omitMessageNames ? '' : 'PeerStatusResponse',
      package: const $pb.PackageName(_omitMessageNames ? '' : 'graph.v1'),
      createEmptyInstance: create)
    ..a<$core.List<$core.int>>(
        1, _omitFieldNames ? '' : 'selfOrigin', $pb.PbFieldType.OY)
    ..pc<OriginState>(2, _omitFieldNames ? '' : 'origins', $pb.PbFieldType.PM,
        subBuilder: OriginState.create)
    ..aOS(3, _omitFieldNames ? '' : 'searchConfigFingerprint')
    ..hasRequiredFields = false;

  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PeerStatusResponse clone() => PeerStatusResponse()..mergeFromMessage(this);
  @$core.Deprecated('See https://github.com/google/protobuf.dart/issues/998.')
  PeerStatusResponse copyWith(void Function(PeerStatusResponse) updates) =>
      super.copyWith((message) => updates(message as PeerStatusResponse))
          as PeerStatusResponse;

  @$core.override
  $pb.BuilderInfo get info_ => _i;

  @$core.pragma('dart2js:noInline')
  static PeerStatusResponse create() => PeerStatusResponse._();
  @$core.override
  PeerStatusResponse createEmptyInstance() => create();
  static $pb.PbList<PeerStatusResponse> createRepeated() =>
      $pb.PbList<PeerStatusResponse>();
  @$core.pragma('dart2js:noInline')
  static PeerStatusResponse getDefault() => _defaultInstance ??=
      $pb.GeneratedMessage.$_defaultFor<PeerStatusResponse>(create);
  static PeerStatusResponse? _defaultInstance;

  @$pb.TagNumber(1)
  $core.List<$core.int> get selfOrigin => $_getN(0);
  @$pb.TagNumber(1)
  set selfOrigin($core.List<$core.int> value) => $_setBytes(0, value);
  @$pb.TagNumber(1)
  $core.bool hasSelfOrigin() => $_has(0);
  @$pb.TagNumber(1)
  void clearSelfOrigin() => $_clearField(1);

  @$pb.TagNumber(2)
  $pb.PbList<OriginState> get origins => $_getList(1);

  /// Fingerprint of every search setting that can change capabilities or
  /// ordered results. Replicas compare this before declaring themselves ready;
  /// empty means the responder cannot prove search-config compatibility.
  @$pb.TagNumber(3)
  $core.String get searchConfigFingerprint => $_getSZ(2);
  @$pb.TagNumber(3)
  set searchConfigFingerprint($core.String value) => $_setString(2, value);
  @$pb.TagNumber(3)
  $core.bool hasSearchConfigFingerprint() => $_has(2);
  @$pb.TagNumber(3)
  void clearSearchConfigFingerprint() => $_clearField(3);
}

/// LanternReplicationService carries the peer-to-peer (and CDC) replication
/// surface. It is intentionally a separate service from LanternService so
/// that operators can route, throttle, secure, or even disable replication
/// independently of the public read/write API.
///
/// NOTE — deviation from the original RFC: the Subscribe RPC was originally
/// specified to live on LanternService. The realised proto split introduces
/// a cyclic file import between graph.proto and replication.proto when both
/// the request/response messages and the RPC live on opposite sides of the
/// boundary. Hosting Subscribe on a dedicated service keeps the import
/// graph one-way (replication.proto → graph.proto) and is documented as
/// part of #178's implementation in docs/replication.md.
class LanternReplicationServiceApi {
  final $pb.RpcClient _client;

  LanternReplicationServiceApi(this._client);

  /// Subscribe streams replicated mutations to a peer (or CDC consumer)
  /// starting at the supplied per-origin cursor. A same-responder snapshot
  /// resume also supplies `from_local_seq`; the server replays retained entries
  /// from that local position first, then streams live mutations.
  ///
  /// If the requested replica-local replay window has been evicted, the call
  /// fails with FAILED_PRECONDITION ("gapped") and the caller must
  /// snapshot + resubscribe. Slow consumers whose channel backs up may
  /// also have the stream terminated with FAILED_PRECONDITION — the
  /// remedy is the same.
  ///
  /// No HTTP gateway annotation: replication is intentionally exposed
  /// only as Connect server-streaming (consumed by replication pumps on
  /// peer nodes, never directly from browsers).
  $async.Future<SubscribeResponse> subscribe(
          $pb.ClientContext? ctx, SubscribeRequest request) =>
      _client.invoke<SubscribeResponse>(ctx, 'LanternReplicationService',
          'Subscribe', request, SubscribeResponse());

  /// Snapshot streams a point-in-time, causally-consistent dump of every
  /// live vertex and edge to a bootstrapping peer. The first frame is a
  /// SnapshotHeader carrying the (cutoff_seq_per_origin, cutoff_local_seq,
  /// cutoff_hlc) the server used to materialise the snapshot; the last frame is a
  /// SnapshotFooter with the actual vertex / edge counts streamed.
  ///
  /// Bootstrap stitch contract: after receiving the SnapshotFooter the
  /// peer MUST call `Subscribe(from_seq_per_origin = {origin: seq+1 for
  /// each (origin, seq) in cutoff_seq_per_origin}, from_local_seq =
  /// cutoff_local_seq+1)` against the same responder to pick up the live tail.
  /// Without that the snapshot and live stream cannot be glued
  /// together without gap or overlap.
  ///
  /// No HTTP gateway annotation (parity with Subscribe).
  $async.Future<SnapshotResponse> snapshot(
          $pb.ClientContext? ctx, SnapshotRequest request) =>
      _client.invoke<SnapshotResponse>(ctx, 'LanternReplicationService',
          'Snapshot', request, SnapshotResponse());

  /// PeerStatus returns the responder's per-origin (last_applied_seq,
  /// last_applied_hlc) map. Used by anti-entropy (#186) as the
  /// convergence safety net for the Subscribe pump: a periodic
  /// PeerStatus poll lets each node detect that the peer's view of
  /// some origin is ahead of its own and request the missing tail
  /// (via Subscribe(from_seq = local+1)) or — when that returns
  /// FailedPrecondition — fall back to Snapshot.
  ///
  /// The reply set always includes the responder's OWN origin (taken
  /// from its mutation log's last appended seq + hlc) plus every
  /// remote origin whose mutation has ever been applied via
  /// ApplyMutation since process start. Origins the responder has
  /// never seen are simply absent from the map.
  ///
  /// The RPC is intentionally unary and stateless — callers can
  /// multiplex it across many peers without holding any server-side
  /// resources. Returns Unavailable when the mutation log is unset
  /// (single-instance test path).
  $async.Future<PeerStatusResponse> peerStatus(
          $pb.ClientContext? ctx, PeerStatusRequest request) =>
      _client.invoke<PeerStatusResponse>(ctx, 'LanternReplicationService',
          'PeerStatus', request, PeerStatusResponse());
}

const $core.bool _omitFieldNames =
    $core.bool.fromEnvironment('protobuf.omit_field_names');
const $core.bool _omitMessageNames =
    $core.bool.fromEnvironment('protobuf.omit_message_names');
