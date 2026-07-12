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

import 'package:protobuf/protobuf.dart' as $pb;

/// Reduction is the optional post-traversal tree view applied to a
/// BFS-discovered neighbourhood (#846). UNSPECIFIED returns the raw
/// discovered subgraph; MINIMUM_SPANNING_TREE / SHORTEST_PATH_TREE reduce it
/// to a tree rooted at the seed. MINIMUM_SPANNING_TREE means a directed
/// minimum/maximum rooted arborescence over Lantern's directed edges (not an
/// undirected Prim projection); the MIN/MAX direction is carried by Objective.
/// Reductions are a BfsParams knob — not sibling traversals — which is why the
/// former Algorithm enum (which conflated the two; see #410/#801 history) left
/// the request in the oneof redesign.
class Reduction extends $pb.ProtobufEnum {
  static const Reduction REDUCTION_UNSPECIFIED =
      Reduction._(0, _omitEnumNames ? '' : 'REDUCTION_UNSPECIFIED');
  static const Reduction REDUCTION_MINIMUM_SPANNING_TREE =
      Reduction._(1, _omitEnumNames ? '' : 'REDUCTION_MINIMUM_SPANNING_TREE');
  static const Reduction REDUCTION_SHORTEST_PATH_TREE =
      Reduction._(2, _omitEnumNames ? '' : 'REDUCTION_SHORTEST_PATH_TREE');

  static const $core.List<Reduction> values = <Reduction>[
    REDUCTION_UNSPECIFIED,
    REDUCTION_MINIMUM_SPANNING_TREE,
    REDUCTION_SHORTEST_PATH_TREE,
  ];

  static final $core.List<Reduction?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static Reduction? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const Reduction._(super.value, super.name);
}

/// Objective is the direction of the weight-sensitive optimisation. It
/// governs BOTH the per-hop top-k pruning during the BFS walk AND the
/// post-traversal Algorithm reduction (#560), so a cost-minimiser is never
/// handed a candidate set already pruned to the costliest edges.
///
/// UNSPECIFIED defaults to MAXIMIZE server-side. MINIMIZE treats edge
/// weights as costs (keeps the k smallest-weight edges per hop; smallest
/// tree wins); MAXIMIZE treats them as relevance (keeps the k largest-weight
/// edges per hop; largest tree wins, equivalent to the historical
/// "inverse-SPT" / "max-MST" variants and the default strongest-neighbour
/// behaviour of an Objective-unspecified BFS request).
class Objective extends $pb.ProtobufEnum {
  static const Objective OBJECTIVE_UNSPECIFIED =
      Objective._(0, _omitEnumNames ? '' : 'OBJECTIVE_UNSPECIFIED');
  static const Objective OBJECTIVE_MINIMIZE =
      Objective._(1, _omitEnumNames ? '' : 'OBJECTIVE_MINIMIZE');
  static const Objective OBJECTIVE_MAXIMIZE =
      Objective._(2, _omitEnumNames ? '' : 'OBJECTIVE_MAXIMIZE');

  static const $core.List<Objective> values = <Objective>[
    OBJECTIVE_UNSPECIFIED,
    OBJECTIVE_MINIMIZE,
    OBJECTIVE_MAXIMIZE,
  ];

  static final $core.List<Objective?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static Objective? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const Objective._(super.value, super.name);
}

/// Weighting is the edge-weight transform applied BEFORE the BFS walk
/// (and therefore before any Algorithm-driven reduction).
///
/// UNSPECIFIED defaults to RAW server-side. RAW uses the stored
/// edge.weight verbatim. TFIDF re-scores edge weights using the crude
/// hub-suppressor w / log2(1 + df(head)); cheap but corpus-size-blind.
/// BM25 re-scores with Okapi BM25 over the per-vertex out-edge
/// distribution (TF = edge weight, DF = distinct tails into head,
/// N = #tails, DocLen = tail out-degree), adding TF saturation,
/// document-length normalization, and a real N-aware IDF — a principled
/// successor to TFIDF that is consistent with full-text SearchVertices.
class Weighting extends $pb.ProtobufEnum {
  static const Weighting WEIGHTING_UNSPECIFIED =
      Weighting._(0, _omitEnumNames ? '' : 'WEIGHTING_UNSPECIFIED');
  static const Weighting WEIGHTING_RAW =
      Weighting._(1, _omitEnumNames ? '' : 'WEIGHTING_RAW');
  static const Weighting WEIGHTING_TFIDF =
      Weighting._(2, _omitEnumNames ? '' : 'WEIGHTING_TFIDF');
  static const Weighting WEIGHTING_BM25 =
      Weighting._(3, _omitEnumNames ? '' : 'WEIGHTING_BM25');

