import 'dart:convert';

import 'package:lantern_client/lantern_client.dart';

import 'errors.dart';

/// One bounded, derived Server observation. Never an entity cache or overlay.
///
/// The owner tag distinguishes live-owner observations from reopened rows. It
/// is bookkeeping only: reopened rows still require an application-certified
/// responder continuation assertion and all authority, epoch and time fences.
final class OfflineTraversalRecord {
  /// Creates an immutable record with original, capped deadlines.
  OfflineTraversalRecord({
    required this.partitionId,
    required this.generation,
    required this.authorityBinding,
    required this.responderId,
    required this.ownerTag,
    required this.queryKey,
    required this.validityEpoch,
    required this.graph,
    required this.observedAt,
    required this.freshUntil,
    required this.historyUntil,
    required this.sourceExpiration,
    required this.securityUntil,
    required this.lastAccessAt,
  }) {
    for (final field in [partitionId, authorityBinding]) {
      if (field.isEmpty || utf8.encode(field).length > 4096) {
        throw const OfflineArgumentException();
      }
    }
    if (generation < 0 ||
        generation > 0x7fffffffffffffff ||
        validityEpoch < 0 ||
        validityEpoch > 0x7fffffffffffffff ||
        responderId.isEmpty ||
        utf8.encode(responderId).length > 512 ||
        !RegExp(r'^[0-9a-f]{32}$').hasMatch(ownerTag) ||
        !queryKey.startsWith('1:') ||
        utf8.encode(queryKey).length > maxBytes ||
        graph.vertices.length > maxVertices ||
        graph.allEdges.length > maxEdges) {
      throw const OfflineArgumentException();
    }
    try {
      final encoded = queryKey.substring(2);
      if (encoded.isEmpty || base64Encode(base64Decode(encoded)) != encoded) {
        throw const OfflineArgumentException();
      }
    } on FormatException {
      throw const OfflineArgumentException();
    }
    for (final time in [
      observedAt,
      freshUntil,
      historyUntil,
      sourceExpiration,
      securityUntil,
      lastAccessAt,
    ]) {
      if (time != null &&
          (time.microsecondsSinceEpoch < 0 ||
              time.microsecondsSinceEpoch > 253402300799999999)) {
        throw const OfflineArgumentException();
      }
    }
    if (sourceExpiration != expirationOf(graph) ||
        freshUntil !=
            deadline(observedAt, freshAge, sourceExpiration, securityUntil) ||
        historyUntil !=
            deadline(observedAt, historyAge, sourceExpiration, securityUntil) ||
        !observedAt.isBefore(freshUntil) ||
        lastAccessAt.isBefore(observedAt)) {
      throw const OfflineArgumentException();
    }
  }

  /// Maximum encoded record bytes, independent of every other store lane.
  static const maxBytes = 1024 * 1024;

  /// Maximum returned vertices in one retained Graph.
  static const maxVertices = 4096;

  /// Maximum returned edges in one retained Graph.
  static const maxEdges = 16384;

  /// Freshness ceiling of an eligible observation.
  static const freshAge = Duration(seconds: 30);

  /// Absolute historical retention ceiling; never reset on decode or access.
  static const historyAge = Duration(seconds: 60);

  /// Stable account/corpus scope, not a Server authorization claim.
  final String partitionId;

  /// Store generation at observation.
  final int generation;

  /// Full nonsecret authority binding at observation.
  final String authorityBinding;

  /// Application-certified actual responder identity.
  final String responderId;

  /// Bounded nonsecret tag of the producing owner lifetime.
  final String ownerTag;

  /// Parent-owned canonical query identity, including every traversal option.
  final String queryKey;

  /// Semantic corpus validity, independent of read/publication races.
  final int validityEpoch;

  /// Exact immutable Server result, including synthetic edges and source TTLs.
  final Graph graph;

  /// Original Server observation time.
  final DateTime observedAt;

  /// Original capped fresh deadline.
  final DateTime freshUntil;

  /// Original capped historical deadline.
  final DateTime historyUntil;

  /// Earliest included source TTL; expires the whole derived Graph.
  final DateTime? sourceExpiration;

  /// Original authority/credential lifetime cap, if known.
  final DateTime? securityUntil;

  /// Local LRU metadata; never extends either deadline.
  final DateTime lastAccessAt;

  /// Copies only local LRU metadata.
  OfflineTraversalRecord accessedAt(DateTime at) => OfflineTraversalRecord(
    partitionId: partitionId,
    generation: generation,
    authorityBinding: authorityBinding,
    responderId: responderId,
    ownerTag: ownerTag,
    queryKey: queryKey,
    validityEpoch: validityEpoch,
    graph: graph,
    observedAt: observedAt,
    freshUntil: freshUntil,
    historyUntil: historyUntil,
    sourceExpiration: sourceExpiration,
    securityUntil: securityUntil,
    lastAccessAt: at,
  );

  /// Earliest known expiration across every included source.
  static DateTime? expirationOf(Graph graph) {
    DateTime? first;
    for (final at in [
      ...graph.vertices.values.map((v) => v.expiration),
      ...graph.allEdges.map((e) => e.expiration),
    ]) {
      if (at != null && (first == null || at.isBefore(first))) first = at;
    }
    return first;
  }

  /// Computes an original age deadline capped by source and security lifetime.
  static DateTime deadline(
    DateTime at,
    Duration age,
    DateTime? source,
    DateTime? security,
  ) {
    var end = at.add(age);
    for (final cap in [source, security]) {
      if (cap != null && cap.isBefore(end)) end = cap;
    }
    return end;
  }
}