  static const $core.List<Weighting> values = <Weighting>[
    WEIGHTING_UNSPECIFIED,
    WEIGHTING_RAW,
    WEIGHTING_TFIDF,
    WEIGHTING_BM25,
  ];

  static final $core.List<Weighting?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static Weighting? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const Weighting._(super.value, super.name);
}

/// ScanOrder selects the key order a ScanVertices / ScanVertexKeys page walks
/// (#898). SCAN_ORDER_UNSPECIFIED preserves the historical behavior (ascending)
/// so an unset field is a no-op. DESC walks the same prefix range from the high
/// end, so "give me the newest N" on a timestamp-ordered keyspace is a single
/// bounded page instead of a full-prefix scan-then-sort.
class ScanOrder extends $pb.ProtobufEnum {
  static const ScanOrder SCAN_ORDER_UNSPECIFIED =
      ScanOrder._(0, _omitEnumNames ? '' : 'SCAN_ORDER_UNSPECIFIED');
  static const ScanOrder SCAN_ORDER_ASC =
      ScanOrder._(1, _omitEnumNames ? '' : 'SCAN_ORDER_ASC');
  static const ScanOrder SCAN_ORDER_DESC =
      ScanOrder._(2, _omitEnumNames ? '' : 'SCAN_ORDER_DESC');

  static const $core.List<ScanOrder> values = <ScanOrder>[
    SCAN_ORDER_UNSPECIFIED,
    SCAN_ORDER_ASC,
    SCAN_ORDER_DESC,
  ];

  static final $core.List<ScanOrder?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static ScanOrder? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const ScanOrder._(super.value, super.name);
}

/// MatchMode selects how a multi-word query's terms combine when choosing which
/// vertices match (#890). It governs membership only; BM25 still ranks whatever
/// matches. Coverage counts word-channel terms, so requiring "all" of a CJK run
/// means all of its bigrams.
class MatchMode extends $pb.ProtobufEnum {
  /// MATCH_MODE_UNSPECIFIED defers to the server default
  /// (LANTERN_SEARCH_DEFAULT_MODE, itself defaulting to ANY), so an unset field
  /// keeps the OR-union behavior.
  static const MatchMode MATCH_MODE_UNSPECIFIED =
      MatchMode._(0, _omitEnumNames ? '' : 'MATCH_MODE_UNSPECIFIED');

  /// MATCH_MODE_ANY keeps every vertex sharing at least one query term (OR).
  static const MatchMode MATCH_MODE_ANY =
      MatchMode._(1, _omitEnumNames ? '' : 'MATCH_MODE_ANY');

  /// MATCH_MODE_ALL keeps only vertices carrying every query word term (AND).
  static const MatchMode MATCH_MODE_ALL =
      MatchMode._(2, _omitEnumNames ? '' : 'MATCH_MODE_ALL');

  /// MATCH_MODE_MIN_SHOULD keeps vertices carrying at least min_should_match
  /// distinct query word terms.
  static const MatchMode MATCH_MODE_MIN_SHOULD =
      MatchMode._(3, _omitEnumNames ? '' : 'MATCH_MODE_MIN_SHOULD');

  static const $core.List<MatchMode> values = <MatchMode>[
    MATCH_MODE_UNSPECIFIED,
    MATCH_MODE_ANY,
    MATCH_MODE_ALL,
    MATCH_MODE_MIN_SHOULD,
  ];

  static final $core.List<MatchMode?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static MatchMode? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const MatchMode._(super.value, super.name);
}

/// SearchErrorReason is the bounded, machine-readable reason attached to a
/// FAILED_PRECONDITION from SearchVertices. Clients must branch on this enum,
/// never on the human-readable status message.
class SearchErrorReason extends $pb.ProtobufEnum {
  static const SearchErrorReason SEARCH_ERROR_REASON_UNSPECIFIED =
      SearchErrorReason._(
          0, _omitEnumNames ? '' : 'SEARCH_ERROR_REASON_UNSPECIFIED');

  /// buf:lint:ignore ENUM_VALUE_PREFIX -- stable cross-SDK reason code.
  static const SearchErrorReason SEARCH_DISABLED =
      SearchErrorReason._(1, _omitEnumNames ? '' : 'SEARCH_DISABLED');

  /// buf:lint:ignore ENUM_VALUE_PREFIX -- stable cross-SDK reason code.
  static const SearchErrorReason SEARCH_POSITIONS_DISABLED =
      SearchErrorReason._(2, _omitEnumNames ? '' : 'SEARCH_POSITIONS_DISABLED');

  static const $core.List<SearchErrorReason> values = <SearchErrorReason>[
    SEARCH_ERROR_REASON_UNSPECIFIED,
    SEARCH_DISABLED,
    SEARCH_POSITIONS_DISABLED,
  ];

  static final $core.List<SearchErrorReason?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 2);
  static SearchErrorReason? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const SearchErrorReason._(super.value, super.name);
}

/// Direction selects which incident edges count toward a vertex's degree.
class TopVerticesByDegreeRequest_Direction extends $pb.ProtobufEnum {
  /// DIRECTION_UNSPECIFIED defaults to out-degree server-side.
  static const TopVerticesByDegreeRequest_Direction DIRECTION_UNSPECIFIED =
      TopVerticesByDegreeRequest_Direction._(
          0, _omitEnumNames ? '' : 'DIRECTION_UNSPECIFIED');

  /// DIRECTION_OUT counts edges leaving the vertex (the vertex as tail).
  static const TopVerticesByDegreeRequest_Direction DIRECTION_OUT =
      TopVerticesByDegreeRequest_Direction._(
          1, _omitEnumNames ? '' : 'DIRECTION_OUT');

  /// DIRECTION_IN counts edges entering the vertex (the vertex as head).
  static const TopVerticesByDegreeRequest_Direction DIRECTION_IN =
      TopVerticesByDegreeRequest_Direction._(
          2, _omitEnumNames ? '' : 'DIRECTION_IN');

  /// DIRECTION_BOTH counts edges in either direction (out-degree +
  /// in-degree; a self-loop counts twice).
  static const TopVerticesByDegreeRequest_Direction DIRECTION_BOTH =
      TopVerticesByDegreeRequest_Direction._(
          3, _omitEnumNames ? '' : 'DIRECTION_BOTH');

  static const $core.List<TopVerticesByDegreeRequest_Direction> values =
      <TopVerticesByDegreeRequest_Direction>[
    DIRECTION_UNSPECIFIED,
    DIRECTION_OUT,
    DIRECTION_IN,
    DIRECTION_BOTH,
  ];

  static final $core.List<TopVerticesByDegreeRequest_Direction?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 3);
  static TopVerticesByDegreeRequest_Direction? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const TopVerticesByDegreeRequest_Direction._(super.value, super.name);
}

class ReplicationPeer_State extends $pb.ProtobufEnum {
  static const ReplicationPeer_State STATE_UNSPECIFIED =
      ReplicationPeer_State._(0, _omitEnumNames ? '' : 'STATE_UNSPECIFIED');

  /// Pump is dialing or has dialed but Subscribe has not yet
  /// returned a frame.
  static const ReplicationPeer_State STATE_CONNECTING =
      ReplicationPeer_State._(1, _omitEnumNames ? '' : 'STATE_CONNECTING');

  /// Subscribe stream is open and frames have been received.
  static const ReplicationPeer_State STATE_STREAMING =
      ReplicationPeer_State._(2, _omitEnumNames ? '' : 'STATE_STREAMING');

  /// Last session errored; the pump is sleeping before the next
  /// reconnect attempt.
  static const ReplicationPeer_State STATE_BACKOFF =
      ReplicationPeer_State._(3, _omitEnumNames ? '' : 'STATE_BACKOFF');

  /// Per-peer goroutine has exited (typically because DNS discovery
  /// removed the address). Reported transiently — the row drops out
  /// of subsequent snapshots once the supervisor has reaped it.
  static const ReplicationPeer_State STATE_CLOSED =
      ReplicationPeer_State._(4, _omitEnumNames ? '' : 'STATE_CLOSED');

  static const $core.List<ReplicationPeer_State> values =
      <ReplicationPeer_State>[
    STATE_UNSPECIFIED,
    STATE_CONNECTING,
    STATE_STREAMING,
    STATE_BACKOFF,
    STATE_CLOSED,
  ];

  static final $core.List<ReplicationPeer_State?> _byValue =
      $pb.ProtobufEnum.$_initByValueList(values, 4);
  static ReplicationPeer_State? valueOf($core.int value) =>
      value < 0 || value >= _byValue.length ? null : _byValue[value];

  const ReplicationPeer_State._(super.value, super.name);
}

const $core.bool _omitEnumNames =
    $core.bool.fromEnvironment('protobuf.omit_enum_names');
